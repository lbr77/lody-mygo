package app

import (
	"regexp"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"

	"github.com/lbr77/lody-mygo/locales"
)

// A table in a reply has Lody's copy button over its top end corner: it
// fades in over 150ms, as Tailwind's transition-opacity, while the pointer
// is on the table, and copies the table as GitHub's Markdown, its cells'
// text with the pipes in it escaped, saying so for 1.5s. MyGo's Markdown
// draws a table in with the rest, so a finished reply with tables at its
// top level is shown in stretches: each table on its own, in a frame that
// fits it, and what lies between, spaced as one view spaces its blocks.

// mdStretch is a stretch of a reply's Markdown: one table, or the blocks
// between tables. first and last are the levels of the headings it starts
// and ends with, 0 when it doesn't.
type mdStretch struct {
	src         string
	table       bool
	first, last int
	copy        string // a table's Markdown, as Lody copies it
}

// mdDefinitions finds what a stretch could need from another: link
// reference and footnote definitions.
var mdDefinitions = regexp.MustCompile(`(?m)^ {0,3}\[[^\]]+\]:`)

// splitMarkdownTables is src in stretches around its top-level tables, or
// nil when it has none, or defines what its stretches would share.
func splitMarkdownTables(src string) []mdStretch {
	if !strings.Contains(src, "|") || mdDefinitions.MatchString(src) {
		return nil
	}
	d := parser.Parse(src)
	var segs []mdStretch
	tables := false
	start, end, first, last := -1, -1, 0, 0
	flush := func() {
		if start >= 0 {
			segs = append(segs, mdStretch{src: src[start:end], first: first, last: last})
			start = -1
		}
	}
	for j := range d.Children(0) {
		b := d.Block(j)
		if b.Kind == parser.Table {
			flush()
			tables = true
			segs = append(segs, mdStretch{src: src[b.Start:b.End], table: true, copy: tableMarkdown(d, j)})
			continue
		}
		level := 0
		if b.Kind == parser.Heading {
			level = b.Level
		}
		if start < 0 {
			start, first = b.Start, level
		}
		end, last = b.End, level
	}
	flush()
	if !tables {
		return nil
	}
	return segs
}

// tableMarkdown is table i of d as GitHub's Markdown, as Lody's copy button
// writes it: the header row, a delimiter row, then the body, every row as
// wide as the widest.
func tableMarkdown(d *parser.Document, i int) string {
	var rows [][]string
	width := 0
	for r := range d.Children(i) {
		var cells []string
		for cell := range d.Children(r) {
			text := strings.Join(strings.Fields(inlineText(d, d.Inlines(cell))), " ")
			cells = append(cells, strings.ReplaceAll(text, "|", `\|`))
		}
		width = max(width, len(cells))
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return ""
	}
	line := func(cells []string) string {
		out := make([]string, width)
		copy(out, cells)
		return "| " + strings.Join(out, " | ") + " |"
	}
	delim := make([]string, width)
	for k := range delim {
		delim[k] = "---"
	}
	lines := []string{line(rows[0]), line(delim)}
	for _, r := range rows[1:] {
		lines = append(lines, line(r))
	}
	return strings.Join(lines, "\n")
}

// inlineText is what inlines read as, as a cell's textContent: their text,
// code and math, without the markup around them or raw HTML.
func inlineText(d *parser.Document, inl []parser.Inline) string {
	var b strings.Builder
	for _, in := range inl {
		switch in.Kind {
		case parser.Text, parser.Code, parser.Math:
			b.WriteString(in.Text)
		case parser.SoftBreak, parser.LineBreak:
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// segmentedMarkdown shows a finished reply in its stretches, each table
// with its copy button.
func (a *App) segmentedMarkdown(c *ui.Context, p *palette, key string, segs []mdStretch) ui.Element {
	th := a.md.Theme
	em := func(v float32) float32 { return v * th.FontSize }
	spacing := th.BlockSpacing
	if spacing == 0 {
		spacing = 0.75
	}
	heading := func(level int) markdown.HeadingStyle {
		h := th.Headings[level-1]
		if h.Top == 0 && h.Bottom == 0 {
			h.Top, h.Bottom = 1.4, 0.55
		}
		return h
	}
	col := ui.Column(c.Key(key)).FillWidth()
	col.Children(func() {
		for i, seg := range segs {
			var el ui.Element
			if seg.table {
				el = a.tableFrame(c, p, key, i, seg)
			} else {
				el = markdown.View(c.Key(i), seg.src, &a.md)
			}
			if i == 0 {
				continue
			}
			// As one view spaces its blocks: a heading's own room below
			// and above it in place of the space between blocks.
			top := em(spacing)
			if prev := segs[i-1].last; prev > 0 && !segs[i-1].table {
				top = em(heading(prev).Bottom)
			}
			if seg.first > 0 && !seg.table {
				top = max(top, em(heading(seg.first).Top))
			}
			el.Margin(top, 0, 0, 0)
		}
	})
	return col
}

// tableFrame is a table fitted by a frame, its copy button over the frame's
// top end corner.
func (a *App) tableFrame(c *ui.Context, p *palette, key string, i int, seg mdStretch) ui.Element {
	frame := ui.Column(c.Key(i)).AlignSelf(ui.Start).MaxWidthPercent(100)
	id := key + "/table/" + seg.copy
	if a.copiedAt == nil {
		a.copiedAt = map[string]time.Time{}
	}
	at := a.copiedAt[id]
	copied := !at.IsZero() && c.Now().Sub(at) < 1500*time.Millisecond
	frame.Children(func() {
		markdown.View(c.Key("table"), seg.src, &a.md)
		label := locales.CopyTable()
		icon := iconCopy
		if copied {
			label, icon = locales.Copied(), iconCheck
			c.After(1500*time.Millisecond - c.Now().Sub(at))
		}
		b := ui.ButtonBase(c.Key("copy")).Absolute().Top(6).Right(6).Size(24, 24).Radius(6).Center().Border(1, p.Text.Alpha(0.1)).
			Background(p.Canvas.Alpha(0.9)).Cursor(ui.CursorPointer).Label(label)
		shown := frame.Hovered() || b.FocusVisible() || copied
		b.Opacity(b.AnimateWith("shown", b2f(shown), 150*time.Millisecond, twEase))
		ink := p.Secondary
		if b.Hovered() {
			ink = p.Text
		}
		ink = b.AnimateColor("ink", ink, ui.Animation{Duration: 150 * time.Millisecond, Ease: twEase})
		b.Children(func() { ui.Icon(c, icon).Size(14, 14).TextColor(ink) })
		tip(c, p, b, label).OnClick(func() {
			writeClipboard(seg.copy)
			a.copiedAt[id] = time.Now()
		})
	})
	return frame
}
