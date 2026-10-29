package app

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// Reporting a bug, as Lody's dialog does: with a machine picked, the
// machine bundles its recent CLI logs with the description
// (machine/bug-report, its request token minted by the backend);
// without one, the description goes to the backend alone. Either way the
// report's id comes back to pass on to the Lody team.

// clientVersion is this client's version: the packaged app's, from
// mygo.json's version, or dev when run from source.
func clientVersion() string {
	if v := mygo.App.Version(); v != "" {
		return v
	}
	return "dev"
}

type bugReport struct {
	open        bool
	machine     string // "" for none
	description string
	busy        bool
	err         string
	id          string // the report filed
}

func (a *App) openBugReport() {
	machine := ""
	for _, id := range a.catalog.MachineIDs {
		if a.presence.IsOnline(id) {
			if machine == "" || id == a.newChat.machine {
				machine = id
			}
		}
	}
	a.bug = bugReport{open: true, machine: machine}
}

// buildInfo is what Lody appends about the reporting client.
func buildInfo(description string) string {
	return description + "\n\n---\nReporting client build\nVersion: " + clientVersion() + "\nClient: Lody Native (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
}

func (a *App) submitBugReport() {
	b := &a.bug
	text := strings.TrimSpace(b.description)
	if text == "" || b.busy || a.account == nil || a.workspaceID == "" {
		return
	}
	b.busy, b.err = true, ""
	api, token, ws, user, conn, machine := a.api, a.tokenNow(), a.workspaceID, a.account.User.ID, a.conn, b.machine
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 130*time.Second)
		defer cancel()
		report := buildInfo(text)
		var id string
		err := func() error {
			if machine == "" {
				var res struct {
					BugReportID string `json:"bugReportId"`
				}
				if err := api.Call(ctx, token, "/api/bug-reports/create", map[string]any{"workspaceId": ws, "description": report}, &res); err != nil {
					return err
				}
				id = res.BugReportID
				return nil
			}
			var minted struct {
				RequestToken string `json:"requestToken"`
			}
			if err := api.Call(ctx, token, "/api/bug-reports/request-token", map[string]any{"workspaceId": ws, "machineId": machine}, &minted); err != nil {
				return err
			}
			if conn == nil {
				return &opError{locales.BugReportNotReady()}
			}
			raw, err := conn.MachineRPC(ctx, machine, "machine/bug-report", map[string]any{
				"description": report, "reporterUserId": user, "requestToken": minted.RequestToken,
			}, 120*time.Second)
			if err != nil {
				return err
			}
			var res struct {
				Success     bool   `json:"success"`
				BugReportID string `json:"bugReportId"`
				Error       string `json:"error"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return err
			}
			if !res.Success || res.BugReportID == "" {
				return &opError{firstNonEmpty(res.Error, locales.BugReportTimeout())}
			}
			id = res.BugReportID
			return nil
		}()
		a.update(func() {
			b.busy = false
			if err != nil {
				b.err = err.Error()
				return
			}
			b.id = id
		})
	}()
}

// opError is an error in the app's words.
type opError struct{ msg string }

func (e *opError) Error() string { return e.msg }

func (a *App) bugReportDialog(c *ui.Context, p *palette) {
	b := &a.bug
	if !b.open {
		return
	}
	if b.machine != "" && b.id == "" && !b.busy && !a.presence.IsOnline(b.machine) {
		b.machine = "" // its logs can't come now
	}
	sheet(c, p, &b.open, 520, func() {
		if b.id != "" {
			sheetHeader(c, p, locales.BugReportSuccessTitle(), "", func() { b.open = false })
			ui.Column(c).FillWidth().Padding(4, 20, 8, 20).Gap(10).Children(func() {
				lead := locales.BugReportSuccessNoLogs()
				if b.machine != "" {
					lead = locales.BugReportSuccess()
				}
				ui.Text(c, lead).FontSize(sizeSub).LineHeight(1.45).TextColor(p.Secondary)
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Padding(8, 12).Radius(8).Background(p.CodeBg).Children(func() {
					ui.Text(c, b.id).Font(fontMono).FontSize(sizeSub).TextColor(p.Text).Grow(1).Selectable()
					id := b.id
					iconButton(c, p, iconCopy, locales.Copy(), p.Hover).OnClick(func() { mygo.Clipboard.WriteText(id) })
				})
			})
			ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Justify(ui.End).Children(func() {
				primaryButton(c, p, locales.Close(), true).OnClick(func() { b.open = false })
			})
			return
		}
		sheetHeader(c, p, locales.BugReportTitle(), locales.BugReportLead(), func() { b.open = false })
		ui.Column(c).FillWidth().Padding(4, 20, 8, 20).Gap(12).Children(func() {
			field(c, p, locales.BugReportMachine(), "", func() {
				options := []option{{"", locales.BugReportNoMachine()}}
				for _, id := range a.catalog.MachineIDs {
					if a.presence.IsOnline(id) {
						options = append(options, option{id, a.machineName(id)})
					}
				}
				label := locales.BugReportNoMachine()
				if b.machine != "" {
					label = a.machineName(b.machine)
				}
				a.toolbarPicker(c, p, "bug-machine", iconMonitor, label, options, b.machine, func(id string) { b.machine = id })
				hint := ""
				switch {
				case len(options) == 1:
					hint = locales.BugReportNoMachines()
				case b.machine == "":
					hint = locales.BugReportNoMachineHint()
				}
				if hint != "" {
					ui.Text(c, hint).FontSize(sizeCaption).TextColor(p.Secondary)
				}
			})
			field(c, p, locales.BugReportDescription(), locales.BugReportBuildInfo(), func() {
				area(c, p, &b.description, locales.BugReportDescriptionPlaceholder(), locales.BugReportDescription(), 5).AutoFocus()
			})
			if b.err != "" {
				ui.Text(c, b.err).FontSize(sizeFoot).TextColor(p.Danger).Selectable()
			}
		})
		ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { b.open = false })
			label := locales.BugReportSubmitNoLogs()
			if b.machine != "" {
				label = locales.BugReportSubmit()
			}
			if b.busy {
				spinner(c).Size(14, 14)
				label = locales.BugReportSubmittingNoLogs()
				if b.machine != "" {
					label = locales.BugReportSubmitting()
				}
			}
			primaryButton(c, p, label, strings.TrimSpace(b.description) != "" && !b.busy).OnClick(a.submitBugReport)
		})
	})
}
