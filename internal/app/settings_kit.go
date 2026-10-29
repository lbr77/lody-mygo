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
	row := ui.Row(c.Key("line-tabs")).FillWidth().Gap(20).BorderWidth(0, 0, 1, 0).BorderColor(p.Text.Alpha(0.08))
	row.Children(func() {
		var sel ui.Rect
		for _, id := range ids {
			machine := id
			active := id == *selected
			tab := ui.ButtonBase(c.Key(id)).Height(36).Gap(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(a.machineName(id)).
				OnClick(func() { *selected = machine })
			if active {
				sel = tab.Bounds()
			}
			ink := tab.AnimateColor("ink", map[bool]ui.Color{true: p.Text, false: p.Secondary}[active || tab.Hovered()], ui.Animation{Duration: 150 * time.Millisecond, Ease: easeStandard})
			tab.Children(func() {
				dot := p.Tertiary
				if a.presence.IsOnline(machine) {
					dot = p.Success
				}
				ui.Box(c).Size(6, 6).Radius(3).Background(dot)
				ui.Text(c, a.machineName(machine)).FontSize(13.5).FontWeight(500).TextColor(ink).SingleLine()
			})
		}
		// The current tab's line travels to the next rather than blinking:
		// 240ms, on Lody's curve.
		if origin := row.Bounds(); sel.W > 0 {
			line := ui.Box(c.Key("line")).Absolute().Bottom(-1).Height(2).Radius(1).Background(p.Text)
			x := line.AnimateWith("x", sel.X-origin.X, 240*time.Millisecond, easeLineTab)
			w := line.AnimateWith("w", sel.W, 240*time.Millisecond, easeLineTab)
			line.Left(x).Width(w)
		}
	})
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
	run         func()
}

func (a *App) confirm(title, body, action string, run func()) {
	a.confirming = confirmation{open: true, title: title, body: body, action: action, run: run}
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
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { cf.open = false })
			b := ui.ButtonBase(c).Height(28).Padding(0, 12).Radius(8).Background(p.Danger).Cursor(ui.CursorPointer).Label(cf.action)
			pressable(b, 0.97)
			run := cf.run
			b.OnClick(func() {
				cf.open = false
				run()
			})
			b.Children(func() {
				ui.Text(c, cf.action).FontSize(sizeSub).FontWeight(500).TextColor(ui.RGB(255, 255, 255))
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
