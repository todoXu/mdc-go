package server

// nextJobSequence 返回该监控目录的下一个记录序号。每个监控目录各自从 1 开始计数；手动任务的目录 ID 为空。
// 调用方须持有 jobsMu。
func (s *Server) nextJobSequence(directoryID string) int {
	if s.jobSequences == nil {
		s.jobSequences = map[string]int{}
	}
	s.jobSequences[directoryID]++
	return s.jobSequences[directoryID]
}

// resetJobSequenceIfEmpty 在某个监控目录的记录全部删除后，让它的序号从 1 重新开始。调用方须持有 jobsMu。
func (s *Server) resetJobSequenceIfEmpty(directoryID string) {
	for _, job := range s.jobs {
		if job.DirectoryID == directoryID {
			return
		}
	}
	delete(s.jobSequences, directoryID)
}
