package app

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// Billing, as Lody shows it: the plan and its renewal, referral rewards,
// seats, the next charge and the invoices. Every change to billing is
// made on lody.ai, which this page opens.

type billingOverview struct {
	BillingAccountID  *string  `json:"billingAccountId"`
	PlanTier          string   `json:"planTier"`
	EffectivePlanTier string   `json:"effectivePlanTier"`
	EntitlementSource string   `json:"entitlementSource"`
	BillingInterval   *string  `json:"billingInterval"`
	CancelAtPeriodEnd bool     `json:"cancelAtPeriodEnd"`
	CurrentPeriodEnd  *float64 `json:"currentPeriodEnd"`
	NextBillingAt     *float64 `json:"nextBillingAt"`
	GiftEndsAt        *float64 `json:"giftEndsAt"`
	PromotionEndsAt   *float64 `json:"promotionalEntitlementEndsAt"`
	SeatCount         float64  `json:"seatCount"`
	CanManageBilling  bool     `json:"canManageBilling"`
	ReferralRewards   bool     `json:"referralRewardsSupported"`
}

type billingInvoice struct {
	ID         string   `json:"id"`
	Number     *string  `json:"number"`
	Status     string   `json:"status"`
	AmountPaid float64  `json:"amountPaid"`
	Total      *float64 `json:"total"`
	Currency   string   `json:"currency"`
	Reason     string   `json:"reason"`
	CreatedAt  float64  `json:"createdAt"`
	HostedURL  *string  `json:"hostedInvoiceUrl"`
}

type billingInvoices struct {
	Invoices []billingInvoice `json:"invoices"`
	Upcoming *struct {
		AmountDue  float64  `json:"amountDue"`
		Currency   string   `json:"currency"`
		ExpectedAt *float64 `json:"expectedAt"`
		Renewal    *struct {
			Amount   float64  `json:"amount"`
			Quantity *float64 `json:"quantity"`
		} `json:"renewal"`
		Discount *struct {
			Amount float64 `json:"amount"`
		} `json:"discount"`
		CreditApplied *struct {
			Amount float64 `json:"amount"`
		} `json:"creditApplied"`
	} `json:"upcoming"`
}

type memberLimit struct {
	MemberLimit *float64 `json:"memberLimit"`
	MemberCount float64  `json:"memberCount"`
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

// formatMoney writes an amount in a currency's minor units.
func formatMoney(minor float64, currency string) string {
	cur := strings.ToUpper(currency)
	switch cur {
	case "USD", "":
		sign := ""
		if minor < 0 {
			sign, minor = "-", -minor
		}
		whole := int64(minor) / 100
		cents := ""
		if int64(minor)%100 != 0 {
			cents = fmt.Sprintf(".%02d", int64(minor)%100)
		}
		return sign + "$" + groupThousands(whole) + cents
	case "JPY", "KRW":
		return fmt.Sprintf("%.0f %s", minor, cur)
	}
	return fmt.Sprintf("%.2f %s", minor/100, cur)
}

// groupThousands writes n with commas between its thousands.
func groupThousands(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// formatDay writes a date as the app's language does: 2026年11月27日 or
// Nov 27, 2026.
func formatDay(ms float64) string {
	if ms <= 0 {
		return ""
	}
	t := time.UnixMilli(int64(ms))
	if strings.HasPrefix(i18n.Locale(), "zh") {
		return t.Format("2006年1月2日")
	}
	return t.Format("Jan 2, 2006")
}

func invoiceReason(reason string) string {
	switch reason {
	case "subscription_started":
		return locales.InvoiceStarted()
	case "subscription_renewed":
		return locales.InvoiceRenewed()
	case "seats_added":
		return locales.InvoiceSeatsAdded()
	case "seats_removed":
		return locales.InvoiceSeatsRemoved()
	case "plan_changed", "subscription_changed":
		return locales.InvoiceChanged()
	case "gift_redemption":
		return locales.InvoiceGift()
	}
	return locales.InvoiceOther()
}

// webBilling opens the workspace's billing on lody.ai.
func (a *App) webBilling() {
	slug := a.workspaceID
	if ws := a.workspace(); ws != nil && ws.Slug != "" {
		slug = ws.Slug
	}
	mygo.Shell.OpenExternal("https://lody.ai/" + url.PathEscape(slug) + "/settings/billing")
}

// webLink is a link-styled button that opens lody.ai.
func webLink(c *ui.Context, p *palette, label string, open func()) {
	b := ui.ButtonBase(c.Key(label)).Height(24).Padding(0, 4).Radius(6).Gap(4).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(label).OnClick(open).Transition(colorFade)
	ink := p.Primary
	if b.Hovered() {
		b.Background(p.Hover)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(sizeFoot).TextColor(ink)
		ui.Icon(c, iconExternal).Size(12, 12).TextColor(ink)
	})
}

func (a *App) billingPage(c *ui.Context, p *palette) {
	settingsTitle(c, p, locales.SettingsBilling())
	convex, ws := a.convex, a.workspaceID
	f := a.fetch("billing", func(ctx context.Context) (any, error) {
		var out *billingOverview
		err := convex.Query(ctx, "billing:getBillingOverview", map[string]any{"workspaceId": ws}, &out)
		return out, err
	})
	if !pending(c, p, f) {
		return
	}
	b, _ := f.value.(*billingOverview)
	if b == nil {
		ui.Text(c, locales.BillingUnavailable()).FontSize(sizeFoot).TextColor(p.Secondary)
		return
	}
	tier := firstNonEmpty(b.EffectivePlanTier, b.PlanTier)
	ui.Row(c).FillWidth().AlignItems(ui.Start).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, planLabel(tier)).FontSize(sizeBody).FontWeight(500).TextColor(p.Strong)
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				if b.BillingInterval != nil {
					ui.Text(c, map[string]string{"month": locales.BillingMonthly(), "year": locales.BillingYearly()}[*b.BillingInterval]).FontSize(sizeFoot).TextColor(p.Secondary)
				}
				if tier == "free" {
					webLink(c, p, locales.BillingUpgrade(), a.webBilling)
				} else if b.BillingInterval != nil && *b.BillingInterval == "month" && b.CanManageBilling {
					webLink(c, p, locales.BillingSwitchYearly(), a.webBilling)
				}
			})
		})
		var when *float64
		label := locales.BillingRenews()
		switch {
		case b.CancelAtPeriodEnd && b.CurrentPeriodEnd != nil:
			when, label = b.CurrentPeriodEnd, locales.BillingEnds()
		case b.NextBillingAt != nil:
			when = b.NextBillingAt
		case b.GiftEndsAt != nil:
			when, label = b.GiftEndsAt, locales.BillingEnds()
		case b.PromotionEndsAt != nil:
			when, label = b.PromotionEndsAt, locales.BillingEnds()
		}
		if when != nil {
			ui.Column(c).AlignItems(ui.End).Gap(4).Children(func() {
				ui.Text(c, label).FontSize(sizeFoot).TextColor(p.Secondary)
				ui.Text(c, formatDay(*when)).FontSize(sizeBody).FontWeight(500).TextColor(p.Strong)
			})
		}
	})
	if b.ReferralRewards {
		divider(c, p)
		a.referralCard(c, p, b.CanManageBilling)
	}
	divider(c, p)
	settingRow(c, p, "Sessions", "", func() { valueText(c, p, locales.BillingUnlimited()) })
	lf := a.fetch("member-limit", func(ctx context.Context) (any, error) {
		var out *memberLimit
		err := convex.Query(ctx, "billing:getWorkspaceMemberLimitState", map[string]any{"workspaceId": ws}, &out)
		return out, err
	})
	if l, _ := lf.value.(*memberLimit); l != nil {
		limit := "∞"
		if l.MemberLimit != nil {
			limit = fmt.Sprint(*l.MemberLimit)
		}
		if b.SeatCount > 0 {
			limit = fmt.Sprint(b.SeatCount)
		}
		settingRow(c, p, locales.BillingMembers(), locales.BillingMembersHelp(), func() {
			valueText(c, p, fmt.Sprintf("%v / %s", l.MemberCount, limit))
		})
	}
	if b.BillingAccountID == nil {
		return
	}
	inv := a.fetch("invoices", func(ctx context.Context) (any, error) {
		var out billingInvoices
		err := convex.Action(ctx, "billing:listBillingInvoices", map[string]any{"workspaceId": ws}, &out)
		return &out, err
	})
	if !pending(c, p, inv) {
		return
	}
	list, _ := inv.value.(*billingInvoices)
	if list == nil {
		return
	}
	if up := list.Upcoming; up != nil {
		divider(c, p)
		settingsHeading(c, p, locales.BillingUpcoming())
		ui.Row(c).FillWidth().AlignItems(ui.End).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, locales.BillingUpcomingDate()).FontSize(sizeFoot).TextColor(p.Secondary)
				if up.ExpectedAt != nil {
					ui.Text(c, formatDay(*up.ExpectedAt)).FontSize(sizeBody).FontWeight(500).TextColor(p.Strong)
				}
			})
			ui.Column(c).AlignItems(ui.End).Gap(4).Children(func() {
				ui.Text(c, formatMoney(up.AmountDue, up.Currency)).FontSize(26).FontWeight(500).TextColor(p.Strong)
				ui.Text(c, locales.BillingUpcomingAmount()).FontSize(sizeFoot).TextColor(p.Secondary)
			})
		})
		if up.Renewal != nil {
			seats := 1.0
			if up.Renewal.Quantity != nil {
				seats = *up.Renewal.Quantity
			}
			settingRow(c, p, locales.BillingRenewalLine(int(seats)), "", func() {
				ui.Text(c, formatMoney(up.Renewal.Amount, up.Currency)).FontSize(sizeFoot).TextColor(p.Secondary)
			})
		}
		if up.Discount != nil && up.Discount.Amount != 0 {
			settingRow(c, p, locales.BillingDiscount(), "", func() {
				ui.Text(c, "−"+formatMoney(up.Discount.Amount, up.Currency)).FontSize(sizeFoot).TextColor(p.Secondary)
			})
		}
	}
	divider(c, p)
	settingsHeading(c, p, locales.BillingInvoices())
	if len(list.Invoices) == 0 {
		ui.Text(c, locales.BillingNoInvoices()).FontSize(sizeFoot).TextColor(p.Tertiary)
	}
	for _, in := range list.Invoices {
		amount := in.AmountPaid
		if in.Total != nil {
			amount = *in.Total
		}
		link := ""
		if in.HostedURL != nil {
			link = *in.HostedURL
		}
		row := ui.Row(c.Key(in.ID)).FillWidth().MinHeight(44).Gap(10).AlignItems(ui.Center).Label(invoiceReason(in.Reason))
		if link != "" {
			row.Cursor(ui.CursorPointer).OnClick(func() { mygo.Shell.OpenExternal(link) })
		}
		row.Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, invoiceReason(in.Reason)).FontSize(sizeBody).TextColor(p.Text)
				ui.Text(c, formatDay(in.CreatedAt)).FontSize(sizeFoot).TextColor(p.Secondary)
			})
			ui.Text(c, formatMoney(amount, in.Currency)).FontSize(sizeBody).TextColor(p.Text)
			if link != "" {
				ui.Icon(c, iconChevronRight).Size(14, 14).TextColor(p.Tertiary)
			}
		})
	}
	divider(c, p)
	settingRow(c, p, locales.BillingRedeem(), locales.BillingRedeemHelp(), func() {
		ghostButton(c, p, locales.BillingOnWeb()).Border(1, p.Border).OnClick(a.webBilling)
	})
	divider(c, p)
	settingRow(c, p, locales.BillingPayment(), locales.BillingPaymentHelp(), func() {
		ghostButton(c, p, locales.BillingChangePayment()).Border(1, p.Border).OnClick(a.webBilling)
	})
	if b.CanManageBilling && !b.CancelAtPeriodEnd && tier != "free" {
		ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
			ghostButton(c, p, locales.BillingCancel()).OnClick(a.webBilling)
		})
	}
}
