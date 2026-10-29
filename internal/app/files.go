package app

import (
	"context"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/lbr77/lody-mygo/internal/collab"
	"github.com/lbr77/lody-mygo/locales"
)

const (
	tabFiles   = "files"
	tabChanges = "changes"
)

// inspectState is the inspector's state for a session: the folders it
// listed and opened, the file it shows, and the changes it compared.
type inspectState struct {
	session string
	tab     string
	dirs    map[string]*dirListing // by path relative to the project
	open    map[string]bool
	tree    ui.ListState
	file    string // the file Files shows
	preview *filePreview
	scope   string
	changes map[string]*changesState // by scope
	change  string                   // the file Changes shows
}

type dirListing struct {
	entries   []collab.DirEntry
	truncated bool
	loading   bool
	err       string
}

type filePreview struct {
	path    string
	loading bool
	err     string
	text    string
	binary  bool
	mime    string
	size    int
	image   *ui.Bitmap
	code    *editor.State
	asText  bool // a Markdown file shown as its source
}

type changesState struct {
	loading  bool
	err      string
	res      *collab.Changes
	deferred map[string]*collab.Change // files fetched one at a time
}

// inspectFor returns the inspector's state for a session, made anew when
// the session changes.
func (a *App) inspectFor(id string) *inspectState {
	if a.inspect == nil || a.inspect.session != id {
		a.inspect = &inspectState{
			session: id, tab: tabChanges, scope: collab.ScopeBranch,
			dirs: map[string]*dirListing{}, open: map[string]bool{}, changes: map[string]*changesState{},
		}
	}
	return a.inspect
}

// collabSession is where the open session's files are.
func (a *App) collabSession() (collab.Session, bool) {
	s := a.catalog.Session(a.selected)
	if s == nil || a.conn == nil || a.account == nil {
		return collab.Session{}, false
	}
	_, local, _ := strings.Cut(s.ProjectID, ":local:")
	return collab.Session{Machine: s.MachineID, LocalProject: local, ID: s.ID, Owner: firstNonEmpty(s.ParentSessionID, s.ID), User: a.account.User.ID}, true
}

// refreshInspector forgets what the shown tab fetched, so it fetches anew.
func (a *App) refreshInspector(in *inspectState) {
	if in.tab == tabFiles {
		in.dirs = map[string]*dirListing{}
		if in.preview != nil && !in.preview.loading {
			in.preview = nil
		}
		return
	}
	in.changes = map[string]*changesState{}
	for k := range a.diffs {
		if strings.HasPrefix(k, "machine/") {
			delete(a.diffs, k)
		}
	}
}

// loadChanges returns the session's changes in the inspector's scope,
// fetching them the first time.
func (a *App) loadChanges(in *inspectState) *changesState {
	cs := in.changes[in.scope]
	if cs != nil {
		return cs
	}
	cs = &changesState{deferred: map[string]*collab.Change{}}
	in.changes[in.scope] = cs
	s, ok := a.collabSession()
	if !ok {
		return cs
	}
	if !a.presence.IsOnline(s.Machine) {
		cs.err = locales.MentionOffline(a.machineName(s.Machine))
		return cs
	}
	cs.loading = true
	conn, scope := a.conn, in.scope
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
		defer cancel()
		res, err := collab.AllChanges(ctx, conn, s, scope)
		a.update(func() {
			cs.loading, cs.res = false, res
			if err != nil {
				cs.err = err.Error()
			}
		})
	}()
	return cs
}

// deferredChange fetches a file whose diff the list left out, as it does
// past its budget.
func (a *App) deferredChange(in *inspectState, cs *changesState, file string) *collab.Change {
	if c, ok := cs.deferred[file]; ok {
		return c
	}
	cs.deferred[file] = nil
	s, ok := a.collabSession()
	if !ok {
		return nil
	}
	conn, scope := a.conn, in.scope
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		c, err := collab.CurrentDiff(ctx, conn, s, file, scope)
		if err != nil {
			c = &collab.Change{Status: "unavailable", Path: file, Reason: err.Error()}
		}
		a.update(func() { cs.deferred[file] = c })
	}()
	return nil
}

// listDir returns a folder of the project, listing it the first time.
func (a *App) listDir(in *inspectState, s collab.Session, rel string) *dirListing {
	if l := in.dirs[rel]; l != nil {
		return l
	}
	l := &dirListing{loading: true}
	in.dirs[rel] = l
	conn := a.conn
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		entries, truncated, err := collab.ListDir(ctx, conn, s, rel)
		a.update(func() {
			l.loading, l.entries, l.truncated = false, entries, truncated
			if err != nil {
				l.err = err.Error()
			}
		})
	}()
	return l
}

// treeRow is a row of the file tree: an entry at a depth, or a folder's
// state while it lists.
type treeRow struct {
	path    string
	name    string
	dir     bool
	depth   int
	status  string // loading, an error, or "more" past what a folder listed
	loading bool
}

// treeRows flattens the open folders into rows.
func (a *App) treeRows(in *inspectState, s collab.Session, rel string, depth int, out []treeRow) []treeRow {
	l := a.listDir(in, s, rel)
	switch {
	case l.loading:
		return append(out, treeRow{path: rel + "/…", depth: depth, loading: true, status: locales.Loading()})
	case l.err != "":
		return append(out, treeRow{path: rel + "/!", depth: depth, status: l.err})
	}
	for _, e := range l.entries {
		p := path.Join(rel, e.Name)
		out = append(out, treeRow{path: p, name: e.Name, dir: e.Dir(), depth: depth})
		if e.Dir() && in.open[p] {
			out = a.treeRows(in, s, p, depth+1, out)
		}
	}
	if l.truncated {
		out = append(out, treeRow{path: rel + "/+", depth: depth, status: locales.PanelTruncated()})
	}
	return out
}

// filesTab browses the session's project, and shows the file chosen.
func (a *App) filesTab(c *ui.Context, p *palette, in *inspectState) {
	s, ok := a.collabSession()
	if !ok {
		return
	}
	if in.file != "" {
		a.fileView(c, p, in, s)
		return
	}
	if s.LocalProject == "" {
		panelStatus(c, p, false, locales.PanelNoFiles(), locales.PanelNoFilesHint())
		return
	}
	if !a.presence.IsOnline(s.Machine) {
		panelStatus(c, p, false, locales.MentionOffline(a.machineName(s.Machine)), "")
		return
	}
	rows := a.treeRows(in, s, "", 0, nil)
	ui.Column(c).Grow(1).MinHeight(0).Padding(4, 4, 8, 4).Children(func() {
		ui.List(c, &in.tree, len(rows), func(i int) {
			r := rows[i]
			indent := float32(r.depth)*12 + 6
			if r.status != "" {
				ui.Row(c.Key(r.path)).Height(24).Padding(0, 8, 0, indent+18).Gap(6).AlignItems(ui.Center).Children(func() {
					if r.loading {
						ui.Spinner(c).Size(11, 11)
					}
					ui.Text(c, r.status).FontSize(sizeFoot).TextColor(p.Tertiary).SingleLine()
				})
				return
			}
			row := ui.Row(c.Key(r.path)).Height(24).Padding(0, 8, 0, indent).Radius(6).Gap(4).AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Label(r.path).Tooltip(r.path)
			if row.Hovered() {
				row.Background(p.Hover)
			}
			pathOf, dir := r.path, r.dir
			row.OnClick(func() {
				if dir {
					in.open[pathOf] = !in.open[pathOf]
				} else {
					a.openFile(in, s, pathOf)
				}
			})
			row.Children(func() {
				ui.Box(c).Size(14, 14).Center().Shrink(0).Children(func() {
					if r.dir {
						icon := iconChevronRight
						if in.open[r.path] {
							icon = iconChevronDown
						}
						ui.Icon(c, icon).Size(12, 12).TextColor(p.Tertiary)
					}
				})
				icon := iconFile
				if r.dir {
					icon = iconFolder
					if in.open[r.path] {
						icon = iconFolderOpen
					}
				}
				ui.Icon(c, icon).Size(15, 15).TextColor(p.Secondary).Shrink(0)
				ui.Text(c, r.name).FontSize(sizeSub).TextColor(p.Text).SingleLine().MinWidth(0)
			})
		}).Fill()
	})
}

// openFile reads a file from the machine to show it.
func (a *App) openFile(in *inspectState, s collab.Session, file string) {
	in.file = file
	if in.preview != nil && in.preview.path == file && in.preview.err == "" {
		return
	}
	fp := &filePreview{path: file, loading: true}
	in.preview = fp
	conn := a.conn
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		res, err := collab.ReadFile(ctx, conn, s, file)
		var data []byte
		if err == nil {
			data, err = res.Content.Bytes()
		}
		var bm *ui.Bitmap
		if err == nil && res.Kind == "binary" && strings.HasPrefix(res.MimeType, "image/") {
			bm, _ = ui.DecodeBitmap(data)
		}
		a.update(func() {
			fp.loading = false
			if err != nil {
				fp.err = err.Error()
				return
			}
			fp.size, fp.mime, fp.image = res.SizeBytes, res.MimeType, bm
			fp.binary = res.Kind == "binary" || !utf8.Valid(data)
			if !fp.binary {
				fp.text = string(data)
			}
		})
	}()
}

// fileView shows the file Files opened: its text in a read-only editor,
// Markdown rendered, a picture, or why it can't show.
func (a *App) fileView(c *ui.Context, p *palette, in *inspectState, s collab.Session) {
	fp := in.preview
	markdownFile := strings.EqualFold(path.Ext(in.file), ".md") || strings.EqualFold(path.Ext(in.file), ".markdown")
	ui.Row(c).Shrink(0).Height(36).Padding(0, 8).Gap(6).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.Border).Children(func() {
		iconButton(c, p, iconChevronLeft, locales.NavBack(), p.Hover).OnClick(func() { in.file = "" })
		ui.Text(c, in.file).Font(fontMono).FontSize(sizeFoot).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0).Tooltip(in.file)
		if markdownFile && fp != nil && fp.text != "" {
			label := locales.PanelSource()
			if fp.asText {
				label = locales.PanelPreview()
			}
			ghostButton(c, p, label).OnClick(func() { fp.asText = !fp.asText })
		}
		iconButton(c, p, iconCopy, locales.Copy(), p.Hover).OnClick(func() { mygo.Clipboard.WriteText(in.file) })
	})
	switch {
	case fp == nil || fp.loading:
		panelStatus(c, p, true, locales.Loading(), "")
	case fp.err != "":
		panelStatus(c, p, false, locales.PanelCantOpen(), fp.err)
	case fp.image != nil:
		ui.Column(c).Grow(1).MinHeight(0).Padding(16).Center().Children(func() {
			ui.Image(c, fp.image).Fill().Fit(ui.Contain)
		})
	case fp.binary:
		panelStatus(c, p, false, locales.PanelBinary(), firstNonEmpty(fp.mime, ""))
	case markdownFile && !fp.asText:
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(16, 20, 24, 20).Children(func() {
			markdown.View(c.Key("md/"+in.file), fp.text, &a.md)
		})
	default:
		if fp.code == nil {
			buf := editor.NewBuffer(fp.text)
			buf.SetLanguage(editor.LanguageFor(in.file))
			fp.code = &editor.State{Buffer: buf, ReadOnly: true, Font: ui.Font{Family: fontMono, Size: 12}, NoFolding: true, NoActiveLine: true, Label: in.file}
		}
		editor.View(c, fp.code).Grow(1).MinHeight(0).FillWidth()
	}
}
