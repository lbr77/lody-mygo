package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
)

// Archived sessions are chosen several at a time as Lody's: a row's check
// box starts it, a click on a row then chooses it, a group's header checks
// it all, and the title bar says how many, with Delete asking first.
func TestArchiveMultiSelect(t *testing.T) {
	a := fixture()
	for _, s := range []catalog.Session{
		{ID: "o1", MachineID: "m1", Title: "Old experiment", Archived: true, ProjectID: "m1:local:p2", CreatedAt: "2026-09-01T00:00:00Z"},
		{ID: "o2", MachineID: "m1", Title: "Older probe", Archived: true, ProjectID: "m1:local:p2", CreatedAt: "2026-08-01T00:00:00Z"},
		{ID: "o3", MachineID: "m1", Title: "Loose chat", Archived: true, CreatedAt: "2026-07-01T00:00:00Z"},
	} {
		a.catalog.Sessions = append(a.catalog.Sessions, s)
	}
	a.page = "archive"
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	r, ok := tt.Find("Old experiment")
	if !ok {
		t.Fatalf("no row: %q", tt.Texts())
	}
	tt.Move(r.X+40, r.Y+r.H/2)
	tt.Frame()
	if err := tt.Click("Select Old experiment"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.archivePick["o1"] || !tt.HasText("1 selected") || a.page != "archive" {
		t.Fatalf("not choosing: %v %q", a.archivePick, tt.Texts())
	}
	// A click on a row now chooses it rather than opening it.
	if err := tt.Click("Loose chat"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.archivePick["o3"] || !tt.HasText("2 selected") || a.selected == "o3" {
		t.Fatalf("a row click: %v selected %q", a.archivePick, a.selected)
	}
	// The project's header checks both its rows.
	if err := tt.Click("Select all in website"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.archivePick) != 3 || !tt.HasText("3 selected") {
		t.Fatalf("group: %v", a.archivePick)
	}
	if err := tt.Click("Select all in website"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.archivePick) != 1 || !a.archivePick["o3"] {
		t.Fatalf("group again: %v", a.archivePick)
	}
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !a.archiveBulkOpen || !tt.HasText("Delete 1 sessions permanently?") || !tt.HasText("This will permanently delete the selected sessions.") {
		t.Fatalf("no confirmation: %q", tt.Texts())
	}
	a.archiveBulkOpen = false
	tt.Frame()
	if err := tt.Click("Exit selection"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.archivePicking() || tt.HasText("1 selected") {
		t.Fatal("still choosing")
	}
	// What leaves the archive leaves the choice.
	a.togglePick("o1")
	a.catalog.Sessions[len(a.catalog.Sessions)-3].Archived = false
	tt.Frame()
	if a.archivePicking() {
		t.Fatalf("restored but chosen: %v", a.archivePick)
	}
}
