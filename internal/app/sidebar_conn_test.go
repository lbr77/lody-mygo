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
