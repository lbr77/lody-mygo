package app

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// The conversation header's one access control, as Lody's: a pill saying
// Share (or Shared, once published; a lock while private) that opens a
// panel answering who can open the conversation. A title and a faint
// watermark name how far it reaches, and a strip of audiences — only you,
// the team, the web — is the control: the team asks first, the web opens
// the publishing view in the panel itself. Copying its link, and copies
// that leave the conversation, sit below. The panel's height follows its
// view with a small overshoot, 460ms, and the views slide 14px to the side
// they live on over 260ms; the watermark lands from its corner over 550ms.

var (
	// elasticHeight is the panel's height following its view.
	elasticHeight = ui.ElementTransition{Size: true, Duration: 460 * time.Millisecond, Ease: ui.CubicBezier(0.3, 1.2, 0.5, 1)}
	// iconLockKeyhole is Lucide's lock-keyhole.
	iconLockKeyhole = lucide(`<circle cx="12" cy="16" r="1"/><rect x="3" y="10" width="18" height="12" rx="2"/><path d="M7 10V7a5 5 0 0 1 10 0v3"/>`)
)

// accessState is who can open a conversation, as Lody resolves it from
// its machine's and project's team sharing.
type accessState struct {
	visibility string // unknown, private or team
	reason     string // machine, project, machine-and-project or machine-not-registered
	canManage  bool
	machineID  string
	localID    string // the local project's ID, "" for another project
	machine    string
	project    string
}

// accessPanel is the panel's state.
type accessPanel struct {
	open    bool
	view    string // home or publish
	moved   bool   // the reader moved between views: they slide
	publish shareDialog
}

// sessionAccess is who can open s, or nil where a workspace of one has
// no one to share with.
func (a *App) sessionAccess(s *catalog.Session) *accessState {
	if a.account == nil || a.noCloud() {
		return nil
	}
	members, mf := a.members()
	if mf == nil || mf.loading || len(members) <= 1 {
		return nil
	}
	st := &accessState{machineID: s.MachineID, machine: a.machineName(s.MachineID)}
	if pr := a.catalog.Project(s.ProjectID); pr != nil {
		st.project = pr.Name
	}
	if kind, _ := s.Project["kind"].(string); kind == "local" {
		st.localID, _ = s.Project["localProjectId"].(string)
	}
	if st.machineID == "" {
		st.visibility = "unknown"
		return st
	}
	machines := a.visibleMachines()
	mfetch := a.fetches[a.workspaceID+"|machines"]
	pf := a.fetch("projects", func(ctx context.Context) (any, error) {
		var out []visibleProject
		err := a.convex.Query(ctx, "localProjects:listVisibleLocalProjects", map[string]any{"workspaceId": a.workspaceID}, &out)
		return out, err
	})
	m, known := machines[st.machineID]
	st.canManage = known && m.OwnerUserID == a.account.User.ID
	if mfetch == nil || mfetch.loading && !known || st.localID != "" && pf.loading && pf.value == nil {
		st.visibility = "unknown"
		return st
	}
	if !known || m.UpdatedAt == 0 {
		st.visibility, st.reason = "private", "machine-not-registered"
		return st
	}
	projectShared := true
	if st.localID != "" {
		projectShared = false
		projects, _ := pf.value.([]visibleProject)
		for _, pr := range projects {
			if pr.MachineID == st.machineID && pr.LocalProjectID == st.localID {
				projectShared = pr.SharedWithTeam
			}
		}
	}
	switch {
	case m.SharedWithTeam && projectShared:
		st.visibility = "team"
	case !m.SharedWithTeam && !projectShared:
		st.visibility, st.reason = "private", "machine-and-project"
	case !m.SharedWithTeam:
		st.visibility, st.reason = "private", "machine"
	default:
		st.visibility, st.reason = "private", "project"
	}
	return st
}

// description is what the panel says of a conversation's reach.
func (st *accessState) description() string {
	machine := firstNonEmpty(st.machine, locales.AccessThisDevice())
	project := firstNonEmpty(st.project, locales.AccessThisProject())
	switch {
	case st.visibility == "unknown":
		return locales.AccessCheckingDescription()
	case st.visibility == "team":
		return locales.AccessTeamDescription()
	}
	switch st.reason {
	case "machine":
		return locales.AccessPrivateMachine(machine)
	case "project":
		return locales.AccessPrivateProject(project)
	case "machine-and-project":
		return locales.AccessPrivateBoth(machine, project)
	case "machine-not-registered":
		return locales.AccessNotRegistered()
	}
	return locales.AccessPrivateDescription()
}

// confirmBody is what sharing with the team asks before it goes ahead.
func (st *accessState) confirmBody() string {
	machine := firstNonEmpty(st.machine, locales.AccessThisDevice())
	project := firstNonEmpty(st.project, locales.AccessThisProject())
	switch st.reason {
	case "project":
		return locales.AccessConfirmProject(project)
	case "machine-and-project":
		return locales.AccessConfirmBoth(machine, project)
	case "machine":
		return locales.AccessConfirmMachine(machine)
	}
	return locales.AccessConfirmGeneric()
}

// teamAction is the name of sharing with the team, by what it shares.
func (st *accessState) teamAction() string {
	switch st.reason {
	case "project":
		return locales.AccessShareProject()
	case "machine-and-project":
		return locales.AccessShareBoth()
	case "machine":
		return locales.AccessShareDevice()
	case "machine-not-registered":
		return locales.AccessRegisterDevice()
	}
	return locales.AccessShareWithTeam()
}

// publishedShare is whether the conversation has a live published link.
func (a *App) publishedShare(id string) bool {
	if a.convex == nil {
		return false
	}
	convex, ws := a.convex, a.workspaceID
	f := a.fetch("share/"+id, func(ctx context.Context) (any, error) {
		var entry *shareEntry
		err := convex.Query(ctx, "sessionSharing:getManagement", map[string]any{"workspaceId": ws, "rootSessionId": id}, &entry)
		return entry, err
	})
	entry, _ := f.value.(*shareEntry)
	return entry != nil && entry.Status == "active"
}

// headerPill is a status pill of the conversation header, as Lody's.
func headerPill(c *ui.Context, p *palette, key string, icon *ui.SVG, label string, open bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Height(28).Padding(0, 8).Gap(6).Radius(6).AlignItems(ui.Center).Shrink(0).Transition(colorFade)
	ink := p.Text.Alpha(0.8)
	switch {
	case open:
		b.Background(p.Text.Alpha(0.07))
		ink = p.Text
	case b.Hovered():
		b.Background(p.Text.Alpha(0.05))
		ink = p.Text
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(14, 14).TextColor(ink)
		ui.Text(c, label).FontSize(11.2).FontWeight(500).TextColor(ink).SingleLine()
	})
	return b
}

// archivedBadge marks an archived conversation in the header.
func archivedBadge(c *ui.Context, p *palette) {
	b := headerPill(c, p, "archived", iconArchive, locales.AccessArchived(), false).Label(locales.AccessArchived() + ": " + locales.AccessArchivedDescription())
	ui.TooltipBase(c, b, func(tip ui.Element) {
		tip.Padding(8, 10).Radius(8).MaxWidth(288).Background(p.Popover).Border(0.5, p.CardRim).Gap(2).
			Shadow(0, 6, 16, -4, ui.RGBA(0, 0, 0, 0.18)).
			Transition(ui.ElementTransition{Duration: durFast, Ease: easeStandard, Enter: &ui.Motion{Y: 4}, Exit: &ui.Motion{Y: 4}})
		tip.Children(func() {
			ui.Text(c, locales.AccessArchived()).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text)
			ui.Text(c, locales.AccessArchivedDescription()).FontSize(sizeFoot).TextColor(p.Secondary)
		})
	})
}

// accessControl is the header's Share pill and its panel.
func (a *App) accessControl(c *ui.Context, p *palette, s *catalog.Session) {
	st := a.sessionAccess(s)
	private := st != nil && st.visibility == "private"
	publish := !a.noCloud() && a.account != nil
	if !private && !publish {
		return
	}
	ap := &a.access
	shared := publish && a.publishedShare(s.ID)
	icon, label := iconShare, locales.ShareTitle()
	if private && !shared {
		icon = iconLockKeyhole
	}
	if shared {
		label = locales.AccessShared()
	}
	pill := headerPill(c, p, "access", icon, label, ap.open).Cursor(ui.CursorPointer).Label(label).OnClick(func() {
		ap.open = !ap.open
	})
	pill.Children(func() {
		ui.Icon(c, iconChevronDown).Size(12, 12).TextColor(p.Text).Opacity(0.45)
	})
	was := ap.open
	pop := popup(c, pill, &ap.open, func() {
		panel := ui.Column(c.Key("access-panel")).Width(296).Padding(8)
		panel.Children(func() {
			// The height follows the view; what overshoots is clipped.
			elastic := ui.Column(c.Key("elastic")).FillWidth().ClipY().Transition(elasticHeight)
			elastic.Children(func() {
				view := ui.Column(c.Key("view/" + ap.view)).FillWidth()
				if ap.moved {
					from := float32(-14)
					if ap.view == "publish" {
						from = 14
					}
					view.Transition(ui.ElementTransition{Colors: true, Duration: 260 * time.Millisecond, Ease: easeStandard, Enter: &ui.Motion{X: from}})
				}
				view.Children(func() {
					if ap.view == "publish" && publish {
						a.accessPublish(c, p, s)
						return
					}
					a.accessHome(c, p, s, st, shared, publish)
				})
			})
		})
	})
	if pop.Bounds().W > 0 || ap.open {
		pop.AttachTo(pill, ui.AnchorBottomEnd, ui.AnchorTopEnd).Margin(6, 0, 0, 0)
	}
	if was && !ap.open {
		// A publication under way keeps the panel open.
		if ap.publish.job != nil && ap.publish.job.busy {
			ap.open = true
			return
		}
		ap.view, ap.moved = "home", false
	}
}

// goAccess moves the panel to a view.
func (a *App) goAccess(view string, s *catalog.Session) {
	ap := &a.access
	ap.moved, ap.view = true, view
	if view == "publish" && (ap.publish.job == nil || ap.publish.job.root != s.ID || !ap.publish.open) {
		job := &shareJob{root: s.ID}
		ap.publish = shareDialog{open: true, job: job}
		a.loadShare(job)
	}
}

// accessHome is the panel's front: the reach, the audiences and the copies.
func (a *App) accessHome(c *ui.Context, p *palette, s *catalog.Session, st *accessState, shared, publish bool) {
	ap := &a.access
	private := st != nil && st.visibility == "private"
	teamScope := st != nil && (st.visibility == "private" || st.visibility == "team")
	current := "you"
	switch {
	case shared:
		current = "web"
	case st != nil && st.visibility == "team":
		current = "team"
	}
	title := locales.AccessPrivateToYou()
	desc := locales.AccessShareDescription()
	if st != nil {
		desc = st.description()
		switch st.visibility {
		case "team":
			title = locales.AccessSharedWithTeam()
		case "unknown":
			title = locales.AccessChecking()
		}
	}
	teamReachable := private && st.reason != "machine-not-registered" && st.canManage
	blocked := ""
	if private {
		switch {
		case !st.canManage:
			blocked = locales.AccessOnlyOwner()
		case st.reason == "machine-not-registered":
			blocked = locales.AccessRegisterDevice()
		}
	}
	heading, caption := title, firstNonEmpty(blocked, desc)
	if shared {
		heading, caption = locales.AccessAnyoneWithLink(), locales.AccessSharedDescription()
	}
	mark := map[string]*ui.SVG{"you": iconLockKeyhole, "team": iconUsers, "web": iconGlobe}[current]
	ui.Column(c.Key("access-home")).FillWidth().Children(func() {
		// A faint mark of the reach, landing from the corner that crops it.
		wm := ui.Icon(c.Key("mark/"+current), mark).Size(52, 52).Absolute().Top(-2).Right(-6).TextColor(p.Text).PassThrough()
		k := wm.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: 550 * time.Millisecond, Value: 1, Ease: easeStandard}})
		grow := 0.92 + 0.08*k
		wm.Translate(8*(1-k), 6*(1-k)).Rotate(-8*(1-k)).Scale(grow, grow).Opacity(0.07 * k)
		ui.Column(c).FillWidth().Gap(2).Padding(0, 32, 0, 4).Children(func() {
			ui.Text(c, heading).FontSize(sizeSub).FontWeight(600).TextColor(p.Text)
			ui.Text(c, caption).FontSize(sizeFoot).LineHeight(16.0 / 12).TextColor(p.Secondary)
		})
		var stops []accessStop
		if st == nil || teamScope {
			stops = append(stops, accessStop{"you", locales.AccessYou(), iconLockKeyhole, current != "you", ""})
		}
		if teamScope {
			tip := ""
			if teamReachable {
				tip = st.teamAction()
			}
			stops = append(stops, accessStop{"team", locales.AccessTeam(), iconUsers, current != "team" && !teamReachable, tip})
		}
		if publish {
			stops = append(stops, accessStop{"web", locales.AccessWeb(), iconGlobe, false, ""})
		}
		if len(stops) > 1 {
			ui.Column(c).FillWidth().Margin(8, 0, 0, 0).Children(func() {
				accessStops(c, p, stops, current, func(next string) {
					switch {
					case next == current:
					case next == "team":
						ap.open = false
						a.shareWithTeam(s, st)
					case next == "web":
						a.goAccess("publish", s)
					}
				})
			})
		}
		type row struct {
			key, label string
			icon       *ui.SVG
			keepOpen   bool
			run        func()
		}
		var rows []row
		if shared {
			rows = append(rows, row{"manage", locales.AccessManageLink(), iconGlobe, true, func() { a.goAccess("publish", s) }})
		}
		id := s.ID
		rows = append(rows,
			row{"copy-url", locales.AccessCopyURL(), iconLink, false, func() { a.copySessionLink(id) }},
		)
		ui.Box(c).FillWidth().Height(1).Margin(8, 4).Background(p.Border)
		ui.Column(c).FillWidth().Children(func() {
			for _, r := range rows {
				b := menuRow(c.Key(r.key), p, r.label).FillWidth().OnClick(func() {
					if !r.keepOpen {
						ap.open = false
					}
					r.run()
				})
				b.Children(func() {
					ui.Icon(c, r.icon).Size(14, 14).TextColor(p.Secondary)
					ui.Text(c, r.label).FontSize(sizeSub).TextColor(p.Text)
				})
			}
		})
	})
}

// accessPublish is the panel's publishing view: the way back, and the
// share editor.
func (a *App) accessPublish(c *ui.Context, p *palette, s *catalog.Session) {
	ap := &a.access
	d := &ap.publish
	job := d.job
	if job == nil || job.root != s.ID {
		a.goAccess("publish", s)
		job = d.job
	}
	busy := job.busy
	ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).Padding(0, 0, 12, 0).Children(func() {
		back := iconButton(c.Key("back"), p, iconChevronLeft, locales.NavBack(), p.Hover).Size(24, 24).Disabled(busy)
		back.OnClick(func() { a.goAccess("home", s) })
		ui.Text(c, locales.AccessPublishTitle()).FontSize(sizeSub).FontWeight(600).TextColor(p.Text)
	})
	step := a.shareStep(job, d)
	ui.Column(c.Key("publish-"+step)).FillWidth().Gap(12).Padding(0, 4).Children(func() { a.shareBody(c, p, job, d, step) })
	ui.Row(c).FillWidth().Wrap().Gap(8).Padding(12, 4, 4, 4).AlignItems(ui.Center).Children(func() { a.shareFooter(c, p, job, d, step) })
	if !d.open && !busy {
		// The editor closed: back to the front.
		d.open = true
		a.goAccess("home", s)
	}
}

// accessStop is an audience on the panel's strip.
type accessStop struct {
	id, label string
	icon      *ui.SVG
	disabled  bool
	tip       string
}

// accessStops is the strip of audiences: Lody's small tab list, stretched,
// its pill sliding to the audience that is.
func accessStops(c *ui.Context, p *palette, stops []accessStop, current string, pick func(string)) {
	track := ui.Row(c.Key("stops")).FillWidth().Padding(2).Radius(8).Background(p.Text.Alpha(0.05)).Label(locales.AccessWhoCanOpen())
	track.Children(func() {
		// What flows only sizes the track; the pill, then the tabs, lie
		// over it, as what is placed absolutely lies above what flows.
		ui.Box(c).FillWidth().Height(24)
		pill := ui.Column(c.Key("pill")).Absolute().Top(2).Height(24).Radius(6).Background(p.Popover).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.08))
		var first, sel ui.Rect
		ui.Row(c.Key("tabs")).Absolute().Top(2).Left(2).Right(2).Gap(2).Children(func() {
			for i, st := range stops {
				id := st.id
				tab := ui.ButtonBase(c.Key(st.id)).Grow(1).Height(24).Padding(0, 8).Radius(6).Gap(6).Center().Label(st.label).Disabled(st.disabled)
				if !st.disabled {
					tab.Cursor(ui.CursorPointer).OnClick(func() { pick(id) })
				}
				if st.tip != "" {
					lodyTooltip(c, p, tab, st.tip)
				}
				b := tab.Bounds()
				if i == 0 {
					first = b
				}
				if st.id == current {
					sel = b
				}
				ink := tab.AnimateColor("ink", map[bool]ui.Color{true: p.Text, false: p.Secondary}[st.id == current], ui.Animation{Duration: durFast, Ease: easeLody})
				if st.disabled && st.id != current {
					tab.Opacity(0.5)
				}
				tab.Children(func() {
					ui.Icon(c, st.icon).Size(13, 13).TextColor(ink)
					ui.Text(c, st.label).FontSize(sizeFoot).FontWeight(500).TextColor(ink).SingleLine()
				})
			}
		})
		if sel.W == 0 {
			pill.Width(0).Opacity(0)
			return
		}
		x := pill.AnimateWith("x", sel.X-first.X, durRegular, easeLody)
		w := pill.AnimateWith("w", sel.W, durRegular, easeLody)
		pill.Left(2 + x).Width(w)
	})
}

// shareWithTeam asks, then shares the conversation's machine and project
// with the team as its reach needs, and copies its link.
func (a *App) shareWithTeam(s *catalog.Session, st *accessState) {
	if st == nil || a.convex == nil {
		return
	}
	title := firstNonEmpty(s.Title, locales.UntitledSession())
	convex, ws, id := a.convex, a.workspaceID, s.ID
	machine, local, reason := st.machineID, st.localID, st.reason
	a.confirmSafe(locales.AccessConfirmTitle(title), st.confirmBody(), locales.AccessShareAndCopy(), func() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			var err error
			if reason == "machine" || reason == "machine-and-project" {
				err = convex.Mutation(ctx, "machines:setMachineSharedWithTeam", map[string]any{"workspaceId": ws, "machineId": machine, "sharedWithTeam": true}, nil)
			}
			if err == nil && local != "" && (reason == "project" || reason == "machine-and-project") {
				err = convex.Mutation(ctx, "localProjects:setLocalProjectSharedWithTeam",
					map[string]any{"workspaceId": ws, "machineId": machine, "localProjectId": local, "sharedWithTeam": true}, nil)
			}
			a.update(func() {
				if err != nil {
					a.toast("error", err.Error())
					return
				}
				for _, key := range []string{"machines", "projects"} {
					if f := a.fetches[ws+"|"+key]; f != nil {
						a.reload(f, nil)
					}
				}
				a.copySessionLink(id)
			})
		}()
	})
}
