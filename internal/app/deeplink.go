package app

import (
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/convmd"
	"github.com/lbr77/lody-mygo/locales"
)

// Lody's links from other apps, as its desktop app routes them: the
// system hands the app the lody:// URLs (urlSchemes in mygo.json), and
// lody://session/<id>?workspace=<id> opens that conversation in its
// workspace, lody://chat/new?machine=…&project=…&workspaceSlug=… New chat
// on that machine's project, which `lody app <dir>` opens. A link that
// comes before the app can open it, as at launch, waits for the account
// and the workspace's catalog: at most 15 seconds, after which a
// conversation that never came is reported. A workspace the account
// lacks is reported at once; it never falls back to the one shown. Links
// name places and grant nothing, and any other route is left alone.

// linkScheme is the scheme of Lody's links.
const linkScheme = "lody"

// linkTimeout is how long a link waits for what it opens, as Lody's.
const linkTimeout = 15 * time.Second

// linkTarget is where a link goes: a conversation, or New chat.
type linkTarget struct {
	session, workspace, tab string
	chat                    bool
	machine, project, slug  string
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
	if u.Hostname() != "chat" || strings.Trim(u.Path, "/") != "new" {
		return nil
	}
	q := u.Query()
	t := &linkTarget{chat: true, machine: strings.TrimSpace(q.Get("machine")), project: strings.TrimSpace(q.Get("project")), slug: strings.TrimSpace(q.Get("workspaceSlug"))}
	if t.machine == "" || t.project == "" {
		return nil
	}
	return t
}

// OpenURL opens a link the system hands the app, bringing its window
// forward. Call it on the main thread.
func (a *App) OpenURL(raw string) {
	t := parseLink(raw)
	if t == nil {
		return
	}
	// The newest link wins; one waiting before it is dropped.
	t.until = time.Now().Add(linkTimeout)
	a.link = t
	if a.win != nil {
		a.win.Show()
		a.win.Focus()
	}
	// A frame once it is due, which gives up on it if it still waits.
	time.AfterFunc(linkTimeout+50*time.Millisecond, a.redraw)
	a.followLink()
	a.redraw()
}

// linkWorkspace is the workspace a link names, by id or by slug, else
// the one shown; "" when the account has no such workspace.
func (a *App) linkWorkspace(t *linkTarget) string {
	for _, w := range a.account.Workspaces {
		switch {
		case t.chat && t.slug != "" && w.Slug == t.slug,
			!t.chat && t.workspace != "" && w.ID == t.workspace,
			!t.chat && t.workspace == "" && w.ID == a.workspaceID,
			t.chat && t.slug == "" && w.ID == a.workspaceID:
			return w.ID
		}
	}
	return ""
}

// followLink opens the link waiting once it can, switching to its
// workspace first. It runs as the app draws, so that what a link waits
// for, as the catalog, opens it as it comes.
func (a *App) followLink() {
	t := a.link
	if t == nil {
		return
	}
	if time.Now().After(t.until) {
		// A conversation that never came is reported, as Lody's.
		a.link = nil
		if !t.chat {
			a.toast("error", locales.DeepLinkOpenFailed())
		}
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
