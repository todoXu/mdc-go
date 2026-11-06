package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mdcng-go/internal/provider"
)

func (s *Server) providerRoutes() {
	s.mux.HandleFunc("GET /api/images/{key}", s.serveImage)
	s.mux.HandleFunc("GET /api/providers/priorities", s.getPriorities)
	s.mux.HandleFunc("PUT /api/providers/priorities", s.putPriorities)
	s.mux.HandleFunc("POST /api/providers/fetch", s.fetchByPriority)
	s.mux.HandleFunc("GET /api/providers", s.listProviders)
	s.mux.HandleFunc("POST /api/providers/install", s.installProvider)
	s.mux.HandleFunc("GET /api/providers/{id}/package", s.downloadProvider)
	s.mux.HandleFunc("DELETE /api/providers/{id}", s.deleteProvider)
	s.mux.HandleFunc("GET /api/providers/{id}/config", s.readProviderConfig)
	s.mux.HandleFunc("PUT /api/providers/{id}/config", s.saveProviderConfig)
	s.mux.HandleFunc("POST /api/providers/{id}/test", s.testProvider)
	s.mux.HandleFunc("POST /api/providers/{id}/fetch", s.fetchProvider)
}
func (s *Server) installProvider(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		fail(w, 400, "invalid_plugin", "插件包超过 2 MB 或读取失败")
		return
	}
	manifest, err := s.providers.InstallZIP(data)
	if err != nil {
		providerError(w, err)
		return
	}
	writeJSON(w, 201, object{"success": true, "id": manifest.ID, "name": manifest.Name})
}
func (s *Server) downloadProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	data, err := s.providers.ExportZIP(id)
	if err != nil {
		providerError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+id+".zip\"")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(data)
}
func (s *Server) deleteProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.providers.Remove(id); err != nil {
		providerError(w, err)
		return
	}
	writeJSON(w, 200, object{"success": true})
}
func (s *Server) plugin(w http.ResponseWriter, r *http.Request) (provider.Source, bool) {
	p, err := s.providers.Get(r.PathValue("id"))
	if err != nil {
		providerError(w, err)
		return nil, false
	}
	return p, true
}
func (s *Server) providerConfig(id string) provider.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if config, ok := s.providerConfigs[id]; ok {
		return config
	}
	return provider.Config{CooldownSeconds: 0}
}
func (s *Server) listProviders(w http.ResponseWriter, r *http.Request) {
	items, err := s.providers.List()
	if err != nil {
		fail(w, 400, "invalid_plugin", err.Error())
		return
	}
	result := []any{}
	for _, m := range items {
		c := s.providerConfig(m.ID)
		base := c.BaseURL
		if base == "" {
			base = m.BaseURL
		}
		result = append(result, object{"id": m.ID, "name": m.Name, "version": m.Version, "engine": m.Engine, "base_url": base, "enabled": c.Enabled, "input_hint": m.InputHint, "builtin": m.Builtin})
	}
	writeJSON(w, 200, object{"data": result, "api_version": 1})
}
func (s *Server) readProviderConfig(w http.ResponseWriter, r *http.Request) {
	p, ok := s.plugin(w, r)
	if !ok {
		return
	}
	c := s.providerConfig(p.Descriptor().ID)
	if c.BaseURL == "" {
		c.BaseURL = p.Descriptor().BaseURL
	}
	if c.Options == nil {
		c.Options = map[string]any{}
	}
	writeJSON(w, 200, c)
}
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		fail(w, 400, "invalid_body", "请求格式错误或包含未知字段")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		fail(w, 400, "invalid_body", "请求必须是一个 JSON 对象")
		return false
	}
	return true
}
func (s *Server) saveProviderConfig(w http.ResponseWriter, r *http.Request) {
	p, ok := s.plugin(w, r)
	if !ok {
		return
	}
	var c *provider.Config
	if !decode(w, r, &c) {
		return
	}
	if c == nil {
		fail(w, 400, "invalid_config", "配置必须是对象")
		return
	}
	if c.Options == nil {
		c.Options = map[string]any{}
	}
	if c.CooldownSeconds < 0 || c.CooldownSeconds > 60 || strings.ContainsAny(c.Cookies+c.UserAgent, "\r\n") {
		fail(w, 400, "invalid_config", "请求间隔需为 0–60 秒，Cookies 和 User-Agent 不能包含换行")
		return
	}
	if c.RequestTimeout < 0 || c.RequestTimeout > 300 || c.TrailerTimeout < 0 || c.TrailerTimeout > 600 || c.RetryTimes < 0 || c.RetryTimes > 5 {
		fail(w, 400, "invalid_config", "请求超时需为 0–300 秒，预告片超时为 0–600 秒，失败重复次数为 0–5 次；0 表示使用默认值")
		return
	}
	if c.BaseURL != "" {
		u, err := url.Parse(c.BaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			fail(w, 400, "invalid_config", "站点地址无效")
			return
		}
	}
	if c.Proxy != "" {
		u, err := url.Parse(c.Proxy)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h") {
			fail(w, 400, "invalid_config", "代理地址无效")
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]provider.Config, len(s.providerConfigs)+1)
	for k, v := range s.providerConfigs {
		next[k] = v
	}
	next[p.Descriptor().ID] = *c
	if err := save(s.providerConfigPath, next); err != nil {
		fail(w, 500, "save_failed", "无法保存数据源配置")
		return
	}
	s.providerConfigs = next
	writeJSON(w, 200, object{"success": true})
}
func providerError(w http.ResponseWriter, err error) {
	var source *provider.SourceError
	if errors.As(err, &source) {
		status := 502
		switch source.Code {
		case "invalid_number", "invalid_config", "invalid_plugin":
			status = 400
		case "plugin_exists":
			status = 409
		case "builtin_plugin":
			status = 403
		case "not_found", "provider_not_found":
			status = 404
		case "verification_required":
			status = 428
		case "timeout":
			status = 504
		}
		fail(w, status, source.Code, source.Message)
		return
	}
	fail(w, 400, "invalid_plugin", err.Error())
}
func (s *Server) testProvider(w http.ResponseWriter, r *http.Request) {
	p, ok := s.plugin(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	start := time.Now()
	config := s.providerConfig(p.Descriptor().ID)
	if !config.Enabled {
		fail(w, 400, "provider_disabled", "请先启用该数据源，再测试连接")
		return
	}
	if err := p.Probe(ctx, config); err != nil {
		providerError(w, err)
		return
	}
	writeJSON(w, 200, object{"success": true, "message": "已连接数据源并识别页面内容", "elapsed_ms": time.Since(start).Milliseconds()})
}
func (s *Server) fetchProvider(w http.ResponseWriter, r *http.Request) {
	p, ok := s.plugin(w, r)
	if !ok {
		return
	}
	var request struct {
		Number string `json:"number"`
	}
	if !decode(w, r, &request) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	config := s.providerConfig(p.Descriptor().ID)
	if !config.Enabled {
		fail(w, 400, "provider_disabled", "请先启用该数据源，再查询元数据")
		return
	}
	metadata, err := p.Fetch(ctx, config, request.Number)
	if err != nil {
		providerError(w, err)
		return
	}
	s.registerImages(&metadata)
	writeJSON(w, 200, metadata)
}
