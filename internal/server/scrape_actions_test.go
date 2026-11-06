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

func configureDeleteWatchRoot(t *testing.T, s *Server, root string) {
	t.Helper()
	body, err := json.Marshal(object{"watch_dirs": []object{{
		"id": "library", "path": root, "enabled": true,
		"use_global_config": true, "use_global_download": true,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if response := call(s, "PUT", "/api/config/common", string(body)); response.Code != 200 {
		t.Fatalf("configure watch root: %s", response.Body.String())
	}
}

func TestDeleteRecordCanDeleteSingleMediaFolder(t *testing.T) {
	s := configureScrapeServer(t)
	root := t.TempDir()
	directory := filepath.Join(root, "IPZZ-034")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(directory, "IPZZ-034.mp4")
	for _, path := range []string{video, filepath.Join(directory, "IPZZ-034.nfo"), filepath.Join(directory, "IPZZ-034-poster.jpg")} {
		if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	configureDeleteWatchRoot(t, s, root)
	settings, _ := s.scrapeSettings()
	s.jobs = []scrapeJob{{
		ID: "remove-media", Sequence: 1, Status: 2, Path: video, Output: video,
		Directory: directory, DirectoryID: "library", Number: "IPZZ-034", Settings: &settings,
	}}
	response := call(s, "DELETE", "/api/scrape/jobs/remove-media", `{"delete_media":true}`)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("media folder was not removed: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("watch root was removed", err)
	}
	if len(s.jobs) != 0 {
		t.Fatal("record was not removed")
	}
}

func TestDeleteRecordInPlaceKeepsWatchRoot(t *testing.T) {
	s := configureScrapeServer(t)
	root := t.TempDir()
	video := filepath.Join(root, "IPZZ-034.mp4")
	if err := os.WriteFile(video, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	configureDeleteWatchRoot(t, s, root)
	settings, _ := s.scrapeSettings()
	settings.Common.LinkMode = linkModeInPlace
	s.jobs = []scrapeJob{{
		ID: "keep-root", Sequence: 1, Status: 2, Path: video, Output: video, LinkMode: linkModeInPlace,
		Directory: root, DirectoryID: "library", Number: "IPZZ-034", Settings: &settings,
	}}
	response := call(s, "DELETE", "/api/scrape/jobs/keep-root", `{"delete_media":true}`)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if _, err := os.Stat(video); !os.IsNotExist(err) {
		t.Fatal("in-place video was not removed", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("watch root was removed", err)
	}
	if len(s.jobs) != 0 {
		t.Fatal("record was not removed")
	}
}

func TestScrapeRecordStopDeleteAndRetry(t *testing.T) {
	s := configureScrapeServer(t)
	settings, err := s.scrapeSettings()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ABP-321.mp4")
	s.jobs = []scrapeJob{{ID: "queued", Sequence: 1, Path: path, Directory: filepath.Dir(path), Number: "ABP-321", DirectoryID: "library", Status: 0, Settings: &settings}}
	s.jobSequence = 1
	if response := call(s, "DELETE", "/api/scrape/jobs/queued", ""); response.Code != 409 {
		t.Fatal(response.Body.String())
	}
	if response := call(s, "POST", "/api/scrape/jobs/queued/stop", ""); response.Code != 202 {
		t.Fatal(response.Body.String())
	}
	s.runScrapeJob("queued", time.Second)
	if s.jobs[0].Status != -2 || s.jobs[0].StartedAt != "" {
		t.Fatal("stopped queued job was executed")
	}
	response := call(s, "POST", "/api/scrape/jobs/queued/retry", "")
	if response.Code != 202 {
		t.Fatal(response.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.jobsMu.RLock()
		job := s.jobs[len(s.jobs)-1]
		s.jobsMu.RUnlock()
		if job.Status == -1 {
			if job.ID != "queued" || job.Source != "retry" || job.Sequence != 1 || job.DirectoryID != "library" || job.Number != "ABP-321" {
				t.Fatalf("retry lost context: %+v", job)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retry did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.jobsMu.RLock()
	count, sequence := len(s.jobs), s.jobSequence
	s.jobsMu.RUnlock()
	if count != 1 || sequence != 1 {
		t.Fatal("retry created another record or consumed a sequence")
	}
	if response := call(s, "DELETE", "/api/scrape/jobs/queued", ""); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if response := call(s, "POST", "/api/scrape/jobs/queued/stop", ""); response.Code != 404 {
		t.Fatal("deleted record still exists")
	}
}

func TestStopRunningRecordCancelsContext(t *testing.T) {
	s := configureScrapeServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.jobs = []scrapeJob{{ID: "running", Status: 1, Cancel: cancel}}
	if response := call(s, "POST", "/api/scrape/jobs/running/stop", ""); response.Code != 202 {
		t.Fatal(response.Body.String())
	}
	if ctx.Err() != context.Canceled || !s.jobs[0].StopRequested {
		t.Fatal("stop did not cancel the worker context")
	}
	if response := call(s, "DELETE", "/api/scrape/jobs/running", ""); response.Code != 409 {
		t.Fatal("deleted running worker")
	}
}

func TestReorganizeExistingMovesSidecarsAndRejectsCollision(t *testing.T) {
	s := configureScrapeServer(t)
	settings, err := s.scrapeSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Naming.File = "{number} {actor}"
	settings.Naming.Suffix = ""
	dir := t.TempDir()
	path := filepath.Join(dir, "ABP-123.mp4")
	base := strings.TrimSuffix(path, ".mp4")
	meta := provider.Metadata{Number: "ABP-123", Title: "Title", Actors: []string{"Actor"}}
	files := map[string][]byte{path: []byte("video"), base + ".mdc.json": []byte("old metadata"), base + ".nfo": []byte("nfo"), base + "-poster.jpg": []byte("poster")}
	for name, data := range files {
		if err := os.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(dir, "ABP-123 Actor.mp4")
	if err := os.WriteFile(target, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := reorganizeExisting(context.Background(), path, meta, settings, func(provider.Metadata) {}, nil); err == nil {
		t.Fatal("overwrote conflicting video")
	}
	for name := range files {
		if _, err := os.Stat(name); err != nil {
			t.Fatal("collision modified source files")
		}
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reorganizeExisting(ctx, path, meta, settings, func(provider.Metadata) {}, nil); err == nil {
		t.Fatal("ignored cancellation")
	}
	output, err := reorganizeExisting(context.Background(), path, meta, settings, func(meta provider.Metadata) {
		if meta.Actors[0] != "Actor" {
			t.Fatal("missing metadata")
		}
	}, nil)
	if err != nil || output != target {
		t.Fatalf("reorganize: %s %v", output, err)
	}
	for name, data := range files {
		targetName := strings.TrimSuffix(target, ".mp4") + strings.TrimPrefix(name, base)
		if strings.HasSuffix(name, ".mdc.json") {
			if _, err := os.Stat(targetName); !os.IsNotExist(err) {
				t.Fatalf("legacy metadata file was not removed: %s", targetName)
			}
			continue
		}
		got, err := os.ReadFile(targetName)
		if err != nil || string(got) != string(data) {
			t.Fatalf("sidecar not preserved: %s %v", targetName, err)
		}
	}
	s.jobs = []scrapeJob{{ID: "finished", Status: 2, Path: path, Output: target}}
	if response := call(s, "DELETE", "/api/scrape/jobs/finished", ""); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal("record deletion deleted video")
	}
}
