package app

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/session"
)

func TestFindInChat(t *testing.T) {
	a := fixture()
	a.view.Working = false
	tt := snap(t, a, "find", false, func(tt *ui.Tester) {
		tt.Frame()
		tt.Key(ui.Cmd, ui.KeyF)
		tt.Frame()
		tt.Type("wasm")
		tt.Frame()
	})
	if !a.find.open {
		t.Fatal("⌘F left find closed")
	}
	// The thought and the answer, never the tool calls.
	if n := len(a.find.results); n != 3 || !tt.HasText("1 / 3") {
		t.Fatalf("wasm: %d results, %q", n, tt.Texts())
	}
	// The first sits in a thought folded under Worked for: it opens.
	if r := a.find.results[0]; !a.expanded[r.folds[len(r.folds)-1]] || !a.expanded["r1/work"] {
		t.Fatalf("folds %v not opened: %v", r.folds, a.expanded)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.find.active != 1 || !tt.HasText("2 / 3") {
		t.Fatalf("Enter: active %d, %q", a.find.active, tt.Texts())
	}
	tt.Key(ui.Shift, ui.KeyEnter)
	tt.Key(ui.Shift, ui.KeyEnter)
	tt.Frame()
	if a.find.active != 2 {
		t.Fatalf("⇧Enter twice from the second: %d", a.find.active)
	}
	// Escape clears the query, then closes.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.find.query != "" || !a.find.open {
		t.Fatalf("Escape: query %q, open %v", a.find.query, a.find.open)
	}
	tt.Type("nothing like it")
	tt.Frame()
	if !tt.HasText("0 / 0") {
		t.Fatalf("no results: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.find.open {
		t.Fatal("Escape twice left find open")
	}
}

func TestFindMarkdown(t *testing.T) {
	src := "Flock now runs, see [flock](https://flock.dev) and `flock.Run`.\n\n```go\nflock.FromFile()\n```\n<b>Flock</b>"
	q := normalizedQuery(" FLOCK ")
	ranges, markable := markdownOccurrences(src, q)
	// Text, link text, code span, fenced code, text inside a tag; not the URL.
	if len(ranges) != 5 {
		t.Fatalf("%d occurrences: %v", len(ranges), ranges)
	}
	want := []bool{true, true, false, false, true}
	for i, m := range markable {
		if m != want[i] {
			t.Errorf("occurrence %d markable %v", i, m)
		}
	}
	got := markMarkdown(src, q, 1)
	wantSrc := "<mark>Flock</mark> now runs, see [<mark><u>flock</u></mark>](https://flock.dev) and `flock.Run`.\n\n```go\nflock.FromFile()\n```\n<b><mark>Flock</mark></b>"
	if got != wantSrc {
		t.Errorf("marked:\n%s\nwant:\n%s", got, wantSrc)
	}
	if o := occurrences("ÉCOLE école", normalizedQuery("école")); len(o) != 2 || o[1][0] != len("ÉCOLE ") {
		t.Errorf("case folding: %v", o)
	}
}

func TestUserMessageChips(t *testing.T) {
	a := fixture()
	a.view.Working = false
	text := "你需要 发送回 [@Sidebar status marks](lody://session/s2?workspace=w1)"
	start := strings.Index(text, "[@")
	a.view.Entries = a.view.Entries[:2]
	a.view.Entries[0].Items = []session.Item{{ID: "t", Type: "text", Text: text, Spans: []session.TextSpan{{Start: start, End: len(text), Kind: "session", Label: "@Sidebar status marks", Target: "s2"}}}}
	tt := snap(t, a, "user-chips", false, nil)
	// The chip shows what was picked, not the link the agent read.
	if tt.HasText(text) || !tt.HasText("@Sidebar status marks") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if err := tt.Click("@Sidebar status marks"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.selected != "s2" {
		t.Fatalf("chip opened %q", a.selected)
	}
}

func TestSessionLinkSpans(t *testing.T) {
	text := `see [@计划 \[v2\]](lody://session/a71ad127-fd8b?workspace=jh7d) and [docs](https://x.y)`
	spans := sessionLinkSpans(text)
	if len(spans) != 1 || spans[0].Label != "@计划 [v2]" || spans[0].Target != "a71ad127-fd8b" || text[spans[0].End-1] != ')' {
		t.Fatalf("%+v", spans)
	}
}

func TestProposedPlan(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.view.Entries = a.view.Entries[:2]
	plan := "## Plan\n\n" + strings.Repeat("- Step that does a thing\n", 30)
	a.view.Entries[1].Items = append(a.view.Entries[1].Items, session.Item{ID: "pp", Type: "proposed_plan", Text: plan, Status: "completed", Latest: true})
	a.catalog.Sessions[0].AgentType, a.catalog.Sessions[0].Status = "codex", "idle"
	tt := snap(t, a, "proposed-plan", false, nil)
	for _, want := range []string{"Proposed Plan", "Implement plan", "Continue discussing"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	// Awaiting the decision, the plan opens in full.
	if !tt.HasText("Step that does a thing") {
		t.Fatal("plan body not shown")
	}
	if err := tt.Click("Continue discussing"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Implement plan") {
		t.Fatal("Continue discussing left the decision")
	}
	// A Claude session is never asked.
	a.plan.dismissed = nil
	a.catalog.Sessions[0].AgentType = "claude"
	tt.Frame()
	if tt.HasText("Implement plan") {
		t.Fatal("decision offered outside Codex")
	}
}

func TestFailedActivityMark(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.view.Entries = a.view.Entries[:2]
	a.view.Entries[1].Items[2].Status = "failed"
	tt := snap(t, a, "activity-failed", false, func(tt *ui.Tester) {
		tt.Frame()
		a.expanded["r1/work"] = true
	})
	if !tt.HasText("Worked for 1m 12s") {
		t.Fatalf("%q", tt.Texts())
	}
}

func TestQuickReplies(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.view.Entries = a.view.Entries[:2]
	tt := snap(t, a, "quick-replies", false, nil)
	for _, want := range []string{"Continue", "Review Changes", "Commit & Push"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q", want)
		}
	}
	tt.Type("x")
	tt.Frame()
	if tt.HasText("Review Changes") {
		t.Fatal("typing left the quick replies")
	}
	// Edited, they replace the defaults, and keep their order.
	a.setQuickReplies([]quickReply{{ID: "a", Label: "Ship it", Message: "Merge when green."}})
	a.draft = ""
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Ship it") || tt.HasText("Continue") {
		t.Fatalf("%q", tt.Texts())
	}
	if validQuickReply(quickReply{Label: strings.Repeat("x", 41), Message: "m"}) || validQuickReply(quickReply{Label: "x", Message: "  "}) {
		t.Error("invalid quick reply accepted")
	}
}

func TestFreeQuota(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.view.Entries = a.view.Entries[:2]
	a.tier = planTier{workspace: "w1", tier: "free", at: time.Now()}
	for i := 0; i < 26; i++ {
		a.view.Entries = append(a.view.Entries, session.Entry{ID: fmt.Sprint("u", i+10), Role: "user", Status: "handled", Items: []session.Item{{ID: "t", Type: "text", Text: "again"}}})
	}
	tt := snap(t, a, "free-quota", false, nil)
	// 27 turns: three left.
	if !tt.HasText("Free session · 3 messages left.") {
		t.Fatalf("%q", tt.Texts())
	}
	for i := 0; i < 3; i++ {
		a.view.Entries = append(a.view.Entries, session.Entry{ID: fmt.Sprint("v", i), Role: "user", Status: "handled"})
	}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Free session · 30-message limit reached.") || !tt.HasText("Free session limit") || tt.HasText("Add attachment") {
		t.Fatalf("locked: %q", tt.Texts())
	}
	// Plus has no quota.
	a.tier.tier = "plus"
	a.confirming.open = false
	tt.Frame()
	if tt.HasText("Free session · 30-message limit reached.") {
		t.Fatal("quota on Plus")
	}
}

func TestContextChips(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.view.Entries = a.view.Entries[:2]
	a.view.Preview = &session.Preview{Candidate: "available", Target: session.PreviewTarget{Protocol: "http", Host: "localhost", Port: 5173}}
	a.view.Entries[1].Items = append(a.view.Entries[1].Items, session.Item{ID: "task", Type: "subagent_task", Subagent: &session.Subagent{TaskID: "t1", Description: "Index the repo", State: "running"}})
	tt := snap(t, a, "context-chips", false, nil)
	if !tt.HasText("localhost:5173") || !tt.HasText("1 subtask") || !tt.HasText("Continue") {
		t.Fatalf("%q", tt.Texts())
	}
	// They stay as the user types; the quick replies go.
	tt.Type("x")
	tt.Frame()
	if !tt.HasText("localhost:5173") || tt.HasText("Review Changes") {
		t.Fatalf("typing: %q", tt.Texts())
	}
	if err := tt.Click("Open preview localhost:5173"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if in := a.inspectFor("s1"); !a.panelOpen || in.tab != tabPreview {
		t.Fatalf("panel %v tab %q", a.panelOpen, in.tab)
	}
	// A server off the loopback is never offered.
	a.view.Preview.Target.Host = "example.com"
	if _, _, ok := previewResource(a.view.Preview); ok {
		t.Fatal("non-loopback preview offered")
	}
}

func TestModelMemory(t *testing.T) {
	a := fixture()
	a.selected = ""
	a.catalog.Capabilities = map[string]catalog.Capability{"a1": {
		Models: []catalog.Model{{ModelID: "opus"}, {ModelID: "haiku"}},
		Modes:  []catalog.Mode{{ID: "default"}, {ID: "plan"}},
		Options: []catalog.ConfigOption{{ID: "reasoning_effort", Category: "thought_level", Type: "select", Choices: []catalog.Choice{{Value: "low"}, {Value: "high"}}},
			{ID: "fast", Type: "boolean"}},
		Efforts: map[string][]string{"haiku": {"low"}},
	}}
	a.newChat.machine, a.newChat.agent, a.newChat.project = "m1", "a1", "m1:local:p1"
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	a.models[""], a.modes[""] = "opus", "plan"
	a.setRunValue("", "reasoning_effort", "high")
	a.setRunValue("", "fast", true)
	tt.Frame()
	// Another project starts from what it had, here nothing.
	a.newChat.project = "m1:local:p2"
	tt.Frame()
	a.models[""], a.modes[""] = "haiku", "default"
	delete(a.runValues, "")
	tt.Frame()
	a.newChat.project = "m1:local:p1"
	tt.Frame()
	if a.models[""] != "opus" || a.modes[""] != "plan" || a.runValues[""]["reasoning_effort"] != "high" || a.runValues[""]["fast"] != true {
		t.Fatalf("p1 restored model %q mode %q values %v", a.models[""], a.modes[""], a.runValues[""])
	}
	// An effort the model doesn't allow is not given back.
	a.applyModelChoice(modelChoice{Model: "haiku", Values: map[string]any{"reasoning_effort": "high", "fast": "yes"}})
	if a.models[""] != "haiku" || a.runValues[""]["reasoning_effort"] != nil || a.runValues[""]["fast"] != nil {
		t.Fatalf("validated: %q %v", a.models[""], a.runValues[""])
	}
}

func TestMessageShare(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.view.Entries = a.view.Entries[:2]
	e := a.view.Entries[1]
	bs := shareBlocks(replyText(e))
	if len(bs) != 3 || bs[0].kind != "paragraph" || bs[1].kind != "code" || bs[2].kind != "list" {
		t.Fatalf("blocks %+v", bs)
	}
	a.openMessageShare(e)
	if a.msgShare.image == nil || a.msgShare.tooLarge {
		t.Fatalf("no image: %+v", a.msgShare.err)
	}
	// 1080 wide, as tall as the reply on paper.
	b := a.msgShare.image.Bounds()
	if b.Dx() != 1080 || b.Dy() < 400 || b.Dy() > 2000 {
		t.Fatalf("image %v", b)
	}
	if px := a.msgShare.image.RGBAAt(8, b.Dy()-2); px.R != 250 || px.G != 248 || px.B != 243 {
		t.Fatalf("paper %v", px)
	}
	if dir := os.Getenv("LODY_SNAPSHOTS"); dir != "" {
		f, _ := os.Create(filepath.Join(dir, "share-card.png"))
		png.Encode(f, a.msgShare.image)
		f.Close()
	}
	// Picking none says so.
	for i := range a.msgShare.picked {
		a.msgShare.picked[i] = false
	}
	a.renderMessageShare()
	if a.msgShare.image != nil {
		t.Fatal("image of nothing")
	}
	if data, err := shareJPEG(image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil || len(data) == 0 {
		t.Fatal(err)
	}
}

func TestConversationTabs(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.view.Entries = a.view.Entries[:2]
	a.catalog.Sessions = append(a.catalog.Sessions, catalog.Session{ID: "s7", MachineID: "m1", Title: "Review the branch", Status: "idle", ProjectID: "m1:local:p1", ParentSessionID: "s1", CreatedAt: "2026-10-29T10:00:00.000Z"})
	tt := snap(t, a, "conversation-tabs", false, nil)
	if !tt.HasText("Review the branch") {
		t.Fatalf("child tab missing: %q", tt.Texts())
	}
	if got := strings.Join(a.orderedTabs(a.tabRoot(a.catalog.Session("s1"))), ","); got != "s1,s7" {
		t.Fatalf("tabs %s", got)
	}
	// ⌘2 shows the child; its tabs are its parent's.
	tt.Key(ui.Cmd, ui.Key2)
	tt.Frame()
	if a.selected != "s7" || a.tabRoot(a.catalog.Session("s7")).ID != "s1" {
		t.Fatalf("⌘2 selected %q", a.selected)
	}
	// New Tab is a draft, with what to ask of the branch.
	a.newTab()
	tt.Frame()
	if a.activeDraft == "" || !tt.HasText("Review the changes on this branch") || !tt.HasText("New Tab") {
		t.Fatalf("draft: %q", tt.Texts())
	}
	if err := tt.Click("Summarize the changes on this branch"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.draft != "Summarize the changes on this branch" {
		t.Fatalf("suggestion %q", a.draft)
	}
	// Closing the draft shows the tab before it; closing a child keeps it out.
	draft := a.activeDraft
	a.closeTab(a.catalog.Session("s1"), draft)
	tt.Frame()
	if a.activeDraft != "" || a.selected != "s7" {
		t.Fatalf("after close: draft %q selected %q", a.activeDraft, a.selected)
	}
	a.closeTab(a.catalog.Session("s1"), "s7")
	tt.Frame()
	if a.selected != "s1" || len(a.orderedTabs(a.catalog.Session("s1"))) != 1 {
		t.Fatalf("closing the child: selected %q", a.selected)
	}
}
