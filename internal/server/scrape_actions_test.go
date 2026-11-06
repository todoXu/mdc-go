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

// STRM 记录改用移动模式重试：旧的 .strm、NFO、图片在整理目录里被清理；
// 源视频所在的监控目录里用户自己的文件不能被删。
func TestRetrySTRMRecordCleansOutputFolderNotWatchFolder(t *testing.T) {
	s := configureScrapeServer(t)
	settings, err := s.scrapeSettings()
	if err != nil {
		t.Fatal(err)
	}
	watch := t.TempDir()
	source := filepath.Join(watch, "ABP-123.mp4")
	userFile := filepath.Join(watch, "readme.txt")
	output := filepath.Join(t.TempDir(), "Actor", "ABP-123", "ABP-123.strm")
	base := strings.TrimSuffix(output, ".strm")
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{source: "video", userFile: "mine", output: source, base + ".nfo": "nfo", base + "-poster.jpg": "poster"} {
		if err := os.WriteFile(name, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	job := &scrapeJob{ID: "strm", Status: 2, Path: source, Output: output, LinkMode: linkModeSTRM}
	removed, err := removeRetryProducts(job, source, settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 3 {
		t.Fatalf("removed %v", removed)
	}
	if _, err := os.Stat(filepath.Dir(output)); !os.IsNotExist(err) {
		t.Fatal("empty output folder kept")
	}
	for _, keep := range []string{source, userFile} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("watch folder file removed: %s", keep)
		}
	}
	// 上一次失败、没有 .strm 时什么都不删。
	failed := &scrapeJob{ID: "failed", Status: -1, Path: source, LinkMode: linkModeSTRM}
	if removed, err := removeRetryProducts(failed, source, settings); err != nil || len(removed) != 0 {
		t.Fatalf("failed strm retry removed %v %v", removed, err)
	}
	if _, err := os.Stat(userFile); err != nil {
		t.Fatal("watch folder file removed on failed retry")
	}
}

// 已成功整理的记录：监控目录的整理方式改变后，重新刮削和重新整理都被拒绝，文件保持不动；
// 改回原来的方式后可以正常处理。未成功的记录不受限制。
func TestRecordActionsRejectChangedLinkMode(t *testing.T) {
	s := configureScrapeServer(t)
	target := t.TempDir()
	watch := t.TempDir()
	source := filepath.Join(watch, "ABP-123.mp4")
	output := filepath.Join(target, "ABP-123.strm")
	for name, data := range map[string]string{source: "video", output: source} {
		if err := os.WriteFile(name, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	setMode := func(mode int) {
		t.Helper()
		body, _ := json.Marshal(object{"link_mode": mode, "target_folder": target})
		if w := call(s, "PUT", "/api/config/common", string(body)); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	settings, _ := s.scrapeSettings()
	s.jobsMu.Lock()
	s.jobs = []scrapeJob{
		{ID: "done", Status: 2, Path: source, Output: output, Number: "ABP-123", LinkMode: linkModeSTRM, Settings: &settings, Metadata: &provider.Metadata{Number: "ABP-123", Title: "T"}},
		{ID: "failed", Status: -1, Path: filepath.Join(watch, "ABP-124.mp4"), Number: "ABP-124", LinkMode: linkModeSTRM, Settings: &settings},
	}
	s.jobsMu.Unlock()

	setMode(linkModeMove)
	for _, action := range []string{"retry", "reorganize"} {
		w := call(s, "POST", "/api/scrape/jobs/done/"+action, "")
		if w.Code != 409 || !strings.Contains(w.Body.String(), "link_mode_changed") || !strings.Contains(w.Body.String(), "STRM") {
			t.Fatalf("%s with changed mode: %d %s", action, w.Code, w.Body.String())
		}
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatal("rejected retry still removed the old strm")
	}
	// 修改元数据中的命名字段会触发重新整理，同样被拒绝。
	edit := `{"fields":{"title":"New title"}}`
	if w := call(s, "PUT", "/api/scrape/jobs/done/metadata", edit); w.Code != 409 || !strings.Contains(w.Body.String(), "link_mode_changed") {
		t.Fatalf("metadata edit with changed mode: %d %s", w.Code, w.Body.String())
	}
	// 失败的记录还没整理，可以按新方式重新刮削。
	if w := call(s, "POST", "/api/scrape/jobs/failed/retry", ""); w.Code == 409 && strings.Contains(w.Body.String(), "link_mode_changed") {
		t.Fatalf("failed record rejected: %s", w.Body.String())
	}
	// 改回原来的方式后，不再因为整理方式被拒绝。
	setMode(linkModeSTRM)
	if w := call(s, "POST", "/api/scrape/jobs/done/reorganize", ""); strings.Contains(w.Body.String(), "link_mode_changed") {
		t.Fatalf("same mode rejected: %d %s", w.Code, w.Body.String())
	}
}
