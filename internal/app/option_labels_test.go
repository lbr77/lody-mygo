package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/i18n"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// settingByID is the setting of opts with id.
func settingByID(t *testing.T, opts []catalog.ConfigOption, id string) catalog.ConfigOption {
	t.Helper()
	for _, o := range opts {
		if o.ID == id {
			return o
		}
	}
	t.Fatalf("no %s in %+v", id, opts)
	return catalog.ConfigOption{}
}

// choiceNames are o's values by the names the composer shows.
func choiceNames(o catalog.ConfigOption) []string {
	var out []string
	for _, ch := range o.Choices {
		out = append(out, ch.Name)
	}
	return out
}

func hasSetting(opts []catalog.ConfigOption, id string) bool {
	for _, o := range opts {
		if o.ID == id {
			return true
		}
	}
	return false
}

// Values lose "(recommended)"; Codex's models their "gpt-"; built-in
// Codex's full-access and auto-review modes take Lody's names.
func TestOptionLabels(t *testing.T) {
	codex := catalog.Capability{CLIType: "builtin", AgentType: "codex", Options: []catalog.ConfigOption{
		{ID: "model", Name: "Model", Category: "model", Type: "select", Current: "gpt-5.6-sol",
			Choices: []catalog.Choice{{Value: "gpt-5.6-sol", Name: "GPT-5.6-Sol (recommended)"}, {Value: "gpt-5.4", Name: "gpt-5.4"}}},
		{ID: "mode", Name: "Mode", Category: "mode", Type: "select", Current: "agent",
			Choices: []catalog.Choice{{Value: "agent-full-access", Name: "Agent (full access)"}, {Value: "agent-auto-review", Name: "Agent (auto review)"}, {Value: "agent", Name: "Agent (Recommended)"}}},
		{ID: "agent_preset", Name: "Preset", Type: "select", Current: "std", Choices: []catalog.Choice{{Value: "std", Name: "Standard (recommended)"}}},
	}}
	got := runSettings(codex, "")
	if names := choiceNames(settingByID(t, got, "model")); !reflect.DeepEqual(names, []string{"5.6-Sol", "5.4"}) {
		t.Errorf("models %q", names)
	}
	if names := choiceNames(settingByID(t, got, "mode")); !reflect.DeepEqual(names, []string{"Full access", "Auto review", "Agent"}) {
		t.Errorf("modes %q", names)
	}
	if names := choiceNames(settingByID(t, got, "agent_preset")); !reflect.DeepEqual(names, []string{"Standard"}) {
		t.Errorf("preset %q", names)
	}
	// The agent's own report is left as it was.
	if codex.Options[0].Choices[0].Name != "GPT-5.6-Sol (recommended)" {
		t.Errorf("report renamed: %+v", codex.Options[0])
	}

	// A Codex from the registry drops "gpt-" too, but keeps its modes'
	// names; another agent's models keep theirs.
	registry := catalog.Capability{CLIType: "registry", AgentType: "codex", Options: codex.Options}
	got = runSettings(registry, "")
	if names := choiceNames(settingByID(t, got, "mode")); names[0] != "Agent (full access)" || names[2] != "Agent" {
		t.Errorf("registry modes %q", names)
	}
	if modelLabel(registry, "GPT-5") != "5" || modelLabel(catalog.Capability{AgentType: "claude"}, "gpt-5 (recommended)") != "gpt-5" {
		t.Error("model labels")
	}
	modes := runModes(codex, []catalog.Mode{{ID: "agent-full-access", Name: "Agent (full access)"}, {ID: "agent", Name: "Agent"}})
	if modes[0].Name != "Full access" {
		t.Errorf("modes %+v", modes)
	}
}

// Built-in Grok's interaction and permission settings are named and
// described in Lody's words, in the app's language.
func TestGrokSettings(t *testing.T) {
	grok := catalog.Capability{CLIType: "builtin", AgentType: "grok", Options: []catalog.ConfigOption{
		{ID: "interaction_mode", Name: "interaction_mode", Type: "select", Current: "agent",
			Choices: []catalog.Choice{{Value: "agent", Name: "agent"}, {Value: "plan", Name: "plan"}, {Value: "ask", Name: "ask"}, {Value: "debug", Name: "Debug"}}},
		{ID: "permission_mode", Name: "permission_mode", Category: "_permission", Type: "select", Current: "ask",
			Choices: []catalog.Choice{{Value: "ask", Name: "ask"}, {Value: "auto", Name: "auto"}, {Value: "always-approve", Name: "always-approve"}}},
	}}
	got := runSettings(grok, "")
	interaction, permission := settingByID(t, got, "interaction_mode"), settingByID(t, got, "permission_mode")
	if interaction.Name != "Interaction Mode" || interaction.Description != "Controls whether Grok acts, plans, or answers read-only questions" {
		t.Errorf("interaction %+v", interaction)
	}
	if names := choiceNames(interaction); !reflect.DeepEqual(names, []string{"Agent", "Plan", "Ask", "Debug"}) {
		t.Errorf("interaction values %q", names)
	}
	if interaction.Choices[2].Description != "Answer questions without modifying the workspace" || interaction.Choices[3].Description != "" {
		t.Errorf("interaction lines %+v", interaction.Choices)
	}
	if names := choiceNames(permission); permission.Name != "Permission Mode" || !reflect.DeepEqual(names, []string{"Ask Every Time", "Auto", "Always Approve"}) {
		t.Errorf("permission %+v", permission)
	}
	if permission.Choices[1].Description != "Let Grok decide when approval is required (experimental)" {
		t.Errorf("permission lines %+v", permission.Choices)
	}
	if permissionControl(grok, permission, true) != "Permission" {
		t.Error("Grok's permission isn't named Permission")
	}

	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	defer i18n.SetLocaleFunc(func() string { return "en" })
	got = runSettings(grok, "")
	interaction, permission = settingByID(t, got, "interaction_mode"), settingByID(t, got, "permission_mode")
	if interaction.Name != "交互模式" || permission.Choices[0].Name != "每次询问" || permission.Choices[2].Description != "自动允许受保护的操作" {
		t.Errorf("zh %+v %+v", interaction, permission)
	}

	// A registry Grok's settings are its own.
	i18n.SetLocaleFunc(func() string { return "en" })
	registry := grok
	registry.CLIType = "registry"
	if o := settingByID(t, runSettings(registry, ""), "permission_mode"); o.Name != "permission_mode" || o.Choices[0].Name != "ask" {
		t.Errorf("registry Grok renamed: %+v", o)
	}
}

// The effort follows the model: its declared efforts, Codex's tiers, or the
// agent's per-model list, in that list's order and Lody's names.
func TestEffortFollowsModel(t *testing.T) {
	effort := catalog.ConfigOption{ID: "reasoning_effort", Name: "Reasoning Effort", Category: "thought_level", Type: "select", Current: "max",
		Choices: []catalog.Choice{{Value: "low", Name: "Low"}, {Value: "high", Name: "High"}}}

	// A per-model list, as Grok's, in its own order.
	grok := catalog.Capability{CLIType: "registry", AgentType: "grok", Options: []catalog.ConfigOption{effort},
		Efforts: map[string][]string{"grok-4.6": {"xhigh", "high", "medium", "low"}, "grok-4.5": {}}}
	o := settingByID(t, runSettings(grok, "grok-4.6"), "reasoning_effort")
	if names := choiceNames(o); !reflect.DeepEqual(names, []string{"X-High", "High", "Medium", "Low"}) || o.Current != "medium" {
		t.Errorf("grok-4.6 %q current %v", names, o.Current)
	}
	// An empty list, or a model the list leaves out, keeps the report.
	for _, model := range []string{"grok-4.5", "grok-5"} {
		if names := choiceNames(settingByID(t, runSettings(grok, model), "reasoning_effort")); !reflect.DeepEqual(names, []string{"Low", "High"}) {
			t.Errorf("%s %q", model, names)
		}
	}

	// Codex's Max and Ultra only where the model reaches them.
	codex := catalog.Capability{CLIType: "builtin", AgentType: "codex", Options: []catalog.ConfigOption{{ID: "reasoning_effort", Category: "thought_level", Type: "select", Current: "ultra",
		Choices: []catalog.Choice{{Value: "low", Name: "Low"}, {Value: "medium", Name: "Medium"}, {Value: "xhigh", Name: "XHigh"}, {Value: "max", Name: "Max"}}}},
		Efforts: map[string][]string{"gpt-5.4": {"low"}}}
	o = settingByID(t, runSettings(codex, "gpt-5.6-sol"), "reasoning_effort")
	if names := choiceNames(o); !reflect.DeepEqual(names, []string{"Low", "Medium", "XHigh", "Max", "Ultra"}) || o.Current != "ultra" {
		t.Errorf("sol %q current %v", names, o.Current)
	}
	if o.Choices[4].Description != "Maximum reasoning with automatic task delegation" {
		t.Errorf("ultra %+v", o.Choices[4])
	}
	o = settingByID(t, runSettings(codex, "gpt-5.4"), "reasoning_effort")
	if names := choiceNames(o); !reflect.DeepEqual(names, []string{"Low", "Medium", "XHigh"}) || o.Current != "medium" {
		t.Errorf("5.4 %q current %v", names, o.Current)
	}

	// Claude's declared efforts, its default first; a model declared
	// without any has no effort, and one the agent left out keeps the
	// report.
	yes, no := true, false
	claude := catalog.Capability{CLIType: "builtin", AgentType: "claude", Options: []catalog.ConfigOption{
		{ID: "model", Category: "model", Type: "select", Current: "opus"},
		{ID: "effort", Name: "Effort", Category: "thought_level", Type: "select", Current: "max", Choices: []catalog.Choice{{Value: "max", Name: "Max"}}},
		{ID: "fast", Name: "Fast mode", Type: "boolean", Current: false},
	}, Declared: map[string]catalog.ModelControls{
		"opus":  {HasEfforts: true, Efforts: []string{"low", "medium", "high", "xhigh"}, Fast: &yes},
		"haiku": {Fast: &no},
	}}
	o = settingByID(t, runSettings(claude, ""), "effort")
	if names := choiceNames(o); !reflect.DeepEqual(names, []string{"Default", "Low", "Medium", "High", "X-High"}) || o.Current != "default" {
		t.Errorf("opus %q current %v", names, o.Current)
	}
	haiku := runSettings(claude, "haiku")
	if hasSetting(haiku, "effort") || hasSetting(haiku, "fast") {
		t.Errorf("haiku %+v", haiku)
	}
	if o := settingByID(t, runSettings(claude, "sonnet"), "effort"); !reflect.DeepEqual(choiceNames(o), []string{"Max"}) {
		t.Errorf("sonnet %+v", o)
	}
	// A model with them gets the effort and Fast even where the report,
	// of another model, had neither.
	bare := claude
	bare.Options = bare.Options[:1]
	opus := runSettings(bare, "opus")
	if o := settingByID(t, opus, "effort"); o.Name != "Effort" || len(o.Choices) != 5 {
		t.Errorf("added effort %+v", o)
	}
	if o := settingByID(t, opus, "fast"); o.Type != "boolean" || o.Name != "Fast mode" {
		t.Errorf("added fast %+v", o)
	}
	// An agent Lody has no binding for never gets a setting guessed.
	other := bare
	other.CLIType = "registry"
	if got := runSettings(other, "opus"); hasSetting(got, "effort") || hasSetting(got, "fast") {
		t.Errorf("guessed %+v", got)
	}
	if effortLabel("minimal") != "Minimal" || effortLabel("") != "" {
		t.Error("effort labels")
	}
}

// What the model hides stays out of the message, as an effort it doesn't
// allow does.
func TestChosenRunValuesFollowModel(t *testing.T) {
	a := fixture()
	yes, no := true, false
	a.catalog.Capabilities = map[string]catalog.Capability{"a1": {Options: []catalog.ConfigOption{
		{ID: "effort", Category: "thought_level", Type: "select", Current: "high", Choices: []catalog.Choice{{Value: "high", Name: "High"}, {Value: "max", Name: "Max"}}},
		{ID: "fast", Type: "boolean", Current: false},
		{ID: "web_search", Type: "boolean", Current: false},
	}, Declared: map[string]catalog.ModelControls{"opus": {HasEfforts: true, Efforts: []string{"high"}, Fast: &yes}, "haiku": {Fast: &no}}}}
	a.runValues["k"] = map[string]any{"effort": "max", "fast": true, "web_search": true, "agent_only": "x"}
	if got := a.chosenRunValues("k", "a1", "opus"); !reflect.DeepEqual(got, map[string]any{"fast": true, "web_search": true, "agent_only": "x"}) {
		t.Errorf("opus %v", got)
	}
	a.runValues["k"]["effort"] = "high"
	if got := a.chosenRunValues("k", "a1", "haiku"); !reflect.DeepEqual(got, map[string]any{"web_search": true, "agent_only": "x"}) {
		t.Errorf("haiku %v", got)
	}
	if got := a.chosenRunValues("k", "a1", "opus"); got["effort"] != "high" {
		t.Errorf("opus effort %v", got)
	}
}

// The permission button names what it sets, Lody's way, and its menu says
// beside a mode what it allows once the pointer rests on it.
func TestPermissionDescriptions(t *testing.T) {
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	a.catalog.Sessions[0].AgentConfigID = "a1"
	a.catalog.Capabilities = map[string]catalog.Capability{"a1": {Modes: []catalog.Mode{
		{ID: "default", Name: "Manual (recommended)", Description: "Standard behavior, prompts for dangerous operations"},
		{ID: "bypassPermissions", Name: "Bypass Permissions"},
	}, Options: []catalog.ConfigOption{{ID: "mode", Category: "mode", Type: "select", Current: "default",
		Choices: []catalog.Choice{{Value: "bypassPermissions", Name: "Bypass Permissions", Description: "Bypass all permission checks"}}}}}}
	tt := snap(t, a, "permission-descriptions", false, nil)
	face := locales.RunConfigMode() + ": " + locales.ModeDefault()
	if err := tt.Click(face); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	// Named as Lody names them, the mode setting's line standing in for
	// one the mode lacks.
	r, ok := tt.Find("Manual")
	if !ok || !tt.HasText("Bypass Permissions") {
		t.Fatalf("modes: %q", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if tt.HasText("Standard behavior") {
		t.Fatal("a description showed before the pointer rested")
	}
	tt.Advance(310 * time.Millisecond)
	tt.Frame()
	tip, ok := tt.Find("Standard behavior, prompts for dangerous operations")
	if !ok {
		t.Fatalf("no description after 300ms: %q", tt.Texts())
	}
	// Its text sits inside the chip's 4×8 padding, the chip 4px right of
	// the row, their tops level.
	if tip.X < r.X+r.W+4 || tip.Y < r.Y || tip.Y > r.Y+8 {
		t.Errorf("description at %+v, not beside its row %+v", tip, r)
	}
	snapFrame(t, tt, "permission-descriptions-tip")
	bypass, _ := tt.Find("Bypass Permissions")
	tt.Move(bypass.X+bypass.W/2, bypass.Y+bypass.H/2)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	if !tt.HasText("Bypass all permission checks") || tt.HasText("Standard behavior") {
		t.Errorf("descriptions: %q", tt.Texts())
	}
	if err := tt.Click("Bypass Permissions"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.modes["s1"] != "bypassPermissions" {
		t.Fatalf("mode %q", a.modes["s1"])
	}
	tt.Advance(time.Second)
	tt.Frame()
	if _, ok := tt.Find(locales.RunConfigMode() + ": Bypass Permissions"); !ok {
		t.Errorf("face: %q", tt.Texts())
	}
}

// In a conversation with built-in Grok, the run-configuration menu and the
// permission button speak Lody's words for Grok's settings.
func TestGrokRunConfig(t *testing.T) {
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	a.catalog.Agents["m1"] = append(a.catalog.Agents["m1"], catalog.AgentConfig{ID: "g1", Name: "Grok", MachineID: "m1", AgentType: "grok", CLIType: "builtin"})
	a.catalog.Sessions[0].AgentConfigID = "g1"
	a.catalog.Sessions[0].AgentType = "grok"
	a.catalog.Capabilities = map[string]catalog.Capability{"g1": {Options: []catalog.ConfigOption{
		{ID: "interaction_mode", Name: "interaction_mode", Type: "select", Current: "plan",
			Choices: []catalog.Choice{{Value: "agent", Name: "agent"}, {Value: "plan", Name: "plan"}, {Value: "ask", Name: "ask"}}},
		{ID: "permission_mode", Name: "permission_mode", Category: "_permission", Type: "select", Current: "ask",
			Choices: []catalog.Choice{{Value: "ask", Name: "ask"}, {Value: "always-approve", Name: "always-approve"}}},
	}}}
	tt := snap(t, a, "grok-run-config", false, nil)
	if _, ok := tt.Find(locales.RunConfigPermission() + ": Ask Every Time"); !ok {
		t.Fatalf("permission face: %q", tt.Texts())
	}
	if err := tt.Click(locales.RunConfigTitle()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Interaction Mode") || !tt.HasText("Plan") || tt.HasText("interaction_mode") {
		t.Fatalf("menu: %q", tt.Texts())
	}
	if err := tt.Click("Interaction Mode"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Ask"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	if a.runValues["s1"]["interaction_mode"] != "ask" {
		t.Fatalf("values %v", a.runValues["s1"])
	}
}
