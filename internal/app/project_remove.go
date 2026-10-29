package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/locales"
)

// Removing a project, as Lody's dialog does: its chats go to Archive, and
// the worktrees Lody made for them may go too — the machine checks each
// for changes first (local-project/removal-preflight) and only clean ones
// are deleted. The project's own folder is never touched.

type worktreeItem struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
	Path      string `json:"path"`
	Message   string `json:"message"`
}

type removalPreflight struct {
	Clean  []worktreeItem `json:"clean"`
	Dirty  []worktreeItem `json:"dirty"`
	Failed []worktreeItem `json:"failed"`
}

type projectRemoval struct {
	open      bool
	project   catalog.Project
	cleanup   bool
	loading   bool
	preflight *removalPreflight
	err       string
}

// askRemoveProject opens the removal dialog, checking the project's
// worktrees where the machine can.
func (a *App) askRemoveProject(pr catalog.Project) {
	a.removal = projectRemoval{open: true, project: pr, cleanup: true}
	st := &a.removal
	if a.conn == nil || !a.presence.IsOnline(pr.MachineID) || a.machineCap(pr.MachineID, "localProjectRemoval") < 1 {
		st.cleanup = false
		return
	}
	st.loading = true
	_, local, _ := strings.Cut(pr.ID, ":local:")
	conn, req := a.conn, map[string]any{"type": "local-project/removal-preflight", "localProjectId": local}
	if a.account != nil {
		req["requestedByUserId"] = a.account.User.ID
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
		defer cancel()
		raw, err := conn.ProjectControl(ctx, pr.MachineID, req, 60*time.Second)
		var res removalPreflight
		if err == nil {
			err = json.Unmarshal(raw, &res)
		}
		a.update(func() {
			if a.removal.project.ID != pr.ID {
				return
			}
			st.loading = false
			if err != nil {
				st.err = err.Error()
				return
			}
			st.preflight = &res
		})
	}()
}

// removeProject asks the machine to archive the project's chats and drop
// it, its clean worktrees too when cleanup is asked for.
func (a *App) removeProject(pr catalog.Project, cleanup bool) {
	_, local, _ := strings.Cut(pr.ID, ":local:")
	row := map[string]any{"v": 1, "requestedAt": nowMs(), "projectName": pr.Name, "originalRootPath": pr.RootPath}
	if a.account != nil {
		row["requestedBy"] = a.account.User.ID
	}
	if cleanup {
		row["cleanupWorktrees"] = true
	}
	a.streamWrite(pr.MachineID, func(f *flock.Flock) error {
		return f.Put([]any{"cmd", "deleteLocalProject", local}, row)
	}, nil)
}

func (a *App) removalDialog(c *ui.Context, p *palette) {
	st := &a.removal
	if !st.open {
		return
	}
	pr := st.project
	chats, running := 0, 0
	for _, s := range a.catalog.Sessions {
		if s.ProjectID == pr.ID && !s.Archived {
			chats++
			if s.Working() {
				running++
			}
		}
	}
	online := a.presence.IsOnline(pr.MachineID)
	sheet(c, p, &st.open, 520, func() {
		sheetHeader(c, p, locales.ProjectRemoveTitle(pr.Name), locales.ProjectRemoveLead(), func() { st.open = false })
		ui.Column(c).FillWidth().Padding(4, 20, 8, 20).Gap(10).Children(func() {
			line := func(text string, ink ui.Color) {
				ui.Text(c, text).FontSize(sizeFoot).LineHeight(1.45).TextColor(ink)
			}
			if chats > 0 {
				line(locales.ProjectRemoveArchive(chats), p.Secondary)
			} else {
				line(locales.ProjectRemoveArchiveEmpty(), p.Secondary)
			}
			if running > 0 {
				line(locales.ProjectRemoveRunning(running), p.Warning)
			}
			if !online {
				line(locales.ProjectRemoveOffline(), p.Secondary)
			}
			if a.machineCap(pr.MachineID, "localProjectRemoval") >= 1 {
				ui.Column(c).FillWidth().Gap(6).Padding(10, 12).Radius(8).Background(p.Text.Alpha(0.035)).Children(func() {
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Column(c).Grow(1).Gap(2).Children(func() {
							ui.Text(c, locales.ProjectCleanupWorktrees()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
							ui.Text(c, locales.ProjectCleanupHelper()).FontSize(sizeCaption).TextColor(p.Secondary)
						})
						sw := lodySwitch(c, &st.cleanup, !online).Label(locales.ProjectCleanupWorktrees())
						if !online {
							sw.Disabled(true)
						}
					})
					if !st.cleanup {
						return
					}
					switch {
					case !online:
						line(locales.ProjectCleanupUnavailable(), p.Secondary)
					case st.loading:
						ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
							spinner(c).Size(12, 12)
							line(locales.ProjectCheckingWorktrees(), p.Secondary)
						})
					case st.err != "":
						line(st.err, p.Danger)
					case st.preflight != nil:
						pf := st.preflight
						if n := len(pf.Clean); n > 0 {
							line(locales.ProjectCleanWorktrees(n), p.Secondary)
						}
						if n := len(pf.Dirty); n > 0 {
							line(locales.ProjectDirtyWorktrees(n), p.Warning)
						}
						if n := len(pf.Failed); n > 0 {
							line(locales.ProjectInspectFailed(n), p.Warning)
						}
					}
				})
			}
			line(locales.ProjectFolderSafe(), p.Tertiary)
		})
		ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).Children(func() {
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { st.open = false })
			cleanup := st.cleanup && online && a.machineCap(pr.MachineID, "localProjectRemoval") >= 1
			b := ghostButton(c, p, locales.ProjectRemove()).Border(1, p.Danger.Alpha(0.5))
			b.OnClick(func() {
				st.open = false
				a.projectWin.open = false
				a.removeProject(pr, cleanup)
			})
		})
	})
}

// newFolder makes a folder in the folder the browser shows, then shows it.
func (a *App) newFolder() {
	f := &a.addProject
	name := strings.TrimSpace(f.folderName)
	if name == "" || f.path == "" || a.conn == nil {
		return
	}
	conn, machine, ws, parent := a.conn, f.machine, a.workspaceID, f.path
	f.busy, f.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		raw, err := conn.ProjectControl(ctx, machine, map[string]any{"type": "local-project/create-directory", "workspaceId": ws, "parentPath": parent, "name": name}, 20*time.Second)
		var res struct {
			Path string `json:"path"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &res)
		}
		a.update(func() {
			f.busy = false
			if err != nil {
				f.err = err.Error()
				return
			}
			f.naming, f.folderName = false, ""
			a.browseFolder(res.Path)
		})
	}()
}
