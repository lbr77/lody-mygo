package app

import (
	"embed"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
)

// An agent shows Lody's mark for it: its provider's brand for a preset
// over Claude Code (DeepSeek, MiniMax, GLM), Anthropic's for Claude,
// OpenAI's for Codex, the registry's own for an ACP agent, a terminal for
// a custom command, and a robot when nothing else says. The marks are
// Lody's, drawn in the text's ink where they take currentColor.

//go:embed assets/agents/*.svg
var agentSVGFiles embed.FS

var agentSVGs sync.Map // file name to *ui.SVG, or nil when it can't be read

// agentSVG is an embedded mark by its file's name, nil when there is none.
func agentSVG(name string) *ui.SVG {
	if v, ok := agentSVGs.Load(name); ok {
		svg, _ := v.(*ui.SVG)
		return svg
	}
	var svg *ui.SVG
	if data, err := agentSVGFiles.ReadFile("assets/agents/" + name + ".svg"); err == nil {
		svg, _ = ui.ParseSVG(data)
	}
	agentSVGs.Store(name, svg)
	return svg
}

// brandHosts are the providers a Claude config's ANTHROPIC_BASE_URL can
// name, for configs from before their brand was kept.
var brandHosts = []struct {
	brand string
	hosts []string
}{
	{"deepseek", []string{"deepseek.com"}},
	{"mimo", []string{"xiaomimimo.com"}},
	{"minimax", []string{"minimaxi.com", "minimax.io"}},
	{"glm", []string{"bigmodel.cn", "z.ai"}},
}

// agentBrand is an agent's provider brand: the one its config keeps, or
// the one its base URL names.
func agentBrand(row map[string]any) string {
	if b, _ := row["brandId"].(string); b != "" {
		for _, bh := range brandHosts {
			if bh.brand == b {
				return b
			}
		}
	}
	env, _ := row["env"].(map[string]any)
	base, _ := env["ANTHROPIC_BASE_URL"].(string)
	u, err := url.Parse(base)
	if base == "" || err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	for _, bh := range brandHosts {
		for _, h := range bh.hosts {
			if host == h || strings.HasSuffix(host, "."+h) {
				return bh.brand
			}
		}
	}
	return ""
}

// agentMark is the mark of an agent of cliType and agentType, its config
// row (or nil) giving its brand.
func agentMark(cliType, agentType string, row map[string]any) *ui.SVG {
	if cliType == "custom" {
		return iconSquareTerminal
	}
	if cliType == "builtin" {
		if brand := agentBrand(row); brand != "" {
			if svg := agentSVG("brand-" + brand); svg != nil {
				return svg
			}
			// MiMo's Xiaomi mark isn't bundled: it falls to the robot.
			return iconBot
		}
		switch agentType {
		case "claude":
			return agentSVG("brand-anthropic")
		case "codex":
			return agentSVG("brand-openai")
		case "deepseek":
			return agentSVG("brand-deepseek")
		case "bub", "dimcode":
			return agentSVG("builtin-" + agentType)
		case "grok":
			if svg := agentSVG("grok-build"); svg != nil {
				return svg
			}
		case "pi":
			agentType = "pi-acp"
		}
	}
	if cliType == "registry" && agentType == "claude-p" {
		return agentSVG("brand-anthropic")
	}
	if agentType != "" && !strings.ContainsAny(agentType, "/.") {
		if svg := agentSVG(agentType); svg != nil {
			return svg
		}
	}
	return iconBot
}

// kindMark is a provider kind's mark, its preset's brand first.
func kindMark(k providerKind) *ui.SVG {
	var row map[string]any
	if k.brand != "" {
		row = map[string]any{"brandId": k.brand}
	}
	return agentMark(k.cliType, k.agentType, row)
}

// configMark is an agent config's mark, the robot without one.
func configMark(ag *catalog.AgentConfig) *ui.SVG {
	if ag == nil {
		return iconBot
	}
	return agentMark(ag.CLIType, ag.AgentType, ag.Row)
}

// modelBrands match a model id to its maker's mark, first match winning.
var modelBrands = []struct {
	test *regexp.Regexp
	mark string
}{
	{regexp.MustCompile(`^(claude|anthropic)`), "brand-anthropic"},
	{regexp.MustCompile(`^(gpt|codex|o[1-9]|chatgpt|openai)`), "brand-openai"},
	{regexp.MustCompile(`^deepseek`), "brand-deepseek"},
	{regexp.MustCompile(`^(glm|zhipu)`), "brand-glm"},
	{regexp.MustCompile(`^(minimax|abab)`), "brand-minimax"},
	{regexp.MustCompile(`^gemini`), "gemini"},
	{regexp.MustCompile(`^(kimi|moonshot)`), "kimi"},
	{regexp.MustCompile(`^(qwen|qwq)`), "qwen-code"},
	{regexp.MustCompile(`^grok`), "grok-build"},
	{regexp.MustCompile(`^(mistral|codestral|devstral)`), "mistral-vibe"},
}

// modelMark is a model's maker's mark, by its id's last segment (ids come
// routed, as anthropic/claude-… or openrouter:google/gemini-…), sparkles
// when no maker is known.
func modelMark(modelID string) *ui.SVG {
	id := strings.ToLower(modelID)
	if i := strings.LastIndexAny(id, ":/"); i >= 0 {
		id = id[i+1:]
	}
	for _, b := range modelBrands {
		if b.test.MatchString(id) {
			if svg := agentSVG(b.mark); svg != nil {
				return svg
			}
		}
	}
	return iconSparkles
}
