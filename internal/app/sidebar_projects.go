package app

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// The sidebar's project rows, as Lody's (loro-app-sidebar.tsx and
// session-list.tsx): each folder's menu, on a right click or its ⋯ —
// Edit project… starts a project of several from it, then its settings,
// its folder, archiving its chats and removing it; a GitHub repository's
// has Edit project… and Archive chats. Under each machine, after its
// folders, the projects of several bound to it, each with its
// conversations; one that runs anywhere sits where its latest one ran,
// while it has any.

var iconSettings2 = lucide(`<path d="M14 17H5"/><path d="M19 7h-9"/><circle cx="17" cy="17" r="3"/><circle cx="7" cy="7" r="3"/>`)

// sideMoreButton is a row's ⋯ among its hover controls: it opens the
// row's own right-click menu from a left click, under its left edge.
func sideMoreButton(c *ui.Context, p *palette, menu *contextMenu, rowBox ui.Rect, opacity float32, live bool) {
	b := ui.ButtonBase(c.Key("more")).Size(20, 20).Radius(4).Label(locales.MoreActions()).Opacity(opacity).
		Transition(ui.ElementTransition{Colors: true, Duration: 100 * time.Millisecond, Ease: twEase})
	ink := p.Secondary.Alpha(0.7)
	if live {
		bb := b.Bounds()
		b.Cursor(ui.CursorPointer).OnClick(func() {
			menu.open, menu.sub = true, ""
			menu.at = ui.Point{X: bb.X - rowBox.X, Y: bb.Y + bb.H - rowBox.Y}
		})
		if b.Hovered() || menu.open {
			b.Background(p.Text.Alpha(0.06))
			ink = p.Text
		}
	} else {
		b.PassThrough()
	}
	ink = b.AnimateColor("ink", ink, ui.Animation{Duration: 100 * time.Millisecond, Ease: twEase})
	b.Children(func() { ui.Icon(c, iconMore).Size(14, 14).TextColor(ink) })
}

// sideProjectMenu is a project row's menu.
func (a *App) sideProjectMenu(m *lodyMenu, r sideRow) {
	pr := a.catalog.Project(r.project)
	if pr == nil {
		return
	}
	var chats []string
	for _, s := range a.catalog.Sessions {
		if s.ProjectID == pr.ID && !s.Archived && s.ProjectGroupID == "" {
			chats = append(chats, s.ID)
		}
	}
	if repo, ok := strings.CutPrefix(pr.ID, "github:"); ok {
		if m.item("edit", iconFolders, locales.SidebarEditProject(), menuItem{}) {
			a.openProjectWizardFrom("github:"+strings.ToLower(repo), repo[strings.LastIndex(repo, "/")+1:], nil)
		}
		if m.item("archive", iconArchive, locales.SidebarArchiveChats(), menuItem{disabled: len(chats) == 0}) {
			a.archiveChats(chats)
		}
		return
	}
	_, local, _ := strings.Cut(pr.ID, ":local:")
	project := *pr
	if m.item("edit", iconFolders, locales.SidebarEditProject(), menuItem{}) {
		a.openProjectWizardFrom("local:"+pr.MachineID+":"+local, pr.Name, nil)
	}
	if m.item("settings", iconSettings2, locales.SidebarProjectSettings(), menuItem{}) {
		a.settingsOpen, a.settingsTab = true, "projects"
		a.openProject(project.ID)
	}
	// Only this machine's folders can be shown in its file manager.
	if pr.MachineID == a.localMachine() && pr.RootPath != "" {
		if m.item("reveal", iconFolderOpen, locales.SidebarRevealProject(), menuItem{}) {
			a.revealFolder(project.RootPath)
		}
	}
	if m.item("archive", iconArchive, locales.SidebarArchiveChats(), menuItem{disabled: len(chats) == 0}) {
		a.archiveChats(chats)
	}
	// Only its machine's owner removes it.
	if a.ownsMachine(pr.MachineID) {
		m.sep()
		if m.item("remove", iconTrash, locales.SidebarRemoveProject(), menuItem{}) {
			a.askRemoveProject(project)
		}
	}
}

// openFolder opens a folder of this machine in its file manager, and
// revealFolder does, saying when it is gone or cannot be opened.
var openFolder = func(path string) error { return mygo.Shell.OpenPath(path) }

func (a *App) revealFolder(path string) {
	if _, err := os.Stat(path); err != nil {
		a.toast("error", locales.SidebarRevealMissing())
		return
	}
	if openFolder(path) != nil {
		a.toast("error", locales.SidebarRevealFailed())
	}
}

// archiveChats archives conversations one after another, as Lody does,
// leaving for New chat if the one open goes.
func (a *App) archiveChats(ids []string) {
	sync := a.sync
	if sync == nil || len(ids) == 0 {
		return
	}
	for _, id := range ids {
		if id == a.selected {
			a.open("")
		}
	}
	go func() {
		var first error
		for _, id := range ids {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			if err := sync.Archive(ctx, id, true); err != nil && first == nil {
				first = err
			}
			cancel()
		}
		if first != nil {
			a.update(func() { a.toast("error", first.Error()) })
		}
	}()
}

// sideGroup is a project of several as the sidebar lists it.
type sideGroup struct {
	id, name string
	removed  bool // deleted since: its conversations keep its name
}

// sidebarGroups places the projects of several under the machines, as
// Lody's placeProjectGroups: one whose folders bind it to a machine sits
// under it, conversations or not, as its folders do; one that runs
// anywhere, or was deleted, sits where its latest conversation ran, while
// it has one.
func (a *App) sidebarGroups(byGroup map[string][]*catalog.Session) map[string][]sideGroup {
	listed := map[string]bool{}
	for _, id := range a.catalog.MachineIDs {
		listed[id] = true
	}
	out := map[string][]sideGroup{}
	place := func(g sideGroup, bound string) {
		machine := bound
		if !listed[machine] {
			machine = ""
			if list := byGroup[g.id]; len(list) > 0 {
				machine = list[0].MachineID
			}
		}
		if listed[machine] {
			out[machine] = append(out[machine], g)
		}
	}
	defined := map[string]bool{}
	for _, g := range a.catalog.Groups {
		defined[g.ID] = true
		place(sideGroup{id: g.ID, name: g.Name}, g.BoundMachine())
	}
	var removed []sideGroup
	seen := map[string]bool{}
	for id, list := range byGroup {
		if !defined[id] && !seen[id] {
			seen[id] = true
			removed = append(removed, sideGroup{id: id, name: firstNonEmpty(list[0].ProjectGroupName, locales.PickerRemovedGroup()), removed: true})
		}
	}
	sortFold(removed, func(g sideGroup) string { return g.name })
	for _, g := range removed {
		place(g, "")
	}
	return out
}

// sortFold orders items by a name as a person reads it.
func sortFold[T any](items []T, name func(T) string) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && lessFold(name(items[j]), name(items[j-1])); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

// groupRow is a project of several under its machine, as Lody's: its
// mark of several folders giving way to the chevron with the pointer,
// its name, and on hover its ⋯ and New chat in this project. Its menu
// edits it (read only for whoever did not create it), archives its
// chats, and, for its creator, deletes it.
func (a *App) groupRow(c *ui.Context, p *palette, r sideRow) {
	collapsed := a.collapsed[r.group]
	selected := a.newChat.project == "group:"+r.project && a.selected == ""
	g := a.group(r.project)
	row := ui.Row(c.Key(r.key)).Height(30).Padding(4, 8).Border(1, ui.Transparent).Radius(6).Gap(8).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label(r.label).Transition(twFade).
		OnClick(func() {
			a.collapsed[r.group] = !a.collapsed[r.group]
			a.savePrefs()
		})
	menu := a.rowMenu("side-group/" + r.project)
	id := r.project
	lodyContextMenu(c, p, row, menu, 28, func(m *lodyMenu) {
		var chats []string
		for _, s := range a.catalog.Sessions {
			if s.ProjectGroupID == id && !s.Archived {
				chats = append(chats, s.ID)
			}
		}
		if g != nil && m.item("edit", iconFolders, locales.SidebarEditProject(), menuItem{}) {
			a.requestProjectEdit(id)
		}
		if m.item("archive", iconArchive, locales.SidebarArchiveChats(), menuItem{disabled: len(chats) == 0}) {
			a.archiveChats(chats)
		}
		if g != nil && a.canManageGroup(*g) && m.item("delete", iconTrash, locales.SidebarRemoveProject(), menuItem{}) {
			a.requestProjectDelete(id)
		}
	})
	hovered := row.Hovered() || menu.open
	switch {
	case selected:
		row.Background(p.SideSelect)
	case hovered:
		row.Background(p.SideHover)
	}
	actions := row.AnimateWith("actions", b2f(hovered), 100*time.Millisecond, twEase)
	swap := row.AnimateWith("swap", b2f(hovered), 100*time.Millisecond, ui.CubicBezier(0.4, 0, 0.2, 1))
	turn := row.AnimateWith("chevron", b2f(collapsed), 100*time.Millisecond, ui.CubicBezier(0.4, 0, 0.2, 1))
	rowBox := row.Bounds()
	row.Children(func() {
		ui.Box(c).Size(14, 16).Center().Shrink(0).Children(func() {
			if swap < 1 {
				ink := p.SideMuted
				if selected {
					ink = p.SideRowSel
				}
				ui.Icon(c, iconFolders).Size(16, 16).TextColor(ink).Opacity(1 - swap).Absolute()
			}
			if swap > 0 {
				ui.Icon(c, iconChevronDown).Size(16, 16).TextColor(p.SideRow).Rotate(-90 * turn).Opacity(swap).Absolute()
			}
		})
		ui.Text(c, r.label).FontSize(sizeSub).TextColor(p.SideRow).SingleLine().Grow(1).MinWidth(0)
		if actions <= 0 {
			return
		}
		sideMoreButton(c, p, menu, rowBox, actions, hovered)
		// A deleted project starts nothing new.
		if g == nil {
			return
		}
		b := ui.ButtonBase(c.Key("new-chat")).Size(20, 20).Radius(4).Label(locales.SidebarGroupNewChat()).Opacity(actions).
			Transition(ui.ElementTransition{Colors: true, Duration: 100 * time.Millisecond, Ease: twEase})
		ink := p.Secondary.Alpha(0.7)
		if hovered {
			tip(c, p, b, locales.SidebarGroupNewChat()).Cursor(ui.CursorPointer).OnClick(func() { a.newChatInGroup(id) })
			if b.Hovered() {
				b.Background(p.Text.Alpha(0.06))
				ink = p.Text
			}
		} else {
			b.PassThrough()
		}
		ink = b.AnimateColor("ink", ink, ui.Animation{Duration: 100 * time.Millisecond, Ease: twEase})
		b.Children(func() { ui.Icon(c, iconSquarePen).Size(14, 14).TextColor(ink) })
	})
}
