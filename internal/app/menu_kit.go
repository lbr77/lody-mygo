package app

import "github.com/egoist/mygo/ui"

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
	end         func() // what ends the row: a copy mark, a switch, a status
}

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
	ink, hint := p.Text, p.Tertiary
	if o.destructive {
		ink, hint = p.Danger, p.Danger
	}
	r.Children(func() {
		ui.Box(c).Size(16, 16).Shrink(0).Center().Children(func() {
			if icon != nil {
				ui.Icon(c, icon).Size(16, 16).TextColor(hint)
			}
		})
		if o.detail == "" {
			ui.Text(c, label).FontSize(sizeSub).TextColor(ink).SingleLine().Grow(1).MinWidth(0)
		} else {
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, label).FontSize(sizeSub).TextColor(ink).SingleLine()
				ui.Text(c, o.detail).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
			})
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
	el.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind == ui.InputPointerDown && (ev.Button == 1 || ev.Button == 0 && ev.Mods&ui.Ctrl != 0) {
			st.open, st.sub, st.at = true, "", ui.Point{X: ev.X, Y: ev.Y}
			return true
		}
		return false
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
