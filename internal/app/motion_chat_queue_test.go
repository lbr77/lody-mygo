package app

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// queued is a message waiting in the queue.
func queued(id, text string) session.Entry {
	return session.Entry{ID: id, Role: "user", Status: "queued", Queued: true, Items: []session.Item{{ID: "t", Type: "text", Text: text}}}
}

// queueOps records what a sheet's rows ask for, applying it to its items
// at once, as a local write lands.
type queueOps struct {
	items []session.Entry
	calls []string
	stale bool // a cancel lands before the queue says so
}

func (q *queueOps) props() queueProps {
	find := func(id string) int { return indexOf(q.items, id) }
	return queueProps{
		items:     q.items,
		showSteer: true,
		onSteer:   func(id string) { q.calls = append(q.calls, "steer "+id) },
		onRemove: func(id string) {
			q.calls = append(q.calls, "remove "+id)
			if i := find(id); i >= 0 {
				q.items = append(q.items[:i:i], q.items[i+1:]...)
			}
		},
		onReorder: func(id, over string, done func(error)) {
			q.calls = append(q.calls, "reorder "+id+" "+over)
			ids := arrayMove(queueIDs(q.items), find(id), find(over))
			byID := map[string]session.Entry{}
			for _, e := range q.items {
				byID[e.ID] = e
			}
			q.items = nil
			for _, id := range ids {
				q.items = append(q.items, byID[id])
			}
			done(nil)
		},
		onEditStart: func(id string, done func(error)) {
			q.calls = append(q.calls, "editing "+id)
			q.items[find(id)].Editing = true
			done(nil)
		},
		onEditCancel: func(id string, done func(error)) {
			q.calls = append(q.calls, "cancel "+id)
			q.items[find(id)].Editing = q.stale && q.items[find(id)].Editing
			if done != nil {
				done(nil)
			}
		},
		onEditSave: func(id, text string, done func(error)) {
			q.calls = append(q.calls, "save "+id+" "+text)
			e := &q.items[find(id)]
			e.Editing, e.Items = false, []session.Item{{ID: "t", Type: "text", Text: text}}
			done(nil)
		},
	}
}

// queueTester draws a sheet at the bottom of a window, as on the info bar.
func queueTester(q *queueOps, st *queueView, w float32) *ui.Tester {
	a := fixture()
	tt := ui.NewTester(func(c *ui.Context) {
		p := applyTheme(c)
		ui.Column(c).Fill().Justify(ui.End).Padding(20).Background(p.Canvas).Children(func() {
			a.queuePanel(c, p, st, q.props())
		})
	}, int(w), 420)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	return tt
}

// The queue waits on the info bar, not in the conversation: "Up next" and
// how many, the messages numbered, Steer on the first alone while the agent
// can't take one mid-turn, and edit and remove on each.
func TestMessageQueueSheet(t *testing.T) {
	a := fixture()
	a.view.Entries[3].Items[1].Permission = nil
	a.view.Queue = []session.Entry{queued("q1", "Write the tests"), queued("q2", "Fix the build"), queued("q3", "Ship it")}
	tt := snap(t, a, "queue-sheet", false, nil)
	for _, want := range []string{"Up next", "3 queued", "Write the tests", "Fix the build", "Ship it", "1", "2", "3"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	steers := 0
	for _, s := range tt.Texts() {
		if s == "Steer" {
			steers++
		}
	}
	if steers != 1 {
		t.Errorf("%d Steer buttons, want the first row's alone", steers)
	}
	if _, ok := tt.Find(locales.MqEditMessage()); !ok {
		t.Error("no edit action")
	}
	if _, ok := tt.Find(locales.MqRemove()); !ok {
		t.Error("no remove action")
	}
	// The sheet sits right on the bar, over the composer: its last row
	// ends its padding, the bar's 32 and the 8 under it above the field.
	last, _ := tt.Find("Ship it")
	field, _ := tt.Find(locales.ComposerLabel())
	if gap := field.Y - (last.Y + last.H); gap < 50 || gap > 64 {
		t.Errorf("the sheet's last row ends %v above the composer's field", gap)
	}
	// Steering natively, every row can.
	a.catalog.Sessions[0].AgentConfigID = "a1"
	a.catalog.Capabilities = map[string]catalog.Capability{"a1": {Steer: true}}
	tt.Frame()
	steers = 0
	for _, s := range tt.Texts() {
		if s == "Steer" {
			steers++
		}
	}
	if steers != 3 {
		t.Errorf("%d Steer buttons with native steering, want 3", steers)
	}
}

// The header folds the rows away, their height shrinking over 180ms as
// the chevron turns, and back.
func TestMessageQueueFold(t *testing.T) {
	q := &queueOps{items: []session.Entry{queued("q1", "Write the tests"), queued("q2", "Fix the build")}}
	st := &queueView{}
	tt := queueTester(q, st, 720)
	head, ok := tt.Find("Up next")
	if !ok || !tt.HasText("2 queued") {
		t.Fatalf("no header: %q", tt.Texts())
	}
	open := head.Y
	if err := tt.Click("Up next"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(60 * time.Millisecond)
	tt.Frame()
	mid, _ := tt.Find("Up next")
	if !st.folded || mid.Y <= open {
		t.Fatalf("not folding: header at %v from %v", mid.Y, open)
	}
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	tt.Frame()
	shut, _ := tt.Find("Up next")
	if tt.HasText("Write the tests") || shut.Y <= mid.Y {
		t.Fatalf("folded rows still show, header at %v: %q", shut.Y, tt.Texts())
	}
	if !tt.HasText("2 queued") {
		t.Fatal("the count goes with the rows")
	}
	if err := tt.Click("Up next"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Write the tests") {
		t.Fatal("unfolding shows no rows")
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if back, _ := tt.Find("Up next"); back.Y != open {
		t.Fatalf("unfolded header at %v, was %v", back.Y, open)
	}
}

// Dragged by its grip, a row follows the pointer while the one it passes
// slides aside over 250ms; dropped, it takes its new place and is moved
// there.
func TestMessageQueueDrag(t *testing.T) {
	q := &queueOps{items: []session.Entry{queued("q1", "Write the tests"), queued("q2", "Fix the build"), queued("q3", "Ship it")}}
	st := &queueView{}
	tt := queueTester(q, st, 720)
	first, _ := tt.Find("Write the tests")
	second, _ := tt.Find("Fix the build")
	grip, ok := tt.Find("1")
	if !ok {
		t.Fatalf("no number: %q", tt.Texts())
	}
	x, y := grip.X+grip.W/2, grip.Y+grip.H/2
	tt.Move(x, y)
	tt.Frame()
	tt.Press(x, y)
	tt.Frame()
	// Past dnd-kit's 6 DIPs, over the second row's center.
	step := second.Y - first.Y
	tt.Move(x, y+step*0.8)
	tt.Frame()
	tt.Frame()
	if !st.drag.active || st.drag.over != 1 {
		t.Fatalf("drag %+v", st.drag)
	}
	tt.Advance(100 * time.Millisecond)
	tt.Frame()
	moving, _ := tt.Find("Fix the build")
	if moving.Y >= second.Y || moving.Y <= first.Y {
		t.Fatalf("the passed row isn't sliding: at %v, from %v to %v", moving.Y, second.Y, first.Y)
	}
	dragged, _ := tt.Find("Write the tests")
	if d := dragged.Y - first.Y; d < step*0.7 || d > step*0.9 {
		t.Fatalf("the dragged row is %v from its place, the pointer %v", d, step*0.8)
	}
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	if aside, _ := tt.Find("Fix the build"); aside.Y != first.Y {
		t.Fatalf("the passed row rests at %v, not %v", aside.Y, first.Y)
	}
	snapFrame(t, tt, "queue-drag")

	tt.Release(x, y+step*0.8)
	tt.Frame()
	tt.Frame()
	if len(q.calls) != 1 || q.calls[0] != "reorder q1 q2" {
		t.Fatalf("calls %q", q.calls)
	}
	if ids := strings.Join(queueIDs(q.items), " "); ids != "q2 q1 q3" {
		t.Fatalf("order %s", ids)
	}
	// It glides the rest of the way into its new place.
	settling, _ := tt.Find("Write the tests")
	if settling.Y == second.Y {
		t.Fatal("the dropped row jumped into place")
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	tt.Frame()
	if placed, _ := tt.Find("Write the tests"); placed.Y != second.Y {
		t.Fatalf("dropped row at %v, its place %v", placed.Y, second.Y)
	}
	if moved, _ := tt.Find("Fix the build"); moved.Y != first.Y {
		t.Fatalf("passed row at %v, its place %v", moved.Y, first.Y)
	}
}

// Editing a row marks it, holding the queue; Enter saves a change, an
// unchanged one is given up, and Escape commits too.
func TestMessageQueueEdit(t *testing.T) {
	q := &queueOps{items: []session.Entry{queued("q1", "Write the tests"), queued("q2", "Fix the build")}}
	st := &queueView{}
	tt := queueTester(q, st, 720)
	if err := tt.Click(locales.MqEditMessage()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if st.edit.id != "q1" || st.edit.pending || len(q.calls) != 1 || q.calls[0] != "editing q1" {
		t.Fatalf("edit %+v, calls %q", st.edit, q.calls)
	}
	if _, ok := tt.Find(locales.MqSaveEdit()); !ok {
		t.Fatalf("no editor: %q", tt.Texts())
	}
	if !tt.Focused(locales.MqEditMessage()) {
		t.Fatal("the editor doesn't take the focus")
	}
	tt.Type(", then run them")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if st.edit.id != "" || q.calls[len(q.calls)-1] != "save q1 Write the tests, then run them" {
		t.Fatalf("edit %+v, calls %q", st.edit, q.calls)
	}
	tt.Frame()
	if !tt.HasText("Write the tests, then run them") {
		t.Fatalf("saved text not shown: %q", tt.Texts())
	}

	// Unchanged, Escape gives the edit up, and the queue saying so late
	// doesn't reopen it.
	if err := tt.Click(locales.MqEditMessage()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	q.stale = true
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Frame()
	if st.edit.id != "" || q.calls[len(q.calls)-1] != "cancel q1" {
		t.Fatalf("edit %+v, calls %q", st.edit, q.calls)
	}

	// A row edited elsewhere opens here too.
	q.items[0].Editing = false
	q.items[1].Editing = true
	tt.Frame()
	tt.Frame()
	if st.edit.id != "q2" || st.edit.text != "Fix the build" {
		t.Fatalf("not following an edit from elsewhere: %+v", st.edit)
	}
}

// Scheduled messages follow the queue in a section of their own, by send
// time; alone, they title the sheet. One that failed to go says so, in
// its row and, folded, in the header.
func TestMessageQueueScheduled(t *testing.T) {
	loc := time.UTC
	soon := time.Now().Add(2 * time.Hour).In(loc)
	later := soon.Add(time.Hour)
	q := &queueOps{items: []session.Entry{queued("q1", "Write the tests")}}
	st := &queueView{}
	var opened, cancelled string
	o := func() queueProps {
		props := q.props()
		props.scheduled = []scheduledItem{
			{id: "s2", title: "Ship the release notes", at: later, loc: loc, failed: true},
			{id: "s1", title: "Check the deploy", at: soon, loc: loc},
		}
		props.onOpenScheduled = func(s scheduledItem) { opened = s.id }
		props.onCancelScheduled = func(s scheduledItem) { cancelled = s.id }
		props.onEditScheduledTime = func(s scheduledItem) {}
		return props
	}
	a := fixture()
	tt := ui.NewTester(func(c *ui.Context) {
		p := applyTheme(c)
		ui.Column(c).Fill().Justify(ui.End).Padding(20).Children(func() { a.queuePanel(c, p, st, o()) })
	}, 720, 420)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "queue-scheduled")
	for _, want := range []string{"Up next", "1 queued · 2 scheduled", locales.MqScheduledTitle(), "Check the deploy", "Ship the release notes"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	first, _ := tt.Find("Check the deploy")
	second, _ := tt.Find("Ship the release notes")
	if first.Y >= second.Y {
		t.Error("scheduled messages aren't by send time")
	}
	failedTime := capitalize(upcomingText(later, time.Now(), loc))
	if _, ok := tt.Find(failedTime + " · " + locales.SmNotSent() + " · " + locales.SmEditTime()); !ok {
		t.Errorf("the failed one doesn't say so: %q", tt.Texts())
	}
	if err := tt.Click(locales.SmCancel()); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click(locales.MqEdit()); err != nil {
		t.Fatal(err)
	}
	if cancelled != "s1" || opened != "s1" {
		t.Fatalf("cancelled %q, opened %q", cancelled, opened)
	}
	if err := tt.Click("Up next"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !tt.HasText(locales.MqScheduledFailedCount(1)) {
		t.Fatalf("folded header hides the failure: %q", tt.Texts())
	}

	// Alone, they title the sheet, without a count.
	q.items = nil
	st.folded = false
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !tt.HasText(locales.MqScheduledTitle()) || tt.HasText("Up next") || tt.HasText("2 scheduled") {
		t.Fatalf("scheduled alone: %q", tt.Texts())
	}
}

// On a sheet narrower than 520, a row's actions fold into More, whose
// menu moves a row to the top.
func TestMessageQueueCompact(t *testing.T) {
	q := &queueOps{items: []session.Entry{queued("q1", "Write the tests"), queued("q2", "Fix the build")}}
	st := &queueView{}
	tt := queueTester(q, st, 440)
	tt.Frame()
	if !st.compact {
		t.Fatal("not compact")
	}
	if _, ok := tt.Find(locales.MqRemove()); ok {
		t.Fatal("inline actions on a narrow sheet")
	}
	// The second row's More, a row below the first's.
	r1, _ := tt.Find("Write the tests")
	r2, ok := tt.Find("Fix the build")
	more, ok2 := tt.Find(locales.MqMore())
	if !ok || !ok2 {
		t.Fatalf("no row or More: %q", tt.Texts())
	}
	tt.ClickAt(more.X+more.W/2, more.Y+more.H/2+r2.Y-r1.Y)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	for _, want := range []string{locales.MqMoveToTop(), locales.MqEdit(), locales.MqDelete()} {
		if !tt.HasText(want) {
			t.Errorf("menu lacks %q: %q", want, tt.Texts())
		}
	}
	// Steer is the first row's alone, without native steering.
	if tt.HasText(locales.Steer()) {
		t.Errorf("the second row's menu steers: %q", tt.Texts())
	}
	if err := tt.Click(locales.MqMoveToTop()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ids := strings.Join(queueIDs(q.items), " "); ids != "q2 q1" || q.calls[0] != "reorder q2 q1" {
		t.Fatalf("order %s, calls %q", ids, q.calls)
	}
}
