package recurrence_test

import (
	"testing"

	"recurringjob/internal/recurrence"
)

// excepted reports whether the except settings of rule skip the given date.
// It runs a one-day daily schedule through the public engine: an empty result
// means the date was excepted.
func excepted(t *testing.T, rule recurrence.Rule, date string, exclude map[string]struct{}) bool {
	t.Helper()
	rule.Frequency = recurrence.FreqDaily
	got, err := recurrence.GenerateOccurrences(rule, d(date), recurrence.Options{To: d(date), Limit: 1, ExcludeDates: exclude})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return len(got) == 0
}

func TestExceptRules(t *testing.T) {
	friday := intp(5)
	excl := map[string]struct{}{"2026-10-03": {}}
	cond := func(every, period string, dow *int) recurrence.Rule {
		return recurrence.Rule{ExceptType: recurrence.ExceptCondition, ExceptConditionEvery: every, ExceptConditionPeriod: period, ExceptConditionDayOfWeek: dow}
	}
	tests := []struct {
		name    string
		rule    recurrence.Rule
		date    string
		exclude map[string]struct{}
		want    bool
	}{
		// off / unknown
		{"off never excepts", recurrence.Rule{ExceptType: recurrence.ExceptOff, ExceptMonths: []int{10}}, "2026-10-05", nil, false},
		{"empty type behaves as off", recurrence.Rule{ExceptMonths: []int{10}}, "2026-10-05", nil, false},
		{"unknown type never excepts", recurrence.Rule{ExceptType: "weird", ExceptMonths: []int{10}}, "2026-10-05", nil, false},

		// month
		{"month hit", recurrence.Rule{ExceptType: recurrence.ExceptMonth, ExceptMonths: []int{10, 12}}, "2026-10-05", nil, true},
		{"month second entry", recurrence.Rule{ExceptType: recurrence.ExceptMonth, ExceptMonths: []int{10, 12}}, "2026-12-31", nil, true},
		{"month miss", recurrence.Rule{ExceptType: recurrence.ExceptMonth, ExceptMonths: []int{10, 12}}, "2026-11-05", nil, false},
		{"month empty list", recurrence.Rule{ExceptType: recurrence.ExceptMonth}, "2026-10-05", nil, false},
		{"month january boundary", recurrence.Rule{ExceptType: recurrence.ExceptMonth, ExceptMonths: []int{1}}, "2026-01-01", nil, true},

		// condition every week
		{"week: matching weekday", cond("week", "", friday), "2026-10-02", nil, true},
		{"week: other weekday", cond("week", "", friday), "2026-10-03", nil, false},
		{"week: period is ignored", cond("week", "2nd", friday), "2026-10-02", nil, true},
		{"week: Sunday is 0", cond("week", "", intp(0)), "2026-10-04", nil, true},

		// condition every month
		{"1st Friday", cond("month", "1st", friday), "2026-10-02", nil, true},
		{"1st Friday: 2nd Friday does not match", cond("month", "1st", friday), "2026-10-09", nil, false},
		{"2nd Friday", cond("month", "2nd", friday), "2026-10-09", nil, true},
		{"3rd Friday", cond("month", "3rd", friday), "2026-10-16", nil, true},
		{"4th Friday", cond("month", "4th", friday), "2026-10-23", nil, true},
		{"5th Friday", cond("month", "5th", friday), "2026-10-30", nil, true},
		{"5th Friday absent month: 4th is not 5th", cond("month", "5th", friday), "2026-04-24", nil, false},
		{"right ordinal wrong weekday", cond("month", "1st", friday), "2026-10-01", nil, false},
		{"last Friday (31-day month)", cond("month", "last", friday), "2026-10-30", nil, true},
		{"not the last Friday (31-day month)", cond("month", "last", friday), "2026-10-23", nil, false},
		{"last Friday (30-day month)", cond("month", "last", friday), "2026-04-24", nil, true},
		{"not last Friday (30-day month)", cond("month", "last", friday), "2026-04-17", nil, false},
		{"last Friday (28-day month)", cond("month", "last", friday), "2026-02-27", nil, true},
		{"not last Friday (28-day month)", cond("month", "last", friday), "2026-02-20", nil, false},
		{"last Friday (leap February)", cond("month", "last", friday), "2028-02-25", nil, true},
		{"not last Friday (leap February)", cond("month", "last", friday), "2028-02-18", nil, false},
		{"last: day+7 equal to month length is not last", cond("month", "last", intp(1)), "2026-11-23", nil, false}, // Mon 23 + 7 = 30 = len(Nov)
		{"last: day+7 just over month length is last", cond("month", "last", intp(1)), "2026-11-30", nil, true},

		// condition: incomplete / invalid
		{"no weekday", cond("week", "", nil), "2026-10-02", nil, false},
		{"no every", cond("", "1st", friday), "2026-10-02", nil, false},
		{"month without period", cond("month", "", friday), "2026-10-02", nil, false},
		{"month with invalid period", cond("month", "6th", friday), "2026-10-02", nil, false},
		{"invalid every", cond("year", "1st", friday), "2026-10-02", nil, false},

		// frequency
		{"frequency hit", recurrence.Rule{ExceptType: recurrence.ExceptFrequency}, "2026-10-03", excl, true},
		{"frequency miss", recurrence.Rule{ExceptType: recurrence.ExceptFrequency}, "2026-10-04", excl, false},
		{"frequency nil set", recurrence.Rule{ExceptType: recurrence.ExceptFrequency}, "2026-10-03", nil, false},
		{"frequency empty set", recurrence.Rule{ExceptType: recurrence.ExceptFrequency}, "2026-10-03", map[string]struct{}{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := excepted(t, tt.rule, tt.date, tt.exclude); got != tt.want {
				t.Fatalf("excepted(%s) = %v, want %v", tt.date, got, tt.want)
			}
		})
	}
}
