package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/internal/presence"
)

// wizardFixture is Settings › Projects with two machines, this one online
// with a copy of a GitHub repository and a plain folder, the other offline
// with a folder; and the workspace's two GitHub repositories.
func wizardFixture() *App {
	a := fixture()
	a.settingsOpen, a.settingsTab = true, "projects"
	a.term.machine, a.term.checked = "m1", time.Now()
	// The sidebar's own Back and Search are not the wizard's.
	a.sidebarHidden = true
	a.catalog.MachineIDs = []string{"m1", "m2"}
	a.catalog.MachineNames = map[string]string{"m1": "Studio", "m2": "Laptop"}
	a.catalog.Projects = []catalog.Project{
		{ID: "m1:local:p1", MachineID: "m1", Name: "lody-mygo", RootPath: "/src/lody-mygo", RepoFullName: "lbr77/lody-mygo", OpenedAt: 2},
		{ID: "m1:local:p2", MachineID: "m1", Name: "website", RootPath: "/src/website", OpenedAt: 1},
		{ID: "m2:local:p3", MachineID: "m2", Name: "notes", RootPath: "/home/ada/notes"},
	}
	a.presence = presence.State{Live: true, Online: []string{"m1"}}
	a.convex = &cloud.Convex{}
	a.fetches = map[string]*fetched{"w1|github-repos": {value: []workspaceRepository{{ID: 1, Name: "lody-mygo", FullName: "lbr77/lody-mygo"}, {ID: 2, Name: "mygo", FullName: "egoist/mygo"}}}}
	return a
}

// fakeWizardIO stands in for the machines' daemons and the streams the
// wizard writes: each stream a flock of its own, "" the workspace's, and
// prepare-add answered from what the test says each folder is.
type fakeWizardIO struct {
	mu      sync.Mutex
	streams map[string]*flock.Flock
	folders map[string]string // prepare-add's result, by root path
	asked   []string          // machine and request type, in order
}

func swapWizardIO(t *testing.T) *fakeWizardIO {
	t.Helper()
	io := &fakeWizardIO{streams: map[string]*flock.Flock{}, folders: map[string]string{}}
	control, write := wizardControl, wizardWrite
	t.Cleanup(func() { wizardControl, wizardWrite = control, write })
	wizardControl = func(_ *cloud.Conn, _ context.Context, machine string, request map[string]any) (json.RawMessage, error) {
		io.mu.Lock()
		defer io.mu.Unlock()
		io.asked = append(io.asked, machine+" "+request["type"].(string))
		if request["type"] != "local-project/prepare-add" {
			return nil, errors.New("unexpected request")
		}
		reply, ok := io.folders[request["rootPath"].(string)]
		if !ok {
			return nil, errors.New("no such folder")
		}
		return json.RawMessage(reply), nil
	}
	wizardWrite = func(_ *catalog.Sync, _ context.Context, machine string, edit func(f *flock.Flock) error) error {
		f := io.stream(t, machine)
		io.mu.Lock()
		defer io.mu.Unlock()
		return edit(f)
	}
	return io
}

func (io *fakeWizardIO) stream(t *testing.T, machine string) *flock.Flock {
	io.mu.Lock()
	defer io.mu.Unlock()
	f := io.streams[machine]
	if f == nil {
		var err error
		if f, err = flock.New("test-" + machine); err != nil {
			t.Fatal(err)
		}
		io.streams[machine] = f
	}
	return f
}

func (io *fakeWizardIO) row(t *testing.T, machine string, key ...any) map[string]any {
	t.Helper()
	v, _, err := io.stream(t, machine).Get(key)
	if err != nil {
		t.Fatal(err)
	}
	row, _ := v.(map[string]any)
	return row
}

// wizardClick clicks what shows s, and lets the frame it brings settle.
func wizardClick(t *testing.T, tt *ui.Tester, s string) {
	t.Helper()
	if err := tt.Click(s); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
}

func wantTexts(t *testing.T, tt *ui.Tester, want ...string) {
	t.Helper()
	for _, s := range want {
		if !tt.HasText(s) {
			t.Fatalf("no %q in %q", s, tt.Texts())
		}
	}
}

// Settings › Projects' add menu opens the wizard, which asks, one question
// at a time, what the project holds, which machine, which folders and
// repositories, then its name; a repository ticked both as a folder and
// on GitHub is one clone, as Lody's.
func TestProjectWizard(t *testing.T) {
	a := wizardFixture()
	a.catalog.Groups = []catalog.ProjectGroup{{ID: "g0", Name: "website", OwnerUserID: "u1", Members: []catalog.ProjectGroupMember{{ID: "x", Kind: "github", RepoFullName: "egoist/mygo"}}}}
	tt := snap(t, a, "project-groups", false, nil)
	wantTexts(t, tt, "Any machine", "Projects of several repositories, run wherever a copy is", "1 repository or folder")

	wizardClick(t, tt, "Add project")
	wantTexts(t, tt, "Add a folder", "Browse the machine and pick a folder", "Add a GitHub repository", "Connect a GitHub repository",
		"Several repositories or folders", "One conversation works across all of them")
	snapFrame(t, tt, "project-add-menu")
	wizardClick(t, tt, "Several repositories or folders")
	snapFrame(t, tt, "project-wizard-kind")
	wantTexts(t, tt, "What will this project contain?", "Step 1 of 4", "Includes local folders", "GitHub repositories only", "Cancel")

	wizardClick(t, tt, "Includes local folders")
	snapFrame(t, tt, "project-wizard-machine")
	wantTexts(t, tt, "Which machine are the folders on?", "Step 2 of 4", "This machine", "Online · 2 folders", "Offline · 1 folder", "Back")
	// This machine first, then by whether it is online.
	if studio, _ := tt.Find("Online · 2 folders"); true {
		if laptop, _ := tt.Find("Offline · 1 folder"); laptop.Y <= studio.Y {
			t.Fatalf("this machine isn't first: %v, %v", studio, laptop)
		}
	}

	wizardClick(t, tt, "Online · 2 folders")
	wantTexts(t, tt, "Choose folders and repositories", "Tick what this project needs on This machine.", "Step 3 of 4",
		"Folders on This machine", "GitHub repositories", "lbr77/lody-mygo · /src/lody-mygo", "/src/website", "0 selected",
		"Folders with a GitHub remote use a worktree of your copy.")
	wizardClick(t, tt, "Next")
	if a.projectWizard.step != "sources" {
		t.Fatal("Next goes on with nothing ticked")
	}

	// One ticked finishes there; two make a project.
	wizardClick(t, tt, "lbr77/lody-mygo · /src/lody-mygo")
	wantTexts(t, tt, "Only one selected: it is added as an ordinary project.", "Done")
	wizardClick(t, tt, "/src/website")
	wantTexts(t, tt, "2 selected", "Next")

	// The same repository ticked on GitHub: GitHub wins, the folder noted.
	wizardClick(t, tt, "GitHub repositories")
	wantTexts(t, tt, "egoist/mygo", "Repositories ticked here are cloned for each conversation.")
	wizardClick(t, tt, "lbr77/lody-mygo")
	wantTexts(t, tt, "lbr77/lody-mygo is selected both as a folder and as a GitHub repository.", "2 selected")
	snapFrame(t, tt, "project-wizard-sources")
	var sources []string
	for _, m := range a.projectWizard.draft.members {
		sources = append(sources, m.source.key()+" "+m.usage())
	}
	if want := []string{"local:m1:p2 inPlace", "github:lbr77/lody-mygo worktree"}; !reflect.DeepEqual(sources, want) || !a.projectWizard.alsoFolder["local:m1:p1"] {
		t.Fatalf("members %q, also %v", sources, a.projectWizard.alsoFolder)
	}

	// The name comes from the first member, past the names taken.
	wizardClick(t, tt, "Next")
	snapFrame(t, tt, "project-wizard-details")
	wantTexts(t, tt, "Name the project", "Step 4 of 4", "Includes 2 items", "Instructions for the agent (optional)",
		"Sent to the agent at the start of every new conversation in this project.", "Create",
		"lbr77/lody-mygo is selected both as a folder and as a GitHub repository.")
	if a.projectWizard.draft.name != "website 2" {
		t.Fatalf("name %q", a.projectWizard.draft.name)
	}

	// Back keeps every answer.
	wizardClick(t, tt, "Back")
	wantTexts(t, tt, "Choose folders and repositories", "2 selected")
	wizardClick(t, tt, "Next")

	io := swapWizardIO(t)
	pump := pumpUpdates(t, a)
	a.projectWizard.draft.instructions = "  web is the frontend  "
	wizardClick(t, tt, "Create")
	pump("the project saved", func() bool { return !a.projectWizard.open })
	row := io.row(t, "", "project", a.projectWizard.id)
	members, _ := row["members"].([]any)
	if row["name"] != "website 2" || row["ownerUserId"] != "u1" || row["v"] != 1.0 || row["revision"] != 1.0 || row["instructions"] != "web is the frontend" || len(members) != 2 {
		t.Fatalf("row %v", row)
	}
	first, second := members[0].(map[string]any), members[1].(map[string]any)
	if !reflect.DeepEqual(first["source"], map[string]any{"kind": "local", "machineId": "m1", "localProjectId": "p2"}) ||
		!reflect.DeepEqual(first["placement"], map[string]any{"kind": "root", "dir": "website"}) ||
		!reflect.DeepEqual(second["source"], map[string]any{"kind": "github", "repoFullName": "lbr77/lody-mygo"}) ||
		!reflect.DeepEqual(second["placement"], map[string]any{"kind": "root", "dir": "lody-mygo"}) {
		t.Fatalf("members %v", members)
	}
	if len(io.asked) != 0 {
		t.Fatalf("asked the machine %q with no folder picked", io.asked)
	}
}

// For GitHub only there is no machine to choose; the list searches, and a
// lone repository finishes as an ordinary project, which needs nothing.
func TestProjectWizardGitHubOnly(t *testing.T) {
	a := wizardFixture()
	io := swapWizardIO(t)
	a.openProjectWizard()
	tt := snap(t, a, "project-wizard-open", true, nil)
	wizardClick(t, tt, "GitHub repositories only")
	wantTexts(t, tt, "Choose GitHub repositories", "Step 2 of 3", "egoist/mygo", "lbr77/lody-mygo")
	if a.projectWizard.scope.kind != "github" {
		t.Fatalf("scope %+v", a.projectWizard.scope)
	}
	a.projectWizard.query = "EGO"
	tt.Frame()
	if !tt.HasText("egoist/mygo") || tt.HasText("lbr77/lody-mygo") {
		t.Fatalf("search: %q", tt.Texts())
	}
	a.projectWizard.query = "nothing"
	tt.Frame()
	wantTexts(t, tt, "Nothing matches")
	a.projectWizard.query = ""
	tt.Frame()
	wizardClick(t, tt, "egoist/mygo")
	snapFrame(t, tt, "project-wizard-github-dark")
	wizardClick(t, tt, "Done")
	if a.projectWizard.open || len(io.asked) != 0 || len(io.streams) != 0 {
		t.Fatalf("open %v, asked %q", a.projectWizard.open, io.asked)
	}
	wantTexts(t, tt, "“egoist/mygo” is already available as a project")

	// Going back to it, the kind is GitHub's and the machine step is skipped.
	a.openProjectWizard()
	tt.Frame()
	wizardClick(t, tt, "GitHub repositories only")
	wizardClick(t, tt, "Back")
	wantTexts(t, tt, "What will this project contain?", "Step 1 of 3")
}

// A folder only projects held, finished alone, is listed again.
func TestProjectWizardSingleFolder(t *testing.T) {
	a := wizardFixture()
	io := swapWizardIO(t)
	if err := io.stream(t, "m1").Put([]any{"localProject", "p2"}, map[string]any{"id": "p2", "name": "website", "rootPath": "/src/website", "standaloneVisible": false}); err != nil {
		t.Fatal(err)
	}
	pump := pumpUpdates(t, a)
	a.openProjectWizard()
	a.chooseGroupScope(groupScope{kind: "machine", machine: "m1"})
	tt := snap(t, a, "project-wizard-single", false, nil)
	wizardClick(t, tt, "/src/website")
	wizardClick(t, tt, "Done")
	pump("finished", func() bool { return !a.projectWizard.open })
	if row := io.row(t, "m1", "localProject", "p2"); row["standaloneVisible"] != true || row["name"] != "website" {
		t.Fatalf("row %v", row)
	}
	tt.Frame()
	wantTexts(t, tt, "“website” is in your projects")
}

// A folder browsed on the machine is a draft until the project is saved,
// which has the machine prepare it again and registers it, hidden from the
// single projects; a folder that changed meanwhile stops the save.
func TestProjectWizardBrowse(t *testing.T) {
	a := wizardFixture()
	a.catalog.MachineCaps = map[string]map[string]float64{"m1": {"projectFolderSelection": 1}}
	io := swapWizardIO(t)
	api := `{"localProjectId":"p9","name":"api","rootPath":"/src/api","alreadyRegistered":false,"inspection":{"isGit":true,"repoFullName":"lbr77/api","submodules":[]}}`
	io.folders["/src/api"] = api
	pump := pumpUpdates(t, a)
	a.openProjectWizard()
	a.chooseGroupScope(groupScope{kind: "machine", machine: "m1"})
	tt := snap(t, a, "project-wizard-browse", false, nil)
	wantTexts(t, tt, "Browse…")

	// As if browsed to the folder: the browser picks for the wizard.
	a.addProject = addProjectForm{open: true, machine: "m1", path: "/src/api", selectOnly: true}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	wantTexts(t, tt, "Select local folders", "Select this folder")
	snapFrame(t, tt, "project-wizard-browser")
	wizardClick(t, tt, "Select this folder")
	pump("the folder picked", func() bool { return !a.addProject.open })
	tt.Frame()
	wantTexts(t, tt, "/src/api", "Only one selected: it is added as an ordinary project.")
	if m := a.projectWizard.draft.members[0]; m.folder == nil || m.source.key() != "github:lbr77/api" || !m.source.preferLocal {
		t.Fatalf("picked %+v", m)
	}
	if len(io.streams) != 0 {
		t.Fatal("a picked folder is registered before the project is saved")
	}
	wizardClick(t, tt, "/src/website")
	wizardClick(t, tt, "Next")
	if a.projectWizard.draft.name != "api" {
		t.Fatalf("name %q", a.projectWizard.draft.name)
	}
	// Bound to the machine it was picked on, the folder is its folder.
	if m := a.projectWizard.draft.members[0]; m.source.key() != "local:m1:p9" || m.usage() != "inPlace" {
		t.Fatalf("bound %+v", m)
	}

	io.folders["/src/api"] = strings.Replace(api, "p9", "p10", 1)
	wizardClick(t, tt, "Create")
	pump("the save stopped", func() bool { return !a.projectWizard.submitting })
	tt.Frame()
	wantTexts(t, tt, "The selected folder changed. Remove it and select it again.")
	if !a.projectWizard.open || len(io.streams) != 0 {
		t.Fatal("a changed folder saved")
	}

	io.folders["/src/api"] = api
	wizardClick(t, tt, "Create")
	pump("the project saved", func() bool { return !a.projectWizard.open })
	local := io.row(t, "m1", "localProject", "p9")
	if local["standaloneVisible"] != false || local["githubRepoFullName"] != "lbr77/api" || local["rootPath"] != "/src/api" {
		t.Fatalf("folder %v", local)
	}
	row := io.row(t, "", "project", a.projectWizard.id)
	members, _ := row["members"].([]any)
	if len(members) != 2 || !reflect.DeepEqual(members[0].(map[string]any)["source"], map[string]any{"kind": "local", "machineId": "m1", "localProjectId": "p9"}) {
		t.Fatalf("row %v", row)
	}
}

// The step bar's fill grows to the next step over 180ms; a choice sinks a
// pixel while pressed, over 120ms, and lifts back.
func TestProjectWizardMotion(t *testing.T) {
	a := wizardFixture()
	a.openProjectWizard()
	tt := snap(t, a, "project-wizard-motion", false, nil)
	// The accent's pixels along the bar, which its step names.
	fill := func(label string) int {
		t.Helper()
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("no %q in %q", label, tt.Texts())
		}
		img, y, n := tt.Image(), int(r.Y+r.H/2), 0
		for x := int(r.X); x < int(r.X+r.W); x++ {
			if c := img.RGBAAt(x, y); int(c.B)-int(c.R) > 80 {
				n++
			}
		}
		return n
	}
	start := fill("Step 1 of 4")
	if start == 0 {
		t.Fatal("no fill at the first step")
	}
	if err := tt.Click("Includes local folders"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(60 * time.Millisecond)
	tt.Frame()
	going := fill("Step 2 of 4")
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	end := fill("Step 2 of 4")
	if end < start*2-4 || end > start*2+4 {
		t.Fatalf("half full at the second step: %d, a quarter %d", end, start)
	}
	if going <= start || going >= end {
		t.Fatalf("the fill doesn't grow: %d on the way from %d to %d", going, start, end)
	}

	wizardClick(t, tt, "Back")
	rest, _ := tt.Find("Includes local folders")
	tt.Press(rest.X+rest.W/2, rest.Y+rest.H/2)
	tt.Frame()
	tt.Advance(40 * time.Millisecond)
	tt.Frame()
	sinking, _ := tt.Find("Includes local folders")
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	down, _ := tt.Find("Includes local folders")
	if sinking.Y <= rest.Y || sinking.Y >= rest.Y+1 || down.Y != rest.Y+1 {
		t.Fatalf("the choice doesn't sink a pixel: %v, %v, %v", rest.Y, sinking.Y, down.Y)
	}
	tt.Release(rest.X+rest.W/2, rest.Y+rest.H/2)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if a.projectWizard.step != "machine" {
		t.Fatalf("the press didn't answer: %q", a.projectWizard.step)
	}
}

// In Chinese, the wizard says what Lody's zh_CN says.
func TestProjectWizardChinese(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	defer i18n.SetLocaleFunc(func() string { return "en" })
	a := wizardFixture()
	a.openProjectWizard()
	a.chooseGroupScope(groupScope{kind: "machine", machine: "m1"})
	tt := snap(t, a, "project-wizard-chinese", false, nil)
	wantTexts(t, tt, "选择文件夹和仓库", "勾选这个项目在本机上需要的文件夹", "第 3 步，共 4 步", "本机上的文件夹", "GitHub 仓库", "已选 0 项", "上一步", "下一步")
}

// With no machine, only GitHub can be chosen.
func TestProjectWizardNoMachine(t *testing.T) {
	a := wizardFixture()
	a.catalog.MachineIDs = nil
	a.openProjectWizard()
	tt := snap(t, a, "project-wizard-no-machine", false, nil)
	wantTexts(t, tt, "Connect a machine first to use its folders.")
	wizardClick(t, tt, "Includes local folders")
	if a.projectWizard.step != "kind" {
		t.Fatalf("a disabled choice answered: %q", a.projectWizard.step)
	}
}

// The draft's rules, as Lody's project-group-form.
func TestProjectGroupDraft(t *testing.T) {
	for in, want := range map[string]string{"lbr77/lody-mygo": "lody-mygo", "a/b:c": "b-c", "..hidden. ": "hidden", "con": "repo", "": "repo"} {
		if got := suggestMemberDir(in); got != want {
			t.Errorf("suggestMemberDir(%q) = %q, want %q", in, got, want)
		}
	}
	if !safeProjectPath("web/app") || safeProjectPath("../x") || safeProjectPath("a.") || safeProjectPath(".git") || safeProjectPath("a:b") {
		t.Error("safeProjectPath")
	}
	var d groupDraft
	d = d.add(githubSource("acme/web"), "acme/web")
	d = d.add(githubSource("other/web"), "other/web")
	d = d.add(githubSource("ACME/web"), "ACME/web") // the same repository
	if len(d.members) != 2 || d.members[1].dir != "web-2" {
		t.Fatalf("members %+v", d.members)
	}
	d = d.add(localSource("m1", "p1"), "notes").add(localSource("m2", "p2"), "notes")
	d.members[3].dir = "web"
	v := d.validate(func(s groupSource) string {
		if s.local == "p1" {
			return "acme/web"
		}
		return ""
	}, map[string]bool{"taken": true})
	want := map[string]string{d.members[0].id: "dir_duplicate", d.members[3].id: "dir_duplicate", d.members[2].id: "duplicate_repository"}
	if !v.nameRequired || v.valid || !reflect.DeepEqual(v.memberErrors, want) {
		t.Fatalf("validation %+v, want %v", v, want)
	}
	d.members[3].dir = "notes-2"
	if v := d.validate(nil, nil); v.memberErrors[d.members[3].id] != "machine_mismatch" {
		t.Fatalf("validation %+v", v)
	}
	d.name = " Taken "
	if v := d.validate(nil, map[string]bool{"taken": true}); !v.nameTaken {
		t.Fatal("name taken")
	}

	// A GitHub-only scope keeps no folder and no machine's copy; a machine's
	// keeps its own folders and binds its copies.
	d = groupDraft{}.add(groupSource{kind: "github", repo: "acme/web", preferLocal: true}, "acme/web").add(localSource("m1", "p1"), "notes").add(localSource("m2", "p2"), "docs")
	if g := d.retain(groupScope{kind: "github"}); len(g.members) != 1 || g.members[0].source.preferLocal {
		t.Fatalf("github %+v", g.members)
	}
	m := d.retain(groupScope{kind: "machine", machine: "m1"}).bind("m1", func(repo string) string { return map[string]string{"acme/web": "p7"}[repo] })
	var keys []string
	for _, x := range m.members {
		keys = append(keys, x.source.key())
	}
	if !reflect.DeepEqual(keys, []string{"local:m1:p7", "local:m1:p1"}) {
		t.Fatalf("machine %q", keys)
	}
	if got := m.suggestName(func(x groupMember) string { return "acme/web" }, map[string]bool{"web": true, "web 2": true}); got != "web 3" {
		t.Fatalf("name %q", got)
	}

	// Options: copies merge into their repository, one a machine, the one
	// opened last; folders without a remote stay folders.
	options := buildMemberOptions([]catalog.Project{
		{ID: "m1:local:a", MachineID: "m1", Name: "web", RepoFullName: "Acme/Web", OpenedAt: 1},
		{ID: "m1:local:b", MachineID: "m1", Name: "web-2", RepoFullName: "acme/web", OpenedAt: 5},
		{ID: "m2:local:c", MachineID: "m2", Name: "notes"},
	}, []string{"acme/web", "zeta/z"}, func(id string) string { return id }, func(string) bool { return true })
	var got []string
	for _, o := range options {
		got = append(got, o.kind+" "+o.label+" "+itoa(len(o.copies)))
	}
	if !reflect.DeepEqual(got, []string{"repository acme/web 1", "repository zeta/z 0", "directory notes 1"}) || options[0].copies[0].local != "b" ||
		!options[0].onGithub || !options[0].source.preferLocal || options[1].source.preferLocal {
		t.Fatalf("options %q %+v", got, options[0])
	}
	if by := optionsByKey(options); by["local:m1:a"] != by["github:acme/web"] {
		t.Fatal("a copy's folder doesn't find its repository")
	}
}
