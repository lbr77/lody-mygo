package app

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
)

// Lody's status illustrations (lib/status-illustrations.ts): the drawing
// a status page opens with. Both are a Lody window — a surface, three
// dots, a sidebar, content — so they say "this app" rather than "an
// error", and each moves once to say what happened, then is still:
// "broken", a piece of the content slips out of its slot and lands below
// the window; "missing", a lens sweeps an empty content area and comes to
// rest on its corner. A page that keeps moving keeps asking for attention,
// which is the opposite of what it is for.
//
// They are Lody's SVG, 200×140, drawn from its markup. What moves is an
// element of its own, drawn in the box of the drawing it moves in, centred
// where CSS's transform-box: fill-box puts its origin (the middle of its
// shapes), so it turns, moves, scales and fades as its keyframes say.

// statusArt is the drawing a status page opens with.
type statusArt string

const (
	artBroken  statusArt = "broken"
	artMissing statusArt = "missing"
)

// siWindow is the window both drawings stand on, as Lody's WINDOW.
const siWindow = `<ellipse cx="100" cy="112" rx="66" ry="4" fill="var(--si-shadow)"/>` +
	`<rect x="30.5" y="12.5" width="139" height="95" rx="13" fill="var(--si-surface)" stroke="var(--si-edge)"/>` +
	`<circle cx="45" cy="25" r="3" fill="var(--si-dot)"/>` +
	`<circle cx="55" cy="25" r="3" fill="var(--si-dot)"/>` +
	`<circle cx="65" cy="25" r="3" fill="var(--si-dot)"/>` +
	`<rect x="40" y="36" width="28" height="62" rx="6" fill="var(--si-region)"/>` +
	`<rect x="45" y="43" width="18" height="4" rx="2" fill="var(--si-line)"/>` +
	`<rect x="45" y="52" width="14" height="4" rx="2" fill="var(--si-line)"/>` +
	`<rect x="45" y="61" width="16" height="4" rx="2" fill="var(--si-line)"/>`

// siPart is a part of a drawing and the box of it the part is drawn in,
// in the drawing's units.
type siPart struct {
	x, y, w, h float32
	markup     string
}

// svg is the part as a document of its own, its view box its box.
func (s siPart) svg() string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="%g %g %g %g" fill="none">%s</svg>`, s.x, s.y, s.w, s.h, s.markup)
}

var (
	// What stays still: the window, and in "broken" its content with the
	// slot the piece left, dashed where it should be; in "missing" the
	// empty content, dashed.
	siBrokenStill = siPart{0, 0, 200, 140, siWindow +
		`<rect x="76" y="38" width="82" height="8" rx="4" fill="var(--si-line)"/>` +
		`<rect x="76" y="52" width="60" height="8" rx="4" fill="var(--si-line)"/>` +
		`<rect x="76.5" y="66.5" width="73" height="17" rx="5" stroke="var(--si-dot)" stroke-dasharray="3 3"/>` +
		`<rect x="76" y="90" width="46" height="8" rx="4" fill="var(--si-line)"/>`}
	siMissingStill = siPart{0, 0, 200, 140, siWindow +
		`<rect x="76.5" y="38.5" width="81" height="59" rx="6" stroke="var(--si-dot)" stroke-dasharray="3 3"/>`}

	// The shadow the piece lands on, centred on (142, 135).
	siBrokenLanding = siPart{105, 130.5, 74, 9, `<ellipse cx="142" cy="135" rx="36" ry="3.5" fill="var(--si-shadow)"/>`}
	// The bump where its low corner hit the ground, centred on (99, 122).
	siBump = siPart{90, 113, 18, 18, `<g stroke="var(--si-accent)" stroke-width="2" stroke-linecap="round">` +
		`<path d="M99 129 L92 126.5"/><path d="M101 123.5 L96 118"/><path d="M106 121 L105.5 115"/></g>`}
	// The piece, at rest tipped onto one corner, centred on (142, 121).
	siFall = siPart{104, 111, 76, 20, `<g transform="translate(142 121)">` +
		`<rect x="-37" y="-9" width="74" height="18" rx="5" fill="var(--si-accent)"/>` +
		`<rect x="-29" y="-2" width="30" height="4" rx="2" fill="var(--si-on-accent)" opacity="0.7"/></g>`}

	// The shadow the lens comes to rest over, centred on (160, 133).
	siMissingLanding = siPart{137, 129, 46, 8, `<ellipse cx="160" cy="133" rx="22" ry="3" fill="var(--si-shadow)"/>`}
	// The lens at rest, its shapes from (134, 84) to (174, 124), so
	// centred on (154, 104).
	siSeek = siPart{130, 80, 48, 48, `<g transform="translate(150 100)">` +
		`<line x1="12" y1="12" x2="24" y2="24" stroke="var(--si-ink)" stroke-width="6" stroke-linecap="round"/>` +
		`<circle r="16" fill="var(--si-surface)" stroke="var(--si-accent)" stroke-width="4"/>` +
		`<path d="M-8 -3 A9 9 0 0 1 -3 -8" stroke="var(--si-accent)" stroke-width="2.5" stroke-linecap="round" opacity="0.55"/></g>`}
)

// siColors are the drawing's colours, as Lody's StatusPage sets them from
// the rungs it depicts: the window on the raised rung, its sidebar the
// secondary ground, its bars and marks the grays, the moving part the
// accent; and its shadows the ground it stands on, a step darker
// (color-mix in oklab with 8% black), which in the dark palette is small,
// as shadows read there.
func siColors(p *palette) *strings.Replacer {
	label, gray2, gray3, gray4 := ui.Hex("#1C1C1C"), ui.Hex("#B3B3B3"), ui.Hex("#C7C7C7"), ui.Hex("#D9D9D9")
	if p.Dark {
		label, gray2, gray3, gray4 = ui.Hex("#FFFFFF"), ui.Hex("#575757"), ui.Hex("#454545"), ui.Hex("#383838")
	}
	hex := func(c ui.Color) string { return fmt.Sprintf("#%02x%02x%02x%02x", c.R, c.G, c.B, c.A) }
	return strings.NewReplacer(
		"var(--si-surface)", hex(raisedGround(p)),
		"var(--si-edge)", hex(label.Alpha(0.1)),
		"var(--si-shadow)", hex(darkerOklab(p.Canvas, 0.08)),
		"var(--si-region)", hex(secondaryGround(p)),
		"var(--si-line)", hex(gray4),
		"var(--si-dot)", hex(gray3),
		"var(--si-accent)", hex(p.Primary),
		"var(--si-on-accent)", hex(p.OnPrimary),
		"var(--si-ink)", hex(gray2),
	)
}

// darkerOklab is c mixed with black in Oklab, k of the way, as CSS's
// color-mix(in oklab, c, black k): Oklab is linear light cubed through
// linear maps, so the mix scales c's linear light by (1-k)³.
func darkerOklab(c ui.Color, k float64) ui.Color {
	s := math.Pow(1-k, 3)
	ch := func(v uint8) uint8 {
		x := float64(v) / 255
		if x <= 0.04045 {
			x /= 12.92
		} else {
			x = math.Pow((x+0.055)/1.055, 2.4)
		}
		x *= s
		if x <= 0.0031308 {
			x *= 12.92
		} else {
			x = 1.055*math.Pow(x, 1/2.4) - 0.055
		}
		return uint8(math.Round(x * 255))
	}
	return ui.Color{R: ch(c.R), G: ch(c.G), B: ch(c.B), A: c.A}
}

// siPictures are the parts parsed, by markup and palette: parsed once, as
// MyGo asks of an SVG.
var siPictures struct {
	sync.Mutex
	m map[siPictureKey]*ui.SVG
}

type siPictureKey struct {
	markup string
	dark   bool
}

// siPicture is part as p colours it.
func siPicture(p *palette, part siPart) *ui.SVG {
	siPictures.Lock()
	defer siPictures.Unlock()
	k := siPictureKey{part.markup, p.Dark}
	if s := siPictures.m[k]; s != nil {
		return s
	}
	if siPictures.m == nil {
		siPictures.m = map[siPictureKey]*ui.SVG{}
	}
	s := ui.MustParseSVG([]byte(siColors(p).Replace(part.svg())))
	siPictures.m[k] = s
	return s
}

// The motion, once: each part holds for a 250ms beat, then plays its
// keyframes; where the desktop asks for less motion MyGo's Keyframes end
// at once, at rest, as Lody's prefers-reduced-motion leaves the drawing
// (its bump at 0.7).
const (
	siBeat = 250 * time.Millisecond
	// siDrop is how long the piece falls, its bump comes and its shadow
	// gathers; siSweep how long the lens crosses.
	siDrop  = 1200 * time.Millisecond
	siSweep = 1500 * time.Millisecond
)

// siGravity is the fall's ease-in, cubic-bezier(0.5, 0, 1, 1); the rest
// move on Lody's ease.standard, cubic-bezier(0.2, 0, 0, 1).
var siGravity = ui.CubicBezier(0.5, 0, 1, 1)

// siTrack plays one property's CSS keyframes on el once, after the beat:
// at are their offsets, as shares of d; eases the timing function each
// keyframe starts, nil for linear (CSS eases a step by the keyframe it
// leaves, MyGo by the one it arrives at); values theirs.
func siTrack(el ui.Element, key string, d time.Duration, at []float64, eases []ui.Easing, values ...float32) float32 {
	frames := make([]ui.Keyframe, len(at))
	for i := range at {
		frames[i] = ui.Keyframe{At: time.Duration(at[i] * float64(d)), Value: values[i]}
		if i > 0 && eases != nil {
			frames[i].Ease = eases[i-1]
		}
	}
	return el.Keyframes(key, frames, ui.KeyframeOptions{Delay: siBeat})
}

// siElement places part in the drawing, an element of its own.
func siElement(c *ui.Context, p *palette, key string, part siPart) ui.Element {
	pic := siPicture(p, part)
	return ui.Box(c.Key(key)).Absolute().Left(part.x).Top(part.y).Size(part.w, part.h).PassThrough().
		Draw(func(pt *ui.Painter, r ui.Rect) { pt.Image(pic, r, ui.FillBox) })
}

// statusArtView draws a status page's drawing, 200 wide, and plays its
// motion once.
func statusArtView(c *ui.Context, p *palette, art statusArt) {
	still := siBrokenStill
	if art == artMissing {
		still = siMissingStill
	}
	pic := siPicture(p, still)
	box := ui.Box(c.Key("status-art/"+string(art))).Size(200, 140).Shrink(0).Margin(0, 0, 8, 0).PassThrough().
		Draw(func(pt *ui.Painter, r ui.Rect) { pt.Image(pic, r, ui.FillBox) })
	box.Children(func() {
		switch art {
		case artBroken:
			siLand(c, p, siBrokenLanding)
			siBumpIn(c, p)
			siFallIn(c, p)
		case artMissing:
			siLand(c, p, siMissingLanding)
			siSeekIn(c, p)
		}
	})
}

// siFallIn is lody-si-fall, over 1.2s: the piece holds in its slot
// (113, 75) for a beat, tips 8° on ease.standard, falls with gravity's
// ease-in, tipping back to -10°, and settles at rest with one small
// bounce, 2px up and back, at -6°.
func siFallIn(c *ui.Context, p *palette) {
	el := siElement(c, p, "si-fall", siFall)
	at := []float64{0, 0.22, 0.34, 0.74, 0.86, 1}
	eases := []ui.Easing{easeStandard, easeStandard, siGravity, easeStandard, siGravity}
	x := siTrack(el, "x", siDrop, at, eases, -29, -29, -25, 0, 0, 0)
	y := siTrack(el, "y", siDrop, at, eases, -46, -46, -47, 0, -2, 0)
	turn := siTrack(el, "turn", siDrop, at, eases, 0, 0, 8, -10, -4, -6)
	// CSS's translate() rotate() around the middle: turned there, then
	// moved.
	el.Rotate(turn).Translate(x, y)
}

// siBumpIn is lody-si-bump, over 1.2s on ease.standard: the bump appears
// as the piece lands, swelling from 0.4 past its size to 1.1, and settles
// at 0.7.
func siBumpIn(c *ui.Context, p *palette) {
	el := siElement(c, p, "si-bump", siBump)
	at := []float64{0, 0.7, 0.82, 1}
	eases := []ui.Easing{easeStandard, easeStandard, easeStandard}
	fade := siTrack(el, "opacity", siDrop, at, eases, 0, 0, 1, 0.7)
	scale := siTrack(el, "scale", siDrop, at, eases, 0.4, 0.4, 1.1, 1)
	el.Scale(scale, scale).Opacity(fade)
}

// siLand is lody-si-land, over 1.2s, linear: the shadow under what comes
// to rest gathers as it lands, stretching from half its width past it to
// 1.04, and back.
func siLand(c *ui.Context, p *palette, part siPart) {
	el := siElement(c, p, "si-landing", part)
	at := []float64{0, 0.6, 0.74, 1}
	fade := siTrack(el, "opacity", siDrop, at, nil, 0, 0, 1, 1)
	stretch := siTrack(el, "stretch", siDrop, at, nil, 0.5, 0.5, 1.04, 1)
	el.Scale(stretch, 1).Opacity(fade)
}

// siSeekIn is lody-si-seek, over 1.5s on ease.standard: the lens fades in
// over its first 12% as it crosses the empty content from up and left,
// turned -10°, by way of 6° at 55%, to rest on its corner.
func siSeekIn(c *ui.Context, p *palette) {
	el := siElement(c, p, "si-seek", siSeek)
	move := []float64{0, 0.55, 1}
	eases := []ui.Easing{easeStandard, easeStandard}
	x := siTrack(el, "x", siSweep, move, eases, -58, -24, 0)
	y := siTrack(el, "y", siSweep, move, eases, -52, -40, 0)
	turn := siTrack(el, "turn", siSweep, move, eases, -10, 6, 0)
	fade := siTrack(el, "opacity", siSweep, []float64{0, 0.12, 1}, eases, 0, 1, 1)
	el.Rotate(turn).Translate(x, y).Opacity(fade)
}
