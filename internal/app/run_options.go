package app

import (
	"slices"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// The composer's agent settings beyond the model, as Lody's footer and the
// iOS model panel offer them: the reasoning effort the model allows, and
// Fast where the agent has it. A choice rides on the next message's
// configOptionValues, over what the last turn used.

// effortOption is the agent's reasoning effort, its choices those model
// allows.
func effortOption(cap catalog.Capability, model string) (catalog.ConfigOption, bool) {
	if model == "" {
		for _, o := range cap.Options {
			if o.ID == "model" || o.Category == "model" {
				model = str(o.Current)
			}
		}
	}
	for _, o := range cap.Options {
		if o.Type != "select" || o.ID != "reasoning_effort" && o.Category != "thought_level" {
			continue
		}
		if efforts, ok := cap.Efforts[model]; ok {
			var kept []catalog.Choice
			for _, ch := range o.Choices {
				if slices.Contains(efforts, ch.Value) {
					kept = append(kept, ch)
				}
			}
			o.Choices = kept
		}
		return o, len(o.Choices) > 0
	}
	return catalog.ConfigOption{}, false
}

// fastOption is the agent's Fast switch, by either of its ids.
func fastOption(cap catalog.Capability) (catalog.ConfigOption, bool) {
	for _, o := range cap.Options {
		if o.Type == "boolean" && (o.ID == "fast-mode" || o.ID == "fast") {
			return o, true
		}
	}
	return catalog.ConfigOption{}, false
}

// runValue is a setting as the composer shows it: chosen, else as the last
// turn left it, else the agent's current value.
func (a *App) runValue(key string, o catalog.ConfigOption, last map[string]any) any {
	if v, ok := a.runValues[key][o.ID]; ok {
		return v
	}
	if v, ok := last[o.ID]; ok {
		return v
	}
	return o.Current
}

func (a *App) setRunValue(key, id string, v any) {
	if a.runValues[key] == nil {
		a.runValues[key] = map[string]any{}
	}
	a.runValues[key][id] = v
}

// chosenRunValues are the settings chosen for key's next message, an
// effort the model doesn't allow left out.
func (a *App) chosenRunValues(key, agentConfig, model string) map[string]any {
	chosen := a.runValues[key]
	if len(chosen) == 0 {
		return nil
	}
	cap := a.catalog.Capabilities[agentConfig]
	out := map[string]any{}
	for id, v := range chosen {
		out[id] = v
	}
	if o, ok := effortOption(cap, model); ok {
		if v, set := out[o.ID]; set && !slices.ContainsFunc(o.Choices, func(ch catalog.Choice) bool { return ch.Value == v }) {
			delete(out, o.ID)
		}
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
	cap := a.catalog.Capabilities[agentConfig]
	if o, ok := effortOption(cap, model); ok {
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
		on := a.runValue(key, o, last) == true
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
		b.OnClick(func() { a.setRunValue(key, o.ID, !on) })
	}
}
