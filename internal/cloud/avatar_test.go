package cloud

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestAvatarImage(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1200, 800))
	for x := 0; x < 1200; x++ {
		for y := 0; y < 800; y++ {
			src.Set(x, y, color.RGBA{uint8(x), uint8(y), 90, 255})
		}
	}
	var in bytes.Buffer
	png.Encode(&in, src)
	out, kind, err := AvatarImage(in.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil || img.Bounds().Dx() != 512 || img.Bounds().Dy() != 512 || len(out) > maxAvatarBytes || kind == "" {
		t.Fatalf("avatar %v %s %d bytes, %v", img.Bounds(), kind, len(out), err)
	}
	// Centered: the left edge of the square is the source's column 200.
	if r, _, _, _ := img.At(0, 256).RGBA(); r>>8 < 190 || r>>8 > 210 {
		t.Fatalf("not centered: the left edge is column %d", r>>8)
	}
	if _, _, err := AvatarImage([]byte("not an image")); err == nil {
		t.Fatal("decoded garbage")
	}
}
