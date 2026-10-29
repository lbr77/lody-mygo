// Package convmd renders a session's conversation as Markdown for "Copy as
// Markdown", as Lody's conversation-markdown builder does, so it can be
// pasted into another chat or agent.
//
// The copy aims at ~60k tokens and 150k characters, and has to fit both:
// CJK text is about a token a character. Message text and proposed plans
// are kept whole at every size, only their headings pushed below the turn
// headings; if they alone are over, the copy goes over and says so.
// Everything else gives way first, in steps: tool results and terminal
// output are capped, then terminal output dropped, then a turn's tool calls
// collapsed to one counted summary, and last thinking capped, head and tail
// kept, never dropped. Older turns give way before the last few, which are
// what a reader pastes the copy for.
package convmd

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/lbr77/lody-mygo/internal/session"
)

// The copy budget, and how many trailing turns keep their detail while
// older ones give way.
const (
	MaxChars      = 150_000
	MaxTokens     = 60_000
	RecentEntries = 4
)

// Options is what goes around the conversation, and how big it may get.
type Options struct {
	// WorkspaceID goes into session links that lack one, so they open in
	// the right workspace wherever the copy is pasted.
	WorkspaceID string
	Title       string // the document's # heading, if any
	Source      string // a provenance line, as "owner/repo · branch"
	// Participants names user turns by user id. Names show only when more
	// than one person wrote in the conversation.
	Participants map[string]string
	// IncompleteFinalResponse is a line, in the reader's language, saying
	// the last reply was still being written. It goes in the header, so
	// whoever reads the copy knows before reading it.
	IncompleteFinalResponse string
	// MaxChars, MaxTokens and RecentEntries are the package's when zero.
	MaxChars, MaxTokens, RecentEntries int
	// OmitTrimNotice leaves out the header line naming what was cut.
	OmitTrimNotice bool
}

// Stats is how big the copy came out and what was cut to fit.
type Stats struct {
	Chars           int // in UTF-16 units, as Lody counts
	EstimatedTokens int // see EstimateTokens
	EntryCount      int // turns rendered
	// TerminalOutputOmitted and TerminalOutputTruncated say some tool
	// call's terminal output was dropped, or cut to its tail.
	TerminalOutputOmitted, TerminalOutputTruncated bool
	ThinkingTruncated                              bool // capped in some turn; never dropped
	ToolResultsTruncated                           int
	ToolCallsCollapsed                             bool // some turn's calls are only counted
	PathsCount                                     int  // files listed at the end
	OverBudget                                     bool // message text alone is over
}

// EstimateTokens approximates text's token count: Latin script runs about
// four characters a token, CJK about one. It only has to keep a paste
// under a context window, not match a tokenizer.
func EstimateTokens(text string) int {
	wide, narrow := 0, 0
	for _, r := range text {
		if wideScript(r) {
			wide++
		} else {
			narrow++
		}
	}
	return wide + (narrow+3)/4
}

func wideScript(r rune) bool {
	return r >= 0x1100 && r <= 0x11ff || // Hangul Jamo
		r >= 0x2e80 && r <= 0x303e || // CJK radicals, Kangxi, punctuation
		r >= 0x3041 && r <= 0x33ff || // Kana, Hangul compat, CJK compat
		r >= 0x3400 && r <= 0x4dbf || // CJK Ext A
		r >= 0x4e00 && r <= 0x9fff || // CJK Unified
		r >= 0xa960 && r <= 0xa97f || // Hangul Jamo Ext A
		r >= 0xac00 && r <= 0xd7ff || // Hangul syllables
		r >= 0xf900 && r <= 0xfaff || // CJK compat ideographs
		r >= 0xfe30 && r <= 0xfe4f || // CJK compat forms
		r >= 0xff00 && r <= 0xff60 || // Fullwidth forms
		r >= 0xffe0 && r <= 0xffe6 ||
		r >= 0x20000 && r <= 0x3ffff // CJK Ext B+
}

// level is how much of a turn's other content survives. Thinking is
// capped, never dropped, so the reader always inherits some reasoning.
type level struct {
	thinkingCap    int
	terminalOutput bool
	terminalTail   int
	toolTextCap    int // 0 drops tool result bodies
	collapseTools  bool
}

const noCap = math.MaxInt

var levels = []level{
	{noCap, true, noCap, noCap, false},
	{noCap, true, 2048, 4000, false},
	{noCap, true, 512, 1000, false},
	{noCap, false, 0, 300, false},
	{noCap, false, 0, 0, true},
	{2000, false, 0, 0, true},
	{500, false, 0, 0, true},
}

// Build renders entries as Markdown, cutting all but message text until it
// fits opt's budget. It does not change entries.
func Build(entries []session.Entry, opt Options) (string, Stats) {
	if opt.MaxChars == 0 {
		opt.MaxChars = MaxChars
	}
	if opt.MaxTokens == 0 {
		opt.MaxTokens = MaxTokens
	}
	if opt.RecentEntries == 0 {
		opt.RecentEntries = RecentEntries
	}

	// Lody copies the persisted history; a queued message is not in it yet.
	history := make([]session.Entry, 0, len(entries))
	for _, e := range entries {
		if e.Queued || e.Role != "user" && e.Role != "assistant" {
			continue
		}
		items := make([]session.Item, len(e.Items))
		for i, it := range e.Items {
			switch it.Type {
			case "text", "thought", "proposed_plan":
				it.Text = exportSessionLinks(it.Text, opt.WorkspaceID)
			}
			items[i] = it
		}
		e.Items = items
		history = append(history, e)
	}

	// Older turns give way all the way first; only then the recent ones.
	last := len(levels) - 1
	var r rendered
	for pass := 0; pass <= 2*last; pass++ {
		r = render(history, opt, min(pass, last), max(0, pass-last))
		if jsLen(r.markdown) > opt.MaxChars {
			continue
		}
		if tokens := EstimateTokens(r.markdown); tokens <= opt.MaxTokens {
			return r.markdown, r.stats(tokens, false)
		}
	}
	// Message text alone is over: ship it whole rather than cut prose.
	return r.markdown, r.stats(EstimateTokens(r.markdown), true)
}

// tally is what a render cut.
type tally struct {
	terminalOmitted, terminalTruncated, thinkingTruncated, collapsed bool
	toolResultsTruncated                                             int
}

type rendered struct {
	markdown          string
	tally             tally
	entries, pathsLen int
}

func (r rendered) stats(tokens int, over bool) Stats {
	return Stats{
		Chars:                   jsLen(r.markdown),
		EstimatedTokens:         tokens,
		EntryCount:              r.entries,
		TerminalOutputOmitted:   r.tally.terminalOmitted,
		TerminalOutputTruncated: r.tally.terminalTruncated,
		ThinkingTruncated:       r.tally.thinkingTruncated,
		ToolResultsTruncated:    r.tally.toolResultsTruncated,
		ToolCallsCollapsed:      r.tally.collapsed,
		PathsCount:              r.pathsLen,
		OverBudget:              over,
	}
}

// render renders history, user and assistant turns only, with older turns
// at oldLevel and the recent ones at recentLevel.
func render(history []session.Entry, opt Options, oldLevel, recentLevel int) rendered {
	var t tally
	recentFrom := max(0, len(history)-opt.RecentEntries)

	// A name on every turn of a solo session is noise.
	var names []string
	speakers := map[string]bool{}
	for _, e := range history {
		if e.Role == "user" && e.UserID != "" && !speakers[e.UserID] {
			speakers[e.UserID] = true
			if name := opt.Participants[e.UserID]; name != "" {
				names = append(names, name)
			}
		}
	}
	showNames := len(speakers) > 1

	var body, paths, models []string
	var first, last int64
	timed := false
	count, turn := 0, 0
	for i, e := range history {
		lv := levels[oldLevel]
		if i >= recentFrom {
			lv = levels[recentLevel]
		}
		// Turn numbers follow the conversation, not what renders.
		if e.Role == "user" || turn == 0 {
			turn++
		}

		var calls []session.Item
		for _, it := range e.Items {
			if it.Type == "tool_call" {
				calls = append(calls, it)
			}
		}
		var parts []string
		summarized := false
		for _, it := range e.Items {
			if it.Type == "tool_call" {
				paths = append(paths, toolPaths(it)...)
				if lv.collapseTools {
					// One summary where the turn's first call was, so the
					// prose around it keeps its order.
					if !summarized {
						summarized = true
						if s := summarizeTools(calls, &t); s != "" {
							parts = append(parts, s)
						}
					}
					continue
				}
			}
			if s := renderItem(it, lv, &t); s != "" {
				parts = append(parts, s)
			}
		}
		if len(parts) == 0 {
			continue
		}

		started, ok := epochMs(e.Timestamp)
		if ok {
			if !timed || started < first {
				first = started
			}
			if !timed || started > last {
				last = started
			}
			timed = true
		}
		role := "User"
		if e.Role == "assistant" {
			role = "Assistant"
		}
		head := []string{fmt.Sprintf("%d · %s", turn, role)}
		if showNames && e.Role == "user" && e.UserID != "" {
			if name := opt.Participants[e.UserID]; name != "" {
				head = append(head, name)
			}
		}
		if ok {
			head = append(head, turnTime(started))
		}
		if e.Role == "assistant" {
			if m := turnModel(e); m != "" {
				if !contains(models, m) {
					models = append(models, m)
				}
				head = append(head, m)
			}
			// Lody also takes off the turn's approval waits, which our
			// projection does not keep.
			if ok && e.EndedAt != 0 {
				if d := duration(e.EndedAt - float64(started)); d != "" {
					head = append(head, d)
				}
			}
		}

		// A rule before each user turn chunks the transcript into rounds.
		if len(body) > 0 && e.Role == "user" {
			body = append(body, "---")
		}
		body = append(body, "## "+strings.Join(head, " · "), strings.Join(parts, "\n\n"))
		count++
	}

	files, pathsLen := pathsSection(paths)
	if files != "" {
		body = append(body, "---", files)
	}

	var sections []string
	if title := trimJS(opt.Title); title != "" {
		sections = append(sections, "# "+title)
	}
	// The header says what is missing before the transcript, not after.
	summary := []string{"Lody session"}
	if timed {
		if first == last {
			summary = append(summary, fullTime(first))
		} else {
			summary = append(summary, fullTime(first)+" → "+fullTime(last))
		}
	}
	summary = append(summary, fmt.Sprintf("%d message%s", count, plural(count)))
	header := []string{strings.Join(summary, " · ")}
	if source := trimJS(opt.Source); source != "" {
		header = append(header, source)
	}
	if len(names) > 1 {
		header = append(header, "Participants: "+strings.Join(names, ", "))
	}
	if len(models) > 0 {
		header = append(header, "Models: "+strings.Join(models, ", "))
	}
	if note := trimJS(opt.IncompleteFinalResponse); note != "" {
		header = append(header, "**"+note+"**")
	}
	if !opt.OmitTrimNotice {
		if notes := trimNotes(t); len(notes) > 0 {
			header = append(header, "**Trimmed to fit the copy budget:** "+strings.Join(notes, "; ")+".")
		}
	}
	if len(body) > 0 {
		sections = append(sections, "> "+strings.Join(header, "\n>\n> "), "---")
	}
	sections = append(sections, body...)

	return rendered{markdown: strings.Join(sections, "\n\n") + "\n", tally: t, entries: count, pathsLen: pathsLen}
}

func renderItem(it session.Item, lv level, t *tally) string {
	switch it.Type {
	case "text":
		if trimJS(it.Text) == "" {
			return ""
		}
		return demoteHeadings(it.Text)
	case "file":
		return fmt.Sprintf("- **Attachment:** %s (%d bytes). _File contents not included._", summaryLine(it.FileName), it.SizeBytes)
	case "image":
		name := "Attached image"
		if it.FileName != "" {
			name = summaryLine(it.FileName)
		}
		// Lody gives the size after the type; our projection keeps no
		// image's size, so it shows only when one is there.
		about := it.MimeType
		if it.SizeBytes > 0 {
			about += fmt.Sprintf(", %d bytes", it.SizeBytes)
		}
		return fmt.Sprintf("- **Image:** %s (%s). _Image contents not included._", name, about)
	case "image_group":
		return fmt.Sprintf("- **Images:** %d. _Image contents not included._", len(it.Images))
	case "proposed_plan":
		// Drawn out of the reply's text upstream, so it is prose too.
		if it.Status == "cleared" || trimJS(it.Text) == "" {
			return ""
		}
		return demoteHeadings(it.Text)
	case "thought":
		if trimJS(it.Text) == "" {
			return ""
		}
		text, cut := clampMiddle(demoteHeadings(it.Text), lv.thinkingCap)
		if cut {
			t.thinkingTruncated = true
		}
		// Named for the reader: reasoning carried in, not its own.
		return details("Thinking (from the original session)", text)
	case "plan":
		if len(it.Plan) == 0 {
			return ""
		}
		lines := make([]string, len(it.Plan))
		for i, p := range it.Plan {
			box, marker := "[ ]", ""
			if p.Status == "completed" {
				box = "[x]"
			}
			if p.Status == "in_progress" {
				marker = " _(in progress)_"
			}
			lines[i] = "- " + box + " " + p.Content + marker
		}
		return "**Plan**\n\n" + strings.Join(lines, "\n")
	case "tool_call":
		return renderTool(it, lv, t)
	case "subagent_task":
		sa := it.Subagent
		if sa == nil || sa.SkipTranscript {
			return ""
		}
		name := trimJS(sa.Description)
		for _, s := range []string{sa.SubagentType, sa.TaskType, "Subagent task"} {
			if name == "" {
				name = s
			}
		}
		return fmt.Sprintf("- **Subagent** %s _(%s)_", summaryLine(name), sa.Status)
	}
	// Lody also renders comment and visual annotation references, whose
	// payloads our projection does not keep.
	return ""
}

func renderTool(it session.Item, lv level, t *tally) string {
	// Lody adds the call's locations to its label when the title lacks
	// them; our projection does not keep locations.
	label := trimJS(it.Title)
	if label == "" {
		label = it.Kind
	}
	if label == "" {
		label = "Tool"
	}
	label = summaryLine(label)

	var blocks []string
	for _, b := range it.Content {
		if s := renderBlock(asMap(b), lv, t); s != "" {
			blocks = append(blocks, s)
		}
	}
	if len(blocks) == 0 {
		return "- **" + label + "**"
	}
	return details(label, strings.Join(blocks, "\n\n"))
}

func renderBlock(b map[string]any, lv level, t *tally) string {
	switch b["type"] {
	case "terminal_command":
		parts := []string{jsString(b["command"])}
		if args, ok := b["args"].([]any); ok {
			for _, a := range args {
				parts = append(parts, jsString(a))
			}
		}
		if command := strings.Join(parts, " "); command != "" {
			return "`$ " + command + "`"
		}
	case "terminal_output":
		output, _ := b["output"].(string)
		if !lv.terminalOutput {
			if output != "" {
				t.terminalOmitted = true
			}
			return ""
		}
		if output = redact(output); output == "" {
			return ""
		}
		text, cut := tailOf(output, lv.terminalTail)
		if cut {
			t.terminalTruncated = true
		}
		return fence(text, "")
	case "diff":
		// Path only: the file's contents would swamp the budget.
		path, _ := b["path"].(string)
		return "_Diff:_ `" + path + "`"
	case "content":
		c := asMap(b["content"])
		text, _ := c["text"].(string)
		if c["type"] != "text" || text == "" || lv.toolTextCap == 0 {
			return ""
		}
		text, cut := clampMiddle(redact(text), lv.toolTextCap)
		if cut {
			t.toolResultsTruncated++
		}
		return fence(text, "")
	}
	return ""
}

// summarizeTools stands in for a turn's tool calls: how many, by name.
// Lody counts by the agent's own tool name; our projection keeps only the
// call's kind, so that is what is counted.
func summarizeTools(calls []session.Item, t *tally) string {
	if len(calls) == 0 {
		return ""
	}
	t.collapsed = true
	counts := map[string]int{}
	var names []string
	for _, it := range calls {
		name := trimJS(it.Kind)
		if name == "" {
			name = "tool"
		}
		if counts[name] == 0 {
			names = append(names, name)
		}
		counts[name]++
	}
	col := collate.New(language.Und)
	sort.SliceStable(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return col.CompareString(names[i], names[j]) < 0
	})
	breakdown := make([]string, len(names))
	for i, name := range names {
		breakdown[i] = fmt.Sprintf("`%s` ×%d", name, counts[name])
	}
	return details(fmt.Sprintf("%d tool call%s (details omitted)", len(calls), plural(len(calls))), strings.Join(breakdown, " · "))
}

// toolPaths is the files a tool call edited, from its diffs. Lody also
// lists the call's locations, as read or other files, which our projection
// does not keep, so every path here is an edit.
func toolPaths(it session.Item) []string {
	var paths []string
	for _, b := range it.Content {
		m := asMap(b)
		if path, _ := m["path"].(string); m["type"] == "diff" && path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

// pathsSection lists the files tool calls touched, and how many. Its
// heading is qualified: a session that worked through a shell reports
// almost none.
func pathsSection(paths []string) (string, int) {
	var unique []string
	seen := map[string]bool{}
	for _, p := range paths {
		if !seen[p] {
			seen[p] = true
			unique = append(unique, "`"+p+"`")
		}
	}
	if len(unique) == 0 {
		return "", 0
	}
	return "## Files referenced (from tool call locations)\n\n- **Edited:** " + strings.Join(unique, ", "), len(unique)
}

func trimNotes(t tally) []string {
	var notes []string
	if t.collapsed {
		// A collapsed call's results never reach the other tallies.
		notes = append(notes, "tool calls collapsed to per-turn summaries (results and terminal output omitted)")
	}
	if t.thinkingTruncated {
		notes = append(notes, "thinking truncated")
	}
	if t.terminalOmitted {
		notes = append(notes, "terminal output omitted")
	} else if t.terminalTruncated {
		notes = append(notes, "terminal output truncated")
	}
	if t.toolResultsTruncated > 0 {
		notes = append(notes, fmt.Sprintf("%d tool result(s) truncated", t.toolResultsTruncated))
	}
	return notes
}

// turnModel is the model a turn ran on; the one requested is the last
// resort.
func turnModel(e session.Entry) string {
	if e.Model != "" {
		return e.Model
	}
	m, _ := e.Config["modelId"].(string)
	return m
}

// epochMs reads a history timestamp, an ISO instant.
func epochMs(ts string) (int64, bool) {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0, false
	}
	return t.UnixMilli(), true
}

// turnTime is "MM-DD HH:mm" in the local zone: the header has the full
// range, so a turn's stamp only has to tell turns apart.
func turnTime(ms int64) string {
	t := time.UnixMilli(ms).Local()
	return fmt.Sprintf("%02d-%02d %02d:%02d", int(t.Month()), t.Day(), t.Hour(), t.Minute())
}

func fullTime(ms int64) string {
	return fmt.Sprintf("%d-%s", time.UnixMilli(ms).Local().Year(), turnTime(ms))
}

// duration is a turn's working time, compactly; under a second gets none.
func duration(ms float64) string {
	if math.IsNaN(ms) || math.IsInf(ms, 0) || ms < 1000 {
		return ""
	}
	total := int64(math.Floor(ms/1000 + 0.5))
	hours, minutes, seconds := total/3600, total%3600/60, total%60
	switch {
	case hours > 0:
		return fmt.Sprintf("%dh%02dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

// Turn headings are ##, so headings in prose start at ###.
var (
	fenceRe   = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	headingRe = regexp.MustCompile(`^( {0,3})(#{1,6})`)
)

// demoteHeadings pushes the ATX headings in a message below the turn
// heading; left alone, an agent's # headings would outrank the turns and
// flatten the document. It only adds #s, and leaves fenced code alone.
func demoteHeadings(text string) string {
	if !strings.Contains(text, "#") {
		return text
	}
	lines := strings.Split(text, "\n")
	fence := ""
	for i, line := range lines {
		m := fenceRe.FindStringSubmatch(line)
		if fence != "" {
			if m != nil && m[1][0] == fence[0] && len(m[1]) >= len(fence) {
				fence = ""
			}
			continue
		}
		if m != nil {
			fence = m[1]
			continue
		}
		h := headingRe.FindStringSubmatchIndex(line)
		if h == nil {
			continue
		}
		// The #s have to end the line or be followed by a space.
		if r, _ := utf8.DecodeRuneInString(line[h[1]:]); h[1] < len(line) && !isJSSpace(r) {
			continue
		}
		n := min(6, h[5]-h[4]+2)
		lines[i] = line[:h[3]] + strings.Repeat("#", n) + line[h[1]:]
	}
	return strings.Join(lines, "\n")
}

// fence fences text with enough backticks that fences inside it cannot
// break out.
func fence(text, language string) string {
	body := strings.TrimRightFunc(text, isJSSpace)
	longest, run := 0, 0
	for i := 0; i < len(body); i++ {
		if body[i] == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	ticks := strings.Repeat("`", max(3, longest+1))
	return ticks + language + "\n" + body + "\n" + ticks
}

func details(summary, body string) string {
	return "<details>\n<summary>" + escapeHTML(summary) + "</summary>\n\n" + body + "\n\n</details>"
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func escapeHTML(s string) string { return htmlEscaper.Replace(s) }

var spaceRun = regexp.MustCompile("[" + jsSpace + "]+")

// summaryLine flattens text to one line of at most 120 characters, so a
// <summary> stays one row.
func summaryLine(text string) string {
	const limit = 120
	flat := strings.Trim(spaceRun.ReplaceAllString(text, " "), " ")
	if jsLen(flat) > limit {
		return jsSlice(flat, 0, limit-1) + "…"
	}
	return flat
}

// clampMiddle keeps the head and tail of text over limit and elides its
// middle.
func clampMiddle(text string, limit int) (string, bool) {
	n := jsLen(text)
	if n <= limit {
		return text, false
	}
	head := max(1, int(math.Floor(float64(limit)*0.6)))
	tail := max(0, limit-head)
	out := jsSlice(text, 0, head) + "\n\n… " + strconv.Itoa(n-head-tail) + " characters elided …\n\n"
	if tail > 0 {
		out += jsSlice(text, n-tail, n)
	}
	return out, true
}

// tailOf keeps the last limit characters of text.
func tailOf(text string, limit int) (string, bool) {
	n := jsLen(text)
	if n <= limit {
		return text, false
	}
	return "… " + strconv.Itoa(n-limit) + " characters elided …\n" + jsSlice(text, n-limit, n), true
}

// Redaction, as Lody's redactSensitiveTokens: GitHub tokens, bearer
// tokens, OpenAI-style keys and passwords in URLs.
var (
	githubToken = regexp.MustCompile(`\bghp_[A-Za-z0-9]{20,}\b`)
	githubPAT   = regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`)
	bearerToken = regexp.MustCompile(`\b(?i:Bearer)[` + jsSpace + `]+[A-Za-z0-9\-_.=]{20,}\b`)
	apiKey      = regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`)
	urlAuth     = regexp.MustCompile(`((?i:https?)://)[^@` + jsSpace + `]+@`)
)

// redact masks secrets in tool output. Each pattern runs only when its
// marker is there: tool output is most of a copy, and is rendered again
// at every level.
func redact(text string) string {
	if strings.Contains(text, "ghp_") {
		text = githubToken.ReplaceAllLiteralString(text, "ghp_***")
	}
	if strings.Contains(text, "github_pat_") {
		text = githubPAT.ReplaceAllLiteralString(text, "github_pat_***")
	}
	if containsFold(text, "bearer") {
		text = bearerToken.ReplaceAllLiteralString(text, "Bearer ***")
	}
	if strings.Contains(text, "sk-") {
		text = apiKey.ReplaceAllLiteralString(text, "sk-***")
	}
	if strings.Contains(text, "://") {
		text = urlAuth.ReplaceAllString(text, "${1}***@")
	}
	return text
}

// containsFold reports whether s has the ASCII word w in any case.
func containsFold(s, w string) bool {
	for i := 0; i+len(w) <= len(s); i++ {
		if s[i]|0x20 == w[0] && strings.EqualFold(s[i:i+len(w)], w) {
			return true
		}
	}
	return false
}

// jsSpace is the characters JavaScript's \s matches, as a regexp class.
const jsSpace = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// isJSSpace reports whether JavaScript's \s, and so its trim, matches r.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func trimJS(s string) string { return strings.TrimFunc(s, isJSSpace) }

// jsLen is s's length as JavaScript counts it, in UTF-16 units, which is
// what Lody's budgets and caps are in.
func jsLen(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// jsSlice is s's UTF-16 units [from, to), as JavaScript's slice. Half a
// surrogate pair comes out as U+FFFD, as it would when the copy is encoded.
func jsSlice(s string, from, to int) string {
	return string(utf16.Decode(utf16.Encode([]rune(s))[from:to]))
}

// jsString is v as JavaScript's join writes it.
func jsString(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	}
	return fmt.Sprint(v)
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
