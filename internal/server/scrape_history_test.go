package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"mdcng-go/internal/provider"
)

func TestScrapeHistoryKeepsRecordAndRefreshesFilenameFlags(t *testing.T) {
	s := configureScrapeServer(t)
	settings, _ := s.scrapeSettings()
	settings.Common.LTX = []string{"LTX"}
	s.jobs = []scrapeJob{{ID: "saved", Sequence: 7, Status: 2, Path: "ABP-123-LTX-中字.mp4", Settings: &settings, Metadata: &provider.Metadata{Number: "ABP-123"}, Logs: []scrapeEvent{{Stage: "处理结果", Message: "完成"}}}, {ID: "interrupted", Sequence: 8, Status: 1, Path: "ABP-124.mp4"}}
	s.saveScrapeHistory()
	if _, err := os.Stat(s.scrapeHistoryPath()); err != nil {
		t.Fatalf("SQLite history was not created: %v", err)
	}
	restored, err := New(filepath.Dir(s.statePath))
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.jobs) != 2 || restored.jobSequence != 8 {
		t.Fatal("lost records")
	}
	job := restored.jobs[0]
	if job.ID != "saved" || job.Status != 2 || !job.HasSubtitle || job.FileInfo.Mosaic != "is_ltx_hack" || job.Settings == nil || len(job.Logs) != 1 {
		t.Fatalf("lost record state: %+v", job)
	}
	if restored.jobs[1].Status != -2 || restored.jobs[1].Error == "" {
		t.Fatal("interrupted job should be marked stopped")
	}
	if response := call(restored, "DELETE", "/api/scrape/jobs/saved", ""); response.Code != 200 {
		t.Fatal(response.Code)
	}
	again, err := New(filepath.Dir(s.statePath))
	if err != nil {
		t.Fatal(err)
	}
	if len(again.jobs) != 1 || again.jobs[0].ID != "interrupted" {
		t.Fatal("deleted record reappeared")
	}
}

func TestScrapeHistoryIgnoresLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	legacy := `[{"job":{"id":"legacy","sequence":1,"path":"LEGACY-001.mp4","status":2}}]`
	if err := os.WriteFile(filepath.Join(dir, "scrape-history.json"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.jobs) != 0 {
		t.Fatalf("legacy JSON was imported: %+v", s.jobs)
	}
	if _, err := os.Stat(filepath.Join(dir, "scrape-history.db")); err != nil {
		t.Fatalf("SQLite history was not initialized: %v", err)
	}
}

func TestScrapeDatabaseStatus(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	response := call(s, "GET", "/api/system/database", "")
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var status struct {
		Backend       string `json:"backend"`
		Path          string `json:"path"`
		Records       int64  `json:"record_count"`
		Size          int64  `json:"size_bytes"`
		ConfigStorage string `json:"config_storage"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Backend != "sqlite" || status.Path != s.scrapeHistoryPath() || status.Records != 0 || status.Size <= 0 || status.ConfigStorage != "json" {
		t.Fatalf("%+v", status)
	}
}
