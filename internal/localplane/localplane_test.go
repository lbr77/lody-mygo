package localplane

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeDaemon answers joins with a missed update, then pushes another,
// then one in two chunks, and records what the peer sends.
func fakeDaemon(t *testing.T) (string, chan message) {
	t.Helper()
	dir, err := os.MkdirTemp("", "lp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "dp.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got := make(chan message, 8)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		sc := bufio.NewScanner(conn)
		write := func(m message) {
			m.ProtocolVersion = Protocol
			b, _ := json.Marshal(m)
			conn.Write(append(b, '\n'))
		}
		for sc.Scan() {
			var m message
			json.Unmarshal(sc.Bytes(), &m)
			got <- m
			if m.Type != "join" {
				continue
			}
			missed := DocUpdate([]byte("missed"))
			write(message{Type: "joined", RequestID: m.RequestID, WorkspaceID: m.WorkspaceID, PeerID: m.PeerID, Room: m.Room, ServerVersion: "vv1", Payload: &missed})
			// Another workspace's frame, which the peer drops.
			other := DocUpdate([]byte("not yours"))
			write(message{Type: "update", WorkspaceID: "elsewhere", PeerID: m.PeerID, Room: m.Room, Payload: &other})
			pushed := DocUpdate([]byte("pushed"))
			write(message{Type: "update", WorkspaceID: m.WorkspaceID, PeerID: m.PeerID, Room: m.Room, Payload: &pushed})
			for i, part := range []string{"chun", "ked"} {
				write(message{Type: "update", WorkspaceID: m.WorkspaceID, PeerID: m.PeerID, Room: m.Room, Payload: &Payload{
					Kind: "doc-update-chunk", TransferID: "t1", ChunkIndex: i, ChunkCount: 2, DataBase64: base64.StdEncoding.EncodeToString([]byte(part))}})
			}
		}
	}()
	return path, got
}

func TestJoinPushUpdate(t *testing.T) {
	path, got := fakeDaemon(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, path, "w1")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	pushes := make(chan string, 4)
	joined, err := c.Join(ctx, DocRoom("session-s1"), "vv0", func(p Payload) {
		b, _ := p.Bytes()
		pushes <- string(b)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := joined.Payload.Bytes(); joined.ServerVersion != "vv1" || string(b) != "missed" {
		t.Fatalf("joined %+v", joined)
	}
	if m := <-got; m.Type != "join" || m.Room.DocID != "session-s1" || m.HaveVersion != "vv0" || m.WorkspaceID != "w1" {
		t.Fatalf("join sent %+v", m)
	}
	var seen []string
	for len(seen) < 2 {
		select {
		case s := <-pushes:
			seen = append(seen, s)
		case <-ctx.Done():
			t.Fatalf("pushes %v", seen)
		}
	}
	if !(seen[0] == "pushed" && seen[1] == "chunked" || seen[0] == "chunked" && seen[1] == "pushed") {
		t.Fatalf("pushes %v", seen)
	}
	if err := c.Update(DocRoom("session-s1"), "vv2", DocUpdate([]byte("mine"))); err != nil {
		t.Fatal(err)
	}
	m := <-got
	if b, _ := m.Payload.Bytes(); m.Type != "update" || string(b) != "mine" {
		t.Fatalf("update sent %+v", m)
	}
}
