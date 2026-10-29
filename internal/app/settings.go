package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const appVersion = "0.1.0"

var appearanceChoices = []string{"System", "Light", "Dark"}

type settingsTab struct {
	id, label string
	icon      *ui.SVG
}

// The tabs this client can fill: the personal ones, the workspace's that
// its catalog holds, and About.
var settingsSections = []struct {
	title string
	tabs  []settingsTab
}{
	{"Personal", []settingsTab{{"account", "", nil}, {"preferences", "Preferences", iconSliders}, {"appearance", "Appearance", iconPalette}, {"keyboard", "Keyboard", iconKeyboard}}},
	{"Workspace", []settingsTab{{"general", "General", iconBuilding}, {"agents", "Agents", iconBot}, {"projects", "Projects", iconFolder}}},
	{"Other", []settingsTab{{"about", "About", iconInfo}}},
}

// settingsDialog is Settings: a large dialog with its tabs on the left, as
// Lody's.
func (a *App) settingsDialog(c *ui.Context, p *palette) {
	w, h := c.Size()
	ui.DialogBase(c, &a.settingsOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.28))
		panel.Size(min(1100, w-80), min(950, h-48)).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Clip().
			Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
			a.settingsNav(c, p)
			ui.Box(c).Grow(1).MinWidth(0).Children(func() {
				ui.Scroll(c).Fill().Padding(36, 56, 40, 56).Children(func() {
					ui.Column(c).FillWidth().MaxWidth(728).Gap(12).Children(func() { a.settingsPage(c, p) })
				})
				iconButton(c, p, iconX, "Close", p.Hover).Absolute().Top(12).Right(12).OnClick(func() { a.settingsOpen = false })
			})
		})
	})
}

func (a *App) settingsNav(c *ui.Context, p *palette) {
	ui.Column(c).Width(240).Shrink(0).Background(p.Ground).Padding(16, 12).Gap(2).Children(func() {
		for si, section := range settingsSections {
			ui.Text(c, section.title).FontSize(sizeFoot).TextColor(p.Tertiary).Padding(4, 8).Margin(map[bool]float32{true: 0, false: 14}[si == 0], 0, 2, 0)
			for _, tab := range section.tabs {
				t := tab
				selected := firstNonEmpty(a.settingsTab, "account") == t.id
				row := ui.ButtonBase(c.Key(t.id)).Height(32).Padding(0, 8).Radius(8).Gap(8).Justify(ui.Start).Cursor(ui.CursorPointer).
					OnClick(func() { a.settingsTab = t.id })
				switch {
				case selected:
					row.Background(p.SideSelect)
				case row.Hovered():
					row.Background(p.SideHover)
				}
				row.Children(func() {
					if t.id == "account" {
						name := "Account"
						if a.account != nil {
							name = a.account.User.Name
						}
						row.Label(name)
						a.accountAvatar(c, p, 22)
						ui.Text(c, name).FontSize(sizeBody).TextColor(p.Text).SingleLine()
						return
					}
					row.Label(t.label)
					ui.Icon(c, t.icon).Size(16, 16).TextColor(p.SideMuted)
					ui.Text(c, t.label).FontSize(sizeBody).TextColor(p.Text)
				})
			}
		}
		ui.Spacer(c)
		bug := ui.ButtonBase(c).Height(32).Padding(0, 8).Radius(8).Gap(8).Justify(ui.Start).Cursor(ui.CursorPointer).Label("Report bug").
			OnClick(func() { mygo.Shell.OpenExternal("https://github.com/lbr77/lody-mygo/issues") })
		if bug.Hovered() {
			bug.Background(p.SideHover)
		}
		bug.Children(func() {
			ui.Icon(c, iconBug).Size(16, 16).TextColor(p.SideMuted)
			ui.Text(c, "Report bug").FontSize(sizeBody).TextColor(p.Text)
		})
	})
}

func (a *App) settingsPage(c *ui.Context, p *palette) {
	switch firstNonEmpty(a.settingsTab, "account") {
	case "account":
		a.accountPage(c, p)
	case "preferences":
		settingsTitle(c, p, "Preferences")
		settingRow(c, p, "When the agent is busy", "Steer gives your message to the running turn, when its agent takes steering. Otherwise messages wait in the queue.", func() {
			picked := 0
			if a.busyBehavior == "steer" {
				picked = 1
			}
			was := picked
			ui.Segmented(c, &picked, "Queue", "Steer").Label("When the agent is busy")
			if picked != was {
				a.busyBehavior = []string{"queue", "steer"}[picked]
				a.savePrefs()
			}
		})
	case "appearance":
		settingsTitle(c, p, "Appearance")
		settingRow(c, p, "Theme", "Follow the system, or keep Lody light or dark.", func() {
			picked := 0
			for i, choice := range appearanceChoices {
				if a.appearance == choice {
					picked = i
				}
			}
			was := picked
			ui.Segmented(c, &picked, appearanceChoices...).Label("Theme")
			if picked != was {
				a.setAppearance(appearanceChoices[picked])
			}
		})
	case "keyboard":
		settingsTitle(c, p, "Keyboard")
		for _, sc := range [][2]string{
			{"New chat", "⌘ N"}, {"Focus the message box", "⌘ L"}, {"Toggle the sidebar", "⌘ \\"},
			{"Send the message", "↩"}, {"New line", "⇧ ↩"},
		} {
			settingRow(c.Key(sc[0]), p, sc[0], "", func() {
				ui.Row(c).Gap(4).Children(func() {
					for _, key := range strings.Fields(sc[1]) {
						ui.Text(c, key).FontSize(sizeFoot).TextColor(p.Secondary).Padding(2, 7).Radius(5).Border(1, p.Border).Background(p.Canvas)
					}
				})
			})
		}
	case "general":
		a.workspacePage(c, p)
	case "agents":
		a.agentsPage(c, p)
	case "projects":
		a.projectsPage(c, p)
	case "about":
		settingsTitle(c, p, "About")
		settingRow(c, p, "Lody Native "+appVersion, "A native Lody client: MyGo draws it on the GPU, without a web view, and it syncs with Lody Cloud directly.", nil)
		divider(c, p)
		for _, link := range []struct{ label, url string }{
			{"Lody documentation", "https://lody.ai/docs"},
			{"Source code", "https://github.com/lbr77/lody-mygo"},
		} {
			settingRow(c.Key(link.label), p, link.label, "", func() {
				ghostButton(c, p, "Open").Border(1, p.Border).OnClick(func() { mygo.Shell.OpenExternal(link.url) })
			})
		}
		settingRow(c, p, "License", "AGPL-3.0-only", nil)
	}
}

func settingsTitle(c *ui.Context, p *palette, title string) {
	ui.Text(c, title).FontSize(sizeTitle).FontWeight(600).TextColor(p.Strong).Margin(0, 0, 8, 0)
}

func settingsHeading(c *ui.Context, p *palette, title string) {
	ui.Text(c, title).FontSize(sizeBody).FontWeight(600).TextColor(p.Strong).Margin(4, 0, 0, 0)
}

func divider(c *ui.Context, p *palette) {
	ui.Box(c).FillWidth().Height(1).Background(p.Border).Margin(12, 0)
}

// settingRow is a setting: its name and what it does on the left, its
// control on the right.
func settingRow(c *ui.Context, p *palette, label, description string, control func()) {
	ui.Row(c).FillWidth().MinHeight(36).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, label).FontSize(sizeBody).TextColor(p.Text)
			if description != "" {
				ui.Text(c, description).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Secondary)
			}
		})
		if control != nil {
			ui.Row(c).Shrink(0).Children(control)
		}
	})
}

func valueText(c *ui.Context, p *palette, s string) {
	ui.Text(c, s).FontSize(sizeBody).TextColor(p.Secondary).Selectable()
}

func (a *App) accountPage(c *ui.Context, p *palette) {
	settingsTitle(c, p, "Account")
	if a.account == nil {
		return
	}
	u := a.account.User
	settingsHeading(c, p, "Profile")
	settingRow(c, p, "Email", "", func() { valueText(c, p, u.Email) })
	settingRow(c, p, "Name", "", func() { valueText(c, p, u.Name) })
	settingRow(c, p, "Avatar", "", func() { a.accountAvatar(c, p, 32) })
	divider(c, p)
	settingsHeading(c, p, "My machines")
	if len(a.catalog.MachineIDs) == 0 {
		ui.Text(c, "No machine is connected. Run npx lody daemon start on a computer to connect it.").FontSize(sizeFoot).TextColor(p.Secondary)
	}
	for _, id := range a.catalog.MachineIDs {
		var facts []string
		if a.presence.Live {
			facts = append(facts, map[bool]string{true: "Online", false: "Offline"}[a.presence.IsOnline(id)])
		}
		if os := a.catalog.MachineOS[id]; os != "" {
			facts = append(facts, os)
		}
		facts = append(facts, plural(len(a.catalog.Agents[id]), "agent", "agents"))
		folders := 0
		for _, pr := range a.catalog.Projects {
			if pr.MachineID == id && strings.Contains(pr.ID, ":local:") {
				folders++
			}
		}
		facts = append(facts, plural(folders, "folder", "folders"))
		settingRow(c.Key(id), p, a.machineName(id), strings.Join(facts, " · "), nil)
	}
	divider(c, p)
	settingsHeading(c, p, "Sign in")
	settingRow(c, p, "Sign out", "Sign out of Lody on this Mac. Your chats stay in the workspace.", func() {
		ghostButton(c, p, "Sign out").Border(1, p.Border).OnClick(func() {
			a.settingsOpen = false
			a.signOut()
		})
	})
}

func (a *App) workspacePage(c *ui.Context, p *palette) {
	settingsTitle(c, p, "General")
	ws := a.workspace()
	if ws == nil {
		return
	}
	settingRow(c, p, "Workspace", "", func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			workspaceTile(c, ws.Name, 20)
			valueText(c, p, ws.Name)
		})
	})
	if ws.Slug != "" {
		settingRow(c, p, "Slug", "", func() { valueText(c, p, ws.Slug) })
	}
	settingRow(c, p, "Workspace ID", "", func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, ws.ID).Font(fontMono).FontSize(sizeFoot).TextColor(p.Secondary).Selectable()
			iconButton(c, p, iconCopy, "Copy", p.Hover).Size(24, 24).OnClick(func() { mygo.Clipboard.WriteText(ws.ID) })
		})
	})
	divider(c, p)
	settingsHeading(c, p, "Workspaces")
	for _, w := range a.account.Workspaces {
		id := w.ID
		settingRow(c.Key(w.ID), p, w.Name, "", func() {
			if id == a.workspaceID {
				ui.Text(c, "Current").FontSize(sizeFoot).TextColor(p.Secondary)
				return
			}
			ghostButton(c, p, "Switch").Border(1, p.Border).OnClick(func() {
				a.settingsOpen = false
				a.connect(id)
			})
		})
	}
}

func (a *App) agentsPage(c *ui.Context, p *palette) {
	settingsTitle(c, p, "Agents")
	ui.Text(c, "The agents each machine offers. Configure them with the Lody app or CLI on the machine.").FontSize(sizeFoot).TextColor(p.Secondary)
	for _, machine := range a.catalog.MachineIDs {
		settingsHeading(c.Key(machine), p, a.machineName(machine))
		agents := a.catalog.Agents[machine]
		if len(agents) == 0 {
			ui.Text(c, "No agents").FontSize(sizeFoot).TextColor(p.Tertiary)
		}
		for _, ag := range agents {
			capability := a.catalog.Capabilities[ag.ID]
			facts := []string{firstNonEmpty(ag.AgentType, "agent"), ag.CLIType}
			if n := len(capability.Models); n > 0 {
				facts = append(facts, plural(n, "model", "models"))
			}
			if n := len(a.catalog.ModesFor(ag.ID, ag.AgentType)); n > 0 {
				facts = append(facts, plural(n, "mode", "modes"))
			}
			if capability.Steer {
				facts = append(facts, "steering")
			}
			settingRow(c.Key(ag.ID), p, ag.Name, strings.Join(facts, " · "), nil)
		}
	}
}

func (a *App) projectsPage(c *ui.Context, p *palette) {
	settingsTitle(c, p, "Projects")
	ui.Text(c, "The folders each machine works in. Add one with the Lody app or CLI on the machine.").FontSize(sizeFoot).TextColor(p.Secondary)
	for _, machine := range a.catalog.MachineIDs {
		settingsHeading(c.Key(machine), p, a.machineName(machine))
		shown := 0
		for _, pr := range a.catalog.Projects {
			if pr.MachineID != machine || !strings.Contains(pr.ID, ":local:") {
				continue
			}
			shown++
			settingRow(c.Key(pr.ID), p, pr.Name, pr.RootPath, nil)
		}
		if shown == 0 {
			ui.Text(c, "No projects").FontSize(sizeFoot).TextColor(p.Tertiary)
		}
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// accountAvatar is the signed-in user's picture, or the stand-in.
func (a *App) accountAvatar(c *ui.Context, p *palette, size float32) {
	if a.account != nil && a.account.User.Image != "" {
		if img := a.urlImage(a.account.User.Image); img.bitmap != nil {
			ui.Box(c).Size(size, size).Shrink(0).Radius(size / 2).Clip().Children(func() {
				ui.Image(c, img.bitmap).Size(size, size).Fit(ui.Cover)
			})
			return
		}
	}
	ui.Row(c).Size(size, size).Shrink(0).Radius(size / 2).Background(p.Text.Alpha(0.1)).Center().Children(func() {
		ui.Icon(c, iconUser).Size(size/2, size/2).TextColor(p.Secondary)
	})
}
