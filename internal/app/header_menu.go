package app

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// The conversation header's ⋯ is Lody's session menu, in its order: who
// and where the chat is (its repository, branch or folder, each copied on
// a click; its machine and reach), then Full width, what opened it and
// what it opened, Share, Find, the forks, Rename, the owner, sharing with
// the team, the copies, and archiving.

// headerMenu builds the menu of the open chat s.
func (a *App) headerMenu(m *ui.Menu, s catalog.Session) {
	copied := func(text, message string) {
		writeClipboard(text)
		a.toast("success", message)
	}
	kind, _ := s.Project["kind"].(string)
	repo := s.RepoFullName
	if strings.HasPrefix(s.ProjectID, "github:") {
		repo = strings.TrimPrefix(s.ProjectID, "github:")
	}
	if r, _ := s.Project["githubRepoFullName"].(string); repo == "" {
		repo = r
	}
	repo = strings.TrimSpace(repo)
	github := kind == "github" || repo != ""
	base := strings.TrimSpace(s.BaseBranch)
	if b, _ := s.Project["branch"].(string); base == "" {
		base = strings.TrimSpace(b)
	}
	current := strings.TrimSpace(s.BranchName)
	showBranch := (github || kind == "local") && (base != "" || current != "")
	branch := firstNonEmpty(current, base)
	baseContext := current != "" && base != "" && current != base
	root := ""
	if pr := a.catalog.Project(s.ProjectID); pr != nil {
		root = pr.RootPath
	}
	showPath := !showBranch && root != ""
	machine := ""
	if s.MachineID != "" {
		machine = a.machineName(s.MachineID)
	}
	access := a.sessionAccess(&s)

	// Who and where.
	if github && repo != "" || showBranch || showPath || machine != "" || access != nil {
		if github && repo != "" && m.Item(repo).Chosen() {
			copied(repo, locales.HmRepoCopied())
		}
		switch {
		case showBranch:
			label := branch
			if baseContext {
				label = locales.HmBaseBranchLine(branch, base)
			}
			if m.Item(label).Chosen() {
				if current != "" {
					copied(branch, locales.CmdBranchCopied())
				} else {
					copied(branch, locales.HmBaseBranchCopied())
				}
			}
		case showPath:
			if m.Item(root).Chosen() {
				copied(root, locales.HmProjectPathCopied())
			}
		}
		if machine != "" {
			line := locales.HmMachineLine(machine)
			if kind == "local" {
				where := locales.WorkdirLocal()
				if a.sessionWorktree(&s) {
					where = locales.WorkdirWorktree()
				}
				line = locales.HmMachineLineKind(machine, where)
			}
			m.Item(line).Disabled(true)
		}
		if access != nil {
			label := locales.AccessChecking()
			switch access.visibility {
			case "team":
				label = locales.AccessTeamLabel()
			case "private":
				label = locales.AccessPrivate()
			}
			m.Item(label).Disabled(true)
		}
		m.Separator()
	}

	// The page's own display option.
	if m.Item(locales.PrefWide()).Checked(a.wideMode).Chosen() {
		a.wideMode = !a.wideMode
		a.savePrefs()
	}

	// What opened this chat, and what it opened.
	if s.OpenedBySessionID != "" {
		opener := a.catalog.Session(s.OpenedBySessionID)
		title := locales.UntitledSession()
		if opener != nil {
			title = firstNonEmpty(opener.Title, title)
		}
		if m.Item(locales.HmOpenedBy(title)).Disabled(opener == nil).Chosen() {
			a.open(s.OpenedBySessionID)
		}
	}
	var opened []catalog.Session
	for _, o := range a.catalog.Sessions {
		if o.OpenedBySessionID == s.ID && !o.Archived {
			opened = append(opened, o)
		}
	}
	if len(opened) > 0 {
		m.Submenu(locales.HmOpenedSessions(len(opened)), func(m *ui.Menu) {
			for _, o := range opened {
				if m.Item(firstNonEmpty(o.Title, locales.UntitledSession())).Chosen() {
					a.open(o.ID)
				}
			}
		})
	}

	if !a.noCloud() && a.account != nil && m.Item(locales.ShareTitle()).Chosen() {
		a.openShare(s.ID)
	}
	if m.Item(locales.FindInSession()).Chosen() {
		a.openFind()
	}

	// The forks, from the last finished reply, and the context to carry.
	turn := a.lastFinishedTurn()
	canFork := !s.Archived && turn != "" && a.sessionForkable(&s)
	if canFork || a.view != nil {
		_, forking := a.forks[s.ID]
		m.Submenu(locales.ForkSession(), func(m *ui.Menu) {
			if canFork {
				if m.Item(locales.ForkNewTab()).Disabled(forking).Chosen() {
					a.fork(turn, false, false, "")
				}
				if s.ProjectID != "" && m.Item(locales.ForkNewWorktree()).Disabled(forking).Chosen() {
					a.fork(turn, true, false, "")
				}
			}
			if m.Item(locales.HmCopyContext()).Disabled(a.view == nil).Chosen() {
				a.copyConversation(s.ID)
			}
		})
	}

	if !s.Archived && m.Item(locales.HmRenameChat()).Chosen() {
		a.renaming, a.renameText = s.ID, s.Title
	}

	// The owner, in a workspace of more than one.
	if members, mf := a.members(); !s.Archived && mf != nil && len(members) > 1 {
		m.Submenu(locales.HmChangeOwner(), func(m *ui.Menu) {
			for _, id := range sortedMemberIDs(members) {
				name := a.memberName(id, members)
				if m.Item(name).Radio(id == s.UserID).Chosen() && id != s.UserID {
					a.changeOwner(s.ID, id, name)
				}
			}
		})
	}

	// Sharing with the team, while it isn't.
	if access != nil && access.visibility != "team" {
		label := locales.AccessShareWithTeam()
		disabled := access.visibility == "unknown" || access.reason == "machine-not-registered" || !access.canManage
		switch {
		case access.visibility == "unknown":
			label = locales.HmCheckingSharing()
		case access.reason == "machine-not-registered":
			label = locales.AccessRegisterDevice()
		case !access.canManage:
			label = locales.AccessOnlyOwner()
		}
		if m.Item(label).Disabled(disabled).Chosen() {
			a.shareWithTeam(&s, access)
		}
	}

	m.Submenu(locales.Copy(), func(m *ui.Menu) {
		if baseContext {
			if m.Item(locales.HmCopyBaseBranch()).Chosen() {
				copied(base, locales.HmBaseBranchCopied())
			}
			m.Separator()
		}
		path := a.sessionWorkspacePath(&s)
		if m.Item(locales.HmCopyPath()).Disabled(path == "").Chosen() {
			copied(path, locales.HmPathCopied())
		}
		if m.Item(locales.AccessCopyMarkdown()).Disabled(a.view == nil).Chosen() {
			a.copyConversation(s.ID)
		}
		if m.Item(locales.AccessCopyURL()).Chosen() {
			a.copySessionLink(s.ID)
		}
	})

	m.Separator()
	if s.Archived {
		if m.Item(locales.HmRestore()).Chosen() {
			id := s.ID
			a.catalogAction(locales.FailRestore(), func(ctx context.Context, sync *catalog.Sync) error {
				return sync.Archive(ctx, id, false)
			})
		}
		if m.Item(locales.ArchiveDelete()).Chosen() {
			sess := s
			a.deleting, a.deleteOpen = &sess, true
		}
		return
	}
	if m.Item(locales.HmArchive()).Chosen() {
		a.archiveSession(s.ID)
	}
}

// lastFinishedTurn is the conversation's last finished reply, which a fork
// of the whole chat starts from.
func (a *App) lastFinishedTurn() string {
	if a.view == nil {
		return ""
	}
	for i := len(a.view.Entries) - 1; i >= 0; i-- {
		if e := a.view.Entries[i]; e.Role == "assistant" && e.Finished {
			return e.ID
		}
	}
	return ""
}

// changeOwner hands a chat to another member, as Lody does: its meta's
// userId is the owner.
func (a *App) changeOwner(sessionID, userID, name string) {
	sync := a.sync
	if sync == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := sync.SetField(ctx, sessionID, "userId", userID)
		a.update(func() {
			if err != nil {
				a.toast("error", locales.HmOwnerChangeFailed())
				return
			}
			a.toast("success", locales.HmOwnerChanged(name))
		})
	}()
}

// sortedMemberIDs are the members' user ids, in a stable order.
func sortedMemberIDs[T any](members map[string]T) []string {
	ids := make([]string, 0, len(members))
	for id := range members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
