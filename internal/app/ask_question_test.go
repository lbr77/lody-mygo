package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/session"
)

// breakfast is two questions, any of then one of, in Claude's shape.
func breakfast() *session.AskQuestions {
	return &session.AskQuestions{Source: "claude", Version: 1, Questions: []session.AskQuestion{
		{Header: "Breakfast", Question: "Pick ingredients", MultiSelect: true, Options: []session.AskOption{{Label: "Eggs"}, {Label: "Toast", Description: "Sourdough, buttered"}}},
		{Header: "Drink", Question: "Pick a drink", Options: []session.AskOption{{Label: "Coffee"}, {Label: "Tea"}}},
	}}
}

// askFixture is the fixture with the agent asking qs where it asked for a
// permission.
func askFixture(qs *session.AskQuestions) *App {
	a := fixture()
	it := &a.view.Entries[3].Items[1]
	it.Kind, it.Title = "think", "AskUserQuestion"
	it.Permission = &session.Permission{RequestID: "q1", Pending: true, Question: true, Questions: qs,
		Options: []session.Option{{ID: "answer", Name: "Submit answers", Kind: "allow_once"}, {ID: "cancel", Name: "Cancel", Kind: "reject_once"}}}
	return a
}

// askSent swaps the responder for one that keeps what it is sent; recv
// waits for the answer to come back and returns it.
func askSent(t *testing.T, a *App) (recv func() map[string]any) {
	t.Helper()
	posted := make(chan func(), 4)
	a.post = func(fn func()) { posted <- fn }
	var got map[string]any
	old := askResponder
	askResponder = func(ctx context.Context, r *session.Replica, entryID, itemID, requestID string, outcome map[string]any) error {
		if entryID != "r2" || itemID != "c5" || requestID != "q1" {
			t.Errorf("sent to %s/%s/%s", entryID, itemID, requestID)
		}
		got = outcome
		return nil
	}
	t.Cleanup(func() { askResponder = old })
	return func() map[string]any {
		select {
		case fn := <-posted:
			fn()
		case <-time.After(5 * time.Second):
			t.Fatal("nothing was sent")
		}
		return got
	}
}

func click(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}

// dotWidth is how wide a pager dot is drawn.
func dotWidth(t *testing.T, tt *ui.Tester, n string) float32 {
	t.Helper()
	r, ok := tt.Find("Go to question " + n)
	if !ok {
		t.Fatalf("no dot %s in %q", n, tt.Texts())
	}
	return r.W
}

// The questions come a page at a time in the composer's place, and
// submit together, only from Submit, in the request's shape.
func TestAskQuestionPages(t *testing.T) {
	a := askFixture(breakfast())
	recv := askSent(t, a)
	tt := snap(t, a, "ask-question", false, nil)
	for _, want := range []string{"Pick ingredients", "1/2", "Eggs", "Toast", "Prev", "Next"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("Pick a drink") || tt.HasText("Submit") {
		t.Fatal("one page at a time, Submit on the last")
	}
	click(t, tt, "Next")
	if !tt.HasText("1/2") {
		t.Fatal("Next waits for an answer")
	}
	// Any of stays on its page.
	click(t, tt, "Eggs")
	if !tt.HasText("1/2") {
		t.Fatal("an any-of question stays")
	}
	click(t, tt, "Next")
	if !tt.HasText("2/2") || !tt.HasText("Pick a drink") || !tt.HasText("Submit") {
		t.Fatalf("page two: %q", tt.Texts())
	}
	click(t, tt, "Coffee")
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "ask-question-last")
	if a.view.Entries[3].Items[1].Permission.Pending != true || a.asks["live/q1"].sending != "" {
		t.Fatal("choosing the last answer submits nothing")
	}
	click(t, tt, "Submit")
	got, _ := json.Marshal(recv())
	want := `{"_meta":{"claudeCode":{"askUserQuestion":{"answers":{"Pick a drink":"Coffee","Pick ingredients":["Eggs"]}}}},"optionId":"answer","outcome":"selected"}`
	if string(got) != want {
		t.Fatalf("sent\n%s\nwant\n%s", got, want)
	}
}

// One of one moves on to the next question as it is chosen.
func TestAskQuestionAdvances(t *testing.T) {
	qs := breakfast()
	qs.Questions[0], qs.Questions[1] = qs.Questions[1], qs.Questions[0]
	a := askFixture(qs)
	tt := snap(t, a, "ask-advance", false, nil)
	click(t, tt, "Tea")
	if !tt.HasText("2/2") || !tt.HasText("Pick ingredients") {
		t.Fatalf("not on: %q", tt.Texts())
	}
	click(t, tt, "Prev")
	if !tt.HasText("1/2") {
		t.Fatal("Prev goes back")
	}
}

// The dots say where the pages are: the one shown 20 wide, one answered
// 10, one waiting 6, each changing over 180ms on ease.standard.
func TestAskQuestionDots(t *testing.T) {
	a := askFixture(breakfast())
	tt := snap(t, a, "ask-dots", false, nil)
	if w1, w2 := dotWidth(t, tt, "1"), dotWidth(t, tt, "2"); w1 != 20 || w2 != 6 {
		t.Fatalf("at rest %v %v", w1, w2)
	}
	click(t, tt, "Eggs")
	click(t, tt, "Next")
	tt.Advance(90 * time.Millisecond)
	tt.Frame()
	w1, w2 := dotWidth(t, tt, "1"), dotWidth(t, tt, "2")
	if !(w1 > 10 && w1 < 20) || !(w2 > 6 && w2 < 20) {
		t.Fatalf("halfway %v %v", w1, w2)
	}
	tt.Advance(100 * time.Millisecond)
	tt.Frame()
	if w1, w2 := dotWidth(t, tt, "1"), dotWidth(t, tt, "2"); w1 != 10 || w2 != 20 {
		t.Fatalf("after 180ms %v %v", w1, w2)
	}
	// A dot goes to its question.
	click(t, tt, "Go to question 1")
	if !tt.HasText("1/2") {
		t.Fatal("the dot went nowhere")
	}
}

// A press dragged across the card pages it, once the page is answered;
// its release chooses nothing.
func TestAskQuestionSwipe(t *testing.T) {
	a := askFixture(breakfast())
	tt := snap(t, a, "ask-swipe", false, nil)
	swipe := func(label string, by float32) {
		t.Helper()
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("no %q", label)
		}
		x, y := r.X+r.W/2, r.Y+r.H/2
		tt.Press(x, y)
		tt.Frame()
		for _, step := range []float32{0.25, 0.5, 0.75, 1} {
			tt.Move(x+by*step, y+2*step)
			tt.Frame()
		}
		tt.Release(x+by, y+2)
		tt.Frame()
	}
	swipe("Toast", -80)
	if !tt.HasText("1/2") {
		t.Fatal("an unanswered page does not swipe on")
	}
	// A click right after a swipe is its release's, and chooses nothing.
	click(t, tt, "Eggs")
	if len(a.asks["live/q1"].drafts[0].selected) != 0 {
		t.Fatal("a click 350ms after a swipe chose")
	}
	tt.Advance(400 * time.Millisecond)
	click(t, tt, "Eggs")
	swipe("Toast", -80)
	if !tt.HasText("2/2") {
		t.Fatalf("no swipe on: %q", tt.Texts())
	}
	if d := a.asks["live/q1"].drafts[0]; len(d.selected) != 1 || d.selected[0] != "Eggs" {
		t.Fatalf("the swipe's release chose: %+v", d)
	}
	swipe("Coffee", 30)
	if !tt.HasText("2/2") {
		t.Fatal("a short drag does not page")
	}
	tt.Advance(400 * time.Millisecond)
	swipe("Coffee", 80)
	if !tt.HasText("1/2") {
		t.Fatal("no swipe back")
	}
}

// An answer of one's own takes the place of the options, and Enter goes
// on with it.
func TestAskQuestionCustomAnswer(t *testing.T) {
	qs := breakfast()
	qs.AllowCustomAnswer = true
	a := askFixture(qs)
	recv := askSent(t, a)
	tt := snap(t, a, "ask-custom", false, nil)
	click(t, tt, "Eggs")
	click(t, tt, "Type a custom answer...")
	tt.Type("Porridge")
	tt.Frame()
	if d := a.asks["live/q1"].drafts[0]; d.custom != "Porridge" || len(d.selected) != 0 {
		t.Fatalf("draft %+v", d)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !tt.HasText("2/2") {
		t.Fatalf("Enter goes on: %q", tt.Texts())
	}
	click(t, tt, "Tea")
	click(t, tt, "Submit")
	got, _ := json.Marshal(recv())
	want := `{"_meta":{"claudeCode":{"askUserQuestion":{"answers":{"Pick a drink":"Tea","Pick ingredients":"Porridge"}}}},"optionId":"answer","outcome":"selected"}`
	if string(got) != want {
		t.Fatalf("sent\n%s\nwant\n%s", got, want)
	}
}

// A Lody request's note goes beside its answer under its own field.
func TestAskQuestionNote(t *testing.T) {
	custom := false
	qs := &session.AskQuestions{Source: "lody", Version: 1, Questions: []session.AskQuestion{{
		ID: "drink", Header: "Drink", Question: "Pick a drink", AllowCustomAnswer: &custom,
		Options: []session.AskOption{{Label: "Coffee"}, {Label: "Tea"}},
		Note:    &session.AskNote{FieldID: "drink_note", Description: "Milk, sugar…"},
	}}}
	a := askFixture(qs)
	recv := askSent(t, a)
	tt := snap(t, a, "ask-note", false, nil)
	if !tt.HasText("Additional context (optional)") || !tt.HasText("Milk, sugar…") || tt.HasText("Type a custom answer...") {
		t.Fatalf("texts %q", tt.Texts())
	}
	click(t, tt, "Coffee")
	// The field under the note's heading.
	r, _ := tt.Find("Additional context (optional)")
	tt.ClickAt(r.X+20, r.Y+r.H+6+20)
	tt.Frame()
	tt.Type("oat milk")
	tt.Frame()
	click(t, tt, "Submit")
	got, _ := json.Marshal(recv())
	want := `{"_meta":{"lody":{"elicitation":{"answers":{"drink":"Coffee","drink_note":"oat milk"},"version":1}}},"optionId":"answer","outcome":"selected"}`
	if string(got) != want {
		t.Fatalf("sent\n%s\nwant\n%s", got, want)
	}
}

// The cancel button picks the request's other option, and a failure
// says the answer did not reach the agent.
func TestAskQuestionCancel(t *testing.T) {
	a := askFixture(breakfast())
	posted := make(chan func(), 4)
	a.post = func(fn func()) { posted <- fn }
	var sent map[string]any
	old := askResponder
	askResponder = func(ctx context.Context, r *session.Replica, entryID, itemID, requestID string, outcome map[string]any) error {
		sent = outcome
		return context.DeadlineExceeded
	}
	t.Cleanup(func() { askResponder = old })
	tt := snap(t, a, "ask-cancel-dark", true, nil)
	click(t, tt, "Cancel")
	(<-posted)()
	tt.Frame()
	if sent["optionId"] != "cancel" || sent["_meta"] != nil {
		t.Fatalf("sent %+v", sent)
	}
	if !tt.HasText("Your answer didn't reach the agent. Try again.") {
		t.Fatalf("no failure: %q", tt.Texts())
	}
	snapFrame(t, tt, "ask-cancel-failed")
}

// The card counts down to the agent going on without an answer, and then
// takes none.
func TestAskQuestionCountdown(t *testing.T) {
	qs := breakfast()
	qs.AutoResolveAt = float64(time.Now().Add(2500 * time.Millisecond).UnixMilli())
	a := askFixture(qs)
	tt := snap(t, a, "ask-countdown", false, nil)
	if !tt.HasText("Continues in 3s") {
		t.Fatalf("texts %q", tt.Texts())
	}
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Continues in 2s") {
		t.Fatalf("after 1s %q", tt.Texts())
	}
	tt.Advance(1600 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Continuing") {
		t.Fatalf("at the end %q", tt.Texts())
	}
	click(t, tt, "Eggs")
	if len(a.asks["live/q1"].drafts[0].selected) != 0 {
		t.Fatal("an expired card takes answers")
	}
}

// Once answered, the request is its card in the turn, read only: Answered,
// the answers chosen, its pages walked with Next.
func TestAskQuestionAnswered(t *testing.T) {
	a := askFixture(breakfast())
	perm := a.view.Entries[3].Items[1].Permission
	perm.Pending, perm.Answers = false, session.AskAnswers{"Pick ingredients": []string{"Eggs"}, "Pick a drink": "Coffee"}
	a.expanded["r2/c4"] = true
	tt := snap(t, a, "ask-answered", false, nil)
	for _, want := range []string{"Answered", "Pick ingredients", "1/2", "Eggs"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("Submit") || tt.HasText("Type a custom answer...") {
		t.Fatal("a read-only card asks nothing")
	}
	click(t, tt, "Next")
	if !tt.HasText("2/2") || !tt.HasText("Coffee") {
		t.Fatalf("page two: %q", tt.Texts())
	}
	click(t, tt, "Tea")
	if d := a.asks["read/q1"].drafts[1]; len(d.selected) != 1 || d.selected[0] != "Coffee" {
		t.Fatalf("a read-only card changed: %+v", d)
	}
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "ask-answered-2")
	// The step folds it away.
	a.expanded["r2/c4/c5"] = true
	tt.Frame()
	if tt.HasText("Answered") {
		t.Fatal("the folded step still shows its card")
	}
}

// An option's details open in a dialog from its (i), which chooses
// nothing.
func TestAskQuestionDetails(t *testing.T) {
	a := askFixture(breakfast())
	tt := snap(t, a, "ask-details-closed", false, nil)
	click(t, tt, "Show details")
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "ask-details")
	if !tt.HasText("Sourdough, buttered") {
		t.Fatalf("no details: %q", tt.Texts())
	}
	if len(a.asks["live/q1"].drafts[0].selected) != 0 {
		t.Fatal("the (i) chose its option")
	}
	click(t, tt, "Close")
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if tt.HasText("Sourdough, buttered") {
		t.Fatal("the details stay")
	}
}

// Lody's pick of the options that answer and cancel.
func TestAskOptionIDs(t *testing.T) {
	cases := []struct {
		options        []session.Option
		answer, cancel string
	}{
		{[]session.Option{{ID: "cancel", Kind: "reject_once"}, {ID: "answer", Kind: "allow_once"}}, "answer", "cancel"},
		{[]session.Option{{ID: "no", Kind: "deny"}, {ID: "yes", Kind: "allow_always"}}, "yes", "no"},
		{[]session.Option{{ID: "a"}, {ID: "b"}}, "a", "b"},
		{nil, "", ""},
	}
	for _, tc := range cases {
		if a, c := askOptionIDs(tc.options); a != tc.answer || c != tc.cancel {
			t.Fatalf("%+v: %q %q", tc.options, a, c)
		}
	}
}
