package server

import (
	"path/filepath"
	"regexp"
	"strings"
)

// cleanFileNameJunk 按「文件名垃圾信息过滤」列表删掉文件名中的内容，只用于识别，不改动原文件。
// 普通条目不区分大小写地整段删除；以 r: 开头的条目是正则。无效的正则会被跳过。
func cleanFileNameJunk(name string, junk []string) string {
	for _, entry := range junk {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		pattern := "(?i)" + regexp.QuoteMeta(entry)
		if strings.HasPrefix(entry, "r:") {
			raw := strings.TrimSpace(strings.TrimPrefix(entry, "r:"))
			if raw == "" {
				continue
			}
			pattern = "(?i)" + raw
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		name = re.ReplaceAllString(name, "")
	}
	return name
}

// pathWithoutJunk 返回文件名已去掉垃圾信息的路径，供番号和标记识别使用。
func pathWithoutJunk(path string, junk []string) string {
	base := cleanFileNameJunk(filepath.Base(path), junk)
	return filepath.Join(filepath.Dir(path), base)
}
