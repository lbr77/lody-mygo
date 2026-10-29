package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/session"
)

// fixture is a signed-in app with a catalog and an open session, without
// any network.
func fixture() *App {
	a := New()
	a.phase = phaseSignedIn
	a.account = &cloud.Account{
		User:       cloud.User{ID: "u1", Name: "Ada Lovelace", Email: "ada@example.com"},
		Workspaces: []cloud.Workspace{{ID: "w1", Name: "Analytical"}, {ID: "w2", Name: "Personal"}},
	}
	a.workspaceID = "w1"
	a.synced = true
	a.syncState = catalog.State{Live: true}
	a.catalog = catalog.Catalog{
		MachineIDs:   []string{"m1"},
		MachineNames: map[string]string{"m1": "Studio"},
		Agents:       map[string][]catalog.AgentConfig{"m1": {{ID: "a1", Name: "Claude Code", MachineID: "m1", AgentType: "claude", CLIType: "builtin"}}},
		Projects: []catalog.Project{
			{ID: "m1:local:p1", MachineID: "m1", Name: "lody-mygo", RootPath: "/src/lody-mygo"},
			{ID: "m1:local:p2", MachineID: "m1", Name: "website", RootPath: "/src/website"},
		},
		Sessions: []catalog.Session{
			{ID: "s1", MachineID: "m1", Title: "Port the Flock runtime to Go", Status: "running", ProjectID: "m1:local:p1", AgentType: "claude", CLIType: "builtin"},
			{ID: "s2", MachineID: "m1", Title: "Sidebar status marks", Status: "idle", ProjectID: "m1:local:p1", LastMessageAt: 10, LastReadAt: 1},
			{ID: "s3", MachineID: "m1", Title: "Fix the flaky upload test", Status: "requestPermission", ProjectID: "m1:local:p2"},
			{ID: "s4", MachineID: "m1", Title: "What is a CRDT?", Status: "idle", ProjectID: "m1:unassigned", Pinned: true},
		},
	}
	a.selected = "s1"
	a.view = &session.View{
		Status:  "live",
		Working: true,
		ModelID: "claude-opus-5-5",
		Entries: []session.Entry{
			{ID: "u1", Role: "user", Status: "handled", Timestamp: "2026-10-29T09:12:00Z", Items: []session.Item{{ID: "t", Type: "text", Text: "Port the Flock catalog runtime from the iOS client to Go, without a web view."}}},
			{ID: "r1", Role: "assistant", UserTurnID: "u1", Finished: true, StartedAt: 1000, EndedAt: 73000, Model: "Opus 5.5", Items: []session.Item{
				{ID: "th", Type: "thought", Text: "The snapshot format is only in the wasm build."},
				{ID: "c1", Type: "tool_call", Kind: "read", Title: "packages/cloud-runtime/src/runtime/index.ts", Status: "completed"},
				{ID: "c2", Type: "tool_call", Kind: "execute", Title: "go test ./internal/flock", Status: "completed"},
				{ID: "x", Type: "text", Text: "Flock now runs in **wazero**: the wasm build of flock-rs, behind a Go shim of the imports wasm-bindgen expects.\n\n```go\nf, _ := flock.FromFile(peer, snapshot)\nrows, _ := f.Scan()\n```\n\n- Snapshots load with `FromFile`\n- Deltas apply with `ImportJSON`"},
				{ID: "c3", Type: "tool_call", Kind: "edit", Title: "internal/catalog/sync.go", Status: "completed", Path: "internal/catalog/sync.go", Added: 120, Removed: 4},
			}, FileDiffs: []session.FileDiff{{Path: "internal/catalog/sync.go", Add: 120, Del: 4}}},
			{ID: "u2", Role: "user", Status: "handled", Timestamp: "2026-10-29T09:20:00Z", Items: []session.Item{{ID: "t", Type: "text", Text: "Now run it against the real stream."}}},
			{ID: "r2", Role: "assistant", UserTurnID: "u2", Items: []session.Item{
				{ID: "c4", Type: "tool_call", Kind: "execute", Title: "go run . --workspace w1", Status: "in_progress"},
				{ID: "c5", Type: "tool_call", Kind: "execute", Title: "Run rm -rf build/", Status: "pending", Permission: &session.Permission{RequestID: "r", Pending: true, Options: []session.Option{{ID: "allow", Name: "Allow once", Kind: "allow_once"}, {ID: "always", Name: "Always allow", Kind: "allow_always"}, {ID: "reject", Name: "Reject", Kind: "reject_once"}}}},
			}},
		},
	}
	return a
}

// snap renders the view and saves it when LODY_SNAPSHOTS names a directory.
func snap(t *testing.T, a *App, name string, dark bool, setup func(tt *ui.Tester)) *ui.Tester {
	t.Helper()
	tt := ui.NewTester(a.render, 1100, 720)
	tt.SetDark(dark)
	tt.SetTitleBar(ui.TitleBar{Height: 44, Left: 80})
	if setup != nil {
		setup(tt)
	}
	tt.Frame()
	if dir := os.Getenv("LODY_SNAPSHOTS"); dir != "" {
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	return tt
}

func TestSignIn(t *testing.T) {
	a := New()
	a.phase = phaseSignedOut
	tt := snap(t, a, "signin-light", false, nil)
	if !tt.HasText("Welcome to Lody") || !tt.HasText("Open browser to sign in") {
		t.Fatalf("texts: %q", tt.Texts())
	}
	a.signIn.busy, a.signIn.code = true, "WXYZ-1234"
	tt = snap(t, a, "signin-code-dark", true, nil)
	if !tt.HasText("WXYZ-1234") {
		t.Fatalf("texts: %q", tt.Texts())
	}
}

func TestSessionView(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "session-light"
		if dark {
			name = "session-dark"
		}
		a := fixture()
		tt := snap(t, a, name, dark, nil)
		for _, want := range []string{"Port the Flock runtime to Go", "lody-mygo", "Pinned", "Allow once", "Now run it against the real stream."} {
			if !tt.HasText(want) {
				t.Errorf("%s: missing %q in %q", name, want, tt.Texts())
			}
		}
	}
}

func TestExpandActivity(t *testing.T) {
	a := fixture()
	tt := snap(t, a, "session-expanded", false, func(tt *ui.Tester) {
		tt.Frame()
		// The visible group: the reply in progress.
		if err := tt.Click("Ran 1 command"); err != nil {
			t.Fatal(err)
		}
	})
	if !tt.HasText("go run . --workspace w1") {
		t.Fatalf("steps not shown: %q", tt.Texts())
	}
}

func TestNewChat(t *testing.T) {
	a := fixture()
	a.selected, a.view = "", nil
	a.pickNewChatDefaults()
	tt := snap(t, a, "newchat-light", false, nil)
	for _, want := range []string{"What should we work on?", "Studio", "Claude Code", "No project"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
}
