package app

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/localterm"
)

// Only ${VAR} references reach the workspace; a value is the user's and
// goes to this computer's daemon.
func TestSplitMCP(t *testing.T) {
	shared, local := splitMCP(map[string]any{"transport": "stdio", "command": "npx", "args": []any{"-y", "srv"},
		"env": map[string]any{"TOKEN": "sk-live", "HOME_DIR": "${HOME}"}, "envPassthrough": []any{"PATH"}})
	if shared["authentication"] != "local" || shared["env"].(map[string]any)["HOME_DIR"] != "${HOME}" || shared["env"].(map[string]any)["TOKEN"] != nil ||
		local["env"].(map[string]any)["TOKEN"] != "sk-live" || shared["envPassthrough"] == nil {
		t.Fatalf("stdio: shared %v, local %v", shared, local)
	}
	shared, local = splitMCP(map[string]any{"transport": "stdio", "command": "srv", "env": map[string]any{"A": "${A}"}})
	if shared["authentication"] != nil || len(local) != 0 {
		t.Fatalf("references only: shared %v, local %v", shared, local)
	}
	shared, local = splitMCP(map[string]any{"transport": "http", "url": "https://u:p@mcp.example.com/mcp?api_key=k&team=1#x",
		"bearerToken": "tok", "headers": map[string]any{"X-Key": "v", "X-Ref": "${REF}"}})
	if shared["url"] != "https://mcp.example.com/mcp?team=1" || shared["bearerToken"] != nil || shared["authentication"] != "local" ||
		local["bearerToken"] != "tok" || local["headers"].(map[string]any)["X-Key"] != "v" || shared["headers"].(map[string]any)["X-Ref"] != "${REF}" {
		t.Fatalf("http: shared %v, local %v", shared, local)
	}
	shared, _ = splitMCP(map[string]any{"transport": "http", "url": "https://mcp.example.com/mcp", "bearerToken": "${TOKEN}"})
	if shared["bearerToken"] != "${TOKEN}" || shared["authentication"] != nil {
		t.Fatalf("shared bearer: %v", shared)
	}
	if !sameDestination(map[string]any{"transport": "http", "url": "a"}, map[string]any{"transport": "http", "url": "a", "authentication": "local"}) ||
		sameDestination(map[string]any{"transport": "stdio", "command": "a", "args": []any{"x"}}, map[string]any{"transport": "stdio", "command": "a"}) {
		t.Fatal("sameDestination")
	}
}

func TestMCPAuthMessage(t *testing.T) {
	cases := []struct {
		res                    *mcpAuthResult
		available, dirty, http bool
		want                   string
	}{
		{nil, false, false, true, "Connect to the local daemon to configure authentication."},
		{nil, true, true, true, "Save the configuration before authorizing."},
		{&mcpAuthResult{Status: "required", Reason: "oauth_required"}, true, false, true, "This server needs authorization."},
		{&mcpAuthResult{Status: "error", Reason: "client_required"}, true, false, true, "This service requires an OAuth application. Enter its client ID below."},
		{&mcpAuthResult{Status: "waiting"}, true, false, true, "Waiting for authorization in your browser…"},
		{&mcpAuthResult{Status: "not_required"}, true, false, false, "Configure credentials in the environment fields, then save."},
		{&mcpAuthResult{Status: "authorized", HasCredentials: true}, true, false, false, "Authorized"},
	}
	for _, c := range cases {
		if got := mcpAuthMessage(c.res, c.available, c.dirty, c.http); got != c.want {
			t.Errorf("%+v: %q, want %q", c.res, got, c.want)
		}
	}
	if !mcpAuthURLOK("https://auth.example.com/x") || !mcpAuthURLOK("http://127.0.0.1:8080/cb") || mcpAuthURLOK("http://evil.example.com") || mcpAuthURLOK("file:///etc") {
		t.Fatal("mcpAuthURLOK")
	}
}

// The panel's calls reach the daemon's control socket with the server as
// the workspace holds it and the user asking.
func TestMCPAuthCall(t *testing.T) {
	dir, err := os.MkdirTemp("", "ma")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("LODY_DATA_DIR", dir)
	os.Mkdir(filepath.Join(dir, "run"), 0o700)
	os.WriteFile(filepath.Join(dir, "machine-id"), []byte("m1"), 0o600)
	ln, err := net.Listen("unix", localterm.ControlSocketPath())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"type": "mcp/authentication", "status": "waiting",
			"hasCredentials": false, "mode": "oauth", "attemptId": "at1", "authorizationUrl": "https://auth.example.com/authorize"}})
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	a := fixture()
	m := catalog.MCPServer{ID: "x2", Name: "Notes", Transport: "http", CreatedAt: 1, UpdatedAt: 5,
		Connection: map[string]any{"transport": "http", "url": "https://notes.example/mcp", "authentication": "local"}}
	res, err := a.mcpAuthCall(m.Meta(), "authorize", nil)
	if err != nil || res.Status != "waiting" || res.AttemptID != "at1" {
		t.Fatalf("res = %+v, %v", res, err)
	}
	params := got["params"].(map[string]any)
	server := params["server"].(map[string]any)
	if got["method"] != "mcp/authentication" || got["machineId"] != "m1" || got["workspaceId"] != "w1" || params["action"] != "authorize" ||
		params["requesterUserId"] != "u1" || server["updatedAt"] != 5.0 || server["connection"].(map[string]any)["authentication"] != "local" {
		t.Fatalf("request = %v", got)
	}
}
