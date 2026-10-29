package app

import (
	"encoding/json"
	"errors"
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
	// What a saved member carries that the editor leaves as it is: the
	// branch its worktrees start from, and the repository's GitHub id.
	baseBranch string
	repoID     float64
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
	row := map[string]any{"kind": "local", "machineId": s.machine, "localProjectId": s.local}
	if s.kind == "github" {
		row = map[string]any{"kind": "github", "repoFullName": s.repo}
		if s.repoID > 0 {
			row["repoId"] = s.repoID
		}
		if s.preferLocal {
			row["preferLocal"] = true
		}
	}
	if s.baseBranch != "" {
		row["baseBranch"] = s.baseBranch
	}
	return row
}

// sourceOf reads a source as the workspace stream stores it.
func sourceOf(row map[string]any) groupSource {
	text := func(k string) string { s, _ := row[k].(string); return s }
	s := groupSource{kind: text("kind"), repo: text("repoFullName"), preferLocal: row["preferLocal"] == true,
		machine: text("machineId"), local: text("localProjectId"), baseBranch: text("baseBranch")}
	s.repoID, _ = row["repoId"].(float64)
	return s
}

// groupFolder is a folder picked from a machine's browser for a project.
// It is not a listed project until the project is saved, which registers
// it on its machine, hidden from the lists of single projects.
type groupFolder struct {
	machine, local string // the local project the machine would make it
	name, rootPath string
	isGit          bool
	repo           string // its GitHub remote, as the machine inspected it
	submodules     []detectedSubmodule
}

// groupMember is a member of the draft: top-level in a directory of its
// own, or, once its machine or GitHub reports it, a submodule of another
// member at the path the parent's .gitmodules gives it.
type groupMember struct {
	id     string
	source groupSource
	dir    string
	// parent and path place it as parent's submodule; dir is then unused.
	parent, path string
	// origin is where its placement came from: default, detected, or the
	// user's. Detection moves only the first two.
	origin string
	folder *groupFolder
}

// submodule is whether the member sits inside another.
func (m groupMember) submodule() bool { return m.parent != "" }

// placementRow is where the member sits, as the stream stores it.
func (m groupMember) placementRow() map[string]any {
	if m.submodule() {
		return map[string]any{"kind": "submodule", "parent": m.parent, "path": m.path}
	}
	return map[string]any{"kind": "root", "dir": m.dir}
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
	d.members = append(slices.Clone(d.members), groupMember{id: newMemberID(), source: source, dir: uniqueDir(suggestMemberDir(label), d.rootDirs()), origin: "default"})
	return d
}

// rootDirs are the directories the top-level members take, lowercased.
func (d groupDraft) rootDirs() map[string]bool {
	taken := map[string]bool{}
	for _, m := range d.members {
		if !m.submodule() {
			taken[strings.ToLower(m.dir)] = true
		}
	}
	return taken
}

// rootAgain makes a submodule a top-level member again, in a directory
// of its own named after it.
func rootAgain(m groupMember, label string, taken map[string]bool) groupMember {
	m.dir = uniqueDir(suggestMemberDir(label), taken)
	taken[strings.ToLower(m.dir)] = true
	m.parent, m.path, m.origin = "", "", "default"
	return m
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

// remove takes a member out. Its submodules become top-level again rather
// than going with it: one repository was removed, not those inside it.
func (d groupDraft) remove(id string, labelOf func(groupMember) string) groupDraft {
	d.members = slices.DeleteFunc(slices.Clone(d.members), func(m groupMember) bool { return m.id == id })
	taken := d.rootDirs()
	for i, m := range d.members {
		if m.parent == id {
			d.members[i] = rootAgain(m, memberLabel(m, labelOf), taken)
		}
	}
	return d
}

// memberLabel is a member's name, by labelOf when there is one.
func memberLabel(m groupMember, labelOf func(groupMember) string) string {
	switch {
	case labelOf != nil:
		return labelOf(m)
	case m.folder != nil:
		return m.folder.name
	case m.source.kind == "github":
		return m.source.repo
	}
	return m.source.local
}

// retain drops the members a scope cannot hold: folders of another
// machine, and every folder for a GitHub-only project, even one a GitHub
// remote made portable. GitHub only means a clone for each conversation,
// never a machine's copy.
func (d groupDraft) retain(scope groupScope, labelOf func(groupMember) string) groupDraft {
	for _, m := range d.members {
		machine := ""
		if m.folder != nil {
			machine = m.folder.machine
		} else if m.source.kind == "local" {
			machine = m.source.machine
		}
		if machine != "" && (scope.kind == "github" || machine != scope.machine) {
			d = d.remove(m.id, labelOf)
		}
	}
	if scope.kind == "github" {
		d.members = slices.Clone(d.members)
		for i := range d.members {
			d.members[i].source.preferLocal = false
		}
	}
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
	ids := map[string]bool{}
	for _, m := range d.members {
		ids[m.id] = true
	}
	for _, m := range d.members {
		if m.submodule() {
			switch {
			case !safeProjectPath(m.path):
				v.memberErrors[m.id] = "submodule_path_invalid"
			case !ids[m.parent] || m.parent == m.id:
				v.memberErrors[m.id] = "submodule_parent_missing"
			}
			continue
		}
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
	// The contract rejects the tree as a whole, overlapping or in a cycle:
	// the submodules are marked, as what a user can fix, or else all.
	if len(v.memberErrors) == 0 && len(d.members) > 0 {
		if _, err := memberPaths(d.members); err != nil {
			for _, m := range d.members {
				if m.submodule() {
					v.memberErrors[m.id] = "path_overlap"
				}
			}
			if len(v.memberErrors) == 0 {
				for _, m := range d.members {
					v.memberErrors[m.id] = "path_overlap"
				}
			}
		}
	}
	v.valid = !v.nameRequired && !v.nameTaken && !v.membersRequired && len(v.memberErrors) == 0
	return v
}

var errMemberPaths = errors.New("invalid project member paths")

// memberPaths is each member's directory in a conversation, as Lody's
// resolveProjectMemberPaths: a top-level member at its dir, a submodule at
// its path within its parent's; none of them inside another unless it is
// that one's submodule.
func memberPaths(members []groupMember) (map[string]string, error) {
	byID := map[string]groupMember{}
	for _, m := range members {
		if _, dup := byID[m.id]; dup {
			return nil, errMemberPaths
		}
		byID[m.id] = m
	}
	paths, visiting := map[string]string{}, map[string]bool{}
	var visit func(id string) (string, error)
	visit = func(id string) (string, error) {
		if p, ok := paths[id]; ok {
			return p, nil
		}
		m, ok := byID[id]
		if !ok || visiting[id] {
			return "", errMemberPaths
		}
		visiting[id] = true
		suffix := m.dir
		if m.submodule() {
			suffix = m.path
		}
		if !safeProjectPath(suffix) || !m.submodule() && strings.Contains(suffix, "/") {
			return "", errMemberPaths
		}
		path := suffix
		if m.submodule() {
			parent, err := visit(m.parent)
			if err != nil {
				return "", err
			}
			path = parent + "/" + suffix
		}
		delete(visiting, id)
		paths[id] = path
		return path, nil
	}
	for _, m := range members {
		if _, err := visit(m.id); err != nil {
			return nil, err
		}
	}
	// One inside another only as its submodule, however deep.
	inside := func(parent string, child groupMember) bool {
		for child.submodule() {
			if child.parent == parent {
				return true
			}
			child = byID[child.parent]
		}
		return false
	}
	for i, a := range members {
		for _, b := range members[i+1:] {
			ap, bp := strings.ToLower(paths[a.id]), strings.ToLower(paths[b.id])
			if ap == bp || strings.HasPrefix(ap, bp+"/") && !inside(b.id, a) || strings.HasPrefix(bp, ap+"/") && !inside(a.id, b) {
				return nil, errMemberPaths
			}
		}
	}
	return paths, nil
}

// membersRow is the members as the stream stores them.
func (d groupDraft) membersRow() []any {
	members := []any{}
	for _, m := range d.members {
		members = append(members, map[string]any{"id": m.id, "source": m.source.row(), "placement": m.placementRow()})
	}
	return members
}

// row is the project as the workspace stream stores it: a first revision,
// or, editing one, the next, as Lody's buildWorkspaceProjectFromForm. Its
// creator and creation stay; the revision moves only when what it holds
// does, so saving it unchanged hands back the row as it was.
func (d groupDraft) row(id, owner string, now float64, existing *catalog.ProjectGroup) map[string]any {
	row := map[string]any{"v": 1, "id": id, "name": strings.TrimSpace(d.name), "ownerUserId": owner, "createdAtMs": now, "revision": 1, "members": d.membersRow()}
	if instructions := strings.TrimSpace(d.instructions); instructions != "" {
		row["instructions"] = instructions
	}
	if existing == nil {
		return row
	}
	row["ownerUserId"], row["createdAtMs"], row["revision"] = existing.OwnerUserID, existing.CreatedAt, max(existing.Revision, 1)
	before, _ := json.Marshal(existing.Row["members"])
	after, _ := json.Marshal(row["members"])
	if row["name"] == existing.Name && strings.TrimSpace(d.instructions) == strings.TrimSpace(existing.Instructions) && string(before) == string(after) {
		return existing.Row
	}
	row["revision"] = max(existing.Revision, 1) + 1
	return row
}

// draftOf is a saved project as the editor drafts it, as Lody's
// buildProjectGroupFormValue: every placement it saved is the user's.
func draftOf(g catalog.ProjectGroup) groupDraft {
	d := groupDraft{name: g.Name, instructions: g.Instructions}
	raws, _ := g.Row["members"].([]any)
	for _, raw := range raws {
		row, _ := raw.(map[string]any)
		id, _ := row["id"].(string)
		src, _ := row["source"].(map[string]any)
		at, _ := row["placement"].(map[string]any)
		m := groupMember{id: id, source: sourceOf(src), origin: "user"}
		if at["kind"] == "submodule" {
			m.parent, _ = at["parent"].(string)
			m.path, _ = at["path"].(string)
		} else {
			m.dir, _ = at["dir"].(string)
		}
		if m.id != "" && (m.source.kind == "github" || m.source.kind == "local") {
			d.members = append(d.members, m)
		}
	}
	return d
}

// detectedSubmodule is one submodule a member's repository declares, and
// the repository it points at.
type detectedSubmodule struct {
	path, repo string
}

// applySubmodules places members inside the member that declares them as
// submodules, as Lody's applyDetectedProjectSubmodules: a member becomes
// parent's submodule when one of parent's points at its repository. Only
// a default or detected placement moves, never the user's; a detected one
// no longer declared goes back to the top. A result the contract would
// reject, as two repositories vendoring each other, is not applied.
func (d groupDraft) applySubmodules(detected map[string][]detectedSubmodule, repositoryOf, labelOf func(groupMember) string) groupDraft {
	byRepo := map[string]string{}
	for _, m := range d.members {
		if repo := strings.ToLower(repositoryOf(m)); repo != "" {
			if _, ok := byRepo[repo]; !ok {
				byRepo[repo] = m.id
			}
		}
	}
	type place struct{ parent, path string }
	next := map[string]place{}
	for _, parent := range d.members {
		for _, sub := range detected[parent.id] {
			child := byRepo[strings.ToLower(sub.repo)]
			if _, taken := next[child]; sub.repo == "" || child == "" || child == parent.id || taken || !safeProjectPath(sub.path) {
				continue
			}
			next[child] = place{parent.id, sub.path}
		}
	}
	taken := d.rootDirs()
	members := slices.Clone(d.members)
	changed := false
	for i, m := range members {
		if m.origin == "user" {
			continue
		}
		if at, ok := next[m.id]; ok {
			if m.parent != at.parent || m.path != at.path {
				members[i].parent, members[i].path, members[i].origin = at.parent, at.path, "detected"
				changed = true
			}
			continue
		}
		if m.origin == "detected" && m.submodule() {
			members[i] = rootAgain(m, memberLabel(m, labelOf), taken)
			changed = true
		}
	}
	if !changed {
		return d
	}
	if _, err := memberPaths(members); err != nil {
		return d
	}
	d.members = members
	return d
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

// takenGroupNames are the other projects' names, normalized: the one the
// wizard edits keeps its own.
func (a *App) takenGroupNames() map[string]bool {
	taken := map[string]bool{}
	for _, g := range a.catalog.Groups {
		if g.ID != a.projectWizard.id {
			taken[normalizeGroupName(g.Name)] = true
		}
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
	case "submodule_path_invalid":
		return locales.GroupErrSubmodulePath()
	case "submodule_parent_missing":
		return locales.GroupErrSubmoduleParent()
	case "path_overlap":
		return locales.GroupErrPathOverlap()
	}
	return code
}
