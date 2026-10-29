package app

import (
	"strings"
	"time"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// Quick replies, as lody-ios offers them: buttons over the composer that
// send a message at once, shown while the agent is idle and the draft
// empty, kept on this device for every conversation.

type quickReply struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Message string `json:"message"`
}

// defaultQuickReplies are lody-ios's: Continue, Review Changes, and
// Commit & Push, in the app's language.
func defaultQuickReplies() []quickReply {
	return []quickReply{
		{"continue", locales.QuickContinueLabel(), locales.QuickContinueMessage()},
		{"review", locales.QuickReviewLabel(), locales.QuickReviewMessage()},
		{"commit-push", locales.QuickCommitLabel(), locales.QuickCommitMessage()},
	}
}

// quickReplyList is the user's quick replies, the defaults until edited.
func (a *App) quickReplyList() []quickReply {
	if a.quickReplies == nil {
		return defaultQuickReplies()
	}
	return a.quickReplies
}

// validQuickReply is lody-ios's bar: a name of at most 40, a message of at
// most 32000, neither blank.
func validQuickReply(q quickReply) bool {
	label, message := strings.TrimSpace(q.Label), strings.TrimSpace(q.Message)
	return label != "" && message != "" && len(utf16.Encode([]rune(q.Label))) <= 40 && len(utf16.Encode([]rune(q.Message))) <= 32000
}

func (a *App) setQuickReplies(list []quickReply) {
	a.quickReplies = list
	a.savePrefs()
}

// showQuickReplies is whether the buttons show: a live conversation with
// something said in it, the agent idle, nothing waiting to go, and the
// composer empty. lody-ios hides them while the keyboard is up; a desktop
// composer holds the focus at rest, so here the first character typed
// hides them.
func (a *App) showQuickReplies(v *session.View) bool {
	if v == nil || v.Status != "live" || v.Working || a.sending || a.sendError != "" || len(a.attachments) > 0 || strings.TrimSpace(a.draft) != "" {
		return false
	}
	talked := false
	for _, e := range v.Entries {
		if e.Queued {
			return false
		}
		talked = talked || e.Role == "user" || e.Role == "assistant"
	}
	return talked && len(a.quickReplyList()) > 0
}

// quickReplyRow is the context chips and the quick replies, a hairline
// between them, fading in and out over 150ms.
func (a *App) quickReplyRow(c *ui.Context, p *palette, sess *catalog.Session, v *session.View) {
	chips := a.hasContextChips(v)
	quick := a.showQuickReplies(v)
	if !chips && !quick {
		return
	}
	ui.Row(c.Key("quick-replies")).FillWidth().Wrap().Gap(8).Padding(0, 2, 2, 2).AlignItems(ui.Center).
		Transition(ui.ElementTransition{Duration: 150 * time.Millisecond, Ease: ui.EaseOut, Enter: &ui.Motion{}, Exit: &ui.Motion{}}).
		Children(func() {
			if chips {
				a.contextChips(c, p, sess, v)
			}
			if chips && quick {
				ui.Box(c).Size(1, 16).Background(p.Border).Shrink(0)
			}
			if !quick {
				return
			}
			for _, q := range a.quickReplyList() {
				message := q.Message
				b := ui.ButtonBase(c.Key("quick/"+q.ID)).Height(32).Padding(0, 12).Radius(16).Border(1, p.Border).Background(p.Composer).
					Cursor(ui.CursorPointer).Label(q.Label).Tooltip(locales.QuickSendHint()).Transition(colorFade)
				pressable(b, 0.96)
				if b.Hovered() {
					b.Background(p.Hover)
				}
				b.OnClick(func() {
					a.draft = message
					a.send()
				})
				b.Children(func() { ui.Text(c, q.Label).FontSize(sizeSub).TextColor(p.Text).SingleLine().MaxWidth(240) })
			}
		})
}

// hasContextChips is whether the session has a chip to show.
func (a *App) hasContextChips(v *session.View) bool {
	if v == nil {
		return false
	}
	_, _, preview := previewResource(v.Preview)
	tasks, _ := runningSubtasks(v)
	return preview || tasks > 0
}

// quickReplyForm edits one quick reply, or a new one.
type quickReplyForm struct {
	open    bool
	id      string // "" for a new one
	label   string
	message string
	err     string
}

func (a *App) editQuickReply(q *quickReply) {
	f := quickReplyForm{open: true}
	if q != nil {
		f.id, f.label, f.message = q.ID, q.Label, q.Message
	}
	a.quickForm = f
}

func (a *App) saveQuickReply() {
	f := &a.quickForm
	q := quickReply{ID: f.id, Label: strings.TrimSpace(f.label), Message: f.message}
	if !validQuickReply(q) {
		f.err = locales.QuickSaveFailed()
		return
	}
	list := append([]quickReply(nil), a.quickReplyList()...)
	if q.ID == "" {
		q.ID = uuid.NewString()
		list = append(list, q)
	} else {
		for i := range list {
			if list[i].ID == q.ID {
				list[i] = q
			}
		}
	}
	a.setQuickReplies(list)
	f.open = false
}

// quickRepliesSection is Preferences' Quick Replies: each reply with its
// message, to edit, move or delete, and adding one.
func (a *App) quickRepliesSection(c *ui.Context, p *palette) {
	prefSection(c, p, locales.QuickTitle(), false, func() {
		ui.Text(c, locales.QuickHint()).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Secondary)
		list := a.quickReplyList()
		if len(list) == 0 {
			emptyCard(c, p, locales.QuickEmpty())
		} else {
			card(c, p, func() {
				for i, q := range list {
					index, reply := i, q
					ui.Row(c.Key(q.ID)).FillWidth().MinHeight(44).Padding(6, 8, 6, 14).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
							ui.Text(c, reply.Label).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine()
							ui.Text(c, reply.Message).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
						})
						move := func(to int) {
							next := append([]quickReply(nil), list...)
							next[index], next[to] = next[to], next[index]
							a.setQuickReplies(next)
						}
						up := iconButton(c, p, iconArrowUp, locales.QuickMoveUp(), p.Hover).Size(26, 26).Disabled(index == 0)
						up.OnClick(func() { move(index - 1) })
						down := iconButton(c, p, iconArrowDown, locales.QuickMoveDown(), p.Hover).Size(26, 26).Disabled(index == len(list)-1)
						down.OnClick(func() { move(index + 1) })
						iconButton(c, p, iconPencil, locales.QuickEdit(), p.Hover).Size(26, 26).OnClick(func() { a.editQuickReply(&reply) })
						iconButton(c, p, iconTrash, locales.ActionDelete(), p.Hover).Size(26, 26).OnClick(func() {
							a.confirm(locales.QuickDeleteTitle(), locales.QuickDeleteHint(), locales.ActionDelete(), func() {
								var next []quickReply
								for _, r := range a.quickReplyList() {
									if r.ID != reply.ID {
										next = append(next, r)
									}
								}
								if next == nil {
									next = []quickReply{}
								}
								a.setQuickReplies(next)
							})
						})
					})
				}
			})
		}
		ui.Row(c).Gap(8).Children(func() {
			outlineButton(c, p, iconPlus, locales.QuickAdd()).OnClick(func() { a.editQuickReply(nil) })
			if a.quickReplies != nil {
				ghostButton(c, p, locales.QuickReset()).OnClick(func() { a.setQuickReplies(nil) })
			}
		})
	})
	a.quickReplyDialog(c, p)
}

func (a *App) quickReplyDialog(c *ui.Context, p *palette) {
	f := &a.quickForm
	if !f.open {
		return
	}
	title := locales.QuickAdd()
	if f.id != "" {
		title = locales.QuickEdit()
	}
	sheet(c, p, &f.open, 520, func() {
		sheetHeader(c, p, title, locales.QuickSendHintLong(), func() { f.open = false })
		ui.Column(c).FillWidth().Padding(8, 20).Gap(10).Children(func() {
			field(c, p, locales.QuickLabel(), "", func() {
				input(c, p, &f.label, locales.QuickLabel(), locales.QuickLabel()).AutoFocus()
			})
			field(c, p, locales.QuickMessage(), "", func() {
				area(c, p, &f.message, locales.QuickMessage(), locales.QuickMessage(), 5)
			})
			if f.err != "" {
				ui.Text(c, f.err).FontSize(sizeFoot).TextColor(p.Danger)
			}
		})
		ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).Children(func() {
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { f.open = false })
			ready := validQuickReply(quickReply{Label: f.label, Message: f.message})
			primaryButton(c, p, locales.Save(), ready).OnClick(a.saveQuickReply)
		})
	})
}
