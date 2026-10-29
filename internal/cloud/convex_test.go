package cloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestConvex(t *testing.T) {
	minted := 0
	exp := time.Now().Add(time.Hour).Unix()
	jwt := func(n int) string {
		claims, _ := json.Marshal(map[string]any{"exp": exp, "n": n})
		return "h." + base64.RawURLEncoding.EncodeToString(claims) + ".s"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/convex/token":
			if r.Header.Get("Authorization") != "Bearer sign-in" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			minted++
			json.NewEncoder(w).Encode(map[string]string{"token": jwt(minted)})
		case "/api/query":
			var body struct {
				Path string         `json:"path"`
				Args map[string]any `json:"args"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			// The first JWT is refused, as a stale one would be.
			if r.Header.Get("Authorization") == "Bearer "+jwt(1) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if body.Path == "usage:getWorkspaceUsageSummary" && body.Args["workspaceId"] == "w1" {
				fmt.Fprint(w, `{"status":"success","value":{"totals":{"tokens":42}}}`)
				return
			}
			fmt.Fprint(w, `{"status":"error","errorMessage":"Not a member"}`)
		}
	}))
	defer srv.Close()
	defer func(auth, convex string) { AuthOrigin, ConvexOrigin = auth, convex }(AuthOrigin, ConvexOrigin)
	AuthOrigin, ConvexOrigin = srv.URL, srv.URL
	c := &Convex{API: &API{}, Token: func() string { return "sign-in" }}
	var out struct {
		Totals struct{ Tokens int }
	}
	if err := c.Query(context.Background(), "usage:getWorkspaceUsageSummary", map[string]any{"workspaceId": "w1", "range": "month"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Totals.Tokens != 42 || minted != 2 {
		t.Fatalf("tokens %d, minted %d", out.Totals.Tokens, minted)
	}
	err := c.Query(context.Background(), "usage:getWorkspaceUsageSummary", map[string]any{"workspaceId": "w2"}, nil)
	if ce, ok := err.(*ConvexError); !ok || ce.Message != "Not a member" {
		t.Fatalf("err = %v", err)
	}
	if minted != 2 {
		t.Fatalf("minted again for a live JWT: %d", minted)
	}
}
