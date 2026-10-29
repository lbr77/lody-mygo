package app

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/mention"
	"github.com/lbr77/lody-mygo/internal/shortcuts"
	"github.com/lbr77/lody-mygo/locales"
)

// The composer's mentions, as Lody's: @ for the project's files, skills
// and other sessions, $ for skills, and / at the start for the agent's
// commands. A panel above the box lists what matches what follows the
// sigil; Up and Down choose, Enter or Tab inserts, Escape closes it.
// Skills and sessions expand as the message is sent, as lody-ios does.

// mentionTarget is where mentions come from: the open session's machine
// and project, or New chat's.
type mentionTarget struct {
	machine, project, session, agent string
}

func (a *App) mentionTarget() mentionTarget {
	if s := a.catalog.Session(a.selected); s != nil {
		return mentionTarget{s.MachineID, s.ProjectID, s.ID, s.AgentConfigID}
	}
	return mentionTarget{machine: a.newChat.machine, project: a.newChat.project, agent: a.newChat.agent}
}

// localProject is the project's id on its machine, for a local project.
func (t mentionTarget) localProject() string {
	_, id, ok := strings.Cut(t.project, ":local:")
	if !ok {
		return ""
	}
	return id
}

// mentionSource is a list a machine gives, as last fetched.
type mentionSource struct {
	items   []mention.Item
	loading bool
	err     string
	loaded  time.Time
}

// mentionRow is a row of the panel: a category to narrow to, or an item.
type mentionRow struct {
	category string // file, skill or session
	item     mention.Item
}

type mentionState struct {
	active    bool
	trigger   mention.Trigger
	text      string // the text the trigger is in
	caret     int
	rows      []mentionRow
	highlight int
	reveal    bool   // scroll the highlight into view
	dismissed string // the trigger Escape closed, until it changes
	moveCaret bool   // put the caret at caretTo in the next frame
	caretTo   int
}

// mentionSource returns a list for target, fetching it when it is missing
// or a minute old.
func (a *App) mentionSource(kind string, t mentionTarget) *mentionSource {
	if a.mentionSources == nil {
		a.mentionSources = map[string]*mentionSource{}
	}
	key := kind + "|" + t.machine + "|" + t.project
	src := a.mentionSources[key]
	if src == nil {
		src = &mentionSource{}
		a.mentionSources[key] = src
	}
	if src.loading || (!src.loaded.IsZero() && time.Since(src.loaded) < time.Minute) {
		return src
	}
	if kind != mentionShortcut && !a.presence.IsOnline(t.machine) {
		src.err = locales.MentionOffline(a.machineName(t.machine))
		return src
	}
	conn, user := a.conn, a.account.User.ID
	if conn == nil {
		return src
	}
	store := a.shortcutStore()
	src.loading, src.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var items []mention.Item
		var err error
		switch kind {
		case mention.File:
			items, err = fetchFiles(ctx, conn, user, t)
		case mentionShortcut:
			items, err = fetchShortcuts(ctx, store)
		default:
			items, err = fetchSkills(ctx, conn, user, t)
		}
		a.update(func() {
			src.loading, src.loaded = false, time.Now()
			src.items, src.err = items, ""
			if err != nil {
				src.err = err.Error()
			}
		})
	}()
	return src
}

// fetchFiles lists a local project's files. A GitHub project's worktree
// isn't listed over the cloud.
func fetchFiles(ctx context.Context, conn *cloud.Conn, user string, t mentionTarget) ([]mention.Item, error) {
	id := t.localProject()
	if id == "" {
		return nil, nil
	}
	raw, err := conn.ProjectControl(ctx, t.machine, map[string]any{
		"type": "local-project/list-files", "localProjectId": id, "maxFiles": mention.MaxFiles, "requestedByUserId": user,
	}, 15*time.Second)
	if err != nil {
		return nil, err
	}
	var res struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return mention.Files(res.Paths), nil
}

// Where Lody looks for a project's skills, as lody-ios asks.
var skillDirs = []string{
	".adal/skills", ".agent/skills", ".agents/skills", ".aider-desk/skills", ".augment/skills", ".autohand/skills",
	".bob/skills", ".claude/skills", ".cline/skills", ".clinerules/skills", ".codeartsdoer/skills", ".codebuddy/skills",
	".codemaker/skills", ".codestudio/skills", ".commandcode/skills", ".continue/skills", ".cortex/skills", ".crush/skills",
	".cursor/skills", ".devin/skills", ".dsh/skills", ".factory/skills", ".forge/skills", ".gemini/skills", ".github/skills",
	".goose/skills", ".grok/skills", ".hermes/skills", ".iflow/skills", ".inferencesh/skills", ".jazz/skills", ".junie/skills",
	".kilo/skills", ".kilocode/skills", ".kimi-code/skills", ".kiro/skills", ".kode/skills", ".lingma/skills", ".mcpjam/skills",
	".moxby/skills", ".mux/skills", ".neovate/skills", ".ona/skills", ".opencode/skills", ".openhands/skills", ".pi/skills",
	".pochi/skills", ".qoder/skills", ".qwen/skills", ".reasonix/skills", ".roo/skills", ".rovodev/skills",
	".tabnine/agent/skills", ".terramind/skills", ".tinycloud/skills", ".trae/skills", ".vibe/skills", ".windsurf/skills",
	".zencoder/skills", "agent/skills", "data/skills", "skills", "skills/.curated", "skills/.experimental", "skills/.system",
}

// fetchSkills lists the machine's skills and the project's.
func fetchSkills(ctx context.Context, conn *cloud.Conn, user string, t mentionTarget) ([]mention.Item, error) {
	requests := []map[string]any{{"type": "local-project/list-global-skills", "requestedByUserId": user}}
	if id := t.localProject(); id != "" {
		requests = append(requests, map[string]any{"type": "local-project/list-skills", "localProjectId": id, "skillDirs": skillDirs, "requestedByUserId": user})
	}
	type result struct {
		groups []mention.SkillGroup
		err    error
	}
	results := make(chan result, len(requests))
	for _, req := range requests {
		go func() {
			raw, err := conn.ProjectControl(ctx, t.machine, req, 15*time.Second)
			var res struct {
				Groups []mention.SkillGroup `json:"groups"`
			}
			if err == nil {
				err = json.Unmarshal(raw, &res)
			}
			results <- result{res.Groups, err}
		}()
	}
	var groups []mention.SkillGroup
	var errs []error
	for range requests {
		r := <-results
		groups = append(groups, r.groups...)
		if r.err != nil {
			errs = append(errs, r.err)
		}
	}
	if len(errs) == len(requests) {
		return nil, errors.Join(errs...)
	}
	return mention.Skills(groups), nil
}

// sessionMentions lists the workspace's other sessions, latest first.
func (a *App) sessionMentions(t mentionTarget) []mention.Item {
	sessions := make([]int, 0, len(a.catalog.Sessions))
	for i, s := range a.catalog.Sessions {
		if s.ID != t.session && !s.Archived {
			sessions = append(sessions, i)
		}
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		return a.catalog.Sessions[sessions[i]].LastMessageAt > a.catalog.Sessions[sessions[j]].LastMessageAt
	})
	items := make([]mention.Item, 0, len(sessions))
	for _, i := range sessions {
		s := a.catalog.Sessions[i]
		project := ""
		if p := a.catalog.Project(s.ProjectID); p != nil {
			project = p.Name
		}
		items = append(items, mention.SessionItem(s.ID, firstNonEmpty(s.Title, locales.UntitledSession()), project))
	}
	return items
}

func (a *App) commandMentions(t mentionTarget) []mention.Item {
	cmds := a.catalog.Capabilities[t.agent].Commands
	names, descs := make([]string, len(cmds)), make([]string, len(cmds))
	for i, cmd := range cmds {
		names[i], descs[i] = cmd.Name, cmd.Description
	}
	return mention.Commands(names, descs)
}

func categoryLabel(ns string) string {
	switch ns {
	case mention.File:
		return locales.MentionFiles()
	case mention.Skill:
		return locales.MentionSkills()
	case mention.Session:
		return locales.MentionSessions()
	}
	return ns
}

// mentionRows lists what matches the trigger, with what is still loading
// or failed when nothing does.
func (a *App) mentionRows(t mentionTarget, tr mention.Trigger) (rows []mentionRow, loading bool, status string) {
	source := func(ns string) ([]mention.Item, bool, string) {
		switch ns {
		case mention.Session:
			return a.sessionMentions(t), false, ""
		case mention.Command:
			return a.commandMentions(t), false, ""
		}
		src := a.mentionSource(ns, t)
		return src.items, src.loading, src.err
	}
	add := func(items []mention.Item) {
		for _, it := range items {
			rows = append(rows, mentionRow{item: it})
		}
	}
	single := func(ns, q string) {
		items, l, err := source(ns)
		add(mention.Match(items, q, 50))
		loading, status = l, err
	}
	switch tr.Sigil {
	case '$':
		single(mention.Skill, tr.Query)
	case '/':
		// The agent's commands, then the workspace's prompt shortcuts.
		single(mention.Command, tr.Query)
		src := a.mentionSource(mentionShortcut, mentionTarget{})
		add(mention.Match(src.items, tr.Query, 50))
		loading = loading || src.loading
	default:
		q := tr.Query
		if ns, rest, ok := strings.Cut(q, ":"); ok && (ns == mention.File || ns == mention.Skill || ns == mention.Session) {
			single(ns, rest)
			break
		}
		categories := []string{mention.File, mention.Skill, mention.Session}
		if q == "" {
			for _, ns := range categories {
				rows = append(rows, mentionRow{category: ns})
			}
			break
		}
		for _, ns := range categories {
			items, l, err := source(ns)
			add(mention.Match(items, q, 4))
			loading = loading || l
			if status == "" {
				status = err
			}
		}
		for _, ns := range categories {
			if strings.Contains(strings.ToLower(categoryLabel(ns)), strings.ToLower(q)) || strings.HasPrefix(ns, strings.ToLower(q)) {
				rows = append(rows, mentionRow{category: ns})
			}
		}
	}
	return rows, loading, status
}

// insertMention puts insert in the trigger's place, with a space after it
// unless keepOpen, and returns the text and where the caret goes.
func insertMention(text string, caret int, tr mention.Trigger, insert string, keepOpen bool) (string, int) {
	r := []rune(text)
	caret = min(max(caret, tr.Start), len(r))
	tail := r[caret:]
	ins := []rune(insert)
	if !keepOpen && (len(tail) == 0 || !unicode.IsSpace(tail[0])) {
		ins = append(ins, ' ')
	}
	pos := tr.Start + len(ins)
	if !keepOpen && len(tail) > 0 && unicode.IsSpace(tail[0]) {
		pos++
	}
	return string(r[:tr.Start]) + string(ins) + string(tail), pos
}

// choose is what a row inserts, and whether the panel stays open for
// more: a category narrows to its items, Tab on a folder to what is in it.
func (row mentionRow) choose(descend bool) (insert string, keepOpen bool) {
	if row.category != "" {
		return "@" + row.category + ":", true
	}
	if descend && row.item.Kind == mention.Directory {
		return "@" + mention.File + ":" + row.item.Path + "/", true
	}
	return row.item.Insert, false
}

// mentionKey handles a key in the composer while the panel is open.
func (a *App) mentionKey(k ui.TextKey) bool {
	m := &a.mention
	if !m.active {
		return false
	}
	switch k.Key {
	case ui.KeyUp, ui.KeyDown:
		if k.Mods != 0 || len(m.rows) == 0 {
			return false
		}
		step := 1
		if k.Key == ui.KeyUp {
			step = -1
		}
		m.highlight = (m.highlight + step + len(m.rows)) % len(m.rows)
		m.reveal = true
		a.redraw()
		return true
	case ui.KeyEscape:
		m.dismissed, m.active = dismissKey(m.text, m.trigger), false
		a.redraw()
		return true
	case ui.KeyEnter, ui.KeyTab:
		if k.Mods != 0 {
			return false
		}
		if len(m.rows) == 0 {
			// Nothing to insert: Enter closes the panel rather than sending.
			m.dismissed, m.active = dismissKey(m.text, m.trigger), false
			a.redraw()
			return true
		}
		text := k.Text()
		caret := m.caret + utf8.RuneCountInString(text) - utf8.RuneCountInString(m.text)
		tr, ok := mention.Find(text, caret)
		if !ok {
			return false
		}
		insert, keepOpen := m.rows[min(m.highlight, len(m.rows)-1)].choose(k.Key == ui.KeyTab)
		text, m.caretTo = insertMention(text, caret, tr, insert, keepOpen)
		m.moveCaret, m.highlight = true, 0
		k.SetText(text)
		return true
	}
	return false
}

func dismissKey(text string, tr mention.Trigger) string {
	return text + "\x00" + string(tr.Sigil) + tr.Query
}

// mentionPanel finds the mention typed before the caret and lists what
// matches it above the composer.
func (a *App) mentionPanel(c *ui.Context, p *palette, box, input ui.Element) {
	m := &a.mention
	if m.moveCaret {
		m.moveCaret = false
		input.SetTextSelection(m.caretTo, m.caretTo)
		m.caret = m.caretTo
	}
	start, end := input.TextSelection()
	tr, ok := mention.Find(a.draft, end)
	if !ok || start != end || !input.Focused() || input.Composing() || dismissKey(a.draft, tr) == m.dismissed {
		m.active, m.rows = false, nil
		return
	}
	if !m.active || tr.Start != m.trigger.Start || tr.Query != m.trigger.Query {
		m.highlight = 0
	}
	m.active, m.trigger, m.text, m.caret = true, tr, a.draft, end
	t := a.mentionTarget()
	rows, loading, status := a.mentionRows(t, tr)
	m.rows = rows
	m.highlight = min(m.highlight, max(len(rows)-1, 0))
	if len(rows) == 0 && !loading && tr.Sigil == '/' {
		// A message that starts with a path, not a command.
		return
	}
	ui.Overlay(c, func() {
		panel := ui.Column(c.Key("mentions")).AttachTo(box, ui.AnchorTopStart, ui.AnchorBottomStart).Margin(6, 0, 0, 0).
			Width(460).Radius(12).Border(0.5, p.CardRim).Background(p.Popover).KeepFocus().Clip().
			Shadow(0, 12, 32, -8, ui.RGBA(0, 0, 0, 0.18)).Transition(popIn)
		panel.Children(func() {
			ui.Scroll(c).MaxHeight(320).Padding(4).Gap(1).Children(func() {
				for i, row := range rows {
					a.mentionRowView(c.Key(row.category+"|"+row.item.Kind+"|"+row.item.Path), p, row, i == m.highlight)
				}
				if len(rows) == 0 || loading {
					ui.Row(c).Height(30).Padding(0, 10).Gap(8).AlignItems(ui.Center).Children(func() {
						switch {
						case loading:
							spinner(c).Size(12, 12)
							ui.Text(c, locales.Loading()).FontSize(sizeSub).TextColor(p.Secondary)
						case status != "":
							ui.Text(c, status).FontSize(sizeSub).TextColor(p.Danger).SingleLine().Grow(1).MinWidth(0)
						default:
							ui.Text(c, locales.MentionNoMatches()).FontSize(sizeSub).TextColor(p.Secondary)
						}
					})
				}
			})
		})
	})
	m.reveal = false
}

func (a *App) mentionRowView(c *ui.Context, p *palette, row mentionRow, highlighted bool) {
	label := categoryLabel(row.category)
	if row.category == "" {
		label = row.item.Name
	}
	r := ui.Row(c).Height(32).Padding(0, 8).Radius(8).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(label).Transition(colorFade)
	switch {
	case highlighted:
		r.Background(p.Text.Alpha(0.07))
		if a.mention.reveal {
			r.ScrollIntoView()
		}
	case r.Hovered():
		r.Background(p.Hover)
	}
	r.OnClick(func() {
		m := &a.mention
		insert, keepOpen := row.choose(false)
		a.draft, m.caretTo = insertMention(m.text, m.caret, m.trigger, insert, keepOpen)
		m.moveCaret, m.highlight = true, 0
	})
	r.Children(func() {
		icon := iconFile
		kind := row.item.Kind
		if row.category != "" {
			kind = row.category
		}
		switch kind {
		case mention.Directory:
			icon = iconFolder
		case mention.Skill:
			icon = iconBoxes
		case mention.Session:
			icon = iconMessageSquare
		case mention.Command:
			icon = iconTerminal
		}
		ui.Icon(c, icon).Size(16, 16).TextColor(p.Secondary).Shrink(0)
		title := label
		switch row.item.Kind {
		case mention.Skill, mention.Command:
			title = row.item.Insert
		}
		ui.Text(c, title).FontSize(sizeSub).TextColor(p.Text).SingleLine().Shrink(0).MaxWidth(240)
		if row.category != "" {
			ui.Spacer(c)
			if row.category == mention.Skill {
				ui.Text(c, "$").Font(fontMono).FontSize(sizeFoot).TextColor(p.Tertiary)
			}
			ui.Icon(c, iconChevronRight).Size(14, 14).TextColor(p.Tertiary)
			return
		}
		ui.Text(c, row.item.Subtitle).FontSize(sizeFoot).TextColor(p.Tertiary).SingleLine().Grow(1).MinWidth(0)
		if row.item.Kind == mention.Directory {
			ui.Icon(c, iconChevronRight).Size(14, 14).TextColor(p.Tertiary)
		}
	})
}

// mentionShortcut names the prompt shortcuts among the mention sources;
// they belong to the workspace, not to a machine.
const mentionShortcut = "shortcut"

// fetchShortcuts lists the shortcuts the composer can send, those without
// a scope, as /shortcut:slug.
func fetchShortcuts(ctx context.Context, store *shortcuts.Store) ([]mention.Item, error) {
	list, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	var items []mention.Item
	for _, e := range list {
		if e.Simple() {
			items = append(items, mention.Item{Kind: mention.Command, Path: e.ID, Name: firstNonEmpty(e.Emoji, "📝") + " " + e.Name, Subtitle: e.Description, Insert: "/shortcut:" + e.Slug})
		}
	}
	return items, nil
}

var shortcutSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// shortcutWords finds the /shortcut:slug words of text: their slugs and
// where each starts and ends.
func shortcutWords(text string) (slugs []string, spans [][2]int) {
	space := func(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }
	for i := 0; i < len(text); i++ {
		if (i > 0 && !space(text[i-1])) || !strings.HasPrefix(text[i:], "/shortcut:") {
			continue
		}
		end := i
		for end < len(text) && !space(text[end]) {
			end++
		}
		if slug := text[i+len("/shortcut:") : end]; shortcutSlug.MatchString(slug) {
			slugs, spans = append(slugs, slug), append(spans, [2]int{i, end})
		}
		i = end
	}
	return slugs, spans
}

// replaceShortcuts puts the prompts of known slugs in their words' place.
func replaceShortcuts(text string, prompts map[string]string) string {
	slugs, spans := shortcutWords(text)
	var b strings.Builder
	last := 0
	for i, sp := range spans {
		prompt, ok := prompts[slugs[i]]
		if !ok {
			continue
		}
		b.WriteString(text[last:sp[0]])
		b.WriteString(prompt)
		last = sp[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

// expandShortcuts puts each /shortcut:slug's prompt in its place, as
// lody-ios does as it sends; a slug it can't resolve stays as typed.
func expandShortcuts(ctx context.Context, store *shortcuts.Store, text string) string {
	slugs, _ := shortcutWords(text)
	if store == nil || len(slugs) == 0 {
		return text
	}
	list, err := store.List(ctx)
	if err != nil {
		return text
	}
	prompts := map[string]string{}
	for _, slug := range slugs {
		if _, done := prompts[slug]; done {
			continue
		}
		var match []shortcuts.Entry
		for _, e := range list {
			if e.Slug == slug && e.Simple() {
				match = append(match, e)
			}
		}
		if len(match) != 1 {
			continue
		}
		if sc, err := store.Read(ctx, match[0]); err == nil {
			prompts[slug] = sc.Prompt
		}
	}
	return replaceShortcuts(text, prompts)
}

// expandMentions rewrites the shortcuts, skills and sessions text
// mentions into what the agent reads, fetching what isn't at hand.
func expandMentions(ctx context.Context, conn *cloud.Conn, user string, t mentionTarget, text string, skills, sessions []mention.Item, store *shortcuts.Store) string {
	text = expandShortcuts(ctx, store, text)
	hasSkills, hasSessions := mention.Tokens(text)
	if !hasSkills && !hasSessions {
		return text
	}
	if hasSkills && skills == nil && conn != nil {
		skills, _ = fetchSkills(ctx, conn, user, t)
	}
	return mention.Expand(text, append(skills, sessions...))
}

// mentionItems is what expandMentions needs, taken on the main thread.
func (a *App) mentionItems(t mentionTarget) (skills, sessions []mention.Item) {
	if src := a.mentionSources[mention.Skill+"|"+t.machine+"|"+t.project]; src != nil && !src.loaded.IsZero() && src.err == "" {
		skills = src.items
		if skills == nil {
			skills = []mention.Item{}
		}
	}
	return skills, a.sessionMentions(t)
}
