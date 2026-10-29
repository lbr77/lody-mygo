package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

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
	ui.Switch(c, &on).Label(label).Disabled(disabled)
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
			ui.Spinner(c).Size(13, 13)
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

// membersSection lists the workspace's members on its General page.
func (a *App) membersSection(c *ui.Context, p *palette) {
	settingsHeading(c, p, locales.SettingsMembers())
	_, f := a.members()
	if !pending(c, p, f) {
		return
	}
	list, _ := f.value.([]cloud.Member)
	sorted := append([]cloud.Member(nil), list...)
	rank := map[string]int{"owner": 0, "admin": 1}
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, ok := rank[sorted[i].Role]
		if !ok {
			ri = 2
		}
		rj, ok := rank[sorted[j].Role]
		if !ok {
			rj = 2
		}
		return ri < rj
	})
	for _, m := range sorted {
		name := firstNonEmpty(m.User.Name, m.User.Email)
		if a.account != nil && m.UserID == a.account.User.ID {
			name += " (" + locales.SettingsYou() + ")"
		}
		ui.Row(c.Key(m.ID)).FillWidth().MinHeight(44).Gap(12).AlignItems(ui.Center).Children(func() {
			avatar(c, p, firstNonEmpty(m.User.Name, m.User.Email), 28, 14)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, name).FontSize(sizeBody).TextColor(p.Text).SingleLine()
				if m.User.Email != "" {
					ui.Text(c, m.User.Email).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
				}
			})
			ui.Text(c, roleLabel(m.Role)).FontSize(sizeFoot).TextColor(p.Secondary)
		})
	}
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
	RepoFullName string `json:"repoFullName"`
	Private      bool   `json:"private"`
	Enabled      bool   `json:"enabled"`
}

// githubPage lists the repositories the workspace's GitHub app reaches,
// and which of them Lody works in.
func (a *App) githubPage(c *ui.Context, p *palette) {
	settingsTitle(c, p, "GitHub")
	ui.Text(c, locales.GitHubHelp()).FontSize(sizeFoot).TextColor(p.Secondary)
	convex, ws := a.convex, a.workspaceID
	f := a.fetch("github", func(ctx context.Context) (any, error) {
		var out *[]workspaceRepo
		err := convex.Query(ctx, "github:listWorkspaceReposWithStatus", map[string]any{"workspaceId": ws}, &out)
		return out, err
	})
	a.settingsErrorLine(c, p)
	if !pending(c, p, f) {
		return
	}
	repos, _ := f.value.(*[]workspaceRepo)
	if repos == nil || len(*repos) == 0 {
		ui.Text(c, locales.GitHubNone()).FontSize(sizeFoot).TextColor(p.Tertiary)
		return
	}
	for _, r := range *repos {
		repo := r
		desc := ""
		if r.Private {
			desc = locales.GitHubPrivate()
		}
		settingRow(c.Key(r.RepoFullName), p, r.RepoFullName, desc, func() {
			key := "repo/" + repo.RepoFullName
			if on, flipped := a.toggle(c, key, repo.Enabled, repo.RepoFullName, false); flipped {
				a.cloudCall("mutation", "github:setRepoEnabled",
					map[string]any{"workspaceId": ws, "repoFullName": repo.RepoFullName, "enabled": on}, nil, "github", key, nil)
			}
		})
	}
}

type publishedShare struct {
	ShareID           string  `json:"shareId"`
	PublisherUserID   string  `json:"publisherUserId"`
	Status            string  `json:"status"`
	Revision          float64 `json:"revision"`
	Title             string  `json:"title"`
	CreatedAt         float64 `json:"createdAt"`
	TotalBytes        float64 `json:"totalBytes"`
	ConversationCount float64 `json:"conversationCount"`
	CanRevoke         bool    `json:"canRevoke"`
}

// sharesPage lists the static copies of sessions published from the
// workspace, and revokes them.
func (a *App) sharesPage(c *ui.Context, p *palette) {
	settingsTitle(c, p, locales.SettingsShares())
	ui.Text(c, locales.SharesHelp()).FontSize(sizeFoot).TextColor(p.Secondary)
	convex, ws := a.convex, a.workspaceID
	f := a.fetch("shares", func(ctx context.Context) (any, error) {
		var out struct {
			Page []publishedShare `json:"page"`
		}
		err := convex.Query(ctx, "sessionSharing:list", map[string]any{"workspaceId": ws, "paginationOpts": map[string]any{"numItems": 50, "cursor": nil}}, &out)
		return out.Page, err
	})
	a.settingsErrorLine(c, p)
	if !pending(c, p, f) {
		return
	}
	shares, _ := f.value.([]publishedShare)
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
			if !share.CanRevoke || share.Status != "active" {
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
					a.cloudCall("mutation", "sessionSharing:revoke", map[string]any{"shareId": share.ShareID, "expectedRevision": share.Revision}, nil, "shares", "", nil)
				})
			})
		})
	}
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
