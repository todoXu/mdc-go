package server

import (
	"encoding/json"
	"log/slog"
	"time"
)

// watchRecordStopTimeout 是删除监控目录前，等待其任务全部结束的最长时间。
const watchRecordStopTimeout = 2 * time.Minute

// removedWatchDirectories 返回新配置里已不存在的监控目录 ID。只比较 ID，忽略空 ID。
func removedWatchDirectories(oldRaw, newRaw []byte) []string {
	var before, after struct {
		Directories []struct {
			ID string `json:"id"`
		} `json:"watch_dirs"`
	}
	_ = json.Unmarshal(oldRaw, &before)
	_ = json.Unmarshal(newRaw, &after)
	kept := map[string]bool{}
	for _, dir := range after.Directories {
		kept[dir.ID] = true
	}
	var removed []string
	for _, dir := range before.Directories {
		if dir.ID != "" && !kept[dir.ID] {
			removed = append(removed, dir.ID)
		}
	}
	return removed
}

// removeWatchDirectoryRecords 按顺序执行：停止这些监控目录的任务，等待它们全部结束，再删除记录。
// 只删除记录，不删除本地文件或文件夹。
func (s *Server) removeWatchDirectoryRecords(directoryIDs []string) {
	removed := map[string]bool{}
	for _, id := range directoryIDs {
		removed[id] = true
	}
	s.stopWatchDirectoryJobs(removed)
	s.waitWatchDirectoryJobs(removed, watchRecordStopTimeout)
	s.deleteWatchDirectoryJobs(removed)
}

// stopWatchDirectoryJobs 终止这些监控目录下等待中和处理中的任务。
// 等待中的任务直接标记为已终止，处理中的任务发出取消信号，由工作协程自行结束。
func (s *Server) stopWatchDirectoryJobs(removed map[string]bool) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	for i := range s.jobs {
		job := &s.jobs[i]
		if !removed[job.DirectoryID] || (job.Status != 0 && job.Status != 1) {
			continue
		}
		job.StopRequested = true
		if job.Status == 0 {
			job.Status = -2
			job.Error = "已终止"
			job.CompletedAt = time.Now().Format(time.RFC3339Nano)
		}
		if job.Cancel != nil {
			job.Cancel()
		}
		s.publishJobChanged(job.ID, true)
	}
}

// waitWatchDirectoryJobs 等待这些监控目录下没有等待中或处理中的任务。超时后记录警告并继续。
func (s *Server) waitWatchDirectoryJobs(removed map[string]bool, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		active := 0
		s.jobsMu.RLock()
		for _, job := range s.jobs {
			if removed[job.DirectoryID] && (job.Status == 0 || job.Status == 1) {
				active++
			}
		}
		s.jobsMu.RUnlock()
		if active == 0 {
			return
		}
		if time.Now().After(deadline) {
			slog.Warn("[watch] 等待任务结束超时，仍将删除监控目录的记录", "active", active)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// deleteWatchDirectoryJobs 从内存列表中删除这些监控目录的记录，并同步到历史数据库。
func (s *Server) deleteWatchDirectoryJobs(removed map[string]bool) {
	s.jobsMu.Lock()
	kept := make([]scrapeJob, 0, len(s.jobs))
	for _, job := range s.jobs {
		if !removed[job.DirectoryID] {
			kept = append(kept, job)
			continue
		}
		s.publishJobChanged(job.ID, true)
	}
	s.jobs = kept
	// 被删除的监控目录不再有记录，它的序号计数一并清除。
	for id := range removed {
		delete(s.jobSequences, id)
	}
	s.jobsMu.Unlock()
	s.saveScrapeHistory()
}
