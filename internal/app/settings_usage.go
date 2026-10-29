package app

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// AI usage, as Lody's page shows it: the totals of a range, its "skyline"
// of buckets beside a donut of the models, and the split by model and by
// member, from usage:getWorkspaceUsageTimeline.

type usageTimeline struct {
	StartMs      float64 `json:"startMs"`
	EndMs        float64 `json:"endMs"`
	BucketSizeMs float64 `json:"bucketSizeMs"`
	Totals       struct {
		Tokens  float64 `json:"tokens"`
		CostUSD float64 `json:"costUSD"`
	} `json:"totals"`
	Users map[string]struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"users"`
	Buckets []usageBucket `json:"buckets"`
}

type usageBucket struct {
	BucketStartMs float64 `json:"bucketStartMs"`
	BucketLabel   string  `json:"bucketLabel"`
	Tokens        float64 `json:"tokens"`
	CostUSD       float64 `json:"costUSD"`
	ByModel       []struct {
		ModelID string  `json:"modelId"`
		Tokens  float64 `json:"tokens"`
		CostUSD float64 `json:"costUSD"`
	} `json:"byModel"`
	ByUser []struct {
		UserID  string  `json:"userId"`
		Tokens  float64 `json:"tokens"`
		CostUSD float64 `json:"costUSD"`
	} `json:"byUser"`
}

var usageRanges = []string{"day", "week", "month", "total"}

func usageRangeLabels() []string {
	return []string{locales.Usage24h(), locales.Usage7d(), locales.Usage30d(), locales.UsageTotal()}
}

func usageRangeSubtitle(r string) string {
	switch r {
	case "day":
		return locales.UsageLast24h()
	case "week":
		return locales.UsageLast7d()
	case "total":
		return locales.UsageAllTime()
	}
	return locales.UsageLast30d()
}

// usagePart is one model's or member's share.
type usagePart struct {
	key          string
	tokens, cost float64
}

func (u usagePart) value(cost bool) float64 {
	if cost {
		return u.cost
	}
	return u.tokens
}

// usageSplit sums the buckets' parts by key, largest first.
func usageSplit(buckets []usageBucket, byUser bool) []usagePart {
	sums := map[string]*usagePart{}
	add := func(key string, tokens, cost float64) {
		s := sums[key]
		if s == nil {
			s = &usagePart{key: key}
			sums[key] = s
		}
		s.tokens += tokens
		s.cost += cost
	}
	for _, b := range buckets {
		if byUser {
			for _, u := range b.ByUser {
				add(u.UserID, u.Tokens, u.CostUSD)
			}
		} else {
			for _, m := range b.ByModel {
				add(m.ModelID, m.Tokens, m.CostUSD)
			}
		}
	}
	out := make([]usagePart, 0, len(sums))
	for _, s := range sums {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].tokens != out[j].tokens {
			return out[i].tokens > out[j].tokens
		}
		return out[i].key < out[j].key
	})
	return out
}

// usageStats are the figures under the skyline.
type usageStats struct {
	total, average, peak float64
	peakAt               float64 // the peak bucket's start
	active               int     // buckets with any usage
	current, longest     int     // active buckets in a row: ending now, and at most
}

func usageStatsOf(buckets []usageBucket, cost bool) usageStats {
	var s usageStats
	run := 0
	for _, b := range buckets {
		v := b.Tokens
		if cost {
			v = b.CostUSD
		}
		s.total += v
		if v > s.peak {
			s.peak, s.peakAt = v, b.BucketStartMs
		}
		if v > 0 {
			s.active++
			run++
			s.longest = max(s.longest, run)
		} else {
			run = 0
		}
	}
	s.current = run
	if len(buckets) > 0 {
		s.average = s.total / float64(len(buckets))
	}
	return s
}

// formatCount writes a count in the app's language: 1.5M or 1.2B, and in
// Chinese 1500万 or 2.4亿, as Lody does.
func formatCount(n float64) string {
	if strings.HasPrefix(i18n.Locale(), "zh") {
		switch {
		case n >= 1e8:
			return trimZero(fmt.Sprintf("%.1f", n/1e8)) + "亿"
		case n >= 1e4:
			return trimZero(fmt.Sprintf("%.1f", n/1e4)) + "万"
		}
		return fmt.Sprintf("%.0f", n)
	}
	switch {
	case n >= 1e9:
		return trimZero(fmt.Sprintf("%.1f", n/1e9)) + "B"
	case n >= 1e6:
		return trimZero(fmt.Sprintf("%.1f", n/1e6)) + "M"
	case n >= 1e3:
		return trimZero(fmt.Sprintf("%.1f", n/1e3)) + "K"
	}
	return fmt.Sprintf("%.0f", n)
}

// usagePalette colors the parts of a split, in order.
func usagePalette(p *palette, i int) ui.Color {
	colors := []ui.Color{
		ui.RGB(102, 140, 237), ui.RGB(61, 97, 184), ui.RGB(120, 92, 214),
		ui.RGB(227, 125, 61), ui.RGB(82, 171, 148), ui.RGB(204, 92, 133),
	}
	if i < len(colors) {
		return colors[i]
	}
	return p.Tertiary
}

// bucketTime writes when a bucket starts: its hour in a day's range, its
// day otherwise, in UTC as Lody's usage is.
func bucketTime(ms float64, hourly bool) string {
	t := time.UnixMilli(int64(ms)).UTC()
	zh := strings.HasPrefix(i18n.Locale(), "zh")
	switch {
	case hourly && zh:
		return t.Format("1月2日 15:04") + " UTC"
	case hourly:
		return t.Format("Jan 2 15:04") + " UTC"
	case zh:
		return t.Format("1月2日")
	}
	return t.Format("Jan 2")
}

func (a *App) usagePage(c *ui.Context, p *palette) {
	settingsTitle(c, p, locales.SettingsUsage())
	if a.usageRange == "" {
		a.usageRange = "day"
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			if ws := a.workspace(); ws != nil {
				ui.Text(c, ws.Name).FontSize(18).FontWeight(500).TextColor(p.Strong).SingleLine()
			}
			ui.Text(c, usageRangeSubtitle(a.usageRange)).FontSize(sizeFoot).TextColor(p.Secondary)
		})
		usageRangeTray(c, p, a.usageRange, func(r string) { a.usageRange = r })
	})
	// Lody asks for every range at once, so that a switch shows at once
	// and its motion has both sides to move between.
	for _, r := range usageRanges {
		a.usageTimelineFetch(r)
	}
	rng := a.usageRange
	f := a.usageTimelineFetch(rng)
	if !pending(c, p, f) {
		a.motion.rng = ""
		return
	}
	t, _ := f.value.(*usageTimeline)
	if t == nil {
		a.motion.rng = ""
		return
	}
	ui.Row(c).FillWidth().Gap(12).Margin(4, 0).Children(func() {
		a.bigStat(c, p, "Token", t.Totals.Tokens, formatCount)
		a.bigStat(c, p, locales.UsageCostUSD(), t.Totals.CostUSD, formatUSD)
	})
	cost := a.usageCost
	models := usageSplit(t.Buckets, false)
	people := usageSplit(t.Buckets, true)
	hourly := rng == "day" || rng == "week"
	ui.Column(c).FillWidth().Radius(12).Border(1, p.Border).Clip().Children(func() {
		ui.Column(c).FillWidth().Padding(16).Gap(16).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, locales.UsageSkyline()).FontSize(sizeBody).FontWeight(600).TextColor(p.Strong)
					ui.Text(c, usageRangeSubtitle(rng)).FontSize(sizeFoot).TextColor(p.Secondary)
				})
				usageMetricSegments(c, p, cost, func(v bool) { a.usageCost = v })
			})
			// As Lody's: the donut is the hourly ranges' own column, fading
			// in and out over 150ms; beside it, one view for the hourly
			// ranges and one for the year, crossfading over 200ms as the
			// range crosses between them. 30 days and all time share the
			// year, which only relights its days. None of it moves as the
			// card first shows.
			frame := ui.Row(c.Key("usage-view")).FillWidth().Gap(24).AlignItems(ui.Start)
			settle := firstShown(frame, "frame")
			frame.Children(func() {
				if hourly {
					ui.Column(c.Key("rings")).Width(170).Shrink(0).Transition(usageViewFade(150 * time.Millisecond)).Children(func() {
						a.usageDonut(c, p, models, cost, t, settle)
					})
				}
				view := ui.Column(c.Key(map[bool]string{true: "hourly", false: "skyline"}[hourly])).Grow(1).MinWidth(0).Gap(16).
					Transition(usageViewFade(200 * time.Millisecond))
				view.Children(func() {
					if hourly {
						a.usageRangePanel(c, p, t, cost)
					} else if days := a.usageCalendar(); days != nil {
						// Longer ranges show the year, the range's days lit.
						a.usageHeatmap(c, p, days, cost, rng == "month")
						a.usageDayPanel(c, p)
					}
				})
			})
			if !hourly {
				a.motion.rng = ""
			}
		})
		stats := usageStatsOf(t.Buckets, cost)
		dayStats := rng == "total" && a.usageCalendar() != nil
		if dayStats {
			stats = calendarStats(a.usageCalendar(), cost)
		}
		fmtv := formatCount
		if cost {
			fmtv = formatUSD
		}
		ui.Column(c).FillWidth().Padding(16).Gap(16).Background(p.Text.Alpha(0.025)).BorderWidth(1, 0, 0, 0).BorderColor(p.Border).Children(func() {
			ui.Row(c).FillWidth().Gap(24).Children(func() {
				usageShareBar(c, p, locales.UsageByModel(), models, cost, func(k string) string { return k })
				usageShareBar(c, p, locales.UsageByMember(), people, cost, func(id string) string {
					if a.account != nil && id == a.account.User.ID {
						return firstNonEmpty(a.account.User.Name, locales.SettingsYou())
					}
					if u, ok := t.Users[id]; ok {
						return firstNonEmpty(u.Name, u.Email, id)
					}
					return id
				})
			})
			ui.Row(c).FillWidth().Gap(12).Children(func() {
				smallStat(c, p, locales.UsageStatTotal(), fmtv(stats.total), "")
				average, peak, active := locales.UsageStatAverage(), locales.UsageStatPeak(), locales.UsageStatActive()
				if dayStats {
					average, peak, active = locales.UsageStatDaily(), locales.UsageStatPeakDay(), locales.UsageStatActiveDays()
				}
				smallStat(c, p, average, fmtv(stats.average), "")
				peakAt := ""
				if stats.peak > 0 {
					peakAt = bucketTime(stats.peakAt, !dayStats && t.BucketSizeMs > 0 && t.BucketSizeMs < 86400000)
				}
				smallStat(c, p, peak, fmtv(stats.peak), peakAt)
				smallStat(c, p, active, fmt.Sprint(stats.active), locales.UsageStatCurrent(stats.current))
				smallStat(c, p, locales.UsageStatLongest(), fmt.Sprint(stats.longest), "")
			})
		})
	})
	name := func(id string) string {
		if a.account != nil && id == a.account.User.ID {
			return firstNonEmpty(a.account.User.Name, locales.SettingsYou())
		}
		if u, ok := t.Users[id]; ok {
			return firstNonEmpty(u.Name, u.Email, id)
		}
		return id
	}
	usageStacked(c, p, locales.UsageByModel(), t, models, false, cost, strings.NewReplacer(" (recommended)", "").Replace)
	if len(people) > 1 {
		usageStacked(c, p, locales.UsageByMember(), t, people, true, cost, name)
	}
}

type usageDay struct {
	DayStartMs float64 `json:"dayStartMs"`
	Date       string  `json:"date"`
	Tokens     float64 `json:"tokens"`
	CostUSD    float64 `json:"costUSD"`
	IsFuture   bool    `json:"isFuture"`
}

// usageCalendar is the workspace's usage by day over the last year.
func (a *App) usageCalendar() []usageDay {
	convex, ws := a.convex, a.workspaceID
	f := a.fetch("usage-calendar", func(ctx context.Context) (any, error) {
		var out struct {
			Days []usageDay `json:"days"`
		}
		err := convex.Query(ctx, "usage:getWorkspaceUsageCalendar", map[string]any{"workspaceId": ws}, &out)
		return out.Days, err
	})
	days, _ := f.value.([]usageDay)
	return days
}

// calendarStats are the year's figures by day, as Lody shows for All
// time.
func calendarStats(days []usageDay, cost bool) usageStats {
	var buckets []usageBucket
	for _, d := range days {
		if !d.IsFuture {
			buckets = append(buckets, usageBucket{BucketStartMs: d.DayStartMs, Tokens: d.Tokens, CostUSD: d.CostUSD})
		}
	}
	return usageStatsOf(buckets, cost)
}

// heat is how strongly a value shows: against the 90th percentile of the
// values above zero, eased, as Lody's charts scale.
func heatScale(values []float64) func(float64) float32 {
	var active []float64
	for _, v := range values {
		if v > 0 {
			active = append(active, v)
		}
	}
	sort.Float64s(active)
	p90 := 0.0
	if len(active) > 0 {
		p90 = active[min(len(active)-1, int(math.Ceil(0.9*float64(len(active))))-1)]
	}
	return func(v float64) float32 {
		if v <= 0 || p90 <= 0 {
			return 0
		}
		return float32(math.Pow(math.Min(v/p90, 1), 0.62))
	}
}

// usageHeatmap is the year a column a week, a cell a day. As Lody's, the
// cells only relight: hovering one brightens it over 300ms and rings it,
// and the days outside 30 days dim to 0.3 over 300ms, on CSS's ease.
func (a *App) usageHeatmap(c *ui.Context, p *palette, days []usageDay, cost, month bool) {
	values := make([]float64, len(days))
	for i, d := range days {
		values[i] = d.Tokens
		if cost {
			values[i] = d.CostUSD
		}
	}
	heat := heatScale(values)
	window := time.Now().Add(-30 * 24 * time.Hour).UnixMilli()
	fmtv := formatCount
	if cost {
		fmtv = formatUSD
	}
	relight := ui.Animation{Duration: 300 * time.Millisecond, Ease: cssEase}
	ui.Row(c.Key("heatmap")).FillWidth().Gap(2).Children(func() {
		for w := 0; w*7 < len(days); w++ {
			ui.Column(c.Key(w)).Gap(2).Children(func() {
				for i := w * 7; i < min(len(days), w*7+7); i++ {
					d := days[i]
					cell := ui.ButtonBase(c.Key(i)).Size(9, 9).Radius(2).Label(d.Date)
					tip(c, p, cell, d.Date+" · "+fmtv(values[i]))
					if !d.IsFuture {
						day := d.DayStartMs
						cell.Cursor(ui.CursorPointer).OnClick(func() {
							if a.usageDay == day {
								a.usageDay = 0
							} else {
								a.usageDay = day
							}
						})
					}
					hovered := cell.Hovered() && !d.IsFuture
					fill := p.Text.Alpha(0.06)
					switch {
					case d.IsFuture:
						fill = p.Text.Alpha(0.02)
					case values[i] > 0:
						fill = usagePalette(p, 0).Alpha(0.18 + 0.74*heat(values[i]))
					}
					bright := cell.AnimateTo("bright", b2f(hovered), relight)
					cell.Background(cssBrightness(fill, 1+0.1*bright))
					switch {
					case a.usageDay == d.DayStartMs:
						cell.Shadow(0, 0, 0, 1, p.Text)
					case hovered:
						cell.Shadow(0, 0, 0, 1, p.Text.Alpha(0.4))
					}
					dim := float32(1)
					if month && d.DayStartMs < float64(window) {
						dim = 0.3
					}
					if o := cell.AnimateTo("dim", dim, relight); o < 1 {
						cell.Opacity(o)
					}
				}
			})
		}
	})
}

// usageStacked plots each bucket's parts, the largest few by color, the
// rest together.
func usageStacked(c *ui.Context, p *palette, title string, t *usageTimeline, parts []usagePart, byUser, cost bool, name func(string) string) {
	if len(parts) == 0 || len(t.Buckets) == 0 {
		return
	}
	top := map[string]int{}
	for i, s := range parts[:min(len(parts), 4)] {
		top[s.key] = i
	}
	type stack struct{ values [5]float64 }
	stacks := make([]stack, len(t.Buckets))
	peak := 0.0
	for i, b := range t.Buckets {
		add := func(key string, tokens, costUSD float64) {
			v := tokens
			if cost {
				v = costUSD
			}
			slot, ok := top[key]
			if !ok {
				slot = 4
			}
			stacks[i].values[slot] += v
		}
		if byUser {
			for _, u := range b.ByUser {
				add(u.UserID, u.Tokens, u.CostUSD)
			}
		} else {
			for _, m := range b.ByModel {
				add(m.ModelID, m.Tokens, m.CostUSD)
			}
		}
		total := 0.0
		for _, v := range stacks[i].values {
			total += v
		}
		peak = math.Max(peak, total)
	}
	ui.Column(c.Key(title)).FillWidth().Radius(12).Border(1, p.Border).Padding(16).Gap(12).Margin(4, 0, 0, 0).Children(func() {
		ui.Text(c, title).FontSize(sizeBody).FontWeight(600).TextColor(p.Strong)
		ui.Row(c).FillWidth().Height(140).Gap(2).AlignItems(ui.End).Children(func() {
			for i := range stacks {
				st := stacks[i]
				ui.Column(c.Key(i)).Grow(1).Basis(0).MinWidth(1).Justify(ui.End).Children(func() {
					for slot := 4; slot >= 0; slot-- {
						if st.values[slot] <= 0 || peak <= 0 {
							continue
						}
						color := p.Tertiary.Alpha(0.5)
						if slot < 4 {
							color = usagePalette(p, slot)
						}
						ui.Box(c.Key(slot)).FillWidth().Height(max(1, float32(st.values[slot]/peak*140))).Background(color)
					}
				})
			}
		})
		ui.Row(c).FillWidth().Wrap().Gap(12).Children(func() {
			for i, s := range parts[:min(len(parts), 4)] {
				ui.Row(c.Key(s.key)).Gap(5).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(8, 8).Radius(2).Background(usagePalette(p, i))
					ui.Text(c, name(s.key)).FontSize(sizeCaption).TextColor(p.Secondary)
				})
			}
			if len(parts) > 4 {
				ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(8, 8).Radius(2).Background(p.Tertiary.Alpha(0.5))
					ui.Text(c, locales.UsageOthers()).FontSize(sizeCaption).TextColor(p.Secondary)
				})
			}
		})
	})
}

func slicesIndex(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return 0
}

// bigStat is one of the range's two totals, its digits rolling as
// NumberFlow's when the range changes it.
func (a *App) bigStat(c *ui.Context, p *palette, label string, value float64, format func(float64) string) {
	ui.Column(c.Key("stat/"+label)).Grow(1).Basis(0).Height(140).Padding(18, 18).Radius(12).Border(1, p.Border).Justify(ui.SpaceBetween).Children(func() {
		ui.Text(c, label).FontSize(sizeSub).TextColor(p.Secondary)
		a.rollingNumber(c, "stat/"+label, format(value), value, func(t ui.Element) ui.Element {
			return t.FontSize(40).FontWeight(500).LetterSpacing(-1).TextColor(p.Strong)
		})
	})
}

func smallStat(c *ui.Context, p *palette, label, value, note string) {
	ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(4).Children(func() {
		ui.Text(c, label).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
		ui.Text(c, value).FontSize(sizeBody).FontWeight(500).TextColor(p.Strong).SingleLine()
		if note != "" {
			ui.Text(c, note).FontSize(sizeCaption).TextColor(p.Tertiary).SingleLine()
		}
	})
}

// usageDonut is the models' shares around the range's total. As Lody's,
// it shows settled as the card first does; afterwards each slice goes
// from the length it had to its new one over 850ms, 60ms after the one
// before, a new slice from nothing, while where it starts moves at once.
// settle says the card is showing for the first time.
func (a *App) usageDonut(c *ui.Context, p *palette, parts []usagePart, cost bool, t *usageTimeline, settle bool) {
	total := t.Totals.Tokens
	if cost {
		total = t.Totals.CostUSD
	}
	ui.Column(c).Width(170).Shrink(0).Gap(12).Children(func() {
		ring := ui.Box(c.Key("donut")).Size(170, 170).Center()
		type slice struct {
			from, gap, length float64
			color             ui.Color
		}
		var slices []slice
		from := 0.0
		for i, s := range parts {
			share := 0.0
			if total > 0 {
				share = s.value(cost) / total
			}
			// A hairline between slices, as Lody's donut has.
			gap := math.Min(0.003, share/4)
			key := "slice/" + s.key
			if firstShown(ring, key) && !settle {
				ring.Jump(key, 0)
			}
			length := ring.AnimateTo(key, float32(max(0, math.Min(from+share, 1)-from-2*gap)),
				ui.Animation{Duration: 850 * time.Millisecond, Ease: easeSweep, Delay: time.Duration(i) * 60 * time.Millisecond})
			if share > 0 || length > 0 {
				slices = append(slices, slice{from, gap, float64(length), usagePalette(p, i)})
			}
			from += share
		}
		ring.Draw(func(pt *ui.Painter, r ui.Rect) {
			cx, cy := r.X+r.W/2, r.Y+r.H/2
			outer, inner := float32(83), float32(57)
			pt.FillPath(ringSlice(cx, cy, inner, outer, 0, 1), p.Text.Alpha(0.06))
			for _, s := range slices {
				if s.length > 0 {
					pt.FillPath(ringSlice(cx, cy, inner, outer, s.from+s.gap, math.Min(1, s.from+s.gap+s.length)), s.color)
				}
			}
		}).Children(func() {
			ui.Column(c).AlignItems(ui.Center).Children(func() {
				// The total rolls as NumberFlow's in tokens; in cost it is
				// written plainly.
				if cost {
					ui.Text(c.Key("total-cost"), formatUSD(total)).FontSize(18).FontWeight(600).TextColor(p.Strong)
				} else {
					a.rollingNumber(c, "donut-total", formatCount(total), total, func(t ui.Element) ui.Element {
						return t.FontSize(18).FontWeight(600).TextColor(p.Strong)
					})
				}
				ui.Text(c, map[bool]string{false: "TOKEN", true: "USD"}[cost]).FontSize(sizeCaption).TextColor(p.Tertiary)
			})
		})
		ui.Text(c, locales.UsageByModel()).FontSize(sizeCaption).TextColor(p.Secondary)
		for i, s := range parts[:min(len(parts), 4)] {
			share := 0.0
			if total > 0 {
				share = s.value(cost) / total * 100
			}
			ui.Row(c.Key(s.key)).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(usagePalette(p, i)).Shrink(0)
				ui.Text(c, s.key).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine().Grow(1).MinWidth(0)
				ui.Text(c, fmt.Sprintf("%.0f%%", share)).FontSize(sizeCaption).TextColor(p.Tertiary).Shrink(0)
			})
		}
	})
}

// usageRangePanel is an hourly range: what it spans and its peak over the
// day's skyline or the week's dots. As Lody's, switching between the two
// crossfades them over 200ms, each sharpening out of or blurring into 6px,
// as the new one's tracks sweep in one after another.
func (a *App) usageRangePanel(c *ui.Context, p *palette, t *usageTimeline, cost bool) {
	m := &a.motion
	rng := a.usageRange
	if m.rng != "" && m.rng != rng {
		m.rngFrom, m.rngAt = m.rng, c.Now()
	}
	m.rng = rng
	k := motionClock(c, m.rngAt, 0, 200*time.Millisecond)
	if k >= 1 {
		m.rngFrom = ""
	}
	usageRangeHead(c, p, t, cost)
	matrix := func(rng string, t *usageTimeline, sweep time.Time) {
		if rng == "week" {
			usageWeekMatrix(c, p, t, cost, sweep)
		} else {
			usageDayMatrix(c, p, t, cost, sweep)
		}
	}
	ui.Column(c.Key("range-frame")).FillWidth().Children(func() {
		in := ui.Column(c.Key("range/" + rng)).FillWidth()
		if e := cssEaseOut(k); e < 1 {
			in.Opacity(e).Blur(6 * (1 - e))
		}
		in.Children(func() { matrix(rng, t, m.rngAt) })
		if m.rngFrom == "" {
			return
		}
		old, _ := a.usageTimelineFetch(m.rngFrom).value.(*usageTimeline)
		if old == nil {
			return
		}
		// The range going pops out of the flow where it was.
		out := ui.Column(c.Key("range/" + m.rngFrom)).Absolute().Left(0).Top(0).Right(0).PassThrough()
		e := cssEaseOut(k)
		out.Opacity(1 - e).Blur(6 * e)
		out.Children(func() { matrix(m.rngFrom, old, time.Time{}) })
	})
}

// usageRangeHead says what an hourly range spans, how active it was, its
// peak and the shades' key.
func usageRangeHead(c *ui.Context, p *palette, t *usageTimeline, cost bool) {
	stats := usageStatsOf(t.Buckets, cost)
	fmtv := formatCount
	if cost {
		fmtv = formatUSD
	}
	if len(t.Buckets) > 0 {
		span := bucketTime(t.Buckets[0].BucketStartMs, true) + " — " + bucketTime(t.EndMs, true)
		ui.Text(c, span+" · "+locales.UsageActiveOf(stats.active, len(t.Buckets))).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine()
	}
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		if stats.peak > 0 {
			ui.Text(c, locales.UsageStatPeak()+" "+fmtv(stats.peak)+" · "+bucketTime(stats.peakAt, true)).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine()
		}
		ui.Spacer(c)
		ui.Text(c, locales.UsageLess()).FontSize(sizeCaption).TextColor(p.Tertiary)
		ui.Box(c).Size(80, 6).Radius(3).Draw(func(pt *ui.Painter, r ui.Rect) {
			pt.FillGradient(r, ui.LinearGradient{From: usagePalette(p, 0).Alpha(0.15), To: usagePalette(p, 0), Angle: 90}, 3)
		})
		ui.Text(c, locales.UsageMore()).FontSize(sizeCaption).TextColor(p.Tertiary)
	})
}

// rangeSweep is Lody's RangeSweep on one track of an hourly range, an
// hour's column or a day's row: from a switch at `at`, the tracks resolve
// in order, 22ms apart, each a 5px blur sharpening over 360ms as it rises
// 6px (a column) or slides 8px in from the left (a row).
func rangeSweep(c *ui.Context, track ui.Element, i int, at time.Time, row bool) {
	k := motionClock(c, at, time.Duration(i)*22*time.Millisecond, 360*time.Millisecond)
	if k >= 1 {
		return
	}
	e := easeSweep(k)
	track.Opacity(e).Blur(5 * (1 - e))
	if row {
		track.Translate(-8*(1-e), 0)
	} else {
		track.Translate(0, 6*(1-e))
	}
}

// rangeIntensity is how strongly a range's buckets show, as heatScale.
func rangeIntensity(t *usageTimeline, cost bool) (func(float64) float32, []float64) {
	values := make([]float64, len(t.Buckets))
	for i, b := range t.Buckets {
		values[i] = b.Tokens
		if cost {
			values[i] = b.CostUSD
		}
	}
	return heatScale(values), values
}

// usageDayMatrix is the day's skyline: a bar an hour standing on a
// baseline, as tall as it used, at least 7% of the tallest, its light its
// share of the peak. As Lody's, a bar changing grows and recolors over
// 300ms on CSS's ease; hovering one lights its column and brightens it.
func usageDayMatrix(c *ui.Context, p *palette, t *usageTimeline, cost bool, sweep time.Time) {
	const track = 148
	scale, values := rangeIntensity(t, cost)
	peak := 0.0
	for _, v := range values {
		peak = math.Max(peak, v)
	}
	fmtv := formatCount
	if cost {
		fmtv = formatUSD
	}
	change := ui.Animation{Duration: 300 * time.Millisecond, Ease: cssEase}
	ui.Column(c).FillWidth().Children(func() {
		ui.Row(c).FillWidth().Height(track).Gap(3).Children(func() {
			for i, b := range t.Buckets {
				v := values[i]
				col := ui.Column(c.Key(b.BucketStartMs)).Grow(1).Basis(0).MinWidth(1).Height(track)
				rangeSweep(c, col, i, sweep, false)
				col.Children(func() {
					cell := ui.ButtonBase(c.Key("cell")).FillWidth().Height(track).Radius(3).Justify(ui.End).Label(bucketTime(b.BucketStartMs, true))
					tip(c, p, cell, bucketTime(b.BucketStartMs, true)+" · "+fmtv(v))
					hovered := cell.Hovered()
					if hovered {
						cell.Background(p.Secondary.Alpha(0.06))
					}
					h := float32(0)
					if v > 0 && peak > 0 {
						h = float32(math.Max(7, v/peak*100)) / 100 * track
					}
					fill := ui.Transparent
					if v > 0 {
						fill = usagePalette(p, 0).Alpha(0.18 + 0.74*scale(v))
					}
					fill = cell.AnimateColor("fill", fill, change)
					if hovered {
						fill = cssBrightness(fill, 1.1)
					}
					cell.Children(func() {
						ui.Box(c.Key("bar")).FillWidth().Height(cell.AnimateTo("height", h, change)).Radius(3, 3, 0, 0).Background(fill)
					})
				})
			}
		})
		ui.Box(c).FillWidth().Height(1).Background(p.Border.Alpha(0.7))
		// The hours, labelled every three: a label on all 24 is noise.
		ui.Row(c).FillWidth().Gap(3).Margin(6, 0, 0, 0).Children(func() {
			for i, b := range t.Buckets {
				label := ""
				if i%3 == 0 {
					label = time.UnixMilli(int64(b.BucketStartMs)).UTC().Format("15")
				}
				ui.Text(c.Key(i), label).FontSize(9).TextColor(p.Secondary.Alpha(0.6)).TextAlign(ui.Center).FontFeatures("tnum").
					Grow(1).Basis(0).MinWidth(1).SingleLine()
			}
		})
	})
}

// usageWeekMatrix is the week: the day's 24 hour tracks stacked seven
// deep, a dot an hour that grows from 5 to 13 and brightens with what it
// used. As Lody's, a dot changing grows and recolors over 300ms; hovering
// one brightens it over 300ms and rings it at once.
func usageWeekMatrix(c *ui.Context, p *palette, t *usageTimeline, cost bool, sweep time.Time) {
	const day, hour = 86400000.0, 3600000.0
	scale, values := rangeIntensity(t, cost)
	byStart := map[float64]float64{}
	for i, b := range t.Buckets {
		byStart[b.BucketStartMs] = values[i]
	}
	first := math.Floor(t.StartMs/day) * day
	last := math.Floor(math.Max(t.StartMs, t.EndMs-1)/day) * day
	var days []float64
	for d := first; d <= last && len(days) < 8; d += day {
		days = append(days, d)
	}
	fmtv := formatCount
	if cost {
		fmtv = formatUSD
	}
	change := ui.Animation{Duration: 300 * time.Millisecond, Ease: cssEase}
	zh := strings.HasPrefix(i18n.Locale(), "zh")
	ui.Row(c).FillWidth().Gap(8).Children(func() {
		// The days' gutter, outside the rows so the sweep cannot drag it.
		ui.Column(c).Shrink(0).Gap(3).Children(func() {
			for _, d := range days {
				tm := time.UnixMilli(int64(d)).UTC()
				weekday := tm.Format("Mon")
				if zh {
					weekday = [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[tm.Weekday()]
				}
				ui.Row(c.Key(d)).Height(18).Gap(4).AlignItems(ui.Center).Justify(ui.End).Children(func() {
					ui.Text(c, weekday).FontSize(10).TextColor(p.Secondary)
					ui.Text(c, tm.Format("2")).FontSize(10).TextColor(p.Secondary.Alpha(0.55)).FontFeatures("tnum")
				})
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			for di, d := range days {
				row := ui.Row(c.Key(d)).FillWidth().Height(18).Gap(3)
				rangeSweep(c, row, di, sweep, true)
				row.Children(func() {
					for h := 0; h < 24; h++ {
						at := d + float64(h)*hour
						v := byStart[at]
						cell := ui.ButtonBase(c.Key(h)).Grow(1).Basis(0).MinWidth(1).Height(18).Center().Label(bucketTime(at, true))
						tip(c, p, cell, bucketTime(at, true)+" · "+fmtv(v))
						heat := scale(v)
						size, fill := float32(4), p.Text.Alpha(0.08)
						if heat > 0 {
							size, fill = 5+8*heat, usagePalette(p, 0).Alpha(0.18+0.74*heat)
						}
						hovered := cell.Hovered()
						fill = cssBrightness(cell.AnimateColor("fill", fill, change), 1+0.1*cell.AnimateTo("bright", b2f(hovered), change))
						size = cell.AnimateTo("size", size, change)
						cell.Children(func() {
							dot := ui.Box(c.Key("dot")).Size(size, size).Radius(size / 2).Background(fill)
							if hovered {
								dot.Shadow(0, 0, 0, 1, p.Text.Alpha(0.4))
							}
						})
					}
				})
			}
		})
	})
}

// usageShareBar is a split as one bar of parts, with its legend.
func usageShareBar(c *ui.Context, p *palette, title string, parts []usagePart, cost bool, name func(string) string) {
	total := 0.0
	for _, s := range parts {
		total += s.value(cost)
	}
	ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(8).Children(func() {
		ui.Text(c, title).FontSize(sizeFoot).TextColor(p.Secondary)
		ui.Row(c).FillWidth().Height(6).Radius(3).Gap(2).Clip().Background(p.Text.Alpha(0.06)).Children(func() {
			for i, s := range parts {
				if total > 0 && s.value(cost) > 0 {
					// Each part grows to its share over 500ms.
					seg := ui.Box(c.Key(s.key)).Shrink(1).MinWidth(1).Height(6).Background(usagePalette(p, i))
					grow := seg.Keyframes("grow", []ui.Keyframe{{Value: 0}, {At: 500 * time.Millisecond, Value: 1, Ease: easeSweep}})
					seg.BasisPercent(grow * seg.AnimateWith("share", float32(s.value(cost)/total*100), 500*time.Millisecond, easeSweep))
				}
			}
		})
		ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
			for i, s := range parts[:min(len(parts), 6)] {
				share := 0.0
				if total > 0 {
					share = s.value(cost) / total * 100
				}
				ui.Row(c.Key(s.key)).Gap(5).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(6, 6).Radius(3).Background(usagePalette(p, i))
					ui.Text(c, fmt.Sprintf("%s %.0f%%", name(s.key), share)).FontSize(sizeCaption).TextColor(p.Secondary)
				})
			}
		})
	})
}

// ringSlice is the part of a ring from one fraction of the turn to
// another, clockwise from the top.
func ringSlice(cx, cy, inner, outer float32, from, to float64) *ui.Path {
	path := new(ui.Path)
	if to <= from {
		return path
	}
	steps := max(2, int(math.Ceil((to-from)*96)))
	point := func(r float32, f float64) (float32, float32) {
		angle := 2*math.Pi*f - math.Pi/2
		return cx + r*float32(math.Cos(angle)), cy + r*float32(math.Sin(angle))
	}
	x, y := point(outer, from)
	path.MoveTo(x, y)
	for i := 1; i <= steps; i++ {
		x, y = point(outer, from+(to-from)*float64(i)/float64(steps))
		path.LineTo(x, y)
	}
	for i := steps; i >= 0; i-- {
		x, y = point(inner, from+(to-from)*float64(i)/float64(steps))
		path.LineTo(x, y)
	}
	return path.Close()
}

// usageTimelineFetch is a range's usage, by bucket.
func (a *App) usageTimelineFetch(rng string) *fetched {
	convex, ws := a.convex, a.workspaceID
	return a.fetch("usage/"+rng, func(ctx context.Context) (any, error) {
		var out usageTimeline
		args := map[string]any{"workspaceId": ws, "range": rng}
		if rng == "day" || rng == "week" {
			args["granularity"] = "hour" // as Lody asks; the server picks for longer ranges
		}
		err := convex.Query(ctx, "usage:getWorkspaceUsageTimeline", args, &out)
		return &out, err
	})
}

// usageViewFade fades a view of the usage card in as it comes and out as
// it goes, in place, over d on CSS's ease-out: not as the card first shows.
func usageViewFade(d time.Duration) ui.ElementTransition {
	return ui.ElementTransition{Colors: true, Duration: d, Ease: cssEaseOut, Enter: &ui.Motion{}, Exit: &ui.Motion{}}
}

// usageRangeTray is Lody's range selector: a bordered tray of the four
// ranges, the chosen one raised on the page's ground; the fills and inks
// crossfade over 150ms on Tailwind's curve, and nothing slides.
func usageRangeTray(c *ui.Context, p *palette, selected string, pick func(string)) {
	labels := usageRangeLabels()
	fade := ui.Animation{Duration: 150 * time.Millisecond, Ease: twEase}
	ui.Row(c.Key("usage-range")).Gap(2).Padding(2).Radius(8).Border(1, p.Border.Alpha(0.6)).Background(p.Text.Alpha(0.03)).
		Shrink(0).Label(locales.SettingsUsage()).Children(func() {
		for i, r := range usageRanges {
			id := r
			on := r == selected
			tab := ui.ButtonBase(c.Key(r)).Padding(4, 12).Radius(6).Cursor(ui.CursorPointer).Label(labels[i]).OnClick(func() { pick(id) })
			if k := tab.AnimateTo("on", b2f(on), fade); k > 0 {
				tab.Background(p.Canvas.Alpha(k)).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.05*k))
			}
			ink := tab.AnimateColor("ink", map[bool]ui.Color{true: p.Text, false: p.Secondary}[on || tab.Hovered()], fade)
			tab.Children(func() { ui.Text(c, labels[i]).FontSize(sizeFoot).TextColor(ink).SingleLine() })
		}
	})
}

// usageMetricSegments is the usage card's tokens-or-cost switch, as Lody's
// SegmentedControl: the chosen segment raised and ringed, the fills and
// inks crossfading over 150ms on CSS's ease.
func usageMetricSegments(c *ui.Context, p *palette, cost bool, pick func(bool)) {
	fade := ui.Animation{Duration: 150 * time.Millisecond, Ease: cssEase}
	ui.Row(c.Key("usage-metric")).Padding(2).Radius(6).Background(p.Text.Alpha(0.05)).Shrink(0).Label(locales.UsageSkyline()).Children(func() {
		for _, o := range []struct {
			cost  bool
			label string
		}{{false, "Token"}, {true, locales.UsageCostUSD()}} {
			v := o.cost
			on := v == cost
			seg := ui.ButtonBase(c.Key(o.label)).Padding(4, 10).Radius(5).Cursor(ui.CursorPointer).Label(o.label).OnClick(func() { pick(v) })
			if k := seg.AnimateTo("on", b2f(on), fade); k > 0 {
				seg.Background(p.Canvas.Alpha(k)).Shadow(0, 0, 0, 1, p.Border.Alpha(0.7*k))
			}
			ink := seg.AnimateColor("ink", map[bool]ui.Color{true: p.Text, false: p.Secondary}[on || seg.Hovered()], fade)
			seg.Children(func() { ui.Text(c, o.label).FontSize(sizeFoot).TextColor(ink).SingleLine() })
		}
	})
}
