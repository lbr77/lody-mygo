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
	// easeLineTab moves a line tab's underline.
	easeLineTab = ui.CubicBezier(0.2, 0.8, 0.2, 1)
	// easeSweep is the curve Lody's usage charts sweep in on.
	easeSweep = ui.CubicBezier(0.22, 1, 0.36, 1)
	// peekSpring grows the composer's image peek out of its thumbnail.
	peekSpring = ui.Animation{Spring: ui.SpringOptions{Stiffness: 420, Damping: 28, Mass: 1}}
	// streamReveal paces a streaming reply.
	streamReveal = markdown.Reveal{Unit: markdown.ByCharacter, CatchUp: 160 * time.Millisecond}
	// chatGlide scrolls the conversation to what arrives.
	chatGlide = ui.Animation{Duration: 360 * time.Millisecond, Ease: ui.CubicBezier(0.33, 1, 0.68, 1)}
	// slideSpring opens and closes the sidebar, over 220ms.
	slideSpring = ui.Animation{Duration: 220 * time.Millisecond, Ease: easeSheet}
)

// The curves Lody's CSS names, which MyGo's own EaseOut and EaseInOut
// (cubic) are not.
var (
	// cssEase is CSS's ease, and framer-motion's default.
	cssEase = ui.CubicBezier(0.25, 0.1, 0.25, 1)
	// cssEaseOut is CSS's and framer-motion's easeOut.
	cssEaseOut = ui.CubicBezier(0, 0, 0.58, 1)
	// cssEaseInOut is CSS's ease-in-out and framer-motion's easeInOut.
	cssEaseInOut = ui.CubicBezier(0.42, 0, 0.58, 1)
	// twEaseOut is Tailwind's ease-out.
	twEaseOut = ui.CubicBezier(0, 0, 0.2, 1)
	// twEase is Tailwind's default timing, for transition-colors and the
	// like without an ease of their own.
	twEase = ui.CubicBezier(0.4, 0, 0.2, 1)
	// twFade is Tailwind's transition-colors: 150ms on its default curve,
	// where Lody's components, not its @lody/ui primitives, fade a fill.
	twFade = ui.ElementTransition{Colors: true, Duration: 150 * time.Millisecond, Ease: twEase}
)

var (
	// tabFade fades a tab's colors over 150ms, as Lody's tab bars'
	// Tailwind transition-colors.
	tabFade = ui.ElementTransition{Colors: true, Duration: 150 * time.Millisecond, Ease: ui.CubicBezier(0.4, 0, 0.2, 1)}
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
	// popAbove is popIn for a popup above what opened it: it starts 4px
	// higher and comes down into place.
	popAbove = ui.ElementTransition{Colors: true, Duration: popTime, Ease: easeStandard, Enter: &ui.Motion{Y: -4}, Exit: &ui.Motion{Y: -4}}
	// sheetOut sinks a dialog's panel 4px as it fades out.
	sheetOut = ui.ElementTransition{Colors: true, Duration: sheetTime, Ease: easeStandard, Exit: &ui.Motion{Y: 4}}
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
	// Closing, the panel sinks back the 4px it rose, as it fades.
	panel.Transition(sheetOut)
	k := panel.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: sheetTime, Value: 1, Ease: easeStandard}})
	if k < 1 {
		panel.Translate(0, 4*(1-k)).Opacity(k)
	}
}

// popup is a Popover that rises in and sinks out, as Lody's: it starts
// 4px further from its anchor on the side it landed, below or above, and
// closes that step as it arrives.
func popup(c *ui.Context, anchor ui.Element, open *bool, fn func()) ui.Element {
	pop := ui.Popover(c, anchor, open, fn)
	away := float32(4)
	if pb, ab := pop.Bounds(), anchor.Bounds(); pb.H > 0 && pb.Y+pb.H <= ab.Y+1 {
		away = -4 // it opened above its anchor
	}
	pop.Transition(ui.ElementTransition{Colors: true, Duration: popTime, Ease: easeStandard, Exit: &ui.Motion{Y: away}})
	if k := pop.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: popTime, Value: 1, Ease: easeStandard}}); k < 1 {
		pop.Translate(0, away*(1-k)).Opacity(k)
	}
	return pop
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
