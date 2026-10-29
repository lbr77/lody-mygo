package schedule

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Recurrence is a time rule as a person picks it. What is stored is a
// trigger: cron for every rule the clock divides, an interval for steps it
// doesn't, a once for a single run. A stored trigger no rule names is
// Unsupported, shown as is and replaced, never edited.
type Recurrence struct {
	Kind         string // daily, weekdays, weekly, monthly, hours, minutes, once or unsupported
	Hour, Minute int
	Weekdays     []int // weekly: Sunday is 0
	Days         []int // monthly: 1–31
	Every        int   // hours or minutes
	At           time.Time
	TimeZone     string
	Trigger      Trigger // unsupported: the stored trigger
}

// Kinds are the rules offered, in menu order.
var Kinds = []string{"daily", "weekdays", "weekly", "monthly", "hours", "minutes", "once"}

// MinuteSteps and HourSteps are the steps offered; all divide the clock.
var (
	MinuteSteps = []int{5, 10, 15, 20, 30}
	HourSteps   = []int{1, 2, 3, 4, 6, 8, 12}
)

var (
	fixedNumber = regexp.MustCompile(`^\d{1,2}$`)
	stepField   = regexp.MustCompile(`^\*/(\d{1,2})$`)
)

func fixed(field string, min, max int) (int, bool) {
	if !fixedNumber.MatchString(field) {
		return 0, false
	}
	n, _ := strconv.Atoi(field)
	return n, n >= min && n <= max
}

func step(field string) (int, bool) {
	m := stepField.FindStringSubmatch(field)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	return n, n >= 1
}

// weekdayField is a day-of-week field as a sorted Sunday=0 set, or nil
// for one with steps or wrapping ranges.
func weekdayField(field string) []int {
	token := func(s string) (int, bool) {
		for i, n := range weekdays {
			if strings.EqualFold(s, n) {
				return i, true
			}
		}
		if len(s) != 1 || s[0] < '0' || s[0] > '7' {
			return 0, false
		}
		return int(s[0] - '0'), true
	}
	found := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		if part == "" || strings.Contains(part, "/") {
			return nil
		}
		a, b, isRange := strings.Cut(part, "-")
		from, ok := token(a)
		if !ok {
			return nil
		}
		to := from
		if isRange {
			if to, ok = token(b); !ok || from > to {
				return nil
			}
		}
		for d := from; d <= to; d++ {
			found[d%7] = true
		}
	}
	return sortedSet(found)
}

func dayField(field string) []int {
	found := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		if part == "" || strings.Contains(part, "/") {
			return nil
		}
		a, b, isRange := strings.Cut(part, "-")
		from, ok := fixed(a, 1, 31)
		if !ok {
			return nil
		}
		to := from
		if isRange {
			if to, ok = fixed(b, 1, 31); !ok || from > to {
				return nil
			}
		}
		for d := from; d <= to; d++ {
			found[d] = true
		}
	}
	return sortedSet(found)
}

func sortedSet(m map[int]bool) []int {
	if len(m) == 0 {
		return nil
	}
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// RecurrenceOf is the rule a timed trigger means; zone is the zone an
// interval's rule is read in.
func RecurrenceOf(t Trigger, zone string) Recurrence {
	unsupported := Recurrence{Kind: "unsupported", Trigger: t}
	switch t.Kind {
	case "once":
		at, _ := time.Parse(time.RFC3339Nano, t.At)
		return Recurrence{Kind: "once", At: at}
	case "interval":
		switch {
		case t.EveryMs > 0 && t.EveryMs%3_600_000 == 0:
			return Recurrence{Kind: "hours", Every: int(t.EveryMs / 3_600_000), TimeZone: zone}
		case t.EveryMs > 0 && t.EveryMs%60_000 == 0:
			return Recurrence{Kind: "minutes", Every: int(t.EveryMs / 60_000), TimeZone: zone}
		}
		return unsupported
	case "cron":
	default:
		return unsupported
	}
	f := strings.Fields(t.Expression)
	if len(f) != 5 || f[3] != "*" {
		return unsupported
	}
	anyDay := f[2] == "*" && f[4] == "*"
	if n, ok := step(f[0]); ok {
		if f[1] == "*" && anyDay {
			return Recurrence{Kind: "minutes", Every: n, TimeZone: t.TimeZone}
		}
		return unsupported
	}
	if n, ok := step(f[1]); ok {
		if f[0] == "0" && anyDay {
			return Recurrence{Kind: "hours", Every: n, TimeZone: t.TimeZone}
		}
		return unsupported
	}
	minute, ok1 := fixed(f[0], 0, 59)
	hour, ok2 := fixed(f[1], 0, 23)
	if !ok1 || !ok2 {
		return unsupported
	}
	r := Recurrence{Hour: hour, Minute: minute, TimeZone: t.TimeZone}
	if f[2] == "*" {
		if f[4] == "*" {
			r.Kind = "daily"
			return r
		}
		days := weekdayField(f[4])
		switch {
		case days == nil:
			return unsupported
		case slices.Equal(days, []int{1, 2, 3, 4, 5}):
			r.Kind = "weekdays"
		case len(days) == 7:
			r.Kind = "daily"
		default:
			r.Kind, r.Weekdays = "weekly", days
		}
		return r
	}
	if f[4] != "*" {
		return unsupported
	}
	if r.Days = dayField(f[2]); r.Days == nil {
		return unsupported
	}
	r.Kind = "monthly"
	return r
}

// Trigger errors, for a rule not yet complete.
var (
	ErrNoWeekday = errors.New("schedule: choose at least one day of the week")
	ErrNoDay     = errors.New("schedule: choose at least one day of the month")
	ErrNoTime    = errors.New("schedule: choose a time")
)

// ISO writes an instant as JavaScript's toISOString does.
func ISO(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// TriggerOf is the trigger a rule means. now anchors an interval.
func TriggerOf(r Recurrence, now time.Time) (Trigger, error) {
	switch r.Kind {
	case "once":
		if r.At.IsZero() {
			return Trigger{}, ErrNoTime
		}
		return Trigger{Kind: "once", At: ISO(r.At)}, nil
	case "unsupported":
		return r.Trigger, nil
	case "minutes":
		if r.Every < 1 {
			return Trigger{}, ErrNoTime
		}
		if 60%r.Every == 0 {
			return Trigger{Kind: "cron", Expression: fmt.Sprintf("*/%d * * * *", r.Every), TimeZone: r.TimeZone}, nil
		}
		return Trigger{Kind: "interval", EveryMs: int64(r.Every) * 60_000, AnchorAt: ISO(now)}, nil
	case "hours":
		if r.Every < 1 {
			return Trigger{}, ErrNoTime
		}
		if 24%r.Every == 0 {
			return Trigger{Kind: "cron", Expression: fmt.Sprintf("0 */%d * * *", r.Every), TimeZone: r.TimeZone}, nil
		}
		return Trigger{Kind: "interval", EveryMs: int64(r.Every) * 3_600_000, AnchorAt: ISO(now)}, nil
	}
	dow, dom := "*", "*"
	switch r.Kind {
	case "weekdays":
		dow = "1-5"
	case "weekly":
		set := map[int]bool{}
		for _, d := range r.Weekdays {
			set[d] = true
		}
		days := sortedSet(set)
		if len(days) == 0 {
			return Trigger{}, ErrNoWeekday
		}
		dow = joinInts(days)
	case "monthly":
		set := map[int]bool{}
		for _, d := range r.Days {
			set[d] = true
		}
		days := sortedSet(set)
		if len(days) == 0 {
			return Trigger{}, ErrNoDay
		}
		dom = joinInts(days)
	}
	return Trigger{Kind: "cron", Expression: fmt.Sprintf("%d %d %s * %s", r.Minute, r.Hour, dom, dow), TimeZone: r.TimeZone}, nil
}

func joinInts(ns []int) string {
	s := make([]string, len(ns))
	for i, n := range ns {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ",")
}
