package app

import (
	"context"
	"net/url"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/preview"
	"github.com/lbr77/lody-mygo/locales"
)

// The Preview tab shows a web server the session runs, as Lody's Browser
// panel does, through the quick tunnel the machine opens to it. The page
// is the one thing the app shows in a web view, a page of someone else's,
// with none of the app's bindings.

const tabPreview = "preview"

type previewState struct {
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

// hasPreview reports whether the open session has a server to show.
func (a *App) hasPreview() bool {
	if a.view != nil && a.view.Preview != nil {
		return true
	}
	ps := a.previews[a.selected]
	return ps != nil && ps.url != ""
}

func (a *App) previewRequest(s *catalog.Session) preview.Request {
	return preview.Request{Workspace: a.conn.ID, Machine: s.MachineID, Session: s.ID, User: a.account.User.ID, Token: a.tokenNow()}
}

func (a *App) startPreview(s *catalog.Session, ps *previewState) {
	if a.conn == nil || a.view == nil || a.view.Preview == nil || ps.starting {
		return
	}
	conn, api, req, target := a.conn, a.api, a.previewRequest(s), a.view.Preview.Target
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

// previewStart offers to share the server, as only the session's owner
// may.
func (a *App) previewStart(c *ui.Context, p *palette, s *catalog.Session, ps *previewState) {
	pv := a.view.Preview
	ui.Column(c).Grow(1).Center().Padding(24).Gap(12).Children(func() {
		ui.Icon(c, iconGlobe).Size(28, 28).TextColor(p.Tertiary)
		if pv == nil {
			ui.Text(c, locales.PreviewNone()).FontSize(sizeSub).TextColor(p.Secondary)
			return
		}
		ui.Text(c, pv.Target.Label()).Font(fontMono).FontSize(sizeBody).FontWeight(600).TextColor(p.Text)
		ui.Text(c, locales.PreviewHint()).FontSize(sizeFoot).LineHeight(1.5).TextColor(p.Secondary).TextAlign(ui.Center).MaxWidth(300)
		switch {
		case s.UserID != "" && s.UserID != a.account.User.ID:
			ui.Text(c, locales.PreviewOwnerOnly()).FontSize(sizeFoot).TextColor(p.Tertiary).TextAlign(ui.Center).MaxWidth(300)
		case ps.starting:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(13, 13)
				ui.Text(c, locales.PreviewStarting()).FontSize(sizeSub).TextColor(p.Secondary)
			})
		case !a.presence.IsOnline(s.MachineID):
			ui.Text(c, locales.MentionOffline(a.machineName(s.MachineID))).FontSize(sizeFoot).TextColor(p.Tertiary)
		default:
			inkButton(c, p, locales.PreviewOpen(), nil, 32).OnClick(func() { a.startPreview(s, ps) })
		}
		if ps.err != "" {
			ui.Text(c, ps.err).FontSize(sizeFoot).TextColor(p.Danger).TextAlign(ui.Center).MaxWidth(320).Selectable()
		}
	})
}
