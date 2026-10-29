package app

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// A workspace that stays connecting for 45 seconds says so, as Lody's
// stuck-connection banner: a card that springs up near the bottom, whose
// Recover opens the recovery page over the whole window — reload, clear
// the local cache, copy diagnostics, or, last, clear everything and sign
// out. Local-only mode has no cloud connection to get stuck.

// stuckAfter is how long connecting takes before the banner shows.
const stuckAfter = 45 * time.Second

// stuckSpring brings the banner up 12px, as Lody's (420, 34, 0.8).
var stuckSpring = ui.Animation{Spring: ui.SpringOptions{Stiffness: 420, Damping: 34, Mass: 0.8}}

// stuckState is how long the connection has been waiting, and what the
// reader did about it.
type stuckState struct {
	since     time.Time
	dismissed bool
	recovery  bool // the recovery page is open
	details   bool // its technical details are
	copied    time.Time
}

// connectionStuck reports whether the workspace has been connecting long
// enough to say so, asking for a frame when it will have been.
func (a *App) connectionStuck(c *ui.Context) bool {
	st := &a.stuck
	connecting := a.sync != nil && !a.syncState.Live && !a.localOnly && a.tokenNow() != ""
	if !connecting {
		// The recovery page closes on its own, as Lody's does once the
		// connection leaves loading.
		st.since, st.dismissed, st.recovery, st.details = time.Time{}, false, false, false
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
		a.recoveryPage(c, p)
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

// recoveryPage is Lody's RecoveryPage, which the banner's Recover opens
// over the whole window: what is happening, and the ways out — reload,
// clear the local cache, copy diagnostics — then the diagnostics behind a
// disclosure and, last and quiet, the way out of last resort. It has no
// close: it closes on its own once the connection leaves connecting.
func (a *App) recoveryPage(c *ui.Context, p *palette) {
	st := &a.stuck
	// It names the workspace when one is opening, as Lody's names the one
	// its route targets.
	title := locales.RecoverySlowTitleWorkspace()
	if a.workspaceID == "" {
		title = locales.RecoverySlowTitle()
	}
	ui.Overlay(c, func() {
		statusPage(c, p, "recovery", statusWindow, artBroken, true, title, locales.RecoveryDescription(), func() {
			statusActions(c, func() {
				lodyButton(c, p, "primary", "reload", nil, locales.RecoveryReload(), true).OnClick(a.reloadWorkspace)
				lodyButton(c, p, "secondary", "clear-cache", nil, locales.RecoveryClearCache(), true).OnClick(func() {
					a.confirm(locales.PrefClearCacheTitle(), locales.PrefClearCacheBody(), locales.PrefClearCacheButton(), func() {
						// A clear starts the wait over, as Lody's reloads
						// into it.
						a.stuck = stuckState{}
						a.clearCache()
					})
				})
				// "Copied" for 2s, as Lody's.
				label := locales.RecoveryCopyDiagnostics()
				if left := 2*time.Second - time.Since(st.copied); left > 0 {
					label = locales.RecoveryCopied()
					c.After(left)
				}
				lodyButton(c, p, "ghost", "copy-diagnostics", nil, label, true).OnClick(func() {
					writeClipboard(a.diagnostics())
					st.copied = time.Now()
				})
			})
			statusDetails(c, p, "diagnostics", locales.ErrorTechnicalDetails(), &st.details, func() {
				statusCode(c, p, "diagnostics", a.diagnostics(), true, nil)
			})
			a.stillStuck(c, p)
		})
	})
}

// stillStuck is the recovery's last line, in the footnote type and the
// tertiary ink, with the way out of last resort in it as a quiet link,
// semibold and underlined, secondary until hovered: clear everything this
// Mac keeps and sign out, asked first.
func (a *App) stillStuck(c *ui.Context, p *palette) {
	before, after, _ := strings.Cut(locales.RecoveryStillStuck("\x00"), "\x00")
	ui.RichText(c.Key("still-stuck")).FontSize(sizeFoot).LineHeight(16.0 / 12).TextColor(p.Tertiary).TextAlign(ui.Center).Children(func() {
		if before != "" {
			ui.Text(c, before)
		}
		link := ui.Text(c.Key("hard-reset"), locales.RecoveryClearAll()).FontWeight(500).Underline().Cursor(ui.CursorPointer).
			OnClick(func() {
				a.confirm(locales.HardResetTitle(), locales.HardResetBody(), locales.HardResetButton(), a.hardReset)
			})
		ink := p.Secondary
		if link.Hovered() {
			ink = p.Text
		}
		link.TextColor(ink)
		if after != "" {
			ui.Text(c, after)
		}
	})
}

// reloadWorkspace is the recovery's Reload: the workspace connects again
// from the start, and the wait with it, as Lody's page reloads.
func (a *App) reloadWorkspace() {
	a.stuck = stuckState{}
	if a.workspaceID != "" && !a.offline {
		a.connect(a.workspaceID)
	}
}

// hardReset is "Clear all local data and sign out": what Lody keeps on
// this Mac goes, its caches, preferences and sign-in with them, and it
// starts over at signing in.
func (a *App) hardReset() {
	a.stuck = stuckState{}
	a.images = map[string]*remoteImage{}
	a.mentionSources = nil
	a.fetches = map[string]*fetched{}
	a.signOut()
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
