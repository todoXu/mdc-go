package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"mdcng-go/internal/provider"
)

type scrapeEvent struct {
	Time    string `json:"time"`
	Stage   string `json:"stage"`
	Message string `json:"message"`
}
type scrapeTraceKey struct{}
type scrapeTrace struct {
	log      func(string, string)
	metadata func(provider.MergeResult, map[string]provider.Metadata)
	// sourceErrors 在一次抓取结束后报告失败的数据源（数据源 ID → 失败原因）。可以为 nil。
	sourceErrors func(map[string]string)
	nfoTags      func(*nfoTagTrace)
}

func traceScrape(ctx context.Context, stage, message string) {
	if trace, ok := ctx.Value(scrapeTraceKey{}).(scrapeTrace); ok {
		trace.log(stage, message)
	}
}

func (s *Server) getScrapeJob(w http.ResponseWriter, r *http.Request) {
	s.jobsMu.RLock()
	var job scrapeJob
	for _, item := range s.jobs {
		if item.ID == r.PathValue("id") {
			job = item
			break
		}
	}
	// Copy mutable slices while holding the lock; metadata and source snapshots are immutable.
	job.Logs = append([]scrapeEvent{}, job.Logs...)
	s.jobsMu.RUnlock()
	if job.ID == "" {
		fail(w, 404, "not_found", "记录不存在或服务重启后已清空")
		return
	}
	if job.Status == 1 {
		if started, err := time.Parse(time.RFC3339Nano, job.StartedAt); err == nil {
			job.DurationMS = time.Since(started).Milliseconds()
		}
	}
	if job.Metadata != nil {
		meta := *job.Metadata
		meta.ImagePreviews = map[string]string{}
		meta.LocalImages = map[string]string{}
		for _, field := range []string{"poster", "cover", "samples"} {
			id := job.Provenance[field]
			source, ok := job.SourceResults[id]
			if !ok {
				continue
			}
			urls := []string{}
			switch field {
			case "poster":
				urls = []string{meta.Poster}
			case "cover":
				urls = []string{meta.Cover}
			case "samples":
				urls = meta.Samples
			}
			source = provider.Metadata{Provider: id, SourceURL: source.SourceURL, Samples: urls}
			s.registerImages(&source)
			for address, preview := range source.ImagePreviews {
				meta.ImagePreviews[address] = preview
			}
		}
		if job.Status == 2 && job.Output != "" {
			base := strings.TrimSuffix(job.Output, filepath.Ext(job.Output))
			imageWithPrefix := false
			if job.Settings != nil {
				imageWithPrefix = job.Settings.Naming.ImageWithPrefix
			}
			assets := map[string]string{"poster": "poster", "cover": "thumb", "fanart": "fanart", "trailer": "trailer"}
			for i := 1; i <= 32; i++ {
				index := strconv.Itoa(i)
				assets["sample_"+index] = "extrafanart-" + strings.Repeat("0", 3-len(index)) + index
			}
			// 一次读取输出目录再在内存中匹配。逐个 Lstat 约 200 个候选文件，
			// 在网络盘上每次查不到都要等待，会让详情接口超时。
			files := regularFilesIn(filepath.Dir(base))
			for field, suffix := range assets {
				exts := []string{".jpg", ".png", ".webp"}
				if field == "trailer" {
					exts = []string{".mp4", ".webm"}
				}
				for _, ext := range exts {
					paths := scrapeNamedAssetCandidateOrder(base, suffix+ext, imageWithPrefix)
					found := false
					for _, path := range paths {
						if info := files.lookup(path); info != nil {
							revision := strconv.FormatInt(info.ModTime().UnixNano(), 10) + "-" + strconv.FormatInt(info.Size(), 10)
							meta.LocalImages[field] = "/api/scrape/jobs/" + job.ID + "/images/" + suffix + ext + "?v=" + revision
							found = true
							break
						}
					}
					if found {
						break
					}
				}
			}
		}
		job.Metadata = &meta
	}
	// Expose only display switches, keeping the rest of the settings private.
	options := map[string]bool{"thumb": false, "fanart": false, "extra_fanarts": false, "trailer": false}
	if job.Settings != nil {
		options["thumb"] = job.Settings.Download.Thumb
		options["fanart"] = job.Settings.Download.Fanart
		options["extra_fanarts"] = job.Settings.Download.ExtraFanarts
		options["trailer"] = job.Settings.Download.Trailer
	}
	writeJSON(w, 200, struct {
		scrapeJob
		DownloadOptions map[string]bool              `json:"download_options"`
		Sources         map[string]provider.Metadata `json:"sources"`
	}{job, options, job.SourceResults})
}

// directoryFiles 是一个目录下普通文件的快照，按文件名精确匹配，再按忽略大小写匹配（Windows 文件名不区分大小写）。
type directoryFiles struct {
	dir   string
	exact map[string]os.FileInfo
	fold  map[string]os.FileInfo
}

func regularFilesIn(dir string) directoryFiles {
	files := directoryFiles{dir: filepath.Clean(dir), exact: map[string]os.FileInfo{}, fold: map[string]os.FileInfo{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return files
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files.exact[entry.Name()] = info
		if _, ok := files.fold[strings.ToLower(entry.Name())]; !ok {
			files.fold[strings.ToLower(entry.Name())] = info
		}
	}
	return files
}

// lookup 返回 path 对应的文件信息；path 不在快照目录中时返回 nil。
func (files directoryFiles) lookup(path string) os.FileInfo {
	if filepath.Clean(filepath.Dir(path)) != files.dir {
		return nil
	}
	name := filepath.Base(path)
	if info, ok := files.exact[name]; ok {
		return info
	}
	if pathsCaseInsensitive {
		return files.fold[strings.ToLower(name)]
	}
	return nil
}

var recordExtraImage = regexp.MustCompile(`^extrafanart-(00[1-9]|0[12][0-9]|03[0-2])\.(jpg|png|webp)$`)

func (s *Server) serveScrapeImage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	allowed := false
	// 不带扩展名的 poster / thumb / fanart 依次尝试 .jpg、.png、.webp，列表页不必知道实际格式。
	names := []string{name}
	for _, suffix := range []string{"poster", "thumb", "fanart"} {
		if name == suffix {
			allowed = true
			names = []string{suffix + ".jpg", suffix + ".png", suffix + ".webp"}
		}
		for _, ext := range []string{".jpg", ".png", ".webp"} {
			if name == suffix+ext {
				allowed = true
			}
		}
	}
	allowed = allowed || recordExtraImage.MatchString(name) || name == "trailer.mp4" || name == "trailer.webm"
	if !allowed {
		fail(w, 404, "not_found", "图片不存在")
		return
	}
	s.jobsMu.RLock()
	output := ""
	imageWithPrefix := false
	for _, job := range s.jobs {
		if job.ID == r.PathValue("id") && job.Status == 2 {
			output = job.Output
			if job.Settings != nil {
				imageWithPrefix = job.Settings.Naming.ImageWithPrefix
			}
			break
		}
	}
	s.jobsMu.RUnlock()
	if output == "" {
		fail(w, 404, "not_found", "图片不存在")
		return
	}
	base := strings.TrimSuffix(output, filepath.Ext(output))
	path := ""
	var info os.FileInfo
	var err error
	for _, candidateName := range names {
		for _, candidate := range scrapeNamedAssetCandidateOrder(base, candidateName, imageWithPrefix) {
			info, err = os.Lstat(candidate)
			if err == nil && info.Mode().IsRegular() {
				path, name = candidate, candidateName
				break
			}
		}
		if path != "" {
			break
		}
	}
	if path == "" {
		fail(w, 404, "not_found", "图片不存在")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		fail(w, 404, "not_found", "图片不存在")
		return
	}
	defer file.Close()
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// 带版本号（?v=）的地址由浏览器长期缓存：详情页的版本号是文件修改时间和大小，
	// 列表海报的版本号是完成时间和日志条数（裁剪、编辑元数据、重试都会改变它）。
	// 图片一改，地址就变，浏览器会重新读取；不带版本号的地址不缓存。
	if r.URL.Query().Get("v") != "" {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, no-store, max-age=0")
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Type", map[string]string{".jpg": "image/jpeg", ".png": "image/png", ".webp": "image/webp", ".mp4": "video/mp4", ".webm": "video/webm"}[filepath.Ext(name)])
	http.ServeContent(w, r, name, info.ModTime(), file)
}
