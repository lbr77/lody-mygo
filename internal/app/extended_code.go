package app

import (
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
)

// Extended code languages, as Lody's setting gates them: the languages
// past the usual set show plain until it is on; then those the editor
// knows highlight as it does, the rest by their keywords, comments,
// strings and numbers.

// extendedLanguages are Lody's MARKDOWN_EXTENDED_CODE_LANGUAGES.
var extendedLanguages = []string{
	"dart", "mdx", "lua", "objective-c", "perl", "powershell", "bat", "fish", "scss", "less", "hcl", "protobuf",
	"vue", "svelte", "astro", "elixir", "erlang", "clojure", "scala", "haskell", "ocaml", "fsharp", "julia", "r",
	"solidity", "zig", "wgsl", "glsl", "latex", "cmake", "nginx", "make", "lean", "coq",
}

// lexicon is what a language's lines are made of, for a simple tokenizer.
type lexicon struct {
	line     []string // line comment starts
	block    [2]string
	keywords string
	types    string
	quotes   string
}

var lexicons = map[string]lexicon{
	"perl":       {line: []string{"#"}, keywords: "my our local sub if elsif else unless while until for foreach do last next redo return use require package BEGIN END and or not eq ne lt gt le ge", quotes: `"'`},
	"powershell": {line: []string{"#"}, block: [2]string{"<#", "#>"}, keywords: "function param if elseif else switch foreach for while do until break continue return try catch finally throw begin process end filter", quotes: `"'`},
	"bat":        {line: []string{"rem ", "REM ", "::"}, keywords: "echo set if else for in do goto call exit not exist defined errorlevel setlocal endlocal", quotes: `"`},
	"fish":       {line: []string{"#"}, keywords: "function end if else switch case for in while begin and or not return set status", quotes: `"'`},
	"scss":       {line: []string{"//"}, block: [2]string{"/*", "*/"}, keywords: "@import @mixin @include @extend @if @else @each @for @function @return @use @forward", quotes: `"'`},
	"less":       {line: []string{"//"}, block: [2]string{"/*", "*/"}, keywords: "@import @media when and not", quotes: `"'`},
	"hcl":        {line: []string{"#", "//"}, block: [2]string{"/*", "*/"}, keywords: "resource data variable output locals module provider terraform for in if true false null", quotes: `"`},
	"mdx":        {keywords: "import export from default", quotes: `"'`},
	"vue":        {line: []string{"//"}, block: [2]string{"<!--", "-->"}, keywords: "template script style export default import from const let function return if else", quotes: `"'` + "`"},
	"svelte":     {line: []string{"//"}, block: [2]string{"<!--", "-->"}, keywords: "script style export let const import from function return if each await then catch", quotes: `"'` + "`"},
	"astro":      {line: []string{"//"}, block: [2]string{"<!--", "-->"}, keywords: "import export from const let function return if else", quotes: `"'` + "`"},
	"elixir":     {line: []string{"#"}, keywords: "def defp defmodule defstruct defprotocol defimpl defmacro do end fn if else unless case cond with when in import alias require use receive try rescue after raise nil true false", quotes: `"'`},
	"erlang":     {line: []string{"%"}, keywords: "module export import fun end if case of receive after when try catch throw begin andalso orelse not true false", quotes: `"'`},
	"clojure":    {line: []string{";"}, keywords: "def defn defn- defmacro let fn if when cond do loop recur ns require import nil true false and or not", quotes: `"`},
	"haskell":    {line: []string{"--"}, block: [2]string{"{-", "-}"}, keywords: "module import where let in if then else case of data type newtype class instance deriving do forall qualified as hiding", types: "Int Integer Double Float Bool Char String Maybe Either IO", quotes: `"`},
	"ocaml":      {block: [2]string{"(*", "*)"}, keywords: "let rec in if then else match with fun function type module struct sig end open of and or not begin mutable", types: "int float bool string unit list option", quotes: `"`},
	"fsharp":     {line: []string{"//"}, block: [2]string{"(*", "*)"}, keywords: "let rec in if then elif else match with fun function type module open of and or not mutable member interface async", types: "int float bool string unit list option", quotes: `"`},
	"julia":      {line: []string{"#"}, block: [2]string{"#=", "=#"}, keywords: "function end if elseif else for while in return module using import export struct mutable begin let do try catch finally true false nothing", quotes: `"`},
	"solidity":   {line: []string{"//"}, block: [2]string{"/*", "*/"}, keywords: "pragma solidity contract interface library function modifier event emit returns return if else for while public private internal external view pure payable memory storage calldata require revert mapping struct enum constructor", types: "uint uint256 int address bool string bytes bytes32", quotes: `"'`},
	"wgsl":       {line: []string{"//"}, block: [2]string{"/*", "*/"}, keywords: "fn let var const struct return if else for loop break continue switch case default", types: "f32 i32 u32 bool vec2 vec3 vec4 mat4x4 texture_2d sampler", quotes: `"`},
	"glsl":       {line: []string{"//"}, block: [2]string{"/*", "*/"}, keywords: "uniform in out inout attribute varying const return if else for while break continue discard struct layout precision", types: "void float int bool vec2 vec3 vec4 mat3 mat4 sampler2D", quotes: `"`},
	"latex":      {line: []string{"%"}, keywords: `\begin \end \documentclass \usepackage \section \subsection \textbf \emph \label \ref \cite \item`, quotes: ``},
	"cmake":      {line: []string{"#"}, keywords: "cmake_minimum_required project add_executable add_library target_link_libraries include_directories set if else endif foreach endforeach function endfunction find_package option message", quotes: `"`},
	"nginx":      {line: []string{"#"}, keywords: "server location listen server_name root index proxy_pass return rewrite include upstream http events", quotes: `"'`},
	"lean":       {line: []string{"--"}, block: [2]string{"/-", "-/"}, keywords: "theorem lemma def example structure inductive class instance namespace end open import variable by fun have show from let in if then else match with at calc", types: "Nat Int Prop Type", quotes: `"`},
	"coq":        {block: [2]string{"(*", "*)"}, keywords: "Theorem Lemma Definition Fixpoint Inductive Proof Qed Defined intros apply induction rewrite simpl reflexivity destruct exact Require Import Module End forall exists fun match with end", types: "nat bool Prop Type", quotes: `"`},
}

// lexTokenizer tokenizes a line of a language by its lexicon. The state
// is 1 inside a block comment.
func lexTokenizer(lx lexicon) editor.Tokenizer {
	keywords := map[string]bool{}
	for _, k := range strings.Fields(lx.keywords) {
		keywords[k] = true
	}
	types := map[string]bool{}
	for _, k := range strings.Fields(lx.types) {
		types[k] = true
	}
	return editor.TokenizerFunc(func(line string, state uint64, toks []editor.Token) ([]editor.Token, uint64) {
		i := 0
		emit := func(end int, kind editor.TokenKind) {
			toks = append(toks, editor.Token{End: end, Kind: kind})
			i = end
		}
		for i < len(line) {
			if state == 1 {
				if j := strings.Index(line[i:], lx.block[1]); j >= 0 {
					emit(i+j+len(lx.block[1]), editor.TokenComment)
					state = 0
					continue
				}
				emit(len(line), editor.TokenComment)
				break
			}
			rest := line[i:]
			if lx.block[0] != "" && strings.HasPrefix(rest, lx.block[0]) {
				state = 1
				if j := strings.Index(rest[len(lx.block[0]):], lx.block[1]); j >= 0 {
					emit(i+len(lx.block[0])+j+len(lx.block[1]), editor.TokenComment)
					state = 0
					continue
				}
				emit(len(line), editor.TokenComment)
				break
			}
			comment := false
			for _, start := range lx.line {
				if strings.HasPrefix(rest, start) {
					comment = true
				}
			}
			if comment {
				emit(len(line), editor.TokenComment)
				break
			}
			c := rune(line[i])
			switch {
			case strings.ContainsRune(lx.quotes, c) && c != 0:
				j := i + 1
				for j < len(line) && rune(line[j]) != c {
					if line[j] == '\\' {
						j++
					}
					j++
				}
				emit(min(j+1, len(line)), editor.TokenString)
			case unicode.IsDigit(c):
				j := i
				for j < len(line) && (unicode.IsDigit(rune(line[j])) || line[j] == '.' || line[j] == '_' || line[j] == 'x' || unicode.Is(unicode.ASCII_Hex_Digit, rune(line[j]))) {
					j++
				}
				emit(j, editor.TokenNumber)
			case unicode.IsLetter(c) || c == '_' || c == '@' || c == '\\':
				j := i + 1
				for j < len(line) && (unicode.IsLetter(rune(line[j])) || unicode.IsDigit(rune(line[j])) || line[j] == '_' || line[j] == '\'' || line[j] == '-' && c == '@') {
					j++
				}
				word := line[i:j]
				switch {
				case keywords[word]:
					emit(j, editor.TokenKeyword)
				case types[word]:
					emit(j, editor.TokenType)
				case j < len(line) && line[j] == '(':
					emit(j, editor.TokenFunction)
				default:
					emit(j, editor.TokenText)
				}
			default:
				emit(i+1, editor.TokenText)
			}
		}
		return toks, state
	})
}

// registerExtendedCode registers the extended languages' code blocks.
func (a *App) registerExtendedCode(o *markdown.Options) {
	o.Code = map[string]func(c *ui.Context, b *markdown.CodeBlockView){}
	for _, lang := range extendedLanguages {
		lx, simple := lexicons[lang]
		o.Code[lang] = func(c *ui.Context, b *markdown.CodeBlockView) {
			switch {
			case a.extendedCode && editor.LanguageNamed(lang) != nil:
				markdown.DefaultCodeBlock(c, b)
			case a.extendedCode && simple:
				a.codeBlock(c, o, b, lexTokenizer(lx))
			default:
				a.codeBlock(c, o, b, nil)
			}
		}
	}
}

// codeBlock draws a code block as the view does, its lines highlighted by
// tok, or plain without one.
func (a *App) codeBlock(c *ui.Context, o *markdown.Options, b *markdown.CodeBlockView, tok editor.Tokenizer) {
	t := o.Theme
	ct := t.CodeColors
	ui.Column(c).Direction(ui.LeftToRight).Background(t.CodeBlockBackground).Border(1, t.CodeBlockBorder).Radius(t.CodeBlockRadius).AlignSelf(ui.Stretch).Clip().
		Label("Code, " + b.Language).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Padding(t.FontSize*0.35, t.FontSize*0.4, 0, t.FontSize*0.85).Children(func() {
			ui.Text(c, b.Language).FontSize(t.FontSize * 0.8).TextColor(t.Muted)
			ui.Spacer(c)
			b.CopyButton(c)
		})
		var spans []ui.Span
		state := uint64(0)
		for k, line := range strings.Split(strings.TrimSuffix(b.Code(), "\n"), "\n") {
			if k > 0 {
				spans = append(spans, ui.Span{Text: "\n"})
			}
			if tok == nil || ct == nil {
				spans = append(spans, ui.Span{Text: line})
				continue
			}
			var toks []editor.Token
			toks, state = tok.Tokenize(line, state, nil)
			start := 0
			for _, tk := range toks {
				end := min(tk.End, len(line))
				if end > start {
					spans = append(spans, ui.Span{Text: line[start:end], Color: ct.TokenColor(tk.Kind)})
				}
				start = max(start, end)
			}
			if start < len(line) {
				spans = append(spans, ui.Span{Text: line[start:]})
			}
		}
		ui.Column(c).Padding(t.FontSize*0.25, t.FontSize*0.85, t.FontSize*0.85, t.FontSize*0.85).Font(t.CodeFont).FontSize(t.FontSize * 0.85).
			LineHeight(1.4).TextColor(t.Text).Children(func() { ui.RichText(c, spans...).Selectable() })
	})
}
