package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParsePR(t *testing.T) {
	repo, n, ok := ParsePR("https://github.com/lbr77/lody-mygo/pull/12/files")
	if !ok || repo != "lbr77/lody-mygo" || n != 12 {
		t.Fatalf("%s %d %v", repo, n, ok)
	}
	if _, _, ok := ParsePR("https://gitlab.com/x/y/pull/1"); ok {
		t.Fatal("not GitHub")
	}
}

func TestClient(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		calls = append(calls, r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("Authorization")+" "+string(b))
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/12") && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"number": 12, "title": "Port", "state": "open", "draft": true, "head": map[string]any{"ref": "feat", "sha": "abc"}, "base": map[string]any{"ref": "main"}})
		case strings.Contains(r.URL.Path, "/check-runs"):
			json.NewEncoder(w).Encode(map[string]any{"check_runs": []any{map[string]any{"name": "test", "status": "completed", "conclusion": "failure"}}})
		case strings.HasSuffix(r.URL.Path, "/merge"):
			w.WriteHeader(405)
			json.NewEncoder(w).Encode(map[string]any{"message": "Pull Request is not mergeable"})
		default:
			w.Write([]byte("[]"))
		}
	}))
	defer srv.Close()
	API = srv.URL
	c := &Client{Token: "tok"}
	pr, err := c.PullRequest(context.Background(), "o/r", 12)
	if err != nil || pr.Status() != "draft" || pr.Head.SHA != "abc" {
		t.Fatalf("%+v %v", pr, err)
	}
	runs, err := c.CheckRuns(context.Background(), "o/r", "abc")
	if err != nil || len(runs) != 1 || !runs[0].Failed() {
		t.Fatalf("%+v %v", runs, err)
	}
	err = c.Merge(context.Background(), "o/r", 12, "squash")
	if !IsStatus(err, 405) || !strings.Contains(err.Error(), "not mergeable") {
		t.Fatalf("merge: %v", err)
	}
	if calls[2] != `PUT /repos/o/r/pulls/12/merge Bearer tok {"merge_method":"squash"}` {
		t.Fatalf("call %q", calls[2])
	}
}
