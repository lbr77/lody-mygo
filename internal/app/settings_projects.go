package app

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/locales"
)

// Projects, as Lody's page keeps them: each of the user's machines' local
// projects, rows ['localProject', id] of the machine's stream. A folder is
// added by browsing the machine and registering it; a project is removed
// by asking the machine, with a ['cmd', 'deleteLocalProject', id] row.

type folderEntry struct {
	Name         string `json:"name"`
	AbsolutePath string `json:"absolutePath"`
	Hidden       bool   `json:"hidden"`
	Registered   string `json:"registeredProjectId"`
	Error        string `json:"error"`
	Hints        *struct {
		Git bool `json:"git"`
	} `json:"hints"`
}

type folderListing struct {
	Path       string        `json:"path"`
	ParentPath *string       `json:"parentPath"`
	Entries    []folderEntry `json:"entries"`
}

type addProjectForm struct {
	open    bool
	machine string
	path    string // the folder shown
	listing *folderListing
	loading bool
	busy    bool
	err     string
	// A folder being named, to make in the one shown.
	naming     bool
	folderName string
}

// projectsPage lists each machine's folders, as Lody's: the folder, the
// end of its path, and how many chats it has.
func (a *App) projectsPage(c *ui.Context, p *palette) {
	pageHeader(c, p, locales.SettingsProjects(), "", func() {
		outlineButton(c, p, iconPlus, locales.ProjectAdd()).OnClick(func() { a.openAddProject("") })
	})
	a.settingsErrorLine(c, p)
	chats := map[string]int{}
	for _, s := range a.catalog.Sessions {
		chats[s.ProjectID]++
	}
	machines := append([]string(nil), a.catalog.MachineIDs...)
	sort.SliceStable(machines, func(i, j int) bool {
		return strings.ToLower(a.machineName(machines[i])) < strings.ToLower(a.machineName(machines[j]))
	})
	visible := a.visibleMachines()
	for _, machine := range machines {
		if m, known := visible[machine]; known && a.account != nil && m.OwnerUserID != a.account.User.ID {
			continue // only the user's own machines' projects, as Lody lists
		}
		projects := a.machineProjects(machine)
		ui.Column(c.Key(machine)).FillWidth().Gap(2).Margin(8, 0, 4, 0).Children(func() {
			ui.Text(c, a.machineName(machine)).FontSize(sizeBody).FontWeight(500).TextColor(p.Strong)
			status := map[bool]string{true: locales.Online(), false: locales.Offline()}[a.presence.IsOnline(machine)]
			if machine == a.localMachine() {
				status = locales.MachineThis()
			}
			ui.Text(c, status).FontSize(sizeFoot).TextColor(p.Secondary)
		})
		if len(projects) == 0 {
			emptyCard(c.Key(machine+"/empty"), p, locales.SettingsNoProjects())
			continue
		}
		ui.Column(c.Key(machine+"/list")).FillWidth().Radius(10).Border(1, p.Border).Clip().Dividers(1, p.Border).Children(func() {
			for _, pr := range projects {
				a.projectSettingsRow(c.Key(pr.ID), p, pr, chats[pr.ID])
			}
		})
	}
	a.githubProjectSections(c, p)
	a.addProjectDialog(c, p)
	a.projectWindow(c, p)
	a.githubProjectWindow(c, p)
}

func (a *App) projectSettingsRow(c *ui.Context, p *palette, pr catalog.Project, chats int) {
	project := pr
	removing := a.catalog.Removing[pr.ID]
	ui.Row(c).FillWidth().MinHeight(54).Padding(8, 8, 8, 16).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconFolder).Size(16, 16).TextColor(p.Secondary)
				ui.Text(c, project.Name).FontSize(sizeBody).TextColor(p.Text).SingleLine()
			})
			ui.Text(c, pathTail(project.RootPath, 2)).Font(fontMono).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine().Tooltip(project.RootPath)
		})
		switch {
		case removing && a.presence.IsOnline(project.MachineID):
			ui.Text(c, locales.ProjectRemoving()).FontSize(sizeFoot).TextColor(p.Tertiary)
		case removing:
			ui.Text(c, locales.ProjectWaitingDevice()).FontSize(sizeFoot).TextColor(p.Tertiary)
		case chats > 0:
			ui.Text(c, locales.ProjectChats(chats)).FontSize(sizeFoot).TextColor(p.Tertiary)
		}
		if removing {
			return
		}
		iconButton(c, p, iconChevronRight, locales.ProjectOpen(), p.Hover).OnClick(func() { a.openProject(project.ID) })
		iconButton(c, p, iconMore, locales.MoreActions(), p.Hover).Menu(func(m *ui.Menu) {
			if m.Item(locales.ProjectNewChat()).Chosen() {
				a.settingsOpen = false
				a.newChat.machine, a.newChat.project = project.MachineID, project.ID
				a.open("")
			}
			if m.Item(locales.ProjectCopyPath()).Chosen() {
				mygo.Clipboard.WriteText(project.RootPath)
			}
			m.Separator()
			if m.Item(locales.ProjectRemove()).Chosen() {
				a.askRemoveProject(project)
			}
		})
	})
}

func (a *App) openAddProject(machine string) {
	if machine == "" {
		machine = a.localMachine()
	}
	valid := false
	for _, id := range a.catalog.MachineIDs {
		valid = valid || id == machine
	}
	if !valid && len(a.catalog.MachineIDs) > 0 {
		machine = a.catalog.MachineIDs[0]
	}
	a.addProject = addProjectForm{open: true, machine: machine}
	a.browseFolder("")
}

// browseFolder lists a folder of the machine; "" is the user's home.
func (a *App) browseFolder(path string) {
	f := &a.addProject
	conn, machine, ws := a.conn, f.machine, a.workspaceID
	if conn == nil || machine == "" {
		return
	}
	f.loading, f.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var listing folderListing
		err := func() error {
			if path == "" {
				raw, err := conn.ProjectControl(ctx, machine, map[string]any{"type": "local-project/list-roots"}, 20*time.Second)
				if err != nil {
					return err
				}
				var roots struct {
					HomeDir string `json:"homeDir"`
				}
				if err := json.Unmarshal(raw, &roots); err != nil {
					return err
				}
				path = roots.HomeDir
			}
			raw, err := conn.ProjectControl(ctx, machine, map[string]any{"type": "local-project/browse-dir", "workspaceId": ws, "absolutePath": path}, 20*time.Second)
			if err != nil {
				return err
			}
			return json.Unmarshal(raw, &listing)
		}()
		a.update(func() {
			if f.machine != machine {
				return
			}
			f.loading = false
			if err != nil {
				f.err = err.Error()
				return
			}
			f.listing, f.path = &listing, listing.Path
		})
	}()
}

// registerFolder adds the folder shown as a project of the machine.
func (a *App) registerFolder() {
	f := &a.addProject
	conn, machine, ws, root := a.conn, f.machine, a.workspaceID, f.path
	f.busy, f.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		var prep struct {
			LocalProjectID    string `json:"localProjectId"`
			Name              string `json:"name"`
			RootPath          string `json:"rootPath"`
			AlreadyRegistered bool   `json:"alreadyRegistered"`
		}
		raw, err := conn.ProjectControl(ctx, machine, map[string]any{"type": "local-project/prepare-add", "workspaceId": ws, "rootPath": root}, 30*time.Second)
		if err == nil {
			err = json.Unmarshal(raw, &prep)
		}
		a.update(func() {
			f.busy = false
			if err != nil {
				f.err = err.Error()
				return
			}
			f.open = false
			if prep.AlreadyRegistered || prep.LocalProjectID == "" {
				return
			}
			now := nowMs()
			row := map[string]any{"id": prep.LocalProjectID, "name": prep.Name, "rootPath": prep.RootPath, "createdAtMs": now, "lastOpenedAtMs": now}
			a.streamWrite(machine, func(fl *flock.Flock) error {
				key := []any{"localProject", prep.LocalProjectID}
				if _, ok, _ := fl.Get(key); ok {
					return nil
				}
				return fl.Put(key, row)
			}, nil)
		})
	}()
}

func (a *App) addProjectDialog(c *ui.Context, p *palette) {
	f := &a.addProject
	if !f.open {
		return
	}
	_, h := c.Size()
	sheet(c, p, &f.open, 620, func() {
		sheetHeader(c, p, locales.ProjectAdd(), locales.ProjectAddLead(), func() { f.open = false })
		ui.Column(c).FillWidth().Padding(4, 20).Gap(10).Children(func() {
			if len(a.catalog.MachineIDs) > 1 {
				var options []option
				for _, id := range a.catalog.MachineIDs {
					options = append(options, option{id, a.machineName(id)})
				}
				a.toolbarPicker(c, p, "project-machine", iconMonitor, a.machineName(f.machine), options, f.machine, func(id string) {
					f.machine, f.listing, f.path = id, nil, ""
					a.browseFolder("")
				})
			}
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				up := iconButton(c, p, iconChevronLeft, locales.ProjectParent(), p.Hover)
				if f.listing != nil && f.listing.ParentPath != nil {
					parent := *f.listing.ParentPath
					up.OnClick(func() { a.browseFolder(parent) })
				} else {
					up.Disabled(true).Opacity(0.45)
				}
				ui.Text(c, f.path).Font(fontMono).FontSize(sizeFoot).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
				if f.loading {
					spinner(c).Size(13, 13)
				}
				if f.path != "" && !f.naming {
					outlineButton(c, p, iconPlus, locales.ProjectNewFolder()).OnClick(func() { f.naming = true })
				}
			})
			if f.naming {
				ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					input(c, p, &f.folderName, locales.ProjectNewFolder(), locales.ProjectNewFolder()).Grow(1).AutoFocus()
					ghostButton(c, p, locales.Cancel()).OnClick(func() { f.naming, f.folderName = false, "" })
					primaryButton(c, p, locales.ProviderCreate(), strings.TrimSpace(f.folderName) != "" && !f.busy).OnClick(a.newFolder)
				})
			}
		})
		ui.Scroll(c).FillWidth().Height(min(360, h-300)).Padding(0, 12).Gap(1).Children(func() {
			if f.listing == nil {
				return
			}
			for _, e := range f.listing.Entries {
				if e.Hidden {
					continue
				}
				entry := e
				row := ui.Row(c.Key(e.AbsolutePath)).FillWidth().Height(32).Padding(0, 8).Radius(6).Gap(8).AlignItems(ui.Center).Label(e.Name).Transition(colorFade)
				if e.Error == "" {
					row.Cursor(ui.CursorPointer).OnClick(func() { a.browseFolder(entry.AbsolutePath) })
					if row.Hovered() {
						row.Background(p.Hover)
					}
				} else {
					row.Opacity(0.5)
				}
				row.Children(func() {
					ui.Icon(c, iconFolder).Size(15, 15).TextColor(p.Secondary)
					ui.Text(c, entry.Name).FontSize(sizeSub).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
					if entry.Hints != nil && entry.Hints.Git {
						ui.Text(c, "git").FontSize(sizeCaption).TextColor(p.Tertiary)
					}
					if entry.Registered != "" {
						ui.Text(c, locales.ProjectAdded()).FontSize(sizeCaption).TextColor(p.Tertiary)
					}
				})
			}
		})
		if f.err != "" {
			ui.Text(c, f.err).FontSize(sizeFoot).TextColor(p.Danger).Padding(8, 20, 0, 20).Selectable()
		}
		ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).Children(func() {
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { f.open = false })
			if f.busy {
				spinner(c).Size(14, 14)
			}
			primaryButton(c, p, locales.ProjectAddThis(), f.path != "" && !f.busy && !f.loading).OnClick(a.registerFolder)
		})
	})
}
