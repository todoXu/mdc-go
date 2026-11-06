package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadTrailerValidatesVideoAndUsesSourceRequestSettings(t *testing.T) {
	mp4 := append([]byte{0, 0, 0, 24}, []byte("ftypmp42")...)
	mp4 = append(mp4, make([]byte, 12)...)
	for _, scenario := range []string{"valid", "html", "playlist", "forbidden", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Referer") == "" || r.Header.Get("Cookie") != "test=value" || r.Header.Get("Accept") != "video/mp4,video/webm,*/*;q=0.5" {
					t.Error("missing request settings")
				}
				switch scenario {
				case "html":
					w.Header().Set("Content-Type", "video/mp4")
					w.Write([]byte("<html>not a video</html>"))
				case "playlist":
					w.Write([]byte("#EXTM3U\n#EXTINF:10\npart.ts"))
				case "forbidden":
					w.WriteHeader(403)
				default:
					w.Header().Set("Content-Type", "video/mp4")
					w.Write(mp4)
				}
			}))
			defer upstream.Close()
			_, source := newSource(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			video, err := source.ReadTrailer(ctx, Config{BaseURL: upstream.URL, Cookies: "test=value"}, upstream.URL+"/trailer.mp4", upstream.URL+"/film")
			if scenario == "valid" {
				if err != nil || video.ContentType != "video/mp4" || len(video.Body) != len(mp4) {
					t.Fatal("valid preview rejected", err)
				}
			} else if err == nil {
				t.Fatal("invalid trailer was accepted")
			}
		})
	}
}
