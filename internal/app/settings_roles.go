package app

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/locales"
)

// Agent roles, rows ['agentRole', id] of the workspace stream: a named
// setup of an agent, its prompt and where it runs. This page lists the
// roles the user may use and deletes their own; they are made on
// lody.ai.

func (a *App) rolesPage(c *ui.Context, p *palette) {
	pageHeader(c, p, locales.SettingsAgentRoles(), locales.RolesLead(), func() {
		outlineButton(c, p, iconExternal, locales.RolesNew()).OnClick(func() { a.webSettings("agent-roles") })
	})
	a.settingsErrorLine(c, p)
	me := ""
	if a.account != nil {
		me = a.account.User.ID
	}
	var roles []catalog.AgentRole
	for _, r := range a.catalog.Roles {
		if r.Visibility == "workspace" || r.OwnerUserID == me {
			roles = append(roles, r)
		}
	}
	if len(roles) == 0 {
		emptyCard(c, p, locales.RolesNone())
		return
	}
	card(c, p, func() {
		for _, r := range roles {
			role := r
			ui.Row(c.Key(r.ID)).FillWidth().MinHeight(52).Padding(8, 8, 8, 14).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, firstNonEmpty(role.Emoji, "🪼")).FontSize(18).Shrink(0)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, role.Name).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine()
						if role.Visibility == "workspace" {
							ui.Text(c, locales.ShortcutsShared()).FontSize(sizeCaption).TextColor(p.Secondary).Padding(1, 6).Radius(5).Background(p.Text.Alpha(0.06))
						}
						if role.PromptPrefix != "" {
							ui.Text(c, "Prompt").FontSize(sizeCaption).TextColor(p.Secondary).Padding(1, 6).Radius(5).Background(p.Text.Alpha(0.06))
						}
					})
					var where []string
					for _, t := range role.Targets {
						place := a.machineName(t.MachineID)
						if name := a.agentName(t.MachineID, t.AgentConfigID); name != "" {
							place = name + " · " + place
						}
						if t.ModelID != "" {
							place += " · " + t.ModelID
						}
						where = append(where, place)
					}
					detail := strings.Join(where, " / ")
					if role.Description != "" {
						detail = role.Description + " — " + detail
					}
					ui.Text(c, detail).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
				})
				if role.OwnerUserID != me {
					return
				}
				iconButton(c, p, iconTrash, locales.ActionDelete(), p.Hover).OnClick(func() {
					a.confirm(locales.RolesDeleteTitle(role.Name), locales.RolesDeleteBody(), locales.ActionDelete(), func() {
						a.streamWrite("", func(f *flock.Flock) error { return f.Delete([]any{"agentRole", role.ID}) }, nil)
					})
				})
			})
		}
	})
}
