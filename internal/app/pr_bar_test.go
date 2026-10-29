package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/github"
)

// prBarFixture is the fixture's open conversation on acme/web, idle,
// with pull request #12 open, its details read.
func prBarFixture(t *testing.T) (*App, *catalog.Session, *prPanel) {
	t.Helper()
	// The merge method chosen is kept in a folder of the test's own.
	t.Setenv("LODY_DATA_DIR", t.TempDir())
	a := fixture()
	a.view.Entries, a.view.Working = a.view.Entries[:2], false
	// No quick replies, whose Commit & Push would read as the bar's.
	a.quickReplies = []quickReply{}
	s := a.catalog.Session("s1")
	s.Status, s.ProjectID, s.RepoFullName = "idle", "github:acme/web", "acme/web"
	s.Project = map[string]any{"kind": "github", "repoFullName": "acme/web"}
	s.PullRequests = []catalog.PullRequest{{URL: "https://github.com/acme/web/pull/12", Status: "open", Number: 12}}
	s.PRState = map[string]catalog.PRState{}
	live := a.prPanelFor(s)
	yes := true
	live.pr = &github.PullRequest{Number: 12, NodeID: "PR_12", State: "open", Mergeable: &yes, MergeableState: "clean", HTMLURL: "https://github.com/acme/web/pull/12"}
	live.pr.Head.Ref, live.pr.Head.SHA, live.pr.Base.Ref = "lody/web", "abc", "main"
	live.loadedAt = time.Now()
	return a, s, live
}

// fakeGitHub answers as GitHub for acme/web #12, through the client the
// app is given, and records what it was asked; the app's updates wait
// for the test to run them.
type fakeGitHub struct {
	mu     sync.Mutex
	calls  []string
	pr     func(n int) map[string]any // the pull request read the nth time
	reads  int
	checks []map[string]any
	posted chan func()
}

func newFakeGitHub(t *testing.T, a *App) *fakeGitHub {
	f := &fakeGitHub{posted: make(chan func(), 8)}
	f.pr = func(int) map[string]any {
		return map[string]any{"number": 12, "node_id": "PR_12", "state": "open", "mergeable": true, "mergeable_state": "clean",
			"html_url": "https://github.com/acme/web/pull/12", "head": map[string]any{"ref": "lody/web", "sha": "def"}, "base": map[string]any{"ref": "main"}}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path+" "+string(b))
		f.mu.Unlock()
		switch {
		case r.URL.Path == "/repos/acme/web/pulls/12":
			f.mu.Lock()
			f.reads++
			n := f.reads
			f.mu.Unlock()
			json.NewEncoder(w).Encode(f.pr(n))
		case strings.HasPrefix(r.URL.Path, "/repos/acme/web/commits/def/check-runs"):
			json.NewEncoder(w).Encode(map[string]any{"check_runs": f.checks})
		case r.URL.Path == "/repos/acme/web/pulls/12/merge":
			json.NewEncoder(w).Encode(map[string]any{"sha": "merged", "merged": true})
		case r.URL.Path == "/graphql":
			w.Write([]byte(`{"data":{}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	api, client := github.API, githubClient
	github.API = srv.URL
	githubClient = func(ctx context.Context, a *App, repo string, operate bool) (*github.Client, error) {
		return &github.Client{Token: map[bool]string{false: "read", true: "act"}[operate]}, nil
	}
	t.Cleanup(func() { github.API, githubClient = api, client })
	a.post = func(fn func()) { f.posted <- fn }
	return f
}

// settle runs the app's next update.
func (f *fakeGitHub) settle(t *testing.T) {
	t.Helper()
	select {
	case fn := <-f.posted:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("no update")
	}
}

// What the bar offers once a pull request exists, as Lody ranks it.
func TestInfoBarPRActionRanking(t *testing.T) {
	open := &catalog.PullRequest{Status: "open"}
	draft := &catalog.PullRequest{Status: "draft"}
	gs := func(pr *catalog.PullRequest, st catalog.PRState, dirty, unpushed, mutate bool) githubState {
		return githubState{repo: "acme/web", pr: pr, state: st, dirty: dirty, unpushed: unpushed, canMutate: mutate}
	}
	for i, c := range []struct {
		st   githubState
		want []string
	}{
		{gs(draft, catalog.PRState{}, true, false, true), []string{"commit-and-push", "ready-for-review"}},
		{gs(draft, catalog.PRState{CI: "f", Merge: "d"}, false, false, true), []string{"ready-for-review"}},
		{gs(open, catalog.PRState{CI: "f", Merge: "d"}, false, false, true), []string{"resolve-conflicts", "fix-ci-errors"}},
		{gs(open, catalog.PRState{CI: "e", Merge: "u"}, false, false, true), []string{"fix-ci-errors"}},
		{gs(open, catalog.PRState{CI: "s", Merge: "c"}, false, false, true), []string{"merge"}},
		{gs(open, catalog.PRState{Merge: "c"}, false, false, true), []string{"merge"}},
		{gs(open, catalog.PRState{CI: "p", Merge: "c"}, false, false, true), nil},
		{gs(open, catalog.PRState{CI: "f", Merge: "d"}, true, false, true), []string{"commit-and-push", "resolve-conflicts", "fix-ci-errors"}},
		// A commit whose push didn't land keeps Commit & Push ahead of Merge.
		{gs(open, catalog.PRState{CI: "s", Merge: "c"}, false, true, true), []string{"commit-and-push", "merge"}},
		{gs(&catalog.PullRequest{Status: "merged"}, catalog.PRState{Merge: "c"}, true, true, true), nil},
		// Without GitHub to act on, the agent's repairs stay.
		{gs(draft, catalog.PRState{}, false, false, false), nil},
		{gs(open, catalog.PRState{CI: "f", Merge: "c"}, false, false, false), []string{"fix-ci-errors"}},
		{gs(open, catalog.PRState{Merge: "c"}, false, false, false), nil},
	} {
		if got := infoActionIDs(c.st, false); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%d: %q, want %q", i, got, c.want)
		}
		if got := infoActionIDs(c.st, true); got != nil {
			t.Errorf("%d busy: %q", i, got)
		}
	}
	// The live details' state speaks over the recorded one.
	st := gs(open, catalog.PRState{Merge: "c"}, false, false, true)
	st.status = "merged"
	if got := infoActionIDs(st, false); got != nil {
		t.Fatalf("merged live: %q", got)
	}
}

// The bar leads with the pull request, in its state's hue with its CI's
// verdict, which opens the PR tab; the checks' pill opens their list.
func TestInfoBarPRPill(t *testing.T) {
	a, s, live := prBarFixture(t)
	s.PRState[s.PullRequests[0].URL] = catalog.PRState{CI: "s", Merge: "b"}
	tt := snap(t, a, "info-pr", false, nil)
	wantTexts(t, tt, "#12")
	if tt.HasText("CI passed") {
		t.Fatal("a CI pill without live checks")
	}
	wizardClick(t, tt, "Open pull request")
	if in := a.inspectFor("s1"); in.tab != tabPR || !a.panelOpen {
		t.Fatalf("PR tab %+v", in)
	}
	a.panelOpen = false

	// Live checks: a failure decides the verdict, and Fix CI Errors is
	// offered though the reported CI passed.
	start := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(65 * time.Second)
	live.checks = []github.CheckRun{
		{ID: 1, Name: "build", Status: "completed", Conclusion: "failure", HTMLURL: "https://github.com/acme/web/runs/1", StartedAt: &start, CompletedAt: &end},
		{ID: 2, Name: "deploy", Status: "in_progress"},
		{ID: 3, Name: "lint", Status: "completed", Conclusion: "cancelled"},
	}
	tt.Frame()
	if _, ok := tt.Find("CI checks · CI failed"); !ok {
		t.Fatalf("no CI pill in %q", tt.Texts())
	}
	wantTexts(t, tt, "Fix CI Errors")
	if ci := ciCompact(ciRuns(live.checks), "s"); ci != "f" {
		t.Fatalf("mark's CI %q", ci)
	}
	wizardClick(t, tt, "CI checks · CI failed")
	snapFrame(t, tt, "info-pr-ci")
	wantTexts(t, tt, "CI failed", "· 2/3", "build", "1m 05s", "deploy", "lint")
	var opened string
	open := openExternal
	openExternal = func(u string) { opened = u }
	defer func() { openExternal = open }()
	wizardClick(t, tt, "build")
	if opened != "https://github.com/acme/web/runs/1" {
		t.Fatalf("opened %q", opened)
	}

	// While CI runs, the pill counts what has settled.
	live.checks = live.checks[1:]
	*a.popover("ci/s1") = false
	tt.Frame()
	if _, ok := tt.Find("CI checks · CI running"); !ok || !tt.HasText("1/2") {
		t.Fatalf("running: %q", tt.Texts())
	}
}

// Merge leads once the pull request merges cleanly: the method chosen
// there is the PR tab's too, and merging sends GitHub the head it reads
// first, then marks the pull request merged as GitHub answered.
func TestInfoBarMerge(t *testing.T) {
	a, s, live := prBarFixture(t)
	s.PRState[s.PullRequests[0].URL] = catalog.PRState{CI: "s", Merge: "c"}
	f := newFakeGitHub(t, a)
	tt := snap(t, a, "info-merge", false, nil)
	wantTexts(t, tt, "Merge pull request")
	chevron, _ := tt.Find("Choose merge method")
	wizardClick(t, tt, "Choose merge method")
	snapFrame(t, tt, "info-merge-methods")
	wantTexts(t, tt, "Create a merge commit", "Squash and merge", "Rebase and merge")
	if menu, _ := tt.Find("Rebase and merge"); menu.Y+menu.H > chevron.Y || menu.X+menu.W > chevron.X+chevron.W+8 {
		t.Fatalf("the methods aren't above the chevron, to its end: %v, %v", menu, chevron)
	}
	wizardClick(t, tt, "Squash and merge")
	if a.mergeMethod() != "squash" || a.rowMenu("merge-method/s1").open {
		t.Fatalf("method %q", a.mergeMethod())
	}
	if b := New(); b.mergeMethod() != "squash" {
		t.Fatalf("not kept: %q", b.mergeMethod())
	}
	wantTexts(t, tt, "Squash and merge")

	wizardClick(t, tt, "Squash and merge")
	if live.busy != "merge" {
		t.Fatalf("busy %q", live.busy)
	}
	wantTexts(t, tt, "Merging…")
	f.settle(t)
	tt.Frame()
	if !live.pr.Merged || live.busy != "" {
		t.Fatalf("pr %+v", live.pr)
	}
	if f.calls[len(f.calls)-1] != `PUT /repos/acme/web/pulls/12/merge {"merge_method":"squash","sha":"def"}` {
		t.Fatalf("calls %q", f.calls)
	}
	if tt.HasText("Squash and merge") || tt.HasText("Merging…") {
		t.Fatalf("still offered once merged: %q", tt.Texts())
	}

	// Unpublished work puts Commit & Push first and Merge behind it.
	live.pr.Merged, live.pr.State = false, "open"
	s.WorkspaceUnpushed = true
	tt.Frame()
	wantTexts(t, tt, "Commit & Push")
	if tt.HasText("Squash and merge") {
		t.Fatal("Merge isn't behind the chevron")
	}
	openChevron(t, tt, "Commit & Push")
	wantTexts(t, tt, "Squash and merge")
}

// openChevron opens the menu behind the chevron on the end of the bar's
// leading action.
func openChevron(t *testing.T, tt *ui.Tester, first string) {
	t.Helper()
	b, ok := tt.Find(first)
	if !ok {
		t.Fatalf("no %q in %q", first, tt.Texts())
	}
	tt.ClickAt(b.X+b.W+10, b.Y+b.H/2)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
}

// Fix CI Errors reads the checks again, then asks the agent about those
// that failed, or says why it can't.
func TestInfoBarFixCI(t *testing.T) {
	a, s, live := prBarFixture(t)
	s.PRState[s.PullRequests[0].URL] = catalog.PRState{CI: "f", Merge: "u"}
	f := newFakeGitHub(t, a)
	tt := snap(t, a, "info-fix-ci", false, nil)
	wantTexts(t, tt, "Fix CI Errors")

	// None failed after all.
	f.checks = []map[string]any{{"id": 1, "name": "build", "status": "completed", "conclusion": "success"}}
	wizardClick(t, tt, "Fix CI Errors")
	if !live.fixing {
		t.Fatal("not reading the checks")
	}
	f.settle(t)
	if live.fixing || len(a.toasts) != 1 || a.toasts[0].Title != "No failing CI checks were found" || live.pr.Head.SHA != "def" || len(live.checks) != 1 {
		t.Fatalf("toasts %+v, pr %+v", a.toasts, live.pr)
	}
	a.toasts = nil

	// One failed, but the conversation can't take the turn here.
	f.checks = []map[string]any{{"id": 2, "name": "build", "status": "completed", "conclusion": "failure", "html_url": "https://github.com/acme/web/runs/2"}}
	tt.Frame()
	wizardClick(t, tt, "Fix CI Errors")
	f.settle(t)
	if len(a.toasts) != 1 || a.toasts[0].Title != "Failed to send the CI fix request" {
		t.Fatalf("toasts %+v", a.toasts)
	}
	a.toasts = nil

	// The checks can't be read.
	githubClient = func(context.Context, *App, string, bool) (*github.Client, error) {
		return nil, &github.Error{Status: 403, Message: "Resource not accessible by integration"}
	}
	tt.Frame()
	wizardClick(t, tt, "Fix CI Errors")
	f.settle(t)
	if len(a.toasts) != 1 || a.toasts[0].Title != "Failed to load the failed CI checks" || !strings.Contains(a.toasts[0].Description, "Resource not accessible") {
		t.Fatalf("toasts %+v", a.toasts)
	}
}

// Fix CI Errors' prompt names the pull request as GitHub has it and at
// most 12 of its failed checks; Resolve Conflicts' names it by number.
func TestInfoBarPrompts(t *testing.T) {
	pr := &github.PullRequest{Number: 12, HTMLURL: "https://github.com/acme/web/pull/12"}
	pr.Head.Ref, pr.Head.SHA, pr.Base.Ref = "lody/web", "def", "main"
	var checks []github.CheckRun
	for i := range 14 {
		r := github.CheckRun{ID: int64(i), Name: "test-" + string(rune('a'+i)), Status: "completed", Conclusion: "failure"}
		if i == 0 {
			r.App.Name, r.HTMLURL, r.Conclusion = "GitHub Actions", "https://github.com/acme/web/runs/0", "timed_out"
		}
		checks = append(checks, r)
	}
	checks = append(checks, github.CheckRun{Name: "skipped", Status: "completed", Conclusion: "cancelled"}, github.CheckRun{Name: "going", Status: "in_progress"})
	got := fixCIPrompt("acme/web", pr, checks)
	if !strings.HasPrefix(got, "Fix the failing CI checks for acme/web pull request #12.\n\nInspect the complete GitHub Actions/check logs yourself") ||
		!strings.Contains(got, "Current PR snapshot:\n- URL: https://github.com/acme/web/pull/12\n- Base: main\n- Head: lody/web\n- Head SHA: def\n\n") ||
		!strings.Contains(got, "Failed checks:\n- test-a · GitHub Actions · timed_out · https://github.com/acme/web/runs/0\n- test-b · failure\n") ||
		!strings.HasSuffix(got, "- test-l · failure\n- …and 2 more failed checks; fetch the full list from GitHub.") || strings.Contains(got, "skipped") {
		t.Fatalf("prompt %q", got)
	}
	if fixCIPrompt("acme/web", pr, checks[14:]) != "" {
		t.Fatal("a prompt with nothing failed")
	}
	if long := fixCIPrompt("acme/web", pr, []github.CheckRun{{Name: strings.Repeat("😀", 4000), Status: "completed", Conclusion: "failure"}}); len(utf16Units(long)) > 6000 || len(long) < 1000 {
		t.Fatalf("long prompt %d", len(utf16Units(long)))
	}
	if got := resolveConflictsPrompt("acme/web", 12, "https://github.com/acme/web/pull/12"); got !=
		"Resolve the merge conflicts for acme/web pull request #12 against its base branch.\nPR: https://github.com/acme/web/pull/12\n\nInspect the pull request and repository instructions first. Choose the merge or rebase workflow that matches this repository’s conventions, preserve the intent of both sides, run the relevant checks, and push the resolved branch." {
		t.Fatalf("resolve %q", got)
	}
	if got := resolveConflictsPrompt("acme/web", 0, "https://example.com/pr"); !strings.Contains(got, "pull request https://example.com/pr against") {
		t.Fatalf("resolve by URL %q", got)
	}
}

func utf16Units(s string) []rune {
	var out []rune
	for _, r := range s {
		out = append(out, r)
		if r >= 0x10000 {
			out = append(out, r)
		}
	}
	return out
}

// Resolve Conflicts leads while the pull request conflicts; the PR tab's
// Resolve conflicts goes with it, waiting while it can't be used.
func TestInfoBarResolveConflicts(t *testing.T) {
	a, s, _ := prBarFixture(t)
	s.PRState[s.PullRequests[0].URL] = catalog.PRState{CI: "s", Merge: "d"}
	tt := snap(t, a, "info-conflicts", false, nil)
	wantTexts(t, tt, "Resolve Conflicts")
	if offered, usable := a.offersInfoAction(s, "resolve-conflicts"); !offered || !usable {
		t.Fatalf("offered %v usable %v", offered, usable)
	}
	a.view.Status = "connecting"
	if offered, usable := a.offersInfoAction(s, "resolve-conflicts"); !offered || usable {
		t.Fatalf("while loading: offered %v usable %v", offered, usable)
	}
	// Once the agent works, it's gone from both.
	a.view.Status, a.view.Working = "live", true
	if offered, _ := a.offersInfoAction(s, "resolve-conflicts"); offered {
		t.Fatal("offered while the agent works")
	}
}

// Ready for review marks a draft ready, then waits for GitHub to settle
// it; it fails, in the app's language, if GitHub still has a draft.
func TestInfoBarReadyForReview(t *testing.T) {
	a, s, live := prBarFixture(t)
	s.PullRequests[0].Status = "draft"
	live.pr.Draft, live.pr.MergeableState = true, "draft"
	settle, poll := readySettle, readyPoll
	readySettle, readyPoll = time.Second, time.Millisecond
	defer func() { readySettle, readyPoll = settle, poll }()
	f := newFakeGitHub(t, a)
	base := f.pr
	f.pr = func(n int) map[string]any {
		pr := base(n)
		if n < 3 {
			pr["draft"], pr["mergeable_state"] = true, "draft"
		}
		return pr
	}
	tt := snap(t, a, "info-ready", false, nil)
	wizardClick(t, tt, "Ready for review")
	wantTexts(t, tt, "Marking…")
	f.settle(t)
	tt.Frame()
	if live.busy != "" || live.pr.Status() != "open" || f.reads != 3 || len(a.toasts) != 0 {
		t.Fatalf("pr %+v after %d reads, toasts %+v", live.pr, f.reads, a.toasts)
	}
	if !strings.HasPrefix(f.calls[0], "POST /graphql ") || !strings.Contains(f.calls[0], `"id":"PR_12"`) {
		t.Fatalf("calls %q", f.calls)
	}
	if tt.HasText("Ready for review") {
		t.Fatalf("still offered: %q", tt.Texts())
	}

	// Still a draft once settling is over.
	live.pr.Draft, live.pr.MergeableState = true, "draft"
	f.pr = func(n int) map[string]any {
		pr := base(n)
		pr["draft"] = true
		return pr
	}
	readySettle = 0
	tt.Frame()
	wizardClick(t, tt, "Ready for review")
	f.settle(t)
	if len(a.toasts) != 1 || a.toasts[0].Title != "Failed to mark as ready for review" ||
		a.toasts[0].Description != "GitHub is still reporting this pull request as a draft. Try again in a moment." {
		t.Fatalf("toasts %+v", a.toasts)
	}
}

// The checks as the bar shows them: a completed one by its conclusion,
// cancelled and stale never a failure, how long each ran.
func TestCIRuns(t *testing.T) {
	start := time.Now()
	end := start.Add(3 * time.Second)
	runs := ciRuns([]github.CheckRun{
		{Name: "a", Status: "queued"}, {Name: "b", Status: "waiting"}, {Name: "c", Status: "in_progress"},
		{Name: "d", Status: "completed", Conclusion: "success", StartedAt: &start, CompletedAt: &end},
		{Name: "e", Status: "completed", Conclusion: "neutral"}, {Name: "f", Status: "completed", Conclusion: "stale"},
		{Name: "g", Status: "completed", Conclusion: "action_required"},
	})
	var got []string
	for _, r := range runs {
		got = append(got, r.status)
	}
	if strings.Join(got, " ") != "queued queued running success skipped cancelled failure" || runs[3].dur != 3*time.Second {
		t.Fatalf("%q %v", got, runs[3].dur)
	}
	if ciOverall(runs) != "failing" || ciOverall(runs[:6]) != "running" || ciOverall(runs[3:6]) != "passing" {
		t.Fatal("verdicts")
	}
}

// In Chinese, the actions and prompts say what Lody's zh_CN says.
func TestInfoBarPRChinese(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	defer i18n.SetLocaleFunc(func() string { return "en" })
	a, s, _ := prBarFixture(t)
	s.PRState[s.PullRequests[0].URL] = catalog.PRState{CI: "f", Merge: "d"}
	tt := snap(t, a, "info-pr-chinese", false, nil)
	wantTexts(t, tt, "解决冲突")
	openChevron(t, tt, "解决冲突")
	wantTexts(t, tt, "修复 CI 错误")
	if got := resolveConflictsPrompt("acme/web", 12, "u"); !strings.HasPrefix(got, "解决 acme/web PR #12 与其目标分支之间的合并冲突。\nPR: u\n\n") {
		t.Fatalf("prompt %q", got)
	}
	if got := mergeMethodLabel("squash"); got != "Squash 合并" {
		t.Fatalf("label %q", got)
	}
}
