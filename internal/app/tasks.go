package app

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// The tasks an agent starts, subagents and background commands, show in
// one card per turn, as Lody's subagent task panel: a summary line while
// they run ("Waiting on 2 background tasks"), a row per task, and each
// task's run in a dialog.

// collectTasks gathers a turn's tasks in first-seen order, the latest
// state of each, leaving out housekeeping the agent marks to skip.
func collectTasks(items []session.Item) []*session.Subagent {
	var order []string
	byID := map[string]*session.Subagent{}
	for _, it := range items {
		if it.Type != "subagent_task" || it.Subagent == nil || it.Subagent.SkipTranscript {
			continue
		}
		if _, ok := byID[it.Subagent.TaskID]; !ok {
			order = append(order, it.Subagent.TaskID)
		}
		byID[it.Subagent.TaskID] = it.Subagent
	}
	out := make([]*session.Subagent, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return orderAsTree(out)
}

// orderAsTree puts runs a subagent started under it, at most two deep.
func orderAsTree(tasks []*session.Subagent) []*session.Subagent {
	ids := map[string]bool{}
	for _, t := range tasks {
		ids[t.TaskID] = true
	}
	children := map[string][]*session.Subagent{}
	var roots []*session.Subagent
	for _, t := range tasks {
		if p := t.ParentTaskID; p != "" && p != t.TaskID && ids[p] {
			children[p] = append(children[p], t)
		} else {
			roots = append(roots, t)
		}
	}
	var out []*session.Subagent
	placed := map[string]bool{}
	var place func(t *session.Subagent)
	place = func(t *session.Subagent) {
		if placed[t.TaskID] {
			return
		}
		placed[t.TaskID] = true
		out = append(out, t)
		for _, child := range children[t.TaskID] {
			place(child)
		}
	}
	for _, t := range roots {
		place(t)
	}
	return out
}

func taskActor(t *session.Subagent) string {
	if t.Bash() {
		return locales.TaskBash()
	}
	return firstNonEmpty(t.Actor, t.SubagentType, locales.TaskDefault())
}

func taskStateLabel(state string) string {
	switch state {
	case "cancelled":
		return locales.TaskCancelled()
	case "completed":
		return locales.TaskCompleted()
	case "failed":
		return locales.TaskFailed()
	case "pending":
		return locales.TaskPending()
	case "running":
		return locales.TaskRunning()
	}
	return locales.TaskUnknown()
}

// latestAction is what a running task is doing: its last step, its
// progress, or the tool it last ran.
func latestAction(t *session.Subagent) string {
	if !t.Running() {
		return ""
	}
	for i := len(t.Items) - 1; i >= 0; i-- {
		switch it := t.Items[i]; it.Type {
		case "tool_call":
			if title := firstNonEmpty(it.Title, it.Kind); title != "" {
				return strings.TrimSpace(toolVerb(it) + " " + title)
			}
		case "thought":
			return locales.TaskThinking()
		case "plan":
			return locales.TaskPlanUpdated()
		case "text":
			if line := lastLine(it.Text); line != "" {
				return line
			}
		}
	}
	if s := strings.TrimSpace(t.Progress); s != "" {
		return s
	}
	if s := strings.TrimSpace(t.Summary); s != "" && !strings.Contains(s, strings.TrimSpace(t.Description)) {
		return s
	}
	if t.LastTool != "" {
		return locales.TaskRunningTool(t.LastTool)
	}
	return ""
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(strings.TrimLeft(lines[i], "#>-* ")); l != "" {
			return l
		}
	}
	return ""
}

// taskPanel is a turn's card of tasks.
func (a *App) taskPanel(c *ui.Context, p *palette, e session.Entry, tasks []*session.Subagent) {
	running, background := 0, 0
	for _, t := range tasks {
		if t.Running() {
			running++
		}
		if t.Background {
			background++
		}
	}
	allBackground := background == len(tasks)
	var label string
	switch {
	case allBackground && running > 0:
		label = locales.TasksWaitingBackground(running)
	case allBackground:
		label = locales.TasksCountBackground(len(tasks))
	case running > 0:
		label = locales.TasksWaiting(running)
	default:
		label = locales.TasksCount(len(tasks))
	}
	if !allBackground && background > 0 {
		label += " · " + locales.TasksInBackground(background)
	}
	key := e.ID + "/tasks"
	// Open while anything runs; once all settle, it folds to its summary.
	expanded := running > 0 || a.expanded[key]
	card := ui.Column(c.Key(key)).FillWidth().Radius(10).Border(1, p.Border).Background(p.Composer).Clip()
	card.Children(func() {
		head := ui.Row(c).Height(34).Padding(0, 12).Gap(8).AlignItems(ui.Center)
		if running == 0 {
			head.Cursor(ui.CursorPointer).Label(label).OnClick(func() { a.expanded[key] = !a.expanded[key] })
		}
		head.Children(func() {
			ui.Box(c).Size(14, 14).Center().Shrink(0).Children(func() {
				if running > 0 {
					spinner(c).Size(13, 13)
					return
				}
				icon := iconChevronRight
				if expanded {
					icon = iconChevronDown
				}
				ui.Icon(c, icon).Size(13, 13).TextColor(p.Secondary)
			})
			ui.Text(c, label).FontSize(sizeSub).TextColor(p.Secondary).SingleLine()
		})
		if !expanded {
			return
		}
		ui.Column(c).FillWidth().BorderWidth(1, 0, 0, 0).BorderColor(p.Border).Dividers(1, p.Border).Gap(1).Children(func() {
			depth := map[string]int{}
			for _, t := range tasks {
				d := 0
				if parent, ok := depth[t.ParentTaskID]; ok && t.ParentTaskID != "" {
					d = min(parent+1, 2)
				}
				depth[t.TaskID] = d
				a.taskRow(c.Key(t.TaskID), p, e, t, d)
			}
		})
	})
	a.taskDialog(c, p)
}

func (a *App) taskRow(c *ui.Context, p *palette, e session.Entry, t *session.Subagent, depth int) {
	row := ui.Column(c).FillWidth().Padding(7, 12, 7, 12+float32(depth)*18).Gap(2).Cursor(ui.CursorPointer).
		Label(taskActor(t) + " · " + t.Description).Transition(colorFade)
	if row.Hovered() {
		row.Background(p.Hover)
	}
	entryID, taskID := e.ID, t.TaskID
	row.OnClick(func() { a.taskOpen, a.taskEntry, a.taskID = true, entryID, taskID })
	row.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			taskMark(c, p, t.State)
			ui.Text(c, taskActor(t)).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).Shrink(0)
			if t.Description != "" {
				ui.Text(c, "·").FontSize(sizeSub).TextColor(p.Tertiary).Shrink(0)
				ui.Text(c, t.Description).FontSize(sizeSub).TextColor(p.Secondary).SingleLine().Grow(1).MinWidth(0)
			} else {
				ui.Spacer(c)
			}
			// How long so far, how long it took, or how it ended badly.
			var meta string
			ink := p.Tertiary
			switch {
			case t.Running() && t.Started > 0:
				meta = formatDuration(time.Since(time.Unix(int64(t.Started), 0)))
				c.After(time.Second)
			case t.State == "failed":
				meta, ink = taskStateLabel(t.State), p.Danger
			case t.State == "cancelled" || t.State == "unknown":
				meta = taskStateLabel(t.State)
			case t.Started > 0 && t.Ended >= t.Started:
				meta = formatDuration(time.Duration((t.Ended - t.Started) * float64(time.Second)))
			}
			if meta != "" {
				ui.Text(c, meta).FontSize(sizeFoot).TextColor(ink).Shrink(0)
			}
		})
		if latest := latestAction(t); latest != "" {
			ui.Text(c, latest).FontSize(sizeFoot).TextColor(p.Tertiary).SingleLine().Padding(0, 0, 0, 20)
		}
	})
}

// taskMark is a task's state: a spinner while it runs.
func taskMark(c *ui.Context, p *palette, state string) {
	ui.Box(c).Size(14, 14).Center().Shrink(0).Children(func() {
		switch state {
		case "running", "pending":
			spinner(c).Size(13, 13)
		case "completed":
			ui.Icon(c, iconCircleCheck).Size(14, 14).TextColor(p.Success)
		case "failed":
			ui.Icon(c, iconCircleAlert).Size(14, 14).TextColor(p.Danger)
		default:
			ui.Icon(c, iconCircle).Size(14, 14).TextColor(p.Tertiary)
		}
	})
}

// taskDialog shows a task's brief, outcome and run.
func (a *App) taskDialog(c *ui.Context, p *palette) {
	if !a.taskOpen || a.view == nil {
		return
	}
	var task *session.Subagent
	for _, e := range a.view.Entries {
		if e.ID != a.taskEntry {
			continue
		}
		for _, t := range collectTasks(e.Items) {
			if t.TaskID == a.taskID {
				task = t
			}
		}
	}
	if task == nil {
		return
	}
	w, h := c.Size()
	ui.DialogBase(c, &a.taskOpen, func(backdrop, panel ui.Element) {
		sheetIn(backdrop, panel)
		backdrop.Background(ui.RGBA(0, 0, 0, 0.28))
		panel.Size(min(720, w-80), min(720, h-80)).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Clip().
			Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Column(c).Fill().Children(func() {
			ui.Row(c).Height(52).Shrink(0).Padding(0, 12, 0, 20).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.Border).Children(func() {
				taskMark(c, p, task.State)
				ui.Text(c, taskActor(task)).FontSize(sizeBody).FontWeight(600).TextColor(p.Strong).Shrink(0)
				ui.Text(c, taskStateLabel(task.State)).FontSize(sizeFoot).TextColor(p.Secondary).Grow(1)
				iconButton(c, p, iconX, locales.Close(), p.Hover).OnClick(func() { a.taskOpen = false })
			})
			ui.Scroll(c).Grow(1).MinHeight(0).Padding(16, 20, 24, 20).Gap(16).Children(func() {
				section := func(title, body string, ink ui.Color) {
					if strings.TrimSpace(body) == "" {
						return
					}
					ui.Column(c).Gap(6).Children(func() {
						ui.Text(c, title).FontSize(sizeFoot).FontWeight(500).TextColor(p.Secondary)
						ui.Text(c, body).FontSize(sizeSub).LineHeight(1.5).TextColor(ink).Selectable()
					})
				}
				section(locales.TaskBrief(), task.Description, p.Text)
				section(locales.TaskResult(), task.Summary, p.Text)
				section(locales.TaskError(), task.Error, p.Danger)
				if task.State == "unknown" {
					ui.Text(c, locales.TaskUnknownExplanation()).FontSize(sizeFoot).TextColor(p.Secondary)
				}
				ui.Column(c).Gap(6).Children(func() {
					ui.Text(c, locales.TaskActivity()).FontSize(sizeFoot).FontWeight(500).TextColor(p.Secondary)
					if len(task.Items) == 0 {
						ui.Text(c, locales.TaskNoSteps()).FontSize(sizeSub).TextColor(p.Tertiary)
					}
					for _, it := range task.Items {
						key := "task/" + task.TaskID + "/" + it.ID
						switch it.Type {
						case "text":
							markdown.View(c.Key(key), it.Text, &a.md)
						case "thought":
							markdown.View(c.Key(key), it.Text, &a.mdMuted)
						default:
							a.step(c.Key(key), p, key, it)
						}
					}
				})
			})
		})
	})
}
