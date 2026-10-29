package app

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// Archived sessions are chosen several at a time as Lody's: a row's check
// box shows with the pointer, fading in over 120ms; checking it starts
// choosing, and while choosing a click on a row chooses it, a group's
// header checks all its rows or none, and the title bar says how many
// with Restore and Delete for them all, Delete asking first. Choosing ends
// when nothing is chosen, as the sessions done with leave the archive.

// archivePicking reports whether archived sessions are being chosen.
func (a *App) archivePicking() bool { return len(a.archivePick) > 0 }

// togglePick chooses id, or leaves it.
func (a *App) togglePick(id string) {
	if a.archivePick == nil {
		a.archivePick = map[string]bool{}
	}
	if a.archivePick[id] {
		delete(a.archivePick, id)
	} else {
		a.archivePick[id] = true
	}
}

// pruneArchivePick leaves what is no longer archived.
func (a *App) pruneArchivePick() {
	for id := range a.archivePick {
		if s := a.catalog.Session(id); s == nil || !s.Archived {
			delete(a.archivePick, id)
		}
	}
}

// selectedFill is @lody/ui's colors.selectedFill: the ground of a chosen
// row, and of a control's own fill under the pointer.
func selectedFill(p *palette) ui.Color {
	if p.Dark {
		return ui.Hex("#232323")
	}
	return ui.Hex("#ECECEC")
}

// archiveCheck is a row's or a header's check box, its slot 8px in from
// the start and centered.
func (a *App) archiveCheck(c *ui.Context, p *palette, key, label string, on, some bool, height float32, toggle func()) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Absolute().Left(8).Top((height-16)/2).Size(16, 16).Cursor(ui.CursorPointer).Label(label).OnClick(toggle)
	b.Children(func() { lodyCheckbox(c, p, "box", on, some) })
	return b
}

// archivePickBar is the title bar while choosing: the way out, how many,
// and the actions for them all.
func (a *App) archivePickBar(c *ui.Context, p *palette) {
	iconButton(c, p, iconX, locales.ArchiveExitSelection(), p.Hover).OnClick(func() { a.archivePick = nil })
	ui.Text(c, locales.ArchiveSelectedCount(len(a.archivePick))).FontSize(sizeSub).TextColor(p.Secondary)
	ui.Spacer(c)
	busy := a.archiveBulk != ""
	restore := archiveBarButton(c, p, "restore", iconUndo, locales.ArchiveBulkRestore(), false, busy)
	restore.OnClick(func() { a.bulkArchiveAction("restore") })
	del := archiveBarButton(c, p, "delete", iconTrash, locales.ArchiveBulkDelete(), true, busy)
	del.OnClick(func() { a.archiveBulkOpen = true })
}

// archiveBarButton is a small button of the title bar: secondary, or
// destructive in the danger ink.
func archiveBarButton(c *ui.Context, p *palette, key string, icon *ui.SVG, label string, destructive, disabled bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Height(28).Padding(0, 10).Radius(8).Gap(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Label(label).Disabled(disabled).Transition(colorFade)
	ground, ink := p.Text.Alpha(0.06), p.Text
	if destructive {
		ground, ink = p.Danger, ui.RGB(255, 255, 255)
	}
	if b.Hovered() && !disabled {
		ground = ground.Mix(p.Text, 0.08)
	}
	b.Background(ground)
	if disabled {
		b.Opacity(0.45)
	}
	pressable(b, 0.97)
	b.Children(func() {
		ui.Icon(c, icon).Size(14, 14).TextColor(ink)
		ui.Text(c, label).FontSize(sizeSub).FontWeight(500).TextColor(ink)
	})
	return b
}

// bulkArchiveAction restores or deletes the chosen sessions, one after
// another, as Lody does; those that fail stay chosen.
func (a *App) bulkArchiveAction(kind string) {
	sync := a.sync
	if sync == nil || a.archiveBulk != "" || len(a.archivePick) == 0 {
		return
	}
	var ids []string
	for id := range a.archivePick {
		ids = append(ids, id)
	}
	a.archiveBulk = kind
	go func() {
		var failed []string
		var first error
		for _, id := range ids {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			var err error
			if kind == "delete" {
				err = sync.Delete(ctx, id)
			} else {
				err = sync.Archive(ctx, id, false)
			}
			cancel()
			if err != nil {
				failed = append(failed, id)
				if first == nil {
					first = err
				}
			}
		}
		a.update(func() {
			a.archiveBulk = ""
			a.archivePick = nil
			for _, id := range failed {
				a.togglePick(id)
			}
			if kind == "delete" {
				a.archiveBulkOpen = false
			}
			if first != nil {
				what := "restore the sessions"
				if kind == "delete" {
					what = "delete the sessions"
				}
				a.notice = locales.ActionFailed(what, first.Error())
			}
		})
	}()
}

// bulkDeleteDialog confirms deleting the chosen sessions for good, saying
// so of their worktrees when any is a code session.
func (a *App) bulkDeleteDialog(c *ui.Context, p *palette) {
	if a.archiveBulk == "delete" {
		// It stays while it deletes.
		a.archiveBulkOpen = true
	}
	ui.DialogBase(c, &a.archiveBulkOpen, func(backdrop, panel ui.Element) {
		sheetIn(backdrop, panel)
		backdrop.Background(ui.RGBA(0, 0, 0, 0.28))
		panel.Width(400).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Padding(20).Gap(12).Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Text(c, locales.ArchiveBulkDeleteTitle(len(a.archivePick))).FontSize(16).FontWeight(600).TextColor(p.Strong)
		description := locales.ArchiveBulkDeleteChatOnly()
		for id := range a.archivePick {
			if s := a.catalog.Session(id); s != nil && a.catalog.Project(s.ProjectID) != nil {
				description = locales.ArchiveBulkDeleteMixed()
				break
			}
		}
		ui.Text(c, description).FontSize(sizeSub).LineHeight(1.45).TextColor(p.Secondary)
		ui.Row(c).Gap(8).Justify(ui.End).Padding(8, 0, 0, 0).Children(func() {
			busy := a.archiveBulk != ""
			cancel := pressable(ghostButton(c, p, locales.Cancel()).Border(1, p.Border), 0.97).Disabled(busy)
			cancel.OnClick(func() { a.archiveBulkOpen = false })
			b := ui.ButtonBase(c.Key("confirm")).Height(28).Padding(0, 12).Radius(8).Background(p.Danger).Cursor(ui.CursorPointer).
				Label(locales.ArchiveDelete()).Disabled(busy || len(a.archivePick) == 0).Transition(colorFade)
			if b.Hovered() && !busy {
				b.Background(p.Danger.Mix(p.Text, 0.1))
			}
			pressable(b, 0.97)
			b.OnClick(func() { a.bulkArchiveAction("delete") })
			b.Children(func() {
				ui.Text(c, locales.ArchiveDelete()).FontSize(sizeSub).FontWeight(500).TextColor(ui.RGB(255, 255, 255))
			})
		})
	})
}

// archivePickGroup checks all of a group's sessions, or none if all are.
func (a *App) archivePickGroup(sessions []catalog.Session) {
	all := true
	for _, s := range sessions {
		all = all && a.archivePick[s.ID]
	}
	if a.archivePick == nil {
		a.archivePick = map[string]bool{}
	}
	for _, s := range sessions {
		if all {
			delete(a.archivePick, s.ID)
		} else {
			a.archivePick[s.ID] = true
		}
	}
}
