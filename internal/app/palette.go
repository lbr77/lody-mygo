package app

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/lbr77/lody-mygo/locales"
)

// The ⌘K palette, as Lody's: with no query the commands that apply now,
// under their categories; with one, commands and chats in one list by how
// well they match. The sidebar's Search opens it too.

type paletteState struct {
	open   bool
	query  string
	active string // the row under the keyboard, by key
	reveal bool   // scroll the active row into view
}

// paletteResult is a row of the palette.
type paletteResult struct {
	key      string
	title    string
	subtitle string
	shortcut *chord
	trailing string
	icon     *ui.SVG
	group    string // the heading the row sits under; runs of one form a group
	run      func()
}

// maxSessionResults caps the chats a query lists.
const maxSessionResults = 8

func (a *App) setPaletteOpen(open bool) {
	a.palette.open = open
	if !open {
		a.palette.query = ""
	}
}

func (a *App) paletteResults() []paletteResult {
	var cmds []command
	for _, cmd := range a.commands() {
		if cmd.when {
			cmds = append(cmds, cmd)
		}
	}
	toResult := func(cmd command, grouped bool) paletteResult {
		id := cmd.id
		r := paletteResult{key: "command:" + id, title: cmd.title, icon: cmd.icon, run: func() {
			a.setPaletteOpen(false)
			a.runCommand(id)
		}}
		if len(cmd.keys) > 0 {
			r.shortcut = &cmd.keys[0]
		}
		if grouped {
			r.group = cmd.category.label()
		}
		return r
	}
	query := strings.TrimSpace(a.palette.query)
	if query == "" {
		col := collate.New(language.Make(i18n.Locale()))
		sort.SliceStable(cmds, func(i, j int) bool {
			if cmds[i].category != cmds[j].category {
				return cmds[i].category < cmds[j].category
			}
			return col.CompareString(cmds[i].title, cmds[j].title) < 0
		})
		out := make([]paletteResult, len(cmds))
		for i, cmd := range cmds {
			out[i] = toResult(cmd, true)
		}
		return out
	}

	type scored struct {
		r     paletteResult
		score int
	}
	var results, chats []scored
	for _, cmd := range cmds {
		if score, ok := fuzzyMatch(query, cmd.title+" "+cmd.id); ok {
			results = append(results, scored{toResult(cmd, false), score})
		}
	}
	now := time.Now()
	for i := range a.catalog.Sessions {
		s := &a.catalog.Sessions[i]
		if s.Archived {
			continue
		}
		project := a.projectName(s.ProjectID)
		best, found := 0, false
		for _, text := range []string{s.Title, project, s.BranchName} {
			if text == "" {
				continue
			}
			if score, ok := fuzzyMatch(query, text); ok && (!found || score > best) {
				best, found = score, true
			}
		}
		if !found {
			continue
		}
		subtitle := project
		if branch := s.BranchName; branch != "" && strings.Contains(s.ProjectID, ":local:") {
			subtitle = strings.TrimPrefix(project+" · "+branch, " · ")
		}
		at := s.LastMessageAt
		if at == 0 {
			if t, err := time.Parse(time.RFC3339, s.CreatedAt); err == nil {
				at = float64(t.UnixMilli())
			}
		}
		trailing := ""
		if at > 0 {
			trailing = compactAge(time.UnixMilli(int64(at)), now)
		}
		id := s.ID
		chats = append(chats, scored{paletteResult{
			key: "session:" + id, title: firstNonEmpty(strings.TrimSpace(s.Title), locales.UntitledSession()), subtitle: subtitle, trailing: trailing,
			icon: iconMessageSquare, run: func() {
				a.setPaletteOpen(false)
				a.open(id)
			},
		}, best})
	}
	sort.SliceStable(chats, func(i, j int) bool { return chats[i].score > chats[j].score })
	results = append(results, chats[:min(len(chats), maxSessionResults)]...)
	sort.SliceStable(results, func(i, j int) bool { return results[i].score > results[j].score })
	out := make([]paletteResult, len(results))
	for i, s := range results {
		out[i] = s.r
	}
	return out
}

// fuzzyMatch scores text for a query, as Lody's palette: a prefix ranks
// first, shorter text first; then a substring, earlier first; then the
// query's characters in order, runs and early hits scoring more.
func fuzzyMatch(query, text string) (int, bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return 0, true
	}
	t := strings.ToLower(text)
	if i := strings.Index(t, q); i == 0 {
		return 1000 - utf8.RuneCountInString(text), true
	} else if i > 0 {
		return 600 - utf8.RuneCountInString(t[:i]), true
	}
	qr, tr := []rune(q), []rune(t)
	qi, score, prev := 0, 0, -2
	for ti := 0; ti < len(tr) && qi < len(qr); ti++ {
		if tr[ti] == qr[qi] {
			if prev == ti-1 {
				score += 8
			}
			score += max(0, 12-ti)
			prev = ti
			qi++
		}
	}
	if qi < len(qr) {
		return 0, false
	}
	return score, true
}

// compactAge is how long ago, in Lody's compact units: 5m, 3h, 2d, 1w,
// 4mo, 1y.
func compactAge(t, now time.Time) string {
	minutes := int(math.Max(0, now.Sub(t).Minutes()))
	hours, days := minutes/60, minutes/60/24
	switch {
	case minutes < 60:
		return fmt.Sprintf("%dm", minutes)
	case hours < 24:
		return fmt.Sprintf("%dh", hours)
	case days < 7:
		return fmt.Sprintf("%dd", days)
	case days < 30:
		return fmt.Sprintf("%dw", days/7)
	case days < 365:
		return fmt.Sprintf("%dmo", days/30)
	}
	return fmt.Sprintf("%dy", days/365)
}

// paletteKey moves the active row, runs it or closes the palette.
func (a *App) paletteKey(k ui.TextKey, results []paletteResult) bool {
	pal := &a.palette
	index := -1
	for i, r := range results {
		if r.key == pal.active {
			index = i
		}
	}
	move := func(i int) bool {
		if len(results) > 0 {
			pal.active, pal.reveal = results[i].key, true
		}
		return true
	}
	n := len(results)
	ctrl := k.Mods == ui.Ctrl
	switch {
	case k.Key == ui.KeyEscape:
		a.setPaletteOpen(false)
		return true
	case k.Mods == 0 && k.Key == ui.KeyDown, ctrl && (k.Key == ui.KeyN || k.Key == ui.KeyJ):
		return n == 0 || move((index+1)%n)
	case k.Mods == 0 && k.Key == ui.KeyUp, ctrl && (k.Key == ui.KeyP || k.Key == ui.KeyK):
		return n == 0 || move((index-1+n)%n)
	case k.Mods == 0 && k.Key == ui.KeyHome:
		return n == 0 || move(0)
	case k.Mods == 0 && k.Key == ui.KeyEnd:
		return n == 0 || move(n-1)
	case k.Mods == 0 && k.Key == ui.KeyEnter:
		if index >= 0 {
			results[index].run()
		}
		return true
	}
	return false
}

// paletteView hangs the palette from a fixed top edge, so the field stays
// put as the list under it grows and shrinks with the query.
func (a *App) paletteView(c *ui.Context, p *palette) {
	pal := &a.palette
	if !pal.open {
		return
	}
	results := a.paletteResults()
	if !slicesHasKey(results, pal.active) {
		pal.active = ""
		if len(results) > 0 {
			pal.active = results[0].key
		}
	}
	w, h := c.Size()
	width := min(640, w-32)
	maxH := min(560, h*0.76)
	open := pal.open
	ui.DialogBase(c, &open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.3))
		panel.Absolute().Top(h*0.14).Left((w-width)/2).Width(width).MaxHeight(maxH).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Clip().
			Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Column(c).FillWidth().MaxHeight(maxH).Children(func() {
			ui.Row(c).FillWidth().Height(52).Shrink(0).Padding(0, 16).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconSearch).Size(16, 16).TextColor(p.Tertiary).Shrink(0)
				ui.TextInputBase(c.Key("palette-query"), &pal.query).FontSize(15).TextColor(p.Text).Placeholder(locales.CmdPalettePlaceholder()).
					Grow(1).MinWidth(0).AutoFocus().Label(locales.CmdPalettePlaceholder()).
					OnTextKey(func(k ui.TextKey) bool { return a.paletteKey(k, results) }).
					OnChange(func() { pal.active = "" })
			})
			ui.Box(c).FillWidth().Height(1).Shrink(0).Background(p.Border)
			list := ui.Scroll(c.Key("palette-list")).FillWidth().MaxHeight(maxH - 52 - 1 - 34 - 1).Padding(6)
			list.Children(func() {
				if len(results) == 0 {
					ui.Column(c).FillWidth().Padding(36, 16).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, iconSearch).Size(20, 20).TextColor(p.Tertiary)
						ui.Text(c, locales.CmdPaletteEmpty()).FontSize(13).TextColor(p.Secondary)
					})
					return
				}
				for start := 0; start < len(results); {
					end := start + 1
					for end < len(results) && results[end].group == results[start].group {
						end++
					}
					run := results[start:end]
					group := ui.Column(c.Key("group:" + run[0].key)).FillWidth()
					if run[0].group != "" {
						group.Padding(0, 0, 4, 0)
					}
					group.Children(func() {
						if run[0].group != "" {
							ui.Text(c, run[0].group).FontSize(11).FontWeight(500).LineHeight(16.0/11).TextColor(p.Tertiary).Padding(8, 8, 4, 8)
						}
						for _, r := range run {
							a.paletteRow(c.Key(r.key), p, r, r.key == pal.active, width)
						}
					})
					start = end
				}
			})
			ui.Box(c).FillWidth().Height(1).Shrink(0).Background(p.Border)
			ui.Row(c).FillWidth().Height(34).Shrink(0).Padding(0, 16).Gap(16).AlignItems(ui.Center).Children(func() {
				hint := func(key, label string) {
					ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
						ui.Text(c, key).FontSize(11.5).TextColor(p.Secondary)
						ui.Text(c, label).FontSize(11.5).TextColor(p.Tertiary)
					})
				}
				hint("↑↓", locales.CmdHintNavigate())
				hint("↵", locales.CmdHintSelect())
				ui.Spacer(c)
				hint("esc", locales.CmdHintClose())
			})
		})
	})
	if !open {
		a.setPaletteOpen(false)
	}
	pal.reveal = false
}

func (a *App) paletteRow(c *ui.Context, p *palette, r paletteResult, active bool, width float32) {
	row := ui.Row(c).FillWidth().Height(36).Padding(0, 8).Radius(10).Gap(10).AlignItems(ui.Center).Label(r.title).Transition(colorFade)
	if active {
		row.Background(p.Text.Alpha(0.07))
		if a.palette.reveal {
			row.ScrollIntoView()
		}
	}
	// The pointer selects the row it moves over, as cmdk's.
	if row.Hovered() && !active {
		a.palette.active = r.key
		a.redraw()
	}
	row.OnClick(r.run)
	row.Children(func() {
		ink := p.Secondary
		if active {
			ink = p.Text
		}
		ui.Box(c).Size(18, 18).Center().Shrink(0).Children(func() {
			if r.icon != nil {
				ui.Icon(c, r.icon).Size(16, 16).TextColor(ink)
			}
		})
		ui.Text(c, r.title).FontSize(13.5).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
		switch {
		case r.shortcut != nil:
			ui.Row(c).Gap(3).Shrink(0).Children(func() {
				for _, key := range r.shortcut.parts() {
					ui.Text(c, key).FontSize(12).LetterSpacing(0.24).FontFeatures("tnum").TextColor(p.Tertiary)
				}
			})
		case r.subtitle != "" || r.trailing != "":
			ui.Row(c).Gap(8).Shrink(0).MaxWidth(width * 0.45).AlignItems(ui.Center).Children(func() {
				if r.subtitle != "" {
					ui.Text(c, r.subtitle).FontSize(12).TextColor(p.Tertiary).SingleLine().MinWidth(0).Shrink(1)
				}
				if r.trailing != "" {
					ui.Text(c, r.trailing).FontSize(12).FontFeatures("tnum").TextColor(p.Tertiary).Shrink(0)
				}
			})
		}
	})
}

func slicesHasKey(results []paletteResult, key string) bool {
	for _, r := range results {
		if r.key == key {
			return true
		}
	}
	return false
}
