package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// logCapacity 是内存中保留的最近日志条数，超出后丢弃最早的记录。
const logCapacity = 5000

// 日志等级。配置文件 system.log_level 和页面使用这些名字；低于设定等级的日志直接丢弃。
var logLevelNames = map[string]slog.Level{
	"debug":   slog.LevelDebug,
	"info":    slog.LevelInfo,
	"warning": slog.LevelWarn,
	"error":   slog.LevelError,
}

// defaultLogLevel 与 defaults.json 中的 system.log_level 一致，读取配置前也按这个等级记录。
const defaultLogLevel = "warning"

// logLevel 是当前生效的日志等级，修改后立即对所有新日志生效。
var logLevel = func() *slog.LevelVar {
	level := new(slog.LevelVar)
	level.Set(logLevelNames[defaultLogLevel])
	return level
}()

// setLogLevel 按配置中的名字设置日志等级；名字无效时返回 false，等级保持不变。
func setLogLevel(name string) bool {
	level, ok := logLevelNames[name]
	if ok {
		logLevel.Set(level)
	}
	return ok
}

// levelName 返回日志条目和页面使用的等级名。
func levelName(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return "error"
	case level >= slog.LevelWarn:
		return "warning"
	case level >= slog.LevelInfo:
		return "info"
	default:
		return "debug"
	}
}

type logEntry struct {
	Seq     int64  `json:"seq"`
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// logBuffer 保存最近若干条日志，供页面查看和导出。
// 开启「写入本地文件」后，所有新日志还会追加到文件，不受内存条数限制。
type logBuffer struct {
	mu       sync.Mutex
	out      io.Writer // 控制台输出，通常是 os.Stderr
	entries  []logEntry
	next     int64
	sink     *os.File
	sinkPath string
}

var capturedLogs = &logBuffer{}

// SetupLogging 把 slog 的默认日志器设为本程序的日志处理器：日志同时写到 out（控制台）、
// 内存缓冲区和本地日志文件，并按 system.log_level 过滤。
// 标准 log 包的输出（包括第三方库）也会转到这里，按 info 等级记录。
func SetupLogging(out io.Writer) {
	capturedLogs.mu.Lock()
	capturedLogs.out = out
	capturedLogs.mu.Unlock()
	slog.SetDefault(slog.New(&logHandler{buffer: capturedLogs}))
}

// add 记录一条已格式化的日志。
func (b *logBuffer) add(when time.Time, level slog.Level, line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	entry := logEntry{Seq: b.next, Time: when.Format(time.RFC3339Nano), Level: levelName(level), Message: line}
	if len(b.entries) < logCapacity {
		b.entries = append(b.entries, entry)
	} else {
		b.entries[(b.next-1)%logCapacity] = entry
	}
	// 写入失败时不能再记日志，否则会递归；这里只能忽略错误。
	if b.out != nil {
		_, _ = fmt.Fprintln(b.out, line)
	}
	if b.sink != nil {
		_, _ = fmt.Fprintln(b.sink, line)
	}
}

// logHandler 是 slog.Handler，把每条日志格式化为一行：
// 2026/11/07 01:24:06 WARN  [watcher] message key=value key="带空格的值"
type logHandler struct {
	buffer *logBuffer
	attrs  []slog.Attr // WithAttrs 预先附加的字段，键已带上分组前缀
	group  string      // WithGroup 的分组前缀，形如 "a.b."
}

func (h *logHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= logLevel.Level()
}

func (h *logHandler) Handle(_ context.Context, record slog.Record) error {
	var line strings.Builder
	when := record.Time
	if when.IsZero() {
		when = time.Now()
	}
	line.WriteString(when.Format("2006/01/02 15:04:05 "))
	line.WriteString(map[string]string{"debug": "DEBUG", "info": "INFO ", "warning": "WARN ", "error": "ERROR"}[levelName(record.Level)])
	line.WriteByte(' ')
	line.WriteString(record.Message)
	for _, attr := range h.attrs {
		writeLogAttr(&line, "", attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		writeLogAttr(&line, h.group, attr)
		return true
	})
	h.buffer.add(when, record.Level, strings.TrimRight(line.String(), "\r\n"))
	return nil
}

func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := &logHandler{buffer: h.buffer, group: h.group, attrs: append([]slog.Attr(nil), h.attrs...)}
	for _, attr := range attrs {
		attr.Key = h.group + attr.Key
		next.attrs = append(next.attrs, attr)
	}
	return next
}

func (h *logHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &logHandler{buffer: h.buffer, attrs: h.attrs, group: h.group + name + "."}
}

// writeLogAttr 以 key=value 追加一个字段；值含空格、引号或控制字符时加引号转义。
func writeLogAttr(line *strings.Builder, prefix string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	if attr.Value.Kind() == slog.KindGroup {
		for _, child := range attr.Value.Group() {
			writeLogAttr(line, prefix+attr.Key+".", child)
		}
		return
	}
	value := attr.Value.String()
	if attr.Value.Kind() == slog.KindDuration {
		value = attr.Value.Duration().String()
	}
	needQuote := value == ""
	for _, r := range value {
		if unicode.IsSpace(r) || r == '"' || r == '=' || !unicode.IsPrint(r) {
			needQuote = true
			break
		}
	}
	if needQuote {
		value = strconv.Quote(value)
	}
	line.WriteByte(' ')
	line.WriteString(prefix + attr.Key)
	line.WriteByte('=')
	line.WriteString(value)
}

// orderedLocked 必须在持有锁时调用，按时间从早到晚返回当前保存的全部条目。
func (b *logBuffer) orderedLocked() []logEntry {
	if len(b.entries) < logCapacity {
		// 没有日志时返回空切片而不是 nil，接口输出 [] 而不是 null。
		return append([]logEntry{}, b.entries...)
	}
	start := int(b.next % logCapacity)
	out := make([]logEntry, 0, logCapacity)
	out = append(out, b.entries[start:]...)
	out = append(out, b.entries[:start]...)
	return out
}

func (b *logBuffer) ordered() []logEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.orderedLocked()
}

// tail 返回最近的 limit 条（limit 不大于 0 时返回全部）。
func (b *logBuffer) tail(limit int) []logEntry {
	all := b.ordered()
	if limit > 0 && len(all) > limit {
		return all[len(all)-limit:]
	}
	return all
}

// startFile 创建日志文件，先写入内存中已有的日志，之后的新日志持续追加。
func (b *logBuffer) startFile(path string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sink != nil {
		return errors.New("已经在写入日志文件")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	for _, entry := range b.orderedLocked() {
		if _, err := fmt.Fprintln(file, entry.Message); err != nil {
			file.Close()
			return err
		}
	}
	b.sink, b.sinkPath = file, path
	return nil
}

func (b *logBuffer) stopFile() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sink == nil {
		return nil
	}
	err := b.sink.Close()
	b.sink, b.sinkPath = nil, ""
	return err
}

func (b *logBuffer) fileStatus() (bool, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sink != nil, b.sinkPath
}

func (s *Server) logDirectory() string {
	return filepath.Join(filepath.Dir(s.statePath), "logs")
}

func (s *Server) getSystemLogs(w http.ResponseWriter, r *http.Request) {
	limit := 500
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > logCapacity {
			fail(w, 400, "invalid_limit", "limit 须在 1 到 5000 之间")
			return
		}
		limit = value
	}
	entries := capturedLogs.tail(limit)
	enabled, path := capturedLogs.fileStatus()
	writeJSON(w, 200, object{
		"data": entries, "total_count": len(capturedLogs.ordered()), "capacity": logCapacity,
		"file":  object{"enabled": enabled, "path": path},
		"level": levelName(logLevel.Level()),
	})
}

// setSystemLogFile 打开或关闭「写入本地文件」。文件保存在数据目录的 logs 文件夹下。
func (s *Server) setSystemLogFile(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&request); err != nil {
		fail(w, 400, "invalid_request", "请求内容无效")
		return
	}
	if request.Enabled {
		name := "mdc-go-" + time.Now().Format("20060102-150405") + ".log"
		if err := capturedLogs.startFile(filepath.Join(s.logDirectory(), name)); err != nil {
			fail(w, 500, "log_file_failed", "无法写入日志文件："+err.Error())
			return
		}
	} else if err := capturedLogs.stopFile(); err != nil {
		fail(w, 500, "log_file_failed", "关闭日志文件失败："+err.Error())
		return
	}
	enabled, path := capturedLogs.fileStatus()
	writeJSON(w, 200, object{"success": true, "enabled": enabled, "path": path})
}

func (s *Server) exportSystemLogs(w http.ResponseWriter, r *http.Request) {
	name := "mdc-go-" + time.Now().Format("20060102-150405") + ".log"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	for _, entry := range capturedLogs.ordered() {
		fmt.Fprintln(w, entry.Message)
	}
}
