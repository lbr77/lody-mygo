package app

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const appVersion = "0.1.0"

var (
	appearanceChoices = []string{"System", "Light", "Dark"}
	busyChoices       = []string{"queue", "steer"}
)

// settingsDialog is the Settings window: a dialog centered over the app.
func (a *App) settingsDialog(c *ui.Context, p *palette) {
	ui.DialogBase(c, &a.settingsOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.28))
		panel.Width(560).MaxHeight(640).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Clip().
			Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Column(c).Fill().Children(func() {
			ui.Row(c).Height(52).Padding(0, 12, 0, 20).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.Border).Children(func() {
				ui.Text(c, "Settings").FontSize(sizeTitle).FontWeight(600).TextColor(p.Strong).Grow(1)
				iconButton(c, p, iconX, "Close", p.Hover).OnClick(func() { a.settingsOpen = false })
			})
			ui.Scroll(c).Grow(1).MinHeight(0).Padding(8, 20, 20, 20).Gap(20).Children(func() {
				a.settingsAccount(c, p)
				settingsSection(c, p, "Appearance", "", func() {
					index := 0
					for i, choice := range appearanceChoices {
						if a.appearance == choice {
							index = i
						}
					}
					picked := index
					ui.Segmented(c, &picked, appearanceChoices...).Label("Appearance")
					if picked != index {
						a.setAppearance(appearanceChoices[picked])
					}
				})
				settingsSection(c, p, "When the agent is busy",
					"Steer gives your message to the running turn, when its agent takes steering. Otherwise messages wait in the queue.",
					func() {
						index := 0
						if a.busyBehavior == "steer" {
							index = 1
						}
						picked := index
						ui.Segmented(c, &picked, "Queue", "Steer").Label("When the agent is busy")
						if picked != index {
							a.busyBehavior = busyChoices[picked]
							a.savePrefs()
						}
					})
				settingsSection(c, p, "About", "", func() {
					ui.Text(c, "Lody Native "+appVersion+": a native Lody client drawn by MyGo, without a web view.").FontSize(sizeSub).TextColor(p.Secondary)
					ui.Row(c).Gap(6).Children(func() {
						for _, link := range []struct{ label, url string }{
							{"Lody Docs", "https://lody.ai/docs"},
							{"Source", "https://github.com/lbr77/lody-mygo"},
						} {
							ghostButton(c.Key(link.label), p, link.label).Border(1, p.Border).OnClick(func() { mygo.Shell.OpenExternal(link.url) })
						}
					})
				})
			})
		})
	})
}

func settingsSection(c *ui.Context, p *palette, title, hint string, body func()) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, title).FontSize(sizeFoot).FontWeight(500).TextColor(p.Secondary)
		ui.Column(c).Gap(8).AlignItems(ui.Start).Children(body)
		if hint != "" {
			ui.Text(c, hint).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Tertiary)
		}
	})
}

func (a *App) settingsAccount(c *ui.Context, p *palette) {
	if a.account == nil {
		return
	}
	settingsSection(c, p, "Account", "", func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Padding(12).Radius(10).Background(p.Text.Alpha(0.04)).Children(func() {
			userAvatar(c, p)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, a.account.User.Name).FontSize(sizeBody).FontWeight(500).TextColor(p.Text).SingleLine()
				ui.Text(c, a.account.User.Email).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
			})
			ghostButton(c, p, "Sign Out").Border(1, p.Border).OnClick(func() {
				a.settingsOpen = false
				a.signOut()
			})
		})
		if ws := a.workspace(); ws != nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				workspaceTile(c, ws.Name, 18)
				ui.Text(c, ws.Name).FontSize(sizeSub).TextColor(p.Text)
				ui.Text(c, "· current workspace").FontSize(sizeSub).TextColor(p.Tertiary)
			})
		}
	})
}
