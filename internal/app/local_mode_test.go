package app

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/internal/localplane"
	"github.com/lbr77/lody-mygo/internal/localplane/localplanetest"
	"github.com/lbr77/lody-mygo/locales"
)

// pumpUpdates runs the app's main-thread updates on the test's goroutine
// until ok, as the event loop would.
func pumpUpdates(t *testing.T, a *App) func(what string, ok func() bool) {
	t.Helper()
	q := make(chan func(), 256)
	a.post = func(fn func()) { q <- fn }
	return func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			select {
			case fn := <-q:
				fn()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
}

// Local-only mode syncs the workspace with this machine's daemon and asks
// it, never the cloud; it says so in the sidebar, and comes back with the
// daemon.
func TestLocalOnlyMode(t *testing.T) {
	f, err := flock.New("daemon")
	if err != nil {
		t.Fatal(err)
	}
	f.Put([]any{"e", "machine-m1"}, true)
	f.Put([]any{"m", "machine-m1"}, map[string]any{"name": "Studio"})
	f.Put([]any{"e", "session-s9"}, true)
	f.Put([]any{"m", "session-s9"}, map[string]any{"id": "s9", "machineId": "m1", "title": "Written offline", "createdAt": "2026-10-29T00:00:00Z"})
	b, _ := f.ExportJSON(nil)
	raw, _ := json.Marshal(b)
	d := localplanetest.Start(t)
	d.OnJoin = func(r localplane.Room) *localplane.Payload {
		if r.Scope == "meta" {
			return &localplane.Payload{Kind: "flock-json", Bundle: raw}
		}
		return nil
	}

	var mu sync.Mutex
	var asked []string
	defer swapLocal(func(ctx context.Context, workspace string) (*localplane.Conn, error) {
		return localplane.Dial(ctx, d.Path, workspace)
	}, func(ctx context.Context, workspace, method string, params any, timeout time.Duration) (json.RawMessage, error) {
		mu.Lock()
		asked = append(asked, workspace+" "+method)
		mu.Unlock()
		return json.RawMessage(`{"ok":true}`), nil
	})()

	a := fixture()
	a.localOnly = true
	pump := pumpUpdates(t, a)
	a.connect("w1")
	defer a.disconnect()
	pump("the local catalog", func() bool { return a.local != nil && a.catalog.Session("s9") != nil })
	if !a.presence.IsOnline("m1") || a.presence.IsOnline("m2") {
		t.Fatalf("presence %+v", a.presence)
	}

	// Machine RPCs go to the daemon's control socket.
	if _, err := a.conn.MachineRPC(context.Background(), "m1", "machine/status", map[string]any{}, time.Second); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(asked) != 1 || asked[0] != "w1 machine/status" {
		t.Fatalf("asked %q", asked)
	}
	mu.Unlock()

	// A chat follows through the daemon.
	a.show("s9")
	pump("the session joined", func() bool { return d.Joined(localplane.DocRoom("session-s9")) })

	tt := snap(t, a, "local-only", false, nil)
	if !tt.HasText(locales.LocalChip()) {
		t.Fatalf("no local chip in %q", tt.Texts())
	}

	// The daemon going away leaves what was shown, and says so.
	d.Drop()
	pump("the daemon gone", func() bool { return a.local == nil })
	if a.catalog.Session("s9") == nil || a.syncState.Reason != locales.LocalDaemonAway() || a.presence.IsOnline("m1") {
		t.Fatalf("after the drop: %+v %+v", a.syncState, a.presence)
	}
	tt.Frame()
	if !tt.HasText(locales.Offline()) {
		t.Fatalf("no offline chip in %q", tt.Texts())
	}

	// It comes back, and so does the chat.
	pump("the daemon back", func() bool { return a.local != nil && a.replica != nil })
	pump("the session rejoined", func() bool { return d.Joined(localplane.DocRoom("session-s9")) })
}

func swapLocal(dial func(context.Context, string) (*localplane.Conn, error), rpc func(context.Context, string, string, any, time.Duration) (json.RawMessage, error)) func() {
	oldDial, oldRPC, oldMachine, oldRedial := dialLocalplane, localRPC, localMachineID, localRedial
	dialLocalplane, localRPC, localMachineID, localRedial = dial, rpc, func() string { return "m1" }, 20*time.Millisecond
	return func() { dialLocalplane, localRPC, localMachineID, localRedial = oldDial, oldRPC, oldMachine, oldRedial }
}

func TestLocalOnlyPreference(t *testing.T) {
	a := fixture()
	tt := snap(t, a, "prefs-local-only", false, func(tt *ui.Tester) {
		tt.Frame()
		if err := tt.Click("Settings"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if err := tt.Click("Preferences"); err != nil {
			t.Fatal(err)
		}
	})
	if !tt.HasText(locales.PrefLocalOnly()) || !tt.HasText(locales.PrefLocalOnlyHelp()) {
		t.Fatalf("preferences: %q", tt.Texts())
	}
}
