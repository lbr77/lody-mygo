package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// The provider dialog's account, title generation and download, as Lody's
// agent config dialog has them.

// codexAuthField picks a managed Codex's account: ChatGPT, or an API key
// for a base URL. A saved provider keeps its kind; another account is
// another provider.
func (a *App) codexAuthField(c *ui.Context, p *palette) {
	f := &a.provider
	auth := f.codex
	field(c, p, locales.CodexAuthentication(), locales.CodexStorage(), func() {
		picked := 0
		if auth.mode == "api-key" {
			picked = 1
		}
		was := picked
		ui.Segmented(c, &picked, "ChatGPT", locales.CodexCustomAPI()).Label(locales.CodexAuthentication())
		if picked != was && f.editing == nil {
			auth.mode = []string{"chatgpt", "api-key"}[picked]
			if auth.mode == "api-key" && auth.baseURL == "" {
				auth.baseURL = defaultCodexURL
			}
		}
		if auth.mode == "api-key" {
			field(c, p, locales.CodexBaseURL(), locales.CodexKeyNext(), func() {
				if f.editing != nil {
					ui.Text(c, auth.baseURL).Font(fontMono).FontSize(sizeFoot).TextColor(p.Secondary).Selectable()
					return
				}
				input(c, p, &auth.baseURL, defaultCodexURL, locales.CodexBaseURL()).Font(fontMono)
			})
		}
	})
}

// claudeEnvAuth are the variables that sign Claude in by themselves.
var claudeEnvAuth = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_CUSTOM_HEADERS", "ANTHROPIC_BASE_URL",
	"ANTHROPIC_BEDROCK_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL", "ANTHROPIC_FOUNDRY_BASE_URL", "ANTHROPIC_FOUNDRY_RESOURCE",
	"ANTHROPIC_FOUNDRY_API_KEY", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"}

// builtinSignIn is whether Lody can run a builtin's own sign-in for the
// form: a managed runtime with a login, not one a preset or the
// variables already sign in.
func (f *providerForm) builtinSignIn() bool {
	k := f.kind
	if k.cliType != "builtin" || k.brand != "" || !slices.Contains(managedTypes, k.agentType) || k.agentType == "pi" || k.agentType == "devin" {
		return false
	}
	if f.editing != nil && str(f.editing.Row["brandId"]) != "" {
		return false
	}
	if k.agentType == "claude" {
		env, _ := parsePairs(f.env, "=")
		for _, key := range claudeEnvAuth {
			if v, ok := env[key].(string); ok && strings.TrimSpace(v) != "" {
				return false
			}
		}
	}
	return true
}

// accountField is the provider's sign-in, when it has one to offer: a
// saved builtin with a login, or any provider a test found signed out.
func (a *App) accountField(c *ui.Context, p *palette, managed bool) {
	f := &a.provider
	k := f.kind
	if f.editing != nil && managed && f.codex != nil && f.codex.mode == "chatgpt" {
		field(c, p, locales.AuthAccount(), "", func() {
			ui.Text(c, locales.CodexBound()).FontSize(sizeFoot).TextColor(p.Secondary)
		})
		return
	}
	protocol := (k.cliType == "registry" || k.cliType == "custom" || (k.cliType == "builtin" && k.agentType == "devin")) &&
		a.machineCap(f.machine, "acpProtocolAuthentication") >= 2
	running := f.auth != nil && f.auth.phase != ""
	show := f.authRequired && (protocol || (k.cliType == "builtin" && slices.Contains(managedTypes, k.agentType) && k.agentType != "pi" && k.agentType != "devin"))
	if f.editing != nil {
		show = f.builtinSignIn() || (f.authRequired && protocol)
	}
	if !show && !running {
		return
	}
	hint := locales.AuthReauthHint()
	if f.authRequired {
		hint = locales.AuthRequiredHint()
	}
	field(c, p, locales.AuthAccount(), hint, func() {
		a.signInPanel(c, p, a.machineCap(f.machine, "acpAuthenticationInteractions") >= 2)
	})
}

// formCapability is what the machine last found the form's agent
// supports, if it was for this runtime.
func (a *App) formCapability(f *providerForm) (catalog.Capability, bool) {
	cap, ok := a.catalog.Capabilities[f.id]
	if !ok {
		return cap, false
	}
	if cap.CLIType != "" && (cap.CLIType != f.kind.cliType || cap.AgentType != f.agentType()) {
		return cap, false
	}
	return cap, true
}

// titleOwned is whether the agent names its sessions itself, as Claude
// and Grok do, so Lody runs no title agent for it.
func (a *App) titleOwned(f *providerForm) bool {
	k := f.kind
	if k.cliType == "builtin" && k.agentType == "codex" {
		return false
	}
	if cap, ok := a.formCapability(f); ok && cap.SessionTitle {
		return true
	}
	return k.cliType == "builtin" && (k.agentType == "claude" || k.agentType == "grok") && strings.TrimSpace(f.path) == ""
}

// titleOptions are the settings title generation may pick: the agent's
// select and boolean options, its reasoning efforts those of the model
// picked.
func titleOptions(cap catalog.Capability, values map[string]any) []catalog.ConfigOption {
	model := ""
	for _, o := range cap.Options {
		if o.ID == "model" || o.Category == "model" {
			model = str(o.Current)
			if v, ok := values[o.ID].(string); ok && v != "" {
				model = v
			}
		}
	}
	var out []catalog.ConfigOption
	for _, o := range cap.Options {
		if efforts, ok := cap.Efforts[model]; ok && (o.ID == "reasoning_effort" || o.Category == "thought_level") {
			var kept []catalog.Choice
			for _, ch := range o.Choices {
				if slices.Contains(efforts, ch.Value) {
					kept = append(kept, ch)
				}
			}
			o.Choices = kept
		}
		if o.Type == "select" && len(o.Choices) == 0 {
			continue
		}
		out = append(out, o)
	}
	return out
}

func lowestRank(choices []catalog.Choice, rank func(string) int) string {
	best, bestRank := "", 1<<30
	for _, ch := range choices {
		if r := rank(strings.NewReplacer(" ", "-", "_", "-").Replace(strings.ToLower(ch.Value + " " + ch.Name))); r < bestRank {
			best, bestRank = ch.Value, r
		}
	}
	return best
}

// titleDefaults are Lody's picks for titling: Codex's light model, else
// the least permission and the least reasoning the agent offers.
func titleDefaults(cliType, agentType string, options []catalog.ConfigOption) map[string]any {
	if cliType == "builtin" && agentType == "codex" {
		return map[string]any{"model": "gpt-5.6-luna", "reasoning_effort": "low", "mode": "agent-full-access"}
	}
	out := map[string]any{}
	for _, o := range options {
		if o.Type != "select" {
			continue
		}
		var v string
		switch {
		case o.Category == "mode":
			v = lowestRank(o.Choices, func(s string) int {
				switch {
				case strings.Contains(s, "read-only") || strings.Contains(s, "readonly"):
					return 0
				case strings.Contains(s, "ask"):
					return 1
				case strings.Contains(s, "plan"):
					return 2
				case strings.Contains(s, "auto") || strings.Contains(s, "default"):
					return 3
				}
				return 50
			})
		case o.ID == "reasoning_effort" || o.Category == "thought_level":
			v = lowestRank(o.Choices, func(s string) int {
				for i, level := range []string{"none", "minimal", "low", "medium"} {
					if strings.Contains(s, level) {
						return i
					}
				}
				if strings.Contains(s, "xhigh") || strings.Contains(s, "extra-high") {
					return 5
				}
				if strings.Contains(s, "high") {
					return 4
				}
				return 50
			})
		default:
			continue
		}
		if v == "" {
			v = str(o.Current)
		}
		out[o.ID] = v
	}
	return out
}

// titleSection is the settings the agent titles sessions with, once a
// test has found what it offers.
func (a *App) titleSection(c *ui.Context, p *palette) {
	f := &a.provider
	f.ownsTitle = a.titleOwned(f)
	if f.kind.group == "preset" || f.ownsTitle {
		return
	}
	cap, ready := a.formCapability(f)
	options := titleOptions(cap, f.title)
	if ready && len(options) == 0 {
		return
	}
	if ready && !f.titled {
		f.titled = true
		for k, v := range titleDefaults(f.kind.cliType, f.agentType(), options) {
			if _, set := f.title[k]; !set {
				f.title[k] = v
			}
		}
	}
	a.providerSection(c, p, "title", locales.ProviderTitleGen(), func() {
		if !ready {
			hint := locales.ProviderTitleGenHint()
			if f.testing {
				hint = locales.ProviderProbing()
			}
			ui.Text(c, hint).FontSize(sizeFoot).TextColor(p.Secondary)
			return
		}
		for _, o := range options {
			opt := o
			ui.Row(c.Key(o.ID)).FillWidth().MinHeight(32).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, firstNonEmpty(opt.Name, opt.ID)).FontSize(sizeSub).TextColor(p.Text).Grow(1).MinWidth(0).SingleLine()
				if opt.Type == "boolean" {
					on, _ := f.title[opt.ID].(bool)
					if _, set := f.title[opt.ID]; !set {
						on, _ = opt.Current.(bool)
					}
					was := on
					ui.Switch(c, &on).Label(opt.Name)
					if on != was {
						f.title[opt.ID] = on
					}
					return
				}
				current := str(opt.Current)
				if v, ok := f.title[opt.ID].(string); ok && slices.ContainsFunc(opt.Choices, func(ch catalog.Choice) bool { return ch.Value == v }) {
					current = v
				}
				var choices []option
				label := current
				for _, ch := range opt.Choices {
					choices = append(choices, option{ch.Value, ch.Name})
					if ch.Value == current {
						label = ch.Name
					}
				}
				a.toolbarPicker(c, p, "title-"+opt.ID, nil, label, choices, current, func(v string) { f.title[opt.ID] = v })
			})
		}
	})
}

// binaryState is a registry agent's download on the machine.
type binaryState struct {
	key     string // machine and agent it is for
	status  string // checking, installed, not-installed, downloading, unsupported-platform, incompatible-host or error
	phase   string // the download's step
	percent float64
	err     string
}

// binaryReady is whether the form's agent can run: anything but a
// registry download not yet on the machine.
func (a *App) binaryReady(f *providerForm) bool {
	if !f.kind.binary {
		return true
	}
	return f.binary != nil && f.binary.key == f.machine+"/"+f.kind.agentType && (f.binary.status == "installed" || f.binary.status == "not-applicable")
}

func (a *App) checkBinary(st *binaryState, machine, agentType string) {
	conn := a.conn
	if conn == nil {
		st.status = "error"
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		raw, err := conn.MachineRPC(ctx, machine, "machine/acp-binary-status", map[string]any{"agentType": agentType}, 30*time.Second)
		var res struct {
			Success bool   `json:"success"`
			Status  string `json:"status"`
			Error   string `json:"error"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &res)
		}
		a.update(func() {
			if err != nil {
				st.status, st.err = "error", err.Error()
				return
			}
			st.status, st.err = res.Status, res.Error
		})
	}()
}

func (a *App) installBinary(st *binaryState, machine, agentType string) {
	conn := a.conn
	if conn == nil {
		return
	}
	st.status, st.phase, st.percent, st.err = "downloading", "downloading", -1, ""
	go func() {
		raw, err := conn.MachineRPCProgress(context.Background(), machine, "machine/acp-binary-install", map[string]any{"agentType": agentType}, 120*time.Second, func(raw json.RawMessage) {
			var pr struct {
				Status  string   `json:"status"`
				Percent *float64 `json:"percent"`
			}
			if json.Unmarshal(raw, &pr) != nil {
				return
			}
			a.update(func() {
				st.phase = pr.Status
				if pr.Percent != nil {
					st.percent = *pr.Percent
				}
			})
		})
		var res struct {
			Success bool   `json:"success"`
			Error   string `json:"error"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &res)
		}
		a.update(func() {
			switch {
			case err != nil:
				st.status, st.err = "error", err.Error()
			case !res.Success:
				st.status, st.err = "error", firstNonEmpty(res.Error, locales.BinaryDownloadFailed())
			default:
				st.status = "installed"
			}
		})
	}()
}

// binaryField is a registry agent's download: its state on the machine,
// and the button that fetches it.
func (a *App) binaryField(c *ui.Context, p *palette) {
	f := &a.provider
	key := f.machine + "/" + f.kind.agentType
	if f.binary == nil || f.binary.key != key {
		f.binary = &binaryState{key: key, status: "checking"}
		a.checkBinary(f.binary, f.machine, f.kind.agentType)
	}
	st := f.binary
	if st.status == "installed" || st.status == "not-applicable" {
		return
	}
	ui.Row(c).FillWidth().Wrap().Gap(10).AlignItems(ui.Center).Padding(10, 12).Radius(8).Background(p.Text.Alpha(0.035)).Children(func() {
		var text string
		switch st.status {
		case "checking":
			ui.Spinner(c).Size(13, 13)
			text = locales.BinaryChecking()
		case "downloading":
			ui.Spinner(c).Size(13, 13)
			switch st.phase {
			case "verifying":
				text = locales.BinaryVerifying()
			case "extracting":
				text = locales.BinaryExtracting()
			case "publishing":
				text = locales.BinaryPublishing()
			default:
				text = locales.BinaryDownloading()
				if st.percent >= 0 {
					text = locales.BinaryDownloadingPercent(int(st.percent))
				}
			}
		case "unsupported-platform", "incompatible-host":
			text = locales.BinaryUnsupported()
		case "error":
			text = firstNonEmpty(st.err, locales.BinaryError())
		default:
			text = locales.BinaryNotInstalled()
		}
		color := p.Secondary
		if st.status == "error" {
			color = p.Danger
		}
		ui.Text(c, text).FontSize(sizeFoot).TextColor(color).Grow(1).MinWidth(0).Selectable()
		if st.status == "not-installed" || st.status == "error" {
			label := locales.BinaryDownload()
			if st.status == "error" {
				label = locales.BinaryRetry()
			}
			machine, agentType := f.machine, f.kind.agentType
			outlineButton(c, p, iconDownload, label).OnClick(func() { a.installBinary(st, machine, agentType) })
		}
	})
}
