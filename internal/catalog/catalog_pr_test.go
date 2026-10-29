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
