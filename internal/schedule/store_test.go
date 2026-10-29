package schedule

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	loro "github.com/aholstenson/loro-go"

	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/internal/localplane"
	"github.com/lbr77/lody-mygo/internal/localplane/localplanetest"
)

// daemonDoc is the document the daemon has of a room: what peers sent.
func daemonDoc(t *testing.T, d *localplanetest.Daemon, room localplane.Room) *loro.LoroDoc {
	doc := loro.NewLoroDoc()
	for _, m := range d.Updates(room) {
		data, err := m.Payload.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := doc.Import(data); err != nil {
			t.Fatal(err)
		}
	}
	return doc
}

// daemonFlock is the Flock the daemon has of a room.
func daemonFlock(t *testing.T, d *localplanetest.Daemon, room localplane.Room) *flock.Flock {
	f, err := flock.New("daemon")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range d.Updates(room) {
		var bundle map[string]any
		if err := json.Unmarshal(m.Payload.Bundle, &bundle); err != nil {
			t.Fatal(err)
		}
		if err := f.ImportJSON(bundle); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestStoreLocal(t *testing.T) {
	d := localplanetest.Start(t)
	docRoom, registry := DocRoom("s1"), localplane.FlockRoom("ws:sr")
	d.OnJoin = func(r localplane.Room) *localplane.Payload {
		if r == docRoom && len(d.Updates(docRoom)) > 0 {
			snapshot, _ := daemonDoc(t, d, docRoom).Export(loro.SnapshotMode())
			p := localplane.DocUpdate(snapshot)
			return &p
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lc, err := localplane.Dial(ctx, d.Path, "ws")
	if err != nil {
		t.Fatal(err)
	}
	defer lc.Close()
	changes := make(chan Registry, 32)
	s := &Store{Local: lc, Workspace: "ws", OnChange: func(r Registry) { changes <- r }}
	s.Start()
	defer s.Stop()
	if err := s.room.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	draft := Draft{
		Title: "  Daily summary ", Prompt: "Summarize yesterday's commits", MachineID: "m1",
		Trigger: Trigger{Kind: "cron", Expression: "0 9 * * *", TimeZone: "Asia/Shanghai"},
		Agent:   Agent{AgentConfigID: "a1", ModelID: "opus", ConfigOptionValues: map[string]any{"fast": true, "effort": "high"}},
		Project: &Project{Kind: "local", LocalProjectID: "p1"},
	}
	if err := s.Save(ctx, "s1", true, Draft{Title: "x", MachineID: "m1", Agent: draft.Agent}, "u1", now); err != ErrPrompt {
		t.Fatalf("blank prompt: %v", err)
	}
	if err := s.Save(ctx, "s1", true, draft, "u1", now); err != nil {
		t.Fatal(err)
	}
	d.WaitUpdates(t, docRoom, 1)
	d.WaitUpdates(t, registry, 1)

	// The document the daemon has reads back to what the row fingerprints.
	got := read(daemonDoc(t, d, docRoom))
	def := got.Definition
	if !got.Exists || def.Title != "Daily summary" || def.OwnerID != "u1" || !def.Enabled || got.Prompt != draft.Prompt ||
		def.Destination.Kind != NewSession || def.Agent.ConfigOptionValues["fast"] != true || len(got.Timeline) != 1 || got.Timeline[0].Kind != "created" {
		t.Fatalf("document %+v", got)
	}
	if def.Project == nil || def.Project.LocalProjectID != "p1" || def.CreatedAt != now.UnixMilli() {
		t.Fatalf("definition %+v", def)
	}
	reg := RegistryOf(mustScan(t, daemonFlock(t, d, registry)))
	row := reg.Row("s1")
	if row == nil || row.DefinitionFingerprint != Fingerprint(def, got.Prompt) || row.ProjectKind != "local" || row.ProjectKey != "p1" || row.AgentConfigID != "a1" {
		t.Fatalf("row %+v", row)
	}
	if s.Registry().Row("s1") == nil {
		t.Fatal("the store doesn't list it")
	}

	// Someone else can't change it.
	if err := s.SetEnabled(ctx, "s1", false, "u2", now); err != ErrNotOwner {
		t.Fatalf("not owner: %v", err)
	}

	// Pausing is a new fingerprint and a timeline entry; resuming a new activation.
	if err := s.SetEnabled(ctx, "s1", false, "u1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	d.WaitUpdates(t, docRoom, 2)
	d.WaitUpdates(t, registry, 2)
	paused := read(daemonDoc(t, d, docRoom))
	if paused.Definition.Enabled || len(paused.Timeline) != 2 || paused.Timeline[1].Kind != "paused" {
		t.Fatalf("paused %+v", paused)
	}
	if err := s.SetEnabled(ctx, "s1", true, "u1", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	d.WaitUpdates(t, docRoom, 3)
	d.WaitUpdates(t, registry, 3)
	resumed := read(daemonDoc(t, d, docRoom)).Definition
	if !resumed.Enabled || resumed.ActivationID == def.ActivationID || resumed.ActiveFrom != now.Add(2*time.Minute).UnixMilli() {
		t.Fatalf("resumed %+v", resumed)
	}
	if fp := RegistryOf(mustScan(t, daemonFlock(t, d, registry))).Row("s1").DefinitionFingerprint; fp == row.DefinitionFingerprint {
		t.Fatal("the fingerprint didn't change")
	}

	// Run now asks the machine with the current activation.
	if err := s.RunNow(ctx, "s1", "u1", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	d.WaitUpdates(t, registry, 4)
	manual := false
	for _, r := range mustScan(t, daemonFlock(t, d, registry)) {
		if r.Key[0] == "manual" {
			v := r.Value.(map[string]any)
			manual = v["activationId"] == resumed.ActivationID && v["actorId"] == "u1" && v["manualRunId"] == r.Key[2]
		}
	}
	if !manual {
		t.Fatal("no manual run request")
	}

	// Deleting tombstones it.
	if err := s.Delete(ctx, "s1", "u1", now); err != nil {
		t.Fatal(err)
	}
	d.WaitUpdates(t, registry, 5)
	if RegistryOf(mustScan(t, daemonFlock(t, d, registry))).Row("s1") != nil || s.Registry().Row("s1") != nil {
		t.Fatal("still listed")
	}
	if err := s.Save(ctx, "s1", false, draft, "u1", now); err != ErrDeleted {
		t.Fatalf("edit after delete: %v", err)
	}
}

func mustScan(t *testing.T, f *flock.Flock) []flock.Row {
	rows, err := f.Scan()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// A runtime row counts only while it speaks of the row as it stands.
func TestRegistryRuntime(t *testing.T) {
	row := Row{ScheduleID: "s1", MachineID: "m1", ActivationID: "a", DefinitionFingerprint: "f", CreatedAt: 1}
	rows := []flock.Row{
		{Key: []any{"schedule", "s1"}, Value: plain(row), Live: true},
		{Key: []any{"runtime", "s1", "m1"}, Value: map[string]any{"scheduleId": "s1", "machineId": "m1", "activationId": "a", "observedDefinitionFingerprint": "f", "nextScheduledAt": 5.0, "updatedAt": 1.0}, Live: true},
		{Key: []any{"schedule", "s2"}, Value: map[string]any{"scheduleId": "other"}, Live: true},
	}
	reg := RegistryOf(rows)
	if len(reg.Rows) != 1 || reg.Runtime["s1"] == nil || reg.Runtime["s1"].NextScheduledAt != 5 {
		t.Fatalf("registry %+v", reg)
	}
	rows[1].Value.(map[string]any)["activationId"] = "old"
	if RegistryOf(rows).Runtime["s1"] != nil {
		t.Fatal("a stale runtime row counted")
	}
}

// A scheduled message is a once schedule into a chat whose document holds
// the message; its fingerprint takes the message in.
func TestScheduledMessage(t *testing.T) {
	d := localplanetest.Start(t)
	docRoom, registry := DocRoom("m1"), localplane.FlockRoom("ws:sr")
	d.OnJoin = func(r localplane.Room) *localplane.Payload {
		if r == docRoom && len(d.Updates(docRoom)) > 0 {
			snapshot, _ := daemonDoc(t, d, docRoom).Export(loro.SnapshotMode())
			p := localplane.DocUpdate(snapshot)
			return &p
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lc, err := localplane.Dial(ctx, d.Path, "ws")
	if err != nil {
		t.Fatal(err)
	}
	defer lc.Close()
	s := &Store{Local: lc, Workspace: "ws"}
	s.Start()
	defer s.Stop()
	if err := s.room.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	draft := MessageDraft{SessionID: "chat1", MachineID: "mach", Agent: Agent{AgentConfigID: "a1", ModelID: "opus"},
		Payload: map[string]any{"inputBlocks": []any{map[string]any{"type": "text", "text": "Check the deploy\nand report"}}, "mcpServerIds": []any{"gh"}},
		At:      now.Add(time.Hour)}
	if err := s.SaveMessage(ctx, "m1", MessageDraft{At: now}, "u1", "Schedule send", now); err != ErrTimePassed {
		t.Fatalf("past time: %v", err)
	}
	if err := s.SaveMessage(ctx, "m1", draft, "u1", "Schedule send", now); err != nil {
		t.Fatal(err)
	}
	d.WaitUpdates(t, docRoom, 1)
	d.WaitUpdates(t, registry, 1)
	got := read(daemonDoc(t, d, docRoom))
	def := got.Definition
	if def.MessageVersion != 1 || def.Title != "Check the deploy" || def.Trigger.At != "2026-10-29T10:00:00.000Z" || def.ActiveFrom != draft.At.UnixMilli() ||
		def.Destination.Kind != ExistingSession || def.Destination.SessionID != "chat1" || got.Prompt != "Check the deploy\nand report" || got.Message == nil {
		t.Fatalf("document %+v", got)
	}
	row := RegistryOf(mustScan(t, daemonFlock(t, d, registry))).Row("m1")
	if row == nil || !row.ScheduledMessage || row.DefinitionFingerprint != fingerprint(def, got.Prompt, got.Message) || row.DefinitionFingerprint == Fingerprint(def, got.Prompt) {
		t.Fatalf("row %+v", row)
	}

	// Moving it keeps the message, at the new time.
	if err := s.Reschedule(ctx, "m1", now.Add(3*time.Hour), "u1", now); err != nil {
		t.Fatal(err)
	}
	d.WaitUpdates(t, registry, 2)
	moved := read(daemonDoc(t, d, docRoom))
	if moved.Definition.Trigger.At != "2026-10-29T12:00:00.000Z" || moved.Message == nil {
		t.Fatalf("moved %+v", moved)
	}
	// Cancelling turns it off, which the machine confirms.
	if err := s.SetEnabled(ctx, "m1", false, "u1", now); err != nil {
		t.Fatal(err)
	}
	d.WaitUpdates(t, registry, 3)
	off := RegistryOf(mustScan(t, daemonFlock(t, d, registry))).Row("m1")
	if off.Enabled || off.DefinitionFingerprint != fingerprint(read(daemonDoc(t, d, docRoom)).Definition, moved.Prompt, moved.Message) {
		t.Fatalf("cancelled %+v", off)
	}
}
