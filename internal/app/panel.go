package app

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// The inspector: a panel beside the conversation, as Lody's right panel.
// Its Changes tab lists the files the session's turns edited, each with
// its edits in order, from the diffs the agent's tool calls carry.

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

// inspector is the panel's content.
func (a *App) inspector(c *ui.Context, p *palette) {
	changes := sessionChanges(a.view)
	ui.Column(c).Fill().Background(p.Canvas).BorderWidth(0, 0, 0, 1).BorderColor(p.Border).Children(func() {
		ui.Row(c).Height(titleBarHeight).Shrink(0).Padding(0, 8, 0, 12).Gap(4).AlignItems(ui.Center).DragWindow().Children(func() {
			tab := ui.Row(c).Height(28).Padding(0, 10).Radius(8).Gap(6).AlignItems(ui.Center).Background(p.Text.Alpha(0.06))
			tab.Children(func() {
				ui.Text(c, locales.PanelChanges()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
				if len(changes) > 0 {
					ui.Text(c, fmt.Sprint(len(changes))).FontSize(sizeFoot).TextColor(p.Secondary)
				}
			})
			ui.Spacer(c)
			iconButton(c, p, iconX, locales.Close(), p.Hover).OnClick(func() { a.panelOpen = false })
		})
		if a.panelFile != "" {
			for _, ch := range changes {
				if ch.path == a.panelFile {
					a.fileEdits(c, p, ch)
					return
				}
			}
			a.panelFile = ""
		}
		if len(changes) == 0 {
			ui.Column(c).Grow(1).Center().Padding(24).Gap(6).Children(func() {
				ui.Text(c, locales.PanelNoChanges()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
				ui.Text(c, locales.PanelNoChangesHint()).FontSize(sizeFoot).TextColor(p.Secondary).TextAlign(ui.Center)
			})
			return
		}
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(4, 8, 12, 8).Gap(1).Children(func() {
			sorted := append([]fileChange(nil), changes...)
			sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].path < sorted[j].path })
			for _, ch := range sorted {
				dir, file := path.Split(ch.path)
				row := ui.Row(c.Key(ch.path)).Height(30).Padding(0, 8).Radius(6).Gap(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(ch.path)
				if row.Hovered() {
					row.Background(p.Hover)
				}
				pathOf := ch.path
				row.OnClick(func() { a.panelFile = pathOf })
				row.Children(func() {
					ui.Icon(c, iconFile).Size(14, 14).TextColor(p.Secondary)
					ui.Text(c, file).FontSize(sizeSub).TextColor(p.Text).SingleLine().Shrink(0).MaxWidth(200)
					ui.Text(c, strings.TrimSuffix(dir, "/")).FontSize(sizeFoot).TextColor(p.Tertiary).SingleLine().Grow(1).MinWidth(0)
					diffCounts(c, p, ch.add, ch.del)
				})
			}
		})
	})
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
