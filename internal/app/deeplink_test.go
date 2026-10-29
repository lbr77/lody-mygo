package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/internal/localplane"
	"github.com/lbr77/lody-mygo/internal/localplane/localplanetest"
	"github.com/lbr77/lody-mygo/locales"
)

// testOffer is an end-to-end encrypted workspace's member offer, as
// Lody's web app puts one in a link.
var testOffer = `{"version":1,"workspaceId":"w9","genesisHash":"` + strings.Repeat("a", 64) + `","genesisRecordHex":"0a0b","endorser":"` +
	strings.Repeat("b", 64) + `","token":"` + strings.Repeat("c", 64) + `","mode":"link","expiresAt":1798804800000}`

// fakeConvex answers the app's Convex calls as Lody's backend would, by
// function: answer gives a call's value, or the error it fails with.
// Nothing reaches the network. It returns the calls made so far.
func fakeConvex(t *testing.T, a *App, answer func(path string, args map[string]any) (any, string)) func() []map[string]any {
	t.Helper()
	var mu sync.Mutex
	var calls []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/convex/token" {
			json.NewEncoder(w).Encode(map[string]any{"token": "jwt"})
			return
		}
		var body struct {
			Path string         `json:"path"`
			Args map[string]any `json:"args"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, map[string]any{"path": body.Path, "args": body.Args})
		mu.Unlock()
		value, fail := answer(body.Path, body.Args)
		if fail != "" {
			json.NewEncoder(w).Encode(map[string]any{"status": "error", "errorMessage": fail})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "value": value})
	}))
	t.Cleanup(srv.Close)
	auth, origin := cloud.AuthOrigin, cloud.ConvexOrigin
	cloud.AuthOrigin, cloud.ConvexOrigin = srv.URL, srv.URL
	t.Cleanup(func() { cloud.AuthOrigin, cloud.ConvexOrigin = auth, origin })
	a.convex = &cloud.Convex{API: &cloud.API{}, Token: func() string { return "token" }}
	return func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), calls...)
	}
}

// lastToast is the newest toast waiting to show.
func lastToast(a *App) string {
	if len(a.toasts) == 0 {
		return ""
	}
	return a.toasts[len(a.toasts)-1].Title
}

// The routes back from lody.ai open what the app has for them, as Lody's
// desktop app routes them: an invitation opens to be accepted, or says an
// encrypted workspace's can't be; GitHub's install comes back to its
// settings; checkout to billing, the plan asked for again; a pairing
// request is taken by this machine once its daemon has run, and a refusal
// says so. The routes it can't serve, and sign-in's callback, are left
// alone.
func TestLinkRoutes(t *testing.T) {
	a := fixture()
	a.offline = false
	a.account.Workspaces[0].Slug = "analytical"
	pump := pumpUpdates(t, a)
	refuse := false
	calls := fakeConvex(t, a, func(path string, args map[string]any) (any, string) {
		switch path {
		case "auth:getInvitationPreview":
			return map[string]any{"status": "available", "organizationName": "Acme", "inviterName": "Grace Hopper", "role": "member"}, ""
		case "billing:getWorkspaceBillingEntitlement":
			return map[string]any{"effectivePlanTier": "plus", "checkoutPending": false}, ""
		case "machinePairing:claimFromDesktop":
			if refuse {
				return nil, "[CONVEX M(machinePairing:claimFromDesktop)] Uncaught Error: Request expired"
			}
			return map[string]any{"id": args["requestId"], "status": "claimed"}, ""
		}
		return nil, ""
	})

	// An invitation, with Settings open over the workspace.
	a.settingsOpen = true
	a.OpenURL("lody://invite/open?invitationId=inv_1")
	if !a.wsJoin.open || a.wsJoin.kind != "invite" || a.wsJoin.id != "inv_1" || a.settingsOpen || a.link != nil {
		t.Fatalf("invite: %+v, settings %v", a.wsJoin, a.settingsOpen)
	}
	pump("the invitation", func() bool { return a.wsJoin.preview != nil })
	if a.wsJoin.preview["organizationName"] != "Acme" {
		t.Fatalf("preview: %+v", a.wsJoin.preview)
	}
	a.wsJoin.open = false
	a.OpenURL("lody://invite/open?memberOffer=" + url.QueryEscape(testOffer))
	if lastToast(a) != locales.E2eeModeUpgradeHelp() || a.wsJoin.open {
		t.Fatalf("encrypted invite: %q", lastToast(a))
	}

	// GitHub's install comes back to its settings.
	a.OpenURL("lody://github-install?workspaceSlug=analytical&installed=true")
	if !a.settingsOpen || a.settingsTab != "github" || a.link != nil {
		t.Fatalf("github: settings %v %q, link %+v", a.settingsOpen, a.settingsTab, a.link)
	}

	// Checkout comes back to billing, the plan asked for again.
	a.settingsOpen = false
	a.tier = planTier{workspace: "w1", tier: "free", at: time.Now()}
	a.OpenURL("lody://checkout-return?workspaceSlug=analytical&checkout=success")
	if !a.settingsOpen || a.settingsTab != "billing" {
		t.Fatalf("checkout: settings %v %q", a.settingsOpen, a.settingsTab)
	}
	pump("the plan", func() bool { return a.tier.tier == "plus" })
	a.OpenURL("lody://checkout-return?workspaceSlug=nowhere")
	if lastToast(a) != locales.DeepLinkWorkspaceUnavailable() || a.link != nil {
		t.Fatalf("unknown workspace: %q", lastToast(a))
	}

	// A pairing request waits for this machine's daemon, then is taken
	// under the computer's name.
	a.term.machine, a.term.checked = "", time.Now()
	a.OpenURL("lody://machine/connect?requestId=req1")
	if a.claim.request != "req1" || a.claim.busy {
		t.Fatalf("claim before the daemon: %+v", a.claim)
	}
	a.term.machine = "m1"
	a.followLink()
	pump("the claim", func() bool { return a.claim.request == "" && !a.claim.busy })
	if lastToast(a) != locales.MachinePairingDesktopClaimed() {
		t.Fatalf("claimed: %q", lastToast(a))
	}
	host, _ := os.Hostname()
	var claim map[string]any
	for _, c := range calls() {
		if c["path"] == "machinePairing:claimFromDesktop" {
			claim = c["args"].(map[string]any)
		}
	}
	if claim["requestId"] != "req1" || claim["machineId"] != "m1" || claim["machineName"] != firstNonEmpty(host, "m1") {
		t.Fatalf("claim args: %+v", claim)
	}
	// Refused, it says what Lody's says, not the server's words.
	refuse = true
	a.OpenURL("lody://machine/connect?requestId=req2")
	pump("the refusal", func() bool { return a.claim.request == "" && !a.claim.busy })
	if lastToast(a) != locales.MachinePairingDesktopClaimFailed() {
		t.Fatalf("refused: %q", lastToast(a))
	}
	// Signed out, it waits.
	a.phase = phaseSignedOut
	a.OpenURL("lody://machine/connect?requestId=req3")
	if a.claim.request != "req3" || a.claim.busy {
		t.Fatalf("claim signed out: %+v", a.claim)
	}

	// What it can't serve is left alone.
	a.phase, a.toasts, a.claim = phaseSignedIn, nil, machineClaim{}
	for _, raw := range []string{"lody://auth/callback#token=x", "lody://nowhere/else", "lody://machine/connect/?requestId=r"} {
		a.OpenURL(raw)
		if a.link != nil || len(a.toasts) != 0 || a.claim.request != "" {
			t.Fatalf("%s: link %+v, toasts %+v", raw, a.link, a.toasts)
		}
	}
}

// The links the system hands over read as Lody's desktop app reads them:
// a session under any of Lody's schemes, or New chat on a machine's
// project; nothing else, nor the legacy session://, nor a bent one.
func TestParseLink(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want *linkTarget
	}{
		{"lody://session/s1?workspace=w2", &linkTarget{session: "s1", workspace: "w2"}},
		{`"lody://session/s1"`, &linkTarget{session: "s1"}},
		{"ai.lody.stable://session/s1?tab=s2", &linkTarget{session: "s1", tab: "s2"}},
		{"lody://chat/new?machine=m1&project=p1&workspaceSlug=acme", &linkTarget{chat: true, machine: "m1", project: "p1", slug: "acme"}},
		{"lody://chat/new/?project=p1&machine=m1", &linkTarget{chat: true, machine: "m1", project: "p1"}},
		{"session://s1", nil},
		{"lody://session/s1?workspace=w2&x=1", nil},
		{"lody://session/a/../s1", nil},
		{"lody://session/s%31", nil},
		{"lody://chat/new?machine=m1", nil},
		{"lody://user@chat/new?machine=m1&project=p1", nil},
		{"lody://chat:1/new?machine=m1&project=p1", nil},
		{"lody://auth/callback#token=x", nil},
		{"https://lody.ai/session/s1", nil},
		{"lody://invite/open?invitationId=inv_1", &linkTarget{route: "invite", id: "inv_1"}},
		{"lody://invite/open/?invitationId=%20inv_1%20", &linkTarget{route: "invite", id: "inv_1"}},
		{"lody://invite/open", nil},
		{"lody://invite/open?memberOffer=" + url.QueryEscape(testOffer), &linkTarget{route: "invite", e2ee: true}},
		{"lody://invite/open?memberOffer=%7B%7D&invitationId=inv_1", nil},
		{"lody://machine/connect?requestId=req1", &linkTarget{route: "machine", id: "req1"}},
		{"lody://machine/connect/?requestId=req1", nil},
		{"lody://machine/connect", nil},
		{"lody://github-install?workspaceSlug=acme&installed=true", &linkTarget{route: "github", slug: "acme"}},
		{"lody://github-install/", &linkTarget{route: "github"}},
		{"lody://github-install/elsewhere", nil},
		{"lody://checkout-return?workspaceSlug=acme&checkout=success", &linkTarget{route: "checkout", slug: "acme"}},
		{"lody://provider/import?v=1&data=e30", &linkTarget{route: "magpie", raw: "lody://provider/import?v=1&data=e30"}},
		{"lody://provider/import/?v=1&data=e30", nil},
		{"lody://checkout-return:8080/", nil},
		{"lody://nowhere/else", nil},
	} {
		got := parseLink(c.raw)
		if (got == nil) != (c.want == nil) || got != nil && *got != *c.want {
			t.Errorf("parseLink(%q) = %+v, want %+v", c.raw, got, c.want)
		}
	}
}

// A link opens its conversation, a child one as its tab, and closes
// Settings over it; one to a conversation not synced yet waits for it, at
// most 15 seconds; one to a workspace the account lacks says so, as does
// one that waited in vain. New chat comes with the link's machine and
// project chosen, where they are known.
func TestOpenLink(t *testing.T) {
	a := fixture()
	a.settingsOpen = true
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	a.OpenURL("lody://session/s3?workspace=w1")
	if a.selected != "s3" || a.link != nil || a.settingsOpen {
		t.Fatalf("selected %q, link %+v, settings %v", a.selected, a.link, a.settingsOpen)
	}

	child := a.catalog.Sessions[1]
	child.ID, child.ParentSessionID, child.Title = "s2t", "s2", "A tab"
	a.catalog.Sessions = append(a.catalog.Sessions, child)
	a.tabGroupFor("s2").closed["s2t"] = true
	a.OpenURL("lody://session/s2t")
	if a.selected != "s2t" || a.tabGroupFor("s2").closed["s2t"] {
		t.Fatalf("tab: selected %q, closed %v", a.selected, a.tabGroupFor("s2").closed)
	}

	// Not synced yet: it opens as the catalog brings it.
	a.OpenURL("lody://session/s9")
	if a.link == nil || a.selected == "s9" {
		t.Fatal("opened a session the catalog lacks")
	}
	s9 := a.catalog.Sessions[1]
	s9.ID = "s9"
	a.catalog.Sessions = append(a.catalog.Sessions, s9)
	tt.Frame()
	if a.selected != "s9" || a.link != nil {
		t.Fatalf("waiting link: selected %q, link %+v", a.selected, a.link)
	}

	a.toasts = nil
	a.OpenURL("lody://session/s3?workspace=nowhere")
	if a.link != nil || len(a.toasts) != 1 || a.toasts[0].Title != locales.DeepLinkWorkspaceUnavailable() {
		t.Fatalf("unavailable workspace: %+v %+v", a.link, a.toasts)
	}

	a.toasts = nil
	a.OpenURL("lody://session/gone")
	a.link.until = time.Now().Add(-time.Second)
	a.followLink()
	if a.link != nil || len(a.toasts) != 1 || a.toasts[0].Title != locales.DeepLinkOpenFailed() {
		t.Fatalf("expired: %+v %+v", a.link, a.toasts)
	}

	a.account.Workspaces[0].Slug = "analytical"
	a.newChat.machine, a.newChat.project = "", ""
	a.OpenURL("lody://chat/new?machine=m1&project=p2&workspaceSlug=analytical")
	if a.selected != "" || a.newChat.machine != "m1" || a.newChat.project != "m1:local:p2" || a.newChat.agent != "a1" {
		t.Fatalf("new chat: selected %q, %+v", a.selected, a.newChat)
	}
	// An unknown project stays unchosen; a link never adds one.
	a.OpenURL("lody://chat/new?machine=m1&project=elsewhere")
	if a.newChat.project != "" || a.newChat.machine != "m1" {
		t.Fatalf("unknown project: %+v", a.newChat)
	}

	// Before sign-in, it waits.
	a.phase = phaseLoading
	a.OpenURL("lody://session/s4")
	if a.link == nil {
		t.Fatal("dropped a link before sign-in")
	}
	a.phase = phaseSignedIn
	tt.Frame()
	if a.selected != "s4" {
		t.Fatalf("after sign-in: %q", a.selected)
	}
}

// A link to a conversation of another workspace of the account switches
// to it, then opens the conversation once its catalog brings it; here the
// workspace syncs with a fake daemon, in local-only mode.
func TestOpenLinkOtherWorkspace(t *testing.T) {
	f, err := flock.New("daemon")
	if err != nil {
		t.Fatal(err)
	}
	f.Put([]any{"e", "machine-m1"}, true)
	f.Put([]any{"m", "machine-m1"}, map[string]any{"name": "Studio"})
	f.Put([]any{"e", "session-x7"}, true)
	f.Put([]any{"m", "session-x7"}, map[string]any{"id": "x7", "machineId": "m1", "title": "Over in Personal", "createdAt": "2026-10-29T00:00:00Z"})
	b, _ := f.ExportJSON(nil)
	raw, _ := json.Marshal(b)
	d := localplanetest.Start(t)
	d.OnJoin = func(r localplane.Room) *localplane.Payload {
		if r.Scope == "meta" {
			return &localplane.Payload{Kind: "flock-json", Bundle: raw}
		}
		return nil
	}
	defer swapLocal(func(ctx context.Context, workspace string) (*localplane.Conn, error) {
		return localplane.Dial(ctx, d.Path, workspace)
	}, func(ctx context.Context, workspace, method string, params any, timeout time.Duration) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true}`), nil
	})()

	a := fixture()
	a.localOnly = true
	a.catalog = catalog.Catalog{}
	pump := pumpUpdates(t, a)
	a.OpenURL("lody://session/x7?workspace=w2")
	defer a.disconnect()
	if a.workspaceID != "w2" || a.link == nil {
		t.Fatalf("workspace %q, link %+v", a.workspaceID, a.link)
	}
	pump("the conversation", func() bool {
		a.followLink()
		return a.selected == "x7"
	})
	if a.link != nil {
		t.Fatalf("link kept: %+v", a.link)
	}
}

// About says whether this app opens Lody's links from other apps, as
// Lody's does, and makes it their default on Set as default.
func TestLinkDefaultRow(t *testing.T) {
	ours, claimed := false, 0
	oldIs, oldClaim := linkSchemeIsOurs, claimLinkScheme
	linkSchemeIsOurs = func() bool { return ours }
	claimLinkScheme = func() error { claimed++; ours = true; return nil }
	defer func() { linkSchemeIsOurs, claimLinkScheme = oldIs, oldClaim }()

	a := fixture()
	a.settingsOpen, a.settingsTab = true, "about"
	pump := pumpUpdates(t, a)
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	pump("the system's answer", func() bool { return a.linkDefault.known })
	tt.Frame()
	if !tt.HasText(locales.DeepLinkDefaultHandler()) || !tt.HasText(locales.DeepLinkMakeDefault()) {
		t.Fatalf("no row in %q", tt.Texts())
	}
	snapFrame(t, tt, "settings-about-links")
	if err := tt.Click(locales.DeepLinkMakeDefault()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	pump("the claim", func() bool { return claimed == 1 && !a.linkDefault.pending })
	tt.Frame()
	if !tt.HasText(locales.DeepLinkDefaultHandlerActive()) || tt.HasText(locales.DeepLinkMakeDefault()) {
		t.Fatalf("after the claim: %q", tt.Texts())
	}
}
