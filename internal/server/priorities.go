package server

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"time"

	"mdcng-go/internal/provider"
)

func (s *Server) currentPriorities() (provider.Priorities, error) {
	items, err := s.providers.List()
	if err != nil {
		return provider.Priorities{}, err
	}
	known := map[string]bool{}
	for _, item := range items {
		known[item.ID] = true
	}
	p := provider.Priorities{ByFields: map[string][]string{}}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.priorities != nil {
		for field, order := range s.priorities.ByFields {
			p.ByFields[field] = []string{}
			for _, id := range order {
				if known[id] && s.providerConfigs[id].Enabled {
					p.ByFields[field] = append(p.ByFields[field], id)
				}
			}
		}
	}
	if s.priorities != nil {
		seenNotify := map[string]bool{}
		for _, id := range s.priorities.NotifyFailures {
			if known[id] && !seenNotify[id] {
				seenNotify[id] = true
				p.NotifyFailures = append(p.NotifyFailures, id)
			}
		}
	}
	// Existing configurations predate the trailer field. Preserve their cover
	// source order until the user explicitly chooses a separate trailer order.
	if s.priorities == nil {
		return p, nil
	}
	if _, exists := s.priorities.ByFields["trailer"]; !exists {
		p.ByFields["trailer"] = append([]string{}, p.ByFields["cover"]...)
	}
	if _, exists := s.priorities.ByFields["publish_number"]; !exists {
		p.ByFields["publish_number"] = append([]string{}, p.ByFields["cover"]...)
	}
	return p, nil
}
func (s *Server) getPriorities(w http.ResponseWriter, r *http.Request) {
	p, err := s.currentPriorities()
	if err != nil {
		providerError(w, err)
		return
	}
	writeJSON(w, 200, object{"config": p, "fields": provider.PriorityFields})
}
func (s *Server) putPriorities(w http.ResponseWriter, r *http.Request) {
	// 只更新请求里出现的部分：字段优先级和「失败时特别提示」分别保存，互不覆盖。
	var raw map[string]json.RawMessage
	if !decode(w, r, &raw) {
		return
	}
	if raw == nil {
		fail(w, 400, "invalid_priorities", "优先级必须是对象")
		return
	}
	for key := range raw {
		if key != "by_fields" && key != "notify_failures" {
			fail(w, 400, "invalid_priorities", "不支持的优先级设置："+key)
			return
		}
	}
	p := &provider.Priorities{}
	s.mu.RLock()
	if s.priorities != nil {
		p.ByFields = maps.Clone(s.priorities.ByFields)
		p.NotifyFailures = slices.Clone(s.priorities.NotifyFailures)
	}
	s.mu.RUnlock()
	if value, ok := raw["by_fields"]; ok {
		p.ByFields = nil
		if json.Unmarshal(value, &p.ByFields) != nil {
			fail(w, 400, "invalid_priorities", "字段优先级格式错误")
			return
		}
	}
	if value, ok := raw["notify_failures"]; ok {
		p.NotifyFailures = nil
		if json.Unmarshal(value, &p.NotifyFailures) != nil {
			fail(w, 400, "invalid_priorities", "失败时特别提示格式错误")
			return
		}
	}
	items, err := s.providers.List()
	if err != nil {
		providerError(w, err)
		return
	}
	known := map[string]bool{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range items {
		known[item.ID] = s.providerConfigs[item.ID].Enabled
	}
	validFields := map[string]bool{}
	for _, field := range provider.PriorityFields {
		validFields[field] = true
	}
	validate := func(order []string) bool {
		seen := map[string]bool{}
		for _, id := range order {
			if !known[id] || seen[id] {
				return false
			}
			seen[id] = true
		}
		return true
	}
	_, updateFields := raw["by_fields"]
	for field, order := range p.ByFields {
		if !updateFields {
			break
		}
		if !validFields[field] || !validate(order) {
			fail(w, 400, "invalid_priorities", "字段优先级只能包含已启用且已安装的数据源，不能重复")
			return
		}
	}
	seenNotify := map[string]bool{}
	for _, id := range p.NotifyFailures {
		if _, installed := known[id]; !installed || seenNotify[id] {
			fail(w, 400, "invalid_priorities", "失败时特别提示只能选择已安装且不重复的数据源")
			return
		}
		seenNotify[id] = true
	}
	if p.ByFields == nil {
		p.ByFields = map[string][]string{}
	}
	before, _ := json.Marshal(s.priorities)
	if err := save(s.priorityPath, p); err != nil {
		fail(w, 500, "save_failed", "无法保存优先级")
		return
	}
	s.priorities = p
	if after, _ := json.Marshal(p); !jsonEqual(before, after) {
		// 持有 s.mu，停止任务需放到后台执行。
		go s.stopJobsForConfigChange()
	}
	writeJSON(w, 200, object{"success": true})
}
func (s *Server) fetchByPriority(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Number string `json:"number"`
	}
	if !decode(w, r, &request) {
		return
	}
	number, err := provider.NormalizeNumber(request.Number)
	if err != nil {
		providerError(w, err)
		return
	}
	p, err := s.currentPriorities()
	if err != nil {
		providerError(w, err)
		return
	}
	// Take one consistent settings snapshot for the entire aggregation operation.
	s.mu.RLock()
	configs := make(map[string]provider.Config, len(s.providerConfigs))
	for id, c := range s.providerConfigs {
		configs[id] = c
	}
	s.mu.RUnlock()
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	previews := map[string]string{}
	result, err := provider.Merge(ctx, number, p, func(ctx context.Context, id string) (provider.Metadata, error) {
		config := configs[id]
		if !config.Enabled {
			return provider.Metadata{}, &provider.SourceError{Code: "disabled", Message: "数据源未启用"}
		}
		source, err := s.providers.Get(id)
		if err != nil {
			return provider.Metadata{}, err
		}
		metadata, err := source.Fetch(ctx, config, number)
		if err == nil {
			s.registerImages(&metadata)
			for address, local := range metadata.ImagePreviews {
				previews[address] = local
			}
		}
		return metadata, err
	})
	if err != nil {
		var source *provider.SourceError
		code := "merge_failed"
		status := 502
		if errors.As(err, &source) {
			code = source.Code
			if code == "timeout" {
				status = 504
			}
		}
		writeJSON(w, status, object{"success": false, "error": code, "message": err.Error(), "warnings": result.Warnings})
		return
	}
	result.Metadata.ImagePreviews = previews
	writeJSON(w, 200, result)
}
