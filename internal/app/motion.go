package app

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// Lody's motion, as @lody/ui's tokens set it: colors fade over 120ms,
// popups and dialogs rise 4px over 180ms on ease.standard, a disclosure
// opens over 180ms, and the sidebar slides on the iOS sheet curve. MyGo
// keeps them still where the desktop asks for less motion, fading only.
const (
	fadeTime  = durFast    // hover, press and selection colors
	popTime   = durRegular // popovers and their menus
	pageTime  = 220 * time.Millisecond
	sheetTime = durRegular // a dialog rising into place
	foldTime  = durRegular // a disclosure opening and closing
)

var (
	// easeStandard is Lody's ease.standard.
	easeStandard = easeLody
	// easeEnter decelerates hard, for what comes into view.
	easeEnter = ui.CubicBezier(0.16, 1, 0.3, 1)
	// easeSheet is iOS's sheet curve, which the sidebar slides on.
	easeSheet = ui.CubicBezier(0.32, 0.72, 0, 1)

	// pressMotion sinks a button a pixel while it is held, as Lody's.
	pressMotion = ui.Animation{Duration: fadeTime, Ease: easeStandard}
	// streamReveal paces a streaming reply.
	streamReveal = markdown.Reveal{Unit: markdown.ByCharacter, CatchUp: 160 * time.Millisecond}
	// chatGlide scrolls the conversation to what arrives.
	chatGlide = ui.Animation{Duration: 360 * time.Millisecond, Ease: ui.CubicBezier(0.33, 1, 0.68, 1)}
	// slideSpring opens and closes the sidebar, over 220ms.
	slideSpring = ui.Animation{Duration: 220 * time.Millisecond, Ease: easeSheet}
)

var (
	// colorFade fades an element's background and border as hover, press
	// and selection change them, without moving it.
	colorFade = ui.ElementTransition{Colors: true, Duration: fadeTime, Ease: easeStandard}
	// pageIn brings a page in where another was: it fades in, rising a
	// little, as the one it replaces goes at once.
	pageIn = ui.ElementTransition{Colors: true, Duration: pageTime, Ease: easeEnter, Enter: &ui.Motion{Y: 6}}
	// windowIn fades in what fills the window, as the app goes from
	// loading to signing in to the workspace.
	windowIn = ui.ElementTransition{Colors: true, Duration: pageTime, Ease: easeStandard, Enter: &ui.Motion{}}
	// backdropFade fades a dialog's backdrop in and out over 180ms; the
	// overlay root that render keeps lets it fade out.
	backdropFade = ui.ElementTransition{Colors: true, Duration: sheetTime, Ease: easeStandard, Enter: &ui.Motion{}, Exit: &ui.Motion{}}
	// popIn raises a popover 4px into place as it fades in, and lets it
	// sink and fade the same way out, as Lody's popups.
	popIn = ui.ElementTransition{Colors: true, Duration: popTime, Ease: easeStandard, Enter: &ui.Motion{Y: 4}, Exit: &ui.Motion{Y: 4}}
	// lateIn fades in what shows while waiting, as a spinner, only once
	// the wait is long enough to notice: a quick load shows none.
	lateIn = ui.ElementTransition{Colors: true, Animation: &ui.Animation{Duration: pageTime, Ease: easeStandard, Delay: 350 * time.Millisecond}, Enter: &ui.Motion{}}
)

// pressable sinks a button a pixel while the pointer holds it down, as
// Lody's buttons do, and lifts it back. scale is kept for the callers that
// asked for a scale before; Lody presses by translation alone.
func pressable(b ui.Element, scale float32) ui.Element {
	pose := ui.Pose{}
	if b.Pressed() {
		pose.Y = 1
	}
	return b.AnimatePose(pose, pressMotion)
}

// sheetIn fades a dialog's backdrop in, and out once closed, and raises
// its panel 4px into place as it fades in, as Lody's dialogs, without
// scaling: call it first in a DialogBase.
func sheetIn(backdrop, panel ui.Element) {
	backdrop.Transition(backdropFade)
	k := panel.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: sheetTime, Value: 1, Ease: easeStandard}})
	if k < 1 {
		panel.Translate(0, 4*(1-k)).Opacity(k)
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
