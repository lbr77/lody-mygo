package app

import (
	"bytes"
	"fmt"
	"html"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// A diagram downloads as Lody's do, to the Downloads folder: as SVG, as a
// PNG five times its size, or as its Mermaid source. Its drawing goes to
// a pen: the window's painter, or an SVG writer that records what is
// drawn as elements.

// pngExportScale is how many times its size a diagram's PNG is.
const pngExportScale = 5

// diagramPen is what a diagram draws with. *ui.Painter is one.
type diagramPen interface {
	Fill(r ui.Rect, c ui.Color, radius float32)
	Stroke(r ui.Rect, c ui.Color, radius, width float32)
	StrokePolyline(points [][2]float32, width float32, c ui.Color)
	FillPath(path *ui.Path, c ui.Color)
	StrokePath(path *ui.Path, width float32, c ui.Color)
	TextLayout(l *ui.TextLayout, x, y float32, c ui.Color)
}

// exporting is the SVG writer a diagram draws to, while it does: the
// polygons and text it shapes are kept for it, as a painter's path and
// layout don't say what they hold.
var exporting *svgPen

// polygon is a closed path through points.
func polygon(points ...[2]float32) *ui.Path {
	path := &ui.Path{}
	for i, pt := range points {
		if i == 0 {
			path.MoveTo(pt[0], pt[1])
		} else {
			path.LineTo(pt[0], pt[1])
		}
	}
	path.Close()
	if exporting != nil {
		exporting.polygons[path] = points
	}
	return path
}

// diagramText shapes s in font, wrapped at width (0 for none).
func diagramText(s string, font ui.Font, width float32) *ui.TextLayout {
	l := ui.ShapeText(s, font, width)
	if exporting != nil {
		exporting.texts[l] = svgText{s, font}
	}
	return l
}

type svgText struct {
	s    string
	font ui.Font
}

// svgPen writes what a diagram draws as SVG elements.
type svgPen struct {
	b        strings.Builder
	polygons map[*ui.Path][][2]float32
	texts    map[*ui.TextLayout]svgText
}

func svgColor(c ui.Color) string {
	return fmt.Sprintf("rgba(%d,%d,%d,%.3g)", c.R, c.G, c.B, float32(c.A)/255)
}

func (s *svgPen) Fill(r ui.Rect, c ui.Color, radius float32) {
	fmt.Fprintf(&s.b, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="%.2f" fill="%s"/>`, r.X, r.Y, r.W, r.H, radius, svgColor(c))
}

func (s *svgPen) Stroke(r ui.Rect, c ui.Color, radius, width float32) {
	fmt.Fprintf(&s.b, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="%.2f" fill="none" stroke="%s" stroke-width="%.2f"/>`,
		r.X, r.Y, r.W, r.H, radius, svgColor(c), width)
}

func svgPoints(points [][2]float32) string {
	var b strings.Builder
	for i, pt := range points {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%.2f,%.2f", pt[0], pt[1])
	}
	return b.String()
}

func (s *svgPen) StrokePolyline(points [][2]float32, width float32, c ui.Color) {
	fmt.Fprintf(&s.b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="%.2f"/>`, svgPoints(points), svgColor(c), width)
}

func (s *svgPen) FillPath(path *ui.Path, c ui.Color) {
	if pts, ok := s.polygons[path]; ok {
		fmt.Fprintf(&s.b, `<polygon points="%s" fill="%s"/>`, svgPoints(pts), svgColor(c))
	}
}

func (s *svgPen) StrokePath(path *ui.Path, width float32, c ui.Color) {
	if pts, ok := s.polygons[path]; ok {
		fmt.Fprintf(&s.b, `<polygon points="%s" fill="none" stroke="%s" stroke-width="%.2f"/>`, svgPoints(pts), svgColor(c), width)
	}
}

func (s *svgPen) TextLayout(l *ui.TextLayout, x, y float32, c ui.Color) {
	t, ok := s.texts[l]
	if !ok {
		return
	}
	// A line's offsets count UTF-16 units.
	units := utf16.Encode([]rune(t.s))
	weight := t.font.Weight
	if weight == 0 {
		weight = 400
	}
	for _, line := range l.Lines() {
		text := strings.TrimRight(string(utf16.Decode(units[min(line.Start, len(units)):min(line.End, len(units))])), " \n")
		if text == "" {
			continue
		}
		fmt.Fprintf(&s.b, `<text x="%.2f" y="%.2f" font-family="system-ui, -apple-system, sans-serif" font-size="%.2f" font-weight="%d" fill="%s">%s</text>`,
			x+line.Rect.X, y+line.Baseline, t.font.Size, weight, svgColor(c), html.EscapeString(text))
	}
}

// svgMargin is the room around a diagram in its SVG, for the strokes on
// its edges.
const svgMargin = 8

// diagramSVG is a diagram w by h, as draw draws it, as an SVG document.
func diagramSVG(w, h float32, draw func(p diagramPen, x, y float32)) []byte {
	s := &svgPen{polygons: map[*ui.Path][][2]float32{}, texts: map[*ui.TextLayout]svgText{}}
	exporting = s
	defer func() { exporting = nil }()
	draw(s, svgMargin, svgMargin)
	w, h = w+2*svgMargin, h+2*svgMargin
	return []byte(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.2f %.2f">%s</svg>`,
		w, h, w, h, s.b.String()))
}

// diagramPNG is a diagram w by h, as draw draws it, as a PNG five times its
// size on ground.
func diagramPNG(w, h float32, ground ui.Color, draw func(p diagramPen, x, y float32)) ([]byte, error) {
	img := ui.Render(func(c *ui.Context) {
		ui.Box(c).Size(w, h).Background(ground).Draw(func(p *ui.Painter, r ui.Rect) { draw(p, r.X, r.Y) })
	}, int(w+0.5), int(h+0.5), pngExportScale)
	if img == nil {
		return nil, fmt.Errorf("diagram not rendered")
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// saveDownload writes data to the Downloads folder as name, as a browser
// downloads it, and says so.
func (a *App) saveDownload(name string, data []byte, err error) {
	if err == nil {
		var path string
		if path, err = downloadTarget(name); err == nil {
			if err = os.WriteFile(path, data, 0o644); err == nil {
				a.toast("success", locales.FileDownloaded(filepath.Base(path)))
				return
			}
		}
	}
	a.toast("error", locales.FileDownloadFailed(name))
}

// mermaidActions is Lody's bar over a diagram's top end corner: Download,
// a menu of SVG, PNG and its Mermaid source, and Copy, which says so for
// 2s. It fades in over 120ms, as CSS's ease, while the pointer is on the
// diagram or the focus within it.
func (a *App) mermaidActions(c *ui.Context, p *palette, frame ui.Element, id, src string, w, h float32, ground ui.Color, draw func(p diagramPen, x, y float32)) {
	dl := a.rowMenu(id + "/download")
	shown := frame.Hovered() || frame.FocusWithin() || dl.open
	bar := ui.Row(c.Key("mermaid-actions")).Absolute().Top(6).Right(6).Gap(2).Padding(2).Radius(5).AlignItems(ui.Center).
		Border(1, p.Border.Alpha(0.78)).Background(p.Canvas.Alpha(0.86))
	bar.Opacity(bar.AnimateWith("shown", b2f(shown), 120*time.Millisecond, cssEase))
	button := func(key string, icon *ui.SVG, label string) ui.Element {
		b := ui.ButtonBase(c.Key(key)).Padding(4).Radius(4).Center().Cursor(ui.CursorPointer).Label(label).Transition(ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond, Ease: cssEase})
		ink := p.Secondary
		if b.Hovered() {
			b.Background(p.Hover)
			ink = p.Text
		}
		b.Children(func() { ui.Icon(c, icon).Size(14, 14).TextColor(ink) })
		return b
	}
	bar.Children(func() {
		download := tip(c, p, button("download", iconDownload, locales.DiagramDownload()), locales.DiagramDownload())
		lodyMenuButton(c, p, download, &dl.menuState, 200, 290, 28, func(m *lodyMenu) {
			if m.item("svg", nil, locales.DiagramDownloadSvg(), menuItem{}) {
				a.saveDownload("diagram.svg", diagramSVG(w, h, draw), nil)
			}
			if m.item("png", nil, locales.DiagramDownloadPng(), menuItem{}) {
				data, err := diagramPNG(w, h, ground, draw)
				a.saveDownload("diagram.png", data, err)
			}
			if m.item("source", nil, locales.DiagramDownloadSource(), menuItem{}) {
				a.saveDownload("diagram.mmd", []byte(src), nil)
			}
		})
		if a.copiedAt == nil {
			a.copiedAt = map[string]time.Time{}
		}
		at := a.copiedAt[id]
		copied := !at.IsZero() && c.Now().Sub(at) < 2*time.Second
		icon, label := iconCopy, locales.CopyCode()
		if copied {
			icon, label = iconCheck, locales.Copied()
			c.After(2*time.Second - c.Now().Sub(at))
		}
		tip(c, p, button("copy", icon, locales.CopyCode()), label).OnClick(func() {
			writeClipboard(src)
			a.copiedAt[id] = time.Now()
		})
	})
}
