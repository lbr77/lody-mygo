package app

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// Class and ER diagrams lay out as flowcharts whose nodes are boxes: a
// name over its members, with marks at the ends of their relationships.
// XY charts draw their bars and lines on one axis.

// boxMembers is the size of a box's member lines.
func boxMembers(members []string, size float32) (w, h float32) {
	if len(members) == 0 {
		return 0, 0
	}
	for _, m := range members {
		mw, mh := diagramText(m, ui.Font{Family: fontUI, Size: size * 0.85}, 0).Size()
		w, h = max(w, mw), h+mh+size*0.2
	}
	return w, h + size*0.6
}

func drawBox(p diagramPen, n *mmNode, r ui.Rect, size float32, ink, line, fill ui.Color) {
	p.Fill(r, fill, 4)
	p.Stroke(r, line, 4, 1.2)
	head := diagramText(n.label, ui.Font{Family: fontUI, Size: size, Weight: 600}, 220)
	hw, hh := head.Size()
	headH := hh + size
	p.TextLayout(head, r.X+(r.W-hw)/2, r.Y+size/2, ink)
	if len(n.members) == 0 {
		return
	}
	seg(p, r.X, r.Y+headH, r.X+r.W, r.Y+headH, 1, line)
	y := r.Y + headH + size*0.3
	for _, m := range n.members {
		l := diagramText(m, ui.Font{Family: fontUI, Size: size * 0.85}, 0)
		_, mh := l.Size()
		p.TextLayout(l, r.X+size*0.8, y, ink)
		y += mh + size*0.2
	}
}

// endMark draws a relationship's mark where it meets a box at (x, y),
// coming from (fx, fy), and its cardinality beside it.
func endMark(p diagramPen, mark, text string, x, y, fx, fy, size float32, ink, line, fill ui.Color) {
	if mark == "" && text == "" {
		return
	}
	ang := math.Atan2(float64(fy-y), float64(fx-x)) // along the line, away from the box
	ux, uy := float32(math.Cos(ang)), float32(math.Sin(ang))
	px, py := -uy, ux
	at := func(d, side float32) (float32, float32) { return x + ux*d + px*side, y + uy*d + py*side }
	l := size * 0.7
	switch mark {
	case "arrow":
		p.FillPath(arrowHead(x, y, ang+math.Pi, float64(l)), line)
	case "triangle", "diamond", "odiamond":
		pts := [][2]float32{{x, y}}
		if mark == "triangle" {
			ax, ay := at(l, l*0.55)
			bx, by := at(l, -l*0.55)
			pts = append(pts, [2]float32{ax, ay}, [2]float32{bx, by})
		} else {
			ax, ay := at(l*0.7, l*0.45)
			bx, by := at(l*1.4, 0)
			cx, cy := at(l*0.7, -l*0.45)
			pts = append(pts, [2]float32{ax, ay}, [2]float32{bx, by}, [2]float32{cx, cy})
		}
		path := polygon(pts...)
		if mark == "diamond" {
			p.FillPath(path, line)
		} else {
			p.FillPath(path, fill)
			p.StrokePath(path, 1.2, line)
		}
	case "one", "zeroone", "many", "zeromany":
		// Crow's foot notation: the inner mark says how many at most, the
		// outer one at least.
		bar := func(d float32) {
			ax, ay := at(d, l*0.5)
			bx, by := at(d, -l*0.5)
			seg(p, ax, ay, bx, by, 1.2, line)
		}
		if mark == "many" || mark == "zeromany" {
			fx, fy := at(l, 0)
			for _, side := range []float32{l * 0.55, 0, -l * 0.55} {
				ex, ey := at(0, side)
				seg(p, fx, fy, ex, ey, 1.2, line)
			}
		} else {
			bar(l * 0.5)
		}
		if mark == "one" || mark == "many" {
			bar(l * 1.1)
		} else {
			cx, cy := at(l*1.4, 0)
			r := l * 0.3
			o := ui.Rect{X: cx - r, Y: cy - r, W: 2 * r, H: 2 * r}
			p.Fill(o, fill, r)
			p.Stroke(o, line, r, 1.2)
		}
	}
	if text != "" {
		lay := diagramText(text, ui.Font{Family: fontUI, Size: size * 0.8}, 0)
		w, h := lay.Size()
		tx, ty := at(l*1.9, l*0.9)
		p.TextLayout(lay, tx-w/2, ty-h/2, ink)
	}
}

var (
	classRelRe    = regexp.MustCompile(`^([A-Za-z0-9_~<>.]+)\s*(?:"([^"]*)")?\s*(<\|--|--\|>|\*--|--\*|o--|--o|<--|-->|<\.\.|\.\.>|<\|\.\.|\.\.\|>|--|\.\.)\s*(?:"([^"]*)")?\s*([A-Za-z0-9_~<>.]+)\s*(?::\s*(.*))?$`)
	classMemberRe = regexp.MustCompile(`^([A-Za-z0-9_~<>.]+)\s*:\s*(.+)$`)
	erRelRe       = regexp.MustCompile(`^([A-Za-z0-9_\-"]+)\s+([|}o][|o])(--|\.\.)([|o][|{o])\s+([A-Za-z0-9_\-"]+)\s*:\s*(.*)$`)
)

// generic writes Name~T~ as Name<T>.
func generic(s string) string {
	if i := strings.IndexByte(s, '~'); i >= 0 {
		if j := strings.LastIndexByte(s, '~'); j > i {
			return s[:i] + "<" + s[i+1:j] + ">" + s[j+1:]
		}
	}
	return s
}

func newBoxChart() *flowchart { return &flowchart{dir: "TD", byID: map[string]*mmNode{}} }

// box is a class or entity by name; a class's generic, Name~T~, is the
// same class as Name.
func (fc *flowchart) box(name string) *mmNode {
	name = strings.Trim(name, `"`)
	id, _, _ := strings.Cut(name, "~")
	if n := fc.byID[id]; n != nil {
		if name != id && !strings.Contains(n.label, "<") {
			n.label = strings.Replace(n.label, id, generic(name), 1)
		}
		return n
	}
	n := &mmNode{id: id, label: generic(name), shape: "box"}
	fc.byID[id] = n
	fc.nodes = append(fc.nodes, n)
	return n
}

// parseClass reads a classDiagram: classes with their members, and the
// relationships between them.
func parseClass(src string) (*flowchart, bool) {
	fc := newBoxChart()
	var open *mmNode
	for i, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if i == 0 || line == "" || strings.HasPrefix(line, "%%") || strings.HasPrefix(line, "note") || strings.HasPrefix(line, "classDef") || strings.HasPrefix(line, "style") || strings.HasPrefix(line, "cssClass") || strings.HasPrefix(line, "click") {
			continue
		}
		if strings.HasPrefix(line, "direction") {
			if f := strings.Fields(line); len(f) == 2 && (f[1] == "LR" || f[1] == "RL") {
				fc.dir = "LR"
			}
			continue
		}
		if open != nil {
			if line == "}" {
				open = nil
				continue
			}
			if strings.HasPrefix(line, "<<") {
				open.label = line + "\n" + open.label
				continue
			}
			open.members = append(open.members, generic(line))
			continue
		}
		if rest, ok := strings.CutPrefix(line, "class "); ok {
			rest = strings.TrimSpace(rest)
			body := strings.HasSuffix(rest, "{")
			name := strings.Fields(strings.TrimSuffix(rest, "{"))
			if len(name) == 0 {
				return nil, false
			}
			n := fc.box(name[0])
			if body {
				open = n
			}
			continue
		}
		if strings.HasPrefix(line, "<<") {
			// <<interface>> Name
			if j := strings.Index(line, ">>"); j >= 0 {
				if name := strings.TrimSpace(line[j+2:]); name != "" {
					n := fc.box(name)
					n.label = line[:j+2] + "\n" + n.label
				}
			}
			continue
		}
		if m := classRelRe.FindStringSubmatch(line); m != nil {
			a, b := fc.box(m[1]), fc.box(m[5])
			e := mmEdge{from: a.id, to: b.id, label: strings.TrimSpace(m[6]), fromText: m[2], toText: m[4]}
			op := m[3]
			e.dashed = strings.Contains(op, "..")
			switch op {
			case "<|--", "<|..":
				e.fromMark = "triangle"
			case "--|>", "..|>":
				e.toMark = "triangle"
			case "*--":
				e.fromMark = "diamond"
			case "--*":
				e.toMark = "diamond"
			case "o--":
				e.fromMark = "odiamond"
			case "--o":
				e.toMark = "odiamond"
			case "<--", "<..":
				e.fromMark = "arrow"
			case "-->", "..>":
				e.toMark = "arrow"
			}
			fc.edges = append(fc.edges, e)
			continue
		}
		if m := classMemberRe.FindStringSubmatch(line); m != nil {
			n := fc.box(m[1])
			n.members = append(n.members, generic(strings.TrimSpace(m[2])))
			continue
		}
		return nil, false
	}
	return fc, len(fc.nodes) > 0 && len(fc.nodes)+len(fc.edges) <= mermaidMaxItems
}

// erMark is an ER cardinality's mark.
func erMark(tok string) string {
	switch tok {
	case "||":
		return "one"
	case "|o", "o|":
		return "zeroone"
	case "}|", "|{":
		return "many"
	}
	return "zeromany"
}

// parseER reads an erDiagram: entities with their attributes, and the
// relationships between them in crow's foot notation.
func parseER(src string) (*flowchart, bool) {
	fc := newBoxChart()
	var open *mmNode
	for i, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if i == 0 || line == "" || strings.HasPrefix(line, "%%") || strings.HasPrefix(line, "style") || strings.HasPrefix(line, "classDef") {
			continue
		}
		if strings.HasPrefix(line, "direction") {
			if f := strings.Fields(line); len(f) == 2 && (f[1] == "LR" || f[1] == "RL") {
				fc.dir = "LR"
			}
			continue
		}
		if open != nil {
			if line == "}" {
				open = nil
				continue
			}
			// type name [PK|FK|UK] ["comment"]
			f := strings.Fields(line)
			if len(f) >= 2 {
				member := f[0] + " " + f[1]
				for _, k := range f[2:] {
					if !strings.HasPrefix(k, `"`) {
						member += " " + k
					}
				}
				open.members = append(open.members, member)
			}
			continue
		}
		if strings.HasSuffix(line, "{") {
			open = fc.box(strings.TrimSpace(strings.TrimSuffix(line, "{")))
			continue
		}
		if m := erRelRe.FindStringSubmatch(line); m != nil {
			a, b := fc.box(m[1]), fc.box(m[5])
			fc.edges = append(fc.edges, mmEdge{from: a.id, to: b.id, label: strings.Trim(strings.TrimSpace(m[6]), `"`),
				dashed: m[3] == "..", fromMark: erMark(m[2]), toMark: erMark(m[4])})
			continue
		}
		if f := strings.Fields(line); len(f) == 1 {
			fc.box(f[0])
			continue
		}
		return nil, false
	}
	return fc, len(fc.nodes) > 0 && len(fc.nodes)+len(fc.edges) <= mermaidMaxItems
}

// xyChart is an xychart-beta: categories along x, a value axis, and bar
// and line series.
type xyChart struct {
	title       string
	horizontal  bool
	xLabels     []string
	yLabel      string
	yMin, yMax  float64
	yRange      bool
	bars, lines [][]float64
}

var (
	xyListRe  = regexp.MustCompile(`\[(.*)\]`)
	xyRangeRe = regexp.MustCompile(`(-?[0-9.]+)\s*-->\s*(-?[0-9.]+)`)
)

func xyNumbers(s string) ([]float64, bool) {
	m := xyListRe.FindStringSubmatch(s)
	if m == nil {
		return nil, false
	}
	var out []float64
	for _, f := range strings.Split(m[1], ",") {
		v, err := strconv.ParseFloat(strings.TrimSpace(f), 64)
		if err != nil {
			return nil, false
		}
		out = append(out, v)
	}
	return out, len(out) > 0
}

func parseXYChart(src string) (*xyChart, bool) {
	xc := &xyChart{}
	for i, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		if i == 0 {
			xc.horizontal = strings.Contains(line, "horizontal")
			continue
		}
		word, rest, _ := strings.Cut(line, " ")
		rest = strings.TrimSpace(rest)
		switch word {
		case "title":
			xc.title = strings.Trim(rest, `"`)
		case "x-axis":
			if m := xyListRe.FindStringSubmatch(rest); m != nil {
				for _, f := range strings.Split(m[1], ",") {
					xc.xLabels = append(xc.xLabels, strings.Trim(strings.TrimSpace(f), `"`))
				}
			}
		case "y-axis":
			if m := xyRangeRe.FindStringSubmatch(rest); m != nil {
				xc.yMin, _ = strconv.ParseFloat(m[1], 64)
				xc.yMax, _ = strconv.ParseFloat(m[2], 64)
				xc.yRange = true
				rest = rest[:strings.Index(rest, m[0])]
			}
			xc.yLabel = strings.Trim(strings.TrimSpace(rest), `"`)
		case "bar", "line":
			v, ok := xyNumbers(rest)
			if !ok {
				return nil, false
			}
			if word == "bar" {
				xc.bars = append(xc.bars, v)
			} else {
				xc.lines = append(xc.lines, v)
			}
		default:
			return nil, false
		}
	}
	n := 0
	for _, s := range append(append([][]float64{}, xc.bars...), xc.lines...) {
		n = max(n, len(s))
		if !xc.yRange {
			for _, v := range s {
				xc.yMax = max(xc.yMax, v)
				xc.yMin = min(xc.yMin, v)
			}
		}
	}
	for len(xc.xLabels) < n {
		xc.xLabels = append(xc.xLabels, strconv.Itoa(len(xc.xLabels)+1))
	}
	if !xc.yRange && xc.yMax > 0 {
		xc.yMax = niceCeil(xc.yMax)
	}
	return xc, n > 0 && xc.yMax > xc.yMin && n <= mermaidMaxItems
}

// niceCeil rounds up to 1, 2 or 5 times a power of ten.
func niceCeil(v float64) float64 {
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 5, 10} {
		if m*p >= v {
			return m * p
		}
	}
	return v
}

const xyPlotW, xyPlotH = 34.0, 16.0 // the plot's size, in font sizes

func (xc *xyChart) size(size float32) (w, h float32) {
	w, h = size*xyPlotW, size*xyPlotH
	if xc.title != "" {
		h += size * 2
	}
	return w, h
}

func (xc *xyChart) draw(p diagramPen, ox, oy, size float32, ink, line, accent ui.Color) {
	font := ui.Font{Family: fontUI, Size: size * 0.8}
	top := oy
	if xc.title != "" {
		t := diagramText(xc.title, ui.Font{Family: fontUI, Size: size, Weight: 600}, 0)
		tw, _ := t.Size()
		p.TextLayout(t, ox+(size*xyPlotW-tw)/2, oy, ink)
		top += size * 2
	}
	left, bottom := ox+size*4, top+size*(xyPlotH-2.5)
	right := ox + size*xyPlotW - size
	plotW, plotH := right-left, bottom-top
	val := func(v float64) float32 {
		return bottom - float32((v-xc.yMin)/(xc.yMax-xc.yMin))*plotH
	}
	// Value axis: five ticks with their gridlines.
	for i := 0; i <= 4; i++ {
		v := xc.yMin + (xc.yMax-xc.yMin)*float64(i)/4
		y := val(v)
		seg(p, left, y, right, y, 0.6, line.Alpha(0.35))
		lay := diagramText(trimNumber(v), font, 0)
		w, h := lay.Size()
		p.TextLayout(lay, left-w-size*0.4, y-h/2, ink)
	}
	if xc.yLabel != "" {
		lay := diagramText(xc.yLabel, font, 0)
		p.TextLayout(lay, ox, top-size*1.2, ink)
	}
	n := len(xc.xLabels)
	slot := plotW / float32(n)
	for i, label := range xc.xLabels {
		lay := diagramText(label, font, slot)
		w, _ := lay.Size()
		p.TextLayout(lay, left+slot*float32(i)+(slot-w)/2, bottom+size*0.4, ink)
	}
	seg(p, left, bottom, right, bottom, 1, line)
	if len(xc.bars) > 0 {
		group := slot * 0.7
		bw := group / float32(len(xc.bars))
		for si, s := range xc.bars {
			c := accent.Alpha(1 - 0.25*float32(si))
			for i, v := range s {
				x := left + slot*float32(i) + (slot-group)/2 + bw*float32(si)
				y := val(max(v, xc.yMin))
				p.Fill(ui.Rect{X: x, Y: y, W: bw - 1, H: bottom - y}, c, 2)
			}
		}
	}
	for si, s := range xc.lines {
		c := ink.Alpha(0.85 - 0.2*float32(si))
		var pts []float32
		for i, v := range s {
			pts = append(pts, left+slot*float32(i)+slot/2, val(v))
		}
		for i := 0; i+3 < len(pts); i += 2 {
			seg(p, pts[i], pts[i+1], pts[i+2], pts[i+3], 1.6, c)
		}
		for i := 0; i+1 < len(pts); i += 2 {
			p.Fill(ui.Rect{X: pts[i] - 2.5, Y: pts[i+1] - 2.5, W: 5, H: 5}, c, 2.5)
		}
	}
}

func trimNumber(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "-0" {
		return "0"
	}
	return s
}
