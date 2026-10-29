package app

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// lodyMenu is a menu drawn as @lody/ui's: a raised surface inset 4px, its
// rows 16px icons in the hint ink before 13px labels, highlighted at once
// under the pointer, and submenus that open beside their row as the
// pointer reaches it. Rows report a click as they are built, as ui.Menu's
// do.
type lodyMenu struct {
	c      *ui.Context
	p      *palette
	open   *bool   // the whole menu's
	sub    *string // the submenu open, by key
	nested bool    // a submenu's rows: hovering them keeps it open
	rowH   float32
}

// menuState is a drawn menu's: whether it shows, and its open submenu.
type menuState struct {
	open bool
	sub  string
}

// lodyMenuPanel styles a menu's surface: min and max width, as the menu
// asks.
func lodyMenuPanel(c *ui.Context, p *palette, key string, minW, maxW float32) ui.Element {
	return ui.Column(c.Key(key)).Padding(4).Gap(0).MinWidth(minW).MaxWidth(maxW).Radius(14).Background(raisedGround(p)).
		Border(0.5, p.CardRim).Shadow(0, 12, 32, -6, ui.RGBA(28, 28, 28, 0.16))
}

// menuHighlight is the fill under the row the pointer is on.
func menuHighlight(p *palette) ui.Color { return raisedGround(p).Mix(p.Text, 0.06) }

// menuItem is a row's options.
type menuItem struct {
	disabled    bool
	destructive bool
	keepOpen    bool // the row acts in place, as a switch
	tip         string
	detail      string // a second line, under the label
	// value is what a submenu's row is set to, after its label in the
	// secondary ink, as @lody/ui's value trigger.
	value string
	end   func() // what ends the row: a copy mark, a switch, a status
	// indicator draws the icon in the label's ink: a tick or a radio dot
	// saying what is chosen, as @lody/ui's indicator, not a hint.
	indicator bool
	// bare leaves out the icon's box, as @lody/ui's row with neither an
	// icon nor an inset.
	bare bool
	// describe says what the row's value does, in Lody's tooltip beside the
	// row once the pointer has rested on it 300ms, as its permission list
	// explains each mode.
	describe string
	// warn draws the icon in the warning ink, as Lody's amber shield on a
	// mode that skips approval.
	warn bool
	// glyph is the icon's size in its 16px box, where smaller: Lody's
	// permission list draws 14px marks.
	glyph float32
}

// describeDelay is how long the pointer rests on a row before its
// description shows, as Lody's permission list waits.
const describeDelay = 300 * time.Millisecond

// describeWidth is the widest a row's description runs before it wraps,
// as Lody's (max-w-72).
const describeWidth = 288

// row lays out a row: its icon box, label and end, highlighted while the
// pointer is on it.
func (m *lodyMenu) row(key string, icon *ui.SVG, label string, o menuItem, extra func()) (ui.Element, bool) {
	c, p := m.c, m.p
	r := ui.ButtonBase(c.Key(key)).FillWidth().MinHeight(m.rowH).Padding(6, 8).Gap(8).Radius(10).AlignItems(ui.Center).Label(label).Disabled(o.disabled)
	hovered := r.Hovered() && !o.disabled
	if hovered {
		bg := menuHighlight(p)
		if o.destructive {
			bg = raisedGround(p).Mix(p.Danger, 0.12)
		}
		r.Background(bg)
	}
	if o.disabled {
		r.Opacity(0.5)
	} else {
		r.Cursor(ui.CursorPointer)
	}
	if o.tip != "" {
		lodyTooltip(c, p, r, o.tip)
	}
	if o.describe != "" {
		r.Description(o.describe)
		withTipDelay(describeDelay, func() { lodyTooltipBeside(c, p, r, o.describe, describeWidth) })
	}
	ink, hint := p.Text, p.Tertiary
	if o.indicator {
		hint = p.Text
	}
	if o.warn {
		hint = p.Warning
	}
	if o.destructive {
		ink, hint = p.Danger, p.Danger
	}
	glyph := float32(16)
	if o.glyph > 0 {
		glyph = o.glyph
	}
	r.Children(func() {
		if !o.bare {
			ui.Box(c).Size(16, 16).Shrink(0).Center().Children(func() {
				if icon != nil {
					ui.Icon(c, icon).Size(glyph, glyph).TextColor(hint)
				}
			})
		}
		if o.detail == "" {
			ui.Text(c, label).FontSize(sizeSub).TextColor(ink).SingleLine().Grow(1).MinWidth(0)
		} else {
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, label).FontSize(sizeSub).TextColor(ink).SingleLine()
				ui.Text(c, o.detail).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
			})
		}
		if o.value != "" {
			ui.Text(c, o.value).FontSize(sizeSub).TextColor(p.Secondary).SingleLine().MaxWidth(160).Margin(0, 0, 0, 8)
		}
		if extra != nil {
			extra()
		}
		if o.end != nil {
			o.end()
		}
	})
	return r, hovered
}

// item is a command; it reports whether it was chosen, closing the menu
// unless it acts in place.
func (m *lodyMenu) item(key string, icon *ui.SVG, label string, o menuItem) bool {
	r, hovered := m.row(key, icon, label, o, nil)
	if hovered && !m.nested {
		*m.sub = ""
	}
	if o.disabled || !r.Clicked() {
		return false
	}
	if !o.keepOpen {
		*m.open, *m.sub = false, ""
	}
	return true
}

// static is a row that only tells: no highlight, no action.
func (m *lodyMenu) static(key string, icon *ui.SVG, label string, end func()) ui.Element {
	c, p := m.c, m.p
	r := ui.Row(c.Key(key)).FillWidth().MinHeight(m.rowH).Padding(6, 8).Gap(8).AlignItems(ui.Center)
	r.Children(func() {
		ui.Box(c).Size(16, 16).Shrink(0).Center().Children(func() {
			if icon != nil {
				ui.Icon(c, icon).Size(16, 16).TextColor(p.Tertiary)
			}
		})
		ui.Text(c, label).FontSize(sizeSub).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
		if end != nil {
			end()
		}
	})
	if r.Hovered() && !m.nested {
		*m.sub = ""
	}
	return r
}

// sep is the rule between groups of rows, edge to edge.
func (m *lodyMenu) sep() {
	ui.Box(m.c).Height(1).Margin(4, -4).Background(m.p.Border.Alpha(0.7)).Shrink(0)
}

// submenu is a row that opens fn beside it once the pointer reaches it.
func (m *lodyMenu) submenu(key string, icon *ui.SVG, label string, o menuItem, minW float32, fn func(sm *lodyMenu)) {
	c, p := m.c, m.p
	openNow := *m.sub == key
	r, hovered := m.row(key, icon, label, o, func() {
		ui.Icon(c, iconChevronRight).Size(14, 14).TextColor(p.Tertiary).Margin(0, -4, 0, 0)
	})
	if openNow && !hovered {
		r.Background(raisedGround(p).Mix(p.Text, 0.03))
	}
	if hovered || r.Clicked() && !o.disabled {
		*m.sub = key
	}
	if *m.sub != key || o.disabled {
		return
	}
	sm := &lodyMenu{c: c, p: p, open: m.open, sub: m.sub, nested: true, rowH: 28}
	ui.Overlay(c, func() {
		panel := lodyMenuPanel(c, p, "sub/"+key, minW, 290).AttachTo(r, ui.AnchorTopEnd, ui.AnchorTopStart).Margin(-4, 0, 0, 4)
		// It comes from 4px further out on the side it opened to, and goes
		// back there as it fades.
		away := float32(4)
		if pb, rb := panel.Bounds(), r.Bounds(); pb.W > 0 && pb.X+pb.W <= rb.X+1 {
			away = -4 // it opened to the left of its row
		}
		panel.Transition(ui.ElementTransition{Colors: true, Duration: popTime, Ease: easeStandard, Enter: &ui.Motion{X: away}, Exit: &ui.Motion{X: away}})
		panel.Children(func() { fn(sm) })
	})
}

// lodyMenuButton is a button that opens a drawn menu below it, its end
// aligned with the button's, rising 4px into place as Lody's popups do.
func lodyMenuButton(c *ui.Context, p *palette, button ui.Element, st *menuState, minW, maxW, rowH float32, fn func(m *lodyMenu)) {
	button.OnClick(func() { st.open, st.sub = !st.open, "" })
	if st.open {
		button.Background(p.Hover)
	}
	ui.PopoverBase(c, button, &st.open, func(panel ui.Element) {
		panel.Padding(4).MinWidth(minW).MaxWidth(maxW).Radius(14).Background(raisedGround(p)).
			Border(0.5, p.CardRim).Shadow(0, 12, 32, -6, ui.RGBA(28, 28, 28, 0.16)).
			AttachTo(button, ui.AnchorBottomEnd, ui.AnchorTopEnd).Margin(4, 0, 0, 0).
			Transition(ui.ElementTransition{Colors: true, Duration: popTime, Ease: easeStandard, Exit: &ui.Motion{Y: 4}})
		if k := panel.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: popTime, Value: 1, Ease: easeStandard}}); k < 1 {
			panel.Translate(0, 4*(1-k)).Opacity(k)
		}
		m := &lodyMenu{c: c, p: p, open: &st.open, sub: &st.sub, rowH: rowH}
		panel.Children(func() { fn(m) })
	})
	if !st.open {
		st.sub = ""
	}
}

// contextMenu is a drawn context menu's state: whether it shows, its open
// submenu, and where it was asked for, in its element.
type contextMenu struct {
	menuState
	at ui.Point
}

// lodyContextMenu opens fn as a drawn menu at the pointer when el is
// right-clicked (or Control-clicked), rising 4px into place as Lody's
// context menus do.
func lodyContextMenu(c *ui.Context, p *palette, el ui.Element, st *contextMenu, rowH float32, fn func(m *lodyMenu)) {
	lodyContextMenuWith(c, p, el, st, rowH, nil, fn)
}

// lodyContextMenuWith is lodyContextMenu for an element that takes other
// input too: other has what the menu leaves, as an element has one input
// handler.
func lodyContextMenuWith(c *ui.Context, p *palette, el ui.Element, st *contextMenu, rowH float32, other func(ev ui.InputEvent) bool, fn func(m *lodyMenu)) {
	el.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind == ui.InputPointerDown && (ev.Button == 1 || ev.Button == 0 && ev.Mods&ui.Ctrl != 0) {
			st.open, st.sub, st.at = true, "", ui.Point{X: ev.X, Y: ev.Y}
			return true
		}
		return other != nil && other(ev)
	})
	if !st.open {
		st.sub = ""
		return
	}
	b := el.Bounds()
	var anchor ui.Element
	ui.Overlay(c, func() {
		anchor = ui.Box(c.Key("ctx-anchor")).Absolute().Left(b.X+st.at.X).Top(b.Y+st.at.Y).Size(1, 1).PassThrough()
	})
	ui.PopoverBase(c, anchor, &st.open, func(panel ui.Element) {
		panel.Padding(4).MinWidth(200).MaxWidth(290).Radius(14).Background(raisedGround(p)).
			Border(0.5, p.CardRim).Shadow(0, 12, 32, -6, ui.RGBA(28, 28, 28, 0.16)).
			AttachTo(anchor, ui.AnchorTopStart, ui.AnchorTopStart).
			Transition(ui.ElementTransition{Colors: true, Duration: popTime, Ease: easeStandard, Exit: &ui.Motion{Y: 4}})
		if k := panel.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: popTime, Value: 1, Ease: easeStandard}}); k < 1 {
			panel.Translate(0, 4*(1-k)).Opacity(k)
		}
		m := &lodyMenu{c: c, p: p, open: &st.open, sub: &st.sub, rowH: rowH}
		panel.Children(func() { fn(m) })
	})
}
