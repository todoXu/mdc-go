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
	ratio := 2.12 / 3.0
	if aspect == "Emby" {
		ratio = 2.0 / 3.0
	} else if aspect != "" && aspect != "Full" {
		return posterCropRect{}, fmt.Errorf("无效海报裁剪比例：%s", aspect)
	}
	w, h := width, height
	cw, ch := w, h
	if float64(w)/float64(h) > ratio {
		cw = max(1, int(math.Round(float64(h)*ratio)))
	} else {
		ch = max(1, int(math.Round(float64(w)/ratio)))
	}
	x, y := (w-cw)/2, (h-ch)/2
	rect := posterCropRect{X: x, Y: y, Width: cw, Height: ch}
	if method == "Right" {
		rect.X = w - cw
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

func finalizeScrapeImage(ctx context.Context, input downloadedImage, kind string, flags filenameFlags, settings scrapeSettings) (downloadedImage, error) {
	// Convert formats without an encoder before applying watermarks; keep original
	// downloaded bytes when no transformation is requested.
	if input.ext == ".gif" || (input.ext != ".jpg" && input.ext != ".png" && watermarkBool(settings.Watermark, "enabled") && watermarkBool(settings.Watermark, kind) && len(selectedWatermarks(flags, settings.Watermark)) > 0) {
		src, err := decodeWatermarkImage(input.data)
		if err != nil {
			return downloadedImage{}, err
		}
		input, err = encodePreparedImage(src, input)
		if err != nil {
			return downloadedImage{}, err
		}
	}
	data, err := applyWatermarks(ctx, input.data, input.ext, kind, flags, settings.Watermark)
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
