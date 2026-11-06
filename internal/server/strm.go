package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// 整理模式编号：2 移动，3 原地整理，4 STRM。
const (
	linkModeMove    = 2
	linkModeInPlace = 3
	linkModeSTRM    = 4
)

var linkModeNames = map[int]string{linkModeMove: "移动", linkModeInPlace: "原地整理", linkModeSTRM: "STRM"}

func linkModeName(mode int) string {
	if name, ok := linkModeNames[mode]; ok {
		return name
	}
	return fmt.Sprintf("模式 %d", mode)
}

// linkModeChangeError 检查已成功整理的记录能否按当前设置再次处理（重新刮削、重新整理）。
// 整理方式改变后，文件已经按旧方式放好（例如移动后视频已在整理目录，再改成 STRM 会让
// .strm 指向整理目录里的视频），所以只要方式不同就拒绝。未成功的记录还没整理，不受限制。
func linkModeChangeError(job scrapeJob, current int) error {
	if job.Status != 2 || job.LinkMode == 0 || job.LinkMode == current {
		return nil
	}
	return fmt.Errorf("这条记录已按「%s」整理，当前设置是「%s」，整理方式不同，不能再次整理。"+
		"请把监控目录的整理方式改回「%s」；如果确实要换方式，请先删除这条记录（可同时删除媒体数据），把视频放回监控目录后重新扫描",
		linkModeName(job.LinkMode), linkModeName(current), linkModeName(job.LinkMode))
}

// supportedLinkMode 表示该模式已经实现，可用于手动刮削、重新整理和监控目录。
func supportedLinkMode(mode int) bool {
	return mode == linkModeMove || mode == linkModeInPlace || mode == linkModeSTRM
}

// usesTargetFolder 表示输出文件写入整理目录（移动与 STRM），需要完整的目标目录。
func usesTargetFolder(mode int) bool {
	return mode == linkModeMove || mode == linkModeSTRM
}

// strmContent 返回 .strm 文件的内容：源视频路径，经过可选的前缀映射。
func strmContent(source string, settings scrapeSettings) string {
	return applyStrmPathMap(source, settings.Common.StrmPathFrom, settings.Common.StrmPathTo)
}

// applyStrmPathMap 把 from 前缀替换为 to。任一项为空或不匹配时原样返回。
// 只在目录边界匹配：from 为 /data/av 时，/data/av2/x.mkv 不会被替换。
// Windows 下前缀比较不区分大小写。输出分隔符跟随 to 的风格。
func applyStrmPathMap(source, from, to string) string {
	from = strings.TrimRight(strings.TrimSpace(from), `\/`)
	to = strings.TrimRight(strings.TrimSpace(to), `\/`)
	if from == "" || to == "" || len(source) < len(from) {
		return source
	}
	head := source[:len(from)]
	if pathsCaseInsensitive {
		if !strings.EqualFold(head, from) {
			return source
		}
	} else if head != from {
		return source
	}
	rest := source[len(from):]
	if rest != "" && rest[0] != '\\' && rest[0] != '/' {
		return source
	}
	separator := "/"
	if strings.Contains(to, `\`) {
		separator = `\`
	}
	rest = strings.TrimLeft(rest, `\/`)
	rest = strings.ReplaceAll(rest, `\`, "/")
	if separator == `\` {
		rest = strings.ReplaceAll(rest, "/", `\`)
	}
	if rest == "" {
		return to
	}
	return to + separator + rest
}

// commitStrm 在整理目录写入 .strm 文件和配套文件（NFO、图片）。
// 源视频不会被移动、重命名或删除，字幕也不处理。目标已存在的同名文件会被覆盖；
// 覆盖前先备份，任一步失败都会恢复原状。
func commitStrm(ctx context.Context, source, target string, writes map[string][]byte, settings scrapeSettings) error {
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("源文件不存在：%s", source)
	}
	targetDir := filepath.Dir(target)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}
	files := make(map[string][]byte, len(writes)+1)
	for path, data := range writes {
		files[path] = data
	}
	files[target] = []byte(strmContent(source, settings))

	// 目标已存在时，按设置决定是否覆盖：.strm 和 NFO 归「视频」，其它是图片。关闭覆盖时直接停止，不写入任何文件。
	for path := range files {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		overwrite := settings.Common.TargetOverwritePic
		if path == target || strings.HasSuffix(strings.ToLower(path), ".nfo") {
			overwrite = settings.Common.TargetOverwriteVideo
		}
		if !overwrite {
			return fmt.Errorf("目标已存在且未开启覆盖：%s", path)
		}
	}

	backupDir, err := os.MkdirTemp(targetDir, ".mdc-strm-*")
	if err != nil {
		return err
	}
	backups := map[string]string{}
	created := []string{}
	rollback := func(cause error) error {
		for _, path := range created {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				cause = errors.Join(cause, err)
			}
		}
		restored := true
		for path, backup := range backups {
			if err := os.Rename(backup, path); err != nil {
				restored = false
				cause = errors.Join(cause, fmt.Errorf("恢复 %s 失败，备份位于 %s：%w", path, backup, err))
			}
		}
		// 恢复失败时保留备份目录，方便手动找回文件。
		if restored {
			_ = os.RemoveAll(backupDir)
		}
		return cause
	}

	index := 0
	for path := range files {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return rollback(err)
		}
		if !info.Mode().IsRegular() {
			return rollback(fmt.Errorf("目标路径不是普通文件：%s", path))
		}
		index++
		backup := filepath.Join(backupDir, fmt.Sprintf("%04d-%s", index, filepath.Base(path)))
		if err := os.Rename(path, backup); err != nil {
			return rollback(err)
		}
		backups[path] = backup
	}
	for path, data := range files {
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			created = append(created, path)
			return rollback(err)
		}
		created = append(created, path)
	}
	if err := os.RemoveAll(backupDir); err != nil {
		slog.Warn("[strm] 备份目录未能删除，已保留", "path", backupDir, "error", err)
	}
	traceScrape(ctx, "保存文件", "已写入 STRM："+target)
	return nil
}
