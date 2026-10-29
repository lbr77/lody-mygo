package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/schedule"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// The message queue, as Lody's MessageQueueDisplay: a sheet tucked onto
// the info bar's top edge, inset past its corners, rounded on top and open
// at the bottom, in the composer's fill and hairline, so bar and sheet read
// as one piece. Its header folds it, the chevron turning from -90° to rest
// and the rows' height growing over 180ms on ease.standard, as @lody/ui's
// Collapsible; it starts open, and folding only hides. "Up next" numbers
// the queued messages, then a Scheduled section lists the chat's scheduled
// messages by send time. A queued row's number turns into a grip under the
// pointer, which drags the row to another place, the rows between sliding
// aside on dnd-kit's default transition, 250ms ease, and gliding into
// their new places on it once dropped. Its actions end the row: Steer, edit
// and remove, with tips after 300ms, folded into one More button on a sheet
// narrower than 520. Queued messages show only here, never in the
// conversation, as Lody's. Messages routed to the queue while their
// attachments upload show after the queued ones as pending rows, which
// only retry and cancel, and give way to the real items as they come.

const (
	queueLine      = 18  // one line of queue text, which the gutter, number and actions center on
	queueGutter    = 16  // the leading gutter every row shares, and the header's chevron
	queueInlineMin = 520 // below this sheet width, a row's actions fold into More
	queueMaxHeight = 240 // or a quarter of the window, past which the rows scroll
	queueEdgeFade  = 20  // the scrolled rows fading out at the edge they go past
	queueDragStart = 6   // how far a press moves before it drags, as dnd-kit's PointerSensor
	queueSortTime  = 250 * time.Millisecond
	// queueLeading is the queue's 13-DIP text on its 18-DIP line.
	queueLeading = float32(queueLine) / sizeSub
)

var (
	// queueRowFade fades a row's fill, and the Steer pill's, over 120ms on
	// CSS's default curve, as their transition-duration without a timing.
	queueRowFade = ui.ElementTransition{Colors: true, Duration: fadeTime, Ease: cssEase}
	// iconGripVertical is Lucide's grip-vertical, a row's drag handle.
	iconGripVertical = lucide(`<circle cx="9" cy="12" r="1"/><circle cx="9" cy="5" r="1"/><circle cx="9" cy="19" r="1"/><circle cx="15" cy="12" r="1"/><circle cx="15" cy="5" r="1"/><circle cx="15" cy="19" r="1"/>`)
	// iconArrowUpToLine is Lucide's arrow-up-to-line, for Move to top.
	iconArrowUpToLine = lucide(`<path d="M5 3h14"/><path d="m18 13-6-6-6 6"/><path d="M12 7v14"/>`)
	// iconSend is Lucide's send, for Steer in a row's menu.
	iconSend = lucide(`<path d="M14.536 21.686a.5.5 0 0 0 .937-.024l6.5-19a.496.496 0 0 0-.635-.635l-19 6.5a.5.5 0 0 0-.024.937l7.93 3.18a2 2 0 0 1 1.112 1.11z"/><path d="m21.854 2.147-10.94 10.939"/>`)
	// iconQueueChevron is the header's ChevronDown, at Lody's 1.75 stroke.
	iconQueueChevron = lucideStroke("1.75", `<path d="m6 9 6 6 6-6"/>`)
	// iconAlertBold is a scheduled message's CircleAlert, at a 2 stroke.
	iconAlertBold = lucideStroke("2", `<circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/>`)
)

// lucideStroke is a Lucide icon at a stroke of its own, where Lody sets one.
func lucideStroke(width, body string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="` + width + `" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`))
}

// scheduledItem is a scheduled message the queue lists, as Lody's
// ScheduledQueueItem: never a queue item, only shown beside them.
type scheduledItem struct {
	id, title string
	at        time.Time
	loc       *time.Location // the machine's, which the time is told in
	failed    bool
}

// queueProps are what the sheet shows and what its rows do, as
// MessageQueueDisplay's props. The edits report back through done, on the
// UI thread, as their writes settle.
type queueProps struct {
	sessionID string
	items     []session.Entry
	// pending are the messages held on their way to the queue, which
	// follow its items.
	pending   []*pendingSend
	scheduled []scheduledItem
	// showSteer is a reply running to steer; nativeSteer that the agent
	// takes any queued message mid-turn, where without it only the first
	// steers, by interrupting the turn, which always runs the queue's head
	// next.
	showSteer, nativeSteer bool

	onSteer      func(id string)
	onRemove     func(id string)
	onReorder    func(id, over string, done func(error))
	onEditStart  func(id string, done func(error))
	onEditCancel func(id string, done func(error))
	onEditSave   func(id, text string, done func(error))

	onOpenScheduled     func(s scheduledItem)
	onEditScheduledTime func(s scheduledItem)
	onCancelScheduled   func(s scheduledItem)
}

// queueView is the sheet's own state, as the mounted display's: folded or
// not, the row being edited and the row being dragged.
type queueView struct {
	folded    bool
	headFocus bool // the header has the focus or the press: a draft keeps
	compact   bool // narrower than queueInlineMin, as the last frame laid it out
	scroll    ui.ScrollState
	edit      queueEdit
	// closed is the row whose edit this sheet just ended, until the queue
	// no longer says it is being edited.
	closed string
	drag   queueDrag
	// order is the order a drop or Move to top asked for, shown until the
	// queue has it.
	order []string
	// rects are the rows' boxes in the last layout, before they moved;
	// rows their parent's.
	rects map[string]ui.Rect
	rows  ui.Rect
	// shift is how far each row is aside while one is dragged; settle how
	// far each starts from its place once one was dropped, gliding to it.
	shift       map[string]float32
	settle      map[string]float32
	settleEpoch int
}

// queueEdit is the row whose text is being edited, as Lody's
// useMessageQueueEditing: only the latest operation, op, may change it.
type queueEdit struct {
	id, text string
	pending  bool // a write for it is on its way
	focus    bool // its field takes the focus once ready
	focused  bool // its field had the focus last frame
	field    ui.Handle
	op       int
}

// queueDrag is a row the pointer presses by its grip, dragged once the
// press moved queueDragStart DIPs.
type queueDrag struct {
	id       string
	x0, y0   float32 // where the press began, in the rows' column
	dx, dy   float32 // how far it moved since
	active   bool
	released bool // let go: dropped in the next build
	over     int  // the row it would drop on
	slots    []queueSlot
	box      ui.Rect // the rows' column as it began
	epoch    int     // which drag this is
}

// queueSlot is a row's place in the list as a drag began.
type queueSlot struct {
	id   string
	rect ui.Rect
}

// queueState is a chat's sheet state, made as it first shows.
func (a *App) queueState(sessionID string) *queueView {
	if a.mqViews == nil {
		a.mqViews = map[string]*queueView{}
	}
	st := a.mqViews[sessionID]
	if st == nil {
		st = &queueView{}
		a.mqViews[sessionID] = st
	}
	return st
}

// queueSheet is the open chat's queue on its info bar, as Lody mounts
// MessageQueueDisplay in the bar's queue slot: with nothing queued or
// scheduled there is none, and its state goes with it.
func (a *App) queueSheet(c *ui.Context, p *palette, sess *catalog.Session, v *session.View) {
	var items []session.Entry
	if v != nil {
		items = v.Queue
	}
	pending := a.heldSends(sess.ID, v, true)
	scheduled := a.scheduledQueueItems(sess)
	if len(items)+len(pending)+len(scheduled) == 0 {
		delete(a.mqViews, sess.ID)
		return
	}
	sid := sess.ID
	o := queueProps{
		sessionID:   sid,
		items:       items,
		pending:     pending,
		scheduled:   scheduled,
		showSteer:   runningReply(v),
		nativeSteer: a.steerable(sess),
		onSteer: func(id string) {
			a.steerQueued(id, !a.steerable(sess))
		},
		onRemove: func(id string) {
			a.queueWrite(locales.MqRemoveError(), func(ctx context.Context, r *session.Replica) error { return r.RemoveQueued(ctx, id) }, nil)
		},
		onReorder: func(id, over string, done func(error)) {
			a.queueWrite(locales.MqReorderError(), func(ctx context.Context, r *session.Replica) error { return r.MoveQueued(ctx, id, over) }, done)
		},
		onEditStart: func(id string, done func(error)) {
			a.queueWrite(locales.MqEditError(), func(ctx context.Context, r *session.Replica) error { return r.EditingQueued(ctx, id, true) }, done)
		},
		onEditCancel: func(id string, done func(error)) {
			a.queueWrite(locales.MqEditError(), func(ctx context.Context, r *session.Replica) error { return r.EditingQueued(ctx, id, false) }, done)
		},
		onEditSave: func(id, text string, done func(error)) {
			imageOnly := locales.MqImageOnly()
			a.queueWrite(locales.MqEditError(), func(ctx context.Context, r *session.Replica) error { return r.EditQueued(ctx, id, text, imageOnly) }, done)
		},
		onOpenScheduled: func(s scheduledItem) { a.openSchedule(s.id) },
		onEditScheduledTime: func(s scheduledItem) {
			a.openScheduleSend(sid, s.at, s.id)
		},
		onCancelScheduled: func(s scheduledItem) {
			user := a.userID()
			a.schedAct(func(ctx context.Context, store *schedule.Store) error {
				return store.SetEnabled(ctx, s.id, false, user, time.Now())
			}, func() { a.toast("success", locales.SmCancelled()) })
		},
	}
	// Inset past the bar's rounded corners, so the sheet's square bottom
	// meets its straight edge.
	ui.Column(c.Key("mq")).FillWidth().Padding(0, 12).Children(func() {
		a.queuePanel(c, p, a.queueState(sid), o)
	})
}

// runningReply reports whether a reply is being written, which a queued
// message can steer.
func runningReply(v *session.View) bool {
	if v == nil || !v.Working {
		return false
	}
	for i := len(v.Entries) - 1; i >= 0; i-- {
		if e := v.Entries[i]; e.Role == "assistant" {
			return !e.Finished
		}
	}
	return false
}

// queueWrite changes the open chat's queue, then marks the session's
// queue as changed, which wakes its machine, as Lody's writer does after
// each change; a failure is a toast saying what failed. done, if set,
// hears how it went, on the UI thread.
func (a *App) queueWrite(fail string, do func(ctx context.Context, r *session.Replica) error, done func(error)) {
	r, sync, sid := a.replica, a.sync, a.selected
	if r == nil {
		if done != nil {
			done(errors.New("session: not open"))
		}
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := do(ctx, r)
		if err == nil && sync != nil {
			err = sync.MarkDispatch(ctx, sid, "", true)
		}
		a.update(func() {
			if err != nil {
				a.toasts = append(a.toasts, ui.Toast{Title: fail, Description: err.Error(), Type: "error"})
			}
			if done != nil {
				done(err)
			}
		})
	}()
}

// scheduledQueueItems are a chat's scheduled messages still to send.
func (a *App) scheduledQueueItems(sess *catalog.Session) []scheduledItem {
	if sess == nil {
		return nil
	}
	loc := loadZone(a.machineZone(sess.MachineID))
	var out []scheduledItem
	for _, row := range a.sched.reg.Rows {
		if !row.ScheduledMessage || row.Destination.SessionID != sess.ID || !row.Enabled {
			continue
		}
		rt := a.sched.reg.Runtime[row.ScheduleID]
		if rt != nil && (rt.MessageState == "sent" || rt.MessageState == "cancelled") {
			continue
		}
		at, _ := time.Parse(time.RFC3339Nano, row.Trigger.At)
		out = append(out, scheduledItem{id: row.ScheduleID, title: row.Title, at: at, loc: loc, failed: rt != nil && rt.MessageState == "failed"})
	}
	return out
}

// queuePanel is the sheet: its header, and the rows it folds.
func (a *App) queuePanel(c *ui.Context, p *palette, st *queueView, o queueProps) {
	items := st.ordered(o.items)
	scheduled := append([]scheduledItem(nil), o.scheduled...)
	sort.SliceStable(scheduled, func(i, j int) bool { return scheduled[i].at.Before(scheduled[j].at) })
	queued := len(items) + len(o.pending)
	if queued+len(scheduled) == 0 {
		return
	}
	st.follow(items)
	onlyScheduled := queued == 0
	failed := 0
	for _, s := range scheduled {
		if s.failed {
			failed++
		}
	}
	notSent := 0
	for _, s := range o.pending {
		if s.err != "" {
			notSent++
		}
	}
	open := !st.folded
	title := locales.MqUpNext()
	if onlyScheduled {
		title = locales.MqScheduledTitle()
	}
	sheet := ui.Column(c.Key("mq-sheet")).FillWidth().Radius(8, 8, 0, 0).BorderWidth(0.5, 0.5, 0, 0.5).BorderColor(p.Text.Alpha(0.1)).
		Background(p.Composer).Clip()
	sheet.Children(func() {
		// The chevron sits in the rows' gutter, so the disclosure, the
		// numbers and the Scheduled label share one left edge.
		head := ui.ButtonBase(c.Key("mq-head")).FillWidth().MinHeight(30).Padding(0, 10).Gap(8).AlignItems(ui.Center).Justify(ui.Start).
			Cursor(ui.CursorPointer).Label(title).OnClick(func() { st.folded = !st.folded })
		st.headFocus = head.Focused() || head.Pressed()
		turn := head.AnimateWith("turn", b2f(open), foldTime, easeStandard)
		k := head.AnimateWith("open", b2f(open), foldTime, easeStandard)
		head.Children(func() {
			ui.Box(c).Width(queueGutter).Shrink(0).Center().Children(func() {
				ui.Icon(c, iconQueueChevron).Size(14, 14).TextColor(p.Secondary).Rotate(-90 * (1 - turn))
			})
			ui.Text(c, title).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text.Alpha(0.86)).SingleLine().Shrink(0)
			if !onlyScheduled {
				var counts []string
				if queued > 0 {
					counts = append(counts, locales.MqQueuedCount(queued))
				}
				if len(scheduled) > 0 {
					counts = append(counts, locales.MqScheduledCount(len(scheduled)))
				}
				ui.Text(c, strings.Join(counts, " · ")).FontSize(sizeFoot).FontFeatures("tnum").TextColor(p.Secondary).SingleLine().MinWidth(0).Shrink(1)
			}
			// Folded, what didn't go out, and what still prepares, says so.
			if !open && (len(o.pending) > 0 || failed > 0) {
				ui.Spacer(c)
				ui.Row(c.Key("mq-status")).Wrap().Gap(8).Shrink(0).Role(ui.RoleStatus).Children(func() {
					if notSent > 0 {
						ui.Text(c, locales.MqFailedCount(notSent)).FontSize(sizeFoot).TextColor(p.Danger).SingleLine()
					}
					if preparing := len(o.pending) - notSent; preparing > 0 {
						ui.Text(c, locales.MqPreparingCount(preparing)).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
					}
					if failed > 0 {
						ui.Text(c, locales.MqScheduledFailedCount(failed)).FontSize(sizeFoot).TextColor(p.Danger).SingleLine()
					}
				})
			}
		})
		heightReveal(c, k, open, func() { a.queueList(c, p, st, o, items, scheduled) })
	})
}

// queueList is the rows, scrolling past a quarter of the window or 240,
// their edges fading out 20 DIPs where more lies beyond.
func (a *App) queueList(c *ui.Context, p *palette, st *queueView, o queueProps, items []session.Entry, scheduled []scheduledItem) {
	maxH := float32(queueMaxHeight)
	if _, h := c.Size(); h > 0 {
		maxH = min(maxH, h/4)
	}
	ui.Column(c.Key("mq-body")).FillWidth().Children(func() {
		list := ui.Scroll(c.Key("mq-list")).FillWidth().MaxHeight(maxH).TrackScroll(&st.scroll)
		if w := list.LayoutBounds().W; w > 0 {
			st.compact = w < queueInlineMin
		}
		list.Children(func() {
			rows := ui.Column(c.Key("mq-rows")).FillWidth().Padding(0, 4, 4, 4)
			// Where the pointer is in the rows, asked only while a grip is
			// held, as asking brings a frame on every move over them.
			pointer := func() (float32, float32) {
				x, y, _ := rows.PointerPosition()
				return x, y
			}
			st.rows = rows.LayoutBounds()
			st.track(c, items, pointer, o)
			rows.Children(func() {
				for i := range items {
					a.queueRow(c, p, st, o, items, i, pointer)
				}
				// Held messages are no queue items yet: they stand where
				// theirs will be appended, out of the drag.
				for i, s := range o.pending {
					a.pendingQueueRow(c, p, st, s, len(items)+i)
				}
				if len(items)+len(o.pending) > 0 && len(scheduled) > 0 {
					ui.Row(c.Key("mq-scheduled")).FillWidth().Padding(8, 6, 2, 6).Children(func() {
						ui.Text(c, locales.MqScheduledTitle()).FontSize(sizeCaption).LineHeight(16.0 / sizeCaption).FontWeight(500).TextColor(p.Secondary)
					})
				}
				for _, s := range scheduled {
					a.scheduledRow(c, p, st, o, s)
				}
			})
		})
		fade := func(key string, top bool) {
			edge := ui.Box(c.Key(key)).Absolute().Left(0).Right(0).Height(queueEdgeFade).PassThrough()
			from, to := p.Composer, p.Composer.Alpha(0)
			if top {
				edge.Top(0)
			} else {
				edge.Bottom(0)
				from, to = to, from
			}
			edge.Draw(func(pt *ui.Painter, r ui.Rect) {
				pt.FillGradient(r, ui.LinearGradient{From: from, To: to, Angle: 180}, 0)
			})
		}
		if st.scroll.Y > 0.5 {
			fade("mq-fade-top", true)
		}
		if st.scroll.Y < st.scroll.MaxY-0.5 {
			fade("mq-fade-bottom", false)
		}
	})
}

// ordered is the queue in the order a drop asked for, until the queue has
// it, or no longer holds the same messages.
func (st *queueView) ordered(items []session.Entry) []session.Entry {
	if st.order == nil {
		return items
	}
	byID := make(map[string]session.Entry, len(items))
	same := len(st.order) == len(items)
	for i, e := range items {
		byID[e.ID] = e
		if same && st.order[i] != e.ID {
			same = false
		}
	}
	if same || len(st.order) != len(items) {
		st.order = nil
		return items
	}
	out := make([]session.Entry, 0, len(items))
	for _, id := range st.order {
		e, ok := byID[id]
		if !ok {
			st.order = nil
			return items
		}
		out = append(out, e)
	}
	return out
}

// follow keeps the editor with the queue, as Lody's: it closes when its
// message leaves, and opens on one being edited elsewhere.
func (st *queueView) follow(items []session.Entry) {
	if st.edit.id != "" {
		for _, e := range items {
			if e.ID == st.edit.id {
				return
			}
		}
		st.edit = queueEdit{op: st.edit.op}
		return
	}
	closing := false
	for _, e := range items {
		switch {
		case e.Editing && e.ID == st.closed:
			closing = true // the queue hasn't heard yet
		case e.Editing:
			st.edit = queueEdit{id: e.ID, text: e.Text(), op: st.edit.op}
			return
		}
	}
	if !closing {
		st.closed = ""
	}
}

// startEdit opens a row's editor, closing another's, once its message is
// marked as edited, which holds the queue while it is first.
func (st *queueView) startEdit(o queueProps, e session.Entry) {
	if st.edit.id == e.ID {
		return
	}
	if prev := st.edit.id; prev != "" {
		st.closed = prev
		o.onEditCancel(prev, nil)
	}
	op := st.edit.op + 1
	st.edit = queueEdit{id: e.ID, text: e.Text(), pending: true, op: op}
	o.onEditStart(e.ID, func(err error) {
		if st.edit.op != op {
			return
		}
		if err != nil {
			st.edit = queueEdit{op: op}
			return
		}
		st.edit.pending, st.edit.focus = false, true
	})
}

// commitEdit ends an edit, as every way of leaving the editor does:
// unchanged, or emptied of all it had, it is given up; otherwise saved.
func (st *queueView) commitEdit(o queueProps, e session.Entry) {
	ed := &st.edit
	if ed.id != e.ID || ed.pending {
		return
	}
	text := strings.TrimSpace(ed.text)
	op := ed.op + 1
	ed.op, ed.pending = op, true
	done := func(err error) {
		if st.edit.op != op {
			return
		}
		if err != nil {
			// It stays open, to be committed again.
			st.edit.pending = false
			return
		}
		st.edit, st.closed = queueEdit{op: op}, e.ID
	}
	if text == strings.TrimSpace(e.Text()) || text == "" && len(queueImages(e)) == 0 {
		o.onEditCancel(e.ID, done)
		return
	}
	o.onEditSave(e.ID, text, done)
}

// moveToTop takes a row to the head of the queue at once.
func (st *queueView) moveToTop(o queueProps, items []session.Entry, i int) {
	st.order = arrayMove(queueIDs(items), i, 0)
	o.onReorder(items[i].ID, items[0].ID, func(err error) {
		if err != nil {
			st.order = nil
		}
	})
}

// track follows the drag under way before the rows are built: how far it
// moved, whether it began, and the row it is over, by the nearest center,
// as dnd-kit's closestCenter. A row let go drops here.
func (st *queueView) track(c *ui.Context, items []session.Entry, pointer func() (float32, float32), o queueProps) {
	d := &st.drag
	if d.id == "" {
		return
	}
	px, py := pointer()
	from := -1
	for i, e := range items {
		if e.ID == d.id {
			from = i
		}
	}
	if from < 0 || d.active && !sameSlots(d.slots, items) {
		st.drag = queueDrag{epoch: d.epoch}
		return
	}
	if d.released {
		st.drop(items, from, o)
		return
	}
	d.dx, d.dy = px-d.x0, py-d.y0
	if !d.active {
		if d.dx*d.dx+d.dy*d.dy < queueDragStart*queueDragStart {
			return
		}
		d.active, d.epoch, d.box, d.over = true, d.epoch+1, st.rows, from
		d.slots = d.slots[:0]
		for _, e := range items {
			d.slots = append(d.slots, queueSlot{id: e.ID, rect: st.rects[e.ID]})
		}
		st.shift, st.settle = map[string]float32{}, nil
		// The rows start where they are; the next build moves them aside.
		c.After(0)
		return
	}
	a := d.slots[from].rect
	center := a.Y + a.H/2 + d.dy
	best := float32(math.Inf(1))
	for i, s := range d.slots {
		if far := float32(math.Abs(float64(s.rect.Y + s.rect.H/2 - center))); far < best {
			best, d.over = far, i
		}
	}
}

// sameSlots reports whether the queue is still the one the drag began on.
func sameSlots(slots []queueSlot, items []session.Entry) bool {
	if len(slots) != len(items) {
		return false
	}
	for i, s := range slots {
		if s.id != items[i].ID {
			return false
		}
	}
	return true
}

// shiftOf is how far row i stands aside while a drag holds the row from
// over the row over, as dnd-kit's verticalListSortingStrategy: the rows
// between make the dragged row's room on its other side.
func (d *queueDrag) shiftOf(i, from int) float32 {
	h := d.slots[from].rect.H
	switch {
	case i > from && i <= d.over:
		return -h
	case i < from && i >= d.over:
		return h
	}
	return 0
}

// drop ends a drag: the queue takes its new order at once, every row
// starting where it shows and gliding to its new place, and the move is
// written.
func (st *queueView) drop(items []session.Entry, from int, o queueProps) {
	d := st.drag
	st.drag = queueDrag{epoch: d.epoch}
	order := queueIDs(items)
	if d.over != from {
		order = arrayMove(order, from, d.over)
	}
	shown := map[string]float32{}
	height := map[string]float32{}
	for i, s := range d.slots {
		height[s.id] = s.rect.H
		shown[s.id] = s.rect.Y + st.shift[s.id]
		if i == from {
			shown[s.id] = s.rect.Y + d.dy
		}
	}
	st.settle, st.settleEpoch = map[string]float32{}, d.epoch
	y := d.slots[0].rect.Y
	for _, id := range order {
		if off := shown[id] - y; off != 0 {
			st.settle[id] = off
		}
		y += height[id]
	}
	st.shift = nil
	if d.over == from {
		return
	}
	st.order = order
	o.onReorder(items[from].ID, items[d.over].ID, func(err error) {
		if err != nil {
			st.order = nil
		}
	})
}

// queueIDs are the queue's messages, by id.
func queueIDs(items []session.Entry) []string {
	ids := make([]string, len(items))
	for i, e := range items {
		ids[i] = e.ID
	}
	return ids
}

// arrayMove moves ids[from] to to, the others between closing up, as
// dnd-kit's arrayMove.
func arrayMove(ids []string, from, to int) []string {
	out := append([]string(nil), ids...)
	id := out[from]
	out = append(out[:from], out[from+1:]...)
	out = append(out[:to], append([]string{id}, out[to:]...)...)
	return out
}

// queueImages are a queued message's pictures.
func queueImages(e session.Entry) []session.Item {
	var out []session.Item
	for _, it := range e.Items {
		switch it.Type {
		case "image":
			out = append(out, it)
		case "image_group":
			out = append(out, it.Images...)
		}
	}
	return out
}

// queueRow is a queued message, as Lody's MessageQueueRow: an inset tile,
// its fill fading in under the pointer, with its number (or grip), its
// pictures and two lines of its text, and its actions; or its editor.
func (a *App) queueRow(c *ui.Context, p *palette, st *queueView, o queueProps, items []session.Entry, i int, pointer func() (float32, float32)) {
	e := items[i]
	d := &st.drag
	editing := st.edit.id == e.ID
	dragging := d.active && d.id == e.ID
	if dragging {
		// Its place stays in the list while the row rides over the others.
		ui.Box(c.Key(e.ID + "/place")).FillWidth().Height(d.slots[i].rect.H).Shrink(0)
	}
	row := ui.Row(c.Key(e.ID)).FillWidth().AlignItems(ui.Start).Gap(8).Padding(6).Radius(5).Transition(queueRowFade)
	hovered := row.Hovered()
	switch {
	case dragging:
		r := d.slots[i].rect
		row.Absolute().Left(r.X-d.box.X).Top(r.Y-d.box.Y+d.dy).Width(r.W).Background(p.Composer).Opacity(0.95).
			Shadow(0, 4, 6, -1, ui.RGBA(0, 0, 0, 0.1))
	case editing || hovered && !d.active:
		row.Background(p.Hover)
	}
	if !dragging {
		var y float32
		if d.active {
			y = row.AnimateWith(fmt.Sprint("shift", d.epoch), d.shiftOf(i, indexOf(items, d.id)), queueSortTime, cssEase)
			st.shift[e.ID] = y
		} else if off, ok := st.settle[e.ID]; ok {
			k := row.Keyframes(fmt.Sprint("settle", st.settleEpoch), []ui.Keyframe{{Value: 0}, {At: queueSortTime, Value: 1, Ease: cssEase}})
			if y = off * (1 - k); k >= 1 {
				delete(st.settle, e.ID)
			}
		}
		if y != 0 {
			row.Translate(0, y)
		}
		if st.rects == nil {
			st.rects = map[string]ui.Rect{}
		}
		st.rects[e.ID] = row.LayoutBounds()
	}
	grip := hovered || row.FocusWithin() || dragging
	row.Children(func() {
		a.queueHandle(c, p, st, e, i, len(items) > 1 && !editing, grip, pointer)
		if editing {
			a.queueEditor(c, p, st, o, e)
			return
		}
		a.queueBody(c, p, st, o, e)
		a.queueRowActions(c, p, st, o, items, i)
	})
}

// indexOf is where the message id is in the queue, or -1.
func indexOf(items []session.Entry, id string) int {
	for i, e := range items {
		if e.ID == id {
			return i
		}
	}
	return -1
}

// queueHandle is a row's number, on its text's first line, which turns
// into a grip under the pointer that drags the row once the queue holds
// more than one, its tip after 300ms.
func (a *App) queueHandle(c *ui.Context, p *palette, st *queueView, e session.Entry, i int, reorder, grip bool, pointer func() (float32, float32)) {
	number := func(ink ui.Color) {
		ui.Text(c, fmt.Sprint(i+1)).FontSize(11).FontWeight(500).FontFeatures("tnum").TextColor(ink).SingleLine()
	}
	muted := p.Secondary.Alpha(0.7)
	if !reorder {
		ui.Box(c.Key("number")).Size(queueGutter, queueLine).Shrink(0).Center().Translate(0, 1).Children(func() { number(muted) })
		return
	}
	label := locales.MqDragToReorder()
	h := ui.ButtonBase(c.Key("grip")).Size(queueGutter, queueLine).Shrink(0).Radius(4).Center().Translate(0, 1).
		Label(label).Cursor(ui.CursorGrab).Transition(twFade)
	// Held, wherever the pointer has gone since: a press here drags the
	// row and clicks nothing.
	_, _, pressed := h.Dragged()
	d := &st.drag
	if pressed && d.id == "" {
		x, y := pointer()
		*d = queueDrag{id: e.ID, x0: x, y0: y, epoch: d.epoch}
	}
	ink := muted
	if d.id == e.ID {
		h.Cursor(ui.CursorGrabbing)
		if !pressed && !d.released {
			if d.active {
				// Dropped in the next build, before any row is.
				d.released = true
				c.After(0)
			} else {
				*d = queueDrag{epoch: d.epoch}
			}
		}
	}
	if h.Hovered() {
		h.Background(p.Hover)
		ink = p.Text
	}
	withTipDelay(300*time.Millisecond, func() { tip(c, p, h, label) })
	h.Children(func() {
		if grip {
			ui.Icon(c, iconGripVertical).Size(12, 12).TextColor(ink)
			return
		}
		number(ink)
	})
}

// queueBody is a queued message's pictures, three at most, and its text,
// two lines of it, one on a narrow sheet.
func (a *App) queueBody(c *ui.Context, p *palette, st *queueView, o queueProps, e session.Entry) {
	ui.Row(c.Key("body")).Grow(1).MinWidth(0).AlignItems(ui.Start).Gap(6).Children(func() {
		if images := queueImages(e); len(images) > 0 {
			a.queueThumbs(c, p, o.sessionID, images, 18)
		}
		text := e.Text()
		if text == "" {
			text = locales.MqImageOnly()
		}
		lines := 2
		if st.compact {
			lines = 1
		}
		ui.Text(c.Key("text"), text).FontSize(sizeSub).LineHeight(queueLeading).TextColor(p.Text.Alpha(0.86)).MaxLines(lines).Grow(1).MinWidth(0)
	})
}

// queueThumbs are a queued message's pictures, as Lody's
// QueuedImagePreview: small rounded tiles named in their tips, each a
// button that opens the picture in the viewer, which loads it at its size
// then; and how many more there are.
func (a *App) queueThumbs(c *ui.Context, p *palette, sessionID string, images []session.Item, size float32) {
	const most = 3
	ui.Row(c.Key("thumbs")).Gap(2).AlignItems(ui.Center).Shrink(0).Padding(1, 0, 0, 0).Children(func() {
		for i, it := range images {
			if i == most {
				ui.Text(c, fmt.Sprintf("+%d", len(images)-most)).FontSize(10).FontFeatures("tnum").TextColor(p.Secondary.Alpha(0.7)).Margin(0, 0, 0, 2)
				break
			}
			label := firstNonEmpty(it.FileName, locales.MqUploadedImage())
			img := a.sessionBitmap(sessionID, it)
			key := fmt.Sprintf("thumb/%d/%s", i, it.ImageID)
			// One whose thumbnail can't load opens nothing.
			failed := img == nil || img.failed
			t := ui.ButtonBase(c.Key(key)).Size(size, size).Shrink(0).Radius(4).Border(1, p.Border.Alpha(0.4)).Background(p.Canvas.Alpha(0.6)).Clip().
				Label(locales.MqPreviewImage(label)).Disabled(failed)
			if !failed {
				t.Cursor(ui.CursorPointer) // Lody's zoom-in, which has no shape here
			}
			owner := "queue/" + sessionID + "/" + key
			a.holdViewer(owner)
			t.OnClick(func() {
				a.openViewer(it.ImageID, owner, func(string) []viewerItem {
					return []viewerItem{{key: it.ImageID, fileName: it.FileName, picture: func() *remoteImage { return a.queuedPicture(sessionID, it) }}}
				})
			})
			withTipDelay(300*time.Millisecond, func() { tip(c, p, t, label) })
			t.Children(func() {
				switch {
				case img != nil && img.bitmap != nil:
					ui.Image(c.Key("bitmap"), img.bitmap).Size(size, size).Fit(ui.Cover)
				case img != nil && img.loading:
					skeleton(c, p, key, size).Width(size).Radius(0)
				default:
					ui.Box(c).Size(size, size).Center().Children(func() {
						ui.Icon(c, iconImage).Size(size/2, size/2).TextColor(p.Secondary)
					})
				}
			})
		}
	})
}

// queueAction is Lody's IconAction: a 22-DIP square holding a 14-DIP icon
// in the muted ink, its fill fading in under the pointer as Tailwind's
// transition-colors, a destructive one turning red, and its tip after
// 300ms above it.
func queueAction(c *ui.Context, p *palette, key string, icon *ui.SVG, label string, destructive, disabled bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Size(22, 22).Shrink(0).Radius(5).Center().Label(label).Disabled(disabled).Transition(twFade)
	ink := p.Secondary
	if disabled {
		b.Opacity(0.5)
	} else {
		b.Cursor(ui.CursorPointer)
		if b.Hovered() {
			b.Background(p.Text.Alpha(0.07))
			if destructive {
				ink = p.Danger
			}
		}
	}
	withTipDelay(300*time.Millisecond, func() { tip(c, p, b, label) })
	b.Children(func() { ui.Icon(c, icon).Size(14, 14).TextColor(ink) })
	return b
}

// queueMore is the More button a row's actions fold into on a narrow
// sheet, opening them as a menu at its end.
func (a *App) queueMore(c *ui.Context, p *palette, key string, fn func(m *lodyMenu)) {
	more := ui.ButtonBase(c.Key("more")).Size(22, queueLine).Shrink(0).Radius(5).Center().Cursor(ui.CursorPointer).
		Label(locales.MqMore()).Transition(twFade)
	if more.Hovered() {
		more.Background(selectedFill(p))
	}
	more.Children(func() { ui.Icon(c, iconMore).Size(14, 14).TextColor(p.Secondary) })
	lodyMenuButton(c, p, more, &a.rowMenu(key).menuState, 180, 290, 32, fn)
}

// queueRowActions end a queued row: Steer, while a reply runs and this
// message can steer it, then edit and remove; on a narrow sheet, one More
// button with Steer, Move to top, Edit and Delete.
func (a *App) queueRowActions(c *ui.Context, p *palette, st *queueView, o queueProps, items []session.Entry, i int) {
	e := items[i]
	steer := o.showSteer && (i == 0 || o.nativeSteer) && !e.Steer
	ui.Row(c.Key("actions")).Height(queueLine).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
		if st.compact {
			a.queueMore(c, p, "mq/"+e.ID, func(m *lodyMenu) {
				if steer && m.item("steer", iconSend, locales.Steer(), menuItem{}) {
					o.onSteer(e.ID)
				}
				if i > 0 && m.item("top", iconArrowUpToLine, locales.MqMoveToTop(), menuItem{}) {
					st.moveToTop(o, items, i)
				}
				if m.item("edit", iconPencil, locales.MqEdit(), menuItem{}) {
					st.startEdit(o, e)
				}
				if m.item("delete", iconTrash, locales.MqDelete(), menuItem{destructive: true}) {
					o.onRemove(e.ID)
				}
			})
			return
		}
		if steer {
			s := ui.ButtonBase(c.Key("steer")).Height(20).Padding(0, 8).Radius(10).Center().Shrink(0).Cursor(ui.CursorPointer).
				Label(locales.MqSteerHint()).Transition(queueRowFade).OnClick(func() { o.onSteer(e.ID) })
			fill := p.Text.Alpha(0.06)
			if s.Hovered() {
				fill = selectedFill(p)
			}
			s.Background(fill).Children(func() {
				ui.Text(c, locales.Steer()).FontSize(sizeCaption).FontWeight(500).LetterSpacing(-0.11).TextColor(p.Secondary).SingleLine()
			})
		}
		queueAction(c, p, "edit", iconPencil, locales.MqEditMessage(), false, false).OnClick(func() { st.startEdit(o, e) })
		queueAction(c, p, "remove", iconX, locales.MqRemove(), true, false).OnClick(func() { o.onRemove(e.ID) })
	})
}

// queueEditor is a row being edited: its text in a three-line field
// whose edge lights while it has the focus, and a confirm button. Enter,
// the button, Escape and clicking away all commit; Shift-Enter breaks the
// line. Moving to the header keeps the draft.
func (a *App) queueEditor(c *ui.Context, p *palette, st *queueView, o queueProps, e session.Entry) {
	ed := &st.edit
	ui.Column(c.Key("editor")).Grow(1).MinWidth(0).Gap(4).Children(func() {
		box := ui.Column(c.Key("edit-box")).FillWidth().Radius(6).Background(p.Composer).Transition(twFade)
		edge := p.Border.Alpha(0.6)
		if box.FocusWithin() {
			edge = p.Primary
			box.Shadow(0, 0, 0, 2, p.Primary.Alpha(0.2))
		}
		box.Border(1, edge).Children(func() {
			field := ui.TextAreaBase(c.Key("field"), &ed.text).Lines(3, 3).FontSize(sizeSub).LineHeight(queueLeading).TextColor(p.Text).
				Padding(6, 8, 0, 8).FillWidth().Label(locales.MqEditMessage()).Bind(&ed.field).Disabled(ed.pending).
				OnTextKey(func(k ui.TextKey) bool {
					switch {
					case k.Key == ui.KeyEscape, k.Key == ui.KeyEnter && k.Mods&ui.Shift == 0:
						ed.text = k.Text()
						st.commitEdit(o, e)
						return true
					}
					return false
				})
			focused := field.Focused()
			if ed.focus && !ed.pending {
				ed.focus = false
				ed.field.Focus(c)
			}
			if ed.focused && !focused && !st.headFocus {
				st.commitEdit(o, e)
			}
			ed.focused = focused
			ui.Row(c).FillWidth().Justify(ui.End).Padding(0, 4, 4, 4).Children(func() {
				queueAction(c, p, "save", iconCheck, locales.MqSaveEdit(), false, ed.pending).OnClick(func() { st.commitEdit(o, e) })
			})
		})
		if images := queueImages(e); len(images) > 0 {
			a.queueThumbs(c, p, o.sessionID, images, 20)
		}
	})
}

// scheduledRow is a scheduled message, laid out as a queued row without a
// number, drag or steer, as Lody's ScheduledQueueRow: its send time leads
// the text and opens the time editor, red with an alert once it failed to
// go; details and cancel end the row, in More on a narrow sheet.
func (a *App) scheduledRow(c *ui.Context, p *palette, st *queueView, o queueProps, s scheduledItem) {
	when := capitalize(upcomingText(s.at, time.Now(), s.loc))
	label := when
	if s.failed {
		label += " · " + locales.SmNotSent()
	}
	label += " · " + locales.SmEditTime()
	row := ui.Row(c.Key("sched/" + s.id)).FillWidth().AlignItems(ui.Start).Gap(8).Padding(6).Radius(5).Transition(queueRowFade).Description(s.title)
	if row.Hovered() && !st.drag.active {
		row.Background(p.Hover)
	}
	row.Children(func() {
		ui.Box(c).Size(queueGutter, queueLine).Shrink(0)
		ui.Row(c.Key("body")).Grow(1).MinWidth(0).AlignItems(ui.Start).Gap(4).Children(func() {
			t := ui.ButtonBase(c.Key("time")).Height(queueLine).Padding(0, 4).Margin(0, 0, 0, -4).Radius(5).Gap(3).AlignItems(ui.Center).Shrink(0).
				Cursor(ui.CursorPointer).Label(label).Transition(queueRowFade).OnClick(func() { o.onEditScheduledTime(s) })
			if t.Hovered() {
				t.Background(selectedFill(p))
			}
			ink := p.Secondary
			if s.failed {
				ink = p.Danger
			}
			t.Children(func() {
				if s.failed {
					ui.Icon(c, iconAlertBold).Size(12, 12).TextColor(ink)
				}
				ui.Text(c, when).FontSize(sizeSub).FontFeatures("tnum").TextColor(ink).SingleLine()
			})
			lines := 2
			if st.compact {
				lines = 1
			}
			title := ui.Text(c.Key("title"), s.title).FontSize(sizeSub).LineHeight(queueLeading).TextColor(p.Text.Alpha(0.86)).MaxLines(lines).Grow(1).MinWidth(0)
			tip(c, p, title, s.title)
		})
		ui.Row(c.Key("actions")).Height(queueLine).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
			if st.compact {
				a.queueMore(c, p, "mq-sched/"+s.id, func(m *lodyMenu) {
					if m.item("time", iconClock, locales.SmEditTime(), menuItem{}) {
						o.onEditScheduledTime(s)
					}
					if m.item("edit", iconPencil, locales.MqEdit(), menuItem{}) {
						o.onOpenScheduled(s)
					}
					if m.item("cancel", iconTrash, locales.SmCancel(), menuItem{destructive: true}) {
						o.onCancelScheduled(s)
					}
				})
				return
			}
			queueAction(c, p, "edit", iconPencil, locales.MqEdit(), false, false).OnClick(func() { o.onOpenScheduled(s) })
			queueAction(c, p, "cancel", iconX, locales.SmCancel(), true, false).OnClick(func() { o.onCancelScheduled(s) })
		})
	})
}

// capitalize starts a label with a capital, as a relative time ("tomorrow
// 9:00 AM") opening a line does.
func capitalize(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}
