package app

import (
	"bytes"
	"encoding/xml"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/session"
)

// A diagram downloads as SVG, its shapes and labels as elements, and as a
// PNG five times its size.
func TestMermaidExport(t *testing.T) {
	fc, ok := parseFlowchart("flowchart TD\n  A[Write code] --> B{Tests pass?}\n  B -->|yes| C(Commit)")
	if !ok {
		t.Fatal("no flowchart")
	}
	w, h := fc.layout(13)
	ink, line, fill := ui.RGBA(20, 20, 20, 1), ui.RGBA(120, 120, 120, 1), ui.RGBA(245, 245, 245, 1)
	draw := func(p diagramPen, x, y float32) { fc.draw(p, x, y, 13, ink, line, fill) }

	svg := diagramSVG(w, h, draw)
	for _, want := range []string{"<svg ", ">Write code</text>", ">Tests pass?</text>", ">yes</text>", "<polygon "} {
		if !bytes.Contains(svg, []byte(want)) {
			t.Errorf("SVG lacks %q", want)
		}
	}
	d := xml.NewDecoder(bytes.NewReader(svg))
	for {
		if _, err := d.Token(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("SVG isn't well formed: %v", err)
		}
	}
	if exporting != nil {
		t.Fatal("still recording after the export")
	}

	data, err := diagramPNG(w, h, fill, draw)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() < int(w*pngExportScale)-pngExportScale || b.Dy() < int(h*pngExportScale)-pngExportScale {
		t.Fatalf("PNG %v for a %vx%v diagram", b, w, h)
	}
}

// Over a diagram, Lody's bar downloads its Mermaid source to Downloads.
func TestMermaidActions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := fixture()
	a.view.Working = false
	a.view.Entries = a.view.Entries[:2]
	src := "flowchart TD\n  A[Write code] --> B{Tests pass?}"
	a.view.Entries[1].Items = []session.Item{{ID: "m", Type: "text", Text: "```mermaid\n" + src + "\n```"}}
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	r, ok := tt.Find("Mermaid\n" + src)
	if !ok {
		t.Fatalf("no diagram: %q", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Download diagram"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	for _, item := range []string{"SVG", "PNG", "Mermaid source"} {
		if !tt.HasText(item) {
			t.Fatalf("no %q in the menu: %q", item, tt.Texts())
		}
	}
	if err := tt.Click("Mermaid source"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	got, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "Downloads", "diagram.mmd"))
	if err != nil || strings.TrimSpace(string(got)) != src {
		t.Fatalf("diagram.mmd: %q, %v", got, err)
	}
}
