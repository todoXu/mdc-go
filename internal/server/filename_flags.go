package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

type probeStreams struct {
	Streams []struct {
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		CodecType string `json:"codec_type"`
	} `json:"streams"`
}

func detectRealResolution(path string) string {
	if strings.EqualFold(filepath.Ext(path), ".strm") {
		slog.Debug("[resolution] STRM 文件跳过真实分辨率探测", "path", path)
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		slog.Debug("[resolution] 视频文件不可访问，跳过真实分辨率探测", "path", path, "error", err)
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height,codec_type", "-of", "json", path).Output()
	if err != nil {
		switch {
		case errors.Is(err, exec.ErrNotFound):
			slog.Debug("[resolution] 未找到 ffprobe（需安装并加入 PATH），跳过真实分辨率探测", "path", path)
		case errors.Is(err, context.DeadlineExceeded):
			slog.Debug("[resolution] ffprobe 探测超时，跳过真实分辨率探测", "path", path)
		default:
			slog.Debug("[resolution] ffprobe 执行失败，跳过真实分辨率探测", "path", path, "error", err)
		}
		return ""
	}
	var result probeStreams
	if err := json.Unmarshal(out, &result); err != nil || len(result.Streams) == 0 {
		slog.Debug("[resolution] ffprobe 输出无法解析，跳过真实分辨率探测", "path", path)
		return ""
	}
	stream := result.Streams[0]
	if stream.CodecType != "" && stream.CodecType != "video" {
		slog.Debug("[resolution] 文件没有视频流，跳过真实分辨率探测", "path", path)
		return ""
	}
	pixels := stream.Width
	if stream.Height > pixels {
		pixels = stream.Height
	}
	switch {
	case pixels >= 7000:
		return "8K"
	case pixels >= 3000:
		return "4K"
	case pixels >= 1000:
		return "1080P"
	case pixels >= 600:
		return "720P"
	}
	return ""
}

type filenameFlags struct {
	HasSubtitle    bool   `json:"has_subtitle"`
	SubtitleMarker string `json:"subtitle_marker,omitempty"`
	Mosaic         string `json:"mosaic"`
	MosaicMarker   string `json:"mosaic_marker,omitempty"`
	Resolution     string `json:"resolution,omitempty"`
	// Custom 是文件名命中的自定义规则（按规则列表顺序），用于后缀、NFO 标签和水印。
	Custom []customRule `json:"custom,omitempty"`
}

type taskFlags struct {
	Clear      bool   `json:"clear"`
	Subtitle   bool   `json:"subtitle"`
	LTX        bool   `json:"ltx"`
	LADA       bool   `json:"lada"`
	JAV        bool   `json:"jav"`
	Hack       bool   `json:"hack"`
	Resolution string `json:"resolution"`
	Uncensored bool   `json:"uncensored"`
}

var resolutionToken = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(8k|4320p|4k|2160p)(?:$|[^a-z0-9])`)

// keywordSeparators 是拆分文件名和关键词时使用的分隔符。
const keywordSeparators = "-_. ()[]【】（）"

// splitKeywordTokens 按分隔符和空白把文本拆成小写的词，中文不再拆分。
func splitKeywordTokens(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(keywordSeparators, r)
	})
}

// wholeKeywordMatch 判断关键词是否完整出现在文件名中：关键词拆出的每个词，都必须是文件名词序列中
// 连续的一段，且完全相同。例如 "中文" 只命中独立的「中文」，不会命中「中文字幕」；"-LTX-破解" 命中「LTX」「破解」连续出现。
func wholeKeywordMatch(name, word string) bool {
	return containsTokenSequence(splitKeywordTokens(name), splitKeywordTokens(word))
}

// containsTokenSequence 判断 want 是否是 have 中连续、完全相同的一段。
func containsTokenSequence(have, want []string) bool {
	if len(want) == 0 {
		return false
	}
	for start := 0; start+len(want) <= len(have); start++ {
		matched := true
		for i, token := range want {
			if have[start+i] != token {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func matchingKeyword(name string, words []string) string {
	for _, word := range words {
		if word != "" && wholeKeywordMatch(name, word) {
			return word
		}
	}
	return ""
}

// matchingSubtitleKeyword 按字幕关键词识别中文字幕。只由英文字母和数字组成的关键词（如 C、CHS）
// 必须是文件名中独立的一段，前后是分隔符或开头结尾，避免 C 匹配到任何含字母 c 的名字；
// 其他关键词（如 中字）只要包含即可。都不区分大小写。
func matchingSubtitleKeyword(name string, words []string) string {
	for _, word := range words {
		word = strings.TrimSpace(word)
		if word != "" && wholeKeywordMatch(name, word) {
			return word
		}
	}
	return ""
}

func detectFilenameFlags(path string, settings scrapeSettings) filenameFlags {
	return detectFilenameFlagsWithProbe(path, settings, true)
}

// detectFilenameFlagsNoProbe 只按文件名和配置关键词识别标记，不调用 ffprobe 探测真实分辨率。
// 用于加载历史记录：关键词需要按当前配置刷新，但没必要在启动时把每条记录的视频都读一遍。
func detectFilenameFlagsNoProbe(path string, settings scrapeSettings) filenameFlags {
	return detectFilenameFlagsWithProbe(path, settings, false)
}

func detectFilenameFlagsWithProbe(path string, settings scrapeSettings, probe bool) filenameFlags {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	flags := filenameFlags{Mosaic: "is_censored"}
	flags.SubtitleMarker = matchingSubtitleKeyword(name, settings.Common.Subtitle)
	flags.HasSubtitle = flags.SubtitleMarker != ""
	version := name
	if number := detectNumber(path); number != "" {
		pattern := regexp.MustCompile(`(?i)` + strings.ReplaceAll(regexp.QuoteMeta(number), "-", `[-_ ]?`))
		version = pattern.ReplaceAllString(version, " ")
	}
	for _, rule := range []struct {
		kind  string
		words []string
	}{
		{"is_ltx_hack", settings.Common.LTX},
		{"is_lada_hack", settings.Common.LADA},
		{"is_jav_hack", settings.Common.JAV},
	} {
		marker := matchingKeyword(version, rule.words)
		if marker != "" {
			flags.Mosaic, flags.MosaicMarker = rule.kind, marker
			break
		}
	}
	// 不是 LTX / LADA / JAV 时，再依次判断其他破解、无码；都不是就保持有码。
	if flags.MosaicMarker == "" {
		if marker := matchingKeyword(name, settings.Common.Other); marker != "" {
			flags.Mosaic, flags.MosaicMarker = "is_other_hack", marker
		} else if marker := matchingKeyword(name, settings.Common.Uncensored); marker != "" {
			flags.Mosaic, flags.MosaicMarker = "is_uncensored", marker
		}
	}
	if match := resolutionToken.FindStringSubmatch(name); len(match) > 1 {
		flags.Resolution = "4K"
		if strings.EqualFold(match[1], "8k") || strings.EqualFold(match[1], "4320p") {
			flags.Resolution = "8K"
		}
	}
	if probe && settings.Naming.Suffixes != nil && strings.EqualFold(stringSetting(settings.Naming.Suffixes, "resolution_detect_method"), "Real") {
		if real := detectRealResolution(path); real != "" {
			flags.Resolution = real
		} else if !boolSetting(settings.Naming.Suffixes, "resolution_detect_fallback") {
			flags.Resolution = ""
		}
	}
	if override := settings.FlagsOverride; override != nil {
		if override.Clear {
			flags = filenameFlags{Mosaic: "is_censored"}
		}
		if override.Subtitle {
			flags.HasSubtitle, flags.SubtitleMarker = true, "手动标识"
		}
		if override.Uncensored {
			flags.Mosaic, flags.MosaicMarker = "is_uncensored", "手动标识"
		}
		if override.JAV {
			flags.Mosaic, flags.MosaicMarker = "is_jav_hack", "手动标识"
		}
		if override.Hack {
			flags.Mosaic, flags.MosaicMarker = "is_other_hack", "手动标识"
		}
		if override.LADA {
			flags.Mosaic, flags.MosaicMarker = "is_lada_hack", "手动标识"
		}
		if override.LTX {
			flags.Mosaic, flags.MosaicMarker = "is_ltx_hack", "手动标识"
		}
		if override.Resolution != "" {
			flags.Resolution = override.Resolution
		}
	}
	// 自定义规则只看文件名（去掉番号后的部分），不受手动标识覆盖影响。
	flags.Custom = matchCustomRules(version, settings.Custom.Rules)
	return flags
}
