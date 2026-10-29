package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// uploadCall is an attachment the fake upload holds until the test says
// how it went.
type uploadCall struct {
	name     string
	ctx      context.Context
	progress func(sent, total int64)
	done     chan error
}

// sendRig is the fixture with an open conversation whose uploads and
// writes the test plays: uploads wait on calls, writes put the turn where
// the replica would, in the conversation or its queue, then wait on
// release.
type sendRig struct {
	t       *testing.T
	a       *App
	pump    func(what string, ok func() bool)
	calls   chan uploadCall
	written chan session.Turn
	release chan error
}

func newSendRig(t *testing.T, working bool) *sendRig {
	t.Helper()
	// What the outbox keeps is the test's own.
	t.Setenv("LODY_DATA_DIR", t.TempDir())
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = working
	if !working {
		a.catalog.Sessions[0].Status = "idle"
	}
	a.conn = &cloud.Conn{ID: "w1"}
	a.replica = &session.Replica{ID: "s1"}
	r := &sendRig{t: t, a: a, pump: pumpUpdates(t, a), calls: make(chan uploadCall, 16), written: make(chan session.Turn, 16), release: make(chan error, 16)}
	upload, send := uploadAttachment, sendTurn
	t.Cleanup(func() { uploadAttachment, sendTurn = upload, send })
	uploadAttachment = func(ctx context.Context, _ *App, workspace, sessionID string, f pendingFile, progress func(sent, total int64)) (map[string]any, error) {
		call := uploadCall{name: f.Name, ctx: ctx, progress: progress, done: make(chan error, 1)}
		r.calls <- call
		select {
		case err := <-call.done:
			if err != nil {
				return nil, err
			}
			if f.Image {
				return map[string]any{"type": "image", "imageId": "img-" + f.Name, "sizeBytes": float64(f.Size)}, nil
			}
			return map[string]any{"type": "file", "fileId": "file-" + f.Name, "fileName": f.Name, "sizeBytes": float64(f.Size)}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	sendTurn = func(ctx context.Context, _ *session.Replica, turn session.Turn) (string, error) {
		// The local write: the turn shows at once, as the replica's.
		a.update(func() {
			e := session.Entry{ID: turn.ID, Role: "user", Status: "pending", Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
				Items: []session.Item{{ID: "t", Type: "text", Text: turn.Text}}}
			if turn.Queue {
				e.Status, e.Queued = "queued", true
				a.view.Queue = append(a.view.Queue, e)
			} else {
				a.view.Entries = append(a.view.Entries, e)
			}
		})
		r.written <- turn
		select {
		case err := <-r.release:
			if err != nil {
				return session.NotSent, err
			}
			if turn.Queue {
				return session.Queued, nil
			}
			return session.Accepted, nil
		case <-ctx.Done():
			return session.Unknown, ctx.Err()
		}
	}
	return r
}

// upload waits for the next upload to start, running the app's updates
// meanwhile.
func (r *sendRig) upload(name string) uploadCall {
	r.t.Helper()
	var call uploadCall
	r.pump("the upload of "+name, func() bool {
		select {
		case call = <-r.calls:
			return true
		default:
			return false
		}
	})
	if call.name != name {
		r.t.Fatalf("uploading %q, want %q", call.name, name)
	}
	return call
}

// write waits for the next write.
func (r *sendRig) write() session.Turn {
	r.t.Helper()
	var turn session.Turn
	r.pump("a write", func() bool {
		select {
		case turn = <-r.written:
			return true
		default:
			return false
		}
	})
	return turn
}

// noWrite makes sure nothing is written while the app catches up.
func (r *sendRig) noWrite() {
	r.t.Helper()
	deadline := time.Now().Add(100 * time.Millisecond)
	r.pump("a moment", func() bool { return time.Now().After(deadline) })
	select {
	case turn := <-r.written:
		r.t.Fatalf("written out of turn: %q", turn.Text)
	default:
	}
}

// progress reports how far an upload has gone and waits for the message
// to hear it.
func (r *sendRig) progress(call uploadCall, sent, total int64) {
	r.t.Helper()
	call.progress(sent, total)
	want := float32(sent) * 100 / float32(total)
	r.pump("the progress", func() bool {
		for _, s := range r.a.pending.list {
			for _, at := range s.attachments {
				if at.file.Name == call.name && at.progress == want {
					return true
				}
			}
		}
		return false
	})
}

var (
	pendingImageFile = pendingFile{Path: "/tmp/pending/shot.png", Name: "shot.png", Image: true, Size: 1024}
	pendingTextFile  = pendingFile{Path: "/tmp/pending/notes.txt", Name: "notes.txt", Size: 3072}
)

// Sending a message with attachments clears the composer at once and
// holds the message at the end of the conversation, where it is anchored
// as the message just sent: Waiting to send while its attachments upload,
// each card with its progress; a failed file says why on its card, the
// message Not sent, the sidebar's mark turning to the alert; Continue
// sending uploads only what failed, then writes the turn, and the written
// message takes the held one's place.
func TestPendingSendInConversation(t *testing.T) {
	r := newSendRig(t, false)
	a := r.a
	a.draft = "Here are the logs"
	a.attachments = []pendingFile{pendingImageFile, pendingTextFile}
	a.send()
	if a.draft != "" || len(a.attachments) != 0 || a.sending {
		t.Fatalf("the composer wasn't cleared and freed: %q %v sending %v", a.draft, a.attachments, a.sending)
	}
	if len(a.pending.list) != 1 || a.pending.list[0].queue {
		t.Fatalf("held %+v", a.pending.list)
	}
	held := a.pending.list[0]
	if s := a.chatMotion.sent; !s.held || s.id != held.id {
		t.Fatalf("not anchored to the held message: %+v", s)
	}

	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	tt.Frame()
	img := r.upload("shot.png")
	tt.Frame()
	for _, want := range []string{"Here are the logs", "Waiting to send · Uploading attachments", "notes.txt", "Uploading · 0%", "Cancel send"} {
		if !tt.HasText(want) {
			t.Errorf("held: missing %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("Continue sending") {
		t.Error("offers to continue before it failed")
	}
	if _, ok := tt.Find(locales.SendStatusSending(fileSize(0), fileSize(4096))); !ok {
		t.Errorf("no send ring in the sidebar: %q", tt.Texts())
	}
	r.progress(img, 512, 1024)
	tt.Frame()
	if _, ok := tt.Find(locales.SendStatusSending(fileSize(512), fileSize(4096))); !ok {
		t.Error("the sidebar's ring doesn't say what was sent")
	}
	snapFrame(t, tt, "pending-send-uploading")

	img.done <- nil
	notes := r.upload("notes.txt")
	r.progress(notes, 1536, 3072)
	tt.Frame()
	if !tt.HasText("Uploading · 50%") {
		t.Errorf("the file's progress: %q", tt.Texts())
	}
	notes.done <- errors.New("upload failed: 413")
	r.pump("the failure", func() bool { return held.err != "" })
	tt.Frame()
	for _, want := range []string{"Not sent", "upload failed: 413", "Continue sending", "Cancel send"} {
		if !tt.HasText(want) {
			t.Errorf("failed: missing %q in %q", want, tt.Texts())
		}
	}
	if _, ok := tt.Find(locales.SendStatusFailed()); !ok {
		t.Error("the sidebar doesn't say it was not sent")
	}
	snapFrame(t, tt, "pending-send-failed")

	if err := tt.Click("Continue sending"); err != nil {
		t.Fatal(err)
	}
	// What was uploaded stays: only the file goes again.
	r.upload("notes.txt").done <- nil
	turn := r.write()
	if turn.ID != held.id || turn.Text != "Here are the logs" || len(turn.Attachments) != 2 ||
		turn.Attachments[0]["imageId"] != "img-shot.png" || turn.Attachments[1]["fileId"] != "file-notes.txt" {
		t.Fatalf("wrote %+v", turn)
	}
	r.pump("the turn", func() bool { return landed(a.view, held.id) })
	tt.Frame()
	if tt.HasText("Waiting to send") || tt.HasText("Cancel send") {
		t.Errorf("the held message stays beside the written one: %q", tt.Texts())
	}
	if !a.sendingShown(a.view.Entries[len(a.view.Entries)-1]) {
		t.Error("the written message isn't on its way while its write goes")
	}
	r.release <- nil
	r.pump("the send", func() bool { return len(a.pending.list) == 0 })
	tt.Frame()
	if s := a.chatMotion.sent; s.id != held.id {
		t.Fatalf("the anchor left the message: %+v", s)
	}
	if _, ok := tt.Find(locales.SendStatusSendingWithoutSize()); ok {
		t.Error("the sidebar still shows a send")
	}
}

// Sent while the agent works, a message with attachments is held in the
// queue sheet, not the conversation: numbered after the queued ones, its
// first three attachments and how many more, its text muted, and its
// upload's progress; folded, the sheet says it prepares, or that it was
// not sent. The real queue item takes its place as it comes.
func TestPendingSendInQueue(t *testing.T) {
	r := newSendRig(t, true)
	a := r.a
	a.view.Queue = []session.Entry{{ID: "q1", Role: "user", Status: "queued", Queued: true, Items: []session.Item{{ID: "t", Type: "text", Text: "Already queued"}}}}
	a.draft = "Check these five"
	for i := range 5 {
		f := pendingImageFile
		f.Path, f.Name = f.Path+string(rune('a'+i)), string(rune('a'+i))+".png"
		a.attachments = append(a.attachments, f)
	}
	a.send()
	if len(a.pending.list) != 1 || !a.pending.list[0].queue || !a.pending.list[0].turn.Queue {
		t.Fatalf("not bound for the queue: %+v", a.pending.list)
	}
	held := a.pending.list[0]
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	tt.Frame()
	first := r.upload("a.png")
	r.progress(first, 1024, 1024)
	tt.Frame()
	for _, want := range []string{"Up next", "2 queued", "Already queued", "Check these five", "+2", "Uploading · 20%"} {
		if !tt.HasText(want) {
			t.Errorf("queue: missing %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("Waiting to send") {
		t.Error("a message for the queue shows in the conversation")
	}
	if _, ok := tt.Find("Cancel send"); !ok {
		t.Error("no cancel")
	}
	snapFrame(t, tt, "pending-send-queue")

	if err := tt.Click("Up next"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("1 preparing") {
		t.Errorf("folded: %q", tt.Texts())
	}
	// The rest still go after one failed.
	first.done <- errors.New("network is down")
	for _, name := range []string{"b.png", "c.png", "d.png", "e.png"} {
		r.upload(name).done <- nil
	}
	r.pump("the failure", func() bool { return held.err != "" })
	tt.Frame()
	if !tt.HasText("1 not sent") {
		t.Errorf("folded, failed: %q", tt.Texts())
	}
	if err := tt.Click("Up next"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Continue sending"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	r.upload("a.png").done <- nil
	turn := r.write()
	if !turn.Queue || len(turn.Attachments) != 5 {
		t.Fatalf("wrote %+v", turn)
	}
	r.pump("the queue item", func() bool { return len(a.view.Queue) == 2 })
	tt.Frame()
	if !tt.HasText("2 queued") || tt.HasText("Continue sending") || tt.HasText("Uploading") {
		t.Errorf("the real item and the held row show together: %q", tt.Texts())
	}
	r.release <- nil
	r.pump("the send", func() bool { return len(a.pending.list) == 0 })
}

// A conversation's held messages go in the order sent: a message sent
// behind one held is held too, though it has nothing to upload, and waits
// while the one before fails; canceling that one sends it.
func TestPendingSendsInOrder(t *testing.T) {
	r := newSendRig(t, false)
	a := r.a
	a.draft, a.attachments = "With a file", []pendingFile{pendingTextFile}
	a.send()
	a.draft = "Right behind it"
	a.send()
	if len(a.pending.list) != 2 || a.sending {
		t.Fatalf("held %d, sending %v", len(a.pending.list), a.sending)
	}
	first, second := a.pending.list[0], a.pending.list[1]
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	call := r.upload("notes.txt")
	tt.Frame()
	if !tt.HasText("Right behind it") || !tt.HasText("Waiting to send") {
		t.Errorf("the second isn't held in view: %q", tt.Texts())
	}
	call.done <- errors.New("quota exceeded")
	r.pump("the failure", func() bool { return first.err != "" })
	r.noWrite()
	if second.err != "" {
		t.Fatal("the second failed with the first")
	}
	tt.Frame()
	if err := tt.Click("Cancel send"); err != nil {
		t.Fatal(err)
	}
	turn := r.write()
	if turn.ID != second.id || turn.Text != "Right behind it" {
		t.Fatalf("wrote %+v", turn)
	}
	r.release <- nil
	r.pump("the send", func() bool { return len(a.pending.list) == 0 })
	if landed(a.view, first.id) {
		t.Fatal("the canceled message was written")
	}
}

// Canceling a message while it uploads stops the upload, and writes
// nothing, on disk either; archiving its conversation drops what it holds;
// closing the workspace stops it, though the disk keeps it for when the
// workspace opens again.
func TestPendingSendCanceled(t *testing.T) {
	r := newSendRig(t, false)
	a := r.a
	a.draft, a.attachments = "Never mind", []pendingFile{pendingTextFile}
	a.send()
	call := r.upload("notes.txt")
	if kept := keptSends(t, a); len(kept) != 1 || kept[0].Text != "Never mind" {
		t.Fatalf("kept %+v", kept)
	}
	a.cancelSend(a.pending.list[0].id)
	if len(a.pending.list) != 0 || len(keptSends(t, a)) != 0 {
		t.Fatal("still held")
	}
	r.pump("the upload to stop", func() bool { return call.ctx.Err() != nil })
	r.pump("the conversation to free", func() bool { return !a.pending.running["s1"] })
	r.noWrite()

	a.draft, a.attachments = "Again", []pendingFile{pendingTextFile}
	a.send()
	r.upload("notes.txt")
	before := a.catalog
	a.catalog.Sessions = append([]catalog.Session(nil), a.catalog.Sessions...)
	a.catalog.Sessions[0].Archived = true
	a.dropGoneSends(before, a.catalog)
	if len(a.pending.list) != 0 || len(keptSends(t, a)) != 0 {
		t.Fatal("an archived conversation keeps its held message")
	}

	a.catalog = before
	a.draft, a.attachments = "Once more", []pendingFile{pendingTextFile}
	a.send()
	r.upload("notes.txt")
	a.pending.dispose()
	if len(a.pending.list) != 0 || len(a.pending.running) != 0 {
		t.Fatal("closing the workspace keeps held messages")
	}
	r.noWrite()
	if kept := keptSends(t, a); len(kept) != 1 || kept[0].Text != "Once more" {
		t.Fatalf("closing the workspace lost the kept message: %+v", kept)
	}
}

// keptSends is what the outbox keeps for the rig's workspace.
func keptSends(t *testing.T, a *App) []outboxRecord {
	t.Helper()
	var kept []outboxRecord
	readJSON(a.outboxFile(), &kept)
	return kept
}

// Held messages are kept on disk as they go, and taken up again when the
// workspace opens after a restart: a picture uploaded is not uploaded
// again, the message goes under the same id with the run it was sent
// with, through a replica opened for its conversation when it isn't the
// one open, and the disk lets it go once written. One whose write had
// begun is let go as soon as the conversation shows it.
func TestPendingSendsDurable(t *testing.T) {
	r := newSendRig(t, false)
	a := r.a
	a.models["s1"] = "claude-opus-5-5"
	a.draft, a.attachments = "Survives a restart", []pendingFile{pendingImageFile, pendingTextFile}
	a.send()
	held := a.pending.list[0]
	if !held.persisted {
		t.Fatal("a held message isn't kept")
	}
	r.upload("shot.png").done <- nil
	notes := r.upload("notes.txt")
	r.pump("the picture kept", func() bool {
		kept := keptSends(t, a)
		return len(kept) == 1 && kept[0].Attachments[0].Ready != nil
	})
	// The app quits while the file uploads.
	a.pending.dispose()
	r.pump("the upload to stop", func() bool { return notes.ctx.Err() != nil })

	// Opened again, in another conversation, offline at first.
	a.selected, a.replica = "s2", nil
	a.syncState.Live = false
	opened, closed := map[string]bool{}, make(chan string, 1)
	open := openSendReplica
	t.Cleanup(func() { openSendReplica = open })
	openSendReplica = func(_ *App, id string) (*session.Replica, func()) {
		opened[id] = true
		return &session.Replica{ID: id}, func() { closed <- id }
	}
	a.tickSends()
	if len(a.pending.list) != 1 || a.pending.list[0].id != held.id || !a.pending.list[0].persisted {
		t.Fatalf("not taken up: %+v", a.pending.list)
	}
	r.noWrite()
	if a.unsavedWork() {
		t.Fatal("a kept message makes leaving ask")
	}
	a.syncState.Live = true
	a.tickSends()
	r.upload("notes.txt").done <- nil
	turn := r.write()
	if turn.ID != held.id || turn.Text != "Survives a restart" || turn.ModelID != "claude-opus-5-5" || len(turn.Attachments) != 2 ||
		turn.Attachments[0]["imageId"] != "img-shot.png" || turn.MarkDispatch == nil || !opened["s1"] {
		t.Fatalf("wrote %+v, opened %v", turn, opened)
	}
	if kept := keptSends(t, a); len(kept) != 1 || !kept[0].Writing {
		t.Fatalf("the write isn't kept as begun: %+v", kept)
	}
	r.release <- nil
	r.pump("the send", func() bool { return len(a.pending.list) == 0 })
	if len(keptSends(t, a)) != 0 {
		t.Fatal("the disk keeps a message written")
	}
	if id := <-closed; id != "s1" {
		t.Fatalf("closed %q", id)
	}

	// One whose write had begun goes once the conversation shows it.
	writeOutbox(a.outboxFile(), []outboxRecord{{ID: "w1", SessionID: "s1", Text: "Landed", Writing: true}})
	a.pending.dispose()
	a.catalog.Sessions[0].LatestUserMsgID = "w1"
	a.tickSends()
	if len(a.pending.list) != 0 || len(keptSends(t, a)) != 0 {
		t.Fatalf("a written message stays: %+v", a.pending.list)
	}
	r.noWrite()
}

// Editing the last message, its attachments show above the editor, each
// removable, and Add attachment adds files: sent, the files added upload
// to the conversation first, then the edit keeps the attachments left by
// their ids and adds the uploaded blocks, as Lody's editSession. A file
// that fails keeps the editor open; sending again uploads only what did
// not go.
func TestEditWithAttachments(t *testing.T) {
	r := newSendRig(t, false)
	a := r.a
	a.view.Entries = a.view.Entries[:3]
	a.view.Entries[1].AcpTurnID = "t1"
	a.catalog.Sessions[0].AgentConfigID = "a1"
	a.catalog.Capabilities = map[string]catalog.Capability{"a1": {Fork: true}}
	u := &a.view.Entries[2]
	u.Items = append(u.Items, session.Item{ID: "i", Type: "image", ImageID: "img1", FileName: "before.png"},
		session.Item{ID: "f", Type: "file", FileID: "file1", FileName: "spec.pdf", SizeBytes: 2048, Transport: "r2"})
	u.Config = map[string]any{"modelId": "m", "inputBlocks": []any{map[string]any{"type": "text", "text": u.Text()},
		map[string]any{"type": "image", "imageId": "img1", "fileName": "before.png"},
		map[string]any{"type": "file", "fileId": "file1", "fileName": "spec.pdf", "sizeBytes": float64(2048)}}}
	var edits []session.Edit
	resend := editResend
	t.Cleanup(func() { editResend = resend })
	editResend = func(_ context.Context, _ *session.Replica, _, _, _, _ string, e session.Entry, edit session.Edit) error {
		if e.ID != "u2" {
			t.Errorf("edited %q", e.ID)
		}
		edits = append(edits, edit)
		return nil
	}

	a.startEdit(*u)
	dir := t.TempDir()
	logs := dir + "/build.log"
	if err := os.WriteFile(logs, []byte("FAIL ./internal/app"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.addEditFiles([]string{logs})
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	for _, want := range []string{"before.png", "spec.pdf", "build.log", "Remove before.png", "Remove spec.pdf", "Add attachment", "Send"} {
		if _, ok := tt.Find(want); !ok {
			t.Errorf("editor: missing %q in %q", want, tt.Texts())
		}
	}
	snapFrame(t, tt, "edit-attachments")
	if err := tt.Click("Remove before.png"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.msgEdit.attachments) != 2 || a.msgEdit.attachments[0].key() != "original:1" {
		t.Fatalf("after removing: %+v", a.msgEdit.attachments)
	}

	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	call := r.upload("build.log")
	r.progress2(call, 10, 20)
	tt.Frame()
	if err := tt.Click("Remove spec.pdf"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tt.Find("Remove build.log"); ok || len(a.msgEdit.attachments) != 2 || !a.msgEdit.busy {
		t.Error("an attachment can be removed while the edit goes")
	}
	call.done <- errors.New("upload failed: 500")
	r.pump("the failure", func() bool { return !a.msgEdit.busy })
	if a.msgEdit.id != "u2" || !strings.Contains(a.sendError, "build.log: upload failed: 500") || len(edits) != 0 {
		t.Fatalf("after a failed upload: %q, %d edits", a.sendError, len(edits))
	}

	a.msgEdit.text = "Look at the log instead"
	tt.Frame()
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	r.upload("build.log").done <- nil
	r.pump("the edit", func() bool { return a.msgEdit.id == "" })
	if len(edits) != 1 {
		t.Fatalf("%d edits", len(edits))
	}
	e := edits[0]
	if e.Text != "Look at the log instead" || len(e.Retained) != 1 || e.Retained[0] != "original:1" || len(e.Added) != 1 || e.Added[0]["fileId"] != "file-build.log" {
		t.Fatalf("edit %+v", e)
	}
}

// progress2 reports how far an edit's upload has gone and waits for the
// editor to hear it.
func (r *sendRig) progress2(call uploadCall, sent, total int64) {
	r.t.Helper()
	call.progress(sent, total)
	want := float32(sent) * 100 / float32(total)
	r.pump("the progress", func() bool {
		for path, pct := range r.a.msgEdit.progress {
			if strings.HasSuffix(path, call.name) && pct == want {
				return true
			}
		}
		return false
	})
}

// Offline, a held message waits rather than failing, and goes once the
// workspace is back.
func TestPendingSendOffline(t *testing.T) {
	r := newSendRig(t, false)
	a := r.a
	a.syncState.Live = false
	a.draft, a.attachments = "Sent on a train", []pendingFile{pendingTextFile}
	a.send()
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	if !tt.HasText("Waiting to send") || tt.HasText("Not sent") {
		t.Fatalf("offline: %q", tt.Texts())
	}
	r.noWrite()
	a.syncState.Live = true
	tt.Frame()
	call := r.upload("notes.txt")
	// The connection drops mid-upload: it waits again.
	a.syncState.Live = false
	call.done <- errors.New("network is unreachable")
	held := a.pending.list[0]
	r.pump("the attempt", func() bool { return !a.pending.running["s1"] })
	if held.err != "" || held.attachments[0].err != "" {
		t.Fatalf("failed offline: %q", held.err)
	}
	a.syncState.Live = true
	tt.Frame()
	r.upload("notes.txt").done <- nil
	if turn := r.write(); turn.ID != held.id {
		t.Fatalf("wrote %+v", turn)
	}
	r.release <- nil
	r.pump("the send", func() bool { return len(a.pending.list) == 0 })
}

// A message held while its attachments upload is the message just sent:
// it glides to 100px below the top of the conversation, as Lody anchors
// the row sent, and the written message, taking its place, stays there.
func TestPendingSendAnchored(t *testing.T) {
	r := newSendRig(t, false)
	a := r.a
	var long []session.Entry
	for i := range 12 {
		id := fmt.Sprint("pad", i)
		long = append(long, session.Entry{ID: "u" + id, Role: "user", Items: []session.Item{{ID: "t", Type: "text", Text: "Question " + id}}},
			session.Entry{ID: "r" + id, Role: "assistant", UserTurnID: "u" + id, Finished: true, Items: []session.Item{{ID: "x", Type: "text", Text: strings.Repeat("A long answer line. ", 40)}}})
	}
	a.view.Entries = append(long, a.view.Entries...)
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()

	a.draft, a.attachments = "Brand new question", []pendingFile{pendingTextFile}
	a.send()
	held := a.pending.list[0]
	tt.Frame()
	tt.Frame()
	call := r.upload("notes.txt")
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	tt.Frame()
	s := &a.chatMotion.sent
	target := a.outline.top + chatPadTop + sentContext
	if !s.held || s.id != held.id || abs32(s.top-target) > 1 {
		t.Fatalf("the held message isn't held at %v: %+v", target, *s)
	}
	if s.room <= 0 || a.chatList.FollowEnd {
		t.Fatalf("no room under it, or following: room %v follow %v", s.room, a.chatList.FollowEnd)
	}
	snapFrame(t, tt, "pending-send-anchored")

	call.done <- nil
	r.write()
	r.pump("the turn", func() bool { return landed(a.view, held.id) })
	for range 4 {
		tt.Frame()
	}
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	tt.Frame()
	if !s.held || s.id != held.id || abs32(s.top-target) > 1 {
		t.Fatalf("the written message moved from %v: %+v", target, *s)
	}
	r.release <- nil
	r.pump("the send", func() bool { return len(a.pending.list) == 0 })
}

// Leaving asks first while a held message is not kept on disk, or a file
// has unsaved edits, as Lody's unload sheet.
func TestUnsavedWork(t *testing.T) {
	a := fixture()
	if a.unsavedWork() {
		t.Fatal("nothing to lose, yet it would ask")
	}
	a.pending.list = []*pendingSend{{id: "held", sessionID: "s1"}}
	if !a.unsavedWork() {
		t.Fatal("a held message not kept doesn't make it ask")
	}
	a.pending.list[0].persisted = true
	if a.unsavedWork() {
		t.Fatal("a kept message makes it ask")
	}
	a.pending.list = nil
	in := a.inspectFor("s1")
	buf := editor.NewBuffer("package main\n")
	in.preview = &filePreview{path: "main.go", editing: true, code: &editor.State{Buffer: buf}}
	if a.unsavedWork() {
		t.Fatal("an untouched file makes it ask")
	}
	buf.Set("package other\n")
	if !a.unsavedWork() {
		t.Fatal("unsaved edits don't make it ask")
	}
}

// A new conversation's first message, canceled before anything was written
// there, takes the conversation with it, as Lody never writes it; once
// something was, it stays.
func TestFreshSendCanceled(t *testing.T) {
	a := fixture()
	a.selected, a.view = "new1", &session.View{}
	a.pending.list = []*pendingSend{{id: "p1", sessionID: "new1", fresh: true}}
	a.cancelSend("p1")
	if a.selected != "" || len(a.pending.list) != 0 {
		t.Fatalf("the empty conversation stays: selected %q", a.selected)
	}

	a.selected = "new2"
	a.view = &session.View{Entries: []session.Entry{{ID: "u1", Role: "user"}}}
	a.pending.list = []*pendingSend{{id: "p2", sessionID: "new2", fresh: true}}
	a.cancelSend("p2")
	if a.selected != "new2" {
		t.Fatal("a conversation with something written was taken away")
	}
}
