package app

import (
	"context"
	"testing"
	"time"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/localplane"
	"github.com/lbr77/lody-mygo/internal/localplane/localplanetest"
	"github.com/lbr77/lody-mygo/internal/schedule"
	"github.com/lbr77/lody-mygo/internal/session"
)

func schedFixture() *App {
	a := fixture()
	a.page = "schedules"
	a.catalog.MachineZone = map[string]string{"m1": "Asia/Shanghai"}
	epoch := 0
	next := time.Now().Add(26 * time.Hour).UnixMilli()
	a.sched.ready = true
	a.sched.reg = schedule.Registry{
		Rows: []schedule.Row{
			{ScheduleID: "s1", Title: "Daily summary", OwnerID: "u1", MachineID: "m1", Enabled: true, AgentConfigID: "a1",
				Trigger: schedule.Trigger{Kind: "cron", Expression: "0 9 * * *", TimeZone: "Asia/Shanghai"}, Destination: schedule.Destination{Kind: schedule.NewSession},
				ProjectKind: "local", ProjectKey: "p1", CreatedAt: 3},
			{ScheduleID: "s2", Title: "Release notes", OwnerID: "u1", MachineID: "m1", Enabled: true, AgentConfigID: "a1",
				Trigger: schedule.Trigger{Kind: "manual"}, Destination: schedule.Destination{Kind: schedule.OwnSession, Epoch: &epoch}, CreatedAt: 2},
			{ScheduleID: "s3", Title: "Weekly report", OwnerID: "u1", MachineID: "m1", Enabled: false, AgentConfigID: "a1",
				Trigger: schedule.Trigger{Kind: "cron", Expression: "30 17 * * 1,5", TimeZone: "Asia/Shanghai"}, Destination: schedule.Destination{Kind: schedule.NewSession}, CreatedAt: 1},
		},
		Runtime: map[string]*schedule.Runtime{"s1": {ScheduleID: "s1", MachineID: "m1", NextScheduledAt: next}},
	}
	return a
}

func TestSchedulesPage(t *testing.T) {
	a := schedFixture()
	tt := snap(t, a, "schedules", false, nil)
	for _, want := range []string{"Schedules", "Scheduled tasks", "Daily summary", "Every day at 9:00 AM", "Claude Code", "Studio",
		"New chat each run · lody-mygo", "Release notes", "Run manually", "One chat for this task · Not created yet",
		"Weekly report", "Every Mon & Fri at 5:30 PM", "Paused", "Results go to"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	if !tt.HasText("Runs tomorrow 9:00 AM") && !tt.HasText("Runs "+upcomingText(time.UnixMilli(a.sched.reg.Runtime["s1"].NextScheduledAt), time.Now(), loadZone("Asia/Shanghai"))) {
		t.Errorf("no next run in %q", tt.Texts())
	}

	// Searching narrows the list.
	a.sched.query = "weekly"
	tt.Frame()
	if tt.HasText("Daily summary") || !tt.HasText("Weekly report") {
		t.Errorf("search: %q", tt.Texts())
	}
	a.sched.query = ""

	// New schedule opens the form: every day at 09:00, on the machine
	// New chat uses.
	if err := tt.Click("New schedule"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "schedule-new")
	f := a.sched.form
	if f == nil || !f.create || f.machine != "m1" || f.agent != "a1" || f.rec.Kind != "daily" || f.rec.Hour != 9 {
		t.Fatalf("form %+v", f)
	}
	for _, want := range []string{"Save schedule", "Send to", "New chat each run", "Trigger", "On a schedule", "Repeat", "Every day", "At", "Next runs", "Studio time"} {
		if !tt.HasText(want) {
			t.Errorf("form: missing %q in %q", want, tt.Texts())
		}
	}
	// Weekly shows the days.
	f.rec.Kind, f.rec.Weekdays, f.recEdited = "weekly", []int{1}, true
	tt.Frame()
	if !tt.HasText("On") {
		t.Errorf("weekly: %q", tt.Texts())
	}
	// Saving blank marks what's missing.
	a.saveSchedule()
	tt.Frame()
	if !f.attempted || !tt.HasText("Fix the marked items to save.") {
		t.Errorf("blank save: %q", tt.Texts())
	}
}

func TestScheduleDetail(t *testing.T) {
	a := schedFixture()
	a.catalog.Sessions = append(a.catalog.Sessions,
		catalogSession("r1", "m1", "Summary for Oct 28", "s1", "2026-10-28T01:00:00Z"),
		catalogSession("r2", "m1", "Summary for Oct 29", "s1", "2026-10-29T01:00:00Z"))
	tt := snap(t, a, "schedules", false, nil)
	// Its history: the chats its runs started, newest first.
	a.sched.open, a.sched.modal, a.sched.tab = "s1", true, "history"
	a.sched.form = &schedForm{id: "s1", owner: "u1", title: "Daily summary", enabled: true, machine: "m1", agent: "a1"}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "schedule-history")
	for _, want := range []string{"Scheduled task", "Daily summary", "Enabled", "Settings", "Run history", "2 chats", "Summary for Oct 29", "Summary for Oct 28"} {
		if !tt.HasText(want) {
			t.Errorf("history: missing %q in %q", want, tt.Texts())
		}
	}
}

// Saving a new schedule writes its document and its Registry row, here
// through a daemon's data plane.
func TestScheduleSave(t *testing.T) {
	d := localplanetest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lc, err := localplane.Dial(ctx, d.Path, "w1")
	if err != nil {
		t.Fatal(err)
	}
	defer lc.Close()
	a := schedFixture()
	a.sched.reg = schedule.Registry{}
	pump := pumpUpdates(t, a)
	a.startSchedules("w1", lc)
	defer a.stopSchedules()
	pump("the registry", func() bool { return a.sched.ready })

	a.newSchedule()
	f := a.sched.form
	f.title, f.prompt = "Nightly triage", "Triage new issues"
	f.rec = schedule.Recurrence{Kind: "weekdays", Hour: 8, Minute: 30}
	f.recEdited = true
	a.models["sched"] = "opus"
	a.saveSchedule()
	pump("the saved row", func() bool { return a.sched.reg.Row(f.id) != nil })
	row := a.sched.reg.Row(f.id)
	if row.Title != "Nightly triage" || row.Trigger.Expression != "30 8 * * 1-5" || row.Trigger.TimeZone != "Asia/Shanghai" || row.OwnerID != "u1" || row.AgentConfigID != "a1" {
		t.Fatalf("row %+v", row)
	}
	d.WaitUpdates(t, schedule.DocRoom(f.id), 1)
	pump("the form saved", func() bool { return !a.sched.saving && !f.create })
}

func catalogSession(id, machine, title, scheduleID, created string) catalog.Session {
	return catalog.Session{ID: id, MachineID: machine, Title: title, Status: "idle", ScheduleID: scheduleID, CreatedAt: created, ProjectID: machine + ":unassigned"}
}

// A schedule that posts into one chat lists its runs from that chat.
func TestScheduleChatRuns(t *testing.T) {
	a := schedFixture()
	chat := schedule.OwnSessionID("s2", 0)
	a.catalog.Sessions = append(a.catalog.Sessions, catalogSession(chat, "m1", "Release notes chat", "", "2026-10-20T01:00:00Z"))
	a.selected = chat
	a.view = &session.View{Status: "live", Entries: []session.Entry{
		{ID: "u1", Role: "user", Timestamp: "2026-10-27T01:00:00Z", ScheduleRun: &session.ScheduleRun{ScheduleID: "s2"}},
		{ID: "r1", Role: "assistant", UserTurnID: "u1", Finished: true, EndedAt: 5},
		{ID: "u2", Role: "user", Timestamp: "2026-10-28T01:00:00Z"},
		{ID: "u3", Role: "user", Timestamp: "2026-10-29T01:00:00Z", ScheduleRun: &session.ScheduleRun{ScheduleID: "s2"}},
		{ID: "r3", Role: "assistant", UserTurnID: "u3"},
	}}
	a.page = "schedules"
	tt := snap(t, a, "schedules-chat-runs", false, nil)
	a.sched.open, a.sched.modal, a.sched.tab = "s2", true, "history"
	a.sched.form = &schedForm{id: "s2", owner: "u1", title: "Release notes", enabled: true, machine: "m1", agent: "a1"}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "schedule-chat-runs")
	for _, want := range []string{"Every run goes to", "Release notes chat", "2 runs in this chat", "Run 2", "Run 1", "Run now"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	runs := chatRuns(a.view, "s2")
	if len(runs) != 2 || runs[0].ID != "u3" || runState(a.view, "u3") != "running" || runState(a.view, "u1") != "done" {
		t.Fatalf("runs %+v", runs)
	}
}
