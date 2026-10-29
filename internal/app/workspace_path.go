package app

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/lbr77/lody-mygo/internal/catalog"
)

// Where a chat works on its machine, as Lody resolves it: a worktree lies
// at <.lody>/repos/<repo id>/worktrees/<owner session>; any other chat
// works in its local project's folder, or else in <.lody>/chats/<owner
// session>. A machine reports its .lody folder; this one's is ~/.lody.

var (
	safeSessionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	driveRoot     = regexp.MustCompile(`^[A-Za-z]:/$`)
	windowsPath   = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
)

// sessionWorkspacePath is the folder s works in, "" when it can't be told.
func (a *App) sessionWorkspacePath(s *catalog.Session) string {
	dotlody := strings.TrimSpace(a.catalog.DotLody[s.MachineID])
	if dotlody == "" && s.MachineID != "" && s.MachineID == a.localMachine() {
		if home, err := os.UserHomeDir(); err == nil {
			dotlody = hostJoin(home, ".lody")
		}
	}
	root := ""
	if pr := a.catalog.Project(s.ProjectID); pr != nil {
		root = strings.TrimSpace(pr.RootPath)
	}
	repo := strings.TrimSpace(s.RepoFullName)
	if r, _ := s.Project["githubRepoFullName"].(string); repo == "" {
		repo = strings.TrimSpace(r)
	}
	if strings.HasPrefix(s.ProjectID, "github:") {
		repo = strings.TrimPrefix(s.ProjectID, "github:")
	}
	owner := firstNonEmpty(s.ParentSessionID, s.ID)
	if !safeSessionID.MatchString(owner) {
		return ""
	}
	if s.IsWorktree && dotlody != "" {
		id := ""
		switch {
		case root != "":
			id = localRepoID(root)
		case repo != "":
			id = githubRepoID(repo)
		}
		if id != "" {
			return hostJoin(dotlody, "repos", id, "worktrees", owner)
		}
	}
	if !s.IsWorktree {
		if root != "" {
			return root
		}
		if dotlody != "" {
			return hostJoin(dotlody, "chats", owner)
		}
	}
	return ""
}

// localRepoID is a local project's repo id: a hash of its root folder.
func localRepoID(root string) string {
	p := strings.ReplaceAll(strings.TrimSpace(root), `\`, "/")
	if p != "/" && !driveRoot.MatchString(p) {
		p = strings.TrimRight(p, "/")
	}
	if p == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(p))
	return "local---" + hex.EncodeToString(sum[:])[:12]
}

// githubRepoID is a GitHub repository's repo id.
func githubRepoID(repo string) string {
	repo = strings.TrimSuffix(strings.Trim(strings.TrimSpace(repo), "/"), ".git")
	repo = strings.TrimPrefix(strings.TrimPrefix(repo, "https://github.com/"), "git@github.com:")
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return ""
	}
	return "github---" + strings.ToLower(owner) + "---" + strings.ToLower(name)
}

// hostJoin joins a path the machine reported with more parts, in its own
// separators: a Windows machine's backslashes stay.
func hostJoin(base string, parts ...string) string {
	b := strings.ReplaceAll(strings.TrimSpace(base), `\`, "/")
	if b != "/" && !driveRoot.MatchString(b) {
		b = strings.TrimRight(b, "/")
	}
	joined := path.Join(append([]string{b}, parts...)...)
	if windowsPath.MatchString(base) || strings.Contains(base, `\`) {
		return strings.ReplaceAll(joined, "/", `\`)
	}
	return joined
}
