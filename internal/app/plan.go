package app

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// A proposed plan, as Lody's plan panel: the plan as the same panel as a
// command's output, clamped once it is history, open in full while it is
// written or waits on a decision; and under a Codex turn that ends on one,
// Implement plan and Continue discussing.

// planClamp is how tall a plan shows until it is opened.
const planClamp = 224

type planState struct {
	open      map[string]bool // panels opened or closed by hand
	dismissed map[string]bool // decisions passed over with Continue discussing
	pending   string          // the plan being implemented
	copied    string          // the plan just copied
	copiedAt  time.Time
}

// latestPlan is the key of the last completed, latest proposed plan of
// the conversation, and the entry it is in.
func latestPlan(entries []session.Entry) (key, entry string) {
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		for j := len(e.Items) - 1; j >= 0; j-- {
			it := e.Items[j]
			if it.Type == "proposed_plan" && it.Status == "completed" && it.Latest && strings.TrimSpace(it.Text) != "" {
				return e.ID + ":" + it.ID, e.ID
			}
		}
	}
	return "", ""
}

// planDecision is whether the plan of key waits on Implement or Continue,
// as Lody asks: a Codex session at rest on it, or one being implemented.
func (a *App) planDecision(key string) bool {
	sess := a.catalog.Session(a.selected)
	if key == "" || sess == nil || a.plan.dismissed[key] {
		return false
	}
	if a.plan.pending == key {
		return true
	}
	busy := a.sending || a.view != nil && a.view.Working
	return sess.AgentType == "codex" && sess.Status == "idle" && !busy
}

func (a *App) planPanel(c *ui.Context, p *palette, e session.Entry, it session.Item) {
	key := e.ID + ":" + it.ID
	streaming := it.Status == "delta"
	open, set := a.plan.open[key]
	if !set {
		latest := ""
		if a.view != nil {
			latest, _ = latestPlan(a.view.Entries)
		}
		open = streaming || latest == key && a.planDecision(key)
	}
	frame := ui.Column(c.Key("plan/"+it.ID)).FillWidth().Radius(10).Border(1, p.Border).Clip()
	frame.Children(func() {
		var body ui.Element
		over := false
		header := ui.Row(c).FillWidth().Height(36).Padding(0, 6, 0, 12).Gap(8).AlignItems(ui.Center).Background(p.Text.Alpha(0.025))
		header.Children(func() {
			toggle := ui.ButtonBase(c.Key("toggle")).Grow(1).MinWidth(0).Height(28).Gap(8).AlignItems(ui.Center).Label(locales.PlanTitle())
			toggle.Children(func() {
				switch {
				case streaming:
					spinner(c).Size(14, 14)
				default:
					turn := toggle.AnimateWith("turn", b2f(open), 200*time.Millisecond, ui.EaseOut)
					ui.Icon(c, iconChevronRight).Size(14, 14).TextColor(p.Secondary).Rotate(90 * turn)
				}
				ui.Text(c, locales.PlanTitle()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine()
			})
			toggle.Cursor(ui.CursorPointer).OnClick(func() {
				if a.plan.open == nil {
					a.plan.open = map[string]bool{}
				}
				a.plan.open[key] = !open
			})
			copied := a.plan.copied == key && time.Since(a.plan.copiedAt) < 1200*time.Millisecond
			icon, label := iconCopy, locales.PlanCopy()
			if copied {
				icon, label = iconCheck, locales.PlanCopied()
				c.After(1200 * time.Millisecond)
			}
			iconButton(c, p, icon, label, p.Hover).Size(24, 24).OnClick(func() {
				writeClipboard(it.Text)
				a.plan.copied, a.plan.copiedAt = key, time.Now()
			})
		})
		ui.Box(c).FillWidth().Height(1).Background(p.Border)
		clip := ui.Column(c.Key("clip")).FillWidth().Clip()
		if !open {
			clip.MaxHeight(planClamp)
		}
		clip.Children(func() {
			body = ui.Column(c.Key("body")).FillWidth().Padding(10, 14)
			body.Children(func() { markdown.View(c.Key(it.ID), a.findMarkdown(e.ID+"/"+it.ID, it.Text), &a.md) })
			over = body.Bounds().H > planClamp+1
			if !open && over {
				// The body fades out where it is cut.
				ui.Box(c.Key("fade")).Absolute().Left(0).Right(0).Bottom(0).Height(40).Draw(func(pt *ui.Painter, r ui.Rect) {
					pt.FillGradient(r, ui.LinearGradient{From: p.Canvas.Alpha(0), To: p.Canvas.Alpha(0.95), Angle: 180}, 0)
				})
			}
		})
		a.findRing(p, e.ID+"/"+it.ID, frame)
	})
}

// planActions are Lody's quick actions under the turn that ends on the
// plan waiting on a decision.
func (a *App) planActions(c *ui.Context, p *palette, e session.Entry) {
	if a.view == nil {
		return
	}
	key, entry := latestPlan(a.view.Entries)
	if entry != e.ID || !a.planDecision(key) {
		return
	}
	sess := a.catalog.Session(a.selected)
	pending := a.plan.pending == key
	ready := sess != nil && !sess.Archived && a.replica != nil
	ui.Row(c.Key("plan-actions")).Gap(8).Margin(8, 0, 0, 0).Children(func() {
		label, icon := locales.PlanImplement(), iconPlay
		if pending {
			label = locales.PlanImplementing()
		}
		b := ui.ButtonBase(c.Key("implement")).Height(30).Padding(0, 12).Radius(8).Gap(6).AlignItems(ui.Center).Background(p.Primary).
			Cursor(ui.CursorPointer).Label(label).Disabled(!ready || pending).Transition(colorFade)
		pressable(b, 0.97)
		if !ready || pending {
			b.Opacity(0.7)
		}
		b.OnClick(func() { a.implementPlan(key) })
		b.Children(func() {
			if pending {
				spinner(c).Size(14, 14)
			} else {
				ui.Icon(c, icon).Size(14, 14).TextColor(p.OnPrimary)
			}
			ui.Text(c, label).FontSize(sizeSub).FontWeight(500).TextColor(p.OnPrimary)
		})
		d := ui.ButtonBase(c.Key("discuss")).Height(30).Padding(0, 12).Radius(8).Gap(6).AlignItems(ui.Center).Border(1, p.Border).
			Cursor(ui.CursorPointer).Label(locales.PlanContinue()).Disabled(pending).Transition(colorFade)
		pressable(d, 0.97)
		if d.Hovered() {
			d.Background(p.Hover)
		}
		d.OnClick(func() {
			if a.plan.dismissed == nil {
				a.plan.dismissed = map[string]bool{}
			}
			a.plan.dismissed[key] = true
		})
		d.Children(func() {
			ui.Icon(c, iconMessage).Size(14, 14).TextColor(p.Text)
			ui.Text(c, locales.PlanContinue()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
		})
	})
}

// implementPlan sends "Implement the plan" straight to the agent, with plan
// mode off: Lody's plan_mode set false, or the collaboration mode back to
// default, or a plan permission mode left for the first other one.
func (a *App) implementPlan(key string) {
	sess := a.catalog.Session(a.selected)
	if sess == nil || a.replica == nil || a.conn == nil || a.plan.pending != "" {
		return
	}
	values := map[string]any{}
	if a.view != nil {
		for k, v := range a.view.ConfigValues {
			values[k] = v
		}
	}
	for k, v := range a.chosenRunValues(a.selected, sess.AgentConfigID, a.currentModel(sess)) {
		values[k] = v
	}
	off := map[string]any{}
	_, hasPlan := values["plan_mode"]
	_, hasCollab := values["collaboration_mode"]
	if hasPlan || !hasCollab {
		off["plan_mode"] = false
	}
	if hasCollab {
		off["collaboration_mode"] = "default"
	}
	for k, v := range off {
		values[k] = v
	}
	mode := a.modes[a.selected]
	if mode == "" && a.view != nil {
		mode = a.view.ModeID
	}
	if mode == "plan" {
		mode = ""
		for _, m := range a.catalog.ModesFor(sess.AgentConfigID, sess.AgentType) {
			if m.ID != "plan" {
				mode = m.ID
				break
			}
		}
	}
	turn := session.Turn{
		MCPServerIDs: a.mcpSelection(a.selected), Text: locales.PlanImplementPrompt(), UserID: a.account.User.ID,
		MachineID: sess.MachineID, AgentType: sess.AgentType, CLIType: sess.CLIType, Resume: sess.Resume,
		ModelID: a.models[a.selected], ModeID: mode, ConfigOptionValues: values, MarkDispatch: a.sync.MarkDispatch,
	}
	r := a.replica
	a.plan.pending = key
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		state, err := r.Send(ctx, turn)
		a.update(func() {
			a.plan.pending = ""
			if err != nil && state == session.NotSent {
				a.toast("danger", locales.PlanImplementFailed())
				return
			}
			if a.plan.dismissed == nil {
				a.plan.dismissed = map[string]bool{}
			}
			a.plan.dismissed[key] = true
		})
	}()
}
