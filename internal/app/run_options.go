package app

import (
	"slices"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// The composer's agent settings beyond the model, as Lody's footer and the
// iOS model panel offer them: the reasoning effort the model allows, Fast
// where the agent has it, the permission button, and Lody's
// run-configuration menu for every other setting the agent reports, select
// or switch. A choice rides on the next message's configOptionValues, over
// what the last turn used, at creation and in a running conversation alike.

// effortOption is the agent's reasoning effort, of its settings as
// runCapability fits them to the model.
func effortOption(cap catalog.Capability) (catalog.ConfigOption, bool) {
	for _, o := range cap.Options {
		if isEffort(o) {
			return o, len(o.Choices) > 0
		}
	}
	return catalog.ConfigOption{}, false
}

// fastOption is the agent's Fast switch, by either of its ids: a boolean,
// or a select of on and off.
func fastOption(cap catalog.Capability) (catalog.ConfigOption, bool) {
	for _, o := range cap.Options {
		if optionKind(o) == "fast" {
			return o, true
		}
	}
	return catalog.ConfigOption{}, false
}

// optionKind places an agent's setting as Lody's run configuration orders
// them: model, effort, interaction, permission, mode, fast, plan, a switch,
// or other.
func optionKind(o catalog.ConfigOption) string {
	switch {
	case o.Category == "model" && o.Type == "select":
		return "model"
	case o.ID == "reasoning_effort" || o.Category == "thought_level":
		return "effort"
	case o.ID == "interaction_mode" && o.Type == "select":
		return "interaction"
	case o.Type == "select" && (o.ID == "permission_mode" || o.Category == "_permission"):
		return "permission"
	case o.Category == "mode" && o.Type == "select":
		return "mode"
	case (o.ID == "fast-mode" || o.ID == "fast") && (o.Type == "boolean" || onOffSelect(o)):
		return "fast"
	case o.ID == "plan_mode" || o.ID == "collaboration_mode" || o.Category == "collaboration_mode":
		return "plan"
	case o.Type == "boolean":
		return "switch"
	}
	return "other"
}

// onOffSelect is a select whose choices are on and off, a switch by
// another name.
func onOffSelect(o catalog.ConfigOption) bool {
	has := func(v string) bool {
		return slices.ContainsFunc(o.Choices, func(ch catalog.Choice) bool { return ch.Value == v })
	}
	return o.Type == "select" && has("on") && has("off")
}

// switchOn is whether a switch is on as v sets it: true, "on" for a
// select of on and off, or "plan" for the collaboration mode.
func switchOn(o catalog.ConfigOption, v any) bool {
	switch {
	case o.Type == "boolean":
		return v == true
	case optionKind(o) == "plan":
		return v == "plan"
	}
	return v == "on"
}

// switchValue is what turns a switch on or off, in its own kind of value.
func switchValue(o catalog.ConfigOption, on bool) any {
	switch {
	case o.Type == "boolean":
		return on
	case optionKind(o) == "plan":
		return map[bool]string{true: "plan", false: "default"}[on]
	}
	return map[bool]string{true: "on", false: "off"}[on]
}

// permissionOption is the setting the permission button writes, as Lody
// resolves it: the agent's permission setting, over its modes; with
// neither, a setting of the mode category standing in. ok is false while
// the modes are what it picks.
func permissionOption(cap catalog.Capability, modes []catalog.Mode) (o catalog.ConfigOption, ok bool) {
	var stand *catalog.ConfigOption
	for i, opt := range cap.Options {
		switch optionKind(opt) {
		case "permission":
			return opt, len(opt.Choices) > 0
		case "mode":
			if stand == nil {
				stand = &cap.Options[i]
			}
		}
	}
	if len(modes) > 0 || stand == nil || len(stand.Choices) == 0 {
		return catalog.ConfigOption{}, false
	}
	return *stand, true
}

// menuOptions are the settings Lody's run-configuration menu holds beyond
// the model, the effort and Fast: the agent's other choices, its
// interaction mode, then its switches, Plan first. The permission setting
// has its button, and so has a mode-category one standing in for it.
func menuOptions(cap catalog.Capability) (selects, switches []catalog.ConfigOption) {
	var interaction, plans []catalog.ConfigOption
	for _, o := range cap.Options {
		switch optionKind(o) {
		case "other":
			if len(o.Choices) > 0 {
				selects = append(selects, o)
			}
		case "interaction":
			if len(o.Choices) > 0 {
				interaction = append(interaction, o)
			}
		case "plan":
			plans = append(plans, o)
		case "switch":
			switches = append(switches, o)
		}
	}
	return append(selects, interaction...), append(plans, switches...)
}

// runValue is a setting as the composer shows it: chosen, else as the last
// turn left it, else the agent's current value; one the setting can't take,
// as an effort the model lacks, shows as its current value, as in Lody.
func (a *App) runValue(key string, o catalog.ConfigOption, last map[string]any) any {
	if v, ok := a.runValues[key][o.ID]; ok {
		return optionValue(o, v)
	}
	if v, ok := last[o.ID]; ok {
		return optionValue(o, v)
	}
	return o.Current
}

func (a *App) setRunValue(key, id string, v any) {
	if a.runValues[key] == nil {
		a.runValues[key] = map[string]any{}
	}
	a.runValues[key][id] = v
}

// chosenRunValues are the settings chosen for key's next message, as
// Lody sends them for model: an effort the model doesn't allow left out,
// and a setting the model doesn't have, as Fast or the effort of a model
// declared without them.
func (a *App) chosenRunValues(key, agentConfig, model string) map[string]any {
	chosen := a.runValues[key]
	if len(chosen) == 0 {
		return nil
	}
	cap := a.runCapability(agentConfig, model)
	reported := a.catalog.Capabilities[agentConfig].Options
	out := map[string]any{}
	for id, v := range chosen {
		at := slices.IndexFunc(cap.Options, func(o catalog.ConfigOption) bool { return o.ID == id })
		switch {
		case at < 0 && slices.ContainsFunc(reported, func(o catalog.ConfigOption) bool { return o.ID == id }):
			continue // the model hides it
		case at >= 0 && isEffort(cap.Options[at]) && !slices.ContainsFunc(cap.Options[at].Choices, func(ch catalog.Choice) bool { return ch.Value == v }):
			continue
		}
		out[id] = v
	}
	return out
}

// currentModel is the model a session's composer shows.
func (a *App) currentModel(sess *catalog.Session) string {
	if m := a.models[sess.ID]; m != "" {
		return m
	}
	if a.view != nil && a.view.ModelID != "" {
		return a.view.ModelID
	}
	if sess.LastModel != nil {
		return sess.LastModel.ModelID
	}
	return ""
}

// runOptions builds the effort picker and the Fast switch for key's
// composer, and offers the effort to the cycle command.
func (a *App) runOptions(c *ui.Context, p *palette, key, agentConfig, model string, last map[string]any) {
	cap := a.runCapability(agentConfig, model)
	if o, ok := effortOption(cap); ok {
		current := str(a.runValue(key, o, last))
		label, values := "", []string{}
		var options []option
		for _, ch := range o.Choices {
			options = append(options, option{ch.Value, firstNonEmpty(ch.Name, ch.Value)})
			values = append(values, ch.Value)
			if ch.Value == current {
				label = firstNonEmpty(ch.Name, ch.Value)
			}
		}
		if label == "" {
			label = firstNonEmpty(o.Name, locales.CmdThinkEffort())
		}
		pick := func(v string) { a.setRunValue(key, o.ID, v) }
		a.picker(c, p, "effort/"+key, iconBrain, label, options, pick)
		a.surfNext.effort = &cycleTarget{values: values, current: current, pick: pick}
	}
	if o, ok := fastOption(cap); ok {
		on := switchOn(o, a.runValue(key, o, last))
		tip := locales.FastModeTooltip()
		if o.ID == "fast" {
			tip = locales.FastModeTooltipClaude()
		}
		icon := iconZapOff
		if on {
			icon = iconZap
		}
		b := iconButton(c.Key("fast/"+key), p, icon, tip, p.Hover).Size(26, 26)
		if on {
			b.Background(p.Text.Alpha(0.08))
		}
		b.OnClick(func() { a.setRunValue(key, o.ID, switchValue(o, !on)) })
	}
	a.runConfigMenu(c, p, key, cap, last)
}

// iconListChecks is Lucide's list-checks, Lody's Plan.
var iconListChecks = lucide(`<path d="m3 17 2 2 4-4"/><path d="m3 7 2 2 4-4"/><path d="M13 6h8"/><path d="M13 12h8"/><path d="M13 18h8"/>`)

// runConfigMenu is Lody's run-configuration menu for the settings the
// footer has no control of its own for: each choice a row naming its
// value that opens the values beside it, then the switches, Plan first.
// Rows act in place, the menu staying open, as Lody's. Once a
// conversation has started, its agent preset is fixed, as its agent is.
func (a *App) runConfigMenu(c *ui.Context, p *palette, key string, cap catalog.Capability, last map[string]any) {
	selects, switches := menuOptions(cap)
	if len(selects) == 0 && len(switches) == 0 {
		return
	}
	started := a.catalog.Session(key) != nil
	planOn := false
	for _, o := range switches {
		planOn = planOn || optionKind(o) == "plan" && switchOn(o, a.runValue(key, o, last))
	}
	b := ui.ButtonBase(c.Key("run-config/"+key)).Height(26).MinWidth(26).Padding(0, 5).Radius(8).Gap(4).AlignItems(ui.Center).Justify(ui.Center).
		Cursor(ui.CursorPointer).Label(locales.RunConfigTitle()).Transition(colorFade)
	st := &a.rowMenu("run-config/" + key).menuState
	if !st.open {
		tip(c, p, b, locales.RunConfigTitle())
	}
	ink := p.SideMuted
	if b.Hovered() || st.open {
		ink = p.Text
		b.Background(p.Hover)
	}
	b.Children(func() {
		ui.Icon(c, iconSliders).Size(16, 16).TextColor(ink)
		if planOn {
			ui.Icon(c, iconListChecks).Size(14, 14).TextColor(p.Text)
		}
	})
	lodyMenuAbove(c, p, b, st, false, 6, 240, 290, 28, func(m *lodyMenu) {
		for _, o := range selects {
			current := str(a.runValue(key, o, last))
			value := current
			for _, ch := range o.Choices {
				if ch.Value == current {
					value = firstNonEmpty(ch.Name, ch.Value)
				}
			}
			locked := started && o.ID == "agent_preset"
			m.submenu(o.ID, nil, firstNonEmpty(o.Name, o.ID), menuItem{bare: true, value: value, disabled: locked}, 108, func(sm *lodyMenu) {
				for _, ch := range o.Choices {
					chosen := ch.Value == current
					check := func() {
						if chosen {
							ui.Icon(c, iconCheck).Size(14, 14).TextColor(p.Text)
						}
					}
					if sm.item(ch.Value, nil, firstNonEmpty(ch.Name, ch.Value), menuItem{bare: true, keepOpen: true, end: check}) {
						a.setRunValue(key, o.ID, ch.Value)
					}
				}
			})
		}
		if len(selects) > 0 && len(switches) > 0 {
			m.sep()
		}
		for _, o := range switches {
			on := switchOn(o, a.runValue(key, o, last))
			var icon *ui.SVG
			label := firstNonEmpty(o.Name, o.ID)
			if optionKind(o) == "plan" {
				icon, label = iconListChecks, locales.RunConfigPlan()
			}
			if m.item(o.ID, icon, label, menuItem{keepOpen: true, bare: icon == nil, end: func() { menuSwitch(c, p, on) }}) {
				a.setRunValue(key, o.ID, switchValue(o, !on))
			}
		}
	})
}

// permissionButton is the composer's permission button, its choices the
// agent's permission setting where it has one, else its modes, else a
// mode-category setting standing in, each named and described as Lody
// does; it returns them for the commands that step through them. key is
// the composer's, for its settings.
func (a *App) permissionButton(c *ui.Context, p *palette, button, key, agentConfig string, modes []catalog.Mode, mode string, pickMode func(string), last map[string]any) *cycleTarget {
	cap := a.runCapability(agentConfig, "")
	o, ok := permissionOption(cap, modes)
	if !ok {
		modes = runModes(cap, modes)
		a.modeButton(c, p, button, permissionControl(cap, o, false), modes, mode, pickMode)
		return modeCycle(modes, mode, pickMode)
	}
	choices := make([]catalog.Mode, 0, len(o.Choices))
	for _, ch := range o.Choices {
		choices = append(choices, catalog.Mode{ID: ch.Value, Name: firstNonEmpty(ch.Name, ch.Value), Description: ch.Description})
	}
	current := str(a.runValue(key, o, last))
	pick := func(v string) { a.setRunValue(key, o.ID, v) }
	a.modeButton(c, p, button, permissionControl(cap, o, true), choices, current, pick)
	return modeCycle(choices, current, pick)
}
