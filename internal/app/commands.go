package app

import (
	"context"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// Lody's command registry: each application shortcut is a command, which
// the keyboard runs and the ⌘K palette lists while its when holds. Local
// keys (Escape in a dialog, Enter in the composer) stay with their views.

type commandCategory int

// The palette's order, with no query.
const (
	catNavigation commandCategory = iota
	catSession
	catEditor
	catView
	catWorkspace
	catHelp
	catOther
)

func (k commandCategory) label() string {
	switch k {
	case catNavigation:
		return locales.CmdCatNavigation()
	case catSession:
		return locales.CmdCatSession()
	case catEditor:
		return locales.CmdCatEditor()
	case catView:
		return locales.CmdCatView()
	case catWorkspace:
		return locales.CmdCatWorkspace()
	case catHelp:
		return locales.CmdCatHelp()
	}
	return locales.CmdCatOther()
}

type command struct {
	id       string
	title    string
	category commandCategory
	keys     []chord // the first is the one shown
	icon     *ui.SVG
	when     bool
	run      func()
	defaults []chord // the chords it ships with
}

// chord is a key binding in Lody's registry syntax, as "Mod+Shift+p": Mod
// is Command on macOS and Control elsewhere.
type chord struct {
	mods ui.Modifiers
	key  ui.Key
	spec string
	// local chords are the view's own, as ⇧Tab in the composer: shown, not
	// bound app-wide.
	local bool
}

var chordKeys = map[string]ui.Key{
	"[": ui.KeyBracketLeft, "]": ui.KeyBracketRight, ",": ui.KeyComma, ".": ui.KeyPeriod, "`": ui.KeyBackquote,
	"-": ui.KeyMinus, "=": ui.KeyEqual, "/": ui.KeySlash, ";": ui.KeySemicolon, "'": ui.KeyQuote, "\\": ui.KeyBackslash,
	"F1": ui.KeyF1, "F2": ui.KeyF2, "F3": ui.KeyF3, "F4": ui.KeyF4, "F5": ui.KeyF5, "F6": ui.KeyF6,
	"F7": ui.KeyF7, "F8": ui.KeyF8, "F9": ui.KeyF9, "F10": ui.KeyF10, "F11": ui.KeyF11, "F12": ui.KeyF12,
	"Tab": ui.KeyTab, "Enter": ui.KeyEnter, "Space": ui.KeySpace, "Backspace": ui.KeyBackspace, "Delete": ui.KeyDelete,
	"ArrowUp": ui.KeyUp, "ArrowDown": ui.KeyDown, "ArrowLeft": ui.KeyLeft, "ArrowRight": ui.KeyRight,
	"0": ui.Key0, "1": ui.Key1, "2": ui.Key2, "3": ui.Key3, "4": ui.Key4, "5": ui.Key5, "6": ui.Key6, "7": ui.Key7, "8": ui.Key8, "9": ui.Key9,
}

// chordName is a key's name in a chord, "" for one that can't be bound.
func chordName(k ui.Key) string {
	if k >= ui.KeyA && k <= ui.KeyZ {
		return string(rune('a' + k - ui.KeyA))
	}
	for name, key := range chordKeys {
		if key == k {
			return name
		}
	}
	return ""
}

// chordSpec writes a chord in the registry's syntax, Mod for the
// platform's command key.
func chordSpec(mods ui.Modifiers, k ui.Key) string {
	var parts []string
	if mods&ui.Cmd != 0 {
		parts = append(parts, "Mod")
	}
	if mods&ui.Ctrl != 0 && ui.Cmd != ui.Ctrl {
		parts = append(parts, "Ctrl")
	}
	if mods&ui.Alt != 0 {
		parts = append(parts, "Alt")
	}
	if mods&ui.Shift != 0 {
		parts = append(parts, "Shift")
	}
	return strings.Join(append(parts, chordName(k)), "+")
}

func keyChord(spec string) chord {
	ch := chord{spec: spec}
	for _, part := range strings.Split(spec, "+") {
		switch part {
		case "Mod":
			ch.mods |= ui.Cmd
		case "Ctrl":
			ch.mods |= ui.Ctrl
		case "Alt":
			ch.mods |= ui.Alt
		case "Shift":
			ch.mods |= ui.Shift
		default:
			if k, ok := chordKeys[part]; ok {
				ch.key = k
			} else if len(part) == 1 && part[0] >= 'a' && part[0] <= 'z' {
				ch.key = ui.KeyA + ui.Key(part[0]-'a')
			}
		}
	}
	return ch
}

func keyChords(specs ...string) []chord {
	out := make([]chord, len(specs))
	for i, s := range specs {
		out[i] = keyChord(s)
	}
	return out
}

// parts are a chord's keys as the platform writes them, in the order
// Lody's registry has them: ⌘ ⌥ A on macOS, Ctrl Alt A elsewhere.
func (ch chord) parts() []string {
	mac := runtime.GOOS == "darwin"
	var out []string
	for _, part := range strings.Split(ch.spec, "+") {
		switch {
		case part == "Mod" && mac:
			part = "⌘"
		case part == "Mod":
			part = "Ctrl"
		case part == "Ctrl" && mac:
			part = "⌃"
		case part == "Alt" && mac:
			part = "⌥"
		case part == "Shift" && mac:
			part = "⇧"
		case part == "Tab" && mac:
			part = "⇥"
		case part == "Enter":
			part = "↩"
		case part == "ArrowUp":
			part = "↑"
		case part == "ArrowDown":
			part = "↓"
		case part == "ArrowLeft":
			part = "←"
		case part == "ArrowRight":
			part = "→"
		default:
			part = strings.ToUpper(part[:1]) + part[1:]
		}
		out = append(out, part)
	}
	return out
}

// writeClipboard copies text for the commands; tests swap it.
var writeClipboard = mygo.Clipboard.WriteText

// surface is what the view on screen offers the commands, published by
// its parts as they build: Lody's mounted command implementations.
type surface struct {
	composer    bool
	save        func()
	mode        *cycleTarget
	model       *cycleTarget
	provider    *cycleTarget
	effort      *cycleTarget
	termFocused bool
}

// cycleTarget is a composer option the commands step through.
type cycleTarget struct {
	values  []string
	current string
	pick    func(string)
}

func (t *cycleTarget) can() bool { return t != nil && len(t.values) > 1 }

func (t *cycleTarget) next() {
	if !t.can() {
		return
	}
	i := -1
	for j, v := range t.values {
		if v == t.current {
			i = j
		}
	}
	if next := t.values[(i+1)%len(t.values)]; next != t.current {
		t.pick(next)
	}
}

// commands are the registry as the app stands, each with its when.
func (a *App) commands() []command {
	sess := a.catalog.Session(a.selected)
	if a.page != "" {
		sess = nil
	}
	live := sess != nil && !sess.Archived
	tabs := len(a.orderedTabs(a.tabRoot(sess)))
	branch := ""
	if sess != nil {
		branch = strings.TrimSpace(sess.BranchName)
	}
	visible := a.visibleSessionIDs()
	s := a.surf
	cmds := []command{
		{id: "palette.toggle", title: locales.CmdPaletteToggle(), category: catView, keys: keyChords("Mod+k", "Mod+Shift+p"), icon: iconCommand, when: true,
			run: func() { a.setPaletteOpen(!a.palette.open) }},
		{id: "nav.back", title: locales.CmdNavBack(), category: catNavigation, keys: keyChords("Mod+["), icon: iconArrowLeft, when: true, run: a.goBack},
		{id: "nav.forward", title: locales.CmdNavForward(), category: catNavigation, keys: keyChords("Mod+]"), icon: iconArrowRight, when: true, run: a.goForward},
		{id: "app.cycleTheme", title: locales.CmdCycleTheme(), category: catView, icon: iconSunMoon, when: true, run: a.cycleTheme},
		{id: "layout.toggleZenMode", title: locales.CmdToggleZen(), category: catView, keys: keyChords("Mod+."), icon: iconFocus, when: true, run: a.toggleZen},
		{id: "schedules.open", title: locales.CmdSchedulesOpen(), category: catWorkspace, icon: iconListTodo, when: a.workspaceID != "",
			run: func() { a.page = "schedules" }},
		{id: "schedules.new", title: locales.CmdSchedulesNew(), category: catWorkspace, icon: iconCalendarClock, when: a.workspaceID != "",
			run: func() {
				a.page = "schedules"
				a.newSchedule()
			}},
		{id: "schedules.pause", title: locales.CmdSchedulesPause(), category: catWorkspace, icon: iconPause, when: a.pausableSchedule() != nil,
			run: func() {
				if row := a.pausableSchedule(); row != nil {
					a.toggleSchedule(*row)
				}
			}},
		{id: "workspace.openSettings", title: locales.CmdOpenSettings(), category: catWorkspace, keys: keyChords("Mod+,"), icon: iconSettings, when: a.workspaceID != "",
			run: func() { a.settingsOpen = !a.settingsOpen }},
		{id: "session.new", title: locales.CmdSessionNew(), category: catSession, keys: keyChords("Mod+n"), icon: iconSquarePen, when: a.workspaceID != "",
			run: func() { a.open("") }},
		{id: "sidebar.toggle", title: locales.SidebarToggle(), category: catView, keys: keyChords("Mod+b"), icon: iconSidebar, when: true, run: a.toggleSidebar},
		{id: "session.previousVisible", title: locales.CmdPreviousVisible(), category: catNavigation, keys: keyChords("Mod+Shift+["), icon: iconArrowUp,
			when: !a.palette.open && len(visible) > 0, run: func() { a.stepVisibleSession(-1) }},
		{id: "session.nextVisible", title: locales.CmdNextVisible(), category: catNavigation, keys: keyChords("Mod+Shift+]"), icon: iconArrowDown,
			when: !a.palette.open && len(visible) > 0, run: func() { a.stepVisibleSession(1) }},
		{id: "session.focusInput", title: locales.CmdFocusInput(), category: catEditor, keys: keyChords("Mod+l"), icon: iconTextCursor, when: s.composer,
			run: func() { a.focusComposer = true }},
		{id: "session.saveCurrentFile", title: locales.CmdSaveFile(), category: catEditor, keys: keyChords("Mod+s"), icon: iconSave, when: s.save != nil,
			run: func() {
				if save := a.surf.save; save != nil {
					save()
				}
			}},
		{id: "session.cycleMode", title: locales.CmdCycleMode(), category: catSession, keys: []chord{{mods: ui.Shift, key: ui.KeyTab, spec: "Shift+Tab", local: true}},
			icon: iconShieldCheck, when: s.mode.can(), run: func() { a.surf.mode.next() }},
		{id: "session.cycleProvider", title: locales.CmdCycleProvider(), category: catSession, icon: iconBot, when: s.provider.can(), run: func() { a.surf.provider.next() }},
		{id: "session.cycleModel", title: locales.CmdCycleModel(), category: catSession, icon: iconSparkles, when: s.model.can(), run: func() { a.surf.model.next() }},
		{id: "mention.toggleSessionProjectScope", title: locales.CmdToggleMentionScope(), category: catEditor, icon: iconAtSign, when: a.mention.sessionMentionLevel(),
			run: func() { a.mention.allProjects = !a.mention.allProjects }},
		{id: "session.cycleThinkEffort", title: locales.CmdCycleThinkEffort(), category: catSession, icon: iconBrain, when: s.effort.can(), run: func() { a.surf.effort.next() }},
	}
	// A session's commands are listed without one too, as Lody's
	// placeholders, so settings can show every shortcut.
	id, open := "", sess != nil
	if open {
		id = sess.ID
	}
	cmds = append(cmds,
		command{id: "session.archiveCurrent", title: locales.CmdArchiveCurrent(), category: catSession, keys: keyChords("Mod+Alt+a"), icon: iconArchive, when: live,
			run: func() { a.archiveSession(id) }},
		command{id: "session.toggleCurrentPinned", title: locales.CmdTogglePinned(), category: catSession, keys: keyChords("Mod+Alt+p"), icon: iconPin, when: live,
			run: func() { a.togglePinned(id) }},
		command{id: "session.searchCurrent", title: locales.CmdSearchCurrent(), category: catSession, keys: keyChords("Mod+f"), icon: iconTextSearch, when: open,
			run: a.openFind},
		command{id: "session.toggleExplorerSidebar", title: locales.CmdToggleExplorer(), category: catView, keys: keyChords("Mod+Alt+b"), icon: iconPanelRight, when: open,
			run: a.togglePanel},
		command{id: "session.newTabOrTerminal", title: locales.CmdNewTabOrTerminal(), category: catSession, keys: keyChords("Alt+n", "Mod+t"), icon: iconPlus, when: open,
			run: func() { a.newTabOrTerminal(id) }},
		command{id: "session.toggleTerminal", title: locales.CmdToggleTerminal(), category: catView, keys: keyChords("Ctrl+`", "Mod+j"), icon: iconSquareTerminal,
			when: open && a.localMachine() != "", run: a.toggleTerminal},
		command{id: "session.copyCurrentBranch", title: locales.CmdCopyBranch(), category: catSession, keys: keyChords("Alt+Shift+b"), icon: iconBranch, when: branch != "",
			run: func() {
				writeClipboard(branch)
				a.toast("success", locales.CmdBranchCopied())
			}},
		command{id: "session.copyUrl", title: locales.CmdCopyURL(), category: catSession, keys: keyChords("Alt+Shift+c"), icon: iconLink, when: open,
			run: func() { a.copySessionLink(id) }},
		command{id: "session.renameCurrent", title: locales.CmdRenameCurrent(), category: catSession, keys: keyChords("F2"), icon: iconPencil, when: live,
			run: func() {
				a.sidebarHidden = false
				a.renaming, a.renameText = id, a.catalog.Session(id).Title
			}},
		command{id: "session.nextTab", title: locales.CmdNextTab(), category: catNavigation, keys: keyChords("Mod+Shift+."), icon: iconChevronsRight, when: tabs > 1, run: func() { a.stepTab(1) }},
		command{id: "session.previousTab", title: locales.CmdPreviousTab(), category: catNavigation, keys: keyChords("Mod+Shift+,"), icon: iconChevronsLeft, when: tabs > 1, run: func() { a.stepTab(-1) }},
	)
	// ⌘1–⌘8 jump to a tab, ⌘9 to the last.
	for n := 1; n <= 8; n++ {
		index := n - 1
		cmds = append(cmds, command{id: "session.switchToTab" + string(rune('0'+n)), title: locales.CmdSwitchToTab(n), category: catNavigation,
			keys: keyChords("Mod+" + string(rune('0'+n))), icon: iconRectangle, when: tabs > n-1, run: func() { a.switchToTab(index) }})
	}
	cmds = append(cmds, command{id: "session.switchToLastTab", title: locales.CmdSwitchToLastTab(), category: catNavigation, keys: keyChords("Mod+9"),
		icon: iconArrowToLine, when: tabs > 0, run: func() { a.switchToTab(-1) }})
	// A rebound command takes the user's chords in place of its own, bound
	// everywhere, as Lody's overrides drop a default's binding-level when.
	for i := range cmds {
		cmds[i].defaults = cmds[i].keys
		if keys, ok := a.keyOverrides[cmds[i].id]; ok {
			cmds[i].keys = keyChords(keys...)
		}
	}
	return cmds
}

// runCommand runs a command if it still applies.
func (a *App) runCommand(id string) {
	for _, cmd := range a.commands() {
		if cmd.id == id && cmd.when {
			cmd.run()
			return
		}
	}
}

// bindCommands binds every command's chords for the window, except while
// a chord is being recorded, which takes every key.
func (a *App) bindCommands(c *ui.Context) {
	if a.recording.id != "" {
		return
	}
	for _, cmd := range a.commands() {
		id := cmd.id
		for _, ch := range cmd.keys {
			if !ch.local {
				c.OnShortcut(ch.mods, ch.key, func() { a.runCommand(id) })
			}
		}
	}
}

// toggleSidebar shows or hides the sidebar; in Zen it leaves Zen with the
// sidebar shown, as Lody's.
func (a *App) toggleSidebar() {
	if a.zen.on {
		a.zen.on, a.sidebarHidden = false, false
		return
	}
	a.sidebarHidden = !a.sidebarHidden
}

func (a *App) togglePanel() {
	if a.zen.on {
		a.zen.on, a.panelOpen = false, true
		return
	}
	a.panelOpen = !a.panelOpen
}

// zenState is Lody's Zen layout: the sidebar and the panel away, as they
// were before it.
type zenState struct {
	on                       bool
	sidebarHidden, panelOpen bool
}

// toggleZen enters or leaves Zen. A layout already bare reads as Zen, so
// the first press reveals it.
func (a *App) toggleZen() {
	switch {
	case a.zen.on:
		a.zen.on = false
		a.sidebarHidden, a.panelOpen = a.zen.sidebarHidden, a.zen.panelOpen
	case a.sidebarHidden && !a.panelOpen:
		a.sidebarHidden = false
		a.panelOpen = a.selected != ""
	default:
		a.zen = zenState{on: true, sidebarHidden: a.sidebarHidden, panelOpen: a.panelOpen}
		a.sidebarHidden, a.panelOpen = true, false
	}
}

// cycleTheme steps Light, Dark, System, as Lody's.
func (a *App) cycleTheme() {
	next := map[string]string{"Light": "Dark", "Dark": "System"}[a.appearance]
	if next == "" {
		next = "Light"
	}
	a.setAppearance(next)
}

// visibleSessionIDs are the sessions the sidebar lists, in its order.
func (a *App) visibleSessionIDs() []string {
	var ids []string
	for _, r := range a.sideRows() {
		if r.kind == rowSession && r.session != nil {
			ids = append(ids, r.session.ID)
		}
	}
	return ids
}

// stepVisibleSession opens the session above or below the one shown.
func (a *App) stepVisibleSession(step int) {
	ids := a.visibleSessionIDs()
	if len(ids) == 0 {
		return
	}
	i := -1
	for j, id := range ids {
		if id == a.selected {
			i = j
		}
	}
	next := min(max(i+step, 0), len(ids)-1)
	if i < 0 && step < 0 {
		next = 0
	}
	if ids[next] != a.selected {
		a.open(ids[next])
	}
}

func (a *App) archiveSession(id string) {
	if a.selected == id {
		a.open("")
	}
	a.catalogAction(locales.FailArchive(), func(ctx context.Context, sync *catalog.Sync) error {
		return sync.Archive(ctx, id, true)
	})
}

func (a *App) togglePinned(id string) {
	s := a.catalog.Session(id)
	if s == nil {
		return
	}
	pinned := !s.Pinned
	a.catalogAction(locales.FailPin(), func(ctx context.Context, sync *catalog.Sync) error {
		return sync.Pin(ctx, id, pinned)
	})
}

// copySessionLink copies Lody's link to a session, which opens it in the
// desktop app.
func (a *App) copySessionLink(id string) {
	link := "lody://session/" + id
	if a.workspaceID != "" {
		link += "?workspace=" + a.workspaceID
	}
	writeClipboard(link)
	a.toast("success", locales.CmdURLCopied())
}

// newTabOrTerminal opens a terminal while one has the focus; otherwise a
// new chat where the session runs, with its agent, as Lody's new tab.
func (a *App) newTabOrTerminal(id string) {
	sess := a.catalog.Session(id)
	if sess == nil {
		return
	}
	if a.surf.termFocused {
		a.newTerminal(id, a.termSession(id))
		return
	}
	a.newTab()
}
