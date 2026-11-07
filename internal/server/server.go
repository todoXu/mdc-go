package server

import (
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"mdcng-go/internal/provider"
)

//go:embed assets/*.json assets/mapping_actor_default.xml
var assets embed.FS

// defaultDataFiles 是数据目录中由内置默认文件生成的文件：数据目录里的文件 → 内置默认文件。
// data 是纯运行时目录，启动时缺哪个就从默认复制哪个，之后程序只读写数据目录里的副本。
var defaultDataFiles = map[string]string{
	builtinActorMappingFile: "assets/mapping_actor_default.xml",
	"priorities.json":       "assets/priorities_default.json",
}

// seedDataFiles 把数据目录里缺少的文件从内置默认复制出来；已经存在的文件不覆盖。
func seedDataFiles(dataDir string) error {
	for name, source := range defaultDataFiles {
		target := filepath.Join(dataDir, name)
		if _, err := os.Stat(target); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		data, err := assets.ReadFile(source)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(target, data); err != nil {
			return fmt.Errorf("生成 %s 失败：%w", name, err)
		}
		slog.Info("[config] 已从内置默认文件生成", "path", target)
	}
	return nil
}

type object = map[string]any

// Server implements the observed frontend contract. It never contacts the old deployment.
type Server struct {
	mux             *http.ServeMux
	mu              sync.RWMutex
	configs         map[string]json.RawMessage
	defaults        map[string]json.RawMessage
	statePath       string
	providers       *provider.Registry
	providerConfigs map[string]provider.Config
	priorities      *provider.Priorities
	priorityPath    string
	imageRefs       map[string]imageRef
	watchMu         sync.RWMutex
	watchState      watchState
	watchWake       chan struct{}
	jobsMu          sync.RWMutex
	historyMu       sync.Mutex
	actorDir        actorDirectory
	embyRefresh     embyRefresher
	gallery         galleryIndex
	jobs            []scrapeJob
	jobSequence     int
	jobWake         chan struct{}
	scrapeSlots     scrapeLimiter
	jobEvents       jobEventHub
}

func New(dataDir string) (*Server, error) {
	return NewWithPlugins(dataDir, filepath.Join(dataDir, "plugins"))
}

func NewWithPlugins(dataDir, pluginsDir string) (*Server, error) {
	s := &Server{mux: http.NewServeMux(), statePath: filepath.Join(dataDir, "config.json"), watchWake: make(chan struct{}, 1), jobWake: make(chan struct{}, 1)}
	if err := seedDataFiles(dataDir); err != nil {
		return nil, err
	}
	actorMaps.setDataDir(dataDir)
	registry, err := provider.New(pluginsDir)
	if err != nil {
		return nil, err
	}
	s.providers = registry
	s.priorityPath = filepath.Join(dataDir, "priorities.json")
	if b, err := os.ReadFile(s.priorityPath); err == nil {
		if err := json.Unmarshal(b, &s.priorities); err != nil || s.priorities == nil {
			return nil, fmt.Errorf("invalid priority config")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	s.providerConfigs, err = registry.LoadConfigs()
	if err != nil {
		return nil, fmt.Errorf("读取数据源配置失败：%w", err)
	}
	if b, err := assets.ReadFile("assets/defaults.json"); err != nil {
		return nil, err
	} else if err := json.Unmarshal(b, &s.defaults); err != nil {
		return nil, err
	}
	s.configs = make(map[string]json.RawMessage)
	for name, value := range s.defaults {
		s.configs[name] = value
	}
	if b, err := os.ReadFile(s.statePath); err == nil {
		var saved map[string]json.RawMessage
		if err := json.Unmarshal(b, &saved); err != nil {
			return nil, fmt.Errorf("invalid config file: %w", err)
		}
		for name, value := range saved {
			base, exists := s.defaults[name]
			if !exists {
				return nil, fmt.Errorf("unknown saved config section %q", name)
			}
			merged, err := mergeConfig(base, value)
			if err != nil {
				return nil, fmt.Errorf("invalid saved config %s: %w", name, err)
			}
			s.configs[name] = merged
		}
	} else if errors.Is(err, os.ErrNotExist) {
		// 第一次启动：旧的刮削数据库改名备份，随后新建空库；再用内置默认配置
		// （assets/defaults.json）生成 config.json。先处理数据库，失败时不会留下新配置。
		if err := s.retireScrapeHistory(); err != nil {
			return nil, fmt.Errorf("备份旧的刮削数据库失败：%w", err)
		}
		if err := save(s.statePath, s.configs); err != nil {
			return nil, fmt.Errorf("生成默认配置文件失败：%w", err)
		}
		slog.Info("[config] 没有找到配置文件，已用默认配置生成", "path", s.statePath)
	} else {
		return nil, err
	}
	applyLogLevelConfig(s.configs["system"])
	if err := s.loadScrapeHistory(); err != nil {
		return nil, fmt.Errorf("读取刮削记录失败：%w", err)
	}
	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-MDC-Implementation", "go-native")
	w.Header().Set("Cache-Control", "no-store")
	s.mux.ServeHTTP(w, r)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, object{"success": false, "error": code, "message": message})
}

func (s *Server) routes() {
	s.providerRoutes()
	s.mux.HandleFunc("GET /api/scrape/jobs", s.listScrapeJobs)
	s.mux.HandleFunc("GET /api/scrape/events", s.streamJobEvents)
	s.mux.HandleFunc("GET /api/scrape/jobs/{id}", s.getScrapeJob)
	s.mux.HandleFunc("PUT /api/scrape/jobs/{id}/metadata", s.editScrapeMetadata)
	s.mux.HandleFunc("POST /api/scrape/jobs/{id}/crop-source", s.scrapeCropSource)
	s.mux.HandleFunc("POST /api/scrape/jobs/{id}/crop", s.cropScrapePoster)
	s.mux.HandleFunc("GET /api/scrape/jobs/{id}/images/{name}", s.serveScrapeImage)
	s.mux.HandleFunc("GET /api/scrape/jobs/{id}/nfo", s.getScrapeJobNFO)
	s.mux.HandleFunc("POST /api/scrape/jobs/{id}/{action}", s.scrapeJobAction)
	s.mux.HandleFunc("DELETE /api/scrape/jobs/{id}", s.deleteScrapeJob)
	s.mux.HandleFunc("GET /api/system/database", s.getScrapeDatabaseStatus)
	s.mux.HandleFunc("GET /api/system/folders", s.listLocalFolders)
	s.mux.HandleFunc("GET /api/system/logs", s.getSystemLogs)
	s.mux.HandleFunc("GET /api/system/logs/export", s.exportSystemLogs)
	s.mux.HandleFunc("PUT /api/system/logs/file", s.setSystemLogFile)
	s.mux.HandleFunc("GET /api/watcher/status", s.getWatchStatus)
	s.mux.HandleFunc("POST /api/watcher/scan", s.requestWatchScan)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, object{"status": "ok", "implementation": "go-native"})
	})
	s.mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		// os 供页面显示运行平台、选择路径分隔符：windows、linux 等（与 Go 的 runtime.GOOS 一致）。
		writeJSON(w, 200, object{"version": Version, "os": runtime.GOOS})
	})
	s.mux.HandleFunc("GET /api/config/{section}", s.getConfig)
	s.mux.HandleFunc("PUT /api/config/{section}", s.putConfig)
	s.mux.HandleFunc("POST /api/config/metadata/verify-actor-mapping", s.verifyActorMapping)
	s.mux.HandleFunc("POST /api/config/metadata/check-actor-mapping", s.checkActorMapping)
	s.mux.HandleFunc("GET /api/system/monitor", s.monitor)
	s.mux.HandleFunc("GET /api/emby/libraries", s.listEmbyLibraries)
	s.mux.HandleFunc("GET /api/actors", s.listActors)
	s.mux.HandleFunc("POST /api/actor-tools/fill-missing", s.fillMissingAvatars)
	s.mux.HandleFunc("POST /api/actor-gallery/{source}/refresh", s.refreshGallery)
	s.mux.HandleFunc("GET /api/actor-gallery/{source}/image", s.galleryImageHandler)
	s.mux.HandleFunc("GET /api/actors/{id}", s.getActor)
	s.mux.HandleFunc("GET /api/actors/{id}/image", s.actorImage)
	s.mux.HandleFunc("GET /api/emby/items/{id}/image", s.actorImage) // 影片封面，与演员头像相同的转发方式
	s.mux.HandleFunc("POST /api/actors/{id}/image", s.setActorImage)
	s.mux.HandleFunc("PUT /api/actors/{id}/names", s.updateActorNames)
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	value, ok := s.configs[r.PathValue("section")]
	s.mu.RUnlock()
	if !ok {
		fail(w, 404, "not_found", "未知配置类别")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(value)
}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	maxBody := int64(1 << 20)
	if r.PathValue("section") == "user" {
		maxBody = 2 << 20 // A 1 MB image grows when encoded as a data URL.
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		fail(w, 400, "invalid_body", err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	section := r.PathValue("section")
	current, ok := s.configs[section]
	if !ok {
		fail(w, 404, "not_found", "未知配置类别")
		return
	}
	next, err := mergeConfig(current, b)
	if err != nil {
		fail(w, 400, "invalid_config", err.Error())
		return
	}
	if section == "common" {
		if err := validateWatchConfig(next); err != nil {
			fail(w, 400, "invalid_watch_config", err.Error())
			return
		}
	}
	if section == "download" {
		if err := validateDownloadConfig(next); err != nil {
			fail(w, 400, "invalid_download_config", err.Error())
			return
		}
	}
	if section == "system" {
		var settings struct {
			Threads     float64 `json:"scrape_thread_count"`
			Timeout     float64 `json:"scrape_timeout"`
			GitHubToken string  `json:"github_token"`
			LogLevel    string  `json:"log_level"`
			LogInterval float64 `json:"log_refresh_interval"`
		}
		if err := json.Unmarshal(next, &settings); err != nil || settings.Threads < 1 || settings.Threads > 128 || math.Trunc(settings.Threads) != settings.Threads || settings.Timeout < 1 || settings.Timeout > 86400 || math.Trunc(settings.Timeout) != settings.Timeout {
			fail(w, 400, "invalid_system_config", "线程数或超时设置超出允许范围")
			return
		}
		// 令牌会放进 HTTP 请求头，只允许可见的 ASCII 字符，不能有空格或换行。
		if !validSecretToken(settings.GitHubToken) {
			fail(w, 400, "invalid_system_config", "GitHub Token 只能包含字母、数字和符号，不能有空格或换行，最长 512 个字符")
			return
		}
		if _, ok := logLevelNames[settings.LogLevel]; !ok {
			fail(w, 400, "invalid_system_config", "日志等级须为 error、warning、info 或 debug")
			return
		}
		if settings.LogInterval < 1 || settings.LogInterval > 60 || math.Trunc(settings.LogInterval) != settings.LogInterval {
			fail(w, 400, "invalid_system_config", "日志自动刷新间隔须为 1～60 秒的整数")
			return
		}
	}
	if section == "actress" {
		var settings struct {
			Emby struct {
				ServerAddress string `json:"server_address"`
				APIKey        string `json:"api_key"`
				AutoScrape    bool   `json:"auto_scrape_new_actors"`
				Refresh       bool   `json:"refresh_after_scrape"`
			} `json:"emby_config"`
		}
		if err := json.Unmarshal(next, &settings); err != nil {
			fail(w, 400, "invalid_emby_config", "Emby 配置格式无效")
			return
		}
		address := strings.TrimSpace(settings.Emby.ServerAddress)
		if address != "" {
			parsed, err := url.Parse(address)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				fail(w, 400, "invalid_emby_config", "Emby 服务器地址须为 HTTP 或 HTTPS 地址")
				return
			}
		}
		if settings.Emby.AutoScrape && (address == "" || strings.TrimSpace(settings.Emby.APIKey) == "") {
			fail(w, 400, "invalid_emby_config", "启用自动刮削前请填写 Emby 服务器地址和 API Key")
			return
		}
		if settings.Emby.Refresh && (address == "" || strings.TrimSpace(settings.Emby.APIKey) == "") {
			fail(w, 400, "invalid_emby_config", "启用自动扫描媒体库前请填写 Emby 服务器地址和 API Key")
			return
		}
	}
	if section == "user" {
		var profile struct {
			Username string `json:"username"`
			Avatar   string `json:"avatar"`
		}
		if err := json.Unmarshal(next, &profile); err != nil || len([]rune(strings.TrimSpace(profile.Username))) < 1 || len([]rune(profile.Username)) > 64 {
			fail(w, 400, "invalid_user_config", "用户名须为 1～64 个字符")
			return
		}
		if profile.Avatar != "" {
			declaredType := ""
			for _, prefix := range []string{"data:image/jpeg;base64,", "data:image/png;base64,", "data:image/webp;base64,"} {
				if strings.HasPrefix(profile.Avatar, prefix) {
					profile.Avatar = strings.TrimPrefix(profile.Avatar, prefix)
					declaredType = strings.TrimSuffix(strings.TrimPrefix(prefix, "data:"), ";base64,")
					break
				}
			}
			decoded, err := base64.StdEncoding.DecodeString(profile.Avatar)
			if declaredType == "" || err != nil || len(decoded) > 1<<20 || http.DetectContentType(decoded) != declaredType {
				fail(w, 400, "invalid_user_config", "头像必须是不超过 1 MB 的 JPG、PNG 或 WebP 图片")
				return
			}
		}
	}
	updated := make(map[string]json.RawMessage, len(s.configs))
	for key, value := range s.configs {
		updated[key] = value
	}
	updated[section] = next
	if err := save(s.statePath, updated); err != nil {
		fail(w, 500, "save_failed", "无法保存本地配置")
		return
	}
	s.configs = updated
	if section == "common" {
		select {
		case s.watchWake <- struct{}{}:
		default:
		}
	}
	if section == "system" {
		applyLogLevelConfig(next)
	}
	writeJSON(w, 200, object{"success": true})
}

// applyLogLevelConfig 按 system 配置中的 log_level 设置日志等级，值无效时保持当前等级。
func applyLogLevelConfig(raw json.RawMessage) {
	var settings struct {
		LogLevel string `json:"log_level"`
	}
	if json.Unmarshal(raw, &settings) == nil && !setLogLevel(settings.LogLevel) {
		slog.Warn("[log] 日志等级无效，沿用当前等级", "log_level", settings.LogLevel)
	}
}

// Merge only known fields and preserve unspecified nested values. Complex business
// constraints (ranges, path validity, provider-specific options) remain TODO.
func mergeConfig(current, update []byte) (json.RawMessage, error) {
	var base, patch object
	if err := json.Unmarshal(current, &base); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(update, &patch); err != nil {
		return nil, err
	}
	if patch == nil {
		return nil, fmt.Errorf("配置必须是 JSON 对象")
	}
	if err := mergeObject(base, patch, ""); err != nil {
		return nil, err
	}
	return json.Marshal(base)
}

func mergeObject(base, patch object, path string) error {
	for key, value := range patch {
		old, ok := base[key]
		if !ok {
			return fmt.Errorf("未知配置字段 %s%s", path, key)
		}
		if value == nil && (key == "custom_folder" || key == "user_id" || key == "library_ids") {
			base[key] = nil
			continue
		}
		if old == nil {
			if value != nil {
				if _, ok := value.(string); !ok {
					return fmt.Errorf("字段 %s%s 需要字符串或 null", path, key)
				}
			}
			base[key] = value
			continue
		}
		switch typed := old.(type) {
		case map[string]any:
			nested, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("字段 %s%s 需要对象", path, key)
			}
			// Empty dictionaries represent extensible maps in the original contract.
			if len(typed) == 0 {
				base[key] = nested
				continue
			}
			if err := mergeObject(typed, nested, path+key+"."); err != nil {
				return err
			}
		case []any:
			if _, ok := value.([]any); !ok {
				return fmt.Errorf("字段 %s%s 需要数组", path, key)
			}
			base[key] = value
		case string:
			if _, ok := value.(string); !ok {
				return fmt.Errorf("字段 %s%s 需要字符串", path, key)
			}
			base[key] = value
		case bool:
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("字段 %s%s 需要布尔值", path, key)
			}
			base[key] = value
		case float64:
			if _, ok := value.(float64); !ok {
				return fmt.Errorf("字段 %s%s 需要数值", path, key)
			}
			base[key] = value
		default:
			return fmt.Errorf("字段 %s%s 类型尚未支持", path, key)
		}
	}
	return nil
}

func save(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = io.Copy(f, bytes.NewReader(b)); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (s *Server) monitor(w http.ResponseWriter, r *http.Request) {
	workers := 0
	s.mu.RLock()
	var config object
	_ = json.Unmarshal(s.configs["system"], &config)
	if n, ok := config["scrape_thread_count"].(float64); ok {
		workers = int(n)
	}
	s.mu.RUnlock()
	current := []scrapeJob{}
	success, waiting, failed := 0, 0, 0
	s.jobsMu.RLock()
	for _, job := range s.jobs {
		switch job.Status {
		case 0:
			waiting++
		case 1:
			current = append(current, job)
		case 2:
			success++
		case -1:
			failed++
		}
	}
	s.jobsMu.RUnlock()
	writeJSON(w, http.StatusOK, object{
		"scrape":  object{"current_tasks": current, "total_workers": workers, "success": success, "waiting": waiting, "error": failed},
		"manual":  object{"current_job": nil, "current_search_path": nil, "processing": 0, "finished": 0, "total": 0},
		"monitor": object{"this_week_count": 0, "last_week_count": 0},
		"actress": object{"current": 0, "last_week_count": 0},
	})
}

// validSecretToken 检查令牌能否安全地放进 HTTP 请求头：可以为空；不为空时只能是可见的 ASCII 字符，最长 512 个。
func validSecretToken(value string) bool {
	if len(value) > 512 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] <= ' ' || value[i] > '~' {
			return false
		}
	}
	return true
}
