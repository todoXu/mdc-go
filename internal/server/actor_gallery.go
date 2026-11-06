package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 演员图库：Gfriends 头像仓库和 Jalbum 写真仓库。
// 两个仓库的文件树（Filetree.json）下载到 data/cache，在本机按名字匹配；图片用到时才下载，并缓存到 data/cache/gallery。

const (
	galleryGfriends = "gfriends"
	galleryJalbum   = "jalbum"
)

type gallerySource struct {
	tree    string // 文件树地址
	content string // 图片地址前缀（Content 目录）
	file    string // 本地缓存的文件树文件名
	// 图片相对路径的层数范围：Gfriends 是「厂牌/文件」；Jalbum 是「首字/演员/文件」，单字名演员是「首字/文件」。
	minSegments, maxSegments int
}

var gallerySources = map[string]gallerySource{
	galleryGfriends: {
		tree:        "https://raw.githubusercontent.com/gfriends/gfriends/master/Filetree.json",
		content:     "https://raw.githubusercontent.com/gfriends/gfriends/master/Content/",
		file:        "gfriends-filetree.json",
		minSegments: 2, maxSegments: 2,
	},
	galleryJalbum: {
		tree:        "https://raw.githubusercontent.com/li-peifeng/Jalbum/main/Filetree.json",
		content:     "https://raw.githubusercontent.com/li-peifeng/Jalbum/main/Content/",
		file:        "jalbum-filetree.json",
		minSegments: 2, maxSegments: 3,
	},
}

// gfImage 是 Gfriends 文件树里的一张头像。seq 是它在文件树中的出现顺序。
type gfImage struct {
	path string // 厂牌/文件名?t=时间戳
	seq  int
}

type jalbumActor struct {
	dir   string   // 首字/演员名
	files []string // 已按序号排序
}

// galleryData 是解析后的索引。重新加载时整体替换，读取时不需要加锁。
type galleryData struct {
	gfriends map[string][]gfImage
	jalbum   map[string]*jalbumActor
}

type galleryState struct {
	State   string    `json:"state"` // missing、loading、ready、error
	Updated time.Time `json:"updated,omitzero"`
	Actors  int       `json:"actors"`
	Images  int       `json:"images"`
	Error   string    `json:"error,omitempty"`
}

type galleryIndex struct {
	mu      sync.Mutex
	data    galleryData
	state   map[string]*galleryState
	running map[string]chan struct{} // 正在加载的来源；加载结束时关闭
}

func (g *galleryIndex) snapshot() galleryData {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.data
}

func (g *galleryIndex) status() map[string]galleryState {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := map[string]galleryState{}
	for name := range gallerySources {
		if state := g.state[name]; state != nil {
			out[name] = *state
		} else {
			out[name] = galleryState{State: "missing"}
		}
	}
	return out
}

func galleryCacheDir(s *Server) string {
	return filepath.Join(dataDirOf(s), "cache")
}

// ensureLoaded 在后台加载还没有加载的来源：本地有缓存就读取，没有就下载。不等待结果。
func (g *galleryIndex) ensureLoaded(s *Server) {
	for name := range gallerySources {
		g.mu.Lock()
		state := g.state[name]
		start := state == nil && g.running[name] == nil
		g.mu.Unlock()
		if start {
			go func(name string) { _ = g.load(context.Background(), s, name, false) }(name)
		}
	}
}

// require 等待来源加载完成，用于需要立即使用索引的操作（如补头像）。
func (g *galleryIndex) require(ctx context.Context, s *Server, name string) error {
	g.mu.Lock()
	state := g.state[name]
	g.mu.Unlock()
	if state != nil && state.State == "ready" {
		return nil
	}
	return g.load(ctx, s, name, false)
}

// load 加载一个来源的文件树。download 为 true 时总是重新下载，否则优先读取本地缓存。
// 同一来源同时只加载一次，其它调用等待它完成。
func (g *galleryIndex) load(ctx context.Context, s *Server, name string, download bool) error {
	source, ok := gallerySources[name]
	if !ok {
		return errors.New("未知的图库")
	}
	g.mu.Lock()
	if g.state == nil {
		g.state, g.running = map[string]*galleryState{}, map[string]chan struct{}{}
	}
	if wait := g.running[name]; wait != nil {
		g.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
		if state := g.status()[name]; state.State != "ready" {
			return errors.New(state.Error)
		}
		return nil
	}
	done := make(chan struct{})
	g.running[name] = done
	previous := g.state[name]
	loading := &galleryState{State: "loading"}
	if previous != nil {
		loading.Updated, loading.Actors, loading.Images = previous.Updated, previous.Actors, previous.Images
	}
	g.state[name] = loading
	g.mu.Unlock()

	err := g.loadSource(ctx, s, name, source, download)
	g.mu.Lock()
	delete(g.running, name)
	if err != nil {
		failed := *loading
		failed.State, failed.Error = "error", err.Error()
		if previous != nil && previous.State == "ready" {
			// 更新失败时继续使用旧索引。
			failed.State = "ready"
		}
		g.state[name] = &failed
		slog.Warn("[gallery] 加载文件树失败", "source", name, "error", err)
	}
	g.mu.Unlock()
	close(done)
	return err
}

func (g *galleryIndex) loadSource(ctx context.Context, s *Server, name string, source gallerySource, download bool) error {
	path := filepath.Join(galleryCacheDir(s), source.file)
	if !download && !fileExists(path) {
		download = true
	}
	if download {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		data, err := githubDownload(ctx, source.tree, s.githubToken(), 128<<20)
		if err != nil {
			return fmt.Errorf("下载文件树失败：%w", err)
		}
		if err := writeFileAtomic(path, data); err != nil {
			return fmt.Errorf("保存文件树失败：%w", err)
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, _ := file.Stat()
	state := &galleryState{State: "ready", Updated: info.ModTime()}
	// 两个来源可能同时加载，各自只替换自己的索引，不能整体覆盖 g.data。
	switch name {
	case galleryGfriends:
		index, images, err := parseGfriendsTree(file)
		if err != nil {
			return err
		}
		state.Actors, state.Images = len(index), images
		g.mu.Lock()
		g.data.gfriends = index
		g.state[name] = state
		g.mu.Unlock()
	case galleryJalbum:
		index, images, err := parseJalbumTree(file)
		if err != nil {
			return err
		}
		state.Actors, state.Images = len(index), images
		g.mu.Lock()
		g.data.jalbum = index
		g.state[name] = state
		g.mu.Unlock()
	}
	slog.Info("[gallery] 文件树已加载", "source", name, "actors", state.Actors, "images", state.Images)
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

// githubDownload 下载 GitHub 上的文件。填写了 GitHub Token 时附带令牌，令牌只发给 GitHub。
func githubDownload(ctx context.Context, address, token string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	withToken := token != "" && isGitHubHost(request.URL.Hostname())
	if withToken {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("跳转次数过多")
		}
		if !isGitHubHost(next.URL.Hostname()) {
			next.Header.Del("Authorization")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("无法访问 GitHub：%w", err)
	}
	defer response.Body.Close()
	if withToken && (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
		return nil, fmt.Errorf("GitHub Token 无效或没有读取权限（HTTP %d）", response.StatusCode)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub 返回 HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("读取失败：%w", err)
	}
	if int64(len(data)) > limit {
		return nil, errors.New("文件过大")
	}
	return data, nil
}

// readObject 读取一个 JSON 对象，对每个键调用 field，field 必须读完对应的值。
func readObject(decoder *json.Decoder, field func(key string) error) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return errors.New("文件树格式不正确")
	}
	return readObjectBody(decoder, field)
}

// readObjectBody 与 readObject 相同，但开头的「{」已经被读取。
func readObjectBody(decoder *json.Decoder, field func(key string) error) error {
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("文件树格式不正确")
		}
		if err := field(key); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}

func skipValue(decoder *json.Decoder) error {
	var discard json.RawMessage
	return decoder.Decode(&discard)
}

// parseGfriendsTree 解析 Gfriends 文件树：{"Content": {厂牌: {"名字.jpg": "文件名?t=时间戳"}}}。
// 同一张图片有多个名字（别名）时，每个名字都指向它。按出现顺序记录，文件树里越靠后质量越高。
func parseGfriendsTree(r io.Reader) (map[string][]gfImage, int, error) {
	decoder := json.NewDecoder(r)
	index := map[string][]gfImage{}
	seqByPath := map[string]int{}
	err := readObject(decoder, func(key string) error {
		if key != "Content" {
			return skipValue(decoder)
		}
		return readObject(decoder, func(company string) error {
			return readObject(decoder, func(name string) error {
				var file string
				if err := decoder.Decode(&file); err != nil {
					return err
				}
				if file == "" {
					return nil
				}
				path := company + "/" + file
				seq, ok := seqByPath[path]
				if !ok {
					seq = len(seqByPath)
					seqByPath[path] = seq
				}
				actor := normalizeActorName(strings.TrimSuffix(name, filepath.Ext(name)))
				if actor != "" {
					index[actor] = append(index[actor], gfImage{path: path, seq: seq})
				}
				return nil
			})
		})
	})
	if err != nil {
		return nil, 0, fmt.Errorf("Gfriends 文件树无效：%w", err)
	}
	if len(index) == 0 {
		return nil, 0, errors.New("Gfriends 文件树没有任何头像")
	}
	return index, len(seqByPath), nil
}

// parseJalbumTree 解析 Jalbum 文件树：{"Content": {首字: {演员名: {"演员名-1.jpg": ""}}}}。
func parseJalbumTree(r io.Reader) (map[string]*jalbumActor, int, error) {
	decoder := json.NewDecoder(r)
	index := map[string]*jalbumActor{}
	total := 0
	err := readObject(decoder, func(key string) error {
		if key != "Content" {
			return skipValue(decoder)
		}
		return readObject(decoder, func(letter string) error {
			// 名字只有一个字的演员（如「杏」），写真直接放在首字目录里：{首字: {"杏-1.jpg": ""}}。
			direct := &jalbumActor{dir: letter}
			err := readObject(decoder, func(name string) error {
				token, err := decoder.Token()
				if err != nil {
					return err
				}
				if _, isFile := token.(string); isFile {
					direct.files = append(direct.files, name)
					return nil
				}
				if delim, ok := token.(json.Delim); !ok || delim != '{' {
					return errors.New("文件树格式不正确")
				}
				actor := &jalbumActor{dir: letter + "/" + name}
				if err := readObjectBody(decoder, func(file string) error {
					actor.files = append(actor.files, file)
					return skipValue(decoder)
				}); err != nil {
					return err
				}
				total += addJalbumActor(index, name, actor)
				return nil
			})
			total += addJalbumActor(index, letter, direct)
			return err
		})
	})
	if err != nil {
		return nil, 0, fmt.Errorf("Jalbum 文件树无效：%w", err)
	}
	if len(index) == 0 {
		return nil, 0, errors.New("Jalbum 文件树没有任何写真")
	}
	return index, total, nil
}

// addJalbumActor 记录一位演员的写真，返回记录的张数。同名目录以先出现的为准。
func addJalbumActor(index map[string]*jalbumActor, name string, actor *jalbumActor) int {
	key := normalizeActorName(name)
	if len(actor.files) == 0 || key == "" {
		return 0
	}
	if _, exists := index[key]; exists {
		return 0
	}
	sort.SliceStable(actor.files, func(i, j int) bool { return photoOrder(actor.files[i]) < photoOrder(actor.files[j]) })
	index[key] = actor
	return len(actor.files)
}

// photoOrder 取文件名末尾的序号（如「上羽絢-12.jpg」的 12），让写真按 1、2、…、10 的顺序排列。
func photoOrder(file string) int {
	stem := strings.TrimSuffix(file, filepath.Ext(file))
	index := strings.LastIndex(stem, "-")
	if index < 0 {
		return 0
	}
	n, err := strconv.Atoi(stem[index+1:])
	if err != nil {
		return 0
	}
	return n
}

type galleryImage struct {
	Path    string `json:"path"`
	Company string `json:"company,omitempty"`
	AI      bool   `json:"ai,omitempty"`
	Default bool   `json:"default,omitempty"`
}

// gfriendsImages 按名字匹配 Gfriends 头像，按文件树中的顺序排列，最后一张标为默认。
func (d galleryData) gfriendsImages(names []string) []galleryImage {
	seen := map[string]bool{}
	var matched []gfImage
	for _, name := range names {
		for _, image := range d.gfriends[normalizeActorName(name)] {
			if !seen[image.path] {
				seen[image.path] = true
				matched = append(matched, image)
			}
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].seq < matched[j].seq })
	out := make([]galleryImage, 0, len(matched))
	for i, image := range matched {
		company, file, _ := strings.Cut(image.path, "/")
		out = append(out, galleryImage{Path: image.path, Company: company, AI: strings.HasPrefix(file, "AI-Fix-"), Default: i == len(matched)-1})
	}
	return out
}

// jalbumPhotos 按名字匹配 Jalbum 写真，与 Gfriends 一样合并所有名字匹配到的目录：
// 本名在前，别名对应的目录依次接在后面；同一目录只算一次。
func (d galleryData) jalbumPhotos(names []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, name := range names {
		actor := d.jalbum[normalizeActorName(name)]
		if actor == nil || seen[actor.dir] {
			continue
		}
		seen[actor.dir] = true
		for _, file := range actor.files {
			out = append(out, actor.dir+"/"+file)
		}
	}
	return out
}

// imageURL 校验图片相对路径并返回仓库中的下载地址。路径只能在对应仓库的 Content 目录内。
func (source gallerySource) imageURL(path string) (string, error) {
	file, query, _ := strings.Cut(path, "?")
	parts := strings.Split(file, "/")
	if len(parts) < source.minSegments || len(parts) > source.maxSegments {
		return "", errors.New("图片路径无效")
	}
	for i, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\\x00") {
			return "", errors.New("图片路径无效")
		}
		parts[i] = url.PathEscape(part)
	}
	address := source.content + strings.Join(parts, "/")
	if query != "" {
		values, err := url.ParseQuery(query)
		if err != nil {
			return "", errors.New("图片路径无效")
		}
		address += "?" + values.Encode()
	}
	return address, nil
}

// image 返回图库图片：本地有缓存直接读取，否则从 GitHub 下载并缓存。
func (g *galleryIndex) image(ctx context.Context, s *Server, name, path string) ([]byte, error) {
	source, ok := gallerySources[name]
	if !ok {
		return nil, errors.New("未知的图库")
	}
	address, err := source.imageURL(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(address))
	cache := filepath.Join(galleryCacheDir(s), "gallery", hex.EncodeToString(sum[:]))
	if data, err := os.ReadFile(cache); err == nil && len(data) > 0 {
		return data, nil
	}
	data, err := githubDownload(ctx, address, s.githubToken(), 20<<20)
	if err != nil {
		return nil, fmt.Errorf("下载图片失败：%w", err)
	}
	if !strings.HasPrefix(http.DetectContentType(data), "image/") {
		return nil, errors.New("下载的文件不是图片")
	}
	if err := writeFileAtomic(cache, data); err != nil {
		slog.Warn("[gallery] 缓存图片失败", "path", cache, "error", err)
	}
	return data, nil
}

// galleryImageHandler 提供 GET /api/actors/gallery/{source}?path=…：页面显示图库图片。
func (s *Server) galleryImageHandler(w http.ResponseWriter, r *http.Request) {
	source, ok := gallerySources[r.PathValue("source")]
	if !ok {
		fail(w, 404, "not_found", "未知的图库")
		return
	}
	if _, err := source.imageURL(r.URL.Query().Get("path")); err != nil {
		fail(w, 400, "invalid_path", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	data, err := s.gallery.image(ctx, s, r.PathValue("source"), r.URL.Query().Get("path"))
	if err != nil {
		fail(w, 502, "download_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(data))
	w.Header().Set("Cache-Control", "private, max-age=604800")
	_, _ = w.Write(data)
}

// refreshGallery 提供 POST /api/actors/sources/{source}/refresh：重新下载文件树。
func (s *Server) refreshGallery(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	name := r.PathValue("source")
	if _, ok := gallerySources[name]; !ok {
		fail(w, 404, "not_found", "未知的图库")
		return
	}
	if err := s.gallery.load(r.Context(), s, name, true); err != nil {
		fail(w, 502, "refresh_failed", err.Error())
		return
	}
	writeJSON(w, 200, object{"sources": s.gallery.status()})
}
