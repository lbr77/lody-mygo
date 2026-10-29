package app

import (
	"context"
	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/locales"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
)

const (
	sidebarWidth    = 300
	projectPreview  = 5 // sessions a project shows before "Show all"
	sidebarListPadX = 6
)

type rowKind int

const (
	rowSection rowKind = iota
	rowProject
	rowSession
	rowMore
	rowNote
	rowGroup // a project of several repositories or folders
)

type sideRow struct {
	kind    rowKind
	key     string
	group   string // the section or project the row belongs to
	label   string
	machine string
	project string
	count   int
	session *catalog.Session
	nested  bool    // a session under a project
	depth   int     // sessions opened by another sit under it
	top     float32 // space above the row
	opened  int     // the sessions it opened, nested under it
	last    bool    // the last nested under its opener, where the trunk ends
}

// machineLabel is a machine's name without the ".local" of mDNS hosts.
func (a *App) machineLabel(id string) string {
	return strings.TrimSuffix(a.machineName(id), ".local")
}

// sideRows lays the sessions out as Lody's Project mode: Pinned, then each
// machine with its projects and their sessions, then Chats outside any
// project.
func (a *App) sideRows() []sideRow {
	var rows []sideRow
	sessions := a.catalog.Sessions
	// A child conversation (parentSessionId) is one of its parent's tabs,
	// never a row of its own, as Lody's sidebar.
	listed := map[string]*catalog.Session{}
	for i := range sessions {
		if s := &sessions[i]; !s.Archived && s.ParentSessionID == "" {
			listed[s.ID] = s
		}
	}
	// A session another opened nests under the topmost listed one that
	// opened it, in that one's group.
	group := map[string]string{}
	for _, s := range listed {
		switch pr := a.catalog.Project(s.ProjectID); {
		case s.Pinned:
			group[s.ID] = "pinned"
		case s.ProjectGroupID != "":
			// A conversation of a project of several sits under it.
			group[s.ID] = "group:" + s.ProjectGroupID
		case pr != nil && !pr.MemberOnly:
			group[s.ID] = "project:" + s.ProjectID
		default:
			// A project the sidebar does not list hosts nothing.
			group[s.ID] = "chats"
		}
	}
	opener := openedBy(listed)
	for id, root := range opener {
		if root != "" {
			group[id] = group[root]
		}
	}
	sectionOpen := false // whether the last section was expanded
	section := func(key, label, machine string) bool {
		top := float32(0)
		if len(rows) > 0 {
			top = 4
			if sectionOpen {
				top = 16
			}
		}
		rows = append(rows, sideRow{kind: rowSection, key: "s:" + key, group: key, label: label, machine: machine, top: top})
		sectionOpen = !a.collapsed[key]
		return sectionOpen
	}
	addSessions := func(group string, list []*catalog.Session, nested bool, limit int) {
		roots, kids := openedTree(list, opener)
		shown := 0
		for _, s := range roots {
			if limit > 0 && shown == limit && !a.showAll[group] {
				rows = append(rows, sideRow{kind: rowMore, key: "m:" + group, group: group, count: len(roots), nested: nested})
				return
			}
			top := float32(0)
			if shown == 0 {
				top = 2
			}
			rows = append(rows, sideRow{kind: rowSession, key: group + "/" + s.ID, group: group, session: s, nested: nested, top: top, opened: len(kids[s.ID])})
			if !a.collapsed["opened:"+s.ID] {
				for i, child := range kids[s.ID] {
					rows = append(rows, sideRow{kind: rowSession, key: group + "/" + child.ID, group: group, session: child, nested: nested, depth: 1, last: i == len(kids[s.ID])-1})
				}
			}
			shown++
		}
	}

	var pinned, chats []*catalog.Session
	byProject := map[string][]*catalog.Session{}
	byGroup := map[string][]*catalog.Session{}
	for i := range sessions {
		s := &sessions[i]
		if listed[s.ID] == nil {
			continue
		}
		switch g := group[s.ID]; {
		case g == "pinned":
			pinned = append(pinned, s)
		case strings.HasPrefix(g, "project:"):
			byProject[strings.TrimPrefix(g, "project:")] = append(byProject[strings.TrimPrefix(g, "project:")], s)
		case strings.HasPrefix(g, "group:"):
			byGroup[strings.TrimPrefix(g, "group:")] = append(byGroup[strings.TrimPrefix(g, "group:")], s)
		default:
			chats = append(chats, s)
		}
	}
	if len(pinned) > 0 && section("pinned", locales.SidebarPinned(), "") {
		addSessions("pinned", pinned, false, 0)
	}
	groups := a.sidebarGroups(byGroup)
	for _, machine := range a.catalog.MachineIDs {
		var projects []catalog.Project
		for _, p := range a.catalog.Projects {
			// A folder registered only for projects of several isn't one.
			if p.MachineID == machine && !p.MemberOnly {
				projects = append(projects, p)
			}
		}
		if len(projects) == 0 && len(groups[machine]) == 0 {
			continue
		}
		if !section("machine:"+machine, a.machineLabel(machine), machine) {
			continue
		}
		for _, p := range projects {
			group := "project:" + p.ID
			list := byProject[p.ID]
			rows = append(rows, sideRow{kind: rowProject, key: "p:" + p.ID, group: group, label: p.Name, machine: machine, project: p.ID, count: len(list)})
			if !a.collapsed[group] {
				addSessions(group, list, true, projectPreview)
			}
		}
		// Its projects of several after its folders, as Lody's.
		for _, g := range groups[machine] {
			group := "group:" + g.id
			list := byGroup[g.id]
			rows = append(rows, sideRow{kind: rowGroup, key: "g:" + g.id, group: group, label: g.name, machine: machine, project: g.id, count: len(list)})
			if !a.collapsed[group] {
				addSessions(group, list, true, projectPreview)
			}
		}
	}
	if len(chats) > 0 && section("chats", locales.SidebarChats(), "") {
		addSessions("chats", chats, false, 0)
	}
	return rows
}

func (a *App) sidebar(c *ui.Context, p *palette) ui.Element {
	return ui.Column(c).Width(a.sidebarW()).Shrink(0).Children(func() {
		// The traffic lights sit on the left of this row.
		ui.Row(c).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Justify(ui.End).Padding(0, 6).Gap(2).DragWindow().Children(func() {
			iconButton(c, p, iconSidebar, locales.SidebarToggle(), p.SideHover).OnClick(func() { a.sidebarHidden = true })
			back := iconButton(c, p, iconChevronLeft, locales.NavBack(), p.SideHover).Disabled(!a.canGoBack())
			if !a.canGoBack() {
				back.Opacity(0.6)
			}
			back.OnClick(a.goBack)
			forward := iconButton(c, p, iconChevronRight, locales.NavForward(), p.SideHover).Disabled(!a.canGoForward())
			if !a.canGoForward() {
				forward.Opacity(0.6)
			}
			forward.OnClick(a.goForward)
		})
		ui.Column(c).Padding(0, 6).Gap(1).Shrink(0).Children(func() {
			if navRow(c, p, iconNewChat, locales.NewChat(), a.selected == "" && a.page == "").Clicked() {
				a.open("")
			}
			// Schedules sits under New chat, as Lody's.
			navRow(c, p, iconListTodo, locales.SchedTitle(), a.page == "schedules").OnClick(func() { a.page = "schedules" })
			// Search opens the ⌘K palette, as Lody's.
			navRow(c, p, iconSearch, locales.Search(), a.palette.open).OnClick(func() { a.setPaletteOpen(true) })
			navRowCount(c, p, iconInbox, locales.Inbox(), a.page == "inbox", a.inboxUnread()).OnClick(func() {
				a.page = "inbox"
				a.refetchKey("inbox")
			})
		})
		rows := a.sideRows()
		a.sessionList.Key = func(i int) any {
			if i < len(rows) {
				return rows[i].key
			}
			return i
		}
		ui.List(c, &a.sessionList, len(rows), func(i int) {
			r := rows[i]
			switch r.kind {
			case rowSection:
				a.sectionHeader(c, p, r)
			case rowProject:
				a.projectRow(c, p, r)
			case rowGroup:
				a.groupRow(c, p, r)
			case rowSession:
				a.sessionRow(c, p, r)
			case rowMore:
				a.moreRow(c, p, r)
			case rowNote:
				ui.Text(c.Key(r.key), r.label).FontSize(sizeSub).TextColor(p.SideMuted).Padding(4, 9)
			}
		}).Grow(1).MinHeight(0).Padding(12, sidebarListPadX, 12, sidebarListPadX).Gap(1).Children(func() {
			if len(rows) == 0 {
				msg := locales.SidebarNoChats()
				if !a.synced {
					msg = locales.SidebarSyncing()
				}
				ui.Text(c, msg).FontSize(sizeSub).TextColor(p.SideMuted).Margin(24, 10)
			}
		})
		a.sessionCardOverlay(c, p)
		if a.notice != "" {
			ui.Row(c).Shrink(0).Margin(0, 10).Padding(6, 8).Gap(6).Radius(8).Background(p.Danger.Alpha(0.1)).AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Label(locales.Dismiss()).OnClick(func() { a.notice = "" }).Children(func() {
				ui.Icon(c, iconCircleAlert).Size(12, 12).TextColor(p.Danger)
				ui.Text(c, a.notice).FontSize(sizeFoot).TextColor(p.Danger).MaxLines(3).Grow(1).MinWidth(0)
			})
		}
		a.footer(c, p)
	})
}

// navRow is a 28px sidebar navigation row.
func navRow(c *ui.Context, p *palette, icon *ui.SVG, label string, selected bool) ui.Element {
	return navRowCount(c, p, icon, label, selected, 0)
}

// navRowCount is a nav row with a count at its end, as the Inbox's.
func navRowCount(c *ui.Context, p *palette, icon *ui.SVG, label string, selected bool, count int) ui.Element {
	// Tailwind's transition: the fill and the icon's ink, which rests a
	// step quieter than the label and comes up with the hover, over 150ms.
	row := ui.ButtonBase(c).Height(28).Padding(4, 9).Radius(8).Gap(6).Justify(ui.Start).Cursor(ui.CursorPointer).Label(label).Transition(twFade)
	ink, iconInk := p.SideRow, p.SideMuted
	switch {
	case selected:
		row.Background(p.SideSelect)
		ink, iconInk = p.SideRowSel, p.SideRowSel
	case row.Hovered():
		row.Background(p.SideHover)
		iconInk = p.SideRow
	}
	iconInk = row.AnimateColor("icon-ink", iconInk, ui.Animation{Duration: 150 * time.Millisecond, Ease: twEase})
	row.Children(func() {
		ui.Box(c).Size(16, 20).Center().Children(func() { ui.Icon(c, icon).Size(16, 16).TextColor(iconInk) })
		ui.Text(c, label).FontSize(sizeSub).TextColor(ink).Grow(1)
		if count > 0 {
			ui.Text(c, itoa(count)).FontSize(sizeCaption).FontWeight(600).TextColor(p.Primary).Padding(0, 6).Radius(8).Background(p.Primary.Alpha(0.12))
		}
	})
	return row
}

// sectionHeader is a machine's, Pinned's or Chats' header: muted label,
// chevron on hover or while folded.
func (a *App) sectionHeader(c *ui.Context, p *palette, r sideRow) {
	row := ui.Row(c.Key(r.key)).Height(26).Padding(0, 8).Border(1, ui.Transparent).Radius(6).Gap(6).AlignItems(ui.Center).
		Margin(r.top, 0, 0, 0).Cursor(ui.CursorPointer).Label(r.label).
		OnClick(func() {
			a.collapsed[r.group] = !a.collapsed[r.group]
			a.savePrefs()
		})
	hovered := row.Hovered()
	collapsed := a.collapsed[r.group]
	// The chevron fades in with the pointer, and stays while folded, and
	// turns to point right as it folds: 150ms on Tailwind's ease-out, as
	// Lody's; the label comes up to the sidebar's ink under the pointer.
	reveal := row.AnimateWith("reveal", b2f(hovered || collapsed), 150*time.Millisecond, twEaseOut)
	turn := row.AnimateWith("turn", b2f(collapsed), 150*time.Millisecond, twEaseOut)
	label := p.SideMuted.Alpha(0.8)
	if hovered {
		label = p.SideRow
	}
	label = row.AnimateColor("label", label, ui.Animation{Duration: 150 * time.Millisecond, Ease: twEase})
	row.Children(func() {
		ui.Text(c, r.label).FontSize(sizeFoot).FontWeight(500).TextColor(label).SingleLine().MinWidth(0)
		if r.machine != "" && !a.presence.IsOnline(r.machine) {
			offlinePill(c, p)
		}
		if reveal > 0 {
			ui.Icon(c, iconChevronDown).Size(14, 14).TextColor(p.SideMuted).Rotate(-90 * turn).Opacity(reveal)
		}
	})
}

// projectRow is a project under its machine: folder icon, name, and New
// chat on hover.
func (a *App) projectRow(c *ui.Context, p *palette, r sideRow) {
	collapsed := a.collapsed[r.group]
	selected := a.newChat.project == r.project && a.selected == ""
	row := ui.Row(c.Key(r.key)).Height(30).Padding(4, 8).Border(1, ui.Transparent).Radius(6).Gap(8).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label(r.label).Transition(twFade).
		OnClick(func() {
			a.collapsed[r.group] = !a.collapsed[r.group]
			a.savePrefs()
		})
	// Its menu, on a right click or its ⋯, as Lody's project row: none
	// while its machine removes it.
	menu := a.rowMenu("side-project/" + r.project)
	removing := a.catalog.Removing[r.project]
	if !removing {
		lodyContextMenu(c, p, row, menu, 28, func(m *lodyMenu) { a.sideProjectMenu(m, r) })
	}
	hovered := row.Hovered() || menu.open
	switch {
	case selected:
		row.Background(p.SideSelect)
	case hovered:
		row.Background(p.SideHover)
	}
	// The row's hover controls fade in with the pointer over 100ms, and
	// take no pointer while they are away.
	actions := row.AnimateWith("actions", b2f(hovered), 100*time.Millisecond, twEase)
	rowBox := row.Bounds()
	// With the pointer the folder gives way to the chevron, which turns
	// between open and folded: both over 100ms, as Lody's.
	swap := row.AnimateWith("swap", b2f(hovered), 100*time.Millisecond, ui.CubicBezier(0.4, 0, 0.2, 1))
	turn := row.AnimateWith("chevron", b2f(collapsed), 100*time.Millisecond, ui.CubicBezier(0.4, 0, 0.2, 1))
	row.Children(func() {
		icon := iconFolderOpen
		if collapsed {
			icon = iconFolder
		}
		ui.Box(c).Size(14, 16).Center().Shrink(0).Children(func() {
			if swap < 1 {
				ink := p.SideMuted
				if selected {
					ink = p.SideRowSel
				}
				ui.Icon(c, icon).Size(16, 16).TextColor(ink).Opacity(1 - swap).Absolute()
			}
			if swap > 0 {
				ui.Icon(c, iconChevronDown).Size(16, 16).TextColor(p.SideRow).Rotate(-90 * turn).Opacity(swap).Absolute()
			}
		})
		ui.Text(c, r.label).FontSize(sizeSub).TextColor(p.SideRow).SingleLine().Grow(1).MinWidth(0)
		if actions > 0 && !removing {
			sideMoreButton(c, p, menu, rowBox, actions, hovered)
		}
		if actions > 0 {
			b := ui.ButtonBase(c.Key("new-chat")).Size(20, 20).Radius(4).Label(locales.NewChatIn(r.label)).Opacity(actions).
				Transition(ui.ElementTransition{Colors: true, Duration: 100 * time.Millisecond, Ease: twEase})
			ink := p.Secondary.Alpha(0.7)
			if hovered {
				tip(c, p, b, locales.NewChat()).Cursor(ui.CursorPointer).OnClick(func() {
					a.newChat.machine, a.newChat.project = r.machine, r.project
					a.pickNewChatDefaults()
					a.open("")
				})
				if b.Hovered() {
					b.Background(p.Text.Alpha(0.06))
					ink = p.Text
				}
			} else {
				b.PassThrough()
			}
			ink = b.AnimateColor("ink", ink, ui.Animation{Duration: 100 * time.Millisecond, Ease: twEase})
			b.Children(func() { ui.Icon(c, iconSquarePen).Size(14, 14).TextColor(ink) })
		}
	})
}

func (a *App) moreRow(c *ui.Context, p *palette, r sideRow) {
	row := ui.Row(c.Key(r.key)).Height(28).Padding(4, 8, 4, 37).Radius(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Label(locales.SidebarShowAll(r.count)).OnClick(func() { a.showAll[r.group] = true }).Transition(twFade)
	ink := p.SideMuted.Alpha(0.8)
	if row.Hovered() {
		row.Background(p.SideHover)
		ink = p.SideRow
	}
	ink = row.AnimateColor("ink", ink, ui.Animation{Duration: 150 * time.Millisecond, Ease: twEase})
	row.Children(func() {
		ui.Text(c, locales.SidebarShowAll(r.count)).FontSize(sizeSub).TextColor(ink)
	})
}

func (a *App) sessionRow(c *ui.Context, p *palette, r sideRow) {
	s := r.session
	selected := a.selected == s.ID
	row := ui.Row(c.Key(r.key)).Border(1, ui.Transparent).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(s.Title).Margin(r.top, 0, 0, 0)
	if r.nested {
		row.Height(30).Padding(4, 8).Radius(6)
	} else {
		row.Height(26).Padding(3, 8).Radius(8)
	}
	hovered := row.Hovered()
	switch {
	case selected:
		row.Background(p.SideSelect)
	case hovered:
		row.Background(p.SideHover)
	}
	clicked := row.Clicked()
	if a.titleSwipe.noClick == s.ID {
		// The release that ended a swipe across the title isn't a click.
		clicked, a.titleSwipe.noClick = false, ""
	}
	if clicked && a.renaming != s.ID {
		a.open(s.ID)
	}
	sess := *s
	// The row takes its input once: the context menu, and a sideways
	// scroll or drag across its title.
	var title titleBox
	rowBox := row.Bounds()
	lodyContextMenuWith(c, p, row, a.rowMenu("row/"+s.ID), 28, func(ev ui.InputEvent) bool {
		return a.scrubTitle(ev, s.ID, rowBox, &title)
	}, func(m *lodyMenu) { a.sessionRowMenu(m, sess) })
	a.sessionHover(c, p, row, s, hovered, row.Pressed() || clicked)
	ink := p.SideRow
	if selected {
		ink = p.SideRowSel
	}
	rowH := float32(26)
	if r.nested {
		rowH = 30
	}
	row.Children(func() {
		if r.nested || r.depth > 0 || r.opened > 0 {
			a.rowLeadingSlot(c, p, r, sess, hovered, rowH)
		}
		if s.Pinned && r.group != "pinned" {
			ui.Icon(c, iconPin).Size(12, 12).TextColor(p.SideMuted)
		}
		if a.renaming == s.ID {
			a.renameField(c, p, s)
			return
		}
		title = a.rowTitle(c, s.ID, s.Title, ink, hovered)
		a.rowEnd(c, p, s, !r.nested && r.depth == 0, hovered)
	})
}

// titleSwipe is a sidebar title moved by a sideways scroll or a drag
// across it: whose, and by how much.
type titleSwipe struct {
	id     string
	offset float32
	drag   titleDrag
	// noClick is the row whose release ended a drag, which isn't a click.
	noClick string
}

// titleDrag is a press on a long title: where it went down and the
// title's offset then. It moves the title once it has gone 5px, more
// across than down; going down first, it lets go.
type titleDrag struct {
	id               string
	x, y, from, over float32
	started          bool
}

// titleBox is where a row's title lies, as the last frame laid it out,
// how far the title runs past it, and where it is in its run.
type titleBox struct {
	clip           ui.Rect
	over, position float32
}

// scrubTitle moves a long title under a sideways scroll or a drag across
// it, as Lody's: from where the marquee had it, which stops, the title
// staying where it was put.
func (a *App) scrubTitle(ev ui.InputEvent, id string, rb ui.Rect, t *titleBox) bool {
	sw := &a.titleSwipe
	x, y := rb.X+ev.X, rb.Y+ev.Y
	cb := t.clip
	onTitle := x >= cb.X && x <= cb.X+cb.W && y >= cb.Y && y <= cb.Y+cb.H
	from := t.position
	if sw.id == id {
		from = sw.offset
	}
	d := &sw.drag
	switch ev.Kind {
	case ui.InputScroll:
		if t.over <= 8 || !onTitle {
			return false
		}
		delta := ev.DX
		if delta == 0 && ev.Mods&ui.Shift != 0 {
			delta = ev.DY
		}
		if abs32(delta) < 1 {
			return false
		}
		sw.id, sw.offset = id, max(-t.over, min(0, from-delta))
		return true
	case ui.InputPointerDown:
		if ev.Button != 0 || t.over <= 8 || !onTitle {
			return false
		}
		*d = titleDrag{id: id, x: x, y: y, from: max(-t.over, min(0, from)), over: t.over}
		return true
	case ui.InputPointerMove:
		if d.id != id {
			return false
		}
		dx, dy := x-d.x, y-d.y
		if !d.started {
			if max(abs32(dx), abs32(dy)) < 5 {
				return true
			}
			if abs32(dy) >= abs32(dx) {
				*d = titleDrag{}
				return false
			}
			d.started = true
		}
		sw.id, sw.offset = id, max(-d.over, min(0, d.from+dx))
		return true
	case ui.InputPointerUp:
		if d.id != id {
			return false
		}
		if d.started {
			sw.noClick = id
		}
		*d = titleDrag{}
		return true
	}
	return false
}

// rowTitle is a row's title, ellipsized; while the row is hovered, a
// title too long to show runs back and forth, as Lody's marquee does:
// after 600ms, over max(3.8s, 2.4s + 1s every 30px it overflows by).
func (a *App) rowTitle(c *ui.Context, id, title string, ink ui.Color, hovered bool) titleBox {
	sw := &a.titleSwipe
	// A drag across the title holds it, even off the row.
	held := sw.drag.id == id && sw.drag.started
	if !hovered && !held && sw.id == id {
		// The pointer leaving puts the title back.
		sw.id = ""
	}
	clip := ui.Column(c.Key("title")).Grow(1).MinWidth(0).ClipX()
	var over, position float32
	clip.Children(func() {
		var natural float32
		// The title's own width, measured where it takes no room.
		ui.Row(c.Key("measure")).FillWidth().Height(0).Opacity(0).Children(func() {
			natural = ui.Text(c.Key("m"), title).FontSize(sizeSub).SingleLine().Shrink(0).Bounds().W
		})
		over = natural - clip.Bounds().W
		if !hovered && !held || over <= 8 {
			ui.Text(c.Key("t"), title).FontSize(sizeSub).TextColor(ink).SingleLine().FillWidth()
			return
		}
		run := ui.Text(c.Key("run"), title).FontSize(sizeSub).TextColor(ink).SingleLine().Shrink(0)
		if sw.id == id {
			position = sw.offset
			run.Translate(position, 0)
			return
		}
		dur := time.Duration(max(3.8, 2.4+float64(over)/30) * float64(time.Second))
		k := run.Keyframes("marquee", []ui.Keyframe{{Value: 0}, {At: dur, Value: 1, Ease: cssEaseInOut}},
			ui.KeyframeOptions{Repeat: ui.RepeatForever, Alternate: true, Delay: 600 * time.Millisecond})
		position = -over * k
		run.Translate(position, 0)
	})
	return titleBox{clip: clip.Bounds(), over: over, position: position}
}

// archiveArmed is how long an archive button waits for its confirmation.
const archiveArmed = 3500 * time.Millisecond

// rowEnd is a row's end: its status mark at rest; while hovered, the mark
// fades out over 100ms as the archive button fades in over it, which asks
// to be clicked again to archive, as Lody's sidebar does.
func (a *App) rowEnd(c *ui.Context, p *palette, s *catalog.Session, age, hovered bool) {
	armed := a.archiveArm == s.ID && c.Now().Sub(a.archiveArmAt) < archiveArmed
	if a.archiveArm == s.ID && !armed {
		a.archiveArm = ""
	}
	if armed {
		c.After(archiveArmed - c.Now().Sub(a.archiveArmAt))
	}
	show := (hovered || armed) && !s.Archived
	slot := ui.Row(c.Key("end")).Height(20).MinWidth(20).Shrink(0).Justify(ui.End).AlignItems(ui.Center)
	rest := slot.AnimateWith("rest", b2f(!show), 100*time.Millisecond, twEase)
	slot.Children(func() {
		ui.Row(c.Key("rest")).AlignItems(ui.Center).Opacity(rest).Children(func() { a.statusMark(c, p, s, age) })
		if !show {
			return
		}
		fade := ui.ElementTransition{Duration: 100 * time.Millisecond, Ease: twEase, Enter: &ui.Motion{}, Exit: &ui.Motion{}}
		id := s.ID
		if armed {
			// The confirmation snaps in, a pill in the destructive ink.
			pill := ui.ButtonBase(c.Key("confirm")).Absolute().Right(0).Top(0).Height(20).MinWidth(56).Padding(0, 8).Radius(10).
				Border(1, p.Danger.Alpha(0.2)).Background(p.Ground).Justify(ui.Center).AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Label(locales.ArchiveConfirmLabel())
			if pill.PressedOutside() || !hovered {
				a.archiveArm = ""
			}
			pill.OnClick(func() {
				a.archiveArm = ""
				if a.selected == id {
					a.open("")
				}
				a.catalogAction(locales.FailArchive(), func(ctx context.Context, sync *catalog.Sync) error {
					return sync.Archive(ctx, id, true)
				})
			})
			pill.Children(func() {
				ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Radius(10).Background(p.Danger.Alpha(0.12))
				ui.Text(c, locales.ArchiveConfirm()).FontSize(11).FontWeight(500).TextColor(p.Danger).SingleLine()
			})
			return
		}
		b := ui.ButtonBase(c.Key("archive")).Absolute().Right(0).Top(0).Size(20, 20).Radius(4).Justify(ui.Center).AlignItems(ui.Center).
			Cursor(ui.CursorPointer).Label(locales.ActionArchive()).Transition(fade)
		tip(c, p, b, locales.ActionArchive())
		ink := p.SideMuted
		if b.Hovered() {
			ink = p.SideRow
		}
		now := c.Now()
		b.OnClick(func() { a.archiveArm, a.archiveArmAt = id, now })
		b.Children(func() { ui.Icon(c, iconArchive).Size(14, 14).TextColor(ink) })
	})
}

// statusMark is the one mark at a row's end: waiting, working or unread;
// an idle row outside a project shows its age, one inside nothing.
func (a *App) statusMark(c *ui.Context, p *palette, s *catalog.Session, age bool) {
	switch {
	case s.Waiting():
		tip(c, p, ui.Icon(c, iconHand).Size(12, 12).TextColor(p.Primary), locales.StatusWaiting())
	case a.sending && a.sendingID == s.ID && !s.Working():
		progress, known := a.sendProgress()
		sendRing(c, p, progress, known)
	case a.workingMark(c, p, s.ID, s.Working(), s.Unread() && a.selected != s.ID):
	case prMark(c, p, s, true):
	case age:
		if t := relativeTime(time.UnixMilli(int64(activity(*s)))); t != "" {
			ui.Text(c, t).FontSize(sizeFoot).TextColor(p.SideMuted).Shrink(0)
		}
	}
}

func (a *App) projectName(id string) string {
	if pr := a.catalog.Project(id); pr != nil {
		return pr.Name
	}
	return ""
}

// footer holds the workspace switcher, Help, Archive and Settings.
func (a *App) footer(c *ui.Context, p *palette) {
	ws := a.workspace()
	name := locales.WorkspaceFallback()
	if ws != nil {
		name = ws.Name
	}
	ui.Row(c).Shrink(0).Padding(6, 10).Gap(4).AlignItems(ui.Center).Children(func() {
		switcher := ui.ButtonBase(c).Height(28).Padding(0, 6).Radius(8).Gap(6).Grow(1).MinWidth(0).Justify(ui.Start).Cursor(ui.CursorPointer).Label(locales.WorkspaceSwitch()).Transition(colorFade)
		if switcher.Hovered() || a.workspacesOpen {
			switcher.Background(p.Text.Alpha(0.06))
		}
		switcher.OnClick(func() { a.workspacesOpen = !a.workspacesOpen })
		switcher.Children(func() {
			a.workspaceIcon(c, ws, name, 16)
			ui.Text(c, name).FontSize(sizeSub).TextColor(p.SideRow).SingleLine().MinWidth(0)
			switch {
			case a.localOnly:
				// Local-only mode says so, and whether the daemon is up.
				label, color := locales.LocalChip(), p.SideMuted
				if a.local == nil {
					label, color = locales.Offline(), p.Danger
				}
				ui.Text(c, label).FontSize(10).FontWeight(500).TextColor(color).Padding(1, 5).Radius(5).Background(color.Alpha(0.14)).Shrink(0).
					Tooltip(firstNonEmpty(a.syncState.Reason, locales.PrefLocalOnly()))
			case a.sync != nil && (!a.syncState.Live || !a.synced):
				// Lody's connection badge: connecting, reconnecting after a
				// drop, or syncing once connected, each with the working
				// grid turning in it.
				label := locales.ConnLoading()
				switch {
				case a.syncState.Live:
					label = locales.ConnSyncing()
				case a.syncState.Reason != "":
					label = locales.ConnReconnecting()
				}
				ink := p.Primary.Mix(p.Text, 0.5)
				ui.Row(c.Key("conn")).Height(20).Padding(0, 6).Gap(4).Radius(5).AlignItems(ui.Center).Background(p.Primary.Alpha(0.14)).Shrink(0).
					Label(label).Children(func() {
					workingGrid(c, p, 12)
					ui.Text(c, label).FontSize(sizeCaption).FontWeight(500).TextColor(ink).SingleLine()
				})
			}
		})
		popup(c, switcher, &a.workspacesOpen, func() { a.workspaceMenu(c, p) })
		iconButton(c, p, iconHelp, locales.Help(), p.SideHover).Menu(func(m *ui.Menu) {
			for _, link := range []struct{ label, url string }{
				{locales.HelpDocs(), "https://lody.ai/docs"},
				{"GitHub", "https://github.com/LodyAI/Lody"},
				{locales.HelpCommunity(), "https://discord.gg/E8mZtMu38s"},
			} {
				if m.Item(link.label).Chosen() {
					mygo.Shell.OpenExternal(link.url)
				}
			}
			m.Separator()
			if m.Item(locales.PairingAddMachine()).Chosen() {
				a.addMachine()
			}
			if m.Item(locales.DeviceMenu()).Chosen() {
				a.device = deviceAuth{open: true}
			}
			m.Separator()
			if m.Item(locales.ReportBug()).Chosen() {
				a.openBugReport()
			}
		})
		archive := iconButton(c, p, iconArchive, locales.ArchivedChats(), p.SideHover).OnClick(func() {
			if a.page == "archive" {
				a.page = ""
			} else {
				a.page = "archive"
			}
		})
		if a.page == "archive" {
			archive.Background(p.SideSelect)
		}
		iconButton(c, p, iconSettings, locales.Settings(), p.SideHover).OnClick(func() { a.settingsOpen, a.fetches = true, nil })
	})
}

// workspaceTile is a workspace's letter on its hashed hue, as Lody's
// WorkspaceAvatar draws one without a logo.
func workspaceTile(c *ui.Context, name string, size float32) ui.Element {
	initial := "W"
	if t := strings.TrimSpace(name); t != "" {
		initial = strings.ToUpper(string([]rune(t)[:1]))
	}
	key := name
	if key == "" {
		key = "workspace"
	}
	return ui.Row(c).Size(size, size).Shrink(0).Radius(size / 4).Background(hsl(stringToHue(key), 0.62, 0.52)).Center().Children(func() {
		ui.Text(c, initial).FontSize(size * 0.56).FontWeight(600).TextColor(ui.RGB(255, 255, 255))
	})
}

// stringToHue is Lody's hash of a name to a hue, over UTF-16 code units.
func stringToHue(s string) float64 {
	var hash int32
	for _, u := range utf16.Encode([]rune(s)) {
		hash = (hash << 5) - hash + int32(u)
	}
	h := int64(hash)
	if h < 0 {
		h = -h
	}
	return float64(h % 360)
}

func hsl(h, s, l float64) ui.Color {
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g = c, x
	case h < 120:
		r, g = x, c
	case h < 180:
		g, b = c, x
	case h < 240:
		g, b = x, c
	case h < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	to := func(v float64) uint8 { return uint8(math.Round((v + m) * 255)) }
	return ui.RGB(to(r), to(g), to(b))
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
					ws := w
					a.workspaceIcon(c, &ws, w.Name, 18)
					ui.Text(c, w.Name).FontSize(sizeSub).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
					if w.ID == a.workspaceID {
						ui.Icon(c, iconCheck).Size(14, 14).TextColor(p.Text)
					}
				})
			}
			ui.Box(c).Height(1).Background(p.Border).Margin(4, 0)
			for _, act := range []struct {
				icon  *ui.SVG
				label string
				do    func()
			}{{iconPlus, locales.WorkspaceCreateTitle(), a.openCreateWorkspace}, {iconLink, locales.JoinWorkspaceTitle(), a.openJoinWorkspace}} {
				act := act
				row := menuRow(c.Key(act.label), p, act.label).OnClick(act.do)
				row.Children(func() {
					ui.Icon(c, act.icon).Size(16, 16).TextColor(p.Secondary)
					ui.Text(c, act.label).FontSize(sizeSub).TextColor(p.Text)
				})
			}
			ui.Box(c).Height(1).Background(p.Border).Margin(4, 0)
		}
		out := menuRow(c, p, locales.SignOut()).OnClick(func() {
			a.workspacesOpen = false
			a.signOut()
		})
		out.Children(func() {
			ui.Icon(c, iconLogOut).Size(16, 16).TextColor(p.Secondary)
			ui.Text(c, locales.SignOut()).FontSize(sizeSub).TextColor(p.Text)
		})
	})
}

func menuRow(c *ui.Context, p *palette, label string) ui.Element {
	// A popup row's highlight is instant: it follows the pointer and the
	// arrow keys, which a fade would leave behind.
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
		return locales.MachineFallback(strings.ToUpper(id[:6]))
	}
	return id
}

// The sidebar's width, which its right edge drags between these.
const (
	sidebarMin = 220
	sidebarMax = 480
)

// barInset is the left padding of a title bar over the card, base beside
// the sidebar, clearing the traffic lights as the sidebar slides away.
func (a *App) barInset(base float32) float32 {
	return base + (76-base)*(1-a.sideOut)
}

func (a *App) sidebarW() float32 {
	if a.sidebarWidth == 0 {
		return sidebarWidth
	}
	return min(max(a.sidebarWidth, sidebarMin), sidebarMax)
}

// sidebarHandle is the sidebar's right edge, which drags to resize it: a
// strip over the edge that shows a line while it's hovered or dragged.
func (a *App) sidebarHandle(c *ui.Context, p *palette) {
	w := a.sidebarW()
	handle := ui.Box(c).Absolute().Top(0).Bottom(0).Left(w - 3).Width(6).Cursor(ui.CursorResizeColumn).Label(locales.SidebarResize())
	if dx, _, ok := handle.Dragged(); ok && dx != 0 {
		// From where the last frame put the edge, as a frame may build
		// more than once and must move it once.
		base := w
		if b := handle.Bounds(); b.W > 0 {
			base = b.X + 3
		}
		a.sidebarWidth = min(max(base+dx, sidebarMin), sidebarMax)
	}
	if _, _, ended := handle.DragEnded(); ended {
		a.savePrefs()
	}
	if handle.Hovered() || handle.Dragging() {
		handle.Children(func() {
			ui.Box(c).Width(2).Fill().Margin(0, 2).Background(p.Primary.Alpha(0.6))
		})
	}
}

// workspaceIcon is a workspace's logo, or its lettered tile without one.
func (a *App) workspaceIcon(c *ui.Context, ws *cloud.Workspace, name string, size float32) {
	if ws != nil && ws.Logo != "" {
		if img := a.urlImage(ws.Logo); img.bitmap != nil {
			ui.Box(c).Size(size, size).Shrink(0).Radius(size / 4).Clip().Children(func() {
				ui.Image(c, img.bitmap).Size(size, size).Fit(ui.Cover)
			})
			return
		}
	}
	workspaceTile(c, name, size)
}

// openedBy maps each listed session to the topmost listed session that
// opened it, "" for a top-level row, as Lody's opened-by tree: a chain
// climbs to its topmost listed ancestor, and only a top-level row hosts
// others, so a cycle stays flat.
func openedBy(listed map[string]*catalog.Session) map[string]string {
	candidate := map[string]string{}
	for id, s := range listed {
		top := ""
		seen := map[string]bool{id: true}
		for cursor := strings.TrimSpace(s.OpenedBySessionID); cursor != "" && !seen[cursor]; {
			o := listed[cursor]
			if o == nil {
				break
			}
			seen[cursor], top = true, cursor
			cursor = strings.TrimSpace(o.OpenedBySessionID)
		}
		candidate[id] = top
	}
	out := map[string]string{}
	for id, c := range candidate {
		if c != "" && candidate[c] == "" {
			out[id] = c
		} else {
			out[id] = ""
		}
	}
	return out
}

// openedTree splits a group's sessions into its top-level rows, ranked by
// the freshest of each and the sessions it opened (pinned first), and those
// nested under each, in the order they came.
func openedTree(list []*catalog.Session, opener map[string]string) (roots []*catalog.Session, kids map[string][]*catalog.Session) {
	kids = map[string][]*catalog.Session{}
	in := map[string]bool{}
	for _, s := range list {
		in[s.ID] = true
	}
	for _, s := range list {
		if o := opener[s.ID]; o != "" && in[o] {
			kids[o] = append(kids[o], s)
			continue
		}
		roots = append(roots, s)
	}
	if len(kids) == 0 {
		return roots, kids
	}
	rank := func(s *catalog.Session) float64 {
		r := activity(*s)
		for _, k := range kids[s.ID] {
			r = max(r, activity(*k))
		}
		if s.Pinned {
			r += 1e15
		}
		return r
	}
	sort.SliceStable(roots, func(i, j int) bool { return rank(roots[i]) > rank(roots[j]) })
	return roots, kids
}

// rowLeadingSlot is a row's leading slot, as Lody's: an opener's chevron,
// which folds the sessions it opened, or a nested row's connector, at rest;
// "⋯" in their place while the row is hovered.
func (a *App) rowLeadingSlot(c *ui.Context, p *palette, r sideRow, sess catalog.Session, hovered bool, rowH float32) {
	width := float32(14)
	if r.depth > 0 {
		width = 26
	}
	slot := ui.Box(c.Key("slot")).Size(width, 14).Shrink(0)
	// The rest gives way to ⋯ with the pointer, each fading over 100ms;
	// ⋯ stays while its menu is open.
	hovered = hovered || a.rowMenu("more/"+sess.ID).open
	more := slot.AnimateWith("more", b2f(hovered), 100*time.Millisecond, twEase)
	slot.Children(func() {
		line := p.SideRow.Alpha(0.2)
		rest := ui.Box(c.Key("rest")).Absolute().Left(0).Top(0).Size(width, 14).Opacity(1 - more)
		if hovered {
			rest.PassThrough()
		}
		rest.Children(func() {
			switch {
			case more >= 1:
			case r.depth > 0:
				// The trunk climbs to the row above and runs on into the next
				// unless this is the last; the elbow reaches the title.
				reach := (rowH - 14) / 2
				trunk := ui.Box(c.Key("trunk")).Absolute().Left(7).Top(-reach).Width(1).Background(line)
				if r.last {
					trunk.Height(reach + 7)
				} else {
					trunk.Height(rowH + 1)
				}
				ui.Box(c.Key("elbow")).Absolute().Left(7).Top(7).Width(13).Height(1).Background(line)
			case r.opened > 0:
				collapsed := a.collapsed["opened:"+sess.ID]
				label := locales.OpenedHide()
				if collapsed {
					label = locales.OpenedShow(r.opened)
				}
				b := ui.ButtonBase(c.Key("fold")).Size(14, 14).Radius(4).Center().Cursor(ui.CursorPointer).Label(label).
					OnClick(func() { a.collapsed["opened:"+sess.ID] = !collapsed })
				tip(c, p, b, label)
				b.Children(func() {
					turn := b.AnimateWith("turn", b2f(!collapsed), 150*time.Millisecond, twEaseOut)
					ui.Icon(c, iconChevronDown).Size(14, 14).TextColor(p.SideMuted).Rotate(-90 * (1 - turn))
				})
			}
		})
		if more <= 0 {
			return
		}
		btn := ui.ButtonBase(c.Key("more")).Size(14, 14).Radius(4).Label(locales.MoreActions()).Opacity(more).
			Transition(ui.ElementTransition{Colors: true, Duration: 100 * time.Millisecond, Ease: twEase})
		if r.depth > 0 {
			btn.Absolute().Left(0)
		}
		if hovered {
			lodyMenuButton(c, p, btn, &a.rowMenu("more/"+sess.ID).menuState, 180, 290, 28, func(m *lodyMenu) { a.sessionRowMenu(m, sess) })
		} else {
			btn.PassThrough()
		}
		btn.Children(func() { ui.Icon(c, iconMore).Size(14, 14).TextColor(p.SideMuted) })
	})
}
