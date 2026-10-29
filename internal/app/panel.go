package app

import (
	"context"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lbr77/lody-mygo/internal/collab"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// The inspector: a panel beside the conversation, as Lody's right panel.
// Files browses the session's project and shows a file read from the
// machine; Changes lists what the session changed, as the machine
// compares it with the branch's base or the last commit, or else the
// edits the agent's tool calls carry.

// fileChange is a file a session edited.
type fileChange struct {
	path     string
	add, del int
	edits    []fileEdit
}

// fileEdit is one edit of a file, as a tool call reported it: often the
// region it changed rather than the whole file.
type fileEdit struct {
	key      string
	at       string // the turn's time
	old, new string
}

// sessionChanges gathers the edits of a session by file, files in the
// order they were first edited.
func sessionChanges(v *session.View) []fileChange {
	if v == nil {
		return nil
	}
	var out []fileChange
	index := map[string]int{}
	for _, e := range v.Entries {
		if e.Role != "assistant" {
			continue
		}
		for _, it := range e.Items {
			if it.Type != "tool_call" {
				continue
			}
			for bi, raw := range it.Content {
				block, _ := raw.(map[string]any)
				if block == nil || block["type"] != "diff" {
					continue
				}
				p := str(block["path"])
				if p == "" {
					continue
				}
				i, ok := index[p]
				if !ok {
					i = len(out)
					index[p] = i
					out = append(out, fileChange{path: p})
				}
				old, new := str(block["oldText"]), str(block["newText"])
				add, del := lineChanges(old, new)
				out[i].add += add
				out[i].del += del
				out[i].edits = append(out[i].edits, fileEdit{key: fmt.Sprintf("%s/%s/%d", e.ID, it.ID, bi), at: e.Timestamp, old: old, new: new})
			}
		}
	}
	return out
}

// lineChanges counts the lines added and removed, as Lody counts a tool's
// diff: lines of one side missing from the other.
func lineChanges(old, new string) (add, del int) {
	before, after := strings.Split(old, "\n"), strings.Split(new, "\n")
	inBefore, inAfter := map[string]bool{}, map[string]bool{}
	for _, l := range before {
		inBefore[l] = true
	}
	for _, l := range after {
		inAfter[l] = true
		if !inBefore[l] {
			add++
		}
	}
	for _, l := range before {
		if !inAfter[l] {
			del++
		}
	}
	return add, del
}

// panelOption is a panel the inspector can open.
type panelOption struct {
	id, label string
	icon      *ui.SVG
}

// panelOptions are the panels Lody offers beside a session, the pull
// request's when the session has one.
func (a *App) panelOptions() []panelOption {
	options := []panelOption{
		{tabFiles, locales.PanelFiles(), iconFiles},
		{tabChanges, locales.PanelAllChanges(), iconFileDiff},
		{tabPreview, locales.PanelBrowser(), iconMonitorPlay},
	}
	if s := a.catalog.Session(a.selected); s != nil && s.LatestPR() != nil {
		options = append(options, panelOption{tabPR, locales.PrTabTitle(), iconGitPR})
	}
	return options
}

// openPanel shows a panel, as a tab of its own.
func (in *inspectState) openPanel(id string) {
	if !slices.Contains(in.tabs, id) {
		in.tabs = append(in.tabs, id)
	}
	in.tab = id
}

// closePanel closes a panel's tab, showing the one before it.
func (in *inspectState) closePanel(id string) {
	i := slices.Index(in.tabs, id)
	if i < 0 {
		return
	}
	in.tabs = slices.Delete(in.tabs, i, i+1)
	if in.tab == id {
		in.tab = ""
		if len(in.tabs) > 0 {
			in.tab = in.tabs[max(i-1, 0)]
		}
	}
}

// inspector is the panel beside the conversation: the panels opened as
// tabs, or a choice of them while none is, as Lody's.
func (a *App) inspector(c *ui.Context, p *palette) {
	in := a.inspectFor(a.selected)
	ui.Column(c).Fill().Background(p.Canvas).BorderWidth(0, 0, 0, 1).BorderColor(p.Border).Children(func() {
		ui.Row(c).Height(titleBarHeight).Shrink(0).Padding(0, 8, 0, 8).Gap(2).AlignItems(ui.Center).DragWindow().
			BorderWidth(0, 0, 1, 0).BorderColor(p.Border).Children(func() {
			ui.Row(c).Grow(1).MinWidth(0).Gap(2).AlignItems(ui.Center).Children(func() {
				for _, o := range a.panelOptions() {
					if slices.Contains(in.tabs, o.id) {
						id := o.id
						panelTab(c, p, o.label, in.tab == id, func() { in.closePanel(id) }).OnClick(func() { in.tab = id })
					}
				}
			})
			if in.tab == tabFiles || in.tab == tabChanges {
				iconButton(c, p, iconRefresh, locales.Refresh(), p.Hover).OnClick(func() { a.refreshInspector(in) })
			}
			if len(in.tabs) < len(a.panelOptions()) {
				iconButton(c, p, iconPlus, locales.PanelAdd(), p.Hover).Menu(func(m *ui.Menu) {
					for _, o := range a.panelOptions() {
						if !slices.Contains(in.tabs, o.id) && m.Item(o.label).Chosen() {
							in.openPanel(o.id)
						}
					}
				})
			}
			iconButton(c, p, iconPanelRight, locales.PanelHide(), p.Hover).OnClick(func() { a.panelOpen = false })
		})
		// Keyed by tab, so that each panel fades in as it's chosen.
		ui.Column(c.Key(in.tab)).FillWidth().Grow(1).MinHeight(0).Transition(pageIn).Children(func() {
			switch in.tab {
			case tabFiles:
				a.filesTab(c, p, in)
			case tabChanges:
				a.changesTab(c, p, in)
			case tabPreview:
				a.previewTab(c, p)
			case tabPR:
				a.prTab(c, p)
			default:
				a.panelChooser(c, p, in)
			}
		})
	})
}

// panelChooser offers the panels while none is open: a grid of two
// columns, as wide as the panel lets it be.
func (a *App) panelChooser(c *ui.Context, p *palette, in *inspectState) {
	ui.Column(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Padding(24, 12).Gap(14).Children(func() {
		ui.Text(c, locales.PanelOpenTitle()).FontSize(sizeSub).FontWeight(600).TextColor(p.Text)
		options := a.panelOptions()
		ui.Column(c).FillWidth().MaxWidth(328).Gap(8).Children(func() {
			for i := 0; i < len(options); i += 2 {
				ui.Row(c.Key(i)).FillWidth().Children(func() {
					for j := i; j < i+2; j++ {
						// Half the row each, the gap padded in, so no cell
						// has to shrink to fit.
						ui.Box(c.Key(j)).BasisPercent(50).MinWidth(0).Padding(0, 4).Children(func() {
							if j >= len(options) {
								return
							}
							o := options[j]
							b := ui.ButtonBase(c).FillWidth().Height(40).Padding(0, 12).Radius(8).Gap(8).Justify(ui.Start).AlignItems(ui.Center).
								Border(1, p.Border).Cursor(ui.CursorPointer).Label(o.label).OnClick(func() { in.openPanel(o.id) }).Transition(colorFade)
							pressable(b, 0.98)
							if b.Hovered() {
								b.Background(p.Hover)
							}
							b.Children(func() {
								ui.Icon(c, o.icon).Size(15, 15).TextColor(p.Secondary).Shrink(0)
								ui.Text(c, o.label).FontSize(sizeSub).TextColor(p.Text).SingleLine().MinWidth(0)
							})
						})
					}
				})
			}
		})
	})
}

func panelTab(c *ui.Context, p *palette, label string, active bool, close func()) ui.Element {
	tab := ui.ButtonBase(c.Key(label)).Height(28).Padding(0, 4, 0, 10).Radius(8).Gap(4).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(label).Transition(colorFade)
	ink := p.Secondary
	switch {
	case active:
		tab.Background(p.Text.Alpha(0.07))
		ink = p.Text
	case tab.Hovered():
		tab.Background(p.Hover)
	}
	shown := active || tab.Hovered()
	tab.Children(func() {
		ui.Text(c, label).FontSize(sizeSub).FontWeight(500).TextColor(ink).SingleLine()
		x := ui.ButtonBase(c).Size(18, 18).Radius(5).Center().Cursor(ui.CursorPointer).Label(locales.Close() + " " + label).OnClick(close).Transition(colorFade)
		if x.Hovered() {
			x.Background(p.Text.Alpha(0.08))
		}
		if !shown {
			x.Opacity(0)
		}
		x.Children(func() { ui.Icon(c, iconX).Size(11, 11).TextColor(ink) })
	})
	return tab
}

// changesTab lists what the session changed in its workspace, as the
// machine compares it, and falls back to the edits the conversation shows
// when the machine can't.
func (a *App) changesTab(c *ui.Context, p *palette, in *inspectState) {
	if in.turnDiff != nil {
		a.turnDiffView(c, p, in)
		return
	}
	if in.change != "" {
		a.machineDiff(c, p, in)
		return
	}
	if a.panelFile != "" {
		for _, ch := range sessionChanges(a.view) {
			if ch.path == a.panelFile {
				a.fileEdits(c, p, ch)
				return
			}
		}
		a.panelFile = ""
	}
	cs := a.loadChanges(in)
	ui.Row(c).Shrink(0).Height(40).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		picked := 0
		if in.scope == collab.ScopeUncommitted {
			picked = 1
		}
		was := picked
		ui.Segmented(c, &picked, locales.ScopeBranch(), locales.ScopeUncommitted()).Label(locales.PanelChanges())
		if picked != was {
			in.scope = []string{collab.ScopeBranch, collab.ScopeUncommitted}[picked]
		}
		ui.Spacer(c)
		if cs.res != nil && cs.res.Status == "ok" {
			add, del := 0, 0
			for _, e := range cs.res.Entries {
				if e.Add != nil {
					add += *e.Add
				}
				if e.Del != nil {
					del += *e.Del
				}
			}
			ui.Text(c, fmt.Sprint(len(cs.res.Entries))).FontSize(sizeFoot).TextColor(p.Secondary)
			diffCounts(c, p, add, del)
		}
	})
	switch {
	case cs.loading && cs.res == nil:
		panelStatus(c, p, true, locales.PanelLoadingChanges(), "")
		return
	case cs.res != nil && cs.res.Status == "ok":
		if len(cs.res.Entries) == 0 {
			panelStatus(c, p, false, locales.PanelNoChanges(), "")
			return
		}
		if base := cs.res.Base; base != "" && base != "diff-store" {
			ui.Text(c, locales.PanelComparedWith(shortSHA(base))).FontSize(sizeCaption).TextColor(p.Tertiary).Padding(0, 14, 6, 14)
		}
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, 8, 12, 8).Gap(1).Children(func() {
			for _, e := range cs.res.Entries {
				add, del := -1, -1
				if e.Add != nil {
					add = *e.Add
				}
				if e.Del != nil {
					del = *e.Del
				}
				pathOf := e.Path
				changeRow(c.Key(e.Path), p, e.Path, e.Kind(), add, del).OnClick(func() { in.change = pathOf })
			}
			if cs.res.Truncated {
				ui.Text(c, locales.PanelTruncated()).FontSize(sizeCaption).TextColor(p.Tertiary).Padding(6, 8)
			}
		})
		return
	}
	// The machine couldn't compare: show why, then the conversation's edits.
	reason := cs.err
	if cs.res != nil {
		reason = firstNonEmpty(cs.res.Message, unavailableReason(cs.res.Reason))
	}
	changes := sessionChanges(a.view)
	if reason != "" {
		ui.Text(c, reason).FontSize(sizeFoot).TextColor(p.Secondary).Padding(4, 14, 8, 14).Selectable()
	}
	if len(changes) == 0 {
		panelStatus(c, p, false, locales.PanelNoChanges(), locales.PanelNoChangesHint())
		return
	}
	ui.Text(c, locales.PanelConversationEdits()).FontSize(sizeCaption).FontWeight(600).TextColor(p.Tertiary).Padding(4, 14)
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, 8, 12, 8).Gap(1).Children(func() {
		sorted := append([]fileChange(nil), changes...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].path < sorted[j].path })
		for _, ch := range sorted {
			pathOf := ch.path
			changeRow(c.Key(ch.path), p, ch.path, "", ch.add, ch.del).OnClick(func() { a.panelFile = pathOf })
		}
	})
}

// changeRow is a changed file: its name over its folder, and its counts.
func changeRow(c *ui.Context, p *palette, file, kind string, add, del int) ui.Element {
	dir, name := path.Split(file)
	row := ui.Row(c).MinHeight(36).Padding(4, 8).Radius(6).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(file).Transition(colorFade)
	if row.Hovered() {
		row.Background(p.Hover)
	}
	row.Children(func() {
		ui.Icon(c, iconFile).Size(16, 16).TextColor(p.Secondary).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ink := p.Text
			switch kind {
			case "added":
				ink = p.DiffAdd
			case "deleted":
				ink = p.DiffDel
			}
			ui.Text(c, name).FontSize(sizeSub).LineHeight(20.0 / 13).TextColor(ink).SingleLine()
			if dir != "" {
				ui.Text(c, dir).FontSize(sizeCaption).TextColor(p.Tertiary).SingleLine()
			}
		})
		if add < 0 && del < 0 {
			ui.Text(c, "--").FontSize(sizeFoot).TextColor(p.Tertiary).Tooltip(locales.PanelNoStats())
			return
		}
		diffCounts(c, p, max(add, 0), max(del, 0))
	})
	return row
}

func panelStatus(c *ui.Context, p *palette, loading bool, title, hint string) {
	ui.Column(c).Grow(1).Center().Padding(24).Gap(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if loading {
				spinner(c).Size(13, 13)
			}
			ui.Text(c, title).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
		})
		if hint != "" {
			ui.Text(c, hint).FontSize(sizeFoot).TextColor(p.Secondary).TextAlign(ui.Center)
		}
	})
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func unavailableReason(reason string) string {
	switch reason {
	case "not_a_git_worktree":
		return locales.PanelNotGit()
	case "no_commits":
		return locales.PanelNoCommits()
	case "":
		return ""
	}
	return locales.PanelUnavailable(reason)
}

// machineDiff shows one file of the machine's changes.
func (a *App) machineDiff(c *ui.Context, p *palette, in *inspectState) {
	cs := a.loadChanges(in)
	var entry *collab.Change
	if cs.res != nil {
		for i := range cs.res.Entries {
			if cs.res.Entries[i].Path == in.change {
				entry = &cs.res.Entries[i]
			}
		}
	}
	if entry == nil {
		in.change = ""
		return
	}
	if entry.Status == "deferred" {
		entry = a.deferredChange(in, cs, entry.Path)
	}
	ui.Row(c).Shrink(0).Height(36).Padding(0, 8).Gap(6).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.Border).Children(func() {
		iconButton(c, p, iconChevronLeft, locales.NavBack(), p.Hover).OnClick(func() { in.change = "" })
		ui.Text(c, in.change).Font(fontMono).FontSize(sizeFoot).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
		if entry != nil && entry.Add != nil && entry.Del != nil {
			diffCounts(c, p, *entry.Add, *entry.Del)
		}
	})
	if entry == nil {
		panelStatus(c, p, true, locales.Loading(), "")
		return
	}
	a.changeDiff(c, p, "machine/"+in.scope+"/"+entry.Path, entry)
}

// changeDiff shows a change's two sides as a diff, or why it can't.
func (a *App) changeDiff(c *ui.Context, p *palette, key string, entry *collab.Change) {
	old, okOld := entry.Old.String()
	now, okNew := entry.New.String()
	if entry.Status != "ok" || !okOld || !okNew {
		why := locales.PanelNoDiff()
		switch {
		case entry.Old != nil && entry.Old.Kind == "binary", entry.New != nil && entry.New.Kind == "binary":
			why = locales.PanelBinary()
		case entry.Old != nil && entry.Old.Kind == "too_large", entry.New != nil && entry.New.Kind == "too_large":
			why = locales.PanelTooLarge()
		case entry.Reason != "":
			why = unavailableReason(entry.Reason)
		}
		panelStatus(c, p, false, why, "")
		return
	}
	d := a.diffs[key]
	if d == nil {
		lang := editor.LanguageFor(entry.Path)
		before, after := editor.NewBuffer(old), editor.NewBuffer(now)
		before.SetLanguage(lang)
		after.SetLanguage(lang)
		d = &editor.DiffState{Old: before, New: after, Layout: editor.DiffUnified, Wrap: true}
		a.diffs[key] = d
	}
	editor.DiffView(c, d).Grow(1).MinHeight(0).FillWidth()
}

// fileEdits shows a file's edits, each as its own diff.
func (a *App) fileEdits(c *ui.Context, p *palette, ch fileChange) {
	ui.Row(c).Shrink(0).Height(36).Padding(0, 8).Gap(6).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.Border).Children(func() {
		iconButton(c, p, iconChevronLeft, locales.NavBack(), p.Hover).OnClick(func() { a.panelFile = "" })
		ui.Text(c, ch.path).Font(fontMono).FontSize(sizeFoot).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
		diffCounts(c, p, ch.add, ch.del)
	})
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(12).Gap(12).Children(func() {
		for i, ed := range ch.edits {
			ui.Column(c.Key(ed.key)).FillWidth().Radius(8).Border(1, p.Border).Clip().Children(func() {
				ui.Row(c).Height(28).Padding(0, 10).AlignItems(ui.Center).Background(p.Text.Alpha(0.03)).BorderWidth(0, 0, 1, 0).BorderColor(p.Border).Children(func() {
					label := locales.PanelEdit(i + 1)
					if t := conversationTimeOf(ed.at); t != "" {
						label += " · " + t
					}
					ui.Text(c, label).FontSize(sizeCaption).TextColor(p.Secondary)
				})
				d := a.diffs["panel/"+ed.key]
				if d == nil {
					lang := editor.LanguageFor(ch.path)
					before, after := editor.NewBuffer(ed.old), editor.NewBuffer(ed.new)
					before.SetLanguage(lang)
					after.SetLanguage(lang)
					d = &editor.DiffState{Old: before, New: after, Layout: editor.DiffUnified, FitContent: true, Wrap: true}
					a.diffs["panel/"+ed.key] = d
				}
				editor.DiffView(c, d).FillWidth()
			})
		}
	})
}

// turnDiffState is what one reply changed in a file, as its edited files
// open it.
type turnDiffState struct {
	turn, path string
	loading    bool
	err        string
	change     *collab.Change
}

// openTurnDiff shows a reply's change to a file in the Changes panel.
func (a *App) openTurnDiff(turn, file string) {
	s, ok := a.collabSession()
	if !ok {
		return
	}
	in := a.inspectFor(a.selected)
	in.openPanel(tabChanges)
	a.panelOpen = true
	td := &turnDiffState{turn: turn, path: file, loading: true}
	in.turnDiff = td
	conn := a.conn
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
		defer cancel()
		ch, err := collab.TurnDiff(ctx, conn, s, turn, file)
		a.update(func() {
			td.loading, td.change = false, ch
			if err != nil {
				td.err = err.Error()
			}
		})
	}()
}

func (a *App) turnDiffView(c *ui.Context, p *palette, in *inspectState) {
	td := in.turnDiff
	ui.Row(c).Shrink(0).Height(36).Padding(0, 8).Gap(6).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.Border).Children(func() {
		iconButton(c, p, iconChevronLeft, locales.NavBack(), p.Hover).OnClick(func() { in.turnDiff = nil })
		ui.Text(c, td.path).Font(fontMono).FontSize(sizeFoot).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
		if ch := td.change; ch != nil && ch.Add != nil && ch.Del != nil {
			diffCounts(c, p, *ch.Add, *ch.Del)
		}
	})
	switch {
	case td.loading:
		panelStatus(c, p, true, locales.Loading(), "")
	case td.err != "":
		panelStatus(c, p, false, locales.PanelNoDiff(), td.err)
	case td.change == nil:
		panelStatus(c, p, false, locales.PanelNoDiff(), "")
	default:
		a.changeDiff(c, p, "turn/"+td.turn+"/"+td.path, td.change)
	}
}
