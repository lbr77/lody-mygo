package app

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/shortcuts"
	"github.com/lbr77/lody-mygo/locales"
)

// Prompt shortcuts, as Lody's page keeps them: prompts saved to send with
// /slug, private or shared with the workspace. This client edits those
// without a scope, as lody-ios does.

type shortcutForm struct {
	open        bool
	base        *shortcuts.Entry
	loading     bool
	emoji       string
	name        string
	slug        string
	slugEdited  bool
	description string
	prompt      string
	shared      bool
	busy        bool
	err         string
}

func (a *App) shortcutStore() *shortcuts.Store {
	user := ""
	if a.account != nil {
		user = a.account.User.ID
	}
	return &shortcuts.Store{Convex: a.convex, HTTP: a.api.HTTP, Workspace: a.workspaceID, User: user}
}

func (a *App) shortcutsPage(c *ui.Context, p *palette) {
	pageHeader(c, p, "Prompt Shortcuts", locales.ShortcutsLead(), func() {
		outlineButton(c, p, iconPlus, locales.ShortcutsNew()).OnClick(func() { a.shortcut = shortcutForm{open: true} })
	})
	a.settingsErrorLine(c, p)
	store := a.shortcutStore()
	f := a.fetch("shortcuts", func(ctx context.Context) (any, error) { return store.List(ctx) })
	if !pending(c, p, f) {
		a.shortcutDialog(c, p)
		return
	}
	list, _ := f.value.([]shortcuts.Entry)
	sort.SliceStable(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	if len(list) == 0 {
		emptyCard(c, p, locales.ShortcutsNone())
	} else {
		card(c, p, func() {
			for _, e := range list {
				a.shortcutRow(c.Key(e.ID), p, e)
			}
		})
	}
	a.shortcutDialog(c, p)
}

func (a *App) shortcutRow(c *ui.Context, p *palette, e shortcuts.Entry) {
	entry := e
	mine := a.account != nil && e.OwnerUserID == a.account.User.ID
	ui.Row(c).FillWidth().MinHeight(48).Padding(8, 8, 8, 14).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, firstNonEmpty(e.Emoji, "📝")).FontSize(18).Shrink(0)
		body := ui.Column(c).Grow(1).MinWidth(0).Gap(2)
		if mine && e.Simple() {
			body.Cursor(ui.CursorPointer).Label(e.Name).OnClick(func() { a.editShortcut(entry) })
		}
		body.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, entry.Name).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine()
				ui.Text(c, "/"+entry.Slug).Font(fontMono).FontSize(sizeFoot).TextColor(p.Secondary)
			})
			if entry.Description != "" {
				ui.Text(c, entry.Description).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
			}
		})
		label := locales.ShortcutsPrivate()
		if entry.Visibility == "workspace" {
			label = locales.ShortcutsShared()
		}
		ui.Text(c, label).FontSize(sizeCaption).TextColor(p.Secondary).Padding(1, 6).Radius(5).Background(p.Text.Alpha(0.06))
		if !mine {
			return
		}
		iconButton(c, p, iconTrash, locales.ActionDelete(), p.Hover).OnClick(func() {
			a.confirm(locales.ShortcutsDeleteTitle(entry.Name), locales.ShortcutsDeleteBody(), locales.ActionDelete(), func() {
				store := a.shortcutStore()
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel()
					err := store.Delete(ctx, entry)
					a.update(func() {
						if err != nil {
							a.settingsError = err.Error()
						}
						a.refetchKey("shortcuts")
					})
				}()
			})
		})
	})
}

// editShortcut opens the editor on a shortcut, reading its prompt.
func (a *App) editShortcut(e shortcuts.Entry) {
	entry := e
	a.shortcut = shortcutForm{open: true, base: &entry, loading: true, emoji: e.Emoji, name: e.Name, slug: e.Slug, slugEdited: true,
		description: e.Description, shared: e.Visibility == "workspace"}
	f := &a.shortcut
	store := a.shortcutStore()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		sc, err := store.Read(ctx, entry)
		a.update(func() {
			f.loading = false
			if err != nil {
				f.err = err.Error()
				return
			}
			f.prompt = sc.Prompt
		})
	}()
}

func (a *App) saveShortcut() {
	f := &a.shortcut
	visibility := "private"
	if f.shared {
		visibility = "workspace"
	}
	value := shortcuts.Shortcut{
		Name: strings.TrimSpace(f.name), Emoji: strings.TrimSpace(f.emoji), Slug: strings.TrimSpace(f.slug),
		Description: strings.TrimSpace(f.description), Prompt: f.prompt, Visibility: visibility,
	}
	if err := value.Validate(); err != nil {
		f.err = strings.TrimPrefix(err.Error(), "shortcuts: ")
		return
	}
	store, base := a.shortcutStore(), f.base
	f.busy, f.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_, err := store.Save(ctx, value, base)
		a.update(func() {
			f.busy = false
			if err != nil {
				f.err = strings.TrimPrefix(err.Error(), "shortcuts: ")
				return
			}
			f.open = false
			a.refetchKey("shortcuts")
		})
	}()
}

func (a *App) shortcutDialog(c *ui.Context, p *palette) {
	f := &a.shortcut
	if !f.open {
		return
	}
	title := locales.ShortcutsNew()
	if f.base != nil {
		title = locales.ShortcutsEdit()
	}
	sheet(c, p, &f.open, 620, func() {
		sheetHeader(c, p, title, locales.ShortcutsDialogLead(), func() { f.open = false })
		ui.Column(c).FillWidth().Padding(8, 20).Gap(10).Children(func() {
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				input(c, p, &f.emoji, "📝", locales.ShortcutsEmoji()).Width(48)
				before := f.name
				input(c, p, &f.name, locales.ShortcutsName(), locales.ShortcutsName()).Grow(1).AutoFocus()
				if f.name != before && !f.slugEdited && f.base == nil {
					f.slug = shortcuts.Slugify(f.name)
				}
				ui.Row(c).Width(200).Shrink(0).Gap(4).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "/").Font(fontMono).FontSize(sizeSub).TextColor(p.Secondary)
					before := f.slug
					input(c, p, &f.slug, "review-pr", locales.ShortcutsSlug()).Font(fontMono).Grow(1)
					if f.slug != before {
						f.slugEdited = true
					}
				})
			})
			input(c, p, &f.description, locales.ShortcutsDescription(), locales.ShortcutsDescription())
			ui.Column(c).Gap(2).Margin(6, 0, 0, 0).Children(func() {
				ui.Text(c, "Prompt").FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
				ui.Text(c, locales.ShortcutsPromptHint()).FontSize(sizeCaption).LineHeight(1.45).TextColor(p.Secondary)
			})
			if f.loading {
				spinner(c).Size(14, 14)
			} else {
				area(c, p, &f.prompt, locales.ShortcutsPromptPlaceholder(), "Prompt", 6)
			}
			ui.Row(c).FillWidth().Padding(10, 12).Radius(8).Gap(12).AlignItems(ui.Center).Background(p.Text.Alpha(0.035)).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, locales.ShortcutsShare()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
					ui.Text(c, locales.ShortcutsShareHint()).FontSize(sizeCaption).TextColor(p.Secondary)
				})
				sw := ui.Switch(c, &f.shared).Label(locales.ShortcutsShare())
				if f.base != nil {
					// A shortcut keeps its visibility; Lody republishes to change it.
					sw.Disabled(true)
				}
			})
			if f.err != "" {
				ui.Text(c, f.err).FontSize(sizeFoot).TextColor(p.Danger).Selectable()
			}
		})
		ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).Children(func() {
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { f.open = false })
			if f.busy {
				spinner(c).Size(14, 14)
			}
			label := locales.ProviderCreate()
			if f.base != nil {
				label = locales.Save()
			}
			ready := strings.TrimSpace(f.name) != "" && strings.TrimSpace(f.prompt) != "" && f.slug != "" && !f.busy && !f.loading
			primaryButton(c, p, label, ready).OnClick(a.saveShortcut)
		})
	})
}
