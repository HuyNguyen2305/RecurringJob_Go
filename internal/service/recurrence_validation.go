package service

import (
	"context"
	"fmt"
	"time"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
)

// MaxInterval is the largest accepted rule interval.
const MaxInterval = 999

// JobGetter loads a job by id. It returns an apperror NotFound when the job
// does not exist.
type JobGetter interface {
	GetJob(ctx context.Context, id string) (*model.Job, error)
}

// ValidateRecurrence checks rule against the job's anchor date and fills the
// save-time defaults (endsType=never, exceptType=off, interval=1). A bad rule
// is a 400; an exceptJobId that does not exist is a 404.
func ValidateRecurrence(ctx context.Context, rule *recurrence.Rule, jobDate time.Time, jobs JobGetter) error {
	if rule == nil {
		return nil
	}
	if rule.Interval == 0 {
		rule.Interval = 1
	}
	if rule.EndsType == "" {
		rule.EndsType = recurrence.EndsNever
	}
	if rule.ExceptType == "" {
		rule.ExceptType = recurrence.ExceptOff
	}
	if rule.Interval < 1 || rule.Interval > MaxInterval {
		return bad("interval must be between 1 and %d", MaxInterval)
	}

	switch rule.Frequency {
	case recurrence.FreqDaily:
	case recurrence.FreqWeekly:
		if err := validateWeekly(rule); err != nil {
			return err
		}
	case recurrence.FreqMonthly:
		if rule.MonthlyRepeatBy != recurrence.RepeatDayOfMonth && rule.MonthlyRepeatBy != recurrence.RepeatDayOfWeek {
			return bad("monthlyRepeatBy is required for monthly recurrence (day_of_month or day_of_week)")
		}
	case recurrence.FreqYearly:
		if rule.YearlyRepeatBy != recurrence.RepeatDayOfYear && rule.YearlyRepeatBy != recurrence.RepeatDayOfWeek {
			return bad("yearlyRepeatBy is required for yearly recurrence (day_of_year or day_of_week)")
		}
	default:
		return bad("frequency must be one of daily, weekly, monthly, yearly")
	}

	if err := validateEnds(rule, jobDate); err != nil {
		return err
	}
	return validateExcept(ctx, rule, jobs)
}

func validateWeekly(rule *recurrence.Rule) error {
	if len(rule.WeeklyDaysOfWeek) == 0 {
		return bad("weeklyDaysOfWeek is required for weekly recurrence")
	}
	seen := map[int]bool{}
	for _, d := range rule.WeeklyDaysOfWeek {
		if d < 0 || d > 6 {
			return bad("weeklyDaysOfWeek values must be between 0 and 6")
		}
		if seen[d] {
			return bad("weeklyDaysOfWeek must not contain duplicates")
		}
		seen[d] = true
	}
	switch rule.WeeklyPeriod {
	case recurrence.WeeklyEvery, recurrence.WeeklyFirstThird, recurrence.WeeklySecondFour:
		return nil
	}
	return bad("weeklyPeriod is required for weekly recurrence (every, first_third or second_fourth)")
}

func validateEnds(rule *recurrence.Rule, jobDate time.Time) error {
	switch rule.EndsType {
	case recurrence.EndsNever:
	case recurrence.EndsAfter:
		if rule.EndsAfterCount < 1 {
			return bad("endsAfterCount must be >= 1 when endsType is after")
		}
	case recurrence.EndsOnDate:
		if rule.EndsOnDate == "" {
			return bad("endsOnDate is required when endsType is on_date")
		}
		end, err := civil.Parse(rule.EndsOnDate)
		if err != nil {
			return bad("endsOnDate must be a YYYY-MM-DD date")
		}
		if end.Before(civil.Truncate(jobDate)) {
			return bad("endsOnDate cannot be before the job date")
		}
	default:
		return bad("endsType must be one of never, after, on_date")
	}
	return nil
}

func validateExcept(ctx context.Context, rule *recurrence.Rule, jobs JobGetter) error {
	switch rule.ExceptType {
	case recurrence.ExceptOff:
	case recurrence.ExceptMonth:
		if len(rule.ExceptMonths) == 0 {
			return bad("exceptMonths is required when exceptType is month")
		}
		for _, m := range rule.ExceptMonths {
			if m < 1 || m > 12 {
				return bad("exceptMonths values must be between 1 and 12")
			}
		}
	case recurrence.ExceptCondition:
		if rule.ExceptConditionDayOfWeek == nil || rule.ExceptConditionEvery == "" {
			return bad("exceptConditionDayOfWeek and exceptConditionEvery are required when exceptType is condition")
		}
		if d := *rule.ExceptConditionDayOfWeek; d < 0 || d > 6 {
			return bad("exceptConditionDayOfWeek must be between 0 and 6")
		}
		switch rule.ExceptConditionEvery {
		case recurrence.ConditionEveryWeek:
		case recurrence.ConditionEveryMonth:
			switch rule.ExceptConditionPeriod {
			case "1st", "2nd", "3rd", "4th", "5th", "last":
			case "":
				return bad("exceptConditionPeriod is required when exceptConditionEvery is month")
			default:
				return bad("exceptConditionPeriod must be one of 1st, 2nd, 3rd, 4th, 5th, last")
			}
		default:
			return bad("exceptConditionEvery must be week or month")
		}
	case recurrence.ExceptFrequency:
		if rule.ExceptJobID == "" {
			return bad("exceptJobId is required when exceptType is frequency")
		}
		if err := ValidateID("exceptJobId", rule.ExceptJobID); err != nil {
			return err
		}
		if _, err := jobs.GetJob(ctx, rule.ExceptJobID); err != nil {
			return err
		}
	default:
		return bad("exceptType must be one of off, month, condition, frequency")
	}
	return nil
}

func bad(format string, a ...any) error {
	return apperror.Validation(fmt.Sprintf(format, a...))
}
