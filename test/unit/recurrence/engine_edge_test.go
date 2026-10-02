package recurrence_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/recurrence"
)

func TestGenerateOccurrencesEdges(t *testing.T) {
	plus7 := time.FixedZone("+7", 7*3600)
	tests := []struct {
		name   string
		rule   recurrence.Rule
		anchor time.Time
		opts   recurrence.Options
		want   []string
	}{
		{
			name:   "interval 0 and negative are treated as 1",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily, Interval: -3},
			anchor: d("2026-10-01"), opts: recurrence.Options{Limit: 3},
			want: []string{"2026-10-01", "2026-10-02", "2026-10-03"},
		},
		{
			name:   "From long before the anchor changes nothing",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily},
			anchor: d("2026-10-01"), opts: recurrence.Options{From: d("2020-01-01"), Limit: 2},
			want: []string{"2026-10-01", "2026-10-02"},
		},
		{
			name:   "To before the anchor gives nothing",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily},
			anchor: d("2026-10-01"), opts: recurrence.Options{To: d("2026-09-01")},
			want: []string{},
		},
		{
			name:   "To equal to the anchor gives only the anchor",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily},
			anchor: d("2026-10-01"), opts: recurrence.Options{To: d("2026-10-01")},
			want: []string{"2026-10-01"},
		},
		{
			name:   "From after To gives nothing",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily},
			anchor: d("2026-10-01"), opts: recurrence.Options{From: d("2026-10-10"), To: d("2026-10-05")},
			want: []string{},
		},
		{
			name:   "limit 1",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily},
			anchor: d("2026-10-01"), opts: recurrence.Options{Limit: 1},
			want: []string{"2026-10-01"},
		},
		{
			name:   "anchor with a time zone and clock time is truncated to its UTC date",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily},
			anchor: time.Date(2026, 10, 3, 2, 0, 0, 0, plus7), // 2026-10-02 19:00 UTC
			opts:   recurrence.Options{Limit: 2},
			want:   []string{"2026-10-02", "2026-10-03"},
		},
		{
			name:   "From and To are truncated to dates",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily},
			anchor: d("2026-10-01"),
			opts:   recurrence.Options{From: time.Date(2026, 10, 3, 15, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)},
			want:   []string{"2026-10-03", "2026-10-04"},
		},
		{
			name:   "weekly: unsorted weekdays are emitted in date order",
			rule:   recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyEvery, WeeklyDaysOfWeek: []int{5, 1, 3}},
			anchor: d("2026-10-04"), // Sunday
			opts:   recurrence.Options{Limit: 4},
			want:   []string{"2026-10-05", "2026-10-07", "2026-10-09", "2026-10-12"},
		},
		{
			name:   "weekly: every weekday selected, anchor mid-week",
			rule:   recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyEvery, WeeklyDaysOfWeek: []int{0, 1, 2, 3, 4, 5, 6}},
			anchor: d("2026-10-07"), // Wednesday
			opts:   recurrence.Options{Limit: 6},
			want:   []string{"2026-10-07", "2026-10-08", "2026-10-09", "2026-10-10", "2026-10-11", "2026-10-12"},
		},
		{
			name:   "weekly: anchor on Sunday, Sunday selected",
			rule:   recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyEvery, WeeklyDaysOfWeek: []int{0}},
			anchor: d("2026-10-04"),
			opts:   recurrence.Options{Limit: 2},
			want:   []string{"2026-10-04", "2026-10-11"},
		},
		{
			name:   "weekly: anchor on Saturday, Sunday selected starts the next week",
			rule:   recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyEvery, WeeklyDaysOfWeek: []int{0}},
			anchor: d("2026-10-03"),
			opts:   recurrence.Options{Limit: 2},
			want:   []string{"2026-10-04", "2026-10-11"},
		},
		{
			name:   "weekly: interval 2 keeps the parity of the anchor's week",
			rule:   recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyEvery, Interval: 2, WeeklyDaysOfWeek: []int{0, 6}},
			anchor: d("2026-10-03"), // Saturday; its week started 09-27
			opts:   recurrence.Options{Limit: 3},
			want:   []string{"2026-10-03", "2026-10-11", "2026-10-17"},
		},
		{
			name:   "weekly: interval 3",
			rule:   recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyEvery, Interval: 3, WeeklyDaysOfWeek: []int{2}},
			anchor: d("2026-10-06"), // Tuesday
			opts:   recurrence.Options{Limit: 3},
			want:   []string{"2026-10-06", "2026-10-27", "2026-11-17"},
		},
		{
			name:   "first_third with endsAfter counts every scheduled date",
			rule:   recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyFirstThird, WeeklyDaysOfWeek: []int{5}, EndsType: recurrence.EndsAfter, EndsAfterCount: 3},
			anchor: d("2026-10-01"),
			opts:   recurrence.Options{Limit: recurrence.Unlimited},
			want:   []string{"2026-10-02", "2026-10-16", "2026-11-06"},
		},
		{
			name:   "second_fourth with two weekdays merges and sorts by date",
			rule:   recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklySecondFour, WeeklyDaysOfWeek: []int{5, 2}},
			anchor: d("2026-10-01"),
			opts:   recurrence.Options{Limit: 4},
			want:   []string{"2026-10-09", "2026-10-13", "2026-10-23", "2026-10-27"},
		},
		{
			name:   "first_third starting mid-month drops earlier dates of that month",
			rule:   recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyFirstThird, WeeklyDaysOfWeek: []int{5}},
			anchor: d("2026-10-03"),
			opts:   recurrence.Options{Limit: 2},
			want:   []string{"2026-10-16", "2026-11-06"},
		},
		{
			name:   "monthly day_of_month: Dec -> Jan rollover keeps the day",
			rule:   recurrence.Rule{Frequency: recurrence.FreqMonthly, MonthlyRepeatBy: recurrence.RepeatDayOfMonth},
			anchor: d("2026-11-30"),
			opts:   recurrence.Options{Limit: 4},
			want:   []string{"2026-11-30", "2026-12-30", "2027-01-30", "2027-02-28"},
		},
		{
			name:   "monthly day_of_month: interval 12 from Feb 29 clamps in non-leap years only",
			rule:   recurrence.Rule{Frequency: recurrence.FreqMonthly, MonthlyRepeatBy: recurrence.RepeatDayOfMonth, Interval: 12},
			anchor: d("2028-02-29"),
			opts:   recurrence.Options{Limit: 6},
			want:   []string{"2028-02-29", "2029-02-28", "2030-02-28", "2031-02-28", "2032-02-29", "2033-02-28"},
		},
		{
			name:   "monthly day_of_month: day 29 survives February clamp without drifting",
			rule:   recurrence.Rule{Frequency: recurrence.FreqMonthly, MonthlyRepeatBy: recurrence.RepeatDayOfMonth},
			anchor: d("2026-01-29"),
			opts:   recurrence.Options{Limit: 4},
			want:   []string{"2026-01-29", "2026-02-28", "2026-03-29", "2026-04-29"},
		},
		{
			name:   "monthly day_of_week: 1st Monday with interval 3",
			rule:   recurrence.Rule{Frequency: recurrence.FreqMonthly, MonthlyRepeatBy: recurrence.RepeatDayOfWeek, Interval: 3},
			anchor: d("2026-10-05"),
			opts:   recurrence.Options{Limit: 3},
			want:   []string{"2026-10-05", "2027-01-04", "2027-04-05"},
		},
		{
			name:   "yearly day_of_year: Feb 29 with interval 4 always lands on leap days",
			rule:   recurrence.Rule{Frequency: recurrence.FreqYearly, YearlyRepeatBy: recurrence.RepeatDayOfYear, Interval: 4},
			anchor: d("2028-02-29"),
			opts:   recurrence.Options{Limit: 3},
			want:   []string{"2028-02-29", "2032-02-29", "2036-02-29"},
		},
		{
			name:   "endsOnDate before the anchor gives nothing",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily, EndsType: recurrence.EndsOnDate, EndsOnDate: "2026-09-01"},
			anchor: d("2026-10-01"),
			opts:   recurrence.Options{Limit: recurrence.Unlimited},
			want:   []string{},
		},
		{
			name:   "endsAfterCount 1",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily, EndsType: recurrence.EndsAfter, EndsAfterCount: 1},
			anchor: d("2026-10-01"),
			opts:   recurrence.Options{Limit: recurrence.Unlimited},
			want:   []string{"2026-10-01"},
		},
		{
			name:   "limit smaller than endsAfterCount wins",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily, EndsType: recurrence.EndsAfter, EndsAfterCount: 10},
			anchor: d("2026-10-01"),
			opts:   recurrence.Options{Limit: 2},
			want:   []string{"2026-10-01", "2026-10-02"},
		},
		{
			name:   "endsAfterCount smaller than limit wins",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily, EndsType: recurrence.EndsAfter, EndsAfterCount: 2},
			anchor: d("2026-10-01"),
			opts:   recurrence.Options{Limit: 10},
			want:   []string{"2026-10-01", "2026-10-02"},
		},
		{
			name:   "endsAfter + From: slots before From still use up the count",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily, EndsType: recurrence.EndsAfter, EndsAfterCount: 5},
			anchor: d("2026-10-01"),
			opts:   recurrence.Options{From: d("2026-10-04"), Limit: recurrence.Unlimited},
			want:   []string{"2026-10-04", "2026-10-05"},
		},
		{
			name:   "except frequency + endsAfter: excluded dates use up slots",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily, EndsType: recurrence.EndsAfter, EndsAfterCount: 4, ExceptType: recurrence.ExceptFrequency, ExceptJobID: "x"},
			anchor: d("2026-10-01"),
			opts:   recurrence.Options{Limit: recurrence.Unlimited, ExcludeDates: map[string]struct{}{"2026-10-02": {}}},
			want:   []string{"2026-10-01", "2026-10-03", "2026-10-04"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := recurrence.GenerateOccurrences(tt.rule, tt.anchor, tt.opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %v\nwant %v", got, tt.want)
			}
		})
	}
}

func TestGenerateOccurrencesTerminatesOnImpossibleRules(t *testing.T) {
	t.Run("except every month still terminates (iteration cap) and returns nothing", func(t *testing.T) {
		rule := recurrence.Rule{Frequency: recurrence.FreqDaily, ExceptType: recurrence.ExceptMonth, ExceptMonths: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}}
		start := time.Now()
		got, err := recurrence.GenerateOccurrences(rule, d("2026-01-01"), recurrence.Options{Limit: 5})
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("took too long")
		}
	})
	t.Run("sparse yearly day_of_week with a huge interval terminates", func(t *testing.T) {
		rule := recurrence.Rule{Frequency: recurrence.FreqYearly, YearlyRepeatBy: recurrence.RepeatDayOfWeek, Interval: 999}
		start := time.Now()
		got, err := recurrence.GenerateOccurrences(rule, d("2028-02-29"), recurrence.Options{Limit: 5}) // 5th Tuesday of February
		if err != nil || len(got) == 0 || got[0] != "2028-02-29" {
			t.Fatalf("got %v, %v", got, err)
		}
		for _, s := range got {
			if !strings.HasSuffix(s, "-02-29") {
				t.Errorf("%s is not a Feb 29", s)
			}
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("took too long")
		}
	})
}

// FINDING F1: dates beyond year 9999 are rendered with five digits, which is
// not a YYYY-MM-DD civil date and cannot be parsed back by civil.Parse.
func TestGenerateOccurrencesNeverEmitsYearsAbove9999(t *testing.T) {
	tests := []struct {
		name   string
		rule   recurrence.Rule
		anchor string
	}{
		{"daily across the year-9999 boundary", recurrence.Rule{Frequency: recurrence.FreqDaily}, "9999-12-30"},
		{"yearly", recurrence.Rule{Frequency: recurrence.FreqYearly, YearlyRepeatBy: recurrence.RepeatDayOfYear}, "9998-06-01"},
		{"sparse yearly day_of_week with huge interval", recurrence.Rule{Frequency: recurrence.FreqYearly, YearlyRepeatBy: recurrence.RepeatDayOfWeek, Interval: 999}, "2028-02-29"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := recurrence.GenerateOccurrences(tt.rule, d(tt.anchor), recurrence.Options{Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range got {
				if _, perr := civil.Parse(s); perr != nil {
					t.Errorf("emitted %q, which is not a valid civil date", s)
				}
			}
		})
	}
}

// Weekly weekdays must be unique; the service validates that, but the engine
// must not emit the same date twice if a bad rule ever reaches it.
func TestGenerateOccurrencesDuplicateWeekdaysEmitEachDateOnce(t *testing.T) {
	for _, period := range []string{recurrence.WeeklyEvery, recurrence.WeeklyFirstThird, recurrence.WeeklySecondFour} {
		t.Run(period, func(t *testing.T) {
			rule := recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: period, WeeklyDaysOfWeek: []int{1, 1, 3}}
			got, err := recurrence.GenerateOccurrences(rule, d("2026-10-04"), recurrence.Options{Limit: 8})
			if err != nil {
				return // rejecting the rule is also acceptable
			}
			seen := map[string]bool{}
			for _, s := range got {
				if seen[s] {
					t.Fatalf("date %s emitted twice: %v", s, got)
				}
				seen[s] = true
			}
		})
	}
}

func TestGenerateOccurrencesRejectsOutOfRangeWeekdays(t *testing.T) {
	for _, days := range [][]int{{7}, {-1}, {1, 9}} {
		rule := recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyEvery, WeeklyDaysOfWeek: days}
		_, err := recurrence.GenerateOccurrences(rule, d("2026-10-01"), recurrence.Options{Limit: 3})
		var re *recurrence.Error
		if !errors.As(err, &re) {
			t.Errorf("days %v: want *Error, got %v", days, err)
		}
	}
}

// F1 fix: a series ends at the last valid civil date (year 9999) instead of
// emitting dates the calendar cannot represent.
func TestGenerateOccurrencesStopsAtYear9999(t *testing.T) {
	friday := d("9999-12-31")
	tests := []struct {
		name   string
		rule   recurrence.Rule
		anchor string
		opts   recurrence.Options
		want   []string
	}{
		{"daily", recurrence.Rule{Frequency: recurrence.FreqDaily}, "9999-12-30", recurrence.Options{Limit: 10},
			[]string{"9999-12-30", "9999-12-31"}},
		{"daily from the very last day", recurrence.Rule{Frequency: recurrence.FreqDaily}, "9999-12-31", recurrence.Options{Limit: 10},
			[]string{"9999-12-31"}},
		{"daily with endsAfter far beyond the calendar", recurrence.Rule{Frequency: recurrence.FreqDaily, EndsType: recurrence.EndsAfter, EndsAfterCount: 100}, "9999-12-30", recurrence.Options{Limit: recurrence.Unlimited},
			[]string{"9999-12-30", "9999-12-31"}},
		{"daily with a To beyond the calendar", recurrence.Rule{Frequency: recurrence.FreqDaily}, "9999-12-30", recurrence.Options{To: time.Date(10005, 1, 1, 0, 0, 0, 0, time.UTC)},
			[]string{"9999-12-30", "9999-12-31"}},
		{"weekly", recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyEvery, WeeklyDaysOfWeek: []int{int(friday.Weekday())}}, "9999-12-10", recurrence.Options{Limit: 10}, // 12-10 is itself a Friday
			[]string{"9999-12-10", "9999-12-17", "9999-12-24", "9999-12-31"}},
		{"weekly first_third", recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyFirstThird, WeeklyDaysOfWeek: []int{int(d("9999-12-17").Weekday())}}, "9999-11-01", recurrence.Options{Limit: 10},
			[]string{"9999-11-05", "9999-11-19", "9999-12-03", "9999-12-17"}},
		{"monthly day_of_month", recurrence.Rule{Frequency: recurrence.FreqMonthly, MonthlyRepeatBy: recurrence.RepeatDayOfMonth}, "9999-11-15", recurrence.Options{Limit: 10},
			[]string{"9999-11-15", "9999-12-15"}},
		{"monthly clamped day", recurrence.Rule{Frequency: recurrence.FreqMonthly, MonthlyRepeatBy: recurrence.RepeatDayOfMonth}, "9999-10-31", recurrence.Options{Limit: 10},
			[]string{"9999-10-31", "9999-11-30", "9999-12-31"}},
		{"yearly", recurrence.Rule{Frequency: recurrence.FreqYearly, YearlyRepeatBy: recurrence.RepeatDayOfYear}, "9998-06-01", recurrence.Options{Limit: 10},
			[]string{"9998-06-01", "9999-06-01"}},
		{"yearly with an interval that jumps past the end", recurrence.Rule{Frequency: recurrence.FreqYearly, YearlyRepeatBy: recurrence.RepeatDayOfYear, Interval: 5}, "9996-06-01", recurrence.Options{Limit: 10},
			[]string{"9996-06-01"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := recurrence.GenerateOccurrences(tt.rule, d(tt.anchor), tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %v\nwant %v", got, tt.want)
			}
			for _, s := range got {
				if _, perr := civil.Parse(s); perr != nil {
					t.Errorf("%q is not a valid civil date", s)
				}
			}
		})
	}
}

// F2 fix: a weekday list with duplicates is rejected, never emitted twice.
func TestGenerateOccurrencesRejectsDuplicateWeekdays(t *testing.T) {
	periods := []string{recurrence.WeeklyEvery, recurrence.WeeklyFirstThird, recurrence.WeeklySecondFour}
	for _, period := range periods {
		for _, days := range [][]int{{1, 1}, {3, 5, 3}, {0, 0, 0}, {6, 1, 2, 6}} {
			rule := recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: period, WeeklyDaysOfWeek: days}
			_, err := recurrence.GenerateOccurrences(rule, d("2026-10-04"), recurrence.Options{Limit: 5})
			var re *recurrence.Error
			if !errors.As(err, &re) {
				t.Errorf("%s %v: want *recurrence.Error, got %v", period, days, err)
			}
		}
		// A unique, unsorted list is still fine.
		rule := recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: period, WeeklyDaysOfWeek: []int{5, 1, 3}}
		got, err := recurrence.GenerateOccurrences(rule, d("2026-10-04"), recurrence.Options{Limit: 5})
		if err != nil || len(got) != 5 {
			t.Errorf("%s unique list: got %v, %v", period, got, err)
		}
	}
}
