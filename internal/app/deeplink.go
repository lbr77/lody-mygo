package app

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/convmd"
	"github.com/lbr77/lody-mygo/locales"
)

// Lody's links from other apps, as its desktop app routes them: the
// system hands the app the lody:// URLs (urlSchemes in mygo.json), and
//
//   - lody://session/<id>?workspace=<id> opens that conversation in its
//     workspace;
//   - lody://chat/new?machine=…&project=…&workspaceSlug=… opens New chat on
//     that machine's project, which `lody app <dir>` opens;
//   - lody://invite/open?invitationId=… opens the invitation, to accept it;
//   - lody://machine/connect?requestId=… connects this machine to the
//     pairing request another device made, once the account is signed in
//     and this machine's daemon has run, however long that takes;
//   - lody://github-install?workspaceSlug=… comes back from installing
//     the GitHub App to the workspace's GitHub settings, read again;
//   - lody://checkout-return?workspaceSlug=… comes back from checkout to
//     its billing, asking again for the workspace's plan;
//   - lody://provider/import?v=1&data=… imports Magpie's providers.
//
// lody://auth/callback carries the desktop's browser sign-in, which
// redeems it only against the PKCE request it opened itself; this app
// signs in with a device code and opens no such request, so a callback
// has nothing to complete and is left alone, as Lody's leaves a callback
// that matches no request. An invitation for an end-to-end encrypted
// workspace (memberOffer) names a mode this client can't open, and says
// so. Any other route is left alone too: the desktop's "does not support
// this link" is its native dialog's English, which Lody's catalogs don't
// carry.
//
// A link that comes before the app can open it, as at launch, waits for
// the account and the workspace's catalog: at most 15 seconds, after
// which a conversation that never came is reported. A workspace the
// account lacks is reported at once; it never falls back to the one
// shown. Links name places and grant nothing.

// linkScheme is the scheme of Lody's links.
const linkScheme = "lody"

// linkTimeout is how long a link waits for what it opens, as Lody's.
const linkTimeout = 15 * time.Second

// linkTarget is where a link goes: a conversation, New chat, or one of
// the routes that come back from lody.ai.
type linkTarget struct {
	session, workspace, tab string
	chat                    bool
	machine, project, slug  string
	route                   string    // invite, machine, github, checkout or magpie; "" for the two above
	id                      string    // the invitation, or the pairing request
	e2ee                    bool      // an invitation to an end-to-end encrypted workspace
	raw                     string    // a Magpie link, read as it opens
	until                   time.Time // when it stops waiting
}

// parseLink reads a URL the system hands the app as one of Lody's links;
// nil when it is none.
func parseLink(raw string) *linkTarget {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && (raw[0] == '"' || raw[0] == '\'') && raw[len(raw)-1] == raw[0] {
		raw = strings.TrimSpace(raw[1 : len(raw)-1])
	}
	// The legacy session:// is read in conversations only; the system
	// never hands it over.
	if strings.HasPrefix(raw, "session:") {
		return nil
	}
	if session, workspace, tab, ok := convmd.ParseSessionLink(raw); ok {
		return &linkTarget{session: session, workspace: workspace, tab: tab}
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, linkScheme) || u.User != nil || u.Port() != "" {
		return nil
	}
	q := u.Query()
	param := func(name string) string { return strings.TrimSpace(q.Get(name)) }
	switch u.Hostname() + strings.TrimSuffix(u.EscapedPath(), "/") {
	case "chat/new":
		t := &linkTarget{chat: true, machine: param("machine"), project: param("project"), slug: param("workspaceSlug")}
		if t.machine == "" || t.project == "" {
			return nil
		}
		return t
	case "invite/open":
		if q.Has("memberOffer") {
			if !memberOffer(q.Get("memberOffer")) {
				return nil
			}
			return &linkTarget{route: "invite", e2ee: true}
		}
		if id := param("invitationId"); id != "" {
			return &linkTarget{route: "invite", id: id}
		}
	case "machine/connect":
		// Exactly /connect, as Lody's reads it.
		if id := param("requestId"); id != "" && u.EscapedPath() == "/connect" {
			return &linkTarget{route: "machine", id: id}
		}
	case "github-install":
		return &linkTarget{route: "github", slug: param("workspaceSlug")}
	case "checkout-return":
		return &linkTarget{route: "checkout", slug: param("workspaceSlug")}
	case "provider/import":
		if isMagpieLink(raw) {
			return &linkTarget{route: "magpie", raw: raw}
		}
	}
	return nil
}

var (
	offerHash = regexp.MustCompile(`^[0-9a-f]{64}$`)
	offerHex  = regexp.MustCompile(`^(?:[0-9a-f]{2})+$`)
)

// memberOffer is whether s is an end-to-end encrypted workspace's member
// offer, as Lody's reads one.
func memberOffer(s string) bool {
	var o struct {
		Version          any    `json:"version"`
		WorkspaceID      any    `json:"workspaceId"`
		GenesisHash      string `json:"genesisHash"`
		GenesisRecordHex any    `json:"genesisRecordHex"`
		Endorser         string `json:"endorser"`
		Token            string `json:"token"`
		Mode             string `json:"mode"`
		ExpiresAt        any    `json:"expiresAt"`
	}
	if len(s) > 8192 || json.Unmarshal([]byte(s), &o) != nil || o.Version != 1.0 {
		return false
	}
	ws, _ := o.WorkspaceID.(string)
	record, _ := o.GenesisRecordHex.(string)
	at, _ := o.ExpiresAt.(float64)
	return ws != "" && len(ws) <= 256 && offerHash.MatchString(o.GenesisHash) && offerHash.MatchString(o.Endorser) &&
		offerHash.MatchString(o.Token) && len(record) <= 4096 && offerHex.MatchString(record) &&
		(o.Mode == "email" || o.Mode == "link") && at >= 0 && at == math.Trunc(at) && at <= 1<<53-1
}

// OpenURL opens a link the system hands the app, bringing its window
// forward. Call it on the main thread.
func (a *App) OpenURL(raw string) {
	t := parseLink(raw)
	if t == nil {
		return
	}
	if a.win != nil {
		a.win.Show()
		a.win.Focus()
	}
	switch t.route {
	case "machine":
		// The newest request wins, and waits as long as it must.
		a.claim.request = t.id
		a.claimMachine()
	case "magpie":
		a.openMagpie(t.raw)
	default:
		// The newest link wins; one waiting before it is dropped.
		t.until = time.Now().Add(linkTimeout)
		a.link = t
		// A frame once it is due, which gives up on it if it still waits.
		time.AfterFunc(linkTimeout+50*time.Millisecond, a.redraw)
		a.followLink()
	}
	a.redraw()
}

// linkWorkspace is the workspace a link names, by id or, for New chat and
// the routes back from lody.ai, by slug, else the one shown; "" when the
// account has no such workspace.
func (a *App) linkWorkspace(t *linkTarget) string {
	bySlug := t.chat || t.route != ""
	named := t.workspace
	if bySlug {
		named = t.slug
	}
	for _, w := range a.account.Workspaces {
		switch {
		case named == "" && w.ID == a.workspaceID,
			bySlug && named != "" && w.Slug == named,
			!bySlug && named != "" && w.ID == named:
			return w.ID
		}
	}
	return ""
}

// followLink opens the link waiting once it can, switching to its
// workspace first. It runs as the app draws, so that what a link waits
// for, as the catalog, opens it as it comes.
func (a *App) followLink() {
	a.claimMachine()
	t := a.link
	if t == nil {
		return
	}
	if time.Now().After(t.until) {
		// A conversation that never came is reported, as Lody's.
		a.link = nil
		if !t.chat && t.route == "" {
			a.toast("error", locales.DeepLinkOpenFailed())
		}
		return
	}
	if t.route == "invite" {
		// An invitation is the account's, whichever workspace shows, or
		// none yet.
		if a.phase != phaseSignedIn || a.account == nil {
			return
		}
		a.link, a.settingsOpen = nil, false
		if t.e2ee {
			a.toast("error", locales.E2eeModeUpgradeHelp())
			return
		}
		a.wsJoin = workspaceJoin{open: true, link: "https://lody.ai/invite/" + url.PathEscape(t.id)}
		a.lookUpLink()
		return
	}
	if a.phase != phaseSignedIn || a.account == nil || a.workspaceID == "" {
		return
	}
	ws := a.linkWorkspace(t)
	if ws == "" {
		a.link = nil
		a.toast("error", locales.DeepLinkWorkspaceUnavailable())
		return
	}
	if ws != a.workspaceID {
		a.connect(ws)
		return
	}
	switch t.route {
	case "github":
		// Back from GitHub: its settings, as they are now.
		a.link = nil
		a.settingsOpen, a.settingsTab = true, "github"
		if !a.noCloud() {
			a.refetchKey("github")
			a.refetchKey("github-personal")
		}
		return
	case "checkout":
		// Back from checkout: billing, and the plan asked for again, as
		// it may have changed.
		a.link = nil
		a.settingsOpen, a.settingsTab = true, "billing"
		a.tier.at = time.Time{}
		a.entitlement(true)
		if !a.noCloud() {
			a.refetchKey("billing")
			a.refetchKey("invoices")
		}
		return
	}
	if t.chat {
		machine := ""
		for _, id := range a.catalog.MachineIDs {
			if id == t.machine {
				machine = id
			}
		}
		if machine == "" && !a.synced {
			return
		}
		a.link, a.settingsOpen = nil, false
		// An unknown machine or project stays unchosen; a link never adds
		// a project.
		if machine != "" {
			project := ""
			if pr := a.catalog.Project(machine + ":local:" + t.project); pr != nil {
				project = pr.ID
			}
			a.newChat.machine, a.newChat.project, a.newChat.branch = machine, project, ""
			a.pickNewChatDefaults()
		}
		a.open("")
		return
	}
	id := t.session
	if t.tab != "" {
		id = t.tab
	}
	// A synced catalog may still lack a child just made: it waits.
	s := a.catalog.Session(id)
	if s == nil {
		return
	}
	a.link, a.settingsOpen = nil, false
	if root := a.tabRoot(s); root != nil && root.ID != s.ID {
		a.showTab(root, s.ID)
		return
	}
	a.open(s.ID)
}

// machineClaim is the pairing request a lody://machine/connect link asked
// this machine to take: Lody's web app offers the link to the desktop of
// the computer to connect.
type machineClaim struct {
	request string // until it is claimed, or refused
	busy    bool
	wake    time.Time // when a frame was last asked for, to look for the daemon again
}

// claimMachine connects this machine to the request a link named, under
// the computer's name, once the account is signed in and this machine's
// daemon has run, as Lody's desktop app does
// (machinePairing:claimFromDesktop); a toast says how it went. A refusal
// says what Lody's does, whatever the server's words.
func (a *App) claimMachine() {
	cl := &a.claim
	if cl.request == "" || cl.busy || a.phase != phaseSignedIn || a.account == nil || a.convex == nil || a.noCloud() {
		return
	}
	machine := a.localMachine()
	if machine == "" {
		// The daemon is looked for every ten seconds; a frame then.
		if time.Since(cl.wake) > 10*time.Second {
			cl.wake = time.Now()
			time.AfterFunc(10*time.Second+50*time.Millisecond, a.redraw)
		}
		return
	}
	name, _ := os.Hostname()
	name = firstNonEmpty(strings.TrimSpace(name), machine)
	request, convex := cl.request, a.convex
	cl.busy = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := convex.Mutation(ctx, "machinePairing:claimFromDesktop", map[string]any{"requestId": request, "machineId": machine, "machineName": name}, nil)
		a.update(func() {
			cl.busy = false
			if cl.request == request {
				cl.request = ""
			}
			if err != nil {
				a.toast("error", locales.MachinePairingDesktopClaimFailed())
				return
			}
			a.toast("success", locales.MachinePairingDesktopClaimed())
		})
	}()
}

// linkDefault is whether the system opens lody:// links with this app,
// as last asked, and what came of making it their default.
type linkDefault struct {
	askedAt   time.Time
	known     bool
	isDefault bool
	pending   bool
	message   string
}

// The system's default app for lody:// links. They block until the main
// thread answers, so they are asked off it; tests swap them.
var (
	linkSchemeIsOurs = func() bool { return mygo.App.IsURLSchemeRegistered(linkScheme) }
	claimLinkScheme  = func() error { return mygo.App.RegisterURLScheme(linkScheme) }
)

// askLinkDefault asks the system again whether lody:// links open here,
// at most once a second while About shows, as Lody's asks as its window
// comes back.
func (a *App) askLinkDefault() {
	ld := &a.linkDefault
	if ld.pending || time.Since(ld.askedAt) < time.Second {
		return
	}
	ld.askedAt = time.Now()
	go func() {
		ok := linkSchemeIsOurs()
		a.update(func() { ld.known, ld.isDefault = true, ok })
	}()
}

// linkDefaultRow is About's first row, as Lody's: whether this app opens
// Lody's links from other apps, and Set as default when it doesn't.
func (a *App) linkDefaultRow(c *ui.Context, p *palette) {
	ld := &a.linkDefault
	a.askLinkDefault()
	settingRow(c.Key("link-default"), p, locales.DeepLinkDefaultHandler(), firstNonEmpty(ld.message, locales.DeepLinkDefaultHandlerHelp()), func() {
		label := locales.DeepLinkMakeDefault()
		if ld.isDefault {
			label = locales.DeepLinkDefaultHandlerActive()
		}
		disabled := !ld.known || ld.pending || ld.isDefault
		b := secondaryButton(c, p, label, disabled)
		if disabled {
			return
		}
		b.OnClick(func() {
			ld.pending = true
			go func() {
				err := claimLinkScheme()
				ok := err == nil && linkSchemeIsOurs()
				a.update(func() {
					ld.pending, ld.known, ld.isDefault, ld.askedAt = false, true, ok, time.Now()
					switch {
					case err != nil:
						ld.message = locales.DeepLinkDefaultHandlerFailed()
					case !ok:
						ld.message = locales.DeepLinkDefaultHandlerPending()
					default:
						ld.message = ""
					}
				})
			}()
		})
	})
}
