package session

import (
	"testing"
	"time"

	loro "github.com/aholstenson/loro-go"
)

func TestAppendAndProject(t *testing.T) {
	r := &Replica{doc: loro.NewLoroDoc()}
	cfg := inputConfig(nil, Turn{CLIType: "builtin", AgentType: "claude", ModelID: "opus"}, "hello")
	if err := r.appendUserTurn("u1", "user-1", "2026-10-29T00:00:00.000Z", cfg, "pending"); err != nil {
		t.Fatal(err)
	}

	// An assistant reply streamed in by the machine, as another peer.
	other := loro.NewLoroDoc()
	if _, err := other.Import(mustExport(t, r.doc)); err != nil {
		t.Fatal(err)
	}
	history := other.GetList(loro.AsContainerId("history"))
	reply, _ := history.InsertLoroMap(history.Len())
	reply.InsertAny("id", "a1")
	reply.InsertAny("role", "assistant")
	reply.InsertAny("userTurnId", "u1")
	reply.InsertAny("finished", false)
	items, _ := reply.GetOrCreateLoroList("items")
	text, _ := items.InsertLoroMap(0)
	text.InsertAny("type", "text")
	body, _ := text.GetOrCreateLoroText("text")
	body.Insert(0, "Hi there")
	tool, _ := items.InsertLoroMap(1)
	tool.InsertAny("type", "tool_call")
	tool.InsertAny("toolCallId", "tc1")
	tool.InsertAny("kind", "edit")
	tool.InsertAny("title", "Edit main.go")
	tool.InsertAny("content", []any{map[string]any{"type": "diff", "path": "main.go", "oldText": "a\nb", "newText": "a\nc\nd"}})
	tool.InsertAny("permissionRequest", map[string]any{"requestId": "r1", "options": []any{map[string]any{"optionId": "allow", "name": "Allow", "kind": "allow_once"}}})
	other.Commit()
	if _, err := r.doc.Import(mustExport(t, other)); err != nil {
		t.Fatal(err)
	}

	v := project(r.doc, "live", "")
	if len(v.Entries) != 2 || !v.Working {
		t.Fatalf("view = %+v", v)
	}
	user, assistant := v.Entries[0], v.Entries[1]
	if user.Role != "user" || user.Text() != "hello" || v.ModelID != "opus" {
		t.Fatalf("user = %+v", user)
	}
	if assistant.Text() != "Hi there" || len(assistant.Items) != 2 {
		t.Fatalf("assistant = %+v", assistant)
	}
	call := assistant.Items[1]
	if call.ID != "tc1" || call.Path != "main.go" || call.Added != 2 || call.Removed != 1 || call.Permission == nil || !call.Permission.Pending {
		t.Fatalf("tool call = %+v", call)
	}

	before := r.doc.OplogVv()
	r.mu.Lock()
	err := r.pushQueued("u2", "next", "user-1", "2026-10-29T00:01:00.000Z", inputConfig(nil, Turn{CLIType: "builtin", AgentType: "claude"}, "next"))
	r.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if update, err := r.doc.Export(loro.UpdatesMode(before)); err != nil || len(update) == 0 {
		t.Fatalf("update %d bytes, %v", len(update), err)
	}
	v = project(r.doc, "live", "")
	if last := v.Entries[len(v.Entries)-1]; !last.Queued || last.Text() != "next" {
		t.Fatalf("queued = %+v", last)
	}
}

func mustExport(t *testing.T, doc *loro.LoroDoc) []byte {
	t.Helper()
	b, err := doc.Export(loro.SnapshotMode())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBusy(t *testing.T) {
	h := []any{
		map[string]any{"role": "user", "status": "pending"},
	}
	if !busy(h) {
		t.Fatal("pending turn without reply should be busy")
	}
	h = append(h, map[string]any{"role": "assistant", "finished": true})
	if busy(h) {
		t.Fatal("answered turn should not be busy")
	}
}

func TestThoughtTiming(t *testing.T) {
	r := &Replica{ready: true}
	view := func(text string, finished bool) *View {
		return &View{Entries: []Entry{{ID: "a1", Role: "assistant", Finished: finished, Items: []Item{{ID: "t1", Type: "thought", Text: text}}}}}
	}
	t0 := time.Unix(1000, 0)
	r.timeThoughts(view("Let", false), t0)
	r.timeThoughts(view("Let me think", false), t0.Add(3*time.Second))
	r.timeThoughts(view("Let me think", false), t0.Add(9*time.Second)) // no new text: still 3s
	v := view("Let me think", true)
	r.timeThoughts(v, t0.Add(20*time.Second))
	if got := v.Entries[0].Items[0].ThoughtFor; got != 3*time.Second {
		t.Fatalf("ThoughtFor = %v", got)
	}
	// A thought already finished when the replica caught up has no timing.
	old := &View{Entries: []Entry{{ID: "a0", Role: "assistant", Finished: true, Items: []Item{{ID: "t0", Type: "thought", Text: "x"}}}}}
	r.timeThoughts(old, t0)
	if old.Entries[0].Items[0].ThoughtFor != 0 {
		t.Fatal("timed a thought it never saw stream")
	}
}
