package streams

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEncodeURIComponent(t *testing.T) {
	if got := EncodeURIComponent("ws_1:s:abc-def"); got != "ws_1%3As%3Aabc-def" {
		t.Fatal(got)
	}
	if got := StreamURL("https://gw.example/", "ws:meta"); got != "https://gw.example/ds/lody/ws%3Ameta" {
		t.Fatal(got)
	}
}

func TestFrames(t *testing.T) {
	body := append(EncodeFrame([]byte("one")), EncodeFrame([]byte("two"))...)
	frames, err := DecodeFrames(body)
	if err != nil || len(frames) != 2 || string(frames[0]) != "one" || string(frames[1]) != "two" {
		t.Fatalf("%q %v", frames, err)
	}
	frames, _ = DecodeFrames([]byte(`{"a":1}`))
	if len(frames) != 1 {
		t.Fatal("unframed JSON")
	}
	if _, err := DecodeFrames([]byte{0, 0, 0, 9, 1}); err == nil {
		t.Fatal("expected truncation error")
	}
}

func TestBootstrapAndRead(t *testing.T) {
	snapshot := []byte{0, 1, 2, '\r', '\n', 3}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.URL.EscapedPath() == "/ds/lody/ws%3Ameta/bootstrap":
			w.Header().Set("Content-Type", "multipart/mixed; boundary=b1")
			w.Header().Set("Stream-Snapshot-Offset", "5")
			w.Header().Set("Stream-Next-Offset", "9")
			w.Header().Set("Stream-Up-To-Date", "true")
			var body bytes.Buffer
			fmt.Fprintf(&body, "--b1\r\nContent-Type: application/octet-stream\r\n\r\n%s\r\n", snapshot)
			fmt.Fprintf(&body, "--b1\r\nContent-Type: application/octet-stream\r\n\r\nU1\r\n")
			fmt.Fprintf(&body, "--b1\r\nContent-Type: application/octet-stream\r\n\r\nU2\r\n--b1--")
			w.Write(body.Bytes())
		case r.URL.EscapedPath() == "/ds/lody/ws%3Ameta" && r.Method == http.MethodGet:
			if r.URL.Query().Get("offset") != "9" || r.URL.Query().Get("live") != "long-poll" || r.URL.Query().Get("cursor") != "c1" {
				w.WriteHeader(400)
				return
			}
			w.Header().Set("Stream-Next-Offset", "9")
			w.Header().Set("Stream-Up-To-Date", "true")
			w.Header().Set("Stream-Cursor", "c2")
			w.WriteHeader(204)
		case r.Method == http.MethodPost:
			w.Header().Set("Stream-Next-Offset", "10")
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := &Client{URL: StreamURL(srv.URL, "ws:meta"), Token: func(context.Context) (string, error) { return "tok", nil }}
	b, err := c.Bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b.Snapshot, snapshot) || len(b.Updates) != 2 || string(b.Updates[1]) != "U2" || b.NextOffset != "9" || !b.UpToDate {
		t.Fatalf("%+v", b)
	}
	r, err := c.Read(context.Background(), "9", "c1", true)
	if err != nil || !r.UpToDate || len(r.Payload) != 0 || r.Cursor != "c2" {
		t.Fatalf("%+v %v", r, err)
	}
	off, err := c.Append(context.Background(), "application/octet-stream", []byte("x"))
	if err != nil || off != "10" {
		t.Fatal(off, err)
	}
	missing := &Client{URL: StreamURL(srv.URL, "ws:nope"), Token: c.Token}
	if _, err := missing.Bootstrap(context.Background()); !IsNotFound(err) {
		t.Fatal(err)
	}
}
