package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/locales"
)

// The Agents page, as Lody's: each machine's providers, the agent configs
// rows ['agentConfig', id] of its stream hold, and the dialog that adds
// or edits one from Lody's catalog of runtimes and presets.

// providerKind is an entry of the dialog's rail.
type providerKind struct {
	value     string // builtin:<type>, preset:<id> or custom
	name      string
	group     string // builtin, preset or custom
	agentType string
	cliType   string
	pathKey   string // runtimeOverrides key for the runtime's path
	brand     string
	tokenEnv  string            // the preset's token variable
	fixedEnv  map[string]string // the preset's settings
	endpoints []endpoint        // the base URLs a preset may use
}

type endpoint struct{ id, label, url string }

const anthropicURL = "ANTHROPIC_BASE_URL"

func claudeModels(model string, extra map[string]string) map[string]string {
	env := map[string]string{
		"ANTHROPIC_MODEL": model, "ANTHROPIC_DEFAULT_OPUS_MODEL": model, "ANTHROPIC_DEFAULT_SONNET_MODEL": model,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL": model, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

// providerKinds is Lody's provider catalog, in the rail's order.
var providerKinds = []providerKind{
	{value: "builtin:kimi", name: "Kimi Code", group: "builtin", agentType: "kimi", cliType: "builtin", pathKey: "kimiPath"},
	{value: "builtin:devin", name: "Devin", group: "builtin", agentType: "devin", cliType: "builtin", pathKey: "devinPath"},
	{value: "builtin:grok", name: "Grok", group: "builtin", agentType: "grok", cliType: "builtin", pathKey: "grokPath"},
	{value: "builtin:claude", name: "Claude", group: "builtin", agentType: "claude", cliType: "builtin", pathKey: "claudeCodeExecutable"},
	{value: "builtin:codex", name: "Codex", group: "builtin", agentType: "codex", cliType: "builtin", pathKey: "codexPath"},
	{value: "builtin:deepseek", name: "DeepSeek Harness", group: "builtin", agentType: "deepseek", cliType: "builtin", tokenEnv: "DEEPSEEK_API_KEY",
		endpoints: []endpoint{{"official", "DeepSeek", "https://api.deepseek.com/anthropic"}}},
	{value: "builtin:pi", name: "Pi", group: "builtin", agentType: "pi", cliType: "builtin", pathKey: "piPath"},
	{value: "builtin:dimcode", name: "Dimcode", group: "builtin", agentType: "dimcode", cliType: "builtin"},
	{value: "builtin:bub", name: "Bub", group: "builtin", agentType: "bub", cliType: "builtin"},
	{value: "preset:deepseek-over-claude-code", name: "DeepSeek over Claude Code", group: "preset", agentType: "claude", cliType: "builtin", brand: "deepseek",
		tokenEnv: "ANTHROPIC_AUTH_TOKEN", fixedEnv: map[string]string{
			anthropicURL: "https://api.deepseek.com/anthropic", "ANTHROPIC_MODEL": "deepseek-v4-pro[1m]",
			"ANTHROPIC_DEFAULT_OPUS_MODEL": "deepseek-v4-pro", "ANTHROPIC_DEFAULT_SONNET_MODEL": "deepseek-v4-pro",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL": "deepseek-v4-flash", "CLAUDE_CODE_SUBAGENT_MODEL": "deepseek-v4-pro",
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1", "CLAUDE_CODE_EFFORT_LEVEL": "max",
		}},
	{value: "preset:deepseek-reasonix", name: "DeepSeek Reasonix", group: "preset", agentType: "reasonix", cliType: "registry", brand: "deepseek", tokenEnv: "DEEPSEEK_API_KEY"},
	{value: "preset:mimo-over-claude-code", name: "MiMo over Claude Code", group: "preset", agentType: "claude", cliType: "builtin", brand: "mimo",
		tokenEnv: "ANTHROPIC_AUTH_TOKEN", fixedEnv: claudeModels("mimo-v2.5-pro", map[string]string{"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1"}),
		endpoints: []endpoint{
			{"token-plan-cn", "Token Plan · China", "https://token-plan-cn.xiaomimimo.com/anthropic"},
			{"token-plan-sgp", "Token Plan · Singapore", "https://token-plan-sgp.xiaomimimo.com/anthropic"},
			{"token-plan-ams", "Token Plan · Amsterdam", "https://token-plan-ams.xiaomimimo.com/anthropic"},
			{"pay-as-you-go", "Pay as you go", "https://api.xiaomimimo.com/anthropic"},
		}},
	{value: "preset:minimax-over-claude-code", name: "MiniMax over Claude Code", group: "preset", agentType: "claude", cliType: "builtin", brand: "minimax",
		tokenEnv: "ANTHROPIC_AUTH_TOKEN", fixedEnv: claudeModels("MiniMax-M3", map[string]string{anthropicURL: "https://api.minimaxi.com/anthropic", "API_TIMEOUT_MS": "3000000"})},
	{value: "preset:glm-over-claude-code", name: "GLM over Claude Code", group: "preset", agentType: "claude", cliType: "builtin", brand: "glm",
		tokenEnv: "ANTHROPIC_AUTH_TOKEN", fixedEnv: map[string]string{
			"ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-4.7", "ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.2[1m]", "ANTHROPIC_DEFAULT_OPUS_MODEL": "glm-5.2[1m]",
			"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "1000000", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "API_TIMEOUT_MS": "3000000",
		},
		endpoints: []endpoint{{"bigmodel", "open.bigmodel.cn", "https://open.bigmodel.cn/api/anthropic"}, {"zai", "z.ai", "https://api.z.ai/api/anthropic"}}},
	{value: "custom", name: "", group: "custom", cliType: "custom"},
}

func (k providerKind) label() string {
	if k.group == "custom" {
		return locales.ProviderCustom()
	}
	return k.name
}

// managedTypes are the builtins Lody's CLI adds back unless opted out.
var managedTypes = []string{"kimi", "devin", "grok", "claude", "codex", "pi"}

type providerForm struct {
	open      bool
	machine   string
	editing   *catalog.AgentConfig
	kind      providerKind
	search    string
	id        string
	name      string
	path      string
	command   string // custom: the launch command
	token     string
	endpoint  string
	baseURL   string
	prompt    string
	env       string
	sections  map[string]bool
	testing   bool
	tested    string
	testErr   string
	err       string
	persisted bool // the config is saved, as a test saves it
}

func (a *App) agentsPage(c *ui.Context, p *palette) {
	pageHeader(c, p, locales.SettingsAgents(), "", func() {
		outlineButton(c, p, iconPlus, locales.ProviderAdd()).OnClick(func() { a.openProvider(a.agentsMachine, nil) })
	})
	a.settingsErrorLine(c, p)
	machine := a.agentsMachine
	if len(a.catalog.MachineIDs) > 1 {
		machine = a.machineTabs(c, p, &a.agentsMachine)
	} else if len(a.catalog.MachineIDs) == 1 {
		machine, a.agentsMachine = a.catalog.MachineIDs[0], a.catalog.MachineIDs[0]
	}
	agents := append([]catalog.AgentConfig(nil), a.catalog.Agents[machine]...)
	slices.SortStableFunc(agents, func(x, y catalog.AgentConfig) int {
		return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
	})
	if len(agents) == 0 {
		emptyCard(c, p, locales.MachinesNoAgents())
	} else {
		card(c, p, func() {
			for _, ag := range agents {
				a.providerRow(c.Key(ag.ID), p, ag)
			}
		})
	}
	a.providerDialog(c, p)
}

// providerFacts is a provider's subtitle: its kind, default model, chats,
// when it was last used, and its variables.
func (a *App) providerFacts(ag catalog.AgentConfig) []string {
	var facts []string
	switch ag.CLIType {
	case "custom":
		facts = append(facts, locales.ProviderCustomShort())
	case "registry":
		facts = append(facts, "ACP Provider")
	default:
		if m := a.catalog.Capabilities[ag.ID].DefaultModel; m != "" {
			facts = append(facts, m)
		}
	}
	chats, last := 0, 0.0
	for _, s := range a.catalog.Sessions {
		if s.MachineID == ag.MachineID && s.AgentConfigID == ag.ID && !s.Archived {
			chats++
			last = max(last, s.LastMessageAt)
		}
	}
	if chats > 0 {
		facts = append(facts, locales.ProjectChats(chats))
	}
	if last > 0 {
		facts = append(facts, locales.ProviderUsed(agoText(time.UnixMilli(int64(last)))))
	}
	if env, _ := ag.Row["env"].(map[string]any); len(env) > 0 {
		facts = append(facts, locales.ProviderEnvCount(len(env)))
	}
	return facts
}

// agoText says how long ago t was, in words.
func agoText(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return locales.AgoMinutes(max(1, int(d.Minutes())))
	case d < 24*time.Hour:
		return locales.AgoHours(int(d.Hours()))
	}
	return locales.AgoDays(int(d.Hours() / 24))
}

func (a *App) providerRow(c *ui.Context, p *palette, ag catalog.AgentConfig) {
	agent := ag
	ui.Row(c).FillWidth().MinHeight(48).Padding(8, 8, 8, 16).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, agentIcon(agent.CLIType)).Size(18, 18).TextColor(p.Text).Shrink(0)
		body := ui.Column(c).Grow(1).MinWidth(0).Gap(2).Cursor(ui.CursorPointer).Label(agent.Name).OnClick(func() { a.openProvider(agent.MachineID, &agent) })
		body.Children(func() {
			ui.Text(c, agent.Name).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine()
			if facts := a.providerFacts(agent); len(facts) > 0 {
				ui.Text(c, strings.Join(facts, " · ")).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
			}
		})
		iconButton(c, p, iconMore, locales.MoreActions(), p.Hover).Menu(func(m *ui.Menu) {
			if m.Item(locales.ProviderRefresh()).Chosen() {
				a.refreshProvider(agent.MachineID, agent.ID, nil)
			}
			m.Separator()
			if m.Item(locales.ActionDelete()).Chosen() {
				a.confirm(locales.ProviderDeleteTitle(agent.Name), locales.ProviderDeleteBody(), locales.ActionDelete(), func() { a.deleteProvider(agent) })
			}
		})
	})
}

// refreshProvider asks the machine to start the agent and report its
// models and modes, which it writes to its stream.
func (a *App) refreshProvider(machine, configID string, done func(result string, err error)) {
	conn := a.conn
	if conn == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 125*time.Second)
		defer cancel()
		raw, err := conn.MachineRPC(ctx, machine, "machine/acp-capabilities-refresh", map[string]any{"configId": configID, "force": true}, 120*time.Second)
		var res struct {
			Success      bool              `json:"success"`
			Models       []json.RawMessage `json:"models"`
			Modes        []json.RawMessage `json:"modes"`
			AuthRequired bool              `json:"authRequired"`
			Error        string            `json:"error"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &res)
		}
		result := ""
		switch {
		case err != nil:
		case res.AuthRequired:
			err = fmt.Errorf("%s", locales.ProviderAuthRequired())
		case !res.Success:
			err = fmt.Errorf("%s", firstNonEmpty(res.Error, locales.ProviderTestFailed()))
		default:
			result = locales.ProviderTestOK(len(res.Models), len(res.Modes))
		}
		a.update(func() {
			if done != nil {
				done(result, err)
			} else if err != nil {
				a.settingsError = err.Error()
			}
		})
	}()
}

// deleteProvider removes an agent config; the last of a managed builtin
// type is opted out too, or the CLI would add it back.
func (a *App) deleteProvider(ag catalog.AgentConfig) {
	last := slices.Contains(managedTypes, ag.AgentType)
	for _, other := range a.catalog.Agents[ag.MachineID] {
		if other.ID != ag.ID && other.AgentType == ag.AgentType {
			last = false
		}
	}
	id, agentType := ag.ID, ag.AgentType
	a.streamWrite(ag.MachineID, func(f *flock.Flock) error {
		if last {
			if err := f.Put([]any{"builtinAgentOptOut", agentType}, map[string]any{"v": 1, "removedAt": nowMs()}); err != nil {
				return err
			}
		}
		return f.Delete([]any{"agentConfig", id})
	}, nil)
}

// openProvider opens the dialog on a new provider or an existing one.
func (a *App) openProvider(machine string, ag *catalog.AgentConfig) {
	if machine == "" && len(a.catalog.MachineIDs) > 0 {
		machine = a.catalog.MachineIDs[0]
	}
	f := providerForm{open: true, machine: machine, kind: providerKinds[0], id: uuid.NewString(), sections: map[string]bool{}}
	if ag != nil {
		f.editing, f.id, f.name, f.persisted = ag, ag.ID, ag.Name, true
		f.kind = providerKind{value: ag.CLIType + ":" + ag.AgentType, name: ag.AgentType, agentType: ag.AgentType, cliType: ag.CLIType}
		for _, k := range providerKinds {
			if k.group == "builtin" && k.agentType == ag.AgentType && ag.CLIType == "builtin" {
				f.kind = k
			}
		}
		if ag.CLIType == "custom" {
			f.kind = providerKinds[len(providerKinds)-1]
		}
		row := ag.Row
		if ov, _ := row["runtimeOverrides"].(map[string]any); ov != nil && f.kind.pathKey != "" {
			f.path = str(ov[f.kind.pathKey])
		}
		if acp, _ := row["customAcp"].(map[string]any); acp != nil {
			parts := []string{shellQuote(str(acp["command"]))}
			for _, arg := range anyList(acp["args"]) {
				parts = append(parts, shellQuote(str(arg)))
			}
			f.command = strings.Join(parts, " ")
		}
		f.prompt = str(row["prompt"])
		f.env = pairs(row["env"], "=")
	}
	a.provider = f
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// splitCommand reads a launch command as a simple shell does: quotes and
// backslashes, no variables or pipes.
func splitCommand(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inWord := false
	quote := rune(0)
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\\' && quote != '\'':
			if i+1 < len(runes) {
				i++
				cur.WriteRune(runes[i])
				inWord = true
			}
		case quote == '"':
			if r == '"' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("%s", locales.ProviderUnclosedQuote())
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out, nil
}

// row is the agent config the form describes, built on the one it edits.
func (f *providerForm) row() (map[string]any, error) {
	row := map[string]any{}
	if f.editing != nil {
		for k, v := range f.editing.Row {
			row[k] = v
		}
	}
	k := f.kind
	name := strings.TrimSpace(f.name)
	if name == "" {
		name = k.label()
	}
	row["id"], row["machineId"], row["name"] = f.id, f.machine, name
	if f.editing == nil {
		row["cliType"], row["agentType"] = k.cliType, k.agentType
		if k.group == "custom" {
			row["agentType"] = "custom-" + uuid.NewString()
		}
		if k.brand != "" {
			row["brandId"] = k.brand
		}
	}
	envMap, err := parsePairs(f.env, "=")
	if err != nil {
		return nil, err
	}
	env := map[string]any{}
	for key, v := range envMap {
		env[key] = v
	}
	for key, v := range k.fixedEnv {
		env[key] = v
	}
	if len(k.endpoints) > 0 {
		url := strings.TrimSpace(f.baseURL)
		for _, e := range k.endpoints {
			if e.id == f.endpoint || (f.endpoint == "" && url == "" && e == k.endpoints[0]) {
				url = e.url
			}
		}
		urlKey := anthropicURL
		if k.agentType == "deepseek" {
			urlKey = "DEEPSEEK_BASE_URL"
		}
		if url != "" {
			env[urlKey] = url
		}
	}
	if k.tokenEnv != "" && strings.TrimSpace(f.token) != "" {
		env[k.tokenEnv] = strings.TrimSpace(f.token)
	}
	row["env"] = env
	if k.pathKey != "" {
		ov, _ := row["runtimeOverrides"].(map[string]any)
		next := map[string]any{}
		for key, v := range ov {
			next[key] = v
		}
		if path := strings.TrimSpace(f.path); path != "" {
			next[k.pathKey] = path
		} else {
			delete(next, k.pathKey)
		}
		if len(next) > 0 {
			row["runtimeOverrides"] = next
		} else {
			delete(row, "runtimeOverrides")
		}
	}
	if k.group == "custom" {
		parts, err := splitCommand(f.command)
		if err != nil {
			return nil, err
		}
		if len(parts) == 0 {
			return nil, fmt.Errorf("%s", locales.ProviderNeedsCommand())
		}
		acp := map[string]any{"command": parts[0]}
		if len(parts) > 1 {
			args := make([]any, len(parts)-1)
			for i, a := range parts[1:] {
				args[i] = a
			}
			acp["args"] = args
		}
		row["customAcp"] = acp
	}
	if prompt := strings.TrimSpace(f.prompt); prompt != "" {
		row["prompt"] = prompt
	} else {
		delete(row, "prompt")
	}
	return row, nil
}

// saveProvider writes the config; test also asks the machine to start it.
func (a *App) saveProvider(test bool) {
	f := &a.provider
	row, err := f.row()
	if err != nil {
		f.err = err.Error()
		return
	}
	f.err, f.testErr, f.tested = "", "", ""
	machine, id, agentType := f.machine, f.id, str(row["agentType"])
	optOut := slices.Contains(managedTypes, agentType)
	if test {
		f.testing = true
	}
	a.streamWrite(machine, func(fl *flock.Flock) error {
		if optOut {
			if _, ok, _ := fl.Get([]any{"builtinAgentOptOut", agentType}); ok {
				if err := fl.Delete([]any{"builtinAgentOptOut", agentType}); err != nil {
					return err
				}
			}
		}
		return fl.Put([]any{"agentConfig", id}, row)
	}, func(err error) {
		if err != nil {
			f.testing, f.err = false, err.Error()
			return
		}
		f.persisted = true
		if !test {
			f.open = false
			return
		}
		a.refreshProvider(machine, id, func(result string, err error) {
			f.testing = false
			if err != nil {
				f.testErr = err.Error()
				return
			}
			f.tested = result
		})
	})
}

func (a *App) providerDialog(c *ui.Context, p *palette) {
	f := &a.provider
	if !f.open {
		return
	}
	w, h := c.Size()
	ui.DialogBase(c, &f.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.22))
		panel.Size(min(1040, w-80), min(680, h-80)).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Clip().
			Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
			if f.editing == nil {
				a.providerRail(c, p)
			}
			ui.Column(c).Grow(1).MinWidth(0).Children(func() { a.providerFormView(c, p) })
		})
	})
}

func (a *App) providerRail(c *ui.Context, p *palette) {
	f := &a.provider
	ui.Column(c).Width(290).Shrink(0).Background(p.Ground).BorderWidth(0, 1, 0, 0).BorderColor(p.Border).Padding(12, 8).Gap(2).Children(func() {
		ui.TextInput(c, &f.search).Placeholder(locales.Search()).Label(locales.Search()).FontSize(sizeSub).FillWidth().Height(34).
			Padding(0, 10).Radius(8).Background(p.Canvas).Border(1, p.Border).Margin(0, 0, 8, 0)
		ui.Scroll(c).Grow(1).MinHeight(0).Gap(2).Children(func() {
			q := strings.ToLower(strings.TrimSpace(f.search))
			for _, group := range []struct{ id, label string }{{"builtin", locales.ProviderBuiltin()}, {"preset", locales.ProviderPresets()}, {"custom", locales.ProviderCustomGroup()}} {
				shown := false
				for _, k := range providerKinds {
					if k.group != group.id || (q != "" && !strings.Contains(strings.ToLower(k.label()), q)) {
						continue
					}
					if !shown {
						ui.Text(c, group.label).FontSize(sizeCaption).TextColor(p.Tertiary).Padding(10, 10, 4, 10)
						shown = true
					}
					kind := k
					row := ui.Row(c.Key(k.value)).FillWidth().Height(34).Padding(0, 10).Radius(8).Gap(10).AlignItems(ui.Center).Cursor(ui.CursorPointer).
						Label(k.label()).OnClick(func() {
						f.kind, f.endpoint, f.baseURL, f.testErr, f.tested = kind, "", "", "", ""
					})
					selected := f.kind.value == k.value
					switch {
					case selected:
						row.Background(p.SideSelect)
					case row.Hovered():
						row.Background(p.SideHover)
					}
					row.Children(func() {
						ui.Icon(c, agentIcon(kind.cliType)).Size(15, 15).TextColor(p.Text)
						ui.Text(c, kind.label()).FontSize(sizeSub).TextColor(p.Text).Grow(1).SingleLine()
						if selected {
							ui.Icon(c, iconCheck).Size(14, 14).TextColor(p.Secondary)
						}
					})
				}
			}
		})
	})
}

// providerSection is a collapsible part of the form.
func (a *App) providerSection(c *ui.Context, p *palette, id, title string, body func()) {
	f := &a.provider
	open := f.sections[id]
	ui.Column(c.Key(id)).FillWidth().Radius(8).Background(p.Text.Alpha(0.035)).Clip().Children(func() {
		head := ui.Row(c).FillWidth().Height(40).Padding(0, 12).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(title).
			OnClick(func() { f.sections[id] = !f.sections[id] })
		head.Children(func() {
			ui.Text(c, title).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).Grow(1)
			icon := iconChevronDown
			if open {
				icon = iconChevronUp
			}
			ui.Icon(c, icon).Size(14, 14).TextColor(p.Tertiary)
		})
		if open {
			ui.Column(c).FillWidth().Padding(0, 12, 12, 12).Gap(8).Children(body)
		}
	})
}

func (a *App) providerFormView(c *ui.Context, p *palette) {
	f := &a.provider
	k := f.kind
	title := locales.ProviderNew()
	if f.editing != nil {
		title = locales.ProviderEdit()
	}
	ui.Row(c).FillWidth().Padding(16, 14, 8, 20).Gap(12).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Box(c).Size(36, 36).Radius(8).Center().Background(p.Text.Alpha(0.05)).Children(func() {
			ui.Icon(c, agentIcon(k.cliType)).Size(18, 18).TextColor(p.Text)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(sizeBody).FontWeight(600).TextColor(p.Strong)
			ui.Text(c, locales.ProviderRuntime(k.label())).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
		})
		test := outlineButton(c, p, iconFlask, locales.ProviderTest())
		if f.testing {
			test.Disabled(true).Opacity(0.6)
		} else {
			test.OnClick(func() { a.saveProvider(true) })
		}
		iconButton(c, p, iconX, locales.Close(), p.Hover).OnClick(func() { f.open = false })
	})
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(8, 20, 8, 20).Gap(16).Children(func() {
		if len(a.catalog.MachineIDs) > 1 && f.editing == nil {
			field(c, p, locales.ProviderMachine(), "", func() {
				var options []option
				for _, id := range a.catalog.MachineIDs {
					options = append(options, option{id, a.machineName(id)})
				}
				a.toolbarPicker(c, p, "provider-machine", iconMonitor, a.machineName(f.machine), options, f.machine, func(id string) { f.machine = id })
			})
		}
		field(c, p, locales.ProviderName(), locales.ProviderNameHint(), func() { input(c, p, &f.name, k.label(), locales.ProviderName()) })
		if k.group == "custom" {
			field(c, p, locales.ProviderCommand(), locales.ProviderCommandHint(), func() {
				input(c, p, &f.command, "/path/to/agent --acp", locales.ProviderCommand()).Font(fontMono)
			})
		}
		if k.pathKey != "" {
			field(c, p, locales.ProviderPath(), locales.ProviderPathHint(), func() {
				input(c, p, &f.path, "/path/to/"+k.agentType, locales.ProviderPath()).Font(fontMono)
			})
		}
		if k.tokenEnv != "" {
			field(c, p, "API Key", k.tokenEnv, func() { input(c, p, &f.token, "sk-…", "API Key").Password() })
		}
		if len(k.endpoints) > 1 {
			field(c, p, locales.ProviderEndpoint(), "", func() {
				var options []option
				current := k.endpoints[0]
				for _, e := range k.endpoints {
					options = append(options, option{e.id, e.label})
					if e.id == f.endpoint {
						current = e
					}
				}
				a.toolbarPicker(c, p, "provider-endpoint", iconGlobe, current.label, options, current.id, func(id string) { f.endpoint = id })
			})
		}
		if k.group != "preset" {
			a.providerSection(c, p, "title", locales.ProviderTitleGen(), func() {
				ui.Text(c, locales.ProviderTitleGenHint()).FontSize(sizeFoot).TextColor(p.Secondary)
			})
		}
		a.providerSection(c, p, "prompt", locales.ProviderPrompt(), func() {
			area(c, p, &f.prompt, locales.ProviderPromptPlaceholder(), locales.ProviderPrompt(), 4)
		})
		envTitle := locales.McpEnv()
		if k.group == "preset" {
			envTitle = locales.ProviderExtraEnv()
		}
		a.providerSection(c, p, "env", envTitle, func() {
			area(c, p, &f.env, "KEY=value", envTitle, 3).Font(fontMono)
			ui.Text(c, locales.McpEnvHint()).FontSize(sizeCaption).TextColor(p.Tertiary)
		})
		switch {
		case f.testing:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(13, 13)
				ui.Text(c, locales.ProviderTesting()).FontSize(sizeFoot).TextColor(p.Secondary)
			})
		case f.tested != "":
			ui.Text(c, f.tested).FontSize(sizeFoot).TextColor(p.Success)
		case f.testErr != "":
			ui.Text(c, f.testErr).FontSize(sizeFoot).TextColor(p.Danger).Selectable()
		}
		if f.err != "" {
			ui.Text(c, f.err).FontSize(sizeFoot).TextColor(p.Danger)
		}
	})
	ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).Shrink(0).Children(func() {
		ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { f.open = false })
		label := locales.ProviderCreate()
		if f.persisted {
			label = locales.Save()
		}
		ready := f.machine != "" && (k.group != "custom" || strings.TrimSpace(f.command) != "")
		primaryButton(c, p, label, ready && !f.testing).OnClick(func() { a.saveProvider(false) })
	})
}
