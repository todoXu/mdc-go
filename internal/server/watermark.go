package server

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"

	"mdcng-go/internal/web"
)

type watermarkMark struct{ key, file string }

func watermarkBool(config map[string]any, key string) bool {
	value, _ := config[key].(bool)
	return value
}
func watermarkNumber(config map[string]any, key string, fallback float64) float64 {
	value, ok := config[key].(float64)
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
		return fallback
	}
	return value
}
func selectedWatermarks(flags filenameFlags, config map[string]any) []watermarkMark {
	marks := []watermarkMark{}
	if flags.HasSubtitle && watermarkBool(config, "watermark_subtitle") {
		marks = append(marks, watermarkMark{"watermark_subtitle", "sub.png"})
	}
	kinds := map[string]watermarkMark{
		"is_ltx_hack": {"watermark_ltx", "ltx.png"}, "is_lada_hack": {"watermark_lada", "lada.png"}, "is_jav_hack": {"watermark_jav", "jav.png"},
		"is_other_hack": {"watermark_hack", "hack.png"}, "is_uncensored": {"watermark_uncensored", "wuma.png"}, "is_censored": {"watermark_censored", "youma.png"},
	}
	if mark, ok := kinds[flags.Mosaic]; ok && watermarkBool(config, mark.key) {
		marks = append(marks, mark)
	}
	if watermarkBool(config, "watermark_resolution") {
		if file := map[string]string{"4K": "4k.png", "8K": "8k.png"}[strings.ToUpper(flags.Resolution)]; file != "" {
			marks = append(marks, watermarkMark{"watermark_resolution", file})
		}
	}
	return marks
}

func decodeWatermarkImage(data []byte) (image.Image, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("无法读取图片（支持 JPEG、PNG、WebP、GIF）：%w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return nil, fmt.Errorf("图片尺寸超出限制")
	}
	picture, _, err := image.Decode(bytes.NewReader(data))
	return picture, err
}

func resizeBadge(source image.Image, width, height int) *image.RGBA {
	bounds := source.Bounds()
	input := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(input, input.Bounds(), source, bounds.Min, draw.Src)
	out := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		sy := math.Max(0, (float64(y)+0.5)*float64(bounds.Dy())/float64(height)-0.5)
		y0 := int(sy)
		y1 := min(y0+1, bounds.Dy()-1)
		fy := sy - float64(y0)
		for x := 0; x < width; x++ {
			sx := math.Max(0, (float64(x)+0.5)*float64(bounds.Dx())/float64(width)-0.5)
			x0 := int(sx)
			x1 := min(x0+1, bounds.Dx()-1)
			fx := sx - float64(x0)
			a, b, c, d := input.RGBAAt(x0, y0), input.RGBAAt(x1, y0), input.RGBAAt(x0, y1), input.RGBAAt(x1, y1)
			blend := func(a, b, c, d uint8) uint8 {
				return uint8(math.Round((float64(a)*(1-fx)+float64(b)*fx)*(1-fy) + (float64(c)*(1-fx)+float64(d)*fx)*fy))
			}
			out.SetRGBA(x, y, color.RGBA{blend(a.R, b.R, c.R, d.R), blend(a.G, b.G, c.G, d.G), blend(a.B, b.B, c.B, d.B), blend(a.A, b.A, c.A, d.A)})
		}
	}
	return out
}

func applyWatermarks(ctx context.Context, data []byte, ext, kind string, flags filenameFlags, config map[string]any) ([]byte, error) {
	if !watermarkBool(config, "enabled") || !watermarkBool(config, kind) {
		return data, nil
	}
	marks := selectedWatermarks(flags, config)
	if len(marks) == 0 {
		return data, nil
	}
	source, err := decodeWatermarkImage(data)
	if err != nil {
		return nil, err
	}
	width, height := source.Bounds().Dx(), source.Bounds().Dy()
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), source, source.Bounds().Min, draw.Src)
	scale := watermarkNumber(config, "scale", 14)
	if scale < 1 || scale > 100 {
		return nil, fmt.Errorf("水印缩放倍率应为 1～100")
	}

	ox := int(math.Round(watermarkNumber(config, "offset_x", 0) / 1000 * float64(width)))
	oy := int(math.Round(watermarkNumber(config, "offset_y", 0) / 1000 * float64(height)))
	gap := int(math.Round(watermarkNumber(config, "gap", 0)))

	if ox < 0 || oy < 0 || gap < 0 || ox >= width || oy >= height {
		return nil, fmt.Errorf("水印偏移或间距无效")
	}
	start := int(watermarkNumber(config, "start", 0))
	if start < 0 || start > 3 {
		return nil, fmt.Errorf("水印起始位置无效")
	}
	mode, _ := config["mode"].(string)
	if mode == "" {
		mode = "Stack"
	}
	if mode != "Stack" && mode != "Clockwise" && mode != "CounterClockwise" {
		return nil, fmt.Errorf("水印布局方式无效")
	}
	folder, _ := config["custom_folder"].(string)
	occupied := map[int]int{}
	auto := 0
	for _, mark := range marks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var raw []byte
		if folder != "" {
			raw, err = os.ReadFile(filepath.Join(folder, mark.file))
		} else {
			raw, err = web.WatermarkAsset(mark.file)
		}
		if err != nil {
			return nil, fmt.Errorf("读取水印 %s：%w", mark.file, err)
		}
		if len(raw) > 10<<20 {
			return nil, fmt.Errorf("水印图片过大：%s", mark.file)
		}
		badge, err := decodeWatermarkImage(raw)
		if err != nil {
			return nil, err
		}
		bh := max(1, int(math.Round(float64(height)/scale)))
		bw := max(1, int(math.Round(float64(bh)*float64(badge.Bounds().Dx())/float64(badge.Bounds().Dy()))))
		if bw > width-ox || bh > height-oy {
			factor := math.Min(float64(width-ox)/float64(bw), float64(height-oy)/float64(bh))
			bw = max(1, int(float64(bw)*factor))
			bh = max(1, int(float64(bh)*factor))
		}
		corner := int(watermarkNumber(config, mark.key+"_fixed", 5))
		if corner == 5 {
			direction := 1
			if mode == "CounterClockwise" {
				direction = -1
			}
			corner = (start + direction*auto + 4) % 4
			auto++
		}
		if corner < 0 || corner > 3 {
			return nil, fmt.Errorf("水印固定位置无效")
		}
		x, y := ox, oy
		if corner == 1 || corner == 2 {
			x = width - ox - bw
		}
		if corner == 2 || corner == 3 {
			y = height - oy - bh
		}
		distance := occupied[corner]
		horizontal := mode == "Stack" || (mode == "Clockwise" && (corner == 0 || corner == 2)) || (mode == "CounterClockwise" && (corner == 1 || corner == 3))
		if horizontal {
			if corner == 1 || corner == 2 {
				x -= distance
			} else {
				x += distance
			}
			occupied[corner] += bw + gap
		} else {
			if corner == 2 || corner == 3 {
				y -= distance
			} else {
				y += distance
			}
			occupied[corner] += bh + gap
		}
		if x < 0 || y < 0 || x+bw > width || y+bh > height {
			return nil, fmt.Errorf("水印超出图片边界，请调整缩放、偏移或位置")
		}
		resized := resizeBadge(badge, bw, bh)
		draw.Draw(canvas, image.Rect(x, y, x+bw, y+bh), resized, image.Point{}, draw.Over)
		traceScrape(ctx, "写入水印", fmt.Sprintf("%s：%s，位置=%d，尺寸=%d×%d", kind, mark.file, corner, bw, bh))
	}
	var output bytes.Buffer
	if ext == ".png" {
		err = png.Encode(&output, canvas)
	} else if ext == ".jpg" {
		err = jpeg.Encode(&output, canvas, &jpeg.Options{Quality: 95})
	} else {
		return nil, fmt.Errorf("暂不支持写入 %s 水印", ext)
	}
	if err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
