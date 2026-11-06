package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// 刮削完成后自动扫描 Emby 媒体库（「设置 → 头像 → 刮削完成后自动扫描媒体库」）。
// 批量刮削时每条任务完成都会请求扫描，这里合并：最后一条完成后等 embyRefreshDelay 仍没有新完成的任务，
// 并且没有正在等待或处理的任务，才扫描一次。

const embyRefreshDelay = 15 * time.Second

type embyRefresher struct {
	mu    sync.Mutex
	timer *time.Timer
}

func (s *Server) embyRefreshEnabled() bool {
	s.mu.RLock()
	raw := append([]byte(nil), s.configs["actress"]...)
	s.mu.RUnlock()
	var actress struct {
		Emby struct {
			Refresh bool `json:"refresh_after_scrape"`
		} `json:"emby_config"`
	}
	return json.Unmarshal(raw, &actress) == nil && actress.Emby.Refresh
}

// requestEmbyRefresh 在任务整理成功后调用：没有开启时什么也不做。
func (s *Server) requestEmbyRefresh() {
	if !s.embyRefreshEnabled() {
		return
	}
	s.embyRefresh.mu.Lock()
	defer s.embyRefresh.mu.Unlock()
	if s.embyRefresh.timer != nil {
		s.embyRefresh.timer.Stop()
	}
	s.embyRefresh.timer = time.AfterFunc(embyRefreshDelay, s.runEmbyRefresh)
}

func (s *Server) scrapeBusy() bool {
	s.jobsMu.RLock()
	defer s.jobsMu.RUnlock()
	for _, job := range s.jobs {
		if job.Status == 0 || job.Status == 1 {
			return true
		}
	}
	return false
}

func (s *Server) runEmbyRefresh() {
	// 还有任务在排队或处理时再等一轮，等整批结束后只扫描一次。
	if s.scrapeBusy() {
		s.embyRefresh.mu.Lock()
		s.embyRefresh.timer = time.AfterFunc(embyRefreshDelay, s.runEmbyRefresh)
		s.embyRefresh.mu.Unlock()
		return
	}
	if !s.embyRefreshEnabled() {
		return
	}
	conn, err := s.embyConnection()
	if err != nil {
		slog.Warn("[emby] 自动扫描媒体库跳过", "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := refreshEmbyLibraries(ctx, conn); err != nil {
		slog.Warn("[emby] 自动扫描媒体库失败", "error", err)
		return
	}
	// 新影片入库后演员列表可能变化，下次打开演员管理时重新读取。
	s.actorDir.mu.Lock()
	s.actorDir.loaded = time.Time{}
	s.actorDir.mu.Unlock()
}

// refreshEmbyLibraries 让 Emby 扫描媒体库：选了媒体库时只扫描这些，否则扫描全部。
// Emby 收到请求后在后台扫描，接口立即返回。
func refreshEmbyLibraries(ctx context.Context, conn embyConn) error {
	if len(conn.libraries) == 0 {
		response, err := conn.send(ctx, http.MethodPost, "/Library/Refresh", nil, nil, "")
		if err != nil {
			return err
		}
		response.Body.Close()
		slog.Info("[emby] 已请求扫描全部媒体库")
		return nil
	}
	query := url.Values{"Recursive": {"true"}, "MetadataRefreshMode": {"Default"}, "ImageRefreshMode": {"Default"}}
	for _, id := range conn.libraries {
		response, err := conn.send(ctx, http.MethodPost, "/Items/"+url.PathEscape(id)+"/Refresh", query, nil, "")
		if err != nil {
			return err
		}
		response.Body.Close()
	}
	slog.Info("[emby] 已请求扫描媒体库", "count", len(conn.libraries))
	return nil
}
