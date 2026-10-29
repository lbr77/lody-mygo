package app

import (
	"fmt"
	"github.com/lbr77/lody-mygo/locales"
	"path"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/lbr77/lody-mygo/internal/session"
)

// block is a run of items shown together: text, or an activity group of
// the thinking and tool calls between texts.
type block struct {
	text   bool
	plan   bool // a proposed plan, a panel of its own
	notice bool // a system notice, which never folds away
	files  bool // files the agent delivered, one list of cards
	items  []session.Item
	key    string
}

func blocks(items []session.Item) []block {
	var out []block
	for _, it := range items {
		switch {
		case it.Type == "subagent_task":
			continue // the task card shows these
		case it.Type == "plan":
			continue // the plan bar at the turn's head shows the latest
		case it.Type == "text":
			out = append(out, block{text: true, items: []session.Item{it}, key: it.ID})
		case it.Type == "system_notice":
			out = append(out, block{notice: true, items: []session.Item{it}, key: it.ID})
		case it.Type == "file":
			// Adjacent files are one list.
			if n := len(out); n > 0 && out[n-1].files {
				out[n-1].items = append(out[n-1].items, it)
			} else {
				out = append(out, block{files: true, items: []session.Item{it}, key: it.ID})
			}
		case it.Type == "proposed_plan":
			if strings.TrimSpace(it.Text) != "" && it.Status != "cleared" {
				out = append(out, block{plan: true, items: []session.Item{it}, key: it.ID})
			}
		default:
			if n := len(out); n > 0 && !out[n-1].text {
				out[n-1].items = append(out[n-1].items, it)
			} else {
				out = append(out, block{items: []session.Item{it}, key: it.ID})
			}
		}
	}
	return out
}

// answerStart is where a finished turn's answer begins: its last run of
// text. What comes before folds under "Worked for".
func answerStart(bs []block) int {
	k := len(bs)
	for k > 0 && (bs[k-1].text || bs[k-1].plan || bs[k-1].notice || bs[k-1].files) {
		k--
	}
	return k
}

func turnDuration(e session.Entry) time.Duration {
	if e.StartedAt > 0 && e.EndedAt > e.StartedAt {
		return time.Duration(e.EndedAt-e.StartedAt) * time.Millisecond
	}
	return 0
}

func (a *App) assistantMessage(c *ui.Context, p *palette, e session.Entry) {
	bs := blocks(e.Items)
	turn := ui.Column(c).FillWidth().Padding(0, 4)
	hovered := turn.Hovered()
	turn.Children(func() {
		// Prose keeps its paragraph rhythm; rows of work sit close.
		prevText, first := false, true
		place := func(b block, el func()) {
			top := float32(4)
			switch {
			case first:
				top = 0
			case b.text || prevText || b.plan:
				top = 12
			}
			// Before el, which places the blocks a group folds.
			prevText, first = b.text, false
			ui.Column(c.Key("b/"+b.key)).FillWidth().Margin(top, 0, 0, 0).Children(el)
		}
		// The agent's plan leads the turn, as Lody's plan row.
		if plan := lastPlan(e.Items); len(plan) > 0 {
			place(block{key: "plan-bar"}, func() { a.planBar(c, p, e.ID+"/plan-bar", plan) })
		}
		tasks := collectTasks(e.Items)
		taskCard := func() {
			if len(tasks) > 0 {
				ui.Column(c.Key("tasks")).FillWidth().Margin(12, 0, 0, 0).Children(func() { a.taskPanel(c, p, e, tasks) })
			}
		}
		if !e.Finished {
			for bi, b := range bs {
				last := bi == len(bs)-1
				place(b, func() { a.block(c, p, e, b, last) })
			}
			taskCard()
			return
		}
		k := answerStart(bs)
		if k > 0 {
			key := e.ID + "/work"
			open := a.expanded[key]
			label := locales.Worked()
			if d := turnDuration(e); d > 0 {
				label = locales.WorkedFor(formatDuration(d))
			}
			place(block{key: "work"}, func() {
				attention := false
				for _, b := range bs[:k] {
					for _, it := range b.items {
						attention = attention || it.Status == "failed" || it.Permission != nil && it.Permission.Pending
					}
				}
				shown := disclosureMarked(c.Key(key), p, label, open, false, attention, func() { a.expanded[key] = !a.expanded[key] })
				unfold(c, shown, open, func() {
					for _, b := range bs[:k] {
						place(b, func() { a.block(c, p, e, b, false) })
					}
				})
			})
		}
		for _, b := range bs[k:] {
			place(b, func() { a.block(c, p, e, b, false) })
		}
		taskCard()
		if len(e.FileDiffs) > 0 {
			ui.Column(c).FillWidth().Margin(12, 0, 0, 0).Children(func() { a.editedFiles(c, p, e) })
		}
		ui.Column(c).Margin(4, 0, 0, 0).Children(func() { a.turnFooter(c, p, e, hovered) })
		a.planActions(c, p, e)
	})
}

func (a *App) block(c *ui.Context, p *palette, e session.Entry, b block, last bool) {
	live := !e.Finished && last
	switch {
	case b.plan:
		a.planPanel(c, p, e, b.items[0])
	case b.notice:
		a.systemNotice(c, p, e, b.items[0])
	case b.files:
		a.fileCards(c, p, a.selected, b.items, false)
	case b.text:
		it := b.items[0]
		id := e.ID + "/" + it.ID
		src := a.findMarkdown(id, it.Text)
		var md ui.Element
		if live {
			md = markdown.Stream(c.Key(it.ID), src, &a.md)
		} else {
			md = markdown.View(c.Key(it.ID), src, &a.md)
		}
		a.findRing(p, id, md)
	default:
		a.activity(c, p, e, b, live)
	}
}

// disclosure is a text row that folds what follows it, its chevron showing
// on hover and turning as it opens. It returns how far it is open, for
// unfold.
func disclosure(c *ui.Context, p *palette, label string, open, live bool, toggle func()) float32 {
	return disclosureMarked(c, p, label, open, live, false, toggle)
}

// disclosureMarked is a disclosure that, with attention, leads with the
// orange warning lody-ios puts on a group a step of which failed or waits
// on a permission.
func disclosureMarked(c *ui.Context, p *palette, label string, open, live, attention bool, toggle func()) float32 {
	head := ui.Row(c).MinHeight(24).Gap(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(label)
	// Lody's chevron turns over 200ms on CSS's ease-out only as it is
	// clicked: opened or closed otherwise, it is already where it goes.
	turnTime := time.Millisecond
	if head.Clicked() {
		toggle()
		open, turnTime = !open, 200*time.Millisecond
	}
	// The body shows at once, as Lody's CollapsibleCard renders it.
	shown := b2f(open)
	hovered := head.Hovered()
	turn := head.AnimateWith("chevron", b2f(open), turnTime, cssEaseOut)
	// It fades in with the pointer over 150ms, Tailwind's
	// transition-opacity; the label keeps its ink.
	reveal := head.AnimateWith("reveal", b2f(hovered), 150*time.Millisecond, twEase)
	head.Children(func() {
		ink := p.Secondary
		if attention {
			ui.Icon(c, iconTriangleAlert).Size(12, 12).TextColor(p.Warning).Shrink(0)
		}
		shimmerIf(c, p, live, func() { ui.Text(c, label).FontSize(sizeSub).TextColor(ink).SingleLine().MinWidth(0) })
		if reveal > 0 {
			ui.Icon(c, iconChevronRight).Size(13, 13).TextColor(ink).Rotate(90 * turn).Opacity(reveal)
		}
	})
	return shown
}

// shimmerIf builds a label, with Lody's agent shimmer over it while live:
// a band of the page's color, 60% of the label wide, sliding across it
// every 2.2s, fading the glyphs it passes.
func shimmerIf(c *ui.Context, p *palette, live bool, label func()) {
	if !live {
		label()
		return
	}
	wrap := ui.Row(c.Key("shimmer")).MinWidth(0).Shrink(1).ClipX()
	k := wrap.Keyframes("sweep", []ui.Keyframe{{Value: 0}, {At: 2200 * time.Millisecond, Value: 1}}, ui.KeyframeOptions{Repeat: ui.RepeatForever})
	wrap.Children(func() {
		label()
		w := wrap.Bounds().W
		if w <= 0 {
			return
		}
		band := 0.6 * w
		clear := p.Canvas.Alpha(0)
		ui.Row(c.Key("band")).Absolute().Top(0).Bottom(0).Left(band * (-1 + 2.67*k)).Width(band).Children(func() {
			ui.Box(c).Grow(1).Basis(0).FillHeight().LinearGradient(ui.LinearGradient{From: clear, To: p.Canvas.Alpha(0.78), Angle: 90})
			ui.Box(c).Grow(1).Basis(0).FillHeight().LinearGradient(ui.LinearGradient{From: p.Canvas.Alpha(0.78), To: clear, Angle: 90})
		})
	})
}

// thought is one thinking step in a group: a collapsed row, its text shown
// only once clicked.
func (a *App) thought(c *ui.Context, p *palette, key string, it session.Item, live bool) {
	open := a.expanded[key]
	label := locales.Thought()
	switch {
	case live:
		label = locales.Thinking()
	case it.ThoughtFor >= time.Second:
		label = locales.ThoughtFor(formatDuration(it.ThoughtFor))
	}
	ui.Column(c).FillWidth().Gap(2).Padding(0, 4).Children(func() {
		shown := disclosure(c.Key(key), p, label, open, live, func() { a.expanded[key] = !a.expanded[key] })
		unfold(c, shown, open, func() {
			ui.Column(c).Padding(0, 0, 4, 0).Children(func() {
				id := strings.SplitN(key, "/", 2)[0] + "/" + it.ID
				a.findRing(p, id, markdown.View(c.Key(it.ID), a.findMarkdown(id, it.Text), &a.mdMuted))
			})
		})
	})
}

// activity is a collapsible group of steps, labeled by what they did.
func (a *App) activity(c *ui.Context, p *palette, e session.Entry, b block, live bool) {
	key := e.ID + "/" + b.key
	open := a.expanded[key]
	label := activityLabel(b.items, live)
	if live && e.StartedAt > 0 {
		label = locales.ActivityWithDuration(label, formatDuration(time.Since(time.UnixMilli(int64(e.StartedAt)))))
		c.After(time.Second)
	}
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		attention := false
		for _, it := range b.items {
			attention = attention || it.Status == "failed" || it.Permission != nil && it.Permission.Pending
		}
		shown := disclosureMarked(c.Key(key), p, label, open, live, attention, func() { a.expanded[key] = !a.expanded[key] })
		unfold(c, shown, open, func() {
			ui.Column(c).FillWidth().Gap(2).Children(func() {
				for i, it := range b.items {
					if it.Type == "thought" {
						a.thought(c.Key(it.ID), p, key+"/"+it.ID, it, live && i == len(b.items)-1)
						continue
					}
					a.step(c.Key(it.ID), p, key+"/"+it.ID, it)
				}
			})
		})
	})
}

func (a *App) step(c *ui.Context, p *palette, key string, it session.Item) {
	switch it.Type {
	case "plan":
		a.planBar(c, p, key, it.Plan)
	case "subagent_task":
		sa := it.Subagent
		ui.Row(c).MinHeight(24).Padding(2, 4).Gap(6).AlignItems(ui.Center).Children(func() {
			label := locales.Subagent()
			if sa != nil && sa.Description != "" {
				label += ": " + sa.Description
			}
			ui.Text(c, label).FontSize(sizeSub).TextColor(p.Secondary).SingleLine().MinWidth(0).Grow(1)
			if sa != nil && sa.Status != "" {
				ui.Text(c, sa.Status).FontSize(sizeCaption).TextColor(p.Tertiary)
			}
		})
	case "system_notice":
		a.systemNotice(c, p, session.Entry{}, it)
	case "goal":
		goalMarker(c, p, it)
	case "tool_call":
		open := a.expanded[key]
		title := firstNonEmpty(it.Title, it.Path, it.Kind)
		verb, rest := toolVerbParts(it, title)
		row := ui.Row(c).MinHeight(24).Padding(2, 4).Gap(6).AlignItems(ui.Center).Radius(5).Label(strings.TrimSpace(verb + " " + rest))
		shown := b2f(open)
		if it.HasDetail {
			row.Cursor(ui.CursorPointer).OnClick(func() { a.expanded[key] = !a.expanded[key] })
		}
		// As Lody's step, the title brightens under the pointer over 150ms
		// (Tailwind's transition-colors), with no chevron; its verb, a
		// shade under the ink, shimmers while the step runs, and a step
		// without one spins Lody's loader instead.
		inks := map[bool]ui.Color{true: p.Text, false: p.Secondary}
		ink := row.AnimateColor("ink", inks[row.Hovered()], ui.Animation{Duration: 150 * time.Millisecond, Ease: twEase})
		running := it.Status == "pending" || it.Status == "in_progress"
		row.Children(func() {
			ui.Row(c).MinWidth(0).Shrink(1).Gap(4).AlignItems(ui.Center).Children(func() {
				if verb != "" {
					shimmerIf(c, p, running, func() {
						ui.Text(c, verb).FontSize(sizeSub).TextColor(p.Text.Alpha(0.9)).SingleLine().Shrink(0)
					})
				}
				if rest != "" {
					ui.Text(c, rest).FontSize(sizeSub).TextColor(ink).SingleLine().MinWidth(0).Shrink(1)
				}
			})
			if it.Added > 0 || it.Removed > 0 {
				ui.Text(c, fmt.Sprintf("+%d", it.Added)).FontSize(sizeFoot).Font(fontMono).TextColor(p.DiffAdd).Shrink(0)
				ui.Text(c, fmt.Sprintf("-%d", it.Removed)).FontSize(sizeFoot).Font(fontMono).TextColor(p.DiffDel).Shrink(0)
			}
			if it.Status == "failed" {
				ui.Icon(c, iconCircleAlert).Size(12, 12).TextColor(p.Danger)
			}
			if running && verb == "" {
				loader(c, p)
			}
		})
		unfold(c, shown, open, func() { a.toolDetail(c, p, key, it) })
	default:
		ui.Text(c, it.Type).FontSize(sizeSub).TextColor(p.Tertiary).Padding(2, 4)
	}
}

// toolVerbs are the English verbs agents start their titles with, and the
// verbs in the app's language, by tool kind: in progress, then done.
var toolVerbs = map[string]struct {
	english     [2]string
	going, done func() string
}{
	"read":    {[2]string{"Reading", "Read"}, locales.VerbReading, locales.VerbRead},
	"edit":    {[2]string{"Editing", "Edited"}, locales.VerbEditing, locales.VerbEdited},
	"delete":  {[2]string{"Deleting", "Deleted"}, locales.VerbDeleting, locales.VerbDeleted},
	"move":    {[2]string{"Moving", "Moved"}, locales.VerbMoving, locales.VerbMoved},
	"search":  {[2]string{"Searching", "Searched"}, locales.VerbSearching, locales.VerbSearched},
	"execute": {[2]string{"Running", "Ran"}, locales.VerbRunning, locales.VerbRan},
	"fetch":   {[2]string{"Fetching", "Fetched"}, locales.VerbFetching, locales.VerbFetched},
	"think":   {[2]string{"Thinking", "Thought"}, locales.VerbThinking, locales.VerbThought},
}

// toolVerbParts splits a step's sentence into its verb, in the tense of
// its status, and what it acted on: the verb is the one toolVerb adds, or
// the one the title starts with; none for a step whose words carry no
// tense.
func toolVerbParts(it session.Item, title string) (verb, rest string) {
	if v := toolVerb(it); v != "" {
		return v, title
	}
	if forms, ok := toolVerbs[it.Kind]; ok {
		low := strings.ToLower(title)
		for _, w := range forms.english {
			n := len(w)
			if strings.HasPrefix(low, strings.ToLower(w)) && (len(title) == n || strings.ContainsRune(" :(", rune(title[n]))) {
				return title[:n], strings.TrimSpace(title[n:])
			}
		}
	}
	return "", title
}

// toolVerb names what a tool call does, in the tense of its status.
func toolVerb(it session.Item) string {
	v, ok := toolVerbs[it.Kind]
	if !ok {
		return ""
	}
	// Agents' titles often start with the verb already.
	title := strings.ToLower(it.Title)
	if strings.HasPrefix(title, strings.ToLower(v.english[0])) || strings.HasPrefix(title, strings.ToLower(v.english[1])) {
		return ""
	}
	if it.Status == "completed" || it.Status == "failed" {
		return v.done()
	}
	return v.going()
}

// activityLabel summarizes a group in Lody's words: how long it thought,
// then what its tools did.
func activityLabel(items []session.Item, live bool) string {
	counts := map[string]int{}
	other, thoughts := 0, 0
	var thought time.Duration
	for _, it := range items {
		switch it.Type {
		case "tool_call":
			counts[it.Kind]++
		case "thought":
			thoughts++
			thought += it.ThoughtFor
		default:
			other++
		}
	}
	var parts []string
	switch {
	case live && len(items) > 0 && items[len(items)-1].Type == "thought":
		parts = append(parts, locales.Thinking())
	case thoughts > 0 && thought >= time.Second:
		parts = append(parts, locales.ThoughtFor(formatDuration(thought)))
	case thoughts > 0:
		parts = append(parts, locales.Thought())
	}
	if n := counts["execute"]; n > 0 {
		parts = append(parts, locales.ToolCommands(n))
	}
	if n := counts["read"]; n > 0 {
		parts = append(parts, locales.ToolReadFiles(n))
	}
	if n := counts["edit"] + counts["delete"] + counts["move"]; n > 0 {
		parts = append(parts, locales.ToolEditedFiles(n))
	}
	if n := counts["search"]; n > 0 {
		parts = append(parts, locales.ToolSearches(n))
	}
	if n := counts["fetch"]; n > 0 {
		parts = append(parts, locales.ToolFetches(n))
	}
	if n := counts["other"] + counts["think"] + counts[""] + other; n > 0 {
		parts = append(parts, locales.ToolCalls(n))
	}
	if len(parts) == 0 && live {
		return locales.WorkingDots()
	}
	return strings.Join(parts, " · ")
}

// editedFiles is the card of a turn's file changes.
func (a *App) editedFiles(c *ui.Context, p *palette, e session.Entry) {
	diffs := e.FileDiffs
	key := e.ID + "/files"
	shown := diffs
	more := 0
	if len(diffs) > 4 {
		more = len(diffs) - 4
		if !a.expanded[key] {
			shown = diffs[:4]
		}
	}
	ui.Column(c).FillWidth().Radius(8).Border(1, p.Border).Background(p.Canvas).Clip().Children(func() {
		if len(diffs) > 1 {
			add, del := 0, 0
			for _, d := range diffs {
				add, del = add+d.Add, del+d.Del
			}
			ui.Row(c).Height(32).Padding(0, 10).Gap(8).AlignItems(ui.Center).Background(p.Secondary.Alpha(0.03)).BorderWidth(0, 0, 1, 0).BorderColor(p.Border).Children(func() {
				ui.Text(c, locales.ToolEditedFiles(len(diffs))).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).Grow(1)
				diffCounts(c, p, add, del)
			})
		}
		height := float32(40)
		if len(diffs) > 1 {
			height = 36
		}
		for _, d := range shown {
			dir, file := path.Split(d.Path)
			full := d.Path
			// A row fills under the pointer over 150ms, Tailwind's
			// transition-colors.
			row := ui.Row(c.Key(d.Path)).Height(height).Padding(0, 10).Gap(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Label(d.Path).Transition(twFade).OnClick(func() { a.openTurnDiff(e.ID, full) })
			if row.Hovered() {
				row.Background(p.Hover.Alpha(0.45))
			}
			row.Children(func() {
				ui.Text(c, file).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine().Shrink(0).MaxWidth(320)
				ui.Text(c, strings.TrimSuffix(dir, "/")).FontSize(sizeSub).TextColor(p.Tertiary).SingleLine().MinWidth(0).Grow(1)
				diffCounts(c, p, d.Add, d.Del)
			})
		}
		if more > 0 {
			// Show more and Show less, as Lody's: the chevron turns over
			// 150ms as the list opens, and the fill and ink brighten under
			// the pointer over 150ms, Tailwind's default curve for both.
			open := a.expanded[key]
			label := locales.EditedFilesShowMore(more)
			if open {
				label = locales.ShowLess()
			}
			row := ui.Row(c.Key("show-more")).Height(32).Padding(0, 10).Gap(4).AlignItems(ui.Center).Justify(ui.Center).
				BorderWidth(1, 0, 0, 0).BorderColor(p.Border.Alpha(0.7)).Cursor(ui.CursorPointer).Label(label).Transition(twFade).
				OnClick(func() { a.expanded[key] = !a.expanded[key] })
			hovered := row.Hovered()
			if hovered {
				row.Background(p.Hover.Alpha(0.45))
			}
			ink := row.AnimateColor("ink", map[bool]ui.Color{true: p.Text, false: p.Secondary}[hovered], ui.Animation{Duration: 150 * time.Millisecond, Ease: twEase})
			turn := row.AnimateWith("turn", b2f(open), 150*time.Millisecond, twEase)
			row.Children(func() {
				ui.Icon(c, iconChevronDown).Size(12, 12).TextColor(ink).Rotate(180 * turn)
				ui.Text(c, label).FontSize(sizeFoot).FontWeight(500).TextColor(ink)
			})
		}
	})
}

func diffCounts(c *ui.Context, p *palette, add, del int) {
	ui.Text(c, fmt.Sprintf("+%d", add)).Font(fontMono).FontSize(sizeFoot).TextColor(p.DiffAdd).Shrink(0)
	ui.Text(c, fmt.Sprintf("-%d", del)).Font(fontMono).FontSize(sizeFoot).TextColor(p.DiffDel).Shrink(0)
}

// turnFooter shows on hover: copy, then when the turn ended and how long
// it took.
func (a *App) turnFooter(c *ui.Context, p *palette, e session.Entry, hovered bool) {
	row := ui.Row(c).Height(24).Gap(4).AlignItems(ui.Center)
	// The reply's meta row fades in with the pointer over 150ms, as
	// Tailwind's transition-opacity.
	row.Opacity(row.AnimateWith("shown", b2f(hovered), 150*time.Millisecond, twEase))
	row.Children(func() {
		// Copied, the button wears a check for 1.2s, as Lody's.
		cm := &a.chatMotion
		icon, label := iconCopy, locales.Copy()
		if cm.copiedTurn == e.ID && time.Since(cm.copiedAt) < 1200*time.Millisecond {
			icon, label = iconCheck, locales.PlanCopied()
			c.After(1200*time.Millisecond - time.Since(cm.copiedAt))
		}
		iconButton(c, p, icon, label, p.Hover).Size(24, 24).OnClick(func() {
			var parts []string
			for _, b := range blocks(e.Items)[answerStart(blocks(e.Items)):] {
				parts = append(parts, b.items[0].Text)
			}
			mygo.Clipboard.WriteText(strings.Join(parts, "\n\n"))
			cm.copiedTurn, cm.copiedAt = e.ID, time.Now()
		})
		a.messageActions(c, p, e)
		if a.forkable(e) {
			a.forkButton(c, p, e)
		}
		var meta []string
		if e.EndedAt > 0 {
			meta = append(meta, locales.MessageToday(time.UnixMilli(int64(e.EndedAt)).Local()))
		}
		if d := turnDuration(e); d > 0 {
			meta = append(meta, formatDuration(d))
		}
		ui.Text(c, strings.Join(meta, " · ")).FontSize(sizeCaption).TextColor(p.Tertiary)
	})
}

// liveStatus is the last row: what the agent is doing right now.
func (a *App) liveStatus(c *ui.Context, p *palette, v *session.View) {
	a.column(c, "status", func() {
		if !v.Working || len(v.Entries) == 0 {
			return
		}
		last := v.Entries[len(v.Entries)-1]
		if last.Role == "assistant" && !last.Finished {
			return // the reply shows its own progress
		}
		ui.Row(c).Padding(36, 4, 0, 4).Children(func() {
			shimmerIf(c, p, true, func() { ui.Text(c, locales.WorkingDots()).FontSize(sizeSub).TextColor(p.Secondary) })
		})
	})
}
