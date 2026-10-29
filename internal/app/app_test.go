package app

import (
	"strings"
	"time"

	"github.com/egoist/mygo/i18n"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/collab"
	"github.com/lbr77/lody-mygo/internal/mention"
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
	tt.Frame()
	// Thinking and tool calls fold into one row.
	if err := tt.Click("Thought · Ran 1 command · Read 1 file"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	if tt.HasText("The snapshot format is only in the wasm build.") {
		t.Fatal("thinking shows before it is clicked")
	}
	if err := tt.Click("Thought"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
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
	if got := tt.Menu(); len(got) == 0 {
		t.Fatal("no context menu")
	}
	if err := tt.ChooseMenuItem("Rename"); err != nil {
		t.Fatalf("%v; menu: %q", err, tt.Menu())
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
		tt.Frame()
		if err := tt.Click("Edited 1 file"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
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
	for _, want := range []string{"notes.md", "Remove screenshot.png", "Attach files"} {
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
	a.view.Entries = append(a.view.Entries, session.Entry{ID: "q1", Role: "user", Status: "queued", Queued: true, Timestamp: "2026-10-29T09:21:00Z",
		Items: []session.Item{{ID: "t", Type: "text", Text: "Also update the README."}}})
	tt := snap(t, a, "queued", true, nil)
	for _, want := range []string{"Queued", "Steer", "Send now", "Permission mode: Bypass Permissions"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Permission mode: Bypass Permissions"); err != nil {
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
	if !tt.HasText("Claude Code") || !tt.HasText("claude · builtin · 6 modes") {
		t.Fatalf("agents: %q", tt.Texts())
	}
}

// The app speaks the system's language; tests pin English but this one.
func TestMain(m *testing.M) {
	i18n.SetLocaleFunc(func() string { return "en" })
	os.Exit(m.Run())
}

func TestChinese(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	defer i18n.SetLocaleFunc(func() string { return "en" })
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	tt := snap(t, a, "chinese", false, nil)
	for _, want := range []string{"新对话", "搜索", "已置顶", "工作了1分12秒", "添加附件", "权限模式：默认", "复制"} {
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
	for _, want := range []string{"Waiting on 2 background tasks", "Bash", "Run full make test twice on P7", "Wait for both P7 gate runs", "6m 49s", "Running sleep 60", "Claude Code", "Show more"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
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
	for _, want := range []string{"正在等待 2 个后台任务", "6分49秒", "展开"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
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
	tt := snap(t, a, "changes-panel", false, func(tt *ui.Tester) {
		tt.Frame()
		if err := tt.Click("Show panel"); err != nil {
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
	if err := tt.Click("Files"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("README.md") || tt.HasText("app") {
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
	if !tt.HasText("localhost:5173") || !tt.HasText("Open preview") {
		t.Fatalf("start: %q", tt.Texts())
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
