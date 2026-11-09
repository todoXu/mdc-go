package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mdcng-go/internal/provider"
)

func (s *Server) deleteScrapeJob(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DeleteMedia bool `json:"delete_media"`
	}
	if r.ContentLength != 0 {
		if !decode(w, r, &request) {
			return
		}
	}
	deletedDirectory, failure := s.deleteJob(r.PathValue("id"), request.DeleteMedia)
	if failure != nil {
		fail(w, failure.status, failure.code, failure.message)
		return
	}
	s.saveScrapeHistory()
	writeJSON(w, 200, object{"success": true, "deleted_media": request.DeleteMedia, "directory": deletedDirectory})
}

// deleteJob 删除一条记录；deleteMedia 为 true 时先删除它的媒体文件。不保存历史，由调用方保存。
// 返回被删除的空文件夹（没有删除文件夹时为空）。
func (s *Server) deleteJob(id string, deleteMedia bool) (string, *actionError) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	for i, job := range s.jobs {
		if job.ID != id {
			continue
		}
		if job.Status == 0 || job.Status == 1 || job.Editing {
			return "", &actionError{409, "job_active", "请先终止正在等待或处理的记录"}
		}
		deletedDirectory := ""
		if deleteMedia {
			files, directory, err := s.deletableMediaFiles(job)
			if err != nil {
				return "", &actionError{400, "unsafe_media_directory", err.Error()}
			}
			for _, file := range files {
				if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
					return "", &actionError{500, "delete_media_failed", "删除本地媒体文件失败：" + err.Error()}
				}
			}
			// 文件夹里没有其它文件了才删除文件夹，不会连带删除无关内容。
			// 监控目录和整理目录本身即使变空也不删除。
			if entries, err := os.ReadDir(directory); err == nil && len(entries) == 0 && !s.isConfiguredFolder(job, directory) {
				if err := os.Remove(directory); err == nil {
					deletedDirectory = directory
				}
			}
		}
		s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
		// 该监控目录的记录全部删除后，它的序号从 1 重新开始；任务 ID 带时间戳，不会重复。
		s.resetJobSequenceIfEmpty(job.DirectoryID)
		s.publishJobChanged(job.ID, true)
		return deletedDirectory, nil
	}
	return "", &actionError{404, "not_found", "记录不存在"}
}

func strictSubdirectory(root, target string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// isConfiguredFolder 判断目录是否是某个监控目录或这条记录的整理目录本身。
func (s *Server) isConfiguredFolder(job scrapeJob, directory string) bool {
	for _, root := range s.currentWatchConfig().Directories {
		if samePath(root.Path, directory) {
			return true
		}
	}
	return job.Settings != nil && job.Settings.Common.TargetFolder != "" && samePath(job.Settings.Common.TargetFolder, directory)
}

// deletableMediaFiles 返回删除记录媒体数据时要移除的文件，以及产物所在的文件夹（删除后为空才删除）。
//
// 产物路径：成功的记录用实际输出路径；失败的记录按元数据推算整理目标（与整理时使用同一套命名规则），
// 然后逐个检查是否真的存在，只删除实际存在的文件。
//   - 源视频：STRM 模式的源视频在监控目录里；失败的记录源视频仍在原位。这两种情况都会删除。
//   - 产物：NFO、.mdc.json、海报、缩略图、背景图、预告片。
//
// 没有视频的文件夹里，不带记录名前缀的图片可能属于其它记录，这种情况下不删除它们。
func (s *Server) deletableMediaFiles(job scrapeJob) ([]string, string, error) {
	settings := job.Settings
	if settings == nil {
		current, err := s.recordSettings(job)
		if err != nil {
			return nil, "", err
		}
		settings = &current
	}
	mode := job.LinkMode
	if mode == 0 {
		mode = settings.Common.LinkMode
	}
	if mode != linkModeMove && mode != linkModeInPlace && mode != linkModeSTRM {
		return nil, "", errors.New("当前整理模式不支持删除媒体数据")
	}

	// 路径必须位于整理目录或某个监控目录内（包括目录本身是监控根目录的情况）。
	withinRoot := func(path string) bool {
		if usesTargetFolder(mode) && filepath.IsAbs(settings.Common.TargetFolder) && (samePath(settings.Common.TargetFolder, path) || strictSubdirectory(settings.Common.TargetFolder, path)) {
			return true
		}
		for _, root := range s.currentWatchConfig().Directories {
			if samePath(root.Path, path) || strictSubdirectory(root.Path, path) {
				return true
			}
		}
		return false
	}

	success := job.Status == 2 && job.Output != ""
	output := ""
	if success {
		output = filepath.Clean(job.Output)
	} else if job.Metadata != nil && job.Path != "" {
		// 未成功的记录没有输出路径，按元数据推算整理后的位置。
		if target, err := plannedMediaTarget(job.Path, *job.Metadata, *settings); err == nil {
			output = filepath.Clean(target)
		}
	}

	files := []string{}
	// 源视频：STRM 的源在监控目录；失败的记录源视频仍在原位。成功的移动和原地记录没有单独的源文件。
	if job.Path != "" && (mode == linkModeSTRM || !success) {
		source := filepath.Clean(job.Path)
		if !samePath(source, output) {
			if info, err := os.Lstat(source); err == nil {
				if !info.Mode().IsRegular() {
					return nil, "", errors.New("源视频不是普通文件，已拒绝删除")
				}
				if !withinRoot(filepath.Dir(source)) {
					return nil, "", errors.New("源视频不在监控目录下，已拒绝删除")
				}
				files = append(files, source)
			}
		}
	}

	if output == "" {
		// 没有可删除的文件（例如源视频已被手动删除、记录也没有输出）：返回空列表，记录照常删除。
		if len(files) == 0 {
			return []string{}, "", nil
		}
		return files, "", nil
	}

	directory := filepath.Dir(output)
	dirInfo, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		// 产物所在文件夹不存在，只需要删除源视频。
		return files, "", nil
	}
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return nil, "", errors.New("产物所在文件夹不是普通目录，已拒绝删除")
	}
	if !withinRoot(directory) {
		return nil, "", errors.New("产物不在整理目录或监控目录下，已拒绝删除")
	}

	videos := map[string]bool{"strm": true}
	for _, extension := range settings.Common.Extensions {
		videos[strings.ToLower(strings.TrimPrefix(strings.TrimSpace(extension), "."))] = true
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, "", err
	}
	videoCount := 0
	for _, entry := range entries {
		if entry.Type().IsRegular() && videos[strings.TrimPrefix(strings.ToLower(filepath.Ext(entry.Name())), ".")] {
			videoCount++
		}
	}
	outputExists := fileExists(output)
	if outputExists {
		if videoCount != 1 {
			return nil, "", fmt.Errorf("产物所在文件夹内应只有一个视频文件，当前为 %d 个，已取消删除以免误删其它文件", videoCount)
		}
		files = append(files, output)
	}
	// 不带记录名前缀的图片只有在文件夹里没有其它视频时才可以确认属于本记录。
	unprefixedSafe := (outputExists && videoCount == 1) || (!outputExists && videoCount == 0)

	base := strings.TrimSuffix(output, filepath.Ext(output))
	for _, candidate := range []string{base + ".nfo", base + ".mdc.json"} {
		if fileExists(candidate) {
			files = append(files, candidate)
		}
	}
	for _, name := range oldAssetNames() {
		for _, candidate := range scrapeNamedAssetCandidateOrder(base, name, settings.Naming.ImageWithPrefix) {
			prefixed := strings.HasPrefix(filepath.Base(candidate), filepath.Base(base)+"-")
			if !prefixed && !unprefixedSafe {
				continue
			}
			if fileExists(candidate) {
				files = append(files, candidate)
			}
		}
	}
	return files, directory, nil
}

func (s *Server) scrapeJobAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if action != "retry" && action != "stop" && action != "reorganize" {
		fail(w, 404, "not_found", "未知操作")
		return
	}
	var request taskRequest
	if action != "stop" && r.ContentLength != 0 {
		if !decode(w, r, &request) {
			return
		}
		if request.Number != "" {
			number, err := provider.NormalizeNumber(request.Number)
			if err != nil {
				fail(w, 400, "invalid_number", "覆盖番号格式不正确")
				return
			}
			request.Number = number
		}
	}
	var settings scrapeSettings
	if action == "reorganize" || action == "retry" {
		var failure *actionError
		settings, failure = s.actionSettings(r.PathValue("id"), &request)
		if failure != nil {
			fail(w, failure.status, failure.code, failure.message)
			return
		}
	}
	s.jobsMu.Lock()
	for i := range s.jobs {
		job := &s.jobs[i]
		if job.ID != r.PathValue("id") {
			continue
		}
		if action != "stop" {
			failure := s.requeueJobLocked(job, action, request, r.ContentLength != 0, settings)
			s.jobsMu.Unlock()
			if failure != nil {
				fail(w, failure.status, failure.code, failure.message)
				return
			}
			s.saveScrapeHistory()
			s.startScrapeJobs([]string{r.PathValue("id")})
			writeJSON(w, 202, object{"success": true, "id": r.PathValue("id")})
			return
		}
		if job.Editing {
			s.jobsMu.Unlock()
			fail(w, 409, "job_active", "记录正在更新，请稍后重试")
			return
		}
		{
			if job.Status != 0 && job.Status != 1 {
				s.jobsMu.Unlock()
				fail(w, 409, "job_finished", "记录已结束")
				return
			}
			job.StopRequested = true
			if job.Status == 0 {
				job.Status = -2
				job.Error = "已终止"
				job.CompletedAt = time.Now().Format(time.RFC3339Nano)
			}
			if job.Cancel != nil {
				job.Cancel()
			}
			s.publishJobChanged(job.ID, true)
			s.jobsMu.Unlock()
			s.saveScrapeHistory()
			writeJSON(w, 202, object{"success": true})
			return
		}
	}
	s.jobsMu.Unlock()
	fail(w, 404, "not_found", "记录不存在")
}

// actionError 是重试、重新整理在校验阶段的失败，对应一个 HTTP 状态码和错误代码。
type actionError struct {
	status        int
	code, message string
}

// actionSettings 读取记录所属监控目录的整理配置，并套用请求里的标识、网址和番号覆盖。
// 按网址识别出的番号会写回 request.Number。
func (s *Server) actionSettings(id string, request *taskRequest) (scrapeSettings, *actionError) {
	settings, err := s.scrapeSettings()
	s.jobsMu.RLock()
	directoryID := ""
	for _, job := range s.jobs {
		if job.ID == id {
			directoryID = job.DirectoryID
			break
		}
	}
	s.jobsMu.RUnlock()
	for _, directory := range s.currentWatchConfig().Directories {
		if directory.ID == directoryID {
			settings, err = s.scrapeSettingsForDirectory(directory)
			break
		}
	}
	if err != nil {
		return settings, &actionError{500, "config_error", "无法读取整理配置"}
	}
	settings.FlagsOverride = request.Flags
	if request.URL != "" {
		if err := s.resolveTaskURL(&settings, request.URL); err != nil {
			return settings, &actionError{400, "invalid_url", err.Error()}
		}
		if request.Number == "" {
			request.Number = detectNumber(request.URL)
		}
	}
	settings.OverrideNumber = request.Number
	if !supportedLinkMode(settings.Common.LinkMode) {
		return settings, &actionError{409, "unsupported_mode", "当前只支持移动、原地整理和 STRM 模式"}
	}
	if usesTargetFolder(settings.Common.LinkMode) && (!filepath.IsAbs(settings.Common.TargetFolder) || strings.TrimSpace(settings.Common.TargetFolder) == "") {
		return settings, &actionError{409, "target_folder_required", "移动和 STRM 模式需要先设置完整的整理目录"}
	}
	return settings, nil
}

// requeueJobLocked 把已结束的记录重新放回等待队列（action 为 retry 或 reorganize）。
// 重试会先删除上次刮削的产物。调用前必须持有 jobsMu 写锁；不保存历史，也不启动任务，由调用方统一处理。
// overrideNumber 为 true 时用 request.Number 替换记录的番号（为空表示重新识别）。
func (s *Server) requeueJobLocked(job *scrapeJob, action string, request taskRequest, overrideNumber bool, settings scrapeSettings) *actionError {
	if job.Editing {
		return &actionError{409, "job_active", "记录正在更新，请稍后重试"}
	}
	if action == "retry" && job.Status != 2 && job.Status != -1 && job.Status != -2 {
		return &actionError{409, "invalid_status", "仅可重试已结束的记录"}
	}
	if action == "reorganize" && job.Status != 2 {
		return &actionError{409, "invalid_status", "仅可重新整理已成功的记录"}
	}
	if err := linkModeChangeError(*job, settings.Common.LinkMode); err != nil {
		return &actionError{409, "link_mode_changed", err.Error()}
	}
	path := job.Path
	// 移动模式的源视频已经移走，重试和重新整理要以输出文件为准。
	// STRM 模式的源视频仍在原位，必须继续使用源路径，否则会把 .strm 当成源文件。
	if job.LinkMode != linkModeSTRM && (action == "reorganize" || (action == "retry" && job.Output != "")) {
		path = job.Output
	}
	target := pathKey(path)
	for i := range s.jobs {
		other := &s.jobs[i]
		if (other.Status == 0 || other.Status == 1 || other.Editing) && pathKey(other.Path) == target {
			return &actionError{409, "job_active", "该文件已有等待或处理中的记录"}
		}
	}
	removed := []string{}
	if action == "retry" {
		var cleanupErr error
		removed, cleanupErr = removeRetryProducts(job, path, settings)
		if cleanupErr != nil {
			slog.Warn("[scrape] 重试前清理旧产物失败", "id", job.ID, "path", path, "error", cleanupErr)
			return &actionError{500, "cleanup_failed", cleanupErr.Error()}
		}
		slog.Debug("[scrape] 重试前已清理旧产物", "id", job.ID, "path", path, "removed", len(removed), "files", removed)
	}
	next := *job
	if overrideNumber {
		next.Number = request.Number
	}
	next.Logs = nil
	if action == "retry" {
		next.Logs = append(next.Logs, scrapeEvent{
			Time: time.Now().Format(time.RFC3339Nano), Stage: "准备重试",
			Message: fmt.Sprintf("已删除上次刮削产物 %d 个", len(removed)),
		})
		next.Settings = &settings
		next.Metadata, next.Provenance, next.SourceResults = nil, nil, nil
		next.Actors = nil
		next.Reorganize = false
		next.RetryExisting = job.RetryExisting || job.Status == 2
	}
	if action == "reorganize" {
		next.Settings = &settings
		next.Reorganize = true
		next.Source = "reorganize"
		// 记下整理前的输出文件，成功后由 cleanupReorganizedOutput 删除旧位置的 strm、NFO、图片和空目录。
		next.ReorgFrom = job.Output
	} else {
		next.Source = "retry"
	}
	next.Path = path
	next.Directory = filepath.Dir(path)
	next.Status = 0
	next.StartedAt, next.CompletedAt, next.Output, next.Error = "", "", "", ""
	next.DurationMS, next.Cancel, next.StopRequested, next.Warnings, next.StopNote = 0, nil, false, nil, ""
	*job = next
	s.publishJobChanged(next.ID, true)
	return nil
}

// retryJobForBatch 把一条记录重新放回队列，校验和清理旧产物与单条重试相同。不保存历史，也不启动任务。
func (s *Server) retryJobForBatch(id string, flags *taskFlags) *actionError {
	task := taskRequest{Flags: flags}
	settings, failure := s.actionSettings(id, &task)
	if failure != nil {
		return failure
	}
	// 每条记录单独加锁：清理旧产物是文件操作，不能在整个批次期间一直挡住列表和运行中的任务。
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	for i := range s.jobs {
		if s.jobs[i].ID == id {
			// 与单条带请求体的重试一致：清空番号，由刮削时重新识别。
			return s.requeueJobLocked(&s.jobs[i], "retry", task, true, settings)
		}
	}
	return &actionError{404, "not_found", "记录不存在"}
}

// batchStopLimit 是一次批量停止最多接受的记录数。停止只改内存状态，不涉及文件操作，所以上限比重试大。
const batchStopLimit = 10000

// batchStopScrapeJobs 提供 POST /api/scrape/batch/stop，一次停止多条记录，只处理请求里列出的记录。
// 整个批次在一次加锁内完成：先把等待中的标为已终止，再取消处理中的。
// 这样停掉处理中的任务后空出的线程，不会让同一批里等待中的任务先跑起来。
// 已结束、正在编辑或不存在的记录逐条返回失败原因，不影响其它记录。
func (s *Server) batchStopScrapeJobs(w http.ResponseWriter, r *http.Request) {
	var request struct {
		IDs []string `json:"ids"`
	}
	if !decode(w, r, &request) {
		return
	}
	if len(request.IDs) == 0 {
		fail(w, 400, "invalid_ids", "请选择要停止的记录")
		return
	}
	if len(request.IDs) > batchStopLimit {
		fail(w, 400, "too_many_ids", fmt.Sprintf("一次最多停止 %d 条记录", batchStopLimit))
		return
	}
	type batchFailure struct {
		ID      string `json:"id"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	wanted := map[string]bool{}
	for _, id := range request.IDs {
		wanted[id] = true
	}
	stopped := []string{}
	failures := []batchFailure{}
	found := map[string]bool{}
	now := time.Now().Format(time.RFC3339Nano)
	s.jobsMu.Lock()
	// 第一遍：等待中的直接标为已终止，之后排到它们时 runScrapeJob 会跳过。
	for i := range s.jobs {
		job := &s.jobs[i]
		if !wanted[job.ID] {
			continue
		}
		found[job.ID] = true
		switch {
		case job.Editing:
			failures = append(failures, batchFailure{ID: job.ID, Code: "job_active", Message: "记录正在更新，请稍后重试"})
		case job.Status == 0:
			job.StopRequested = true
			job.Status = -2
			job.Error = "已终止"
			job.CompletedAt = now
			stopped = append(stopped, job.ID)
			s.publishJobChanged(job.ID, true)
		case job.Status != 1:
			failures = append(failures, batchFailure{ID: job.ID, Code: "job_finished", Message: "记录已结束"})
		}
	}
	// 第二遍：取消处理中的任务，由工作协程自行结束并写入已终止状态。
	for i := range s.jobs {
		job := &s.jobs[i]
		if !wanted[job.ID] || job.Editing || job.Status != 1 {
			continue
		}
		job.StopRequested = true
		if job.Cancel != nil {
			job.Cancel()
		}
		stopped = append(stopped, job.ID)
		s.publishJobChanged(job.ID, true)
	}
	s.jobsMu.Unlock()
	for id := range wanted {
		if !found[id] {
			failures = append(failures, batchFailure{ID: id, Code: "not_found", Message: "记录不存在"})
		}
	}
	if len(stopped) > 0 {
		s.saveScrapeHistory()
	}
	writeJSON(w, 200, object{"success": len(stopped), "ids": stopped, "failed": failures})
}

// Keep saved metadata, but prepare fresh images before committing any changes.
func reorganizeExisting(ctx context.Context, path string, meta provider.Metadata, settings scrapeSettings, observe func(provider.Metadata), prepare func(provider.Metadata, string) (map[string][]byte, error)) (string, error) {
	if !supportedLinkMode(settings.Common.LinkMode) {
		return "", errors.New("当前只支持移动、原地整理和 STRM 模式")
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	if meta.Number == "" {
		return "", errors.New("记录缺少已有元数据")
	}
	if settings.OverrideNumber != "" {
		meta.Number = settings.OverrideNumber
	}
	flags := detectFilenameFlags(path, settings)
	meta.HasSubtitle, meta.Mosaic, meta.Resolution = flags.HasSubtitle, flags.Mosaic, flags.Resolution
	observe(meta)
	traceScrape(ctx, "重新整理", "已读取数据库中的已有元数据")
	output, err := organizationTarget(path, meta, settings)
	if err != nil {
		return "", err
	}
	writes := map[string][]byte{}
	managedNFO := false
	if prepare != nil {
		managedNFO = true
		writes, err = prepare(meta, output)
		if err != nil {
			return "", err
		}
	}
	switch settings.Common.LinkMode {
	case linkModeMove:
		if err := commitMove(ctx, path, output, writes, settings); err != nil {
			return "", err
		}
	case linkModeSTRM:
		if err := commitStrm(ctx, path, output, writes, settings); err != nil {
			return "", err
		}
	default:
		if err := commitScrapeRetryManagedNFO(ctx, path, output, writes, managedNFO, settings.Naming.ImageWithPrefix); err != nil {
			return "", err
		}
	}
	return output, nil
}
