package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// 演员管理：演员、头像、简介、关联影片都来自 Emby；修改头像后上传回 Emby。
// 名字按映射表显示，头像候选来自 Gfriends，写真来自 Jalbum（见 actor_gallery.go）。

// embyConn 是「设置 → 头像」里保存的 Emby 连接。
type embyConn struct {
	base      *url.URL
	key       string
	libraries []string
}

var errEmbyNotConfigured = errors.New("请先在「设置 → 头像」填写 Emby 服务器地址和 API Key")

func (s *Server) embyConnection() (embyConn, error) {
	s.mu.RLock()
	raw := append([]byte(nil), s.configs["actress"]...)
	s.mu.RUnlock()
	var actress struct {
		Emby struct {
			Address   string `json:"server_address"`
			Key       string `json:"api_key"`
			Libraries string `json:"library_ids"`
		} `json:"emby_config"`
	}
	if err := json.Unmarshal(raw, &actress); err != nil {
		return embyConn{}, errors.New("Emby 配置无法读取")
	}
	address, key := strings.TrimSpace(actress.Emby.Address), strings.TrimSpace(actress.Emby.Key)
	if address == "" || key == "" {
		return embyConn{}, errEmbyNotConfigured
	}
	base, err := url.Parse(address)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return embyConn{}, errors.New("Emby 服务器地址无效")
	}
	base.Path = strings.TrimRight(base.Path, "/")
	base.RawQuery, base.Fragment = "", ""
	conn := embyConn{base: base, key: key}
	for _, id := range strings.Split(actress.Emby.Libraries, ",") {
		if id = strings.TrimSpace(id); id != "" {
			conn.libraries = append(conn.libraries, id)
		}
	}
	return conn, nil
}

// signature 用于判断连接或媒体库选择是否变化，变化后缓存失效。
func (c embyConn) signature() string {
	return c.base.String() + "|" + c.key + "|" + strings.Join(c.libraries, ",")
}

func (c embyConn) endpoint(path string, query url.Values) string {
	u := *c.base
	u.Path = c.base.Path + path
	u.RawQuery = query.Encode()
	return u.String()
}

var embyClient = &http.Client{
	Timeout:       60 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (c embyConn) send(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.endpoint(path, query), body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Emby-Token", c.key)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := embyClient.Do(request)
	if err != nil {
		return nil, errors.New("无法连接 Emby 服务器")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, fmt.Errorf("Emby 返回状态 %d，请检查地址和 API Key", response.StatusCode)
	}
	return response, nil
}

func (c embyConn) getJSON(ctx context.Context, path string, query url.Values, limit int64, out any) error {
	response, err := c.send(ctx, http.MethodGet, path, query, nil, "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if err := json.NewDecoder(io.LimitReader(response.Body, limit)).Decode(out); err != nil {
		return errors.New("无法解析 Emby 返回的数据")
	}
	return nil
}

// embyActor 是演员列表里的一位演员，从媒体库影片的演职人员汇总得到。
type embyActor struct {
	ID       string
	Name     string
	ImageTag string
	Movies   int
}

// actorDirectory 缓存 Emby 演员列表，10 分钟内重复打开页面不再请求 Emby。
type actorDirectory struct {
	mu        sync.Mutex
	signature string
	loaded    time.Time
	serverID  string
	actors    []embyActor
	byID      map[string]*embyActor
}

const actorDirectoryTTL = 10 * time.Minute

// load 返回演员列表。force 为 true 或缓存过期、连接变化时重新从 Emby 读取。
func (d *actorDirectory) load(ctx context.Context, conn embyConn, force bool) ([]embyActor, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !force && d.byID != nil && d.signature == conn.signature() && time.Since(d.loaded) < actorDirectoryTTL {
		return d.actors, d.serverID, nil
	}
	var info struct {
		ID string `json:"Id"`
	}
	_ = conn.getJSON(ctx, "/System/Info/Public", nil, 1<<20, &info)
	libraries := conn.libraries
	if len(libraries) == 0 {
		libraries = []string{""}
	}
	byID := map[string]*embyActor{}
	var order []string
	seenMovie := map[string]bool{}
	for _, library := range libraries {
		query := url.Values{
			"Recursive": {"true"}, "IncludeItemTypes": {"Movie"}, "Fields": {"People"},
			"EnableImages": {"false"}, "EnableUserData": {"false"},
		}
		if library != "" {
			query.Set("ParentId", library)
		}
		var payload struct {
			Items []struct {
				ID     string `json:"Id"`
				People []struct {
					ID       string `json:"Id"`
					Name     string `json:"Name"`
					Type     string `json:"Type"`
					ImageTag string `json:"PrimaryImageTag"`
				} `json:"People"`
			} `json:"Items"`
		}
		if err := conn.getJSON(ctx, "/Items", query, 256<<20, &payload); err != nil {
			return nil, "", err
		}
		for _, movie := range payload.Items {
			if seenMovie[movie.ID] {
				continue
			}
			seenMovie[movie.ID] = true
			counted := map[string]bool{}
			for _, person := range movie.People {
				if person.Type != "Actor" || person.ID == "" || counted[person.ID] {
					continue
				}
				counted[person.ID] = true
				actor := byID[person.ID]
				if actor == nil {
					actor = &embyActor{ID: person.ID, Name: person.Name}
					byID[person.ID] = actor
					order = append(order, person.ID)
				}
				if actor.ImageTag == "" {
					actor.ImageTag = person.ImageTag
				}
				actor.Movies++
			}
		}
	}
	actors := make([]embyActor, 0, len(order))
	for _, id := range order {
		actors = append(actors, *byID[id])
	}
	d.byID = map[string]*embyActor{}
	for i := range actors {
		d.byID[actors[i].ID] = &actors[i]
	}
	d.actors, d.serverID, d.signature, d.loaded = actors, info.ID, conn.signature(), time.Now()
	return actors, d.serverID, nil
}

func (d *actorDirectory) get(id string) (embyActor, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	actor, ok := d.byID[id]
	if !ok {
		return embyActor{}, false
	}
	return *actor, true
}

// setImageTag 在上传头像后更新缓存里的头像版本，列表立即显示新头像。
func (d *actorDirectory) setImageTag(id, tag string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if actor, ok := d.byID[id]; ok {
		actor.ImageTag = tag
	}
}

// actorNames 返回用来匹配 Gfriends、Jalbum 的全部名字：Emby 原名在前，然后是映射表条目的全部名字和别名。
func actorNames(embyName string, mapping *actorMapping) []string {
	names := []string{embyName}
	if mapping != nil {
		if entry := mapping.entry(embyName); entry != nil {
			names = append(names, entry.names()...)
		}
	}
	seen := map[string]bool{}
	out := names[:0]
	for _, name := range names {
		key := normalizeActorName(name)
		if key != "" && !seen[key] {
			seen[key] = true
			out = append(out, name)
		}
	}
	return out
}

// actorDisplay 是演员在列表和详情里显示的名字：按「数据映射语言」映射，映射不到用 Emby 原名。
func actorDisplay(name string, mapping *actorMapping, lang string) string {
	if mapping != nil {
		if value, ok := mapping.lookup(name, lang); ok {
			return value
		}
	}
	return name
}

// actorMappingState 返回当前生效的映射表和语言。映射表不可用时 mapping 为 nil。
func (s *Server) actorMappingState() (mapping *actorMapping, lang string, custom bool) {
	settings, err := s.scrapeSettings()
	if err != nil {
		return nil, "", false
	}
	custom = settings.Metadata.ActorCustom && fileExists(customActorMappingPath(dataDirOf(s)))
	mapping, err = actorMaps.active(settings.Metadata.ActorCustom)
	if err != nil {
		mapping = nil
	}
	return mapping, settings.Metadata.StaticLang, custom
}

func embyFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, errEmbyNotConfigured) {
		fail(w, 400, "emby_not_configured", err.Error())
		return
	}
	fail(w, 502, "emby_error", err.Error())
}

func (s *Server) actorImageURL(actor embyActor) string {
	if actor.ImageTag == "" {
		return ""
	}
	return "/api/actors/" + url.PathEscape(actor.ID) + "/image?tag=" + url.QueryEscape(actor.ImageTag)
}

// listActors 提供 GET /api/actors。refresh=1 时重新从 Emby 读取。
func (s *Server) listActors(w http.ResponseWriter, r *http.Request) {
	conn, err := s.embyConnection()
	if err != nil {
		embyFailure(w, err)
		return
	}
	actors, _, err := s.actorDir.load(r.Context(), conn, r.URL.Query().Get("refresh") == "1")
	if err != nil {
		embyFailure(w, err)
		return
	}
	mapping, lang, custom := s.actorMappingState()
	gallery := s.gallery.snapshot()
	s.gallery.ensureLoaded(s)
	type item struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		EmbyName string `json:"emby_name"`
		Image    string `json:"image,omitempty"`
		Movies   int    `json:"movies"`
		Photos   int    `json:"photos"`
		Search   string `json:"search"`
	}
	items := make([]item, 0, len(actors))
	for _, actor := range actors {
		names := actorNames(actor.Name, mapping)
		items = append(items, item{
			ID: actor.ID, Name: actorDisplay(actor.Name, mapping, lang), EmbyName: actor.Name,
			Image: s.actorImageURL(actor), Movies: actor.Movies, Photos: len(gallery.jalbumPhotos(names)),
			Search: strings.ToLower(strings.Join(names, " ")),
		})
	}
	writeJSON(w, 200, object{"data": items, "sources": s.gallery.status(), "mapping": object{"custom": custom}})
}

var movieNumberPattern = regexp.MustCompile(`^([A-Za-z0-9]+-[A-Za-z0-9]+)\s+(.*)$`)

// getActor 提供 GET /api/actors/{id}：演员详情、关联影片和头像候选。
func (s *Server) getActor(w http.ResponseWriter, r *http.Request) {
	conn, err := s.embyConnection()
	if err != nil {
		embyFailure(w, err)
		return
	}
	_, serverID, err := s.actorDir.load(r.Context(), conn, false)
	if err != nil {
		embyFailure(w, err)
		return
	}
	id := r.PathValue("id")
	var detail struct {
		Items []struct {
			ID        string            `json:"Id"`
			Name      string            `json:"Name"`
			Overview  string            `json:"Overview"`
			ImageTags map[string]string `json:"ImageTags"`
		} `json:"Items"`
	}
	if err := conn.getJSON(r.Context(), "/Items", url.Values{"Ids": {id}, "Fields": {"Overview"}}, 4<<20, &detail); err != nil {
		embyFailure(w, err)
		return
	}
	if len(detail.Items) == 0 {
		fail(w, 404, "not_found", "Emby 中没有这位演员")
		return
	}
	person := detail.Items[0]
	actor, _ := s.actorDir.get(id)
	actor.ID, actor.Name, actor.ImageTag = person.ID, person.Name, person.ImageTags["Primary"]
	s.actorDir.setImageTag(id, actor.ImageTag)

	var movies struct {
		Items []struct {
			ID        string            `json:"Id"`
			Name      string            `json:"Name"`
			Year      int               `json:"ProductionYear"`
			ImageTags map[string]string `json:"ImageTags"`
		} `json:"Items"`
	}
	query := url.Values{
		"PersonIds": {id}, "Recursive": {"true"}, "IncludeItemTypes": {"Movie"}, "Fields": {"ProductionYear"},
		"SortBy": {"PremiereDate,SortName"}, "SortOrder": {"Descending"}, "Limit": {"500"},
		"EnableImageTypes": {"Primary"}, "EnableUserData": {"false"},
	}
	if err := conn.getJSON(r.Context(), "/Items", query, 16<<20, &movies); err != nil {
		embyFailure(w, err)
		return
	}
	type movie struct {
		ID     string `json:"id"`
		Number string `json:"number,omitempty"`
		Title  string `json:"title"`
		Year   int    `json:"year,omitempty"`
		URL    string `json:"url,omitempty"`
		Poster string `json:"poster,omitempty"`
	}
	list := make([]movie, 0, len(movies.Items))
	for _, item := range movies.Items {
		entry := movie{ID: item.ID, Title: item.Name, Year: item.Year, URL: s.embyItemURL(conn, serverID, item.ID)}
		if tag := item.ImageTags["Primary"]; tag != "" {
			entry.Poster = "/api/emby/items/" + url.PathEscape(item.ID) + "/image?tag=" + url.QueryEscape(tag)
		}
		if match := movieNumberPattern.FindStringSubmatch(item.Name); match != nil {
			entry.Number, entry.Title = strings.ToUpper(match[1]), match[2]
		}
		list = append(list, entry)
	}

	mapping, lang, custom := s.actorMappingState()
	var names object
	if mapping != nil {
		if entry := mapping.entry(person.Name); entry != nil {
			// 别名完整返回映射表 keyword 的内容，与日、简、繁名相同的也保留。
			names = object{"jp": entry.jp, "zh_cn": entry.zhCN, "zh_tw": entry.zhTW, "aliases": append([]string{}, entry.aliases...)}
		}
	}
	matchNames := actorNames(person.Name, mapping)
	gallery := s.gallery.snapshot()
	s.gallery.ensureLoaded(s)
	writeJSON(w, 200, object{
		"id": person.ID, "name": actorDisplay(person.Name, mapping, lang), "emby_name": person.Name,
		"overview": person.Overview, "image": s.actorImageURL(actor), "emby_url": s.embyItemURL(conn, serverID, person.ID),
		"names": names, "match_names": matchNames, "movies": list,
		"gfriends": gallery.gfriendsImages(matchNames), "jalbum": gallery.jalbumPhotos(matchNames),
		"sources": s.gallery.status(), "mapping": object{"custom": custom, "editable": !custom},
	})
}

func (s *Server) embyItemURL(conn embyConn, serverID, id string) string {
	u := *conn.base
	u.Path = conn.base.Path + "/web/index.html"
	u.Fragment = "!/item?id=" + url.QueryEscape(id)
	if serverID != "" {
		u.Fragment += "&serverId=" + url.QueryEscape(serverID)
	}
	return u.String()
}

// actorImage 提供 GET /api/actors/{id}/image 和 GET /api/emby/items/{id}/image：
// 转发 Emby 的主图（演员头像、影片封面），浏览器不需要 API Key。
// 地址带 tag（头像版本），头像更换后地址随之变化，所以可以长期缓存。
func (s *Server) actorImage(w http.ResponseWriter, r *http.Request) {
	conn, err := s.embyConnection()
	if err != nil {
		embyFailure(w, err)
		return
	}
	query := url.Values{"maxWidth": {"600"}, "quality": {"90"}}
	if tag := r.URL.Query().Get("tag"); tag != "" {
		query.Set("tag", tag)
	}
	response, err := conn.send(r.Context(), http.MethodGet, "/Items/"+url.PathEscape(r.PathValue("id"))+"/Images/Primary", query, nil, "")
	if err != nil {
		fail(w, 404, "no_image", "没有头像")
		return
	}
	defer response.Body.Close()
	w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	if r.URL.Query().Get("tag") != "" {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	_, _ = io.Copy(w, io.LimitReader(response.Body, 32<<20))
}

// avatarImage 检查图片格式，返回 Emby 接受的 Content-Type。
func avatarImage(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("图片为空")
	}
	if len(data) > 10<<20 {
		return "", errors.New("图片超过 10 MB")
	}
	switch kind := http.DetectContentType(data); kind {
	case "image/jpeg", "image/png", "image/webp":
		return kind, nil
	default:
		return "", errors.New("只支持 JPG、PNG、WebP 图片")
	}
}

// uploadEmbyAvatar 把图片设为演员头像。Emby 要求请求体是 base64 编码的图片。
// 返回上传后的头像版本（tag），读取失败时为空。
func (s *Server) uploadEmbyAvatar(ctx context.Context, conn embyConn, id string, data []byte) (string, error) {
	kind, err := avatarImage(data)
	if err != nil {
		return "", err
	}
	body := strings.NewReader(base64.StdEncoding.EncodeToString(data))
	response, err := conn.send(ctx, http.MethodPost, "/Items/"+url.PathEscape(id)+"/Images/Primary", nil, body, kind)
	if err != nil {
		return "", fmt.Errorf("上传头像到 Emby 失败：%w", err)
	}
	response.Body.Close()
	var detail struct {
		Items []struct {
			ImageTags map[string]string `json:"ImageTags"`
		} `json:"Items"`
	}
	tag := ""
	if conn.getJSON(ctx, "/Items", url.Values{"Ids": {id}}, 4<<20, &detail) == nil && len(detail.Items) > 0 {
		tag = detail.Items[0].ImageTags["Primary"]
	}
	s.actorDir.setImageTag(id, tag)
	return tag, nil
}

// setActorImage 提供 POST /api/actors/{id}/image：从 Gfriends、Jalbum 或本地上传的图片设置头像。
func (s *Server) setActorImage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 15<<20)
	var request struct {
		Source string `json:"source"`
		Path   string `json:"path"`
		Body   string `json:"body_base64"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		fail(w, 400, "invalid_body", "请求格式错误或图片超过 10 MB")
		return
	}
	conn, err := s.embyConnection()
	if err != nil {
		embyFailure(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	var data []byte
	switch request.Source {
	case "upload":
		data, err = base64.StdEncoding.DecodeString(request.Body)
		if err != nil {
			fail(w, 400, "invalid_image", "图片数据无效")
			return
		}
	case galleryGfriends, galleryJalbum:
		data, err = s.gallery.image(ctx, s, request.Source, request.Path)
		if err != nil {
			fail(w, 502, "download_failed", err.Error())
			return
		}
	default:
		fail(w, 400, "invalid_source", "未知的图片来源")
		return
	}
	tag, err := s.uploadEmbyAvatar(ctx, conn, r.PathValue("id"), data)
	if err != nil {
		fail(w, 400, "upload_failed", err.Error())
		return
	}
	slog.Info("[actors] 已更新头像", "id", r.PathValue("id"), "source", request.Source)
	writeJSON(w, 200, object{"success": true, "image": s.actorImageURL(embyActor{ID: r.PathValue("id"), ImageTag: tag})})
}

// fillMissingAvatars 提供 POST /api/actors/fill-missing：为 Emby 中没有头像的演员，
// 使用 Gfriends 文件树里该演员的最后一张头像。已有头像的演员不处理。
func (s *Server) fillMissingAvatars(w http.ResponseWriter, r *http.Request) {
	// main.go 设置了写超时；补头像可能需要几分钟，这里清除它。
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	conn, err := s.embyConnection()
	if err != nil {
		embyFailure(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Minute)
	defer cancel()
	actors, _, err := s.actorDir.load(ctx, conn, true)
	if err != nil {
		embyFailure(w, err)
		return
	}
	if err := s.gallery.require(ctx, s, galleryGfriends); err != nil {
		fail(w, 502, "gallery_unavailable", err.Error())
		return
	}
	mapping, _, _ := s.actorMappingState()
	gallery := s.gallery.snapshot()
	type job struct {
		actor embyActor
		path  string
	}
	var jobs []job
	missing, noImage := 0, 0
	for _, actor := range actors {
		if actor.ImageTag != "" {
			continue
		}
		missing++
		images := gallery.gfriendsImages(actorNames(actor.Name, mapping))
		if len(images) == 0 {
			noImage++
			continue
		}
		jobs = append(jobs, job{actor: actor, path: images[len(images)-1].Path})
	}
	var mu sync.Mutex
	updated, failed := 0, []string{}
	queue := make(chan job)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range queue {
				data, err := s.gallery.image(ctx, s, galleryGfriends, item.path)
				if err == nil {
					_, err = s.uploadEmbyAvatar(ctx, conn, item.actor.ID, data)
				}
				mu.Lock()
				if err != nil {
					failed = append(failed, item.actor.Name+"："+err.Error())
					slog.Warn("[actors] 补头像失败", "name", item.actor.Name, "error", err)
				} else {
					updated++
				}
				mu.Unlock()
			}
		}()
	}
	for _, item := range jobs {
		if ctx.Err() != nil {
			break
		}
		queue <- item
	}
	close(queue)
	wg.Wait()
	sort.Strings(failed)
	slog.Info("[actors] 补头像完成", "missing", missing, "updated", updated, "no_gfriends", noImage, "failed", len(failed))
	writeJSON(w, 200, object{"missing": missing, "updated": updated, "no_gfriends": noImage, "failed": failed})
}
