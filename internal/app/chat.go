package app

import (
	"fmt"
	"github.com/lbr77/lody-mygo/locales"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/session"
)

const (
	columnWidth  = 768 // Lody's conversation content
	columnGutter = 18
)

func (a *App) mainView(c *ui.Context, p *palette) {
	c.OnShortcut(ui.Cmd, ui.KeyN, func() { a.open("") })
	c.OnShortcut(ui.Cmd, ui.KeyL, func() { a.composerFocus.Focus(c) })
	c.OnShortcut(ui.Cmd, ui.KeyBackslash, func() { a.sidebarHidden = !a.sidebarHidden })
	ui.Row(c).Fill().AlignItems(ui.Stretch).Background(p.Ground).Children(func() {
		if !a.sidebarHidden {
			a.sidebar(c, p)
		}
		left := float32(0)
		if a.sidebarHidden {
			left = 4
		}
		card := ui.Column(c).Grow(1).MinWidth(0).Margin(4, 4, 4, left).Radius(14).Border(0.5, p.CardRim).Background(p.Canvas).Clip()
		if !p.Dark {
			card.Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.03))
		}
		card.Children(func() {
			switch {
			case a.page == "archive":
				a.archiveView(c, p)
			case a.selected == "":
				a.homeView(c, p)
			default:
				a.sessionView(c, p)
			}
		})
	})
	a.settingsDialog(c, p)
}

// tabBar is the 44px bar over the conversation: the session's tab, New
// chat, and the session's actions. Its empty part drags the window.
func (a *App) tabBar(c *ui.Context, p *palette, sess *catalog.Session) {
	left := float32(3)
	if a.sidebarHidden {
		left = 76 // clear the traffic lights
	}
	ui.Row(c).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Padding(0, 8, 0, left).Gap(4).DragWindow().Children(func() {
		if a.sidebarHidden {
			iconButton(c, p, iconSidebar, locales.SidebarToggle(), p.Hover).OnClick(func() { a.sidebarHidden = false })
		}
		if sess != nil {
			a.sessionTab(c, p, sess)
			iconButton(c, p, iconPlus, locales.NewChat(), p.Hover).OnClick(func() { a.open("") })
		}
		ui.Spacer(c)
		if sess != nil {
			s := *sess
			iconButton(c, p, iconMore, locales.MoreActions(), p.Hover).Menu(func(m *ui.Menu) { a.sessionMenu(m, s) })
		}
	})
}

// sessionTab is a session's tab. The only tab has no fill.
func (a *App) sessionTab(c *ui.Context, p *palette, s *catalog.Session) {
	ui.Row(c).Height(32).Radius(10).Padding(0, 12).Gap(6).Border(1, ui.Transparent).AlignItems(ui.Center).MinWidth(0).Shrink(1).Children(func() {
		ui.Box(c).Size(12, 12).Center().Shrink(0).Children(func() {
			switch {
			case s.Waiting():
				ui.Icon(c, iconHand).Size(12, 12).TextColor(p.Warning)
			case s.Working():
				ui.Spinner(c).Size(12, 12)
			case s.Unread() && a.selected != s.ID:
				ui.Box(c).Size(8, 8).Radius(4).Background(p.Primary)
			default:
				ui.Icon(c, iconBot).Size(12, 12).TextColor(p.Text).Opacity(0.6)
			}
		})
		ui.Text(c, firstNonEmpty(s.Title, locales.UntitledSession())).FontSize(12.6).TextColor(p.Text).SingleLine().MinWidth(0).MaxWidth(320)
	})
}

func (a *App) sessionView(c *ui.Context, p *palette) {
	sess := a.catalog.Session(a.selected)
	a.tabBar(c, p, sess)
	v := a.view
	if v == nil || (v.Status != "live" && len(v.Entries) == 0) {
		ui.Column(c).Grow(1).Center().Gap(10).Children(func() {
			ui.Spinner(c).Size(18, 18)
			msg := locales.LoadingConversation()
			if v != nil && v.Reason != "" {
				msg = locales.Reconnecting(v.Reason)
			}
			ui.Text(c, msg).FontSize(sizeSub).TextColor(p.Secondary)
		})
		a.bottom(c, p, sess, nil)
		return
	}
	a.syncMarkdown(p)
	entries := v.Entries
	a.chatList.Key = func(i int) any {
		if i < len(entries) {
			return entries[i].ID
		}
		return i
	}
	a.chatList.FollowEnd = true
	ui.List(c, &a.chatList, len(entries)+1, func(i int) {
		if i == len(entries) {
			a.liveStatus(c, p, v)
			return
		}
		a.entry(c, p, entries, i)
	}).Grow(1).MinHeight(0).Padding(16, 0, 24, 0).Children(func() {
		ui.Text(c, locales.SendToStart()).FontSize(sizeSub).TextColor(p.Secondary).Margin(ui.Auto)
	})
	a.bottom(c, p, sess, v)
}

// offlinePill marks a machine that isn't connected.
func offlinePill(c *ui.Context, p *palette) {
	ui.Text(c, locales.Offline()).FontSize(10).FontWeight(500).TextColor(p.Danger).Padding(1, 5).Radius(5).Background(p.Danger.Alpha(0.14)).Shrink(0)
}

// column centers the conversation column: 768 DIPs of content between
// 18-DIP gutters.
func column(c *ui.Context, key any, fn func()) ui.Element {
	return ui.Row(c.Key(key)).FillWidth().Justify(ui.Center).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).MaxWidth(columnWidth+2*columnGutter).Padding(0, columnGutter).Children(fn)
	})
}

func (a *App) entry(c *ui.Context, p *palette, entries []session.Entry, i int) {
	e := entries[i]
	top := float32(0)
	if i > 0 {
		switch {
		case e.Role == "user":
			top = 56 // answer to the next message
		case entries[i-1].Role == "user":
			top = 36 // message to its response
		default:
			top = 16
		}
	}
	// Queued messages follow each other closely, the first able to
	// interrupt the running turn.
	firstQueued := e.Queued && (i == 0 || !entries[i-1].Queued)
	if e.Queued && i > 0 && entries[i-1].Queued {
		top = 12
	}
	column(c, e.ID, func() {
		if e.Role == "user" {
			a.userMessage(c, p, e, firstQueued)
		} else {
			a.assistantMessage(c, p, e)
		}
	}).Padding(top, 0, 0, 0)
}

// deliveryLabel is what a user message's meta line says of its delivery,
// empty for an ordinary delivered message.
func deliveryLabel(e session.Entry) string {
	switch {
	case e.Steer && e.SteerMode == "interrupt" && e.Queued:
		return locales.DeliverySendingNext()
	case e.Queued:
		return locales.DeliveryQueued()
	case e.Steer && e.SteerState == "unknown":
		return locales.DeliverySteerUnconfirmed()
	case e.Steer && (e.Status == "pending_apply" || e.SteerState == "confirming"):
		return locales.DeliverySteering()
	case e.Steer:
		return locales.DeliverySteered()
	}
	return ""
}

// userMessage is the user's bubble on the right, its avatar beside its
// first line, the delivery mark and time above it, and its attachments.
func (a *App) userMessage(c *ui.Context, p *palette, e session.Entry, firstQueued bool) {
	var images, files []session.Item
	for _, it := range e.Items {
		switch it.Type {
		case "image":
			images = append(images, it)
		case "image_group":
			images = append(images, it.Images...)
		case "file":
			files = append(files, it)
		}
	}
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Justify(ui.End).Children(func() {
		ui.Column(c).AlignItems(ui.End).Gap(4).Shrink(1).MinWidth(0).MaxWidth(560).Children(func() {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				label := deliveryLabel(e)
				switch {
				case label != "":
					if e.Steer {
						ui.Icon(c, iconSteer).Size(13, 13).TextColor(p.Secondary)
					}
					ui.Text(c, label).FontSize(sizeCaption).TextColor(p.Secondary)
				case e.Status == "pending":
					ui.Icon(c, iconCircle).Size(14, 14).TextColor(p.Secondary)
				default:
					ui.Icon(c, iconCircleCheck).Size(14, 14).TextColor(p.Primary)
				}
				ui.Text(c, conversationTime(e.Timestamp, time.Now())).FontSize(sizeCaption).TextColor(p.Secondary)
				if e.Author != nil {
					ui.Text(c, e.Author.Name).FontSize(sizeCaption).TextColor(p.Secondary)
				}
			})
			if len(images) > 0 {
				ui.Row(c).Wrap().Gap(6).Justify(ui.End).Children(func() {
					size := float32(160)
					if len(images) > 1 {
						size = 112
					}
					for _, img := range images {
						a.sessionImage(c, p, a.selected, img, size)
					}
				})
			}
			for _, f := range files {
				fileChip(c, p, f)
			}
			if text := e.Text(); text != "" {
				// A long message folds to its first lines, as Lody's.
				long := strings.Count(text, "\n") >= 10 || utf8.RuneCountInString(text) > 900
				key := e.ID + "/more"
				open := a.expanded[key]
				bubble := ui.Column(c).Padding(12, 16).Radius(14).Background(p.UserBubble).MinWidth(0).Gap(6)
				if e.Queued {
					bubble.Opacity(0.7)
				}
				bubble.Children(func() {
					body := ui.Text(c, text).Selectable().FontSize(sizeBody).LineHeight(24.0 / 14).TextColor(p.Reading)
					if long && !open {
						body.MaxLines(8)
					}
					if long {
						label := locales.ShowMore()
						if open {
							label = locales.ShowLess()
						}
						ui.Text(c, label).FontSize(sizeFoot).TextColor(p.Secondary).AlignSelf(ui.End).Cursor(ui.CursorPointer).
							Label(label).OnClick(func() { a.expanded[key] = !a.expanded[key] })
					}
				})
			}
			if e.Queued && a.view != nil && a.view.Working {
				a.queuedActions(c, p, e, firstQueued)
			}
		})
		ui.Box(c).Margin(28, 0, 0, 0).Children(func() {
			if e.Author != nil {
				// An agent wrote it: its mark, not a person.
				ui.Row(c).Size(32, 32).Center().Children(func() {
					ui.Icon(c, agentGlyph(e.Author.AgentType)).Size(20, 20).TextColor(p.Secondary)
				})
				return
			}
			if a.account != nil && (e.UserID == "" || e.UserID == a.account.User.ID) {
				a.accountAvatar(c, p, 32)
			} else {
				userAvatar(c, p)
			}
		})
	})
}

// queuedActions give a queued message to the running turn: Steer when its
// agent takes steering, Send now by interrupting it.
func (a *App) queuedActions(c *ui.Context, p *palette, e session.Entry, first bool) {
	sess := a.catalog.Session(a.selected)
	if sess == nil || e.SteerMode == "interrupt" {
		return
	}
	ui.Row(c).Gap(4).Children(func() {
		if a.steerable(sess) {
			ghostButton(c, p, locales.Steer()).Tooltip(locales.QueuedSteerTooltip()).OnClick(func() { a.steerQueued(e.ID, false) })
		}
		if first {
			ghostButton(c, p, locales.QueuedSendNow()).Tooltip(locales.QueuedSendNowTooltip()).OnClick(func() { a.steerQueued(e.ID, true) })
		}
	})
}

// userAvatar is the 32px stand-in Lody shows without a picture.
func userAvatar(c *ui.Context, p *palette) ui.Element {
	return ui.Row(c).Size(32, 32).Shrink(0).Radius(16).Background(p.Text.Alpha(0.1)).Center().Children(func() {
		ui.Icon(c, iconUser).Size(16, 16).TextColor(p.Secondary)
	})
}

// conversationTime formats a message time as Lody does, in the app's
// language: the time today, the weekday within a week, the date before.
func conversationTime(ts string, now time.Time) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ""
	}
	t = t.Local()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	today := time.Date(y2, m2, d2, 0, 0, 0, 0, now.Location())
	day := time.Date(y1, m1, d1, 0, 0, 0, 0, now.Location())
	switch days := int(today.Sub(day).Hours() / 24); {
	case days <= 0:
		return locales.MessageToday(t)
	case days < 7:
		return locales.MessageThisWeek(t)
	}
	return locales.MessageEarlier(t)
}

// formatDuration is Lody's compact duration in the app's language: "12s",
// "1m 05s", "1h 02m 05s"; "12秒", "1分05秒".
func formatDuration(d time.Duration) string {
	s := int(d / time.Second) // whole seconds, as Lody floors them
	pad := func(n int) string { return fmt.Sprintf("%02d", n) }
	switch {
	case s < 60:
		return locales.DurationSeconds(fmt.Sprint(s))
	case s < 3600:
		return locales.DurationMinutes(fmt.Sprint(s/60), pad(s%60))
	}
	return locales.DurationHours(fmt.Sprint(s/3600), pad(s%3600/60), pad(s%60))
}

// syncMarkdown keeps the Markdown themes in step with the palette.
func (a *App) syncMarkdown(p *palette) {
	if a.md.Theme != nil && a.mdDark == p.Dark {
		return
	}
	a.mdDark = p.Dark
	base := ui.LightTheme()
	if p.Dark {
		base = ui.DarkTheme()
	}
	base.Font, base.FontSize = fontUI, sizeBody
	t := markdown.DefaultTheme(base)
	t.FontSize = sizeBody
	t.Font = fontUI
	t.CodeFont = fontMono
	t.LineHeight = 1.55
	t.Text = p.Reading
	t.Strong = p.Strong
	t.Muted = p.Secondary
	t.Link = p.Link
	t.Code = p.Text
	t.CodeBackground = p.InlineCode
	t.CodeBlockBackground = p.CodeBg
	t.CodeBlockBorder = p.CodeBg
	t.CodeBlockRadius = 8
	t.Rule = p.Border
	t.QuoteBar = p.Border
	t.QuoteText = p.Secondary
	a.md.Theme = t

	muted := *t
	muted.FontSize = sizeSub
	muted.Text = p.Secondary
	muted.Strong = p.Secondary
	a.mdMuted.Theme = &muted
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
