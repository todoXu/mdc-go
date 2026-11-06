package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const avbaseFixture = `<html><head><link rel="canonical" href="https://www.avbase.net/works/example:TEST-123"></head><body><div role="tabpanel"><h1>Example title</h1><img src="https://example.com/pl.jpg"><dl><div><dt>発売日</dt><dd>2026/11/27</dd></div><div><dt>収録分数</dt><dd>120</dd></div><div><dt>メーカー</dt><dd>Example studio</dd></div><div><dt>レーベル</dt><dd>Example publisher</dd></div><div><dt>シリーズ</dt><dd>Example series</dd></div><div><dt>監督</dt><dd>Example director</dd></div></dl><a href="https://pics.dmm.co.jp/example-1.jpg">sample</a></div><section><h2>紹介文</h2><p>Example outline text.</p></section><section><div><h2>出演者・メモ</h2></div><a href="/talents/one">Actor One</a><a href="/talents/two">Actor Two</a></section><section><div><h2>タグ・説明文</h2></div><a href="/tags/a">Tag A</a></section><section><h2>関連作品</h2><a href="/talents/other">Unrelated actor</a></section></body></html>`

func TestAvbaseDMMPosterUsesExactProductPath(t *testing.T) {
	for _, cover := range []string{
		"https://awsimgsrc.dmm.com/dig/mono/movie/cawb068/cawb068pl.jpg",
		"https://pics.dmm.co.jp/dig/mono/movie/cawb068/cawb068pl.jpg?cache=1",
		"https://pics.dmm.co.jp/digital/video/jur00888/jur00888pl.jpg",
		"https://awsimgsrc.dmm.com/digital/video/cawb00068/cawb00068pl.jpg",
		"https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/ipzz00034/ipzz00034pl.jpg",
		"https://example.com/pl.jpg",
	} {
		t.Run(cover, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(strings.ReplaceAll(avbaseFixture, "https://example.com/pl.jpg", cover)))
			}))
			defer upstream.Close()
			registry, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			source, err := registry.Get("avbase")
			if err != nil {
				t.Fatal(err)
			}
			meta, err := source.Fetch(t.Context(), Config{BaseURL: upstream.URL}, "example:TEST-123")
			if err != nil {
				t.Fatal(err)
			}
			want := "https://awsimgsrc.dmm.com/dig/mono/movie/cawb068/cawb068ps.jpg"
			wantPublishNumber := "cawb068"
			if strings.Contains(cover, "cawb00068") {
				wantPublishNumber = "cawb00068"
			}
			if strings.Contains(cover, "ipzz00034") {
				want = ""
				wantPublishNumber = "ipzz00034"
			}
			if strings.Contains(cover, "jur00888") {
				want = "https://awsimgsrc.dmm.com/dig/mono/movie/jur888/jur888ps.jpg"
				wantPublishNumber = "jur00888"
			}
			if strings.Contains(cover, "example.com") {
				want = ""
				wantPublishNumber = ""
			}
			if meta.Poster != want || meta.Cover != cover || meta.PublishNumber != wantPublishNumber {
				t.Fatalf("unexpected URLs: %+v", meta)
			}
		})
	}
}

func TestAvbaseNumberSearchAndFields(t *testing.T) {
	paths := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		if r.URL.Path == "/works" {
			if r.URL.Query().Get("q") != "TEST-123" {
				t.Error("missing query parameter")
			}
			w.Write([]byte(`<a href="/works/example:TEST-124">wrong</a><a href="/works/example:TEST-123">match</a>`))
			return
		}
		if r.URL.Path != "/works/example:TEST-123" {
			t.Error(r.URL.Path)
		}
		w.Write([]byte(avbaseFixture))
	}))
	defer upstream.Close()
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source, err := r.Get("avbase")
	if err != nil {
		t.Fatal(err)
	}
	m, err := source.Fetch(context.Background(), Config{BaseURL: upstream.URL}, "test-123")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || !strings.Contains(paths[0], "?q=TEST-123") {
		t.Fatal(paths)
	}
	if m.Number != "TEST-123" || m.PublishNumber != "" || m.Title != "Example title" || m.OriginalTitle != m.Title || m.Outline != "Example outline text." || m.Release != "2026-11-27" || m.Runtime != "120" || m.Studio != "Example studio" || m.Publisher != "Example publisher" || m.Director != "Example director" || len(m.Actors) != 2 || len(m.Tags) != 1 || len(m.Samples) != 1 || m.Cover != "https://example.com/pl.jpg" {
		t.Fatalf("%+v", m)
	}
}
func TestAvbaseDirectLinkAndAmbiguity(t *testing.T) {
	for _, input := range []string{"example:TEST-123", "URL"} {
		t.Run(input, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/works/example:TEST-123" {
					t.Error("unexpected search", r.URL.Path)
				}
				w.Write([]byte(avbaseFixture))
			}))
			defer upstream.Close()
			r, _ := New(t.TempDir())
			source, _ := r.Get("avbase")
			query := input
			if query == "URL" {
				query = upstream.URL + "/works/example:TEST-123"
			}
			if _, err := source.Fetch(context.Background(), Config{BaseURL: upstream.URL}, query); err != nil {
				t.Fatal(err)
			}
		})
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<a href="/works/a:TEST-123">a</a><a href="/works/b:TEST-123">b</a>`))
	}))
	defer upstream.Close()
	r, _ := New(t.TempDir())
	source, _ := r.Get("avbase")
	_, err := source.Fetch(context.Background(), Config{BaseURL: upstream.URL}, "TEST-123")
	if err == nil || !strings.Contains(err.Error(), "多个") {
		t.Fatal(err)
	}
	if _, err := source.Fetch(context.Background(), Config{BaseURL: upstream.URL}, "https://another.example/works/a:TEST-123"); err == nil {
		t.Fatal("external URL accepted")
	}
}
