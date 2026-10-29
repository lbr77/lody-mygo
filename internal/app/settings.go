package app

import (
	"github.com/lbr77/lody-mygo/locales"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const appVersion = "0.1.0"

// appearanceChoices are the saved values of Appearance; appearanceLabels
// say them.
var appearanceChoices = []string{"System", "Light", "Dark"}

func appearanceLabels() []string {
	return []string{locales.ThemeSystem(), locales.ThemeLight(), locales.ThemeDark()}
}

type settingsTab struct {
	id, label string
	icon      *ui.SVG
}

type settingsSection struct {
	title string
	tabs  []settingsTab
}

// settingsSections are the tabs this client can fill: the personal ones,
// the workspace's that its catalog holds, and About; said in the app's
// language as the view is built.
func settingsSections() []settingsSection {
	return []settingsSection{
		{locales.SettingsPersonal(), []settingsTab{{"account", "", nil}, {"preferences", locales.SettingsPreferences(), iconSliders}, {"appearance", locales.SettingsAppearance(), iconPalette}, {"keyboard", locales.SettingsKeyboard(), iconKeyboard}}},
		{locales.SettingsWorkspace(), []settingsTab{
			{"general", locales.SettingsGeneral(), iconBuilding}, {"agents", locales.SettingsAgents(), iconBot},
			{"agent-roles", locales.SettingsAgentRoles(), iconUserCog}, {"memory", locales.SettingsMemory(), iconBrain},
			{"mcp", "MCP", iconPlug}, {"prompt-shortcuts", "Prompt Shortcuts", iconFileText},
			{"projects", locales.SettingsProjects(), iconFolderOpen}, {"github", "GitHub", iconGithub},
			{"ai-usage", locales.SettingsUsage(), iconChart}, {"shares", locales.SettingsShares(), iconShare},
			{"billing", locales.SettingsBilling(), iconCreditCard},
		}},
		{locales.SettingsOther(), []settingsTab{{"about", locales.SettingsAbout(), iconInfo}}},
	}
}

// settingsDialog is Settings: a large dialog with its tabs on the left, as
// Lody's.
func (a *App) settingsDialog(c *ui.Context, p *palette) {
	a.syncMonitors()
	w, h := c.Size()
	ui.DialogBase(c, &a.settingsOpen, func(backdrop, panel ui.Element) {
		sheetIn(backdrop, panel)
		backdrop.Background(ui.RGBA(0, 0, 0, 0.28))
		panel.Size(min(1100, w-80), min(950, h-48)).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Clip().
			Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
			a.settingsNav(c, p)
			ui.Box(c).Grow(1).MinWidth(0).Children(func() {
				ui.Scroll(c).Fill().Padding(36, 56, 40, 56).Children(func() {
					// Keyed by tab, so that each page fades in as it's chosen.
					ui.Column(c.Key(firstNonEmpty(a.settingsTab, "account"))).FillWidth().MaxWidth(728).Gap(12).Transition(pageIn).Children(func() {
						a.settingsPage(c, p)
						a.removalDialog(c, p)
						a.confirmDialog(c, p)
					})
				})
				iconButton(c, p, iconX, locales.Close(), p.Hover).Absolute().Top(12).Right(12).OnClick(func() { a.settingsOpen = false })
			})
		})
	})
}

func (a *App) settingsNav(c *ui.Context, p *palette) {
	ui.Column(c).Width(240).Shrink(0).Background(p.Ground).Padding(16, 12).Gap(2).Children(func() {
		for si, section := range settingsSections() {
			ui.Text(c, section.title).FontSize(sizeFoot).TextColor(p.Tertiary).Padding(4, 8).Margin(map[bool]float32{true: 0, false: 14}[si == 0], 0, 2, 0)
			for _, tab := range section.tabs {
				t := tab
				current := firstNonEmpty(a.settingsTab, "account")
				if current == "machines" {
					current = "account" // a page of the account's
				}
				selected := current == t.id
				row := ui.ButtonBase(c.Key(t.id)).Height(32).Padding(0, 8).Radius(8).Gap(8).Justify(ui.Start).Cursor(ui.CursorPointer).
					OnClick(func() { a.settingsTab = t.id }).Transition(colorFade)
				switch {
				case selected:
					row.Background(p.SideSelect)
				case row.Hovered():
					row.Background(p.SideHover)
				}
				row.Children(func() {
					if t.id == "account" {
						name := locales.SettingsAccount()
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
		bug := ui.ButtonBase(c).Height(32).Padding(0, 8).Radius(8).Gap(8).Justify(ui.Start).Cursor(ui.CursorPointer).Label(locales.ReportBug()).
			OnClick(a.openBugReport).Transition(colorFade)
		if bug.Hovered() {
			bug.Background(p.SideHover)
		}
		bug.Children(func() {
			ui.Icon(c, iconBug).Size(16, 16).TextColor(p.SideMuted)
			ui.Text(c, locales.ReportBug()).FontSize(sizeBody).TextColor(p.Text)
		})
	})
}

func (a *App) settingsPage(c *ui.Context, p *palette) {
	switch firstNonEmpty(a.settingsTab, "account") {
	case "account":
		a.accountPage(c, p)
	case "preferences":
		a.preferencesPage(c, p)
	case "appearance":
		a.appearancePage(c, p)
	case "keyboard":
		a.keyboardPage(c, p)
	case "general":
		a.generalPage(c, p)
	case "agents":
		a.agentsPage(c, p)
	case "projects":
		a.projectsPage(c, p)
	case "machines":
		a.machinesPage(c, p)
	case "github":
		a.githubPage(c, p)
	case "ai-usage":
		a.usagePage(c, p)
	case "shares":
		a.sharesPage(c, p)
	case "billing":
		a.billingPage(c, p)
	case "mcp":
		a.mcpPage(c, p)
	case "memory":
		a.memoryPage(c, p)
	case "prompt-shortcuts":
		a.shortcutsPage(c, p)
	case "agent-roles":
		a.rolesPage(c, p)
	case "about":
		settingsTitle(c, p, locales.SettingsAbout())
		settingRow(c, p, "Lody Native "+appVersion, locales.SettingsAboutDescription(), nil)
		divider(c, p)
		for _, link := range []struct{ label, url string }{
			{locales.SettingsDocs(), "https://lody.ai/docs"},
			{locales.SettingsSource(), "https://github.com/lbr77/lody-mygo"},
		} {
			settingRow(c.Key(link.label), p, link.label, "", func() {
				ghostButton(c, p, locales.Open()).Border(1, p.Border).OnClick(func() { mygo.Shell.OpenExternal(link.url) })
			})
		}
		settingRow(c, p, locales.SettingsLicense(), "AGPL-3.0-only", nil)
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

// pathTail is the last n folders of a path.
func pathTail(p string, n int) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) > n {
		parts = parts[len(parts)-n:]
	}
	return strings.Join(parts, "/")
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
