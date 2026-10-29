package app

import (
	"testing"

	"github.com/lbr77/lody-mygo/internal/catalog"
)

// The sidebar's workspace badge says how the connection stands, as Lody's:
// connecting, reconnecting after a drop, then syncing; nothing once synced.
func TestConnectionBadge(t *testing.T) {
	a := fixture()
	a.sync = &catalog.Sync{}
	for _, tc := range []struct {
		state  catalog.State
		synced bool
		want   string
	}{
		{catalog.State{}, false, "Connecting…"},
		{catalog.State{Reason: "dial failed"}, false, "Reconnecting…"},
		{catalog.State{Live: true}, false, "Syncing workspace…"},
		{catalog.State{Live: true}, true, ""},
	} {
		a.syncState, a.synced = tc.state, tc.synced
		tt := snap(t, a, "conn-badge", false, nil)
		for _, label := range []string{"Connecting…", "Reconnecting…", "Syncing workspace…"} {
			if tt.HasText(label) != (label == tc.want) {
				t.Errorf("%+v synced %v: %q shown %v", tc.state, tc.synced, label, tt.HasText(label))
			}
		}
	}
}

// A sideways scroll over a long sidebar title moves it, as Lody's; the
// pointer leaving puts it back.
func TestTitleScrub(t *testing.T) {
	a := fixture()
	long := "A very long session title that cannot possibly fit in the sidebar row"
	a.catalog.Sessions[1].Title = long
	tt := snap(t, a, "title-scrub", false, nil)
	r, ok := tt.Find(long)
	if !ok {
		t.Fatalf("no title in %q", tt.Texts())
	}
	tt.Move(r.X+100, r.Y+r.H/2)
	tt.Frame()
	tt.Scroll(r.X+100, r.Y+r.H/2, 60, 0)
	tt.Frame()
	if a.titleSwipe.id != "s2" || a.titleSwipe.offset != -60 {
		t.Fatalf("swipe %+v", a.titleSwipe)
	}
	tt.Move(r.X+600, r.Y+300)
	tt.Frame()
	if a.titleSwipe.id != "" {
		t.Fatalf("still swiped after leaving: %+v", a.titleSwipe)
	}
}

// A press dragged across a long sidebar title moves it under the pointer,
// as Lody's, and its release doesn't open the chat; a plain press still
// does, and a drag that goes down first leaves the title be.
func TestTitleDrag(t *testing.T) {
	a := fixture()
	long := "A very long session title that cannot possibly fit in the sidebar row"
	a.catalog.Sessions[1].Title = long
	tt := snap(t, a, "title-drag", false, nil)
	r, ok := tt.Find(long)
	if !ok {
		t.Fatalf("no title in %q", tt.Texts())
	}
	x, y := r.X+100, r.Y+r.H/2
	tt.Move(x, y)
	tt.Frame()
	before := a.selected
	tt.Press(x, y)
	tt.Move(x-3, y)
	tt.Frame()
	if a.titleSwipe.id != "" {
		t.Fatalf("moved before 5px: %+v", a.titleSwipe)
	}
	tt.Move(x-40, y+2)
	tt.Frame()
	if a.titleSwipe.id != "s2" || a.titleSwipe.offset != -40 {
		t.Fatalf("drag %+v", a.titleSwipe)
	}
	tt.Move(x+500, y+2) // past its start: it stops at rest
	tt.Frame()
	if a.titleSwipe.offset != 0 {
		t.Fatalf("not clamped at 0: %+v", a.titleSwipe)
	}
	tt.Move(x-50, y)
	tt.Release(x-50, y)
	tt.Frame()
	tt.Frame()
	if a.selected != before {
		t.Fatalf("the drag's release opened %q", a.selected)
	}
	if a.titleSwipe.id != "s2" || a.titleSwipe.offset != -50 {
		t.Fatalf("title not left where it was put: %+v", a.titleSwipe)
	}

	// Going down first, it lets the title be.
	tt.Press(x, y)
	tt.Move(x-2, y+8)
	tt.Move(x-30, y+8)
	tt.Frame()
	if a.titleSwipe.offset != -50 {
		t.Fatalf("a downward drag moved the title: %+v", a.titleSwipe)
	}
	tt.Release(x-30, y+8)
	tt.Frame()

	// A plain press on the title opens the chat.
	a.selected = before
	tt.Move(x, y)
	tt.Press(x, y)
	tt.Release(x, y)
	tt.Frame()
	tt.Frame()
	if a.selected != "s2" {
		t.Fatalf("a press on the title didn't open the chat: %q", a.selected)
	}
}
