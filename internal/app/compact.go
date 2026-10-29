package app

import (
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// A window narrower than 768 DIPs takes Lody's compact presentation: the
// sidebar leaves the window's side, and shows instead as a drawer over it,
// at most 18rem or 85% of the window wide, sliding in from the start edge
// over 220ms as its backdrop fades in over 180ms, both on ease.standard,
// and going back the same way. Entering compact hides a sidebar that was
// showing; leaving it puts the sidebar back as it was. Choosing where to go
// in the drawer closes it.

// compactWidth is the window width below which the layout is compact.
const compactWidth = 768

// compactState is the compact presentation's: whether it is on, whether
// the sidebar showed as it began, and where the drawer was opened.
type compactState struct {
	on, sidebarWas bool
	drawerAt       string
}

// syncCompact enters or leaves the compact presentation as the window's
// width says.
func (a *App) syncCompact(c *ui.Context) {
	w, _ := c.Size()
	on := w > 0 && w < compactWidth
	cs := &a.compactLayout
	switch {
	case on && !cs.on:
		cs.on, cs.sidebarWas = true, !a.sidebarHidden
		a.sidebarHidden = true
	case !on && cs.on:
		cs.on = false
		a.sidebarHidden = !cs.sidebarWas
	}
}

// compactDrawer is the sidebar as a drawer over the window, while compact.
func (a *App) compactDrawer(c *ui.Context, p *palette) {
	cs := &a.compactLayout
	if !cs.on {
		return
	}
	open := !a.sidebarHidden
	here := a.selected + "\x00" + a.page
	switch {
	case !open:
		cs.drawerAt = ""
	case cs.drawerAt == "":
		cs.drawerAt = here
	case cs.drawerAt != here:
		// Gone somewhere: the drawer closes.
		open, cs.drawerAt = false, ""
	}
	w, h := c.Size()
	dw := min(288, w*0.85)
	ui.DialogBase(c, &open, func(backdrop, panel ui.Element) {
		backdrop.Transition(ui.ElementTransition{Colors: true, Duration: durRegular, Ease: easeStandard, Enter: &ui.Motion{}, Exit: &ui.Motion{}}).
			Background(ui.RGBA(0, 0, 0, 0.28))
		panel.Absolute().Left(0).Top(0).Size(dw, h).Label(locales.SidebarNavigationMenu()).
			Transition(ui.ElementTransition{Duration: 220 * time.Millisecond, Ease: easeStandard, Exit: &ui.Motion{X: -dw}})
		if k := panel.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: 220 * time.Millisecond, Value: 1, Ease: easeStandard}}); k < 1 {
			panel.Translate(-dw*(1-k), 0)
		}
		panel.Background(p.Ground).BorderWidth(0, 1, 0, 0).BorderColor(p.Border).Shadow(0, 20, 25, -5, ui.RGBA(0, 0, 0, 0.1))
		panel.Children(func() {
			a.sidebar(c, p).Width(dw).FillHeight()
		})
	})
	if !open {
		a.sidebarHidden = true
	}
}
