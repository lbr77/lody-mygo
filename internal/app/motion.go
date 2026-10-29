package app

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// Lody's motion: short curves that start fast and settle, as its desktop
// app's CSS transitions. MyGo keeps them still where the desktop asks for
// less motion, fading only.
const (
	fadeTime  = 150 * time.Millisecond // hover, press and selection colors
	popTime   = 160 * time.Millisecond // popovers and their menus
	pageTime  = 220 * time.Millisecond // a page or a conversation coming in
	sheetTime = 240 * time.Millisecond // a dialog rising into place
	foldTime  = 200 * time.Millisecond // a disclosure opening and closing
)

var (
	// easeStandard is Tailwind's ease-in-out, which Lody's colors fade on.
	easeStandard = ui.CubicBezier(0.4, 0, 0.2, 1)
	// easeEnter decelerates hard, for what comes into view.
	easeEnter = ui.CubicBezier(0.16, 1, 0.3, 1)

	// pressSpring sinks a button and springs it back, turning smoothly
	// when the pointer lets go before it is down.
	pressSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 220 * time.Millisecond, Bounce: 0.2}}
	// slideSpring opens and closes the sidebar.
	slideSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 320 * time.Millisecond}}
)

var (
	// colorFade fades an element's background and border as hover, press
	// and selection change them, without moving it.
	colorFade = ui.ElementTransition{Colors: true, Duration: fadeTime, Ease: easeStandard}
	// pageIn brings a page in where another was: it fades in, rising a
	// little, as the one it replaces goes at once, as MyGo's router fades
	// pages across rather than blurring the two into each other.
	pageIn = ui.ElementTransition{Colors: true, Duration: pageTime, Ease: easeEnter, Enter: &ui.Motion{Y: 6}}
	// windowIn fades in what fills the window, as the app goes from
	// loading to signing in to the workspace.
	windowIn = ui.ElementTransition{Colors: true, Duration: pageTime, Ease: easeStandard, Enter: &ui.Motion{}}
	// backdropFade fades a dialog's backdrop, and the dialog in it, in and
	// out; the overlay root that render keeps lets it fade out.
	backdropFade = ui.ElementTransition{Colors: true, Duration: popTime, Ease: easeStandard, Enter: &ui.Motion{}, Exit: &ui.Motion{}}
	// popIn grows a popover in, and fades it out: from its center, as it
	// may open below its anchor or above it.
	popIn = ui.ElementTransition{Colors: true, Duration: popTime, Ease: easeEnter, Enter: &ui.Motion{Scale: 0.97}, Exit: &ui.Motion{}}
	// lateIn fades in what shows while waiting, as a spinner, only once
	// the wait is long enough to notice: a quick load shows none.
	lateIn = ui.ElementTransition{Colors: true, Animation: &ui.Animation{Duration: pageTime, Ease: easeStandard, Delay: 350 * time.Millisecond}, Enter: &ui.Motion{}}
)

// pressable sinks a button while the pointer holds it down, by scale, and
// springs it back as the pointer lets go.
func pressable(b ui.Element, scale float32) ui.Element {
	pose := ui.Pose{}
	if b.Pressed() {
		pose.Scale = scale
	}
	return b.AnimatePose(pose, pressSpring)
}

// sheetIn fades a dialog's backdrop in, and out once closed, and raises
// its panel into place, growing a little: call it first in a DialogBase.
func sheetIn(backdrop, panel ui.Element) {
	backdrop.Transition(backdropFade)
	k := panel.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: sheetTime, Value: 1, Ease: easeEnter}})
	if k < 1 {
		s := 0.96 + 0.04*k
		panel.Translate(0, 8*(1-k)).Scale(s, s)
	}
}

// popup is a Popover that grows in and fades out.
func popup(c *ui.Context, anchor ui.Element, open *bool, fn func()) ui.Element {
	return ui.Popover(c, anchor, open, fn).Transition(popIn)
}

// unfold builds fn below a disclosure while it is open, growing it into
// view as it opens and shrinking it away as it closes, as MyGo's
// Collapsible: k is how far it is open, from disclosure.
func unfold(c *ui.Context, k float32, open bool, fn func()) {
	if !open && k == 0 {
		return
	}
	clip := ui.Column(c).FillWidth().Shrink(0)
	var inner ui.Element
	clip.Children(func() {
		inner = ui.Column(c).FillWidth().Shrink(0).Children(fn)
	})
	if k < 1 {
		// The height the content had in the last frame: none as it
		// starts to open.
		clip.ClipY().Height(k * inner.Bounds().H).Opacity(k)
	}
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
