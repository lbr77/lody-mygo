package app

import (
	"context"
	"time"

	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// The Free plan's session quota, as lody-ios keeps it: 30 user turns a
// session; from 25 the composer counts down, at 30 it closes, saying so
// once. The plan is asked of the workspace only once the count nears it.

const (
	freeTurnLimit   = 30
	freeTurnWarning = 5
)

// planTier is the workspace's effective plan, asked for at most a minute
// apart: free, plus, enterprise, or "" while unknown, which fails open.
type planTier struct {
	workspace string
	tier      string
	at        time.Time
	asking    bool
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
	t := &a.tier
	if t.workspace != a.workspaceID {
		*t = planTier{workspace: a.workspaceID}
	}
	if watch && !t.asking && time.Since(t.at) > time.Minute && a.convex != nil && !a.offline && a.workspaceID != "" {
		t.asking = true
		convex, ws := a.convex, a.workspaceID
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var out struct {
				EffectivePlanTier string `json:"effectivePlanTier"`
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
					t.tier = ""
				case tier == "free" || tier == "plus" || tier == "enterprise":
					t.tier = tier
				}
			})
		}()
	}
	return t.tier
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
