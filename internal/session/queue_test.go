package session

import (
	"context"
	"errors"
	"testing"

	loro "github.com/aholstenson/loro-go"
)

// queueOf is the texts of the queue's messages, in order.
func queueOf(v *View) []string {
	var texts []string
	for _, e := range v.Queue {
		texts = append(texts, e.Text())
	}
	return texts
}

// The queue is reordered, marked as edited, edited and emptied in place,
// each change uploaded, as Lody's writer does.
func TestQueueEdits(t *testing.T) {
	r, g := running(t)
	ctx := context.Background()
	image := map[string]any{"type": "image", "imageId": "img1", "mimeType": "image/png", "sizeBytes": float64(10)}
	for _, text := range []string{"first", "second", "third"} {
		turn := Turn{Text: text, UserID: "user-1", MachineID: "m1", AgentType: "claude", CLIType: "builtin",
			MarkDispatch: func(context.Context, string, string, bool) error { return nil }}
		if text == "second" {
			turn.Attachments = []map[string]any{image}
		}
		if state, err := r.Send(ctx, turn); err != nil || state != Queued {
			t.Fatalf("Send(%q) = %q, %v", text, state, err)
		}
	}
	v := r.View()
	if got := queueOf(v); len(got) != 3 || got[0] != "first" || got[2] != "third" {
		t.Fatalf("queue = %q", got)
	}
	ids := []string{v.Queue[0].ID, v.Queue[1].ID, v.Queue[2].ID}

	// Dropped on the first, the third goes before it.
	appends := func() int { g.mu.Lock(); defer g.mu.Unlock(); return g.appends }
	uploads := appends()
	if err := r.MoveQueued(ctx, ids[2], ids[0]); err != nil {
		t.Fatal(err)
	}
	if got := queueOf(r.View()); got[0] != "third" || got[1] != "first" || got[2] != "second" {
		t.Fatalf("moved = %q", got)
	}
	if appends() == uploads {
		t.Fatal("the move wasn't uploaded")
	}

	// Editing holds it, with a lease.
	if err := r.EditingQueued(ctx, ids[1], true); err != nil {
		t.Fatal(err)
	}
	if e := userEntry(r.View(), ids[1]); e == nil || !e.Editing {
		t.Fatalf("editing = %+v", e)
	}
	mq := r.doc.GetMovableList(loro.AsContainerId("mq"))
	item := obj(list(deepValue(mq.GetDeepValue()))[queueIndex(mq, ids[1])])
	if item["isEditing"] != true || item["editingStartedAt"] == nil {
		t.Fatalf("item = %v", item)
	}

	// The new text leads the image it kept, and the editing ends.
	if err := r.EditQueued(ctx, ids[1], "second, edited", "[Image message]"); err != nil {
		t.Fatal(err)
	}
	e := userEntry(r.View(), ids[1])
	if e == nil || e.Editing || e.Text() != "second, edited" || len(e.Items) != 2 || e.Items[1].ImageID != "img1" {
		t.Fatalf("edited = %+v", e)
	}
	item = obj(list(deepValue(mq.GetDeepValue()))[queueIndex(mq, ids[1])])
	cfg := obj(item["acpSessionConfig"])
	if item["task"] != "second, edited" || item["isEditing"] != false || item["editingStartedAt"] != nil || cfg["prompt"] != "second, edited" {
		t.Fatalf("item = %v", item)
	}

	// Left with only its image, it says so in the queue.
	if err := r.EditQueued(ctx, ids[1], "", "[Image message]"); err != nil {
		t.Fatal(err)
	}
	item = obj(list(deepValue(mq.GetDeepValue()))[queueIndex(mq, ids[1])])
	if blocks := list(obj(item["acpSessionConfig"])["inputBlocks"]); item["task"] != "[Image message]" || len(blocks) != 1 || str(obj(blocks[0])["type"]) != "image" {
		t.Fatalf("image only = %v", item)
	}

	// Removed, it is gone unsent; a message no longer queued is an error.
	if err := r.RemoveQueued(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if got := queueOf(r.View()); len(got) != 2 || got[0] != "third" {
		t.Fatalf("removed = %q", got)
	}
	if err := r.RemoveQueued(ctx, ids[0]); !errors.Is(err, errNotQueued) {
		t.Fatalf("removing again = %v", err)
	}
	if m := g.methods(); len(m) != 0 {
		t.Fatalf("machine calls = %v", m)
	}
}

// A send whose turn id is in the conversation or its queue already writes
// nothing, as a held message sent again after a restart must not repeat.
func TestSendNeverRepeatsATurn(t *testing.T) {
	r, g := running(t)
	ctx := context.Background()
	turn := Turn{ID: "q-1", Text: "queued once", UserID: "user-1", MachineID: "m1", AgentType: "claude", CLIType: "builtin",
		MarkDispatch: func(context.Context, string, string, bool) error { return nil }}
	if state, err := r.Send(ctx, turn); err != nil || state != Queued {
		t.Fatalf("Send = %q, %v", state, err)
	}
	g.mu.Lock()
	uploads := g.appends
	g.mu.Unlock()
	for _, id := range []string{"q-1", "u1"} {
		turn.ID = id
		if state, err := r.Send(ctx, turn); !errors.Is(err, ErrTurnExists) || state != Unknown {
			t.Fatalf("Send(%s) again = %q, %v", id, state, err)
		}
	}
	if got := queueOf(r.View()); len(got) != 1 {
		t.Fatalf("queue = %q", got)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.appends != uploads {
		t.Fatal("a repeat was uploaded")
	}
}
