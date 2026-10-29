package app

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// New chat in a project of several repositories or folders, as Lody's
// chat landing: its project picker offers them beside single projects,
// each with what it holds and what this machine lacks, Edit on hover and
// Create project at its end; a project is "group:<id>" there. A
// conversation always gets the whole project, on the one machine its
// folders pin it to, or on any. Its first member is the session's
// project, so older clients still show a repository and a branch; with
// two or more, the session carries Lody's projectRun snapshot, which the
// machine prepares and from then on owns.

// newChatGroup is the project of several New chat starts in, if any.
func (a *App) newChatGroup() (string, bool) {
	return strings.CutPrefix(a.newChat.project, "group:")
}

// groupAvailability is what a project of several can do on one machine,
// as Lody's getProjectGroupMachineAvailability: elsewhere, when its
// folders are on another machine, where it isn't offered; conflict, when
// they are on several (saved before the editor forbade it), offered but
// never started; or else the repositories that use a machine's copy that
// this one has none of, which a managed workspace clones on start.
type groupAvailability struct {
	elsewhere string
	conflict  bool
	missing   int
}

func (a *App) groupAvailability(g catalog.ProjectGroup, machine string) groupAvailability {
	var bound string
	for _, m := range g.Members {
		if m.Kind != "local" {
			continue
		}
		if bound != "" && bound != m.MachineID {
			return groupAvailability{conflict: true}
		}
		bound = m.MachineID
	}
	if bound != "" && machine != "" && bound != machine {
		return groupAvailability{elsewhere: bound}
	}
	var out groupAvailability
	if machine == "" {
		return out
	}
	// Which machines hold a copy of each repository, as they publish it.
	copies := map[string]bool{}
	for _, pr := range a.catalog.Projects {
		if pr.MachineID == machine && pr.RepoFullName != "" && strings.Contains(pr.ID, ":local:") {
			copies[strings.ToLower(pr.RepoFullName)] = true
		}
	}
	for _, m := range g.Members {
		if m.Kind == "github" && m.PreferLocal && !copies[strings.ToLower(m.RepoFullName)] {
			out.missing++
		}
	}
	return out
}

// canCloneRepositories is whether a repository a machine has no copy of
// can be cloned for a conversation: not in local-only mode, which has no
// GitHub integration.
func (a *App) canCloneRepositories() bool { return !a.localOnly }

// groupStatus is why New chat cannot start in its project of several on
// its machine, as Lody's composer warns; "" when it can.
func (a *App) groupStatus() string {
	id, ok := a.newChatGroup()
	if !ok {
		return ""
	}
	g := a.group(id)
	if g == nil {
		return locales.GroupMissing()
	}
	machine := a.newChat.machine
	av := a.groupAvailability(*g, machine)
	switch {
	case av.conflict:
		return locales.GroupMachinesConflictStatus()
	case av.missing > 0 && !a.canCloneRepositories():
		return locales.GroupMissingLocalStatus(av.missing)
	case len(g.Members) > 1 && machine != "" && a.machineCap(machine, "multiRepoProject") < 1:
		return locales.GroupUnsupportedMachine()
	case groupUsesCopies(*g) && machine != "" && a.machineCap(machine, "projectRepositoryMembers") < 1:
		return locales.GroupUnsupportedRepositoryMembers()
	}
	return ""
}

// groupUsesCopies is whether a project has repositories that use a
// machine's copy, which an older machine would clone instead.
func groupUsesCopies(g catalog.ProjectGroup) bool {
	for _, m := range g.Members {
		if m.Kind == "github" && m.PreferLocal {
			return true
		}
	}
	return false
}

// groupBlocked is whether New chat's project of several cannot start: it
// is gone, its folders are on several machines, or this machine lacks a
// repository it cannot clone.
func (a *App) groupBlocked() bool {
	id, ok := a.newChatGroup()
	if !ok {
		return false
	}
	g := a.group(id)
	if g == nil {
		return true
	}
	av := a.groupAvailability(*g, a.newChat.machine)
	return av.conflict || av.missing > 0 && !a.canCloneRepositories()
}

// homeGroupStatus is the warning over the composer while New chat's
// project of several cannot start as it is.
func (a *App) homeGroupStatus(c *ui.Context, p *palette) {
	if status := a.groupStatus(); status != "" {
		ui.Text(c.Key("group-status"), status).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Warning).Padding(0, 6).Role(ui.RoleStatus)
	}
}

// chooseNewChatGroup sets New chat on a project of several, on the
// machine its folders pin it to when that one is connected.
func (a *App) chooseNewChatGroup(id string) {
	a.newChat.project, a.newChat.branch = "group:"+id, ""
	if g := a.group(id); g != nil {
		if bound := g.BoundMachine(); bound != "" && bound != a.newChat.machine {
			for _, m := range a.catalog.MachineIDs {
				if m == bound {
					a.newChat.machine = bound
					a.pickNewChatDefaults()
				}
			}
		}
	}
}

// newChatInGroup opens New chat in a project of several, as its sidebar
// row's button does.
func (a *App) newChatInGroup(id string) {
	a.chooseNewChatGroup(id)
	a.pickNewChatDefaults()
	a.open("")
}

// homeProjectLabel is the project pill's label: a project of several's
// name, or "Removed project" for one deleted since.
func (a *App) homeProjectLabel() (string, bool) {
	id, ok := a.newChatGroup()
	if !ok {
		return "", false
	}
	if g := a.group(id); g != nil {
		return g.Name, true
	}
	return locales.PickerRemovedGroup(), true
}

// homeProjectList is the project pill's list: No project, the projects
// of several this machine can take, its folders and the workspace's
// GitHub repositories, then Create project, which opens the wizard and
// chooses what it creates.
func (a *App) homeProjectList(c *ui.Context, p *palette, anchor ui.Element, open *bool) {
	selected := a.newChat.project
	popup(c, anchor, open, func() {
		ui.Scroll(c).MinWidth(240).MaxWidth(320).MaxHeight(320).Gap(1).Children(func() {
			for i, o := range a.projectOptions() {
				if i == 1 {
					a.homeGroupOptions(c, p, open)
				}
				row := menuRow(c.Key(o.id), p, o.label)
				if row.Clicked() {
					a.newChat.project, a.newChat.branch = o.id, ""
					*open = false
				}
				row.Children(func() {
					ui.Text(c, o.label).FontSize(sizeSub).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
					if selected != "" && o.id == selected {
						ui.Icon(c, iconCheck).Size(14, 14).TextColor(p.Text)
					}
				})
			}
		})
		ui.Box(c).Height(1).Margin(4, -4).Background(p.Border.Alpha(0.7)).Shrink(0)
		create := menuRow(c.Key("create-project"), p, locales.PickerCreateProject())
		if create.Clicked() {
			*open = false
			// The project just created is what the user meant to work in.
			a.openProjectWizardFrom("", "", a.chooseNewChatGroup)
		}
		create.Children(func() {
			ui.Icon(c, iconFolders).Size(14, 14).TextColor(p.Secondary).Shrink(0)
			ui.Text(c, locales.PickerCreateProject()).FontSize(sizeSub).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
		})
	})
}

// homeGroupOptions are the projects of several in the list: those whose
// folders are on another machine aren't offered here, as single folders
// aren't; each says what it holds, or why it cannot start here, and
// offers Edit with the pointer on it.
func (a *App) homeGroupOptions(c *ui.Context, p *palette, open *bool) {
	machine := a.newChat.machine
	for _, g := range a.catalog.Groups {
		av := a.groupAvailability(g, machine)
		if av.elsewhere != "" {
			continue
		}
		disabled := ""
		switch {
		case av.conflict:
			disabled = locales.GroupMachinesConflict()
		case av.missing > 0 && !a.canCloneRepositories():
			disabled = locales.GroupMissingLocal(av.missing)
		}
		description := disabled
		if description == "" {
			description = locales.PickerGroupMembers(len(g.Members))
			if av.missing > 0 {
				description += " · " + locales.GroupMissingClone(av.missing)
			}
		}
		id, name := g.ID, g.Name
		row := ui.Row(c.Key("group:"+g.ID)).FillWidth().MinHeight(30).Padding(6, 8).Radius(6).Gap(8).AlignItems(ui.Center).Label(g.Name)
		hovered := row.Hovered()
		reveal := row.AnimateWith("edit", b2f(hovered), durFast, easeStandard)
		if disabled != "" {
			row.Opacity(0.5)
		} else {
			row.Cursor(ui.CursorPointer).OnClick(func() {
				a.chooseNewChatGroup(id)
				*open = false
			})
			if hovered {
				row.Background(p.Hover)
			}
		}
		row.Children(func() {
			ui.Icon(c, iconFolders).Size(16, 16).TextColor(p.Text.Alpha(0.7)).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, name).FontSize(sizeSub).TextColor(p.Text).SingleLine()
				ui.Text(c, description).FontSize(sizeCaption).LineHeight(1.3).TextColor(p.Secondary).MaxLines(2)
			})
			// The row chooses on a press; the button only edits.
			edit := ui.ButtonBase(c.Key("edit")).Height(22).Padding(0, 6).Radius(6).Label(locales.PickerEditGroup(name)).
				Opacity(reveal).Transition(colorFade)
			if hovered {
				edit.Cursor(ui.CursorPointer).OnClick(func() {
					*open = false
					a.requestProjectEdit(id)
				})
				if edit.Hovered() {
					edit.Background(p.Text.Alpha(0.06))
				}
			} else {
				edit.PassThrough()
			}
			edit.Children(func() { ui.Text(c, locales.PropEdit()).FontSize(sizeCaption).FontWeight(500).TextColor(p.Secondary) })
			if a.newChat.project == "group:"+id {
				ui.Icon(c, iconCheck).Size(14, 14).TextColor(p.Text)
			}
		})
	}
}

// startHomeChat sends New chat's first message: in a project of several
// as Lody's chat landing starts one, or else as any.
func (a *App) startHomeChat() {
	if id, ok := a.newChatGroup(); ok {
		a.startGroupChat(id)
		return
	}
	a.startChat()
}

// startGroupChat creates a session in a project of several and sends its
// first message, after Lody's checks: the project still there and able
// to start on this machine, and the machine new enough for it.
func (a *App) startGroupChat(id string) {
	text := strings.TrimSpace(a.draft)
	nc := a.newChat
	agent := a.agent(nc.machine, nc.agent)
	files := a.attachments
	if (text == "" && len(files) == 0) || a.sending || agent == nil || a.sync == nil || a.conn == nil || a.account == nil {
		return
	}
	g := a.group(id)
	if g == nil || a.groupBlocked() {
		a.sendError = firstNonEmpty(a.groupStatus(), locales.GroupMissing())
		return
	}
	project, run, repo, base, err := groupLaunch(*g)
	switch {
	case err != nil:
		a.sendError = err.Error()
		return
	case run != nil && a.machineCap(nc.machine, "multiRepoProject") < 1:
		a.sendError = locales.GroupUnsupportedMachine()
		return
	case groupUsesCopies(*g) && a.machineCap(nc.machine, "projectRepositoryMembers") < 1:
		// An older machine would clone instead of using its copy.
		a.sendError = locales.GroupUnsupportedRepositoryMembers()
		return
	}
	a.recordActivity()
	target := a.mentionTarget()
	skills, sessions := a.mentionItems(target)
	model, mode := a.models[""], a.modes[""]
	title := draftTitle(text)
	if title == "" && len(files) > 0 {
		title = draftTitle(files[0].Name)
	}
	a.createAndSend(catalog.NewSession{
		ID: uuid.NewString(), MachineID: nc.machine, UserID: a.account.User.ID, Title: title, Agent: *agent, CreatedAt: time.Now(),
		Project: project, ProjectRun: run, RepoFullName: repo, BaseBranch: base,
	}, chatSend{key: "", text: text, files: files, model: model, mode: mode, runValues: a.chosenRunValues("", agent.ID, model), mcp: a.mcpSelection(""),
		target: target, skills: skills, sessions: sessions, store: a.shortcutStore()}, nil)
}

// projectSubmoduleBase is the base Lody gives a submodule member: the
// commit its parent pins.
const projectSubmoduleBase = "gitlink"

var runSlug = regexp.MustCompile(`[^a-z0-9-]+`)

// projectRunRoot is the directory a project's conversations work in, as
// Lody's getProjectRunRoot: its name made a slug, then its id.
func projectRunRoot(g catalog.ProjectGroup) string {
	slug := strings.Trim(runSlug.ReplaceAllString(strings.ToLower(g.Name), "-"), "-")
	if len(slug) > 32 {
		slug = slug[:32]
	}
	if slug == "" {
		slug = "project"
	}
	return "project-" + slug + "-" + g.ID
}

// groupLaunch is how a conversation starts in a project, with every
// member, as Lody's chat landing: the session's project is the first
// member (a GitHub repository at its base branch, or HEAD, or a folder's
// own worktree), its repository the first GitHub member's; with two
// members or more, the run snapshot, each member at its path with the
// base its worktree starts from.
func groupLaunch(g catalog.ProjectGroup) (project, run map[string]any, repo, base string, err error) {
	d := draftOf(g)
	if len(d.members) == 0 {
		return nil, nil, "", "", errors.New(locales.GroupMissing())
	}
	first := d.members[0].source
	if first.kind == "github" {
		base = firstNonEmpty(first.baseBranch, "HEAD")
		project, repo = map[string]any{"kind": "github", "repoFullName": first.repo, "branch": base}, first.repo
	} else {
		project = map[string]any{"kind": "local", "localProjectId": first.local, "useWorktree": true}
		if first.baseBranch != "" {
			project["branch"] = first.baseBranch
		}
		for _, m := range d.members {
			if m.source.kind == "github" {
				repo = m.source.repo
				break
			}
		}
	}
	if len(d.members) < 2 {
		return project, nil, repo, base, nil
	}
	paths, err := memberPaths(d.members)
	if err != nil {
		return nil, nil, "", "", err
	}
	members := map[string]any{}
	for o, m := range d.members {
		start := firstNonEmpty(m.source.baseBranch, "HEAD")
		if m.submodule() {
			start = projectSubmoduleBase
		}
		members[m.id] = map[string]any{"o": o, "src": m.source.row(), "placement": m.placementRow(), "path": paths[m.id], "base": start}
	}
	run = map[string]any{"id": g.ID, "name": g.Name, "root": projectRunRoot(g), "members": members}
	if g.Instructions != "" {
		run["instructions"] = g.Instructions
	}
	return project, run, repo, base, nil
}
