package server

import (
	"encoding/json"
	"log/slog"
	"reflect"
	"time"
)

// configStopWhitelist 列出不会停止任务的配置键。只改动这些键时，即使内容有变化也不停止任务。
// 目前只有日志相关的设置：日志等级、刷新间隔、自动刷新，它们只影响日志页面，不影响刮削。
var configStopWhitelist = map[string]map[string]bool{
	"system": {"log_level": true, "log_refresh_interval": true, "log_auto_refresh": true},
}

// configChangeStopsTasks 判断这次保存是否应该停止任务：内容有变化，且修改了白名单之外的键。
func configChangeStopsTasks(section string, oldRaw, newRaw []byte) bool {
	if jsonEqual(oldRaw, newRaw) {
		return false
	}
	var before, after map[string]any
	if json.Unmarshal(oldRaw, &before) != nil || json.Unmarshal(newRaw, &after) != nil {
		return true
	}
	allowed := configStopWhitelist[section]
	keys := map[string]bool{}
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	for key := range keys {
		if allowed[key] {
			continue
		}
		if !reflect.DeepEqual(before[key], after[key]) {
			return true
		}
	}
	return false
}

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

// stopJobsForConfigChange 停止所有等待中和处理中的任务，并在任务下面记录提示。reason 说明是哪项配置触发的，只用于日志。
// 只持有 jobsMu，不能在持有 s.mu 时同步调用，调用方应使用 go 启动。
func (s *Server) stopJobsForConfigChange(reason string) {
	s.jobsMu.Lock()
	stopped := false
	count := 0
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
		count++
	}
	s.jobsMu.Unlock()
	slog.Info("[config] 配置改变，停止任务", "reason", reason, "stopped", count)
	if stopped {
		s.saveScrapeHistory()
	}
}
