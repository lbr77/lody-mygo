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
