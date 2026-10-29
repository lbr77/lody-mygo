package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/locales"
)

// The account page, as Lody's: the profile and the sign-in methods linked
// to it, the user's machines with a way into managing each, and signing
// out. Managing machines is a page of its own under the account.

func brand(path string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor"><path d="` + path + `"/></svg>`))
}

// Brand marks of the sign-in providers, from Simple Icons (CC0).
var (
	iconBrandGitHub  = brand(`M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12`)
	iconBrandGoogle  = brand(`M12.48 10.92v3.28h7.84c-.24 1.84-.853 3.187-1.787 4.133-1.147 1.147-2.933 2.4-6.053 2.4-4.827 0-8.6-3.893-8.6-8.72s3.773-8.72 8.6-8.72c2.6 0 4.507 1.027 5.907 2.347l2.307-2.307C18.747 1.44 16.133 0 12.48 0 5.867 0 .307 5.387.307 12s5.56 12 12.173 12c3.573 0 6.267-1.173 8.373-3.36 2.16-2.16 2.84-5.213 2.84-7.667 0-.76-.053-1.467-.173-2.053H12.48z`)
	iconBrandApple   = brand(`M12.152 6.896c-.948 0-2.415-1.078-3.96-1.04-2.04.027-3.91 1.183-4.961 3.014-2.117 3.675-.546 9.103 1.519 12.09 1.013 1.454 2.208 3.09 3.792 3.039 1.52-.065 2.09-.987 3.935-.987 1.831 0 2.35.987 3.96.948 1.637-.026 2.676-1.48 3.676-2.948 1.156-1.688 1.636-3.325 1.662-3.415-.039-.013-3.182-1.221-3.22-4.857-.026-3.04 2.48-4.494 2.597-4.559-1.429-2.09-3.623-2.324-4.39-2.376-2-.156-3.675 1.09-4.61 1.09zM15.53 3.83c.843-1.012 1.4-2.427 1.245-3.83-1.207.052-2.662.805-3.532 1.818-.78.896-1.454 2.338-1.273 3.714 1.338.104 2.715-.688 3.559-1.701`)
	iconBrandDiscord = brand(`M20.317 4.37a19.79 19.79 0 0 0-4.885-1.515.074.074 0 0 0-.079.037c-.21.375-.444.865-.608 1.25a18.27 18.27 0 0 0-5.487 0 12.64 12.64 0 0 0-.617-1.25.077.077 0 0 0-.079-.037A19.74 19.74 0 0 0 3.677 4.37a.07.07 0 0 0-.032.028C.533 9.046-.32 13.58.099 18.058a.082.082 0 0 0 .031.056 19.9 19.9 0 0 0 5.993 3.03.078.078 0 0 0 .084-.028c.462-.63.874-1.295 1.226-1.994a.076.076 0 0 0-.042-.106 13.1 13.1 0 0 1-1.872-.892.077.077 0 0 1-.008-.128c.126-.094.252-.192.372-.291a.074.074 0 0 1 .078-.01c3.928 1.793 8.18 1.793 12.062 0a.074.074 0 0 1 .078.01c.12.098.246.198.373.292a.077.077 0 0 1-.006.127 12.3 12.3 0 0 1-1.873.892.077.077 0 0 0-.041.107c.36.698.772 1.362 1.225 1.993a.076.076 0 0 0 .084.028 19.84 19.84 0 0 0 6.002-3.03.077.077 0 0 0 .032-.054c.5-5.177-.838-9.674-3.549-13.66a.061.061 0 0 0-.031-.03zM8.02 15.33c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.956-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.956 2.418-2.157 2.418zm7.975 0c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.955-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.946 2.418-2.157 2.418z`)
)

var signInProviders = []struct {
	id   string
	name string
	icon *ui.SVG
}{
	{"github", "GitHub", iconBrandGitHub}, {"google", "Google", iconBrandGoogle},
	{"apple", "Apple", iconBrandApple}, {"discord", "Discord", iconBrandDiscord},
}

func (a *App) accountPage(c *ui.Context, p *palette) {
	settingsTitle(c, p, locales.SettingsAccount())
	if a.account == nil {
		return
	}
	u := a.account.User
	settingsHeading(c, p, locales.SettingsProfile())
	settingRow(c, p, locales.SettingsEmail(), "", func() { valueText(c, p, u.Email) })
	settingRow(c, p, locales.SettingsUsername(), "", func() { a.nameEditor(c, p) })
	settingRow(c, p, locales.SettingsAvatar(), "", func() { a.accountAvatar(c, p, 32) })
	settingRow(c, p, locales.SettingsLinkedAccounts(), "", func() { a.linkedAccounts(c, p) })
	divider(c, p)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		settingsHeading(c, p, locales.SettingsMyMachines())
		ui.Spacer(c)
		ui.Icon(c, iconHelp).Size(16, 16).TextColor(p.Secondary).Tooltip(locales.SettingsMyMachinesHelp())
	})
	a.myMachines(c, p)
	divider(c, p)
	a.cliTokens(c, p)
	divider(c, p)
	settingsHeading(c, p, locales.SettingsSignIn())
	a.passwordRow(c, p)
	settingRow(c, p, locales.SignOut(), locales.SettingsSignOutDescription(), func() {
		ghostButton(c, p, locales.SettingsSignOutButton()).Border(1, p.Border).OnClick(func() {
			a.settingsOpen = false
			a.signOut()
		})
	})
}

// nameEditor is the user's name, which the pencil makes editable.
func (a *App) nameEditor(c *ui.Context, p *palette) {
	if !a.editingName {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			valueText(c, p, a.account.User.Name)
			iconButton(c, p, iconPenLine, locales.SettingsEditName(), p.Hover).Size(24, 24).OnClick(func() {
				a.editingName, a.nameDraft = true, a.account.User.Name
			})
		})
		return
	}
	save := func() {
		name := strings.TrimSpace(a.nameDraft)
		a.editingName = false
		if name == "" || name == a.account.User.Name {
			return
		}
		api, token := a.api, a.tokenNow()
		a.account.User.Name = name
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			err := api.SetName(ctx, token, name)
			a.update(func() {
				if err != nil {
					a.settingsError = err.Error()
				}
				go a.refreshAccount()
			})
		}()
	}
	ui.TextInput(c, &a.nameDraft).Width(200).FontSize(sizeBody).AutoFocus().Label(locales.SettingsUsername()).OnSubmit(save).
		OnTextKey(func(k ui.TextKey) bool {
			if k.Key == ui.KeyEscape {
				a.editingName = false
				return true
			}
			return false
		})
}

// linkedAccounts marks the providers the user signs in with.
func (a *App) linkedAccounts(c *ui.Context, p *palette) {
	api, token := a.api, a.tokenNow()
	f := a.fetch("accounts", func(ctx context.Context) (any, error) { return api.LinkedAccounts(ctx, token) })
	linked := map[string]bool{}
	if list, ok := f.value.([]cloud.LinkedAccount); ok {
		for _, acc := range list {
			linked[acc.ProviderID] = true
		}
	}
	ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
		for _, pr := range signInProviders {
			ink := p.Text
			label := locales.SettingsLinked(pr.name)
			if !linked[pr.id] {
				ink, label = p.Text.Alpha(0.25), locales.SettingsNotLinked(pr.name)
			}
			icon := ui.Box(c.Key(pr.id)).Size(22, 22).Center().Tooltip(label).Label(label)
			if !linked[pr.id] {
				// Linking needs the provider's page, which lody.ai opens.
				icon.Cursor(ui.CursorPointer).OnClick(func() { a.webSettings("account") })
			}
			icon.Children(func() { ui.Icon(c, pr.icon).Size(20, 20).TextColor(ink) })
		}
	})
}

// myMachines lists the user's machines, each with a way into managing it.
func (a *App) myMachines(c *ui.Context, p *palette) {
	machines := a.visibleMachines()
	mine := 0
	for _, id := range a.catalog.MachineIDs {
		m, known := machines[id]
		if known && a.account != nil && m.OwnerUserID != a.account.User.ID {
			continue
		}
		mine++
		settingRow(c.Key(id), p, a.machineName(id), strings.Join(a.machineFacts(id, m, known), " · "), func() {
			ghostButton(c, p, locales.SettingsManage()).Border(1, p.Border).OnClick(func() {
				a.settingsTab = "machines"
				a.expanded["machine/"+id] = true
			})
		})
	}
	if mine == 0 {
		ui.Text(c, locales.SettingsNoMachine()).FontSize(sizeFoot).TextColor(p.Secondary)
	}
}

// visibleMachines is what Convex says of the machines the workspace sees:
// their owners and whether they are shared.
func (a *App) visibleMachines() map[string]visibleMachine {
	convex, ws := a.convex, a.workspaceID
	f := a.fetch("machines", func(ctx context.Context) (any, error) {
		var out []visibleMachine
		err := convex.Query(ctx, "machines:listVisibleMachines", map[string]any{"workspaceId": ws}, &out)
		return out, err
	})
	byID := map[string]visibleMachine{}
	if list, ok := f.value.([]visibleMachine); ok {
		for _, m := range list {
			byID[m.MachineID] = m
		}
	}
	return byID
}

// machineFacts is a machine's summary: online, its OS, shared or private,
// and how many agents and folders it has.
func (a *App) machineFacts(id string, m visibleMachine, known bool) []string {
	var facts []string
	if id == a.localMachine() {
		facts = append(facts, locales.MachineThis())
	}
	if a.presence.Live {
		facts = append(facts, map[bool]string{true: locales.Online(), false: locales.Offline()}[a.presence.IsOnline(id)])
	}
	if os := a.catalog.MachineOS[id]; os != "" {
		facts = append(facts, os)
	}
	if known {
		facts = append(facts, map[bool]string{true: locales.MachineShared(), false: locales.MachinePrivate()}[m.SharedWithTeam])
	}
	facts = append(facts, locales.SettingsAgentCount(len(a.catalog.Agents[id])), locales.SettingsFolderCount(len(a.machineProjects(id))))
	return facts
}

// machineProjects are the local projects of a machine.
func (a *App) machineProjects(id string) []catalog.Project {
	var out []catalog.Project
	for _, pr := range a.catalog.Projects {
		if pr.MachineID == id && strings.Contains(pr.ID, ":local:") {
			out = append(out, pr)
		}
	}
	return out
}

type visibleProject struct {
	MachineID      string `json:"machineId"`
	LocalProjectID string `json:"localProjectId"`
	OwnerUserID    string `json:"ownerUserId"`
	SharedWithTeam bool   `json:"sharedWithTeam"`
}

// machinesPage manages machines, as Lody's: those shared with the team,
// then the user's own that aren't, each a card that opens to its sharing,
// folders and agents.
func (a *App) machinesPage(c *ui.Context, p *palette) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		iconButton(c, p, iconChevronLeft, locales.NavBack(), p.Hover).OnClick(func() { a.settingsTab = "account" })
		settingsTitle(c, p, locales.SettingsMachines())
	})
	a.settingsErrorLine(c, p)
	machines := a.visibleMachines()
	convex, ws := a.convex, a.workspaceID
	pf := a.fetch("projects", func(ctx context.Context) (any, error) {
		var out []visibleProject
		err := convex.Query(ctx, "localProjects:listVisibleLocalProjects", map[string]any{"workspaceId": ws}, &out)
		return out, err
	})
	shared := map[string]bool{}
	if list, ok := pf.value.([]visibleProject); ok {
		for _, pr := range list {
			shared[pr.MachineID+":local:"+pr.LocalProjectID] = pr.SharedWithTeam
		}
	}
	var team, private []string
	for _, id := range a.catalog.MachineIDs {
		m, known := machines[id]
		if known && m.SharedWithTeam || known && a.account != nil && m.OwnerUserID != a.account.User.ID {
			team = append(team, id)
		} else {
			private = append(private, id)
		}
	}
	if len(team) == 0 {
		ui.Text(c, locales.MachinesNoneShared()).FontSize(sizeSub).TextColor(p.Secondary).FillWidth().TextAlign(ui.Center).Padding(16, 0)
	}
	for _, id := range team {
		a.machineCard(c.Key(id), p, id, machines, shared)
	}
	if len(private) == 0 {
		return
	}
	ui.Column(c).Gap(2).Margin(12, 0, 0, 0).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			ui.Text(c, locales.MachinesYourPrivate()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
			ui.Text(c, fmt.Sprint(len(private))).FontSize(sizeSub).TextColor(p.Secondary)
		})
		ui.Text(c, locales.MachinesPrivateHint()).FontSize(sizeFoot).TextColor(p.Secondary)
	})
	for _, id := range private {
		a.machineCard(c.Key(id), p, id, machines, shared)
	}
}

func pill(c *ui.Context, p *palette, icon *ui.SVG, label string) {
	ui.Row(c).Height(22).Padding(0, 7).Radius(5).Gap(4).AlignItems(ui.Center).Background(p.Text.Alpha(0.06)).Shrink(0).Children(func() {
		if icon != nil {
			ui.Icon(c, icon).Size(12, 12).TextColor(p.Secondary)
		}
		ui.Text(c, label).FontSize(sizeCaption).FontWeight(500).TextColor(p.Text)
	})
}

// machineCard is a machine, folded to its summary until opened.
func (a *App) machineCard(c *ui.Context, p *palette, id string, machines map[string]visibleMachine, shared map[string]bool) {
	m, known := machines[id]
	mine := !known || a.account != nil && m.OwnerUserID == a.account.User.ID
	key := "machine/" + id
	open := a.expanded[key]
	projects := a.machineProjects(id)
	ws := a.workspaceID
	ui.Column(c).FillWidth().Radius(12).Border(1, p.Border).Background(p.Canvas).Clip().Children(func() {
		head := ui.Row(c).FillWidth().Height(48).Padding(0, 14).Gap(10).AlignItems(ui.Center).Cursor(ui.CursorPointer).
			Label(a.machineName(id)).OnClick(func() { a.expanded[key] = !a.expanded[key] })
		head.Children(func() {
			dot := p.Tertiary
			if a.presence.IsOnline(id) {
				dot = p.Success
			}
			ui.Box(c).Size(8, 8).Radius(4).Background(dot).Shrink(0)
			ui.Text(c, a.machineName(id)).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
			if id == a.localMachine() {
				pill(c, p, nil, locales.MachineThis())
			}
			if known && !m.SharedWithTeam {
				pill(c, p, iconLock, locales.MachinePrivate())
			}
			if os := a.catalog.MachineOS[id]; os != "" {
				pill(c, p, iconLaptop, os)
			}
			if v := a.catalog.MachineCLI[id]; v != "" {
				pill(c, p, nil, "v"+v)
			}
			ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
				ui.Icon(c, iconFolder).Size(12, 12).TextColor(p.Secondary)
				ui.Text(c, locales.SettingsFolderCount(len(projects))).FontSize(sizeCaption).TextColor(p.Secondary)
			})
			ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
				ui.Icon(c, iconBot).Size(12, 12).TextColor(p.Secondary)
				ui.Text(c, locales.SettingsAgentCount(len(a.catalog.Agents[id]))).FontSize(sizeCaption).TextColor(p.Secondary)
			})
			chevron := iconChevronDown
			if open {
				chevron = iconChevronUp
			}
			ui.Icon(c, chevron).Size(14, 14).TextColor(p.Secondary)
			byUser, _ := a.members()
			owner := byUser[m.OwnerUserID]
			avatar(c, p, firstNonEmpty(owner.User.Name, a.memberName(m.OwnerUserID, byUser)), 22, 11)
		})
		if !open {
			return
		}
		ui.Column(c).FillWidth().Padding(4, 14, 14, 14).Gap(14).Children(func() {
			if mine {
				a.machineActions(c, p, id, m, known)
			}
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconFolder).Size(14, 14).TextColor(p.Secondary)
					ui.Text(c, locales.MachinesFolders()).FontSize(sizeFoot).TextColor(p.Secondary)
				})
				if len(projects) == 0 {
					ui.Text(c, locales.MachinesNoFolders()).FontSize(sizeFoot).TextColor(p.Tertiary)
					return
				}
				ui.Text(c, locales.MachinesFolderHint()).FontSize(sizeFoot).TextColor(p.Secondary)
				for _, pr := range projects {
					project := pr
					ui.Row(c.Key(pr.ID)).FillWidth().Padding(10, 14).Radius(8).Gap(10).AlignItems(ui.Center).Background(p.Text.Alpha(0.03)).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Children(func() {
							ui.Text(c, project.Name).FontSize(sizeSub).TextColor(p.Text).SingleLine()
							ui.Text(c, project.RootPath).Font(fontMono).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine()
						})
						if !mine {
							return
						}
						ui.Text(c, locales.MachinesShareFolder()).FontSize(sizeFoot).TextColor(p.Secondary)
						_, local, _ := strings.Cut(project.ID, ":local:")
						tkey := "project/" + project.ID
						if on, flipped := a.toggle(c, tkey, shared[project.ID], locales.MachinesShareFolder(), false); flipped {
							a.cloudCall("mutation", "localProjects:setLocalProjectSharedWithTeam",
								map[string]any{"workspaceId": ws, "machineId": id, "localProjectId": local, "sharedWithTeam": on}, nil, "projects", tkey, nil)
						}
					})
				}
			})
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconBot).Size(14, 14).TextColor(p.Secondary)
					ui.Text(c, locales.MachinesAgents()).FontSize(sizeFoot).TextColor(p.Secondary).Grow(1)
					ghostButton(c, p, locales.MachinesManageAgents()).Border(1, p.Border).OnClick(func() {
						a.settingsTab, a.agentsMachine = "agents", id
					})
				})
				agents := a.catalog.Agents[id]
				if len(agents) == 0 {
					ui.Text(c, locales.MachinesNoAgents()).FontSize(sizeFoot).TextColor(p.Tertiary)
					return
				}
				ui.Row(c).FillWidth().Wrap().Gap(6).Children(func() {
					for _, ag := range agents {
						pill(c.Key(ag.ID), p, agentIcon(ag.CLIType), ag.Name)
					}
				})
			})
		})
	})
}

// agentIcon is the mark of an agent's kind: a terminal for custom
// commands.
func agentIcon(cliType string) *ui.SVG {
	if cliType == "custom" {
		return iconSquareTerminal
	}
	return iconBot
}

type cliKey struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	KeyPreview  string `json:"keyPreview"`
	KeyStart    string `json:"keyStart"`
	Note        string `json:"note"`
	Source      string `json:"source"`
	CreatedAt   any    `json:"createdAt"`
	LastRequest any    `json:"lastRequest"`
}

type tokenForm struct {
	open    bool
	note    string
	created string // the key just made, shown once
	err     string
}

// stampDay writes a time the backend sends as ms or as an ISO string.
func stampDay(v any) string {
	switch t := v.(type) {
	case float64:
		return formatDay(t)
	case string:
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			return formatDay(float64(parsed.UnixMilli()))
		}
	}
	return ""
}

// cliTokens are the keys the Lody CLI signs in with.
func (a *App) cliTokens(c *ui.Context, p *palette) {
	api, token := a.api, a.tokenNow()
	f := a.fetch("cli-keys", func(ctx context.Context) (any, error) {
		var out struct {
			Records []cliKey `json:"records"`
		}
		err := api.Call(ctx, token, "/api/cli/api-key/list", map[string]any{}, &out)
		return out.Records, err
	})
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		settingsHeading(c, p, "CLI Token")
		ui.Spacer(c)
		ghostButton(c, p, locales.TokenCreate()).OnClick(func() { a.tokens = tokenForm{open: true} })
	})
	keys, _ := f.value.([]cliKey)
	switch {
	case f.loading:
		pending(c, p, f)
	case len(keys) == 0:
		ui.Text(c, locales.TokenNone()).FontSize(sizeFoot).TextColor(p.Secondary)
	}
	for _, k := range keys {
		key := k
		facts := []string{firstNonEmpty(k.KeyPreview, k.KeyStart+"…")}
		if k.Source != "" {
			facts = append(facts, k.Source)
		}
		if d := stampDay(k.CreatedAt); d != "" {
			facts = append(facts, locales.TokenCreated(d))
		}
		if d := stampDay(k.LastRequest); d != "" {
			facts = append(facts, locales.TokenUsed(d))
		}
		settingRow(c.Key(k.ID), p, firstNonEmpty(k.Note, k.Name, locales.TokenUnnamed()), strings.Join(facts, " · "), func() {
			iconButton(c, p, iconTrash, locales.TokenRevoke(), p.Hover).OnClick(func() {
				a.confirm(locales.TokenRevokeTitle(), locales.TokenRevokeBody(), locales.TokenRevoke(), func() {
					a.authCall("/api/cli/api-key/revoke", map[string]any{"keyId": key.ID}, "cli-keys", nil)
				})
			})
		})
	}
	a.tokenDialog(c, p)
}

func (a *App) tokenDialog(c *ui.Context, p *palette) {
	t := &a.tokens
	if !t.open {
		return
	}
	sheet(c, p, &t.open, 460, func() {
		sheetHeader(c, p, locales.TokenCreate(), locales.TokenLead(), func() { t.open = false })
		ui.Column(c).FillWidth().Padding(8, 20).Gap(12).Children(func() {
			if t.created != "" {
				ui.Text(c, locales.TokenOnce()).FontSize(sizeFoot).TextColor(p.Warning)
				ui.Row(c).FillWidth().Padding(8, 10).Radius(8).Gap(8).AlignItems(ui.Center).Background(p.Text.Alpha(0.05)).Children(func() {
					ui.Text(c, t.created).Font(fontMono).FontSize(sizeFoot).TextColor(p.Text).Grow(1).MinWidth(0).Selectable()
					created := t.created
					iconButton(c, p, iconCopy, locales.Copy(), p.Hover).OnClick(func() { mygo.Clipboard.WriteText(created) })
				})
				return
			}
			field(c, p, locales.TokenNote(), "", func() { input(c, p, &t.note, locales.TokenNotePlaceholder(), locales.TokenNote()).AutoFocus() })
			if t.err != "" {
				ui.Text(c, t.err).FontSize(sizeFoot).TextColor(p.Danger)
			}
		})
		ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).Children(func() {
			if t.created != "" {
				primaryButton(c, p, locales.Done(), true).OnClick(func() { t.open = false })
				return
			}
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { t.open = false })
			primaryButton(c, p, locales.TokenCreate(), true).OnClick(func() {
				api, token, note := a.api, a.tokenNow(), strings.TrimSpace(t.note)
				if len([]rune(note)) > 160 {
					note = string([]rune(note)[:160])
				}
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					var out struct {
						APIKey string `json:"apiKey"`
					}
					err := api.Call(ctx, token, "/api/cli/api-key/create", map[string]any{"note": note, "source": "manual"}, &out)
					a.update(func() {
						if err != nil {
							t.err = err.Error()
							return
						}
						t.created = out.APIKey
						a.refetchKey("cli-keys")
					})
				}()
			})
		})
	})
}

// passwordRow sets up email sign-in, or changes the password on lody.ai.
func (a *App) passwordRow(c *ui.Context, p *palette) {
	api, token := a.api, a.tokenNow()
	f := a.fetch("accounts", func(ctx context.Context) (any, error) { return api.LinkedAccounts(ctx, token) })
	hasPassword := false
	if list, ok := f.value.([]cloud.LinkedAccount); ok {
		for _, acc := range list {
			hasPassword = hasPassword || acc.ProviderID == "credential"
		}
	}
	if hasPassword {
		settingRow(c, p, locales.PasswordTitle(), locales.PasswordChangeHelp(), func() {
			outlineButton(c, p, iconExternal, locales.PasswordChange()).OnClick(func() { a.webSettings("account") })
		})
		return
	}
	desc := locales.PasswordSetHelp()
	if a.passwordNotice != "" {
		desc = a.passwordNotice
	}
	settingRow(c, p, locales.PasswordTitle(), desc, func() {
		ghostButton(c, p, locales.PasswordSet()).Border(1, p.Border).OnClick(func() {
			email := a.account.User.Email
			a.authCall("/api/auth/request-password-reset", map[string]any{"email": email, "redirectTo": "https://lody.ai/reset-password"}, "", func(err error) {
				if err != nil {
					a.settingsError = err.Error()
					return
				}
				a.passwordNotice = locales.PasswordSent(email)
			})
		})
	})
}

// machineActions are what a machine's owner may do with it: name it,
// share it, sign it out, or take it out of the workspace while offline.
func (a *App) machineActions(c *ui.Context, p *palette, id string, m visibleMachine, known bool) {
	ws := a.workspaceID
	key := "machine/" + id
	convex := a.convex
	cf := a.fetch("credentials/"+id, func(ctx context.Context) (any, error) {
		var out struct {
			RevocableCount float64 `json:"revocableCount"`
		}
		err := convex.Query(ctx, "machineCredentials:getMachineCredentialState", map[string]any{"workspaceId": ws, "machineId": id}, &out)
		return out.RevocableCount, err
	})
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		if a.renamingMachine == id {
			save := func() {
				name := strings.TrimSpace(a.machineDraft)
				a.renamingMachine = ""
				if name == "" || name == a.machineName(id) {
					return
				}
				a.catalogAction(locales.MachinesRename(), func(ctx context.Context, sync *catalog.Sync) error { return sync.RenameMachine(ctx, id, name) })
			}
			ui.TextInput(c, &a.machineDraft).Width(220).FontSize(sizeSub).AutoFocus().Label(locales.MachinesRename()).OnSubmit(save)
		} else {
			b := ui.ButtonBase(c).Height(28).Padding(0, 8).Radius(8).Gap(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(locales.MachinesRename()).
				OnClick(func() { a.renamingMachine, a.machineDraft = id, a.machineName(id) })
			if b.Hovered() {
				b.Background(p.Hover)
			}
			b.Children(func() {
				ui.Icon(c, iconPenLine).Size(14, 14).TextColor(p.Secondary)
				ui.Text(c, locales.MachinesRename()).FontSize(sizeFoot).TextColor(p.Text)
			})
		}
		ui.Spacer(c)
		if known {
			ui.Text(c, locales.MachinesShareSwitch()).FontSize(sizeFoot).TextColor(p.Secondary)
			if on, flipped := a.toggle(c, key, m.SharedWithTeam, locales.MachinesShareSwitch(), false); flipped {
				a.cloudCall("mutation", "machines:setMachineSharedWithTeam",
					map[string]any{"workspaceId": ws, "machineId": id, "sharedWithTeam": on}, nil, "machines", key, nil)
			}
		}
		if n, _ := cf.value.(float64); n > 0 {
			iconButton(c, p, iconUnplug, locales.MachinesRevoke(), p.Hover).OnClick(func() {
				a.confirm(locales.MachinesRevokeTitle(a.machineName(id)), locales.MachinesRevokeBody(), locales.MachinesRevoke(), func() {
					a.cloudCall("mutation", "machineCredentials:revokeMachineCredentials", map[string]any{"workspaceId": ws, "machineId": id}, nil, "credentials/"+id, "", nil)
				})
			})
		}
		offline := !a.presence.IsOnline(id)
		remove := ui.ButtonBase(c).Height(28).Padding(0, 8).Radius(8).Gap(6).AlignItems(ui.Center).Label(locales.MachinesRemove()).Disabled(!offline)
		if offline {
			remove.Cursor(ui.CursorPointer).OnClick(func() {
				a.confirm(locales.MachinesRemoveTitle(a.machineName(id)), locales.MachinesRemoveBody(), locales.MachinesRemove(), func() {
					a.catalogAction(locales.MachinesRemove(), func(ctx context.Context, sync *catalog.Sync) error { return sync.RemoveMachine(ctx, id) })
				})
			})
		} else {
			remove.Opacity(0.5).Tooltip(locales.MachinesRemoveOnline())
		}
		remove.Children(func() {
			ui.Icon(c, iconLogOutAlt).Size(14, 14).TextColor(p.Danger)
			ui.Text(c, locales.MachinesRemove()).FontSize(sizeFoot).TextColor(p.Danger)
		})
	})
}
