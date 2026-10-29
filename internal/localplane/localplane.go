// Package localplane is a client of the Lody daemon's local Loro data
// plane: the socket over which a desktop client keeps a workspace's
// documents in step with this machine's daemon without the cloud, as
// Lody's renderer does through its relay. One JSON message a line,
// protocol 7: a peer joins a room — a session's document, or a Flock —
// with the version it has, gets back what it misses, and from then on
// pushes its updates and receives the daemon's.
package localplane

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Protocol is the data plane's protocol version, which every message
// carries.
const Protocol = 7

// MaxFrame bounds one line.
const MaxFrame = 32 << 20

// SocketPath is where the daemon listens: ~/.lody/run/lody-loro-data-plane.sock.
func SocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".lody", "run", "lody-loro-data-plane.sock")
}

// Room is what a peer joins: a document by id, or a Flock by id.
type Room struct {
	Scope      string `json:"scope"` // meta, doc or flock-doc
	DocID      string `json:"docId,omitempty"`
	FlockDocID string `json:"flockDocId,omitempty"`
}

func (r Room) key() string { return r.Scope + "|" + r.DocID + "|" + r.FlockDocID }

// DocRoom is a Loro document's room, as session-<id>.
func DocRoom(id string) Room { return Room{Scope: "doc", DocID: id} }

// FlockRoom is a Flock's room.
func FlockRoom(id string) Room { return Room{Scope: "flock-doc", FlockDocID: id} }

// Payload is a sync payload: a Loro update, one chunk of a large one, or
// a Flock bundle.
type Payload struct {
	Kind       string          `json:"kind"` // doc-update, doc-update-chunk or flock-json
	DataBase64 string          `json:"dataBase64,omitempty"`
	TransferID string          `json:"transferId,omitempty"`
	ChunkIndex int             `json:"chunkIndex,omitempty"`
	ChunkCount int             `json:"chunkCount,omitempty"`
	Bundle     json.RawMessage `json:"bundle,omitempty"`
}

// DocUpdate is a Loro update as a payload.
func DocUpdate(data []byte) Payload {
	return Payload{Kind: "doc-update", DataBase64: base64.StdEncoding.EncodeToString(data)}
}

// Bytes is a doc update's data.
func (p Payload) Bytes() ([]byte, error) { return base64.StdEncoding.DecodeString(p.DataBase64) }

type message struct {
	Type            string   `json:"type"`
	ProtocolVersion int      `json:"protocolVersion"`
	RequestID       string   `json:"requestId,omitempty"`
	WorkspaceID     string   `json:"workspaceId,omitempty"`
	PeerID          string   `json:"peerId,omitempty"`
	Room            *Room    `json:"room,omitempty"`
	HaveVersion     string   `json:"haveVersion,omitempty"`
	ServerVersion   string   `json:"serverVersion,omitempty"`
	Payload         *Payload `json:"payload,omitempty"`
	Status          string   `json:"status,omitempty"`
	Code            string   `json:"code,omitempty"`
	Message         string   `json:"message,omitempty"`
	Terminal        bool     `json:"terminal,omitempty"`
}

// Error is an error the daemon sent.
type Error struct {
	Code, Message string
	Terminal      bool
}

func (e *Error) Error() string { return fmt.Sprintf("localplane: %s: %s", e.Code, e.Message) }

// Joined is what a join returns: the daemon's version and what the peer
// missed, if anything.
type Joined struct {
	ServerVersion string
	Payload       *Payload
}

// Conn is a connection to the data plane, for one workspace and peer.
type Conn struct {
	Workspace, Peer string

	nc      net.Conn
	writeMu sync.Mutex
	mu      sync.Mutex
	waiting map[string]chan message  // joins, by request
	rooms   map[string]func(Payload) // pushes, by room
	status  map[string]func(string)  // room status, by room
	chunks  map[string][][]byte      // chunked updates being assembled, by transfer
	closed  chan struct{}
	err     error
}

// Dial connects to the daemon's data plane for a workspace, as a new
// peer.
func Dial(ctx context.Context, path, workspace string) (*Conn, error) {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	c := &Conn{Workspace: workspace, Peer: uuid.NewString(), nc: nc, waiting: map[string]chan message{},
		rooms: map[string]func(Payload){}, status: map[string]func(string){}, chunks: map[string][][]byte{}, closed: make(chan struct{})}
	go c.read()
	go c.keepAlive()
	return c, nil
}

// Close leaves every room and closes the connection.
func (c *Conn) Close() error {
	_ = c.send(message{Type: "detach", WorkspaceID: c.Workspace, PeerID: c.Peer})
	return c.nc.Close()
}

// Done is closed when the connection ends; Err says why.
func (c *Conn) Done() <-chan struct{} { return c.closed }

func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Conn) send(m message) error {
	m.ProtocolVersion = Protocol
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(b) >= MaxFrame {
		return &Error{Code: "payload_too_large", Message: "frame over the data plane's limit"}
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.nc.Write(append(b, '\n'))
	return err
}

// keepAlive pings every 15 seconds, as Lody's relay does: the daemon drops
// a connection silent for a minute.
func (c *Conn) keepAlive() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-t.C:
			if c.send(message{Type: "ping"}) != nil {
				return
			}
		}
	}
}

func (c *Conn) read() {
	sc := bufio.NewScanner(c.nc)
	sc.Buffer(make([]byte, 64<<10), MaxFrame)
	for sc.Scan() {
		var m message
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.ProtocolVersion != Protocol {
			continue
		}
		// The pipe may carry frames for other peers and workspaces.
		if m.WorkspaceID != "" && m.WorkspaceID != c.Workspace || m.PeerID != "" && m.PeerID != c.Peer {
			continue
		}
		c.dispatch(m)
	}
	c.mu.Lock()
	c.err = sc.Err()
	if c.err == nil {
		c.err = errors.New("localplane: connection closed")
	}
	for id, ch := range c.waiting {
		close(ch)
		delete(c.waiting, id)
	}
	c.mu.Unlock()
	close(c.closed)
}

func (c *Conn) dispatch(m message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch m.Type {
	case "joined":
		if ch := c.waiting[m.RequestID]; ch != nil {
			ch <- m
			delete(c.waiting, m.RequestID)
		}
	case "error":
		if ch := c.waiting[m.RequestID]; m.RequestID != "" && ch != nil {
			ch <- m
			delete(c.waiting, m.RequestID)
		}
	case "update":
		if m.Room == nil || m.Payload == nil {
			return
		}
		key := m.Room.key()
		p := *m.Payload
		if p.Kind == "doc-update-chunk" {
			// A large update comes in pieces: hand it on once whole.
			parts := c.chunks[p.TransferID]
			if parts == nil {
				parts = make([][]byte, p.ChunkCount)
			}
			if p.ChunkIndex < len(parts) {
				parts[p.ChunkIndex], _ = base64.StdEncoding.DecodeString(p.DataBase64)
			}
			c.chunks[p.TransferID] = parts
			for _, part := range parts {
				if part == nil {
					return
				}
			}
			delete(c.chunks, p.TransferID)
			var whole []byte
			for _, part := range parts {
				whole = append(whole, part...)
			}
			p = DocUpdate(whole)
		}
		if fn := c.rooms[key]; fn != nil {
			go fn(p)
		}
	case "room-status":
		if m.Room != nil {
			if fn := c.status[m.Room.key()]; fn != nil {
				go fn(m.Status)
			}
		}
	}
}

// Join joins a room with the version the peer has — a Loro version vector
// for a document, a Flock's JSON one for a Flock — calling push with
// every update the daemon sends it from then on, and onStatus with the
// room's status.
func (c *Conn) Join(ctx context.Context, room Room, have string, push func(Payload), onStatus func(string)) (Joined, error) {
	id := uuid.NewString()
	ch := make(chan message, 1)
	c.mu.Lock()
	c.waiting[id] = ch
	c.rooms[room.key()] = push
	if onStatus != nil {
		c.status[room.key()] = onStatus
	}
	c.mu.Unlock()
	r := room
	if err := c.send(message{Type: "join", RequestID: id, WorkspaceID: c.Workspace, PeerID: c.Peer, Room: &r, HaveVersion: have}); err != nil {
		return Joined{}, err
	}
	select {
	case <-ctx.Done():
		return Joined{}, ctx.Err()
	case m, ok := <-ch:
		if !ok {
			return Joined{}, c.Err()
		}
		if m.Type == "error" {
			return Joined{}, &Error{Code: m.Code, Message: m.Message, Terminal: m.Terminal}
		}
		return Joined{ServerVersion: m.ServerVersion, Payload: m.Payload}, nil
	}
}

// Update sends the peer's update of a room.
func (c *Conn) Update(room Room, have string, p Payload) error {
	r := room
	return c.send(message{Type: "update", WorkspaceID: c.Workspace, PeerID: c.Peer, Room: &r, HaveVersion: have, Payload: &p})
}

// Leave leaves a room.
func (c *Conn) Leave(room Room) error {
	c.mu.Lock()
	delete(c.rooms, room.key())
	delete(c.status, room.key())
	c.mu.Unlock()
	r := room
	return c.send(message{Type: "leave", WorkspaceID: c.Workspace, PeerID: c.Peer, Room: &r})
}

// Available reports whether this machine's daemon serves the data plane.
func Available() bool {
	conn, err := net.DialTimeout("unix", SocketPath(), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
