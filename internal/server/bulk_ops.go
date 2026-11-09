package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// 批量删除和批量重新刮削在服务端后台执行：请求只登记任务就返回，页面刷新或关闭都不影响执行。
// 同一时间只运行一个批量任务。页面通过 GET /api/scrape/batch 读取进度。
//
// 批量删除期间暂停目录监控扫描：删除记录（不删文件）后，扫描会把这些文件当成新文件重新入队。
// 删除开始前先等正在进行的扫描结束；删除结束后，下一次扫描推迟一个检查周期。

// bulkLimit 是一次批量任务最多接受的记录数。
const bulkLimit = 20000

// bulkSaveEvery 是批量任务每处理多少条保存一次历史，服务中途退出时已处理的部分不会丢失。
const bulkSaveEvery = 50

type bulkFailure struct {
	ID      string `json:"id"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// bulkSnapshot 是返回给页面的批量任务进度。
type bulkSnapshot struct {
	Running  bool          `json:"running"`
	Action   string        `json:"action,omitempty"` // delete 或 retry
	Phase    string        `json:"phase,omitempty"`  // waiting_scan 等待当前监控扫描结束；running 执行中；done 已结束
	Total    int           `json:"total"`
	Done     int           `json:"done"`
	Success  int           `json:"success"`
	Failed   []bulkFailure `json:"failed"`
	Started  string        `json:"started,omitempty"`
	Finished string        `json:"finished,omitempty"`
}

type bulkOperation struct {
	mu    sync.Mutex
	state bulkSnapshot
	// scanHoldUntil 之前不启动监控扫描。批量删除开始时设为很远的将来，结束时设为结束时间加一个检查周期。
	scanHoldUntil time.Time
}

func (b *bulkOperation) snapshot() bulkSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	copy := b.state
	copy.Failed = append([]bulkFailure{}, b.state.Failed...)
	return copy
}

// scansHeld 判断现在是否暂停监控扫描。
func (b *bulkOperation) scansHeld() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return time.Now().Before(b.scanHoldUntil)
}

func (b *bulkOperation) update(change func(*bulkSnapshot)) {
	b.mu.Lock()
	change(&b.state)
	b.mu.Unlock()
}

func (s *Server) getBulkStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, s.bulk.snapshot())
}

// startBulk 登记一个批量任务。已有批量任务在运行时返回 false。
func (s *Server) startBulk(action string, ids []string) bool {
	s.bulk.mu.Lock()
	defer s.bulk.mu.Unlock()
	if s.bulk.state.Running {
		return false
	}
	s.bulk.state = bulkSnapshot{Running: true, Action: action, Phase: "running", Total: len(ids), Failed: []bulkFailure{}, Started: time.Now().Format(time.RFC3339Nano)}
	if action == "delete" {
		s.bulk.state.Phase = "waiting_scan"
		s.bulk.scanHoldUntil = time.Now().Add(100 * 365 * 24 * time.Hour)
	}
	return true
}

// finishBulk 标记批量任务结束。批量删除结束后，下一次监控扫描推迟一个检查周期。
func (s *Server) finishBulk() {
	interval := time.Duration(s.currentWatchConfig().IntervalSeconds) * time.Second
	now := time.Now()
	s.bulk.mu.Lock()
	wasDelete := s.bulk.state.Action == "delete"
	s.bulk.state.Running = false
	s.bulk.state.Phase = "done"
	s.bulk.state.Finished = now.Format(time.RFC3339Nano)
	if wasDelete {
		s.bulk.scanHoldUntil = now.Add(interval)
	}
	snapshot := s.bulk.state
	s.bulk.mu.Unlock()
	if wasDelete {
		s.watchMu.Lock()
		s.watchState.NextScan = now.Add(interval).Format(time.RFC3339)
		s.jobEvents.publish("watch-changed", struct{}{})
		s.watchMu.Unlock()
	}
	slog.Info("[bulk] 批量任务结束", "action", snapshot.Action, "total", snapshot.Total, "success", snapshot.Success, "failed", len(snapshot.Failed))
}

// bulkIDs 读取请求中的记录 ID，去掉重复，并检查数量。
func bulkIDs(w http.ResponseWriter, ids []string, verb string) ([]string, bool) {
	unique := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	if len(unique) == 0 {
		fail(w, 400, "invalid_ids", "请选择要"+verb+"的记录")
		return nil, false
	}
	if len(unique) > bulkLimit {
		fail(w, 400, "too_many_ids", fmt.Sprintf("一次最多%s %d 条记录", verb, bulkLimit))
		return nil, false
	}
	return unique, true
}

// batchDeleteScrapeJobs 提供 POST /api/scrape/batch/delete，在后台删除多条记录。
func (s *Server) batchDeleteScrapeJobs(w http.ResponseWriter, r *http.Request) {
	var request struct {
		IDs         []string `json:"ids"`
		DeleteMedia bool     `json:"delete_media"`
	}
	if !decode(w, r, &request) {
		return
	}
	ids, ok := bulkIDs(w, request.IDs, "删除")
	if !ok {
		return
	}
	if !s.startBulk("delete", ids) {
		fail(w, 409, "bulk_running", "已有批量任务正在执行，请等它结束")
		return
	}
	slog.Info("[bulk] 开始批量删除", "total", len(ids), "delete_media", request.DeleteMedia)
	go s.runBulkDelete(ids, request.DeleteMedia)
	writeJSON(w, 202, s.bulk.snapshot())
}

func (s *Server) runBulkDelete(ids []string, deleteMedia bool) {
	defer s.finishBulk()
	// 等正在进行的监控扫描结束，避免扫描在删除过程中把文件重新入队。新的扫描已经暂停。
	for {
		s.watchMu.RLock()
		running := s.watchState.Running
		s.watchMu.RUnlock()
		if !running {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	s.bulk.update(func(b *bulkSnapshot) { b.Phase = "running" })
	for i, id := range ids {
		_, failure := s.deleteJob(id, deleteMedia)
		s.bulk.update(func(b *bulkSnapshot) {
			b.Done++
			if failure != nil {
				b.Failed = append(b.Failed, bulkFailure{ID: id, Code: failure.code, Message: failure.message})
			} else {
				b.Success++
			}
		})
		if (i+1)%bulkSaveEvery == 0 {
			s.saveScrapeHistory()
		}
	}
	s.saveScrapeHistory()
}

// batchRetryScrapeJobs 提供 POST /api/scrape/batch/retry，在后台重新刮削多条记录。
// 每 20 条保存一次历史并启动这些任务，前面的记录不用等全部提交完就开始刮削；任务按提交顺序开始。
func (s *Server) batchRetryScrapeJobs(w http.ResponseWriter, r *http.Request) {
	var request struct {
		IDs   []string   `json:"ids"`
		Flags *taskFlags `json:"flags,omitempty"`
	}
	if !decode(w, r, &request) {
		return
	}
	ids, ok := bulkIDs(w, request.IDs, "重新刮削")
	if !ok {
		return
	}
	if !s.startBulk("retry", ids) {
		fail(w, 409, "bulk_running", "已有批量任务正在执行，请等它结束")
		return
	}
	slog.Info("[bulk] 开始批量重新刮削", "total", len(ids))
	go s.runBulkRetry(ids, request.Flags)
	writeJSON(w, 202, s.bulk.snapshot())
}

// runBulkRetry 分两步：先把记录逐条放回队列（校验失败的立即计入结果），然后等这些记录的刮削真正结束，
// 再按每条记录的最终状态更新进度。进度因此反映刮削完成了多少，而不是只到入队为止。
func (s *Server) runBulkRetry(ids []string, flags *taskFlags) {
	defer s.finishBulk()
	const chunk = 20
	queued := []string{}
	tracked := []string{}
	flush := func() {
		if len(queued) > 0 {
			s.saveScrapeHistory()
			s.startScrapeJobs(queued)
			tracked = append(tracked, queued...)
			queued = []string{}
		}
	}
	for _, id := range ids {
		failure := s.retryJobForBatch(id, flags)
		if failure == nil {
			queued = append(queued, id)
		} else {
			// 校验失败的记录不会开始刮削，直接计入结果。
			s.bulk.update(func(b *bulkSnapshot) {
				b.Done++
				b.Failed = append(b.Failed, bulkFailure{ID: id, Code: failure.code, Message: failure.message})
			})
		}
		if len(queued) >= chunk {
			flush()
		}
	}
	flush()
	s.bulk.update(func(b *bulkSnapshot) { b.Phase = "running" })
	s.trackRetryJobs(tracked)
}

// trackRetryJobs 轮询这些记录，直到每一条都结束（成功、失败或已终止）或已被删除，并把结果计入批量进度。
func (s *Server) trackRetryJobs(ids []string) {
	pending := map[string]bool{}
	for _, id := range ids {
		pending[id] = true
	}
	for len(pending) > 0 {
		var finished []bulkFailure
		var succeeded int
		found := map[string]bool{}
		s.jobsMu.RLock()
		for _, job := range s.jobs {
			if !pending[job.ID] {
				continue
			}
			found[job.ID] = true
			if job.Status == 0 || job.Status == 1 {
				continue
			}
			if job.Status == 2 {
				succeeded++
			} else {
				message := job.Error
				if message == "" {
					message = "刮削失败"
				}
				finished = append(finished, bulkFailure{ID: job.ID, Code: "scrape_failed", Message: message})
			}
			delete(pending, job.ID)
		}
		s.jobsMu.RUnlock()
		// 批量任务进行中被删除的记录，视为无法完成。
		for id := range pending {
			if !found[id] {
				finished = append(finished, bulkFailure{ID: id, Code: "not_found", Message: "记录已被删除"})
				delete(pending, id)
			}
		}
		if len(finished) > 0 || succeeded > 0 {
			s.bulk.update(func(b *bulkSnapshot) {
				b.Done += len(finished) + succeeded
				b.Success += succeeded
				b.Failed = append(b.Failed, finished...)
			})
		}
		if len(pending) > 0 {
			time.Sleep(300 * time.Millisecond)
		}
	}
}
