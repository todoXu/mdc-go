package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"mdcng-go/internal/provider"
)

// 演员映射表：内置表 data/mapping_actor.xml（缺少时启动会从 assets/mapping_actor_default.xml 复制）；
// 自定义表 data/mapping_actor_custom.xml 存在且启用时只使用自定义表。
// 每个 <a> 条目有 zh_cn、zh_tw、jp 三个名字，keyword 是逗号分隔的别名。
// 刮削到的名字只要匹配到任一名字或任一别名，就替换成该条目所选语言的名字；匹配不到则保持原样。

const (
	builtinActorMappingFile = "mapping_actor.xml"
	customActorMappingFile  = "mapping_actor_custom.xml"
)

type actorEntry struct {
	zhCN, zhTW, jp string
	aliases        []string // keyword 里的别名，保持原顺序，已去掉空项
}

// names 返回条目的全部名字：日文、简体、繁体在前，然后是别名，已去重。
func (e *actorEntry) names() []string {
	var out []string
	seen := map[string]bool{}
	for _, name := range append([]string{e.jp, e.zhCN, e.zhTW}, e.aliases...) {
		key := normalizeActorName(name)
		if key != "" && !seen[key] {
			seen[key] = true
			out = append(out, name)
		}
	}
	return out
}

// value 返回所选语言的名字；该语言为空时依次退回简体、繁体、日文。
func (e *actorEntry) value(lang string) string {
	order := []string{e.zhCN, e.zhTW, e.jp}
	switch lang {
	case "zh_tw":
		order = []string{e.zhTW, e.zhCN, e.jp}
	case "jp":
		order = []string{e.jp, e.zhCN, e.zhTW}
	}
	for _, name := range order {
		if name != "" {
			return name
		}
	}
	return ""
}

type actorMapping struct {
	byName map[string]*actorEntry
}

// normalizeActorName 统一大小写、去掉空白，并把各种中点符号统一成「・」，便于比较。
func normalizeActorName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, name)
	name = strings.NewReplacer("･", "・", "·", "・", "•", "・").Replace(name)
	return strings.ToLower(name)
}

func parseActorMapping(data []byte) (*actorMapping, error) {
	var document struct {
		Items []struct {
			ZhCN    string `xml:"zh_cn,attr"`
			ZhTW    string `xml:"zh_tw,attr"`
			JP      string `xml:"jp,attr"`
			Keyword string `xml:"keyword,attr"`
		} `xml:"a"`
	}
	if err := xml.NewDecoder(bytes.NewReader(data)).Decode(&document); err != nil {
		return nil, fmt.Errorf("演员映射表 XML 无效：%w", err)
	}
	if len(document.Items) == 0 {
		return nil, errors.New("演员映射表没有任何条目")
	}
	mapping := &actorMapping{byName: map[string]*actorEntry{}}
	for _, item := range document.Items {
		entry := &actorEntry{zhCN: strings.TrimSpace(item.ZhCN), zhTW: strings.TrimSpace(item.ZhTW), jp: strings.TrimSpace(item.JP)}
		for _, alias := range strings.Split(item.Keyword, ",") {
			if alias = strings.TrimSpace(alias); alias != "" {
				entry.aliases = append(entry.aliases, alias)
			}
		}
		names := []string{entry.zhCN, entry.zhTW, entry.jp}
		names = append(names, entry.aliases...)
		for _, name := range names {
			key := normalizeActorName(name)
			if key == "" {
				continue
			}
			// 同一名字出现在多个条目时，以先出现的为准。
			if _, exists := mapping.byName[key]; !exists {
				mapping.byName[key] = entry
			}
		}
	}
	return mapping, nil
}

// entry 返回名字（任一语言名或别名）所在的条目，没有时为 nil。
func (m *actorMapping) entry(name string) *actorEntry {
	return m.byName[normalizeActorName(name)]
}

func (m *actorMapping) lookup(name, lang string) (string, bool) {
	entry := m.byName[normalizeActorName(name)]
	if entry == nil {
		return "", false
	}
	value := entry.value(lang)
	return value, value != ""
}

// actorMapCache 缓存已解析的映射表。文件变化（修改时间或大小）后自动重新解析。
type actorMapCache struct {
	mu      sync.Mutex
	dataDir string
	stamp   string
	mapping *actorMapping
}

// actorMaps 是进程内唯一的映射表缓存，由 NewWithPlugins 设置数据目录。
var actorMaps = &actorMapCache{}

func (c *actorMapCache) setDataDir(dir string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dataDir = dir
	c.stamp = ""
	c.mapping = nil
}

// active 返回当前应使用的映射表：启用自定义且自定义表存在时只用自定义表，否则用内置表。
func (c *actorMapCache) active(useCustom bool) (*actorMapping, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dataDir == "" {
		return nil, errors.New("未设置数据目录")
	}
	path := filepath.Join(c.dataDir, builtinActorMappingFile)
	if useCustom {
		if custom := customActorMappingPath(c.dataDir); fileExists(custom) {
			path = custom
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	stamp := fmt.Sprintf("%s|%d|%d", path, info.ModTime().UnixNano(), info.Size())
	if c.mapping != nil && c.stamp == stamp {
		return c.mapping, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	mapping, err := parseActorMapping(data)
	if err != nil {
		return nil, err
	}
	c.stamp, c.mapping = stamp, mapping
	return mapping, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// dataDirOf 返回服务的数据目录（config.json 所在目录）。
func dataDirOf(s *Server) string {
	return filepath.Dir(s.statePath)
}

func customActorMappingPath(dataDir string) string {
	return filepath.Join(dataDir, customActorMappingFile)
}

// applyActorMapping 按设置把演员名替换成映射表中所选语言的名字；未启用或匹配不到时保持原值。
// 结果会去掉替换后产生的重复名字。映射是幂等的，多次调用结果相同。
func applyActorMapping(actors []string, settings scrapeSettings) []string {
	out := make([]string, 0, len(actors))
	if !settings.Metadata.UseActorMapping {
		return append(out, actors...)
	}
	mapping, err := actorMaps.active(settings.Metadata.ActorCustom)
	if err != nil {
		slog.Warn("[mapping] 演员映射表不可用，保持原始名字", "error", err)
		return append(out, actors...)
	}
	lang := settings.Metadata.StaticLang
	seen := map[string]bool{}
	for _, actor := range actors {
		name := actor
		if value, ok := mapping.lookup(actor, lang); ok {
			name = value
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// mapMetadataActors 返回演员名已映射的元数据副本，不修改传入的值。
func mapMetadataActors(meta provider.Metadata, settings scrapeSettings) provider.Metadata {
	meta.Actors = applyActorMapping(meta.Actors, settings)
	meta.MaleActors = applyActorMapping(meta.MaleActors, settings)
	return meta
}

// githubToken 返回「系统设置 → GitHub Token」，未填写时为空。调用时不能持有 s.mu。
func (s *Server) githubToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var system struct {
		Token string `json:"github_token"`
	}
	_ = json.Unmarshal(s.configs["system"], &system)
	return strings.TrimSpace(system.Token)
}

// isGitHubHost 判断地址是否属于 GitHub。令牌只发给 GitHub，不会发给其它网站。
func isGitHubHost(host string) bool {
	host = strings.ToLower(host)
	return host == "github.com" || host == "api.github.com" || host == "raw.githubusercontent.com" || host == "objects.githubusercontent.com"
}

// fetchActorMappingXML 从远端下载映射表，并检查 HTTP 状态、大小和 XML 结构。
// token 是 GitHub Token，可为空；填写后可以访问私有仓库，也不容易触发 GitHub 的匿名访问频率限制。
func fetchActorMappingXML(ctx context.Context, document, token string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, document, nil)
	if err != nil {
		return nil, errors.New("演员映射地址无效")
	}
	request.Header.Set("Accept", "application/xml,text/xml,*/*")
	withToken := token != "" && isGitHubHost(request.URL.Hostname())
	if withToken {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		// 跳转到其它网站时去掉令牌，避免泄露。
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("跳转次数过多")
			}
			if !isGitHubHost(next.URL.Hostname()) {
				next.Header.Del("Authorization")
			}
			return nil
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("无法访问演员映射地址：%w", err)
	}
	defer response.Body.Close()
	if withToken && (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
		return nil, fmt.Errorf("GitHub Token 无效或没有该仓库的读取权限（HTTP %d）", response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("演员映射地址返回 HTTP %d", response.StatusCode)
	}
	const maxXML = 16 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxXML+1))
	if err != nil || len(data) > maxXML {
		return nil, errors.New("演员映射 XML 读取失败或超过 16 MB")
	}
	if _, err := parseActorMapping(data); err != nil {
		return nil, err
	}
	return data, nil
}

// saveCustomActorMapping 先校验再原子替换自定义表；校验或写入失败时保留原文件。
// 返回值表示文件内容是否发生了变化。
func saveCustomActorMapping(dataDir string, data []byte) (bool, error) {
	if _, err := parseActorMapping(data); err != nil {
		return false, err
	}
	target := customActorMappingPath(dataDir)
	if old, err := os.ReadFile(target); err == nil && sha256.Sum256(old) == sha256.Sum256(data) {
		return false, nil
	}
	temp, err := os.CreateTemp(dataDir, ".mapping_actor_custom-*.xml")
	if err != nil {
		return false, err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return false, err
	}
	if err := temp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(temp.Name(), target); err != nil {
		return false, err
	}
	return true, nil
}

// updateCustomActorMapping 下载远端映射表并保存到自定义表。下载或校验失败时不改动本地文件。
func (s *Server) updateCustomActorMapping(ctx context.Context, repository string) (bool, error) {
	document, err := actorMappingDocument(repository)
	if err != nil {
		return false, err
	}
	data, err := fetchActorMappingXML(ctx, document, s.githubToken())
	if err != nil {
		return false, err
	}
	return saveCustomActorMapping(filepath.Dir(s.statePath), data)
}

// RefreshActorMapping 在程序启动时调用：自定义表已启用且填写了仓库地址时，检查远端是否有更新并下载。
func (s *Server) RefreshActorMapping(ctx context.Context) {
	s.mu.RLock()
	var metadata struct {
		Custom     bool   `json:"actor_mapping_custom"`
		Repository string `json:"actor_mapping_repository"`
	}
	if raw := s.configs["metadata"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &metadata)
	}
	s.mu.RUnlock()
	if !metadata.Custom || strings.TrimSpace(metadata.Repository) == "" {
		return
	}
	updated, err := s.updateCustomActorMapping(ctx, metadata.Repository)
	if err != nil {
		slog.Warn("[mapping] 检查自定义演员映射表失败", "error", err)
		return
	}
	if updated {
		slog.Info("[mapping] 自定义演员映射表已更新")
	}
}
