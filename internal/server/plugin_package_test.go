package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"testing"
)

func examplePackage(t *testing.T) string {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, contents := range map[string]string{
		"Example/plugin.json": `{"api_version":2,"id":"Example","name":"Example","version":"1.0","engine":"javascript-v1","base_url":"https://example.com"}`,
		"Example/main.js":     `var plugin={test:function(){},fetch:function(){},image:function(){}};`,
	} {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.String()
}

func TestPluginPackageAPIAndSavedPreferences(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Data []struct {
			ID      string `json:"id"`
			Builtin bool   `json:"builtin"`
		} `json:"data"`
	}
	if err := json.Unmarshal(call(s, "GET", "/api/providers", "").Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	for _, item := range list.Data {
		if !item.Builtin {
			t.Fatal("default plugin not classified as built-in", item)
		}
	}
	if w := call(s, "GET", "/api/providers/Javbus/package", ""); w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatal(w.Code)
	}
	if w := call(s, "DELETE", "/api/providers/Javbus", ""); w.Code != 403 {
		t.Fatal("built-in deletion accepted", w.Code)
	}
	pack := examplePackage(t)
	if w := call(s, "POST", "/api/providers/install", pack); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call(s, "PUT", "/api/providers/Example/config", `{"enabled":true}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := call(s, "PUT", "/api/providers/priorities", `{"by_fields":{"title":["Example"]}}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	packageResponse := call(s, "GET", "/api/providers/Example/package", "")
	if packageResponse.Code != 200 {
		t.Fatal(packageResponse.Code)
	}
	if w := call(s, "DELETE", "/api/providers/Example", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call(s, "GET", "/api/providers/Example/config", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	var priorities struct {
		Config struct {
			ByFields map[string][]string `json:"by_fields"`
		} `json:"config"`
	}
	if err := json.Unmarshal(call(s, "GET", "/api/providers/priorities", "").Body.Bytes(), &priorities); err != nil || len(priorities.Config.ByFields["title"]) != 0 {
		t.Fatal(priorities, err)
	}
	s, err = New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(s, "POST", "/api/providers/install", packageResponse.Body.String()); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call(s, "POST", "/api/providers/install", packageResponse.Body.String()); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	var config struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(call(s, "GET", "/api/providers/Example/config", "").Body.Bytes(), &config); err != nil || !config.Enabled {
		t.Fatal(config, err)
	}
	if err := json.Unmarshal(call(s, "GET", "/api/providers/priorities", "").Body.Bytes(), &priorities); err != nil || len(priorities.Config.ByFields["title"]) != 1 {
		t.Fatal(priorities, err)
	}
}
