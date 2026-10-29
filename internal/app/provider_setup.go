package app

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/locales"
)

// A built-in provider added to a machine that speaks Lody's provider setup
// protocol is not written as an agent config but as a setup task the
// machine carries out on its own: it downloads the agent's runtime,
// checks it starts and can reach its provider, and publishes the config.
// Meanwhile the provider's row says how far it has got, its mark carrying
// a ring that orbits every 1.4s; a failure offers Retry, and Delete cancels
// it.

// binaryProgress is the last a machine said of an agent runtime's
// download, to this app: what the setup rows report, as Lody's do.
type binaryProgress struct {
	status  string // checking, downloading, verifying, extracting, publishing, installed or error
	percent float64
	known   bool // percent is measured
	err     string
}

// noteBinary keeps what a machine said of a runtime's download.
func (a *App) noteBinary(machine, agentType, status string, percent *float64, err string) {
	if a.binaries == nil {
		a.binaries = map[string]binaryProgress{}
	}
	bp := binaryProgress{status: status, err: err}
	if percent != nil {
		bp.percent, bp.known = *percent, true
	}
	a.binaries[machine+"/"+agentType] = bp
}

// runtimeProgress is what a setup's runtime download says, as Lody words it.
func runtimeProgress(bp binaryProgress) string {
	switch bp.status {
	case "downloading":
		if bp.known {
			return locales.SetupDownloadingPercent(int(math.Round(bp.percent)))
		}
	case "verifying":
		return locales.SetupVerifyingRuntime()
	case "extracting":
		return locales.SetupExtractingRuntime()
	case "publishing":
		return locales.SetupInstallingRuntime()
	case "installed":
		return locales.SetupRuntimeReady()
	case "error":
		return firstNonEmpty(bp.err, locales.SetupRuntimeInstallFailed())
	}
	return locales.SetupPreparingRuntime()
}

// setupPercent is how much of a setup's runtime has downloaded, -1 when
// it can't be told.
func (a *App) setupPercent(st catalog.ProviderSetup) float32 {
	bp, ok := a.binaries[st.MachineID+"/"+str(st.Config["agentType"])]
	if !ok || st.Status != "preparing-runtime" || bp.status != "downloading" || !bp.known {
		return -1
	}
	return float32(min(100, max(0, math.Round(bp.percent))))
}

// supportsSetup is whether a built-in agent type can be set up in the
// background.
func supportsSetup(agentType string) bool {
	return slices.Contains(managedTypes, agentType) || agentType == "bub" || agentType == "dimcode"
}

// backgroundSetup is whether saving the form hands the machine a setup
// task rather than a config: a new built-in provider on its default
// runtime, for a machine that takes the task.
func (a *App) backgroundSetup(f *providerForm) bool {
	if f.editing != nil || f.kind.cliType != "builtin" || !supportsSetup(f.agentType()) || a.machineCap(f.machine, "providerSetup") < 1 {
		return false
	}
	row, err := f.row()
	if err != nil {
		return false
	}
	over, _ := row["runtimeOverrides"].(map[string]any)
	return len(over) == 0
}

// createProviderSetup writes a new setup task for a config.
func (a *App) createProviderSetup(machine, id string, config map[string]any, done func(error)) {
	now := float64(time.Now().UnixMilli())
	task := map[string]any{"v": 1, "id": id, "machineId": machine, "config": config, "status": "queued", "attempt": 1, "createdAt": now, "updatedAt": now}
	agentType := str(config["agentType"])
	a.streamWrite(machine, func(fl *flock.Flock) error {
		// Adding a built-in takes it back off the opted-out ones.
		if _, ok, _ := fl.Get([]any{"builtinAgentOptOut", agentType}); ok {
			if err := fl.Delete([]any{"builtinAgentOptOut", agentType}); err != nil {
				return err
			}
		}
		return fl.Put([]any{"providerSetup", id}, task)
	}, done)
}

// retryProviderSetup queues a failed setup again, its attempt counted.
func (a *App) retryProviderSetup(st catalog.ProviderSetup) {
	task := map[string]any{"v": 1, "id": st.ID, "machineId": st.MachineID, "config": st.Config, "status": "queued",
		"attempt": st.Attempt + 1, "createdAt": st.CreatedAt, "updatedAt": float64(time.Now().UnixMilli())}
	a.streamWrite(st.MachineID, func(fl *flock.Flock) error {
		return fl.Put([]any{"providerSetup", st.ID}, task)
	}, func(err error) {
		if err != nil {
			a.toast("error", locales.SetupRetryFailed())
		}
	})
}

// cancelProviderSetup cancels a setup: the cancellation is the durable
// mark the machine reconciles from, then the task and any config it had
// published go.
func (a *App) cancelProviderSetup(st catalog.ProviderSetup) {
	now := float64(time.Now().UnixMilli())
	agentType := str(st.Config["agentType"])
	published, others := false, false
	for _, ag := range a.catalog.Agents[st.MachineID] {
		published = published || ag.ID == st.ID
		others = others || ag.ID != st.ID && ag.AgentType == agentType
	}
	a.streamWrite(st.MachineID, func(fl *flock.Flock) error {
		if err := fl.Put([]any{"providerSetupCancellation", st.ID}, map[string]any{"v": 1, "id": st.ID, "machineId": st.MachineID, "cancelledAt": now}); err != nil {
			return err
		}
		if published && !others && slices.Contains(managedTypes, agentType) {
			// Cancelling a published built-in is removing it: remembered, or
			// the machine adds it back on its next start.
			if err := fl.Put([]any{"builtinAgentOptOut", agentType}, map[string]any{"v": 1, "removedAt": now}); err != nil {
				return err
			}
		}
		fl.Delete([]any{"providerSetup", st.ID})
		fl.Delete([]any{"agentConfig", st.ID})
		return nil
	}, func(err error) {
		if err != nil {
			a.toast("error", locales.SetupDeleteFailed())
		}
	})
}

// setupStatus is what a setup's row says of it.
func (a *App) setupStatus(st catalog.ProviderSetup) string {
	agentType := str(st.Config["agentType"])
	switch st.Status {
	case "queued":
		if _, known := a.catalog.MachineCaps[st.MachineID]; known && a.machineCap(st.MachineID, "providerSetup") < 1 {
			return locales.SetupUnsupportedTarget()
		}
		if a.presence.Live && !a.presence.IsOnline(st.MachineID) {
			return locales.SetupMachineOffline()
		}
		return locales.SetupQueued()
	case "preparing-runtime":
		if bp, ok := a.binaries[st.MachineID+"/"+agentType]; ok {
			return runtimeProgress(bp)
		}
		return locales.SetupPreparingRuntime()
	case "verifying":
		return locales.SetupVerifying()
	case "awaiting-auth":
		return locales.SetupAwaitingAuth()
	case "failed":
		switch st.FailureCode {
		case "runtime-unavailable":
			if agentType == "bub" {
				return locales.SetupBubInstallRequired()
			}
			return locales.SetupRuntimeUnavailable()
		case "runtime-install-failed":
			return locales.SetupRuntimeInstallFailed()
		}
		return locales.SetupVerificationFailed()
	}
	return locales.SetupQueued()
}

// setupReadiness is how near a setup is to usable: cold when it can't go
// on, arriving while it works, ready once it waits only on a sign-in.
func (a *App) setupReadiness(st catalog.ProviderSetup) string {
	if st.Status == "failed" {
		return "cold"
	}
	if st.Status == "queued" {
		_, known := a.catalog.MachineCaps[st.MachineID]
		offline := a.presence.Live && !a.presence.IsOnline(st.MachineID)
		if offline || known && a.machineCap(st.MachineID, "providerSetup") < 1 {
			return "cold"
		}
	}
	if setupActive(st) {
		return "arriving"
	}
	return "ready"
}

func setupActive(st catalog.ProviderSetup) bool {
	return st.Status == "queued" || st.Status == "preparing-runtime" || st.Status == "verifying"
}

// providerSetupRow is a setup's row, laid out as a provider's so the
// columns line up: the mark, the name over its agent, the status and edit
// slots, the progress pill or Retry, and Delete; the status sentence under
// the name.
func (a *App) providerSetupRow(c *ui.Context, p *palette, st catalog.ProviderSetup) {
	name := firstNonEmpty(str(st.Config["name"]), str(st.Config["agentType"]))
	agentType, cliType := str(st.Config["agentType"]), str(st.Config["cliType"])
	status := a.setupStatus(st)
	ui.Column(c.Key("setup/" + st.ID)).FillWidth().MinWidth(0).Children(func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Padding(12, 0, 12, 12).Children(func() {
			percent := a.setupPercent(st)
			agentReadinessMark(c, p, cliType, agentType, st.Config, a.setupReadiness(st), percent)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, name).FontSize(sizeFoot).TextColor(p.Text).SingleLine()
				ui.Text(c, agentKindLabel(cliType, agentType)).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
			})
			ui.Box(c).Width(80).Shrink(0)
			ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Padding(0, 12, 0, 0).Children(func() {
				ui.Box(c).Width(48).Shrink(0)
				ui.Row(c).Width(80).Shrink(0).Justify(ui.End).Children(func() {
					switch {
					case setupActive(st):
						label := locales.SetupWorking()
						switch {
						case percent >= 0:
							label = fmt.Sprintf("%d%%", int(percent))
						case st.Status == "queued":
							label = locales.SetupWaiting()
						}
						pill := ui.Row(c.Key("progress")).FillWidth().Height(28).Radius(8).Center().Background(p.Text.Alpha(0.06)).Clip().Label(status)
						pill.Children(func() {
							if percent >= 0 {
								// Work in flight: a film of the accent, no edge, the
								// width easing over 320ms.
								w := pill.AnimateWith("fill", percent, durSlow, easeStandard)
								ui.Box(c.Key("fill")).Absolute().Left(0).Top(0).Bottom(0).WidthPercent(w).Background(p.Primary.Alpha(0.2))
							}
							ui.Text(c, label).FontSize(sizeFoot).FontWeight(500).TextColor(p.Secondary).FontFeatures("tnum")
						})
					case st.Status == "failed":
						retry := ui.ButtonBase(c.Key("retry")).FillWidth().Height(28).Radius(8).Gap(6).Center().Background(p.Text.Alpha(0.06)).
							Cursor(ui.CursorPointer).Label(locales.SetupRetry()).Transition(colorFade).OnClick(func() { a.retryProviderSetup(st) })
						pressable(retry, 0.97)
						retry.Children(func() {
							ui.Icon(c, iconRefresh).Size(14, 14).TextColor(p.Text)
							ui.Text(c, locales.SetupRetry()).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text)
						})
					}
				})
				iconButton(c.Key("delete"), p, iconTrash, locales.SetupDelete(), p.Danger.Alpha(0.12)).OnClick(func() { a.cancelProviderSetup(st) })
			})
		})
		// The sentence is about this agent: it starts where its name does.
		line := ui.Column(c).FillWidth().Padding(0, 12, 12, 52)
		line.Children(func() {
			if agentType == "dimcode" && st.Status == "failed" && st.FailureCode == "runtime-unavailable" {
				ui.Text(c, locales.DimcodeInstallHint("npm install -g dimcode")).FontSize(sizeFoot).LineHeight(16.0 / 12).TextColor(p.Secondary).Selectable()
				return
			}
			ink := p.Secondary
			if st.Status == "failed" {
				ink = p.Danger
			}
			ui.Text(c, status).FontSize(sizeFoot).LineHeight(16.0 / 12).TextColor(ink)
		})
	})
}

// agentKindLabel is the second line of a provider's name: what agent it is.
func agentKindLabel(cliType, agentType string) string {
	for _, k := range allKinds {
		if k.cliType == cliType && k.agentType == agentType {
			return k.label()
		}
	}
	return agentType
}

// agentReadinessMark is Lody's agent mark on a tile, saying how near the
// agent is to usable: dim when cold, a ring around it while arriving — a
// 22% arc orbiting every 1.4s, or filling to percent over 400ms on CSS's
// ease-out when the work can be measured — and full contrast once ready.
func agentReadinessMark(c *ui.Context, p *palette, cliType, agentType string, row map[string]any, readiness string, percent float32) {
	tile := ui.Box(c.Key("mark")).Size(40, 40).Radius(8).Shrink(0).Center().Background(p.Text.Alpha(0.04))
	var orbit, fill float32
	arriving := readiness == "arriving"
	if arriving {
		if percent >= 0 {
			fill = tile.AnimateWith("fill", min(100, max(0, percent))/100, 400*time.Millisecond, cssEaseOut)
		} else {
			orbit = tile.Keyframes("orbit", []ui.Keyframe{{Value: 0}, {At: 1400 * time.Millisecond, Value: 1}}, ui.KeyframeOptions{Repeat: ui.RepeatForever})
		}
	}
	accent := p.Primary
	track := p.Border.Alpha(0.7)
	tile.Draw(func(pt *ui.Painter, r ui.Rect) {
		if !arriving {
			return
		}
		// The ring of a 100-unit box: radius 45, stroke 7, from twelve o'clock.
		u := r.W / 100
		cx, cy, rad, w := r.X+r.W/2, r.Y+r.H/2, 45*u, 7*u
		arc := func(from, sweep float64) [][2]float32 {
			n := max(4, int(64*sweep))
			pts := make([][2]float32, 0, n+1)
			for i := 0; i <= n; i++ {
				t := 2 * math.Pi * (from + sweep*float64(i)/float64(n))
				pts = append(pts, [2]float32{cx + rad*float32(math.Cos(t)), cy + rad*float32(math.Sin(t))})
			}
			return pts
		}
		pt.StrokePolyline(arc(0, 1), w, track)
		if percent >= 0 {
			if fill > 0 {
				pt.StrokePolyline(arc(-0.25, float64(fill)), w, accent)
			}
			return
		}
		pt.StrokePolyline(arc(-0.25+float64(orbit), 0.22), w, accent)
	})
	ink, opacity := p.Text, float32(1)
	switch readiness {
	case "arriving":
		ink, opacity = p.Secondary, 0.7
	case "cold":
		ink, opacity = p.Secondary, 0.4
	}
	// The mark's opacity, not its ink, eases between the states over 500ms
	// on CSS's ease, as Lody's.
	tile.Children(func() {
		icon := ui.Icon(c.Key("agent"), agentMark(cliType, agentType, row)).Size(18, 18).TextColor(ink)
		icon.Opacity(icon.AnimateWith("opacity", opacity, 500*time.Millisecond, cssEase))
	})
}

// queuedBuiltin is whether the form adds a built-in only its machine can
// install, as bub and dimcode: its dialog waits on the setup.
func queuedBuiltin(f *providerForm) bool {
	t := f.agentType()
	return f.editing == nil && f.kind.cliType == "builtin" && (t == "bub" || t == "dimcode")
}

// waitSetup shows, in place of the form, the setup its dialog waits on,
// and reports whether it did. Once the machine publishes it, the dialog
// edits the provider; cancelled, it is a draft again, under a new id, as
// the cancellation stays with the old one.
func (a *App) waitSetup(c *ui.Context, p *palette, f *providerForm) bool {
	if f.setupWait == "" {
		return false
	}
	for _, ag := range a.catalog.Agents[f.machine] {
		if ag.ID == f.setupWait {
			agent := ag
			f.setupWait = ""
			a.openProvider(f.machine, &agent)
			return false
		}
	}
	var st *catalog.ProviderSetup
	for i, s := range a.catalog.ProviderSetups[f.machine] {
		if s.ID == f.setupWait {
			st = &a.catalog.ProviderSetups[f.machine][i]
		}
	}
	if st == nil {
		if f.setupSeen {
			f.setupWait, f.setupSeen, f.id, f.persisted = "", false, uuid.NewString(), false
			return false
		}
		ui.Column(c.Key("setup-wait")).Grow(1).Center().Children(func() { spinner(c).Size(18, 18) })
		ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).Shrink(0).Children(func() {
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { f.open = false })
			primaryButton(c, p, locales.ProviderCreate(), false)
		})
		return true
	}
	f.setupSeen = true
	ui.Column(c.Key("setup-wait")).Grow(1).MinHeight(0).Padding(8, 20, 8, 20).Children(func() {
		card(c, p, func() { a.providerSetupRow(c, p, *st) })
	})
	// The footer stays, its action held while the machine works.
	ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).AlignItems(ui.Center).Shrink(0).Children(func() {
		ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { f.open = false })
		primaryButton(c, p, locales.ProviderCreate(), false)
	})
	return true
}
