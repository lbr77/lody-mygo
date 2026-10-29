package app

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/session"
)

// shareFixture is the fixture's conversation, its second reply finished
// with prose, so four of its messages can be shared.
func shareFixture() *App {
	a := fixture()
	a.view.Working = false
	a.view.Entries[3] = session.Entry{ID: "r2", Role: "assistant", UserTurnID: "u2", Finished: true, StartedAt: 1000, EndedAt: 9000, Model: "Opus 5.5", Items: []session.Item{
		{ID: "c4", Type: "tool_call", Kind: "execute", Title: "go run . --workspace w1", Status: "completed", RawInput: map[string]any{"command": "go run . --workspace w1"}},
		{ID: "y", Type: "text", Text: "It synced **1,204 rows** from the live stream and kept up with every delta."},
	}}
	a.catalog.Sessions[0].CreatedAt = "2026-10-29T09:10:00.000Z"
	return a
}

// clickText clicks the middle of the element showing s, holding mods.
func clickText(t *testing.T, tt *ui.Tester, mods ui.Modifiers, s string) {
	t.Helper()
	r, ok := tt.Find(s)
	if !ok {
		t.Fatalf("no %q in %q", s, tt.Texts())
	}
	tt.ClickAtWith(mods, r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
}

func picks(a *App) string {
	var ids []string
	for _, m := range a.shareImg.picked() {
		ids = append(ids, m.id)
	}
	return strings.Join(ids, ",")
}

const (
	u1Text = "Port the Flock catalog runtime from the iOS client to Go, without a web view."
	u2Text = "Now run it against the real stream."
)

// "Share as image…" in the header menu picks messages over the
// conversation, as Lody's: a click picks or unpicks one, Shift extends
// from the last, Cmd flips a range, a drag sweeps one; the bar counts
// them and opens the preview, whose four controls shape the card.
func TestShareImage(t *testing.T) {
	a := shareFixture()
	tt := snap(t, a, "share-image-before", false, nil)
	if err := tt.Click("More actions"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "share-image-menu")
	if err := tt.Click("Share as image…"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	st := &a.shareImg
	if st.session != "s1" || len(st.candidates) != 4 {
		t.Fatalf("selection %q over %d", st.session, len(st.candidates))
	}
	// The composer gives way to the bar.
	for _, want := range []string{"0 selected", "Select all", "Cancel", "Preview image"} {
		if !tt.HasText(want) {
			t.Errorf("bar misses %q in %q", want, tt.Texts())
		}
	}
	if a.hmenu.open {
		t.Fatal("menu still open")
	}

	// A click picks a message, another unpicks it.
	clickText(t, tt, 0, u1Text)
	if got := picks(a); got != "u1" {
		t.Fatalf("click picked %q", got)
	}
	// Shift extends from it, in the conversation's order.
	clickText(t, tt, ui.Shift, u2Text)
	if got := picks(a); got != "u1,r1,u2" {
		t.Fatalf("shift-click picked %q", got)
	}
	clickText(t, tt, 0, u1Text)
	if got := picks(a); got != "r1,u2" {
		t.Fatalf("unpicked %q", got)
	}
	// Cmd flips each from the anchor to the row clicked.
	clickText(t, tt, ui.Cmd|ui.Shift, u2Text)
	if got := picks(a); got != "u1" {
		t.Fatalf("cmd-shift flipped to %q", got)
	}
	if !tt.HasText("1 selected") {
		t.Fatalf("count: %q", tt.Texts())
	}
	// A drag from a message unpicked sweeps the messages it crosses.
	from, _ := tt.Find(u2Text)
	to, ok := tt.Find("It synced 1,204 rows from the live stream and kept up with every delta.")
	if !ok {
		t.Fatalf("no reply in %q", tt.Texts())
	}
	tt.Press(from.X+from.W/2, from.Y+from.H/2)
	tt.Frame()
	tt.Move(from.X+from.W/2, from.Y+from.H/2+12)
	tt.Frame()
	tt.Move(to.X+4, to.Y+to.H/2)
	tt.Frame()
	snapFrame(t, tt, "share-image-drag")
	tt.Release(to.X+4, to.Y+to.H/2)
	tt.Frame()
	if got := picks(a); got != "u1,u2,r2" {
		t.Fatalf("drag picked %q", got)
	}
	if st.drag != nil {
		t.Fatal("the drag outlived the release")
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "share-image-selection")
	tt.SetDark(true)
	tt.Frame()
	snapFrame(t, tt, "share-image-selection-dark")
	tt.SetDark(false)
	tt.Frame()

	// Select all, then Clear selection.
	if err := tt.Click("Select all"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := picks(a); got != "u1,r1,u2,r2" || !tt.HasText("Clear selection") {
		t.Fatalf("select all: %q, %q", got, tt.Texts())
	}

	// Preview image opens the preview on them; the selection stays.
	if err := tt.Click("Preview image"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	tt.Frame()
	if !st.open || len(st.target.messages) != 4 {
		t.Fatalf("preview %v of %d", st.open, len(st.target.messages))
	}
	for _, want := range []string{"Share as image", "PNG image", "Sharing to", "Chat", "Post", "Padding", "56", "Light", "Dark",
		"Copy image", "Export PNG", "Port the Flock runtime to Go", "Claude Code", "Opus 5.5 · ~", " tokens", "2026-10-29"} {
		if !tt.HasText(want) {
			t.Errorf("preview misses %q in %q", want, tt.Texts())
		}
	}
	if st.dest != "post" || st.mat != 56 || st.backdrop != "lody" || st.dark {
		t.Fatalf("opened on %s %v %s dark=%v", st.dest, st.mat, st.backdrop, st.dark)
	}
	snapFrame(t, tt, "share-image-preview")

	// The size reseeds the mat; without a ground the mat is inert and the
	// sign-off moves into the caption.
	if err := tt.Click("Chat"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if st.dest != "chat" || st.mat != 16 || !tt.HasText("16") {
		t.Fatalf("chat: %s %v", st.dest, st.mat)
	}
	snapFrame(t, tt, "share-image-preview-chat")
	if err := tt.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Welcome"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if !st.dark || st.backdrop != "welcome" {
		t.Fatalf("surface: dark=%v %s", st.dark, st.backdrop)
	}
	snapFrame(t, tt, "share-image-preview-dark")
	if err := tt.Click("None"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if spec := st.spec(); spec.signOffOnMat() || spec.matPx() != 0 {
		t.Fatalf("no ground: %+v", spec)
	}
	if !tt.HasText("lody.ai") {
		t.Fatalf("caption sign-off: %q", tt.Texts())
	}
	snapFrame(t, tt, "share-image-preview-none")

	// Dismissing the preview keeps the selection.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if st.open || st.session != "s1" || picks(a) != "u1,r1,u2,r2" {
		t.Fatalf("dismissed: open=%v session=%q picks=%q", st.open, st.session, picks(a))
	}
	tt.Advance(400 * time.Millisecond)
	tt.Frame()

	// Copy image finishes the share: the PNG on the clipboard, the
	// preview and the selection gone, a toast saying so.
	if err := tt.Click("Preview image"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	var copied []byte
	oldClip := shareImageClipboard
	shareImageClipboard = func(b []byte) error { copied = b; return nil }
	defer func() { shareImageClipboard = oldClip }()
	wait := pumpUpdates(t, a)
	if err := tt.Click("Copy image"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if st.busy != "copy" || !tt.HasText("Copying...") {
		t.Fatalf("copy shows no progress: %q %q", st.busy, tt.Texts())
	}
	wait("the copy", func() bool { return copied != nil })
	tt.Frame()
	img, err := png.Decode(bytes.NewReader(copied))
	if err != nil {
		t.Fatal(err)
	}
	// A post card on its 56px mat, at twice the pixels.
	if w := img.Bounds().Dx(); w != (560+2*56)*2 {
		t.Fatalf("copied image %v", img.Bounds())
	}
	if st.open || st.session != "" {
		t.Fatalf("after copy: open=%v session=%q", st.open, st.session)
	}
	if !tt.HasText("Image copied to clipboard") {
		t.Fatalf("no toast: %q", tt.Texts())
	}
	if dir := os.Getenv("LODY_SNAPSHOTS"); dir != "" {
		os.WriteFile(filepath.Join(dir, "share-image-copied.png"), copied, 0o644)
	}
}

// Export PNG completes only on a real save: a cancelled picker leaves
// the preview and the selection, a failure says so, and a save writes
// the card and ends the flow.
func TestShareImageExport(t *testing.T) {
	a := shareFixture()
	tt := snap(t, a, "share-image-export", false, nil)
	a.startShareImage("s1")
	tt.Frame()
	clickText(t, tt, 0, u2Text)
	if err := tt.Click("Preview image"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	st := &a.shareImg
	tt.Frame()
	// A short card: the preview, and the dialog, shrink to it.
	snapFrame(t, tt, "share-image-preview-short")
	wait := pumpUpdates(t, a)
	var asked string
	answer, answerErr := "", error(nil)
	oldSave := shareImageSaveDialog
	shareImageSaveDialog = func(_ *mygo.Window, name string) (string, error) { asked = name; return answer, answerErr }
	defer func() { shareImageSaveDialog = oldSave }()

	// Backing out of the picker.
	if err := tt.Click("Export PNG"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	wait("the picker", func() bool { return st.busy == "" })
	tt.Frame()
	if asked != "Port the Flock runtime to Go.png" || !st.open || st.session != "s1" || st.failed {
		t.Fatalf("cancelled: asked %q open=%v session=%q failed=%v", asked, st.open, st.session, st.failed)
	}
	// A failure is reported, for retry.
	answerErr = errors.New("disk full")
	if err := tt.Click("Export PNG"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	wait("the failure", func() bool { return st.busy == "" })
	tt.Frame()
	if !st.open || !st.failed || !tt.HasText("Could not complete the image action. Please try again.") {
		t.Fatalf("failure: open=%v failed=%v %q", st.open, st.failed, tt.Texts())
	}
	// A save.
	answerErr = nil
	answer = filepath.Join(t.TempDir(), "card.png")
	if err := tt.Click("Export PNG"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	wait("the save", func() bool { return !st.open })
	tt.Frame()
	data, err := os.ReadFile(answer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if st.session != "" || tt.HasText("Preview image") {
		t.Fatalf("after save: session=%q", st.session)
	}
}

// Cancel and Escape end the selection; a conversation with no prose has
// nothing to share.
func TestShareImageCancel(t *testing.T) {
	a := shareFixture()
	tt := snap(t, a, "share-image-cancel", false, nil)
	a.startShareImage("s1")
	tt.Frame()
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.shareImg.session != "" || tt.HasText("Preview image") {
		t.Fatalf("cancel left the selection: %q", tt.Texts())
	}
	a.startShareImage("s1")
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.shareImg.session != "" {
		t.Fatal("escape left the selection")
	}
	// Opening another session ends it too.
	a.startShareImage("s1")
	tt.Frame()
	a.selected = "s2"
	tt.Frame()
	if a.shareImg.session != "" {
		t.Fatal("the selection followed to another session")
	}

	a = fixture()
	a.view.Entries = a.view.Entries[3:] // a turn of tool calls alone
	tt = snap(t, a, "share-image-empty", false, nil)
	a.startShareImage("s1")
	tt.Frame()
	if a.shareImg.session != "" || !tt.HasText("No conversation to share") {
		t.Fatalf("empty: %q", tt.Texts())
	}
}

// The card is one template: the size sets its width, the mat the ground
// around it, and without a ground the image is the card on its corners.
func TestShareImageCard(t *testing.T) {
	msgs := shareImageCandidates(shareFixture().view.Entries)
	if len(msgs) != 4 || msgs[1].model != "Opus 5.5" || msgs[1].tokens <= 0 || msgs[3].tokens <= 0 {
		t.Fatalf("candidates %+v", msgs)
	}
	spec := shareCardSpec{messages: msgs, title: "Port the Flock runtime to Go", dest: "chat", mat: 16, backdrop: "lody", meta: shareMeta{name: "Claude Code", params: []string{"Opus 5.5"}, date: "2026-10-29 17:10"}}
	img := renderShareImage(spec)
	if img == nil || img.Bounds().Dx() != (360+32)*2 || img.Bounds().Dy() < 600 {
		t.Fatalf("chat card %v", img.Bounds())
	}
	// The ground's corner is the deep-sea night.
	if c := img.RGBAAt(2, 2); c.A != 255 || c.B < c.R || c.R > 40 {
		t.Fatalf("ground %v", c)
	}
	spec.backdrop, spec.dark = "none", true
	img = renderShareImage(spec)
	if img.Bounds().Dx() != 360*2 {
		t.Fatalf("bare card %v", img.Bounds())
	}
	// Past its rounded corner, nothing.
	if c := img.RGBAAt(1, 1); c.A != 0 {
		t.Fatalf("corner %v", c)
	}
	if dir := os.Getenv("LODY_SNAPSHOTS"); dir != "" {
		f, _ := os.Create(filepath.Join(dir, "share-image-card-bare.png"))
		png.Encode(f, img)
		f.Close()
	}
	if got := shareFileName(`a/b: "c"?. `, "lody-conversation"); got != "a-b- -c--.png" {
		t.Fatalf("file name %q", got)
	}
	if got := shareFileName("  ", "lody-conversation"); got != "lody-conversation.png" {
		t.Fatalf("fallback %q", got)
	}
}

// Held near the conversation's top edge, a drag scrolls it and goes on
// picking the messages it brings into view.
func TestShareImageEdgeScroll(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.view.Entries = nil
	for i := 0; i < 24; i++ {
		id := fmt.Sprint(i)
		a.view.Entries = append(a.view.Entries,
			session.Entry{ID: "u" + id, Role: "user", Status: "handled", Items: []session.Item{{ID: "t", Type: "text", Text: "Question " + id}}},
			session.Entry{ID: "r" + id, Role: "assistant", Finished: true, Items: []session.Item{{ID: "x", Type: "text", Text: "Answer " + id + "\n\nwith a second paragraph."}}})
	}
	tt := snap(t, a, "share-image-edge", false, nil)
	a.startShareImage("s1")
	tt.Frame()
	first, _ := a.chatList.Visible()
	r, ok := tt.Find("Question 23")
	if !ok {
		t.Fatalf("no last question in %q", tt.Texts())
	}
	tt.Press(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	top := a.shareImg.viewport.Y
	tt.Move(r.X+r.W/2, top+6)
	for i := 0; i < 40; i++ {
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
	}
	snapFrame(t, tt, "share-image-edge-scroll")
	tt.Release(r.X+r.W/2, top+6)
	tt.Frame()
	now, _ := a.chatList.Visible()
	if now >= first {
		t.Fatalf("the list did not scroll: first row %d, then %d", first, now)
	}
	n := len(a.shareImg.picked())
	if n < 6 || !a.shareImg.ids["u23"] || !a.shareImg.ids["r22"] {
		t.Fatalf("picked %d: %q", n, picks(a))
	}
}
