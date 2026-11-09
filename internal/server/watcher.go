package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mdcng-go/internal/provider"
)

type watchConfig struct {
	Enabled         bool             `json:"watcher_enabled"`
	IntervalSeconds int              `json:"watcher_poll_interval_seconds"`
	Extensions      []string         `json:"file_ext_filter"`
	Directories     []watchDirectory `json:"watch_dirs"`
}

type watchDirectory struct {
	ID                 string          `json:"id"`
	Path               string          `json:"path"`
	Enabled            bool            `json:"enabled"`
	UseGlobalConfig    bool            `json:"use_global_config"`
	UseGlobalDownload  bool            `json:"use_global_download"`
	UseGlobalNaming    *bool           `json:"use_global_naming,omitempty"`
	UseGlobalWatermark *bool           `json:"use_global_watermark,omitempty"`
	UseGlobalNFO       *bool           `json:"use_global_nfo,omitempty"`
	UseGlobalProviders *bool           `json:"use_global_providers,omitempty"`
	Common             json.RawMessage `json:"common"`
	Download           json.RawMessage `json:"download"`
	Naming             json.RawMessage `json:"naming"`
	Watermark          json.RawMessage `json:"watermark"`
	NFO                json.RawMessage `json:"nfo"`
	Providers          json.RawMessage `json:"providers"`
}

type directoryProviderConfig struct {
	Configs    map[string]provider.Config `json:"configs"`
	Priorities *provider.Priorities       `json:"priorities,omitempty"`
}

type watchDirectoryStatus struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Matched   int    `json:"matched"`
	NewFiles  int    `json:"new_files"`
	Error     string `json:"error,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type watchCandidate struct {
	DirectoryID string `json:"directory_id"`
	Path        string `json:"path"`
	FoundAt     string `json:"found_at"`
}

type watchState struct {
	Enabled     bool                   `json:"enabled"`
	Running     bool                   `json:"running"`
	LastScan    string                 `json:"last_scan,omitempty"`
	NextScan    string                 `json:"next_scan,omitempty"`
	Directories []watchDirectoryStatus `json:"directories"`
	Recent      []watchCandidate       `json:"recent"`
}

func parseWatchConfig(raw json.RawMessage) (watchConfig, error) {
	var config watchConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return config, err
	}
	return config, nil
}

func validateWatchConfig(raw json.RawMessage) error {
	config, err := parseWatchConfig(raw)
	if err != nil {
		return fmt.Errorf("监控配置无效: %w", err)
	}
	if config.IntervalSeconds < 1 || config.IntervalSeconds > 86400 {
		return errors.New("检查周期必须是 1～86400 秒")
	}
	if len(config.Directories) > 100 {
		return errors.New("最多配置 100 个监控目录")
	}
	if err := validateTargetFolders(raw, config); err != nil {
		return err
	}
	seenIDs := make(map[string]bool)
	seenPaths := make(map[string]bool)
	for _, dir := range config.Directories {
		if len(dir.Download) > 0 && string(dir.Download) != "null" {
			if err := validateDownloadConfig(dir.Download); err != nil {
				return fmt.Errorf("目录 %s：%w", dir.ID, err)
			}
		}
		if dir.ID == "" || seenIDs[dir.ID] {
			return errors.New("监控目录 ID 不能为空或重复")
		}
		seenIDs[dir.ID] = true
		if !filepath.IsAbs(dir.Path) {
			return fmt.Errorf("监控路径必须是完整路径: %s", dir.Path)
		}
		clean := pathKey(dir.Path)
		if seenPaths[clean] {
			return fmt.Errorf("监控路径重复: %s", dir.Path)
		}
		seenPaths[clean] = true
	}
	return nil
}

// organizeTarget 是整理配置中与目标目录有关的部分。
type organizeTarget struct {
	TargetFolder string `json:"target_folder"`
	LinkMode     int    `json:"link_mode"`
}

// validateTargetFolders 检查整理目录：未开启监控时可以为空；
// 开启监控后，每个启用的监控目录实际使用的整理目录（独立配置或全局配置）
// 在移动和 STRM 模式下必须是完整路径。原地整理不需要整理目录。
func validateTargetFolders(raw json.RawMessage, config watchConfig) error {
	var global organizeTarget
	if err := json.Unmarshal(raw, &global); err != nil {
		return fmt.Errorf("整理配置无效: %w", err)
	}
	// 填写了就必须是完整路径；留空交给下面按是否开启监控判断。
	wellFormed := func(target organizeTarget, owner string) error {
		if folder := strings.TrimSpace(target.TargetFolder); folder != "" && !filepath.IsAbs(folder) {
			return fmt.Errorf("%s的整理目录必须是完整路径: %s", owner, folder)
		}
		return nil
	}
	// 开启监控后，移动和 STRM 模式要求整理目录已存在。
	usable := func(target organizeTarget, owner string) error {
		if target.LinkMode != 2 && target.LinkMode != 4 {
			return nil
		}
		folder := strings.TrimSpace(target.TargetFolder)
		if folder == "" {
			return fmt.Errorf("已开启目录监控，请先为%s设置整理目录", owner)
		}
		if info, err := os.Stat(folder); err != nil || !info.IsDir() {
			return fmt.Errorf("已开启目录监控，%s的整理目录不存在或不是文件夹: %s", owner, folder)
		}
		return nil
	}
	if err := wellFormed(global, "全局整理配置"); err != nil {
		return err
	}
	for _, dir := range config.Directories {
		target, owner := global, "全局整理配置"
		if !dir.UseGlobalConfig && len(dir.Common) > 0 && string(dir.Common) != "null" {
			if err := json.Unmarshal(dir.Common, &target); err != nil {
				return fmt.Errorf("监控目录 %s 的整理配置无效: %w", dir.Path, err)
			}
			owner = "监控目录 " + dir.Path
			if err := wellFormed(target, owner); err != nil {
				return err
			}
		}
		if config.Enabled && dir.Enabled {
			if err := usable(target, owner); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) currentWatchConfig() watchConfig {
	s.mu.RLock()
	raw := append(json.RawMessage(nil), s.configs["common"]...)
	s.mu.RUnlock()
	config, _ := parseWatchConfig(raw)
	return config
}

// StartWatcher rechecks current files, filters, and organization state on every scan.
func (s *Server) StartWatcher(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		force := true
		for {
			config := s.currentWatchConfig()
			s.watchMu.RLock()
			last := s.watchState.LastScan
			s.watchMu.RUnlock()
			// 批量删除期间和结束后的一个检查周期内不扫描；暂停期间收到的「立即扫描」保留到暂停结束。
			held := s.bulk.scansHeld()
			if !held && config.Enabled && (force || last == "" || watchScanDue(last, config.IntervalSeconds)) {
				s.scanWatchDirectories(ctx, config)
			}
			if !held {
				force = false
			}
			select {
			case <-ctx.Done():
				return
			case <-s.watchWake:
				force = true
			case <-ticker.C:
			}
		}
	}()
}

func watchScanDue(last string, interval int) bool {
	when, err := time.Parse(time.RFC3339, last)
	return err != nil || time.Since(when) >= time.Duration(interval)*time.Second
}

func (s *Server) scrapeSettingsForDirectory(dir watchDirectory) (scrapeSettings, error) {
	settings, err := s.scrapeSettings()
	if err != nil {
		return settings, err
	}
	if !dir.UseGlobalConfig && len(dir.Common) > 0 && string(dir.Common) != "null" {
		if err := json.Unmarshal(dir.Common, &settings.Common); err != nil {
			return settings, fmt.Errorf("独立整理配置无效: %w", err)
		}
	}
	if !dir.UseGlobalDownload && len(dir.Download) > 0 && string(dir.Download) != "null" {
		if err := json.Unmarshal(dir.Download, &settings.Download); err != nil {
			return settings, fmt.Errorf("独立下载配置无效: %w", err)
		}
	}
	if !useGlobalConfigValue(dir.UseGlobalNaming) && len(dir.Naming) > 0 && string(dir.Naming) != "null" {
		if err := json.Unmarshal(dir.Naming, &settings.Naming); err != nil {
			return settings, fmt.Errorf("独立命名配置无效: %w", err)
		}
	}
	if !useGlobalConfigValue(dir.UseGlobalWatermark) && len(dir.Watermark) > 0 && string(dir.Watermark) != "null" {
		if err := json.Unmarshal(dir.Watermark, &settings.Watermark); err != nil {
			return settings, fmt.Errorf("独立水印配置无效: %w", err)
		}
	}
	if !useGlobalConfigValue(dir.UseGlobalNFO) && len(dir.NFO) > 0 && string(dir.NFO) != "null" {
		if err := json.Unmarshal(dir.NFO, &settings.NFO); err != nil {
			return settings, fmt.Errorf("独立 NFO 配置无效: %w", err)
		}
	}
	if !useGlobalConfigValue(dir.UseGlobalProviders) {
		var override directoryProviderConfig
		if len(dir.Providers) > 0 && string(dir.Providers) != "null" {
			if err := json.Unmarshal(dir.Providers, &override); err != nil {
				return settings, fmt.Errorf("独立数据源配置无效: %w", err)
			}
		}
		items, err := s.providers.List()
		if err != nil {
			return settings, err
		}
		configs := make(map[string]provider.Config, len(items))
		for _, item := range items {
			config := s.providerConfig(item.ID)
			if value, ok := override.Configs[item.ID]; ok {
				config = value
			}
			configs[item.ID] = config
		}
		settings.ProviderConfigs = configs
		priorities := provider.Priorities{ByFields: map[string][]string{}}
		if override.Priorities != nil {
			for field, order := range override.Priorities.ByFields {
				for _, id := range order {
					if config, ok := configs[id]; ok && config.Enabled {
						priorities.ByFields[field] = append(priorities.ByFields[field], id)
					}
				}
			}
		} else {
			current, err := s.currentPriorities()
			if err != nil {
				return settings, err
			}
			for field, order := range current.ByFields {
				for _, id := range order {
					if config, ok := configs[id]; ok && config.Enabled {
						priorities.ByFields[field] = append(priorities.ByFields[field], id)
					}
				}
			}
		}
		settings.Priorities = &priorities
	}
	return settings, nil
}

func useGlobalConfigValue(value *bool) bool {
	return value == nil || *value
}

func (s *Server) alreadyOrganized(path string) bool {
	target := pathKey(path)
	s.jobsMu.RLock()
	defer s.jobsMu.RUnlock()
	// 数据库中只要存在该视频的记录（无论成功、失败或终止）就不再自动入队；
	// 之后是重试还是删除记录由用户决定。STRM 模式下源视频保持原位，所以按源路径比对；
	// 移动和原地整理的输出路径也一并比对。
	for _, job := range s.jobs {
		if job.Path != "" && pathKey(job.Path) == target {
			return true
		}
		if job.Output != "" && pathKey(job.Output) == target {
			return true
		}
	}
	return false
}

func (s *Server) scanWatchDirectories(ctx context.Context, config watchConfig) {
	slog.Debug("[watcher] 开始扫描", "directories", len(config.Directories), "interval", time.Duration(config.IntervalSeconds)*time.Second)
	s.watchMu.Lock()
	recent := append([]watchCandidate(nil), s.watchState.Recent...)
	s.watchState.Running = true
	s.watchState.Enabled = true
	s.jobEvents.publish("watch-changed", struct{}{})
	s.watchMu.Unlock()

	statuses := make([]watchDirectoryStatus, 0, len(config.Directories))
	stamp := time.Now().Format(time.RFC3339)
	for _, dir := range config.Directories {
		if ctx.Err() != nil {
			break
		}
		status := watchDirectoryStatus{ID: dir.ID, Path: dir.Path}
		if !dir.Enabled {
			slog.Debug("[watcher] 跳过已停用的目录", "directory", dir.ID, "path", dir.Path)
			statuses = append(statuses, status)
			continue
		}
		settings, err := s.scrapeSettingsForDirectory(dir)
		if err != nil {
			status.Error = err.Error()
			statuses = append(statuses, status)
			slog.Warn("[watcher] 目录配置无效", "directory", dir.ID, "path", dir.Path, "error", err)
			continue
		}
		if !supportedLinkMode(settings.Common.LinkMode) {
			status.Error = "自动任务目前只支持移动、原地整理和 STRM 模式"
			statuses = append(statuses, status)
			slog.Warn("[watcher] 跳过目录：不支持的整理模式", "directory", dir.ID, "path", dir.Path, "link_mode", settings.Common.LinkMode)
			continue
		}
		if usesTargetFolder(settings.Common.LinkMode) && (!filepath.IsAbs(settings.Common.TargetFolder) || strings.TrimSpace(settings.Common.TargetFolder) == "") {
			status.Error = "移动和 STRM 模式需要先设置完整的整理目录"
			statuses = append(statuses, status)
			slog.Warn("[watcher] 跳过目录：没有设置整理目录", "directory", dir.ID, "path", dir.Path)
			continue
		}
		allowed := make(map[string]bool, len(settings.Common.Extensions))
		for _, ext := range settings.Common.Extensions {
			allowed[strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))] = true
		}
		visited := 0
		candidates := make([]string, 0)
		err = filepath.WalkDir(dir.Path, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			visited++
			if visited > 200000 {
				status.Truncated = true
				return fs.SkipAll
			}
			if entry.IsDir() {
				return nil
			}
			if strings.HasSuffix(strings.ToLower(entry.Name()), ".mdc.json") {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					slog.Warn("[watcher] 删除旧版元数据文件失败", "path", path, "error", err)
				} else {
					slog.Debug("[watcher] 已删除旧版元数据文件", "path", path)
				}
				return nil
			}
			if !allowed[strings.TrimPrefix(strings.ToLower(filepath.Ext(entry.Name())), ".")] {
				return nil
			}
			status.Matched++
			if s.alreadyOrganized(path) {
				slog.Debug("[watcher] 跳过已有记录的文件", "directory", dir.ID, "path", path)
				return nil
			}
			if !eligibleFile(path, settings) {
				slog.Debug("[watcher] 跳过不符合过滤规则的文件", "directory", dir.ID, "path", path)
				return nil
			}
			if !watchFileReady(path) {
				slog.Debug("[watcher] 跳过仍在复制的文件", "directory", dir.ID, "path", path)
				return nil
			}
			if len(candidates) >= 500 {
				status.Truncated = true
				return nil
			}
			candidates = append(candidates, path)
			return nil
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			status.Error = err.Error()
		}
		if status.Error == "" && len(candidates) > 0 {
			ids := s.enqueueScrapeJobs(candidates, dir.ID, settings)
			status.NewFiles = len(ids)
			queued := make(map[string]bool, len(ids))
			for _, id := range ids {
				queued[id] = true
			}
			s.jobsMu.RLock()
			for _, job := range s.jobs {
				if queued[job.ID] {
					recent = append(recent, watchCandidate{DirectoryID: dir.ID, Path: job.Path, FoundAt: stamp})
					slog.Info("[watcher] 发现新视频", "directory", dir.ID, "path", job.Path)
				}
			}
			s.jobsMu.RUnlock()
			if len(recent) > 100 {
				recent = recent[len(recent)-100:]
			}
		}
		if status.Error != "" {
			slog.Warn("[watcher] 扫描目录出错", "directory", dir.ID, "path", dir.Path, "error", status.Error)
		} else if status.Truncated {
			slog.Warn("[watcher] 目录文件过多，本次未扫描完", "directory", dir.ID, "path", dir.Path, "matched", status.Matched, "new", status.NewFiles)
		} else {
			slog.Debug("[watcher] 目录扫描完成", "directory", dir.ID, "path", dir.Path, "matched", status.Matched, "new", status.NewFiles)
		}
		statuses = append(statuses, status)
	}
	end := time.Now()
	nextScan := end.Add(time.Duration(config.IntervalSeconds) * time.Second).Format(time.RFC3339)
	s.watchMu.Lock()
	s.watchState = watchState{
		Enabled: config.Enabled, Running: false,
		LastScan:    end.Format(time.RFC3339),
		NextScan:    nextScan,
		Directories: statuses, Recent: recent,
	}
	s.jobEvents.publish("watch-changed", struct{}{})
	s.watchMu.Unlock()
	slog.Debug("[watcher] 扫描完成", "next", nextScan)
}

func watchFileReady(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	// Compare size and modification time twice. This catches a file whose
	// producer is still copying it without delaying normal scans by seconds.
	time.Sleep(150 * time.Millisecond)
	latest, err := os.Stat(path)
	if err != nil || !latest.Mode().IsRegular() || latest.Size() != info.Size() || !latest.ModTime().Equal(info.ModTime()) {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func (s *Server) getWatchStatus(w http.ResponseWriter, r *http.Request) {
	s.watchMu.RLock()
	state := s.watchState
	s.watchMu.RUnlock()
	state.Enabled = s.currentWatchConfig().Enabled
	if state.Directories == nil {
		state.Directories = []watchDirectoryStatus{}
	}
	if state.Recent == nil {
		state.Recent = []watchCandidate{}
	}
	writeJSON(w, 200, state)
}

func (s *Server) requestWatchScan(w http.ResponseWriter, r *http.Request) {
	if !s.currentWatchConfig().Enabled {
		fail(w, 409, "watcher_disabled", "请先启用目录监控")
		return
	}
	if s.bulk.scansHeld() {
		fail(w, 409, "scan_held", "批量删除期间监控扫描已暂停，删除结束一个检查周期后自动恢复")
		return
	}
	select {
	case s.watchWake <- struct{}{}:
	default:
	}
	writeJSON(w, 202, object{"queued": true})
}
