package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/cloud"
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

	description string // a registry agent's
	binary      bool   // a registry agent the machine downloads first
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

// registryAgent is an agent of the ACP registry, which Lody offers as a
// provider.
type registryAgent struct {
	id, name, description string
	binary                bool
}

// allKinds is the rail's catalog: the built-ins and presets, the ACP
// registry's agents, then a custom command.
var allKinds = func() []providerKind {
	custom := providerKinds[len(providerKinds)-1]
	out := append([]providerKind(nil), providerKinds[:len(providerKinds)-1]...)
	for _, r := range registryAgents {
		out = append(out, providerKind{value: "registry:" + r.id, name: r.name, group: "registry", agentType: r.id, cliType: "registry",
			description: r.description, binary: r.binary})
	}
	return append(out, custom)
}()

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

	customType   string         // a new custom provider's agent type
	codex        *codexAuth     // the account a managed Codex signs in with
	title        map[string]any // title generation's option values
	titled       bool           // their defaults are filled in
	ownsTitle    bool           // the agent names its sessions itself
	authRequired bool           // the last test found no credentials
	auth         *authFlow
	binary       *binaryState
	// Pi's extensions: the paths loaded, a scan's finds, a path typed.
	piExt      []string
	piScan     *piDiscovery
	piScanning bool
	piErr      string
	piPath     string
	testStop   func() // cancels the running test
}

// codexAuth is the account of a managed Codex provider: ChatGPT, or an
// API key for a base URL. Its credentials stay on the machine, under the
// profile.
type codexAuth struct {
	mode      string // chatgpt or api-key
	profileID string
	baseURL   string
}

// codexLaunchGuard stands in for the Codex path of a profile's config, so
// a machine too old for profiles fails rather than use its global login.
const codexLaunchGuard = "\x00lody-codex-profile-v1"

const defaultCodexURL = "https://api.openai.com/v1"

// normalizeCodexURL checks a Codex base URL as Lody does: HTTPS, or HTTP
// on loopback, with no credentials, query or fragment.
func normalizeCodexURL(v string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(v))
	bad := fmt.Errorf("%s", locales.CodexBadURL())
	if err != nil || u.Host == "" {
		return "", bad
	}
	host := u.Hostname()
	loopback := host == "localhost" || host == "::1" || strings.HasPrefix(host, "127.")
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && loopback)) {
		return "", bad
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// codexProtectedEnv are the variables a Codex profile keeps to itself.
var codexProtectedEnv = []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "DBUS_SESSION_BUS_ADDRESS",
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS",
	"NODE_TLS_REJECT_UNAUTHORIZED", "MODEL_PROVIDER", "DEFAULT_AUTH_REQUEST", "NODE_OPTIONS", "LD_PRELOAD", "LD_LIBRARY_PATH"}

func codexProtected(key string) bool {
	k := strings.ToUpper(key)
	if slices.Contains(codexProtectedEnv, k) {
		return true
	}
	for _, prefix := range []string{"CODEX_", "OPENAI_", "LODY_CODEX_", "DYLD_"} {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// agentType is the config's agent type, as its row has it.
func (f *providerForm) agentType() string {
	switch {
	case f.editing != nil:
		return f.editing.AgentType
	case f.kind.group == "custom":
		return f.customType
	}
	return f.kind.agentType
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
	a.refreshQuotas(agents)
	// Setups still under way list with the providers, until each publishes.
	var setups []catalog.ProviderSetup
	for _, st := range a.catalog.ProviderSetups[machine] {
		if !slices.ContainsFunc(agents, func(ag catalog.AgentConfig) bool { return ag.ID == st.ID }) {
			setups = append(setups, st)
		}
	}
	if len(agents) == 0 && len(setups) == 0 {
		emptyCard(c, p, locales.MachinesNoAgents())
	} else {
		card(c, p, func() {
			for _, st := range setups {
				a.providerSetupRow(c, p, st)
			}
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
			if a.catalog.Capabilities[agent.ID].RateLimits {
				quotaMeters(c, p, a.catalog.RateLimits[agent.ID], time.Now())
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
func (a *App) refreshProvider(machine, configID string, done func(result string, authRequired bool, err error)) {
	conn := a.conn
	if conn == nil {
		return
	}
	rid := uuid.NewString()
	ctx, cancel := context.WithTimeout(cloud.WithRequestID(context.Background(), rid), 125*time.Second)
	if done != nil {
		a.provider.testStop = func() {
			cancel()
			go func() {
				c, stop := context.WithTimeout(context.Background(), 15*time.Second)
				defer stop()
				conn.MachineRPC(c, machine, "machine/acp-capabilities-refresh-cancel", map[string]any{"requestId": rid}, 10*time.Second)
			}()
		}
	}
	go func() {
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
		case res.AuthRequired && done != nil:
		case res.AuthRequired:
			err = fmt.Errorf("%s", locales.ProviderAuthRequired())
		case !res.Success:
			err = fmt.Errorf("%s", firstNonEmpty(res.Error, locales.ProviderTestFailed()))
		default:
			result = locales.ProviderTestOK(len(res.Models), len(res.Modes))
		}
		a.update(func() {
			if done != nil {
				done(result, res.AuthRequired, err)
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
	if prev := a.provider.auth; prev != nil && prev.phase == "running" {
		a.cancelProviderAuth(prev)
	}
	f := providerForm{open: true, machine: machine, kind: providerKinds[0], id: uuid.NewString(), sections: map[string]bool{"title": true},
		customType: "custom-" + uuid.NewString(), title: map[string]any{}}
	if ag != nil {
		f.editing, f.id, f.name, f.persisted = ag, ag.ID, ag.Name, true
		f.kind = providerKind{value: ag.CLIType + ":" + ag.AgentType, name: ag.AgentType, agentType: ag.AgentType, cliType: ag.CLIType}
		for _, k := range allKinds {
			if (k.group == "builtin" || k.group == "registry") && k.agentType == ag.AgentType && k.cliType == ag.CLIType {
				f.kind = k
			}
		}
		if ag.CLIType == "custom" {
			f.kind = providerKinds[len(providerKinds)-1]
		}
		row := ag.Row
		if ov, _ := row["runtimeOverrides"].(map[string]any); ov != nil && f.kind.pathKey != "" {
			f.path = str(ov[f.kind.pathKey])
			for _, x := range anyList(ov["piExtensions"]) {
				f.piExt = append(f.piExt, str(x))
			}
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
		if auth, _ := row["codexAuth"].(map[string]any); auth != nil {
			f.codex = &codexAuth{mode: str(auth["mode"]), profileID: str(auth["profileId"]), baseURL: str(auth["baseUrl"])}
			f.path = ""
		}
		if tg, _ := row["titleGeneration"].(map[string]any); tg != nil {
			if values, _ := tg["configOptionValues"].(map[string]any); values != nil {
				for k, v := range values {
					f.title[k] = v
				}
			}
		}
	}
	a.provider = f
}

// selectKind switches a new provider's kind, leaving what the last one
// had set.
func (a *App) selectKind(k providerKind) {
	f := &a.provider
	if f.auth != nil && f.auth.phase == "running" {
		a.cancelProviderAuth(f.auth)
	}
	f.kind, f.endpoint, f.baseURL, f.testErr, f.tested = k, "", "", "", ""
	f.codex, f.title, f.titled, f.authRequired, f.auth, f.binary = nil, map[string]any{}, false, false, nil, nil
}

// machineCap is the version of a protocol capability a machine has.
func (a *App) machineCap(machine, name string) int {
	return int(a.catalog.MachineCaps[machine][name])
}

// managedCodex is whether the form's Codex signs in with its own account,
// as machines with profiles keep them.
func (a *App) managedCodex(f *providerForm) bool {
	return f.kind.cliType == "builtin" && f.kind.agentType == "codex" && a.machineCap(f.machine, "codexAuthProfiles") >= 1
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
		if k.group == "custom" && f.customType == "" {
			f.customType = "custom-" + uuid.NewString()
		}
		row["cliType"], row["agentType"] = k.cliType, f.agentType()
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
	codex := f.codex != nil && k.cliType == "builtin" && k.agentType == "codex"
	if codex {
		for key := range env {
			if codexProtected(key) {
				return nil, fmt.Errorf("%s", locales.CodexProtectedEnv())
			}
		}
		auth := map[string]any{"mode": f.codex.mode, "profileId": f.codex.profileID}
		if f.codex.mode == "api-key" {
			base, err := normalizeCodexURL(f.codex.baseURL)
			if err != nil {
				return nil, err
			}
			auth["baseUrl"] = base
		}
		row["codexAuth"] = auth
		row["runtimeOverrides"] = map[string]any{"codexPath": codexLaunchGuard}
	} else if k.pathKey != "" {
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
		if k.agentType == "pi" {
			delete(next, "piExtensions")
			if len(f.piExt) > 0 {
				exts := make([]any, len(f.piExt))
				for i, x := range f.piExt {
					exts[i] = x
				}
				next["piExtensions"] = exts
			}
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
	switch {
	case f.ownsTitle:
		delete(row, "titleGeneration")
	case k.group != "preset" && len(f.title) > 0:
		tg := map[string]any{}
		if old, _ := row["titleGeneration"].(map[string]any); old != nil {
			for key, v := range old {
				tg[key] = v
			}
		}
		values := map[string]any{}
		for key, v := range f.title {
			values[key] = v
		}
		tg["configOptionValues"] = values
		row["titleGeneration"] = tg
	}
	return row, nil
}

// writeProvider writes an agent config, taking its type off the opted-out
// built-ins.
func (a *App) writeProvider(machine, id string, row map[string]any, done func(error)) {
	agentType := str(row["agentType"])
	optOut := slices.Contains(managedTypes, agentType)
	a.streamWrite(machine, func(fl *flock.Flock) error {
		if optOut {
			if _, ok, _ := fl.Get([]any{"builtinAgentOptOut", agentType}); ok {
				if err := fl.Delete([]any{"builtinAgentOptOut", agentType}); err != nil {
					return err
				}
			}
		}
		return fl.Put([]any{"agentConfig", id}, row)
	}, done)
}

// saveProvider writes the config; test also asks the machine to start it.
func (a *App) saveProvider(test bool) {
	f := &a.provider
	f.ownsTitle = a.titleOwned(f)
	row, err := f.row()
	if err != nil {
		f.err = err.Error()
		return
	}
	f.err, f.testErr, f.tested = "", "", ""
	machine, id := f.machine, f.id
	if a.backgroundSetup(f) {
		// The machine sets it up in the background, its row saying how far.
		a.createProviderSetup(machine, id, row, func(err error) {
			if err != nil {
				f.err = err.Error()
				return
			}
			f.persisted, f.open = true, false
		})
		return
	}
	if test {
		f.testing = true
	}
	a.writeProvider(machine, id, row, func(err error) {
		if err != nil {
			f.testing, f.err = false, err.Error()
			return
		}
		f.persisted = true
		if !test {
			f.open = false
			return
		}
		a.refreshProvider(machine, id, func(result string, authRequired bool, err error) {
			if !f.testing {
				return // cancelled
			}
			f.testing, f.testStop = false, nil
			if err != nil {
				f.testErr = err.Error()
				return
			}
			f.authRequired = authRequired
			f.tested = result
		})
	})
}

func (a *App) providerDialog(c *ui.Context, p *palette) {
	f := &a.provider
	if !f.open {
		if f.auth != nil && f.auth.phase == "running" {
			a.cancelProviderAuth(f.auth)
			f.auth.phase = "cancelled"
		}
		return
	}
	w, h := c.Size()
	ui.DialogBase(c, &f.open, func(backdrop, panel ui.Element) {
		sheetIn(backdrop, panel)
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
			groups := []struct{ id, label string }{{"builtin", locales.ProviderBuiltin()}, {"preset", locales.ProviderPresets()},
				{"registry", locales.ProviderRegistry()}, {"custom", locales.ProviderCustomGroup()}}
			for _, group := range groups {
				shown := false
				for _, k := range allKinds {
					keys := strings.ToLower(k.label() + " " + k.agentType + " " + k.description)
					if k.group != group.id || (q != "" && !strings.Contains(keys, q)) {
						continue
					}
					if !shown {
						ui.Text(c, group.label).FontSize(sizeCaption).TextColor(p.Tertiary).Padding(10, 10, 4, 10)
						shown = true
					}
					kind := k
					row := ui.Row(c.Key(k.value)).FillWidth().Height(34).Padding(0, 10).Radius(8).Gap(10).AlignItems(ui.Center).Cursor(ui.CursorPointer).
						Label(k.label()).Transition(colorFade).OnClick(func() { a.selectKind(kind) })
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
	foldSection(c, p, f.sections[id], func() { f.sections[id] = !f.sections[id] }, id, title, body)
}

// foldSection is a part of a form that folds away under its title.
func foldSection(c *ui.Context, p *palette, open bool, toggle func(), id, title string, body func()) {
	ui.Column(c.Key(id)).FillWidth().Radius(8).Background(p.Text.Alpha(0.035)).Clip().Children(func() {
		head := ui.Row(c).FillWidth().Height(40).Padding(0, 12).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(title).OnClick(toggle)
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
			subtitle := locales.ProviderRuntime(k.label())
			if k.description != "" {
				subtitle = k.description
			}
			ui.Text(c, subtitle).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
		})
		if f.testing && f.testStop != nil {
			ghostButton(c, p, locales.Cancel()).OnClick(func() {
				f.testStop()
				f.testStop, f.testing = nil, false
			})
		}
		test := outlineButton(c, p, iconFlask, locales.ProviderTest())
		if f.testing || !(a.binaryReady(f) || a.backgroundSetup(f)) {
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
		managed := a.managedCodex(f)
		if managed && f.codex == nil && f.editing == nil {
			f.codex = &codexAuth{mode: "chatgpt", profileID: uuid.NewString()}
		}
		if k.pathKey != "" && f.codex == nil {
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
		if managed && f.codex != nil {
			a.codexAuthField(c, p)
		}
		if k.binary {
			a.binaryField(c, p)
		}
		a.accountField(c, p, managed)
		if k.cliType == "builtin" && k.agentType == "pi" {
			a.piExtensionsField(c, p)
		}
		a.titleSection(c, p)
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
		if f.testing || f.tested != "" || f.testErr != "" {
			// The test's mark crossfades between its states, each growing in
			// from .85 over 180ms.
			swap := ui.ElementTransition{Duration: durRegular, Ease: easeStandard, Enter: &ui.Motion{Scale: 0.85}, Exit: &ui.Motion{Scale: 0.85}}
			ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
				ui.Box(c).Size(14, 14).Shrink(0).Margin(1, 0, 0, 0).Children(func() {
					switch {
					case f.testing:
						spinner(c.Key("testing")).Size(14, 14).Absolute().Transition(swap)
					case f.tested != "":
						ui.Icon(c.Key("passed"), markSuccess).Size(14, 14).TextColor(p.Success).Absolute().Transition(swap)
					default:
						ui.Icon(c.Key("failed"), markDanger).Size(14, 14).TextColor(p.Danger).Absolute().Transition(swap)
					}
				})
				switch {
				case f.testing:
					ui.Text(c, locales.ProviderTesting()).FontSize(sizeFoot).TextColor(p.Secondary)
				case f.tested != "":
					ui.Text(c, f.tested).FontSize(sizeFoot).TextColor(p.Success)
				default:
					ui.Text(c, f.testErr).FontSize(sizeFoot).TextColor(p.Danger).Selectable().Grow(1).MinWidth(0)
				}
			})
		}
		if f.err != "" {
			ui.Text(c, f.err).FontSize(sizeFoot).TextColor(p.Danger)
		}
	})
	ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).AlignItems(ui.Center).Shrink(0).Children(func() {
		if k.group == "registry" {
			ui.Row(c).Grow(1).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconFlask).Size(12, 12).TextColor(p.Tertiary)
				ui.Text(c, locales.ProviderRegistryNote()).FontSize(sizeCaption).TextColor(p.Tertiary)
			})
		}
		ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { f.open = false })
		label := locales.ProviderCreate()
		if f.persisted {
			label = locales.Save()
		}
		ready := f.machine != "" && (k.group != "custom" || strings.TrimSpace(f.command) != "") && (a.binaryReady(f) || a.backgroundSetup(f))
		primaryButton(c, p, label, ready && !f.testing).OnClick(func() { a.saveProvider(false) })
	})
}
