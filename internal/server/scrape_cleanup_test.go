package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRemoveScrapeProductsKeepsConfiguredVideosAndSubtitles(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "ABP-123.mp4")
	base := filepath.Join(dir, "ABP-123")
	protected := []string{
		video,
		filepath.Join(dir, "ABP-124.mkv"),
		base + ".srt",
		base + "-中文.ass",
		filepath.Join(dir, "ABP-124.vtt"),
	}
	products := []string{
		base + ".mdc.json", base + ".NFO",
		base + "-poster.jpg", base + "-poster.original.png",
		base + "-thumb.webp", base + "-fanart.jpg",
		base + "-extrafanart-001.jpg", base + "-extrafanart-032.webp",
		base + "-trailer.mp4", base + "-trailer.webm",
		base + "-notes.txt", base + "-extrafanart-custom.jpg",
		base + "-poster-user.jpg", filepath.Join(dir, "ABP-1234-poster.jpg"),
		filepath.Join(dir, "ABP-125.avi"),
	}
	for _, path := range append(protected, products...) {
		if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	settings := scrapeSettings{}
	settings.Common.Extensions = []string{"mp4", "MKV"}
	removed, err := removeScrapeProducts(video, settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != len(products) {
		t.Fatalf("removed %d files: %v", len(removed), removed)
	}
	for _, path := range products {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("file remains: %s", path)
		}
	}
	for _, path := range protected {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("protected file removed: %s: %v", path, err)
		}
	}
}

func TestRemoveScrapeProductsRejectsInvalidVideoPath(t *testing.T) {
	if _, err := removeScrapeProducts("", scrapeSettings{}); err == nil {
		t.Fatal("empty path accepted")
	}
}

func TestRetryDeletesProductsBeforeStarting(t *testing.T) {
	s := configureScrapeServer(t)
	settings, err := s.scrapeSettings()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "ABP-123.mp4")
	base := filepath.Join(dir, "ABP-123")
	for _, path := range []string{
		video, base + ".mdc.json", base + ".nfo", base + "-poster.jpg",
		base + "-thumb.jpg", base + "-fanart.jpg", base + "-extrafanart-001.jpg",
		base + "-trailer.mp4", base + "-notes.txt", base + ".srt",
		filepath.Join(dir, "ABP-124.mp4"),
	} {
		if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s.jobs = []scrapeJob{{
		ID: "retry-cleanup", Sequence: 9, CreatedAt: "original-time", Status: 2,
		Path: video, Output: video, Number: "ABP-123", Settings: &settings,
	}}
	s.scrapeSlots.acquire(1)
	response := call(s, "POST", "/api/scrape/jobs/retry-cleanup/retry", "")
	if response.Code != 202 {
		s.scrapeSlots.release()
		t.Fatal(response.Body.String())
	}
	s.jobsMu.RLock()
	job := s.jobs[0]
	s.jobsMu.RUnlock()
	for _, path := range []string{
		base + ".mdc.json", base + ".nfo", base + "-poster.jpg",
		base + "-thumb.jpg", base + "-fanart.jpg", base + "-extrafanart-001.jpg",
		base + "-trailer.mp4",
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			s.scrapeSlots.release()
			t.Fatalf("old product remains before retry: %s", path)
		}
	}
	for _, path := range []string{video, base + ".srt", filepath.Join(dir, "ABP-124.mp4")} {
		if _, err := os.Stat(path); err != nil {
			s.scrapeSlots.release()
			t.Fatalf("protected file removed: %s: %v", path, err)
		}
	}
	if job.ID != "retry-cleanup" || job.Sequence != 9 || job.CreatedAt != "original-time" || len(job.Logs) != 1 || job.Logs[0].Stage != "准备重试" {
		s.scrapeSlots.release()
		t.Fatalf("retry context changed: %+v", job)
	}
	s.scrapeSlots.release()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.jobsMu.RLock()
		status := s.jobs[0].Status
		s.jobsMu.RUnlock()
		if status != 0 && status != 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retry did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRemoveScrapeProductsPreservesSubtitlesRegardlessOfNaming(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "ABP-123.mp4")
	protected := []string{
		video,
		filepath.Join(dir, "poster.srt"),
		filepath.Join(dir, "ABP-123.ASS"),
	}
	products := []string{
		filepath.Join(dir, "poster.jpg"),
		filepath.Join(dir, "thumb.jpg"),
		filepath.Join(dir, "fanart.jpg"),
		filepath.Join(dir, "extrafanart-001.jpg"),
		filepath.Join(dir, "trailer.mp4"),
		filepath.Join(dir, "notes.txt"),
	}
	for _, path := range append(protected, products...) {
		if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	settings := scrapeSettings{}
	settings.Naming.ImageWithPrefix = true
	removed, err := removeScrapeProducts(video, settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != len(products) {
		t.Fatalf("removed %d files: %v", len(removed), removed)
	}
	for _, path := range protected {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("protected file removed: %s: %v", path, err)
		}
	}
}
