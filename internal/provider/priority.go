package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

var PriorityFields = []string{"publish_number", "title", "original_title", "cover", "poster", "release", "runtime", "director", "studio", "publisher", "series", "actors", "tags", "samples", "outline", "rating", "trailer"}

type Priorities struct {
	ByFields map[string][]string `json:"by_fields"`
	// NotifyFailures 是「失败时特别提示」的数据源 ID。成功的记录中这些数据源抓取失败时，记录显示为黄色。
	NotifyFailures []string `json:"notify_failures,omitempty"`
}
type MergeResult struct {
	Metadata   Metadata          `json:"metadata"`
	Provenance map[string]string `json:"provenance"`
	Warnings   map[string]string `json:"warnings"`
}

// MergeHooks 用于记录抓取过程，任何一项都可以为 nil。
type MergeHooks struct {
	// Start 在开始并行抓取前调用，参数为需要请求的全部数据源（按优先级首次出现的顺序）。
	Start func(ids []string)
	// Collected 在全部数据源抓取完成、开始合并前调用。
	Collected func()
}

// bareNumberKey 用于比较数据源返回的番号：去掉连字符和字母前的前导数字，大小写不敏感。
// 406FTHT-146 与 FTHT-146 视为同一作品（站点常省略前导数字）。
func bareNumberKey(number string) string {
	key := strings.ToUpper(strings.ReplaceAll(number, "-", ""))
	i := 0
	for i < len(key) && key[i] >= '0' && key[i] <= '9' {
		i++
	}
	if i > 0 && i < len(key) && key[i] >= 'A' && key[i] <= 'Z' {
		key = key[i:]
	}
	return key
}

// Merge only uses each field's explicit order. No implicit fallback is used.
func Merge(ctx context.Context, number string, priorities Priorities, fetch func(context.Context, string) (Metadata, error)) (MergeResult, error) {
	return MergeWithHooks(ctx, number, priorities, fetch, MergeHooks{})
}

// MergeWithHooks 并行请求所有配置了字段优先级的数据源，全部完成后再按字段优先级合并。
// 合并结果只取决于优先级顺序，与各数据源返回的先后无关。
func MergeWithHooks(ctx context.Context, number string, priorities Priorities, fetch func(context.Context, string) (Metadata, error), hooks MergeHooks) (MergeResult, error) {
	number, err := NormalizeNumber(number)
	if err != nil {
		return MergeResult{}, err
	}
	result := MergeResult{Provenance: map[string]string{}, Warnings: map[string]string{}}

	var ids []string
	seenID := map[string]bool{}
	for _, field := range PriorityFields {
		for _, id := range priorities.ByFields[field] {
			if !seenID[id] {
				seenID[id] = true
				ids = append(ids, id)
			}
		}
	}
	if ctx.Err() != nil {
		return result, issue("timeout", "聚合查询已取消或超时")
	}
	if hooks.Start != nil && len(ids) > 0 {
		hooks.Start(ids)
	}

	type fetchOutcome struct {
		data    map[string]json.RawMessage
		warning string
	}
	outcomes := make(map[string]fetchOutcome, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			var outcome fetchOutcome
			metadata, err := fetch(ctx, id)
			switch {
			case err != nil:
				outcome.warning = err.Error()
			case bareNumberKey(metadata.Number) != bareNumberKey(number):
				outcome.warning = "返回番号不一致"
			default:
				b, _ := json.Marshal(metadata)
				_ = json.Unmarshal(b, &outcome.data)
			}
			mu.Lock()
			outcomes[id] = outcome
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	if hooks.Collected != nil {
		hooks.Collected()
	}
	if ctx.Err() != nil {
		return result, issue("timeout", "聚合查询已取消或超时")
	}
	cache := map[string]map[string]json.RawMessage{}
	for id, outcome := range outcomes {
		if outcome.warning != "" {
			result.Warnings[id] = outcome.warning
			cache[id] = nil
			continue
		}
		cache[id] = outcome.data
	}

	values := map[string]json.RawMessage{}
	for _, field := range PriorityFields {
		seen := map[string]bool{}
		for _, id := range priorities.ByFields[field] {
			if seen[id] {
				continue
			}
			seen[id] = true
			data := cache[id]
			value, ok := data[field]
			if !ok || !hasValue(value) {
				continue
			}
			values[field] = value
			result.Provenance[field] = id
			if result.Metadata.SourceURL == "" {
				var source string
				_ = json.Unmarshal(data["source_url"], &source)
				result.Metadata.SourceURL = source
			}
			break
		}
	}
	if len(result.Provenance) == 0 {
		return result, issue("no_metadata", "没有获取到元数据，请检查优先级、插件启用状态及各数据源连接")
	}
	sourceURL := result.Metadata.SourceURL
	b, _ := json.Marshal(values)
	if err := json.Unmarshal(b, &result.Metadata); err != nil {
		return result, fmt.Errorf("合并字段失败: %w", err)
	}
	result.Metadata.Number = number
	result.Metadata.Provider = "merged"
	result.Metadata.SourceURL = sourceURL
	return result, nil
}
func hasValue(value json.RawMessage) bool {
	var v any
	if json.Unmarshal(value, &v) != nil {
		return false
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) != ""
	case []any:
		for _, item := range x {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				return true
			}
		}
		return false
	case float64:
		return x != 0
	default:
		return false
	}
}
