package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// Each tile shrinks to the trough and swells back, between Lody's floor
// and ceiling.
func TestWorkingGridWaves(t *testing.T) {
	for _, light := range []bool{false, true} {
		lo, hi := 1.0, 0.0
		for ms := 0.0; ms < 60000; ms += 20 {
			s, o := gridTile(1, 2, ms, light)
			lo, hi = min(lo, s), max(hi, s)
			if o < gridMinOpacity-1e-9 || o > 1+1e-9 {
				t.Fatalf("opacity %v", o)
			}
		}
		floor, ceil := 0.3, 0.9
		if light {
			floor, ceil = 0.44, 0.972
		}
		if lo < floor-0.01 || lo > floor+0.06 || hi < ceil-0.01 || hi > ceil+1e-9 {
			t.Errorf("light=%v: scale %.3f…%.3f", light, lo, hi)
		}
	}
	// Neighbouring tiles sit at different points of the wave.
	a, _ := gridTile(0, 0, 0, false)
	b, _ := gridTile(2, 2, 0, false)
	if a == b {
		t.Fatal("the wave doesn't travel")
	}
}

func TestWorkingGridDraws(t *testing.T) {
	p := lightPalette
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().Center().Gap(24).Background(p.Canvas).Children(func() {
			workingGrid(c, &p, 48)
			workingGrid(c, &p, 12)
		})
	}, 200, 80)
	dir := os.Getenv("LODY_SNAPSHOTS")
	for i := range 4 {
		tt.Frame()
		if dir != "" {
			f, _ := os.Create(filepath.Join(dir, "working-grid-"+string(rune('a'+i))+".png"))
			png.Encode(f, tt.Image())
			f.Close()
		}
		tt.Advance(450 * time.Millisecond)
	}
}

func TestWorkingHandOver(t *testing.T) {
	a := fixture()
	tt := snap(t, a, "handover-working", false, nil)
	if !tt.HasText("Working") {
		t.Fatal("working mark")
	}
	// Work stops before the unread write lands: the grid holds.
	a.catalog.Sessions[0].Status = "idle"
	a.selected = "s4"
	tt.Frame()
	if m := a.marks["s1"]; m == nil || m.holdUntil.IsZero() || !m.drawn {
		t.Fatalf("no hold: %+v", m)
	}
	// The unread write lands: the done transition plays.
	a.catalog.Sessions[0].LastMessageAt, a.catalog.Sessions[0].LastReadAt = 20, 1
	tt.Frame()
	m := a.marks["s1"]
	if m.drawn || m.finishAt.IsZero() || !tt.HasText("Unread") {
		t.Fatalf("no transition: %+v %q", m, tt.Texts())
	}
	snapFrame(t, tt, "handover-collapse")
	tt.Advance(collapseTotal + 50*time.Millisecond)
	tt.Frame()
	if tt.HasText("Unread") {
		t.Fatal("the transition outlived itself")
	}
}
