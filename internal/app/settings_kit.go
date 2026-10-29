package app

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/locales"
)

// Pieces the settings pages share: machine tabs, form fields, dialogs,
// a confirmation, and writes to the workspace's and machines' streams.

// machineTabs are a page's machines as line tabs, each with its status;
// they keep *selected one of them.
func (a *App) machineTabs(c *ui.Context, p *palette, selected *string) string {
	ids := a.catalog.MachineIDs
	if len(ids) == 0 {
		return ""
	}
	valid := false
	for _, id := range ids {
		valid = valid || id == *selected
	}
	if !valid {
		*selected = ids[0]
	}
	var tabs []lineTab
	for _, id := range ids {
		machine := id
		tabs = append(tabs, lineTab{id: id, label: a.machineName(id), leading: func() {
			dot := p.Tertiary
			if a.presence.IsOnline(machine) {
				dot = p.Success
			}
			ui.Box(c).Size(6, 6).Radius(3).Background(dot)
		}})
	}
	lineTabs(c, p, "line-tabs", tabs, *selected, true, func(id string) { *selected = id })
	return *selected
}

// pageHeader is a page's title and lead, with its action on the right.
func pageHeader(c *ui.Context, p *palette, title, lead string, actions func()) {
	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(12).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, title).FontSize(sizeTitle).FontWeight(600).TextColor(p.Strong)
			if lead != "" {
				ui.Text(c, lead).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Secondary)
			}
		})
		if actions != nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(actions)
		}
	})
}

// outlineButton is a bordered button with an icon, as Lody's page actions.
func outlineButton(c *ui.Context, p *palette, icon *ui.SVG, label string) ui.Element {
	b := ui.ButtonBase(c.Key(label)).Height(30).Padding(0, 10).Radius(8).Gap(6).AlignItems(ui.Center).Border(1, p.Border).
		Background(p.Canvas).Cursor(ui.CursorPointer).Label(label).Transition(colorFade)
	pressable(b, 0.97)
	if b.Hovered() {
		b.Background(p.Hover)
	}
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).Size(14, 14).TextColor(p.Text)
		}
		ui.Text(c, label).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
	})
	return b
}

// secondaryButton is Lody's small secondary button: the ghost button's
// label, bordered. Disabled, it dims and neither fills nor points; its
// caller leaves its click unset.
func secondaryButton(c *ui.Context, p *palette, label string, disabled bool) ui.Element {
	b := ui.ButtonBase(c.Key(label)).Height(28).Radius(8).Padding(0, 10).Border(1, p.Border).Label(label).Disabled(disabled).Transition(colorFade)
	if disabled {
		b.Opacity(0.5)
	} else {
		b.Cursor(ui.CursorPointer)
		if b.Hovered() {
			b.Background(p.Text.Alpha(0.06))
		}
	}
	b.Children(func() { ui.Text(c, label).FontSize(sizeSub).FontWeight(500).TextColor(p.Secondary) })
	return b
}

// card is a bordered list whose rows are divided.
func card(c *ui.Context, p *palette, fn func()) {
	ui.Column(c).FillWidth().Radius(10).Border(1, p.Border).Background(p.Canvas).Clip().Dividers(1, p.Border).Children(fn)
}

// emptyCard is a card that says there is nothing yet.
func emptyCard(c *ui.Context, p *palette, text string) {
	ui.Text(c, text).FontSize(sizeFoot).TextColor(p.Secondary).FillWidth().Padding(12, 16).Radius(10).Border(1, p.Border)
}

// field is a form field: its label over its control, a hint under it.
func field(c *ui.Context, p *palette, label, hint string, control func()) {
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		if label != "" {
			ui.Text(c, label).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
		}
		control()
		if hint != "" {
			ui.Text(c, hint).FontSize(sizeCaption).LineHeight(1.45).TextColor(p.Secondary)
		}
	})
}

// input is a filled text field, as Lody's forms have.
func input(c *ui.Context, p *palette, value *string, placeholder, label string) ui.Element {
	return ui.TextInput(c, value).Placeholder(placeholder).Label(label).FontSize(sizeSub).FillWidth().Height(34).
		Padding(0, 10).Radius(8).Background(p.Text.Alpha(0.05))
}

// area is a filled multi-line field.
func area(c *ui.Context, p *palette, value *string, placeholder, label string, lines int) ui.Element {
	return ui.TextArea(c, value).Placeholder(placeholder).Label(label).FontSize(sizeSub).FillWidth().Lines(lines, lines+6).
		Padding(8, 10).Radius(8).Background(p.Text.Alpha(0.05))
}

// sheet is a dialog over the settings, as Lody's editors.
func sheet(c *ui.Context, p *palette, open *bool, width float32, fn func()) {
	w, h := c.Size()
	ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		sheetIn(backdrop, panel)
		backdrop.Background(ui.RGBA(0, 0, 0, 0.22))
		panel.Width(min(width, w-80)).MaxHeight(h-80).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Clip().
			Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Column(c).FillWidth().MaxHeight(h - 80).Children(fn)
	})
}

// lodyDialog is @lody/ui's Dialog on the modal rung: a 512px panel over
// the veil, padded 16 with 16 between its parts, its cross in the corner,
// rising into place as sheetIn raises it. fn builds the panel's parts.
func lodyDialog(c *ui.Context, p *palette, open *bool, label string, fn func()) {
	w, h := c.Size()
	ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		sheetIn(backdrop, panel)
		if p.Dark {
			backdrop.Background(ui.RGBA(0, 0, 0, 0.6))
			panel.Background(p.Ground).InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.06)).
				Shadow(0, 0, 0, 0.5, ui.RGBA(255, 255, 255, 0.08)).Shadow(0, 24, 56, -8, ui.RGBA(0, 0, 0, 0.7))
		} else {
			backdrop.Background(ui.RGBA(28, 28, 28, 0.5))
			panel.Background(p.Popover).Shadow(0, 0, 0, 0.5, ui.RGBA(28, 28, 28, 0.08)).
				Shadow(0, 2, 6, -2, ui.RGBA(28, 28, 28, 0.08)).Shadow(0, 24, 56, -12, ui.RGBA(28, 28, 28, 0.28))
		}
		panel.Width(min(512, w-64)).MaxHeight(h - 64).Padding(16).Gap(16).Radius(14).Label(label)
		fn()
		if lodyButton(c, p, "ghost", "dialog-close", iconX, "", true).Absolute().Top(16).Right(16).Label(locales.Close()).Clicked() {
			*open = false
		}
	})
}

// lodyDialogHeader is a Dialog's title, clear of its cross, over the
// sentence about it.
func lodyDialogHeader(c *ui.Context, p *palette, title, description string, mark *ui.SVG) {
	ui.Column(c.Key("dialog-head")).FillWidth().Gap(6).Shrink(0).Padding(0, 36, 0, 0).Children(func() {
		heading := func() {
			ui.Text(c, title).FontSize(16).LineHeight(24.0 / 16).FontWeight(600).TextColor(p.Text).MinWidth(0)
		}
		if mark == nil {
			heading()
		} else {
			// A mark leads the title, as Magpie's does.
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, mark).Size(20, 20).TextColor(p.Text).Shrink(0)
				heading()
			})
		}
		if description != "" {
			ui.Text(c, description).FontSize(sizeBody).LineHeight(20.0 / 14).TextColor(p.Secondary).MinWidth(0)
		}
	})
}

// sheetHeader is a dialog's title and lead, with its close button.
func sheetHeader(c *ui.Context, p *palette, title, lead string, close func()) {
	ui.Row(c).FillWidth().Padding(18, 14, 8, 20).Gap(12).AlignItems(ui.Start).Shrink(0).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, title).FontSize(16).FontWeight(600).TextColor(p.Strong)
			if lead != "" {
				ui.Text(c, lead).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Secondary)
			}
		})
		iconButton(c, p, iconX, locales.Close(), p.Hover).OnClick(close)
	})
}

// primaryButton is the ink button of a dialog's footer.
func primaryButton(c *ui.Context, p *palette, label string, enabled bool) ui.Element {
	b := ui.ButtonBase(c.Key(label)).Height(30).Padding(0, 14).Radius(8).Background(p.Text).Cursor(ui.CursorPointer).Label(label).Disabled(!enabled)
	pressable(b, 0.97)
	if !enabled {
		b.Opacity(0.45)
	}
	b.Children(func() { ui.Text(c, label).FontSize(sizeSub).FontWeight(500).TextColor(p.Canvas) })
	return b
}

// confirmation is a question a destructive action asks first.
type confirmation struct {
	open        bool
	title, body string
	action      string
	cancel      string // the cancel button's label; Cancel when empty
	run         func()
	notice      bool // only tells: one button, in ink, no cancel
	safe        bool // not destructive: the action in ink, with a cancel
}

func (a *App) confirm(title, body, action string, run func()) {
	a.confirming = confirmation{open: true, title: title, body: body, action: action, run: run}
}

// confirmSafe asks before something that isn't destructive.
func (a *App) confirmSafe(title, body, action string, run func()) {
	a.confirming = confirmation{open: true, title: title, body: body, action: action, run: run, safe: true}
}

func (a *App) confirmDialog(c *ui.Context, p *palette) {
	cf := &a.confirming
	if !cf.open {
		return
	}
	ui.DialogBase(c, &cf.open, func(backdrop, panel ui.Element) {
		sheetIn(backdrop, panel)
		backdrop.Background(ui.RGBA(0, 0, 0, 0.22))
		panel.Width(400).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Padding(20).Gap(12).Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Text(c, cf.title).FontSize(16).FontWeight(600).TextColor(p.Strong)
		if cf.body != "" {
			ui.Text(c, cf.body).FontSize(sizeSub).LineHeight(1.45).TextColor(p.Secondary)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Padding(8, 0, 0, 0).Children(func() {
			if !cf.notice {
				ghostButton(c, p, firstNonEmpty(cf.cancel, locales.Cancel())).Border(1, p.Border).OnClick(func() { cf.open = false })
			}
			fill := p.Danger
			if cf.notice || cf.safe {
				fill = p.Text
			}
			b := ui.ButtonBase(c).Height(28).Padding(0, 12).Radius(8).Background(fill).Cursor(ui.CursorPointer).Label(cf.action)
			pressable(b, 0.97)
			run := cf.run
			b.OnClick(func() {
				cf.open = false
				run()
			})
			b.Children(func() {
				ink := ui.RGB(255, 255, 255)
				if cf.notice || cf.safe {
					ink = p.Canvas
				}
				ui.Text(c, cf.action).FontSize(sizeSub).FontWeight(500).TextColor(ink)
			})
		})
	})
}

// streamWrite writes to the workspace stream, or a machine's, in the
// background; a failure shows on the page.
func (a *App) streamWrite(machine string, edit func(f *flock.Flock) error, done func(err error)) {
	sync := a.sync
	if sync == nil {
		return
	}
	a.settingsError = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var err error
		if machine == "" {
			err = sync.WriteWorkspace(ctx, edit)
		} else {
			err = sync.WriteMachine(ctx, machine, edit)
		}
		a.update(func() {
			if err != nil {
				a.settingsError = err.Error()
			}
			if done != nil {
				done(err)
			}
		})
	}()
}

// nowMs is the time as Lody's rows stamp it.
func nowMs() float64 { return float64(time.Now().UnixMilli()) }
