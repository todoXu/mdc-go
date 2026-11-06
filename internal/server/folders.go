package server

import (
	"net/http"
	"os"
	"path/filepath"
)

type folderItem struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// listLocalFolders 列出程序所在电脑上的文件夹，供页面选择整理、监控等目录。
// 程序默认只监听 127.0.0.1；用 -listen 开放到局域网后，远程打开的页面也可以浏览，
// 因为能访问页面的人本来就能修改全部设置。这里只拦截其他网站发起的跨站请求。
func (s *Server) listLocalFolders(w http.ResponseWriter, r *http.Request) {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		fail(w, 403, "invalid_origin", "只能从本地页面浏览目录")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
		fail(w, 403, "invalid_origin", "只能从本地页面浏览目录")
		return
	}
	requested := r.URL.Query().Get("path")
	if len(requested) > 32767 {
		fail(w, 400, "invalid_path", "目录路径过长")
		return
	}
	if requested == "" {
		roots := []folderItem{}
		for _, drive := range availableRoots() {
			roots = append(roots, folderItem{Name: drive, Path: drive})
		}
		writeJSON(w, 200, object{"path": "", "parent": "", "folders": roots, "truncated": false})
		return
	}
	if !filepath.IsAbs(requested) {
		fail(w, 400, "invalid_path", "请输入完整目录路径")
		return
	}
	current := filepath.Clean(requested)
	info, err := os.Stat(current)
	if err != nil || !info.IsDir() {
		fail(w, 404, "folder_not_found", "目录不存在或无法访问；网络共享请检查路径、连接，以及运行程序的用户是否有权限")
		return
	}
	entries, err := os.ReadDir(current)
	if err != nil {
		fail(w, 403, "folder_unreadable", "没有权限读取此目录")
		return
	}
	folders := []folderItem{}
	truncated := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if len(folders) >= 2000 {
			truncated = true
			break
		}
		folders = append(folders, folderItem{Name: entry.Name(), Path: filepath.Join(current, entry.Name())})
	}
	parent := filepath.Dir(current)
	if parent == current {
		parent = ""
	}
	writeJSON(w, 200, object{"path": current, "parent": parent, "folders": folders, "truncated": truncated})
}
