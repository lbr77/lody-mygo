package app

import (
	"strings"
	"testing"
	"time"

	"github.com/lbr77/lody-mygo/internal/session"
)

func notice(id, name string, meta map[string]any) session.Item {
	return session.Item{ID: id, Type: "system_notice", Title: name, Notice: meta}
}

// A warning or failure that is its own turn reads as the end of the turn
// before it; one after a user turn stays.
func TestFoldNotices(t *testing.T) {
	entries := []session.Entry{
		{ID: "u1", Role: "user"},
		{ID: "a1", Role: "assistant", Items: []session.Item{{ID: "x", Type: "text", Text: "hi"}}},
		{ID: "n1", Role: "system", Items: []session.Item{notice("w", "agent_warning", map[string]any{"message": "slow"})}},
		{ID: "u2", Role: "user"},
		{ID: "n2", Role: "system", Items: []session.Item{notice("f", "chat_failed", nil)}},
	}
	got := foldNotices(entries)
	if len(got) != 4 || len(got[1].Items) != 2 || got[1].Items[1].Title != "agent_warning" || got[3].ID != "n2" {
		t.Fatalf("folded %+v", got)
	}
	if len(entries[1].Items) != 1 {
		t.Fatal("the view's entry was changed")
	}
}

func TestChatFailedText(t *testing.T) {
	d := chatFailedDiagnostic("", "spawn failed [DSH_NODE_NOT_FOUND] node missing")
	if d == nil || d.installURL == "" || !strings.Contains(d.title, "Node.js") {
		t.Fatalf("diagnostic %+v", d)
	}
	if got := chatFailedDetail("Agent error", "", "Agent error"); got != "" {
		t.Fatalf("title-only payload %q", got)
	}
	if got := chatFailedDetail("T", "Do this.", "raw 500"); got != "Do this.\n\nraw 500" {
		t.Fatalf("detail %q", got)
	}
	if got := chatFailedDetail("T", "Do this.", "raw: Do this."); got != "raw: Do this." {
		t.Fatalf("remedy in message %q", got)
	}
	report := chatFailedReport("Agent error", "", "acp_unknown_error", "", "boom", "s1", "claude", "m1")
	if report != "Error details: Agent error\nReason: acp_unknown_error\nSession: s1\nAgent: claude\nMachine: m1\n\nboom" {
		t.Fatalf("report %q", report)
	}
}

func TestNoticeViews(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.view.Entries = append(a.view.Entries[:2],
		session.Entry{ID: "n1", Role: "system", Finished: true, Items: []session.Item{notice("f1", "chat_failed", map[string]any{
			"reason": "acp_provider_overloaded", "message": `API Error: 529 {"error":{"message":"Overloaded"}}`})}},
	)
	a.view.Entries[1].Items = append(a.view.Entries[1].Items,
		notice("w1", "agent_warning", map[string]any{"message": "Context window is nearly full."}),
		notice("o1", "session_fork_origin", map[string]any{"sourceSessionId": "s2", "sourceTitle": "Sidebar status marks"}),
		notice("h1", "resume_from_external_chat_history", map[string]any{"truncated": true}),
		notice("q1", "", nil),
	)
	tt := snap(t, a, "notices", false, nil)
	for _, want := range []string{"Selected model is at capacity. Please try a different model.", `API Error: 529 {"error":{"message":"Overloaded"}}`,
		"Retry and auto-retry", "Copy error", "Agent warning", "Context window is nearly full.", "This conversation was forked from",
		"Resuming conversation from chat history. Some context may be limited. (History truncated due to length)"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	if latestCapacityFailure(a.view) != "f1" {
		t.Fatalf("capacity failure %q", latestCapacityFailure(a.view))
	}
}

func TestScheduleProposalCard(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.view.ModeID, a.view.ConfigValues = "acceptEdits", map[string]any{"effort": "high", "api_token": "secret"}
	a.catalog.Sessions[0].AgentConfigID = "a1"
	a.catalog.Sessions[0].Project = map[string]any{"kind": "local", "localProjectId": "p1"}
	meta := map[string]any{
		"proposalId": "prop-1", "title": "Morning triage", "prompt": "Triage **new** issues.",
		"rule":       map[string]any{"kind": "weekdays", "hour": 9.0, "minute": 0.0},
		"proposedBy": map[string]any{"name": "Claude Code"},
	}
	a.view.Entries = append(a.view.Entries[:2], session.Entry{ID: "a2", Role: "assistant", Finished: true, Items: []session.Item{notice("p1", "schedule_proposal", meta)}})
	tt := snap(t, a, "schedule-proposal", false, nil)
	for _, want := range []string{"Schedule this task?", "Proposed by Claude Code", "Morning triage", "Every weekday at 9:00 AM", "New chat each run",
		"Claude Code · Studio", "lody-mygo", "Ignore", "Edit", "Create"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	var pr proposal
	noticeMeta(meta, &pr)
	target, problem := a.resolveProposal(&pr, a.catalog.Session("s1"), a.view)
	if problem != "" || target.agent.ModeID != "acceptEdits" || target.agent.ConfigOptionValues["effort"] != "high" ||
		target.agent.ConfigOptionValues["api_token"] != nil || target.project == nil || target.project.LocalProjectID != "p1" {
		t.Fatalf("target %+v %q", target, problem)
	}
	// A named Agent that isn't there says so.
	pr.Target = &proposalNamed{AgentConfigID: "gone"}
	if _, problem := a.resolveProposal(&pr, a.catalog.Session("s1"), a.view); problem != "The named Agent is not available." {
		t.Fatalf("problem %q", problem)
	}

	// Edit shows the rule's rows; Done keeps the edits.
	if err := tt.Click("Edit"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Repeat") || !tt.HasText("Done") {
		t.Fatalf("editing: %q", tt.Texts())
	}
	ed := a.proposals["a2/p1"]
	ed.form.title = "Morning triage, edited"
	if err := tt.Click("Done"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Morning triage, edited") || tt.HasText("Repeat") {
		t.Fatalf("after Done: %q", tt.Texts())
	}

	// A created proposal reads as a receipt.
	meta["outcome"], meta["scheduleId"] = "created", "prop-1"
	a.view.Entries[2].Items[0].Notice = meta
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Scheduled task created") || tt.HasText("Create") {
		t.Fatalf("receipt: %q", tt.Texts())
	}
}
