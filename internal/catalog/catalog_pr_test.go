package catalog

import "testing"

func TestLatestPR(t *testing.T) {
	s := Session{PullRequests: []PullRequest{
		{URL: "https://github.com/o/r/pull/3", Status: "merged", ReportedAt: "2026-10-02"},
		{URL: "https://github.com/o/r/pull/9", Status: "open", ReportedAt: "2026-10-05"},
		{URL: "https://github.com/o/r/pull/5", Status: "closed", ReportedAt: "2026-10-01"},
	}}
	if pr := s.LatestPR(); pr == nil || pr.PRNumber() != 9 {
		t.Fatalf("latest %+v", pr)
	}
	if !(PRState{Merge: "c"}).Ready() || (PRState{Merge: "c", CI: "p"}).Ready() || (PRState{CI: "s"}).Ready() {
		t.Fatal("ready")
	}
}

func TestGitHubProjectSession(t *testing.T) {
	n := NewSession{ID: "s", GitHubProject: "o/r"}
	if n.LocalProject != "" || n.GitHubProject != "o/r" {
		t.Fatal("fields")
	}
	// A repository session starts in a worktree of its own from the
	// branch picked, its base.
	n.Branch = " feat/x "
	meta := n.Meta()
	project, _ := meta["project"].(map[string]any)
	if project["kind"] != "github" || project["repoFullName"] != "o/r" || project["branch"] != "feat/x" ||
		meta["repoFullName"] != "o/r" || meta["baseBranch"] != "feat/x" || meta["isWorktree"] != true {
		t.Fatalf("meta %v", meta)
	}
	// An empty repository has no branch: no base either.
	n.Branch = ""
	if meta := n.Meta(); meta["baseBranch"] != nil || meta["isWorktree"] != true {
		t.Fatalf("empty repository %v", meta)
	}
	// A local project's own worktree makes the session one; its folder
	// does not.
	local := NewSession{ID: "s", LocalProject: "p1", Worktree: true, Branch: "main"}
	if meta := local.Meta(); meta["isWorktree"] != true || meta["baseBranch"] != nil {
		t.Fatalf("worktree %v", meta)
	}
	local.Worktree = false
	if meta := local.Meta(); meta["isWorktree"] != nil {
		t.Fatalf("folder %v", meta)
	}
}
