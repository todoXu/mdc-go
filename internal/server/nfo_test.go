package server

import (
	"encoding/xml"
	"os"
	"strings"
	"testing"

	"mdcng-go/internal/provider"
)

func nfoFixture() (provider.Metadata, map[string][]byte) {
	meta := provider.Metadata{
		Provider: "test", SourceURL: "https://example.com/TEST-123", Number: "TEST-123", PublishNumber: "test00123",
		Title: "Title", OriginalTitle: "Original", SortTitle: "Sort", Outline: "Outline",
		OriginalOutline: "Original outline", OriginalPlot: "Original plot", TranslatedBy: "Translator",
		Release: "2026-11-04", Runtime: "120", Rating: "4.5", CriticRating: "8.1", Votes: "321",
		Country: "Japan", MPAA: "NC-17", CustomRating: "Custom", Director: "Director",
		Studio: "Studio", Publisher: "Publisher", Series: "Series",
		Actors: []string{"Actor A", "Actor B"}, MaleActors: []string{"Actor M"},
		Tags: []string{"Tag A", "Tag B"}, Mosaic: "is_ltx_hack", Resolution: "4K", HasSubtitle: true,
	}
	writes := map[string][]byte{
		"/library/TEST-123-CD1-poster.jpg":  []byte("poster"),
		"/library/TEST-123-CD1-thumb.png":   []byte("thumb"),
		"/library/TEST-123-CD1-fanart.webp": []byte("fanart"),
		"/library/TEST-123-CD1-trailer.mp4": []byte("trailer"),
	}
	return meta, writes
}

func decodeNFO(t *testing.T, raw []byte) map[string][]string {
	t.Helper()
	var root struct {
		XMLName xml.Name
		Items   []struct {
			XMLName xml.Name
			Value   string `xml:",chardata"`
			Name    string `xml:"name"`
			Thumb   string `xml:"thumb"`
		} `xml:",any"`
	}
	if err := xml.Unmarshal(raw, &root); err != nil {
		t.Fatal(err, string(raw))
	}
	if root.XMLName.Local != "movie" {
		t.Fatalf("wrong root: %s", root.XMLName.Local)
	}
	out := map[string][]string{}
	for _, item := range root.Items {
		value := strings.TrimSpace(item.Value)
		if item.XMLName.Local == "actor" {
			value = strings.TrimSpace(item.Name)
		}
		if item.XMLName.Local == "fanart" && strings.TrimSpace(item.Thumb) != "" {
			value = strings.TrimSpace(item.Thumb)
		}
		out[item.XMLName.Local] = append(out[item.XMLName.Local], value)
	}
	return out
}

func strictNFO(t *testing.T, settings scrapeSettings, meta provider.Metadata, writes map[string][]byte) map[string][]string {
	t.Helper()
	raw, err := buildNFO(meta, "/library/TEST-123-CD1", settings, writes)
	if err != nil {
		t.Fatal(err)
	}
	return decodeNFO(t, raw)
}

func TestNFOAllOptionalFieldsDisabled(t *testing.T) {
	meta, writes := nfoFixture()
	settings := scrapeSettings{}
	settings.NFO.Enabled = true
	fields := strictNFO(t, settings, meta, writes)
	for _, name := range []string{
		"sorttitle", "originaltitle", "outline", "plot", "originalplot", "tagline", "country", "mpaa",
		"customrating", "year", "runtime", "votes", "rating", "criticrating", "actor", "director", "series",
		"tag", "genre", "studio", "maker", "publisher", "label", "poster", "thumb", "fanart", "cover",
		"trailer", "website", "release", "releasedate", "premiered", "set",
	} {
		if values := fields[name]; len(values) != 0 {
			t.Fatalf("%s written while disabled: %v\n%s", name, values, fields)
		}
	}
	if len(fields["title"]) != 1 || fields["title"][0] != "Title" {
		t.Fatal(fields["title"])
	}
}

func TestNFOTitleUsesEmbyPlexNamingTemplate(t *testing.T) {
	meta, writes := nfoFixture()
	settings := scrapeSettings{}
	settings.NFO.Enabled = true
	settings.NFO.EnableSortTitle = true
	settings.NFO.EnableOriginalTitle = true
	settings.Naming.Title = "{number} {title} [{actor}] {year}"
	settings.Naming.MaxActor = 1
	fields := strictNFO(t, settings, meta, writes)
	want := "TEST-123 Title [多人作品] 2026"
	if got := fields["title"]; len(got) != 1 || got[0] != want {
		t.Fatalf("title=%v", got)
	}
	if got := fields["sorttitle"]; len(got) != 1 || got[0] != "Sort" {
		t.Fatalf("sorttitle=%v", got)
	}
	if got := fields["originaltitle"]; len(got) != 1 || got[0] != "Original" {
		t.Fatalf("original title must remain unchanged: %v", got)
	}
}

func TestNFOEveryEnabledFieldAndTemplate(t *testing.T) {
	meta, writes := nfoFixture()
	settings := scrapeSettings{}
	config := &settings.NFO
	config.Enabled, config.EnableSortTitle, config.EnableOriginalTitle = true, true, true
	config.AddPartToTitle, config.EnableOutline, config.EnableOriginalPlot, config.EnablePlot = true, true, true, true
	config.AddTranslatedBy, config.EnableRelease, config.EnableReleaseDate = true, true, true
	config.EnablePremiered, config.TaglineFormat = true, "发行日期: {release}"
	config.EnableCountry, config.EnableMPAA, config.EnableCustomRating = true, true, true
	config.EnableYear, config.EnableRuntime, config.EnableVotes = true, true, true
	config.EnableScore, config.EnableCriticRating = true, true
	config.EnableActor, config.IncludeMaleActors, config.EnableDirector = true, true, true
	config.EnableSeries, config.EnableTag, config.EnableGenre = true, true, true
	config.EnableStudio, config.EnableMaker, config.EnablePublisher, config.EnableLabel = true, true, true, true
	config.EnablePoster, config.EnableCover, config.EnableTrailer, config.EnableWebsite = true, true, true, true
	config.SetByActress, config.SetBySeries, config.SetBySerialName = true, true, true
	config.AdditionalTags.SerialName, config.AdditionalTags.Actors = true, true
	config.AdditionalTags.Resolution, config.AdditionalTags.Mosaic, config.AdditionalTags.HackType = true, true, true
	config.AdditionalTags.HasSubtitle, config.AdditionalTags.SubtitleFormat = true, "字幕"
	config.AdditionalTags.Series, config.AdditionalTags.SeriesFormat = true, "系列: {series}"
	config.AdditionalTags.Studio, config.AdditionalTags.StudioFormat = true, "片商: {studio}"
	config.AdditionalTags.Publisher, config.AdditionalTags.PublisherFormat = true, "发行: {publisher}"
	fields := strictNFO(t, settings, meta, writes)
	expect := map[string]string{
		"title": "Title CD1", "publishnumber": "test00123", "num": "TEST-123", "originaltitle": "Original", "sorttitle": "Sort",
		"outline": "Outline", "plot": "Outline\n由 Translator 提供翻译", "originalplot": "Original plot",
		"tagline": "发行日期: 2026-11-04", "country": "Japan", "mpaa": "NC-17", "customrating": "Custom",
		"year": "2026", "runtime": "120", "votes": "321", "rating": "4.5", "criticrating": "8.1",
		"director": "Director", "series": "Series", "studio": "Studio", "maker": "Studio",
		"publisher": "Publisher", "label": "Publisher", "poster": "TEST-123-CD1-poster.jpg",
		"thumb": "TEST-123-CD1-thumb.png", "fanart": "TEST-123-CD1-fanart.webp",
		"trailer": "TEST-123-CD1-trailer.mp4",
		"website": "https://example.com/TEST-123", "release": "2026-11-04",
		"releasedate": "2026-11-04", "premiered": "2026-11-04",
	}
	for name, want := range expect {
		if values := fields[name]; len(values) != 1 || values[0] != want {
			t.Fatalf("%s=%v want %q\nall=%v", name, values, want, fields)
		}
	}
	if values := fields["code"]; len(values) != 0 {
		t.Fatalf("legacy code tag must not be written: %v", values)
	}
	if got := fields["actor"]; strings.Join(got, "|") != "Actor A|Actor B|Actor M" {
		t.Fatal(got)
	}
	for _, value := range []string{"TEST", "Actor A", "Actor M", "4K", "LTX破解", "字幕", "系列: Series", "片商: Studio", "发行: Publisher", "Tag A", "Tag B"} {
		found := false
		for _, tag := range fields["tag"] {
			found = found || tag == value
		}
		if !found {
			t.Fatalf("missing tag %q: %v", value, fields["tag"])
		}
	}
	for _, tag := range fields["tag"] {
		if tag == "有码" || tag == "无码" {
			t.Fatalf("hacked video must not have a mosaic tag: %v", fields["tag"])
		}
	}
	if got, want := fields["genre"], fields["tag"]; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("genre and tag differ: genre=%v tag=%v", got, want)
	}
	sets := fields["set"]
	if strings.Join(sets, "|") != "Actor A|Actor B|Series|TEST" {
		t.Fatal(sets)
	}
}

func TestNFOIndependentTogglesAndMediaAvailability(t *testing.T) {
	meta, writes := nfoFixture()
	settings := scrapeSettings{}
	settings.NFO.Enabled = true
	settings.NFO.EnableActor = true
	settings.NFO.IncludeMaleActors = false
	fields := strictNFO(t, settings, meta, writes)
	if got := strings.Join(fields["actor"], "|"); got != "Actor A|Actor B" {
		t.Fatal(got)
	}
	settings.NFO.IncludeMaleActors = true
	fields = strictNFO(t, settings, meta, writes)
	if got := strings.Join(fields["actor"], "|"); got != "Actor A|Actor B|Actor M" {
		t.Fatal(got)
	}
	delete(writes, "/library/TEST-123-CD1-thumb.png")
	delete(writes, "/library/TEST-123-CD1-fanart.webp")
	delete(writes, "/library/TEST-123-CD1-poster.jpg")
	delete(writes, "/library/TEST-123-CD1-trailer.mp4")
	settings.NFO.EnableCover, settings.NFO.EnablePoster, settings.NFO.EnableTrailer = true, true, true
	fields = strictNFO(t, settings, meta, writes)
	for _, name := range []string{"poster", "thumb", "fanart", "trailer"} {
		if len(fields[name]) != 0 {
			t.Fatalf("unavailable media %s was written: %v", name, fields[name])
		}
	}
	meta, writes = nfoFixture()
	settings.NFO.EnableCover, settings.NFO.EnablePoster, settings.NFO.EnableTrailer = true, false, false
	fields = strictNFO(t, settings, meta, writes)
	if got := fields["thumb"]; len(got) != 1 || got[0] != "TEST-123-CD1-thumb.png" {
		t.Fatalf("cover-only thumb: %v", got)
	}
}

func TestNFOAdditionalTagsRequireTagAndTemplateFormats(t *testing.T) {
	meta, writes := nfoFixture()
	settings := scrapeSettings{}
	settings.NFO.Enabled = true
	settings.NFO.AdditionalTags.Actors = true
	fields := strictNFO(t, settings, meta, writes)
	if len(fields["tag"]) != 0 {
		t.Fatal("additional tags written while tag is disabled")
	}
	settings.NFO.EnableTag = true
	settings.NFO.IncludeMaleActors = true
	settings.NFO.AdditionalTags = struct {
		SerialName      bool   `json:"serial_name"`
		Actors          bool   `json:"actors"`
		Resolution      bool   `json:"resolution"`
		Mosaic          bool   `json:"mosaic"`
		HackType        bool   `json:"hack_type"`
		HasSubtitle     bool   `json:"has_subtitle"`
		SubtitleFormat  string `json:"subtitle_format"`
		Series          bool   `json:"series"`
		SeriesFormat    string `json:"series_format"`
		Studio          bool   `json:"studio"`
		StudioFormat    string `json:"studio_format"`
		Publisher       bool   `json:"publisher"`
		PublisherFormat string `json:"publisher_format"`
	}{Actors: true, HasSubtitle: true, SubtitleFormat: "", Publisher: true, PublisherFormat: "{publisher} / {series}"}
	fields = strictNFO(t, settings, meta, writes)
	if strings.Join(fields["tag"], "|") != "Tag A|Tag B|Actor A|Actor B|Actor M|Publisher / Series" {
		t.Fatal(fields["tag"])
	}
}

func TestNFODisabledCreatesNoNFO(t *testing.T) {
	meta, _ := nfoFixture()
	writes := map[string][]byte{"/library/TEST-123.nfo": []byte("stale")}
	if err := addMetadataWrites(writes, meta, "/library/TEST-123", scrapeSettings{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := writes["/library/TEST-123.nfo"]; ok {
		t.Fatal("disabled NFO retained in transaction")
	}
	if _, ok := writes["/library/TEST-123.mdc.json"]; ok {
		t.Fatal("legacy metadata JSON was written")
	}
}

func TestManagedMetadataRemovesNFOWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/TEST-123.mp4"
	base := dir + "/TEST-123"
	if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+".nfo", []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+".mdc.json", []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	writes := map[string][]byte{}
	meta, _ := nfoFixture()
	if err := addMetadataWrites(writes, meta, base, scrapeSettings{}); err != nil {
		t.Fatal(err)
	}
	if err := commitScrapeRetryManagedNFO(t.Context(), path, path, writes, true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(base + ".nfo"); !os.IsNotExist(err) {
		t.Fatal("disabled NFO was preserved")
	}
	if _, err := os.Stat(base + ".mdc.json"); !os.IsNotExist(err) {
		t.Fatal("legacy metadata JSON was preserved")
	}
}

func TestNFOMosaicTagOmittedForHackedVideos(t *testing.T) {
	settings := scrapeSettings{}
	settings.NFO.AdditionalTags.Mosaic = true
	settings.NFO.AdditionalTags.HackType = true
	for mosaic, want := range map[string]string{
		"is_ltx_hack": "LTX破解", "is_lada_hack": "LADA破解", "is_jav_hack": "JAV破解", "is_other_hack": "破解",
		"is_censored": "有码", "is_uncensored": "无码",
	} {
		tags := nfoAdditionalTags(provider.Metadata{Number: "ABC-123"}, filenameFlags{Mosaic: mosaic}, settings)
		if strings.Join(tags, "|") != want {
			t.Fatalf("%s: tags=%v want only %q", mosaic, tags, want)
		}
	}
}
