package app

import (
	"context"
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
	stage := ui.Column(c.Key("home-stage")).Grow(1).MinHeight(0).Justify(ui.Center).AlignItems(ui.Center).Padding(0, 24).Gap(20)
	// A board opening, switching or folding re-centres the stage: the
	// heading and the line travel there over 240ms, as Lody's morph.
	a.homeBoardMorph(c, firstShown(stage, "stage"))
	stage.Children(func() {
		ui.Text(c.Key("home-heading"), homeHeading(time.Now())).FontSize(36).LineHeight(40.0 / 36).FontWeight(600).LetterSpacing(-0.9).TextColor(p.Strong).
			TextAlign(ui.Center).Transition(homeTravel)
		switch {
		case len(a.catalog.MachineIDs) > 0:
			a.syncNewChatMemory()
			a.homeDashboard(c, p)
		case a.synced:
			a.noMachineHint(c, p)
		default:
			ui.Text(c, locales.HomeSyncing()).FontSize(sizeBody).TextColor(p.Secondary).TextAlign(ui.Center)
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
			pickAgent := func(id string) {
				a.newChat.agent = id
				delete(a.models, "")
				delete(a.modes, "")
			}
			agents := a.agentOptions()
			a.picker(c, p, "agent", configMark(agent), agentLabel, agents, pickAgent)
			providers := &cycleTarget{current: a.newChat.agent, pick: pickAgent}
			for _, o := range agents {
				providers.values = append(providers.values, o.id)
			}
			a.surfNext.provider = providers
			if agent != nil {
				pickModel := func(id string) { a.models[""] = id }
				a.runConfig(c, p, "model/new", agent.ID, a.models[""], nil, pickModel)
				a.runOptions(c, p, "", agent.ID, a.models[""], nil)
				modes := a.catalog.ModesFor(agent.ID, agent.AgentType)
				pickMode := func(id string) { a.modes[""] = id }
				a.modeButton(c, p, "mode/new", modes, a.modes[""], pickMode)
				a.surfNext.model = a.modelCycle(agent.ID, a.models[""], pickModel)
				a.surfNext.mode = modeCycle(modes, a.modes[""], pickMode)
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
		} else if repo, ok := strings.CutPrefix(a.newChat.project, "github:"); ok {
			label = repo
		}
		// With a project chosen, the pointer on the pill swaps its folder
		// for a clear button in the same place, each fading over 150ms, as
		// Tailwind's transition-opacity.
		group := ui.Row(c.Key("project-group")).AlignItems(ui.Center)
		chosen := a.newChat.project != ""
		var project ui.Element
		group.Children(func() {
			project = ui.ButtonBase(c.Key("project")).Height(24).Padding(0, 8).Radius(12).Gap(4).Cursor(ui.CursorPointer).
				Label(locales.HomeProject()).OnClick(func() { *openProject = !*openProject }).Transition(colorFade)
			if project.Hovered() {
				project.Background(p.Text.Alpha(0.06))
			}
			swap := float32(0)
			if chosen {
				swap = group.AnimateWith("clear", b2f(group.Hovered() || group.FocusWithin()), 150*time.Millisecond, twEase)
			}
			project.Children(func() {
				ui.Icon(c, iconFolderOpen).Size(16, 16).TextColor(p.Text.Alpha(0.8)).Opacity(1 - swap)
				ui.Text(c, label).FontSize(12.6).TextColor(p.Text.Alpha(0.8)).SingleLine().MaxWidth(240)
			})
			if !chosen {
				return
			}
			clear := ui.ButtonBase(c.Key("clear")).Absolute().Left(8).Top(4).Size(16, 16).Radius(8).Center().Cursor(ui.CursorPointer).
				Label(locales.ProjectPickerClear()).Opacity(swap)
			ink := p.Secondary
			if clear.Hovered() {
				clear.Background(p.Hover)
				ink = p.Text
			}
			clear.Children(func() { ui.Icon(c, iconX).Size(12, 12).TextColor(ink) })
			clear.OnClick(func() {
				a.newChat.project, a.newChat.branch = "", ""
				*openProject = false
			})
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
	// The workspace's GitHub repositories, which the machine checks out.
	for _, r := range a.githubRepos() {
		out = append(out, option{"github:" + r.FullName, r.FullName})
	}
	return out
}

// workspaceRepository is a repository the workspace's GitHub App reaches.
type workspaceRepository struct {
	ID       float64 `json:"id"`
	Name     string  `json:"name"`
	FullName string  `json:"fullName"`
	Private  bool    `json:"private"`
}

// githubRepos are the workspace's repositories new chats can start in
// (github:getWorkspaceRepositories).
func (a *App) githubRepos() []workspaceRepository {
	if a.convex == nil {
		return nil
	}
	convex, ws := a.convex, a.workspaceID
	f := a.fetch("github-repos", func(ctx context.Context) (any, error) {
		var out []workspaceRepository
		err := convex.Query(ctx, "github:getWorkspaceRepositories", map[string]any{"workspaceId": ws}, &out)
		return out, err
	})
	repos, _ := f.value.([]workspaceRepository)
	return repos
}
