package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mdcng-go/internal/provider"
)

func TestScrapeJobNFOReadsWrittenFile(t *testing.T) {
	s := configureScrapeServer(t)
	dir := t.TempDir()
	output := filepath.Join(dir, "ABP-123.mp4")
	settings, _ := s.scrapeSettings()
	settings.NFO.Enabled = true
	nfo := `<?xml version="1.0" encoding="UTF-8"?>
<movie><title>ABP-123 Title</title><actor><name>Actor A</name></actor><actor><name>Actor B</name></actor><tag>T1</tag><genre>T1</genre><num>ABP-123</num></movie>`
	if err := os.WriteFile(filepath.Join(dir, "ABP-123.nfo"), []byte(nfo), 0600); err != nil {
		t.Fatal(err)
	}
	s.jobs = []scrapeJob{
		{ID: "done", Status: 2, Output: output, Settings: &settings},
		{ID: "running", Status: 1, Output: output, Settings: &settings},
		{ID: "missing", Status: 2, Output: filepath.Join(dir, "XYZ-001.mp4"), Settings: &settings},
	}
	var got recordNFO
	response := call(s, "GET", "/api/scrape/jobs/done/nfo", "")
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &got) != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	if !got.Enabled || !got.Exists || got.Name != "ABP-123.nfo" || got.Fields == nil || got.Raw != nfo || got.Mappings == nil {
		t.Fatalf("%+v", got)
	}
	if got.Fields.Title != "ABP-123 Title" || len(got.Fields.Actors) != 2 || got.Fields.Actors[1] != "Actor B" || got.Fields.Tags[0] != "T1" || got.Fields.Num != "ABP-123" {
		t.Fatalf("%+v", got.Fields)
	}
	for _, id := range []string{"running", "missing"} {
		got = recordNFO{}
		response = call(s, "GET", "/api/scrape/jobs/"+id+"/nfo", "")
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &got) != nil || got.Exists || got.Fields != nil {
			t.Fatal(id, response.Body.String())
		}
	}
	if response := call(s, "GET", "/api/scrape/jobs/none/nfo", ""); response.Code != 404 {
		t.Fatal(response.Code)
	}
}

func TestNFOTagOriginsRecordReplacedResolution(t *testing.T) {
	settings := scrapeSettings{}
	settings.NFO.AdditionalTags.Resolution = true
	settings.NFO.AdditionalTags.SerialName = true
	settings.NFO.AdditionalTags.HackType = true
	meta := provider.Metadata{Number: "IPZZ-034", Tags: []string{"巨乳", "4K", "IPZZ"}}
	trace := nfoTagsWithOrigin(meta, filenameFlags{Resolution: "4K", Mosaic: "is_ltx_hack"}, settings)
	got := []string{}
	for _, entry := range trace.Tags {
		got = append(got, entry.Tag+"="+entry.Origin)
	}
	// 刮削的「4K」被丢弃，按文件分辨率重新生成；重复的「IPZZ」保留首次出现的刮削来源。
	if want := "巨乳=scraped|IPZZ=scraped|4K=resolution|LTX破解=hack_type"; strings.Join(got, "|") != want {
		t.Fatalf("got %v want %s", got, want)
	}
	if len(trace.Dropped) != 1 || trace.Dropped[0] != (nfoTagEntry{Tag: "4K", Origin: "resolution"}) {
		t.Fatalf("dropped=%v", trace.Dropped)
	}
	if strings.Join(nfoAdditionalTags(meta, filenameFlags{Resolution: "4K", Mosaic: "is_ltx_hack"}, settings), "|") != "巨乳|IPZZ|4K|LTX破解" {
		t.Fatal("plain tags differ from traced tags")
	}
}

func TestNFOResolutionTagOnlyWrites4K8K(t *testing.T) {
	settings := scrapeSettings{}
	settings.NFO.AdditionalTags.Resolution = true
	for _, test := range []struct{ resolution, want string }{
		{"4K", "4K"}, {"8K", "8K"}, {"2160P", "4K"}, {"4320P", "8K"},
		{"1080P", ""}, {"720P", ""}, {"", ""},
	} {
		got := strings.Join(nfoAdditionalTags(provider.Metadata{Number: "ABP-123"}, filenameFlags{Resolution: test.resolution}, settings), "|")
		if got != test.want {
			t.Fatalf("resolution %q: got %q want %q", test.resolution, got, test.want)
		}
	}
}

func TestScrapeJobStoresNFOTagOriginsAfterSuccess(t *testing.T) {
	s := configureScrapeServer(t)
	path := filepath.Join(t.TempDir(), "ABP-123.mp4")
	if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	settings, _ := s.scrapeSettings()
	settings.NFO.Enabled = true
	settings.NFO.EnableTag = true
	s.jobs = []scrapeJob{{ID: "tags", Path: path, Number: "ABP-123", Settings: &settings}}
	s.runScrapeJob("tags", 5*time.Second)
	var got recordNFO
	response := call(s, "GET", "/api/scrape/jobs/tags/nfo", "")
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &got) != nil {
		t.Fatal(response.Body.String())
	}
	if !got.Exists || got.Fields == nil || got.TagOrigins == nil || len(got.TagOrigins.Tags) != len(got.Fields.Tags) {
		t.Fatalf("%+v", got)
	}
	for i, entry := range got.TagOrigins.Tags {
		if entry.Tag != got.Fields.Tags[i] || entry.Origin == "" {
			t.Fatalf("trace %v differs from written tags %v", got.TagOrigins.Tags, got.Fields.Tags)
		}
	}
	if list := call(s, "GET", "/api/scrape/jobs", ""); strings.Contains(list.Body.String(), "nfo_tags") {
		t.Fatal("list includes NFO tag trace")
	}
}
