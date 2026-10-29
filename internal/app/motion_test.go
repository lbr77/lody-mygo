package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// luma is how light the window is at x, y.
func luma(tt *ui.Tester, x, y int) int {
	c := tt.Image().RGBAAt(x, y)
	return int(c.R) + int(c.G) + int(c.B)
}

// Settings opens and closes at once, as Lody's (noAnimation); other
// dialogs fade their backdrop in and out.
func TestDialogMotion(t *testing.T) {
	a := fixture()
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	// A corner of the window, which only the backdrop covers.
	ground := luma(tt, 2, 710)
	a.settingsOpen = true
	tt.Frame()
	if got := luma(tt, 2, 710); got >= ground-20 {
		t.Fatalf("Settings fades in: %d, was %d", got, ground)
	}
	a.settingsOpen = false
	tt.Frame()
	if got := luma(tt, 2, 710); got < ground-2 {
		t.Fatalf("Settings fades out: %d, was %d", got, ground)
	}

	a.confirm("Delete?", "", "Delete", func() {})
	tt.Frame()
	if got := luma(tt, 2, 710); got < ground-6 {
		t.Fatalf("the backdrop dims the window at once: %d, was %d", got, ground)
	}
	tt.Advance(popTime + 50*time.Millisecond)
	dimmed := luma(tt, 2, 710)
	if dimmed >= ground-20 {
		t.Fatalf("the backdrop doesn't dim the window once in: %d, was %d", dimmed, ground)
	}
	a.confirming.open = false
	tt.Frame()
	if got := luma(tt, 2, 710); got > dimmed+6 {
		t.Fatalf("the backdrop goes at once on closing: %d, was %d", got, dimmed)
	}
	tt.Advance(popTime + 50*time.Millisecond)
	if got := luma(tt, 2, 710); got < ground-2 {
		t.Fatalf("the backdrop stays after it faded out: %d, was %d", got, ground)
	}
}

func TestSidebarSlides(t *testing.T) {
	a := fixture()
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	shown, ok := tt.Find("Archived chats")
	if !ok {
		t.Fatal("no sidebar")
	}
	a.sidebarHidden = true
	tt.Frame()
	tt.Advance(80 * time.Millisecond)
	going, ok := tt.Find("Archived chats")
	if !ok || going.X >= shown.X {
		t.Fatalf("the sidebar doesn't slide out: %v, was %v (%v)", going, shown, ok)
	}
	tt.Advance(time.Second)
	if tt.HasText("Archived chats") {
		t.Fatal("the sidebar stays once hidden")
	}
	a.sidebarHidden = false
	tt.Frame()
	tt.Advance(time.Second)
	if back, ok := tt.Find("Archived chats"); !ok || back.X != shown.X {
		t.Fatalf("the sidebar isn't back in place: %v, was %v (%v)", back, shown, ok)
	}
}
