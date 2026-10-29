package app

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// The projects of several reorder by dragging, as Lody's sidebar sorts
// them (projectGroupOrderAtom, session-list.tsx): each one's grip, after
// its hover controls, drags it, with its conversations, among the others
// under the same machine once the press has moved 6 DIPs; it follows the
// pointer at 60% opacity while those it passes slide aside, and dropped,
// each glides into its new place, on dnd-kit's 250ms ease. The order is
// kept on this device by workspace; a project it doesn't name follows
// those it does, and one deleted keeps its place after them.

// groupOrderFile keeps the order, as Lody's localStorage key
// lody-sidebar-project-group-order-by-workspace.
const groupOrderFile = "project-group-order.json"

const (
	groupDragStart = 6 // DIPs a press moves before it drags, as dnd-kit's PointerSensor
	groupSortTime  = 250 * time.Millisecond
)

// sideGroupDrag is a project of several held by its grip.
type sideGroupDrag struct {
	id, machine string
	x0, y0      float32 // where the press began, in the window
	dx, dy      float32 // how far it moved since
	active      bool
	released    bool // let go: dropped in the next build
	over        int  // the place it would drop in
	slots       []groupSlot
	epoch       int // which drag this is
	// pointer is where the pointer is in the window, while the sidebar
	// builds; nil otherwise.
	pointer func() (float32, float32)
	// rects are the rows of the projects of several as last laid out, by
	// row; shift how far each project is aside while one is dragged, and
	// settle how far each starts from its place once one was dropped.
	rects       map[string]ui.Rect
	shift       map[string]float32
	settle      map[string]float32
	settleEpoch int
}

// groupSlot is a project of several's place, its row and its
// conversations', as a drag began.
type groupSlot struct {
	id     string
	top, h float32
}

// groupOrder is the workspace's order of projects of several, as dragged.
func (a *App) groupOrder() []string {
	if !a.groupOrderRead {
		a.groupOrderRead = true
		readJSON(groupOrderFile, &a.groupOrders)
	}
	return a.groupOrders[a.workspaceID]
}

// moveGroups puts ids in that order, as Lody's handleMoveProjectGroup:
// the projects it names first, then the rest of the order as it was.
func (a *App) moveGroups(ids []string) {
	listed := map[string]bool{}
	for _, id := range ids {
		listed[id] = true
	}
	order := append([]string(nil), ids...)
	for _, id := range a.groupOrder() {
		if !listed[id] {
			order = append(order, id)
		}
	}
	if a.groupOrders == nil {
		a.groupOrders = map[string][]string{}
	}
	a.groupOrders[a.workspaceID] = order
	writeJSON(groupOrderFile, a.groupOrders)
}

// orderedGroups are the workspace's projects of several in the order
// dragged, those it doesn't name after them as they come.
func (a *App) orderedGroups() []catalog.ProjectGroup {
	order := a.groupOrder()
	rank := map[string]int{}
	for i, id := range order {
		rank[id] = i
	}
	at := map[string]int{}
	for i, g := range a.catalog.Groups {
		at[g.ID] = len(order) + i
		if r, ok := rank[g.ID]; ok {
			at[g.ID] = r
		}
	}
	groups := append([]catalog.ProjectGroup(nil), a.catalog.Groups...)
	sort.SliceStable(groups, func(i, j int) bool { return at[groups[i].ID] < at[groups[j].ID] })
	return groups
}

// groupBucket is the projects of several under machine that reorder, as
// the sidebar lists them.
func groupBucket(rows []sideRow, machine string) []string {
	var ids []string
	for _, r := range rows {
		if r.kind == rowGroup && r.machine == machine && r.reorder {
			ids = append(ids, r.project)
		}
	}
	return ids
}

// trackGroupDrag follows the drag under way before the rows are built:
// how far it moved, whether it began, and the place it is over, by the
// nearest center, as dnd-kit's closestCenter. One let go drops here.
func (a *App) trackGroupDrag(c *ui.Context, rows []sideRow) {
	d := &a.groupDrag
	if d.id == "" || d.pointer == nil {
		return
	}
	bucket := groupBucket(rows, d.machine)
	from := slices.Index(bucket, d.id)
	if from < 0 || d.active && !sameGroupSlots(d.slots, bucket) {
		a.endGroupDrag()
		return
	}
	if d.released {
		a.dropGroup(bucket, from)
		return
	}
	x, y := d.pointer()
	d.dx, d.dy = x-d.x0, y-d.y0
	if !d.active {
		if d.dx*d.dx+d.dy*d.dy < groupDragStart*groupDragStart {
			return
		}
		d.active, d.epoch, d.over = true, d.epoch+1, from
		d.slots = groupSlots(rows, bucket, d.rects)
		d.shift, d.settle = map[string]float32{}, nil
		// The rows start where they are; the next build moves them.
		c.After(0)
		return
	}
	s := d.slots[from]
	center := s.top + s.h/2 + d.dy
	best := float32(math.Inf(1))
	for i, o := range d.slots {
		if far := abs32(o.top + o.h/2 - center); far < best {
			best, d.over = far, i
		}
	}
}

// endGroupDrag forgets the drag, keeping what the next one counts from
// and what still settles.
func (a *App) endGroupDrag() {
	d := &a.groupDrag
	*d = sideGroupDrag{epoch: d.epoch, pointer: d.pointer, rects: d.rects, settle: d.settle, settleEpoch: d.settleEpoch}
}

// groupSlots are the places of the projects in bucket: each from its
// row's top to the next one's, its conversations with it.
func groupSlots(rows []sideRow, bucket []string, rects map[string]ui.Rect) []groupSlot {
	slots := make([]groupSlot, len(bucket))
	for i, id := range bucket {
		top, bottom := float32(math.Inf(1)), float32(math.Inf(-1))
		for _, r := range rows {
			if rb, ok := rects[r.key]; ok && r.group == "group:"+id {
				top, bottom = min(top, rb.Y), max(bottom, rb.Y+rb.H)
			}
		}
		if top > bottom {
			top, bottom = 0, 0
		}
		// The list's 1-DIP gap after it is its too.
		slots[i] = groupSlot{id: id, top: top, h: bottom - top + 1}
	}
	for i := 0; i+1 < len(slots); i++ {
		if next := slots[i+1].top - slots[i].top; next > 0 {
			slots[i].h = next
		}
	}
	return slots
}

// sameGroupSlots reports whether the projects are still the ones the drag
// began with.
func sameGroupSlots(slots []groupSlot, bucket []string) bool {
	if len(slots) != len(bucket) {
		return false
	}
	for i, s := range slots {
		if s.id != bucket[i] {
			return false
		}
	}
	return true
}

// shiftOf is how far the project in place i stands aside while the one
// from place from is over d.over, as dnd-kit's
// verticalListSortingStrategy: those between make its room on its other
// side.
func (d *sideGroupDrag) shiftOf(i, from int) float32 {
	h := d.slots[from].h
	switch {
	case i > from && i <= d.over:
		return -h
	case i < from && i >= d.over:
		return h
	}
	return 0
}

// dropGroup ends a drag: the projects take their new order at once, each
// starting where it shows and gliding into its new place, and the order
// is kept.
func (a *App) dropGroup(bucket []string, from int) {
	d := a.groupDrag
	a.endGroupDrag()
	order := append([]string(nil), bucket...)
	if d.over != from {
		order = arrayMove(order, from, d.over)
	}
	shown, height := map[string]float32{}, map[string]float32{}
	for i, s := range d.slots {
		height[s.id] = s.h
		shown[s.id] = s.top + d.shift[s.id]
		if i == from {
			shown[s.id] = s.top + d.dy
		}
	}
	settle := map[string]float32{}
	y := d.slots[0].top
	for _, id := range order {
		if off := shown[id] - y; off != 0 {
			settle[id] = off
		}
		y += height[id]
	}
	a.groupDrag.settle, a.groupDrag.settleEpoch = settle, d.epoch
	if d.over != from {
		a.moveGroups(order)
	}
}

// groupBlockMotion moves a row of a project of several as its project is
// dragged, stands aside or settles, and notes where it was laid out.
func (a *App) groupBlockMotion(row ui.Element, r sideRow) {
	id, ok := strings.CutPrefix(r.group, "group:")
	if !ok {
		return
	}
	d := &a.groupDrag
	if d.rects == nil {
		d.rects = map[string]ui.Rect{}
	}
	d.rects[r.key] = row.LayoutBounds()
	var y float32
	switch i := slices.IndexFunc(d.slots, func(s groupSlot) bool { return s.id == id }); {
	case d.active && d.id == id:
		y = d.dy
		row.Opacity(0.6)
	case d.active && i >= 0:
		from := slices.IndexFunc(d.slots, func(s groupSlot) bool { return s.id == d.id })
		y = row.AnimateWith(fmt.Sprint("group-shift", d.epoch), d.shiftOf(i, from), groupSortTime, cssEase)
		d.shift[id] = y
	default:
		if off, ok := d.settle[id]; ok {
			k := row.Keyframes(fmt.Sprint("group-settle", d.settleEpoch), []ui.Keyframe{{Value: 0}, {At: groupSortTime, Value: 1, Ease: cssEase}})
			if y = off * (1 - k); k >= 1 {
				delete(d.settle, id)
			}
		}
	}
	if y != 0 {
		row.Translate(0, y)
	}
}

// groupGrip is a project of several's drag handle, Lucide's grip, last of
// its hover controls: a press on it drags the project and clicks nothing.
func (a *App) groupGrip(c *ui.Context, p *palette, r sideRow, opacity float32, live bool) {
	d := &a.groupDrag
	h := ui.ButtonBase(c.Key("grip")).Size(20, 20).Radius(4).Label(locales.SidebarGroupReorder()).Opacity(opacity).Cursor(ui.CursorGrab).
		Transition(ui.ElementTransition{Colors: true, Duration: 100 * time.Millisecond, Ease: twEase})
	ink := p.Secondary.Alpha(0.7)
	if live {
		_, _, pressed := h.Dragged()
		if pressed && d.id == "" && d.pointer != nil {
			x, y := d.pointer()
			*d = sideGroupDrag{id: r.project, machine: r.machine, x0: x, y0: y, epoch: d.epoch, pointer: d.pointer, rects: d.rects, settle: d.settle, settleEpoch: d.settleEpoch}
		}
		if d.id == r.project {
			h.Cursor(ui.CursorGrabbing)
			if !pressed && !d.released {
				if d.active {
					// Dropped in the next build, before any row is.
					d.released = true
					c.After(0)
				} else {
					a.endGroupDrag()
				}
			}
		}
		if h.Hovered() || d.id == r.project {
			h.Background(p.Text.Alpha(0.06))
			ink = p.Text
		}
	} else {
		h.PassThrough()
	}
	ink = h.AnimateColor("ink", ink, ui.Animation{Duration: 100 * time.Millisecond, Ease: twEase})
	h.Children(func() { ui.Icon(c, iconGripVertical).Size(14, 14).TextColor(ink) })
}
