package app

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// GitHub projects, as Lody's projects page lists them under the
// machines' folders: the workspace's repositories by owner, each opening
// a window with the repository and the scripts its worktrees run, Bash
// and PowerShell both, as a GitHub project may be cloned on either.

// githubProject is the window of a GitHub project.
type githubProject struct {
	open    bool
	repo    string // owner/name
	page    string
	setup   githubScripts
	cleanup githubScripts
}

// githubScripts is one phase's scripts as edited.
type githubScripts struct {
	shell     string // bash or powershell, the tab shown
	bash, ps  string
	from      string // the saved config the text was loaded from
	saving    bool
	err       string
	hasEdited bool
}

func (a *App) workspaceRepos() []workspaceRepo {
	convex, ws := a.convex, a.workspaceID
	f := a.fetch("github", func(ctx context.Context) (any, error) {
		var out *[]workspaceRepo
		err := convex.Query(ctx, "github:listWorkspaceReposWithStatus", map[string]any{"workspaceId": ws}, &out)
		return out, err
	})
	if r, _ := f.value.(*[]workspaceRepo); r != nil {
		return *r
	}
	return nil
}

// githubProjectSections are the repositories by owner, owners and names in
// order.
func (a *App) githubProjectSections(c *ui.Context, p *palette) {
	byOwner := map[string][]workspaceRepo{}
	for _, r := range a.workspaceRepos() {
		owner, _, _ := strings.Cut(r.RepoFullName, "/")
		if owner = strings.TrimSpace(owner); owner == "" {
			owner = "GitHub"
		}
		byOwner[owner] = append(byOwner[owner], r)
	}
	owners := make([]string, 0, len(byOwner))
	for o := range byOwner {
		owners = append(owners, o)
	}
	sort.Strings(owners)
	for i, owner := range owners {
		rows := byOwner[owner]
		sort.Slice(rows, func(i, j int) bool { return rows[i].RepoFullName < rows[j].RepoFullName })
		ui.Row(c.Key("gh/"+owner)).FillWidth().Gap(8).Margin(8, 0, 4, 0).AlignItems(ui.End).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, owner).FontSize(sizeBody).FontWeight(500).TextColor(p.Strong)
				ui.Text(c, "GitHub").FontSize(sizeFoot).TextColor(p.Secondary)
			})
			if i == 0 {
				ghostButton(c, p, locales.GitHubManageInSettings()).OnClick(func() { a.settingsTab = "github" })
			}
		})
		ui.Column(c.Key("gh/"+owner+"/list")).FillWidth().Radius(10).Border(1, p.Border).Clip().Dividers(1, p.Border).Children(func() {
			for _, r := range rows {
				repo := r
				row := ui.ButtonBase(c.Key(r.RepoFullName)).FillWidth().MinHeight(48).Padding(8, 8, 8, 16).Gap(10).AlignItems(ui.Center).
					Cursor(ui.CursorPointer).Label(r.RepoFullName).OnClick(func() { a.openGitHubProject(repo.RepoFullName) }).Transition(colorFade)
				if row.Hovered() {
					row.Background(p.Hover)
				}
				row.Children(func() {
					ui.Icon(c, iconBrandGitHub).Size(16, 16).TextColor(p.Secondary)
					ui.Text(c, firstNonEmpty(repo.Name, repo.RepoFullName)).FontSize(sizeBody).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
					if repo.Private {
						pill(c, p, iconLock, locales.GitHubPrivate())
					}
					ui.Icon(c, iconChevronRight).Size(14, 14).TextColor(p.Tertiary)
				})
			}
		})
	}
}

func (a *App) openGitHubProject(repo string) {
	a.ghProject = githubProject{open: true, repo: repo, page: "general"}
}

var (
	bashExport = regexp.MustCompile(`(^|[\n;&|])\s*export\s+[A-Za-z_]`)
	psEnvSet   = regexp.MustCompile(`(?i)\$env:[A-Za-z_]\w*\s*=|\[Environment\]::SetEnvironmentVariable`)
)

// setsEphemeralEnv is whether a script sets variables the agent won't see.
func setsEphemeralEnv(shell, script string) bool {
	if shell == "powershell" {
		return psEnvSet.MatchString(script)
	}
	return bashExport.MatchString(script)
}

// scriptsConfig is the config Lody stores for the two scripts: the empty
// ones left out.
func scriptsConfig(bash, ps string, timeoutMs float64) map[string]any {
	scripts := map[string]any{}
	if strings.TrimSpace(bash) != "" {
		scripts["bash"] = bash
	}
	if strings.TrimSpace(ps) != "" {
		scripts["powershell"] = ps
	}
	config := map[string]any{"scripts": scripts}
	if timeoutMs > 0 {
		config["timeoutMs"] = int64(timeoutMs)
	}
	return config
}

func (a *App) githubProjectWindow(c *ui.Context, p *palette) {
	w := &a.ghProject
	if !w.open {
		return
	}
	var repo *workspaceRepo
	repos := a.workspaceRepos()
	for i := range repos {
		if repos[i].RepoFullName == w.repo {
			repo = &repos[i]
		}
	}
	pages := []struct{ id, label, lead string }{
		{"general", locales.ProjectPageGeneral(), locales.ProjectPageGeneralLead()},
		{"worktree", "Worktree", locales.ProjectPageWorktreeLead()},
	}
	ww, hh := c.Size()
	ui.DialogBase(c, &w.open, func(backdrop, panel ui.Element) {
		sheetIn(backdrop, panel)
		backdrop.Background(ui.RGBA(0, 0, 0, 0.22))
		panel.Size(min(920, ww-80), min(640, hh-80)).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Clip().
			Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(240).Shrink(0).Background(p.Ground).BorderWidth(0, 1, 0, 0).BorderColor(p.Border).Padding(18, 10).Gap(2).Children(func() {
				ui.Column(c).Padding(0, 8, 14, 8).Gap(4).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, iconBrandGitHub).Size(16, 16).TextColor(p.Secondary)
						name := w.repo
						if repo != nil {
							name = firstNonEmpty(repo.Name, repo.RepoFullName)
						}
						ui.Text(c, name).FontSize(sizeBody).FontWeight(600).TextColor(p.Strong).SingleLine()
					})
					ui.Text(c, "GitHub").FontSize(sizeFoot).TextColor(p.Secondary)
				})
				for _, pg := range pages {
					page := pg
					row := ui.ButtonBase(c.Key(pg.id)).Height(32).Padding(0, 10).Radius(8).Justify(ui.Start).Cursor(ui.CursorPointer).Label(pg.label).
						OnClick(func() { w.page = page.id }).Transition(colorFade)
					switch {
					case w.page == pg.id:
						row.Background(p.SideSelect)
					case row.Hovered():
						row.Background(p.SideHover)
					}
					row.Children(func() { ui.Text(c, page.label).FontSize(sizeSub).TextColor(p.Text) })
				}
			})
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Row(c).FillWidth().Padding(18, 14, 4, 24).Gap(12).AlignItems(ui.Start).Shrink(0).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
						for _, pg := range pages {
							if pg.id == w.page {
								ui.Text(c, pg.label).FontSize(16).FontWeight(600).TextColor(p.Strong)
								ui.Text(c, pg.lead).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Secondary)
							}
						}
					})
					iconButton(c, p, iconX, locales.Close(), p.Hover).OnClick(func() { w.open = false })
				})
				ui.Scroll(c).Grow(1).MinHeight(0).Padding(12, 24, 24, 24).Gap(14).Children(func() {
					if repo == nil {
						ui.Text(c, locales.GitHubRepoUnavailable()).FontSize(sizeFoot).TextColor(p.Secondary)
						return
					}
					switch w.page {
					case "general":
						ui.Row(c).FillWidth().Padding(12, 14).Gap(12).Radius(10).Border(1, p.Border).AlignItems(ui.Center).Children(func() {
							ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
								ui.Text(c, locales.GitHubRepository()).FontSize(sizeSub).TextColor(p.Text)
								ui.Text(c, repo.RepoFullName).Font(fontMono).FontSize(sizeFoot).TextColor(p.Secondary).Selectable()
							})
							outlineButton(c, p, nil, locales.GitHubManageInSettings()).OnClick(func() {
								w.open = false
								a.settingsTab = "github"
							})
						})
					case "worktree":
						a.githubScriptsEditor(c, p, *repo, "setup", &w.setup)
						a.githubScriptsEditor(c, p, *repo, "cleanup", &w.cleanup)
					}
				})
			})
		})
	})
}

// githubScriptsEditor edits a GitHub project's setup or cleanup scripts,
// with a tab for each shell; Save writes both.
func (a *App) githubScriptsEditor(c *ui.Context, p *palette, repo workspaceRepo, phase string, e *githubScripts) {
	saved := repo.WorktreeSetup
	if phase == "cleanup" {
		saved = repo.WorktreeCleanup
	}
	var cfg worktreeScripts
	if saved != nil {
		cfg = *saved
	}
	from := cfg.Scripts.Bash + "\x00" + cfg.Scripts.PowerShell
	if e.from != from && !e.hasEdited {
		e.from, e.bash, e.ps = from, cfg.Scripts.Bash, cfg.Scripts.PowerShell
	}
	if e.shell == "" {
		e.shell = "bash"
	}
	title, lead := "Worktree Setup", locales.WorktreeSetupLead()
	if phase == "cleanup" {
		title, lead = "Worktree Cleanup", locales.WorktreeCleanupLead()
	}
	ui.Column(c.Key(phase)).FillWidth().Gap(8).Padding(14).Radius(10).Border(1, p.Border).Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				icon := iconWrench
				if phase == "cleanup" {
					icon = iconBrush
				}
				ui.Icon(c, icon).Size(14, 14).TextColor(p.Secondary)
				ui.Text(c, title).FontSize(sizeSub).FontWeight(600).TextColor(p.Strong)
			})
			ui.Text(c, lead).FontSize(sizeFoot).TextColor(p.Secondary)
		})
		segmented(c, p, phase+"-shell", []option{{"bash", "Bash"}, {"powershell", "PowerShell"}}, e.shell, func(s string) { e.shell = s })
		value, placeholder := &e.bash, "cp .env.example .env\npnpm install"
		switch {
		case e.shell == "powershell" && phase == "cleanup":
			value, placeholder = &e.ps, "Remove-Item -Recurse -Force node_modules -ErrorAction SilentlyContinue"
		case e.shell == "powershell":
			value, placeholder = &e.ps, "Copy-Item .env.example .env\npnpm install"
		case phase == "cleanup":
			placeholder = "rm -rf node_modules"
		}
		before := *value
		area(c.Key(phase+"/"+e.shell), p, value, placeholder, title, 6).Font(fontMono)
		if *value != before {
			e.hasEdited = true
		}
		if setsEphemeralEnv(e.shell, *value) {
			ui.Text(c, locales.WorktreeEnvHint()).FontSize(sizeCaption).LineHeight(1.45).TextColor(p.Tertiary)
		}
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if e.err != "" {
				ui.Text(c, e.err).FontSize(sizeFoot).TextColor(p.Danger).Grow(1).Selectable()
			} else {
				ui.Spacer(c)
			}
			if e.saving {
				ui.Spinner(c).Size(12, 12)
				ui.Text(c, locales.WorktreeSaving()).FontSize(sizeFoot).TextColor(p.Secondary)
			}
			dirty := e.bash != cfg.Scripts.Bash || e.ps != cfg.Scripts.PowerShell
			primaryButton(c, p, locales.Save(), dirty && !e.saving).OnClick(func() { a.saveGitHubScripts(repo.RepoFullName, phase, e, cfg.TimeoutMs) })
		})
	})
}

func (a *App) saveGitHubScripts(repo, phase string, e *githubScripts, timeoutMs float64) {
	if a.convex == nil {
		return
	}
	convex, ws := a.convex, a.workspaceID
	config := scriptsConfig(e.bash, e.ps, timeoutMs)
	fn := "github:setRepoWorktreeSetup"
	if phase == "cleanup" {
		fn = "github:setRepoWorktreeCleanup"
	}
	e.saving, e.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := convex.Mutation(ctx, fn, map[string]any{"workspaceId": ws, "repoFullName": repo, "config": config}, nil)
		a.update(func() {
			e.saving = false
			if err != nil {
				e.err = err.Error()
				return
			}
			e.hasEdited = false
			a.refetchKey("github")
		})
	}()
}

// refreshGitHubProfile asks GitHub for the user's profile once, when the
// authorization holds none, as Lody does.
func (a *App) refreshGitHubProfile(personal *githubPersonal) {
	if personal == nil || personal.Authorization.State != "authorized" || a.convex == nil || a.offline {
		return
	}
	if pr := personal.Authorization.Profile; pr != nil && pr.Login != "" {
		return
	}
	if a.ghProfileAsked == nil {
		a.ghProfileAsked = map[string]bool{}
	}
	if a.ghProfileAsked[a.workspaceID] {
		return
	}
	a.ghProfileAsked[a.workspaceID] = true
	convex, ws := a.convex, a.workspaceID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if convex.Action(ctx, "github:refreshPersonalGitHubProfile", map[string]any{"workspaceId": ws}, nil) == nil {
			a.update(func() { a.refetchKey("github-personal") })
		}
	}()
}
