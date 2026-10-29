package app

import (
	"math"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// Lody's working mark: a 3×3 grid of tiles that shrink and swell, and dim
// and brighten, as two waves cross them. The waves cross the whole window
// and every tile samples them where it is, so the marks down the sidebar
// are windows onto one sea and a crest passes from one row's mark to the
// next.

// gridWave is a plane wave over the sea; positions are in cells, a third
// of a grid.
type gridWave struct {
	dx, dy   float64 // travel direction
	length   float64 // crest to crest, in cells
	periodMs float64
	phase    float64 // in turns
}

// Lody's waves "across": the first shorter and faster, 27° down-right; the
// second 60° down-left; their periods 19:27, so the pattern never reads
// as a loop.
var (
	gridMain   = gridWave{0.9, 0.45, 3.2, 1900, 0}
	gridRipple = gridWave{-0.5, 0.85, 4.4, 2700, 0.3}
)

// Lody's defaults for the mark.
const (
	gridGap        = 0.18 // between tiles, of a tile
	gridMaxScale   = 0.9  // largest a tile gets, of its cell
	gridMinScale   = 0.3  // smallest, in a double trough
	gridMinOpacity = 0.25
	gridRippleMix  = 0.3 // the ripple's share of the range
	gridWavelength = 1.2
	gridRowPitch   = 28 // the sidebar's row pitch, which stitches rows' marks together
)

func frac(v float64) float64 { return v - math.Floor(v) }

// height is a wave's height at sea point (x, y) at t ms: 1 at a crest, 0
// in a trough, a cosine between, as Lody's eased crest → trough → crest
// loop is.
func (w gridWave) height(x, y, t float64) float64 {
	phase := frac((w.dx*x+w.dy*y)/(w.length*gridWavelength) + w.phase)
	progress := frac(t/w.periodMs + 0.25 - phase)
	return 0.5 + 0.5*math.Cos(2*math.Pi*progress)
}

// gridTile is a tile's scale (of the largest tile) and opacity at sea
// point (x, y) at t ms. A light theme lifts the floors and grows the
// tiles a little, as small dark tiles on white otherwise vanish.
func gridTile(x, y, t float64, light bool) (scale, opacity float64) {
	minOpacity, minScale, maxScale := gridMinOpacity, gridMinScale, gridMaxScale
	if light {
		minOpacity += (1 - minOpacity) * 0.35
		minScale += (1 - minScale) * 0.2
		maxScale = math.Min(1, maxScale*1.08)
	}
	// The ripple takes its share of the range; the main wave the rest, so
	// their product bottoms out at the floors.
	rippleScaleLow := 1 - gridRippleMix
	rippleOpacityLow := 1 - gridRippleMix*(1-minOpacity)
	mainScaleLow := math.Min(1, (minScale/maxScale)/rippleScaleLow)
	mainOpacityLow := minOpacity / rippleOpacityLow
	m, r := gridMain.height(x, y, t), gridRipple.height(x, y, t)
	scale = (mainScaleLow + (1-mainScaleLow)*m) * (rippleScaleLow + (1-rippleScaleLow)*r)
	opacity = (mainOpacityLow + (1-mainOpacityLow)*m) * (rippleOpacityLow + (1-rippleOpacityLow)*r)
	return scale * maxScale, opacity
}

// workingGrid draws the mark, size points square.
func workingGrid(c *ui.Context, p *palette, size float32) {
	color, light := p.Primary, !p.Dark
	ui.Box(c).Size(size, size).Shrink(0).Label(locales.StatusWorking()).Draw(func(pt *ui.Painter, r ui.Rect) {
		cell := float64(r.W) / (3 + 2*gridGap)
		pitch := cell * (1 + gridGap)
		seaCell := float64(r.W) / 3
		stitch := 1.0
		if gridRowPitch > float64(r.W) {
			stitch = float64(r.W) / gridRowPitch
		}
		t := float64(pt.Now().UnixMilli())
		for row := range 3 {
			for col := range 3 {
				x := float64(r.X)/seaCell + float64(col)
				y := float64(r.Y)/seaCell*stitch + float64(row)
				scale, opacity := gridTile(x, y, t, light)
				tile := cell * scale
				cx := float64(r.X) + float64(col)*pitch + cell/2
				cy := float64(r.Y) + float64(row)*pitch + cell/2
				box := ui.Rect{X: float32(cx - tile/2), Y: float32(cy - tile/2), W: float32(tile), H: float32(tile)}
				pt.Fill(box, color.Alpha(float32(opacity)), float32(tile*0.4))
			}
		}
		pt.AnimationFrameRate(30)
	})
}
