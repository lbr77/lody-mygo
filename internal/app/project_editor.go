package app

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/locales"
)

// Lody's project editor host (settings/project-editor-host.tsx): the one
// editor of projects of several repositories, mounted once over the
// workspace, which Settings › Projects, the sidebar's project rows and
// New chat's project picker all ask for. A project's creator edits it in
// the wizard and may delete it; anyone else sees it as it is, read only.

// groupViewState is a project shown read only, to whoever did not
// create it.
type groupViewState struct {
	open  bool
	group catalog.ProjectGroup
}

// groupDeleteState is a project whose deletion is being confirmed.
type groupDeleteState struct {
	open     bool
	id, name string
	removing bool
}

// canManageGroup is whether the user may change a project: only its
// creator edits or deletes it; everyone else can use it.
func (a *App) canManageGroup(g catalog.ProjectGroup) bool {
	return a.account != nil && g.OwnerUserID == a.account.User.ID
}

// group is the workspace's project of several repositories with id.
func (a *App) group(id string) *catalog.ProjectGroup {
	for i := range a.catalog.Groups {
		if a.catalog.Groups[i].ID == id {
			return &a.catalog.Groups[i]
		}
	}
	return nil
}

// requestProjectEdit opens a project: in the wizard for its creator, read
// only for anyone else. A project not synced yet opens nothing.
func (a *App) requestProjectEdit(id string) {
	g := a.group(id)
	if g == nil {
		return
	}
	if !a.canManageGroup(*g) {
		a.groupView = groupViewState{open: true, group: *g}
		return
	}
	a.openProjectEditor(*g)
}

// requestProjectDelete asks before deleting a project. Only its creator
// deletes it; anyone else asking is a no-op, not an error.
func (a *App) requestProjectDelete(id string) {
	if g := a.group(id); g != nil && a.canManageGroup(*g) {
		a.groupDelete = groupDeleteState{open: true, id: g.ID, name: g.Name}
	}
}

// projectEditorHost draws the editor and what it opens over everything
// else: the wizard, the folder browser picking for it, the read-only
// view and the deletion's question; and, outside Settings, the removal
// of a folder the sidebar asked for.
func (a *App) projectEditorHost(c *ui.Context, p *palette) {
	a.projectWizardDialog(c, p)
	if a.addProject.selectOnly {
		a.addProjectDialog(c, p)
	}
	a.groupViewDialog(c, p)
	a.groupDeleteDialog(c, p)
	if !a.settingsOpen {
		a.removalDialog(c, p)
	}
}

// memberTreeRow is a member where it sits in a conversation's directory:
// how deep, and its path there.
type memberTreeRow struct {
	m     groupMember
	depth int
	path  string
}

// memberTree lists members as their directories nest: each parent, then
// its submodules. A cycle leaves members unvisited, which then show flat,
// so nothing disappears.
func memberTree(members []groupMember) []memberTreeRow {
	ids := map[string]bool{}
	for _, m := range members {
		ids[m.id] = true
	}
	children := map[string][]groupMember{}
	var roots []groupMember
	for _, m := range members {
		if m.submodule() && ids[m.parent] {
			children[m.parent] = append(children[m.parent], m)
		} else {
			roots = append(roots, m)
		}
	}
	var out []memberTreeRow
	seen := map[string]bool{}
	var visit func(m groupMember, depth int, prefix string)
	visit = func(m groupMember, depth int, prefix string) {
		if seen[m.id] {
			return
		}
		seen[m.id] = true
		own := m.dir
		if m.submodule() {
			own = strings.TrimPrefix(prefix+m.path, "/")
		}
		out = append(out, memberTreeRow{m, depth, own})
		for _, child := range children[m.id] {
			visit(child, depth+1, own+"/")
		}
	}
	for _, m := range roots {
		visit(m, 0, "")
	}
	for _, m := range members {
		visit(m, 0, "")
	}
	return out
}

// memberIcon is a member's glyph: a submodule's branch, a folder used in
// place, a repository from a machine's copy, or GitHub's mark for a clone.
func memberIcon(m groupMember) *ui.SVG {
	switch {
	case m.submodule():
		return iconBranch
	case m.source.kind == "local":
		return iconFolder
	case m.source.preferLocal:
		return iconFolderGit2
	}
	return iconGithub
}

// groupViewDialog is a project someone else created, as it is (Lody's
// ProjectGroupView): its name and why it cannot be changed above, its
// members where they sit in a conversation's directory, each submodule a
// step further in under its parent with its path at the end, then its
// instructions, and Close.
func (a *App) groupViewDialog(c *ui.Context, p *palette) {
	st := &a.groupView
	if !st.open {
		return
	}
	f := a.wizardFrame()
	d := draftOf(st.group)
	w, h := c.Size()
	ui.DialogBase(c, &st.open, func(backdrop, panel ui.Element) {
		sheetIn(backdrop, panel)
		backdrop.Background(ui.RGBA(0, 0, 0, 0.22))
		panel.Width(min(620, w-80)).Height(min(680, h*0.88)).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Clip().
			Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Column(c).Fill().Children(func() {
			sheetHeader(c, p, st.group.Name, locales.GroupReadOnly(), func() { st.open = false })
			ui.Scroll(c.Key("group-view")).FillWidth().Grow(1).MinHeight(0).Padding(8, 20).Gap(12).Children(func() {
				wizardSection(c, p, "members", locales.GroupMembers(), "", func() {
					ui.Column(c.Key("members")).FillWidth().Radius(10).Border(1, p.Border).Background(p.Canvas).Clip().Dividers(1, p.Border).Shrink(0).Children(func() {
						for _, r := range memberTree(d.members) {
							label, detail := f.labelOf(r.m), f.detailOf(r.m)
							ui.Row(c.Key(r.m.id)).FillWidth().Padding(6, 12, 6, 12+float32(r.depth)*20).Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Icon(c, memberIcon(r.m)).Size(14, 14).TextColor(p.Secondary).Shrink(0)
								ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(2).Children(func() {
									ui.Text(c, label).FontSize(sizeSub).TextColor(p.Text).SingleLine()
									if detail != "" {
										ui.Text(c, detail).FontSize(sizeFoot).TextColor(p.Secondary)
									}
								})
								path := ui.Text(c, r.path).Font(fontMono).FontSize(sizeFoot).TextColor(p.Tertiary).SingleLine().Shrink(0).MaxWidth(min(620, w-80) * 0.4)
								tip(c, p, path, r.path)
							})
						}
					})
				})
				if instructions := strings.TrimSpace(st.group.Instructions); instructions != "" {
					wizardSection(c, p, "instructions", locales.GroupInstructions(), "", func() {
						ui.Text(c, st.group.Instructions).FontSize(sizeFoot).LineHeight(1.5).TextColor(p.Secondary).Selectable()
					})
				}
			})
			ui.Row(c.Key("footer")).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).Shrink(0).Children(func() {
				outlineButton(c, p, nil, locales.Close()).OnClick(func() { st.open = false })
			})
		})
	})
}

// groupDeleteDialog asks before deleting a project, as Lody's alert: its
// conversations keep their repositories and branches, and the folders
// and repositories themselves are untouched. It stays while the row
// goes, its buttons held, a spinner beside Remove.
func (a *App) groupDeleteDialog(c *ui.Context, p *palette) {
	st := &a.groupDelete
	if !st.open {
		return
	}
	ui.DialogBase(c, &st.open, func(backdrop, panel ui.Element) {
		sheetIn(backdrop, panel)
		backdrop.Background(ui.RGBA(0, 0, 0, 0.22))
		panel.Width(400).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Padding(20).Gap(12).Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Text(c, locales.GroupDeleteTitle()).FontSize(16).FontWeight(600).TextColor(p.Strong)
		ui.Text(c, locales.GroupDeleteBody(st.name)).FontSize(sizeSub).LineHeight(1.45).TextColor(p.Secondary)
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Padding(8, 0, 0, 0).Children(func() {
			cancel := ghostButton(c, p, locales.Cancel()).Border(1, p.Border).Disabled(st.removing)
			pressable(cancel, 0.97).OnClick(func() { st.open = false })
			b := ui.ButtonBase(c.Key("remove")).Height(28).Padding(0, 12).Gap(6).Radius(8).Background(p.Danger).AlignItems(ui.Center).
				Cursor(ui.CursorPointer).Label(locales.GroupRemove()).Disabled(st.removing).Transition(colorFade)
			if b.Hovered() && !st.removing {
				b.Background(p.Danger.Mix(p.Text, 0.1))
			}
			pressable(b, 0.97)
			b.OnClick(a.deleteGroup)
			b.Children(func() {
				if st.removing {
					spinner(c).Size(12, 12)
				}
				ui.Text(c, locales.GroupRemove()).FontSize(sizeSub).FontWeight(500).TextColor(ui.RGB(255, 255, 255))
			})
		})
	})
	// A deletion in flight keeps it open.
	if !st.open && st.removing {
		st.open = true
	}
}

// deleteGroup deletes the project's row from the workspace's stream; its
// conversations keep their snapshot of it. The question closes either
// way: Lody only logs a failure.
func (a *App) deleteGroup() {
	st := &a.groupDelete
	if st.removing {
		return
	}
	sync, id := a.sync, st.id
	st.removing = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		wizardWrite(sync, ctx, "", func(f *flock.Flock) error { return f.Delete([]any{"project", id}) })
		a.update(func() {
			if st.id == id {
				*st = groupDeleteState{}
			}
		})
	}()
}
