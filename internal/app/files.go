package app

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/lbr77/lody-mygo/internal/collab"
	"github.com/lbr77/lody-mygo/internal/localterm"
	"github.com/lbr77/lody-mygo/locales"
)

const (
	tabFiles   = "files"
	tabChanges = "changes"
)

// inspectState is the inspector's state for a session: the folders it
// listed and opened, the file it shows, and the changes it compared.
type inspectState struct {
	session  string
	tabs     []string               // the panels open, in the order they opened
	tab      string                 // the one shown
	dirs     map[string]*dirListing // by path relative to the project
	open     map[string]bool
	tree     ui.ListState
	file     string // the file Files shows
	preview  *filePreview
	scope    string
	changes  map[string]*changesState // by scope
	change   string                   // the file Changes shows
	turnDiff *turnDiffState           // a reply's change to a file, shown over Changes
	// The files changed, from the local Lody's file index, for a session
	// on this machine.
	changed    map[string]bool
	indexAsked bool
	// The cards folded shut in Changes, by key.
	folded map[string]bool
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

	// An HTML file shown rendered, as lody-ios's preview: in a web view
	// of its own, untrusted, made between frames.
	htmlPreview bool
	web         *mygo.WebView
	webErr      string
	webMaking   bool

	// Editing, through Code Collab: the file as opened, its digest the
	// base a save writes over.
	editing  bool
	opening  bool
	base     *collab.File
	saving   bool
	saveErr  string
	conflict *collab.Conflict
	disk     *collab.File // a newer text the disk has, while there are edits
	checked  time.Time
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
			session: id, scope: collab.ScopeBranch,
			dirs: map[string]*dirListing{}, open: map[string]bool{}, changes: map[string]*changesState{},
		}
	}
	return a.inspect
}

// collabSession is where the open session's files are: in a project of
// several, with the members that are roots of their own mounted at their
// directories.
func (a *App) collabSession() (collab.Session, bool) {
	s := a.catalog.Session(a.selected)
	if s == nil || a.conn == nil || a.account == nil {
		return collab.Session{}, false
	}
	_, local, _ := strings.Cut(s.ProjectID, ":local:")
	return collab.Session{Machine: s.MachineID, LocalProject: local, ID: s.ID, Owner: firstNonEmpty(s.ParentSessionID, s.ID), User: a.account.User.ID,
		Mounts: a.fileMounts(s)}, true
}

// refreshInspector forgets what the shown tab fetched, so it fetches anew.
func (a *App) refreshInspector(in *inspectState) {
	if in.tab == tabFiles {
		in.dirs = map[string]*dirListing{}
		in.indexAsked = false
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

// listDir returns a folder of the project, listing it the first time. A
// member mounted at its directory lists from its own folder; the
// project's top lists those directories too.
func (a *App) listDir(in *inspectState, s collab.Session, rel string) *dirListing {
	if l := in.dirs[rel]; l != nil {
		return l
	}
	l := &dirListing{loading: true}
	in.dirs[rel] = l
	conn := a.conn
	at, within := s, rel
	if m, inside := s.MountOf(rel); m != nil {
		at.LocalProject, within = m.LocalProject, inside
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var entries []collab.DirEntry
		var truncated bool
		var err error
		if at.LocalProject != "" {
			entries, truncated, err = collab.ListDir(ctx, conn, at, within)
		}
		if rel == "" {
			entries = mountEntries(entries, s.Mounts)
		}
		a.update(func() {
			l.loading, l.entries, l.truncated = false, entries, truncated
			if err != nil {
				l.err = err.Error()
			}
		})
	}()
	return l
}

// mountEntries are a top's entries with the directories the mounted
// members show at, as Lody's file tree has them where a link would put
// them; an entry of the same name gives way.
func mountEntries(entries []collab.DirEntry, mounts []collab.Mount) []collab.DirEntry {
	if len(mounts) == 0 {
		return entries
	}
	out := append([]collab.DirEntry(nil), entries...)
	for _, m := range mounts {
		if strings.Contains(m.Path, "/") {
			continue
		}
		out = slices.DeleteFunc(out, func(e collab.DirEntry) bool { return e.Name == m.Path })
		out = append(out, collab.DirEntry{Name: m.Path, Type: "directory"})
	}
	collab.SortEntries(out)
	return out
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
	a.fileBrowser(c, p, in, s)
}

// loadFileIndex reads which files a session on this machine changed,
// from its Lody's file index (code-collab/get-file-index), as Lody's
// desktop app does before the index's stream catches up.
func (a *App) loadFileIndex(in *inspectState, s collab.Session) {
	if in.indexAsked || s.ID == "" || s.Machine != a.localMachine() || a.offline {
		return
	}
	in.indexAsked = true
	ws := a.workspaceID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		raw, err := localterm.MachineRPC(ctx, ws, "code-collab/get-file-index", map[string]any{"sessionId": s.ID, "ownerSessionId": s.Owner}, 30*time.Second)
		var res struct {
			Status    string                     `json:"status"`
			FileIndex map[string]json.RawMessage `json:"fileIndex"`
		}
		if err != nil || json.Unmarshal(raw, &res) != nil || res.Status != "ok" {
			return
		}
		changed := map[string]bool{}
		for p, v := range res.FileIndex {
			var entry struct {
				Change json.RawMessage `json:"change"`
			}
			if json.Unmarshal(v, &entry) == nil && len(entry.Change) > 0 && string(entry.Change) != "null" {
				// A member's file is marked at its directory.
				p = s.Display(p)
				changed[p] = true
				// Its folders hold a change too.
				for dir := path.Dir(p); dir != "." && dir != "/"; dir = path.Dir(dir) {
					changed[dir] = true
				}
			}
		}
		a.update(func() { in.changed = changed })
	}()
}

// fileBrowser browses a local project, a session's or one alone, and
// shows the file chosen.
func (a *App) fileBrowser(c *ui.Context, p *palette, in *inspectState, s collab.Session) {
	a.loadFileIndex(in, s)
	if in.file != "" {
		a.fileView(c, p, in, s)
		return
	}
	if s.LocalProject == "" && len(s.Mounts) == 0 {
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
						spinner(c).Size(11, 11)
					}
					ui.Text(c, r.status).FontSize(sizeFoot).TextColor(p.Tertiary).SingleLine()
				})
				return
			}
			// The row fills under the pointer at once, as Lody's tree's.
			row := ui.Row(c.Key(r.path)).Height(24).Padding(0, 8, 0, indent).Radius(6).Gap(4).AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Label(r.path)
			tip(c, p, row, r.path)
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
						// The chevron turns down over 200ms as the folder
						// opens, as Lody's transition-transform.
						chevron := ui.Icon(c.Key("chevron"), iconChevronRight).Size(12, 12).TextColor(p.Tertiary)
						chevron.Rotate(chevron.AnimateWith("open", 90*b2f(in.open[r.path]), 200*time.Millisecond, twEase))
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
				ink := p.Text
				if in.changed[r.path] {
					ink = p.Warning
				}
				ui.Text(c, r.name).FontSize(sizeSub).TextColor(ink).SingleLine().MinWidth(0)
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
		read := collab.ReadFile
		if s.ID == "" {
			// A project's file, outside any session.
			read = collab.ReadProjectFile
		}
		res, err := read(ctx, conn, s, file)
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
	htmlFile := strings.EqualFold(path.Ext(in.file), ".html") || strings.EqualFold(path.Ext(in.file), ".htm")
	ui.Row(c).Shrink(0).Height(36).Padding(0, 8).Gap(6).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.Border).Children(func() {
		fileToolButton(c, p, iconChevronLeft, locales.NavBack()).OnClick(func() { in.file = "" })
		tip(c, p, ui.Text(c, in.file).Font(fontMono).FontSize(sizeFoot).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0), in.file)
		if markdownFile && fp != nil && fp.text != "" {
			label := locales.PanelSource()
			if fp.asText {
				label = locales.PanelPreview()
			}
			ghostButton(c, p, label).OnClick(func() { fp.asText = !fp.asText })
		}
		if htmlFile && fp != nil && fp.text != "" && !fp.editing {
			// Source first, as lody-ios opens it; Preview renders it.
			label := locales.PanelPreview()
			if fp.htmlPreview {
				label = locales.PanelSource()
			}
			ghostButton(c, p, label).OnClick(func() {
				fp.htmlPreview = !fp.htmlPreview
				if !fp.htmlPreview && fp.web != nil {
					fp.web.Destroy()
					fp.web = nil
				}
			})
		}
		switch {
		case fp != nil && fp.editing:
			// Lody's 6px marks: saving breathes in the info ink, a conflict
			// is red, unsaved edits amber.
			switch {
			case fp.saving:
				savingDot(c, p)
			case fp.conflict != nil:
				ui.Box(c.Key("conflict")).Size(6, 6).Radius(3).Background(p.Danger)
			case fp.code != nil && fp.code.Buffer.Modified():
				tip(c, p, ui.Box(c.Key("dirty")).Size(6, 6).Radius(3).Background(p.Warning), locales.FileUnsaved())
			}
			if fp.code != nil && fp.code.Buffer.Modified() {
				ui.Text(c, locales.FileUnsaved()).FontSize(sizeCaption).TextColor(p.Warning)
			}
			ghostButton(c, p, locales.Done()).OnClick(func() { fp.stopEditing() })
			modified := fp.code != nil && fp.code.Buffer.Modified()
			primaryButton(c, p, locales.Save(), modified && !fp.saving && fp.conflict == nil).OnClick(func() { a.saveFile(fp, s) })
		case fp.editable() && s.ID != "" && !(markdownFile && !fp.asText):
			if fp.opening {
				loader(c, p).Size(14, 14)
			}
			fileToolButton(c, p, iconPencil, locales.EditFile()).OnClick(func() { a.editFile(fp, s) })
		}
		fileToolButton(c, p, iconCopy, locales.Copy()).OnClick(func() { mygo.Clipboard.WriteText(in.file) })
	})
	if fp != nil && fp.saveErr != "" {
		ui.Text(c, fp.saveErr).FontSize(sizeFoot).TextColor(p.Danger).Padding(6, 12).Selectable()
	}
	switch {
	case fp == nil || fp.loading:
		panelLoading(c, p, locales.Loading())
	case fp.err != "":
		panelStatus(c, p, false, locales.PanelCantOpen(), fp.err)
	case fp.image != nil:
		ui.Column(c).Grow(1).MinHeight(0).Padding(16).Center().Children(func() {
			ui.Image(c, fp.image).Fill().Fit(ui.Contain)
		})
	case fp.binary:
		panelStatus(c, p, false, locales.PanelBinary(), firstNonEmpty(fp.mime, ""))
	case htmlFile && fp.htmlPreview:
		a.htmlPreview(c, p, fp)
	case markdownFile && !fp.asText:
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(16, 20, 24, 20).Children(func() {
			markdown.View(c.Key("md/"+in.file), fp.text, &a.md)
		})
	default:
		if fp.editing {
			a.editBanner(c, p, fp, s)
		}
		if fp.code == nil {
			buf := editor.NewBuffer(fp.text)
			buf.SetLanguage(editor.LanguageFor(in.file))
			fp.code = &editor.State{Buffer: buf, ReadOnly: true, Font: ui.Font{Family: fontMono, Size: 12}, NoFolding: true, NoActiveLine: true, Label: in.file}
		}
		editor.View(c, fp.code).Grow(1).MinHeight(0).FillWidth()
		if fp.editing {
			c.OnShortcut(ui.Shift, ui.KeyF12, func() { a.askLanguageServer(fp, s, "references", fp.code.Selection().Head) })
			if fp.code.Buffer.Modified() && !fp.saving && fp.conflict == nil {
				a.surfNext.save = func() { a.saveFile(fp, s) }
			}
			a.watchFile(c, fp, s)
		}
	}
}

// editable is whether the shown file opens for editing: text, not too
// large for Code Collab.
func (fp *filePreview) editable() bool {
	return fp != nil && !fp.loading && fp.err == "" && !fp.binary && fp.image == nil && len(fp.text) <= 10<<20
}

// editFile opens the file through Code Collab, which hands its digest,
// and makes the editor writable.
func (a *App) editFile(fp *filePreview, s collab.Session) {
	if fp.opening || fp.editing {
		return
	}
	fp.opening, fp.saveErr = true, ""
	conn := a.conn
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		f, err := collab.OpenText(ctx, conn, s, fp.path)
		a.update(func() {
			fp.opening = false
			if err != nil {
				fp.saveErr = err.Error()
				return
			}
			if f.ReadOnly {
				fp.saveErr = locales.FileReadOnly()
				return
			}
			fp.base, fp.text, fp.editing, fp.checked = f, f.Text, true, time.Now()
			buf := editor.NewBuffer(f.Text)
			buf.SetLanguage(editor.LanguageFor(fp.path))
			fp.code = &editor.State{Buffer: buf, Font: ui.Font{Family: fontMono, Size: 12}, NoFolding: true, Label: fp.path,
				OnDefinition: func(offset int) { a.goToDefinition(fp, s, offset) }}
		})
	}()
}

// stopEditing goes back to reading, dropping unsaved edits.
func (fp *filePreview) stopEditing() {
	fp.editing, fp.conflict, fp.disk, fp.saveErr = false, nil, nil, ""
	if fp.base != nil {
		fp.text = fp.base.Text
	}
	fp.code = nil
}

// saveFile writes the editor's text over the file it opened.
func (a *App) saveFile(fp *filePreview, s collab.Session) {
	if !fp.editing || fp.saving || fp.conflict != nil || fp.code == nil || fp.base == nil {
		return
	}
	text := fp.code.Buffer.String()
	fp.saving, fp.saveErr = true, ""
	conn, base := a.conn, *fp.base
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
		defer cancel()
		digest, err := collab.SaveText(ctx, conn, s, fp.path, base.Digest, text, base.Format)
		a.update(func() {
			fp.saving = false
			var conflict *collab.Conflict
			switch {
			case errors.As(err, &conflict):
				fp.conflict = conflict
			case err != nil:
				fp.saveErr = err.Error()
			default:
				fp.base.Digest, fp.base.Text, fp.text, fp.disk = digest, text, text, nil
				if fp.code.Buffer.String() == text {
					fp.code.Buffer.MarkSaved()
				}
			}
		})
	}()
}

// watchFile asks every few seconds whether the disk moved on: a clean
// buffer takes the new text, an edited one is told.
func (a *App) watchFile(c *ui.Context, fp *filePreview, s collab.Session) {
	c.After(5 * time.Second)
	if fp.saving || fp.conflict != nil || fp.disk != nil || fp.base == nil || time.Since(fp.checked) < 5*time.Second {
		return
	}
	fp.checked = time.Now()
	conn, path, digest := a.conn, fp.path, fp.base.Digest
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		f, err := collab.RefreshText(ctx, conn, s, path, digest)
		if err != nil || f == nil {
			return
		}
		a.update(func() {
			if !fp.editing || fp.base == nil || fp.base.Digest != digest {
				return
			}
			if fp.code != nil && !fp.code.Buffer.Modified() {
				fp.base, fp.text = f, f.Text
				fp.code.Buffer.Reset(f.Text)
				return
			}
			fp.disk = f
		})
	}()
}

// conflictMarkers sets mine against the disk's, Git's way, to resolve by
// hand.
func conflictMarkers(mine, disk string) string {
	nl := func(s string) string {
		if s != "" && !strings.HasSuffix(s, "\n") {
			return s + "\n"
		}
		return s
	}
	return "<<<<<<< yours\n" + nl(mine) + "=======\n" + nl(disk) + ">>>>>>> disk\n"
}

// editBanner is the editor's state over it: saving, a save the disk
// refused and how to settle it, or a newer text on disk.
func (a *App) editBanner(c *ui.Context, p *palette, fp *filePreview, s collab.Session) {
	switch {
	case fp.conflict != nil:
		cf := fp.conflict
		ui.Column(c).FillWidth().Padding(10, 12).Gap(8).Background(p.Warning.Alpha(0.1)).BorderWidth(0, 0, 1, 0).BorderColor(p.Warning.Alpha(0.3)).Children(func() {
			ui.Text(c, locales.SaveConflictTitle()).FontSize(sizeSub).FontWeight(600).TextColor(p.Text)
			ui.Text(c, locales.SaveConflictDetail(cf.Reason)).FontSize(sizeFoot).TextColor(p.Secondary)
			ui.Row(c).Wrap().Gap(8).Children(func() {
				tip(c, p, ghostButton(c, p, locales.FileReload()).Border(1, p.Border), locales.SaveConflictDiscardHint()).OnClick(func() {
					fp.conflict = nil
					if cf.HasText {
						fp.base = &collab.File{Path: fp.path, Digest: cf.DiskDigest, Text: cf.DiskText, Format: fp.base.Format}
						fp.text = cf.DiskText
						fp.code.Buffer.Reset(cf.DiskText)
						return
					}
					fp.stopEditing()
					a.editFile(fp, s)
				})
				if cf.HasText {
					tip(c, p, ghostButton(c, p, locales.SaveConflictMarkers()).Border(1, p.Border), locales.SaveConflictMarkersHint()).OnClick(func() {
						mine := fp.code.Buffer.String()
						fp.conflict = nil
						fp.base = &collab.File{Path: fp.path, Digest: cf.DiskDigest, Text: cf.DiskText, Format: fp.base.Format}
						fp.code.Buffer.Set(conflictMarkers(mine, cf.DiskText))
					})
				}
				if cf.DiskDigest != "" {
					ghostButton(c, p, locales.SaveConflictOverwrite()).Border(1, p.Danger.Alpha(0.5)).OnClick(func() {
						a.confirm(locales.SaveConflictOverwriteConfirm(), locales.SaveConflictDetail(cf.Reason), locales.SaveConflictOverwrite(), func() {
							fp.conflict = nil
							fp.base.Digest = cf.DiskDigest
							a.saveFile(fp, s)
						})
					})
				}
			})
		})
	case fp.disk != nil:
		ui.Row(c).FillWidth().Padding(8, 12).Gap(8).AlignItems(ui.Center).Background(p.Primary.Alpha(0.08)).Children(func() {
			ui.Text(c, locales.FileLiveReload()).FontSize(sizeFoot).TextColor(p.Secondary).Grow(1).MinWidth(0)
			ghostButton(c, p, locales.FileReload()).Border(1, p.Border).OnClick(func() {
				d := fp.disk
				fp.disk, fp.base, fp.text = nil, d, d.Text
				fp.code.Buffer.Reset(d.Text)
			})
		})
	}
}

// goToDefinition asks the machine's language server where the word at
// offset is defined.
func (a *App) goToDefinition(fp *filePreview, s collab.Session, offset int) {
	a.askLanguageServer(fp, s, "definition", offset)
}

// askLanguageServer asks the machine's language server about the symbol at
// offset: its definition, or its references (Shift+F12).
func (a *App) askLanguageServer(fp *filePreview, s collab.Session, what string, offset int) {
	if fp.code == nil {
		return
	}
	pos := fp.code.Buffer.Pos(offset)
	conn, path := a.conn, fp.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		_, err := collab.Lsp(ctx, conn, s, what, path, pos.Line, pos.Col)
		a.update(func() {
			switch {
			case errors.Is(err, collab.ErrNoLanguageServer):
				a.notice = locales.LspUnavailable()
			case err != nil:
				a.notice = err.Error()
			}
		})
	}()
}

// htmlPreview renders an HTML file's text in a web view of its own.
func (a *App) htmlPreview(c *ui.Context, p *palette, fp *filePreview) {
	if fp.web == nil && fp.webErr == "" && !fp.webMaking && a.win != nil {
		fp.webMaking = true
		win, html := a.win, fp.text
		a.update(func() {
			web, err := win.NewWebView(mygo.WebViewOptions{Page: mygo.PageOptions{Untrusted: true}})
			fp.webMaking, fp.web = false, web
			if err != nil {
				fp.webErr = err.Error()
				return
			}
			web.Page().LoadHTML(html, "")
		})
	}
	switch {
	case fp.webMaking:
		panelLoading(c, p, locales.Loading())
	case fp.web == nil:
		panelStatus(c, p, false, locales.PreviewNoWebView(), fp.webErr)
	default:
		ui.WebView(c, fp.web).Grow(1).MinHeight(0).FillWidth()
	}
}
