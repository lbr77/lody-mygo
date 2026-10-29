package app

import (
	"context"
	"fmt"
	"math"
	"sort"
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
	a.reload(f, nil)
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

// machinesPage lists the machines the workspace can see, and lets their
// owners share them with the team.
func (a *App) machinesPage(c *ui.Context, p *palette) {
	settingsTitle(c, p, locales.SettingsMachines())
	ui.Text(c, locales.SettingsMachinesHelp()).FontSize(sizeFoot).TextColor(p.Secondary)
	convex, ws := a.convex, a.workspaceID
	f := a.fetch("machines", func(ctx context.Context) (any, error) {
		var out []visibleMachine
		err := convex.Query(ctx, "machines:listVisibleMachines", map[string]any{"workspaceId": ws}, &out)
		return out, err
	})
	a.settingsErrorLine(c, p)
	if !pending(c, p, f) {
		return
	}
	byUser, _ := a.members()
	machines, _ := f.value.([]visibleMachine)
	if len(machines) == 0 {
		ui.Text(c, locales.SettingsNoMachine()).FontSize(sizeFoot).TextColor(p.Tertiary)
	}
	for _, m := range machines {
		facts := []string{a.memberName(m.OwnerUserID, byUser)}
		if a.presence.Live {
			facts = append(facts, map[bool]string{true: locales.Online(), false: locales.Offline()}[a.presence.IsOnline(m.MachineID)])
		}
		facts = append(facts, locales.SettingsAgentCount(len(a.catalog.Agents[m.MachineID])))
		mine := a.account != nil && m.OwnerUserID == a.account.User.ID
		machine := m
		settingRow(c.Key(m.MachineID), p, a.machineName(m.MachineID), strings.Join(facts, " · "), func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.SettingsSharedWithTeam()).FontSize(sizeFoot).TextColor(p.Secondary)
				key := "machine/" + machine.MachineID
				if on, flipped := a.toggle(c, key, machine.SharedWithTeam, locales.SettingsSharedWithTeam(), !mine); flipped {
					a.cloudCall("mutation", "machines:setMachineSharedWithTeam",
						map[string]any{"workspaceId": ws, "machineId": machine.MachineID, "sharedWithTeam": on}, nil, "machines", key, nil)
				}
			})
		})
	}
}

// usageTimeline is AI usage over a range, in buckets.
type usageTimeline struct {
	Totals struct {
		Tokens  float64 `json:"tokens"`
		CostUSD float64 `json:"costUSD"`
	} `json:"totals"`
	Users map[string]struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"users"`
	Buckets []struct {
		BucketStartMs float64 `json:"bucketStartMs"`
		BucketLabel   string  `json:"bucketLabel"`
		Tokens        float64 `json:"tokens"`
		CostUSD       float64 `json:"costUSD"`
		ByModel       []struct {
			ModelID string  `json:"modelId"`
			Tokens  float64 `json:"tokens"`
			CostUSD float64 `json:"costUSD"`
		} `json:"byModel"`
		ByUser []struct {
			UserID  string  `json:"userId"`
			Tokens  float64 `json:"tokens"`
			CostUSD float64 `json:"costUSD"`
		} `json:"byUser"`
	} `json:"buckets"`
}

var usageRanges = []string{"day", "week", "month", "total"}

// usagePage shows the workspace's AI usage: totals, a chart over the
// range, and the split by model and by member.
func (a *App) usagePage(c *ui.Context, p *palette) {
	settingsTitle(c, p, locales.SettingsUsage())
	picked := 2
	for i, r := range usageRanges {
		if r == a.usageRange {
			picked = i
		}
	}
	was := picked
	ui.Segmented(c, &picked, locales.UsageDay(), locales.UsageWeek(), locales.UsageMonth(), locales.UsageTotal()).Label(locales.SettingsUsage())
	if picked != was || a.usageRange == "" {
		a.usageRange = usageRanges[picked]
	}
	convex, ws, rng := a.convex, a.workspaceID, a.usageRange
	f := a.fetch("usage/"+rng, func(ctx context.Context) (any, error) {
		var out usageTimeline
		err := convex.Query(ctx, "usage:getWorkspaceUsageTimeline", map[string]any{"workspaceId": ws, "range": rng}, &out)
		return &out, err
	})
	if !pending(c, p, f) {
		return
	}
	t, _ := f.value.(*usageTimeline)
	if t == nil {
		return
	}
	ui.Row(c).FillWidth().Gap(12).Margin(4, 0).Children(func() {
		statCard(c, p, locales.UsageTokens(), formatCount(t.Totals.Tokens))
		statCard(c, p, locales.UsageCost(), formatUSD(t.Totals.CostUSD))
	})
	usageChart(c, p, t)
	type part struct {
		label        string
		tokens, cost float64
	}
	split := func(of func(add func(key string, tokens, cost float64))) []part {
		sums := map[string]*part{}
		of(func(key string, tokens, cost float64) {
			s := sums[key]
			if s == nil {
				s = &part{label: key}
				sums[key] = s
			}
			s.tokens += tokens
			s.cost += cost
		})
		out := make([]part, 0, len(sums))
		for _, s := range sums {
			out = append(out, *s)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].tokens > out[j].tokens })
		return out
	}
	models := split(func(add func(string, float64, float64)) {
		for _, b := range t.Buckets {
			for _, m := range b.ByModel {
				add(m.ModelID, m.Tokens, m.CostUSD)
			}
		}
	})
	people := split(func(add func(string, float64, float64)) {
		for _, b := range t.Buckets {
			for _, u := range b.ByUser {
				add(u.UserID, u.Tokens, u.CostUSD)
			}
		}
	})
	breakdown := func(title string, parts []part, name func(string) string) {
		if len(parts) == 0 {
			return
		}
		divider(c, p)
		settingsHeading(c, p, title)
		for _, s := range parts {
			share := 0.0
			if t.Totals.Tokens > 0 {
				share = s.tokens / t.Totals.Tokens
			}
			ui.Column(c.Key(title+s.label)).FillWidth().Gap(4).Padding(4, 0).Children(func() {
				ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Text(c, name(s.label)).FontSize(sizeSub).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
					ui.Text(c, formatCount(s.tokens)).Font(fontMono).FontSize(sizeFoot).TextColor(p.Secondary)
					ui.Text(c, formatUSD(s.cost)).Font(fontMono).FontSize(sizeFoot).TextColor(p.Secondary).Width(72).TextAlign(ui.End)
				})
				ui.Box(c).FillWidth().Height(4).Radius(2).Background(p.Text.Alpha(0.06)).Children(func() {
					ui.Box(c).WidthPercent(float32(math.Max(share*100, 0.5))).Height(4).Radius(2).Background(p.Primary)
				})
			})
		}
	}
	breakdown(locales.UsageByModel(), models, func(s string) string { return s })
	breakdown(locales.UsageByMember(), people, func(id string) string {
		if a.account != nil && id == a.account.User.ID {
			return locales.SettingsYou()
		}
		if u, ok := t.Users[id]; ok {
			return firstNonEmpty(u.Name, u.Email, id)
		}
		return id
	})
}

func statCard(c *ui.Context, p *palette, label, value string) {
	ui.Column(c).Grow(1).Basis(0).Padding(14, 16).Radius(12).Border(1, p.Border).Gap(4).Children(func() {
		ui.Text(c, label).FontSize(sizeFoot).TextColor(p.Secondary)
		ui.Text(c, value).FontSize(22).FontWeight(600).TextColor(p.Strong)
	})
}

// usageChart draws the tokens of each bucket as a bar.
func usageChart(c *ui.Context, p *palette, t *usageTimeline) {
	if len(t.Buckets) == 0 {
		return
	}
	peak := 0.0
	for _, b := range t.Buckets {
		peak = math.Max(peak, b.Tokens)
	}
	ui.Column(c).FillWidth().Gap(6).Margin(8, 0, 0, 0).Children(func() {
		ui.Row(c).FillWidth().Height(120).Gap(2).AlignItems(ui.End).Children(func() {
			for i, b := range t.Buckets {
				h := float32(0)
				if peak > 0 {
					h = float32(b.Tokens / peak * 120)
				}
				ui.Box(c.Key(i)).Grow(1).Basis(0).MinWidth(1).Height(max(h, 1)).Radius(2).
					Background(p.Primary.Alpha(map[bool]float32{true: 0.85, false: 0.18}[b.Tokens > 0])).
					Tooltip(b.BucketLabel + " · " + formatCount(b.Tokens) + " · " + formatUSD(b.CostUSD))
			}
		})
		ui.Row(c).FillWidth().Children(func() {
			ui.Text(c, t.Buckets[0].BucketLabel).FontSize(sizeCaption).TextColor(p.Tertiary)
			ui.Spacer(c)
			ui.Text(c, t.Buckets[len(t.Buckets)-1].BucketLabel).FontSize(sizeCaption).TextColor(p.Tertiary)
		})
	})
}

// formatCount writes a count as Lody does: 950, 12.3K, 4.5M, 1.2B.
func formatCount(n float64) string {
	switch {
	case n >= 1e9:
		return trimZero(fmt.Sprintf("%.1f", n/1e9)) + "B"
	case n >= 1e6:
		return trimZero(fmt.Sprintf("%.1f", n/1e6)) + "M"
	case n >= 1e3:
		return trimZero(fmt.Sprintf("%.1f", n/1e3)) + "K"
	}
	return fmt.Sprintf("%.0f", n)
}

func trimZero(s string) string { return strings.TrimSuffix(s, ".0") }

func formatUSD(x float64) string { return fmt.Sprintf("$%.2f", x) }

// formatMoney writes an amount in a currency's minor units.
func formatMoney(minor float64, currency string) string {
	cur := strings.ToUpper(currency)
	switch cur {
	case "USD", "":
		return fmt.Sprintf("$%.2f", minor/100)
	case "JPY", "KRW":
		return fmt.Sprintf("%.0f %s", minor, cur)
	}
	return fmt.Sprintf("%.2f %s", minor/100, cur)
}

func formatDay(ms float64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(int64(ms)).Format("2006-01-02")
}

type billingOverview struct {
	BillingAccountID  *string  `json:"billingAccountId"`
	PlanTier          string   `json:"planTier"`
	EffectivePlanTier string   `json:"effectivePlanTier"`
	SubscriptionState *string  `json:"subscriptionStatus"`
	BillingInterval   *string  `json:"billingInterval"`
	CancelAtPeriodEnd bool     `json:"cancelAtPeriodEnd"`
	CurrentPeriodEnd  *float64 `json:"currentPeriodEnd"`
	NextBillingAt     *float64 `json:"nextBillingAt"`
	GiftEndsAt        *float64 `json:"giftEndsAt"`
	PromotionEndsAt   *float64 `json:"promotionalEntitlementEndsAt"`
	SeatCount         int      `json:"seatCount"`
	CanManageBilling  bool     `json:"canManageBilling"`
}

type billingInvoices struct {
	Invoices []struct {
		ID         string   `json:"id"`
		Number     *string  `json:"number"`
		Status     string   `json:"status"`
		AmountPaid float64  `json:"amountPaid"`
		Total      *float64 `json:"total"`
		Currency   string   `json:"currency"`
		CreatedAt  float64  `json:"createdAt"`
		HostedURL  *string  `json:"hostedInvoiceUrl"`
	} `json:"invoices"`
}

func planLabel(tier string) string {
	switch tier {
	case "plus":
		return "Plus"
	case "enterprise":
		return "Enterprise"
	}
	return locales.PlanFree()
}

// billingPage shows the workspace's plan and invoices, and opens Stripe's
// portal to manage them.
func (a *App) billingPage(c *ui.Context, p *palette) {
	settingsTitle(c, p, locales.SettingsBilling())
	convex, ws := a.convex, a.workspaceID
	f := a.fetch("billing", func(ctx context.Context) (any, error) {
		var out *billingOverview
		err := convex.Query(ctx, "billing:getBillingOverview", map[string]any{"workspaceId": ws}, &out)
		return out, err
	})
	a.settingsErrorLine(c, p)
	if !pending(c, p, f) {
		return
	}
	b, _ := f.value.(*billingOverview)
	if b == nil {
		ui.Text(c, locales.BillingUnavailable()).FontSize(sizeFoot).TextColor(p.Secondary)
		return
	}
	settingRow(c, p, locales.BillingPlan(), "", func() {
		ui.Text(c, planLabel(firstNonEmpty(b.EffectivePlanTier, b.PlanTier))).FontSize(sizeBody).FontWeight(600).TextColor(p.Text)
	})
	if b.BillingInterval != nil {
		settingRow(c, p, locales.BillingInterval(), "", func() {
			valueText(c, p, map[string]string{"month": locales.BillingMonthly(), "year": locales.BillingYearly()}[*b.BillingInterval])
		})
	}
	if b.SeatCount > 0 {
		settingRow(c, p, locales.BillingSeats(), "", func() { valueText(c, p, fmt.Sprint(b.SeatCount)) })
	}
	switch {
	case b.CancelAtPeriodEnd && b.CurrentPeriodEnd != nil:
		settingRow(c, p, locales.BillingEnds(), "", func() { valueText(c, p, formatDay(*b.CurrentPeriodEnd)) })
	case b.NextBillingAt != nil:
		settingRow(c, p, locales.BillingNext(), "", func() { valueText(c, p, formatDay(*b.NextBillingAt)) })
	case b.GiftEndsAt != nil:
		settingRow(c, p, locales.BillingEnds(), "", func() { valueText(c, p, formatDay(*b.GiftEndsAt)) })
	case b.PromotionEndsAt != nil:
		settingRow(c, p, locales.BillingEnds(), "", func() { valueText(c, p, formatDay(*b.PromotionEndsAt)) })
	}
	if b.CanManageBilling && b.BillingAccountID != nil {
		settingRow(c, p, locales.BillingManage(), locales.BillingManageHelp(), func() {
			ghostButton(c, p, locales.Open()).Border(1, p.Border).OnClick(func() {
				var out struct {
					URL string `json:"url"`
				}
				a.cloudCall("action", "billing:createBillingPortalSession", map[string]any{"workspaceId": ws, "returnTarget": "desktop"}, &out, "", "", func() {
					if out.URL != "" {
						mygo.Shell.OpenExternal(out.URL)
					}
				})
			})
		})
	}
	if b.BillingAccountID == nil {
		return
	}
	divider(c, p)
	settingsHeading(c, p, locales.BillingInvoices())
	inv := a.fetch("invoices", func(ctx context.Context) (any, error) {
		var out billingInvoices
		err := convex.Action(ctx, "billing:listBillingInvoices", map[string]any{"workspaceId": ws}, &out)
		return &out, err
	})
	if !pending(c, p, inv) {
		return
	}
	list, _ := inv.value.(*billingInvoices)
	if list == nil || len(list.Invoices) == 0 {
		ui.Text(c, locales.BillingNoInvoices()).FontSize(sizeFoot).TextColor(p.Tertiary)
		return
	}
	for _, in := range list.Invoices {
		amount := in.AmountPaid
		if in.Total != nil {
			amount = *in.Total
		}
		label := formatDay(in.CreatedAt)
		if in.Number != nil {
			label += " · " + *in.Number
		}
		link := ""
		if in.HostedURL != nil {
			link = *in.HostedURL
		}
		settingRow(c.Key(in.ID), p, label, in.Status, func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, formatMoney(amount, in.Currency)).Font(fontMono).FontSize(sizeFoot).TextColor(p.Text)
				if link != "" {
					iconButton(c, p, iconExternal, locales.Open(), p.Hover).OnClick(func() { mygo.Shell.OpenExternal(link) })
				}
			})
		})
	}
}

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
	ConversationCount int     `json:"conversationCount"`
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
			locales.SharesConversations(s.ConversationCount), formatBytes(s.TotalBytes),
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
