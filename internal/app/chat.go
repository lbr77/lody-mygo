package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/session"
)

const (
	columnWidth = 768 // Lody's conversation column
	columnPad   = 18
)

func (a *App) mainView(c *ui.Context, p *palette) {
	c.OnShortcut(ui.Cmd, ui.KeyN, func() { a.open("") })
	c.OnShortcut(ui.Cmd, ui.KeyL, func() { a.composerFocus.Focus(c) })
	c.OnShortcut(ui.Cmd, ui.KeyBackslash, func() { a.sidebarHidden = !a.sidebarHidden })
	ui.Row(c).Fill().AlignItems(ui.Stretch).Background(p.Ground).Children(func() {
		if !a.sidebarHidden {
			a.sidebar(c, p)
		}
		left := float32(0)
		if a.sidebarHidden {
			left = 4
		}
		card := ui.Column(c).Grow(1).MinWidth(0).Margin(4, 4, 4, left).Radius(14).Border(0.5, p.CardRim).Background(p.Canvas).Clip()
		if !p.Dark {
			card.Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.03))
		}
		card.Children(func() {
			if a.selected == "" {
				a.homeView(c, p)
			} else {
				a.sessionView(c, p)
			}
		})
	})
}

// topBar is the 44px bar over the conversation, which drags the window.
func (a *App) topBar(c *ui.Context, p *palette, title string, detail func()) {
	left := float32(16)
	if a.sidebarHidden {
		left = 76 // clear the traffic lights
	}
	ui.Row(c).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Padding(0, 12, 0, left).Gap(8).DragWindow().Children(func() {
		if a.sidebarHidden {
			iconButton(c, p, iconSidebar, "Show sidebar", p.Hover).OnClick(func() { a.sidebarHidden = false })
		}
		ui.Text(c, title).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine().MinWidth(0).Shrink(1)
		if detail != nil {
			detail()
		}
		ui.Spacer(c)
	})
}

func (a *App) sessionView(c *ui.Context, p *palette) {
	sess := a.catalog.Session(a.selected)
	title := "Chat"
	if sess != nil {
		title = sess.Title
	}
	a.topBar(c, p, title, func() {
		if sess == nil {
			return
		}
		ui.Text(c, a.machineName(sess.MachineID)).FontSize(sizeFoot).TextColor(p.Tertiary).SingleLine().Shrink(0)
		if !a.presence.IsOnline(sess.MachineID) {
			offlinePill(c, p)
		}
		if sess.BranchName != "" {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
				ui.Icon(c, iconBranch).Size(12, 12).TextColor(p.Tertiary)
				ui.Text(c, sess.BranchName).FontSize(sizeFoot).TextColor(p.Tertiary).SingleLine()
			})
		}
	})
	v := a.view
	if v == nil || (v.Status != "live" && len(v.Entries) == 0) {
		ui.Column(c).Grow(1).Center().Gap(10).Children(func() {
			ui.Spinner(c).Size(18, 18)
			msg := "Loading conversation…"
			if v != nil && v.Reason != "" {
				msg = "Reconnecting… (" + v.Reason + ")"
			}
			ui.Text(c, msg).FontSize(sizeSub).TextColor(p.Secondary)
		})
		a.composer(c, p, sess, nil)
		return
	}
	a.syncMarkdown(p)
	entries := v.Entries
	a.chatList.Key = func(i int) any {
		if i < len(entries) {
			return entries[i].ID
		}
		return i
	}
	a.chatList.FollowEnd = true
	ui.List(c, &a.chatList, len(entries)+1, func(i int) {
		if i == len(entries) {
			a.liveStatus(c, p, v)
			return
		}
		a.entry(c, p, entries, i)
	}).Grow(1).MinHeight(0).Padding(16, 0, 24, 0).Children(func() {
		ui.Text(c, "Send a message to start.").FontSize(sizeSub).TextColor(p.Secondary).Margin(ui.Auto)
	})
	a.composer(c, p, sess, v)
}

// offlinePill marks a machine that isn't connected.
func offlinePill(c *ui.Context, p *palette) {
	ui.Text(c, "Offline").FontSize(10).FontWeight(500).TextColor(p.Danger).Padding(1, 5).Radius(5).Background(p.Danger.Alpha(0.14)).Shrink(0)
}

// column centers the conversation column.
func column(c *ui.Context, key any, fn func()) ui.Element {
	return ui.Row(c.Key(key)).FillWidth().Justify(ui.Center).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).MaxWidth(columnWidth).Padding(0, columnPad).Children(fn)
	})
}

func (a *App) entry(c *ui.Context, p *palette, entries []session.Entry, i int) {
	e := entries[i]
	top := float32(0)
	if i > 0 {
		switch {
		case e.Role == "user":
			top = 56 // answer to the next message
		case entries[i-1].Role == "user":
			top = 36 // message to its response
		default:
			top = 16
		}
	}
	column(c, e.ID, func() {
		if e.Role == "user" {
			a.userMessage(c, p, e)
		} else {
			a.assistantMessage(c, p, e, i == len(entries)-1)
		}
	}).Padding(top, 0, 0, 0)
}

func (a *App) userMessage(c *ui.Context, p *palette, e session.Entry) {
	name := ""
	if a.account != nil {
		name = a.account.User.Name
	}
	ui.Column(c).FillWidth().AlignItems(ui.End).Gap(4).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(0, 40, 0, 0).Children(func() {
			meta := clock(e.Timestamp)
			switch {
			case e.Queued:
				meta += " · Queued"
			case e.Status == "pending":
				meta += " · Sending"
			}
			ui.Text(c, meta).FontSize(sizeCaption).TextColor(p.Secondary)
			if !e.Queued && e.Status != "pending" {
				ui.Icon(c, iconCheck).Size(12, 12).TextColor(p.Primary)
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Start).Justify(ui.End).FillWidth().Children(func() {
			ui.Text(c, e.Text()).Selectable().FontSize(sizeBody).LineHeight(24.0/14).TextColor(p.Reading).
				Padding(12, 16).Radius(14).Background(p.UserBubble).MaxWidth(540).Shrink(1).MinWidth(0)
			avatar(c, p, name, 32, 16)
		})
	})
}

// block is a run of items shown together: text, a thinking block, or an
// activity group of tool calls.
type block struct {
	text     bool
	thought  bool
	items    []session.Item
	key      string
	question *session.Item
}

func blocks(items []session.Item) []block {
	var out []block
	for _, it := range items {
		switch {
		case it.Type == "text":
			out = append(out, block{text: true, items: []session.Item{it}, key: it.ID})
		case it.Type == "thought":
			out = append(out, block{thought: true, items: []session.Item{it}, key: it.ID})
		case it.Permission != nil && it.Permission.Pending:
			item := it
			out = append(out, block{question: &item, key: it.ID})
		default:
			if n := len(out); n > 0 && !out[n-1].text && !out[n-1].thought && out[n-1].question == nil {
				out[n-1].items = append(out[n-1].items, it)
			} else {
				out = append(out, block{items: []session.Item{it}, key: it.ID})
			}
		}
	}
	return out
}

func (a *App) assistantMessage(c *ui.Context, p *palette, e session.Entry, last bool) {
	bs := blocks(e.Items)
	ui.Column(c).FillWidth().Gap(12).Padding(0, 4).Children(func() {
		for bi, b := range bs {
			lastBlock := bi == len(bs)-1
			switch {
			case b.question != nil:
				a.permissionPanel(c, p, e, *b.question)
			case b.thought:
				a.thinking(c, p, e, b, !e.Finished && lastBlock)
			case b.text:
				it := b.items[0]
				if !e.Finished && lastBlock {
					markdown.Stream(c.Key(it.ID), it.Text, &a.md)
				} else {
					markdown.View(c.Key(it.ID), it.Text, &a.md)
				}
			default:
				a.activity(c, p, e, b, !e.Finished && lastBlock)
			}
		}
		if e.Finished && len(e.FileDiffs) > 0 {
			a.fileDiffs(c, p, e.FileDiffs)
		}
		if e.Finished && e.StartedAt > 0 && e.EndedAt > e.StartedAt {
			d := time.Duration(e.EndedAt-e.StartedAt) * time.Millisecond
			label := "Worked for " + shortDuration(d)
			if e.Model != "" {
				label += " · " + e.Model
			}
			ui.Text(c, label).FontSize(sizeCaption).TextColor(p.Tertiary)
		}
	})
}

// thinking is a thinking block: one collapsed row, its text shown only
// once clicked.
func (a *App) thinking(c *ui.Context, p *palette, e session.Entry, b block, live bool) {
	key := e.ID + "/" + b.key
	open := a.expanded[key]
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		head := ui.Row(c.Key(key)).MinHeight(24).Gap(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label("Show thinking").
			OnClick(func() { a.expanded[key] = !a.expanded[key] })
		hovered := head.Hovered()
		head.Children(func() {
			ink := p.Secondary
			if hovered {
				ink = p.Text
			}
			label := "Thought"
			if live {
				label = "Thinking…"
			}
			ui.Text(c, label).FontSize(sizeSub).TextColor(ink)
			if hovered || open {
				icon := iconChevronRight
				if open {
					icon = iconChevronDown
				}
				ui.Icon(c, icon).Size(13, 13).TextColor(ink)
			}
		})
		if !open {
			return
		}
		ui.Column(c).Padding(2, 0, 4, 12).BorderWidth(0, 0, 0, 1).BorderColor(p.Border).Children(func() {
			for _, it := range b.items {
				markdown.View(c.Key(it.ID), it.Text, &a.mdMuted)
			}
		})
	})
}

// activity is a collapsible group of steps, labeled by what they did.
func (a *App) activity(c *ui.Context, p *palette, e session.Entry, b block, live bool) {
	key := e.ID + "/" + b.key
	open := a.expanded[key]
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		head := ui.Row(c.Key(key)).MinHeight(24).Gap(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label("Show steps").
			OnClick(func() { a.expanded[key] = !a.expanded[key] })
		hovered := head.Hovered()
		head.Children(func() {
			if live {
				ui.Spinner(c).Size(12, 12)
			}
			ink := p.Secondary
			if hovered {
				ink = p.Text
			}
			ui.Text(c, activityLabel(b.items, live)).FontSize(sizeSub).TextColor(ink).SingleLine().MinWidth(0)
			if hovered || open {
				icon := iconChevronRight
				if open {
					icon = iconChevronDown
				}
				ui.Icon(c, icon).Size(13, 13).TextColor(ink)
			}
		})
		if !open {
			return
		}
		ui.Column(c).Gap(2).Padding(2, 0, 4, 2).BorderWidth(0, 0, 0, 1).BorderColor(p.Border).Children(func() {
			for _, it := range b.items {
				a.step(c.Key(it.ID), p, key+"/"+it.ID, it)
			}
		})
	})
}

func (a *App) step(c *ui.Context, p *palette, key string, it session.Item) {
	switch it.Type {
	case "thought":
		ui.Column(c).Padding(2, 10).Children(func() {
			markdown.View(c, it.Text, &a.mdMuted)
		})
	case "plan":
		ui.Column(c).Padding(4, 10).Gap(4).Children(func() {
			for _, pe := range it.Plan {
				mark, ink := "○", p.Secondary
				switch pe.Status {
				case "completed":
					mark, ink = "✓", p.Success
				case "in_progress":
					mark, ink = "◐", p.Primary
				}
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, mark).FontSize(sizeSub).TextColor(ink)
					ui.Text(c, pe.Content).FontSize(sizeSub).TextColor(p.Text).MinWidth(0)
				})
			}
		})
	case "subagent_task":
		sa := it.Subagent
		ui.Row(c).MinHeight(24).Padding(2, 10).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconBot).Size(14, 14).TextColor(p.Secondary)
			label := "Subagent"
			if sa != nil && sa.Description != "" {
				label += ": " + sa.Description
			}
			ui.Text(c, label).FontSize(sizeSub).TextColor(p.Secondary).SingleLine().MinWidth(0).Grow(1)
			if sa != nil && sa.Status != "" {
				ui.Text(c, sa.Status).FontSize(sizeCaption).TextColor(p.Tertiary)
			}
		})
	case "system_notice":
		ui.Text(c, firstNonEmpty(it.Text, it.Title)).FontSize(sizeSub).TextColor(p.Warning).Padding(2, 10)
	case "tool_call":
		open := a.expanded[key]
		row := ui.Row(c).MinHeight(24).Padding(2, 10).Gap(8).AlignItems(ui.Center).Radius(5)
		hovered := false
		if it.HasDetail {
			row.Cursor(ui.CursorPointer).Label("Show details").OnClick(func() { a.expanded[key] = !a.expanded[key] })
			hovered = row.Hovered()
		}
		row.Children(func() {
			title := firstNonEmpty(it.Title, it.Path, it.Kind)
			ink := p.Secondary
			if hovered {
				ink = p.Text
			}
			ui.Text(c, strings.TrimSpace(toolVerb(it)+" "+title)).FontSize(sizeSub).TextColor(ink).SingleLine().MinWidth(0).Shrink(1)
			if it.Added > 0 || it.Removed > 0 {
				ui.Text(c, fmt.Sprintf("+%d", it.Added)).FontSize(sizeFoot).Font(fontMono).TextColor(p.DiffAdd).Shrink(0)
				ui.Text(c, fmt.Sprintf("−%d", it.Removed)).FontSize(sizeFoot).Font(fontMono).TextColor(p.DiffDel).Shrink(0)
			}
			switch it.Status {
			case "pending", "in_progress":
				ui.Spinner(c).Size(12, 12)
			case "failed":
				ui.Icon(c, iconCircleAlert).Size(12, 12).TextColor(p.Danger)
			}
			if hovered || open {
				icon := iconChevronRight
				if open {
					icon = iconChevronDown
				}
				ui.Icon(c, icon).Size(13, 13).TextColor(ink)
			}
		})
		if open {
			a.toolDetail(c, p, key, it)
		}
	default:
		ui.Text(c, it.Type).FontSize(sizeSub).TextColor(p.Tertiary).Padding(2, 10)
	}
}

// toolVerb names what a tool call does, in the tense of its status.
func toolVerb(it session.Item) string {
	done := it.Status == "completed" || it.Status == "failed"
	verbs := map[string][2]string{
		"read":    {"Reading", "Read"},
		"edit":    {"Editing", "Edited"},
		"delete":  {"Deleting", "Deleted"},
		"move":    {"Moving", "Moved"},
		"search":  {"Searching", "Searched"},
		"execute": {"Running", "Ran"},
		"fetch":   {"Fetching", "Fetched"},
		"think":   {"Thinking", "Thought"},
	}
	v, ok := verbs[it.Kind]
	if !ok {
		return ""
	}
	// ACP titles often start with the verb already.
	if strings.HasPrefix(strings.ToLower(it.Title), strings.ToLower(v[1])) || strings.HasPrefix(strings.ToLower(it.Title), strings.ToLower(v[0])) {
		return ""
	}
	if done {
		return v[1]
	}
	return v[0]
}

func activityLabel(items []session.Item, live bool) string {
	counts := map[string]int{}
	thought, other := false, 0
	for _, it := range items {
		switch it.Type {
		case "thought":
			thought = true
		case "tool_call":
			counts[it.Kind]++
		default:
			other++
		}
	}
	plural := func(n int, one, many string) string {
		if n == 1 {
			return "1 " + one
		}
		return fmt.Sprintf("%d %s", n, many)
	}
	var parts []string
	if n := counts["execute"]; n > 0 {
		parts = append(parts, "Ran "+plural(n, "command", "commands"))
	}
	if n := counts["read"]; n > 0 {
		parts = append(parts, "Read "+plural(n, "file", "files"))
	}
	if n := counts["edit"] + counts["delete"] + counts["move"]; n > 0 {
		parts = append(parts, "Edited "+plural(n, "file", "files"))
	}
	if n := counts["search"]; n > 0 {
		parts = append(parts, "Searched "+plural(n, "time", "times"))
	}
	if n := counts["fetch"]; n > 0 {
		parts = append(parts, "Fetched "+plural(n, "page", "pages"))
	}
	rest := counts["other"] + counts["think"] + counts[""] + other
	if rest > 0 {
		parts = append(parts, plural(rest, "step", "steps"))
	}
	if thought && len(parts) == 0 {
		if live {
			return "Thinking…"
		}
		return "Thought"
	}
	if live && len(parts) == 0 {
		return "Working…"
	}
	return strings.Join(parts, " · ")
}

// permissionPanel asks the user to choose, as Lody's framed panels do.
func (a *App) permissionPanel(c *ui.Context, p *palette, e session.Entry, it session.Item) {
	title := "Permission needed"
	if it.Permission.Question {
		title = "Question"
	}
	ui.Column(c).FillWidth().Radius(12).Border(1, p.Border).Background(p.Composer).Clip().Children(func() {
		ui.Row(c).Padding(6, 12).Gap(6).AlignItems(ui.Center).Background(p.Secondary.Alpha(0.09)).Children(func() {
			ui.Icon(c, iconHand).Size(12, 12).TextColor(p.Warning)
			ui.Text(c, title).FontSize(sizeCaption).FontWeight(500).TextColor(p.Text)
		})
		ui.Column(c).Padding(8, 12, 12, 12).Gap(10).Children(func() {
			ui.Text(c, firstNonEmpty(it.Title, it.Kind)).FontSize(sizeSub).TextColor(p.Text).Selectable()
			ui.Row(c).Gap(6).Wrap().Children(func() {
				for _, opt := range it.Permission.Options {
					var b ui.Element
					if strings.HasPrefix(opt.Kind, "allow") {
						b = inkButton(c.Key(opt.ID), p, opt.Name, nil, 28)
					} else {
						b = ghostButton(c.Key(opt.ID), p, opt.Name).Border(1, p.Border)
					}
					b.OnClick(func() { a.answer(e.ID, it, opt) })
				}
			})
		})
	})
}

func (a *App) fileDiffs(c *ui.Context, p *palette, diffs []session.FileDiff) {
	ui.Column(c).FillWidth().Radius(10).Border(1, p.Border).Clip().Children(func() {
		ui.Row(c).Padding(6, 12).Background(p.Secondary.Alpha(0.06)).Children(func() {
			label := fmt.Sprintf("%d files changed", len(diffs))
			if len(diffs) == 1 {
				label = "1 file changed"
			}
			ui.Text(c, label).FontSize(sizeCaption).FontWeight(500).TextColor(p.Secondary)
		})
		for _, d := range diffs {
			ui.Row(c.Key(d.Path)).Padding(4, 12).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, d.Path).Font(fontMono).FontSize(sizeFoot).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
				ui.Text(c, fmt.Sprintf("+%d", d.Add)).Font(fontMono).FontSize(sizeFoot).TextColor(p.DiffAdd)
				ui.Text(c, fmt.Sprintf("−%d", d.Del)).Font(fontMono).FontSize(sizeFoot).TextColor(p.DiffDel)
			})
		}
	})
}

// liveStatus is the last row: what the agent is doing right now.
func (a *App) liveStatus(c *ui.Context, p *palette, v *session.View) {
	column(c, "status", func() {
		if !v.Working || len(v.Entries) == 0 {
			return
		}
		last := v.Entries[len(v.Entries)-1]
		if last.Role == "assistant" && !last.Finished {
			return // the reply shows its own progress
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(36, 4, 0, 4).Children(func() {
			workingGrid(c, p, 12)
			ui.Text(c, "Working…").FontSize(sizeSub).TextColor(p.Secondary)
		})
	})
}

// composer is Lody's message box, pinned under the conversation.
func (a *App) composer(c *ui.Context, p *palette, sess *catalog.Session, v *session.View) {
	working := v != nil && v.Working
	ready := v != nil && v.Status == "live" && sess != nil
	hint := ""
	if sess != nil && !a.presence.IsOnline(sess.MachineID) {
		hint = a.machineName(sess.MachineID) + " is offline. Messages wait until it reconnects."
	}
	a.composerBox(c, p, composerOptions{
		hint:        hint,
		placeholder: "Ask for follow-up changes",
		canSend:     ready && !a.sending,
		working:     working,
		onSend:      a.send,
		onStop:      a.stop,
		footer: func() {
			if sess == nil {
				return
			}
			agent := firstNonEmpty(a.agentName(sess.MachineID, sess.AgentConfigID), sess.AgentType, sess.CLIType)
			chip(c, p, iconBot, agent)
			current := a.models[sess.ID]
			if current == "" && v != nil {
				current = v.ModelID
			}
			if current == "" && sess.LastModel != nil {
				current = sess.LastModel.ModelID
			}
			a.modelPicker(c, p, "model/"+sess.ID, sess.AgentConfigID, current, sess.LastModel, func(id string) { a.models[sess.ID] = id })
		},
	})
}

// modelPicker chooses among the models the agent reports; without its
// capabilities it shows the current model.
func (a *App) modelPicker(c *ui.Context, p *palette, key, agentConfig, current string, last *catalog.Model, pick func(string)) {
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
	if len(models) == 0 {
		if label != "" {
			chip(c, p, nil, label)
		}
		return
	}
	if label == "" {
		label = "Default model"
	}
	var options []option
	for _, m := range models {
		options = append(options, option{m.ModelID, firstNonEmpty(m.Name, m.ModelID)})
	}
	a.picker(c, p, key, nil, label, options, pick)
}

// agentName is an agent config's display name.
func (a *App) agentName(machine, id string) string {
	if ag := a.agent(machine, id); ag != nil {
		return ag.Name
	}
	return ""
}

type composerOptions struct {
	hint        string
	placeholder string
	canSend     bool
	working     bool
	onSend      func()
	onStop      func()
	footer      func()
}

func (a *App) composerBox(c *ui.Context, p *palette, o composerOptions) {
	ui.Row(c).Shrink(0).FillWidth().Justify(ui.Center).Padding(0, 0, 10, 0).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).MaxWidth(columnWidth).Padding(0, columnPad).Gap(6).Children(func() {
			if o.hint != "" {
				ui.Text(c, o.hint).FontSize(sizeFoot).TextColor(p.Secondary).Padding(0, 6)
			}
			box := ui.Column(c).FillWidth().Radius(16).Border(1, p.ComposerBorder).Background(p.Composer).Padding(8, 10).Gap(4)
			if !p.Dark {
				box.Shadow(0, 10, 28, -14, ui.RGBA(0, 0, 0, 0.16))
			}
			box.Children(func() {
				ui.TextAreaBase(c, &a.draft).Lines(2, 10).Placeholder(o.placeholder).FontSize(sizeBody).LineHeight(24.0/14).
					TextColor(p.Reading).Padding(2, 4).FillWidth().AutoFocus().Label("Message").Bind(&a.composerFocus).
					OnTextKey(func(k ui.TextKey) bool {
						if k.Key == ui.KeyEnter && k.Mods&ui.Shift == 0 {
							if o.canSend && strings.TrimSpace(k.Text()) != "" {
								a.draft = k.Text()
								o.onSend()
								k.SetText(a.draft)
							}
							return true
						}
						return false
					})
				ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(2, 0, 0, 0).Children(func() {
					if o.footer != nil {
						o.footer()
					}
					ui.Spacer(c)
					if a.sending {
						ui.Spinner(c).Size(14, 14)
					}
					if o.working && strings.TrimSpace(a.draft) == "" && o.onStop != nil {
						sendDisc(c, p, nil, "Stop", true).OnClick(o.onStop)
						return
					}
					empty := strings.TrimSpace(a.draft) == ""
					sendDisc(c, p, iconArrowUp, "Send", o.canSend && !empty).OnClick(func() {
						if o.canSend && strings.TrimSpace(a.draft) != "" {
							o.onSend()
						}
					})
				})
			})
			if a.sendError != "" {
				ui.Text(c, a.sendError).FontSize(sizeFoot).TextColor(p.Danger).Padding(0, 6).Selectable()
			}
		})
	})
}

// sendDisc is the 28px round ink button; without an icon it is Stop's
// rounded square.
func sendDisc(c *ui.Context, p *palette, icon *ui.SVG, label string, enabled bool) ui.Element {
	b := ui.ButtonBase(c).Size(28, 28).Radius(14).Background(p.Text).Cursor(ui.CursorPointer).Label(label).Tooltip(label).Disabled(!enabled)
	if !enabled {
		b.Opacity(0.3)
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

// chip is a run-config label in the composer's footer.
func chip(c *ui.Context, p *palette, icon *ui.SVG, label string) ui.Element {
	return ui.Row(c).Height(28).Padding(0, 8).Radius(8).Gap(6).AlignItems(ui.Center).Children(func() {
		if icon != nil {
			ui.Icon(c, icon).Size(14, 14).TextColor(p.Secondary)
		}
		ui.Text(c, label).FontSize(sizeSub).FontWeight(550).TextColor(p.Secondary).SingleLine()
	})
}

// homeView is New chat: pick a machine, an agent and a project, then
// write the first message.
func (a *App) homeView(c *ui.Context, p *palette) {
	a.topBar(c, p, "New chat", nil)
	ui.Column(c).Grow(1).MinHeight(0).Justify(ui.Center).AlignItems(ui.Center).Padding(0, 0, 40, 0).Gap(20).Children(func() {
		ui.Icon(c, iconLody).Size(36, 40).TextColor(p.Text.Alpha(0.85))
		ui.Text(c, "What should we work on?").FontSize(22).FontWeight(600).TextColor(p.Strong)
		if len(a.catalog.MachineIDs) == 0 {
			ui.Column(c).Gap(8).AlignItems(ui.Center).MaxWidth(420).Children(func() {
				msg := "Syncing your workspace…"
				if a.synced {
					msg = "No machine is connected to this workspace yet. Run this on a computer to connect it:"
				}
				ui.Text(c, msg).FontSize(sizeBody).TextColor(p.Secondary).TextAlign(ui.Center)
				if a.synced {
					ui.Text(c, "npx lody daemon start").Font(fontMono).FontSize(sizeSub).TextColor(p.Text).Padding(6, 10).Radius(8).Background(p.CodeBg).Selectable()
				}
			})
			return
		}
		ui.Row(c).Gap(6).AlignItems(ui.Center).Wrap().Justify(ui.Center).Children(func() {
			a.picker(c, p, "machine", iconMonitor, a.machineName(a.newChat.machine), a.machineOptions(), func(id string) {
				a.newChat.machine, a.newChat.project = id, ""
				a.pickNewChatDefaults()
			})
			agentLabel := "No agent"
			if ag := a.agent(a.newChat.machine, a.newChat.agent); ag != nil {
				agentLabel = ag.Name
			}
			a.picker(c, p, "agent", iconBot, agentLabel, a.agentOptions(), func(id string) { a.newChat.agent = id })
			projectLabel := "No project"
			if pr := a.catalog.Project(a.newChat.project); pr != nil {
				projectLabel = pr.Name
			}
			a.picker(c, p, "project", iconFolder, projectLabel, a.projectOptions(), func(id string) { a.newChat.project = id })
		})
	})
	a.composerBox(c, p, composerOptions{
		placeholder: "Describe a task for the agent",
		canSend:     a.agent(a.newChat.machine, a.newChat.agent) != nil && !a.sending,
		onSend:      a.startChat,
	})
}

type option struct{ id, label string }

func (a *App) machineOptions() []option {
	var out []option
	for _, id := range a.catalog.MachineIDs {
		out = append(out, option{id, a.machineName(id)})
	}
	return out
}

func (a *App) agentOptions() []option {
	var out []option
	for _, ag := range a.catalog.Agents[a.newChat.machine] {
		out = append(out, option{ag.ID, ag.Name})
	}
	return out
}

func (a *App) projectOptions() []option {
	out := []option{{"", "No project"}}
	for _, pr := range a.catalog.Projects {
		if pr.MachineID == a.newChat.machine && strings.Contains(pr.ID, ":local:") {
			out = append(out, option{pr.ID, pr.Name})
		}
	}
	return out
}

// picker is a composer-style chip that opens a list to choose from.
func (a *App) picker(c *ui.Context, p *palette, key string, icon *ui.SVG, label string, options []option, pick func(string)) {
	if a.pickers == nil {
		a.pickers = map[string]*bool{}
	}
	open := a.pickers[key]
	if open == nil {
		open = new(bool)
		a.pickers[key] = open
	}
	b := ui.ButtonBase(c.Key(key)).Height(28).Padding(0, 8).Radius(8).Gap(6).Cursor(ui.CursorPointer).Label(label)
	if b.Hovered() || *open {
		b.Background(p.Text.Alpha(0.06))
	}
	b.OnClick(func() { *open = !*open })
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).Size(14, 14).TextColor(p.Secondary)
		}
		ui.Text(c, label).FontSize(sizeSub).FontWeight(550).TextColor(p.Secondary).SingleLine().MaxWidth(200)
		ui.Icon(c, iconChevronDown).Size(12, 12).TextColor(p.Tertiary)
	})
	ui.Popover(c, b, open, func() {
		ui.Column(c).MinWidth(180).MaxHeight(320).Gap(1).Children(func() {
			if len(options) == 0 {
				ui.Text(c, "Nothing to choose").FontSize(sizeSub).TextColor(p.Secondary).Padding(6, 8)
			}
			for _, o := range options {
				row := menuRow(c.Key(o.id), p, o.label)
				if row.Clicked() {
					pick(o.id)
					*open = false
				}
				row.Children(func() {
					ui.Text(c, o.label).FontSize(sizeSub).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
				})
			}
		})
	})
}

// syncMarkdown keeps the Markdown themes in step with the palette.
func (a *App) syncMarkdown(p *palette) {
	if a.md.Theme != nil && a.mdDark == p.Dark {
		return
	}
	a.mdDark = p.Dark
	base := ui.LightTheme()
	if p.Dark {
		base = ui.DarkTheme()
	}
	base.Font, base.FontSize = fontUI, sizeBody
	t := markdown.DefaultTheme(base)
	t.FontSize = sizeBody
	t.Font = fontUI
	t.CodeFont = fontMono
	t.LineHeight = 1.55
	t.Text = p.Reading
	t.Strong = p.Strong
	t.Muted = p.Secondary
	t.Link = p.Link
	t.Code = p.Text
	t.CodeBackground = p.InlineCode
	t.CodeBlockBackground = p.CodeBg
	t.CodeBlockBorder = p.CodeBg
	t.CodeBlockRadius = 8
	t.Rule = p.Border
	t.QuoteBar = p.Border
	t.QuoteText = p.Secondary
	a.md.Theme = t

	muted := *t
	muted.FontSize = sizeSub
	muted.Text = p.Secondary
	muted.Strong = p.Secondary
	a.mdMuted.Theme = &muted
}

func clock(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ""
	}
	t = t.Local()
	if time.Since(t) > 20*time.Hour {
		return t.Format("Jan 2, 15:04")
	}
	return t.Format("15:04")
}

func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
