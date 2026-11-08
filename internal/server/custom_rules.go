package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"mdcng-go/internal/provider"
)

// 自定义规则：文件名命中关键词（完全匹配，见 wholeKeywordMatch）时，给生成的文件名加后缀、
// 给 NFO 加标签、给图片加水印。配置保存在 config.json 的 custom 分区，水印图片保存在
// 数据目录的 custom-watermarks 文件夹，与「水印」设置里的系统水印分开。

const (
	customRuleLimit       = 200
	customTextLimit       = 64
	customWatermarkLimit  = 2 << 20
	customWatermarkMaxPix = 4000
)

// customRule 是一条自定义规则。Position 为 before / after，表示后缀放在系统后缀之前还是之后；
// Watermark 是水印库中的文件名，为空表示不加水印；Corner 为 0 左上、1 右上、2 右下、3 左下、5 自动。
type customRule struct {
	Keyword   string `json:"keyword"`
	Suffix    string `json:"suffix"`
	Position  string `json:"position"`
	Tag       string `json:"tag"`
	Watermark string `json:"watermark"`
	Corner    int    `json:"corner"`
}

// keywordGroup 是一组已有的识别关键词，例如「字幕」。自定义关键词不能与其中任何一个冲突。
type keywordGroup struct {
	Name  string   `json:"name"`
	Words []string `json:"words"`
}

var resolutionKeywords = []string{"4K", "8K", "2160P", "4320P"}

func (s *Server) customWatermarkDir() string {
	return filepath.Join(filepath.Dir(s.statePath), "custom-watermarks")
}

// matchCustomRules 返回文件名命中的规则，按列表顺序。
func matchCustomRules(name string, rules []customRule) []customRule {
	var hits []customRule
	for _, rule := range rules {
		if wholeKeywordMatch(name, rule.Keyword) {
			hits = append(hits, rule)
		}
	}
	return hits
}

// systemKeywordGroups 读取「整理」设置中的识别关键词，包括各监控目录的独立整理配置，再加上分辨率关键词。
func systemKeywordGroups(common json.RawMessage) ([]keywordGroup, error) {
	type keywords struct {
		Subtitle   []string `json:"subtitle_keywords"`
		LTX        []string `json:"ltx_hack_keywords"`
		LADA       []string `json:"lada_hack_keywords"`
		JAV        []string `json:"jav_hack_keywords"`
		Other      []string `json:"other_hack_keywords"`
		Uncensored []string `json:"uncensored_keywords"`
	}
	var global struct {
		keywords
		Directories []struct {
			UseGlobalConfig bool            `json:"use_global_config"`
			Common          json.RawMessage `json:"common"`
		} `json:"watch_dirs"`
	}
	if err := json.Unmarshal(common, &global); err != nil {
		return nil, fmt.Errorf("整理配置无效：%w", err)
	}
	sets := []keywords{global.keywords}
	for _, dir := range global.Directories {
		if dir.UseGlobalConfig || len(dir.Common) == 0 || string(dir.Common) == "null" {
			continue
		}
		var own keywords
		if err := json.Unmarshal(dir.Common, &own); err != nil {
			return nil, fmt.Errorf("监控目录的整理配置无效：%w", err)
		}
		sets = append(sets, own)
	}
	groups := []keywordGroup{{Name: "字幕"}, {Name: "LTX 破解"}, {Name: "LADA 破解"}, {Name: "JAV 破解"}, {Name: "其他破解"}, {Name: "无码"}, {Name: "分辨率", Words: append([]string{}, resolutionKeywords...)}}
	seen := make([]map[string]bool, len(groups))
	for i := range seen {
		seen[i] = map[string]bool{}
	}
	for _, set := range sets {
		for i, words := range [][]string{set.Subtitle, set.LTX, set.LADA, set.JAV, set.Other, set.Uncensored} {
			for _, word := range words {
				word = strings.TrimSpace(word)
				key := strings.Join(splitKeywordTokens(word), "-")
				if key == "" || seen[i][key] {
					continue
				}
				seen[i][key] = true
				groups[i].Words = append(groups[i].Words, word)
			}
		}
	}
	return groups, nil
}

// keywordConflict 判断两个关键词是否会命中同一个文件名：拆词后相同，或一方是另一方连续的一段。
func keywordConflict(a, b string) bool {
	left, right := splitKeywordTokens(a), splitKeywordTokens(b)
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	return containsTokenSequence(left, right) || containsTokenSequence(right, left)
}

// customKeywordConflict 返回与关键词冲突的系统关键词，没有冲突时 ok 为 false。
func customKeywordConflict(word string, groups []keywordGroup) (group, system string, ok bool) {
	for _, g := range groups {
		for _, w := range g.Words {
			if keywordConflict(word, w) {
				return g.Name, w, true
			}
		}
	}
	return "", "", false
}

func hasControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

// normalizeCustomRules 去掉首尾空白并校验每条规则。files 为水印库中已有的文件名。
func normalizeCustomRules(rules []customRule, groups []keywordGroup, files map[string]bool) ([]customRule, error) {
	if len(rules) > customRuleLimit {
		return nil, fmt.Errorf("自定义规则最多 %d 条", customRuleLimit)
	}
	result := make([]customRule, 0, len(rules))
	keys := map[string]int{}
	for i, rule := range rules {
		at := fmt.Sprintf("第 %d 条规则：", i+1)
		rule.Keyword = strings.TrimSpace(rule.Keyword)
		rule.Suffix = strings.TrimSpace(rule.Suffix)
		rule.Tag = strings.TrimSpace(rule.Tag)
		rule.Watermark = strings.TrimSpace(rule.Watermark)
		key := strings.Join(splitKeywordTokens(rule.Keyword), "-")
		switch {
		case key == "":
			return nil, errors.New(at + "请填写关键词")
		case utf8.RuneCountInString(rule.Keyword) > customTextLimit || utf8.RuneCountInString(rule.Suffix) > customTextLimit || utf8.RuneCountInString(rule.Tag) > customTextLimit:
			return nil, fmt.Errorf("%s关键词、后缀和 NFO 标签最多 %d 个字符", at, customTextLimit)
		case hasControl(rule.Keyword) || hasControl(rule.Tag):
			return nil, errors.New(at + "关键词和 NFO 标签不能包含控制字符")
		case hasControl(rule.Suffix) || strings.ContainsAny(rule.Suffix, `\/:*?"<>|`):
			return nil, errors.New(at + `后缀不能包含 \ / : * ? " < > | 等文件名中不允许的字符`)
		case rule.Position != "before" && rule.Position != "after":
			return nil, errors.New(at + "后缀位置无效")
		case rule.Corner != 5 && (rule.Corner < 0 || rule.Corner > 3):
			return nil, errors.New(at + "水印位置无效")
		case rule.Suffix == "" && rule.Tag == "" && rule.Watermark == "":
			return nil, errors.New(at + "请至少设置后缀、NFO 标签或水印中的一项")
		case rule.Watermark != "" && !files[rule.Watermark]:
			return nil, fmt.Errorf("%s水印图片「%s」不存在，请重新上传或改选", at, rule.Watermark)
		}
		if previous, ok := keys[key]; ok {
			return nil, fmt.Errorf("%s关键词「%s」与第 %d 条规则重复", at, rule.Keyword, previous+1)
		}
		keys[key] = i
		if group, word, ok := customKeywordConflict(rule.Keyword, groups); ok {
			return nil, fmt.Errorf("%s关键词「%s」与已有的%s关键词「%s」冲突（会命中同一个文件名）", at, rule.Keyword, group, word)
		}
		result = append(result, rule)
	}
	return result, nil
}

// validateCustomSection 校验并规范化 custom 分区，返回规范化后的 JSON。调用时需持有 s.mu。
func (s *Server) validateCustomSection(next json.RawMessage, common json.RawMessage) (json.RawMessage, error) {
	var section struct {
		Rules            []customRule `json:"rules"`
		WatermarkEnabled bool         `json:"watermark_enabled"`
	}
	decoder := json.NewDecoder(bytes.NewReader(next))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&section); err != nil {
		return nil, fmt.Errorf("自定义规则格式无效：%w", err)
	}
	groups, err := systemKeywordGroups(common)
	if err != nil {
		return nil, err
	}
	files, err := s.customWatermarkFiles()
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, file := range files {
		names[file.Name] = true
	}
	rules, err := normalizeCustomRules(section.Rules, groups, names)
	if err != nil {
		return nil, err
	}
	section.Rules = rules
	return json.Marshal(section)
}

// validateCommonAgainstCustom 在保存「整理」设置时检查新的识别关键词是否与已保存的自定义规则冲突。
func validateCommonAgainstCustom(common, custom json.RawMessage) error {
	var section struct {
		Rules []customRule `json:"rules"`
	}
	if len(custom) == 0 || json.Unmarshal(custom, &section) != nil || len(section.Rules) == 0 {
		return nil
	}
	groups, err := systemKeywordGroups(common)
	if err != nil {
		return err
	}
	for _, rule := range section.Rules {
		if group, word, ok := customKeywordConflict(rule.Keyword, groups); ok {
			return fmt.Errorf("%s关键词「%s」与自定义规则「%s」冲突，请先在「自定义规则」中修改", group, word, rule.Keyword)
		}
	}
	return nil
}

func (s *Server) getCustomKeywords(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	common := append(json.RawMessage(nil), s.configs["common"]...)
	s.mu.RUnlock()
	groups, err := systemKeywordGroups(common)
	if err != nil {
		fail(w, 500, "config_error", err.Error())
		return
	}
	writeJSON(w, 200, object{"data": groups})
}

// ---- 水印库 ----

type customWatermarkFile struct {
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Size   int64  `json:"size"`
}

// validCustomWatermarkName 只接受普通的 PNG 文件名，不能包含路径或文件名中不允许的字符。
func validCustomWatermarkName(name string) bool {
	if name == "" || name != strings.TrimSpace(name) || utf8.RuneCountInString(name) > 80 || strings.HasPrefix(name, ".") || hasControl(name) {
		return false
	}
	if strings.ContainsAny(name, `\/:*?"<>|`) || filepath.Base(name) != name {
		return false
	}
	return strings.EqualFold(filepath.Ext(name), ".png") && len(name) > len(".png")
}

func (s *Server) customWatermarkFiles() ([]customWatermarkFile, error) {
	entries, err := os.ReadDir(s.customWatermarkDir())
	if errors.Is(err, os.ErrNotExist) {
		return []customWatermarkFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	files := []customWatermarkFile{}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !validCustomWatermarkName(entry.Name()) {
			continue
		}
		path := filepath.Join(s.customWatermarkDir(), entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		file := customWatermarkFile{Name: entry.Name(), Size: info.Size()}
		if f, err := os.Open(path); err == nil {
			if config, err := png.DecodeConfig(f); err == nil {
				file.Width, file.Height = config.Width, config.Height
			}
			f.Close()
		}
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

func (s *Server) listCustomWatermarks(w http.ResponseWriter, _ *http.Request) {
	files, err := s.customWatermarkFiles()
	if err != nil {
		fail(w, 500, "read_failed", "读取自定义水印失败："+err.Error())
		return
	}
	writeJSON(w, 200, object{"data": files})
}

func (s *Server) serveCustomWatermark(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validCustomWatermarkName(name) {
		fail(w, 400, "invalid_name", "水印文件名无效")
		return
	}
	data, err := os.ReadFile(filepath.Join(s.customWatermarkDir(), name))
	if err != nil {
		fail(w, 404, "not_found", "水印图片不存在")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

// uploadCustomWatermark 提供 PUT /api/custom-watermarks/{name}，请求体是 PNG 图片本身。同名文件会被覆盖。
func (s *Server) uploadCustomWatermark(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validCustomWatermarkName(name) {
		fail(w, 400, "invalid_name", `水印文件名需以 .png 结尾，最多 80 个字符，不能包含 \ / : * ? " < > |`)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, customWatermarkLimit+1))
	if err != nil || len(data) > customWatermarkLimit {
		fail(w, 400, "too_large", "水印图片不能超过 2 MB")
		return
	}
	if http.DetectContentType(data) != "image/png" {
		fail(w, 400, "invalid_image", "水印只支持 PNG 图片")
		return
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > customWatermarkMaxPix || config.Height > customWatermarkMaxPix {
		fail(w, 400, "invalid_image", fmt.Sprintf("PNG 无法读取，或尺寸超过 %d×%d", customWatermarkMaxPix, customWatermarkMaxPix))
		return
	}
	if err := os.MkdirAll(s.customWatermarkDir(), 0o755); err != nil {
		fail(w, 500, "save_failed", "无法创建水印目录："+err.Error())
		return
	}
	if err := writeFileAtomic(filepath.Join(s.customWatermarkDir(), name), data); err != nil {
		fail(w, 500, "save_failed", "保存水印失败："+err.Error())
		return
	}
	slog.Info("[custom] 已上传自定义水印", "name", name, "size", len(data))
	writeJSON(w, 200, object{"success": true, "file": customWatermarkFile{Name: name, Width: config.Width, Height: config.Height, Size: int64(len(data))}})
}

// deleteCustomWatermark 删除水印图片。仍被已保存的规则引用时拒绝，需先在规则中改选并保存。
func (s *Server) deleteCustomWatermark(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validCustomWatermarkName(name) {
		fail(w, 400, "invalid_name", "水印文件名无效")
		return
	}
	s.mu.RLock()
	custom := s.configs["custom"]
	s.mu.RUnlock()
	var section struct {
		Rules []customRule `json:"rules"`
	}
	_ = json.Unmarshal(custom, &section)
	for _, rule := range section.Rules {
		if rule.Watermark == name {
			fail(w, 409, "in_use", fmt.Sprintf("「%s」正被已保存的规则「%s」使用，请先在规则中改选并保存", name, rule.Keyword))
			return
		}
	}
	if err := os.Remove(filepath.Join(s.customWatermarkDir(), name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		fail(w, 500, "delete_failed", "删除水印失败："+err.Error())
		return
	}
	slog.Info("[custom] 已删除自定义水印", "name", name)
	writeJSON(w, 200, object{"success": true})
}

// ---- 预览 ----

type customPreviewMark struct {
	File   string `json:"file"`
	Custom bool   `json:"custom"`
	Corner int    `json:"corner"`
}

// previewCustomRules 提供 POST /api/custom-rules/preview：按页面上尚未保存的规则和当前全局设置，
// 计算示例文件名命中的规则、后缀、NFO 标签和海报水印位置。只读，不访问视频文件。
func (s *Server) previewCustomRules(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name             string       `json:"name"`
		Rules            []customRule `json:"rules"`
		WatermarkEnabled *bool        `json:"watermark_enabled"`
	}
	if !decode(w, r, &request) {
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" || utf8.RuneCountInString(request.Name) > 255 || strings.ContainsAny(request.Name, `\/`) {
		fail(w, 400, "invalid_name", "请输入一个文件名（不含路径）")
		return
	}
	if len(request.Rules) > customRuleLimit {
		fail(w, 400, "too_many_rules", fmt.Sprintf("自定义规则最多 %d 条", customRuleLimit))
		return
	}
	settings, err := s.scrapeSettings()
	if err != nil {
		fail(w, 500, "config_error", "无法读取设置")
		return
	}
	settings.Custom.Rules = request.Rules
	flags := detectFilenameFlagsNoProbe(request.Name, settings)
	number := detectNumber(request.Name)
	before, system, after := namingSuffixParts(flags, settings)
	tags := []nfoTagEntry{}
	for _, entry := range nfoTagsWithOrigin(provider.Metadata{Number: number}, flags, settings).Tags {
		switch entry.Origin {
		case nfoTagResolution, "mosaic", "hack_type", "subtitle", nfoTagCustom:
			tags = append(tags, entry)
		}
	}
	marks := []customPreviewMark{}
	// 预览按页面上开关的状态显示，未保存的修改也生效。
	if request.WatermarkEnabled != nil {
		settings.Custom.WatermarkEnabled = *request.WatermarkEnabled
	}
	enabled := watermarkBool(settings.Watermark, "poster")
	if enabled {
		selected := selectedWatermarks(flags, settings.Watermark, settings.Custom.WatermarkEnabled)
		corners, err := watermarkCorners(selected, settings.Watermark)
		if err != nil {
			fail(w, 400, "watermark_config", err.Error())
			return
		}
		for i, mark := range selected {
			marks = append(marks, customPreviewMark{File: mark.file, Custom: mark.custom, Corner: corners[i]})
		}
	}
	matched := []string{}
	for _, rule := range flags.Custom {
		matched = append(matched, rule.Keyword)
	}
	writeJSON(w, 200, object{
		"number": number, "matched": matched,
		"suffix":         object{"before": before, "system": system, "after": after},
		"tags":           tags,
		"poster_enabled": enabled, "system_watermark_enabled": watermarkBool(settings.Watermark, "enabled"),
		"custom_watermark_enabled": settings.Custom.WatermarkEnabled, "watermark_mode": settings.Watermark["mode"], "marks": marks,
	})
}
