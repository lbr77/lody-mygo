package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/lbr77/lody-mygo/internal/session"
)

const tableReply = "## Results\n\nThe runs, by **suite**:\n\n| Suite | Passed | Note |\n| --- | ---: | --- |\n| `app` | 412 | fast \\| cached |\n| session | 38 | *flaky* once |\n\n### Next\n\nShip it.\n\n| A | B |\n|---|---|\n| 1 | 2 |"

// A reply's tables stand on their own, spaced as the one view would space
// them, and copy as GitHub's Markdown, as Lody's do.
func TestTableStretches(t *testing.T) {
	segs := splitMarkdownTables(tableReply)
	if len(segs) != 4 || !segs[1].table || !segs[3].table || segs[0].first != 2 || segs[2].first != 3 {
		t.Fatalf("stretches: %+v", segs)
	}
	want := "| Suite | Passed | Note |\n| --- | --- | --- |\n| app | 412 | fast \\| cached |\n| session | 38 | flaky once |"
	if segs[1].copy != want {
		t.Fatalf("copied as\n%s\nwant\n%s", segs[1].copy, want)
	}
	if splitMarkdownTables("No tables here.") != nil || splitMarkdownTables("| a |\n|---|\n| 1 |\n\n[x]: https://lody.ai") != nil {
		t.Fatal("split what needn't or mustn't be")
	}

	a := fixture()
	var whole, split, frame, col ui.Rect
	tt := ui.NewTester(func(c *ui.Context) {
		p := applyTheme(c)
		a.syncMarkdown(p)
		ui.Row(c).Fill().AlignItems(ui.Start).Gap(20).Children(func() {
			whole = markdown.View(c.Key("whole"), tableReply, &a.md).Width(480).Bounds()
			s := ui.Column(c.Key("split")).Width(480)
			s.Children(func() {
				el := a.segmentedMarkdown(c, p, "r", segs)
				col = el.Bounds()
			})
			split = s.Bounds()
		})
		_ = frame
	}, 1100, 900)
	tt.Frame()
	tt.Frame()
	if whole.H == 0 || abs32(whole.H-split.H) > 1 {
		t.Fatalf("one view is %v tall, the stretches %v", whole.H, split.H)
	}
	if col.W != 480 {
		t.Fatalf("stretches %v wide", col.W)
	}
}

// Over a table, its copy button fades in, copies it, and says so for 1.5s.
func TestTableCopy(t *testing.T) {
	var copied string
	old := writeClipboard
	writeClipboard = func(s string) { copied = s }
	defer func() { writeClipboard = old }()
	a := fixture()
	a.view.Working = false
	a.view.Entries = a.view.Entries[:2]
	a.view.Entries[1].Items = []session.Item{{ID: "t", Type: "text", Text: tableReply}}
	tt := ui.NewTester(a.render, 1100, 900)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	r, ok := tt.Find("Copy table")
	if !ok {
		t.Fatalf("no copy button: %q", tt.Texts())
	}
	tt.Move(r.X-40, r.Y+30)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Copy table"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if copied != splitMarkdownTables(tableReply)[1].copy {
		t.Fatalf("copied %q", copied)
	}
	if _, ok := tt.Find("Copied"); !ok {
		t.Fatal("doesn't say it copied")
	}
	tt.Advance(1600 * time.Millisecond)
	tt.Frame()
	if _, ok := tt.Find("Copied"); ok {
		t.Fatal("still says copied after 1.5s")
	}
}

// With a project chosen on Home, the pointer on its pill swaps the folder
// for Lody's clear button, which leaves the project.
func TestHomeProjectClear(t *testing.T) {
	a := fixture()
	a.selected = ""
	a.newChat.machine, a.newChat.project = "m1", "m1:local:p1"
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	r, ok := tt.Find("Project")
	if !ok {
		t.Fatalf("no project pill: %q", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Don't work in a project"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.newChat.project != "" {
		t.Fatalf("still in %q", a.newChat.project)
	}
	if _, ok := tt.Find("Don't work in a project"); ok {
		t.Fatal("a clear button with nothing to clear")
	}
}
