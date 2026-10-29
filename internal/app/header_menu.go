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

// headerMenu builds the menu of the open chat s, drawn as Lody's: 290
// wide at most, its rows 32 high.
func (a *App) headerMenu(c *ui.Context, p *palette, s catalog.Session) {
	button := iconButton(c.Key("header-menu"), p, iconMore, locales.MoreActions(), p.Hover)
	lodyMenuButton(c, p, button, &a.hmenu, 200, 290, 32, func(m *lodyMenu) { a.headerMenuRows(m, s) })
}

// headerMenuRows are the menu's rows.
func (a *App) headerMenuRows(m *lodyMenu, s catalog.Session) {
	c, p := m.c, m.p
	copied := func(text, message string) {
		writeClipboard(text)
		a.toast("success", message)
	}
	copyMark := func() { ui.Icon(c, iconCopy).Size(12, 12).TextColor(p.Secondary).Shrink(0) }
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

	// Who and where: identity stays inline, each copied on a click.
	if github && repo != "" || showBranch || showPath || machine != "" || access != nil {
		if github && repo != "" && m.item("repo", iconGithub, repo, menuItem{tip: repo, end: copyMark}) {
			copied(repo, locales.HmRepoCopied())
		}
		switch {
		case showBranch:
			o := menuItem{tip: branch, end: copyMark}
			if baseContext {
				o.tip = branch + "\n" + locales.HmBaseBranchOnly(base)
				o.detail = locales.HmBaseBranchOnly(base)
			}
			if m.item("branch", iconBranch, branch, o) {
				if current != "" {
					copied(branch, locales.CmdBranchCopied())
				} else {
					copied(branch, locales.HmBaseBranchCopied())
				}
			}
		case showPath:
			if m.item("path", iconFolder, root, menuItem{tip: root, end: copyMark}) {
				copied(root, locales.HmProjectPathCopied())
			}
		}
		if machine != "" {
			m.static("machine", iconMonitor, machine, func() {
				if kind != "local" {
					return
				}
				where := locales.WorkdirLocal()
				if a.sessionWorktree(&s) {
					where = locales.WorkdirWorktree()
				}
				ui.Text(c, where).FontSize(sizeCaption).FontWeight(500).TextColor(p.Secondary).Padding(1, 6).Radius(6).
					Background(p.Text.Alpha(0.06)).Shrink(0)
			}).Label(locales.HmMachineLine(machine))
		}
		if access != nil {
			icon, label := iconLockKeyhole, locales.AccessChecking()
			switch access.visibility {
			case "team":
				icon, label = iconUsers, locales.AccessTeamLabel()
			case "private":
				label = locales.AccessPrivate()
			}
			row := m.static("access", icon, label, nil)
			lodyTooltip(c, p, row, access.description())
		}
		m.sep()
	}

	// The page's own display option, a switch that stays open.
	wide := a.wideMode
	if m.item("wide", iconMoveHorizontal, locales.PrefWide(), menuItem{keepOpen: true, end: func() { menuSwitch(c, p, wide) }}) {
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
		if m.item("opened-by", iconGitBranchPlus, locales.HmOpenedBy(title), menuItem{disabled: opener == nil}) {
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
		m.submenu("opened", iconGitBranchPlus, locales.HmOpenedSessions(len(opened)), menuItem{}, 200, func(sm *lodyMenu) {
			for _, o := range opened {
				if sm.item("open/"+o.ID, nil, firstNonEmpty(o.Title, locales.UntitledSession()), menuItem{}) {
					a.open(o.ID)
				}
			}
		})
	}

	if !a.noCloud() && a.account != nil && m.item("share", iconShare, locales.ShareTitle(), menuItem{}) {
		a.openShare(s.ID)
	}
	if m.item("find", iconSearch, locales.FindInSession(), menuItem{}) {
		a.openFind()
	}

	// The forks, from the last finished reply, and the context to carry.
	turn := a.lastFinishedTurn()
	canFork := !s.Archived && turn != "" && a.sessionForkable(&s)
	if canFork || a.view != nil {
		_, forking := a.forks[s.ID]
		icon := iconGitFork
		m.submenu("fork", icon, locales.ForkSession(), menuItem{}, 208, func(sm *lodyMenu) {
			if canFork {
				if sm.item("fork-tab", iconFolder, locales.ForkNewTab(), menuItem{disabled: forking, tip: locales.ForkNewTabHint()}) {
					a.fork(turn, false, false, "")
				}
				if s.ProjectID != "" && sm.item("fork-worktree", iconWorktree, locales.ForkNewWorktree(), menuItem{disabled: forking, tip: locales.ForkNewWorktreeHint()}) {
					a.fork(turn, true, false, "")
				}
			}
			if sm.item("copy-context", iconCopy, locales.HmCopyContext(), menuItem{disabled: a.view == nil}) {
				a.copyConversation(s.ID)
			}
		})
	}

	if !s.Archived && m.item("rename", iconPencil, locales.HmRenameChat(), menuItem{}) {
		a.renaming, a.renameText = s.ID, s.Title
	}

	// The owner, in a workspace of more than one.
	if members, mf := a.members(); !s.Archived && mf != nil && len(members) > 1 {
		m.submenu("owner", iconUserRoundCog, locales.HmChangeOwner(), menuItem{}, 200, func(sm *lodyMenu) {
			for _, id := range sortedMemberIDs(members) {
				name := a.memberName(id, members)
				owner := id == s.UserID
				end := func() {
					if owner {
						ui.Icon(c, iconCheck).Size(14, 14).TextColor(p.Text).Shrink(0)
					}
				}
				if sm.item("owner/"+id, nil, name, menuItem{end: end}) && !owner {
					a.changeOwner(s.ID, id, name)
				}
			}
		})
	}

	// Sharing with the team, while it isn't.
	if access != nil && access.visibility != "team" {
		label, icon := locales.AccessShareWithTeam(), iconUsers
		disabled := access.visibility == "unknown" || access.reason == "machine-not-registered" || !access.canManage
		switch {
		case access.visibility == "unknown":
			label = locales.HmCheckingSharing()
		case access.reason == "machine-not-registered":
			label, icon = locales.AccessRegisterDevice(), iconMonitor
		case !access.canManage:
			label, icon = locales.AccessOnlyOwner(), iconLockKeyhole
		}
		if m.item("share-team", icon, label, menuItem{disabled: disabled}) {
			a.shareWithTeam(&s, access)
		}
	}

	m.submenu("copy", iconCopy, locales.Copy(), menuItem{}, 200, func(sm *lodyMenu) {
		if baseContext {
			if sm.item("copy-base", iconBranch, locales.HmCopyBaseBranch(), menuItem{tip: base}) {
				copied(base, locales.HmBaseBranchCopied())
			}
			sm.sep()
		}
		path := a.sessionWorkspacePath(&s)
		if sm.item("copy-path", iconCopy, locales.HmCopyPath(), menuItem{disabled: path == "", tip: firstNonEmpty(path, locales.HmPathUnavailable())}) {
			copied(path, locales.HmPathCopied())
		}
		if sm.item("copy-markdown", iconCopy, locales.AccessCopyMarkdown(), menuItem{disabled: a.view == nil}) {
			a.copyConversation(s.ID)
		}
		if sm.item("copy-url", iconCopy, locales.AccessCopyURL(), menuItem{}) {
			a.copySessionLink(s.ID)
		}
	})
	if m.item("share-image", iconImage, locales.ShareImgMenu(), menuItem{disabled: a.view == nil}) {
		a.startShareImage(s.ID)
	}

	m.sep()
	if s.Archived {
		if m.item("restore", iconArchiveRestore, locales.HmRestore(), menuItem{}) {
			id := s.ID
			a.catalogAction(locales.FailRestore(), func(ctx context.Context, sync *catalog.Sync) error {
				return sync.Archive(ctx, id, false)
			})
		}
		if m.item("delete", iconTrash, locales.ArchiveDelete(), menuItem{destructive: true}) {
			sess := s
			a.deleting, a.deleteOpen = &sess, true
		}
		return
	}
	if m.item("archive", iconArchive, locales.HmArchive(), menuItem{}) {
		a.archiveSession(s.ID)
	}
}

// menuSwitch is the small switch at a menu row's end.
func menuSwitch(c *ui.Context, p *palette, on bool) {
	track := ui.Box(c.Key("switch")).Size(28, 16).Radius(8).Shrink(0).Transition(colorFade)
	knob := float32(2)
	if on {
		track.Background(p.Text)
		knob = 14
	} else {
		track.Background(p.Text.Alpha(0.15))
	}
	track.Children(func() {
		k := ui.Box(c.Key("knob")).Size(12, 12).Radius(6).Absolute().Top(2).Background(raisedGround(p)).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.2))
		k.Left(k.AnimateWith("x", knob, durRegular, easeStandard))
	})
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
