package app

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// Connecting a machine, as Lody's "Add machine" does: the backend mints a
// one-time pairing token (/api/cli/machine-pairing/create), the user runs
// the daemon with it on the machine, and the request goes pending →
// claimed → registered as the machine signs in (machinePairing:getRequest).
//
// And authorizing a CLI device: the code the CLI shows is checked
// (/api/auth/device) and approved (/api/cli/device/approve), as Lody's
// /device page does.

const desktopDownloadURL = "https://lody.ai/download"

type machinePairing struct {
	open      bool
	creating  bool
	createErr string
	requestID string
	command   string
	expiresAt float64
	status    string // pending, claimed, registered, cancelled, expired
	machineID string
	machine   string // its name
	notified  string // the request whose success was told
	selected  string // the request whose machine was picked
}

// addMachine opens the dialog, on the request in progress if there is one.
func (a *App) addMachine() {
	pr := &a.pairing
	if pr.requestID != "" && (pr.status == "pending" || pr.status == "claimed" || pr.status == "registered") {
		pr.open = true
		return
	}
	a.createPairing()
}

func (a *App) createPairing() {
	a.pairing = machinePairing{open: true, creating: true}
	pr := &a.pairing
	if a.account == nil || a.workspaceID == "" {
		pr.creating, pr.createErr = false, "missing_context"
		return
	}
	api, token, ws := a.api, a.tokenNow(), a.workspaceID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var res struct {
			Request struct {
				ID        string  `json:"id"`
				Status    string  `json:"status"`
				ExpiresAt float64 `json:"expiresAt"`
			} `json:"request"`
			Token string `json:"token"`
		}
		err := api.Call(ctx, token, "/api/cli/machine-pairing/create", map[string]any{"workspaceId": ws}, &res)
		a.update(func() {
			if a.pairing.open != pr.open || !pr.creating {
				return
			}
			pr.creating = false
			if err != nil || res.Request.ID == "" || res.Token == "" {
				pr.createErr = "invalid_response"
				if err != nil {
					pr.createErr = err.Error()
				}
				return
			}
			pr.requestID, pr.expiresAt, pr.status = res.Request.ID, res.Request.ExpiresAt, firstNonEmpty(res.Request.Status, "pending")
			pr.command = "npx lody@latest daemon start --auth " + res.Token
			a.watchPairing(pr.requestID)
		})
	}()
}

// watchPairing follows a request until it settles, as Lody's live query
// does.
func (a *App) watchPairing(id string) {
	if a.convex == nil || a.offline {
		return
	}
	convex := a.convex
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			var view *struct {
				ID          string  `json:"id"`
				Status      string  `json:"status"`
				MachineID   string  `json:"machineId"`
				MachineName string  `json:"machineName"`
				ExpiresAt   float64 `json:"expiresAt"`
			}
			err := convex.Query(ctx, "machinePairing:getRequest", map[string]any{"requestId": id}, &view)
			cancel()
			done := false
			a.update(func() {
				pr := &a.pairing
				if pr.requestID != id {
					done = true
					return
				}
				if err != nil {
					return
				}
				if view == nil {
					pr.status = "expired"
				} else {
					pr.status, pr.machineID, pr.machine = view.Status, view.MachineID, view.MachineName
				}
				a.pairingSettled()
				done = pr.status == "registered" || pr.status == "cancelled" || pr.status == "expired"
			})
			if done {
				return
			}
			time.Sleep(2 * time.Second)
		}
	}()
}

// pairingSettled tells of a machine connected while the dialog is closed,
// and picks it for new chats once it shows.
func (a *App) pairingSettled() {
	pr := &a.pairing
	if pr.status != "registered" {
		return
	}
	if !pr.open && pr.notified != pr.requestID {
		pr.notified = pr.requestID
		a.toasts = append(a.toasts, ui.Toast{Type: "success", Title: locales.PairingConnectedToast(firstNonEmpty(pr.machine, locales.HomeMachine())),
			Description: locales.PairingPrivateByDefault(), Action: locales.PairingOpenMachineSettings(),
			OnAction: func() { a.settingsOpen, a.settingsTab, a.fetches = true, "machines", nil }})
	}
	if pr.selected != pr.requestID && pr.machineID != "" {
		for _, id := range a.catalog.MachineIDs {
			if id == pr.machineID {
				pr.selected = pr.requestID
				a.newChat.machine, a.newChat.project = id, ""
				a.pickNewChatDefaults()
			}
		}
	}
}

func (a *App) cancelPairing() {
	pr := &a.pairing
	id := pr.requestID
	if id == "" || a.convex == nil {
		return
	}
	convex := a.convex
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := convex.Mutation(ctx, "machinePairing:cancelRequest", map[string]any{"requestId": id}, nil)
		a.update(func() {
			if err != nil {
				a.toast("danger", firstNonEmpty(err.Error(), locales.PairingCancelFailed()))
				return
			}
			if pr.requestID == id {
				*pr = machinePairing{}
			}
		})
	}()
}

// closePairing closes the dialog, forgetting a request that is over.
func (a *App) closePairing() {
	pr := &a.pairing
	pr.open = false
	if pr.status == "registered" || pr.status == "cancelled" || pr.status == "expired" {
		*pr = machinePairing{notified: pr.notified, selected: pr.selected}
	}
}

// remaining is the time left as m:ss.
func remaining(ms float64) string {
	s := int((ms + 999) / 1000)
	if s < 0 {
		s = 0
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func (a *App) pairingDialog(c *ui.Context, p *palette) {
	pr := &a.pairing
	if !pr.open {
		return
	}
	open := true
	sheet(c, p, &open, 576, func() {
		if pr.status == "registered" && pr.machineID != "" {
			ui.Row(c).FillWidth().Padding(18, 14, 8, 20).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, markSuccess).Size(20, 20).TextColor(p.Success)
				ui.Text(c, locales.PairingConnectedTitle()).FontSize(16).FontWeight(600).TextColor(p.Strong).Grow(1)
				iconButton(c, p, iconX, locales.Close(), p.Hover).OnClick(a.closePairing)
			})
			ui.Text(c, locales.PairingConfigureDescription(firstNonEmpty(pr.machine, pr.machineID))).FontSize(sizeFoot).TextColor(p.Secondary).Padding(0, 20)
			ui.Row(c).FillWidth().Padding(16, 20).Gap(8).Justify(ui.End).Children(func() {
				ghostButton(c, p, locales.PairingSkipAgentSetup()).OnClick(a.closePairing)
				machine := pr.machineID
				primaryButton(c, p, locales.PairingConfigureAgents(), true).OnClick(func() {
					a.closePairing()
					a.settingsOpen, a.settingsTab, a.fetches = true, "agents", nil
					a.openProvider(machine, nil)
				})
			})
			return
		}
		sheetHeader(c, p, locales.PairingTitle(), locales.PairingDescription(), a.closePairing)
		ui.Column(c).FillWidth().Padding(4, 20, 8, 20).Gap(12).Children(func() {
			center := func(fn func()) {
				ui.Column(c).FillWidth().MinHeight(160).Gap(12).AlignItems(ui.Center).Justify(ui.Center).Children(fn)
			}
			switch {
			case pr.creating:
				center(func() { spinner(c).Size(20, 20) })
			case pr.createErr != "":
				center(func() {
					ui.Text(c, locales.PairingCreateFailed()).FontSize(sizeSub).TextColor(p.Danger)
					outlineButton(c, p, nil, locales.PairingRetry()).OnClick(a.createPairing)
				})
			case pr.status == "pending":
				a.pairingOptions(c, p, pr)
			case pr.status == "claimed":
				center(func() {
					ui.Icon(c, iconTerminal).Size(32, 32).TextColor(p.Secondary)
					ui.Column(c).Gap(4).AlignItems(ui.Center).Children(func() {
						ui.Text(c, locales.PairingClaimed()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
						ui.Text(c, locales.PairingPreparingAgents()).FontSize(sizeCaption).TextColor(p.Secondary)
					})
					spinner(c).Size(16, 16)
				})
			default:
				center(func() {
					line := locales.PairingExpired()
					if pr.status == "cancelled" {
						line = locales.PairingCancelled()
					}
					ui.Text(c, line).FontSize(sizeSub).TextColor(p.Secondary)
					outlineButton(c, p, nil, locales.PairingCreateNew()).OnClick(a.createPairing)
				})
			}
		})
		if pr.status == "pending" || pr.status == "claimed" {
			ui.Row(c).FillWidth().Padding(8, 20, 16, 20).Justify(ui.End).Children(func() {
				ghostButton(c, p, locales.PairingCancelRequest()).OnClick(a.cancelPairing)
			})
		} else {
			ui.Spacer(c).Height(12)
		}
	})
	if !open {
		a.closePairing()
	}
}

// pairingOptions are the two ways to connect: the desktop app, or the
// command with its time left.
func (a *App) pairingOptions(c *ui.Context, p *palette, pr *machinePairing) {
	option := func(icon *ui.SVG, title, lead string, body func()) {
		ui.Column(c).FillWidth().Padding(16).Gap(12).Radius(10).Border(1, p.Border).Children(func() {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Size(36, 36).Shrink(0).Radius(8).Background(p.Text.Alpha(0.05)).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, icon).Size(16, 16).TextColor(p.Secondary)
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, title).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
					ui.Text(c, lead).FontSize(sizeCaption).TextColor(p.Secondary)
				})
			})
			body()
		})
	}
	option(iconLaptop, locales.PairingDesktopTitle(), locales.PairingDesktopDescription(), func() {
		b := ui.ButtonBase(c.Key("download-desktop")).FillWidth().Height(32).Radius(8).Gap(6).Justify(ui.Center).AlignItems(ui.Center).
			Background(p.Text).Cursor(ui.CursorPointer).Label(locales.PairingDownloadDesktop()).OnClick(func() { openExternal(desktopDownloadURL) })
		pressable(b, 0.98)
		b.Children(func() {
			ui.Icon(c, iconDownload).Size(16, 16).TextColor(p.Canvas)
			ui.Text(c, locales.PairingDownloadDesktop()).FontSize(sizeSub).FontWeight(500).TextColor(p.Canvas)
		})
	})
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Height(1).Background(p.Border)
		ui.Text(c, locales.PairingOr()).FontSize(sizeCaption).TextColor(p.Secondary)
		ui.Column(c).Grow(1).Height(1).Background(p.Border)
	})
	option(iconTerminal, locales.PairingCliTitle(), locales.PairingCliDescription(), func() {
		ui.Row(c).FillWidth().Padding(12, 8, 12, 12).Gap(8).Radius(6).Border(1, p.Border).Background(p.Text.Alpha(0.03)).AlignItems(ui.Start).Children(func() {
			ui.Text(c, pr.command).Font(fontMono).FontSize(sizeCaption).LineHeight(1.65).TextColor(p.Text).Grow(1).MinWidth(0).Selectable()
			command := pr.command
			iconButton(c, p, iconCopy, locales.PairingCopyCommand(), p.Hover).Tooltip(locales.PairingCopyCommand()).OnClick(func() {
				mygo.Clipboard.WriteText(command)
				a.toast("success", locales.PairingCommandCopied())
			})
		})
		left := pr.expiresAt - float64(time.Now().UnixMilli())
		c.After(time.Second)
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconClock).Size(14, 14).TextColor(p.Secondary)
			ui.Text(c, locales.PairingExpiresIn(remaining(left))).FontSize(sizeCaption).TextColor(p.Secondary)
		})
	})
}

// deviceAuth is the dialog that approves a CLI's sign-in by its code.
type deviceAuth struct {
	open      bool
	code      string
	focused   bool // the code's field, as the last frame had it
	verifying bool
	err       string
	shook     uint64 // bumped on each failure, to shake the slots
	success   bool
}

// normalizeDeviceCode keeps a code's letters and digits, up to 8, in
// capitals.
func normalizeDeviceCode(v string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(v) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			if b.Len() == 8 {
				break
			}
		}
	}
	return b.String()
}

func (a *App) approveDevice() {
	d := &a.device
	code := normalizeDeviceCode(d.code)
	if len(code) != 8 || d.verifying {
		return
	}
	d.verifying, d.err = true, ""
	api, token := a.api, a.tokenNow()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := api.Call(ctx, token, "/api/auth/device?user_code="+url.QueryEscape(code), nil, nil)
		if err == nil {
			err = api.Call(ctx, token, "/api/cli/device/approve", map[string]any{"userCode": code}, nil)
		}
		a.update(func() {
			d.verifying = false
			if err != nil {
				d.err, d.shook = err.Error(), d.shook+1
				return
			}
			d.success = true
		})
	}()
}

func (a *App) deviceAuthDialog(c *ui.Context, p *palette) {
	d := &a.device
	if !d.open {
		return
	}
	sheet(c, p, &d.open, 440, func() {
		if d.success {
			sheetHeader(c, p, locales.DeviceTitle(), "", func() { d.open = false })
			ui.Column(c).FillWidth().Padding(8, 20, 20, 20).Gap(12).AlignItems(ui.Center).Children(func() {
				doneMark(c, p)
				ui.Text(c, locales.DeviceSuccess()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).TextAlign(ui.Center)
			})
			ui.Row(c).FillWidth().Padding(0, 20, 16, 20).Justify(ui.End).Children(func() {
				primaryButton(c, p, locales.Close(), true).OnClick(func() { d.open = false })
			})
			return
		}
		sheetHeader(c, p, locales.DeviceTitle(), locales.DeviceDescription(), func() { d.open = false })
		ui.Column(c).FillWidth().Padding(4, 20, 8, 20).Gap(8).Children(func() {
			ui.Text(c, locales.DeviceVerificationCode()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
			a.codeSlots(c, p, d)
			ui.Text(c, locales.DeviceCodeHint()).FontSize(sizeFoot).TextColor(p.Tertiary)
			if d.err != "" {
				ui.Row(c).FillWidth().Gap(12).Padding(12).Radius(10).Background(p.Danger.Alpha(0.08)).Children(func() {
					ui.Icon(c, markDanger).Size(16, 16).TextColor(p.Danger).Margin(1, 0, 0, 0)
					ui.Text(c, d.err).FontSize(sizeFoot).TextColor(p.Text).Grow(1).MinWidth(0).Selectable()
				})
			}
			if a.account != nil {
				ui.Text(c, locales.DeviceLoginAs()+" "+a.account.User.Name+" ("+a.account.User.Email+")").FontSize(sizeFoot).TextColor(p.Secondary)
			}
		})
		ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			label := locales.DeviceVerifyButton()
			if d.verifying {
				spinner(c).Size(14, 14)
				label = locales.DeviceVerifying()
			}
			primaryButton(c, p, label, len(normalizeDeviceCode(d.code)) == 8 && !d.verifying).OnClick(a.approveDevice)
		})
	})
}

// codeSlots shows the code as Lody does: eight wells, 4-4, a caret in the
// next one, each character arriving from below as it is typed; the row
// shakes when the code is refused and the characters breathe while it is
// checked.
func (a *App) codeSlots(c *ui.Context, p *palette, d *deviceAuth) {
	code := normalizeDeviceCode(d.code)
	invalid := d.err != ""
	row := ui.Row(c.Key("device-code")).FillWidth().Gap(4).AlignItems(ui.Center)
	shake := row.Keyframes("shake", []ui.Keyframe{
		{Value: 0}, {At: 92 * time.Millisecond, Value: -3}, {At: 184 * time.Millisecond, Value: 3},
		{At: 276 * time.Millisecond, Value: -3}, {At: 368 * time.Millisecond, Value: 3}, {At: 460 * time.Millisecond, Value: 0},
	}, ui.KeyframeOptions{Run: d.shook})
	if d.shook > 0 {
		row.Translate(shake, 0)
	}
	row.Children(func() {
		active := min(len(code), 7)
		for i := 0; i < 8; i++ {
			slot := ui.Row(c.Key(i)).Grow(1).Basis(0).MinWidth(0).Height(44).Radius(6).Justify(ui.Center).AlignItems(ui.Center).Gap(2).
				Background(p.Text.Alpha(0.05)).Transition(ui.ElementTransition{Colors: true, Duration: durFast, Ease: easeLody})
			focused := d.focused
			switch {
			case invalid:
				slot.Border(1, p.Danger)
			case focused && i == active:
				slot.Border(2, p.Primary)
			}
			if i == active && !d.verifying {
				slot.Background(p.Popover)
			}
			slot.Children(func() {
				if i < len(code) {
					ink := p.Text
					if invalid {
						ink = p.Danger
					}
					ch := ui.Text(c.Key(fmt.Sprint(i, code[i:i+1])), code[i:i+1]).Font(fontMono).FontSize(21).TextColor(ink).FontFeatures("tnum")
					k := ch.Keyframes("enter", []ui.Keyframe{{Value: 0}, {At: 260 * time.Millisecond, Value: 1, Ease: ui.CubicBezier(0.22, 1, 0.36, 1)}})
					if k < 1 {
						ch.Opacity(k).Translate(0, 8*(1-k)).Scale(0.88+0.12*k, 0.88+0.12*k).Blur(3 * (1 - k))
					}
					if d.verifying {
						pulse := ch.Keyframes("checking", []ui.Keyframe{{Value: 1}, {At: 360 * time.Millisecond, Value: 0.92, Ease: easeLody}, {At: 720 * time.Millisecond, Value: 1, Ease: easeLody}},
							ui.KeyframeOptions{Repeat: ui.RepeatForever, Delay: time.Duration(i) * 110 * time.Millisecond})
						ch.Scale(pulse, pulse)
					}
				}
				if focused && i == active && !d.verifying {
					caret := ui.Column(c.Key("caret")).Width(2).Height(19).Radius(1).Background(p.Primary)
					blink := caret.Keyframes("blink", []ui.Keyframe{{Value: 1}, {At: 450 * time.Millisecond, Value: 0.2, Ease: ui.EaseInOut}, {At: 900 * time.Millisecond, Value: 1, Ease: ui.EaseInOut}},
						ui.KeyframeOptions{Repeat: ui.RepeatForever})
					caret.Opacity(blink)
				}
			})
			if i == 3 {
				ui.Text(c, "-").FontSize(sizeBody).TextColor(p.Tertiary).Width(12).TextAlign(ui.Center).Shrink(0)
			}
		}
		before := d.code
		field := ui.TextInput(c.Key("device-code-input"), &d.code).Label(locales.DeviceVerificationCode()).Placeholder(locales.DeviceCodePlaceholder()).
			Absolute().Left(0).Top(0).Right(0).Bottom(0).Opacity(0).AutoFocus().Disabled(d.verifying)
		if d.code != before {
			d.code = normalizeDeviceCode(d.code)
			d.err = ""
		}
		d.focused = field.Focused()
		if field.Submitted() {
			a.approveDevice()
		}
	})
}

// doneMark is the tick that settles in when something is done.
func doneMark(c *ui.Context, p *palette) {
	ring := ui.Row(c.Key("done-mark")).Size(44, 44).Radius(22).Background(p.Success.Alpha(0.12)).Justify(ui.Center).AlignItems(ui.Center)
	k := ring.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: durSlow, Value: 1, Ease: ui.CubicBezier(0.22, 1, 0.36, 1)}})
	ring.Opacity(k).Scale(0.8+0.2*k, 0.8+0.2*k)
	ring.Children(func() { ui.Icon(c, markSuccess).Size(22, 22).TextColor(p.Success) })
}
