package app

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/locales"
)

// The settings that live in Lody Cloud rather than in the workspace's
// streams: its members, the machines it can see, AI usage, billing, its
// GitHub repositories and published shares, read from Convex and the
// backend as Lody's apps read them.

// fetched is what a settings page asked the cloud for.
type fetched struct {
	loading bool
	err     string
	value   any
	load    func(ctx context.Context) (any, error)
}

// fetch returns what key loads, loading it the first time the open
// settings ask for it.
func (a *App) fetch(key string, load func(ctx context.Context) (any, error)) *fetched {
	if a.fetches == nil {
		a.fetches = map[string]*fetched{}
	}
	key = a.workspaceID + "|" + key
	if f := a.fetches[key]; f != nil {
		return f
	}
	f := &fetched{loading: true, load: load}
	a.fetches[key] = f
	if !a.offline {
		a.reload(f, nil)
	}
	return f
}

// reload loads f again, keeping what it shows until the new value comes.
func (a *App) reload(f *fetched, done func()) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		v, err := f.load(ctx)
		a.update(func() {
			f.loading = false
			if err != nil {
				f.err = err.Error()
			} else {
				f.value, f.err = v, ""
			}
			if done != nil {
				done()
			}
		})
	}()
}

// cloudCall runs a Convex mutation or action from a settings page,
// reporting a failure on the page, then reloads what it changed; toggle
// names the switch it flipped, shown flipped until then.
func (a *App) cloudCall(kind, path string, args map[string]any, out any, reload, toggle string, then func()) {
	convex, ws := a.convex, a.workspaceID
	a.settingsError = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var err error
		if kind == "action" {
			err = convex.Action(ctx, path, args, out)
		} else {
			err = convex.Mutation(ctx, path, args, out)
		}
		a.update(func() {
			settle := func() { delete(a.toggles, toggle) }
			if err != nil {
				a.settingsError = err.Error()
				settle()
				return
			}
			if f := a.fetches[ws+"|"+reload]; reload != "" && f != nil {
				a.reload(f, settle)
			} else {
				settle()
			}
			if then != nil {
				then()
			}
		})
	}()
}

// toggle is a switch of a cloud setting, shown as flipped while its
// change is on its way; it reports the value chosen when it flips.
func (a *App) toggle(c *ui.Context, key string, value bool, label string, disabled bool) (bool, bool) {
	if a.toggles == nil {
		a.toggles = map[string]bool{}
	}
	on := value
	if v, ok := a.toggles[key]; ok {
		on = v
	}
	was := on
	lodySwitch(c, &on, disabled).Label(label).Disabled(disabled)
	if on == was {
		return on, false
	}
	a.toggles[key] = on
	return on, true
}

// pending shows what a page waits for, or why it failed; it reports
// whether the page has its data.
func pending(c *ui.Context, p *palette, f *fetched) bool {
	switch {
	case f.loading:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(8, 0).Children(func() {
			spinner(c).Size(13, 13)
			ui.Text(c, locales.Loading()).FontSize(sizeFoot).TextColor(p.Secondary)
		})
		return false
	case f.err != "":
		ui.Text(c, f.err).FontSize(sizeFoot).TextColor(p.Danger).Selectable()
		return false
	}
	return true
}

func (a *App) settingsErrorLine(c *ui.Context, p *palette) {
	if a.settingsError != "" {
		ui.Text(c, a.settingsError).FontSize(sizeFoot).TextColor(p.Danger).Selectable()
	}
	if a.settingsNotice != "" {
		ui.Text(c, a.settingsNotice).FontSize(sizeFoot).TextColor(p.Secondary)
	}
}

// members is the workspace's members, by user id.
func (a *App) members() (map[string]cloud.Member, *fetched) {
	api, token, ws := a.api, a.tokenNow(), a.workspaceID
	f := a.fetch("members", func(ctx context.Context) (any, error) { return api.Members(ctx, token, ws) })
	byUser := map[string]cloud.Member{}
	if list, ok := f.value.([]cloud.Member); ok {
		for _, m := range list {
			byUser[m.UserID] = m
		}
	}
	return byUser, f
}

// memberName is a member's name, "You" for the signed-in user.
func (a *App) memberName(id string, byUser map[string]cloud.Member) string {
	if a.account != nil && id == a.account.User.ID {
		return locales.SettingsYou()
	}
	if m, ok := byUser[id]; ok {
		return firstNonEmpty(m.User.Name, m.User.Email, id)
	}
	return a.userName(id)
}

// userName is someone's name who is not among the members, as one who
// left, read once (auth:getUserById).
func (a *App) userName(id string) string {
	if name, ok := a.userNames[id]; ok {
		return firstNonEmpty(name, id)
	}
	if a.userNames == nil {
		a.userNames = map[string]string{}
	}
	a.userNames[id] = ""
	if a.convex == nil || a.offline || id == "" {
		return id
	}
	convex, ws := a.convex, a.workspaceID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var user *struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		}
		if convex.Query(ctx, "auth:getUserById", map[string]any{"workspaceId": ws, "userId": id}, &user) == nil && user != nil {
			a.update(func() { a.userNames[id] = firstNonEmpty(user.Name, user.Email) })
		}
	}()
	return id
}

func roleLabel(role string) string {
	switch role {
	case "owner":
		return locales.RoleOwner()
	case "admin":
		return locales.RoleAdmin()
	}
	return locales.RoleMember()
}

type visibleMachine struct {
	MachineID      string  `json:"machineId"`
	OwnerUserID    string  `json:"ownerUserId"`
	SharedWithTeam bool    `json:"sharedWithTeam"`
	UpdatedAt      float64 `json:"updatedAt"`
}

func trimZero(s string) string { return strings.TrimSuffix(s, ".0") }

func formatUSD(x float64) string { return fmt.Sprintf("$%.2f", x) }

type workspaceRepo struct {
	RepoFullName    string           `json:"repoFullName"`
	Name            string           `json:"name"`
	Private         bool             `json:"private"`
	Enabled         bool             `json:"enabled"`
	WorktreeSetup   *worktreeScripts `json:"worktreeSetup"`
	WorktreeCleanup *worktreeScripts `json:"worktreeCleanup"`
}

// githubPage is the workspace's GitHub: its app and the repositories it
// reaches, and whether Lody acts as the user. Installing the app and
// authorizing the user happen in the browser, as Lody's desktop app does.
func (a *App) githubPage(c *ui.Context, p *palette) {
	settingsTitle(c, p, "GitHub")
	convex, ws := a.convex, a.workspaceID
	f := a.fetch("github", func(ctx context.Context) (any, error) {
		var out *[]workspaceRepo
		err := convex.Query(ctx, "github:listWorkspaceReposWithStatus", map[string]any{"workspaceId": ws}, &out)
		return out, err
	})
	pf := a.fetch("github-personal", func(ctx context.Context) (any, error) {
		var out *githubPersonal
		err := convex.Query(ctx, "github:getPersonalOperationSettings", map[string]any{"workspaceId": ws}, &out)
		return out, err
	})
	a.settingsErrorLine(c, p)
	var repos []workspaceRepo
	if r, _ := f.value.(*[]workspaceRepo); r != nil {
		repos = *r
	}
	enabled := 0
	for _, r := range repos {
		if r.Enabled {
			enabled++
		}
	}
	status := locales.GitHubNotInstalled()
	if len(repos) > 0 {
		status = locales.GitHubEnabledOf(enabled, len(repos))
	}
	install := func() {
		slug := ""
		if w := a.workspace(); w != nil {
			slug = w.Slug
		}
		var out struct {
			State string `json:"state"`
		}
		a.cloudCall("action", "github:createGitHubInstallState", map[string]any{"workspaceId": ws, "workspaceSlug": slug, "returnTarget": "web"}, &out, "", "", func() {
			if out.State != "" {
				mygo.Shell.OpenExternal("https://github.com/apps/lodyai/installations/new?state=" + url.QueryEscape(out.State))
			}
		})
	}
	settingRow(c, p, "GitHub App", status, func() {
		outlineButton(c, p, iconExternal, locales.SettingsManage()).OnClick(install)
	})
	personal, _ := pf.value.(*githubPersonal)
	a.refreshGitHubProfile(personal)
	desc := locales.GitHubPersonalHelp()
	if personal != nil && personal.Authorization.State == "authorized" && personal.Authorization.Profile != nil && personal.Authorization.Profile.Login != "" {
		desc += " · @" + personal.Authorization.Profile.Login
	}
	settingRow(c, p, locales.GitHubPersonal(), desc, func() {
		on := personal != nil && personal.Enabled
		key := "github-personal"
		if v, flipped := a.toggle(c, key, on, locales.GitHubPersonal(), personal == nil); flipped {
			if v && personal.Authorization.State != "authorized" {
				// Authorizing needs GitHub's page, on lody.ai.
				delete(a.toggles, key)
				a.webSettings("github")
				return
			}
			a.cloudCall("mutation", "github:setPersonalOperationPreference", map[string]any{"workspaceId": ws, "enabled": v}, nil, "github-personal", key, nil)
		}
	})
	if !pending(c, p, f) {
		return
	}
	if len(repos) == 0 {
		ui.Row(c).FillWidth().Padding(10, 10, 10, 16).Radius(10).Border(1, p.Border).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.GitHubNone()).FontSize(sizeFoot).TextColor(p.Secondary).Grow(1)
			outlineButton(c, p, nil, locales.GitHubInstall()).OnClick(install)
		})
		return
	}
	card(c, p, func() {
		for _, r := range repos {
			repo := r
			ui.Row(c.Key(r.RepoFullName)).FillWidth().MinHeight(44).Padding(6, 14).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconBrandGitHub).Size(15, 15).TextColor(p.Secondary)
				ui.Text(c, repo.RepoFullName).FontSize(sizeSub).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
				if repo.Private {
					pill(c, p, iconLock, locales.GitHubPrivate())
				}
				key := "repo/" + repo.RepoFullName
				if on, flipped := a.toggle(c, key, repo.Enabled, repo.RepoFullName, false); flipped {
					a.cloudCall("mutation", "github:setRepoEnabled",
						map[string]any{"workspaceId": ws, "repoFullName": repo.RepoFullName, "enabled": on}, nil, "github", key, nil)
				}
			})
		}
	})
	ui.Row(c).Gap(4).Children(func() {
		webLink(c, p, locales.GitHubMissing(), func() { mygo.Shell.OpenExternal("https://github.com/settings/installations") })
	})
}

type githubPersonal struct {
	Enabled       bool `json:"enabled"`
	Authorization struct {
		State   string `json:"state"`
		Profile *struct {
			Login string `json:"login"`
		} `json:"profile"`
	} `json:"authorization"`
}

type publishedShare struct {
	ShareID           string  `json:"shareId"`
	RootSessionID     string  `json:"rootSessionId"`
	PublisherUserID   string  `json:"publisherUserId"`
	Status            string  `json:"status"`
	Revision          float64 `json:"revision"`
	CredentialVersion float64 `json:"credentialVersion"`
	CanManage         bool    `json:"canManage"`
	Title             string  `json:"title"`
	CreatedAt         float64 `json:"createdAt"`
	TotalBytes        float64 `json:"totalBytes"`
	ConversationCount float64 `json:"conversationCount"`
	CanRevoke         bool    `json:"canRevoke"`
}

// sharesPerPage is how many shares sessionSharing:list returns at a time,
// as Lody's apps ask.
const sharesPerPage = 20

// sharesPage is a page of the published shares, and where the next one
// starts.
type sharesPage struct {
	Page           []publishedShare `json:"page"`
	IsDone         bool             `json:"isDone"`
	ContinueCursor string           `json:"continueCursor"`
}

// sharePaging is how far the published shares were paged: the cursors of
// the pages before the one shown, as Lody's list keeps them. No page
// outlives a switch of workspace.
type sharePaging struct {
	workspace string
	cursors   []string
}

// cursor is where the page shown starts: "" for the first.
func (s *sharePaging) cursor() string {
	if len(s.cursors) == 0 {
		return ""
	}
	return s.cursors[len(s.cursors)-1]
}

// sharesKey is what the page of the shares from cursor is fetched as.
func sharesKey(cursor string) string {
	if cursor == "" {
		return "shares"
	}
	return "shares/" + cursor
}

// sharesFetch is the page of the shares from cursor.
func (a *App) sharesFetch(cursor string) *fetched {
	convex, ws := a.convex, a.workspaceID
	return a.fetch(sharesKey(cursor), func(ctx context.Context) (any, error) {
		var at any
		if cursor != "" {
			at = cursor
		}
		var out sharesPage
		err := convex.Query(ctx, "sessionSharing:list", map[string]any{"workspaceId": ws, "paginationOpts": map[string]any{"numItems": sharesPerPage, "cursor": at}}, &out)
		return &out, err
	})
}

// refetchShares loads the shares' pages again after one of them changed.
func (a *App) refetchShares() {
	prefix := a.workspaceID + "|shares"
	for key, f := range a.fetches {
		if (key == prefix || strings.HasPrefix(key, prefix+"/")) && f.load != nil {
			a.reload(f, nil)
		}
	}
}

// pageShares moves to the page before or after the one shown, which
// loads again if it was shown before.
func (a *App) pageShares(next string) {
	sp := &a.sharePages
	if next == "" {
		sp.cursors = sp.cursors[:max(0, len(sp.cursors)-1)]
	} else {
		sp.cursors = append(sp.cursors, next)
	}
	a.revoking, a.resetting = "", ""
	if f := a.fetches[a.workspaceID+"|"+sharesKey(sp.cursor())]; f != nil && f.load != nil && !a.offline {
		a.reload(f, nil)
	}
}

// sharesPage lists the static copies of sessions published from the
// workspace, 20 at a time, and opens, copies, resets and revokes them.
// As Lody's, Previous and Next show only once there is a page to go to.
func (a *App) sharesPage(c *ui.Context, p *palette) {
	settingsTitle(c, p, locales.SettingsShares())
	ui.Text(c, locales.SharesHelp()).FontSize(sizeFoot).TextColor(p.Secondary)
	sp := &a.sharePages
	if sp.workspace != a.workspaceID {
		*sp = sharePaging{workspace: a.workspaceID}
	}
	f := a.sharesFetch(sp.cursor())
	a.settingsErrorLine(c, p)
	if !pending(c, p, f) {
		return
	}
	page, _ := f.value.(*sharesPage)
	if page == nil {
		page = &sharesPage{IsDone: true}
	}
	defer a.sharesPager(c, p, page)
	shares := page.Page
	if len(shares) == 0 {
		ui.Text(c, locales.SharesNone()).FontSize(sizeFoot).TextColor(p.Tertiary)
		return
	}
	byUser, _ := a.members()
	for _, s := range shares {
		share := s
		facts := []string{
			map[string]string{"active": locales.SharesActive(), "revoked": locales.SharesRevoked()}[s.Status],
			a.memberName(s.PublisherUserID, byUser), formatDay(s.CreatedAt),
			locales.SharesConversations(int(s.ConversationCount)), formatBytes(s.TotalBytes),
		}
		settingRow(c.Key(s.ShareID), p, firstNonEmpty(s.Title, locales.UntitledSession()), strings.Join(facts, " · "), func() {
			if share.Status != "active" {
				return
			}
			entry := &shareEntry{ShareID: share.ShareID, RootSessionID: share.RootSessionID, PublisherUserID: share.PublisherUserID, Status: share.Status,
				Revision: share.Revision, CredentialVersion: share.CredentialVersion, CanManage: share.CanManage, CanRevoke: share.CanRevoke}
			mine := a.account != nil && share.PublisherUserID == a.account.User.ID
			if a.revoking != share.ShareID && a.resetting != share.ShareID {
				switch link := a.shareLink(entry); {
				case link != "":
					// The published page, in the browser, as Lody's
					// management opens it; then its link.
					ui.Row(c).Gap(6).Margin(0, 6, 0, 0).Children(func() {
						iconButton(c, p, iconExternal, locales.SharesOpenPublished(), p.Text.Alpha(0.06)).Border(1, p.Border).
							OnClick(func() { openExternal(link) })
						ghostButton(c, p, locales.ShareCopy()).Border(1, p.Border).OnClick(func() {
							writeClipboard(link)
							a.toast("success", locales.ShareCopied())
						})
					})
				case mine && share.CanManage:
					tip(c, p, ghostButton(c, p, locales.ShareReset()).Border(1, p.Border), locales.SharesLinkOtherDevice()).OnClick(func() { a.resetting = share.ShareID })
				}
			}
			if a.resetting == share.ShareID {
				ui.Row(c).Gap(6).Children(func() {
					ghostButton(c, p, locales.Cancel()).OnClick(func() { a.resetting = "" })
					tip(c, p, ghostButton(c, p, locales.ShareReset()).Border(1, p.Danger), locales.ShareInvalidateNotice()).OnClick(func() {
						a.resetting = ""
						a.resetShareLink(&shareJob{root: share.RootSessionID, entry: entry, loaded: true})
					})
				})
				return
			}
			if !share.CanRevoke {
				return
			}
			if a.revoking != share.ShareID {
				ghostButton(c, p, locales.SharesRevoke()).Border(1, p.Border).OnClick(func() { a.revoking = share.ShareID })
				return
			}
			ui.Row(c).Gap(6).Children(func() {
				ghostButton(c, p, locales.Cancel()).OnClick(func() { a.revoking = "" })
				b := ghostButton(c, p, locales.SharesConfirmRevoke()).Border(1, p.Danger)
				b.OnClick(func() {
					a.revoking = ""
					a.cloudCall("mutation", "sessionSharing:revoke", map[string]any{"shareId": share.ShareID, "expectedRevision": share.Revision}, nil, sharesKey(sp.cursor()), "", nil)
				})
			})
		})
	}
}

// sharesPager is Previous and Next under the shares, at the end: shown
// once there is a page before or after this one, each disabled where
// there is none, as Lody's.
func (a *App) sharesPager(c *ui.Context, p *palette, page *sharesPage) {
	sp := &a.sharePages
	first := len(sp.cursors) == 0
	if first && page.IsDone {
		return
	}
	button := func(label string, disabled bool, move func()) {
		if b := secondaryButton(c, p, label, disabled); !disabled {
			b.OnClick(move)
		}
	}
	ui.Row(c.Key("shares-pager")).FillWidth().Gap(8).Justify(ui.End).Margin(4, 0, 0, 0).Children(func() {
		button(locales.SharesPrevious(), first, func() { a.pageShares("") })
		next := page.ContinueCursor
		button(locales.SharesNext(), page.IsDone || next == "" || next == sp.cursor(), func() { a.pageShares(next) })
	})
}

func formatBytes(n float64) string {
	switch {
	case n >= 1<<30:
		return trimZero(fmt.Sprintf("%.1f", n/(1<<30))) + " GB"
	case n >= 1<<20:
		return trimZero(fmt.Sprintf("%.1f", n/(1<<20))) + " MB"
	case n >= 1<<10:
		return trimZero(fmt.Sprintf("%.1f", n/(1<<10))) + " KB"
	}
	return fmt.Sprintf("%.0f B", n)
}
