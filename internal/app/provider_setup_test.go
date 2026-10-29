package app

import (
	"testing"
	"time"

	"github.com/lbr77/lody-mygo/internal/catalog"
)

// A provider a machine is still setting up lists with the providers: its
// mark orbits while the machine works, its row says how far it has got,
// and a failure offers Retry.
func TestProviderSetupRows(t *testing.T) {
	a := fixture()
	a.settingsOpen, a.settingsTab = true, "agents"
	a.catalog.MachineCaps = map[string]map[string]float64{"m1": {"providerSetup": 1}}
	a.catalog.ProviderSetups = map[string][]catalog.ProviderSetup{"m1": {
		{ID: "k1", MachineID: "m1", Status: "preparing-runtime", Attempt: 1, Config: map[string]any{"name": "Kimi", "cliType": "builtin", "agentType": "kimi"}},
		{ID: "d1", MachineID: "m1", Status: "failed", FailureCode: "runtime-install-failed", Attempt: 1, Config: map[string]any{"name": "Devin", "cliType": "builtin", "agentType": "devin"}},
	}}
	tt := snap(t, a, "provider-setup", false, nil)
	tt.Advance(700 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "provider-setup-orbit")
	for _, want := range []string{"Kimi", "Downloading the agent runtime…", "Working", "Devin", "The agent runtime could not be downloaded.", "Retry", "Claude Code"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	if got := a.setupReadiness(a.catalog.ProviderSetups["m1"][0]); got != "arriving" {
		t.Fatalf("readiness %q", got)
	}
	if got := a.setupReadiness(a.catalog.ProviderSetups["m1"][1]); got != "cold" {
		t.Fatalf("readiness of a failure %q", got)
	}
	// What the machine said of the runtime's download shows, measured.
	pct := 42.0
	a.noteBinary("m1", "kimi", "downloading", &pct, "")
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "provider-setup-percent")
	if !tt.HasText("42%") || !tt.HasText("Downloading the agent runtime… 42%") {
		t.Fatalf("no progress in %q", tt.Texts())
	}
	// A new managed built-in on such a machine is set up in the background;
	// on one without the protocol, it is written at once.
	a.openProvider("m1", nil)
	for _, k := range allKinds {
		if k.value == "builtin:kimi" {
			a.selectKind(k)
		}
	}
	if !a.backgroundSetup(&a.provider) {
		t.Fatal("not a background setup")
	}
	a.catalog.MachineCaps["m1"]["providerSetup"] = 0
	if a.backgroundSetup(&a.provider) {
		t.Fatal("background setup on a machine without the protocol")
	}
	a.catalog.MachineCaps["m1"]["providerSetup"] = 1
	a.provider.editing = &a.catalog.Agents["m1"][0]
	if a.backgroundSetup(&a.provider) {
		t.Fatal("background setup of an edit")
	}

	// Bub only its machine can install: the dialog waits on the setup, then
	// edits the provider it published.
	a.provider.editing = nil
	for _, k := range allKinds {
		if k.value == "builtin:bub" {
			a.selectKind(k)
		}
	}
	a.provider.name = "Bub"
	if !queuedBuiltin(&a.provider) || !a.backgroundSetup(&a.provider) {
		t.Fatal("bub is not waited on")
	}
	a.saveProvider(false)
	id := a.provider.id
	if a.provider.setupWait != id || !a.provider.open {
		t.Fatalf("not waiting: %+v", a.provider.setupWait)
	}
	a.catalog.ProviderSetups["m1"] = append(a.catalog.ProviderSetups["m1"], catalog.ProviderSetup{ID: id, MachineID: "m1", Status: "verifying", Config: map[string]any{"name": "Bub", "cliType": "builtin", "agentType": "bub"}})
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "provider-setup-dialog")
	if !tt.HasText("Checking credentials and provider access…") {
		t.Fatalf("no setup in the dialog: %q", tt.Texts())
	}
	a.catalog.Agents["m1"] = append(a.catalog.Agents["m1"], catalog.AgentConfig{ID: id, Name: "Bub", MachineID: "m1", AgentType: "bub", CLIType: "builtin"})
	tt.Frame()
	if a.provider.setupWait != "" || a.provider.editing == nil || a.provider.editing.ID != id {
		t.Fatalf("not editing the published provider: %+v", a.provider.editing)
	}
	// Without the protocol, Add is held, saying why.
	a.provider.editing = nil
	a.provider.id = "x"
	a.catalog.MachineCaps["m1"]["providerSetup"] = 0
	tt.Frame()
	if !tt.HasText("Update Lody on the target machine to finish this provider setup.") {
		t.Fatalf("no reason in %q", tt.Texts())
	}
}
