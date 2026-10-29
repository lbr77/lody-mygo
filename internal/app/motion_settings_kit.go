package app

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// The motion the settings and the home share with Lody's: what goes as
// another comes, the clocks those exits run on, and the small controls
// whose motion is theirs alone.

// settingsMotion is what that motion keeps from frame to frame: what was
// shown, so that what goes can go as Lody's does, and since when.
type settingsMotion struct {
	// The home's board: the one built last frame ("" folded), the one
	// going, how it goes (next, previous, unfold or fold), and since when.
	board, boardFrom, boardKind string
	boardAt                     time.Time
	pill                        ui.Rect // where the line's pill is, in the line
	// The usage card's hourly range (day or week): the one built last
	// frame ("" none), the one going as the other comes, and since when.
	rng, rngFrom string
	rngAt        time.Time
}

// motionClock is how far, 0 to 1 and linearly, a motion of d begun at `at`
// has gone, after delay: 1 once done, with no start, or while the desktop
// asks for less motion. It asks for frames until it is done.
func motionClock(c *ui.Context, at time.Time, delay, d time.Duration) float32 {
	if at.IsZero() || c.Preferences().ReduceMotion {
		return 1
	}
	t := c.Now().Sub(at) - delay
	if t >= d {
		return 1
	}
	c.AnimationFrame()
	if t <= 0 {
		return 0
	}
	return float32(t) / float32(d)
}

// firstShown reports whether e shows key for the first time this frame: as
// something new appears in a view that was already there.
func firstShown(e ui.Element, key string) bool {
	return e.Keyframes("seen/"+key, []ui.Keyframe{{Value: 0}, {At: time.Nanosecond, Value: 1}}) == 0
}

// numberFlowEase is NumberFlow's default movement: a spring written as
// CSS linear(), most of the way by a third of its 900ms, settling without
// a tail.
var numberFlowEase = func() ui.Easing {
	stops := []float32{0, .005, .019, .039, .066, .096, .129, .165, .202, .24, .278, .316, .354, .39, .426, .461, .494, .526, .557, .586,
		.614, .64, .665, .689, .711, .731, .751, .769, .786, .802, .817, .831, .844, .856, .867, .877, .887, .896, .904, .912, .919,
		.925, .931, .937, .942, .947, .951, .955, .959, .962, .965, .968, .971, .973, .976, .978, .98, .981, .983, .984, .986, .987,
		.988, .989, .99, .991, .992, .992, .993, .994, .994, .995, .995, .996, .996, .9963, .9967, .9969, .9972, .9975, .9977, .9979,
		.9981, .9982, .9984, .9985, .9987, .9988, .9989, 1}
	return func(t float32) float32 {
		if t <= 0 {
			return 0
		}
		if t >= 1 {
			return 1
		}
		at := t * float32(len(stops)-1)
		i := int(at)
		return stops[i] + (stops[i+1]-stops[i])*(at-float32(i))
	}
}()

// cssBrightness is CSS's brightness() filter: each channel scaled by f.
func cssBrightness(c ui.Color, f float32) ui.Color {
	ch := func(v uint8) uint8 { return uint8(min(255, float32(v)*f+0.5)) }
	return ui.RGBA(ch(c.R), ch(c.G), ch(c.B), float32(c.A)/255)
}

// lineTab is one of a line tab bar's tabs.
type lineTab struct {
	id, label string
	leading   func() // before the label: a status dot
}

// lineTabs is Lody's SettingsLineTabs: labels in a row 20 apart, 36 high,
// their ink fading over 150ms on CSS's ease, and one 2px line under the
// current tab that travels to the next over 240ms rather than blinking.
// ruled draws the hairline the line runs along, under a page's title.
func lineTabs(c *ui.Context, p *palette, key string, tabs []lineTab, current string, ruled bool, pick func(string)) {
	row := ui.Row(c.Key(key)).FillWidth().Gap(20)
	if ruled {
		row.BorderWidth(0, 0, 1, 0).BorderColor(p.Text.Alpha(0.08))
	}
	row.Children(func() {
		var sel ui.Rect
		for _, t := range tabs {
			id := t.id
			active := id == current
			tab := ui.ButtonBase(c.Key(id)).Height(36).Gap(6).Shrink(0).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(t.label).
				OnClick(func() { pick(id) })
			if active {
				sel = tab.Bounds()
			}
			ink := tab.AnimateColor("ink", map[bool]ui.Color{true: p.Text, false: p.Secondary}[active || tab.Hovered()],
				ui.Animation{Duration: 150 * time.Millisecond, Ease: cssEase})
			tab.Children(func() {
				if t.leading != nil {
					t.leading()
				}
				ui.Text(c, t.label).FontSize(13.5).FontWeight(500).TextColor(ink).SingleLine()
			})
		}
		if origin := row.Bounds(); sel.W > 0 {
			line := ui.Box(c.Key("line")).Absolute().Bottom(0).Height(2).Radius(1).Background(p.Text)
			if ruled {
				line.Bottom(-1)
			}
			x := line.AnimateWith("x", sel.X-origin.X, 240*time.Millisecond, easeLineTab)
			w := line.AnimateWith("w", sel.W, 240*time.Millisecond, easeLineTab)
			line.Left(x).Width(w)
		}
	})
}

// trayChoice is Lody's two-way strip (QueuedMessageBehaviorControl): a
// flat tray 28 high whose options share its width, 64 at least each, and
// the choice that holds the one thing standing on it: its fill, shadow
// and ink crossfade over 120ms, and nothing slides.
func trayChoice(c *ui.Context, p *palette, key string, options []option, selected string, pick func(string)) {
	track := ui.Row(c.Key("tray-" + key)).Height(28).Padding(2).Radius(8).Background(p.Text.Alpha(0.06)).AlignSelf(ui.Start)
	track.Children(func() {
		var buttons []ui.Element
		widest := float32(0)
		for _, o := range options {
			id := o.id
			on := id == selected
			b := ui.ButtonBase(c.Key(id)).Height(24).Padding(0, 12).Radius(6).Justify(ui.Center).AlignItems(ui.Center).
				Cursor(ui.CursorPointer).Label(o.label).OnClick(func() { pick(id) })
			k := b.AnimateWith("on", b2f(on), durFast, easeStandard)
			if k > 0 {
				b.Background(raisedGround(p).Alpha(k)).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.1*k))
			}
			ink := b.AnimateColor("ink", map[bool]ui.Color{true: p.Text, false: p.Secondary}[on || b.Hovered()], ui.Animation{Duration: durFast, Ease: easeStandard})
			var label ui.Element
			b.Children(func() {
				label = ui.Text(c, o.label).FontSize(sizeSub).FontWeight(500).LetterSpacing(-0.13).TextColor(ink).SingleLine()
			})
			widest = max(widest, label.Bounds().W)
			buttons = append(buttons, b)
		}
		// One width for every option, as a grid of equal columns: the widest
		// label's, as the last frame laid it out.
		if widest > 0 {
			for _, b := range buttons {
				b.Width(max(64, widest+24))
			}
		}
	})
}

// foldChevron is a disclosure's chevron: it turns half a turn over 120ms
// as the section opens, and back as it closes.
func foldChevron(c *ui.Context, p *palette, open bool, size float32) {
	icon := ui.Icon(c.Key("chevron"), iconChevronDown).Size(size, size).Shrink(0).TextColor(p.Tertiary)
	icon.Rotate(180 * icon.AnimateWith("open", b2f(open), durFast, easeStandard))
}

// foldBody builds fn under a settings disclosure while it is open, its
// height growing out of nothing and shrinking back as k, how far it is
// open, goes, clipped and never fading: as Base UI's collapsible panel.
func foldBody(c *ui.Context, k float32, open bool, fn func()) {
	if !open && k <= 0 {
		return
	}
	clip := ui.Column(c).FillWidth().Shrink(0)
	var inner ui.Element
	clip.Children(func() {
		inner = ui.Column(c).FillWidth().Shrink(0).Children(fn)
	})
	if k < 1 {
		clip.ClipY().Height(max(0, k) * inner.Bounds().H)
	}
}
