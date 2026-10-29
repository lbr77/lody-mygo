package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

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
// nothing; archiving its conversation, or closing the workspace, drops
// what it holds.
func TestPendingSendCanceled(t *testing.T) {
	r := newSendRig(t, false)
	a := r.a
	a.draft, a.attachments = "Never mind", []pendingFile{pendingTextFile}
	a.send()
	call := r.upload("notes.txt")
	a.cancelSend(a.pending.list[0].id)
	if len(a.pending.list) != 0 {
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
	if len(a.pending.list) != 0 {
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
