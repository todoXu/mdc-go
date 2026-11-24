package server

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"sort"
	"strings"
	"sync"

	pigo "github.com/esimov/pigo/core"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Pigo v1.4.6 cascade/facefinder, SHA256 d8014993e7298c7b1865d1f8b855d6dbf4ec5c808bf879e2091ab6837abf90cd.
//
//go:embed assets/facefinder
var faceCascade []byte

var faceClassifier = sync.OnceValues(func() (*pigo.Pigo, error) { return pigo.NewPigo().Unpack(faceCascade) })

type downloadedImage struct {
	data          []byte
	ext           string
	width, height int
	provider, url string
}

func inspectImage(data []byte) (downloadedImage, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return downloadedImage{}, fmt.Errorf("无法解码图片：%w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return downloadedImage{}, fmt.Errorf("图片尺寸超出限制")
	}
	ext := map[string]string{"jpeg": ".jpg", "png": ".png", "webp": ".webp", "gif": ".gif"}[format]
	if ext == "" {
		return downloadedImage{}, fmt.Errorf("不支持的图片格式：%s", format)
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return downloadedImage{}, fmt.Errorf("图片内容损坏：%w", err)
	}
	return downloadedImage{data: data, ext: ext, width: config.Width, height: config.Height}, nil
}

func encodePreparedImage(src image.Image, original downloadedImage) (downloadedImage, error) {
	var out bytes.Buffer
	var err error
	if original.ext == ".png" {
		err = png.Encode(&out, src)
	} else {
		original.ext = ".jpg"
		err = jpeg.Encode(&out, src, &jpeg.Options{Quality: 95})
	}
	if err != nil {
		return downloadedImage{}, err
	}
	original.data, original.width, original.height = out.Bytes(), src.Bounds().Dx(), src.Bounds().Dy()
	return original, nil
}

// Find the largest confident face on a bounded image; the model only detects a
// face location, never the identity of the person.
func detectPosterFace(ctx context.Context, src image.Image) (image.Point, bool, error) {
	classifier, err := faceClassifier()
	if err != nil {
		return image.Point{}, false, err
	}
	bounds := src.Bounds()
	scale := math.Min(1, 800.0/float64(max(bounds.Dx(), bounds.Dy())))
	w, h := max(1, int(float64(bounds.Dx())*scale)), max(1, int(float64(bounds.Dy())*scale))
	small := image.NewGray(image.Rect(0, 0, w, h))
	xdraw.ApproxBiLinear.Scale(small, small.Bounds(), src, bounds, draw.Src, nil)
	if err := ctx.Err(); err != nil {
		return image.Point{}, false, err
	}
	detections := classifier.RunCascade(pigo.CascadeParams{MinSize: 24, MaxSize: min(w, h), ShiftFactor: 0.1, ScaleFactor: 1.15, ImageParams: pigo.ImageParams{Pixels: small.Pix, Rows: h, Cols: w, Dim: small.Stride}}, 0)
	if err := ctx.Err(); err != nil {
		return image.Point{}, false, err
	}
	detections = classifier.ClusterDetections(detections, 0.2)
	var best pigo.Detection
	for _, face := range detections {
		if face.Q >= 5 && (best.Q < 5 || face.Scale > best.Scale || (face.Scale == best.Scale && face.Q > best.Q)) {
			best = face
		}
	}
	if best.Q < 5 {
		return image.Point{}, false, nil
	}
	return image.Pt(int(float64(best.Col)/scale), int(float64(best.Row)/scale)), true, nil
}

func cropPoster(ctx context.Context, input downloadedImage, method, aspect string) (downloadedImage, error) {
	if method == "None" {
		return input, nil
	}
	if method != "Right" && method != "Center" && method != "Face" {
		return downloadedImage{}, fmt.Errorf("无效海报裁剪方式：%s", method)
	}
	if err := ctx.Err(); err != nil {
		return downloadedImage{}, err
	}
	src, err := decodeWatermarkImage(input.data)
	if err != nil {
		return downloadedImage{}, err
	}
	rect, err := posterCropRectangle(ctx, src, input.width, input.height, method, aspect)
	if err != nil {
		return downloadedImage{}, err
	}
	out := image.NewRGBA(image.Rect(0, 0, rect.Width, rect.Height))
	draw.Draw(out, out.Bounds(), src, src.Bounds().Min.Add(image.Pt(rect.X, rect.Y)), draw.Src)
	traceScrape(ctx, "海报裁剪", fmt.Sprintf("%s：%d×%d → %d×%d，偏移=%d,%d", method, input.width, input.height, rect.Width, rect.Height, rect.X, rect.Y))
	return encodePreparedImage(out, input)
}

type posterCropRect struct {
	X, Y, Width, Height int
	FaceX, FaceY        int
	FaceFound           bool
}

// jacketSeamColumn 在缩略图左边界附近寻找封套的接缝，返回正面内容的第一列。
// DMM 的封面常是 800×538 的封套扫描图：左边背面、中间书脊、右边正面。按比例算出的
// 左边界会压在这条中缝上——比例窄了带进背面的黑边，宽了切掉正面左边的标题。
// 只在封套比例（宽高比 1.3～1.65）的源图上找，普通 16:9 剧照不会命中。
func jacketSeamColumn(src image.Image, x, width, height int) (int, bool) {
	if width < 64 || height < 64 {
		return 0, false
	}
	if ratio := float64(width) / float64(height); ratio < 1.3 || ratio > 1.65 {
		return 0, false
	}
	// 接缝可能落在比例边界左右各 1% 的范围内，两边都要找。
	shift := max(2, width/100)
	start := max(1, x-shift)
	end := min(width-1, x+shift)
	if start > end {
		return 0, false
	}
	origin := src.Bounds().Min
	// 颜色差（R、G、B 三个通道的平均差）：只看亮度会漏掉红蓝这类亮度接近的接缝。
	difference := func(c, y int) float64 {
		r1, g1, b1, _ := src.At(origin.X+c, origin.Y+y).RGBA()
		r2, g2, b2, _ := src.At(origin.X+c-1, origin.Y+y).RGBA()
		return (math.Abs(float64(r1)-float64(r2)) + math.Abs(float64(g1)-float64(g2)) + math.Abs(float64(b1)-float64(b2))) / 3 / 257
	}
	// 逐列取每行颜色差的中位数：接缝贯穿整幅图，中位数可以排除只在局部出现的竖线。
	step := max(1, height/200)
	diffs := make([]float64, end-start+1)
	values := make([]float64, 0, height/step+1)
	for c := start; c <= end; c++ {
		values = values[:0]
		for y := 0; y < height; y += step {
			values = append(values, difference(c, y))
		}
		sort.Float64s(values)
		diffs[c-start] = values[len(values)/2]
	}
	peak, peakAt := 0.0, -1
	for i, d := range diffs {
		if d > peak {
			peak, peakAt = d, start+i
		}
	}
	others := make([]float64, 0, len(diffs))
	for _, d := range diffs {
		if d != peak {
			others = append(others, d)
		}
	}
	sort.Float64s(others)
	// 峰值要明显高于窗口里的其它列，避免把画面里偶然的竖线当成中缝。
	if peak < 12 || peak < max(12.0, 2.5*others[len(others)/2]) {
		return 0, false
	}
	// 中缝和书脊常有好几像素宽，取这条接缝之后的第一列作为正面起点（留 1px 余量）。
	last := peakAt
	for last < end && diffs[last+1-start] >= peak*0.5 {
		last++
	}
	seam := last + 1
	if seam < 1 || seam >= width-1 {
		return 0, false
	}
	return seam, true
}

func posterCropRectangle(ctx context.Context, src image.Image, width, height int, method, aspect string) (posterCropRect, error) {
	if method == "" {
		method = "Right"
	}
	if method != "None" && method != "Right" && method != "Center" && method != "Face" {
		return posterCropRect{}, fmt.Errorf("无效海报裁剪方式：%s", method)
	}
	if err := ctx.Err(); err != nil {
		return posterCropRect{}, err
	}
	// 完整海报取 0.7（2.1:3），宽度向下取整：封套约 800×538，正面约 379 宽，0.7 比
	// 2.12/3 窄一点，按比例裁时不容易把中缝带进海报。真正贴身的是下面的接缝对齐。
	ratio := 0.7
	if aspect == "Emby" {
		ratio = 2.0 / 3.0
	} else if aspect != "" && aspect != "Full" {
		return posterCropRect{}, fmt.Errorf("无效海报裁剪比例：%s", aspect)
	}
	w, h := width, height
	cw, ch := w, h
	if float64(w)/float64(h) > ratio {
		cw = max(1, int(float64(h)*ratio))
	} else {
		ch = max(1, int(float64(w)/ratio))
	}
	x, y := (w-cw)/2, (h-ch)/2
	rect := posterCropRect{X: x, Y: y, Width: cw, Height: ch}
	if method == "Right" {
		rect.X = w - cw
		// 封面是左右两面的封套时，比例算出的左边界会压在中缝上：窄了会带进背面的黑边，
		// 宽了会切掉正面左边的标题。检测到接缝就直接对齐，宽取正面实际宽度。
		if seam, ok := jacketSeamColumn(src, rect.X, w, h); ok {
			traceScrape(ctx, "海报裁剪", fmt.Sprintf("左边界对齐封套接缝：%d → %d", rect.X, seam))
			rect.X, rect.Width = seam, w-seam
		}
	}
	if method == "Face" {
		face, found, err := detectPosterFace(ctx, src)
		if err != nil {
			return posterCropRect{}, err
		}
		rect.FaceFound = found
		if found {
			rect.FaceX, rect.FaceY = face.X, face.Y
			rect.X, rect.Y = max(0, min(w-cw, face.X-cw/2)), max(0, min(h-ch, face.Y-ch/3))
			traceScrape(ctx, "海报裁剪", fmt.Sprintf("检测到人脸，定位=%d,%d", face.X, face.Y))
		} else {
			traceScrape(ctx, "海报裁剪", "未检测到可靠人脸，回退到中心裁剪")
		}
	}
	return rect, nil
}

// posterCropSkipRatio 候选海报高度达到封面的这个比例时就不再裁封面：裁剪有错位风险，
// 候选已经和封面差不多高时裁了也没多少收益。0.9 取自 Amane 的默认值。
const posterCropSkipRatio = 0.9

// shouldCropPoster 判断是否值得用封面裁剪来生成海报：没有候选海报，或候选比封面明显矮时才裁。
// 候选海报够高（≥ 封面的 90%）时直接用候选，避免把一张正经的竖版海报换成封套裁片。
func shouldCropPoster(poster, cover downloadedImage) bool {
	if cover.data == nil || cover.width <= 0 || cover.height <= 0 {
		return false
	}
	if poster.data == nil || poster.width <= 0 || poster.height <= 0 {
		return true
	}
	return float64(poster.height) < posterCropSkipRatio*float64(cover.height)
}

func finalizeScrapeImage(ctx context.Context, input downloadedImage, kind string, flags filenameFlags, settings scrapeSettings) (downloadedImage, error) {
	// Convert formats without an encoder before applying watermarks; keep original
	// downloaded bytes when no transformation is requested.
	if input.ext == ".gif" || (input.ext != ".jpg" && input.ext != ".png" && watermarkBool(settings.Watermark, kind) && len(selectedWatermarks(flags, settings.Watermark, settings.Custom.WatermarkEnabled)) > 0) {
		src, err := decodeWatermarkImage(input.data)
		if err != nil {
			return downloadedImage{}, err
		}
		input, err = encodePreparedImage(src, input)
		if err != nil {
			return downloadedImage{}, err
		}
	}
	data, err := applyWatermarks(ctx, input.data, input.ext, kind, flags, settings.Watermark, customWatermarkOptions{Enabled: settings.Custom.WatermarkEnabled, Dir: settings.CustomWatermarkDir})
	input.data = data
	return input, err
}

func posterCropMethod(number, path string, settings scrapeSettings) string {
	method := settings.Download.CropRegular
	if strings.HasPrefix(strings.ToUpper(number), "FC2-") || strings.HasPrefix(strings.ToUpper(detectNumber(path)), "FC2-") {
		method = settings.Download.CropFC2
	}
	if method == "" {
		method = "Right"
	}
	return method
}
