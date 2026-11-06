package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPrioritySettingsPersistAndValidate(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 默认配置会启用内置数据源，这里先停用 Javbus，验证未启用的数据源会被拒绝。
	if w := call(s, "PUT", "/api/providers/Javbus/config", `{"enabled":false}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{"/api/providers/Javbus/test", "/api/providers/Javbus/fetch"} {
		if w := call(s, "POST", path, `{"number":"TEST-123"}`); w.Code != 400 {
			t.Fatal("disabled source accepted", path, w.Code)
		}
	}
	if w := call(s, "PUT", "/api/providers/priorities", `{"by_fields":{"title":["Javbus"]}}`); w.Code != 400 {
		t.Fatal("accepted disabled source", w.Code)
	}
	call(s, "PUT", "/api/providers/Javbus/config", `{"enabled":true}`)
	body := `{"by_fields":{"title":["Javbus"],"actors":[]}}`
	if w := call(s, "PUT", "/api/providers/priorities", body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	s, err = New(dir)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Config struct {
			ByFields map[string][]string `json:"by_fields"`
		} `json:"config"`
	}
	json.Unmarshal(call(s, "GET", "/api/providers/priorities", "").Body.Bytes(), &response)
	if response.Config.ByFields["title"][0] != "Javbus" {
		t.Fatal(response)
	}
	for _, bad := range []string{`null`, `{"global":["missing"]}`, `{"global":["Javbus","Javbus"]}`, `{"by_fields":{"unknown":["Javbus"]}}`, `{"by_fields":{"title":["Javbus","Javbus"]}}`} {
		if w := call(s, "PUT", "/api/providers/priorities", bad); w.Code != 400 {
			t.Fatal(bad, w.Code)
		}
	}
	if w := call(s, "POST", "/api/providers/fetch", `{"number":"../invalid"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	call(s, "PUT", "/api/providers/Javbus/config", `{"enabled":false}`)
	json.Unmarshal(call(s, "GET", "/api/providers/priorities", "").Body.Bytes(), &response)
	if len(response.Config.ByFields["title"]) != 0 {
		t.Fatal("disabled source still offered")
	}
	if w := call(s, "POST", "/api/providers/fetch", `{"number":"TEST-123"}`); w.Code != 502 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestPriorityQueryAPI(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<h3>TEST-123 Example</h3><div class="info"><p><span class="header">識別碼:</span>TEST-123</p><p><span class="header">製作商:</span>Studio</p></div>`))
	}))
	defer upstream.Close()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := `{"enabled":true,"base_url":"` + upstream.URL + `"}`
	if w := call(s, "PUT", "/api/providers/Javbus/config", config); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := call(s, "PUT", "/api/providers/priorities", `{"by_fields":{"title":["Javbus"]}}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w := call(s, "POST", "/api/providers/fetch", `{"number":"TEST-123"}`)
	var result struct {
		Metadata struct {
			Title  string `json:"title"`
			Studio string `json:"studio"`
		} `json:"metadata"`
		Provenance map[string]string `json:"provenance"`
	}
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Metadata.Title != "Example" || result.Metadata.Studio != "" || result.Provenance["title"] != "Javbus" || result.Provenance["studio"] != "" {
		t.Fatal(result)
	}
}
