package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"mdcng-go/internal/server"
	"mdcng-go/internal/web"
)

// appDir 返回程序文件所在的目录，data 和 plugins 默认放在这里，从哪个目录启动都一样。
// go run 时程序在临时编译目录里，这时退回当前目录，方便开发。
func appDir() string {
	exe, err := os.Executable()
	if err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		dir := filepath.Dir(exe)
		if !strings.Contains(dir, "go-build") {
			return dir
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// fatal 记录错误后退出。错误等级总会被记录，不受日志等级设置影响。
func fatal(message string, args ...any) {
	slog.Error(message, args...)
	os.Exit(1)
}

func main() {
	addr := flag.String("listen", "0.0.0.0:19208", "HTTP listen address")
	base := appDir()
	data := flag.String("data", filepath.Join(base, "data"), "runtime data directory (config, database, logs); created on first start")
	plugins := flag.String("plugins", filepath.Join(base, "plugins"), "data source plugin directory; built-in plugins are written here on first start")
	assets := flag.String("assets", "", "development only: serve frontend files from this directory instead of the embedded copy")
	flag.Parse()
	// 日志按 system.log_level 过滤；读取配置之前使用默认等级。
	server.SetupLogging(os.Stderr)
	if *assets != "" {
		if info, err := os.Stat(*assets); err != nil || !info.IsDir() {
			fatal("[main] 前端文件目录不可用", "path", *assets, "error", err)
		}
		slog.Info("[main] 从磁盘读取前端文件", "path", *assets)
	}
	app, err := server.NewWithPlugins(*data, *plugins)
	if err != nil {
		fatal("[main] 启动失败", "error", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", app)
	mux.Handle("/healthz", app)
	mux.Handle("/", web.Handler(*assets))
	httpServer := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app.StartWatcher(ctx)
	// 每次启动时检查自定义演员映射表是否有更新；在后台进行，不阻塞启动。
	go app.RefreshActorMapping(ctx)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdown); err != nil {
			slog.Error("[main] 关闭 HTTP 服务失败", "error", err)
		}
	}()
	// 访问地址直接打印到控制台，不受日志等级影响，方便确认程序已启动。
	fmt.Fprintf(os.Stderr, "mdc-go %s listening on http://%s (data: %s)\n", server.Version, *addr, *data)
	slog.Info("[main] 服务已启动", "listen", *addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal("[main] HTTP 服务异常退出", "error", err)
	}
}
