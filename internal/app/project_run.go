package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/collab"
	"github.com/lbr77/lody-mygo/internal/github"
	"github.com/lbr77/lody-mygo/locales"
)

// A conversation in a project of several, as Lody's chat shows one
// (session-chat-interface.tsx, session-project-members-chip.tsx and
// lib/session-project-members.ts): the conversation that owns the
// snapshot, its tab group's root, lists its repositories in the info
// bar's Repositories panel, and the one chosen there is what the bar's
// project, branch and changes, the PR tab and the header's pull request
// describe; until one is chosen, the first member's repository, as the
// session names it, or else the first row. The choice is view state,
// kept by that owner so a child tab and its root agree.

// memberRow is a repository of the conversation as the panel lists it,
// Lody's SessionProjectMemberRow.
type memberRow struct {
	id   string
	path string // under the conversation's directory, as api/proto
	// localRoot is a folder used in place's own directory, when it is on
	// the conversation's machine.
	localRoot string
	kind      string // github or local
	submodule bool
	depth     int    // how far in under its parents
	access    string // worktree, or additional or link for a folder used in place
	repo      string // its GitHub repository, when known
	branch    string
	hasDiff   bool
	add, del  int
	pr        *memberPR
}

// memberPR is a repository's current pull request: the last recorded.
type memberPR struct {
	url    string
	number int
	status string
	ci     string
}

// inPlace is a local folder used where it is: no branch, no backup, no PR.
func (r memberRow) inPlace() bool { return r.access == "additional" || r.access == "link" }

// currentPRs is the last pull request each repository recorded, by the
// repository lowercased, as Lody's getCurrentPullRequestsByRepository.
func currentPRs(prs []catalog.PullRequest) map[string]catalog.PullRequest {
	out := map[string]catalog.PullRequest{}
	for _, pr := range prs {
		if repo, _, ok := github.ParsePR(pr.URL); ok {
			out[strings.ToLower(repo)] = pr
		}
	}
	return out
}

// memberRepo is the repository a member's pull requests are in: a GitHub
// member's own, or the remote the machine saw for a folder.
func memberRepo(m catalog.RunMember) string {
	if m.Kind == "github" {
		return m.RepoFullName
	}
	return m.Observed
}

// buildMemberRows lists a conversation's members in placement order, as
// Lody's buildSessionProjectMemberRows: each top-level member followed by
// its submodules, siblings in the snapshot's order, each with its
// repository's current pull request. A submodule whose parent is missing
// is listed at the top rather than dropped. rootOf is a folder's
// directory on the conversation's machine.
func buildMemberRows(owner *catalog.Session, rootOf func(local string) string) []memberRow {
	run := owner.ProjectRun
	if run == nil {
		return nil
	}
	current := currentPRs(owner.PullRequests)
	byID := map[string]bool{}
	children := map[string][]catalog.RunMember{}
	for _, m := range run.Members {
		byID[m.ID] = true
		if m.Submodule() {
			children[m.Parent] = append(children[m.Parent], m)
		}
	}
	var rows []memberRow
	visited := map[string]bool{}
	var visit func(m catalog.RunMember, depth int)
	visit = func(m catalog.RunMember, depth int) {
		if visited[m.ID] {
			return
		}
		visited[m.ID] = true
		r := memberRow{id: m.ID, path: m.Path, kind: m.Kind, submodule: m.Submodule(), depth: depth, access: m.Access, repo: memberRepo(m),
			branch: m.Branch, hasDiff: m.HasDiff, add: int(m.DiffAdd), del: int(m.DiffDel)}
		if r.inPlace() && m.Kind == "local" && m.MachineID == owner.MachineID && rootOf != nil {
			r.localRoot = rootOf(m.LocalProjectID)
		}
		if pr, ok := current[strings.ToLower(r.repo)]; ok && r.repo != "" {
			_, n, _ := github.ParsePR(pr.URL)
			r.pr = &memberPR{url: pr.URL, number: n, status: pr.Status, ci: owner.PRState[pr.URL].CI}
		}
		rows = append(rows, r)
		for _, child := range children[m.ID] {
			visit(child, depth+1)
		}
	}
	for _, m := range run.Members {
		if !m.Submodule() || !byID[m.Parent] {
			visit(m, 0)
		}
	}
	// A cycle the contract refuses still lists every member.
	for _, m := range run.Members {
		visit(m, 0)
	}
	return rows
}

// resolveMemberRow is the row a conversation describes, as Lody's
// resolveSessionProjectMemberRow: the one chosen, else the one of the
// session's own repository, else the first.
func resolveMemberRow(rows []memberRow, selected, fallbackRepo string) *memberRow {
	for i := range rows {
		if rows[i].id == selected && selected != "" {
			return &rows[i]
		}
	}
	if fallbackRepo != "" {
		for i := range rows {
			if strings.EqualFold(rows[i].repo, fallbackRepo) {
				return &rows[i]
			}
		}
	}
	if len(rows) > 0 {
		return &rows[0]
	}
	return nil
}

// legacyRepo is the repository a session names, as Lody's
// resolveProjectGitHubRepo(project) ?? repoFullName: a GitHub project's,
// a folder's remote, or the session's own field.
func legacyRepo(s *catalog.Session) string {
	if s.Project["kind"] == "github" {
		if r, _ := s.Project["repoFullName"].(string); strings.TrimSpace(r) != "" {
			return strings.TrimSpace(r)
		}
	}
	if s.Project["kind"] == "local" {
		if r, _ := s.Project["githubRepoFullName"].(string); strings.TrimSpace(r) != "" {
			return strings.TrimSpace(r)
		}
	}
	return strings.TrimSpace(s.RepoFullName)
}

// runOwner is the conversation whose snapshot s reads: its tab group's
// root, when that one started in a project of several.
func (a *App) runOwner(s *catalog.Session) *catalog.Session {
	owner := a.tabRoot(s)
	if owner == nil || owner.ProjectRun == nil {
		return nil
	}
	return owner
}

// projectMembers is the conversation's members and the one it describes,
// for a conversation in a project of several; nil rows otherwise.
func (a *App) projectMembers(s *catalog.Session) (owner *catalog.Session, rows []memberRow, picked *memberRow) {
	if owner = a.runOwner(s); owner == nil {
		return nil, nil, nil
	}
	machine := s.MachineID
	rows = buildMemberRows(owner, func(local string) string {
		if pr := a.catalog.Project(machine + ":local:" + local); pr != nil {
			return pr.RootPath
		}
		return ""
	})
	return owner, rows, resolveMemberRow(rows, a.memberPicks[owner.ID], legacyRepo(owner))
}

// pickMember makes a member the one the conversation describes.
func (a *App) pickMember(owner, id string) {
	if a.memberPicks == nil {
		a.memberPicks = map[string]string{}
	}
	a.memberPicks[owner] = id
}

// githubState is what a conversation's GitHub actions go by, as Lody's
// getSessionGitHubState: the session whose pull requests they are, the
// repository, its latest pull request, and the work not yet published.
type githubState struct {
	source     *catalog.Session
	repo       string
	pr         *catalog.PullRequest
	dirty      bool
	unpushed   bool
	hasChanges bool // uncommitted, or committed changes to base a PR on
}

// latestPRFor is a session's latest pull request of one repository: a
// conversation of several records several repositories' in one list.
func latestPRFor(s *catalog.Session, repo string) *catalog.PullRequest {
	var matching []catalog.PullRequest
	for _, pr := range s.PullRequests {
		if r, _, ok := github.ParsePR(pr.URL); ok && strings.EqualFold(r, repo) {
			matching = append(matching, pr)
		}
	}
	return (&catalog.Session{PullRequests: matching}).LatestPR()
}

// sessionGitHub is s's GitHub state. In a project of several it is the
// owner's, for the member chosen: that one's repository and pull request,
// none for a member without a GitHub remote, so it never borrows the
// first member's; otherwise s's own.
func (a *App) sessionGitHub(s *catalog.Session) githubState {
	owner, _, picked := a.projectMembers(s)
	if owner == nil {
		st := githubState{source: s, repo: legacyRepo(s), pr: s.LatestPR(), dirty: s.WorkspaceDirty, unpushed: s.WorkspaceUnpushed}
		st.hasChanges = st.dirty || s.DiffAdd+s.DiffDel > 0
		return st
	}
	st := githubState{source: owner, dirty: owner.WorkspaceDirty, unpushed: owner.WorkspaceUnpushed}
	if picked != nil {
		st.repo = strings.TrimSpace(picked.repo)
	}
	if st.repo != "" {
		st.pr = latestPRFor(owner, st.repo)
	}
	// The totals come only when every member reports its own; a member's
	// committed changes still count.
	st.hasChanges = st.dirty || owner.DiffAdd+owner.DiffDel > 0
	for _, m := range owner.ProjectRun.Members {
		st.hasChanges = st.hasChanges || m.HasDiff && m.DiffAdd+m.DiffDel > 0
	}
	return st
}

// sessionPR is the pull request s describes, and the session it is on.
func (a *App) sessionPR(s *catalog.Session) (*catalog.Session, *catalog.PullRequest) {
	st := a.sessionGitHub(s)
	return st.source, st.pr
}

// fileMounts are the conversation's members that are file roots of their
// own, as Lody's buildSessionFileRootMounts: the folders used in place.
func (a *App) fileMounts(s *catalog.Session) []collab.Mount {
	owner := a.runOwner(s)
	if owner == nil {
		return nil
	}
	var mounts []collab.Mount
	for _, m := range owner.ProjectRun.Members {
		root := collab.MemberRoot(m.ID)
		if m.Access != "additional" && m.Access != "link" || root == "" {
			continue
		}
		mount := collab.Mount{Root: root, Path: m.Path}
		if m.Kind == "local" && m.MachineID == s.MachineID {
			mount.LocalProject = m.LocalProjectID
		}
		mounts = append(mounts, mount)
	}
	return mounts
}

// displayPath is where a synced file key of the open conversation shows:
// a member's file at its directory, as Lody's diff panel shows it.
func (a *App) displayPath(key string) string {
	s := a.catalog.Session(a.selected)
	if s == nil || !strings.HasPrefix(key, "\x00") {
		return key
	}
	return collab.Session{Mounts: a.fileMounts(s)}.Display(key)
}

// lodySeparator is @lody/ui's colors.separator, the rule under a panel's
// header.
func lodySeparator(p *palette) ui.Color {
	if p.Dark {
		return ui.Hex("#282828")
	}
	return ui.Hex("#EAEAEA")
}

// membersChip is the info bar's Repositories, as Lody's
// SessionProjectMembersChip: Folders and how many there are, the count
// only on a bar 420 wide or more, a plain action that never takes the
// bar's stage. It opens the panel above the bar, as wide as the bar: the
// project's name and how many repositories and pull requests it has,
// then each repository a row, indented as its submodules nest, saying
// where it sits and its branch, or for a folder used in place, its
// directory, and its current pull request. Choosing a row makes the bar
// and the PR tab describe that repository and closes the panel; opening
// its pull request chooses it too.
func (a *App) membersChip(c *ui.Context, p *palette, bar ui.Element, owner *catalog.Session, rows []memberRow, picked *memberRow) {
	open := a.popover("members/" + owner.ID)
	label := locales.RunMembers()
	b := ui.ButtonBase(c.Key("members")).Height(24).Padding(0, 4).Radius(6).Gap(4).AlignItems(ui.Center).Shrink(0).
		Cursor(ui.CursorPointer).Label(label).Transition(twFade).OnClick(func() { *open = !*open })
	tip(c, p, b, label)
	ink := p.Secondary
	switch {
	case *open:
		b.Background(p.Secondary.Alpha(0.1))
		ink = p.Text
	case b.Hovered():
		b.Background(p.Secondary.Alpha(0.1))
	}
	wide := bar.Bounds().W >= 420
	b.Children(func() {
		ui.Icon(c, iconFolders).Size(14, 14).TextColor(ink).Shrink(0)
		if wide {
			ui.Text(c, fmt.Sprint(len(rows))).FontSize(sizeFoot).FontFeatures("tnum").TextColor(ink).Shrink(0)
		}
	})
	selected := ""
	if picked != nil {
		selected = picked.id
	}
	ownerID, name := owner.ID, owner.ProjectRun.Name
	ui.PopoverBase(c, bar, open, func(panel ui.Element) {
		// Above the bar, sharing its edges, 4 apart; it comes from 4
		// further up and settles as it fades in.
		panel.Width(bar.Bounds().W).Radius(14).Background(raisedGround(p)).Border(1, p.Border.Alpha(0.6)).Clip().
			Shadow(0, 20, 25, -5, ui.RGBA(0, 0, 0, 0.1)).Label(label).
			AttachTo(bar, ui.AnchorTopStart, ui.AnchorBottomStart).Margin(0, 0, 4, 0).
			Transition(ui.ElementTransition{Colors: true, Duration: popTime, Ease: easeStandard, Exit: &ui.Motion{Y: -4}})
		if k := panel.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: popTime, Value: 1, Ease: easeStandard}}); k < 1 {
			panel.Translate(0, -4*(1-k)).Opacity(k)
		}
		prs := 0
		for _, r := range rows {
			if r.pr != nil {
				prs++
			}
		}
		panel.Children(func() {
			ui.Row(c.Key("members-head")).FillWidth().Padding(8).Gap(8).AlignItems(ui.Baseline).BorderWidth(0, 0, 1, 0).BorderColor(lodySeparator(p)).Children(func() {
				ui.Text(c, name).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text).SingleLine().MinWidth(0)
				ui.Text(c, locales.RunMembersSummary(len(rows), prs)).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine().Shrink(0)
			})
			ui.Scroll(c.Key("members-list")).FillWidth().MaxHeight(360).MinWidth(340).Padding(4).Role(ui.RoleList).Children(func() {
				for _, r := range rows {
					a.memberRowView(c, p, r, r.id == selected, func() {
						a.pickMember(ownerID, r.id)
						*open = false
					})
				}
			})
		})
	})
}

// memberRowView is a repository in the panel, as Lody's MemberRow: its
// glyph (a submodule's branch, GitHub's mark, or a folder), its GitHub
// name or directory over where it sits and its branch, and its pull
// request, its glyph in its CI's color, with its number.
func (a *App) memberRowView(c *ui.Context, p *palette, r memberRow, selected bool, choose func()) {
	glyph := iconFolder
	switch {
	case r.submodule:
		glyph = iconBranch
	case r.kind == "github":
		glyph = iconGithub
	}
	identity := firstNonEmpty(r.repo, r.path)
	inPlaceHint := ""
	location := r.branch
	if r.inPlace() {
		inPlaceHint = locales.RunInPlace()
		location = firstNonEmpty(r.localRoot, inPlaceHint)
	}
	locationTitle := location
	if r.inPlace() && r.localRoot != "" {
		locationTitle += " · " + inPlaceHint
	}
	showPath := r.repo != "" && r.repo != r.path
	parts := []string{identity}
	for _, s := range []string{r.path, locationTitle} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	row := ui.Row(c.Key(r.id)).FillWidth().MinHeight(40).Padding(4, 6, 4, 4+float32(r.depth)*14).Gap(8).Radius(6).AlignItems(ui.Center).Role(ui.RoleListItem)
	switch {
	case selected:
		row.Background(selectedFill(p))
	case row.Hovered():
		row.Background(hoverFill(p))
	}
	row.Children(func() {
		ui.Icon(c, glyph).Size(14, 14).TextColor(p.Tertiary).Shrink(0)
		sel := ui.ButtonBase(c.Key("select")).Grow(1).MinWidth(0).Justify(ui.Start).Cursor(ui.CursorPointer).Label(strings.Join(parts, " · ")).OnClick(choose)
		sel.Children(func() {
			ui.Column(c).FillWidth().MinWidth(0).Gap(1).Children(func() {
				id := ui.Text(c, identity).Font(fontMono).FontSize(sizeFoot).TextColor(p.Text).SingleLine()
				if selected {
					id.FontWeight(500)
				}
				if !showPath && location == "" {
					return
				}
				ui.Row(c.Key("meta")).FillWidth().MinWidth(0).Gap(4).AlignItems(ui.Center).Children(func() {
					if showPath {
						path := ui.Text(c.Key("path"), r.path).Font(fontMono).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine().MinWidth(0).MaxWidthPercent(60)
						tip(c, p, path, r.path)
						if location != "" {
							ui.Text(c, "·").FontSize(sizeCaption).TextColor(p.Tertiary).Shrink(0)
						}
					}
					if location != "" {
						at := ui.Text(c.Key("location"), location).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine().MinWidth(0).Shrink(1)
						tip(c, p, at, locationTitle)
					}
				})
			})
		})
		if r.pr == nil {
			return
		}
		icon, hue := prTone(p, r.pr.status)
		switch r.pr.ci {
		case "s":
			hue = p.Success
		case "f", "e":
			hue = p.Danger
		case "p":
			hue = p.Warning
		}
		label := locales.RunOpenPr(r.pr.number)
		if ci := runCiLabel(r.pr.ci); ci != "" {
			label += " · " + ci
		}
		url := r.pr.url
		pb := ui.ButtonBase(c.Key("pr")).Gap(3).AlignItems(ui.Center).Shrink(0).Cursor(ui.CursorPointer).Label(label).OnClick(func() {
			// Opening it from here also makes its repository the one
			// described.
			choose()
			openExternal(url)
		})
		hovered := pb.Hovered()
		pb.Children(func() {
			ui.Icon(c, icon).Size(12, 12).TextColor(hue).Shrink(0)
			n := ui.Text(c, fmt.Sprintf("#%d", r.pr.number)).FontSize(sizeFoot).FontFeatures("tnum").TextColor(p.Text).SingleLine()
			if hovered {
				n.Underline()
			}
		})
	})
}

// runCiLabel is a pull request's CI in words, as the panel's PR button
// says it: none while CI hasn't reported.
func runCiLabel(ci string) string {
	switch ci {
	case "s":
		return locales.InfoCiPassing()
	case "p":
		return locales.InfoCiRunning()
	case "f", "e":
		return locales.InfoCiFailing()
	}
	return ""
}

// infoContext is what the info bar's context says: the project, its
// glyph, the branch and the changes. In a project of several it is the
// member chosen: its repository or directory, its branch and its lines.
type infoContext struct {
	name     string
	icon     *ui.SVG
	branch   string
	diff     bool
	add, del int
}

func (a *App) infoContext(s *catalog.Session) infoContext {
	owner, _, picked := a.projectMembers(s)
	if owner == nil {
		ctx := infoContext{branch: s.BranchName, diff: s.DiffAdd > 0 || s.DiffDel > 0, add: int(s.DiffAdd), del: int(s.DiffDel)}
		if project := a.catalog.Project(s.ProjectID); project != nil {
			ctx.name, ctx.icon = project.Name, iconFolder
			if strings.HasPrefix(project.ID, "github:") {
				ctx.icon = iconBranch
			}
		}
		return ctx
	}
	if picked == nil {
		return infoContext{}
	}
	ctx := infoContext{name: firstNonEmpty(picked.repo, picked.path), branch: picked.branch, diff: picked.hasDiff && picked.add+picked.del > 0, add: picked.add, del: picked.del}
	switch {
	case picked.inPlace():
		ctx.icon = iconFolder
	case picked.repo != "":
		ctx.icon = iconGithub
	default:
		ctx.icon = iconWorktree
	}
	return ctx
}

// The prompts the info bar's GitHub actions send, as Lody's
// (packages/shared/src/pr-prompts.ts): Create PR and Create Draft PR
// lead with the line saying which button sent them and carry the
// standing instruction to keep the PR current; in a project of several,
// each, and Commit & Push, carries the project's scope, which Lody keeps
// in English in every language.
const projectGroupPrScope = "Project group scope:\n" +
	"- Apply the requested action to each member repository with changes or unpublished commits. Use the member inventory in this conversation and run Git in each member directory; the Project group directory itself is not a Git repository.\n" +
	"- Process submodules before their parents, recursively: commit and push each changed submodule first, then update and commit its gitlink in the parent repository and push the parent branch.\n" +
	"- If this action requests PRs, create or update one PR per changed repository. Open submodule PRs before parent PRs, preserve the requested draft or regular status, and link all related PRs in each PR body once their URLs are available.\n" +
	"- If this action only requests Commit & Push, do not open PRs. If PR upkeep was requested, apply it to each repository with a PR.\n" +
	"- If publishing a child fails, report the failure and do not publish a parent gitlink pointing to that unpublished commit. Report results and any PR URLs per repository."

// withProjectGroupPrScope applies a PR or Commit & Push action to the
// whole project of several, as Lody's.
func withProjectGroupPrScope(prompt string) string { return prompt + "\n\n" + projectGroupPrScope }

// infoAction is one of the info bar's GitHub actions.
type infoAction struct {
	id, label string
	disabled  bool
}

// infoActionIDs are the actions the bar offers, as Lody's
// resolveSessionInfoBarGitHubActionIds, for those the agent carries out:
// none without a repository or while the agent works. With no pull
// request yet, Create PR and, behind it, Create Draft PR once there are
// changes, and Commit & Push for uncommitted ones; with an open one,
// Commit & Push while work is unpublished.
func infoActionIDs(st githubState, busy bool) []string {
	if st.repo == "" || busy {
		return nil
	}
	if st.pr != nil {
		if st.pr.Status == "merged" || st.pr.Status == "closed" || !st.dirty && !st.unpushed {
			return nil
		}
		return []string{"commit-and-push"}
	}
	var ids []string
	if st.hasChanges {
		ids = append(ids, "create-pr", "create-draft-pr")
	}
	if st.dirty {
		ids = append(ids, "commit-and-push")
	}
	return ids
}

// infoPrompt is the message an action sends, scoped to the project of
// several when the conversation is in one.
func (a *App) infoPrompt(s *catalog.Session, id string) string {
	scope := func(prompt string) string {
		if s.ProjectRun != nil {
			return withProjectGroupPrScope(prompt)
		}
		return prompt
	}
	switch id {
	case "create-pr":
		return locales.PromptCreatePrOrigin() + "\n\n" + scope(locales.PromptCreatePr()+"\n\n"+locales.PromptPrUpkeep())
	case "create-draft-pr":
		return locales.PromptCreateDraftPrOrigin() + "\n\n" + scope(locales.PromptCreateDraftPr()+"\n\n"+locales.PromptPrUpkeep())
	}
	return scope(locales.PromptCommitAndPush())
}

// infoActions are the bar's actions for s, labeled; disabled until the
// conversation has loaded.
func (a *App) infoActions(s *catalog.Session) []infoAction {
	v := a.view
	if a.selected != s.ID {
		v = nil
	}
	busy := a.sending || s.Working() || v != nil && v.Working
	ready := v != nil && v.Status == "live"
	var out []infoAction
	for _, id := range infoActionIDs(a.sessionGitHub(s), busy) {
		label := locales.InfoCommitAndPush()
		switch id {
		case "create-pr":
			label = locales.InfoCreatePr()
		case "create-draft-pr":
			label = locales.InfoCreateDraftPr()
		}
		out = append(out, infoAction{id: id, label: label, disabled: !ready})
	}
	return out
}

// dispatchPrompt sends text to the open conversation as a turn of its
// own, leaving what the composer holds as it was.
func (a *App) dispatchPrompt(text string) {
	draft, files := a.draft, a.attachments
	a.draft, a.attachments = text, nil
	a.send()
	a.draft, a.attachments = draft, files
}

// infoActionsView ends the bar's context with its actions, as Lody's
// ContextChipActions: the first a text button on a tinted pill, the rest
// behind a chevron on its end that opens a menu above it, aligned to its
// end. The pill yields first when the bar is squeezed.
func (a *App) infoActionsView(c *ui.Context, p *palette, s *catalog.Session, actions []infoAction) {
	if len(actions) == 0 {
		return
	}
	tint := p.Text.Alpha(0.05)
	if p.Dark {
		tint = p.Secondary.Alpha(0.08)
	}
	hoverTint := tint
	sessionID := s.ID
	run := func(id string) {
		if sess := a.catalog.Session(sessionID); sess != nil && a.selected == sessionID {
			a.dispatchPrompt(a.infoPrompt(sess, id))
		}
	}
	first := actions[0]
	ui.Row(c.Key("info-actions")).Height(20).MinWidth(48).Shrink(8).Radius(6).Clip().Background(tint).AlignItems(ui.Stretch).Children(func() {
		b := ui.ButtonBase(c.Key("primary")).Padding(0, 6).MinWidth(0).Shrink(1).Cursor(ui.CursorPointer).Label(first.label).Disabled(first.disabled).Transition(twFade)
		tip(c, p, b, first.label)
		ink := p.Secondary
		if p.Dark {
			ink = p.Secondary.Alpha(0.8)
		}
		if b.Hovered() && !first.disabled {
			b.Background(hoverTint)
			ink = p.Text
		}
		if first.disabled {
			b.Opacity(0.5)
		}
		id := first.id
		b.OnClick(func() { run(id) })
		b.Children(func() {
			ui.Text(c, first.label).FontSize(sizeFoot).FontWeight(500).TextColor(ink).SingleLine().MinWidth(0)
		})
		if len(actions) < 2 {
			return
		}
		st := &a.rowMenu("info-actions/" + sessionID).menuState
		more := ui.ButtonBase(c.Key("more")).Width(20).Shrink(0).Cursor(ui.CursorPointer).Label(locales.MoreActions()).Transition(twFade).
			OnClick(func() { st.open, st.sub = !st.open, "" })
		tip(c, p, more, locales.MoreActions())
		chevron := p.Secondary.Alpha(0.75)
		if more.Hovered() || st.open {
			more.Background(hoverTint)
			chevron = p.Text
		}
		more.Children(func() {
			ui.Box(c).Absolute().Left(0).Top(4).Size(1, 12).Background(p.Text.Alpha(0.1)).PassThrough()
			ui.Icon(c, iconChevronDown).Size(14, 14).TextColor(chevron)
		})
		ui.PopoverBase(c, more, &st.open, func(panel ui.Element) {
			panel.Padding(4).MinWidth(200).MaxWidth(290).Radius(14).Background(raisedGround(p)).
				Border(0.5, p.CardRim).Shadow(0, 12, 32, -6, ui.RGBA(28, 28, 28, 0.16)).
				AttachTo(more, ui.AnchorTopEnd, ui.AnchorBottomEnd).Margin(0, 0, 6, 0).
				Transition(ui.ElementTransition{Colors: true, Duration: popTime, Ease: easeStandard, Exit: &ui.Motion{Y: -4}})
			if k := panel.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: popTime, Value: 1, Ease: easeStandard}}); k < 1 {
				panel.Translate(0, -4*(1-k)).Opacity(k)
			}
			m := &lodyMenu{c: c, p: p, open: &st.open, sub: &st.sub, rowH: 28}
			panel.Children(func() {
				for _, act := range actions[1:] {
					if m.item(act.id, nil, act.label, menuItem{disabled: act.disabled, bare: true}) {
						run(act.id)
					}
				}
			})
		})
	})
}
