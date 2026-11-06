package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// 推送事件：
//   - jobs-changed：任务被创建、修改、停止、删除或编辑状态变化。data 为 {"id": "..."}。
//   - job-finished：任务结束（成功、失败或终止）。用时字段与任务记录一致，页面用它校正动画终值。
//   - resync：订阅者的缓冲溢出，部分事件已丢失，页面需要整体重新拉取。
//
// 页面在 SSE 首次连接后不需要主动请求；列表和详情页的数据完全由这些事件驱动。

// jobChangedEvent 的 list 为 true 表示列表页展示的内容变了，需要刷新列表。
// 只有日志、进度等细节变化时 list 为 false，列表不会重绘，只有详情页会更新。
type jobChangedEvent struct {
	ID       string `json:"id"`
	List     bool   `json:"list"`
	Progress string `json:"progress,omitempty"` // 处理中记录的最新一条日志
	Phase    string `json:"phase,omitempty"`    // 处理中记录所在阶段，见 jobPhase
}

// jobListView 返回任务在列表接口中的内容（去掉日志、元数据，用时置零，因为页面自己计时）。
// 调用前后比较这个字符串，即可判断列表是否需要刷新。
func jobListView(job *scrapeJob) string {
	item := *job
	item.Metadata, item.Provenance, item.Logs, item.SourceResults, item.NFOTags = nil, nil, nil, nil, nil
	item.DurationMS = 0
	data, err := json.Marshal(item)
	if err != nil {
		return ""
	}
	return string(data)
}

type jobFinishedEvent struct {
	ID          string `json:"id"`
	Status      int    `json:"status"`
	DurationMS  int64  `json:"duration_ms"`
	CompletedAt string `json:"completed_at"`
}

type jobEvent struct {
	name string
	data string
}

// jobSubscriber 是一个 SSE 连接。缓冲满了就标记 lost，由推送循环发送 resync。
type jobSubscriber struct {
	ch   chan jobEvent
	lost atomic.Bool
}

// jobEventHub 向所有已连接的 SSE 客户端广播事件。零值可直接使用。
type jobEventHub struct {
	mu   sync.Mutex
	subs map[*jobSubscriber]struct{}
}

func (h *jobEventHub) subscribe() *jobSubscriber {
	sub := &jobSubscriber{ch: make(chan jobEvent, 256)}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs == nil {
		h.subs = make(map[*jobSubscriber]struct{})
	}
	h.subs[sub] = struct{}{}
	return sub
}

func (h *jobEventHub) unsubscribe(sub *jobSubscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, sub)
}

// publish 不阻塞，也不访问任务列表，因此可以在持有 jobsMu 时调用。
func (h *jobEventHub) publish(name string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		select {
		case sub.ch <- jobEvent{name: name, data: string(data)}:
		default:
			sub.lost.Store(true)
		}
	}
}

// publishJobChanged 通知页面某个任务发生了变化。调用时可以持有或不持有 jobsMu。
// list 表示列表页需要刷新。
func (s *Server) publishJobChanged(id string, list bool) {
	s.jobEvents.publish("jobs-changed", jobChangedEvent{ID: id, List: list})
}

// publishJobFinished 读取任务结束后的状态并广播。须在 updateJob 写入结束状态之后调用。
func (s *Server) publishJobFinished(id string) {
	s.jobsMu.RLock()
	var event jobFinishedEvent
	found := false
	for i := range s.jobs {
		if s.jobs[i].ID == id {
			event = jobFinishedEvent{ID: id, Status: s.jobs[i].Status, DurationMS: s.jobs[i].DurationMS, CompletedAt: s.jobs[i].CompletedAt}
			found = true
			break
		}
	}
	s.jobsMu.RUnlock()
	if found {
		s.jobEvents.publish("job-finished", event)
	}
}

// streamJobEvents 提供 GET /api/scrape/events，以 text/event-stream 推送任务事件。
func (s *Server) streamJobEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, "stream_unsupported", "当前连接不支持事件推送")
		return
	}
	// main.go 设置了 90 秒写超时，这里清除它，否则长连接会被中断。
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	header := w.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	sub := s.jobEvents.subscribe()
	defer s.jobEvents.unsubscribe(sub)
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		if sub.lost.Swap(false) {
			fmt.Fprint(w, "event: resync\ndata: {}\n\n")
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case event := <-sub.ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.name, event.data)
			flusher.Flush()
		}
	}
}
