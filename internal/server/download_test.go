package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mdcng-go/internal/provider"
)

func downloadTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, w, h))
	for i, c := range []color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}} {
		draw.Draw(picture, image.Rect(i*w/3, 0, (i+1)*w/3, h), image.NewUniform(c), image.Point{}, draw.Src)
	}
	var out bytes.Buffer
	if err := png.Encode(&out, picture); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func installDownloadSource(t *testing.T, s *Server, id string, meta provider.Metadata, pictures map[string][]byte) {
	t.Helper()
	dir := filepath.Join(s.providers.Dir, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(provider.Manifest{APIVersion: 2, ID: id, Name: id, Engine: "javascript-v1", Version: "1.0.0", BaseURL: "https://example.test"})
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(meta)
	encoded := map[string]string{}
	for url, data := range pictures {
		encoded[url] = base64.StdEncoding.EncodeToString(data)
	}
	images, _ := json.Marshal(encoded)
	script := fmt.Sprintf(`var images=%s;var plugin={fetch:function(){return %s},image:function(url,referer){if(!images[url])throw "image unavailable";return {body_base64:images[url],content_type:"image/png"}}};`, images, metadata)
	if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	s.providerConfigs[id] = provider.Config{Enabled: true, BaseURL: "https://example.test"}
}

func TestDownloadStrategiesUseActualPixelsAndFallback(t *testing.T) {
	for _, scenario := range []string{"priority", "quality", "first fails", "too small", "all fail", "mismatched number"} {
		t.Run(scenario, func(t *testing.T) {
			s := configureScrapeServer(t)
			first := provider.Metadata{Number: "ABP-123", Title: "First", SourceURL: "https://example.test/first", Cover: "https://example.test/first.png"}
			second := provider.Metadata{Number: "ABP-123", Title: "Second", SourceURL: "https://example.test/second", Cover: "https://example.test/second.png"}
			pictures := map[string][]byte{first.Cover: downloadTestPNG(t, 300, 200)}
			if scenario == "first fails" || scenario == "all fail" || scenario == "mismatched number" {
				pictures = map[string][]byte{}
			}
			if scenario == "too small" {
				pictures[first.Cover] = downloadTestPNG(t, 30, 20)
			}
			secondPictures := map[string][]byte{second.Cover: downloadTestPNG(t, 600, 400)}
			if scenario == "all fail" {
				secondPictures = map[string][]byte{}
			}
			if scenario == "mismatched number" {
				second.Number = "ABP-999"
			}
			installDownloadSource(t, s, "First", first, pictures)
			installDownloadSource(t, s, "Second", second, secondPictures)
			s.priorities.ByFields["cover"] = []string{"First", "Second"}
			settings, _ := s.scrapeSettings()
			settings.Download.Thumb, settings.Download.Fanart = true, true
			settings.Download.Poster = false
			settings.Download.Strategy = "Priority"
			if scenario == "quality" {
				settings.Download.Strategy = "Quality"
			}
			meta := first
			meta.Provider = "merged"
			provenance := map[string]string{"cover": "First"}
			reads := map[string]int{}
			ctx := context.WithValue(context.Background(), scrapeTraceKey{}, scrapeTrace{log: func(stage, message string) {
				if stage == "下载图片" && strings.HasPrefix(message, "读取 [") {
					reads[message]++
				}
			}})
			writes, warnings, err := s.prepareScrapeImages(ctx, "ABP-123.mp4", "video", &meta, provenance, map[string]provider.Metadata{"First": first}, settings, false)
			if scenario == "all fail" || scenario == "mismatched number" {
				if err == nil {
					t.Fatal("unavailable sources did not fail")
				}
				if len(writes) != 0 {
					t.Fatal("partial output returned")
				}
				return
			}
			if err != nil {
				t.Fatal(err, warnings)
			}
			want := 300
			wantProvider := "First"
			if scenario != "priority" {
				want = 600
				wantProvider = "Second"
			}
			got, _, err := image.DecodeConfig(bytes.NewReader(writes["video-thumb.png"]))
			if err != nil || got.Width != want || provenance["cover"] != wantProvider {
				t.Fatal("wrong source or dimensions", got, provenance, err)
			}
			if !bytes.Equal(writes["video-thumb.png"], writes["video-fanart.png"]) {
				t.Fatal("fanart did not reuse thumb pixels")
			}
			for _, count := range reads {
				if count != 1 {
					t.Fatal("duplicate image download")
				}
			}
			if scenario == "priority" && len(reads) != 1 {
				t.Fatal("priority downloaded lower candidates unnecessarily")
			}
		})
	}
}

func TestPosterCropGeometryAndFaceFallback(t *testing.T) {
	input, err := inspectImage(downloadTestPNG(t, 900, 600))
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"Right", "Center", "None", "Face"} {
		messages := []string{}
		ctx := context.WithValue(context.Background(), scrapeTraceKey{}, scrapeTrace{log: func(stage, message string) { messages = append(messages, message) }})
		cropped, err := cropPoster(ctx, input, method, "Emby")
		if err != nil {
			t.Fatal(err)
		}
		if method == "None" {
			if !bytes.Equal(cropped.data, input.data) {
				t.Fatal("None changed image")
			}
			continue
		}
		if cropped.width != 400 || cropped.height != 600 {
			t.Fatal("wrong crop ratio", method, cropped.width, cropped.height)
		}
		decoded, err := png.Decode(bytes.NewReader(cropped.data))
		if err != nil {
			t.Fatal(err)
		}
		r, g, _, _ := decoded.At(10, 10).RGBA()
		if method == "Right" && g <= r {
			t.Fatal("not a right crop")
		}
		if method == "Center" && r <= g {
			t.Fatal("not a center crop")
		}
		if method == "Face" && !strings.Contains(strings.Join(messages, " "), "回退到中心裁剪") {
			t.Fatal("no-face fallback was not reported")
		}
	}
	full, err := cropPoster(context.Background(), input, "Right", "Full")
	if err != nil || full.width != 424 {
		t.Fatal("wrong Full aspect", full.width, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cropPoster(ctx, input, "Face", "Emby"); err == nil {
		t.Fatal("ignored cancellation")
	}
	settings := scrapeSettings{}
	settings.Download.CropRegular = "Right"
	settings.Download.CropFC2 = "Face"
	if posterCropMethod("FC2-PPV-123456", "video.mp4", settings) != "Face" || posterCropMethod("ABP-123", "video.mp4", settings) != "Right" {
		t.Fatal("wrong crop category")
	}
}

func TestPosterSelectionUsesFinalCropResolution(t *testing.T) {
	for _, scenario := range []string{"fallback", "keep independent", "prefer crop", "crop independent"} {
		t.Run(scenario, func(t *testing.T) {
			s := configureScrapeServer(t)
			meta := provider.Metadata{Provider: "First", Number: "ABP-123", Title: "Test", SourceURL: "https://example.test/film", Cover: "https://example.test/cover.png", Poster: "https://example.test/poster.png"}
			pictures := map[string][]byte{meta.Cover: downloadTestPNG(t, 1800, 1200), meta.Poster: downloadTestPNG(t, 500, 600)}
			if scenario == "fallback" {
				delete(pictures, meta.Poster)
			}
			installDownloadSource(t, s, "First", meta, pictures)
			s.priorities.ByFields["cover"] = []string{"First"}
			s.priorities.ByFields["poster"] = []string{"First"}
			settings, _ := s.scrapeSettings()
			settings.Download.Poster = true
			settings.Download.Thumb = false
			settings.Download.Fanart = false
			settings.Download.Aspect = "Emby"
			settings.Download.CropRegular = "Right"
			settings.Download.PreferCrop = scenario == "prefer crop"
			settings.Download.CropPoster = scenario == "crop independent"
			originalMeta := meta
			provenance := map[string]string{"cover": "First", "poster": "First"}
			sources := map[string]provider.Metadata{"First": originalMeta}
			writes, _, err := s.prepareScrapeImages(context.Background(), "ABP-123.mp4", "video", &meta, provenance, sources, settings, false)
			if err != nil {
				t.Fatal(err)
			}
			config, _, err := image.DecodeConfig(bytes.NewReader(writes["video-poster.png"]))
			wantW, wantH := 500, 600
			if scenario == "fallback" || scenario == "prefer crop" {
				wantW, wantH = 800, 1200
			}
			if scenario == "crop independent" {
				wantW = 400
			}
			if err != nil || config.Width != wantW || config.Height != wantH {
				t.Fatal("wrong poster result", config, err)
			}
			if len(writes) != 1 {
				t.Fatal("disabled thumbnail/fanart was saved")
			}
			second, _, err := s.prepareScrapeImages(context.Background(), "ABP-123.mp4", "video", &meta, provenance, sources, settings, false)
			if err != nil || !bytes.Equal(writes["video-poster.png"], second["video-poster.png"]) {
				t.Fatal("reorganization changed the crop or reused an already cropped image", err)
			}
		})
	}
}

func TestPosterCropCandidateFollowsConfiguredMethod(t *testing.T) {
	input, err := inspectImage(downloadTestPNG(t, 300, 200))
	if err != nil {
		t.Fatal(err)
	}
	src, err := decodeWatermarkImage(input.data)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		method string
		x      int
	}{
		{"None", 83},
		{"Center", 83},
		{"Right", 167},
	} {
		rect, err := posterCropRectangle(t.Context(), src, 300, 200, scenario.method, "Emby")
		if err != nil {
			t.Fatal(err)
		}
		if rect.X != scenario.x || rect.Y != 0 || rect.Width != 133 || rect.Height != 200 {
			t.Fatalf("%s: %+v", scenario.method, rect)
		}
	}
	rect, err := posterCropRectangle(t.Context(), src, 300, 200, "Face", "Emby")
	if err != nil {
		t.Fatal(err)
	}
	if rect.FaceFound || rect.X != 83 {
		t.Fatalf("no-face fallback: %+v", rect)
	}
}

func TestScrapeDownloadsExtrasTrailerAndSavesChosenMetadata(t *testing.T) {
	video := append([]byte{0, 0, 0, 24}, []byte("ftypmp42")...)
	video = append(video, make([]byte, 12)...)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Write(video)
	}))
	defer upstream.Close()
	s := configureScrapeServer(t)
	meta := provider.Metadata{Number: "ABP-123", Title: "Test", SourceURL: upstream.URL + "/film", Cover: "https://example.test/cover.png", Samples: []string{"https://example.test/one.png", "https://example.test/one.png", "https://example.test/two.png"}, Trailer: upstream.URL + "/trailer.mp4"}
	cover := downloadTestPNG(t, 900, 600)
	installDownloadSource(t, s, "First", meta, map[string][]byte{meta.Cover: cover, meta.Samples[0]: cover, meta.Samples[2]: cover})
	for _, field := range []string{"title", "cover", "samples", "trailer"} {
		s.priorities.ByFields[field] = []string{"First"}
	}
	settings, _ := s.scrapeSettings()
	settings.Download.Poster = true
	settings.Download.Thumb = true
	settings.Download.Fanart = true
	settings.Download.ExtraFanarts = true
	settings.Download.Trailer = true
	settings.NFO.EnablePoster = true
	settings.NFO.EnableCover = true
	settings.NFO.EnableTrailer = true
	settings.Download.CropRegular = "Right"
	settings.Download.Aspect = "Emby"
	settings.Naming.File = "{number}"
	settings.Naming.Suffix = ""
	settings.Naming.ImageWithPrefix = false
	path := filepath.Join(t.TempDir(), "ABP-123.mp4")
	if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	var saved provider.Metadata
	output, warnings, err := s.organizeInPlaceWithSettings(context.Background(), path, "", settings, func(meta provider.Metadata) { saved = meta })
	if err != nil || len(warnings) != 0 {
		t.Fatal(err, warnings)
	}
	base := strings.TrimSuffix(output, ".mp4")
	for _, name := range []string{"thumb.png", "poster.png", "fanart.png", "extrafanart-001.png", "extrafanart-002.png", "trailer.mp4"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(base), name)); err != nil {
			t.Fatal(name, err)
		}
	}
	if saved.Poster != meta.Cover || saved.Trailer != meta.Trailer {
		t.Fatal("chosen resource metadata was not saved", saved)
	}
	if _, err := os.Stat(base + ".mdc.json"); !os.IsNotExist(err) {
		t.Fatal("legacy metadata file was created")
	}
	nfo, err := os.ReadFile(base + ".nfo")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"<poster>poster.png</poster>",
		"<thumb>thumb.png</thumb>",
		"<fanart>fanart.png</fanart>",
		"<trailer>trailer.mp4</trailer>",
	} {
		if !strings.Contains(string(nfo), line) {
			t.Fatalf("NFO does not use unprefixed image name %q:\n%s", line, nfo)
		}
	}
	settings.Download.Poster, settings.Download.Thumb, settings.Download.Fanart = false, false, false
	settings.Download.ExtraFanarts, settings.Download.Trailer = false, false
	writes, _, err := s.prepareScrapeImages(context.Background(), path, base, &meta, nil, nil, settings, false)
	if err != nil || len(writes) != 0 {
		t.Fatal("disabled resource downloads produced files", err)
	}
}

func TestRetryUsesCurrentDirectoryDownloadSettings(t *testing.T) {
	s := configureScrapeServer(t)
	dir := t.TempDir()
	body, _ := json.Marshal(object{"watch_dirs": []object{{"id": "library", "path": dir, "enabled": false, "use_global_download": false, "download": object{"thumb_download_strategy": "Quality", "crop_type_censored": "Center", "crop_aspect": "Emby", "extra_fanarts": true}}}})
	if response := call(s, "PUT", "/api/config/common", string(body)); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	old, _ := s.scrapeSettings()
	old.Download.Strategy = "Priority"
	s.jobs = []scrapeJob{{ID: "record", Status: -1, DirectoryID: "library", Path: filepath.Join(dir, "ABP-123.mp4"), Settings: &old}}
	s.scrapeSlots.acquire(1)
	response := call(s, "POST", "/api/scrape/jobs/record/retry", "")
	s.jobsMu.RLock()
	job := s.jobs[0]
	s.jobsMu.RUnlock()
	s.scrapeSlots.release()
	if response.Code != 202 || job.ID != "record" || job.Settings.Download.Strategy != "Quality" || job.Settings.Download.CropRegular != "Center" || !job.Settings.Download.ExtraFanarts {
		t.Fatal("retry did not use directory overrides", response.Body.String())
	}
	// Drain the asynchronous worker so its persisted state cannot outlive this test.
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

func TestInvalidDownloadSettingsAreRejected(t *testing.T) {
	s := configureScrapeServer(t)
	for _, body := range []string{`{"crop_type_censored":"fake"}`, `{"crop_type_fc2":"fake"}`, `{"crop_aspect":"wide"}`, `{"thumb_download_strategy":"unknown"}`, `{"thumb_min_width":-1}`, `{"thumb_min_height":1.5}`} {
		if response := call(s, "PUT", "/api/config/download", body); response.Code != 400 {
			t.Fatal("accepted invalid download config", body, response.Body.String())
		}
	}
}
