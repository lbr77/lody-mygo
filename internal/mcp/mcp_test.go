package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The test binary is the stdio server too, when asked to be.
func TestMain(m *testing.M) {
	if os.Getenv("MCP_FAKE_SERVER") == "1" {
		fakeServer(os.Stdin, os.Stdout)
		return
	}
	os.Exit(m.Run())
}

// fakeServer answers initialize and lists three tools over two pages,
// logging a line and pinging the client on the way.
func fakeServer(in io.Reader, out io.Writer) {
	fmt.Fprintln(out, "starting up…")
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		var m struct {
			ID     *int           `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.ID == nil {
			continue
		}
		var result any
		switch m.Method {
		case "initialize":
			fmt.Fprintln(out, `{"jsonrpc":"2.0","id":"s1","method":"ping"}`)
			result = map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{"tools": map[string]any{}},
				"serverInfo": map[string]any{"name": "fake", "version": "1"}}
		case "tools/list":
			if m.Params["cursor"] == "p2" {
				result = map[string]any{"tools": []any{map[string]any{"name": "write", "description": "Writes"}}}
			} else {
				result = map[string]any{"tools": []any{map[string]any{"name": "read", "description": "Reads"}, map[string]any{"name": "list"}}, "nextCursor": "p2"}
			}
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": *m.ID, "result": result})
		fmt.Fprintln(out, string(b))
	}
}

func TestResolve(t *testing.T) {
	s, err := FromConnection(map[string]any{"transport": "stdio", "command": "npx", "args": []any{"-y", "${PKG}"},
		"env": map[string]any{"TOKEN": "${TOKEN}"}, "envPassthrough": []any{"HOME"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Resolve(map[string]string{"PKG": "server", "TOKEN": "t", "HOME": "/h"})
	if err != nil || got.Args[1] != "server" || got.Env["TOKEN"] != "t" || got.Env["HOME"] != "/h" {
		t.Fatalf("resolved = %+v, %v", got, err)
	}
	var missing *MissingError
	if _, err := s.Resolve(map[string]string{"PKG": "server", "HOME": "/h"}); !errors.As(err, &missing) || missing.Variable != "TOKEN" {
		t.Fatalf("err = %v", err)
	}
	if _, err := FromConnection(map[string]any{"transport": "http"}); err == nil {
		t.Fatal("an empty URL")
	}
}

func TestStdio(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s := Server{Transport: "stdio", Command: exe, Env: map[string]string{"MCP_FAKE_SERVER": "1"}}
	tools, err := ListTools(context.Background(), s, map[string]string{"PATH": "/usr/bin:/bin"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 3 || tools[0].Name != "read" || tools[0].Description != "Reads" || tools[2].Name != "write" {
		t.Fatalf("tools = %+v", tools)
	}
	var notFound *NotFoundError
	if _, err := ListTools(context.Background(), Server{Transport: "stdio", Command: "no-such-mcp-server"}, map[string]string{"PATH": t.TempDir()}); !errors.As(err, &notFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamableHTTP(t *testing.T) {
	var sessions, deleted int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("X-Team") != "lody" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodDelete {
			deleted++
			return
		}
		var m struct {
			ID     *int           `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&m)
		if m.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != "sess-1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch m.Method {
		case "initialize":
			sessions++
			w.Header().Set("Mcp-Session-Id", "sess-1")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-03-26","capabilities":{}}}`, *m.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if r.Header.Get("MCP-Protocol-Version") != "2025-03-26" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{}}\n\n")
			fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%d,\ndata: \"result\":{\"tools\":[{\"name\":\"search\"}]}}\n\n", *m.ID)
		}
	}))
	defer srv.Close()
	s := Server{Transport: "http", URL: srv.URL + "/mcp", BearerToken: "${TOKEN}", Headers: map[string]string{"X-Team": "lody"}}
	tools, err := ListTools(context.Background(), s, map[string]string{"TOKEN": "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "search" || sessions != 1 || deleted != 1 {
		t.Fatalf("tools = %+v, sessions %d, deleted %d", tools, sessions, deleted)
	}
	s.BearerToken = "wrong"
	var status *StatusError
	if _, err := ListTools(context.Background(), s, nil); !errors.As(err, &status) || status.Code != 401 {
		t.Fatalf("err = %v", err)
	}
	if _, err := dialHTTP(Server{URL: "ftp://x"}); err == nil || !strings.Contains(err.Error(), "HTTP") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnviron(t *testing.T) {
	if env := Environ(); env["PATH"] == "" {
		t.Fatal("no PATH")
	}
}
