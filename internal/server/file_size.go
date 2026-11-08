package server

import "math"

// minFileSizeBytes 把「文件大小过滤（MB）」换算成字节数，用于比较文件大小。四舍五入避免小数误差。
func minFileSizeBytes(mb float64) int64 {
	return int64(math.Round(mb * 1048576))
}
