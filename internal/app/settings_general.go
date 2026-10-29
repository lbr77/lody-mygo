package app

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/locales"
)

// The workspace's General page, as Lody's: its name and icon, its members
// and invitations, the open join link with the requests it brought, and
// the danger zone. Names and members go through Better Auth's
// organization endpoints, join links through Convex.

type generalForm struct {
	renaming     bool
	name         string
	inviteOpen   bool
	inviteEmail  string
	inviteAdmin  bool
	inviteErr    string
	linkDays     int // 7, 30 or 90
	transferOpen bool
	transferTo   string // member id
	deleteOpen   bool
	typedName    string
	notice       string
}

type invitation struct {
	ID     string `json:"id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Status string `json:"status"`
}

type joinOwnerState struct {
	ActiveLink *struct {
		ID        string  `json:"id"`
		Token     string  `json:"token"`
		ExpiresAt float64 `json:"expiresAt"`
	} `json:"activeLink"`
	PendingRequests []struct {
		ID             string  `json:"id"`
		ApplicantName  string  `json:"applicantName"`
		ApplicantEmail string  `json:"applicantEmail"`
		Reason         string  `json:"reason"`
		CreatedAt      float64 `json:"createdAt"`
	} `json:"pendingRequests"`
}

// authCall runs a Better Auth call from a settings page, then reloads
// what it changed.
func (a *App) authCall(path string, body any, reload string, then func(err error)) {
	api, token, ws := a.api, a.tokenNow(), a.workspaceID
	a.settingsError = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := api.Call(ctx, token, path, body, nil)
		a.update(func() {
			if err != nil && then == nil {
				a.settingsError = err.Error()
			}
			if f := a.fetches[ws+"|"+reload]; reload != "" && f != nil && err == nil {
				a.reload(f, nil)
			}
			if then != nil {
				then(err)
			}
		})
	}()
}

// myRole is the signed-in user's role in the workspace.
func (a *App) myRole(byUser map[string]cloud.Member) string {
	if a.account == nil {
		return ""
	}
	return byUser[a.account.User.ID].Role
}

func (a *App) generalPage(c *ui.Context, p *palette) {
	g := &a.general
	if g.linkDays == 0 {
		g.linkDays = 30
	}
	settingsTitle(c, p, locales.SettingsGeneral())
	ws := a.workspace()
	if ws == nil {
		return
	}
	byUser, mf := a.members()
	role := a.myRole(byUser)
	admin := role == "owner" || role == "admin"
	a.settingsErrorLine(c, p)
	settingsHeading(c, p, locales.SettingsWorkspace())
	settingRow(c, p, locales.WorkspaceCurrent(), "", func() {
		if g.renaming {
			save := func() {
				name := strings.TrimSpace(g.name)
				g.renaming = false
				if name == "" || name == ws.Name || len([]rune(name)) > 120 {
					return
				}
				a.authCall("/api/auth/organization/update", map[string]any{"organizationId": ws.ID, "data": map[string]any{"name": name}}, "", func(err error) {
					if err != nil {
						a.settingsError = err.Error()
						return
					}
					go a.refreshAccount()
				})
			}
			ui.TextInput(c, &g.name).Width(220).FontSize(sizeBody).AutoFocus().Label(locales.WorkspaceCurrent()).OnSubmit(save)
			return
		}
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			valueText(c, p, ws.Name)
			if admin {
				iconButton(c, p, iconPenLine, locales.WorkspaceRename(), p.Hover).Size(24, 24).OnClick(func() { g.renaming, g.name = true, ws.Name })
			}
		})
	})
	settingRow(c, p, locales.WorkspaceIcon(), "", func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if admin {
				ghostButton(c, p, locales.AvatarChange()).Border(1, p.Border).OnClick(func() { a.pickAvatar("workspace") })
			}
			a.workspaceIcon(c, ws, ws.Name, 32)
		})
	})
	divider(c, p)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		settingsHeading(c, p, locales.SettingsMembers())
		ui.Spacer(c)
		if admin {
			ghostButton(c, p, locales.WorkspaceInvite()).OnClick(func() { g.inviteOpen, g.inviteEmail, g.inviteErr = true, "", "" })
		}
	})
	if pending(c, p, mf) {
		a.memberRows(c, p, mf, admin)
	}
	if admin {
		a.invitations(c, p, ws.ID)
	}
	if role == "owner" {
		divider(c, p)
		a.joinLink(c, p, ws.ID)
	}
	divider(c, p)
	ui.Text(c, locales.WorkspaceDanger()).FontSize(sizeBody).FontWeight(600).TextColor(p.Danger)
	if role == "owner" {
		others := 0
		list, _ := mf.value.([]cloud.Member)
		for _, m := range list {
			if a.account != nil && m.UserID != a.account.User.ID {
				others++
			}
		}
		hint := locales.WorkspaceTransferHelp()
		if others == 0 {
			hint = locales.WorkspaceTransferNoOne()
		}
		settingRow(c, p, locales.WorkspaceTransfer(), hint, func() {
			b := ghostButton(c, p, locales.WorkspaceTransferButton()).Border(1, p.Danger.Alpha(0.4)).Disabled(others == 0)
			if others == 0 {
				b.Opacity(0.5)
			}
			b.OnClick(func() { g.transferOpen, g.transferTo, g.typedName = true, "", "" })
		})
		settingRow(c, p, locales.WorkspaceDelete(), locales.WorkspaceDeleteHelp(), func() {
			ghostButton(c, p, locales.WorkspaceDelete()).Border(1, p.Danger.Alpha(0.4)).OnClick(func() { g.deleteOpen, g.typedName = true, "" })
		})
	} else {
		settingRow(c, p, locales.WorkspaceLeave(), locales.WorkspaceLeaveHelp(), func() {
			ghostButton(c, p, locales.WorkspaceLeave()).Border(1, p.Danger.Alpha(0.4)).OnClick(func() {
				a.confirm(locales.WorkspaceLeaveTitle(ws.Name), locales.WorkspaceLeaveHelp(), locales.WorkspaceLeave(), func() {
					a.authCall("/api/auth/organization/leave", map[string]any{"organizationId": ws.ID}, "", func(err error) {
						if err != nil {
							a.settingsError = err.Error()
							return
						}
						a.settingsOpen = false
						go a.refreshAccount()
					})
				})
			})
		})
	}
	a.inviteDialog(c, p, ws.ID)
	a.transferDialog(c, p, mf)
	a.deleteWorkspaceDialog(c, p)
}

func (a *App) memberRows(c *ui.Context, p *palette, mf *fetched, admin bool) {
	list, _ := mf.value.([]cloud.Member)
	sorted := append([]cloud.Member(nil), list...)
	me := ""
	if a.account != nil {
		me = a.account.User.ID
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].UserID == me && sorted[j].UserID != me })
	ws := a.workspaceID
	for _, m := range sorted {
		member := m
		name := firstNonEmpty(m.User.Name, m.User.Email)
		if m.UserID == me {
			name += " (" + locales.SettingsYou() + ")"
		}
		ui.Row(c.Key(m.ID)).FillWidth().MinHeight(48).Gap(12).AlignItems(ui.Center).Children(func() {
			a.memberAvatar(c, p, member.User, 32)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, name).FontSize(sizeBody).TextColor(p.Text).SingleLine()
				if member.User.Email != "" {
					ui.Text(c, member.User.Email).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
				}
			})
			ui.Text(c, roleLabel(member.Role)).FontSize(sizeFoot).TextColor(p.Secondary)
			if !admin || member.UserID == me || member.Role == "owner" {
				return
			}
			iconButton(c, p, iconMore, locales.MoreActions(), p.Hover).Menu(func(menu *ui.Menu) {
				next := map[string]string{"admin": "member", "member": "admin"}[member.Role]
				label := locales.MemberMakeAdmin()
				if next == "member" {
					label = locales.MemberMakeMember()
				}
				if menu.Item(label).Chosen() {
					a.authCall("/api/auth/organization/update-member-role", map[string]any{"organizationId": ws, "memberId": member.ID, "role": next}, "members", nil)
				}
				if menu.Item(locales.MemberRemove()).Chosen() {
					a.confirm(locales.MemberRemoveTitle(firstNonEmpty(member.User.Name, member.User.Email)), locales.MemberRemoveBody(), locales.MemberRemove(), func() {
						a.authCall("/api/auth/organization/remove-member", map[string]any{"organizationId": ws, "memberIdOrEmail": member.ID}, "members", nil)
					})
				}
			})
		})
	}
}

// memberAvatar is a member's picture, or their initial.
func (a *App) memberAvatar(c *ui.Context, p *palette, u cloud.User, size float32) {
	if u.Image != "" {
		if img := a.urlImage(u.Image); img.bitmap != nil {
			ui.Box(c).Size(size, size).Shrink(0).Radius(size / 2).Clip().Children(func() {
				ui.Image(c, img.bitmap).Size(size, size).Fit(ui.Cover)
			})
			return
		}
	}
	avatar(c, p, firstNonEmpty(u.Name, u.Email), size, size/2)
}

// invitations lists the invitations still pending.
func (a *App) invitations(c *ui.Context, p *palette, ws string) {
	api, token := a.api, a.tokenNow()
	f := a.fetch("invitations", func(ctx context.Context) (any, error) {
		var out []invitation
		err := api.Call(ctx, token, "/api/auth/organization/list-invitations?organizationId="+url.QueryEscape(ws), nil, &out)
		return out, err
	})
	list, _ := f.value.([]invitation)
	for _, in := range list {
		if in.Status != "pending" {
			continue
		}
		inv := in
		ui.Row(c.Key(in.ID)).FillWidth().MinHeight(44).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(32, 32).Radius(16).Center().Border(1, p.Border).Shrink(0).Children(func() {
				ui.Icon(c, iconMail).Size(14, 14).TextColor(p.Secondary)
			})
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, inv.Email).FontSize(sizeBody).TextColor(p.Text).SingleLine()
				ui.Text(c, locales.InvitationPending(roleLabel(inv.Role))).FontSize(sizeFoot).TextColor(p.Secondary)
			})
			iconButton(c, p, iconCopy, locales.InvitationCopy(), p.Hover).OnClick(func() {
				mygo.Clipboard.WriteText("https://lody.ai/invite/" + inv.ID)
			})
			iconButton(c, p, iconX, locales.InvitationCancel(), p.Hover).OnClick(func() {
				a.authCall("/api/auth/organization/cancel-invitation", map[string]any{"invitationId": inv.ID}, "invitations", nil)
			})
		})
	}
}

// joinLink is the open join link: who may ask to join, and the requests.
func (a *App) joinLink(c *ui.Context, p *palette, ws string) {
	g := &a.general
	convex := a.convex
	f := a.fetch("join", func(ctx context.Context) (any, error) {
		var out *joinOwnerState
		err := convex.Query(ctx, "workspaceJoinRequests:getOwnerState", map[string]any{"workspaceId": ws}, &out)
		return out, err
	})
	st, _ := f.value.(*joinOwnerState)
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, locales.JoinTitle()).FontSize(sizeBody).FontWeight(600).TextColor(p.Strong)
		ui.Text(c, locales.JoinHelp()).FontSize(sizeFoot).TextColor(p.Secondary)
	})
	if st != nil && st.ActiveLink != nil {
		link := "https://lody.ai/join/" + st.ActiveLink.Token
		ui.Row(c).FillWidth().Padding(8, 12).Radius(8).Gap(8).AlignItems(ui.Center).Background(p.Text.Alpha(0.04)).Children(func() {
			ui.Text(c, link).Font(fontMono).FontSize(sizeFoot).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0).Selectable()
			ui.Text(c, locales.JoinExpires(formatDay(st.ActiveLink.ExpiresAt))).FontSize(sizeCaption).TextColor(p.Secondary)
			iconButton(c, p, iconCopy, locales.Copy(), p.Hover).OnClick(func() { mygo.Clipboard.WriteText(link) })
		})
	}
	ui.Row(c).FillWidth().Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
		days := []int{7, 30, 90}
		options := []option{}
		for _, d := range days {
			options = append(options, option{fmt.Sprint(d), locales.JoinDays(d)})
		}
		a.toolbarPicker(c, p, "join-days", nil, locales.JoinDays(g.linkDays), options, fmt.Sprint(g.linkDays), func(id string) {
			fmt.Sscan(id, &g.linkDays)
		})
		label := locales.JoinCreate()
		if st != nil && st.ActiveLink != nil {
			label = locales.JoinRotate()
			linkID := st.ActiveLink.ID
			ghostButton(c, p, locales.JoinDisable()).Border(1, p.Border).OnClick(func() {
				a.cloudCall("mutation", "workspaceJoinRequests:revokeLink", map[string]any{"workspaceId": ws, "linkId": linkID}, nil, "join", "", nil)
			})
		}
		outlineButton(c, p, iconLink, label).OnClick(func() {
			a.cloudCall("mutation", "workspaceJoinRequests:rotateLink", map[string]any{"workspaceId": ws, "expiresInDays": g.linkDays}, nil, "join", "", nil)
		})
	})
	if st == nil || len(st.PendingRequests) == 0 {
		if !f.loading {
			ui.Text(c, locales.JoinNoRequests()).FontSize(sizeFoot).TextColor(p.Secondary)
		}
		return
	}
	for _, r := range st.PendingRequests {
		req := r
		ui.Row(c.Key(r.ID)).FillWidth().MinHeight(48).Gap(12).AlignItems(ui.Center).Children(func() {
			avatar(c, p, firstNonEmpty(req.ApplicantName, req.ApplicantEmail), 32, 16)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, firstNonEmpty(req.ApplicantName, req.ApplicantEmail)).FontSize(sizeBody).TextColor(p.Text).SingleLine()
				detail := req.ApplicantEmail
				if req.Reason != "" {
					detail += " · " + req.Reason
				}
				ui.Text(c, detail).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
			})
			ghostButton(c, p, locales.JoinReject()).Border(1, p.Border).OnClick(func() {
				a.cloudCall("mutation", "workspaceJoinRequests:reviewRequest", map[string]any{"requestId": req.ID, "decision": "rejected"}, nil, "join", "", nil)
			})
			a.approveButton(c, p, ws, req.ID)
		})
	}
}

// webSettings opens a settings page of the workspace on lody.ai.
func (a *App) webSettings(page string) {
	slug := a.workspaceID
	if ws := a.workspace(); ws != nil && ws.Slug != "" {
		slug = ws.Slug
	}
	mygo.Shell.OpenExternal("https://lody.ai/" + url.PathEscape(slug) + "/settings/" + page)
}

func (a *App) inviteDialog(c *ui.Context, p *palette, ws string) {
	g := &a.general
	if !g.inviteOpen {
		return
	}
	sheet(c, p, &g.inviteOpen, 440, func() {
		sheetHeader(c, p, locales.WorkspaceInvite(), locales.InviteLead(), func() { g.inviteOpen = false })
		ui.Column(c).FillWidth().Padding(8, 20).Gap(14).Children(func() {
			field(c, p, locales.InviteEmail(), "", func() { input(c, p, &g.inviteEmail, "name@example.com", locales.InviteEmail()).AutoFocus() })
			field(c, p, locales.InviteRole(), "", func() {
				picked := map[bool]int{false: 0, true: 1}[g.inviteAdmin]
				ui.Segmented(c, &picked, locales.RoleMember(), locales.RoleAdmin()).Label(locales.InviteRole())
				g.inviteAdmin = picked == 1
			})
			if g.inviteErr != "" {
				ui.Text(c, g.inviteErr).FontSize(sizeFoot).TextColor(p.Danger)
			}
		})
		ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).Children(func() {
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { g.inviteOpen = false })
			email := strings.TrimSpace(g.inviteEmail)
			primaryButton(c, p, locales.InviteSend(), strings.Contains(email, "@")).OnClick(func() {
				role := "member"
				if g.inviteAdmin {
					role = "admin"
				}
				a.authCall("/api/auth/organization/invite-member", map[string]any{"organizationId": ws, "email": email, "role": role}, "invitations", func(err error) {
					if err != nil {
						g.inviteErr = err.Error()
						if strings.Contains(err.Error(), "member_limit") {
							g.inviteErr = locales.InviteLimit()
						}
						return
					}
					g.inviteOpen = false
					a.refetchKey("invitations")
				})
			})
		})
	})
}

// refetchKey reloads what a page fetched under key, if it did.
func (a *App) refetchKey(key string) {
	if f := a.fetches[a.workspaceID+"|"+key]; f != nil {
		a.reload(f, nil)
	}
}

func (a *App) transferDialog(c *ui.Context, p *palette, mf *fetched) {
	g := &a.general
	ws := a.workspace()
	if !g.transferOpen || ws == nil {
		return
	}
	list, _ := mf.value.([]cloud.Member)
	sheet(c, p, &g.transferOpen, 460, func() {
		sheetHeader(c, p, locales.WorkspaceTransfer(), locales.TransferLead(), func() { g.transferOpen = false })
		ui.Column(c).FillWidth().Padding(8, 20).Gap(10).Children(func() {
			for _, m := range list {
				if a.account != nil && m.UserID == a.account.User.ID || m.Role == "owner" {
					continue
				}
				member := m
				row := ui.Row(c.Key(m.ID)).FillWidth().Height(44).Padding(0, 10).Radius(8).Gap(10).AlignItems(ui.Center).Cursor(ui.CursorPointer).
					Border(1, p.Border).Label(firstNonEmpty(m.User.Name, m.User.Email)).OnClick(func() { g.transferTo = member.ID })
				if g.transferTo == m.ID {
					row.Border(1.5, p.Text)
				}
				row.Children(func() {
					a.memberAvatar(c, p, member.User, 24)
					ui.Text(c, firstNonEmpty(member.User.Name, member.User.Email)).FontSize(sizeSub).TextColor(p.Text).Grow(1)
					ui.Text(c, roleLabel(member.Role)).FontSize(sizeFoot).TextColor(p.Secondary)
				})
			}
			field(c, p, locales.TypeName(ws.Name), "", func() { input(c, p, &g.typedName, ws.Name, locales.TypeName(ws.Name)) })
		})
		ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).Children(func() {
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { g.transferOpen = false })
			ok := g.transferTo != "" && g.typedName == ws.Name
			b := ui.ButtonBase(c).Height(30).Padding(0, 14).Radius(8).Background(p.Danger).Cursor(ui.CursorPointer).Label(locales.WorkspaceTransferButton()).Disabled(!ok)
			if !ok {
				b.Opacity(0.4)
			}
			to := g.transferTo
			b.OnClick(func() {
				g.transferOpen = false
				a.cloudCall("mutation", "auth:transferWorkspaceOwnership", map[string]any{"workspaceId": ws.ID, "targetMemberId": to}, nil, "members", "", func() {
					go a.refreshAccount()
				})
			})
			b.Children(func() {
				ui.Text(c, locales.WorkspaceTransferButton()).FontSize(sizeSub).FontWeight(500).TextColor(ui.RGB(255, 255, 255))
			})
		})
	})
}

func (a *App) deleteWorkspaceDialog(c *ui.Context, p *palette) {
	g := &a.general
	ws := a.workspace()
	if !g.deleteOpen || ws == nil {
		return
	}
	convex, wsID := a.convex, ws.ID
	bf := a.fetch("billing", func(ctx context.Context) (any, error) {
		var out *billingOverview
		err := convex.Query(ctx, "billing:getBillingOverview", map[string]any{"workspaceId": wsID}, &out)
		return out, err
	})
	b, _ := bf.value.(*billingOverview)
	// The backend refuses to delete a workspace still paying, as Lody's
	// dialog explains first.
	paying := b != nil && firstNonEmpty(b.EffectivePlanTier, b.PlanTier) != "free" && b.EntitlementSource != "stripe_gift" && !b.CancelAtPeriodEnd
	sheet(c, p, &g.deleteOpen, 460, func() {
		sheetHeader(c, p, locales.WorkspaceDeleteTitle(ws.Name), locales.WorkspaceDeleteHelp(), func() { g.deleteOpen = false })
		ui.Column(c).FillWidth().Padding(8, 20).Gap(10).Children(func() {
			if paying {
				ui.Text(c, locales.WorkspaceDeletePaying()).FontSize(sizeSub).LineHeight(1.45).TextColor(p.Text)
				return
			}
			field(c, p, locales.TypeName(ws.Name), "", func() { input(c, p, &g.typedName, ws.Name, locales.TypeName(ws.Name)) })
		})
		ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).Children(func() {
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { g.deleteOpen = false })
			if paying {
				primaryButton(c, p, locales.BillingOnWeb(), true).OnClick(func() {
					g.deleteOpen = false
					a.webBilling()
				})
				return
			}
			ok := g.typedName == ws.Name
			del := ui.ButtonBase(c).Height(30).Padding(0, 14).Radius(8).Background(p.Danger).Cursor(ui.CursorPointer).Label(locales.WorkspaceDelete()).Disabled(!ok)
			if !ok {
				del.Opacity(0.4)
			}
			del.OnClick(func() {
				g.deleteOpen = false
				a.authCall("/api/auth/organization/delete", map[string]any{"organizationId": ws.ID}, "", func(err error) {
					if err != nil {
						a.settingsError = err.Error()
						return
					}
					a.settingsOpen = false
					go a.refreshAccount()
				})
			})
			del.Children(func() {
				ui.Text(c, locales.WorkspaceDelete()).FontSize(sizeSub).FontWeight(500).TextColor(ui.RGB(255, 255, 255))
			})
		})
	})
}

// approveButton approves a join request here when it adds no paid seat;
// a seat that bills is confirmed on lody.ai, with the billing.
func (a *App) approveButton(c *ui.Context, p *palette, ws, requestID string) {
	convex := a.convex
	f := a.fetch("seat-preview", func(ctx context.Context) (any, error) {
		var out struct {
			Status string `json:"status"`
		}
		err := convex.Query(ctx, "billing:getWorkspaceSeatInvitePreview", map[string]any{"workspaceId": ws}, &out)
		return out.Status, err
	})
	status, _ := f.value.(string)
	if status != "not_billed" {
		outlineButton(c, p, iconExternal, locales.JoinApproveWeb()).OnClick(func() { a.webSettings("workspace") })
		return
	}
	outlineButton(c, p, iconCheck, locales.JoinApprove()).OnClick(func() {
		a.cloudCall("action", "billing:purchaseWorkspaceMemberSeat", map[string]any{"workspaceId": ws}, nil, "", "", func() {
			a.cloudCall("mutation", "workspaceJoinRequests:reviewRequest", map[string]any{"requestId": requestID, "decision": "approved"}, nil, "join", "", func() {
				a.refetchKey("members")
			})
		})
	})
}
