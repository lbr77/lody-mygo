package app

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/preview"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// The Preview tab shows a web server the session runs, as Lody's Browser
// panel does, through the quick tunnel the machine opens to it. The page
// is the one thing the app shows in a web view, a page of someone else's,
// with none of the app's bindings.

const tabPreview = "preview"

type previewState struct {
	address  string // what the address bar holds
	url      string
	starting bool
	stopping bool
	err      string
	web      *mygo.WebView
	creating bool
	webErr   string
}

func (a *App) previewFor(id string) *previewState {
	if a.previews == nil {
		a.previews = map[string]*previewState{}
	}
	ps := a.previews[id]
	if ps == nil {
		ps = &previewState{}
		a.previews[id] = ps
	}
	return ps
}

func (a *App) previewRequest(s *catalog.Session) preview.Request {
	return preview.Request{Workspace: a.conn.ID, Machine: s.MachineID, Session: s.ID, User: a.account.User.ID, Token: a.tokenNow()}
}

// previewAddress reads what was typed in the address bar: a port, or a
// URL on this machine's loopback, as Lody's browser panel takes it.
func previewAddress(s string) (session.PreviewTarget, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return session.PreviewTarget{}, false
	}
	if _, err := strconv.Atoi(s); err == nil {
		s = "localhost:" + s
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return session.PreviewTarget{}, false
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return session.PreviewTarget{}, false
	}
	host := strings.ToLower(u.Hostname())
	if host == "::1" {
		host = "[::1]"
	}
	switch host {
	case "localhost", "127.0.0.1", "[::1]":
	default:
		return session.PreviewTarget{}, false
	}
	t := session.PreviewTarget{Protocol: u.Scheme, Host: host, Port: port}
	if p := u.EscapedPath(); p != "" && p != "/" || u.RawQuery != "" {
		t.Path = "/" + strings.TrimPrefix(p, "/")
		if u.RawQuery != "" {
			t.Path += "?" + u.RawQuery
		}
	}
	return t, port > 0 && port < 65536
}

func (a *App) startPreview(s *catalog.Session, ps *previewState) {
	target, ok := previewAddress(ps.address)
	if !ok {
		ps.err = locales.PreviewBadAddress()
		return
	}
	if a.conn == nil || ps.starting {
		return
	}
	conn, api, req := a.conn, a.api, a.previewRequest(s)
	ps.starting, ps.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		viewer, err := preview.Start(ctx, conn, api, req, target)
		a.update(func() {
			ps.starting = false
			if err != nil {
				ps.err = err.Error()
				return
			}
			ps.url = viewer
		})
	}()
}

func (a *App) stopPreview(s *catalog.Session, ps *previewState) {
	if a.conn == nil || ps.stopping {
		return
	}
	conn, api, req := a.conn, a.api, a.previewRequest(s)
	ps.stopping, ps.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		err := preview.Stop(ctx, conn, api, req)
		a.update(func() {
			ps.stopping = false
			if err != nil {
				ps.err = err.Error()
				return
			}
			if ps.web != nil {
				ps.web.Destroy()
			}
			ps.url, ps.web, ps.webErr = "", nil, ""
		})
	}()
}

// previewTab shows the session's server: a way to share it, then the page.
func (a *App) previewTab(c *ui.Context, p *palette) {
	s := a.catalog.Session(a.selected)
	if s == nil {
		return
	}
	ps := a.previewFor(s.ID)
	pv := a.view.Preview
	if ps.url == "" && pv != nil && pv.PublicURL != "" {
		// The machine already shares it.
		if viewer, ok := preview.ViewerURL(pv.PublicURL, pv.Target.Path); ok {
			ps.url = viewer
		}
	}
	if ps.url == "" {
		a.previewStart(c, p, s, ps)
		return
	}
	ui.Row(c).Shrink(0).Height(36).Padding(0, 6, 0, 10).Gap(4).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.Border).Children(func() {
		ui.Icon(c, iconGlobe).Size(14, 14).TextColor(p.Secondary).Shrink(0)
		label := ps.url
		if u, err := url.Parse(ps.url); err == nil {
			label = u.Host + u.Path
			if pv != nil {
				label = pv.Target.Label() + u.Path
			}
		}
		ui.Text(c, label).Font(fontMono).FontSize(sizeFoot).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
		if ps.web != nil {
			iconButton(c, p, iconRefresh, locales.Refresh(), p.Hover).OnClick(func() { ps.web.Page().Reload() })
		}
		viewer := ps.url
		iconButton(c, p, iconExternal, locales.OpenInBrowser(), p.Hover).OnClick(func() { mygo.Shell.OpenExternal(viewer) })
		if ps.stopping {
			ui.Spinner(c).Size(14, 14).Margin(0, 7)
		} else if s.UserID == "" || s.UserID == a.account.User.ID {
			iconButton(c, p, iconCircleStop, locales.PreviewStop(), p.Hover).OnClick(func() { a.stopPreview(s, ps) })
		}
	})
	if ps.err != "" {
		ui.Text(c, ps.err).FontSize(sizeFoot).TextColor(p.Danger).Padding(6, 12).Selectable()
	}
	if ps.web == nil && ps.webErr == "" && !ps.creating && a.win != nil {
		// Web views are made between frames, not as the view builds.
		ps.creating = true
		win, address := a.win, ps.url
		a.update(func() {
			web, err := win.NewWebView(mygo.WebViewOptions{URL: address, Page: mygo.PageOptions{Untrusted: true}})
			ps.creating, ps.web = false, web
			if err != nil {
				ps.webErr = err.Error()
			}
		})
	}
	if ps.creating {
		panelStatus(c, p, true, locales.Loading(), "")
		return
	}
	if ps.web == nil {
		// No web view to show it in: the address, to open elsewhere.
		ui.Column(c).Grow(1).Center().Padding(24).Gap(10).Children(func() {
			ui.Text(c, firstNonEmpty(ps.webErr, locales.PreviewNoWebView())).FontSize(sizeSub).TextColor(p.Secondary).TextAlign(ui.Center)
			ui.Text(c, ps.url).Font(fontMono).FontSize(sizeCaption).TextColor(p.Tertiary).Selectable().TextAlign(ui.Center)
		})
		return
	}
	ui.WebView(c, ps.web).Grow(1).MinHeight(0).FillWidth()
}

// previewStart is the address bar: the server the agent reported, or one
// typed, and the way to share it, as only the session's owner may.
func (a *App) previewStart(c *ui.Context, p *palette, s *catalog.Session, ps *previewState) {
	pv := a.view.Preview
	if ps.address == "" && pv != nil {
		ps.address = pv.Target.Label() + pv.Target.Path
	}
	ui.Column(c).Grow(1).Center().Padding(24).Gap(12).Children(func() {
		ui.Icon(c, iconGlobe).Size(28, 28).TextColor(p.Tertiary)
		if pv != nil {
			ui.Text(c, locales.PreviewReported(pv.Target.Label())).FontSize(sizeSub).TextColor(p.Secondary).TextAlign(ui.Center)
		}
		owner := s.UserID == "" || s.UserID == a.account.User.ID
		ui.Row(c).Gap(8).AlignItems(ui.Center).Width(320).MaxWidth(320).Children(func() {
			ui.TextInput(c, &ps.address).Placeholder("localhost:3000").Font(fontMono).FontSize(sizeSub).Grow(1).
				Label(locales.PreviewAddress()).Disabled(!owner || ps.starting).OnSubmit(func() { a.startPreview(s, ps) })
			if ps.starting {
				ui.Spinner(c).Size(14, 14)
				return
			}
			inkButton(c, p, locales.PreviewOpen(), nil, 30).Disabled(!owner || !a.presence.IsOnline(s.MachineID)).OnClick(func() { a.startPreview(s, ps) })
		})
		switch {
		case !owner:
			ui.Text(c, locales.PreviewOwnerOnly()).FontSize(sizeFoot).TextColor(p.Tertiary).TextAlign(ui.Center).MaxWidth(320)
		case ps.starting:
			ui.Text(c, locales.PreviewStarting()).FontSize(sizeFoot).TextColor(p.Secondary).TextAlign(ui.Center)
		case !a.presence.IsOnline(s.MachineID):
			ui.Text(c, locales.MentionOffline(a.machineName(s.MachineID))).FontSize(sizeFoot).TextColor(p.Tertiary)
		default:
			ui.Text(c, locales.PreviewHint()).FontSize(sizeFoot).LineHeight(1.5).TextColor(p.Secondary).TextAlign(ui.Center).MaxWidth(320)
		}
		if ps.err != "" {
			ui.Text(c, ps.err).FontSize(sizeFoot).TextColor(p.Danger).TextAlign(ui.Center).MaxWidth(320).Selectable()
		}
	})
}
