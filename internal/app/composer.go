package app

import (
	"github.com/lbr77/lody-mygo/locales"
	"strings"
	"time"

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
	if sess != nil {
		if reqs := a.pendingShareRequests(c, sess.ID); len(reqs) > 0 {
			bottomColumn(c, func() { a.shareRequestCards(c, p, sess.ID, reqs) })
		}
	}
	working := v != nil && v.Working
	ready := v != nil && v.Status == "live" && sess != nil
	quota, locked := a.quota(v)
	if locked && sess != nil {
		// The Free quota closed the composer: say so in its place, and
		// once in a dialog.
		a.quotaLockedNotice(sess.ID)
		bottomColumn(c, func() {
			a.infoBar(c, p, sess)
			ui.Text(c, quota).FontSize(13).TextColor(p.Secondary).Padding(10, 16)
		})
		return
	}
	hint := ""
	if sess != nil && !a.presence.IsOnline(sess.MachineID) {
		hint = a.machineName(sess.MachineID) + " is offline. Messages wait until it reconnects."
	}
	busyLabel := locales.Queue()
	if sess != nil && a.busyBehavior == "steer" && a.steerable(sess) {
		busyLabel = locales.Steer()
	}
	scheduled, onScheduled := a.scheduledAction(sess)
	a.composerBox(c, p, composerOptions{
		sessionID:   a.selected,
		scheduled:   scheduled,
		onScheduled: onScheduled,
		expandable:  true,
		quota:       quota,
		hint:        hint,
		busyLabel:   busyLabel,
		canSend:     ready && !a.sending,
		working:     working,
		onSend:      a.send,
		onStop:      a.stop,
		above: func() {
			a.notifyPrompt(c, p, sess, v)
			a.quickReplyRow(c, p, sess, v)
			if sess != nil {
				// The message queue sits right on the bar, as one piece.
				ui.Column(c.Key("bar-stack")).FillWidth().Children(func() {
					a.queueSheet(c, p, sess, v)
					a.infoBar(c, p, sess)
				})
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
			pickModel := func(id string) { a.models[sess.ID] = id }
			a.runConfig(c, p, "model/"+sess.ID, sess.AgentConfigID, current, sess.LastModel, pickModel)
			var last map[string]any
			if v != nil {
				last = v.ConfigValues
			}
			a.runOptions(c, p, sess.ID, sess.AgentConfigID, current, last)
			mode := a.modes[sess.ID]
			if mode == "" && v != nil {
				mode = v.ModeID
			}
			modes := a.catalog.ModesFor(sess.AgentConfigID, sess.AgentType)
			pickMode := func(id string) { a.modes[sess.ID] = id }
			a.modeButton(c, p, "mode/"+sess.ID, modes, mode, pickMode)
			a.surfNext.model = a.modelCycle(sess.AgentConfigID, current, pickModel)
			a.surfNext.mode = modeCycle(modes, mode, pickMode)
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
	bottomColumnFull(c, false, fn)
}

// bottomColumnFull is the bottom column, taking the room the conversation
// leaves when full.
func bottomColumnFull(c *ui.Context, full bool, fn func()) {
	row := ui.Row(c).Shrink(0).FillWidth().Justify(ui.Center).Padding(0, 0, 8, 0)
	col := ui.Column(c)
	if full {
		row.Grow(1).MinHeight(0).AlignItems(ui.Stretch).Padding(8, 0, 8, 0)
	}
	row.Children(func() {
		col = ui.Column(c).Grow(1).MinWidth(0).MaxWidth(columnWidth+2*columnGutter).Padding(0, columnGutter).Gap(8)
		if full {
			col.MinHeight(0)
		}
		col.Children(fn)
	})
}

// infoBar shows where the session runs: its machine, project, branch and
// changes. In a project of several, its Repositories follow the machine,
// as Lody's cluster has them, and the project, branch and changes are
// the member chosen there's. Its GitHub actions end it.
func (a *App) infoBar(c *ui.Context, p *palette, s *catalog.Session) {
	bar := ui.Row(c).Height(32).Radius(8).Border(0.5, p.Text.Alpha(0.1)).Background(p.Composer).Padding(0, 8, 0, 4).Gap(6).AlignItems(ui.Center)
	if !p.Dark {
		bar.Shadow(0, 0.5, 1, 1, ui.RGBA(0, 0, 0, 0.04))
	}
	ctx := a.infoContext(s)
	owner, members, picked := a.projectMembers(s)
	actions := a.infoActions(s)
	bar.Children(func() {
		ui.Row(c).Height(24).Padding(0, 6).Radius(6).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Icon(c, iconMonitor).Size(14, 14).TextColor(p.Secondary)
			ui.Text(c, a.machineLabel(s.MachineID)).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
			if !a.presence.IsOnline(s.MachineID) {
				offlinePill(c, p)
			}
		})
		if len(members) > 0 {
			a.membersChip(c, p, bar, owner, members, picked)
		}
		if ctx.name != "" || ctx.branch != "" {
			ui.Box(c).Size(1, 14).Background(p.SideMuted.Alpha(0.25)).Shrink(0)
		}
		if ctx.name != "" {
			ui.Icon(c, ctx.icon).Size(14, 14).TextColor(p.Secondary)
			ui.Text(c, ctx.name).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine().MinWidth(0).Shrink(1)
		}
		if ctx.branch != "" {
			ui.Icon(c, iconBranch).Size(14, 14).TextColor(p.Secondary)
			ui.Text(c, ctx.branch).FontSize(sizeFoot).TextColor(p.Reading).SingleLine().MinWidth(0).Shrink(1)
		}
		ui.Spacer(c)
		if ctx.diff {
			ui.Row(c).Height(20).Padding(0, 6).Radius(5).Gap(4).AlignItems(ui.Center).Background(p.Text.Alpha(0.06)).Shrink(0).Children(func() {
				diffCounts(c, p, ctx.add, ctx.del)
			})
		}
		a.infoActionsView(c, p, s, actions)
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
	sessionID   string // the chat the composer writes to; "" for a new one
	expandable  bool   // the composer can take the conversation's room, as lody-ios's full screen
	quota       string // the Free quota's count-down, over the input
	// scheduled is the chip of a message held for a time, onScheduled
	// what it opens.
	scheduled   string
	onScheduled func()
}

// composerBox is Lody's message box: a 16-DIP-round card with the text
// above its controls.
func (a *App) composerBox(c *ui.Context, p *palette, o composerOptions) {
	if o.placeholder == "" {
		o.placeholder = a.composerPlaceholder(c)
	}
	if o.sendSize == 0 {
		o.sendSize = 28
	}
	full := o.expandable && a.composerFull
	bottomColumnFull(c, full, func() {
		if o.hint != "" {
			ui.Text(c, o.hint).FontSize(sizeFoot).TextColor(p.Secondary).Padding(0, 6)
		}
		if o.above != nil {
			o.above()
		}
		// It takes the height of its text at once, as Lody's.
		box := ui.Column(c.Key("composer")).FillWidth().Radius(16).Border(1, p.ComposerBorder).Background(p.Composer).Padding(8, 10).Gap(4)
		if full {
			box.Grow(1).MinHeight(0)
		}
		if !p.Dark {
			box.Shadow(0, 10, 28, -14, ui.RGBA(0, 0, 0, 0.16))
		}
		if dropped := box.DroppedFiles(); len(dropped) > 0 {
			a.addFiles(dropped)
		}
		hasFiles := len(a.attachments) > 0
		a.surfNext.composer = true
		var input ui.Element
		box.Children(func() {
			if full {
				a.formatBar(c, p)
			}
			if o.quota != "" {
				ui.Text(c, o.quota).FontSize(13).TextColor(p.Secondary).Padding(2, 6, 0, 6)
			}
			a.attachmentChips(c, p)
			field := ui.Box(c).FillWidth()
			if full {
				field.Grow(1).MinHeight(0)
			}
			field.Children(func() {
				empty := strings.TrimSpace(a.draft) == "" && !hasFiles
				lines := 10
				if full {
					lines = 400
				}
				input = ui.TextAreaBase(c, &a.draft).Lines(2, lines).Placeholder(o.placeholder).FontSize(sizeBody).LineHeight(24.0/14).
					TextColor(p.Reading).Padding(2, 4).FillWidth().AutoFocus().Label(locales.ComposerLabel()).Bind(&a.composerFocus).
					OnTextKey(func(k ui.TextKey) bool {
						if a.mentionKey(k) {
							return true
						}
						// ⇧Tab cycles the agent mode while the composer has the focus.
						if k.Key == ui.KeyTab && k.Mods == ui.Shift && a.surf.mode.can() {
							a.surf.mode.next()
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
				if full {
					input.FillHeight()
				}
				a.composerSel[0], a.composerSel[1] = input.TextSelection()
				if a.focusComposer {
					a.focusComposer = false
					a.composerFocus.Focus(c)
				}
				if empty {
					// The shortcut that focuses the box, while it waits: half
					// there, fading out over 150ms while the box has the focus.
					input.Padding(2, 64, 2, 4)
					hint := ui.Text(c.Key("focus-hint"), "⌘L").FontSize(sizeCaption).TextColor(p.Secondary).Padding(1, 5).Radius(5).Border(1, p.Border).
						Absolute().Top(2).Right(2).PassThrough()
					hint.Opacity(0.5 * (1 - hint.AnimateWith("focus", b2f(input.Focused()), 150*time.Millisecond, twEase)))
				}
			})
			ui.Row(c).Gap(4).AlignItems(ui.Center).Padding(2, 0, 0, 0).Children(func() {
				a.plusMenu(c, p, o.sessionID)
				if o.footer != nil {
					o.footer()
				}
				ui.Spacer(c)
				if o.expandable {
					icon, label := iconMaximize, locales.ComposerExpand()
					if full {
						icon, label = iconMinimize, locales.ComposerCollapse()
					}
					iconButton(c, p, icon, label, p.Hover).Size(26, 26).OnClick(func() {
						a.composerFull = !a.composerFull
						a.focusComposer = true
					})
				}
				if a.sending {
					if a.uploading != "" {
						ui.Text(c, a.uploading).FontSize(sizeFoot).TextColor(p.Secondary).Shrink(0)
					} else {
						spinner(c).Size(14, 14)
					}
				}
				if o.scheduled != "" {
					chip := ui.ButtonBase(c.Key("scheduled")).Height(26).Padding(0, 10).Radius(13).Gap(5).Border(1, p.Border).Cursor(ui.CursorPointer).
						Label(o.scheduled).OnClick(o.onScheduled).Transition(colorFade)
					if chip.Hovered() {
						chip.Background(p.Hover)
					}
					chip.Children(func() {
						ui.Icon(c, iconClock).Size(13, 13).TextColor(p.Text)
						ui.Text(c, o.scheduled).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text).SingleLine()
					})
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
// Lody's transition-all moves its fill, its press and its dimming over
// Tailwind's 150ms; it tints to 90% under the pointer.
func sendDisc(c *ui.Context, p *palette, icon *ui.SVG, label string, enabled bool, size float32) ui.Element {
	b := ui.ButtonBase(c.Key("send-disc")).Size(size, size).Radius(size / 2).Background(p.Text).Cursor(ui.CursorPointer).Label(label).Disabled(!enabled).Transition(twFade)
	twPress(b)
	if !p.Dark {
		b.Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.08))
	}
	if enabled && b.Hovered() {
		b.Background(p.Text.Alpha(0.9))
	}
	if dim := b.AnimateTo("dim", b2f(!enabled), twMotion); dim > 0 {
		b.Opacity(1 - 0.55*dim)
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
	b := ui.ButtonBase(c.Key(key)).Height(28).Padding(0, 8).Radius(8).Gap(6).Label(label).Transition(colorFade)
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
		Label(locales.PermissionMode(name)).OnClick(func() { *open = !*open }).Transition(colorFade)
	tip(c, p, b, name)
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
	popup(c, anchor, open, func() {
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
	if it.Permission.Questions != nil {
		a.askQuestionCard(c, p, e, it)
		return
	}
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
	// A question card comes up 8px as it fades in, over 320ms, as Lody's
	// question cards arrive; a permission request is there at once.
	card := ui.Column(c.Key("ask/"+it.Permission.RequestID)).FillWidth().Padding(12).Gap(8).Radius(14).Border(1, p.ComposerBorder).Background(p.Composer)
	if it.Permission.Question {
		card.Transition(ui.ElementTransition{Duration: durSlow, Ease: easeStandard, Enter: &ui.Motion{Y: 8}})
	}
	if !p.Dark {
		card.Shadow(0, 10, 28, -14, ui.RGBA(0, 0, 0, 0.16))
	}
	card.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
			ui.Text(c, question).FontSize(sizeBody).LineHeight(20.0 / 14).FontWeight(600).TextColor(p.Strong).Grow(1).MinWidth(0)
			stop := ui.ButtonBase(c).Height(24).Padding(0, 6).Radius(6).Gap(4).Cursor(ui.CursorPointer).Label(locales.StopAgent()).OnClick(a.stop).Transition(colorFade)
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
		main := ui.ButtonBase(c).Padding(0, 12).Cursor(ui.CursorPointer).Label(label).OnClick(choose).Transition(colorFade)
		if main.Hovered() {
			main.Background(ink.Alpha(0.08))
		}
		main.Children(func() { ui.Text(c, label).FontSize(sizeSub).FontWeight(500).TextColor(ink).SingleLine().MaxWidth(240) })
		if len(more) == 0 || len(chooseMore) == 0 {
			return
		}
		ui.Box(c).Width(1).Background(ink.Alpha(0.2))
		chevron := ui.ButtonBase(c.Key("more")).Width(24).Cursor(ui.CursorPointer).Label(locales.MoreOptions()).Transition(colorFade)
		if chevron.Hovered() {
			chevron.Background(ink.Alpha(0.08))
		}
		// The other answers of its kind, in Lody's menu above the button,
		// its end on the button's.
		st := &a.rowMenu("answers/" + label).menuState
		lodyMenuAbove(c, p, chevron, st, true, 4, 160, 290, 28, func(m *lodyMenu) {
			for _, o := range more {
				if m.item(o.ID, nil, o.Name, menuItem{}) {
					chooseMore[0](o)
				}
			}
		})
		if st.open {
			chevron.Background(ink.Alpha(0.08))
		}
		chevron.Children(func() { ui.Icon(c, iconChevronDown).Size(14, 14).TextColor(ink) })
	})
}

// modelCycle and modeCycle are the composer's models and modes, for the
// commands that step through them.
func (a *App) modelCycle(agentConfig, current string, pick func(string)) *cycleTarget {
	t := &cycleTarget{current: current, pick: pick}
	for _, m := range a.catalog.Capabilities[agentConfig].Models {
		t.values = append(t.values, m.ModelID)
	}
	return t
}

func modeCycle(modes []catalog.Mode, current string, pick func(string)) *cycleTarget {
	t := &cycleTarget{current: current, pick: pick}
	for _, m := range modes {
		t.values = append(t.values, m.ID)
	}
	return t
}

// composerPlaceholder teaches the keys the composer answers, as Lody's: /
// for the agent's commands, @ for what the project can mention, $ for
// skills; a narrow composer just asks.
func (a *App) composerPlaceholder(c *ui.Context) string {
	if w, _ := c.Size(); w > 0 && w < 440 {
		return locales.ComposerPlaceholder()
	}
	t := a.mentionTarget()
	return composerHint(len(a.catalog.Capabilities[t.agent].Commands) > 0, t.localProject() != "" || strings.HasPrefix(t.project, "github:"), t.machine != "")
}

// composerHint names the keys the composer answers.
func composerHint(commands, mentions, skills bool) string {
	switch {
	case commands && mentions && skills:
		return locales.HintCommandsMentionsSkills()
	case mentions && skills:
		return locales.HintMentionsSkills()
	case commands && skills:
		return locales.HintCommandsSkills()
	case commands && mentions:
		return locales.HintCommandsMentions()
	case commands:
		return locales.HintCommands()
	case mentions:
		return locales.HintMentions()
	case skills:
		return locales.HintSkills()
	}
	return locales.ComposerPlaceholder()
}

// formatBar is lody-ios's format bar over a full composer: Markdown
// around the selection, or at the start of its line.
func (a *App) formatBar(c *ui.Context, p *palette) {
	type format struct {
		icon         *ui.SVG
		label        string
		wrap, prefix string
	}
	formats := []format{
		{iconHeading, locales.FormatHeading(), "", "## "},
		{iconBold, locales.FormatBold(), "**", ""},
		{iconItalic, locales.FormatItalic(), "*", ""},
		{iconStrike, locales.FormatStrikethrough(), "~~", ""},
		{iconCode, locales.FormatCode(), "`", ""},
		{iconList, locales.FormatList(), "", "- "},
		{iconQuote, locales.FormatQuote(), "", "> "},
	}
	ui.Row(c.Key("format")).Height(32).Gap(2).AlignItems(ui.Center).Transition(popIn).Children(func() {
		for _, f := range formats {
			format := f
			iconButton(c, p, f.icon, f.label, p.Hover).Size(28, 28).OnClick(func() {
				text, caret := applyFormat(a.draft, a.composerSel[0], a.composerSel[1], format.wrap, format.prefix)
				a.draft = text
				a.mention.moveCaret, a.mention.caretTo = true, caret
				a.focusComposer = true
			})
		}
	})
}

// applyFormat wraps the runes start..end of text in wrap, or puts prefix
// at the start of their line, returning the text and where the caret goes.
func applyFormat(text string, start, end int, wrap, prefix string) (string, int) {
	r := []rune(text)
	start, end = min(max(start, 0), len(r)), min(max(end, 0), len(r))
	if start > end {
		start, end = end, start
	}
	if prefix != "" {
		line := start
		for line > 0 && r[line-1] != '\n' {
			line--
		}
		out := string(r[:line]) + prefix + string(r[line:])
		return out, end + len([]rune(prefix))
	}
	out := string(r[:start]) + wrap + string(r[start:end]) + wrap + string(r[end:])
	if start == end {
		// Nothing chosen: the caret goes between the marks.
		return out, start + len([]rune(wrap))
	}
	return out, end + 2*len([]rune(wrap))
}
