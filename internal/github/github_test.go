package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
			json.NewEncoder(w).Encode(map[string]any{"check_runs": []any{
				map[string]any{"id": 7, "name": "test", "status": "completed", "conclusion": "failure", "app": map[string]any{"name": "GitHub Actions"},
					"started_at": "2026-10-01T10:00:00Z", "completed_at": "2026-10-01T10:01:05Z"},
				map[string]any{"id": 5, "name": "test", "status": "completed", "conclusion": "success", "app": map[string]any{"name": "GitHub Actions"}},
				map[string]any{"id": 6, "name": "lint", "status": "in_progress", "app": nil},
			}})
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
	// The latest attempt of each check, by name.
	runs, err := c.CheckRuns(context.Background(), "o/r", "abc")
	if err != nil || len(runs) != 2 || runs[0].Name != "lint" || runs[1].ID != 7 || !runs[1].Failed() || runs[1].App.Name != "GitHub Actions" ||
		runs[1].CompletedAt.Sub(*runs[1].StartedAt) != 65*time.Second || runs[0].StartedAt != nil {
		t.Fatalf("%+v %v", runs, err)
	}
	err = c.Merge(context.Background(), "o/r", 12, "squash", "abc")
	if !IsStatus(err, 405) || !strings.Contains(err.Error(), "not mergeable") {
		t.Fatalf("merge: %v", err)
	}
	if calls[2] != `PUT /repos/o/r/pulls/12/merge Bearer tok {"merge_method":"squash","sha":"abc"}` {
		t.Fatalf("call %q", calls[2])
	}
}

// A pull request is a draft while GitHub reports its mergeability as a
// draft's, and stays pending ready for review until GitHub has worked
// its mergeability out.
func TestPullRequestStatus(t *testing.T) {
	yes := true
	for _, c := range []struct {
		pr      PullRequest
		status  string
		pending bool
	}{
		{PullRequest{State: "open", MergeableState: "draft"}, "draft", true},
		{PullRequest{State: "open", Mergeable: &yes, MergeableState: "clean"}, "open", false},
		{PullRequest{State: "open", MergeableState: "unknown", Mergeable: &yes}, "open", true},
		{PullRequest{State: "open"}, "open", true},
		{PullRequest{State: "closed", Merged: true, Draft: true}, "merged", false},
	} {
		if c.pr.Status() != c.status || c.pr.ReadyForReviewPending() != c.pending {
			t.Errorf("%+v: %s %v", c.pr, c.pr.Status(), c.pr.ReadyForReviewPending())
		}
	}
}

func TestIssuesAndPRs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/issues" || r.URL.Query().Get("state") != "open" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("page") == "2" {
			w.Write([]byte(`[{"number":3,"title":"Old","state":"open","updated_at":"2026-01-01T00:00:00Z"}]`))
			return
		}
		w.Write([]byte(`[{"number":7,"title":"Fix it","state":"open","updated_at":"2026-10-01T00:00:00Z","pull_request":{}},
			{"number":5,"title":"Broken","state":"open","updated_at":"2026-09-01T00:00:00Z"},
			{"number":4,"title":"Done","state":"closed","updated_at":"2026-10-02T00:00:00Z"}]`))
	}))
	defer srv.Close()
	old := API
	API = srv.URL
	defer func() { API = old }()
	got, err := (&Client{Token: "t"}).IssuesAndPRs(context.Background(), "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Number != 7 || !got[0].PR || got[1].Number != 5 || got[1].PR || got[2].Number != 3 {
		t.Fatalf("%+v", got)
	}
}

func TestBranches(t *testing.T) {
	var pages []string
	empty := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r":
			w.Write([]byte(`{"default_branch":"trunk"}`))
		case "/repos/o/r/branches":
			pages = append(pages, r.URL.Query().Get("page"))
			if empty {
				w.Write([]byte(`[]`))
				return
			}
			if r.URL.Query().Get("page") == "1" {
				var rows []map[string]string
				for i := 0; i < 100; i++ {
					rows = append(rows, map[string]string{"name": fmt.Sprintf("b%03d", i)})
				}
				json.NewEncoder(w).Encode(rows)
				return
			}
			w.Write([]byte(`[{"name":"a-last"},{"name":" b000 "}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := API
	API = srv.URL
	defer func() { API = old }()
	c := &Client{Token: "t"}
	got, def, err := c.Branches(context.Background(), "o/r")
	// Every page is read, names deduplicated and sorted, the default
	// branch among them.
	if err != nil || def != "trunk" || len(got) != 102 || got[0] != "a-last" || got[1] != "b000" || got[101] != "trunk" || strings.Join(pages, ",") != "1,2" {
		t.Fatalf("%d %q %v %v", len(got), def, pages, err)
	}
	// An empty repository has neither branches nor a default to pick.
	empty = true
	if got, def, err := c.Branches(context.Background(), "o/r"); err != nil || len(got) != 0 || def != "" {
		t.Fatalf("empty: %q %q %v", got, def, err)
	}
	if _, _, err := c.Branches(context.Background(), "o/missing"); !IsStatus(err, 404) {
		t.Fatalf("missing: %v", err)
	}
}
