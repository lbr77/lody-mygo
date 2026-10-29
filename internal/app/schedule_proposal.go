package app

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/schedule"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// A schedule proposal is an agent asking to schedule a task it has
// gathered: confirming the card is the creation, with no form after; the
// outcome is written back to the notice, which then reads as a receipt.

// proposal is a schedule_proposal notice's metadata.
type proposal struct {
	ProposalID string `json:"proposalId"`
	Title      string `json:"title"`
	Prompt     string `json:"prompt"`
	Rule       struct {
		Kind     string `json:"kind"`
		Every    int    `json:"every"`
		Hour     int    `json:"hour"`
		Minute   int    `json:"minute"`
		Weekdays []int  `json:"weekdays"`
		Days     []int  `json:"days"`
		At       string `json:"at"`
		TimeZone string `json:"timeZone"`
	} `json:"rule"`
	Destination *struct {
		Kind      string `json:"kind"`
		SessionID string `json:"sessionId"`
	} `json:"destination"`
	Target     *proposalNamed `json:"target"`
	Outcome    string         `json:"outcome"`
	ScheduleID string         `json:"scheduleId"`
	ProposedBy *struct {
		Name string `json:"name"`
	} `json:"proposedBy"`
}

// proposalNamed is what a proposal names to run with.
type proposalNamed struct {
	AgentConfigID string            `json:"agentConfigId"`
	AgentRoleID   string            `json:"agentRoleId"`
	MachineID     string            `json:"machineId"`
	Project       *schedule.Project `json:"project"`
}

// recurrence is the proposal's rule on a clock, or nil for manual.
func (pr *proposal) recurrence(zone string) *schedule.Recurrence {
	r := pr.Rule
	tz := firstNonEmpty(r.TimeZone, zone)
	switch r.Kind {
	case "minutes", "hours":
		return &schedule.Recurrence{Kind: r.Kind, Every: r.Every, TimeZone: zone}
	case "once":
		at, _ := time.Parse(time.RFC3339Nano, r.At)
		return &schedule.Recurrence{Kind: "once", At: at}
	case "daily", "weekdays":
		return &schedule.Recurrence{Kind: r.Kind, Hour: r.Hour, Minute: r.Minute, TimeZone: tz}
	case "weekly":
		return &schedule.Recurrence{Kind: "weekly", Weekdays: toggleSet(r.Weekdays), Hour: r.Hour, Minute: r.Minute, TimeZone: tz}
	case "monthly":
		return &schedule.Recurrence{Kind: "monthly", Days: toggleSet(r.Days), Hour: r.Hour, Minute: r.Minute, TimeZone: tz}
	}
	return nil
}

// toggleSet is ns as a sorted set.
func toggleSet(ns []int) []int {
	var out []int
	for _, n := range ns {
		if !slices.Contains(out, n) {
			out = toggleInt(out, n)
		}
	}
	return out
}

// proposalTarget is what a proposal resolves to run with.
type proposalTarget struct {
	agent       schedule.Agent
	config      catalog.AgentConfig
	project     *schedule.Project
	destination schedule.Destination
	role        *catalog.AgentRole
}

var sensitiveOption = regexp.MustCompile(`(?i)api[_-]?key|auth|bearer|credential|password|passwd|secret|token`)

// builtinDefaultModes are the modes Lody gives an Agent named without the
// conversation's.
var builtinDefaultModes = map[string]string{"kimi": "auto", "grok": "default", "claude": "auto", "codex": "agent-auto-review", "deepseek": "workspace-write"}

// resolveProposal takes everything from the conversation the proposal came
// from, but what it names: a Role, an Agent, a machine or a project.
func (a *App) resolveProposal(pr *proposal, sess *catalog.Session, v *session.View) (*proposalTarget, string) {
	named := pr.Target
	if named == nil {
		named = &proposalNamed{}
	}
	var role *catalog.AgentRole
	var roleTarget *catalog.RoleTarget
	if named.AgentRoleID != "" {
		for i := range a.catalog.Roles {
			if a.catalog.Roles[i].ID == named.AgentRoleID {
				role = &a.catalog.Roles[i]
			}
		}
		if role == nil {
			return nil, locales.PropRoleNotFound()
		}
		machine := named.MachineID
		if machine == "" {
			switch {
			case len(role.Targets) == 1:
				machine = role.Targets[0].MachineID
			case sess != nil:
				for _, t := range role.Targets {
					if t.MachineID == sess.MachineID {
						machine = sess.MachineID
					}
				}
			}
		}
		if machine == "" {
			return nil, locales.PropRoleMachineRequired()
		}
		for i := range role.Targets {
			if role.Targets[i].MachineID == machine {
				roleTarget = &role.Targets[i]
			}
		}
		if roleTarget == nil {
			return nil, locales.PropMachineMismatch()
		}
	}
	configID := named.AgentConfigID
	if roleTarget != nil {
		configID = roleTarget.AgentConfigID
	}
	if configID == "" && sess != nil {
		configID = sess.AgentConfigID
	}
	if configID == "" {
		return nil, locales.PropNoAgent()
	}
	var config *catalog.AgentConfig
	for _, agents := range a.catalog.Agents {
		for i := range agents {
			if agents[i].ID == configID {
				config = &agents[i]
			}
		}
	}
	if config == nil {
		return nil, locales.PropAgentNotFound()
	}
	machine := named.MachineID
	if machine == "" && roleTarget != nil {
		machine = roleTarget.MachineID
	}
	if machine != "" && machine != config.MachineID {
		return nil, locales.PropMachineMismatch()
	}
	agent := schedule.Agent{AgentConfigID: config.ID}
	switch {
	case roleTarget != nil:
		agent.ModeID, agent.ModelID = roleTarget.ModeID, roleTarget.ModelID
		if roleTarget.Memory != "" {
			agent.Memory = &schedule.Memory{ProviderID: roleTarget.MemoryProvider, MemoryID: roleTarget.Memory}
		}
	case sess != nil && sess.AgentConfigID == config.ID:
		// The conversation's run config, but its credentials.
		if v != nil {
			agent.ModeID, agent.ModelID = v.ModeID, v.ModelID
			for k, val := range v.ConfigValues {
				if val == nil || sensitiveOption.MatchString(k) {
					continue
				}
				if agent.ConfigOptionValues == nil {
					agent.ConfigOptionValues = map[string]any{}
				}
				agent.ConfigOptionValues[k] = val
			}
		}
	case config.CLIType == "builtin":
		agent.ModeID = builtinDefaultModes[config.AgentType]
	}
	t := &proposalTarget{agent: agent, config: *config, role: role, destination: schedule.Destination{Kind: schedule.NewSession}}
	if d := pr.Destination; d != nil {
		switch d.Kind {
		case schedule.OwnSession:
			zero := 0
			t.destination = schedule.Destination{Kind: schedule.OwnSession, Epoch: &zero}
		case schedule.ExistingSession:
			t.destination = schedule.Destination{Kind: schedule.ExistingSession, SessionID: d.SessionID}
		}
	}
	if t.destination.Kind == schedule.NewSession {
		switch {
		case named.Project != nil:
			t.project = named.Project
		case sess != nil && sess.Project != nil:
			p := &schedule.Project{}
			if noticeMeta(sess.Project, p) && p.Kind != "" {
				t.project = p
			}
		case sess != nil && sess.RepoFullName != "":
			t.project = &schedule.Project{Kind: "github", RepoFullName: sess.RepoFullName, Branch: firstNonEmpty(sess.BaseBranch, "main")}
		}
	}
	return t, ""
}

// proposalEdit is a proposal being edited before it is created.
type proposalEdit struct {
	form    *schedForm // the edits, kept after Done for Create
	editing bool
	busy    bool
}

func (a *App) scheduleProposal(c *ui.Context, p *palette, e session.Entry, it session.Item) {
	var pr proposal
	if !noticeMeta(it.Notice, &pr) || pr.ProposalID == "" {
		return
	}
	switch pr.Outcome {
	case "created":
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Padding(8, 12).Radius(8).Border(0.5, p.Border).Background(p.Canvas).Children(func() {
			ui.Icon(c, iconCalendarClock).Size(14, 14).TextColor(p.Secondary)
			ui.Text(c, locales.PropCreated()).FontSize(sizeFoot).TextColor(p.Secondary).Shrink(0)
			ui.Text(c, pr.Title).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
			if id := pr.ScheduleID; id != "" {
				ghostButton(c, p, locales.PropOpen()).OnClick(func() {
					a.page = "schedules"
					a.openSchedule(id)
				})
			}
		})
		return
	case "dismissed":
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Padding(4, 0).Children(func() {
			ui.Icon(c, iconCalendarClock).Size(14, 14).TextColor(p.Tertiary)
			ui.Text(c, locales.PropDismissed()).FontSize(sizeFoot).TextColor(p.Tertiary).Shrink(0)
			ui.Text(c, pr.Title).FontSize(sizeFoot).TextColor(p.Tertiary).SingleLine().Strikethrough().MinWidth(0)
		})
		return
	}
	sess := a.catalog.Session(a.selected)
	target, problem := a.resolveProposal(&pr, sess, a.view)
	machine := ""
	if target != nil {
		machine = target.config.MachineID
	}
	zone := a.machineZone(machine)
	if a.proposals == nil {
		a.proposals = map[string]*proposalEdit{}
	}
	key := e.ID + "/" + it.ID
	ed := a.proposals[key]
	if ed == nil {
		ed = &proposalEdit{}
		a.proposals[key] = ed
	}
	title, prompt := pr.Title, pr.Prompt
	if ed.form != nil {
		title, prompt = ed.form.title, ed.form.prompt
	}
	ui.Column(c).FillWidth().Gap(10).Padding(12, 14).Radius(12).Border(0.5, p.Border).Background(p.Popover).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconCalendarClock).Size(14, 14).TextColor(p.Secondary)
			ui.Text(c, locales.PropTitle()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
			if pr.ProposedBy != nil && pr.ProposedBy.Name != "" {
				ui.Text(c, locales.PropBy(pr.ProposedBy.Name)).FontSize(sizeFoot).TextColor(p.Secondary)
			}
		})
		if f := ed.form; f != nil && ed.editing {
			input(c, p, &f.title, locales.SchedNamePlaceholder(), locales.SchedName())
			area(c, p, &f.prompt, "", locales.SchedPrompt(), 4)
			ui.Column(c).FillWidth().Radius(12).Border(0.5, p.Border).Background(p.Canvas).Dividers(0.5, p.Border).Children(func() {
				mode := "timed"
				if f.manual {
					mode = "manual"
				}
				propertyRow(c, p, "trigger", locales.SchedTrigger(), func() {
					segmented(c, p, "prop/trigger/"+key, []option{{"timed", locales.SchedTimed()}, {"manual", locales.SchedManual()}}, mode, func(v string) { f.manual = v == "manual" })
				})
				if !f.manual {
					a.schedRecurrence(c, p, f, zone, time.Now())
				}
			})
		} else {
			ui.Text(c, title).FontSize(sizeBody).FontWeight(600).TextColor(p.Strong)
			ui.Column(c).FillWidth().MaxHeight(160).Clip().Children(func() {
				markdown.View(c.Key("prompt/"+key), prompt, &a.md)
			})
		}
		rows := [][2]string{}
		if !ed.editing {
			rule := locales.SchedManual()
			if f := ed.form; f != nil {
				if !f.manual {
					rule = describeRecurrence(f.rec, zone)
				}
			} else if r := pr.recurrence(zone); r != nil {
				rule = describeRecurrence(*r, zone)
			}
			rows = append(rows, [2]string{locales.SchedTrigger(), rule})
		}
		dest := locales.SchedDestNew()
		agent := "—"
		if target != nil {
			switch target.destination.Kind {
			case schedule.OwnSession:
				dest = locales.SchedDestOwn()
			case schedule.ExistingSession:
				dest = locales.SchedDestExisting()
			}
			var parts []string
			if target.role != nil {
				parts = append(parts, strings.TrimSpace(target.role.Emoji+" "+target.role.Name))
			}
			parts = append(parts, target.config.Name, a.machineLabel(target.config.MachineID))
			agent = strings.Join(parts, " · ")
		}
		rows = append(rows, [2]string{locales.SchedSendTo(), dest}, [2]string{locales.SchedAgent(), agent})
		if target != nil && target.project != nil {
			name := target.project.RepoFullName
			if target.project.Kind == "local" {
				name = target.project.LocalProjectID
				if pr := a.catalog.Project(target.config.MachineID + ":local:" + target.project.LocalProjectID); pr != nil {
					name = pr.Name
				}
			}
			rows = append(rows, [2]string{locales.SchedProject(), name})
		}
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			for _, r := range rows {
				ui.Row(c.Key(r[0])).FillWidth().Gap(12).Children(func() {
					ui.Text(c, r[0]).FontSize(sizeFoot).TextColor(p.Secondary).Width(96).Shrink(0)
					ui.Text(c, r[1]).FontSize(sizeFoot).TextColor(p.Text).Grow(1).MinWidth(0)
				})
			}
		})
		var reasons []string
		if problem != "" {
			reasons = append(reasons, problem)
		} else if ed.form != nil {
			if strings.TrimSpace(ed.form.title) == "" {
				reasons = append(reasons, locales.SchedRequireName())
			}
			if strings.TrimSpace(ed.form.prompt) == "" {
				reasons = append(reasons, locales.SchedRequirePrompt())
			}
		}
		for _, r := range reasons {
			ui.Text(c, r).FontSize(sizeFoot).TextColor(p.Warning)
		}
		ui.Row(c).FillWidth().Gap(6).Justify(ui.End).Children(func() {
			ghostButton(c, p, locales.PropDismiss()).Disabled(ed.busy).OnClick(func() {
				a.proposalOutcome(e.ID, it.ID, ed, map[string]any{"outcome": "dismissed"})
			})
			if ed.editing {
				// Done leaves the edits for Create, as Lody's card keeps them.
				outlineButton(c, p, nil, locales.PropDoneEditing()).OnClick(func() { ed.editing = false })
			} else {
				outlineButton(c, p, nil, locales.PropEdit()).OnClick(func() {
					ed.editing = true
					if ed.form != nil {
						return
					}
					f := &schedForm{title: pr.Title, prompt: pr.Prompt, manual: pr.Rule.Kind == "manual" || pr.Rule.Kind == ""}
					if r := pr.recurrence(zone); r != nil {
						f.rec = *r
					} else {
						f.rec = schedule.Recurrence{Kind: "daily", Hour: 9, TimeZone: zone}
					}
					f.machine, f.recEdited = machine, true
					f.syncTexts(zone)
					ed.form = f
				})
			}
			primaryButton(c, p, locales.PropCreate(), !ed.busy && len(reasons) == 0 && target != nil).OnClick(func() {
				if len(reasons) == 0 && target != nil {
					a.createProposal(e.ID, it.ID, &pr, target, ed)
				}
			})
		})
	})
}

// createProposal creates the schedule the proposal describes, with any
// edits, then marks the proposal created.
func (a *App) createProposal(entryID, itemID string, pr *proposal, t *proposalTarget, ed *proposalEdit) {
	st := &a.sched
	r := a.replica
	if st.store == nil || r == nil || ed.busy {
		return
	}
	now := time.Now()
	zone := a.machineZone(t.config.MachineID)
	title, prompt := pr.Title, pr.Prompt
	trigger := schedule.Trigger{Kind: "manual"}
	if f := ed.form; f != nil {
		title, prompt = f.title, f.prompt
		tr, err := a.formTrigger(f, now)
		if err != nil {
			a.toast("warning", locales.SchedInvalidTime())
			return
		}
		trigger = tr
	} else if rec := pr.recurrence(zone); rec != nil {
		tr, err := schedule.TriggerOf(*rec, now)
		if err != nil {
			a.toast("warning", locales.SchedInvalidTime())
			return
		}
		trigger = tr
	}
	d := schedule.Draft{Title: title, Prompt: prompt, MachineID: t.config.MachineID, Trigger: trigger, Agent: t.agent, Project: t.project, Destination: t.destination}
	store, user, id := st.store, a.userID(), pr.ProposalID
	ed.busy = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := store.Save(ctx, id, true, d, user, now)
		if err == nil {
			err = r.SetNoticeMeta(ctx, entryID, itemID, map[string]any{"outcome": "created", "scheduleId": id})
		}
		a.update(func() {
			ed.busy = false
			if err != nil {
				a.toast("warning", err.Error())
			}
		})
	}()
}

// proposalOutcome writes fields to the proposal's notice.
func (a *App) proposalOutcome(entryID, itemID string, ed *proposalEdit, fields map[string]any) {
	r := a.replica
	if r == nil || ed.busy {
		return
	}
	ed.busy = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := r.SetNoticeMeta(ctx, entryID, itemID, fields)
		a.update(func() {
			ed.busy = false
			if err != nil {
				a.toast("warning", err.Error())
			}
		})
	}()
}
