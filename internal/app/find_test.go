package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

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
