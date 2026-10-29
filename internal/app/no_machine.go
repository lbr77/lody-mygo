package app

import (
	"os"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/localterm"
	"github.com/lbr77/lody-mygo/locales"
)

// Lody's hint for a workspace with no machine: a plug that reaches into a
// wall socket and lights its lamp, then pulls out, on a 3.6s loop; or,
// where this machine's daemon is on its way up, the plug seated and the
// lamp breathing.

// systemGrays are Apple's grays, which Lody's tokens use: gray, gray2 to
// gray6.
func systemGrays(dark bool) [6]ui.Color {
	if dark {
		return [6]ui.Color{ui.Hex("#8E8E93"), ui.Hex("#636366"), ui.Hex("#48484A"), ui.Hex("#3A3A3C"), ui.Hex("#2C2C2E"), ui.Hex("#1C1C1E")}
	}
	return [6]ui.Color{ui.Hex("#8E8E93"), ui.Hex("#AEAEB2"), ui.Hex("#C7C7CC"), ui.Hex("#D1D1D6"), ui.Hex("#E5E5EA"), ui.Hex("#F2F2F7")}
}

// daemonInstalled is whether this machine has a Lody daemon to start.
func daemonInstalled() bool {
	_, err := os.Stat(localterm.ControlSocketPath())
	return err == nil
}

func (a *App) noMachineHint(c *ui.Context, p *palette) {
	starting := daemonInstalled()
	title, message := locales.NoMachineTitle(), locales.NoMachineMessage()
	if starting {
		title, message = locales.DaemonStartingTitle(), locales.DaemonStartingMessage()
	}
	card := ui.Row(c.Key("no-machine")).MaxWidth(560).Gap(16).Padding(16).Radius(12).AlignItems(ui.Center).Background(p.Popover).
		Border(0.5, p.CardRim).Transition(pageIn)
	if !p.Dark {
		card.Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.06))
	}
	card.Children(func() {
		plugArt(c, p, starting)
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).AlignItems(ui.Start).Children(func() {
			ui.Text(c, title).FontSize(sizeBody).FontWeight(550).TextColor(p.Text)
			ui.Text(c, message).FontSize(sizeSub).LineHeight(1.45).TextColor(p.Secondary)
			ui.Row(c).Wrap().Gap(12).AlignItems(ui.Center).Margin(6, 0, 0, 0).Children(func() {
				if starting {
					outlineButton(c, p, iconBug, locales.HintReportBug()).OnClick(a.openBugReport)
				} else {
					outlineButton(c, p, iconExternal, locales.HintDownloadClient()).OnClick(func() { openExternal(desktopDownloadURL) })
				}
				ui.Row(c).Gap(4).Children(func() {
					ui.Text(c, locales.HintDiscordMessage()).FontSize(sizeFoot).TextColor(p.Tertiary)
					link := ui.ButtonBase(c.Key("discord")).Cursor(ui.CursorPointer).Label("Discord").OnClick(func() { openExternal("https://discord.gg/E8mZtMu38s") })
					ink := p.Secondary
					if link.Hovered() {
						ink = p.Text
					}
					link.Children(func() { ui.Text(c, "Discord").FontSize(sizeFoot).TextColor(ink).Underline() })
				})
			})
		})
	})
}

// plugArt draws the plug and the socket, 135×60 of a 108×48 drawing.
func plugArt(c *ui.Context, p *palette, seated bool) {
	art := ui.Box(c.Key("plug")).Size(135, 60).Shrink(0).ClipX()
	loop := ui.KeyframeOptions{Repeat: ui.RepeatForever}
	var reach, lamp float32
	if seated {
		reach = 1
		ease := ui.EaseInOut
		lamp = art.Keyframes("breathe", []ui.Keyframe{{Value: 0.35}, {At: 900 * time.Millisecond, Value: 1, Ease: ease}, {At: 1800 * time.Millisecond, Value: 0.35, Ease: ease}}, loop)
	} else {
		ms := func(f float64) time.Duration { return time.Duration(f * 3600 * float64(time.Millisecond)) }
		reach = art.Keyframes("reach", []ui.Keyframe{{Value: 0}, {At: ms(0.40), Value: 0}, {At: ms(0.58), Value: 1, Ease: easeStandard},
			{At: ms(0.78), Value: 1}, {At: ms(0.94), Value: 0, Ease: easeStandard}, {At: ms(1), Value: 0}}, loop)
		lamp = art.Keyframes("lamp", []ui.Keyframe{{Value: 0}, {At: ms(0.57), Value: 0}, {At: ms(0.60), Value: 1},
			{At: ms(0.78), Value: 1}, {At: ms(0.82), Value: 0}, {At: ms(1), Value: 0}}, loop)
	}
	grays := systemGrays(p.Dark)
	gray, gray2, gray4, gray5, gray6 := grays[0], grays[1], grays[3], grays[4], grays[5]
	accent, label, secondary, tertiary := p.Primary, p.Text, p.Secondary, p.Tertiary
	art.Draw(func(pt *ui.Painter, r ui.Rect) {
		s := r.W / 108
		rect := func(x, y, w, h, rad float32, col ui.Color) {
			pt.Fill(ui.Rect{X: r.X + x*s, Y: r.Y + y*s, W: w * s, H: h * s}, col, rad*s)
		}
		dot := func(cx, cy, rad float32, col ui.Color) {
			pt.Fill(ui.Rect{X: r.X + (cx-rad)*s, Y: r.Y + (cy-rad)*s, W: 2 * rad * s, H: 2 * rad * s}, col, rad*s)
		}
		// The plug, moved 28 units toward the socket as it reaches.
		dx := 28 * reach
		cable := new(ui.Path).MoveTo(r.X+(22+dx)*s, r.Y+24*s).
			CubeTo(r.X+(14+dx)*s, r.Y+24*s, r.X+(14+dx)*s, r.Y+34*s, r.X+(6+dx)*s, r.Y+34*s).
			CubeTo(r.X+(-2+dx)*s, r.Y+34*s, r.X+(-2+dx)*s, r.Y+24*s, r.X+(-30+dx)*s, r.Y+24*s)
		pt.StrokePath(cable, 3*s, secondary)
		rect(22+dx, 19, 10, 10, 3, secondary)
		rect(52+dx, 15, 10, 4, 1.5, gray)
		rect(52+dx, 29, 10, 4, 1.5, gray)
		rect(30+dx, 10, 22, 28, 8, label)
		for _, x := range []float32{35, 39, 43} {
			rect(x+dx, 17, 1.5, 14, 0.75, secondary)
		}
		// The socket over it, so seated prongs vanish into the slots.
		rect(80, 5, 26, 38, 7, gray5)
		rect(80, 10, 3, 28, 1.5, gray4)
		rect(80, 14.5, 6, 5, 1.5, gray2)
		rect(80, 28.5, 6, 5, 1.5, gray2)
		dot(92, 11, 1.8, tertiary)
		if lamp > 0 {
			dot(92, 11, 4.5, accent.Alpha(0.28*lamp))
			dot(92, 11, 1.8, accent.Alpha(lamp))
		}
		// The wall, cut flat where the socket is mounted.
		rect(100, 0, 8, 48, 0, gray6)
		rect(100, 0, 1, 48, 0, gray4)
	})
}
