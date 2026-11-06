package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type embyLibrary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

func (s *Server) listEmbyLibraries(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	config := append([]byte(nil), s.configs["actress"]...)
	s.mu.RUnlock()
	var actress struct {
		Emby struct {
			Address string `json:"server_address"`
			Key     string `json:"api_key"`
			UserID  string `json:"user_id"`
		} `json:"emby_config"`
	}
	if err := json.Unmarshal(config, &actress); err != nil {
		fail(w, 500, "invalid_emby_config", "Emby 配置无法读取")
		return
	}
	emby := actress.Emby
	if strings.TrimSpace(emby.Address) == "" || strings.TrimSpace(emby.Key) == "" {
		fail(w, 400, "emby_not_configured", "请先保存 Emby 服务器地址和 API Key")
		return
	}
	base, err := url.Parse(strings.TrimSpace(emby.Address))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		fail(w, 400, "invalid_emby_address", "Emby 服务器地址无效")
		return
	}
	apiPath := "/Library/VirtualFolders/Query"
	if emby.UserID != "" {
		apiPath = "/Users/" + url.PathEscape(emby.UserID) + "/Views"
	}
	base.Path = strings.TrimRight(base.Path, "/") + apiPath
	base.RawQuery = ""
	base.Fragment = ""
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, base.String(), nil)
	if err != nil {
		fail(w, 400, "invalid_emby_address", "Emby 服务器地址无效")
		return
	}
	request.Header.Set("X-Emby-Token", emby.Key)
	request.Header.Set("Accept", "application/json")
	client := &http.Client{
		Timeout:       8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		fail(w, 502, "emby_unavailable", "无法连接 Emby 服务器")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		fail(w, 502, "emby_response_error", fmt.Sprintf("Emby 返回状态 %d，请检查地址、API Key 和用户 ID", response.StatusCode))
		return
	}
	var payload struct {
		Items []struct {
			ID             string `json:"Id"`
			ItemID         string `json:"ItemId"`
			Name           string `json:"Name"`
			CollectionType string `json:"CollectionType"`
		} `json:"Items"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload); err != nil {
		fail(w, 502, "invalid_emby_response", "无法解析 Emby 媒体库列表")
		return
	}
	libraries := make([]embyLibrary, 0, len(payload.Items))
	for _, item := range payload.Items {
		id := item.ItemID
		if id == "" {
			id = item.ID
		}
		if id != "" && item.Name != "" {
			libraries = append(libraries, embyLibrary{ID: id, Name: item.Name, Type: item.CollectionType})
		}
	}
	writeJSON(w, 200, object{"data": libraries})
}
