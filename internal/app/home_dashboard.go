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
	// Solid on the tile: quiet days a step toward ink, only activity blue,
	// its lightest well clear of the quiet gray.
	ground := secondaryGround(p)
	if level == 0 {
		return ground.Mix(p.Text, 0.08)
	}
	return ground.Mix(p.Primary, [5]float32{0, 0.38, 0.58, 0.80, 1}[level])
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

// homeBoardMorph notes the board opening, switching or folding, as
// Lody's morph does: the board shown, the one going, how, and since when.
// fresh says the home is showing for the first time, when the board is
// there at once.
func (a *App) homeBoardMorph(c *ui.Context, fresh bool) {
	m := &a.motion
	shown := a.homeBoard
	if fresh {
		m.board, m.boardFrom, m.boardKind, m.boardAt = shown, "", "", time.Time{}
	}
	if shown != m.board {
		kind := "next"
		switch {
		case shown == "":
			kind = "fold"
		case m.board == "":
			kind = "unfold"
		case shown == "now":
			kind = "previous" // the line runs now, then week
		}
		m.boardFrom, m.boardKind, m.boardAt = m.board, kind, c.Now()
		m.board = shown
	}
}

// homeTravel moves the heading and the dashboard to where the stage
// centres them as a board opens, switches or folds, over 240ms on
// ease.standard, the board riding with the line: as Lody's morph, which
// measures the line's travel. Resizing the window moves them at once.
var homeTravel = ui.ElementTransition{Position: true, Duration: 240 * time.Millisecond, Ease: easeStandard}

// easeAccelerate is the curve a switched-from board leaves on.
var easeAccelerate = ui.CubicBezier(0.4, 0, 1, 1)

// homeDashboard is the line under the heading and the board it opens.
// The board never grows out of the line: as Lody's, unfolding it rides
// in with the travelling line, fading in over its first 60%; folding it
// rides away, gone by 40%. Switching, the board left drifts 20px toward
// the segment left behind and fades over 160ms, as the new one comes 28px
// from the side of the one pressed over 300ms, 40ms on.
func (a *App) homeDashboard(c *ui.Context, p *palette) {
	now := c.Now()
	c.After(time.Minute) // ages read in minutes
	lists := a.homeNow(now)
	week := a.homeWeek(now)
	m := &a.motion
	dash := ui.Column(c.Key("home-dashboard")).Width(680).MaxWidth(680).Gap(6).AlignItems(ui.Center).Transition(homeTravel)
	build := func(board string) {
		if board == "week" {
			a.weekBento(c, p, week)
		} else {
			a.nowBoard(c, p, lists, now)
		}
	}
	dash.Children(func() {
		line := a.homeLine(c, p, lists, week)
		if a.homeBoard != "" {
			board := ui.Column(c.Key("board/" + a.homeBoard)).FillWidth()
			switch m.boardKind {
			case "unfold":
				if k := motionClock(c, m.boardAt, 0, 144*time.Millisecond); k < 1 {
					board.Opacity(easeStandard(k))
				}
			case "next", "previous":
				if k := motionClock(c, m.boardAt, 40*time.Millisecond, 300*time.Millisecond); k < 1 {
					from := float32(28)
					if m.boardKind == "previous" {
						from = -28
					}
					e := easeStandard(k)
					board.Opacity(e).Translate(from*(1-e), 0)
				}
			}
			board.Children(func() { build(a.homeBoard) })
		}
		// The board going, out of the flow where it was, under the line.
		from := m.boardFrom
		if from == "" {
			return
		}
		var k, drift float32
		switch m.boardKind {
		case "fold":
			k = easeStandard(motionClock(c, m.boardAt, 0, 96*time.Millisecond))
		case "next", "previous":
			k = easeAccelerate(motionClock(c, m.boardAt, 0, 160*time.Millisecond))
			drift = -20
			if m.boardKind == "previous" {
				drift = 20
			}
		default:
			k = 1
		}
		if k >= 1 {
			m.boardFrom = ""
			return
		}
		ghost := ui.Column(c.Key("board/" + from)).Absolute().Left(0).Right(0).Top(line.Bounds().H + 6).PassThrough()
		ghost.Opacity(1-k).Translate(drift*k, 0)
		ghost.Children(func() { build(from) })
	})
}

// homeLine is the dashboard's line: a segment a board, the open one on a
// pill that glides to the other as the board switches, and fades in and
// out with the board as it unfolds and folds, over 240ms.
func (a *App) homeLine(c *ui.Context, p *palette, lists map[string]*nowList, week homeWeek) ui.Element {
	m := &a.motion
	shown := a.homeBoard
	line := ui.Row(c.Key("home-line")).Gap(4).AlignItems(ui.Center).Justify(ui.Center).MinHeight(28)
	var pill ui.Rect
	line.Children(func() {
		if b := a.boardSegment(c, p, "now", locales.HomeBoardNow(), func() { nowSummary(c, p, lists) }); shown == "now" {
			pill = b
		}
		// A hairline keeps the two summaries two phrases; beside the pill it
		// is not needed, but keeps its room.
		divider := ui.Box(c.Key("divider")).Size(1, 12).Shrink(0).Background(p.Border)
		if shown != "" {
			divider.Opacity(0)
		}
		if b := a.boardSegment(c, p, "week", locales.HomeBoardWeek(), func() { weekSummary(c, p, week) }); shown == "week" {
			pill = b
		}
		// The end of the line folds the board, or unfolds the last one
		// open, as Lody's.
		end, icon := locales.HomeBoardUnfold(), iconChevronsUpDown
		if shown != "" {
			end, icon = locales.HomeBoardFold(), iconChevronsDownUp
		}
		iconButton(c.Key("fold"), p, icon, end, p.Hover).Size(24, 24).OnClick(func() {
			if shown != "" {
				a.setHomeBoard("")
				return
			}
			a.setHomeBoard(firstNonEmpty(a.lastHomeBoard, "now"))
		})
	})
	origin := line.Bounds()
	if shown != "" && pill.W > 0 {
		x, w := pill.X-origin.X, pill.W
		if switching := m.boardKind == "next" || m.boardKind == "previous"; switching && motionClock(c, m.boardAt, 0, 240*time.Millisecond) < 1 {
			x, w = line.AnimateWith("pill-x", x, 240*time.Millisecond, easeStandard), line.AnimateWith("pill-w", w, 240*time.Millisecond, easeStandard)
		} else {
			x, w = line.Jump("pill-x", x), line.Jump("pill-w", w)
		}
		m.pill = ui.Rect{X: x, Y: pill.Y - origin.Y, W: w, H: pill.H}
	}
	alpha := line.AnimateWith("pill", b2f(shown != ""), 240*time.Millisecond, easeStandard)
	if r := m.pill; alpha > 0 && r.W > 0 {
		// In the tiles' own material: the tab the board hangs from.
		line.Draw(func(pt *ui.Painter, box ui.Rect) {
			pt.Fill(ui.Rect{X: box.X + r.X, Y: box.Y + r.Y, W: r.W, H: r.H}, p.Text.Alpha(0.05*alpha), r.H/2)
		})
	}
	return line
}

func (a *App) setHomeBoard(board string) {
	if board != "" {
		a.lastHomeBoard = board
	}
	a.homeBoard = board
	a.savePrefs()
}

// boardSegment is one of the line's two parts: its name and its summary,
// opening its board, or folding it when it is open. It reports where it
// was, for the pill. As Lody's, its hover fill comes and goes at once.
func (a *App) boardSegment(c *ui.Context, p *palette, key, label string, summary func()) ui.Rect {
	open := a.homeBoard == key
	seg := ui.ButtonBase(c.Key("seg/"+key)).Height(28).Padding(0, 10).Radius(14).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Label(label).OnClick(func() {
		if open {
			a.setHomeBoard("")
		} else {
			a.setHomeBoard(key)
		}
	})
	if !open && seg.Hovered() {
		seg.Background(p.Hover)
	}
	ink := p.Secondary
	if open {
		ink = p.Text
	}
	seg.Children(func() {
		ui.Text(c, label).FontSize(sizeFoot).FontWeight(500).TextColor(ink)
		summary()
	})
	return seg.Bounds()
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
			// The list opened over this column, or still landing back.
			var focus *nowList
			focusShown := 0
			for _, b := range col {
				if b.key == a.homeNowFocus && lists[b.key] != nil {
					focus, focusShown = lists[b.key], b.shown
				}
			}
			column := ui.Column(c.Key(i)).Grow(1).Basis(0).MinWidth(0)
			k := column.AnimateTo("focus", b2f(focus != nil && a.homeNowOpen == focus.key), focusSpring)
			if focus != nil && a.homeNowOpen != focus.key && k < 0.002 {
				a.homeNowFocus, focus = "", nil
			}
			var focusTile ui.Element
			column.Children(func() {
				board := ui.Column(c.Key("board")).FillWidth().Gap(6)
				board.Children(func() {
					for _, b := range col {
						switch b.kind {
						case "usage":
							tile(c, p, "usage", func() {
								tileLabel(c, p, locales.HomeWeekUsageNamed(ag.Name))
								quotaMeters(c, p, limits, now, true)
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
							t := a.nowListTile(c, p, lists[b.key], b.shown, now)
							if focus != nil && b.key == focus.key {
								focusTile = t
							}
						}
					}
				})
				if focus != nil && k > 0 {
					a.nowFocus(c, p, column, board, focusTile, focus, focusShown, k, now)
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

// nowListTile is a list: the rows its share of the board allows, and
// "Show all", which opens the whole list over its column.
func (a *App) nowListTile(c *ui.Context, p *palette, l *nowList, shown int, now time.Time) ui.Element {
	total := len(l.items)
	return tile(c, p, l.key, func() {
		a.nowListHead(c, p, l)
		if total == 0 {
			return
		}
		ui.Column(c).FillWidth().Gap(1).Children(func() {
			for _, it := range l.items[:min(shown, total)] {
				a.nowRow(c, p, it, now)
			}
		})
		if total > min(shown, total) {
			key := l.key
			a.nowMore(c, p, locales.HomeNowShowAll(total), iconChevronDown, func() { a.homeNowOpen, a.homeNowFocus = key, key })
		}
	})
}

func (a *App) nowListHead(c *ui.Context, p *palette, l *nowList) {
	total := len(l.items)
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, nowListTitle(l.key)).FontSize(sizeFoot).TextColor(p.Secondary).Grow(1).SingleLine()
		ink := p.Text
		if total == 0 {
			ink = p.Tertiary
		}
		ui.Text(c, fmt.Sprint(total)).FontSize(sizeFoot).FontWeight(500).TextColor(ink).FontFeatures("tnum")
	})
}

func (a *App) nowRow(c *ui.Context, p *palette, it nowItem, now time.Time) ui.Element {
	item := it
	row := ui.ButtonBase(c.Key(it.session.ID)).FillWidth().Height(26).Padding(0, 6).Radius(6).Gap(8).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label(it.session.Title).OnClick(func() { a.open(item.session.ID) })
	if row.Hovered() {
		row.Background(p.Text.Alpha(0.05))
	}
	row.Children(func() {
		reasonMark(c, p, item.reason)
		ui.Text(c, firstNonEmpty(item.session.Title, locales.UntitledSession())).FontSize(sizeSub).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
		ui.Text(c, rowMeta(item, now)).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine().Shrink(0)
	})
	return row
}

func (a *App) nowMore(c *ui.Context, p *palette, label string, icon *ui.SVG, press func()) ui.Element {
	more := ui.ButtonBase(c.Key("more")).Height(22).Padding(0, 6).Gap(4).Radius(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(label).OnClick(press)
	ink := p.Secondary
	if more.Hovered() {
		ink = p.Text
	}
	more.Children(func() {
		ui.Icon(c, icon).Size(12, 12).TextColor(ink)
		ui.Text(c, label).FontSize(sizeCaption).TextColor(ink)
	})
	return more
}

// focusSpring is the one spring a now list opens and closes on, as Lody's
// focus motion: response 0.4s, damping 0.9 — about 95% there at 256ms,
// settled by about 660ms.
var focusSpring = ui.Animation{Spring: ui.SpringOptions{Mass: 1, Stiffness: 246.74, Damping: 28.27}}

// nowFocus is a now list opened over its column, as Lody's: the frame
// grows from the tile to the column on the spring while the rest of the
// column steps back to 0.94 toward the tile and fades; the rows the tile
// hid rise 4px in over 200ms, a few at a time; closing, the frame lands
// back in its tile on the same spring as the rows it takes fade.
func (a *App) nowFocus(c *ui.Context, p *palette, col, board, tileEl ui.Element, l *nowList, shown int, k float32, now time.Time) {
	cb, tb := col.LayoutBounds(), tileEl.LayoutBounds()
	if cb.W <= 0 || tb.W <= 0 {
		return
	}
	tx, ty := tb.X-cb.X, tb.Y-cb.Y
	board.TransformOrigin((tx+tb.W/2)/cb.W, (ty+tb.H/2)/max(cb.H, 1)).Scale(1-0.06*k, 1-0.06*k).Opacity(max(0, 1-k))
	lerp := func(from, to float32) float32 { return from + (to-from)*k }
	frame := ui.Column(c.Key("focus-frame")).Absolute().Left(lerp(tx, 0)).Top(lerp(ty, 0)).Width(lerp(tb.W, cb.W)).Height(lerp(tb.H, cb.H)).
		Padding(10).Radius(12).Background(p.Canvas.Mix(p.Text, 0.04)).Gap(8).Clip()
	opening := a.homeNowOpen == l.key
	// Lody times the fades by the spring: closing, what the tile does not
	// show fades out linearly over its first 35%; the tile's "Show all",
	// which goes in 80ms as the list opens, is back between 20% and 40%,
	// so the real tile replaces the frame unseen.
	closing := phaseClock(col, "closing", !opening, focusMoves)
	opened := phaseClock(col, "opening", opening, 80*time.Millisecond)
	leaving := max(0, 1-closing/0.35)
	late := func(e ui.Element, i int) {
		r := e.Keyframes("late", []ui.Keyframe{{Value: 0}, {At: 200 * time.Millisecond, Value: 1, Ease: easeStandard}},
			ui.KeyframeOptions{Delay: time.Duration(30+14*min(i, 8)) * time.Millisecond})
		if opening {
			// Rising in a few at a time as it opens.
			e.Opacity(r).Translate(0, 4*(1-r))
		} else {
			e.Opacity(r * leaving)
		}
	}
	var last ui.Rect
	frame.Children(func() {
		a.nowListHead(c, p, l)
		ui.Scroll(c.Key("focus-rows")).FillWidth().Grow(1).MinHeight(0).Gap(1).Children(func() {
			for i, it := range l.items {
				row := a.nowRow(c, p, it, now)
				if i == shown-1 {
					last = row.Bounds()
				}
				if i >= shown {
					late(row, i-shown)
				}
			}
			late(a.nowMore(c, p, locales.HomeNowShowLess(), iconChevronUp, func() { a.homeNowOpen = "" }), len(l.items)-shown)
		})
		standIn := 1 - opened
		if !opening {
			standIn = min(1, max(0, (closing-0.2)/0.2))
		}
		if fb := frame.Bounds(); standIn > 0 && last.H > 0 && fb.H > 0 {
			ui.Row(c.Key("stand-in")).Absolute().Left(10).Top(last.Y+last.H+8-fb.Y).Height(22).Padding(0, 6).Gap(4).AlignItems(ui.Center).
				PassThrough().Opacity(standIn).Children(func() {
				ui.Icon(c, iconChevronDown).Size(12, 12).TextColor(p.Secondary)
				ui.Text(c, locales.HomeNowShowAll(len(l.items))).FontSize(sizeCaption).TextColor(p.Secondary)
			})
		}
	})
}

// focusMoves is how long the focus spring takes to settle, as Lody's
// spring(0.4, 0.9) does: what fades with it is timed by it.
const focusMoves = 662 * time.Millisecond

// phaseClock is how far, 0 to 1 and linearly over d, e is into a phase
// while on: it starts again each time on turns true, and is 0 while off.
func phaseClock(e ui.Element, key string, on bool, d time.Duration) float32 {
	if !on {
		return e.Keyframes(key, []ui.Keyframe{{Value: 0}, {At: time.Millisecond, Value: 0}})
	}
	return e.Keyframes(key, []ui.Keyframe{{Value: 0}, {At: d, Value: 1}}, ui.KeyframeOptions{Run: 1})
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
				tip(c, p, ui.Text(c, firstNonEmpty(name, w.topRepo)).FontSize(16).FontWeight(500).TextColor(p.Text).SingleLine(), w.topRepo)
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

// activityWall is the last weeks' workdays, as Lody's contribution wall:
// cells of 0.7em, 3px apart, as many weeks as fit (eight at least), the
// cells stretching to fill the width; it fades in over 400ms on CSS's
// ease-out.
func (a *App) activityWall(c *ui.Context, p *palette, w homeWeek) {
	const gap, cellEm, minWeeks = 3, 0.7, 8
	wall := ui.Row(c.Key("wall")).FillWidth().Gap(gap).Margin(4, 0, 0, 0).ClipX()
	if k := wall.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: 400 * time.Millisecond, Value: 1, Ease: cssEaseOut}}); k < 1 {
		wall.Opacity(k)
	}
	cell := float32(sizeBody * cellEm)
	fit := wallWeeks
	bw := wall.Bounds().W
	if bw > 0 {
		fit = min(wallWeeks, max(minWeeks, int((bw+gap)/(cell+gap))))
		cell = (bw - gap*float32(fit-1)) / float32(fit)
	}
	weeks := w.wall[len(w.wall)-fit:]
	wall.Children(func() {
		for i, days := range weeks {
			ui.Column(c.Key(i)).Gap(gap).Grow(1).Basis(0).MinWidth(0).Children(func() {
				for _, d := range days {
					label := locales.HomeWeekWallQuiet(d.day.Format("Mon, Jan 2, 2006"))
					if d.count > 0 {
						label = locales.HomeWeekWallDay(d.day.Format("Mon, Jan 2, 2006"), d.count)
					}
					tip(c, p, ui.Box(c.Key(dayKey(d.day))).FillWidth().Height(cell).Radius(3).Background(wallInk(p, d.level)), label)
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
