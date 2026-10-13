package scheduler

import "testing"

func TestDescribe(t *testing.T) {
	tests := []struct{ expr, want string }{
		{"", "only when run by hand (backup now --tag)"},
		{"*/10 * * * *", "every 10 minutes"},
		{"*/1 * * * *", "every minute"},
		{"* * * * *", "every minute"},
		{"0 * * * *", "every hour, on the hour"},
		{"15 * * * *", "every hour at :15"},
		{"0 */2 * * *", "every 2 hours, on the hour"},
		{"0 1 * * *", "every day at 01:00"},
		{"30 2 * * *", "every day at 02:30"},
		{"@daily", "every day at 00:00"},
		{"0 12,17 * * *", "every day at 12:00 and 17:00"},
		{"0 6,12,18 * * *", "every day at 06:00, 12:00 and 18:00"},
		{"0 3 * * 0", "Sundays at 03:00"},
		{"0 3 * * 7", "Sundays at 03:00"},
		{"0 8 * * 1-5", "Monday to Friday at 08:00"},
		{"0 12,17 * * 1-5", "Monday to Friday at 12:00 and 17:00"},
		{"0 9 * * 1,3,5", "Mondays, Wednesdays and Fridays at 09:00"},
		{"0 1 1 * *", "on the 1st of every month at 01:00"},
		{"0 1 15 * *", "on the 15th of every month at 01:00"},
		{"0 1 2 * *", "on the 2nd of every month at 01:00"},
		{"0 1 3 * *", "on the 3rd of every month at 01:00"},
		{"0 1 11 * *", "on the 11th of every month at 01:00"},
		{"0 1 21 * *", "on the 21st of every month at 01:00"},
		{"0 1 22 * *", "on the 22nd of every month at 01:00"},
		{"0 1 23 * *", "on the 23rd of every month at 01:00"},
		{"0 1 31 * *", "on the 31st of every month at 01:00"},
		{"0 2 1 1 *", "every year on 1 January at 02:00"},
		{"0 0 1 12 *", "every year on 1 December at 00:00"},
		{"@hourly", "every hour, on the hour"},
		{"@weekly", "Sundays at 00:00"},
		{"@monthly", "on the 1st of every month at 00:00"},
		// fallbacks
		{"*/7 3-5 * 2 1", "*/7 3-5 * 2 1"},
		{"*/10 3 * * *", "*/10 3 * * *"},
		{"0 1,2,3,4 * * *", "0 1,2,3,4 * * *"},
		{"0 1 1 * 1", "0 1 1 * 1"},
		{"0 1 1-31 * 1", "0 1 1-31 * 1"},
		{"0 1 1,15 * *", "0 1 1,15 * *"},
		{"0 1 * 6 *", "0 1 * 6 *"},
		{"0,1 1 * * *", "0,1 1 * * *"},
		{"5 */2 * * *", "5 */2 * * *"},
		{"bogus", "bogus"},
		{"61 * * * *", "61 * * * *"},
		{"@yearly", "@yearly"},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			if got := Describe(tc.expr); got != tc.want {
				t.Errorf("Describe(%q) = %q, want %q", tc.expr, got, tc.want)
			}
		})
	}
}
