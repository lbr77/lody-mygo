package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/session"
)

// openDrawn clicks label and lets the drawn menu it opens rise in.
func openDrawn(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
}

// A reply's fork opens Lody's destinations in a drawn menu: a new tab, a
// new worktree for a chat in a project, and the context copied.
func TestForkMenuDrawn(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.view.Entries = a.view.Entries[:3]
	a.view.Entries[1].AcpTurnID = "t1"
	a.catalog.Sessions[0].Status, a.catalog.Sessions[0].AgentConfigID = "idle", "a1"
	a.catalog.Capabilities = map[string]catalog.Capability{"a1": {Fork: true}}
	tt := snap(t, a, "fork-menu-closed", false, nil)
	openDrawn(t, tt, "Fork session")
	snapFrame(t, tt, "fork-menu")
	for _, want := range []string{"Fork to new tab", "Fork to new worktree", "Copy context as Markdown"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
}

// A permission's other answers open above its chevron in a drawn menu.
func TestPermissionMoreAnswers(t *testing.T) {
	a := fixture()
	tt := snap(t, a, "permission", false, nil)
	if tt.HasText("Always allow") {
		t.Fatal("the other answers show before they are asked for")
	}
	openDrawn(t, tt, "More options")
	snapFrame(t, tt, "permission-more")
	if !tt.HasText("Always allow") {
		t.Fatalf("no other answers: %q", tt.Texts())
	}
}

// The goal's objective folds open only where one line cuts it.
func TestGoalObjectiveFold(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.view.Entries = a.view.Entries[:3]
	short := "Ship the native client"
	a.view.Goal = &session.Goal{ThreadID: "g1", Objective: short, Status: "active"}
	tt := snap(t, a, "goal-short", false, nil)
	key := a.selected + "/goal"
	if err := tt.Click(short); err == nil {
		tt.Frame()
	}
	if a.expanded[key] {
		t.Fatal("a whole objective does not fold open")
	}
	long := strings.Repeat("Port every surface of the desktop client, its motion included, ", 6)
	a.view.Goal.Objective = strings.TrimSpace(long)
	tt.Frame()
	tt.Frame()
	if err := tt.Click(a.view.Goal.Objective); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.expanded[key] {
		t.Fatal("a cut objective folds open")
	}
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "goal-open")
}

// Auto-retrying after a capacity failure waits 5s·2ⁿ, read every 250ms.
func TestCapacityCountdown(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.view.Entries = append(a.view.Entries[:2],
		session.Entry{ID: "n1", Role: "system", Finished: true, Items: []session.Item{notice("f1", "chat_failed", map[string]any{
			"reason": "acp_provider_overloaded", "message": "Overloaded"})}},
	)
	st := a.capState()
	st.auto, st.attempts = true, 1
	tt := snap(t, a, "capacity-countdown", false, nil)
	if !tt.HasText("Retry in 10s") {
		t.Fatalf("a second automatic retry waits 10s: %q", tt.Texts())
	}
	tt.Advance(2100 * time.Millisecond) // 2.5s since it started counting
	tt.Frame()
	if !tt.HasText("Retry in 8s") {
		t.Fatalf("after 2.5s: %q", tt.Texts())
	}
}

// An attached image's peek springs out of its thumbnail and goes again.
func TestImagePeekMotion(t *testing.T) {
	a := fixture()
	a.view.Working = false
	a.view.Entries = a.view.Entries[:2]
	a.attachments = []pendingFile{{Path: "/tmp/peek.png", Name: "peek.png", Image: true, Size: 10}}
	a.images["file:/tmp/peek.png"] = &remoteImage{bitmap: bootMarkBitmap}
	w, h := bootMarkBitmap.Size()
	dims := fmt.Sprintf("%d × %d", w, h)
	tt := snap(t, a, "peek-closed", false, nil)
	if err := tt.Click("peek.png"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(1200 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "peek-open")
	if !tt.HasText(dims) {
		t.Fatalf("no peek: %q", tt.Texts())
	}
	*a.popover("peek//tmp/peek.png") = false
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if tt.HasText(dims) {
		t.Fatal("the peek stays")
	}
}
