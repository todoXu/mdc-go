package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mdcng-go/internal/provider"
)

// testSubtitleKeywords 与 defaults.json 中的 subtitle_keywords 相同，供手工构造配置的测试使用。
var testSubtitleKeywords = []string{"中文字幕", "中字", "中文", "简中", "繁中", "中英双语", "chs", "cht", "chinese", "c"}

func TestFilenameSubtitleAndHackFlags(t *testing.T) {
	settings := scrapeSettings{}
	settings.Common.Subtitle = testSubtitleKeywords
	settings.Common.LTX = []string{"LTX"}
	settings.Common.LADA = []string{"LADA"}
	settings.Common.JAV = []string{"JAV"}
	settings.Common.Other = []string{"破解"}
	settings.Common.Uncensored = []string{"无码"}
	for _, test := range []struct {
		path, mosaic string
		subtitle     bool
	}{
		{"ABP-123-LTX-破解-中字.mp4", "is_ltx_hack", true},
		{"ABP-123_lada_C.mp4", "is_lada_hack", true},
		{"ABP-123-JAV-CHS.mp4", "is_jav_hack", true},
		{"ABP-123-LTX破解.mp4", "is_ltx_hack", false},
		{"ABP-123-破解.mp4", "is_other_hack", false},
		{"ABP-123-CD1.mp4", "is_censored", false},
		{"JAV-123.mp4", "is_censored", false},
		{"ABP-123-C.mp4", "is_censored", true},
		{"ABP-123.CHT.mkv", "is_censored", true},
		{"ABP-123-无码.mp4", "is_uncensored", false},
		{filepath.Join("中字-LTX", "ABP-123.mp4"), "is_censored", false},
	} {
		flags := detectFilenameFlags(test.path, settings)
		if flags.HasSubtitle != test.subtitle || flags.Mosaic != test.mosaic {
			t.Errorf("%s: %+v", test.path, flags)
		}
	}
	settings.Common.LADA = []string{"自定义破解"}
	if flags := detectFilenameFlags("ABP-123-自定义破解.mp4", settings); flags.Mosaic != "is_lada_hack" {
		t.Fatal(flags)
	}
	settings.Naming.File = "{number}"
	settings.Naming.Suffix = "{mosaic}{subtitle}"
	settings.Naming.Suffixes = map[string]any{"is_ltx_hack": "-LTX-破解", "has_subtitle": "-中文"}
	meta := provider.Metadata{Number: "ABP-123", Title: "Example"}
	if stem, err := buildVideoStem("ABP-123-LTX-C.mp4", meta, settings); err != nil || stem != "ABP-123-LTX-破解-中文" {
		t.Fatal(stem, err)
	}
}

func TestHackKeywordsMustComeFromConfiguration(t *testing.T) {
	for _, path := range []string{"ABP-123-LTX.mp4", "ABP-123-LADA.mp4", "ABP-123-JAV.mp4"} {
		if flags := detectFilenameFlags(path, scrapeSettings{}); flags.Mosaic != "is_censored" {
			t.Fatalf("unconfigured keyword detected for %s: %+v", path, flags)
		}
	}
	settings := scrapeSettings{}
	settings.Common.LTX = []string{"LTX"}
	settings.Common.LADA = []string{"LADA"}
	settings.Common.JAV = []string{"JAV"}
	for path, want := range map[string]string{
		"ABP-123-LTX.mp4": "is_ltx_hack", "ABP-123-LADA.mp4": "is_lada_hack", "ABP-123-JAV.mp4": "is_jav_hack",
	} {
		if flags := detectFilenameFlags(path, settings); flags.Mosaic != want {
			t.Fatalf("%s: %+v", path, flags)
		}
	}
}

func watermarkTestPicture(t *testing.T) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 400, 600))
	draw.Draw(picture, picture.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	var data bytes.Buffer
	if err := png.Encode(&data, picture); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
func watermarkTestConfig() map[string]any {
	return map[string]any{"enabled": true, "poster": true, "thumb": true, "watermark_subtitle": true, "watermark_ltx": true, "watermark_jav": true, "watermark_lada": true, "watermark_hack": true, "watermark_subtitle_fixed": float64(3), "watermark_ltx_fixed": float64(2), "watermark_hack_fixed": float64(2), "scale": float64(10), "mode": "Stack"}
}

func TestWatermarkWritesPixelsAndRespectsToggles(t *testing.T) {
	raw := watermarkTestPicture(t)
	config := watermarkTestConfig()
	settings := scrapeSettings{}
	settings.Common.Subtitle = testSubtitleKeywords
	settings.Common.LTX = []string{"LTX"}
	flags := detectFilenameFlags("ABP-123-LTX-中字.mp4", settings)
	data, err := applyWatermarks(context.Background(), raw, ".png", "poster", flags, config)
	if err != nil {
		t.Fatal(err)
	}
	picture, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if picture.Bounds() != image.Rect(0, 0, 400, 600) {
		t.Fatal("changed image size")
	}
	r, g, b, _ := picture.At(20, 555).RGBA()
	if g <= r || g <= b {
		t.Fatal("missing subtitle badge at bottom left")
	}
	r, g, b, _ = picture.At(265, 555).RGBA()
	if r <= g || r <= b {
		t.Fatal("missing LTX badge at bottom right")
	}
	r, g, b, _ = picture.At(200, 300).RGBA()
	if r != 65535 || g != 65535 || b != 65535 {
		t.Fatal("modified unrelated pixels")
	}
	for _, kind := range []string{"fanart"} {
		got, err := applyWatermarks(context.Background(), raw, ".png", kind, flags, config)
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatal("ignored image toggle", err)
		}
	}
	config["enabled"] = false
	got, err := applyWatermarks(context.Background(), raw, ".png", "poster", flags, config)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatal("disabled watermark modified image")
	}
	config["enabled"] = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := applyWatermarks(ctx, raw, ".png", "poster", flags, config); err == nil {
		t.Fatal("ignored cancellation")
	}
	config["custom_folder"] = t.TempDir()
	if _, err := applyWatermarks(context.Background(), raw, ".png", "poster", flags, config); err == nil {
		t.Fatal("missing custom badge silently ignored")
	}
}

func TestReorganizeRedownloadsImagesWithoutOriginalAndDoesNotStack(t *testing.T) {
	s := configureScrapeServer(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "ABP-123-LTX-中字.mp4")
	base := path[:len(path)-4]
	raw := watermarkTestPicture(t)
	script := fmt.Sprintf(`var plugin={fetch:function(){throw "must not refetch metadata"},image:function(url,referer){if(url!=="https://example.com/poster.png" || referer!=="https://example.com/film")throw "wrong image provenance";return {body_base64:%q,content_type:"image/png"}}};`, base64.StdEncoding.EncodeToString(raw))
	pluginPath := filepath.Join(s.providers.Dir, "Javbus", "main.js")
	if err := os.WriteFile(pluginPath, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	meta := provider.Metadata{Provider: "merged", Number: "ABP-123", Title: "Saved title", Poster: "https://example.com/poster.png"}
	if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	target := base + "-poster.png"
	if err := os.WriteFile(target, []byte("old marked file, never use as source"), 0600); err != nil {
		t.Fatal(err)
	}
	legacy := base + "-poster.original.png"
	if err := os.WriteFile(legacy, []byte("obsolete backup, never use as source"), 0600); err != nil {
		t.Fatal(err)
	}
	settings, _ := s.scrapeSettings()
	settings.Common.LTX = []string{"LTX"}
	settings.Watermark = watermarkTestConfig()
	settings.Download.Poster = true
	settings.Naming.File = "{number}"
	settings.Naming.Suffix = "-LTX-中字"
	prepare := func(meta provider.Metadata, output string) (map[string][]byte, error) {
		writes, _, err := s.prepareScrapeImages(context.Background(), path, strings.TrimSuffix(output, ".mp4"), &meta, map[string]string{"poster": "Javbus"}, map[string]provider.Metadata{"Javbus": {SourceURL: "https://example.com/film"}}, settings, true)
		return writes, err
	}
	run := func() error {
		_, err := reorganizeExisting(context.Background(), path, meta, settings, func(provider.Metadata) {}, prepare)
		return err
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy original was not retired", err)
	}
	if bytes.Equal(first, raw) {
		t.Fatal("image not watermarked")
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(target)
	if !bytes.Equal(first, second) {
		t.Fatal("repeated watermarking changed result")
	}
	settings.Watermark["watermark_subtitle"] = false
	settings.Watermark["watermark_ltx"] = false
	if err := run(); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(target)
	if !bytes.Equal(restored, raw) {
		t.Fatal("did not regenerate from freshly downloaded pixels")
	}
	// Plugin failure must not fall back to the current file or a legacy backup.
	if err := os.WriteFile(pluginPath, []byte(`var plugin={image:function(){throw "download failed"}};`), 0600); err != nil {
		t.Fatal(err)
	}
	settings.Naming.File = "{number} {title}"
	if err := run(); err == nil {
		t.Fatal("download failure silently reused local image")
	}
	afterFailure, _ := os.ReadFile(target)
	if !bytes.Equal(afterFailure, restored) {
		t.Fatal("failed download changed existing image")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("failed download renamed video", err)
	}
	if _, err := os.Stat(base + "-poster.original.png"); !os.IsNotExist(err) {
		t.Fatal("original file recreated")
	}
}

func TestScrapeWritesFilenameFlagsAndActualWatermark(t *testing.T) {
	s := configureScrapeServer(t)
	raw := watermarkTestPicture(t)
	script := fmt.Sprintf(`var plugin={test:function(){return true},fetch:function(input){return {number:input,title:"Example",source_url:"https://example.com/film",poster:"https://example.com/poster.png"}},image:function(){return {body_base64:%q,content_type:"image/png"}}};`, base64.StdEncoding.EncodeToString(raw))
	if err := os.WriteFile(filepath.Join(s.providers.Dir, "Javbus", "main.js"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	s.priorities.ByFields["poster"] = []string{"Javbus"}
	settings, _ := s.scrapeSettings()
	settings.Common.LTX = []string{"LTX"}
	settings.Download.Poster = true
	settings.Watermark = watermarkTestConfig()
	path := filepath.Join(t.TempDir(), "ABP-123-LTX-C.mp4")
	if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	var saved provider.Metadata
	output, warnings, err := s.organizeInPlaceWithSettings(context.Background(), path, "", settings, func(meta provider.Metadata) { saved = meta })
	if err != nil || len(warnings) > 0 {
		t.Fatal(err, warnings)
	}
	base := strings.TrimSuffix(output, filepath.Ext(output))
	marked, err := os.ReadFile(base + "-poster.png")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(marked, raw) {
		t.Fatal("scrape did not apply watermark")
	}
	if _, err := os.Stat(base + "-poster.original.png"); !os.IsNotExist(err) {
		t.Fatal("scrape created an unwanted original file", err)
	}
	if !saved.HasSubtitle || saved.Mosaic != "is_ltx_hack" {
		t.Fatal("filename flags not saved", saved)
	}
	if _, err := os.Stat(base + ".mdc.json"); !os.IsNotExist(err) {
		t.Fatal("legacy metadata file was created")
	}
}
