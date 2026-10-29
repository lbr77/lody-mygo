package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// NotFoundError is a command that isn't on the PATH.
type NotFoundError struct{ Command string }

func (e *NotFoundError) Error() string { return "mcp: " + e.Command + " is not on the PATH" }

// lookPath finds command on path, as a shell would.
func lookPath(command, path string) (string, error) {
	if strings.Contains(command, "/") {
		return command, nil
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		p := filepath.Join(dir, command)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", &NotFoundError{command}
}

// stdio is a server run as a child, newline-delimited JSON-RPC on its
// standard input and output.
type stdio struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	mu      sync.Mutex
	nextID  int
	waiting map[string]chan reply
	done    chan struct{}
	err     error
}

type reply struct {
	result json.RawMessage
	err    error
}

func startStdio(ctx context.Context, s Server, env map[string]string) (*stdio, error) {
	path, err := lookPath(s.Command, env["PATH"])
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, s.Args...)
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	merged := map[string]string{}
	for k, v := range env {
		merged[k] = v
	}
	for k, v := range s.Env {
		merged[k] = v
	}
	for _, k := range sortedKeys(merged) {
		cmd.Env = append(cmd.Env, k+"="+merged[k])
	}
	// Its own process group, so the children a launcher like npx starts
	// go with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: %w", err)
	}
	t := &stdio{cmd: cmd, stdin: stdin, waiting: map[string]chan reply{}, done: make(chan struct{})}
	go t.read(stdout)
	return t, nil
}

// message is any JSON-RPC message.
type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

func (t *stdio) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue // a server's stray logging
		}
		var m message
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		t.handle(m)
	}
	t.mu.Lock()
	t.err = errors.New("mcp: the server exited")
	for id, ch := range t.waiting {
		ch <- reply{err: t.err}
		delete(t.waiting, id)
	}
	t.mu.Unlock()
	close(t.done)
}

func (t *stdio) handle(m message) {
	if m.Method != "" {
		if len(m.ID) > 0 { // a request of the server's: ping, or what a settings page can't answer
			if m.Method == "ping" {
				t.write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{}})
			} else {
				t.write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32601, "message": "Method not found"}})
			}
		}
		return
	}
	t.mu.Lock()
	ch, ok := t.waiting[string(m.ID)]
	delete(t.waiting, string(m.ID))
	t.mu.Unlock()
	if !ok {
		return
	}
	if m.Error != nil {
		ch <- reply{err: m.Error}
	} else {
		ch <- reply{result: m.Result}
	}
}

func (t *stdio) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err = t.stdin.Write(append(b, '\n'))
	return err
}

func (t *stdio) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	t.mu.Lock()
	if t.err != nil {
		t.mu.Unlock()
		return nil, t.err
	}
	t.nextID++
	id := t.nextID
	ch := make(chan reply, 1)
	t.waiting[strconv.Itoa(id)] = ch
	t.mu.Unlock()
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if err := t.write(msg); err != nil {
		return nil, fmt.Errorf("mcp: %w", err)
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *stdio) notify(ctx context.Context, method string, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return t.write(msg)
}

// close ends the server: its input closed, then its group terminated,
// then killed.
func (t *stdio) close() {
	t.stdin.Close()
	pgid := t.cmd.Process.Pid
	exited := make(chan struct{})
	go func() {
		t.cmd.Wait()
		close(exited)
	}()
	for _, step := range []struct {
		signal syscall.Signal
		wait   time.Duration
	}{{0, 500 * time.Millisecond}, {syscall.SIGTERM, 2 * time.Second}, {syscall.SIGKILL, 2 * time.Second}} {
		if step.signal != 0 {
			syscall.Kill(-pgid, step.signal)
		}
		select {
		case <-exited:
			syscall.Kill(-pgid, syscall.SIGKILL) // what it left behind
			return
		case <-time.After(step.wait):
		}
	}
}
