package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/i18n"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/collab"
	"github.com/lbr77/lody-mygo/internal/session"
)

// runFixture is the fixture's open conversation started in Shop, a
// project of several: acme/web, with acme/proto its submodule, and the
// website folder used in place; web and proto have pull requests.
func runFixture() *App {
	a := fixture()
	a.view.Entries, a.view.Working = a.view.Entries[:2], false
	s := a.catalog.Session("s1")
	s.Status, s.ProjectID, s.RepoFullName = "idle", "github:acme/web", "acme/web"
	s.Project = map[string]any{"kind": "github", "repoFullName": "acme/web", "branch": "HEAD"}
	s.ProjectGroupID, s.ProjectGroupName = "g", "Shop"
	s.ProjectRun = &catalog.ProjectRun{ID: "g", Name: "Shop", Root: "project-shop-g", Members: []catalog.RunMember{
		{ID: "web1", Kind: "github", RepoFullName: "acme/web", Dir: "web", Path: "web", Base: "HEAD", Access: "worktree", Branch: "lody/web", HasDiff: true, DiffAdd: 3, DiffDel: 1},
		{ID: "docs1", Order: 2, Kind: "local", MachineID: "m1", LocalProjectID: "p2", Dir: "docs", Path: "docs", Base: "HEAD", Access: "additional"},
		{ID: "proto1", Order: 1, Kind: "github", RepoFullName: "acme/proto", Parent: "web1", SubPath: "vendor/proto", Path: "web/vendor/proto", Base: "gitlink", Branch: "lody/proto"},
	}}
	s.PullRequests = []catalog.PullRequest{
		{URL: "https://github.com/acme/web/pull/12", Status: "open", Number: 12},
		{URL: "https://github.com/acme/proto/pull/3", Status: "draft", Number: 3},
	}
	s.PRState = map[string]catalog.PRState{"https://github.com/acme/web/pull/12": {CI: "s"}}
	return a
}

// The rows go as the members nest, each with its repository's current
// pull request; the one described is the one chosen, else the session's
// own repository, else the first.
func TestProjectMemberRows(t *testing.T) {
	a := runFixture()
	owner, rows, picked := a.projectMembers(a.catalog.Session("s1"))
	if owner == nil || len(rows) != 3 || picked == nil || picked.id != "web1" {
		t.Fatalf("rows %+v, picked %+v", rows, picked)
	}
	var got []string
	for _, r := range rows {
		got = append(got, r.id+"/"+string(rune('0'+r.depth)))
	}
	if strings.Join(got, " ") != "web1/0 proto1/1 docs1/0" {
		t.Fatalf("order %q", got)
	}
	if rows[0].pr == nil || rows[0].pr.number != 12 || rows[0].pr.ci != "s" || rows[1].pr == nil || rows[1].pr.status != "draft" || rows[2].pr != nil {
		t.Fatalf("prs %+v %+v %+v", rows[0].pr, rows[1].pr, rows[2].pr)
	}
	if rows[2].localRoot != "/src/website" || !rows[2].inPlace() || !rows[1].submodule {
		t.Fatalf("docs %+v", rows[2])
	}
	// The latest of a repository's pull requests is its current one.
	s := a.catalog.Session("s1")
	s.PullRequests = append(s.PullRequests, catalog.PullRequest{URL: "https://github.com/ACME/web/pull/15", Status: "merged"})
	if _, rows, _ := a.projectMembers(s); rows[0].pr.number != 15 || rows[0].pr.status != "merged" {
		t.Fatalf("current %+v", rows[0].pr)
	}
	if r := resolveMemberRow(rows, "", "ACME/PROTO"); r == nil || r.id != "proto1" {
		t.Fatalf("fallback %+v", r)
	}
	if r := resolveMemberRow(rows, "gone", ""); r == nil || r.id != "web1" {
		t.Fatalf("first %+v", r)
	}
	// A parent missing lists its submodule at the top.
	s.ProjectRun.Members = s.ProjectRun.Members[1:]
	if _, rows, _ := a.projectMembers(s); len(rows) != 2 || rows[1].id != "proto1" || rows[1].depth != 0 {
		t.Fatalf("orphan %+v", rows)
	}
}

// The info bar's Repositories opens the panel above the bar: the
// project, how many repositories and pull requests, each repository where
// it sits with its branch or, used in place, its folder. Choosing one
// makes the bar, the PR tab and the header describe it; opening its pull
// request chooses it too.
func TestProjectMembersPanel(t *testing.T) {
	a := runFixture()
	tt := snap(t, a, "run-info-bar", false, nil)
	wantTexts(t, tt, "acme/web", "lody/web", "+3", "-1")
	chip, ok := tt.Find("Repositories")
	if !ok || !tt.HasText("3") {
		t.Fatalf("no Repositories chip in %q", tt.Texts())
	}
	wizardClick(t, tt, "Repositories")
	snapFrame(t, tt, "run-members")
	wantTexts(t, tt, "Shop", "3 repositories · 2 PRs", "acme/proto", "web/vendor/proto", "lody/proto", "docs", "/src/website", "#12", "#3")
	web, _ := tt.Find("acme/web · web · lody/web")
	proto, _ := tt.Find("acme/proto · web/vendor/proto · lody/proto")
	if proto.X <= web.X || proto.Y <= web.Y || web.Y >= chip.Y {
		t.Fatalf("rows %v, %v over the chip %v", web, proto, chip)
	}
	wizardClick(t, tt, "acme/proto · web/vendor/proto · lody/proto")
	if a.memberPicks["s1"] != "proto1" || *a.popover("members/s1") {
		t.Fatalf("picked %q", a.memberPicks)
	}
	wantTexts(t, tt, "acme/proto", "lody/proto")
	if tt.HasText("lody/web") || tt.HasText("+3") {
		t.Fatalf("the bar still describes web: %q", tt.Texts())
	}
	if src, pr := a.sessionPR(a.catalog.Session("s1")); src.ID != "s1" || pr == nil || pr.Number != 3 {
		t.Fatalf("pr %+v", pr)
	}
	if st := a.prPanelFor(a.catalog.Session("s1")); st == nil || st.repo != "acme/proto" || st.number != 3 {
		t.Fatalf("PR tab %+v", st)
	}
	if opts := a.panelOptions(); opts[len(opts)-1].id != tabPR {
		t.Fatalf("panels %+v", opts)
	}

	// A folder used in place has no pull request: none is borrowed.
	a.pickMember("s1", "docs1")
	tt.Frame()
	wantTexts(t, tt, "docs")
	if _, pr := a.sessionPR(a.catalog.Session("s1")); pr != nil {
		t.Fatalf("borrowed %+v", pr)
	}
	for _, o := range a.panelOptions() {
		if o.id == tabPR {
			t.Fatal("a PR tab without a pull request")
		}
	}

	// Its pull request's button chooses it and opens it.
	var opened string
	open := openExternal
	openExternal = func(u string) { opened = u }
	defer func() { openExternal = open }()
	wizardClick(t, tt, "Repositories")
	wizardClick(t, tt, "Open pull request #12 · CI passed")
	if opened != "https://github.com/acme/web/pull/12" || a.memberPicks["s1"] != "web1" || *a.popover("members/s1") {
		t.Fatalf("opened %q, picked %q", opened, a.memberPicks)
	}
	// A child tab describes what its root does.
	child := catalog.Session{ID: "c1", MachineID: "m1", Title: "child", Status: "idle", ParentSessionID: "s1", ProjectID: "github:acme/web"}
	a.catalog.Sessions = append(a.catalog.Sessions, child)
	if _, pr := a.sessionPR(a.catalog.Session("c1")); pr == nil || pr.Number != 12 {
		t.Fatalf("child pr %+v", pr)
	}
}

// Create PR leads the bar's actions once there are changes, Create Draft
// PR and Commit & Push behind its chevron; in a project of several each
// prompt carries the project's scope.
func TestProjectRunPrompts(t *testing.T) {
	a := runFixture()
	// No pull request yet, and uncommitted changes.
	a.catalog.Session("s1").WorkspaceDirty = true
	prs := a.catalog.Session("s1").PullRequests
	a.catalog.Session("s1").PullRequests = nil
	tt := snap(t, a, "run-actions", false, nil)
	wantTexts(t, tt, "Create PR")
	// The chevron on the pill's end.
	create, _ := tt.Find("Create PR")
	tt.ClickAt(create.X+create.W+10, create.Y+create.H/2)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if !a.rowMenu("info-actions/s1").open {
		t.Fatal("the chevron opened nothing")
	}
	wantTexts(t, tt, "Create Draft PR", "Commit & Push")
	if menu, _ := tt.Find("Create Draft PR"); menu.Y >= create.Y {
		t.Fatalf("the menu isn't above the bar: %v, %v", menu, create)
	}
	snapFrame(t, tt, "run-actions-menu")

	s := a.catalog.Session("s1")
	pr := a.infoPrompt(s, "create-pr")
	if !strings.HasPrefix(pr, "(Sent by the **Create PR** button in Lody.)\n\nCreate a PR for the current worktree (a regular PR, not a draft).") ||
		!strings.Contains(pr, "\n\nAfter the PR exists, keep it current:") || !strings.HasSuffix(pr, projectGroupPrScope) ||
		!strings.Contains(pr, "silently.\n\nProject group scope:\n- Apply the requested action") {
		t.Fatalf("create PR prompt %q", pr)
	}
	if push := a.infoPrompt(s, "commit-and-push"); !strings.HasPrefix(push, "Publish the current branch:") || !strings.HasSuffix(push, projectGroupPrScope) {
		t.Fatalf("commit prompt %q", push)
	}
	plain := &catalog.Session{ID: "x"}
	if draft := a.infoPrompt(plain, "create-draft-pr"); strings.Contains(draft, "Project group scope") || !strings.HasPrefix(draft, "(Sent by the **Create Draft PR** button in Lody.)") {
		t.Fatalf("draft prompt %q", draft)
	}

	// What they offer, as Lody ranks it.
	st := githubState{repo: "acme/web", hasChanges: true}
	for _, c := range []struct {
		st   githubState
		busy bool
		want []string
	}{
		{st, false, []string{"create-pr", "create-draft-pr"}},
		{githubState{repo: "acme/web", hasChanges: true, dirty: true}, false, []string{"create-pr", "create-draft-pr", "commit-and-push"}},
		{st, true, nil},
		{githubState{hasChanges: true, dirty: true}, false, nil},
		{githubState{repo: "acme/web", pr: &catalog.PullRequest{Status: "open"}, unpushed: true}, false, []string{"commit-and-push"}},
		{githubState{repo: "acme/web", pr: &catalog.PullRequest{Status: "open"}}, false, nil},
		{githubState{repo: "acme/web", pr: &catalog.PullRequest{Status: "merged"}, dirty: true}, false, nil},
	} {
		if got := infoActionIDs(c.st, c.busy); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%+v busy %v: %q, want %q", c.st, c.busy, got, c.want)
		}
	}

	// With its pull request, Commit & Push alone, while work is
	// unpublished.
	s.PullRequests = prs
	tt.Frame()
	if tt.HasText("Create PR") || !tt.HasText("Commit & Push") {
		t.Fatalf("with a PR: %q", tt.Texts())
	}
	// A member's committed changes count though the totals are missing.
	s.WorkspaceDirty = false
	if st := a.sessionGitHub(s); !st.hasChanges || st.repo != "acme/web" || st.pr == nil || st.pr.Number != 12 {
		t.Fatalf("state %+v", st)
	}
	// The composer keeps what it held.
	a.draft = "half a thought"
	a.dispatchPrompt("Create a PR")
	if a.draft != "half a thought" {
		t.Fatalf("draft %q", a.draft)
	}
}

// The members used in place are roots of their own: their files show at
// their directories, in the Files tree among the project's own, and in a
// reply's edited files.
func TestProjectRunFileRoots(t *testing.T) {
	a := runFixture()
	s := a.catalog.Session("s1")
	if got := a.fileMounts(s); !reflect.DeepEqual(got, []collab.Mount{{Root: "member:docs1", Path: "docs", LocalProject: "p2"}}) {
		t.Fatalf("mounts %+v", got)
	}
	if got := a.displayPath("\x00lody-root:member:docs1/guide/intro.md"); got != "docs/guide/intro.md" {
		t.Fatalf("display %q", got)
	}
	entries := mountEntries([]collab.DirEntry{{Name: "zeta.go", Type: "file"}, {Name: "docs", Type: "file"}, {Name: "api", Type: "directory"}},
		[]collab.Mount{{Root: "member:docs1", Path: "docs"}})
	if len(entries) != 3 || entries[0].Name != "api" || entries[1].Name != "docs" || !entries[1].Dir() || entries[2].Name != "zeta.go" {
		t.Fatalf("entries %+v", entries)
	}
	// A reply's edited file of a member shows at its directory.
	a.view.Entries[1].FileDiffs = []session.FileDiff{{Path: "\x00lody-root:member:docs1/guide.md", Add: 2}}
	tt := snap(t, a, "run-edited-files", false, nil)
	wantTexts(t, tt, "guide.md", "docs")
	if tt.HasText("lody-root") {
		t.Fatalf("a key shows: %q", tt.Texts())
	}
	// Elsewhere nothing mounts.
	a.catalog.Session("s1").ProjectRun = nil
	if a.fileMounts(s) != nil {
		t.Fatal("mounts without a run")
	}
}

// The projects of several reorder by their grips: one dragged over
// another follows the pointer as the other slides aside, and dropped,
// takes its place, the order kept by workspace.
func TestSidebarGroupDrag(t *testing.T) {
	t.Cleanup(func() { os.Remove(filepath.Join(dataDir(), groupOrderFile)) })
	a := sidebarFixture()
	a.catalog.Groups = []catalog.ProjectGroup{
		{ID: "d", Name: "Docs", OwnerUserID: "u1", Members: []catalog.ProjectGroupMember{{ID: "x", Kind: "local", MachineID: "m1", LocalProjectID: "p1", Dir: "lody"}}},
		shopGroup("u1"),
	}
	a.catalog.Sessions = append(a.catalog.Sessions, catalog.Session{ID: "s9", MachineID: "m1", Title: "Ship the shop", Status: "idle", ProjectID: "m1:local:p2", ProjectGroupID: "g", ProjectGroupName: "Shop"})
	tt := snap(t, a, "sidebar-group-order", false, nil)
	docs, _ := tt.Find("Docs")
	shop, _ := tt.Find("Shop")
	if shop.Y <= docs.Y {
		t.Fatalf("not by name: %v %v", docs, shop)
	}
	tt.Move(shop.X+40, shop.Y+shop.H/2)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	grip, ok := tt.Find("Reorder project")
	if !ok {
		t.Fatalf("no grip in %q", tt.Texts())
	}
	x, y := grip.X+grip.W/2, grip.Y+grip.H/2
	tt.Move(x, y)
	tt.Frame()
	tt.Press(x, y)
	tt.Frame()
	step := shop.Y - docs.Y
	tt.Move(x, y-step)
	tt.Frame()
	tt.Frame()
	if d := a.groupDrag; !d.active || d.id != "g" || d.over != 0 {
		t.Fatalf("drag %+v", d)
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if moved, _ := tt.Find("Docs"); moved.Y <= docs.Y {
		t.Fatalf("Docs didn't slide aside: %v from %v", moved, docs)
	}
	if dragged, _ := tt.Find("Shop"); abs32(dragged.Y-docs.Y) > 2 {
		t.Fatalf("Shop isn't under the pointer: %v, %v", dragged, docs)
	}
	snapFrame(t, tt, "sidebar-group-drag")
	tt.Release(x, y-step)
	tt.Frame()
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if got := a.groupOrder(); !reflect.DeepEqual(got, []string{"g", "d"}) {
		t.Fatalf("order %q", got)
	}
	var kept map[string][]string
	if !readJSON(groupOrderFile, &kept) || !reflect.DeepEqual(kept["w1"], []string{"g", "d"}) {
		t.Fatalf("kept %v", kept)
	}
	docsNow, _ := tt.Find("Docs")
	shopNow, _ := tt.Find("Shop")
	if shopNow.Y >= docsNow.Y || shopNow.Y != docs.Y {
		t.Fatalf("after the drop: Shop %v, Docs %v", shopNow, docsNow)
	}
	// Another workspace keeps its own; one not named follows.
	a.workspaceID = "w2"
	a.catalog.Groups = append(a.catalog.Groups, catalog.ProjectGroup{ID: "e", Name: "Extra"})
	a.moveGroups([]string{"e"})
	a.workspaceID = "w1"
	var names []string
	for _, g := range a.orderedGroups() {
		names = append(names, g.ID)
	}
	if strings.Join(names, " ") != "g d e" {
		t.Fatalf("ordered %q", names)
	}
}

// In Chinese, the panel, the actions and the prompts say what Lody's
// zh_CN says.
func TestProjectRunChinese(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	defer i18n.SetLocaleFunc(func() string { return "en" })
	a := runFixture()
	a.catalog.Session("s1").WorkspaceDirty = true
	*a.popover("members/s1") = true
	tt := snap(t, a, "run-members-chinese", false, nil)
	wantTexts(t, tt, "3 个仓库 · 2 个 PR", "原目录 · 不隔离", "打开 PR #12", "提交并推送")
	if !strings.HasPrefix(a.infoPrompt(a.catalog.Session("s1"), "create-pr"), "（此消息由 Lody 的「**创建 PR**」按钮发送）\n\n为当前工作区创建一个正式 PR（非草稿）。") {
		t.Fatal("prompt")
	}
}
