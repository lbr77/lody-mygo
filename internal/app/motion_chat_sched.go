package app

import "github.com/egoist/mygo/ui"

// iconRadioDot is @lody/ui's DotGlyph: the mark of a menu's chosen radio
// row, a 6px dot in the row's 16px leading box.
var iconRadioDot = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><circle cx="8" cy="8" r="3" fill="currentColor"/></svg>`))

// lodyMenuButtonStart is lodyMenuButton for a menu Lody aligns to its
// trigger's start (Menu.Content align="start"): it opens below the button,
// their starts lined up, rising 4px into place.
func lodyMenuButtonStart(c *ui.Context, p *palette, button ui.Element, st *menuState, minW, maxW, rowH float32, fn func(m *lodyMenu)) {
	button.OnClick(func() { st.open, st.sub = !st.open, "" })
	ui.PopoverBase(c, button, &st.open, func(panel ui.Element) {
		panel.Padding(4).MinWidth(minW).MaxWidth(maxW).Radius(14).Background(raisedGround(p)).
			Border(0.5, p.CardRim).Shadow(0, 12, 32, -6, ui.RGBA(28, 28, 28, 0.16)).
			AttachTo(button, ui.AnchorBottomStart, ui.AnchorTopStart).Margin(4, 0, 0, 0).
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

// radio is a Menu.RadioItem: the dot marks the one chosen, and count, if
// set, ends the row. It reports whether the row was chosen.
func (m *lodyMenu) radio(key, label string, on bool, count string) bool {
	var icon *ui.SVG
	if on {
		icon = iconRadioDot
	}
	o := menuItem{}
	if count != "" {
		c, p := m.c, m.p
		o.end = func() { ui.Text(c, count).FontSize(sizeSub).TextColor(p.Text).Shrink(0) }
	}
	return m.item(key, icon, label, o)
}

// schedMenuRows are Lody's ScheduleMenuItems: each action with its icon,
// the destructive ones after a rule and in the destructive ink.
func schedMenuRows(m *lodyMenu, actions []schedAction) {
	for _, act := range actions {
		if act.danger {
			m.sep()
		}
		if m.item(act.key, act.icon, act.label, menuItem{destructive: act.danger}) {
			act.run()
		}
	}
}
