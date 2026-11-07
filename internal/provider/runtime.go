package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"github.com/dop251/goja"
)

type scriptSource struct {
	manifest Manifest
	registry *Registry
}

func (p *scriptSource) Descriptor() Manifest { return p.manifest }
func (p *scriptSource) Probe(ctx context.Context, c Config) error {
	_, err := p.run(ctx, c, "test")
	return err
}
func (p *scriptSource) Fetch(ctx context.Context, c Config, input string) (Metadata, error) {
	value, err := p.run(ctx, c, "fetch", input)
	if err != nil {
		return Metadata{}, err
	}
	data, err := jsonValue(value)
	if err != nil {
		return Metadata{}, issue("plugin_error", "插件返回的元数据格式无效")
	}
	var m Metadata
	if err := decodeValue(data, &m); err != nil {
		return Metadata{}, issue("plugin_error", "插件返回的元数据格式无效")
	}
	if m.Provider != "" && m.Provider != p.manifest.ID {
		return Metadata{}, issue("plugin_error", "插件返回的数据源 ID 不一致")
	}
	m.Provider = p.manifest.ID
	if m.Number == "" || m.Title == "" || m.SourceURL == "" {
		return Metadata{}, issue("parse_error", "插件未返回番号、标题或来源地址")
	}
	if _, err := NormalizeNumber(m.Number); err != nil {
		return Metadata{}, issue("parse_error", "插件返回的番号无效")
	}
	if _, err := baseURL(m.SourceURL); err != nil {
		return Metadata{}, issue("plugin_error", "插件返回的来源地址无效")
	}
	if m.Actors == nil {
		m.Actors = []string{}
	}
	if m.Tags == nil {
		m.Tags = []string{}
	}
	if m.Samples == nil {
		m.Samples = []string{}
	}
	return m, nil
}
func (p *scriptSource) ReadImage(ctx context.Context, c Config, address, referer string) (Image, error) {
	value, err := p.run(ctx, c, "image", address, referer)
	if err != nil {
		return Image{}, err
	}
	data, err := jsonValue(value)
	if err != nil {
		return Image{}, issue("invalid_image", "插件返回的图片格式无效")
	}
	var out struct {
		BodyBase64  string `json:"body_base64"`
		ContentType string `json:"content_type"`
	}
	if err := decodeValue(data, &out); err != nil {
		return Image{}, issue("invalid_image", "插件返回的图片格式无效")
	}
	body, err := base64.StdEncoding.DecodeString(out.BodyBase64)
	if err != nil || len(body) == 0 || len(body) > 20<<20 {
		return Image{}, issue("invalid_image", "插件返回的图片无效或超过 20 MB")
	}
	kind := http.DetectContentType(body)
	if !strings.HasPrefix(kind, "image/") || strings.Contains(kind, "svg") {
		return Image{}, issue("invalid_image", "来源返回的不是支持的图片")
	}
	return Image{Body: body, ContentType: kind}, nil
}

// Trailer URLs come from source metadata. Reuse the same proxy, origin-scoped
// cookies, referer, redirect rules and cancellation as the plugin HTTP bridge.
func (p *scriptSource) ReadTrailer(ctx context.Context, c Config, address, referer string) (Image, error) {
	result, err := p.request(ctx, c, address, map[string]any{"binary": true, "media": "trailer", "referer": referer})
	if err != nil {
		return Image{}, err
	}
	if result["status"] != http.StatusOK {
		return Image{}, issue("video_upstream", fmt.Sprintf("预告片来源返回 HTTP %v", result["status"]))
	}
	encoded, _ := result["body_base64"].(string)
	body, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(body) == 0 {
		return Image{}, issue("invalid_video", "预告片为空或数据无效")
	}
	kind := http.DetectContentType(body)
	if kind != "video/mp4" && kind != "video/webm" {
		return Image{}, issue("invalid_video", "预告片不是可直接下载的 MP4/WebM；不支持播放网页或 HLS 播放列表")
	}
	return Image{Body: body, ContentType: kind}, nil
}

func (p *scriptSource) run(ctx context.Context, c Config, method string, args ...string) (any, error) {
	code, err := os.ReadFile(filepath.Join(p.registry.Dir, p.manifest.ID, "main.js"))
	if err != nil {
		return nil, issue("plugin_error", "无法读取插件脚本")
	}
	if len(code) > 1<<20 {
		return nil, issue("plugin_error", "插件脚本超过 1 MB")
	}
	if c.BaseURL == "" {
		c.BaseURL = p.manifest.BaseURL
	}
	if _, err := baseURL(c.BaseURL); err != nil {
		return nil, err
	}
	vm := goja.New()
	host := map[string]any{
		"request": func(address string, opts map[string]any) map[string]any {
			result, err := p.request(ctx, c, address, opts)
			if err != nil {
				panic(vm.ToValue(map[string]string{"code": errorCode(err), "message": err.Error()}))
			}
			return result
		},
		"select": func(body, selector string) []map[string]any {
			if _, err := cascadia.Compile(selector); err != nil {
				panic(vm.ToValue(map[string]string{"code": "plugin_error", "message": "插件 CSS 选择器无效"}))
			}
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
			if err != nil {
				panic(vm.ToValue(map[string]string{"code": "invalid_page", "message": "无法解析 HTML"}))
			}
			nodes := []map[string]any{}
			doc.Find(selector).Each(func(_ int, s *goquery.Selection) {
				attrs := map[string]string{}
				if len(s.Nodes) > 0 {
					for _, a := range s.Nodes[0].Attr {
						attrs[a.Key] = a.Val
					}
				}
				outer, _ := goquery.OuterHtml(s)
				nodes = append(nodes, map[string]any{"text": strings.Join(strings.Fields(s.Text()), " "), "html": outer, "attrs": attrs})
			})
			return nodes
		},
		"resolve": func(base, reference string) string {
			u, e1 := url.Parse(base)
			r, e2 := url.Parse(reference)
			if e1 != nil || e2 != nil || u == nil || r == nil {
				return ""
			}
			resolved := u.ResolveReference(r)
			if resolved.Scheme != "http" && resolved.Scheme != "https" {
				return ""
			}
			return resolved.String()
		},
		"normalize": func(s string) string {
			n, err := NormalizeNumber(s)
			if err != nil {
				return ""
			}
			return n
		},
	}
	vm.Set("host", host)
	vm.Set("config", c)
	vm.Set("manifest", p.manifest)
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			vm.Interrupt(ctx.Err())
		case <-done:
		}
	}()
	defer close(done)
	if _, err := vm.RunScript(filepath.Join(p.manifest.ID, "main.js"), string(code)); err != nil {
		return nil, scriptError(ctx, vm, err)
	}
	exported := vm.Get("plugin")
	if exported == nil || goja.IsUndefined(exported) || goja.IsNull(exported) {
		return nil, issue("plugin_error", "插件没有定义 plugin 对象")
	}
	plugin := exported.ToObject(vm)
	fn, ok := goja.AssertFunction(plugin.Get(method))
	if !ok {
		return nil, issue("plugin_error", "插件缺少 "+method+" 方法")
	}
	values := make([]goja.Value, len(args))
	for i, arg := range args {
		values[i] = vm.ToValue(arg)
	}
	result, err := fn(plugin, values...)
	if err != nil {
		return nil, scriptError(ctx, vm, err)
	}
	return result.Export(), nil
}
func scriptError(ctx context.Context, vm *goja.Runtime, err error) error {
	if ctx.Err() != nil {
		return issue("timeout", "插件执行已取消或超时")
	}
	var ex *goja.Exception
	if errors.As(err, &ex) {
		obj := ex.Value().ToObject(vm)
		code := obj.Get("code")
		message := obj.Get("message")
		if code != nil && !goja.IsUndefined(code) && message != nil && !goja.IsUndefined(message) {
			return issue(code.String(), message.String())
		}
	}
	return issue("plugin_error", "插件执行失败: "+err.Error())
}
func errorCode(err error) string {
	var e *SourceError
	if errors.As(err, &e) {
		return e.Code
	}
	return "plugin_error"
}
func jsonValue(value any) ([]byte, error)       { return json.Marshal(value) }
func decodeValue(data []byte, target any) error { return json.Unmarshal(data, target) }

// request is a site-agnostic capability. The plugin chooses every URL, header
// and response interpretation; the host bounds network and memory use.
func (p *scriptSource) request(ctx context.Context, c Config, address string, opts map[string]any) (map[string]any, error) {
	u, err := url.Parse(address)
	if err != nil || u == nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, issue("invalid_config", "插件请求地址无效")
	}
	binary, _ := opts["binary"].(bool)
	trailer := opts["media"] == "trailer"
	limit := int64(4 << 20)
	if binary {
		limit = 20 << 20
	}
	if trailer {
		limit = 64 << 20
	}
	// 冷却为 0 时不排队，请求立即发出；大于 0 时同一数据源的请求之间至少间隔这么久。
	interval := c.CooldownSeconds
	if interval < 0 {
		interval = 0
	}
	if interval > 60 {
		interval = 60
	}
	p.registry.mu.Lock()
	wait := time.Duration(0)
	if interval > 0 {
		wait = time.Until(p.registry.last[p.manifest.ID])
		if wait < 0 {
			wait = 0
		}
		p.registry.last[p.manifest.ID] = time.Now().Add(wait + time.Duration(interval)*time.Second)
	}
	p.registry.mu.Unlock()
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, issue("timeout", "请求已取消或等待超时")
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	if c.Proxy != "" {
		proxy, err := url.Parse(c.Proxy)
		if err != nil || proxy.Host == "" {
			return nil, issue("invalid_config", "代理地址无效")
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
	timeout := 20 * time.Second
	if c.RequestTimeout > 0 {
		timeout = time.Duration(c.RequestTimeout) * time.Second
	}
	if trailer {
		timeout = 60 * time.Second
		if c.TrailerTimeout > 0 {
			timeout = time.Duration(c.TrailerTimeout) * time.Second
		}
	}
	client := &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Host != via[0].URL.Host {
			return fmt.Errorf("重定向过多或跨站")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, issue("invalid_config", "无法创建请求")
	}
	ua := c.UserAgent
	if ua == "" {
		ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/130.0.0.0 Safari/537.36"
	}
	req.Header.Set("User-Agent", ua)
	acceptLanguage := "zh-CN,zh;q=0.9,en;q=0.8"
	if requested, ok := opts["accept_language"].(string); ok && requested != "" {
		if strings.ContainsAny(requested, "\r\n") {
			return nil, issue("invalid_config", "插件 Accept-Language 不能包含换行")
		}
		acceptLanguage = requested
	}
	req.Header.Set("Accept-Language", acceptLanguage)
	if extra, ok := opts["headers"].(map[string]any); ok {
		for name, raw := range extra {
			value, ok := raw.(string)
			if !ok || name == "" {
				continue
			}
			if strings.ContainsAny(name, " \t\r\n:") || strings.ContainsAny(value, "\r\n") {
				return nil, issue("invalid_config", "插件请求头无效")
			}
			switch strings.ToLower(name) {
			case "host", "content-length", "cookie", "user-agent", "accept-language":
				continue
			}
			req.Header.Set(name, value)
		}
	}
	if binary {
		req.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,image/gif,*/*;q=0.5")
	}
	if trailer {
		req.Header.Set("Accept", "video/mp4,video/webm,*/*;q=0.5")
	}
	configured, _ := url.Parse(c.BaseURL)
	if configured != nil && u.Host == configured.Host && c.Cookies != "" {
		req.Header.Set("Cookie", c.Cookies)
	}
	if referer, ok := opts["referer"].(string); ok && referer != "" {
		page, err := url.Parse(referer)
		if err != nil || page == nil || page.Host == "" || (page.Scheme != "http" && page.Scheme != "https") {
			return nil, issue("invalid_image", "图片来源无效")
		}
		req.Header.Set("Referer", referer)
	}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, issue("timeout", "请求已取消或超时")
		}
		return nil, issue("network_error", "连接数据源失败，请检查网络、代理和站点地址")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, issue("network_error", "读取数据源失败")
	}
	if int64(len(body)) > limit {
		return nil, issue("invalid_page", "来源内容过大")
	}
	result := map[string]any{"status": response.StatusCode, "url": response.Request.URL.String(), "content_type": response.Header.Get("Content-Type")}
	if binary {
		result["body_base64"] = base64.StdEncoding.EncodeToString(body)
	} else {
		result["body"] = string(body)
	}
	return result, nil
}
