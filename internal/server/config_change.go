package server

import (
	"encoding/json"
	"reflect"
	"time"
)

// configChangedStopNote 是配置改变导致任务停止时，显示在任务下面的提示。
const configChangedStopNote = "配置已改变，任务已自动停止"

// jsonEqual 比较两段 JSON 的内容，不看键的顺序和空白。无法解析时视为不同。
func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// stopJobsForConfigChange 停止所有等待中和处理中的任务，并在任务下面记录提示。
// 只持有 jobsMu，不能在持有 s.mu 时同步调用，调用方应使用 go 启动。
func (s *Server) stopJobsForConfigChange() {
	s.jobsMu.Lock()
	stopped := false
	for i := range s.jobs {
		job := &s.jobs[i]
		if job.Status != 0 && job.Status != 1 {
			continue
		}
		job.StopRequested = true
		job.StopNote = configChangedStopNote
		if job.Status == 0 {
			job.Status = -2
			job.Error = "已终止"
			job.CompletedAt = time.Now().Format(time.RFC3339Nano)
		}
		if job.Cancel != nil {
			job.Cancel()
		}
		s.publishJobChanged(job.ID, true)
		stopped = true
	}
	s.jobsMu.Unlock()
	if stopped {
		s.saveScrapeHistory()
	}
}
