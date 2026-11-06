package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"mdcng-go/internal/provider"
)

func mediaNamingValues(meta provider.Metadata, settings scrapeSettings) map[string]string {
	meta = mapMetadataActors(meta, settings)
	limit := settings.Naming.MaxActor
	if limit <= 0 {
		limit = 3
	}
	actor := strings.Join(meta.Actors, "、")
	if len(meta.Actors) > limit {
		actor = "多人作品"
	}
	firstActor := ""
	if len(meta.Actors) > 0 {
		firstActor = meta.Actors[0]
	}
	parts := strings.Split(meta.Number, "-")
	seriesName, serialNumber := "", ""
	if len(parts) > 0 {
		seriesName = parts[0]
		serialNumber = parts[len(parts)-1]
	}
	year := ""
	if len(meta.Release) >= 4 {
		year = meta.Release[:4]
	}
	return map[string]string{
		"number": meta.Number, "title": meta.Title, "actor": actor, "first_actor": firstActor,
		"studio": meta.Studio, "publisher": meta.Publisher, "series": meta.Series,
		"year": year, "series_name": seriesName, "serial_number": serialNumber,
	}
}

func applyNamingTemplate(template string, values map[string]string) string {
	return namingVariable.ReplaceAllStringFunc(template, func(token string) string {
		return values[token[1:len(token)-1]]
	})
}

func buildMediaFolder(meta provider.Metadata, settings scrapeSettings) (string, error) {
	folder := strings.TrimSpace(applyNamingTemplate(settings.Naming.Folder, mediaNamingValues(meta, settings)))
	if folder == "" {
		return "", nil
	}
	folder = strings.ReplaceAll(folder, "/", string(filepath.Separator))
	if filepath.IsAbs(folder) || filepath.VolumeName(folder) != "" {
		return "", errors.New("目录命名规则不能生成绝对路径")
	}
	segments := []string{}
	for _, raw := range strings.Split(folder, string(filepath.Separator)) {
		segment := strings.TrimSpace(raw)
		if segment == "" {
			continue
		}
		if segment == "." || segment == ".." || badFilename.MatchString(segment) || reservedWindowsName(segment) {
			return "", fmt.Errorf("目录命名规则生成了非法目录：%q", segment)
		}
		if limit := settings.Naming.MaxFolderLength; limit > 0 && utf8.RuneCountInString(segment) > limit {
			segment = string([]rune(segment)[:limit])
		}
		segments = append(segments, segment)
	}
	return filepath.Join(segments...), nil
}

// organizationTarget 返回整理后的完整路径，并在需要时创建整理目录。真正写入文件前使用它。
func organizationTarget(path string, meta provider.Metadata, settings scrapeSettings) (string, error) {
	if usesTargetFolder(settings.Common.LinkMode) {
		if root := strings.TrimSpace(settings.Common.TargetFolder); root != "" && filepath.IsAbs(root) {
			if err := os.MkdirAll(root, 0755); err != nil {
				return "", fmt.Errorf("创建整理目录失败：%w", err)
			}
		}
	}
	return plannedMediaTarget(path, meta, settings)
}

// plannedMediaTarget 只计算整理后的完整路径，不创建任何目录。删除记录、保存前检查冲突时使用。
func plannedMediaTarget(path string, meta provider.Metadata, settings scrapeSettings) (string, error) {
	stem, err := buildVideoStem(path, meta, settings)
	if err != nil {
		return "", err
	}
	mode := settings.Common.LinkMode
	if mode == linkModeInPlace {
		return filepath.Join(filepath.Dir(path), stem+filepath.Ext(path)), nil
	}
	if !usesTargetFolder(mode) {
		return "", errors.New("当前只支持移动、原地整理和 STRM 模式")
	}
	root := strings.TrimSpace(settings.Common.TargetFolder)
	if root == "" || !filepath.IsAbs(root) {
		return "", errors.New("整理目录需要设置完整路径")
	}
	folder, err := buildMediaFolder(meta, settings)
	if err != nil {
		return "", err
	}
	// STRM 模式输出 .strm 文件，源视频保持原样。
	ext := filepath.Ext(path)
	if mode == linkModeSTRM {
		ext = ".strm"
	}
	return filepath.Join(root, folder, stem+ext), nil
}

type moveEntry struct {
	source    string
	target    string
	overwrite bool
}

type movedFile struct {
	source string
	target string
}

func subtitleMoves(source, targetBase string) ([]moveEntry, error) {
	sourceBase := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	entries, err := os.ReadDir(filepath.Dir(source))
	if err != nil {
		return nil, err
	}
	moves := []moveEntry{}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if !retrySubtitleExtensions[extension] {
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if !strings.EqualFold(stem, sourceBase) && !strings.HasPrefix(strings.ToLower(stem), strings.ToLower(sourceBase)+".") {
			continue
		}
		suffix := stem[len(sourceBase):]
		moves = append(moves, moveEntry{
			source: filepath.Join(filepath.Dir(source), entry.Name()),
			target: targetBase + suffix + filepath.Ext(entry.Name()),
		})
	}
	return moves, nil
}

func moveFile(source, target string) error {
	if samePath(source, target) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	if err := os.Rename(source, target); err == nil {
		return nil
	}
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	temp, err := os.CreateTemp(filepath.Dir(target), ".mdc-move-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	success := false
	defer func() {
		temp.Close()
		if !success {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := io.Copy(temp, input); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Chtimes(tempPath, info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	if err := os.Rename(tempPath, target); err != nil {
		return err
	}
	success = true
	return os.Remove(source)
}

func removeMovedSourceProducts(source string, settings scrapeSettings) {
	base := strings.TrimSuffix(source, filepath.Ext(source))
	entries, err := os.ReadDir(filepath.Dir(source))
	if err != nil {
		return
	}
	known := map[string]bool{".nfo": true, ".mdc.json": true, "-trailer.mp4": true, "-trailer.webm": true}
	for _, kind := range []string{"poster", "thumb", "fanart"} {
		for _, extension := range []string{".jpg", ".png", ".webp"} {
			known["-"+kind+extension] = true
			known["-"+kind+".original"+extension] = true
		}
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		target := filepath.Join(filepath.Dir(source), entry.Name())
		if samePath(target, source) || retrySubtitleExtensions[strings.ToLower(filepath.Ext(entry.Name()))] {
			continue
		}
		name := strings.ToLower(entry.Name())
		suffix := strings.TrimPrefix(name, strings.ToLower(filepath.Base(base)))
		remove := known[suffix] || extraFanartSuffix.MatchString(suffix)
		if !settings.Naming.ImageWithPrefix {
			remove = remove || unprefixedImage.MatchString(name) || unprefixedExtra.MatchString(name) || unprefixedTrailer.MatchString(name)
		}
		if remove {
			if err := os.Remove(target); err != nil {
				slog.Warn("[move] 删除源目录中的旧产物失败", "path", target, "error", err)
			}
		}
	}
}

func commitMove(ctx context.Context, source, target string, writes map[string][]byte, settings scrapeSettings) error {
	targetDir := filepath.Dir(target)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}
	targetBase := strings.TrimSuffix(target, filepath.Ext(target))
	moves := []moveEntry{{source: source, target: target, overwrite: settings.Common.TargetOverwriteVideo}}
	subtitles, err := subtitleMoves(source, targetBase)
	if err != nil {
		return err
	}
	for _, subtitle := range subtitles {
		subtitle.overwrite = settings.Common.TargetOverwriteVideo
		moves = append(moves, subtitle)
	}
	for path := range writes {
		overwrite := settings.Common.TargetOverwritePic
		if strings.HasSuffix(strings.ToLower(path), ".nfo") {
			overwrite = settings.Common.TargetOverwriteVideo
		}
		moves = append(moves, moveEntry{target: path, overwrite: overwrite})
	}

	targets := map[string]bool{}
	for _, move := range moves {
		key := pathKey(move.target)
		if targets[key] {
			return fmt.Errorf("目标路径重复：%s", move.target)
		}
		targets[key] = true
		if move.source != "" {
			if _, err := os.Stat(move.source); err != nil {
				return fmt.Errorf("源文件不存在：%s", move.source)
			}
		}
		if _, err := os.Lstat(move.target); err == nil && !samePath(move.source, move.target) && !move.overwrite {
			return fmt.Errorf("目标文件已存在：%s", move.target)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	backupDir, err := os.MkdirTemp(targetDir, ".mdc-move-*")
	if err != nil {
		return err
	}
	backups := map[string]string{}
	created := []string{}
	moved := []movedFile{}
	rollback := func(cause error) error {
		for i := len(moved) - 1; i >= 0; i-- {
			if err := moveFile(moved[i].target, moved[i].source); err != nil {
				cause = errors.Join(cause, fmt.Errorf("恢复源文件 %s 失败：%w", moved[i].source, err))
			}
		}
		for _, path := range created {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				cause = errors.Join(cause, err)
			}
		}
		for target, backup := range backups {
			if err := os.Rename(backup, target); err != nil {
				cause = errors.Join(cause, fmt.Errorf("恢复目标文件 %s 失败：%w", target, err))
			}
		}
		_ = os.RemoveAll(backupDir)
		if settings.Common.DeleteMetadataFolderWhenFailed {
			_ = os.Remove(targetDir)
		}
		return cause
	}

	for index, move := range moves {
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}
		if samePath(move.source, move.target) {
			continue
		}
		if _, err := os.Lstat(move.target); err == nil {
			backup := filepath.Join(backupDir, fmt.Sprintf("%04d-%s", index, filepath.Base(move.target)))
			if err := os.Rename(move.target, backup); err != nil {
				return rollback(err)
			}
			backups[move.target] = backup
		} else if !errors.Is(err, os.ErrNotExist) {
			return rollback(err)
		}
	}
	for target, data := range writes {
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}
		if err := os.WriteFile(target, data, 0644); err != nil {
			return rollback(err)
		}
		created = append(created, target)
	}
	for _, move := range moves {
		if move.source == "" || samePath(move.source, move.target) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}
		if err := moveFile(move.source, move.target); err != nil {
			return rollback(err)
		}
		moved = append(moved, movedFile{source: move.source, target: move.target})
	}
	if err := os.RemoveAll(backupDir); err != nil {
		traceScrape(ctx, "保存文件", "移动已完成，旧目标文件备份保留于："+backupDir)
	}
	sourceDir, targetDirectory := filepath.Dir(source), filepath.Dir(target)
	// When the move stays in the same directory, the generated image/NFO
	// files are already in their final location. Do not run source-product
	// cleanup here: for unprefixed naming it would delete the freshly written
	// thumb.jpg/fanart.jpg files.
	if !samePath(source, target) && !samePath(filepath.Dir(source), filepath.Dir(target)) {
		removeMovedSourceProducts(source, settings)
	}
	if !samePath(sourceDir, targetDirectory) {
		if settings.Common.DeleteEmptyParentAfterMove {
			if err := os.Remove(sourceDir); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Warn("[move] 删除空的源目录失败", "path", sourceDir, "error", err)
			}
		}
	}
	return nil
}
