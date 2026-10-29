package app

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// The Free plan's quotas and the checkout gate, as Lody and lody-ios keep
// them. A Free session takes 30 user turns: from 25 the composer counts
// down, at 30 it closes, saying so once. A Free workspace keeps 200
// sessions: at 200 New chat says so above the composer, and creating
// another is refused. A paid workspace whose checkout is still pending
// takes neither a new session nor a message until it completes, and greets
// the user once a visit with the way back to checkout. Each check is made
// on what the app already has, failing open while the plan or the
// workspace's sessions are not yet known; billing itself is on lody.ai.

const (
	freeTurnLimit    = 30
	freeTurnWarning  = 5
	freeSessionLimit = 200
)

// The reasons Lody gives for refusing a session or a turn.
const (
	quotaPaymentRequired = "workspace_payment_required"
	quotaSessionLimit    = "free_session_limit_reached"
	quotaTurnLimit       = "free_session_turn_limit_reached"
)

// planTier is the workspace's billing entitlement, asked for at most a
// minute apart: its effective plan (free, plus, enterprise, or "" while
// unknown, which fails open) and whether its checkout is pending.
type planTier struct {
	workspace       string
	tier            string
	checkoutPending bool
	at              time.Time
	asking          bool
}

// billableTurns is the session's user turns, the queued ones too.
func billableTurns(v *session.View) int {
	n := 0
	for _, e := range v.Entries {
		if e.Role == "user" {
			n++
		}
	}
	return n
}

// workspaceTier is the plan, asking for it when watch and it is stale.
func (a *App) workspaceTier(watch bool) string {
	return a.entitlement(watch).tier
}

// entitlement is the workspace's billing entitlement
// (billing:getWorkspaceBillingEntitlement), asked for when watch and it is
// a minute old.
func (a *App) entitlement(watch bool) planTier {
	t := &a.tier
	if t.workspace != a.workspaceID {
		*t = planTier{workspace: a.workspaceID}
	}
	if watch && !t.asking && time.Since(t.at) > time.Minute && a.convex != nil && !a.noCloud() && a.workspaceID != "" {
		t.asking = true
		convex, ws := a.convex, a.workspaceID
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var out struct {
				EffectivePlanTier string `json:"effectivePlanTier"`
				CheckoutPending   bool   `json:"checkoutPending"`
			}
			err := convex.Query(ctx, "billing:getWorkspaceBillingEntitlement", map[string]any{"workspaceId": ws}, &out)
			a.update(func() {
				if t.workspace != ws {
					return
				}
				t.asking, t.at = false, time.Now()
				switch tier := out.EffectivePlanTier; {
				case err != nil:
					// An unavailable entitlement fails open, as every client does.
					t.tier, t.checkoutPending = "", false
				case tier == "free" || tier == "plus" || tier == "enterprise":
					t.tier, t.checkoutPending = tier, out.CheckoutPending
				}
			})
		}()
	}
	return *t
}

// quota is a live session's standing: the count-down line, and whether
// its composer is closed.
func (a *App) quota(v *session.View) (notice string, locked bool) {
	if v == nil || v.Status != "live" {
		return "", false
	}
	n := billableTurns(v)
	tier := a.workspaceTier(n >= freeTurnLimit-freeTurnWarning)
	if tier != "free" || n < freeTurnLimit-freeTurnWarning {
		return "", false
	}
	if n >= freeTurnLimit {
		return locales.FreeTurnLimit(), true
	}
	return locales.FreeTurnsRemaining(freeTurnLimit - n), false
}

// quotaLockedNotice says once a session is opened that its quota closed
// the composer.
func (a *App) quotaLockedNotice(sessionID string) {
	if a.quotaAnnounced == sessionID {
		return
	}
	a.quotaAnnounced = sessionID
	a.confirming = confirmation{open: true, title: locales.FreeTurnLimitTitle(), body: locales.FreeTurnLimitBody(), action: locales.FreeTurnOK(), run: func() {}, notice: true}
}

// workspaceSessions is how many sessions the workspace keeps, archived
// ones too, as its catalog lists them; -1 until the catalog has synced.
func (a *App) workspaceSessions() int {
	if !a.synced {
		return -1
	}
	return len(a.catalog.Sessions)
}

// sessionQuota is why a new session may not be created, as Lody's
// session-create admission decides it: a pending checkout whatever the
// count, else on the Free plan the workspace's 200 sessions; "" when it
// may.
func (a *App) sessionQuota() (reason string, current int) {
	t := a.entitlement(true)
	n := a.workspaceSessions()
	switch {
	case t.checkoutPending:
		return quotaPaymentRequired, max(n, 0)
	case t.tier != "free" || n < 0:
		return "", n
	case n >= freeSessionLimit:
		return quotaSessionLimit, n
	}
	return "", n
}

// createAllowed reports whether a new session may be created, saying why
// not as Lody's chat landing does: checkout first, or the Free plan's
// limit, with the way to Plus.
func (a *App) createAllowed() bool {
	reason, n := a.sessionQuota()
	switch reason {
	case quotaPaymentRequired:
		a.checkoutToast()
		return false
	case quotaSessionLimit:
		a.toasts = append(a.toasts, ui.Toast{Type: "danger", Title: locales.FreeSessionLimitReachedTitle(),
			Description: locales.FreeSessionLimitReachedDescription(freeSessionLimit, n), Action: locales.UpgradeToPlus(), OnAction: a.webBilling})
		return false
	}
	return true
}

// turnAllowed reports whether the open session takes another user turn,
// saying why not as Lody's send gate does: checkout first, or the Free
// session's 30 turns.
func (a *App) turnAllowed(v *session.View) bool {
	t := a.entitlement(true)
	if t.checkoutPending {
		a.checkoutToast()
		return false
	}
	if v == nil || t.tier != "free" {
		return true
	}
	if n := billableTurns(v); n >= freeTurnLimit {
		a.toasts = append(a.toasts, ui.Toast{Type: "danger", Title: locales.FreeTurnLimitReachedTitle(), Description: locales.FreeTurnLimitReachedDescription(freeTurnLimit, n)})
		return false
	}
	return true
}

func (a *App) checkoutToast() {
	a.toasts = append(a.toasts, ui.Toast{Type: "danger", Title: locales.WorkspaceCheckoutPendingTitle(), Description: locales.WorkspaceCheckoutPendingDescription()})
}

// sendChecked sends the composer's message once the workspace takes it; a
// message reserved for later is not a turn yet.
func (a *App) sendChecked() {
	empty := strings.TrimSpace(a.draft) == "" && len(a.attachments) == 0
	if !empty && a.schedSends[a.selected] == nil && !a.turnAllowed(a.view) {
		return
	}
	a.send()
}

// sessionLimitNotice is New chat's line above the composer once a Free
// workspace has its 200 sessions, before a send is refused for it: the
// limit, and the way to Plus.
func (a *App) sessionLimitNotice(c *ui.Context, p *palette) {
	if t := a.entitlement(true); t.tier != "free" || a.workspaceSessions() < freeSessionLimit {
		return
	}
	// Tailwind's amber, as Lody's notice: a 30% rule over a 10% wash.
	amber := ui.Hex("#F59E0B")
	ink, link := ui.Hex("#451A03"), ui.Hex("#B45309")
	if p.Dark {
		ink, link = ui.Hex("#FEF3C7"), ui.Hex("#FDE68A")
	}
	ui.Row(c).FillWidth().Justify(ui.Center).Padding(0, 24, 8, 24).Children(func() {
		ui.Row(c.Key("session-limit")).FillWidth().MaxWidth(760).Wrap().Gap(6).AlignItems(ui.Center).Padding(8, 12).Radius(8).
			Border(1, amber.Alpha(0.3)).Background(amber.Alpha(0.1)).Children(func() {
			ui.Text(c, locales.SessionLimitNotice(freeSessionLimit)).FontSize(sizeFoot).FontWeight(500).TextColor(ink)
			up := ui.ButtonBase(c.Key("upgrade")).Cursor(ui.CursorPointer).Label(locales.UpgradeToPlus()).OnClick(a.webBilling)
			hue := link
			if up.Hovered() {
				hue = link.Mix(ink, 0.35)
			}
			up.Children(func() {
				ui.Text(c, locales.UpgradeToPlus()).FontSize(sizeFoot).FontWeight(600).TextColor(hue).Underline()
			})
		})
	})
}

// checkoutPendingPrompt greets the user, once a visit for each workspace,
// when the workspace's checkout was left pending, as Lody's dialog does:
// the way back to checkout for those who manage its billing, and who to
// ask for everyone else. Settings shows checkout on its own.
func (a *App) checkoutPendingPrompt() {
	ws := a.workspaceID
	t := a.entitlement(true)
	if !t.checkoutPending || ws == "" || a.settingsOpen || a.confirming.open || a.checkoutSeen[ws] || a.convex == nil {
		return
	}
	convex := a.convex
	f := a.fetch("billing", func(ctx context.Context) (any, error) {
		var out *billingOverview
		err := convex.Query(ctx, "billing:getBillingOverview", map[string]any{"workspaceId": ws}, &out)
		return out, err
	})
	if f.loading {
		return
	}
	if a.checkoutSeen == nil {
		a.checkoutSeen = map[string]bool{}
	}
	a.checkoutSeen[ws] = true
	if b, _ := f.value.(*billingOverview); b != nil && b.CanManageBilling {
		a.confirming = confirmation{open: true, title: locales.CheckoutPendingTitle(), body: locales.CheckoutPendingDescription(),
			action: locales.CheckoutPendingGoToCheckout(), cancel: locales.CheckoutPendingLater(), run: a.webBilling, safe: true}
		return
	}
	a.confirming = confirmation{open: true, title: locales.CheckoutPendingTitle(), body: locales.CheckoutPendingMemberDescription(),
		action: locales.CheckoutPendingLater(), run: func() {}, notice: true}
}
