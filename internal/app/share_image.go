package app

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/convmd"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// "Share as image…", as Lody desktop offers it from the session menu: the
// header menu starts a selection over the conversation's messages, the
// composer gives way to a bar that counts them, and confirming previews
// the picked messages as one card (share_image_dialog.go). Finishing the
// share, or an explicit Cancel, ends the selection; dismissing the preview
// keeps it, to adjust and reopen.

// shareImage is the flow's state: the selection, then its preview.
type shareImage struct {
	// The selection: the session it is over, the messages it can pick
	// in their order, those picked, the row a Shift-click extends from,
	// and the gesture under way.
	session    string
	candidates []shareMsg
	ids        map[string]bool
	anchor     string
	drag       *shareDrag
	// What the conversation's rows reported this frame: their boxes,
	// for the gesture to find the row under the pointer, and the
	// viewport they show in.
	rows     []shareRowBox
	viewport ui.Rect
	scrollBy float32 // the edge scroll the next frame applies
	scrolled float32 // and the one the last frame did
	lastTick time.Time

	// The preview: open, what it shows, and its four choices.
	open     bool
	target   shareTarget
	dest     string // chat or post
	mat      float64
	backdrop string
	dark     bool
	natural  ui.Rect // the card's own size, as the last frame laid it out
	trackW   map[string]float32
	busy     string // copy or export, while it runs
	waited   bool   // the busy state has shown a frame
	failed   bool
	copied   bool
}

// shareTarget is what the preview was opened on.
type shareTarget struct {
	session  string
	title    string
	created  string
	messages []shareMsg
	meta     shareMeta
}

// shareDrag is a press on a row and the rows it drags over: from the
// anchor to the row under the pointer, picked or unpicked as the first
// row was, or each flipped with Cmd or Ctrl.
type shareDrag struct {
	anchor string
	base   map[string]bool
	pick   bool
	invert bool
	last   string
	// Where the press began, in its row, for the marquee.
	row    string
	ox, oy float32
	origin ui.Point
	moved  bool
}

type shareRowBox struct {
	id string
	r  ui.Rect
}

// picking reports whether messages of session are being picked; a
// selection over another session ends, as Lody's does with its scope.
func (st *shareImage) picking(session string) bool {
	if st.session == "" {
		return false
	}
	if st.session != session {
		st.cancel()
		return false
	}
	return true
}

func (st *shareImage) cancel() {
	st.session, st.candidates, st.ids, st.anchor, st.drag = "", nil, nil, "", nil
	st.rows, st.scrollBy, st.scrolled = nil, 0, 0
}

func (st *shareImage) index(id string) int {
	for i, m := range st.candidates {
		if m.id == id {
			return i
		}
	}
	return -1
}

// picked are the messages picked, in the conversation's order.
func (st *shareImage) picked() []shareMsg {
	var out []shareMsg
	for _, m := range st.candidates {
		if st.ids[m.id] {
			out = append(out, m)
		}
	}
	return out
}

func (st *shareImage) allPicked() bool {
	return len(st.candidates) > 0 && len(st.picked()) == len(st.candidates)
}

func (st *shareImage) toggleAll() {
	if st.allPicked() {
		st.ids = map[string]bool{}
		return
	}
	st.ids = map[string]bool{}
	for _, m := range st.candidates {
		st.ids[m.id] = true
	}
}

// toggle flips one message, as a click on its box does.
func (st *shareImage) toggle(id string) {
	if st.index(id) < 0 {
		return
	}
	st.anchor = id
	ids := clonePicks(st.ids)
	if ids[id] {
		delete(ids, id)
	} else {
		ids[id] = true
	}
	st.ids = ids
}

// begin starts a gesture on row id: it picks the row, or unpicks it, at
// once, and the drag extends that from the anchor.
func (st *shareImage) begin(id string, shift, invert bool, ox, oy float32) {
	anchor := id
	if shift && st.anchor != "" {
		anchor = st.anchor
	}
	st.drag = &shareDrag{anchor: anchor, base: clonePicks(st.ids), pick: shift || !st.ids[id], invert: invert, row: id, ox: ox, oy: oy}
	if !shift || st.anchor == "" {
		st.anchor = id
	}
	st.extend(id)
}

// extend makes the gesture reach row id, in chronological order.
func (st *shareImage) extend(id string) {
	d := st.drag
	if d == nil || d.last == id {
		return
	}
	from, to := st.index(d.anchor), st.index(id)
	if from < 0 || to < 0 {
		return
	}
	d.last = id
	ids := clonePicks(d.base)
	for _, m := range st.candidates[min(from, to) : max(from, to)+1] {
		on := d.pick
		if d.invert {
			on = !d.base[m.id]
		}
		if on {
			ids[m.id] = true
		} else {
			delete(ids, m.id)
		}
	}
	st.ids = ids
}

func clonePicks(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		if v {
			out[k] = true
		}
	}
	return out
}

// shareImageCandidates are the conversation's persisted messages with prose,
// as Lody's collectConversationMessages finds them: a user or assistant
// entry's text items, joined by a blank line; a turn of tool calls alone
// has none.
func shareImageCandidates(entries []session.Entry) []shareMsg {
	var out []shareMsg
	for _, e := range entries {
		// What is persisted: not a message queued or still on its way.
		if (e.Role != "user" && e.Role != "assistant") || e.Queued || e.Role == "user" && e.Status == "pending" {
			continue
		}
		var texts []string
		for _, it := range e.Items {
			if it.Type == "text" && strings.TrimSpace(it.Text) != "" {
				texts = append(texts, it.Text)
			}
		}
		if len(texts) == 0 {
			continue
		}
		model := e.Model
		if model == "" {
			model, _ = e.Config["modelId"].(string)
		}
		out = append(out, shareMsg{id: e.ID, role: e.Role, text: strings.Join(texts, "\n\n"), tokens: storedTokens(e.Items), model: model})
	}
	return out
}

// storedTokens estimates a turn as stored: its text and thinking, its
// plans, and its tools' textual input and output. It is not billed usage.
func storedTokens(items []session.Item) int {
	n := 0
	count := func(s string) { n += convmd.EstimateTokens(s) }
	js := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
	for _, it := range items {
		switch it.Type {
		case "text", "thought", "proposed_plan":
			count(it.Text)
		case "plan":
			for _, p := range it.Plan {
				count(p.Content)
			}
		case "tool_call":
			count(it.Title)
			if it.RawInput != nil {
				count(js(it.RawInput))
			}
			// Structured content shows the raw output: count one of them.
			output := false
			for _, b := range it.Content {
				if m, _ := b.(map[string]any); m != nil && m["type"] != "terminal_command" {
					output = true
				}
			}
			if !output && it.RawOutput != nil {
				count(js(it.RawOutput))
			}
			for _, b := range it.Content {
				m, _ := b.(map[string]any)
				if m == nil {
					continue
				}
				str := func(k string) string { s, _ := m[k].(string); return s }
				switch m["type"] {
				case "terminal_command":
					if it.RawInput == nil {
						parts := []string{str("command")}
						if args, ok := m["args"].([]any); ok {
							for _, a := range args {
								if s, ok := a.(string); ok {
									parts = append(parts, s)
								}
							}
						}
						count(strings.Join(parts, " "))
					}
				case "terminal_output":
					count(str("output"))
				case "diff":
					count(str("path"))
					count(str("oldText"))
					count(str("newText"))
				case "content":
					inner, _ := m["content"].(map[string]any)
					switch inner["type"] {
					case "text":
						s, _ := inner["text"].(string)
						count(s)
					case "resource":
						if res, ok := inner["resource"].(map[string]any); ok {
							s, _ := res["text"].(string)
							count(s)
						}
					}
				}
			}
		}
	}
	return n
}

// startShareImage is the menu's "Share as image…": a selection over the
// open conversation, or a toast when it has nothing to share.
func (a *App) startShareImage(sessionID string) {
	var msgs []shareMsg
	if a.view != nil && sessionID == a.selected {
		msgs = shareImageCandidates(a.view.Entries)
	}
	if len(msgs) == 0 {
		a.toast("danger", locales.ShareImgEmpty())
		return
	}
	st := &a.shareImg
	st.cancel()
	st.session, st.candidates, st.ids = sessionID, msgs, map[string]bool{}
	// The conversation takes the room back from a full-height composer.
	a.composerFull = false
}

// confirmShareImage opens the preview on the messages picked; the
// selection stays behind it.
func (a *App) confirmShareImage(p *palette) {
	st := &a.shareImg
	msgs := st.picked()
	if len(msgs) == 0 {
		return
	}
	t := shareTarget{session: st.session, messages: msgs}
	if s := a.catalog.Session(st.session); s != nil {
		t.title, t.created = strings.TrimSpace(s.Title), s.CreatedAt
		t.meta = a.shareMetaFor(s, msgs)
	} else {
		t.meta = shareMeta{name: "Lody"}
	}
	st.target = t
	st.open = true
	// Every opening starts from the app's appearance and the device's
	// guess at the destination: a desktop's is a post.
	st.dark = p.Dark
	st.backdrop = "lody"
	st.dest = "post"
	st.mat = float64(shareDefaultMat[st.dest])
	st.failed, st.copied, st.busy, st.waited = false, false, "", false
	st.natural = ui.Rect{}
}

// shareMetaFor is the caption of a card of msgs from session s: its
// runtime, the last reply's recorded model, the token estimate, and the
// session's creation date.
func (a *App) shareMetaFor(s *catalog.Session, msgs []shareMsg) shareMeta {
	m := shareMeta{date: shareDate(s.CreatedAt)}
	switch {
	case s.CLIType == "custom":
		m.name = firstNonEmpty(a.agentName(s.MachineID, s.AgentConfigID), "Custom Agent")
		m.icon = iconSquareTerminal
	case s.CLIType != "" && s.AgentType != "":
		m.name = agentTypeLabel(s.AgentType, s.CLIType)
		m.icon = shareAgentIcon(s.AgentType)
	default:
		m.name = "Lody"
	}
	model, tokens := "", 0
	for _, msg := range msgs {
		if msg.role == "assistant" && msg.model != "" {
			model = msg.model
		}
		if msg.tokens > 0 {
			tokens += msg.tokens
		} else {
			tokens += convmd.EstimateTokens(msg.text)
		}
	}
	if model != "" {
		m.params = append(m.params, model)
	}
	if tokens > 0 {
		m.params = append(m.params, locales.ShareImgEstimatedTokens(formatCount(float64(tokens))))
	}
	return m
}

// The runtimes' marks, as Lody's AgentIcon draws them in currentColor.
var (
	iconAnthropic = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><path fill="currentColor" d="m3.127 10.604l3.135-1.76l.053-.153l-.053-.085H6.11l-.525-.032l-1.791-.048l-1.554-.065l-1.505-.08l-.38-.081L0 7.832l.036-.234l.32-.214l.455.04l1.009.069l1.513.105l1.097.064l1.626.17h.259l.036-.105l-.089-.065l-.068-.064l-1.566-1.062l-1.695-1.121l-.887-.646l-.48-.327l-.243-.306l-.104-.67l.435-.48l.585.04l.15.04l.593.456l1.267.981l1.654 1.218l.242.202l.097-.068l.012-.049l-.109-.181l-.9-1.626l-.96-1.655l-.428-.686l-.113-.411a2 2 0 0 1-.068-.484l.496-.674L4.446 0l.662.089l.279.242l.411.94l.666 1.48l1.033 2.014l.302.597l.162.553l.06.17h.105v-.097l.085-1.134l.157-1.392l.154-1.792l.052-.504l.25-.605l.497-.327l.387.186l.319.456l-.045.294l-.19 1.23l-.37 1.93l-.243 1.29h.142l.161-.16l.654-.868l1.097-1.372l.484-.545l.565-.601l.363-.287h.686l.505.751l-.226.775l-.707.895l-.585.759l-.839 1.13l-.524.904l.048.072l.125-.012l1.897-.403l1.024-.186l1.223-.21l.553.258l.06.263l-.218.536l-1.307.323l-1.533.307l-2.284.54l-.028.02l.032.04l1.029.098l.44.024h1.077l2.005.15l.525.346l.315.424l-.053.323l-.807.411l-3.631-.863l-.872-.218h-.12v.073l.726.71l1.331 1.202l1.667 1.55l.084.383l-.214.302l-.226-.032l-1.464-1.101l-.565-.497l-1.28-1.077h-.084v.113l.295.432l1.557 2.34l.08.718l-.112.234l-.404.141l-.444-.08l-.911-1.28l-.94-1.44l-.759-1.291l-.093.053l-.448 4.821l-.21.246l-.484.186l-.403-.307l-.214-.496l.214-.98l.258-1.28l.21-1.016l.19-1.263l.112-.42l-.008-.028l-.092.012l-.953 1.307l-1.448 1.957l-1.146 1.227l-.274.109l-.477-.247l.045-.44l.266-.39l1.586-2.018l.956-1.25l.617-.723l-.004-.105h-.036l-4.212 2.736l-.75.096l-.324-.302l.04-.496l.154-.162l1.267-.871z"/></svg>`))
	iconOpenAI    = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="currentColor" d="M22.282 9.821a5.985 5.985 0 0 0-.516-4.91a6.046 6.046 0 0 0-6.51-2.9A6.065 6.065 0 0 0 4.981 4.18a5.985 5.985 0 0 0-3.998 2.9a6.046 6.046 0 0 0 .743 7.097a5.98 5.98 0 0 0 .51 4.911a6.051 6.051 0 0 0 6.515 2.9A5.985 5.985 0 0 0 13.26 24a6.056 6.056 0 0 0 5.772-4.206a5.99 5.99 0 0 0 3.997-2.9a6.056 6.056 0 0 0-.747-7.073M13.26 22.43a4.476 4.476 0 0 1-2.876-1.04l.141-.081l4.779-2.758a.795.795 0 0 0 .392-.681v-6.737l2.02 1.168a.071.071 0 0 1 .038.052v5.583a4.504 4.504 0 0 1-4.494 4.494M3.6 18.304a4.47 4.47 0 0 1-.535-3.014l.142.085l4.783 2.759a.771.771 0 0 0 .78 0l5.843-3.369v2.332a.08.08 0 0 1-.033.062L9.74 19.95a4.5 4.5 0 0 1-6.14-1.646M2.34 7.896a4.485 4.485 0 0 1 2.366-1.973V11.6a.766.766 0 0 0 .388.677l5.815 3.355l-2.02 1.168a.076.076 0 0 1-.071 0l-4.83-2.786A4.504 4.504 0 0 1 2.34 7.872zm16.597 3.855l-5.833-3.387L15.119 7.2a.076.076 0 0 1 .071 0l4.83 2.791a4.494 4.494 0 0 1-.676 8.105v-5.678a.79.79 0 0 0-.407-.667m2.01-3.023l-.141-.085l-4.774-2.782a.776.776 0 0 0-.785 0L9.409 9.23V6.897a.066.066 0 0 1 .028-.061l4.83-2.787a4.5 4.5 0 0 1 6.68 4.66zm-12.64 4.135l-2.02-1.164a.08.08 0 0 1-.038-.057V6.075a4.5 4.5 0 0 1 7.375-3.453l-.142.08L8.704 5.46a.795.795 0 0 0-.393.681zm1.097-2.365l2.602-1.5l2.607 1.5v2.999l-2.597 1.5l-2.607-1.5z"/></svg>`))
)

func shareAgentIcon(agentType string) *ui.SVG {
	switch strings.ToLower(agentType) {
	case "claude", "claude-code", "claude-p":
		return iconAnthropic
	case "codex":
		return iconOpenAI
	}
	return iconBot
}

// shareImageEnded finishes the flow: the image reached its destination,
// so the preview closes and the selection behind it goes. A copy says so,
// as nothing else is left to.
func (a *App) shareImageEnded(action string) {
	st := &a.shareImg
	st.open, st.busy, st.waited = false, "", false
	if st.session == st.target.session {
		st.cancel()
	}
	if action == "copied" {
		a.toast("success", locales.ShareImgCopied())
	}
}

// shareRow builds an entry of the conversation while its messages are
// picked: the gap below it rather than above, so a picked row's tint
// runs from its top to the next row as Lody's does, and for a message
// with prose its box in the gutter, its content moved over for it, and a
// cover over it all that takes the pointer for picking.
func (a *App) shareRow(c *ui.Context, p *palette, entries []session.Entry, i int, row func() ui.Element) ui.Element {
	st := &a.shareImg
	e := entries[i]
	below := float32(0)
	if i+1 < len(entries) {
		below = entryTop(entries, i+1)
	}
	wrap := ui.Column(c.Key("share/"+e.ID)).FillWidth().Padding(0, 0, below, 0)
	box := wrap.Bounds()
	edge := func() {
		if st.scrollBy == 0 || box.H <= 0 || st.viewport.H <= 0 {
			return
		}
		// The edge scroll: a point just past the viewport's edge, which
		// the list scrolls as little as shows.
		y := st.viewport.Y + st.scrollBy - box.Y
		if st.scrollBy > 0 {
			y = st.viewport.Y + st.viewport.H + st.scrollBy - 1 - box.Y
		}
		ui.Box(c.Key("edge")).Absolute().Left(0).Top(y).Size(1, 1).PassThrough().ScrollIntoView()
		st.scrolled, st.scrollBy = st.scrollBy, 0
	}
	if st.index(e.ID) < 0 {
		return wrap.Children(func() {
			row()
			edge()
		})
	}
	id := e.ID
	picked := st.ids[id]
	if picked {
		wrap.Background(p.Primary.Alpha(0.1))
	}
	if len(st.rows) < 256 {
		st.rows = append(st.rows, shareRowBox{id: id, r: box})
	}
	wrap.Children(func() {
		ui.Row(c.Key("content")).FillWidth().Padding(0, 0, 0, 28).Children(func() { row() })
		// The box sits in the unmoved column's gutter, 12 below the top.
		ui.Box(c.Key("mark")).Absolute().Left(0).Right(0).Top(12).PassThrough().Children(func() {
			a.column(c, "mark-column", func() {
				shareCheckbox(c, p, picked).Margin(0, 0, 0, -columnGutter)
			})
		})
		edge()
		cover := ui.Box(c.Key("cover")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Focusable().
			Role(ui.RoleCheckBox).Checked(picked).Label(locales.ShareImgSelectMessage())
		cover.HandleInput(func(ev ui.InputEvent) bool {
			switch ev.Kind {
			case ui.InputPointerDown:
				if ev.Button != 0 {
					return false
				}
				st.begin(id, ev.Mods&ui.Shift != 0, ev.Mods&(ui.Cmd|ui.Ctrl) != 0, ev.X, ev.Y)
				return true
			case ui.InputPointerMove:
				return st.drag != nil
			case ui.InputPointerUp:
				st.drag = nil
				return true
			case ui.InputKeyDown:
				if ev.Key == ui.KeySpace && ev.Mods == 0 {
					st.toggle(id)
					return true
				}
			}
			return false
		})
	})
	return wrap
}

// shareCheckbox is @lody/ui's check box: a 16px well, the accent with an
// ink mark once checked, fading between them over 120ms.
func shareCheckbox(c *ui.Context, p *palette, on bool) ui.Element {
	b := ui.Box(c.Key("check")).Size(16, 16).Radius(5).Shrink(0).Center().Transition(colorFade)
	if on {
		top := p.Primary.Mix(ui.Hex("#FFFFFF"), 0.1)
		bottom := p.Primary
		if p.Dark {
			top, bottom = p.Primary, p.Primary.Mix(ui.Hex("#000000"), 0.08)
		}
		b.Gradient(top, bottom, 180)
		if p.Dark {
			b.InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.55)).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.5))
		} else {
			b.InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.18)).Shadow(0, 1, 1.5, 0, ui.RGBA(28, 28, 28, 0.18))
		}
	} else {
		b.Background(wellGround(p))
		if p.Dark {
			b.InsetShadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.4)).InsetShadow(0, 0, 0, 1, ui.RGBA(255, 255, 255, 0.06)).Shadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.04))
		} else {
			b.InsetShadow(0, 1, 1.5, 0, ui.RGBA(28, 28, 28, 0.08)).InsetShadow(0, 0, 0, 1, ui.RGBA(28, 28, 28, 0.06)).Shadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.8))
		}
	}
	b.Children(func() {
		if on {
			ui.Icon(c, shareCheckMark).Size(10, 10).TextColor(p.Canvas)
		}
	})
	return b
}

var shareCheckMark = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10" fill="none"><path d="M1.6 5.2 3.9 7.5 8.4 2.7" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>`))

// shareMarquee lies over the conversation while messages are picked: it
// follows a drag, finding the row under the pointer, drawing the
// rectangle swept from the press, and scrolling the list near its edges.
func (a *App) shareMarquee(c *ui.Context, p *palette) {
	st := &a.shareImg
	if !st.picking(a.selected) {
		return
	}
	rows := st.rows
	st.rows = nil
	layer := ui.Box(c.Key("share-marquee")).Absolute().Left(0).Top(0).Right(0).Bottom(0).PassThrough().Clip()
	vp := layer.Bounds()
	st.viewport = vp
	d := st.drag
	if d == nil || vp.H <= 0 {
		st.lastTick = time.Time{}
		return
	}
	lx, ly, _ := layer.PointerPosition()
	x, y := vp.X+lx, vp.Y+ly
	// The row under the pointer, held inside the viewport, as Lody hit-
	// tests a point clamped into its scroll root.
	hy := min(max(y, vp.Y+1), vp.Y+vp.H-1)
	for _, r := range rows {
		if hy >= r.r.Y && hy < r.r.Y+r.r.H {
			st.extend(r.id)
			break
		}
	}
	// The marquee, from the press point in its row, scrolling with it
	// once the row is out of the list, to the pointer held inside.
	found := false
	for _, r := range rows {
		if r.id == d.row {
			d.origin, found = ui.Point{X: r.r.X + d.ox, Y: r.r.Y + d.oy}, true
		}
	}
	if !found {
		d.origin.Y -= st.scrolled
	}
	st.scrolled = 0
	ex, ey := min(max(x, vp.X), vp.X+vp.W), min(max(y, vp.Y), vp.Y+vp.H)
	w, h := abs32(ex-d.origin.X), abs32(ey-d.origin.Y)
	if max(w, h) >= 4 {
		d.moved = true
	}
	layer.Children(func() {
		if max(w, h) < 4 {
			return
		}
		ui.Box(c.Key("rect")).Absolute().Left(min(ex, d.origin.X)-vp.X).Top(min(ey, d.origin.Y)-vp.Y).Size(max(1, w), max(1, h)).
			Border(1, p.Primary).Background(p.Primary.Alpha(0.15))
	})
	// Within 48 of an edge, or past it, the list scrolls that way, faster
	// the deeper: up to 0.7 DIPs a millisecond.
	const edge = 48
	speed := float32(0)
	switch {
	case y < vp.Y+edge:
		speed = -min(1, (vp.Y+edge-y)/edge)
	case y > vp.Y+vp.H-edge:
		speed = min(1, (y-vp.Y-vp.H+edge)/edge)
	}
	now := c.Now()
	if speed == 0 {
		st.lastTick = time.Time{}
		return
	}
	if !st.lastTick.IsZero() {
		dt := min(32, float32(now.Sub(st.lastTick))/float32(time.Millisecond))
		st.scrollBy = speed * dt * 0.7
	}
	st.lastTick = now
	c.AnimationFrame()
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// shareSelectionBar takes the composer's place while messages are picked:
// the session's info bar, then how many are picked, Select all or Clear
// selection, Cancel, and Preview image. It reports whether it did.
func (a *App) shareSelectionBar(c *ui.Context, p *palette, sess *catalog.Session) bool {
	st := &a.shareImg
	if !st.picking(a.selected) {
		return false
	}
	if !st.open && c.Shortcut(0, ui.KeyEscape) {
		st.cancel()
		return false
	}
	if sess != nil {
		bottomColumn(c, func() { a.infoBar(c, p, sess) })
	}
	count := len(st.picked())
	ui.Row(c.Key("share-bar")).FillWidth().Shrink(0).Justify(ui.Center).Children(func() {
		bar := ui.Row(c).Grow(1).MinWidth(0).Padding(12, columnGutter).Gap(8).Wrap().AlignItems(ui.Center).
			BorderWidth(1, 0, 0, 0).BorderColor(p.Border)
		if !a.wideMode {
			bar.MaxWidth(columnWidth + 2*columnGutter)
		}
		bar.Children(func() {
			ui.Text(c, locales.ShareImgSelectedCount(count)).FontSize(sizeBody).TextColor(p.Text).Grow(1).Role(ui.RoleStatus)
			label := locales.ShareImgSelectAll()
			if st.allPicked() {
				label = locales.ShareImgClearSelection()
			}
			if lodyButton(c, p, "ghost", "toggle-all", nil, label, true).Clicked() {
				st.toggleAll()
			}
			if lodyButton(c, p, "ghost", "cancel", nil, locales.Cancel(), true).Clicked() {
				st.cancel()
			}
			if lodyButton(c, p, "primary", "preview", iconImage, locales.ShareImgPreview(), count > 0).Clicked() && count > 0 {
				a.confirmShareImage(p)
			}
		})
	})
	return true
}
