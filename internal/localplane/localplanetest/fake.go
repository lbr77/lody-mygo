// Package localplanetest is a fake of the daemon's local data plane, for
// tests: it answers joins with what OnJoin gives, records what peers send,
// and pushes updates to them.
package localplanetest

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lbr77/lody-mygo/internal/localplane"
)

// Message is a frame on the data plane.
type Message struct {
	Type            string              `json:"type"`
	ProtocolVersion int                 `json:"protocolVersion"`
	RequestID       string              `json:"requestId,omitempty"`
	WorkspaceID     string              `json:"workspaceId,omitempty"`
	PeerID          string              `json:"peerId,omitempty"`
	Room            *localplane.Room    `json:"room,omitempty"`
	HaveVersion     string              `json:"haveVersion,omitempty"`
	ServerVersion   string              `json:"serverVersion,omitempty"`
	Payload         *localplane.Payload `json:"payload,omitempty"`
}

// Daemon is a fake data plane listening on a Unix socket.
type Daemon struct {
	Path string
	// OnJoin is what a join of a room gets back, when anything.
	OnJoin func(room localplane.Room) *localplane.Payload

	mu    sync.Mutex
	got   []Message
	conns []net.Conn
	peers map[string]Message // the join of each room, by room
	seen  chan struct{}
}

// Start listens until the test ends.
func Start(t *testing.T) *Daemon {
	t.Helper()
	dir, err := os.MkdirTemp("", "lp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d := &Daemon{Path: filepath.Join(dir, "dp.sock"), peers: map[string]Message{}, seen: make(chan struct{}, 1)}
	ln, err := net.Listen("unix", d.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ln.Close()
		d.Drop()
	})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			d.mu.Lock()
			d.conns = append(d.conns, conn)
			d.mu.Unlock()
			go d.serve(conn)
		}
	}()
	return d
}

func key(r localplane.Room) string { return r.Scope + "|" + r.DocID + "|" + r.FlockDocID }

func (d *Daemon) serve(conn net.Conn) {
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64<<10), localplane.MaxFrame)
	for sc.Scan() {
		var m Message
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		d.mu.Lock()
		d.got = append(d.got, m)
		if m.Type == "join" && m.Room != nil {
			d.peers[key(*m.Room)] = m
		}
		d.mu.Unlock()
		select {
		case d.seen <- struct{}{}:
		default:
		}
		if m.Type != "join" || m.Room == nil {
			continue
		}
		var p *localplane.Payload
		if d.OnJoin != nil {
			p = d.OnJoin(*m.Room)
		}
		write(conn, Message{Type: "joined", RequestID: m.RequestID, WorkspaceID: m.WorkspaceID, PeerID: m.PeerID, Room: m.Room, ServerVersion: "v", Payload: p})
	}
}

func write(conn net.Conn, m Message) {
	m.ProtocolVersion = localplane.Protocol
	b, _ := json.Marshal(m)
	conn.Write(append(b, '\n'))
}

// Push sends an update of a room to the peer that joined it, reporting
// whether one had.
func (d *Daemon) Push(room localplane.Room, p localplane.Payload) bool {
	d.mu.Lock()
	join, ok := d.peers[key(room)]
	conns := append([]net.Conn(nil), d.conns...)
	d.mu.Unlock()
	if !ok {
		return false
	}
	for _, conn := range conns {
		write(conn, Message{Type: "update", WorkspaceID: join.WorkspaceID, PeerID: join.PeerID, Room: &room, Payload: &p})
	}
	return true
}

// Joined reports whether a peer joined a room.
func (d *Daemon) Joined(room localplane.Room) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.peers[key(room)]
	return ok
}

// Updates are the updates peers sent of a room.
func (d *Daemon) Updates(room localplane.Room) []Message {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []Message
	for _, m := range d.got {
		if m.Type == "update" && m.Room != nil && key(*m.Room) == key(room) {
			out = append(out, m)
		}
	}
	return out
}

// Drop closes every connection, as a daemon going away does.
func (d *Daemon) Drop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, conn := range d.conns {
		conn.Close()
	}
	d.conns = nil
	d.peers = map[string]Message{}
}
