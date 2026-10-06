package avatar

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"
)

func photo(width, height int) string {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var data bytes.Buffer
	_ = jpeg.Encode(&data, img, &jpeg.Options{Quality: 80})
	return Prefix + base64.StdEncoding.EncodeToString(data.Bytes())
}

func TestValidation(t *testing.T) {
	for _, value := range []string{"", photo(128, 128), photo(32, 64)} {
		if err := Validate(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{
		"https://example.com/photo.jpg", "data:image/svg+xml;base64,PHN2Zy8+", Prefix + "broken!",
		photo(129, 128), photo(128, 129), Prefix + strings.Repeat("A", base64.StdEncoding.EncodedLen(MaxBytes+1)),
		Prefix + base64.StdEncoding.EncodeToString([]byte("not a JPEG")),
	} {
		if err := Validate(value); err == nil {
			t.Fatalf("accepted invalid photo: %.50s", value)
		}
	}
	raw := strings.TrimPrefix(photo(128, 128), Prefix)
	data, _ := base64.StdEncoding.DecodeString(raw)
	if err := Validate(Prefix + base64.StdEncoding.EncodeToString(data[:len(data)/2])); err == nil {
		t.Fatal("accepted truncated JPEG")
	}
}
