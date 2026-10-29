package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/internal/localplane"
	"github.com/lbr77/lody-mygo/internal/localplane/localplanetest"
	"github.com/lbr77/lody-mygo/locales"
)

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
