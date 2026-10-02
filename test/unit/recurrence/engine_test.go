package recurrence_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/recurrence"
)

func d(s string) time.Time {
	t, err := civil.Parse(s)
	if err != nil {
		panic(err)
	}
	return t
}

func intp(i int) *int { return &i }

func TestGenerateOccurrences(t *testing.T) {
	tests := []struct {
		name   string
		rule   recurrence.Rule
		anchor string
		opts   recurrence.Options
		want   []string
	}{
		{
			name:   "daily interval 3",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily, Interval: 3},
			anchor: "2026-09-29",
			opts:   recurrence.Options{Limit: 4},
			want:   []string{"2026-09-29", "2026-10-02", "2026-10-05", "2026-10-08"},
		},
		{
			name:   "weekly every skips days before anchor in first week",
			rule:   recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyEvery, WeeklyDaysOfWeek: []int{3, 1}},
			anchor: "2026-09-29", // Tuesday
			opts:   recurrence.Options{Limit: 4},
			want:   []string{"2026-09-30", "2026-10-05", "2026-10-07", "2026-10-12"},
		},
		{
			name:   "weekly every interval 2 (parity weeks)",
			rule:   recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyEvery, Interval: 2, WeeklyDaysOfWeek: []int{2}},
			anchor: "2026-09-29",
			opts:   recurrence.Options{Limit: 3},
			want:   []string{"2026-09-29", "2026-10-13", "2026-10-27"},
		},
		{
			name:   "weekly first_third",
			rule:   recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyFirstThird, WeeklyDaysOfWeek: []int{5}},
			anchor: "2026-10-01",
			opts:   recurrence.Options{Limit: 4},
			want:   []string{"2026-10-02", "2026-10-16", "2026-11-06", "2026-11-20"},
		},
		{
			name:   "weekly first_third filters before anchor and ignores interval",
			rule:   recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyFirstThird, Interval: 5, WeeklyDaysOfWeek: []int{5}},
			anchor: "2026-10-10",
			opts:   recurrence.Options{Limit: 2},
			want:   []string{"2026-10-16", "2026-11-06"},
		},
		{
			name:   "weekly second_fourth",
			rule:   recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklySecondFour, WeeklyDaysOfWeek: []int{5}},
			anchor: "2026-10-01",
			opts:   recurrence.Options{Limit: 4},
			want:   []string{"2026-10-09", "2026-10-23", "2026-11-13", "2026-11-27"},
		},
		{
			name:   "monthly day_of_month clamps at month end",
			rule:   recurrence.Rule{Frequency: recurrence.FreqMonthly, MonthlyRepeatBy: recurrence.RepeatDayOfMonth},
			anchor: "2026-01-31",
			opts:   recurrence.Options{Limit: 4},
			want:   []string{"2026-01-31", "2026-02-28", "2026-03-31", "2026-04-30"},
		},
		{
			name:   "monthly day_of_month interval 2",
			rule:   recurrence.Rule{Frequency: recurrence.FreqMonthly, MonthlyRepeatBy: recurrence.RepeatDayOfMonth, Interval: 2},
			anchor: "2026-01-15",
			opts:   recurrence.Options{Limit: 3},
			want:   []string{"2026-01-15", "2026-03-15", "2026-05-15"},
		},
		{
			name:   "monthly day_of_week skips months without a 5th weekday",
			rule:   recurrence.Rule{Frequency: recurrence.FreqMonthly, MonthlyRepeatBy: recurrence.RepeatDayOfWeek},
			anchor: "2026-01-30", // 5th Friday
			opts:   recurrence.Options{Limit: 3},
			want:   []string{"2026-01-30", "2026-05-29", "2026-07-31"},
		},
		{
			name:   "monthly day_of_week 2nd Tuesday",
			rule:   recurrence.Rule{Frequency: recurrence.FreqMonthly, MonthlyRepeatBy: recurrence.RepeatDayOfWeek},
			anchor: "2026-09-08",
			opts:   recurrence.Options{Limit: 3},
			want:   []string{"2026-09-08", "2026-10-13", "2026-11-10"},
		},
		{
			name:   "yearly day_of_year Feb 29 clamps",
			rule:   recurrence.Rule{Frequency: recurrence.FreqYearly, YearlyRepeatBy: recurrence.RepeatDayOfYear},
			anchor: "2028-02-29",
			opts:   recurrence.Options{Limit: 3},
			want:   []string{"2028-02-29", "2029-02-28", "2030-02-28"},
		},
		{
			name:   "yearly day_of_year interval 2",
			rule:   recurrence.Rule{Frequency: recurrence.FreqYearly, YearlyRepeatBy: recurrence.RepeatDayOfYear, Interval: 2},
			anchor: "2026-10-02",
			opts:   recurrence.Options{Limit: 3},
			want:   []string{"2026-10-02", "2028-10-02", "2030-10-02"},
		},
		{
			name:   "yearly day_of_week",
			rule:   recurrence.Rule{Frequency: recurrence.FreqYearly, YearlyRepeatBy: recurrence.RepeatDayOfWeek},
			anchor: "2026-10-02",
			opts:   recurrence.Options{Limit: 3},
			want:   []string{"2026-10-02", "2027-10-01", "2028-10-06"},
		},
		{
			name:   "yearly day_of_week skips years without the 5th weekday",
			rule:   recurrence.Rule{Frequency: recurrence.FreqYearly, YearlyRepeatBy: recurrence.RepeatDayOfWeek},
			anchor: "2028-02-29", // 5th Tuesday of Feb 2028
			opts:   recurrence.Options{Limit: 2},
			want:   []string{"2028-02-29", "2056-02-29"},
		},
		{
			name:   "except month",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily, ExceptType: recurrence.ExceptMonth, ExceptMonths: []int{10}},
			anchor: "2026-09-29",
			opts:   recurrence.Options{To: d("2026-11-02")},
			want:   []string{"2026-09-29", "2026-09-30", "2026-11-01", "2026-11-02"},
		},
		{
			name:   "endsAfterCount counts excepted slots (decided behaviour)",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily, EndsType: recurrence.EndsAfter, EndsAfterCount: 5, ExceptType: recurrence.ExceptMonth, ExceptMonths: []int{10}},
			anchor: "2026-09-29",
			opts:   recurrence.Options{Limit: recurrence.Unlimited},
			want:   []string{"2026-09-29", "2026-09-30"},
		},
		{
			name:   "endsOnDate inclusive",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily, EndsType: recurrence.EndsOnDate, EndsOnDate: "2026-10-02"},
			anchor: "2026-09-30",
			opts:   recurrence.Options{Limit: recurrence.Unlimited},
			want:   []string{"2026-09-30", "2026-10-01", "2026-10-02"},
		},
		{
			name: "except condition every week",
			rule: recurrence.Rule{Frequency: recurrence.FreqDaily, ExceptType: recurrence.ExceptCondition,
				ExceptConditionEvery: recurrence.ConditionEveryWeek, ExceptConditionDayOfWeek: intp(5)},
			anchor: "2026-10-02",
			opts:   recurrence.Options{To: d("2026-10-09")},
			want:   []string{"2026-10-03", "2026-10-04", "2026-10-05", "2026-10-06", "2026-10-07", "2026-10-08"},
		},
		{
			name: "except condition 2nd Friday of month",
			rule: recurrence.Rule{Frequency: recurrence.FreqDaily, ExceptType: recurrence.ExceptCondition, ExceptConditionEvery: recurrence.ConditionEveryMonth,
				ExceptConditionPeriod: "2nd", ExceptConditionDayOfWeek: intp(5)},
			anchor: "2026-10-08",
			opts:   recurrence.Options{To: d("2026-10-10")},
			want:   []string{"2026-10-08", "2026-10-10"},
		},
		{
			name: "except condition last Friday of month",
			rule: recurrence.Rule{Frequency: recurrence.FreqDaily, ExceptType: recurrence.ExceptCondition, ExceptConditionEvery: recurrence.ConditionEveryMonth,
				ExceptConditionPeriod: "last", ExceptConditionDayOfWeek: intp(5)},
			anchor: "2026-10-29",
			opts:   recurrence.Options{To: d("2026-10-31")},
			want:   []string{"2026-10-29", "2026-10-31"},
		},
		{
			name:   "except frequency uses ExcludeDates",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily, ExceptType: recurrence.ExceptFrequency, ExceptJobID: "x"},
			anchor: "2026-10-01",
			opts:   recurrence.Options{Limit: 3, ExcludeDates: map[string]struct{}{"2026-10-03": {}}},
			want:   []string{"2026-10-01", "2026-10-02", "2026-10-04"},
		},
		{
			name:   "except frequency with nil ExcludeDates excludes nothing",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily, ExceptType: recurrence.ExceptFrequency, ExceptJobID: "x"},
			anchor: "2026-10-01",
			opts:   recurrence.Options{Limit: 3},
			want:   []string{"2026-10-01", "2026-10-02", "2026-10-03"},
		},
		{
			name:   "from and to window",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily},
			anchor: "2026-09-29",
			opts:   recurrence.Options{From: d("2026-10-01"), To: d("2026-10-03")},
			want:   []string{"2026-10-01", "2026-10-02", "2026-10-03"},
		},
		{
			name:   "limit counts only emitted dates, not skipped ones before from",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily},
			anchor: "2026-09-29",
			opts:   recurrence.Options{From: d("2026-10-01"), Limit: 2},
			want:   []string{"2026-10-01", "2026-10-02"},
		},
		{
			name:   "from after finite series end gives empty",
			rule:   recurrence.Rule{Frequency: recurrence.FreqDaily, EndsType: recurrence.EndsAfter, EndsAfterCount: 2},
			anchor: "2026-09-29",
			opts:   recurrence.Options{From: d("2027-01-01")},
			want:   []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := recurrence.GenerateOccurrences(tt.rule, d(tt.anchor), tt.opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %v\nwant %v", got, tt.want)
			}
		})
	}
}

func TestGenerateOccurrencesGuards(t *testing.T) {
	daily := recurrence.Rule{Frequency: recurrence.FreqDaily}

	t.Run("endless without to or limit is a validation error", func(t *testing.T) {
		_, err := recurrence.GenerateOccurrences(daily, d("2026-09-29"), recurrence.Options{})
		var re *recurrence.Error
		if !errors.As(err, &re) {
			t.Fatalf("want *Error, got %v", err)
		}
	})
	t.Run("finite series needs no to or limit", func(t *testing.T) {
		got, err := recurrence.GenerateOccurrences(recurrence.Rule{Frequency: recurrence.FreqDaily, EndsType: recurrence.EndsAfter, EndsAfterCount: 3}, d("2026-09-29"), recurrence.Options{})
		if err != nil || len(got) != 3 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("default cap is 1000 when limit unset", func(t *testing.T) {
		got, err := recurrence.GenerateOccurrences(daily, d("2026-01-01"), recurrence.Options{To: d("2040-01-01")})
		if err != nil || len(got) != recurrence.DefaultLimit {
			t.Fatalf("len=%d err=%v", len(got), err)
		}
	})
	t.Run("hard stop after 100000 iterations", func(t *testing.T) {
		got, err := recurrence.GenerateOccurrences(daily, d("2026-01-01"), recurrence.Options{Limit: recurrence.Unlimited, To: d("2999-01-01")})
		if err != nil || len(got) != recurrence.MaxIterations {
			t.Fatalf("len=%d err=%v", len(got), err)
		}
	})
	t.Run("negative limit rejected", func(t *testing.T) {
		if _, err := recurrence.GenerateOccurrences(daily, d("2026-01-01"), recurrence.Options{Limit: -1}); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("malformed rules rejected", func(t *testing.T) {
		bad := []recurrence.Rule{
			{Frequency: "hourly"},
			{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyEvery},
			{Frequency: recurrence.FreqWeekly, WeeklyDaysOfWeek: []int{1}},
			{Frequency: recurrence.FreqMonthly},
			{Frequency: recurrence.FreqYearly},
			{Frequency: recurrence.FreqDaily, EndsType: recurrence.EndsAfter},
			{Frequency: recurrence.FreqDaily, EndsType: recurrence.EndsOnDate, EndsOnDate: "nope"},
		}
		for _, r := range bad {
			if _, err := recurrence.GenerateOccurrences(r, d("2026-01-01"), recurrence.Options{Limit: 1}); err == nil {
				t.Errorf("rule %+v: want error", r)
			}
		}
	})
}
