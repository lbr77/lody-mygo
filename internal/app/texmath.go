package app

import (
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// Math typesetting for Lody's Render inline math: TeX between $…$ and
// \(…\), and blocks between $$…$$ and \[…\], set in the conversation's
// text — letters in italic, Greek and the operators as their symbols,
// scripts raised and lowered smaller, fractions stacked over a rule, roots
// under a radical. What it does not know shows as its TeX.

// mathBox is a typeset piece: its advance, the height above the baseline
// and the depth below it, and how to draw it at a baseline.
type mathBox struct {
	w, asc, desc float32
	draw         func(p *ui.Painter, x, baseline float32, ink ui.Color)
}

var texSymbols = map[string]string{
	"alpha": "α", "beta": "β", "gamma": "γ", "delta": "δ", "epsilon": "ϵ", "varepsilon": "ε", "zeta": "ζ", "eta": "η",
	"theta": "θ", "vartheta": "ϑ", "iota": "ι", "kappa": "κ", "lambda": "λ", "mu": "μ", "nu": "ν", "xi": "ξ", "pi": "π",
	"varpi": "ϖ", "rho": "ρ", "varrho": "ϱ", "sigma": "σ", "varsigma": "ς", "tau": "τ", "upsilon": "υ", "phi": "ϕ",
	"varphi": "φ", "chi": "χ", "psi": "ψ", "omega": "ω",
	"Gamma": "Γ", "Delta": "Δ", "Theta": "Θ", "Lambda": "Λ", "Xi": "Ξ", "Pi": "Π", "Sigma": "Σ", "Upsilon": "Υ",
	"Phi": "Φ", "Psi": "Ψ", "Omega": "Ω",
	"cdot": "·", "times": "×", "div": "÷", "pm": "±", "mp": "∓", "ast": "∗", "star": "⋆", "circ": "∘", "bullet": "•",
	"le": "≤", "leq": "≤", "ge": "≥", "geq": "≥", "ne": "≠", "neq": "≠", "approx": "≈", "equiv": "≡", "sim": "∼",
	"simeq": "≃", "cong": "≅", "propto": "∝", "ll": "≪", "gg": "≫", "in": "∈", "notin": "∉", "ni": "∋",
	"subset": "⊂", "supset": "⊃", "subseteq": "⊆", "supseteq": "⊇", "cup": "∪", "cap": "∩", "emptyset": "∅",
	"varnothing": "∅", "forall": "∀", "exists": "∃", "neg": "¬", "lnot": "¬", "land": "∧", "wedge": "∧", "lor": "∨",
	"vee": "∨", "to": "→", "rightarrow": "→", "leftarrow": "←", "gets": "←", "leftrightarrow": "↔", "Rightarrow": "⇒",
	"Leftarrow": "⇐", "Leftrightarrow": "⇔", "implies": "⟹", "iff": "⟺", "mapsto": "↦", "uparrow": "↑", "downarrow": "↓",
	"infty": "∞", "partial": "∂", "nabla": "∇", "hbar": "ℏ", "ell": "ℓ", "Re": "ℜ", "Im": "ℑ", "aleph": "ℵ",
	"sum": "∑", "prod": "∏", "coprod": "∐", "int": "∫", "iint": "∬", "oint": "∮", "bigcup": "⋃", "bigcap": "⋂",
	"angle": "∠", "perp": "⊥", "parallel": "∥", "mid": "∣", "ldots": "…", "cdots": "⋯", "vdots": "⋮", "ddots": "⋱",
	"dots": "…", "prime": "′", "degree": "°", "langle": "⟨", "rangle": "⟩", "lfloor": "⌊", "rfloor": "⌋",
	"lceil": "⌈", "rceil": "⌉", "{": "{", "}": "}", "%": "%", "$": "$", "&": "&", "#": "#", "_": "_", "|": "‖",
	"oplus": "⊕", "otimes": "⊗", "setminus": "∖", "triangle": "△", "square": "□", "checkmark": "✓",
}

// texFunctions are set upright, as \sin is.
var texFunctions = map[string]bool{
	"sin": true, "cos": true, "tan": true, "cot": true, "sec": true, "csc": true, "arcsin": true, "arccos": true,
	"arctan": true, "sinh": true, "cosh": true, "tanh": true, "log": true, "ln": true, "lg": true, "exp": true,
	"lim": true, "max": true, "min": true, "sup": true, "inf": true, "det": true, "gcd": true, "deg": true, "dim": true,
	"arg": true, "ker": true, "Pr": true,
}

var texSpaces = map[string]float32{",": 0.17, ":": 0.22, ";": 0.28, "!": -0.17, " ": 0.25, "quad": 1, "qquad": 2}

type texParser struct {
	src     []rune
	pos     int
	font    string
	ok      bool
	display bool // a block: big operators take their limits above and below
}

// firstRune is the symbol an atom's source names: a command's, or itself.
func firstRune(src string, t *texParser) rune {
	src = strings.TrimSpace(src)
	if name, ok := strings.CutPrefix(src, "\\"); ok {
		if s, ok := texSymbols[name]; ok {
			return []rune(s)[0]
		}
		return 0
	}
	if src == "" {
		return 0
	}
	return []rune(src)[0]
}

func (t *texParser) peek() rune {
	if t.pos < len(t.src) {
		return t.src[t.pos]
	}
	return 0
}

// command reads a command's name after its backslash.
func (t *texParser) command() string {
	start := t.pos
	for t.pos < len(t.src) && unicode.IsLetter(t.src[t.pos]) {
		t.pos++
	}
	if t.pos == start && t.pos < len(t.src) {
		t.pos++
	}
	return string(t.src[start:t.pos])
}

func (t *texParser) skipSpace() {
	for t.pos < len(t.src) && unicode.IsSpace(t.src[t.pos]) {
		t.pos++
	}
}

// group reads one argument: a braced group or a single atom.
func (t *texParser) group(size float32) mathBox {
	t.skipSpace()
	if t.pos >= len(t.src) {
		// An argument the TeX never gave.
		t.ok = false
		return mathBox{}
	}
	if t.peek() == '{' {
		t.pos++
		b := t.list(size, '}')
		if t.peek() == '}' {
			t.pos++
		} else {
			t.ok = false
		}
		return b
	}
	return t.atom(size)
}

// rawGroup reads a braced group as text, for \text and its kin.
func (t *texParser) rawGroup() string {
	t.skipSpace()
	if t.peek() != '{' {
		t.ok = false
		return ""
	}
	t.pos++
	start, depth := t.pos, 1
	for t.pos < len(t.src) {
		switch t.src[t.pos] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				s := string(t.src[start:t.pos])
				t.pos++
				return s
			}
		}
		t.pos++
	}
	t.ok = false
	return ""
}

func (t *texParser) list(size float32, end rune) mathBox {
	var parts []mathBox
	for t.pos < len(t.src) && t.ok {
		if t.peek() == end {
			break
		}
		if unicode.IsSpace(t.peek()) {
			t.pos++
			continue
		}
		start := t.pos
		base := t.atom(size)
		bigOp := strings.ContainsRune("∑∏∐⋃⋂", firstRune(string(t.src[start:t.pos]), t))
		// Scripts on what came before, stacked when both come.
		var sup, sub *mathBox
		for t.ok {
			t.skipSpace()
			c := t.peek()
			if c != '^' && c != '_' {
				break
			}
			t.pos++
			script := t.group(size * 0.7)
			if c == '^' {
				sup = &script
			} else {
				sub = &script
			}
		}
		if bigOp && t.display && (sup != nil || sub != nil) {
			base = limits(base, sup, sub, size)
		} else if sup != nil || sub != nil {
			base = scripts(base, sup, sub, size)
		}
		parts = append(parts, base)
	}
	return hbox(parts)
}

func (t *texParser) atom(size float32) mathBox {
	c := t.peek()
	switch {
	case c == '\\':
		t.pos++
		name := t.command()
		switch {
		case name == "frac" || name == "dfrac" || name == "tfrac":
			num := t.group(size * 0.8)
			den := t.group(size * 0.8)
			return fraction(num, den, size)
		case name == "sqrt":
			return radical(t.group(size), size)
		case name == "text" || name == "mathrm" || name == "operatorname" || name == "textrm" || name == "mathsf":
			return t.run(t.rawGroup(), size, false, 400)
		case name == "mathbf" || name == "textbf" || name == "boldsymbol":
			return t.run(t.rawGroup(), size, false, 700)
		case name == "mathit" || name == "textit":
			return t.run(t.rawGroup(), size, true, 400)
		case name == "left" || name == "right" || name == "big" || name == "Big" || name == "bigl" || name == "bigr":
			t.skipSpace()
			if t.peek() == '\\' {
				t.pos++
				d := t.command()
				if s, ok := texSymbols[d]; ok {
					return t.run(s, size, false, 400)
				}
				return t.run(d, size, false, 400)
			}
			if t.peek() == '.' {
				t.pos++
				return mathBox{}
			}
			r := t.peek()
			t.pos++
			return t.run(string(r), size, false, 400)
		case texFunctions[name]:
			return t.run(name, size, false, 400)
		}
		if gap, ok := texSpaces[name]; ok {
			return mathBox{w: gap * size}
		}
		if s, ok := texSymbols[name]; ok {
			if t.display && strings.Contains("∑∏∐⋃⋂∫∬∮", s) {
				// A block's big operators stand taller, about the axis.
				b := t.run(s, size*1.5, false, 400)
				shift := (b.asc-b.desc)/2 - size*0.27
				return mathBox{w: b.w, asc: b.asc - shift, desc: b.desc + shift, draw: func(p *ui.Painter, x, baseline float32, ink ui.Color) {
					b.draw(p, x, baseline+shift, ink)
				}}
			}
			return t.run(s, size, false, 400)
		}
		t.ok = false
		return mathBox{}
	case c == '{':
		return t.group(size)
	case c == '}':
		t.ok = false
		t.pos++
		return mathBox{}
	case unicode.IsLetter(c):
		t.pos++
		return t.run(string(c), size, true, 400)
	default:
		t.pos++
		s := string(c)
		switch c {
		case '-':
			s = "−"
		case '\'':
			s = "′"
		case '*':
			s = "∗"
		}
		box := t.run(s, size, false, 400)
		if strings.ContainsRune("=<>+−±×÷≤≥≠→", []rune(s)[0]) {
			// Relations and operators keep space about them.
			gap := size * 0.22
			box = hbox([]mathBox{{w: gap}, box, {w: gap}})
		}
		return box
	}
}

// run is text set in the conversation's font.
func (t *texParser) run(s string, size float32, italic bool, weight int) mathBox {
	if s == "" {
		return mathBox{}
	}
	font := ui.Font{Family: t.font, Size: size, Italic: italic, Weight: weight}
	layout := ui.ShapeText(s, font, 0)
	w, _ := layout.Size()
	m := font.Metrics()
	return mathBox{w: w, asc: m.Ascent, desc: m.Descent, draw: func(p *ui.Painter, x, baseline float32, ink ui.Color) {
		p.TextLayout(layout, x, baseline-m.Ascent, ink)
	}}
}

func hbox(parts []mathBox) mathBox {
	var b mathBox
	for _, part := range parts {
		b.w += part.w
		b.asc = max(b.asc, part.asc)
		b.desc = max(b.desc, part.desc)
	}
	b.draw = func(p *ui.Painter, x, baseline float32, ink ui.Color) {
		for _, part := range parts {
			if part.draw != nil {
				part.draw(p, x, baseline, ink)
			}
			x += part.w
		}
	}
	return b
}

// scripts sets a superscript, a subscript or both, stacked, after base.
func scripts(base mathBox, sup, sub *mathBox, size float32) mathBox {
	switch {
	case sub == nil:
		return superscript(base, *sup, size)
	case sup == nil:
		return subscript(base, *sub, size)
	}
	rise := max(size*0.4, base.asc-sup.asc*0.55)
	drop := size * 0.25
	gap := size * 0.05
	return mathBox{w: base.w + gap + max(sup.w, sub.w), asc: max(base.asc, rise+sup.asc), desc: max(base.desc, drop+sub.desc), draw: func(p *ui.Painter, x, baseline float32, ink ui.Color) {
		if base.draw != nil {
			base.draw(p, x, baseline, ink)
		}
		sup.draw(p, x+base.w+gap, baseline-rise, ink)
		sub.draw(p, x+base.w+gap, baseline+drop, ink)
	}}
}

// limits sets a big operator's scripts above and below it, centered.
func limits(base mathBox, sup, sub *mathBox, size float32) mathBox {
	gap := size * 0.1
	w := base.w
	if sup != nil {
		w = max(w, sup.w)
	}
	if sub != nil {
		w = max(w, sub.w)
	}
	b := mathBox{w: w, asc: base.asc, desc: base.desc}
	if sup != nil {
		b.asc += gap + sup.asc + sup.desc
	}
	if sub != nil {
		b.desc += gap + sub.asc + sub.desc
	}
	b.draw = func(p *ui.Painter, x, baseline float32, ink ui.Color) {
		if base.draw != nil {
			base.draw(p, x+(w-base.w)/2, baseline, ink)
		}
		if sup != nil {
			sup.draw(p, x+(w-sup.w)/2, baseline-base.asc-gap-sup.desc, ink)
		}
		if sub != nil {
			sub.draw(p, x+(w-sub.w)/2, baseline+base.desc+gap+sub.asc, ink)
		}
	}
	return b
}

func superscript(base, sup mathBox, size float32) mathBox {
	rise := max(size*0.4, base.asc-sup.asc*0.55)
	return mathBox{w: base.w + sup.w + size*0.05, asc: max(base.asc, rise+sup.asc), desc: base.desc, draw: func(p *ui.Painter, x, baseline float32, ink ui.Color) {
		if base.draw != nil {
			base.draw(p, x, baseline, ink)
		}
		sup.draw(p, x+base.w+size*0.05, baseline-rise, ink)
	}}
}

func subscript(base, sub mathBox, size float32) mathBox {
	drop := size * 0.2
	return mathBox{w: base.w + sub.w + size*0.05, asc: base.asc, desc: max(base.desc, drop+sub.desc), draw: func(p *ui.Painter, x, baseline float32, ink ui.Color) {
		if base.draw != nil {
			base.draw(p, x, baseline, ink)
		}
		sub.draw(p, x+base.w+size*0.05, baseline+drop, ink)
	}}
}

// fraction stacks a numerator over a denominator about the math axis.
func fraction(num, den mathBox, size float32) mathBox {
	axis := size * 0.27
	gap := size * 0.12
	rule := max(1, size*0.06)
	w := max(num.w, den.w) + size*0.2
	return mathBox{w: w, asc: axis + gap + num.asc + num.desc, desc: max(0, den.asc+den.desc+gap-axis), draw: func(p *ui.Painter, x, baseline float32, ink ui.Color) {
		line := baseline - axis
		p.Fill(ui.Rect{X: x + size*0.05, Y: line - rule/2, W: w - size*0.1, H: rule}, ink, 0)
		if num.draw != nil {
			num.draw(p, x+(w-num.w)/2, line-gap-num.desc, ink)
		}
		if den.draw != nil {
			den.draw(p, x+(w-den.w)/2, line+gap+den.asc, ink)
		}
	}}
}

// radical draws a root sign and its bar over the body.
func radical(body mathBox, size float32) mathBox {
	sign := size * 0.55
	pad := size * 0.12
	rule := max(1, size*0.06)
	asc := body.asc + pad + rule
	return mathBox{w: sign + body.w + size*0.1, asc: asc, desc: body.desc, draw: func(p *ui.Painter, x, baseline float32, ink ui.Color) {
		top := baseline - asc + rule/2
		p.StrokePolyline([][2]float32{
			{x, baseline - size*0.25}, {x + sign*0.3, baseline - size*0.35}, {x + sign*0.55, baseline + body.desc},
			{x + sign, top}, {x + sign + body.w + size*0.1, top},
		}, rule, ink)
		if body.draw != nil {
			body.draw(p, x+sign, baseline, ink)
		}
	}}
}

// typesetTeX sets TeX in font at size, false for what it does not know.
func typesetTeX(tex, font string, size float32) (mathBox, bool) {
	return typeset(tex, font, size, false)
}

func typeset(tex, font string, size float32, display bool) (mathBox, bool) {
	t := &texParser{src: []rune(strings.TrimSpace(tex)), font: font, ok: true, display: display}
	b := t.list(size, 0)
	return b, t.ok && t.pos >= len(t.src) && b.w > 0
}

// mathOptions turns math on in Markdown options, typeset in ink, keeping
// what it sets as the options ask.
func (a *App) mathOptions(o *markdown.Options, ink func() ui.Color) {
	cache := map[string]markdown.InlineObject{}
	o.InlineMath = func(tex string, size float32) (markdown.InlineObject, bool) {
		key := tex + "\x00" + string(rune(int(size*10)))
		if obj, ok := cache[key]; ok {
			return obj, true
		}
		b, ok := typesetTeX(tex, fontUI, size)
		if !ok {
			return markdown.InlineObject{}, false
		}
		obj := markdown.InlineObject{Width: b.w, Height: b.asc, Depth: b.desc, Draw: func(p *ui.Painter, r ui.Rect) {
			b.draw(p, r.X, r.Y+b.asc, ink())
		}}
		cache[key] = obj
		return obj, true
	}
	o.Math = func(c *ui.Context, tex string) {
		size := o.Theme.FontSize * 1.15
		b, ok := typeset(tex, fontUI, size, true)
		if !ok {
			ui.Text(c, tex).Font(fontMono).FontSize(o.Theme.FontSize * 0.9).TextColor(ink()).Selectable()
			return
		}
		ui.Row(c).FillWidth().Justify(ui.Center).Padding(4, 0).Children(func() {
			ui.Box(c).Size(b.w, b.asc+b.desc).Label(tex).Draw(func(p *ui.Painter, r ui.Rect) {
				b.draw(p, r.X, r.Y+b.asc, ink())
			})
		})
	}
}
