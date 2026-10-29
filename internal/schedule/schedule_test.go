package schedule

import (
	"regexp"
	"testing"
)

func intp(n int) *int { return &n }

// The fingerprints of Lody's algorithm, for two definitions.
func TestFingerprint(t *testing.T) {
	d := Definition{
		ScheduleID: "test", Title: "ignored", OwnerID: "owner", MachineID: "machine", Enabled: true,
		ActivationID: "activation", ActiveFrom: 0,
		Trigger:       Trigger{Kind: "interval", EveryMs: 60000, AnchorAt: "2026-01-01T00:00:00.000Z"},
		MisfirePolicy: Policy{Kind: "skip"}, OverlapPolicy: "queue_one",
		Agent:       Agent{AgentConfigID: "agent", ModeID: "safe"},
		Project:     &Project{Kind: "github", RepoFullName: "loro-dev/lody", Branch: "main"},
		Destination: Destination{Kind: NewSession}, RetryPolicy: DefaultRetry,
		CreatedAt: 5, UpdatedAt: 6, CreatedBy: "someone",
	}
	if got := Fingerprint(d, "hello"); got != "b93495946a18b1c00d99302943de81de13fa93fadcbd543fd80a58035e313939" {
		t.Fatalf("fingerprint %s", got)
	}
	d.Project = nil
	d.Agent = Agent{AgentConfigID: "agent", ModelID: "gpt-5", ConfigOptionValues: map[string]any{"reasoning_effort": "high", "fast": true}}
	d.Destination = Destination{Kind: OwnSession, Epoch: intp(0)}
	d.Trigger = Trigger{Kind: "cron", Expression: "0 9 * * 1-5", TimeZone: "Asia/Shanghai"}
	if got := Fingerprint(d, "每天总结"); got != "f1f8fb00c577fe59ee125ac5f361d31de350ffde7738e1d186830dba7e1207dd" {
		t.Fatalf("fingerprint %s", got)
	}

	row := RowOf(d, "每天总结")
	if row.DefinitionFingerprint != Fingerprint(d, "每天总结") || row.AgentConfigID != "agent" || row.ProjectKind != "" || row.ElevatedPermissions {
		t.Fatalf("row %+v", row)
	}
	d.Agent.ConfigOptionValues["mode"] = "bypassPermissions"
	d.Project = &Project{Kind: "local", LocalProjectID: "p1"}
	if row := RowOf(d, "x"); !row.ElevatedPermissions || row.ProjectKind != "local" || row.ProjectKey != "p1" {
		t.Fatalf("row %+v", row)
	}
}

func TestCanonical(t *testing.T) {
	got := string(Canonical(map[string]any{"b": 1, "a": []any{"<&>", "\u2028", "\x01"}, "z": nil, "ä": true, "B": 1.5}))
	if want := "{\"B\":1.5,\"a\":[\"<&>\",\"\u2028\",\"\\u0001\"],\"b\":1,\"ä\":true}"; got != want {
		t.Fatalf("canonical %s, want %s", got, want)
	}
}

func TestOwnSessionID(t *testing.T) {
	// As Lody's uuidV8 names it.
	id := OwnSessionID("s1", 0)
	if id != "718f1cec-c2b6-8d96-8c84-12f035cf6499" {
		t.Fatalf("id %s", id)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-8[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) {
		t.Fatalf("id %s", id)
	}
	if id == OwnSessionID("s1", 1) || id != OwnSessionID("s1", 0) {
		t.Fatal("ids aren't per epoch")
	}
}
