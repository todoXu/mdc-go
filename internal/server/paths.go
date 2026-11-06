package server

import (
	"path/filepath"
	"runtime"
	"strings"
)

// pathsCaseInsensitive 表示当前系统的文件路径是否不区分大小写：Windows 不区分，Linux 区分。
var pathsCaseInsensitive = runtime.GOOS == "windows"

// pathKey 返回用于比较或去重的路径键：先规范化路径，Windows 下再统一为小写。
func pathKey(path string) string {
	clean := filepath.Clean(path)
	if pathsCaseInsensitive {
		return strings.ToLower(clean)
	}
	return clean
}

// samePath 判断两个路径是否指向同一个位置。相对路径按当前工作目录转为绝对路径后比较。
func samePath(a, b string) bool {
	if aa, err := filepath.Abs(a); err == nil {
		a = aa
	}
	if bb, err := filepath.Abs(b); err == nil {
		b = bb
	}
	return pathKey(a) == pathKey(b)
}
