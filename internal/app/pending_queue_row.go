package app

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// pendingQueueRow is a held message routed to the queue, as Lody's
// PendingQueueRow: not a queue item yet, so it cannot be dragged, edited
// or used to steer. It stands where the real item will be appended, laid
// out as a queued row: its number, its first three attachments and how
// many more, and its text, muted until it is a real item. Its end says
// how its upload goes, a ring filling with the bytes sent beside
// "Uploading · N%" (or Sending without bytes to measure), or, failed,
// offers Continue sending; Cancel send ends it. A message with nothing to
// upload reads as queued, without a status. The real item takes its place
// as it syncs in.
func (a *App) pendingQueueRow(c *ui.Context, p *palette, st *queueView, s *pendingSend, index int) {
	failed := s.err != ""
	instant := !failed && len(s.attachments) == 0
	row := ui.Row(c.Key("pending/" + s.id)).FillWidth().AlignItems(ui.Start).Gap(8).Padding(6).Radius(5).Transition(queueRowFade)
	if row.Hovered() && !st.drag.active {
		row.Background(p.Hover)
	}
	id := s.id
	row.Children(func() {
		ui.Box(c.Key("number")).Size(queueGutter, queueLine).Shrink(0).Center().Translate(0, 1).Children(func() {
			ui.Text(c, fmt.Sprint(index+1)).FontSize(11).FontWeight(500).FontFeatures("tnum").TextColor(p.Secondary.Alpha(0.7)).SingleLine()
		})
		ui.Row(c.Key("body")).Grow(1).MinWidth(0).AlignItems(ui.Start).Gap(6).Children(func() {
			if len(s.attachments) > 0 {
				a.localThumbs(c, p, s.attachments)
			}
			// Muted until it is a real queue item, as an unsent
			// conversation's title.
			ink := p.Text.Alpha(0.86)
			if !instant {
				ink = p.Text.Alpha(0.55)
			}
			ui.Text(c.Key("text"), s.text).FontSize(sizeSub).LineHeight(queueLeading).TextColor(ink).MaxLines(2).Grow(1).MinWidth(0)
		})
		ui.Row(c.Key("actions")).Height(queueLine).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
			switch {
			case failed:
				ui.Icon(c, iconAlertHeavy).Size(12, 12).TextColor(p.Danger).Label(locales.PendingMessageUploadFailed())
				queueTextAction(c, p, "retry", locales.RetryPendingSend()).OnClick(func() { a.retrySend(id) })
			case !instant:
				total, sent := s.bytes()
				progress, known := sendProgress(total, sent)
				label := locales.SendStatusSendingWithoutSize()
				if known {
					label = locales.AttachmentUploading(progress)
				}
				ui.Row(c.Key("status")).Gap(4).AlignItems(ui.Center).Role(ui.RoleStatus).Children(func() {
					sendRing(c, p, float32(progress), known, "")
					ui.Text(c, label).FontSize(sizeCaption).FontFeatures("tnum").TextColor(p.Secondary).SingleLine()
				})
			}
			queueAction(c, p, "cancel", iconX, locales.CancelPendingSend(), true, false).OnClick(func() { a.cancelSend(id) })
		})
	})
}

// queueTextAction is Lody's TextAction: a 20-DIP word in the muted ink,
// its fill and ink coming up under the pointer as Tailwind's
// transition-colors.
func queueTextAction(c *ui.Context, p *palette, key, text string) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Height(20).Padding(0, 6).Radius(4).Center().Shrink(0).Cursor(ui.CursorPointer).Label(text).Transition(twFade)
	ink := p.Secondary
	if b.Hovered() {
		b.Background(p.Hover)
		ink = p.Text
	}
	b.Children(func() { ui.Text(c, text).FontSize(sizeCaption).FontWeight(500).TextColor(ink).SingleLine() })
	return b
}

// localThumbs are a held message's first three attachments, as Lody's
// LocalThumbnail: a picture as it is on disk, any other file its glyph,
// each named in its tip; and how many more there are.
func (a *App) localThumbs(c *ui.Context, p *palette, attachments []sendAttachment) {
	const most = 3
	ui.Row(c.Key("thumbs")).Gap(2).AlignItems(ui.Center).Shrink(0).Padding(1, 0, 0, 0).Children(func() {
		for i, at := range attachments {
			if i == most {
				ui.Text(c, fmt.Sprintf("+%d", len(attachments)-most)).FontSize(10).FontFeatures("tnum").TextColor(p.Secondary.Alpha(0.7)).Margin(0, 0, 0, 2)
				break
			}
			t := ui.Box(c.Key(fmt.Sprint("thumb/", i))).Size(18, 18).Shrink(0).Radius(4).Border(1, p.Border.Alpha(0.4)).Background(p.Canvas.Alpha(0.6)).Clip().Center()
			tip(c, p, t, at.file.Name)
			img := a.images["file:"+at.file.Path]
			t.Children(func() {
				if at.file.Image && img != nil && img.bitmap != nil {
					ui.Image(c.Key("bitmap"), img.bitmap).Size(18, 18).Fit(ui.Cover)
					return
				}
				ui.Icon(c, iconFile).Size(10, 10).TextColor(p.Secondary)
			})
		}
	})
}
