package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mdcng-go/internal/provider"
)

// Keep queued jobs pending during scan assertions; drain workers before temp cleanup.
func holdWatcherJobs(t *testing.T, s *Server) {
	t.Helper()
	s.scrapeSlots.acquire(1)
	t.Cleanup(func() {
		s.scrapeSlots.release()
		deadline := time.Now().Add(5 * time.Second)
		for {
			s.jobsMu.RLock()
			active := false
			for _, job := range s.jobs {
				if job.Status == 0 || job.Status == 1 {
					active = true
				}
			}
			s.jobsMu.RUnlock()
			if !active {
				s.scrapeSlots.acquire(1)
				s.scrapeSlots.release()
				return
			}
			if time.Now().After(deadline) {
				t.Error("watcher jobs did not finish")
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

func TestWatcherScansEachDirectoryWithItsOwnWhitelist(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	for _, dir := range []string{first, second, filepath.Join(first, "nested")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{
		filepath.Join(first, "tiny.MP4"), filepath.Join(first, "skip.mkv"),
		filepath.Join(second, "film.mkv"), filepath.Join(second, "skip.mp4"),
	} {
		if err := os.WriteFile(name, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	holdWatcherJobs(t, s)
	config := watchConfig{Enabled: true, IntervalSeconds: 30}
	config.Directories = []watchDirectory{
		{ID: "first", Path: first, Enabled: true, Common: json.RawMessage(`{"link_mode":3,"file_ext_filter":["mp4"]}`)},
		{ID: "second", Path: second, Enabled: true, Common: json.RawMessage(`{"link_mode":3,"file_ext_filter":["mkv"]}`)},
	}
	s.scanWatchDirectories(context.Background(), config)
	if len(s.watchState.Directories) != 2 || s.watchState.Directories[0].Matched != 1 || s.watchState.Directories[1].Matched != 1 || s.watchState.Directories[0].NewFiles != 1 || s.watchState.Directories[1].NewFiles != 1 {
		t.Fatal(s.watchState.Directories)
	}
	s.scanWatchDirectories(context.Background(), config)
	if s.watchState.Directories[0].NewFiles != 0 || s.watchState.Directories[1].NewFiles != 0 {
		t.Fatal("active files were queued twice", s.watchState.Directories)
	}
	if err := os.WriteFile(filepath.Join(first, "nested", "new.mp4"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	s.scanWatchDirectories(context.Background(), config)
	if s.watchState.Directories[0].Matched != 2 || s.watchState.Directories[0].NewFiles != 1 || s.watchState.Directories[1].NewFiles != 0 {
		t.Fatal(s.watchState.Directories)
	}
}

func TestWatcherRechecksUnchangedVideoState(t *testing.T) {
	for _, scenario := range []struct {
		name, initialCommon, nextCommon string
		organized                       bool
	}{
		{"removed successful record", `{"link_mode":3,"file_ext_filter":["mp4"]}`, `{"link_mode":3,"file_ext_filter":["mp4"]}`, true},
		{"changed size filter", `{"link_mode":3,"file_ext_filter":["mp4"],"file_size_filter":100}`, `{"link_mode":3,"file_ext_filter":["mp4"],"file_size_filter":0}`, false},
		{"changed blacklist", `{"link_mode":3,"file_ext_filter":["mp4"],"file_name_blacklist":["ABP"]}`, `{"link_mode":3,"file_ext_filter":["mp4"],"file_name_blacklist":[]}`, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s := configureScrapeServer(t)
			dir := t.TempDir()
			video := filepath.Join(dir, "ABP-123.mp4")
			if err := os.WriteFile(video, []byte("video"), 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(video)
			if err != nil {
				t.Fatal(err)
			}
			if scenario.organized {
				s.jobs = []scrapeJob{{ID: "organized", Sequence: 1, Status: 2, Path: video, Output: video}}
			}
			holdWatcherJobs(t, s)
			config := watchConfig{Enabled: true, IntervalSeconds: 30, Directories: []watchDirectory{{ID: "recheck", Path: dir, Enabled: true, UseGlobalDownload: true, Common: json.RawMessage(scenario.initialCommon)}}}
			s.scanWatchDirectories(context.Background(), config)
			if s.watchState.Directories[0].NewFiles != 0 {
				t.Fatal("initially excluded file was queued")
			}
			if scenario.organized {
				s.scanWatchDirectories(context.Background(), config)
				if s.watchState.Directories[0].NewFiles != 0 {
					t.Fatal("organized file was queued")
				}
				s.jobs = nil
			}
			config.Directories[0].Common = json.RawMessage(scenario.nextCommon)
			s.scanWatchDirectories(context.Background(), config)
			if s.watchState.Directories[0].NewFiles != 1 {
				t.Fatal("changed state of unchanged video was ignored", s.watchState.Directories)
			}
			after, err := os.Stat(video)
			if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
				t.Fatal("test must not change video")
			}
			s.scanWatchDirectories(context.Background(), config)
			if s.watchState.Directories[0].NewFiles != 0 {
				t.Fatal("pending job was queued twice")
			}
		})
	}
}

func TestWatcherRemovesLegacyMetadataSidecars(t *testing.T) {
	s := configureScrapeServer(t)
	dir := t.TempDir()
	sidecar := filepath.Join(dir, "ABP-123.mdc.json")
	if err := os.WriteFile(sidecar, []byte(`{"old":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	holdWatcherJobs(t, s)
	s.scanWatchDirectories(context.Background(), watchConfig{
		Enabled: true, IntervalSeconds: 30,
		Directories: []watchDirectory{{ID: "legacy", Path: dir, Enabled: true, UseGlobalDownload: true}},
	})
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Fatalf("legacy metadata sidecar was not removed: %v", err)
	}
}

func TestWatcherAcceptsMoveMode(t *testing.T) {
	s := configureScrapeServer(t)
	source := t.TempDir()
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "ABP-123.mp4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	common, err := json.Marshal(object{
		"link_mode": 2, "target_folder": target,
		"file_ext_filter": []string{"mp4"}, "file_size_filter": 0, "file_name_blacklist": []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	holdWatcherJobs(t, s)
	s.scanWatchDirectories(context.Background(), watchConfig{
		Enabled: true, IntervalSeconds: 30,
		Directories: []watchDirectory{{
			ID: "move", Path: source, Enabled: true, UseGlobalDownload: true, Common: common,
		}},
	})
	status := s.watchState.Directories[0]
	if status.Error != "" || status.NewFiles != 1 {
		t.Fatalf("move mode was not accepted: %+v", status)
	}
}

func TestDirectoryOverridesNamingWatermarkAndNFO(t *testing.T) {
	s := configureScrapeServer(t)
	global, err := s.scrapeSettings()
	if err != nil {
		t.Fatal(err)
	}
	localNaming := global.Naming
	localNaming.Folder = "Local/{number}"
	localNaming.File = "{publish_number}"
	namingRaw, _ := json.Marshal(localNaming)
	watermarkRaw, _ := json.Marshal(object{"enabled": true, "thumb": false, "poster": true})
	nfoRaw, _ := json.Marshal(object{"enabled": false, "enable_actor": false})
	useLocal := false
	dir := watchDirectory{
		ID: "local", Path: t.TempDir(), Enabled: true,
		UseGlobalNaming: &useLocal, Naming: namingRaw,
		UseGlobalWatermark: &useLocal, Watermark: watermarkRaw,
		UseGlobalNFO: &useLocal, NFO: nfoRaw,
	}
	settings, err := s.scrapeSettingsForDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Naming.Folder != "Local/{number}" || settings.Naming.File != "{publish_number}" {
		t.Fatalf("naming override not applied: %+v", settings.Naming)
	}
	if settings.Watermark["thumb"] != false || settings.Watermark["enabled"] != true {
		t.Fatalf("watermark override not applied: %+v", settings.Watermark)
	}
	if settings.NFO.Enabled || settings.NFO.EnableActor {
		t.Fatalf("NFO override not applied: %+v", settings.NFO)
	}
}

func TestDirectoryOverridesProvidersAndPriorities(t *testing.T) {
	s := configureScrapeServer(t)
	useLocal := false
	providersRaw, _ := json.Marshal(directoryProviderConfig{
		Configs: map[string]provider.Config{
			"Javbus": {Enabled: true, BaseURL: "https://local.example", Cookies: "local=1"},
		},
		Priorities: &provider.Priorities{ByFields: map[string][]string{
			"title": {"Javbus"}, "original_title": {"Javbus"},
		}},
	})
	settings, err := s.scrapeSettingsForDirectory(watchDirectory{
		ID: "provider-local", Path: t.TempDir(), Enabled: true,
		UseGlobalProviders: &useLocal, Providers: providersRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	if settings.ProviderConfigs["Javbus"].BaseURL != "https://local.example" || settings.ProviderConfigs["Javbus"].Cookies != "local=1" {
		t.Fatalf("provider config override not applied: %+v", settings.ProviderConfigs["Javbus"])
	}
	if settings.Priorities == nil || fmt.Sprint(settings.Priorities.ByFields["title"]) != "[Javbus]" {
		t.Fatalf("priority override not applied: %+v", settings.Priorities)
	}
}

func TestWatcherConfigPersistsAndRejectsInvalidInterval(t *testing.T) {
	root := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(object{
		"watcher_enabled": true, "watcher_poll_interval_seconds": 15,
		"watch_dirs": []any{object{"id": "one", "path": root, "enabled": true, "use_global_config": false, "common": object{"file_ext_filter": []string{"mp4"}, "target_folder": t.TempDir()}}},
	})
	if response := call(s, "PUT", "/api/config/common", string(body)); response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	s, err = New(root)
	if err != nil {
		t.Fatal(err)
	}
	config := s.currentWatchConfig()
	var common struct {
		Extensions []string `json:"file_ext_filter"`
	}
	_ = json.Unmarshal(config.Directories[0].Common, &common)
	if !config.Enabled || config.IntervalSeconds != 15 || len(config.Directories) != 1 || len(common.Extensions) != 1 || common.Extensions[0] != "mp4" {
		t.Fatal(config)
	}
	if response := call(s, "PUT", "/api/config/common", `{"watcher_poll_interval_seconds":0}`); response.Code != 400 {
		t.Fatal("invalid interval accepted", response.Code)
	}
}
