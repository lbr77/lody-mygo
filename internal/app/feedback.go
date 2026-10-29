package app

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// Feedback, as Lody's @lody/ui draws it: toasts arriving at the top of the
// window, skeletons breathing where content is on its way, and a copy
// button that says it copied.

// Lody's motion tokens.
const (
	durFast    = 120 * time.Millisecond
	durRegular = 180 * time.Millisecond
	durSlow    = 320 * time.Millisecond
)

// easeLody is Lody's ease.standard.
var easeLody = ui.CubicBezier(0.2, 0, 0, 1)

// ringed is one of the marks a message wears: a ring with a stroke in it.
func ringed(d string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><circle cx="8" cy="8" r="6.2"/><path d="` + d + `"/></svg>`))
}

var (
	markInfo    = ringed("M8 7.2v4M8 4.8h.01")
	markSuccess = ringed("M5.2 8.2 7.2 10.2 10.8 6")
	markDanger  = ringed("M8 4.8v4M8 11.2h.01")
	markWarning = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M8 1.8 15 13.8H1L8 1.8Z"/><path d="M8 6.4v3.2M8 11.8h.01"/></svg>`))
)

// toast reports something over the page: tone is "success", "warning",
// "danger" or "" for neutral. It shows with the next frame.
func (a *App) toast(tone, title string) {
	a.toasts = append(a.toasts, ui.Toast{Title: title, Type: tone})
}

// toastViewport shows the toasts: one column at the top of the window,
// 360 wide, each arriving from 4px above as it fades in.
func (a *App) toastViewport(c *ui.Context, p *palette) {
	for _, t := range a.toasts {
		c.AddToast(t)
	}
	a.toasts = nil
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Padding(16).AlignItems(ui.Center).Justify(ui.Start).Gap(8)
		for _, t := range toasts {
			mark, ink := markInfo, p.Secondary
			switch t.Type {
			case "success":
				mark, ink = markSuccess, p.Success
			case "warning":
				mark, ink = markWarning, p.Warning
			case "danger":
				mark, ink = markDanger, p.Danger
			}
			tt := ui.ToastBase(c, t)
			tt.Root.Row().Width(360).Gap(12).Padding(12).AlignItems(ui.Start).Radius(12).Background(p.Popover).
				Border(0.5, p.CardRim).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.14)).
				Transition(ui.ElementTransition{Duration: durRegular, Ease: easeLody, Enter: &ui.Motion{Y: -4}, Exit: &ui.Motion{Y: -4}})
			tt.Root.Children(func() {
				ui.Icon(c, mark).Size(16, 16).TextColor(ink).Margin(1, 0, 0, 0)
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Text(c, t.Title).FontSize(sizeSub).LineHeight(18.0 / float32(sizeSub)).FontWeight(500).TextColor(p.Text)
					if t.Description != "" {
						ui.Text(c, t.Description).FontSize(sizeFoot).TextColor(p.Secondary)
					}
					if t.Action != "" {
						act := tt.ActionButton().Label(t.Action).Height(28).Padding(0, 10).Radius(8).Margin(4, 0, 0, 0).AlignSelf(ui.Start).
							AlignItems(ui.Center).Border(1, p.Border).Background(p.Canvas).Cursor(ui.CursorPointer).Transition(colorFade)
						pressable(act, 0.97)
						if act.Hovered() {
							act.Background(p.Hover)
						}
						act.Children(func() { ui.Text(c, t.Action).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text) })
					}
				})
				tt.CloseButton().Label(locales.Close()).Size(28, 28).Margin(-5, -4, -5, 0).Radius(8).Justify(ui.Center).AlignItems(ui.Center).
					Cursor(ui.CursorPointer).Children(func() {
					ui.Icon(c, iconX).Size(14, 14).TextColor(p.Secondary)
				})
			})
		}
	})
}

// skeleton holds the room of something on its way, breathing from full to
// .55 and back every 1.6s.
func skeleton(c *ui.Context, p *palette, key string, height float32) ui.Element {
	s := ui.Column(c.Key("skeleton-" + key)).Height(height).Radius(4).Background(p.Text.Alpha(0.08))
	k := s.Keyframes("pulse", []ui.Keyframe{{Value: 1}, {At: 800 * time.Millisecond, Value: 0.55, Ease: easeLody}, {At: 1600 * time.Millisecond, Value: 1, Ease: easeLody}},
		ui.KeyframeOptions{Repeat: ui.RepeatForever})
	return s.Opacity(k)
}

// copyButton copies text and says so for 2s, its icon swapping in from
// smaller as Lody's copy buttons do.
func copyButton(c *ui.Context, p *palette, key string, copiedAt *time.Time, label, done string, copy func()) ui.Element {
	copied := !copiedAt.IsZero() && time.Since(*copiedAt) < 2*time.Second
	if copied {
		c.After(2*time.Second - time.Since(*copiedAt))
	}
	text := label
	if copied {
		text = done
	}
	b := ui.ButtonBase(c.Key("copy-"+key)).Height(28).Padding(0, 10).Radius(8).Gap(6).AlignItems(ui.Center).Border(1, p.Border).
		Background(p.Canvas).Cursor(ui.CursorPointer).Label(text).Transition(colorFade)
	pressable(b, 0.97)
	if b.Hovered() {
		b.Background(p.Hover)
	}
	b.OnClick(func() {
		copy()
		*copiedAt = time.Now()
	})
	b.Children(func() {
		ui.Box(c).Size(14, 14).Children(func() {
			icon, k := iconCopy, "copy"
			if copied {
				icon, k = iconCheck, "check"
			}
			ui.Icon(c.Key(k), icon).Size(14, 14).TextColor(p.Text).Absolute().
				Transition(ui.ElementTransition{Duration: 200 * time.Millisecond, Ease: easeLody, Enter: &ui.Motion{Scale: 0.75}, Exit: &ui.Motion{Scale: 0.75}})
		})
		ui.Text(c, text).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text)
	})
	return b
}

// segmented is Lody's small tab list: the selected tab's pill slides and
// stretches to the tab chosen over 180ms, the labels' ink fading over
// 120ms. An invisible copy of the tabs sizes the track; the pill, then
// the tabs, lie over it, as what is placed absolutely lies above what
// flows.
func segmented(c *ui.Context, p *palette, key string, options []option, selected string, pick func(string)) {
	tabLook := func(e ui.Element) ui.Element {
		return e.Height(24).Padding(0, 10).Radius(6).Gap(6).AlignItems(ui.Center)
	}
	content := func(label string, ink ui.Color) {
		ui.Icon(c, iconTerminal).Size(13, 13).TextColor(ink)
		ui.Text(c, label).FontSize(sizeFoot).FontWeight(500).TextColor(ink)
	}
	track := ui.Row(c.Key("seg-" + key)).Padding(2).Radius(8).Background(p.Text.Alpha(0.05)).AlignSelf(ui.Start)
	track.Children(func() {
		ui.Row(c).Gap(2).Opacity(0).Children(func() {
			for _, o := range options {
				tabLook(ui.Row(c.Key(o.id))).Children(func() { content(o.label, p.Text) })
			}
		})
		pill := ui.Column(c.Key("pill")).Absolute().Top(2).Height(24).Radius(6).Background(p.Popover).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.08))
		var first, sel ui.Rect
		ui.Row(c.Key("tabs")).Absolute().Top(2).Left(2).Gap(2).Children(func() {
			for i, o := range options {
				id := o.id
				tab := tabLook(ui.ButtonBase(c.Key(o.id))).Cursor(ui.CursorPointer).Label(o.label).OnClick(func() { pick(id) })
				b := tab.Bounds()
				if i == 0 {
					first = b
				}
				if o.id == selected {
					sel = b
				}
				ink := tab.AnimateColor("ink", map[bool]ui.Color{true: p.Text, false: p.Secondary}[o.id == selected], ui.Animation{Duration: durFast, Ease: easeLody})
				tab.Children(func() { content(o.label, ink) })
			}
		})
		if sel.W == 0 {
			pill.Width(0).Opacity(0)
			return
		}
		x := pill.AnimateWith("x", sel.X-first.X, durRegular, easeLody)
		w := pill.AnimateWith("w", sel.W, durRegular, easeLody)
		pill.Left(2 + x).Width(w)
	})
}

// spinnerInk is the ink spinners turn in, the frame's secondary text: as
// Lody's take currentColor, mostly a label's.
var spinnerInk = ui.RGBA(0, 0, 0, 0.5)

// spinner is Lody's: a faint ring, and a quarter of it turning once every
// 900ms. Size it as any element; it is 16 across unless told.
func spinner(c *ui.Context) ui.Element {
	s := ui.Box(c.Key("spinner")).Size(16, 16).Shrink(0).Label(locales.Loading())
	k := s.Keyframes("turn", []ui.Keyframe{{Value: 0}, {At: 900 * time.Millisecond, Value: 1}}, ui.KeyframeOptions{Repeat: ui.RepeatForever})
	ink := spinnerInk
	return s.Draw(func(pt *ui.Painter, r ui.Rect) {
		u := min(r.W, r.H) / 16
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		arc := func(from, sweep float64) [][2]float32 {
			n := max(4, int(64*sweep))
			pts := make([][2]float32, 0, n+1)
			for i := 0; i <= n; i++ {
				a := 2 * math.Pi * (from + sweep*float64(i)/float64(n))
				pts = append(pts, [2]float32{cx + 6.5*u*float32(math.Cos(a)), cy + 6.5*u*float32(math.Sin(a))})
			}
			return pts
		}
		width := max(1, 1.6*u)
		pt.StrokePolyline(arc(0, 1), width, ink.Alpha(0.25*float32(ink.A)/255))
		// The quarter from the top round to the right, as the path draws it.
		pt.StrokePolyline(arc(-0.25+float64(k), 0.25), width, ink)
	})
}
