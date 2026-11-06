package provider

import (
	"archive/zip"
	"bytes"
	"errors"
	"testing"
)

func zipFiles(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for name, body := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

const exampleManifest = `{"api_version":2,"id":"Example","name":"Example","version":"1.0","engine":"javascript-v1","base_url":"https://example.com"}`
const exampleScript = `var plugin={test:function(){},fetch:function(){},image:function(){}};`

func TestPackageLifecycle(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !IsBuiltin("Javbus") || !IsBuiltin("avbase") || IsBuiltin("Example") {
		t.Fatal("built-in classification")
	}
	if _, err := r.ExportZIP("Javbus"); err != nil {
		t.Fatal("built-in download failed", err)
	}
	var source *SourceError
	if err := r.Remove("Javbus"); !errors.As(err, &source) || source.Code != "builtin_plugin" {
		t.Fatal("built-in removal accepted", err)
	}
	if _, err := r.Get("Javbus"); err != nil {
		t.Fatal("built-in disappeared", err)
	}
	pack := zipFiles(t, map[string]string{"Example/plugin.json": exampleManifest, "Example/main.js": exampleScript})
	if _, err := r.InstallZIP(pack); err != nil {
		t.Fatal(err)
	}
	archive, err := r.ExportZIP("Example")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Remove("Example"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("Example"); err == nil {
		t.Fatal("third-party plugin still available")
	}
	r, err = New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("Example"); err == nil {
		t.Fatal("third-party plugin reinstalled after deletion")
	}
	if _, err := r.InstallZIP(archive); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("Example"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.InstallZIP(archive); err == nil {
		t.Fatal("duplicate plugin accepted")
	}
}

func TestRejectUnsafePackage(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, files := range map[string]map[string]string{
		"traversal": {"../plugin.json": exampleManifest, "main.js": exampleScript},
		"mixed":     {"plugin.json": exampleManifest, "Example/main.js": exampleScript},
		"extra":     {"plugin.json": exampleManifest, "main.js": exampleScript, "evil.exe": "x"},
		"syntax":    {"plugin.json": exampleManifest, "main.js": "var plugin = {"},
		"folder":    {"Wrong/plugin.json": exampleManifest, "Wrong/main.js": exampleScript},
		"builtin":   {"Javbus/plugin.json": `{"api_version":2,"id":"Javbus","name":"changed","version":"1","engine":"javascript-v1","base_url":"https://example.com"}`, "Javbus/main.js": exampleScript},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := r.InstallZIP(zipFiles(t, files)); err == nil {
				t.Fatal("unsafe package accepted")
			}
		})
	}
	if _, err := r.Get("Example"); err == nil {
		t.Fatal("invalid plugin installed")
	}
}
