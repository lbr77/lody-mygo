package app

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/github"
)

// Lody's submodule detection for the project editor
// (hooks/use-project-submodule-detection.ts): as members are added, each
// one's repository is asked what its .gitmodules declares, and a member
// another one declares moves inside it, unless the user placed it. A
// folder is read by its machine (project/probe-local-member), a folder
// picked from the browser by what its machine inspected; a repository
// from a copy on an online machine, this one first, or else from GitHub
// at its base branch. An offline machine is not asked at all: that is
// not something the editor reports.

// gitmodulesMax is the most of a .gitmodules GitHub is read for.
const gitmodulesMax = 64 * 1024

// groupProbe is one source's answer: what its repository declares, or
// nothing where it cannot be read. A failure is asked again the next
// time the wizard opens.
type groupProbe struct {
	done, failed bool
	result       *probeResult
}

// probeResult is what a member's repository declares: which GitHub
// repository it is, and its submodules.
type probeResult struct {
	repo       string
	submodules []detectedSubmodule
}

// probeEnv is what a probe reads with, taken on the main thread.
type probeEnv struct {
	conn    *cloud.Conn
	user    string
	offline bool
	token   func(ctx context.Context, repo string) (string, error)
}

// groupProbeRead reads a source's submodules: a machine's local project
// over its daemon, or a GitHub repository at its base branch, or its
// default one. Nil is "cannot be read here". Tests stand in for it.
var groupProbeRead = func(ctx context.Context, env probeEnv, source groupSource) (*probeResult, error) {
	if env.offline {
		return nil, nil
	}
	if source.kind == "local" {
		if env.conn == nil {
			return nil, nil
		}
		raw, err := env.conn.MachineRPC(ctx, source.machine, "project/probe-local-member",
			map[string]any{"localProjectId": source.local, "requestedByUserId": env.user}, 30*time.Second)
		if err != nil {
			return nil, err
		}
		var res struct {
			RepoFullName string `json:"repoFullName"`
			Submodules   []struct {
				Path         string `json:"path"`
				URL          string `json:"url"`
				RepoFullName string `json:"repoFullName"`
			} `json:"submodules"`
		}
		if string(raw) == "null" || len(raw) == 0 {
			return nil, nil
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, err
		}
		out := &probeResult{repo: res.RepoFullName}
		for _, s := range res.Submodules {
			out.submodules = append(out.submodules, detectedSubmodule{path: s.Path, repo: firstNonEmpty(s.RepoFullName, submoduleRepo(s.URL, res.RepoFullName))})
		}
		return out, nil
	}
	if env.token == nil {
		return nil, nil
	}
	repo := source.repo
	token, err := env.token(ctx, repo)
	if err != nil {
		return nil, err
	}
	gh := &github.Client{Token: token}
	ref := source.baseBranch
	if ref == "" {
		if ref, err = gh.DefaultBranch(ctx, repo); err != nil {
			return nil, err
		}
	}
	text, err := gh.FileAt(ctx, repo, ".gitmodules", ref, gitmodulesMax)
	if github.IsStatus(err, 404) {
		return &probeResult{repo: repo}, nil
	}
	if err != nil {
		return nil, err
	}
	out := &probeResult{repo: repo}
	for _, e := range parseGitmodules(text) {
		out.submodules = append(out.submodules, detectedSubmodule{path: e.path, repo: submoduleRepo(e.url, repo)})
	}
	return out, nil
}

// gitmodule is one submodule a .gitmodules declares.
type gitmodule struct{ name, path, url string }

var (
	gitmodulesHeader = regexp.MustCompile(`(?i)^\[\s*submodule\s+"((?:[^"\\]|\\.)*)"\s*\]$`)
	gitmodulesPair   = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9-]*)\s*=\s*(.*)$`)
	gitmodulesNote   = regexp.MustCompile(`\s+[#;].*$`)
	unescape         = regexp.MustCompile(`\\(.)`)
)

// parseGitmodules reads a .gitmodules' submodules, as Lody's small reader
// of Git's config: section headers, key = value lines, # and ; comments,
// quoted values. One missing its path or its URL is dropped, not guessed.
func parseGitmodules(text string) []gitmodule {
	var out []gitmodule
	var cur *gitmodule
	flush := func() {
		if cur != nil && cur.path != "" && cur.url != "" {
			out = append(out, *cur)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if m := gitmodulesHeader.FindStringSubmatch(line); m != nil {
			flush()
			cur = &gitmodule{name: unescape.ReplaceAllString(m[1], "$1")}
			continue
		}
		if strings.HasPrefix(line, "[") {
			flush()
			cur = nil
			continue
		}
		m := gitmodulesPair.FindStringSubmatch(line)
		if cur == nil || m == nil {
			continue
		}
		value := strings.TrimSpace(gitmodulesNote.ReplaceAllString(m[2], ""))
		if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
			value = value[1 : len(value)-1]
		}
		switch strings.ToLower(m[1]) {
		case "path":
			cur.path = strings.TrimRight(value, "/")
		case "url":
			cur.url = value
		}
	}
	flush()
	return out
}

var githubRemote = regexp.MustCompile(`(?i)^(?:https?://(?:[^@/]+@)?github\.com/|git://github\.com/|ssh://(?:[^@/]+@)?github\.com(?::\d+)?/|[^@/\s]+@github\.com:)([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

// submoduleRepo is the GitHub repository a submodule's URL names: any
// form Git takes of a github.com remote, or one relative to its parent's
// repository, as Git resolves ../proto.git against the superproject.
func submoduleRepo(url, parent string) string {
	url = strings.TrimSpace(url)
	if m := githubRemote.FindStringSubmatch(url); m != nil {
		return m[1] + "/" + m[2]
	}
	if parent == "" || !strings.HasPrefix(url, "../") && !strings.HasPrefix(url, "./") {
		return ""
	}
	segments := strings.Split(parent, "/")
	for _, part := range strings.Split(strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git"), "/") {
		switch part {
		case ".", "":
		case "..":
			if len(segments) == 0 {
				return ""
			}
			segments = segments[:len(segments)-1]
		default:
			segments = append(segments, part)
		}
	}
	if len(segments) != 2 {
		return ""
	}
	return strings.Join(segments, "/")
}

// probeTarget is what a member is read from: its own source, or a
// machine's copy of its repository; repo is then the member's, whatever
// the copy's remote says.
type probeTarget struct {
	key    string
	source groupSource
	repo   string
}

// probeTargetOf is where a member's submodules are read, or false where
// they are not asked for: a folder on a machine offline.
func (f *wizardFrame) probeTargetOf(m groupMember) (probeTarget, bool) {
	key := m.source.key() + "@" + m.source.baseBranch
	if m.source.kind == "local" {
		return probeTarget{key: key, source: m.source}, f.a.machineReachable(m.source.machine)
	}
	if m.source.preferLocal {
		// A copy on an online machine, this one first: the same
		// .gitmodules, without GitHub.
		if o := f.byKey[m.source.key()]; o != nil {
			var copy *memberCopy
			for i, cp := range o.copies {
				if !f.a.machineReachable(cp.machine) {
					continue
				}
				if copy == nil || cp.machine == f.current {
					copy = &o.copies[i]
				}
			}
			if copy != nil {
				local := localSource(copy.machine, copy.local)
				return probeTarget{key: key + "@" + local.key(), source: local, repo: m.source.repo}, true
			}
		}
	}
	return probeTarget{key: key, source: m.source, repo: m.source.repo}, true
}

// detectSubmodules asks each member's repository what it declares, once
// a source, and folds the answers into the draft as they come: the draft
// is handed back unchanged when nothing moves, so this settles.
func (a *App) detectSubmodules(f *wizardFrame) {
	wz := &a.projectWizard
	if wz.probes == nil {
		wz.probes = map[string]*groupProbe{}
	}
	detected := map[string][]detectedSubmodule{}
	repos := map[string]string{}
	for _, m := range wz.draft.members {
		var result *probeResult
		if m.folder != nil {
			// What its machine inspected as it was picked.
			result = &probeResult{repo: m.folder.repo, submodules: m.folder.submodules}
			if m.source.kind == "github" {
				result.repo = m.source.repo
			}
		} else {
			t, ask := f.probeTargetOf(m)
			if !ask {
				continue
			}
			pr := wz.probes[t.key]
			if pr == nil {
				pr = &groupProbe{}
				wz.probes[t.key] = pr
				a.startProbe(t, pr)
			}
			if !pr.done || pr.result == nil {
				continue
			}
			result = &probeResult{repo: firstNonEmpty(t.repo, pr.result.repo), submodules: pr.result.submodules}
		}
		detected[m.id] = result.submodules
		if result.repo != "" {
			repos[m.id] = result.repo
		}
	}
	repositoryOf := func(m groupMember) string {
		if m.source.kind == "github" {
			return m.source.repo
		}
		return repos[m.id]
	}
	wz.draft = wz.draft.applySubmodules(detected, repositoryOf, f.labelOf)
	wz.repos = map[string]string{}
	for _, m := range wz.draft.members {
		if repo := repos[m.id]; repo != "" && m.source.kind == "local" {
			wz.repos[m.source.key()] = repo
		}
	}
}

// startProbe reads one source in the background.
func (a *App) startProbe(t probeTarget, pr *groupProbe) {
	env := probeEnv{conn: a.conn, offline: a.offline, token: func(ctx context.Context, repo string) (string, error) { return a.githubToken(ctx, repo, false) }}
	if a.account != nil {
		env.user = a.account.User.ID
	}
	if a.convex == nil {
		env.token = nil
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		result, err := groupProbeRead(ctx, env, t.source)
		a.update(func() {
			pr.done, pr.failed, pr.result = true, err != nil, result
		})
	}()
}
