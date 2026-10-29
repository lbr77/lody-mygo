package app

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/github"
	"github.com/lbr77/lody-mygo/locales"
)

// A new chat in one of the workspace's GitHub repositories, as Lody's chat
// landing starts one: the machine checks the repository out into a
// worktree of the session's own, from a branch picked among the
// repository's. The branches are read from GitHub with a token the
// workspace mints for this client, kept by workspace and repository and
// read again each time the repository is chosen. Unless the user picks
// one, the branch is main, else master, else the repository's default,
// else the first; an empty repository has none, and starts without.

// repoBranches is a repository's branches, as GitHub last listed them.
type repoBranches struct {
	loading  bool
	err      string
	branches []string // sorted
	def      string   // the default branch; "" for an empty repository
	query    string   // the picker's search
}

// listRepoBranches reads repo's branches for workspace ws; tests replace
// it.
var listRepoBranches = func(ctx context.Context, convex *cloud.Convex, ws, repo string) ([]string, string, error) {
	token, err := workspaceGitHubToken(ctx, convex, ws, repo, false)
	if err != nil {
		return nil, "", err
	}
	return (&github.Client{Token: token}).Branches(ctx, repo)
}

// repoBranchList is repo's branches, read from GitHub the first time they
// are asked for and again with refresh, what was read showing meanwhile.
func (a *App) repoBranchList(repo string, refresh bool) *repoBranches {
	if a.repoBranchLists == nil {
		a.repoBranchLists = map[string]*repoBranches{}
	}
	key := a.workspaceID + ":" + repo
	rb := a.repoBranchLists[key]
	if rb == nil {
		rb = &repoBranches{}
		a.repoBranchLists[key] = rb
		refresh = true
	}
	if !refresh || rb.loading || a.convex == nil || a.noCloud() || a.workspaceID == "" {
		return rb
	}
	rb.loading = true
	convex, ws := a.convex, a.workspaceID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		branches, def, err := listRepoBranches(ctx, convex, ws, repo)
		a.update(func() {
			rb.loading = false
			if err != nil {
				// What was read before stays, as Lody keeps its cache.
				rb.err = err.Error()
				return
			}
			rb.err, rb.branches, rb.def = "", branches, def
		})
	}()
	return rb
}

// repoBranch is the branch a repository session starts from: picked while
// the repository has it, else main, else master, else its default, else
// its first; none while it lists no branch.
func repoBranch(rb *repoBranches, picked string) string {
	if len(rb.branches) == 0 {
		return ""
	}
	for _, want := range []string{strings.TrimSpace(picked), "main", "master", rb.def} {
		if want != "" && slices.Contains(rb.branches, want) {
			return want
		}
	}
	return rb.branches[0]
}

// repoWorkdirControls are a repository's branch, then its worktree, which
// is always the session's own.
func (a *App) repoWorkdirControls(c *ui.Context, p *palette, repo string) {
	key := a.workspaceID + ":" + repo
	rb := a.repoBranchList(repo, a.repoBranchShown != key)
	a.repoBranchShown = key
	branch := repoBranch(rb, a.newChat.branch)
	a.branchPicker(c, p, "repo-branch", rb, branch, func(b string) { a.newChat.branch = b })
	pill := ui.Row(c.Key("repo-worktree")).Height(24).Padding(0, 8).Radius(12).Gap(4).AlignItems(ui.Center).Background(p.Primary.Alpha(0.12))
	tip(c, p, pill, locales.WorkdirGitHubRequired())
	pill.Children(func() {
		ui.Icon(c, iconWorktree).Size(14, 14).TextColor(p.Primary)
		ui.Text(c, locales.WorkdirWorktree()).FontSize(12.6).TextColor(p.Primary).SingleLine()
	})
}

// branchPicker is Lody's branch selector: the branch mark and the branch,
// the placeholder while there is none, opening the branches, which a
// search narrows once there are more than six. With no branch to pick it
// is inert.
func (a *App) branchPicker(c *ui.Context, p *palette, key string, rb *repoBranches, branch string, pick func(string)) {
	open := a.popover(key)
	label := firstNonEmpty(branch, locales.BranchPlaceholder())
	disabled := len(rb.branches) == 0
	b := ui.ButtonBase(c.Key(key)).Height(24).Padding(0, 6).Radius(6).Gap(4).Label(label).Disabled(disabled).Transition(colorFade)
	ink := p.Secondary
	if disabled {
		*open = false
		b.Opacity(0.5)
	} else {
		b.Cursor(ui.CursorPointer).OnClick(func() {
			*open = !*open
			rb.query = ""
		})
		if b.Hovered() || *open {
			b.Background(p.Text.Alpha(0.06))
			ink = p.Text
		}
	}
	b.Children(func() {
		ui.Icon(c, iconBranch).Size(14, 14).TextColor(ink)
		ui.Text(c, label).FontSize(12.6).TextColor(ink).SingleLine().MaxWidth(220)
	})
	if disabled {
		return
	}
	popup(c, b, open, func() {
		ui.Column(c).MinWidth(320).MaxWidth(576).Gap(4).Children(func() {
			if len(rb.branches) > 6 {
				ui.Row(c.Key("search")).FillWidth().Height(30).Padding(0, 10).Gap(8).Radius(8).Background(p.Text.Alpha(0.05)).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconSearch).Size(14, 14).TextColor(p.Tertiary).Shrink(0)
					ui.TextInputBase(c.Key("query"), &rb.query).Placeholder(locales.BranchSearchPlaceholder()).Label(locales.BranchSearchPlaceholder()).
						FontSize(sizeSub).Grow(1).AutoFocus()
				})
			}
			shown := filterBranches(rb.branches, rb.query)
			if len(shown) == 0 {
				ui.Text(c, locales.BranchEmpty()).FontSize(sizeSub).TextColor(p.Secondary).Padding(8)
				return
			}
			ui.Scroll(c.Key("branches")).FillWidth().MaxHeight(320).Gap(1).Children(func() {
				for _, name := range shown {
					row := menuRow(c.Key(name), p, name)
					if row.Clicked() {
						pick(name)
						*open = false
					}
					row.Children(func() {
						ui.Text(c, name).FontSize(sizeSub).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
						if name == branch {
							ui.Icon(c, iconCheck).Size(14, 14).TextColor(p.Text)
						}
					})
				}
			})
		})
	})
}

// filterBranches are the branches whose names hold query, ignoring case.
func filterBranches(branches []string, query string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return branches
	}
	var out []string
	for _, b := range branches {
		if strings.Contains(strings.ToLower(b), q) {
			out = append(out, b)
		}
	}
	return out
}

// startRepoChat creates a session in a checkout of repo, from the branch
// picked, and sends its first message. Lody asks for a branch while the
// repository has any; one whose branches are still on their way has none
// picked yet.
func (a *App) startRepoChat(repo string) {
	text := strings.TrimSpace(a.draft)
	nc := a.newChat
	agent := a.agent(nc.machine, nc.agent)
	files := a.attachments
	if (text == "" && len(files) == 0) || a.sending || agent == nil || a.sync == nil || a.conn == nil || a.account == nil {
		return
	}
	rb := a.repoBranchList(repo, false)
	branch := repoBranch(rb, nc.branch)
	if branch == "" && (rb.loading || len(rb.branches) > 0) {
		a.sendError = locales.BranchMissing()
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
		GitHubProject: repo, Branch: branch,
	}, chatSend{key: "", text: text, files: files, model: model, mode: mode, runValues: a.chosenRunValues("", agent.ID, model), mcp: a.mcpSelection(""),
		target: target, skills: skills, sessions: sessions, store: a.shortcutStore()}, nil)
}
