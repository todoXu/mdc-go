package server

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"mdcng-go/internal/provider"
)

// Fetch each source and image at most once per execution. Only field-specific
// configured sources participate; an empty order does not enable other plugins.
func (s *Server) prepareScrapeImages(ctx context.Context, path, base string, meta *provider.Metadata, provenance map[string]string, sources map[string]provider.Metadata, settings scrapeSettings, strict bool) (map[string][]byte, []string, error) {
	var priorities provider.Priorities
	if settings.Priorities != nil {
		priorities = provider.Priorities{ByFields: maps.Clone(settings.Priorities.ByFields)}
	} else {
		current, err := s.currentPriorities()
		if err != nil {
			return nil, nil, err
		}
		priorities = current
	}
	configs := maps.Clone(settings.ProviderConfigs)
	if configs == nil {
		s.mu.RLock()
		configs = maps.Clone(s.providerConfigs)
		s.mu.RUnlock()
	}
	if sources == nil {
		sources = map[string]provider.Metadata{}
	}
	if provenance == nil {
		provenance = map[string]string{}
	}
	writes := map[string][]byte{}
	warnings := []string{}
	fetchErrors := map[string]error{}
	images := map[string]downloadedImage{}
	imageErrors := map[string]error{}
	bytesPrepared := 0
	refererFor := func(id string) string {
		if sources[id].SourceURL != "" {
			return sources[id].SourceURL
		}
		return meta.SourceURL
	}
	read := func(id, address, referer string) (downloadedImage, error) {
		key := id + "\x00" + address
		if picture, ok := images[key]; ok {
			return picture, nil
		}
		if err := imageErrors[key]; err != nil {
			return downloadedImage{}, err
		}
		if err := ctx.Err(); err != nil {
			return downloadedImage{}, err
		}
		source, err := s.providers.Get(id)
		var picture provider.Image
		if err == nil && !configs[id].Enabled {
			err = fmt.Errorf("数据源未启用")
		}
		if err == nil {
			traceScrape(ctx, "下载图片", "读取 ["+id+"]："+address)
			picture, err = source.ReadImage(ctx, configs[id], address, referer)
		}
		var inspected downloadedImage
		if err == nil {
			inspected, err = inspectImage(picture.Body)
		}
		if err == nil && bytesPrepared+len(picture.Body) > 128<<20 {
			err = fmt.Errorf("本次图片总大小超出 128 MB 限制")
		}
		if err != nil {
			imageErrors[key] = err
			return downloadedImage{}, err
		}
		inspected.provider, inspected.url = id, address
		bytesPrepared += len(picture.Body)
		images[key] = inspected
		return inspected, nil
	}
	loadSource := func(id string) (provider.Metadata, error) {
		if data, ok := sources[id]; ok {
			return data, nil
		}
		if err := fetchErrors[id]; err != nil {
			return provider.Metadata{}, err
		}
		if err := ctx.Err(); err != nil {
			return provider.Metadata{}, err
		}
		if !configs[id].Enabled {
			return provider.Metadata{}, fmt.Errorf("数据源未启用")
		}
		source, err := s.providers.Get(id)
		var data provider.Metadata
		if err == nil {
			traceScrape(ctx, "下载图片", "查询备用图片来源："+id)
			data, err = source.Fetch(ctx, configs[id], meta.Number)
		}
		if err == nil && strings.ReplaceAll(strings.ToUpper(data.Number), "-", "") != strings.ReplaceAll(strings.ToUpper(meta.Number), "-", "") {
			err = fmt.Errorf("图片来源返回番号不一致")
		}
		if err != nil {
			fetchErrors[id] = err
			return provider.Metadata{}, err
		}
		sources[id] = data
		return data, nil
	}
	// posterFromCrop 记录最近一次选中的海报是否由缩略图裁剪得到。
	posterFromCrop := false
	// posterFromSource 取某个来源的海报：先用它自己的竖版海报；没有，就用它自己的缩略图按「常规作品海报裁剪方式」裁剪。
	// cropped 为 true 表示海报是由缩略图裁剪得到的。两者都没有时返回错误，由调用方尝试下一个来源。
	posterFromSource := func(id string, data provider.Metadata, address string) (downloadedImage, bool, error) {
		var lastErr error
		if address != "" {
			candidate, err := read(id, address, data.SourceURL)
			if err == nil && candidate.height > candidate.width {
				return candidate, false, nil
			}
			if err == nil {
				err = fmt.Errorf("海报 %d×%d 不是竖版", candidate.width, candidate.height)
			}
			lastErr = err
		}
		method := posterCropMethod(meta.Number, path, settings)
		if method == "None" || data.Cover == "" {
			if lastErr == nil {
				lastErr = fmt.Errorf("没有竖版海报，也没有可裁剪的缩略图")
			}
			return downloadedImage{}, false, lastErr
		}
		cover, err := read(id, data.Cover, data.SourceURL)
		if err != nil {
			return downloadedImage{}, false, err
		}
		cropped, err := cropPoster(ctx, cover, method, settings.Download.Aspect)
		if err != nil {
			return downloadedImage{}, false, err
		}
		cropped.provider, cropped.url = id, data.Cover
		return cropped, true, nil
	}
	selectImage := func(field string, quality bool) (downloadedImage, error) {
		if field == "poster" {
			posterFromCrop = false
		}
		order := append([]string{}, priorities.ByFields[field]...)
		selected := provenance[field]
		if selected == "" && meta.Provider != "merged" {
			selected = meta.Provider
		}
		if selected != "" {
			// 已选定的来源（人工在编辑元数据时选择的，或上次刮削的实际来源）排在最前面，
			// 不再按字段优先级顺序，否则它排在后面时会被其他来源替代。
			rest := make([]string, 0, len(order))
			for _, id := range order {
				if id != selected {
					rest = append(rest, id)
				}
			}
			order = append([]string{selected}, rest...)
		}
		var best downloadedImage
		seen := map[string]bool{}
		for _, id := range order {
			if seen[id] {
				continue
			}
			seen[id] = true
			if err := ctx.Err(); err != nil {
				return downloadedImage{}, err
			}
			data, ok := sources[id]
			var fetchErr error
			if !ok && id != selected {
				data, fetchErr = loadSource(id)
			}
			address := data.Cover
			if field == "poster" {
				address = data.Poster
			}
			if id == selected {
				address = meta.Cover
				if field == "poster" {
					if meta.PosterFromCover {
						address = data.Poster
					} else {
						address = meta.Poster
					}
				}
				if data.SourceURL == "" {
					data.SourceURL = meta.SourceURL
				}
			}
			if fetchErr != nil {
				// 数据源失败已在详细数据中显示，这里不再重复记为警告；该来源的图片直接跳过。
				continue
			}
			var candidate downloadedImage
			var err error
			cropped := false
			if field == "poster" {
				// 海报按来源处理：本来源没有竖版海报时，用它自己的缩略图裁剪，不足则换下一个来源。
				candidate, cropped, err = posterFromSource(id, data, address)
			} else {
				if address == "" {
					continue
				}
				candidate, err = read(id, address, data.SourceURL)
				if err == nil && field == "cover" && (candidate.width < settings.Download.MinWidth || candidate.height < settings.Download.MinHeight) {
					err = fmt.Errorf("缩略图 %d×%d 低于最小尺寸 %d×%d", candidate.width, candidate.height, settings.Download.MinWidth, settings.Download.MinHeight)
				}
			}
			if err != nil {
				message := field + " [" + id + "]：" + err.Error()
				traceScrape(ctx, "下载图片", message+"，尝试下一来源")
				warnings = append(warnings, message)
				continue
			}
			traceScrape(ctx, "选择图片", fmt.Sprintf("%s [%s]：%d×%d", field, id, candidate.width, candidate.height))
			if best.data == nil || int64(candidate.width)*int64(candidate.height) > int64(best.width)*int64(best.height) {
				best = candidate
				posterFromCrop = cropped && field == "poster"
			}
			if !quality {
				break
			}
		}
		if best.data == nil {
			return best, fmt.Errorf("%s 的全部候选图片不可用，请检查字段优先级、来源和下载设置", field)
		}
		return best, nil
	}
	needCover := settings.Download.Thumb || settings.Download.Fanart || (settings.Download.Poster && (meta.Poster == "" || meta.PosterFromCover || settings.Download.PreferCrop))
	var cover, poster downloadedImage
	var coverErr, posterErr error
	if needCover {
		cover, coverErr = selectImage("cover", settings.Download.Strategy == "Quality")
	}
	if (settings.Download.Thumb || settings.Download.Fanart) && coverErr != nil {
		return nil, warnings, coverErr
	}
	if cover.data != nil {
		meta.Cover = cover.url
		provenance["cover"] = cover.provider
	}
	if settings.Download.Poster {
		poster, posterErr = selectImage("poster", false)
		// 由缩略图裁剪得到的海报不再做独立海报的居中裁剪。
		posterFromCover := posterFromCrop && posterErr == nil
		if posterErr == nil && settings.Download.CropPoster && !posterFromCover {
			poster, posterErr = cropPoster(ctx, poster, "Center", settings.Download.Aspect)
		}
		if posterErr != nil && !needCover {
			cover, coverErr = selectImage("cover", settings.Download.Strategy == "Quality")
		}
		if cover.data != nil && (posterErr != nil || settings.Download.PreferCrop) {
			cropped, err := cropPoster(ctx, cover, posterCropMethod(meta.Number, path, settings), settings.Download.Aspect)
			if err != nil {
				return nil, warnings, err
			}
			if posterErr != nil || int64(cropped.width)*int64(cropped.height) > int64(poster.width)*int64(poster.height) {
				poster, posterErr = cropped, nil
				posterFromCover = true
				traceScrape(ctx, "选择图片", "采用缩略图裁剪生成海报")
			}
		}
		if posterErr != nil {
			return nil, warnings, fmt.Errorf("海报及缩略图裁剪均不可用：%w", posterErr)
		}
		meta.Poster = poster.url
		meta.PosterFromCover = posterFromCover
		provenance["poster"] = poster.provider
	}
	if cover.data != nil {
		meta.Cover = cover.url
		provenance["cover"] = cover.provider
	}
	flags := detectFilenameFlags(path, settings)
	for _, item := range []struct {
		enabled bool
		kind    string
		picture downloadedImage
	}{
		{settings.Download.Thumb, "thumb", cover}, {settings.Download.Fanart, "fanart", cover}, {settings.Download.Poster, "poster", poster},
	} {
		if !item.enabled {
			continue
		}
		picture, err := finalizeScrapeImage(ctx, item.picture, item.kind, flags, settings)
		if err != nil {
			return nil, warnings, fmt.Errorf("生成 %s 失败：%w", item.kind, err)
		}
		writes[scrapeAssetPath(base, item.kind, picture.ext, settings.Naming.ImageWithPrefix)] = picture.data
		traceScrape(ctx, "图片处理", fmt.Sprintf("%s 准备完成 [%s]：%d×%d", item.kind, picture.provider, picture.width, picture.height))
	}
	if settings.Download.ExtraFanarts {
		id := provenance["samples"]
		if id == "" && meta.Provider != "merged" {
			id = meta.Provider
		}
		urls := meta.Samples
		if len(urls) == 0 {
			warnings = append(warnings, "数据源未提供剧照，跳过下载")
		}
		if len(urls) > 32 {
			urls = urls[:32]
			warnings = append(warnings, "剧照数量超过 32 张，仅下载前 32 张")
		}
		seen := map[string]bool{}
		index := 0
		for _, address := range urls {
			if seen[address] || address == "" {
				continue
			}
			seen[address] = true
			index++
			picture, err := read(id, address, refererFor(id))
			if err != nil {
				if ctx.Err() != nil {
					return nil, warnings, ctx.Err()
				}
				warnings = append(warnings, fmt.Sprintf("剧照 %d：%s", index, err))
				continue
			}
			if picture.ext == ".gif" {
				src, err := decodeWatermarkImage(picture.data)
				if err != nil {
					return nil, warnings, err
				}
				picture, err = encodePreparedImage(src, picture)
				if err != nil {
					return nil, warnings, err
				}
			}
			writes[scrapeExtraFanartPath(base, index, picture.ext, settings.Naming.ImageWithPrefix)] = picture.data
			traceScrape(ctx, "下载剧照", fmt.Sprintf("第 %d 张读取完成：%d×%d", index, picture.width, picture.height))
		}
	}
	if settings.Download.Trailer {
		if meta.Trailer == "" {
			warnings = append(warnings, "数据源未提供预告片，跳过下载")
		} else {
			id := provenance["trailer"]
			if id == "" && meta.Provider != "merged" {
				id = meta.Provider
			}
			source, err := s.providers.Get(id)
			var video provider.Image
			if err == nil {
				if !configs[id].Enabled {
					err = fmt.Errorf("数据源未启用")
				} else {
					video, err = source.ReadTrailer(ctx, configs[id], meta.Trailer, refererFor(id))
				}
			}
			if err != nil {
				if ctx.Err() != nil {
					return nil, warnings, ctx.Err()
				}
				if strict {
					return nil, warnings, fmt.Errorf("预告片下载失败：%w", err)
				}
				warnings = append(warnings, "预告片下载失败："+err.Error())
			} else {
				ext := ".mp4"
				if video.ContentType == "video/webm" {
					ext = ".webm"
				}
				writes[scrapeAssetPath(base, "trailer", ext, settings.Naming.ImageWithPrefix)] = video.Body
				traceScrape(ctx, "下载预告片", fmt.Sprintf("下载完成，%d 字节", len(video.Body)))
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, warnings, err
	}
	return writes, warnings, nil
}
