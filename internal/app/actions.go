package app

import (
	"context"
	"github.com/lbr77/lody-mygo/locales"
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
			a.update(func() { a.notice = locales.ActionFailed(what, err.Error()) })
		}
	}()
}

// renameField edits a session's title in place of its row's text.
func (a *App) renameField(c *ui.Context, p *palette, s *catalog.Session) {
	in := ui.TextInputBase(c.Key("rename/"+s.ID), &a.renameText).FontSize(sizeSub).TextColor(p.SideRowSel).
		Grow(1).MinWidth(0).AutoFocus().Label(locales.RenameLabel()).
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
			a.catalogAction(locales.FailRename(), func(ctx context.Context, sync *catalog.Sync) error {
				return sync.Rename(ctx, id, title)
			})
		}
	}
}
