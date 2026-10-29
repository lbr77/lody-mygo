package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// streamable is a server reached over Streamable HTTP: each message a
// POST, answered with JSON or with an event stream that carries it.
type streamable struct {
	url     string
	headers http.Header
	client  *http.Client

	mu        sync.Mutex
	nextID    int
	sessionID string
	version   string
}

func dialHTTP(s Server) (*streamable, error) {
	u, err := url.Parse(s.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("mcp: the URL must be HTTP or HTTPS")
	}
	if u.User != nil {
		return nil, errors.New("mcp: the URL must not carry credentials")
	}
	h := http.Header{}
	for _, k := range sortedKeys(s.Headers) {
		h.Set(k, s.Headers[k])
	}
	if s.BearerToken != "" {
		h.Set("Authorization", "Bearer "+s.BearerToken)
	}
	return &streamable{url: s.URL, headers: h, client: &http.Client{
		// Lody refuses redirects, which could carry the headers elsewhere.
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("mcp: the server redirected") },
	}}, nil
}

func (t *streamable) post(ctx context.Context, msg map[string]any) (*http.Response, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = t.headers.Clone()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	t.mu.Lock()
	if t.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	if t.version != "" {
		req.Header.Set("MCP-Protocol-Version", t.version)
	}
	t.mu.Unlock()
	res, err := t.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the URL may carry a token
		}
		return nil, fmt.Errorf("mcp: %w", err)
	}
	if res.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(res.Body, 64*1024))
		res.Body.Close()
		return nil, &StatusError{res.StatusCode}
	}
	if id := res.Header.Get("Mcp-Session-Id"); id != "" {
		t.mu.Lock()
		t.sessionID = id
		t.mu.Unlock()
	}
	return res, nil
}

// StatusError is a server's refusal over HTTP.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return "mcp: the server answered " + strconv.Itoa(e.Code) }

func (t *streamable) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	t.mu.Lock()
	t.nextID++
	id := t.nextID
	t.mu.Unlock()
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	res, err := t.post(ctx, msg)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	want := strconv.Itoa(id)
	media, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	var result json.RawMessage
	if media == "text/event-stream" {
		result, err = readEvents(res.Body, want)
	} else {
		result, err = readJSON(io.LimitReader(res.Body, 32*1024*1024), want)
	}
	if err != nil {
		return nil, err
	}
	if method == "initialize" {
		var init struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(result, &init)
		t.mu.Lock()
		t.version = init.ProtocolVersion
		t.mu.Unlock()
	}
	return result, nil
}

// answer is the result of the message for id among msgs, if one is.
func answer(msgs []message, id string) (json.RawMessage, error, bool) {
	for _, m := range msgs {
		if m.Method != "" || string(m.ID) != id {
			continue
		}
		if m.Error != nil {
			return nil, m.Error, true
		}
		return m.Result, nil, true
	}
	return nil, nil, false
}

func decodeMessages(b []byte) []message {
	b = bytes.TrimSpace(b)
	var msgs []message
	if len(b) > 0 && b[0] == '[' {
		json.Unmarshal(b, &msgs)
		return msgs
	}
	var m message
	if json.Unmarshal(b, &m) == nil {
		msgs = append(msgs, m)
	}
	return msgs
}

func readJSON(r io.Reader, id string) (json.RawMessage, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("mcp: %w", err)
	}
	if res, err, ok := answer(decodeMessages(b), id); ok {
		return res, err
	}
	return nil, errors.New("mcp: the server didn't answer")
}

// readEvents reads an event stream until the answer to id.
func readEvents(r io.Reader, id string) (json.RawMessage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 32*1024*1024)
	var data strings.Builder
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if data.Len() > 0 {
				if res, err, ok := answer(decodeMessages([]byte(data.String())), id); ok {
					return res, err
				}
				data.Reset()
			}
			continue
		}
		if v, ok := strings.CutPrefix(line, "data:"); ok {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(v, " "))
		}
	}
	if data.Len() > 0 {
		if res, err, ok := answer(decodeMessages([]byte(data.String())), id); ok {
			return res, err
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("mcp: %w", err)
	}
	return nil, errors.New("mcp: the server didn't answer")
}

func (t *streamable) notify(ctx context.Context, method string, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	res, err := t.post(ctx, msg)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, io.LimitReader(res.Body, 64*1024))
	return res.Body.Close()
}

// close ends the session, if the server kept one.
func (t *streamable) close() {
	t.mu.Lock()
	id := t.sessionID
	t.mu.Unlock()
	if id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.url, nil)
	if err != nil {
		return
	}
	req.Header = t.headers.Clone()
	req.Header.Set("Mcp-Session-Id", id)
	if res, err := t.client.Do(req); err == nil {
		res.Body.Close()
	}
}
