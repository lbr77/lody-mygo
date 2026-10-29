package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/locales"
)

// Restarting and updating a machine's Lody CLI, as Lody's machine card
// does: the machine says whether it can (machine/status), the backend
// signs the request for the user, and the machine checks the signature
// as it restarts (machine/restart) or updates (machine/upgrade).

type machineLifecycle struct {
	LaunchMode       string `json:"launchMode"`
	CanRemoteRestart bool   `json:"canRemoteRestart"`
	CanRemoteUpgrade bool   `json:"canRemoteUpgrade"`
	Reason           string `json:"reason"`
}

var semver = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// olderVersion reports whether version a comes before b, by their major,
// minor and patch numbers.
func olderVersion(a, b string) bool {
	ma, mb := semver.FindStringSubmatch(a), semver.FindStringSubmatch(b)
	if ma == nil || mb == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(ma[i])
		y, _ := strconv.Atoi(mb[i])
		if x != y {
			return x < y
		}
	}
	return false
}

// lifecycleButtons restart or update the machine's CLI, when it can.
func (a *App) lifecycleButtons(c *ui.Context, p *palette, id string) {
	if !a.presence.IsOnline(id) || a.conn == nil {
		return
	}
	conn, api, token := a.conn, a.api, a.tokenNow()
	f := a.fetch("lifecycle/"+id, func(ctx context.Context) (any, error) {
		raw, err := conn.MachineRPC(ctx, id, "machine/status", map[string]any{}, 15*time.Second)
		if err != nil {
			return nil, err
		}
		var res struct {
			Lifecycle *machineLifecycle `json:"lifecycle"`
		}
		err = json.Unmarshal(raw, &res)
		return res.Lifecycle, err
	})
	lc, _ := f.value.(*machineLifecycle)
	if lc == nil {
		return
	}
	latest := a.fetch("cli-latest", func(ctx context.Context) (any, error) {
		var out struct {
			LatestVersion string `json:"latestVersion"`
		}
		err := api.Call(ctx, token, "/api/cli/latest-version", nil, &out)
		return out.LatestVersion, err
	})
	newest, _ := latest.value.(string)
	if lc.CanRemoteUpgrade && newest != "" && olderVersion(a.catalog.MachineCLI[id], newest) {
		outlineButton(c, p, iconArrowUp, locales.MachineUpgrade(newest)).OnClick(func() {
			a.confirm(locales.MachineUpgradeTitle(a.machineName(id), newest), locales.MachineRestartBody(), locales.MachineUpgrade(newest), func() {
				a.lifecycle(id, "upgrade", newest)
			})
		})
	}
	if lc.CanRemoteRestart {
		iconButton(c, p, iconRefresh, locales.MachineRestart(), p.Hover).OnClick(func() {
			a.confirm(locales.MachineRestartTitle(a.machineName(id)), locales.MachineRestartBody(), locales.MachineRestart(), func() {
				a.lifecycle(id, "restart", "")
			})
		})
	}
}

// lifecycle restarts or updates a machine's CLI.
func (a *App) lifecycle(machine, action, version string) {
	conn, api, token, ws := a.conn, a.api, a.tokenNow(), a.workspaceID
	a.settingsError, a.settingsNotice = "", ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		requestID := uuid.NewString()
		body := map[string]any{"workspaceId": ws, "machineId": machine, "action": action, "requestId": requestID}
		if version != "" {
			body["targetVersion"] = version
		}
		var grant struct {
			RequestToken    string `json:"requestToken"`
			RequesterUserID string `json:"requesterUserId"`
		}
		err := api.Call(ctx, token, "/api/machine-lifecycle/request-token", body, &grant)
		if err == nil {
			params := map[string]any{"requesterUserId": grant.RequesterUserID, "requestToken": grant.RequestToken, "requestId": requestID}
			if version != "" {
				params["targetVersion"] = version
			}
			var raw json.RawMessage
			if raw, err = conn.MachineRPC(ctx, machine, "machine/"+action, params, 45*time.Second); err == nil {
				var res struct {
					Success  bool   `json:"success"`
					Accepted bool   `json:"accepted"`
					Error    string `json:"error"`
					Message  string `json:"message"`
				}
				if err = json.Unmarshal(raw, &res); err == nil && !(res.Success && res.Accepted) {
					err = fmt.Errorf("%s", firstNonEmpty(res.Message, res.Error, locales.MachineLifecycleRefused()))
				}
			}
		}
		a.update(func() {
			if err != nil {
				a.settingsError = err.Error()
				return
			}
			a.settingsNotice = locales.MachineLifecycleStarted(a.machineName(machine))
			a.refetchKey("lifecycle/" + machine)
		})
	}()
}
