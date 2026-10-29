package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// Probe: does a dialog's backdrop fade out with an Exit when the overlay
// root is built every frame?
func TestProbeDialogExit(t *testing.T) {
	open := false
	keep := false
	tt := ui.NewTester(func(c *ui.Context) {
		if keep {
			ui.Overlay(c, func() {})
		}
		ui.Box(c).Fill().Background(ui.RGB(255, 255, 255))
		ui.DialogBase(c, &open, func(backdrop, panel ui.Element) {
			backdrop.Background(ui.RGBA(0, 0, 0, 1)).Transition(ui.ElementTransition{Colors: true, Enter: &ui.Motion{}, Exit: &ui.Motion{}, Duration: 200 * time.Millisecond, Ease: ui.Linear})
			panel.Size(40, 40).Background(ui.RGB(255, 0, 0))
		})
	}, 200, 200)
	px := func() uint8 { return tt.Image().RGBAAt(5, 5).R }
	tt.Frame()
	t.Logf("closed: %d", px())
	open = true
	tt.Frame()
	t.Logf("open t=0: %d", px())
	tt.Advance(100 * time.Millisecond)
	t.Logf("open t=100: %d", px())
	tt.Advance(200 * time.Millisecond)
	t.Logf("open t=300: %d", px())
	open = false
	tt.Frame()
	t.Logf("closed t=0: %d", px())
	tt.Advance(100 * time.Millisecond)
	t.Logf("closed t=100: %d", px())
	tt.Advance(200 * time.Millisecond)
	t.Logf("closed t=300: %d", px())
}

// Probe: a keyed page under a persistent parent fades in.
func TestProbePageEnter(t *testing.T) {
	page := "a"
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Background(ui.RGB(255, 255, 255)).Children(func() {
			col := ui.Column(c.Key(page)).Fill().Transition(ui.ElementTransition{Colors: true, Enter: &ui.Motion{Y: 6}, Duration: 200 * time.Millisecond, Ease: ui.Linear})
			if page == "b" {
				col.Background(ui.RGB(0, 0, 0))
			}
		})
	}, 200, 200)
	px := func() uint8 { return tt.Image().RGBAAt(100, 100).R }
	tt.Frame()
	t.Logf("a: %d", px())
	page = "b"
	tt.Frame()
	t.Logf("b t=0: %d", px())
	tt.Advance(100 * time.Millisecond)
	t.Logf("b t=100: %d", px())
	tt.Advance(200 * time.Millisecond)
	t.Logf("b t=300: %d", px())
}
