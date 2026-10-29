package session

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	loro "github.com/aholstenson/loro-go"

	"github.com/lbr77/lody-mygo/internal/localplane"
	"github.com/lbr77/lody-mygo/internal/localplane/localplanetest"
)

// jsonMap reads JSON as the document's deep value reads, numbers as
// float64.
func jsonMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// Claude Code's AskUserQuestion metadata reads as Lody's parser reads it.
func TestParseClaudeQuestions(t *testing.T) {
	qs := ParseAskQuestions(jsonMap(t, `{"claudeCode": {"requestType": "askUserQuestion", "askUserQuestion": {
		"version": 1, "allowCustomAnswer": true,
		"questions": [{"question": "Which database should we use?", "header": "Database", "multiSelect": false,
			"options": [{"label": "Postgres", "description": "Use PostgreSQL"}, {"label": "SQLite", "description": "Use SQLite", "preview": "sqlite.db"}]}]}}}`))
	want := &AskQuestions{Source: "claude", Version: 1, AllowCustomAnswer: true, Questions: []AskQuestion{{
		Question: "Which database should we use?", Header: "Database",
		Options: []AskOption{{Label: "Postgres", Description: "Use PostgreSQL"}, {Label: "SQLite", Description: "Use SQLite", Preview: "sqlite.db"}},
	}}}
	if !reflect.DeepEqual(qs, want) {
		t.Fatalf("got %+v", qs)
	}
	if !qs.CustomAnswer(0) {
		t.Fatal("a Claude question takes the request's custom answer")
	}
	if ParseAskQuestions(jsonMap(t, `{"claudeCode": {"askUserQuestion": {"questions": [{"question": "Missing header", "options": []}]}}}`)) != nil {
		t.Fatal("a question without a header is malformed")
	}
}

// Codex's request_user_input takes an answer of the user's own where it
// says isOther, in either spelling, and hides a secret's.
func TestParseCodexQuestions(t *testing.T) {
	qs := ParseAskQuestions(jsonMap(t, `{"codex": {"requestUserInput": {"autoResolveAt": 1700000000000, "questions": [
		{"id": "next_step", "header": "Next step", "question": "What should happen next?", "is_other": true, "is_secret": true,
		 "options": [{"label": "Revise plan"}, {"label": "Start implementation"}]},
		{"id": "token", "header": "Token", "question": "Paste the token"}]}}}`))
	if qs == nil || qs.Source != "codex" || !qs.AllowCustomAnswer || qs.AutoResolveAt != 1700000000000 {
		t.Fatalf("got %+v", qs)
	}
	if q := qs.Questions[0]; q.ID != "next_step" || !q.IsSecret || !qs.CustomAnswer(0) || len(q.Options) != 2 || q.MultiSelect {
		t.Fatalf("first %+v", q)
	}
	if qs.CustomAnswer(1) || len(qs.Questions[1].Options) != 0 {
		t.Fatalf("second %+v", qs.Questions[1])
	}
}

// Lody's own elicitation comes first, with its notes and the time the
// agent goes on at, in seconds; one that does not read is no question,
// whatever the agent's copy beside it says.
func TestParseLodyElicitation(t *testing.T) {
	qs := ParseAskQuestions(jsonMap(t, `{"lody": {"elicitation": {"version": 1, "autoResolveAtEpochSeconds": 1700000000, "questions": [
		{"id": "q1", "question": "Pick a drink", "header": "Drink", "multiSelect": false, "allowCustomAnswer": true,
		 "options": [{"label": "Coffee"}, {"label": "Tea"}], "note": {"fieldId": "q1_note", "title": "Why?", "isSecret": true}},
		{"id": "q2", "question": "Pick sides", "header": "Sides", "multiSelect": true, "allowCustomAnswer": false, "options": [{"label": "Toast"}]},
		{"id": "q3", "question": "Anything else?", "header": "Else", "options": []}]}},
		"claudeCode": {"askUserQuestion": {"questions": [{"question": "Other", "header": "Other", "options": []}]}}}`))
	if qs == nil || qs.Source != "lody" || qs.AutoResolveAt != 1700000000000 || !qs.AllowCustomAnswer {
		t.Fatalf("got %+v", qs)
	}
	if n := qs.Questions[0].Note; n == nil || n.FieldID != "q1_note" || n.Title != "Why?" || !n.IsSecret {
		t.Fatalf("note %+v", n)
	}
	// A question that does not say takes the request's: any of them.
	if !qs.CustomAnswer(0) || qs.CustomAnswer(1) || !qs.CustomAnswer(2) || !qs.Questions[1].MultiSelect {
		t.Fatalf("questions %+v", qs.Questions)
	}
	// A note may not shadow a question's answer.
	if ParseAskQuestions(jsonMap(t, `{"lody": {"elicitation": {"version": 1, "questions": [
		{"id": "q1", "question": "A", "header": "A", "options": [], "note": {"fieldId": "q1"}}]}},
		"claudeCode": {"askUserQuestion": {"questions": [{"question": "Other", "header": "Other", "options": []}]}}}`)) != nil {
		t.Fatal("a note keyed as its question is malformed, and the agent's copy is not read")
	}
}

// Answers go under the question's id, else its question, else its
// header, whichever is unique, else its place.
func TestAskAnswerKey(t *testing.T) {
	same := []AskQuestion{{Question: "Which option?", Header: "Database"}, {Question: "Which option?", Header: "Runtime"}, {Question: "Which option?", Header: "Runtime"}}
	for i, want := range []string{"Database", "1", "2"} {
		if got := AskAnswerKey(same, i); got != want {
			t.Fatalf("%d: %q, want %q", i, got, want)
		}
	}
	ids := []AskQuestion{{ID: "next_step", Question: "Which option?"}, {ID: "constraints", Question: "Which constraints?"}}
	if AskAnswerKey(ids, 0) != "next_step" || AskAnswerKey(ids, 1) != "constraints" {
		t.Fatal("ids are the keys")
	}
}

// An answer's outcome carries the answers in the request's own shape.
func TestAskOutcome(t *testing.T) {
	answers := AskAnswers{"Which database should we use?": "Postgres", "Which constraints matter?": []string{"Docker", "Offline"}}
	got, _ := json.Marshal(AskOutcome("answer", answers, "claude"))
	want := `{"_meta":{"claudeCode":{"askUserQuestion":{"answers":{"Which constraints matter?":["Docker","Offline"],"Which database should we use?":"Postgres"}}}},"optionId":"answer","outcome":"selected"}`
	if string(got) != want {
		t.Fatalf("claude:\n%s\nwant\n%s", got, want)
	}
	got, _ = json.Marshal(AskOutcome("answer", AskAnswers{"next_step": "Start implementation", "constraints": []string{"Docker", "Offline"}}, "codex"))
	want = `{"_meta":{"codex":{"requestUserInput":{"answers":{"constraints":{"answers":["Docker","Offline"]},"next_step":{"answers":["Start implementation"]}}}}},"optionId":"answer","outcome":"selected"}`
	if string(got) != want {
		t.Fatalf("codex:\n%s\nwant\n%s", got, want)
	}
	got, _ = json.Marshal(AskOutcome("answer", AskAnswers{"q1": "Tea", "q1_note": "decaf"}, "lody"))
	want = `{"_meta":{"lody":{"elicitation":{"answers":{"q1":"Tea","q1_note":"decaf"},"version":1}}},"optionId":"answer","outcome":"selected"}`
	if string(got) != want {
		t.Fatalf("lody:\n%s\nwant\n%s", got, want)
	}
}

// Answers read back from an outcome in any shape, keyed as the request
// keys them: Claude's older ones by question, not by Lody's field id.
func TestExtractAskAnswers(t *testing.T) {
	core := &AskQuestions{Source: "lody", Questions: []AskQuestion{
		{ID: "question_0", Question: "Which database should we use?", Header: "Database", Options: []AskOption{{Label: "Postgres"}}},
		{ID: "sides", Question: "Sides?", Header: "Sides", MultiSelect: true},
	}}
	got := ExtractAskAnswers(core, jsonMap(t, `{"outcome": "selected", "optionId": "answer", "_meta": {"claudeCode": {"askUserQuestion": {"answers": {"Which database should we use?": "Postgres", "Sides?": ["Toast", "Eggs"]}}}}}`))
	if !reflect.DeepEqual(got, AskAnswers{"question_0": "Postgres", "sides": []string{"Toast", "Eggs"}}) {
		t.Fatalf("claude: %+v", got)
	}
	got = ExtractAskAnswers(core, jsonMap(t, `{"_meta": {"codex": {"requestUserInput": {"answers": {"question_0": {"answers": ["Postgres", "SQLite"]}}}}}}`))
	if !reflect.DeepEqual(got, AskAnswers{"question_0": "Postgres"}) {
		t.Fatalf("codex: %+v", got)
	}
	if ExtractAskAnswers(core, jsonMap(t, `{"outcome": "selected", "optionId": "cancel"}`)) != nil {
		t.Fatal("a cancel answers nothing")
	}
}

// A tool call's question request projects its questions, and, once
// answered, its answers.
func TestProjectQuestion(t *testing.T) {
	it := summarizeItem(jsonMap(t, `{"type": "tool_call", "toolCallId": "c1", "kind": "think", "title": "Pick a drink",
		"permissionRequest": {"requestId": "r1", "options": [{"optionId": "answer", "name": "Submit answers", "kind": "allow_once"}, {"optionId": "cancel", "name": "Cancel", "kind": "reject_once"}],
			"_meta": {"lody": {"elicitation": {"version": 1, "questions": [{"id": "drink", "question": "Pick a drink", "header": "Drink", "options": [{"label": "Tea"}]}]}}},
			"outcome": {"outcome": "selected", "optionId": "answer", "_meta": {"lody": {"elicitation": {"version": 1, "answers": {"drink": "Tea"}}}}}}}`), "x")
	p := it.Permission
	if p == nil || !p.Question || p.Pending || p.Questions == nil || p.Questions.Questions[0].ID != "drink" {
		t.Fatalf("permission %+v", p)
	}
	if !reflect.DeepEqual(p.Answers, AskAnswers{"drink": "Tea"}) {
		t.Fatalf("answers %+v", p.Answers)
	}
	plain := summarizeItem(jsonMap(t, `{"type": "tool_call", "permissionRequest": {"requestId": "r2", "_meta": {"questions": []}}}`), "y")
	if plain.Permission.Question || plain.Permission.Questions != nil {
		t.Fatal("metadata Lody does not read as questions is a permission")
	}
}

// A question's answers are written into its request's outcome, and sent
// on for the agent.
func TestRespondWithAnswers(t *testing.T) {
	daemon := loro.NewLoroDoc()
	history := daemon.GetList(loro.AsContainerId("history"))
	reply, _ := history.InsertLoroMap(0)
	reply.InsertAny("id", "a1")
	reply.InsertAny("role", "assistant")
	items, _ := reply.GetOrCreateLoroList("items")
	call, _ := items.InsertLoroMap(0)
	call.InsertAny("type", "tool_call")
	call.InsertAny("toolCallId", "c1")
	call.InsertAny("permissionRequest", map[string]any{
		"requestId": "r1",
		"options":   []any{map[string]any{"optionId": "answer", "name": "Submit answers", "kind": "allow_once"}},
		"_meta": map[string]any{"lody": map[string]any{"elicitation": map[string]any{"version": float64(1), "questions": []any{
			map[string]any{"id": "sides", "question": "Pick sides", "header": "Sides", "multiSelect": true, "options": []any{map[string]any{"label": "Toast"}, map[string]any{"label": "Eggs"}}},
		}}}},
	})
	daemon.Commit()
	room := localplane.DocRoom("session-sid")
	d := localplanetest.Start(t)
	d.OnJoin = func(r localplane.Room) *localplane.Payload {
		p := localplane.DocUpdate(mustExport(t, daemon))
		return &p
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lc, err := localplane.Dial(ctx, d.Path, "ws")
	if err != nil {
		t.Fatal(err)
	}
	defer lc.Close()
	r := OpenLocal(nil, lc, "sid", func(*View) {})
	defer r.Close()
	if err := r.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	item := r.View().Entries[0].Items[0]
	qs := item.Permission.Questions
	if qs == nil || !item.Permission.Pending {
		t.Fatalf("permission %+v", item.Permission)
	}
	outcome := AskOutcome("answer", AskAnswers{"sides": []string{"Toast", "Eggs"}}, qs.Source)
	if err := r.Respond(ctx, "a1", item.ID, "r1", outcome); err != nil {
		t.Fatal(err)
	}
	if got := r.View().Entries[0].Items[0].Permission; got.Pending || !reflect.DeepEqual(got.Answers, AskAnswers{"sides": []string{"Toast", "Eggs"}}) {
		t.Fatalf("after %+v", got)
	}
	if err := r.Respond(ctx, "a1", item.ID, "r1", outcome); err == nil {
		t.Fatal("a request is answered once")
	}
	data, _ := d.WaitUpdates(t, room, 1)[0].Payload.Bytes()
	daemon.Import(data)
	raw := obj(list(obj(list(deepValue(daemon.GetList(loro.AsContainerId("history")).GetDeepValue()))[0])["items"])[0])
	got, _ := json.Marshal(obj(raw["permissionRequest"])["outcome"])
	want := `{"_meta":{"lody":{"elicitation":{"answers":{"sides":["Toast","Eggs"]},"version":1}}},"optionId":"answer","outcome":"selected"}`
	if string(got) != want {
		t.Fatalf("the daemon's outcome\n%s\nwant\n%s", got, want)
	}
}
