package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	loro "github.com/aholstenson/loro-go"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/streams"
)

// gateway is a Streams gateway with a machine behind it that applies every
// steer and cancel.
type gateway struct {
	mu       sync.Mutex
	appends  int
	requests []map[string]any
	replies  map[string][]byte
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, _ := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), "/ds/lody/"))
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case r.Method == http.MethodPut:
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodPost && strings.Contains(name, ":rpc:req:"):
		var req map[string]any
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &req)
		g.requests = append(g.requests, req)
		result := map[string]any{"applied": true, "success": true, "accepted": true}
		reply, _ := json.Marshal([]any{map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": result}})
		g.replies[req["replyTo"].(string)] = reply
		w.Header().Set("Stream-Next-Offset", "1")
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost:
		g.appends++
		w.Header().Set("Stream-Next-Offset", "1")
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && strings.Contains(name, ":rpc:res:"):
		w.Header().Set("Stream-Next-Offset", "1")
		w.Header().Set("Stream-Up-To-Date", "true")
		if reply, ok := g.replies[name]; ok && r.URL.Query().Get("offset") == "-1" {
			w.Write(reply)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (g *gateway) methods() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, r := range g.requests {
		out = append(out, r["method"].(string))
	}
	return out
}

// running is a replica whose reply a1 to u1 is still being written.
func running(t *testing.T) (*Replica, *gateway) {
	t.Helper()
	g := &gateway{replies: map[string][]byte{}}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	conn := &cloud.Conn{ID: "ws", HTTP: srv.Client(), API: &cloud.API{}}
	conn.SetGrant(&cloud.Grant{Token: "tok", GatewayBaseURL: srv.URL, ExpiresIn: 600})
	r := &Replica{ID: "sid", Conn: conn, doc: loro.NewLoroDoc(), ready: true, status: "live"}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	t.Cleanup(r.cancel)
	r.client = &streams.Client{URL: streams.StreamURL(srv.URL, "ws:s:sid"), HTTP: srv.Client()}
	cfg := inputConfig(nil, Turn{CLIType: "builtin", AgentType: "claude"}, "start")
	if err := r.appendUserTurn("u1", "user-1", "2026-10-29T00:00:00.000Z", cfg, "processing"); err != nil {
		t.Fatal(err)
	}
	history := r.doc.GetList(loro.AsContainerId("history"))
	reply, _ := history.InsertLoroMap(history.Len())
	reply.InsertAny("id", "a1")
	reply.InsertAny("role", "assistant")
	reply.InsertAny("userTurnId", "u1")
	reply.InsertAny("finished", false)
	r.doc.Commit()
	return r, g
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func userEntry(v *View, id string) *Entry {
	for i := range v.Entries {
		if v.Entries[i].ID == id {
			return &v.Entries[i]
		}
	}
	return nil
}

func TestGuideSteersTheRunningTurn(t *testing.T) {
	r, g := running(t)
	turn := Turn{Text: "use the other API", UserID: "user-1", MachineID: "m1", AgentType: "claude", CLIType: "builtin", Guide: true,
		MarkDispatch: func(context.Context, string, string, bool) error {
			t.Fatal("a steer must not mark a dispatch")
			return nil
		}}
	state, err := r.Send(context.Background(), turn)
	if err != nil || state != Steering {
		t.Fatalf("Send = %q, %v", state, err)
	}
	var steered *Entry
	waitFor(t, "the steer receipt", func() bool {
		v := r.View()
		for i := range v.Entries {
			if e := &v.Entries[i]; e.Role == "user" && e.Text() == "use the other API" && e.SteerState == "accepted" {
				steered = e
				return true
			}
		}
		return false
	})
	if steered.Status != "processing" || !steered.Steer {
		t.Fatalf("steered entry = %+v", steered)
	}
	if m := g.methods(); len(m) != 1 || m[0] != "session/steer" {
		t.Fatalf("machine calls = %v", m)
	}
	g.mu.Lock()
	params := g.requests[0]["params"].(map[string]any)
	g.mu.Unlock()
	if params["expectedTurnId"] != "a1" {
		t.Fatalf("steer params = %v", params)
	}
}

func TestSteerQueuedMessage(t *testing.T) {
	r, g := running(t)
	turn := Turn{Text: "then add tests", UserID: "user-1", MachineID: "m1", AgentType: "claude", CLIType: "builtin",
		MarkDispatch: func(context.Context, string, string, bool) error { return nil }}
	state, err := r.Send(context.Background(), turn)
	if err != nil || state != Queued {
		t.Fatalf("Send = %q, %v", state, err)
	}
	v := r.View()
	queued := v.Entries[len(v.Entries)-1]
	if !queued.Queued {
		t.Fatalf("last entry = %+v", queued)
	}
	if err := r.SteerQueued(context.Background(), "m1", queued.ID, false); err != nil {
		t.Fatal(err)
	}
	v = r.View()
	e := userEntry(v, queued.ID)
	if e == nil || e.Queued || e.Status != "processing" || e.SteerState != "accepted" || e.Text() != "then add tests" {
		t.Fatalf("steered entry = %+v", e)
	}
	if n := r.doc.GetMovableList(loro.AsContainerId("mq")).Len(); n != 0 {
		t.Fatalf("queue still holds %d", n)
	}
	if m := g.methods(); len(m) != 1 || m[0] != "session/steer" {
		t.Fatalf("machine calls = %v", m)
	}
}

func TestInterruptWithQueuedMessage(t *testing.T) {
	r, g := running(t)
	turn := Turn{Text: "stop and do this", UserID: "user-1", MachineID: "m1", AgentType: "claude", CLIType: "builtin",
		MarkDispatch: func(context.Context, string, string, bool) error { return nil }}
	if _, err := r.Send(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	v := r.View()
	queued := v.Entries[len(v.Entries)-1]
	if err := r.SteerQueued(context.Background(), "m1", queued.ID, true); err != nil {
		t.Fatal(err)
	}
	e := userEntry(r.View(), queued.ID)
	if e == nil || !e.Queued || e.SteerMode != "interrupt" || e.Status != "pending_apply" {
		t.Fatalf("interrupting entry = %+v", e)
	}
	if m := g.methods(); len(m) != 1 || m[0] != "session/cancel" {
		t.Fatalf("machine calls = %v", m)
	}
}

func TestAttachmentBlocks(t *testing.T) {
	image := map[string]any{"type": "image", "imageId": "img1", "mimeType": "image/png", "sizeBytes": float64(10)}
	cfg := inputConfig(nil, Turn{CLIType: "builtin", AgentType: "claude", ModeID: "plan", Attachments: []map[string]any{image}}, "")
	blocks := cfg["inputBlocks"].([]any)
	if len(blocks) != 1 || blocks[0].(map[string]any)["imageId"] != "img1" || cfg["modeId"] != "plan" {
		t.Fatalf("config = %v", cfg)
	}
	r := &Replica{doc: loro.NewLoroDoc()}
	if err := r.appendUserTurn("u1", "user-1", "2026-10-29T00:00:00.000Z", cfg, "pending"); err != nil {
		t.Fatal(err)
	}
	v := project(r.doc, "live", "")
	it := v.Entries[0].Items[0]
	if it.Type != "image" || it.ImageID != "img1" || v.ModeID != "plan" {
		t.Fatalf("item = %+v, mode %q", it, v.ModeID)
	}
}

func TestTurnMCPServers(t *testing.T) {
	previous := []any{map[string]any{"role": "user", "inputConfig": map[string]any{"mcpServerIds": []any{"old"}}}}
	if cfg := inputConfig(previous, Turn{CLIType: "builtin", AgentType: "claude"}, "x"); fmt.Sprint(cfg["mcpServerIds"]) != "[old]" {
		t.Fatalf("kept: %v", cfg["mcpServerIds"])
	}
	cfg := inputConfig(previous, Turn{CLIType: "builtin", AgentType: "claude", MCPServerIDs: []string{"a", "b"}}, "x")
	if fmt.Sprint(cfg["mcpServerIds"]) != "[a b]" {
		t.Fatalf("chosen: %v", cfg["mcpServerIds"])
	}
	cfg = inputConfig(previous, Turn{CLIType: "builtin", AgentType: "claude", MCPServerIDs: []string{}}, "x")
	if ids, _ := cfg["mcpServerIds"].([]any); ids == nil || len(ids) != 0 {
		t.Fatalf("none chosen: %#v", cfg["mcpServerIds"])
	}
}

func TestTurnConfigOptionValues(t *testing.T) {
	previous := []any{map[string]any{"role": "user", "inputConfig": map[string]any{"configOptionValues": map[string]any{"reasoning_effort": "high", "permission": "ask"}}}}
	cfg := inputConfig(previous, Turn{CLIType: "builtin", AgentType: "codex"}, "x")
	if fmt.Sprint(cfg["configOptionValues"]) != "map[permission:ask reasoning_effort:high]" {
		t.Fatalf("kept: %v", cfg["configOptionValues"])
	}
	// A choice lies over what the last turn used, as on iOS.
	cfg = inputConfig(previous, Turn{CLIType: "builtin", AgentType: "codex", ConfigOptionValues: map[string]any{"reasoning_effort": "low", "fast-mode": true}}, "x")
	if fmt.Sprint(cfg["configOptionValues"]) != "map[fast-mode:true permission:ask reasoning_effort:low]" {
		t.Fatalf("chosen: %v", cfg["configOptionValues"])
	}
}
