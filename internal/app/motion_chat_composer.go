package app

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// The motion of the composer and the chat's actions, as Lody's: menus that
// open above what opened them, a press that sinks over Tailwind's 150ms,
// and the peek that springs out of a thumbnail.

// lodyMenuAbove is lodyMenuButton for a menu Lody opens above its button
// (side "top"), its start or end aligned with the button's as alignEnd
// says, gap apart: it comes down the 4px it starts above its place, or up
// them where the window turned it below.
func lodyMenuAbove(c *ui.Context, p *palette, button ui.Element, st *menuState, alignEnd bool, gap, minW, maxW, rowH float32, fn func(m *lodyMenu)) {
	button.OnClick(func() { st.open, st.sub = !st.open, "" })
	if st.open {
		button.Background(p.Hover)
	}
	at, self := ui.AnchorTopStart, ui.AnchorBottomStart
	if alignEnd {
		at, self = ui.AnchorTopEnd, ui.AnchorBottomEnd
	}
	ui.PopoverBase(c, button, &st.open, func(panel ui.Element) {
		panel.Padding(4).MinWidth(minW).MaxWidth(maxW).Radius(14).Background(raisedGround(p)).
			Border(0.5, p.CardRim).Shadow(0, 12, 32, -6, ui.RGBA(28, 28, 28, 0.16)).
			AttachTo(button, at, self).Margin(0, 0, gap, 0)
		away := float32(-4)
		if pb, bb := panel.Bounds(), button.Bounds(); pb.H > 0 && pb.Y >= bb.Y+bb.H-1 {
			away = 4 // the window turned it below its button
		}
		panel.Transition(ui.ElementTransition{Colors: true, Duration: popTime, Ease: easeStandard, Exit: &ui.Motion{Y: away}})
		if k := panel.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: popTime, Value: 1, Ease: easeStandard}}); k < 1 {
			panel.Translate(0, away*(1-k)).Opacity(k)
		}
		m := &lodyMenu{c: c, p: p, open: &st.open, sub: &st.sub, rowH: rowH}
		panel.Children(func() { fn(m) })
	})
	if !st.open {
		st.sub = ""
	}
}

// twMotion is Tailwind's transition-all: 150ms on its default curve.
var twMotion = ui.Animation{Duration: 150 * time.Millisecond, Ease: twEase}

// twPress sinks b a pixel while it is held, over Tailwind's 150ms, as
// Lody's active:translate-y-[1px] under transition-all.
func twPress(b ui.Element) ui.Element {
	pose := ui.Pose{}
	if b.Pressed() {
		pose.Y = 1
	}
	return b.AnimatePose(pose, twMotion)
}

// peekIn grows a popover out of what opened it on Lody's peek spring,
// from a third of its size and 4px above, as the composer's image peek
// arrives; it leaves over 150ms on Tailwind's ease-out, shrinking to .92,
// for a card leaving should not bounce.
func peekIn(pop ui.Element) {
	// The first frame it shows, it starts from nothing.
	if pop.Keyframes("born", []ui.Keyframe{{Value: 0}, {At: time.Millisecond, Value: 1}}) == 0 {
		pop.Jump("peek", 0)
	}
	if k := pop.AnimateTo("peek", 1, peekSpring); k != 1 {
		s := 0.35 + 0.65*k
		pop.Scale(s, s).Translate(0, -4*(1-k)).Opacity(min(max(k, 0), 1))
	}
	pop.Transition(ui.ElementTransition{Colors: true, Duration: 150 * time.Millisecond, Ease: twEaseOut, Exit: &ui.Motion{Scale: 0.92, Y: -4}})
}
