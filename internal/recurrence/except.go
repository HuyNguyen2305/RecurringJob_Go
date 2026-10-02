package recurrence

import (
	"time"

	"recurringjob/internal/common/civil"
)

var conditionPeriods = map[string]int{"1st": 1, "2nd": 2, "3rd": 3, "4th": 4, "5th": 5}

// isExcepted reports whether d is skipped by the rule's except settings.
func isExcepted(r Rule, d time.Time, exclude map[string]struct{}) bool {
	switch r.exceptType() {
	case ExceptMonth:
		for _, m := range r.ExceptMonths {
			if int(d.Month()) == m {
				return true
			}
		}
	case ExceptCondition:
		if r.ExceptConditionDayOfWeek == nil || int(d.Weekday()) != *r.ExceptConditionDayOfWeek {
			return false
		}
		switch r.ExceptConditionEvery {
		case ConditionEveryWeek:
			return true
		case ConditionEveryMonth:
			if r.ExceptConditionPeriod == "last" {
				return d.Day()+7 > civil.DaysInMonth(d.Year(), d.Month())
			}
			n, ok := conditionPeriods[r.ExceptConditionPeriod]
			return ok && civil.Ordinal(d) == n
		}
	case ExceptFrequency:
		if exclude == nil {
			return false
		}
		_, ok := exclude[civil.Format(d)]
		return ok
	}
	return false
}
