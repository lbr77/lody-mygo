package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/locales"
)

// Magpie, a local model gateway, hands Lody its providers with a
// lody://provider/import?v=1&data=… link, as Lody's desktop app reads it:
// a versioned, bounded envelope naming the gateway on this computer
// (http on 127.0.0.1, localhost or [::1], nothing else), its fixed public
// token, and which runtimes it serves. A link that bends any of that is
// refused, saying so. A good one asks first, in Import from Magpie, which
// runtimes to add to this machine, all it can take ticked; importing adds
// each as a built-in provider over the gateway, the ones Lody sets up in
// the background as setup tasks. A runtime already imported from the same
// gateway is left as it is, so opening a link again, or retrying one that
// half failed, adds nothing twice.

// magpieToken is the gateway's public token: a label of its use, never a
// vendor's credential.
const magpieToken = "magpie-lody"

// magpieTargets are the runtimes a gateway can serve, in Lody's order.
var magpieTargets = []string{"claude", "codex", "pi", "dsh", "kimi", "grok", "bub"}

// magpieLabels name the runtimes, as the providers they become do.
var magpieLabels = map[string]string{"claude": "Claude", "codex": "Codex", "pi": "Pi", "dsh": "DSH", "kimi": "Kimi Code", "grok": "Grok", "bub": "Bub"}

// magpieSchemes are Lody's schemes a Magpie link may come under.
var magpieSchemes = []string{"lody", "lody-oss", "ai.lody.stable", "ai.lody.nightly"}

var (
	magpieGatewayPattern = regexp.MustCompile(`^http://(127\.0\.0\.1|localhost|\[::1\])(?::[0-9]+)?/?$`)
	magpieDataPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// errMagpie is any way a Magpie link can be bent.
var errMagpie = errors.New("invalid Magpie import")

// isMagpieLink is whether raw is a Magpie import link at all, good or bad.
func isMagpieLink(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && slices.Contains(magpieSchemes, strings.ToLower(u.Scheme)) && u.Hostname() == "provider" && u.EscapedPath() == "/import"
}

// magpieGateway is a gateway's origin, when it is a local HTTP gateway.
func magpieGateway(value string) (string, error) {
	if len(value) > 256 || !magpieGatewayPattern.MatchString(value) {
		return "", errMagpie
	}
	u, err := url.Parse(value)
	if err != nil {
		return "", errMagpie
	}
	host := u.Host
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n > 65535 {
			return "", errMagpie
		}
		host = u.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		if n != 80 {
			host += ":" + strconv.Itoa(n)
		}
	}
	return "http://" + host, nil
}

// parseMagpieLink reads a Magpie import link: its gateway, and the
// runtimes it serves in Lody's order.
func parseMagpieLink(raw string) (gateway string, targets []string, err error) {
	if len(raw) > 8192 || !isMagpieLink(raw) {
		return "", nil, errMagpie
	}
	u, _ := url.Parse(raw)
	q, qerr := url.ParseQuery(u.RawQuery)
	if qerr != nil || u.User != nil || u.Port() != "" || u.Fragment != "" || strings.Contains(raw, "#") ||
		len(q) != 2 || len(q["v"]) != 1 || q.Get("v") != "1" || len(q["data"]) != 1 || !magpieDataPattern.MatchString(q.Get("data")) {
		return "", nil, errMagpie
	}
	data, derr := base64.RawURLEncoding.DecodeString(q.Get("data"))
	if derr != nil || !utf8.Valid(data) {
		return "", nil, errMagpie
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(data, &payload) != nil || payload == nil {
		return "", nil, errMagpie
	}
	for k := range payload {
		if !slices.Contains([]string{"kind", "id", "name", "auth", "endpoints"}, k) {
			return "", nil, errMagpie
		}
	}
	text := func(raw json.RawMessage) string {
		var s string
		json.Unmarshal(raw, &s)
		return s
	}
	if text(payload["kind"]) != "custom" || text(payload["id"]) != "magpie" || text(payload["name"]) != "Magpie" {
		return "", nil, errMagpie
	}
	var auth map[string]json.RawMessage
	if json.Unmarshal(payload["auth"], &auth) != nil || len(auth) != 2 || text(auth["method"]) != "apiKey" || text(auth["apiKey"]) != magpieToken {
		return "", nil, errMagpie
	}
	var endpoints []map[string]json.RawMessage
	if json.Unmarshal(payload["endpoints"], &endpoints) != nil || len(endpoints) == 0 || len(endpoints) > 3 {
		return "", nil, errMagpie
	}
	served := map[string]bool{}
	for _, e := range endpoints {
		if e == nil {
			return "", nil, errMagpie
		}
		for k := range e {
			if !slices.Contains([]string{"protocol", "baseUrl", "targets", "modelsUrl"}, k) {
				return "", nil, errMagpie
			}
		}
		var base string
		var names []json.RawMessage
		if json.Unmarshal(e["baseUrl"], &base) != nil || json.Unmarshal(e["targets"], &names) != nil || len(names) == 0 {
			return "", nil, errMagpie
		}
		var allowed map[string]string
		switch text(e["protocol"]) {
		case "anthropic-messages":
			allowed = map[string]string{"claude-code": "claude"}
		case "openai-responses":
			allowed = map[string]string{"codex": "codex"}
		case "openai-chat":
			allowed = map[string]string{"pi": "pi", "dsh": "dsh", "kimi-code": "kimi", "grok": "grok", "bub": "bub"}
		default:
			return "", nil, errMagpie
		}
		if text(e["protocol"]) != "anthropic-messages" {
			if !strings.HasSuffix(base, "/v1") {
				return "", nil, errMagpie
			}
			base = strings.TrimSuffix(base, "/v1")
		}
		g, err := magpieGateway(base)
		if err != nil || (gateway != "" && gateway != g) || text(e["modelsUrl"]) != g+"/v1/models" {
			return "", nil, errMagpie
		}
		gateway = g
		for _, n := range names {
			var name string
			if json.Unmarshal(n, &name) != nil {
				return "", nil, errMagpie
			}
			t, ok := allowed[name]
			if !ok || served[t] {
				return "", nil, errMagpie
			}
			served[t] = true
		}
	}
	for _, t := range magpieTargets {
		if served[t] {
			targets = append(targets, t)
		}
	}
	return gateway, targets, nil
}

// magpieConfig is the provider a runtime becomes over the gateway: Lody's
// built-in agent, named for the runtime, its keys pointed at the gateway.
func magpieConfig(target, gateway string) map[string]any {
	env := map[string]any{}
	switch target {
	case "claude":
		env["ANTHROPIC_BASE_URL"], env["ANTHROPIC_AUTH_TOKEN"] = gateway, magpieToken
	case "codex":
		env["OPENAI_API_KEY"], env["OPENAI_BASE_URL"] = magpieToken, gateway+"/v1"
	case "dsh":
		env["DEEPSEEK_API_KEY"], env["DEEPSEEK_BASE_URL"] = magpieToken, gateway+"/v1"
	}
	agentType := target
	if target == "dsh" {
		agentType = "deepseek"
	}
	return map[string]any{"magpieGatewayUrl": gateway, "name": magpieLabels[target] + "-magpie", "cliType": "builtin", "agentType": agentType, "env": env}
}

// magpieEnv is a runtime's keys, as its row shows them.
func magpieEnv(config map[string]any) string {
	env, _ := config["env"].(map[string]any)
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	// In the order Lody writes them: Anthropic's URL before its token, the
	// key before the URL for OpenAI and DeepSeek.
	slices.SortFunc(keys, func(x, y string) int { return magpieKeyRank(x) - magpieKeyRank(y) })
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + str(env[k])
	}
	return strings.Join(parts, " · ")
}

func magpieKeyRank(k string) int {
	switch {
	case k == "ANTHROPIC_BASE_URL", strings.HasSuffix(k, "_API_KEY"):
		return 0
	}
	return 1
}

// magpieImport is the import a Magpie link asked for, while its dialog
// shows.
type magpieImport struct {
	open     bool
	request  string // new with each link
	gateway  string
	targets  []string
	ids      map[string]string // each runtime's provider id, kept across retries
	picked   []string          // the runtimes ticked, once the user changes them
	touched  bool
	busy     bool
	err      string
	finished string // the request that was imported
}

// openMagpie takes a Magpie link, saying so when it is bent.
func (a *App) openMagpie(raw string) {
	gateway, targets, err := parseMagpieLink(raw)
	if err != nil {
		a.toast("error", locales.MagpieImportInvalidLink())
		return
	}
	ids := map[string]string{}
	for _, t := range targets {
		ids[t] = uuid.NewString()
	}
	a.magpie = magpieImport{open: true, request: uuid.NewString(), gateway: gateway, targets: targets, ids: ids}
}

// magpieUnavailable is why a runtime can't be imported to machine, "" when
// it can.
func (a *App) magpieUnavailable(machine, target string) string {
	switch {
	case machine == "":
		return locales.MagpieImportLocalUnavailable()
	case a.machineCap(machine, "magpieImport") < 1 || a.machineCap(machine, "providerSetup") < 1:
		return locales.MagpieImportUpdateRequired()
	case target == "pi" && a.machineCap(machine, "builtinPi") < 1:
		return locales.MagpieImportPiUnavailable()
	}
	return ""
}

// magpieSelection is what Import adds: every runtime it can take until
// the user changes the ticks, then theirs, in Lody's order.
func (a *App) magpieSelection(machine string) []string {
	m := &a.magpie
	var out []string
	for _, t := range m.targets {
		if a.magpieUnavailable(machine, t) == "" && (!m.touched || slices.Contains(m.picked, t)) {
			out = append(out, t)
		}
	}
	return out
}

// importMagpie adds the runtimes picked to machine in one write, past
// those already imported from the gateway.
func (a *App) importMagpie(machine string, picked []string) {
	m := &a.magpie
	if m.busy || len(picked) == 0 {
		return
	}
	if a.sync == nil {
		m.err = locales.MagpieImportFailed()
		return
	}
	request, gateway := m.request, m.gateway
	var adds []magpieAdd
	for _, t := range picked {
		cfg := magpieConfig(t, gateway)
		cfg["id"], cfg["machineId"] = m.ids[t], machine
		adds = append(adds, magpieAdd{m.ids[t], cfg})
	}
	// What the catalog already holds of this gateway's, read here, and the
	// stream's own rows as the write sees them.
	known := map[string]bool{}
	for _, ag := range a.catalog.Agents[machine] {
		if ag.CLIType == "builtin" && str(ag.Row["magpieGatewayUrl"]) == gateway {
			known[ag.AgentType] = true
		}
	}
	for _, st := range a.catalog.ProviderSetups[machine] {
		if str(st.Config["cliType"]) == "builtin" && str(st.Config["magpieGatewayUrl"]) == gateway {
			known[str(st.Config["agentType"])] = true
		}
	}
	m.busy, m.err = true, ""
	a.streamWrite(machine, func(fl *flock.Flock) error {
		return writeMagpie(fl, machine, gateway, adds, known, float64(time.Now().UnixMilli()))
	}, func(err error) {
		if a.magpie.request != request {
			return // a newer link replaced it
		}
		m.busy = false
		if err != nil {
			a.settingsError = ""
			m.err = locales.MagpieImportFailed()
			return
		}
		m.open, m.finished = false, request
		a.toast("success", locales.MagpieImportAdded())
	})
}

// magpieAdd is a provider an import adds, by its id.
type magpieAdd struct {
	id     string
	config map[string]any
}

// writeMagpie adds the providers to a machine's stream, past the agent
// types known from the gateway already and those its rows hold: as a setup
// task for a built-in the machine sets up in the background, else as an
// agent config.
func writeMagpie(fl *flock.Flock, machine, gateway string, adds []magpieAdd, known map[string]bool, now float64) error {
	rows, err := fl.Scan()
	if err != nil {
		return err
	}
	for _, r := range rows {
		if !r.Live || len(r.Key) != 2 {
			continue
		}
		cfg, _ := r.Value.(map[string]any)
		if r.Key[0] == "providerSetup" {
			cfg, _ = cfg["config"].(map[string]any)
		} else if r.Key[0] != "agentConfig" {
			continue
		}
		if str(cfg["machineId"]) == machine && str(cfg["cliType"]) == "builtin" && str(cfg["magpieGatewayUrl"]) == gateway {
			known[str(cfg["agentType"])] = true
		}
	}
	for _, add := range adds {
		agentType := str(add.config["agentType"])
		if known[agentType] {
			continue
		}
		// Adding a built-in takes it off the opted-out ones.
		if supportsSetup(agentType) || slices.Contains(managedTypes, agentType) {
			if _, ok, _ := fl.Get([]any{"builtinAgentOptOut", agentType}); ok {
				if err := fl.Delete([]any{"builtinAgentOptOut", agentType}); err != nil {
					return err
				}
			}
		}
		if supportsSetup(agentType) {
			task := map[string]any{"v": 1, "id": add.id, "machineId": machine, "config": add.config, "status": "queued", "attempt": 1, "createdAt": now, "updatedAt": now}
			if err := fl.Put([]any{"providerSetup", add.id}, task); err != nil {
				return err
			}
			continue
		}
		if err := fl.Put([]any{"agentConfig", add.id}, add.config); err != nil {
			return err
		}
	}
	return nil
}

// magpieDialog is Import from Magpie: the machine the runtimes go to, each
// runtime with its keys or why it can't be imported, and Import.
func (a *App) magpieDialog(c *ui.Context, p *palette) {
	m := &a.magpie
	if !m.open && !m.busy {
		return
	}
	machine := a.localMachine()
	if machine != "" && !slices.Contains(a.catalog.MachineIDs, machine) {
		machine = ""
	}
	name := locales.MagpieImportThisMachine()
	if n := strings.TrimSpace(a.catalog.MachineNames[machine]); n != "" {
		name = n
	}
	picked := a.magpieSelection(machine)
	open := m.open
	lodyDialog(c, p, &open, locales.MagpieImportTitle(), func() {
		lodyDialogHeader(c, p, locales.MagpieImportTitle(), locales.MagpieImportDescription(), agentSVG("magpie"))
		ui.Column(c.Key("magpie-body")).FillWidth().Gap(12).Children(func() {
			ui.Row(c.Key("where")).FillWidth().Gap(8).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, locales.MagpieImportMachineLineLabel()).FontSize(sizeFoot).TextColor(p.Tertiary).Shrink(0)
				ui.Text(c, name).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text).SingleLine().MinWidth(0)
			})
			if len(m.targets) == 0 {
				ui.Text(c, locales.MagpieImportEmpty()).FontSize(sizeFoot).TextColor(p.Secondary)
			} else {
				ui.Column(c.Key("runtimes")).FillWidth().Dividers(1, p.Border).Label(locales.MagpieImportRuntimes()).Children(func() {
					for _, t := range m.targets {
						a.magpieRow(c, p, machine, t, slices.Contains(picked, t))
					}
				})
			}
			if m.err != "" && !m.busy {
				ui.Row(c.Key("error")).FillWidth().Gap(8).AlignItems(ui.Start).Padding(8, 12).Radius(10).Background(p.Danger.Alpha(0.1)).Children(func() {
					ui.Icon(c, iconTriangleAlert).Size(14, 14).TextColor(p.Danger).Shrink(0).Margin(2, 0, 0, 0)
					ui.Text(c, m.err).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Danger).MinWidth(0).Selectable()
				})
			}
		})
		ui.Row(c.Key("magpie-foot")).FillWidth().Gap(8).Justify(ui.End).Children(func() {
			lodyButton(c, p, "secondary", "magpie-cancel", nil, locales.Cancel(), !m.busy).OnClick(func() { m.open = false })
			label := locales.MagpieImportImport()
			if m.busy {
				label = locales.MagpieImportImporting()
			}
			lodyButton(c, p, "primary", "magpie-import", nil, label, !m.busy && len(picked) > 0, m.busy).OnClick(func() {
				a.importMagpie(machine, picked)
			})
		})
	})
	// Closing drops the request; an import under way holds it open.
	if !open && !m.busy {
		m.open = false
	}
}

// magpieRow is one runtime: its tick, its mark, its provider's name, and
// its keys, or why it can't be imported here.
func (a *App) magpieRow(c *ui.Context, p *palette, machine, target string, on bool) {
	m := &a.magpie
	cfg := magpieConfig(target, m.gateway)
	reason := a.magpieUnavailable(machine, target)
	disabled := reason != "" || m.busy
	sub := reason
	if sub == "" {
		if slices.Contains([]string{"kimi", "grok", "bub"}, target) {
			sub = locales.MagpieImportIndependentConfig()
		} else {
			sub = magpieEnv(cfg)
		}
	}
	label := str(cfg["name"])
	row := ui.ButtonBase(c.Key(target)).FillWidth().Padding(12, 0).Gap(12).AlignItems(ui.Center).Justify(ui.Start).Label(label).Disabled(disabled)
	if !disabled {
		row.Cursor(ui.CursorPointer).OnClick(func() {
			// Ticking starts from what shows: every runtime it can take.
			next := []string{}
			for _, t := range a.magpieSelection(machine) {
				if t != target {
					next = append(next, t)
				}
			}
			if !on {
				next = append(next, target)
			}
			m.picked, m.touched = next, true
		})
	}
	row.Children(func() {
		box := lodyCheckbox(c, p, "tick", on, false)
		if disabled {
			box.Opacity(0.45)
		}
		ui.Box(c).Size(32, 32).Shrink(0).Center().Children(func() {
			ui.Icon(c, agentMark("builtin", str(cfg["agentType"]), nil)).Size(20, 20).TextColor(p.Text)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ink := p.Text
			if reason != "" {
				ink = p.Secondary
			}
			ui.Text(c, label).FontSize(sizeFoot).TextColor(ink).SingleLine()
			ui.Text(c, sub).Font(fontMono).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine().Description(sub)
		})
	})
}
