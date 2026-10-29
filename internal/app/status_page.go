package app

import (
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// Lody's StatusPage: the one frame every error and not-found screen
// stands in. No card: a page that has failed is the page, not a panel
// floating over one. It is centred, the drawing, what happened and the
// answers on one axis, which reads as a pause rather than an alarm; it
// opens with a drawing of what happened, says it in words, reassures, and
// offers the way out; the verbatim error and the details come after,
// quieter, for whoever needs them.

// statusLayout is where a status page stands, as Lody's StatusPageLayout.
type statusLayout int

const (
	// statusWindow is the whole window: the page ground, a strip that
	// drags the window, and its own scroll.
	statusWindow statusLayout = iota
	// statusPane fills a panel that already has a ground, as a session's.
	statusPane
)

// statusPage builds a status page: in the window, padded 32 above and
// 12% of the window's height below, since the eye's centre is above the
// geometric one and a column hung at 50% reads low; in a pane, 24 all
// round. Its column is 440 wide at most, 16 between its parts; the
// title 18 on 24, semibold, -0.01em, the description 14 on 20 in the
// secondary ink, 6 under the drawing's 8. announce is for a page that
// assistive technology hears of, as Lody's role alert or status: a
// failure the person did not ask for, or a wait that ran long; a place
// that is missing is not one. body is what follows: the answers, then
// what is verbatim.
func statusPage(c *ui.Context, p *palette, key string, layout statusLayout, art statusArt, announce bool, title, description string, body func()) {
	_, h := c.Size()
	// The page centres its column, which scrolls once it is taller than
	// the room, as Lody's column on auto margins in a scrolling page: a
	// MyGo scroll lays out on unbounded height, which leaves its margins
	// no room, so here the scroll shrinks to the room and the page
	// centres it.
	page := func() ui.Element {
		el := ui.Column(c.Key("status-page/" + key)).Justify(ui.Center).AlignItems(ui.Center)
		if announce {
			el.Role(ui.RoleStatus)
		}
		return el
	}
	var column func()
	scroll := func() {
		ui.Scroll(c.Key("status-scroll")).FillWidth().MinHeight(0).Shrink(1).AlignItems(ui.Center).Children(column)
	}
	column = func() {
		ui.Column(c.Key("status-column")).FillWidth().MaxWidth(440).MinWidth(0).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Column(c.Key("status-header")).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
				if art != "" {
					statusArtView(c, p, art)
				}
				level := 2
				if layout == statusWindow {
					level = 1
				}
				ui.Text(c, title).FontSize(sizeTitle).LineHeight(24.0 / 18).FontWeight(600).LetterSpacing(-0.18).TextColor(p.Strong).
					TextAlign(ui.Center).Role(ui.RoleHeading).Level(level)
				if description != "" {
					ui.Text(c, description).FontSize(sizeBody).LineHeight(20.0 / 14).TextColor(p.Secondary).TextAlign(ui.Center)
				}
			})
			if body != nil {
				body()
			}
		})
	}
	if layout == statusPane {
		page().FillWidth().Grow(1).MinHeight(0).Padding(24).Children(scroll)
		return
	}
	ui.Column(c.Key("status-window/" + key)).Absolute().Left(0).Top(0).Right(0).Bottom(0).Background(p.Canvas).Children(func() {
		page().Fill().Padding(32, 24, 0.12*h, 24).Children(scroll)
		// Above the page, as Lody's fixed drag strip: the window still
		// moves by its top.
		ui.Box(c.Key("status-drag")).Absolute().Left(0).Top(0).Right(0).Height(titleBarHeight).DragWindow()
	})
}

// statusActions are a page's answers, side by side and centred, 8 apart;
// the first is the one most likely to get the person out.
func statusActions(c *ui.Context, fn func()) {
	ui.Row(c.Key("status-actions")).Wrap().Gap(8).Justify(ui.Center).AlignItems(ui.Center).Children(fn)
}

// statusCode is Lody's StatusPageCode: verbatim text a person may need to
// read, select or send, the error itself or, as details, the diagnostics
// under it, in a block of its own: a 3% fill, 10 round, in mono 12 on 18
// (the details 11 on 16), kept to 144 high (the details to 40% of the
// window) and scrolling past it. action, the one thing done to the text,
// sits in its corner, on the text it acts on rather than among the ways
// out.
func statusCode(c *ui.Context, p *palette, key, text string, details bool, action func()) {
	_, h := c.Size()
	size, lead, most := float32(sizeFoot), float32(18), float32(144)
	if details {
		size, lead, most = sizeCaption, 16, 0.4*h
	}
	ui.Box(c.Key("status-code/" + key)).FillWidth().MinWidth(0).Children(func() {
		block := ui.Scroll(c.Key("block")).FillWidth().MaxHeight(most).Radius(10).Background(p.Text.Alpha(0.03))
		if action != nil {
			block.Padding(10, 36, 10, 12)
		} else {
			block.Padding(10, 12)
		}
		block.Children(func() {
			ui.Text(c, text).Font(fontMono).FontSize(size).LineHeight(lead / size).TextColor(p.Secondary).TextAlign(ui.Start).FillWidth().Selectable()
		})
		if action != nil {
			ui.Row(c.Key("action")).Absolute().Top(6).Right(6).Children(action)
		}
	})
}

// statusDetails is a disclosure for what most people never need to see:
// its label in the footnote type, secondary until hovered, its chevron
// turning a quarter as it opens, both over 120ms on ease.standard.
func statusDetails(c *ui.Context, p *palette, key, label string, open *bool, fn func()) {
	ui.Column(c.Key("status-details/" + key)).FillWidth().MinWidth(0).Gap(8).Children(func() {
		b := ui.ButtonBase(c.Key("disclosure")).AlignSelf(ui.Center).Gap(4).AlignItems(ui.Center).Cursor(ui.CursorPointer).
			Label(label).Expanded(*open).OnClick(func() { *open = !*open })
		ink := p.Secondary
		if b.Hovered() {
			ink = p.Text
		}
		ink = b.AnimateColor("ink", ink, ui.Animation{Duration: durFast, Ease: easeStandard})
		turn := b.AnimateWith("turn", b2f(*open), durFast, easeStandard)
		b.Children(func() {
			ui.Icon(c, iconChevronRight).Size(14, 14).TextColor(ink).Rotate(90 * turn)
			ui.Text(c, label).FontSize(sizeFoot).LineHeight(16.0 / 12).TextColor(ink)
		})
		if *open {
			fn()
		}
	})
}

// statusCopy is a mini ghost button that copies the error it sits on, its
// copy mark a check in the success ink once it has.
func statusCopy(c *ui.Context, p *palette, copied bool, copy func()) {
	label := locales.ErrorCopyDetails()
	if copied {
		label = locales.RecoveryCopied()
	}
	b := ui.ButtonBase(c.Key("status-copy")).Size(24, 24).Radius(6).Cursor(ui.CursorPointer).Label(label).Transition(colorFade).OnClick(copy)
	tip(c, p, b, label)
	ink := p.Secondary
	if b.Hovered() {
		b.Background(p.Text.Alpha(0.06))
		ink = p.Text
	}
	icon := iconCopy
	if copied {
		icon, ink = iconCheck, p.Success
	}
	b.Children(func() { ui.Icon(c, icon).Size(14, 14).TextColor(ink) })
}

// sessionMissing is whether the session open is not in the workspace, as
// Lody's session detail decides it: only once the workspace's catalog
// has come from the cloud, since one not yet there may yet arrive, as a
// session shared or made on another device.
func (a *App) sessionMissing() bool {
	return a.selected != "" && a.page == "" && a.synced && a.catalog.Session(a.selected) == nil
}

// sessionNotFound stands in the session's place when the session it was
// opened for cannot be found, as Lody's SessionNotFound, with the way
// back to the sessions.
func (a *App) sessionNotFound(c *ui.Context, p *palette) {
	a.tabBar(c, p, nil)
	statusPage(c, p, "session-not-found", statusPane, artMissing, false, locales.SessionNotFoundTitle(), locales.SessionNotFoundDescription(), func() {
		statusActions(c, func() {
			lodyButton(c, p, "secondary", "back-to-sessions", nil, locales.SessionNotFoundBack(), true).OnClick(func() { a.open("") })
		})
	})
}

// listFailure is a conversation that could not be shown, as Lody's
// message-list fallback keeps it: when, which its report stamps, and
// whether its error was copied and its details are open, until it is
// tried again.
type listFailure struct {
	session string
	at      time.Time
	copied  bool
	details bool
}

// listFailed is whether the conversation stopped loading for good: its
// replica gave up, as on a history past its limit, and shows nothing it
// can keep up to date.
func listFailed(v *session.View) bool { return v != nil && v.Status == "offline" }

// listError is what stopped it: the replica's reason, its limit in the
// words of its error.
func listError(v *session.View) string {
	if v.Reason == "session_limit" {
		return "session: history exceeds the limit"
	}
	return firstNonEmpty(v.Reason, "sync_failed")
}

// listReport is what the failure's copy and details hold, as Lody's crash
// report: the error, then where and on what it happened.
func (a *App) listReport(v *session.View) (summary, details string) {
	return listError(v), strings.Join([]string{
		"Boundary: SessionChatStream",
		"Runtime: native",
		"OS: " + runtime.GOOS + "/" + runtime.GOARCH,
		"App version: " + clientVersion(),
		"Workspace: " + a.workspaceID,
		"Session: " + a.selected,
		"Time: " + a.listFail.at.UTC().Format("2006-01-02T15:04:05.000Z"),
	}, "\n")
}

// messageListFailed is Lody's MessageListErrorFallback in the
// conversation's place: the list failed but the composer under it did not,
// so the page says so, keeps the draft, and offers a retry that loads the
// conversation again; the error and the report come after, to copy.
func (a *App) messageListFailed(c *ui.Context, p *palette, sess *catalog.Session, v *session.View) {
	f := &a.listFail
	if f.session != a.selected {
		*f = listFailure{session: a.selected, at: time.Now()}
	}
	summary, details := a.listReport(v)
	report := summary + "\n\n" + details
	statusPage(c, p, "list-failed", statusPane, artBroken, true, locales.MessageListFailedTitle(), locales.MessageListFailed(), func() {
		statusActions(c, func() {
			lodyButton(c, p, "primary", "try-again", nil, locales.TryAgain(), true).OnClick(a.retryConversation)
		})
		statusCode(c, p, "error", summary, false, func() {
			statusCopy(c, p, f.copied, func() {
				writeClipboard(report)
				f.copied = true
			})
		})
		statusDetails(c, p, "report", locales.ErrorTechnicalDetails(), &f.details, func() {
			statusCode(c, p, "report", report, true, nil)
		})
	})
	a.bottom(c, p, sess, v)
}

// retryConversation loads the conversation again, as Lody's retry
// remounts the list alone: its skeleton until it comes.
func (a *App) retryConversation() {
	a.listFail = listFailure{}
	a.closeSession()
	if a.selected != "" && a.conn != nil {
		a.openReplica(a.selected)
	}
}
