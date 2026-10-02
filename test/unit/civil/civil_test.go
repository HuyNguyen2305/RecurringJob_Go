package civil_test

import (
	"testing"
	"time"

	"recurringjob/internal/common/civil"
)

func TestNewAndFormat(t *testing.T) {
	d := civil.New(2026, 10, 2)
	if got := civil.Format(d); got != "2026-10-02" {
		t.Fatalf("Format = %q", got)
	}
	if d.Location() != time.UTC || d.Hour() != 0 || d.Minute() != 0 || d.Second() != 0 || d.Nanosecond() != 0 {
		t.Fatalf("not UTC midnight: %v", d)
	}
	// New normalises like time.Date does.
	if got := civil.Format(civil.New(2026, 13, 1)); got != "2027-01-01" {
		t.Fatalf("overflow month: %q", got)
	}
	// Format always renders the UTC date, whatever zone the value carries.
	plus7 := time.Date(2026, 10, 3, 2, 0, 0, 0, time.FixedZone("x", 7*3600))
	if got := civil.Format(plus7); got != "2026-10-02" {
		t.Fatalf("Format(+07:00 02:00) = %q, want the UTC date 2026-10-02", got)
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		in   string
		want string // "" means an error is expected
	}{
		{"2026-10-02", "2026-10-02"},
		{"2028-02-29", "2028-02-29"},
		{"0001-01-01", "0001-01-01"},
		{"9999-12-31", "9999-12-31"},
		{"2026-02-30", ""},
		{"2026-02-29", ""}, // not a leap year
		{"2026-13-01", ""},
		{"2026-00-10", ""},
		{"2026-10-00", ""},
		{"", ""},
		{"2026-10-02T00:00:00Z", ""},
		{"2026-10-02 ", ""},
		{"2026/10/02", ""},
		{"26-10-02", ""},
		{"2026-1-2", ""},
		{"not a date", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := civil.Parse(tt.in)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("want error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if civil.Format(got) != tt.want || got.Location() != time.UTC {
				t.Fatalf("got %v", got)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	plus7 := time.FixedZone("+7", 7*3600)
	minus8 := time.FixedZone("-8", -8*3600)
	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{"UTC afternoon", time.Date(2026, 10, 2, 15, 4, 5, 6, time.UTC), "2026-10-02"},
		{"+07:00 late evening is still the same UTC day", time.Date(2026, 10, 2, 23, 30, 0, 0, plus7), "2026-10-02"},
		{"+07:00 early morning is the previous UTC day", time.Date(2026, 10, 3, 2, 0, 0, 0, plus7), "2026-10-02"},
		{"-08:00 evening is the next UTC day", time.Date(2026, 10, 2, 20, 0, 0, 0, minus8), "2026-10-03"},
		{"year boundary", time.Date(2027, 1, 1, 3, 0, 0, 0, plus7), "2026-12-31"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := civil.Truncate(tt.in)
			if civil.Format(got) != tt.want || got.Location() != time.UTC || got.Hour() != 0 || got.Nanosecond() != 0 {
				t.Fatalf("Truncate = %v, want %s at UTC midnight", got, tt.want)
			}
		})
	}
}

func TestToday(t *testing.T) {
	before := time.Now().UTC()
	got := civil.Today()
	after := time.Now().UTC()
	if got.Location() != time.UTC || got.Hour() != 0 || got.Minute() != 0 || got.Nanosecond() != 0 {
		t.Fatalf("not UTC midnight: %v", got)
	}
	if got.Before(civil.Truncate(before)) || got.After(civil.Truncate(after)) {
		t.Fatalf("Today %v not between %v and %v", got, before, after)
	}
}

func TestDaysInMonth(t *testing.T) {
	tests := []struct {
		y    int
		m    time.Month
		want int
	}{
		{2026, time.January, 31}, {2026, time.February, 28}, {2024, time.February, 29},
		{1900, time.February, 28}, {2000, time.February, 29}, {2100, time.February, 28},
		{2026, time.April, 30}, {2026, time.June, 30}, {2026, time.September, 30}, {2026, time.November, 30},
		{2026, time.December, 31}, {2026, time.July, 31}, {2026, time.August, 31},
	}
	for _, tt := range tests {
		if got := civil.DaysInMonth(tt.y, tt.m); got != tt.want {
			t.Errorf("DaysInMonth(%d, %v) = %d, want %d", tt.y, tt.m, got, tt.want)
		}
	}
}

func TestAddDays(t *testing.T) {
	tests := []struct {
		from string
		n    int
		want string
	}{
		{"2026-10-02", 0, "2026-10-02"},
		{"2026-10-31", 1, "2026-11-01"},
		{"2026-12-31", 1, "2027-01-01"},
		{"2026-03-01", -1, "2026-02-28"},
		{"2024-03-01", -1, "2024-02-29"},
		{"2026-10-02", 7, "2026-10-09"},
		{"2026-10-02", -7, "2026-09-25"},
		{"2026-01-01", 365, "2027-01-01"},
		{"2028-01-01", 366, "2029-01-01"},
	}
	for _, tt := range tests {
		from, _ := civil.Parse(tt.from)
		got := civil.AddDays(from, tt.n)
		if civil.Format(got) != tt.want || got.Hour() != 0 || got.Location() != time.UTC {
			t.Errorf("AddDays(%s, %d) = %v, want %s", tt.from, tt.n, got, tt.want)
		}
	}
}

func TestMonthDayClamped(t *testing.T) {
	tests := []struct {
		y, m, day int
		want      string
	}{
		{2026, 1, 31, "2026-01-31"},
		{2026, 2, 31, "2026-02-28"},
		{2028, 2, 31, "2028-02-29"},
		{2026, 2, 29, "2026-02-28"},
		{2028, 2, 29, "2028-02-29"},
		{2026, 4, 31, "2026-04-30"},
		{2026, 4, 30, "2026-04-30"},
		{2026, 4, 15, "2026-04-15"},
		{2026, 13, 31, "2027-01-31"}, // month overflow
		{2026, 14, 30, "2027-02-28"}, // overflow into a short month
		{2026, 0, 15, "2025-12-15"},  // month underflow
		{2026, 25, 31, "2028-01-31"}, // two years of overflow
	}
	for _, tt := range tests {
		if got := civil.Format(civil.MonthDayClamped(tt.y, tt.m, tt.day)); got != tt.want {
			t.Errorf("MonthDayClamped(%d, %d, %d) = %s, want %s", tt.y, tt.m, tt.day, got, tt.want)
		}
	}
}

func TestOrdinal(t *testing.T) {
	tests := map[int]int{1: 1, 7: 1, 8: 2, 14: 2, 15: 3, 21: 3, 22: 4, 28: 4, 29: 5, 31: 5}
	for day, want := range tests {
		if got := civil.Ordinal(civil.New(2026, 10, day)); got != want {
			t.Errorf("Ordinal(day %d) = %d, want %d", day, got, want)
		}
	}
}

func TestNthWeekday(t *testing.T) {
	// October 2026 starts on a Thursday and has 31 days.
	tests := []struct {
		name string
		y    int
		m    time.Month
		wd   time.Weekday
		n    int
		want string // "" = does not exist
	}{
		{"1st Thursday", 2026, 10, time.Thursday, 1, "2026-10-01"},
		{"1st Friday", 2026, 10, time.Friday, 1, "2026-10-02"},
		{"1st Saturday", 2026, 10, time.Saturday, 1, "2026-10-03"},
		{"1st Sunday", 2026, 10, time.Sunday, 1, "2026-10-04"},
		{"1st Monday", 2026, 10, time.Monday, 1, "2026-10-05"},
		{"1st Tuesday", 2026, 10, time.Tuesday, 1, "2026-10-06"},
		{"1st Wednesday", 2026, 10, time.Wednesday, 1, "2026-10-07"},
		{"2nd Friday", 2026, 10, time.Friday, 2, "2026-10-09"},
		{"4th Friday", 2026, 10, time.Friday, 4, "2026-10-23"},
		{"5th Thursday", 2026, 10, time.Thursday, 5, "2026-10-29"},
		{"5th Friday", 2026, 10, time.Friday, 5, "2026-10-30"},
		{"5th Saturday", 2026, 10, time.Saturday, 5, "2026-10-31"},
		{"no 5th Sunday", 2026, 10, time.Sunday, 5, ""},
		{"no 5th Monday", 2026, 10, time.Monday, 5, ""},
		{"Feb 2026 has no 5th Sunday", 2026, 2, time.Sunday, 5, ""},
		{"Feb 2026 4th Sunday", 2026, 2, time.Sunday, 4, "2026-02-22"},
		{"leap Feb 2028 5th Tuesday", 2028, 2, time.Tuesday, 5, "2028-02-29"},
		{"leap Feb 2028 no 5th Wednesday", 2028, 2, time.Wednesday, 5, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := civil.NthWeekday(tt.y, tt.m, tt.wd, tt.n)
			if tt.want == "" {
				if ok {
					t.Fatalf("want missing, got %v", got)
				}
				return
			}
			if !ok || civil.Format(got) != tt.want {
				t.Fatalf("got %v ok=%v, want %s", got, ok, tt.want)
			}
			if got.Weekday() != tt.wd || civil.Ordinal(got) != tt.n {
				t.Fatalf("result %v is not the %d-th %v", got, tt.n, tt.wd)
			}
		})
	}
}
