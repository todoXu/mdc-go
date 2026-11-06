// Package provider loads user-editable JavaScript data source plugins.
package provider

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed defaults/*/plugin.json defaults/*/main.js
var defaults embed.FS

type Manifest struct {
	APIVersion int    `json:"api_version"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	Engine     string `json:"engine"`
	BaseURL    string `json:"base_url"`
	InputHint  string `json:"input_hint,omitempty"`
	Builtin    bool   `json:"-"`
}
type Config struct {
	Enabled         bool   `json:"enabled"`
	BaseURL         string `json:"base_url"`
	Cookies         string `json:"cookies"`
	UserAgent       string `json:"user_agent"`
	Proxy           string `json:"proxy"`
	CooldownSeconds int    `json:"cooldown_seconds"`
	// RequestTimeout 是单个请求的超时秒数，0 表示使用默认值 20。
	RequestTimeout int `json:"request_timeout"`
	// TrailerTimeout 是预告片下载的超时秒数，0 表示使用默认值 60。
	TrailerTimeout int `json:"trailer_timeout"`
	// RetryTimes 是请求失败后额外重复请求的次数，0 表示不重试。
	RetryTimes int            `json:"retry_times"`
	Options    map[string]any `json:"options"`
}
type Metadata struct {
	HasSubtitle     bool              `json:"has_subtitle"`
	Mosaic          string            `json:"mosaic,omitempty"`
	Resolution      string            `json:"resolution,omitempty"`
	ImagePreviews   map[string]string `json:"image_previews,omitempty"`
	Provider        string            `json:"provider"`
	SourceURL       string            `json:"source_url"`
	Number          string            `json:"number"`
	PublishNumber   string            `json:"publish_number,omitempty"`
	OriginalOutline string            `json:"original_outline,omitempty"`
	OriginalPlot    string            `json:"original_plot,omitempty"`
	SortTitle       string            `json:"sort_title,omitempty"`
	TranslatedBy    string            `json:"translated_by,omitempty"`
	Country         string            `json:"country,omitempty"`
	MPAA            string            `json:"mpaa,omitempty"`
	CustomRating    string            `json:"custom_rating,omitempty"`
	CriticRating    string            `json:"critic_rating,omitempty"`
	Votes           string            `json:"votes,omitempty"`
	MaleActors      []string          `json:"male_actors,omitempty"`
	Title           string            `json:"title"`
	OriginalTitle   string            `json:"original_title"`
	Poster          string            `json:"poster"`
	PosterFromCover bool              `json:"poster_from_cover,omitempty"`
	Outline         string            `json:"outline"`
	Rating          string            `json:"rating"`
	Cover           string            `json:"cover"`
	Release         string            `json:"release"`
	Runtime         string            `json:"runtime"`
	Director        string            `json:"director"`
	Studio          string            `json:"studio"`
	Publisher       string            `json:"publisher"`
	Series          string            `json:"series"`
	Actors          []string          `json:"actors"`
	Tags            []string          `json:"tags"`
	Samples         []string          `json:"samples"`
	Trailer         string            `json:"trailer,omitempty"`
	LocalImages     map[string]string `json:"local_images,omitempty"`
}
type Image struct {
	Body        []byte
	ContentType string
}
type SourceError struct{ Code, Message string }

func (e *SourceError) Error() string   { return e.Message }
func issue(code, message string) error { return &SourceError{code, message} }

type Source interface {
	Descriptor() Manifest
	Probe(context.Context, Config) error
	Fetch(context.Context, Config, string) (Metadata, error)
	ReadImage(context.Context, Config, string, string) (Image, error)
	ReadTrailer(context.Context, Config, string, string) (Image, error)
}
type Registry struct {
	Dir  string
	mu   sync.Mutex
	last map[string]time.Time
}

var idPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var numberPattern = regexp.MustCompile(`^[A-Z0-9]+(?:-[A-Z0-9]+)*$`)

func New(dir string) (*Registry, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	entries, err := defaults.ReadDir("defaults")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		target := filepath.Join(dir, entry.Name())
		if err := os.MkdirAll(target, 0700); err != nil {
			return nil, err
		}
		for _, name := range []string{"plugin.json", "main.js"} {
			path := filepath.Join(target, name)
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				data, err := defaults.ReadFile("defaults/" + entry.Name() + "/" + name)
				if err != nil {
					return nil, err
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					return nil, err
				}
			} else if err != nil {
				return nil, err
			}
		}
	}
	return &Registry{Dir: dir, last: map[string]time.Time{}}, nil
}
func (r *Registry) List() ([]Manifest, error) {
	entries, err := os.ReadDir(r.Dir)
	if err != nil {
		return nil, err
	}
	out := []Manifest{}
	seen := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		file := filepath.Join(r.Dir, entry.Name(), "plugin.json")
		data, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var m Manifest
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("插件 %s 清单格式错误: %w", entry.Name(), err)
		}
		if err := validate(m); err != nil {
			return nil, fmt.Errorf("插件 %s: %w", entry.Name(), err)
		}
		if entry.Name() != m.ID {
			return nil, fmt.Errorf("插件目录 %s 与 ID %s 不一致", entry.Name(), m.ID)
		}
		if seen[m.ID] {
			return nil, fmt.Errorf("重复的插件 ID: %s", m.ID)
		}
		m.Builtin = IsBuiltin(m.ID)
		if _, err := os.Stat(filepath.Join(r.Dir, m.ID, "main.js")); err != nil {
			return nil, fmt.Errorf("插件 %s 缺少 main.js", m.ID)
		}
		seen[m.ID] = true
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func IsBuiltin(id string) bool {
	if !idPattern.MatchString(id) {
		return false
	}
	_, err := defaults.ReadFile("defaults/" + id + "/plugin.json")
	return err == nil
}
func (r *Registry) Get(id string) (Source, error) {
	items, err := r.List()
	if err != nil {
		return nil, err
	}
	for _, m := range items {
		if m.ID == id {
			return &scriptSource{manifest: m, registry: r}, nil
		}
	}
	return nil, issue("provider_not_found", "没有安装这个数据源插件")
}
func validate(m Manifest) error {
	if m.APIVersion != 2 || m.Engine != "javascript-v1" {
		return fmt.Errorf("不支持的插件协议或引擎")
	}
	if !idPattern.MatchString(m.ID) || m.Name == "" || m.Version == "" {
		return fmt.Errorf("插件标识、名称或版本无效")
	}
	_, err := baseURL(m.BaseURL)
	return err
}
func baseURL(value string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u == nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, issue("invalid_config", "站点地址需要是有效的 HTTP/HTTPS 地址，不能包含账户或查询参数")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}
func NormalizeNumber(number string) (string, error) {
	number = strings.ToUpper(strings.TrimSpace(number))
	number = strings.ReplaceAll(number, "_", "-")
	if len(number) < 3 || len(number) > 64 || !numberPattern.MatchString(number) {
		return "", issue("invalid_number", "请输入有效番号，不要填写网址或文件路径")
	}
	return number, nil
}
