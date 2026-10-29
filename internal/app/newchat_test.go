package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// A repository session starts from the branch picked while the repository
// has it, else main, master, the default, or the first, as Lody's.
func TestRepoBranchChoice(t *testing.T) {
	rb := &repoBranches{branches: []string{"dev", "feat/x", "main", "master", "trunk"}, def: "trunk"}
	for picked, want := range map[string]string{"feat/x": "feat/x", " dev ": "dev", "": "main", "gone": "main"} {
		if got := repoBranch(rb, picked); got != want {
			t.Errorf("picked %q: %q, want %q", picked, got, want)
		}
	}
	if got := repoBranch(&repoBranches{branches: []string{"dev", "master"}, def: "dev"}, ""); got != "master" {
		t.Errorf("master: %q", got)
	}
	if got := repoBranch(&repoBranches{branches: []string{"dev", "trunk"}, def: "trunk"}, ""); got != "trunk" {
		t.Errorf("default: %q", got)
	}
	if got := repoBranch(&repoBranches{branches: []string{"b", "c"}}, ""); got != "b" {
		t.Errorf("first: %q", got)
	}
	if got := repoBranch(&repoBranches{}, "main"); got != "" {
		t.Errorf("empty repository: %q", got)
	}
	if got := filterBranches([]string{"main", "Feat/Login", "fix/x"}, " feat "); len(got) != 1 || got[0] != "Feat/Login" {
		t.Errorf("filter: %q", got)
	}
}

func TestNewChatRepoBranches(t *testing.T) {
	a := fixture()
	a.selected, a.view = "", nil
	a.conn = &cloud.Conn{ID: "w1"}
	a.pickNewChatDefaults()

	// The branches are read from GitHub through the workspace, once, and
	// again as the repository is chosen again.
	posted := make(chan func(), 4)
	a.post = func(fn func()) { posted <- fn }
	old := listRepoBranches
	defer func() { listRepoBranches = old }()
	var asked []string
	listRepoBranches = func(ctx context.Context, convex *cloud.Convex, ws, repo string) ([]string, string, error) {
		asked = append(asked, ws+":"+repo)
		return []string{"a1", "a2", "a3", "dev", "main", "release/1.0", "zeta"}, "main", nil
	}
	a.convex, a.offline = &cloud.Convex{}, false
	rb := a.repoBranchList("acme/web", false)
	if !rb.loading || rb != a.repoBranchList("acme/web", false) {
		t.Fatal("not loading once")
	}
	(<-posted)()
	if rb.loading || len(rb.branches) != 7 || rb.def != "main" || len(asked) != 1 || asked[0] != "w1:acme/web" {
		t.Fatalf("loaded %+v %v", rb, asked)
	}
	a.convex, a.offline = nil, true

	a.newChat.project = "github:acme/web"
	a.repoBranchShown = "w1:acme/web"
	tt := snap(t, a, "newchat-repo", false, nil)
	if !tt.HasText("main") || !tt.HasText("Worktree") || tt.HasText("Local") {
		t.Fatalf("repo: %q", tt.Texts())
	}
	// More than six: a search narrows them.
	if err := tt.Click("main"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("release/1.0") {
		t.Fatalf("list: %q", tt.Texts())
	}
	snapFrame(t, tt, "newchat-repo-branches")
	rb.query = "rel"
	tt.Frame()
	if tt.HasText("dev") || !tt.HasText("release/1.0") {
		t.Fatalf("search: %q", tt.Texts())
	}
	if err := tt.Click("release/1.0"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.newChat.branch != "release/1.0" || repoBranch(rb, a.newChat.branch) != "release/1.0" {
		t.Fatalf("picked %q", a.newChat.branch)
	}

	// No branch is picked while they are on their way: Lody asks for one.
	a.sync = &catalog.Sync{}
	a.draft = "Fix the login page"
	rb.loading, rb.branches = true, nil
	a.startHomeChat()
	if a.sendError != locales.BranchMissing() || a.sending || a.draft == "" {
		t.Fatalf("missing branch: %q sending %v", a.sendError, a.sending)
	}
	// An empty repository has none to pick, and its picker is inert.
	rb.loading = false
	a.sendError = ""
	tt.Frame()
	if !tt.HasText(locales.BranchPlaceholder()) {
		t.Fatalf("empty: %q", tt.Texts())
	}
}

func TestSessionQuotaAndCheckout(t *testing.T) {
	a := fixture()
	view := a.view
	a.selected, a.view = "", nil
	a.pickNewChatDefaults()
	a.tier = planTier{workspace: "w1", tier: "free", at: time.Now()}
	if !a.createAllowed() || len(a.toasts) > 0 {
		t.Fatal("4 sessions refused")
	}
	// The workspace's sessions are counted only once known.
	a.synced = false
	for i := 0; i < freeSessionLimit; i++ {
		a.catalog.Sessions = append(a.catalog.Sessions, catalog.Session{ID: fmt.Sprint("x", i), MachineID: "m1", Archived: true})
	}
	if !a.createAllowed() {
		t.Fatal("refused before the catalog synced")
	}
	a.synced = true
	notice := locales.SessionLimitNotice(freeSessionLimit)
	tt := snap(t, a, "newchat-session-limit", false, nil)
	if !tt.HasText(notice) || !tt.HasText("Upgrade to Plus") {
		t.Fatalf("notice: %q", tt.Texts())
	}
	a.draft = "Fix the login page"
	a.startHomeChat()
	if len(a.toasts) != 1 || a.toasts[0].Title != "Free session limit reached" || a.toasts[0].Action != "Upgrade to Plus" || a.toasts[0].OnAction == nil ||
		a.toasts[0].Description != locales.FreeSessionLimitReachedDescription(freeSessionLimit, freeSessionLimit+4) || a.sending || a.draft == "" {
		t.Fatalf("refused: %+v", a.toasts)
	}
	// Plus keeps as many as it likes.
	a.toasts = nil
	a.tier.tier = "plus"
	tt.Frame()
	if tt.HasText(notice) || !a.createAllowed() {
		t.Fatal("limited on Plus")
	}
	// A pending checkout takes neither a session nor a message.
	a.tier.checkoutPending = true
	if a.createAllowed() || len(a.toasts) != 1 || a.toasts[0].Title != "Checkout required" ||
		a.toasts[0].Description != "Complete checkout for this workspace before sending messages." {
		t.Fatalf("checkout: %+v", a.toasts)
	}
	a.toasts = nil
	a.selected, a.view = "s1", view
	a.draft = "One more thing"
	a.sendChecked()
	if a.draft != "One more thing" || a.sending || len(a.toasts) != 1 || a.toasts[0].Title != "Checkout required" {
		t.Fatalf("sent while checkout pending: %q %+v", a.draft, a.toasts)
	}
	// A Free session takes 30 turns.
	a.toasts = nil
	a.tier = planTier{workspace: "w1", tier: "free", at: time.Now()}
	for len(a.view.Entries) < 2*freeTurnLimit {
		a.view.Entries = append(a.view.Entries, session.Entry{ID: fmt.Sprint("t", len(a.view.Entries)), Role: "user"})
	}
	if a.turnAllowed(a.view) || len(a.toasts) != 1 || a.toasts[0].Title != "Free plan turn limit reached" {
		t.Fatalf("turn limit: %+v", a.toasts)
	}
}

func TestCheckoutPendingDialog(t *testing.T) {
	a := fixture()
	a.convex = &cloud.Convex{}
	a.tier = planTier{workspace: "w1", tier: "plus", checkoutPending: true, at: time.Now()}
	a.fetches = map[string]*fetched{"w1|billing": {value: &billingOverview{CanManageBilling: true}}}
	tt := snap(t, a, "checkout-pending", false, nil)
	if !tt.HasText("Complete checkout to activate this workspace") || !tt.HasText(locales.CheckoutPendingDescription()) ||
		!tt.HasText("Go to checkout") || !tt.HasText("Later") {
		t.Fatalf("dialog: %q", tt.Texts())
	}
	if err := tt.Click("Later"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	// Once a visit.
	if tt.HasText("Complete checkout to activate this workspace") {
		t.Fatal("shown again")
	}
	// Someone who can't pay is told who can.
	a.checkoutSeen = nil
	a.fetches["w1|billing"].value = &billingOverview{}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if !tt.HasText(locales.CheckoutPendingMemberDescription()) || tt.HasText("Go to checkout") {
		t.Fatalf("member: %q", tt.Texts())
	}
}

func TestRunConfigMenu(t *testing.T) {
	a := fixture()
	a.selected, a.view = "", nil
	a.pickNewChatDefaults()
	a.catalog.Capabilities = map[string]catalog.Capability{"a1": {Options: []catalog.ConfigOption{
		{ID: "permission_mode", Name: "Permission Mode", Category: "_permission", Type: "select", Current: "ask",
			Choices: []catalog.Choice{{Value: "ask", Name: "Ask Every Time"}, {Value: "auto", Name: "Auto"}, {Value: "always-approve", Name: "Always Approve"}}},
		{ID: "interaction_mode", Name: "Interaction Mode", Type: "select", Current: "agent",
			Choices: []catalog.Choice{{Value: "agent", Name: "Agent"}, {Value: "ask", Name: "Ask"}}},
		{ID: "agent_preset", Name: "Preset", Type: "select", Current: "std", Choices: []catalog.Choice{{Value: "std", Name: "Standard"}, {Value: "deep", Name: "Deep"}}},
		{ID: "plan_mode", Name: "Plan mode", Type: "boolean", Current: false},
		{ID: "web_search", Name: "Web search", Type: "boolean", Current: true},
		{ID: "fast", Name: "Fast", Type: "select", Current: "off", Choices: []catalog.Choice{{Value: "on", Name: "On"}, {Value: "off", Name: "Off"}}},
	}}}
	tt := snap(t, a, "run-config", false, nil)
	// The agent's permission setting outranks Claude Code's modes.
	if _, ok := tt.Find(locales.PermissionMode("Ask Every Time")); !ok {
		t.Fatalf("permission: %q", tt.Texts())
	}
	if err := tt.Click(locales.RunConfigTitle()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	for _, want := range []string{"Preset", "Standard", "Interaction Mode", "Agent", "Plan", "Web search"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("Permission Mode") {
		t.Fatal("permission in the menu as well as its button")
	}
	// Switches act in place.
	if err := tt.Click("Plan"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Web search"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Preset"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "run-config-menu")
	if err := tt.Click("Deep"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	if !tt.HasText("Web search") {
		t.Fatal("menu closed by a choice")
	}
	// Fast as a select of on and off.
	if err := tt.Click(locales.FastModeTooltipClaude()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	got := a.chosenRunValues("", "a1", "")
	if got["plan_mode"] != true || got["web_search"] != false || got["agent_preset"] != "deep" || got["fast"] != "on" {
		t.Fatalf("values %v", got)
	}
	// The permission button writes the agent's setting.
	a.permissionButtonPick(t, tt, "Always Approve")
	if a.runValues[""]["permission_mode"] != "always-approve" || a.modes[""] != "" {
		t.Fatalf("permission %v mode %q", a.runValues[""], a.modes[""])
	}

	// In a conversation under way the preset is the one it started with.
	b := fixture()
	b.view.Working = false
	b.view.Entries = b.view.Entries[:2]
	b.catalog.Capabilities = a.catalog.Capabilities
	b.catalog.Sessions[0].AgentConfigID = "a1"
	tt = snap(t, b, "run-config-session", false, nil)
	if err := tt.Click(locales.RunConfigTitle()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	tt.Click("Preset")
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if tt.HasText("Deep") {
		t.Fatal("preset changed mid-conversation")
	}
	if err := tt.Click("Web search"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if b.runValues["s1"]["web_search"] != false {
		t.Fatalf("session values %v", b.runValues["s1"])
	}
}

// permissionButtonPick opens the permission button and picks label.
func (a *App) permissionButtonPick(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if err := tt.Click(locales.PermissionMode("Ask Every Time")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if err := tt.Click(label); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
}
