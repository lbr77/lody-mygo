package session

import (
	"context"
	"testing"
	"time"

	loro "github.com/aholstenson/loro-go"

	"github.com/lbr77/lody-mygo/internal/localplane"
	"github.com/lbr77/lody-mygo/internal/localplane/localplanetest"
)

// A replica in local-only mode follows the session through the daemon:
// it takes the document it joins with, what the daemon pushes, and sends
// its own changes back as updates.
func TestFollowLocal(t *testing.T) {
	// The daemon's copy: the user's turn.
	daemon := loro.NewLoroDoc()
	seed := &Replica{doc: daemon}
	cfg := inputConfig(nil, Turn{CLIType: "builtin", AgentType: "claude"}, "hello")
	if err := seed.appendUserTurn("u1", "user-1", "2026-10-29T00:00:00.000Z", cfg, "processing"); err != nil {
		t.Fatal(err)
	}
	room := localplane.DocRoom("session-sid")
	d := localplanetest.Start(t)
	d.OnJoin = func(r localplane.Room) *localplane.Payload {
		if r != room {
			return nil
		}
		p := localplane.DocUpdate(mustExport(t, daemon))
		return &p
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lc, err := localplane.Dial(ctx, d.Path, "ws")
	if err != nil {
		t.Fatal(err)
	}
	defer lc.Close()

	views := make(chan *View, 16)
	r := OpenLocal(nil, lc, "sid", func(v *View) { views <- v })
	defer r.Close()
	if err := r.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if v := r.View(); len(v.Entries) != 1 || v.Entries[0].Text() != "hello" || v.Status != "live" {
		t.Fatalf("joined view %+v", v)
	}

	// The agent's reply, as the daemon pushes it.
	before := daemon.OplogVv()
	history := daemon.GetList(loro.AsContainerId("history"))
	reply, _ := history.InsertLoroMap(history.Len())
	reply.InsertAny("id", "a1")
	reply.InsertAny("role", "assistant")
	reply.InsertAny("userTurnId", "u1")
	reply.InsertAny("finished", true)
	items, _ := reply.GetOrCreateLoroList("items")
	text, _ := items.InsertLoroMap(0)
	text.InsertAny("type", "text")
	body, _ := text.GetOrCreateLoroText("text")
	body.Insert(0, "Hi from the daemon")
	daemon.Commit()
	update, err := daemon.Export(loro.UpdatesMode(before))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Push(room, localplane.DocUpdate(update)) {
		t.Fatal("no peer joined the session")
	}
	waitFor(t, "the pushed reply", func() bool {
		v := r.View()
		return len(v.Entries) == 2 && v.Entries[1].Text() == "Hi from the daemon"
	})

	// A queued message goes back to the daemon as an update.
	r.mu.Lock()
	mine := r.doc.OplogVv()
	err = r.pushQueued("u2", "next", "user-1", "2026-10-29T00:01:00.000Z", inputConfig(nil, Turn{CLIType: "builtin", AgentType: "claude"}, "next"))
	r.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.upload(ctx, mine); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the update", func() bool { return len(d.Updates(room)) == 1 })
	sent := d.Updates(room)[0]
	data, err := sent.Payload.Bytes()
	if err != nil || sent.HaveVersion == "" {
		t.Fatalf("update %+v, %v", sent, err)
	}
	if _, err := daemon.Import(data); err != nil {
		t.Fatal(err)
	}
	if v := project(daemon, "live", ""); !v.Entries[len(v.Entries)-1].Queued {
		t.Fatalf("the daemon's view %+v", v.Entries)
	}

	// The daemon going away puts the replica back to syncing.
	d.Drop()
	waitFor(t, "syncing", func() bool { return r.View().Status == "syncing" })
}

// A notice's outcome is written into its metadata and sent on.
func TestSetNoticeMeta(t *testing.T) {
	daemon := loro.NewLoroDoc()
	history := daemon.GetList(loro.AsContainerId("history"))
	reply, _ := history.InsertLoroMap(0)
	reply.InsertAny("id", "a1")
	reply.InsertAny("role", "assistant")
	items, _ := reply.GetOrCreateLoroList("items")
	n, _ := items.InsertLoroMap(0)
	n.InsertAny("type", "system_notice")
	n.InsertAny("itemId", "p1")
	n.InsertAny("name", "schedule_proposal")
	n.InsertAny("meta", map[string]any{"proposalId": "x", "title": "T"})
	daemon.Commit()
	room := localplane.DocRoom("session-sid")
	d := localplanetest.Start(t)
	d.OnJoin = func(r localplane.Room) *localplane.Payload {
		p := localplane.DocUpdate(mustExport(t, daemon))
		return &p
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lc, err := localplane.Dial(ctx, d.Path, "ws")
	if err != nil {
		t.Fatal(err)
	}
	defer lc.Close()
	r := OpenLocal(nil, lc, "sid", func(*View) {})
	defer r.Close()
	if err := r.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	item := r.View().Entries[0].Items[0]
	if err := r.SetNoticeMeta(ctx, "a1", item.ID, map[string]any{"outcome": "created", "scheduleId": "x"}); err != nil {
		t.Fatal(err)
	}
	got := r.View().Entries[0].Items[0].Notice
	if got["outcome"] != "created" || got["title"] != "T" {
		t.Fatalf("meta %+v", got)
	}
	data, _ := d.WaitUpdates(t, room, 1)[0].Payload.Bytes()
	daemon.Import(data)
	if m := project(daemon, "live", "").Entries[0].Items[0].Notice; m["scheduleId"] != "x" {
		t.Fatalf("the daemon's %+v", m)
	}
}
