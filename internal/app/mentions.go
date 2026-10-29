package app

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/github"
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
	// The level shown: the category and folder narrowed to. A change
	// slides the list 12px from the side it goes to.
	level    string
	levelDir float32
	levelRun uint64
	// allProjects lists sessions of every project, not only the current
	// one's, until the panel closes.
	allProjects bool
}

// sessionScopeKey is the project a session counts as for mentions: its
// own, or "chat" for one outside any.
func sessionScopeKey(project string) string {
	if strings.Contains(project, ":local:") || strings.HasPrefix(project, "github:") {
		return project
	}
	return "chat"
}

// sessionMentionLevel is whether the panel lists sessions.
func (m *mentionState) sessionMentionLevel() bool {
	return m.active && m.trigger.Sigil == '@' && strings.HasPrefix(m.trigger.Query, mention.Session+":")
}

// mentionLevel is how deep a query has narrowed: to a category, then to
// folders within it.
func mentionLevel(query string) string {
	if i := strings.LastIndexAny(query, ":/"); i >= 0 {
		return query[:i+1]
	}
	return ""
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

// fetchFiles lists a local project's files, or the files of a GitHub
// project's chat worktree, as lody-ios asks.
func fetchFiles(ctx context.Context, conn *cloud.Conn, user string, t mentionTarget) ([]mention.Item, error) {
	var req map[string]any
	if id := t.localProject(); id != "" {
		req = map[string]any{"type": "local-project/list-files", "localProjectId": id, "maxFiles": mention.MaxFiles, "requestedByUserId": user}
	} else if repo, ok := strings.CutPrefix(t.project, "github:"); ok && t.session != "" {
		req = map[string]any{"type": "worktree/list-files", "repoFullName": repo, "sessionId": t.session, "maxFiles": mention.MaxFiles, "requestedByUserId": user}
	}
	if req == nil {
		return nil, nil
	}
	raw, err := conn.ProjectControl(ctx, t.machine, req, 15*time.Second)
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

// sessionMentions lists the workspace's other sessions, latest first:
// the current project's unless the list covers all, or all is asked for.
func (a *App) sessionMentions(t mentionTarget, all bool) []mention.Item {
	sessions := make([]int, 0, len(a.catalog.Sessions))
	for i, s := range a.catalog.Sessions {
		if s.ID != t.session && !s.Archived && (all || a.mention.allProjects || sessionScopeKey(s.ProjectID) == sessionScopeKey(t.project)) {
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
	case mention.Role:
		return locales.MentionRoles()
	case mention.Issue:
		return locales.MentionIssues()
	case mention.PR:
		return locales.MentionPullRequests()
	case mention.Shortcut:
		return locales.MentionShortcuts()
	case mention.Command:
		return locales.MentionCommands()
	}
	return ns
}

// mentionRows lists what matches the trigger, with what is still loading
// or failed when nothing does.
func (a *App) mentionRows(t mentionTarget, tr mention.Trigger) (rows []mentionRow, loading bool, status string) {
	source := func(ns string) ([]mention.Item, bool, string) {
		switch ns {
		case mention.Session:
			return a.sessionMentions(t, false), false, ""
		case mention.Role:
			return a.roleMentions(t), false, ""
		case mention.Issue, mention.PR:
			src := a.issueSource(a.mentionRepo(t))
			var items []mention.Item
			for _, it := range src.items {
				if it.Kind == ns {
					items = append(items, it)
				}
			}
			return items, src.loading, src.err
		case mention.Shortcut:
			src := a.mentionSource(mentionShortcut, mentionTarget{})
			return src.items, src.loading, src.err
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
		categories := a.atCategories(t)
		if ns, rest, ok := strings.Cut(q, ":"); ok && slices.Contains(categories, ns) {
			single(ns, rest)
			break
		}
		if q == "" {
			for _, ns := range categories {
				rows = append(rows, mentionRow{category: ns})
			}
			break
		}
		for _, ns := range []string{mention.File, mention.Skill, mention.Session, mention.Role} {
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
		m.active, m.rows, m.allProjects = false, nil, false
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
	if level := mentionLevel(tr.Query); level != m.level {
		m.levelDir = 1
		if len(level) < len(m.level) {
			m.levelDir = -1
		}
		m.level = level
		m.levelRun++
	}
	ui.Overlay(c, func() {
		// The categories alone are a short menu; what they list is wider.
		width := float32(460)
		if len(rows) > 0 && rows[0].category != "" && rows[len(rows)-1].category != "" {
			width = 220
		}
		panel := ui.Column(c.Key("mentions")).AttachTo(box, ui.AnchorTopStart, ui.AnchorBottomStart).Margin(6, 0, 0, 0).
			Width(width).Radius(12).Border(0.5, p.CardRim).Background(p.Popover).KeepFocus().Clip().
			Shadow(0, 12, 32, -8, ui.RGBA(0, 0, 0, 0.18))
		// It rises 4px into place from the side it landed on as it fades
		// in, and closes at once, as Lody's mention menu.
		away := float32(-4)
		if pb, bb := panel.Bounds(), box.Bounds(); pb.H > 0 && pb.Y >= bb.Y+bb.H-1 {
			away = 4 // it landed below the box
		}
		if k := panel.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: popTime, Value: 1, Ease: easeStandard}}); k < 1 {
			panel.Translate(0, away*(1-k)).Opacity(k)
		}
		panel.Children(func() {
			list := ui.Scroll(c).MaxHeight(320).Padding(4).Gap(1)
			if k := list.Keyframes("level", []ui.Keyframe{{Value: 0}, {At: popTime, Value: 1, Ease: easeStandard}}, ui.KeyframeOptions{Run: m.levelRun}); m.levelRun > 1 && k < 1 {
				list.Translate(12*m.levelDir*(1-k), 0).Opacity(k)
			}
			list.Children(func() {
				if m.sessionMentionLevel() {
					a.sessionScopeHeader(c, p, t)
				}
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
						case m.sessionMentionLevel() && !m.allProjects:
							empty := locales.MentionSessionEmptyCurrent()
							if sessionScopeKey(t.project) == "chat" {
								empty = locales.MentionSessionEmptyNone()
							}
							ui.Text(c, empty).FontSize(sizeSub).TextColor(p.Secondary).Grow(1).MinWidth(0)
							ui.Text(c, locales.MentionSessionViewAll()).FontSize(sizeSub).FontWeight(500).TextColor(p.Link).Cursor(ui.CursorPointer).
								Label(locales.MentionSessionShowAll()).OnClick(func() { m.allProjects = true })
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
	// Instant, as a popup row's highlight: it keeps up with the arrow keys.
	r := ui.Row(c).Height(32).Padding(0, 8).Radius(8).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(label)
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
		case mention.Command, mention.Shortcut:
			icon = iconTerminal
		case mention.Role:
			icon = iconUserCog
		case mention.Issue:
			icon = iconCircleDot
		case mention.PR:
			icon = iconGitPR
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
			// The key that opens the category without the menu.
			switch row.category {
			case mention.Skill:
				ui.Text(c, "$").Font(fontMono).FontSize(sizeFoot).TextColor(p.Tertiary)
			case mention.Shortcut, mention.Command:
				ui.Text(c, "/").Font(fontMono).FontSize(sizeFoot).TextColor(p.Tertiary)
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

// expandMentions rewrites the shortcuts, skills, sessions and roles text
// mentions into what the agent reads, fetching what isn't at hand, with
// the spans of what they became, which transcripts show as chips.
func expandMentions(ctx context.Context, conn *cloud.Conn, user string, t mentionTarget, text string, skills, sessions []mention.Item, store *shortcuts.Store) (string, []map[string]any) {
	text = expandShortcuts(ctx, store, text)
	hasSkills, hasSessions := mention.Tokens(text)
	if !hasSkills && !hasSessions {
		return text, nil
	}
	if hasSkills && skills == nil && conn != nil {
		skills, _ = fetchSkills(ctx, conn, user, t)
	}
	workspace := ""
	if conn != nil {
		workspace = conn.ID
	}
	out, spans := mention.ExpandSpans(text, append(skills, sessions...), workspace)
	var maps []map[string]any
	for _, s := range spans {
		maps = append(maps, s.Map())
	}
	return out, maps
}

// mentionItems is what expandMentions needs, taken on the main thread.
func (a *App) mentionItems(t mentionTarget) (skills, sessions []mention.Item) {
	if src := a.mentionSources[mention.Skill+"|"+t.machine+"|"+t.project]; src != nil && !src.loaded.IsZero() && src.err == "" {
		skills = src.items
		if skills == nil {
			skills = []mention.Item{}
		}
	}
	// Every session and role, so what was picked from all projects expands.
	return skills, append(a.sessionMentions(t, true), a.roleMentions(t)...)
}

// roleMentions lists the agent roles the user can use, one per machine a
// role runs on, the composer's machine first, as Lody's.
func (a *App) roleMentions(t mentionTarget) []mention.Item {
	me := ""
	if a.account != nil {
		me = a.account.User.ID
	}
	roles := append([]catalog.AgentRole(nil), a.catalog.Roles...)
	sort.SliceStable(roles, func(i, j int) bool { return roles[i].Name < roles[j].Name })
	var out []mention.Item
	for _, r := range roles {
		if r.Visibility != "workspace" && r.OwnerUserID != me {
			continue
		}
		targets := append([]catalog.RoleTarget(nil), r.Targets...)
		sort.SliceStable(targets, func(i, j int) bool { return targets[i].MachineID == t.machine && targets[j].MachineID != t.machine })
		if len(targets) <= 1 {
			out = append(out, mention.RoleItem(r.ID, r.Name, firstNonEmpty(r.Emoji, "🤖")+" "+r.Name, r.Description, "", firstNonEmpty(r.Emoji, "🤖")))
			continue
		}
		for _, tg := range targets {
			out = append(out, mention.RoleItem(r.ID, r.Name, firstNonEmpty(r.Emoji, "🤖")+" "+r.Name+" · "+a.machineLabel(tg.MachineID), r.Description, tg.MachineID, firstNonEmpty(r.Emoji, "🤖")))
		}
	}
	return out
}

// atCategories are what @ reaches, in Lody's order: files, skills,
// sessions, agent roles, prompt shortcuts, and the agent's commands when
// it has some.
func (a *App) atCategories(t mentionTarget) []string {
	out := []string{mention.File}
	if a.mentionRepo(t) != "" {
		out = append(out, mention.Issue, mention.PR)
	}
	out = append(out, mention.Skill, mention.Session, mention.Role, mention.Shortcut)
	if len(a.catalog.Capabilities[t.agent].Commands) > 0 {
		out = append(out, mention.Command)
	}
	return out
}

// sessionScopeHeader picks the projects the session list covers: the
// current one, or all.
func (a *App) sessionScopeHeader(c *ui.Context, p *palette, t mentionTarget) {
	m := &a.mention
	current := locales.MentionSessionScopeCurrent()
	if sessionScopeKey(t.project) == "chat" {
		current = locales.MentionSessionScopeNone()
	}
	ui.Row(c.Key("scope")).Padding(2, 4, 4, 4).Gap(2).Label(locales.MentionSessionScope()).Children(func() {
		for _, o := range []struct {
			label string
			all   bool
		}{{current, false}, {locales.MentionSessionScopeAll(), true}} {
			all := o.all
			b := ui.ButtonBase(c.Key(o.label)).Height(24).Padding(0, 8).Radius(6).Cursor(ui.CursorPointer).Label(o.label).Transition(colorFade).
				OnClick(func() { m.allProjects = all })
			selected := m.allProjects == all
			if selected {
				b.Background(p.Text.Alpha(0.07))
			}
			b.Children(func() {
				ink := p.Secondary
				if selected {
					ink = p.Text
				}
				ui.Text(c, o.label).FontSize(sizeFoot).FontWeight(500).TextColor(ink)
			})
		}
	})
}

// mentionRepo is the GitHub repository the composer's project is, for
// its issues and pull requests: a GitHub project's, or the remote of a
// local one.
func (a *App) mentionRepo(t mentionTarget) string {
	if repo, ok := strings.CutPrefix(t.project, "github:"); ok {
		return repo
	}
	if s := a.catalog.Session(t.session); s != nil {
		if repo := firstNonEmpty(s.RepoFullName, str(s.Project["githubRepoFullName"]), str(s.Project["repoFullName"])); repo != "" {
			return repo
		}
	}
	if st := a.gitStates[t.project]; st != nil {
		return st.repo
	}
	return ""
}

// issueSource is a repository's open issues and pull requests, read with
// the workspace's token and kept five minutes, as Lody's.
func (a *App) issueSource(repo string) *mentionSource {
	if a.mentionSources == nil {
		a.mentionSources = map[string]*mentionSource{}
	}
	key := "issuepr|" + repo
	src := a.mentionSources[key]
	if src == nil {
		src = &mentionSource{}
		a.mentionSources[key] = src
	}
	if repo == "" || src.loading || (!src.loaded.IsZero() && time.Since(src.loaded) < 5*time.Minute) || a.noCloud() {
		return src
	}
	src.loading, src.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var items []mention.Item
		token, err := a.githubToken(ctx, repo, false)
		if err == nil {
			var list []github.IssueOrPR
			if list, err = (&github.Client{Token: token}).IssuesAndPRs(ctx, repo); err == nil {
				for _, it := range list {
					items = append(items, mention.IssueItem(it.Number, it.Title, it.PR))
				}
			}
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
