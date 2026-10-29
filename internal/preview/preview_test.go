package preview

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lbr77/lody-mygo/internal/session"
)

func TestViewerURL(t *testing.T) {
	cases := []struct {
		public, path, want string
		ok                 bool
	}{
		{"https://calm-river-12.trycloudflare.com/?__lody_preview_token=abc", "/app?x=1", "https://calm-river-12.trycloudflare.com/app?__lody_preview_token=abc&x=1", true},
		{"https://calm-river-12.trycloudflare.com/?__lody_preview_token=abc", "", "https://calm-river-12.trycloudflare.com/?__lody_preview_token=abc", true},
		{"https://calm-river-12.trycloudflare.com/?__lody_preview_token=abc", "//evil.example/", "", false},
		{"https://example.com/?__lody_preview_token=abc", "/", "", false},
		{"http://calm-river-12.trycloudflare.com/?__lody_preview_token=abc", "/", "", false},
		{"https://calm-river-12.trycloudflare.com:8443/?__lody_preview_token=abc", "/", "", false},
		{"https://calm-river-12.trycloudflare.com/", "/", "", false},
	}
	for _, c := range cases {
		got, ok := ViewerURL(c.public, c.path)
		if got != c.want || ok != c.ok {
			t.Errorf("ViewerURL(%q, %q) = %q, %v; want %q, %v", c.public, c.path, got, ok, c.want, c.ok)
		}
	}
}

func TestLocalEndpoint(t *testing.T) {
	var calls []string
	rpc := func(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
		b, _ := json.Marshal(params)
		calls = append(calls, method+" "+string(b))
		switch method {
		case "session/preview-endpoint-acquire":
			return json.RawMessage(`{"success":true,"endpoint":{"endpointId":"e1","kind":"local-proxy","viewerUrl":"http://127.0.0.1:4700/p/e1/"}}`), nil
		case "session/preview-status":
			return json.RawMessage(`{"success":true,"connection":{"status":"active","endpointId":"e1"}}`), nil
		}
		return json.RawMessage(`{"success":true}`), nil
	}
	r := Request{Session: "s1", User: "u1"}
	ep, err := Acquire(context.Background(), rpc, r, session.PreviewTarget{Protocol: "http", Host: "localhost", Port: 3000})
	if err != nil || ep.ViewerURL != "http://127.0.0.1:4700/p/e1/" {
		t.Fatalf("acquire %+v %v", ep, err)
	}
	st, err := LocalStatus(context.Background(), rpc, r, "e1")
	if err != nil || st.Status != "active" {
		t.Fatalf("status %+v %v", st, err)
	}
	if err := Release(context.Background(), rpc, r, "e1"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`session/preview-endpoint-acquire {"requestedByUserId":"u1","sessionId":"s1","target":{"protocol":"http","host":"localhost","port":3000}}`,
		`session/preview-status {"renewEndpointId":"e1","requestedByUserId":"u1","sessionId":"s1"}`,
		`session/preview-endpoint-release {"endpointId":"e1","sessionId":"s1"}`,
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("call %d = %s", i, calls[i])
		}
	}
	bad := func(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
		return json.RawMessage(`{"success":true,"endpoint":{"endpointId":"e2","kind":"tunnel","viewerUrl":"https://x"}}`), nil
	}
	if _, err := Acquire(context.Background(), bad, r, session.PreviewTarget{}); err == nil {
		t.Error("a non-local endpoint is accepted")
	}
}
