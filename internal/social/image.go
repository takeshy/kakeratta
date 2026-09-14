package social

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// MaxImageBytes follows app.bsky.embed.images, which increased from 1 MB to 2 MB.
const MaxImageBytes = 2_000_000

// PrepareImage changes only the outgoing copy. Small files retain their original
// encoding; oversized images become JPEG, with white behind transparent pixels.
func PrepareImage(data []byte, mime string) ([]byte, string, error) {
	if !strings.HasPrefix(mime, "image/") {
		return nil, "", fmt.Errorf("画像形式が不正です")
	}
	if len(data) <= MaxImageBytes {
		return data, mime, nil
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("画像を圧縮できませんでした。JPEG・PNG・GIF・WebP形式の画像を使用してください")
	}
	// Bound decoded memory before allocating a potentially hostile image.
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 50_000_000 {
		return nil, "", fmt.Errorf("画像の解像度が大きすぎます。5,000万画素以下に縮小してください")
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("画像を読み込めませんでした")
	}
	width, height := config.Width, config.Height
	if longest := max(width, height); longest > 2400 {
		width, height = max(1, width*2400/longest), max(1, height*2400/longest)
	}
	for {
		resized := image.NewRGBA(image.Rect(0, 0, width, height))
		draw.Draw(resized, resized.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		draw.CatmullRom.Scale(resized, resized.Bounds(), source, source.Bounds(), draw.Over, nil)
		for _, quality := range []int{90, 80, 70, 60} {
			var out bytes.Buffer
			if err := jpeg.Encode(&out, resized, &jpeg.Options{Quality: quality}); err != nil {
				return nil, "", fmt.Errorf("画像を圧縮できませんでした")
			}
			if out.Len() <= MaxImageBytes {
				return out.Bytes(), "image/jpeg", nil
			}
		}
		width, height = max(1, width*3/4), max(1, height*3/4)
	}
}
