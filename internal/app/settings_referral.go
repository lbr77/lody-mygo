package app

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// Referrals, as Lody's billing page shows them: one card under the plan
// with the link to share and the credit it has earned, and behind
// Details, the breakdown, the history and the rules. Money is integer USD
// cents.

type referralPolicy struct {
	CampaignID      string   `json:"campaignId"`
	DiscountPercent float64  `json:"discountPercent"`
	RewardPercent   float64  `json:"rewardPercent"`
	StartsAt        *float64 `json:"startsAt"`
	EndsAt          *float64 `json:"endsAt"`
	NextChangeAt    *float64 `json:"nextChangeAt"`
}

type referralOverview struct {
	Policy     *referralPolicy `json:"policy"`
	Eligible   bool            `json:"eligible"`
	Code       *string         `json:"code"`
	URL        *string         `json:"url"`
	Issued     float64         `json:"issued"`
	Spent      float64         `json:"spent"`
	Available  float64         `json:"available"`
	Frozen     float64         `json:"frozen"`
	Adjustment float64         `json:"adjustment"`
}

type referralEntry struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"` // reward, adjustment, freeze, use, release
	Amount    float64 `json:"amount"`
	Frozen    float64 `json:"frozen"`
	CreatedAt float64 `json:"createdAt"`
}

type referralLedger struct {
	Page           []referralEntry `json:"page"`
	IsDone         bool            `json:"isDone"`
	ContinueCursor string          `json:"continueCursor"`
}

// referrals is the card's state.
type referrals struct {
	linkPending bool
	copiedAt    time.Time
	details     bool
	cursors     []string // the history's pages read, "" the first
	policyRun   float64  // the policy change last refetched for
}

const referralPageSize = 10

// policyValues are what the copy says of a policy: the discount, the
// reward, and the discount as a Chinese 折.
func policyValues(pol *referralPolicy) (discount, reward, rate string) {
	d, r := 10.0, 20.0
	if pol != nil {
		d, r = pol.DiscountPercent, pol.RewardPercent
	}
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	return f(d), f(r), f((100 - d) / 10)
}

// describeEntry is what a ledger entry says, its amount and its tone.
func describeEntry(e referralEntry) (label string, amount float64, tone string) {
	switch e.Kind {
	case "reward":
		return locales.ReferralEntryReward(), e.Amount, "gain"
	case "adjustment":
		return locales.ReferralEntryReversal(), e.Amount, "loss"
	case "use":
		return locales.ReferralEntryUse(), e.Amount, "loss"
	case "release":
		return locales.ReferralEntryRelease(), e.Amount, "gain"
	case "freeze":
		if e.Frozen >= 0 {
			return locales.ReferralEntryHold(), e.Frozen, "hold"
		}
		return locales.ReferralEntryUnhold(), -e.Frozen, "hold"
	}
	return e.Kind, e.Amount, "hold"
}

func signedUsd(amount float64, tone string) string {
	if amount < 0 {
		amount = -amount
		if tone != "hold" {
			return "−" + formatMoney(amount, "usd")
		}
	} else if tone != "hold" {
		return "+" + formatMoney(amount, "usd")
	}
	return formatMoney(amount, "usd")
}

// campaignDay is a campaign's date as Lody writes it: in China's time,
// 10/29/2026 or 2026/10/29.
func campaignDay(ms float64) string {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	t := time.UnixMilli(int64(ms)).In(loc)
	if strings.HasPrefix(i18n.Locale(), "zh") {
		return t.Format("2006/01/02")
	}
	return t.Format("01/02/2006")
}

func (a *App) referralOverview(c *ui.Context) (*referralOverview, *fetched) {
	convex, ws := a.convex, a.workspaceID
	f := a.fetch("referrals", func(ctx context.Context) (any, error) {
		var out *referralOverview
		err := convex.Query(ctx, "referrals:getOverview", map[string]any{"workspaceId": ws}, &out)
		return out, err
	})
	r, _ := f.value.(*referralOverview)
	// A campaign starting or ending changes the policy: read it again then.
	if r != nil && r.Policy != nil && r.Policy.NextChangeAt != nil {
		at := *r.Policy.NextChangeAt
		if wait := time.Until(time.UnixMilli(int64(at))); wait > 0 {
			c.After(max(wait, time.Second))
		} else if a.referral.policyRun != at && !f.loading {
			a.referral.policyRun = at
			a.reload(f, nil)
		}
	}
	return r, f
}

// createReferralLink makes the workspace's link and copies it.
func (a *App) createReferralLink() {
	st := &a.referral
	if st.linkPending {
		return
	}
	st.linkPending = true
	convex, ws := a.convex, a.workspaceID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var out struct {
			Code string `json:"code"`
			URL  string `json:"url"`
		}
		err := convex.Mutation(ctx, "referrals:createLink", map[string]any{"workspaceId": ws}, &out)
		a.update(func() {
			st.linkPending = false
			if err != nil || out.URL == "" {
				a.toast("danger", locales.ReferralCreateFailed())
				return
			}
			mygo.Clipboard.WriteText(out.URL)
			st.copiedAt = time.Now()
			a.toast("success", locales.ReferralCreatedCopied())
			a.refetchKey("referrals")
		})
	}()
}

// openReferralDetails opens the details, their history read afresh.
func (a *App) openReferralDetails() {
	st := &a.referral
	st.details, st.cursors = true, []string{""}
	for k := range a.fetches {
		if strings.HasPrefix(k, a.workspaceID+"|referral-ledger|") {
			delete(a.fetches, k)
		}
	}
}

// referralCard is the card under the plan, where the plan can refer.
func (a *App) referralCard(c *ui.Context, p *palette, canManage bool) {
	r, f := a.referralOverview(c)
	if r == nil {
		if f.loading {
			ui.Column(c).FillWidth().Gap(12).Padding(4, 0).Children(func() {
				skeleton(c, p, "referral-title", 16).WidthPercent(40)
				skeleton(c, p, "referral-link", 28).FillWidth()
			})
		}
		return
	}
	active := r.Code != nil || r.Issued > 0 || r.Spent > 0 || r.Adjustment > 0
	if !r.Eligible && !active {
		return
	}
	st := &a.referral
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, locales.ReferralTitle()).FontSize(sizeBody).TextColor(p.Text)
				referralDescription(c, p, r.Policy)
				if pol := r.Policy; pol != nil && pol.StartsAt != nil && pol.EndsAt != nil {
					// The server's deadline is exclusive: the last day is the one before.
					ui.Text(c, locales.ReferralCampaignDates(campaignDay(*pol.StartsAt), campaignDay(*pol.EndsAt-1))).FontSize(sizeFoot).TextColor(p.Secondary)
				}
			})
			ui.Column(c).AlignItems(ui.End).Shrink(0).Gap(2).Children(func() {
				ui.Text(c, formatMoney(r.Available, "usd")).FontSize(sizeBody*1.25).FontWeight(500).TextColor(p.Strong).FontFeatures("tnum")
				ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
					ui.Text(c, locales.ReferralAvailable()).FontSize(sizeFoot).TextColor(p.Secondary)
					ui.Text(c, "·").FontSize(sizeFoot).TextColor(p.Secondary)
					link := ui.ButtonBase(c.Key("referral-details")).Gap(2).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(locales.ReferralDetails()).
						OnClick(a.openReferralDetails)
					ink := p.Secondary
					if link.Hovered() {
						ink = p.Text
					}
					link.Children(func() {
						ui.Text(c, locales.ReferralDetails()).FontSize(sizeFoot).TextColor(ink)
						ui.Icon(c, iconChevronRight).Size(12, 12).TextColor(ink)
					})
				})
			})
		})
		switch {
		case !r.Eligible:
			ui.Text(c, locales.ReferralIneligible()).FontSize(sizeFoot).TextColor(p.Secondary)
		case r.URL != nil:
			url := *r.URL
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Grow(1).MinWidth(0).Height(28).Padding(0, 10).Radius(8).AlignItems(ui.Center).Background(p.Input.Alpha(0.5)).Children(func() {
					ui.Text(c, url).FontSize(sizeFoot).TextColor(p.Text).SingleLine().Selectable().Label(locales.ReferralLink())
				})
				copyButton(c, p, "referral", &st.copiedAt, locales.Copy(), locales.ReferralCopied(), func() { mygo.Clipboard.WriteText(url) })
			})
		case canManage:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				b := outlineButton(c, p, nil, locales.ReferralGenerate()).Disabled(st.linkPending)
				if st.linkPending {
					b.Opacity(0.45)
					ui.Spinner(c).Size(13, 13)
				}
				b.OnClick(a.createReferralLink)
			})
		default:
			ui.Text(c, locales.ReferralMemberHint()).FontSize(sizeFoot).TextColor(p.Secondary)
		}
	})
	if st.details {
		a.referralDetails(c, p, r)
	}
}

// referralDescription says what each side gets, a raised discount or
// reward shown against the standard one struck through.
func referralDescription(c *ui.Context, p *palette, pol *referralPolicy) {
	discount, reward, rate := policyValues(pol)
	const dMark, rMark = "\x01", "\x02"
	text := locales.ReferralDescription(dMark, rMark)
	var spans []ui.Span
	compared := func(now, before string, raised bool) {
		if raised {
			spans = append(spans, ui.Span{Text: before, Strikethrough: true, Color: p.Secondary}, ui.Span{Text: " "}, ui.Span{Text: now, Weight: 600, Color: p.Text})
			return
		}
		spans = append(spans, ui.Span{Text: now})
	}
	d0, _, r0 := policyValues(nil)
	for len(text) > 0 {
		i := strings.IndexAny(text, dMark+rMark)
		if i < 0 {
			spans = append(spans, ui.Span{Text: text})
			break
		}
		if i > 0 {
			spans = append(spans, ui.Span{Text: text[:i]})
		}
		if text[i:i+1] == dMark {
			compared(locales.ReferralDiscount(discount, rate), locales.ReferralDiscount(d0, r0), pol != nil && pol.DiscountPercent > 10)
		} else {
			compared(reward+"%", "20%", pol != nil && pol.RewardPercent > 20)
		}
		text = text[i+1:]
	}
	ui.RichText(c, spans...).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Secondary)
}

// referralDetails is the dialog behind Details: the credit's breakdown,
// its history a page at a time, and the rules.
func (a *App) referralDetails(c *ui.Context, p *palette, r *referralOverview) {
	st := &a.referral
	if len(st.cursors) == 0 {
		st.cursors = []string{""}
	}
	cursor := st.cursors[len(st.cursors)-1]
	convex, ws := a.convex, a.workspaceID
	lf := a.fetch("referral-ledger|"+cursor, func(ctx context.Context) (any, error) {
		var out referralLedger
		var cur any
		if cursor != "" {
			cur = cursor
		}
		err := convex.Query(ctx, "referrals:listLedger", map[string]any{"workspaceId": ws, "paginationOpts": map[string]any{"numItems": referralPageSize, "cursor": cur}}, &out)
		return &out, err
	})
	ledger, _ := lf.value.(*referralLedger)
	sheet(c, p, &st.details, 480, func() {
		close := func() { st.details, st.cursors = false, nil }
		sheetHeader(c, p, locales.ReferralDetailsTitle(), locales.ReferralAvailableHelp(), close)
		ui.Column(c).FillWidth().Padding(4, 20, 18, 20).Gap(16).Children(func() {
			figure := func(label, help, value string, muted bool) {
				ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Baseline).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Children(func() {
						ui.Text(c, label).FontSize(sizeBody).TextColor(p.Text)
						if help != "" {
							ui.Text(c, help).FontSize(sizeFoot).TextColor(p.Secondary)
						}
					})
					ink := p.Text
					if muted {
						ink = p.Secondary
					}
					ui.Text(c, value).FontSize(sizeBody).TextColor(ink).FontFeatures("tnum").Shrink(0)
				})
			}
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				figure(locales.ReferralAvailable(), "", formatMoney(r.Available, "usd"), false)
				figure(locales.ReferralEarned(), "", formatMoney(r.Issued, "usd"), true)
				used := formatMoney(0, "usd")
				if r.Spent > 0 {
					used = "−" + formatMoney(r.Spent, "usd")
				}
				figure(locales.ReferralUsed(), "", used, true)
				if r.Frozen > 0 {
					figure(locales.ReferralFrozen(), locales.ReferralFrozenHelp(), formatMoney(r.Frozen, "usd"), true)
				}
				if r.Adjustment > 0 {
					figure(locales.ReferralAdjustment(), locales.ReferralAdjustmentHelp(), formatMoney(r.Adjustment, "usd"), true)
				}
			})
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				ui.Text(c, locales.ReferralHistory()).FontSize(sizeFoot).TextColor(p.Secondary)
				switch {
				case ledger == nil && lf.err != "":
					ui.Text(c, lf.err).FontSize(sizeFoot).TextColor(p.Danger).Selectable()
				case ledger == nil:
					skeleton(c, p, "ledger-1", 16).FillWidth()
					skeleton(c, p, "ledger-2", 16).FillWidth()
				case len(ledger.Page) == 0:
					ui.Text(c, locales.ReferralHistoryEmpty()).FontSize(sizeFoot).TextColor(p.Secondary)
				default:
					ui.Scroll(c).FillWidth().MaxHeight(280).Children(func() {
						ui.Column(c).FillWidth().Dividers(0.5, p.Border).Children(func() {
							for _, e := range ledger.Page {
								label, amount, tone := describeEntry(e)
								ui.Row(c.Key(e.ID)).FillWidth().Gap(16).Padding(8, 0).AlignItems(ui.Center).Children(func() {
									ui.Column(c).Grow(1).MinWidth(0).Children(func() {
										ui.Text(c, label).FontSize(sizeBody).TextColor(p.Text)
										ui.Text(c, formatDay(e.CreatedAt)).FontSize(sizeFoot).TextColor(p.Secondary)
									})
									ink := p.Text
									if tone == "hold" {
										ink = p.Secondary
									}
									ui.Text(c, signedUsd(amount, tone)).FontSize(sizeBody).TextColor(ink).FontFeatures("tnum").Shrink(0)
								})
							}
						})
					})
				}
				newer := len(st.cursors) > 1
				older := ledger != nil && !ledger.IsDone
				if newer || older {
					ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Children(func() {
						pager := func(label string, on bool, run func()) {
							b := ghostButton(c, p, label)
							if !on {
								b.Disabled(true).Opacity(0.45)
								return
							}
							b.OnClick(run)
						}
						pager(locales.ReferralNewer(), newer, func() { st.cursors = st.cursors[:len(st.cursors)-1] })
						pager(locales.ReferralOlder(), older, func() { st.cursors = append(st.cursors, ledger.ContinueCursor) })
					})
				}
			})
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				ui.Text(c, locales.ReferralRulesTitle()).FontSize(sizeFoot).TextColor(p.Secondary)
				discount, reward, rate := policyValues(r.Policy)
				for _, perk := range []string{locales.ReferralPerkDiscount(discount, rate), locales.ReferralPerkReward(reward), locales.ReferralPerkShared()} {
					ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Start).Children(func() {
						ui.Icon(c, iconCheck).Size(14, 14).TextColor(p.Primary).Margin(2, 0, 0, 0)
						ui.Text(c, perk).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Text).Grow(1)
					})
				}
				ui.Text(c, locales.ReferralTerms()).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Secondary)
			})
		})
	})
}
