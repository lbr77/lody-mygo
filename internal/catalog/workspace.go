package catalog

import (
	"context"
	"sort"
	"strings"

	"github.com/lbr77/lody-mygo/internal/flock"
)

// The workspace stream, "wf:workspace", holds what the workspace shares
// beyond its sessions: its MCP servers and agent roles. Machines' streams
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
}

// RoleTarget is a machine and agent a role runs on.
type RoleTarget struct {
	MachineID     string
	AgentConfigID string
	ModelID       string
	ModeID        string
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
		if id == "" || v == nil || text(v["id"]) != id {
			continue
		}
		switch row.Key[0] {
		case "mcpServer":
			m := MCPServer{
				ID: id, Name: strings.TrimSpace(text(v["name"])), Transport: text(v["transport"]), Description: text(v["description"]),
				Connection: object(v["connection"]), EnabledByDefault: v["enabledByDefault"] == true,
				CreatedAt: stamp(v["createdAt"]), CreatedBy: text(v["createdBy"]),
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
			}
			if r.OwnerUserID == "" || r.Name == "" {
				continue
			}
			target := func(t map[string]any) RoleTarget {
				cfg := object(t["runConfig"])
				return RoleTarget{MachineID: text(t["machineId"]), AgentConfigID: text(t["agentConfigId"]), ModelID: text(cfg["modelId"]), ModeID: text(cfg["modeId"])}
			}
			if targets := anyList(v["targets"]); len(targets) > 0 {
				for _, t := range targets {
					r.Targets = append(r.Targets, target(object(t)))
				}
			} else if text(v["machineId"]) != "" {
				r.Targets = append(r.Targets, target(v))
			}
			c.Roles = append(c.Roles, r)
		}
	}
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
