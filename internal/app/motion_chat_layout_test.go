package app

import (
	"testing"
	"time"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/collab"
	"github.com/lbr77/lody-mygo/internal/github"
)

// The side panel's + opens a drawn menu of the panels not open yet.
func TestPanelAddMenuDrawn(t *testing.T) {
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	a.panelOpen = true
	in := a.inspectFor("s1")
	in.openPanel(tabChanges)
	tt := snap(t, a, "panel-add-closed", false, nil)
	if err := tt.Click("Open a panel"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "panel-add-open")
	if !tt.HasText("Browser") || !tt.HasText("Files") {
		t.Fatalf("menu: %q", tt.Texts())
	}
	if err := tt.Click("Browser"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if in.tab != tabPreview || len(in.tabs) != 2 {
		t.Fatalf("browser: %+v", in.tabs)
	}
}

// The pull request's More actions is a drawn menu; its checks show while
// they run, and a running check turns Lody's Loader2.
func TestPRMoreMenuDrawn(t *testing.T) {
	a := fixture()
	s := &a.catalog.Sessions[0]
	s.PullRequests = []catalog.PullRequest{{URL: "https://github.com/lbr77/lody-mygo/pull/12", Status: "open"}}
	in := a.inspectFor("s1")
	in.openPanel(tabPR)
	a.panelOpen = true
	st := a.prPanelFor(s)
	mergeable := true
	st.pr = &github.PullRequest{Number: 12, Title: "Port the Flock runtime", Body: "Moves Flock.", State: "open", Mergeable: &mergeable, MergeableState: "clean",
		HTMLURL: "https://github.com/lbr77/lody-mygo/pull/12", User: github.User{Login: "ada"}, CreatedAt: time.Now().Add(-2 * time.Hour)}
	st.pr.Head.Ref, st.pr.Base.Ref = "flock-go", "main"
	st.checks = []github.CheckRun{{ID: 1, Name: "test", Status: "completed", Conclusion: "success"}, {ID: 2, Name: "lint", Status: "in_progress", HTMLURL: "https://example.com/run"}}
	tt := snap(t, a, "pr-checks-open", false, nil)
	if !tt.HasText("lint") {
		t.Fatalf("running checks should show their runs: %q", tt.Texts())
	}
	// The conversation header has a More actions of its own: open the
	// panel's.
	a.rowMenu("pr-more/s1").open = true
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "pr-more-menu")
	if !tt.HasText("Copy branch name") || !tt.HasText("Close pull request") {
		t.Fatalf("menu: %q", tt.Texts())
	}
	// The checks fold once asked, the chevron turning over 120ms.
	if err := tt.Click("Checks running"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if open, set := st.open["checks"]; !set || open {
		t.Fatalf("checks: %v %v", open, set)
	}
}

// A pointer close arms the strip's close mode, in which the tabs slide.
func TestTabCloseArmsSlide(t *testing.T) {
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.catalog.Sessions = append(a.catalog.Sessions,
		catalog.Session{ID: "s7", MachineID: "m1", Title: "Review the branch", Status: "idle", ProjectID: "m1:local:p1", ParentSessionID: "s1", CreatedAt: "2026-10-29T10:00:00.000Z"},
		catalog.Session{ID: "s8", MachineID: "m1", Title: "Write the docs", Status: "idle", ProjectID: "m1:local:p1", ParentSessionID: "s1", CreatedAt: "2026-10-29T11:00:00.000Z"})
	tt := snap(t, a, "tabs-three", false, nil)
	g := a.tabGroupFor("s1")
	if !g.closing.IsZero() {
		t.Fatal("armed before a close")
	}
	// The close shows with the pointer over the tab.
	r, ok := tt.Find("Review the branch")
	if !ok {
		t.Fatalf("no tab: %q", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if g.closing.IsZero() || !g.closed["s7"] {
		t.Fatalf("close: %+v", g)
	}
	tt.Advance(100 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "tabs-closing")
	tt.Advance(2 * time.Second)
	tt.Frame()
	if !tt.HasText("Write the docs") || tt.HasText("Review the branch") {
		t.Fatalf("after close: %q", tt.Texts())
	}
}

// The file tree's chevron turns rather than swapping glyphs.
func TestFileTreeChevronTurns(t *testing.T) {
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	a.conn = &cloud.Conn{ID: "w1"}
	a.panelOpen = true
	in := a.inspectFor("s1")
	in.dirs[""] = &dirListing{entries: []collab.DirEntry{{Name: "internal", Type: "directory"}}}
	in.dirs["internal"] = &dirListing{}
	in.openPanel(tabFiles)
	tt := snap(t, a, "tree-closed", false, nil)
	if err := tt.Click("internal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(100 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "tree-turning")
	if !in.open["internal"] {
		t.Fatal("folder didn't open")
	}
}
