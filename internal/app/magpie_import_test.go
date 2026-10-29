package app

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/locales"
)

// magpieEnvelope is the envelope a Magpie gateway on port 3425 hands
// Lody, which bend changes before it is sent.
func magpieEnvelope(bend func(p map[string]any)) string {
	endpoint := func(protocol, base string, targets ...any) map[string]any {
		return map[string]any{"protocol": protocol, "baseUrl": base, "targets": targets, "modelsUrl": "http://127.0.0.1:3425/v1/models"}
	}
	p := map[string]any{"kind": "custom", "id": "magpie", "name": "Magpie", "auth": map[string]any{"method": "apiKey", "apiKey": "magpie-lody"},
		"endpoints": []any{
			endpoint("anthropic-messages", "http://127.0.0.1:3425", "claude-code"),
			endpoint("openai-responses", "http://127.0.0.1:3425/v1", "codex"),
			endpoint("openai-chat", "http://127.0.0.1:3425/v1", "kimi-code", "pi", "dsh"),
		}}
	if bend != nil {
		bend(p)
	}
	b, _ := json.Marshal(p)
	return "lody://provider/import?v=1&data=" + base64.RawURLEncoding.EncodeToString(b)
}

// A Magpie link is read as Lody's reads it: a local HTTP gateway, its
// public token, the runtimes it serves in Lody's order; any bend of it is
// refused.
func TestParseMagpieLink(t *testing.T) {
	gateway, targets, err := parseMagpieLink(magpieEnvelope(nil))
	if err != nil || gateway != "http://127.0.0.1:3425" || !slices.Equal(targets, []string{"claude", "codex", "pi", "dsh", "kimi"}) {
		t.Fatalf("good link: %q %q %v", gateway, targets, err)
	}
	// The default port drops out of the gateway, as a URL's origin does.
	at80 := magpieEnvelope(func(p map[string]any) {
		p["endpoints"] = []any{map[string]any{"protocol": "anthropic-messages", "baseUrl": "http://localhost:80/", "targets": []any{"claude-code"}, "modelsUrl": "http://localhost/v1/models"}}
	})
	if gateway, _, err := parseMagpieLink(at80); err != nil || gateway != "http://localhost" {
		t.Fatalf("port 80: %q %v", gateway, err)
	}
	endpoint := func(p map[string]any) map[string]any { return p["endpoints"].([]any)[0].(map[string]any) }
	for name, bad := range map[string]string{
		"remote gateway": magpieEnvelope(func(p map[string]any) { endpoint(p)["baseUrl"] = "http://example.com:3425" }),
		"https gateway":  magpieEnvelope(func(p map[string]any) { endpoint(p)["baseUrl"] = "https://127.0.0.1:3425" }),
		"path":           magpieEnvelope(func(p map[string]any) { endpoint(p)["baseUrl"] = "http://127.0.0.1:3425/x" }),
		"other token": magpieEnvelope(func(p map[string]any) {
			p["auth"] = map[string]any{"method": "apiKey", "apiKey": "sk-real"}
		}),
		"extra field":    magpieEnvelope(func(p map[string]any) { p["command"] = "rm -rf" }),
		"extra endpoint": magpieEnvelope(func(p map[string]any) { endpoint(p)["env"] = map[string]any{} }),
		"twice":          magpieEnvelope(func(p map[string]any) { endpoint(p)["targets"] = []any{"claude-code", "claude-code"} }),
		"wrong protocol": magpieEnvelope(func(p map[string]any) { endpoint(p)["targets"] = []any{"codex"} }),
		"models":         magpieEnvelope(func(p map[string]any) { endpoint(p)["modelsUrl"] = "http://127.0.0.1:9/v1/models" }),
		"two gateways": magpieEnvelope(func(p map[string]any) {
			e := endpoint(p)
			e["baseUrl"], e["modelsUrl"] = "http://127.0.0.1:9", "http://127.0.0.1:9/v1/models"
		}),
		"too many": magpieEnvelope(func(p map[string]any) {
			p["endpoints"] = append(p["endpoints"].([]any), p["endpoints"].([]any)[0])
		}),
		"version":  "lody://provider/import?v=2&data=e30",
		"no data":  "lody://provider/import?v=1",
		"extra":    magpieEnvelope(nil) + "&x=1",
		"fragment": magpieEnvelope(nil) + "#x",
		"padding":  magpieEnvelope(nil) + "=",
		"other":    "lody://provider/export?v=1&data=e30",
	} {
		if _, _, err := parseMagpieLink(bad); err == nil {
			t.Errorf("%s: accepted %s", name, bad)
		}
	}
}

// A Magpie link asks first, in Import from Magpie, which runtimes to add
// to this machine: those it can take ticked, the others saying why; a bent
// link says it is invalid.
func TestMagpieDialog(t *testing.T) {
	a := fixture()
	a.term.machine, a.term.checked = "m1", time.Now()
	a.catalog.MachineCaps = map[string]map[string]float64{"m1": {"magpieImport": 1, "providerSetup": 1}}
	a.OpenURL(magpieEnvelope(nil))
	if !a.magpie.open || a.magpie.gateway != "http://127.0.0.1:3425" || len(a.magpie.ids) != 5 {
		t.Fatalf("request: %+v", a.magpie)
	}
	tt := snap(t, a, "magpie-import", false, nil)
	for _, want := range []string{
		locales.MagpieImportTitle(), locales.MagpieImportDescription(), locales.MagpieImportMachineLineLabel(), "Studio",
		"Claude-magpie", "ANTHROPIC_BASE_URL=http://127.0.0.1:3425 · ANTHROPIC_AUTH_TOKEN=magpie-lody",
		"Codex-magpie", "OPENAI_API_KEY=magpie-lody · OPENAI_BASE_URL=http://127.0.0.1:3425/v1",
		"DSH-magpie", "Kimi Code-magpie", locales.MagpieImportIndependentConfig(),
		"Pi-magpie", locales.MagpieImportPiUnavailable(), locales.MagpieImportImport(),
	} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if got := a.magpieSelection("m1"); !slices.Equal(got, []string{"claude", "codex", "dsh", "kimi"}) {
		t.Fatalf("ticked: %q", got)
	}
	// Unticking one leaves the rest; Pi can't be ticked here.
	for _, label := range []string{"Codex-magpie", "Pi-magpie"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	if got := a.magpieSelection("m1"); !slices.Equal(got, []string{"claude", "dsh", "kimi"}) {
		t.Fatalf("after unticking Codex: %q", got)
	}
	// Without the workspace's stream, the import fails as Lody's says.
	if err := tt.Click(locales.MagpieImportImport()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(locales.MagpieImportFailed()) || a.magpie.busy {
		t.Fatalf("failure: %q", tt.Texts())
	}

	// A machine that can't take Magpie says so on every runtime.
	a.catalog.MachineCaps = nil
	tt.Frame()
	if !tt.HasText(locales.MagpieImportUpdateRequired()) || len(a.magpieSelection("m1")) != 0 {
		t.Fatalf("update required: %q", tt.Texts())
	}
	a.term.machine = ""
	if a.magpieUnavailable(a.localMachine(), "claude") != locales.MagpieImportLocalUnavailable() {
		t.Fatal("no local agent")
	}

	// A new link starts over; a bent one says it is invalid.
	request := a.magpie.request
	a.OpenURL(magpieEnvelope(nil))
	if a.magpie.request == request || a.magpie.touched || a.magpie.err != "" {
		t.Fatalf("new link: %+v", a.magpie)
	}
	a.toasts = nil
	a.OpenURL("lody://provider/import?v=1&data=e30")
	if lastToast(a) != locales.MagpieImportInvalidLink() {
		t.Fatalf("invalid link: %+v", a.toasts)
	}
}

// Importing adds each runtime once: a background setup task for a
// built-in Lody sets up, an agent config for DeepSeek, an opt-out taken
// back; a runtime already imported from the gateway, as a config or a
// setup, is left as it is.
func TestWriteMagpie(t *testing.T) {
	fl, err := flock.New("t")
	if err != nil {
		t.Fatal(err)
	}
	gateway := "http://127.0.0.1:3425"
	fl.Put([]any{"agentConfig", "old"}, map[string]any{"id": "old", "machineId": "m1", "cliType": "builtin", "agentType": "claude", "magpieGatewayUrl": gateway, "name": "Mine"})
	fl.Put([]any{"builtinAgentOptOut", "codex"}, true)
	adds := []magpieAdd{}
	for _, target := range []string{"claude", "codex", "dsh"} {
		cfg := magpieConfig(target, gateway)
		cfg["id"], cfg["machineId"] = "id-"+target, "m1"
		adds = append(adds, magpieAdd{"id-" + target, cfg})
	}
	if err := writeMagpie(fl, "m1", gateway, adds, map[string]bool{}, 1); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := fl.Get([]any{"agentConfig", "old"}); !ok || v.(map[string]any)["name"] != "Mine" {
		t.Fatalf("the earlier import changed: %v", v)
	}
	if _, ok, _ := fl.Get([]any{"agentConfig", "id-claude"}); ok {
		t.Fatal("Claude imported twice")
	}
	task, ok, _ := fl.Get([]any{"providerSetup", "id-codex"})
	if !ok || task.(map[string]any)["status"] != "queued" || task.(map[string]any)["config"].(map[string]any)["name"] != "Codex-magpie" {
		t.Fatalf("Codex setup: %v", task)
	}
	if _, ok, _ := fl.Get([]any{"builtinAgentOptOut", "codex"}); ok {
		t.Fatal("Codex still opted out")
	}
	dsh, ok, _ := fl.Get([]any{"agentConfig", "id-dsh"})
	if !ok || dsh.(map[string]any)["agentType"] != "deepseek" || dsh.(map[string]any)["env"].(map[string]any)["DEEPSEEK_BASE_URL"] != gateway+"/v1" {
		t.Fatalf("DeepSeek config: %v", dsh)
	}
	// Again, as a retried link would: nothing changes.
	before, _ := fl.Scan()
	for i := range adds {
		adds[i].id += "-again"
	}
	if err := writeMagpie(fl, "m1", gateway, adds, map[string]bool{}, 2); err != nil {
		t.Fatal(err)
	}
	if after, _ := fl.Scan(); len(after) != len(before) {
		t.Fatalf("retry added rows: %d → %d", len(before), len(after))
	}
}
