package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/locales"
)

// Memory, as Lody's page manages it: the memories each machine links to
// Lody, rows ['memory', provider, id] of its stream, from what the
// machine's memory provider holds (machine/memory over Machine RPC).

const nowledgeMem = "nowledge-mem"

var memoryID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

type memoryInventory struct {
	Status   string `json:"status"` // ready, not_installed, not_running or error
	Memories []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Role        string `json:"role"`
	} `json:"memories"`
	Error string `json:"error"`
}

type memoryForm struct {
	open        bool
	editing     *catalog.Memory // the memory the form edits, if any
	importing   bool
	name        string
	id          string
	idEdited    bool
	description string
	role        string
	picked      map[string]bool
	busy        bool
	err         string
}

// memoryRPC asks a machine's memory provider.
func (a *App) memoryRPC(ctx context.Context, machine string, params map[string]any) (*memoryInventory, error) {
	raw, err := a.conn.MachineRPC(ctx, machine, "machine/memory", params, 60*time.Second)
	if err != nil {
		return nil, err
	}
	var inv memoryInventory
	if err := json.Unmarshal(raw, &inv); err != nil {
		return nil, err
	}
	return &inv, nil
}

func (a *App) memoryPage(c *ui.Context, p *palette) {
	pageHeader(c, p, locales.SettingsMemory(), locales.MemoryLead(), func() {
		machine := a.memoryMachine
		iconButton(c, p, iconRefresh, locales.Refresh(), p.Hover).OnClick(func() { a.refetchKey("memory/" + machine) })
		outlineButton(c, p, iconPlus, locales.MemoryAdd()).OnClick(func() {
			a.memoryForm = memoryForm{open: true, picked: map[string]bool{}}
		})
	})
	a.settingsErrorLine(c, p)
	machine := a.machineTabs(c, p, &a.memoryMachine)
	if machine == "" {
		return
	}
	inv := a.memoryInventory(machine)
	known := map[string]bool{}
	if v, _ := inv.value.(*memoryInventory); v != nil && v.Status == "ready" {
		for _, m := range v.Memories {
			known[m.ID] = true
		}
	}
	memories := a.catalog.Memories[machine]
	if len(memories) == 0 {
		emptyCard(c, p, locales.MemoryNone())
	} else {
		card(c, p, func() {
			for _, m := range memories {
				mem := m
				ui.Row(c.Key(m.ProviderID+"/"+m.MemoryID)).FillWidth().MinHeight(48).Padding(8, 8, 8, 16).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconBrain).Size(16, 16).TextColor(p.Secondary)
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, mem.Name).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine()
							if inv.value != nil && !known[mem.MemoryID] {
								ui.Text(c, locales.MemoryMissing()).FontSize(sizeCaption).TextColor(p.Warning).Padding(1, 6).Radius(5).Background(p.Warning.Alpha(0.12))
							}
						})
						detail := "Nowledge Mem · " + mem.MemoryID
						if mem.Description != "" {
							detail += " · " + mem.Description
						}
						ui.Text(c, detail).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
					})
					iconButton(c, p, iconPenLine, locales.MemoryEdit(), p.Hover).OnClick(func() {
						a.memoryForm = memoryForm{open: true, editing: &mem, name: mem.Name, id: mem.MemoryID, idEdited: true, description: mem.Description, picked: map[string]bool{}}
					})
					iconButton(c, p, iconTrash, locales.MemoryRemove(), p.Hover).OnClick(func() {
						a.streamWrite(mem.MachineID, func(f *flock.Flock) error {
							return f.Delete([]any{"memory", mem.ProviderID, mem.MemoryID})
						}, nil)
					})
				})
			}
		})
	}
	if v, _ := inv.value.(*memoryInventory); v != nil && v.Status != "ready" {
		ui.Text(c, memoryStatus(v)).FontSize(sizeFoot).TextColor(p.Secondary)
	} else if inv.err != "" {
		ui.Text(c, inv.err).FontSize(sizeFoot).TextColor(p.Danger)
	}
	a.memoryDialog(c, p, machine)
}

func memoryStatus(v *memoryInventory) string {
	switch v.Status {
	case "not_installed":
		return locales.MemoryNotInstalled()
	case "not_running":
		return locales.MemoryNotRunning()
	}
	return firstNonEmpty(v.Error, v.Status)
}

// memoryInventory is what the machine's provider holds, asked once a
// visit and again on Refresh.
func (a *App) memoryInventory(machine string) *fetched {
	return a.fetch("memory/"+machine, func(ctx context.Context) (any, error) {
		if !a.presence.IsOnline(machine) {
			return nil, fmt.Errorf("%s", locales.MentionOffline(a.machineName(machine)))
		}
		return a.memoryRPC(ctx, machine, map[string]any{"action": "list", "providerId": nowledgeMem})
	})
}

// linkMemory writes a memory's association, unless the machine has one.
func (a *App) linkMemory(machine string, m catalog.Memory) {
	a.streamWrite(machine, func(f *flock.Flock) error {
		key := []any{"memory", m.ProviderID, m.MemoryID}
		if _, ok, _ := f.Get(key); ok {
			return nil
		}
		row := map[string]any{"providerId": m.ProviderID, "memoryId": m.MemoryID, "machineId": machine, "name": m.Name}
		if m.Description != "" {
			row["description"] = m.Description
		}
		return f.Put(key, row)
	}, nil)
}

func (a *App) memoryDialog(c *ui.Context, p *palette, machine string) {
	f := &a.memoryForm
	if !f.open {
		return
	}
	w, h := c.Size()
	ui.DialogBase(c, &f.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.22))
		panel.Size(min(900, w-80), min(600, h-80)).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Clip().
			Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(290).Shrink(0).Background(p.Ground).BorderWidth(0, 1, 0, 0).BorderColor(p.Border).Padding(16, 8).Gap(4).Children(func() {
				ui.Text(c, locales.MemoryProviders()).FontSize(sizeCaption).TextColor(p.Tertiary).Padding(0, 10, 4, 10)
				ui.Row(c).FillWidth().Height(34).Padding(0, 10).Radius(8).Gap(10).AlignItems(ui.Center).Background(p.SideSelect).Children(func() {
					ui.Icon(c, iconBrain).Size(15, 15).TextColor(p.Text)
					ui.Text(c, "Nowledge Mem").FontSize(sizeSub).TextColor(p.Text)
				})
			})
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				title, lead := locales.MemoryAdd(), locales.MemoryAddLead()
				if f.editing != nil {
					title, lead = locales.MemoryEdit(), ""
				}
				sheetHeader(c, p, title, lead, func() { f.open = false })
				ui.Scroll(c).Grow(1).MinHeight(0).Padding(8, 20).Gap(14).Children(func() {
					if f.editing == nil {
						picked := map[bool]int{false: 0, true: 1}[f.importing]
						ui.Segmented(c, &picked, locales.MemoryCreate(), locales.MemoryImport()).Label(locales.MemoryAdd())
						f.importing = picked == 1
					}
					if f.importing {
						a.memoryImportList(c, p, machine)
					} else {
						a.memoryCreateForm(c, p)
					}
					if f.err != "" {
						ui.Text(c, f.err).FontSize(sizeFoot).TextColor(p.Danger).Selectable()
					}
				})
				ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).Shrink(0).Children(func() {
					ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { f.open = false })
					if f.busy {
						ui.Spinner(c).Size(14, 14)
					}
					if f.importing {
						n := 0
						for _, on := range f.picked {
							if on {
								n++
							}
						}
						primaryButton(c, p, locales.MemoryImportButton(n), n > 0 && !f.busy).OnClick(func() { a.importMemories(machine) })
						return
					}
					ok := strings.TrimSpace(f.name) != "" && memoryID.MatchString(strings.TrimSpace(f.id)) && !f.busy
					if f.editing != nil {
						primaryButton(c, p, locales.Save(), ok).OnClick(func() { a.updateMemory(machine) })
						return
					}
					primaryButton(c, p, locales.MemoryCreateButton(), ok).OnClick(func() { a.createMemory(machine) })
				})
			})
		})
	})
}

func (a *App) memoryCreateForm(c *ui.Context, p *palette) {
	f := &a.memoryForm
	ui.Row(c).FillWidth().Gap(16).Children(func() {
		ui.Column(c).Grow(1).Basis(0).Children(func() {
			field(c, p, locales.MemoryName(), "", func() {
				before := f.name
				input(c, p, &f.name, "", locales.MemoryName()).AutoFocus()
				// The id follows the name until it is edited.
				if f.name != before && !f.idEdited {
					f.id = strings.ToLower(strings.Join(strings.Fields(f.name), "-"))
				}
			})
		})
		ui.Column(c).Grow(1).Basis(0).Children(func() {
			field(c, p, "Agent ID", "", func() {
				before := f.id
				input(c, p, &f.id, "", "Agent ID").Font(fontMono).Disabled(f.editing != nil)
				if f.id != before {
					f.idEdited = true
				}
			})
		})
	})
	field(c, p, locales.MemoryDescription(), "", func() { area(c, p, &f.description, "", locales.MemoryDescription(), 3) })
	field(c, p, locales.MemoryRole(), "", func() { input(c, p, &f.role, "", locales.MemoryRole()) })
}

func (a *App) memoryImportList(c *ui.Context, p *palette, machine string) {
	f := &a.memoryForm
	inv := a.memoryInventory(machine)
	if !pending(c, p, inv) {
		return
	}
	v, _ := inv.value.(*memoryInventory)
	if v == nil || v.Status != "ready" {
		if v != nil {
			ui.Text(c, memoryStatus(v)).FontSize(sizeFoot).TextColor(p.Secondary)
		}
		return
	}
	linked := map[string]bool{}
	for _, m := range a.catalog.Memories[machine] {
		linked[m.MemoryID] = true
	}
	shown := 0
	for _, m := range v.Memories {
		if linked[m.ID] {
			continue
		}
		shown++
		id := m.ID
		on := f.picked[id]
		ui.Checkbox(c.Key(id), &on, firstNonEmpty(m.Name, m.ID)+" · "+m.ID)
		f.picked[id] = on
	}
	if shown == 0 {
		ui.Text(c, locales.MemoryNothingToImport()).FontSize(sizeFoot).TextColor(p.Secondary)
	}
}

func (a *App) createMemory(machine string) {
	f := &a.memoryForm
	input := map[string]any{"id": strings.TrimSpace(f.id), "name": strings.TrimSpace(f.name)}
	if d := strings.TrimSpace(f.description); d != "" {
		input["description"] = d
	}
	if r := strings.TrimSpace(f.role); r != "" {
		input["role"] = r
	}
	f.busy, f.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
		defer cancel()
		inv, err := a.memoryRPC(ctx, machine, map[string]any{"action": "create", "providerId": nowledgeMem, "input": input})
		a.update(func() {
			f.busy = false
			if err == nil && inv.Status != "ready" {
				err = fmt.Errorf("%s", memoryStatus(inv))
			}
			if err != nil {
				f.err = err.Error()
				return
			}
			for _, m := range inv.Memories {
				if m.ID == input["id"] {
					a.linkMemory(machine, catalog.Memory{ProviderID: nowledgeMem, MemoryID: m.ID, Name: firstNonEmpty(m.Name, m.ID), Description: m.Description})
				}
			}
			f.open = false
			a.refetchKey("memory/" + machine)
		})
	}()
}

func (a *App) importMemories(machine string) {
	f := &a.memoryForm
	if v, _ := a.memoryInventory(machine).value.(*memoryInventory); v != nil {
		for _, m := range v.Memories {
			if f.picked[m.ID] {
				a.linkMemory(machine, catalog.Memory{ProviderID: nowledgeMem, MemoryID: m.ID, Name: firstNonEmpty(m.Name, m.ID), Description: m.Description})
			}
		}
	}
	f.open = false
}

// updateMemory changes a memory on the machine's provider, then its name
// and description in Lody, as Lody's pencil does.
func (a *App) updateMemory(machine string) {
	f := &a.memoryForm
	mem := *f.editing
	input := map[string]any{"id": mem.MemoryID, "name": strings.TrimSpace(f.name), "description": strings.TrimSpace(f.description), "role": strings.TrimSpace(f.role)}
	f.busy, f.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
		defer cancel()
		inv, err := a.memoryRPC(ctx, machine, map[string]any{"action": "update", "providerId": mem.ProviderID, "input": input})
		a.update(func() {
			f.busy = false
			if err == nil && inv.Status != "ready" {
				err = fmt.Errorf("%s", memoryStatus(inv))
			}
			if err != nil {
				f.err = err.Error()
				return
			}
			f.open = false
			name, desc := input["name"].(string), input["description"].(string)
			a.streamWrite(machine, func(fl *flock.Flock) error {
				key := []any{"memory", mem.ProviderID, mem.MemoryID}
				v, ok, err := fl.Get(key)
				if err != nil || !ok {
					return err
				}
				row, _ := v.(map[string]any)
				next := map[string]any{}
				for k, x := range row {
					next[k] = x
				}
				next["name"] = name
				if desc != "" {
					next["description"] = desc
				} else {
					delete(next, "description")
				}
				return fl.Put(key, next)
			}, nil)
			a.refetchKey("memory/" + machine)
		})
	}()
}
