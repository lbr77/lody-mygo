package cloud

import (
	"encoding/json"
	"testing"
)

// The key id Lody's own tests pin for session-1.
func TestCollabKeyID(t *testing.T) {
	if got := collabKeyID("session-1"); got != "ccv2:efb61a27dce18cd331dd9f25" {
		t.Fatalf("key id = %s", got)
	}
}

func TestSealRoundTrip(t *testing.T) {
	env, err := Seal("root-1", map[string]any{"sessionId": "child", "path": "a.go"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(env)
	var fields map[string]any
	json.Unmarshal(b, &fields)
	if len(fields) != 7 || fields["algorithm"] != "AES-256-GCM" || fields["keyVersion"] != 1.0 {
		t.Fatalf("envelope = %s", b)
	}
	var out struct{ SessionID, Path string }
	if err := Open("root-1", env, &out); err != nil || out.Path != "a.go" || out.SessionID != "child" {
		t.Fatalf("open = %+v, %v", out, err)
	}
	if err := Open("root-2", env, &out); err == nil {
		t.Fatal("opened another tree's envelope")
	}
	env.Ciphertext = env.Ciphertext[:len(env.Ciphertext)-2] + "AA"
	if err := Open("root-1", env, &out); err == nil {
		t.Fatal("opened a tampered envelope")
	}
}

// A machine answers a long request with progress frames before its answer,
// all under the request's id.
func TestFindReplies(t *testing.T) {
	payload := []byte(`[{"id":"other","result":{"type":"x"}},
{"id":"r1","result":{"type":"machine/acp-authentication-progress","status":"starting"}},
{"id":"r1","result":{"type":"machine/acp-authenticate_response","success":true}}]`)
	replies, rpcErr := findReplies(payload, "r1")
	if rpcErr != nil || len(replies) != 2 || !isProgress(replies[0]) || isProgress(replies[1]) {
		t.Fatalf("replies = %s, %v", replies, rpcErr)
	}
	if _, rpcErr := findReplies([]byte(`{"id":"r1","error":{"code":"x","message":"no"}}`), "r1"); rpcErr == nil || rpcErr.Message != "no" {
		t.Fatalf("error = %v", rpcErr)
	}
}
