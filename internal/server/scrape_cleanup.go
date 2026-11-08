package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var retrySubtitleExtensions = map[string]bool{
	".ass": true, ".dfxp": true, ".idx": true, ".mks": true,
	".sami": true, ".sbv": true, ".smi": true, ".srt": true,
	".ssa": true, ".sub": true, ".sup": true, ".ttml": true,
	".vtt": true,
}

// removeRetryProducts 在重试前删除记录上一次刮削的产物，按记录上一次使用的整理模式找产物：
//   - 移动、原地整理：产物在视频旁边，path 就是那个视频。
//   - STRM：产物（.strm、NFO、图片、预告片）在整理目录，与 .strm 同一文件夹；源视频所在的监控目录里
//     没有本程序写的文件，不能清理那里，否则会删掉监控目录里用户自己的文件。
//
// 本次改用其它整理模式时也按上一次的模式清理，旧的 .strm 不会留在整理目录里。
func removeRetryProducts(job *scrapeJob, path string, settings scrapeSettings) ([]string, error) {
	if job.LinkMode != linkModeSTRM {
		return removeScrapeProducts(path, settings)
	}
	if job.Output == "" || !strings.EqualFold(filepath.Ext(job.Output), ".strm") {
		// 上一次没有写出 .strm（例如刮削失败），整理目录里没有它的产物。
		return []string{}, nil
	}
	removed, err := removeScrapeProducts(job.Output, settings)
	if err != nil {
		return removed, err
	}
	if fileExists(job.Output) {
		if err := os.Remove(job.Output); err != nil {
			return removed, fmt.Errorf("删除旧的 STRM 文件 %s：%w", job.Output, err)
		}
		removed = append(removed, filepath.Base(job.Output))
	}
	// 文件夹是整理时创建的，清空后一并删除；os.Remove 不会删除非空目录。
	_ = os.Remove(filepath.Dir(job.Output))
	return removed, nil
}

func removeScrapeProducts(path string, settings scrapeSettings) ([]string, error) {
	if path == "" || filepath.Dir(path) == path {
		return nil, fmt.Errorf("无效的视频路径")
	}
	videoPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("解析视频路径失败：%w", err)
	}
	entries, err := os.ReadDir(filepath.Dir(videoPath))
	if errors.Is(err, os.ErrNotExist) {
		// 产物所在文件夹已被删除，没有需要清理的文件，重试可以继续。
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	removed := []string{}
	configuredVideoExtensions := map[string]bool{}
	for _, extension := range settings.Common.Extensions {
		extension = strings.ToLower(strings.TrimSpace(extension))
		if extension != "" {
			configuredVideoExtensions["."+strings.TrimPrefix(extension, ".")] = true
		}
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		candidate := filepath.Join(filepath.Dir(videoPath), name)
		if samePath(candidate, videoPath) {
			continue
		}
		if isRetryTrailerProduct(name) {
			if err := os.Remove(candidate); err != nil {
				return removed, fmt.Errorf("删除重试目录文件 %s：%w", candidate, err)
			}
			removed = append(removed, name)
			continue
		}
		extension := strings.ToLower(filepath.Ext(name))
		if configuredVideoExtensions[extension] || retrySubtitleExtensions[extension] {
			continue
		}
		if err := os.Remove(candidate); err != nil {
			return removed, fmt.Errorf("删除重试目录文件 %s：%w", candidate, err)
		}
		removed = append(removed, name)
	}
	return removed, nil
}

func isRetryTrailerProduct(name string) bool {
	name = strings.ToLower(name)
	for _, extension := range []string{".mp4", ".webm"} {
		if name == "trailer"+extension || strings.HasSuffix(name, "-trailer"+extension) {
			return true
		}
	}
	return false
}
