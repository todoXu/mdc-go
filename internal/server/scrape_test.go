package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mdcng-go/internal/provider"
)

func configureScrapeServer(t *testing.T) *Server {
	t.Helper()
	dataDir := t.TempDir()
	s, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.scrapeSlots.acquire(1); s.scrapeSlots.release() })
	script := `var plugin = {
  test: function () { return true; },
  fetch: function (input) {
    return {
      number: input,
      title: "Example Title",
      source_url: "https://example.com/items/" + input,
      actors: ["Example Actor"],
      release: "2020-01-02",
      studio: "Example Studio",
      tags: ["Example Tag"]
    };
  },
  image: function () { return {}; }
};`
	if err := os.WriteFile(filepath.Join(dataDir, "plugins", "Javbus", "main.js"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.providerConfigs["Javbus"] = provider.Config{Enabled: true, BaseURL: "https://example.com"}
	s.priorities = &provider.Priorities{ByFields: map[string][]string{
		"title":   {"Javbus"},
		"actors":  {"Javbus"},
		"release": {"Javbus"},
		"studio":  {"Javbus"},
		"tags":    {"Javbus"},
	}}
	s.mu.Unlock()
	for _, update := range []struct {
		section string
		body    string
	}{
		{"common", `{"link_mode":3,"file_size_filter":0,"file_ext_filter":["mp4"],"file_name_blacklist":[]}`},
		{"download", `{"poster":false,"thumb":false,"fanart":false}`},
		{"naming", `{"file":"{number} {title}","suffix":"{mosaic}{subtitle}","image_with_prefix":true}`},
		{"nfo", `{"enabled":true}`},
		{"system", `{"scrape_thread_count":2,"scrape_timeout":10}`},
	} {
		response := call(s, "PUT", "/api/config/"+update.section, update.body)
		if response.Code != 200 {
			t.Fatalf("configure %s: %d %s", update.section, response.Code, response.Body.String())
		}
	}
	return s
}

func TestDetectNumber(t *testing.T) {
	for _, test := range []struct {
		name string
		want string
	}{
		{"ABP-123.mp4", "ABP-123"},
		{"prefix_abp123_1080p.mkv", "ABP-123"},
		{"FC2-PPV-123456.mp4", "FC2-PPV-123456"},
		{"no-number.mp4", ""},
	} {
		if got := detectNumber(test.name); got != test.want {
			t.Errorf("detectNumber(%q) = %q, want %q", test.name, got, test.want)
		}
	}
}

func TestEligibleFile(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "ABP-123.mp4")
	blocked := filepath.Join(dir, "ABP-124-sample.mp4")
	if err := os.WriteFile(video, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	var settings scrapeSettings
	settings.Common.Extensions = []string{"mp4"}
	settings.Common.Blacklist = []string{"sample"}
	settings.Common.MinSizeMB = 5.0 / 1048576
	if !eligibleFile(video, settings) || eligibleFile(blocked, settings) {
		t.Fatal("file filters did not apply")
	}
	settings.Common.MinSizeMB = 6.0 / 1048576
	if eligibleFile(video, settings) {
		t.Fatal("minimum size was ignored")
	}
}

func TestBuildVideoStem(t *testing.T) {
	var settings scrapeSettings
	settings.Naming.File = "{number} {title}"
	settings.Naming.Suffix = "{mosaic}{subtitle}"
	settings.Naming.Suffixes = map[string]any{
		"is_censored":  "",
		"is_jav_hack":  "-JAV",
		"has_subtitle": "-CHS",
	}
	settings.Common.JAV = []string{"破解"}
	settings.Common.Subtitle = testSubtitleKeywords
	meta := provider.Metadata{Number: "ABP-123", Title: "Example", Actors: []string{"A"}}
	got, err := buildVideoStem("ABP-123-破解-中字.mp4", meta, settings)
	if err != nil || got != "ABP-123 Example-JAV-CHS" {
		t.Fatalf("unexpected stem %q: %v", got, err)
	}
	settings.Naming.Suffix = "{mosaic}{subtitle}{resolution}"
	settings.Naming.Suffixes["resolution_naming"] = "-{resolution_text}"
	settings.Naming.Suffixes["resolution_uhd"] = true
	got, err = buildVideoStem("ABP-123-破解-中字-4K.mp4", meta, settings)
	if err != nil || got != "ABP-123 Example-JAV-CHS-4K" {
		t.Fatalf("unexpected resolution stem %q: %v", got, err)
	}
	settings.Naming.Suffixes["resolution_uhd"] = false
	got, err = buildVideoStem("ABP-123-破解-中字-4K.mp4", meta, settings)
	if err != nil || got != "ABP-123 Example-JAV-CHS" {
		t.Fatalf("disabled resolution suffix still written %q: %v", got, err)
	}
	settings.Naming.File = "CON"
	settings.Naming.Suffix = ""
	if _, err := buildVideoStem("ABP-123.mp4", meta, settings); err == nil {
		t.Fatal("accepted a reserved Windows file name")
	}
	settings.Naming.File = "{title}"
	meta.Title = `bad:name`
	if _, err := buildVideoStem("ABP-123.mp4", meta, settings); err == nil {
		t.Fatal("accepted an invalid Windows file name")
	}
}

func TestOrganizeInPlace(t *testing.T) {
	s := configureScrapeServer(t)
	settings, err := s.scrapeSettings()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	original := filepath.Join(dir, "ABP-123.mp4")
	if err := os.WriteFile(original, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	var saved provider.Metadata
	output, warnings, err := s.organizeInPlaceWithSettings(context.Background(), original, "", settings, func(meta provider.Metadata) { saved = meta })
	if err != nil || len(warnings) != 0 {
		t.Fatalf("organize failed: %v, warnings=%v", err, warnings)
	}
	wantVideo := filepath.Join(dir, "ABP-123 Example Title.mp4")
	if output != wantVideo {
		t.Fatalf("output = %q, want %q", output, wantVideo)
	}
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Fatalf("original video still exists: %v", err)
	}
	for _, path := range []string{wantVideo, filepath.Join(dir, "ABP-123 Example Title.nfo")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing output %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "ABP-123 Example Title.mdc.json")); !os.IsNotExist(err) {
		t.Fatal("legacy metadata file was created")
	}
	if len(saved.Actors) != 1 || saved.Actors[0] != "Example Actor" {
		t.Fatalf("invalid metadata: %+v", saved)
	}
	nfo, err := os.ReadFile(filepath.Join(dir, "ABP-123 Example Title.nfo"))
	if err != nil || !strings.Contains(string(nfo), "<title>ABP-123 Example Title</title>") {
		t.Fatalf("invalid NFO: %v %s", err, nfo)
	}
}

func TestWatcherQueuesScrapeJobAndMonitor(t *testing.T) {
	s := configureScrapeServer(t)
	dir := t.TempDir()
	video := filepath.Join(dir, "ABP-124.mp4")
	if err := os.WriteFile(video, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	s.scanWatchDirectories(context.Background(), watchConfig{Enabled: true, IntervalSeconds: 30, Directories: []watchDirectory{{ID: "auto", Path: dir, Enabled: true, UseGlobalConfig: true, UseGlobalDownload: true}}})
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.jobsMu.RLock()
		done := len(s.jobs) == 1 && (s.jobs[0].Status == 2 || s.jobs[0].Status == -1)
		job := scrapeJob{}
		if len(s.jobs) == 1 {
			job = s.jobs[0]
		}
		s.jobsMu.RUnlock()
		if done {
			if job.Status != 2 {
				t.Fatalf("job failed: %s", job.Error)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	list := call(s, "GET", "/api/scrape/jobs", "")
	if list.Code != 200 || !strings.Contains(list.Body.String(), `"total_count":1`) {
		t.Fatalf("list jobs: %d %s", list.Code, list.Body.String())
	}
	var records struct {
		Data []scrapeJob `json:"data"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &records); err != nil {
		t.Fatal(err)
	}
	record := records.Data[0]
	if record.Sequence != 1 || record.Number != "ABP-124" || record.Directory != dir || record.DirectoryID != "auto" || record.Source != "watcher" || record.LinkMode != 3 {
		t.Fatalf("missing record context: %+v", record)
	}
	if len(record.Actors) != 1 || record.Actors[0] != "Example Actor" || record.HasSubtitle {
		t.Fatalf("invalid record metadata: %+v", record)
	}
	started, startErr := time.Parse(time.RFC3339Nano, record.StartedAt)
	completed, completeErr := time.Parse(time.RFC3339Nano, record.CompletedAt)
	if startErr != nil || completeErr != nil || completed.Before(started) || record.DurationMS != completed.Sub(started).Milliseconds() {
		t.Fatalf("invalid record timing: %+v", record)
	}
	monitor := call(s, "GET", "/api/system/monitor", "")
	if monitor.Code != 200 || !strings.Contains(monitor.Body.String(), `"success":1`) {
		t.Fatalf("monitor: %d %s", monitor.Code, monitor.Body.String())
	}
	s.scanWatchDirectories(context.Background(), watchConfig{Enabled: true, IntervalSeconds: 30, Directories: []watchDirectory{{ID: "auto", Path: dir, Enabled: true, UseGlobalConfig: true, UseGlobalDownload: true}}})
	s.jobsMu.RLock()
	jobCount := len(s.jobs)
	s.jobsMu.RUnlock()
	if jobCount != 1 {
		t.Fatalf("organized video was queued again: %d jobs", jobCount)
	}
}

func TestManualScrapeEndpointRemoved(t *testing.T) {
	s := configureScrapeServer(t)
	response := call(s, "POST", "/api/scrape/jobs", `{}`)
	if response.Code != 405 {
		t.Fatalf("manual scrape endpoint still accepts POST: %d %s", response.Code, response.Body.String())
	}
}

func TestFailedScrapeRecordKeepsContext(t *testing.T) {
	s := configureScrapeServer(t)
	path := filepath.Join(t.TempDir(), "ABP-123-中字.mp4")
	settings, err := s.scrapeSettings()
	if err != nil {
		t.Fatal(err)
	}
	// Missing video fails before metadata fetch but must still retain the queued context.
	ids := s.enqueueScrapeJobs([]string{path}, "missing-video", settings)
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.jobsMu.RLock()
		job := s.jobs[0]
		s.jobsMu.RUnlock()
		if job.Status == -1 {
			if job.ID != ids[0] || job.Number != "ABP-123" || !job.HasSubtitle || job.Error == "" || job.StartedAt == "" || job.CompletedAt == "" || job.LinkMode != 3 {
				t.Fatalf("failed record lost context: %+v", job)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failed job did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
