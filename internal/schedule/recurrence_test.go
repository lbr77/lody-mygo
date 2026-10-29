package schedule

import (
	"reflect"
	"testing"
	"time"
)

func TestRecurrenceRoundTrip(t *testing.T) {
	now := time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		r    Recurrence
		want Trigger
	}{
		{Recurrence{Kind: "daily", Hour: 9, Minute: 0, TimeZone: "Asia/Shanghai"}, Trigger{Kind: "cron", Expression: "0 9 * * *", TimeZone: "Asia/Shanghai"}},
		{Recurrence{Kind: "weekdays", Hour: 18, Minute: 30, TimeZone: "UTC"}, Trigger{Kind: "cron", Expression: "30 18 * * 1-5", TimeZone: "UTC"}},
		{Recurrence{Kind: "weekly", Hour: 7, Minute: 5, Weekdays: []int{5, 1, 1}, TimeZone: "UTC"}, Trigger{Kind: "cron", Expression: "5 7 * * 1,5", TimeZone: "UTC"}},
		{Recurrence{Kind: "monthly", Hour: 0, Minute: 0, Days: []int{15, 1}, TimeZone: "UTC"}, Trigger{Kind: "cron", Expression: "0 0 1,15 * *", TimeZone: "UTC"}},
		{Recurrence{Kind: "hours", Every: 6, TimeZone: "UTC"}, Trigger{Kind: "cron", Expression: "0 */6 * * *", TimeZone: "UTC"}},
		{Recurrence{Kind: "minutes", Every: 15, TimeZone: "UTC"}, Trigger{Kind: "cron", Expression: "*/15 * * * *", TimeZone: "UTC"}},
		{Recurrence{Kind: "once", At: now}, Trigger{Kind: "once", At: "2026-10-29T08:00:00.000Z"}},
	} {
		got, err := TriggerOf(c.r, now)
		if err != nil || got != c.want {
			t.Errorf("%s: %+v, %v", c.r.Kind, got, err)
			continue
		}
		back := RecurrenceOf(got, "UTC")
		if c.r.Kind == "weekly" {
			c.r.Weekdays = []int{1, 5}
		}
		if c.r.Kind == "monthly" {
			c.r.Days = []int{1, 15}
		}
		if !reflect.DeepEqual(back, c.r) {
			t.Errorf("%s back: %+v", c.r.Kind, back)
		}
	}
	if tr, _ := TriggerOf(Recurrence{Kind: "hours", Every: 5}, now); tr.Kind != "interval" || tr.EveryMs != 5*3_600_000 || tr.AnchorAt != "2026-10-29T08:00:00.000Z" {
		t.Errorf("uneven hours: %+v", tr)
	}
	if _, err := TriggerOf(Recurrence{Kind: "weekly"}, now); err != ErrNoWeekday {
		t.Errorf("no weekday: %v", err)
	}
	for expr, kind := range map[string]string{
		"0 9 * * 0-6":     "daily",
		"0 9 * * MON-FRI": "weekdays",
		"0 9 * * 0,7":     "weekly",
		"0 9 1 * 1":       "unsupported",
		"0 9 * 1 *":       "unsupported",
		"*/7 9 * * *":     "unsupported",
		"5 */2 * * *":     "unsupported",
	} {
		if got := RecurrenceOf(Trigger{Kind: "cron", Expression: expr, TimeZone: "UTC"}, "UTC").Kind; got != kind {
			t.Errorf("%q is %s, want %s", expr, got, kind)
		}
	}
}

func TestCronNext(t *testing.T) {
	sh, _ := time.LoadLocation("Asia/Shanghai")
	from := time.Date(2026, 10, 29, 10, 0, 0, 0, sh) // a Thursday
	runs := NextRuns(Trigger{Kind: "cron", Expression: "0 9 * * 1-5", TimeZone: "Asia/Shanghai"}, time.Time{}, from, 3, time.UTC)
	want := []time.Time{time.Date(2026, 10, 30, 9, 0, 0, 0, sh), time.Date(2026, 11, 2, 9, 0, 0, 0, sh), time.Date(2026, 11, 3, 9, 0, 0, 0, sh)}
	if !reflect.DeepEqual(runs, want) {
		t.Fatalf("runs %v", runs)
	}
	// Both day fields restricted: either matches.
	c, _ := ParseCron("0 0 13 * FRI")
	if at, _ := c.Next(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.UTC); !at.Equal(time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("or semantics: %v", at)
	}
	// A wall time the clock skips is skipped.
	ny, _ := time.LoadLocation("America/New_York")
	c, _ = ParseCron("30 2 * * *")
	if at, _ := c.Next(time.Date(2026, 3, 7, 12, 0, 0, 0, ny), ny); !at.Equal(time.Date(2026, 3, 9, 2, 30, 0, 0, ny)) {
		t.Fatalf("DST gap: %v", at)
	}
	for _, bad := range []string{"0 9 * *", "0 9 L * *", "0 9 * * 1#2", "@daily", "60 9 * * *"} {
		if _, err := ParseCron(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	// An interval steps from its anchor.
	anchor := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	runs = NextRuns(Trigger{Kind: "interval", EveryMs: 5 * 3_600_000, AnchorAt: ISO(anchor)}, time.Time{}, anchor.Add(6*time.Hour), 2, time.UTC)
	if !reflect.DeepEqual(runs, []time.Time{anchor.Add(10 * time.Hour), anchor.Add(15 * time.Hour)}) {
		t.Fatalf("interval %v", runs)
	}
}
