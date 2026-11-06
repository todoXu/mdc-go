package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func call(s *Server, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func TestObservedReadContracts(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name := range s.defaults {
		if w := call(s, "GET", "/api/config/"+name, ""); w.Code != 200 {
			t.Fatal(name, w.Code)
		}
		if w := call(s, "PUT", "/api/config/"+name, string(s.defaults[name])); w.Code != 200 {
			t.Fatalf("config round trip %s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if w := call(s, "GET", "/api/system/monitor", ""); w.Code != 200 || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("monitor: %d %s", w.Code, w.Body.String())
	}
}

func TestConfigPersistenceAndValidation(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"3", "4"} {
		w := call(s, "PUT", "/api/config/system", `{"scrape_thread_count":`+n+`}`)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	reloaded, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got object
	json.Unmarshal(call(reloaded, "GET", "/api/config/system", "").Body.Bytes(), &got)
	if got["scrape_thread_count"] != float64(4) || got["scrape_timeout"] == nil {
		t.Fatal(got)
	}
	before := call(s, "GET", "/api/config/system", "").Body.String()
	for _, body := range []string{`null`, `[]`, `{`, `{} {}`, `{"scrape_thread_count":"bad"}`, `{"unknown":1}`} {
		w := call(s, "PUT", "/api/config/system", body)
		if w.Code != 400 {
			t.Fatalf("accepted %s: %d", body, w.Code)
		}
		if call(s, "GET", "/api/config/system", "").Body.String() != before {
			t.Fatal("invalid update changed config")
		}
	}
	w := call(s, "PUT", "/api/config/actress", `{"emby_config":{"user_id":"test"}}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := call(s, "PUT", "/api/config/actress", `{"emby_config":{"user_id":null}}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = call(s, "PUT", "/api/config/naming", `{"suffixs":{"has_subtitle":"TEST"}}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

func TestSystemAndUserSettings(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct {
		section string
		body    string
		status  int
	}{
		{"system", `{"scrape_thread_count":8,"scrape_timeout":180}`, 200},
		{"system", `{"scrape_thread_count":0}`, 400},
		{"user", `{"username":"测试用户"}`, 200},
		{"user", `{"username":""}`, 400},
		{"user", `{"avatar":"data:image/png;base64,AA=="}`, 400},
	} {
		response := call(s, "PUT", "/api/config/"+request.section, request.body)
		if response.Code != request.status {
			t.Fatalf("%s %s: %d %s", request.section, request.body, response.Code, response.Body.String())
		}
	}
	reloaded, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	var user object
	if err := json.Unmarshal(call(reloaded, "GET", "/api/config/user", "").Body.Bytes(), &user); err != nil || user["username"] != "测试用户" {
		t.Fatalf("user config did not persist: %v %v", user, err)
	}
}

func TestEmbyNewActorSettings(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if response := call(s, "PUT", "/api/config/actress", `{"emby_config":{"auto_scrape_new_actors":true}}`); response.Code != 400 {
		t.Fatalf("accepted automatic scrape without connection: %d", response.Code)
	}
	body := `{"enabled":true,"emby_config":{"server_address":"http://127.0.0.1:8096/","api_key":"test-key","auto_scrape_new_actors":true}}`
	if response := call(s, "PUT", "/api/config/actress", body); response.Code != 200 {
		t.Fatalf("could not save Emby settings: %d %s", response.Code, response.Body.String())
	}
	reloaded, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Emby struct {
			AutoScrape bool `json:"auto_scrape_new_actors"`
		} `json:"emby_config"`
	}
	if err := json.Unmarshal(call(reloaded, "GET", "/api/config/actress", "").Body.Bytes(), &saved); err != nil || !saved.Emby.AutoScrape {
		t.Fatalf("Emby setting did not persist: %v %v", saved, err)
	}
}

func TestEmbyLibrarySelection(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Emby-Token") != "test-key" {
			t.Errorf("missing Emby API key")
		}
		switch r.URL.Path {
		case "/Library/VirtualFolders/Query":
			_, _ = w.Write([]byte(`{"Items":[{"ItemId":"library-1","Name":"电影","CollectionType":"movies"}]}`))
		case "/Users/user-1/Views":
			_, _ = w.Write([]byte(`{"Items":[{"Id":"library-2","Name":"用户媒体库"}]}`))
		default:
			t.Errorf("unexpected Emby path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		userID string
		wantID string
	}{
		{"", "library-1"},
		{"user-1", "library-2"},
	} {
		userID := `null`
		if check.userID != "" {
			userID = `"` + check.userID + `"`
		}
		body := `{"emby_config":{"server_address":"` + remote.URL + `","api_key":"test-key","user_id":` + userID + `}}`
		if response := call(s, "PUT", "/api/config/actress", body); response.Code != 200 {
			t.Fatalf("save Emby connection: %d %s", response.Code, response.Body.String())
		}
		response := call(s, "GET", "/api/emby/libraries", "")
		if response.Code != 200 {
			t.Fatalf("load libraries: %d %s", response.Code, response.Body.String())
		}
		var result struct {
			Data []embyLibrary `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result.Data) != 1 || result.Data[0].ID != check.wantID {
			t.Fatalf("wrong libraries: %v %v", result, err)
		}
	}
}

func TestFailedSaveDoesNotChangeMemory(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	before := call(s, "GET", "/api/config/system", "").Body.String()
	s.statePath = filepath.Join(blocker, "config.json")
	if w := call(s, "PUT", "/api/config/system", `{"scrape_thread_count":7}`); w.Code != 500 {
		t.Fatal(w.Code)
	}
	if call(s, "GET", "/api/config/system", "").Body.String() != before {
		t.Fatal("failed save changed memory")
	}
}

func TestMissingRoutes(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// 原 mdcng 的接口已经移除，访问时与其它不存在的地址一样返回 404。
	for _, path := range []string{"/api/unknown", "/api/config/unknown", "/api/tasks", "/api/tasks/1", "/api/config/naming/variables"} {
		if w := call(s, "GET", path, ""); w.Code != http.StatusNotFound {
			t.Fatal(path, w.Code)
		}
	}
	if w := call(s, "POST", "/api/version", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
}
