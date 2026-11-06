package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSuccessfulRecordRetryRefetchesFromOutput(t *testing.T) {
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
	output, _, err := s.organizeInPlaceWithSettings(context.Background(), original, "", settings)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSuffix(output, ".mp4")
	if err := os.WriteFile(base+".mdc.json", []byte(`{"title":"Old title"}`), 0600); err != nil {
		t.Fatal(err)
	}
	// Reorganized records must also refetch rather than inherit Reorganize=true.
	created := "2026-11-04T10:00:00+08:00"
	s.jobs = []scrapeJob{{ID: "success", Sequence: 7, CreatedAt: created, Status: 2, Path: original, Output: output, Number: "ABP-123", Settings: &settings, Reorganize: true, DirectoryID: "library", Logs: []scrapeEvent{{Message: "old attempt"}}}}
	s.jobSequence = 7
	s.scrapeSlots.acquire(1)
	response := call(s, "POST", "/api/scrape/jobs/success/retry", "")
	if response.Code != 202 {
		s.scrapeSlots.release()
		t.Fatal(response.Body.String())
	}
	s.jobsMu.RLock()
	next := s.jobs[len(s.jobs)-1]
	count, sequence := len(s.jobs), s.jobSequence
	s.jobsMu.RUnlock()
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		s.scrapeSlots.release()
		t.Fatal(err)
	}
	if count != 1 || sequence != 7 || result.ID != "success" || next.ID != "success" || next.Sequence != 7 || next.CreatedAt != created || len(next.Logs) != 1 || next.Logs[0].Stage != "准备重试" || next.Path != output || next.Reorganize || !next.RetryExisting || next.DirectoryID != "library" {
		s.scrapeSlots.release()
		t.Fatalf("retry context: %+v", next)
	}
	duplicate := call(s, "POST", "/api/scrape/jobs/success/retry", "")
	restored, restoreErr := New(filepath.Dir(s.statePath))
	s.scrapeSlots.release()
	if restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if len(restored.jobs) != 1 || restored.jobs[0].ID != "success" || restored.jobs[0].Status != -2 || !restored.jobs[0].RetryExisting || restored.jobs[0].Path != output {
		t.Fatal("restart lost retry identity or overwrite context")
	}
	if duplicate.Code != 409 {
		t.Fatalf("duplicate accepted: %s", duplicate.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.jobsMu.RLock()
		job := s.jobs[len(s.jobs)-1]
		s.jobsMu.RUnlock()
		if job.Status != 0 && job.Status != 1 {
			if job.Status != 2 {
				t.Fatalf("retry failed: %s", job.Error)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retry timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(base + ".mdc.json"); !os.IsNotExist(err) {
		t.Fatal("legacy metadata file was not removed")
	}
	video, err := os.ReadFile(output)
	if err != nil || string(video) != "video" {
		t.Fatal("video changed", err)
	}
	s.jobsMu.RLock()
	final := s.jobs[0]
	count = len(s.jobs)
	s.jobsMu.RUnlock()
	if count != 1 || final.ID != "success" || final.Sequence != 7 || final.CreatedAt != created || final.StartedAt == "" || final.CompletedAt == "" {
		t.Fatal("completed retry changed record identity")
	}
	if final.Metadata == nil || final.Metadata.Title != "Example Title" {
		t.Fatalf("metadata was not refreshed: %+v", final.Metadata)
	}
}

func TestCommitRetryPreservesOrRestoresSidecars(t *testing.T) {
	for _, scenario := range []string{"replace", "rename", "collision", "rollback", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			base := filepath.Join(dir, "ABP-123")
			path, output := base+".mp4", base+".mp4"
			old := map[string]string{".mdc.json": "old metadata", ".nfo": "old nfo", "-poster.jpg": "old poster", "-poster.original.jpg": "old original", "-thumb.png": "keep thumb"}
			for suffix, content := range old {
				if err := os.WriteFile(base+suffix, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "rollback" {
				if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "rename" || scenario == "collision" || scenario == "rollback" {
				output = filepath.Join(dir, "ABP-123 New.mp4")
			}
			newBase := strings.TrimSuffix(output, ".mp4")
			if scenario == "collision" {
				if err := os.WriteFile(newBase+".nfo", []byte("unrelated"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancel" {
				cancel()
			}
			err := commitScrapeRetry(ctx, path, output, map[string][]byte{newBase + "-poster.png": []byte("new poster")})
			if scenario == "collision" || scenario == "rollback" || scenario == "cancel" {
				if err == nil {
					t.Fatal("expected failure")
				}
				for suffix, want := range old {
					got, err := os.ReadFile(base + suffix)
					if err != nil || string(got) != want {
						t.Fatalf("lost %s: %s %v", suffix, got, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for suffix, want := range map[string]string{"-poster.png": "new poster", ".nfo": "old nfo", "-thumb.png": "keep thumb", ".mp4": "video"} {
				got, err := os.ReadFile(newBase + suffix)
				if err != nil || string(got) != want {
					t.Fatalf("bad %s: %s %v", suffix, got, err)
				}
			}
			if _, err := os.Stat(newBase + ".mdc.json"); !os.IsNotExist(err) {
				t.Fatal("legacy metadata file was preserved")
			}
			for _, suffix := range []string{"-poster.jpg", "-poster.original.jpg"} {
				if _, err := os.Stat(newBase + suffix); !os.IsNotExist(err) {
					t.Fatalf("stale image retained: %s", suffix)
				}
			}
		})
	}
}

func TestRetryMovesAndReplacesOnlyOwnedExtraMedia(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "ABP-123")
	newBase := filepath.Join(dir, "ABP-123 New")
	for suffix, content := range map[string]string{".mp4": "video", ".mdc.json": "metadata", "-extrafanart-001.jpg": "old scene", "-extrafanart-002.jpg": "stale scene", "-trailer.webm": "old trailer", "-notes.txt": "user note", "-extrafanart-custom.jpg": "user picture"} {
		if err := os.WriteFile(base+suffix, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	err := commitScrapeRetry(context.Background(), base+".mp4", newBase+".mp4", map[string][]byte{newBase + "-extrafanart-001.png": []byte("new scene"), newBase + "-trailer.mp4": []byte("new trailer")})
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-notes.txt", "-extrafanart-custom.jpg"} {
		if _, err := os.Stat(base + suffix); err != nil {
			t.Fatal("unrelated file changed", err)
		}
	}
	for suffix, want := range map[string]string{".mp4": "video", "-extrafanart-001.png": "new scene", "-trailer.mp4": "new trailer"} {
		got, err := os.ReadFile(newBase + suffix)
		if err != nil || string(got) != want {
			t.Fatal("resource missing after rename", suffix, err)
		}
	}
	if _, err := os.Stat(newBase + ".mdc.json"); !os.IsNotExist(err) {
		t.Fatal("legacy metadata file was preserved")
	}
	for _, suffix := range []string{"-extrafanart-001.jpg", "-extrafanart-002.jpg", "-trailer.webm"} {
		if _, err := os.Stat(base + suffix); !os.IsNotExist(err) {
			t.Fatal("old resource remained")
		}
		if _, err := os.Stat(newBase + suffix); !os.IsNotExist(err) {
			t.Fatal("stale format was kept")
		}
	}
}
