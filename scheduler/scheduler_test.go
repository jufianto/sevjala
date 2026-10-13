package scheduler

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func utc(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

func TestParseErrors(t *testing.T) {
	tests := []struct{ expr, want string }{
		{"* * * *", "want 5 fields"},
		{"@yearly", "want 5 fields"},
		{"60 * * * *", `minute "60": must be 0-59`},
		{"* 24 * * *", `hour "24": must be 0-23`},
		{"* * 0 * *", `day of month "0": must be 1-31`},
		{"* * * 13 *", `month "13": must be 1-12`},
		{"* * * * 8", `day of week "8": must be 0-7`},
		{"*/0 * * * *", `minute "*/0": step must be a positive number`},
		{"5-1 * * * *", `minute "5-1": range goes backwards`},
		{"a * * * *", `minute "a": must be 0-59`},
		{"* * * jan *", `month "jan": must be 1-12`},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			if _, err := Parse(tt.expr); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestEmptyNeverFires(t *testing.T) {
	s, err := Parse("  ")
	if err != nil || !s.Never() {
		t.Fatalf("Parse = %+v, %v", s, err)
	}
	if _, ok := s.Prev(utc(2026, 10, 13, 10, 0)); ok {
		t.Error("Prev fired")
	}
	if _, ok := s.Next(utc(2026, 10, 13, 10, 0)); ok {
		t.Error("Next fired")
	}
}

func TestPrevNext(t *testing.T) {
	tests := []struct {
		expr       string
		at         time.Time
		prev, next time.Time // zero = none within the horizon
	}{
		{"*/10 * * * *", time.Date(2026, 10, 13, 10, 15, 42, 0, time.UTC), utc(2026, 10, 13, 10, 10), utc(2026, 10, 13, 10, 20)},
		{"0 * * * *", utc(2026, 10, 13, 10, 0), utc(2026, 10, 13, 10, 0), utc(2026, 10, 13, 11, 0)},
		{"@hourly", utc(2026, 10, 13, 10, 59), utc(2026, 10, 13, 10, 0), utc(2026, 10, 13, 11, 0)},
		{"0 1 * * *", utc(2026, 10, 13, 0, 30), utc(2026, 10, 12, 1, 0), utc(2026, 10, 13, 1, 0)},
		{"@daily", utc(2026, 10, 13, 0, 30), utc(2026, 10, 13, 0, 0), utc(2026, 10, 14, 0, 0)},
		{"0 1 1 * *", utc(2026, 10, 13, 9, 0), utc(2026, 10, 1, 1, 0), utc(2026, 11, 1, 1, 0)},
		{"@monthly", utc(2026, 12, 31, 23, 59), utc(2026, 12, 1, 0, 0), utc(2027, 1, 1, 0, 0)},
		{"0 0 31 * *", utc(2026, 10, 13, 9, 0), utc(2026, 8, 31, 0, 0), utc(2026, 10, 31, 0, 0)},
		{"0 0 29 2 *", utc(2026, 10, 13, 9, 0), time.Time{}, time.Time{}},
		// 2026-10-13 is a Tuesday: day 13 OR Friday.
		{"0 0 13 * 5", utc(2026, 10, 13, 12, 0), utc(2026, 10, 13, 0, 0), utc(2026, 10, 16, 0, 0)},
		{"0 0 * * 7", utc(2026, 10, 13, 12, 0), utc(2026, 10, 11, 0, 0), utc(2026, 10, 18, 0, 0)},
		{"@weekly", utc(2026, 10, 13, 12, 0), utc(2026, 10, 11, 0, 0), utc(2026, 10, 18, 0, 0)},
		{"*/15 9-17 * * 1-5", utc(2026, 10, 13, 18, 0), utc(2026, 10, 13, 17, 45), utc(2026, 10, 14, 9, 0)},
		{"5,35 */6 * * *", utc(2026, 10, 13, 13, 0), utc(2026, 10, 13, 12, 35), utc(2026, 10, 13, 18, 5)},
		{"10/20 * * * *", utc(2026, 10, 13, 13, 0), utc(2026, 10, 13, 12, 50), utc(2026, 10, 13, 13, 10)},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			s, err := Parse(tt.expr)
			if err != nil {
				t.Fatal(err)
			}
			if p, ok := s.Prev(tt.at); ok != !tt.prev.IsZero() || !p.Equal(tt.prev) {
				t.Errorf("Prev = %v %v, want %v", p, ok, tt.prev)
			}
			if n, ok := s.Next(tt.at); ok != !tt.next.IsZero() || !n.Equal(tt.next) {
				t.Errorf("Next = %v %v, want %v", n, ok, tt.next)
			}
		})
	}
}

func TestDaylightSaving(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	s, _ := Parse("30 2 * * *")
	// 2026-03-29 02:00 jumps to 03:00: 02:30 does not exist that day.
	if n, _ := s.Next(time.Date(2026, 3, 28, 12, 0, 0, 0, berlin)); !n.Equal(time.Date(2026, 3, 30, 2, 30, 0, 0, berlin)) {
		t.Errorf("Next over the gap = %v", n)
	}
	if p, _ := s.Prev(time.Date(2026, 3, 29, 12, 0, 0, 0, berlin)); !p.Equal(time.Date(2026, 3, 28, 2, 30, 0, 0, berlin)) {
		t.Errorf("Prev over the gap = %v", p)
	}
	// 2026-10-25 03:00 goes back to 02:00: the walk must still move forward.
	first := time.Date(2026, 10, 25, 2, 30, 0, 0, berlin)
	if n, ok := s.Next(first); !ok || !n.After(first) {
		t.Errorf("Next in the repeated hour = %v %v", n, ok)
	}
	hourly, _ := Parse("0 * * * *")
	if p, ok := hourly.Prev(first.Add(time.Hour)); !ok || !p.Before(first.Add(time.Hour)) {
		t.Errorf("Prev in the repeated hour = %v %v", p, ok)
	}
}

func TestDue(t *testing.T) {
	parse := func(e string) Schedule { s, _ := Parse(e); return s }
	ps := []Policy{
		{Name: "frequent", Schedule: parse("*/10 * * * *"), Enabled: true},
		{Name: "daily", Schedule: parse("0 1 * * *"), Enabled: true},
		{Name: "manual", Schedule: parse(""), Enabled: true},
		{Name: "off", Schedule: parse("* * * * *"), Enabled: false},
	}
	now := utc(2026, 10, 13, 10, 15)
	tests := []struct {
		name   string
		newest map[string]time.Time
		want   []string
	}{
		{"nothing made yet", nil, []string{"daily", "frequent"}},
		{"all made", map[string]time.Time{"frequent": utc(2026, 10, 13, 10, 10), "daily": utc(2026, 10, 13, 1, 0).Add(5 * time.Second)}, nil},
		{"frequent missed", map[string]time.Time{"frequent": utc(2026, 10, 13, 10, 9), "daily": utc(2026, 10, 13, 1, 1)}, []string{"frequent"}},
		{"daily missed overnight", map[string]time.Time{"frequent": utc(2026, 10, 13, 10, 10), "daily": utc(2026, 10, 12, 1, 0)}, []string{"daily"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Due(now, ps, tt.newest); !slices.Equal(got, tt.want) {
				t.Errorf("Due = %q, want %q", got, tt.want)
			}
		})
	}
}
