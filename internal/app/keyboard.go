package app

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// Keyboard settings, as Lody's: every command under its category with the
// chord that runs it. A click records a new chord, Escape cancels it, a
// chord another command holds is refused, and a hovered row can unbind.

type keyRecording struct {
	id       string
	rejected string // the chord refused, and the command holding it
	holder   string
}

// keyHolder is the command, other than id, a chord runs.
func keyHolder(cmds []command, spec, id string) *command {
	for i := range cmds {
		if cmds[i].id == id {
			continue
		}
		for _, ch := range cmds[i].keys {
			if strings.EqualFold(ch.spec, spec) {
				return &cmds[i]
			}
		}
	}
	return nil
}

// recordKey takes a key pressed while a chord records.
func (a *App) recordKey(ev ui.InputEvent) bool {
	rec := &a.recording
	if ev.Kind != ui.InputKeyDown || rec.id == "" {
		return false
	}
	if ev.Key == ui.KeyEscape && ev.Mods == 0 {
		a.recording = keyRecording{}
		return true
	}
	name := chordName(ev.Key)
	fn := ev.Key >= ui.KeyF1 && ev.Key <= ui.KeyF12
	if name == "" || ev.Mods&(ui.Cmd|ui.Ctrl|ui.Alt) == 0 && !fn {
		// A key without Command, Control or Option would take typing.
		return true
	}
	spec := chordSpec(ev.Mods, ev.Key)
	if holder := keyHolder(a.commands(), spec, rec.id); holder != nil {
		rec.rejected, rec.holder = spec, holder.title
		return true
	}
	a.setKeys(rec.id, []string{spec})
	a.recording = keyRecording{}
	return true
}

func (a *App) setKeys(id string, specs []string) {
	if a.keyOverrides == nil {
		a.keyOverrides = map[string][]string{}
	}
	a.keyOverrides[id] = specs
	a.savePrefs()
}

func (a *App) keyboardPage(c *ui.Context, p *palette) {
	pageHeader(c, p, locales.SettingsKeyboard(), locales.KeyLead(), func() {
		b := outlineButton(c, p, nil, locales.KeyResetAll()).Disabled(len(a.keyOverrides) == 0)
		if len(a.keyOverrides) == 0 {
			b.Opacity(0.5)
		}
		b.OnClick(func() {
			a.keyOverrides, a.recording = nil, keyRecording{}
			a.savePrefs()
		})
	})
	cmds := a.commands()
	for cat := catNavigation; cat <= catOther; cat++ {
		var rows []command
		for _, cmd := range cmds {
			if cmd.category == cat {
				rows = append(rows, cmd)
			}
		}
		if len(rows) == 0 {
			continue
		}
		settingsHeading(c, p, cat.label())
		card(c, p, func() {
			for _, cmd := range rows {
				a.keyRow(c.Key(cmd.id), p, cmds, cmd)
			}
		})
	}
}

// keyCaps draws a chord as its keys.
func keyCaps(c *ui.Context, p *palette, ch chord) {
	ui.Row(c).Gap(4).Children(func() {
		for _, key := range ch.parts() {
			ui.Text(c, key).FontSize(sizeFoot).TextColor(p.Secondary).Padding(2, 7).Radius(5).Border(1, p.Border).Background(p.Canvas)
		}
	})
}

func (a *App) keyRow(c *ui.Context, p *palette, cmds []command, cmd command) {
	rec := &a.recording
	recording := rec.id == cmd.id
	_, overridden := a.keyOverrides[cmd.id]
	row := ui.Row(c).FillWidth().MinHeight(40).Padding(6, 8, 6, 14).Gap(12).AlignItems(ui.Center)
	hovered := row.Hovered()
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, cmd.title).FontSize(sizeSub).TextColor(p.Text)
			// By urgency: a chord just refused, one shared with another
			// command, the default when rebound.
			var holder *command
			if len(cmd.keys) > 0 {
				holder = keyHolder(cmds, cmd.keys[0].spec, cmd.id)
			}
			switch {
			case recording && rec.rejected != "":
				ui.Text(c, locales.KeyAlreadyUsed(strings.Join(keyChord(rec.rejected).parts(), ""), rec.holder)).FontSize(sizeCaption).TextColor(p.Danger)
			case holder != nil:
				ui.Text(c, locales.KeyConflict(holder.title)).FontSize(sizeCaption).TextColor(p.Danger)
			case overridden && len(cmd.defaults) > 0 && (len(cmd.keys) == 0 || cmd.keys[0].spec != cmd.defaults[0].spec):
				ui.Text(c, locales.KeyDefault(strings.Join(cmd.defaults[0].parts(), ""))).FontSize(sizeCaption).TextColor(p.Secondary)
			case overridden && len(cmd.defaults) == 0 && len(cmd.keys) > 0:
				ui.Text(c, locales.KeyNoDefault()).FontSize(sizeCaption).TextColor(p.Secondary)
			}
		})
		// Fixed slots, so the caps and the trash line up down the card.
		ui.Row(c).Width(144).Shrink(0).Justify(ui.End).Children(func() {
			b := ui.ButtonBase(c.Key("keys")).Padding(3).Radius(7).Cursor(ui.CursorPointer).Transition(colorFade)
			if b.Hovered() || recording {
				b.Background(p.Text.Alpha(0.05))
			}
			if recording {
				b.Label(locales.KeyRecordingCancel()).Tooltip(locales.KeyRecordingCancel()).Focus().HandleInput(a.recordKey).
					OnClick(func() { a.recording = keyRecording{} })
				b.Children(func() {
					// The prompt breathes while it waits, as Lody's.
					t := ui.Text(c, locales.KeyRecording()).FontSize(sizeFoot).TextColor(p.Secondary).Padding(2, 7)
					k := t.Keyframes("pulse", []ui.Keyframe{{Value: 1}, {At: 600 * time.Millisecond, Value: 0.4}, {At: 1200 * time.Millisecond, Value: 1}}, ui.KeyframeOptions{Repeat: ui.RepeatForever})
					t.Opacity(k)
				})
				return
			}
			b.Label(locales.KeyEdit()).Tooltip(locales.KeyEdit()).OnClick(func() { a.recording = keyRecording{id: cmd.id} })
			b.Children(func() {
				if len(cmd.keys) == 0 {
					ui.Text(c, locales.KeyUnbound()).FontSize(sizeFoot).TextColor(p.Tertiary).Padding(2, 7)
					return
				}
				keyCaps(c, p, cmd.keys[0])
			})
		})
		ui.Box(c).Size(28, 28).Shrink(0).Center().Children(func() {
			if recording || len(cmd.keys) == 0 || !hovered {
				return
			}
			iconButton(c, p, iconTrash, locales.KeyUnbind(), p.Hover).Size(26, 26).OnClick(func() { a.setKeys(cmd.id, []string{}) })
		})
	})
}
