package app

import (
	"fmt"
	"github.com/lbr77/lody-mygo/locales"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/session"
)

const (
	columnWidth  = 768 // Lody's conversation content
	columnGutter = 18
)

func (a *App) mainView(c *ui.Context, p *palette) {
	a.openForked()
	a.tickSends()
	a.membershipSync(c)
	a.bindCommands(c)
	defer func() { a.surf, a.surfNext = a.surfNext, surface{} }()
	a.syncCompact(c)
	// No fade of its own: Lody's boot shell fades out over it (bootOut).
	shell := ui.Row(c.Key("main")).Fill().AlignItems(ui.Stretch).Background(p.Ground)
	// The sidebar slides in and out as it shows and hides; the card grows
	// into its room. Compact, it shows as a drawer instead (compactDrawer).
	a.sideOut = shell.AnimateTo("sidebar", b2f(!a.sidebarHidden && !a.compactLayout.on), slideSpring)
	shell.Children(func() {
		if out := a.sideOut; out > 0 {
			w := a.sidebarW()
			// Stretched, so the sidebar runs the window's height rather than
			// sitting centered in it.
			ui.Row(c).Width(w * out).Shrink(0).AlignItems(ui.Stretch).ClipX().Children(func() {
				withTipDelay(400*time.Millisecond, func() { a.sidebar(c, p).Translate(-w*(1-out), 0) })
			})
			if out == 1 {
				a.sidebarHandle(c, p)
			}
		}
		// Keyed, as the sidebar before it comes and goes.
		card := ui.Column(c.Key("card")).Grow(1).MinWidth(0).Margin(4, 4, 4, 4*(1-a.sideOut)).Radius(14).Border(0.5, p.CardRim).Background(p.Canvas).Clip()
		if !p.Dark {
			card.Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.03))
		}
		card.Children(func() {
			if mode := a.workspaceMode(); mode != "" {
				a.modeGate(c, p, mode)
				return
			}
			page, key := a.sessionView, "session"
			switch {
			case a.page == "archive":
				page, key = a.archiveView, "archive"
			case a.page == "inbox":
				page, key = a.inboxPage, "inbox"
			case a.page == "schedules":
				page, key = a.schedulesPage, "schedules"
			case a.selected == "":
				page, key = a.homeView, "home"
			}
			// Pages and sessions cut over, as Lody's routes do.
			delay := tipDelay
			if key == "archive" {
				delay = 300 * time.Millisecond
			}
			ui.Column(c.Key(key)).Fill().Children(func() { withTipDelay(delay, func() { page(c, p) }) })
		})
	})
	withTipDelay(400*time.Millisecond, func() { a.compactDrawer(c, p) })
	withTipDelay(250*time.Millisecond, func() { a.settingsDialog(c, p) })
	a.projectEditorHost(c, p)
	a.bugReportDialog(c, p)
	a.createWorkspaceDialog(c, p)
	a.joinWorkspaceDialog(c, p)
	a.pairingDialog(c, p)
	a.shareDialogView(c, p)
	a.deviceAuthDialog(c, p)
	a.messageShareDialog(c, p)
	a.shareImageDialog(c, p)
	a.filePreviewDialog(c, p)
	a.scheduleSendDialog(c, p)
	a.stuckBanner(c, p)
	a.paletteView(c, p)
	if !a.settingsOpen {
		// Settings shows its own; outside it, what the conversation asks.
		a.confirmDialog(c, p)
	}
	// Over everything but the toasts, which say what its menu did.
	a.imageViewerView(c)
	a.toastViewport(c, p)
}

// tabBar is the 44px bar over the conversation: the session's tab, New
// chat, and the session's actions. Its empty part drags the window.
func (a *App) tabBar(c *ui.Context, p *palette, sess *catalog.Session) {
	left := a.barInset(3)
	ui.Row(c).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Padding(0, 8, 0, left).Gap(4).DragWindow().Children(func() {
		if a.sidebarHidden {
			iconButton(c, p, iconSidebar, locales.SidebarToggle(), p.Hover).OnClick(func() { a.sidebarHidden = false })
		}
		if sess != nil {
			a.tabStrip(c, p, sess)
		}
		ui.Spacer(c)
		if sess != nil {
			s := *sess
			if s.Archived {
				archivedBadge(c, p)
			}
			a.accessControl(c, p, sess)
			a.headerMenu(c, p, s)
			if a.localMachine() != "" {
				iconButton(c, p, iconSquareTerminal, locales.TerminalToggle(), p.Hover).OnClick(a.toggleTerminal)
			}
			if !a.panelOpen {
				iconButton(c, p, iconPanelRight, locales.PanelToggle(), p.Hover).OnClick(func() { a.panelOpen = true })
			}
		}
	})
}

// sessionView is the conversation, with the inspector beside it when open.
func (a *App) sessionView(c *ui.Context, p *palette) {
	if a.sessionMissing() {
		a.sessionNotFound(c, p)
		return
	}
	// The panel slides in from the right as its room opens, over 220ms on
	// the iOS sheet curve, as Lody's right panel does, and out the same way.
	host := ui.Column(c.Key("session-split")).Fill()
	k := host.AnimateTo("panel", b2f(a.panelOpen), slideSpring)
	host.Children(func() { a.sessionSplit(c, p, k) })
}

func (a *App) sessionMain(c *ui.Context, p *palette) {
	sess := a.catalog.Session(a.selected)
	a.tabBar(c, p, sess)
	if root := a.tabRoot(sess); root != nil && a.activeDraft != "" {
		if d := a.draftFor(root.ID, a.activeDraft); d != nil {
			a.draftView(c, p, root, d)
			return
		}
		a.activeDraft = ""
	}
	a.goalBanner(c, p)
	// The terminal dock grows into its height over 200ms on Tailwind's
	// ease-out, its content at full height and clipped, as Lody's; closing,
	// its content goes at once and its room shrinks away the same way.
	host := ui.Column(c.Key("term-split")).Grow(1).MinHeight(0).FillWidth()
	open := a.term.open && sess != nil && a.localMachine() != ""
	k := host.AnimateWith("dock", b2f(open), 200*time.Millisecond, twEaseOut)
	hostH := host.Bounds().H
	host.Children(func() { a.sessionDock(c, p, sess, k, open, hostH) })
}

func (a *App) sessionDock(c *ui.Context, p *palette, sess *catalog.Session, k float32, open bool, hostH float32) {
	if k <= 0 || sess == nil || a.localMachine() == "" {
		a.conversation(c, p, sess)
		return
	}
	if a.term.percent == 0 {
		a.term.percent = 60
	}
	// Keep each pane at least 140 DIPs high, as Lody's dock.
	if _, h := c.Size(); h > 0 {
		a.term.percent = min(max(a.term.percent, 140/h*100), 100-140/h*100)
	}
	percent := &a.term.percent
	if k < 1 {
		moving := 100 - (100-a.term.percent)*k
		percent = &moving
	}
	ui.SplitVerticalPercent(c, percent, func() {
		ui.Column(c).Fill().MinHeight(0).Children(func() { a.conversation(c, p, sess) })
	}, func() {
		pane := ui.Column(c).Fill().MinHeight(0).ClipY()
		if !open {
			return
		}
		pane.Children(func() {
			inner := ui.Column(c).FillWidth().MinHeight(0)
			if k < 1 && hostH > 0 {
				// The dock's content keeps its full height as its room
				// grows, clipped below.
				inner.Height(hostH * (100 - a.term.percent) / 100).Shrink(0)
			} else {
				inner.Grow(1)
			}
			inner.Children(func() { a.terminalDock(c, p, sess) })
		})
	}).Grow(1).MinHeight(0).FillWidth()
}

// conversation is the session's entries over the composer.
func (a *App) conversation(c *ui.Context, p *palette, sess *catalog.Session) {
	// The viewer its pictures open goes with it.
	a.holdViewer("session/" + a.selected)
	v := a.view
	if listFailed(v) {
		a.messageListFailed(c, p, sess, v)
		return
	}
	if v == nil || (v.Status != "live" && len(v.Entries) == 0) {
		// Lody's conversation skeleton: shape, not text, breathing every
		// 1.6s where the exchange will be.
		ui.Column(c.Key("loading")).Grow(1).MinHeight(0).ClipY().Padding(chatPadTop, 0, 24, 0).Label(locales.LoadingConversation()).Children(func() {
			a.column(c, "skeleton", func() {
				if v != nil && v.Reason != "" {
					ui.Text(c, locales.Reconnecting(v.Reason)).FontSize(sizeSub).TextColor(p.Secondary).Margin(0, 0, 16, 0)
				}
				conversationSkeleton(c, p)
			})
		})
		a.bottom(c, p, sess, nil)
		return
	}
	a.syncMarkdown(p)
	if a.composerFull {
		// The composer has the conversation's room, as lody-ios's full screen.
		a.bottom(c, p, sess, v)
		return
	}
	entries := foldNotices(v.Entries)
	a.outline.prepare(a.selected)
	// While messages are picked to share, their text is not selected: a
	// press on it picks the message, as Lody's rows are select-none.
	a.md.ShareSelection = a.shareImg.picking(a.selected)
	a.indexFind(entries)
	// The messages held on their way follow what is written, as Lody's
	// trailing rows, each keyed by the turn it becomes, which takes its
	// place.
	held := a.heldSends(a.selected, v, false)
	rows := len(entries) + len(held)
	a.chatMotion.rows = rows + 1
	a.revealFind()
	defer func() {
		if a.find.reveal > 0 {
			a.find.reveal--
			a.redraw()
		}
	}()
	a.chatList.Key = func(i int) any {
		switch {
		case i < len(entries):
			return entries[i].ID
		case i < rows:
			return held[i-len(entries)].id
		}
		return i
	}
	// Following the end would carry a search away from its result, and a
	// message just sent away from where it is held (holdSent).
	sendGlide := a.sendGlide(entries)
	sent := &a.chatMotion.sent
	a.chatList.FollowEnd = !a.find.open && !sent.holds(a.selected)
	// A message too tall to hold glides to the end over 360ms; a reply
	// streaming in is followed at once.
	a.chatList.FollowAnimation = nil
	if sendGlide {
		a.chatList.FollowAnimation = &chatGlide
	}
	// Keyed by session, so that each conversation fades in as it opens.
	frame := ui.Box(c.Key("chat-frame/" + a.selected)).FillWidth().Grow(1).MinHeight(0)
	// Scrolling lets a message just sent go, and stops a search result's
	// glide.
	frame.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind == ui.InputScroll {
			sent.letGo()
			a.find.glide = findGlide{}
		}
		return false
	})
	frame.Children(func() {
		a.stepFindGlide(c)
		a.chatMotion.applyNudge(a.selected, c.Now())
		list := ui.List(c.Key("chat/"+a.selected), &a.chatList, rows+1, func(i int) {
			if i == rows {
				sent.noteEnd(a.liveStatus(c, p, v).Bounds())
				return
			}
			if i >= len(entries) {
				s := held[i-len(entries)]
				row := a.column(c, s.id, func() { a.pendingMessage(c, p, s) })
				if i > 0 {
					row.Padding(56, 0, 0, 0) // as a written message's
				}
				b := row.Bounds()
				a.outline.tops[i] = b.Y
				sent.noteRow(s.id, b, i == rows-1)
				return
			}
			// Where each row was, for the outline's reader position and
			// the message just sent.
			b := a.entry(c, p, entries, i).Bounds()
			a.outline.tops[i] = b.Y
			sent.noteRow(entries[i].ID, b, i == rows-1)
		})
		// Tracked, so that a search can glide its result into view, and a
		// message just sent glide to where it is held.
		list.TrackScroll(a.chatMotion.chatScroll(a.selected, list.Bounds().H == 0))
		list.Fill().Padding(chatPadTop, 0, chatPadBottom+sent.roomIn(a.selected), 0).FontFeatures(a.ligatureFeatures()...).Children(func() {
			ui.Text(c, locales.SendToStart()).FontSize(sizeSub).TextColor(p.Secondary).Margin(ui.Auto)
		})
		a.holdSent(list)
		a.chatMotion.viewH = list.Bounds().H
		a.outline.top = list.Bounds().Y
		a.chatEdges(c, p, v, len(entries))
		a.shareMarquee(c, p)
	})
	if !a.shareSelectionBar(c, p, sess) {
		a.bottom(c, p, sess, v)
	}
	a.outlineRail(c, p, entries)
	a.findBar(c, p)
}

// sendGlide holds the message being sent once it shows in the conversation
// (holdSent), and reports whether the conversation glides to its end in
// this frame: for 360ms from when a message too tall to hold let go.
func (a *App) sendGlide(entries []session.Entry) bool {
	cm := &a.chatMotion
	last, id := "", ""
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Role == "user" {
			last, id = a.selected+"/"+entries[i].ID, entries[i].ID
			break
		}
	}
	if last != cm.lastUser {
		// A message the user is sending, not one opened with the chat,
		// nor one held already, which was anchored as it was sent.
		if cm.lastUser != "" && strings.HasPrefix(cm.lastUser, a.selected+"/") && a.sending && a.sendingID == a.selected && cm.sent.id != id {
			cm.sent.sendAnchor(a.selected, id)
		}
		cm.lastUser = last
	}
	return time.Now().Before(cm.glideUntil)
}

// conversationSkeleton is Lody's ConversationSkeleton: three exchanges'
// shapes, each a user bubble at the end over the lines of a reply.
func conversationSkeleton(c *ui.Context, p *palette) {
	turns := []struct {
		bubble float32
		lines  []float32
	}{
		{40, []float32{92, 80, 60}},
		{33, []float32{100, 83, 67, 50}},
		{50, []float32{92, 75}},
	}
	ui.Column(c.Key("conversation-skeleton")).FillWidth().Gap(24).Padding(8, 0).Children(func() {
		for i, t := range turns {
			ui.Column(c.Key(i)).FillWidth().Gap(12).Children(func() {
				skeleton(c, p, fmt.Sprintf("bubble-%d", i), 36).WidthPercent(t.bubble).Radius(10).AlignSelf(ui.End)
				ui.Column(c).FillWidth().Gap(8).Children(func() {
					for j, w := range t.lines {
						skeleton(c, p, fmt.Sprintf("line-%d-%d", i, j), 14).WidthPercent(w).Radius(5)
					}
				})
			})
		}
	})
}

// chatEdges is what Lody lays over the conversation's edges: a fade into
// the canvas at the top once it is scrolled from there and at the bottom
// while more lies below, and a round button back to the latest message,
// which spins while the agent works and shows its arrow under the pointer.
func (a *App) chatEdges(c *ui.Context, p *palette, v *session.View, n int) {
	if n == 0 {
		return
	}
	first, _ := a.chatList.Visible()
	atEnd := a.chatList.AtEnd()
	if first > 0 {
		ui.Box(c.Key("fade-top")).Absolute().Left(0).Right(0).Top(0).Height(48).PassThrough().Draw(func(pt *ui.Painter, r ui.Rect) {
			pt.FillGradient(r, ui.LinearGradient{From: p.Canvas, To: p.Canvas.Alpha(0), Angle: 180}, 0)
		})
	}
	if atEnd {
		return
	}
	ui.Box(c.Key("fade-bottom")).Absolute().Left(0).Right(0).Bottom(0).Height(40).PassThrough().Draw(func(pt *ui.Painter, r ui.Rect) {
		pt.FillGradient(r, ui.LinearGradient{From: p.Canvas.Alpha(0), To: p.Canvas, Angle: 180}, 0)
	})
	ui.Box(c.Key("to-latest")).Absolute().Left(0).Right(0).Bottom(16).PassThrough().Children(func() {
		a.column(c, "to-latest-col", func() {
			ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
				b := ui.ButtonBase(c.Key("scroll-latest")).Size(32, 32).Radius(16).Center().Border(0.5, p.Border).Cursor(ui.CursorPointer).
					Label(locales.ScrollToLatest()).Transition(colorFade).OnClick(func() {
					a.chatMotion.sent.letGo()
					a.chatMotion.sent.room = 0
					a.chatList.ScrollToEnd()
				})
				if p.Dark {
					b.Background(p.Hover)
				} else {
					b.Background(ui.Hex("#FFFFFF")).Shadow(0, 0.5, 1, 1, ui.RGBA(0, 0, 0, 0.04))
				}
				pressable(b, 0.96)
				working := v != nil && v.Working
				b.Children(func() {
					if working && !b.Hovered() {
						loader(c, p)
						return
					}
					ui.Icon(c, iconArrowDown).Size(16, 16).TextColor(p.Text)
				})
			})
		})
	})
}

// offlinePill marks a machine that isn't connected.
func offlinePill(c *ui.Context, p *palette) {
	ui.Text(c, locales.Offline()).FontSize(10).FontWeight(500).TextColor(p.Danger).Padding(1, 5).Radius(5).Background(p.Danger.Alpha(0.14)).Shrink(0)
}

// column centers the conversation column: 768 DIPs of content between
// 18-DIP gutters, or the window's width in full-width mode.
func (a *App) column(c *ui.Context, key any, fn func()) ui.Element {
	return ui.Row(c.Key(key)).FillWidth().Justify(ui.Center).Children(func() {
		col := ui.Column(c).Grow(1).MinWidth(0).Padding(0, columnGutter)
		if !a.wideMode {
			col.MaxWidth(columnWidth + 2*columnGutter)
		}
		col.Children(fn)
	})
}

func (a *App) entry(c *ui.Context, p *palette, entries []session.Entry, i int) ui.Element {
	e := entries[i]
	row := func() ui.Element {
		return a.column(c, e.ID, func() {
			if e.Role == "user" {
				a.userMessage(c, p, e)
			} else {
				a.assistantMessage(c, p, e)
			}
		})
	}
	if a.shareImg.picking(a.selected) {
		// Messages are being picked to share as an image.
		return a.shareRow(c, p, entries, i, row)
	}
	return row().Padding(entryTop(entries, i), 0, 0, 0)
}

// entryTop is the room above entry i.
func entryTop(entries []session.Entry, i int) float32 {
	if i == 0 {
		return 0
	}
	switch {
	case entries[i].Role == "user":
		return 56 // answer to the next message
	case entries[i-1].Role == "user":
		return 36 // message to its response
	}
	return 16
}

// deliveryLabel is what a user message's meta line says of its delivery,
// empty for an ordinary delivered message. Queued messages wait in the
// message queue, not here.
func deliveryLabel(e session.Entry) string {
	switch {
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
func (a *App) userMessage(c *ui.Context, p *palette, e session.Entry) {
	var images, files []session.Item
	var imageKeys []string // each picture's in the conversation's gallery
	for i, it := range e.Items {
		switch it.Type {
		case "image":
			images = append(images, it)
			imageKeys = append(imageKeys, galleryKey(e.ID, i, 0, it.ImageID))
		case "image_group":
			images = append(images, it.Images...)
			for j, img := range it.Images {
				imageKeys = append(imageKeys, galleryKey(e.ID, i, j, img.ImageID))
			}
		case "file":
			files = append(files, it)
		}
	}
	editable := a.editableEntry()
	canEdit := editable != nil && editable.ID == e.ID
	editing := canEdit && a.msgEdit.id == e.ID
	outer := ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Justify(ui.End)
	// Lody's Loader2 turns beside the bubble while the message is still on
	// its way, once that has taken 500ms, where the row is 520 wide.
	sending := !editing && a.sendingShown(e) && outer.Bounds().W >= 520
	outer.Children(func() {
		col := ui.Column(c).AlignItems(ui.End).Gap(4).Shrink(1).MinWidth(0).MaxWidth(560)
		hovered := col.Hovered()
		col.Children(func() {
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
			// Rewritten, its attachments are the editor's to keep or leave.
			if len(images) > 0 && !editing {
				ui.Row(c).Wrap().Gap(6).Justify(ui.End).Children(func() {
					size := float32(160)
					if len(images) > 1 {
						size = 112
					}
					sessionID := a.selected
					for i, img := range images {
						// It opens the conversation's viewer at it, among the
						// pictures of its turn.
						key := imageKeys[i]
						a.sessionImage(c, p, sessionID, img, size, func() { a.openSessionGallery(sessionID, key) })
					}
				})
			}
			if len(files) > 0 && !editing {
				a.fileCards(c, p, a.selected, files, true)
			}
			if editing {
				a.messageEditor(c, p)
			} else if text := e.Text(); text != "" {
				// A long message folds to its first lines, as Lody's.
				long := strings.Count(text, "\n") >= 10 || utf8.RuneCountInString(text) > 900
				key := e.ID + "/more"
				open := a.expanded[key]
				bubble := ui.Column(c).Padding(12, 16).Radius(14).Background(p.UserBubble).MinWidth(0).Gap(6)
				a.findRing(p, e.ID+"/user", bubble)
				bubble.Children(func() {
					if sending {
						sendingSpinner(c, p)
					}
					body := a.userBody(c, p, e, text)
					if !a.shareImg.picking(a.selected) {
						body.Selectable()
					}
					body.FontSize(a.convFontSize()).LineHeight(24.0 / 14).TextColor(p.Reading)
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
			if canEdit && !editing {
				entry := e
				withTipDelay(500*time.Millisecond, func() {
					b := iconButton(c, p, iconPencil, locales.EditMessage(), p.Hover).Size(24, 24).
						Transition(colorFade).OnClick(func() { a.startEdit(entry) })
					// The message's actions fade in with the pointer over 150ms,
					// as Tailwind's transition-opacity.
					b.Opacity(b.AnimateWith("shown", b2f(hovered), 150*time.Millisecond, twEase))
				})
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

// sendingShown reports whether e is a message on its way: written
// locally, its write not through yet. That is a held message once its
// turn shows, or the conversation's last from the user while a message
// sent at once is not done.
func (a *App) sendingShown(e session.Entry) bool {
	if s := a.pending.find(e.ID); s != nil && s.sessionID == a.selected {
		return true
	}
	if !a.sending || a.sendingID != a.selected || a.view == nil || e.Status != "pending" {
		return false
	}
	for i := len(a.view.Entries) - 1; i >= 0; i-- {
		if u := a.view.Entries[i]; u.Role == "user" {
			return u.ID == e.ID
		}
	}
	return false
}

// sendingSpinner is the loader left of a message's bubble, level with its
// last line: it shows only once the send has taken 500ms, as Lody's
// useIsMessageSendingVisible, so a quick send shows none.
func sendingSpinner(c *ui.Context, p *palette) {
	box := ui.Box(c.Key("sending")).Absolute().Left(-22).Bottom(13).Size(16, 16)
	late := box.Keyframes("late", []ui.Keyframe{{Value: 0}, {At: 500 * time.Millisecond, Value: 0}, {At: 501 * time.Millisecond, Value: 1}})
	box.Opacity(late).Children(func() { loader(c, p) })
}

// userAvatar is the 32px stand-in Lody shows without a picture.
func userAvatar(c *ui.Context, p *palette) ui.Element {
	return ui.Row(c).Size(32, 32).Shrink(0).Radius(16).Background(p.Text.Alpha(0.1)).Center().Children(func() {
		ui.Icon(c, iconUser).Size(16, 16).TextColor(p.Secondary)
	})
}

func conversationTimeOf(ts string) string { return conversationTime(ts, time.Now()) }

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
	// The theme follows the palette and the text size chosen.
	if a.md.Theme != nil && a.mdDark == p.Dark && a.mdSize == a.convFontSize() && a.mdMath == a.inlineMath {
		return
	}
	a.mdDark, a.mdSize, a.mdMath = p.Dark, a.convFontSize(), a.inlineMath
	base := ui.LightTheme()
	if p.Dark {
		base = ui.DarkTheme()
	}
	base.Font, base.FontSize = fontUI, sizeBody
	t := markdown.DefaultTheme(base)
	t.FontSize = a.convFontSize()
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
	t.Mark = findMark(p, false)
	a.md.Theme = t
	// A reply streams in as Lody's Streamdown reveals it: a character at a
	// time, nothing waiting longer than 160ms, each fading in.
	a.md.Reveal = &streamReveal
	// Images a reply links load as a browser would; one that fails shows
	// its description.
	a.md.LoadImage = loadMarkdownImage

	muted := *t
	muted.FontSize = sizeSub
	muted.Text = p.Secondary
	muted.Strong = p.Secondary
	a.mdMuted.Theme = &muted
	// Extended code languages: plain until the setting is on.
	a.registerExtendedCode(&a.md)
	a.registerExtendedCode(&a.mdMuted)
	// Mermaid diagrams, drawn once their block closes.
	a.registerMermaid(&a.md)
	a.registerMermaid(&a.mdMuted)
	// Render inline math: TeX between $…$ and \(…\), and $$…$$ blocks.
	a.md.Extensions, a.md.InlineMath, a.md.Math = 0, nil, nil
	a.mdMuted.Extensions, a.mdMuted.InlineMath, a.mdMuted.Math = 0, nil, nil
	if a.inlineMath {
		ext := parser.Default | parser.ExtDollarMath | parser.ExtMath
		a.md.Extensions, a.mdMuted.Extensions = ext, ext
		a.mathOptions(&a.md, func() ui.Color { return a.md.Theme.Text })
		a.mathOptions(&a.mdMuted, func() ui.Color { return a.mdMuted.Theme.Text })
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// userBody is a user message's text, as Lody's transcript shows it: plain,
// its mentions as chips in the accent with what the user picked in place
// of what the agent read, a session's opening it; marked instead while a
// search matches in it.
func (a *App) userBody(c *ui.Context, p *palette, e session.Entry, text string) ui.Element {
	if spans, ok := a.findSpans(p, e.ID+"/user", text); ok {
		return ui.RichText(c, spans...)
	}
	chips := false
	items := append([]session.Item(nil), e.Items...)
	for i := range items {
		if items[i].Type == "text" && len(items[i].Spans) == 0 {
			// A session link sent without its span still reads as one.
			items[i].Spans = sessionLinkSpans(items[i].Text)
		}
		chips = chips || items[i].Type == "text" && len(items[i].Spans) > 0
	}
	if !chips {
		return ui.Text(c, text)
	}
	accent := p.Primary.Mix(p.Text, 0.18)
	return ui.RichText(c).Children(func() {
		first := true
		for _, it := range items {
			if it.Type != "text" || it.Text == "" {
				continue
			}
			if !first {
				ui.Text(c, "\n\n")
			}
			first = false
			last := 0
			for i, s := range it.Spans {
				if s.Start > last {
					ui.Text(c, it.Text[last:s.Start])
				}
				label := s.Label
				if s.Mark != "" {
					label = s.Mark + " " + label
				}
				chip := ui.Text(c.Key(fmt.Sprintf("chip/%s/%d", it.ID, i)), label).TextColor(accent)
				if target := s.Target; s.Kind == "session" && a.catalog.Session(target) != nil {
					chip.Cursor(ui.CursorPointer).OnClick(func() { a.open(target) })
				}
				last = s.End
			}
			if last < len(it.Text) {
				ui.Text(c, it.Text[last:])
			}
		}
	})
}

// sessionLinkRe is a session link as Lody writes a session mention:
// [@Title](lody://session/<id>?workspace=…).
var sessionLinkRe = regexp.MustCompile(`\[(@(?:\\.|[^\]\\])+)\]\(lody://session/([A-Za-z0-9_-]+)(?:\?[^)\s]*)?\)`)

func sessionLinkSpans(text string) []session.TextSpan {
	var out []session.TextSpan
	for _, m := range sessionLinkRe.FindAllStringSubmatchIndex(text, -1) {
		label := strings.NewReplacer(`\\`, `\`, `\[`, "[", `\]`, "]").Replace(text[m[2]:m[3]])
		out = append(out, session.TextSpan{Start: m[0], End: m[1], Kind: "session", Label: label, Target: text[m[4]:m[5]]})
	}
	return out
}
