package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"mdcng-go/internal/provider"

	_ "modernc.org/sqlite"
)

type savedScrapeJob struct {
	Job           scrapeJob                    `json:"job"`
	Settings      *scrapeSettings              `json:"settings,omitempty"`
	SourceResults map[string]provider.Metadata `json:"source_results,omitempty"`
	Reorganize    bool                         `json:"reorganize,omitempty"`
	RetryExisting bool                         `json:"retry_existing,omitempty"`
}

func (s *Server) scrapeHistoryPath() string {
	path := filepath.Join(filepath.Dir(s.statePath), "scrape-history.db")
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return path
}

// retireScrapeHistory 在第一次启动（没有 config.json）时调用：旧的刮削数据库与新配置不再对应，之后会新建空数据库。
// 旧库里有刮削记录时改名备份为 scrape-history.db.bak-时间戳；没有记录时直接删除，不留备份。
// -wal、-shm 文件随主文件一起处理。
func (s *Server) retireScrapeHistory() error {
	path := s.scrapeHistoryPath()
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	records, err := s.countScrapeHistory()
	if err != nil {
		return err
	}
	if records == 0 {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		slog.Info("[history] 第一次启动：旧的刮削数据库没有记录，已删除")
		return nil
	}
	backup := path + ".bak-" + time.Now().Format("20060102-150405")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Rename(path+suffix, backup+suffix); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	slog.Warn("[history] 第一次启动：旧的刮削数据库已备份", "records", records, "backup", backup)
	return nil
}

// countScrapeHistory 返回刮削数据库中的记录数，数据库用完即关闭。
func (s *Server) countScrapeHistory() (int, error) {
	db, err := s.openScrapeHistoryDB()
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var records int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scrape_jobs`).Scan(&records); err != nil {
		return 0, err
	}
	return records, nil
}

func (s *Server) openScrapeHistoryDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite", s.scrapeHistoryPath())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS scrape_jobs (
			id TEXT PRIMARY KEY,
			sequence INTEGER NOT NULL,
			path TEXT NOT NULL,
			output TEXT NOT NULL DEFAULT '',
			number TEXT NOT NULL DEFAULT '',
			status INTEGER NOT NULL,
			source TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL DEFAULT '',
			completed_at TEXT NOT NULL DEFAULT '',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			payload TEXT NOT NULL
		)
	`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS scrape_jobs_sequence
		ON scrape_jobs(sequence, created_at)
	`); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (s *Server) scrapeDatabaseStatus() (object, error) {
	db, err := s.openScrapeHistoryDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var records int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM scrape_jobs`).Scan(&records); err != nil {
		return nil, err
	}
	info, err := os.Stat(s.scrapeHistoryPath())
	if err != nil {
		return nil, err
	}
	return object{
		"backend": "sqlite", "status": "ready", "path": s.scrapeHistoryPath(),
		"record_count": records, "size_bytes": info.Size(), "config_storage": "json",
	}, nil
}

func (s *Server) getScrapeDatabaseStatus(w http.ResponseWriter, _ *http.Request) {
	status, err := s.scrapeDatabaseStatus()
	if err != nil {
		fail(w, 500, "database_error", "读取任务数据库状态失败："+err.Error())
		return
	}
	writeJSON(w, 200, status)
}

func (s *Server) loadScrapeHistory() error {
	db, err := s.openScrapeHistoryDB()
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT payload
		FROM scrape_jobs
		ORDER BY sequence ASC, created_at ASC, id ASC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var entry savedScrapeJob
		if err := json.Unmarshal(raw, &entry); err != nil {
			return fmt.Errorf("无效的刮削记录 %q: %w", entry.Job.ID, err)
		}
		job := entry.Job
		job.Editing = false
		job.Settings = entry.Settings
		job.SourceResults = entry.SourceResults
		job.Reorganize = entry.Reorganize
		job.RetryExisting = entry.RetryExisting
		if job.Status == 0 || job.Status == 1 {
			job.Status = -2
			job.Error = "程序重启，任务已中断"
			job.CompletedAt = time.Now().Format(time.RFC3339Nano)
		}
		settings := entry.Settings
		if settings == nil {
			current, err := s.scrapeSettings()
			if err != nil {
				return err
			}
			settings = &current
		}
		job.FileInfo = detectFilenameFlags(job.Path, *settings)
		job.HasSubtitle = job.FileInfo.HasSubtitle
		s.jobs = append(s.jobs, job)
		s.jobSequence = max(s.jobSequence, job.Sequence)
	}
	return rows.Err()
}

func (s *Server) saveScrapeHistory() {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()

	type historyRow struct {
		id, path, output, number, source, createdAt, startedAt, completedAt string
		sequence, status, durationMS                                        int64
		payload                                                             []byte
	}
	s.jobsMu.RLock()
	rows := make([]historyRow, 0, len(s.jobs))
	for _, job := range s.jobs {
		entry := savedScrapeJob{Job: job, Settings: job.Settings, SourceResults: job.SourceResults, Reorganize: job.Reorganize, RetryExisting: job.RetryExisting}
		payload, err := json.Marshal(entry)
		if err != nil {
			s.jobsMu.RUnlock()
			slog.Error("[history] 保存刮削记录失败", "error", err)
			return
		}
		rows = append(rows, historyRow{
			id: job.ID, sequence: int64(job.Sequence), path: job.Path, output: job.Output,
			number: job.Number, status: int64(job.Status), source: job.Source,
			createdAt: job.CreatedAt, startedAt: job.StartedAt, completedAt: job.CompletedAt,
			durationMS: job.DurationMS, payload: payload,
		})
	}
	s.jobsMu.RUnlock()

	db, err := s.openScrapeHistoryDB()
	if err != nil {
		slog.Error("[history] 保存刮削记录失败", "error", err)
		return
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		slog.Error("[history] 保存刮削记录失败", "error", err)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM scrape_jobs`); err != nil {
		slog.Error("[history] 保存刮削记录失败", "error", err)
		return
	}
	statement, err := tx.Prepare(`
		INSERT INTO scrape_jobs (
			id, sequence, path, output, number, status, source,
			created_at, started_at, completed_at, duration_ms, payload
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		slog.Error("[history] 保存刮削记录失败", "error", err)
		return
	}
	defer statement.Close()
	for _, row := range rows {
		if _, err := statement.Exec(
			row.id, row.sequence, row.path, row.output, row.number, row.status, row.source,
			row.createdAt, row.startedAt, row.completedAt, row.durationMS, row.payload,
		); err != nil {
			slog.Error("[history] 保存刮削记录失败", "error", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		slog.Error("[history] 保存刮削记录失败", "error", err)
	}
}
