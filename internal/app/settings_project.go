package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/internal/mention"
	"github.com/lbr77/lody-mygo/locales"
)

// A project's window, as Lody's: where it is and who may use it, the
// scripts its worktrees run, the skills its agents find, and the MCP
// servers its new chats load.

type worktreeScripts struct {
	Scripts struct {
		Bash       string `json:"bash,omitempty"`
		PowerShell string `json:"powershell,omitempty"`
	} `json:"scripts"`
	TimeoutMs float64 `json:"timeoutMs,omitempty"`
}

type scriptEditor struct {
	loaded  bool
	loading bool
	config  worktreeScripts
	text    string
	saved   string
	saving  bool
	err     string
}

type projectWindow struct {
	open            bool
	id              string // the project's
	page            string
	setup           scriptEditor
	cleanup         scriptEditor
	history         map[string]*historyState // by provider
	historyProvider string
}

func (a *App) openProject(id string) {
	a.projectWin = projectWindow{open: true, id: id, page: "general"}
}

// projectWindowPages are the window's pages.
func projectWindowPages() []struct{ id, label, lead string } {
	return []struct{ id, label, lead string }{
		{"general", locales.ProjectPageGeneral(), locales.ProjectPageGeneralLead()},
		{"worktree", "Worktree", locales.ProjectPageWorktreeLead()},
		{"skills", locales.ProjectPageSkills(), locales.ProjectPageSkillsLead()},
		{"mcp", locales.ProjectPageMCP(), locales.ProjectPageMCPLead()},
		{"conversations", locales.ProjectPageConversations(), locales.ProjectPageConversationsLead()},
	}
}

func (a *App) projectWindow(c *ui.Context, p *palette) {
	w := &a.projectWin
	if !w.open {
		return
	}
	var pr *catalog.Project
	for i := range a.catalog.Projects {
		if a.catalog.Projects[i].ID == w.id {
			pr = &a.catalog.Projects[i]
		}
	}
	if pr == nil {
		w.open = false
		return
	}
	project := *pr
	ww, hh := c.Size()
	ui.DialogBase(c, &w.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.22))
		panel.Size(min(920, ww-80), min(640, hh-80)).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Clip().
			Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(240).Shrink(0).Background(p.Ground).BorderWidth(0, 1, 0, 0).BorderColor(p.Border).Padding(18, 10).Gap(2).Children(func() {
				ui.Column(c).Padding(0, 8, 14, 8).Gap(4).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, iconFolder).Size(16, 16).TextColor(p.Secondary)
						ui.Text(c, project.Name).FontSize(sizeBody).FontWeight(600).TextColor(p.Strong).SingleLine()
					})
					status := map[bool]string{true: locales.Online(), false: locales.Offline()}[a.presence.IsOnline(project.MachineID)]
					ui.Text(c, a.machineName(project.MachineID)+" · "+status).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
				})
				for _, pg := range projectWindowPages() {
					page := pg
					row := ui.ButtonBase(c.Key(pg.id)).Height(32).Padding(0, 10).Radius(8).Justify(ui.Start).Cursor(ui.CursorPointer).Label(pg.label).
						OnClick(func() { w.page = page.id })
					switch {
					case w.page == pg.id:
						row.Background(p.SideSelect)
					case row.Hovered():
						row.Background(p.SideHover)
					}
					row.Children(func() { ui.Text(c, page.label).FontSize(sizeSub).TextColor(p.Text) })
				}
			})
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Row(c).FillWidth().Padding(18, 14, 4, 24).Gap(12).AlignItems(ui.Start).Shrink(0).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
						for _, pg := range projectWindowPages() {
							if pg.id == w.page {
								ui.Text(c, pg.label).FontSize(16).FontWeight(600).TextColor(p.Strong)
								ui.Text(c, pg.lead).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Secondary)
							}
						}
					})
					iconButton(c, p, iconX, locales.Close(), p.Hover).OnClick(func() { w.open = false })
				})
				ui.Scroll(c).Grow(1).MinHeight(0).Padding(12, 24, 24, 24).Gap(14).Children(func() {
					online := a.presence.IsOnline(project.MachineID)
					if !online && (w.page == "worktree" || w.page == "skills" || w.page == "conversations") {
						ui.Text(c, locales.ProjectMachineOffline(a.machineName(project.MachineID))).FontSize(sizeFoot).TextColor(p.Secondary).
							Padding(10, 12).Radius(8).Background(p.Text.Alpha(0.04))
						if w.page == "skills" {
							return
						}
					}
					switch w.page {
					case "general":
						a.projectGeneral(c, p, project)
					case "worktree":
						a.projectScripts(c, p, project, "setup", &w.setup, online)
						a.projectScripts(c, p, project, "cleanup", &w.cleanup, online)
					case "skills":
						a.projectSkills(c, p, project)
					case "mcp":
						a.projectMCP(c, p, project)
					case "conversations":
						a.projectHistory(c, p, project)
					}
				})
			})
		})
	})
}

// ownsMachine reports whether the user owns a machine, as projects'
// settings are its owner's.
func (a *App) ownsMachine(id string) bool {
	m, known := a.visibleMachines()[id]
	return !known || a.account != nil && m.OwnerUserID == a.account.User.ID
}

func (a *App) projectGeneral(c *ui.Context, p *palette, pr catalog.Project) {
	project := pr
	settingRow(c, p, locales.ProjectFolder(), "", func() {
		ghostButton(c, p, locales.ProjectCopyPath()).Border(1, p.Border).OnClick(func() { mygo.Clipboard.WriteText(project.RootPath) })
	})
	ui.Text(c, pr.RootPath).Font(fontMono).FontSize(sizeFoot).TextColor(p.Secondary).Selectable().Margin(-8, 0, 0, 0)
	divider(c, p)
	_, local, _ := strings.Cut(pr.ID, ":local:")
	convex, ws := a.convex, a.workspaceID
	pf := a.fetch("projects", func(ctx context.Context) (any, error) {
		var out []visibleProject
		err := convex.Query(ctx, "localProjects:listVisibleLocalProjects", map[string]any{"workspaceId": ws}, &out)
		return out, err
	})
	shared := false
	if list, ok := pf.value.([]visibleProject); ok {
		for _, v := range list {
			if v.MachineID == pr.MachineID && v.LocalProjectID == local {
				shared = v.SharedWithTeam
			}
		}
	}
	mine := a.ownsMachine(pr.MachineID)
	settingRow(c, p, locales.MachinesShareFolder(), locales.ProjectShareHint(), func() {
		key := "project/" + pr.ID
		if on, flipped := a.toggle(c, key, shared, locales.MachinesShareFolder(), !mine); flipped {
			a.cloudCall("mutation", "localProjects:setLocalProjectSharedWithTeam",
				map[string]any{"workspaceId": ws, "machineId": project.MachineID, "localProjectId": local, "sharedWithTeam": on}, nil, "projects", key, nil)
		}
	})
	if !mine {
		return
	}
	divider(c, p)
	removing := a.catalog.Removing[pr.ID]
	hint := locales.ProjectDeleteSafe()
	if removing {
		hint = locales.ProjectRemoving()
	}
	settingRow(c, p, locales.ProjectRemove(), hint, func() {
		b := ghostButton(c, p, locales.ProjectRemove()).Border(1, p.Danger.Alpha(0.4)).Disabled(removing)
		b.OnClick(func() {
			a.confirm(locales.ProjectRemoveTitle(project.Name), locales.ProjectRemoveBody(), locales.ProjectRemove(), func() {
				a.projectWin.open = false
				a.removeProject(project)
			})
		})
	})
}

// shellFor is the script language of a machine: PowerShell on Windows.
func (a *App) shellFor(machine string) string {
	if strings.HasPrefix(strings.ToLower(a.catalog.MachineOS[machine]), "win") {
		return "powershell"
	}
	return "bash"
}

// projectScripts edits a script the machine runs for the project's
// worktrees: setup once one is made, cleanup before one is removed.
func (a *App) projectScripts(c *ui.Context, p *palette, pr catalog.Project, phase string, e *scriptEditor, online bool) {
	_, local, _ := strings.Cut(pr.ID, ":local:")
	conn, machine, user := a.conn, pr.MachineID, ""
	if a.account != nil {
		user = a.account.User.ID
	}
	shell := a.shellFor(machine)
	if !e.loaded && !e.loading && online && conn != nil {
		e.loading = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			raw, err := conn.ProjectControl(ctx, machine, map[string]any{"type": "local-project/get-worktree-" + phase, "localProjectId": local, "requestedByUserId": user}, 20*time.Second)
			var cfg *worktreeScripts
			if err == nil {
				err = json.Unmarshal(raw, &cfg)
			}
			a.update(func() {
				e.loading, e.loaded = false, true
				if err != nil {
					e.err = err.Error()
					return
				}
				if cfg != nil {
					e.config = *cfg
				}
				e.text = e.config.Scripts.Bash
				if shell == "powershell" {
					e.text = e.config.Scripts.PowerShell
				}
				e.saved = e.text
			})
		}()
	}
	title, lead := "Worktree Setup", locales.WorktreeSetupLead()
	if phase == "cleanup" {
		title, lead = "Worktree Cleanup", locales.WorktreeCleanupLead()
	}
	ui.Column(c.Key(phase)).FillWidth().Gap(8).Padding(14).Radius(10).Border(1, p.Border).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, title).FontSize(sizeSub).FontWeight(600).TextColor(p.Strong)
				ui.Text(c, lead).FontSize(sizeFoot).TextColor(p.Secondary)
			})
			ui.Text(c, shell).Font(fontMono).FontSize(sizeCaption).TextColor(p.Secondary).Padding(1, 6).Radius(5).Background(p.Text.Alpha(0.06))
		})
		if e.loading {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(12, 12)
				ui.Text(c, locales.Loading()).FontSize(sizeFoot).TextColor(p.Secondary)
			})
			return
		}
		placeholder := "npm install\ncp ../.env .env"
		if shell == "powershell" {
			placeholder = "npm install\nCopy-Item ..\\.env .env"
		}
		area(c, p, &e.text, placeholder, title, 5).Font(fontMono).Disabled(!online || !e.loaded)
		ui.Text(c, locales.WorktreeEnvHint()).FontSize(sizeCaption).LineHeight(1.45).TextColor(p.Tertiary)
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if e.err != "" {
				ui.Text(c, e.err).FontSize(sizeFoot).TextColor(p.Danger).Grow(1).Selectable()
			} else {
				ui.Spacer(c)
			}
			if e.saving {
				ui.Text(c, locales.WorktreeSaving()).FontSize(sizeFoot).TextColor(p.Secondary)
			}
			dirty := e.loaded && e.text != e.saved
			primaryButton(c, p, locales.Save(), dirty && !e.saving && online).OnClick(func() {
				cfg := e.config
				if shell == "powershell" {
					cfg.Scripts.PowerShell = e.text
				} else {
					cfg.Scripts.Bash = e.text
				}
				text := e.text
				e.saving, e.err = true, ""
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					config := map[string]any{"scripts": map[string]any{}}
					scripts := config["scripts"].(map[string]any)
					if cfg.Scripts.Bash != "" {
						scripts["bash"] = cfg.Scripts.Bash
					}
					if cfg.Scripts.PowerShell != "" {
						scripts["powershell"] = cfg.Scripts.PowerShell
					}
					if cfg.TimeoutMs > 0 {
						config["timeoutMs"] = int64(cfg.TimeoutMs)
					}
					_, err := conn.ProjectControl(ctx, machine, map[string]any{"type": "local-project/set-worktree-" + phase, "localProjectId": local, "config": config, "requestedByUserId": user}, 20*time.Second)
					a.update(func() {
						e.saving = false
						if err != nil {
							e.err = err.Error()
							return
						}
						e.config, e.saved = cfg, text
					})
				}()
			})
		})
	})
}

// projectSkills lists the skills the project's folder holds.
func (a *App) projectSkills(c *ui.Context, p *palette, pr catalog.Project) {
	_, local, _ := strings.Cut(pr.ID, ":local:")
	conn, machine, user := a.conn, pr.MachineID, ""
	if a.account != nil {
		user = a.account.User.ID
	}
	f := a.fetch("project-skills/"+pr.ID, func(ctx context.Context) (any, error) {
		raw, err := conn.ProjectControl(ctx, machine, map[string]any{"type": "local-project/list-skills", "localProjectId": local, "skillDirs": skillDirs, "requestedByUserId": user}, 20*time.Second)
		if err != nil {
			return nil, err
		}
		var res struct {
			Groups []mention.SkillGroup `json:"groups"`
		}
		err = json.Unmarshal(raw, &res)
		return res.Groups, err
	})
	if !pending(c, p, f) {
		return
	}
	groups, _ := f.value.([]mention.SkillGroup)
	shown := 0
	for _, g := range groups {
		if g.Scope != "project" || len(g.Skills) == 0 {
			continue
		}
		group := g
		shown++
		ui.Text(c.Key(g.Dir), g.Dir).Font(fontMono).FontSize(sizeCaption).TextColor(p.Tertiary).Margin(4, 0, 0, 0)
		card(c.Key(g.Dir+"/list"), p, func() {
			for _, s := range group.Skills {
				skill := s
				ui.Row(c.Key(s.RelativePath)).FillWidth().MinHeight(44).Padding(8, 14).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconBoxes).Size(15, 15).TextColor(p.Secondary)
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Text(c, skill.Name).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine()
						if skill.Description != "" {
							ui.Text(c, skill.Description).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
						}
					})
					ui.Text(c, skill.RelativePath).Font(fontMono).FontSize(sizeCaption).TextColor(p.Tertiary).SingleLine().MaxWidth(240)
				})
			}
		})
	}
	if shown == 0 {
		emptyCard(c, p, locales.ProjectNoSkills())
	}
}

// projectMCP chooses the MCP servers the project's new chats load; until
// a switch changes, the project follows the workspace's defaults.
func (a *App) projectMCP(c *ui.Context, p *palette, pr catalog.Project) {
	_, local, _ := strings.Cut(pr.ID, ":local:")
	key := catalog.ProjectMCPKey(pr.MachineID, local)
	own, custom := a.catalog.ProjectMCP[key]
	servers := a.catalog.MCPServers
	if len(servers) == 0 {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.ProjectMCPEmpty()).FontSize(sizeFoot).TextColor(p.Secondary).Grow(1)
			ghostButton(c, p, locales.ProjectMCPManage()).Border(1, p.Border).OnClick(func() {
				a.projectWin.open, a.settingsTab = false, "mcp"
			})
		})
		return
	}
	note := locales.ProjectMCPInherited()
	if custom {
		note = locales.ProjectMCPCustom()
	}
	ui.Text(c, note).FontSize(sizeFoot).TextColor(p.Secondary)
	enabled := func(m catalog.MCPServer) bool {
		if custom {
			return slices.Contains(own, m.ID)
		}
		return m.EnabledByDefault
	}
	mine := a.ownsMachine(pr.MachineID)
	card(c, p, func() {
		for _, m := range servers {
			server := m
			ui.Row(c.Key(m.ID)).FillWidth().MinHeight(48).Padding(8, 14).Gap(10).AlignItems(ui.Center).Children(func() {
				icon := iconSquareTerminal
				if server.Transport == "http" {
					icon = iconGlobe
				}
				ui.Icon(c, icon).Size(15, 15).TextColor(p.Secondary)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, server.Name).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine()
					detail := server.Description
					if server.Connection == nil {
						detail = locales.ProjectMCPNoConnection()
					}
					if detail != "" {
						ui.Text(c, detail).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
					}
				})
				tkey := "project-mcp/" + key + "/" + server.ID
				if on, flipped := a.toggle(c, tkey, enabled(server), locales.ProjectMCPToggle(server.Name), !mine); flipped {
					// The project's own list from now on, as Lody writes it.
					var ids []any
					for _, other := range servers {
						if other.ID == server.ID {
							if on {
								ids = append(ids, other.ID)
							}
						} else if enabled(other) {
							ids = append(ids, other.ID)
						}
					}
					if ids == nil {
						ids = []any{}
					}
					a.streamWrite("", func(f *flock.Flock) error {
						return f.Put([]any{"projectMcpDefault", key}, map[string]any{"ids": ids})
					}, func(error) { delete(a.toggles, tkey) })
				}
			})
		}
	})
	if !mine {
		ui.Text(c, locales.ProjectMCPNotOwner()).FontSize(sizeFoot).TextColor(p.Tertiary)
	}
}
