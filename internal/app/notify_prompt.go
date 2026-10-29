package app

import (
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// After a reply finishes, Lody asks above the composer whether to turn
// notifications on, once per chat, a second later, unless they are on or
// the reader said not to remind them again. Here they are Lody Native's
// desktop notifications, which Preferences turns on.

// notifyAsk is the notifications prompt.
type notifyAsk struct {
	never   bool   // Don't remind me
	shown   string // the chat it showed for, until a new turn there
	visible bool
	since   time.Time // when the chat finished, for the second's wait
	pending string    // the chat that finished
}

// notifyPrompt shows the prompt over the composer when its time comes.
func (a *App) notifyPrompt(c *ui.Context, p *palette, sess *catalog.Session, v *session.View) {
	np := &a.notifyAsk
	if sess == nil || a.account == nil || np.never || a.notifications {
		np.visible = false
		return
	}
	completed := sess.Status == "idle" && v != nil && !v.Working
	switch {
	case !completed:
		// A new turn: the next completion may ask again.
		if np.shown == sess.ID {
			np.shown = ""
		}
		np.pending, np.since = "", time.Time{}
	case np.shown != sess.ID && !np.visible:
		if np.pending != sess.ID {
			np.pending, np.since = sess.ID, time.Now()
		}
		if left := time.Second - time.Since(np.since); left > 0 {
			c.After(left)
		} else {
			np.visible, np.shown, np.pending = true, sess.ID, ""
		}
	}
	if !np.visible || np.shown != sess.ID {
		return
	}
	// The composer's own surface: it asks a question, it reports nothing.
	card := ui.Row(c.Key("notify-ask")).FillWidth().Padding(12, 12, 12, 16).Gap(12).AlignItems(ui.Start).Radius(12).
		Border(0.5, p.Text.Alpha(0.10)).Background(p.Composer).Margin(0, 0, 8, 0).
		Transition(ui.ElementTransition{Colors: true, Duration: 300 * time.Millisecond, Ease: ui.CubicBezier(0.25, 0.1, 0.25, 1), Enter: &ui.Motion{Y: 8}})
	if !p.Dark {
		card.Shadow(0, 10, 28, -14, ui.RGBA(0, 0, 0, 0.16))
	}
	card.Children(func() {
		ui.Box(c).Size(24, 24).Radius(12).Center().Background(p.Text.Alpha(0.06)).Shrink(0).Children(func() {
			ui.Icon(c, iconBell).Size(14, 14).TextColor(p.Secondary)
		})
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, locales.NotifyPromptTitle()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
			ui.Text(c, locales.NotifyPromptDescription()).FontSize(sizeCaption).TextColor(p.Secondary).Margin(2, 0, 0, 0)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Margin(8, 0, 0, 0).Children(func() {
				enable := ui.ButtonBase(c.Key("enable")).Height(28).Padding(0, 12).Radius(8).Background(p.Text).Cursor(ui.CursorPointer).
					Label(locales.NotifyPromptEnable()).OnClick(func() {
					np.visible = false
					a.settingsOpen, a.settingsTab = true, "preferences"
				})
				pressable(enable, 0.97)
				enable.Children(func() {
					ui.Text(c, locales.NotifyPromptEnable()).FontSize(sizeFoot).FontWeight(500).TextColor(p.Canvas)
				})
				ghostButton(c, p, locales.NotifyPromptDontRemind()).OnClick(func() {
					np.visible, np.never = false, true
					a.savePrefs()
				})
			})
		})
		iconButton(c.Key("close"), p, iconX, locales.Close(), p.Hover).Size(24, 24).OnClick(func() { np.visible = false })
	})
}
