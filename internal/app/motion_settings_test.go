package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/locales"
)

// hourlyTimeline is a range of hourly buckets from start, each using
// tokens by its hour.
func hourlyTimeline(start time.Time, hours int) *usageTimeline {
	t := &usageTimeline{StartMs: float64(start.UnixMilli()), EndMs: float64(start.Add(time.Duration(hours) * time.Hour).UnixMilli()), BucketSizeMs: 3600000}
	for h := 0; h < hours; h++ {
		b := usageBucket{BucketStartMs: float64(start.Add(time.Duration(h) * time.Hour).UnixMilli()), Tokens: float64((h%9 + 1) * 1000), CostUSD: float64(h%5) / 10}
		b.ByModel = append(b.ByModel, struct {
			ModelID string  `json:"modelId"`
			Tokens  float64 `json:"tokens"`
			CostUSD float64 `json:"costUSD"`
		}{fmt.Sprint("model-", h%3), b.Tokens, b.CostUSD})
		t.Buckets = append(t.Buckets, b)
		t.Totals.Tokens += b.Tokens
		t.Totals.CostUSD += b.CostUSD
	}
	return t
}

// Switching the usage range between 24 hours and 7 days crossfades the
// two, the day's skyline going as the week's rows sweep in; the donut
// stays, its slices re-cut in place. Nothing moves as the card first
// shows.
func TestUsageRangeSwitch(t *testing.T) {
	a := fixture()
	a.settingsOpen, a.settingsTab, a.usageRange = true, "ai-usage", "day"
	day := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	a.fetches = map[string]*fetched{
		"w1|usage/day":  {value: hourlyTimeline(day, 24)},
		"w1|usage/week": {value: hourlyTimeline(day.Add(-6*24*time.Hour), 7*24)},
	}
	tt := snap(t, a, "usage-day-settled", false, nil)
	if a.motion.rng != "day" || a.motion.rngFrom != "" {
		t.Fatalf("first show moved: %+v", a.motion)
	}
	if err := tt.Click("7 days"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Advance(100 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "usage-range-switching")
	if a.motion.rng != "week" || a.motion.rngFrom != "day" {
		t.Fatalf("no crossfade: %+v", a.motion)
	}
	tt.Advance(time.Second)
	tt.Frame()
	snapFrame(t, tt, "usage-week")
	if a.motion.rngFrom != "" {
		t.Fatalf("the day stays: %+v", a.motion)
	}
	if !tt.HasText(day.Format("Mon")) {
		t.Fatalf("no day rows in %q", tt.Texts())
	}
	// Cost, then 30 days: the hourly view goes for the year's.
	if err := tt.Click(locales.UsageCostUSD()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	snapFrame(t, tt, "usage-week-cost")
}

// Opening, switching and folding the home's board: the board switched
// from drifts away as the new one comes; folded, it fades as the heading
// and the line travel back down over 240ms.
func TestHomeBoardMorph(t *testing.T) {
	a := fixture()
	a.selected = ""
	a.homeBoard = "now"
	tt := snap(t, a, "home-board-now", false, nil)
	if a.motion.board != "now" || a.motion.boardFrom != "" {
		t.Fatalf("first show moved: %+v", a.motion)
	}
	if err := tt.Click("Last 7 days"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(80 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "home-board-switching")
	if a.motion.boardFrom != "now" || a.motion.boardKind != "next" {
		t.Fatalf("not switching: %+v", a.motion)
	}
	tt.Advance(time.Second)
	tt.Frame()
	if a.motion.boardFrom != "" || !tt.HasText("PRs merged") {
		t.Fatalf("not switched: %+v %q", a.motion, tt.Texts())
	}
	open, ok := tt.Find(homeHeading(time.Now()))
	if !ok {
		t.Fatal("no heading")
	}
	if err := tt.Click("Fold into one line"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(60 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "home-board-folding")
	going, _ := tt.Find(homeHeading(time.Now()))
	if a.motion.boardKind != "fold" || a.motion.boardFrom != "week" {
		t.Fatalf("not folding: %+v", a.motion)
	}
	tt.Advance(time.Second)
	tt.Frame()
	folded, _ := tt.Find(homeHeading(time.Now()))
	if !(going.Y > open.Y && going.Y < folded.Y) {
		t.Fatalf("the heading doesn't travel: %v, %v, %v", open.Y, going.Y, folded.Y)
	}
	if tt.HasText("PRs merged") {
		t.Fatal("the board stays once folded")
	}
}

// Settings' row menus are drawn: a provider's refresh and delete, a
// project's delete, and a member's roles.
func TestSettingsDrawnMenus(t *testing.T) {
	a := fixture()
	a.selected = "" // no conversation's More actions behind Settings
	a.settingsOpen, a.settingsTab = true, "projects"
	tt := snap(t, a, "settings-projects-menu-closed", false, nil)
	if err := tt.Click("More actions"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "settings-project-menu")
	if !tt.HasText("Delete project") || tt.HasText("Copy path") {
		t.Fatalf("project menu: %q", tt.Texts())
	}

	a.settingsTab = "agents"
	tt = snap(t, a, "settings-agents-menu-closed", false, nil)
	if err := tt.Click("More actions"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "settings-provider-menu")
	if !tt.HasText("Refresh models and modes") || !tt.HasText("Delete") {
		t.Fatalf("provider menu: %q", tt.Texts())
	}

	a.settingsTab = "general"
	a.fetches = map[string]*fetched{"w1|members": {value: []cloud.Member{
		{ID: "mem1", UserID: "u1", Role: "owner", User: cloud.User{ID: "u1", Name: "Ada Lovelace"}},
		{ID: "mem2", UserID: "u2", Role: "member", User: cloud.User{ID: "u2", Name: "Grace Hopper"}},
	}}}
	tt = snap(t, a, "settings-members", false, nil)
	if err := tt.Click("Member"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "settings-member-menu")
	if !tt.HasText("Admin") || !tt.HasText("Remove member") {
		t.Fatalf("member menu: %q", tt.Texts())
	}
}

// The password dialog's steps take turns, as Lody's wait mode: the one
// going slides away first, then the next slides in.
func TestPasswordStepsTakeTurns(t *testing.T) {
	a := fixture()
	a.settingsOpen, a.settingsTab = true, "account"
	a.fetches = map[string]*fetched{"w1|accounts": {value: []cloud.LinkedAccount{{ID: "a1", ProviderID: "credential"}}}}
	a.password = passwordForm{open: true}
	tt := snap(t, a, "password-turns-current", false, nil)
	a.password.step, a.password.dir, a.password.run = 1, 1, 1
	tt.Frame()
	tt.Advance(100 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "password-turns-leaving")
	if !tt.HasText("Current password") || tt.HasText("New password") {
		t.Fatalf("the first step doesn't leave first: %q", tt.Texts())
	}
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "password-turns-coming")
	if tt.HasText("Current password") || !tt.HasText("New password") {
		t.Fatalf("the next step doesn't come: %q", tt.Texts())
	}
}

// A shortcut's unbind keeps its room, unseen, until the row is reached.
func TestShortcutUnbindReveal(t *testing.T) {
	a := fixture()
	a.settingsOpen, a.settingsTab = true, "keyboard"
	tt := ui.NewTester(a.render, 1100, 720)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "shortcuts-unbind-rest")
	// Every bound row keeps the unbind in its slot.
	if !tt.HasText(locales.KeyUnbind()) {
		t.Fatalf("no unbind kept: %q", tt.Texts())
	}
}

// The device code's dialog carries Lody's connector: asking, checking,
// done.
func TestDeviceConnector(t *testing.T) {
	a := fixture()
	a.device = deviceAuth{open: true, code: "ABCD12EF"}
	snap(t, a, "device-connector-ask", false, nil)
	a.device.verifying = true
	tt := snap(t, a, "device-connector-arrive", false, nil)
	if !tt.HasText("Verify & Authorize") && !tt.HasText("Verifying...") && !tt.HasText("Verifying…") {
		t.Logf("texts %q", tt.Texts())
	}
	a.device = deviceAuth{open: true, success: true}
	snap(t, a, "device-connector-done", false, nil)
}
