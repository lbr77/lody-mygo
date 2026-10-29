package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// Narrower than 768, the sidebar leaves the window's side and shows as
// Lody's drawer over it; choosing a chat closes it, and a wider window
// puts the sidebar back.
func TestCompactDrawer(t *testing.T) {
	a := fixture()
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if a.sidebarHidden {
		t.Fatal("starts hidden")
	}
	tt.SetSize(700, 720)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if !a.sidebarHidden || !a.compactLayout.on || tt.HasText("Sidebar status marks") {
		t.Fatalf("compact but the sidebar shows: hidden %v %q", a.sidebarHidden, tt.Texts())
	}
	if err := tt.Click("Toggle Sidebar"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	r, ok := tt.Find("Sidebar status marks")
	if !ok || r.X+r.W > 288 {
		t.Fatalf("no drawer, or past 18rem: %v %v", r, ok)
	}
	if _, ok := tt.Find("Navigation Menu"); !ok {
		t.Fatal("the drawer isn't named")
	}
	if err := tt.Click("Sidebar status marks"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if a.selected != "s2" || !a.sidebarHidden {
		t.Fatalf("choosing: selected %q, drawer hidden %v", a.selected, a.sidebarHidden)
	}
	// The backdrop closes it too.
	if err := tt.Click("Toggle Sidebar"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	tt.ClickAt(650, 400)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !a.sidebarHidden {
		t.Fatal("the backdrop didn't close it")
	}
	tt.SetSize(1100, 720)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if a.sidebarHidden || a.compactLayout.on {
		t.Fatal("wide again, but the sidebar stays hidden")
	}
}
