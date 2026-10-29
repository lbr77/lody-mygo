package app

import (
	"math"
	"time"

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

// The done transition, as Lody's WorkingGridCollapse plays it when a
// session stops working with replies unread: the grid turns 135° as its
// tiles fall into the centre and fade, then the unread dot pops past its
// size and settles with two bounces.
const (
	collapseGather   = 480 * time.Millisecond
	collapseDotStart = 400 * time.Millisecond
	collapseDot      = 620 * time.Millisecond
	collapseTotal    = collapseDotStart + collapseDot
	// workingHandOver is how long a finished row keeps its grid while the
	// unread write is on its way.
	workingHandOver = 1500 * time.Millisecond
)

var (
	collapseIn  = ui.CubicBezier(0.45, 0, 0.7, 0.2)
	collapseOut = ui.CubicBezier(0.2, 0.7, 0.3, 1)
)

// lerpKeys interpolates values at offsets, linearly between them.
func lerpKeys(e float64, offsets, values []float64) float64 {
	if e <= offsets[0] {
		return values[0]
	}
	for i := 1; i < len(offsets); i++ {
		if e <= offsets[i] {
			f := (e - offsets[i-1]) / (offsets[i] - offsets[i-1])
			return values[i-1] + (values[i]-values[i-1])*f
		}
	}
	return values[len(values)-1]
}

// roundSquare is a square's outline, rounded by r, turned by angle around
// (cx, cy).
func roundSquare(cx, cy, half, r, angle float64) *ui.Path {
	path := new(ui.Path)
	sin, cos := math.Sincos(angle)
	put := func(x, y float64, first bool) {
		px, py := cx+x*cos-y*sin, cy+x*sin+y*cos
		if first {
			path.MoveTo(float32(px), float32(py))
		} else {
			path.LineTo(float32(px), float32(py))
		}
	}
	corners := [4][2]float64{{half - r, half - r}, {-(half - r), half - r}, {-(half - r), -(half - r)}, {half - r, -(half - r)}}
	first := true
	for i, k := range corners {
		for s := 0; s <= 4; s++ {
			a := float64(i)*math.Pi/2 + float64(s)*math.Pi/8
			put(k[0]+r*math.Cos(a), k[1]+r*math.Sin(a), first)
			first = false
		}
	}
	return path.Close()
}

// workingCollapse draws the done transition begun at start, size points
// square, the dot 8 across.
func workingCollapse(c *ui.Context, p *palette, size float32, start time.Time) {
	color := p.Primary
	ui.Box(c.Key("collapse")).Size(size, size).Shrink(0).Label(locales.StatusUnread()).Draw(func(pt *ui.Painter, r ui.Rect) {
		t := pt.Now().Sub(start)
		grid := float64(r.W)
		cell := grid / (3 + 2*gridGap)
		pitch := cell * (1 + gridGap)
		tile := cell * 0.9
		cx, cy := float64(r.X)+grid/2, float64(r.Y)+grid/2
		if t < collapseGather {
			e := float64(collapseIn(float32(t) / float32(collapseGather)))
			angle := 135 * math.Pi / 180 * e
			gather := lerpKeys(e, []float64{0, 0.8, 1}, []float64{0, 1, 1})
			scale := lerpKeys(e, []float64{0, 0.8, 1}, []float64{1, 0.55, 0.4})
			alpha := lerpKeys(e, []float64{0, 0.8, 1}, []float64{1, 1, 0})
			for row := range 3 {
				for col := range 3 {
					dx, dy := float64(col-1)*pitch, float64(row-1)*pitch
					x, y := dx*(1-gather), dy*(1-gather)
					sin, cos := math.Sincos(angle)
					px, py := cx+x*cos-y*sin, cy+x*sin+y*cos
					half := tile * scale / 2
					pt.FillPath(roundSquare(px, py, half, half*0.8, angle), color.Alpha(float32(alpha)))
				}
			}
		}
		if dt := t - collapseDotStart; dt >= 0 {
			e := float64(collapseOut(min(1, float32(dt)/float32(collapseDot))))
			offsets := []float64{0, 0.25, 0.45, 0.62, 0.78, 0.9, 1}
			scale := lerpKeys(e, offsets, []float64{0.35, 1.75, 0.72, 1.3, 0.88, 1.06, 1})
			alpha := lerpKeys(e, offsets, []float64{0, 1, 1, 1, 1, 1, 1})
			radius := float32(4 * scale)
			pt.Fill(ui.Rect{X: float32(cx) - radius, Y: float32(cy) - radius, W: 2 * radius, H: 2 * radius}, color.Alpha(float32(alpha)), radius)
		}
		if t < collapseTotal {
			pt.AnimationFrame()
		}
	})
}

// markState follows a row's working mark from frame to frame.
type markState struct {
	working   bool      // as last seen
	holdUntil time.Time // the grid kept after work stopped, for the unread write
	drawn     bool      // whether the grid was drawn
	finishAt  time.Time // when the done transition began
}

// workingMark is a session's working or unread mark, with Lody's
// hand-over between them; it reports whether it drew anything.
func (a *App) workingMark(c *ui.Context, p *palette, id string, working, unread bool) bool {
	if a.marks == nil {
		a.marks = map[string]*markState{}
	}
	m := a.marks[id]
	if m == nil {
		m = &markState{working: working, drawn: working}
		a.marks[id] = m
	}
	now := c.Now()
	if m.working != working {
		if m.working && !working && !unread {
			m.holdUntil = now.Add(workingHandOver)
		} else {
			m.holdUntil = time.Time{}
		}
		m.working = working
	} else if unread {
		m.holdUntil = time.Time{}
	}
	holding := now.Before(m.holdUntil)
	if holding {
		c.After(m.holdUntil.Sub(now))
	}
	draw := working || holding
	if m.drawn != draw {
		if m.drawn && !draw && unread {
			m.finishAt = now
		}
		m.drawn = draw
	}
	switch {
	case draw:
		workingGrid(c, p, 12)
	case unread && now.Sub(m.finishAt) < collapseTotal:
		workingCollapse(c, p, 12, m.finishAt)
	case unread:
		ui.Box(c.Key("unread")).Size(8, 8).Radius(4).Background(p.Primary).Margin(0, 2)
	default:
		return false
	}
	return true
}

// sendRing marks messages still going out, as Lody's
// SessionSendProgressRing: a faint ring that fills with the bytes sent,
// easing out over 300ms, a highlight sweeping along what is filled every
// 1.8s so a ring waiting on the next report never reads as frozen;
// without a size to measure, a quarter of it turns every 1.2s. label
// names it where nothing beside it does.
func sendRing(c *ui.Context, p *palette, progress float32, known bool, label string) {
	ring := ui.Box(c.Key("send-ring")).Size(12, 12).Shrink(0)
	if label != "" {
		ring.Label(label)
	}
	ink := p.Primary
	var turn, fill, sweep float32
	if known {
		fill = ring.AnimateWith("fill", min(100, max(4, progress))/100, 300*time.Millisecond, twEaseOut)
		sweep = ring.Keyframes("sweep", []ui.Keyframe{{Value: 0}, {At: 1800 * time.Millisecond, Value: 1}}, ui.KeyframeOptions{Repeat: ui.RepeatForever})
	} else {
		turn = ring.Keyframes("turn", []ui.Keyframe{{Value: 0}, {At: 1200 * time.Millisecond, Value: 1}}, ui.KeyframeOptions{Repeat: ui.RepeatForever})
	}
	ring.Draw(func(pt *ui.Painter, r ui.Rect) {
		cx, cy, rad := r.X+r.W/2, r.Y+r.H/2, 4.25*r.W/12
		arc := func(from, sweep float64) [][2]float32 {
			n := max(4, int(48*sweep))
			pts := make([][2]float32, 0, n+1)
			for i := 0; i <= n; i++ {
				a := 2 * math.Pi * (from + sweep*float64(i)/float64(n))
				pts = append(pts, [2]float32{cx + rad*float32(math.Cos(a)), cy + rad*float32(math.Sin(a))})
			}
			return pts
		}
		pt.StrokePolyline(arc(0, 1), 1.5, ink.Alpha(0.22))
		if !known {
			pt.StrokePolyline(arc(-0.25+float64(turn), 0.25), 1.5, ink)
			return
		}
		pt.StrokePolyline(arc(-0.25, float64(fill)), 1.5, ink)
		// The highlight: five of the ring's units long, from just before
		// its start round to its end, showing only on what is filled.
		circ := float32(2 * math.Pi * 4.25)
		from := -5 + sweep*(circ+5)
		lo, hi := max(0, from), min(from+5, fill*circ)
		if hi > lo {
			pt.StrokePolyline(arc(-0.25+float64(lo/circ), float64((hi-lo)/circ)), 2*r.W/12, ui.RGBA(255, 255, 255, 0.65))
		}
	})
}
