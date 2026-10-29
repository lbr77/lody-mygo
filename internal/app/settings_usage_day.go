package app

import (
	"context"
	"sort"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// A day of usage, as Lody's calendar opens it under the heatmap when a
// day is clicked: the day's tokens and cost, what the tokens were, and
// who and which models spent them (usage:getWorkspaceUsageDay).

type usageDayDetail struct {
	DayStartMs float64 `json:"dayStartMs"`
	Totals     struct {
		Tokens              float64 `json:"tokens"`
		CostUSD             float64 `json:"costUSD"`
		InputTokens         float64 `json:"inputTokens"`
		OutputTokens        float64 `json:"outputTokens"`
		CacheReadTokens     float64 `json:"cacheReadInputTokens"`
		CacheCreationTokens float64 `json:"cacheCreationInputTokens"`
		ReasoningTokens     float64 `json:"reasoningOutputTokens"`
		WebSearchRequests   float64 `json:"webSearchRequests"`
	} `json:"totals"`
	ByModel []struct {
		ModelID string  `json:"modelId"`
		Tokens  float64 `json:"tokens"`
	} `json:"byModel"`
	ByUser []struct {
		UserID string  `json:"userId"`
		Tokens float64 `json:"tokens"`
	} `json:"byUser"`
	Users map[string]struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"users"`
}

// usageDayPanel opens below the heatmap as a day is picked, growing in
// with a little overshoot as Lody's does, and folding away when closed.
func (a *App) usageDayPanel(c *ui.Context, p *palette) {
	selected := a.usageDay
	if selected != 0 {
		a.usageDayShown = selected
	}
	host := ui.Column(c.Key("usage-day")).FillWidth()
	k := host.AnimateWith("open", b2f(selected != 0), 450*time.Millisecond, ui.CubicBezier(0.34, 1.25, 0.64, 1))
	if k <= 0.001 && selected == 0 {
		a.usageDayShown = 0
		return
	}
	day := a.usageDayShown
	var inner ui.Element
	host.ClipY().Children(func() {
		inner = ui.Column(c).FillWidth().Padding(12, 0, 0, 0).Children(func() { a.usageDayDetail(c, p, day) })
	})
	if k < 1 {
		host.Height(max(0, k) * inner.Bounds().H).Opacity(min(1, max(0, k)))
	}
}

func (a *App) usageDayDetail(c *ui.Context, p *palette, dayMs float64) {
	convex, ws := a.convex, a.workspaceID
	f := a.fetch("usage-day/"+time.UnixMilli(int64(dayMs)).Format("2006-01-02"), func(ctx context.Context) (any, error) {
		var out *usageDayDetail
		err := convex.Query(ctx, "usage:getWorkspaceUsageDay", map[string]any{"workspaceId": ws, "dayStartMs": dayMs}, &out)
		return out, err
	})
	d, _ := f.value.(*usageDayDetail)
	if d != nil && d.DayStartMs != dayMs {
		d = nil
	}
	ui.Column(c).FillWidth().Padding(14, 16).Gap(12).Radius(12).Border(1, p.Border).Background(p.Canvas).Children(func() {
		ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, usageDayLabel(dayMs)).FontSize(sizeFoot).TextColor(p.Secondary)
				ui.Row(c).Gap(6).AlignItems(ui.Baseline).Children(func() {
					total := "—"
					if d != nil {
						total = formatCount(d.Totals.Tokens)
					}
					ui.Text(c, total).FontSize(22).FontWeight(600).TextColor(p.Strong).FontFeatures("tnum")
					ui.Text(c, locales.UsageTokens()).FontSize(sizeFoot).TextColor(p.Secondary)
				})
				if d != nil {
					line := formatUSD(d.Totals.CostUSD)
					if n := int(d.Totals.WebSearchRequests); n > 0 {
						line += " · " + locales.UsageWebSearches(n)
					}
					ui.Text(c, line).FontSize(sizeFoot).TextColor(p.Secondary)
				}
			})
			iconButton(c, p, iconX, locales.Close(), p.Hover).OnClick(func() { a.usageDay = 0 })
		})
		switch {
		case d == nil && f.err != "":
			ui.Text(c, f.err).FontSize(sizeFoot).TextColor(p.Danger).Selectable()
		case d == nil:
			skeleton(c, p, "day-1", 12).FillWidth()
			skeleton(c, p, "day-2", 12).WidthPercent(60)
		case d.Totals.Tokens <= 0:
			ui.Text(c, locales.UsageNoUsage()).FontSize(sizeFoot).TextColor(p.Secondary)
		default:
			a.usageDayBreakdown(c, p, d)
		}
	})
}

// usageDayLabel is the day as Lody writes it: Wednesday, October 29.
func usageDayLabel(ms float64) string {
	t := time.UnixMilli(int64(ms))
	if len(i18n.Locale()) >= 2 && i18n.Locale()[:2] == "zh" {
		return t.Format("1月2日 ") + [...]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}[t.Weekday()]
	}
	return t.Format("Monday, January 2")
}

func (a *App) usageDayBreakdown(c *ui.Context, p *palette, d *usageDayDetail) {
	type part struct {
		label string
		value float64
		icon  func() // what leads a ranked row: the model's maker, the member
	}
	parts := []part{
		{locales.UsageCache(), d.Totals.CacheReadTokens + d.Totals.CacheCreationTokens, nil},
		{locales.UsageInput(), d.Totals.InputTokens, nil},
		{locales.UsageOutput(), d.Totals.OutputTokens, nil},
		{locales.UsageReasoning(), d.Totals.ReasoningTokens, nil},
	}
	sum := 0.0
	var shown []part
	for _, s := range parts {
		if s.value > 0 {
			shown = append(shown, s)
			sum += s.value
		}
	}
	sort.SliceStable(shown, func(i, j int) bool { return shown[i].value > shown[j].value })
	ui.Row(c).FillWidth().Height(6).Radius(3).Gap(2).Clip().Background(p.Text.Alpha(0.06)).Children(func() {
		for i, s := range shown {
			ui.Box(c.Key(s.label)).BasisPercent(float32(s.value / sum * 100)).Shrink(1).MinWidth(1).Height(6).Background(usagePalette(p, i))
		}
	})
	ui.Row(c).FillWidth().Wrap().Gap(12).Children(func() {
		for i, s := range shown {
			ui.Row(c.Key(s.label)).Gap(5).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(usagePalette(p, i))
				ui.Text(c, s.label).FontSize(sizeCaption).TextColor(p.Secondary)
				ui.Text(c, formatCount(s.value)).FontSize(sizeCaption).TextColor(p.Text).FontFeatures("tnum")
			})
		}
	})
	ranked := func(title string, rows []part) {
		top := 0.0
		for _, r := range rows {
			top = max(top, r.value)
		}
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(6).Children(func() {
			ui.Text(c, title).FontSize(sizeCaption).TextColor(p.Secondary)
			for _, r := range rows {
				ui.Column(c.Key(r.label)).FillWidth().Gap(3).Children(func() {
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						if r.icon != nil {
							r.icon()
						}
						ui.Text(c, r.label).FontSize(sizeFoot).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
						ui.Text(c, formatCount(r.value)).FontSize(sizeFoot).TextColor(p.Secondary).FontFeatures("tnum")
					})
					ui.Row(c).FillWidth().Height(4).Radius(2).Background(p.Text.Alpha(0.06)).Children(func() {
						if top > 0 {
							ui.Box(c).WidthPercent(float32(r.value / top * 100)).Height(4).Radius(2).Background(usagePalette(p, 0))
						}
					})
				})
			}
		})
	}
	var models, people []part
	for _, m := range d.ByModel {
		mark := modelMark(m.ModelID)
		models = append(models, part{m.ModelID, m.Tokens, func() { ui.Icon(c, mark).Size(14, 14).TextColor(p.Secondary).Shrink(0) }})
	}
	for _, u := range d.ByUser {
		who := d.Users[u.UserID]
		name := firstNonEmpty(who.Name, who.Email, u.UserID)
		people = append(people, part{name, u.Tokens, func() { avatar(c, p, name, 16, 8) }})
	}
	ui.Row(c).FillWidth().Gap(24).AlignItems(ui.Start).Children(func() {
		ranked(locales.UsageByModel(), models)
		ranked(locales.UsageByMember(), people)
	})
}
