package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LoadConfigs 读取每个已安装插件目录里的 config.json。没有这个文件的插件不会出现在结果中，
// 表示该插件使用默认配置（未启用）。
func (r *Registry) LoadConfigs() (map[string]Config, error) {
	entries, err := os.ReadDir(r.Dir)
	if err != nil {
		return nil, err
	}
	out := map[string]Config{}
	for _, entry := range entries {
		if !entry.IsDir() || !idPattern.MatchString(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.Dir, entry.Name(), "config.json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var c Config
		if err := json.Unmarshal(data, &c); err != nil {
			return nil, fmt.Errorf("插件 %s 的 config.json 格式错误: %w", entry.Name(), err)
		}
		if c.Options == nil {
			c.Options = map[string]any{}
		}
		out[entry.Name()] = c
	}
	return out, nil
}

// SaveConfig 只写入指定插件目录下的 config.json，不影响其他插件。
func (r *Registry) SaveConfig(id string, c Config) error {
	if !idPattern.MatchString(id) {
		return issue("provider_not_found", "没有安装这个数据源插件")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	dir := filepath.Join(r.Dir, id)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return issue("provider_not_found", "没有安装这个数据源插件")
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	temp := filepath.Join(dir, "config.json.tmp")
	if err := os.WriteFile(temp, append(data, '\n'), 0600); err != nil {
		return err
	}
	if err := os.Rename(temp, filepath.Join(dir, "config.json")); err != nil {
		os.Remove(temp)
		return err
	}
	return nil
}
