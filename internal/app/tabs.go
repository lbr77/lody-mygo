package app

import (
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// Conversation tabs, as Lody's: a session and the child conversations it
// holds in its workspace, each a tab; New Tab is a draft until its first
// message makes it a child, and a closed tab only leaves the strip.

type draftTab struct {
	id    string // the session the draft becomes
	agent string
}

type tabGroup struct {
	drafts []draftTab
	closed map[string]bool
}

// tabRoot is the session whose tabs a session sits among: its parent,
// for a child conversation shown as a tab.
func (a *App) tabRoot(s *catalog.Session) *catalog.Session {
	if s == nil {
		return nil
	}
	if s.ParentSessionID != "" && s.ChildPlacement != "side-panel" {
		if parent := a.catalog.Session(s.ParentSessionID); parent != nil {
			return parent
		}
	}
	return s
}

func (a *App) tabGroupFor(root string) *tabGroup {
	if a.tabGroups == nil {
		a.tabGroups = map[string]*tabGroup{}
	}
	g := a.tabGroups[root]
	if g == nil {
		g = &tabGroup{closed: map[string]bool{}}
		a.tabGroups[root] = g
	}
	return g
}

// orderedTabs are a root's tabs: itself, its open child conversations,
// oldest first, then its drafts.
func (a *App) orderedTabs(root *catalog.Session) []string {
	if root == nil {
		return nil
	}
	g := a.tabGroupFor(root.ID)
	ids := []string{root.ID}
	var children []*catalog.Session
	for i := range a.catalog.Sessions {
		s := &a.catalog.Sessions[i]
		if s.ParentSessionID == root.ID && s.ChildPlacement != "side-panel" && !s.Archived && !g.closed[s.ID] {
			children = append(children, s)
		}
	}
	sort.SliceStable(children, func(i, j int) bool { return children[i].CreatedAt < children[j].CreatedAt })
	for _, s := range children {
		ids = append(ids, s.ID)
	}
	for _, d := range g.drafts {
		ids = append(ids, d.id)
	}
	return ids
}

// activeTab is the tab shown: the draft being written, else the session.
func (a *App) activeTab() string {
	if a.activeDraft != "" {
		return a.activeDraft
	}
	return a.selected
}

func (a *App) draftFor(root, id string) *draftTab {
	g := a.tabGroupFor(root)
	for i := range g.drafts {
		if g.drafts[i].id == id {
			return &g.drafts[i]
		}
	}
	return nil
}

// showTab shows a tab: a draft over its root, or a conversation.
func (a *App) showTab(root *catalog.Session, id string) {
	if root == nil {
		return
	}
	if a.draftFor(root.ID, id) != nil {
		if a.selected != root.ID && a.tabRoot(a.catalog.Session(a.selected)) != root {
			a.open(root.ID)
		}
		a.stashDraft()
		a.activeDraft = id
		a.draft, a.attachments = a.drafts[id], a.attachDrafts[id]
		return
	}
	a.activeDraft = ""
	a.tabGroupFor(root.ID).closed[id] = false
	a.open(id)
}

// newTab opens a draft tab beside the session shown, with its agent.
func (a *App) newTab() {
	root := a.tabRoot(a.catalog.Session(a.selected))
	if root == nil || root.Archived {
		return
	}
	g := a.tabGroupFor(root.ID)
	d := draftTab{id: uuid.NewString(), agent: root.AgentConfigID}
	g.drafts = append(g.drafts, d)
	a.stashDraft()
	a.activeDraft = d.id
	a.draft, a.attachments = "", nil
	a.focusComposer = true
}

// closeTab takes a tab out of the strip, showing the one before it.
func (a *App) closeTab(root *catalog.Session, id string) {
	if root == nil || id == root.ID {
		return
	}
	tabs := a.orderedTabs(root)
	at := -1
	for i, t := range tabs {
		if t == id {
			at = i
		}
	}
	g := a.tabGroupFor(root.ID)
	if d := a.draftFor(root.ID, id); d != nil {
		for i := range g.drafts {
			if g.drafts[i].id == id {
				g.drafts = append(g.drafts[:i], g.drafts[i+1:]...)
				break
			}
		}
	} else {
		g.closed[id] = true
	}
	if a.activeTab() == id && at > 0 {
		a.showTab(root, tabs[at-1])
	}
}

// stepTab shows the tab step from the shown one, round the strip.
func (a *App) stepTab(step int) {
	root := a.tabRoot(a.catalog.Session(a.selected))
	tabs := a.orderedTabs(root)
	if len(tabs) < 2 {
		return
	}
	at := 0
	for i, t := range tabs {
		if t == a.activeTab() {
			at = i
		}
	}
	a.showTab(root, tabs[(at+step+len(tabs))%len(tabs)])
}

// switchToTab shows the n-th tab, or the last for n < 0.
func (a *App) switchToTab(n int) {
	root := a.tabRoot(a.catalog.Session(a.selected))
	tabs := a.orderedTabs(root)
	if len(tabs) == 0 {
		return
	}
	if n < 0 || n >= len(tabs) {
		n = len(tabs) - 1
	}
	if tabs[n] != a.activeTab() {
		a.showTab(root, tabs[n])
	}
}

// tabStrip is the conversation's tabs and New Tab, in the bar over it.
func (a *App) tabStrip(c *ui.Context, p *palette, sess *catalog.Session) {
	root := a.tabRoot(sess)
	tabs := a.orderedTabs(root)
	solo := len(tabs) == 1
	ui.Row(c.Key("tabs")).Gap(2).AlignItems(ui.Center).MinWidth(0).Shrink(1).Children(func() {
		for _, id := range tabs {
			tabID := id
			active := tabID == a.activeTab()
			title, s := locales.TabNew(), a.catalog.Session(tabID)
			if s != nil {
				title = firstNonEmpty(s.Title, locales.UntitledSession())
			}
			tab := ui.Row(c.Key("tab/"+tabID)).Height(32).Radius(10).Padding(0, 6, 0, 12).Gap(6).Border(1, ui.Transparent).AlignItems(ui.Center).
				MinWidth(0).Shrink(1).MaxWidth(240).Cursor(ui.CursorPointer).Label(title).Transition(ui.ElementTransition{Colors: true, Size: true, Duration: 150 * time.Millisecond, Ease: ui.EaseOut, Enter: &ui.Motion{Collapse: true}, Exit: &ui.Motion{Collapse: true}})
			hovered := tab.Hovered()
			ink := p.Secondary
			switch {
			case solo:
				ink = p.Text
			case active:
				tab.Background(p.Canvas).Border(1, p.Border)
				ink = p.Text
			case hovered:
				tab.Background(p.Hover)
				ink = p.Text
			}
			tab.OnClick(func() { a.showTab(root, tabID) })
			tab.Children(func() {
				ui.Box(c).Size(12, 12).Center().Shrink(0).Children(func() {
					switch {
					case s == nil:
						ui.Icon(c, iconSquarePen).Size(12, 12).TextColor(ink)
					case s.Waiting():
						ui.Icon(c, iconHand).Size(12, 12).TextColor(p.Warning)
					case s.Working():
						spinner(c).Size(12, 12)
					case s.Unread() && !active:
						ui.Box(c).Size(8, 8).Radius(4).Background(p.Primary)
					default:
						ui.Icon(c, iconBot).Size(12, 12).TextColor(ink).Opacity(0.6)
					}
				})
				ui.Text(c, title).FontSize(12.6).TextColor(ink).SingleLine().MinWidth(0).Shrink(1)
				if tabID != root.ID {
					// Close shows with the pointer, as Lody's.
					x := ui.ButtonBase(c.Key("close")).Size(18, 18).Radius(4).Center().Cursor(ui.CursorPointer).Label(locales.TabClose()).Tooltip(locales.TabClose()).
						OnClick(func() { a.closeTab(root, tabID) })
					if !hovered && !active {
						x.Opacity(0)
					} else if x.Hovered() {
						x.Background(p.Text.Alpha(0.1))
					}
					x.Children(func() { ui.Icon(c, iconX).Size(11, 11).TextColor(ink) })
				} else {
					ui.Box(c).Width(6)
				}
			})
		}
		iconButton(c, p, iconPlus, locales.TabNew(), p.Hover).Disabled(root == nil || root.Archived).OnClick(a.newTab)
	})
}

// draftView is a draft tab: what to ask of the branch, over the composer
// that sends the first message and makes it a child conversation.
func (a *App) draftView(c *ui.Context, p *palette, root *catalog.Session, d *draftTab) {
	ui.Column(c.Key("draft/"+d.id)).Grow(1).MinHeight(0).Justify(ui.End).Padding(0, 0, 16, 0).Transition(pageIn).Children(func() {
		a.column(c, "suggestions", func() {
			ui.Column(c).FillWidth().AlignItems(ui.End).Gap(4).Children(func() {
				for _, s := range []string{locales.TabSuggestReview(), locales.TabSuggestSummarize(), locales.TabSuggestSimplify()} {
					text := s
					b := ui.ButtonBase(c.Key(s)).Height(30).Padding(0, 12).Radius(8).Gap(8).AlignItems(ui.Center).Border(1, p.Border.Alpha(0.4)).
						Cursor(ui.CursorPointer).Label(s).Transition(colorFade)
					if b.Hovered() {
						b.Background(p.Hover).Border(1, p.Border)
					}
					b.OnClick(func() { a.draft = text })
					b.Children(func() { ui.Text(c, text).FontSize(sizeFoot).TextColor(p.Secondary) })
				}
			})
		})
	})
	agent := a.agent(root.MachineID, d.agent)
	a.composerBox(c, p, composerOptions{
		sessionID: d.id,
		canSend:   agent != nil && !a.sending,
		onSend:    func() { a.sendDraftTab(root, d) },
		footer: func() {
			if agent == nil {
				return
			}
			pickModel := func(id string) { a.models[d.id] = id }
			a.runConfig(c, p, "model/"+d.id, agent.ID, a.models[d.id], nil, pickModel)
			a.runOptions(c, p, d.id, agent.ID, a.models[d.id], nil)
			modes := a.catalog.ModesFor(agent.ID, agent.AgentType)
			pickMode := func(id string) { a.modes[d.id] = id }
			a.modeButton(c, p, "mode/"+d.id, modes, a.modes[d.id], pickMode)
		},
	})
}

// sendDraftTab makes a draft its root's child conversation, in the root's
// workspace, and sends its first message there.
func (a *App) sendDraftTab(root *catalog.Session, d *draftTab) {
	text := strings.TrimSpace(a.draft)
	files := a.attachments
	agent := a.agent(root.MachineID, d.agent)
	if (text == "" && len(files) == 0) || a.sending || agent == nil || a.sync == nil || a.conn == nil {
		return
	}
	a.recordActivity()
	id, rootID := d.id, root.ID
	target := mentionTarget{machine: root.MachineID, project: root.ProjectID, session: id, agent: agent.ID}
	skills, sessions := a.mentionItems(target)
	title := draftTitle(text)
	if title == "" && len(files) > 0 {
		title = draftTitle(files[0].Name)
	}
	a.createAndSend(catalog.NewSession{
		ID: id, MachineID: root.MachineID, UserID: a.account.User.ID, Title: title, Agent: *agent, CreatedAt: time.Now(),
		ParentSessionID: root.ID, Project: root.Project, RepoFullName: root.RepoFullName, BaseBranch: root.BaseBranch,
	}, chatSend{key: id, text: text, files: files, model: a.models[id], mode: a.modes[id], runValues: a.chosenRunValues(id, agent.ID, a.models[id]),
		mcp: a.mcpSelection(rootID), target: target, skills: skills, sessions: sessions, store: a.shortcutStore()}, func() {
		// The draft is a child now: its tab stays, a conversation.
		g := a.tabGroupFor(rootID)
		for i := range g.drafts {
			if g.drafts[i].id == id {
				g.drafts = append(g.drafts[:i], g.drafts[i+1:]...)
				break
			}
		}
		a.activeDraft = ""
	})
}
