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
		picked := slicesIndex(usageRanges, a.usageRange)
		was := picked
		ui.Segmented(c, &picked, usageRangeLabels()...).Label(locales.SettingsUsage())
		if picked != was {
			a.usageRange = usageRanges[picked]
		}
	})
	convex, ws, rng := a.convex, a.workspaceID, a.usageRange
	f := a.fetch("usage/"+rng, func(ctx context.Context) (any, error) {
		var out usageTimeline
		args := map[string]any{"workspaceId": ws, "range": rng}
		if rng == "day" || rng == "week" {
			args["granularity"] = "hour" // as Lody asks; the server picks for longer ranges
		}
		err := convex.Query(ctx, "usage:getWorkspaceUsageTimeline", args, &out)
		return &out, err
	})
	if !pending(c, p, f) {
		return
	}
	t, _ := f.value.(*usageTimeline)
	if t == nil {
		return
	}
	ui.Row(c).FillWidth().Gap(12).Margin(4, 0).Children(func() {
		bigStat(c, p, "Token", formatCount(t.Totals.Tokens))
		bigStat(c, p, locales.UsageCostUSD(), formatUSD(t.Totals.CostUSD))
	})
	cost := a.usageCost
	models := usageSplit(t.Buckets, false)
	people := usageSplit(t.Buckets, true)
	ui.Column(c).FillWidth().Radius(12).Border(1, p.Border).Clip().Children(func() {
		ui.Column(c).FillWidth().Padding(16).Gap(16).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, locales.UsageSkyline()).FontSize(sizeBody).FontWeight(600).TextColor(p.Strong)
					ui.Text(c, usageRangeSubtitle(rng)).FontSize(sizeFoot).TextColor(p.Secondary)
				})
				picked := map[bool]int{false: 0, true: 1}[cost]
				ui.Segmented(c, &picked, "Token", locales.UsageCostUSD()).Label(locales.UsageSkyline())
				a.usageCost = picked == 1
			})
			if rng == "day" || rng == "week" {
				ui.Row(c).FillWidth().Gap(24).AlignItems(ui.Start).Children(func() {
					usageDonut(c, p, models, cost, t)
					usageSkyline(c, p, t, cost, t.BucketSizeMs > 0 && t.BucketSizeMs < 86400000)
				})
			} else if days := a.usageCalendar(); days != nil {
				// Longer ranges show the year, the range's days lit.
				usageHeatmap(c, p, days, cost, rng == "month")
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

// usageHeatmap is the year a column a week, a cell a day.
func usageHeatmap(c *ui.Context, p *palette, days []usageDay, cost, month bool) {
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
	ui.Row(c).FillWidth().Gap(2).Children(func() {
		for w := 0; w*7 < len(days); w++ {
			ui.Column(c.Key(w)).Gap(2).Children(func() {
				for i := w * 7; i < min(len(days), w*7+7); i++ {
					d := days[i]
					cell := ui.Box(c.Key(i)).Size(9, 9).Radius(2).Tooltip(d.Date + " · " + fmtv(values[i]))
					switch {
					case d.IsFuture:
						cell.Background(p.Text.Alpha(0.02))
					case values[i] > 0:
						cell.Background(usagePalette(p, 0).Alpha(0.18 + 0.74*heat(values[i])))
					default:
						cell.Background(p.Text.Alpha(0.06))
					}
					if month && d.DayStartMs < float64(window) {
						cell.Opacity(0.3)
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

func bigStat(c *ui.Context, p *palette, label, value string) {
	ui.Column(c).Grow(1).Basis(0).Height(140).Padding(18, 18).Radius(12).Border(1, p.Border).Justify(ui.SpaceBetween).Children(func() {
		ui.Text(c, label).FontSize(sizeSub).TextColor(p.Secondary)
		ui.Text(c, value).FontSize(40).FontWeight(500).LetterSpacing(-1).TextColor(p.Strong).SingleLine()
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

// usageDonut is the models' shares around the range's total.
func usageDonut(c *ui.Context, p *palette, parts []usagePart, cost bool, t *usageTimeline) {
	total := t.Totals.Tokens
	if cost {
		total = t.Totals.CostUSD
	}
	ui.Column(c).Width(170).Shrink(0).Gap(12).Children(func() {
		ui.Box(c).Size(170, 170).Center().Draw(func(pt *ui.Painter, r ui.Rect) {
			cx, cy := r.X+r.W/2, r.Y+r.H/2
			outer, inner := float32(83), float32(57)
			pt.FillPath(ringSlice(cx, cy, inner, outer, 0, 1), p.Text.Alpha(0.06))
			if total <= 0 {
				return
			}
			from := 0.0
			for i, s := range parts {
				share := s.value(cost) / total
				if share <= 0 {
					continue
				}
				// A hairline between slices, as Lody's donut has.
				gap := math.Min(0.003, share/4)
				pt.FillPath(ringSlice(cx, cy, inner, outer, from+gap, math.Min(from+share, 1)-gap), usagePalette(p, i))
				from += share
			}
		}).Children(func() {
			ui.Column(c).AlignItems(ui.Center).Children(func() {
				v := formatCount(total)
				if cost {
					v = formatUSD(total)
				}
				ui.Text(c, v).FontSize(18).FontWeight(600).TextColor(p.Strong)
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

// usageSkyline is a bar a bucket, shaded by how much it used.
func usageSkyline(c *ui.Context, p *palette, t *usageTimeline, cost, hourly bool) {
	stats := usageStatsOf(t.Buckets, cost)
	fmtv := formatCount
	if cost {
		fmtv = formatUSD
	}
	ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
		if len(t.Buckets) > 0 {
			span := bucketTime(t.Buckets[0].BucketStartMs, true) + " — " + bucketTime(t.EndMs, true)
			ui.Text(c, span+" · "+locales.UsageActiveOf(stats.active, len(t.Buckets))).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine()
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if stats.peak > 0 {
				ui.Text(c, locales.UsageStatPeak()+" "+fmtv(stats.peak)+" · "+bucketTime(stats.peakAt, hourly)).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine()
			}
			ui.Spacer(c)
			ui.Text(c, locales.UsageLess()).FontSize(sizeCaption).TextColor(p.Tertiary)
			ui.Box(c).Size(80, 6).Radius(3).Draw(func(pt *ui.Painter, r ui.Rect) {
				pt.FillGradient(r, ui.LinearGradient{From: usagePalette(p, 0).Alpha(0.15), To: usagePalette(p, 0), Angle: 90}, 3)
			})
			ui.Text(c, locales.UsageMore()).FontSize(sizeCaption).TextColor(p.Tertiary)
		})
		values := make([]float64, len(t.Buckets))
		for i, b := range t.Buckets {
			values[i] = b.Tokens
			if cost {
				values[i] = b.CostUSD
			}
		}
		scale := heatScale(values)
		ui.Row(c).FillWidth().Height(150).Gap(3).AlignItems(ui.End).BorderWidth(0, 0, 1, 0).BorderColor(p.Border).Children(func() {
			for i, b := range t.Buckets {
				v := values[i]
				h, heat := float32(0), scale(v)
				if stats.peak > 0 && v > 0 {
					// At least 7% high, as Lody's bars.
					h = max(0.07, float32(v/stats.peak)) * 146
				}
				bar := ui.Box(c.Key(i)).Grow(1).Basis(0).MinWidth(1).Height(max(h, 0)).Radius(2).
					Tooltip(bucketTime(b.BucketStartMs, hourly) + " · " + fmtv(v))
				if v > 0 {
					bar.Background(usagePalette(p, 0).Alpha(0.18 + 0.74*heat))
				}
			}
		})
		// Axis labels at even steps, about eight of them.
		ui.Row(c).FillWidth().Gap(3).Children(func() {
			step := max(1, int(math.Ceil(float64(len(t.Buckets))/8)))
			for i, b := range t.Buckets {
				label := ""
				if i%step == 0 {
					tm := time.UnixMilli(int64(b.BucketStartMs)).UTC()
					label = tm.Format("02")
					if hourly {
						label = tm.Format("15")
					}
				}
				ui.Text(c.Key(i), label).FontSize(sizeCaption).TextColor(p.Tertiary).Grow(1).Basis(0).MinWidth(1).SingleLine()
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
					ui.Box(c.Key(s.key)).BasisPercent(float32(s.value(cost) / total * 100)).Shrink(1).MinWidth(1).Height(6).Background(usagePalette(p, i))
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
