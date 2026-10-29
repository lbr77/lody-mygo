package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
)

// TestJellyfishShader runs the shader on the CPU at one sample a pixel, as
// the GPU runs it a cell, to check the port draws the jellyfish.
func TestJellyfishShader(t *testing.T) {
	const w, h = 150, 120
	view := func(c *ui.Context) {
		ui.Box(c).Size(w, h).Draw(func(p *ui.Painter, r ui.Rect) {
			jellyfish.Draw(p, r, shader.Options{Uniforms: map[string]any{
				"Time": 3.0, "Res": []float32{w, h}, "Mouse": []float32{0.5, 0.5},
				"Paper": ui.Hex("#F0F0F0"), "Ink": ui.Hex("#1C1C1C"), "Reveal": 1.0,
			}})
		})
	}
	tt := ui.NewTester(view, w, h)
	tt.Frame()
	img := tt.Image()
	dark := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.RGBAAt(x, y).R < 0x80 {
				dark++
			}
		}
	}
	total := b.Dx() * b.Dy()
	// The bell and tentacles ink a fair part of the frame; the dim
	// background dithers a few percent.
	if dark < total/20 || dark > total*3/4 {
		t.Fatalf("%d of %d pixels inked", dark, total)
	}
	if dir := os.Getenv("LODY_SNAPSHOTS"); dir != "" {
		f, _ := os.Create(filepath.Join(dir, "jellyfish.png"))
		defer f.Close()
		png.Encode(f, img)
	}
}
