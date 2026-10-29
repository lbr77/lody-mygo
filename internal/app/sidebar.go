package app

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
)

const sidebarWidth = 280

type sideRow struct {
	header  bool
	group   string
	label   string
	project string // for New chat in this project
	machine string
	count   int
	session *catalog.Session
	child   bool
}

// sideRows groups the sessions as Lody's Project mode does: pinned first,
// then each project, then chats outside any project.
func (a *App) sideRows() []sideRow {
	var rows []sideRow
	sessions := a.catalog.Sessions
	byParent := map[string][]*catalog.Session{}
	visible := map[string]bool{}
	for i := range sessions {
		if !sessions[i].Archived {
			visible[sessions[i].ID] = true
		}
	}
	for i := range sessions {
		s := &sessions[i]
		if s.Archived {
			continue
		}
		if parent := s.ParentSessionID; parent != "" && visible[parent] {
			byParent[parent] = append(byParent[parent], s)
		}
	}
	isChild := func(s *catalog.Session) bool { return s.ParentSessionID != "" && visible[s.ParentSessionID] }
	add := func(group, label, project, machine string, list []*catalog.Session) {
		if len(list) == 0 {
			return
		}
		rows = append(rows, sideRow{header: true, group: group, label: label, project: project, machine: machine, count: len(list)})
		if a.collapsed[group] {
			return
		}
		for _, s := range list {
			rows = append(rows, sideRow{group: group, session: s})
			for _, child := range byParent[s.ID] {
				rows = append(rows, sideRow{group: group, session: child, child: true})
			}
		}
	}

	var pinned []*catalog.Session
	byProject := map[string][]*catalog.Session{}
	var chats []*catalog.Session
	for i := range sessions {
		s := &sessions[i]
		if s.Archived || isChild(s) {
			continue
		}
		switch {
		case s.Pinned:
			pinned = append(pinned, s)
		case a.catalog.Project(s.ProjectID) != nil:
			byProject[s.ProjectID] = append(byProject[s.ProjectID], s)
		default:
			chats = append(chats, s)
		}
	}
	add("pinned", "Pinned", "", "", pinned)
	for _, p := range a.catalog.Projects {
		label := p.Name
		if len(a.catalog.MachineIDs) > 1 {
			if m := a.catalog.MachineNames[p.MachineID]; m != "" {
				label += " · " + m
			}
		}
		add("project:"+p.ID, label, p.ID, p.MachineID, byProject[p.ID])
	}
	add("chats", "Chats", "", "", chats)
	return rows
}

func (a *App) sidebar(c *ui.Context, p *palette) {
	ui.Column(c).Width(sidebarWidth).Shrink(0).FillHeight().Children(func() {
		// The traffic lights sit on the left of this row.
		ui.Row(c).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Justify(ui.End).Padding(0, 6).DragWindow().Children(func() {
			iconButton(c, p, iconSidebar, "Hide sidebar", p.SideHover).OnClick(func() { a.sidebarHidden = true })
		})
		ui.Column(c).Padding(0, 6).Gap(1).Shrink(0).Children(func() {
			if navRow(c, p, iconNewChat, "New chat", a.selected == "").Clicked() {
				a.open("")
			}
		})
		rows := a.sideRows()
		a.sessionList.Key = func(i int) any {
			if i >= len(rows) {
				return i
			}
			if rows[i].header {
				return "h:" + rows[i].group
			}
			return rows[i].group + "/" + rows[i].session.ID
		}
		ui.List(c, &a.sessionList, len(rows), func(i int) {
			if rows[i].header {
				a.groupHeader(c, p, rows[i])
			} else {
				a.sessionRow(c, p, rows[i])
			}
		}).Grow(1).MinHeight(0).Padding(12, 6, 12, 6).Gap(1).Children(func() {
			if len(rows) == 0 {
				msg := "No chats yet"
				if !a.synced {
					msg = "Syncing workspace…"
				}
				ui.Text(c, msg).FontSize(sizeSub).TextColor(p.SideMuted).Margin(24, 10)
			}
		})
		a.footer(c, p)
	})
}

// navRow is a 28px sidebar navigation row.
func navRow(c *ui.Context, p *palette, icon *ui.SVG, label string, selected bool) ui.Element {
	row := ui.ButtonBase(c).Height(28).Padding(4, 9).Radius(8).Gap(6).Justify(ui.Start).Cursor(ui.CursorPointer).Label(label)
	ink, iconInk := p.SideRow, p.SideMuted
	switch {
	case selected:
		row.Background(p.SideSelect)
		ink, iconInk = p.SideRowSel, p.SideRowSel
	case row.Hovered():
		row.Background(p.SideHover)
		iconInk = p.SideRow
	}
	row.Children(func() {
		ui.Icon(c, icon).Size(16, 16).TextColor(iconInk)
		ui.Text(c, label).FontSize(sizeSub).TextColor(ink)
	})
	return row
}

func (a *App) groupHeader(c *ui.Context, p *palette, r sideRow) {
	row := ui.Row(c.Key("h:"+r.group)).Height(28).Padding(0, 4, 0, 9).Gap(4).AlignItems(ui.Center).Radius(8).Cursor(ui.CursorPointer)
	if !a.collapsed[r.group] {
		row.Margin(0, 0, 4, 0)
	}
	hovered := row.Hovered()
	row.OnClick(func() {
		a.collapsed[r.group] = !a.collapsed[r.group]
		a.savePrefs()
	})
	row.Children(func() {
		ui.Text(c, r.label).FontSize(sizeFoot).FontWeight(500).TextColor(p.GroupLabel).SingleLine().MinWidth(0)
		if hovered {
			icon := iconChevronDown
			if a.collapsed[r.group] {
				icon = iconChevronRight
			}
			ui.Icon(c, icon).Size(12, 12).TextColor(p.GroupLabel)
		}
		ui.Spacer(c)
		if hovered && r.project != "" {
			b := ui.ButtonBase(c).Size(24, 24).Radius(4).Cursor(ui.CursorPointer).Label("New chat in " + r.label).Tooltip("New chat")
			if b.Hovered() {
				b.Background(p.SideHover)
			}
			if b.Clicked() {
				a.newChat.machine, a.newChat.project = r.machine, r.project
				a.pickNewChatDefaults()
				a.open("")
			}
			b.Children(func() { ui.Icon(c, iconPlus).Size(14, 14).TextColor(p.SideMuted) })
		}
	})
}

func (a *App) sessionRow(c *ui.Context, p *palette, r sideRow) {
	s := r.session
	selected := a.selected == s.ID
	row := ui.Row(c.Key(r.group+"/"+s.ID)).Height(28).Padding(3, 8).Radius(8).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(s.Title)
	if r.child {
		row.Padding(3, 8, 3, 22)
	}
	switch {
	case selected:
		row.Background(p.SideSelect)
	case row.Hovered():
		row.Background(p.SideHover)
	}
	if row.Clicked() {
		a.open(s.ID)
	}
	ink := p.SideRow
	if selected {
		ink = p.SideRowSel
	}
	row.Children(func() {
		if s.Pinned && r.group != "pinned" {
			ui.Icon(c, iconPin).Size(12, 12).TextColor(p.SideMuted)
		}
		ui.Text(c, s.Title).FontSize(sizeSub).TextColor(ink).SingleLine().Grow(1).MinWidth(0)
		a.statusMark(c, p, s)
	})
}

// statusMark is the one mark at a row's end: waiting, working, unread, or
// the time of the last message.
func (a *App) statusMark(c *ui.Context, p *palette, s *catalog.Session) {
	switch {
	case s.Waiting():
		ui.Icon(c, iconHand).Size(12, 12).TextColor(p.Primary).Tooltip("Waiting for you")
	case s.Working():
		workingGrid(c, p, 12)
	case s.Unread() && a.selected != s.ID:
		ui.Box(c).Size(8, 8).Radius(4).Background(p.Primary).Margin(0, 2)
	default:
		if at := s.LastMessageAt; at > 0 {
			ui.Text(c, relativeTime(time.UnixMilli(int64(at)))).FontSize(sizeFoot).TextColor(p.SideMuted).Shrink(0)
		}
	}
}

// workingGrid is Lody's working mark: a 3×3 grid of tiles pulsing in a
// wave.
func workingGrid(c *ui.Context, p *palette, size float32) {
	tile := size / 3.36
	gap := tile * 0.18
	phase := float64(time.Now().UnixMilli()%1400) / 1400
	ui.Column(c).Size(size, size).Shrink(0).Gap(gap).Center().Label("Working").Children(func() {
		for row := 0; row < 3; row++ {
			ui.Row(c).Gap(gap).Children(func() {
				for col := 0; col < 3; col++ {
					wave := math.Sin(2*math.Pi*(phase-float64(row+col)/6))*0.5 + 0.5
					ui.Box(c).Size(tile, tile).Radius(tile * 0.4).Background(p.Primary).Opacity(float32(0.3 + 0.7*wave))
				}
			})
		}
	})
	c.After(80 * time.Millisecond)
}

func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	case d < 7*24*time.Hour:
		return strconv.Itoa(int(d.Hours()/24)) + "d"
	}
	return t.Format("Jan 2")
}

// footer holds the workspace switcher.
func (a *App) footer(c *ui.Context, p *palette) {
	ws := a.workspace()
	name := "Workspace"
	if ws != nil {
		name = ws.Name
	}
	ui.Row(c).Shrink(0).Padding(6, 10).Gap(4).AlignItems(ui.Center).Children(func() {
		switcher := ui.ButtonBase(c).Height(28).Padding(0, 6).Radius(8).Gap(8).Grow(1).MinWidth(0).Justify(ui.Start).Cursor(ui.CursorPointer).Label("Switch workspace")
		if switcher.Hovered() || a.workspacesOpen {
			switcher.Background(p.Text.Alpha(0.06))
		}
		switcher.OnClick(func() { a.workspacesOpen = !a.workspacesOpen })
		switcher.Children(func() {
			avatar(c, p, name, 16, 4)
			ui.Text(c, name).FontSize(sizeSub).FontWeight(500).TextColor(p.SideRow).SingleLine().MinWidth(0)
			if !a.syncState.Live && a.sync != nil {
				label := "Syncing"
				color := p.SideMuted
				if a.syncState.Reason != "" {
					label, color = "Offline", p.Danger
				}
				ui.Text(c, label).FontSize(10).FontWeight(500).TextColor(color).Padding(1, 5).Radius(5).Background(color.Alpha(0.14)).Shrink(0)
			}
			ui.Spacer(c)
			ui.Icon(c, iconChevronDown).Size(12, 12).TextColor(p.SideMuted)
		})
		ui.Popover(c, switcher, &a.workspacesOpen, func() {
			a.workspaceMenu(c, p)
		})
	})
}

func (a *App) workspaceMenu(c *ui.Context, p *palette) {
	ui.Column(c).Width(240).Gap(1).Children(func() {
		if a.account != nil {
			ui.Column(c).Padding(6, 8, 8, 8).Gap(2).Children(func() {
				ui.Text(c, a.account.User.Name).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine()
				ui.Text(c, a.account.User.Email).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
			})
			ui.Box(c).Height(1).Background(p.Border).Margin(2, 0, 4, 0)
			for _, w := range a.account.Workspaces {
				item := menuRow(c.Key(w.ID), p, w.Name)
				if item.Clicked() {
					a.workspacesOpen = false
					if w.ID != a.workspaceID {
						a.connect(w.ID)
					}
				}
				item.Children(func() {
					avatar(c, p, w.Name, 18, 5)
					ui.Text(c, w.Name).FontSize(sizeSub).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
					if w.ID == a.workspaceID {
						ui.Icon(c, iconCheck).Size(14, 14).TextColor(p.Text)
					}
				})
			}
			ui.Box(c).Height(1).Background(p.Border).Margin(4, 0)
		}
		out := menuRow(c, p, "Sign out").OnClick(func() {
			a.workspacesOpen = false
			a.signOut()
		})
		out.Children(func() {
			ui.Icon(c, iconLogOut).Size(16, 16).TextColor(p.Secondary)
			ui.Text(c, "Sign out").FontSize(sizeSub).TextColor(p.Text)
		})
	})
}

func menuRow(c *ui.Context, p *palette, label string) ui.Element {
	row := ui.ButtonBase(c).Height(30).Padding(0, 8).Radius(6).Gap(8).Justify(ui.Start).Cursor(ui.CursorPointer).Label(label)
	if row.Hovered() {
		row.Background(p.Hover)
	}
	return row
}

// machineName is a machine's display name.
func (a *App) machineName(id string) string {
	if name := a.catalog.MachineNames[id]; name != "" {
		return name
	}
	if len(id) > 8 {
		return "Machine " + strings.ToUpper(id[:6])
	}
	return id
}
