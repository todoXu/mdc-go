package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var extraFanartSuffix = regexp.MustCompile(`^-extrafanart-[0-9]{3}\.(?:jpg|png|webp)$`)
var unprefixedImage = regexp.MustCompile(`^(poster|thumb|fanart)\.(?:original\.)?(?:jpg|png|webp)$`)
var unprefixedExtra = regexp.MustCompile(`^extrafanart-[0-9]{3}\.(?:jpg|png|webp)$`)
var unprefixedTrailer = regexp.MustCompile(`^trailer\.(?:mp4|webm)$`)

func removeLegacyMetadataFiles(paths ...string) {
	seen := map[string]bool{}
	for _, path := range paths {
		if path == "" {
			continue
		}
		target := strings.TrimSuffix(path, filepath.Ext(path)) + ".mdc.json"
		if seen[target] {
			continue
		}
		seen[target] = true
		_ = os.Remove(target)
	}
}

// Commit only after fetching, rendering and collision checks have succeeded.
// Keep the previous sidecars on the same volume until the video rename succeeds.
func commitScrapeRetry(ctx context.Context, path, output string, writes map[string][]byte) error {
	return commitScrapeRetryManagedNFO(ctx, path, output, writes, false, true)
}

func commitScrapeRetryManagedNFO(ctx context.Context, path, output string, writes map[string][]byte, managedNFO, imageWithPrefix bool) error {
	base := strings.TrimSuffix(path, filepath.Ext(path))
	newBase := strings.TrimSuffix(output, filepath.Ext(output))
	suffixes := []string{".mdc.json", ".nfo", "-trailer.mp4", "-trailer.webm"}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		suffix := strings.TrimPrefix(entry.Name(), filepath.Base(base))
		if extraFanartSuffix.MatchString(suffix) {
			suffixes = append(suffixes, suffix)
		}
	}
	for _, kind := range []string{"poster", "thumb", "fanart"} {
		for _, ext := range []string{".jpg", ".png", ".webp"} {
			suffixes = append(suffixes, "-"+kind+ext, "-"+kind+".original"+ext)
		}
	}
	existing := map[string]bool{}
	freshImages := map[string]bool{}
	freshExtras, freshTrailer := false, false
	freshImagesUnprefixed := map[string]bool{}
	freshExtrasUnprefixed, freshTrailerUnprefixed := false, false
	for target := range writes {
		suffix := strings.TrimPrefix(target, newBase)
		freshExtras = freshExtras || extraFanartSuffix.MatchString(suffix)
		freshTrailer = freshTrailer || suffix == "-trailer.mp4" || suffix == "-trailer.webm"
		if filepath.Clean(filepath.Dir(target)) == filepath.Clean(filepath.Dir(newBase)) {
			name := strings.ToLower(filepath.Base(target))
			if match := unprefixedImage.FindStringSubmatch(name); len(match) == 2 {
				freshImagesUnprefixed[match[1]] = true
			}
			freshExtrasUnprefixed = freshExtrasUnprefixed || unprefixedExtra.MatchString(name)
			freshTrailerUnprefixed = freshTrailerUnprefixed || unprefixedTrailer.MatchString(name)
		}
	}
	for _, kind := range []string{"poster", "thumb", "fanart"} {
		for _, ext := range []string{".jpg", ".png", ".webp"} {
			if _, ok := writes[newBase+"-"+kind+ext]; ok {
				freshImages[kind] = true
			}
		}
	}
	for _, suffix := range suffixes {
		source := base + suffix
		info, err := os.Lstat(source)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("伴随文件不是普通文件：%s", source)
		}
		existing[source] = true
		if suffix == ".mdc.json" {
			continue
		}
		// Retire legacy original copies after a successful commit. They are never
		// used for rendering; failures still restore every existing file.
		if strings.Contains(suffix, ".original.") {
			continue
		}
		// Preserve disabled/failed image downloads, but remove obsolete formats.
		replacedImage := false
		if freshExtras && extraFanartSuffix.MatchString(suffix) {
			replacedImage = true
		}
		if freshTrailer && (suffix == "-trailer.mp4" || suffix == "-trailer.webm") {
			replacedImage = true
		}
		if managedNFO && suffix == ".nfo" {
			replacedImage = true
		}
		for _, kind := range []string{"poster", "thumb", "fanart"} {
			if !strings.HasPrefix(suffix, "-"+kind+".") {
				continue
			}
			replacedImage = freshImages[kind]
		}
		if _, ok := writes[newBase+suffix]; !ok && !replacedImage {
			data, err := os.ReadFile(source)
			if err != nil {
				return err
			}
			writes[newBase+suffix] = data
		}
	}
	if !imageWithPrefix {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := strings.ToLower(entry.Name())
			replace := freshExtrasUnprefixed && unprefixedExtra.MatchString(name) ||
				freshTrailerUnprefixed && unprefixedTrailer.MatchString(name)
			if match := unprefixedImage.FindStringSubmatch(name); len(match) == 2 {
				replace = replace || freshImagesUnprefixed[match[1]]
			}
			if replace {
				existing[filepath.Join(filepath.Dir(path), entry.Name())] = true
			}
		}
	}
	for target := range writes {
		if _, err := os.Lstat(target); err == nil {
			if !existing[target] {
				return fmt.Errorf("目标文件已存在：%s", target)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if !strings.EqualFold(path, output) {
		if _, err := os.Lstat(output); err == nil {
			return fmt.Errorf("目标视频文件已存在：%s", output)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	backupDir, err := os.MkdirTemp(filepath.Dir(path), ".mdc-retry-*")
	if err != nil {
		return err
	}
	backups := map[string]string{}
	created := []string{}
	rollback := func(cause error) error {
		for _, target := range created {
			if err := os.Remove(target); err != nil {
				cause = errors.Join(cause, err)
			}
		}
		for source, backup := range backups {
			if err := os.Rename(backup, source); err != nil {
				cause = errors.Join(cause, fmt.Errorf("恢复 %s 失败，备份位于 %s：%w", source, backup, err))
			}
		}
		_ = os.Remove(backupDir) // Leave recoverable backups if restoration failed.
		return cause
	}
	for source := range existing {
		backup := filepath.Join(backupDir, filepath.Base(source))
		if err := os.Rename(source, backup); err != nil {
			return rollback(err)
		}
		backups[source] = backup
	}
	for target, content := range writes {
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return rollback(err)
		}
		created = append(created, target)
		_, err = file.Write(content)
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return rollback(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	if path != output {
		if err := os.Rename(path, output); err != nil {
			return rollback(err)
		}
	}
	if err := os.RemoveAll(backupDir); err != nil {
		traceScrape(ctx, "保存文件", "更新已完成，旧文件备份保留于："+backupDir)
	}
	traceScrape(ctx, "保存文件", "已更新元数据、图片及配套文件："+output)
	return nil
}
