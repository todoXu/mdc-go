package server

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"mdcng-go/internal/provider"
)

type scrapeJob struct {
	ID            string                       `json:"id"`
	Sequence      int                          `json:"sequence"`
	Path          string                       `json:"path"`
	Directory     string                       `json:"directory"`
	Number        string                       `json:"number,omitempty"`
	Actors        []string                     `json:"actors"`
	HasSubtitle   bool                         `json:"has_subtitle"`
	FileInfo      filenameFlags                `json:"file_info"`
	LinkMode      int                          `json:"link_mode"`
	Source        string                       `json:"source"`
	DirectoryID   string                       `json:"directory_id,omitempty"`
	Status        int                          `json:"status"` // 0 waiting, 1 running, 2 completed, -1 failed
	CreatedAt     string                       `json:"created_at"`
	StartedAt     string                       `json:"started_at,omitempty"`
	CompletedAt   string                       `json:"completed_at,omitempty"`
	DurationMS    int64                        `json:"duration_ms"`
	Output        string                       `json:"output,omitempty"`
	Error         string                       `json:"error,omitempty"`
	Warnings      []string                     `json:"warnings,omitempty"`
	Settings      *scrapeSettings              `json:"-"`
	Cancel        context.CancelFunc           `json:"-"`
	StopRequested bool                         `json:"stop_requested,omitempty"`
	Reorganize    bool                         `json:"-"`
	ReorgFrom     string                       `json:"-"` // 自动重新整理前的旧输出文件，成功后清理
	RetryExisting bool                         `json:"-"`
	Editing       bool                         `json:"editing,omitempty"`
	Metadata      *provider.Metadata           `json:"metadata,omitempty"`
	Provenance    map[string]string            `json:"provenance,omitempty"`
	Mappings      []metadataMapping            `json:"mappings,omitempty"` // 预留：映射表转换明细
	NFOTags       *nfoTagTrace                 `json:"nfo_tags,omitempty"` // 最近一次成功写入 NFO 时的标签来源
	Logs          []scrapeEvent                `json:"logs,omitempty"`
	Progress      string                       `json:"progress,omitempty"`      // 仅列表接口填写：处理中记录的最新一条日志
	Phase         string                       `json:"phase,omitempty"`         // 仅列表接口填写：处理中记录所在阶段，见 jobPhase
	ActorLimit    int                          `json:"actor_limit,omitempty"`   // 仅列表接口填写：该记录所在目录的「演员显示数量上限」
	SourceErrors  map[string]string            `json:"source_errors,omitempty"` // 最近一次抓取中失败的数据源及原因
	Poster        string                       `json:"poster,omitempty"`        // 仅列表接口填写：海报模式使用的本地海报地址（不含扩展名，由页面依次尝试）
	Series        string                       `json:"series,omitempty"`        // 仅列表接口填写：系列
	SourceResults map[string]provider.Metadata `json:"-"`
	// NotifyFailedSources 仅列表接口填写：成功记录中抓取失败、且开启了「失败时特别提示」的数据源 ID（已排序）。
	NotifyFailedSources []string `json:"notify_failed_sources,omitempty"`
}

// jobProgress 返回处理中记录的最新一条日志，用于列表里的进度文字；其它状态返回空。
func jobProgress(job *scrapeJob) string {
	if job.Status != 1 || len(job.Logs) == 0 {
		return ""
	}
	return job.Logs[len(job.Logs)-1].Message
}

// 刮削任务的五个阶段，按执行顺序排列，概览页据此统计处理中的任务分布。
const (
	phaseIdentify = "identify" // 识别：解析番号、识别文件标记
	phaseSource   = "source"   // 数据源：并行抓取并合并元数据
	phaseImage    = "image"    // 图片：计算目标文件名后下载、裁剪图片并加水印
	phaseWrite    = "write"    // 写入：写入 NFO、图片等文件
	phaseOrganize = "organize" // 整理：移动或重命名视频，记录结果
)

// scrapeStagePhases 把日志的阶段名归入五个阶段。未列出的阶段名沿用上一条日志的阶段。
var scrapeStagePhases = map[string]string{
	"准备重试": phaseIdentify, "自动重新整理": phaseIdentify, "重新整理": phaseIdentify,
	"解析番号": phaseIdentify, "识别文件标记": phaseIdentify,
	"刮削数据":  phaseSource,
	"计算文件名": phaseImage, "下载图片": phaseImage, "选择图片": phaseImage, "海报裁剪": phaseImage,
	"写入水印": phaseImage, "图片处理": phaseImage, "下载剧照": phaseImage, "下载预告片": phaseImage,
	"保存文件": phaseWrite,
	"整理视频": phaseOrganize, "处理结果": phaseOrganize,
}

// jobPhase 返回处理中记录当前所在的阶段；还没有日志时视为识别阶段，其它状态返回空。
func jobPhase(job *scrapeJob) string {
	if job.Status != 1 {
		return ""
	}
	for i := len(job.Logs) - 1; i >= 0; i-- {
		if phase, ok := scrapeStagePhases[job.Logs[i].Stage]; ok {
			return phase
		}
	}
	return phaseIdentify
}

type scrapeSettings struct {
	FlagsOverride    *taskFlags                 `json:"flags_override,omitempty"`
	OverrideURL      string                     `json:"override_url,omitempty"`
	OverrideProvider string                     `json:"override_provider,omitempty"`
	OverrideNumber   string                     `json:"override_number,omitempty"`
	ProviderConfigs  map[string]provider.Config `json:"provider_configs,omitempty"`
	Priorities       *provider.Priorities       `json:"priorities,omitempty"`
	Watermark        map[string]any             `json:"watermark"`
	Common           struct {
		LinkMode                       int      `json:"link_mode"`
		TargetFolder                   string   `json:"target_folder"`
		StrmPathFrom                   string   `json:"strm_path_from"`
		StrmPathTo                     string   `json:"strm_path_to"`
		DeleteMetadataFolderWhenFailed bool     `json:"delete_metadata_folder_when_failed"`
		DeleteEmptyParentAfterMove     bool     `json:"delete_empty_parent_after_move"`
		TargetOverwriteVideo           bool     `json:"target_overwrite_video"`
		TargetOverwritePic             bool     `json:"target_overwrite_pic"`
		Extensions                     []string `json:"file_ext_filter"`
		MinSize                        int64    `json:"file_size_filter"`
		Blacklist                      []string `json:"file_name_blacklist"`
		JAV                            []string `json:"jav_hack_keywords"`
		LADA                           []string `json:"lada_hack_keywords"`
		LTX                            []string `json:"ltx_hack_keywords"`
		Subtitle                       []string `json:"subtitle_keywords"`
		Other                          []string `json:"other_hack_keywords"`
		Uncensored                     []string `json:"uncensored_keywords"`
	} `json:"common"`
	Naming struct {
		Folder          string         `json:"folder"`
		MaxFolderLength int            `json:"max_folder_length"`
		File            string         `json:"file"`
		Title           string         `json:"title"`
		Suffix          string         `json:"suffix"`
		MaxActor        int            `json:"max_actor_count"`
		ImageWithPrefix bool           `json:"image_with_prefix"`
		Suffixes        map[string]any `json:"suffixs"`
	} `json:"naming"`
	Download struct {
		Poster       bool   `json:"poster"`
		Thumb        bool   `json:"thumb"`
		Fanart       bool   `json:"fanart"`
		ExtraFanarts bool   `json:"extra_fanarts"`
		Trailer      bool   `json:"trailer"`
		Strategy     string `json:"thumb_download_strategy"`
		MinWidth     int    `json:"thumb_min_width"`
		MinHeight    int    `json:"thumb_min_height"`
		CropRegular  string `json:"crop_type_censored"`
		CropFC2      string `json:"crop_type_fc2"`
		Aspect       string `json:"crop_aspect"`
		CropPoster   bool   `json:"crop_poster"`
		PreferCrop   bool   `json:"crop_when_poster_low_res"`
	} `json:"download"`
	NFO struct {
		Enabled             bool   `json:"enabled"`
		EnableSortTitle     bool   `json:"enable_sorttitle"`
		EnableOriginalTitle bool   `json:"enable_originaltitle"`
		AddPartToTitle      bool   `json:"add_part_to_title"`
		EnableOutline       bool   `json:"enable_outline"`
		EnableOriginalPlot  bool   `json:"enable_originalplot"`
		EnablePlot          bool   `json:"enable_plot"`
		AddTranslatedBy     bool   `json:"add_translated_by"`
		EnableRelease       bool   `json:"enable_release"`
		EnableReleaseDate   bool   `json:"enable_releasedate"`
		EnablePremiered     bool   `json:"enable_premiered"`
		TaglineFormat       string `json:"tagline_format"`
		EnableCountry       bool   `json:"enable_country"`
		CountryCode         string `json:"country_code"`
		EnableMPAA          bool   `json:"enable_mpaa"`
		EnableCustomRating  bool   `json:"enable_customrating"`
		EnableYear          bool   `json:"enable_year"`
		EnableRuntime       bool   `json:"enable_runtime"`
		EnableVotes         bool   `json:"enable_votes"`
		EnableScore         bool   `json:"enable_score"`
		EnableCriticRating  bool   `json:"enable_criticrating"`
		EnableActor         bool   `json:"enable_actor"`
		IncludeMaleActors   bool   `json:"include_male_actors"`
		EnableDirector      bool   `json:"enable_director"`
		EnableSeries        bool   `json:"enable_series"`
		EnableTag           bool   `json:"enable_tag"`
		EnableGenre         bool   `json:"enable_genre"`
		EnableStudio        bool   `json:"enable_studio"`
		EnableMaker         bool   `json:"enable_maker"`
		EnablePublisher     bool   `json:"enable_publisher"`
		EnableLabel         bool   `json:"enable_label"`
		EnablePoster        bool   `json:"enable_poster"`
		EnableCover         bool   `json:"enable_cover"`
		EnableTrailer       bool   `json:"enable_trailer"`
		EnableWebsite       bool   `json:"enable_website"`
		SetByActress        bool   `json:"set_by_actress"`
		SetBySeries         bool   `json:"set_by_series"`
		SetBySerialName     bool   `json:"set_by_serial_name"`
		AdditionalTags      struct {
			SerialName      bool   `json:"serial_name"`
			Actors          bool   `json:"actors"`
			Resolution      bool   `json:"resolution"`
			Mosaic          bool   `json:"mosaic"`
			HackType        bool   `json:"hack_type"`
			HasSubtitle     bool   `json:"has_subtitle"`
			SubtitleFormat  string `json:"subtitle_format"`
			Series          bool   `json:"series"`
			SeriesFormat    string `json:"series_format"`
			Studio          bool   `json:"studio"`
			StudioFormat    string `json:"studio_format"`
			Publisher       bool   `json:"publisher"`
			PublisherFormat string `json:"publisher_format"`
		} `json:"additional_tags"`
	} `json:"nfo"`
	System struct {
		Threads int `json:"scrape_thread_count"`
		Timeout int `json:"scrape_timeout"`
	} `json:"system"`
	// Metadata 是元数据设置中与演员映射有关的部分。
	Metadata struct {
		UseActorMapping bool   `json:"use_actor_mapping"`
		ActorCustom     bool   `json:"actor_mapping_custom"`
		StaticLang      string `json:"static_data_lang"`
	} `json:"metadata"`
}

var filenameNumber = regexp.MustCompile(`(?i)(?:^|[^A-Z0-9])([A-Z]{2,10})[-_ ]?([0-9]{2,6})(?:[^A-Z0-9]|$)`)
var filenameFC2Number = regexp.MustCompile(`(?i)(?:^|[^A-Z0-9])FC2[-_ ]?PPV[-_ ]?([0-9]{4,7})(?:[^A-Z0-9]|$)`)
var namingVariable = regexp.MustCompile(`\{([a-z_]+)\}`)
var badFilename = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

// actorLimitFor 返回监控目录适用的「演员显示数量上限」。目录使用独立命名时取目录自己的值，否则取全局值；
// 未设置或为 0 时使用默认值 3。
func (s *Server) actorLimitFor(directoryID string) int {
	type namingLimit struct {
		MaxActor int `json:"max_actor_count"`
	}
	limit := 0
	s.mu.RLock()
	if raw := s.configs["naming"]; len(raw) > 0 {
		var global namingLimit
		if json.Unmarshal(raw, &global) == nil {
			limit = global.MaxActor
		}
	}
	s.mu.RUnlock()
	for _, dir := range s.currentWatchConfig().Directories {
		if dir.ID != directoryID || useGlobalConfigValue(dir.UseGlobalNaming) || len(dir.Naming) == 0 || string(dir.Naming) == "null" {
			continue
		}
		var own namingLimit
		if json.Unmarshal(dir.Naming, &own) == nil {
			limit = own.MaxActor
		}
		break
	}
	if limit <= 0 {
		limit = 3
	}
	return limit
}

// notifyFailureSources 返回数据源设置中「失败时特别提示」的数据源 ID 集合。
func (s *Server) notifyFailureSources() map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	set := map[string]bool{}
	if s.priorities != nil {
		for _, id := range s.priorities.NotifyFailures {
			set[id] = true
		}
	}
	return set
}

// notifyFailedSources 返回成功记录中抓取失败、且在「失败时特别提示」里的数据源（已排序）。
func notifyFailedSources(job *scrapeJob, notify map[string]bool) []string {
	if job.Status != 2 || len(notify) == 0 {
		return nil
	}
	var names []string
	for id := range job.SourceErrors {
		if notify[id] {
			names = append(names, id)
		}
	}
	sort.Strings(names)
	return names
}

func (s *Server) listScrapeJobs(w http.ResponseWriter, r *http.Request) {
	// 先读数据源设置再拿任务锁，保持与其它接口一致的加锁顺序。
	notify := s.notifyFailureSources()
	s.jobsMu.RLock()
	defer s.jobsMu.RUnlock()
	items := make([]scrapeJob, len(s.jobs))
	limits := map[string]int{}
	for i := range s.jobs {
		job := s.jobs[i]
		job.Progress = jobProgress(&job)
		job.Phase = jobPhase(&job)
		limit, ok := limits[job.DirectoryID]
		if !ok {
			limit = s.actorLimitFor(job.DirectoryID)
			limits[job.DirectoryID] = limit
		}
		job.ActorLimit = limit
		job.NotifyFailedSources = notifyFailedSources(&job, notify)
		if job.Metadata != nil {
			job.Series = job.Metadata.Series
		}
		// 只给成功的记录提供海报地址。版本号随完成时间和日志条数变化（重试、裁剪、编辑都会追加日志），
		// 海报墙据此判断图片是否更新：地址不变就沿用已加载的图片，变了才重新读取。
		if job.Status == 2 && job.Output != "" {
			job.Poster = "/api/scrape/jobs/" + url.PathEscape(job.ID) + "/images/poster?v=" + url.QueryEscape(fmt.Sprintf("%s-%d", job.CompletedAt, len(job.Logs)))
		}
		job.Metadata, job.Provenance, job.Logs, job.SourceResults, job.NFOTags = nil, nil, nil, nil, nil
		if job.Status == 1 {
			if started, err := time.Parse(time.RFC3339Nano, job.StartedAt); err == nil {
				job.DurationMS = time.Since(started).Milliseconds()
			}
		}
		items[len(s.jobs)-1-i] = job
	}
	writeJSON(w, 200, object{"data": items, "total_count": len(items), "num_pages": 1})
}

func (s *Server) enqueueScrapeJobs(paths []string, directoryID string, settings scrapeSettings) []string {
	s.jobsMu.Lock()
	active := make(map[string]bool)
	for _, job := range s.jobs {
		if job.Status == 0 || job.Status == 1 || job.Editing {
			active[pathKey(job.Path)] = true
		}
	}
	ids := make([]string, 0, len(paths))
	for _, path := range paths {
		key := pathKey(path)
		if active[key] {
			slog.Debug("[scrape] 跳过重复的文件", "path", path)
			continue
		}
		s.jobSequence++
		id := fmt.Sprintf("%d-%d", time.Now().UnixNano(), s.jobSequence)
		jobSettings := settings
		s.jobs = append(s.jobs, scrapeJob{
			ID: id, Sequence: s.jobSequence, Path: path, Directory: filepath.Dir(path),
			Number: detectNumber(path), Actors: []string{}, HasSubtitle: detectSubtitle(path), FileInfo: detectFilenameFlags(path, settings), LinkMode: settings.Common.LinkMode,
			Source: "watcher", DirectoryID: directoryID, CreatedAt: time.Now().Format(time.RFC3339Nano), Settings: &jobSettings,
		})
		ids = append(ids, id)
		s.publishJobChanged(id, true)
		active[key] = true
		slog.Debug("[scrape] 已加入队列", "id", id, "directory", directoryID, "path", path)
	}
	s.jobsMu.Unlock()
	if len(ids) > 0 {
		s.saveScrapeHistory()
		go s.runScrapeJobs(ids)
	}
	return ids
}

func eligibleFile(path string, settings scrapeSettings) bool {
	name := strings.ToLower(filepath.Base(path))
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	allowed := false
	for _, candidate := range settings.Common.Extensions {
		if strings.EqualFold(strings.TrimPrefix(candidate, "."), ext) {
			allowed = true
			break
		}
	}
	if !allowed {
		return false
	}
	for _, word := range settings.Common.Blacklist {
		if word != "" && strings.Contains(name, strings.ToLower(word)) {
			return false
		}
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() >= settings.Common.MinSize
}

func (s *Server) scrapeSettings() (scrapeSettings, error) {
	s.mu.RLock()
	parts := map[string]json.RawMessage{}
	for _, name := range []string{"common", "naming", "download", "watermark", "nfo", "system", "metadata"} {
		parts[name] = append([]byte(nil), s.configs[name]...)
	}
	s.mu.RUnlock()
	var settings scrapeSettings
	b, _ := json.Marshal(parts)
	err := json.Unmarshal(b, &settings)
	return settings, err
}

func (s *Server) updateJob(id string, change func(*scrapeJob)) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	for i := range s.jobs {
		if s.jobs[i].ID == id {
			before := jobListView(&s.jobs[i])
			change(&s.jobs[i])
			// 进度文字随事件一起推送，列表直接改那一格，不必重新拉取整个列表。
			s.jobEvents.publish("jobs-changed", jobChangedEvent{ID: id, List: before != jobListView(&s.jobs[i]), Progress: jobProgress(&s.jobs[i]), Phase: jobPhase(&s.jobs[i])})
			return
		}
	}
}

// scrapeLimiter 限制所有刮削任务同时运行的数量，上限为「刮削任务线程数」。
// 零值可直接使用。每个调用各自排队，不再互相等待，所以单条重试也受同一上限约束。
type scrapeLimiter struct {
	mu      sync.Mutex
	running int
	wake    chan struct{}
}

func (l *scrapeLimiter) acquire(limit int) {
	for {
		l.mu.Lock()
		if l.running < limit {
			l.running++
			l.mu.Unlock()
			return
		}
		if l.wake == nil {
			l.wake = make(chan struct{})
		}
		wake := l.wake
		l.mu.Unlock()
		<-wake
	}
}

func (l *scrapeLimiter) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.running--
	if l.wake != nil {
		close(l.wake)
		l.wake = nil
	}
}

func (s *Server) runScrapeJobs(ids []string) {
	settings, err := s.scrapeSettings()
	if err != nil {
		for _, id := range ids {
			s.updateJob(id, func(item *scrapeJob) {
				item.Status = -1
				item.Error = "读取刮削配置失败：" + err.Error()
				item.CompletedAt = time.Now().Format(time.RFC3339Nano)
			})
		}
		return
	}
	threads := settings.System.Threads
	if threads < 1 {
		threads = 1
	}
	workers := threads
	if workers > len(ids) {
		workers = len(ids)
	}
	timeout := time.Duration(settings.System.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	queue := make(chan string)
	var group sync.WaitGroup
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			for id := range queue {
				s.scrapeSlots.acquire(threads)
				s.runScrapeJob(id, timeout)
				s.scrapeSlots.release()
			}
		}()
	}
	for _, id := range ids {
		queue <- id
	}
	close(queue)
	group.Wait()
}

func (s *Server) runScrapeJob(id string, timeout time.Duration) {
	defer s.saveScrapeHistory()
	var job scrapeJob
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	s.updateJob(id, func(item *scrapeJob) {
		if item.Status != 0 {
			return
		}
		item.Status = 1
		item.Cancel = cancel
		item.StartedAt = started.Format(time.RFC3339Nano)
		job = *item
	})
	if job.ID == "" {
		return
	}
	slog.Debug("[scrape] 开始处理", "id", id, "directory", job.DirectoryID, "path", job.Path, "timeout", timeout)
	var pendingNFOTags *nfoTagTrace
	nfoTagsTraced := false
	ctx = context.WithValue(ctx, scrapeTraceKey{}, scrapeTrace{
		sourceErrors: func(failed map[string]string) {
			s.updateJob(id, func(item *scrapeJob) {
				item.SourceErrors = failed
			})
		},
		log: func(stage, message string) {
			s.updateJob(id, func(item *scrapeJob) {
				item.Logs = append(item.Logs, scrapeEvent{time.Now().Format(time.RFC3339Nano), stage, message})
			})
		},
		metadata: func(result provider.MergeResult, sources map[string]provider.Metadata) {
			s.updateJob(id, func(item *scrapeJob) {
				meta := result.Metadata
				item.Metadata = &meta
				item.Provenance = maps.Clone(result.Provenance)
				item.SourceResults = maps.Clone(sources)
			})
		},
		// 先暂存，只有整理成功、NFO 已实际写入后才保存到记录。
		nfoTags: func(trace *nfoTagTrace) { pendingNFOTags, nfoTagsTraced = trace, true },
	})
	traceScrape(ctx, "解析番号", "开始处理文件："+job.Path)
	var output string
	var warnings []string
	var err error
	settings := job.Settings
	if settings == nil {
		var current scrapeSettings
		current, err = s.scrapeSettings()
		settings = &current
	}
	if err == nil {
		s.updateJob(id, func(item *scrapeJob) { item.LinkMode = settings.Common.LinkMode })
		flags := detectFilenameFlags(job.Path, *settings)
		s.updateJob(id, func(item *scrapeJob) { item.FileInfo = flags; item.HasSubtitle = flags.HasSubtitle })
		traceScrape(ctx, "识别文件标记", fmt.Sprintf("字幕=%t（%s），影片类型=%s（%s）", flags.HasSubtitle, flags.SubtitleMarker, flags.Mosaic, flags.MosaicMarker))
		observe := func(meta provider.Metadata) {
			s.updateJob(id, func(item *scrapeJob) {
				item.Number = meta.Number
				item.Actors = append([]string{}, meta.Actors...)
				item.Metadata = &meta
			})
		}
		if job.Reorganize {
			traceScrape(ctx, "重新整理", "读取已有元数据，通过刮削器重新下载图片并应用当前水印配置")
			provenance, sources := maps.Clone(job.Provenance), maps.Clone(job.SourceResults)
			if provenance == nil {
				provenance = map[string]string{}
			}
			if sources == nil {
				sources = map[string]provider.Metadata{}
			}
			var savedMeta provider.Metadata
			if job.Metadata != nil {
				savedMeta = *job.Metadata
			}
			output, err = reorganizeExisting(ctx, job.Path, savedMeta, *settings, observe, func(meta provider.Metadata, output string) (map[string][]byte, error) {
				base := strings.TrimSuffix(output, filepath.Ext(output))
				writes, warningsNew, err := s.prepareScrapeImages(ctx, job.Path, base, &meta, provenance, sources, *settings, true)
				warnings = append(warnings, warningsNew...)
				if err == nil {
					nfoTags, marshalErr := addMetadataWritesWithTrace(writes, meta, base, *settings)
					if marshalErr != nil {
						return nil, marshalErr
					}
					traceNFOTags(ctx, nfoTags)
					observe(meta)
					if trace, ok := ctx.Value(scrapeTraceKey{}).(scrapeTrace); ok {
						trace.metadata(provider.MergeResult{Metadata: meta, Provenance: provenance}, sources)
					}
				}
				return writes, err
			})
		} else {
			output, warnings, err = s.scrapeInPlace(ctx, job.Path, job.Number, *settings, job.RetryExisting, observe)
		}
	}
	stopped := errors.Is(ctx.Err(), context.Canceled) && err != nil
	cancel()
	completed := time.Now()
	if err != nil {
		traceScrape(ctx, "处理结果", err.Error())
	} else {
		traceScrape(ctx, "处理结果", "整理完成："+output)
	}
	s.updateJob(id, func(item *scrapeJob) {
		item.Cancel = nil
		item.Warnings = warnings
		item.CompletedAt = completed.Format(time.RFC3339Nano)
		item.DurationMS = completed.Sub(started).Milliseconds()
		if stopped {
			item.Status = -2
			item.Error = "已终止"
		} else if err != nil {
			item.Status = -1
			item.Error = err.Error()
		} else {
			item.Status = 2
			item.Output = output
			item.Directory = filepath.Dir(output)
			if item.Metadata != nil && settings != nil {
				// 记录的演员与实际写入 NFO 的一致：存映射后的名字，列表直接显示，不再随设置变化。
				item.Actors = applyActorMapping(item.Metadata.Actors, *settings)
			}
			if nfoTagsTraced {
				item.NFOTags = pendingNFOTags
			}
		}
	})
	if err == nil && !stopped && job.Reorganize && job.ReorgFrom != "" && settings != nil {
		cleanupReorganizedOutput(job.ReorgFrom, output, *settings, job.LinkMode)
	}
	s.publishJobFinished(id)
	if err != nil {
		slog.Warn("[scrape] 刮削失败", "id", id, "path", job.Path, "error", err)
		return
	}
	slog.Info("[scrape] 整理完成", "id", id, "path", job.Path, "output", output, "warnings", len(warnings))
	s.requestEmbyRefresh()
}

func detectNumber(path string) string {
	name := strings.ToUpper(filepath.Base(path))
	if match := filenameFC2Number.FindStringSubmatch(name); len(match) == 2 {
		return "FC2-PPV-" + match[1]
	}
	match := filenameNumber.FindStringSubmatch(name)
	if len(match) != 3 {
		return ""
	}
	return match[1] + "-" + match[2]
}

func containsKeyword(name string, words []string) bool {
	for _, word := range words {
		if word != "" && strings.Contains(strings.ToLower(name), strings.ToLower(word)) {
			return true
		}
	}
	return false
}

func detectSubtitle(path string) bool {
	return detectFilenameFlags(path, scrapeSettings{}).HasSubtitle
}

func stringSetting(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func boolSetting(values map[string]any, key string) bool {
	value, _ := values[key].(bool)
	return value
}

func intSetting(values map[string]any, key string) int {
	value, _ := values[key].(float64)
	return int(value)
}

func resolutionNamingSuffix(resolution string, settings scrapeSettings) string {
	if resolution == "" {
		return ""
	}
	key, text := "", resolution
	if resolution == "8K" {
		key = "resolution_uhd8"
		if intSetting(settings.Naming.Suffixes, "resolution_type") == 1 {
			text = "4320P"
		}
	} else if resolution == "4K" {
		key = "resolution_uhd"
		if intSetting(settings.Naming.Suffixes, "resolution_type") == 1 {
			text = "2160P"
		}
	}
	if key == "" || !boolSetting(settings.Naming.Suffixes, key) {
		return ""
	}
	return strings.ReplaceAll(stringSetting(settings.Naming.Suffixes, "resolution_naming"), "{resolution_text}", text)
}

func reservedWindowsName(stem string) bool {
	name := strings.ToUpper(strings.SplitN(stem, ".", 2)[0])
	if name == "CON" || name == "PRN" || name == "AUX" || name == "NUL" {
		return true
	}
	if len(name) == 4 && (strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT")) && name[3] >= '1' && name[3] <= '9' {
		return true
	}
	return false
}

func buildVideoStem(path string, meta provider.Metadata, settings scrapeSettings) (string, error) {
	meta = mapMetadataActors(meta, settings)
	mosaic := detectFilenameFlags(path, settings).Mosaic
	limit := settings.Naming.MaxActor
	if limit <= 0 {
		limit = 3
	}
	actor := strings.Join(meta.Actors, "、")
	if len(meta.Actors) > limit {
		actor = "多人作品"
	}
	vars := map[string]string{"number": meta.Number, "title": meta.Title, "actor": actor, "first_actor": "", "studio": meta.Studio, "publisher": meta.Publisher, "series": meta.Series, "year": ""}
	if len(meta.Actors) > 0 {
		vars["first_actor"] = meta.Actors[0]
	}
	if len(meta.Release) >= 4 {
		vars["year"] = meta.Release[:4]
	}
	parts := strings.Split(meta.Number, "-")
	vars["series_name"] = parts[0]
	vars["serial_number"] = parts[len(parts)-1]
	vars["publish_number"] = meta.Number
	apply := func(template string) string {
		return namingVariable.ReplaceAllStringFunc(template, func(token string) string { return vars[token[1:len(token)-1]] })
	}
	body := apply(settings.Naming.File)
	if body == "" {
		return "", errors.New("视频命名规则生成了空文件名")
	}
	suffix := settings.Naming.Suffix
	suffix = strings.ReplaceAll(suffix, "{mosaic}", stringSetting(settings.Naming.Suffixes, mosaic))
	subtitle := ""
	if detectFilenameFlags(path, settings).HasSubtitle {
		subtitle = stringSetting(settings.Naming.Suffixes, "has_subtitle")
	}
	suffix = strings.ReplaceAll(suffix, "{subtitle}", subtitle)
	suffix = strings.ReplaceAll(suffix, "{resolution}", resolutionNamingSuffix(detectFilenameFlags(path, settings).Resolution, settings))
	stem := strings.TrimSpace(body + suffix)
	if stem == "" || stem == "." || stem == ".." || badFilename.MatchString(stem) || strings.HasSuffix(stem, ".") || strings.HasSuffix(stem, " ") || reservedWindowsName(stem) {
		return "", fmt.Errorf("命名结果含非法文件名字符：%q", stem)
	}
	return stem, nil
}

type nfoMovie struct {
	XMLName       xml.Name   `xml:"movie"`
	Title         string     `xml:"title,omitempty"`
	OriginalTitle string     `xml:"originaltitle,omitempty"`
	SortTitle     string     `xml:"sorttitle,omitempty"`
	Tagline       string     `xml:"tagline,omitempty"`
	Country       string     `xml:"country,omitempty"`
	CountryCode   string     `xml:"countrycode,omitempty"`
	PublishNumber string     `xml:"publishnumber,omitempty"`
	CustomRating  string     `xml:"customrating,omitempty"`
	MPAA          string     `xml:"mpaa,omitempty"`
	Sets          []string   `xml:"set,omitempty"`
	Series        string     `xml:"series,omitempty"`
	Studio        string     `xml:"studio,omitempty"`
	Maker         string     `xml:"maker,omitempty"`
	Outline       string     `xml:"outline,omitempty"`
	Plot          string     `xml:"plot,omitempty"`
	OriginalPlot  string     `xml:"originalplot,omitempty"`
	Runtime       string     `xml:"runtime,omitempty"`
	Year          string     `xml:"year,omitempty"`
	Votes         string     `xml:"votes,omitempty"`
	Poster        string     `xml:"poster,omitempty"`
	Thumb         string     `xml:"thumb,omitempty"`
	Fanart        string     `xml:"fanart,omitempty"`
	Trailer       string     `xml:"trailer,omitempty"`
	Director      string     `xml:"director,omitempty"`
	Actors        []nfoActor `xml:"actor,omitempty"`
	Publisher     string     `xml:"publisher,omitempty"`
	Label         string     `xml:"label,omitempty"`
	Tags          []string   `xml:"tag,omitempty"`
	Genres        []string   `xml:"genre,omitempty"`
	Num           string     `xml:"num,omitempty"`
	Premiered     string     `xml:"premiered,omitempty"`
	ReleaseDate   string     `xml:"releasedate,omitempty"`
	Release       string     `xml:"release,omitempty"`
	Rating        string     `xml:"rating,omitempty"`
	CriticRating  string     `xml:"criticrating,omitempty"`
	Website       string     `xml:"website,omitempty"`
}
type nfoActor struct {
	Name string `xml:"name"`
}

var nfoPart = regexp.MustCompile(`(?i)(?:^|[-_. ])(?:cd|disc|part|parte|碟|盘)\s*0*([0-9]+)(?:$|[-_. ])`)

func nfoAsset(base, kind string, writes map[string][]byte, settings scrapeSettings) string {
	extensions := []string{".jpg", ".png", ".webp"}
	if kind == "trailer" {
		extensions = []string{".mp4", ".webm"}
	}
	for _, extension := range extensions {
		paths := []string{
			scrapeAssetPath(base, kind, extension, settings.Naming.ImageWithPrefix),
			scrapeAssetPath(base, kind, extension, !settings.Naming.ImageWithPrefix),
		}
		for _, path := range paths {
			if _, ok := writes[path]; ok {
				return filepath.Base(path)
			}
			if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
				return filepath.Base(path)
			}
		}
	}
	return ""
}

func nfoExpand(template string, values map[string]string) string {
	if template == "" {
		return ""
	}
	return namingVariable.ReplaceAllStringFunc(template, func(token string) string {
		return values[token[1:len(token)-1]]
	})
}

func nfoUnique(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value != "" && !seen[key] {
			seen[key] = true
			out = append(out, value)
		}
	}
	return out
}

func nfoValues(meta provider.Metadata) map[string]string {
	actors := append([]string{}, meta.Actors...)
	actors = append(actors, meta.MaleActors...)
	return map[string]string{
		"number": meta.Number, "title": meta.Title, "actor": strings.Join(actors, "、"),
		"first_actor": func() string {
			if len(actors) > 0 {
				return actors[0]
			}
			return ""
		}(),
		"studio": meta.Studio, "publisher": meta.Publisher, "series": meta.Series,
		"release": meta.Release, "year": strings.SplitN(meta.Release, "-", 2)[0], "runtime": meta.Runtime,
	}
}

func buildMediaTitle(meta provider.Metadata, settings scrapeSettings) string {
	meta = mapMetadataActors(meta, settings)
	template := settings.Naming.Title
	if strings.TrimSpace(template) == "" {
		return meta.Title
	}
	limit := settings.Naming.MaxActor
	if limit <= 0 {
		limit = 3
	}
	actor := strings.Join(meta.Actors, "、")
	if len(meta.Actors) > limit {
		actor = "多人作品"
	}
	firstActor := ""
	if len(meta.Actors) > 0 {
		firstActor = meta.Actors[0]
	}
	parts := strings.Split(meta.Number, "-")
	serialName, serialNumber := "", ""
	if len(parts) > 0 {
		serialName = parts[0]
		serialNumber = parts[len(parts)-1]
	}
	year := ""
	if len(meta.Release) >= 4 {
		year = meta.Release[:4]
	}
	publishNumber := meta.PublishNumber
	if publishNumber == "" {
		publishNumber = meta.Number
	}
	values := map[string]string{
		"number": meta.Number, "publish_number": publishNumber, "title": meta.Title,
		"actor": actor, "first_actor": firstActor, "studio": meta.Studio, "publisher": meta.Publisher,
		"series": meta.Series, "series_name": serialName, "serial_number": serialNumber, "year": year,
	}
	title := namingVariable.ReplaceAllStringFunc(template, func(token string) string {
		return values[token[1:len(token)-1]]
	})
	if strings.TrimSpace(title) == "" {
		return meta.Title
	}
	return title
}

func nfoAdditionalTags(meta provider.Metadata, flags filenameFlags, settings scrapeSettings) []string {
	trace := nfoTagsWithOrigin(meta, flags, settings)
	tags := make([]string, 0, len(trace.Tags))
	for _, entry := range trace.Tags {
		tags = append(tags, entry.Tag)
	}
	return tags
}

// nfoResolutionTag 返回可写入 NFO 的分辨率标签：只保留 4K/8K。
// 1080P/720P 这类常见分辨率不写入，避免 NFO 里出现无意义的标签。
func nfoResolutionTag(resolution string) string {
	switch strings.ToUpper(strings.TrimSpace(resolution)) {
	case "4K":
		return "4K"
	case "8K":
		return "8K"
	case "2160P":
		return "4K"
	case "4320P":
		return "8K"
	}
	return ""
}

// nfoTagsWithOrigin 生成 NFO 标签，并记录每个标签的来源以及被丢弃的刮削标签。
// 结果与写入 NFO 的标签顺序完全一致；重复标签保留第一次出现时的来源。
func nfoTagsWithOrigin(meta provider.Metadata, flags filenameFlags, settings scrapeSettings) nfoTagTrace {
	config := settings.NFO.AdditionalTags
	values := nfoValues(meta)
	trace := nfoTagTrace{Tags: []nfoTagEntry{}}
	candidates := make([]nfoTagEntry, 0, len(meta.Tags)+8)
	add := func(origin string, tags ...string) {
		for _, tag := range tags {
			candidates = append(candidates, nfoTagEntry{Tag: tag, Origin: origin})
		}
	}
	// Only normalize source resolution tags when the NFO resolution-tag option
	// is enabled. When disabled, preserve provider tags exactly as returned.
	for _, tag := range meta.Tags {
		if !config.Resolution {
			add(nfoTagScraped, tag)
			continue
		}
		normalized := strings.ToLower(strings.TrimSpace(tag))
		switch normalized {
		case "4k", "8k", "2160p", "4320p", "1080p", "720p", "uhd", "fhd", "hd":
			trace.Dropped = append(trace.Dropped, nfoTagEntry{Tag: tag, Origin: nfoTagResolution})
		default:
			add(nfoTagScraped, tag)
		}
	}
	serial := strings.SplitN(meta.Number, "-", 2)[0]
	if config.SerialName {
		add("serial_name", serial)
	}
	actors := append([]string{}, meta.Actors...)
	if settings.NFO.IncludeMaleActors {
		actors = append(actors, meta.MaleActors...)
	}
	if config.Actors {
		add("actor", actors...)
	}
	if config.Resolution {
		// 只写 4K/8K；1080P/720P 等常见分辨率不写入 NFO。
		if resolution := nfoResolutionTag(flags.Resolution); resolution != "" {
			add(nfoTagResolution, resolution)
		}
	}
	if config.Mosaic {
		switch flags.Mosaic {
		case "is_uncensored":
			add("mosaic", "无码")
		case "is_ltx_hack", "is_lada_hack", "is_jav_hack", "is_other_hack":
			// 破解片已无马赛克，只由破解类型标签表示，不再写「有码」。
		default:
			add("mosaic", "有码")
		}
	}
	if config.HackType {
		switch flags.Mosaic {
		case "is_ltx_hack":
			add("hack_type", "LTX破解")
		case "is_lada_hack":
			add("hack_type", "LADA破解")
		case "is_jav_hack":
			add("hack_type", "JAV破解")
		case "is_other_hack":
			add("hack_type", "破解")
		}
	}
	if config.HasSubtitle && flags.HasSubtitle {
		add("subtitle", nfoExpand(config.SubtitleFormat, values))
	}
	if config.Series {
		add("series", nfoExpand(config.SeriesFormat, values))
	}
	if config.Studio {
		add("studio", nfoExpand(config.StudioFormat, values))
	}
	if config.Publisher {
		add("publisher", nfoExpand(config.PublisherFormat, values))
	}
	// 与 nfoUnique 相同的去重规则：去掉首尾空白，忽略大小写，保留首次出现。
	seen := map[string]bool{}
	for _, entry := range candidates {
		entry.Tag = strings.TrimSpace(entry.Tag)
		key := strings.ToLower(entry.Tag)
		if entry.Tag != "" && !seen[key] {
			seen[key] = true
			trace.Tags = append(trace.Tags, entry)
		}
	}
	return trace
}

func buildNFO(meta provider.Metadata, base string, settings scrapeSettings, writes map[string][]byte) ([]byte, error) {
	raw, _, err := buildNFOWithTrace(meta, base, settings, writes)
	return raw, err
}

// buildNFOWithTrace 生成 NFO，同时返回生成时记录的标签来源。
func buildNFOWithTrace(meta provider.Metadata, base string, settings scrapeSettings, writes map[string][]byte) ([]byte, nfoTagTrace, error) {
	// NFO 里的演员必须是映射后的名字；映射表里没有的保持原样。
	meta = mapMetadataActors(meta, settings)
	config := settings.NFO
	values := nfoValues(meta)
	title := buildMediaTitle(meta, settings)
	if config.AddPartToTitle {
		if match := nfoPart.FindStringSubmatch(filepath.Base(base)); len(match) == 2 {
			title += " CD" + match[1]
		}
	}
	movie := nfoMovie{Title: title, Num: meta.Number, PublishNumber: meta.PublishNumber}
	if config.EnableSortTitle {
		movie.SortTitle = meta.SortTitle
		if movie.SortTitle == "" {
			movie.SortTitle = title
		}
	}
	if config.EnableOriginalTitle {
		movie.OriginalTitle = meta.OriginalTitle
	}
	if config.EnableOutline {
		movie.Outline = meta.Outline
	}
	if config.EnablePlot {
		movie.Plot = meta.Outline
		if config.AddTranslatedBy && meta.TranslatedBy != "" {
			movie.Plot += "\n由 " + meta.TranslatedBy + " 提供翻译"
		}
	}
	if config.EnableOriginalPlot {
		movie.OriginalPlot = meta.OriginalPlot
		if movie.OriginalPlot == "" {
			movie.OriginalPlot = meta.OriginalOutline
		}
	}
	if tagline := nfoExpand(config.TaglineFormat, values); tagline != "" {
		movie.Tagline = tagline
	}
	// 所选国家决定分级：日本 JP-18+；国产、欧美 XXX。未选择时沿用数据源的分级。
	countryRating := ""
	if config.EnableCountry {
		movie.Country = meta.Country
		// 只接受已知的国家代码，避免手改配置文件后写入任意内容。
		switch config.CountryCode {
		case "JP":
			movie.CountryCode, countryRating = "JP", "JP-18+"
		case "CN", "US":
			movie.CountryCode, countryRating = config.CountryCode, "XXX"
		}
	}
	if config.EnableMPAA {
		movie.MPAA = meta.MPAA
		if countryRating != "" {
			movie.MPAA = countryRating
		}
	}
	if config.EnableCustomRating {
		movie.CustomRating = meta.CustomRating
		if countryRating != "" {
			movie.CustomRating = countryRating
		}
	}
	if config.EnableYear && len(meta.Release) >= 4 {
		movie.Year = meta.Release[:4]
	}
	if config.EnableRuntime {
		movie.Runtime = meta.Runtime
	}
	if config.EnableVotes {
		movie.Votes = meta.Votes
	}
	if config.EnableScore {
		movie.Rating = meta.Rating
	}
	if config.EnableCriticRating {
		movie.CriticRating = meta.CriticRating
	}
	if config.EnableActor {
		actors := append([]string{}, meta.Actors...)
		if config.IncludeMaleActors {
			actors = append(actors, meta.MaleActors...)
		}
		for _, name := range nfoUnique(actors) {
			movie.Actors = append(movie.Actors, nfoActor{Name: name})
		}
	}
	if config.EnableDirector {
		movie.Director = meta.Director
	}
	if config.EnableSeries {
		movie.Series = meta.Series
	}
	flags := detectFilenameFlags(base, settings)
	if meta.Mosaic != "" {
		flags.Mosaic = meta.Mosaic
	}
	if meta.HasSubtitle {
		flags.HasSubtitle = true
	}
	if meta.Resolution != "" {
		flags.Resolution = meta.Resolution
	}
	tagTrace := nfoTagsWithOrigin(meta, flags, settings)
	tags := make([]string, 0, len(tagTrace.Tags))
	for _, entry := range tagTrace.Tags {
		tags = append(tags, entry.Tag)
	}
	if config.EnableTag {
		movie.Tags = append([]string{}, tags...)
	}
	if config.EnableGenre {
		movie.Genres = append([]string{}, tags...)
	}
	if config.EnableStudio {
		movie.Studio = meta.Studio
	}
	if config.EnableMaker {
		movie.Maker = meta.Studio
	}
	if config.EnablePublisher {
		movie.Publisher = meta.Publisher
	}
	if config.EnableLabel {
		movie.Label = meta.Publisher
	}
	posterPath := ""
	if config.EnablePoster {
		posterPath = nfoAsset(base, "poster", writes, settings)
		movie.Poster = posterPath
	}
	thumbPath, fanartPath := "", ""
	if config.EnableCover {
		thumbPath = nfoAsset(base, "thumb", writes, settings)
		fanartPath = nfoAsset(base, "fanart", writes, settings)
	}
	movie.Thumb = thumbPath
	movie.Fanart = fanartPath
	if config.EnableTrailer {
		movie.Trailer = nfoAsset(base, "trailer", writes, settings)
		if movie.Trailer == "" {
			movie.Trailer = meta.Trailer
		}
	}
	if config.EnableWebsite {
		movie.Website = meta.SourceURL
	}
	if config.EnableRelease {
		movie.Release = meta.Release
	}
	if config.EnableReleaseDate {
		movie.ReleaseDate = meta.Release
	}
	if config.EnablePremiered {
		movie.Premiered = meta.Release
	}
	var sets []string
	if config.SetByActress {
		sets = append(sets, meta.Actors...)
	}
	if config.SetBySeries {
		sets = append(sets, meta.Series)
	}
	if config.SetBySerialName {
		sets = append(sets, strings.SplitN(meta.Number, "-", 2)[0])
	}
	movie.Sets = nfoUnique(sets)
	raw, err := xml.MarshalIndent(movie, "", "  ")
	if err != nil {
		return nil, tagTrace, err
	}
	return append([]byte(xml.Header), raw...), tagTrace, nil
}

func addMetadataWrites(writes map[string][]byte, meta provider.Metadata, base string, settings scrapeSettings) error {
	_, err := addMetadataWritesWithTrace(writes, meta, base, settings)
	return err
}

// addMetadataWritesWithTrace 与 addMetadataWrites 相同，另外返回本次写入 NFO 的标签来源；
// NFO 关闭时返回 nil。
func addMetadataWritesWithTrace(writes map[string][]byte, meta provider.Metadata, base string, settings scrapeSettings) (*nfoTagTrace, error) {
	if settings.NFO.Enabled {
		raw, trace, err := buildNFOWithTrace(meta, base, settings, writes)
		if err != nil {
			return nil, err
		}
		writes[base+".nfo"] = raw
		return &trace, nil
	}
	delete(writes, base+".nfo")
	return nil, nil
}

// traceNFOTags 把本次生成 NFO 的标签来源交给当前任务暂存，任务成功后才保存到记录。
func traceNFOTags(ctx context.Context, trace *nfoTagTrace) {
	if recorder, ok := ctx.Value(scrapeTraceKey{}).(scrapeTrace); ok && recorder.nfoTags != nil {
		recorder.nfoTags(trace)
	}
}

func (s *Server) organizeInPlace(ctx context.Context, path, override string) (string, []string, error) {
	settings, err := s.scrapeSettings()
	if err != nil {
		return "", nil, err
	}
	return s.organizeInPlaceWithSettings(ctx, path, override, settings)
}

func (s *Server) organizeInPlaceWithSettings(ctx context.Context, path, override string, settings scrapeSettings, observers ...func(provider.Metadata)) (string, []string, error) {
	return s.scrapeInPlace(ctx, path, override, settings, false, observers...)
}

func (s *Server) scrapeInPlace(ctx context.Context, path, override string, settings scrapeSettings, retryExisting bool, observers ...func(provider.Metadata)) (string, []string, error) {
	if !supportedLinkMode(settings.Common.LinkMode) {
		return "", nil, errors.New("当前只支持移动、原地整理和 STRM 模式")
	}
	if !eligibleFile(path, settings) {
		return "", nil, errors.New("文件已不存在或不符合过滤规则")
	}
	number := override
	if number == "" {
		number = detectNumber(path)
	}
	if number == "" {
		return "", nil, errors.New("无法从文件名识别番号，请单独提交并填写番号")
	}
	traceScrape(ctx, "解析番号", "番号识别结果："+number)
	var priorities provider.Priorities
	if settings.Priorities != nil {
		priorities = provider.Priorities{ByFields: maps.Clone(settings.Priorities.ByFields)}
	} else {
		current, err := s.currentPriorities()
		if err != nil {
			return "", nil, err
		}
		priorities = current
	}
	if !settings.Download.Trailer {
		priorities.ByFields["trailer"] = nil
	}
	if settings.OverrideProvider != "" {
		for _, field := range provider.PriorityFields {
			if field == "trailer" && !settings.Download.Trailer {
				continue
			}
			priorities.ByFields[field] = append([]string{settings.OverrideProvider}, priorities.ByFields[field]...)
		}
	}
	configs := make(map[string]provider.Config)
	if settings.ProviderConfigs != nil {
		configs = maps.Clone(settings.ProviderConfigs)
	} else {
		s.mu.RLock()
		for id, config := range s.providerConfigs {
			configs[id] = config
		}
		s.mu.RUnlock()
	}
	fetched := map[string]provider.Metadata{}
	var fetchedMu sync.Mutex
	hooks := provider.MergeHooks{
		Start: func(ids []string) {
			traceScrape(ctx, "刮削数据", "开始刮削元数据")
			traceScrape(ctx, "刮削数据", fmt.Sprintf("开始从以下[%d]个站点并行抓取数据: %s", len(ids), strings.Join(ids, ", ")))
		},
		Collected: func() {
			traceScrape(ctx, "刮削数据", "数据源采集完成，开始合并及处理元数据")
		},
	}
	// 记录每个数据源的失败原因，写入任务后详细数据页会显示「xx 失败」。
	failures := map[string]string{}
	recordFailure := func(id string, reason error) {
		fetchedMu.Lock()
		failures[id] = reason.Error()
		fetchedMu.Unlock()
	}
	result, err := provider.MergeWithHooks(ctx, number, priorities, func(ctx context.Context, id string) (provider.Metadata, error) {
		started := time.Now()
		config := configs[id]
		if !config.Enabled {
			traceScrape(ctx, "刮削数据", fmt.Sprintf("抓取失败 <%s>：数据源未启用", id))
			recordFailure(id, errors.New("数据源未启用"))
			return provider.Metadata{}, errors.New("数据源未启用")
		}
		source, err := s.providers.Get(id)
		if err != nil {
			traceScrape(ctx, "刮削数据", fmt.Sprintf("抓取失败 <%s>：%s", id, err.Error()))
			recordFailure(id, err)
			return provider.Metadata{}, err
		}
		input := number
		if id == settings.OverrideProvider && settings.OverrideURL != "" {
			input = settings.OverrideURL
		}
		meta, err := source.Fetch(ctx, config, input)
		// 按数据源的「失败重复次数」重新请求；任务已取消或超时后不再重试。
		for attempt := 1; err != nil && attempt <= config.RetryTimes && ctx.Err() == nil; attempt++ {
			traceScrape(ctx, "刮削数据", fmt.Sprintf("抓取失败，第 %d 次重复请求 <%s>：%s", attempt, id, err.Error()))
			meta, err = source.Fetch(ctx, config, input)
		}
		elapsed := time.Since(started).Seconds()
		if err != nil {
			traceScrape(ctx, "刮削数据", fmt.Sprintf("抓取失败, 用时 %.3fs <%s>：%s", elapsed, id, err.Error()))
			recordFailure(id, err)
			return meta, err
		}
		fetchedMu.Lock()
		fetched[id] = meta
		fetchedMu.Unlock()
		traceScrape(ctx, "刮削数据", fmt.Sprintf("成功抓取到数据, 用时 %.3fs <%s>", elapsed, id))
		return meta, nil
	}, hooks)
	fetchedMu.Lock()
	failed := make(map[string]string, len(failures))
	for id, reason := range failures {
		failed[id] = reason
	}
	fetchedMu.Unlock()
	if trace, ok := ctx.Value(scrapeTraceKey{}).(scrapeTrace); ok {
		if trace.sourceErrors != nil {
			trace.sourceErrors(failed)
		}
		trace.metadata(result, fetched)
	}
	if err != nil {
		return "", nil, err
	}
	meta := result.Metadata
	flags := detectFilenameFlags(path, settings)
	meta.HasSubtitle, meta.Mosaic, meta.Resolution = flags.HasSubtitle, flags.Mosaic, flags.Resolution
	traceScrape(ctx, "刮削数据", "元数据已获取")
	for _, observe := range observers {
		observe(meta)
	}
	if meta.Title == "" {
		return "", nil, errors.New("没有获取到标题；为避免错误重命名，未整理该视频")
	}
	newPath, err := organizationTarget(path, meta, settings)
	if err != nil {
		return "", nil, err
	}
	traceScrape(ctx, "计算文件名", "目标文件："+newPath)
	if settings.Common.LinkMode == 3 && !retryExisting && pathKey(newPath) != pathKey(path) {
		if _, err := os.Lstat(newPath); err == nil {
			return "", nil, errors.New("目标视频文件已存在：" + newPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", nil, err
		}
	}
	base := strings.TrimSuffix(newPath, filepath.Ext(newPath))
	writes := map[string][]byte{}
	// 数据源的失败原因已经在详细数据中显示（source_errors），这里不再重复记为警告。
	warnings := []string{}
	images, imageWarnings, err := s.prepareScrapeImages(ctx, path, base, &meta, result.Provenance, fetched, settings, false)
	warnings = append(warnings, imageWarnings...)
	if trace, ok := ctx.Value(scrapeTraceKey{}).(scrapeTrace); ok {
		result.Metadata = meta
		trace.metadata(result, fetched)
	}
	if err != nil {
		return "", warnings, err
	}
	for target, content := range images {
		writes[target] = content
	}
	nfoTags, err := addMetadataWritesWithTrace(writes, meta, base, settings)
	if err != nil {
		return "", warnings, err
	}
	traceNFOTags(ctx, nfoTags)
	for _, observe := range observers {
		observe(meta)
	}
	if settings.Common.LinkMode == linkModeMove {
		if err := commitMove(ctx, path, newPath, writes, settings); err != nil {
			return "", warnings, err
		}
		return newPath, warnings, nil
	}
	if settings.Common.LinkMode == linkModeSTRM {
		if err := commitStrm(ctx, path, newPath, writes, settings); err != nil {
			return "", warnings, err
		}
		return newPath, warnings, nil
	}
	if retryExisting {
		if err := commitScrapeRetryManagedNFO(ctx, path, newPath, writes, true, settings.Naming.ImageWithPrefix); err != nil {
			return "", warnings, err
		}
		return newPath, warnings, nil
	}
	for target := range writes {
		if _, err := os.Lstat(target); err == nil {
			return "", warnings, errors.New("目标元数据文件已存在：" + target)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", warnings, err
		}
	}
	created := []string{}
	for target, content := range writes {
		if err := ctx.Err(); err != nil {
			for _, item := range created {
				_ = os.Remove(item)
			}
			return "", warnings, err
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			for _, item := range created {
				_ = os.Remove(item)
			}
			return "", warnings, err
		}
		created = append(created, target)
		_, err = file.Write(content)
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			for _, item := range created {
				_ = os.Remove(item)
			}
			return "", warnings, err
		}
		traceScrape(ctx, "保存文件", "已写入："+target)
	}
	if err := ctx.Err(); err != nil {
		for _, item := range created {
			_ = os.Remove(item)
		}
		return "", warnings, err
	}
	if newPath != path {
		if err := os.Rename(path, newPath); err != nil {
			for _, item := range created {
				_ = os.Remove(item)
			}
			return "", warnings, fmt.Errorf("重命名视频失败：%w", err)
		}
		traceScrape(ctx, "整理视频", path+" → "+newPath)
	}
	removeLegacyMetadataFiles(path, newPath)
	return newPath, warnings, nil
}
