package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// Lody's AskUserQuestion card (ask-user-question-card.tsx): what the agent
// asks, one question to a page, in the composer's place while it waits,
// and read only in its turn once answered. A page offers its options, one
// of or any of, an answer of the user's own and a note; a pager walks the
// pages, its dots saying which is shown and which are answered.

// A press dragged across the card pages it, as Lody's touch swipe; MyGo
// tells no finger from a mouse, so any press does. It is a swipe once it
// has gone 14px across, 1.25 times as far across as down (going 14px down
// first lets it go); released 56px across with no more than 44px of
// drift, it pages, and the click its release makes is dropped for 350ms.
const (
	askSwipeStart   = 14
	askSwipePage    = 56
	askSwipeDrift   = 44
	askSwipeAxis    = 1.25
	askSwipeNoClick = 350 * time.Millisecond
)

var (
	iconClock3 = lucide(`<circle cx="12" cy="12" r="10"/><path d="M12 6v6h4.5"/>`)
	// askTick is the tick in a chosen any-of mark: Lucide's check at a
	// stroke of 3, as Lody draws it there.
	askTick = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>`))
)

// askDraft is one question's answer as it is given: the options chosen,
// an answer of the user's own, and the note.
type askDraft struct {
	selected []string
	custom   string
	note     string
}

// askCard is what a question card keeps between frames.
type askCard struct {
	drafts []askDraft
	page   int
	swipe  askSwipe
	// info is the option whose details show, kept as the dialog closes.
	info     session.AskOption
	infoOpen bool
	// sending is the option on its way, while live, and err why the last
	// answer did not go.
	sending, err string
}

// askSwipe is a press dragged across the card: how far it went, whether
// it is a swipe yet, and until when a click is dropped.
type askSwipe struct {
	held, dead, active bool
	dx, dy             float32
	noClickUntil       time.Time
}

// move takes a held press's move, from where it went down when fresh.
func (sw *askSwipe) move(dx, dy float32, fresh bool) {
	if fresh {
		*sw = askSwipe{noClickUntil: sw.noClickUntil}
	}
	sw.dx, sw.dy = sw.dx+dx, sw.dy+dy
	if sw.dead || sw.active {
		return
	}
	ax, ay := abs32(sw.dx), abs32(sw.dy)
	switch {
	case ay > askSwipeStart && ay > ax:
		sw.dead = true
	case ax >= askSwipeStart && ax >= ay*askSwipeAxis:
		sw.active = true
	}
}

// end takes the release: -1 pages on, 1 back, 0 stays.
func (sw *askSwipe) end(now time.Time) int {
	active := sw.active
	sw.active, sw.dead = false, false
	if !active {
		return 0
	}
	sw.noClickUntil = now.Add(askSwipeNoClick)
	ax, ay := abs32(sw.dx), abs32(sw.dy)
	if ax < askSwipePage || ay > askSwipeDrift || ax < ay*askSwipeAxis {
		return 0
	}
	if sw.dx < 0 {
		return -1
	}
	return 1
}

// swallow reports whether a click is the release of a swipe, which
// clicks nothing.
func (sw *askSwipe) swallow(now time.Time) bool {
	if sw.active {
		return true
	}
	if now.Before(sw.noClickUntil) {
		sw.noClickUntil = time.Time{}
		return true
	}
	return false
}

// askLive is a card's live side: where its answers and its cancel go,
// and whether one of them is on its way, or failed.
type askLive struct {
	submit                       func(session.AskAnswers)
	cancel                       func()
	sending                      bool
	pendingSubmit, pendingCancel bool
	err                          string
}

// askResponder writes a request's outcome into its session, as Lody's
// respondToPermission writes it through the session's writer, for the
// agent to read. Tests swap it.
var askResponder = func(ctx context.Context, r *session.Replica, entryID, itemID, requestID string, outcome map[string]any) error {
	if r == nil {
		return errors.New("session: not open")
	}
	return r.Respond(ctx, entryID, itemID, requestID, outcome)
}

// askState is a card's state, made on first use: a live card starts on
// its first unanswered question; one read only shows the answers as they
// last came, from its first page.
func (a *App) askState(key string, qs *session.AskQuestions, answers session.AskAnswers, live bool) *askCard {
	if a.asks == nil {
		a.asks = map[string]*askCard{}
	}
	st := a.asks[key]
	if st == nil || len(st.drafts) != len(qs.Questions) {
		st = &askCard{drafts: askDrafts(qs, answers)}
		if live {
			st.page = askFirstIncomplete(qs, st.drafts)
		}
		a.asks[key] = st
	}
	if !live {
		st.drafts = askDrafts(qs, answers)
	}
	st.page = min(max(0, st.page), len(qs.Questions)-1)
	return st
}

// askQuestionCard asks a request's questions in the composer's place, as
// Lody's QuestionRequest: Submit answers with the request's answer option,
// its outcome carrying the answers in the shape the request asked in; the
// cancel button picks its other option.
func (a *App) askQuestionCard(c *ui.Context, p *palette, e session.Entry, it session.Item) {
	perm := it.Permission
	qs := perm.Questions
	st := a.askState("live/"+perm.RequestID, qs, nil, true)
	answer, cancel := askOptionIDs(perm.Options)
	live := &askLive{
		sending:       st.sending != "",
		pendingSubmit: st.sending != "" && st.sending == answer,
		pendingCancel: st.sending != "" && st.sending == cancel,
		err:           st.err,
		submit: func(answers session.AskAnswers) {
			if answer != "" {
				a.respondAsk(st, e.ID, it, answer, session.AskOutcome(answer, answers, qs.Source))
			}
		},
		cancel: func() {
			if cancel != "" {
				a.respondAsk(st, e.ID, it, cancel, map[string]any{"outcome": "selected", "optionId": cancel})
			}
		},
	}
	// With several waiting, where this one is stands over it, at its end.
	if a.permPager.all > 1 {
		ui.Row(c.Key("ask-strip")).FillWidth().Justify(ui.End).AlignItems(ui.Center).Gap(2).Margin(0, 0, 4, 0).Children(func() { a.permissionPosition(c, p) })
	}
	questionCard(c.Key("ask/"+perm.RequestID), p, qs, st, live)
}

// askAnswered reports whether a step is an answered question, which its
// turn shows read only.
func askAnswered(it session.Item) bool {
	return it.Permission != nil && it.Permission.Questions != nil && it.Permission.Answers != nil
}

// askAnsweredCard is an answered request in its turn, as Lody's
// PermissionRequestBlock shows it: the card, read only, its answers
// chosen and the rest receded.
func (a *App) askAnsweredCard(c *ui.Context, p *palette, it session.Item) {
	perm := it.Permission
	st := a.askState("read/"+perm.RequestID, perm.Questions, perm.Answers, false)
	questionCard(c.Key("asked/"+perm.RequestID), p, perm.Questions, st, nil)
}

// respondAsk sends a card's outcome, one at a time, as Lody's respond: the
// option on its way shows its spinner, and a failure says the answer
// didn't reach the agent.
func (a *App) respondAsk(st *askCard, entryID string, it session.Item, optionID string, outcome map[string]any) {
	if st.sending != "" {
		return
	}
	st.sending, st.err = optionID, ""
	r, itemID, requestID := a.replica, it.ID, it.Permission.RequestID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := askResponder(ctx, r, entryID, itemID, requestID, outcome)
		a.update(func() {
			st.sending = ""
			if err != nil {
				st.err = locales.AskSendFailed()
			}
		})
	}()
}

// askOptionIDs are a request's options that answer and that cancel, as
// Lody's QuestionRequest picks them: "answer", else the first that
// allows, else the first; and another that rejects or denies, else any
// other.
func askOptionIDs(options []session.Option) (answer, cancel string) {
	for _, pick := range []func(session.Option) bool{
		func(o session.Option) bool { return o.ID == "answer" },
		func(o session.Option) bool { return strings.HasPrefix(o.Kind, "allow") },
		func(session.Option) bool { return true },
	} {
		if i := slices.IndexFunc(options, pick); i >= 0 {
			answer = options[i].ID
			break
		}
	}
	for _, pick := range []func(session.Option) bool{
		func(o session.Option) bool {
			return strings.HasPrefix(o.Kind, "deny") || strings.HasPrefix(o.Kind, "reject")
		},
		func(session.Option) bool { return true },
	} {
		if i := slices.IndexFunc(options, func(o session.Option) bool { return o.ID != answer && pick(o) }); i >= 0 {
			cancel = options[i].ID
			break
		}
	}
	return answer, cancel
}

// askDrafts are the drafts answers make: the labels a question offers are
// chosen, and the rest, a line each, are the user's own answer.
func askDrafts(qs *session.AskQuestions, answers session.AskAnswers) []askDraft {
	drafts := make([]askDraft, len(qs.Questions))
	for i, q := range qs.Questions {
		d := &drafts[i]
		if q.Note != nil {
			d.note, _ = answers[q.Note.FieldID].(string)
		}
		var values []string
		switch v := answers[session.AskAnswerKey(qs.Questions, i)].(type) {
		case string:
			values = []string{v}
		case []string:
			values = v
		}
		var custom []string
		for _, v := range values {
			if slices.ContainsFunc(q.Options, func(o session.AskOption) bool { return o.Label == v }) {
				d.selected = append(d.selected, v)
			} else {
				custom = append(custom, v)
			}
		}
		d.custom = strings.Join(custom, "\n")
	}
	return drafts
}

// askValue is a draft's answer: the user's own, trimmed, else the labels
// chosen, all of them for an any-of question; nil when there is none.
func askValue(q session.AskQuestion, d askDraft) any {
	if custom := strings.TrimSpace(d.custom); custom != "" {
		return custom
	}
	var labels []string
	for _, l := range d.selected {
		if l != "" {
			labels = append(labels, l)
		}
	}
	switch {
	case len(labels) == 0:
		return nil
	case q.MultiSelect:
		return labels
	}
	return labels[0]
}

func askComplete(q session.AskQuestion, d askDraft) bool { return askValue(q, d) != nil }

// askAnswersOf are the answers the drafts give, with each note written;
// nil until every question has one.
func askAnswersOf(qs *session.AskQuestions, drafts []askDraft) session.AskAnswers {
	answers := session.AskAnswers{}
	for i, q := range qs.Questions {
		if i >= len(drafts) {
			return nil
		}
		v := askValue(q, drafts[i])
		if v == nil {
			return nil
		}
		answers[session.AskAnswerKey(qs.Questions, i)] = v
		if q.Note != nil && strings.TrimSpace(drafts[i].note) != "" {
			answers[q.Note.FieldID] = drafts[i].note
		}
	}
	return answers
}

// askFirstIncomplete is the first question without an answer, else the
// last.
func askFirstIncomplete(qs *session.AskQuestions, drafts []askDraft) int {
	for i, q := range qs.Questions {
		if i >= len(drafts) || !askComplete(q, drafts[i]) {
			return i
		}
	}
	return max(0, len(qs.Questions)-1)
}

// questionCard is Lody's AskUserQuestionCard, live, or read only without
// live.
func questionCard(c *ui.Context, p *palette, qs *session.AskQuestions, st *askCard, live *askLive) {
	readonly := live == nil
	total := len(qs.Questions)
	cur := st.page
	q := qs.Questions[cur]
	d := &st.drafts[cur]
	now := c.Now()

	// The time left before the agent goes on without an answer, read
	// every 250ms; at none the card stops taking answers.
	secs, counting := 0, !readonly && qs.AutoResolveAt > 0
	if counting {
		secs = max(0, int(math.Ceil((qs.AutoResolveAt-float64(now.UnixMilli()))/1000)))
		if secs > 0 {
			c.After(250 * time.Millisecond)
		}
	}
	expired := counting && secs == 0
	disabled := readonly || live.sending || expired
	first, last := cur == 0, cur == total-1
	complete := askComplete(q, *d)
	canSubmit := askAnswersOf(qs, st.drafts) != nil
	canBack, canOn := !first, !last && (readonly || complete)
	custom := qs.CustomAnswer(cur)
	customOn := strings.TrimSpace(d.custom) != ""

	goTo := func(i int) { st.page = min(max(0, i), total-1) }
	submit := func() {
		if answers := askAnswersOf(qs, st.drafts); live != nil && answers != nil {
			live.submit(answers)
		}
	}
	// One of one moves on to the next question, never submitting: the
	// user reviews every answer and submits them.
	choose := func(label string) {
		if disabled {
			return
		}
		if !q.MultiSelect {
			d.selected = []string{label}
		} else if i := slices.Index(d.selected, label); i >= 0 {
			d.selected = slices.Delete(slices.Clone(d.selected), i, i+1)
		} else {
			d.selected = append(slices.Clone(d.selected), label)
		}
		d.custom = ""
		if !q.MultiSelect && q.Note == nil && live != nil && cur < total-1 {
			goTo(cur + 1)
		}
	}

	// It rests on the conversation by its shadow, with no edge and no
	// bands inside: the question, the choices and the answers are set
	// apart by space alone. It arrives from 8px below as it fades in,
	// over 320ms on ease.standard, as a new block enters the conversation.
	card := ui.Column(c).FillWidth().MinWidth(0).Gap(8).Padding(12).Radius(14)
	askCardLook(card, p)
	if k := card.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: durSlow, Value: 1, Ease: easeStandard}}); k < 1 {
		card.Translate(0, 8*(1-k)).Opacity(k)
	}

	// The swipe follows a press on the card or a choice in it; a field
	// keeps its drags for its caret and selection.
	sw := &st.swipe
	swipes := total > 1 && !(live != nil && disabled)
	wasHeld, held := sw.held, false
	follow := func(el ui.Element) {
		if !swipes {
			return
		}
		if dx, dy, ok := el.Dragged(); ok {
			sw.move(dx, dy, !wasHeld && !held)
			held = true
		}
	}
	follow(card)

	card.Children(func() {
		ui.Row(c.Key("head")).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Start).Children(func() {
			ui.Text(c, q.Question).FontSize(sizeBody).LineHeight(20.0 / 14).FontWeight(600).TextColor(p.Text).Grow(1).Shrink(1).MinWidth(0)
			// What sits beside the question sits on its first line, however
			// many it wraps to.
			ui.Row(c.Key("meta")).Height(20).Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
				if total > 1 {
					ui.Text(c, fmt.Sprintf("%d/%d", cur+1, total)).FontSize(sizeCaption).LineHeight(16.0 / 11).FontWeight(500).
						FontFeatures("tnum").TextColor(p.Tertiary)
				}
				if counting {
					ui.Row(c.Key("countdown")).Gap(4).AlignItems(ui.Center).Children(func() {
						label := locales.AskContinuesIn(secs)
						if expired {
							spinner(c)
							label = locales.AskContinuing()
						} else {
							ui.Icon(c, iconClock3).Size(12, 12).Shrink(0).TextColor(p.Tertiary)
						}
						ui.Text(c, label).FontSize(sizeCaption).LineHeight(16.0 / 11).TextColor(p.Tertiary).SingleLine()
					})
				}
				if readonly {
					askBadge(c, p)
					return
				}
				// A 24px button on the 20px line, overhanging it.
				enabled := !disabled || live.pendingCancel
				x := lodyButton(c, p, "ghost", "ask-cancel", iconX, "", enabled, live.pendingCancel).Size(24, 24).Radius(6).Label(locales.Cancel())
				if x.Clicked() && enabled {
					live.cancel()
				}
			})
		})

		if len(q.Options) > 0 || custom {
			ui.Column(c.Key(fmt.Sprint("choices/", cur))).FillWidth().MinWidth(0).Gap(2).Children(func() {
				for _, o := range q.Options {
					askOption(c, p, st, q, o, slices.Contains(d.selected, o.Label), readonly, disabled || customOn, follow, choose)
				}
				if custom {
					row := ui.Row(c.Key("custom")).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Center).Padding(2, 8)
					follow(row)
					row.Children(func() {
						askMark(c, p, false, customOn, true)
						value := &d.custom
						if readonly && q.IsSecret && d.custom != "" {
							masked := "••••••••"
							value = &masked
						}
						in := askField(ui.TextInputBase(c.Key("custom-in"), value), p, 28, 8, 8, disabled).Placeholder(locales.AskCustomAnswer()).
							Label(locales.AskCustomAnswer()).Grow(1)
						if q.IsSecret {
							in.Password()
						}
						if in.Changed() && strings.TrimSpace(d.custom) != "" {
							d.selected = nil
						}
						// Enter goes on to the next question once this one is
						// answered, and on the last submits them all.
						in.OnTextKey(func(k ui.TextKey) bool {
							if k.Key != ui.KeyEnter || k.Mods&ui.Shift != 0 || disabled {
								return false
							}
							if text := k.Text(); text != d.custom {
								d.custom = text
								if strings.TrimSpace(text) != "" {
									d.selected = nil
								}
							}
							switch {
							case !askComplete(q, *d):
							case last:
								submit()
							default:
								goTo(cur + 1)
							}
							return true
						})
					})
				}
			})
		}

		if n := q.Note; n != nil {
			title := n.Title
			if title == "" {
				title = locales.AskNoteLabel()
			}
			ui.Column(c.Key(fmt.Sprint("note/", cur))).FillWidth().MinWidth(0).Gap(6).Children(func() {
				ui.Row(c).FillWidth().Wrap().AlignItems(ui.Baseline).GapX(8).GapY(2).Children(func() {
					ui.Text(c, title).FontSize(sizeFoot).LineHeight(16.0 / 12).FontWeight(500).TextColor(p.Text)
					if n.Description != "" {
						ui.Text(c, n.Description).FontSize(sizeFoot).LineHeight(16.0 / 12).TextColor(p.Tertiary)
					}
				})
				value := &d.note
				if readonly && n.IsSecret && d.note != "" {
					masked := "••••••••"
					value = &masked
				}
				if n.IsSecret {
					askField(ui.TextInputBase(c.Key("note-in"), value), p, 32, 10, 10, disabled).Label(title).FillWidth().Password()
					return
				}
				askField(ui.TextAreaBase(c.Key("note-area"), value), p, 0, 10, 10, disabled).Label(title).FillWidth().
					Lines(2, 8).MinHeight(72).Padding(8, 10).LineHeight(18.0 / 13)
			})
		}

		if live != nil && live.err != "" {
			askNotice(c, p, iconCircleAlert, p.Danger, live.err).Role(ui.RoleStatus)
		}

		// Submit is a small button bottom left, a discrete action rather
		// than a bar; several questions submit from the pager.
		if live != nil && total == 1 {
			ui.Row(c.Key("submit-row")).Children(func() {
				ok := !disabled && canSubmit
				if lodyButton(c, p, "secondary", "ask-submit", iconArrowUp, locales.AskSubmit(), ok, live.pendingSubmit).Clicked() && ok {
					submit()
				}
			})
		}

		if total > 1 {
			ui.Row(c.Key("pager")).FillWidth().Gap(8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				if askNavButton(c, p, "ghost", "ask-prev", iconChevronLeft, nil, locales.AskPrev(), canBack).Clicked() && canBack {
					goTo(cur - 1)
				}
				askDots(c, p, qs, st, cur, disabled && !readonly, goTo)
				switch {
				case readonly || !last:
					if askNavButton(c, p, "ghost", "ask-next", nil, iconChevronRight, locales.AskNext(), canOn).Clicked() && canOn {
						goTo(cur + 1)
					}
				default:
					ok := !disabled && canSubmit
					if lodyButton(c, p, "secondary", "ask-submit", iconArrowUp, locales.AskSubmit(), ok, live.pendingSubmit).Clicked() && ok {
						submit()
					}
				}
			})
		}
	})

	sw.held = held
	if !held && wasHeld {
		switch sw.end(now) {
		case -1:
			if canOn {
				goTo(cur + 1)
			}
		case 1:
			if canBack {
				goTo(cur - 1)
			}
		}
	}
	askInfoDialog(c, p, st)
}

// askOption is one of a question's options: a row that is itself the
// pressable, its mark beside its label, and an (i) at the label's end
// when it has details. A replayed answer reads at full strength and the
// options it did not take recede; a live row that cannot be pressed dims
// whole.
func askOption(c *ui.Context, p *palette, st *askCard, q session.AskQuestion, o session.AskOption, sel, readonly, off bool, follow func(ui.Element), choose func(string)) {
	row := ui.Row(c.Key("opt/"+o.Label)).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Start).Padding(4, 8).Radius(8).
		Role(ui.RoleToggleButton).Checked(sel).Label(o.Label).Transition(colorFade)
	if !off {
		row.Cursor(ui.CursorPointer).Focusable()
	}
	// Its fill and its dimming move over 120ms on ease.standard.
	switch {
	case sel:
		row.Background(selectedFill(p))
	case !off && row.Hovered():
		row.Background(hoverFill(p))
	}
	dim := off
	if readonly {
		dim = !sel
	}
	row.Opacity(row.AnimateWith("dim", map[bool]float32{true: 0.45, false: 1}[dim], durFast, easeStandard))
	follow(row)
	if row.Clicked() && !st.swipe.swallow(c.Now()) && !off {
		choose(o.Label)
	}
	row.Children(func() {
		askMark(c, p, q.MultiSelect, sel, false)
		ui.Row(c).Grow(1).MinWidth(0).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, o.Label).FontSize(sizeSub).LineHeight(20.0 / 13).TextColor(p.Text).Shrink(1).MinWidth(0)
			if o.Description == "" && o.Preview == "" {
				return
			}
			// Its details are in a dialog, so the row stays one line; the
			// tooltip says the description where there is one.
			info := lodyButton(c, p, "ghost", "info", iconInfo, "", true).Size(24, 24).Radius(6).Label(locales.ShowDetails())
			if o.Description != "" {
				tip(c, p, info, o.Description)
			}
			if info.Clicked() {
				st.info, st.infoOpen = o, true
			}
		})
	})
}

// askDots are the pager's dots, one a question: the one shown a 20px bar
// in the accent, one answered 10px in its tint, one waiting 6px in the
// ink's; each grows and shades over 180ms on ease.standard as the pages
// turn. A dot goes to its question.
func askDots(c *ui.Context, p *palette, qs *session.AskQuestions, st *askCard, cur int, off bool, goTo func(int)) {
	motion := ui.Animation{Duration: durRegular, Ease: easeStandard}
	ui.Row(c.Key("dots")).Gap(6).AlignItems(ui.Center).Role(ui.RoleGroup).Label(locales.AskProgress()).Children(func() {
		for i, q := range qs.Questions {
			w, fill := float32(6), p.Text.Alpha(0.15)
			switch {
			case i == cur:
				w, fill = 20, p.Primary
			case askComplete(q, st.drafts[i]):
				w, fill = 10, p.Primary.Alpha(0.55)
			}
			dot := ui.ButtonBase(c.Key(fmt.Sprint("dot/", i))).Height(6).Shrink(0).Radius(3).Label(locales.AskGoToQuestion(i + 1)).Disabled(off)
			if !off {
				dot.Cursor(ui.CursorPointer)
			}
			dot.Width(dot.AnimateWith("w", w, durRegular, easeStandard)).Background(dot.AnimateColor("fill", fill, motion))
			if dot.Clicked() && !off {
				goTo(i)
			}
		}
	})
}

// askNavButton is a small @lody/ui Button with its icon before its label,
// as Prev's chevron, or after it, as Next's.
func askNavButton(c *ui.Context, p *palette, variant, key string, lead, trail *ui.SVG, label string, enabled bool) ui.Element {
	b, ink := lodyButtonLook(c, p, variant, key, label, enabled)
	b.Children(func() {
		if lead != nil {
			ui.Icon(c, lead).Size(16, 16).TextColor(ink)
		}
		ui.Text(c, label).FontSize(sizeSub).FontWeight(500).LetterSpacing(-0.13).TextColor(ink).SingleLine()
		if trail != nil {
			ui.Icon(c, trail).Size(16, 16).TextColor(ink)
		}
	})
	return b
}

// askMark is what a choice wears, as Lody's ChoiceMark: drawn as a radio
// for one of, a checkbox for any of, on a row that is itself the
// pressable. It is an empty well until it holds the value, then the
// accent under the ink edge, with a dot or a tick; its fill and shadow
// move over 120ms on ease.standard.
func askMark(c *ui.Context, p *palette, multiple, checked, centered bool) {
	m := ui.Box(c.Key("mark")).Size(16, 16).Shrink(0).Center().Radius(8)
	if multiple {
		m.Radius(5)
	}
	if !centered {
		m.Margin(2, 0, 0, 0)
	}
	k := m.AnimateWith("on", b2f(checked), durFast, easeStandard)
	fill := wellGround(p).Mix(p.Primary, k)
	switch {
	case !checked:
		m.Background(fill)
	case p.Dark:
		m.Gradient(fill, fill.Mix(ui.Hex("#000000"), 0.08), 180)
	default:
		m.Gradient(fill.Mix(ui.Hex("#FFFFFF"), 0.1), fill, 180)
	}
	well, edge := 1-k, k
	if p.Dark {
		m.InsetShadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.4*well)).InsetShadow(0, 0, 0, 1, ui.RGBA(255, 255, 255, 0.06*well)).Shadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.04*well)).
			InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.55*edge)).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.5*edge))
	} else {
		m.InsetShadow(0, 1, 1.5, 0, ui.RGBA(28, 28, 28, 0.08*well)).InsetShadow(0, 0, 0, 1, ui.RGBA(28, 28, 28, 0.06*well)).Shadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.8*well)).
			InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.18*edge)).Shadow(0, 1, 1.5, 0, ui.RGBA(28, 28, 28, 0.18*edge))
	}
	m.Children(func() {
		switch {
		case !checked:
		case multiple:
			ui.Icon(c, askTick).Size(12, 12).TextColor(p.Canvas)
		default:
			ui.Box(c).Size(6, 6).Radius(3).Background(p.Canvas)
		}
	})
}

// askField sinks a field into the card, as Lody's well: its fill and inset
// shadow, the accent's 2px ring while it has the focus, and under half
// strength while disabled, the ring and the dimming moving over 120ms on
// ease.standard. height 0 leaves it to its lines.
func askField(in ui.Element, p *palette, height, padX, radius float32, disabled bool) ui.Element {
	in.FontSize(sizeSub).FontWeight(500).LetterSpacing(-0.13).TextColor(p.Text).Radius(radius).MinWidth(0).Disabled(disabled).FocusRing(false)
	if height > 0 {
		in.Height(height).Padding(0, padX)
	}
	in.Background(wellGround(p))
	if ring := in.AnimateWith("ring", b2f(in.Focused()), durFast, easeStandard); ring > 0 {
		in.Shadow(0, 0, 0, 2, p.Primary.Alpha(ring))
	}
	if p.Dark {
		in.InsetShadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.4)).InsetShadow(0, 0, 0, 1, ui.RGBA(255, 255, 255, 0.06)).Shadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.04))
	} else {
		in.InsetShadow(0, 1, 1.5, 0, ui.RGBA(28, 28, 28, 0.08)).InsetShadow(0, 0, 0, 1, ui.RGBA(28, 28, 28, 0.06)).Shadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.8))
	}
	return in.Opacity(in.AnimateWith("dim", map[bool]float32{true: 0.45, false: 1}[disabled], durFast, easeStandard))
}

// askCardLook is Lody's card rung: the elevated surface, resting on the
// conversation by an edge and a contact shadow, with no lift.
func askCardLook(card ui.Element, p *palette) {
	if p.Dark {
		card.Background(ui.Hex("#161616")).InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.04)).
			Shadow(0, 0, 0, 0.5, ui.RGBA(255, 255, 255, 0.07)).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.35))
		return
	}
	card.Background(ui.Hex("#FFFFFF")).Shadow(0, 0, 0, 0.5, ui.RGBA(28, 28, 28, 0.1)).
		Shadow(0, 1, 1.5, 0, ui.RGBA(28, 28, 28, 0.04)).Shadow(0, 2, 6, -2, ui.RGBA(28, 28, 28, 0.04))
}

// hoverFill is Lody's hoverFill, the ground of a row under the pointer.
func hoverFill(p *palette) ui.Color {
	if p.Dark {
		return ui.Hex("#282828")
	}
	return ui.Hex("#F2F2F2")
}

// askBadge is Lody's success Badge saying the request was answered: its
// check and word in the success ink, on the success tint.
func askBadge(c *ui.Context, p *palette) {
	ink := p.Success.Mix(p.Text, 0.5)
	ui.Row(c.Key("answered")).Height(20).Padding(0, 6).Gap(4).Radius(5).AlignItems(ui.Center).Shrink(0).Background(p.Success.Alpha(0.14)).Children(func() {
		ui.Icon(c, iconCheck).Size(12, 12).Shrink(0).TextColor(ink)
		ui.Text(c, locales.AskAnswered()).FontSize(sizeCaption).LineHeight(16.0 / 11).FontWeight(500).FontFeatures("tnum").TextColor(ink).SingleLine()
	})
}

// askNotice is a notice inside the card, as Lody's: the region's tint and
// a mark, with no edge.
func askNotice(c *ui.Context, p *palette, icon *ui.SVG, mark ui.Color, text string) ui.Element {
	n := ui.Row(c.Key("notice")).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Start).Padding(6, 8).Radius(10).Background(p.Text.Alpha(0.03))
	n.Children(func() {
		ui.Icon(c, icon).Size(14, 14).Shrink(0).Margin(1, 0, 0, 0).TextColor(mark)
		ui.Text(c, text).FontSize(sizeFoot).LineHeight(16.0 / 12).TextColor(p.Secondary).Grow(1).MinWidth(0)
	})
	return n
}

// askInfoDialog shows an option's details, as Lody's: its label over its
// description, and its preview set in mono, in a dialog on the modal rung.
func askInfoDialog(c *ui.Context, p *palette, st *askCard) {
	if !st.infoOpen {
		return
	}
	o := st.info
	w, h := c.Size()
	ui.DialogBase(c, &st.infoOpen, func(backdrop, panel ui.Element) {
		sheetIn(backdrop, panel)
		if p.Dark {
			backdrop.Background(ui.RGBA(0, 0, 0, 0.6))
			panel.Background(p.Ground).InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.06)).
				Shadow(0, 0, 0, 0.5, ui.RGBA(255, 255, 255, 0.08)).Shadow(0, 24, 56, -8, ui.RGBA(0, 0, 0, 0.7))
		} else {
			backdrop.Background(ui.RGBA(28, 28, 28, 0.5))
			panel.Background(p.Popover).Shadow(0, 0, 0, 0.5, ui.RGBA(28, 28, 28, 0.08)).
				Shadow(0, 2, 6, -2, ui.RGBA(28, 28, 28, 0.08)).Shadow(0, 24, 56, -12, ui.RGBA(28, 28, 28, 0.28))
		}
		panel.Width(min(512, w-64)).MaxHeight(h - 64).Padding(16).Gap(16).Radius(14).Label(o.Label)
		ui.Column(c.Key("ask-info-head")).FillWidth().Gap(6).Shrink(0).Padding(0, 36, 0, 0).Children(func() {
			ui.Text(c, o.Label).FontSize(16).LineHeight(24.0 / 16).FontWeight(600).TextColor(p.Text).MinWidth(0)
			if o.Description != "" {
				ui.Text(c, o.Description).FontSize(sizeBody).LineHeight(20.0 / 14).TextColor(p.Secondary).MinWidth(0)
			}
		})
		if o.Preview != "" {
			ui.Scroll(c.Key("ask-info-preview")).FillWidth().MaxHeight(h * 0.6).Radius(10).Background(p.Text.Alpha(0.03)).Children(func() {
				ui.Text(c, o.Preview).Font(fontMono).FontSize(sizeFoot).LineHeight(1.625).TextColor(p.Text).NoWrap().Padding(8, 12).Selectable()
			})
		}
		if lodyButton(c, p, "ghost", "ask-info-close", iconX, "", true).Absolute().Top(16).Right(16).Label(locales.Close()).Clicked() {
			st.infoOpen = false
		}
	})
}
