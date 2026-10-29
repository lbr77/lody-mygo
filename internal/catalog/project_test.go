package catalog

import (
	"testing"

	"github.com/lbr77/lody-mygo/internal/flock"
)

func row(value any, key ...any) flock.Row { return flock.Row{Key: key, Value: value, Live: true} }

func TestProjectMeta(t *testing.T) {
	rows := []flock.Row{
		row(true, "e", "machine-m1"),
		row(true, "e", "session-s1"),
		row(true, "e", "session-s2"),
		row(false, "e", "session-s3"),
		row(map[string]any{"name": "Studio", "localProjects": map[string]any{"p1": map[string]any{"name": "lody", "rootPath": "/src/lody"}}}, "m", "machine-m1"),
		row(map[string]any{"id": "s1", "machineId": "m1", "title": "Draft", "status": map[string]any{"type": "running"}, "createdAt": "2026-10-01T00:00:00Z", "project": map[string]any{"kind": "local", "localProjectId": "p1"}, "lastMessageAt": 5.0}, "m", "session-s1"),
		row("Renamed", "m", "session-s1", "title"),
		{Key: []any{"m", "session-s1", "lastMessageAt"}},
		row(map[string]any{"machineId": "m1", "repoFullName": "acme/app", "createdAt": "2026-10-02T00:00:00Z"}, "m", "session-s2"),
		row(map[string]any{"machineId": "m1", "title": "gone"}, "m", "session-s3"),
	}
	c := merge(projectMeta(rows), map[string]Catalog{"m1": projectMachine([]flock.Row{
		row(map[string]any{"name": "lody (machine)", "rootPath": "/src/lody"}, "localProject", "p1"),
		row(map[string]any{"id": "a1", "name": "Claude Code", "agentType": "claude", "cliType": "builtin"}, "agentConfig", "a1"),
	}, "m1")})

	if len(c.Sessions) != 2 || c.Sessions[0].ID != "s2" || c.Sessions[1].ID != "s1" {
		t.Fatalf("sessions = %+v", c.Sessions)
	}
	s1 := c.Session("s1")
	if s1.Title != "Renamed" || s1.Status != "running" || s1.ProjectID != "m1:local:p1" || s1.LastMessageAt != 0 || !s1.Working() {
		t.Fatalf("s1 = %+v", s1)
	}
	s2 := c.Session("s2")
	if s2.Title != "Untitled session" || s2.ProjectID != "github:acme/app" || s2.Status != "unknown" {
		t.Fatalf("s2 = %+v", s2)
	}
	if p := c.Project("m1:local:p1"); p == nil || p.Name != "lody (machine)" {
		t.Fatalf("project = %+v", p)
	}
	if p := c.Project("github:acme/app"); p == nil || p.RepoFullName != "acme/app" {
		t.Fatalf("repo project = %+v", p)
	}
	if c.MachineNames["m1"] != "Studio" || len(c.Agents["m1"]) != 1 || c.Agents["m1"][0].Name != "Claude Code" {
		t.Fatalf("machines = %+v %+v", c.MachineNames, c.Agents)
	}
}

func TestProjectMCPKey(t *testing.T) {
	if got := ProjectMCPKey("m1", "p1"); got != `["v1","local","m1","p1"]` {
		t.Fatalf("key = %s", got)
	}
}

func TestRateLimitRows(t *testing.T) {
	rows := []flock.Row{
		{Live: true, Key: []any{"rateLimit", "c1", "codex", "codex"}, Value: map[string]any{"limitId": "codex", "scope": map[string]any{"providerId": "c1"},
			"windows": []any{map[string]any{"usedPercent": 42.0, "windowDurationSeconds": 18000.0, "resetsAtEpochSeconds": 1798800000.0},
				map[string]any{"usedPercent": 130.0, "windowDurationSeconds": 604800.0, "resetsAtEpochSeconds": nil}}}},
		{Live: true, Key: []any{"rateLimit", "codex", "primary"}, Value: map[string]any{"windows": []any{}}},
		{Live: true, Key: []any{"acpCapability", "c1"}, Value: map[string]any{"cliType": "builtin", "agentType": "codex", "rateLimits": map[string]any{"version": 1.0, "query": true}}},
	}
	c := projectMachine(rows, "m1")
	limits := c.RateLimits["c1"]
	if len(limits) != 1 || len(limits[0].Windows) != 2 || limits[0].Windows[0].UsedPercent != 42 || limits[0].Windows[1].UsedPercent != 100 ||
		limits[0].Windows[1].ResetsAt != 0 || limits[0].CLIType != "codex" {
		t.Fatalf("limits = %+v", c.RateLimits)
	}
	if cap := c.Capabilities["c1"]; !cap.RateLimits || !cap.QueryRateLimits {
		t.Fatalf("capability = %+v", cap)
	}
}
