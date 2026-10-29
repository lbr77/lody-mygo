package app

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// Lody's chat share card (share-card/chat-share-card.tsx): ONE fixed
// template, not an appearance editor. One gutter per band, left-aligned
// turns, unconditional code wrap, no height cap, no QR. Size (chat or
// post), the mat, the backdrop and the palette are the only choices, and
// none of them changes the layout; every interior dimension lives in
// shareLayouts.

// shareMsg is a selected message as the card shows it: its prose, with
// the estimate of its whole stored turn and the model it ran on.
type shareMsg struct {
	id     string
	role   string // user or assistant
	text   string
	tokens int    // thinking, plans and tool text counted too
	model  string // the turn's own model, never the composer's
}

// shareMeta is the caption: the runtime that produced the conversation,
// its parameters and the capture date.
type shareMeta struct {
	name   string
	icon   *ui.SVG // nil for Lody's mark
	params []string
	date   string
}

// shareCardSpec is everything one card is drawn from.
type shareCardSpec struct {
	messages []shareMsg
	title    string
	dest     string  // chat or post
	mat      float32 // ground around the card, in DIPs
	dark     bool    // the card's own palette, not the app's
	backdrop string  // none, lody, welcome, aurora, ocean, sunset
	meta     shareMeta
}

// The mat is the one continuous dimension: a slider over pixels on a 4px
// grid, seeded from the size.
const (
	shareMinMat     = 0
	shareMaxMat     = 96
	shareMatStep    = 4
	shareMinSignOff = 12 // under it, the sign-off moves into the caption
	shareBodySize   = 15 // prose, fixed for both sizes and the app's setting
)

var shareDefaultMat = map[string]float32{"chat": 16, "post": 56}

// shareLayout is one size's interior: the two rows LAYOUT holds.
type shareLayout struct {
	width, radius, gutter, top     float32
	titleSize, afterTitle, bottom  float32
	exchangeGap, replyGap          float32
	promptRadius, promptX, promptY float32
	captionPad, signature          float32
}

var shareLayouts = map[string]shareLayout{
	// A handset's own content width, for a message thread.
	"chat": {width: 360, radius: 22, gutter: 20, top: 24, titleSize: 17, afterTitle: 16, bottom: 20,
		exchangeGap: 24, replyGap: 12, promptRadius: 14, promptX: 14, promptY: 10, captionPad: 14, signature: 16},
	// Room for a line of prose and a real line of code, for a feed.
	"post": {width: 560, radius: 26, gutter: 28, top: 28, titleSize: 20, afterTitle: 20, bottom: 24,
		exchangeGap: 28, replyGap: 14, promptRadius: 16, promptX: 16, promptY: 12, captionPad: 16, signature: 20},
}

// shareBackdrops are the grounds in the picker's order.
var shareBackdrops = []string{"none", "lody", "welcome", "aurora", "ocean", "sunset"}

// matPx is the ground that shows: none without a backdrop.
func (s shareCardSpec) matPx() float32 {
	if s.backdrop == "none" {
		return 0
	}
	return max(shareMinMat, s.mat)
}

// signOffOnMat says whether the sign-off prints on the ground below the
// card rather than in its caption.
func (s shareCardSpec) signOffOnMat() bool {
	return s.backdrop != "none" && s.matPx() >= shareMinSignOff
}

// groundWidth is the exported image's width in DIPs.
func (s shareCardSpec) groundWidth() float32 {
	return shareLayouts[s.dest].width + 2*s.matPx()
}

// sharePalette is the card's palette: Lody's bundled light or dark theme,
// whatever the app wears.
func sharePalette(dark bool) *palette {
	if dark {
		return &darkPalette
	}
	return &lightPalette
}

// shareCard builds the card on its ground, at its natural size.
func shareCard(c *ui.Context, s shareCardSpec) ui.Element {
	l := shareLayouts[s.dest]
	cp := sharePalette(s.dark)
	mat := s.matPx()
	card := func() {
		// bg-card is the theme's sidebar step; a ring inside its edge.
		box := ui.Column(c.Key("card")).Width(l.width).Shrink(0).Radius(l.radius).Background(cp.Ground).Clip()
		ring := ui.RGBA(0, 0, 0, 0.06)
		if s.dark {
			ring = ui.RGBA(255, 255, 255, 0.10)
		}
		box.InsetShadow(0, 0, 0, 1, ring)
		if mat >= shareMinSignOff {
			// A drop shadow needs a ground to fall on.
			box.Shadow(0, 28, 70, -20, ui.RGBA(2, 10, 18, 0.6))
		}
		box.Children(func() {
			if s.title != "" {
				ui.Column(c.Key("title")).FillWidth().Padding(l.top, l.gutter, 0, l.gutter).Children(func() {
					ui.Text(c, s.title).FontSize(l.titleSize).FontWeight(600).LineHeight(1.375).
						LetterSpacing(-0.025 * l.titleSize).TextColor(cp.Text).MaxLines(2)
				})
			}
			top := l.top
			if s.title != "" {
				top = l.afterTitle
			}
			md := shareMarkdown(s.dark)
			ui.Column(c.Key("turns")).FillWidth().Padding(top, l.gutter, l.bottom, l.gutter).Children(func() {
				for i, m := range s.messages {
					// A reply belongs to the prompt above it; anything else
					// opens a new exchange and gets the wider gap.
					gap := float32(0)
					switch {
					case i == 0:
					case m.role == "assistant" && s.messages[i-1].role == "user":
						gap = l.replyGap
					default:
						gap = l.exchangeGap
					}
					turn := ui.Column(c.Key(fmt.Sprint("m/", i, "/", m.id))).FillWidth().Margin(gap, 0, 0, 0)
					turn.Children(func() {
						if m.role == "user" {
							ui.Column(c).FillWidth().Radius(l.promptRadius).Padding(l.promptY, l.promptX).
								Border(1, cp.Text.Alpha(0.07)).Background(cp.Text.Alpha(0.045)).Children(func() {
								ui.Text(c, m.text).FontSize(shareBodySize).LineHeight(1.55).TextColor(cp.Text)
							})
							return
						}
						markdown.View(c, m.text, md)
					})
				}
			})
			shareCaption(c, s, l, cp)
		})
	}
	if s.backdrop == "none" {
		// The card itself is the whole image, on its own corners.
		return ui.Column(c.Key("share-card")).Width(l.width).Shrink(0).Children(card)
	}
	ground := ui.Column(c.Key("share-card")).Width(l.width + 2*mat).Shrink(0).Padding(mat)
	backdrop := s.backdrop
	ground.Draw(func(pt *ui.Painter, r ui.Rect) { paintShareBackdrop(pt, r, backdrop) })
	ground.Children(func() {
		card()
		if !s.signOffOnMat() {
			return
		}
		ink := ui.RGBA(255, 255, 255, 0.85)
		if backdrop == "welcome" {
			ink = ui.RGBA(32, 66, 76, 0.78)
		}
		ui.Row(c.Key("sign-off")).FillWidth().Margin(l.signature, 0, 0, 0).Justify(ui.Center).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Image(c, bootMarkBitmap).Size(16, 16)
			ui.Text(c, "lody.ai").FontSize(12).FontWeight(500).LineHeight(1.5).LetterSpacing(0.3).TextColor(ink)
		})
	})
	return ground
}

// shareCaption is the provenance band at the card's foot, camera-style.
func shareCaption(c *ui.Context, s shareCardSpec, l shareLayout, cp *palette) {
	var params []string
	for _, v := range s.meta.params {
		if strings.TrimSpace(v) != "" {
			params = append(params, v)
		}
	}
	name := firstNonEmpty(s.meta.name, "Lody")
	ui.Row(c.Key("caption")).FillWidth().AlignItems(ui.Center).Gap(10).Padding(l.captionPad, l.gutter).
		BorderWidth(1, 0, 0, 0).BorderColor(cp.Border.Alpha(0.7)).Children(func() {
		ui.Box(c).Size(20, 20).Shrink(0).Center().Children(func() {
			if s.meta.icon != nil {
				ui.Icon(c, s.meta.icon).Size(20, 20).TextColor(cp.Text)
			} else {
				ui.Image(c, bootMarkBitmap).Size(20, 20)
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, name).FontSize(13).FontWeight(600).LineHeight(1.5).TextColor(cp.Text).SingleLine()
			if !s.signOffOnMat() {
				// No ground to sign on: the caption signs the card.
				ui.Text(c, "lody.ai").FontSize(10.5).LineHeight(1.25).TextColor(cp.Secondary).SingleLine().Margin(2, 0, 0, 0)
			}
		})
		if len(params) == 0 && s.meta.date == "" {
			return
		}
		ui.Column(c).MinWidth(0).MaxWidth((l.width - 2*l.gutter) * 0.62).AlignItems(ui.End).Children(func() {
			if len(params) > 0 {
				ui.Text(c, strings.Join(params, " · ")).FontSize(12).FontWeight(500).LineHeight(1.5).TextColor(cp.Text).SingleLine()
			}
			if s.meta.date != "" {
				ui.Text(c, s.meta.date).FontSize(10.5).LineHeight(1.25).TextColor(cp.Secondary).SingleLine().Margin(2, 0, 0, 0)
			}
		})
	})
}

// shareMarkdown is the reply's Markdown in the card's palette: the app's
// conversation look at the card's fixed size, code wrapped, its language
// kept, no copy control.
func shareMarkdown(dark bool) *markdown.Options {
	cp := sharePalette(dark)
	base := ui.LightTheme()
	if dark {
		base = ui.DarkTheme()
	}
	base.Font, base.FontSize = fontUI, shareBodySize
	t := markdown.DefaultTheme(base)
	t.FontSize = shareBodySize
	t.Font = fontUI
	t.CodeFont = fontMono
	t.LineHeight = 1.55
	t.Text = cp.Reading
	t.Strong = cp.Strong
	t.Muted = cp.Secondary
	t.Link = cp.Link
	t.Code = cp.Text
	t.CodeBackground = cp.InlineCode
	t.CodeBlockBackground = cp.CodeBg
	t.CodeBlockBorder = cp.CodeBg
	t.CodeBlockRadius = 8
	t.Rule = cp.Border
	t.QuoteBar = cp.Border
	t.QuoteText = cp.Secondary
	md := &markdown.Options{Theme: t, WrapCode: true}
	// An image has no scrollbar and nothing in it can be pressed: the
	// code wraps, and its block keeps the language but not the button.
	md.CodeBlock = func(c *ui.Context, b *markdown.CodeBlockView) {
		ui.Column(c).Direction(ui.LeftToRight).Background(t.CodeBlockBackground).Border(1, t.CodeBlockBorder).Radius(t.CodeBlockRadius).
			AlignSelf(ui.Stretch).Clip().Children(func() {
			// Lody's toolbar row, its actions hidden: the language in the
			// mono caption, over a hairline.
			ui.Row(c).FillWidth().MinHeight(28).Padding(0, 12).AlignItems(ui.Center).BorderWidth(0, 0, 0.5, 0).BorderColor(t.Rule).Children(func() {
				if b.Language != "" {
					ui.Text(c, b.Language).Font(fontMono).FontSize(11).LetterSpacing(0.22).TextColor(t.Code.Alpha(0.55)).SingleLine()
				}
			})
			// A labelled block starts its first line 1.5rem lower, as the
			// card's code style has it.
			body := ui.Column(c).FillWidth()
			if b.Language != "" {
				body.Padding(24, 0, 0, 0)
			}
			body.Children(func() { b.Body(c) })
		})
	}
	return md
}

// paintShareBackdrop paints a ground into r: its gradients, and the
// shallow-water field's lines over them.
func paintShareBackdrop(pt *ui.Painter, r ui.Rect, backdrop string) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	if bmp := shareBackdropBitmap(backdrop, r.W, r.H); bmp != nil {
		pt.Image(bmp, r, ui.FillBox)
	}
	if backdrop == "welcome" {
		// linear-gradient(90deg, rgba(25,58,68,.14) 1px, transparent 1px)
		// at 88px: a hairline every 88px from the left edge.
		line := ui.RGBA(25, 58, 68, 0.14)
		for x := float32(0); x < r.W; x += 88 {
			pt.Fill(ui.Rect{X: r.X + x, Y: r.Y, W: 1, H: r.H}, line, 0)
		}
	}
}

// The grounds as CSS writes them, layers top first, evaluated here
// rather than sampled: radial gradients, which the painter lacks, and
// linear ones, in premultiplied sRGB as browsers blend them.

type gradStop struct {
	c  [4]float64 // premultiplied, 0 to 1
	at float64
}

func rgba(r, g, b uint8, a float64) [4]float64 {
	return [4]float64{float64(r) / 255 * a, float64(g) / 255 * a, float64(b) / 255 * a, a}
}

func hexStop(h string, at float64) gradStop {
	c := ui.Hex(h)
	return gradStop{rgba(c.R, c.G, c.B, 1), at}
}

var clear4 = [4]float64{}

type gradLayer struct {
	radial bool
	angle  float64 // linear: degrees clockwise from up
	// radial: center and radii as fractions of the box; farthest makes
	// the ellipse CSS's default, through the farthest corner.
	cx, cy, rx, ry float64
	farthest       bool
	stops          []gradStop
}

func (l gradLayer) at(x, y, w, h float64) [4]float64 {
	var t float64
	if l.radial {
		px, py := l.cx*w, l.cy*h
		rx, ry := l.rx*w, l.ry*h
		if l.farthest {
			rx = math.Max(px, w-px) * math.Sqrt2
			ry = math.Max(py, h-py) * math.Sqrt2
		}
		dx, dy := (x-px)/rx, (y-py)/ry
		t = math.Sqrt(dx*dx + dy*dy)
	} else {
		a := l.angle * math.Pi / 180
		dx, dy := math.Sin(a), -math.Cos(a)
		length := math.Abs(w*dx) + math.Abs(h*dy)
		t = ((x-w/2)*dx+(y-h/2)*dy)/length + 0.5
	}
	s := l.stops
	if t <= s[0].at {
		return s[0].c
	}
	for i := 1; i < len(s); i++ {
		if t <= s[i].at {
			k := (t - s[i-1].at) / (s[i].at - s[i-1].at)
			var out [4]float64
			for j := range out {
				out[j] = s[i-1].c[j] + (s[i].c[j]-s[i-1].c[j])*k
			}
			return out
		}
	}
	return s[len(s)-1].c
}

var shareGrounds = map[string][]gradLayer{
	// Signature: deep-sea night with teal and blue blooms from the mark's
	// palette, a horizon glow, and a vignette to keep the edges quiet.
	"lody": {
		{radial: true, rx: 0.52, ry: 0.38, cx: 0.18, cy: 0.12, stops: []gradStop{{rgba(53, 200, 176, 0.45), 0}, {clear4, 0.7}}},
		{radial: true, rx: 0.48, ry: 0.36, cx: 0.86, cy: 0.16, stops: []gradStop{{rgba(47, 119, 191, 0.5), 0}, {clear4, 0.7}}},
		{radial: true, rx: 0.70, ry: 0.55, cx: 0.68, cy: 0.96, stops: []gradStop{{rgba(31, 79, 127, 0.65), 0}, {clear4, 0.75}}},
		{radial: true, rx: 1.20, ry: 1.00, cx: 0.5, cy: 0.5, stops: []gradStop{{clear4, 0.55}, {rgba(2, 10, 18, 0.55), 1}}},
		{angle: 165, stops: []gradStop{hexStop("#0a1c2b", 0), hexStop("#0c2438", 0.55), hexStop("#081626", 1)}},
	},
	// The opening ceremony's shallow-water field, the one light ground;
	// its lines paint over this (paintShareBackdrop).
	"welcome": {
		{radial: true, farthest: true, cx: 0.18, cy: 0.18, stops: []gradStop{{rgba(255, 255, 255, 0.58), 0}, {clear4, 0.46}}},
		{radial: true, farthest: true, cx: 0.82, cy: 0.72, stops: []gradStop{{rgba(42, 93, 111, 0.13), 0}, {clear4, 0.56}}},
		{angle: 180, stops: []gradStop{{rgba(255, 255, 255, 0.2), 0}, {rgba(33, 68, 79, 0.06), 1}}},
		{angle: 0, stops: []gradStop{hexStop("#dce5e7", 0), hexStop("#dce5e7", 1)}},
	},
	"aurora": {{angle: 135, stops: []gradStop{hexStop("#4f46e5", 0), hexStop("#7c3aed", 0.45), hexStop("#db2777", 1)}}},
	"ocean":  {{angle: 135, stops: []gradStop{hexStop("#0369a1", 0), hexStop("#0891b2", 0.5), hexStop("#34d399", 1)}}},
	"sunset": {{angle: 135, stops: []gradStop{hexStop("#9a3412", 0), hexStop("#ea580c", 0.45), hexStop("#f59e0b", 1)}}},
}

// shareGroundCache keeps the grounds drawn, by kind and size.
var shareGroundCache = map[string]*ui.Bitmap{}

// shareBackdropBitmap is the ground for a w×h box. Its gradients are all
// smooth, and scale with the box, so it is drawn small and stretched: at
// most 160 pixels across, as tall as the box's shape says.
func shareBackdropBitmap(backdrop string, w, h float32) *ui.Bitmap {
	layers := shareGrounds[backdrop]
	if len(layers) == 0 {
		return nil
	}
	pw := int(math.Min(160, math.Max(8, float64(w)*2)))
	ph := int(math.Max(1, math.Min(4096, math.Round(float64(pw)*float64(h)/float64(w)))))
	key := fmt.Sprint(backdrop, pw, "x", ph)
	if b := shareGroundCache[key]; b != nil {
		return b
	}
	if len(shareGroundCache) > 48 {
		clear(shareGroundCache)
	}
	img := shareGroundImage(layers, pw, ph, float64(w), float64(h))
	b := ui.NewBitmap(img)
	shareGroundCache[key] = b
	return b
}

// shareGroundImage evaluates the layers over a pw×ph image of a w×h box.
func shareGroundImage(layers []gradLayer, pw, ph int, w, h float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, pw, ph))
	for py := 0; py < ph; py++ {
		y := (float64(py) + 0.5) * h / float64(ph)
		for px := 0; px < pw; px++ {
			x := (float64(px) + 0.5) * w / float64(pw)
			var out [4]float64
			// Bottom layer first; each over what is under it.
			for i := len(layers) - 1; i >= 0; i-- {
				src := layers[i].at(x, y, w, h)
				for j := range out {
					out[j] = src[j] + out[j]*(1-src[3])
				}
			}
			img.SetRGBA(px, py, color.RGBA{q8(out[0]), q8(out[1]), q8(out[2]), q8(out[3])})
		}
	}
	return img
}

func q8(v float64) uint8 { return uint8(math.Round(math.Max(0, math.Min(1, v)) * 255)) }

// shareDate is the capture date as the card prints it: YYYY-MM-DD HH:mm,
// whatever the language.
func shareDate(iso string) string {
	if iso == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

// renderShareImage draws the card and its ground at twice its size, as
// Lody captures it, whatever the preview shows; the image is as tall as
// the card is, and transparent past its corners without a ground.
func renderShareImage(s shareCardSpec) (img *image.RGBA) {
	// A capture that fails reports so, for retry; it never takes the app.
	defer func() {
		if recover() != nil {
			img = nil
		}
	}()
	w := s.groundWidth()
	var h float32
	view := func(c *ui.Context) {
		t := *c.Theme()
		t.Background = ui.RGBA(0, 0, 0, 0)
		t.Font, t.FontSize = fontUI, sizeBody
		c.SetTheme(&t)
		ui.Column(c.Key("root")).Fill().AlignItems(ui.Start).Children(func() {
			h = shareCard(c, s).LayoutBounds().H
		})
	}
	tt := ui.NewTester(view, int(math.Ceil(float64(w))), 600)
	tt.SetDark(s.dark)
	tt.Frame()
	tt.Frame()
	height := int(math.Ceil(float64(h)))
	if height <= 0 {
		return nil
	}
	// Twice the pixels, unless that would pass 64 megapixels.
	scale := float32(2)
	if px := float64(w) * float64(height) * 4; px > 64e6 {
		scale = float32(max(1, math.Sqrt(64e6/(float64(w)*float64(height)))))
	}
	tt.SetSize(int(math.Ceil(float64(w))), height)
	tt.SetScale(scale)
	tt.Frame()
	return tt.Image()
}
