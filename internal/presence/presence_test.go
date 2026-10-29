package presence

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	loro "github.com/aholstenson/loro-go"

	"github.com/lbr77/lody-mygo/internal/cloud"
)

func TestOnline(t *testing.T) {
	machine := loro.NewEphemeralStore(TTL.Milliseconds())
	now := time.Now()
	machine.SetAny("machine:m1:i1", map[string]any{"kind": "machine", "machineId": "m1", "instanceId": "i1", "updatedAt": float64(now.UnixMilli())})
	machine.SetAny("machine:m2:i1", map[string]any{"kind": "machine", "machineId": "m2", "instanceId": "i1", "updatedAt": float64(now.Add(-2 * TTL).UnixMilli())})
	machine.SetAny("user:u1", map[string]any{"kind": "user", "machineId": "m3", "instanceId": "x", "updatedAt": float64(now.UnixMilli())})
	update := machine.EncodeAll()

	// Served as the gateway's presence channel does.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ephemeral") != "presence" || r.URL.Query().Get("live") != "sse" || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Stream-SSE-Data-Encoding", "base64")
		fmt.Fprintf(w, "event: bootstrap\ndata: %s\n\n", base64.StdEncoding.EncodeToString(update))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	conn := &cloud.Conn{ID: "ws", HTTP: srv.Client()}
	conn.SetGrant(&cloud.Grant{Token: "tok", GatewayBaseURL: srv.URL, ExpiresIn: 600})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got := make(chan State, 4)
	go Watch(ctx, conn, func(s State) { got <- s })
	for {
		select {
		case s := <-got:
			if !s.Live {
				continue
			}
			if len(s.Online) != 1 || s.Online[0] != "m1" || !s.IsOnline("m1") || s.IsOnline("m2") {
				t.Fatalf("state = %+v", s)
			}
			return
		case <-ctx.Done():
			t.Fatal("no live presence")
		}
	}
}
