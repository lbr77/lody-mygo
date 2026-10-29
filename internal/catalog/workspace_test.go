package catalog

import (
	"testing"

	"github.com/lbr77/lody-mygo/internal/flock"
)

// The workspace's projects of several repositories, and the GitHub remote
// a machine published for its folder, which files it under its repository.
func TestProjectGroups(t *testing.T) {
	ws := projectWorkspace([]flock.Row{
		row(map[string]any{"v": 1.0, "id": "g2", "name": "Shop", "ownerUserId": "u1", "revision": 1.0, "members": []any{
			map[string]any{"id": "a", "source": map[string]any{"kind": "local", "machineId": "m1", "localProjectId": "p1"}, "placement": map[string]any{"kind": "root", "dir": "web"}},
			map[string]any{"id": "b", "source": map[string]any{"kind": "github", "repoFullName": "acme/api", "preferLocal": true}, "placement": map[string]any{"kind": "root", "dir": "api"}},
		}}, "project", "g2"),
		row(map[string]any{"id": "g1", "name": "anywhere", "ownerUserId": "u1", "members": []any{
			map[string]any{"id": "c", "source": map[string]any{"kind": "github", "repoFullName": "acme/web"}},
		}}, "project", "g1"),
		row(map[string]any{"id": "g3", "name": "", "ownerUserId": "u1", "members": []any{}}, "project", "g3"),
	})
	c := merge(Catalog{MachineIDs: []string{"m1"}}, map[string]Catalog{workspaceMode: ws, "m1": projectMachine([]flock.Row{
		row(map[string]any{"name": "web", "rootPath": "/src/web", "githubRepoFullName": "acme/web", "createdAtMs": 3.0, "lastOpenedAtMs": 9.0}, "localProject", "p1"),
	}, "m1")})
	if len(c.Groups) != 2 || c.Groups[0].Name != "anywhere" || c.Groups[1].Name != "Shop" {
		t.Fatalf("groups %+v", c.Groups)
	}
	shop := c.Groups[1]
	if shop.BoundMachine() != "m1" || c.Groups[0].BoundMachine() != "" || len(shop.Members) != 2 || !shop.Members[1].PreferLocal || shop.Members[1].RepoFullName != "acme/api" {
		t.Fatalf("shop %+v", shop)
	}
	if p := c.Project("m1:local:p1"); p == nil || p.RepoFullName != "acme/web" || p.OpenedAt != 9 {
		t.Fatalf("project %+v", p)
	}
}

// A project's instructions, revision and placements, a folder only
// projects hold, and a session started in a project of several, which
// carries its run.
func TestProjectGroupDetails(t *testing.T) {
	ws := projectWorkspace([]flock.Row{
		row(map[string]any{"v": 1.0, "id": "g", "name": "Shop", "instructions": "web first", "ownerUserId": "u1", "createdAtMs": 7.0, "revision": 3.0, "members": []any{
			map[string]any{"id": "a", "source": map[string]any{"kind": "github", "repoFullName": "acme/web"}, "placement": map[string]any{"kind": "root", "dir": "web"}},
			map[string]any{"id": "b", "source": map[string]any{"kind": "github", "repoFullName": "acme/proto"}, "placement": map[string]any{"kind": "submodule", "parent": "a", "path": "vendor/proto"}},
		}}, "project", "g"),
	})
	g := ws.Groups[0]
	if g.Instructions != "web first" || g.Revision != 3 || g.CreatedAt != 7 || g.Members[0].Dir != "web" || g.Members[1].Parent != "a" || g.Members[1].Path != "vendor/proto" {
		t.Fatalf("group %+v", g)
	}
	m := projectMachine([]flock.Row{
		row(map[string]any{"name": "api", "rootPath": "/src/api", "standaloneVisible": false}, "localProject", "p1"),
		row(map[string]any{"name": "web", "rootPath": "/src/web"}, "localProject", "p2"),
	}, "m1")
	if !m.Projects[0].MemberOnly || m.Projects[1].MemberOnly {
		t.Fatalf("projects %+v", m.Projects)
	}
	n := NewSession{ID: "s", MachineID: "m1", Agent: AgentConfig{ID: "a1"},
		Project: map[string]any{"kind": "github", "repoFullName": "acme/web", "branch": "HEAD"}, RepoFullName: "acme/web", BaseBranch: "HEAD",
		ProjectRun: map[string]any{"id": "g", "name": "Shop", "root": "project-shop-g", "members": map[string]any{}}}
	meta := n.Meta()
	if meta["projectRun"] == nil || meta["isWorktree"] != true || meta["baseBranch"] != "HEAD" || meta["repoFullName"] != "acme/web" || meta["project"] == nil {
		t.Fatalf("meta %v", meta)
	}
	c := projectMeta([]flock.Row{row(true, "e", "session-s"), row(meta, "m", "session-s")})
	if s := c.Session("s"); s == nil || s.ProjectGroupID != "g" || s.ProjectGroupName != "Shop" || s.ProjectID != "github:acme/web" || !s.IsWorktree {
		t.Fatalf("session %+v", s)
	}
}

// A conversation's run snapshot, as its machine writes it back: the
// members in their order, where each sits, how the agent reaches it, the
// repository and branch it saw and the lines it counted; a member with an
// unknown source is left out.
func TestProjectRunSnapshot(t *testing.T) {
	meta := map[string]any{"id": "s", "machineId": "m1", "workspaceDirty": true, "projectRun": map[string]any{"id": "g", "name": "Shop", "root": "project-shop-g", "members": map[string]any{
		"b": map[string]any{"o": 1.0, "src": map[string]any{"kind": "github", "repoFullName": "acme/proto"}, "placement": map[string]any{"kind": "submodule", "parent": "a", "path": "vendor/proto"},
			"path": "web/vendor/proto", "base": "gitlink", "branch": "lody/b", "unpushed": true},
		"a": map[string]any{"o": 0.0, "src": map[string]any{"kind": "github", "repoFullName": "acme/web"}, "placement": map[string]any{"kind": "root", "dir": "web"},
			"path": "web", "base": "HEAD", "branch": "lody/a", "access": "worktree", "diff": map[string]any{"add": 3.0, "del": 1.0}, "dirty": true},
		"c": map[string]any{"o": 2.0, "src": map[string]any{"kind": "local", "machineId": "m1", "localProjectId": "p2"}, "placement": map[string]any{"kind": "root", "dir": "docs"},
			"path": "docs", "base": "HEAD", "access": "additional", "repoFullName": "acme/docs"},
		"x": map[string]any{"o": 3.0, "src": map[string]any{"kind": "svn"}, "path": "x"},
	}}}
	c := projectMeta([]flock.Row{row(true, "e", "session-s"), row(meta, "m", "session-s")})
	s := c.Session("s")
	if s == nil || s.ProjectRun == nil || !s.WorkspaceDirty || s.WorkspaceUnpushed {
		t.Fatalf("session %+v", s)
	}
	run := s.ProjectRun
	if run.ID != "g" || run.Name != "Shop" || run.Root != "project-shop-g" || len(run.Members) != 3 {
		t.Fatalf("run %+v", run)
	}
	a, b, d := run.Members[0], run.Members[1], run.Members[2]
	if a.ID != "a" || a.Dir != "web" || a.Branch != "lody/a" || !a.HasDiff || a.DiffAdd != 3 || a.DiffDel != 1 || !a.Dirty || a.Access != "worktree" || a.Submodule() {
		t.Fatalf("a %+v", a)
	}
	if b.ID != "b" || !b.Submodule() || b.Parent != "a" || b.SubPath != "vendor/proto" || b.Path != "web/vendor/proto" || b.Base != "gitlink" || !b.Unpushed || b.HasDiff {
		t.Fatalf("b %+v", b)
	}
	if d.ID != "c" || d.Kind != "local" || d.MachineID != "m1" || d.LocalProjectID != "p2" || d.Access != "additional" || d.Observed != "acme/docs" {
		t.Fatalf("c %+v", d)
	}
}
