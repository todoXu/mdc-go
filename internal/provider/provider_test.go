package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Synthetic page with the structure expected by the JavBus plugin. No live site
// content or account cookies are included in fixtures.
const fixture = `<html><h3>ABP-123 Example title</h3><a class="bigImage"><img src="/covers/example.jpg"></a><div class="info"><p><span class="header">識別碼:</span><span>ABP-123</span></p><p><span class="header">發行日期:</span>2020-01-02</p><p><span class="header">長度:</span>120分鐘</p><p><span class="header">導演:</span><a>Example director</a></p><p><span class="header">製作商:</span><a>Example studio</a></p><p><span class="header">發行商:</span><a>Example publisher</a></p><p><span class="header">系列:</span><a>Example series</a></p></div><span class="star-name"><a>Example actor</a><a>Example actor</a></span><span class="genre"><a href="/genre/test">Example tag</a></span><a class="sample-box" href="/samples/example.jpg"></a></html>`

func newSource(t *testing.T) (*Registry, Source) {
	t.Helper()
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.Get("Javbus")
	if err != nil {
		t.Fatal(err)
	}
	return r, p
}
func TestFetchMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ABP-123" || r.Header.Get("Cookie") != "test=value" || r.Header.Get("Accept-Language") != "zh-CN,zh;q=0.9,en;q=0.8" {
			t.Error("request contract", r.URL.Path)
		}
		w.Write([]byte(fixture))
	}))
	defer server.Close()
	_, p := newSource(t)
	metadata, err := p.Fetch(context.Background(), Config{BaseURL: server.URL, Cookies: "test=value"}, "abp_123")
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Number != "ABP-123" || metadata.Title != "Example title" || metadata.OriginalTitle != "Example title" || metadata.Studio != "Example studio" || metadata.Release != "2020-01-02" || metadata.Cover != server.URL+"/covers/example.jpg" || len(metadata.Actors) != 1 || len(metadata.Samples) != 1 || len(metadata.Tags) != 1 {
		t.Fatalf("%+v", metadata)
	}
}
func TestSourceFailures(t *testing.T) {
	for _, item := range []struct {
		name       string
		status     int
		body, code string
	}{
		{"verification", 200, `<title>Just a moment</title>`, "verification_required"},
		{"forbidden", 403, "", "verification_required"},
		{"missing", 404, "", "not_found"},
		{"unexpected", 200, `<h3>Search results</h3>`, "parse_error"},
		{"mismatch", 200, strings.ReplaceAll(fixture, "ABP-123", "ABP-124"), "number_mismatch"},
	} {
		t.Run(item.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(item.status); w.Write([]byte(item.body)) }))
			defer upstream.Close()
			_, p := newSource(t)
			_, err := p.Fetch(context.Background(), Config{BaseURL: upstream.URL}, "ABP-123")
			var source *SourceError
			if !errors.As(err, &source) || source.Code != item.code {
				t.Fatal(err)
			}
		})
	}
}
func TestPluginsReloadAndValidation(t *testing.T) {
	r, _ := newSource(t)
	b, err := os.ReadFile(filepath.Join(r.Dir, "Javbus", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	json.Unmarshal(b, &m)
	m.ID = "Example"
	m.Name = "Example"
	encoded, _ := json.Marshal(m)
	if err := os.Mkdir(filepath.Join(r.Dir, "Example"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Dir, "Example", "plugin.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Dir, "Example", "main.js"), []byte(`var plugin = {test:function(){return true}, fetch:function(){return {}}, image:function(){return {}}};`), 0600); err != nil {
		t.Fatal(err)
	}
	items, err := r.List()
	// 内置插件：Javbus、avbase、javdb、javdbui、dmm，外加上面手工创建的 Example。
	if err != nil || len(items) != 6 {
		t.Fatal(items, err)
	}
	m.Engine = "html-v1"
	encoded, _ = json.Marshal(m)
	os.WriteFile(filepath.Join(r.Dir, "Example", "plugin.json"), encoded, 0600)
	if _, err := r.List(); err == nil {
		t.Fatal("unsupported engine accepted")
	}
}
func TestInvalidNumberAndProbe(t *testing.T) {
	for _, number := range []string{"../etc", "https://example.com", "A?x=1", "", "a/b"} {
		if _, err := NormalizeNumber(number); err == nil {
			t.Fatal(number)
		}
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`<a class="movie-box"></a>`)) }))
	defer upstream.Close()
	_, p := newSource(t)
	if err := p.Probe(context.Background(), Config{BaseURL: upstream.URL}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Fetch(ctx, Config{BaseURL: upstream.URL}, "ABP-123"); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestScriptReloadOptionsAndInterrupt(t *testing.T) {
	r, p := newSource(t)
	path := filepath.Join(r.Dir, "Javbus", "main.js")
	code := `var plugin = {test:function(){return true},fetch:function(input){return {number:input,title:config.Options.label,source_url:config.BaseURL + "/x"}},image:function(){return {}}};`
	if err := os.WriteFile(path, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	c := Config{BaseURL: "https://example.com", Options: map[string]any{"label": "First"}}
	m, err := p.Fetch(t.Context(), c, "TEST-123")
	if err != nil || m.Title != "First" {
		t.Fatal(m, err)
	}
	c.Options["label"] = "Second"
	m, err = p.Fetch(t.Context(), c, "TEST-123")
	if err != nil || m.Title != "Second" {
		t.Fatal(m, err)
	}
	if err := os.WriteFile(path, []byte(`var plugin = {fetch:function(){while(true){}},test:function(){},image:function(){}};`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	_, err = p.Fetch(ctx, c, "TEST-123")
	var source *SourceError
	if !errors.As(err, &source) || source.Code != "timeout" {
		t.Fatal(err)
	}
}
