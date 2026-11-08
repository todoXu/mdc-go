package server

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"

	xdraw "golang.org/x/image/draw"

	"mdcng-go/internal/web"
)

// watermarkMark 是一个要添加的水印。系统水印的位置读水印设置里的 key+"_fixed"；
// 自定义水印（custom 为 true）的位置是规则自己的 corner，图片在自定义水印目录中。
type watermarkMark struct {
	key, file string
	custom    bool
	corner    int
}

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

// selectedWatermarks 返回要添加的水印。系统水印受系统水印设置里的「启用系统水印」控制，
// 自定义规则的水印受 customEnabled（自定义规则页的开关）控制，两者互相独立。
func selectedWatermarks(flags filenameFlags, config map[string]any, customEnabled bool) []watermarkMark {
	marks := []watermarkMark{}
	if watermarkBool(config, "enabled") {
		marks = systemWatermarks(flags, config)
	}
	if !customEnabled {
		return marks
	}
	// 自定义规则的水印排在系统水印之后，同一张图只加一次。
	seen := map[string]bool{}
	for _, rule := range flags.Custom {
		if rule.Watermark == "" || seen[rule.Watermark] {
			continue
		}
		seen[rule.Watermark] = true
		marks = append(marks, watermarkMark{key: "custom", file: rule.Watermark, custom: true, corner: rule.Corner})
	}
	return marks
}

func systemWatermarks(flags filenameFlags, config map[string]any) []watermarkMark {
	marks := []watermarkMark{}
	if flags.HasSubtitle && watermarkBool(config, "watermark_subtitle") {
		marks = append(marks, watermarkMark{key: "watermark_subtitle", file: "sub.png"})
	}
	kinds := map[string]watermarkMark{
		"is_ltx_hack": {key: "watermark_ltx", file: "ltx.png"}, "is_lada_hack": {key: "watermark_lada", file: "lada.png"}, "is_jav_hack": {key: "watermark_jav", file: "jav.png"},
		"is_other_hack": {key: "watermark_hack", file: "hack.png"}, "is_uncensored": {key: "watermark_uncensored", file: "wuma.png"}, "is_censored": {key: "watermark_censored", file: "youma.png"},
	}
	if mark, ok := kinds[flags.Mosaic]; ok && watermarkBool(config, mark.key) {
		marks = append(marks, mark)
	}
	if watermarkBool(config, "watermark_resolution") {
		if file := map[string]string{"4K": "4k.png", "8K": "8k.png"}[strings.ToUpper(flags.Resolution)]; file != "" {
			marks = append(marks, watermarkMark{key: "watermark_resolution", file: file})
		}
	}
	return marks
}

// watermarkCorners 按水印设置计算每个水印所在的角：0 左上、1 右上、2 右下、3 左下。
// 位置为「自动」的水印从起始位置开始，按布局方向依次占用各角。
func watermarkCorners(marks []watermarkMark, config map[string]any) ([]int, error) {
	start := int(watermarkNumber(config, "start", 0))
	if start < 0 || start > 3 {
		return nil, fmt.Errorf("水印起始位置无效")
	}
	mode, _ := config["mode"].(string)
	direction := 1
	if mode == "CounterClockwise" {
		direction = -1
	}
	corners := make([]int, len(marks))
	auto := 0
	for i, mark := range marks {
		corner := mark.corner
		if !mark.custom {
			corner = int(watermarkNumber(config, mark.key+"_fixed", 5))
		}
		if corner == 5 {
			corner = (start + direction*auto + 4) % 4
			auto++
		}
		if corner < 0 || corner > 3 {
			return nil, fmt.Errorf("水印固定位置无效")
		}
		corners[i] = corner
	}
	return corners, nil
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
	out := image.NewRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(out, out.Bounds(), source, source.Bounds(), xdraw.Src, nil)
	return out
}

// customWatermarkOptions 是自定义规则水印的开关和图片目录。
type customWatermarkOptions struct {
	Enabled bool
	Dir     string
}

// applyWatermarks 按水印设置把水印叠加到图片上。图片类型（poster / thumb / fanart）的勾选
// 对系统水印和自定义水印都生效。
func applyWatermarks(ctx context.Context, data []byte, ext, kind string, flags filenameFlags, config map[string]any, custom customWatermarkOptions) ([]byte, error) {
	if !watermarkBool(config, kind) {
		return data, nil
	}
	customDir := custom.Dir
	marks := selectedWatermarks(flags, config, custom.Enabled)
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
	mode, _ := config["mode"].(string)
	if mode == "" {
		mode = "Stack"
	}
	if mode != "Stack" && mode != "Clockwise" && mode != "CounterClockwise" {
		return nil, fmt.Errorf("水印布局方式无效")
	}
	corners, err := watermarkCorners(marks, config)
	if err != nil {
		return nil, err
	}
	folder, _ := config["custom_folder"].(string)
	occupied := map[int]int{}
	for i, mark := range marks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var raw []byte
		switch {
		case mark.custom && customDir == "":
			err = fmt.Errorf("没有自定义水印目录")
		case mark.custom:
			raw, err = os.ReadFile(filepath.Join(customDir, mark.file))
		case folder != "":
			raw, err = os.ReadFile(filepath.Join(folder, mark.file))
		default:
			raw, err = web.WatermarkAsset(mark.file)
		}
		if err != nil && mark.custom {
			// 自定义水印图片被删除时跳过这一个水印，不让整个刮削失败。
			slog.Warn("[watermark] 自定义水印不可用，已跳过", "file", mark.file, "error", err)
			traceScrape(ctx, "写入水印", fmt.Sprintf("%s：自定义水印 %s 不可用，已跳过", kind, mark.file))
			continue
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
		corner := corners[i]
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
