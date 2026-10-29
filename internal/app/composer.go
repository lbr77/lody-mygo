package app

import (
	"github.com/lbr77/lody-mygo/locales"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/session"
)

// bottom is what sits under the conversation: the info bar, then the
// composer, or the permission card in its place while the agent asks.
func (a *App) bottom(c *ui.Context, p *palette, sess *catalog.Session, v *session.View) {
	if e, it, ok := pendingPermission(v); ok {
		bottomColumn(c, func() {
			if sess != nil {
				a.infoBar(c, p, sess)
			}
			a.permissionCard(c, p, e, it)
		})
		return
	}
	working := v != nil && v.Working
	ready := v != nil && v.Status == "live" && sess != nil
	hint := ""
	if sess != nil && !a.presence.IsOnline(sess.MachineID) {
		hint = a.machineName(sess.MachineID) + " is offline. Messages wait until it reconnects."
	}
	busyLabel := locales.Queue()
	if sess != nil && a.busyBehavior == "steer" && a.steerable(sess) {
		busyLabel = locales.Steer()
	}
	a.composerBox(c, p, composerOptions{
		hint:      hint,
		busyLabel: busyLabel,
		canSend:   ready && !a.sending,
		working:   working,
		onSend:    a.send,
		onStop:    a.stop,
		above: func() {
			if sess != nil {
				a.infoBar(c, p, sess)
			}
		},
		footer: func() {
			if sess == nil {
				return
			}
			current := a.models[sess.ID]
			if current == "" && v != nil {
				current = v.ModelID
			}
			if current == "" && sess.LastModel != nil {
				current = sess.LastModel.ModelID
			}
			a.runConfig(c, p, "model/"+sess.ID, sess.AgentConfigID, current, sess.LastModel, func(id string) { a.models[sess.ID] = id })
			mode := a.modes[sess.ID]
			if mode == "" && v != nil {
				mode = v.ModeID
			}
			a.modeButton(c, p, "mode/"+sess.ID, a.catalog.ModesFor(sess.AgentConfigID, sess.AgentType), mode, func(id string) { a.modes[sess.ID] = id })
		},
	})
}

// pendingPermission finds the request the agent waits on.
func pendingPermission(v *session.View) (session.Entry, session.Item, bool) {
	if v == nil {
		return session.Entry{}, session.Item{}, false
	}
	for i := len(v.Entries) - 1; i >= 0; i-- {
		e := v.Entries[i]
		if e.Role != "assistant" {
			continue
		}
		for _, it := range e.Items {
			if it.Permission != nil && it.Permission.Pending {
				return e, it, true
			}
		}
	}
	return session.Entry{}, session.Item{}, false
}

func bottomColumn(c *ui.Context, fn func()) {
	ui.Row(c).Shrink(0).FillWidth().Justify(ui.Center).Padding(0, 0, 8, 0).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).MaxWidth(columnWidth+2*columnGutter).Padding(0, columnGutter).Gap(8).Children(fn)
	})
}

// infoBar shows where the session runs: its machine, project, branch and
// changes.
func (a *App) infoBar(c *ui.Context, p *palette, s *catalog.Session) {
	bar := ui.Row(c).Height(32).Radius(8).Border(0.5, p.Text.Alpha(0.1)).Background(p.Composer).Padding(0, 8, 0, 4).Gap(6).AlignItems(ui.Center)
	if !p.Dark {
		bar.Shadow(0, 0.5, 1, 1, ui.RGBA(0, 0, 0, 0.04))
	}
	bar.Children(func() {
		ui.Row(c).Height(24).Padding(0, 6).Radius(6).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Icon(c, iconMonitor).Size(14, 14).TextColor(p.Secondary)
			ui.Text(c, a.machineLabel(s.MachineID)).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
			if !a.presence.IsOnline(s.MachineID) {
				offlinePill(c, p)
			}
		})
		project := a.catalog.Project(s.ProjectID)
		if project != nil || s.BranchName != "" {
			ui.Box(c).Size(1, 14).Background(p.SideMuted.Alpha(0.25)).Shrink(0)
		}
		if project != nil {
			icon := iconFolder
			if strings.HasPrefix(project.ID, "github:") {
				icon = iconBranch
			}
			ui.Icon(c, icon).Size(14, 14).TextColor(p.Secondary)
			ui.Text(c, project.Name).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine().MinWidth(0).Shrink(1)
		}
		if s.BranchName != "" {
			ui.Icon(c, iconBranch).Size(14, 14).TextColor(p.Secondary)
			ui.Text(c, s.BranchName).FontSize(sizeFoot).TextColor(p.Reading).SingleLine().MinWidth(0).Shrink(1)
		}
		ui.Spacer(c)
		if s.DiffAdd > 0 || s.DiffDel > 0 {
			ui.Row(c).Height(20).Padding(0, 6).Radius(5).Gap(4).AlignItems(ui.Center).Background(p.Text.Alpha(0.06)).Shrink(0).Children(func() {
				diffCounts(c, p, int(s.DiffAdd), int(s.DiffDel))
			})
		}
	})
}

type composerOptions struct {
	hint        string
	busyLabel   string // the send button's label while the agent works
	placeholder string
	canSend     bool
	working     bool
	sendSize    float32
	onSend      func()
	onStop      func()
	above       func() // between the hint and the box
	footer      func()
}

// composerBox is Lody's message box: a 16-DIP-round card with the text
// above its controls.
func (a *App) composerBox(c *ui.Context, p *palette, o composerOptions) {
	if o.placeholder == "" {
		o.placeholder = locales.ComposerPlaceholder()
	}
	if o.sendSize == 0 {
		o.sendSize = 28
	}
	bottomColumn(c, func() {
		if o.hint != "" {
			ui.Text(c, o.hint).FontSize(sizeFoot).TextColor(p.Secondary).Padding(0, 6)
		}
		if o.above != nil {
			o.above()
		}
		box := ui.Column(c).FillWidth().Radius(16).Border(1, p.ComposerBorder).Background(p.Composer).Padding(8, 10).Gap(4)
		if !p.Dark {
			box.Shadow(0, 10, 28, -14, ui.RGBA(0, 0, 0, 0.16))
		}
		if dropped := box.DroppedFiles(); len(dropped) > 0 {
			a.addFiles(dropped)
		}
		hasFiles := len(a.attachments) > 0
		var input ui.Element
		box.Children(func() {
			a.attachmentChips(c, p)
			ui.Box(c).FillWidth().Children(func() {
				empty := strings.TrimSpace(a.draft) == "" && !hasFiles
				input = ui.TextAreaBase(c, &a.draft).Lines(2, 10).Placeholder(o.placeholder).FontSize(sizeBody).LineHeight(24.0/14).
					TextColor(p.Reading).Padding(2, 4).FillWidth().AutoFocus().Label(locales.ComposerLabel()).Bind(&a.composerFocus).
					OnTextKey(func(k ui.TextKey) bool {
						if a.mentionKey(k) {
							return true
						}
						if k.Key == ui.KeyEnter && k.Mods&ui.Shift == 0 {
							if o.canSend && (strings.TrimSpace(k.Text()) != "" || len(a.attachments) > 0) {
								a.draft = k.Text()
								o.onSend()
								k.SetText(a.draft)
							}
							return true
						}
						return false
					})
				if empty && !input.Focused() {
					// The shortcut that focuses the box, while it waits.
					input.Padding(2, 64, 2, 4)
					ui.Text(c, "⌘L").FontSize(sizeCaption).TextColor(p.Secondary).Padding(1, 5).Radius(5).Border(1, p.Border).
						Opacity(0.5).Absolute().Top(2).Right(2)
				}
			})
			ui.Row(c).Gap(4).AlignItems(ui.Center).Padding(2, 0, 0, 0).Children(func() {
				iconButton(c, p, iconPlus, locales.AttachFiles(), p.Text.Alpha(0.06)).OnClick(a.pickFiles)
				if o.footer != nil {
					o.footer()
				}
				ui.Spacer(c)
				if a.sending {
					if a.uploading != "" {
						ui.Text(c, a.uploading).FontSize(sizeFoot).TextColor(p.Secondary).Shrink(0)
					} else {
						ui.Spinner(c).Size(14, 14)
					}
				}
				empty := strings.TrimSpace(a.draft) == "" && !hasFiles
				if o.working && empty && o.onStop != nil {
					sendDisc(c, p, nil, locales.Stop(), true, o.sendSize).OnClick(o.onStop)
					return
				}
				label := locales.Send()
				if o.working {
					label = o.busyLabel
				}
				sendDisc(c, p, iconArrowUp, label, o.canSend && !empty, o.sendSize).OnClick(func() {
					if o.canSend && (strings.TrimSpace(a.draft) != "" || len(a.attachments) > 0) {
						o.onSend()
					}
				})
			})
		})
		a.mentionPanel(c, p, box, input)
		if a.sendError != "" {
			ui.Text(c, a.sendError).FontSize(sizeFoot).TextColor(p.Danger).Padding(0, 6).Selectable()
		}
	})
}

// sendDisc is the round ink button; without an icon it is Stop's rounded
// square.
func sendDisc(c *ui.Context, p *palette, icon *ui.SVG, label string, enabled bool, size float32) ui.Element {
	b := ui.ButtonBase(c).Size(size, size).Radius(size / 2).Background(p.Text).Cursor(ui.CursorPointer).Label(label).Tooltip(label).Disabled(!enabled)
	if !p.Dark {
		b.Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.08))
	}
	if !enabled {
		b.Opacity(0.45)
	} else if b.Pressed() {
		b.Background(p.Text.Alpha(0.8))
	}
	b.Children(func() {
		if icon == nil {
			ui.Box(c).Size(10, 10).Radius(3).Background(p.Canvas)
			return
		}
		ui.Icon(c, icon).Size(16, 16).TextColor(p.Canvas)
	})
	return b
}

// runConfig is the composer's run-config button: the agent's mark and the
// model, which it chooses among the models the agent reports.
func (a *App) runConfig(c *ui.Context, p *palette, key, agentConfig, current string, last *catalog.Model, pick func(string)) {
	models := a.catalog.Capabilities[agentConfig].Models
	label := current
	for _, m := range models {
		if m.ModelID == current && m.Name != "" {
			label = m.Name
		}
	}
	if label == current && last != nil && last.ModelID == current && last.Name != "" {
		label = last.Name
	}
	if label == "" {
		label = locales.DefaultModel()
	}
	var options []option
	for _, m := range models {
		options = append(options, option{m.ModelID, firstNonEmpty(m.Name, m.ModelID)})
	}
	a.picker(c, p, key, iconBot, label, options, pick)
}

// agentName is an agent config's display name.
func (a *App) agentName(machine, id string) string {
	if ag := a.agent(machine, id); ag != nil {
		return ag.Name
	}
	return ""
}

type option struct{ id, label string }

// picker is a composer-footer button that opens a list to choose from;
// without options it is a plain label.
func (a *App) picker(c *ui.Context, p *palette, key string, icon *ui.SVG, label string, options []option, pick func(string)) {
	open := a.popover(key)
	b := ui.ButtonBase(c.Key(key)).Height(28).Padding(0, 8).Radius(8).Gap(6).Label(label)
	if len(options) > 0 {
		b.Cursor(ui.CursorPointer).OnClick(func() { *open = !*open })
		if b.Hovered() || *open {
			b.Background(p.Text.Alpha(0.06))
		}
	}
	ink := p.Secondary
	if b.Hovered() && len(options) > 0 {
		ink = p.Text
	}
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).Size(16, 16).TextColor(ink)
		}
		ui.Text(c, label).FontSize(sizeSub).FontWeight(550).TextColor(ink).SingleLine().MaxWidth(220)
	})
	a.optionList(c, p, b, open, options, "", pick)
}

// modeFace is how a permission mode shows on its button, as Lody classifies
// modes: an icon by what it allows, amber for those that skip approval,
// locales.ModeAuto() for those a reviewing model approves.
func modeFace(id string) (icon *ui.SVG, warning, auto bool) {
	switch id {
	case "read-only":
		return iconEye, false, false
	case "agent-auto-review", "auto":
		return nil, false, true
	case "agent-full-access", "danger-full-access", "bypassPermissions", "yolo", "always-approve":
		return iconShieldAlert, true, false
	case "acceptEdits":
		return iconPenLine, false, false
	case "plan":
		return iconCompass, false, false
	case "dontAsk":
		return iconShieldOff, true, false
	}
	return iconShield, false, false
}

// modeButton picks the agent's permission mode.
func (a *App) modeButton(c *ui.Context, p *palette, key string, modes []catalog.Mode, current string, pick func(string)) {
	if len(modes) == 0 {
		return
	}
	name := locales.ModeDefault()
	for _, m := range modes {
		if m.ID == current {
			name = m.Name
		}
	}
	icon, warning, auto := modeFace(current)
	open := a.popover(key)
	b := ui.ButtonBase(c.Key(key)).Height(28).MinWidth(28).Padding(0, 6).Radius(8).Cursor(ui.CursorPointer).
		Label(locales.PermissionMode(name)).Tooltip(name).OnClick(func() { *open = !*open })
	if b.Hovered() || *open {
		b.Background(p.Text.Alpha(0.06))
	}
	ink := p.Secondary
	if warning {
		ink = p.Warning
	}
	b.Children(func() {
		if auto {
			ui.Text(c, locales.ModeAuto()).FontSize(sizeCaption).FontWeight(500).TextColor(ink)
			return
		}
		ui.Icon(c, icon).Size(16, 16).TextColor(ink)
	})
	var options []option
	for _, m := range modes {
		options = append(options, option{m.ID, m.Name})
	}
	a.optionList(c, p, b, open, options, current, pick)
}

// popover returns the open state of a popover, made on first use.
func (a *App) popover(key string) *bool {
	if a.pickers == nil {
		a.pickers = map[string]*bool{}
	}
	open := a.pickers[key]
	if open == nil {
		open = new(bool)
		a.pickers[key] = open
	}
	return open
}

func (a *App) optionList(c *ui.Context, p *palette, anchor ui.Element, open *bool, options []option, selected string, pick func(string)) {
	ui.Popover(c, anchor, open, func() {
		// Long lists, as an agent's models, scroll within the popover.
		ui.Scroll(c).MinWidth(200).MaxHeight(320).Gap(1).Children(func() {
			for _, o := range options {
				row := menuRow(c.Key(o.id), p, o.label)
				if row.Clicked() {
					pick(o.id)
					*open = false
				}
				row.Children(func() {
					ui.Text(c, o.label).FontSize(sizeSub).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
					if selected != "" && o.id == selected {
						ui.Icon(c, iconCheck).Size(14, 14).TextColor(p.Text)
					}
				})
			}
		})
	})
}

// permissionCard asks for a permission in the composer's place, as Lody's
// floating request does: a question, what it is about, and the choices,
// refusals first and the suggested one in ink.
func (a *App) permissionCard(c *ui.Context, p *palette, e session.Entry, it session.Item) {
	question := locales.PermissionAction()
	switch {
	case it.Permission.Question:
		question = firstNonEmpty(it.Title, locales.PermissionQuestion())
	case it.Kind == "execute":
		question = locales.PermissionRunCommand()
	case it.Kind == "edit" || it.Kind == "delete" || it.Kind == "move":
		question = locales.PermissionEdit()
	}
	var allow, reject []session.Option
	for _, o := range it.Permission.Options {
		if strings.HasPrefix(o.Kind, "reject") {
			reject = append(reject, o)
		} else {
			allow = append(allow, o)
		}
	}
	card := ui.Column(c).FillWidth().Padding(12).Gap(8).Radius(14).Border(1, p.ComposerBorder).Background(p.Composer)
	if !p.Dark {
		card.Shadow(0, 10, 28, -14, ui.RGBA(0, 0, 0, 0.16))
	}
	card.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
			ui.Text(c, question).FontSize(sizeBody).LineHeight(20.0 / 14).FontWeight(600).TextColor(p.Strong).Grow(1).MinWidth(0)
			stop := ui.ButtonBase(c).Height(24).Padding(0, 6).Radius(6).Gap(4).Cursor(ui.CursorPointer).Label(locales.StopAgent()).OnClick(a.stop)
			if stop.Hovered() {
				stop.Background(p.Text.Alpha(0.06))
			}
			stop.Children(func() {
				ui.Icon(c, iconCircleStop).Size(14, 14).TextColor(p.Secondary)
				ui.Text(c, locales.StopAgent()).FontSize(sizeFoot).TextColor(p.Secondary)
			})
		})
		if !it.Permission.Question && it.Title != "" {
			ui.Text(c, it.Title).Font(fontMono).FontSize(sizeFoot).LineHeight(1.5).TextColor(p.Text).Padding(8, 12).Radius(10).
				Background(p.Text.Alpha(0.04)).Selectable().MaxLines(6)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Padding(4, 0, 0, 0).Wrap().Children(func() {
			if it.Permission.Question {
				for i, o := range it.Permission.Options {
					opt := o
					a.choiceButton(c.Key(o.ID), p, opt.Name, i == 0, nil, func() { a.answer(e.ID, it, opt) })
				}
				return
			}
			if len(reject) > 0 {
				first := reject[0]
				a.choiceButton(c.Key("reject"), p, first.Name, false, reject[1:], func() { a.answer(e.ID, it, first) }, func(o session.Option) { a.answer(e.ID, it, o) })
			}
			if len(allow) > 0 {
				first := allow[0]
				a.choiceButton(c.Key("allow"), p, first.Name, true, allow[1:], func() { a.answer(e.ID, it, first) }, func(o session.Option) { a.answer(e.ID, it, o) })
			}
		})
	})
}

// choiceButton is a 28px button, in ink when suggested, with a chevron
// opening the other options of its kind.
func (a *App) choiceButton(c *ui.Context, p *palette, label string, suggested bool, more []session.Option, choose func(), chooseMore ...func(session.Option)) {
	fill, ink, border := p.Composer, p.Text, p.Border
	if suggested {
		fill, ink, border = p.Text, p.Canvas, p.Text
	}
	ui.Row(c).Height(28).Radius(8).Border(1, border).Background(fill).AlignItems(ui.Stretch).Clip().Children(func() {
		main := ui.ButtonBase(c).Padding(0, 12).Cursor(ui.CursorPointer).Label(label).OnClick(choose)
		if main.Hovered() {
			main.Background(ink.Alpha(0.08))
		}
		main.Children(func() { ui.Text(c, label).FontSize(sizeSub).FontWeight(500).TextColor(ink).SingleLine().MaxWidth(240) })
		if len(more) == 0 || len(chooseMore) == 0 {
			return
		}
		ui.Box(c).Width(1).Background(ink.Alpha(0.2))
		chevron := ui.ButtonBase(c).Width(24).Cursor(ui.CursorPointer).Label(locales.MoreOptions())
		if chevron.Hovered() {
			chevron.Background(ink.Alpha(0.08))
		}
		chevron.Menu(func(m *ui.Menu) {
			for _, o := range more {
				if m.Item(o.Name).Chosen() {
					chooseMore[0](o)
				}
			}
		})
		chevron.Children(func() { ui.Icon(c, iconChevronDown).Size(14, 14).TextColor(ink) })
	})
}
