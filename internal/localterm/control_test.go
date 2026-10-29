package localterm

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMachineRPC(t *testing.T) {
	dir, err := os.MkdirTemp("", "lc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("LODY_DATA_DIR", dir)
	if _, err := MachineRPC(context.Background(), "w1", "machine/status", map[string]any{}, time.Second); err == nil {
		t.Fatal("no daemon, yet an answer")
	}
	os.Mkdir(filepath.Join(dir, "run"), 0o700)
	os.WriteFile(filepath.Join(dir, "machine-id"), []byte("machine-1\n"), 0o600)
	ln, err := net.Listen("unix", ControlSocketPath())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/machine-rpc" || r.Header.Get("x-lody-local-control") != "1" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "forbidden"})
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		if got["workspaceId"] != "w1" {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "workspace_runtime_unavailable:" + got["workspaceId"].(string)})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"type": "mcp/tools", "tools": []any{map[string]any{"name": "read"}}}})
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	raw, err := MachineRPC(context.Background(), "w1", "mcp/list-tools", map[string]any{"server": map[string]any{"id": "s1"}}, 35*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"tools":[{"name":"read"}],"type":"mcp/tools"}` || got["machineId"] != "machine-1" || got["method"] != "mcp/list-tools" || got["timeoutMs"] != 35000.0 {
		t.Fatalf("result %s, request %v", raw, got)
	}
	var daemonErr *Error
	if _, err := MachineRPC(context.Background(), "w2", "mcp/list-tools", nil, time.Second); !errors.As(err, &daemonErr) || daemonErr.Code != "workspace_runtime_unavailable:w2" {
		t.Fatalf("err = %v", err)
	}
}
