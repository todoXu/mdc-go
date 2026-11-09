package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mdcng-go/internal/provider"
)

func recordEditorFixture(t *testing.T) (*Server, string) {
	t.Helper()
	s := configureScrapeServer(t)
	settings, _ := s.scrapeSettings()
	settings.Naming.File, settings.Naming.Suffix = "{number} {title}", ""
	settings.Download.Poster, settings.Download.Thumb, settings.Download.Fanart = false, false, false
	settings.Download.ExtraFanarts, settings.Download.Trailer = false, false
	settings.NFO.Enabled = true
	path := filepath.Join(t.TempDir(), "ABP-123 Original.mp4")
	meta := provider.Metadata{Provider: "Javbus", Number: "ABP-123", Title: "Original", Cover: "https://example.test/cover.png"}
	writes := map[string][]byte{}
	err := addMetadataWrites(writes, meta, strings.TrimSuffix(path, ".mp4"), settings)
	if err != nil {
		t.Fatal(err)
	}
	writes[path] = []byte("video")
	for name, data := range writes {
		if err := os.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	s.jobs = []scrapeJob{{ID: "edit", Sequence: 42, CreatedAt: "original-time", Status: 2, Path: path, Output: path, Settings: &settings, Metadata: &meta, Provenance: map[string]string{"cover": "Javbus"}}}
	s.jobSequences = map[string]int{"": 42}
	return s, path
}

func TestRecordEditCommitsMetadataNFOAndRename(t *testing.T) {
	s, original := recordEditorFixture(t)
	response := call(s, "PUT", "/api/scrape/jobs/edit/metadata", `{"fields":{"title":"Updated","actors":["Actor"],"rating":"4.5"}}`)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	// 命名字段变化后，记录排队重新整理（后台执行），等待其完成再检查结果。
	if queued := s.jobs[0]; !queued.Reorganize || queued.ReorgFrom != original {
		t.Fatalf("edit did not queue reorganize: %+v", queued)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		s.jobsMu.RLock()
		status := s.jobs[0].Status
		s.jobsMu.RUnlock()
		if status == 2 || status == -1 || status == -2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	job := s.jobs[0]
	if job.Status != 2 {
		t.Fatalf("reorganize did not complete: status=%d error=%s", job.Status, job.Error)
	}
	if job.ID != "edit" || job.Sequence != 42 || job.CreatedAt != "original-time" || job.Editing || s.jobSequences[""] != 42 || len(s.jobs) != 1 {
		t.Fatalf("identity changed: %+v", job)
	}
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Fatal("old video remains", err)
	}
	video, err := os.ReadFile(job.Output)
	if err != nil || string(video) != "video" {
		t.Fatal("video lost", err)
	}
	base := strings.TrimSuffix(job.Output, ".mp4")
	nfo, _ := os.ReadFile(base + ".nfo")
	if !strings.Contains(string(nfo), "<title>ABP-123 Updated</title>") || !strings.Contains(string(nfo), "<rating>4.5</rating>") || !strings.Contains(string(nfo), "<name>Actor</name>") {
		t.Fatal(string(nfo))
	}
	if job.Metadata == nil || job.Metadata.Title != "Updated" || job.Metadata.Rating != "4.5" {
		t.Fatal(job.Metadata)
	}
	if _, err := os.Stat(base + ".mdc.json"); !os.IsNotExist(err) {
		t.Fatal("legacy metadata file was created")
	}
	if job.Provenance["title"] != "manual" {
		t.Fatal(job.Provenance)
	}
}

func TestRecordEditCollisionAndSourceValidationLeaveOriginal(t *testing.T) {
	s, path := recordEditorFixture(t)
	target := filepath.Join(filepath.Dir(path), "ABP-123 Taken.mp4")
	if err := os.WriteFile(target, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"fields":{"title":"Taken"}}`, `{"fields":{"status":"2"}}`, `{"fields":{"title":"Fake"},"sources":{"title":"Unknown"}}`, `{"fields":{"actors":"bad"}}`} {
		response := call(s, "PUT", "/api/scrape/jobs/edit/metadata", body)
		if response.Code != 400 {
			t.Fatalf("%s: %d %s", body, response.Code, response.Body.String())
		}
		if s.jobs[0].Editing || s.jobs[0].Metadata.Title != "Original" {
			t.Fatal("failed edit changed record")
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != "video" {
			t.Fatal("failed edit changed video")
		}
	}
	s.jobs[0].Editing = true
	for _, action := range []string{"retry", "reorganize"} {
		if response := call(s, "POST", "/api/scrape/jobs/edit/"+action, ""); response.Code != 409 {
			t.Fatal(response.Code)
		}
	}
	if response := call(s, "DELETE", "/api/scrape/jobs/edit", ""); response.Code != 409 {
		t.Fatal(response.Code)
	}
}

func TestRecordManualCropBoundsAndReplacement(t *testing.T) {
	s, path := recordEditorFixture(t)
	base := strings.TrimSuffix(path, ".mp4")
	old := map[string]string{"-poster.jpg": "old poster", "-thumb.jpg": "old thumb", "-fanart.jpg": "original fanart"}
	for suffix, body := range old {
		if err := os.WriteFile(base+suffix, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	picture := downloadTestPNG(t, 300, 200)
	body := map[string]any{"body_base64": base64.StdEncoding.EncodeToString(picture), "x": 150, "y": 0, "width": 150, "height": 200, "replace_thumb": true, "mosaic": "", "resolution": "", "subtitle": false}
	body["x"] = 151
	raw, _ := json.Marshal(body)
	if response := call(s, "POST", "/api/scrape/jobs/edit/crop", string(raw)); response.Code != 400 {
		t.Fatal(response.Body.String())
	}
	body["x"] = 150
	raw, _ = json.Marshal(body)
	response := call(s, "POST", "/api/scrape/jobs/edit/crop", string(raw))
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	for kind, dimensions := range map[string]image.Point{"poster": image.Pt(150, 200), "thumb": image.Pt(300, 200)} {
		data, err := os.ReadFile(base + "-" + kind + ".png")
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width != dimensions.X || cfg.Height != dimensions.Y {
			t.Fatal(cfg, err)
		}
		if _, err := os.Stat(base + "-" + kind + ".jpg"); !os.IsNotExist(err) {
			t.Fatal("old format remains")
		}
	}
	if data, _ := os.ReadFile(base + "-fanart.jpg"); string(data) != "original fanart" {
		t.Fatal("fanart changed")
	}
	if s.jobs[0].Editing || len(s.jobs) != 1 {
		t.Fatal("crop changed identity")
	}
	source := call(s, "POST", "/api/scrape/jobs/edit/crop-source", `{"source":"local:thumb"}`)
	var result struct {
		Body      string `json:"body_base64"`
		Method    string `json:"crop_method"`
		Aspect    string `json:"crop_aspect"`
		X         int    `json:"crop_x"`
		Width     int    `json:"crop_width"`
		FaceFound bool   `json:"face_found"`
	}
	if err := json.Unmarshal(source.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if source.Code != 200 || err != nil || config.Width != 300 || result.Method != "Right" || result.Aspect != "Full" || result.X != 159 || result.Width != 141 || result.FaceFound {
		t.Fatal(source.Body.String())
	}
	if response := call(s, "POST", "/api/scrape/jobs/edit/crop-source", `{"source":"local:../config"}`); response.Code != 400 {
		t.Fatal(response.Code)
	}
}

func TestTaskOverrideFlagsAndURLValidation(t *testing.T) {
	settings := scrapeSettings{}
	settings.Common.Subtitle = testSubtitleKeywords
	settings.FlagsOverride = &taskFlags{Clear: true, Subtitle: true}
	flags := detectFilenameFlags("ABP-123-LTX-4K.mp4", settings)
	if !flags.HasSubtitle || flags.Mosaic != "is_censored" || flags.Resolution != "" {
		t.Fatal(flags)
	}
	for _, item := range []struct {
		flags taskFlags
		want  string
	}{
		{taskFlags{LTX: true}, "is_ltx_hack"},
		{taskFlags{LADA: true}, "is_lada_hack"},
		{taskFlags{JAV: true}, "is_jav_hack"},
		{taskFlags{Hack: true}, "is_other_hack"},
		{taskFlags{Uncensored: true}, "is_uncensored"},
	} {
		settings.FlagsOverride = &item.flags
		if flags := detectFilenameFlags("ABP-123-中文.mp4", settings); !flags.HasSubtitle || flags.Mosaic != item.want {
			t.Fatalf("%s: %+v", item.want, flags)
		}
	}
	settings.FlagsOverride = &taskFlags{Clear: true, Resolution: "8K"}
	if flags := detectFilenameFlags("ABP-123-4K.mp4", settings); flags.Mosaic != "is_censored" || flags.Resolution != "8K" {
		t.Fatalf("resolution override: %+v", flags)
	}
	s, _ := recordEditorFixture(t)
	if response := call(s, "POST", "/api/scrape/jobs/edit/retry", `{"url":"https://unknown.example/ABP-123"}`); response.Code != 400 {
		t.Fatal(response.Body.String())
	}
	if s.jobs[0].Status != 2 || len(s.jobs) != 1 {
		t.Fatal("invalid override queued work")
	}
}
