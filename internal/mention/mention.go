// Package mention is what the composer offers as the user types @, $ or
// /: the files of a project, the skills of a machine, the other sessions
// and the agent's commands, as lody-ios builds them, and the expansion of
// the tokens they insert as a message is sent.
package mention

import (
	"encoding/json"
	"path"
	"sort"
	"strings"
	"unicode"
)

// Kinds of items.
const (
	File      = "file"
	Directory = "directory"
	Skill     = "skill"
	Session   = "session"
	Command   = "cmd"
)

// Item is a thing the composer can mention.
type Item struct {
	Kind     string
	Path     string // the file's path, the skill's SKILL.md, the session's id, the command's name
	Name     string
	Subtitle string
	Insert   string // the text that takes the trigger's place
}

// MaxFiles is how many paths are asked of a machine.
const MaxFiles = 20000

// Files lists paths and the folders they are in, each folder before what
// it holds, with the insert text lody-ios gives them: the path after @,
// quoted when it has a space or a quote.
func Files(paths []string) []Item {
	seen := map[string]bool{}
	var out []Item
	for _, p := range paths {
		if !relative(p) {
			continue
		}
		parts := strings.Split(p, "/")
		for depth := 1; depth <= len(parts); depth++ {
			current := strings.Join(parts[:depth], "/")
			if seen[current] {
				continue
			}
			seen[current] = true
			kind := Directory
			if depth == len(parts) {
				kind = File
			}
			out = append(out, Item{Kind: kind, Path: current, Name: parts[depth-1], Subtitle: strings.Join(parts[:depth-1], "/"), Insert: "@" + quotePath(current)})
		}
	}
	return out
}

func quotePath(p string) string {
	if strings.ContainsRune(p, '"') || strings.IndexFunc(p, unicode.IsSpace) >= 0 {
		b, _ := json.Marshal(p)
		return string(b)
	}
	return p
}

func relative(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return strings.IndexFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0
}

// SkillGroup is a folder of skills as a machine lists it.
type SkillGroup struct {
	Scope     string       `json:"scope"` // project, global or system
	Dir       string       `json:"dir"`
	Skills    []SkillEntry `json:"skills"`
	Truncated bool         `json:"truncated"`
	Error     string       `json:"error,omitempty"`
}

// SkillEntry is a skill as a machine lists it.
type SkillEntry struct {
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	RelativePath string  `json:"relativePath"`
	AbsolutePath *string `json:"absolutePath"`
}

// Skills lists the skills of groups, the project's first, then the
// user's, then the system's, the first skill of a token winning.
func Skills(groups []SkillGroup) []Item {
	rank := map[string]int{"project": 0, "global": 1, "system": 2}
	sorted := append([]SkillGroup(nil), groups...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, ok := rank[sorted[i].Scope]
		if !ok {
			ri = 3
		}
		rj, ok := rank[sorted[j].Scope]
		if !ok {
			rj = 3
		}
		if ri != rj {
			return ri < rj
		}
		return sorted[i].Dir < sorted[j].Dir
	})
	seen := map[string]bool{}
	var out []Item
	for _, g := range sorted {
		for _, s := range g.Skills {
			p := s.RelativePath
			if g.Scope != "project" && s.AbsolutePath != nil {
				p = *s.AbsolutePath
			}
			if p == "" || !strings.HasSuffix(strings.ToLower(p), "/skill.md") || strings.TrimSpace(s.Name) == "" {
				continue
			}
			token := strings.TrimSpace(s.Name)
			if strings.IndexFunc(token, unicode.IsSpace) >= 0 {
				dir := s.RelativePath
				if dir == "" {
					dir = p
				}
				dir = path.Dir(dir)
				token = strings.Join(strings.Fields(path.Base(dir)), "-")
			}
			if token == "" || seen[token] {
				continue
			}
			seen[token] = true
			sub := strings.TrimSpace(s.Description)
			if sub == "" {
				sub = g.Dir
			}
			out = append(out, Item{Kind: Skill, Path: p, Name: s.Name, Subtitle: sub, Insert: "$" + token})
		}
	}
	return out
}

// Commands lists an agent's slash commands.
func Commands(names, descriptions []string) []Item {
	seen := map[string]bool{}
	var out []Item
	for i, name := range names {
		if name == "" || seen[name] || strings.IndexFunc(name, func(r rune) bool {
			return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == ':' || r == '.' || r == '-')
		}) >= 0 {
			continue
		}
		seen[name] = true
		desc := ""
		if i < len(descriptions) {
			desc = descriptions[i]
		}
		out = append(out, Item{Kind: Command, Path: name, Name: name, Subtitle: desc, Insert: "/" + name})
	}
	return out
}

// SessionItem is a session to mention.
func SessionItem(id, title, project string) Item {
	return Item{Kind: Session, Path: id, Name: title, Subtitle: project, Insert: "@session:" + id}
}

// Trigger is the mention being typed: its sigil, where it starts and what
// follows it up to the caret.
type Trigger struct {
	Start int  // in runes
	Sigil rune // '@', '$' or '/'
	Query string
}

// Find returns the mention being typed before caret, a rune offset: @ or
// $ (￥ and ¥ for Chinese keyboards) at the start or after a space, and /
// at the very start of the text.
func Find(text string, caret int) (Trigger, bool) {
	runes := []rune(text)
	if caret < 0 || caret > len(runes) {
		return Trigger{}, false
	}
	// The caret must end a word: what follows is the end or a space.
	if caret < len(runes) && !unicode.IsSpace(runes[caret]) {
		return Trigger{}, false
	}
	ws := caret
	for ws > 0 && !unicode.IsSpace(runes[ws-1]) {
		ws--
	}
	word := runes[ws:caret]
	if len(word) == 0 {
		return Trigger{}, false
	}
	sigil := func(r rune) rune {
		switch r {
		case '@', '$':
			return r
		case '￥', '¥':
			return '$'
		case '/', '、':
			return '/'
		}
		return 0
	}
	switch s := sigil(word[0]); s {
	case '@', '$':
		for _, r := range word[1:] {
			if k := sigil(r); k == '@' || k == '$' {
				return Trigger{}, false
			}
		}
		return Trigger{Start: ws, Sigil: s, Query: string(word[1:])}, true
	case '/':
		for _, r := range word[1:] {
			if sigil(r) == '/' {
				return Trigger{}, false
			}
		}
		if ws == 0 {
			return Trigger{Start: 0, Sigil: '/', Query: string(word[1:])}, true
		}
	}
	return Trigger{}, false
}

// Match returns up to limit items that hold query, case aside: those
// whose name starts with it first, then those whose name holds it, then
// those whose path or subtitle does, each in the catalog's order.
func Match(items []Item, query string, limit int) []Item {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		if len(items) > limit {
			return items[:limit]
		}
		return items
	}
	var tiers [3][]Item
	for _, it := range items {
		name := strings.ToLower(it.Name)
		switch {
		case strings.HasPrefix(name, q) || strings.HasPrefix(strings.ToLower(it.Insert[min(1, len(it.Insert)):]), q):
			tiers[0] = append(tiers[0], it)
		case strings.Contains(name, q):
			tiers[1] = append(tiers[1], it)
		case strings.Contains(strings.ToLower(it.Path), q) || strings.Contains(strings.ToLower(it.Subtitle), q):
			tiers[2] = append(tiers[2], it)
		}
	}
	var out []Item
	for _, t := range tiers {
		for _, it := range t {
			if len(out) == limit {
				return out
			}
			out = append(out, it)
		}
	}
	return out
}

// Tokens reports which kinds of tokens text holds that Expand rewrites:
// skills ($name) and sessions (@session:id).
func Tokens(text string) (skills, sessions bool) {
	for _, t := range tokens(text) {
		if strings.HasPrefix(t.text, "$") {
			skills = true
		} else {
			sessions = true
		}
	}
	return skills, sessions
}

type token struct {
	start, end int // bytes
	text       string
}

// tokens finds $skill and @session:id words, skipping quoted paths, whose
// text is their own.
func tokens(text string) []token {
	var out []token
	i := 0
	for i < len(text) {
		if strings.HasPrefix(text[i:], `@"`) {
			j := i + 2
			for j < len(text) && text[j] != '"' {
				if text[j] == '\\' {
					j++
				}
				j++
			}
			i = j + 1
			continue
		}
		if i > 0 && !isSpaceByte(text[i-1]) {
			i++
			continue
		}
		end := i
		for end < len(text) && !isSpaceByte(text[end]) {
			end++
		}
		word := text[i:end]
		switch {
		case len(word) > 1 && word[0] == '$' && !strings.ContainsAny(word[1:], "$@"):
			out = append(out, token{i, end, word})
		case len(word) > len("@session:") && strings.HasPrefix(word, "@session:") && !strings.Contains(word[1:], "@"):
			out = append(out, token{i, end, word})
		}
		if end == i {
			end++
		}
		i = end
	}
	return out
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}

// Expand rewrites the tokens of text that items know into what the agent
// reads, as lody-ios does as it sends: a skill into "use /name [Skill
// Path](…)", a session into a request to read its history. Unknown tokens
// stay as typed.
func Expand(text string, items []Item) string {
	byInsert := map[string]Item{}
	for _, it := range items {
		if it.Kind == Skill || it.Kind == Session {
			byInsert[it.Insert] = it
		}
	}
	var b strings.Builder
	last := 0
	for _, t := range tokens(text) {
		it, ok := byInsert[t.text]
		if !ok {
			continue
		}
		if it.Kind == Skill && strings.HasPrefix(strings.TrimLeft(text[t.end:], " \t\n\r"), "[Skill Path](") {
			continue
		}
		b.WriteString(text[last:t.start])
		b.WriteString(prompt(it))
		last = t.end
	}
	b.WriteString(text[last:])
	return b.String()
}

func prompt(it Item) string {
	switch it.Kind {
	case Session:
		return "use lody mcp to query session[id: " + it.Path + "] history"
	case Skill:
		dest := strings.ReplaceAll(strings.ReplaceAll(it.Path, `\`, `\\`), ")", `\)`)
		return "use /" + strings.TrimPrefix(it.Insert, "$") + " [Skill Path](" + dest + ")"
	}
	return it.Insert
}
