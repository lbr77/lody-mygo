package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// staticFixture adds built-in Codex and Grok, and a Claude run from the
// user's own executable, none of which its machine has reported yet.
func staticFixture() *App {
	a := fixture()
	a.catalog.Agents["m1"] = append(a.catalog.Agents["m1"],
		catalog.AgentConfig{ID: "c1", Name: "Codex", MachineID: "m1", AgentType: "codex", CLIType: "builtin"},
		catalog.AgentConfig{ID: "g1", Name: "Grok", MachineID: "m1", AgentType: "grok", CLIType: "builtin"},
		catalog.AgentConfig{ID: "o1", Name: "My Claude", MachineID: "m1", AgentType: "claude", CLIType: "builtin",
			Row: map[string]any{"runtimeOverrides": map[string]any{"claudeCodeExecutable": "/opt/claude"}}})
	return a
}

// Before its machine reports, a built-in's composer offers Lody's static
// settings, named, ordered and fitted to the model as a report's are.
func TestStaticRunSettings(t *testing.T) {
	a := staticFixture()

	claude := a.runCapability("a1", "")
	if names := choiceNames(settingByID(t, claude.Options, "model")); !reflect.DeepEqual(names, []string{"Default", "Opus (1M context)", "Fable", "Sonnet", "Haiku"}) {
		t.Errorf("claude models %q", names)
	}
	if names := choiceNames(settingByID(t, claude.Options, "effort")); !reflect.DeepEqual(names, []string{"Default", "Low", "Medium", "High", "Xhigh", "Max"}) {
		t.Errorf("claude efforts %q", names)
	}
	if o, ok := fastOption(claude); !ok || o.ID != "fast" {
		t.Errorf("claude fast %+v", o)
	}
	if modes := a.catalog.ModesFor("a1", "claude"); len(modes) != 6 || modes[0].ID != "auto" {
		t.Errorf("claude modes %+v", modes)
	}

	// Codex's models drop their "gpt-", its modes take Lody's names, and
	// its efforts reach Max and Ultra only on the models that have them.
	codex := a.runCapability("c1", "")
	if names := choiceNames(settingByID(t, codex.Options, "model")); names[0] != "5.6-Sol" || names[5] != "5.4-Mini" {
		t.Errorf("codex models %q", names)
	}
	if names := choiceNames(settingByID(t, codex.Options, "mode")); !reflect.DeepEqual(names, []string{"Read-only", "Agent", "Auto review", "Full access"}) {
		t.Errorf("codex modes %q", names)
	}
	if o := settingByID(t, codex.Options, "reasoning_effort"); !reflect.DeepEqual(choiceNames(o), []string{"Low", "Medium", "High", "XHigh", "Max", "Ultra"}) || o.Current != "medium" {
		t.Errorf("sol effort %+v", o)
	}
	if o := settingByID(t, a.runCapability("c1", "gpt-5.4").Options, "reasoning_effort"); !reflect.DeepEqual(choiceNames(o), []string{"Low", "Medium", "High", "XHigh"}) {
		t.Errorf("5.4 effort %+v", o)
	}
	if selects, switches := menuOptions(codex); len(selects) != 0 || len(switches) != 1 || switches[0].ID != "plan_mode" {
		t.Errorf("codex menu %+v %+v", selects, switches)
	}
	if models := pickerModels(a.capability("c1")); len(models) != 6 || a.modelCycle("c1", "", nil).values[1] != "gpt-5.6-terra" {
		t.Errorf("codex picker %+v", models)
	}

	// Grok's efforts follow its per-model list, and its permission speaks
	// Lody's words.
	if o := settingByID(t, a.runCapability("g1", "grok-4.5").Options, "reasoning_effort"); !reflect.DeepEqual(choiceNames(o), []string{"High", "Medium", "Low"}) || o.Current != "high" {
		t.Errorf("grok-4.5 effort %+v", o)
	}
	perm, ok := permissionOption(a.runCapability("g1", ""), a.catalog.ModesFor("g1", "grok"))
	if !ok || perm.ID != "permission_mode" || !reflect.DeepEqual(choiceNames(perm), []string{locales.GrokPermissionAsk(), locales.GrokPermissionAlwaysApprove()}) {
		t.Errorf("grok permission %+v", perm)
	}

	// A runtime of the user's own may offer anything: nothing until it
	// reports. A report replaces the static settings.
	if own := a.runCapability("o1", ""); len(own.Options) != 0 || len(a.catalog.ModesFor("o1", "claude")) != 0 {
		t.Errorf("overridden %+v", own)
	}
	a.catalog.Capabilities = map[string]catalog.Capability{"c1": {Options: []catalog.ConfigOption{
		{ID: "model", Category: "model", Type: "select", Current: "gpt-6-sol", Choices: []catalog.Choice{{Value: "gpt-6-sol", Name: "GPT-6-Sol"}}},
	}}}
	if got := a.runCapability("c1", ""); len(got.Options) != 1 || hasSetting(got.Options, "plan_mode") {
		t.Errorf("reported %+v", got.Options)
	}
}

// A conversation with built-in Grok, before the machine has reported,
// shows Grok's model, effort and permission, and Plan in its menu.
func TestStaticComposer(t *testing.T) {
	a := staticFixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	a.view.ModelID = "grok-4.6"
	a.catalog.Sessions[0].AgentConfigID = "g1"
	a.catalog.Sessions[0].AgentType = "grok"
	tt := snap(t, a, "static-composer", false, nil)
	if !tt.HasText("Grok 4.6") || !tt.HasText("High") {
		t.Fatalf("model and effort: %q", tt.Texts())
	}
	if _, ok := tt.Find(locales.RunConfigPermission() + ": " + locales.GrokPermissionAsk()); !ok {
		t.Fatalf("permission face: %q", tt.Texts())
	}
	if err := tt.Click(locales.RunConfigTitle()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !tt.HasText(locales.RunConfigPlan()) {
		t.Fatalf("menu: %q", tt.Texts())
	}
	if err := tt.Click(locales.RunConfigPlan()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.runValues["s1"]["plan_mode"] != true {
		t.Fatalf("values %v", a.runValues["s1"])
	}
}

// Lody's agent dialog takes a built-in's static settings as known, so
// title generation offers them before a test; not Kimi's, nor those of a
// runtime of the user's own.
func TestStaticFormCapability(t *testing.T) {
	a := fixture()
	kind := func(value string) providerKind {
		for _, k := range providerKinds {
			if k.value == value {
				return k
			}
		}
		t.Fatalf("no kind %s", value)
		return providerKind{}
	}
	f := &providerForm{id: "new", kind: kind("builtin:codex"), title: map[string]any{}}
	if cap, ready := a.formCapability(f); !ready || !hasSetting(cap.Options, "reasoning_effort") {
		t.Errorf("codex %v %+v", ready, cap)
	}
	f.codex = &codexAuth{mode: "chatgpt"}
	if _, ready := a.formCapability(f); !ready {
		t.Error("a Codex profile isn't Lody's runtime")
	}
	f.codex, f.path = nil, "/opt/codex"
	if _, ready := a.formCapability(f); ready {
		t.Error("a Codex of the user's own is ready")
	}
	f = &providerForm{id: "new", kind: kind("builtin:kimi"), title: map[string]any{}}
	if _, ready := a.formCapability(f); ready {
		t.Error("Kimi is ready before a test")
	}
	f = &providerForm{id: "new", kind: kind("builtin:pi"), title: map[string]any{}}
	if _, ready := a.formCapability(f); ready {
		t.Error("Pi has static settings")
	}
}
