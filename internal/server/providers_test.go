package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProviderAPI(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<h3>TEST-123 Example</h3><div class="info"><p><span class="header">識別碼:</span>TEST-123</p></div><a class="movie-box"></a>`))
	}))
	defer upstream.Close()
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(s, "GET", "/api/providers", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	config := `{"base_url":"` + upstream.URL + `","enabled":true}`
	if w := call(s, "PUT", "/api/providers/Javbus/config", config); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	s, err = New(dir)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	json.Unmarshal(call(s, "GET", "/api/providers/Javbus/config", "").Body.Bytes(), &saved)
	if saved["base_url"] != upstream.URL {
		t.Fatal(saved)
	}
	if w := call(s, "POST", "/api/providers/Javbus/test", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := call(s, "POST", "/api/providers/Javbus/fetch", `{"number":"TEST-123"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, body := range []string{`null`, `{"base_url":"javascript:bad"}`, `{"cooldown_seconds":-1}`, `{"unknown":true}`} {
		if w := call(s, "PUT", "/api/providers/Javbus/config", body); w.Code != 400 {
			t.Fatal(body, w.Code)
		}
	}
	if w := call(s, "POST", "/api/providers/missing/test", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
}
