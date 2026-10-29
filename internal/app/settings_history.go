package app

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// A project's Conversations page, as Lody's: the conversations its
// machine's agents had about the project outside Lody, listed by the
// machine (local-project/sync-history) and imported into the workspace
// as chats (import-history), a provider at a time.

type historyItem struct {
	AcpSessionID      string `json:"acpSessionId"`
	Title             string `json:"title"`
	UpdatedAt         string `json:"updatedAt,omitempty"`
	ImportedSessionID string `json:"importedSessionId,omitempty"`
	Status            string `json:"status,omitempty"` // available, imported or sync_conflict
}

type historyCatalog struct {
	LastListedAt float64       `json:"lastListedAt"`
	Sessions     []historyItem `json:"sessions"`
}

type historyState struct {
	catalog   *historyCatalog
	syncing   bool
	importing bool
	selected  map[string]bool
	resolving map[string]bool
	filter    int // all, to import, conflicts, imported
	search    string
	summary   string
	err       string
}

// historyProvider is an agent whose conversations a machine lists.
type historyProvider struct {
	key   string
	label string
	wire  map[string]any
}

// historyProviders are the machine's agents, one a type unless the
// machine tells its configs apart.
func (a *App) historyProviders(machine string) []historyProvider {
	byConfig := a.catalog.MachineCaps[machine]["localProjectHistoryProvider"] >= 1
	var out []historyProvider
	seen := map[string]bool{}
	for _, ag := range a.catalog.Agents[machine] {
		key := ag.CLIType + ":" + ag.AgentType
		wire := map[string]any{"cliType": ag.CLIType, "agentType": ag.AgentType}
		label := ag.Name
		if byConfig {
			key += ":" + ag.ID
			wire["agentConfigId"] = ag.ID
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, historyProvider{key: key, label: label, wire: wire})
	}
	return out
}

func (s *historyItem) state() string {
	if s.Status != "" {
		return s.Status
	}
	if s.ImportedSessionID != "" {
		return "imported"
	}
	return "available"
}

// storedHistory is what the project's row keeps of a provider's last
// listing.
func storedHistory(pr catalog.Project, key string) *historyCatalog {
	entry, _ := pr.History[key].(map[string]any)
	if entry == nil {
		return nil
	}
	out := &historyCatalog{LastListedAt: num(entry["lastListedAt"])}
	sessions, _ := entry["sessions"].(map[string]any)
	for _, v := range sessions {
		b, _ := json.Marshal(v)
		var item historyItem
		if json.Unmarshal(b, &item) == nil && item.AcpSessionID != "" {
			out.Sessions = append(out.Sessions, item)
		}
	}
	return out
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

func sortHistory(items []historyItem) {
	sort.SliceStable(items, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339, items[i].UpdatedAt)
		tj, _ := time.Parse(time.RFC3339, items[j].UpdatedAt)
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return items[i].Title < items[j].Title
	})
}

func (a *App) projectHistory(c *ui.Context, p *palette, pr catalog.Project) {
	w := &a.projectWin
	providers := a.historyProviders(pr.MachineID)
	if len(providers) == 0 {
		emptyCard(c, p, locales.HistoryNoAgents())
		return
	}
	if w.history == nil {
		w.history = map[string]*historyState{}
	}
	current := providers[0]
	for _, pv := range providers {
		if pv.key == w.historyProvider {
			current = pv
		}
	}
	w.historyProvider = current.key
	if len(providers) > 1 {
		ui.Row(c).FillWidth().Gap(18).BorderWidth(0, 0, 1, 0).BorderColor(p.Border).Children(func() {
			for _, pv := range providers {
				provider := pv
				tab := ui.ButtonBase(c.Key(pv.key)).Height(32).Cursor(ui.CursorPointer).Label(pv.label).OnClick(func() { w.historyProvider = provider.key })
				if pv.key == current.key {
					tab.BorderWidth(0, 0, 2, 0).BorderColor(p.Text)
				}
				tab.Children(func() { ui.Text(c, provider.label).FontSize(sizeSub).TextColor(p.Text) })
			}
		})
	}
	st := w.history[current.key]
	if st == nil {
		st = &historyState{selected: map[string]bool{}, resolving: map[string]bool{}, catalog: storedHistory(pr, current.key)}
		w.history[current.key] = st
	}
	online := a.presence.IsOnline(pr.MachineID)
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, locales.HistoryPanel(current.label)).FontSize(sizeSub).FontWeight(600).TextColor(p.Strong)
			synced := locales.HistoryNotSynced()
			if st.catalog != nil && st.catalog.LastListedAt > 0 {
				synced = locales.HistoryLastSynced(agoText(time.UnixMilli(int64(st.catalog.LastListedAt))))
			}
			ui.Text(c, synced).FontSize(sizeFoot).TextColor(p.Secondary)
		})
		if st.syncing {
			ui.Spinner(c).Size(14, 14)
		}
		label := locales.HistorySync()
		if st.catalog != nil {
			label = locales.HistorySyncAgain()
		}
		b := outlineButton(c, p, iconRefresh, label)
		if st.syncing || st.importing || !online {
			b.Disabled(true).Opacity(0.5)
		} else {
			b.OnClick(func() { a.syncHistory(pr, current, st) })
		}
	})
	if st.err != "" {
		ui.Text(c, st.err).FontSize(sizeFoot).TextColor(p.Danger).Selectable()
	}
	if st.summary != "" {
		ui.Text(c, st.summary).FontSize(sizeFoot).TextColor(p.Secondary)
	}
	if st.catalog == nil {
		ui.Column(c).FillWidth().Padding(18).Radius(10).Border(1, p.Border).Gap(6).Children(func() {
			ui.Text(c, locales.HistoryInitialTitle(current.label)).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
			ui.Text(c, locales.HistoryInitialHint(current.label)).FontSize(sizeFoot).TextColor(p.Secondary)
		})
		return
	}
	if len(st.catalog.Sessions) == 0 {
		ui.Column(c).FillWidth().Padding(18).Radius(10).Border(1, p.Border).Gap(6).Children(func() {
			ui.Text(c, locales.HistoryEmpty(current.label)).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
			ui.Text(c, locales.HistoryEmptyHint(current.label)).FontSize(sizeFoot).TextColor(p.Secondary)
		})
		return
	}
	items := append([]historyItem(nil), st.catalog.Sessions...)
	sortHistory(items)
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Segmented(c, &st.filter, locales.HistoryAll(), locales.HistoryToImport(), locales.HistoryConflicts(), locales.HistoryImported()).Label(locales.HistoryPanel(current.label))
		ui.TextInput(c, &st.search).Placeholder(locales.HistorySearch()).Label(locales.HistorySearch()).FontSize(sizeSub).Grow(1).Height(30).
			Padding(0, 10).Radius(8).Background(p.Text.Alpha(0.05))
	})
	want := []string{"", "available", "sync_conflict", "imported"}[st.filter]
	q := strings.ToLower(strings.TrimSpace(st.search))
	var shown []historyItem
	for _, it := range items {
		if (want == "" || it.state() == want) && (q == "" || strings.Contains(strings.ToLower(it.Title), q)) {
			shown = append(shown, it)
		}
	}
	selectable, picked := 0, 0
	for _, it := range shown {
		if it.state() == "available" {
			selectable++
		}
	}
	for _, it := range items {
		if st.selected[it.AcpSessionID] && it.state() == "available" {
			picked++
		}
	}
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		if selectable > 0 {
			ghostButton(c, p, locales.HistorySelectShown(selectable)).OnClick(func() {
				for _, it := range shown {
					if it.state() == "available" {
						st.selected[it.AcpSessionID] = true
					}
				}
			})
		}
		ui.Spacer(c)
		if st.importing {
			ui.Spinner(c).Size(14, 14)
		}
		primaryButton(c, p, locales.HistoryImportCount(picked), picked > 0 && !st.importing && !st.syncing && online).OnClick(func() {
			a.importHistory(pr, current, st)
		})
	})
	if len(shown) == 0 {
		emptyCard(c, p, locales.HistoryNoMatches())
		return
	}
	card(c, p, func() {
		for _, it := range shown {
			item := it
			ui.Row(c.Key(it.AcpSessionID)).FillWidth().MinHeight(44).Padding(6, 12).Gap(10).AlignItems(ui.Center).Children(func() {
				switch item.state() {
				case "available":
					on := st.selected[item.AcpSessionID]
					ui.CheckboxBase(c, &on).Label(item.Title)
					st.selected[item.AcpSessionID] = on
				default:
					ui.Box(c).Size(16, 16)
				}
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, firstNonEmpty(item.Title, item.AcpSessionID)).FontSize(sizeSub).TextColor(p.Text).SingleLine()
					when := locales.HistoryUnknownTime()
					if t, err := time.Parse(time.RFC3339, item.UpdatedAt); err == nil {
						when = agoText(t)
					}
					ui.Text(c, when).FontSize(sizeCaption).TextColor(p.Tertiary)
				})
				switch item.state() {
				case "imported":
					ui.Text(c, locales.HistoryImported()).FontSize(sizeCaption).TextColor(p.Secondary).Padding(1, 6).Radius(5).Background(p.Text.Alpha(0.06))
				case "sync_conflict":
					ui.Text(c, locales.HistoryConflict()).FontSize(sizeCaption).TextColor(p.Warning).Padding(1, 6).Radius(5).Background(p.Warning.Alpha(0.12))
					if st.resolving[item.AcpSessionID] {
						ui.Spinner(c).Size(12, 12)
					} else if online {
						ghostButton(c, p, locales.HistoryResolve()).Border(1, p.Border).OnClick(func() { a.resolveHistory(pr, current, st, item) })
					}
				}
			})
		}
	})
}

// historyCall asks the project's machine about its agents' history.
func (a *App) historyCall(ctx context.Context, pr catalog.Project, request map[string]any, out any) error {
	_, local, _ := strings.Cut(pr.ID, ":local:")
	request["localProjectId"] = local
	if a.account != nil {
		request["requestedByUserId"] = a.account.User.ID
	}
	raw, err := a.conn.ProjectControl(ctx, pr.MachineID, request, 120*time.Second)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (a *App) syncHistory(pr catalog.Project, pv historyProvider, st *historyState) {
	if a.conn == nil {
		return
	}
	st.syncing, st.err, st.summary = true, "", ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 125*time.Second)
		defer cancel()
		var res historyCatalog
		err := a.historyCall(ctx, pr, map[string]any{"type": "local-project/sync-history", "provider": pv.wire}, &res)
		a.update(func() {
			st.syncing = false
			if err != nil {
				st.err = locales.HistorySyncFailed(pv.label) + ": " + err.Error()
				return
			}
			st.catalog, st.selected = &res, map[string]bool{}
		})
	}()
}

type historySummary struct {
	Imported   int `json:"imported"`
	Refreshed  int `json:"refreshed"`
	Skipped    int `json:"skipped"`
	Conflicted int `json:"conflicted"`
	Failed     int `json:"failed"`
}

// importHistory imports the chosen conversations, a batch at a time so
// that each batch shows as it lands.
func (a *App) importHistory(pr catalog.Project, pv historyProvider, st *historyState) {
	if a.conn == nil {
		return
	}
	var ids []string
	for id, on := range st.selected {
		if on {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	st.importing, st.err, st.summary = true, "", ""
	go func() {
		var total historySummary
		var err error
		for start := 0; start < len(ids) && err == nil; start += 20 {
			batch := ids[start:min(start+20, len(ids))]
			ctx, cancel := context.WithTimeout(context.Background(), 125*time.Second)
			var res struct {
				Summary historySummary `json:"summary"`
				Catalog historyCatalog `json:"catalog"`
			}
			err = a.historyCall(ctx, pr, map[string]any{"type": "local-project/import-history", "provider": pv.wire, "acpSessionIds": batch}, &res)
			cancel()
			if err != nil {
				break
			}
			total.Imported += res.Summary.Imported
			total.Refreshed += res.Summary.Refreshed
			total.Skipped += res.Summary.Skipped
			total.Conflicted += res.Summary.Conflicted
			total.Failed += res.Summary.Failed
			catalog, sum := res.Catalog, total
			a.update(func() {
				st.catalog = &catalog
				st.summary = locales.HistorySummary(sum.Imported, sum.Refreshed, sum.Skipped, sum.Conflicted, sum.Failed)
				for _, id := range batch {
					delete(st.selected, id)
				}
			})
		}
		a.update(func() {
			st.importing = false
			if err != nil {
				st.err = err.Error()
			}
		})
	}()
}

// resolveHistory takes the agent's version of a conversation that changed
// on both sides since it was imported.
func (a *App) resolveHistory(pr catalog.Project, pv historyProvider, st *historyState, it historyItem) {
	if a.conn == nil || it.ImportedSessionID == "" {
		return
	}
	st.resolving[it.AcpSessionID] = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 125*time.Second)
		defer cancel()
		var res struct {
			Catalog historyCatalog `json:"catalog"`
		}
		err := a.historyCall(ctx, pr, map[string]any{"type": "local-project/resolve-history-conflict", "provider": pv.wire,
			"sessionId": it.ImportedSessionID, "acpSessionId": it.AcpSessionID}, &res)
		a.update(func() {
			delete(st.resolving, it.AcpSessionID)
			if err != nil {
				st.err = err.Error()
				return
			}
			st.catalog = &res.Catalog
		})
	}()
}
