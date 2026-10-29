package app

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// Find in the conversation, as Lody's ⌘F: the prose only, what the user
// typed and what the agent wrote, its thinking included, never tool calls.
// Matches are marked, the one shown more strongly, and Enter steps through
// them, opening what folds them.

type findState struct {
	open    bool
	query   string
	active  int // the result shown, of the results
	reveal  int // frames left to scroll it into view
	glide   findGlide
	handoff ui.Animation // the rest of a glide from afar, once laid out
	focus   bool         // the field takes the focus as it builds
	results []findResult
	blocks  map[string]findBlock // by block, for the views marking them
}

// findResult is one occurrence.
type findResult struct {
	block string
	entry int      // the entry's index in the conversation
	local int      // its place among its block's occurrences
	folds []string // the expanded keys that show it
}

// findBlock is what a searched block's view needs: how many occurrences
// it has and which, if any, is the one shown.
type findBlock struct {
	count  int
	active int // -1 when the shown result is elsewhere
}

func (a *App) openFind() {
	a.find.open, a.find.focus = true, true
}

func (a *App) closeFind() {
	a.find = findState{}
}

// normalizedQuery is the query as Lody searches it: trimmed, lowercase.
func normalizedQuery(q string) []rune {
	return []rune(strings.ToLower(strings.TrimSpace(q)))
}

// occurrences finds q in text, case folded, without overlaps, as byte
// ranges of text.
func occurrences(text string, q []rune) [][2]int {
	if len(q) == 0 {
		return nil
	}
	var runes []rune
	var offsets []int
	for i, r := range text {
		runes = append(runes, unicode.ToLower(r))
		offsets = append(offsets, i)
	}
	offsets = append(offsets, len(text))
	var out [][2]int
	for i := 0; i+len(q) <= len(runes); {
		match := true
		for j, r := range q {
			if runes[i+j] != r {
				match = false
				break
			}
		}
		if !match {
			i++
			continue
		}
		out = append(out, [2]int{offsets[i], offsets[i+len(q)]})
		i += len(q)
	}
	return out
}

// mdSegment is a stretch of Markdown source: text, which a mark can
// wrap, or code, searched but left as it is.
type mdSegment struct {
	start, end int
	code       bool
}

// markdownSegments splits Markdown source into what is searched: text and
// code, leaving out fences, link destinations and HTML tags.
func markdownSegments(src string) []mdSegment {
	var out []mdSegment
	fence := ""
	pos := 0
	for _, line := range strings.SplitAfter(src, "\n") {
		start := pos
		pos += len(line)
		trimmed := strings.TrimLeft(line, " ")
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
				continue
			}
			out = append(out, mdSegment{start, start + len(line), true})
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence = trimmed[:3]
			continue
		}
		out = append(out, inlineSegments(line, start)...)
	}
	return out
}

// inlineSegments splits a line at code spans, link destinations and tags.
func inlineSegments(line string, base int) []mdSegment {
	var out []mdSegment
	text := 0
	flush := func(end int) {
		if end > text {
			out = append(out, mdSegment{base + text, base + end, false})
		}
	}
	for i := 0; i < len(line); {
		switch {
		case line[i] == '`':
			n := 0
			for i+n < len(line) && line[i+n] == '`' {
				n++
			}
			ticks := line[i : i+n]
			if j := strings.Index(line[i+n:], ticks); j >= 0 {
				flush(i)
				out = append(out, mdSegment{base + i + n, base + i + n + j, true})
				i += n + j + n
				text = i
				continue
			}
			i += n
		case strings.HasPrefix(line[i:], "]("):
			if j := strings.IndexByte(line[i:], ')'); j >= 0 {
				flush(i + 1)
				i += j + 1
				text = i
				continue
			}
			i++
		case line[i] == '<':
			if j := strings.IndexByte(line[i:], '>'); j > 0 && !strings.ContainsAny(line[i+1:i+j], " \t") || j > 0 && isTagStart(line[i+1:]) {
				flush(i)
				i += j + 1
				text = i
				continue
			}
			i++
		default:
			_, size := utf8.DecodeRuneInString(line[i:])
			i += size
		}
	}
	flush(len(line))
	return out
}

func isTagStart(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	return c == '/' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// markdownOccurrences are q's occurrences in Markdown source, in order,
// each with whether a mark can wrap it.
func markdownOccurrences(src string, q []rune) (ranges [][2]int, markable []bool) {
	for _, seg := range markdownSegments(src) {
		for _, r := range occurrences(src[seg.start:seg.end], q) {
			ranges = append(ranges, [2]int{seg.start + r[0], seg.start + r[1]})
			markable = append(markable, !seg.code)
		}
	}
	return ranges, markable
}

// markMarkdown wraps q's occurrences in Markdown source in <mark>, the one
// shown underlined too.
func markMarkdown(src string, q []rune, active int) string {
	ranges, markable := markdownOccurrences(src, q)
	if len(ranges) == 0 {
		return src
	}
	var b strings.Builder
	last := 0
	for i, r := range ranges {
		if !markable[i] {
			continue
		}
		b.WriteString(src[last:r[0]])
		open, close := "<mark>", "</mark>"
		if i == active {
			open, close = "<mark><u>", "</u></mark>"
		}
		b.WriteString(open + src[r[0]:r[1]] + close)
		last = r[1]
	}
	b.WriteString(src[last:])
	return b.String()
}

// indexFind finds the query through the conversation, keeping the result
// shown in range.
func (a *App) indexFind(entries []session.Entry) {
	f := &a.find
	f.results, f.blocks = nil, map[string]findBlock{}
	q := normalizedQuery(f.query)
	if !f.open || len(q) == 0 {
		return
	}
	add := func(id string, entry, n int, folds []string) {
		for i := 0; i < n; i++ {
			f.results = append(f.results, findResult{block: id, entry: entry, local: i, folds: folds})
		}
		if n > 0 {
			f.blocks[id] = findBlock{count: n, active: -1}
		}
	}
	for ei, e := range entries {
		if e.Role == "user" {
			if text := e.Text(); text != "" {
				add(e.ID+"/user", ei, len(occurrences(text, q)), []string{e.ID + "/more"})
			}
			continue
		}
		bs := blocks(e.Items)
		k := len(bs)
		if e.Finished {
			k = answerStart(bs)
		}
		for bi, b := range bs {
			var folds []string
			if bi < k {
				folds = append(folds, e.ID+"/work")
			}
			if b.text {
				it := b.items[0]
				ranges, _ := markdownOccurrences(it.Text, q)
				add(e.ID+"/"+it.ID, ei, len(ranges), folds)
				continue
			}
			group := e.ID + "/" + b.key
			for _, it := range b.items {
				if it.Type != "thought" {
					continue
				}
				ranges, _ := markdownOccurrences(it.Text, q)
				add(e.ID+"/"+it.ID, ei, len(ranges), append(append([]string{}, folds...), group, group+"/"+it.ID))
			}
		}
	}
	if len(f.results) == 0 {
		f.active = 0
		return
	}
	f.active = min(max(f.active, 0), len(f.results)-1)
	r := f.results[f.active]
	f.blocks[r.block] = findBlock{count: f.blocks[r.block].count, active: r.local}
}

// stepFind shows the next result, or the one before, wrapping around.
func (a *App) stepFind(step int) {
	f := &a.find
	n := len(f.results)
	if n == 0 {
		return
	}
	f.active = (f.active + step + n) % n
	f.reveal, f.handoff = 3, ui.Animation{}
}

// revealFind opens what folds the result shown and scrolls the list to its
// message, the frame after it changed; its block then scrolls itself in.
func (a *App) revealFind() {
	f := &a.find
	if f.reveal == 0 || f.active >= len(f.results) {
		return
	}
	r := f.results[f.active]
	if f.reveal == 3 {
		for _, key := range r.folds {
			a.expanded[key] = true
		}
		// A message out of view glides the whole way from where it lies
		// (stepFindGlide); one in view, from where it shows (findRing).
		if first, last := a.chatList.Visible(); r.entry < first || r.entry > last {
			a.startFindGlide(r.entry)
		}
	}
}

// findGlide is a search result gliding into view from out of it, as Lody's
// jump: the conversation moves so that its block goes from where it lay
// to the top over 360ms, easing out as a cubic. Until the list builds its
// row, where it lies is reckoned from what the list reckons, and the
// conversation is scrolled along the curve; once its block is laid out,
// the scroll glides the rest of the way to it in the time left (findRing),
// the rest of the curve, which starts as fast as it was going.
type findGlide struct {
	on    bool
	entry int
	start time.Time
	frame time.Time // the frame last stepped
	// dir is the way the conversation moves it, down positive; e is how
	// far along its curve the glide was as of the last frame.
	dir    float32
	e      float32
	frames int // since the glide's time ran out
}

// findGlideTime is how long a search result takes to glide into view.
const findGlideTime = 360 * time.Millisecond

// rowEstimate is where entry's row lay in the window as the list last laid
// the conversation out, as it reckoned it: the rows above the first it
// showed take as much room as it had scrolled past, and those below the
// last share what it had left under it, evenly.
func (a *App) rowEstimate(entry int) float32 {
	m := &a.chatMotion
	top := a.outline.top + chatPadTop
	first, last := a.chatList.Visible()
	if last < first || m.scroll == nil {
		return top
	}
	tf, tl := a.outline.tops[first], a.outline.tops[last]
	// Scrolled as it was laid out, before what the last frame scrolled it.
	y := m.scroll.Y - m.inflight
	if entry < first {
		above := y + tf - top
		return tf - above*float32(first-entry)/float32(first)
	}
	below := m.scroll.MaxY + m.viewH - (y + tl - a.outline.top)
	return tl + below*float32(entry-last)/float32(max(1, m.rows-last))
}

// startFindGlide glides the result in entry to the top from where it is
// reckoned to lie.
func (a *App) startFindGlide(entry int) {
	a.find.glide = findGlide{on: true, entry: entry, dir: sign32(a.outline.top + chatPadTop - a.rowEstimate(entry))}
}

// stepFindGlide scrolls the conversation as far along the curve as the
// glide has come by this frame, before the list builds so that the frame
// lays it out there. Scrolling stops it.
func (a *App) stepFindGlide(c *ui.Context) {
	g := &a.find.glide
	m := &a.chatMotion
	if !g.on || m.scroll == nil {
		return
	}
	now := c.Now()
	if now.Equal(g.frame) {
		return // built again in the frame: stepped already
	}
	g.frame = now
	if g.start.IsZero() {
		g.start = now
	}
	k := min(1, float32(now.Sub(g.start))/float32(findGlideTime))
	e := 1 - (1-k)*(1-k)*(1-k)
	// What is left, reckoned again each frame as the rows it passes are
	// laid out: the frame moves it the share of that the curve covers.
	left := a.outline.top + chatPadTop - (a.rowEstimate(g.entry) - m.inflight)
	var step float32
	if g.e < 1 {
		step = left * (e - g.e) / (1 - g.e)
	}
	g.e = e
	if k == 1 {
		// Further than reckoned: on the same way, a quarter view a frame.
		if g.frames++; g.frames > 60 {
			a.chatList.ScrollTo(g.entry, ui.Start)
			*g = findGlide{}
			return
		}
		step = m.viewH / 4 * g.dir
	}
	// Steps under two views move the rows by as much (ListState); one
	// further jumps where the list reckons. Never back.
	if step = max(-1.8*m.viewH, min(1.8*m.viewH, step)); step*g.dir > 0 {
		m.nudge -= step
	}
	a.redraw()
}

// sign32 is -1, 0 or 1, as v is negative, zero or positive.
func sign32(v float32) float32 {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	}
	return 0
}

// findMark is the look of a search mark: amber, stronger for the result
// shown.
func findMark(p *palette, active bool) ui.Color {
	switch {
	case active && p.Dark:
		return ui.RGBA(252, 211, 77, 0.55)
	case active:
		return ui.RGBA(252, 211, 77, 1)
	case p.Dark:
		return ui.RGBA(251, 191, 36, 0.25)
	}
	return ui.RGBA(253, 230, 138, 0.75)
}

// findMarkdown is what a block shows, marked where the query matches.
func (a *App) findMarkdown(id, src string) string {
	b, ok := a.find.blocks[id]
	if !ok {
		return src
	}
	return markMarkdown(src, normalizedQuery(a.find.query), b.active)
}

// findSpans marks plain text, as a user's message.
func (a *App) findSpans(p *palette, id, text string) ([]ui.Span, bool) {
	b, ok := a.find.blocks[id]
	if !ok {
		return nil, false
	}
	var spans []ui.Span
	last := 0
	for i, r := range occurrences(text, normalizedQuery(a.find.query)) {
		spans = append(spans, ui.Span{Text: text[last:r[0]]}, ui.Span{Text: text[r[0]:r[1]], Background: findMark(p, i == b.active), Radius: 2})
		last = r[1]
	}
	return append(spans, ui.Span{Text: text[last:]}), true
}

// findActive is whether a block holds the result shown, which its view
// rings and scrolls to.
func (a *App) findActive(id string) bool {
	b, ok := a.find.blocks[id]
	return ok && b.active >= 0
}

// findRing rings the block holding the result shown and brings it into
// view.
func (a *App) findRing(p *palette, id string, e ui.Element) {
	if !a.findActive(id) {
		return
	}
	e.Radius(6).Shadow(0, 0, 0, 1, p.Primary.Alpha(0.25))
	anim := chatGlide
	if g := &a.find.glide; g.on {
		b := e.Bounds()
		if b.H == 0 {
			return // gliding from afar, not laid out yet
		}
		// Laid out at last: the rest of the way in the time left.
		left := findGlideTime - time.Since(g.start)
		if !g.frame.IsZero() {
			left = findGlideTime - g.frame.Sub(g.start)
		}
		a.find.handoff = ui.Animation{Duration: max(120*time.Millisecond, left), Ease: chatGlide.Ease}
		*g = findGlide{}
		a.find.reveal = 2
	}
	if a.find.handoff.Duration > 0 {
		anim = a.find.handoff
	}
	if a.find.reveal == 0 {
		return
	}
	// As Lody's search, the block holding the result glides to the top
	// over 360ms, easing out as a cubic, from where it was laid out last;
	// one not laid out yet comes into view at once.
	st := a.chatMotion.scroll
	b := e.Bounds()
	if st == nil || b.H == 0 {
		e.ScrollIntoView()
		return
	}
	st.ScrollTo(0, max(0, st.Y+b.Y-a.outline.top-chatPadTop), anim)
}

// findBar floats over the conversation's top end: the field, the count,
// and previous, next and close.
func (a *App) findBar(c *ui.Context, p *palette) {
	f := &a.find
	if !f.open {
		return
	}
	w, _ := c.Size()
	hasQuery := f.query != ""
	hasResults := len(f.results) > 0
	noResults := hasQuery && !hasResults
	border := p.Border
	if noResults {
		border = p.Danger.Alpha(0.3)
	}
	bar := ui.Row(c.Key("find")).Absolute().Top(16).Right(16).Width(min(360, w-32)).Height(36).Radius(10).Border(0.5, border).
		Background(p.Canvas.Alpha(0.95)).Padding(0, 4, 0, 10).Gap(2).AlignItems(ui.Center).Shadow(0, 4, 12, -4, ui.RGBA(15, 23, 42, 0.16)).
		Transition(twFade) // Tailwind's transition-colors, as Lody's search surface
	bar.Children(func() {
		ink := p.Secondary.Alpha(0.8)
		switch {
		case noResults:
			ink = p.Danger.Alpha(0.8)
		case hasQuery:
			ink = p.Text
		}
		// The glass's ink fades with it, over 150ms.
		ink = bar.AnimateColor("icon", ink, ui.Animation{Duration: 150 * time.Millisecond, Ease: twEase})
		ui.Icon(c, iconSearch).Size(14, 14).TextColor(ink).Shrink(0)
		in := ui.TextInputBase(c.Key("find-query"), &f.query).FontSize(13).TextColor(p.Text).Placeholder(locales.FindInSession()).
			Label(locales.FindInSession()).Grow(1).MinWidth(0).Padding(0, 6).
			OnChange(func() {
				f.active = 0
				f.reveal, f.handoff = 3, ui.Animation{}
			}).
			OnTextKey(func(k ui.TextKey) bool {
				switch {
				case k.Key == ui.KeyEnter && k.Mods == ui.Shift:
					a.stepFind(-1)
					return true
				case k.Key == ui.KeyEnter && k.Mods == 0:
					a.stepFind(1)
					return true
				case k.Key == ui.KeyEscape:
					if f.query != "" {
						k.SetText("")
						f.query = ""
					} else {
						a.closeFind()
					}
					return true
				}
				return false
			})
		if f.focus {
			f.focus = false
			in.Focus()
		}
		if hasQuery {
			label := locales.FindPosition(f.active+1, len(f.results))
			ink := p.Secondary
			if !hasResults {
				label, ink = "0 / 0", p.Danger.Alpha(0.9)
			}
			ui.Text(c, label).FontSize(11.5).FontWeight(500).FontFeatures("tnum").TextColor(ink).Padding(0, 4).Shrink(0)
		}
		ui.Box(c).Size(1, 16).Margin(0, 2).Background(p.Border.Alpha(0.6)).Shrink(0)
		nav := func(icon *ui.SVG, label string, step int) {
			b := iconButton(c, p, icon, label, p.Hover).Size(24, 24).Disabled(!hasResults)
			if !hasResults {
				b.Opacity(0.4)
			}
			b.OnClick(func() { a.stepFind(step) })
		}
		nav(iconArrowUp, locales.FindPrevious(), -1)
		nav(iconArrowDown, locales.FindNext(), 1)
		iconButton(c, p, iconX, locales.Close(), p.Hover).Size(24, 24).OnClick(a.closeFind)
	})
}
