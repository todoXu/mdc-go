package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mdcng-go/internal/provider"
)

type taskRequest struct {
	Number string     `json:"number"`
	URL    string     `json:"url"`
	Flags  *taskFlags `json:"flags,omitempty"`
}

func (s *Server) resolveTaskURL(settings *scrapeSettings, address string) error {
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Host == "" {
		return errors.New("请输入有效的作品网址")
	}
	items, err := s.providers.List()
	if err != nil {
		return err
	}
	for _, item := range items {
		config := s.providerConfig(item.ID)
		if settings.ProviderConfigs != nil {
			if override, ok := settings.ProviderConfigs[item.ID]; ok {
				config = override
			}
		}
		base := config.BaseURL
		if base == "" {
			base = item.BaseURL
		}
		parsed, _ := url.Parse(base)
		if config.Enabled && parsed != nil && strings.EqualFold(u.Host, parsed.Host) {
			settings.OverrideURL, settings.OverrideProvider = address, item.ID
			return nil
		}
	}
	return errors.New("覆盖网址必须属于已启用的数据源")
}

// Reserve the record while preparing resources, so retry/delete cannot race a file commit.
func (s *Server) reserveRecord(id string) (scrapeJob, error) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	for i := range s.jobs {
		job := &s.jobs[i]
		if job.ID != id {
			continue
		}
		if job.Status == 0 || job.Status == 1 || job.Editing {
			return scrapeJob{}, errors.New("记录正在处理，请完成后再操作")
		}
		if job.Metadata == nil {
			return scrapeJob{}, errors.New("记录尚无元数据")
		}
		for _, other := range s.jobs {
			if other.ID != id && (other.Status == 0 || other.Status == 1 || other.Editing) && strings.EqualFold(filepath.Clean(other.Path), filepath.Clean(job.Path)) {
				return scrapeJob{}, errors.New("该文件正在处理")
			}
		}
		job.Editing = true
		s.publishJobChanged(job.ID, true)
		copy := *job
		meta := *job.Metadata
		meta.ImagePreviews, meta.LocalImages = nil, nil
		copy.Metadata = &meta
		copy.Provenance, copy.SourceResults = maps.Clone(job.Provenance), maps.Clone(job.SourceResults)
		if copy.Provenance == nil {
			copy.Provenance = map[string]string{}
		}
		if copy.SourceResults == nil {
			copy.SourceResults = map[string]provider.Metadata{}
		}
		return copy, nil
	}
	return scrapeJob{}, os.ErrNotExist
}

func (s *Server) releaseRecord(id string) {
	s.updateJob(id, func(job *scrapeJob) { job.Editing = false })
	s.saveScrapeHistory()
}

func (s *Server) recordSettings(job scrapeJob) (scrapeSettings, error) {
	for _, directory := range s.currentWatchConfig().Directories {
		if directory.ID == job.DirectoryID {
			return s.scrapeSettingsForDirectory(directory)
		}
	}
	return s.scrapeSettings()
}

var editableMetadata = map[string]bool{
	"number": true, "publish_number": true, "title": true, "original_title": true,
	"outline": true, "original_outline": true, "actors": true, "cover": true, "poster": true,
	"samples": true, "tags": true, "release": true, "runtime": true, "rating": true,
	"director": true, "series": true, "studio": true, "publisher": true, "votes": true, "trailer": true,
}

func (s *Server) editScrapeMetadata(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Fields  map[string]json.RawMessage `json:"fields"`
		Sources map[string]string          `json:"sources"`
	}
	if !decode(w, r, &request) {
		return
	}
	job, err := s.reserveRecord(r.PathValue("id"))
	if err != nil {
		fail(w, 409, "record_unavailable", err.Error())
		return
	}
	defer s.releaseRecord(job.ID)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	settings, err := s.recordSettings(job)
	if err != nil {
		fail(w, 500, "config_error", err.Error())
		return
	}
	raw, _ := json.Marshal(job.Metadata)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	changed := map[string]bool{}
	for key, value := range request.Fields {
		if !editableMetadata[key] {
			fail(w, 400, "invalid_field", "不支持编辑字段："+key)
			return
		}
		if key == "actors" || key == "tags" || key == "samples" {
			var list []string
			if json.Unmarshal(value, &list) != nil || len(list) > 200 {
				fail(w, 400, "invalid_field", "列表格式错误："+key)
				return
			}
		} else {
			var text string
			if json.Unmarshal(value, &text) != nil || len(text) > 100000 {
				fail(w, 400, "invalid_field", "字段格式错误："+key)
				return
			}
		}
		changed[key] = string(fields[key]) != string(value)
		fields[key] = value
		if sourceID := request.Sources[key]; sourceID != "" && sourceID != "manual" {
			source, exists := job.SourceResults[sourceID]
			if !exists {
				fail(w, 400, "invalid_source", "来源不存在："+sourceID)
				return
			}
			data, _ := json.Marshal(source)
			var candidate map[string]json.RawMessage
			_ = json.Unmarshal(data, &candidate)
			var a, b any
			_ = json.Unmarshal(value, &a)
			_ = json.Unmarshal(candidate[key], &b)
			va, _ := json.Marshal(a)
			vb, _ := json.Marshal(b)
			if string(va) != string(vb) {
				fail(w, 400, "invalid_source", "字段值与所选来源不一致："+key)
				return
			}
			job.Provenance[key] = sourceID
		} else if changed[key] && key != "cover" && key != "poster" && key != "samples" && key != "trailer" {
			job.Provenance[key] = "manual"
		}
	}
	raw, _ = json.Marshal(fields)
	var meta provider.Metadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		fail(w, 400, "invalid_metadata", err.Error())
		return
	}
	if meta.Number, err = provider.NormalizeNumber(meta.Number); err != nil || strings.TrimSpace(meta.Title) == "" {
		fail(w, 400, "invalid_metadata", "番号和标题不能为空，番号须符合格式")
		return
	}
	if changed["poster"] {
		meta.PosterFromCover = false
	}
	output := job.Output
	// 命名相关字段变化时，目录和文件名都要按新元数据重新生成，改为排队重新整理（见 reorganize_auto.go）。
	namingChanged := changed["actors"] || changed["number"] || changed["title"] || changed["release"] || changed["studio"] || changed["publisher"] || changed["series"]
	autoReorganize := namingChanged && job.Status == 2 && output != ""
	if autoReorganize {
		// 保存时就检查目标是否可用，避免接口返回成功后，后台整理才因冲突失败。
		target, err := plannedMediaTarget(output, meta, settings)
		if err != nil {
			fail(w, 400, "invalid_name", err.Error())
			return
		}
		if _, err := os.Lstat(target); err == nil && !samePath(target, output) {
			fail(w, 400, "save_failed", "目标视频文件已存在："+target)
			return
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			fail(w, 400, "save_failed", err.Error())
			return
		}
	}
	if job.Status == 2 && output != "" && !autoReorganize {
		stem, err := buildVideoStem(output, meta, settings)
		if err != nil {
			fail(w, 400, "invalid_name", err.Error())
			return
		}
		newOutput := filepath.Join(filepath.Dir(output), stem+filepath.Ext(output))
		base := strings.TrimSuffix(newOutput, filepath.Ext(newOutput))
		resources := settings
		resources.Download.Thumb = settings.Download.Thumb && changed["cover"]
		resources.Download.Fanart = settings.Download.Fanart && changed["cover"]
		resources.Download.Poster = settings.Download.Poster && (changed["poster"] || (meta.PosterFromCover && changed["cover"]))
		resources.Download.ExtraFanarts = settings.Download.ExtraFanarts && changed["samples"]
		resources.Download.Trailer = settings.Download.Trailer && changed["trailer"]
		writes, warnings, err := s.prepareScrapeImages(ctx, output, base, &meta, job.Provenance, job.SourceResults, resources, true)
		if err != nil {
			fail(w, 400, "resource_error", err.Error())
			return
		}
		job.NFOTags, err = addMetadataWritesWithTrace(writes, meta, base, settings)
		if err == nil {
			err = commitScrapeRetryManagedNFO(ctx, output, newOutput, writes, true, settings.Naming.ImageWithPrefix)
		}
		if err != nil {
			fail(w, 400, "save_failed", err.Error())
			return
		}
		output, job.Warnings = newOutput, append(job.Warnings, warnings...)
	}
	s.updateJob(job.ID, func(item *scrapeJob) {
		if job.Status == 2 && job.Output != "" {
			item.NFOTags = job.NFOTags
		}
		item.Metadata, item.Number = &meta, meta.Number
		switch {
		case autoReorganize:
			// 排队重新整理：列表继续显示上次写入的演员，整理完成后再更新。
		case job.Status == 2 && output != "":
			// 已原地写入 NFO：存映射后的名字，与文件一致。
			item.Actors = applyActorMapping(meta.Actors, settings)
		default:
			// 尚未写入：保留用户输入的原始名字。
			item.Actors = append([]string{}, meta.Actors...)
		}
		item.Provenance, item.SourceResults, item.Output, item.Warnings = job.Provenance, job.SourceResults, output, job.Warnings
		item.Logs = append(item.Logs, scrapeEvent{time.Now().Format(time.RFC3339Nano), "编辑元数据", "已更新元数据；成功记录已同步本地文件和 NFO"})
	})
	if autoReorganize {
		// 必须在 updateJob 之后调用，此时记录的元数据已是新值；queueReorganize 不持有 jobsMu。
		s.queueReorganize(job.ID, output)
	} else if job.Status == 2 && job.Output != "" {
		// 原地改写了 NFO 和图片，让 Emby 重新读取。重新整理的情况由任务完成时触发。
		s.requestEmbyRefresh()
	}
	writeJSON(w, 200, object{"success": true})
}

type cropSourceRequest struct {
	Source   string `json:"source"`
	Provider string `json:"provider"`
	Aspect   string `json:"aspect"`
}

func (s *Server) readCropSource(ctx context.Context, job scrapeJob, request cropSourceRequest, settings scrapeSettings) (downloadedImage, error) {
	var data []byte
	if strings.HasPrefix(request.Source, "local:") {
		kind := strings.TrimPrefix(request.Source, "local:")
		if kind != "thumb" && kind != "poster" && kind != "fanart" {
			return downloadedImage{}, errors.New("本地图片类型不正确")
		}
		if job.Status != 2 || job.Output == "" {
			return downloadedImage{}, errors.New("没有本地图片")
		}
		base := strings.TrimSuffix(job.Output, filepath.Ext(job.Output))
		for _, ext := range []string{".jpg", ".png", ".webp"} {
			for _, path := range scrapeAssetCandidateOrder(base, kind, ext, settings.Naming.ImageWithPrefix) {
				info, err := os.Lstat(path)
				if err != nil || !info.Mode().IsRegular() {
					continue
				}
				if info.Size() > 20<<20 {
					return downloadedImage{}, errors.New("图片超过 20 MB")
				}
				data, err = os.ReadFile(path)
				if err != nil {
					return downloadedImage{}, err
				}
				return inspectImage(data)
			}
		}
		return downloadedImage{}, errors.New("本地图片不存在")
	}
	if request.Source != "cover" && request.Source != "poster" {
		return downloadedImage{}, errors.New("请选择封面或海报来源")
	}
	id := request.Provider
	meta := *job.Metadata
	if id != "" {
		var ok bool
		meta, ok = job.SourceResults[id]
		if !ok {
			return downloadedImage{}, errors.New("来源不存在")
		}
	} else {
		id = job.Provenance[request.Source]
	}
	address := meta.Cover
	if request.Source == "poster" {
		address = meta.Poster
	}
	if address == "" {
		return downloadedImage{}, errors.New("来源没有提供该图片")
	}
	config := s.providerConfig(id)
	if !config.Enabled {
		return downloadedImage{}, errors.New("图片来源未启用")
	}
	source, err := s.providers.Get(id)
	if err != nil {
		return downloadedImage{}, err
	}
	picture, err := source.ReadImage(ctx, config, address, meta.SourceURL)
	if err != nil {
		return downloadedImage{}, err
	}
	return inspectImage(picture.Body)
}

func (s *Server) scrapeCropSource(w http.ResponseWriter, r *http.Request) {
	var request cropSourceRequest
	if !decode(w, r, &request) {
		return
	}
	job, err := s.reserveRecord(r.PathValue("id"))
	if err != nil {
		fail(w, 409, "record_unavailable", err.Error())
		return
	}
	defer s.releaseRecord(job.ID)
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	settings, err := s.recordSettings(job)
	if err != nil {
		fail(w, 500, "config_error", "无法读取裁剪设置")
		return
	}
	picture, err := s.readCropSource(ctx, job, request, settings)
	if err != nil {
		fail(w, 400, "image_error", err.Error())
		return
	}
	method := posterCropMethod(job.Number, job.Path, settings)
	aspect := request.Aspect
	if aspect == "" {
		aspect = settings.Download.Aspect
	}
	if aspect == "" {
		aspect = "Full"
	}
	src, err := decodeWatermarkImage(picture.data)
	if err != nil {
		fail(w, 400, "image_error", err.Error())
		return
	}
	rect, err := posterCropRectangle(ctx, src, picture.width, picture.height, method, aspect)
	if err != nil {
		fail(w, 400, "crop_error", err.Error())
		return
	}
	writeJSON(w, 200, object{
		"body_base64": base64.StdEncoding.EncodeToString(picture.data), "content_type": http.DetectContentType(picture.data),
		"width": picture.width, "height": picture.height, "crop_method": method, "crop_aspect": aspect,
		"crop_x": rect.X, "crop_y": rect.Y, "crop_width": rect.Width, "crop_height": rect.Height,
		"face_found": rect.FaceFound, "face_x": rect.FaceX, "face_y": rect.FaceY,
	})
}

func (s *Server) cropScrapePoster(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Body         string `json:"body_base64"`
		X            int    `json:"x"`
		Y            int    `json:"y"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		ReplaceThumb bool   `json:"replace_thumb"`
		Subtitle     bool   `json:"subtitle"`
		Mosaic       string `json:"mosaic"`
		Resolution   string `json:"resolution"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 29<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		fail(w, 400, "invalid_body", "裁剪数据格式错误或超过限制")
		return
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		fail(w, 400, "invalid_body", "请求必须为一个对象")
		return
	}
	data, err := base64.StdEncoding.DecodeString(request.Body)
	if err != nil || len(data) > 20<<20 {
		fail(w, 400, "invalid_image", "图片无效或超过 20 MB")
		return
	}
	picture, err := inspectImage(data)
	if err != nil {
		fail(w, 400, "invalid_image", err.Error())
		return
	}
	if request.Width < 1 || request.Height < 1 || request.X < 0 || request.Y < 0 || request.X > picture.width-request.Width || request.Y > picture.height-request.Height {
		fail(w, 400, "invalid_crop", "裁剪范围超出原图")
		return
	}
	if request.Resolution != "" && request.Resolution != "4K" && request.Resolution != "8K" {
		fail(w, 400, "invalid_flags", "分辨率标识无效")
		return
	}
	validMosaic := map[string]bool{"": true, "is_censored": true, "is_uncensored": true, "is_ltx_hack": true, "is_lada_hack": true, "is_jav_hack": true, "is_other_hack": true}
	if !validMosaic[request.Mosaic] {
		fail(w, 400, "invalid_flags", "水印类型无效")
		return
	}
	job, err := s.reserveRecord(r.PathValue("id"))
	if err != nil {
		fail(w, 409, "record_unavailable", err.Error())
		return
	}
	defer s.releaseRecord(job.ID)
	if job.Status != 2 || job.Output == "" {
		fail(w, 409, "invalid_status", "请先完成刮削再保存裁剪")
		return
	}
	settings, err := s.recordSettings(job)
	if err != nil {
		fail(w, 500, "config_error", err.Error())
		return
	}
	ctx := r.Context()
	src, err := decodeWatermarkImage(data)
	if err != nil {
		fail(w, 400, "invalid_image", err.Error())
		return
	}
	sourcePicture := picture
	rect := image.Rect(request.X, request.Y, request.X+request.Width, request.Y+request.Height)
	canvas := image.NewRGBA(image.Rect(0, 0, request.Width, request.Height))
	draw.Draw(canvas, canvas.Bounds(), src, rect.Min.Add(src.Bounds().Min), draw.Src)
	picture, err = encodePreparedImage(canvas, picture)
	if err != nil {
		fail(w, 400, "crop_error", err.Error())
		return
	}
	settings.Watermark = maps.Clone(settings.Watermark)
	if settings.Watermark == nil {
		settings.Watermark = map[string]any{}
	}
	for _, key := range []string{"enabled", "poster", "thumb", "watermark_subtitle", "watermark_ltx", "watermark_lada", "watermark_jav", "watermark_hack", "watermark_uncensored", "watermark_censored", "watermark_resolution"} {
		settings.Watermark[key] = true
	}
	flags := filenameFlags{HasSubtitle: request.Subtitle, Mosaic: request.Mosaic, Resolution: request.Resolution}
	base := strings.TrimSuffix(job.Output, filepath.Ext(job.Output))
	writes := map[string][]byte{}
	kinds := []string{"poster"}
	if request.ReplaceThumb {
		kinds = append(kinds, "thumb")
	}
	for _, kind := range kinds {
		input := picture
		if kind == "thumb" {
			input = sourcePicture
		}
		final, err := finalizeScrapeImage(ctx, input, kind, flags, settings)
		if err != nil {
			fail(w, 400, "watermark_error", err.Error())
			return
		}
		writes[scrapeAssetPath(base, kind, final.ext, settings.Naming.ImageWithPrefix)] = final.data
	}
	if err := commitScrapeRetryManagedNFO(ctx, job.Output, job.Output, writes, false, settings.Naming.ImageWithPrefix); err != nil {
		fail(w, 400, "save_failed", err.Error())
		return
	}
	s.updateJob(job.ID, func(item *scrapeJob) {
		item.Logs = append(item.Logs, scrapeEvent{time.Now().Format(time.RFC3339Nano), "手动裁剪", fmt.Sprintf("已替换海报，选区 %d×%d；同时替换缩略图=%t（使用未裁剪原图）", request.Width, request.Height, request.ReplaceThumb)})
	})
	writeJSON(w, 200, object{"success": true})
}
