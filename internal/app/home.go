package app

import (
	"github.com/lbr77/lody-mygo/locales"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// homeView is New chat: a heading over the composer, the machine and
// project to run on just above it, and the agent and model in its footer.
func (a *App) homeView(c *ui.Context, p *palette) {
	left := a.barInset(8)
	ui.Row(c).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Padding(0, 8, 0, left).DragWindow().Children(func() {
		if a.sidebarHidden {
			iconButton(c, p, iconSidebar, locales.SidebarToggle(), p.Hover).OnClick(func() { a.sidebarHidden = false })
		}
	})
	ui.Column(c).Grow(1).MinHeight(0).Justify(ui.Center).AlignItems(ui.Center).Padding(0, 24).Gap(20).Children(func() {
		ui.Text(c, homeHeading(time.Now())).FontSize(36).LineHeight(40.0 / 36).FontWeight(600).LetterSpacing(-0.9).TextColor(p.Strong).TextAlign(ui.Center)
		if len(a.catalog.MachineIDs) == 0 {
			ui.Column(c).Gap(8).AlignItems(ui.Center).MaxWidth(420).Children(func() {
				msg := locales.HomeSyncing()
				if a.synced {
					msg = locales.HomeNoMachine()
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
	a.sharingNotice(c, p)
	a.composerBox(c, p, composerOptions{
		canSend:  agent != nil && !a.sending,
		onSend:   a.startChat,
		sendSize: 32,
		above:    func() { a.homeTargets(c, p) },
		footer: func() {
			agentLabel := locales.NoAgent()
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
		return locales.HomeShip()
	}
	return locales.HomeWork()
}

// homeTargets is the row above the composer: the machine, then the
// project.
func (a *App) homeTargets(c *ui.Context, p *palette) {
	ui.Row(c).Gap(4).AlignItems(ui.Center).Padding(0, 2).Children(func() {
		open := a.popover("machine")
		machine := ui.ButtonBase(c).Height(24).Padding(0, 8).Radius(6).Gap(4).Border(1, p.Border).Background(p.Composer).Cursor(ui.CursorPointer).
			Label(locales.HomeMachine()).OnClick(func() { *open = !*open }).Transition(colorFade)
		pressable(machine, 0.97)
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
		a.optionList(c, p, machine, open, append(a.machineOptions(), option{addMachineOption, "+ " + locales.PairingAddMachine()}), a.newChat.machine, func(id string) {
			if id == addMachineOption {
				a.addMachine()
				return
			}
			a.newChat.machine, a.newChat.project = id, ""
			a.pickNewChatDefaults()
		})

		openProject := a.popover("project")
		label := locales.SelectProject()
		if pr := a.catalog.Project(a.newChat.project); pr != nil {
			label = pr.Name
		}
		project := ui.ButtonBase(c).Height(24).Padding(0, 8).Radius(12).Gap(4).Cursor(ui.CursorPointer).
			Label(locales.HomeProject()).OnClick(func() { *openProject = !*openProject }).Transition(colorFade)
		pressable(project, 0.97)
		if project.Hovered() {
			project.Background(p.Text.Alpha(0.06))
		}
		project.Children(func() {
			ui.Icon(c, iconFolderOpen).Size(16, 16).TextColor(p.Text.Alpha(0.8))
			ui.Text(c, label).FontSize(12.6).TextColor(p.Text.Alpha(0.8)).SingleLine().MaxWidth(240)
		})
		a.optionList(c, p, project, openProject, a.projectOptions(), a.newChat.project, func(id string) {
			a.newChat.project, a.newChat.branch = id, ""
		})
		a.workdirControls(c, p)
	})
}

// addMachineOption is the machine list's last row, which connects one.
const addMachineOption = "\x00add-machine"

func (a *App) machineOptions() []option {
	var out []option
	for _, id := range a.catalog.MachineIDs {
		label := a.machineLabel(id)
		if !a.presence.IsOnline(id) {
			label = locales.MachineOfflineOption(label)
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
	out := []option{{"", locales.NoProject()}}
	for _, pr := range a.catalog.Projects {
		if pr.MachineID == a.newChat.machine && strings.Contains(pr.ID, ":local:") {
			out = append(out, option{pr.ID, pr.Name})
		}
	}
	return out
}
