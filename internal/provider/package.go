package provider

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/dop251/goja"
)

// InstallZIP accepts exactly plugin.json and main.js, either at archive root
// or under one matching plugin-ID directory. No other files are installed.
func (r *Registry) InstallZIP(data []byte) (Manifest, error) {
	if len(data) == 0 || len(data) > 2<<20 {
		return Manifest{}, issue("invalid_plugin", "插件 ZIP 不得超过 2 MB")
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Manifest{}, issue("invalid_plugin", "请选择有效的 ZIP 插件包")
	}
	files := map[string][]byte{}
	var folder string
	depth := 0
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		if entry.Mode()&os.ModeSymlink != 0 || strings.Contains(entry.Name, "\\") || strings.HasPrefix(entry.Name, "/") || path.Clean(entry.Name) != entry.Name {
			return Manifest{}, issue("invalid_plugin", "插件包包含非法路径")
		}
		parts := strings.Split(entry.Name, "/")
		if depth == 0 {
			depth = len(parts)
		}
		if len(parts) != depth {
			return Manifest{}, issue("invalid_plugin", "插件包目录结构无效")
		}
		if len(parts) == 2 {
			if folder == "" {
				folder = parts[0]
			}
			if parts[0] != folder {
				return Manifest{}, issue("invalid_plugin", "插件包只能包含一个插件目录")
			}
		} else if len(parts) != 1 || folder != "" {
			return Manifest{}, issue("invalid_plugin", "插件包目录结构无效")
		}
		name := parts[len(parts)-1]
		if name != "plugin.json" && name != "main.js" {
			return Manifest{}, issue("invalid_plugin", "插件包只能包含 plugin.json 和 main.js")
		}
		if _, ok := files[name]; ok {
			return Manifest{}, issue("invalid_plugin", "插件包包含重复文件")
		}
		limit := int64(1 << 20)
		if name == "plugin.json" {
			limit = 64 << 10
		}
		if entry.UncompressedSize64 > uint64(limit) {
			return Manifest{}, issue("invalid_plugin", "插件文件过大")
		}
		reader, err := entry.Open()
		if err != nil {
			return Manifest{}, issue("invalid_plugin", "无法读取插件包")
		}
		content, err := io.ReadAll(io.LimitReader(reader, limit+1))
		reader.Close()
		if err != nil || int64(len(content)) > limit {
			return Manifest{}, issue("invalid_plugin", "插件文件过大或损坏")
		}
		files[name] = content
	}
	if len(files) != 2 {
		return Manifest{}, issue("invalid_plugin", "插件包缺少 plugin.json 或 main.js")
	}
	var manifest Manifest
	if err := json.Unmarshal(files["plugin.json"], &manifest); err != nil || validate(manifest) != nil {
		return Manifest{}, issue("invalid_plugin", "插件清单格式或协议无效")
	}
	if folder != "" && folder != manifest.ID {
		return Manifest{}, issue("invalid_plugin", "插件目录与 ID 不一致")
	}
	if IsBuiltin(manifest.ID) {
		return Manifest{}, issue("builtin_plugin", "内置插件不能通过上传覆盖")
	}
	if _, err := goja.Compile("main.js", string(files["main.js"]), false); err != nil {
		return Manifest{}, issue("invalid_plugin", "插件脚本存在语法错误")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	target := filepath.Join(r.Dir, manifest.ID)
	if _, err := os.Lstat(target); err == nil {
		return Manifest{}, issue("plugin_exists", "同 ID 插件已安装，请先删除旧版本")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, err
	}
	temp, err := os.MkdirTemp(r.Dir, ".install-")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(temp)
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(temp, name), content, 0600); err != nil {
			return Manifest{}, err
		}
	}
	if err := os.Rename(temp, target); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (r *Registry) ExportZIP(id string) ([]byte, error) {
	if !idPattern.MatchString(id) {
		return nil, issue("provider_not_found", "没有安装这个数据源插件")
	}
	if _, err := r.Get(id); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for _, name := range []string{"plugin.json", "main.js"} {
		content, err := os.ReadFile(filepath.Join(r.Dir, id, name))
		if err != nil {
			return nil, err
		}
		writer, err := archive.Create(id + "/" + name)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write(content); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (r *Registry) Remove(id string) error {
	if !idPattern.MatchString(id) {
		return issue("provider_not_found", "没有安装这个数据源插件")
	}
	if IsBuiltin(id) {
		return issue("builtin_plugin", "内置插件不能删除")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	target := filepath.Join(r.Dir, id)
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return issue("provider_not_found", "没有安装这个数据源插件")
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return issue("invalid_plugin", "插件目录无效")
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("删除插件 %s: %w", id, err)
	}
	delete(r.last, id)
	return nil
}
