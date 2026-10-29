package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/schedule"
	"github.com/lbr77/lody-mygo/locales"
)

// Schedule send: the composer's "+" can hold the next message for a time.
// Send then reserves it as a scheduled message, a once schedule into the
// chat that the machine sends when its time comes, as Lody's does; the
// chat's message queue lists it until then (scheduledRow), its time
// editable, and cancellable.

// schedSendDraft is the time chosen for a chat's next message.
type schedSendDraft struct {
	at time.Time
	id string // the reservation's schedule id, kept across a retry
}

// schedSendDialog picks the time: for the draft, or to move a scheduled
// message.
type schedSendDialog struct {
	open       bool
	session    string
	text       string // the date and time being typed, on the machine's clock
	reschedule string // the scheduled message being moved
}

const smLayout = "2006-01-02 15:04"

// scheduleSendReason is why a chat can't schedule a message, or "".
func (a *App) scheduleSendReason(sess *catalog.Session) string {
	user := a.userID()
	switch {
	case sess == nil || a.sched.store == nil || user == "":
		return locales.SchedLoadError()
	case sess.UserID != "" && sess.UserID != user,
		a.catalog.MachineOwner[sess.MachineID] != "" && a.catalog.MachineOwner[sess.MachineID] != user:
		return locales.SmOwnMachine()
	case !a.canScheduleMessages(sess.MachineID):
		return locales.SmUpgrade()
	case sess.Archived:
		return locales.SmArchived()
	}
	return ""
}

// canScheduleMessages reports whether a machine's CLI sends complete
// scheduled messages: schedules protocol 3.
func (a *App) canScheduleMessages(machine string) bool {
	caps, ok := a.catalog.MachineCaps[machine]
	return !ok || caps["schedules"] >= 3
}

// openScheduleSend opens the time picker at a time, or an hour from now.
func (a *App) openScheduleSend(sessionID string, at time.Time, reschedule string) {
	sess := a.catalog.Session(sessionID)
	if sess == nil {
		return
	}
	if at.IsZero() {
		at = time.Now().Add(time.Hour)
	}
	loc := loadZone(a.machineZone(sess.MachineID))
	a.schedSendDlg = schedSendDialog{open: true, session: sessionID, text: at.In(loc).Format(smLayout), reschedule: reschedule}
}

func (a *App) scheduleSendDialog(c *ui.Context, p *palette) {
	dl := &a.schedSendDlg
	if !dl.open {
		return
	}
	sess := a.catalog.Session(dl.session)
	if sess == nil {
		dl.open = false
		return
	}
	zone := a.machineZone(sess.MachineID)
	loc := loadZone(zone)
	now := time.Now()
	at, err := time.ParseInLocation(smLayout, strings.TrimSpace(dl.text), loc)
	valid := err == nil && at.After(now)
	apply := func() {
		if !valid {
			return
		}
		if dl.reschedule != "" {
			id := dl.reschedule
			user := a.userID()
			a.schedAct(func(ctx context.Context, s *schedule.Store) error { return s.Reschedule(ctx, id, at, user, time.Now()) }, nil)
		} else {
			d := a.schedSends[dl.session]
			if d == nil {
				d = &schedSendDraft{id: uuid.NewString()}
				a.schedSends[dl.session] = d
			}
			d.at = at
		}
		dl.open = false
	}
	ui.DialogBase(c, &dl.open, func(backdrop, panel ui.Element) {
		sheetIn(backdrop, panel)
		backdrop.Background(ui.RGBA(0, 0, 0, 0.22))
		panel.Width(380).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Padding(20).Gap(12).Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Text(c, locales.SmTitle()).FontSize(16).FontWeight(600).TextColor(p.Strong)
		ui.Row(c).Gap(8).Wrap().Children(func() {
			for _, q := range []struct {
				label string
				after time.Duration
			}{{locales.SmIn30Minutes(), 30 * time.Minute}, {locales.SmInOneHour(), time.Hour}, {locales.SmInFiveHours(), 5 * time.Hour}} {
				q := q
				outlineButton(c, p, nil, q.label).OnClick(func() { dl.text = now.Add(q.after).In(loc).Format(smLayout) })
			}
		})
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Text(c, locales.SmDateAndTime()).FontSize(sizeSub).TextColor(p.Text)
			in := input(c, p, &dl.text, "2026-10-30 09:00", locales.SmDateAndTime()).OnSubmit(apply)
			if !valid {
				in.Border(1, p.Danger.Alpha(0.6))
			}
		})
		hint := zone
		if valid {
			hint += " · " + instantText(at.In(loc))
		}
		ui.Text(c, hint).FontSize(sizeFoot).TextColor(p.Secondary)
		if !valid {
			ui.Text(c, locales.SmFutureTime()).FontSize(sizeFoot).TextColor(p.Text)
		}
		ui.Text(c, locales.SmMachineHint()).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Secondary)
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Padding(4, 0, 0, 0).Children(func() {
			if dl.reschedule == "" && a.schedSends[dl.session] != nil {
				session := dl.session
				ghostButton(c, p, locales.SmRemoveTime()).OnClick(func() {
					delete(a.schedSends, session)
					dl.open = false
				})
				ui.Spacer(c)
			}
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { dl.open = false })
			primaryButton(c, p, locales.SmApply(), valid).OnClick(apply)
		})
	})
}

// scheduledAction is the composer's chip for a draft with a time.
func (a *App) scheduledAction(sess *catalog.Session) (string, func()) {
	if sess == nil {
		return "", nil
	}
	d := a.schedSends[sess.ID]
	if d == nil || a.scheduleSendReason(sess) != "" {
		return "", nil
	}
	label := locales.SmComposerAction(upcomingText(d.at, time.Now(), loadZone(a.machineZone(sess.MachineID))))
	id := sess.ID
	return label, func() { a.openScheduleSend(id, d.at, "") }
}

// sendScheduled reserves the composer's message for its chosen time.
func (a *App) sendScheduled(d *schedSendDraft) {
	text := strings.TrimSpace(a.draft)
	sess := a.catalog.Session(a.selected)
	files := a.attachments
	if (text == "" && len(files) == 0) || a.sending || sess == nil || a.conn == nil {
		return
	}
	if reason := a.scheduleSendReason(sess); reason != "" {
		a.sendError = reason
		return
	}
	if !d.at.After(time.Now()) {
		a.sendError = locales.SmFutureTime()
		return
	}
	store, conn, user, workspace := a.sched.store, a.conn, a.userID(), a.conn.ID
	target := a.mentionTarget()
	skills, sessions := a.mentionItems(target)
	shortcuts := a.shortcutStore()
	sid := sess.ID
	agent := schedule.Agent{
		AgentConfigID:      sess.AgentConfigID,
		ModelID:            a.currentModel(sess),
		ModeID:             a.modes[sid],
		ConfigOptionValues: a.chosenRunValues(sid, sess.AgentConfigID, a.currentModel(sess)),
	}
	mcp := a.mcpSelection(sid)
	a.sending, a.sendError = true, ""
	a.sendingID = sid
	a.sendingAttachments(files)
	a.draft, a.attachments = "", nil
	delete(a.drafts, sid)
	delete(a.attachDrafts, sid)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		expanded, spans := expandMentions(ctx, conn, user, target, text, skills, sessions, shortcuts)
		var blocks []any
		if expanded != "" {
			block := map[string]any{"type": "text", "text": expanded}
			if len(spans) > 0 {
				list := make([]any, len(spans))
				for i, s := range spans {
					list[i] = s
				}
				block["spans"] = list
			}
			blocks = append(blocks, block)
		}
		attached, err := a.upload(ctx, workspace, sid, files)
		for _, b := range attached {
			blocks = append(blocks, b)
		}
		if err == nil {
			payload := map[string]any{"inputBlocks": blocks}
			if mcp != nil {
				ids := make([]any, len(mcp))
				for i, id := range mcp {
					ids[i] = id
				}
				payload["mcpServerIds"] = ids
			}
			err = store.SaveMessage(ctx, d.id, schedule.MessageDraft{SessionID: sid, MachineID: sess.MachineID, Agent: agent, Payload: payload, At: d.at}, user, locales.SmTitle(), time.Now())
		}
		a.update(func() {
			a.sending = false
			a.sentAttachments()
			if err != nil {
				// The draft comes back, and the same reservation is
				// tried again.
				if a.selected == sid && a.draft == "" && len(a.attachments) == 0 {
					a.draft, a.attachments = text, files
				}
				a.sendError = locales.SmSaveError()
				if errors.Is(err, schedule.ErrTimePassed) {
					a.sendError = locales.SmFutureTime()
				}
				return
			}
			if a.schedSends[sid] == d {
				delete(a.schedSends, sid)
			}
			a.toast("success", locales.SmSaved())
		})
	}()
}
