package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// The chat's session actions, as Lody's: edit the last message and send
// it again, fork a conversation from a reply, steer the agent's goal, and
// end an agent process from the machine's page.

// messageEdit is the last user message being rewritten.
type messageEdit struct {
	id   string
	text string
	busy bool
}

// forkPending is a fork the machine is making.
type forkPending struct {
	source, turn, target string
	worktree             bool
}

// sessionForkable is whether the session's agent can start again from a
// reply: its runtime said so, for the config the session runs.
func (a *App) sessionForkable(sess *catalog.Session) bool {
	if sess == nil || sess.Archived || sess.AgentConfigID == "" {
		return false
	}
	found := false
	for _, ag := range a.catalog.Agents[sess.MachineID] {
		if ag.ID == sess.AgentConfigID && ag.CLIType == sess.CLIType && ag.AgentType == sess.AgentType {
			found = true
		}
	}
	return found && a.catalog.Capabilities[sess.AgentConfigID].Fork
}

// goalActive is whether the session pursues a goal now, when Lody keeps
// its messages as they are.
func (a *App) goalActive() bool {
	return a.view != nil && a.view.Goal != nil && a.view.Goal.Status == "active"
}

// editableEntry is the user message Edit applies to, if any: the last,
// in a Claude or Codex session that can restart before it.
func (a *App) editableEntry() *session.Entry {
	sess := a.catalog.Session(a.selected)
	if sess == nil || a.view == nil || sess.Archived || a.goalActive() || sess.CLIType != "builtin" ||
		(sess.AgentType != "codex" && sess.AgentType != "claude") {
		return nil
	}
	return session.EditableUser(a.view.Entries, a.sessionForkable(sess))
}

func (a *App) startEdit(e session.Entry) {
	a.msgEdit = messageEdit{id: e.ID, text: e.Text()}
}

// submitEdit replaces the message and runs its turn again.
func (a *App) submitEdit() {
	ed := &a.msgEdit
	sess := a.catalog.Session(a.selected)
	target := a.editableEntry()
	text := strings.TrimSpace(ed.text)
	if sess == nil || a.replica == nil || target == nil || target.ID != ed.id || text == "" || ed.busy || a.account == nil {
		return
	}
	ed.busy = true
	r, machine, user, entry := a.replica, sess.MachineID, a.account.User.ID, *target
	cliType, agentType := sess.CLIType, sess.AgentType
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 125*time.Second)
		defer cancel()
		err := r.EditAndResend(ctx, machine, user, cliType, agentType, entry, text)
		a.update(func() {
			ed.busy = false
			if err != nil {
				a.sendError = editFailed(err)
				return
			}
			a.msgEdit = messageEdit{}
		})
	}()
}

func editFailed(err error) string {
	var op *session.OpError
	if errors.As(err, &op) && op.Message != "" {
		return op.Message
	}
	return locales.EditResendFailed()
}

// messageEditor replaces the bubble while the message is rewritten.
func (a *App) messageEditor(c *ui.Context, p *palette) {
	ed := &a.msgEdit
	ui.Column(c).FillWidth().MinWidth(320).Gap(8).Padding(10, 12).Radius(14).Background(p.UserBubble).Border(1, p.Border).Children(func() {
		area(c, p, &ed.text, "", locales.EditMessage(), 3).AutoFocus()
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if ed.busy {
				spinner(c).Size(13, 13)
			}
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() {
				if !ed.busy {
					a.msgEdit = messageEdit{}
				}
			})
			primaryButton(c, p, locales.Send(), strings.TrimSpace(ed.text) != "" && !ed.busy).OnClick(a.submitEdit)
		})
	})
}

// forkable is whether a reply can start a fork: finished, and either
// the last one or one the agent can restart from.
func (a *App) forkable(e session.Entry) bool {
	sess := a.catalog.Session(a.selected)
	if !e.Finished || !a.sessionForkable(sess) || a.view == nil {
		return false
	}
	if e.AcpTurnID != "" {
		return true
	}
	for i := len(a.view.Entries) - 1; i >= 0; i-- {
		if x := a.view.Entries[i]; x.Role == "assistant" && x.Finished {
			return x.ID == e.ID
		}
	}
	return false
}

// forkButton is the reply's fork: to a new tab in the same directory, or
// to a new worktree when the session works in a project.
func (a *App) forkButton(c *ui.Context, p *palette, e session.Entry) {
	sess := a.catalog.Session(a.selected)
	if sess == nil {
		return
	}
	if f, ok := a.forks[sess.ID]; ok && f.turn == e.ID {
		ui.Box(c).Size(24, 24).Center().Children(func() { spinner(c).Size(12, 12) })
		return
	}
	turn := e.ID
	worktree := sess.ProjectID != ""
	b := iconButton(c, p, iconGitFork, locales.ForkSession(), p.Hover).Size(24, 24)
	if !worktree {
		b.OnClick(func() { a.fork(turn, false, false, "") })
		return
	}
	b.Menu(func(m *ui.Menu) {
		if m.Item(locales.ForkNewTab()).Chosen() {
			a.fork(turn, false, false, "")
		}
		if m.Item(locales.ForkNewWorktree()).Chosen() {
			a.fork(turn, true, false, "")
		}
	})
}

// fork asks the machine for a new session from a reply. A worktree fork
// over uncommitted changes asks first; target carries the session it was
// to make into the confirmed retry.
func (a *App) fork(turn string, worktree, dirtyOK bool, target string) {
	sess := a.catalog.Session(a.selected)
	if sess == nil || a.replica == nil || a.account == nil {
		return
	}
	if a.forks == nil {
		a.forks = map[string]forkPending{}
	}
	if _, busy := a.forks[sess.ID]; busy && target == "" {
		return
	}
	pending := forkPending{source: sess.ID, turn: turn, target: target, worktree: worktree}
	a.forks[sess.ID] = pending
	r, machine, user, source := a.replica, sess.MachineID, a.account.User.ID, sess.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 130*time.Second)
		defer cancel()
		res, err := r.Fork(ctx, machine, user, turn, worktree, target, dirtyOK)
		a.update(func() {
			delete(a.forks, source)
			switch {
			case err != nil:
				var op *session.OpError
				if errors.As(err, &op) && op.Message != "" {
					a.notice = op.Message
				} else {
					a.notice = locales.ForkFailed()
				}
			case res.Confirm:
				a.confirm(locales.ForkDirtyTitle(), locales.ForkDirtyBody(), locales.ForkDirtyConfirm(), func() {
					a.fork(turn, true, true, res.Target)
				})
			default:
				if res.Partial {
					a.notice = locales.ForkPartial()
				}
				a.forkOpen = res.Target
			}
		})
	}()
}

// openForked shows a fork once the workspace lists it.
func (a *App) openForked() {
	if a.forkOpen != "" && a.catalog.Session(a.forkOpen) != nil {
		id := a.forkOpen
		a.forkOpen = ""
		a.open(id)
	}
}

// goalStatus is how Lody names a goal's state, and whether it is warm,
// done or quiet.
func goalStatus(status string) (string, string) {
	switch status {
	case "paused":
		return locales.GoalPaused(), "warning"
	case "budgetLimited":
		return locales.GoalUnmet(), "warning"
	case "blocked":
		return locales.GoalBlocked(), "warning"
	case "usageLimited":
		return locales.GoalUsageLimited(), "warning"
	case "complete":
		return locales.GoalAchieved(), "success"
	case "cleared":
		return locales.GoalCleared(), "muted"
	}
	return locales.GoalActive(), "info"
}

// goalCommands are the commands the session's agent takes for its goal.
func (a *App) goalCommands() []string {
	sess := a.catalog.Session(a.selected)
	if sess == nil || sess.AgentConfigID == "" {
		return nil
	}
	var out []string
	for _, cmd := range []string{"pause", "resume", "clear"} {
		for _, have := range a.catalog.Capabilities[sess.AgentConfigID].GoalActions {
			if have == cmd {
				out = append(out, cmd)
			}
		}
	}
	return out
}

// goalCommand pauses, resumes or clears the goal.
func (a *App) goalCommand(cmd string) {
	sess := a.catalog.Session(a.selected)
	if sess == nil || a.replica == nil || a.account == nil || a.goalPending != "" {
		return
	}
	a.goalPending = cmd
	r, machine, user := a.replica, sess.MachineID, a.account.User.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		_, err := r.Goal(ctx, machine, user, cmd, "")
		a.update(func() {
			if err != nil {
				a.goalPending = ""
				a.notice = locales.GoalCommandError() + ": " + err.Error()
			}
		})
	}()
}

// goalBanner sits over the conversation while the session has a goal: its
// state, what it spent, its objective, and its commands.
func (a *App) goalBanner(c *ui.Context, p *palette) {
	if a.view == nil || a.view.Goal == nil {
		return
	}
	g := a.view.Goal
	if g.Status == "cleared" && a.goalDismissed[a.selected] == g.ThreadID {
		return
	}
	// A command settles when the agent's snapshot moves.
	if a.goalPending != "" && a.goalSeen != g.ThreadID+"/"+g.Status {
		a.goalPending = ""
	}
	a.goalSeen = g.ThreadID + "/" + g.Status
	label, tone := goalStatus(g.Status)
	ink := map[string]ui.Color{"info": p.Primary, "warning": p.Warning, "success": p.Success, "muted": p.Secondary}[tone]
	key := a.selected + "/goal"
	open := a.expanded[key]
	ui.Column(c).FillWidth().Padding(10, 16).Gap(6).Background(ink.Alpha(0.08)).BorderWidth(0, 0, 1, 0).BorderColor(ink.Alpha(0.3)).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			icon := ui.Icon(c.Key("goal-icon"), iconTarget).Size(14, 14).TextColor(ink)
			if tone == "info" {
				pulse(icon)
			}
			ui.Text(c, "Goal").FontSize(sizeFoot).FontWeight(600).TextColor(p.Text)
			ui.Text(c, label).FontSize(sizeCaption).FontWeight(500).TextColor(ink).Padding(1, 7).Radius(9).Background(ink.Alpha(0.12))
			var metrics []string
			if g.Seconds > 0 {
				metrics = append(metrics, formatDuration(time.Duration(g.Seconds)*time.Second))
			}
			if g.TokensUsed > 0 && g.TokenBudget > 0 {
				metrics = append(metrics, locales.GoalTokensBudget(tokensCompact(g.TokensUsed), tokensCompact(g.TokenBudget)))
			} else if g.TokensUsed > 0 {
				metrics = append(metrics, locales.GoalTokens(tokensCompact(g.TokensUsed)))
			}
			ui.Text(c, strings.Join(metrics, " · ")).FontSize(sizeCaption).TextColor(p.Secondary).Grow(1).SingleLine()
			commands := a.goalCommands()
			button := func(cmd, text string, icon *ui.SVG) {
				b := outlineButton(c, p, icon, text)
				if a.goalPending != "" {
					b.Disabled(true).Opacity(0.5)
				} else {
					b.OnClick(func() { a.goalCommand(cmd) })
				}
			}
			has := func(cmd string) bool {
				for _, x := range commands {
					if x == cmd {
						return true
					}
				}
				return false
			}
			if a.goalPending != "" {
				spinner(c).Size(12, 12)
			}
			if g.Status == "active" && has("pause") {
				button("pause", locales.GoalPause(), iconPause)
			}
			if (g.Status == "paused" || g.Status == "blocked") && has("resume") {
				button("resume", locales.GoalResume(), iconPlay)
			}
			if g.Status != "cleared" && g.Status != "complete" && has("clear") {
				button("clear", locales.GoalClear(), iconX)
			}
			if g.Status == "cleared" || g.Status == "complete" {
				thread, sel := g.ThreadID, a.selected
				iconButton(c, p, iconX, locales.GoalDismiss(), p.Hover).Size(24, 24).OnClick(func() {
					if a.goalDismissed == nil {
						a.goalDismissed = map[string]string{}
					}
					a.goalDismissed[sel] = thread
				})
			}
		})
		objective := ui.Text(c, strings.TrimSpace(g.Objective)).FontSize(sizeSub).LineHeight(1.45).TextColor(p.Text).Selectable().
			Cursor(ui.CursorPointer).OnClick(func() { a.expanded[key] = !a.expanded[key] })
		if !open {
			objective.MaxLines(1)
		}
	})
}

// tokensCompact writes a token count as 12.4K or 1.2M.
func tokensCompact(v float64) string {
	trim := func(s string) string { return strings.TrimSuffix(strings.TrimSuffix(s, ".0"), ".00") }
	digits := func(x float64) int {
		switch {
		case x >= 100:
			return 0
		case x >= 10:
			return 1
		}
		return 2
	}
	switch {
	case v >= 1e6:
		return trim(fmt.Sprintf("%.*f", digits(v/1e6), v/1e6)) + "M"
	case v >= 1e3:
		return trim(fmt.Sprintf("%.*f", digits(v/1e3), v/1e3)) + "K"
	}
	return fmt.Sprintf("%.0f", v)
}

// goalMarker is a goal's place in the timeline.
func goalMarker(c *ui.Context, p *palette, it session.Item) {
	if it.Goal == nil {
		return
	}
	label, tone := goalStatus(it.Goal.Status)
	ink := map[string]ui.Color{"info": p.Primary, "warning": p.Warning, "success": p.Success, "muted": p.Secondary}[tone]
	ui.Row(c).MinHeight(24).Padding(2, 4).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconTarget).Size(13, 13).TextColor(ink)
		ui.Text(c, label).FontSize(sizeSub).TextColor(ink).Shrink(0)
		ui.Text(c, strings.TrimSpace(it.Goal.Objective)).FontSize(sizeSub).TextColor(p.Secondary).SingleLine().MinWidth(0)
	})
}

// sessionProcesses lists the agent processes a machine runs, each with
// its use and a way to end it.
func (a *App) sessionProcesses(c *ui.Context, p *palette, machine string) {
	m := a.monitors[machine]
	if m == nil || m.sample == nil {
		return
	}
	list := m.sample.SessionList
	ui.Column(c).FillWidth().Gap(2).Padding(4, 0).Children(func() {
		if len(list) == 0 {
			ui.Text(c, locales.MonitorNoSessions()).FontSize(sizeFoot).TextColor(p.Secondary).Padding(6, 4)
			return
		}
		for _, s := range list {
			proc := s
			title := locales.MonitorUntitled()
			if sess := a.catalog.Session(s.SessionID); sess != nil && sess.Title != "" {
				title = sess.Title
			}
			ui.Row(c.Key(s.SessionID)).FillWidth().MinHeight(34).Padding(4, 8).Gap(10).AlignItems(ui.Center).Radius(6).Children(func() {
				ui.Text(c, title).FontSize(sizeSub).TextColor(p.Text).Grow(1).MinWidth(0).SingleLine()
				ui.Text(c, proc.Status).FontSize(sizeCaption).TextColor(p.Secondary).Shrink(0)
				ui.Text(c, cpuText(proc.Resource.CPUCores)).FontSize(sizeCaption).Font(fontMono).TextColor(p.Secondary).Width(48).Shrink(0)
				ui.Text(c, bytesText(proc.Resource.MemoryBytes)).FontSize(sizeCaption).Font(fontMono).TextColor(p.Secondary).Width(64).Shrink(0)
				iconButton(c, p, iconCircleStop, locales.MonitorTerminate(), p.Hover).Size(24, 24).OnClick(func() {
					a.confirm(locales.MonitorTerminateTitle(), locales.MonitorTerminateBody(), locales.MonitorTerminateAction(), func() {
						a.terminateProcess(machine, proc.SessionID)
					})
				})
			})
		}
		if m.sample.Truncated {
			ui.Text(c, locales.MonitorTruncated()).FontSize(sizeCaption).TextColor(p.Tertiary).Padding(4, 8)
		}
	})
}

func (a *App) terminateProcess(machine, sessionID string) {
	conn := a.conn
	if conn == nil {
		a.notice = locales.MonitorTerminateUnavailable()
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		err := session.Terminate(ctx, conn.MachineRPC, machine, sessionID)
		a.update(func() {
			if err != nil {
				a.notice = locales.MonitorTerminateFailed() + ": " + err.Error()
			}
		})
	}()
}

// checkLive asks the machine whether the session's agent still runs,
// after a Stop found nothing to stop.
func (a *App) checkLive() {
	sess := a.catalog.Session(a.selected)
	if sess == nil || a.replica == nil {
		return
	}
	r, machine := a.replica, sess.MachineID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		state, err := r.LiveStatus(ctx, machine)
		a.update(func() {
			if err == nil && state == "idle" {
				a.sendError = locales.AgentIdle()
			}
		})
	}()
}

// pingState is a machine's last round trip.
type pingState struct {
	at      time.Time
	busy    bool
	latency time.Duration
	failed  bool
}

// machinePing shows how long a request takes to reach the machine and
// come back (machine/ping), asked again every half minute while shown.
func (a *App) machinePing(c *ui.Context, p *palette, machine string) {
	if a.conn == nil || !a.presence.IsOnline(machine) {
		return
	}
	if a.pings == nil {
		a.pings = map[string]*pingState{}
	}
	st := a.pings[machine]
	if st == nil {
		st = &pingState{}
		a.pings[machine] = st
	}
	c.After(30 * time.Second)
	if !st.busy && time.Since(st.at) > 30*time.Second {
		st.busy, st.at = true, time.Now()
		conn := a.conn
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			start := time.Now()
			raw, err := conn.MachineRPC(ctx, machine, "machine/ping", map[string]any{"requestId": uuid.NewString()}, 15*time.Second)
			var res struct {
				Success bool `json:"success"`
			}
			if err == nil {
				err = json.Unmarshal(raw, &res)
			}
			took := time.Since(start)
			a.update(func() {
				st.busy, st.latency, st.failed = false, took, err != nil || !res.Success
			})
		}()
	}
	if st.latency == 0 && !st.failed {
		return
	}
	text := locales.MachinePing(int(st.latency.Milliseconds()))
	if st.failed {
		text = locales.MachinePingFailed()
	}
	ui.Text(c, text).FontSize(sizeCaption).TextColor(p.Tertiary).Padding(0, 4)
}
