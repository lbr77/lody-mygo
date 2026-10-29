package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// The chat's session actions, as Lody's: edit the last message and send
// it again, fork a conversation from a reply, steer the agent's goal, and
// end an agent process from the machine's page.

// messageEdit is the last user message being rewritten: its text, and its
// attachments as the edit leaves them, those it had and files added;
// progress is how far each file added has uploaded, by path, while the
// edit is sent.
type messageEdit struct {
	id          string
	text        string
	busy        bool
	attachments []editAttachment
	progress    map[string]float32
}

// editAttachment is one of the edited message's attachments: one it had,
// kept by its edit id, or a file added and, once uploaded, its block,
// which sending again does not upload twice.
type editAttachment struct {
	original *session.EditAttachment
	file     pendingFile
	ready    map[string]any
}

// key tells the attachment apart among the editor's.
func (at editAttachment) key() string {
	if at.original != nil {
		return at.original.ID
	}
	return at.file.Path
}

// editResend replaces the last user message through its machine; tests
// swap it.
var editResend = func(ctx context.Context, r *session.Replica, machine, user, cliType, agentType string, u session.Entry, e session.Edit) error {
	return r.EditAndResend(ctx, machine, user, cliType, agentType, u, e)
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

// startEdit opens the editor on the message: its text, and the pictures
// and files it carries, each of which the edit may leave out.
func (a *App) startEdit(e session.Entry) {
	ed := messageEdit{id: e.ID, text: e.Text()}
	for _, at := range session.EditAttachments(e) {
		ed.attachments = append(ed.attachments, editAttachment{original: &at})
	}
	a.msgEdit = ed
}

// addEditFiles adds files to the message being rewritten, within Lody's
// limits for the attachments it keeps and those added.
func (a *App) addEditFiles(paths []string) {
	ed := &a.msgEdit
	images, files := 0, 0
	for _, at := range ed.attachments {
		if at.original != nil && at.original.Item.Type == "image" || at.original == nil && at.file.Image {
			images++
		} else {
			files++
		}
	}
	for _, f := range a.admitFiles(images, files, paths) {
		ed.attachments = append(ed.attachments, editAttachment{file: f})
	}
}

// pickEditFiles opens the system's file dialog for the message being
// rewritten, off the main thread as it waits for the user.
func (a *App) pickEditFiles() {
	win, id := a.win, a.msgEdit.id
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: locales.AttachFiles(), Multiple: true})
		if err != nil || len(paths) == 0 {
			return
		}
		a.update(func() {
			if a.msgEdit.id == id && !a.msgEdit.busy {
				a.addEditFiles(paths)
			}
		})
	}()
}

// removeEditAttachment leaves an attachment out of the edit.
func (a *App) removeEditAttachment(key string) {
	ed := &a.msgEdit
	if ed.busy {
		return
	}
	for i, at := range ed.attachments {
		if at.key() == key {
			ed.attachments = append(ed.attachments[:i:i], ed.attachments[i+1:]...)
			return
		}
	}
}

// submitEdit replaces the message and runs its turn again, as Lody's
// editSession: the files added upload to the conversation first, one at a
// time, then the edit names the attachments it keeps and adds the blocks
// of those uploaded. A file that fails stops it with the editor open.
func (a *App) submitEdit() {
	ed := &a.msgEdit
	sess := a.catalog.Session(a.selected)
	target := a.editableEntry()
	text := strings.TrimSpace(ed.text)
	if sess == nil || a.replica == nil || target == nil || target.ID != ed.id || text == "" && len(ed.attachments) == 0 || ed.busy || a.account == nil {
		return
	}
	ed.busy, ed.progress = true, map[string]float32{}
	var retained []string
	for _, at := range ed.attachments {
		if at.original != nil {
			retained = append(retained, at.original.ID)
		}
	}
	all := append([]editAttachment(nil), ed.attachments...)
	r, machine, user, entry, id, sessionID := a.replica, sess.MachineID, a.account.User.ID, *target, ed.id, sess.ID
	cliType, agentType := sess.CLIType, sess.AgentType
	workspace := ""
	if a.conn != nil {
		workspace = a.conn.ID
	}
	// still reports whether the editor is the one this edit was sent from.
	still := func() bool { return a.msgEdit.id == id && a.msgEdit.busy }
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		var blocks []map[string]any
		for _, at := range all {
			if at.original != nil {
				continue
			}
			block, path := at.ready, at.file.Path
			if block == nil {
				var err error
				block, err = uploadAttachment(ctx, a, workspace, sessionID, at.file, func(sent, total int64) {
					if total > 0 {
						pct := float32(sent) * 100 / float32(total)
						a.update(func() {
							if still() {
								a.msgEdit.progress[path] = pct
							}
						})
					}
				})
				if err != nil {
					name := at.file.Name
					a.update(func() {
						if still() {
							a.msgEdit.busy, a.msgEdit.progress = false, nil
						}
						a.sendError = name + ": " + err.Error()
					})
					return
				}
			}
			blocks = append(blocks, block)
			a.update(func() {
				if !still() {
					return
				}
				for i := range a.msgEdit.attachments {
					if x := &a.msgEdit.attachments[i]; x.original == nil && x.file.Path == path {
						x.ready = block
					}
				}
				a.msgEdit.progress[path] = 100
			})
		}
		editCtx, cancelEdit := context.WithTimeout(ctx, 125*time.Second)
		err := editResend(editCtx, r, machine, user, cliType, agentType, entry, session.Edit{Text: text, Retained: retained, Added: blocks})
		cancelEdit()
		a.update(func() {
			if still() {
				a.msgEdit.busy, a.msgEdit.progress = false, nil
			}
			if err != nil {
				a.sendError = editFailed(err)
				return
			}
			if a.msgEdit.id == id {
				a.msgEdit = messageEdit{}
			}
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

// messageEditor replaces the bubble while the message is rewritten: the
// attachments the edit leaves, above the card as the message's are, each
// with the composer's remove button, then the card, Add attachment at
// the start of its actions.
func (a *App) messageEditor(c *ui.Context, p *palette) {
	ed := &a.msgEdit
	if len(ed.attachments) > 0 {
		ui.Row(c.Key("edit-attachments")).Wrap().Gap(8).Justify(ui.End).Children(func() {
			for _, at := range ed.attachments {
				a.editAttachmentChip(c, p, at)
			}
		})
	}
	// Its edge darkens while the field has the focus, over Tailwind's
	// 150ms, as Lody's editor card.
	card := ui.Column(c.Key("msg-editor")).FillWidth().MinWidth(320).Gap(8).Padding(10, 12).Radius(14).Background(p.UserBubble).Transition(twFade)
	edge := p.Border
	if card.FocusWithin() {
		edge = p.Text.Alpha(0.25)
	}
	card.Border(1, edge).Children(func() {
		area(c, p, &ed.text, "", locales.EditMessage(), 3).AutoFocus()
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			attach := iconButton(c, p, iconPaperclip, locales.AttachMenu(), p.Hover).Disabled(ed.busy)
			if ed.busy {
				attach.Opacity(0.5)
			} else {
				attach.OnClick(a.pickEditFiles)
			}
			ui.Box(c).Grow(1)
			if ed.busy {
				spinner(c).Size(14, 14)
			}
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() {
				if !ed.busy {
					a.msgEdit = messageEdit{}
				}
			})
			ready := (strings.TrimSpace(ed.text) != "" || len(ed.attachments) > 0) && !ed.busy
			primaryButton(c, p, locales.Send(), ready).OnClick(a.submitEdit)
		})
	})
}

// editAttachmentChip is an attachment of the message being rewritten, as
// the composer's: one it had shows its picture as the conversation
// fetched it, one added its file; while the edit is sent, one added shows
// how far it has uploaded, and none can be removed.
func (a *App) editAttachmentChip(c *ui.Context, p *palette, at editAttachment) {
	ed := &a.msgEdit
	key := at.key()
	remove := func() { a.removeEditAttachment(key) }
	if ed.busy {
		remove = nil
	}
	progress := float32(-1)
	if ed.busy && at.original == nil {
		progress = ed.progress[at.file.Path]
	}
	if o := at.original; o != nil {
		var bm *ui.Bitmap
		if o.Item.Type == "image" {
			if img := a.sessionBitmap(a.selected, o.Item); img != nil {
				bm = img.bitmap
			}
		}
		name := firstNonEmpty(o.Item.FileName, locales.Image())
		a.attachmentChip(c, p, key, name, o.Item.Type == "image", o.Item.SizeBytes, bm, progress, remove)
		return
	}
	var bm *ui.Bitmap
	if img := a.images["file:"+at.file.Path]; img != nil {
		bm = img.bitmap
	}
	a.attachmentChip(c, p, key, at.file.Name, at.file.Image, at.file.Size, bm, progress, remove)
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
		// Lody's components' Loader2, turning in the button's place.
		ui.Box(c).Size(24, 24).Center().Children(func() { loader(c, p).Size(14, 14) })
		return
	}
	turn := e.ID
	worktree := sess.ProjectID != ""
	// Lody's fork destinations, opening above the button from its start:
	// a new tab, a new worktree where the chat has a project, and the
	// conversation copied as context.
	b := iconButton(c, p, iconGitFork, locales.ForkSession(), p.Hover).Size(24, 24)
	lodyMenuAbove(c, p, b, &a.rowMenu("fork/"+e.ID).menuState, false, 6, 200, 290, 28, func(m *lodyMenu) {
		if m.item("fork-tab", iconFolder, locales.ForkNewTab(), menuItem{tip: locales.ForkNewTabHint()}) {
			a.fork(turn, false, false, "")
		}
		if worktree && m.item("fork-worktree", iconWorktree, locales.ForkNewWorktree(), menuItem{tip: locales.ForkNewWorktreeHint()}) {
			a.fork(turn, true, false, "")
		}
		m.sep()
		if m.item("copy-context", iconCopy, locales.HmCopyContext(), menuItem{disabled: a.view == nil, tip: locales.CopyContextHint()}) {
			a.copyConversationThrough(a.selected, turn)
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
	// Dim at rest so the banner yields to the conversation, lifting to full
	// under the pointer over Tailwind's 150ms, as Lody's.
	banner := ui.Column(c.Key("goal-banner")).FillWidth().Padding(10, 16).Gap(6).Background(ink.Alpha(0.08)).BorderWidth(0, 0, 1, 0).BorderColor(ink.Alpha(0.3))
	banner.Opacity(0.75 + 0.25*banner.AnimateWith("lift", b2f(banner.Hovered()), 150*time.Millisecond, twEase))
	banner.Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c.Key("goal-icon"), iconTarget).Size(14, 14).TextColor(ink)
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
			// @lody/ui's secondary buttons, a pending one turning its spinner
			// in its icon's place.
			button := func(cmd, text string, icon *ui.SVG) {
				b := lodyButton(c, p, "secondary", "goal-"+cmd, icon, text, a.goalPending == "", a.goalPending == cmd)
				if a.goalPending == "" {
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
		a.goalObjective(c, p, key, strings.TrimSpace(g.Objective), open)
	})
}

// goalObjective is the banner's objective: one line, which a click opens
// to the whole of it where it is cut, the row filling under the pointer
// and its chevron turning over Tailwind's 150ms, as Lody's.
func (a *App) goalObjective(c *ui.Context, p *palette, key, objective string, open bool) {
	row := ui.Row(c.Key("goal-objective")).FillWidth().Gap(8).Padding(2, 4).Margin(0, -4).Radius(2).AlignItems(ui.Start)
	var text ui.Element
	var natural float32
	row.Children(func() {
		text = ui.Text(c, objective).FontSize(sizeSub).LineHeight(1.45).TextColor(p.Text).Grow(1).MinWidth(0)
		if !open {
			text.MaxLines(1)
		}
		// The line as long as it is, unseen, to tell whether one line cuts it.
		probe := ui.Row(c.Key("probe")).Absolute().Left(0).Top(0).Width(1 << 14).Height(0).ClipY().Invisible()
		probe.Children(func() {
			natural = ui.Text(c, objective).FontSize(sizeSub).LineHeight(1.45).NoWrap().Shrink(0).Bounds().W
		})
		expandable := open || natural > text.Bounds().W+0.5 && text.Bounds().W > 0
		if !expandable {
			return
		}
		turn := ui.Icon(c.Key("chevron"), iconChevronDown).Size(14, 14).Shrink(0).Margin(2, 0, 0, 0).TextColor(p.Secondary)
		turn.Rotate(180 * turn.AnimateWith("turn", b2f(open), 150*time.Millisecond, twEase))
	})
	if !open && !(natural > text.Bounds().W+0.5 && text.Bounds().W > 0) {
		row.Label(locales.GoalObjective())
		return
	}
	toggle := locales.GoalExpandObjective()
	if open {
		toggle = locales.GoalCollapseObjective()
	}
	row.Label(toggle).Cursor(ui.CursorPointer).Transition(twFade).OnClick(func() { a.expanded[key] = !a.expanded[key] })
	if row.Hovered() {
		row.Background(p.Canvas.Alpha(0.4))
	}
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
		// An active goal's mark breathes, as Lody's inline goal block.
		mark := ui.Icon(c.Key("goal-mark"), iconTarget).Size(13, 13).TextColor(ink)
		if it.Goal.Status == "active" {
			pulse(mark)
		}
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
