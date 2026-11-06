package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed assets/*
var content embed.FS

// WatermarkAsset returns the same built-in badge used by the settings preview.
func WatermarkAsset(name string) ([]byte, error) {
	return content.ReadFile("assets/watermarks/" + name)
}

// Handler serves the editable frontend source embedded in the Windows executable.
// 开发时传入 dir（如 internal/web/assets）则直接读取磁盘文件，修改前端后刷新页面即可生效。
func Handler(dir string) http.Handler {
	var files http.FileSystem
	if dir != "" {
		files = http.Dir(dir)
	} else {
		sub, err := fs.Sub(content, "assets")
		if err != nil {
			panic(err)
		}
		files = http.FS(sub)
	}
	server := http.FileServer(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 每次向服务确认是否有更新，避免升级或修改后浏览器继续使用旧脚本。
		w.Header().Set("Cache-Control", "no-cache")
		server.ServeHTTP(w, r)
	})
}
