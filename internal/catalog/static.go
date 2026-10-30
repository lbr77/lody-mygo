package catalog

import (
	"maps"
	"slices"
	"strings"
)

// Lody's static capabilities for its built-in agents
// (STATIC_BUILTIN_ACP_CAPABILITIES): the modes, models and settings
// built-in Claude, Codex, Kimi, Grok and DeepSeek Harness start with, so
// their composers have them before a machine has started the agent and
// reported what it supports. They are for the first render only, never a
// probe: a machine's report for the agent replaces them, and a built-in
// run from a runtime of the user's own never has them, as that runtime
// may support anything. Built-ins not listed, as Pi and Devin, have none.

// staticCapability is a built-in's static capability, as Lody's table
// has it: its modes, models, settings and each model's efforts.
type staticCapability struct {
	modes   []Mode
	models  []Model
	options []ConfigOption
	efforts map[string][]string
}

// planModeOption is Lody's Plan switch, off (createPlanModeConfigOption):
// it plans before implementing and leaves permissions alone.
var planModeOption = ConfigOption{ID: "plan_mode", Name: "Plan", Type: "boolean", Current: false,
	Description: "Plan before implementing; permissions are controlled separately."}

// modeChoices and modelChoices are modes and models as a select's values.
func modeChoices(modes []Mode) []Choice {
	out := make([]Choice, len(modes))
	for i, m := range modes {
		out[i] = Choice{Value: m.ID, Name: m.Name, Description: m.Description}
	}
	return out
}

func modelChoices(models []Model) []Choice {
	out := make([]Choice, len(models))
	for i, m := range models {
		out[i] = Choice{Value: m.ModelID, Name: firstText(m.Name, m.ModelID), Description: m.Description}
	}
	return out
}

// Built-in Codex: its approval modes, from read-only to full access, the
// GPT models, its reasoning efforts, Fast and Plan. It starts on Auto
// review and GPT-5.6-Sol at medium effort.
var (
	codexModes = []Mode{
		{"read-only", "Read-only", "Requires approval to edit files and run commands."},
		{"agent", "Agent", "Read and edit files, and run commands."},
		{"agent-auto-review", "Auto review", "Read and edit files, and run commands. Approval requests are reviewed automatically by a Codex subagent."},
		{"agent-full-access", "Full access", "Codex can edit files outside this workspace and run commands with network access. Exercise caution when using."},
	}
	codexModels = []Model{
		{"gpt-5.6-sol", "GPT-5.6-Sol", "Latest frontier agentic coding model."},
		{"gpt-5.6-terra", "GPT-5.6-Terra", "Balanced agentic coding model for everyday work."},
		{"gpt-5.6-luna", "GPT-5.6-Luna", "Fast and affordable agentic coding model."},
		{"gpt-5.5", "GPT-5.5", "Frontier model for complex coding, research, and real-world work."},
		{"gpt-5.4", "GPT-5.4", "Strong model for everyday coding."},
		{"gpt-5.4-mini", "GPT-5.4-Mini", "Small, fast, and cost-efficient model for simpler coding tasks."},
	}
	codexStatic = staticCapability{
		modes:  codexModes,
		models: codexModels,
		options: []ConfigOption{
			{ID: "mode", Name: "Mode", Description: "Approval and sandboxing preset for the session", Category: "mode", Type: "select",
				Current: "agent-auto-review", Choices: modeChoices(codexModes)},
			{ID: "model", Name: "Model", Description: "Model Codex uses for the session", Category: "model", Type: "select",
				Current: "gpt-5.6-sol", Choices: modelChoices(codexModels)},
			{ID: "reasoning_effort", Name: "Reasoning effort", Description: "How much reasoning effort the model should use", Category: "thought_level", Type: "select",
				Current: "medium", Choices: []Choice{
					{"low", "Low", "Fastest responses"},
					{"medium", "Medium", "Balanced reasoning"},
					{"high", "High", "More reasoning for difficult tasks"},
					{"xhigh", "XHigh", "Extra reasoning for complex tasks"},
				}},
			{ID: "fast-mode", Name: "Fast mode", Description: "1.5x speed, increased usage", Category: "model_config", Type: "boolean", Current: false},
			planModeOption,
		},
	}
)

// Built-in Claude: Claude Code's permission modes, its model aliases, its
// effort and Fast. It starts on Auto, the default model and the default
// effort, which leaves the effort to the provider.
var (
	claudeModes = []Mode{
		{"auto", "Auto", "Use a model classifier to approve/deny permission prompts"},
		{"default", "Manual", "Standard behavior, prompts for dangerous operations"},
		{"acceptEdits", "Accept Edits", "Auto-accept file edit operations"},
		{"plan", "Plan Mode", "Planning mode, no actual tool execution"},
		{"dontAsk", "Don't Ask", "Don't prompt for permissions, deny if not pre-approved"},
		{"bypassPermissions", "Bypass Permissions", "Bypass all permission checks"},
	}
	claudeModels = []Model{
		{"default", "Default (recommended)", "Opus (1M context)"},
		{"opus[1m]", "Opus (1M context)", "Opus 5 with 1M context · Best for everyday, complex tasks"},
		{"claude-fable-5-1[1m]", "Fable", "Fable 5.1 · Most capable for your hardest and longest-running tasks"},
		{"sonnet", "Sonnet", "Sonnet 5 · Efficient for routine tasks"},
		{"haiku", "Haiku", "Haiku 4.5 · Fastest for quick answers"},
	}
	claudeStatic = staticCapability{
		modes:  claudeModes,
		models: claudeModels,
		options: []ConfigOption{
			{ID: "mode", Name: "Mode", Description: "Session permission mode", Category: "mode", Type: "select",
				Current: "auto", Choices: modeChoices(claudeModes)},
			{ID: "model", Name: "Model", Description: "AI model to use", Category: "model", Type: "select",
				Current: "default", Choices: modelChoices(claudeModels)},
			{ID: "effort", Name: "Effort", Description: "Available effort levels for this model", Category: "thought_level", Type: "select",
				Current: "default", Choices: []Choice{
					{Value: "default", Name: "Default"},
					{Value: "low", Name: "Low"},
					{Value: "medium", Name: "Medium"},
					{Value: "high", Name: "High"},
					{Value: "xhigh", Name: "Xhigh"},
					{Value: "max", Name: "Max"},
				}},
			{ID: "fast", Name: "Fast mode", Description: "Faster responses on supported models", Category: "model_config", Type: "boolean", Current: false},
		},
	}
)

// Built-in Kimi: its modes, and its permission setting of them but Plan,
// which is Lody's Plan switch instead; it starts on Auto. Its models
// depend on the account, so it has none until the machine reports.
var (
	kimiModes = []Mode{
		{"default", "Default", "Manual approvals; tools execute normally."},
		{"plan", "Plan", "Read-only planning; no tool execution."},
		{"auto", "Auto", "Auto-approve safe operations."},
		{"yolo", "YOLO", "Auto-approve everything."},
	}
	kimiStatic = staticCapability{
		modes: kimiModes,
		options: []ConfigOption{
			{ID: "permission_mode", Name: "Permission", Category: "_permission", Type: "select",
				Current: "auto", Choices: modeChoices(slices.DeleteFunc(slices.Clone(kimiModes), func(m Mode) bool { return m.ID == "plan" }))},
			planModeOption,
		},
	}
)

// Built-in Grok: as its official runtime's compatibility adapter has it,
// Plan, its permission setting, its models and their efforts. It starts
// asking every time, on Grok 4.6 at high effort; Grok 4.5 doesn't reach
// X-High.
var (
	grokModes = []Mode{
		{"default", "Agent", "Use tools and make changes when needed"},
		{"plan", "Plan", "Plan and reason without modifying the workspace"},
	}
	grokModels = []Model{
		{"grok-4.6", "Grok 4.6", "SpaceXAI's latest frontier model"},
		{ModelID: "grok-4.5", Name: "Grok 4.5"},
	}
	grokStatic = staticCapability{
		modes:  grokModes,
		models: grokModels,
		options: []ConfigOption{
			planModeOption,
			{ID: "permission_mode", Name: "Permission Mode", Description: "Controls how protected tool actions are approved", Category: "_permission", Type: "select",
				Current: "ask", Choices: []Choice{
					{"ask", "Ask Every Time", "Request approval before protected actions"},
					{"always-approve", "Always Approve", "Approve protected actions automatically"},
				}},
			{ID: "model", Name: "Model", Description: "Select the model used for this session", Category: "model", Type: "select",
				Current: "grok-4.6", Choices: modelChoices(grokModels)},
			{ID: "reasoning_effort", Name: "Reasoning Effort", Description: "Controls how much reasoning the model performs", Category: "thought_level", Type: "select",
				Current: "high", Choices: []Choice{
					{Value: "xhigh", Name: "X-High"},
					{Value: "high", Name: "High"},
					{Value: "medium", Name: "Medium"},
					{Value: "low", Name: "Low"},
				}},
		},
		efforts: map[string][]string{
			"grok-4.6": {"xhigh", "high", "medium", "low"},
			"grok-4.5": {"high", "medium", "low"},
		},
	}
)

// Built-in DeepSeek Harness: Plan, its permission presets, from read-only
// to full access, and the agent presets its CLI ships. It starts writing
// to the workspace in the standard preset. Its models depend on the
// endpoint, so it has none until the machine reports.
var (
	deepseekModes = []Mode{
		{"read-only", "Read-only", "Read inside the workspace; protected writes require one-time approval."},
		{"workspace-write", "Workspace write", "Read and write inside the workspace; wider access requires one-time approval."},
		{"danger-full-access", "Full access", "Allow unrestricted file and command access without approval prompts."},
	}
	deepseekStatic = staticCapability{
		modes: deepseekModes,
		options: []ConfigOption{
			planModeOption,
			{ID: "mode", Name: "Permission", Description: "Sandbox and approval policy for the session", Category: "mode", Type: "select",
				Current: "workspace-write", Choices: modeChoices(deepseekModes)},
			{ID: "agent_preset", Name: "Agent preset", Description: "Tools, prompt, and capabilities composed for the session", Category: "agent_preset", Type: "select",
				Current: "standard", Choices: []Choice{
					{"standard", "Standard mode", "Full coding agent with file editing, shell, search, skills, planning, goals, subagents, and workflows."},
					{"ptc", "PTC mode", "Full coding agent without the workflow tools; other tools are exposed through the PTC SDK so one TypeScript program composes multi-step work."},
					{"minimal", "Minimal mode", "Single-tool coding agent with a persistent shell."},
					{"cordis", "Creator mode", "Standard capabilities plus runtime inspection, plugin experiments, and preset-authoring guidance."},
				}},
		},
	}
)

// staticCapabilities are Lody's static capabilities by built-in agent
// type.
var staticCapabilities = map[string]staticCapability{
	"claude":   claudeStatic,
	"codex":    codexStatic,
	"kimi":     kimiStatic,
	"grok":     grokStatic,
	"deepseek": deepseekStatic,
}

// StaticCapability is Lody's static capability for an agent
// (getStaticBuiltinAcpCapabilities): for a built-in of a type Lody's
// table lists, run on the runtime Lody installs, a copy of what the table
// has; for any other, ok is false, as for a built-in overridden with a
// runtime of the user's own.
func StaticCapability(cliType, agentType string, overridden bool) (Capability, bool) {
	s, ok := staticCapabilities[agentType]
	if cliType != "builtin" || !ok || overridden {
		return Capability{}, false
	}
	cap := Capability{CLIType: cliType, AgentType: agentType, Modes: slices.Clone(s.modes), Models: slices.Clone(s.models)}
	for _, o := range s.options {
		o.Choices = slices.Clone(o.Choices)
		cap.Options = append(cap.Options, o)
	}
	if s.efforts != nil {
		cap.Efforts = maps.Clone(s.efforts)
		for model, efforts := range cap.Efforts {
			cap.Efforts[model] = slices.Clone(efforts)
		}
	}
	return cap, true
}

// CodexProfileGuard stands in for the Codex path of a config that signs
// in with a profile, so a machine too old for profiles fails rather than
// use its global login.
const CodexProfileGuard = "\x00lody-codex-profile-v1"

// RuntimeOverridden is whether the config runs its built-in agent from a
// runtime of the user's own, as Lody's hasBuiltinRuntimeOverrideValues
// reads its runtimeOverrides: a path that isn't blank, or Pi extensions.
// A profile's Codex guard alone is none, as Lody drops it on reading a
// config that signs in with a profile.
func (ag AgentConfig) RuntimeOverridden() bool {
	over, _ := ag.Row["runtimeOverrides"].(map[string]any)
	if _, profile := ag.Row["codexAuth"].(map[string]any); profile && len(over) == 1 && over["codexPath"] == CodexProfileGuard {
		return false
	}
	for _, v := range over {
		switch v := v.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return true
			}
		case []any:
			if len(v) > 0 {
				return true
			}
		case []string:
			if len(v) > 0 {
				return true
			}
		}
	}
	return false
}

// agentConfig is the agent config with id, of any machine.
func (c *Catalog) agentConfig(id string) *AgentConfig {
	for _, agents := range c.Agents {
		for i := range agents {
			if agents[i].ID == id {
				return &agents[i]
			}
		}
	}
	return nil
}

// CapabilityFor is an agent config's capability as Lody's composer
// resolves it: what its machine last reported for the agent's runtime,
// knowing which runtime it is even where the report doesn't say; with no
// report for that runtime yet, Lody's static capability for a built-in on
// the runtime Lody installs. A report of another runtime, from before the
// agent was changed, isn't the agent's.
func (c *Catalog) CapabilityFor(agentConfigID string) Capability {
	cap, reported := c.Capabilities[agentConfigID]
	ag := c.agentConfig(agentConfigID)
	if ag == nil {
		return cap
	}
	same := func(a, b string) bool { return a == "" || b == "" || a == b }
	if reported && same(cap.CLIType, ag.CLIType) && same(cap.AgentType, ag.AgentType) {
		cap.CLIType, cap.AgentType = firstText(cap.CLIType, ag.CLIType), firstText(cap.AgentType, ag.AgentType)
		return cap
	}
	if static, ok := StaticCapability(ag.CLIType, ag.AgentType, ag.RuntimeOverridden()); ok {
		return static
	}
	return Capability{CLIType: ag.CLIType, AgentType: ag.AgentType}
}
