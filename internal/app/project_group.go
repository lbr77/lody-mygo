package app

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// A project of several repositories or folders, as Lody's project editor
// drafts one (lib/project-group-form.ts and lib/project-member-sources.ts):
// each member a GitHub repository or one machine's folder, in a directory
// of its own under the conversation's root, so that one conversation works
// across all of them. "Project" elsewhere still means one folder or one
// repository. The rules here are the shared contract's that an editor
// needs: which directory names are safe, which members are the same
// repository, and which machine the folders bind the project to.

// groupSource is where a member comes from, Lody's ProjectMemberSource: a
// GitHub repository, cloned for each conversation or, preferring a local
// copy, the running machine's own copy of it; or one machine's local
// project.
type groupSource struct {
	kind        string // github or local
	repo        string // owner/name, of a github source
	preferLocal bool
	machine     string // of a local source
	local       string // its local project's id
}

func githubSource(repo string) groupSource { return groupSource{kind: "github", repo: repo} }

func localSource(machine, local string) groupSource {
	return groupSource{kind: "local", machine: machine, local: local}
}

// key is the source's identity, as Lody's getProjectMemberSourceKey.
func (s groupSource) key() string {
	if s.kind == "github" {
		return "github:" + strings.ToLower(s.repo)
	}
	return "local:" + s.machine + ":" + s.local
}

// row is the source as the workspace stream stores it.
func (s groupSource) row() map[string]any {
	if s.kind == "github" {
		row := map[string]any{"kind": "github", "repoFullName": s.repo}
		if s.preferLocal {
			row["preferLocal"] = true
		}
		return row
	}
	return map[string]any{"kind": "local", "machineId": s.machine, "localProjectId": s.local}
}

// groupFolder is a folder picked from a machine's browser for a project.
// It is not a listed project until the project is saved, which registers
// it on its machine, hidden from the lists of single projects.
type groupFolder struct {
	machine, local string // the local project the machine would make it
	name, rootPath string
	isGit          bool
	repo           string // its GitHub remote, as the machine inspected it
}

// groupMember is a member of the draft. Every one is top-level, in a
// directory of its own: Lody nests a member as another's submodule only
// once its machine or GitHub reports the submodules, which this port does
// not ask.
type groupMember struct {
	id     string
	source groupSource
	dir    string
	folder *groupFolder
}

// usage is how a member is used: a folder in place, a machine's copy of
// a repository, or a fresh clone in a worktree.
func (m groupMember) usage() string {
	switch {
	case m.source.kind == "local":
		return "inPlace"
	case m.source.preferLocal:
		return "localCopy"
	}
	return "worktree"
}

// groupDraft is a project being put together: its name, the instructions
// the agent gets first, and its members.
type groupDraft struct {
	name, instructions string
	members            []groupMember
}

// groupScope is where a new project's conversations run, chosen before
// any member: on one machine, whose folders it may then hold, or
// anywhere, which holds only GitHub repositories.
type groupScope struct {
	kind    string // machine or github; none chosen yet
	machine string
}

// newMemberID is a member's id, as Lody's: twelve hex digits of a UUID.
func newMemberID() string { return strings.ReplaceAll(uuid.NewString(), "-", "")[:12] }

var (
	unsafePathChar   = regexp.MustCompile(`[\\<>:"|?*]`)
	reservedPathPart = regexp.MustCompile(`(?i)^(?:\.git|con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)`)
)

// safeProjectPath is Lody's isSafeProjectPath: a portable Git path, which
// no Windows alias or POSIX traversal escapes.
func safeProjectPath(v string) bool {
	if v == "" {
		return false
	}
	for _, part := range strings.Split(v, "/") {
		if part == "" || part == "." || part == ".." || unsafePathChar.MatchString(part) || reservedPathPart.MatchString(part) ||
			strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		for _, r := range part {
			if r < 32 {
				return false
			}
		}
	}
	return true
}

var (
	leadingDots     = regexp.MustCompile(`^\.+`)
	trailingDotsGap = regexp.MustCompile(`[. ]+$`)
)

// suggestMemberDir is a directory name for a member from its name: the
// last segment of a repository's, reduced to what every platform takes.
func suggestMemberDir(label string) string {
	last := ""
	if parts := strings.FieldsFunc(label, func(r rune) bool { return r == '/' }); len(parts) > 0 {
		last = parts[len(parts)-1]
	}
	cleaned := strings.Map(func(r rune) rune {
		if r < 32 {
			return '-'
		}
		return r
	}, last)
	cleaned = unsafePathChar.ReplaceAllString(cleaned, "-")
	cleaned = strings.TrimSpace(trailingDotsGap.ReplaceAllString(leadingDots.ReplaceAllString(cleaned, ""), ""))
	if cleaned != "" && safeProjectPath(cleaned) {
		return cleaned
	}
	return "repo"
}

// uniqueDir is base, or base-2, base-3 and on, whichever is free.
func uniqueDir(base string, taken map[string]bool) string {
	if !taken[strings.ToLower(base)] {
		return base
	}
	for i := 2; ; i++ {
		if candidate := fmt.Sprintf("%s-%d", base, i); !taken[strings.ToLower(candidate)] {
			return candidate
		}
	}
}

func (d groupDraft) find(key string) int {
	return slices.IndexFunc(d.members, func(m groupMember) bool { return m.source.key() == key })
}

// add appends a source as a member in a directory of its own, unless the
// draft has it already.
func (d groupDraft) add(source groupSource, label string) groupDraft {
	if d.find(source.key()) >= 0 {
		return d
	}
	taken := map[string]bool{}
	for _, m := range d.members {
		taken[strings.ToLower(m.dir)] = true
	}
	d.members = append(slices.Clone(d.members), groupMember{id: newMemberID(), source: source, dir: uniqueDir(suggestMemberDir(label), taken)})
	return d
}

// addFolder adds a folder picked from the browser: as its repository,
// preferring this copy, when it has a GitHub remote, else as its machine's
// folder. Only the draft holds its path.
func (d groupDraft) addFolder(f groupFolder) groupDraft {
	source := localSource(f.machine, f.local)
	if f.repo != "" {
		source = groupSource{kind: "github", repo: f.repo, preferLocal: true}
	}
	next := d.add(source, f.name)
	if len(next.members) == len(d.members) {
		i := d.find(source.key())
		if i < 0 || d.members[i].folder != nil {
			return d
		}
		d.members = slices.Clone(d.members)
		d.members[i].folder = &f
		return d
	}
	next.members[len(next.members)-1].folder = &f
	return next
}

// remove takes a member out.
func (d groupDraft) remove(id string) groupDraft {
	d.members = slices.DeleteFunc(slices.Clone(d.members), func(m groupMember) bool { return m.id == id })
	return d
}

// retain drops the members a scope cannot hold: folders of another
// machine, and every folder for a GitHub-only project, even one a GitHub
// remote made portable. GitHub only means a clone for each conversation,
// never a machine's copy.
func (d groupDraft) retain(scope groupScope) groupDraft {
	var members []groupMember
	for _, m := range d.members {
		machine := ""
		if m.folder != nil {
			machine = m.folder.machine
		} else if m.source.kind == "local" {
			machine = m.source.machine
		}
		if machine != "" && (scope.kind == "github" || machine != scope.machine) {
			continue
		}
		if scope.kind == "github" && m.source.kind == "github" {
			m.source.preferLocal = false
		}
		members = append(members, m)
	}
	d.members = members
	return d
}

// bind ties a project to the machine whose folders it was built from: a
// repository that would use a local copy becomes that machine's folder,
// picked folders included, so the project runs there only. Clones, and
// repositories with no copy there, stay as they are.
func (d groupDraft) bind(machine string, copyOf func(repo string) string) groupDraft {
	d.members = slices.Clone(d.members)
	for i, m := range d.members {
		if m.source.kind != "github" || !m.source.preferLocal {
			continue
		}
		local := ""
		switch {
		case m.folder != nil && m.folder.machine == machine:
			local = m.folder.local
		case m.folder == nil:
			local = copyOf(m.source.repo)
		}
		if local != "" {
			d.members[i].source = localSource(machine, local)
		}
	}
	return d
}

// normalizeGroupName is how two projects' names compare: one project per
// name, whatever its case and edges.
func normalizeGroupName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// suggestName names a new project after its first member, unless one was
// typed, numbering it past the names other projects have.
func (d groupDraft) suggestName(labelOf func(groupMember) string, taken map[string]bool) string {
	if strings.TrimSpace(d.name) != "" || len(d.members) == 0 {
		return d.name
	}
	base := d.name
	if parts := strings.FieldsFunc(labelOf(d.members[0]), func(r rune) bool { return r == '/' }); len(parts) > 0 {
		base = parts[len(parts)-1]
	}
	if !taken[normalizeGroupName(base)] {
		return base
	}
	for i := 2; ; i++ {
		if candidate := fmt.Sprintf("%s %d", base, i); !taken[normalizeGroupName(candidate)] {
			return candidate
		}
	}
}

// groupValidation is what keeps a draft from being saved: its name, and
// each member's one problem, as Lody's error codes.
type groupValidation struct {
	nameRequired, nameTaken, membersRequired bool
	memberErrors                             map[string]string // by member id
	valid                                    bool
}

// localMachine is the machine the draft's folders are on: the first's.
func (d groupDraft) localMachine() string {
	for _, m := range d.members {
		if m.source.kind == "local" {
			return m.source.machine
		}
	}
	return ""
}

// validate checks a draft as Lody's validateProjectGroupForm: a name of its
// own, a member at least, directories that are safe and apart, folders on
// one machine, and each repository once. repoOf is the GitHub repository
// a machine reported for a folder, if any.
func (d groupDraft) validate(repoOf func(groupSource) string, taken map[string]bool) groupValidation {
	v := groupValidation{memberErrors: map[string]string{}}
	switch {
	case strings.TrimSpace(d.name) == "":
		v.nameRequired = true
	case taken[normalizeGroupName(d.name)]:
		v.nameTaken = true
	}
	v.membersRequired = len(d.members) == 0
	dirs := map[string]string{}
	for _, m := range d.members {
		if !safeProjectPath(m.dir) || strings.Contains(m.dir, "/") {
			v.memberErrors[m.id] = "dir_invalid"
			continue
		}
		key := strings.ToLower(m.dir)
		if first, ok := dirs[key]; ok {
			v.memberErrors[m.id] = "dir_duplicate"
			if _, has := v.memberErrors[first]; !has {
				v.memberErrors[first] = "dir_duplicate"
			}
		} else {
			dirs[key] = m.id
		}
	}
	machine := d.localMachine()
	for _, m := range d.members {
		if _, has := v.memberErrors[m.id]; !has && m.source.kind == "local" && m.source.machine != machine {
			v.memberErrors[m.id] = "machine_mismatch"
		}
	}
	// One repository once, whether a GitHub member or a folder of it.
	seen := map[string]bool{}
	for _, m := range d.members {
		repo := m.source.repo
		if m.source.kind == "local" && repoOf != nil {
			repo = repoOf(m.source)
		}
		key := "github:" + strings.ToLower(repo)
		if repo == "" {
			key = m.source.key()
		}
		if _, has := v.memberErrors[m.id]; seen[key] && !has {
			v.memberErrors[m.id] = "duplicate_repository"
		}
		seen[key] = true
	}
	v.valid = !v.nameRequired && !v.nameTaken && !v.membersRequired && len(v.memberErrors) == 0
	return v
}

// row is the project as the workspace stream stores it, a first revision.
func (d groupDraft) row(id, owner string, now float64) map[string]any {
	members := []any{}
	for _, m := range d.members {
		members = append(members, map[string]any{"id": m.id, "source": m.source.row(), "placement": map[string]any{"kind": "root", "dir": m.dir}})
	}
	row := map[string]any{"v": 1, "id": id, "name": strings.TrimSpace(d.name), "ownerUserId": owner, "createdAtMs": now, "revision": 1, "members": members}
	if instructions := strings.TrimSpace(d.instructions); instructions != "" {
		row["instructions"] = instructions
	}
	return row
}

// memberCopy is a machine's copy of a repository: a local project there.
type memberCopy struct {
	machine, name string // the machine, and what it is called
	local         string // the local project
	rootPath      string
	online        bool
	opened        float64
}

// memberOption is something a project may hold, as Lody's
// ProjectMemberSourceOption: a repository, however many machines hold a
// copy of it, or a folder that is only on its machine, having no GitHub
// remote.
type memberOption struct {
	key       string
	kind      string // repository or directory
	source    groupSource
	label     string // owner/name, or the folder's name
	onGithub  bool   // the workspace's GitHub integration reaches it
	copies    []memberCopy
	localKeys []string // a repository's copies, as folder sources
}

// buildMemberOptions merges the machines' local projects with the
// workspace's GitHub repositories, one option a repository: a local project
// whose machine published a GitHub remote joins its repository's, one
// without stays a folder. A repository with a copy somewhere prefers it;
// one only on GitHub is cloned. Copies are one a machine, the most
// recently opened, as the daemon picks, by machine name.
func buildMemberOptions(projects []catalog.Project, repos []string, machineName func(string) string, online func(string) bool) []memberOption {
	type repoRow struct {
		label    string
		onGithub bool
		copies   []memberCopy
	}
	byRepo := map[string]*repoRow{}
	var order []string
	row := func(name string) *repoRow {
		key := strings.ToLower(name)
		r := byRepo[key]
		if r == nil {
			r = &repoRow{label: name}
			byRepo[key] = r
			order = append(order, key)
		}
		return r
	}
	var folders []memberOption
	for _, pr := range projects {
		_, local, _ := strings.Cut(pr.ID, ":local:")
		cp := memberCopy{machine: pr.MachineID, name: machineName(pr.MachineID), local: local, rootPath: pr.RootPath, online: online(pr.MachineID), opened: pr.OpenedAt}
		if pr.RepoFullName == "" {
			source := localSource(pr.MachineID, local)
			folders = append(folders, memberOption{key: source.key(), kind: "directory", source: source, label: pr.Name, copies: []memberCopy{cp}})
			continue
		}
		r := row(pr.RepoFullName)
		r.copies = append(r.copies, cp)
	}
	for _, name := range repos {
		r := row(name)
		r.label, r.onGithub = name, true // GitHub's spelling wins over a remote URL's
	}
	var withCopies, githubOnly []memberOption
	for _, key := range order {
		r := byRepo[key]
		chosen := map[string]memberCopy{}
		var localKeys []string
		for _, cp := range r.copies {
			localKeys = append(localKeys, localSource(cp.machine, cp.local).key())
			if best, ok := chosen[cp.machine]; !ok || cp.opened > best.opened || cp.opened == best.opened && cp.local < best.local {
				chosen[cp.machine] = cp
			}
		}
		var copies []memberCopy
		for _, cp := range chosen {
			copies = append(copies, cp)
		}
		sort.Slice(copies, func(i, j int) bool { return lessFold(copies[i].name, copies[j].name) })
		source := githubSource(r.label)
		source.preferLocal = len(copies) > 0
		o := memberOption{key: source.key(), kind: "repository", source: source, label: r.label, onGithub: r.onGithub, copies: copies, localKeys: localKeys}
		if len(copies) > 0 {
			withCopies = append(withCopies, o)
		} else {
			githubOnly = append(githubOnly, o)
		}
	}
	for _, list := range [][]memberOption{withCopies, githubOnly, folders} {
		sort.SliceStable(list, func(i, j int) bool { return lessFold(list[i].label, list[j].label) })
	}
	return append(append(withCopies, githubOnly...), folders...)
}

// lessFold orders names as a person reads them, case aside.
func lessFold(a, b string) bool {
	if la, lb := strings.ToLower(a), strings.ToLower(b); la != lb {
		return la < lb
	}
	return a < b
}

// memberOptions are what this workspace's projects may hold: the folders
// of its machines, but those being removed, and its GitHub repositories.
func (a *App) memberOptions() []memberOption {
	var projects []catalog.Project
	for _, pr := range a.catalog.Projects {
		if strings.Contains(pr.ID, ":local:") && !a.catalog.Removing[pr.ID] {
			projects = append(projects, pr)
		}
	}
	var repos []string
	for _, r := range a.githubRepos() {
		repos = append(repos, r.FullName)
	}
	return buildMemberOptions(projects, repos, a.machineName, a.machineReachable)
}

// machineReachable is whether a machine answers now: this one always.
func (a *App) machineReachable(id string) bool {
	return id == a.localMachine() || a.presence.IsOnline(id)
}

// optionsByKey looks an option up by its source, and a repository by the
// folder source of any copy of it, as a project saved before repository
// members names one.
func optionsByKey(options []memberOption) map[string]*memberOption {
	by := map[string]*memberOption{}
	for i := range options {
		by[options[i].key] = &options[i]
	}
	for i := range options {
		for _, k := range options[i].localKeys {
			if by[k] == nil {
				by[k] = &options[i]
			}
		}
	}
	return by
}

// takenGroupNames are the other projects' names, normalized.
func (a *App) takenGroupNames() map[string]bool {
	taken := map[string]bool{}
	for _, g := range a.catalog.Groups {
		taken[normalizeGroupName(g.Name)] = true
	}
	return taken
}

// groupMemberError says a member's problem, by Lody's code.
func groupMemberError(code string) string {
	switch code {
	case "dir_invalid":
		return locales.WizardErrDirInvalid()
	case "dir_duplicate":
		return locales.WizardErrDirDuplicate()
	case "duplicate_repository":
		return locales.WizardErrDuplicateRepository()
	case "machine_mismatch":
		return locales.WizardErrMachineMismatch()
	}
	return code
}
