package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func actorMappingDocument(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("请输入有效的 HTTP 或 HTTPS 地址")
	}
	if !strings.EqualFold(parsed.Hostname(), "github.com") {
		return parsed.String(), nil
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", errors.New("GitHub 地址需包含用户名和仓库名")
	}
	owner, repo := parts[0], strings.TrimSuffix(parts[1], ".git")
	branch, file := "main", "mapping_actor.xml"
	if len(parts) >= 4 && (parts[2] == "tree" || parts[2] == "blob") {
		branch = parts[3]
		if parts[2] == "blob" {
			if len(parts) < 5 {
				return "", errors.New("GitHub 文件地址缺少 XML 路径")
			}
			file = strings.Join(parts[4:], "/")
		}
	}
	document := &url.URL{Scheme: "https", Host: "raw.githubusercontent.com", Path: "/" + owner + "/" + repo + "/" + branch + "/" + file}
	return document.String(), nil
}

// verifyActorMapping 验证仓库地址：下载 mapping_actor.xml、检查格式，并保存为自定义表。
// 只有下载和保存都成功，前端才允许保存设置。
func (s *Server) verifyActorMapping(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var input struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		fail(w, 400, "invalid_body", "请提供演员映射地址")
		return
	}
	document, err := actorMappingDocument(input.URL)
	if err != nil {
		fail(w, 400, "invalid_url", err.Error())
		return
	}
	data, err := fetchActorMappingXML(r.Context(), document, s.githubToken())
	if err != nil {
		fail(w, 502, "verify_failed", err.Error())
		return
	}
	updated, err := saveCustomActorMapping(dataDirOf(s), data)
	if err != nil {
		fail(w, 500, "save_failed", "保存自定义演员映射表失败："+err.Error())
		return
	}
	if updated {
		go s.stopJobsForConfigChange("actor mapping verify")
	}
	writeJSON(w, 200, object{"valid": true, "document_url": document, "updated": updated})
}

// checkActorMapping 手动检查远端映射表是否有更新：有变化才下载并替换自定义表。
func (s *Server) checkActorMapping(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	var metadata struct {
		Repository string `json:"actor_mapping_repository"`
	}
	if raw := s.configs["metadata"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &metadata)
	}
	s.mu.RUnlock()
	if strings.TrimSpace(metadata.Repository) == "" {
		fail(w, 400, "no_repository", "请先填写并验证演员映射仓库地址")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	updated, err := s.updateCustomActorMapping(ctx, metadata.Repository)
	if err != nil {
		fail(w, 502, "check_failed", err.Error())
		return
	}
	if updated {
		go s.stopJobsForConfigChange("actor mapping check")
	}
	writeJSON(w, 200, object{"success": true, "updated": updated})
}
