package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lbr77/lody-mygo/internal/streams"
)

// Grant is a short-lived token for a workspace's streams.
type Grant struct {
	Token           string  `json:"token"`
	GatewayBaseURL  string  `json:"gatewayBaseUrl"`
	ExpiresIn       float64 `json:"expiresIn"`
	ShardHostSuffix string  `json:"shardHostSuffix,omitempty"`
}

// StreamsGrant asks the backend for a workspace's Streams grant.
func (a *API) StreamsGrant(ctx context.Context, token, workspaceID string) (*Grant, error) {
	var g Grant
	if err := a.call(ctx, token, "/api/loro-streams/token", map[string]string{"workspaceId": workspaceID}, &g); err != nil {
		return nil, err
	}
	u, err := url.Parse(g.GatewayBaseURL)
	if g.Token == "" || g.ExpiresIn <= 0 || err != nil || u.Scheme != "https" || u.User != nil {
		return nil, errors.New("cloud: invalid streams grant")
	}
	return &g, nil
}

// Conn is a connection to one workspace's streams. It caches the grant
// until 30 seconds before it expires and shares one request among callers.
type Conn struct {
	API     *API
	Session func() string // the Better Auth session token
	ID      string
	UserID  string
	HTTP    *http.Client // for streams; long polls need no client timeout

	mu      sync.Mutex
	grant   *Grant
	expires time.Time
	pending chan struct{}
}

// Grant returns a valid grant, fetching one when needed.
func (w *Conn) Grant(ctx context.Context) (*Grant, error) {
	for {
		w.mu.Lock()
		if w.grant != nil && time.Now().Before(w.expires) {
			g := w.grant
			w.mu.Unlock()
			return g, nil
		}
		if w.pending != nil {
			wait := w.pending
			w.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		done := make(chan struct{})
		w.pending = done
		w.mu.Unlock()

		g, err := w.API.StreamsGrant(ctx, w.Session(), w.ID)
		w.mu.Lock()
		w.pending = nil
		if err == nil {
			w.grant = g
			w.expires = time.Now().Add(time.Duration(max(1, g.ExpiresIn-30) * float64(time.Second)))
		}
		w.mu.Unlock()
		close(done)
		if err != nil {
			return nil, err
		}
		return g, nil
	}
}

// Invalidate drops the cached grant, after the gateway rejected it.
func (w *Conn) Invalidate() {
	w.mu.Lock()
	w.grant = nil
	w.mu.Unlock()
}

// Stream returns a client for a stream of this workspace, named without the
// workspace prefix, e.g. "meta" or "s:<session id>".
func (w *Conn) Stream(ctx context.Context, name string) (*streams.Client, error) {
	g, err := w.Grant(ctx)
	if err != nil {
		return nil, err
	}
	return &streams.Client{
		URL:  streams.StreamURL(g.GatewayBaseURL, w.ID+":"+name),
		HTTP: w.HTTP,
		Token: func(ctx context.Context) (string, error) {
			g, err := w.Grant(ctx)
			if err != nil {
				return "", err
			}
			return g.Token, nil
		},
	}, nil
}

// RPCError is an error reply from a machine.
type RPCError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e.Message != "" {
		return "machine: " + e.Message
	}
	return "machine: " + e.Code
}

// MachineRPC calls a method on a connected machine: the request goes to the
// machine's request stream and the reply comes back on a stream made for
// this call. A timeout means the outcome is unknown, not that it failed.
func (w *Conn) MachineRPC(ctx context.Context, machineID, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout+3*time.Second)
	defer cancel()
	id := uuid.NewString()
	replyName := fmt.Sprintf("rpc:res:%s:%s", machineID, uuid.NewString())
	reply, err := w.Stream(ctx, replyName)
	if err != nil {
		return nil, err
	}
	if err := reply.Create(ctx, "application/json", 300*time.Second); err != nil {
		return nil, err
	}
	requests, err := w.Stream(ctx, "rpc:req:"+machineID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	body, err := json.Marshal(map[string]any{
		"jsonrpc":     "2.0",
		"id":          id,
		"rpcVersion":  "1",
		"workspaceId": w.ID,
		"machineId":   machineID,
		"replyTo":     w.ID + ":" + replyName,
		"sentAt":      now,
		"expiresAt":   now + timeout.Milliseconds(),
		"method":      method,
		"params":      params,
	})
	if err != nil {
		return nil, err
	}
	if _, err := requests.Append(ctx, "application/json", body); err != nil {
		return nil, err
	}
	offset := "-1"
	for {
		r, err := reply.Read(ctx, offset, "", true)
		if err != nil {
			return nil, err
		}
		offset = r.NextOffset
		if len(r.Payload) == 0 {
			continue
		}
		if res, rpcErr, ok := findReply(r.Payload, id); ok {
			if rpcErr != nil {
				return nil, rpcErr
			}
			return res, nil
		}
	}
}

func findReply(payload []byte, id string) (json.RawMessage, *RPCError, bool) {
	type envelope struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *RPCError       `json:"error"`
	}
	var list []envelope
	if strings.HasPrefix(strings.TrimSpace(string(payload)), "[") {
		if json.Unmarshal(payload, &list) != nil {
			return nil, nil, false
		}
	} else {
		var one envelope
		if json.Unmarshal(payload, &one) != nil {
			return nil, nil, false
		}
		list = []envelope{one}
	}
	for _, e := range list {
		if e.ID == id {
			return e.Result, e.Error, true
		}
	}
	return nil, nil, false
}
