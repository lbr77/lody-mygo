package catalog

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/internal/localplane"
	"github.com/lbr77/lody-mygo/internal/localplane/localplanetest"
)

// bundle is a Flock holding rows, as the JSON bundle a join returns.
func bundle(t *testing.T, rows ...flock.Row) (*flock.Flock, *localplane.Payload) {
	t.Helper()
	f, err := flock.New("daemon")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if err := f.Put(r.Key, r.Value); err != nil {
			t.Fatal(err)
		}
	}
	b, err := f.ExportJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b)
	return f, &localplane.Payload{Kind: "flock-json", Bundle: raw}
}

// In local-only mode the catalog syncs with the daemon's rooms: the meta
// room, then each machine's Flock, and writes go back as Flock bundles.
func TestSyncLocal(t *testing.T) {
	meta, metaJoin := bundle(t,
		row(true, "e", "machine-m1"),
		row(map[string]any{"name": "Studio"}, "m", "machine-m1"),
		row(true, "e", "session-s1"),
		row(map[string]any{"id": "s1", "machineId": "m1", "title": "Offline chat", "createdAt": "2026-10-29T00:00:00Z"}, "m", "session-s1"),
	)
	_, machineJoin := bundle(t, row(map[string]any{"id": "a1", "name": "Claude Code", "agentType": "claude", "cliType": "builtin"}, "agentConfig", "a1"))
	metaRoom := localplane.Room{Scope: "meta"}
	machineRoom := localplane.FlockRoom("ws:mf:m1")
	d := localplanetest.Start(t)
	d.OnJoin = func(r localplane.Room) *localplane.Payload {
		switch r {
		case metaRoom:
			return metaJoin
		case machineRoom:
			return machineJoin
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lc, err := localplane.Dial(ctx, d.Path, "ws")
	if err != nil {
		t.Fatal(err)
	}
	defer lc.Close()

	var mu sync.Mutex
	var last Catalog
	s := &Sync{Local: lc, OnChange: func(c Catalog) {
		mu.Lock()
		last = c
		mu.Unlock()
	}}
	s.Start()
	defer s.Stop()
	current := func() Catalog {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
	wait := func(what string, ok func(Catalog) bool) {
		t.Helper()
		for !ok(current()) {
			if ctx.Err() != nil {
				t.Fatalf("timed out waiting for %s: %+v", what, current())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	wait("the catalog", func(c Catalog) bool {
		return c.Session("s1") != nil && len(c.Agents["m1"]) == 1 && c.MachineNames["m1"] == "Studio"
	})
	if !d.Joined(localplane.FlockRoom("ws:wf:workspace")) {
		t.Fatal("the workspace Flock wasn't joined")
	}

	// The daemon renames the chat.
	before, _ := meta.Version()
	meta.Put([]any{"m", "session-s1", "title"}, "Renamed offline")
	delta, _ := meta.ExportJSON(before)
	raw, _ := json.Marshal(delta)
	d.Push(metaRoom, localplane.Payload{Kind: "flock-json", Bundle: raw})
	wait("the rename", func(c Catalog) bool { return c.Session("s1").Title == "Renamed offline" })

	// Pinning it is a bundle the daemon can import, with the version after.
	if err := s.Pin(ctx, "s1", true); err != nil {
		t.Fatal(err)
	}
	ups := d.Updates(metaRoom)
	if len(ups) != 1 || ups[0].Payload.Kind != "flock-json" || !strings.Contains(ups[0].HaveVersion, `"physicalTime"`) {
		t.Fatalf("updates %+v", ups)
	}
	var in map[string]any
	if err := json.Unmarshal(ups[0].Payload.Bundle, &in); err != nil {
		t.Fatal(err)
	}
	if err := meta.ImportJSON(in); err != nil {
		t.Fatal(err)
	}
	rows, _ := meta.Scan()
	if c := projectMeta(rows); !c.Session("s1").Pinned {
		t.Fatal("the daemon didn't get the pin")
	}
}
