package app

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// homeView is New chat: a heading over the composer, the machine and
// project to run on just above it, and the agent and model in its footer.
func (a *App) homeView(c *ui.Context, p *palette) {
	left := float32(8)
	if a.sidebarHidden {
		left = 76
	}
	ui.Row(c).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Padding(0, 8, 0, left).DragWindow().Children(func() {
		if a.sidebarHidden {
			iconButton(c, p, iconSidebar, "Toggle Sidebar", p.Hover).OnClick(func() { a.sidebarHidden = false })
		}
	})
	ui.Column(c).Grow(1).MinHeight(0).Justify(ui.Center).AlignItems(ui.Center).Padding(0, 24).Gap(20).Children(func() {
		ui.Text(c, homeHeading(time.Now())).FontSize(36).LineHeight(40.0 / 36).FontWeight(600).LetterSpacing(-0.9).TextColor(p.Strong).TextAlign(ui.Center)
		if len(a.catalog.MachineIDs) == 0 {
			ui.Column(c).Gap(8).AlignItems(ui.Center).MaxWidth(420).Children(func() {
				msg := "Syncing your workspace…"
				if a.synced {
					msg = "No machine is connected to this workspace yet. Run this on a computer to connect it:"
				}
				ui.Text(c, msg).FontSize(sizeBody).TextColor(p.Secondary).TextAlign(ui.Center)
				if a.synced {
					ui.Text(c, "npx lody daemon start").Font(fontMono).FontSize(sizeSub).TextColor(p.Text).Padding(6, 10).Radius(8).Background(p.CodeBg).Selectable()
				}
			})
		}
	})
	if len(a.catalog.MachineIDs) == 0 {
		return
	}
	agent := a.agent(a.newChat.machine, a.newChat.agent)
	a.composerBox(c, p, composerOptions{
		canSend:  agent != nil && !a.sending,
		onSend:   a.startChat,
		sendSize: 32,
		above:    func() { a.homeTargets(c, p) },
		footer: func() {
			agentLabel := "No agent"
			if agent != nil {
				agentLabel = agent.Name
			}
			a.picker(c, p, "agent", iconBot, agentLabel, a.agentOptions(), func(id string) {
				a.newChat.agent = id
				delete(a.models, "")
				delete(a.modes, "")
			})
			if agent != nil {
				a.runConfig(c, p, "model/new", agent.ID, a.models[""], nil, func(id string) { a.models[""] = id })
				a.modeButton(c, p, "mode/new", a.catalog.ModesFor(agent.ID, agent.AgentType), a.modes[""], func(id string) { a.modes[""] = id })
			}
		},
	})
}

// homeHeading alternates Lody's two headings by day.
func homeHeading(now time.Time) string {
	if now.YearDay()%2 == 0 {
		return "Let's ship something"
	}
	return "What should we work on?"
}

// homeTargets is the row above the composer: the machine, then the
// project.
func (a *App) homeTargets(c *ui.Context, p *palette) {
	ui.Row(c).Gap(4).AlignItems(ui.Center).Padding(0, 2).Children(func() {
		open := a.popover("machine")
		machine := ui.ButtonBase(c).Height(24).Padding(0, 8).Radius(6).Gap(4).Border(1, p.Border).Background(p.Composer).Cursor(ui.CursorPointer).
			Label("Machine").OnClick(func() { *open = !*open })
		if machine.Hovered() {
			machine.Background(p.Hover)
		}
		machine.Children(func() {
			ui.Icon(c, iconMonitor).Size(14, 14).TextColor(p.Secondary)
			ui.Text(c, a.machineLabel(a.newChat.machine)).FontSize(sizeFoot).TextColor(p.Text).SingleLine()
			if !a.presence.IsOnline(a.newChat.machine) {
				offlinePill(c, p)
			}
		})
		a.optionList(c, p, machine, open, a.machineOptions(), a.newChat.machine, func(id string) {
			a.newChat.machine, a.newChat.project = id, ""
			a.pickNewChatDefaults()
		})

		openProject := a.popover("project")
		label := "Select a project"
		if pr := a.catalog.Project(a.newChat.project); pr != nil {
			label = pr.Name
		}
		project := ui.ButtonBase(c).Height(24).Padding(0, 8).Radius(12).Gap(4).Cursor(ui.CursorPointer).
			Label("Project").OnClick(func() { *openProject = !*openProject })
		if project.Hovered() {
			project.Background(p.Text.Alpha(0.06))
		}
		project.Children(func() {
			ui.Icon(c, iconFolderOpen).Size(16, 16).TextColor(p.Text.Alpha(0.8))
			ui.Text(c, label).FontSize(12.6).TextColor(p.Text.Alpha(0.8)).SingleLine().MaxWidth(240)
		})
		a.optionList(c, p, project, openProject, a.projectOptions(), a.newChat.project, func(id string) { a.newChat.project = id })
	})
}

func (a *App) machineOptions() []option {
	var out []option
	for _, id := range a.catalog.MachineIDs {
		label := a.machineLabel(id)
		if !a.presence.IsOnline(id) {
			label += " (offline)"
		}
		out = append(out, option{id, label})
	}
	return out
}

func (a *App) agentOptions() []option {
	var out []option
	for _, ag := range a.catalog.Agents[a.newChat.machine] {
		out = append(out, option{ag.ID, ag.Name})
	}
	return out
}

func (a *App) projectOptions() []option {
	out := []option{{"", "No project"}}
	for _, pr := range a.catalog.Projects {
		if pr.MachineID == a.newChat.machine && strings.Contains(pr.ID, ":local:") {
			out = append(out, option{pr.ID, pr.Name})
		}
	}
	return out
}
