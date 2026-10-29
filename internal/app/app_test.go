package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/egoist/mygo/i18n"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/collab"
	"github.com/lbr77/lody-mygo/internal/github"
	"github.com/lbr77/lody-mygo/internal/mention"
	"github.com/lbr77/lody-mygo/internal/presence"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/internal/shortcuts"
)

// fixture is a signed-in app with a catalog and an open session, without
// any network.
func fixture() *App {
	a := New()
	a.offline = true
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
	// Let what comes in, as a dialog fading up, settle first.
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, name)
	return tt
}

// snapFrame writes the tester's frame out when LODY_SNAPSHOTS names a
// folder.
func snapFrame(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
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
		if err := tt.Click("Ran 2 commands"); err != nil {
			t.Fatal(err)
		}
	})
	if !tt.HasText("go run . --workspace w1") {
		t.Fatalf("steps not shown: %q", tt.Texts())
	}
}

func TestThinkingCollapsed(t *testing.T) {
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	tt := snap(t, a, "thinking-collapsed", false, nil)
	if tt.HasText("Thought") {
		t.Fatal("a finished turn's work shows before it is unfolded")
	}
	if err := tt.Click("Worked for 1m 12s"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(foldTime) // the group unfolds
	// Thinking and tool calls fold into one row.
	if err := tt.Click("Thought · Ran 1 command · Read 1 file"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Advance(foldTime) // the group unfolds
	if tt.HasText("The snapshot format is only in the wasm build.") {
		t.Fatal("thinking shows before it is clicked")
	}
	if err := tt.Click("Thought"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(foldTime) // the group unfolds
	if !tt.HasText("The snapshot format is only in the wasm build.") {
		t.Fatalf("thinking not shown after the click: %q", tt.Texts())
	}
}

func TestNewChat(t *testing.T) {
	a := fixture()
	a.selected, a.view = "", nil
	a.pickNewChatDefaults()
	tt := snap(t, a, "newchat-light", false, nil)
	if !tt.HasText("What should we work on?") && !tt.HasText("Let's ship something") {
		t.Errorf("no heading in %q", tt.Texts())
	}
	for _, want := range []string{"Studio", "Claude Code", "Select a project"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestSessionMenuRename(t *testing.T) {
	a := fixture()
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	if err := tt.RightClick("Sidebar status marks"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	for _, want := range []string{"Pin Session", "Rename", "Copy Session URL", "Archive Session"} {
		if !tt.HasText(want) {
			t.Fatalf("context menu misses %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Rename"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.renaming != "s2" {
		t.Fatalf("renaming = %q", a.renaming)
	}
	tt.Type(" v2")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.renaming != "" {
		t.Fatalf("still renaming after Enter: %q", a.renameText)
	}
}

func TestToolDetail(t *testing.T) {
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	items := a.view.Entries[1].Items
	items[4].HasDetail = true
	items[4].Content = []any{map[string]any{"type": "diff", "path": "internal/catalog/sync.go", "oldText": "package catalog\n", "newText": "package catalog\n\nconst maxReplica = 8 << 20\n"}}
	items[2].HasDetail = true
	items[2].Content = []any{map[string]any{"type": "terminal", "command": "go", "args": []any{"test", "./internal/flock"}, "output": "ok  github.com/lbr77/lody-mygo/internal/flock 0.04s"}}
	tt := snap(t, a, "tool-detail", false, func(tt *ui.Tester) {
		tt.Frame()
		if err := tt.Click("Worked for 1m 12s"); err != nil {
			t.Fatal(err)
		}
		tt.Advance(foldTime) // the group unfolds
		if err := tt.Click("Edited 1 file"); err != nil {
			t.Fatal(err)
		}
		tt.Advance(foldTime) // the group unfolds
		if err := tt.Click("Edited internal/catalog/sync.go"); err != nil {
			t.Fatalf("%v: %q", err, tt.Texts())
		}
	})
	// The diff viewer paints its lines itself; its editor is labeled.
	if !tt.HasText("New text") {
		t.Fatalf("diff not shown: %q", tt.Texts())
	}
}

func TestAttachmentChips(t *testing.T) {
	a := fixture()
	dir := t.TempDir()
	img := filepath.Join(dir, "screenshot.png")
	notes := filepath.Join(dir, "notes.md")
	os.WriteFile(img, []byte("not really a png"), 0o600)
	os.WriteFile(notes, []byte("# notes"), 0o600)
	a.view.Working = false
	a.view.Entries = a.view.Entries[:2]
	a.addFiles([]string{img, notes, filepath.Join(dir, "missing.txt")})
	if len(a.attachments) != 2 || !a.attachments[0].Image || a.attachments[1].Image {
		t.Fatalf("attachments = %+v", a.attachments)
	}
	if a.sendError == "" {
		t.Fatal("a missing file should be reported")
	}
	tt := snap(t, a, "attachments", false, nil)
	for _, want := range []string{"notes.md", "Remove screenshot.png", "Add attachment"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Remove notes.md"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.attachments) != 1 {
		t.Fatalf("after removing: %+v", a.attachments)
	}
}

func TestArchivePage(t *testing.T) {
	a := fixture()
	a.catalog.Sessions = append(a.catalog.Sessions, catalog.Session{ID: "old", MachineID: "m1", Title: "Old experiment", Archived: true, ProjectID: "m1:local:p2", CreatedAt: "2026-09-01T00:00:00Z"})
	tt := snap(t, a, "archive", false, func(tt *ui.Tester) {
		tt.Frame()
		if err := tt.Click("Archived chats"); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"Old experiment", "website", "(1)", "Restore session", "Delete permanently", "By project", "Newest first"} {
		if a.page != "archive" || !tt.HasText(want) {
			t.Errorf("archive page: missing %q in %q", want, tt.Texts())
		}
	}
	// Delete asks first.
	tt.Move(700, 168)
	tt.Frame()
	if err := tt.Click("Delete permanently"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.deleteOpen || !tt.HasText("Delete permanently?") {
		t.Fatalf("no confirmation: %q", tt.Texts())
	}
}

func TestSettingsDialog(t *testing.T) {
	a := fixture()
	tt := snap(t, a, "settings", false, func(tt *ui.Tester) {
		tt.Frame()
		if err := tt.Click("Settings"); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"Account", "Profile", "ada@example.com", "My machines", "Studio", "Preferences", "Agents", "Report bug"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Preferences"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Queued message behavior") {
		t.Fatalf("preferences: %q", tt.Texts())
	}
	if err := tt.Click("Steer"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.busyBehavior != "steer" {
		t.Fatalf("busy behavior = %q", a.busyBehavior)
	}
}

func TestQueuedActionsAndModes(t *testing.T) {
	a := fixture()
	a.catalog.Sessions[0].AgentConfigID = "a1"
	a.catalog.Capabilities = map[string]catalog.Capability{"a1": {Steer: true, Modes: []catalog.Mode{{ID: "default", Name: "Manual"}, {ID: "plan", Name: "Plan Mode"}, {ID: "bypassPermissions", Name: "Bypass Permissions"}}}}
	// Answered: no permission card, a queued follow-up instead.
	a.view.Entries[3].Items[1].Permission = nil
	a.view.ModeID = "bypassPermissions"
	a.view.Queue = []session.Entry{{ID: "q1", Role: "user", Status: "queued", Queued: true, Timestamp: "2026-10-29T09:21:00Z",
		Items: []session.Item{{ID: "t", Type: "text", Text: "Also update the README."}}}}
	tt := snap(t, a, "queued", true, nil)
	for _, want := range []string{"Up next", "1 queued", "Also update the README.", "Steer", "Mode: Bypass Permissions"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	// It waits above the composer, not in the conversation.
	if tt.HasText("Queued") || tt.HasText("Send now") {
		t.Errorf("the queued message shows in the conversation: %q", tt.Texts())
	}
	if err := tt.Click("Mode: Bypass Permissions"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Plan Mode"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	if a.modes["s1"] != "plan" {
		t.Fatalf("mode = %q", a.modes["s1"])
	}
}

func TestSettingsAgents(t *testing.T) {
	a := fixture()
	a.settingsOpen, a.settingsTab = true, "agents"
	tt := snap(t, a, "settings-agents", true, nil)
	if !tt.HasText("Claude Code") || !tt.HasText("Add Provider") {
		t.Fatalf("agents: %q", tt.Texts())
	}
}

// The app speaks the system's language; tests pin English but this one.
func TestMain(m *testing.M) {
	i18n.SetLocaleFunc(func() string { return "en" })
	dir, err := os.MkdirTemp("", "lody-test")
	if err == nil {
		os.Setenv("LODY_DATA_DIR", dir)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestChinese(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	defer i18n.SetLocaleFunc(func() string { return "en" })
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	tt := snap(t, a, "chinese", false, nil)
	for _, want := range []string{"新对话", "搜索", "已置顶", "工作了1分12秒", "添加附件", "模式: 默认", "复制"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	a.settingsOpen = true
	tt = snap(t, a, "chinese-settings", false, nil)
	for _, want := range []string{"账号", "个人资料", "我的机器", "1 个 Agent", "2 个目录", "偏好设置"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
}

// backgroundFixture is a turn waiting on two background commands, after a
// message an agent wrote, as in Lody.
func backgroundFixture() *App {
	a := fixture()
	started := float64(time.Now().Add(-6*time.Minute - 49*time.Second).Unix())
	long := strings.Repeat("P7 is done; please review commit 7a8fdd3 on feature/bilibili-p1.\n", 14)
	a.view.Entries = []session.Entry{
		{ID: "u1", Role: "user", Status: "handled", Timestamp: "2026-10-29T14:53:00Z", Author: &session.Author{Agent: true, Name: "Claude Code", AgentType: "claude"},
			Items: []session.Item{{ID: "t", Type: "text", Text: long}}},
		{ID: "r1", Role: "assistant", UserTurnID: "u1", StartedAt: float64(time.Now().Add(-6 * time.Minute).UnixMilli()), Items: []session.Item{
			{ID: "x1", Type: "text", Text: "Reviewing P7: starting the two gate runs first, and going through the code in parallel."},
			{ID: "s1", Type: "subagent_task", Subagent: &session.Subagent{TaskID: "b1", Actor: "local_bash", Description: "Run full make test twice on P7", State: "running", Background: true, Started: started}},
			{ID: "s2", Type: "subagent_task", Subagent: &session.Subagent{TaskID: "b2", Actor: "local_bash", Description: "Wait for both P7 gate runs", State: "running", Background: true, Started: started,
				Items: []session.Item{{ID: "c", Type: "tool_call", Kind: "execute", Title: "sleep 60", Status: "in_progress"}}}},
		}},
	}
	return a
}

func TestBackgroundTasks(t *testing.T) {
	a := backgroundFixture()
	tt := snap(t, a, "background-tasks", false, nil)
	for _, want := range []string{"Waiting on 2 background tasks", "Bash", "Run full make test twice on P7", "Wait for both P7 gate runs", "Running sleep 60", "Claude Code", "Show more"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	if !tt.HasText("6m 49s") && !tt.HasText("6m 50s") {
		t.Errorf("no elapsed time in %q", tt.Texts())
	}
	if err := tt.Click("Bash · Wait for both P7 gate runs"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.taskOpen || !tt.HasText("Activity") {
		t.Fatalf("task dialog: %q", tt.Texts())
	}

	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	defer i18n.SetLocaleFunc(func() string { return "en" })
	a = backgroundFixture()
	tt = snap(t, a, "background-tasks-zh", false, nil)
	for _, want := range []string{"正在等待 2 个后台任务", "展开"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	// The clock runs while the snapshot settles: 6m49s or a second on.
	if !tt.HasText("6分49秒") && !tt.HasText("6分50秒") {
		t.Errorf("no elapsed time in %q", tt.Texts())
	}
}

func TestDurations(t *testing.T) {
	for d, want := range map[time.Duration]string{12 * time.Second: "12s", 6*time.Minute + 9*time.Second: "6m 09s", time.Hour + 2*time.Minute + 5*time.Second: "1h 02m 05s"} {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", d, got, want)
		}
	}
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	defer i18n.SetLocaleFunc(func() string { return "en" })
	if got := formatDuration(6*time.Minute + 49*time.Second); got != "6分49秒" {
		t.Errorf("zh: %q", got)
	}
}

func TestArchiveMine(t *testing.T) {
	a := fixture()
	a.catalog.Sessions = append(a.catalog.Sessions,
		catalog.Session{ID: "a1", Title: "Mine", Archived: true, UserID: "u1", CreatedAt: "2026-09-01T00:00:00Z"},
		catalog.Session{ID: "a2", Title: "Theirs", Archived: true, UserID: "someone", CreatedAt: "2026-09-02T00:00:00Z"})
	if got := len(a.archivedSessions()); got != 2 {
		t.Fatalf("all = %d", got)
	}
	a.archiveFilter = "mine"
	if got := a.archivedSessions(); len(got) != 1 || got[0].Title != "Mine" {
		t.Fatalf("mine = %+v", got)
	}
}

func TestChangesPanel(t *testing.T) {
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	items := a.view.Entries[1].Items
	items[4].Content = []any{map[string]any{"type": "diff", "path": "internal/catalog/sync.go", "oldText": "package catalog\n", "newText": "package catalog\n\nconst maxReplica = 8 << 20\n"}}
	items[2].Content = []any{map[string]any{"type": "diff", "path": "README.md", "oldText": "# Lody", "newText": "# Lody Native"}}
	a.view.Entries[1].FileDiffs = nil // its card opens the reply's own diff
	tt := snap(t, a, "changes-panel", false, func(tt *ui.Tester) {
		tt.Frame()
		if err := tt.Click("Show panel"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		tt.Advance(300 * time.Millisecond) // as it slides in
		tt.Frame()
		if !tt.HasText("Open a panel") {
			t.Fatalf("no chooser: %q", tt.Texts())
		}
		if err := tt.Click("All Changes"); err != nil {
			t.Fatal(err)
		}
	})
	if !a.panelOpen || !tt.HasText("sync.go") || !tt.HasText("README.md") {
		t.Fatalf("panel: %q", tt.Texts())
	}
	if err := tt.Click("internal/catalog/sync.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.panelFile != "internal/catalog/sync.go" || !tt.HasText("Edit 1") {
		t.Fatalf("file edits: %q", tt.Texts())
	}
}

func TestMentions(t *testing.T) {
	a := fixture()
	a.sidebarHidden = true // it lists the same titles as the panel
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	a.mentionSources = map[string]*mentionSource{
		"skill|m1|m1:local:p1": {loaded: time.Now(), items: mention.Skills([]mention.SkillGroup{{Scope: "project", Dir: ".claude/skills", Skills: []mention.SkillEntry{
			{Name: "review", Description: "Review the diff", RelativePath: ".claude/skills/review/SKILL.md"},
			{Name: "release", Description: "Cut a release", RelativePath: ".claude/skills/release/SKILL.md"},
		}}})},
		"file|m1|m1:local:p1": {loaded: time.Now(), items: mention.Files([]string{"internal/app/view.go", "README.md"})},
	}
	tt := snap(t, a, "mentions", false, func(tt *ui.Tester) {
		tt.Frame()
		tt.Type("@")
		tt.Frame()
	})
	for _, want := range []string{"Files", "Skills", "Sessions"} {
		if !tt.HasText(want) {
			t.Fatalf("no %s category: %q", want, tt.Texts())
		}
	}
	tt.Type("view")
	tt.Frame()
	if !tt.HasText("view.go") {
		t.Fatalf("no file match: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.draft != "@internal/app/view.go " {
		t.Fatalf("draft = %q", a.draft)
	}
	tt.Type("$re")
	tt.Frame()
	if !tt.HasText("$review") || !tt.HasText("$release") {
		t.Fatalf("skills: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if a.draft != "@internal/app/view.go $release " {
		t.Fatalf("draft = %q", a.draft)
	}
	tt.Type("@session:")
	tt.Frame()
	if err := tt.Click("Sidebar status marks"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	if a.draft != "@internal/app/view.go $release @session:s2 " {
		t.Fatalf("draft = %q", a.draft)
	}
	tt.Type("@nothing")
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.HasText("No matches") || a.mention.active {
		t.Fatalf("Escape left the panel open: %q", tt.Texts())
	}
}

func TestFilesPanel(t *testing.T) {
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	a.conn = &cloud.Conn{ID: "w1"}
	a.panelOpen = true
	text := func(s string) *collab.Snapshot {
		return &collab.Snapshot{Kind: "text", Text: &collab.Text{Encoding: "plain", Text: s}}
	}
	two, one := 2, 1
	in := a.inspectFor("s1")
	in.changes[collab.ScopeBranch] = &changesState{res: &collab.Changes{Status: "ok", Base: "0123456789abcdef", Entries: []collab.Change{
		{Status: "ok", Path: "internal/app/view.go", Old: text("package app\n"), New: text("package app\n\nconst x = 1\n"), Add: &two, Del: &one},
	}}}
	in.dirs[""] = &dirListing{entries: []collab.DirEntry{{Name: "internal", Type: "directory"}, {Name: "README.md", Type: "file"}}}
	in.dirs["internal"] = &dirListing{entries: []collab.DirEntry{{Name: "app", Type: "directory"}}}
	in.preview = &filePreview{path: "README.md", text: "# Hello from the machine"}
	in.openPanel(tabChanges)
	tt := snap(t, a, "files-panel", false, nil)
	if !tt.HasText("view.go") || !tt.HasText("Compared with 0123456") {
		t.Fatalf("changes: %q", tt.Texts())
	}
	if err := tt.Click("internal/app/view.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if in.change != "internal/app/view.go" || a.diffs["machine/branch/internal/app/view.go"] == nil {
		t.Fatalf("diff not shown: %q", tt.Texts())
	}
	// Files opens from the + menu, beside All Changes.
	in.openPanel(tabFiles)
	tt.Frame()
	if len(in.tabs) != 2 || !tt.HasText("README.md") || tt.HasText("app") {
		t.Fatalf("tree: %q", tt.Texts())
	}
	if err := tt.Click("internal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("app") {
		t.Fatalf("folder didn't open: %q", tt.Texts())
	}
	if err := tt.Click("README.md"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Hello from the machine") {
		t.Fatalf("file: %q", tt.Texts())
	}
}

func TestTerminalDock(t *testing.T) {
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	a.term.machine, a.term.checked = "this-mac", time.Now()
	tt := snap(t, a, "terminal-remote", false, func(tt *ui.Tester) {
		tt.Frame()
		if err := tt.Click("Toggle terminal"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		tt.Advance(300 * time.Millisecond) // as it grows in
	})
	if !a.term.open || !tt.HasText("Terminal is only available on the machine that owns this session (Studio).") {
		t.Fatalf("remote session: %q", tt.Texts())
	}
	// On this machine, with no daemon to reach.
	a.term.machine = "m1"
	a.term.err = "The Lody daemon isn't running on this machine."
	tt.Frame()
	if !tt.HasText("The Lody daemon isn't running on this machine.") || !tt.HasText("Try again") {
		t.Fatalf("no daemon: %q", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyJ)
	tt.Frame()
	if a.term.open {
		t.Fatal("⌘J left the dock open")
	}
}

func TestPreviewTab(t *testing.T) {
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	a.panelOpen = true
	a.view.Preview = &session.Preview{Candidate: "available", Target: session.PreviewTarget{Protocol: "http", Host: "localhost", Port: 5173, Path: "/app"}}
	tt := snap(t, a, "preview-tab", false, func(tt *ui.Tester) {
		tt.Frame()
		if err := tt.Click("Browser"); err != nil {
			t.Fatal(err)
		}
	})
	if !tt.HasText("The agent reported a server at localhost:5173.") || !tt.HasText("Open preview") {
		t.Fatalf("start: %q", tt.Texts())
	}
	if ps := a.previews["s1"]; ps == nil || ps.address != "localhost:5173/app" {
		t.Fatalf("address bar: %+v", a.previews["s1"])
	}
	// Once the machine shares it, its page shows.
	a.view.Preview.Connection, a.view.Preview.PublicURL = "active", "https://calm-river.trycloudflare.com/?__lody_preview_token=tok"
	tt.Frame()
	if ps := a.previews["s1"]; ps == nil || ps.url != "https://calm-river.trycloudflare.com/app?__lody_preview_token=tok" {
		t.Fatalf("url: %+v", a.previews["s1"])
	}
	if !tt.HasText("localhost:5173/app") {
		t.Fatalf("toolbar: %q", tt.Texts())
	}
}

func TestPreviewAddress(t *testing.T) {
	cases := map[string]string{
		"3000":                        "http localhost:3000 ",
		"localhost:5173/app?x=1":      "http localhost:5173 /app?x=1",
		"https://127.0.0.1:8443":      "https 127.0.0.1:8443 ",
		"http://[::1]:8080/":          "http [::1]:8080 ",
		"example.com:80":              "",
		"localhost":                   "",
		"http://user@localhost:3000/": "",
	}
	for in, want := range cases {
		got := ""
		if t2, ok := previewAddress(in); ok {
			got = t2.Protocol + " " + t2.Label() + " " + t2.Path
		}
		if got != want {
			t.Errorf("previewAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPanelChooser(t *testing.T) {
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	a.panelOpen = true
	tt := snap(t, a, "panel-chooser", false, nil)
	for _, want := range []string{"Open a panel", "Files", "All Changes", "Browser"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Browser"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	in := a.inspectFor("s1")
	if in.tab != tabPreview || !tt.HasText("Open preview") {
		t.Fatalf("browser: %+v %q", in.tabs, tt.Texts())
	}
	in.closePanel(tabPreview)
	tt.Frame()
	if in.tab != "" || !tt.HasText("Open a panel") {
		t.Fatalf("after close: %+v %q", in.tabs, tt.Texts())
	}
}

func TestCloudSettings(t *testing.T) {
	a := fixture()
	a.settingsOpen = true
	seed := func(key, raw string, into any) {
		if err := json.Unmarshal([]byte(raw), into); err != nil {
			t.Fatal(key, err)
		}
		if a.fetches == nil {
			a.fetches = map[string]*fetched{}
		}
		v := reflect.ValueOf(into)
		switch v.Elem().Kind() {
		case reflect.Slice:
			a.fetches[a.workspaceID+"|"+key] = &fetched{value: v.Elem().Interface()}
		default:
			a.fetches[a.workspaceID+"|"+key] = &fetched{value: into}
		}
	}
	seed("members", `[{"id":"mem1","userId":"u1","role":"owner","user":{"id":"u1","name":"Ada Lovelace","email":"ada@example.com"}},
		{"id":"mem2","userId":"u2","role":"member","user":{"id":"u2","name":"Grace Hopper","email":"grace@example.com"}}]`, &[]cloud.Member{})
	seed("machines", `[{"machineId":"m1","ownerUserId":"u1","sharedWithTeam":true,"updatedAt":1}]`, &[]visibleMachine{})
	seed("usage/day", `{"totals":{"tokens":1500000,"costUSD":12.34},"users":{"u2":{"name":"Grace Hopper"}},
		"buckets":[{"bucketStartMs":0,"bucketLabel":"Oct 1","tokens":500000,"costUSD":4,"byModel":[{"modelId":"claude-opus-5-5","tokens":500000,"costUSD":4}],"byUser":[{"userId":"u1","tokens":500000,"costUSD":4}]},
		{"bucketStartMs":1,"bucketLabel":"Oct 2","tokens":1000000,"costUSD":8.34,"byModel":[{"modelId":"gpt-5","tokens":1000000,"costUSD":8.34}],"byUser":[{"userId":"u2","tokens":1000000,"costUSD":8.34}]}]}`, &usageTimeline{})
	var billing *billingOverview
	seed("billing", `{"billingAccountId":null,"planTier":"plus","effectivePlanTier":"plus","billingInterval":"month","nextBillingAt":1798804800000,"seatCount":2.0,"canManageBilling":true,"cancelAtPeriodEnd":false}`, &billing)
	a.fetches[a.workspaceID+"|billing"].value = billing
	var repos *[]workspaceRepo
	seed("github", `[{"repoFullName":"lbr77/lody-mygo","name":"lody-mygo","repositoryId":1,"private":true,"enabled":true}]`, &repos)
	a.fetches[a.workspaceID+"|github"].value = repos
	var limit *memberLimit
	seed("member-limit", `{"memberLimit":null,"canInvite":true,"isPaid":true,"memberCount":2.0,"pendingInvitationCount":0}`, &limit)
	a.fetches[a.workspaceID+"|member-limit"].value = limit
	seed("shares", `{"page":[{"shareId":"sh1","publisherUserId":"u2","status":"active","revision":3,"title":"Flock port","createdAt":1798761600000,"totalBytes":2048,"conversationCount":2.0,"canRevoke":true}],"isDone":true,"continueCursor":"c1"}`, &sharesPage{})

	check := func(tab string, want ...string) *ui.Tester {
		t.Helper()
		a.settingsTab = tab
		tt := snap(t, a, "settings-"+tab, false, nil)
		for _, w := range want {
			if !tt.HasText(w) {
				t.Fatalf("%s: no %q in %q", tab, w, tt.Texts())
			}
		}
		return tt
	}
	check("general", "Members", "Ada Lovelace (You)", "Grace Hopper", "Owner", "Member")
	seed("cli-keys", `[{"id":"k1","note":"build server","keyPreview":"lody_ab…","source":"manual","createdAt":1798804800000.0}]`, &[]cliKey{})
	seed("accounts", `[{"id":"a1","providerId":"github","accountId":"1"}]`, &[]cloud.LinkedAccount{})
	check("account", "My machines", "Studio", "Manage", "CLI Token", "build server", "Set up password sign-in")
	a.expanded["machine/m1"] = true
	check("machines", "Studio", "Edit machine name", "Share machine", "Remove from workspace", "Connected folders", "lody-mygo", "Connected agents", "Claude Code")
	check("ai-usage", "1.5M", "$12.34", "Usage skyline", "By model", "claude-opus-5-5", "gpt-5", "By member", "Ada Lovelace", "Grace Hopper", "Longest streak")
	var timeline usageTimeline
	json.Unmarshal([]byte(`{"totals":{"tokens":1500000,"costUSD":12.34},"buckets":[{"bucketStartMs":0,"bucketLabel":"Oct 1","tokens":1500000,"costUSD":12.34,"byModel":[{"modelId":"gpt-5","tokens":1500000,"costUSD":12.34}]}]}`), &timeline)
	a.fetches[a.workspaceID+"|usage/total"] = &fetched{value: &timeline}
	seed("usage-calendar", `[{"dayStartMs":1798761600000,"date":"2027-01-01","tokens":1000,"costUSD":1,"isFuture":false},{"dayStartMs":1798848000000,"date":"2027-01-02","tokens":3000,"costUSD":2,"isFuture":false},{"dayStartMs":1798934400000,"date":"2027-01-03","tokens":0,"costUSD":0,"isFuture":true}]`, &[]usageDay{})
	a.usageRange = "total"
	check("ai-usage", "Daily average", "2K", "Peak day", "Active days", "Longest streak")
	a.usageRange = ""
	check("billing", "Plus", "Monthly", "Switch to yearly", "Renews on", "Jan 1, 2027", "Team members", "2 / 2")
	check("github", "GitHub App", "1 of 1 repositories enabled", "Act as yourself", "lbr77/lody-mygo", "Private")
	a.catalog.MCPServers = []catalog.MCPServer{
		{ID: "x1", Name: "IDA Pro", Transport: "stdio", Connection: map[string]any{"transport": "stdio", "command": "uvx", "args": []any{"ida-mcp", "stdio"}}, EnabledByDefault: true},
		{ID: "x2", Name: "Notes", Transport: "http", Connection: map[string]any{"transport": "http", "url": "https://notes.example/mcp"}, Description: "A note server"},
	}
	check("mcp", "IDA Pro", "stdio", "uvx ida-mcp stdio", "Notes", "Streamable HTTP", "https://notes.example/mcp", "Add server")
	a.catalog.Agents["m1"][0].Row = map[string]any{"env": map[string]any{"A": "1"}}
	check("agents", "Claude Code", "1 environment variable", "Add Provider")
	a.openProvider("m1", nil)
	check("agents", "New Provider", "Kimi Code", "DeepSeek over Claude Code", "ACP Providers", "Amp", "Custom command", "Runtime binary path")
	if a.catalog.MachineCaps == nil {
		a.catalog.MachineCaps = map[string]map[string]float64{}
	}
	a.catalog.MachineCaps["m1"] = map[string]float64{"codexAuthProfiles": 1, "acpAuthenticationInteractions": 2, "acpProtocolAuthentication": 2}
	for _, k := range allKinds {
		if k.value == "builtin:codex" {
			a.selectKind(k)
		}
	}
	if a.catalog.Capabilities == nil {
		a.catalog.Capabilities = map[string]catalog.Capability{}
	}
	a.catalog.Capabilities[a.provider.id] = catalog.Capability{CLIType: "builtin", AgentType: "codex", Options: []catalog.ConfigOption{
		{ID: "model", Name: "Model", Category: "model", Type: "select", Current: "gpt-5.6", Choices: []catalog.Choice{{Value: "gpt-5.6", Name: "GPT-5.6"}, {Value: "gpt-5.6-luna", Name: "GPT-5.6 Luna"}}},
		{ID: "reasoning_effort", Name: "Reasoning effort", Category: "thought_level", Type: "select", Current: "medium", Choices: []catalog.Choice{{Value: "low", Name: "Low"}, {Value: "medium", Name: "Medium"}}},
	}}
	check("agents", "Authentication", "ChatGPT", "Custom API", "Title generation", "Model", "GPT-5.6 Luna", "Reasoning effort", "Low")
	if a.provider.codex == nil || a.provider.codex.mode != "chatgpt" || a.provider.title["model"] != "gpt-5.6-luna" {
		t.Fatalf("codex form = %+v, title %v", a.provider.codex, a.provider.title)
	}
	a.provider.authRequired = true
	check("agents", "Account", "This provider has no credentials on this machine yet.", "Sign in with ChatGPT")
	var opened string
	openExternal = func(u string) { opened = u }
	defer func() { openExternal = func(u string) { mygo.Shell.OpenExternal(u) } }()
	flow := &authFlow{phase: "running", machine: "m1", requestID: "auth-1"}
	a.provider.auth = flow
	a.signInProgress(flow, authProgress{RequestID: "auth-1", Status: "authorization", AuthorizationURL: "https://auth.openai.com/device", UserCode: "ABCD-1234", AcceptsAuthorizationCode: true})
	check("agents", "Waiting for ChatGPT sign-in", "Finish signing in to ChatGPT", "ABCD-1234", "Copy code", "Authorization code")
	if opened != "https://auth.openai.com/device" {
		t.Fatalf("opened %q", opened)
	}
	a.signInProgress(flow, authProgress{Status: "input-required", InteractionID: "i1", Form: &authForm{Fields: []authField{{ID: "apiKey", Type: "secret", Label: "API Key", Required: true}}}})
	check("agents", "Additional information", "API Key", "Continue")
	a.signInDone(flow, authResponse{Success: true, Disposition: "authenticated"}, nil)
	check("agents", "ChatGPT sign-in completed", "Sign in again")
	if a.provider.authRequired {
		t.Fatal("still signed out")
	}
	a.provider.open = false
	a.mcpTools = map[string]*mcpTools{"x1": {conn: connKey(a.catalog.MCPServers[0].Connection), tools: []mcpTool{{Name: "decompile", Description: "Decompiles"}}},
		"x2": {conn: connKey(a.catalog.MCPServers[1].Connection), err: "Couldn't connect. Check the configuration and try again."}}
	check("mcp", "decompile", "Couldn't connect. Check the configuration and try again.", "Settings")
	a.mcpTools["x2"].oauth, a.mcpTools["x2"].err = true, "This server needs authorization."
	check("mcp", "This server needs authorization.", "Authorize")
	a.editMCP(&a.catalog.MCPServers[1])
	a.term.machine, a.term.checked = "m1", time.Now()
	a.catalog.MachineCaps["m1"]["mcpLocalAuthentication"] = 1
	a.catalog.MachineCaps["m1"]["mcpToolDiscovery"] = 1
	a.mcp.auth.result = &mcpAuthResult{Status: "required", Reason: "oauth_required"}
	check("mcp", "Edit MCP server", "Manual credentials", "Sign-in and authorization", "This server needs authorization.", "Authorize",
		"Use your own OAuth application", "Select by default", "Save configuration")
	a.mcp.auth.result = &mcpAuthResult{Status: "waiting", Mode: "oauth", AttemptID: "at1", AuthorizationURL: "https://auth.notes.example/authorize?x=1"}
	check("mcp", "Waiting for authorization in your browser…", "Cancel", "https://auth.notes.example")
	a.mcp.auth.result = &mcpAuthResult{Status: "authorized", Mode: "oauth", HasCredentials: true}
	check("mcp", "Authorized", "Sign out")
	a.mcp.url = "https://other.example/mcp"
	check("mcp", "Save the configuration before authorizing.")
	a.mcp.open = false
	a.term.machine = ""
	a.editMCP(nil)
	check("mcp", "Add MCP server", "Save the configuration before authorizing.", "Environment variable passthrough")
	a.mcp.open = false
	a.catalog.Memories = map[string][]catalog.Memory{"m1": {{ProviderID: "nowledge-mem", MemoryID: "ada", MachineID: "m1", Name: "Ada's memory"}}}
	seed("memory/m1", `{"type":"machine/memory","status":"ready","memories":[{"id":"ada","name":"Ada's memory"},{"id":"grace","name":"Grace"}]}`, &memoryInventory{})
	check("memory", "Ada's memory", "Add memory")
	check("projects", "lody-mygo", "src/lody-mygo", "Add project")
	a.openProject("m1:local:p1")
	check("projects", "General", "Worktree", "Skills", "Default MCP", "Folder", "/src/lody-mygo", "Copy path", "Delete project")
	a.projectWin.page = "worktree"
	check("projects", "Worktree Setup", "Worktree Cleanup", "bash")
	a.projectWin.page = "mcp"
	check("projects", "Until you change a switch, the project follows the workspace's defaults.", "IDA Pro", "Notes")
	a.catalog.ProjectMCP = map[string][]string{catalog.ProjectMCPKey("m1", "p1"): {"x2"}}
	check("projects", "This project has its own list; servers the workspace turns on by default later aren't added.")
	for i := range a.catalog.Projects {
		if a.catalog.Projects[i].ID == "m1:local:p1" {
			a.catalog.Projects[i].History = map[string]any{"builtin:claude": map[string]any{"lastListedAt": 1.0, "sessions": map[string]any{
				"x": map[string]any{"acpSessionId": "x", "title": "Fix the login page", "status": "available", "updatedAt": "2026-10-20T10:00:00Z"},
				"y": map[string]any{"acpSessionId": "y", "title": "Port the parser", "importedSessionId": "s9"},
			}}}
		}
	}
	a.projectWin.page = "conversations"
	tt0 := check("projects", "Claude Code conversations", "Sync again", "Fix the login page", "Port the parser", "Imported", "Select all 1 shown")
	if err := tt0.Click("Select all 1 shown"); err != nil {
		t.Fatal(err)
	}
	tt0.Frame()
	if !tt0.HasText("Import 1 selected") {
		t.Fatalf("selection: %q", tt0.Texts())
	}
	a.projectWin.open = false
	seed("shortcuts", `[{"v":1,"id":"p1","workspaceId":"w1","ownerUserId":"u1","visibility":"private","name":"Review","slug":"review","scope":{},"revision":"r1","createdAt":1,"updatedAt":1,"bodyDocId":"b1","dependencySummary":[]}]`, &[]shortcuts.Entry{})
	check("prompt-shortcuts", "Review", "/review", "Private", "New Prompt Shortcut")
	a.catalog.Roles = []catalog.AgentRole{{ID: "r1", OwnerUserID: "u1", Visibility: "workspace", Name: "Reviewer", Emoji: "🔍", Targets: []catalog.RoleTarget{{MachineID: "m1", AgentConfigID: "a1"}}}}
	check("agent-roles", "Reviewer", "Shared", "Claude Code · Studio")
	a.editRole(&a.catalog.Roles[0])
	check("agent-roles", "Edit role", "Runs on", "Claude Code", "Share with the workspace")
	a.role.open = false
	a.expanded["machine/m1"] = true
	cores, mem := 0.009, 458.0*(1<<20)
	a.monitors = map[string]*machineMonitor{"m1": {cancel: func() {}, sample: &presence.Sample{MachineID: "m1", Platform: "darwin",
		CLI: presence.Resource{CPUCores: &cores, MemoryBytes: &mem}, EffectiveMemoryBytes: 16 * (1 << 30), AvailableMemoryBytes: 8 * (1 << 30)}}}
	check("machines", "CLI", "0.9%", "458 MB", "ACP sessions", "8.0 GB / 16 GB")
	a.monitors = nil
	tt := check("shares", "Flock port", "View profile for Grace Hopper")
	if err := tt.Click("Flock port"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Revoke share"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	if !tt.HasText("The previous link will stop working. Downloaded copies cannot be recalled.") || !a.confirming.open || a.sharePages.detailOpen {
		t.Fatalf("revoke confirmation: %q", tt.Texts())
	}
	a.confirming.open = false
}

func TestProviderRow(t *testing.T) {
	var deepseek providerKind
	for _, k := range providerKinds {
		if k.value == "preset:deepseek-over-claude-code" {
			deepseek = k
		}
	}
	f := providerForm{machine: "m1", id: "c1", kind: deepseek, token: "sk-x", env: "EXTRA=1"}
	row, err := f.row()
	if err != nil {
		t.Fatal(err)
	}
	env := row["env"].(map[string]any)
	if row["agentType"] != "claude" || row["brandId"] != "deepseek" || env["ANTHROPIC_AUTH_TOKEN"] != "sk-x" ||
		env["ANTHROPIC_BASE_URL"] != "https://api.deepseek.com/anthropic" || env["EXTRA"] != "1" || row["name"] != "DeepSeek over Claude Code" {
		t.Fatalf("row = %v", row)
	}
	custom := providerForm{machine: "m1", id: "c2", kind: providerKinds[len(providerKinds)-1], command: `/opt/my agent --flag "two words" a\ b`}
	row, err = custom.row()
	if err != nil {
		t.Fatal(err)
	}
	acp := row["customAcp"].(map[string]any)
	if acp["command"] != "/opt/my" || len(acp["args"].([]any)) != 4 || acp["args"].([]any)[2] != "two words" || acp["args"].([]any)[3] != "a b" {
		t.Fatalf("custom = %v", acp)
	}
	if !strings.HasPrefix(row["agentType"].(string), "custom-") || row["cliType"] != "custom" {
		t.Fatalf("custom row = %v", row)
	}
}

func TestCodexRow(t *testing.T) {
	var codex providerKind
	for _, k := range allKinds {
		if k.value == "builtin:codex" {
			codex = k
		}
	}
	f := providerForm{machine: "m1", id: "c1", kind: codex, codex: &codexAuth{mode: "api-key", profileID: "p1", baseURL: " https://api.example.com/v1/ "}, path: "/usr/bin/codex"}
	row, err := f.row()
	if err != nil {
		t.Fatal(err)
	}
	auth := row["codexAuth"].(map[string]any)
	if auth["mode"] != "api-key" || auth["profileId"] != "p1" || auth["baseUrl"] != "https://api.example.com/v1" ||
		row["runtimeOverrides"].(map[string]any)["codexPath"] != "\x00lody-codex-profile-v1" {
		t.Fatalf("row = %v", row)
	}
	for _, bad := range []string{"http://api.example.com", "https://u:p@api.example.com", "https://api.example.com/?k=1", "nope"} {
		f.codex.baseURL = bad
		if _, err := f.row(); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	f.codex.baseURL = "http://127.0.0.1:8080/v1"
	if _, err := f.row(); err != nil {
		t.Fatal(err)
	}
	f.env = "OPENAI_API_KEY=sk"
	if _, err := f.row(); err == nil {
		t.Fatal("a profile's variable overridden")
	}
}

func TestTitleDefaults(t *testing.T) {
	cap := catalog.Capability{Options: []catalog.ConfigOption{
		{ID: "mode", Category: "mode", Type: "select", Current: "default", Choices: []catalog.Choice{{Value: "default", Name: "Default"}, {Value: "plan", Name: "Plan"}, {Value: "read-only", Name: "Read only"}}},
		{ID: "model", Category: "model", Type: "select", Current: "big", Choices: []catalog.Choice{{Value: "big", Name: "Big"}, {Value: "small", Name: "Small"}}},
		{ID: "reasoning_effort", Type: "select", Current: "high", Choices: []catalog.Choice{{Value: "high", Name: "High"}, {Value: "minimal", Name: "Minimal"}, {Value: "low", Name: "Low"}}},
		{ID: "fast", Type: "boolean", Current: false},
		{ID: "empty", Type: "select"},
	}, Efforts: map[string][]string{"small": {"low", "high"}}}
	options := titleOptions(cap, nil)
	if len(options) != 4 || len(options[2].Choices) != 3 {
		t.Fatalf("options = %+v", options)
	}
	got := titleDefaults("registry", "x", options)
	if got["mode"] != "read-only" || got["reasoning_effort"] != "minimal" || len(got) != 2 {
		t.Fatalf("defaults = %v", got)
	}
	if efforts := titleOptions(cap, map[string]any{"model": "small"})[2].Choices; len(efforts) != 2 || efforts[0].Value != "high" {
		t.Fatalf("small's efforts = %v", efforts)
	}
	if got := titleDefaults("builtin", "codex", options); got["model"] != "gpt-5.6-luna" {
		t.Fatalf("codex = %v", got)
	}
	f := providerForm{kind: providerKind{cliType: "builtin", agentType: "claude", group: "builtin"}, id: "c1", title: map[string]any{"mode": "plan"}}
	a := &App{}
	if f.ownsTitle = a.titleOwned(&f); !f.ownsTitle {
		t.Fatal("Claude titles its own sessions")
	}
	if row, _ := f.row(); row["titleGeneration"] != nil {
		t.Fatalf("row = %v", row)
	}
	f.kind = providerKind{cliType: "registry", agentType: "amp-acp", group: "registry"}
	f.ownsTitle = a.titleOwned(&f)
	if row, _ := f.row(); row["titleGeneration"].(map[string]any)["configOptionValues"].(map[string]any)["mode"] != "plan" {
		t.Fatalf("row = %v", row)
	}
}

func TestReplaceShortcuts(t *testing.T) {
	prompts := map[string]string{"review": "Review the diff.", "ship": "Ship it."}
	cases := map[string]string{
		"/shortcut:review":                       "Review the diff.",
		"/shortcut:review /shortcut:ship please": "Review the diff. Ship it. please",
		"see/shortcut:review":                    "see/shortcut:review",
		"/shortcut:unknown and /shortcut:ship":   "/shortcut:unknown and Ship it.",
	}
	for in, want := range cases {
		if got := replaceShortcuts(in, prompts); got != want {
			t.Errorf("replaceShortcuts(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRoleRow(t *testing.T) {
	a := fixture()
	a.editRole(nil)
	f := &a.role
	cfg := f.runConfig(roleTarget{machine: "m1", agent: "a1", model: "claude-opus-5-5"})
	if cfg["modelId"] != "claude-opus-5-5" || cfg["modeId"] != nil {
		t.Fatalf("runConfig = %v", cfg)
	}
	// An edit keeps the options the editor doesn't show.
	base := catalog.AgentRole{ID: "r1", Revision: 2, Row: map[string]any{"v": 2.0, "targets": []any{
		map[string]any{"machineId": "m1", "agentConfigId": "a1", "runConfig": map[string]any{"configOptionValues": map[string]any{"effort": "high"}}},
	}}}
	f.base = &base
	if cfg := f.runConfig(roleTarget{machine: "m1", agent: "a1"}); cfg["configOptionValues"] == nil {
		t.Fatalf("lost the role's other options: %v", cfg)
	}
}

func TestSidebarResize(t *testing.T) {
	a := fixture()
	tt := snap(t, a, "sidebar-resize", false, nil)
	r, ok := tt.Find("Resize sidebar")
	if !ok {
		t.Fatalf("no handle: %q", tt.Texts())
	}
	x, y := r.X+r.W/2, r.Y+r.H/2
	tt.Press(x, y)
	tt.Move(x+40, y)
	tt.Frame()
	tt.Move(x+80, y)
	tt.Frame()
	tt.Release(x+80, y)
	tt.Frame()
	if w := a.sidebarW(); w < 370 || w > 390 {
		t.Fatalf("width after dragging 80 = %v", w)
	}
	tt.Press(x+80, y)
	tt.Move(x+500, y)
	tt.Frame()
	tt.Release(x+500, y)
	tt.Frame()
	if w := a.sidebarW(); w != sidebarMax {
		t.Fatalf("width past the end = %v", w)
	}
}

func TestOlderVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{{"0.108.1", "0.109.0", true}, {"0.108.1", "0.108.1", false}, {"1.0.0", "0.99.9", false}, {"v0.9.10", "0.10.0", true}, {"", "1.0.0", false}}
	for _, c := range cases {
		if got := olderVersion(c.a, c.b); got != c.want {
			t.Errorf("olderVersion(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestSessionActions(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.view.Entries = a.view.Entries[:3]
	a.view.Entries[1].AcpTurnID = "t1"
	a.catalog.Sessions[0].Status, a.catalog.Sessions[0].AgentConfigID = "idle", "a1"
	a.catalog.Capabilities = map[string]catalog.Capability{"a1": {Fork: true, GoalActions: []string{"pause", "resume", "clear"}}}
	if e := a.editableEntry(); e == nil || e.ID != "u2" {
		t.Fatalf("editable = %v", e)
	}
	if !a.forkable(a.view.Entries[1]) {
		t.Fatal("the reply is forkable")
	}
	tt := snap(t, a, "session-actions", false, func(tt *ui.Tester) {})
	if !tt.HasText("Edit message") || !tt.HasText("Fork session") {
		t.Fatalf("labels: %q", tt.Texts())
	}
	a.startEdit(a.view.Entries[2])
	tt = snap(t, a, "session-edit", false, nil)
	if !tt.HasText("Cancel") || a.msgEdit.text != "Now run it against the real stream." {
		t.Fatalf("editor: %q", tt.Texts())
	}
	a.view.Goal = &session.Goal{ThreadID: "g1", Objective: "Ship the native client", Status: "active", TokensUsed: 12400, Seconds: 95}
	tt = snap(t, a, "session-goal", false, nil)
	for _, want := range []string{"Goal", "Pursuing goal", "Ship the native client", "Pause", "Clear", "tokens: 12.4K"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if a.editableEntry() != nil {
		t.Fatal("an active goal keeps messages as they are")
	}
	a.view.Goal.Status = "cleared"
	tt = snap(t, a, "session-goal-cleared", false, nil)
	if !tt.HasText("Goal cleared") || !tt.HasText("Dismiss goal banner") {
		t.Fatalf("cleared: %q", tt.Texts())
	}
	if tokensCompact(1_234_567) != "1.23M" || tokensCompact(999) != "999" || tokensCompact(120_000) != "120K" {
		t.Fatal("tokensCompact")
	}
}

func TestFileEditing(t *testing.T) {
	if got := conflictMarkers("mine", "disk\n"); got != "<<<<<<< yours\nmine\n=======\ndisk\n>>>>>>> disk\n" {
		t.Fatalf("markers = %q", got)
	}
	a := fixture()
	a.panelOpen = true
	in := a.inspectFor("s1")
	in.openPanel(tabFiles)
	in.file = "main.go"
	buf := editor.NewBuffer("package main\n")
	fp := &filePreview{path: "main.go", text: "package main\n", editing: true, base: &collab.File{Path: "main.go", Digest: "sha256:x", Text: "package main\n"},
		code: &editor.State{Buffer: buf}, conflict: &collab.Conflict{Reason: "digest_mismatch", DiskDigest: "sha256:y", DiskText: "package other\n", HasText: true}}
	in.preview = fp
	a.conn = &cloud.Conn{ID: "w1"}
	tt := snap(t, a, "file-conflict", false, nil)
	for _, want := range []string{"Save conflict", "digest_mismatch", "Reload", "Insert conflict markers", "Overwrite disk", "Done"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	fp.conflict, fp.disk = nil, &collab.File{Digest: "sha256:z", Text: "package newer\n"}
	tt = snap(t, a, "file-disk-changed", false, nil)
	if !tt.HasText("The agent or another peer changed this file.") {
		t.Fatalf("disk banner: %q", tt.Texts())
	}
}

// A command step opens Lody's sheet: the command after a prompt, then the
// tail of its output, saying when earlier output was left out.
func TestCommandDetail(t *testing.T) {
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	var out []string
	for i := 1; i <= 30; i++ {
		out = append(out, fmt.Sprintf("line %d", i))
	}
	items := a.view.Entries[1].Items
	items[2].HasDetail = true
	items[2].Content = []any{
		map[string]any{"type": "terminal_command", "command": "/bin/zsh", "args": []any{"-lc", "grep -rn MUSE MuseAmp | head"}},
		map[string]any{"type": "terminal_output", "output": strings.Join(out, "\n"), "exitStatus": map[string]any{"exitCode": 1.0}},
	}
	key := "r1/" + blocks(items)[0].key
	a.expanded["r1/work"], a.expanded[key], a.expanded[key+"/"+items[2].ID] = true, true, true
	tt := snap(t, a, "command-detail", false, nil)
	for _, want := range []string{"$", "grep -rn MUSE MuseAmp | head", "Earlier output not shown", "line 30", "Exit 1"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("line 14\n") || strings.Contains(strings.Join(tt.Texts(), "\n"), "\"terminal_command\"") {
		t.Fatalf("raw content shown: %q", tt.Texts())
	}
}

func TestToolCommandAndTail(t *testing.T) {
	if got := toolCommand(map[string]any{"command": "/bin/bash", "args": []any{"-lc", "ls -la"}}); got != "ls -la" {
		t.Fatalf("wrapped = %q", got)
	}
	if got := toolCommand(map[string]any{"command": "rg", "args": []any{"two words", "src/"}}); got != "rg 'two words' src/" {
		t.Fatalf("quoted = %q", got)
	}
	text, cut := terminalTail([]string{"a\nb\n"})
	if text != "a\nb" || cut {
		t.Fatalf("tail = %q %v", text, cut)
	}
	spans := ansiSpans("\x1b[31mred\x1b[0m plain\x1b[K", ui.Hex("#777777"), false)
	if len(spans) != 2 || spans[0].Text != "red" || spans[0].Color != ui.Hex("#C91B00") || spans[1].Text != " plain" {
		t.Fatalf("spans = %+v", spans)
	}
}

func TestNewChatWorktree(t *testing.T) {
	a := fixture()
	a.selected, a.view = "", nil
	a.conn = &cloud.Conn{ID: "w1"}
	a.pickNewChatDefaults()
	a.newChat.project = "m1:local:p1"
	a.gitStates = map[string]*gitState{"m1:local:p1": {git: true, branches: []string{"main", "feature/x"}, current: "main", def: "main", repo: "lbr77/lody-mygo"}}
	tt := snap(t, a, "newchat-local", false, nil)
	if !tt.HasText("Local") || !tt.HasText("main") {
		t.Fatalf("local: %q", tt.Texts())
	}
	a.newChat.worktree, a.newChat.branch = true, "feature/x"
	tt = snap(t, a, "newchat-worktree", false, nil)
	if !tt.HasText("Worktree") || !tt.HasText("feature/x") {
		t.Fatalf("worktree: %q", tt.Texts())
	}
	if b := a.newChatBranch(a.gitStates["m1:local:p1"]); b != "feature/x" {
		t.Fatalf("branch %q", b)
	}
	a.newChat.branch = "gone"
	if b := a.newChatBranch(a.gitStates["m1:local:p1"]); b != "main" {
		t.Fatalf("a branch no longer there: %q", b)
	}
}

func TestProjectRemoval(t *testing.T) {
	a := fixture()
	a.settingsOpen, a.settingsTab = true, "projects"
	a.catalog.MachineCaps = map[string]map[string]float64{"m1": {"localProjectRemoval": 1}}
	a.removal = projectRemoval{open: true, project: a.catalog.Projects[0], cleanup: true,
		preflight: &removalPreflight{Clean: []worktreeItem{{SessionID: "a"}, {SessionID: "b"}}, Dirty: []worktreeItem{{SessionID: "c"}}}}
	a.presence = presence.State{Live: true, Online: []string{"m1"}}
	tt := snap(t, a, "project-removal", false, nil)
	for _, want := range []string{"Remove “lody-mygo” from Lody?", "2 conversations will move to Archive.", "1 conversation is still running",
		"Also delete session worktrees created by Lody", "2 clean worktrees will be deleted.", "1 worktree has changes", "never deletes the original project folder"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
}

func TestQuotasBugReportPi(t *testing.T) {
	a := fixture()
	a.settingsOpen, a.settingsTab = true, "agents"
	now := time.Now()
	a.catalog.Capabilities = map[string]catalog.Capability{"a1": {RateLimits: true}}
	a.catalog.RateLimits = map[string][]catalog.RateLimit{"a1": {{ConfigID: "a1", CLIType: "claude", Windows: []catalog.RateWindow{
		{UsedPercent: 42, Seconds: 5 * 3600, ResetsAt: float64(now.Add(2 * time.Hour).Unix())},
		{UsedPercent: 7.6, Seconds: 7 * 86400},
	}}}}
	tt := snap(t, a, "provider-quota", false, nil)
	for _, want := range []string{"5h", "42% used", "Resets in 2 hours", "7d", "8% used", "Reset time unknown"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	a.settingsOpen = false
	a.openBugReport()
	a.presence = presence.State{Live: true}
	tt = snap(t, a, "bug-report", false, nil)
	for _, want := range []string{"Report a bug", "No machine (client diagnostics only)", "No machines are online.", "What happened?", "Submit report"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	a.bug.id, a.bug.machine = "br_123", "m1"
	tt = snap(t, a, "bug-report-done", false, nil)
	if !tt.HasText("br_123") || !tt.HasText("The machine logs and your description were uploaded.") {
		t.Fatalf("done: %q", tt.Texts())
	}
	if !strings.Contains(buildInfo("it broke"), "it broke\n\n---\nReporting client build\nVersion: ") {
		t.Fatal("build info")
	}
	a.bug.open = false
	a.settingsOpen = true
	a.catalog.MachineCaps = map[string]map[string]float64{"m1": {"piExtensions": 1}}
	for _, k := range allKinds {
		if k.value == "builtin:pi" {
			a.openProvider("m1", nil)
			a.selectKind(k)
		}
	}
	a.provider.sections["pi"] = true
	a.provider.piExt = []string{"~/ext/a"}
	a.provider.piScan = &piDiscovery{AgentDir: "~/.pi/agent", Extensions: []struct {
		Path   string `json:"path"`
		Name   string `json:"name"`
		Source string `json:"source"`
	}{{Path: "~/.pi/agent/extensions/b", Name: "b", Source: "directory"}}}
	tt = snap(t, a, "provider-pi", false, nil)
	for _, want := range []string{"Pi extensions", "Rescan", "Scanned ~/.pi/agent", "~/.pi/agent/extensions/b", "not in last scan", "Add path"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	row, err := a.provider.row()
	if err != nil || row["runtimeOverrides"].(map[string]any)["piExtensions"].([]any)[0] != "~/ext/a" {
		t.Fatalf("row = %v, %v", row, err)
	}
	if validPiPath("relative/x") || !validPiPath("~/x") || !validPiPath("/abs") {
		t.Fatal("validPiPath")
	}
}

// The revision hash matches Lody's, so clients agree when the review's
// sources changed.
func TestReviewRevision(t *testing.T) {
	if got := reviewRevision([]string{"members:u1,u2", "machines:m1", "projects:m1:p1", "no-local:0", "empty:0"}); got != "v1-2955884c3a5d08c1" {
		t.Fatalf("revision = %s", got)
	}
	if got := reviewRevision([]string{"members:用户", "machines:", "projects:", "no-local:1", "empty:1"}); got != "v1-e2392db184931c79" {
		t.Fatalf("revision = %s", got)
	}
}

func TestInbox(t *testing.T) {
	a := fixture()
	read := 1.0
	a.fetches = map[string]*fetched{
		"w1|inbox": {value: []inboxItem{
			{ID: "i1", Kind: "permission_requested", Title: "Fix the flaky upload test", Route: "/analytical/sessions/s3", UpdatedAt: float64(time.Now().Add(-5 * time.Minute).UnixMilli()), ToolLabel: "rm -rf build/"},
			{ID: "i2", Kind: "session_completed", Title: "Sidebar status marks", Route: "/analytical/sessions/s2?tab=x", UpdatedAt: 1, ReadAt: &read},
			{ID: "i3", Kind: "sharing_review"},
		}},
	}
	tt := snap(t, a, "inbox-sidebar", false, nil)
	if !tt.HasText("Inbox") || !tt.HasText("1") {
		t.Fatalf("sidebar: %q", tt.Texts())
	}
	a.page = "inbox"
	tt = snap(t, a, "inbox", false, nil)
	for _, want := range []string{"Approval needed: rm -rf build/", "Session completed.", "Fix the flaky upload test"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("Review what your team can see") {
		t.Fatal("the review shows only while it applies")
	}
	if (inboxItem{Route: "/w/sessions/s2?tab=x"}).session() != "s2" {
		t.Fatal("session from route")
	}
}

func TestWorkspaceSlugsAndLinks(t *testing.T) {
	for name, want := range map[string]string{
		"Analytical Engine": "analytical-engine",
		"  Café — Résumé ":  "cafe-resume",
		"研究":                "workspace",
		"":                  "",
		"a--b__c":           "a-b-c",
	} {
		if got := suggestSlug(name); got != want {
			t.Errorf("suggestSlug(%q) = %q, want %q", name, got, want)
		}
	}
	if got := typedSlug("--My-Team-"); got != "my-team-" {
		t.Errorf("typedSlug = %q", got)
	}
	for slug, ok := range map[string]bool{"team": true, "ab": false, "my-team-": false, "my--team": false, "My_Team": false, "": false} {
		if (slugRuleError(slug) == "") != ok {
			t.Errorf("slugRuleError(%q) = %q", slug, slugRuleError(slug))
		}
	}
	for link, want := range map[string][2]string{
		"https://lody.ai/join/abc_DEF-1":               {"join", "abc_DEF-1"},
		"https://lody.ai/invite/inv123?x":              {"invite", "inv123"},
		"https://lody.ai/settings":                     {"", ""},
		"https://lody.ai/analytical/sessions/s3?tab=x": {"workspace", "analytical/s3"},
		"https://lody.ai/my-team":                      {"workspace", "my-team/"},
		"https://lody.ai/s/abc":                        {"", ""},
	} {
		if k, id := parseWorkspaceLink(link); k != want[0] || id != want[1] {
			t.Errorf("parseWorkspaceLink(%q) = %q, %q", link, k, id)
		}
	}
}

func TestWorkspaceDialogs(t *testing.T) {
	a := fixture()
	a.openCreateWorkspace()
	a.wsCreate.name, a.wsCreate.slug = "Analytical Engine", "analytical-engine"
	a.wsCreate.checked, a.wsCreate.available = "analytical-engine", true
	tt := snap(t, a, "workspace-create", false, nil)
	for _, want := range []string{"Create New Workspace", "Slug available"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	a.fetches["w1|creation"] = &fetched{value: false}
	tt = snap(t, a, "workspace-limit", false, nil)
	if !tt.HasText("Open lody.ai") {
		t.Fatalf("limit: %q", tt.Texts())
	}
	a.wsCreate.open = false
	a.openJoinWorkspace()
	a.wsJoin.kind = "invite"
	a.wsJoin.preview = map[string]any{"status": "available", "organizationName": "Difference Engine", "inviterName": "Charles", "role": "member"}
	tt = snap(t, a, "workspace-invite", false, nil)
	for _, want := range []string{"Difference Engine", "Accept invitation"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	a.wsJoin.kind = "join"
	a.wsJoin.preview = map[string]any{"status": "available", "workspaceName": "Difference Engine", "viewer": map[string]any{"alreadyMember": true}}
	tt = snap(t, a, "workspace-join", false, nil)
	if !tt.HasText("Difference Engine") {
		t.Fatalf("join: %q", tt.Texts())
	}
	a.wsJoin.open = false
	a.settingsOpen, a.settingsTab = true, "account"
	a.deletion = accountDeletion{open: true}
	tt = snap(t, a, "account-delete", false, nil)
	if !tt.HasText("ada@example.com") {
		t.Fatalf("deletion: %q", tt.Texts())
	}
}

func TestReferrals(t *testing.T) {
	a := fixture()
	a.settingsOpen, a.settingsTab = true, "billing"
	var billing *billingOverview
	json.Unmarshal([]byte(`{"planTier":"plus","effectivePlanTier":"plus","billingInterval":"month","seatCount":2.0,"canManageBilling":true,"referralRewardsSupported":true}`), &billing)
	var overview *referralOverview
	json.Unmarshal([]byte(`{"policy":{"campaignId":"launch","discountPercent":15,"rewardPercent":20},"eligible":true,"code":null,"url":null,"issued":12345,"spent":2000,"available":10345,"frozen":0,"adjustment":0}`), &overview)
	a.fetches = map[string]*fetched{"w1|billing": {value: billing}, "w1|referrals": {value: overview}}
	tt := snap(t, a, "referrals", false, nil)
	for _, want := range []string{"Referrals", "$103.45", "Available", "Details", "Create referral link"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	a.openReferralDetails()
	a.fetches["w1|referral-ledger|"] = &fetched{value: &referralLedger{Page: []referralEntry{
		{ID: "e1", Kind: "reward", Amount: 12345, CreatedAt: 1798761600000},
		{ID: "e2", Kind: "use", Amount: -2000, CreatedAt: 1798848000000},
		{ID: "e3", Kind: "freeze", Frozen: -500, CreatedAt: 1798848000000},
	}, ContinueCursor: "c2"}}
	tt = snap(t, a, "referral-details", false, nil)
	for _, want := range []string{"Referral credit", "Earned", "$123.45", "−$20", "Reward earned", "+$123.45", "Applied to invoice", "Hold released", "$5", "Older", "How it works", "They get 15% off their first payment"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if formatMoney(123456789, "usd") != "$1,234,567.89" || formatMoney(-50000, "usd") != "-$500" {
		t.Fatalf("money: %s %s", formatMoney(123456789, "usd"), formatMoney(-50000, "usd"))
	}
	if d, r, rate := policyValues(&referralPolicy{DiscountPercent: 15, RewardPercent: 25}); d != "15" || r != "25" || rate != "8.5" {
		t.Fatalf("policy values %s %s %s", d, r, rate)
	}
	a.referral.details = false
	a.toast("success", "Referral link created and copied")
	tt = snap(t, a, "toast", false, nil)
	if !tt.HasText("Referral link created and copied") {
		t.Fatalf("toast: %q", tt.Texts())
	}
}

func TestPairingAndDevice(t *testing.T) {
	a := fixture()
	a.pairing = machinePairing{open: true, requestID: "r1", status: "pending", command: "npx lody@latest daemon start --auth lody_pair_x",
		expiresAt: float64(time.Now().Add(29*time.Minute + 30*time.Second).UnixMilli())}
	tt := snap(t, a, "pairing", false, nil)
	for _, want := range []string{"Connect a machine", "Lody Desktop", "Download Lody Desktop", "or", "Command line", "npx lody@latest daemon start --auth lody_pair_x", "Cancel connection request"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if !strings.Contains(strings.Join(tt.Texts(), "|"), "expires in 29:") {
		t.Fatalf("no countdown: %q", tt.Texts())
	}
	a.pairing.status = "claimed"
	if tt = snap(t, a, "pairing-claimed", false, nil); !tt.HasText("Machine is signing in") {
		t.Fatalf("claimed: %q", tt.Texts())
	}
	a.pairing.status, a.pairing.machineID, a.pairing.machine = "registered", "m1", "Studio"
	if tt = snap(t, a, "pairing-done", false, nil); !tt.HasText("Machine connected") || !tt.HasText("Configure agents") {
		t.Fatalf("registered: %q", tt.Texts())
	}
	a.closePairing()
	if a.pairing.requestID != "" || a.pairing.open {
		t.Fatal("a settled request is forgotten on close")
	}
	a.pairing = machinePairing{requestID: "r2", status: "registered", machineID: "m1", machine: "Studio"}
	a.newChat.machine = ""
	a.pairingSettled()
	if a.newChat.machine != "m1" || len(a.toasts) != 1 {
		t.Fatalf("settled: machine %q, toasts %d", a.newChat.machine, len(a.toasts))
	}
	if remaining(61_000) != "1:01" || remaining(-5) != "0:00" {
		t.Fatal("remaining")
	}
	if normalizeDeviceCode("abcd-12ef-9") != "ABCD12EF" {
		t.Fatal(normalizeDeviceCode("abcd-12ef-9"))
	}
	a.pairing = machinePairing{}
	a.device = deviceAuth{open: true, code: "ABCD12", err: "cloud: invalid code"}
	tt = snap(t, a, "device-auth", false, nil)
	for _, want := range []string{"Device Authorization", "Verification Code", "A", "B", "D", "2", "-", "Code is case-insensitive", "cloud: invalid code", "Verify & Authorize"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	a.device = deviceAuth{open: true, success: true}
	if tt = snap(t, a, "device-done", false, nil); !tt.HasText("Authorization successful! You can now return to the CLI to continue.") {
		t.Fatalf("device done: %q", tt.Texts())
	}
}

func TestGitHubProjects(t *testing.T) {
	a := fixture()
	a.settingsOpen, a.settingsTab = true, "projects"
	var repos *[]workspaceRepo
	json.Unmarshal([]byte(`[{"repoFullName":"lbr77/lody-mygo","name":"lody-mygo","private":true,"enabled":true,"worktreeSetup":{"scripts":{"bash":"pnpm install"}}},
		{"repoFullName":"egoist/mygo","name":"mygo","private":false,"enabled":true}]`), &repos)
	a.fetches = map[string]*fetched{"w1|github": {value: repos}}
	tt := snap(t, a, "github-projects", false, nil)
	for _, want := range []string{"egoist", "lbr77", "mygo", "lody-mygo", "Manage in GitHub settings"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	a.openGitHubProject("lbr77/lody-mygo")
	a.ghProject.page = "worktree"
	tt = snap(t, a, "github-project-worktree", false, nil)
	for _, want := range []string{"Worktree Setup", "Worktree Cleanup", "Bash", "PowerShell"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if a.ghProject.setup.bash != "pnpm install" || a.ghProject.cleanup.bash != "" {
		t.Fatalf("scripts loaded: %+v", a.ghProject.setup)
	}
	if !setsEphemeralEnv("bash", "cd x && export FOO=1") || setsEphemeralEnv("bash", "echo export") || !setsEphemeralEnv("powershell", "$env:FOO = 1") {
		t.Fatal("ephemeral env")
	}
	cfg := scriptsConfig("pnpm i", "  ", 5000)
	if s := cfg["scripts"].(map[string]any); s["bash"] != "pnpm i" || s["powershell"] != nil || cfg["timeoutMs"] != int64(5000) {
		t.Fatalf("config %v", cfg)
	}
	a.ghProject.setup.shell = "powershell"
	snap(t, a, "github-project-ps", false, nil)
}

func TestUsageDay(t *testing.T) {
	a := fixture()
	a.settingsOpen, a.settingsTab, a.usageRange = true, "ai-usage", "total"
	var timeline usageTimeline
	json.Unmarshal([]byte(`{"totals":{"tokens":1500000,"costUSD":12.34},"buckets":[{"bucketStartMs":0,"bucketLabel":"Oct 1","tokens":1500000,"costUSD":12.34,"byModel":[{"modelId":"gpt-5","tokens":1500000,"costUSD":12.34}]}]}`), &timeline)
	var days []usageDay
	json.Unmarshal([]byte(`[{"dayStartMs":1798761600000,"date":"2027-01-01","tokens":1000,"costUSD":1},{"dayStartMs":1798848000000,"date":"2027-01-02","tokens":3000,"costUSD":2}]`), &days)
	var detail *usageDayDetail
	json.Unmarshal([]byte(`{"dayStartMs":1798848000000,"totals":{"tokens":3000,"costUSD":2,"inputTokens":1000,"outputTokens":500,"cacheReadInputTokens":1400,"cacheCreationInputTokens":0,"reasoningOutputTokens":100,"webSearchRequests":2},
		"byModel":[{"modelId":"claude-opus-5-5","tokens":3000}],"byUser":[{"userId":"u2","tokens":3000}],"users":{"u2":{"name":"Grace Hopper"}}}`), &detail)
	key := "w1|usage-day/" + time.UnixMilli(1798848000000).Format("2006-01-02")
	a.fetches = map[string]*fetched{"w1|usage/total": {value: &timeline}, "w1|usage-calendar": {value: days}, key: {value: detail}}
	a.usageDay = 1798848000000
	tt := snap(t, a, "usage-day", false, nil)
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	for _, want := range []string{"3K", "Tokens", "$2.00 · 2 web searches", "Cache", "Input", "Output", "Reasoning output", "claude-opus-5-5", "Grace Hopper"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
}

func TestShareDialog(t *testing.T) {
	a := fixture()
	a.catalog.Sessions = append(a.catalog.Sessions,
		catalog.Session{ID: "s5", MachineID: "m1", Title: "Sub-agent: tests", ParentSessionID: "s1"},
		catalog.Session{ID: "s6", MachineID: "m1", Title: "Opened from tests", OpenedBySessionID: "s5"})
	if c := shareCandidates("s1", a.catalog.Sessions); len(c) != 3 || c[0].ID != "s1" || c[1].ID != "s5" || c[2].ID != "s6" {
		t.Fatalf("candidates %+v", c)
	}
	a.openShare("s1")
	job := a.share.job
	tt := snap(t, a, "share-loading", false, nil)
	if !tt.HasText("Share") || !tt.HasText("Port the Flock runtime to Go") {
		t.Fatalf("loading: %q", tt.Texts())
	}
	job.loaded = true
	tt = snap(t, a, "share-setup", false, nil)
	for _, want := range []string{"Anyone with the link can view this conversation.", "Also share 2 sub-conversations", "Images are also shared. File attachments are not included.", "Share conversation", "Cancel"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if !a.canCapture(job) {
		t.Fatal("the conversation alone can be captured")
	}
	job.selected = []string{"s1", "s5"}
	tt = snap(t, a, "share-partial", false, nil)
	if !tt.HasText("Also share sub-conversations (1 of 2)") {
		t.Fatalf("partial: %q", tt.Texts())
	}
	job.entry = &shareEntry{ShareID: "sh1", RootSessionID: "s1", PublisherUserID: "u1", Status: "active", Revision: 2, CredentialVersion: 1, CanManage: true, CanRevoke: true}
	tt = snap(t, a, "share-active", false, nil)
	for _, want := range []string{"Update share", "Revoke share", "The link credential is not saved on this device.", "Reset link"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	job.phase, job.progress = "uploading", 42
	if tt = snap(t, a, "share-uploading", false, nil); !tt.HasText("Uploading the copy… 42%") {
		t.Fatalf("uploading: %q", tt.Texts())
	}
	job.phase, job.result = "", &shareResult{url: "https://share.lody.ai/s/sh1#access=v1." + strings.Repeat("ab", 32), copied: true}
	if tt = snap(t, a, "share-published", false, nil); !tt.HasText("Shared. Link copied to your clipboard.") || !tt.HasText("Copy link") {
		t.Fatalf("published: %q", tt.Texts())
	}
	a.share.confirming, a.share.confirmRev = "revoke", 2
	if tt = snap(t, a, "share-confirm", false, nil); !tt.HasText("The previous link will stop working. Downloaded copies cannot be recalled.") || !tt.HasText("Confirm") {
		t.Fatalf("confirm: %q", tt.Texts())
	}
	a.share = shareDialog{}
	a.fetches["w1|share-requests/s1"] = &fetched{value: []shareRequest{{RequestID: "r1", SourceSessionID: "s1", SessionIDs: []string{"s1"}, Purpose: "Show the design review", Status: "pending"}}}
	a.shareReqAt = map[string]time.Time{"s1": time.Now()}
	a.view.Entries = a.view.Entries[:2]
	tt = snap(t, a, "share-request", false, nil)
	for _, want := range []string{"Share this conversation?", "Purpose: Show the design review", "Do not share", "Approve and share"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
}

func TestProjectFiles(t *testing.T) {
	a := fixture()
	a.presence = presence.State{Live: true, Online: []string{"m1"}}
	a.settingsOpen, a.settingsTab = true, "projects"
	a.openProject("m1:local:p1")
	a.projectWin.page = "files"
	a.projectWin.files = &inspectState{session: "project:m1:local:p1", dirs: map[string]*dirListing{
		"": {entries: []collab.DirEntry{{Name: "internal", Type: "directory"}, {Name: "go.mod", Type: "file"}}},
	}, open: map[string]bool{}, changes: map[string]*changesState{}, changed: map[string]bool{"go.mod": true}}
	tt := snap(t, a, "project-files", false, nil)
	if !tt.HasText("Files") || !tt.HasText("internal") || !tt.HasText("go.mod") {
		t.Fatalf("files: %q", tt.Texts())
	}
}

func TestSidebarRowArchive(t *testing.T) {
	a := fixture()
	tt := snap(t, a, "sidebar-row", false, nil)
	r, ok := tt.Find("Sidebar status marks")
	if !ok {
		t.Fatal("no row")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Archive") {
		t.Fatalf("no archive on hover: %q", tt.Texts())
	}
	a.archiveArm, a.archiveArmAt = "s2", time.Now()
	tt.Frame()
	if !tt.HasText("Confirm Archive session") {
		t.Fatalf("not armed: %q", tt.Texts())
	}
	a.archiveArmAt = time.Now().Add(-4 * time.Second)
	tt.Frame()
	if a.archiveArm != "" {
		t.Fatal("the confirmation outlives 3.5s")
	}
}

func TestNoMachineHint(t *testing.T) {
	a := fixture()
	a.catalog.MachineIDs = nil
	a.selected = ""
	tt := snap(t, a, "no-machine", false, func(tt *ui.Tester) { tt.Advance(2300 * time.Millisecond) })
	if !(tt.HasText("Nothing's plugged in yet") || tt.HasText("Plugging in…")) || !tt.HasText("Discord") {
		t.Fatalf("hint: %q", tt.Texts())
	}
}

func TestPasswordSteps(t *testing.T) {
	a := fixture()
	a.settingsOpen, a.settingsTab = true, "account"
	a.fetches = map[string]*fetched{"w1|accounts": {value: []cloud.LinkedAccount{{ID: "a1", ProviderID: "credential"}}}}
	a.password = passwordForm{open: true}
	tt := snap(t, a, "password-current", false, func(tt *ui.Tester) { tt.Advance(300 * time.Millisecond) })
	if !tt.HasText("First, enter your current password.") || !tt.HasText("Continue") {
		t.Fatalf("step 0: %q", tt.Texts())
	}
	a.password.step, a.password.dir, a.password.run = 1, 1, 1
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Now choose a new password. You'll be signed out afterwards.") || !tt.HasText("Back") {
		t.Fatalf("step 1: %q", tt.Texts())
	}
}

func TestMCPSelection(t *testing.T) {
	a := fixture()
	a.catalog.MCPServers = []catalog.MCPServer{
		{ID: "x1", Name: "IDA Pro", Transport: "stdio", Connection: map[string]any{"command": "uvx", "args": []any{"ida-mcp"}}, EnabledByDefault: true},
		{ID: "x2", Name: "Notes", Transport: "http", Connection: map[string]any{"url": "https://notes.example/mcp"}, Description: "A note server"},
	}
	if got := a.defaultMCP(""); len(got) != 1 || got[0] != "x1" {
		t.Fatalf("workspace defaults %v", got)
	}
	a.catalog.ProjectMCP = map[string][]string{catalog.ProjectMCPKey("m1", "p1"): {"x2", "gone"}}
	if got := a.defaultMCP("m1:local:p1"); len(got) != 1 || got[0] != "x2" {
		t.Fatalf("project defaults %v", got)
	}
	a.selected = ""
	a.newChat.machine, a.newChat.project = "m1", "m1:local:p1"
	a.pickers = map[string]*bool{"plus": new(bool)}
	*a.pickers["plus"] = true
	tt := snap(t, a, "plus-menu", false, nil)
	if !tt.HasText("Add attachment") || !tt.HasText("Load 1 MCP server") {
		t.Fatalf("root: %q", tt.Texts())
	}
	a.plusLevel = "mcp"
	tt.Frame()
	if !tt.HasText("MCP servers") || !tt.HasText("A note server") || !tt.HasText("uvx ida-mcp") {
		t.Fatalf("mcp level: %q", tt.Texts())
	}
	if err := tt.Click("IDA Pro"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.mcpSelection(""); len(got) != 2 {
		t.Fatalf("after ticking: %v", got)
	}
	a.newChat.project = "m1:local:p2"
	if got := a.mcpSelection(""); len(got) != 1 || got[0] != "x1" {
		t.Fatalf("a new project starts from its defaults: %v", got)
	}
}

func TestPullRequestMarks(t *testing.T) {
	a := fixture()
	s := &a.catalog.Sessions[1]
	s.LastMessageAt, s.LastReadAt = 1, 1
	s.PullRequests = []catalog.PullRequest{{URL: "https://github.com/lbr77/lody-mygo/pull/7", Status: "open", ReportedAt: "2026-10-29T09:00:00Z"}}
	s.PRState = map[string]catalog.PRState{"https://github.com/lbr77/lody-mygo/pull/7": {CI: "f", Merge: "c"}}
	tt := snap(t, a, "pr-mark", false, nil)
	if !tt.HasText("Open PR #7") {
		t.Fatalf("no PR mark: %q", tt.Texts())
	}
	if s.LatestPR().PRNumber() != 7 || s.PRState[s.LatestPR().URL].Ready() {
		t.Fatal("PR facts")
	}
}

func TestHomeDashboard(t *testing.T) {
	a := fixture()
	a.selected = ""
	now := time.Now()
	ms := func(d time.Duration) float64 { return float64(now.Add(-d).UnixMilli()) }
	created := now.Add(-2 * 24 * time.Hour).UTC().Format(time.RFC3339)
	for i := range a.catalog.Sessions {
		s := &a.catalog.Sessions[i]
		s.UserID, s.CreatedAt = "u1", created
	}
	a.catalog.Sessions[0].LastMessageAt = ms(time.Minute)      // running
	a.catalog.Sessions[1].LastMessageAt = ms(30 * time.Minute) // finished, unread
	a.catalog.Sessions[2].LastMessageAt = ms(time.Hour)        // waiting
	a.catalog.Sessions[3].LastMessageAt, a.catalog.Sessions[3].LastReadAt = ms(2*time.Hour), ms(time.Hour)
	a.catalog.Sessions[3].PullRequests = []catalog.PullRequest{{URL: "https://github.com/o/r/pull/12", Status: "open"}}
	a.catalog.Sessions[0].DiffAdd, a.catalog.Sessions[0].DiffDel = 1200, 300
	a.catalog.Sessions[1].PullRequests = []catalog.PullRequest{{URL: "https://github.com/o/r/pull/3", Status: "merged"}}
	lists := a.homeNow(now)
	if len(lists["attention"].items) != 1 || len(lists["running"].items) != 1 || len(lists["finished"].items) != 1 || len(lists["next"].items) != 1 {
		t.Fatalf("lists: %d %d %d %d", len(lists["attention"].items), len(lists["running"].items), len(lists["finished"].items), len(lists["next"].items))
	}
	if lists["next"].items[0].reason != reasonOpenPR || lists["next"].items[0].pr != 12 {
		t.Fatalf("next: %+v", lists["next"].items[0])
	}
	w := a.homeWeek(now)
	if w.lines != 1500 || w.added != 1200 || w.mergedPRs != 1 || w.sessions != 4 || len(w.wall) != wallWeeks {
		t.Fatalf("week: %+v", w)
	}
	a.homeBoard = "now"
	tt := snap(t, a, "home-now", false, nil)
	for _, want := range []string{"Now", "Last 7 days", "Needs you", "Running", "Just finished", "Worth continuing", "Needs your reply", "In review · #12", "1,500"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	a.homeBoard = "week"
	tt = snap(t, a, "home-week", false, nil)
	for _, want := range []string{"Code shipped, last 7 days", "+1,200", "−300", "PRs merged", "Agents", "Most-used repository"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if got := allocateNowRows(map[string]int{"attention": 10, "running": 1, "finished": 0, "next": 5}, 12); got["attention"] != 6 || got["running"] != 1 || got["next"] != 3 {
		t.Fatalf("allocation %v", got)
	}
	if d := diffSquares(1200, 300); d != [5]int{1, 1, 1, 1, 2} {
		t.Fatalf("squares %v", d)
	}
}

func TestWorkspaceModeGate(t *testing.T) {
	a := fixture()
	a.convex = &cloud.Convex{}
	a.fetches = map[string]*fetched{"w1|mode": {value: map[string]any{"workspaceId": "w1", "contentMode": "plaintext", "contentModeVersion": 1.0}}}
	tt := snap(t, a, "mode-plain", false, nil)
	if tt.HasText("This workspace is end-to-end encrypted") {
		t.Fatal("a plaintext workspace is gated")
	}
	a.fetches["w1|mode"] = &fetched{value: map[string]any{"workspaceId": "w1", "contentMode": "e2ee", "activation": "control-only"}}
	tt = snap(t, a, "mode-e2ee", false, nil)
	if !tt.HasText("This workspace is end-to-end encrypted") || tt.HasText("Port the Flock catalog runtime from the iOS client to Go, without a web view.") {
		t.Fatalf("not gated: %q", tt.Texts())
	}
	a.fetches["w1|mode"] = &fetched{value: map[string]any{"workspaceId": "w1", "contentMode": "quantum"}}
	if tt = snap(t, a, "mode-unknown", false, nil); !tt.HasText("Workspace mode could not be verified") {
		t.Fatalf("unknown mode not gated: %q", tt.Texts())
	}
}

func TestPRPanel(t *testing.T) {
	a := fixture()
	s := &a.catalog.Sessions[0]
	s.PullRequests = []catalog.PullRequest{{URL: "https://github.com/lbr77/lody-mygo/pull/12", Status: "open"}}
	in := a.inspectFor("s1")
	in.openPanel(tabPR)
	a.panelOpen = true
	st := a.prPanelFor(s)
	mergeable := true
	st.pr = &github.PullRequest{Number: 12, Title: "Port the Flock runtime", Body: "Moves **Flock** to wazero.", State: "open", Mergeable: &mergeable, MergeableState: "clean",
		HTMLURL: "https://github.com/lbr77/lody-mygo/pull/12", User: github.User{Login: "ada"}, CreatedAt: time.Now().Add(-2 * time.Hour), Additions: 120, Deletions: 4, ChangedFiles: 1, Commits: 3}
	st.pr.Head.Ref, st.pr.Base.Ref = "flock-go", "main"
	st.checks = []github.CheckRun{{ID: 1, Name: "test", Status: "completed", Conclusion: "success"}, {ID: 2, Name: "lint", Status: "in_progress"}}
	st.reviews = []github.Review{{ID: 9, User: github.User{Login: "grace"}, State: "APPROVED", SubmittedAt: time.Now().Add(-time.Hour)}}
	st.comments = []github.Comment{{ID: 5, User: github.User{Login: "ada"}, Body: "Ready for a look.", CreatedAt: time.Now().Add(-90 * time.Minute)}}
	st.files = []github.File{{Filename: "internal/flock/flock.go", Status: "modified", Additions: 120, Deletions: 4, Patch: "@@ -1,2 +1,3 @@\n package flock\n+// wazero\n-old"}}
	tt := snap(t, a, "pr-panel", false, nil)
	for _, want := range []string{"Pull request", "Open", "Port the Flock runtime", " #12", "flock-go", "main", "3 commits", "Checks running", "2 checks", "grace", "approved", "Ready for a look.", "Ready to merge", "Merge pull request", "Leave a comment"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	st.view = "changes"
	st.open["file/internal/flock/flock.go"] = true
	tt.Frame()
	if !tt.HasText("internal/flock/flock.go") || !tt.HasText("+// wazero") {
		t.Fatalf("changes: %q", tt.Texts())
	}
}

func TestGitHubRepoTargets(t *testing.T) {
	a := fixture()
	a.convex = &cloud.Convex{}
	a.fetches = map[string]*fetched{"w1|github-repos": {value: []workspaceRepository{{ID: 1, Name: "lody-mygo", FullName: "lbr77/lody-mygo"}}}}
	found := false
	for _, o := range a.projectOptions() {
		found = found || o.id == "github:lbr77/lody-mygo"
	}
	if !found {
		t.Fatalf("no repo target: %+v", a.projectOptions())
	}
}
