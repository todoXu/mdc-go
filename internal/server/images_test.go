package server

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"mdcng-go/internal/provider"
)

func TestImageRefererRelay(t *testing.T) {
	var pngBody bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	png.Encode(&pngBody, img)
	var host string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/not-image" {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("verification required"))
			return
		}
		if r.Header.Get("Referer") != host+"/TEST-123" {
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBody.Bytes())
	}))
	defer upstream.Close()
	host = upstream.URL
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.providerConfigs["Javbus"] = provider.Config{Enabled: true, BaseURL: host}
	m := provider.Metadata{Provider: "Javbus", SourceURL: host + "/TEST-123", Cover: host + "/cover.png"}
	s.registerImages(&m)
	response := call(s, "GET", m.ImagePreviews[m.Cover], "")
	if response.Code != 200 || response.Header().Get("Content-Type") != "image/png" || !bytes.Equal(response.Body.Bytes(), pngBody.Bytes()) {
		t.Fatal(response.Code, response.Body.String())
	}
	if response := call(s, "GET", "/api/images/unregistered", ""); response.Code != 404 {
		t.Fatal(response.Code)
	}
	m.Cover = host + "/not-image"
	s.registerImages(&m)
	if response := call(s, "GET", m.ImagePreviews[m.Cover], ""); response.Code == 200 {
		t.Fatal("HTML treated as image")
	}
	s.providerConfigs["Javbus"] = provider.Config{}
	if response := call(s, "GET", m.ImagePreviews[m.Cover], ""); response.Code != 400 {
		t.Fatal("disabled source accepted")
	}
}
func TestImageCookiesNotSentToOtherHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("cookies leaked to image host")
		}
		w.WriteHeader(404)
	}))
	defer upstream.Close()
	registry, err := provider.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source, err := registry.Get("Javbus")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = source.ReadImage(t.Context(), provider.Config{BaseURL: "https://example.com", Cookies: "private=value"}, upstream.URL, "https://example.com/TEST-123")
}
