package app

import (
	"github.com/lbr77/lody-mygo/locales"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const titleBarHeight = 44

func (a *App) render(c *ui.Context) {
	p := applyTheme(c)
	spinnerInk = p.Secondary
	// The overlay layer stays built while nothing is in it, so that what
	// leaves it, as a dialog closing, fades out rather than vanishing.
	ui.Overlay(c, func() {})
	switch a.phase {
	case phaseLoading:
		ui.Column(c.Key("loading")).Fill().Center().Background(p.Ground).DragWindow().Children(func() {
			spinner(c).Label(locales.Loading())
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
	ui.Row(c.Key("signin")).Fill().Padding(12).Gap(12).AlignItems(ui.Stretch).Background(p.Canvas).Transition(windowIn).Children(func() {
		a.dither.view(c, p).DragWindow()
		ui.Column(c).Width(440).Shrink(0).Padding(28, 40, 36, 52).Children(func() {
			ui.Spacer(c).DragWindow()
			// The column's parts rise in one after another, 70ms apart after
			// 150ms, each over 500ms, as Lody's login page arrives.
			part := 0
			rise := func(e ui.Element) ui.Element {
				k := e.Keyframes("rise", []ui.Keyframe{{Value: 0}, {At: 500 * time.Millisecond, Value: 1, Ease: easeSweep}},
					ui.KeyframeOptions{Delay: 150*time.Millisecond + time.Duration(part)*70*time.Millisecond})
				part++
				if k < 1 {
					e.Opacity(k).Translate(0, 10*(1-k))
				}
				return e
			}
			ui.Column(c).MaxWidth(360).Gap(24).Children(func() {
				rise(ui.Column(c.Key("signin-head"))).Gap(10).Children(func() {
					ui.Text(c, locales.SignInTitle()).FontSize(28).FontWeight(600).LineHeight(1.2).LetterSpacing(-0.7).TextColor(p.Strong)
					ui.Text(c, locales.SignInDescription()).FontSize(sizeBody).LineHeight(1.5).TextColor(p.Secondary)
				})
				rise(ui.Column(c.Key("signin-action"))).Gap(12).Children(func() {
					if a.signIn.busy {
						a.deviceCodeCard(c, p)
					} else if inkButton(c, p, locales.SignInOpenBrowser(), iconExternal, 40).FillWidth().Clicked() {
						a.beginSignIn()
					}
					if a.signIn.err != "" {
						ui.Text(c, a.signIn.err).FontSize(sizeFoot).TextColor(p.Danger).Selectable()
					}
					ui.Text(c, locales.SignInHint()).FontSize(sizeFoot).TextColor(p.Secondary).TextAlign(ui.Center)
				})
				rise(ui.Column(c.Key("signin-foot"))).BorderWidth(1, 0, 0, 0).BorderColor(p.Border).Padding(16, 0, 0, 0).Children(func() {
					ui.Text(c, locales.SignInFootnote()).FontSize(sizeCaption).LineHeight(1.6).TextColor(p.Tertiary)
				})
			})
		})
	})
}

func (a *App) deviceCodeCard(c *ui.Context, p *palette) {
	ui.Column(c).Gap(12).Padding(16).Radius(12).Border(1, p.Border).Background(p.Popover).Children(func() {
		if a.signIn.code == "" {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				spinner(c).Size(14, 14)
				ui.Text(c, locales.SignInContacting()).FontSize(sizeSub).TextColor(p.Secondary)
			})
			return
		}
		ui.Text(c, locales.SignInConfirmCode()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
		ui.Text(c, a.signIn.code).Font(fontMono).FontSize(24).FontWeight(600).LetterSpacing(2).TextColor(p.Strong).Selectable()
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			spinner(c).Size(12, 12)
			ui.Text(c, locales.SignInWaiting()).FontSize(sizeFoot).TextColor(p.Secondary).Grow(1)
			ghostButton(c, p, locales.SignInReopen()).OnClick(func() { mygo.Shell.OpenExternal(a.signIn.url) })
			if ghostButton(c, p, locales.Cancel()).Clicked() {
				a.cancelSignIn()
			}
		})
	})
}

// inkButton is Lody's primary button: ink fill, page-colored text.
func inkButton(c *ui.Context, p *palette, label string, icon *ui.SVG, height float32) ui.Element {
	b := ui.ButtonBase(c).Height(height).Radius(10).Padding(0, 16).Gap(10).Background(p.Text).Cursor(ui.CursorPointer).Label(label).Transition(colorFade)
	if b.Pressed() {
		b.Background(p.Text.Alpha(0.8))
	} else if b.Hovered() {
		b.Background(p.Text.Alpha(0.9))
	}
	pressable(b, 0.98)
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
	b := ui.ButtonBase(c).Height(28).Radius(8).Padding(0, 8).Cursor(ui.CursorPointer).Label(label).Transition(colorFade)
	if b.Hovered() {
		b.Background(p.Text.Alpha(0.06))
	}
	pressable(b, 0.97)
	b.Children(func() {
		ui.Text(c, label).FontSize(sizeSub).FontWeight(500).TextColor(p.Secondary)
	})
	return b
}

// dangerGhostButton is a ghost button in the destructive ink.
func dangerGhostButton(c *ui.Context, p *palette, label string) ui.Element {
	b := ui.ButtonBase(c).Height(28).Radius(8).Padding(0, 8).Cursor(ui.CursorPointer).Label(label).Transition(colorFade)
	if b.Hovered() {
		b.Background(p.Danger.Alpha(0.08))
	}
	pressable(b, 0.97)
	b.Children(func() {
		ui.Text(c, label).FontSize(sizeSub).FontWeight(500).TextColor(p.Danger)
	})
	return b
}

// iconButton is a 28px square icon button, muted at rest.
func iconButton(c *ui.Context, p *palette, icon *ui.SVG, label string, hover ui.Color) ui.Element {
	b := ui.ButtonBase(c).Size(28, 28).Radius(8).Cursor(ui.CursorPointer).Label(label).Tooltip(label).Transition(colorFade)
	color := p.SideMuted
	if b.Hovered() {
		b.Background(hover)
		color = p.Text
	}
	pressable(b, 0.92)
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
