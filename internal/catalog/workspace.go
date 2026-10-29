package catalog

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/lbr77/lody-mygo/internal/flock"
)

// The workspace stream, "wf:workspace", holds what the workspace shares
// beyond its sessions: its MCP servers, agent roles and projects of
// several repositories. Machines' streams
// hold the memory each machine links and the projects it is removing.

// MCPServer is an MCP server the workspace offers its sessions.
type MCPServer struct {
	ID               string
	Name             string
	Transport        string // stdio or http
	Description      string
	Connection       map[string]any
	EnabledByDefault bool
	CreatedAt        float64
	UpdatedAt        float64
	CreatedBy        string
}

// Summary is how the server starts: its command and arguments, or its
// URL.
func (m MCPServer) Summary() string {
	if m.Connection == nil {
		return "—"
	}
	if m.Transport == "http" {
		return text(m.Connection["url"])
	}
	parts := []string{text(m.Connection["command"])}
	for _, a := range anyList(m.Connection["args"]) {
		parts = append(parts, text(a))
	}
	return strings.Join(parts, " ")
}

// Meta is the server as the workspace stream holds it, unchanged.
func (m MCPServer) Meta() map[string]any {
	return m.Row(max(m.UpdatedAt, m.CreatedAt), "")
}

// Row is the server as the workspace stream stores it.
func (m MCPServer) Row(now float64, user string) map[string]any {
	row := map[string]any{
		"id": m.ID, "name": m.Name, "transport": m.Transport, "enabledByDefault": m.EnabledByDefault,
		"createdAt": m.CreatedAt, "updatedAt": now,
	}
	if m.CreatedAt == 0 {
		row["createdAt"] = now
	}
	if m.CreatedBy != "" {
		row["createdBy"] = m.CreatedBy
	} else if user != "" {
		row["createdBy"] = user
	}
	if m.Description != "" {
		row["description"] = m.Description
	}
	if m.Connection != nil {
		row["connection"] = m.Connection
	}
	return row
}

// AgentRole is a reusable agent setup: a name, a prompt and where it runs.
type AgentRole struct {
	ID           string
	OwnerUserID  string
	Visibility   string // private or workspace
	Name         string
	Description  string
	Emoji        string
	PromptPrefix string
	Targets      []RoleTarget
	Revision     float64
	// Row is the role as the workspace stream stores it, for edits.
	Row map[string]any
}

// RoleTarget is a machine and agent a role runs on.
type RoleTarget struct {
	MachineID      string
	AgentConfigID  string
	ModelID        string
	ModeID         string
	Memory         string // memoryId, of the provider MemoryProvider names
	MemoryProvider string
}

// ProjectGroup is a project of several repositories or folders, Lody's
// WorkspaceProject: a row ['project', id] of the workspace stream that
// one conversation works across, each member a GitHub repository or one
// machine's folder in its own directory.
type ProjectGroup struct {
	ID           string
	Name         string
	Instructions string // what the agent is told first in its conversations
	OwnerUserID  string
	CreatedAt    float64
	Revision     float64 // moves on with each saved change
	Members      []ProjectGroupMember
	// Row is the project as the workspace stream stores it.
	Row map[string]any
}

// ProjectGroupMember is a project's repository or folder: a GitHub
// repository, cloned for each conversation or, preferring a local copy,
// a machine's copy of it; or a machine's local project.
type ProjectGroupMember struct {
	ID             string
	Kind           string // github or local
	RepoFullName   string
	PreferLocal    bool
	MachineID      string
	LocalProjectID string
	// Where it sits in a conversation's directory: a top-level Dir of its
	// own, or, as its Parent's submodule, at Path within it.
	Dir, Parent, Path string
}

// ProjectRun is the snapshot of a project of several a conversation
// started in carries, Lody's SessionProjectRun: the client writes it as
// the conversation is created, and from then on only the machine that
// owns the conversation does, recording what it observes of each member.
type ProjectRun struct {
	ID, Name     string
	Instructions string
	Root         string // the conversation's directory the members sit in
	// Members in the snapshot's order (its "o").
	Members []RunMember
}

// RunMember is one repository or folder of a run, Lody's
// SessionProjectMember: where it came from, where it sits, and what the
// machine has seen of it.
type RunMember struct {
	ID             string
	Order          float64
	Kind           string // github or local, as its source
	RepoFullName   string // a GitHub source's repository
	MachineID      string // a local source's
	LocalProjectID string
	// Where it sits: a top-level Dir, or as Parent's submodule at
	// SubPath; Path is its directory under the run's.
	Dir, Parent, SubPath string
	Path                 string
	Base                 string // the commit or branch its worktree started from
	// Access is how the agent reaches it, as the owning machine says:
	// worktree, or a local folder used in place, additional or link.
	Access string
	// Observed is the GitHub repository the machine saw, a local
	// member's remote included.
	Observed string
	Branch   string
	Dirty    bool
	Unpushed bool
	// HasDiff tells that the machine counted its lines, DiffAdd and
	// DiffDel.
	HasDiff          bool
	DiffAdd, DiffDel float64
}

// Submodule is whether the member sits inside another.
func (m RunMember) Submodule() bool { return m.Parent != "" }

// projectRunOf reads a session's projectRun, its members by their order.
// A member with no id or an unknown source is left out.
func projectRunOf(v map[string]any) *ProjectRun {
	run := &ProjectRun{ID: text(v["id"]), Name: text(v["name"]), Instructions: text(v["instructions"]), Root: text(v["root"])}
	for id, raw := range object(v["members"]) {
		m := object(raw)
		src, at := object(m["src"]), object(m["placement"])
		member := RunMember{ID: id, Order: stamp(m["o"]), Kind: text(src["kind"]), RepoFullName: text(src["repoFullName"]),
			MachineID: text(src["machineId"]), LocalProjectID: text(src["localProjectId"]), Path: text(m["path"]), Base: text(m["base"]),
			Access: text(m["access"]), Observed: text(m["repoFullName"]), Branch: text(m["branch"]),
			Dirty: m["dirty"] == true, Unpushed: m["unpushed"] == true}
		if text(at["kind"]) == "submodule" {
			member.Parent, member.SubPath = text(at["parent"]), text(at["path"])
		} else {
			member.Dir = text(at["dir"])
		}
		if diff, ok := m["diff"].(map[string]any); ok {
			member.HasDiff, member.DiffAdd, member.DiffDel = true, stamp(diff["add"]), stamp(diff["del"])
		}
		if id == "" || member.Kind != "github" && member.Kind != "local" {
			continue
		}
		run.Members = append(run.Members, member)
	}
	sort.SliceStable(run.Members, func(i, j int) bool {
		if run.Members[i].Order != run.Members[j].Order {
			return run.Members[i].Order < run.Members[j].Order
		}
		return run.Members[i].ID < run.Members[j].ID
	})
	return run
}

// BoundMachine is the one machine a project's folders put it on, as
// Lody's getProjectBoundMachineId: none when it runs anywhere, or its
// folders disagree.
func (g ProjectGroup) BoundMachine() string {
	machine := ""
	for _, m := range g.Members {
		if m.Kind != "local" {
			continue
		}
		if machine != "" && machine != m.MachineID {
			return ""
		}
		machine = m.MachineID
	}
	return machine
}

// Memory is a memory a machine links to Lody from a provider.
type Memory struct {
	ProviderID  string
	MemoryID    string
	MachineID   string
	Name        string
	Description string
}

func projectWorkspace(rows []flock.Row) Catalog {
	var c Catalog
	for _, row := range rows {
		if !row.Live || len(row.Key) != 2 {
			continue
		}
		id := text(row.Key[1])
		v := object(row.Value)
		if row.Key[0] == "projectMcpDefault" && id != "" && v != nil {
			// Keyed by the project, not an id: the servers its new chats load.
			ids := []string{}
			for _, x := range anyList(v["ids"]) {
				if s := text(x); s != "" {
					ids = append(ids, s)
				}
			}
			if c.ProjectMCP == nil {
				c.ProjectMCP = map[string][]string{}
			}
			c.ProjectMCP[id] = ids
			continue
		}
		if id == "" || v == nil || text(v["id"]) != id {
			continue
		}
		switch row.Key[0] {
		case "mcpServer":
			m := MCPServer{
				ID: id, Name: strings.TrimSpace(text(v["name"])), Transport: text(v["transport"]), Description: text(v["description"]),
				Connection: object(v["connection"]), EnabledByDefault: v["enabledByDefault"] == true,
				CreatedAt: stamp(v["createdAt"]), UpdatedAt: stamp(v["updatedAt"]), CreatedBy: text(v["createdBy"]),
			}
			if m.Name == "" || (m.Transport != "stdio" && m.Transport != "http") {
				continue
			}
			if m.Connection != nil && text(m.Connection["transport"]) != m.Transport {
				continue
			}
			c.MCPServers = append(c.MCPServers, m)
		case "agentRole":
			r := AgentRole{
				ID: id, OwnerUserID: text(v["ownerUserId"]), Visibility: text(v["visibility"]), Name: strings.TrimSpace(text(v["name"])),
				Description: text(v["description"]), Emoji: text(v["emoji"]), PromptPrefix: text(v["promptPrefix"]),
				Revision: stamp(v["revision"]), Row: v,
			}
			if r.OwnerUserID == "" || r.Name == "" {
				continue
			}
			target := func(t map[string]any) RoleTarget {
				cfg := object(t["runConfig"])
				mem := object(cfg["memory"])
				return RoleTarget{MachineID: text(t["machineId"]), AgentConfigID: text(t["agentConfigId"]), ModelID: text(cfg["modelId"]), ModeID: text(cfg["modeId"]),
					Memory: text(mem["memoryId"]), MemoryProvider: text(mem["providerId"])}
			}
			if targets := anyList(v["targets"]); len(targets) > 0 {
				for _, t := range targets {
					r.Targets = append(r.Targets, target(object(t)))
				}
			} else if text(v["machineId"]) != "" {
				r.Targets = append(r.Targets, target(v))
			}
			c.Roles = append(c.Roles, r)
		case "project":
			g := ProjectGroup{ID: id, Name: strings.TrimSpace(text(v["name"])), Instructions: text(v["instructions"]), OwnerUserID: text(v["ownerUserId"]),
				CreatedAt: stamp(v["createdAtMs"]), Revision: stamp(v["revision"]), Row: v}
			for _, raw := range anyList(v["members"]) {
				m := object(raw)
				src, at := object(m["source"]), object(m["placement"])
				member := ProjectGroupMember{ID: text(m["id"]), Kind: text(src["kind"]), RepoFullName: text(src["repoFullName"]),
					PreferLocal: src["preferLocal"] == true, MachineID: text(src["machineId"]), LocalProjectID: text(src["localProjectId"])}
				if text(at["kind"]) == "submodule" {
					member.Parent, member.Path = text(at["parent"]), text(at["path"])
				} else {
					member.Dir = text(at["dir"])
				}
				if member.ID == "" || (member.Kind != "github" && member.Kind != "local") {
					continue
				}
				g.Members = append(g.Members, member)
			}
			if g.Name == "" || g.OwnerUserID == "" || len(g.Members) == 0 {
				continue
			}
			c.Groups = append(c.Groups, g)
		}
	}
	sort.SliceStable(c.Groups, func(i, j int) bool {
		return strings.ToLower(c.Groups[i].Name) < strings.ToLower(c.Groups[j].Name)
	})
	sort.SliceStable(c.MCPServers, func(i, j int) bool {
		a, b := strings.ToLower(c.MCPServers[i].Name), strings.ToLower(c.MCPServers[j].Name)
		if a != b {
			return a < b
		}
		return c.MCPServers[i].ID < c.MCPServers[j].ID
	})
	sort.SliceStable(c.Roles, func(i, j int) bool {
		return strings.ToLower(c.Roles[i].Name) < strings.ToLower(c.Roles[j].Name)
	})
	return c
}

// WriteWorkspace applies edits to the workspace stream.
func (s *Sync) WriteWorkspace(ctx context.Context, edit func(f *flock.Flock) error) error {
	return s.WriteMachine(ctx, workspaceMode, edit)
}

// WriteMachine applies edits to a machine's stream.
func (s *Sync) WriteMachine(ctx context.Context, machineID string, edit func(f *flock.Flock) error) error {
	s.mu.Lock()
	rep := s.machines[machineID]
	s.mu.Unlock()
	if rep == nil {
		return ErrNotReady
	}
	return s.writeTo(ctx, rep, machineID, edit)
}

// workspaceMode names the workspace stream among the replicas.
const workspaceMode = "wf"

// project projects a replica's rows by the stream they come from.
func project(mode string, rows []flock.Row) Catalog {
	switch mode {
	case "meta":
		return projectMeta(rows)
	case workspaceMode:
		return projectWorkspace(rows)
	}
	return projectMachine(rows, mode)
}

// ProjectMCPKey is how the workspace stream keys a local project's
// default MCP servers.
func ProjectMCPKey(machineID, localProjectID string) string {
	b, _ := json.Marshal([]string{"v1", "local", machineID, localProjectID})
	return string(b)
}
