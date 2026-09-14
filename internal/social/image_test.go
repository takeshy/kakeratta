package social

import (
	"bytes"
	"image"
	"image/png"
	"math/rand/v2"
	"testing"
)

func TestPrepareImagePreservesFilesThroughTwoMB(t *testing.T) {
	for _, size := range []int{1_000_001, MaxImageBytes} {
		original := bytes.Repeat([]byte{42}, size)
		got, mime, err := PrepareImage(original, "image/png")
		if err != nil || mime != "image/png" || !bytes.Equal(got, original) {
			t.Fatalf("size %d: %s %v", size, mime, err)
		}
	}
}

func TestPrepareImageCompressesLargePNGWithoutChangingOriginal(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 1200, 900))
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(rng.Uint32()), byte(rng.Uint32()), byte(rng.Uint32()), 255
	}
	var input bytes.Buffer
	if err := png.Encode(&input, img); err != nil {
		t.Fatal(err)
	}
	original := bytes.Clone(input.Bytes())
	if len(original) <= MaxImageBytes {
		t.Fatal("fixture must exceed size limit")
	}
	got, mime, err := PrepareImage(input.Bytes(), "image/png")
	if err != nil || mime != "image/jpeg" || len(got) > MaxImageBytes {
		t.Fatalf("%d %s %v", len(got), mime, err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(got))
	if err != nil || decoded.Bounds() != img.Bounds() {
		t.Fatalf("bounds or decode: %v", err)
	}
	if !bytes.Equal(input.Bytes(), original) {
		t.Fatal("source changed")
	}
}

func TestPrepareImageRejectsUnreadableOversizedImage(t *testing.T) {
	if _, _, err := PrepareImage(make([]byte, MaxImageBytes+1), "image/jpeg"); err == nil {
		t.Fatal("accepted broken image")
	}
}
