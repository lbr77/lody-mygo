package app

import (
	"context"
	"errors"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/internal/mention"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// An Agent Role proposal is an agent asking to create a Role, or change
// one: Save applies only the proposed fields to the Role as it now stands,
// and the outcome is written back to the notice.

// roleProposal is an agent_role_proposal notice's metadata.
type roleProposal struct {
	Command struct {
		Action        string  `json:"action"` // create or update
		Name          string  `json:"name"`
		Description   *string `json:"description"`
		PromptPrefix  *string `json:"promptPrefix"`
		MachineID     string  `json:"machineId"`
		AgentConfigID string  `json:"agentConfigId"`
		RoleID        string  `json:"roleId"`
		Patch         struct {
			Name         *string `json:"name"`
			Description  *string `json:"description"`
			PromptPrefix *string `json:"promptPrefix"`
		} `json:"patch"`
	} `json:"command"`
	RoleID          string `json:"roleId"`
	RequesterUserID string `json:"requesterUserId"`
	Before          *struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		PromptPrefix string `json:"promptPrefix"`
	} `json:"before"`
	Outcome string `json:"outcome"`
}

// fields are the proposed values by field, nil when not proposed.
func (rp *roleProposal) fields() (name, description, prompt *string) {
	c := rp.Command
	if c.Action == "create" {
		n := c.Name
		return &n, c.Description, c.PromptPrefix
	}
	return c.Patch.Name, c.Patch.Description, c.Patch.PromptPrefix
}

// applyRoleProposal is the Role row Save writes, as Lody's
// applyAgentRoleProposal: a create retried after an edit leaves the Role,
// and a name another of the user's Roles answers to is refused.
func applyRoleProposal(rp *roleProposal, roles []catalog.AgentRole, user string, now float64) (map[string]any, error) {
	if rp.RequesterUserID != user {
		return nil, errors.New(locales.ArpOwnerOnly())
	}
	var existing *catalog.AgentRole
	for i := range roles {
		if roles[i].ID == rp.RoleID {
			existing = &roles[i]
		}
	}
	if existing != nil && existing.OwnerUserID != user {
		return nil, errors.New(locales.ArpNotEditable())
	}
	c := rp.Command
	if c.Action == "update" && (existing == nil || c.RoleID != rp.RoleID) {
		return nil, errors.New(locales.ArpRoleGone())
	}
	if c.Action == "create" && existing != nil {
		return nil, nil
	}
	row := map[string]any{}
	if c.Action == "create" {
		row = map[string]any{"v": 1, "id": rp.RoleID, "ownerUserId": user, "visibility": "private", "name": c.Name,
			"description": deref(c.Description), "promptPrefix": deref(c.PromptPrefix), "machineId": c.MachineID,
			"agentConfigId": c.AgentConfigID, "runConfig": map[string]any{}, "revision": 1, "createdAt": now, "updatedAt": now}
	} else {
		for k, v := range existing.Row {
			row[k] = v
		}
		changed := false
		for key, v := range map[string]*string{"name": c.Patch.Name, "description": c.Patch.Description, "promptPrefix": c.Patch.PromptPrefix} {
			if v != nil && noticeText(row, key) != *v {
				row[key], changed = *v, true
			}
		}
		if !changed {
			return nil, nil
		}
		row["revision"], row["updatedAt"] = existing.Revision+1, now
	}
	slug := mention.RoleSlug(noticeText(row, "name"))
	for _, r := range roles {
		if r.ID != rp.RoleID && (r.OwnerUserID == user || r.Visibility == "workspace") && mention.RoleSlug(r.Name) == slug {
			return nil, errors.New(locales.ArpNameInUse())
		}
	}
	return row, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// roleProposalState is a card's save in flight and its error.
type roleProposalState struct {
	busy bool
	err  string
}

func (a *App) roleProposalCard(c *ui.Context, p *palette, e session.Entry, it session.Item) {
	var rp roleProposal
	if !noticeMeta(it.Notice, &rp) || rp.RoleID == "" {
		return
	}
	key := e.ID + "/" + it.ID
	if a.roleProposals == nil {
		a.roleProposals = map[string]*roleProposalState{}
	}
	st := a.roleProposals[key]
	if st == nil {
		st = &roleProposalState{}
		a.roleProposals[key] = st
	}
	name, description, prompt := rp.fields()
	heading := locales.ArpCreate()
	switch {
	case rp.Outcome == "saved":
		heading = locales.ArpSaved()
	case rp.Outcome == "dismissed":
		heading = locales.ArpDismissed()
	case rp.Command.Action == "update":
		heading = locales.ArpUpdate()
	}
	target := ""
	if rp.Command.Action == "create" {
		target = firstNonEmpty(a.agentName(rp.Command.MachineID, rp.Command.AgentConfigID), rp.Command.AgentConfigID) + " · " + rp.Command.MachineID
	} else if rp.Before != nil {
		target = rp.Before.Name
	}
	ui.Column(c).FillWidth().Gap(12).Padding(16).Radius(12).Border(1, p.Border).Background(p.Text.Alpha(0.025)).Children(func() {
		ui.Text(c, heading).FontSize(sizeSub).FontWeight(600).TextColor(p.Text)
		if target != "" {
			ui.Text(c, target).FontSize(sizeSub).TextColor(p.Text)
		}
		if rp.Command.Action == "create" {
			ui.Text(c, locales.ArpDefaults()).FontSize(sizeSub).TextColor(p.Secondary)
		}
		for _, f := range []struct {
			label  string
			value  *string
			before string
		}{
			{locales.ArpName(), name, beforeField(rp, "name")},
			{locales.ArpDescription(), description, beforeField(rp, "description")},
			{locales.ArpPromptPrefix(), prompt, beforeField(rp, "promptPrefix")},
		} {
			if f.value == nil {
				continue
			}
			f := f
			ui.Column(c.Key(f.label)).FillWidth().Gap(4).Children(func() {
				ui.Text(c, f.label).FontSize(sizeSub).FontWeight(600).TextColor(p.Text)
				if rp.Before != nil {
					ui.Text(c, locales.ArpBefore()).FontSize(sizeFoot).TextColor(p.Secondary)
					ui.Text(c, firstNonEmpty(f.before, locales.ArpEmpty())).FontSize(sizeSub).TextColor(p.Secondary).Selectable()
					ui.Text(c, locales.ArpAfter()).FontSize(sizeFoot).TextColor(p.Text)
				}
				ui.Text(c, firstNonEmpty(*f.value, locales.ArpEmpty())).FontSize(sizeSub).TextColor(p.Text).Selectable()
			})
		}
		if st.err != "" {
			ui.Text(c, st.err).FontSize(sizeSub).TextColor(p.Danger)
		}
		if rp.Outcome != "" {
			return
		}
		ui.Text(c, locales.ArpScope()).FontSize(sizeSub).TextColor(p.Secondary)
		disabled := st.busy || a.userID() != rp.RequesterUserID || a.replica == nil
		ui.Row(c).Gap(8).Children(func() {
			outlineButton(c, p, nil, locales.ArpDismiss()).Disabled(disabled).OnClick(func() { a.decideRoleProposal(e.ID, it.ID, rp, st, "dismissed") })
			primaryButton(c, p, locales.ArpSave(), !disabled).OnClick(func() {
				if !disabled {
					a.decideRoleProposal(e.ID, it.ID, rp, st, "saved")
				}
			})
		})
	})
}

func beforeField(rp roleProposal, key string) string {
	if rp.Before == nil {
		return ""
	}
	return map[string]string{"name": rp.Before.Name, "description": rp.Before.Description, "promptPrefix": rp.Before.PromptPrefix}[key]
}

// decideRoleProposal saves the Role, or not, and records the outcome.
func (a *App) decideRoleProposal(entryID, itemID string, rp roleProposal, st *roleProposalState, decision string) {
	r, sync := a.replica, a.sync
	if r == nil || sync == nil || st.busy {
		return
	}
	user := a.userID()
	var row map[string]any
	if decision == "saved" {
		c := rp.Command
		if c.Action == "create" && a.agent(c.MachineID, c.AgentConfigID) == nil {
			st.err = locales.ArpTargetUnavailable()
			return
		}
		var err error
		if row, err = applyRoleProposal(&rp, a.catalog.Roles, user, float64(time.Now().UnixMilli())); err != nil {
			st.err = err.Error()
			return
		}
	}
	st.busy, st.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var err error
		if row != nil {
			id := rp.RoleID
			err = sync.WriteWorkspace(ctx, func(f *flock.Flock) error { return f.Put([]any{"agentRole", id}, row) })
		}
		if err == nil {
			err = r.SetNoticeMeta(ctx, entryID, itemID, map[string]any{"outcome": decision})
		}
		a.update(func() {
			st.busy = false
			if err != nil {
				st.err = locales.ArpFailed() + ": " + err.Error()
			}
		})
	}()
}
