package app

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
)

// catalogAction runs a catalog write in the background and reports a
// failure in the sidebar.
func (a *App) catalogAction(what string, fn func(ctx context.Context, sync *catalog.Sync) error) {
	sync := a.sync
	if sync == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := fn(ctx, sync); err != nil {
			a.update(func() { a.notice = "Couldn't " + what + ": " + err.Error() })
		}
	}()
}

// sessionMenu is a session row's context menu.
func (a *App) sessionMenu(m *ui.Menu, s catalog.Session) {
	if m.Item("Rename").Chosen() {
		a.renaming, a.renameText = s.ID, s.Title
	}
	if m.Item("Pinned").Checked(s.Pinned).Chosen() {
		a.catalogAction("pin the chat", func(ctx context.Context, sync *catalog.Sync) error {
			return sync.Pin(ctx, s.ID, !s.Pinned)
		})
	}
	if m.Item("Mark as Read").Disabled(!s.Unread()).Chosen() {
		a.catalogAction("mark the chat read", func(ctx context.Context, sync *catalog.Sync) error {
			return sync.MarkRead(ctx, s)
		})
	}
	m.Separator()
	label := "Archive"
	if s.Archived {
		label = "Restore"
	}
	if m.Item(label).Chosen() {
		if !s.Archived && a.selected == s.ID {
			a.open("")
		}
		a.catalogAction(strings.ToLower(label)+" the chat", func(ctx context.Context, sync *catalog.Sync) error {
			return sync.Archive(ctx, s.ID, !s.Archived)
		})
	}
}

// renameField edits a session's title in place of its row's text.
func (a *App) renameField(c *ui.Context, p *palette, s *catalog.Session) {
	in := ui.TextInputBase(c.Key("rename/"+s.ID), &a.renameText).FontSize(sizeSub).TextColor(p.SideRowSel).
		Grow(1).MinWidth(0).AutoFocus().Label("Title").
		OnTextKey(func(k ui.TextKey) bool {
			if k.Key == ui.KeyEscape {
				a.renaming = ""
				return true
			}
			return false
		})
	if in.Submitted() {
		title := strings.TrimSpace(a.renameText)
		id := s.ID
		a.renaming = ""
		if title != "" && title != s.Title && len(title) <= 200 {
			a.catalogAction("rename the chat", func(ctx context.Context, sync *catalog.Sync) error {
				return sync.Rename(ctx, id, title)
			})
		}
	}
}
