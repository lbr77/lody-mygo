package app

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

// Lody's tooltips open once the pointer has rested on their trigger as
// long as the surface's Tooltip.Provider says: 600ms by default, 400 in
// the sidebar, 500 over a message's actions, 300 in the archive, 250 in
// settings, 150 over a schedule. Within 400ms of one closing, the next
// opens at once, so a row of buttons reads as one strip. A press closes a
// tip until the pointer leaves; keyboard focus opens one at once.

// tipDelay is the delay of the surface being built, as Lody's provider
// sets it for what it wraps; withTipDelay scopes it.
var tipDelay = 600 * time.Millisecond

// tipWarm is how long after one tip closes the next opens at once.
const tipWarm = 400 * time.Millisecond

// withTipDelay builds fn with tips that wait d.
func withTipDelay(d time.Duration, fn func()) {
	prev := tipDelay
	tipDelay = d
	defer func() { tipDelay = prev }()
	fn()
}

// tipState is which trigger the pointer rests on, since when, and which
// tip shows: one tip at a time, as the window has one pointer.
type tipState struct {
	label     string
	since     time.Time
	shown     string
	lastShown time.Time
	dismissed string
}

var tips tipState

// show reports whether anchor's tip, named label, shows this frame,
// asking for a frame when its delay will have passed.
func (t *tipState) show(c *ui.Context, anchor ui.Element, label string) bool {
	now := c.Now()
	// A trigger is its label where it lies: two Copy buttons are two.
	b := anchor.Bounds()
	key := fmt.Sprintf("%s@%.0f,%.0f", label, b.X, b.Y)
	hovered := anchor.Hovered()
	if anchor.Pressed() {
		t.dismissed = key
	}
	if !hovered && t.dismissed == key {
		t.dismissed = ""
	}
	if anchor.FocusVisible() && t.dismissed != key {
		t.shown, t.lastShown = key, now
		return true
	}
	if !hovered || t.dismissed == key {
		if t.label == key {
			t.label = ""
		}
		if t.shown == key {
			t.shown, t.lastShown = "", now
		}
		return false
	}
	if t.label != key {
		t.label, t.since = key, now
	}
	if t.shown != key && now.Sub(t.lastShown) >= tipWarm {
		if wait := tipDelay - now.Sub(t.since); wait > 0 {
			c.After(wait)
			return false
		}
	}
	t.shown, t.lastShown = key, now
	return true
}

// hoverGrace is how long a hover popover outlasts the pointer leaving its
// trigger, so it can cross the gap to the popover.
const hoverGrace = 100 * time.Millisecond

// hoverPop is a popover that opens on hover, as Base UI's openOnHover: once
// the pointer has rested on its trigger for its delay, or at once on a
// click. Opened by hover, it closes as the pointer leaves both trigger and
// popover; opened by a click, it stays until dismissed. Closed under the
// pointer, it stays closed until the pointer leaves.
type hoverPop struct {
	open, clicked bool
	shown         bool // open as the last frame ended
	held          bool // closed under the pointer
	since, left   time.Time
	overPanel     bool // the pointer was on the popover last frame
}

// update opens or closes the popover for this frame, asking for a frame
// when its delay or grace will have run out.
func (h *hoverPop) update(c *ui.Context, trigger ui.Element, delay time.Duration) {
	defer func() { h.shown = h.open }()
	now := c.Now()
	hovered := trigger.Hovered()
	if h.shown && !h.open {
		// Dismissed: Escape, or a click away.
		h.held = hovered
	}
	if trigger.Clicked() {
		if h.open && h.clicked {
			h.open, h.held = false, true
		} else {
			h.open, h.clicked = true, true
		}
	}
	if !hovered {
		h.since, h.held = time.Time{}, false
	} else if h.since.IsZero() {
		h.since = now
	}
	if !h.open && hovered && !h.held {
		if wait := delay - now.Sub(h.since); wait > 0 {
			c.After(wait)
		} else {
			h.open, h.clicked = true, false
		}
	}
	if !h.open {
		h.clicked, h.overPanel, h.left = false, false, time.Time{}
		return
	}
	if h.clicked || hovered || h.overPanel {
		h.left = time.Time{}
		return
	}
	if h.left.IsZero() {
		h.left = now
	}
	if wait := hoverGrace - now.Sub(h.left); wait > 0 {
		c.After(wait)
		return
	}
	h.open, h.left = false, time.Time{}
}
