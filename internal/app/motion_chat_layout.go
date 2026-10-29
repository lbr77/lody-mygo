package app

import (
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// The motion of the session's side surfaces, as Lody's: the file viewer's
// saving mark, its disclosures' chevrons, and the cards that fold.

// savingDot is the 6px mark of a file being saved: it breathes to half and
// back every 2s on CSS's ease-in-out, as Lody's tab bar's savingPulse.
func savingDot(c *ui.Context, p *palette) ui.Element {
	d := ui.Box(c.Key("saving")).Size(6, 6).Radius(3).Background(p.Primary)
	k := d.Keyframes("pulse", []ui.Keyframe{{Value: 1}, {At: time.Second, Value: 0.5, Ease: cssEaseInOut}, {At: 2 * time.Second, Value: 1, Ease: cssEaseInOut}},
		ui.KeyframeOptions{Repeat: ui.RepeatForever})
	return tip(c, p, d.Opacity(k), locales.WorktreeSaving())
}

// turnChevron turns a disclosure's chevron to deg as it opens, back to
// none as it closes, over d on ease: el is the chevron.
func turnChevron(el ui.Element, deg float32, open bool, d time.Duration, ease ui.Easing) ui.Element {
	return el.Rotate(el.AnimateWith("turn", deg*b2f(open), d, ease))
}

// foldOpen grows fn into view below a header as open turns true and
// shrinks it away as it turns false, over d on ease: Radix's collapsible
// content, which Lody's cards animate on tw-animate's collapsible-down and
// -up (200ms, ease-out). key keeps the fold's state.
func foldOpen(c *ui.Context, key string, open bool, d time.Duration, ease ui.Easing, fn func()) {
	holder := ui.Column(c.Key("fold/" + key)).FillWidth().Shrink(0)
	k := holder.AnimateWith("open", b2f(open), d, ease)
	holder.Children(func() { unfold(c, k, open, fn) })
}

// panelLoading is a panel's wait where Lody's file viewer turns the
// Loader2 of its components (`@/ui/spinner`) beside what it waits for,
// rather than the ring of panelStatus.
func panelLoading(c *ui.Context, p *palette, title string) {
	ui.Column(c).Grow(1).Center().Padding(24).Gap(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			loader(c, p)
			ui.Text(c, title).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
		})
	})
}

// fileToolButton is a tool of Lody's file viewer's toolbar: a 24px glyph
// in the muted ink that fills and inks under the pointer over 150ms on
// CSS's ease.
func fileToolButton(c *ui.Context, p *palette, icon *ui.SVG, label string) ui.Element {
	fade := ui.ElementTransition{Colors: true, Duration: 150 * time.Millisecond, Ease: cssEase}
	b := ui.ButtonBase(c.Key("tool-"+label)).Size(24, 24).Radius(4).Center().Shrink(0).Cursor(ui.CursorPointer).Label(label).Transition(fade)
	tip(c, p, b, label)
	ink := p.Secondary
	if b.Hovered() {
		b.Background(p.Hover)
		ink = p.Text
	}
	ink = b.AnimateColor("ink", ink, ui.Animation{Duration: 150 * time.Millisecond, Ease: cssEase})
	b.Children(func() { ui.Icon(c, icon).Size(14, 14).TextColor(ink) })
	return b
}
