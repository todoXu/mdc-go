package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"
)

// avatarFillState 是补头像任务的进度。页面通过 GET /api/actor-tools/fill-missing 轮询读取。
type avatarFillState struct {
	mu         sync.Mutex
	running    bool
	started    string
	finished   string
	errMsg     string
	missing    int
	noGfriends int
	total      int
	done       int
	updated    int
	failed     []string
	cancel     context.CancelFunc
}

// avatarFillSnapshot 是返回给页面的快照，字段名与前端约定一致。
type avatarFillSnapshot struct {
	Running    bool     `json:"running"`
	Started    string   `json:"started,omitempty"`
	Finished   string   `json:"finished,omitempty"`
	Error      string   `json:"error,omitempty"`
	Missing    int      `json:"missing"`
	NoGfriends int      `json:"no_gfriends"`
	Total      int      `json:"total"`
	Done       int      `json:"done"`
	Updated    int      `json:"updated"`
	Failed     []string `json:"failed"`
}

func (s *avatarFillState) snapshot() avatarFillSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return avatarFillSnapshot{
		Running: s.running, Started: s.started, Finished: s.finished, Error: s.errMsg,
		Missing: s.missing, NoGfriends: s.noGfriends, Total: s.total, Done: s.done, Updated: s.updated,
		Failed: append([]string{}, s.failed...),
	}
}

// startFillMissingAvatars 提供 POST /api/actor-tools/fill-missing：启动后台补头像，立即返回 202。
// 已经在运行时返回 409。
func (s *Server) startFillMissingAvatars(w http.ResponseWriter, r *http.Request) {
	conn, err := s.embyConnection()
	if err != nil {
		embyFailure(w, err)
		return
	}
	s.avatarFill.mu.Lock()
	if s.avatarFill.running {
		s.avatarFill.mu.Unlock()
		fail(w, 409, "fill_running", "补头像正在进行中")
		return
	}
	s.avatarFill.running, s.avatarFill.started, s.avatarFill.finished, s.avatarFill.errMsg = true, time.Now().Format(time.RFC3339), "", ""
	s.avatarFill.missing, s.avatarFill.noGfriends, s.avatarFill.total, s.avatarFill.done, s.avatarFill.updated = 0, 0, 0, 0, 0
	s.avatarFill.failed = nil
	s.avatarFill.mu.Unlock()
	go s.runFillMissingAvatars(conn)
	writeJSON(w, 202, s.avatarFill.snapshot())
}

// fillMissingAvatarStatus 提供 GET /api/actor-tools/fill-missing：返回补头像的当前进度。
func (s *Server) fillMissingAvatarStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.avatarFill.snapshot())
}

// cancelFillMissingAvatars 提供 POST /api/actor-tools/fill-missing/cancel：取消正在进行的补头像。
// 已经上传成功的头像保留；未处理的演员记为「已取消，未处理」。任务没有在运行时返回 409。
func (s *Server) cancelFillMissingAvatars(w http.ResponseWriter, r *http.Request) {
	s.avatarFill.mu.Lock()
	if !s.avatarFill.running || s.avatarFill.cancel == nil {
		s.avatarFill.mu.Unlock()
		fail(w, 409, "fill_not_running", "当前没有正在进行的补头像")
		return
	}
	s.avatarFill.cancel()
	s.avatarFill.mu.Unlock()
	writeJSON(w, 202, object{"success": true})
}

// runFillMissingAvatars 在后台为 Emby 中没有头像的演员补头像：使用 Gfriends 文件树中该演员的最后一张头像。
// 不依赖任何 HTTP 请求，浏览器关闭或刷新不会中断它。
func (s *Server) runFillMissingAvatars(conn embyConn) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	s.avatarFill.mu.Lock()
	s.avatarFill.cancel = cancel
	s.avatarFill.mu.Unlock()
	finish := func(errMsg string) {
		s.avatarFill.mu.Lock()
		s.avatarFill.running = false
		s.avatarFill.cancel = nil
		s.avatarFill.finished = time.Now().Format(time.RFC3339)
		s.avatarFill.errMsg = errMsg
		s.avatarFill.mu.Unlock()
	}
	actors, _, err := s.actorDir.load(ctx, conn, true)
	if err != nil {
		finish("读取 Emby 演员失败：" + err.Error())
		return
	}
	if err := s.gallery.require(ctx, s, galleryGfriends); err != nil {
		finish("Gfriends 不可用：" + err.Error())
		return
	}
	mapping, _, _ := s.actorMappingState()
	gallery := s.gallery.snapshot()
	type job struct {
		actor embyActor
		path  string
	}
	var jobs []job
	missing, noImage := 0, 0
	for _, actor := range actors {
		if actor.ImageTag != "" {
			continue
		}
		missing++
		images := gallery.gfriendsImages(actorNames(actor.Name, mapping))
		if len(images) == 0 {
			noImage++
			continue
		}
		jobs = append(jobs, job{actor: actor, path: images[len(images)-1].Path})
	}
	s.avatarFill.mu.Lock()
	s.avatarFill.missing, s.avatarFill.noGfriends, s.avatarFill.total = missing, noImage, len(jobs)
	s.avatarFill.mu.Unlock()

	// record 记录一个演员的处理结果：成功计入 updated，失败写入 failed，两者都计入 done。
	record := func(name string, err error) {
		s.avatarFill.mu.Lock()
		defer s.avatarFill.mu.Unlock()
		s.avatarFill.done++
		if err != nil {
			s.avatarFill.failed = append(s.avatarFill.failed, name+"："+err.Error())
			return
		}
		s.avatarFill.updated++
	}
	queue := make(chan job)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range queue {
				data, err := s.gallery.image(ctx, s, galleryGfriends, item.path)
				// 图片下载偶尔失败，失败后最多再试两次。
				for attempt := 1; err != nil && attempt < 3 && ctx.Err() == nil; attempt++ {
					data, err = s.gallery.image(ctx, s, galleryGfriends, item.path)
				}
				if err == nil {
					_, err = s.uploadEmbyAvatar(ctx, conn, item.actor.ID, data)
				}
				if err != nil {
					slog.Warn("[actors] 补头像失败", "name", item.actor.Name, "error", err)
				}
				record(item.actor.Name, err)
			}
		}()
	}
	for i, item := range jobs {
		if ctx.Err() != nil {
			// 超时后剩下的演员不再处理，但要计入失败，不能静默丢掉。
			reason := errors.New("补全超时，未处理")
			if errors.Is(ctx.Err(), context.Canceled) {
				reason = errors.New("已取消，未处理")
			}
			for _, rest := range jobs[i:] {
				record(rest.actor.Name, reason)
			}
			break
		}
		queue <- item
	}
	close(queue)
	wg.Wait()
	s.avatarFill.mu.Lock()
	sort.Strings(s.avatarFill.failed)
	updated, failed := s.avatarFill.updated, len(s.avatarFill.failed)
	s.avatarFill.mu.Unlock()
	slog.Info("[actors] 补头像完成", "missing", missing, "updated", updated, "no_gfriends", noImage, "failed", failed)
	if errors.Is(ctx.Err(), context.Canceled) {
		finish("已取消")
		return
	}
	finish("")
}
