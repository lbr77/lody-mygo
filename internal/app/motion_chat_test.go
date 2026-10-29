package app

import (
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
	if !st.Scrolling() {
		t.Fatalf("no glide: at %v of %v", st.Y, st.MaxY)
	}
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	tt.Frame()
	if st.Scrolling() || st.Y > 40 {
		t.Fatalf("the glide outlasts 360ms, or ends away from the result: at %v", st.Y)
	}
}
