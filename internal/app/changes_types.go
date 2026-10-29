package app

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/collab"
	"github.com/lbr77/lody-mygo/locales"
)

// Lody's Changes panel sorts the changed files by what they are — code,
// docs, tests, the project's tooling — under folds that open with their
// chevron turning 90° over 150ms and their rows unfolding over 200ms,
// easing out; Files lists them as they are. Only a strong signal in the
// path takes a file out of code.

var (
	docExts      = []string{".adoc", ".markdown", ".md", ".mdx", ".rst"}
	docNames     = []string{"authors", "changelog", "code_of_conduct", "contributing", "license", "notice", "readme", "security"}
	docSegments  = []string{"doc", "docs", "documentation"}
	testSegments = []string{"__test__", "__tests__", "spec", "test", "tests"}
	devSegments  = []string{".circleci", ".github", ".husky", ".idea", ".vscode", "node_modules"}
	devNames     = []string{".dockerignore", ".editorconfig", ".eslintignore", ".eslintrc", ".gitattributes", ".gitignore", ".npmrc", ".nvmrc",
		".prettierignore", ".prettierrc", "bun.lock", "bun.lockb", "cargo.lock", "docker-compose.yml", "docker-compose.yaml", "gemfile.lock",
		"package-lock.json", "pnpm-lock.yaml", "yarn.lock"}
	devPrefixes = []string{".env", ".eslint", ".prettier"}
	devSuffixes = []string{".config.cjs", ".config.cts", ".config.js", ".config.json", ".config.mjs", ".config.mts", ".config.ts", ".config.yaml",
		".config.yml", "config.cjs", "config.cts", "config.js", "config.mjs", "config.mts", "config.ts"}
	tsconfigRe = regexp.MustCompile(`(^|/)(tsconfig|jsconfig)(\.[^.]+)?\.json$`)
	dtsRe      = regexp.MustCompile(`\.d\.ts$`)
	jsRe       = regexp.MustCompile(`\.[cm]?[jt]sx?$`)
	extRe      = regexp.MustCompile(`\.[^.]+$`)
	testNameRe = regexp.MustCompile(`\.(test|spec)$|^test[-_]|[-_]test$`)
)

// changeCategories are the folds, in Lody's order.
var changeCategories = []string{"code", "doc", "test", "dev"}

// stem is a file name without its extension, a .d.ts or a script's as one.
func stem(name string) string {
	switch {
	case dtsRe.MatchString(name):
		return dtsRe.ReplaceAllString(name, "")
	case jsRe.MatchString(name):
		return jsRe.ReplaceAllString(name, "")
	}
	return extRe.ReplaceAllString(name, "")
}

// classifyChange is what a changed file is: test, doc, dev or code.
func classifyChange(file string) string {
	p := strings.ReplaceAll(file, `\`, "/")
	for strings.HasPrefix(p, "./") {
		p = strings.TrimLeft(p[1:], "/")
	}
	p = strings.ToLower(p)
	var segs []string
	for _, s := range strings.Split(p, "/") {
		if s = strings.TrimSpace(s); s != "" {
			segs = append(segs, s)
		}
	}
	name := ""
	if len(segs) > 0 {
		name = segs[len(segs)-1]
	}
	any := func(set []string) bool {
		return slices.ContainsFunc(segs, func(s string) bool { return slices.Contains(set, s) })
	}
	if any(testSegments) || testNameRe.MatchString(stem(name)) {
		return "test"
	}
	if slices.Contains(docExts, path.Ext(name)) || slices.Contains(docNames, stem(name)) || any(docSegments) {
		return "doc"
	}
	switch {
	case any(devSegments), slices.Contains(devNames, name), tsconfigRe.MatchString(p):
		return "dev"
	}
	for _, pre := range devPrefixes {
		if name == pre || strings.HasPrefix(name, pre+".") {
			return "dev"
		}
	}
	for _, suf := range devSuffixes {
		if strings.HasSuffix(name, suf) {
			return "dev"
		}
	}
	return "code"
}

// changeCategoryLabel is a fold's name.
func changeCategoryLabel(cat string) string {
	switch cat {
	case "doc":
		return locales.ChangeCatDoc()
	case "test":
		return locales.ChangeCatTest()
	case "dev":
		return locales.ChangeCatDev()
	}
	return locales.ChangeCatCode()
}

// changesViewSwitch is the panel's Types and Files, as Lody's two segment
// buttons.
func (a *App) changesViewSwitch(c *ui.Context, p *palette) {
	ui.Row(c.Key("view-switch")).Gap(2).Padding(2).Radius(6).Background(p.Text.Alpha(0.05)).Shrink(0).Children(func() {
		for _, v := range []struct{ id, label string }{{"types", locales.ChangesTypes()}, {"files", locales.ChangesFiles()}} {
			id := v.id
			on := a.changesView == id || a.changesView == "" && id == "types"
			b := ui.ButtonBase(c.Key(id)).Height(20).Padding(0, 8).Radius(4).Cursor(ui.CursorPointer).Label(v.label).Transition(twFade).
				OnClick(func() { a.changesView = id })
			ink := p.Secondary
			if on {
				b.Background(p.Popover).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.08))
				ink = p.Text
			} else if b.Hovered() {
				ink = p.Text
			}
			ink = b.AnimateColor("ink", ink, ui.Animation{Duration: 150 * time.Millisecond, Ease: twEase})
			b.Children(func() { ui.Text(c, v.label).FontSize(sizeCaption).FontWeight(500).TextColor(ink) })
		}
	})
}

// changeTypes lists the changes under their folds, each open until folded.
func (a *App) changeTypes(c *ui.Context, p *palette, in *inspectState, entries []collab.Change) {
	groups := map[string][]collab.Change{}
	for _, e := range entries {
		cat := classifyChange(e.Path)
		groups[cat] = append(groups[cat], e)
	}
	for _, cat := range changeCategories {
		group := groups[cat]
		if len(group) == 0 {
			continue
		}
		key := "changes/" + cat
		open := !a.collapsed[key]
		add, del := 0, 0
		for _, e := range group {
			if e.Add != nil {
				add += *e.Add
			}
			if e.Del != nil {
				del += *e.Del
			}
		}
		ui.Column(c.Key(cat)).FillWidth().Children(func() {
			head := ui.ButtonBase(c.Key("head")).FillWidth().Height(28).Padding(0, 8).Gap(6).Radius(6).AlignItems(ui.Center).Justify(ui.Start).
				Cursor(ui.CursorPointer).Label(changeCategoryLabel(cat)).OnClick(func() { a.collapsed[key] = open })
			turn := head.AnimateWith("turn", b2f(open), 150*time.Millisecond, cssEase)
			k := head.AnimateWith("open", b2f(open), 200*time.Millisecond, cssEaseOut)
			head.Children(func() {
				ui.Icon(c, iconChevronRight).Size(14, 14).TextColor(p.Secondary).Rotate(90 * turn)
				ui.Text(c, changeCategoryLabel(cat)).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text)
				ui.Text(c, fmt.Sprint(len(group))).FontSize(sizeFoot).TextColor(p.Tertiary)
				ui.Spacer(c)
				diffCounts(c, p, add, del)
			})
			heightReveal(c, k, open, func() {
				ui.Column(c).FillWidth().Gap(1).Children(func() {
					for _, e := range group {
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
				})
			})
		})
	}
}

// changeNode is a folder or file of the Files tree.
type changeNode struct {
	name, path string
	dirs       []*changeNode
	files      []collab.Change
}

// changeTree folds the changes into their folders, folders before files,
// each sorted by name.
func changeTree(entries []collab.Change) *changeNode {
	root := &changeNode{}
	for _, e := range entries {
		segs := strings.Split(strings.Trim(strings.ReplaceAll(e.Path, `\`, "/"), "/"), "/")
		node := root
		for i, seg := range segs[:len(segs)-1] {
			var next *changeNode
			for _, d := range node.dirs {
				if d.name == seg {
					next = d
				}
			}
			if next == nil {
				next = &changeNode{name: seg, path: strings.Join(segs[:i+1], "/")}
				node.dirs = append(node.dirs, next)
			}
			node = next
		}
		node.files = append(node.files, e)
	}
	var sortNode func(n *changeNode)
	sortNode = func(n *changeNode) {
		slices.SortFunc(n.dirs, func(x, y *changeNode) int { return strings.Compare(strings.ToLower(x.name), strings.ToLower(y.name)) })
		slices.SortFunc(n.files, func(x, y collab.Change) int {
			return strings.Compare(strings.ToLower(path.Base(x.Path)), strings.ToLower(path.Base(y.Path)))
		})
		for _, d := range n.dirs {
			sortNode(d)
		}
	}
	sortNode(root)
	return root
}

// changeFiles is Lody's Files view: the changed files in their folders,
// every folder open until folded, a folder's chevron turning over 200ms.
func (a *App) changeFiles(c *ui.Context, p *palette, in *inspectState, entries []collab.Change) {
	var walk func(n *changeNode, depth int)
	walk = func(n *changeNode, depth int) {
		indent := float32(depth) * 12
		for _, d := range n.dirs {
			key := "chtree/" + d.path
			open := !a.collapsed[key]
			dir := d
			ui.Column(c.Key("d/" + d.path)).FillWidth().Children(func() {
				row := ui.ButtonBase(c.Key("row")).FillWidth().Height(28).Padding(0, 8, 0, 8+indent).Gap(6).Radius(6).AlignItems(ui.Center).
					Justify(ui.Start).Cursor(ui.CursorPointer).Label(dir.name).OnClick(func() { a.collapsed[key] = open })
				turn := row.AnimateWith("turn", b2f(open), 200*time.Millisecond, twEase)
				row.Children(func() {
					ui.Icon(c, iconChevronRight).Size(14, 14).TextColor(p.Secondary).Rotate(90 * turn)
					icon := iconFolder
					if open {
						icon = iconFolderOpen
					}
					ui.Icon(c, icon).Size(16, 16).TextColor(p.Secondary).Margin(0, 6, 0, 0)
					ui.Text(c, dir.name).FontSize(sizeBody).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
				})
				if open {
					walk(dir, depth+1)
				}
			})
		}
		for _, f := range n.files {
			add, del := -1, -1
			if f.Add != nil {
				add = *f.Add
			}
			if f.Del != nil {
				del = *f.Del
			}
			pathOf := f.Path
			row := ui.ButtonBase(c.Key("f/"+f.Path)).FillWidth().Height(28).Padding(0, 8, 0, 8+indent+20).Gap(6).Radius(6).AlignItems(ui.Center).
				Justify(ui.Start).Cursor(ui.CursorPointer).Label(f.Path).OnClick(func() { in.change = pathOf })
			if row.Hovered() {
				row.Background(p.Hover)
			}
			row.Children(func() {
				ui.Icon(c, iconFile).Size(16, 16).TextColor(p.Secondary).Margin(0, 6, 0, 0)
				ui.Text(c, path.Base(pathOf)).FontSize(sizeBody).TextColor(p.Text.Alpha(0.9)).SingleLine().Grow(1).MinWidth(0)
				if add >= 0 && del >= 0 {
					diffCounts(c, p, add, del)
				}
			})
		}
	}
	walk(changeTree(entries), 0)
}
