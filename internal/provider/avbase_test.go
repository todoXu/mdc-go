package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const avbaseFixture = `<html><head><link rel="canonical" href="https://www.avbase.net/works/example:TEST-123"></head><body><div role="tabpanel"><h1>Example title</h1><img src="https://example.com/pl.jpg"><dl><div><dt>発売日</dt><dd>2026/11/27</dd></div><div><dt>収録分数</dt><dd>120</dd></div><div><dt>メーカー</dt><dd>Example studio</dd></div><div><dt>レーベル</dt><dd>Example publisher</dd></div><div><dt>シリーズ</dt><dd>Example series</dd></div><div><dt>監督</dt><dd>Example director</dd></div></dl><a href="https://pics.dmm.co.jp/example-1.jpg">sample</a></div><section><h2>紹介文</h2><p>Example outline text.</p></section><section><div><h2>出演者・メモ</h2></div><a href="/talents/one">Actor One</a><a href="/talents/two">Actor Two</a></section><section><div><h2>タグ・説明文</h2></div><a href="/tags/a">Tag A</a></section><section><h2>関連作品</h2><a href="/talents/other">Unrelated actor</a></section></body></html>`

func TestAvbaseDMMPosterUsesPicsDigPath(t *testing.T) {
	for _, scenario := range []struct {
		cover, poster, publishNumber string
	}{
		// 竖版海报在 pics_dig 下；mono/movie/adult 去掉 adult 段。
		{"https://pics.dmm.co.jp/mono/movie/adult/h_1712kbr033/h_1712kbr033pl.jpg", "https://awsimgsrc.dmm.co.jp/pics_dig/mono/movie/h_1712kbr033/h_1712kbr033ps.jpg", "h_1712kbr033"},
		// 已经在 pics_dig 上的封面直接换 ps，不重复加前缀。
		{"https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/ipzz00034/ipzz00034pl.jpg", "https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/ipzz00034/ipzz00034ps.jpg", "ipzz00034"},
		// digital/video 保留原路径（零填充番号也一样）。
		{"https://pics.dmm.co.jp/digital/video/jur00888/jur00888pl.jpg", "https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/jur00888/jur00888ps.jpg", "jur00888"},
		{"https://awsimgsrc.dmm.com/digital/video/cawb00068/cawb00068pl.jpg", "https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/cawb00068/cawb00068ps.jpg", "cawb00068"},
		// dig/mono/movie 是另一种路径，保留 dig 前缀，不套 pics_dig。
		{"https://awsimgsrc.dmm.com/dig/mono/movie/cawb068/cawb068pl.jpg", "https://awsimgsrc.dmm.co.jp/dig/mono/movie/cawb068/cawb068ps.jpg", "cawb068"},
		{"https://pics.dmm.co.jp/dig/mono/movie/cawb068/cawb068pl.jpg?cache=1", "https://awsimgsrc.dmm.co.jp/dig/mono/movie/cawb068/cawb068ps.jpg", "cawb068"},
		// 非 DMM 的封面没有对应的竖版海报。
		{"https://example.com/pl.jpg", "", ""},
	} {
		t.Run(scenario.cover, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(strings.ReplaceAll(avbaseFixture, "https://example.com/pl.jpg", scenario.cover)))
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
			if meta.Poster != scenario.poster || meta.Cover != scenario.cover || meta.PublishNumber != scenario.publishNumber {
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
