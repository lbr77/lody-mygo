package app

import (
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// A held message in the conversation is Lody's PendingMessageRow: an
// ordinary user message on the right, not an error card. Its meta line
// carries one short status (Waiting to send, Not sent) beside its time,
// and the reason lives on the attachment that failed: its pictures framed
// as the delivered ones, its files as file cards, each uploading, stopped,
// ready or failed, a bar riding its bottom edge while it uploads. Under it,
// always in the flow at the delivered row's 28-DIP action height, Cancel
// send, and Continue sending once it failed. Written, the delivered
// message takes its place.

var (
	// iconClock3Bold is Lucide's clock-3 at a 2 stroke: a held message
	// waiting.
	iconClock3Bold = lucideStroke("2", `<circle cx="12" cy="12" r="10"/><path d="M12 6v6h4.5"/>`)
	// iconAlertHeavy is CircleAlert at Lody's 2.25 stroke, a send that
	// failed in the sidebar and the queue.
	iconAlertHeavy = lucideStroke("2.25", `<circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/>`)
)

// attachmentState is how a held message's attachment stands, as Lody's:
// ready wins over failed, as uploading clears a failure once it succeeds;
// without either it uploads while its message goes, and is stopped once
// the message failed.
func attachmentState(at sendAttachment, active bool) string {
	switch {
	case at.ready != nil:
		return "ready"
	case at.err != "":
		return "failed"
	case active:
		return "uploading"
	}
	return "interrupted"
}

// attachmentLabel is an attachment's one status line: a failed one's is
// its whole reason.
func attachmentLabel(at sendAttachment, state string) string {
	switch state {
	case "ready":
		return locales.AttachmentPrepared()
	case "failed":
		return at.err
	case "interrupted":
		return locales.AttachmentInterrupted()
	}
	return locales.AttachmentUploading(int(at.progress))
}

// pendingStatus is a held message's status beside its time: Not sent once
// it failed, else waiting, and saying so while its attachments upload.
func pendingStatus(s *pendingSend) string {
	switch {
	case s.err != "":
		return locales.PendingMessageUploadFailed()
	}
	for _, at := range s.attachments {
		if at.ready == nil {
			return locales.PendingMessageUploading()
		}
	}
	return locales.PendingMessageWaiting()
}

// pendingMessage is a held message in the conversation, laid out as the
// delivered message that takes its place: its column at the end, the
// avatar beside it.
func (a *App) pendingMessage(c *ui.Context, p *palette, s *pendingSend) {
	failed := s.err != ""
	var images, files []int
	reasonOnACard := false
	for i, at := range s.attachments {
		if at.file.Image {
			images = append(images, i)
			continue
		}
		files = append(files, i)
		// The message's reason shows on its own only where no card says
		// it, never twice; a picture's frame has no caption.
		reasonOnACard = reasonOnACard || attachmentState(at, true) == "failed"
	}
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Justify(ui.End).Children(func() {
		ui.Column(c).AlignItems(ui.End).Gap(6).Shrink(1).MinWidth(0).MaxWidth(560).Children(func() {
			status := pendingStatus(s)
			ui.Row(c.Key("meta")).Gap(6).AlignItems(ui.Center).MinWidth(0).Children(func() {
				ui.Row(c).Gap(4).AlignItems(ui.Center).MinWidth(0).Children(func() {
					icon := iconClock3Bold
					if failed {
						icon = iconAlertBold
					}
					ui.Icon(c, icon).Size(14, 14).Shrink(0).TextColor(p.Secondary)
					ui.Text(c, status).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine().MinWidth(0)
				}).Role(ui.RoleStatus).Description(status)
				ui.Text(c, conversationTime(s.at.UTC().Format(time.RFC3339Nano), time.Now())).FontSize(sizeCaption).FontFeatures("tnum").
					TextColor(p.Secondary).SingleLine().Shrink(0)
			})
			ui.Column(c.Key("content")).AlignItems(ui.End).Gap(8).MinWidth(0).Children(func() {
				if len(images) > 0 {
					ui.Row(c.Key("images")).FillWidth().Wrap().Gap(8).Justify(ui.End).Padding(4, 8, 0, 8).Children(func() {
						for _, i := range images {
							a.pendingImage(c, p, s.attachments[i], !failed, len(images) == 1)
						}
					})
				}
				if len(files) > 0 {
					ui.Column(c.Key("files")).FillWidth().Gap(8).AlignItems(ui.End).Padding(4, 8, 0, 8).Children(func() {
						for _, i := range files {
							pendingFileCard(c, p, s.attachments[i], !failed)
						}
					})
				}
				if s.text != "" {
					ui.Row(c.Key("text")).Justify(ui.End).MinWidth(0).Padding(0, 0, 0, 8).Children(func() {
						ui.Column(c).Padding(10, 16).Radius(16).Background(p.Text.Alpha(0.05)).MinWidth(0).Children(func() {
							ui.Text(c, s.text).Selectable().FontSize(a.convFontSize()).LineHeight(24.0 / 14).TextColor(p.Reading)
						})
					})
				}
			})
			if failed && !reasonOnACard {
				pendingFailureNotice(c, p, s.err)
			}
			ui.Row(c.Key("actions")).MinHeight(28).Wrap().Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				id := s.id
				lodyButton(c, p, "ghost", "cancel", nil, locales.CancelPendingSend(), true).OnClick(func() { a.cancelSend(id) })
				if failed {
					lodyButton(c, p, "primary", "retry", nil, locales.RetryPendingSend(), true).OnClick(func() { a.retrySend(id) })
				}
			})
		})
		ui.Box(c).Margin(28, 0, 0, 0).Children(func() {
			if a.account != nil {
				a.accountAvatar(c, p, 32)
			} else {
				userAvatar(c, p)
			}
		})
	})
}

// failedFrame is the whole red of a failure, as Lody's: a faint frame and
// one glyph are enough to find what broke.
func failedFrame(e ui.Element, p *palette) ui.Element {
	return e.Border(1, p.Danger.Alpha(0.3)).Background(p.Danger.Alpha(0.04))
}

// pendingFailureNotice is a reason no attachment card shows, in the same
// frame, so every failure of the message reads as one kind of thing: at
// most two lines of it.
func pendingFailureNotice(c *ui.Context, p *palette, reason string) {
	box := ui.Row(c.Key("failure")).FillWidth().MaxWidth(384).Gap(8).AlignItems(ui.Start).Padding(8, 10).Radius(8)
	failedFrame(box, p).Children(func() {
		ui.Icon(c, iconAlertBold).Size(14, 14).Shrink(0).Margin(1, 0, 0, 0).TextColor(p.Danger)
		ui.Text(c, reason).FontSize(sizeFoot).LineHeight(1.375).TextColor(p.Danger).MaxLines(2).Grow(1).MinWidth(0)
	})
}

// attachmentTrack is the bar an uploading attachment's frame carries on
// its bottom edge, adding no row: @lody/ui's Progress.
func attachmentTrack(c *ui.Context, p *palette, at sendAttachment) {
	ui.Box(c.Key("track")).Absolute().Left(0).Right(0).Bottom(0).PassThrough().Children(func() {
		progressBar(c, p, true, int(at.progress))
	})
}

// pendingImage frames a held picture as the delivered one that replaces
// it, without a caption: alone at its own size up to 168 tall, several as
// 160-DIP squares. A failed one has a neutral scrim, only to make its one
// glyph legible over the picture.
func (a *App) pendingImage(c *ui.Context, p *palette, at sendAttachment, active, single bool) {
	state := attachmentState(at, active)
	failed := state == "failed"
	img := a.images["file:"+at.file.Path]
	const square = 160
	frame := ui.Box(c.Key("image/" + at.file.Path)).Radius(12).Clip().Shrink(0).Transition(twFade)
	if failed {
		failedFrame(frame, p)
	} else {
		frame.Border(1, p.Border.Alpha(0.7)).Background(p.Hover.Alpha(0.2))
	}
	tip(c, p, frame, at.file.Name+" · "+attachmentLabel(at, state))
	frame.Children(func() {
		switch {
		case img != nil && img.bitmap != nil && single:
			w, h := img.bitmap.Size()
			scale := min(1, 168/float32(max(h, 1)), 544/float32(max(w, 1)))
			ui.Image(c.Key("bitmap"), img.bitmap).Size(float32(w)*scale, float32(h)*scale).Fit(ui.Contain)
		case img != nil && img.bitmap != nil:
			ui.Image(c.Key("bitmap"), img.bitmap).Size(square, square).Fit(ui.Cover)
		default:
			w := float32(square)
			if single {
				w = 320
			}
			h := float32(square)
			if single {
				h = 144
			}
			ui.Box(c).Size(w, h).Center().Children(func() { ui.Icon(c, iconImage).Size(24, 24).TextColor(p.Secondary) })
		}
		if failed {
			ui.Box(c.Key("scrim")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Background(p.Canvas.Alpha(0.55)).Center().Children(func() {
				ui.Icon(c, iconCircleAlert).Size(24, 24).TextColor(p.Danger)
			})
		}
		if state == "uploading" {
			attachmentTrack(c, p, at)
		}
	})
}

// pendingFileCard is a held file as the delivered file card, inert: its
// sheet, its name, its status where the type and size will be (they are,
// once it is ready), and what it is doing at its end: turning while it
// uploads, a clock once stopped, the alert once failed, the card faintly
// red then and the reason in red.
func pendingFileCard(c *ui.Context, p *palette, at sendAttachment, active bool) {
	state := attachmentState(at, active)
	failed := state == "failed"
	subtitle := attachmentLabel(at, state)
	if state == "ready" {
		size := at.file.Size
		if n, ok := at.ready["sizeBytes"].(float64); ok && n > 0 {
			size = int64(n)
		}
		subtitle = fileMeta(at.file.Name, size)
	}
	tray := secondaryGround(p)
	if failed {
		tray = tray.Mix(p.Danger, 0.08)
	}
	card := ui.Row(c.Key("file/" + at.file.Path)).FillWidth().MaxWidth(384).Radius(10).Gap(4).Clip().Background(tray).Transition(colorFade)
	card.Children(func() {
		ui.Row(c.Key("main")).Grow(1).MinWidth(0).Gap(10).AlignItems(ui.Center).Padding(6, 6, 6, 8).Label(at.file.Name).Children(func() {
			fileSheet(c, p, fileKind(at.file.Name, ""), false, false)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, at.file.Name).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine()
				ink := p.Secondary
				if failed {
					ink = p.Danger
				}
				tip(c, p, ui.Text(c, subtitle).FontSize(sizeFoot).TextColor(ink).FontFeatures("tnum").SingleLine(), subtitle)
			})
			ui.Box(c.Key("action")).Size(28, 28).Radius(5).Center().Shrink(0).Children(func() {
				ink := p.Secondary
				switch state {
				case "failed":
					ui.Icon(c, iconCircleAlert).Size(16, 16).TextColor(p.Danger)
				case "uploading":
					spinner(c).Size(16, 16)
				case "ready":
					icon := iconDownload
					switch {
					case at.ready["transport"] == "local":
						icon = iconClock3
					case at.ready["textPreview"] != nil && at.ready["textPreview"] != false:
						icon = iconEye
					}
					ui.Icon(c, icon).Size(16, 16).TextColor(ink)
				default:
					ui.Icon(c, iconClock3).Size(16, 16).TextColor(ink)
				}
			})
		})
		if state == "uploading" {
			attachmentTrack(c, p, at)
		}
	})
}
