package server

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"time"

	"mdcng-go/internal/provider"
)

type imageRef struct {
	Provider, URL, Referer string
	Expires                time.Time
}

// Only images returned by source queries get opaque local URLs.
func (s *Server) registerImages(metadata *provider.Metadata) {
	metadata.ImagePreviews = map[string]string{}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.imageRefs == nil {
		s.imageRefs = map[string]imageRef{}
	}
	for key, ref := range s.imageRefs {
		if time.Now().After(ref.Expires) {
			delete(s.imageRefs, key)
		}
	}
	urls := append([]string{metadata.Cover, metadata.Poster}, metadata.Samples...)
	for _, address := range urls {
		if address == "" || len(s.imageRefs) >= 4096 {
			continue
		}
		key := fmt.Sprintf("%x", sha256.Sum256([]byte(metadata.Provider+"\x00"+address)))
		s.imageRefs[key] = imageRef{metadata.Provider, address, metadata.SourceURL, time.Now().Add(2 * time.Hour)}
		metadata.ImagePreviews[address] = "/api/images/" + key
	}
}
func (s *Server) serveImage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	ref, ok := s.imageRefs[r.PathValue("key")]
	config := s.providerConfigs[ref.Provider]
	s.mu.RUnlock()
	if !ok || time.Now().After(ref.Expires) {
		fail(w, 404, "image_expired", "图片引用已失效，请重新查询元数据")
		return
	}
	if !config.Enabled {
		fail(w, 400, "provider_disabled", "数据源未启用")
		return
	}
	source, err := s.providers.Get(ref.Provider)
	if err != nil {
		providerError(w, err)
		return
	}
	if config.BaseURL == "" {
		config.BaseURL = source.Descriptor().BaseURL
	}
	image, err := source.ReadImage(r.Context(), config, ref.URL, ref.Referer)
	if err != nil {
		providerError(w, err)
		return
	}
	w.Header().Set("Content-Type", image.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=600")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Write(image.Body)
}
