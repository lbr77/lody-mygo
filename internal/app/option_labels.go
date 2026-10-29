package app

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// Lody's composer doesn't show an agent's settings quite as the agent
// reports them. Every value loses "(recommended)"; Codex's models drop
// their "gpt-" and Lody names two of its modes itself; built-in Grok's
// interaction and permission settings are in Lody's words, in the app's
// language. The effort follows the model chosen: the efforts the agent
// declared for it, Codex's own tiers, or its per-model list, named Low,
// Medium, High, X-High. Fast shows only for a model that has it.

// capability is agentConfig's capability, knowing which runtime it is
// even where the agent's report doesn't say.
func (a *App) capability(agentConfig string) catalog.Capability {
	cap := a.catalog.Capabilities[agentConfig]
	if cap.CLIType != "" && cap.AgentType != "" {
		return cap
	}
	for _, agents := range a.catalog.Agents {
		for _, ag := range agents {
			if ag.ID == agentConfig {
				cap.CLIType, cap.AgentType = firstNonEmpty(cap.CLIType, ag.CLIType), firstNonEmpty(cap.AgentType, ag.AgentType)
				return cap
			}
		}
	}
	return cap
}

// runCapability is agentConfig's capability with its settings as Lody's
// composer offers them while model is chosen.
func (a *App) runCapability(agentConfig, model string) catalog.Capability {
	cap := a.capability(agentConfig)
	cap.Options = runSettings(cap, model)
	return cap
}

// builtinAgent is whether cap is Lody's own build of the agent kind.
func builtinAgent(cap catalog.Capability, kind string) bool {
	return cap.CLIType == "builtin" && strings.EqualFold(cap.AgentType, kind)
}

// runSettings are cap's settings as Lody's composer shows them while model
// is chosen (the agent's current model when none is): its values named as
// Lody names them, the effort fitted to the model, and Fast only where
// the model has it.
func runSettings(cap catalog.Capability, model string) []catalog.ConfigOption {
	opts := make([]catalog.ConfigOption, 0, len(cap.Options))
	for _, o := range cap.Options {
		choices := make([]catalog.Choice, len(o.Choices))
		for i, ch := range o.Choices {
			switch o.Category {
			case "model":
				ch.Name = modelLabel(cap, ch.Name)
			case "mode":
				ch.Name = modeLabel(cap, ch.Value, ch.Name)
			default:
				ch.Name = stripRecommended.ReplaceAllString(ch.Name, "")
			}
			choices[i] = ch
		}
		o.Choices = choices
		opts = append(opts, o)
	}
	if builtinAgent(cap, "grok") {
		grokSettings(opts)
	}
	if model == "" {
		for _, o := range opts {
			if o.Category == "model" && o.Type == "select" {
				model = str(o.Current)
				break
			}
		}
	}
	return fitFast(cap, fitEffort(cap, opts, model), model)
}

// modelLabel is a model's name as Lody shows it: without "(recommended)",
// and a Codex model without its "gpt-".
func modelLabel(cap catalog.Capability, name string) string {
	name = stripRecommended.ReplaceAllString(name, "")
	if strings.EqualFold(cap.AgentType, "codex") {
		name = gptPrefix.ReplaceAllString(name, "")
	}
	return name
}

var gptPrefix = regexp.MustCompile(`(?i)^gpt-`)

// modeLabel is a mode's name as Lody shows it: built-in Codex's
// full-access and auto-review modes by Lody's names for them, any other
// without "(recommended)".
func modeLabel(cap catalog.Capability, id, name string) string {
	if builtinAgent(cap, "codex") {
		switch id {
		case "agent-full-access":
			return "Full access"
		case "agent-auto-review":
			return "Auto review"
		}
	}
	return stripRecommended.ReplaceAllString(name, "")
}

// runModes are an agent's permission modes as its permission button lists
// them: named as Lody names them, each with the agent's line about it, or
// the line its mode setting gives that value.
func runModes(cap catalog.Capability, modes []catalog.Mode) []catalog.Mode {
	lines := map[string]string{}
	for _, o := range cap.Options {
		if optionKind(o) == "mode" {
			for _, ch := range o.Choices {
				lines[ch.Value] = ch.Description
			}
			break
		}
	}
	out := make([]catalog.Mode, len(modes))
	for i, m := range modes {
		m.Name = modeLabel(cap, m.ID, m.Name)
		m.Description = firstNonEmpty(m.Description, lines[m.ID])
		out[i] = m
	}
	return out
}

// grokText is a value of built-in Grok's settings in Lody's words.
type grokText struct{ label, description func() string }

// grokInteraction and grokPermission are the values of the interaction and
// permission settings Lody adds to built-in Grok, which it names itself,
// in the app's language, as the agent's report has them only in English.
var (
	grokInteraction = map[string]grokText{
		"agent": {locales.GrokInteractionAgent, locales.GrokInteractionAgentDescription},
		"plan":  {locales.GrokInteractionPlan, locales.GrokInteractionPlanDescription},
		"ask":   {locales.GrokInteractionAsk, locales.GrokInteractionAskDescription},
	}
	grokPermission = map[string]grokText{
		"ask":            {locales.GrokPermissionAsk, locales.GrokPermissionAskDescription},
		"auto":           {locales.GrokPermissionAuto, locales.GrokPermissionAutoDescription},
		"always-approve": {locales.GrokPermissionAlwaysApprove, locales.GrokPermissionAlwaysApproveDescription},
	}
)

// grokSettings names built-in Grok's interaction and permission settings
// and their values in Lody's words; a value Lody doesn't know keeps the
// agent's.
func grokSettings(opts []catalog.ConfigOption) {
	for i := range opts {
		o := &opts[i]
		var texts map[string]grokText
		switch {
		case o.Type == "select" && o.ID == "interaction_mode":
			o.Name, o.Description, texts = locales.GrokInteractionLabel(), locales.GrokInteractionDescription(), grokInteraction
		case o.Type == "select" && o.ID == "permission_mode":
			o.Name, o.Description, texts = locales.GrokPermissionLabel(), locales.GrokPermissionDescription(), grokPermission
		default:
			continue
		}
		for j, ch := range o.Choices {
			if t, ok := texts[ch.Value]; ok {
				o.Choices[j].Name, o.Choices[j].Description = t.label(), t.description()
			}
		}
	}
}

// effortLabel is an effort as Lody names the efforts it lists for a model
// itself: X-High, else the value capitalized.
func effortLabel(v string) string {
	if v == "xhigh" {
		return "X-High"
	}
	if v == "" {
		return v
	}
	r, n := utf8.DecodeRuneInString(v)
	return string(unicode.ToUpper(r)) + v[n:]
}

// defaultEffort is the effort a model falls back to: medium where it
// allows it, else its first.
func defaultEffort(values []string) string {
	switch {
	case slices.Contains(values, "medium"):
		return "medium"
	case len(values) > 0:
		return values[0]
	}
	return ""
}

// modelBinding is how a built-in agent takes its per-model controls: its
// effort setting and the name Lody gives it, the effort it offers beyond
// the model's own (Claude's default, which leaves it to the provider),
// whether a model declared without efforts has none, and its Fast setting.
type modelBinding struct {
	effortID, effortName, providerDefault string
	noEffortsMeansNone                    bool
	fastID                                string
}

// modelBindings are the built-in agents whose model declarations Lody
// turns into settings; it never guesses another agent's setting ids.
var modelBindings = map[string]modelBinding{
	"claude": {effortID: "effort", effortName: "Effort", providerDefault: "default", noEffortsMeansNone: true, fastID: "fast"},
	"codex":  {effortID: "reasoning_effort", effortName: "Reasoning effort", fastID: "fast-mode"},
}

func bindingFor(cap catalog.Capability) (modelBinding, bool) {
	if cap.CLIType != "builtin" {
		return modelBinding{}, false
	}
	b, ok := modelBindings[strings.ToLower(cap.AgentType)]
	return b, ok
}

// isEffort is whether o is a reasoning effort to choose among.
func isEffort(o catalog.ConfigOption) bool {
	return o.Type == "select" && (o.ID == "reasoning_effort" || o.Category == "thought_level")
}

// codexExtendedEfforts are the efforts above X-High each Codex model
// allows, as Lody keeps them beside Codex's model list, and
// codexExtendedChoices how Lody lists them.
var (
	codexExtendedEfforts = map[string][]string{
		"gpt-6-astra":   {"max", "ultra"},
		"gpt-6.1-sol":   {"max", "ultra"},
		"gpt-6-sol":     {"max", "ultra"},
		"gpt-6-luna":    {"max"},
		"gpt-5.6-sol":   {"max", "ultra"},
		"gpt-5.6-terra": {"max", "ultra"},
		"gpt-5.6-luna":  {"max"},
	}
	codexExtendedChoices = []catalog.Choice{
		{Value: "max", Name: "Max", Description: "Maximum reasoning depth for the hardest problems"},
		{Value: "ultra", Name: "Ultra", Description: "Maximum reasoning with automatic task delegation"},
	}
)

// fitEffort fits the efforts to model, as Lody does: the efforts the
// agent declared for it, provider default first, with none it has no
// effort at all; else for built-in Codex, Codex's tiers, Max and Ultra
// only where the model reaches them; else the agent's list for the
// model, in its order. A model nothing covers keeps what the agent
// reported. A current effort the model lacks falls back to medium, or
// the model's first.
func fitEffort(cap catalog.Capability, opts []catalog.ConfigOption, model string) []catalog.ConfigOption {
	if model == "" {
		return opts
	}
	binding, bound := bindingFor(cap)
	if controls, ok := cap.Declared[model]; ok {
		switch {
		case controls.HasEfforts && len(controls.Efforts) == 0, !controls.HasEfforts && bound && binding.noEffortsMeansNone:
			return slices.DeleteFunc(opts, isEffort)
		case controls.HasEfforts:
			values, fallback := controls.Efforts, defaultEffort(controls.Efforts)
			if bound && binding.providerDefault != "" {
				values, fallback = append([]string{binding.providerDefault}, values...), binding.providerDefault
			}
			if !slices.ContainsFunc(opts, isEffort) && bound {
				// The agent reported the model before last, which had no
				// effort: add its own setting.
				opts = append(opts, catalog.ConfigOption{ID: binding.effortID, Name: binding.effortName, Category: "thought_level", Type: "select", Current: fallback})
			}
			return effortLadder(opts, values, fallback)
		}
	}
	if builtinAgent(cap, "codex") {
		return codexEfforts(opts, model)
	}
	if efforts := cap.Efforts[model]; len(efforts) > 0 {
		return effortLadder(opts, efforts, defaultEffort(efforts))
	}
	return opts
}

// effortLadder lists values, as Lody names them, as every effort's
// choices.
func effortLadder(opts []catalog.ConfigOption, values []string, fallback string) []catalog.ConfigOption {
	for i, o := range opts {
		if !isEffort(o) {
			continue
		}
		choices := make([]catalog.Choice, len(values))
		for j, v := range values {
			choices[j] = catalog.Choice{Value: v, Name: effortLabel(v)}
		}
		opts[i].Choices = choices
		if !slices.Contains(values, str(o.Current)) {
			opts[i].Current = fallback
		}
	}
	return opts
}

// codexEfforts keeps Codex's Max and Ultra to the models that reach them,
// adding those its report left out.
func codexEfforts(opts []catalog.ConfigOption, model string) []catalog.ConfigOption {
	reach := codexExtendedEfforts[model]
	for i, o := range opts {
		if o.Type != "select" || o.ID != "reasoning_effort" {
			continue
		}
		choices := slices.DeleteFunc(slices.Clone(o.Choices), func(ch catalog.Choice) bool {
			return (ch.Value == "max" || ch.Value == "ultra") && !slices.Contains(reach, ch.Value)
		})
		for _, ch := range codexExtendedChoices {
			if slices.Contains(reach, ch.Value) && !slices.ContainsFunc(choices, func(c catalog.Choice) bool { return c.Value == ch.Value }) {
				choices = append(choices, ch)
			}
		}
		opts[i].Choices = choices
		if !slices.ContainsFunc(choices, func(ch catalog.Choice) bool { return ch.Value == str(o.Current) }) {
			values := make([]string, len(choices))
			for j, ch := range choices {
				values[j] = ch.Value
			}
			opts[i].Current = defaultEffort(values)
		}
	}
	return opts
}

// fitFast shows Fast for a model as its agent declared it: not at all for
// a model without it, and for one with it even where the agent's report,
// of another model, left it out.
func fitFast(cap catalog.Capability, opts []catalog.ConfigOption, model string) []catalog.ConfigOption {
	controls, ok := cap.Declared[model]
	if !ok || model == "" || controls.Fast == nil {
		return opts
	}
	isFast := func(o catalog.ConfigOption) bool { return optionKind(o) == "fast" }
	if !*controls.Fast {
		return slices.DeleteFunc(opts, isFast)
	}
	if binding, bound := bindingFor(cap); bound && !slices.ContainsFunc(opts, isFast) {
		opts = append(opts, catalog.ConfigOption{ID: binding.fastID, Name: "Fast mode", Category: "model_config", Type: "boolean", Current: false})
	}
	return opts
}

// optionValue is what o is set to as v sets it, as Lody resolves it: v
// where o can take it, else the value o has.
func optionValue(o catalog.ConfigOption, v any) any {
	switch o.Type {
	case "boolean":
		if _, ok := v.(bool); ok {
			return v
		}
	case "select":
		if s, ok := v.(string); ok && slices.ContainsFunc(o.Choices, func(ch catalog.Choice) bool { return ch.Value == s }) {
			return v
		}
	}
	return o.Current
}

// permissionControl is what the permission button calls what it sets, as
// Lody does: Permission for an agent's permission setting; else the name
// of the mode setting it stands for, where it is the agent's own; else
// Mode.
func permissionControl(cap catalog.Capability, o catalog.ConfigOption, setting bool) string {
	if setting {
		if optionKind(o) == "permission" {
			return locales.RunConfigPermission()
		}
		return firstNonEmpty(o.Name, locales.RunConfigMode())
	}
	if cap.CLIType == "registry" || cap.CLIType == "custom" {
		for _, opt := range cap.Options {
			if optionKind(opt) == "mode" {
				return firstNonEmpty(opt.Name, locales.RunConfigMode())
			}
		}
	}
	return locales.RunConfigMode()
}
