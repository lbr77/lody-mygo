package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The panel beside the conversation takes 40% at first; its edge drags
// as Lody's resizable panel, never under 280 DIPs, and folds the panel
// away when dragged under half that. Its share is kept.
func TestPanelResize(t *testing.T) {
	t.Setenv("LODY_DATA_DIR", t.TempDir())
	a := fixture()
	a.view.Working = false
	a.panelOpen = true
	tt := ui.NewTester(a.render, 1300, 720)
	tt.Frame()
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	r, ok := tt.Find("Resize panel")
	if !ok {
		t.Fatalf("no handle: %q", tt.Texts())
	}
	edge := r.X + r.W/2
	if a.panelPercent != 60 {
		t.Fatalf("panel starts at %v%%", 100-a.panelPercent)
	}
	drag := func(from, to float32) {
		tt.Move(from, 300)
		tt.Frame()
		tt.Press(from, 300)
		for x := from; x != to; {
			step := max(-20, min(20, to-x))
			x += step
			tt.Move(x, 300)
			tt.Frame()
		}
		tt.Release(to, 300)
		tt.Frame()
	}
	// Wider, to at most half the card.
	drag(edge, edge-100)
	if a.panelPercent >= 60 || a.panelPercent < 50 {
		t.Fatalf("dragged wider: the conversation keeps %v%%", a.panelPercent)
	}
	if p := loadPrefs(); p.Panel != a.panelPercent {
		t.Fatalf("not kept: %v, saved %v", a.panelPercent, p.Panel)
	}
	// Narrower: it stops at 280 DIPs.
	tt.Advance(time.Second)
	tt.Frame()
	r, _ = tt.Find("Resize panel")
	edge = r.X + r.W/2
	drag(edge, edge+60)
	tt.Frame()
	r2, _ := tt.Find("Resize panel")
	if w := (r2.X + r2.W/2); w <= edge {
		t.Fatalf("didn't move narrower: %v from %v", w, edge)
	}
	// Under half its least, it folds away.
	drag(r2.X+r2.W/2, r2.X+r2.W/2+900)
	if a.panelOpen {
		t.Fatal("dragged away but still open")
	}
}
