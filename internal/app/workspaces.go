package app

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"golang.org/x/text/unicode/norm"

	"github.com/lbr77/lody-mygo/locales"
)

// Making and joining workspaces, as Lody's dialogs do: a new workspace
// with a name and a slug the server says is free, while the user's free
// allowance lasts (paid ones are made on lody.ai); a join link, which asks
// the owner for access; an invitation, which joins at once.

var (
	slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	slugAllowed = regexp.MustCompile(`^[a-z0-9-]+$`)
	nonSlug     = regexp.MustCompile(`[^a-z0-9]+`)
	dashes      = regexp.MustCompile(`-{2,}`)
)

// stripMarks drops accents, as NFKD then removing combining marks does.
func stripMarks(s string) string {
	var b strings.Builder
	for _, r := range norm.NFKD.String(s) {
		if r >= 0x300 && r <= 0x36f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// suggestSlug is Lody's slug for a workspace name.
func suggestSlug(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	s := nonSlug.ReplaceAllString(strings.ToLower(stripMarks(name)), "-")
	s = dashes.ReplaceAllString(strings.Trim(s, "-"), "-")
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		return "workspace"
	}
	return s
}

// typedSlug is a slug as typed: a trailing dash allowed while editing.
func typedSlug(v string) string {
	s := strings.TrimLeft(strings.ToLower(stripMarks(v)), "-")
	s = dashes.ReplaceAllString(s, "-")
	if r := []rune(s); len(r) > 40 {
		s = string(r[:40])
	}
	return s
}

// slugRuleError is what a slug's form breaks, "" when nothing.
func slugRuleError(s string) string {
	switch {
	case s == "":
		return locales.WorkspaceSlugRequired()
	case len(s) < 3 || len(s) > 40:
		return locales.WorkspaceSlugLength()
	case !slugAllowed.MatchString(s) || strings.Contains(s, "--"):
		return locales.WorkspaceSlugChars()
	case strings.HasSuffix(s, "-"):
		return locales.WorkspaceSlugTrailing()
	}
	return ""
}

type workspaceCreate struct {
	open       bool
	name       string
	slug       string
	slugEdited bool
	checked    string // the slug the server answered for
	checking   string // the slug being asked about
	available  bool
	busy       bool
	err        string
}

func (a *App) openCreateWorkspace() {
	a.wsCreate = workspaceCreate{open: true}
	a.workspacesOpen = false
}

// creationAllowed is whether the user can make a workspace for free.
func (a *App) creationAllowed() (bool, bool) {
	convex := a.convex
	f := a.fetch("creation", func(ctx context.Context) (any, error) {
		var out struct {
			CanCreateFree bool `json:"canCreateFree"`
		}
		err := convex.Query(ctx, "billing:getWorkspaceCreationAvailability", map[string]any{}, &out)
		return out.CanCreateFree, err
	})
	ok, _ := f.value.(bool)
	return ok, f.value != nil
}

// checkSlug asks the server whether the slug is free, once per slug.
func (a *App) checkSlug(slug string) {
	w := &a.wsCreate
	if w.checking == slug || w.checked == slug || !slugPattern.MatchString(slug) || a.convex == nil || a.offline {
		return
	}
	w.checking = slug
	convex := a.convex
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var out struct {
			Available bool `json:"available"`
		}
		err := convex.Query(ctx, "auth:isWorkspaceSlugAvailable", map[string]any{"slug": slug}, &out)
		a.update(func() {
			if w.checking == slug {
				w.checking = ""
			}
			if err == nil {
				w.checked, w.available = slug, out.Available
			}
		})
	}()
}

func (a *App) createWorkspace() {
	w := &a.wsCreate
	name, slug := strings.TrimSpace(w.name), w.slug
	if name == "" || slugRuleError(slug) != "" || w.checked != slug || !w.available || w.busy {
		return
	}
	w.busy, w.err = true, ""
	api, token := a.api, a.tokenNow()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var org struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		}
		err := api.Call(ctx, token, "/api/auth/organization/create", map[string]any{"name": name, "slug": slug}, &org)
		if err == nil {
			a.refreshAccount()
		}
		a.update(func() {
			w.busy = false
			if err != nil {
				w.err = err.Error()
				return
			}
			w.open = false
			if org.ID != "" {
				a.connect(org.ID)
			}
		})
	}()
}

func (a *App) createWorkspaceDialog(c *ui.Context, p *palette) {
	w := &a.wsCreate
	if !w.open {
		return
	}
	free, known := a.creationAllowed()
	blocked := known && !free
	title, lead := locales.WorkspaceCreateTitle(), locales.WorkspaceCreateLead()
	if blocked {
		title, lead = locales.WorkspaceLimitTitle(), locales.WorkspaceLimitLead()
	}
	sheet(c, p, &w.open, 480, func() {
		sheetHeader(c, p, title, lead, func() { w.open = false })
		if blocked {
			ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).Children(func() {
				ghostButton(c, p, locales.Close()).Border(1, p.Border).OnClick(func() { w.open = false })
				primaryButton(c, p, locales.WorkspaceCreateOnWeb(), true).OnClick(func() { openExternal("https://lody.ai/workspace/create") })
			})
			return
		}
		ui.Column(c).FillWidth().Padding(4, 20, 8, 20).Gap(12).Children(func() {
			field(c, p, locales.WorkspaceName(), locales.WorkspaceNameHint(), func() {
				before := w.name
				input(c, p, &w.name, locales.WorkspaceNamePlaceholder(), locales.WorkspaceName()).AutoFocus()
				if w.name != before && !w.slugEdited {
					w.slug = suggestSlug(w.name)
				}
			})
			field(c, p, locales.WorkspaceSlug(), "", func() {
				ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					before := w.slug
					input(c, p, &w.slug, "my-workspace", locales.WorkspaceSlug()).Font(fontMono).Grow(1)
					if w.slug != before {
						w.slug, w.slugEdited = typedSlug(w.slug), true
					}
					if w.slugEdited && w.slug != suggestSlug(w.name) {
						ghostButton(c, p, locales.WorkspaceSlugReset()).OnClick(func() {
							w.slugEdited, w.slug = false, suggestSlug(w.name)
						})
					}
				})
				msg, ink := "", p.Secondary
				switch e := slugRuleError(w.slug); {
				case e != "":
					if w.slug != "" {
						msg, ink = e, p.Danger
					}
				case w.checked == w.slug && !w.available:
					msg, ink = locales.WorkspaceSlugTaken(), p.Danger
				case w.checked == w.slug:
					msg, ink = locales.WorkspaceSlugAvailable(), p.Success
				default:
					a.checkSlug(w.slug)
					msg = locales.WorkspaceSlugChecking()
				}
				if msg != "" {
					ui.Text(c, msg).FontSize(sizeCaption).TextColor(ink)
				}
			})
			if w.err != "" {
				ui.Text(c, w.err).FontSize(sizeFoot).TextColor(p.Danger).Selectable()
			}
		})
		ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { w.open = false })
			if w.busy {
				ui.Spinner(c).Size(14, 14)
			}
			ready := strings.TrimSpace(w.name) != "" && slugRuleError(w.slug) == "" && w.checked == w.slug && w.available && !w.busy
			primaryButton(c, p, locales.WorkspaceCreate(), ready).OnClick(a.createWorkspace)
		})
	})
}

// workspaceJoin is the dialog for a join link or an invitation.
type workspaceJoin struct {
	open    bool
	link    string
	kind    string // join or invite, from the link
	id      string // the join token, or the invitation's id
	loading bool
	preview map[string]any
	reason  string
	busy    bool
	done    string // what happened
	err     string
}

var (
	joinLink   = regexp.MustCompile(`/join/([A-Za-z0-9_-]+)`)
	inviteLink = regexp.MustCompile(`/invite/([A-Za-z0-9_-]+)`)
)

// parseWorkspaceLink reads a lody.ai join or invitation link.
func parseWorkspaceLink(link string) (kind, id string) {
	if m := joinLink.FindStringSubmatch(link); m != nil {
		return "join", m[1]
	}
	if m := inviteLink.FindStringSubmatch(link); m != nil {
		return "invite", m[1]
	}
	return "", ""
}

func (a *App) openJoinWorkspace() {
	a.wsJoin = workspaceJoin{open: true}
	a.workspacesOpen = false
}

// lookUpLink previews what the link offers.
func (a *App) lookUpLink() {
	j := &a.wsJoin
	kind, id := parseWorkspaceLink(strings.TrimSpace(j.link))
	if kind == "" {
		j.err = locales.JoinLinkInvalid()
		return
	}
	j.kind, j.id, j.loading, j.err, j.preview, j.done = kind, id, true, "", nil, ""
	convex := a.convex
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var out map[string]any
		var err error
		if kind == "join" {
			err = convex.Query(ctx, "workspaceJoinRequests:getLinkPreview", map[string]any{"token": id}, &out)
		} else {
			err = convex.Query(ctx, "auth:getInvitationPreview", map[string]any{"invitationId": id}, &out)
		}
		a.update(func() {
			j.loading = false
			if err != nil {
				j.err = err.Error()
				return
			}
			j.preview = out
			if viewer, _ := out["viewer"].(map[string]any); viewer != nil {
				if req, _ := viewer["request"].(map[string]any); req != nil && str(req["status"]) == "rejected" {
					j.reason = str(req["reason"])
				}
			}
		})
	}()
}

// submitJoin asks for access, or accepts the invitation.
func (a *App) submitJoin() {
	j := &a.wsJoin
	if j.busy {
		return
	}
	j.busy, j.err = true, ""
	convex, api, token, kind, id, reason := a.convex, a.api, a.tokenNow(), j.kind, j.id, strings.TrimSpace(j.reason)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var err error
		orgID := ""
		if kind == "join" {
			err = convex.Mutation(ctx, "workspaceJoinRequests:submitRequest", map[string]any{"token": id, "reason": reason}, nil)
		} else {
			var res struct {
				Invitation struct {
					OrganizationID string `json:"organizationId"`
				} `json:"invitation"`
				Member struct {
					OrganizationID string `json:"organizationId"`
				} `json:"member"`
			}
			err = api.Call(ctx, token, "/api/auth/organization/accept-invitation", map[string]any{"invitationId": id}, &res)
			orgID = firstNonEmpty(res.Member.OrganizationID, res.Invitation.OrganizationID)
			if err == nil {
				a.refreshAccount()
			}
		}
		a.update(func() {
			j.busy = false
			if err != nil {
				if kind == "join" {
					j.err = locales.JoinSubmitFailed()
				} else {
					j.err = err.Error()
				}
				return
			}
			if kind == "join" {
				j.done = locales.JoinPending()
				return
			}
			j.open = false
			if orgID != "" {
				a.connect(orgID)
			}
		})
	}()
}

func (a *App) joinWorkspaceDialog(c *ui.Context, p *palette) {
	j := &a.wsJoin
	if !j.open {
		return
	}
	sheet(c, p, &j.open, 480, func() {
		sheetHeader(c, p, locales.JoinWorkspaceTitle(), locales.JoinLead(), func() { j.open = false })
		ui.Column(c).FillWidth().Padding(4, 20, 8, 20).Gap(12).Children(func() {
			ui.Row(c).FillWidth().Gap(6).Children(func() {
				input(c, p, &j.link, "https://lody.ai/join/…", locales.JoinLink()).Grow(1).AutoFocus()
				ghostButton(c, p, locales.JoinContinue()).Border(1, p.Border).OnClick(a.lookUpLink)
			})
			if j.loading {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(12, 12)
					ui.Text(c, locales.JoinChecking()).FontSize(sizeFoot).TextColor(p.Secondary)
				})
			}
			if pv := j.preview; pv != nil {
				a.joinPreview(c, p, j, pv)
			}
			if j.done != "" {
				ui.Text(c, j.done).FontSize(sizeFoot).TextColor(p.Success)
			}
			if j.err != "" {
				ui.Text(c, j.err).FontSize(sizeFoot).TextColor(p.Danger).Selectable()
			}
		})
		ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			ghostButton(c, p, locales.Close()).Border(1, p.Border).OnClick(func() { j.open = false })
		})
	})
}

// joinPreview shows what the link offers and how to take it.
func (a *App) joinPreview(c *ui.Context, p *palette, j *workspaceJoin, pv map[string]any) {
	if str(pv["status"]) != "available" {
		ui.Column(c).Gap(2).Children(func() {
			title, line := locales.JoinUnavailableTitle(), locales.JoinUnavailable()
			if j.kind == "invite" {
				title, line = locales.InviteUnavailableTitle(), locales.InviteUnavailable()
			}
			ui.Text(c, title).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
			ui.Text(c, line).FontSize(sizeFoot).TextColor(p.Secondary)
		})
		return
	}
	card := func(body func()) {
		ui.Column(c).FillWidth().Gap(8).Padding(12).Radius(10).Background(p.Text.Alpha(0.035)).Children(body)
	}
	if j.kind == "invite" {
		card(func() {
			ui.Text(c, locales.InviteEyebrow()).FontSize(sizeCaption).TextColor(p.Tertiary)
			ui.Text(c, firstNonEmpty(str(pv["organizationName"]), locales.WorkspaceFallback())).FontSize(sizeBody).FontWeight(600).TextColor(p.Text)
			if inviter := str(pv["inviterName"]); inviter != "" {
				ui.Text(c, locales.InviteInvitedBy(inviter)).FontSize(sizeFoot).TextColor(p.Secondary)
			}
			if role := str(pv["role"]); role != "" {
				ui.Text(c, locales.WsInviteRole(role)).FontSize(sizeCaption).TextColor(p.Tertiary)
			}
			if pv["recipientMatchesSession"] == false {
				ui.Text(c, locales.InviteMismatch()).FontSize(sizeFoot).TextColor(p.Warning)
				return
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if j.busy {
					ui.Spinner(c).Size(13, 13)
				}
				primaryButton(c, p, locales.InviteAccept(), !j.busy).OnClick(a.submitJoin)
			})
		})
		return
	}
	viewer, _ := pv["viewer"].(map[string]any)
	card(func() {
		ui.Text(c, locales.JoinEyebrow()).FontSize(sizeCaption).TextColor(p.Tertiary)
		ui.Text(c, firstNonEmpty(str(pv["workspaceName"]), locales.WorkspaceFallback())).FontSize(sizeBody).FontWeight(600).TextColor(p.Text)
		req, _ := viewer["request"].(map[string]any)
		switch {
		case viewer == nil:
			ui.Text(c, locales.JoinUnavailable()).FontSize(sizeFoot).TextColor(p.Secondary)
		case viewer["alreadyMember"] == true:
			ui.Text(c, locales.JoinAlreadyMember()).FontSize(sizeFoot).TextColor(p.Secondary)
		case viewer["emailVerified"] == false:
			email := ""
			if a.account != nil {
				email = a.account.User.Email
			}
			ui.Text(c, locales.JoinVerify(email)).FontSize(sizeFoot).TextColor(p.Warning)
		case req != nil && str(req["status"]) == "pending" || j.done != "":
			ui.Text(c, locales.JoinPending()).FontSize(sizeFoot).TextColor(p.Secondary)
		case req != nil && str(req["status"]) == "approved":
			ui.Text(c, locales.JoinApproved()).FontSize(sizeFoot).TextColor(p.Success)
		default:
			if req != nil && str(req["status"]) == "rejected" {
				ui.Text(c, locales.JoinRejected()).FontSize(sizeFoot).TextColor(p.Warning)
			}
			if a.account != nil {
				ui.Text(c, locales.JoinIdentity(a.account.User.Email)).FontSize(sizeCaption).TextColor(p.Tertiary)
			}
			field(c, p, locales.JoinReason(), "", func() {
				area(c, p, &j.reason, locales.JoinReasonPlaceholder(), locales.JoinReason(), 3)
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				label := locales.JoinSubmit()
				if j.busy {
					ui.Spinner(c).Size(13, 13)
					label = locales.JoinSubmitting()
				}
				primaryButton(c, p, label, !j.busy).OnClick(a.submitJoin)
			})
		}
	})
}
