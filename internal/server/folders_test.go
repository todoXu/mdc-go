package server

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func folderRequest(s *Server, address, remote string) *httptest.ResponseRecorder {
	request := httptest.NewRequest("GET", address, nil)
	request.RemoteAddr = remote
	response := httptest.NewRecorder()
	s.ServeHTTP(response, request)
	return response
}

func TestFolderBrowserLocalAndDirectoriesOnly(t *testing.T) {
	if runtime.GOOS == "windows" && !filepath.IsAbs(`\\server\share\Movies`) {
		t.Fatal("UNC path is not absolute on Windows")
	}
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Subfolder"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "private.txt"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	address := "http://127.0.0.1:19208/api/system/folders?path=" + url.QueryEscape(root)
	response := folderRequest(s, address, "127.0.0.1:12345")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var body struct {
		Path    string       `json:"path"`
		Parent  string       `json:"parent"`
		Folders []folderItem `json:"folders"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Path != filepath.Clean(root) || len(body.Folders) != 1 || body.Folders[0].Name != "Subfolder" || body.Parent == "" {
		t.Fatal(body)
	}
	// 开放到局域网后，其他电脑打开的页面也可以浏览目录。
	if response := folderRequest(s, address, "192.0.2.1:12345"); response.Code != 200 {
		t.Fatal("remote listing rejected", response.Code, response.Body.String())
	}
	// 其他网站发起的跨站请求仍然拒绝。
	for header, value := range map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"} {
		request := httptest.NewRequest("GET", address, nil)
		request.RemoteAddr = "127.0.0.1:12345"
		request.Header.Set(header, value)
		response := httptest.NewRecorder()
		s.ServeHTTP(response, request)
		if response.Code != 403 {
			t.Fatalf("cross-site request allowed via %s: %d", header, response.Code)
		}
	}
	if response := folderRequest(s, "http://127.0.0.1:19208/api/system/folders?path=relative", "127.0.0.1:12345"); response.Code != 400 {
		t.Fatal("relative path accepted")
	}
	if response := folderRequest(s, "http://127.0.0.1:19208/api/system/folders", "127.0.0.1:12345"); response.Code != 200 {
		t.Fatal("drive listing failed", response.Code)
	}
}

func TestOrganizeSettingsPreserveOtherFields(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 用当前系统的完整路径：Windows 为 C:\…，Linux 为 /…。
	target, _ := json.Marshal(filepath.Join(t.TempDir(), "Media"))
	body := `{"target_folder":` + string(target) + `,"link_mode":2,"delete_metadata_folder_when_failed":true,"delete_empty_parent_after_move":true,"file_size_filter":100,"file_ext_filter":["mp4","mkv"],"file_name_blacklist":["sample"],"jav_hack_keywords":["JAV"],"lada_hack_keywords":["LADA"],"ltx_hack_keywords":["LTX"]}`
	if w := call(s, "PUT", "/api/config/common", body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	s, err = New(dir)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(call(s, "GET", "/api/config/common", "").Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved["target_folder_metadata"] != nil || saved["delete_metadata_folder_when_failed"] != true || saved["other_hack_keywords"] == nil || saved["delete_empty_parent_after_move"] != true || saved["file_size_filter"] != float64(100) {
		t.Fatal(saved)
	}
	for key, want := range map[string]string{"file_ext_filter": "mp4", "file_name_blacklist": "sample", "jav_hack_keywords": "JAV", "lada_hack_keywords": "LADA", "ltx_hack_keywords": "LTX"} {
		list, ok := saved[key].([]any)
		if !ok || len(list) == 0 || list[0] != want {
			t.Fatal(key, saved[key])
		}
	}
}
