package server

import (
	"regexp"
	"strings"
)

// 番号识别的规则思路参考了 sqzw-x/amane（GPL-3.0，见 src/amane/parsing/file_info.py）：
// 先去掉画质、日期、分集等标记，再按顺序尝试各站点的具体规则，最后才用宽松规则。
// 这里是用 Go 重新实现的，没有复制它的代码。

// numberEscapeMarkers 是识别番号前要去掉的画质和编码标记，两侧必须是分隔符才会去掉。
// 不包含 XXX，否则会破坏 XXX-AV 这类番号。
var numberEscapeMarkers = []string{"4K", "4KS", "8K", "2160P", "1440P", "1080P", "720P", "480P", "HD", "LR", "VR", "DVD", "FULL", "HEVC", "H264", "H265", "X264", "X265", "AAC", "PRT"}

// surenLabels 是素人系标签对应的站内数字前缀。文件名常省略这段数字（LUXU-1700），补齐后与站点写法一致。
var surenLabels = [][2]string{{"LUXU-", "259"}, {"ARA-", "261"}, {"MIUM-", "300"}, {"NTK-", "300"}, {"MAAN-", "300"}}

var (
	numberEscapeRes     = buildNumberEscapeRes(numberEscapeMarkers)
	numberFC2PPVRe      = regexp.MustCompile(`FC2[-_]?PPV[-_]?`)
	numberBracketDateRe = regexp.MustCompile(`[-\[]\d{2}[-_.]\d{2}[-_.]\d{2}\]?`)
	numberDateRe        = regexp.MustCompile(`\d{4}[-_.]\d{1,2}[-_.]\d{1,2}`)
	numberCDRe          = regexp.MustCompile(`[-_. ](?:CD|PART)\d{0,2}([-_. ]|$)`)
	numberWesternRe     = regexp.MustCompile(`([A-Z0-9_-]{2,})[-_.]2?0?(\d{2}[-.]\d{2}[-.]\d{2})`)
)

// numberRule 是一条具体的站点或系列规则。when 为空表示总是尝试；format 把匹配结果转成番号。
type numberRule struct {
	re     *regexp.Regexp
	when   func(text string) bool
	format func(m []string) string
}

func newNumberRule(pattern string, format func([]string) string) numberRule {
	return numberRule{re: regexp.MustCompile(pattern), format: format}
}

func wholeMatch(m []string) string { return m[0] }

func firstGroup(m []string) string { return m[1] }

// withSuren 给省略了数字段的素人番号补上站内前缀，例如 LUXU-1700 → 259LUXU-1700。
func withSuren(m []string) string {
	for _, pair := range surenLabels {
		if strings.HasPrefix(m[0], pair[0]) {
			return pair[1] + m[0]
		}
	}
	return m[0]
}

// numberRules 按顺序尝试，越具体的规则越靠前。通用规则放在最后。
var numberRules = []numberRule{
	newNumberRule(`MMR-?[A-Z]{2,}-?\d+[A-Z]*`, func(m []string) string { return strings.Replace(m[0], "MMR-", "MMR", 1) }),
	newNumberRule(`CW3D2D?BD-?\d{2,}`, wholeMatch),
	// 国产 MD 系列；名字里有 MDVR 时是 VR 作品，不按 MD 处理。
	{re: regexp.MustCompile(`(?:^|[^A-Z])(MD[A-Z-]*\d{4,}(?:-\d)?)`), when: func(t string) bool { return !strings.Contains(t, "MDVR") }, format: firstGroup},
	newNumberRule(`XXX-AV-\d{4,}`, wholeMatch),
	newNumberRule(`MKY-[A-Z]+-\d{3,}`, wholeMatch),
	// FC2 保持与 filenameFC2Number 相同的输出格式。
	newNumberRule(`FC2-?(\d{5,})`, func(m []string) string { return "FC2-PPV-" + m[1] }),
	newNumberRule(`HEYZO-?(\d{3,})`, func(m []string) string { return "HEYZO-" + m[1] }),
	newNumberRule(`HEYDOUGA[-_. ]?(\d{2,4})[-_. ](\d{1,4})`, func(m []string) string { return "HEYDOUGA-" + m[1] + "-" + m[2] }),
	newNumberRule(`(?:H4610|C0930|H0930)-[A-Z]+\d{4,}`, wholeMatch),
	newNumberRule(`KIN8(?:TENGOKU)?-?(\d{3,})`, func(m []string) string { return "KIN8-" + m[1] }),
	newNumberRule(`S2M[BD]*-\d{3,}|MCB3D[BD]*-\d{2,}`, wholeMatch),
	newNumberRule(`TH101-\d{3,}-\d{5,}`, wholeMatch),
	// DMM 的 T 系列：T2800123 与 T28-123 都写成 T28-123。
	newNumberRule(`T(\d{2})(?:-|00)(\d{3})(?:\D|$)`, func(m []string) string { return "T" + m[1] + "-" + m[2] }),
	// DMM 连写：字母 + 00 + 三位数字，例如 AARM00372 → AARM-372。
	newNumberRule(`([A-Z]{2,})00(\d{3})`, func(m []string) string { return m[1] + "-" + m[2] }),
	// 素人番号：数字在前，例如 259LUXU-1700。
	newNumberRule(`\d{2,}[A-Z]{2,}-\d{2,}[A-Z]?`, wholeMatch),
	newNumberRule(`[A-Z]{2,10}-\d{2,}Z?`, withSuren),
	// 六位数字加序号，例如 010115-001。
	newNumberRule(`(?:^|[^0-9])(\d{6}[-_]\d{2,4})(?:[^0-9]|$)`, firstGroup),
	// 前缀里带数字，例如 R18-292。
	newNumberRule(`[A-Z]{1,4}\d{1,4}-\d{2,4}[A-Z]?`, wholeMatch),
	// 东京热 n 系列与 k 系列，例如 n1234、k1363，统一为小写。
	newNumberRule(`(?:^|[^A-Z])([NK]\d{4})(?:\D|$)`, func(m []string) string { return strings.ToLower(m[1]) }),
	newNumberRule(`H_\d{3,}([A-Z]{2,})(\d{2,})`, func(m []string) string { return m[1] + "-" + m[2] }),
}

func buildNumberEscapeRes(markers []string) []*regexp.Regexp {
	res := make([]*regexp.Regexp, 0, len(markers))
	for _, marker := range markers {
		res = append(res, regexp.MustCompile(`[-_ .\[]`+regexp.QuoteMeta(marker)+`[-_ .\]]`))
	}
	return res
}

// prepareNumberText 去掉画质标记、日期和分集标记，让番号规则只看番号本身。
func prepareNumberText(upper string) string {
	c := numberFC2PPVRe.ReplaceAllString(upper, "FC2-")
	for _, re := range numberEscapeRes {
		c = re.ReplaceAllString(c, "-")
	}
	c = numberBracketDateRe.ReplaceAllString(c, "-")
	c = numberDateRe.ReplaceAllString(c, "-")
	c = numberCDRe.ReplaceAllString(c, "-$1")
	return c
}

// numberFromName 只用具体规则从一个文件名或目录名中识别番号，不使用宽松规则。没有命中返回空字符串。
func numberFromName(name string) string {
	upper := strings.ToUpper(name)
	// 欧美作品：工作室.YY.MM.DD，例如 Vixen.24.01.02。必须在去掉日期之前判断。
	if m := numberWesternRe.FindStringSubmatch(upper); m != nil {
		return strings.Trim(m[1], "-_") + "." + strings.ReplaceAll(m[2], "-", ".")
	}
	text := prepareNumberText(upper)
	for _, rule := range numberRules {
		if rule.when != nil && !rule.when(text) {
			continue
		}
		if m := rule.re.FindStringSubmatch(text); m != nil {
			return rule.format(m)
		}
	}
	return ""
}
