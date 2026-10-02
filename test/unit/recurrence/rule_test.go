package recurrence_test

import (
	"errors"
	"reflect"
	"testing"

	"recurringjob/internal/recurrence"
)

// The rule defaults (interval, endsType, exceptType) are applied inside the
// engine; these tests pin them through its public behaviour.
func TestRuleDefaultsThroughTheEngine(t *testing.T) {
	gen := func(t *testing.T, r recurrence.Rule, opts recurrence.Options) []string {
		t.Helper()
		got, err := recurrence.GenerateOccurrences(r, d("2026-10-02"), opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return got
	}
	freqs := map[string]recurrence.Rule{
		"daily":   {Frequency: recurrence.FreqDaily},
		"weekly":  {Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyEvery, WeeklyDaysOfWeek: []int{5}},
		"monthly": {Frequency: recurrence.FreqMonthly, MonthlyRepeatBy: recurrence.RepeatDayOfMonth},
		"yearly":  {Frequency: recurrence.FreqYearly, YearlyRepeatBy: recurrence.RepeatDayOfYear},
	}

	for name, base := range freqs {
		t.Run(name+": interval 0 and negative mean 1", func(t *testing.T) {
			one := base
			one.Interval = 1
			want := gen(t, one, recurrence.Options{Limit: 6})
			for _, iv := range []int{0, -1, -50} {
				r := base
				r.Interval = iv
				if got := gen(t, r, recurrence.Options{Limit: 6}); !reflect.DeepEqual(got, want) {
					t.Errorf("interval %d: got %v, want %v", iv, got, want)
				}
			}
		})
		t.Run(name+": an explicit interval changes the schedule", func(t *testing.T) {
			one, three := base, base
			one.Interval, three.Interval = 1, 3
			if reflect.DeepEqual(gen(t, one, recurrence.Options{Limit: 6}), gen(t, three, recurrence.Options{Limit: 6})) {
				t.Error("interval 3 produced the same dates as interval 1")
			}
		})
		t.Run(name+": empty endsType behaves like never", func(t *testing.T) {
			never := base
			never.EndsType = recurrence.EndsNever
			opts := recurrence.Options{Limit: 6}
			if !reflect.DeepEqual(gen(t, base, opts), gen(t, never, opts)) {
				t.Error("empty endsType differs from 'never'")
			}
			// ... including the guard: an endless rule needs a bound.
			if _, err := recurrence.GenerateOccurrences(base, d("2026-10-02"), recurrence.Options{}); err == nil {
				t.Error("an empty endsType must still require a 'to' date or a limit")
			}
		})
		t.Run(name+": empty exceptType behaves like off", func(t *testing.T) {
			ignored := base
			ignored.ExceptMonths = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12} // only used when exceptType=month
			off := base
			off.ExceptType = recurrence.ExceptOff
			opts := recurrence.Options{Limit: 6}
			want := gen(t, off, opts)
			if got := gen(t, ignored, opts); !reflect.DeepEqual(got, want) {
				t.Errorf("empty exceptType applied the month list: %v", got)
			}
			if len(want) == 0 {
				t.Fatal("test is vacuous")
			}
		})
	}
}

func TestErrorType(t *testing.T) {
	_, err := recurrence.GenerateOccurrences(recurrence.Rule{Frequency: "hourly"}, d("2026-10-02"), recurrence.Options{Limit: 1})
	var re *recurrence.Error
	if !errors.As(err, &re) {
		t.Fatalf("want *recurrence.Error, got %T", err)
	}
	if re.Msg == "" || err.Error() != re.Msg {
		t.Fatalf("Error() = %q, Msg = %q", err.Error(), re.Msg)
	}
}
