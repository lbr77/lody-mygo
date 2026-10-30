package catalog

import (
	"reflect"
	"testing"
)

func optionIDs(cap Capability) []string {
	var out []string
	for _, o := range cap.Options {
		out = append(out, o.ID)
	}
	return out
}

func choiceValues(o ConfigOption) []string {
	var out []string
	for _, ch := range o.Choices {
		out = append(out, ch.Value)
	}
	return out
}

func modeIDs(modes []Mode) []string {
	var out []string
	for _, m := range modes {
		out = append(out, m.ID)
	}
	return out
}

func staticOf(t *testing.T, agentType string) Capability {
	t.Helper()
	cap, ok := StaticCapability("builtin", agentType, false)
	if !ok {
		t.Fatalf("no static capability for %s", agentType)
	}
	return cap
}

// Built-in Claude starts with Claude Code's modes and models, and its
// mode, model, effort and Fast settings, as Lody's table has them.
func TestStaticClaude(t *testing.T) {
	cap := staticOf(t, "claude")
	if cap.CLIType != "builtin" || cap.AgentType != "claude" {
		t.Fatalf("runtime %s/%s", cap.CLIType, cap.AgentType)
	}
	wantModes := []Mode{
		{"auto", "Auto", "Use a model classifier to approve/deny permission prompts"},
		{"default", "Manual", "Standard behavior, prompts for dangerous operations"},
		{"acceptEdits", "Accept Edits", "Auto-accept file edit operations"},
		{"plan", "Plan Mode", "Planning mode, no actual tool execution"},
		{"dontAsk", "Don't Ask", "Don't prompt for permissions, deny if not pre-approved"},
		{"bypassPermissions", "Bypass Permissions", "Bypass all permission checks"},
	}
	if !reflect.DeepEqual(cap.Modes, wantModes) {
		t.Errorf("modes %+v", cap.Modes)
	}
	wantModels := []Model{
		{"default", "Default (recommended)", "Opus (1M context)"},
		{"opus[1m]", "Opus (1M context)", "Opus 5 with 1M context · Best for everyday, complex tasks"},
		{"claude-fable-5-1[1m]", "Fable", "Fable 5.1 · Most capable for your hardest and longest-running tasks"},
		{"sonnet", "Sonnet", "Sonnet 5 · Efficient for routine tasks"},
		{"haiku", "Haiku", "Haiku 4.5 · Fastest for quick answers"},
	}
	if !reflect.DeepEqual(cap.Models, wantModels) {
		t.Errorf("models %+v", cap.Models)
	}
	if ids := optionIDs(cap); !reflect.DeepEqual(ids, []string{"mode", "model", "effort", "fast"}) {
		t.Fatalf("options %q", ids)
	}
	mode, model, effort, fast := cap.Options[0], cap.Options[1], cap.Options[2], cap.Options[3]
	if mode.Name != "Mode" || mode.Description != "Session permission mode" || mode.Category != "mode" || mode.Current != "auto" ||
		!reflect.DeepEqual(choiceValues(mode), modeIDs(wantModes)) || mode.Choices[1].Name != "Manual" || mode.Choices[1].Description != wantModes[1].Description {
		t.Errorf("mode %+v", mode)
	}
	if model.Name != "Model" || model.Description != "AI model to use" || model.Category != "model" || model.Current != "default" ||
		model.Choices[2] != (Choice{"claude-fable-5-1[1m]", "Fable", "Fable 5.1 · Most capable for your hardest and longest-running tasks"}) {
		t.Errorf("model %+v", model)
	}
	if effort.Name != "Effort" || effort.Description != "Available effort levels for this model" || effort.Category != "thought_level" || effort.Current != "default" ||
		!reflect.DeepEqual(choiceValues(effort), []string{"default", "low", "medium", "high", "xhigh", "max"}) || effort.Choices[4].Name != "Xhigh" {
		t.Errorf("effort %+v", effort)
	}
	if !reflect.DeepEqual(fast, ConfigOption{ID: "fast", Name: "Fast mode", Description: "Faster responses on supported models", Category: "model_config", Type: "boolean", Current: false}) {
		t.Errorf("fast %+v", fast)
	}
}

// Built-in Codex starts on Auto review and GPT-5.6-Sol at medium effort,
// with Fast and Lody's Plan switch.
func TestStaticCodex(t *testing.T) {
	cap := staticOf(t, "codex")
	if ids := modeIDs(cap.Modes); !reflect.DeepEqual(ids, []string{"read-only", "agent", "agent-auto-review", "agent-full-access"}) {
		t.Errorf("modes %q", ids)
	}
	if cap.Modes[2].Description != "Read and edit files, and run commands. Approval requests are reviewed automatically by a Codex subagent." {
		t.Errorf("auto review %+v", cap.Modes[2])
	}
	var models []string
	for _, m := range cap.Models {
		models = append(models, m.ModelID+"="+m.Name)
	}
	if !reflect.DeepEqual(models, []string{"gpt-5.6-sol=GPT-5.6-Sol", "gpt-5.6-terra=GPT-5.6-Terra", "gpt-5.6-luna=GPT-5.6-Luna", "gpt-5.5=GPT-5.5", "gpt-5.4=GPT-5.4", "gpt-5.4-mini=GPT-5.4-Mini"}) {
		t.Errorf("models %q", models)
	}
	if ids := optionIDs(cap); !reflect.DeepEqual(ids, []string{"mode", "model", "reasoning_effort", "fast-mode", "plan_mode"}) {
		t.Fatalf("options %q", ids)
	}
	mode, model, effort, fast, plan := cap.Options[0], cap.Options[1], cap.Options[2], cap.Options[3], cap.Options[4]
	if mode.Description != "Approval and sandboxing preset for the session" || mode.Current != "agent-auto-review" || len(mode.Choices) != 4 {
		t.Errorf("mode %+v", mode)
	}
	if model.Description != "Model Codex uses for the session" || model.Current != "gpt-5.6-sol" || model.Choices[5].Description != "Small, fast, and cost-efficient model for simpler coding tasks." {
		t.Errorf("model %+v", model)
	}
	if effort.Name != "Reasoning effort" || effort.Category != "thought_level" || effort.Current != "medium" ||
		!reflect.DeepEqual(effort.Choices, []Choice{{"low", "Low", "Fastest responses"}, {"medium", "Medium", "Balanced reasoning"}, {"high", "High", "More reasoning for difficult tasks"}, {"xhigh", "XHigh", "Extra reasoning for complex tasks"}}) {
		t.Errorf("effort %+v", effort)
	}
	if fast.Name != "Fast mode" || fast.Description != "1.5x speed, increased usage" || fast.Category != "model_config" || fast.Type != "boolean" || fast.Current != false {
		t.Errorf("fast %+v", fast)
	}
	if !reflect.DeepEqual(plan, ConfigOption{ID: "plan_mode", Name: "Plan", Type: "boolean", Current: false, Description: "Plan before implementing; permissions are controlled separately."}) {
		t.Errorf("plan %+v", plan)
	}
}

// Built-in Kimi has its modes and a permission setting of them but Plan,
// on Auto, and no models until the machine knows the account's.
func TestStaticKimi(t *testing.T) {
	cap := staticOf(t, "kimi")
	if ids := modeIDs(cap.Modes); !reflect.DeepEqual(ids, []string{"default", "plan", "auto", "yolo"}) {
		t.Errorf("modes %q", ids)
	}
	if len(cap.Models) != 0 {
		t.Errorf("models %+v", cap.Models)
	}
	if ids := optionIDs(cap); !reflect.DeepEqual(ids, []string{"permission_mode", "plan_mode"}) {
		t.Fatalf("options %q", ids)
	}
	perm := cap.Options[0]
	if perm.Name != "Permission" || perm.Description != "" || perm.Category != "_permission" || perm.Current != "auto" ||
		!reflect.DeepEqual(choiceValues(perm), []string{"default", "auto", "yolo"}) || perm.Choices[2].Description != "Auto-approve everything." {
		t.Errorf("permission %+v", perm)
	}
}

// Built-in Grok has Plan, its permission setting, its models and each
// one's efforts, as the official runtime's adapter offers them.
func TestStaticGrok(t *testing.T) {
	cap := staticOf(t, "grok")
	if ids := modeIDs(cap.Modes); !reflect.DeepEqual(ids, []string{"default", "plan"}) || cap.Modes[0].Name != "Agent" {
		t.Errorf("modes %+v", cap.Modes)
	}
	if !reflect.DeepEqual(cap.Models, []Model{{"grok-4.6", "Grok 4.6", "SpaceXAI's latest frontier model"}, {ModelID: "grok-4.5", Name: "Grok 4.5"}}) {
		t.Errorf("models %+v", cap.Models)
	}
	if ids := optionIDs(cap); !reflect.DeepEqual(ids, []string{"plan_mode", "permission_mode", "model", "reasoning_effort"}) {
		t.Fatalf("options %q", ids)
	}
	plan, perm, model, effort := cap.Options[0], cap.Options[1], cap.Options[2], cap.Options[3]
	if plan.Type != "boolean" || plan.Current != false {
		t.Errorf("plan %+v", plan)
	}
	if perm.Name != "Permission Mode" || perm.Category != "_permission" || perm.Current != "ask" ||
		!reflect.DeepEqual(perm.Choices, []Choice{{"ask", "Ask Every Time", "Request approval before protected actions"}, {"always-approve", "Always Approve", "Approve protected actions automatically"}}) {
		t.Errorf("permission %+v", perm)
	}
	if model.Description != "Select the model used for this session" || model.Current != "grok-4.6" || !reflect.DeepEqual(choiceValues(model), []string{"grok-4.6", "grok-4.5"}) {
		t.Errorf("model %+v", model)
	}
	if effort.Name != "Reasoning Effort" || effort.Current != "high" || !reflect.DeepEqual(choiceValues(effort), []string{"xhigh", "high", "medium", "low"}) || effort.Choices[0].Name != "X-High" {
		t.Errorf("effort %+v", effort)
	}
	if !reflect.DeepEqual(cap.Efforts, map[string][]string{"grok-4.6": {"xhigh", "high", "medium", "low"}, "grok-4.5": {"high", "medium", "low"}}) {
		t.Errorf("efforts %v", cap.Efforts)
	}
}

// Built-in DeepSeek Harness has Plan, its permission presets and agent
// presets, and no models until the machine has probed the endpoint.
func TestStaticDeepSeek(t *testing.T) {
	cap := staticOf(t, "deepseek")
	if ids := modeIDs(cap.Modes); !reflect.DeepEqual(ids, []string{"read-only", "workspace-write", "danger-full-access"}) || len(cap.Models) != 0 {
		t.Errorf("modes %+v models %+v", cap.Modes, cap.Models)
	}
	if ids := optionIDs(cap); !reflect.DeepEqual(ids, []string{"plan_mode", "mode", "agent_preset"}) {
		t.Fatalf("options %q", ids)
	}
	mode, preset := cap.Options[1], cap.Options[2]
	if mode.Name != "Permission" || mode.Category != "mode" || mode.Current != "workspace-write" || mode.Choices[2].Description != "Allow unrestricted file and command access without approval prompts." {
		t.Errorf("mode %+v", mode)
	}
	if preset.Category != "agent_preset" || preset.Current != "standard" || !reflect.DeepEqual(choiceValues(preset), []string{"standard", "ptc", "minimal", "cordis"}) || preset.Choices[3].Name != "Creator mode" {
		t.Errorf("preset %+v", preset)
	}
}

// Only a built-in on the runtime Lody installs has static capabilities,
// and only the kinds Lody's table lists; each is a copy of its own.
func TestStaticCapabilityRule(t *testing.T) {
	for _, c := range []struct {
		cliType, agentType string
		overridden         bool
	}{
		{"registry", "codex", false}, {"custom", "claude", false}, {"builtin", "pi", false}, {"builtin", "bub", false},
		{"builtin", "Claude", false}, {"builtin", "kimi", true}, {"builtin", "claude", true},
	} {
		if _, ok := StaticCapability(c.cliType, c.agentType, c.overridden); ok {
			t.Errorf("%+v has a static capability", c)
		}
	}
	cap := staticOf(t, "grok")
	cap.Options[3].Choices[0].Name = "changed"
	cap.Efforts["grok-4.6"][0] = "changed"
	cap.Modes[0].Name = "changed"
	if again := staticOf(t, "grok"); again.Options[3].Choices[0].Name != "X-High" || again.Efforts["grok-4.6"][0] != "xhigh" || again.Modes[0].Name != "Agent" {
		t.Errorf("the table changed: %+v", again)
	}
}

// A runtime override is a path that isn't blank, or Pi's extensions; a
// Codex profile's guard alone is none.
func TestRuntimeOverridden(t *testing.T) {
	for _, c := range []struct {
		row  map[string]any
		want bool
	}{
		{nil, false},
		{map[string]any{"runtimeOverrides": map[string]any{}}, false},
		{map[string]any{"runtimeOverrides": map[string]any{"kimiPath": "  "}}, false},
		{map[string]any{"runtimeOverrides": map[string]any{"kimiPath": " /opt/kimi "}}, true},
		{map[string]any{"runtimeOverrides": map[string]any{"piExtensions": []any{}}}, false},
		{map[string]any{"runtimeOverrides": map[string]any{"piExtensions": []any{"~/ext/a"}}}, true},
		{map[string]any{"runtimeOverrides": map[string]any{"codexPath": CodexProfileGuard}, "codexAuth": map[string]any{"mode": "chatgpt"}}, false},
		{map[string]any{"runtimeOverrides": map[string]any{"codexPath": CodexProfileGuard}}, true},
		{map[string]any{"runtimeOverrides": map[string]any{"codexPath": CodexProfileGuard, "grokPath": "/opt/grok"}, "codexAuth": map[string]any{}}, true},
	} {
		if got := (AgentConfig{Row: c.row}).RuntimeOverridden(); got != c.want {
			t.Errorf("%v: %v", c.row, got)
		}
	}
}

// An agent's capability is what its machine reported for its runtime;
// until then a built-in on Lody's runtime has the static one.
func TestCapabilityFor(t *testing.T) {
	c := Catalog{Agents: map[string][]AgentConfig{"m1": {
		{ID: "claude", AgentType: "claude", CLIType: "builtin"},
		{ID: "own", AgentType: "claude", CLIType: "builtin", Row: map[string]any{"runtimeOverrides": map[string]any{"claudeCodeExecutable": "/opt/claude"}}},
		{ID: "registry", AgentType: "codex", CLIType: "registry"},
		{ID: "changed", AgentType: "grok", CLIType: "builtin"},
	}}}
	if got := c.CapabilityFor("claude"); got.AgentType != "claude" || len(got.Options) != 4 || len(got.Models) != 5 {
		t.Errorf("static %+v", got)
	}
	for _, id := range []string{"own", "registry"} {
		if got := c.CapabilityFor(id); len(got.Options) != 0 || len(got.Models) != 0 || got.CLIType == "" || got.AgentType == "" {
			t.Errorf("%s %+v", id, got)
		}
	}
	// A report replaces it, even one without settings; one of another
	// runtime doesn't.
	c.Capabilities = map[string]Capability{
		"claude":  {Models: []Model{{ModelID: "opus"}}},
		"changed": {CLIType: "builtin", AgentType: "codex", Models: []Model{{ModelID: "gpt-5"}}},
	}
	if got := c.CapabilityFor("claude"); !reflect.DeepEqual(got, Capability{Models: []Model{{ModelID: "opus"}}, CLIType: "builtin", AgentType: "claude"}) {
		t.Errorf("reported %+v", got)
	}
	if got := c.CapabilityFor("changed"); got.AgentType != "grok" || got.Models[0].ModelID != "grok-4.6" {
		t.Errorf("another runtime's report %+v", got)
	}
	// An agent the catalog doesn't list keeps what was reported for it.
	c.Capabilities["gone"] = Capability{Steer: true}
	if got := c.CapabilityFor("gone"); !got.Steer {
		t.Errorf("gone %+v", got)
	}
}

// A built-in's modes are its static ones until its machine reports, its
// report's once it has; an agent the catalog doesn't list has those of a
// built-in of its type.
func TestModesFor(t *testing.T) {
	c := Catalog{Agents: map[string][]AgentConfig{"m1": {
		{ID: "kimi", AgentType: "kimi", CLIType: "builtin"},
		{ID: "own", AgentType: "codex", CLIType: "builtin", Row: map[string]any{"runtimeOverrides": map[string]any{"codexPath": "/opt/codex"}}},
	}}}
	if ids := modeIDs(c.ModesFor("kimi", "kimi")); !reflect.DeepEqual(ids, []string{"default", "plan", "auto", "yolo"}) {
		t.Errorf("kimi %q", ids)
	}
	if modes := c.ModesFor("own", "codex"); modes != nil {
		t.Errorf("overridden %+v", modes)
	}
	c.Capabilities = map[string]Capability{"kimi": {Modes: []Mode{{ID: "yolo", Name: "YOLO"}}}}
	if ids := modeIDs(c.ModesFor("kimi", "kimi")); !reflect.DeepEqual(ids, []string{"yolo"}) {
		t.Errorf("reported %q", ids)
	}
	if ids := modeIDs(c.ModesFor("gone", "deepseek")); !reflect.DeepEqual(ids, []string{"read-only", "workspace-write", "danger-full-access"}) {
		t.Errorf("gone %q", ids)
	}
}
