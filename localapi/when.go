package localapi

import (
	"fmt"
	"time"
)

// When is "10:20 (7 min ago)"; another day adds the date. The agent, the CLI
// and the tray word times the same way.
func When(t, now time.Time) string {
	return Clock(t, now) + " (" + Ago(now.Sub(t)) + ")"
}

func Clock(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	if t.YearDay() == now.YearDay() && t.Year() == now.Year() {
		return t.Format("15:04")
	}
	return t.Format("2006-01-02 15:04")
}

func Ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}
