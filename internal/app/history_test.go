package app

import (
	"testing"
	"time"
)

// TestVersionTime: the list of versions is read at a glance, so today and
// yesterday go by name and everything else by date.
func TestVersionTime(t *testing.T) {
	zone := time.FixedZone("test", 2*3600)
	now := time.Date(2026, time.September, 29, 15, 30, 0, 0, zone)
	at := func(y int, m time.Month, d, h, min int) time.Time {
		return time.Date(y, m, d, h, min, 0, 0, zone)
	}
	cases := []struct {
		name string
		when time.Time
		want string
	}{
		{"this morning", at(2026, time.September, 29, 9, 5), "Today 09:05"},
		{"a minute ago", now.Add(-time.Minute), "Today 15:29"},
		{"just after midnight", at(2026, time.September, 29, 0, 1), "Today 00:01"},
		{"late last night", at(2026, time.September, 28, 23, 59), "Yesterday 23:59"},
		{"yesterday morning", at(2026, time.September, 28, 9, 12), "Yesterday 09:12"},
		{"two days ago", at(2026, time.September, 27, 14, 5), "27 Sep 2026 14:05"},
		{"last month", at(2026, time.September, 3, 18, 40), "3 Sep 2026 18:40"},
		{"last year", at(2025, time.December, 31, 8, 0), "31 Dec 2025 08:00"},
		// A clock that was a little fast when the version was saved.
		{"slightly in the future", now.Add(5 * time.Minute), "Today 15:35"},
	}
	for _, c := range cases {
		if got := versionTime(c.when, now); got != c.want {
			t.Errorf("%s: versionTime = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestVersionTimeUsesNowsZone: a time stored in UTC is read in the zone of the
// clock it is compared with, or a version saved at 23:50 local time would be
// filed under the wrong day.
func TestVersionTimeUsesNowsZone(t *testing.T) {
	zone := time.FixedZone("test", -8*3600)
	now := time.Date(2026, time.September, 29, 10, 0, 0, 0, zone)
	saved := time.Date(2026, time.September, 29, 3, 50, 0, 0, time.UTC) // 19:50 on the 28th here
	if got, want := versionTime(saved, now), "Yesterday 19:50"; got != want {
		t.Errorf("versionTime = %q, want %q", got, want)
	}
}

// TestVersionTimeAcrossMonthAndYear: "yesterday" is a calendar day, not 24
// hours, and it has to cross the end of a month and of a year.
func TestVersionTimeAcrossMonthAndYear(t *testing.T) {
	zone := time.UTC
	cases := []struct {
		now, when time.Time
		want      string
	}{
		{time.Date(2026, time.October, 1, 8, 0, 0, 0, zone), time.Date(2026, time.September, 30, 20, 0, 0, 0, zone), "Yesterday 20:00"},
		{time.Date(2026, time.January, 1, 8, 0, 0, 0, zone), time.Date(2025, time.December, 31, 20, 0, 0, 0, zone), "Yesterday 20:00"},
		{time.Date(2028, time.March, 1, 8, 0, 0, 0, zone), time.Date(2028, time.February, 29, 20, 0, 0, 0, zone), "Yesterday 20:00"},
	}
	for _, c := range cases {
		if got := versionTime(c.when, c.now); got != c.want {
			t.Errorf("versionTime(%v, %v) = %q, want %q", c.when, c.now, got, c.want)
		}
	}
}

// TestVersionTimeInSentence: the confirmation and the toast put the time after "from",
// where a capital letter reads oddly. Dates have nothing to lower.
func TestVersionTimeInSentence(t *testing.T) {
	now := time.Date(2026, time.September, 29, 15, 30, 0, 0, time.UTC)
	cases := map[string]time.Time{
		"today 14:05":      time.Date(2026, time.September, 29, 14, 5, 0, 0, time.UTC),
		"yesterday 09:12":  time.Date(2026, time.September, 28, 9, 12, 0, 0, time.UTC),
		"3 Sep 2026 18:40": time.Date(2026, time.September, 3, 18, 40, 0, 0, time.UTC),
	}
	for want, when := range cases {
		if got := versionTimeInSentence(when, now); got != want {
			t.Errorf("versionTimeInSentence = %q, want %q", got, want)
		}
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		0:               "0 B",
		512:             "512 B",
		1024:            "1.0 KB",
		1536:            "1.5 KB",
		3 * 1024 * 1024: "3.0 MB",
	}
	for n, want := range cases {
		if got := humanSize(n); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", n, got, want)
		}
	}
}
