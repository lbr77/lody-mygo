package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// A conversation on its way is Lody's skeleton, three exchanges breathing,
// not a spinner.
func TestConversationSkeleton(t *testing.T) {
	a := fixture()
	a.catalog.Sessions[0].Status = "idle" // no working mark in its tab
	a.view = &session.View{Status: "syncing"}
	tt := snap(t, a, "conversation-skeleton", false, nil)
	if !tt.HasText(locales.LoadingConversation()) {
		t.Fatalf("skeleton not labeled: %q", tt.Texts())
	}
	if r, ok := tt.Find(locales.Loading()); ok {
		t.Fatalf("a spinner shows while the conversation loads: %+v", r)
	}
}

// The agent's plan leads its turn as Lody's plan bar: the step under way
// and the count, folding its steps open over 180ms.
func TestPlanBar(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.view.Entries = a.view.Entries[:2]
	items := a.view.Entries[1].Items
	a.view.Entries[1].Items = append([]session.Item{{ID: "pl", Type: "plan", Plan: []session.PlanEntry{
		{Content: "Read the iOS client", Status: "completed"},
		{Content: "Port the runtime", Status: "in_progress"},
		{Content: "Wire the stream", Status: "pending"},
	}}}, items...)
	tt := snap(t, a, "plan-bar", false, nil)
	head := locales.PlanStatusInProgress() + ": Port the runtime"
	if !tt.HasText(head) || !tt.HasText("1/3") || tt.HasText("Wire the stream") {
		t.Fatalf("plan bar: %q", tt.Texts())
	}
	if err := tt.Click(head); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(90 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Wire the stream") {
		t.Fatal("steps not unfolding")
	}
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "plan-bar-open")
	if !a.expanded["r1/plan-bar"] {
		t.Fatalf("open state: %v", a.expanded)
	}
}

// A tool step's verb, apart from what it acted on, shimmers while it runs;
// a step whose words carry no tense spins Lody's loader instead, and none
// wears a chevron.
func TestToolStepRow(t *testing.T) {
	a := fixture()
	a.view.Entries = a.view.Entries[:3]
	a.view.Entries = append(a.view.Entries, session.Entry{ID: "r2", Role: "assistant", UserTurnID: "u2", Items: []session.Item{
		{ID: "c4", Type: "tool_call", Kind: "execute", Title: "go run . --workspace w1", Status: "in_progress"},
		{ID: "c6", Type: "tool_call", Kind: "other", Title: "mcp__linear__list_issues", Status: "in_progress"},
	}})
	key := "r2/c4"
	a.expanded[key] = true
	tt := snap(t, a, "tool-step", false, nil)
	if !tt.HasText(locales.VerbRunning()) || !tt.HasText("go run . --workspace w1") {
		t.Fatalf("verb and title: %q", tt.Texts())
	}
	if _, ok := tt.Find(locales.VerbRunning() + " go run . --workspace w1"); !ok {
		t.Fatal("the step is not named by its sentence")
	}
	if _, ok := tt.Find(locales.Loading()); !ok {
		t.Fatal("no loader for a step without a verb")
	}
	if verb, rest := toolVerbParts(session.Item{Kind: "read", Title: "Read sync.go", Status: "completed"}, "Read sync.go"); verb != "Read" || rest != "sync.go" {
		t.Fatalf("title's own verb: %q %q", verb, rest)
	}
}

// Closing the right panel hides its content at once while its room
// shrinks away; closing the terminal dock drops its content at once.
func TestPanelAndDockClose(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.panelOpen = true
	tt := snap(t, a, "panel-close-before", false, nil)
	if !tt.HasText("Open a panel") {
		t.Fatalf("panel not open: %q", tt.Texts())
	}
	a.panelOpen = false
	tt.Frame()
	if tt.HasText("Open a panel") {
		t.Fatal("the panel's content stays as it closes")
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
}

// Sending, the message's loader shows beside its bubble only once the
// send has taken 500ms.
func TestSendingSpinner(t *testing.T) {
	a := fixture()
	a.catalog.Sessions[0].Status = "idle" // no working mark in its tab
	a.view.Working = false
	a.view.Entries = a.view.Entries[:2]
	a.view.Entries = append(a.view.Entries, session.Entry{ID: "u3", Role: "user", Status: "pending", Timestamp: "2026-10-29T09:30:00Z",
		Items: []session.Item{{ID: "t", Type: "text", Text: "And the deltas?"}}})
	a.sending, a.sendingID = true, a.selected
	if !a.sendingShown(a.view.Entries[2]) || a.sendingShown(a.view.Entries[0]) {
		t.Fatal("the message on its way is the last from the user")
	}
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find(locales.Loading()); !ok {
		t.Fatalf("no sending loader: %q", tt.Texts())
	}
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "sending-spinner")
	a.sending = false
	tt.Frame()
	if r, ok := tt.Find(locales.Loading()); ok {
		t.Fatalf("the loader stays once sent: %+v", r)
	}
}

// A search result glides to the top of the conversation rather than
// jumping there.
func TestFindGlides(t *testing.T) {
	a := fixture()
	a.view.Working = false
	long := strings.Repeat("Filler paragraph about the runtime.\n\n", 40)
	a.view.Entries[1].Items = append(a.view.Entries[1].Items, session.Item{ID: "x2", Type: "text", Text: long})
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	tt.Frame()
	a.openFind()
	a.find.query = "without a web view" // the first message, far above
	a.find.reveal = 3
	tt.Frame()
	tt.Frame()
	tt.Frame()
	st := a.chatMotion.scroll
	if st == nil {
		t.Fatal("the conversation's scroll is not tracked")
	}
	from := st.Y
	tt.Advance(100 * time.Millisecond)
	tt.Frame()
	if mid := st.Y; mid >= from || mid <= 40 {
		t.Fatalf("no glide: at %v from %v", mid, from)
	}
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	tt.Frame()
	if st.Scrolling() || a.find.glide.on || st.Y > 40 {
		t.Fatalf("the glide outlasts 360ms, or ends away from the result: at %v", st.Y)
	}
}

// A message just sent glides to 100px below the top of the conversation,
// what came before it showing above, with room made under it; once the
// reply fills the view, the conversation follows its end again.
func TestSentMessageHeld(t *testing.T) {
	a := fixture()
	a.view.Working = false
	var long []session.Entry
	for i := 0; i < 12; i++ {
		id := fmt.Sprint("pad", i)
		long = append(long, session.Entry{ID: "u" + id, Role: "user", Items: []session.Item{{ID: "t", Type: "text", Text: "Question " + id}}},
			session.Entry{ID: "r" + id, Role: "assistant", UserTurnID: "u" + id, Finished: true, Items: []session.Item{{ID: "x", Type: "text", Text: strings.Repeat("A long answer line. ", 40)}}})
	}
	a.view.Entries = append(long, a.view.Entries...)
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()

	// Sent, as send does.
	a.view.Entries = append(a.view.Entries, session.Entry{ID: "u-new", Role: "user", Status: "pending",
		Items: []session.Item{{ID: "t", Type: "text", Text: "Brand new question"}}})
	a.sending, a.sendingID = true, a.selected
	a.chatList.ScrollToEnd()
	tt.Frame()
	tt.Frame()
	s := &a.chatMotion.sent
	if !s.held || s.id != "u-new" {
		t.Fatalf("not held: %+v", *s)
	}
	tt.Advance(100 * time.Millisecond)
	tt.Frame()
	mid := s.top
	tt.Advance(500 * time.Millisecond)
	tt.Frame()
	tt.Frame()
	target := a.outline.top + chatPadTop + sentContext
	if abs32(s.top-target) > 1 {
		t.Fatalf("held at %v, want %v", s.top, target)
	}
	if mid <= target+1 {
		t.Fatalf("didn't glide there: at %v after 100ms", mid)
	}
	if s.room <= 0 || a.chatList.FollowEnd {
		t.Fatalf("no room under it, or following: room %v follow %v", s.room, a.chatList.FollowEnd)
	}
	snapFrame(t, tt, "sent-held")

	// The reply streams in under it, the message staying put.
	reply := session.Entry{ID: "r-new", Role: "assistant", UserTurnID: "u-new", Items: []session.Item{{ID: "x", Type: "text", Text: "Short start."}}}
	a.sending = false
	a.view.Entries = append(a.view.Entries, reply)
	tt.Frame()
	tt.Frame()
	tt.Frame()
	if !s.held || abs32(s.top-target) > 1 {
		t.Fatalf("a short reply moved it: held %v at %v", s.held, s.top)
	}
	// Once it fills the view, the end is followed.
	a.view.Entries[len(a.view.Entries)-1].Items[0].Text = strings.Repeat("A long streamed line of the reply. ", 120)
	for range 4 {
		tt.Frame()
	}
	if s.held || s.room != 0 || !a.chatList.FollowEnd || !a.chatList.AtEnd() {
		t.Fatalf("not following once the reply filled the view: %+v follow %v end %v", *s, a.chatList.FollowEnd, a.chatList.AtEnd())
	}
}

// Scrolling lets a message just sent go; the room under it shrinks to
// what keeps the reader in place, and once the reader is back at the end
// it is gone and the end followed.
func TestSentMessageLetGo(t *testing.T) {
	a := fixture()
	a.view.Working = false
	var long []session.Entry
	for i := 0; i < 12; i++ {
		id := fmt.Sprint("pad", i)
		long = append(long, session.Entry{ID: "u" + id, Role: "user", Items: []session.Item{{ID: "t", Type: "text", Text: "Question " + id}}},
			session.Entry{ID: "r" + id, Role: "assistant", UserTurnID: "u" + id, Finished: true, Items: []session.Item{{ID: "x", Type: "text", Text: strings.Repeat("A long answer line. ", 40)}}})
	}
	a.view.Entries = append(long, a.view.Entries...)
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	a.view.Entries = append(a.view.Entries, session.Entry{ID: "u-new", Role: "user", Status: "pending",
		Items: []session.Item{{ID: "t", Type: "text", Text: "Brand new question"}}})
	a.sending, a.sendingID = true, a.selected
	a.chatList.ScrollToEnd()
	tt.Frame()
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	s := &a.chatMotion.sent
	room := s.room
	if !s.held || room <= 0 {
		t.Fatalf("not held: %+v", *s)
	}
	tt.Move(600, 300)
	tt.Scroll(600, 300, 0, -200) // up, toward what came before
	tt.Frame()
	tt.Frame()
	if s.held {
		t.Fatal("still held after scrolling")
	}
	if want := max(0, room-200); abs32(s.room-want) > 1 {
		t.Fatalf("room %v after scrolling 200 up from %v, want %v", s.room, room, want)
	}
	// The room stays until the reply takes it, the reader staying where
	// they are, as Lody's reading place; scrolling to the end then follows
	// it again.
	a.view.Entries = append(a.view.Entries, session.Entry{ID: "r-new", Role: "assistant", UserTurnID: "u-new",
		Items: []session.Item{{ID: "x", Type: "text", Text: strings.Repeat("A long streamed line of the reply. ", 120)}}})
	for range 4 {
		tt.Frame()
	}
	if s.room != 0 || !a.chatList.FollowEnd {
		t.Fatalf("the room stayed once the reply took it: %+v follow %v", *s, a.chatList.FollowEnd)
	}
	for range 10 {
		tt.Scroll(600, 300, 0, 2000)
		tt.Frame()
	}
	tt.Advance(time.Second)
	tt.Frame()
	if !a.chatList.AtEnd() {
		t.Fatal("not at the end after scrolling there")
	}
	a.view.Entries[len(a.view.Entries)-1].Items[0].Text += strings.Repeat(" More of it.", 200)
	for range 4 {
		tt.Frame()
	}
	if !a.chatList.AtEnd() {
		t.Fatal("the end isn't followed after scrolling back to it")
	}
}

// A search result the list hasn't built glides in from the side it lies
// on: one far above comes down into view, one far below rises.
func TestFindGlidesFromAfar(t *testing.T) {
	a := fixture()
	a.view.Working = false
	var long []session.Entry
	for i := 0; i < 12; i++ {
		id := fmt.Sprint("pad", i)
		long = append(long, session.Entry{ID: "u" + id, Role: "user", Items: []session.Item{{ID: "t", Type: "text", Text: "Question " + id}}},
			session.Entry{ID: "r" + id, Role: "assistant", UserTurnID: "u" + id, Finished: true, Items: []session.Item{{ID: "x", Type: "text", Text: strings.Repeat("A long answer line. ", 40)}}})
	}
	a.view.Entries = append(long, a.view.Entries...)
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	// seek shows the result for query, the first of its row, and reports
	// the scroll offsets along the way, every 30ms, and where the row ends.
	seek := func(query string, row int) (ys []float32, end float32) {
		t.Helper()
		a.openFind()
		a.find.query = query
		a.find.active = 0
		a.find.reveal = 3
		tt.Frame()
		if !a.find.glide.on {
			t.Fatalf("%q: no glide from afar", query)
		}
		st := a.chatMotion.scroll
		for range 14 {
			ys = append(ys, st.Y)
			tt.Advance(30 * time.Millisecond)
			tt.Frame()
		}
		tt.Advance(200 * time.Millisecond)
		tt.Frame()
		tt.Frame()
		if a.find.glide.on {
			t.Fatalf("%q: still gliding after 600ms", query)
		}
		return ys, a.outline.tops[row]
	}
	// The block holding the result ends at the top; its row's meta line
	// and spacing sit above it.
	top := func() float32 { return a.outline.top + chatPadTop }
	near := func(y float32) bool { return y <= top()+40 && y >= top()-100 }
	// The whole way in one direction, most of it early, as an ease-out.
	glides := func(ys []float32, up bool) bool {
		steps := 0
		for i := 1; i < len(ys); i++ {
			d := ys[i] - ys[i-1]
			if up && d > 0.5 || !up && d < -0.5 {
				return false
			}
			if abs32(d) > 0.5 {
				steps++
			}
		}
		total := abs32(ys[len(ys)-1] - ys[0])
		third := abs32(ys[4] - ys[0]) // 120ms in
		return steps >= 5 && total > 1000 && third > total/2
	}

	ys, end := seek("Question pad0", 0)
	if !glides(ys, true) || !near(end) {
		t.Fatalf("from above: offsets %v, row at %v; want a glide up the conversation to about %v", ys, end, top())
	}
	a.find.open = false
	ys, end = seek("Question pad11", 22)
	if !glides(ys, false) || !near(end) {
		t.Fatalf("from below: offsets %v, row at %v; want a glide down the conversation to about %v", ys, end, top())
	}
}
