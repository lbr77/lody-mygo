package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// A new chat's working directory, as Lody's landing offers it for a Git
// project: the project's own folder on its current branch, or a worktree
// of its own from a branch the user picks (local-project/git-state).

// gitState is a project's Git, as its machine last said.
type gitState struct {
	loading  bool
	err      string
	git      bool
	branches []string
	current  string
	def      string // the default branch
	repo     string // owner/name on GitHub
	clean    bool
}

// projectGit fetches a project's Git the first time it is asked for.
func (a *App) projectGit(projectID string) *gitState {
	if a.gitStates == nil {
		a.gitStates = map[string]*gitState{}
	}
	if st := a.gitStates[projectID]; st != nil {
		return st
	}
	st := &gitState{loading: true}
	a.gitStates[projectID] = st
	pr := a.catalog.Project(projectID)
	conn := a.conn
	if pr == nil || conn == nil || a.account == nil {
		st.loading = false
		return st
	}
	_, local, _ := strings.Cut(pr.ID, ":local:")
	machine, user := pr.MachineID, a.account.User.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		raw, err := conn.MachineRPC(ctx, machine, "local-project/git-state", map[string]any{"localProjectId": local, "requestedByUserId": user}, 30*time.Second)
		var res struct {
			Success bool   `json:"success"`
			Error   string `json:"error"`
			Message string `json:"message"`
			State   struct {
				Git         bool     `json:"git"`
				Branches    []string `json:"branches"`
				Current     *string  `json:"currentBranch"`
				Default     *string  `json:"defaultBranch"`
				Repo        *string  `json:"githubRepoFullName"`
				WorkingTree struct {
					Clean bool `json:"clean"`
				} `json:"workingTree"`
			} `json:"state"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &res)
		}
		a.update(func() {
			st.loading = false
			switch {
			case err != nil:
				st.err = err.Error()
			case !res.Success:
				st.err = firstNonEmpty(res.Message, res.Error)
			default:
				s := res.State
				st.git, st.branches, st.clean = s.Git, s.Branches, s.WorkingTree.Clean
				if s.Current != nil {
					st.current = *s.Current
				}
				if s.Default != nil {
					st.def = *s.Default
				}
				if s.Repo != nil {
					st.repo = *s.Repo
				}
			}
		})
	}()
	return st
}

// newChatBranch is the branch a worktree starts from: the one picked, or
// the current, or the default.
func (a *App) newChatBranch(st *gitState) string {
	if b := a.newChat.branch; b != "" {
		for _, x := range st.branches {
			if x == b {
				return b
			}
		}
	}
	return firstNonEmpty(st.current, st.def)
}

// workdirControls are the Local / Worktree switch and the branch, after
// the project; for a GitHub repository, its branch and its worktree, which
// it always has.
func (a *App) workdirControls(c *ui.Context, p *palette) {
	if repo, ok := strings.CutPrefix(a.newChat.project, "github:"); ok {
		a.repoWorkdirControls(c, p, repo)
		return
	}
	// Choosing the repository again reads its branches again.
	a.repoBranchShown = ""
	pr := a.catalog.Project(a.newChat.project)
	if pr == nil || !strings.Contains(pr.ID, ":local:") || a.conn == nil {
		return
	}
	st := a.projectGit(pr.ID)
	if st.loading {
		tip(c, p, ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(0, 6), locales.WorkdirChecking()).Children(func() {
			spinner(c).Size(11, 11)
		})
		return
	}
	if !st.git {
		return
	}
	worktree := a.newChat.worktree
	mode := ui.ButtonBase(c).Height(24).Padding(0, 8).Radius(12).Gap(4).Cursor(ui.CursorPointer).Label(locales.WorkdirMode()).
		OnClick(func() { a.newChat.worktree = !a.newChat.worktree }).Transition(colorFade)
	tip(c, p, mode, map[bool]string{false: locales.WorkdirLocalHint(), true: locales.WorkdirWorktreeHint()}[worktree])
	switch {
	case worktree:
		mode.Background(p.Primary.Alpha(0.12))
	case mode.Hovered():
		mode.Background(p.Text.Alpha(0.06))
	}
	mode.Children(func() {
		icon, label, ink := iconFolder, locales.WorkdirLocal(), p.Text.Alpha(0.8)
		if worktree {
			icon, label, ink = iconWorktree, locales.WorkdirWorktree(), p.Primary
		}
		ui.Icon(c, icon).Size(14, 14).TextColor(ink)
		ui.Text(c, label).FontSize(12.6).TextColor(ink).SingleLine()
	})
	branch := a.newChatBranch(st)
	if branch == "" {
		return
	}
	if !worktree {
		// The chat runs in the folder as it is, on its branch.
		tip(c, p, ui.Row(c).Height(24).Padding(0, 6).Gap(4).AlignItems(ui.Center), branch).Children(func() {
			ui.Icon(c, iconBranch).Size(14, 14).TextColor(p.Secondary)
			ui.Text(c, branch).FontSize(12.6).TextColor(p.Secondary).SingleLine().MaxWidth(180)
		})
		return
	}
	var options []option
	for _, b := range st.branches {
		options = append(options, option{b, b})
	}
	if len(options) == 0 {
		options = []option{{branch, branch}}
	}
	a.toolbarPicker(c, p, "new-branch", iconBranch, branch, options, branch, func(b string) { a.newChat.branch = b })
}
