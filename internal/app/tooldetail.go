package app

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/lbr77/lody-mygo/locales"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lbr77/lody-mygo/internal/session"
)

const detailLimit = 6000 // characters of output shown inline

// toolDetail is what an expanded tool step shows, as Lody's: one sheet
// holding what the step did and what came back, in order — the command
// it ran, the tail of its output, any text — a diff closing the sheet
// before it as a surface of its own.
func (a *App) toolDetail(c *ui.Context, p *palette, key string, it session.Item) {
	shell := it.Kind == "execute" || it.Kind == "bash"
	hasTerminal := false
	for _, raw := range it.Content {
		if t := str(obj(raw)["type"]); t == "terminal_command" || t == "terminal_output" {
			hasTerminal = true
		}
	}
	var sections []func()
	ui.Column(c).FillWidth().Margin(2, 0, 6, 0).Gap(8).Children(func() {
		flush := func(at int) {
			if len(sections) == 0 {
				return
			}
			parts := sections
			sections = nil
			toolSheet(c.Key(fmt.Sprintf("sheet/%d", at)), p, parts)
		}
		output := func(outs []map[string]any) {
			var texts []string
			limited := false
			for _, o := range outs {
				texts = append(texts, str(o["output"]))
				limited = limited || o["truncated"] == true
			}
			var exit any
			if n := len(outs); n > 0 {
				exit = obj(outs[n-1]["exitStatus"])["exitCode"]
			}
			text, cut := terminalTail(texts)
			sections = append(sections, func() { a.outputSection(c, p, text, limited || cut, exit) })
		}
		blocks := it.Content
		for i := 0; i < len(blocks); i++ {
			block := obj(blocks[i])
			if block == nil {
				continue
			}
			switch str(block["type"]) {
			case "terminal_command", "terminal_output":
				var outs []map[string]any
				next := i
				if str(block["type"]) == "terminal_command" {
					next = i + 1
				}
				for next < len(blocks) {
					nb := obj(blocks[next])
					if str(nb["type"]) == "terminal" {
						next++
						continue
					}
					if str(nb["type"]) != "terminal_output" {
						break
					}
					outs = append(outs, nb)
					next++
				}
				if str(block["type"]) == "terminal_command" {
					command := toolCommand(block)
					sections = append(sections, func() { a.commandSection(c, p, command, shell) })
				}
				if len(outs) > 0 {
					output(outs)
				}
				i = next - 1
			case "terminal":
				// An older history's command and output in one block; a bare
				// one only points at the CLI's live terminal.
				if hasTerminal {
					continue
				}
				command := strings.TrimSpace(str(block["command"]) + " " + joinArgs(block["args"]))
				if command != "" {
					sections = append(sections, func() { a.commandSection(c, p, command, shell) })
				}
				if out := str(block["output"]); out != "" {
					output([]map[string]any{{"output": out, "exitStatus": block["exitStatus"]}})
				} else if command == "" {
					sections = append(sections, func() {
						ui.Text(c, locales.ToolTerminalStreaming()).FontSize(sizeCaption).TextColor(p.Tertiary)
					})
				}
			case "diff":
				flush(i)
				a.diffSection(c.Key(i), p, fmt.Sprintf("%s/%d", key, i), block)
			case "content":
				text := str(obj(block["content"])["text"])
				if text == "" {
					continue
				}
				// A shell step's text is what the program printed.
				if shell && !looksJSON(text) {
					output([]map[string]any{{"output": text}})
					continue
				}
				sections = append(sections, func() { verbatim(c, p, text) })
			}
		}
		if len(it.Content) == 0 || (len(sections) == 0 && !hasDiff(it.Content)) {
			if it.RawInput != nil && !shell {
				in := pretty(it.RawInput)
				sections = append(sections, func() { verbatim(c, p, in) })
			}
			if it.RawOutput != nil {
				out := pretty(it.RawOutput)
				sections = append(sections, func() { verbatim(c, p, out) })
			}
		}
		flush(len(blocks))
	})
}

func hasDiff(blocks []any) bool {
	for _, b := range blocks {
		if str(obj(b)["type"]) == "diff" {
			return true
		}
	}
	return false
}

func looksJSON(s string) bool {
	t := strings.TrimSpace(s)
	return (strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}")) || (strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]"))
}

// toolSheet is the card a step's sections share: the composer's fill on
// the page, a rule between sections.
func toolSheet(c *ui.Context, p *palette, sections []func()) {
	ui.Column(c).FillWidth().Radius(10).Background(p.Composer).Border(0.5, p.CardRim).Clip().
		Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.04)).Dividers(1, p.Border).Children(func() {
		for i, s := range sections {
			ui.Column(c.Key(i)).FillWidth().Padding(8, 12).Children(s)
		}
	})
}

var (
	shellWrapper    = regexp.MustCompile(`^(?:/(?:usr/)?(?:local/)?bin/)?(?:bash|sh|zsh|dash|fish)$`)
	shellScriptFlag = regexp.MustCompile(`^-(?:l?c|cl)$`)
	shellSafeWord   = regexp.MustCompile(`^[\w@%+=:,./~-]+$`)
)

// toolCommand is the command a reader would have typed: a shell's
// wrapper dropped from Codex's ["/bin/bash", "-lc", script], other words
// quoted as a shell needs.
func toolCommand(block map[string]any) string {
	command := str(block["command"])
	var args []string
	for _, a := range list(block["args"]) {
		args = append(args, fmt.Sprint(a))
	}
	if shellWrapper.MatchString(command) {
		if len(args) == 2 && shellScriptFlag.MatchString(args[0]) {
			return args[1]
		}
		if len(args) == 3 && args[0] == "-l" && args[1] == "-c" {
			return args[2]
		}
	}
	parts := []string{command}
	for _, a := range args {
		switch {
		case a == "":
			parts = append(parts, "''")
		case shellSafeWord.MatchString(a):
			parts = append(parts, a)
		default:
			parts = append(parts, "'"+strings.ReplaceAll(a, "'", `'\''`)+"'")
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

// Lody shows the last lines of what a command printed.
const (
	tailLines    = 16
	tailBytes    = 32 * 1024
	tailLineSize = 4 * 1024
)

// terminalTail is the end of outputs, and whether earlier output was
// left out.
func terminalTail(outputs []string) (string, bool) {
	joined := strings.TrimRight(strings.Join(outputs, "\n"), "\n")
	if joined == "" {
		return "", false
	}
	lines := strings.Split(joined, "\n")
	cut := false
	if len(lines) > tailLines {
		lines, cut = lines[len(lines)-tailLines:], true
	}
	size := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if len(lines[i]) > tailLineSize {
			lines[i] = fmt.Sprintf("[A terminal line of %d characters was omitted]", len([]rune(lines[i])))
			cut = true
		}
		size += len(lines[i]) + 1
		if size > tailBytes {
			lines, cut = lines[i+1:], true
			break
		}
	}
	return strings.Join(lines, "\n"), cut
}

// commandSection is the command, after a prompt, colored as shell.
func (a *App) commandSection(c *ui.Context, p *palette, command string, shell bool) {
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
		if shell {
			ui.Text(c, "$").Font(fontMono).FontSize(sizeFoot).LineHeight(1.5).TextColor(p.Tertiary).Shrink(0)
		}
		var spans []ui.Span
		if shell {
			spans = a.shellSpans(command, p)
		} else {
			spans = []ui.Span{{Text: command, Color: p.Text}}
		}
		ui.RichText(c, spans...).Font(fontMono).FontSize(sizeFoot).LineHeight(1.5).Grow(1).MinWidth(0).Selectable()
	})
}

// shellSpans colors a command with the editor's shell grammar.
func (a *App) shellSpans(command string, p *palette) []ui.Span {
	lang := editor.LanguageNamed("bash")
	var colors *editor.Theme
	if a.md.Theme != nil {
		colors = a.md.Theme.CodeColors
	}
	if lang == nil || lang.Tokenizer == nil || colors == nil {
		return []ui.Span{{Text: command, Color: p.Text}}
	}
	var spans []ui.Span
	state := uint64(0)
	for i, line := range strings.Split(command, "\n") {
		if i > 0 {
			spans = append(spans, ui.Span{Text: "\n"})
		}
		var toks []editor.Token
		toks, state = lang.Tokenizer.Tokenize(line, state, nil)
		start := 0
		for _, t := range toks {
			end := min(t.End, len(line))
			if end > start {
				spans = append(spans, ui.Span{Text: line[start:end], Color: colors.TokenColor(t.Kind)})
				start = end
			}
		}
		if start < len(line) {
			spans = append(spans, ui.Span{Text: line[start:], Color: p.Text})
		}
	}
	return spans
}

// outputSection is what the program printed, its tail, with a note when
// earlier output was left out and the exit code when it failed.
func (a *App) outputSection(c *ui.Context, p *palette, text string, limited bool, exit any) {
	code, _ := exit.(float64)
	failed := exit != nil && code != 0
	if text == "" && !failed {
		return
	}
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		if text != "" && limited {
			ui.Text(c, locales.ToolEarlierOutputHidden()).FontSize(sizeCaption).TextColor(p.Tertiary)
		}
		if text != "" {
			ui.RichText(c, ansiSpans(text, p.Secondary, p.Dark)...).Font(fontMono).FontSize(sizeFoot).LineHeight(1.5).FillWidth().Selectable()
		}
		if failed {
			ui.Text(c, locales.ToolExitCode(fmt.Sprint(int(code)))).Font(fontMono).FontSize(sizeCaption).TextColor(p.Danger)
		}
	})
}

// verbatim is machine text, a JSON result: mono, never Markdown.
func verbatim(c *ui.Context, p *palette, text string) {
	ui.Text(c, clip(text)).Font(fontMono).FontSize(sizeFoot).LineHeight(1.5).TextColor(p.Secondary).Selectable().FillWidth()
}

var ansiCSI = regexp.MustCompile("\x1b\\[([0-9;?]*)([A-Za-z])")

// ansiColors are the terminal's sixteen colors, normal then bright.
var ansiColors = [2][16]string{
	{"#000000", "#C91B00", "#00A600", "#999900", "#0225C7", "#C930C7", "#00A6B2", "#BFBFBF",
		"#666666", "#E6000C", "#00D900", "#E5E500", "#0000FF", "#E500E5", "#00E5E5", "#E5E5E5"},
	{"#000000", "#E06C75", "#98C379", "#E5C07B", "#61AFEF", "#C678DD", "#56B6C2", "#ABB2BF",
		"#5C6370", "#F07178", "#C3E88D", "#FFCB6B", "#82AAFF", "#C792EA", "#89DDFF", "#FFFFFF"},
}

// ansiSpans renders a program's SGR colors and drops its other escapes.
func ansiSpans(text string, base ui.Color, dark bool) []ui.Span {
	palette := ansiColors[0]
	if dark {
		palette = ansiColors[1]
	}
	var spans []ui.Span
	color, bold := base, false
	last := 0
	add := func(s string) {
		if s != "" {
			sp := ui.Span{Text: s, Color: color}
			if bold {
				sp.Weight = 600
			}
			spans = append(spans, sp)
		}
	}
	for _, m := range ansiCSI.FindAllStringSubmatchIndex(text, -1) {
		add(text[last:m[0]])
		last = m[1]
		if text[m[4]:m[5]] != "m" {
			continue
		}
		params := strings.Split(text[m[2]:m[3]], ";")
		for k := 0; k < len(params); k++ {
			n, _ := strconv.Atoi(params[k])
			switch {
			case params[k] == "" || n == 0:
				color, bold = base, false
			case n == 1:
				bold = true
			case n == 22:
				bold = false
			case n >= 30 && n <= 37:
				color = ui.Hex(palette[n-30])
			case n >= 90 && n <= 97:
				color = ui.Hex(palette[n-90+8])
			case n == 39:
				color = base
			case n == 38 && k+2 < len(params) && params[k+1] == "5":
				if idx, _ := strconv.Atoi(params[k+2]); idx < 16 {
					color = ui.Hex(palette[idx])
				}
				k += 2
			case n == 38 && k+4 < len(params) && params[k+1] == "2":
				r, _ := strconv.Atoi(params[k+2])
				g, _ := strconv.Atoi(params[k+3])
				b, _ := strconv.Atoi(params[k+4])
				color = ui.RGBA(uint8(r), uint8(g), uint8(b), 1)
				k += 4
			}
		}
	}
	add(text[last:])
	return spans
}

func (a *App) diffSection(c *ui.Context, p *palette, key string, block map[string]any) {
	path := str(block["path"])
	d := a.diffs[key]
	if d == nil {
		lang := editor.LanguageFor(path)
		before, after := editor.NewBuffer(str(block["oldText"])), editor.NewBuffer(str(block["newText"]))
		before.SetLanguage(lang)
		after.SetLanguage(lang)
		d = &editor.DiffState{Old: before, New: after, Layout: editor.DiffUnified, FitContent: true, Wrap: true}
		a.diffs[key] = d
	}
	ui.Column(c).FillWidth().Children(func() {
		if path != "" {
			ui.Text(c, path).Font(fontMono).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine().Padding(8, 12, 6, 12)
		}
		editor.DiffView(c, d).FillWidth()
	})
}

func clip(s string) string {
	s = strings.TrimRight(s, "\n")
	if r := []rune(s); len(r) > detailLimit {
		return string(r[:detailLimit]) + "\n…"
	}
	return s
}

func pretty(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func joinArgs(v any) string {
	list, _ := v.([]any)
	parts := make([]string, 0, len(list))
	for _, a := range list {
		parts = append(parts, fmt.Sprint(a))
	}
	return strings.Join(parts, " ")
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
