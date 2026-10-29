// Package app is the native Lody client: its state, and the views MyGo
// draws from it.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/lbr77/lody-mygo/locales"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/mention"
	"github.com/lbr77/lody-mygo/internal/presence"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/internal/shortcuts"
)

type phase int

const (
	phaseLoading phase = iota
	phaseSignedOut
	phaseSignedIn
)

const secretKey = "better-auth-session"

// App is the client's state. Everything in it belongs to the main thread:
// goroutines hand results back through win.Update.
type App struct {
	win *mygo.Window
	api *cloud.API

	phase   phase
	token   atomic.Value // string; read by network goroutines
	account *cloud.Account

	signIn struct {
		busy   bool
		code   string
		url    string
		err    string
		cancel context.CancelFunc
	}

	workspaceID  string
	conn         *cloud.Conn
	sync         *catalog.Sync
	catalog      catalog.Catalog
	syncState    catalog.State
	synced       bool
	presence     presence.State
	stopPresence context.CancelFunc

	selected string // session id; empty shows New chat
	replica  *session.Replica
	view     *session.View

	drafts    map[string]string
	draft     string // the composer, bound to the open session or New chat
	sending   bool
	sendingID string // the session whose message is on its way
	sendError string

	newChat struct {
		machine, agent, project string
		worktree                bool     // the chat gets a worktree of its own
		branch                  string   // the worktree's base, picked
		mcp                     []string // the MCP servers chosen, when mcpSet
		mcpSet                  bool
		mcpProject              string // the project they were chosen in
	}
	sessionMCP     map[string][]string  // MCP servers chosen for chats' next turns
	homeBoard      string               // the home board open: now, week or none
	homeBoardShown string               // the board shown, as it folds away too
	homeNowOpen    string               // the now list shown whole
	prPanels       map[string]*prPanel  // pull request panels, by session and PR
	plusLevel      string               // the "+" menu's level: "" or "mcp"
	gitStates      map[string]*gitState // projects' Git, by project
	quotaAsked     map[string]time.Time // when each provider's quota was last asked for

	sidebarHidden    bool
	sideOut          float32 // how far the sidebar is out as it slides, 1 shown
	collapsed        map[string]bool
	expanded         map[string]bool // activity groups in the conversation
	workspacesOpen   bool
	sessionList      ui.ListState
	chatList         ui.ListState
	pickers          map[string]*bool
	dither           dither
	renaming         string // session whose title is being edited
	renameText       string
	notice           string // a failed background action, shown in the sidebar
	showArchived     bool
	models           map[string]string // model chosen per session
	composerFocus    ui.Handle
	history          []string // sessions shown, for Back and Forward
	histPos          int
	palette          paletteState
	mdSize           float32 // the text size the Markdown themes were made for
	language         string  // the app's language; "" follows the system
	fontSize         float32 // the conversation's text size; 0 is the default
	wideMode         bool    // the conversation spans the window
	inlineMath       bool
	extendedCode     bool
	ligatures        bool
	codeOnly         bool // sidebar line counts of code alone
	notifications    bool // desktop notifications
	experimental     bool
	keyOverrides     map[string][]string       // chords rebound, by command
	recording        keyRecording              // the command whose chord is being recorded
	runValues        map[string]map[string]any // agent settings chosen in the composer, by session; "" for New chat
	find             findState                 // find in the conversation
	surf, surfNext   surface                   // what the view offers the commands: the last frame's, and this one's
	zen              zenState
	focusComposer    bool              // the composer takes the focus as it builds
	showAll          map[string]bool   // projects listing every session
	appearance       string            // System, Light or Dark
	busyBehavior     string            // queue or steer, for messages sent while the agent works
	modes            map[string]string // permission mode chosen per session; "" for New chat
	attachments      []pendingFile     // the composer's
	attachDrafts     map[string][]pendingFile
	images           map[string]*remoteImage
	uploading        string // upload progress while sending
	settingsOpen     bool
	settingsTab      string
	archiveGroup     string // "project" or "flat"
	archiveSort      string // "newest", "oldest" or "title"
	archiveFilter    string // "", "chats" or a project id
	deleting         *catalog.Session
	deleteOpen       bool
	panelOpen        bool    // the inspector beside the conversation
	panelPercent     float32 // the conversation's share of the width
	panelFile        string  // the file whose edits the panel shows
	inspect          *inspectState
	term             termDock // the terminal dock under the conversation
	previews         map[string]*previewState
	convex           *cloud.Convex
	fetches          map[string]*fetched // what the settings asked the cloud for
	offline          bool                // fetch nothing from the cloud, as in tests
	toggles          map[string]bool     // cloud switches flipped, until they settle
	settingsError    string
	settingsNotice   string // what a settings action did
	avatarBusy       bool
	usageRange       string
	usageCost        bool // the usage charts show cost rather than tokens
	editingName      bool
	nameDraft        string
	agentsMachine    string // the machine whose agents Settings shows
	confirming       confirmation
	mcp              mcpForm
	msgEdit          messageEdit
	forks            map[string]forkPending   // forks being made, by source session
	forkOpen         string                   // a fork to show once the workspace lists it
	goalPending      string                   // a goal command on its way
	goalSeen         string                   // the goal snapshot the banner last showed
	goalDismissed    map[string]string        // cleared goals hidden, by session
	mcpTools         map[string]*mcpTools     // tool lists loaded, by server
	mcpAuthStatus    map[string]mcpAuthResult // servers' sign-ins, as the daemon last said
	general          generalForm
	tokens           tokenForm
	passwordNotice   string
	renamingMachine  string
	machineDraft     string
	provider         providerForm
	memoryMachine    string
	memoryForm       memoryForm
	addProject       addProjectForm
	shortcut         shortcutForm
	monitors         map[string]*machineMonitor // machines observed for their resource use
	role             roleForm
	password         passwordForm
	projectWin       projectWindow
	removal          projectRemoval
	bug              bugReport
	deletion         accountDeletion
	wsCreate         workspaceCreate
	wsJoin           workspaceJoin
	referral         referrals
	pairing          machinePairing
	ghProject        githubProject
	activeDay        string  // workspace|date the user was last counted active
	usageDay         float64 // the usage day opened, its start in ms
	usageDayShown    float64 // the day the panel shows, as it folds away too
	share            shareDialog
	resetting        string                // the share whose link is being reset, in settings
	archiveArm       string                // the row whose archive button asks to be clicked again
	archiveArmAt     time.Time             // since when
	marks            map[string]*markState // the rows' working marks, by session
	membershipSeen   string                // user|fingerprint of the workspaces they belong to
	membershipAt     time.Time             // when it was last asked
	membershipAsking bool
	userNames        map[string]string    // people outside the members list, by id
	shareJobs        map[string]*shareJob // an agent's share requests being answered, by request
	shareReqAt       map[string]time.Time // when each conversation's requests were last read
	ghProfileAsked   map[string]bool      // workspaces whose GitHub profile was refreshed
	device           deviceAuth
	toasts           []ui.Toast // to show with the next frame
	reviewSent       string     // the sharing review last reconciled
	reviewWriter     string
	reviewAttempt    int
	inboxAt          time.Time             // when the inbox was last asked for
	pings            map[string]*pingState // machines' round trips
	sidebarWidth     float32               // as dragged; 0 is the default
	revoking         string                // the share whose revoke awaits confirmation
	taskOpen         bool                  // a task's dialog
	taskEntry        string                // the turn and task it shows
	taskID           string
	page             string // "archive", or "" for chats
	archiveSearch    string
	mention          mentionState // the composer's mention panel
	mentionSources   map[string]*mentionSource
	diffs            map[string]*editor.DiffState // tool diffs of the open session
	md               markdown.Options
	mdMuted          markdown.Options
	mdDark           bool
}

// New returns the app. Run it with Start once the window exists.
func New() *App {
	a := &App{
		api:          &cloud.API{},
		drafts:       map[string]string{},
		models:       map[string]string{},
		runValues:    map[string]map[string]any{},
		diffs:        map[string]*editor.DiffState{},
		showAll:      map[string]bool{},
		modes:        map[string]string{},
		images:       map[string]*remoteImage{},
		attachDrafts: map[string][]pendingFile{},
		collapsed:    map[string]bool{},
		expanded:     map[string]bool{},
		ligatures:    true,
	}
	a.convex = &cloud.Convex{API: a.api, Token: a.tokenNow}
	return a
}

// View is the window's content.
func (a *App) View() *ui.Content { return ui.View(a.render) }

// Start loads the saved session and connects. Call it on the main thread.
func (a *App) Start(win *mygo.Window) {
	a.win = win
	prefs := loadPrefs()
	a.workspaceID = prefs.Workspace
	a.collapsed = prefs.Collapsed
	if a.collapsed == nil {
		a.collapsed = map[string]bool{}
	}
	a.appearance = prefs.Appearance
	a.busyBehavior = prefs.Busy
	a.sidebarWidth = prefs.Sidebar
	a.homeBoard = prefs.HomeBoard
	a.keyOverrides = prefs.Keys
	a.language, a.fontSize, a.wideMode, a.inlineMath, a.extendedCode = prefs.Language, prefs.FontSize, prefs.Wide, prefs.InlineMath, prefs.ExtendedCode
	a.ligatures, a.codeOnly, a.notifications, a.experimental = !prefs.NoLigatures, prefs.CodeOnly, prefs.Notifications, prefs.Experimental
	if a.language != "" {
		mygo.App.SetLocale(a.language)
	}
	if a.appearance != "" {
		applyAppearance(a.appearance)
	}
	go func() {
		token, _ := mygo.Secrets.Get(secretKey)
		cached := loadAccount()
		a.update(func() {
			if len(token) == 0 {
				a.phase = phaseSignedOut
				return
			}
			a.token.Store(string(token))
			if cached != nil {
				// Show the saved catalog at once; the account is checked
				// in the background.
				a.signedIn(cached)
			}
			go a.refreshAccount()
		})
	}()
}

// update runs fn on the main thread and redraws.
func (a *App) update(fn func()) {
	if a.win == nil {
		mygo.RunOnMain(fn)
		return
	}
	a.win.Update(fn)
}

// redraw asks for a frame after a handler changed what the view shows.
func (a *App) redraw() {
	if a.win != nil {
		a.win.Update(func() {})
	}
}

func (a *App) refreshAccount() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	account, err := a.api.Account(ctx, a.tokenNow())
	a.update(func() {
		switch {
		case errors.Is(err, cloud.ErrUnauthorized):
			a.signOutLocal()
		case err != nil:
			if a.account == nil {
				a.phase = phaseSignedOut
				a.signIn.err = locales.SignInUnreachable(err.Error())
			}
		default:
			saveAccount(account)
			a.signedIn(account)
		}
	})
}

func (a *App) tokenNow() string {
	token, _ := a.token.Load().(string)
	return token
}

// beginSignIn starts the device flow: the browser opens on lody.ai, where
// the user approves this device.
func (a *App) beginSignIn() {
	if a.signIn.busy {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	a.signIn.busy, a.signIn.err, a.signIn.code, a.signIn.cancel = true, "", "", cancel
	go func() {
		defer cancel()
		fail := func(err error) {
			a.update(func() {
				a.signIn.busy = false
				a.signIn.code = ""
				if !errors.Is(err, context.Canceled) {
					a.signIn.err = err.Error()
				}
			})
		}
		code, err := a.api.RequestDeviceCode(ctx)
		if err != nil {
			fail(err)
			return
		}
		link, err := code.BrowserURL()
		if err != nil {
			fail(err)
			return
		}
		a.update(func() { a.signIn.code, a.signIn.url = code.UserCode, link })
		mygo.Shell.OpenExternal(link)
		token, err := a.api.PollToken(ctx, code)
		if err != nil {
			fail(err)
			return
		}
		account, err := a.api.Account(ctx, token)
		if err != nil {
			fail(err)
			return
		}
		if err := mygo.Secrets.Set(secretKey, []byte(token)); err != nil {
			fail(errors.New(locales.SignInKeychainFailed(err.Error())))
			return
		}
		saveAccount(account)
		a.update(func() {
			a.signIn.busy, a.signIn.code = false, ""
			a.token.Store(token)
			a.signedIn(account)
		})
	}()
}

func (a *App) cancelSignIn() {
	if a.signIn.cancel != nil {
		a.signIn.cancel()
	}
}

// signedIn shows the account and connects its workspace.
func (a *App) signedIn(account *cloud.Account) {
	a.account = account
	a.phase = phaseSignedIn
	ws := a.workspaceID
	found := false
	for _, w := range account.Workspaces {
		found = found || w.ID == ws
	}
	if !found && len(account.Workspaces) > 0 {
		ws = account.Workspaces[0].ID
	}
	if ws != "" && (ws != a.workspaceID || a.sync == nil) {
		a.connect(ws)
	}
}

func (a *App) workspace() *cloud.Workspace {
	if a.account == nil {
		return nil
	}
	for i := range a.account.Workspaces {
		if a.account.Workspaces[i].ID == a.workspaceID {
			return &a.account.Workspaces[i]
		}
	}
	return nil
}

// connect switches to a workspace and starts syncing its catalog.
func (a *App) connect(id string) {
	a.disconnect()
	a.workspaceID = id
	a.savePrefs()
	a.catalog = loadCatalog(id)
	a.synced = false
	a.conn = &cloud.Conn{
		API:     a.api,
		ID:      id,
		UserID:  a.account.User.ID,
		Session: a.tokenNow,
		HTTP:    &http.Client{Transport: http.DefaultTransport},
	}
	sync := &catalog.Sync{Conn: a.conn}
	sync.OnChange = func(c catalog.Catalog) {
		saveCatalog(id, c)
		a.update(func() {
			if a.sync != sync {
				return
			}
			before := a.catalog
			a.catalog, a.synced = c, true
			a.notifyTransitions(before, c)
			a.pickNewChatDefaults()
		})
	}
	sync.OnState = func(st catalog.State) {
		a.update(func() {
			if a.sync == sync {
				a.syncState = st
			}
		})
	}
	a.sync = sync
	sync.Start()
	ctx, cancel := context.WithCancel(context.Background())
	a.stopPresence = cancel
	go presence.Watch(ctx, a.conn, func(st presence.State) {
		a.update(func() {
			if ctx.Err() == nil {
				a.presence = st
			}
		})
	})
	a.pickNewChatDefaults()
}

func (a *App) disconnect() {
	a.closeSession()
	if a.sync != nil {
		a.sync.Stop()
		a.sync = nil
	}
	if a.stopPresence != nil {
		a.stopPresence()
		a.stopPresence = nil
	}
	a.presence = presence.State{}
	a.selected = ""
	a.catalog = catalog.Catalog{}
	a.syncState = catalog.State{}
}

func (a *App) signOut() {
	token := a.tokenNow()
	go a.api.SignOut(context.Background(), token)
	a.signOutLocal()
}

// signOutLocal forgets the account and everything cached for it.
func (a *App) signOutLocal() {
	a.disconnect()
	mygo.Secrets.Delete(secretKey)
	dir := dataDir()
	os.Remove(filepath.Join(dir, "account.json"))
	os.Remove(filepath.Join(dir, "prefs.json"))
	if cached, err := filepath.Glob(filepath.Join(dir, "catalog-*.json")); err == nil {
		for _, f := range cached {
			os.Remove(f)
		}
	}
	a.drafts = map[string]string{}
	a.token.Store("")
	a.account, a.workspaceID = nil, ""
	a.phase = phaseSignedOut
}

// open shows a session and follows its stream.
// open shows a session, or New chat for "", recording it for Back.
func (a *App) open(id string) {
	a.page = ""
	if a.selected == id && (id == "" || a.replica != nil) {
		return
	}
	if len(a.history) == 0 {
		a.history = []string{a.selected}
	}
	a.history = append(a.history[:a.histPos+1], id)
	a.histPos = len(a.history) - 1
	a.show(id)
}

func (a *App) canGoBack() bool    { return a.histPos > 0 }
func (a *App) canGoForward() bool { return a.histPos < len(a.history)-1 }

func (a *App) goBack() {
	if a.canGoBack() {
		a.histPos--
		a.show(a.history[a.histPos])
	}
}

func (a *App) goForward() {
	if a.canGoForward() {
		a.histPos++
		a.show(a.history[a.histPos])
	}
}

// setAppearance follows the system, or keeps the app light or dark.
func (a *App) setAppearance(choice string) {
	a.appearance = choice
	applyAppearance(choice)
	a.savePrefs()
}

func applyAppearance(choice string) {
	switch choice {
	case "Light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "Dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
}

// show switches to a session and follows its stream.
func (a *App) show(id string) {
	if id != a.selected {
		a.closeFind()
	}
	a.page = ""
	a.msgEdit = messageEdit{}
	a.stashDraft()
	a.closeSession()
	a.selected = id
	a.draft = a.drafts[id]
	a.attachments = a.attachDrafts[id]
	a.sendError = ""
	a.expanded = map[string]bool{}
	a.diffs = map[string]*editor.DiffState{}
	a.chatList = ui.ListState{}
	if id == "" || a.conn == nil {
		return
	}
	var r *session.Replica
	r = session.Open(a.conn, id, func(v *session.View) {
		a.update(func() {
			if a.replica == r {
				a.view = v
			}
		})
	})
	a.replica = r
	if s := a.catalog.Session(id); s != nil && s.Unread() && a.sync != nil {
		sess, sync := *s, a.sync
		go sync.MarkRead(context.Background(), sess)
	}
}

func (a *App) closeSession() {
	if a.replica != nil {
		a.replica.Close()
		a.replica = nil
	}
	a.view = nil
}

func (a *App) stashDraft() {
	if a.draft == "" {
		delete(a.drafts, a.selected)
	} else {
		a.drafts[a.selected] = a.draft
	}
	if len(a.attachments) == 0 {
		delete(a.attachDrafts, a.selected)
	} else {
		a.attachDrafts[a.selected] = a.attachments
	}
}

// steerable reports whether a session's agent takes messages mid-turn.
func (a *App) steerable(s *catalog.Session) bool {
	return a.catalog.Capabilities[s.AgentConfigID].Steer
}

// send sends the composer's text to the open session.
func (a *App) send() {
	text := strings.TrimSpace(a.draft)
	sess := a.catalog.Session(a.selected)
	files := a.attachments
	if (text == "" && len(files) == 0) || a.sending || a.replica == nil || sess == nil || a.conn == nil {
		return
	}
	r, sync, userID, workspace, conn := a.replica, a.sync, a.account.User.ID, a.conn.ID, a.conn
	target := a.mentionTarget()
	skills, sessions := a.mentionItems(target)
	store := a.shortcutStore()
	working := a.view != nil && a.view.Working || sess.Working()
	turn := session.Turn{
		MCPServerIDs:       a.mcpSelection(a.selected),
		Text:               text,
		UserID:             userID,
		MachineID:          sess.MachineID,
		AgentType:          sess.AgentType,
		CLIType:            sess.CLIType,
		Resume:             sess.Resume,
		ModelID:            a.models[a.selected],
		ModeID:             a.modes[a.selected],
		ConfigOptionValues: a.chosenRunValues(a.selected, sess.AgentConfigID, a.currentModel(sess)),
		Queue:              sess.Working(),
		Guide:              working && a.busyBehavior == "steer" && a.steerable(sess),
		MarkDispatch:       sync.MarkDispatch,
	}
	a.recordActivity()
	a.sending, a.sendError, a.uploading = true, "", ""
	a.sendingID = a.selected
	a.draft, a.attachments = "", nil
	delete(a.drafts, a.selected)
	delete(a.attachDrafts, a.selected)
	a.chatList.ScrollToEnd()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		state := session.NotSent
		turn.Text = expandMentions(ctx, conn, userID, target, text, skills, sessions, store)
		blocks, err := a.upload(ctx, workspace, r.ID, files)
		if err == nil {
			turn.Attachments = blocks
			sendCtx, cancelSend := context.WithTimeout(ctx, 60*time.Second)
			state, err = r.Send(sendCtx, turn)
			cancelSend()
		}
		a.update(func() {
			a.sending, a.uploading = false, ""
			if err != nil && state == session.NotSent {
				// Nothing was written: give the message back.
				if a.replica == r && a.draft == "" && len(a.attachments) == 0 {
					a.draft, a.attachments = text, files
				}
				a.sendError = err.Error()
			} else if err != nil {
				a.sendError = locales.SentUnconfirmed()
			}
		})
	}()
}

// steerQueued gives a queued message to the running turn, or with
// interrupt cancels the turn so the queue resumes with it.
func (a *App) steerQueued(id string, interrupt bool) {
	sess := a.catalog.Session(a.selected)
	if a.replica == nil || sess == nil {
		return
	}
	r, machine := a.replica, sess.MachineID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if err := r.SteerQueued(ctx, machine, id, interrupt); err != nil {
			a.update(func() { a.sendError = err.Error() })
		}
	}()
}

// stop cancels the running turn of the open session.
func (a *App) stop() {
	sess := a.catalog.Session(a.selected)
	if a.replica == nil || sess == nil {
		return
	}
	r, machine := a.replica, sess.MachineID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		if err := r.Stop(ctx, machine); err != nil {
			a.update(func() {
				a.sendError = locales.FailStop(err.Error())
				a.checkLive()
			})
		}
	}()
}

func (a *App) answer(entryID string, item session.Item, option session.Option) {
	if a.replica == nil || item.Permission == nil {
		return
	}
	r := a.replica
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := r.Answer(ctx, entryID, item.ID, item.Permission.RequestID, option.ID); err != nil {
			a.update(func() { a.sendError = err.Error() })
		}
	}()
}

// pickNewChatDefaults keeps New chat's machine and agent valid.
func (a *App) pickNewChatDefaults() {
	nc := &a.newChat
	valid := false
	for _, id := range a.catalog.MachineIDs {
		valid = valid || id == nc.machine
	}
	if !valid {
		nc.machine, nc.agent, nc.project = "", "", ""
		if len(a.catalog.MachineIDs) > 0 {
			nc.machine = a.catalog.MachineIDs[0]
		}
	}
	agents := a.catalog.Agents[nc.machine]
	valid = false
	for _, ag := range agents {
		valid = valid || ag.ID == nc.agent
	}
	if !valid {
		nc.agent = ""
		if len(agents) > 0 {
			nc.agent = agents[0].ID
		}
	}
}

func (a *App) agent(machine, id string) *catalog.AgentConfig {
	for _, ag := range a.catalog.Agents[machine] {
		if ag.ID == id {
			return &ag
		}
	}
	return nil
}

// startChat creates a session from New chat and sends its first message.
// recordActivity counts the user active in the workspace today, as Lody
// does as a chat starts or a message goes: best effort, once a day here.
func (a *App) recordActivity() {
	day := a.workspaceID + "|" + time.Now().Format("2006-01-02")
	if a.convex == nil || a.offline || a.workspaceID == "" || a.activeDay == day {
		return
	}
	a.activeDay = day
	convex, ws := a.convex, a.workspaceID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if convex.Mutation(ctx, "activity:recordMyWorkspaceDailyActiveUser", map[string]any{"workspaceId": ws}, nil) != nil {
			a.update(func() {
				if a.activeDay == day {
					a.activeDay = ""
				}
			})
		}
	}()
}

func (a *App) startChat() {
	text := strings.TrimSpace(a.draft)
	nc := a.newChat
	agent := a.agent(nc.machine, nc.agent)
	files := a.attachments
	if (text == "" && len(files) == 0) || a.sending || agent == nil || a.sync == nil || a.conn == nil {
		return
	}

	a.recordActivity()
	id := uuid.NewString()
	userID := a.account.User.ID
	target := a.mentionTarget()
	skills, sessions := a.mentionItems(target)
	store := a.shortcutStore()
	model, mode := a.models[""], a.modes[""]
	runValues := a.chosenRunValues("", agent.ID, model)
	mcp := a.mcpSelection("")
	title := draftTitle(text)
	if title == "" && len(files) > 0 {
		title = draftTitle(files[0].Name)
	}
	localProject, githubProject := "", ""
	worktree, branch, repo := false, "", ""
	if r, ok := strings.CutPrefix(nc.project, "github:"); ok {
		githubProject = r
	}
	if p := a.catalog.Project(nc.project); p != nil {
		_, localProject, _ = strings.Cut(p.ID, ":local:")
		if st := a.gitStates[p.ID]; localProject != "" && st != nil && st.git {
			repo = st.repo
			if nc.worktree {
				worktree, branch = true, a.newChatBranch(st)
			}
		}
	}
	a.createAndSend(catalog.NewSession{
		ID: id, MachineID: nc.machine, UserID: userID, Title: title,
		Agent: *agent, LocalProject: localProject, CreatedAt: time.Now(), GitHubProject: githubProject,
		Worktree: worktree, Branch: branch, GitHubRepo: repo,
	}, chatSend{key: "", text: text, files: files, model: model, mode: mode, runValues: runValues, mcp: mcp, target: target, skills: skills, sessions: sessions, store: store}, nil)
}

// chatSend is a first message: what it says and how it runs.
type chatSend struct {
	key         string // the composer's, for its draft
	text        string
	files       []pendingFile
	model, mode string
	runValues   map[string]any
	mcp         []string
	target      mentionTarget
	skills      []mention.Item
	sessions    []mention.Item
	store       *shortcuts.Store
}

// createAndSend creates a session and sends its first message; created
// runs once it exists, before it opens.
func (a *App) createAndSend(n catalog.NewSession, m chatSend, created func()) {
	sync, userID, workspace, conn := a.sync, a.account.User.ID, a.conn.ID, a.conn
	id, agent, text, files := n.ID, n.Agent, m.text, m.files
	a.sending, a.sendError, a.uploading = true, "", ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		err := sync.CreateSession(ctx, n)
		if err != nil {
			a.update(func() { a.sending, a.sendError = false, locales.FailCreate(err.Error()) })
			return
		}
		opened := make(chan *session.Replica, 1)
		a.update(func() {
			a.draft, a.attachments = "", nil
			if created != nil {
				created()
			}
			a.open(id)
			opened <- a.replica
		})
		r := <-opened
		if r == nil {
			return
		}
		state := session.NotSent
		var blocks []map[string]any
		if blocks, err = a.upload(ctx, workspace, id, files); err == nil {
			if err = r.WaitReady(ctx); err == nil {
				state, err = r.Send(ctx, session.Turn{
					MCPServerIDs: m.mcp,
					Text:         expandMentions(ctx, conn, userID, m.target, text, m.skills, m.sessions, m.store), UserID: userID, MachineID: n.MachineID, ModelID: m.model, ModeID: m.mode, Attachments: blocks,
					AgentType: agent.AgentType, CLIType: agent.CLIType, MarkDispatch: sync.MarkDispatch, ConfigOptionValues: m.runValues,
				})
			}
		}
		a.update(func() {
			a.sending, a.uploading = false, ""
			if err != nil && state == session.NotSent {
				if a.draft == "" && len(a.attachments) == 0 {
					a.draft, a.attachments = text, files
				}
				a.sendError = err.Error()
			}
		})
	}()
}

// draftTitle is the first line, cut to 24 characters, as Lody titles a new
// session until the agent names it.
func draftTitle(text string) string {
	line := strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	if utf8.RuneCountInString(line) <= 24 {
		return line
	}
	return string([]rune(line)[:24]) + "…"
}

// Persistence: preferences, the account and catalogs as JSON in the app's
// data directory; the session token in the keychain.

type prefs struct {
	Workspace  string          `json:"workspace"`
	Collapsed  map[string]bool `json:"collapsed,omitempty"`
	Appearance string          `json:"appearance,omitempty"`
	Busy       string          `json:"busy,omitempty"`
	Sidebar    float32         `json:"sidebar,omitempty"` // the sidebar's width
	HomeBoard  string          `json:"homeBoard,omitempty"`
	// Keys are the chords rebound in keyboard settings, by command: none
	// unbinds it.
	Keys map[string][]string `json:"keys,omitempty"`

	Language      string  `json:"language,omitempty"`
	FontSize      float32 `json:"fontSize,omitempty"`
	Wide          bool    `json:"wide,omitempty"`
	InlineMath    bool    `json:"inlineMath,omitempty"`
	ExtendedCode  bool    `json:"extendedCode,omitempty"`
	NoLigatures   bool    `json:"noLigatures,omitempty"`
	CodeOnly      bool    `json:"codeOnly,omitempty"`
	Notifications bool    `json:"notifications,omitempty"`
	Experimental  bool    `json:"experimental,omitempty"`
}

func dataDir() string {
	// Tests keep what they write in a folder of their own.
	if dir := os.Getenv("LODY_DATA_DIR"); dir != "" {
		return dir
	}
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		dir = filepath.Join(os.TempDir(), "lody-mygo")
	}
	return dir
}

func readJSON(name string, v any) bool {
	b, err := os.ReadFile(filepath.Join(dataDir(), name))
	return err == nil && json.Unmarshal(b, v) == nil
}

func writeJSON(name string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	dir := dataDir()
	os.MkdirAll(dir, 0o700)
	tmp := filepath.Join(dir, name+".tmp")
	if os.WriteFile(tmp, b, 0o600) == nil {
		os.Rename(tmp, filepath.Join(dir, name))
	}
}

func loadPrefs() prefs {
	var p prefs
	readJSON("prefs.json", &p)
	return p
}

func (a *App) savePrefs() {
	writeJSON("prefs.json", prefs{Workspace: a.workspaceID, Collapsed: a.collapsed, Appearance: a.appearance, Busy: a.busyBehavior, Sidebar: a.sidebarWidth, HomeBoard: a.homeBoard, Keys: a.keyOverrides,
		Language: a.language, FontSize: a.fontSize, Wide: a.wideMode, InlineMath: a.inlineMath, ExtendedCode: a.extendedCode,
		NoLigatures: !a.ligatures, CodeOnly: a.codeOnly, Notifications: a.notifications, Experimental: a.experimental})
}

func loadAccount() *cloud.Account {
	var acc cloud.Account
	if !readJSON("account.json", &acc) || acc.User.ID == "" {
		return nil
	}
	return &acc
}

func saveAccount(acc *cloud.Account) { writeJSON("account.json", acc) }

func catalogFile(ws string) string { return "catalog-" + filepath.Base(ws) + ".json" }

func loadCatalog(ws string) catalog.Catalog {
	var c catalog.Catalog
	readJSON(catalogFile(ws), &c)
	return c
}

func saveCatalog(ws string, c catalog.Catalog) { writeJSON(catalogFile(ws), c) }
