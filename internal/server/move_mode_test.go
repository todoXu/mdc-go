package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func moveModeSettings(t *testing.T, s *Server, target string) scrapeSettings {
	t.Helper()
	settings, err := s.scrapeSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Common.LinkMode = 2
	settings.Common.TargetFolder = target
	settings.Common.TargetOverwriteVideo = false
	settings.Common.TargetOverwritePic = false
	settings.Common.DeleteEmptyParentAfterMove = true
	settings.Common.LTX = []string{"LTX"}
	settings.Naming.Folder = "{actor}/{number}"
	settings.Naming.File = "{number}"
	return settings
}

func TestMoveModeMovesVideoSubtitlesAndNFO(t *testing.T) {
	s := configureScrapeServer(t)
	sourceRoot := t.TempDir()
	targetRoot := t.TempDir()
	sourceDir := filepath.Join(sourceRoot, "IPZZ-034")
	if err := os.MkdirAll(sourceDir, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceDir, "IPZZ-034-LTX-中文.mp4")
	subtitle := filepath.Join(sourceDir, "IPZZ-034-LTX-中文.srt")
	for path, data := range map[string]string{source: "video", subtitle: "subtitle"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	settings := moveModeSettings(t, s, targetRoot)
	output, warnings, err := s.scrapeInPlace(context.Background(), source, "IPZZ-034", settings, false)
	if err != nil || len(warnings) != 0 {
		t.Fatal(err, warnings)
	}
	want := filepath.Join(targetRoot, "Example Actor", "IPZZ-034", "IPZZ-034-LTX-破解-中文.mp4")
	if output != want {
		t.Fatalf("output=%q want=%q", output, want)
	}
	for _, path := range []string{want, strings.TrimSuffix(want, ".mp4") + ".srt", strings.TrimSuffix(want, ".mp4") + ".nfo"} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing moved output %s: %v", path, err)
		}
	}
	for _, path := range []string{source, subtitle} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("source remains %s: %v", path, err)
		}
	}
	if _, err := os.Stat(sourceDir); !os.IsNotExist(err) {
		t.Fatalf("empty source directory remains: %v", err)
	}
}

func TestMoveModeCollisionKeepsSourceUnlessOverwriteEnabled(t *testing.T) {
	s := configureScrapeServer(t)
	sourceRoot := t.TempDir()
	targetRoot := t.TempDir()
	sourceDir := filepath.Join(sourceRoot, "ABP-123")
	if err := os.MkdirAll(sourceDir, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceDir, "ABP-123.mp4")
	if err := os.WriteFile(source, []byte("new video"), 0600); err != nil {
		t.Fatal(err)
	}
	settings := moveModeSettings(t, s, targetRoot)
	settings.Naming.Folder = ""
	target := filepath.Join(targetRoot, "ABP-123.mp4")
	if err := os.WriteFile(target, []byte("old video"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.scrapeInPlace(context.Background(), source, "ABP-123", settings, false); err == nil {
		t.Fatal("move overwrote target while overwrite was disabled")
	}
	if got, err := os.ReadFile(source); err != nil || string(got) != "new video" {
		t.Fatal("source was removed after collision", err)
	}

	settings.Common.TargetOverwriteVideo = true
	settings.Common.TargetOverwritePic = true
	if _, _, err := s.scrapeInPlace(context.Background(), source, "ABP-123", settings, false); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "new video" {
		t.Fatal("target was not replaced", err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("source was not moved")
	}
}
