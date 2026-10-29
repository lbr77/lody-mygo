package convmd

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Session links, as Lody's session-link and session-link-export: a link
// reads under any of Lody's schemes, or the legacy session://<id>, and is
// written as lody://session/<id>?workspace=<id>.

var (
	sessionScheme = regexp.MustCompile(`(?:lody|ai\.lody\.stable|ai\.lody\.nightly|lody-oss|session)://`)
	sessionURI    = regexp.MustCompile(`(?:lody|ai\.lody\.stable|ai\.lody\.nightly|lody-oss|session)://[^` + jsSpace + "<>()\\[\\]`\"'*,;。，；：！？]+")
	uriPunct      = regexp.MustCompile(`[.,;:!?。，；：！？]+$`)
	legacyLink    = regexp.MustCompile(`^session://([A-Za-z0-9_-]+)/?$`)
	resourceLink  = regexp.MustCompile(`^([a-zA-Z.-]+)://session/([A-Za-z0-9_-]+)(?:\?([^#]*))?$`)
	linkID        = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

var linkSchemes = map[string]bool{"lody": true, "ai.lody.stable": true, "ai.lody.nightly": true, "lody-oss": true}

// sessionLink is where a session link goes. Tab is a legacy link's child
// session, which new links name directly.
type sessionLink struct {
	session, workspace, tab string
}

// parseSessionLink reads raw, the whole of it, as a session link.
func parseSessionLink(raw string) (sessionLink, bool) {
	if jsLen(raw) > 8192 || strings.ContainsAny(raw, `\%`) || strings.IndexFunc(raw, isJSSpace) >= 0 {
		return sessionLink{}, false
	}
	if m := legacyLink.FindStringSubmatch(raw); m != nil {
		return sessionLink{session: m[1]}, true
	}
	m := resourceLink.FindStringSubmatch(raw)
	if m == nil || !linkSchemes[strings.ToLower(m[1])] {
		return sessionLink{}, false
	}
	link := sessionLink{session: m[2]}
	seen := map[string]bool{}
	for _, pair := range strings.Split(m[3], "&") {
		if pair == "" {
			continue
		}
		key, value, _ := strings.Cut(pair, "=")
		key, value = strings.ReplaceAll(key, "+", " "), strings.ReplaceAll(value, "+", " ")
		if key != "workspace" && key != "tab" || seen[key] || !linkID.MatchString(value) {
			return sessionLink{}, false
		}
		seen[key] = true
		if key == "workspace" {
			link.workspace = value
		} else {
			link.tab = value
		}
	}
	return link, true
}

// ParseSessionLink reads raw, the whole of it, as a link to a session, as
// Lody's parseSessionLink: the session it names, its workspace where it
// says, and a legacy link's child tab, which new links name directly.
func ParseSessionLink(raw string) (session, workspace, tab string, ok bool) {
	l, ok := parseSessionLink(raw)
	return l.session, l.workspace, l.tab, ok
}

// String is the link in Lody's portable form, or "" when it cannot be one.
func (l sessionLink) String() string {
	for _, id := range []string{l.session, l.workspace, l.tab} {
		if id != "" && !linkID.MatchString(id) {
			return ""
		}
	}
	id := l.session
	if l.tab != "" {
		id = l.tab
	}
	url := "lody://session/" + id
	if l.workspace != "" {
		url += "?workspace=" + l.workspace
	}
	if len(url) > 8192 {
		return ""
	}
	return url
}

// exportSessionLinks writes the session links in Markdown prose in their
// portable form, with workspace added where a link has none, and leaves
// every other byte, and links in code, HTML and images, as written. Where
// Lody would throw on an invalid workspace, the link stays as written.
func exportSessionLinks(md, workspace string) string {
	if !sessionScheme.MatchString(md) {
		return md
	}
	opaque, text := scanMarkdown(md)
	var b strings.Builder
	done, oi, ti := 0, 0, 0
	for _, m := range sessionURI.FindAllStringIndex(md, -1) {
		at, raw := m[0], md[m[0]:m[1]]
		b.WriteString(md[done:at])
		b.WriteString(exportLink(md, raw, at, workspace, opaque, text, &oi, &ti))
		done = m[1]
	}
	b.WriteString(md[done:])
	return b.String()
}

// exportLink is what the session URI raw, at offset at, becomes; oi and ti
// walk the sorted opaque and text ranges.
func exportLink(md, raw string, at int, workspace string, opaque, text []span, oi, ti *int) string {
	for *oi < len(opaque) && opaque[*oi].end <= at {
		*oi++
	}
	if *oi < len(opaque) && opaque[*oi].start < at+len(raw) {
		return raw
	}
	// A URI inside another URL (?next=session://...) is not a link.
	for *ti < len(text) && text[*ti].end <= at {
		*ti++
	}
	inText := *ti < len(text) && text[*ti].start <= at
	if at > 0 && !(inText && text[*ti].start == at) {
		if r, _ := utf8.DecodeLastRuneInString(md[:at]); !isJSSpace(r) && r != '(' && r != '<' && r != '*' {
			return raw
		}
	}
	// Emphasis is cut off at the text's end; sentence punctuation is not
	// part of a link.
	token := raw
	if inText && text[*ti].end-at < len(raw) {
		token = raw[:text[*ti].end-at]
	}
	uri := uriPunct.ReplaceAllString(token, "")
	link, ok := parseSessionLink(uri)
	if !ok {
		return raw
	}
	if link.workspace == "" {
		link.workspace = workspace
	}
	if s := link.String(); s != "" {
		return s + raw[len(uri):]
	}
	return raw
}

// span is a byte range of a Markdown text.
type span struct{ start, end int }

// mdScan reads Markdown as CommonMark (mdast-util-from-markdown, which
// Lody uses) does, as far as exportSessionLinks needs: which ranges are
// code, inline code, HTML or images, whose links stay as written, and
// which are text. It does not resolve reference links.
type mdScan struct {
	src          string
	opaque, text []span

	conts []container
	leaf  int
	para  []span // the open paragraph's lines
	// The open code or HTML block: where it starts and ends, its fence
	// and the HTML block's kind, as CommonMark numbers them.
	start, end     int
	fenceCh        byte
	fenceLen, kind int
}

// container is a block quote, or a list item whose content is width
// columns in.
type container struct {
	quote bool
	width int
}

const (
	leafNone = iota
	leafPara
	leafFence
	leafIndented
	leafHTML
)

// scanMarkdown returns src's opaque and text ranges, each sorted.
func scanMarkdown(src string) (opaque, text []span) {
	p := &mdScan{src: src}
	for start := 0; start <= len(src); {
		end := strings.IndexByte(src[start:], '\n')
		if end < 0 {
			end = len(src)
		} else {
			end += start
		}
		lineEnd := end
		if lineEnd > start && src[lineEnd-1] == '\r' {
			lineEnd--
		}
		p.line(start, lineEnd)
		start = end + 1
	}
	p.closeLeaf()
	sort.Slice(p.opaque, func(i, j int) bool { return p.opaque[i].start < p.opaque[j].start })
	sort.Slice(p.text, func(i, j int) bool { return p.text[i].start < p.text[j].start })
	return p.opaque, p.text
}

func (p *mdScan) line(pos, end int) {
	s := p.src
	col, matched := 0, 0
	for ; matched < len(p.conts); matched++ {
		c := p.conts[matched]
		w, i := indentAt(s, pos, col, end)
		if c.quote {
			if w > 3 || i >= end || s[i] != '>' {
				break
			}
			pos, col = i+1, col+w+1
			if pos < end && (s[pos] == ' ' || s[pos] == '\t') {
				pos, col = pos+1, col+1
			}
			continue
		}
		if i >= end {
			continue // a blank line stays in the item
		}
		if w < c.width {
			break
		}
		pos, col = advance(s, pos, col, end, c.width)
	}
	all := matched == len(p.conts)

	switch p.leaf {
	case leafFence:
		if all {
			p.end = end
			if p.closesFence(pos, col, end) {
				p.closeLeaf()
			}
			return
		}
		p.closeLeaf()
	case leafHTML:
		if all && !(p.kind >= 6 && blank(s[pos:end])) {
			p.end = end
			if htmlEnds(p.kind, s[pos:end]) {
				p.closeLeaf()
			}
			return
		}
		p.closeLeaf()
	case leafIndented:
		if w, _ := indentAt(s, pos, col, end); all && (blank(s[pos:end]) || w >= 4) {
			if !blank(s[pos:end]) {
				p.end = end
			}
			return
		}
		p.closeLeaf()
	}

	// New block quotes and list items, which end what was open.
	for {
		w, i := indentAt(s, pos, col, end)
		if w >= 4 || i >= end {
			break
		}
		var c container
		if s[i] == '>' {
			c, pos, col = container{quote: true}, i+1, col+w+1
			if pos < end && (s[pos] == ' ' || s[pos] == '\t') {
				pos, col = pos+1, col+1
			}
		} else if marker, pad, ok := listItem(s, i, end, p.leaf == leafPara); ok {
			c = container{width: w + marker + pad}
			pos, col = advance(s, i+marker, col+w+marker, end, pad)
		} else {
			break
		}
		p.closeLeaf()
		p.conts = append(p.conts[:matched], c)
		matched = len(p.conts)
	}

	if blank(s[pos:end]) {
		if p.leaf == leafPara {
			p.closeLeaf()
		}
		p.conts = p.conts[:matched]
		return
	}
	w, i := indentAt(s, pos, col, end)
	if p.leaf == leafPara && matched < len(p.conts) && (w >= 4 || !p.interrupts(i, end)) {
		p.para = append(p.para, span{i, end}) // a lazy continuation
		return
	}
	p.closeTo(matched)

	kind := htmlStart(s[i:end], p.leaf == leafPara)
	switch {
	case w >= 4:
		if p.leaf == leafPara {
			p.para = append(p.para, span{i, end})
			return
		}
		p.leaf, p.start, p.end = leafIndented, pos, end
	case fenceOpen(s, i, end) > 0:
		p.closeLeaf()
		p.leaf, p.start, p.end = leafFence, pos, end
		p.fenceCh, p.fenceLen = s[i], fenceOpen(s, i, end)
	case atxHeading(s, i, end):
		p.closeLeaf()
		if cs, ce := atxContent(s, i, end); cs < ce {
			p.inline([]span{{cs, ce}})
		}
	case p.leaf == leafPara && setextLine(s, i, end), thematicBreak(s, i, end):
		p.closeLeaf()
	case kind > 0:
		p.closeLeaf()
		p.leaf, p.start, p.end, p.kind = leafHTML, pos, end, kind
		if htmlEndsFirst(kind, s[i:end]) {
			p.closeLeaf()
		}
	default:
		if p.leaf != leafPara {
			p.closeLeaf()
			p.leaf = leafPara
		}
		p.para = append(p.para, span{i, end})
	}
}

// interrupts reports whether a line starting at i would end a paragraph
// rather than lazily continue it.
func (p *mdScan) interrupts(i, end int) bool {
	s := p.src
	return fenceOpen(s, i, end) > 0 || atxHeading(s, i, end) || thematicBreak(s, i, end) || htmlStart(s[i:end], true) > 0
}

func (p *mdScan) closeTo(n int) {
	if n < len(p.conts) {
		p.closeLeaf()
		p.conts = p.conts[:n]
	}
}

func (p *mdScan) closeLeaf() {
	switch p.leaf {
	case leafPara:
		p.inline(p.para)
	case leafFence, leafIndented, leafHTML:
		p.opaque = append(p.opaque, span{p.start, p.end})
	}
	p.leaf, p.para = leafNone, nil
}

func (p *mdScan) closesFence(pos, col, end int) bool {
	s := p.src
	w, i := indentAt(s, pos, col, end)
	if w > 3 {
		return false
	}
	j := i
	for j < end && s[j] == p.fenceCh {
		j++
	}
	return j-i >= p.fenceLen && blank(s[j:end])
}

// indentAt is the width in columns, from column col, of the spaces and
// tabs at pos, and where they end.
func indentAt(s string, pos, col, end int) (int, int) {
	c := col
	for ; pos < end; pos++ {
		switch s[pos] {
		case ' ':
			c++
		case '\t':
			c += 4 - c%4
		default:
			return c - col, pos
		}
	}
	return c - col, pos
}

// advance moves past n columns of indentation.
func advance(s string, pos, col, end, n int) (int, int) {
	for target := col + n; pos < end && col < target; pos++ {
		switch s[pos] {
		case ' ':
			col++
		case '\t':
			col += 4 - col%4
		default:
			return pos, col
		}
	}
	return pos, col
}

func blank(s string) bool { return strings.Trim(s, " \t") == "" }

// listItem reads a list marker at i: its width, and the columns of space
// after it that the item's content starts past.
func listItem(s string, i, end int, inPara bool) (marker, pad int, ok bool) {
	j, ordered := i, false
	switch {
	case s[i] == '-' || s[i] == '+' || s[i] == '*':
		j++
	default:
		for j < end && j-i < 9 && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == i || j >= end || s[j] != '.' && s[j] != ')' {
			return 0, 0, false
		}
		ordered = true
		j++
	}
	if j < end && s[j] != ' ' && s[j] != '\t' || thematicBreak(s, i, end) {
		return 0, 0, false
	}
	empty := blank(s[j:end])
	// Only a non-empty bullet, or a list starting at 1, ends a paragraph.
	if inPara && (empty || ordered && s[i:j-1] != "1") {
		return 0, 0, false
	}
	w, _ := indentAt(s, j, 0, end)
	if empty || w > 4 {
		w = 1
	}
	return j - i, w, true
}

// fenceOpen is the length of the code fence opening at i, or 0.
func fenceOpen(s string, i, end int) int {
	if i >= end || s[i] != '`' && s[i] != '~' {
		return 0
	}
	j := i
	for j < end && s[j] == s[i] {
		j++
	}
	if j-i < 3 || s[i] == '`' && strings.IndexByte(s[j:end], '`') >= 0 {
		return 0
	}
	return j - i
}

func atxHeading(s string, i, end int) bool {
	j := i
	for j < end && s[j] == '#' {
		j++
	}
	return j > i && j-i <= 6 && (j == end || s[j] == ' ' || s[j] == '\t')
}

// atxContent is the range of a heading's text, without its #s.
func atxContent(s string, i, end int) (int, int) {
	for i < end && s[i] == '#' {
		i++
	}
	_, cs := indentAt(s, i, 0, end)
	ce := len(strings.TrimRight(s[:end], " \t"))
	if ce < cs {
		return cs, cs
	}
	k := ce
	for k > cs && s[k-1] == '#' {
		k--
	}
	if k == cs || s[k-1] == ' ' || s[k-1] == '\t' {
		ce = max(cs, len(strings.TrimRight(s[:k], " \t")))
	}
	return cs, ce
}

func thematicBreak(s string, i, end int) bool {
	if i >= end || s[i] != '*' && s[i] != '-' && s[i] != '_' {
		return false
	}
	n := 0
	for j := i; j < end; j++ {
		switch s[j] {
		case s[i]:
			n++
		case ' ', '\t':
		default:
			return false
		}
	}
	return n >= 3
}

func setextLine(s string, i, end int) bool {
	if i >= end || s[i] != '=' && s[i] != '-' {
		return false
	}
	j := i
	for j < end && s[j] == s[i] {
		j++
	}
	return blank(s[j:end])
}

// HTML: a tag's attributes, the HTML blocks' starts, and inline HTML.
const htmlAttr = `(?:[ \t\n]+[A-Za-z_:][A-Za-z0-9_.:-]*(?:[ \t\n]*=[ \t\n]*(?:[^ \t\n"'=<>` + "`" + `]+|'[^']*'|"[^"]*"))?)`

var (
	htmlRaw      = regexp.MustCompile(`^(?i:<(?:script|pre|style|textarea))(?:[ \t>]|$)`)
	htmlRawEnd   = regexp.MustCompile(`(?i)</(?:script|pre|style|textarea)>`)
	htmlDecl     = regexp.MustCompile(`^<![A-Za-z]`)
	htmlBlockTag = regexp.MustCompile(`^(?i:</?(?:address|article|aside|base|basefont|blockquote|body|caption|center|col|colgroup|dd|details|dialog|dir|div|dl|dt|fieldset|figcaption|figure|footer|form|frame|frameset|h[1-6]|head|header|hr|html|iframe|legend|li|link|main|menu|menuitem|nav|noframes|ol|optgroup|option|p|param|search|section|summary|table|tbody|td|tfoot|th|thead|title|tr|track|ul))(?:[ \t]|/?>|$)`)
	htmlLoneTag  = regexp.MustCompile(`^(?:<[A-Za-z][A-Za-z0-9-]*` + htmlAttr + `*[ \t]*/?>|</[A-Za-z][A-Za-z0-9-]*[ \t]*>)[ \t]*$`)
	inlineTag    = regexp.MustCompile(`^(?:<[A-Za-z][A-Za-z0-9-]*` + htmlAttr + `*[ \t\n]*/?>|</[A-Za-z][A-Za-z0-9-]*[ \t\n]*>|<\?(?s:.*?)\?>|<![A-Za-z][^>]*>|<!\[CDATA\[(?s:.*?)\]\]>)`)
	autolink     = regexp.MustCompile(`^<(?:[A-Za-z][A-Za-z0-9+.-]{1,31}:[^\x00-\x20\x7f<>]*|[A-Za-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*)>`)
)

// htmlStart is the kind of HTML block a line starts, 1 to 7, or 0. The
// seventh, a lone tag, cannot end a paragraph.
func htmlStart(line string, inPara bool) int {
	switch {
	case htmlRaw.MatchString(line):
		return 1
	case strings.HasPrefix(line, "<!--"):
		return 2
	case strings.HasPrefix(line, "<?"):
		return 3
	case htmlDecl.MatchString(line):
		return 4
	case strings.HasPrefix(line, "<![CDATA["):
		return 5
	case htmlBlockTag.MatchString(line):
		return 6
	case !inPara && htmlLoneTag.MatchString(line):
		return 7
	}
	return 0
}

// htmlEnds reports whether a line ends an HTML block of kind; 6 and 7 end
// at a blank line instead.
func htmlEnds(kind int, line string) bool {
	switch kind {
	case 1:
		return htmlRawEnd.MatchString(line)
	case 2:
		return strings.Contains(line, "-->")
	case 3:
		return strings.Contains(line, "?>")
	case 4:
		return strings.Contains(line, ">")
	case 5:
		return strings.Contains(line, "]]>")
	}
	return false
}

// htmlEndsFirst is htmlEnds for the block's first line, past its start;
// a comment's end may share the start's dashes, as in <!-->.
func htmlEndsFirst(kind int, line string) bool {
	skip := [...]int{1: 1, 2: 2, 3: 2, 4: 3, 5: 9, 7: 0}[kind]
	return kind <= 5 && htmlEnds(kind, line[skip:])
}

// inlineHTML is the length of the inline HTML at the start of b, or 0.
func inlineHTML(b []byte) int {
	if rest, ok := strings.CutPrefix(string(b[:min(len(b), 6)]), "<!--"); ok {
		switch {
		case strings.HasPrefix(rest, ">"):
			return 5
		case strings.HasPrefix(rest, "->"):
			return 6
		}
		if k := strings.Index(string(b[4:]), "-->"); k >= 0 {
			return 4 + k + 3
		}
		return 0
	}
	if m := inlineTag.FindIndex(b); m != nil {
		return m[1]
	}
	return 0
}

// delim is a run of * or _ that may open or close emphasis: what is left
// of it, at pos.
type delim struct {
	pos, n      int
	ch          byte
	open, close bool
	dead        bool
}

// bracket is an open [ or ![, and how many delims came before it.
type bracket struct {
	pos      int
	image    bool
	delims   int
	inactive bool
}

// inline reads a paragraph's or heading's lines: its code spans, HTML and
// images are opaque, and what is not syntax is text.
func (p *mdScan) inline(lines []span) {
	// The lines' content joined by newlines, and where each byte is in src.
	var buf []byte
	var at []int
	for k, l := range lines {
		if k > 0 {
			buf, at = append(buf, '\n'), append(at, lines[k-1].end)
		}
		buf = append(buf, p.src[l.start:l.end]...)
		for j := l.start; j < l.end; j++ {
			at = append(at, j)
		}
	}
	for len(buf) > 0 && (buf[len(buf)-1] == ' ' || buf[len(buf)-1] == '\t') {
		buf, at = buf[:len(buf)-1], at[:len(at)-1]
	}
	n := len(buf)
	if n == 0 {
		return
	}
	at = append(at, at[n-1]+1)

	syntax := make([]bool, n) // not text
	mark := func(from, to int) {
		for i := from; i < to; i++ {
			syntax[i] = true
		}
	}
	opaque := func(from, to int) {
		mark(from, to)
		p.opaque = append(p.opaque, span{at[from], at[to-1] + 1})
	}

	var ds []*delim
	var bs []bracket
	for i := 0; i < n; {
		switch c := buf[i]; {
		case c == '\\':
			switch {
			case i+1 < n && buf[i+1] == '\n': // a hard break
				mark(i, i+2)
				i += 2
			case i+1 < n && asciiPunct(buf[i+1]):
				i += 2
			default:
				i++
			}
		case c == '`':
			j := i
			for j < n && buf[j] == '`' {
				j++
			}
			if k := closingTicks(buf, j, j-i); k >= 0 {
				opaque(i, k)
				i = k
			} else {
				i = j
			}
		case c == '<':
			if m := autolink.FindIndex(buf[i:]); m != nil {
				mark(i, i+1)
				mark(i+m[1]-1, i+m[1])
				i += m[1]
			} else if k := inlineHTML(buf[i:]); k > 0 {
				opaque(i, i+k)
				i += k
			} else {
				i++
			}
		case c == '!' && i+1 < n && buf[i+1] == '[':
			bs = append(bs, bracket{pos: i, image: true, delims: len(ds)})
			i += 2
		case c == '[':
			bs = append(bs, bracket{pos: i, delims: len(ds)})
			i++
		case c == ']':
			if len(bs) == 0 {
				i++
				break
			}
			b := bs[len(bs)-1]
			bs = bs[:len(bs)-1]
			k := -1
			if !b.inactive {
				k = resource(buf, i+1)
			}
			if k < 0 {
				i++
				break
			}
			if b.image {
				opaque(b.pos, k)
			} else {
				resolveEmphasis(ds[b.delims:], mark)
				mark(b.pos, b.pos+1)
				mark(i, k)
				// No links in links.
				for j := range bs {
					if !bs[j].image {
						bs[j].inactive = true
					}
				}
			}
			ds = ds[:b.delims]
			i = k
		case c == '*' || c == '_':
			j := i
			for j < n && buf[j] == c {
				j++
			}
			ds = append(ds, newDelim(buf, i, j))
			i = j
		case c == ' ':
			j := i
			for j < n && buf[j] == ' ' {
				j++
			}
			if j < n && buf[j] == '\n' && j-i >= 2 { // a hard break
				mark(i, j+1)
				j++
			}
			i = j
		default:
			i++
		}
	}
	resolveEmphasis(ds, mark)

	for i := 0; i < n; {
		if syntax[i] {
			i++
			continue
		}
		j := i
		for j < n && !syntax[j] {
			j++
		}
		p.text = append(p.text, span{at[i], at[j-1] + 1})
		i = j
	}
}

// closingTicks is where the code span whose opening run of n backticks
// ends at from closes, past its closing run, or -1.
func closingTicks(buf []byte, from, n int) int {
	for i := from; i < len(buf); {
		if buf[i] != '`' {
			i++
			continue
		}
		j := i
		for j < len(buf) && buf[j] == '`' {
			j++
		}
		if j-i == n {
			return j
		}
		i = j
	}
	return -1
}

// asciiPunct reports whether a backslash escapes c.
func asciiPunct(c byte) bool {
	return c < 0x80 && (unicode.IsPunct(rune(c)) || unicode.IsSymbol(rune(c)))
}

// resource reads a link's (destination "title") at i: where it ends, or -1.
func resource(buf []byte, i int) int {
	n := len(buf)
	space := func(i int) int {
		for i < n && (buf[i] == ' ' || buf[i] == '\t' || buf[i] == '\n') {
			i++
		}
		return i
	}
	if i >= n || buf[i] != '(' {
		return -1
	}
	i = space(i + 1)
	if i < n && buf[i] == ')' {
		return i + 1
	}
	if i < n && buf[i] == '<' {
		for i++; ; i++ {
			if i >= n || buf[i] == '\n' || buf[i] == '<' {
				return -1
			}
			if buf[i] == '\\' && i+1 < n {
				i++
			} else if buf[i] == '>' {
				i++
				break
			}
		}
	} else {
		start, depth := i, 0
		for ; i < n; i++ {
			c := buf[i]
			if c == '\\' && i+1 < n && asciiPunct(buf[i+1]) {
				i++
				continue
			}
			if c <= ' ' || c == 0x7f {
				break
			}
			if c == '(' {
				if depth++; depth > 32 {
					return -1
				}
			} else if c == ')' {
				if depth == 0 {
					break
				}
				depth--
			}
		}
		if i == start || depth != 0 {
			return -1
		}
	}
	j := space(i)
	if j > i && j < n && (buf[j] == '"' || buf[j] == '\'' || buf[j] == '(') {
		closer := buf[j]
		if closer == '(' {
			closer = ')'
		}
		for j++; ; j++ {
			if j >= n || buf[j] == '(' && closer == ')' {
				return -1
			}
			if buf[j] == '\\' && j+1 < n {
				j++
			} else if buf[j] == closer {
				j++
				break
			}
		}
		j = space(j)
	}
	if j < n && buf[j] == ')' {
		return j + 1
	}
	return -1
}

// newDelim reads the run buf[i:j] of * or _ as micromark does, which lets
// a run open before, or close after, the other marker.
func newDelim(buf []byte, i, j int) *delim {
	const other, space, punct = 0, 1, 2
	class := func(r rune, edge bool) int {
		switch {
		case edge || isJSSpace(r):
			return space
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			return punct
		}
		return other
	}
	prev, _ := utf8.DecodeLastRune(buf[:i])
	next, _ := utf8.DecodeRune(buf[j:])
	before, after := class(prev, i == 0), class(next, j == len(buf))
	open := after == other || after == punct && before != other || j < len(buf) && (next == '*' || next == '_')
	close := before == other || before == punct && after != other || i > 0 && (prev == '*' || prev == '_')
	d := &delim{pos: i, n: j - i, ch: buf[i], open: open, close: close}
	if d.ch == '_' {
		d.open = open && (before != other || !close)
		d.close = close && (after != other || !open)
	}
	return d
}

// resolveEmphasis pairs openers and closers, as micromark's attention
// resolver, marking the delimiters they use up.
func resolveEmphasis(ds []*delim, mark func(from, to int)) {
	for ci := 0; ci < len(ds); ci++ {
		c := ds[ci]
		if c.dead || !c.close || c.n == 0 {
			continue
		}
		for oi := ci - 1; oi >= 0; oi-- {
			o := ds[oi]
			if o.dead || !o.open || o.n == 0 || o.ch != c.ch {
				continue
			}
			if (o.close || c.open) && c.n%3 != 0 && (o.n+c.n)%3 == 0 {
				continue
			}
			use := 1
			if o.n > 1 && c.n > 1 {
				use = 2
			}
			mark(o.pos+o.n-use, o.pos+o.n)
			mark(c.pos, c.pos+use)
			o.n -= use
			c.pos += use
			c.n -= use
			for k := oi + 1; k < ci; k++ {
				ds[k].dead = true
			}
			if c.n > 0 {
				ci-- // what is left of the closer looks again
			}
			break
		}
	}
}
