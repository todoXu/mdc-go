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

func TestScrapeDetailCapturesMetadataAndRealEvents(t *testing.T) {
	s := configureScrapeServer(t)
	path := filepath.Join(t.TempDir(), "ABP-123.mp4")
	if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	settings, _ := s.scrapeSettings()
	s.jobs = []scrapeJob{{ID: "detail", Path: path, Number: "ABP-123", Settings: &settings}}
	s.runScrapeJob("detail", time.Second*5)
	response := call(s, "GET", "/api/scrape/jobs/detail", "")
	var job scrapeJob
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.Status != 2 || job.Metadata == nil || job.Metadata.Title != "Example Title" || job.Provenance["title"] != "Javbus" {
		t.Fatalf("missing detail: %+v", job)
	}
	if len(job.Logs) < 6 || job.Logs[0].Stage != "解析番号" || job.Logs[len(job.Logs)-1].Stage != "处理结果" {
		t.Fatalf("missing events: %+v", job.Logs)
	}
	previous := time.Time{}
	for _, event := range job.Logs {
		parsed, err := time.Parse(time.RFC3339Nano, event.Time)
		if err != nil || parsed.Before(previous) || event.Message == "" {
			t.Fatalf("invalid event: %+v", event)
		}
		previous = parsed
	}
	list := call(s, "GET", "/api/scrape/jobs", "")
	if strings.Contains(list.Body.String(), `"metadata"`) || strings.Contains(list.Body.String(), `"logs"`) {
		t.Fatal("list includes full detail")
	}
	if response := call(s, "GET", "/api/scrape/jobs/missing", ""); response.Code != 404 {
		t.Fatal("missing record must return 404")
	}
}

func TestScrapeDetailImagesUseKnownCompletedOutput(t *testing.T) {
	s := configureScrapeServer(t)
	dir := t.TempDir()
	output := filepath.Join(dir, "ABP-123.mp4")
	if err := os.WriteFile(filepath.Join(dir, "ABP-123-poster.jpg"), []byte("picture"), 0600); err != nil {
		t.Fatal(err)
	}
	s.jobs = []scrapeJob{{ID: "image", Status: 2, Output: output, Metadata: &provider.Metadata{Poster: "https://example.com/poster.jpg"}}, {ID: "active", Status: 1, Output: output}}
	response := call(s, "GET", "/api/scrape/jobs/image", "")
	if !strings.Contains(response.Body.String(), "/api/scrape/jobs/image/images/poster.jpg") {
		t.Fatal(response.Body.String())
	}
	image := call(s, "GET", "/api/scrape/jobs/image/images/poster.jpg", "")
	if image.Code != 200 || image.Body.String() != "picture" || image.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("image failed: %d %s", image.Code, image.Body.String())
	}
	for _, endpoint := range []string{"/api/scrape/jobs/image/images/video.mp4", "/api/scrape/jobs/image/images/poster.png", "/api/scrape/jobs/active/images/poster.jpg", "/api/scrape/jobs/missing/images/poster.jpg"} {
		if response := call(s, "GET", endpoint, ""); response.Code != 404 {
			t.Fatalf("unexpected image access: %s %d", endpoint, response.Code)
		}
	}
}

func TestRecordGalleryMediaAndSwitches(t *testing.T) {
	s := configureScrapeServer(t)
	dir := t.TempDir()
	settings, _ := s.scrapeSettings()
	settings.Download.Thumb = true
	settings.Download.Fanart = true
	settings.Download.ExtraFanarts = false
	settings.Download.Trailer = true
	s.jobs = []scrapeJob{{ID: "gallery", Status: 2, Output: filepath.Join(dir, "TEST-123.mp4"), Settings: &settings, Metadata: &provider.Metadata{}}}
	for _, name := range []string{"thumb.jpg", "fanart.png", "extrafanart-001.webp", "trailer.mp4"} {
		if err := os.WriteFile(filepath.Join(dir, "TEST-123-"+name), []byte("media"), 0600); err != nil {
			t.Fatal(err)
		}
		response := call(s, "GET", "/api/scrape/jobs/gallery/images/"+name, "")
		if response.Code != 200 || response.Body.String() != "media" {
			t.Fatalf("%s: %d", name, response.Code)
		}
	}
	response := call(s, "GET", "/api/scrape/jobs/gallery", "")
	var detail struct {
		Metadata provider.Metadata `json:"metadata"`
		Options  map[string]bool   `json:"download_options"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if !detail.Options["thumb"] || !detail.Options["fanart"] || !detail.Options["trailer"] || detail.Options["extra_fanarts"] {
		t.Fatal(detail.Options)
	}
	for _, key := range []string{"cover", "fanart", "sample_1", "trailer"} {
		if detail.Metadata.LocalImages[key] == "" {
			t.Fatalf("missing %s", key)
		}
	}
	for _, name := range []string{"extrafanart-000.jpg", "extrafanart-033.jpg", "extrafanart-custom.jpg", "video.mp4"} {
		if response := call(s, "GET", "/api/scrape/jobs/gallery/images/"+name, ""); response.Code != 404 {
			t.Fatalf("unexpected access: %s", name)
		}
	}
	video := call(s, "GET", "/api/scrape/jobs/gallery/images/trailer.mp4", "")
	if video.Header().Get("Content-Type") != "video/mp4" {
		t.Fatal(video.Header())
	}
}

func TestRecordImagesUseFreshVersionAfterReplacement(t *testing.T) {
	s := configureScrapeServer(t)
	dir := t.TempDir()
	output := filepath.Join(dir, "ABP-123.mp4")
	poster := filepath.Join(dir, "ABP-123-poster.jpg")
	if err := os.WriteFile(output, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(poster, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	firstTime := time.Unix(100, 0)
	if err := os.Chtimes(poster, firstTime, firstTime); err != nil {
		t.Fatal(err)
	}
	s.jobs = []scrapeJob{{ID: "fresh", Status: 2, Output: output, Metadata: &provider.Metadata{Poster: "https://example.com/poster.jpg"}}}
	readURL := func() string {
		response := call(s, "GET", "/api/scrape/jobs/fresh", "")
		var detail struct {
			Metadata provider.Metadata `json:"metadata"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		return detail.Metadata.LocalImages["poster"]
	}
	firstURL := readURL()
	if !strings.Contains(firstURL, "/images/poster.jpg?v=") {
		t.Fatal(firstURL)
	}
	if err := os.WriteFile(poster, []byte("second-image"), 0600); err != nil {
		t.Fatal(err)
	}
	secondTime := time.Unix(101, 0)
	if err := os.Chtimes(poster, secondTime, secondTime); err != nil {
		t.Fatal(err)
	}
	secondURL := readURL()
	if secondURL == firstURL {
		t.Fatalf("image version did not change: %s", secondURL)
	}
	image := call(s, "GET", secondURL, "")
	if image.Code != 200 || image.Body.String() != "second-image" {
		t.Fatalf("stale image response: %d %s", image.Code, image.Body.String())
	}
	// 带版本号的地址可以长期缓存，因为图片一改版本号就变（上面已验证）；不带版本号的地址不缓存。
	if image.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Fatal(image.Header().Get("Cache-Control"))
	}
	unversioned := call(s, "GET", strings.SplitN(secondURL, "?", 2)[0], "")
	if unversioned.Code != 200 || unversioned.Header().Get("Cache-Control") != "private, no-store, max-age=0" {
		t.Fatalf("unversioned image: %d %s", unversioned.Code, unversioned.Header().Get("Cache-Control"))
	}
}
