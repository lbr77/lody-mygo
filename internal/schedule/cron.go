package schedule

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Cron is a standard five-field expression, as Lody accepts them: numbers,
// ranges, steps, lists and month and day names, no seconds or extensions.
type Cron struct {
	minute, hour, dom, month, dow [64]bool
	domAny, dowAny                bool
}

var (
	cronField = regexp.MustCompile(`^[\d*,/\-A-Za-z]+$`)
	cronNames = regexp.MustCompile(`(?i)JAN|FEB|MAR|APR|MAY|JUN|JUL|AUG|SEP|OCT|NOV|DEC|SUN|MON|TUE|WED|THU|FRI|SAT`)
	months    = []string{"", "JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"}
	weekdays  = []string{"SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"}
)

// ErrCron is a cron expression Lody wouldn't take.
var ErrCron = errors.New("schedule: cron must use standard five-field syntax")

// ParseCron parses an expression.
func ParseCron(expr string) (*Cron, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 || strings.ContainsAny(strings.ToUpper(cronNames.ReplaceAllString(expr, "")), "LW?#@+") {
		return nil, ErrCron
	}
	for _, f := range fields {
		if !cronField.MatchString(f) {
			return nil, ErrCron
		}
	}
	c := &Cron{domAny: fields[2] == "*", dowAny: fields[4] == "*"}
	for i, spec := range []struct {
		set      *[64]bool
		min, max int
		names    []string
	}{{&c.minute, 0, 59, nil}, {&c.hour, 0, 23, nil}, {&c.dom, 1, 31, nil}, {&c.month, 1, 12, months}, {&c.dow, 0, 7, weekdays}} {
		if err := parseField(fields[i], spec.min, spec.max, spec.names, spec.set); err != nil {
			return nil, err
		}
	}
	if c.dow[7] {
		c.dow[0] = true // Sunday is 0 or 7
	}
	return c, nil
}

func parseField(field string, min, max int, names []string, set *[64]bool) error {
	value := func(s string) (int, error) {
		for i, n := range names {
			if n != "" && strings.EqualFold(s, n) {
				return i, nil
			}
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < min || n > max {
			return 0, ErrCron
		}
		return n, nil
	}
	for _, part := range strings.Split(field, ",") {
		rng, step := part, 1
		if i := strings.IndexByte(part, '/'); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n < 1 {
				return ErrCron
			}
			rng, step = part[:i], n
		}
		lo, hi := min, max
		switch {
		case rng == "*":
		case strings.Contains(rng, "-"):
			a, b, _ := strings.Cut(rng, "-")
			var err error
			if lo, err = value(a); err != nil {
				return err
			}
			if hi, err = value(b); err != nil {
				return err
			}
			if lo > hi {
				return ErrCron
			}
		default:
			n, err := value(rng)
			if err != nil {
				return err
			}
			lo, hi = n, n
			if step > 1 {
				hi = max
			}
		}
		for v := lo; v <= hi; v += step {
			set[v] = true
		}
	}
	return nil
}

// day reports whether a date matches the day fields: either one when both
// are restricted, as croner without domAndDow does.
func (c *Cron) day(t time.Time) bool {
	dom, dow := c.dom[t.Day()], c.dow[int(t.Weekday())]
	switch {
	case c.domAny && c.dowAny:
		return true
	case c.domAny:
		return dow
	case c.dowAny:
		return dom
	}
	return dom || dow
}

// Next is the first time after `after` that matches, in loc. A wall time
// the clock skips, as in a DST gap, is skipped, not moved.
func (c *Cron) Next(after time.Time, loc *time.Location) (time.Time, bool) {
	t := after.In(loc)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	for i := 0; i < 366*5; i++ {
		y, m, d := day.Date()
		if c.month[int(m)] && c.day(day) {
			for h := 0; h < 24; h++ {
				if !c.hour[h] {
					continue
				}
				for min := 0; min < 60; min++ {
					if !c.minute[min] {
						continue
					}
					at := time.Date(y, m, d, h, min, 0, 0, loc)
					if at.Hour() != h || at.Minute() != min || at.Day() != d {
						continue // not on this day's clock
					}
					if at.After(after) {
						return at, true
					}
				}
			}
		}
		day = time.Date(y, m, d+1, 0, 0, 0, 0, loc)
	}
	return time.Time{}, false
}

// NextRuns are the next n times a trigger runs after now, from activeFrom.
func NextRuns(tr Trigger, activeFrom, now time.Time, n int, fallback *time.Location) []time.Time {
	var out []time.Time
	after := now
	if activeFrom.After(after) {
		after = activeFrom.Add(-time.Millisecond)
	}
	switch tr.Kind {
	case "once":
		if at, err := time.Parse(time.RFC3339Nano, tr.At); err == nil && at.After(after) {
			out = append(out, at)
		}
	case "interval":
		anchor, err := time.Parse(time.RFC3339Nano, tr.AnchorAt)
		if err != nil || tr.EveryMs <= 0 {
			return nil
		}
		every := time.Duration(tr.EveryMs) * time.Millisecond
		k := int64(0)
		if after.After(anchor) {
			k = int64(after.Sub(anchor)/every) + 1
		}
		for i := 0; i < n; i++ {
			out = append(out, anchor.Add(time.Duration(k+int64(i))*every))
		}
	case "cron":
		c, err := ParseCron(tr.Expression)
		if err != nil {
			return nil
		}
		loc := fallback
		if l, err := time.LoadLocation(tr.TimeZone); err == nil {
			loc = l
		}
		for len(out) < n {
			at, ok := c.Next(after, loc)
			if !ok {
				break
			}
			out = append(out, at)
			after = at
		}
	}
	return out
}
