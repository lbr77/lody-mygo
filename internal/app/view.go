package app

import (
	_ "embed"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

const titleBarHeight = 44

//go:embed assets/boot-mark.png
var bootMarkPNG []byte

var bootMarkBitmap, _ = ui.DecodeBitmap(bootMarkPNG)

// bootMark is Lody's launch mark: its 48px jellyfish fades in over 240ms
// after 120ms on CSS's ease-out, and from 1.2s breathes down to half and
// back every 2.4s, ease-in-out, while the app starts.
func bootMark(c *ui.Context) {
	mark := ui.Image(c.Key("boot-mark"), bootMarkBitmap).Size(48, 48).Label(locales.Loading())
	in := mark.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: 120 * time.Millisecond, Value: 0}, {At: 360 * time.Millisecond, Value: 1, Ease: cssEaseOut}})
	breathe := mark.Keyframes("breathe", []ui.Keyframe{{Value: 1}, {At: 1200 * time.Millisecond, Value: 0.5, Ease: cssEaseInOut}, {At: 2400 * time.Millisecond, Value: 1, Ease: cssEaseInOut}},
		ui.KeyframeOptions{Repeat: ui.RepeatForever, Delay: 1200 * time.Millisecond})
	mark.Opacity(in * breathe)
}

func (a *App) render(c *ui.Context) {
	p := applyTheme(c)
	spinnerInk, framePalette = p.Secondary, p
	// The overlay layer stays built while nothing is in it, so that what
	// leaves it, as a dialog closing, fades out rather than vanishing.
	ui.Overlay(c, func() {})
	// A lody:// link waiting opens as soon as what it names is here.
	a.followLink()
	switch a.phase {
	case phaseLoading:
		a.booting = true
		a.bootShell(c, p, ui.Column(c.Key("loading")).Fill().DragWindow(), a.workspaceID != "")
	case phaseSignedOut:
		a.signInView(c, p)
	default:
		a.mainView(c, p)
	}
	a.bootOut(c, p)
}

// bootOut fades the boot screen out over 160ms, easing out, over the app
// that is ready under it, as Lody's boot shell leaves.
func (a *App) bootOut(c *ui.Context, p *palette) {
	if a.phase == phaseLoading || !a.booting {
		return
	}
	ui.Overlay(c, func() {
		shell := ui.Column(c.Key("boot-out")).Absolute().Left(0).Top(0).Right(0).Bottom(0).PassThrough()
		k := shell.Keyframes("out", []ui.Keyframe{{Value: 1}, {At: 160 * time.Millisecond, Value: 0, Ease: cssEaseOut}})
		if k <= 0 {
			a.booting = false
			return
		}
		// Leaving, it takes the layout it leaves to: plain over sign-in.
		a.bootShell(c, p, shell.Opacity(k), a.phase != phaseSignedOut && a.workspaceID != "")
	})
}

// bootShell is Lody's boot shell in el: the sidebar's ground, room for the
// sidebar where the last session had the workspace, and the workspace
// card with the launch mark at its middle; plain, the canvas alone. Its
// layout morphs over 320ms on the sheet curve as it becomes known.
func (a *App) bootShell(c *ui.Context, p *palette, el ui.Element, workspace bool) {
	side := float32(0)
	if workspace && !a.sidebarHidden {
		side = a.sidebarW()
	}
	morph := ui.Animation{Duration: 320 * time.Millisecond, Ease: easeSheet}
	// A shell first shows the layout as it stands, known before the first
	// frame as Lody's boot script knows it; a change after morphs.
	if !a.bootLaid {
		a.bootSide, a.bootFrame, a.bootLaid = side, b2f(workspace), true
	}
	if el.Bounds().W == 0 {
		el.Jump("boot-side", a.bootSide)
		el.Jump("boot-frame", a.bootFrame)
	}
	w := el.AnimateTo("boot-side", side, morph)
	frame := el.AnimateTo("boot-frame", b2f(workspace), morph)
	a.bootSide, a.bootFrame = w, frame
	el.Background(p.Ground.Mix(p.Canvas, 1-frame)).Children(func() {
		ui.Row(c.Key("boot-row")).Fill().AlignItems(ui.Stretch).Children(func() { a.bootRow(c, p, w, side, frame) })
	})
}

// bootRow is the boot shell's sidebar room and card, w of the side it
// makes room for, framed as far as frame says.
func (a *App) bootRow(c *ui.Context, p *palette, w, side, frame float32) {
	ui.Box(c.Key("boot-sidebar")).Width(w).Shrink(0)
	// The card's frame, 4 from the window but at the sidebar's side,
	// rounded and ruled as the workspace's, is there in the workspace
	// layout alone.
	gutter := 4 * frame
	left := gutter
	if side > 0 {
		left = gutter * (1 - w/side)
	}
	card := ui.Column(c.Key("boot-card")).Grow(1).MinWidth(0).Center().Margin(gutter, gutter, gutter, left).Radius(14 * frame).Background(p.Canvas)
	if frame > 0 {
		card.Border(0.5, p.CardRim.Alpha(frame))
	}
	card.Children(func() { bootMark(c) })
}

// signInView follows Lody's login page: a visual panel beside a form
// column whose content sits at the bottom.
func (a *App) signInView(c *ui.Context, p *palette) {
	ui.Row(c.Key("signin")).Fill().Padding(12).Gap(12).AlignItems(ui.Stretch).Background(p.Canvas).Children(func() {
		a.dither.view(c, p).DragWindow()
		ui.Column(c).Width(440).Shrink(0).Padding(28, 40, 36, 52).Children(func() {
			ui.Spacer(c).DragWindow()
			// The column rises in as one after 150ms, over 500ms, as Lody's
			// login page staggers its one child.
			form := ui.Column(c.Key("signin-form")).MaxWidth(360).Gap(24)
			if k := form.Keyframes("rise", []ui.Keyframe{{Value: 0}, {At: 500 * time.Millisecond, Value: 1, Ease: easeSweep}},
				ui.KeyframeOptions{Delay: 150 * time.Millisecond}); k < 1 {
				form.Opacity(k).Translate(0, 10*(1-k))
			}
			form.Children(func() {
				ui.Column(c.Key("signin-head")).Gap(10).Children(func() {
					ui.Text(c, locales.SignInTitle()).FontSize(28).FontWeight(600).LineHeight(1.2).LetterSpacing(-0.7).TextColor(p.Strong)
					ui.Text(c, locales.SignInDescription()).FontSize(sizeBody).LineHeight(1.5).TextColor(p.Secondary)
				})
				ui.Column(c.Key("signin-action")).Gap(12).Children(func() {
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
				ui.Column(c.Key("signin-foot")).BorderWidth(1, 0, 0, 0).BorderColor(p.Border).Padding(16, 0, 0, 0).Children(func() {
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
	b.Children(func() {
		ui.Text(c, label).FontSize(sizeSub).FontWeight(500).TextColor(p.Danger)
	})
	return b
}

// lodyTooltip is Lody's tooltip on anchor: a raised chip in the footnote
// type, coming 4px toward its anchor as it fades in over 120ms, from the
// side it landed on: above it, from above; below, from below.
func lodyTooltip(c *ui.Context, p *palette, anchor ui.Element, label string) {
	if label == "" {
		return
	}
	anchor.Description(label)
	if !tips.show(c, anchor, label) {
		return
	}
	ui.Overlay(c, func() {
		tip := ui.Column(c.Key("lody-tip")).Absolute().PassThrough().Role(ui.RoleTooltip).AttachTo(anchor, ui.AnchorTop, ui.AnchorBottom).Margin(0, 0, 6, 0)
		away := float32(-4)
		if tb, ab := tip.Bounds(), anchor.Bounds(); tb.H > 0 && tb.Y >= ab.Y+ab.H-1 {
			away = 4 // it landed below its anchor
		}
		tip.Padding(4, 8).Radius(8).MaxWidth(260).Background(p.Popover).Border(0.5, p.CardRim).
			Shadow(0, 6, 16, -4, ui.RGBA(0, 0, 0, 0.18)).
			Transition(ui.ElementTransition{Duration: durFast, Ease: easeStandard, Enter: &ui.Motion{Y: away}, Exit: &ui.Motion{Y: away}})
		tip.Children(func() { ui.Text(c, label).FontSize(sizeFoot).LineHeight(16.0 / 12).TextColor(p.Text) })
	})
}

// tip gives e Lody's tooltip, as Element.Tooltip gives the theme's: for
// the elements whose tips Lody draws.
func tip(c *ui.Context, p *palette, e ui.Element, label string) ui.Element {
	lodyTooltip(c, p, e, label)
	return e.Description(label)
}

// iconButton is a 28px square icon button, muted at rest.
func iconButton(c *ui.Context, p *palette, icon *ui.SVG, label string, hover ui.Color) ui.Element {
	b := ui.ButtonBase(c).Size(28, 28).Radius(8).Cursor(ui.CursorPointer).Label(label).Transition(colorFade)
	lodyTooltip(c, p, b, label)
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
