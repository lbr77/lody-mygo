package app

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/lbr77/lody-mygo/locales"
)

// Mermaid diagrams, drawn natively as lody-ios draws them once the block
// is finished: flowcharts in ranks along their direction, and sequence
// diagrams of participants and the messages between them. What it cannot
// draw, or a block still being written, stays code.

const (
	mermaidMaxBytes = 12 << 10
	mermaidMaxLines = 128
	mermaidMaxItems = 128
)

type mmNode struct {
	id, label, shape string   // shape: rect, round, diamond, circle, dot, or box: a class or entity
	members          []string // a box's lines under its name
	rank, order      int
	x, y, w, h       float32
}

type mmEdge struct {
	from, to, label string
	dashed, arrow   bool
	// Marks at each end, for class and ER relationships: arrow, triangle,
	// diamond, odiamond; one, zeroone, many, zeromany. fromText and toText
	// are a class relationship's cardinalities.
	fromMark, toMark string
	fromText, toText string
}

type flowchart struct {
	dir   string // TD or LR
	nodes []*mmNode
	byID  map[string]*mmNode
	edges []mmEdge
}

var (
	mmNodeRe = regexp.MustCompile(`^([A-Za-z0-9_\-]+)\s*(\(\((.*?)\)\)|\[\[(.*?)\]\]|\[\((.*?)\)\]|\[(.*?)\]|\((.*?)\)|\{(.*?)\}|>(.*?)\])?`)
	mmArrow  = regexp.MustCompile(`^\s*(-->|---|-\.->|-\.-|==>|===|--x|--o)\s*(?:\|([^|]*)\|)?\s*`)
	mmLabel  = regexp.MustCompile(`^\s*--\s*([^-]+?)\s*(-->|---)\s*`)
)

// parseFlowchart reads graph/flowchart source, false for what it does
// not take.
func parseFlowchart(src string) (*flowchart, bool) {
	lines := strings.Split(src, "\n")
	fc := &flowchart{dir: "TD", byID: map[string]*mmNode{}}
	header := true
	node := func(s string) (*mmNode, string, bool) {
		m := mmNodeRe.FindStringSubmatchIndex(s)
		if m == nil {
			return nil, s, false
		}
		id := s[m[2]:m[3]]
		label, shape := "", "rect"
		for i, sh := range []string{"circle", "rect", "round", "rect", "round", "diamond", "rect"} {
			if m[6+2*i] >= 0 {
				label, shape = s[m[6+2*i]:m[7+2*i]], sh
			}
		}
		label = strings.Trim(strings.TrimSpace(label), `"`)
		n := fc.byID[id]
		if n == nil {
			n = &mmNode{id: id, label: id, shape: "rect"}
			fc.byID[id] = n
			fc.nodes = append(fc.nodes, n)
		}
		if label != "" {
			n.label, n.shape = label, shape
		}
		return n, s[m[1]:], true
	}
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		if header {
			header = false
			f := strings.Fields(line)
			if len(f) > 1 {
				switch strings.ToUpper(f[1]) {
				case "LR", "RL":
					fc.dir = "LR"
				}
			}
			continue
		}
		if strings.HasPrefix(line, "classDef") || strings.HasPrefix(line, "class ") || strings.HasPrefix(line, "style ") ||
			strings.HasPrefix(line, "linkStyle") || strings.HasPrefix(line, "click ") || line == "end" || strings.HasPrefix(line, "subgraph") || strings.HasPrefix(line, "direction") {
			continue
		}
		for _, stmt := range strings.Split(line, ";") {
			rest := strings.TrimSpace(stmt)
			if rest == "" {
				continue
			}
			from, after, ok := node(rest)
			if !ok {
				return nil, false
			}
			rest = after
			for strings.TrimSpace(rest) != "" {
				label := ""
				var op string
				if m := mmLabel.FindStringSubmatch(rest); m != nil {
					label, op = m[1], m[2]
					rest = rest[len(m[0]):]
				} else if m := mmArrow.FindStringSubmatch(rest); m != nil {
					op, label = m[1], m[2]
					rest = rest[len(m[0]):]
				} else {
					return nil, false
				}
				to, after, ok := node(strings.TrimSpace(rest))
				if !ok {
					return nil, false
				}
				fc.edges = append(fc.edges, mmEdge{from: from.id, to: to.id, label: strings.TrimSpace(label),
					dashed: strings.Contains(op, "."), arrow: strings.HasSuffix(op, ">")})
				from, rest = to, after
			}
		}
	}
	return fc, len(fc.nodes) > 0 && len(fc.nodes)+len(fc.edges) <= mermaidMaxItems
}

var stateRe = regexp.MustCompile(`^(\[\*\]|[A-Za-z0-9_]+)\s*-->\s*(\[\*\]|[A-Za-z0-9_]+)\s*(?::\s*(.*))?$`)

// parseState reads a state diagram as a flowchart: states as rounded
// boxes, [*] as the start and end dots, transitions with their labels.
func parseState(src string) (*flowchart, bool) {
	fc := &flowchart{dir: "TD", byID: map[string]*mmNode{}}
	node := func(id string, end bool) string {
		key := id
		if id == "[*]" {
			key = "[*]start"
			if end {
				key = "[*]end"
			}
		}
		if fc.byID[key] == nil {
			n := &mmNode{id: key, label: id, shape: "round"}
			if id == "[*]" {
				n.label, n.shape = "", "dot"
			}
			fc.byID[key] = n
			fc.nodes = append(fc.nodes, n)
		}
		return key
	}
	for i, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if i == 0 || line == "" || strings.HasPrefix(line, "%%") || strings.HasPrefix(line, "direction") || strings.HasPrefix(line, "note") || line == "}" {
			continue
		}
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "state" {
			// state "Long name" as S1
			if j := strings.Index(line, " as "); j >= 0 {
				id := strings.TrimSpace(line[j+4:])
				node(id, false)
				fc.byID[id].label = strings.Trim(strings.TrimSpace(line[len("state"):j]), `"`)
			}
			continue
		}
		if id, label, ok := strings.Cut(line, ":"); ok && !strings.Contains(id, "-->") {
			node(strings.TrimSpace(id), false)
			fc.byID[strings.TrimSpace(id)].label = strings.TrimSpace(label)
			continue
		}
		m := stateRe.FindStringSubmatch(line)
		if m == nil {
			return nil, false
		}
		from, to := node(m[1], false), node(m[2], true)
		fc.edges = append(fc.edges, mmEdge{from: from, to: to, label: strings.TrimSpace(m[3]), arrow: true})
	}
	return fc, len(fc.nodes) > 0 && len(fc.nodes)+len(fc.edges) <= mermaidMaxItems
}

// layout ranks the nodes by the longest path to them and spaces them.
func (fc *flowchart) layout(size float32) (w, h float32) {
	// Edges that go back up the chart, found by a walk from the first
	// node, take no part in ranking: a loop back stays a loop back.
	out := map[string][]int{}
	for i, e := range fc.edges {
		out[e.from] = append(out[e.from], i)
	}
	back := map[int]bool{}
	state := map[string]int{} // 1 on the walk, 2 done
	var walk func(id string)
	walk = func(id string) {
		state[id] = 1
		for _, i := range out[id] {
			to := fc.edges[i].to
			switch state[to] {
			case 1:
				back[i] = true
			case 0:
				walk(to)
			}
		}
		state[id] = 2
	}
	for _, n := range fc.nodes {
		if state[n.id] == 0 {
			walk(n.id)
		}
	}
	// Longest path over the forward edges.
	for range len(fc.nodes) {
		changed := false
		for i, e := range fc.edges {
			a, b := fc.byID[e.from], fc.byID[e.to]
			if !back[i] && a != b && b.rank < a.rank+1 {
				b.rank = a.rank + 1
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	ranks := map[int][]*mmNode{}
	maxRank := 0
	for _, n := range fc.nodes {
		n.order = len(ranks[n.rank])
		ranks[n.rank] = append(ranks[n.rank], n)
		maxRank = max(maxRank, n.rank)
		tw, th := ui.ShapeText(n.label, ui.Font{Family: fontUI, Size: size}, 220).Size()
		n.w, n.h = max(tw+size*1.6, size*3), th+size
		if n.shape == "box" {
			mw, mh := boxMembers(n.members, size)
			n.w, n.h = max(n.w, mw+size*1.6), n.h+mh
		}
		if n.shape == "diamond" {
			n.w, n.h = n.w*1.4, n.h*1.6
		}
		if n.shape == "circle" {
			d := max(n.w, n.h)
			n.w, n.h = d, d
		}
		if n.shape == "dot" {
			n.w, n.h = size, size
		}
	}
	gapMain, gapCross := size*3, size*1.5
	for _, n := range fc.nodes {
		if n.shape == "box" {
			// Classes and entities leave room for marks and cardinalities.
			gapMain, gapCross = size*5, size*2.5
			break
		}
	}
	// Place rank by rank: along the direction, ranks; across, each rank
	// centered on the widest.
	var cross []float32
	var along []float32
	for r := 0; r <= maxRank; r++ {
		var c, a float32
		for _, n := range ranks[r] {
			if fc.dir == "LR" {
				c += n.h + gapCross
				a = max(a, n.w)
			} else {
				c += n.w + gapCross
				a = max(a, n.h)
			}
		}
		cross = append(cross, c-gapCross)
		along = append(along, a)
	}
	var widest float32
	for _, c := range cross {
		widest = max(widest, c)
	}
	pos := float32(0)
	for r := 0; r <= maxRank; r++ {
		off := (widest - cross[r]) / 2
		for _, n := range ranks[r] {
			if fc.dir == "LR" {
				n.x, n.y = pos+(along[r]-n.w)/2, off
				off += n.h + gapCross
			} else {
				n.x, n.y = off, pos+(along[r]-n.h)/2
				off += n.w + gapCross
			}
		}
		pos += along[r] + gapMain
	}
	pos -= gapMain
	if fc.dir == "LR" {
		return pos, widest
	}
	return widest, pos
}

// edgePoint is where a line toward (tx, ty) leaves a node's box.
func edgePoint(n *mmNode, tx, ty float32) (float32, float32) {
	cx, cy := n.x+n.w/2, n.y+n.h/2
	dx, dy := tx-cx, ty-cy
	if dx == 0 && dy == 0 {
		return cx, cy
	}
	sx, sy := float32(math.Inf(1)), float32(math.Inf(1))
	if dx != 0 {
		sx = (n.w / 2) / float32(math.Abs(float64(dx)))
	}
	if dy != 0 {
		sy = (n.h / 2) / float32(math.Abs(float64(dy)))
	}
	s := min(sx, sy)
	return cx + dx*s, cy + dy*s
}

func (fc *flowchart) draw(p *ui.Painter, ox, oy, size float32, ink, line, fill ui.Color) {
	pairs := map[[2]string]bool{}
	for _, e := range fc.edges {
		pairs[[2]string{e.from, e.to}] = true
	}
	for _, e := range fc.edges {
		a, b := fc.byID[e.from], fc.byID[e.to]
		x0, y0 := edgePoint(a, b.x+b.w/2, b.y+b.h/2)
		x1, y1 := edgePoint(b, a.x+a.w/2, a.y+a.h/2)
		x0, y0, x1, y1 = x0+ox, y0+oy, x1+ox, y1+oy
		var px, py float32 // the side a two-way edge keeps to
		if pairs[[2]string{e.to, e.from}] && e.from != e.to {
			// Two ways between the same nodes: each to its own side.
			dx, dy := x1-x0, y1-y0
			n := float32(math.Hypot(float64(dx), float64(dy)))
			if n > 0 {
				px, py = -dy/n, dx/n
				off := size * 0.5
				x0, y0, x1, y1 = x0+px*off, y0+py*off, x1+px*off, y1+py*off
			}
		}
		if e.dashed {
			dashLine(p, x0, y0, x1, y1, 1.2, line)
		} else {
			seg(p, x0, y0, x1, y1, 1.2, line)
		}
		if e.arrow {
			ang := math.Atan2(float64(y1-y0), float64(x1-x0))
			l := float64(size * 0.6)
			p.FillPath(arrowHead(x1, y1, ang, l), line)
		}
		if e.label != "" {
			layout := ui.ShapeText(e.label, ui.Font{Family: fontUI, Size: size * 0.85}, 0)
			w, h := layout.Size()
			mx, my := (x0+x1)/2, (y0+y1)/2
			// A two-way edge's label stands off to its side.
			mx += px * (w/2 + size*0.4)
			my += py * (h/2 + size*0.2)
			p.Fill(ui.Rect{X: mx - w/2 - 4, Y: my - h/2 - 2, W: w + 8, H: h + 4}, fill, 4)
			p.TextLayout(layout, mx-w/2, my-h/2, ink)
		}
		// Over the label's ground: a relationship's ends and cardinalities.
		endMark(p, e.toMark, e.toText, x1, y1, x0, y0, size, ink, line, fill)
		endMark(p, e.fromMark, e.fromText, x0, y0, x1, y1, size, ink, line, fill)
	}
	for _, n := range fc.nodes {
		r := ui.Rect{X: n.x + ox, Y: n.y + oy, W: n.w, H: n.h}
		switch n.shape {
		case "diamond":
			path := &ui.Path{}
			path.MoveTo(r.X+r.W/2, r.Y)
			path.LineTo(r.X+r.W, r.Y+r.H/2)
			path.LineTo(r.X+r.W/2, r.Y+r.H)
			path.LineTo(r.X, r.Y+r.H/2)
			path.Close()
			p.FillPath(path, fill)
			p.StrokePath(path, 1.2, line)
		case "dot":
			p.Fill(r, ink, r.W/2)
			if strings.HasSuffix(n.id, "end") {
				p.Stroke(ui.Rect{X: r.X - 3, Y: r.Y - 3, W: r.W + 6, H: r.H + 6}, ink, r.W/2+3, 1.2)
			}
			continue
		case "box":
			drawBox(p, n, r, size, ink, line, fill)
			continue
		default:
			radius := float32(4)
			switch n.shape {
			case "round":
				radius = r.H / 2
			case "circle":
				radius = r.W / 2
			}
			p.Fill(r, fill, radius)
			p.Stroke(r, line, radius, 1.2)
		}
		layout := ui.ShapeText(n.label, ui.Font{Family: fontUI, Size: size}, 220)
		w, h := layout.Size()
		p.TextLayout(layout, r.X+(r.W-w)/2, r.Y+(r.H-h)/2, ink)
	}
}

// seg draws a straight line at any angle; Painter.Line draws only level
// and upright ones.
func seg(p *ui.Painter, x0, y0, x1, y1, width float32, c ui.Color) {
	p.StrokePolyline([][2]float32{{x0, y0}, {x1, y1}}, width, c)
}

func arrowHead(x, y float32, ang, l float64) *ui.Path {
	path := &ui.Path{}
	path.MoveTo(x, y)
	path.LineTo(x-float32(l*math.Cos(ang-0.4)), y-float32(l*math.Sin(ang-0.4)))
	path.LineTo(x-float32(l*math.Cos(ang+0.4)), y-float32(l*math.Sin(ang+0.4)))
	path.Close()
	return path
}

func dashLine(p *ui.Painter, x0, y0, x1, y1, width float32, c ui.Color) {
	dx, dy := x1-x0, y1-y0
	n := float32(math.Hypot(float64(dx), float64(dy)))
	if n == 0 {
		return
	}
	for t := float32(0); t < n; t += 8 {
		e := min(t+4, n)
		seg(p, x0+dx*t/n, y0+dy*t/n, x0+dx*e/n, y0+dy*e/n, width, c)
	}
}

// sequence is a sequence diagram: participants and the messages, in order.
type sequence struct {
	actors   []string
	labels   map[string]string
	messages []mmEdge
}

var seqMsgRe = regexp.MustCompile(`^([^\s\-+>]+)\s*(-->>|->>|-->|->|--x|-x|--\)|-\))\s*[+-]?([^:]+?)\s*:\s*(.*)$`)

func parseSequence(src string) (*sequence, bool) {
	sq := &sequence{labels: map[string]string{}}
	add := func(id string) {
		for _, a := range sq.actors {
			if a == id {
				return
			}
		}
		sq.actors = append(sq.actors, id)
	}
	for i, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if i == 0 || line == "" || strings.HasPrefix(line, "%%") || line == "end" || strings.HasPrefix(line, "autonumber") ||
			strings.HasPrefix(line, "Note") || strings.HasPrefix(line, "note") || strings.HasPrefix(line, "activate") || strings.HasPrefix(line, "deactivate") ||
			slices.Contains([]string{"loop", "alt", "else", "opt", "par", "and", "critical", "break", "rect", "box"}, firstWord(line)) {
			continue
		}
		if f := strings.Fields(line); len(f) >= 2 && (f[0] == "participant" || f[0] == "actor") {
			id := f[1]
			if j := strings.Index(line, " as "); j >= 0 {
				sq.labels[id] = strings.TrimSpace(line[j+4:])
			}
			add(id)
			continue
		}
		m := seqMsgRe.FindStringSubmatch(line)
		if m == nil {
			return nil, false
		}
		add(m[1])
		add(strings.TrimSpace(m[3]))
		sq.messages = append(sq.messages, mmEdge{from: m[1], to: strings.TrimSpace(m[3]), label: strings.TrimSpace(m[4]), dashed: strings.HasPrefix(m[2], "--"), arrow: true})
	}
	return sq, len(sq.actors) > 0 && len(sq.actors)+len(sq.messages) <= mermaidMaxItems
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

func (sq *sequence) label(id string) string {
	if l := sq.labels[id]; l != "" {
		return l
	}
	return id
}

func (sq *sequence) size(size float32) (w, h float32, col []float32, boxW float32) {
	boxW = size * 5
	for _, a := range sq.actors {
		tw, _ := ui.ShapeText(sq.label(a), ui.Font{Family: fontUI, Size: size}, 0).Size()
		boxW = max(boxW, tw+size*1.6)
	}
	gap := size * 3
	for _, m := range sq.messages {
		tw, _ := ui.ShapeText(m.label, ui.Font{Family: fontUI, Size: size * 0.85}, 0).Size()
		gap = max(gap, tw+size*2-boxW)
	}
	for i := range sq.actors {
		col = append(col, float32(i)*(boxW+gap)+boxW/2)
	}
	w = float32(len(sq.actors))*boxW + float32(len(sq.actors)-1)*gap
	h = size*2.4*2 + float32(len(sq.messages)+1)*size*2.6
	return w, h, col, boxW
}

func (sq *sequence) draw(p *ui.Painter, ox, oy, size float32, ink, line, fill ui.Color) {
	w, h, col, boxW := sq.size(size)
	_ = w
	boxH := size * 2.4
	index := map[string]int{}
	for i, a := range sq.actors {
		index[a] = i
		x := ox + col[i]
		dashLine(p, x, oy+boxH, x, oy+h-boxH, 1, line)
		for _, y := range []float32{oy, oy + h - boxH} {
			r := ui.Rect{X: x - boxW/2, Y: y, W: boxW, H: boxH}
			p.Fill(r, fill, 4)
			p.Stroke(r, line, 4, 1.2)
			layout := ui.ShapeText(sq.label(a), ui.Font{Family: fontUI, Size: size}, 0)
			tw, th := layout.Size()
			p.TextLayout(layout, x-tw/2, y+(boxH-th)/2, ink)
		}
	}
	for k, m := range sq.messages {
		y := oy + boxH + float32(k+1)*size*2.6
		x0, x1 := ox+col[index[m.from]], ox+col[index[m.to]]
		if x0 == x1 {
			// To itself: a loop out and back.
			p.StrokePolyline([][2]float32{{x0, y - size*0.6}, {x0 + size*2, y - size*0.6}, {x0 + size*2, y}, {x0 + 4, y}}, 1.2, line)
			p.FillPath(arrowHead(x0, y, math.Pi, float64(size*0.6)), line)
		} else {
			if m.dashed {
				dashLine(p, x0, y, x1, y, 1.2, line)
			} else {
				seg(p, x0, y, x1, y, 1.2, line)
			}
			ang := 0.0
			if x1 < x0 {
				ang = math.Pi
			}
			p.FillPath(arrowHead(x1, y, ang, float64(size*0.6)), line)
		}
		layout := ui.ShapeText(m.label, ui.Font{Family: fontUI, Size: size * 0.85}, 0)
		tw, th := layout.Size()
		p.TextLayout(layout, (x0+x1)/2-tw/2, y-th-3, ink)
	}
}

// mermaidKind is the diagram a source is, "" for one not drawn.
func mermaidKind(src string) string {
	if len(src) > mermaidMaxBytes || strings.Count(src, "\n") >= mermaidMaxLines {
		return ""
	}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		switch strings.ToLower(strings.Fields(line)[0]) {
		case "graph", "flowchart":
			return "flowchart"
		case "sequencediagram":
			return "sequence"
		case "statediagram", "statediagram-v2":
			return "state"
		case "classdiagram", "classdiagram-v2":
			return "class"
		case "erdiagram":
			return "er"
		case "xychart-beta", "xychart":
			return "xychart"
		}
		return ""
	}
	return ""
}

// registerMermaid draws finished mermaid blocks; the rest stay code.
func (a *App) registerMermaid(o *markdown.Options) {
	if o.Code == nil {
		o.Code = map[string]func(c *ui.Context, b *markdown.CodeBlockView){}
	}
	o.Code["mermaid"] = func(c *ui.Context, b *markdown.CodeBlockView) {
		src := strings.TrimSpace(b.Code())
		size := o.Theme.FontSize * 0.9
		var w, h float32
		var draw func(p *ui.Painter, x, y float32)
		ink, line, fill := o.Theme.Text, o.Theme.Muted, o.Theme.CodeBlockBackground
		switch kind := mermaidKind(src); {
		case b.Open:
		case kind == "flowchart":
			if fc, ok := parseFlowchart(src); ok {
				w, h = fc.layout(size)
				draw = func(p *ui.Painter, x, y float32) { fc.draw(p, x, y, size, ink, line, fill) }
			}
		case kind == "state":
			if fc, ok := parseState(src); ok {
				w, h = fc.layout(size)
				draw = func(p *ui.Painter, x, y float32) { fc.draw(p, x, y, size, ink, line, fill) }
			}
		case kind == "class" || kind == "er":
			parse := parseClass
			if kind == "er" {
				parse = parseER
			}
			if fc, ok := parse(src); ok {
				w, h = fc.layout(size)
				draw = func(p *ui.Painter, x, y float32) { fc.draw(p, x, y, size, ink, line, fill) }
			}
		case kind == "xychart":
			if xc, ok := parseXYChart(src); ok {
				w, h = xc.size(size)
				accent := o.Theme.Link
				draw = func(p *ui.Painter, x, y float32) { xc.draw(p, x, y, size, ink, line, accent) }
			}
		case kind == "sequence":
			if sq, ok := parseSequence(src); ok {
				w, h, _, _ = sq.size(size)
				draw = func(p *ui.Painter, x, y float32) { sq.draw(p, x, y, size, ink, line, fill) }
			}
		}
		if draw == nil || w <= 0 || h <= 0 || w > 8192 || h > 8192 {
			markdown.DefaultCodeBlock(c, b)
			return
		}
		frame := ui.Column(c).AlignSelf(ui.Stretch).Padding(12).Radius(o.Theme.CodeBlockRadius).Border(1, o.Theme.CodeBlockBorder).Label("Mermaid\n" + src)
		lodyContextMenu(c, framePalette, frame, a.rowMenu(fmt.Sprintf("mermaid/%x", frame.ID())), 28, func(m *lodyMenu) {
			if m.item("copy", iconCopy, locales.Copy(), menuItem{}) {
				writeClipboard(src)
			}
		})
		frame.Children(func() {
			// Scaled to the room it has, never past its own size.
			avail := frame.Bounds().W - 24
			scale := float32(1)
			if avail > 0 && w > avail {
				scale = avail / w
			}
			ui.Box(c).Size(w*scale, h*scale).AlignSelf(ui.Center).Draw(func(p *ui.Painter, r ui.Rect) {
				p.Transform(ui.Scaling(scale, scale), r.X, r.Y, func() { draw(p, r.X, r.Y) })
			})
		})
	}
}
