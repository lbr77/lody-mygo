package app

import (
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const titleBarHeight = 44

func (a *App) render(c *ui.Context) {
	p := applyTheme(c)
	switch a.phase {
	case phaseLoading:
		ui.Column(c).Fill().Center().Background(p.Ground).DragWindow().Children(func() {
			ui.Spinner(c).Label("Loading")
		})
	case phaseSignedOut:
		a.signInView(c, p)
	default:
		a.mainView(c, p)
	}
}

// signInView follows Lody's login page: a visual panel beside a form
// column whose content sits at the bottom.
func (a *App) signInView(c *ui.Context, p *palette) {
	ui.Row(c).Fill().Padding(12).Gap(12).AlignItems(ui.Stretch).Background(p.Canvas).Children(func() {
		a.dither.view(c, p).DragWindow()
		ui.Column(c).Width(440).Shrink(0).Padding(28, 40, 36, 52).Children(func() {
			ui.Spacer(c).DragWindow()
			ui.Column(c).MaxWidth(360).Gap(24).Children(func() {
				ui.Column(c).Gap(10).Children(func() {
					ui.Text(c, "Welcome to Lody AI").FontSize(28).FontWeight(600).LineHeight(1.2).LetterSpacing(-0.7).TextColor(p.Strong)
					ui.Text(c, "Sign in from your browser to continue in Lody.").FontSize(sizeBody).LineHeight(1.5).TextColor(p.Secondary)
				})
				ui.Column(c).Gap(12).Children(func() {
					if a.signIn.busy {
						a.deviceCodeCard(c, p)
					} else if inkButton(c, p, "Open browser to sign in", iconExternal, 40).FillWidth().Clicked() {
						a.beginSignIn()
					}
					if a.signIn.err != "" {
						ui.Text(c, a.signIn.err).FontSize(sizeFoot).TextColor(p.Danger).Selectable()
					}
					ui.Text(c, "Lody opens lody.ai in your browser. Approve this device there, then come back.").FontSize(sizeFoot).TextColor(p.Secondary).TextAlign(ui.Center)
				})
				ui.Column(c).BorderWidth(1, 0, 0, 0).BorderColor(p.Border).Padding(16, 0, 0, 0).Children(func() {
					ui.Text(c, "An independent native client for Lody Cloud. It signs in with Lody's device flow and keeps your session in the macOS Keychain.").FontSize(sizeCaption).LineHeight(1.6).TextColor(p.Tertiary)
				})
			})
		})
	})
}

func (a *App) deviceCodeCard(c *ui.Context, p *palette) {
	ui.Column(c).Gap(12).Padding(16).Radius(12).Border(1, p.Border).Background(p.Popover).Children(func() {
		if a.signIn.code == "" {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(14, 14)
				ui.Text(c, "Contacting Lody…").FontSize(sizeSub).TextColor(p.Secondary)
			})
			return
		}
		ui.Text(c, "Confirm this code in your browser").FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
		ui.Text(c, a.signIn.code).Font(fontMono).FontSize(24).FontWeight(600).LetterSpacing(2).TextColor(p.Strong).Selectable()
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(12, 12)
			ui.Text(c, "Waiting for approval…").FontSize(sizeFoot).TextColor(p.Secondary).Grow(1)
			ghostButton(c, p, "Reopen").OnClick(func() { mygo.Shell.OpenExternal(a.signIn.url) })
			if ghostButton(c, p, "Cancel").Clicked() {
				a.cancelSignIn()
			}
		})
	})
}

// inkButton is Lody's primary button: ink fill, page-colored text.
func inkButton(c *ui.Context, p *palette, label string, icon *ui.SVG, height float32) ui.Element {
	b := ui.ButtonBase(c).Height(height).Radius(10).Padding(0, 16).Gap(10).Background(p.Text).Cursor(ui.CursorPointer).Label(label)
	if b.Pressed() {
		b.Background(p.Text.Alpha(0.8))
	} else if b.Hovered() {
		b.Background(p.Text.Alpha(0.9))
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(sizeBody).FontWeight(500).TextColor(p.Canvas)
		if icon != nil {
			ui.Icon(c, icon).Size(16, 16).TextColor(p.Canvas)
		}
	})
	return b
}

// ghostButton is a transparent text button that fills on hover.
func ghostButton(c *ui.Context, p *palette, label string) ui.Element {
	b := ui.ButtonBase(c).Height(28).Radius(8).Padding(0, 8).Cursor(ui.CursorPointer).Label(label)
	if b.Hovered() {
		b.Background(p.Text.Alpha(0.06))
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(sizeSub).FontWeight(500).TextColor(p.Secondary)
	})
	return b
}

// iconButton is a 28px square icon button, muted at rest.
func iconButton(c *ui.Context, p *palette, icon *ui.SVG, label string, hover ui.Color) ui.Element {
	b := ui.ButtonBase(c).Size(28, 28).Radius(8).Cursor(ui.CursorPointer).Label(label).Tooltip(label)
	color := p.SideMuted
	if b.Hovered() {
		b.Background(hover)
		color = p.Text
	}
	b.Children(func() { ui.Icon(c, icon).Size(16, 16).TextColor(color) })
	return b
}

// avatar is a rounded tile with a name's initial.
func avatar(c *ui.Context, p *palette, name string, size, radius float32) ui.Element {
	initial := "?"
	if r, _ := utf8.DecodeRuneInString(strings.TrimSpace(name)); r != utf8.RuneError {
		initial = strings.ToUpper(string(r))
	}
	return ui.Row(c).Size(size, size).Shrink(0).Radius(radius).Background(p.Primary.Alpha(0.22)).Center().Children(func() {
		ui.Text(c, initial).FontSize(size * 0.55).FontWeight(600).TextColor(p.Text)
	})
}
