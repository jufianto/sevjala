// Package scheduler parses policy schedules (5-field cron) and decides which
// policies are due (blueprint: Backup engine → Policies and tiers).
package scheduler

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed cron expression; the zero value never fires.
type Schedule struct {
	minute, hour, dom, month, dow uint64 // bit sets
	domStar, dowStar              bool
	set                           bool
}

var aliases = map[string]string{
	"@hourly":  "0 * * * *",
	"@daily":   "0 0 * * *",
	"@weekly":  "0 0 * * 0",
	"@monthly": "0 0 1 * *",
}

type field struct {
	name     string
	min, max int
}

var fields = [5]field{{"minute", 0, 59}, {"hour", 0, 23}, {"day of month", 1, 31}, {"month", 1, 12}, {"day of week", 0, 7}}

// horizon bounds Prev and Next: a schedule that fires less than once a year
// (29 February) is reported as not firing.
const horizon = 366 * 24 * time.Hour

// Parse reads a 5-field cron expression or an alias; "" never fires.
func Parse(expr string) (Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return Schedule{}, nil
	}
	if a, ok := aliases[expr]; ok {
		expr = a
	}
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return Schedule{}, fmt.Errorf("schedule %q: want 5 fields (minute hour day-of-month month day-of-week) or @hourly, @daily, @weekly, @monthly", expr)
	}
	var sets [5]uint64
	for i, p := range parts {
		s, err := parseField(p, fields[i])
		if err != nil {
			return Schedule{}, err
		}
		sets[i] = s
	}
	if sets[4]&(1<<7) != 0 {
		sets[4] = sets[4]&^(1<<7) | 1
	}
	return Schedule{minute: sets[0], hour: sets[1], dom: sets[2], month: sets[3], dow: sets[4],
		domStar: strings.HasPrefix(parts[2], "*"), dowStar: strings.HasPrefix(parts[4], "*"), set: true}, nil
}

func parseField(s string, f field) (uint64, error) {
	var set uint64
	for item := range strings.SplitSeq(s, ",") {
		rng, stepStr, hasStep := strings.Cut(item, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepStr)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("%s %q: step must be a positive number", f.name, item)
			}
			step = n
		}
		lo, hi := f.min, f.max
		if rng != "*" {
			a, b, isRange := strings.Cut(rng, "-")
			var err error
			if lo, err = number(a, f, item); err != nil {
				return 0, err
			}
			hi = lo
			switch {
			case isRange:
				if hi, err = number(b, f, item); err != nil {
					return 0, err
				}
				if hi < lo {
					return 0, fmt.Errorf("%s %q: range goes backwards", f.name, item)
				}
			case hasStep:
				hi = f.max
			}
		}
		for v := lo; v <= hi; v += step {
			set |= 1 << v
		}
	}
	return set, nil
}

func number(s string, f field, item string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < f.min || n > f.max {
		return 0, fmt.Errorf("%s %q: must be %d-%d", f.name, item, f.min, f.max)
	}
	return n, nil
}

func (s Schedule) Never() bool { return !s.set }

// dayMatches applies cron's rule: when both day fields are restricted, either may match.
func (s Schedule) dayMatches(t time.Time) bool {
	if s.month&(1<<int(t.Month())) == 0 {
		return false
	}
	dom := s.dom&(1<<t.Day()) != 0
	dow := s.dow&(1<<int(t.Weekday())) != 0
	if s.domStar || s.dowStar {
		return dom && dow
	}
	return dom || dow
}

// Prev is the latest matching minute at or before t, in t's location.
func (s Schedule) Prev(t time.Time) (time.Time, bool) {
	if !s.set {
		return time.Time{}, false
	}
	t = t.Truncate(time.Minute)
	limit := t.Add(-horizon)
	for !t.Before(limit) {
		switch {
		case !s.dayMatches(t):
			y, m, d := t.Date()
			t = earlier(t, time.Date(y, m, d, 0, 0, 0, 0, t.Location()).Add(-time.Minute))
		case s.hour&(1<<t.Hour()) == 0:
			t = t.Add(-time.Duration(t.Minute()+1) * time.Minute)
		case s.minute&(1<<t.Minute()) == 0:
			t = t.Add(-time.Minute)
		default:
			return t, true
		}
	}
	return time.Time{}, false
}

// Next is the earliest matching minute after t, in t's location.
func (s Schedule) Next(t time.Time) (time.Time, bool) {
	if !s.set {
		return time.Time{}, false
	}
	t = t.Truncate(time.Minute).Add(time.Minute)
	limit := t.Add(horizon)
	for t.Before(limit) {
		switch {
		case !s.dayMatches(t):
			y, m, d := t.Date()
			n := time.Date(y, m, d+1, 0, 0, 0, 0, t.Location())
			if !n.After(t) {
				n = t.Add(time.Minute)
			}
			t = n
		case s.hour&(1<<t.Hour()) == 0:
			t = t.Add(time.Duration(60-t.Minute()) * time.Minute)
		case s.minute&(1<<t.Minute()) == 0:
			t = t.Add(time.Minute)
		default:
			return t, true
		}
	}
	return time.Time{}, false
}

// earlier keeps a backwards walk moving when a daylight-saving change makes
// the computed midnight land after t.
func earlier(t, cand time.Time) time.Time {
	if cand.Before(t) {
		return cand
	}
	return t.Add(-time.Minute)
}

type Policy struct {
	Name     string
	Schedule Schedule
	Enabled  bool
}

// Due lists, sorted, the enabled policies whose latest scheduled time is after
// their newest backup (newest[name], zero when none): a missed run is caught
// up once.
func Due(now time.Time, policies []Policy, newest map[string]time.Time) []string {
	var out []string
	for _, p := range policies {
		if !p.Enabled {
			continue
		}
		if f, ok := p.Schedule.Prev(now); ok && f.After(newest[p.Name]) {
			out = append(out, p.Name)
		}
	}
	slices.Sort(out)
	return out
}
