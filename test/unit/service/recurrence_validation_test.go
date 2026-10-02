package service_test

import (
	"context"
	"errors"
	"testing"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
	"recurringjob/internal/service"
)

type mockJobs map[string]*model.Job

func (m mockJobs) GetJob(_ context.Context, id string) (*model.Job, error) {
	if j, ok := m[id]; ok {
		return j, nil
	}
	return nil, apperror.NotFound("job not found")
}

func ip(i int) *int { return &i }

func TestValidateRecurrence(t *testing.T) {
	jobDate := civil.New(2026, 10, 2)
	jobs := mockJobs{"00000000-0000-0000-0000-00000000000b": {ID: "00000000-0000-0000-0000-00000000000b", Date: jobDate}}

	tests := []struct {
		name   string
		rule   recurrence.Rule
		status int // 0 = valid
	}{
		{"daily ok", recurrence.Rule{Frequency: "daily"}, 0},
		{"unknown frequency", recurrence.Rule{Frequency: "hourly"}, 400},
		{"interval at the maximum (999)", recurrence.Rule{Frequency: "daily", Interval: service.MaxInterval}, 0},
		{"interval just over the maximum (1000)", recurrence.Rule{Frequency: "daily", Interval: service.MaxInterval + 1}, 400},
		{"negative interval", recurrence.Rule{Frequency: "daily", Interval: -1}, 400},
		{"weekly ok", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{1, 3}}, 0},
		{"weekly no days", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every"}, 400},
		{"weekly duplicate days", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{1, 1}}, 400},
		{"weekly day out of range", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{7}}, 400},
		{"weekly no period", recurrence.Rule{Frequency: "weekly", WeeklyDaysOfWeek: []int{1}}, 400},
		{"monthly ok", recurrence.Rule{Frequency: "monthly", MonthlyRepeatBy: "day_of_week"}, 0},
		{"monthly missing repeatBy", recurrence.Rule{Frequency: "monthly"}, 400},
		{"monthly day_of_year invalid", recurrence.Rule{Frequency: "monthly", MonthlyRepeatBy: "day_of_year"}, 400},
		{"yearly ok", recurrence.Rule{Frequency: "yearly", YearlyRepeatBy: "day_of_year"}, 0},
		{"yearly missing repeatBy", recurrence.Rule{Frequency: "yearly"}, 400},
		{"after ok", recurrence.Rule{Frequency: "daily", EndsType: "after", EndsAfterCount: 3}, 0},
		{"after without count", recurrence.Rule{Frequency: "daily", EndsType: "after"}, 400},
		{"on_date ok (same day)", recurrence.Rule{Frequency: "daily", EndsType: "on_date", EndsOnDate: "2026-10-02"}, 0},
		{"on_date missing", recurrence.Rule{Frequency: "daily", EndsType: "on_date"}, 400},
		{"on_date before job date", recurrence.Rule{Frequency: "daily", EndsType: "on_date", EndsOnDate: "2026-10-01"}, 400},
		{"on_date malformed", recurrence.Rule{Frequency: "daily", EndsType: "on_date", EndsOnDate: "10/01/2026"}, 400},
		{"bad endsType", recurrence.Rule{Frequency: "daily", EndsType: "forever"}, 400},
		{"except month ok", recurrence.Rule{Frequency: "daily", ExceptType: "month", ExceptMonths: []int{1}}, 0},
		{"except month empty", recurrence.Rule{Frequency: "daily", ExceptType: "month"}, 400},
		{"except month out of range", recurrence.Rule{Frequency: "daily", ExceptType: "month", ExceptMonths: []int{13}}, 400},
		{"except condition week ok", recurrence.Rule{Frequency: "daily", ExceptType: "condition", ExceptConditionEvery: "week", ExceptConditionDayOfWeek: ip(0)}, 0},
		{"except condition month ok", recurrence.Rule{Frequency: "daily", ExceptType: "condition", ExceptConditionEvery: "month", ExceptConditionPeriod: "last", ExceptConditionDayOfWeek: ip(5)}, 0},
		{"except condition no dow", recurrence.Rule{Frequency: "daily", ExceptType: "condition", ExceptConditionEvery: "week"}, 400},
		{"except condition no every", recurrence.Rule{Frequency: "daily", ExceptType: "condition", ExceptConditionDayOfWeek: ip(1)}, 400},
		{"except condition month no period", recurrence.Rule{Frequency: "daily", ExceptType: "condition", ExceptConditionEvery: "month", ExceptConditionDayOfWeek: ip(1)}, 400},
		{"except condition bad period", recurrence.Rule{Frequency: "daily", ExceptType: "condition", ExceptConditionEvery: "month", ExceptConditionPeriod: "6th", ExceptConditionDayOfWeek: ip(1)}, 400},
		{"except frequency ok", recurrence.Rule{Frequency: "daily", ExceptType: "frequency", ExceptJobID: "00000000-0000-0000-0000-00000000000b"}, 0},
		{"except frequency malformed id is 400, not 404", recurrence.Rule{Frequency: "daily", ExceptType: "frequency", ExceptJobID: "nope"}, 400},
		{"except frequency missing id", recurrence.Rule{Frequency: "daily", ExceptType: "frequency"}, 400},
		{"except frequency unknown job", recurrence.Rule{Frequency: "daily", ExceptType: "frequency", ExceptJobID: "00000000-0000-0000-0000-0000000000ff"}, 404},
		{"bad exceptType", recurrence.Rule{Frequency: "daily", ExceptType: "sometimes"}, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := tt.rule
			err := service.ValidateRecurrence(context.Background(), &rule, jobDate, jobs)
			if tt.status == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var ae *apperror.AppError
			if !errors.As(err, &ae) || ae.Status != tt.status {
				t.Fatalf("want status %d, got %v", tt.status, err)
			}
		})
	}

	t.Run("defaults are filled", func(t *testing.T) {
		rule := recurrence.Rule{Frequency: "daily"}
		if err := service.ValidateRecurrence(context.Background(), &rule, jobDate, jobs); err != nil {
			t.Fatal(err)
		}
		if rule.EndsType != "never" || rule.Interval != 1 || rule.ExceptType != "off" {
			t.Fatalf("defaults not applied: %+v", rule)
		}
	})
	t.Run("nil rule (one-off) is valid", func(t *testing.T) {
		if err := service.ValidateRecurrence(context.Background(), nil, jobDate, jobs); err != nil {
			t.Fatal(err)
		}
	})
}

// neverCalled fails the test if the job lookup is used.
type neverCalled struct{ t *testing.T }

func (n neverCalled) GetJob(context.Context, string) (*model.Job, error) {
	n.t.Helper()
	n.t.Error("GetJob must not be called for this rule")
	return nil, nil
}

func TestValidateRecurrenceMore(t *testing.T) {
	jobDate := civil.New(2026, 10, 2)
	tests := []struct {
		name   string
		rule   recurrence.Rule
		status int
	}{
		{"condition weekday -1", recurrence.Rule{Frequency: "daily", ExceptType: "condition", ExceptConditionEvery: "week", ExceptConditionDayOfWeek: ip(-1)}, 400},
		{"condition weekday 7", recurrence.Rule{Frequency: "daily", ExceptType: "condition", ExceptConditionEvery: "week", ExceptConditionDayOfWeek: ip(7)}, 400},
		{"condition weekday 0 and 6 are valid", recurrence.Rule{Frequency: "daily", ExceptType: "condition", ExceptConditionEvery: "week", ExceptConditionDayOfWeek: ip(6)}, 0},
		{"condition every=year", recurrence.Rule{Frequency: "daily", ExceptType: "condition", ExceptConditionEvery: "year", ExceptConditionDayOfWeek: ip(1)}, 400},
		{"condition every=week ignores a bogus period", recurrence.Rule{Frequency: "daily", ExceptType: "condition", ExceptConditionEvery: "week", ExceptConditionPeriod: "bogus", ExceptConditionDayOfWeek: ip(1)}, 0},
		{"yearly with a monthly-only repeatBy", recurrence.Rule{Frequency: "yearly", YearlyRepeatBy: "day_of_month"}, 400},
		{"monthly with a yearly-only repeatBy", recurrence.Rule{Frequency: "monthly", MonthlyRepeatBy: "day_of_year"}, 400},
		{"weekly day -1", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{-1}}, 400},
		{"weekly with every valid period", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "second_fourth", WeeklyDaysOfWeek: []int{0, 6}}, 0},
		{"weekly bogus period", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "monthly", WeeklyDaysOfWeek: []int{1}}, 400},
		{"fields of other frequencies are ignored", recurrence.Rule{Frequency: "daily", WeeklyDaysOfWeek: []int{1, 1}, WeeklyPeriod: "bogus", MonthlyRepeatBy: "bogus"}, 0},
		{"empty frequency", recurrence.Rule{}, 400},
		{"exceptMonths with a duplicate is accepted", recurrence.Rule{Frequency: "daily", ExceptType: "month", ExceptMonths: []int{3, 3}}, 0},
		{"exceptMonths 0", recurrence.Rule{Frequency: "daily", ExceptType: "month", ExceptMonths: []int{0}}, 400},
		{"endsAfterCount negative", recurrence.Rule{Frequency: "daily", EndsType: "after", EndsAfterCount: -1}, 400},
		{"endsAfterCount ignored when endsType is never", recurrence.Rule{Frequency: "daily", EndsType: "never", EndsAfterCount: -9}, 0},
		{"endsOnDate impossible date", recurrence.Rule{Frequency: "daily", EndsType: "on_date", EndsOnDate: "2026-02-30"}, 400},
		{"endsOnDate equal to the job date", recurrence.Rule{Frequency: "daily", EndsType: "on_date", EndsOnDate: "2026-10-02"}, 0},
		{"a frequency error wins over a later except error", recurrence.Rule{Frequency: "hourly", ExceptType: "frequency", ExceptJobID: "00000000-0000-0000-0000-0000000000ff"}, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := tt.rule
			err := service.ValidateRecurrence(context.Background(), &rule, jobDate, neverCalled{t})
			if tt.status == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if got := statusOf(t, err); got != tt.status {
				t.Fatalf("status %d, want %d (%v)", got, tt.status, err)
			}
		})
	}

	t.Run("an infrastructure error from the lookup is returned as is", func(t *testing.T) {
		boom := errors.New("db down")
		rule := recurrence.Rule{Frequency: "daily", ExceptType: "frequency", ExceptJobID: "00000000-0000-0000-0000-0000000000ff"}
		if err := service.ValidateRecurrence(context.Background(), &rule, jobDate, faultyJobs{err: boom}); !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("a rule is normalised in place, a nil rule is a no-op", func(t *testing.T) {
		rule := recurrence.Rule{Frequency: "daily", Interval: 0}
		if err := service.ValidateRecurrence(context.Background(), &rule, jobDate, neverCalled{t}); err != nil || rule.Interval != 1 {
			t.Fatalf("rule %+v err %v", rule, err)
		}
		if err := service.ValidateRecurrence(context.Background(), nil, jobDate, neverCalled{t}); err != nil {
			t.Fatal(err)
		}
	})
}
