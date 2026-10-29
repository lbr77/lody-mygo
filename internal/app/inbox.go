package app

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/locales"
)

// The Inbox, as Lody's: what needs the user — an agent waiting on an
// approval or a question, a session finished — and, in a team workspace,
// a nudge to review what the team can see. The server keeps the items
// (inbox:listMine); the sharing review is one this client reconciles
// from what it knows of the workspace's machines and projects.

type inboxItem struct {
	ID          string   `json:"_id"`
	Kind        string   `json:"kind"` // sharing_review, permission_requested or session_completed
	Title       string   `json:"title"`
	Route       string   `json:"route"`
	UpdatedAt   float64  `json:"updatedAt"`
	ReadAt      *float64 `json:"readAt"`
	RequestKind string   `json:"requestKind"`
	ToolLabel   string   `json:"toolLabel"`
}

func (it inboxItem) unread() bool { return it.ReadAt == nil }

// session is the session an item points at, from its route.
func (it inboxItem) session() string {
	_, rest, ok := strings.Cut(it.Route, "/sessions/")
	if !ok {
		return ""
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// inboxItems are the user's items, fetched with the workspace.
func (a *App) inboxItems() ([]inboxItem, *fetched) {
	convex, ws := a.convex, a.workspaceID
	f := a.fetch("inbox", func(ctx context.Context) (any, error) {
		var out []inboxItem
		err := convex.Query(ctx, "inbox:listMine", map[string]any{"workspaceId": ws}, &out)
		return out, err
	})
	// Lody's query is live; this one is asked again each minute.
	if !a.noCloud() && !f.loading && time.Since(a.inboxAt) > time.Minute {
		if !a.inboxAt.IsZero() {
			a.reload(f, nil)
		}
		a.inboxAt = time.Now()
	}
	items, _ := f.value.([]inboxItem)
	return items, f
}

// sharingReview is the state Lody derives for the review.
type sharingReview struct {
	ready, active    bool
	privateMachines  int
	privateProjects  int
	noLocalResources bool
	teamLooksEmpty   bool
	revision         string
}

// target is where the review sends the user: their private machines
// first, then their private projects.
func (s sharingReview) target() string {
	switch {
	case s.privateMachines > 0:
		return "machines"
	case s.privateProjects > 0:
		return "projects"
	}
	return ""
}

// reviewRevision is Lody's hash of what the review was computed from, so
// every client agrees whether it changed.
func reviewRevision(parts []string) string {
	first, second := uint32(0x811c9dc5), uint32(0x9e3779b9)
	for _, part := range parts {
		units := utf16.Encode([]rune(part))
		framed := utf16.Encode([]rune(itoa(len(units)) + ":"))
		framed = append(framed, units...)
		framed = append(framed, ';')
		for _, code := range framed {
			first = (first ^ uint32(code)) * 0x01000193
			second = (second ^ uint32(code)) * 0x85ebca6b
		}
	}
	first = (first ^ (first >> 16)) * 0x85ebca6b
	second = (second ^ (second >> 13)) * 0xc2b2ae35
	return "v1-" + hex8(first) + hex8(second)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func hex8(v uint32) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		out[i] = digits[v&15]
		v >>= 4
	}
	return string(out)
}

// computeSharingReview works out the review from the workspace's members,
// machines and projects, as Lody's landing does.
func (a *App) computeSharingReview() sharingReview {
	if a.account == nil {
		return sharingReview{}
	}
	me := a.account.User.ID
	members, mf := a.members()
	machines := a.visibleMachines()
	pf := a.fetches[a.workspaceID+"|projects"]
	if pf == nil {
		convex, ws := a.convex, a.workspaceID
		pf = a.fetch("projects", func(ctx context.Context) (any, error) {
			var out []visibleProject
			err := convex.Query(ctx, "localProjects:listVisibleLocalProjects", map[string]any{"workspaceId": ws}, &out)
			return out, err
		})
	}
	mfetch := a.fetches[a.workspaceID+"|machines"]
	ready := a.synced && mf != nil && !mf.loading && pf != nil && !pf.loading && mfetch != nil && !mfetch.loading
	var privateMachines, privateProjects []string
	for id, m := range machines {
		if m.OwnerUserID == me && !m.SharedWithTeam {
			privateMachines = append(privateMachines, id)
		}
	}
	projects, _ := pf.value.([]visibleProject)
	for _, pr := range projects {
		if pr.OwnerUserID != me {
			continue
		}
		if !pr.SharedWithTeam || !machines[pr.MachineID].SharedWithTeam {
			privateProjects = append(privateProjects, pr.MachineID+":"+pr.LocalProjectID)
		}
	}
	sort.Strings(privateMachines)
	sort.Strings(privateProjects)
	archived := 0
	for _, s := range a.catalog.Sessions {
		if s.Archived {
			archived++
		}
	}
	noLocal := ready && len(machines) == 0 && len(projects) == 0
	empty := noLocal && len(a.catalog.Sessions) == 0
	var ids []string
	for id := range members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	b := map[bool]string{true: "1", false: "0"}
	return sharingReview{
		ready: ready, privateMachines: len(privateMachines), privateProjects: len(privateProjects),
		noLocalResources: noLocal, teamLooksEmpty: empty,
		active: ready && len(members) > 1 && (len(privateMachines) > 0 || len(privateProjects) > 0 || noLocal),
		revision: reviewRevision([]string{
			"members:" + strings.Join(ids, ","),
			"machines:" + strings.Join(privateMachines, ","),
			"projects:" + strings.Join(privateProjects, ","),
			"no-local:" + b[noLocal],
			"empty:" + b[empty],
		}),
	}
}

// reconcileSharingReview tells the server whether the review applies,
// when what it was computed from changed.
func (a *App) reconcileSharingReview(sr sharingReview) {
	if !sr.ready || a.convex == nil || a.noCloud() {
		return
	}
	key := a.workspaceID + "|" + map[bool]string{true: "on", false: "off"}[sr.active] + "|" + sr.revision
	if a.reviewSent == key {
		return
	}
	a.reviewSent = key
	if a.reviewWriter == "" {
		a.reviewWriter = uuid.NewString()
	}
	a.reviewAttempt++
	convex, ws, writer, attempt := a.convex, a.workspaceID, a.reviewWriter, a.reviewAttempt
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var state *struct {
			SourceRevision *string `json:"sourceRevision"`
		}
		if err := convex.Query(ctx, "inbox:getSharingReviewState", map[string]any{"workspaceId": ws}, &state); err != nil {
			return
		}
		var expected any
		if state != nil && state.SourceRevision != nil {
			expected = *state.SourceRevision
		}
		call := func(expected any) (struct {
			Conflict       bool     `json:"conflict"`
			SourceRevision *string  `json:"sourceRevision"`
			WriterID       *string  `json:"reconcileWriterId"`
			Attempt        *float64 `json:"reconcileAttempt"`
		}, error) {
			var res struct {
				Conflict       bool     `json:"conflict"`
				SourceRevision *string  `json:"sourceRevision"`
				WriterID       *string  `json:"reconcileWriterId"`
				Attempt        *float64 `json:"reconcileAttempt"`
			}
			err := convex.Mutation(ctx, "inbox:reconcileSharingReview", map[string]any{
				"workspaceId": ws, "active": sr.active, "sourceRevision": sr.revision, "expectedSourceRevision": expected,
				"reconcileWriterId": writer, "reconcileAttempt": attempt,
			}, &res)
			return res, err
		}
		res, err := call(expected)
		// A conflict from this writer's own earlier attempt is retried once
		// over the server's revision, as Lody does.
		if err == nil && res.Conflict && res.WriterID != nil && *res.WriterID == writer && res.Attempt != nil && int(*res.Attempt) < attempt {
			var again any
			if res.SourceRevision != nil {
				again = *res.SourceRevision
			}
			call(again)
		}
		a.update(func() { a.refetchKey("inbox") })
	}()
}

// inboxAct marks an item read, dismisses it, or stops the review's
// reminders.
func (a *App) inboxAct(fn, id string) {
	a.cloudCall("mutation", "inbox:"+fn, map[string]any{"itemId": id}, nil, "inbox", "", nil)
}

// openInboxItem goes where an item points, marking it read.
func (a *App) openInboxItem(it inboxItem, sr sharingReview) {
	a.inboxAct("markRead", it.ID)
	if it.Kind == "sharing_review" {
		a.settingsOpen, a.settingsTab = true, firstNonEmpty(map[string]string{"machines": "machines", "projects": "projects"}[sr.target()], "projects")
		return
	}
	if id := it.session(); id != "" {
		a.open(id)
	}
}

// inboxShown are the items to show: the review only while it applies.
func inboxShown(items []inboxItem, sr sharingReview) []inboxItem {
	var out []inboxItem
	for _, it := range items {
		if it.Kind == "sharing_review" && !(sr.ready && sr.active) {
			continue
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

// inboxText is an item's title and line.
func inboxText(it inboxItem, sr sharingReview) (string, string) {
	switch it.Kind {
	case "sharing_review":
		line := locales.InboxReviewNoLocal()
		switch n := sr.privateMachines + sr.privateProjects; {
		case n > 0:
			line = locales.InboxReviewPrivate(n)
		case sr.teamLooksEmpty:
			line = locales.InboxReviewEmpty()
		}
		return locales.InboxReviewTitle(), line
	case "permission_requested":
		line := locales.InboxPermission()
		switch {
		case it.RequestKind == "ask_user_question":
			line = locales.InboxQuestion()
		case it.ToolLabel != "":
			line = locales.InboxPermissionTool(it.ToolLabel)
		}
		return firstNonEmpty(it.Title, locales.Untitled()), line
	}
	return firstNonEmpty(it.Title, locales.Untitled()), locales.InboxCompleted()
}

// inboxPage lists the inbox.
func (a *App) inboxPage(c *ui.Context, p *palette) {
	sr := a.computeSharingReview()
	a.reconcileSharingReview(sr)
	items, f := a.inboxItems()
	shown := inboxShown(items, sr)
	left := a.barInset(8)
	ui.Row(c).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Padding(0, 12, 0, left).Gap(8).DragWindow().Children(func() {
		if a.sidebarHidden {
			iconButton(c, p, iconSidebar, locales.SidebarToggle(), p.Hover).OnClick(func() { a.sidebarHidden = false })
		}
		ui.Text(c, locales.Inbox()).FontSize(sizeBody).FontWeight(600).TextColor(p.Strong).Grow(1)
		iconButton(c, p, iconRefresh, locales.Refresh(), p.Hover).OnClick(func() { a.refetchKey("inbox") })
	})
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(8, 24, 24, 24).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(720).AlignSelf(ui.Center).Gap(2).Children(func() {
			switch {
			case f.loading && len(items) == 0:
				ui.Row(c).FillWidth().Justify(ui.Center).Padding(40).Children(func() { spinner(c).Size(16, 16) })
				return
			case len(shown) == 0:
				ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(60, 0).Children(func() {
					ui.Icon(c, iconInbox).Size(28, 28).TextColor(p.Tertiary)
					ui.Text(c, locales.InboxEmpty()).FontSize(sizeSub).TextColor(p.Secondary)
				})
				return
			}
			for _, it := range shown {
				item := it
				title, line := inboxText(it, sr)
				row := ui.Row(c.Key(it.ID)).FillWidth().Padding(10, 12).Radius(10).Gap(12).AlignItems(ui.Start).Cursor(ui.CursorPointer).
					Label(title).Transition(colorFade).OnClick(func() { a.openInboxItem(item, sr) })
				hovered := row.Hovered()
				if hovered {
					row.Background(p.Hover)
				}
				row.Children(func() {
					icon := iconCircleCheck
					switch item.Kind {
					case "sharing_review":
						icon = iconLock
					case "permission_requested":
						icon = iconHand
					}
					ui.Box(c).Size(28, 28).Radius(8).Center().Background(p.Text.Alpha(0.05)).Shrink(0).Children(func() {
						ui.Icon(c, icon).Size(15, 15).TextColor(p.Secondary)
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
							weight := 500
							if item.unread() {
								weight = 600
							}
							ui.Text(c, title).FontSize(sizeSub).FontWeight(weight).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
							if item.UpdatedAt > 0 {
								ui.Text(c, relativeTime(time.UnixMilli(int64(item.UpdatedAt)))).FontSize(sizeCaption).TextColor(p.Tertiary).Shrink(0)
							}
						})
						ui.Text(c, line).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Secondary)
						if item.Kind == "sharing_review" {
							ui.Row(c).Gap(12).Margin(4, 0, 0, 0).Children(func() {
								if t := sr.target(); t != "" {
									label := locales.InboxReviewProjects()
									if t == "machines" {
										label = locales.InboxReviewMachines()
									}
									ui.Text(c, label).FontSize(sizeFoot).FontWeight(600).TextColor(p.Primary)
								}
								ui.Text(c, locales.InboxNeverRemind()).FontSize(sizeFoot).TextColor(p.Secondary).Cursor(ui.CursorPointer).
									Label(locales.InboxNeverRemind()).OnClick(func() { a.inboxAct("suppressSharingReview", item.ID) })
							})
						}
					})
					if item.unread() {
						ui.Box(c).Size(8, 8).Radius(4).Background(p.Primary).Margin(6, 0, 0, 0).Shrink(0)
					}
					dismiss := iconButton(c, p, iconX, locales.Dismiss(), p.Hover).Size(24, 24).OnClick(func() { a.inboxAct("dismiss", item.ID) })
					if !hovered {
						dismiss.Opacity(0)
					}
				})
			}
		})
	})
}

// inboxUnread is how many shown items are unread.
func (a *App) inboxUnread() int {
	if a.convex == nil && !a.offline {
		return 0
	}
	sr := a.computeSharingReview()
	items, _ := a.inboxItems()
	n := 0
	for _, it := range inboxShown(items, sr) {
		if it.unread() {
			n++
		}
	}
	return n
}

// sharingNotice is the review over the new chat's composer, as Lody's
// landing shows it.
func (a *App) sharingNotice(c *ui.Context, p *palette) {
	sr := a.computeSharingReview()
	a.reconcileSharingReview(sr)
	if !sr.ready || !sr.active {
		return
	}
	items, _ := a.inboxItems()
	var row *inboxItem
	for i := range items {
		if items[i].Kind == "sharing_review" {
			row = &items[i]
		}
	}
	if row == nil {
		return
	}
	item := *row
	line := locales.InboxReviewLanding()
	switch {
	case sr.teamLooksEmpty:
		line = locales.InboxReviewEmpty()
	case sr.privateMachines+sr.privateProjects == 0:
		line = locales.InboxReviewNoLocal()
	}
	ui.Row(c).FillWidth().Justify(ui.Center).Padding(0, 24, 8, 24).Children(func() {
		a.sharingCard(c, p, item, sr, line)
	})
}

func (a *App) sharingCard(c *ui.Context, p *palette, item inboxItem, sr sharingReview, line string) {
	ui.Row(c).FillWidth().MaxWidth(760).Gap(10).AlignItems(ui.Start).Padding(10, 12).Radius(10).Border(1, p.Border).Background(p.Text.Alpha(0.035)).Children(func() {
		ui.Icon(c, iconLock).Size(16, 16).TextColor(p.Secondary).Margin(2, 0, 0, 0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, locales.InboxReviewTitle()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
			ui.Text(c, line).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Secondary)
			ui.Row(c).Gap(12).Margin(3, 0, 0, 0).Children(func() {
				if t := sr.target(); t != "" {
					label := locales.InboxReviewProjects()
					if t == "machines" {
						label = locales.InboxReviewMachines()
					}
					ui.Text(c, label).FontSize(sizeFoot).FontWeight(600).TextColor(p.Primary).Cursor(ui.CursorPointer).Label(label).
						OnClick(func() { a.openInboxItem(item, sr) })
				}
				ui.Text(c, locales.InboxNeverRemind()).FontSize(sizeFoot).TextColor(p.Secondary).Cursor(ui.CursorPointer).
					Label(locales.InboxNeverRemind()).OnClick(func() { a.inboxAct("suppressSharingReview", item.ID) })
			})
		})
		iconButton(c, p, iconX, locales.Dismiss(), p.Hover).Size(26, 26).OnClick(func() { a.inboxAct("dismiss", item.ID) })
	})
}
