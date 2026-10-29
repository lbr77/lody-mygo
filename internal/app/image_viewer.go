package app

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// The image viewer is Lody's ZoomableImageViewer, the one full-screen
// viewer of the app: a message's pictures, a queued message's and a
// file's preview all open it, so that they zoom and pan alike. Lody builds
// it on react-photo-view's PhotoSlider, patched never to zoom out past the
// picture's fit, and on the desktop shows it as a lightbox over the app
// rather than in its place: the picture inset to 92% of its fit, its
// corners rounded 4px, on a shadow 18px down and 50px soft; the window
// behind blurred 16px under black at 86%, fading in over 400ms; a bar
// along the top, darkening down from 45% black, clear of the traffic
// lights, with the count of the pictures (hidden for one) and the close
// button; and the previous and next buttons at the middle of each side.
//
// A picture ready within 250ms of coming on, as one seen before is, grows
// out of the middle of the window from a hundredth of its size as it
// fades in, over 400ms; one that takes longer shows as it comes, without
// motion, and nothing shows until then. The wheel, a pinch and a double
// click zoom it about the pointer, from its fit to six times that, or its
// own size where that is more; dragged, it pans, follows half as far past
// its edges, and eases back inside them as it is let go, or glides on
// when flung. Dragged sideways at its fit, or past its edge, the strip of
// pictures follows, and 40 DIPs on goes to the next one over 600ms. The
// arrow keys and the side buttons step through the pictures, round the
// ends past three. Escape, the close button and a click on the mask close
// it at once, without motion, as Lody unmounts it.

const (
	viewerMaskAlpha = 0.86 // the mask's black, Lody's DESKTOP_MASK_OPACITY
	viewerInset     = 0.92 // the lightbox's inset of the picture
	viewerGap       = 20   // DIPs between the pictures on the strip
	viewerDead      = 20   // DIPs a drag goes before it takes a direction
	viewerSwipe     = 40   // DIPs a sideways drag goes to change the picture

	viewerSpeed = 400 * time.Millisecond // opening, zooming and the mask's fades
	viewerSlide = 600 * time.Millisecond // the strip going to a picture
	viewerReady = 250 * time.Millisecond // a picture ready within this opens with motion
	viewerFlick = 200 * time.Millisecond // a drag let go within this flings the picture
	viewerTaps  = 300 * time.Millisecond // a double tap's most between its taps
	viewerFrame = time.Second / 60       // the frames a flung picture meets its edges on
)

var (
	// easePhoto is react-photo-view's easing for its CSS transitions:
	// cubic-bezier(0.25, 0.8, 0.25, 1).
	easePhoto = ui.CubicBezier(0.25, 0.8, 0.25, 1)
	// easeQuartOut is the curve react-photo-view's own tweens move a
	// picture on, frame by frame: 1 − (1 − t)⁴.
	easeQuartOut ui.Easing = func(t float32) float32 { u := 1 - t; return 1 - u*u*u*u }
)

// viewerShaderMask draws the mask with its shader, blurring what lies
// under it 16px as Lody's backdrop-filter. Tests that don't look at the
// mask draw it as a plain fill, as the test renderer runs shaders on the
// CPU, every pixel of every frame.
var viewerShaderMask = true

// viewerMaskShader draws the mask: what is behind it, blurred, under
// Alpha of black.
var viewerMaskShader = shader.MustCompile(`package main

var Alpha float

func Fragment(dst vec4, pos vec2) vec4 {
	b := backdropSample(pos)
	return vec4(b.rgb*(1-Alpha), b.a*(1-Alpha)+Alpha)
}
`)

// viewerItem is a picture the viewer shows, as Lody's
// ZoomableImageViewerItem.
type viewerItem struct {
	key      string
	fileName string // the save dialog's default name
	// picture is the picture at its size, fetched as it is first asked
	// for and kept: nil, or loading, while it comes.
	picture func() *remoteImage
}

// imageViewer is the viewer while it shows.
type imageViewer struct {
	open  bool
	key   string                        // the picture shown
	items func(key string) []viewerItem // the pictures, as they stand; none closes it
	owner string                        // what holds it open as it builds (holdViewer)
	held  bool

	opened time.Time               // when it first showed
	photos map[string]*viewerPhoto // the pictures on the strip, by key
	press  viewerPress
	strip  track // where the strip is, from showing the picture
	mask   track // the mask's black
	// Where a drag the strip took started, across and down, once it had.
	fromX, fromY float32
	dragX, dragY bool
	tapped       time.Time // a tap on the picture, which another within 300ms doubles

	// What the frame last built, which the input between frames goes by.
	vw, vh float32
	keys   []string
	index  int
	active *viewerPhoto
	reduce bool
	asked  time.Time // the frame that last asked for the next
}

// viewerPhoto is a picture on the strip, as react-photo-view's PhotoBox
// has it: x and y are where its middle is from the window's, s its zoom
// of its fit.
type viewerPhoto struct {
	shown, ready time.Time // when it came on the strip, and its picture with it
	opens        bool      // it comes in with the open motion
	eased        bool      // its zoom moves on the 400ms transition (react-photo-view's pause off)
	nw, nh       float32   // the picture's size
	w, h         float32   // its fit
	vw, vh       float32   // the window it was fitted to
	x, y, s      track
}

// viewerPress is a press on the viewer under way: on the picture, or on
// the mask around it.
type viewerPress struct {
	photo, mask    bool
	cx, cy         float32   // where it went down
	lastCX, lastCY float32   // where it last moved the picture from
	lastX, lastY   float32   // where the picture was as it went down
	lastScale      float32   // and its zoom
	at             time.Time // when it went down on the picture
	dir            int       // 1 across, 2 up, 3 down, once it has gone 20 DIPs
	reach          byte      // 'x' once the strip took it, 'y' once the mask did
}

// track is a value of the viewer in motion: from where it was through
// steps, one after another from start, to rest. css marks a CSS
// transition, whose value is where it goes; the rest are react-photo-
// view's tweens, which move it frame by frame, its value where they got.
type track struct {
	from, rest float32
	start      time.Time
	steps      []trackStep
	css        bool
}

type trackStep struct {
	d    time.Duration
	to   float32
	ease ui.Easing
}

// still is a track resting at v.
func still(v float32) track { return track{from: v, rest: v} }

// at is where the track shows at now.
func (t track) at(now time.Time) float32 {
	v, el := t.from, now.Sub(t.start)
	if el <= 0 {
		return v
	}
	for _, s := range t.steps {
		if el < s.d {
			return v + (s.to-v)*s.ease(float32(el)/float32(s.d))
		}
		el -= s.d
		v = s.to
	}
	return t.rest
}

// moving reports whether the track is on its way at now.
func (t track) moving(now time.Time) bool {
	var d time.Duration
	for _, s := range t.steps {
		d += s.d
	}
	return len(t.steps) > 0 && now.Sub(t.start) < d
}

// value is the track's value as react-photo-view keeps it: where a
// transition goes, where a tween has got.
func (t track) value(now time.Time) float32 {
	if t.css {
		return t.rest
	}
	return t.at(now)
}

// to sets the track off from where it shows to v, over d on ease: a CSS
// transition, which one on its way there already lets go on, or a tween.
func (t *track) to(now time.Time, v float32, d time.Duration, ease ui.Easing, css bool) {
	if t.rest == v && (!t.moving(now) || t.css && css) {
		return
	}
	*t = track{from: t.at(now), rest: v, start: now, steps: []trackStep{{d, v, ease}}, css: css}
}

// stop stops a tween where it has got, as react-photo-view's stopRaf.
func (t *track) stop(now time.Time) {
	if !t.css {
		*t = still(t.at(now))
	}
}

// photoFit is react-photo-view's getSuitableImageSize: a picture nw×nh at
// its own size where it fits the window vw×vh, else as large as fits it;
// one three or more times as tall as wide fills the width instead, its
// top at the window's, y below the middle.
func photoFit(nw, nh, vw, vh float32) (w, h, y float32) {
	w, h = vw, vh
	switch {
	case nw < vw && nh < vh:
		w, h = nw, nh
	case nw < vw && nh >= vh:
		w = nw / nh * vh
	case nw >= vw && nh < vh || nw/nh > vw/vh:
		h = nh / nw * vw
	case nh/nw >= 3:
		h = nh / nw * vw
		y = (h - vh) / 2
	default:
		w = nw / nh * vh
	}
	return w, h, y
}

// photoScale is react-photo-view's limit of a zoom, as Lody patches it:
// never below the fit, nor above six times it, or the picture's own size
// (natural fits) where that is more, but for over to spare.
func photoScale(s, natural, over float32) float32 {
	return min(max(s, 1), max(6, natural)*(1+over))
}

// photoEdge is react-photo-view's getEdgeInfo: where a picture size long
// at scale, its middle pos from the window's, lies along an axis the
// window spans view of: 1 where it fits, which holds it at the middle; 2
// or 3 where it has gone past its start or end edge, with where that edge
// holds it; 0, and pos, inside them.
func photoEdge(pos, scale, size, view float32) (int, float32) {
	r := size * scale
	i := (r - view) / 2
	switch {
	case r <= view:
		return 1, 0
	case pos > 0 && i-pos <= 0:
		return 2, i
	case pos < 0 && i+pos <= 0:
		return 3, -i
	}
	return 0, pos
}

// photoZoom is react-photo-view's computePositionByScale: where a picture
// w×h, its middle (x, y) from the window's, goes as its zoom goes from
// from to to about the point (cx, cy) of the window, which stays where it
// is on it, and as it is dragged (dx, dy): half as far past an edge.
func photoZoom(x, y, w, h, from, to, cx, cy, dx, dy, vw, vh float32) (float32, float32) {
	ex, _ := photoEdge(x, to, w, vw)
	ey, _ := photoEdge(y, to, h, vh)
	nx := cx - to/from*(cx-(vw/2+x)) - vw/2
	switch {
	case h/w >= 3 && w*to == vw:
	case ex != 0:
		nx += dx / 2
	default:
		nx += dx
	}
	ny := cy - to/from*(cy-(vh/2+y)) - vh/2
	if ey != 0 {
		ny += dy / 2
	} else {
		ny += dy
	}
	return nx, ny
}

// A flung picture slows against a friction of 0.001 DIPs/ms² and 0.0002
// of its speed squared, as react-photo-view's slide: at speed v0, it has
// gone glideDist after ms, and stops after glideStop.
func glideDist(v0, ms float32) float32 {
	t0 := math.Atan(float64(v0) / math.Sqrt(5))
	return float32(5000 * math.Log(math.Cos(t0-math.Sqrt(2e-7)*float64(ms))/math.Cos(t0)))
}

func glideStop(v0 float32) float32 {
	return float32(math.Atan(float64(v0)/math.Sqrt(5)) / math.Sqrt(2e-7))
}

// flung is an axis of a picture let go at at, flung at speed DIPs a
// millisecond, dir of it along the axis: it glides on, slowing, checked
// against its edges as frames come. Meeting one, it eases to it over
// 400ms where it was past it as it was let go (past), or else bounces off
// it: on twice as far as it went past over 100ms, and back over 400ms.
func flung(now time.Time, at, dir, speed, scale, size, view float32, past bool) track {
	stop := glideStop(speed)
	glide := func(ms, to float32) trackStep {
		total := glideDist(speed, ms)
		ease := ui.Easing(ui.Linear)
		if total != 0 {
			ease = func(u float32) float32 { return glideDist(speed, u*ms) / total }
		}
		return trackStep{time.Duration(ms * float32(time.Millisecond)), to, ease}
	}
	t := track{from: at, start: now}
	for ms := float32(0); ; ms += float32(viewerFrame) / float32(time.Millisecond) {
		end := ms >= stop
		if end {
			ms = stop
		}
		p := at + dir*glideDist(speed, ms)
		if edge, e := photoEdge(p, scale, size, view); edge != 0 {
			if past {
				t.steps = []trackStep{glide(ms, p), {viewerSpeed, e, easeQuartOut}}
			} else {
				t.steps = []trackStep{glide(ms, p), {0, e, ui.Linear}, {100 * time.Millisecond, 2*p - e, ui.Linear}, {viewerSpeed, e, easeQuartOut}}
			}
			t.rest = e
			return t
		}
		if end {
			t.steps, t.rest = []trackStep{glide(ms, p)}, p
			return t
		}
	}
}

// openViewer shows the viewer at key, among the pictures items returns
// as they stand; owner, unless empty, is what holds it open (holdViewer).
func (a *App) openViewer(key, owner string, items func(key string) []viewerItem) {
	a.viewer = imageViewer{open: true, key: key, owner: owner, held: true, items: items}
}

// holdViewer keeps the viewer open while owner, what opened it, builds:
// it goes with its opener, as Lody's viewer is the child of the component
// that opens it.
func (a *App) holdViewer(owner string) {
	if a.viewer.open && a.viewer.owner == owner {
		a.viewer.held = true
	}
}

// close closes the viewer, at once, as Lody unmounts it.
func (v *imageViewer) close() { *v = imageViewer{} }

// imageViewerView shows the viewer over everything while it is open.
func (a *App) imageViewerView(c *ui.Context) {
	v := &a.viewer
	if !v.open {
		return
	}
	if v.owner != "" && !v.held {
		v.close() // what opened it is gone
		return
	}
	v.held = false
	items := v.items(v.key)
	index := slices.IndexFunc(items, func(it viewerItem) bool { return it.key == v.key })
	if index < 0 {
		v.close()
		return
	}
	now := c.Now()
	vw, vh := c.Size()
	if v.opened.IsZero() {
		v.opened, v.mask = now, still(viewerMaskAlpha)
	}
	if v.photos == nil {
		v.photos = map[string]*viewerPhoto{}
	}
	if v.vw != 0 && (v.vw != vw || v.vh != vh) {
		v.strip = still(0) // a new size sets the strip at the picture
	}
	v.vw, v.vh, v.index, v.reduce = vw, vh, index, c.Preferences().ReduceMotion
	v.keys = v.keys[:0]
	for _, it := range items {
		v.keys = append(v.keys, it.key)
	}

	// The strip holds the picture and the ones either side of it, round
	// the ends past three, which load with it.
	type slot struct {
		at  int
		it  viewerItem
		ph  *viewerPhoto
		pic *remoteImage
	}
	var slots []slot
	n := len(items)
	for at := -1; at <= 1; at++ {
		i := index + at
		if n > 3 {
			i = (i + n) % n
		} else if i < 0 || i >= n {
			continue
		}
		it := items[i]
		ph := v.photos[it.key]
		if ph == nil {
			ph = &viewerPhoto{shown: now}
			v.photos[it.key] = ph
		}
		pic := it.picture()
		if pic != nil && pic.bitmap != nil {
			ph.show(now, pic.bitmap, vw, vh)
		}
		slots = append(slots, slot{at, it, ph, pic})
	}
	for k := range v.photos {
		if !slices.ContainsFunc(slots, func(s slot) bool { return s.it.key == k }) {
			delete(v.photos, k)
		}
	}
	v.active = v.photos[items[index].key]

	moving := false
	show := func(t track) float32 {
		if v.reduce {
			return t.rest
		}
		if t.moving(now) {
			moving = true
		}
		return t.at(now)
	}
	// While something moves, the next frame comes, once for each instant:
	// one built at the very instant of the last shows the same.
	defer func() {
		if moving && now != v.asked {
			v.asked = now
			c.AnimationFrame()
		}
	}()
	ui.Overlay(c, func() {
		root := ui.Box(c.Key("image-viewer")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Modal().
			Role(ui.RoleDialog).Label(locales.ImagePreview())
		esc := root.OverlayShortcut(0, ui.KeyEscape)
		left := root.OverlayShortcut(0, ui.KeyLeft)
		right := root.OverlayShortcut(0, ui.KeyRight)
		if esc {
			v.close()
			return
		}
		if left || right {
			v.move(int(b2f(right) - b2f(left)))
			c.Invalidate() // the next frame shows it
		}
		root.Children(func() {
			// The mask fades in over 400ms; a drag up or down lightens it.
			fade := float32(1)
			if k := float32(now.Sub(v.opened)) / float32(viewerSpeed); k < 1 && !v.reduce {
				fade, moving = max(k, 0), true
			}
			alpha := show(v.mask)
			ui.Box(c.Key("viewer-mask")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Opacity(fade).Draw(func(p *ui.Painter, r ui.Rect) {
				if !viewerShaderMask {
					p.Fill(r, ui.RGBA(0, 0, 0, alpha), 0)
					return
				}
				viewerMaskShader.Draw(p, r, shader.Options{Uniforms: map[string]any{"Alpha": alpha}, BackdropBlur: 16})
			})
			stage := ui.Box(c.Key("viewer-stage")).Absolute().Left(0).Top(0).Right(0).Bottom(0)
			stage.HandleInput(v.input)
			stage.Children(func() {
				strip := show(v.strip)
				for _, s := range slots {
					if s.ph.ready.IsZero() {
						continue
					}
					x, y, scale := show(s.ph.x), show(s.ph.y), show(s.ph.s)
					opacity := float32(1)
					// Coming in, it grows out of the middle of the window as it fades in.
					if k := float32(now.Sub(s.ph.ready)) / float32(viewerSpeed); s.ph.opens && k < 1 && !v.reduce {
						e := easePhoto(max(k, 0))
						opacity, moving = cssEase(max(k, 0)), true
						scale = 0.01 + (scale-0.01)*e
						x, y = x*e+0.005*s.ph.w*(1-e), y*e+0.005*s.ph.h*(1-e)
					}
					a.viewerPhotoView(c, s.it, s.ph, s.pic, vw/2+float32(s.at)*(vw+viewerGap)+strip, x, y, scale, opacity, s.at == 0)
				}
			})
			v.bar(c, n)
			if n > 1 {
				// Lody's onIndexChange, which every caller with more than one
				// picture passes, moves them; the ends stop them up to three.
				loops := n > 3
				mid := vh/2 - 24
				ui.Box(c.Key("viewer-previous")).Absolute().Left(0).Top(mid).Padding(8).Children(func() {
					viewerButton(c, "previous", iconChevronLeft, locales.ImagePreviewPrevious(), loops || index > 0).OnClick(func() { v.move(-1) })
				})
				ui.Box(c.Key("viewer-next")).Absolute().Right(0).Top(mid).Padding(8).Children(func() {
					viewerButton(c, "next", iconChevronRight, locales.ImagePreviewNext(), loops || index < n-1).OnClick(func() { v.move(1) })
				})
			}
		})
	})
}

// viewerPhotoView is a picture on the strip, its middle at cx across,
// moved by (x, y) and zoomed by scale, inset as the lightbox shows it. The
// picture shown takes the grab cursor, and the secondary click's menu.
func (a *App) viewerPhotoView(c *ui.Context, it viewerItem, ph *viewerPhoto, pic *remoteImage, cx, x, y, scale, opacity float32, active bool) {
	box := ui.Box(c.Key("viewer-photo/"+it.key)).Absolute().Left(cx-ph.w/2).Top(a.viewer.vh/2-ph.h/2).Size(ph.w, ph.h).
		Scale(scale, scale).Translate(x, y)
	if opacity < 1 {
		box.Opacity(opacity)
	}
	box.Children(func() {
		img := ui.Image(c.Key("picture"), pic.bitmap).Size(ph.w, ph.h).Radius(4).Shadow(0, 18, 50, 0, ui.RGBA(0, 0, 0, 0.45)).
			Scale(viewerInset, viewerInset).Cursor(ui.CursorGrab)
		if !active {
			return
		}
		if a.viewer.press.photo {
			img.Cursor(ui.CursorGrabbing)
		}
		img.ContextMenu(func(m *ui.Menu) {
			if m.Item(locales.ImagePreviewCopy()).Chosen() {
				a.copyViewerImage(pic)
			}
			if m.Item(locales.ImagePreviewSave()).Chosen() {
				a.saveViewerImage(pic, it.fileName)
			}
		})
	})
}

// bar is the viewer's bar along the top: the count, keeping its place but
// hidden for one picture, and the close button, which has the focus as
// the viewer opens. It clears the window's controls, and centers on their
// line: the traffic lights' on macOS, as the session header does; the
// caption buttons' strip on Windows.
func (v *imageViewer) bar(c *ui.Context, n int) {
	tb := c.TitleBar()
	height, top, right, left := float32(56), float32(0), float32(4), float32(4)
	switch {
	case tb.Left > 0:
		height, top, left = 44, 2, 80
	case tb.Right > 0:
		height, right = tb.Height, tb.Right+8
	}
	bar := ui.Row(c.Key("viewer-bar")).Absolute().Left(0).Top(0).Right(0).Height(height).Padding(top, right, 0, left).
		AlignItems(ui.Center).Justify(ui.SpaceBetween).Gradient(ui.RGBA(0, 0, 0, 0.45), ui.RGBA(0, 0, 0, 0), 180)
	bar.Children(func() {
		count := ui.Text(c.Key("viewer-count"), fmt.Sprintf("%d / %d", v.index+1, n)).FontSize(14).TextColor(ui.RGBA(255, 255, 255, 0.75)).
			Padding(0, 10).Role(ui.RoleStatus)
		if n < 2 {
			count.Invisible()
		}
		ui.Box(c.Key("viewer-close")).Padding(0, 8).Children(func() {
			// The first the dialog's focus finds, which it moves to as it opens.
			viewerButton(c, "close", iconX, locales.ImagePreviewClose(), true).OnClick(v.close)
		})
	})
}

// viewerButton is @lody/ui's ghost icon Button at its medium step, in the
// dark palette the viewer's controls are forced to: 32 DIPs square, its
// 16px glyph in the secondary ink, the hover fill and the label's ink under
// the pointer, at 45% while it can't be pressed, all over 120ms.
func viewerButton(c *ui.Context, key string, icon *ui.SVG, label string, enabled bool) ui.Element {
	p := &darkPalette
	b := ui.ButtonBase(c.Key("viewer-btn/"+key)).Size(32, 32).Radius(10).Shrink(0).Center().Label(label).Disabled(!enabled).Transition(colorFade)
	ink := p.Secondary
	if enabled && b.Hovered() {
		b.Background(hoverFill(p))
		ink = p.Text
	}
	ink = b.AnimateColor("ink", ink, ui.Animation{Duration: durFast, Ease: easeStandard})
	b.Opacity(b.AnimateWith("dim", map[bool]float32{true: 1, false: 0.45}[enabled], durFast, easeStandard))
	if enabled {
		b.Cursor(ui.CursorPointer)
	}
	b.Children(func() { ui.Icon(c, icon).Size(16, 16).TextColor(ink) })
	return b
}

// show puts the picture bm on the strip, fitted to the window as
// react-photo-view fits it, as it first comes and again as the window
// changes size, keeping its zoom then. Ready within 250ms of coming on,
// it comes in with the open motion, after which its zoom moves on the
// transition.
func (ph *viewerPhoto) show(now time.Time, bm *ui.Bitmap, vw, vh float32) {
	if !ph.ready.IsZero() && ph.vw == vw && ph.vh == vh {
		return
	}
	iw, ih := bm.Size()
	ph.nw, ph.nh = float32(max(iw, 1)), float32(max(ih, 1))
	var y float32
	ph.w, ph.h, y = photoFit(ph.nw, ph.nh, vw, vh)
	ph.vw, ph.vh = vw, vh
	ph.x, ph.y = still(0), still(y)
	if ph.ready.IsZero() {
		ph.ready, ph.s = now, still(1)
		ph.opens = now.Sub(ph.shown) < viewerReady
	}
	ph.eased = ph.opens && now == ph.ready
}

// input takes the viewer's pointer, wheel and pinch, as react-photo-view's
// picture and the mask around it do.
func (v *imageViewer) input(ev ui.InputEvent) bool {
	switch ev.Kind {
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false // the secondary button's menu
		}
		v.down(ev.X, ev.Y, ev.Time)
	case ui.InputPointerMove:
		if !v.press.photo && !v.press.mask {
			return false
		}
		v.drag(ev.X, ev.Y, ev.Time)
	case ui.InputPointerUp:
		if !v.press.photo && !v.press.mask {
			return false
		}
		v.up(ev.X, ev.Y, ev.Time)
	case ui.InputScroll:
		v.wheel(ev.X, ev.Y, ev.DY, ev.Time)
	case ui.InputZoom:
		// Chromium hands a page a pinch as the wheel with Ctrl held, a
		// notch of 100 for each e it scales by.
		if ev.Scale > 0 {
			v.wheel(ev.X, ev.Y, -100*float32(math.Log(float64(ev.Scale))), ev.Time)
		}
	default:
		return false
	}
	return true
}

// ready is the picture shown, once its picture has come.
func (v *imageViewer) ready() *viewerPhoto {
	if ph := v.active; ph != nil && !ph.ready.IsZero() {
		return ph
	}
	return nil
}

// over reports whether (x, y) of the window is on the picture as it shows
// at now: its inset box, which takes the pointer as CSS transforms it.
func (v *imageViewer) over(ph *viewerPhoto, x, y float32, now time.Time) bool {
	cx, cy := v.vw/2+v.strip.at(now)+ph.x.at(now), v.vh/2+ph.y.at(now)
	s := ph.s.at(now) * viewerInset
	return abs32(x-cx) <= ph.w*s/2 && abs32(y-cy) <= ph.h*s/2
}

// down is a press: on the picture, which stops its tweens where they are
// and its transitions where they go, or on the mask.
func (v *imageViewer) down(x, y float32, now time.Time) {
	ph := v.ready()
	on := ph != nil && v.over(ph, x, y, now)
	pr := viewerPress{photo: on, mask: !on, cx: x, cy: y, lastCX: x, lastCY: y, lastScale: 1}
	if ph != nil {
		if on {
			ph.x, ph.y, ph.s = still(ph.x.value(now)), still(ph.y.value(now)), still(ph.s.value(now))
			pr.at = now
		}
		pr.lastX, pr.lastY, pr.lastScale = ph.x.value(now), ph.y.value(now), ph.s.value(now)
	}
	v.press = pr
}

// drag moves the press: past 20 DIPs it takes a direction; the strip
// takes one across where the picture fits or is at its edge, the mask one
// up or down where it fits; the picture follows the rest, half as far
// past its edges.
func (v *imageViewer) drag(x, y float32, now time.Time) {
	pr := &v.press
	if pr.dir == 0 {
		nearX, nearY := abs32(x-pr.cx) <= viewerDead, abs32(y-pr.cy) <= viewerDead
		switch {
		case nearX && nearY:
			pr.lastCX, pr.lastCY = x, y
			return
		case !nearX:
			pr.dir = 1
		case y > pr.cy:
			pr.dir = 3
		default:
			pr.dir = 2
		}
	}
	ph := v.ready()
	var w, h float32
	scale := float32(1)
	if ph != nil {
		w, h, scale = ph.w, ph.h, ph.s.value(now)
	}
	dx, dy := x-pr.lastCX, y-pr.lastCY
	ex, _ := photoEdge(dx+pr.lastX, scale, w, v.vw)
	ey, _ := photoEdge(dy+pr.lastY, scale, h, v.vh)
	var reach byte
	switch {
	case ex != 0 && pr.dir == 1 || pr.reach == 'x':
		reach = 'x'
	case ey != 0 && pr.dir > 1 || pr.reach == 'y':
		reach = 'y'
	}
	if reach != 0 {
		v.reachMove(reach, x, y, scale)
	}
	if reach == 'x' || pr.mask || ph == nil {
		pr.reach = 'x'
		return
	}
	s := photoScale(scale, ph.nw/ph.w, 0.2)
	nx, ny := photoZoom(ph.x.rest, ph.y.rest, w, h, scale, s, x, y, dx, dy, v.vw, v.vh)
	ph.x, ph.y, ph.s = still(nx), still(ny), still(s)
	pr.reach, pr.lastCX, pr.lastCY = reach, x, y
}

// reachMove is the strip taking a drag the picture hands it: across, the
// strip follows the pointer, half as far past the first and the last
// picture up to three; up or down, at the picture's fit, the mask lightens
// by a 400th of its black for each DIP, to a hundredth.
func (v *imageViewer) reachMove(reach byte, x, y, scale float32) {
	switch reach {
	case 'x':
		if !v.dragX {
			v.dragX, v.fromX, v.strip = true, x, still(v.strip.rest)
			return
		}
		d, n := x-v.fromX, len(v.keys)
		if n <= 3 && (v.index == 0 && d > 0 || v.index == n-1 && d < 0) {
			d /= 2
		}
		v.strip = still(d)
	case 'y':
		if !v.dragY {
			v.dragY, v.fromY, v.mask = true, y, still(v.mask.rest)
			return
		}
		alpha := float32(viewerMaskAlpha)
		if scale == 1 {
			alpha = max(viewerMaskAlpha-abs32(y-v.fromY)/400, 0.01)
		}
		v.mask = still(alpha)
	}
}

// up lets the press go: the picture settles, the strip goes on to the
// next picture past 40 DIPs or back, and the mask darkens again. Let go
// where it went down, a press on the picture taps it, one on the mask
// closes the viewer.
func (v *imageViewer) up(x, y float32, now time.Time) {
	pr := v.press
	v.press = viewerPress{}
	if ph := v.ready(); ph != nil {
		ph.eased = true
		ph.settle(now, pr, photoScale(ph.s.value(now), ph.nw/ph.w, 0), v.vw, v.vh)
	}
	d := float32(0)
	if v.dragX {
		d = x - v.fromX
	}
	v.dragX, v.dragY = false, false
	switch {
	case d < -viewerSwipe:
		v.slideTo(now, v.index+1)
	case d > viewerSwipe:
		v.slideTo(now, v.index-1)
	default:
		v.strip.to(now, 0, viewerSlide, easePhoto, true)
		v.mask.to(now, viewerMaskAlpha, viewerSpeed, easePhoto, true)
	}
	// Where it went down, to the pixel, as the browser counts.
	if math.Round(float64(x)) != math.Round(float64(pr.cx)) || math.Round(float64(y)) != math.Round(float64(pr.cy)) {
		return
	}
	switch {
	case pr.photo:
		v.tap(x, y, now, pr.reach)
	case pr.mask:
		v.close()
	}
}

// settle is the picture let go: held 200ms or more, or zoomed past its
// limits, it eases back inside its edges and its limits over 400ms; flung
// sooner, it glides on, slowing, off the edges it meets.
func (ph *viewerPhoto) settle(now time.Time, pr viewerPress, safe, vw, vh float32) {
	x, y, s := ph.x.value(now), ph.y.value(now), ph.s.value(now)
	fx, ax := photoEdge(x, safe, ph.w, vw)
	fy, ay := photoEdge(y, safe, ph.h, vh)
	held := now.Sub(pr.at)
	if pr.at.IsZero() || held >= viewerFlick || safe != s || abs32(pr.lastScale-s) > 1 {
		nx, ny := photoZoom(x, y, ph.w, ph.h, s, safe, vw/2, vh/2, 0, 0, vw, vh)
		if fx != 0 {
			nx = ax
		}
		if fy != 0 {
			ny = ay
		}
		if nx != x {
			ph.x.to(now, nx, viewerSpeed, easeQuartOut, false)
		}
		if ny != y {
			ph.y.to(now, ny, viewerSpeed, easeQuartOut, false)
		}
		if safe != s {
			ph.s.to(now, safe, viewerSpeed, easeQuartOut, false)
		}
		return
	}
	ms := float32(held) / float32(time.Millisecond)
	if ms <= 0 {
		return // let go as it went down, it has no speed
	}
	vx, vy := (x-pr.lastX)/ms, (y-pr.lastY)/ms
	speed := float32(math.Hypot(float64(vx), float64(vy)))
	if speed == 0 {
		return
	}
	ph.x = flung(now, x, vx/speed, speed, s, ph.w, vw, fx != 0)
	ph.y = flung(now, y, vy/speed, speed, s, ph.h, vh, fy != 0)
}

// tap is a click on the picture: a second within 300ms of the first zooms
// it, about the pointer, to twice its fit or its own size where that is
// more, or back to its fit.
func (v *imageViewer) tap(x, y float32, now time.Time, reach byte) {
	if v.tapped.IsZero() || now.Sub(v.tapped) >= viewerTaps {
		v.tapped = now
		return
	}
	v.tapped = time.Time{}
	ph := v.ready()
	if ph == nil || reach != 0 {
		return
	}
	target := float32(1)
	if ph.s.value(now) == 1 {
		target = max(2, ph.nw/ph.w)
	}
	v.zoom(ph, now, target, x, y)
}

// wheel zooms the picture under the pointer by a 200th for each DIP the
// wheel turns, about the pointer, within its limits.
func (v *imageViewer) wheel(x, y, dy float32, now time.Time) {
	ph := v.ready()
	if ph == nil || v.press.reach != 0 || !v.over(ph, x, y, now) {
		return
	}
	target := photoScale(ph.s.value(now)-dy/200, ph.nw/ph.w, 0)
	ph.x.stop(now)
	ph.y.stop(now)
	ph.s.stop(now)
	v.zoom(ph, now, target, x, y)
}

// zoom zooms the picture to target about (cx, cy) of the window, back to
// the middle at its fit: on the 400ms transition once it has eased in, at
// once before.
func (v *imageViewer) zoom(ph *viewerPhoto, now time.Time, target, cx, cy float32) {
	s := ph.s.value(now)
	if s == target {
		return
	}
	x, y := photoZoom(ph.x.value(now), ph.y.value(now), ph.w, ph.h, s, target, cx, cy, 0, 0, v.vw, v.vh)
	if target <= 1 {
		x, y = 0, 0
	}
	if !ph.eased {
		ph.x, ph.y, ph.s = still(x), still(y), still(target)
		return
	}
	ph.x.to(now, x, viewerSpeed, easePhoto, true)
	ph.y.to(now, y, viewerSpeed, easePhoto, true)
	ph.s.to(now, target, viewerSpeed, easePhoto, true)
}

// slideTo is a swipe going to picture i, as react-photo-view's
// changeIndex: the strip slides to it over 600ms, round the ends past
// three pictures, else back from them.
func (v *imageViewer) slideTo(now time.Time, i int) {
	n := len(v.keys)
	if n <= 3 {
		i = min(max(i, 0), n-1)
	}
	from := v.strip.at(now) + float32(i-v.index)*(v.vw+viewerGap)
	v.strip = track{from: from, rest: 0, start: now, steps: []trackStep{{viewerSlide, 0, easePhoto}}, css: true}
	i = (i + n) % n
	v.index, v.key, v.active = i, v.keys[i], v.photos[v.keys[i]]
}

// move goes offset pictures on, as the side buttons and the arrow keys
// do: round the ends past three pictures, else stopping at them. The
// strip stays as the picture on it changes; past three, the pictures come
// on it afresh, with their open motion, as react-photo-view mounts them
// again.
func (v *imageViewer) move(offset int) {
	n := len(v.keys)
	if n < 2 {
		return
	}
	i := v.index + offset
	if n > 3 {
		i = (i + n) % n
		v.photos = nil
	} else {
		i = min(max(i, 0), n-1)
	}
	v.index, v.key, v.active = i, v.keys[i], nil
}

// copyViewerImage puts the picture on the clipboard as a PNG, the one
// raster the clipboard reliably takes, as Lody re-encodes all but a PNG
// for it, and says how it went.
func (a *App) copyViewerImage(pic *remoteImage) {
	data, err := viewerPNG(pic.data, pic.mime)
	if err == nil {
		err = shareImageClipboard(data)
	}
	if err != nil {
		a.toasts = append(a.toasts, ui.Toast{Type: "danger", Title: locales.ImagePreviewCopyFailed(), Description: err.Error()})
		return
	}
	a.toast("success", locales.ImagePreviewCopied())
}

// saveViewerImage saves the picture's bytes, as they came, where the user
// picks, and says how it went; backing out of the dialog is a choice, not
// a failure, and says nothing.
func (a *App) saveViewerImage(pic *remoteImage, fileName string) {
	data, win := pic.data, a.win
	name := exportFileName(fileName, pictureType(pic.data, pic.mime))
	go func() {
		path, err := shareImageSaveDialog(win, name)
		if err == nil && path == "" {
			return
		}
		if err == nil {
			err = os.WriteFile(path, data, 0o644)
		}
		a.update(func() {
			if err != nil {
				a.toasts = append(a.toasts, ui.Toast{Type: "danger", Title: locales.ImagePreviewSaveFailed(), Description: err.Error()})
				return
			}
			a.toast("success", locales.ImagePreviewSaved())
		})
	}()
}

// viewerPNG is a picture's bytes as a PNG.
func viewerPNG(data []byte, mime string) ([]byte, error) {
	if pictureType(data, mime) == "image/png" {
		return data, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// pictureType is a picture's media type, as it came or from its bytes.
func pictureType(data []byte, mime string) string {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if mime != "" && mime != "application/octet-stream" {
		return mime
	}
	return http.DetectContentType(data)
}

// exportTypes are the extensions a saved picture's name takes by its type.
var exportTypes = map[string]string{
	"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp",
	"image/avif": "avif", "image/bmp": "bmp", "image/svg+xml": "svg",
}

var hasExtension = regexp.MustCompile(`(?i)\.[a-z0-9]{1,8}$`)

// exportFileName is the save dialog's default name for a picture: its own,
// with its type's extension only where it has none, so that a .jpeg is not
// renamed; image and its extension without one.
func exportFileName(source, mime string) string {
	base := source
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSpace(base)
	ext := exportTypes[mime]
	switch {
	case base == "" && ext == "":
		return "image.png"
	case base == "":
		return "image." + ext
	case ext == "" || hasExtension.MatchString(base):
		return base
	}
	return base + "." + ext
}

// galleryKey names a picture of a conversation: its message, the item and
// the picture of it.
func galleryKey(entryID string, item, image int, imageID string) string {
	return fmt.Sprintf("%s:%d:%d:%s", entryID, item, image, imageID)
}

// openSessionGallery opens the viewer at the picture key of the open
// conversation.
func (a *App) openSessionGallery(sessionID, key string) {
	a.openViewer(key, "session/"+sessionID, func(key string) []viewerItem { return a.sessionGallery(sessionID, key) })
}

// sessionGallery is the pictures of the conversation that go with the one
// at key, as Lody's session gallery groups them: a user's message's with
// those of the replies to it, in order; none once it is gone.
func (a *App) sessionGallery(sessionID, key string) []viewerItem {
	if a.selected != sessionID || a.view == nil {
		return nil
	}
	type picture struct {
		group string
		item  viewerItem
	}
	var all []picture
	group := ""
	for _, e := range a.view.Entries {
		g := e.ID
		if id := strings.TrimSpace(e.UserTurnID); e.Role != "user" && id != "" {
			g = id
		}
		add := func(i, j int, it session.Item) {
			k := galleryKey(e.ID, i, j, it.ImageID)
			if k == key {
				group = g
			}
			all = append(all, picture{g, viewerItem{key: k, fileName: it.FileName, picture: func() *remoteImage { return a.sessionOriginal(sessionID, it) }}})
		}
		for i, it := range e.Items {
			switch it.Type {
			case "image":
				add(i, 0, it)
			case "image_group":
				for j, img := range it.Images {
					add(i, j, img)
				}
			}
		}
	}
	var out []viewerItem
	for _, p := range all {
		if p.group == group {
			out = append(out, p.item)
		}
	}
	return out
}

// originalsCap is how much of the originals fetched is kept, as Lody's
// session image cache keeps 200MB of what it loaded.
const originalsCap = 200 << 20

// sessionOriginal is a session's picture at its size, as Lody's viewer
// loads it (the original, not a thumbnail): fetched once and kept while
// the originals kept stay under their cap, nil without a connection to
// fetch it.
func (a *App) sessionOriginal(sessionID string, it session.Item) *remoteImage {
	key := "orig:" + it.ImageID
	img := a.images[key]
	if img != nil {
		img.used = time.Now()
	}
	if img == nil && a.conn != nil {
		img = &remoteImage{loading: true}
		a.images[key] = img
		api, workspace, storage := a.api, a.conn.ID, firstNonEmpty(it.StorageSessionID, sessionID)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			data, mime, err := api.SessionImageOriginal(ctx, a.tokenNow(), workspace, storage, it.ImageID, cloud.MaxFileBytes)
			var bm *ui.Bitmap
			if err == nil {
				bm, err = ui.DecodeBitmap(data)
			}
			a.update(func() {
				a.images[key] = &remoteImage{bitmap: bm, data: data, mime: mime, failed: err != nil, used: time.Now()}
				a.trimOriginals()
			})
		}()
	}
	return img
}

// trimOriginals lets go of the originals shown least lately while those
// kept come to more than their cap, by their bytes as fetched, as Lody's
// cache counts them.
func (a *App) trimOriginals() {
	var keys []string
	total := 0
	for k, img := range a.images {
		if strings.HasPrefix(k, "orig:") && !img.loading {
			keys = append(keys, k)
			total += len(img.data)
		}
	}
	if total <= originalsCap {
		return
	}
	slices.SortFunc(keys, func(x, y string) int { return a.images[x].used.Compare(a.images[y].used) })
	for _, k := range keys {
		if total <= originalsCap {
			break
		}
		total -= len(a.images[k].data)
		delete(a.images, k)
	}
}

// queuedPicture is a queued message's picture at its size, which loads as
// its preview opens; failing that, its thumbnail rather than nothing.
func (a *App) queuedPicture(sessionID string, it session.Item) *remoteImage {
	img := a.sessionOriginal(sessionID, it)
	if img != nil && img.failed {
		if thumb := a.images["img:"+it.ImageID]; thumb != nil && thumb.bitmap != nil {
			return thumb
		}
	}
	return img
}
