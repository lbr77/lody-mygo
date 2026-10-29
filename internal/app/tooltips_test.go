package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// tipTester draws Alpha, whose tip waits 150ms, and Beta, whose waits the
// default 600ms, with room to rest the pointer on neither.
func tipTester(t *testing.T) (*ui.Tester, func(label string), func()) {
	t.Helper()
	tips = tipState{}
	tt := ui.NewTester(func(c *ui.Context) {
		p := applyTheme(c)
		ui.Row(c).Fill().Padding(60).Gap(40).AlignItems(ui.Start).Children(func() {
			withTipDelay(150*time.Millisecond, func() {
				tip(c, p, ui.ButtonBase(c.Key("a")).Size(80, 30).Children(func() { ui.Text(c, "Alpha") }), "Tip A")
			})
			tip(c, p, ui.ButtonBase(c.Key("b")).Size(80, 30).Children(func() { ui.Text(c, "Beta") }), "Tip B")
		})
	}, 400, 300)
	tt.Frame()
	over := func(label string) {
		t.Helper()
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("no %q", label)
		}
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Frame()
	}
	away := func() {
		tt.Move(390, 290)
		tt.Frame()
	}
	return tt, over, away
}

func TestTipWaitsItsSurfaceDelay(t *testing.T) {
	tt, over, away := tipTester(t)
	over("Alpha")
	if tt.HasText("Tip A") {
		t.Fatal("Alpha's tip showed at once")
	}
	tt.Advance(100 * time.Millisecond)
	tt.Frame()
	if tt.HasText("Tip A") {
		t.Fatal("Alpha's tip showed before its 150ms")
	}
	tt.Advance(60 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Tip A") {
		t.Fatal("Alpha's tip didn't show after 150ms")
	}

	// Gone a while, the next waits its own delay: Beta's 600ms.
	away()
	tt.Advance(time.Second)
	tt.Frame()
	over("Beta")
	tt.Advance(550 * time.Millisecond)
	tt.Frame()
	if tt.HasText("Tip B") {
		t.Fatal("Beta's tip showed before 600ms")
	}
	tt.Advance(60 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Tip B") {
		t.Fatal("Beta's tip didn't show after 600ms")
	}
}

func TestTipOpensAtOnceWhileWarm(t *testing.T) {
	tt, over, _ := tipTester(t)
	over("Alpha")
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Tip A") {
		t.Fatal("Alpha's tip didn't show")
	}
	// Straight on to Beta: within the warm window it shows at once.
	over("Beta")
	if !tt.HasText("Tip B") {
		t.Fatal("Beta's tip waited though a tip had just closed")
	}
}

func TestTipClosesOnPressUntilLeft(t *testing.T) {
	tt, over, away := tipTester(t)
	over("Alpha")
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Alpha"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	tt.Frame()
	if tt.HasText("Tip A") {
		t.Fatal("the tip came back under the pointer after a press")
	}
	away()
	tt.Advance(time.Second)
	over("Alpha")
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Tip A") {
		t.Fatal("the tip didn't show again once the pointer came back")
	}
}

func TestHoverPopOpensOnRestAndStaysOnClick(t *testing.T) {
	var h hoverPop
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().Padding(60).AlignItems(ui.Start).Children(func() {
			b := ui.ButtonBase(c.Key("info")).Size(80, 30).Children(func() { ui.Text(c, "Info") })
			h.update(c, b, 150*time.Millisecond)
		})
	}, 400, 300)
	tt.Frame()
	r, _ := tt.Find("Info")
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Advance(100 * time.Millisecond)
	tt.Frame()
	if h.open {
		t.Fatal("opened before 150ms")
	}
	tt.Advance(60 * time.Millisecond)
	tt.Frame()
	if !h.open {
		t.Fatal("didn't open after resting 150ms")
	}
	// Opened by hover, it closes once the pointer has gone.
	tt.Move(390, 290)
	tt.Frame()
	tt.Advance(hoverGrace + 10*time.Millisecond)
	tt.Frame()
	if h.open {
		t.Fatal("stayed open after the pointer left")
	}

	// Opened by a click, it stays.
	if err := tt.Click("Info"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !h.open || !h.clicked {
		t.Fatal("a click didn't open it")
	}
	tt.Move(390, 290)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	if !h.open {
		t.Fatal("a clicked-open popover closed as the pointer left")
	}
	// A second click closes it, and resting there doesn't bring it back.
	if err := tt.Click("Info"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	if h.open {
		t.Fatal("came back under the pointer after a click closed it")
	}
}
