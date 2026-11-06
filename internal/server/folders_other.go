//go:build !windows

package server

func availableRoots() []string { return []string{"/"} }
