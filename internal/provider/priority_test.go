package provider

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestFieldPriorityFallbackAndCache(t *testing.T) {
	calls := map[string]int{}
	data := map[string]Metadata{
		"field":  {Number: "TEST-123", PublishNumber: "test00123", Title: "Preferred title", Actors: []string{""}},
		"global": {Number: "TEST-123", Title: "Global title", Actors: []string{"Actor"}, Studio: "Studio"},
	}
	p := Priorities{ByFields: map[string][]string{"publish_number": {"field"}, "title": {"field", "global"}, "actors": {"field", "global"}, "cover": {"broken", "field"}}}
	result, err := Merge(context.Background(), "test-123", p, func(_ context.Context, id string) (Metadata, error) {
		calls[id]++
		if id == "broken" {
			return Metadata{}, fmt.Errorf("offline")
		}
		return data[id], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Metadata.PublishNumber != "test00123" || result.Provenance["publish_number"] != "field" || result.Metadata.Title != "Preferred title" || result.Provenance["title"] != "field" || result.Provenance["actors"] != "global" || result.Metadata.Studio != "" || result.Provenance["studio"] != "" {
		t.Fatalf("%+v", result)
	}
	if len(result.Metadata.Actors) != 1 || result.Metadata.Actors[0] != "Actor" || result.Warnings["broken"] != "offline" {
		t.Fatal(result)
	}
	for id, n := range calls {
		if n != 1 {
			t.Fatal("duplicate fetch", id, n)
		}
	}
}
func TestFieldOnlyUsesExplicitOrder(t *testing.T) {
	// 数据源并行请求，所以只检查每个来源都被请求一次；合并结果仍按字段的显式顺序决定。
	var mu sync.Mutex
	called := map[string]int{}
	p := Priorities{ByFields: map[string][]string{"title": {"field-a", "field-b"}}}
	result, err := Merge(context.Background(), "TEST-123", p, func(_ context.Context, id string) (Metadata, error) {
		mu.Lock()
		called[id]++
		mu.Unlock()
		m := Metadata{Number: "TEST-123"}
		if id == "field-b" {
			m.Title = "Fallback title"
		}
		return m, nil
	})
	if err != nil || result.Provenance["title"] != "field-b" {
		t.Fatal(result, err)
	}
	if called["field-a"] != 1 || called["field-b"] != 1 {
		t.Fatal(called)
	}
}
func TestMergeAllSourcesFailAndMismatch(t *testing.T) {
	p := Priorities{ByFields: map[string][]string{"title": {"bad"}}}
	result, err := Merge(context.Background(), "TEST-123", p, func(context.Context, string) (Metadata, error) {
		return Metadata{Number: "OTHER-123", Title: "Wrong"}, nil
	})
	if err == nil || len(result.Provenance) != 0 || result.Warnings["bad"] == "" {
		t.Fatal(result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Merge(ctx, "TEST-123", p, func(context.Context, string) (Metadata, error) {
		t.Fatal("called canceled source")
		return Metadata{}, nil
	}); err == nil {
		t.Fatal("cancellation ignored")
	}
}
