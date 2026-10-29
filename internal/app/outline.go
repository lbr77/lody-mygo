package app

import (
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// Beside a long conversation Lody keeps an outline rail: a tick per round
// (a user turn and all the agent did in reply), longer the more was said in
// it, and a bright bar on the round the reader is in. The pointer swells
// the rail around it in a bell, the tick under it longest; resting on a
// tick for 200ms shows the round's opening words and the agent's first
// reply beside it, and while one was shown in the last 2.5s the next comes
// at once. Clicking a tick brings its round to the top. The rail shows from
// ten rounds the user drove, beside a pane at least 928 wide, centred on
// it, at most half its height: past that the ticks scroll, keeping the
// reader's round in the middle and fading the edge with more beyond it.

const (
	outlineMinRounds = 10  // user rounds before the rail shows
	railMinPane      = 928 // the narrowest pane the rail fits beside
	tickPitch        = 8   // from one tick to the next, and a tick's hit height
	tickLine         = 2   // a tick's height
	activeTick       = 24  // the reader's round's bar, at the least
	bellPeak         = 38  // the tick under the pointer
	bellBase         = 15  // what the bell decays to
	bellSigma        = 1.8 // its spread, in ticks
	bellRadius       = 4   // past this the bell is left out
	activeOverhang   = 4   // the bar past the widest resting tick
	railTrack        = 42  // a full swell plus the bar's overhang
	railInset        = 8   // from the pane's edge to the ticks
	railPad          = 8   // above and below the ticks
	railFade         = 56  // the fade at an edge with ticks beyond: six ticks and the padding
	outlineTitleMax  = 72  // a round's title, in characters
	outlinePrevMax   = 240 // a round's preview
	outlineSource    = 960 // what of a message the title and preview are taken from
	chatPadTop       = 16  // the conversation's padding above its first row
	railWarmup       = 200 * time.Millisecond
	railWarm         = 2500 * time.Millisecond
)

// tickWidths are a round's resting widths by its weight, lightest first.
var tickWidths = [4]float32{10, 13, 16, 20}

// tickEase is the ticks' 150ms ease-out as they swell and the bar moves.
var tickEase = ui.ElementTransition{Size: true, Position: true, Colors: true, Duration: 150 * time.Millisecond, Ease: ui.EaseOut}

// outlineRound is a round of the conversation.
type outlineRound struct {
	entry   int    // the index of its first entry
	title   string // the user's opening words
	preview string // the agent's first reply's
	agent   bool   // it starts with the agent: a scheduled run or a fork
	weight  int    // how much was said in it, 0 to 3
}

// roundDigest is what a round takes from an entry, kept by entry, as
// settled entries do not change.
type roundDigest struct {
	text           int // the length of the text it was taken from
	prose          int
	title, preview string
}

// outlineRail is the rail's state.
type outlineRail struct {
	session  string
	digests  map[string]roundDigest
	tops     map[int]float32 // where the conversation's rows were, by row
	top      float32         // where the conversation was
	scroll   ui.ScrollState
	active   int       // the round it last centred
	pointer  int       // the tick under the pointer, -1 when none
	waiting  int       // the tick the card waits to show for, -1 when none
	since    time.Time // when the pointer came onto it
	card     int       // the tick whose card shows
	open     bool      // the card shows
	browsing bool      // a card was waited for: the next comes at once
	closed   time.Time // when that card closed
}

// prepare readies the rail for a chat, before its conversation's rows say
// where they are.
func (o *outlineRail) prepare(session string) {
	if o.session != session || o.digests == nil {
		*o = outlineRail{session: session, digests: map[string]roundDigest{}, tops: map[int]float32{}, active: -1, pointer: -1, waiting: -1}
	}
}

// digest is what an entry gives its round, from its first text: cleaned of
// Markdown and cut to the title's and preview's lengths.
func (o *outlineRail) digest(e *session.Entry) roundDigest {
	src, prose := "", 0
	for _, it := range e.Items {
		switch it.Type {
		case "text", "thought", "proposed_plan":
			prose += utf8.RuneCountInString(it.Text)
		}
		if it.Type == "text" && src == "" && strings.TrimSpace(it.Text) != "" {
			src = it.Text
		}
	}
	if len(src) > outlineSource {
		src = src[:outlineSource]
		for !utf8.ValidString(src) {
			src = src[:len(src)-1]
		}
	}
	if d, ok := o.digests[e.ID]; ok && d.text == len(src) && d.prose == prose {
		return d
	}
	summary := strings.Join(strings.Fields(markdownPlain(src)), " ")
	d := roundDigest{text: len(src), prose: prose, title: cutRunes(summary, outlineTitleMax), preview: cutRunes(summary, outlinePrevMax)}
	o.digests[e.ID] = d
	return d
}

// rounds groups the entries into rounds: each starts at a user's turn, and
// what comes before the first is a round of its own.
func (o *outlineRail) rounds(entries []session.Entry) []outlineRound {
	var out []outlineRound
	prose, previewed := 0, false
	weigh := func() {
		if n := len(out); n > 0 {
			out[n-1].weight = roundWeight(prose)
		}
	}
	for i := range entries {
		e := &entries[i]
		d := o.digest(e)
		user := e.Role == "user"
		if user || out == nil {
			weigh()
			r := outlineRound{entry: i, title: d.title, agent: !user}
			if !user {
				r.preview = d.preview
			}
			out = append(out, r)
			prose, previewed = d.prose, !user
			continue
		}
		prose += d.prose
		if !previewed && e.Role == "assistant" && d.preview != "" {
			out[len(out)-1].preview, previewed = d.preview, true
		}
	}
	weigh()
	return out
}

// roundWeight buckets what was said in a round at Lody's quartiles of real
// rounds.
func roundWeight(prose int) int {
	switch {
	case prose >= 3000:
		return 3
	case prose >= 1500:
		return 2
	case prose >= 700:
		return 1
	}
	return 0
}

// userRounds counts the rounds a user's turn began.
func userRounds(rounds []outlineRound) int {
	n := 0
	for _, r := range rounds {
		if !r.agent {
			n++
		}
	}
	return n
}

// tickWidth is a tick's width with the pointer on the tick at hovered, or
// off the rail when it is negative: the bell is blended in, so the tick
// under the pointer is the longest and its neighbours taper off.
func tickWidth(weight, index, hovered int) float32 {
	rest := tickWidths[max(0, min(weight, 3))]
	if hovered < 0 {
		return rest
	}
	d := index - hovered
	if d < -bellRadius || d > bellRadius {
		return rest
	}
	bell := float32(math.Exp(-float64(d*d) / (2 * bellSigma * bellSigma)))
	target := bellBase + (bellPeak-bellBase)*bell
	return rest*(1-bell) + target*bell
}

// tickTop is how far below the top of the ticks a tick's line is.
func tickTop(i int) float32 { return float32(i*tickPitch) + (tickPitch-tickLine)/2 }

// readerRound is the round the reader is in: the last to have started at or
// above the top of the conversation, give or take 4, or the last at its end.
// Above the first round's start it is the first.
func (a *App) readerRound(rounds []outlineRound) int {
	o := &a.outline
	if len(rounds) == 0 {
		return -1
	}
	if a.chatList.AtEnd() {
		return len(rounds) - 1
	}
	first, last := a.chatList.Visible()
	if last < first {
		return -1
	}
	at := 0
	for i, r := range rounds {
		if r.entry > last {
			break
		}
		if r.entry >= first {
			// The list's padding is above its first row: a round brought to
			// the top sits under it.
			if top, ok := o.tops[r.entry]; !ok || top > o.top+chatPadTop+4 {
				break
			}
		}
		at = i
	}
	return at
}

// outlineRail lays the rail over the conversation's pane, at its left.
func (a *App) outlineRail(c *ui.Context, p *palette, entries []session.Entry) {
	o := &a.outline
	o.prepare(a.selected)
	rounds := o.rounds(entries)
	pane := ui.Box(c.Key("outline-pane")).Absolute().Left(0).Top(0).Right(0).Bottom(0).PassThrough()
	b := pane.Bounds()
	if userRounds(rounds) < outlineMinRounds || b.W < railMinPane {
		o.pointer, o.waiting, o.open = -1, -1, false
		return
	}
	active := a.readerRound(rounds)
	n := len(rounds)
	content := float32(n * tickPitch)
	view := min(content+2*railPad, b.H*0.5)
	if active >= 0 && active != o.active && content+2*railPad > view {
		// The reader's round in the middle of the strip, as it moved.
		o.scroll.Y = max(0, min(railPad+tickTop(active)-view/2, content+2*railPad-view))
	}
	o.active = active
	pane.Children(func() {
		ui.Column(c.Key("outline-rail")).Absolute().Left(0).Top(0).Bottom(0).Width(railTrack + railInset).Justify(ui.Center).PassThrough().
			Label(locales.OutlineRail()).Children(func() {
			strip := ui.Scroll(c.Key("outline-strip")).TrackScroll(&o.scroll).Width(railTrack+railInset).Height(view).Padding(railPad, 0, railPad, railInset)
			fade := func(y float32) float32 {
				// The edges with ticks beyond them fade, over six ticks.
				y -= o.scroll.Y
				k := float32(1)
				if o.scroll.Y > 0.5 {
					k = min(k, max(0, y/railFade))
				}
				if o.scroll.Y < o.scroll.MaxY-0.5 {
					k = min(k, max(0, (view-y)/railFade))
				}
				return k
			}
			lifted := strip.Hovered()
			ticks := make([]ui.Element, n)
			bars := make([]ui.Element, n)
			pointer := -1
			strip.Children(func() {
				ui.Box(c).Width(railTrack).Height(content).Shrink(0).Children(func() {
					ui.Column(c).Width(railTrack).Children(func() {
						for i, r := range rounds {
							label := firstNonEmpty(r.title, locales.OutlineUntitled())
							t := ui.ButtonBase(c.Key(i)).Width(railTrack).Height(tickPitch).AlignItems(ui.Center).Label(label).OnClick(func() { a.jumpToRound(rounds, i) })
							t.Children(func() {
								bars[i] = ui.Box(c).Height(tickLine).Radius(1).Transition(tickEase)
							})
							if t.Hovered() {
								pointer = i
							}
							ticks[i] = t
						}
					})
					if active >= 0 {
						w := max(activeTick, tickWidth(rounds[active].weight, active, pointer)+activeOverhang)
						y := tickTop(active)
						ui.Box(c.Key("outline-active")).Absolute().Left(0).Top(y).Width(w).Height(tickLine).Radius(1).PassThrough().
							Background(p.Text.Alpha(0.8 * fade(railPad+y))).Transition(tickEase)
					}
				})
			})
			for i, r := range rounds {
				alpha := float32(0.32)
				switch {
				case i == pointer:
					alpha = 0.8
				case lifted:
					alpha = 0.5
				}
				bars[i].Width(tickWidth(r.weight, i, pointer)).Background(p.Secondary.Alpha(alpha * fade(railPad+tickTop(i))))
			}
			a.railHover(c, pointer)
			if o.open && o.card >= 0 && o.card < n {
				r := rounds[o.card]
				anchor := ticks[o.card]
				ui.Overlay(c, func() { a.roundCard(c, p, anchor, r) })
			}
		})
	})
}

// railHover follows the pointer along the ticks: it opens a card after the
// warmup, or at once while browsing, and closes it as the pointer leaves.
func (a *App) railHover(c *ui.Context, pointer int) {
	o := &a.outline
	now := time.Now()
	if pointer != o.pointer {
		o.pointer = pointer
		o.waiting = -1
		if pointer < 0 {
			if o.open && o.browsing {
				o.closed = now
			}
			o.open = false
			return
		}
		warm := o.browsing && (o.open || now.Sub(o.closed) < railWarm)
		if !warm {
			o.browsing = false
		}
		if warm {
			o.card, o.open = pointer, true
			return
		}
		o.waiting, o.since = pointer, now
	}
	if o.waiting >= 0 && o.waiting == pointer {
		if left := railWarmup - now.Sub(o.since); left > 0 {
			c.After(left)
			return
		}
		// Waiting out the warmup is what earns quick browsing.
		o.card, o.open, o.browsing, o.waiting = pointer, true, true, -1
	}
}

// jumpToRound brings a round to the top of the conversation.
func (a *App) jumpToRound(rounds []outlineRound, i int) {
	o := &a.outline
	o.waiting = -1
	if o.open && o.browsing {
		o.closed = time.Now()
	}
	o.open = false
	if i >= 0 && i < len(rounds) {
		a.chatList.ScrollTo(rounds[i].entry, ui.Start)
	}
}

// roundCard is a round's card, 10 to the right of its tick: the user's
// opening words over the agent's.
func (a *App) roundCard(c *ui.Context, p *palette, anchor ui.Element, r outlineRound) {
	preview := r.preview
	if preview == "" {
		preview = locales.OutlineNoReply()
		if r.agent {
			preview = locales.OutlineAgentStarted()
		}
	}
	ui.Column(c.Key("outline-card")).Width(288).Padding(12).Gap(6).Radius(12).Border(0.5, p.CardRim).Background(p.Popover).
		Shadow(0, 12, 32, -8, ui.RGBA(0, 0, 0, 0.18)).PassThrough().AttachTo(anchor, ui.AnchorEnd, ui.AnchorStart).Margin(0, 0, 0, 10).
		Transition(ui.ElementTransition{Colors: true, Duration: popTime, Ease: easeStandard, Enter: &ui.Motion{X: 4}, Exit: &ui.Motion{X: 4}}).
		Children(func() {
			ui.Text(c, firstNonEmpty(r.title, locales.OutlineUntitled())).FontSize(sizeSub).FontWeight(500).LineHeight(1.375).TextColor(p.Text).MaxLines(3)
			ui.Text(c, preview).FontSize(sizeFoot).LineHeight(1.625).TextColor(p.Secondary).MaxLines(6)
		})
}

var (
	mdLinkText = regexp.MustCompile(`!?\[([^\]]*)\]`)
	mdBlock    = regexp.MustCompile(`(?m)^\s{0,3}(#{1,6}\s+|>\s?|[-*+]\s+(\[[ xX]\]\s+)?|\d+[.)]\s+)`)
	mdStrong   = regexp.MustCompile(`\*\*|__|~~`)
	mdEmphasis = regexp.MustCompile(`(^|\W)[*_]|[*_](\W|$)`)
)

// markdownPlain is Markdown's text: its words, without the marks around
// them, link destinations, tags and fences.
func markdownPlain(src string) string {
	var b strings.Builder
	for _, s := range markdownSegments(src) {
		b.WriteString(src[s.start:s.end])
	}
	out := mdBlock.ReplaceAllString(b.String(), "")
	out = mdLinkText.ReplaceAllString(out, "$1")
	return mdEmphasis.ReplaceAllString(mdStrong.ReplaceAllString(out, ""), "$1$2")
}

// cutRunes cuts s to n characters, with an ellipsis where it was cut.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimRight(string(r[:n]), " ") + "…"
}
