package server

import (
	"encoding/json"
	"testing"
	"time"
)

// waitBulkDone 等待后台批量任务结束，返回最终进度。
func waitBulkDone(t *testing.T, s *Server) bulkSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var state bulkSnapshot
		response := call(s, "GET", "/api/scrape/batch", "")
		if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		if !state.Running {
			return state
		}
		if time.Now().After(deadline) {
			t.Fatal("bulk operation did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBulkDeleteRunsInBackgroundAndHoldsScans(t *testing.T) {
	s := configureScrapeServer(t)
	s.jobs = []scrapeJob{
		{ID: "a", Sequence: 1, Status: 2, DirectoryID: "library"},
		{ID: "b", Sequence: 2, Status: -1, DirectoryID: "library"},
		{ID: "active", Sequence: 3, Status: 1, DirectoryID: "library"},
		{ID: "other", Sequence: 1, Status: 2, DirectoryID: "second"},
	}
	s.jobSequences = map[string]int{"library": 3, "second": 1}
	// 模拟一次正在进行的扫描：删除要等它结束才开始。
	s.watchState.Running = true
	response := call(s, "POST", "/api/scrape/batch/delete", `{"ids":["a","b","active","missing","a"]}`)
	if response.Code != 202 {
		t.Fatal(response.Body.String())
	}
	if !s.bulk.scansHeld() {
		t.Fatal("scans were not held during bulk delete")
	}
	if again := call(s, "POST", "/api/scrape/batch/delete", `{"ids":["other"]}`); again.Code != 409 {
		t.Fatal("second bulk operation accepted:", again.Body.String())
	}
	time.Sleep(50 * time.Millisecond)
	s.jobsMu.RLock()
	before := len(s.jobs)
	s.jobsMu.RUnlock()
	if before != 4 || s.bulk.snapshot().Phase != "waiting_scan" {
		t.Fatalf("deleted while a scan was running: %d %+v", before, s.bulk.snapshot())
	}
	s.watchMu.Lock()
	s.watchState.Running = false
	s.watchMu.Unlock()
	result := waitBulkDone(t, s)
	if result.Total != 4 || result.Success != 2 || len(result.Failed) != 2 || result.Phase != "done" {
		t.Fatalf("result: %+v", result)
	}
	s.jobsMu.RLock()
	defer s.jobsMu.RUnlock()
	if len(s.jobs) != 2 || s.jobs[0].ID != "active" || s.jobs[1].ID != "other" {
		t.Fatalf("remaining: %+v", s.jobs)
	}
	// 删除结束后，扫描仍推迟一个检查周期。
	if !s.bulk.scansHeld() {
		t.Fatal("scan was not postponed after bulk delete")
	}
}
