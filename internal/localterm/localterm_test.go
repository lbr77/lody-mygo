package localterm

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeDaemon serves the terminal protocol on a socket in a fresh data
// folder, and records what it is sent.
type fakeDaemon struct {
	mu  sync.Mutex
	got []map[string]any
}

func (d *fakeDaemon) record(m map[string]any) {
	d.mu.Lock()
	d.got = append(d.got, m)
	d.mu.Unlock()
}

func (d *fakeDaemon) sent(kind string) []map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []map[string]any
	for _, m := range d.got {
		if m["type"] == kind {
			out = append(out, m)
		}
	}
	return out
}

func startDaemon(t *testing.T) *fakeDaemon {
	dir, err := os.MkdirTemp("", "lt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("LODY_DATA_DIR", dir)
	if err := os.Mkdir(filepath.Join(dir, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "machine-id"), []byte("machine-1\n"), 0o600)
	ln, err := net.Listen("unix", SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	d := &fakeDaemon{}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		out := json.NewEncoder(conn)
		r := bufio.NewScanner(conn)
		for r.Scan() {
			var m map[string]any
			json.Unmarshal(r.Bytes(), &m)
			d.record(m)
			id, _ := m["requestId"].(string)
			switch m["type"] {
			case "list":
				out.Encode(map[string]any{"type": "terminals", "requestId": id, "sessionId": m["sessionId"], "terminals": []any{map[string]any{"terminalId": "t1", "title": "zsh"}}})
			case "open":
				if m["sessionId"] == "remote" {
					out.Encode(map[string]any{"type": "error", "requestId": id, "code": "session_machine_mismatch", "message": "session_machine_mismatch:remote"})
					continue
				}
				out.Encode(map[string]any{"type": "opened", "requestId": id, "terminalId": "t2", "cwd": "/src"})
			case "attach":
				out.Encode(map[string]any{"type": "title", "terminalId": m["terminalId"], "title": "vim"})
				out.Encode(map[string]any{"type": "data", "terminalId": m["terminalId"], "data": "\x1b[cearlier output\r\n", "replay": true})
			case "input":
				if m["data"] == "ls\r" {
					out.Encode(map[string]any{"type": "data", "terminalId": m["terminalId"], "data": "README.md\r\n"})
				}
			case "close":
				out.Encode(map[string]any{"type": "exit", "terminalId": m["terminalId"], "exitCode": 0})
			}
		}
	}()
	return d
}

func TestTerminal(t *testing.T) {
	d := startDaemon(t)
	if id := MachineID(); id != "machine-1" {
		t.Fatalf("machine id = %q", id)
	}
	ctx := context.Background()
	c, err := Dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	list, err := c.List(ctx, "s1")
	if err != nil || len(list) != 1 || list[0].ID != "t1" || list[0].Title != "zsh" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if _, err := c.Open(ctx, "remote", 80, 24); err == nil || err.Error() != "Terminal is only available on the machine that owns this session." {
		t.Fatalf("open on another machine: %v", err)
	}
	info, err := c.Open(ctx, "s1", 80, 24)
	if err != nil || info.ID != "t2" || info.Cwd != "/src" {
		t.Fatalf("open = %+v, %v", info, err)
	}
	titles := make(chan string, 1)
	conn, err := c.Attach(info.ID, 100, 30, func(s string) { titles <- s })
	if err != nil {
		t.Fatal(err)
	}
	if title := <-titles; title != "vim" {
		t.Fatalf("title = %q", title)
	}
	buf := make([]byte, 1024)
	n, _ := conn.Read(buf)
	if string(buf[:n]) != "\x1b[cearlier output\r\n" {
		t.Fatalf("replay = %q", buf[:n])
	}
	// The emulator answers the replayed query: that must not reach the shell.
	conn.Write([]byte("\x1b[?62c"))
	go conn.Read(buf) // the view reads on, which starts the quiet spell
	time.Sleep(muteAfterReplay + 100*time.Millisecond)
	conn.Write([]byte("ls\r"))
	conn.Resize(120, 40)
	deadline := time.Now().Add(2 * time.Second)
	for len(d.sent("resize")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	inputs := d.sent("input")
	if len(inputs) != 1 || inputs[0]["data"] != "ls\r" {
		t.Fatalf("inputs = %v", inputs)
	}
	if r := d.sent("resize"); len(r) != 1 || r[0]["cols"] != 120.0 || r[0]["rows"] != 40.0 {
		t.Fatalf("resize = %v", r)
	}
	if a := d.sent("attach"); len(a) != 1 || a[0]["cols"] != 100.0 {
		t.Fatalf("attach = %v", a)
	}
	if err := c.Kill(info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(buf); err != io.EOF {
		t.Fatalf("read after kill: %v", err)
	}
}

func TestUnsafeRunDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LODY_DATA_DIR", dir)
	os.Mkdir(filepath.Join(dir, "run"), 0o755)
	if _, err := Dial(context.Background()); err == nil {
		t.Fatal("dialed through a run folder others can read")
	}
}
