package app

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
)

func TestCommandPalette(t *testing.T) {
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	a.catalog.Sessions[0].BranchName = "lody/flock"
	tt := snap(t, a, "palette", false, func(tt *ui.Tester) {
		tt.Frame()
		tt.Key(ui.Cmd, ui.KeyK)
	})
	if !a.palette.open {
		t.Fatal("⌘K left the palette closed")
	}
	for _, want := range []string{"Navigation", "Session", "View", "Back", "Copy Current Branch", "Archive Current Chat", "Navigate", "Select", "esc"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	// Commands sit under their category, in Lody's order.
	nav, _ := tt.Find("Navigation")
	sess, _ := tt.Find("Session")
	if nav.Y >= sess.Y {
		t.Errorf("Navigation at %v, Session at %v", nav, sess)
	}
	// Commands that don't apply stay out: one tab, nothing to save.
	for _, gone := range []string{"Switch to Tab 2", "Switch to Next Tab", "Save Current File"} {
		if tt.HasText(gone) {
			t.Errorf("%q listed", gone)
		}
	}

	// A query finds chats by title and project, and runs the first match.
	tt.Type("flaky")
	tt.Frame()
	if !tt.HasText("Fix the flaky upload test") || !tt.HasText("website") || tt.HasText("Navigation") {
		t.Fatalf("query: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.palette.open || a.selected != "s3" {
		t.Fatalf("Enter: open %v, selected %q", a.palette.open, a.selected)
	}

	// The sidebar's Search opens it too, empty; Escape closes it.
	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.palette.open || a.palette.query != "" {
		t.Fatalf("Search: open %v, query %q", a.palette.open, a.palette.query)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.palette.open {
		t.Fatal("Escape left the palette open")
	}
}

func TestPaletteKeys(t *testing.T) {
	a := fixture()
	a.view.Working = false
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	a.setPaletteOpen(true)
	tt.Frame()
	first := a.palette.active
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if a.palette.active == first {
		t.Fatalf("Down kept %q", first)
	}
	tt.Key(0, ui.KeyUp)
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	results := a.paletteResults()
	if a.palette.active != results[len(results)-1].key {
		t.Fatalf("Up from the first row went to %q, not the last", a.palette.active)
	}
	tt.Type("toggle side")
	tt.Frame()
	if results = a.paletteResults(); len(results) == 0 || results[0].title != "Toggle Sidebar" || a.palette.active != results[0].key {
		t.Fatalf("query results %v, active %q", results, a.palette.active)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !a.sidebarHidden || a.palette.open {
		t.Fatalf("Enter: sidebar hidden %v, palette open %v", a.sidebarHidden, a.palette.open)
	}
}

func TestCommandShortcuts(t *testing.T) {
	a := fixture()
	a.view.Working = false
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyB)
	tt.Frame()
	if !a.sidebarHidden {
		t.Fatal("⌘B left the sidebar shown")
	}
	tt.Key(ui.Cmd, ui.KeyB)
	tt.Key(ui.Cmd|ui.Alt, ui.KeyB)
	tt.Frame()
	if a.sidebarHidden || !a.panelOpen {
		t.Fatalf("⌘⌥B: sidebar hidden %v, panel %v", a.sidebarHidden, a.panelOpen)
	}
	// Zen puts both away and brings them back as they were.
	tt.Key(ui.Cmd, ui.KeyPeriod)
	tt.Frame()
	if !a.sidebarHidden || a.panelOpen {
		t.Fatalf("Zen: sidebar hidden %v, panel %v", a.sidebarHidden, a.panelOpen)
	}
	tt.Key(ui.Cmd, ui.KeyPeriod)
	tt.Frame()
	if a.sidebarHidden || !a.panelOpen {
		t.Fatalf("out of Zen: sidebar hidden %v, panel %v", a.sidebarHidden, a.panelOpen)
	}
	var copied string
	writeClipboard = func(s string) { copied = s }
	defer func() { writeClipboard = mygo.Clipboard.WriteText }()
	tt.Key(ui.Alt|ui.Shift, ui.KeyC)
	tt.Frame()
	if got := copied; got != "lody://session/s1?workspace=w1" {
		t.Fatalf("⌥⇧C copied %q", got)
	}
	// ⌘⇧] steps down the sidebar's sessions; ⌘[ goes back.
	tt.Key(ui.Cmd|ui.Shift, ui.KeyBracketRight)
	tt.Frame()
	next := a.selected
	if next == "s1" {
		t.Fatal("⌘⇧] stayed on s1")
	}
	tt.Key(ui.Cmd, ui.KeyBracketLeft)
	tt.Frame()
	if a.selected != "s1" {
		t.Fatalf("⌘[ went to %q", a.selected)
	}
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if !a.settingsOpen {
		t.Fatal("⌘, left settings closed")
	}
}

func TestPaletteChinese(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	defer i18n.SetLocaleFunc(func() string { return "en" })
	a := fixture()
	a.view.Working = false
	tt := snap(t, a, "palette-chinese", true, func(tt *ui.Tester) {
		tt.Frame()
		a.setPaletteOpen(true)
	})
	for _, want := range []string{"导航", "会话", "后退", "前进", "切换到标签 1", "复制当前链接", "切换", "选择", "关闭"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	// Titles sort by pinyin, as Lody's: hòu, qián, qiē.
	var nav []string
	for _, r := range a.paletteResults() {
		if r.group == "导航" {
			nav = append(nav, r.title)
		}
	}
	// Switching sessions stays out while the palette is up, as Lody's.
	if want := []string{"后退", "前进", "切换到标签 1", "切换到最后一个标签"}; strings.Join(nav, "|") != strings.Join(want, "|") {
		t.Errorf("导航: %q", nav)
	}
}

func TestFuzzyMatch(t *testing.T) {
	prefix, _ := fuzzyMatch("arch", "Archive Current Chat")
	sub, _ := fuzzyMatch("chat", "Archive Current Chat")
	seq, _ := fuzzyMatch("acc", "Archive Current Chat")
	if !(prefix > sub && sub > seq) {
		t.Errorf("prefix %d, substring %d, subsequence %d", prefix, sub, seq)
	}
	if _, ok := fuzzyMatch("xyz", "Archive Current Chat"); ok {
		t.Error("xyz matched")
	}
	if score, ok := fuzzyMatch("  ", "anything"); !ok || score != 0 {
		t.Errorf("empty query: %d %v", score, ok)
	}
}

func TestCompactAge(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{
		5 * time.Minute: "5m", 3 * time.Hour: "3h", 50 * time.Hour: "2d", 10 * 24 * time.Hour: "1w",
		40 * 24 * time.Hour: "1mo", 400 * 24 * time.Hour: "1y", -time.Hour: "0m",
	} {
		if got := compactAge(now.Add(-d), now); got != want {
			t.Errorf("%v ago: %q, want %q", d, got, want)
		}
	}
}

func TestPaletteSearchSnapshot(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.catalog.Sessions[1].BranchName = "lody/marks"
	a.catalog.Sessions[1].LastMessageAt = float64(time.Now().Add(-3 * time.Hour).UnixMilli())
	tt := snap(t, a, "palette-search", false, func(tt *ui.Tester) {
		tt.Frame()
		a.setPaletteOpen(true)
		tt.Frame()
		tt.Type("s")
	})
	if !tt.HasText("Sidebar status marks") || !tt.HasText("lody-mygo · lody/marks") || !tt.HasText("3h") {
		t.Errorf("results: %q", tt.Texts())
	}
	a.palette.query = "zzzz"
	tt.Frame()
	if !tt.HasText("No results found.") {
		t.Errorf("empty: %q", tt.Texts())
	}
}

func TestKeyboardSettings(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.selected = ""
	a.settingsOpen, a.settingsTab = true, "keyboard"
	tt := snap(t, a, "settings-keyboard", false, nil)
	// Every command, a session's too with none open.
	for _, want := range []string{"Navigation", "Session", "Editor", "View", "Workspace", "Toggle Sidebar", "Copy Current Branch", "Switch to Tab 8", "Cycle Model", "Unbound"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestEffortAndFast(t *testing.T) {
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	a.view.ConfigValues = map[string]any{"reasoning_effort": "high"}
	a.catalog.Capabilities = map[string]catalog.Capability{"a1": {
		Models: []catalog.Model{{ModelID: "claude-opus-5-5", Name: "Opus 5.5"}, {ModelID: "claude-haiku-5-5", Name: "Haiku 5.5"}},
		Options: []catalog.ConfigOption{
			{ID: "reasoning_effort", Category: "thought_level", Type: "select", Current: "medium", Choices: []catalog.Choice{{Value: "low", Name: "Low"}, {Value: "medium", Name: "Medium"}, {Value: "high", Name: "High"}}},
			{ID: "fast", Type: "boolean", Current: false},
		},
		Efforts: map[string][]string{"claude-haiku-5-5": {"low"}},
	}}
	a.catalog.Sessions[0].AgentConfigID = "a1"
	tt := snap(t, a, "composer-effort", false, nil)
	// The last turn's effort shows; Fast is off.
	if !tt.HasText("High") {
		t.Fatalf("effort: %q", tt.Texts())
	}
	a.runCommand("session.cycleThinkEffort")
	tt.Frame()
	if got := a.runValues["s1"]["reasoning_effort"]; got != "low" || !tt.HasText("Low") {
		t.Fatalf("cycled to %v: %q", got, tt.Texts())
	}
	if err := tt.Click("Accelerated mode: 2.5x faster at a higher cost per token"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.runValues["s1"]["fast"] != true {
		t.Fatalf("Fast: %v", a.runValues["s1"])
	}
	// An effort the model doesn't allow stays out of the message.
	a.runValues["s1"]["reasoning_effort"] = "high"
	if got := a.chosenRunValues("s1", "a1", "claude-haiku-5-5"); got["reasoning_effort"] != nil || got["fast"] != true {
		t.Fatalf("haiku: %v", got)
	}
}
