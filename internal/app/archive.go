package app

import (
	"context"
	"fmt"
	"github.com/lbr77/lody-mygo/locales"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
)

// archiveView is the Archive page, as Lody's: a search over archived
// sessions, grouped by project in cards, each row restored or deleted from
// its hover actions.
func (a *App) archiveView(c *ui.Context, p *palette) {
	left := a.barInset(16)
	ui.Row(c).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Padding(0, 12, 0, left).Gap(8).DragWindow().Children(func() {
		if a.sidebarHidden {
			iconButton(c, p, iconSidebar, locales.SidebarToggle(), p.Hover).OnClick(func() { a.sidebarHidden = false })
		}
		ui.Icon(c, iconArchive).Size(16, 16).TextColor(p.Text)
		ui.Text(c, locales.ArchiveTitle()).FontSize(sizeBody).FontWeight(600).TextColor(p.Text)
		label := locales.ArchiveAll()
		if a.archiveFilter == "mine" {
			label = locales.ArchiveMine()
		}
		a.toolbarPicker(c, p, "archive/filter", nil, label, []option{{"", locales.ArchiveAll()}, {"mine", locales.ArchiveMine()}}, a.archiveFilter, func(id string) { a.archiveFilter = id })
	})

	sessions := a.archivedSessions()
	ui.Row(c).Shrink(0).Padding(4, 24, 12, 24).Gap(16).AlignItems(ui.Center).Children(func() {
		search := ui.Row(c).Grow(1).MinWidth(0).Height(32).Padding(0, 10).Gap(8).Radius(8).Border(1, p.Border).Background(p.Composer).AlignItems(ui.Center)
		search.Children(func() {
			ui.Icon(c, iconSearch).Size(14, 14).TextColor(p.Tertiary)
			ui.TextInputBase(c, &a.archiveSearch).Placeholder(locales.ArchiveSearchPlaceholder()).FontSize(sizeSub).Grow(1).Label(locales.ArchiveSearch())
		})
		groupLabel := locales.ArchiveByProject()
		if a.archiveGroup == "flat" {
			groupLabel = locales.ArchiveOneList()
		}
		a.toolbarPicker(c, p, "archive/group", iconLayers, groupLabel, []option{{"project", locales.ArchiveByProject()}, {"flat", locales.ArchiveOneList()}}, firstNonEmpty(a.archiveGroup, "project"), func(id string) { a.archiveGroup = id })
		sortLabels := map[string]string{"newest": locales.ArchiveNewest(), "oldest": locales.ArchiveOldest(), "title": locales.ArchiveTitleAZ()}
		sortKey := firstNonEmpty(a.archiveSort, "newest")
		a.toolbarPicker(c, p, "archive/sort", iconArrowUpDown, sortLabels[sortKey], []option{{"newest", locales.ArchiveNewest()}, {"oldest", locales.ArchiveOldest()}, {"title", locales.ArchiveTitleAZ()}}, sortKey, func(id string) { a.archiveSort = id })
	})

	ui.Scroll(c).Grow(1).MinHeight(0).Padding(4, 24, 32, 24).Gap(28).Children(func() {
		if len(sessions) == 0 {
			ui.Column(c).FillWidth().Padding(80, 0).AlignItems(ui.Center).Gap(6).Children(func() {
				if strings.TrimSpace(a.archiveSearch) != "" || a.archiveFilter != "" {
					ui.Text(c, locales.ArchiveNoMatches()).FontSize(sizeBody).FontWeight(500).TextColor(p.Text)
					ui.Text(c, locales.ArchiveNoMatchesHint()).FontSize(sizeSub).TextColor(p.Secondary)
					return
				}
				ui.Text(c, locales.ArchiveEmpty()).FontSize(sizeBody).FontWeight(500).TextColor(p.Text)
				ui.Text(c, locales.ArchiveEmptyHint()).FontSize(sizeSub).TextColor(p.Secondary)
			})
			return
		}
		if a.archiveGroup == "flat" {
			archiveCard(c, p, func() {
				for _, s := range sessions {
					a.archivedRow(c.Key(s.ID), p, s)
				}
			})
			return
		}
		for _, g := range a.archiveGroups(sessions) {
			ui.Column(c.Key("g/" + g.id)).FillWidth().Gap(10).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 10).Children(func() {
					icon := iconFolder
					if g.id == "" {
						icon = iconMessage
					}
					ui.Icon(c, icon).Size(16, 16).TextColor(p.Secondary)
					ui.Text(c, g.name).FontSize(sizeSub).FontWeight(600).TextColor(p.Text).SingleLine().Shrink(0)
					if g.path != "" {
						ui.Text(c, g.path).FontSize(sizeSub).TextColor(p.Tertiary).SingleLine().MinWidth(0).Shrink(1)
					}
					ui.Spacer(c)
					ui.Text(c, fmt.Sprintf("(%d)", len(g.sessions))).FontSize(sizeFoot).TextColor(p.Tertiary)
				})
				archiveCard(c, p, func() {
					for _, s := range g.sessions {
						a.archivedRow(c.Key(s.ID), p, s)
					}
				})
			})
		}
	})
	a.deleteDialog(c, p)
}

func archiveCard(c *ui.Context, p *palette, rows func()) {
	ui.Column(c).FillWidth().Radius(8).Border(1, p.Border).Background(p.Popover).Clip().Dividers(1, p.Border).Gap(1).Children(rows)
}

// archivedSessions are the archived sessions the search and filter keep,
// in the chosen order.
func (a *App) archivedSessions() []catalog.Session {
	q := strings.ToLower(strings.TrimSpace(a.archiveSearch))
	var out []catalog.Session
	for _, s := range a.catalog.Sessions {
		if !s.Archived || s.ParentSessionID != "" {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(s.Title), q) && !strings.Contains(strings.ToLower(s.BranchName), q) {
			continue
		}
		if a.archiveFilter == "mine" && (a.account == nil || s.UserID != a.account.User.ID) {
			continue
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		switch a.archiveSort {
		case "oldest":
			return activity(out[i]) < activity(out[j])
		case "title":
			return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
		}
		return activity(out[i]) > activity(out[j])
	})
	return out
}

// activity is when a session was last active, in milliseconds.
func activity(s catalog.Session) float64 {
	if s.LastMessageAt > 0 {
		return s.LastMessageAt
	}
	if t, err := time.Parse(time.RFC3339Nano, s.CreatedAt); err == nil {
		return float64(t.UnixMilli())
	}
	return 0
}

type archiveGroup struct {
	id, name, path string
	sessions       []catalog.Session
}

// archiveGroups groups sessions by project, in the order their newest
// session comes; chats outside projects form their own group.
func (a *App) archiveGroups(sessions []catalog.Session) []archiveGroup {
	var groups []archiveGroup
	index := map[string]int{}
	for _, s := range sessions {
		id, name, path := "", locales.SidebarChats(), ""
		if pr := a.catalog.Project(s.ProjectID); pr != nil {
			id, name, path = pr.ID, pr.Name, pr.RootPath
		}
		i, ok := index[id]
		if !ok {
			i = len(groups)
			index[id] = i
			groups = append(groups, archiveGroup{id: id, name: name, path: path})
		}
		groups[i].sessions = append(groups[i].sessions, s)
	}
	return groups
}

func (a *App) archivedRow(c *ui.Context, p *palette, s catalog.Session) {
	row := ui.Row(c).Height(36).Padding(0, 8, 0, 12).Gap(10).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(s.Title).Transition(colorFade)
	hovered := row.Hovered()
	if hovered {
		row.Background(p.Hover)
	}
	if row.Clicked() {
		a.open(s.ID)
	}
	row.ContextMenu(func(m *ui.Menu) { a.sessionMenu(m, s) })
	row.Children(func() {
		ui.Box(c).Size(20, 20).Center().Shrink(0).Children(func() {
			ui.Icon(c, agentGlyph(s.AgentType)).Size(14, 14).TextColor(p.Secondary)
		})
		ui.Text(c, s.Title).FontSize(sizeBody).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
		ui.Text(c, s.BranchName).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine().Width(160).Shrink(0)
		ui.Text(c, relativeTime(time.UnixMilli(int64(activity(s))))).FontSize(sizeFoot).TextColor(p.Secondary).Width(40).TextAlign(ui.End).Shrink(0)
		ui.Row(c).Width(80).Shrink(0).Gap(4).Justify(ui.End).Children(func() {
			if s.DiffAdd > 0 || s.DiffDel > 0 {
				diffCounts(c, p, int(s.DiffAdd), int(s.DiffDel))
			}
		})
		ui.Box(c).Size(20, 20).Shrink(0).Children(func() {
			if s.UserID != "" && a.account != nil && s.UserID == a.account.User.ID {
				a.accountAvatar(c, p, 20)
			} else {
				ui.Row(c).Size(20, 20).Radius(10).Background(p.Text.Alpha(0.1)).Center().Children(func() {
					ui.Icon(c, iconUser).Size(11, 11).TextColor(p.Secondary)
				})
			}
		})
		// Restore and delete show while the row is hovered.
		ui.Row(c).Width(52).Shrink(0).Gap(4).Opacity(map[bool]float32{true: 1, false: 0}[hovered]).Children(func() {
			id := s.ID
			restore := iconButton(c, p, iconUndo, locales.ArchiveRestore(), p.Text.Alpha(0.08)).Size(24, 24).Disabled(!hovered)
			restore.OnClick(func() {
				a.catalogAction("restore the session", func(ctx context.Context, sync *catalog.Sync) error {
					return sync.Archive(ctx, id, false)
				})
			})
			del := iconButton(c, p, iconTrash, locales.ArchiveDelete(), p.Danger.Alpha(0.12)).Size(24, 24).Disabled(!hovered)
			sess := s
			del.OnClick(func() { a.deleting, a.deleteOpen = &sess, true })
		})
	})
}

// deleteDialog confirms deleting a session for good.
func (a *App) deleteDialog(c *ui.Context, p *palette) {
	if a.deleting == nil {
		return
	}
	s := *a.deleting
	ui.DialogBase(c, &a.deleteOpen, func(backdrop, panel ui.Element) {
		sheetIn(backdrop, panel)
		backdrop.Background(ui.RGBA(0, 0, 0, 0.28))
		panel.Width(400).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Padding(20).Gap(12).Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Text(c, locales.ArchiveDeleteTitle()).FontSize(16).FontWeight(600).TextColor(p.Strong)
		description := locales.ArchiveDeleteChat()
		if a.catalog.Project(s.ProjectID) != nil {
			description = locales.ArchiveDeleteCode()
		}
		ui.Text(c, description).FontSize(sizeSub).LineHeight(1.45).TextColor(p.Secondary)
		ui.Row(c).Gap(8).Justify(ui.End).Padding(8, 0, 0, 0).Children(func() {
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { a.deleteOpen = false })
			b := ui.ButtonBase(c).Height(28).Padding(0, 12).Radius(8).Background(p.Danger).Cursor(ui.CursorPointer).Label(locales.ArchiveDelete())
			b.OnClick(func() {
				a.deleteOpen = false
				id := s.ID
				a.catalogAction("delete the session", func(ctx context.Context, sync *catalog.Sync) error {
					return sync.Delete(ctx, id)
				})
			})
			b.Children(func() {
				ui.Text(c, locales.ArchiveDelete()).FontSize(sizeSub).FontWeight(500).TextColor(ui.RGB(255, 255, 255))
			})
		})
	})
}

// toolbarPicker is a ghost button with a chevron that opens a list.
func (a *App) toolbarPicker(c *ui.Context, p *palette, key string, icon *ui.SVG, label string, options []option, selected string, pick func(string)) {
	open := a.popover(key)
	b := ui.ButtonBase(c.Key(key)).Height(28).Padding(0, 8).Radius(8).Gap(6).Cursor(ui.CursorPointer).Label(label).
		OnClick(func() { *open = !*open }).Transition(colorFade)
	pressable(b, 0.96)
	if b.Hovered() || *open {
		b.Background(p.Text.Alpha(0.06))
	}
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).Size(14, 14).TextColor(p.Secondary)
		}
		ui.Text(c, label).FontSize(sizeSub).TextColor(p.Secondary).SingleLine()
		ui.Icon(c, iconChevronDown).Size(12, 12).TextColor(p.Tertiary)
	})
	a.optionList(c, p, b, open, options, selected, pick)
}

// agentGlyph is an agent's mark in lists.
func agentGlyph(agentType string) *ui.SVG {
	switch strings.ToLower(agentType) {
	case "claude", "claude-code":
		return iconAsterisk
	}
	return iconBot
}

// relativeTime is a short age: "now", "5m", "14h", "3d", or the date.
func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case t.IsZero() || t.UnixMilli() <= 0:
		return ""
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return t.Format("Jan 2")
}
