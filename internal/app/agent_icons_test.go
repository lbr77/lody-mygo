package app

import (
	"strings"
	"testing"
)

// Every bundled mark parses, and agents and models take Lody's: a brand
// before the runtime, the registry's own, the robot when nothing says.
func TestAgentMarks(t *testing.T) {
	files, err := agentSVGFiles.ReadDir("assets/agents")
	if err != nil || len(files) < 45 {
		t.Fatalf("marks: %d, %v", len(files), err)
	}
	for _, f := range files {
		if agentSVG(strings.TrimSuffix(f.Name(), ".svg")) == nil {
			t.Errorf("%s does not parse", f.Name())
		}
	}
	for _, tc := range []struct {
		cli, agent string
		row        map[string]any
		want       string
	}{
		{"builtin", "claude", nil, "brand-anthropic"},
		{"builtin", "codex", nil, "brand-openai"},
		{"builtin", "claude", map[string]any{"brandId": "deepseek"}, "brand-deepseek"},
		{"builtin", "claude", map[string]any{"brandId": "mimo"}, "brand-mimo"},
		{"builtin", "claude", map[string]any{"env": map[string]any{"ANTHROPIC_BASE_URL": "https://api.xiaomimimo.com/anthropic"}}, "brand-mimo"},
		{"builtin", "claude", map[string]any{"env": map[string]any{"ANTHROPIC_BASE_URL": "https://open.bigmodel.cn/api/anthropic"}}, "brand-glm"},
		{"builtin", "claude", map[string]any{"env": map[string]any{"ANTHROPIC_BASE_URL": "https://api.minimax.io/anthropic"}}, "brand-minimax"},
		{"builtin", "pi", nil, "pi-acp"},
		{"builtin", "grok", nil, "grok-build"},
		{"builtin", "kimi", nil, "kimi"},
		{"builtin", "bub", nil, "builtin-bub"},
		{"registry", "gemini", nil, "gemini"},
		{"registry", "claude-p", nil, "brand-anthropic"},
	} {
		if got, want := agentMark(tc.cli, tc.agent, tc.row), agentSVG(tc.want); got != want {
			t.Errorf("%s/%s %v: not %s", tc.cli, tc.agent, tc.row, tc.want)
		}
	}
	if agentMark("custom", "x", nil) != iconSquareTerminal || agentMark("registry", "nobody", nil) != iconBot {
		t.Error("custom and unknown marks")
	}
	for id, want := range map[string]string{"claude-opus-5-5": "brand-anthropic", "openrouter:google/gemini-2.5-pro": "gemini", "gpt-5": "brand-openai", "o3-mini": "brand-openai", "qwq-32b": "qwen-code", "devstral-small": "mistral-vibe", "xiaomi/mimo-v2-flash": "brand-mimo"} {
		if modelMark(id) != agentSVG(want) {
			t.Errorf("model %s: not %s", id, want)
		}
	}
	if modelMark("llama-4") != iconSparkles {
		t.Error("unknown model mark")
	}
}
