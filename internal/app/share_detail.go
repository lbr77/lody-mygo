package app

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/locales"
)

// A published share in Settings → Sharing is a row as Lody's desktop app
// draws it: the publisher's face, which opens their name and email, the
// title, and when it was published; the rest of the row opens the
// share's details. The dialog holds what the row leaves out and the two
// places a share leads, each named: the conversation it was made from,
// and the static copy it published. Copy link, Update share, Reset link
// and Revoke share are there for those who may; Reset and Revoke close it
// under a question, and go ahead only while the share is still on the
// page at the revision asked about.

// sharePublisher is who published a share, as a member, or by the name
// Lody gives one who left; known is whether there is a name at all.
func (a *App) sharePublisher(id string, byUser map[string]cloud.Member) (cloud.User, bool) {
	if m, ok := byUser[id]; ok {
		return m.User, firstNonEmpty(m.User.Name, m.User.Email) != ""
	}
	if a.account != nil && id == a.account.User.ID {
		return a.account.User, true
	}
	if name := a.userName(id); name != "" && name != id {
		return cloud.User{Name: name}, true
	}
	return cloud.User{}, false
}

// publishedShareRow is one published share in the list.
func (a *App) publishedShareRow(c *ui.Context, p *palette, s publishedShare, byUser map[string]cloud.Member, now time.Time) {
	sp := &a.sharePages
	title := firstNonEmpty(s.Title, locales.UntitledSession())
	revoked := s.Status != "active"
	ui.Box(c.Key(s.ShareID)).FillWidth().Children(func() {
		// The row is one control, its whole width the title's; the face
		// lies above it with a control of its own.
		row := ui.ButtonBase(c.Key("open")).FillWidth().Padding(8, 16, 8, 16+32+12).AlignItems(ui.Start).Justify(ui.Start).
			Cursor(ui.CursorPointer).Label(title).Transition(colorFade)
		if row.Hovered() {
			row.Background(p.Canvas.Mix(p.Text, 0.04))
		}
		row.OnClick(func() {
			sp.detail, sp.detailOpen, sp.profile = s, true, ""
		})
		row.Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ink := p.Text
				if revoked {
					ink = p.Secondary
				}
				ui.Text(c, title).FontSize(sizeFoot).LineHeight(1.45).TextColor(ink).MaxLines(2)
				ui.Row(c).Gap(6).AlignItems(ui.Center).Margin(2, 0, 0, 0).Children(func() {
					ui.Text(c, sharePublishedAt(time.UnixMilli(int64(s.CreatedAt)), now)).FontSize(sizeFoot).TextColor(p.Secondary)
					if revoked {
						ui.Text(c, "·").FontSize(sizeFoot).TextColor(p.Secondary)
						ui.Text(c, locales.SharesRevoked()).FontSize(sizeFoot).TextColor(p.Secondary)
					}
				})
			})
		})
		a.sharePublisherFace(c, p, s, byUser)
	})
}

// sharePublisherFace is the publisher's face at the row's start: one
// known by name opens their profile beside it; one who isn't stays a face.
func (a *App) sharePublisherFace(c *ui.Context, p *palette, s publishedShare, byUser map[string]cloud.Member) {
	sp := &a.sharePages
	user, known := a.sharePublisher(s.PublisherUserID, byUser)
	if !known {
		ui.Box(c.Key("face")).Size(32, 32).Absolute().Left(16).Top(8).Children(func() { a.memberAvatar(c, p, user, 32) })
		return
	}
	name := firstNonEmpty(user.Name, user.Email)
	face := ui.ButtonBase(c.Key("face")).Size(32, 32).Radius(16).Absolute().Left(16).Top(8).Cursor(ui.CursorPointer).
		Label(locales.SharesPublisherProfile(name)).Transition(colorFade)
	if face.Hovered() {
		face.Opacity(0.85)
	}
	face.OnClick(func() {
		if sp.profile == s.ShareID {
			sp.profileOpen = !sp.profileOpen
		} else {
			sp.profile, sp.profileOpen = s.ShareID, true
		}
	})
	face.Children(func() { a.memberAvatar(c, p, user, 32) })
	if sp.profile != s.ShareID {
		return
	}
	pop := popup(c, face, &sp.profileOpen, func() {
		ui.Row(c.Key("profile")).Width(232).Padding(12).Gap(12).AlignItems(ui.Center).Children(func() {
			a.memberAvatar(c, p, user, 40)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, name).FontSize(sizeBody).FontWeight(600).TextColor(p.Text).SingleLine()
				if user.Email != "" {
					ui.Text(c, user.Email).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
				}
			})
		})
	})
	if pop.Bounds().W > 0 || sp.profileOpen {
		// To the face's right, its top on the face's.
		pop.AttachTo(face, ui.AnchorTopEnd, ui.AnchorTopStart).Margin(0, 0, 0, 10)
	}
}

// shareDetailDialog is the share's details: when it was published and
// last updated, how many conversations it holds, its size and status,
// then its destinations and what may be done with it.
func (a *App) shareDetailDialog(c *ui.Context, p *palette, page *sharesPage) {
	sp := &a.sharePages
	if !sp.detailOpen {
		return
	}
	s := sp.detail
	// The row as the page has it now, its revision and link current.
	for _, cur := range page.Page {
		if cur.ShareID == s.ShareID {
			s = cur
		}
	}
	title := firstNonEmpty(s.Title, locales.UntitledSession())
	entry := s.entry()
	link := a.shareLink(entry)
	revoked := s.Status != "active"
	mine := a.account != nil && s.PublisherUserID == a.account.User.ID
	source := ""
	if a.catalog.Session(s.RootSessionID) != nil {
		source = s.RootSessionID
	}
	busy := sp.op != nil && sp.op.busy
	lodyDialog(c, p, &sp.detailOpen, title, func() {
		lodyDialogHeader(c, p, title, "", nil)
		ui.Column(c.Key("facts")).FillWidth().Dividers(1, p.Border).Children(func() {
			fact := func(label, value string) {
				ui.Row(c.Key(label)).FillWidth().Padding(6, 0).Gap(16).AlignItems(ui.Baseline).Justify(ui.SpaceBetween).Children(func() {
					ui.Text(c, label).FontSize(sizeFoot).TextColor(p.Secondary).Shrink(0)
					ui.Text(c, value).FontSize(sizeFoot).TextColor(p.Text).TextAlign(ui.End).MinWidth(0)
				})
			}
			// Y-M-D reads the same in every language.
			fact(locales.SharesPublishedAt(), formatYMD(time.UnixMilli(int64(s.CreatedAt))))
			if s.UpdatedAt-s.CreatedAt > 1000 {
				fact(locales.SharesUpdatedAt(), formatYMD(time.UnixMilli(int64(s.UpdatedAt))))
			}
			fact(locales.SharesConversationsLabel(), fmt.Sprint(int(s.ConversationCount)))
			fact(locales.SharesSize(), shareSize(s.TotalBytes))
			status := locales.SharesActive()
			if revoked {
				status = locales.SharesRevoked()
			}
			fact(locales.SharesStatusLabel(), status)
		})
		// Why there is no link, where Copy link would be: each reason has
		// its own remedy.
		if link == "" {
			note := locales.SharesLinkOtherPublisher()
			switch {
			case revoked:
				note = locales.SharesLinkRevoked()
			case mine:
				note = locales.SharesLinkOtherDevice()
			}
			ui.Text(c.Key("link-note"), note).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Secondary)
		}
		if link != "" || source != "" {
			ui.Row(c.Key("go")).FillWidth().Wrap().Gap(8).Children(func() {
				if link != "" {
					lodyButton(c, p, "primary", "open-published", nil, locales.SharesOpenPublished(), true).OnClick(func() { openExternal(link) })
					lodyButton(c, p, "secondary", "copy", nil, locales.ShareCopy(), !busy).OnClick(func() {
						if sp.op != nil && sp.op.busy {
							return
						}
						writeClipboard(link)
						a.toast("success", locales.ShareCopied())
					})
				}
				if source != "" {
					lodyButton(c, p, "secondary", "open-conversation", nil, locales.SharesOpenConversation(), true).OnClick(func() {
						// Settings is a dialog over the workspace, so the
						// conversation shows once it is gone.
						sp.detailOpen, a.settingsOpen = false, false
						a.open(source)
					})
				}
			})
		}
		if !s.CanManage && !s.CanRevoke {
			return
		}
		ui.Row(c.Key("manage")).FillWidth().Wrap().Gap(8).Children(func() {
			if s.CanManage && source != "" {
				lodyButton(c, p, "secondary", "update", nil, locales.ShareUpdate(), !busy).OnClick(func() {
					sp.detailOpen = false
					a.openShare(source)
				})
			}
			if s.CanManage {
				lodyButton(c, p, "secondary", "reset", nil, locales.ShareReset(), !busy).OnClick(func() {
					sp.detailOpen = false
					a.confirmShareChange("reset", s)
				})
			}
			if s.CanRevoke {
				b, _ := lodyButtonLook(c, p, "secondary", "revoke", locales.ShareRevoke(), !busy)
				b.Children(func() {
					ui.Text(c, locales.ShareRevoke()).FontSize(sizeSub).FontWeight(500).LetterSpacing(-0.13).TextColor(p.Danger).SingleLine()
				})
				b.OnClick(func() {
					sp.detailOpen = false
					a.confirmShareChange("revoke", s)
				})
			}
		})
	})
}

// confirmShareChange asks before a share's link is reset or the share
// revoked, as Lody's does: the old link stops working. It goes ahead only
// while the share is on the page shown, at the revision asked about.
func (a *App) confirmShareChange(kind string, s publishedShare) {
	title := locales.ShareReset()
	if kind == "revoke" {
		title = locales.ShareRevoke()
	}
	sp := &a.sharePages
	key := sharesKey(sp.cursor())
	a.confirmSafe(title, locales.ShareInvalidateNotice(), locales.ShareConfirm(), func() {
		page, _ := a.fetches[a.workspaceID+"|"+key].value.(*sharesPage)
		current := false
		if page != nil {
			for _, cur := range page.Page {
				current = current || cur.ShareID == s.ShareID && cur.Revision == s.Revision
			}
		}
		if !current || sp.op != nil && sp.op.busy {
			return
		}
		job := &shareJob{root: s.RootSessionID, entry: s.entry(), loaded: true}
		sp.op = job
		if kind == "reset" {
			a.resetShareLink(job)
		} else {
			a.revokeShare(job)
		}
	})
}

// entry is the share as its management reads it.
func (s publishedShare) entry() *shareEntry {
	return &shareEntry{ShareID: s.ShareID, RootSessionID: s.RootSessionID, PublisherUserID: s.PublisherUserID, Status: s.Status,
		Revision: s.Revision, CredentialVersion: s.CredentialVersion, Title: s.Title, CanManage: s.CanManage, CanRevoke: s.CanRevoke}
}

// sharePublishedAt places a share in time as Lody's list does, coarser
// with age: the clock time today, the month and day this year, the full
// date before.
func sharePublishedAt(at, now time.Time) string {
	at, now = at.Local(), now.Local()
	switch {
	case at.Year() == now.Year() && at.YearDay() == now.YearDay():
		return at.Format("15:04")
	case at.Year() == now.Year():
		return at.Format("01-02")
	}
	return formatYMD(at)
}

// formatYMD is a date as Y-M-D, which reads the same in every language.
func formatYMD(at time.Time) string { return at.Local().Format("2006-01-02") }

// shareSize is a share's size as Lody's details give it: bytes, then KB,
// MB or GB with one decimal under ten.
func shareSize(n float64) string {
	if n < 1024 {
		return fmt.Sprintf("%.0f B", n)
	}
	units := []string{"KB", "MB", "GB"}
	v, u := n/1024, 0
	for v >= 1024 && u < len(units)-1 {
		v /= 1024
		u++
	}
	if v < 10 {
		return fmt.Sprintf("%.1f %s", v, units[u])
	}
	return fmt.Sprintf("%.0f %s", math.Round(v), units[u])
}
