package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/localplane"
	"github.com/lbr77/lody-mygo/internal/schedule"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// Schedules are Lody's scheduled tasks: a page listing the workspace's
// Schedule Registry, and over it a 720-wide modal with a schedule's
// settings and run history, or a new schedule's form. The machine a
// schedule names runs it; the app only writes its definition.

// schedState is the Schedules page.
type schedState struct {
	store *schedule.Store
	reg   schedule.Registry
	ready bool
	err   string

	query          string
	project        string // filter: "" for all, "\x00none", or a project's name
	status         string // filter: "", running, paused, attention or ended
	sort           string // next, recent, name or created
	open           string // the schedule in the modal, or newSchedule
	modal          bool
	tab            string // settings or history
	form           *schedForm
	saving, acting bool
	// The chat a schedule posts to, followed while its history shows.
	chatID   string
	chat     *session.Replica
	chatView *session.View
}

// newScheduleID marks the modal as a new schedule's form.
const newScheduleID = "\x00new"

// schedForm is the editor's draft.
type schedForm struct {
	id                string
	create            bool
	loading           bool
	owner             string
	title, prompt     string
	manual            bool
	rec               schedule.Recurrence
	timeText          string // the time of day being typed, as 09:00
	onceText          string // the once time being typed, as 2026-10-30 09:00
	stored            schedule.Trigger
	recEdited         bool
	machine, agent    string
	project           string // a catalog project id, or ""
	branch            string // a GitHub project's branch, as stored
	worktree          bool
	dest              string
	epoch             int
	session           string // an existing chat
	options           map[string]any
	memory            *schedule.Memory
	attempted         bool
	err               string
	enabled           bool
	destOpen, recOpen bool
}

// startSchedules follows a workspace's Schedule Registry, over local when
// set.
func (a *App) startSchedules(id string, local *localplane.Conn) {
	a.stopSchedules()
	st := &schedule.Store{Conn: a.conn, Local: local, Workspace: id}
	st.OnChange = func(r schedule.Registry) {
		a.update(func() {
			if a.sched.store == st {
				a.sched.reg, a.sched.ready, a.sched.err = r, true, ""
			}
		})
	}
	st.OnState = func(live bool, err error) {
		a.update(func() {
			if a.sched.store == st && !live && err != nil {
				a.sched.err = err.Error()
			}
		})
	}
	a.sched.store = st
	st.Start()
}

func (a *App) stopSchedules() {
	if a.sched.store != nil {
		a.sched.store.Stop()
	}
	a.sched.store, a.sched.reg, a.sched.ready, a.sched.err = nil, schedule.Registry{}, false, ""
}

func (a *App) userID() string {
	if a.account == nil {
		return ""
	}
	return a.account.User.ID
}

// Time words, as Lody's Intl formatters write them.

func zhLocale() bool { return strings.HasPrefix(i18n.Locale(), "zh") }

// deviceZone is this computer's IANA time zone.
func deviceZone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return strings.TrimPrefix(tz, ":")
	}
	if dest, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if _, zone, ok := strings.Cut(dest, "zoneinfo/"); ok {
			return zone
		}
	}
	return "UTC"
}

// machineZone is the clock a machine's schedules are read on.
func (a *App) machineZone(machine string) string {
	return firstNonEmpty(a.catalog.MachineZone[machine], deviceZone())
}

func loadZone(name string) *time.Location {
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.UTC
}

func clockText(h, m int) string {
	if zhLocale() {
		return fmt.Sprintf("%02d:%02d", h, m)
	}
	suffix := "AM"
	if h >= 12 {
		suffix = "PM"
	}
	h12 := h % 12
	if h12 == 0 {
		h12 = 12
	}
	return fmt.Sprintf("%d:%02d %s", h12, m, suffix)
}

var (
	weekdaysEn = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	weekdaysZh = []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	narrowEn   = []string{"S", "M", "T", "W", "T", "F", "S"}
	narrowZh   = []string{"日", "一", "二", "三", "四", "五", "六"}
)

func weekdayShort(d int) string {
	if zhLocale() {
		return weekdaysZh[d%7]
	}
	return weekdaysEn[d%7]
}

// listText joins items as Intl.ListFormat's short conjunction does.
func listText(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	head, last := strings.Join(items[:len(items)-1], ", "), items[len(items)-1]
	if zhLocale() {
		return strings.Join(items[:len(items)-1], "、") + "和" + last
	}
	if len(items) == 2 {
		return head + " & " + last
	}
	return head + ", & " + last
}

func clockOf(t time.Time) string { return clockText(t.Hour(), t.Minute()) }

// instantText is a date and time, as Intl's medium date and short time.
func instantText(t time.Time) string {
	if zhLocale() {
		return fmt.Sprintf("%d年%d月%d日 %s", t.Year(), t.Month(), t.Day(), clockOf(t))
	}
	return fmt.Sprintf("%s %d, %d, %s", t.Month().String()[:3], t.Day(), t.Year(), clockOf(t))
}

// upcomingText is a next run, calendar-relative: the time today,
// tomorrow's, a weekday's within the week, or a date's.
func upcomingText(at, now time.Time, loc *time.Location) string {
	at, now = at.In(loc), now.In(loc)
	day := func(t time.Time) string { return t.Format("2006-01-02") }
	clock := clockOf(at)
	switch {
	case day(at) == day(now):
		return clock
	case day(at) == day(now.AddDate(0, 0, 1)):
		if zhLocale() {
			return "明天 " + clock
		}
		return "tomorrow " + clock
	case at.Sub(now) < 6*24*time.Hour && at.After(now):
		return weekdayShort(int(at.Weekday())) + " " + clock
	}
	if zhLocale() {
		return fmt.Sprintf("%d月%d日 %s", at.Month(), at.Day(), clock)
	}
	return fmt.Sprintf("%s %d %s", at.Month().String()[:3], at.Day(), clock)
}

func durationText(n int, hours bool) string {
	s := strconv.Itoa(n)
	switch {
	case hours && n == 1:
		return locales.SchedHour(s)
	case hours:
		return locales.SchedHours(s)
	case n == 1:
		return locales.SchedMinute(s)
	}
	return locales.SchedMinutes(s)
}

// describeRecurrence is a rule in words.
func describeRecurrence(r schedule.Recurrence, zone string) string {
	switch r.Kind {
	case "minutes":
		return locales.SchedSumInterval(durationText(r.Every, false))
	case "hours":
		return locales.SchedSumInterval(durationText(r.Every, true))
	case "daily":
		return locales.SchedSumDaily(clockText(r.Hour, r.Minute))
	case "weekdays":
		return locales.SchedSumWeekdays(clockText(r.Hour, r.Minute))
	case "weekly":
		if len(r.Weekdays) == 0 {
			return locales.SchedRequireWeekday()
		}
		var days []string
		for _, d := range r.Weekdays {
			days = append(days, weekdayShort(d))
		}
		return locales.SchedSumWeekly(listText(days), clockText(r.Hour, r.Minute))
	case "monthly":
		if len(r.Days) == 0 {
			return locales.SchedRequireMonthDay()
		}
		var days []string
		for _, d := range r.Days {
			days = append(days, strconv.Itoa(d))
		}
		return locales.SchedSumMonthly(listText(days), clockText(r.Hour, r.Minute))
	case "once":
		return locales.SchedSumOnce(instantText(r.At.In(loadZone(zone))) + " (" + zone + ")")
	}
	rule := r.Trigger.Expression
	switch r.Trigger.Kind {
	case "interval":
		rule = durationText(int(r.Trigger.EveryMs/60_000), false)
	case "once":
		rule = r.Trigger.At
	}
	return locales.SchedSumUnsupported(rule)
}

// triggerZone is the zone a trigger is read in: its own for cron, else
// the machine's.
func triggerZone(t schedule.Trigger, machineZone string) string {
	if t.Kind == "cron" {
		return t.TimeZone
	}
	return machineZone
}

func describeTrigger(t schedule.Trigger, zone string) string {
	if t.Kind == "manual" {
		return locales.SchedManual()
	}
	return describeRecurrence(schedule.RecurrenceOf(t, zone), zone)
}

// schedStatus is what a schedule is doing, in words: a main line, an
// optional detail, and a tone and glyph only for what needs a look.
type schedStatus struct {
	label, detail string
	tone          string // normal, muted, progress or attention
	icon          *ui.SVG
	group         string // running, paused, attention or ended
}

func describeSchedState(row schedule.Row, rt *schedule.Runtime, zone string, now time.Time) schedStatus {
	loc := loadZone(zone)
	at := func(ms int64) string { return upcomingText(time.UnixMilli(ms), now, loc) }
	blocked := ""
	if rt != nil && rt.BlockedCode == "DEFINITION_NOT_COMMITTED" {
		blocked = locales.SchedErrNotCommitted()
	}
	if row.ScheduledMessage {
		state := "pending"
		if rt != nil && rt.MessageState != "" {
			state = rt.MessageState
		}
		switch state {
		case "sent":
			s := schedStatus{label: locales.SchedSent(), tone: "muted", icon: iconCheck, group: "ended"}
			if rt.LastDispatch != nil {
				s.detail = at(rt.LastDispatch.DispatchedAt)
			}
			return s
		case "cancelled":
			return schedStatus{label: locales.SchedCancelled(), tone: "muted", group: "ended"}
		case "failed":
			return schedStatus{label: locales.SchedNotSent(), detail: blocked, tone: "attention", icon: iconCircleAlert, group: "ended"}
		}
		if !row.Enabled {
			return schedStatus{label: locales.SchedCancelling(), tone: "muted", group: "paused"}
		}
		if t, err := time.Parse(time.RFC3339Nano, row.Trigger.At); err == nil {
			return schedStatus{label: locales.SchedSendsAt(at(t.UnixMilli())), tone: "normal", group: "running"}
		}
		return schedStatus{label: locales.SchedNotScheduled(), tone: "normal", group: "running"}
	}
	if !row.Enabled {
		return schedStatus{label: locales.SchedPaused(), tone: "muted", icon: iconPause, group: "paused"}
	}
	if rt != nil && rt.QueueState == "blocked" {
		return schedStatus{label: locales.SchedStateBlocked(), detail: blocked, tone: "attention", icon: iconCircleAlert, group: "attention"}
	}
	if rt != nil && rt.QueueState == "retrying" {
		return schedStatus{label: locales.SchedStateRetrying(), tone: "attention", icon: iconCircleAlert, group: "attention"}
	}
	if rt != nil && (rt.QueueState == "due" || rt.QueueState == "waiting_for_agent") {
		s := schedStatus{label: locales.SchedStateDue(), tone: "progress", icon: iconClock, group: "running"}
		if rt.QueueState == "waiting_for_agent" {
			s.label = locales.SchedStateWaiting()
		}
		if rt.NextScheduledAt > 0 {
			s.detail = at(rt.NextScheduledAt)
		}
		return s
	}
	if row.Trigger.Kind == "manual" {
		s := schedStatus{label: locales.SchedRunManually(), tone: "muted", group: "running"}
		if rt != nil && rt.LastDispatch != nil {
			s.detail = locales.SchedLastRan(at(rt.LastDispatch.DispatchedAt))
		}
		return s
	}
	if rt != nil && rt.NextScheduledAt > 0 {
		return schedStatus{label: locales.SchedRunsAt(at(rt.NextScheduledAt)), tone: "normal", group: "running"}
	}
	if row.Trigger.Kind == "once" && rt != nil && rt.LastDispatch != nil {
		return schedStatus{label: locales.SchedDone(), detail: at(rt.LastDispatch.DispatchedAt), tone: "muted", icon: iconCheck, group: "ended"}
	}
	return schedStatus{label: locales.SchedNotScheduled(), detail: locales.SchedAwaitingMachine(), tone: "muted", group: "running"}
}

func (s schedStatus) ink(p *palette) ui.Color {
	switch s.tone {
	case "muted":
		return p.Secondary
	case "progress":
		return p.Primary
	case "attention":
		return p.Warning
	}
	return p.Text
}

// schedEntry is a row as the list shows it.
type schedEntry struct {
	row     schedule.Row
	rt      *schedule.Runtime
	status  schedStatus
	project string // its project's name, or ""
	zone    string
}

func (a *App) schedEntries(now time.Time) []schedEntry {
	var out []schedEntry
	for _, row := range a.sched.reg.Rows {
		rt := a.sched.reg.Runtime[row.ScheduleID]
		zone := triggerZone(row.Trigger, a.machineZone(row.MachineID))
		e := schedEntry{row: row, rt: rt, status: describeSchedState(row, rt, zone, now), zone: zone}
		switch row.ProjectKind {
		case "local":
			if pr := a.catalog.Project(row.MachineID + ":local:" + row.ProjectKey); pr != nil {
				e.project = pr.Name
			} else {
				e.project = row.ProjectKey
			}
		case "github":
			e.project = row.ProjectKey
		}
		out = append(out, e)
	}
	return out
}

// schedulesPage is the list.
func (a *App) schedulesPage(c *ui.Context, p *palette) {
	st := &a.sched
	now := time.Now()
	entries := a.schedEntries(now)
	ui.Row(c).Height(52).Shrink(0).AlignItems(ui.Center).Padding(0, 24, 0, a.barInset(24)).Gap(8).DragWindow().Children(func() {
		if a.sidebarHidden {
			iconButton(c, p, iconSidebar, locales.SidebarToggle(), p.Hover).OnClick(func() { a.sidebarHidden = false })
		}
		ui.Text(c, locales.SchedTitle()).FontSize(16).FontWeight(600).TextColor(p.Strong).Grow(1).SingleLine()
		ui.Row(c).Width(224).Height(28).Padding(0, 8).Gap(6).Radius(8).AlignItems(ui.Center).Border(1, p.Border).Background(p.Canvas).Children(func() {
			ui.Icon(c, iconSearch).Size(14, 14).TextColor(p.Secondary)
			ui.TextInputBase(c, &st.query).Placeholder(locales.SchedSearch()).Label(locales.SchedSearch()).FontSize(sizeFoot).TextColor(p.Text).Grow(1).MinWidth(0)
		})
		newBtn := ui.ButtonBase(c).Height(28).Padding(0, 10).Radius(8).Gap(6).Background(p.Text).Cursor(ui.CursorPointer).Label(locales.SchedNew()).OnClick(a.newSchedule)
		pressable(newBtn, 0.97)
		newBtn.Children(func() {
			ui.Icon(c, iconPlus).Size(14, 14).TextColor(p.Canvas)
			ui.Text(c, locales.SchedNew()).FontSize(sizeFoot).FontWeight(500).TextColor(p.Canvas)
		})
	})
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, 24, 32, 24).Children(func() {
		if st.err != "" {
			msg := locales.SchedLoadError()
			if len(st.reg.Rows) > 0 {
				msg = locales.SchedSyncError()
			}
			ui.Text(c, msg).FontSize(sizeBody).TextColor(p.Danger).Padding(24, 0, 8, 0)
		}
		switch {
		case !st.ready:
			ui.Column(c).FillWidth().Margin(18, 0, 0, 0).Radius(12).Border(0.5, p.Border).Background(p.Popover).Children(func() {
				for i := 0; i < 3; i++ {
					ui.Row(c.Key(i)).FillWidth().Padding(16).Children(func() {
						ui.Box(c).Width(192).Height(10).Radius(5).Background(p.Text.Alpha(0.07))
						ui.Spacer(c)
						ui.Box(c).Width(96).Height(10).Radius(5).Background(p.Text.Alpha(0.07))
					})
				}
			})
			return
		case len(entries) == 0:
			ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(64, 24).Children(func() {
				ui.Icon(c, iconCalendarClock).Size(20, 20).TextColor(p.Secondary)
				ui.Text(c, locales.SchedEmpty()).FontSize(sizeBody).TextColor(p.Text)
				ui.Text(c, locales.SchedEmptyHelp()).FontSize(sizeSub).TextColor(p.Secondary).TextAlign(ui.Center).MaxWidth(336)
				outlineButton(c, p, iconPlus, locales.SchedNew()).Margin(8, 0, 0, 0).OnClick(a.newSchedule)
			})
			return
		}
		a.schedFilters(c, p, entries)
		visible := a.schedFiltered(entries)
		var tasks, messages []schedEntry
		total := map[bool]int{}
		for _, e := range entries {
			total[e.row.ScheduledMessage]++
		}
		for _, e := range visible {
			if e.row.ScheduledMessage {
				messages = append(messages, e)
			} else {
				tasks = append(tasks, e)
			}
		}
		a.schedSection(c, p, "tasks", locales.SchedSectionTasks(), locales.SchedColResults(), tasks, total[false], now)
		if total[true] > 0 {
			a.schedSection(c, p, "messages", locales.SchedSectionMessages(), locales.SchedColSendsTo(), messages, total[true], now)
		}
	})
	a.schedDialog(c, p)
}

func (a *App) schedFiltered(entries []schedEntry) []schedEntry {
	st := &a.sched
	q := strings.ToLower(strings.TrimSpace(st.query))
	var out []schedEntry
	for _, e := range entries {
		if q != "" && !strings.Contains(strings.ToLower(e.row.Title), q) {
			continue
		}
		switch {
		case st.project == "\x00none" && e.project != "":
			continue
		case st.project != "" && st.project != "\x00none" && e.project != st.project:
			continue
		}
		if st.status != "" && e.status.group != st.status {
			continue
		}
		out = append(out, e)
	}
	less := map[string]func(x, y schedEntry) int{
		"next": func(x, y schedEntry) int { return cmpInt(nextAt(x), nextAt(y)) },
		"recent": func(x, y schedEntry) int {
			return cmpInt(lastAt(y), lastAt(x))
		},
		"name": func(x, y schedEntry) int {
			return strings.Compare(strings.ToLower(x.row.Title), strings.ToLower(y.row.Title))
		},
		"created": func(x, y schedEntry) int { return cmpInt(y.row.CreatedAt, x.row.CreatedAt) },
	}[firstNonEmpty(st.sort, "next")]
	sort.SliceStable(out, func(i, j int) bool {
		if c := less(out[i], out[j]); c != 0 {
			return c < 0
		}
		return out[i].row.Title < out[j].row.Title
	})
	return out
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func nextAt(e schedEntry) int64 {
	if e.rt != nil && e.rt.NextScheduledAt > 0 {
		return e.rt.NextScheduledAt
	}
	return 1 << 62
}

func lastAt(e schedEntry) int64 {
	if e.rt != nil && e.rt.LastDispatch != nil {
		return e.rt.LastDispatch.DispatchedAt
	}
	return 0
}

// schedFilters is the toolbar: project and status filters, and the sort.
func (a *App) schedFilters(c *ui.Context, p *palette, entries []schedEntry) {
	st := &a.sched
	pill := func(key string, icon *ui.SVG, label string, active bool, menu func(m *ui.Menu)) {
		b := ui.ButtonBase(c.Key(key)).Height(26).Padding(0, 9).Radius(13).Gap(5).Border(1, p.Border).Cursor(ui.CursorPointer).Label(label).Transition(colorFade)
		if active {
			b.Background(p.Text.Alpha(0.07))
		} else if b.Hovered() {
			b.Background(p.Hover)
		}
		b.Menu(menu)
		b.Children(func() {
			ui.Icon(c, icon).Size(13, 13).TextColor(p.Secondary)
			ui.Text(c, label).FontSize(sizeFoot).TextColor(p.Text).SingleLine()
			ui.Icon(c, iconChevronDown).Size(12, 12).TextColor(p.Tertiary)
		})
	}
	ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Padding(2, 0, 4, 0).Children(func() {
		names := map[string]int{}
		none := 0
		for _, e := range entries {
			if e.project == "" {
				none++
			} else {
				names[e.project]++
			}
		}
		var sorted []string
		for n := range names {
			sorted = append(sorted, n)
		}
		sort.Strings(sorted)
		projectLabel := locales.SchedAllProjects()
		switch {
		case st.project == "\x00none":
			projectLabel = locales.SchedNoProject()
		case st.project != "":
			projectLabel = st.project
		}
		pill("project", iconFolder, projectLabel, st.project != "", func(m *ui.Menu) {
			if m.Item(fmt.Sprintf("%s  %d", locales.SchedAllProjects(), len(entries))).Radio(st.project == "").Chosen() {
				st.project = ""
			}
			for _, n := range sorted {
				if m.Item(fmt.Sprintf("%s  %d", n, names[n])).Radio(st.project == n).Chosen() {
					st.project = n
				}
			}
			if m.Item(fmt.Sprintf("%s  %d", locales.SchedNoProject(), none)).Radio(st.project == "\x00none").Chosen() {
				st.project = "\x00none"
			}
		})
		statuses := []option{{"", locales.SchedAllStatuses()}, {"running", locales.SchedFilterActive()}, {"paused", locales.SchedPaused()},
			{"attention", locales.SchedStateBlocked()}, {"ended", locales.SchedFilterEnded()}}
		statusLabel := statuses[0].label
		for _, o := range statuses {
			if o.id == st.status {
				statusLabel = o.label
			}
		}
		pill("status", iconList, statusLabel, st.status != "", func(m *ui.Menu) {
			for _, o := range statuses {
				n := 0
				for _, e := range entries {
					if o.id == "" || e.status.group == o.id {
						n++
					}
				}
				if m.Item(fmt.Sprintf("%s  %d", o.label, n)).Radio(st.status == o.id).Chosen() {
					st.status = o.id
				}
			}
		})
		ui.Spacer(c)
		sorts := []option{{"next", locales.SchedSortNext()}, {"recent", locales.SchedSortRecent()}, {"name", locales.SchedSortName()}, {"created", locales.SchedSortNewest()}}
		sortLabel := sorts[0].label
		for _, o := range sorts {
			if o.id == st.sort {
				sortLabel = o.label
			}
		}
		pill("sort", iconArrowUpDown, sortLabel, false, func(m *ui.Menu) {
			for _, o := range sorts {
				if m.Item(o.label).Radio(firstNonEmpty(st.sort, "next") == o.id).Chosen() {
					st.sort = o.id
				}
			}
		})
	})
}

// The list's columns: name, destination, status and actions.
const (
	schedDestWidth   = 220
	schedStatusWidth = 168
	schedActionWidth = 64
)

func (a *App) schedSection(c *ui.Context, p *palette, key, title, destTitle string, entries []schedEntry, total int, now time.Time) {
	ui.Column(c.Key(key)).FillWidth().Margin(18, 0, 0, 0).Children(func() {
		ui.Row(c).MinHeight(28).Padding(0, 0, 6, 4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(sizeFoot + 0.5).FontWeight(500).TextColor(p.Text)
			ui.Text(c, strconv.Itoa(total)).FontSize(sizeFoot+0.5).TextColor(p.Secondary).Margin(0, 0, 0, 6)
		})
		box := ui.Column(c).FillWidth().Radius(12).Border(0.5, p.Border).Background(p.Popover).Clip().Dividers(0.5, p.Border)
		if !p.Dark {
			box.Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.06))
		}
		box.Children(func() {
			ui.Row(c).FillWidth().Padding(8, 10, 6, 16).Gap(16).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.SchedColName()).FontSize(sizeCaption).TextColor(p.Secondary).Grow(1)
				ui.Text(c, destTitle).FontSize(sizeCaption).TextColor(p.Secondary).Width(schedDestWidth)
				ui.Text(c, locales.SchedColStatus()).FontSize(sizeCaption).TextColor(p.Secondary).Width(schedStatusWidth).TextAlign(ui.End)
				ui.Box(c).Width(schedActionWidth)
			})
			if len(entries) == 0 {
				ui.Text(c, locales.SchedEmpty()).FontSize(sizeFoot).TextColor(p.Secondary).Padding(16)
			}
			for _, e := range entries {
				a.schedRow(c, p, e)
			}
		})
	})
}

// schedAction is one thing a schedule's menus offer.
type schedAction struct {
	key, label string
	icon       *ui.SVG
	run        func()
	danger     bool
}

func (a *App) schedActions(row schedule.Row, rt *schedule.Runtime) []schedAction {
	id := row.ScheduleID
	owner := row.OwnerID == a.userID()
	out := []schedAction{{key: "open", label: locales.SchedOpen(), icon: iconExternal, run: func() { a.openSchedule(id) }}}
	if !row.ScheduledMessage && owner && a.canRunSchedules(row.MachineID) {
		out = append(out, schedAction{key: "run", label: locales.SchedRunNow(), icon: iconPlay, run: func() { a.runSchedule(row) }})
	}
	if !row.ScheduledMessage && row.Trigger.Kind != "manual" && owner {
		act := schedAction{key: "toggle", label: locales.SchedPause(), icon: iconPause, run: func() { a.toggleSchedule(row) }}
		if !row.Enabled {
			act.label, act.icon = locales.SchedResume(), iconUndo
		}
		out = append(out, act)
	}
	chat := ""
	switch row.Destination.Kind {
	case schedule.OwnSession:
		epoch := 0
		if row.Destination.Epoch != nil {
			epoch = *row.Destination.Epoch
		}
		chat = schedule.OwnSessionID(id, epoch)
	case schedule.ExistingSession:
		chat = row.Destination.SessionID
	}
	if s := a.catalog.Session(chat); s != nil {
		out = append(out, schedAction{key: "chat", label: locales.SchedOpenChat(s.Title), icon: iconMessageSquare, run: func() { a.closeSchedule(); a.open(chat) }})
	} else if rt != nil && rt.LastDispatch != nil && rt.LastDispatch.SessionID != "" {
		last := rt.LastDispatch.SessionID
		out = append(out, schedAction{key: "last", label: locales.SchedLastRun(), icon: iconHistory, run: func() { a.closeSchedule(); a.open(last) }})
	}
	if owner {
		out = append(out, schedAction{key: "delete", label: locales.SchedDelete(), icon: iconTrash, danger: true, run: func() { a.deleteSchedule(row) }})
	}
	return out
}

func schedMenu(m *ui.Menu, actions []schedAction) {
	for _, act := range actions {
		if act.danger {
			m.Separator()
		}
		if m.Item(act.label).Chosen() {
			act.run()
		}
	}
}

// canRunSchedules reports whether a machine's CLI runs schedules.
func (a *App) canRunSchedules(machine string) bool {
	caps, ok := a.catalog.MachineCaps[machine]
	return !ok || caps["schedules"] >= 2
}

func (a *App) schedRow(c *ui.Context, p *palette, e schedEntry) {
	row := e.row
	actions := a.schedActions(row, e.rt)
	primary := "toggle"
	switch {
	case row.ScheduledMessage && e.status.group == "ended":
		primary = "delete"
	case row.ScheduledMessage:
		primary = ""
	case row.Trigger.Kind == "manual":
		primary = "run"
	}
	r := ui.Row(c.Key(row.ScheduleID)).FillWidth().MinHeight(58).Padding(8, 10, 8, 16).Gap(16).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Label(row.Title).Transition(colorFade).OnClick(func() { a.openSchedule(row.ScheduleID) })
	hovered := r.Hovered()
	if a.sched.modal && a.sched.open == row.ScheduleID {
		r.Background(p.Text.Alpha(0.07))
	} else if hovered {
		r.Background(p.Text.Alpha(0.04))
	}
	r.ContextMenu(func(m *ui.Menu) { schedMenu(m, actions) })
	r.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			name := ui.Text(c, row.Title).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine()
			if e.status.group == "ended" {
				name.FontWeight(400).TextColor(p.Secondary)
			}
			ui.Row(c).MinWidth(0).AlignItems(ui.Center).Children(func() {
				sep := func() {
					ui.Text(c, "·").FontSize(sizeCaption).TextColor(p.Secondary.Alpha(0.55)).Padding(0, 5).Shrink(0)
				}
				// The rule keeps its words longest; agent and machine give way.
				ui.Text(c, describeTrigger(row.Trigger, e.zone)).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine().MinWidth(0).Shrink(0.2)
				sep()
				ui.Text(c, firstNonEmpty(a.agentName(row.MachineID, row.AgentConfigID), row.AgentConfigID)).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine().MinWidth(0)
				sep()
				if !a.presence.IsOnline(row.MachineID) {
					ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Tooltip(locales.SchedMachineOffline(a.machineLabel(row.MachineID))).Children(func() {
						ui.Icon(c, iconCloudOff).Size(14, 14).TextColor(p.Warning)
						ui.Text(c, locales.SchedMachineOffline(a.machineLabel(row.MachineID))).FontSize(sizeCaption).TextColor(p.Warning).SingleLine()
					})
				} else {
					ui.Text(c, a.machineLabel(row.MachineID)).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine().MinWidth(0)
				}
			})
		})
		ui.Row(c).Width(schedDestWidth).Gap(6).AlignItems(ui.Center).Children(func() { a.schedDestination(c, p, e) })
		a.schedStatusView(c, p, e.status).Width(schedStatusWidth)
		ui.Row(c).Width(schedActionWidth).Gap(2).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			for _, act := range actions {
				if act.key != primary {
					continue
				}
				b := iconButton(c.Key("primary"), p, act.icon, act.label, p.Hover).Tooltip(act.label).OnClick(act.run)
				if !hovered {
					b.Opacity(0)
				}
			}
			iconButton(c.Key("more"), p, iconMore, locales.SchedMore(), p.Hover).Menu(func(m *ui.Menu) { schedMenu(m, actions) })
		})
	})
}

func (a *App) schedDestination(c *ui.Context, p *palette, e schedEntry) {
	row := e.row
	if row.Destination.Kind == schedule.NewSession || row.Destination.Kind == "" {
		ui.Icon(c, iconFolder).Size(14, 14).TextColor(p.Secondary)
		ui.Text(c, locales.SchedDestNew()+" · "+firstNonEmpty(e.project, locales.SchedChatOnly())).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine().MinWidth(0)
		return
	}
	chat := row.Destination.SessionID
	if row.Destination.Kind == schedule.OwnSession {
		epoch := 0
		if row.Destination.Epoch != nil {
			epoch = *row.Destination.Epoch
		}
		chat = schedule.OwnSessionID(row.ScheduleID, epoch)
	}
	if s := a.catalog.Session(chat); s != nil {
		b := ui.ButtonBase(c).Height(26).Padding(0, 8).Margin(0, 0, 0, -8).Radius(6).Gap(6).MinWidth(0).Cursor(ui.CursorPointer).
			Label(locales.SchedOpenChat(s.Title)).Tooltip(locales.SchedOpenChat(s.Title)).OnClick(func() { a.closeSchedule(); a.open(chat) })
		if b.Hovered() {
			b.Background(p.Hover)
		}
		b.Children(func() {
			ui.Icon(c, iconMessageSquare).Size(14, 14).TextColor(p.Secondary)
			ui.Text(c, s.Title).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine().MinWidth(0)
			ui.Icon(c, iconArrowUpRight).Size(14, 14).TextColor(p.Tertiary)
		})
		return
	}
	ui.Icon(c, iconMessageSquare).Size(14, 14).TextColor(p.Secondary)
	label := locales.SchedDestExisting()
	if row.Destination.Kind == schedule.OwnSession {
		label = locales.SchedDestOwn() + " · " + locales.SchedDestNotCreated()
	}
	ui.Text(c, label).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine().MinWidth(0)
}

func (a *App) schedStatusView(c *ui.Context, p *palette, s schedStatus) ui.Element {
	return ui.Column(c).MinWidth(0).AlignItems(ui.End).Children(func() {
		ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
			if s.icon != nil {
				ui.Icon(c, s.icon).Size(14, 14).TextColor(s.ink(p))
			}
			ui.Text(c, s.label).FontSize(sizeFoot).TextColor(s.ink(p)).SingleLine()
		})
		if s.detail != "" {
			ui.Text(c, s.detail).FontSize(sizeCaption - 0.5).TextColor(p.Secondary).SingleLine().MaxWidth(schedStatusWidth)
		}
	})
}

// Opening, saving and acting on schedules.

func (a *App) closeSchedule() {
	a.sched.modal = false
	a.sched.form = nil
	a.closeSchedChat()
}

// newSchedule opens a new schedule's form: every day at 09:00, on the
// machine and with the Agent New chat has.
func (a *App) newSchedule() {
	machine := ""
	for _, id := range a.catalog.MachineIDs {
		if owner := a.catalog.MachineOwner[id]; (owner == "" || owner == a.userID()) && len(a.catalog.Agents[id]) > 0 {
			if machine == "" || id == a.newChat.machine {
				machine = id
			}
		}
	}
	f := &schedForm{id: uuid.NewString(), create: true, dest: schedule.NewSession, enabled: true, machine: machine}
	zone := a.machineZone(machine)
	f.rec = schedule.Recurrence{Kind: "daily", Hour: 9, Minute: 0, TimeZone: zone}
	f.timeText = "09:00"
	if machine == a.newChat.machine {
		f.agent, f.project, f.worktree = a.newChat.agent, a.newChat.project, a.newChat.worktree
		a.models["sched"], a.modes["sched"] = a.models[""], a.modes[""]
		a.runValues["sched"] = cloneValues(a.runValues[""])
	} else {
		delete(a.models, "sched")
		delete(a.modes, "sched")
		delete(a.runValues, "sched")
	}
	if a.agent(machine, f.agent) == nil {
		f.agent = ""
		if agents := a.catalog.Agents[machine]; len(agents) > 0 {
			f.agent = agents[0].ID
		}
	}
	a.sched.open, a.sched.modal, a.sched.tab, a.sched.form = newScheduleID, true, "settings", f
}

func cloneValues(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// openSchedule opens a schedule's settings, reading its document.
func (a *App) openSchedule(id string) {
	st := &a.sched
	row := st.reg.Row(id)
	if row == nil || st.store == nil {
		return
	}
	f := &schedForm{id: id, loading: true, owner: row.OwnerID, title: row.Title, enabled: row.Enabled, machine: row.MachineID, agent: row.AgentConfigID}
	st.open, st.modal, st.tab, st.form = id, true, "settings", f
	store := st.store
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		doc, err := store.Open(ctx, id)
		var d schedule.Document
		if err == nil {
			d = doc.Read()
			doc.Close()
		}
		a.update(func() {
			if st.form != f {
				return
			}
			f.loading = false
			if err != nil {
				f.err = err.Error()
				return
			}
			a.fillForm(f, d)
		})
	}()
}

func (a *App) fillForm(f *schedForm, d schedule.Document) {
	def := d.Definition
	f.title, f.prompt, f.owner, f.enabled = def.Title, d.Prompt, def.OwnerID, def.Enabled
	f.machine, f.agent = def.MachineID, def.Agent.AgentConfigID
	f.stored = def.Trigger
	f.manual = def.Trigger.Kind == "manual"
	zone := a.machineZone(def.MachineID)
	f.rec = schedule.Recurrence{Kind: "daily", Hour: 9, TimeZone: zone}
	if !f.manual {
		f.rec = schedule.RecurrenceOf(def.Trigger, zone)
	}
	f.syncTexts(zone)
	a.models["sched"], a.modes["sched"] = def.Agent.ModelID, def.Agent.ModeID
	a.runValues["sched"] = cloneValues(def.Agent.ConfigOptionValues)
	f.options, f.memory = cloneValues(def.Agent.ConfigOptionValues), def.Agent.Memory
	f.project, f.worktree = "", false
	if pr := def.Project; pr != nil {
		switch pr.Kind {
		case "local":
			f.project = def.MachineID + ":local:" + pr.LocalProjectID
			f.worktree = pr.UseWorktree != nil && *pr.UseWorktree
		case "github":
			f.project, f.branch = "github:"+pr.RepoFullName, pr.Branch
		}
	}
	f.dest = firstNonEmpty(def.Destination.Kind, schedule.NewSession)
	if def.Destination.Epoch != nil {
		f.epoch = *def.Destination.Epoch
	}
	f.session = def.Destination.SessionID
}

// syncTexts sets the typed time fields from the rule.
func (f *schedForm) syncTexts(zone string) {
	f.timeText = fmt.Sprintf("%02d:%02d", f.rec.Hour, f.rec.Minute)
	if !f.rec.At.IsZero() {
		f.onceText = f.rec.At.In(loadZone(zone)).Format("2006-01-02 15:04")
	}
}

// trigger is what the form will save, or why it can't yet.
func (a *App) formTrigger(f *schedForm, now time.Time) (schedule.Trigger, error) {
	if f.manual {
		return schedule.Trigger{Kind: "manual"}, nil
	}
	// An untouched rule is left exactly as stored.
	if !f.recEdited && f.stored.Kind != "" && f.stored.Kind != "manual" {
		return f.stored, nil
	}
	r := f.rec
	r.TimeZone = a.machineZone(f.machine)
	if r.Kind == "once" {
		at, err := time.ParseInLocation("2006-01-02 15:04", strings.TrimSpace(f.onceText), loadZone(r.TimeZone))
		if err != nil {
			return schedule.Trigger{}, schedule.ErrNoTime
		}
		r.At = at
	}
	return schedule.TriggerOf(r, now)
}

func (a *App) saveSchedule() {
	st := &a.sched
	f := st.form
	if f == nil || st.saving {
		return
	}
	f.attempted, f.err = true, ""
	now := time.Now()
	trigger, err := a.formTrigger(f, now)
	agent := a.agent(f.machine, f.agent)
	switch {
	case strings.TrimSpace(f.title) == "" || strings.TrimSpace(f.prompt) == "" || err != nil:
		return
	case agent == nil:
		f.err = locales.SchedRequireAgent()
		return
	}
	model := a.models["sched"]
	values := cloneValues(f.options)
	for k, v := range a.chosenRunValues("sched", agent.ID, model) {
		if values == nil {
			values = map[string]any{}
		}
		values[k] = v
	}
	d := schedule.Draft{
		Title: f.title, Prompt: f.prompt, MachineID: f.machine, Trigger: trigger,
		Agent: schedule.Agent{AgentConfigID: agent.ID, ModelID: model, ModeID: a.modes["sched"], ConfigOptionValues: values, Memory: f.memory},
	}
	switch f.dest {
	case schedule.OwnSession:
		epoch := f.epoch
		d.Destination = schedule.Destination{Kind: schedule.OwnSession, Epoch: &epoch}
	case schedule.ExistingSession:
		if f.session == "" {
			f.err = locales.SchedDestChoose()
			return
		}
		d.Destination = schedule.Destination{Kind: schedule.ExistingSession, SessionID: f.session}
	default:
		d.Destination = schedule.Destination{Kind: schedule.NewSession}
		if local, ok := strings.CutPrefix(f.project, f.machine+":local:"); ok {
			d.Project = &schedule.Project{Kind: "local", LocalProjectID: local}
			if f.worktree {
				on := true
				d.Project.UseWorktree = &on
			}
		} else if repo, ok := strings.CutPrefix(f.project, "github:"); ok {
			// Lody writes a picked repository on its default, main; an
			// unchanged one keeps the branch it has.
			d.Project = &schedule.Project{Kind: "github", RepoFullName: repo, Branch: firstNonEmpty(f.branch, "main")}
		}
	}
	if st.store == nil {
		return
	}
	st.saving = true
	store, user, id, create := st.store, a.userID(), f.id, f.create
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := store.Save(ctx, id, create, d, user, now)
		a.update(func() {
			st.saving = false
			if st.form != f {
				return
			}
			if err != nil {
				f.err = err.Error()
				return
			}
			// Saved, it is a schedule like the others.
			f.create, f.stored, f.recEdited, f.owner, f.attempted = false, trigger, false, user, false
			st.open = id
		})
	}()
}

// schedAct runs a write on a schedule, reporting a failure.
func (a *App) schedAct(do func(ctx context.Context, store *schedule.Store) error, done func()) {
	st := &a.sched
	if st.store == nil || st.acting {
		return
	}
	st.acting = true
	store := st.store
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := do(ctx, store)
		a.update(func() {
			st.acting = false
			if err != nil {
				a.toast("warning", err.Error())
				return
			}
			if done != nil {
				done()
			}
		})
	}()
}

func (a *App) toggleSchedule(row schedule.Row) {
	user := a.userID()
	a.schedAct(func(ctx context.Context, s *schedule.Store) error {
		return s.SetEnabled(ctx, row.ScheduleID, !row.Enabled, user, time.Now())
	}, func() {
		if f := a.sched.form; f != nil && f.id == row.ScheduleID {
			f.enabled = !row.Enabled
		}
	})
}

// pausableSchedule is the open schedule when Pause Schedule applies: its
// owner's, running, and a task.
func (a *App) pausableSchedule() *schedule.Row {
	if !a.sched.modal || a.page != "schedules" {
		return nil
	}
	row := a.sched.reg.Row(a.sched.open)
	if row == nil || row.OwnerID != a.userID() || !row.Enabled || row.ScheduledMessage {
		return nil
	}
	return row
}

// confirmRun asks before a run from the open schedule, which takes what
// was last saved, not the form's edits.
func (a *App) confirmRun(row schedule.Row) {
	a.confirmSafe(locales.SchedRunNow(), locales.SchedRunNowHelp(), locales.SchedConfirm(), func() { a.runSchedule(row) })
}

func (a *App) runSchedule(row schedule.Row) {
	user := a.userID()
	a.schedAct(func(ctx context.Context, s *schedule.Store) error {
		return s.RunNow(ctx, row.ScheduleID, user, time.Now())
	}, func() { a.toast("success", locales.SchedRunStarted(row.Title)) })
}

func (a *App) deleteSchedule(row schedule.Row) {
	user := a.userID()
	a.confirm(locales.SchedDeleteTitle(row.Title), locales.SchedDeleteHelp(), locales.SchedDelete(), func() {
		a.schedAct(func(ctx context.Context, s *schedule.Store) error {
			return s.Delete(ctx, row.ScheduleID, user, time.Now())
		}, func() {
			if a.sched.open == row.ScheduleID {
				a.closeSchedule()
			}
		})
	})
}

// The modal.

func (a *App) schedDialog(c *ui.Context, p *palette) {
	st := &a.sched
	if !st.modal || st.form == nil {
		return
	}
	f := st.form
	w, h := c.Size()
	open := st.modal
	ui.DialogBase(c, &open, func(backdrop, panel ui.Element) {
		sheetIn(backdrop, panel)
		backdrop.Background(ui.RGBA(0, 0, 0, 0.22))
		panel.Width(min(720, w-48)).MaxHeight(h-64).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Clip().
			Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Column(c).FillWidth().MaxHeight(h - 64).Children(func() {
			row := st.reg.Row(f.id)
			a.schedHeader(c, p, f, row)
			if row != nil && !f.create && !row.ScheduledMessage {
				ui.Row(c).FillWidth().Padding(0, 24, 10, 24).Shrink(0).Children(func() {
					segmented(c, p, "schedTab", []option{{"settings", locales.SchedTabSettings()}, {"history", locales.SchedHistory()}}, st.tab, func(v string) {
						st.tab = v
						if v != "history" {
							a.closeSchedChat()
						}
					})
				})
			}
			ui.Box(c).FillWidth().Height(0.5).Background(p.Border).Shrink(0)
			ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Padding(16, 24, 0, 24).Children(func() {
				if row != nil && row.ScheduledMessage {
					a.schedMessageView(c, p, f, *row)
					return
				}
				if st.tab == "history" && row != nil && !f.create {
					a.schedHistory(c, p, *row)
					return
				}
				a.schedFormView(c, p, f, row)
			})
		})
	})
	if !open {
		a.closeSchedule()
	}
}

func (a *App) schedHeader(c *ui.Context, p *palette, f *schedForm, row *schedule.Row) {
	ui.Row(c).FillWidth().Padding(18, 16, 12, 24).Gap(12).AlignItems(ui.Start).Shrink(0).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			kind := locales.SchedKindTask()
			if row != nil && row.ScheduledMessage {
				kind = locales.SchedKindMessage()
			}
			ui.Text(c, kind).FontSize(sizeCaption).TextColor(p.Secondary)
			title := locales.SchedNew()
			if !f.create {
				title = firstNonEmpty(f.title, row.Title)
			}
			ui.Text(c, title).FontSize(18).FontWeight(600).TextColor(p.Strong).SingleLine()
			if row != nil && !f.create {
				zone := triggerZone(row.Trigger, a.machineZone(row.MachineID))
				s := describeSchedState(*row, a.sched.reg.Runtime[row.ScheduleID], zone, time.Now())
				ui.Row(c).Gap(5).AlignItems(ui.Center).Margin(2, 0, 0, 0).Children(func() {
					if s.icon != nil {
						ui.Icon(c, s.icon).Size(14, 14).TextColor(s.ink(p))
					}
					ui.Text(c, s.label).FontSize(sizeFoot).TextColor(s.ink(p))
					if s.detail != "" {
						ui.Text(c, "· "+s.detail).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
					}
				})
			}
		})
		ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			if row != nil && !f.create && !row.ScheduledMessage {
				owner := row.OwnerID == a.userID()
				if row.Trigger.Kind == "manual" {
					b := outlineButton(c, p, iconPlay, locales.SchedRunNow()).Disabled(!owner || a.sched.acting || !a.canRunSchedules(row.MachineID))
					r := *row
					b.OnClick(func() { a.confirmRun(r) })
				} else {
					label := locales.SchedPaused()
					if row.Enabled {
						label = locales.SchedEnabled()
					}
					ui.Text(c, label).FontSize(sizeFoot).TextColor(p.Secondary)
					on := row.Enabled
					lodySwitch(c, &on, !owner || a.sched.acting).Label(label)
					if on != row.Enabled {
						a.toggleSchedule(*row)
					}
				}
				actions := a.schedActions(*row, a.sched.reg.Runtime[row.ScheduleID])
				var rest []schedAction
				for _, act := range actions {
					switch act.key {
					case "open", "toggle":
						continue
					case "run":
						// Run now from the open schedule asks first.
						r := *row
						act.run = func() { a.confirmRun(r) }
					}
					rest = append(rest, act)
				}
				if len(rest) > 0 {
					iconButton(c.Key("more"), p, iconMore, locales.SchedMore(), p.Hover).Menu(func(m *ui.Menu) { schedMenu(m, rest) })
				}
			}
			iconButton(c.Key("close"), p, iconX, locales.Close(), p.Hover).OnClick(a.closeSchedule)
		})
	})
}

// propertyRow is a labelled row of the schedule's card.
func propertyRow(c *ui.Context, p *palette, key, label string, control func()) {
	ui.Row(c.Key(key)).FillWidth().MinHeight(40).Padding(6, 14).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(sizeSub).TextColor(p.Secondary).Grow(1)
		ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(control)
	})
}

// selectButton is a value that opens its choices.
func (a *App) selectButton(c *ui.Context, p *palette, key, label string, options []option, current string, pick func(string)) {
	open := a.popover(key)
	b := ui.ButtonBase(c.Key(key)).Height(28).Padding(0, 8).Radius(8).Gap(6).Border(1, p.Border).Background(p.Canvas).Cursor(ui.CursorPointer).
		Label(label).OnClick(func() { *open = !*open }).Transition(colorFade)
	if b.Hovered() || *open {
		b.Background(p.Hover)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(sizeSub).TextColor(p.Text).SingleLine().MaxWidth(240)
		ui.Icon(c, iconChevronDown).Size(13, 13).TextColor(p.Tertiary)
	})
	a.optionList(c, p, b, open, options, current, pick)
}

func (a *App) schedFormView(c *ui.Context, p *palette, f *schedForm, row *schedule.Row) {
	if f.loading {
		ui.Row(c).FillWidth().Justify(ui.Center).Padding(40).Children(func() { spinner(c).Size(16, 16) })
		return
	}
	now := time.Now()
	owner := f.create || f.owner == "" || f.owner == a.userID()
	zone := a.machineZone(f.machine)
	ui.Column(c).FillWidth().Gap(14).Padding(0, 0, 16, 0).Children(func() {
		// One box, as the composer: name, a hairline, the prompt, and the
		// Agent's controls along its bottom.
		box := ui.Column(c).FillWidth().Radius(14).Border(1, p.ComposerBorder).Background(p.Composer)
		box.Children(func() {
			ui.Row(c).FillWidth().Padding(12, 14, 6, 14).Gap(6).AlignItems(ui.Center).Children(func() {
				in := ui.TextInputBase(c, &f.title).Placeholder(locales.SchedNamePlaceholder()).Label(locales.SchedName()).FontSize(16).FontWeight(600).TextColor(p.Strong).Grow(1)
				if f.create {
					in.AutoFocus()
				}
				if f.attempted && strings.TrimSpace(f.title) == "" {
					issueMark(c, p, locales.SchedRequireName())
				}
			})
			ui.Box(c).FillWidth().Height(0.5).Margin(0, 14).Background(p.Border)
			ui.Row(c).FillWidth().Padding(8, 14).Gap(6).AlignItems(ui.Start).Children(func() {
				ui.TextAreaBase(c, &f.prompt).Placeholder(locales.SchedPromptPlaceholder()).Label(locales.SchedPrompt()).FontSize(sizeBody).LineHeight(1.5).TextColor(p.Text).Lines(4, 12).Grow(1)
				if f.attempted && strings.TrimSpace(f.prompt) == "" {
					issueMark(c, p, locales.SchedRequirePrompt())
				}
			})
			ui.Row(c).FillWidth().Padding(4, 8, 8, 8).Gap(2).AlignItems(ui.Center).Children(func() {
				agent := a.agent(f.machine, f.agent)
				var agents []option
				for _, ag := range a.catalog.Agents[f.machine] {
					agents = append(agents, option{ag.ID, ag.Name})
				}
				label := locales.NoAgent()
				if agent != nil {
					label = agent.Name
				}
				a.picker(c, p, "sched/agent", iconBot, label, agents, func(id string) {
					f.agent = id
					delete(a.models, "sched")
					delete(a.modes, "sched")
					delete(a.runValues, "sched")
					f.options = nil
				})
				if agent != nil {
					a.runConfig(c, p, "sched/model", agent.ID, a.models["sched"], nil, func(id string) { a.models["sched"] = id })
					a.runOptions(c, p, "sched", agent.ID, a.models["sched"], f.options)
					a.modeButton(c, p, "sched/mode", a.catalog.ModesFor(agent.ID, agent.AgentType), a.modes["sched"], func(id string) { a.modes["sched"] = id })
				}
			})
		})
		// The chat landing's context pills under it: machine, project,
		// worktree.
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Padding(0, 2).Children(func() {
			var machines []option
			for _, id := range a.catalog.MachineIDs {
				if owner := a.catalog.MachineOwner[id]; owner != "" && owner != a.userID() || len(a.catalog.Agents[id]) == 0 {
					continue
				}
				machines = append(machines, option{id, a.machineLabel(id)})
			}
			a.selectButton(c, p, "sched/machine", firstNonEmpty(a.machineLabel(f.machine), locales.HomeMachine()), machines, f.machine, func(id string) {
				if id == f.machine {
					return
				}
				f.machine, f.project, f.worktree, f.agent, f.options = id, "", false, "", nil
				if agents := a.catalog.Agents[id]; len(agents) > 0 {
					f.agent = agents[0].ID
				}
				delete(a.models, "sched")
				delete(a.modes, "sched")
				delete(a.runValues, "sched")
				f.recEdited = true
			})
			if f.dest == schedule.NewSession {
				projects := []option{{"", locales.SchedChatOnly()}}
				for _, pr := range a.catalog.Projects {
					if pr.MachineID == f.machine && strings.Contains(pr.ID, ":local:") {
						projects = append(projects, option{pr.ID, pr.Name})
					}
				}
				for _, r := range a.githubRepos() {
					projects = append(projects, option{"github:" + r.FullName, r.FullName})
				}
				label := locales.SchedChatOnly()
				if pr := a.catalog.Project(f.project); pr != nil {
					label = pr.Name
				} else if repo, ok := strings.CutPrefix(f.project, "github:"); ok {
					label = repo
				}
				a.selectButton(c, p, "sched/project", label, projects, f.project, func(id string) {
					if id != f.project {
						f.project, f.branch = id, ""
					}
				})
				if strings.Contains(f.project, ":local:") {
					ui.Checkbox(c.Key("sched/worktree"), &f.worktree, locales.SchedWorktree())
				}
			}
		})

		// One card for how it runs: where each run goes, then when.
		card := ui.Column(c).FillWidth().Radius(12).Border(0.5, p.Border).Background(p.Canvas).Dividers(0.5, p.Border)
		card.Children(func() {
			dests := []option{{schedule.NewSession, locales.SchedDestNew()}, {schedule.OwnSession, locales.SchedDestOwn()}, {schedule.ExistingSession, locales.SchedDestExisting()}}
			destLabel := dests[0].label
			for _, o := range dests {
				if o.id == f.dest {
					destLabel = o.label
				}
			}
			propertyRow(c, p, "dest", locales.SchedSendTo(), func() {
				if f.dest == schedule.OwnSession && !f.create {
					ghostButton(c, p, locales.SchedDestStartNew()).OnClick(func() { f.epoch++ })
				}
				a.selectButton(c, p, "sched/dest", destLabel, dests, f.dest, func(id string) { f.dest = id })
			})
			if f.dest == schedule.ExistingSession {
				var chats []option
				for _, s := range a.catalog.Sessions {
					if s.MachineID == f.machine && (s.UserID == "" || s.UserID == a.userID()) && !s.Archived && s.ParentSessionID == "" {
						chats = append(chats, option{s.ID, s.Title})
					}
				}
				label := locales.SchedDestChoose()
				if s := a.catalog.Session(f.session); s != nil {
					label = s.Title
				}
				propertyRow(c, p, "chat", locales.SchedOpenChatPlain(), func() {
					if len(chats) == 0 {
						ui.Text(c, locales.SchedDestNoChats()).FontSize(sizeSub).TextColor(p.Secondary)
						return
					}
					a.selectButton(c, p, "sched/chat", label, chats, f.session, func(id string) { f.session = id })
				})
			}
			mode := "timed"
			if f.manual {
				mode = "manual"
			}
			propertyRow(c, p, "trigger", locales.SchedTrigger(), func() {
				segmented(c, p, "sched/trigger", []option{{"timed", locales.SchedTimed()}, {"manual", locales.SchedManual()}}, mode, func(v string) {
					f.manual = v == "manual"
				})
			})
			if f.manual {
				ui.Text(c, locales.SchedManualHelp()).FontSize(sizeFoot).LineHeight(1.5).TextColor(p.Secondary).Padding(10, 14)
				return
			}
			a.schedRecurrence(c, p, f, zone, now)
			ui.Row(c).FillWidth().Padding(10, 14).Gap(8).AlignItems(ui.Center).Children(func() {
				trigger, err := a.formTrigger(f, now)
				var runs []time.Time
				if err == nil {
					runs = schedule.NextRuns(trigger, time.Time{}, now, 3, loadZone(zone))
				}
				switch {
				case err != nil:
					msg := locales.SchedInvalidTime()
					switch {
					case f.rec.Kind == "weekly" && len(f.rec.Weekdays) == 0:
						msg = locales.SchedRequireWeekday()
					case f.rec.Kind == "monthly" && len(f.rec.Days) == 0:
						msg = locales.SchedRequireMonthDay()
					}
					ui.Icon(c, iconCircleAlert).Size(14, 14).TextColor(p.Warning)
					ui.Text(c, msg).FontSize(sizeFoot).TextColor(p.Warning)
				case len(runs) == 0:
					ui.Text(c, locales.SchedNoFuture()).FontSize(sizeFoot).TextColor(p.Secondary)
				default:
					tz := triggerZone(trigger, zone)
					var parts []string
					for _, at := range runs {
						parts = append(parts, upcomingText(at, now, loadZone(tz)))
					}
					ui.Text(c, locales.SchedNextRuns()).FontSize(sizeFoot).TextColor(p.Secondary)
					ui.Text(c, strings.Join(parts, " · ")).FontSize(sizeFoot).TextColor(p.Text).Grow(1).SingleLine().MinWidth(0)
					clock := tz
					if name := a.catalog.MachineNames[f.machine]; name != "" {
						clock = locales.SchedMachineClock(name)
					}
					ui.Text(c, clock).FontSize(sizeFoot).TextColor(p.Secondary).Tooltip(tz).Shrink(0)
				}
			})
		})

		// The footer: what stops the save, then Cancel and Save.
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			note := ""
			switch {
			case !owner:
				note = locales.SchedOwnerOnly()
			case !a.canRunSchedules(f.machine):
				note = locales.SchedUpgrade()
			case f.err != "":
				note = f.err
			case f.attempted:
				note = locales.SchedFixMarked()
			}
			ui.Text(c, note).FontSize(sizeFoot).TextColor(p.Danger).Grow(1).MinWidth(0)
			ghostButton(c, p, locales.Cancel()).OnClick(a.closeSchedule)
			label := locales.SchedSave()
			if a.sched.saving {
				label = locales.SchedSaving()
			}
			primaryButton(c, p, label, owner && !a.sched.saving && a.canRunSchedules(f.machine)).OnClick(a.saveSchedule)
		})
	})
}

// schedMessageView is a scheduled message: what it sends, where and
// when, with its time to change and a cancel while it waits.
func (a *App) schedMessageView(c *ui.Context, p *palette, f *schedForm, row schedule.Row) {
	if f.loading {
		ui.Row(c).FillWidth().Justify(ui.Center).Padding(40).Children(func() { spinner(c).Size(16, 16) })
		return
	}
	rt := a.sched.reg.Runtime[row.ScheduleID]
	pending := row.Enabled && (rt == nil || rt.MessageState == "" || rt.MessageState == "pending")
	owner := row.OwnerID == a.userID()
	ui.Column(c).FillWidth().Gap(14).Padding(0, 0, 16, 0).Children(func() {
		ui.Text(c, f.prompt).FontSize(sizeBody).LineHeight(1.5).TextColor(p.Text).Selectable().FillWidth().
			Padding(12, 14).Radius(12).Border(1, p.ComposerBorder).Background(p.Composer)
		ui.Column(c).FillWidth().Radius(12).Border(0.5, p.Border).Background(p.Canvas).Dividers(0.5, p.Border).Children(func() {
			if s := a.catalog.Session(row.Destination.SessionID); s != nil {
				propertyRow(c, p, "chat", locales.SchedSendTo(), func() {
					ghostButton(c, p, s.Title).OnClick(func() { a.closeSchedule(); a.open(s.ID) })
				})
			}
			at, _ := time.Parse(time.RFC3339Nano, row.Trigger.At)
			propertyRow(c, p, "at", locales.SchedRunAt(), func() {
				zone := a.machineZone(row.MachineID)
				ui.Text(c, instantText(at.In(loadZone(zone)))+" ("+zone+")").FontSize(sizeSub).TextColor(p.Text)
				if pending && owner {
					ghostButton(c, p, locales.SmEditTime()).OnClick(func() { a.openScheduleSend(row.Destination.SessionID, at, row.ScheduleID) })
				}
			})
		})
		if pending && owner {
			ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
				outlineButton(c, p, iconX, locales.SmCancel()).OnClick(func() {
					user := a.userID()
					a.schedAct(func(ctx context.Context, s *schedule.Store) error {
						return s.SetEnabled(ctx, row.ScheduleID, false, user, time.Now())
					}, func() { a.toast("success", locales.SmCancelled()) })
				})
			})
		}
	})
}

// issueMark is an exclamation mark beside the field it is about.
func issueMark(c *ui.Context, p *palette, message string) {
	ui.Icon(c, iconCircleAlert).Size(15, 15).TextColor(p.Warning).Tooltip(message).Label(message).Shrink(0)
}

// schedRecurrence is the rule's rows: what repeats, and its step, days
// and time.
func (a *App) schedRecurrence(c *ui.Context, p *palette, f *schedForm, zone string, now time.Time) {
	r := &f.rec
	if r.Kind == "unsupported" {
		propertyRow(c, p, "repeat", locales.SchedRepeat(), func() {
			ui.Text(c, describeRecurrence(*r, zone)).FontSize(sizeSub).TextColor(p.Text).SingleLine().MaxWidth(320)
			b := ui.ButtonBase(c).Height(24).Padding(0, 4).Cursor(ui.CursorPointer).Label(locales.SchedReplace()).OnClick(func() {
				*r = schedule.Recurrence{Kind: "daily", Hour: 9, TimeZone: zone}
				f.syncTexts(zone)
				f.recEdited = true
			})
			b.Children(func() { ui.Text(c, locales.SchedReplace()).FontSize(sizeSub).TextColor(p.Link) })
		})
		ui.Text(c, locales.SchedUnsupportedHint()).FontSize(sizeFoot).TextColor(p.Secondary).Padding(0, 14, 10, 14)
		return
	}
	labels := map[string]string{
		"daily": locales.SchedRepeatDaily(), "weekdays": locales.SchedRepeatWeekdays(), "weekly": locales.SchedRepeatWeekly(),
		"monthly": locales.SchedRepeatMonthly(), "hours": locales.SchedRepeatHours(), "minutes": locales.SchedRepeatMinutes(), "once": locales.SchedRepeatOnce(),
	}
	var kinds []option
	for _, k := range schedule.Kinds {
		kinds = append(kinds, option{k, labels[k]})
	}
	propertyRow(c, p, "repeat", locales.SchedRepeat(), func() {
		a.selectButton(c, p, "sched/repeat", labels[r.Kind], kinds, r.Kind, func(k string) {
			if k == r.Kind {
				return
			}
			f.recEdited = true
			r.Kind = k
			switch k {
			case "minutes":
				r.Every = 15
			case "hours":
				r.Every = 1
			case "weekly":
				if len(r.Weekdays) == 0 {
					r.Weekdays = []int{int(now.Weekday())}
				}
			case "monthly":
				if len(r.Days) == 0 {
					r.Days = []int{now.Day()}
				}
			case "once":
				if r.At.IsZero() {
					r.At = now.Add(time.Hour).Truncate(time.Hour)
				}
			}
			f.syncTexts(zone)
		})
	})
	edited := func() { f.recEdited = true }
	switch r.Kind {
	case "minutes", "hours":
		steps := schedule.MinuteSteps
		if r.Kind == "hours" {
			steps = schedule.HourSteps
		}
		var opts []option
		for _, s := range steps {
			opts = append(opts, option{strconv.Itoa(s), durationText(s, r.Kind == "hours")})
		}
		propertyRow(c, p, "every", locales.SchedEvery(), func() {
			a.selectButton(c, p, "sched/every", durationText(r.Every, r.Kind == "hours"), opts, strconv.Itoa(r.Every), func(v string) {
				r.Every, _ = strconv.Atoi(v)
				edited()
			})
		})
	case "weekly":
		propertyRow(c, p, "on", locales.SchedOn(), func() {
			for d := 0; d < 7; d++ {
				day := d
				on := false
				for _, x := range r.Weekdays {
					on = on || x == day
				}
				narrow := narrowEn
				if zhLocale() {
					narrow = narrowZh
				}
				dayToggle(c.Key(day), p, narrow[day], weekdayShort(day), on, func() {
					r.Weekdays = toggleInt(r.Weekdays, day)
					edited()
				})
			}
		})
	case "monthly":
		ui.Row(c.Key("days")).FillWidth().Padding(6, 14).Gap(12).AlignItems(ui.Start).Children(func() {
			ui.Text(c, locales.SchedOnDays()).FontSize(sizeSub).TextColor(p.Secondary).Grow(1).Padding(6, 0, 0, 0)
			ui.Column(c).Gap(4).Shrink(0).Children(func() {
				for w := 0; w < 5; w++ {
					ui.Row(c.Key(w)).Gap(4).Children(func() {
						for d := w*7 + 1; d <= min(w*7+7, 31); d++ {
							day := d
							on := false
							for _, x := range r.Days {
								on = on || x == day
							}
							dayToggle(c.Key(day), p, strconv.Itoa(day), strconv.Itoa(day), on, func() {
								r.Days = toggleInt(r.Days, day)
								edited()
							})
						}
					})
				}
			})
		})
	}
	switch r.Kind {
	case "daily", "weekdays", "weekly", "monthly":
		propertyRow(c, p, "at", locales.SchedAt(), func() {
			before := f.timeText
			ui.TextInput(c.Key("sched/time"), &f.timeText).Label(locales.SchedAt()).FontSize(sizeSub).Width(64).Height(28).Padding(0, 8).Radius(8).Border(1, p.Border).TextAlign(ui.Center)
			if f.timeText != before {
				if h, m, ok := parseClock(f.timeText); ok {
					r.Hour, r.Minute = h, m
					edited()
				}
			}
		})
	case "once":
		propertyRow(c, p, "runAt", locales.SchedRunAt(), func() {
			before := f.onceText
			ui.TextInput(c.Key("sched/once"), &f.onceText).Label(locales.SchedRunAt()).Placeholder("2026-10-30 09:00").FontSize(sizeSub).Width(150).Height(28).Padding(0, 8).Radius(8).Border(1, p.Border)
			if f.onceText != before {
				edited()
			}
		})
	}
}

func parseClock(s string) (int, int, bool) {
	hs, ms, ok := strings.Cut(strings.TrimSpace(s), ":")
	h, err1 := strconv.Atoi(hs)
	m, err2 := strconv.Atoi(ms)
	if !ok || err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

func toggleInt(set []int, v int) []int {
	for i, x := range set {
		if x == v {
			return append(append([]int{}, set[:i]...), set[i+1:]...)
		}
	}
	out := append(append([]int{}, set...), v)
	sort.Ints(out)
	return out
}

// dayToggle is a day's round toggle.
func dayToggle(c *ui.Context, p *palette, short, long string, on bool, toggle func()) {
	b := ui.ButtonBase(c).Size(28, 28).Radius(14).Center().Cursor(ui.CursorPointer).Label(long).Tooltip(long).OnClick(toggle).Transition(colorFade)
	switch {
	case on:
		b.Background(p.Text)
	case b.Hovered():
		b.Background(p.Hover)
	default:
		b.Border(1, p.Border)
	}
	b.Children(func() {
		ink := p.Text
		if on {
			ink = p.Canvas
		}
		ui.Text(c, short).FontSize(sizeFoot).FontWeight(500).TextColor(ink)
	})
}

// schedRun is one run in the history.
type schedRun struct {
	id, title, when string
	state           string // done, running or idle
	session         string
}

// chatRuns are a schedule's runs in the chat it posts to, newest first:
// the user turns its runs wrote, done once their reply ended.
func chatRuns(v *session.View, scheduleID string) []session.Entry {
	var out []session.Entry
	for _, e := range v.Entries {
		if e.Role == "user" && e.ScheduleRun != nil && e.ScheduleRun.ScheduleID == scheduleID {
			out = append(out, e)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func runState(v *session.View, turn string) string {
	for _, e := range v.Entries {
		if e.Role != "user" && e.UserTurnID == turn {
			if e.EndedAt > 0 || e.Finished {
				return "done"
			}
			return "running"
		}
	}
	return "idle"
}

// schedChatView follows the chat a schedule posts to while its history is
// on screen, as Lody reads it only then.
func (a *App) schedChatView(chat string) *session.View {
	st := &a.sched
	if a.selected == chat && a.view != nil {
		return a.view
	}
	if a.conn == nil {
		return nil
	}
	if st.chatID != chat {
		a.closeSchedChat()
		st.chatID = chat
		var r *session.Replica
		r = session.OpenLocal(a.conn, a.local, chat, func(v *session.View) {
			a.update(func() {
				if st.chat == r {
					st.chatView = v
				}
			})
		})
		st.chat = r
	}
	return st.chatView
}

func (a *App) closeSchedChat() {
	st := &a.sched
	if st.chat != nil {
		st.chat.Close()
	}
	st.chat, st.chatView, st.chatID = nil, nil, ""
}

// schedHistory is the run history: the chats a new-chat-each-run schedule
// started, or the runs in the one chat it posts to.
func (a *App) schedHistory(c *ui.Context, p *palette, row schedule.Row) {
	now := time.Now()
	loc := loadZone(triggerZone(row.Trigger, a.machineZone(row.MachineID)))
	inChat := row.Destination.Kind == schedule.OwnSession || row.Destination.Kind == schedule.ExistingSession
	var runs []schedRun
	loading := false
	chat := ""
	if inChat {
		chat = row.Destination.SessionID
		if row.Destination.Kind == schedule.OwnSession {
			epoch := 0
			if row.Destination.Epoch != nil {
				epoch = *row.Destination.Epoch
			}
			chat = schedule.OwnSessionID(row.ScheduleID, epoch)
		}
		if a.catalog.Session(chat) != nil {
			v := a.schedChatView(chat)
			loading = v == nil && a.conn != nil
			if v != nil {
				turns := chatRuns(v, row.ScheduleID)
				for i, e := range turns {
					when := ""
					if t, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
						when = upcomingText(t, now, loc)
					}
					runs = append(runs, schedRun{id: e.ID, title: locales.SchedRunNumber(strconv.Itoa(len(turns) - i)), when: when, state: runState(v, e.ID), session: chat})
				}
			}
		}
	} else {
		var sessions []catalog.Session
		for _, s := range a.catalog.Sessions {
			if s.ScheduleID == row.ScheduleID {
				sessions = append(sessions, s)
			}
		}
		sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].CreatedAt > sessions[j].CreatedAt })
		if len(sessions) > 100 {
			sessions = sessions[:100]
		}
		for _, s := range sessions {
			r := schedRun{id: s.ID, title: firstNonEmpty(s.Title, locales.SchedOpenRun()), state: "done", session: s.ID}
			if s.Working() {
				r.state = "running"
			}
			if t, err := time.Parse(time.RFC3339Nano, s.CreatedAt); err == nil {
				r.when = upcomingText(t, now, loc)
			}
			runs = append(runs, r)
		}
	}
	ui.Column(c).FillWidth().Gap(10).Padding(0, 0, 16, 0).Children(func() {
		if inChat {
			s := a.catalog.Session(chat)
			ui.Row(c).FillWidth().Padding(12, 14).Gap(8).AlignItems(ui.Center).Radius(12).Border(0.5, p.Border).Background(p.Canvas).Children(func() {
				ui.Icon(c, iconMessageSquare).Size(14, 14).TextColor(p.Secondary)
				ui.Text(c, locales.SchedAllInChat()).FontSize(sizeSub).TextColor(p.Secondary)
				title := locales.SchedDestOwn()
				if s != nil {
					title = s.Title
				}
				ui.Text(c, title).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).Grow(1).SingleLine().MinWidth(0)
				if s != nil {
					outlineButton(c, p, iconArrowUpRight, locales.SchedOpenChatPlain()).OnClick(func() { a.closeSchedule(); a.open(chat) })
				} else {
					ui.Text(c, locales.SchedDestNotCreated()).FontSize(sizeFoot).TextColor(p.Secondary)
				}
			})
		}
		switch {
		case loading:
			ui.Text(c, locales.SchedHistoryLoading()).FontSize(sizeFoot).TextColor(p.Secondary)
			return
		case len(runs) == 0:
			emptyCard(c, p, locales.SchedHistoryEmpty())
			return
		case inChat:
			ui.Text(c, locales.SchedCountInChat(strconv.Itoa(len(runs)))).FontSize(sizeFoot).TextColor(p.Secondary)
		default:
			ui.Text(c, locales.SchedCountChats(strconv.Itoa(len(runs)))).FontSize(sizeFoot).TextColor(p.Secondary)
		}
		card(c, p, func() {
			for _, run := range runs {
				run := run
				r := ui.Row(c.Key(run.id)).FillWidth().Padding(10, 14).Gap(10).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(run.title).Transition(colorFade).
					OnClick(func() { a.closeSchedule(); a.open(run.session) })
				if r.Hovered() {
					r.Background(p.Hover)
				}
				r.Children(func() {
					ui.Box(c).Size(16, 16).Center().Children(func() {
						switch run.state {
						case "running":
							spinner(c).Size(14, 14).Label(locales.SchedRunning())
						case "done":
							ui.Icon(c, iconCheck).Size(14, 14).TextColor(p.Success).Label(locales.SchedDone())
						}
					})
					ui.Text(c, run.title).FontSize(sizeSub).TextColor(p.Text).Grow(1).SingleLine().MinWidth(0)
					ui.Text(c, run.when).FontSize(sizeCaption).TextColor(p.Secondary).Shrink(0)
					ui.Icon(c, iconChevronRight).Size(14, 14).TextColor(p.Secondary)
				})
			}
		})
	})
}
