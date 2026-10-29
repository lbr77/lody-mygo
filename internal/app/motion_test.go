package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/cloud"
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

// The header's Share pill opens Lody's access panel: who can open the
// conversation, the audiences, and the copies; the web slides to the
// publishing view, and back.
func TestAccessPanel(t *testing.T) {
	a := fixture()
	a.offline = false
	s := &a.catalog.Sessions[0]
	s.Project = map[string]any{"kind": "local", "localProjectId": "p1"}
	ws := a.workspaceID + "|"
	a.fetches = map[string]*fetched{
		ws + "members":  {value: []cloud.Member{{UserID: "u1"}, {UserID: "u2"}}},
		ws + "machines": {value: []visibleMachine{{MachineID: "m1", OwnerUserID: "u1", UpdatedAt: 1}}},
		ws + "projects": {value: []visibleProject{{MachineID: "m1", LocalProjectID: "p1", OwnerUserID: "u1", SharedWithTeam: true}}},
	}
	tt := snap(t, a, "access-closed", false, nil)
	if err := tt.Click("Share"); err != nil {
		t.Fatalf("no pill: %v in %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Advance(700 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "access-panel")
	for _, want := range []string{"Private to you", "Studio is not shared with the team.", "Only you", "Team", "Web", "Copy URL", "Copy as Markdown"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Web"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(700 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "access-publish")
	if a.access.view != "publish" || !tt.HasText("Publish to the web") {
		t.Fatalf("not publishing: %q", tt.Texts())
	}
	// The panel's way back sits before its title (the sidebar has a Back
	// of its own).
	head, _ := tt.Find("Publish to the web")
	tt.ClickAt(head.X-16, head.Y+head.H/2)
	tt.Frame()
	tt.Advance(700 * time.Millisecond)
	tt.Frame()
	if a.access.view != "home" || !tt.HasText("Private to you") {
		t.Fatalf("not back: %q", tt.Texts())
	}
	// Choosing the team asks first.
	if err := tt.Click("Team"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.confirming.open || a.confirming.title != "Share “Port the Flock runtime to Go” with the team?" {
		t.Fatalf("no confirmation: %+v", a.confirming)
	}
}

// Files show as Lody's cards: a sheet drawn by kind, the type and size,
// and an action by state; a text file opens its preview, and hovering a
// card that opens something lifts and tilts its sheet.
func TestFileCards(t *testing.T) {
	a := fixture()
	a.view.Working = false
	now := float64(time.Now().UnixMilli())
	a.view.Entries = []session.Entry{
		{ID: "fu", Role: "user", Items: []session.Item{
			{ID: "t", Type: "text", Text: "Here is the report."},
			{ID: "f1", Type: "file", FileID: "f1", FileName: "report.pdf", MimeType: "application/pdf", SizeBytes: 2516582, UploadedAt: now},
		}},
		{ID: "fr", Role: "assistant", Finished: true, Items: []session.Item{
			{ID: "t", Type: "text", Text: "Done."},
			{ID: "f2", Type: "file", FileID: "f2", FileName: "notes.md", SizeBytes: 812, TextPreview: true, UploadedAt: now},
			{ID: "f3", Type: "file", FileID: "f3", FileName: "data.csv", SizeBytes: 4096, Transport: "local", FileMachine: "m1"},
			{ID: "f4", Type: "file", FileID: "f4", FileName: "old.zip", SizeBytes: 9000, UploadedAt: now - 100*24*3600*1000},
		}},
	}
	tt := snap(t, a, "file-cards", false, nil)
	for _, want := range []string{"report.pdf", "PDF · 2.4 MB", "notes.md", "MD · 812 B", "Uploading from Studio…", "File expired · 8.8 KB"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	for name, kind := range map[string]string{"a.go": "code", "b.yaml": "data", "c.xlsx": "spreadsheet", "d.mp4": "video", "e": "binary", "f.bin": "binary"} {
		if got := fileKind(name, ""); got != kind {
			t.Errorf("kind of %s = %s, want %s", name, got, kind)
		}
	}
	if got := fileKind("x", "image/png"); got != "image" {
		t.Errorf("kind by media type = %s", got)
	}
	r, _ := tt.Find("notes.md")
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "file-card-hover")
	if err := tt.Click("notes.md"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.attachPrev.open || a.attachPrev.item.FileID != "f2" || a.attachPrev.raw {
		t.Fatalf("no rendered preview: %+v", a.attachPrev)
	}
	a.attachPrev.loading, a.attachPrev.text, a.attachPrev.truncated = false, "# Notes\n\nSome **notes**.", true
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "file-preview")
	for _, want := range []string{"Rendered", "Raw", "Notes", "Download full file"} {
		if !tt.HasText(want) {
			t.Errorf("preview misses %q in %q", want, tt.Texts())
		}
	}
}

// The header's ⋯ is Lody's session menu: the chat's place and machine,
// Full width, Share, Find, the forks, Rename, the copies and Archive; Copy
// as Markdown copies the conversation.
func TestHeaderMenu(t *testing.T) {
	a := fixture()
	a.view.Working = false
	s := &a.catalog.Sessions[0]
	s.BranchName, s.BaseBranch = "lody/flock", "main"
	s.Project = map[string]any{"kind": "local", "localProjectId": "p1"}
	tt := snap(t, a, "header-menu-closed", false, nil)
	if err := tt.Click("More actions"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	for _, want := range []string{"lody/flock", "Base branch: main", "Studio", "Local", "Full width", "Find in session", "Fork session", "Rename Chat", "Archive session"} {
		if !tt.HasText(want) {
			t.Errorf("menu misses %q in %q", want, tt.Texts())
		}
	}
	// The copies open beside their row.
	a.hmenu.sub = "copy"
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "header-menu")
	var copied string
	old := writeClipboard
	writeClipboard = func(s string) { copied = s }
	defer func() { writeClipboard = old }()
	wait := pumpUpdates(t, a)
	if err := tt.Click("Copy as Markdown"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	wait("the copy", func() bool { return copied != "" })
	if !strings.Contains(copied, "Port the Flock catalog runtime") || !strings.Contains(copied, "# Port the Flock runtime to Go") {
		t.Fatalf("copied %q", copied)
	}
	if a.hmenu.open {
		t.Fatal("menu still open after a choice")
	}
}
