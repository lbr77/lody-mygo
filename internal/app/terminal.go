package app

import (
	"context"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/localterm"
	"github.com/lbr77/lody-mygo/locales"
)

// The terminal dock under the conversation, as Lody's desktop app has it:
// the shells the Lody daemon keeps for a session, in the session's working
// directory. The daemon serves them only on its own machine, so a session
// has terminals only when it runs on this one.

type termDock struct {
	open     bool
	percent  float32 // the conversation's share of the column's height
	machine  string  // this machine's id, as its daemon knows it
	checked  time.Time
	client   *localterm.Client
	dialing  bool
	err      string
	sessions map[string]*termSession
}

type termSession struct {
	loading bool
	listed  bool
	err     string
	tabs    []*termTab
	active  string
}

type termTab struct {
	id       string
	title    string
	view     *terminal.Terminal
	conn     *localterm.Conn
	starting bool
	err      string
}

// localMachine is this machine's id when a Lody daemon has run here,
// checked again every ten seconds while it isn't.
func (a *App) localMachine() string {
	t := &a.term
	if t.machine == "" && time.Since(t.checked) > 10*time.Second {
		t.machine, t.checked = localterm.MachineID(), time.Now()
	}
	return t.machine
}

func (a *App) toggleTerminal() {
	if a.localMachine() != "" && a.catalog.Session(a.selected) != nil {
		a.term.open = !a.term.open
	}
}

func (a *App) termSession(id string) *termSession {
	if a.term.sessions == nil {
		a.term.sessions = map[string]*termSession{}
	}
	ts := a.term.sessions[id]
	if ts == nil {
		ts = &termSession{}
		a.term.sessions[id] = ts
	}
	return ts
}

// dialTerminals connects to the daemon, and forgets every terminal once
// the connection ends, to connect anew.
func (a *App) dialTerminals() {
	t := &a.term
	if t.client != nil || t.dialing || t.err != "" {
		return
	}
	t.dialing, t.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		client, err := localterm.Dial(ctx)
		a.update(func() {
			t.dialing = false
			if err != nil {
				t.err = err.Error()
				return
			}
			t.client = client
		})
		if err != nil {
			return
		}
		<-client.Done()
		a.update(func() {
			if t.client != client {
				return
			}
			t.client = nil
			for _, ts := range t.sessions {
				for _, tab := range ts.tabs {
					if tab.view != nil {
						tab.view.Close()
					}
				}
			}
			t.sessions = nil
			t.err = locales.TerminalDisconnected()
		})
	}()
}

// listTerminals finds the session's terminals, and opens one when it has
// none.
func (a *App) listTerminals(sessionID string, ts *termSession) {
	client := a.term.client
	if client == nil || ts.loading || ts.listed || ts.err != "" {
		return
	}
	ts.loading, ts.err = true, ""
	go func() {
		ctx := context.Background()
		list, err := client.List(ctx, sessionID)
		if err == nil && len(list) == 0 {
			var info localterm.Info
			if info, err = client.Open(ctx, sessionID, 100, 24); err == nil {
				list = []localterm.Info{info}
			}
		}
		a.update(func() {
			ts.loading, ts.listed = false, err == nil
			if err != nil {
				ts.err = err.Error()
				return
			}
			known := map[string]*termTab{}
			for _, tab := range ts.tabs {
				known[tab.id] = tab
			}
			tabs := ts.tabs[:0:0]
			for _, info := range list {
				tab := known[info.ID]
				if tab == nil {
					tab = &termTab{id: info.ID, title: info.Title}
				}
				tabs = append(tabs, tab)
			}
			ts.tabs = tabs
			if len(ts.tabs) > 0 && ts.active == "" {
				ts.active = ts.tabs[0].id
			}
		})
	}()
}

// newTerminal opens another terminal for the session.
func (a *App) newTerminal(sessionID string, ts *termSession) {
	client := a.term.client
	if client == nil || len(ts.tabs) >= localterm.MaxPerSession {
		return
	}
	go func() {
		info, err := client.Open(context.Background(), sessionID, 100, 24)
		a.update(func() {
			if err != nil {
				ts.err = err.Error()
				return
			}
			ts.err = ""
			ts.tabs = append(ts.tabs, &termTab{id: info.ID})
			ts.active = info.ID
		})
	}()
}

// closeTerminal ends a terminal and its shell.
func (a *App) closeTerminal(ts *termSession, tab *termTab) {
	if a.term.client != nil {
		go a.term.client.Kill(tab.id)
	}
	a.dropTab(ts, tab)
}

func (a *App) dropTab(ts *termSession, tab *termTab) {
	if tab.view != nil {
		tab.view.Close()
	}
	for i, t := range ts.tabs {
		if t == tab {
			ts.tabs = append(ts.tabs[:i], ts.tabs[i+1:]...)
			break
		}
	}
	if ts.active == tab.id {
		ts.active = ""
		if len(ts.tabs) > 0 {
			ts.active = ts.tabs[len(ts.tabs)-1].id
		}
	}
}

// attachTab follows a terminal in a view of its own, made once it shows.
func (a *App) attachTab(ts *termSession, tab *termTab) {
	client := a.term.client
	if client == nil || tab.view != nil || tab.starting || tab.err != "" {
		return
	}
	tab.starting = true
	go func() {
		conn, err := client.Attach(tab.id, 100, 24, func(title string) {
			a.update(func() { tab.title = title })
		})
		var view *terminal.Terminal
		if err == nil {
			view, err = terminal.New(terminal.Options{
				Conn: conn,
				Font: terminal.Font{Family: "JetBrains Mono, SF Mono, Menlo, monospace", Size: 12},
				OnExit: func(int) {
					a.update(func() { a.dropTab(ts, tab) })
				},
			})
			if err != nil {
				conn.Close()
			}
		}
		a.update(func() {
			tab.starting = false
			if err != nil {
				tab.err = err.Error()
				return
			}
			tab.view, tab.conn = view, conn
		})
	}()
}

// terminalDock is the dock: the session's terminals as tabs over the one
// shown.
func (a *App) terminalDock(c *ui.Context, p *palette, sess *catalog.Session) {
	local := sess.MachineID == a.localMachine()
	ts := a.termSession(sess.ID)
	if local {
		a.dialTerminals()
		a.listTerminals(sess.ID, ts)
	}
	var shown *termTab
	for _, tab := range ts.tabs {
		if tab.id == ts.active {
			shown = tab
		}
	}
	ui.Column(c).Fill().Background(p.Canvas).Children(func() {
		ui.Row(c).Height(32).Shrink(0).Padding(0, 6, 0, 10).Gap(10).AlignItems(ui.Center).
			BorderWidth(1, 0, 1, 0).BorderColor(p.Border).Children(func() {
			ui.Row(c).Grow(1).MinWidth(0).Gap(10).AlignItems(ui.Center).Children(func() {
				for _, tab := range ts.tabs {
					a.terminalTab(c.Key(tab.id), p, ts, tab, tab == shown)
				}
				if local && a.term.client != nil {
					// New terminal is a bare glyph, inking on hover at once, as
					// Lody's.
					limited := len(ts.tabs) >= localterm.MaxPerSession
					add := ui.ButtonBase(c).Size(20, 20).Radius(5).Center().Label(locales.TerminalNew()).Disabled(limited)
					tip(c, p, add, locales.TerminalNew())
					ink := p.Secondary
					if limited {
						add.Opacity(0.4)
					} else {
						add.Cursor(ui.CursorPointer)
						if add.Hovered() {
							ink = p.Text
						}
					}
					add.OnClick(func() { a.newTerminal(sess.ID, ts) })
					add.Children(func() { ui.Icon(c, iconPlus).Size(13, 13).TextColor(ink) })
				}
				if err := firstNonEmpty(ts.err, a.term.err); err != "" && local {
					tip(c, p, ui.Text(c, err).FontSize(sizeCaption).TextColor(p.Danger).SingleLine().MinWidth(0).Shrink(1), err)
				}
			})
			iconButton(c, p, iconChevronDown, locales.TerminalHide(), p.Hover).Size(24, 24).OnClick(func() { a.term.open = false })
		})
		switch {
		case !local:
			panelStatus(c, p, false, locales.TerminalRemote(a.machineName(sess.MachineID)), "")
		case shown != nil && shown.view != nil:
			if terminal.View(c.Key("term/"+shown.id), shown.view).Grow(1).MinHeight(0).FillWidth().AutoFocus().Focused() {
				a.surfNext.termFocused = true
			}
		case shown != nil && shown.err != "":
			panelStatus(c, p, false, locales.TerminalFailed(), shown.err)
		case shown != nil:
			a.attachTab(ts, shown)
			panelStatus(c, p, true, locales.TerminalStarting(), "")
		case a.term.dialing || ts.loading:
			panelStatus(c, p, true, locales.TerminalStarting(), "")
		case a.term.client == nil || ts.err != "":
			ui.Column(c).Grow(1).Center().Gap(10).Children(func() {
				ui.Text(c, firstNonEmpty(ts.err, a.term.err, locales.TerminalUnavailable())).FontSize(sizeSub).TextColor(p.Secondary).TextAlign(ui.Center)
				ghostButton(c, p, locales.TryAgain()).OnClick(func() {
					a.term.err, ts.err, ts.listed = "", "", false
				})
			})
		default:
			panelStatus(c, p, false, locales.TerminalNone(), "")
		}
	})
}

func (a *App) terminalTab(c *ui.Context, p *palette, ts *termSession, tab *termTab, active bool) {
	title := firstNonEmpty(tab.title, "shell")
	// The tab's colors and ink fade over 150ms, as Lody's transition-colors.
	row := ui.Row(c).Height(32).Gap(4).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(title).Shrink(1).MinWidth(0).Transition(twFade)
	hovered := row.Hovered()
	ink := p.Tertiary
	if active || hovered {
		ink = p.Text
	}
	ink = row.AnimateColor("ink", ink, ui.Animation{Duration: 150 * time.Millisecond, Ease: twEase})
	if active {
		row.BorderWidth(0, 0, 1.5, 0).BorderColor(p.Text)
	}
	id := tab.id
	row.OnClick(func() { ts.active = id })
	row.Children(func() {
		ui.Icon(c, iconSquareTerminal).Size(12, 12).TextColor(ink).Opacity(0.8)
		ui.Text(c, title).FontSize(11).TextColor(ink).SingleLine().MinWidth(0).MaxWidth(160)
		// Close fades over 150ms, as Lody's transition-opacity: the shown
		// tab's rests at 0.7 and comes up under the pointer; another's
		// shows only while its tab has the pointer.
		x := ui.ButtonBase(c).Size(16, 16).Radius(4).Center().Cursor(ui.CursorPointer).Label(locales.TerminalClose(title))
		shown := float32(0)
		switch {
		case x.Hovered():
			shown = 1
		case active:
			shown = 0.7
		case hovered:
			shown = 1
		}
		x.Opacity(x.AnimateWith("shown", shown, 150*time.Millisecond, twEase))
		x.OnClick(func() { a.closeTerminal(ts, tab) })
		x.Children(func() { ui.Icon(c, iconX).Size(10, 10).TextColor(ink) })
	})
}
