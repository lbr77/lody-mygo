package localterm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"time"
)

// The daemon's local control socket: HTTP on a socket only this user can
// open, as Lody's desktop app reaches the daemon on its own computer for
// what it serves only there, as listing an MCP server's tools.

// ControlSocketPath is where the daemon serves local control.
func ControlSocketPath() string { return filepath.Join(DataDir(), "run", "lody-control.sock") }

// MachineRPC asks the daemon on this computer, for workspace, as a
// machine RPC; it returns the result.
func MachineRPC(ctx context.Context, workspace, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	path := ControlSocketPath()
	if err := checkRunDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	machine := MachineID()
	if machine == "" {
		return nil, &Error{Code: "daemon_unavailable", Message: "The Lody daemon isn't running on this machine."}
	}
	body, err := json.Marshal(map[string]any{
		"machineId": machine, "workspaceId": workspace, "method": method, "params": params, "timeoutMs": timeout.Milliseconds(),
	})
	if err != nil {
		return nil, err
	}
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}}
	ctx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://lody/machine-rpc", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-lody-local-control", "1")
	res, err := client.Do(req)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return nil, &Error{Code: "daemon_unavailable", Message: "The Lody daemon isn't running on this machine."}
		}
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	var reply struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("localterm: %d from the daemon", res.StatusCode)
	}
	if !reply.OK {
		return nil, &Error{Code: reply.Error, Message: reply.Error}
	}
	return reply.Result, nil
}
