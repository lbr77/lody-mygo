package app

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/i18n"

	"github.com/lbr77/lody-mygo/internal/catalog"
)

// shopGroup is a saved project of website, a folder of this machine, and
// egoist/mygo cloned from GitHub, as the workspace stream has it.
func shopGroup(owner string) catalog.ProjectGroup {
	row := map[string]any{"v": 1.0, "id": "g", "name": "Shop", "instructions": "web first", "ownerUserId": owner, "createdAtMs": 5.0, "revision": 2.0, "members": []any{
		map[string]any{"id": "ma", "source": map[string]any{"kind": "local", "machineId": "m1", "localProjectId": "p2"}, "placement": map[string]any{"kind": "root", "dir": "website"}},
		map[string]any{"id": "mb", "source": map[string]any{"kind": "github", "repoFullName": "egoist/mygo"}, "placement": map[string]any{"kind": "root", "dir": "mygo"}},
	}}
	return catalog.ProjectGroup{ID: "g", Name: "Shop", Instructions: "web first", OwnerUserID: owner, CreatedAt: 5, Revision: 2, Row: row,
		Members: []catalog.ProjectGroupMember{
			{ID: "ma", Kind: "local", MachineID: "m1", LocalProjectID: "p2", Dir: "website"},
			{ID: "mb", Kind: "github", RepoFullName: "egoist/mygo", Dir: "mygo"},
		}}
}

// A project's creator opens it from Settings › Projects in the wizard,
// which walks its questions again, its earlier answers marked
// "Previously selected", and saves it as its next revision, its creator
// and creation kept.
func TestProjectEditorEdit(t *testing.T) {
	a := wizardFixture()
	a.catalog.Groups = []catalog.ProjectGroup{shopGroup("u1")}
	tt := snap(t, a, "project-editor-settings", false, nil)
	wantTexts(t, tt, "Shop", "2 repositories or folders")

	wizardClick(t, tt, "Shop")
	snapFrame(t, tt, "project-editor-kind")
	if a.projectWizard.editing == nil || a.projectWizard.id != "g" {
		t.Fatalf("not editing: %+v", a.projectWizard)
	}
	wantTexts(t, tt, "What will this project contain?", "Step 1 of 4", "Previously selected")
	wizardClick(t, tt, "Includes local folders")
	wantTexts(t, tt, "Which machine are the folders on?", "Previously selected")
	wizardClick(t, tt, "Online · 2 folders")
	snapFrame(t, tt, "project-editor-sources")
	wantTexts(t, tt, "/src/website", "Previously selected", "2 selected")
	wizardClick(t, tt, "GitHub repositories")
	wantTexts(t, tt, "egoist/mygo", "Previously selected")
	wizardClick(t, tt, "Next")
	snapFrame(t, tt, "project-editor-details")
	wantTexts(t, tt, "Save", "Includes 2 items")
	if tt.HasText("Create") || a.projectWizard.draft.name != "Shop" {
		t.Fatalf("details %q, name %q", tt.Texts(), a.projectWizard.draft.name)
	}

	io := swapWizardIO(t)
	pump := pumpUpdates(t, a)
	a.projectWizard.draft.instructions = "proto first"
	wizardClick(t, tt, "Save")
	pump("the project saved", func() bool { return !a.projectWizard.open })
	row := io.row(t, "", "project", "g")
	members, _ := row["members"].([]any)
	if row["revision"] != 3.0 || row["ownerUserId"] != "u1" || row["createdAtMs"] != 5.0 || row["instructions"] != "proto first" || len(members) != 2 ||
		members[0].(map[string]any)["id"] != "ma" || members[1].(map[string]any)["id"] != "mb" {
		t.Fatalf("row %v", row)
	}

	// Saved unchanged, it is the row as it was, its revision unmoved.
	g := shopGroup("u1")
	if got := draftOf(g).row("g", "u1", 99, &g); got["revision"] != 2.0 || !reflect.DeepEqual(got, g.Row) {
		t.Fatalf("unchanged %v", got)
	}
}

// Anyone else sees a project as it is: its members where they sit in a
// conversation's directory, a submodule a step in under its parent, and
// its instructions; it has no menu to delete it.
func TestProjectEditorReadOnly(t *testing.T) {
	a := wizardFixture()
	g := shopGroup("u2")
	g.Row["members"] = append(g.Row["members"].([]any), map[string]any{"id": "mc", "source": map[string]any{"kind": "github", "repoFullName": "lbr77/lody-mygo"},
		"placement": map[string]any{"kind": "submodule", "parent": "mb", "path": "deps/lody"}})
	a.catalog.Groups = []catalog.ProjectGroup{g}
	tt := snap(t, a, "project-view-settings", false, nil)
	wizardClick(t, tt, "Shop")
	snapFrame(t, tt, "project-view")
	if a.projectWizard.open || !a.groupView.open {
		t.Fatal("the read-only view didn't open")
	}
	wantTexts(t, tt, "Only the person who created this project can change it.", "Folders and repositories", "website", "/src/website",
		"mygo", "mygo/deps/lody", "lbr77/lody-mygo", "Instructions", "web first", "Close")
	parent, _ := tt.Find("egoist/mygo")
	child, _ := tt.Find("lbr77/lody-mygo")
	if child.X <= parent.X {
		t.Fatalf("the submodule isn't in under its parent: %v, %v", parent, child)
	}
	wizardClick(t, tt, "Close")
	if a.groupView.open {
		t.Fatal("Close")
	}
	// Only its creator deletes it; anyone else asking is a no-op.
	a.requestProjectDelete("g")
	if a.groupDelete.open {
		t.Fatal("a teammate can delete it")
	}
}

// Its creator deletes it from its row's menu, after a question; its row
// goes from the workspace's stream.
func TestProjectEditorDelete(t *testing.T) {
	a := wizardFixture()
	a.catalog.Groups = []catalog.ProjectGroup{shopGroup("u1")}
	io := swapWizardIO(t)
	if err := io.stream(t, "").Put([]any{"project", "g"}, shopGroup("u1").Row); err != nil {
		t.Fatal(err)
	}
	pump := pumpUpdates(t, a)
	a.rowMenu("group/g").open = true
	tt := snap(t, a, "project-delete-menu", false, nil)
	wizardClick(t, tt, "Delete project")
	snapFrame(t, tt, "project-delete")
	wantTexts(t, tt, "Delete project?", "“Shop” will be deleted. Existing conversations keep their repositories and branches; the folders and repositories themselves are untouched.", "Cancel", "Remove")
	wizardClick(t, tt, "Remove")
	pump("deleted", func() bool { return !a.groupDelete.open })
	if _, ok, _ := io.stream(t, "").Get([]any{"project", "g"}); ok {
		t.Fatal("the row is still there")
	}
}

// sidebarFixture is the wizard's workspace with the sidebar shown and
// Settings closed.
func sidebarFixture() *App {
	a := wizardFixture()
	a.settingsOpen, a.sidebarHidden = false, false
	return a
}

// A folder's row in the sidebar has Lody's menu; Edit project… starts a
// project of several from it, named after it, on its folders.
func TestSidebarProjectMenu(t *testing.T) {
	a := sidebarFixture()
	tt := snap(t, a, "sidebar-project-menu", false, nil)
	if err := tt.RightClick("website"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "sidebar-project-menu-open")
	wantTexts(t, tt, "Edit project…", "Project settings", "Reveal in file manager", "Archive chats", "Remove project")
	wizardClick(t, tt, "Edit project…")
	wz := a.projectWizard
	if !wz.open || wz.step != "sources" || wz.draft.name != "website" || len(wz.draft.members) != 1 || wz.draft.members[0].source.key() != "local:m1:p2" {
		t.Fatalf("wizard %+v", wz)
	}
	wantTexts(t, tt, "Choose folders and repositories", "Only one selected: it is added as an ordinary project.")

	// A folder with a GitHub remote starts as its repository, from this
	// machine's copy.
	a.openProjectWizardFrom("local:m1:p1", "lody-mygo", nil)
	if m := a.projectWizard.draft.members[0]; m.source.key() != "github:lbr77/lody-mygo" || !m.source.preferLocal || a.projectWizard.scope.machine != "m1" {
		t.Fatalf("seed %+v, scope %+v", m, a.projectWizard.scope)
	}
}

// A folder registered only for projects is listed still, as Lody's
// sidebar lists it, with its own conversations; the project of several
// bound to its machine is, after its folders, with its conversations, and
// has its own menu.
func TestSidebarProjectGroups(t *testing.T) {
	a := sidebarFixture()
	a.catalog.Projects[1].MemberOnly = true // website
	a.catalog.Groups = []catalog.ProjectGroup{shopGroup("u1")}
	a.catalog.Sessions = append(a.catalog.Sessions,
		catalog.Session{ID: "s9", MachineID: "m1", Title: "Ship the shop", Status: "idle", ProjectID: "m1:local:p2", ProjectGroupID: "g", ProjectGroupName: "Shop"},
		catalog.Session{ID: "s10", MachineID: "m1", Title: "Old shop work", Status: "idle", ProjectID: "github:acme/old", ProjectGroupID: "gone", ProjectGroupName: "Old shop"})
	tt := snap(t, a, "sidebar-project-groups", false, nil)
	wantTexts(t, tt, "website", "Shop", "Ship the shop", "Old shop", "Old shop work", "Fix the flaky upload test")
	lody, _ := tt.Find("lody-mygo")
	shop, _ := tt.Find("Shop")
	if shop.Y <= lody.Y {
		t.Fatalf("the project isn't after the folders: %v, %v", lody, shop)
	}
	if err := tt.RightClick("Shop"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "sidebar-project-group-menu")
	wantTexts(t, tt, "Edit project…", "Archive chats", "Remove project")
	wizardClick(t, tt, "Edit project…")
	if a.projectWizard.editing == nil || a.projectWizard.editing.ID != "g" {
		t.Fatal("Edit project… doesn't edit it")
	}
	a.closeProjectWizard()

	// Its New chat starts there.
	a.newChatInGroup("g")
	if a.newChat.project != "group:g" || a.newChat.machine != "m1" || a.selected != "" {
		t.Fatalf("new chat %+v", a.newChat)
	}
}

// New chat's project picker offers the projects of several this machine
// can take: what each holds and lacks, or why it cannot start, and
// Create project, which chooses what it creates.
func TestHomeProjectGroups(t *testing.T) {
	a := fixture()
	a.term.machine = "m1"
	a.catalog.MachineIDs = []string{"m1", "m2"}
	a.catalog.Projects = append(a.catalog.Projects, catalog.Project{ID: "m1:local:p5", MachineID: "m1", Name: "copy", RepoFullName: "acme/web"},
		catalog.Project{ID: "m1:local:p6", MachineID: "m1", Name: "hidden-folder", MemberOnly: true})
	a.catalog.Groups = []catalog.ProjectGroup{
		shopGroup("u1"),
		{ID: "g2", Name: "Docs", OwnerUserID: "u1", Members: []catalog.ProjectGroupMember{{ID: "x", Kind: "local", MachineID: "m2", LocalProjectID: "p3"}}},
		{ID: "g3", Name: "Both", OwnerUserID: "u1", Members: []catalog.ProjectGroupMember{{ID: "x", Kind: "local", MachineID: "m1", LocalProjectID: "p1"}, {ID: "y", Kind: "local", MachineID: "m2", LocalProjectID: "p3"}}},
		{ID: "g4", Name: "Clones", OwnerUserID: "u1", Members: []catalog.ProjectGroupMember{{ID: "x", Kind: "github", RepoFullName: "acme/web", PreferLocal: true}, {ID: "y", Kind: "github", RepoFullName: "acme/api", PreferLocal: true}}},
	}
	a.selected, a.sidebarHidden = "", true
	a.newChat.machine = "m1"
	a.pickNewChatDefaults()
	*a.popover("project") = true
	tt := snap(t, a, "home-project-groups", false, nil)
	wantTexts(t, tt, "Shop", "2 repositories", "Both", "Its local repositories are on different machines", "Clones", "2 repositories · 1 cloned on start", "Create project", "website")
	if tt.HasText("Docs") || tt.HasText("hidden-folder") {
		t.Fatalf("offered what this machine can't take: %q", tt.Texts())
	}
	wizardClick(t, tt, "Shop")
	if a.newChat.project != "group:g" || *a.popover("project") {
		t.Fatalf("chose %q", a.newChat.project)
	}
	wantTexts(t, tt, "Shop", "Update Lody on this machine to start conversations in multi-repository projects.")
	a.catalog.MachineCaps = map[string]map[string]float64{"m1": {"multiRepoProject": 1, "projectRepositoryMembers": 1}}
	tt.Frame()
	if tt.HasText("Update Lody on this machine to start conversations in multi-repository projects.") || a.groupBlocked() {
		t.Fatal("still blocked")
	}

	// Folders on two machines never start; nor, without GitHub, does a
	// repository this machine has no copy of.
	a.newChat.project = "group:g3"
	tt.Frame()
	wantTexts(t, tt, "This project has local folders on different machines, so they cannot share one conversation.")
	if !a.groupBlocked() {
		t.Fatal("a conflict starts")
	}
	a.newChat.project, a.localOnly = "group:g4", true
	tt.Frame()
	wantTexts(t, tt, "This machine has no copy of 1 of this project’s repositories. Add them as local projects here, or start on another machine.")
	a.localOnly = false
	a.newChat.project = "group:gone"
	tt.Frame()
	wantTexts(t, tt, "Removed project", "This project no longer exists")

	// Create project opens the wizard, and what it creates is chosen.
	*a.popover("project") = true
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	wizardClick(t, tt, "Create project")
	if !a.projectWizard.open || a.projectWizard.onCreated == nil {
		t.Fatal("Create project")
	}
	a.projectWizard.onCreated("g4")
	if a.newChat.project != "group:g4" {
		t.Fatalf("created %q", a.newChat.project)
	}
}

// A conversation in a project of several starts with its first member as
// its project and, with two or more, Lody's run snapshot: each member at
// its path, with the base its worktree starts from.
func TestGroupLaunch(t *testing.T) {
	g := catalog.ProjectGroup{ID: "g1", Name: "Shop & Co", Instructions: "web first", Row: map[string]any{"members": []any{
		map[string]any{"id": "a", "source": map[string]any{"kind": "local", "machineId": "m1", "localProjectId": "p2", "baseBranch": "main"}, "placement": map[string]any{"kind": "root", "dir": "website"}},
		map[string]any{"id": "b", "source": map[string]any{"kind": "github", "repoFullName": "acme/proto", "repoId": 7.0}, "placement": map[string]any{"kind": "submodule", "parent": "a", "path": "vendor/proto"}},
		map[string]any{"id": "c", "source": map[string]any{"kind": "github", "repoFullName": "egoist/mygo", "preferLocal": true}, "placement": map[string]any{"kind": "root", "dir": "mygo"}},
	}}}
	project, run, repo, base, err := groupLaunch(g)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(project, map[string]any{"kind": "local", "localProjectId": "p2", "useWorktree": true, "branch": "main"}) || repo != "acme/proto" || base != "" {
		t.Fatalf("project %v, repo %q, base %q", project, repo, base)
	}
	want := map[string]any{"id": "g1", "name": "Shop & Co", "instructions": "web first", "root": "project-shop-co-g1", "members": map[string]any{
		"a": map[string]any{"o": 0, "src": map[string]any{"kind": "local", "machineId": "m1", "localProjectId": "p2", "baseBranch": "main"}, "placement": map[string]any{"kind": "root", "dir": "website"}, "path": "website", "base": "main"},
		"b": map[string]any{"o": 1, "src": map[string]any{"kind": "github", "repoFullName": "acme/proto", "repoId": 7.0}, "placement": map[string]any{"kind": "submodule", "parent": "a", "path": "vendor/proto"}, "path": "website/vendor/proto", "base": "gitlink"},
		"c": map[string]any{"o": 2, "src": map[string]any{"kind": "github", "repoFullName": "egoist/mygo", "preferLocal": true}, "placement": map[string]any{"kind": "root", "dir": "mygo"}, "path": "mygo", "base": "HEAD"},
	}}
	if !reflect.DeepEqual(run, want) {
		t.Fatalf("run %v\nwant %v", run, want)
	}

	// One member is an ordinary project, with no run.
	g.Row["members"] = g.Row["members"].([]any)[2:]
	project, run, repo, base, _ = groupLaunch(g)
	if run != nil || repo != "egoist/mygo" || base != "HEAD" || !reflect.DeepEqual(project, map[string]any{"kind": "github", "repoFullName": "egoist/mygo", "branch": "HEAD"}) {
		t.Fatalf("single %v %v %q %q", project, run, repo, base)
	}
}

// A repository another member's .gitmodules declares moves inside it as
// it is read, as Lody's detection does; removing the parent brings it
// back to the top.
func TestProjectSubmoduleDetection(t *testing.T) {
	a := wizardFixture()
	var mu sync.Mutex
	var asked []string
	read := groupProbeRead
	t.Cleanup(func() { groupProbeRead = read })
	groupProbeRead = func(_ context.Context, _ probeEnv, s groupSource) (*probeResult, error) {
		mu.Lock()
		asked = append(asked, s.key())
		mu.Unlock()
		if s.repo == "egoist/mygo" {
			return &probeResult{repo: s.repo, submodules: []detectedSubmodule{{path: "deps/lody", repo: "lbr77/lody-mygo"}}}, nil
		}
		return &probeResult{repo: s.repo}, nil
	}
	pump := pumpUpdates(t, a)
	a.openProjectWizard()
	a.chooseGroupScope(groupScope{kind: "github"})
	a.projectWizard.draft = a.projectWizard.draft.add(githubSource("egoist/mygo"), "egoist/mygo").add(githubSource("lbr77/lody-mygo"), "lbr77/lody-mygo")
	tt := snap(t, a, "project-submodules", false, nil)
	pump("detected", func() bool {
		tt.Frame()
		return len(a.projectWizard.draft.members) == 2 && a.projectWizard.draft.members[1].submodule()
	})
	parent, child := a.projectWizard.draft.members[0], a.projectWizard.draft.members[1]
	if child.parent != parent.id || child.path != "deps/lody" || child.origin != "detected" || len(asked) != 2 {
		t.Fatalf("child %+v, asked %q", child, asked)
	}
	if v := a.projectWizard.draft.validate(nil, nil); !v.nameRequired || len(v.memberErrors) != 0 {
		t.Fatalf("validation %+v", v)
	}
	if !reflect.DeepEqual(a.projectWizard.draft.membersRow()[1].(map[string]any)["placement"], map[string]any{"kind": "submodule", "parent": parent.id, "path": "deps/lody"}) {
		t.Fatalf("row %v", a.projectWizard.draft.membersRow())
	}
	d := a.projectWizard.draft.remove(parent.id, nil)
	if m := d.members[0]; m.submodule() || m.dir != "lody-mygo" {
		t.Fatalf("re-rooted %+v", m)
	}
	// A placement the user saved never moves.
	saved := a.projectWizard.draft
	saved.members[1].parent, saved.members[1].path, saved.members[1].dir, saved.members[1].origin = "", "", "lody", "user"
	if got := saved.applySubmodules(map[string][]detectedSubmodule{parent.id: {{path: "deps/lody", repo: "lbr77/lody-mygo"}}},
		func(m groupMember) string { return m.source.repo }, nil); got.members[1].submodule() {
		t.Fatal("a user's placement moved")
	}

	// .gitmodules as Git writes it, and the URLs a submodule may have.
	mods := parseGitmodules("[submodule \"proto\"]\n\tpath = vendor/proto/\n\turl = ../proto.git ; relative\n[submodule \"x\"]\n\tpath = x\n[core]\n\turl = nope\n")
	if len(mods) != 1 || mods[0].path != "vendor/proto" || mods[0].url != "../proto.git" {
		t.Fatalf("gitmodules %+v", mods)
	}
	for url, want := range map[string]string{
		"../proto.git": "acme/proto", "./x": "", "git@github.com:a/b.git": "a/b", "https://user@github.com/a/b": "a/b",
		"ssh://git@github.com:22/a/b.git": "a/b", "https://gitlab.com/a/b": "", "../../other/repo": "other/repo",
	} {
		if got := submoduleRepo(url, "acme/web"); got != want {
			t.Errorf("submoduleRepo(%q) = %q, want %q", url, got, want)
		}
	}
	// Paths overlapping, or a cycle, are the contract's to refuse.
	cycle := []groupMember{{id: "x", parent: "y", path: "a"}, {id: "y", parent: "x", path: "b"}}
	if _, err := memberPaths(cycle); err == nil {
		t.Fatal("a cycle resolves")
	}
	if _, err := memberPaths([]groupMember{{id: "x", dir: "web"}, {id: "y", dir: "WEB"}}); err == nil {
		t.Fatal("one directory twice resolves")
	}
}

// In Chinese, what the editor adds says what Lody's zh_CN says.
func TestProjectEditorChinese(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	defer i18n.SetLocaleFunc(func() string { return "en" })
	a := wizardFixture()
	a.catalog.Groups = []catalog.ProjectGroup{shopGroup("u2")}
	a.requestProjectEdit("g")
	tt := snap(t, a, "project-view-chinese", false, nil)
	wantTexts(t, tt, "只有创建者可以修改这个项目。", "文件夹与仓库", "说明", "关闭")
	a.groupView.open = false
	a.catalog.Groups[0].OwnerUserID = "u1"
	a.catalog.Groups[0].Row["ownerUserId"] = "u1"
	a.requestProjectEdit("g")
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	wantTexts(t, tt, "上次选择")
	if !strings.Contains(strings.Join(tt.Texts(), "|"), "这个项目包含什么？") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

// Home's project list puts the last used first, as Lody's picker: by each
// one's newest conversation, those used before those never, then by name.
func TestPickerRecency(t *testing.T) {
	a := fixture()
	a.newChat.machine = "m1"
	a.catalog.Groups = []catalog.ProjectGroup{shopGroup("u1")}
	a.catalog.Sessions = append(a.catalog.Sessions,
		catalog.Session{ID: "w", MachineID: "m1", Title: "Web", ProjectID: "m1:local:p2", LastMessageAt: 30},
		catalog.Session{ID: "g1", MachineID: "m1", Title: "Shop work", ProjectID: "m1:local:p2", ProjectGroupID: "g", LastMessageAt: 20},
		catalog.Session{ID: "gone", MachineID: "m1", Title: "Archived", ProjectID: "m1:local:p1", Archived: true, LastMessageAt: 99})
	var got []string
	for _, e := range a.pickerEntries(a.projectOptions()[1:]) {
		got = append(got, e.opt.id)
	}
	if len(got) < 3 || got[0] != "m1:local:p2" || got[1] != "group:g" || got[2] != "m1:local:p1" {
		t.Fatalf("order %q", got)
	}
}
