// Package localterm talks to the terminals of the Lody daemon on this
// machine, as Lody's desktop app does: the daemon keeps a session's
// shells, in its working directory, and serves them on a Unix socket in
// ~/.lody/run, one JSON message a line. Only sessions that run on this
// machine have terminals; Lody Cloud carries none.
package localterm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// DataDir is the daemon's data folder: $LODY_DATA_DIR, or ~/.lody.
func DataDir() string {
	if d := strings.TrimSpace(os.Getenv("LODY_DATA_DIR")); d != "" {
		return filepath.Clean(d)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".lody")
}

// MachineID is the id of this machine as its daemon knows it, or "" when
// no daemon has run here.
func MachineID() string {
	dir := DataDir()
	if dir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dir, "machine-id"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// SocketPath is where the daemon serves terminals.
func SocketPath() string { return filepath.Join(DataDir(), "run", "lody-terminal.sock") }

// MaxPerSession is how many terminals the daemon keeps for a session.
const MaxPerSession = 8

// Info is a terminal of a session.
type Info struct {
	ID    string `json:"terminalId"`
	Title string `json:"title"`
	Cwd   string `json:"cwd,omitempty"`
}

// Error is an error the daemon reports, as session_machine_mismatch.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

type event struct {
	Type       string `json:"type"`
	RequestID  string `json:"requestId,omitempty"`
	SessionID  string `json:"sessionId,omitempty"`
	Terminals  []Info `json:"terminals,omitempty"`
	TerminalID string `json:"terminalId,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	Data       string `json:"data,omitempty"`
	Replay     bool   `json:"replay,omitempty"`
	Title      string `json:"title,omitempty"`
	ExitCode   *int   `json:"exitCode,omitempty"`
	Signal     any    `json:"signal,omitempty"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
}

// Client is a connection to the daemon.
type Client struct {
	conn net.Conn
	wmu  sync.Mutex

	mu      sync.Mutex
	waiting map[string]chan event
	conns   map[string]*Conn
	err     error
	done    chan struct{}
}

// Dial connects to the daemon, after checking that its folder is this
// user's alone, as the daemon makes it.
func Dial(ctx context.Context) (*Client, error) {
	path := SocketPath()
	if err := checkRunDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, &Error{Code: "daemon_unavailable", Message: "The Lody daemon isn't running on this machine."}
	}
	c := &Client{conn: conn, waiting: map[string]chan event{}, conns: map[string]*Conn{}, done: make(chan struct{})}
	go c.read()
	return c, nil
}

func checkRunDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return &Error{Code: "daemon_unavailable", Message: "The Lody daemon isn't running on this machine."}
	}
	if !fi.IsDir() || fi.Mode().Perm()&0o077 != 0 {
		return &Error{Code: "unsafe_run_dir", Message: dir + " isn't private to this user."}
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return &Error{Code: "unsafe_run_dir", Message: dir + " belongs to another user."}
	}
	return nil
}

// Close disconnects; the daemon keeps the terminals.
func (c *Client) Close() error {
	c.fail(errors.New("closed"))
	return c.conn.Close()
}

// Done is closed once the connection ends.
func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) read() {
	r := bufio.NewReaderSize(c.conn, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var ev event
			if json.Unmarshal(line, &ev) == nil {
				c.dispatch(ev)
			}
		}
		if err != nil {
			c.fail(err)
			return
		}
	}
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = err
	waiting, conns := c.waiting, c.conns
	c.waiting, c.conns = map[string]chan event{}, map[string]*Conn{}
	close(c.done)
	c.mu.Unlock()
	for _, ch := range waiting {
		close(ch)
	}
	for _, t := range conns {
		t.end(nil)
	}
}

func (c *Client) dispatch(ev event) {
	c.mu.Lock()
	if ev.RequestID != "" {
		if ch, ok := c.waiting[ev.RequestID]; ok && (ev.Type == "terminals" || ev.Type == "opened" || ev.Type == "error") {
			delete(c.waiting, ev.RequestID)
			c.mu.Unlock()
			ch <- ev
			return
		}
	}
	t := c.conns[ev.TerminalID]
	if ev.Type == "exit" {
		delete(c.conns, ev.TerminalID)
	}
	c.mu.Unlock()
	if t == nil {
		return
	}
	switch ev.Type {
	case "data":
		t.push([]byte(ev.Data), ev.Replay)
	case "title":
		t.setTitle(ev.Title)
	case "exit":
		code := -1
		if ev.ExitCode != nil {
			code = *ev.ExitCode
		}
		t.end(&code)
	case "error":
		t.end(nil)
	}
}

func (c *Client) send(msg map[string]any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.conn.Write(append(b, '\n'))
	return err
}

// request sends msg and waits for its reply, as the desktop app waits:
// ten seconds.
func (c *Client) request(ctx context.Context, msg map[string]any) (event, error) {
	id := uuid.NewString()
	msg["requestId"] = id
	ch := make(chan event, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return event{}, &Error{Code: "daemon_unavailable", Message: "The connection to the Lody daemon closed."}
	}
	c.waiting[id] = ch
	c.mu.Unlock()
	if err := c.send(msg); err != nil {
		return event{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case ev, ok := <-ch:
		if !ok {
			return event{}, &Error{Code: "daemon_unavailable", Message: "The connection to the Lody daemon closed."}
		}
		if ev.Type == "error" {
			return event{}, &Error{Code: ev.Code, Message: errorText(ev.Code, ev.Message)}
		}
		return ev, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.waiting, id)
		c.mu.Unlock()
		return event{}, ctx.Err()
	}
}

func errorText(code, message string) string {
	switch code {
	case "session_machine_mismatch":
		return "Terminal is only available on the machine that owns this session."
	case "terminal_limit_exceeded":
		return fmt.Sprintf("Terminal limit reached (%d per session).", MaxPerSession)
	case "workdir_unavailable":
		return "The session's working directory isn't available."
	}
	if message != "" {
		return message
	}
	return code
}

// List returns the terminals the daemon keeps for a session.
func (c *Client) List(ctx context.Context, sessionID string) ([]Info, error) {
	ev, err := c.request(ctx, map[string]any{"type": "list", "sessionId": sessionID})
	if err != nil {
		return nil, err
	}
	return ev.Terminals, nil
}

// Open starts a terminal for a session, in its working directory.
func (c *Client) Open(ctx context.Context, sessionID string, cols, rows int) (Info, error) {
	ev, err := c.request(ctx, map[string]any{"type": "open", "sessionId": sessionID, "cols": cols, "rows": rows})
	if err != nil {
		return Info{}, err
	}
	return Info{ID: ev.TerminalID, Cwd: ev.Cwd}, nil
}

// Kill ends a terminal and its shell.
func (c *Client) Kill(terminalID string) error {
	c.mu.Lock()
	t := c.conns[terminalID]
	delete(c.conns, terminalID)
	c.mu.Unlock()
	if t != nil {
		t.end(nil)
	}
	return c.send(map[string]any{"type": "close", "terminalId": terminalID})
}

// Attach follows a terminal: its output, first what it kept, then what
// comes. The returned Conn is what a terminal view reads and writes.
func (c *Client) Attach(terminalID string, cols, rows int, onTitle func(string)) (*Conn, error) {
	t := &Conn{client: c, id: terminalID, onTitle: onTitle}
	t.cond = sync.NewCond(&t.mu)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return nil, &Error{Code: "daemon_unavailable", Message: "The connection to the Lody daemon closed."}
	}
	c.conns[terminalID] = t
	c.mu.Unlock()
	if err := c.send(map[string]any{"type": "attach", "terminalId": terminalID, "cols": cols, "rows": rows}); err != nil {
		return nil, err
	}
	return t, nil
}

// muteAfterReplay is how long after the terminal took the replayed output
// what it sends is dropped: its answers to the queries in that output,
// which the shell asked long ago.
const muteAfterReplay = 300 * time.Millisecond

// Conn is a terminal's stream, for a terminal view's Options.Conn.
type Conn struct {
	client  *Client
	id      string
	onTitle func(string)

	mu        sync.Mutex
	cond      *sync.Cond
	chunks    []chunk
	ended     bool
	exitCode  *int
	gaveReply bool // the last Read gave replayed output
	muteUntil time.Time
}

type chunk struct {
	data   []byte
	replay bool
}

func (t *Conn) push(data []byte, replay bool) {
	t.mu.Lock()
	if !t.ended {
		t.chunks = append(t.chunks, chunk{data, replay})
		t.cond.Broadcast()
	}
	t.mu.Unlock()
}

func (t *Conn) setTitle(title string) {
	if t.onTitle != nil {
		t.onTitle(title)
	}
}

func (t *Conn) end(code *int) {
	t.mu.Lock()
	if !t.ended {
		t.ended, t.exitCode = true, code
		t.cond.Broadcast()
	}
	t.mu.Unlock()
}

// ExitCode is the shell's exit status once it exited; ok is false while
// it runs, or when the terminal ended otherwise.
func (t *Conn) ExitCode() (code int, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.exitCode == nil {
		return 0, false
	}
	return *t.exitCode, true
}

// ID is the terminal's id.
func (t *Conn) ID() string { return t.id }

func (t *Conn) Read(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.gaveReply {
		// The view has written the replay into its screen by now.
		t.gaveReply, t.muteUntil = false, time.Now().Add(muteAfterReplay)
	}
	for len(t.chunks) == 0 && !t.ended {
		t.cond.Wait()
	}
	if len(t.chunks) == 0 {
		return 0, io.EOF
	}
	c := &t.chunks[0]
	n := copy(p, c.data)
	c.data = c.data[n:]
	if c.replay {
		t.gaveReply = true
	}
	if len(c.data) == 0 {
		t.chunks = t.chunks[1:]
	}
	return n, nil
}

func (t *Conn) Write(p []byte) (int, error) {
	t.mu.Lock()
	muted := t.gaveReply || time.Now().Before(t.muteUntil)
	for _, c := range t.chunks {
		muted = muted || c.replay
	}
	ended := t.ended
	t.mu.Unlock()
	if ended {
		return 0, io.ErrClosedPipe
	}
	if muted {
		return len(p), nil
	}
	if err := t.client.send(map[string]any{"type": "input", "terminalId": t.id, "data": string(p)}); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Resize tells the daemon the view's size.
func (t *Conn) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return t.client.send(map[string]any{"type": "resize", "terminalId": t.id, "cols": cols, "rows": rows})
}

// Close stops following the terminal; the daemon keeps its shell.
func (t *Conn) Close() error {
	t.client.mu.Lock()
	if t.client.conns[t.id] == t {
		delete(t.client.conns, t.id)
	}
	t.client.mu.Unlock()
	t.end(nil)
	return nil
}
