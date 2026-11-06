//go:build windows

package server

import "syscall"

func availableRoots() []string {
	mask, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetLogicalDrives").Call()
	roots := []string{}
	for i := 0; i < 26; i++ {
		if mask&(1<<i) != 0 {
			roots = append(roots, string(rune('A'+i))+":\\")
		}
	}
	return roots
}
