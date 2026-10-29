package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/session"
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

// Scrolled up, the conversation fades at its edges and offers a way back
// to the latest message, which spins while the agent works.
func TestScrollToLatest(t *testing.T) {
	a := fixture()
	var long []session.Entry
	for i := 0; i < 12; i++ {
		id := fmt.Sprint("pad", i)
		long = append(long, session.Entry{ID: "u" + id, Role: "user", Items: []session.Item{{ID: "t", Type: "text", Text: "Question " + id}}},
			session.Entry{ID: "r" + id, Role: "assistant", UserTurnID: "u" + id, Finished: true, Items: []session.Item{{ID: "x", Type: "text", Text: strings.Repeat("A long answer line. ", 40)}}})
	}
	a.view.Entries = append(long, a.view.Entries...)
	tt := snap(t, a, "scroll-latest-end", false, nil)
	if tt.HasText("Scroll to latest message") {
		t.Fatal("offered at the end")
	}
	a.chatList.ScrollTo(0, ui.Start)
	tt.Frame()
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "scroll-latest")
	if !tt.HasText("Scroll to latest message") {
		t.Fatalf("not offered when scrolled up: %q", tt.Texts())
	}
	if err := tt.Click("Scroll to latest message"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	tt.Frame()
	tt.Frame()
	if !a.chatList.AtEnd() || tt.HasText("Scroll to latest message") {
		t.Fatal("didn't go back to the end")
	}
}

// While a message's attachments upload they stay in the composer, an
// image washed over what is still to go and a file with its bar.
func TestUploadProgress(t *testing.T) {
	a := fixture()
	a.view.Entries, a.view.Working = a.view.Entries[:2], false
	files := []pendingFile{{Path: "/tmp/a.png", Name: "a.png", Image: true, Size: 10}, {Path: "/tmp/notes.txt", Name: "notes.txt", Size: 2048}}
	a.sending, a.sendingID = true, a.selected
	a.sendingAttachments(files)
	a.fileProgress["/tmp/a.png"], a.fileProgress["/tmp/notes.txt"] = 100, 40
	tt := snap(t, a, "upload-progress", false, nil)
	if !tt.HasText("notes.txt") || tt.HasText("Remove notes.txt") {
		t.Fatalf("uploading chips: %q", tt.Texts())
	}
	a.sentAttachments()
	a.sending = false
	tt.Frame()
	if tt.HasText("notes.txt") {
		t.Fatal("chips stay after sending")
	}
}

// "Show all" opens a now list over its column on Lody's spring, the rest
// of the column stepping back, and "Show less" lands it back in its tile.
func TestNowListFocus(t *testing.T) {
	a := fixture()
	a.selected = ""
	a.homeBoard = "now"
	now := time.Now()
	created := now.Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	for i := 0; i < 14; i++ {
		a.catalog.Sessions = append(a.catalog.Sessions, catalog.Session{ID: fmt.Sprint("wait", i), MachineID: "m1", UserID: "u1", CreatedAt: created,
			Title: fmt.Sprint("Waiting chat ", i), Status: "requestPermission", ProjectID: "m1:local:p1", LastMessageAt: float64(now.Add(-time.Duration(i) * time.Minute).UnixMilli())})
	}
	for i := range a.catalog.Sessions {
		a.catalog.Sessions[i].UserID, a.catalog.Sessions[i].CreatedAt = "u1", created
	}
	tt := snap(t, a, "now-focus-closed", false, nil)
	var showAll string
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, "Show all") {
			showAll = s
		}
	}
	if showAll == "" {
		t.Fatalf("no Show all in %q", tt.Texts())
	}
	if err := tt.Click(showAll); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(120 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "now-focus-opening")
	tt.Advance(900 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "now-focus-open")
	if a.homeNowFocus == "" || !tt.HasText("Show less") {
		t.Fatalf("not open: %q", tt.Texts())
	}
	// "Show less" sits at the foot of the rows, scrolled to; pressing it:
	a.homeNowOpen = ""
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	tt.Frame()
	if a.homeNowFocus != "" || a.homeNowOpen != "" || tt.HasText("Show less") {
		t.Fatalf("not landed back: focus %q open %q", a.homeNowFocus, a.homeNowOpen)
	}
}

// A workspace connecting for 45 seconds says so near the bottom, with a
// way to recover, as Lody's stuck-connection banner.
func TestStuckConnection(t *testing.T) {
	a := fixture()
	a.view.Entries, a.view.Working = a.view.Entries[:2], false
	a.sync = &catalog.Sync{}
	a.syncState = catalog.State{}
	a.token.Store("token")
	tt := snap(t, a, "stuck-early", false, nil)
	if tt.HasText("Still connecting…") {
		t.Fatal("shown at once")
	}
	a.stuck.since = time.Now().Add(-46 * time.Second)
	tt.Frame()
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "stuck-banner")
	if !tt.HasText("Still connecting…") || !tt.HasText("Recover…") {
		t.Fatalf("no banner in %q", tt.Texts())
	}
	if err := tt.Click("Recover…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Lody is taking too long to open") || !tt.HasText("Reload Lody") || !tt.HasText("Clear local cache and retry") {
		t.Fatalf("no recovery in %q", tt.Texts())
	}
	var copied string
	old := writeClipboard
	writeClipboard = func(s string) { copied = s }
	defer func() { writeClipboard = old }()
	if err := tt.Click("Copy diagnostics"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(copied, "Workspace: w1") || !strings.Contains(copied, "Catalog live: false") {
		t.Fatalf("diagnostics %q", copied)
	}
	a.syncState.Live = true
	a.stuck.recovery = false
	tt.Frame()
	if tt.HasText("Still connecting…") || !a.stuck.since.IsZero() {
		t.Fatal("still stuck once live")
	}
}

// A second after a reply finishes, the composer asks whether to turn
// notifications on, once a chat, as Lody's prompt.
func TestNotifyPrompt(t *testing.T) {
	a := fixture()
	a.view.Entries, a.view.Working = a.view.Entries[:2], false
	a.catalog.Sessions[0].Status = "idle"
	tt := snap(t, a, "notify-ask-early", false, nil)
	if tt.HasText("Enable notifications?") {
		t.Fatal("asked at once")
	}
	a.notifyAsk.since = time.Now().Add(-2 * time.Second)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "notify-ask")
	if !tt.HasText("Enable notifications?") || !tt.HasText("Don't remind me") {
		t.Fatalf("no prompt in %q", tt.Texts())
	}
	if err := tt.Click("Don't remind me"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.notifyAsk.never || tt.HasText("Enable notifications?") {
		t.Fatal("still asks")
	}
	// Notifications on: it never asks.
	b := fixture()
	b.notifications = true
	b.view.Working = false
	b.catalog.Sessions[0].Status = "idle"
	b.notifyAsk.since, b.notifyAsk.pending = time.Now().Add(-2*time.Second), "s1"
	if tt := snap(t, b, "notify-ask-on", false, nil); tt.HasText("Enable notifications?") {
		t.Fatal("asked with notifications on")
	}
}

// Resting on a sidebar chat for 650ms shows its info card beside it;
// within 3s of one the next shows at once, and a press closes it.
func TestSessionHoverCard(t *testing.T) {
	a := fixture()
	s := &a.catalog.Sessions[1]
	s.BranchName, s.UserID, s.DiffAdd, s.DiffDel = "lody/sidebar-marks", "u1", 42, 7
	tt := snap(t, a, "hover-card-cold", false, nil)
	r, ok := tt.Find("Sidebar status marks")
	if !ok {
		t.Fatalf("no row in %q", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if tt.HasText("lody/sidebar-marks") {
		t.Fatal("shown at once")
	}
	a.card.since = time.Now().Add(-time.Second)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "hover-card")
	for _, want := range []string{"lody/sidebar-marks", "lody-mygo", "Ada Lovelace", "Studio", "+42", "−7"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	// Warm: the next chat's card comes at once.
	n, ok := tt.Find("Fix the flaky upload test")
	if !ok {
		t.Fatal("no second row")
	}
	tt.Move(n.X+n.W/2, n.Y+n.H/2)
	tt.Frame()
	if a.card.shown != "s3" || !a.card.inRun {
		t.Fatalf("not warm: %+v", a.card)
	}
	if !tt.HasText("website") {
		t.Fatalf("no folder in %q", tt.Texts())
	}
	// A press closes it, and it does not come back while the pointer stays.
	tt.Press(n.X+n.W/2, n.Y+n.H/2)
	tt.Release(n.X+n.W/2, n.Y+n.H/2)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	if a.card.shown != "" {
		t.Fatalf("still shown after a press: %+v", a.card)
	}
}

// From ten user rounds, beside a wide enough pane, the conversation has
// Lody's outline rail: a tick a round, a card on resting there, and a jump
// on a click.
func TestOutlineRail(t *testing.T) {
	a := fixture()
	a.view.Working = false
	var entries []session.Entry
	for i := range 12 {
		id := fmt.Sprint(i)
		entries = append(entries,
			session.Entry{ID: "ou" + id, Role: "user", Items: []session.Item{{ID: "t", Type: "text", Text: "Question **number** " + id}}},
			session.Entry{ID: "or" + id, Role: "assistant", Finished: true, Items: []session.Item{{ID: "t", Type: "text", Text: "Answer " + id + "\n\n" + strings.Repeat("More prose. ", 40*(i%4))}}},
		)
	}
	a.view.Entries = entries
	a.outline.digests = map[string]roundDigest{}
	rounds := a.outline.rounds(entries)
	if len(rounds) != 12 || rounds[3].title != "Question number 3" || !strings.HasPrefix(rounds[3].preview, "Answer 3 More prose.") {
		t.Fatalf("rounds %+v", rounds[3])
	}
	if rounds[0].weight != 0 || rounds[3].weight == 0 {
		t.Fatalf("weights %d %d", rounds[0].weight, rounds[3].weight)
	}
	tt := ui.NewTester(a.render, 1400, 800)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "outline-rail")
	if a.outline.active < 0 {
		t.Fatalf("no reader round: %+v", a.outline.active)
	}
	r, ok := tt.Find("Question number 4")
	if !ok {
		t.Fatalf("no tick for round 4 in %q", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if a.outline.pointer != 4 || a.outline.open {
		t.Fatalf("pointer %d open %v", a.outline.pointer, a.outline.open)
	}
	a.outline.since = time.Now().Add(-time.Second)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "outline-card")
	if !a.outline.open || !tt.HasText("Answer 4") {
		t.Fatalf("no card: %+v", a.outline)
	}
	if err := tt.Click("Question number 4"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if a.outline.open {
		t.Fatal("card open after the jump")
	}
	tt.Frame()
	if first, last := a.chatList.Visible(); first > 8 || last < 8 || a.outline.active != 4 {
		t.Fatalf("jumped to rows %d–%d, round %d", first, last, a.outline.active)
	}
	// Narrow, it is gone.
	tt.SetSize(1000, 800)
	tt.Frame()
	if _, ok := tt.Find("Question number 4"); ok {
		t.Fatal("rail beside a narrow pane")
	}
}
