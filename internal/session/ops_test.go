package session

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type call struct {
	method string
	params map[string]any
}

// fakeRPC answers each call with the next reply.
func fakeRPC(calls *[]call, replies ...string) rpc {
	return func(ctx context.Context, machineID, method string, params any, timeout time.Duration) (json.RawMessage, error) {
		b, _ := json.Marshal(params)
		var m map[string]any
		json.Unmarshal(b, &m)
		*calls = append(*calls, call{method, m})
		if len(replies) == 0 {
			return nil, errors.New("no reply")
		}
		r := replies[0]
		replies = replies[1:]
		return json.RawMessage(r), nil
	}
}

func TestEditableUser(t *testing.T) {
	first := []Entry{{ID: "u1", Role: "user"}}
	if e := EditableUser(first, false); e == nil || e.ID != "u1" {
		t.Fatal("a first message is editable")
	}
	later := []Entry{{ID: "u1", Role: "user"}, {ID: "a1", Role: "assistant", Finished: true, AcpTurnID: "t1"}, {ID: "u2", Role: "user"}}
	if EditableUser(later, false) != nil {
		t.Fatal("editable without fork support")
	}
	if e := EditableUser(later, true); e == nil || e.ID != "u2" {
		t.Fatal("the last message is editable")
	}
	later[2].Steer = true
	if EditableUser(later, true) != nil {
		t.Fatal("a steer is not editable")
	}
	later[2].Steer, later[1].AcpTurnID = false, ""
	if EditableUser(later, true) != nil {
		t.Fatal("no fork boundary")
	}
	queued := append(first, Entry{ID: "q", Role: "user", Queued: true})
	if e := EditableUser(queued, true); e == nil || e.ID != "u1" {
		t.Fatal("a queued message isn't the last sent")
	}
}

func TestEditAndResend(t *testing.T) {
	var calls []call
	u := Entry{ID: "u1", Role: "user", Config: map[string]any{"modelId": "m", "inputBlocks": []any{
		map[string]any{"type": "text", "text": "old"}, map[string]any{"type": "image", "imageId": "i1"}}}}
	err := editAndResend(context.Background(), fakeRPC(&calls, `{"success":true}`), "s1", "m1", "u", "builtin", "codex", u, " new text ")
	if err != nil {
		t.Fatal(err)
	}
	p := calls[0].params
	cfg := p["inputConfig"].(map[string]any)
	blocks := cfg["inputBlocks"].([]any)
	if calls[0].method != "session/edit-and-resend" || p["expectedUserTurnId"] != "u1" || p["replacementUserTurnId"] == "u1" ||
		cfg["prompt"] != "new text" || cfg["modelId"] != "m" || len(blocks) != 2 || blocks[0].(map[string]any)["text"] != "new text" ||
		blocks[1].(map[string]any)["imageId"] != "i1" || cfg["agentType"] != "codex" {
		t.Fatalf("params = %v", p)
	}
	err = editAndResend(context.Background(), fakeRPC(&calls, `{"success":false,"error":{"code":"SESSION_BUSY","message":"busy"}}`), "s1", "m1", "u", "builtin", "codex", u, "x")
	var op *OpError
	if !errors.As(err, &op) || op.Code != "SESSION_BUSY" {
		t.Fatalf("err = %v", err)
	}
}

func TestFork(t *testing.T) {
	var calls []call
	res, err := fork(context.Background(), fakeRPC(&calls, `{"success":false,"partial":false,"warnings":[],"error":{"code":"INTERNAL_ERROR","message":"x"}}`,
		`{"success":false,"disposition":"confirmation-required","partial":false,"warnings":[]}`), "s1", "m1", "u", "a1", true, "", false)
	if err != nil || !res.Confirm || res.Target == "" || len(calls) != 2 {
		t.Fatalf("res = %+v, %v, %d calls", res, err, len(calls))
	}
	if ctxt := calls[0].params["targetContext"].(map[string]any); ctxt["kind"] != "new-worktree" || ctxt["acknowledgeDirtySource"] != nil {
		t.Fatalf("params = %v", calls[0].params)
	}
	calls = nil
	res2, err := fork(context.Background(), fakeRPC(&calls, `{"success":true,"disposition":"completed","partial":true,"warnings":[{"code":"W","message":"some history"}]}`),
		"s1", "m1", "u", "a1", true, res.Target, true)
	if err != nil || res2.Target != res.Target || !res2.Partial || calls[0].params["targetContext"].(map[string]any)["acknowledgeDirtySource"] != true {
		t.Fatalf("res = %+v, %v", res2, err)
	}
	calls = nil
	if _, err := fork(context.Background(), fakeRPC(&calls, `{"success":false,"partial":false,"warnings":[],"error":{"code":"SOURCE_SESSION_BUSY","message":"busy"}}`),
		"s1", "m1", "u", "a1", false, "", false); err == nil || len(calls) != 1 || calls[0].params["targetContext"] != nil {
		t.Fatalf("err = %v, calls %v", err, calls)
	}
}

func TestGoalTerminateLive(t *testing.T) {
	var calls []call
	if d, err := goal(context.Background(), fakeRPC(&calls, `{"accepted":true,"disposition":"applied"}`), "s1", "m1", "u", "pause", ""); err != nil || d != "applied" || calls[0].params["objective"] != nil {
		t.Fatalf("goal = %q, %v", d, err)
	}
	if _, err := goal(context.Background(), fakeRPC(&calls, `{"accepted":false,"disposition":"unsupported"}`), "s1", "m1", "u", "clear", ""); err == nil {
		t.Fatal("an unsupported goal command")
	}
	if err := Terminate(context.Background(), fakeRPC(&calls, `{"success":false,"error":"gone"}`), "m1", "s1"); err == nil || err.Error() != "gone" {
		t.Fatalf("terminate = %v", err)
	}
	if s, err := liveStatus(context.Background(), fakeRPC(&calls, `{"success":true,"state":"idle","observedAtMs":1}`), "s1", "m1"); err != nil || s != "idle" {
		t.Fatalf("live = %q, %v", s, err)
	}
}
