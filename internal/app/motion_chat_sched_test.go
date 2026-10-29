package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
)

// An archive group's header folds its rows away, and unfolds them, as
// Lody's.
func TestArchiveGroupFold(t *testing.T) {
	a := fixture()
	a.page = "archive"
	a.catalog.Sessions = append(a.catalog.Sessions, catalog.Session{ID: "old", MachineID: "m1", Title: "Old experiment", Archived: true, ProjectID: "m1:local:p2", CreatedAt: "2026-09-01T00:00:00Z"})
	tt := snap(t, a, "archive-group", false, nil)
	if !tt.HasText("Old experiment") {
		t.Fatalf("no row: %q", tt.Texts())
	}
	r, ok := tt.Find("/src/website")
	if !ok {
		t.Fatalf("no header: %q", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "archive-group-hover")
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "archive-group-folded")
	if tt.HasText("Old experiment") {
		t.Fatalf("folded group still shows its rows: %q", tt.Texts())
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if !tt.HasText("Old experiment") {
		t.Fatalf("unfolded group shows no rows: %q", tt.Texts())
	}
}

// The share-image segments pick, their ink and raised fill moving to the
// choice over 120ms.
func TestShareTrackFades(t *testing.T) {
	st := &shareImage{}
	selected := "a"
	opts := []option{{"a", "Light"}, {"b", "Dark"}}
	tt := ui.NewTester(func(c *ui.Context) {
		p := applyTheme(c)
		st.shareTrack(c, p, "theme", opts, selected, 56, false, func(id string) { selected = id })
	}, 300, 100)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if selected != "b" {
		t.Fatalf("picked %q", selected)
	}
	// Halfway, the old choice's ink is still on its way back.
	tt.Advance(60 * time.Millisecond)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Dark") || !tt.HasText("Light") {
		t.Fatalf("segments: %q", tt.Texts())
	}
}
