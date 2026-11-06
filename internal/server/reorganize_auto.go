package server

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// queueReorganize 在元数据的命名相关字段（演员、番号、标题、日期、厂商等）变化后调用：
// 按新元数据重新整理记录，目录和文件名随之改变。from 是保存前的输出文件，整理成功后会清理它。
// 移动模式的源是输出视频；STRM 模式的源是源视频（原地不动）。
func (s *Server) queueReorganize(id, from string) {
	s.jobsMu.RLock()
	directoryID := ""
	for i := range s.jobs {
		if s.jobs[i].ID == id {
			directoryID = s.jobs[i].DirectoryID
			break
		}
	}
	s.jobsMu.RUnlock()
	settings, err := s.recordSettings(scrapeJob{DirectoryID: directoryID})
	if err != nil {
		slog.Error("[reorganize] 读取整理设置失败", "id", id, "error", err)
		return
	}
	// 编辑元数据时已经检查过；这里再检查一次，防止两次请求之间整理方式被修改。
	s.jobsMu.RLock()
	var current scrapeJob
	for i := range s.jobs {
		if s.jobs[i].ID == id {
			current = s.jobs[i]
			break
		}
	}
	s.jobsMu.RUnlock()
	if err := linkModeChangeError(current, settings.Common.LinkMode); err != nil {
		slog.Warn("[reorganize] 整理方式已改变，跳过自动重新整理", "id", id, "error", err)
		return
	}

	queued := false
	s.jobsMu.Lock()
	for i := range s.jobs {
		item := &s.jobs[i]
		if item.ID != id || item.Status != 2 || item.Output == "" {
			continue
		}
		path := item.Output
		if item.LinkMode == linkModeSTRM {
			path = item.Path
		}
		item.Path = path
		item.Directory = filepath.Dir(path)
		item.Reorganize = true
		item.RetryExisting = false
		item.Settings = &settings
		item.ReorgFrom = from
		item.Source = "reorganize"
		item.Status = 0
		item.StartedAt, item.CompletedAt, item.Error = "", "", ""
		item.DurationMS, item.Cancel, item.StopRequested, item.Warnings = 0, nil, false, nil
		item.Logs = []scrapeEvent{{Time: time.Now().Format(time.RFC3339Nano), Stage: "自动重新整理", Message: "元数据字段已变化，按新设置重新整理文件"}}
		s.publishJobChanged(item.ID, true)
		queued = true
		break
	}
	s.jobsMu.Unlock()
	if queued {
		s.saveScrapeHistory()
		go s.runScrapeJobs([]string{id})
	}
}

// cleanupReorganizedOutput 在重新整理成功后删除旧位置的文件：
//   - STRM：删除旧 .strm、旧 NFO 和旧图片；旧文件夹空了就删除。
//   - 移动：视频已经移到新位置，只清理旧 NFO、图片；旧文件夹为空且「移动后删除源文件夹」开启时删除。
//
// 新旧位置在同一文件夹时，图片名可能相同，因此不删图片，避免删掉刚写入的新文件。
func cleanupReorganizedOutput(from, to string, settings scrapeSettings, linkMode int) {
	if from == "" || to == "" || filepath.Clean(from) == filepath.Clean(to) {
		return
	}
	oldDir, newDir := filepath.Dir(from), filepath.Dir(to)
	sameDir := pathKey(oldDir) == pathKey(newDir)
	base := strings.TrimSuffix(from, filepath.Ext(from))
	newBase := strings.TrimSuffix(to, filepath.Ext(to))
	remove := func(path string) {
		if !fileExists(path) {
			return
		}
		if err := os.Remove(path); err != nil {
			slog.Warn("[reorganize] 删除旧文件失败", "path", path, "error", err)
		}
	}
	if linkMode == linkModeSTRM {
		remove(from)
	}
	if base != newBase {
		remove(base + ".nfo")
	}
	if !sameDir {
		for _, name := range oldAssetNames() {
			for _, candidate := range scrapeNamedAssetCandidateOrder(base, name, settings.Naming.ImageWithPrefix) {
				remove(candidate)
			}
		}
		if linkMode == linkModeSTRM || settings.Common.DeleteEmptyParentAfterMove {
			// 只删除空文件夹：os.Remove 对非空目录会失败，不会误删其它文件。
			if err := os.Remove(oldDir); err == nil {
				slog.Info("[reorganize] 已删除旧文件夹", "path", oldDir)
			}
		}
	}
}

// oldAssetNames 是记录生成的图片和预告片的文件名（不含目录和前缀）。
func oldAssetNames() []string {
	names := []string{"poster.jpg", "poster.png", "poster.webp", "thumb.jpg", "thumb.png", "thumb.webp", "fanart.jpg", "fanart.png", "fanart.webp", "trailer.mp4", "trailer.webm"}
	for i := 1; i <= 32; i++ {
		for _, ext := range []string{".jpg", ".png", ".webp"} {
			names = append(names, fmt.Sprintf("extrafanart-%03d%s", i, ext))
		}
	}
	return names
}
