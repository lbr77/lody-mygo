package app

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/locales"
)

// Agent roles, rows ['agentRole', id] of the workspace stream: a named
// setup of an agent, its prompt and where it runs, on one machine or
// several. The page lists the roles the user may use; owners edit and
// delete theirs.

func (a *App) rolesPage(c *ui.Context, p *palette) {
	pageHeader(c, p, locales.SettingsAgentRoles(), locales.RolesLead(), func() {
		outlineButton(c, p, iconPlus, locales.RolesNew()).OnClick(func() { a.editRole(nil) })
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
	defer a.roleDialog(c, p)
	if len(roles) == 0 {
		emptyCard(c, p, locales.RolesNone())
		return
	}
	card(c, p, func() {
		for _, r := range roles {
			role := r
			ui.Row(c.Key(r.ID)).FillWidth().MinHeight(52).Padding(8, 8, 8, 14).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, firstNonEmpty(role.Emoji, "🪼")).FontSize(18).Shrink(0)
				body := ui.Column(c).Grow(1).MinWidth(0).Gap(2)
				if role.OwnerUserID == me {
					body.Cursor(ui.CursorPointer).Label(role.Name).OnClick(func() { a.editRole(&role) })
				}
				body.Children(func() {
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

type roleTarget struct{ machine, agent, model, mode, memory string }

type roleForm struct {
	open        bool
	base        *catalog.AgentRole
	id          string
	emoji       string
	name        string
	description string
	prompt      string
	shared      bool
	targets     []roleTarget
	err         string
}

// editRole opens the editor, on a new role or one of the user's.
func (a *App) editRole(r *catalog.AgentRole) {
	f := roleForm{open: true, id: uuid.NewString()}
	if r == nil {
		machine := firstNonEmpty(a.localMachine(), firstOf(a.catalog.MachineIDs))
		agent := ""
		if ags := a.catalog.Agents[machine]; len(ags) > 0 {
			agent = ags[0].ID
		}
		f.targets = []roleTarget{{machine: machine, agent: agent}}
	} else {
		role := *r
		f.base, f.id, f.emoji, f.name, f.description, f.prompt = &role, r.ID, r.Emoji, r.Name, r.Description, r.PromptPrefix
		f.shared = r.Visibility == "workspace"
		for _, t := range r.Targets {
			f.targets = append(f.targets, roleTarget{machine: t.MachineID, agent: t.AgentConfigID, model: t.ModelID, mode: t.ModeID, memory: t.Memory})
		}
	}
	a.role = f
}

func firstOf(list []string) string {
	if len(list) > 0 {
		return list[0]
	}
	return ""
}

// runConfig is a target's run settings, keeping those the editor doesn't
// show from the role it edits.
func (f *roleForm) runConfig(t roleTarget) map[string]any {
	cfg := map[string]any{}
	if f.base != nil {
		for _, old := range roleTargetRows(f.base.Row) {
			if str(old["machineId"]) == t.machine {
				if prev, _ := old["runConfig"].(map[string]any); prev != nil {
					for k, v := range prev {
						cfg[k] = v
					}
				}
			}
		}
	}
	set := func(key, value string) {
		if value != "" {
			cfg[key] = value
		} else {
			delete(cfg, key)
		}
	}
	set("modelId", t.model)
	set("modeId", t.mode)
	if t.memory != "" {
		cfg["memory"] = map[string]any{"providerId": nowledgeMem, "memoryId": t.memory}
	} else {
		delete(cfg, "memory")
	}
	return cfg
}

// roleTargetRows are a stored role's targets, of either version.
func roleTargetRows(row map[string]any) []map[string]any {
	if targets := anyList(row["targets"]); len(targets) > 0 {
		var out []map[string]any
		for _, t := range targets {
			if m, _ := t.(map[string]any); m != nil {
				out = append(out, m)
			}
		}
		return out
	}
	if str(row["machineId"]) != "" {
		return []map[string]any{row}
	}
	return nil
}

func (a *App) saveRole() {
	f := &a.role
	name := strings.TrimSpace(f.name)
	switch {
	case name == "" || utf8.RuneCountInString(name) > 60:
		f.err = locales.RoleNameLimit()
		return
	case utf8.RuneCountInString(strings.TrimSpace(f.description)) > 140:
		f.err = locales.RoleDescriptionLimit()
		return
	case utf8.RuneCountInString(strings.TrimSpace(f.emoji)) > 8:
		f.err = locales.RoleEmojiLimit()
		return
	}
	var targets []roleTarget
	seen := map[string]bool{}
	for _, t := range f.targets {
		if t.machine == "" || t.agent == "" || seen[t.machine] {
			continue
		}
		seen[t.machine] = true
		targets = append(targets, t)
	}
	if len(targets) == 0 {
		f.err = locales.RoleNeedsAgent()
		return
	}
	row := map[string]any{}
	now := nowMs()
	revision := 1.0
	if f.base != nil {
		for k, v := range f.base.Row {
			row[k] = v
		}
		revision = f.base.Revision + 1
	} else {
		row["createdAt"], row["ownerUserId"] = now, a.account.User.ID
	}
	row["id"], row["name"], row["description"], row["revision"], row["updatedAt"] = f.id, name, strings.TrimSpace(f.description), revision, now
	row["visibility"] = map[bool]string{true: "workspace", false: "private"}[f.shared]
	for key, value := range map[string]string{"emoji": strings.TrimSpace(f.emoji), "promptPrefix": strings.TrimSpace(f.prompt)} {
		if value != "" {
			row[key] = value
		} else {
			delete(row, key)
		}
	}
	// One machine is version 1; more need version 2's targets.
	if len(targets) == 1 {
		t := targets[0]
		row["v"], row["machineId"], row["agentConfigId"], row["runConfig"] = 1, t.machine, t.agent, f.runConfig(t)
		delete(row, "targets")
	} else {
		list := make([]any, len(targets))
		for i, t := range targets {
			list[i] = map[string]any{"machineId": t.machine, "agentConfigId": t.agent, "runConfig": f.runConfig(t)}
		}
		row["v"], row["targets"] = 2, list
		delete(row, "machineId")
		delete(row, "agentConfigId")
		delete(row, "runConfig")
	}
	id := f.id
	f.open = false
	a.streamWrite("", func(fl *flock.Flock) error { return fl.Put([]any{"agentRole", id}, row) }, nil)
}

func (a *App) roleDialog(c *ui.Context, p *palette) {
	f := &a.role
	if !f.open {
		return
	}
	title := locales.RolesNew()
	if f.base != nil {
		title = locales.RolesEdit()
	}
	_, h := c.Size()
	sheet(c, p, &f.open, 680, func() {
		sheetHeader(c, p, title, locales.RolesLead(), func() { f.open = false })
		ui.Scroll(c).FillWidth().MaxHeight(h-220).Padding(8, 20).Gap(14).Children(func() {
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				input(c, p, &f.emoji, "🪼", locales.ShortcutsEmoji()).Width(48)
				input(c, p, &f.name, locales.RoleName(), locales.RoleName()).Grow(1).AutoFocus()
			})
			input(c, p, &f.description, locales.RoleDescription(), locales.RoleDescription())
			field(c, p, locales.RolePrompt(), locales.RolePromptHint(), func() {
				area(c, p, &f.prompt, "", locales.RolePrompt(), 4)
			})
			field(c, p, locales.RoleWhere(), locales.RoleWhereHint(), func() {
				for i := range f.targets {
					a.roleTargetRow(c.Key(i), p, i)
				}
				used := map[string]bool{}
				for _, t := range f.targets {
					used[t.machine] = true
				}
				for _, id := range a.catalog.MachineIDs {
					if used[id] {
						continue
					}
					machine := id
					ghostButton(c, p, "+ "+locales.RoleAddMachine(a.machineName(id))).OnClick(func() {
						agent := ""
						if ags := a.catalog.Agents[machine]; len(ags) > 0 {
							agent = ags[0].ID
						}
						f.targets = append(f.targets, roleTarget{machine: machine, agent: agent})
					})
					break
				}
			})
			ui.Row(c).FillWidth().Padding(10, 12).Radius(8).Gap(12).AlignItems(ui.Center).Background(p.Text.Alpha(0.035)).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, locales.ShortcutsShare()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
					ui.Text(c, locales.RoleShareHint()).FontSize(sizeCaption).TextColor(p.Secondary)
				})
				lodySwitch(c, &f.shared, false).Label(locales.ShortcutsShare())
			})
			if f.err != "" {
				ui.Text(c, f.err).FontSize(sizeFoot).TextColor(p.Danger)
			}
		})
		ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).Children(func() {
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { f.open = false })
			label := locales.ProviderCreate()
			if f.base != nil {
				label = locales.Save()
			}
			primaryButton(c, p, label, strings.TrimSpace(f.name) != "").OnClick(a.saveRole)
		})
	})
}

// roleTargetRow is a machine a role runs on, with its agent, model, mode
// and memory.
func (a *App) roleTargetRow(c *ui.Context, p *palette, i int) {
	f := &a.role
	t := &f.targets[i]
	ui.Row(c).FillWidth().Wrap().Gap(6).AlignItems(ui.Center).Padding(8, 10).Radius(8).Border(1, p.Border).Children(func() {
		var machines []option
		for _, id := range a.catalog.MachineIDs {
			machines = append(machines, option{id, a.machineName(id)})
		}
		key := fmt.Sprintf("role/%d/", i)
		a.toolbarPicker(c, p, key+"machine", iconMonitor, a.machineName(t.machine), machines, t.machine, func(id string) {
			t.machine, t.agent, t.model, t.mode, t.memory = id, "", "", "", ""
			if ags := a.catalog.Agents[id]; len(ags) > 0 {
				t.agent = ags[0].ID
			}
		})
		var agents []option
		for _, ag := range a.catalog.Agents[t.machine] {
			agents = append(agents, option{ag.ID, ag.Name})
		}
		a.toolbarPicker(c, p, key+"agent", configMark(a.agent(t.machine, t.agent)), firstNonEmpty(a.agentName(t.machine, t.agent), locales.NoAgent()), agents, t.agent, func(id string) {
			t.agent, t.model, t.mode = id, "", ""
		})
		models := []option{{"", locales.DefaultModel()}}
		modelLabel := locales.DefaultModel()
		for _, m := range a.catalog.Capabilities[t.agent].Models {
			label := firstNonEmpty(m.Name, m.ModelID)
			models = append(models, option{m.ModelID, label})
			if m.ModelID == t.model {
				modelLabel = label
			}
		}
		a.toolbarPicker(c, p, key+"model", nil, modelLabel, models, t.model, func(id string) { t.model = id })
		agentType := ""
		if ag := a.agent(t.machine, t.agent); ag != nil {
			agentType = ag.AgentType
		}
		if modes := a.catalog.ModesFor(t.agent, agentType); len(modes) > 0 {
			options := []option{{"", locales.ModeDefault()}}
			modeLabel := locales.ModeDefault()
			for _, m := range modes {
				options = append(options, option{m.ID, m.Name})
				if m.ID == t.mode {
					modeLabel = m.Name
				}
			}
			a.toolbarPicker(c, p, key+"mode", iconShield, modeLabel, options, t.mode, func(id string) { t.mode = id })
		}
		if mems := a.catalog.Memories[t.machine]; len(mems) > 0 {
			options := []option{{"", locales.RoleNoMemory()}}
			memLabel := locales.RoleNoMemory()
			for _, m := range mems {
				options = append(options, option{m.MemoryID, m.Name})
				if m.MemoryID == t.memory {
					memLabel = m.Name
				}
			}
			a.toolbarPicker(c, p, key+"memory", iconBrain, memLabel, options, t.memory, func(id string) { t.memory = id })
		}
		ui.Spacer(c)
		if len(f.targets) > 1 {
			iconButton(c, p, iconX, locales.RoleRemoveMachine(), p.Hover).OnClick(func() {
				f.targets = append(f.targets[:i], f.targets[i+1:]...)
			})
		}
	})
}
