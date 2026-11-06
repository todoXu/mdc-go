package server

import (
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

func removeScrapeProducts(path string, settings scrapeSettings) ([]string, error) {
	if path == "" || filepath.Dir(path) == path {
		return nil, fmt.Errorf("无效的视频路径")
	}
	videoPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("解析视频路径失败：%w", err)
	}
	entries, err := os.ReadDir(filepath.Dir(videoPath))
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

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
}
