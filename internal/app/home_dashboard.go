package app

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// New chat's two boards, as Lody's: one line under the heading says what
// is happening now and what the last seven days came to, and either opens
// into its board. "Now" lists what waits on the user, what runs, what
// just finished and what is worth continuing; "Last 7 days" shows the
// code shipped, a wall of active workdays, the repository and agents used
// most, and the pull requests merged. Everything comes from the session
// metadata the sidebar holds.

// The now board.

type nowReason int

const (
	reasonWaiting nowReason = iota
	reasonWorking
	reasonCIFailed
	reasonFinished
	reasonDraft
	reasonReadyToMerge
	reasonOpenPR
	reasonRecent
)

type nowItem struct {
	session *catalog.Session
	lastAt  float64
	reason  nowReason
	pr      int
}

type nowList struct {
	key   string // attention, running, finished, next
	items []nowItem
}

const homeRecentMs = 3 * 24 * 60 * 60_000

// homeNow sorts the user's open root sessions into the board's four lists,
// each session under the first reason that applies.
func (a *App) homeNow(now time.Time) map[string]*nowList {
	lists := map[string]*nowList{"attention": {key: "attention"}, "running": {key: "running"}, "finished": {key: "finished"}, "next": {key: "next"}}
	if a.account == nil {
		return lists
	}
	user := a.account.User.ID
	children := map[string][]*catalog.Session{}
	for i := range a.catalog.Sessions {
		s := &a.catalog.Sessions[i]
		if s.ParentSessionID != "" {
			children[s.ParentSessionID] = append(children[s.ParentSessionID], s)
		}
	}
	nowMs := float64(now.UnixMilli())
	horizon := nowMs - 14*24*60*60_000
	for i := range a.catalog.Sessions {
		s := &a.catalog.Sessions[i]
		if s.ParentSessionID != "" || s.UserID != user || s.Archived {
			continue
		}
		working, waiting, unread := s.Working(), s.Waiting(), s.Unread()
		lastAt := s.LastMessageAt
		// A child tab's live state counts for its parent, as in the sidebar.
		for _, ch := range children[s.ID] {
			working, waiting = working || ch.Working(), waiting || ch.Waiting()
			lastAt = max(lastAt, ch.LastMessageAt)
		}
		if lastAt == 0 {
			if t, err := time.Parse(time.RFC3339, s.CreatedAt); err == nil {
				lastAt = float64(t.UnixMilli())
			}
		}
		if !working && !waiting && lastAt < horizon {
			continue
		}
		item := nowItem{session: s, lastAt: lastAt}
		pr := s.LatestPR()
		live := pr != nil && (pr.Status == "open" || pr.Status == "draft")
		ci := ""
		if pr != nil {
			item.pr = pr.PRNumber()
			ci = s.PRState[pr.URL].CI
		}
		switch {
		case waiting:
			item.reason = reasonWaiting
		case working:
			item.reason = reasonWorking
		case live && (ci == "f" || ci == "e"):
			item.reason = reasonCIFailed
		case unread:
			item.reason = reasonFinished
		case live && pr.Status == "draft":
			item.reason = reasonDraft
		case live && s.PRState[pr.URL].Ready():
			item.reason = reasonReadyToMerge
		case live:
			item.reason = reasonOpenPR
		case pr != nil:
			continue // merged or closed: done, not to pick back up
		case nowMs-lastAt <= homeRecentMs:
			item.reason = reasonRecent
		default:
			continue
		}
		key := "next"
		switch item.reason {
		case reasonWaiting, reasonCIFailed, reasonDraft:
			key = "attention"
		case reasonWorking:
			key = "running"
		case reasonFinished:
			key = "finished"
		}
		lists[key].items = append(lists[key].items, item)
	}
	for _, l := range lists {
		sort.SliceStable(l.items, func(i, j int) bool {
			if l.items[i].reason != l.items[j].reason {
				return l.items[i].reason < l.items[j].reason
			}
			return l.items[i].lastAt > l.items[j].lastAt
		})
	}
	return lists
}

var homeNowLists = []string{"attention", "running", "finished", "next"}

// homeNowCap is how many rows each list may take of the budget.
var homeNowCap = map[string]int{"attention": 6, "running": 4, "finished": 3, "next": 3}

// homeNowColumn is the column each list stands in.
var homeNowColumn = map[string]int{"attention": 0, "finished": 0, "running": 1, "next": 1}

type nowBlock struct {
	kind  string // list, usage or quiet
	key   string
	shown int
}

// allocateNowRows shares a budget of rows: one to each list with any,
// then up to each list's cap in order.
func allocateNowRows(totals map[string]int, budget int) map[string]int {
	shown := map[string]int{}
	left := budget
	for _, k := range homeNowLists {
		if totals[k] > 0 && left > 0 {
			shown[k], left = 1, left-1
		}
	}
	for _, k := range homeNowLists {
		add := max(0, min(min(homeNowCap[k], totals[k])-shown[k], left))
		shown[k] += add
		left -= add
	}
	return shown
}

func nowBlockHeight(b nowBlock, totals map[string]int) int {
	switch b.kind {
	case "usage":
		return 4
	case "quiet":
		return 3
	}
	more := 0
	if totals[b.key] > b.shown {
		more = 1
	}
	return 2 + b.shown + more
}

func nowColumnHeight(col []nowBlock, totals map[string]int) int {
	h := 0
	for _, b := range col {
		h += nowBlockHeight(b, totals)
	}
	return h
}

// layoutHomeNow places the lists in two columns, then grows the shorter
// one's lists toward the taller's height, as Lody balances its board.
func layoutHomeNow(totals map[string]int, usage bool) [][]nowBlock {
	lists := 0
	for _, k := range homeNowLists {
		if totals[k] > 0 {
			lists++
		}
	}
	shown := allocateNowRows(totals, 12)
	var cols [][]nowBlock
	if lists == 0 {
		cols = [][]nowBlock{{{kind: "quiet"}}}
		if usage {
			cols = append(cols, []nowBlock{{kind: "usage"}})
		}
		return cols
	}
	cols = [][]nowBlock{{}, {}}
	if usage {
		cols[1] = append(cols[1], nowBlock{kind: "usage"})
	}
	for _, k := range homeNowLists {
		cols[homeNowColumn[k]] = append(cols[homeNowColumn[k]], nowBlock{kind: "list", key: k, shown: shown[k]})
	}
	target := 0
	for _, col := range cols {
		target = max(target, nowColumnHeight(col, totals))
	}
	for _, col := range cols {
		for grew := true; grew; {
			grew = false
			for i := len(col) - 1; i >= 0; i-- {
				b := col[i]
				if b.kind != "list" || b.shown >= totals[b.key] {
					continue
				}
				next := b
				next.shown++
				if nowColumnHeight(col, totals)-nowBlockHeight(b, totals)+nowBlockHeight(next, totals) > target {
					continue
				}
				col[i], grew = next, true
				break
			}
		}
	}
	return cols
}

// The week.

type homeWeek struct {
	lines, added, removed int
	daily                 [7]int
	wall                  [][]wallCell
	delta                 *int
	mergedPRs, sessions   int
	agents                []weekAgent
	topRepo               string
	topRepoPercent        int
}

type wallCell struct {
	day   time.Time
	count int
	level int // 0 to 4
}

type weekAgent struct {
	cliType, agentType string
	count              int
}

const (
	wallWeeks = 30
	wallRows  = 5 // Monday to Friday
)

func dayStart(now time.Time, back int) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day()-back, 0, 0, 0, 0, now.Location())
}

func dayKey(t time.Time) string { return t.Format("2006-01-02") }

// homeWeek sums the user's last seven days on the local calendar: a
// session's lines count on the day it was last active, its merged pull
// requests too; sessions and agents on the day it was created.
func (a *App) homeWeek(now time.Time) homeWeek {
	var w homeWeek
	if a.account == nil {
		return w
	}
	user := a.account.User.ID
	windowStart, windowEnd, previousStart := dayStart(now, 6), dayStart(now, -1), dayStart(now, 13)
	activity := map[string]int{}
	agents := map[string]*weekAgent{}
	repos := map[string]int{}
	previousLines := 0
	for i := range a.catalog.Sessions {
		s := &a.catalog.Sessions[i]
		if s.ParentSessionID != "" || s.UserID != user {
			continue
		}
		created, err := time.Parse(time.RFC3339, s.CreatedAt)
		if err != nil {
			continue
		}
		created = created.In(now.Location())
		last := created
		if s.LastMessageAt > 0 {
			last = time.UnixMilli(int64(s.LastMessageAt)).In(now.Location())
		}
		activity[dayKey(created)]++
		if dayKey(last) != dayKey(created) {
			activity[dayKey(last)]++
		}
		changed := int(s.DiffAdd + s.DiffDel)
		switch {
		case !last.Before(windowStart) && last.Before(windowEnd):
			w.lines += changed
			w.added += int(s.DiffAdd)
			w.removed += int(s.DiffDel)
			for _, pr := range s.PullRequests {
				if pr.Status == "merged" {
					w.mergedPRs++
				}
			}
			day := 6
			for day > 0 && last.Before(dayStart(now, 6-day)) {
				day--
			}
			w.daily[day] += changed
		case !last.Before(previousStart) && last.Before(windowStart):
			previousLines += changed
		}
		if !created.Before(windowStart) && created.Before(windowEnd) {
			w.sessions++
			if pr := a.catalog.Project(s.ProjectID); pr != nil && pr.RepoFullName != "" {
				repos[pr.RepoFullName]++
			} else if strings.HasPrefix(s.ProjectID, "github:") {
				repos[strings.TrimPrefix(s.ProjectID, "github:")]++
			}
			key := s.CLIType + "\x00" + s.AgentType
			if agents[key] == nil {
				agents[key] = &weekAgent{cliType: s.CLIType, agentType: s.AgentType}
			}
			agents[key].count++
		}
	}
	if previousLines > 0 {
		d := int(math.Round(float64(w.lines-previousLines) / float64(previousLines) * 100))
		w.delta = &d
	}
	for repo, n := range repos {
		if w.topRepo == "" || n > repos[w.topRepo] || n == repos[w.topRepo] && repo < w.topRepo {
			w.topRepo = repo
		}
	}
	if w.topRepo != "" {
		w.topRepoPercent = int(math.Round(float64(repos[w.topRepo]) / float64(w.sessions) * 100))
	}
	for _, ag := range agents {
		w.agents = append(w.agents, *ag)
	}
	sort.Slice(w.agents, func(i, j int) bool {
		if w.agents[i].count != w.agents[j].count {
			return w.agents[i].count > w.agents[j].count
		}
		return w.agents[i].agentType < w.agents[j].agentType
	})
	if len(w.agents) > 2 {
		w.agents = w.agents[:2]
	}
	// The wall: weeks from Monday, their workdays, the last stopping today.
	weekday := (int(now.Weekday()) + 6) % 7
	busiest := 0
	for week := range wallWeeks {
		var days []wallCell
		for row := range wallRows {
			back := (wallWeeks-1-week)*7 + (weekday - row)
			if back < 0 {
				break
			}
			d := dayStart(now, back)
			n := activity[dayKey(d)]
			busiest = max(busiest, n)
			days = append(days, wallCell{day: d, count: n})
		}
		w.wall = append(w.wall, days)
	}
	for _, days := range w.wall {
		for i := range days {
			days[i].level = wallLevel(days[i].count, busiest)
		}
	}
	return w
}

func wallLevel(count, busiest int) int {
	if count <= 0 || busiest <= 0 {
		return 0
	}
	switch r := float64(count) / float64(busiest); {
	case r <= 0.25:
		return 1
	case r <= 0.5:
		return 2
	case r <= 0.75:
		return 3
	}
	return 4
}

// The look.

// wallInk is a wall cell's shade.
func wallInk(p *palette, level int) ui.Color {
	if level == 0 {
		return p.Text.Alpha(0.07)
	}
	return p.Primary.Alpha([5]float32{0, 0.3, 0.5, 0.72, 1}[level])
}

// countUp rolls a number up from zero over 600ms, easing out as a cubic,
// the first time it shows with something to count.
func countUp(e ui.Element, key string, value int) int {
	if value <= 0 {
		return value
	}
	k := e.Keyframes("count/"+key, []ui.Keyframe{{Value: 0}, {At: 600 * time.Millisecond, Value: 1, Ease: ui.CubicBezier(0.33, 1, 0.68, 1)}})
	return int(math.Round(float64(value) * float64(k)))
}

func formatInt(n int) string {
	if n < 0 {
		return "-" + groupThousands(int64(-n))
	}
	return groupThousands(int64(n))
}

// homeDashboard is the line under the heading and the board it opens.
func (a *App) homeDashboard(c *ui.Context, p *palette) {
	now := c.Now()
	c.After(time.Minute) // ages read in minutes
	lists := a.homeNow(now)
	week := a.homeWeek(now)
	ui.Column(c.Key("home-dashboard")).Width(680).MaxWidth(680).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Gap(4).AlignItems(ui.Center).Justify(ui.Center).MinHeight(28).Children(func() {
			a.boardSegment(c, p, "now", locales.HomeBoardNow(), func() { nowSummary(c, p, lists) })
			a.boardSegment(c, p, "week", locales.HomeBoardWeek(), func() { weekSummary(c, p, week) })
			if a.homeBoard != "" {
				iconButton(c, p, iconChevronUp, locales.HomeBoardFold(), p.Hover).Size(28, 28).OnClick(func() { a.setHomeBoard("") })
			}
		})
		// The open board unfolds below the line, and folds back into it.
		host := ui.Column(c.Key("board")).FillWidth()
		k := host.AnimateWith("open", b2f(a.homeBoard != ""), durSlow, easeStandard)
		if a.homeBoard != "" {
			a.homeBoardShown = a.homeBoard
		}
		if k <= 0.001 && a.homeBoard == "" {
			return
		}
		host.Children(func() {
			unfold(c, k, a.homeBoard != "", func() {
				ui.Column(c.Key("board/"+a.homeBoardShown)).FillWidth().Padding(6, 0, 0, 0).
					Transition(ui.ElementTransition{Duration: durRegular, Ease: easeStandard, Enter: &ui.Motion{Y: 4}}).Children(func() {
					if a.homeBoardShown == "week" {
						a.weekBento(c, p, week)
					} else {
						a.nowBoard(c, p, lists, now)
					}
				})
			})
		})
	})
}

func (a *App) setHomeBoard(board string) {
	a.homeBoard = board
	a.savePrefs()
}

// boardSegment is one of the line's two parts: its name and its summary,
// opening its board, or folding it when it is open.
func (a *App) boardSegment(c *ui.Context, p *palette, key, label string, summary func()) {
	open := a.homeBoard == key
	seg := ui.ButtonBase(c.Key("seg/"+key)).Height(28).Padding(0, 10).Radius(14).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Label(label).Transition(colorFade).OnClick(func() {
		if open {
			a.setHomeBoard("")
		} else {
			a.setHomeBoard(key)
		}
	})
	pressable(seg, 0.97)
	switch {
	case open:
		seg.Background(p.Text.Alpha(0.07))
	case seg.Hovered():
		seg.Background(p.Text.Alpha(0.04))
	}
	seg.Children(func() {
		ui.Text(c, label).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text)
		summary()
	})
}

// nowSummary says how many things need the user, run and just finished;
// a zero fades, and with nothing at all one quiet phrase stands instead.
func nowSummary(c *ui.Context, p *palette, lists map[string]*nowList) {
	counts := []int{len(lists["attention"].items), len(lists["running"].items), len(lists["finished"].items)}
	if counts[0]+counts[1]+counts[2] == 0 {
		ui.Text(c, locales.HomeNowAllCaughtUp()).FontSize(sizeFoot).TextColor(p.Secondary)
		return
	}
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		for i, n := range counts {
			ui.Row(c.Key(i)).Gap(4).AlignItems(ui.Center).Opacity(map[bool]float32{true: 0.4, false: 1}[n == 0]).Children(func() {
				switch i {
				case 0:
					ui.Icon(c, iconHand).Size(12, 12).TextColor(p.Primary)
				case 1:
					if n > 0 {
						workingGrid(c, p, 12)
					} else {
						ui.Box(c).Size(10, 10).Radius(2).Background(p.Text.Alpha(0.3))
					}
				default:
					ui.Icon(c, markSuccess).Size(12, 12).TextColor(p.Success)
				}
				ui.Text(c, fmt.Sprint(n)).FontSize(sizeFoot).TextColor(p.Text).FontFeatures("tnum")
			})
		}
	})
}

// weekSummary is seven pebbles, the days' lines shaded as the wall, and
// the lines shipped.
func weekSummary(c *ui.Context, p *palette, w homeWeek) {
	busiest := 0
	for _, n := range w.daily {
		busiest = max(busiest, n)
	}
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Gap(2).Children(func() {
			for i, n := range w.daily {
				level := 0
				if busiest > 0 {
					level = int(math.Ceil(float64(n) / float64(busiest) * 4))
				}
				ui.Box(c.Key(i)).Size(6, 6).Radius(2).Background(wallInk(p, level))
			}
		})
		ui.Row(c).Gap(3).AlignItems(ui.Baseline).Children(func() {
			ui.Text(c, formatInt(w.lines)).FontSize(sizeFoot).TextColor(p.Text).FontFeatures("tnum")
			ui.Text(c, locales.HomeWeekLinesUnit(w.lines)).FontSize(sizeFoot).TextColor(p.Secondary)
		})
	})
}

// tile is one of the boards' cards.
func tile(c *ui.Context, p *palette, key string, fn func()) ui.Element {
	return ui.Column(c.Key("tile/" + key)).FillWidth().MinWidth(0).Padding(10).Radius(12).Background(p.Text.Alpha(0.04)).Gap(8).Children(fn)
}

func tileLabel(c *ui.Context, p *palette, text string) {
	ui.Text(c, text).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
}

// nowBoard is the now board's two columns of lists, balanced.
func (a *App) nowBoard(c *ui.Context, p *palette, lists map[string]*nowList, now time.Time) {
	totals := map[string]int{}
	for k, l := range lists {
		totals[k] = len(l.items)
	}
	ag := a.agent(a.newChat.machine, a.newChat.agent)
	var limits []catalog.RateLimit
	if ag != nil {
		limits = a.catalog.RateLimits[ag.ID]
	}
	usage := len(limits) > 0
	cols := layoutHomeNow(totals, usage)
	ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Start).Children(func() {
		for i, col := range cols {
			ui.Column(c.Key(i)).Grow(1).Basis(0).MinWidth(0).Gap(6).Children(func() {
				for _, b := range col {
					switch b.kind {
					case "usage":
						tile(c, p, "usage", func() {
							tileLabel(c, p, locales.HomeWeekUsageNamed(ag.Name))
							quotaMeters(c, p, limits, now)
						})
					case "quiet":
						tile(c, p, "quiet", func() {
							ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
								ui.Icon(c, markSuccess).Size(16, 16).TextColor(p.Success)
								ui.Text(c, locales.HomeNowQuiet()).FontSize(16).FontWeight(500).TextColor(p.Text)
							})
							ui.Text(c, locales.HomeNowQuietNote()).FontSize(sizeCaption).TextColor(p.Secondary)
						})
					default:
						a.nowListTile(c, p, lists[b.key], b.shown, now)
					}
				}
			})
		}
	})
}

func nowListTitle(key string) string {
	switch key {
	case "attention":
		return locales.HomeNowAttention()
	case "running":
		return locales.HomeNowRunning()
	case "finished":
		return locales.HomeNowFinished()
	}
	return locales.HomeNowNext()
}

// nowListTile is a list: the rows its share of the board allows, and the
// rest a press away, opened in place.
func (a *App) nowListTile(c *ui.Context, p *palette, l *nowList, shown int, now time.Time) {
	total := len(l.items)
	expanded := a.homeNowOpen == l.key
	if expanded {
		shown = total
	}
	tile(c, p, l.key, func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, nowListTitle(l.key)).FontSize(sizeFoot).TextColor(p.Secondary).Grow(1).SingleLine()
			ink := p.Text
			if total == 0 {
				ink = p.Tertiary
			}
			ui.Text(c, fmt.Sprint(total)).FontSize(sizeFoot).FontWeight(500).TextColor(ink).FontFeatures("tnum")
		})
		if total == 0 {
			return
		}
		ui.Column(c).FillWidth().Gap(1).Children(func() {
			for _, it := range l.items[:min(shown, total)] {
				item := it
				row := ui.ButtonBase(c.Key(it.session.ID)).FillWidth().Height(26).Padding(0, 6).Radius(6).Gap(8).AlignItems(ui.Center).
					Cursor(ui.CursorPointer).Label(it.session.Title).Transition(colorFade).OnClick(func() { a.open(item.session.ID) })
				if row.Hovered() {
					row.Background(p.Text.Alpha(0.05))
				}
				row.Children(func() {
					reasonMark(c, p, item.reason)
					ui.Text(c, firstNonEmpty(item.session.Title, locales.UntitledSession())).FontSize(sizeSub).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
					ui.Text(c, rowMeta(item, now)).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine().Shrink(0)
				})
			}
		})
		if total > min(shown, total) || expanded {
			label := locales.HomeNowShowAll(total)
			icon := iconChevronDown
			if expanded {
				label, icon = locales.HomeNowShowLess(), iconChevronUp
			}
			key := l.key
			more := ui.ButtonBase(c.Key("more")).Height(22).Padding(0, 6).Gap(4).Radius(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(label).
				OnClick(func() {
					if a.homeNowOpen == key {
						a.homeNowOpen = ""
					} else {
						a.homeNowOpen = key
					}
				})
			ink := p.Secondary
			if more.Hovered() {
				ink = p.Text
			}
			more.Children(func() {
				ui.Icon(c, icon).Size(12, 12).TextColor(ink)
				ui.Text(c, label).FontSize(sizeCaption).TextColor(ink)
			})
		}
	})
}

// reasonMark is why a row is on the board.
func reasonMark(c *ui.Context, p *palette, r nowReason) {
	ui.Box(c.Key("reason")).Size(14, 14).Shrink(0).Center().Children(func() {
		switch r {
		case reasonWaiting:
			ui.Icon(c, iconHand).Size(13, 13).TextColor(p.Primary)
		case reasonWorking:
			workingGrid(c, p, 12)
		case reasonCIFailed:
			ui.Icon(c, markDanger).Size(13, 13).TextColor(p.Danger)
		case reasonFinished:
			ui.Icon(c, markSuccess).Size(13, 13).TextColor(p.Success)
		case reasonDraft:
			ui.Icon(c, iconPencil).Size(13, 13).TextColor(p.Secondary)
		case reasonReadyToMerge:
			ui.Icon(c, iconGitMerge).Size(13, 13).TextColor(p.Success)
		case reasonOpenPR:
			ui.Icon(c, iconGitPR).Size(13, 13).TextColor(p.Secondary)
		default:
			ui.Icon(c, iconHistory).Size(13, 13).TextColor(p.Secondary)
		}
	})
}

// rowMeta is what a row says at its end: why it is listed, or how long
// ago it moved.
func rowMeta(it nowItem, now time.Time) string {
	pr := ""
	if it.pr > 0 {
		pr = fmt.Sprintf(" · #%d", it.pr)
	}
	switch it.reason {
	case reasonWaiting:
		return locales.HomeNowWaiting()
	case reasonCIFailed:
		return locales.HomeNowCiFailed(pr)
	case reasonDraft:
		return locales.HomeNowDraft(pr)
	case reasonReadyToMerge:
		return locales.HomeNowReadyToMerge(pr)
	case reasonOpenPR:
		return locales.HomeNowOpenPr(pr)
	}
	return relativeTime(time.UnixMilli(int64(it.lastAt)))
}

// weekBento is the week's tiles: the code shipped over the wall, the
// repository, the pull requests merged, and the agents.
func (a *App) weekBento(c *ui.Context, p *palette, w homeWeek) {
	ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Stretch).Children(func() {
		code := tile(c, p, "code", func() {})
		code.Grow(2).Basis(0)
		code.Children(func() {
			tileLabel(c, p, locales.HomeWeekLines())
			ui.Row(c).Gap(8).AlignItems(ui.Baseline).Children(func() {
				ui.Row(c).Gap(4).AlignItems(ui.Baseline).Children(func() {
					ui.Text(c, formatInt(countUp(code, "lines", w.lines))).FontSize(22).FontWeight(500).TextColor(p.Strong).FontFeatures("tnum")
					ui.Text(c, locales.HomeWeekLinesUnit(w.lines)).FontSize(sizeFoot).TextColor(p.Secondary)
				})
				if w.delta != nil && *w.delta != 0 {
					up := *w.delta > 0
					label := locales.HomeWeekDeltaDown(-*w.delta)
					ink := p.Secondary
					if up {
						label, ink = locales.HomeWeekDeltaUp(*w.delta), p.Success
					}
					ui.Text(c, label).FontSize(sizeCaption).FontWeight(500).TextColor(ink)
					ui.Text(c, locales.HomeWeekDeltaVs()).FontSize(sizeCaption).TextColor(p.Secondary)
				}
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "+"+formatInt(w.added)).Font(fontMono).FontSize(sizeCaption).TextColor(p.DiffAdd)
				ui.Text(c, "−"+formatInt(w.removed)).Font(fontMono).FontSize(sizeCaption).TextColor(p.DiffDel)
				ui.Row(c).Gap(1).Children(func() {
					for i, kind := range diffSquares(w.added, w.removed) {
						ink := p.Text.Alpha(0.12)
						switch kind {
						case 1:
							ink = p.DiffAdd
						case 2:
							ink = p.DiffDel
						}
						ui.Box(c.Key(i)).Size(7, 7).Radius(1.5).Background(ink)
					}
				})
			})
			a.activityWall(c, p, w)
		})
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(6).Children(func() {
			tile(c, p, "repo", func() {
				tileLabel(c, p, locales.HomeWeekRepo())
				if w.topRepo == "" {
					ui.Text(c, "–").FontSize(16).TextColor(p.Text)
					return
				}
				_, name, _ := strings.Cut(w.topRepo, "/")
				ui.Text(c, firstNonEmpty(name, w.topRepo)).FontSize(16).FontWeight(500).TextColor(p.Text).SingleLine().Tooltip(w.topRepo)
				ui.Text(c, locales.HomeWeekRepoShare(w.topRepoPercent)).FontSize(sizeCaption).TextColor(p.Secondary)
			})
			prs := tile(c, p, "prs", func() {})
			prs.Children(func() {
				tileLabel(c, p, locales.HomeWeekPrs())
				ui.Text(c, formatInt(countUp(prs, "prs", w.mergedPRs))).FontSize(16).FontWeight(500).TextColor(p.Text).FontFeatures("tnum")
			})
			tile(c, p, "agents", func() {
				tileLabel(c, p, locales.HomeWeekAgents())
				if len(w.agents) == 0 {
					ui.Text(c, "–").FontSize(16).TextColor(p.Text)
					return
				}
				for _, ag := range w.agents {
					percent := int(math.Round(float64(ag.count) / float64(w.sessions) * 100))
					ui.Column(c.Key(ag.agentType + ag.cliType)).FillWidth().Gap(4).Children(func() {
						ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
							ui.Icon(c, agentGlyph(ag.agentType)).Size(12, 12).TextColor(p.Secondary)
							ui.Text(c, locales.HomeWeekAgentShare(agentTypeLabel(ag.agentType, ag.cliType), percent)).FontSize(sizeFoot).TextColor(p.Text).SingleLine()
						})
						// Each bar grows across from the left over 500ms.
						ui.Row(c).FillWidth().Height(3).Radius(2).Background(p.Text.Alpha(0.08)).Clip().Children(func() {
							bar := ui.Box(c.Key("fill")).Height(3).Radius(2).Background(p.Primary)
							grow := bar.Keyframes("grow", []ui.Keyframe{{Value: 0}, {At: 500 * time.Millisecond, Value: 1, Ease: easeSweep}})
							bar.WidthPercent(float32(percent) * grow)
						})
					})
				}
			})
		})
	})
}

// diffSquares are GitHub's five diff-stat squares: 1 added, 2 removed, 0
// neither, in proportion.
func diffSquares(added, removed int) [5]int {
	var out [5]int
	total := added + removed
	if total == 0 {
		return out
	}
	add := int(math.Round(float64(added) / float64(total) * 5))
	if added > 0 && add == 0 {
		add = 1
	}
	del := min(5-add, int(math.Round(float64(removed)/float64(total)*5)))
	if removed > 0 && del == 0 && add < 5 {
		del = 1
	}
	for i := range out {
		switch {
		case i < add:
			out[i] = 1
		case i < add+del:
			out[i] = 2
		}
	}
	return out
}

// activityWall is the last weeks' workdays, as many as fit, each shaded
// by how busy it was.
func (a *App) activityWall(c *ui.Context, p *palette, w homeWeek) {
	wall := ui.Row(c.Key("wall")).FillWidth().Gap(2).Justify(ui.End).Margin(4, 0, 0, 0).ClipX()
	fit := wallWeeks
	if bw := wall.Bounds().W; bw > 0 {
		fit = max(1, min(wallWeeks, int((bw+2)/12)))
	}
	weeks := w.wall[len(w.wall)-fit:]
	wall.Children(func() {
		for i, days := range weeks {
			ui.Column(c.Key(i)).Gap(2).Children(func() {
				for _, d := range days {
					tip := locales.HomeWeekWallQuiet(d.day.Format("Mon, Jan 2, 2006"))
					if d.count > 0 {
						tip = locales.HomeWeekWallDay(d.day.Format("Mon, Jan 2, 2006"), d.count)
					}
					ui.Box(c.Key(dayKey(d.day))).Size(10, 10).Radius(2).Background(wallInk(p, d.level)).Tooltip(tip)
				}
			})
		}
	})
}

// agentTypeLabel names an agent by its type.
func agentTypeLabel(agentType, cliType string) string {
	switch strings.ToLower(agentType) {
	case "claude", "claude-code":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "gemini":
		return "Gemini"
	case "":
		return firstNonEmpty(cliType, "Agent")
	}
	return agentType
}
