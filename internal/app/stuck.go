package app

import (
	"fmt"
	"runtime"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// A workspace that stays connecting for 45 seconds says so, as Lody's
// stuck-connection banner: a card that springs up near the bottom, with a
// way to recover — reload, clear the local cache, or copy diagnostics.
// Local-only mode has no cloud connection to get stuck.

// stuckAfter is how long connecting takes before the banner shows.
const stuckAfter = 45 * time.Second

// stuckSpring brings the banner up 12px, as Lody's (420, 34, 0.8).
var stuckSpring = ui.Animation{Spring: ui.SpringOptions{Stiffness: 420, Damping: 34, Mass: 0.8}}

// stuckState is how long the connection has been waiting, and what the
// reader did about it.
type stuckState struct {
	since     time.Time
	dismissed bool
	recovery  bool
	copied    time.Time
}

// connectionStuck reports whether the workspace has been connecting long
// enough to say so, asking for a frame when it will have been.
func (a *App) connectionStuck(c *ui.Context) bool {
	st := &a.stuck
	connecting := a.sync != nil && !a.syncState.Live && !a.localOnly && a.tokenNow() != ""
	if !connecting {
		st.since, st.dismissed = time.Time{}, false
		return false
	}
	if st.since.IsZero() {
		st.since = time.Now()
	}
	if left := stuckAfter - time.Since(st.since); left > 0 {
		c.After(left)
		return false
	}
	return true
}

func (a *App) stuckBanner(c *ui.Context, p *palette) {
	st := &a.stuck
	stuck := a.connectionStuck(c)
	if stuck && st.recovery {
		a.recoveryDialog(c, p)
	}
	if !stuck || st.dismissed || st.recovery {
		return
	}
	ui.Row(c.Key("stuck")).Absolute().Left(0).Right(0).Bottom(84).Padding(0, 16).Justify(ui.Center).PassThrough().Children(func() {
		card := ui.Column(c.Key("stuck-card")).FillWidth().MaxWidth(448).Padding(10, 12).Gap(4).Radius(16).Border(1, p.Border.Alpha(0.7)).
			Background(p.Popover.Alpha(0.95)).Shadow(0, 10, 15, -3, ui.RGBA(0, 0, 0, 0.1)).Role(ui.RoleStatus).
			Transition(ui.ElementTransition{Animation: &stuckSpring, Enter: &ui.Motion{Y: 12}})
		card.Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				spinner(c).Size(16, 16)
				ui.Text(c, locales.StuckTitle()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
				iconButton(c.Key("dismiss"), p, iconX, locales.StuckDismiss(), p.Hover).Size(24, 24).OnClick(func() { st.dismissed = true })
			})
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Padding(0, 0, 0, 24).Children(func() {
				ui.Text(c, locales.StuckDescription()).FontSize(sizeCaption).LineHeight(1.35).TextColor(p.Secondary).Grow(1).MinWidth(0)
				b := ui.ButtonBase(c.Key("recover")).Height(28).Padding(0, 12).Radius(14).Background(p.Text).Cursor(ui.CursorPointer).
					Label(locales.StuckRecover()).OnClick(func() { st.recovery = true })
				pressable(b, 0.97)
				b.Children(func() { ui.Text(c, locales.StuckRecover()).FontSize(sizeCaption).FontWeight(500).TextColor(p.Canvas) })
			})
		})
	})
}

// recoveryDialog is Lody's recovery page for a slow open, as a sheet.
func (a *App) recoveryDialog(c *ui.Context, p *palette) {
	st := &a.stuck
	open := true
	sheet(c, p, &open, 520, func() {
		sheetHeader(c, p, locales.RecoverySlowTitle(), locales.RecoveryDescription(), func() { st.recovery = false })
		ui.Row(c).FillWidth().Wrap().Gap(8).Padding(8, 20, 20, 20).Children(func() {
			primaryButton(c, p, locales.RecoveryReload(), true).OnClick(func() {
				st.recovery = false
				if a.workspaceID != "" {
					a.connect(a.workspaceID)
				}
			})
			outlineButton(c, p, nil, locales.RecoveryClearCache()).OnClick(func() {
				st.recovery = false
				a.confirm(locales.PrefClearCacheTitle(), locales.PrefClearCacheBody(), locales.PrefClearCacheButton(), a.clearCache)
			})
			label := locales.RecoveryCopyDiagnostics()
			if time.Since(st.copied) < 2*time.Second {
				label = locales.RecoveryCopied()
				c.After(2*time.Second - time.Since(st.copied))
			}
			ghostButton(c, p, label).OnClick(func() {
				writeClipboard(a.diagnostics())
				st.copied = time.Now()
			})
		})
	})
	if !open {
		st.recovery = false
	}
}

// diagnostics is what the recovery copies: the client, the workspace and
// how its connection stands.
func (a *App) diagnostics() string {
	waited := time.Duration(0)
	if !a.stuck.since.IsZero() {
		waited = time.Since(a.stuck.since).Round(time.Second)
	}
	return fmt.Sprintf("Lody Native %s (%s/%s)\nWorkspace: %s\nConnecting for: %s\nCatalog live: %v\nLast failure: %s\nPresence live: %v\nLocal-only: %v\n",
		clientVersion(), runtime.GOOS, runtime.GOARCH, a.workspaceID, waited, a.syncState.Live, firstNonEmpty(a.syncState.Reason, "none"),
		a.presence.Live, a.localOnly)
}
