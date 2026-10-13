package scheduler

import (
	"fmt"
	"math/bits"
	"strings"
	"time"
)

// Describe renders a schedule as short plain English for operators. Anything
// it cannot state exactly (and any invalid expression) comes back unchanged.
func Describe(expr string) string {
	s, err := Parse(expr)
	if err != nil {
		return expr
	}
	if s.Never() {
		return "only when run by hand (backup now --tag)"
	}
	if d, ok := describe(s); ok {
		return d
	}
	return expr
}

func describe(s Schedule) (string, bool) {
	if !s.domStar && !s.dowStar {
		return "", false
	}
	mins, hrs := members(s.minute), members(s.hour)
	doms, months, dows := members(s.dom), members(s.month), members(s.dow)
	everyDay := len(doms) == 31 && len(dows) == 7
	if len(months) == 12 && everyDay {
		if len(mins) == 60 && len(hrs) == 24 {
			return "every minute", true
		}
		if len(mins) > 1 && len(hrs) == 24 {
			n, ok := stride(mins, 60)
			return fmt.Sprintf("every %d minutes", n), ok
		}
		if len(mins) == 1 && len(hrs) == 24 {
			if mins[0] == 0 {
				return "every hour, on the hour", true
			}
			return fmt.Sprintf("every hour at :%02d", mins[0]), true
		}
	}
	if len(mins) != 1 {
		return "", false
	}
	if len(hrs) > 3 {
		n, ok := stride(hrs, 24)
		if !ok || mins[0] != 0 || len(months) != 12 || !everyDay {
			return "", false
		}
		return fmt.Sprintf("every %d hours, on the hour", n), true
	}
	var at []string
	for _, h := range hrs {
		at = append(at, fmt.Sprintf("%02d:%02d", h, mins[0]))
	}
	at2 := joinAnd(at)
	switch {
	case len(months) == 12 && everyDay:
		return "every day at " + at2, true
	case len(months) == 12 && len(doms) == 1 && len(dows) == 7:
		return fmt.Sprintf("on the %s of every month at %s", ordinal(doms[0]), at2), true
	case len(months) == 12 && len(doms) == 31 && len(dows) < 7:
		return weekdays(dows) + " at " + at2, true
	case len(months) == 1 && len(doms) == 1 && len(dows) == 7:
		return fmt.Sprintf("every year on %d %s at %s", doms[0], time.Month(months[0]), at2), true
	}
	return "", false
}

func members(set uint64) []int {
	var out []int
	for ; set != 0; set &= set - 1 {
		out = append(out, bits.TrailingZeros64(set))
	}
	return out
}

// stride reports n when v is 0, n, 2n, ... covering [0, size) evenly.
func stride(v []int, size int) (int, bool) {
	n := v[1]
	return n, v[0] == 0 && size%n == 0 && len(v) == size/n && v[len(v)-1] == size-n
}

func weekdays(d []int) string {
	if len(d) >= 3 && d[len(d)-1]-d[0] == len(d)-1 {
		return time.Weekday(d[0]).String() + " to " + time.Weekday(d[len(d)-1]).String()
	}
	names := make([]string, len(d))
	for i, w := range d {
		names[i] = time.Weekday(w).String() + "s"
	}
	return joinAnd(names)
}

func joinAnd(v []string) string {
	if len(v) < 2 {
		return strings.Join(v, "")
	}
	return strings.Join(v[:len(v)-1], ", ") + " and " + v[len(v)-1]
}

func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}
