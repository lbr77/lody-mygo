package app

import (
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/session"
)

// artAt is where the drawing over the title shows, in the window: 200×140
// on the column's axis, 8 and the header's 6 above the title.
func artAt(t *testing.T, tt *ui.Tester, title string) (x, y float32) {
	t.Helper()
	r, ok := tt.Find(title)
	if !ok {
		t.Fatalf("no %q in %q", title, tt.Texts())
	}
	return r.X + r.W/2 - 100, r.Y - 8 - 6 - 140
}

// pixel is the window's colour at the drawing's point (px, py).
func pixel(tt *ui.Tester, x, y, px, py float32) color.RGBA {
	return tt.Image().RGBAAt(int(x+px), int(y+py))
}

// near is whether c is want, give or take a little antialiasing.
func near(c color.RGBA, want ui.Color) bool {
	d := func(a, b uint8) int {
		if a > b {
			return int(a - b)
		}
		return int(b - a)
	}
	return d(c.R, want.R)+d(c.G, want.G)+d(c.B, want.B) < 30
}

// A session the workspace no longer lists, once its catalog has come,
// stands in its pane as Lody's SessionNotFound, with the way back.
func TestSessionNotFound(t *testing.T) {
	a := fixture()
	a.selected, a.view = "gone", nil
	a.synced = false
	tt := snap(t, a, "session-not-found-syncing", false, nil)
	if tt.HasText("This session isn't here") {
		t.Fatal("not found before the catalog came")
	}
	a.synced = true
	for _, dark := range []bool{false, true} {
		name := "session-not-found-light"
		if dark {
			name = "session-not-found-dark"
		}
		tt = snap(t, a, name, dark, func(tt *ui.Tester) { tt.Advance(2 * time.Second) })
		for _, want := range []string{"This session isn't here", "It may have been deleted, or the link that opened it is out of date.", "Back to sessions"} {
			if !tt.HasText(want) {
				t.Errorf("%s: missing %q in %q", name, want, tt.Texts())
			}
		}
	}
	if err := tt.Click("Back to sessions"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.selected != "" || tt.HasText("This session isn't here") {
		t.Fatalf("not back to the sessions: %q", a.selected)
	}

	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	defer i18n.SetLocaleFunc(func() string { return "en" })
	a.selected = "gone"
	tt = snap(t, a, "session-not-found-chinese", false, nil)
	for _, want := range []string{"这个会话不在这里", "它可能已被删除，或者打开它的链接已经过期。", "返回会话列表"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
}

// The lens holds for 250ms, sweeps in over 1.5s and comes to rest on the
// empty content's corner; asked for less motion, it is there at once.
func TestMissingArtMotion(t *testing.T) {
	const title = "This session isn't here"
	ink := ui.Hex("#B3B3B3") // the lens's handle, gray2
	a := fixture()
	a.selected, a.view = "gone", nil
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	x, y := artAt(t, tt, title)
	// Where its handle rests, below the window.
	if c := pixel(tt, x, y, 168, 118); near(c, ink) {
		t.Fatalf("the lens rests at once: %v", c)
	}
	tt.Advance(200 * time.Millisecond)
	if c := pixel(tt, x, y, 168, 118); near(c, ink) {
		t.Fatalf("the lens moves before its beat: %v", c)
	}
	tt.Advance(1600 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "art-missing-rest")
	if c := pixel(tt, x, y, 168, 118); !near(c, ink) {
		t.Fatalf("the lens didn't come to rest: %v", c)
	}
	// The shadow under it gathered, a step darker than the ground.
	if c := pixel(tt, x, y, 160, 133); near(c, lightPalette.Canvas) {
		t.Fatalf("no shadow under the lens: %v", c)
	}

	a = fixture()
	a.selected, a.view = "gone", nil
	tt = ui.NewTester(a.render, 1100, 720)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	tt.Frame()
	x, y = artAt(t, tt, title)
	if c := pixel(tt, x, y, 168, 118); !near(c, ink) {
		t.Fatalf("with less motion the lens isn't at rest: %v", c)
	}
}

// A conversation whose replica gave up stands as Lody's message-list
// fallback over the composer, its error to copy and its report behind
// a disclosure, and loads again on Try again.
func TestMessageListFailed(t *testing.T) {
	a := fixture()
	a.view = &session.View{Status: "offline", Reason: "session_limit"}
	a.draft = "Half a thought"
	tt := snap(t, a, "message-list-failed", false, func(tt *ui.Tester) { tt.Advance(2 * time.Second) })
	for _, want := range []string{"Messages couldn't be displayed", "The message list failed to render. Your draft below is safe.",
		"Try again", "session: history exceeds the limit", "Technical details", "Add attachment"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("Boundary: SessionChatStream") {
		t.Fatal("the report shows before its details open")
	}

	var copied string
	old := writeClipboard
	writeClipboard = func(s string) { copied = s }
	defer func() { writeClipboard = old }()
	if err := tt.Click("Copy error details"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !strings.HasPrefix(copied, "session: history exceeds the limit\n\n") || !strings.Contains(copied, "Boundary: SessionChatStream") ||
		!strings.Contains(copied, "Session: s1") {
		t.Fatalf("copied %q", copied)
	}
	if _, ok := tt.Find("Copied"); !ok {
		t.Fatalf("not marked copied: %q", tt.Texts())
	}

	if err := tt.Click("Technical details"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapFrame(t, tt, "message-list-failed-details")
	if !tt.HasText("Boundary: SessionChatStream") || !tt.HasText("Session: s1") {
		t.Fatalf("no report in %q", tt.Texts())
	}

	if err := tt.Click("Try again"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.view != nil || tt.HasText("Messages couldn't be displayed") || a.listFail.copied {
		t.Fatal("not loading again")
	}
}

// The piece holds in its slot for the beat, then falls and lands below
// the window, tipped onto its corner; asked for less motion, it lies
// there at once.
func TestBrokenArtMotion(t *testing.T) {
	const title = "Messages couldn't be displayed"
	accent := lightPalette.Primary
	a := fixture()
	a.view = &session.View{Status: "offline", Reason: "session_limit"}
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	x, y := artAt(t, tt, title)
	// In the slot, right of the piece's centre; and where that point
	// lands, tipped -6° about its centre (142, 121).
	inSlot, landed := [2]float32{133, 75}, [2]float32{166.9, 118.4}
	if c := pixel(tt, x, y, inSlot[0], inSlot[1]); !near(c, accent) {
		t.Fatalf("the piece isn't in its slot: %v", c)
	}
	if c := pixel(tt, x, y, landed[0], landed[1]); near(c, accent) {
		t.Fatalf("the piece has landed at once: %v", c)
	}
	tt.Advance(1600 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "art-broken-rest")
	if c := pixel(tt, x, y, inSlot[0], inSlot[1]); near(c, accent) {
		t.Fatalf("the piece stays in its slot: %v", c)
	}
	if c := pixel(tt, x, y, landed[0], landed[1]); !near(c, accent) {
		t.Fatalf("the piece didn't land: %v", c)
	}

	a = fixture()
	a.view = &session.View{Status: "offline", Reason: "session_limit"}
	tt = ui.NewTester(a.render, 1100, 720)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	tt.Frame()
	x, y = artAt(t, tt, title)
	if c := pixel(tt, x, y, landed[0], landed[1]); !near(c, accent) {
		t.Fatalf("with less motion the piece hasn't landed: %v", c)
	}
}

// The recovery page in Chinese, its last line's link in its sentence.
func TestRecoveryChinese(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	defer i18n.SetLocaleFunc(func() string { return "en" })
	a := fixture()
	a.sync = &catalog.Sync{}
	a.syncState = catalog.State{}
	a.token.Store("token")
	a.stuck.since, a.stuck.recovery = time.Now().Add(-46*time.Second), true
	tt := snap(t, a, "stuck-recovery-chinese", false, func(tt *ui.Tester) { tt.Advance(2 * time.Second) })
	for _, want := range []string{"打开此工作区的时间过长", "重新加载 Lody", "清除本地缓存并重试", "复制诊断信息", "技术详情", "仍然无法打开？", "清除全部本地数据并退出登录"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
}
