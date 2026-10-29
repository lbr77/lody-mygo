package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/cloud"
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
	if id, reason := latestRetryableFailure(a.view); id != "f1" || reason != "acp_provider_overloaded" {
		t.Fatalf("capacity failure %q %q", id, reason)
	}
}

// The latest failure the agent can go on from offers Retry, as Lody's
// recoverable reasons: one click continues the turn with Lody's
// continuation, Retrying… while it goes, and the pill leaves once it went;
// a refusal, a failure without Git, or one a later message superseded
// offers none.
func TestErrorRetry(t *testing.T) {
	failed := func(meta map[string]any) []session.Entry {
		return []session.Entry{{ID: "u1", Role: "user"}, {ID: "n1", Role: "system", Finished: true, Items: []session.Item{notice("f1", "chat_failed", meta)}}}
	}
	for reason := range recoverableReasons {
		if id, got := latestRetryableFailure(&session.View{Entries: failed(map[string]any{"reason": reason})}); id != "f1" || got != reason {
			t.Errorf("%s: %q %q", reason, id, got)
		}
	}
	for _, meta := range []map[string]any{{"reason": "acp_auth_required"}, {"reason": "acp_internal_error", "code": "git_executable_not_found"}, {"reason": "future_reason"}} {
		if id, _ := latestRetryableFailure(&session.View{Entries: failed(meta)}); id != "" {
			t.Errorf("%v offers a retry", meta)
		}
	}
	later := append(failed(map[string]any{"reason": "agent_disconnected"}), session.Entry{ID: "u2", Role: "user"})
	if id, _ := latestRetryableFailure(&session.View{Entries: later}); id != "" {
		t.Error("a failure a later message superseded offers a retry")
	}

	r := newSendRig(t, false)
	a := r.a
	a.sync = &catalog.Sync{}
	a.view.Entries = append(a.view.Entries[:2], session.Entry{ID: "n1", Role: "system", Finished: true, Items: []session.Item{
		notice("f1", "chat_failed", map[string]any{"reason": "agent_disconnected", "message": "socket closed"})}})
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	if _, ok := tt.Find("Retry"); !ok || !tt.HasText("Agent disconnected unexpectedly") || tt.HasText("Retry and auto-retry") {
		t.Fatalf("notice: %q", tt.Texts())
	}
	snapFrame(t, tt, "error-retry")
	// While the agent works, it waits.
	a.view.Working = true
	tt.Frame()
	if err := tt.Click("Retry"); err != nil {
		t.Fatal(err)
	}
	r.noWrite()
	a.view.Working = false
	tt.Frame()
	if err := tt.Click("Retry"); err != nil {
		t.Fatal(err)
	}
	turn := r.write()
	if turn.Text != errorPrompt || turn.Queue || turn.ID != "" {
		t.Fatalf("continued with %+v", turn)
	}
	tt.Frame()
	if !tt.HasText("Retrying…") {
		t.Errorf("pending: %q", tt.Texts())
	}
	r.release <- nil
	r.pump("the retry", func() bool { return !a.capState().pending })
	tt.Frame()
	if _, ok := tt.Find("Retry"); ok || !a.capState().handled["f1"] {
		t.Errorf("the retry stays once it went: %q", tt.Texts())
	}

	// Not written, control goes back, and why shows.
	a.view.Entries = append(a.view.Entries[:3], session.Entry{ID: "n2", Role: "system", Finished: true, Items: []session.Item{
		notice("f2", "chat_failed", map[string]any{"reason": "acp_upstream_api_error"})}})
	tt.Frame()
	if err := tt.Click("Retry"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	r.write()
	r.release <- errors.New("session: a message is still sending")
	r.pump("the refusal", func() bool { return !a.capState().pending })
	if a.capState().handled["f2"] || a.sendError != "session: a message is still sending" {
		t.Fatalf("after a refusal: %q", a.sendError)
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

func TestRoleProposal(t *testing.T) {
	roles := []catalog.AgentRole{
		{ID: "r1", OwnerUserID: "u1", Name: "Reviewer", Revision: 2, Row: map[string]any{"id": "r1", "name": "Reviewer", "description": "old", "ownerUserId": "u1"}},
		{ID: "r2", OwnerUserID: "u2", Visibility: "workspace", Name: "Release Bot", Row: map[string]any{}},
	}
	var create roleProposal
	noticeMeta(map[string]any{"roleId": "r9", "requesterUserId": "u1", "command": map[string]any{
		"action": "create", "requestId": "q", "name": "Triage", "machineId": "m1", "agentConfigId": "a1"}}, &create)
	row, err := applyRoleProposal(&create, roles, "u1", 5)
	if err != nil || row["name"] != "Triage" || row["visibility"] != "private" || row["agentConfigId"] != "a1" || row["description"] != "" {
		t.Fatalf("create %+v %v", row, err)
	}
	if _, err := applyRoleProposal(&create, roles, "u2", 5); err == nil {
		t.Fatal("someone else saved it")
	}
	// A name another visible Role answers to is refused.
	create.Command.Name = "Release  Bot"
	if _, err := applyRoleProposal(&create, roles, "u1", 5); err == nil {
		t.Fatal("a taken name saved")
	}
	var update roleProposal
	noticeMeta(map[string]any{"roleId": "r1", "requesterUserId": "u1", "before": map[string]any{"name": "Reviewer", "description": "old"},
		"command": map[string]any{"action": "update", "requestId": "q", "roleId": "r1", "patch": map[string]any{"description": "new"}}}, &update)
	row, err = applyRoleProposal(&update, roles, "u1", 9)
	if err != nil || row["description"] != "new" || row["name"] != "Reviewer" || row["revision"] != 3.0 {
		t.Fatalf("update %+v %v", row, err)
	}

	a := fixture()
	a.view.Working = false
	a.view.Entries = append(a.view.Entries[:2], session.Entry{ID: "a2", Role: "assistant", Finished: true,
		Items: []session.Item{notice("rp", "agent_role_proposal", map[string]any{"roleId": "r1", "requesterUserId": "u1",
			"before":  map[string]any{"name": "Reviewer", "description": "old"},
			"command": map[string]any{"action": "update", "requestId": "q", "roleId": "r1", "patch": map[string]any{"description": "new"}}})}})
	tt := snap(t, a, "role-proposal", false, nil)
	for _, want := range []string{"Update Agent Role?", "Reviewer", "Description", "Before", "old", "After", "new", "Dismiss", "Save",
		"Applies to future uses of this Role. Existing sessions keep their configuration."} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
}

// A GitHub project's chat lists its worktree's files for @, as lody-ios
// asks the machine.
func TestWorktreeFileMentions(t *testing.T) {
	var asked map[string]any
	conn := &cloud.Conn{ID: "w1", Local: func(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
		asked = params.(map[string]any)["request"].(map[string]any)
		return json.RawMessage(`{"ok":true,"type":"worktree/list-files","result":{"paths":["README.md","cmd/main.go"]}}`), nil
	}}
	items, err := fetchFiles(context.Background(), conn, "u1", mentionTarget{machine: "m1", project: "github:acme/app", session: "s1"})
	if err != nil || asked["type"] != "worktree/list-files" || asked["repoFullName"] != "acme/app" || asked["sessionId"] != "s1" {
		t.Fatalf("asked %+v, %v", asked, err)
	}
	if len(items) != 3 || items[2].Path != "cmd/main.go" {
		t.Fatalf("items %+v", items)
	}
}
