// Package recurrence is the pure recurrence engine: it turns a Rule and an
// anchor date into occurrence dates. It has no DB or HTTP dependencies.
package recurrence

import "fmt"

const (
	FreqDaily   = "daily"
	FreqWeekly  = "weekly"
	FreqMonthly = "monthly"
	FreqYearly  = "yearly"

	WeeklyEvery      = "every"
	WeeklyFirstThird = "first_third"
	WeeklySecondFour = "second_fourth"

	RepeatDayOfMonth = "day_of_month"
	RepeatDayOfWeek  = "day_of_week"
	RepeatDayOfYear  = "day_of_year"

	EndsNever  = "never"
	EndsAfter  = "after"
	EndsOnDate = "on_date"

	ExceptOff       = "off"
	ExceptMonth     = "month"
	ExceptCondition = "condition"
	ExceptFrequency = "frequency"

	ConditionEveryWeek  = "week"
	ConditionEveryMonth = "month"
)

// Rule is the recurrence JSON stored on a job.
type Rule struct {
	Frequency        string `json:"frequency"`
	Interval         int    `json:"interval,omitempty"`
	WeeklyPeriod     string `json:"weeklyPeriod,omitempty"`
	WeeklyDaysOfWeek []int  `json:"weeklyDaysOfWeek,omitempty"`
	MonthlyRepeatBy  string `json:"monthlyRepeatBy,omitempty"`
	YearlyRepeatBy   string `json:"yearlyRepeatBy,omitempty"`

	EndsType       string `json:"endsType,omitempty"`
	EndsAfterCount int    `json:"endsAfterCount,omitempty"`
	EndsOnDate     string `json:"endsOnDate,omitempty"` // YYYY-MM-DD

	ExceptType               string `json:"exceptType,omitempty"`
	ExceptMonths             []int  `json:"exceptMonths,omitempty"`
	ExceptConditionEvery     string `json:"exceptConditionEvery,omitempty"`
	ExceptConditionPeriod    string `json:"exceptConditionPeriod,omitempty"` // 1st..5th, last
	ExceptConditionDayOfWeek *int   `json:"exceptConditionDayOfWeek,omitempty"`
	ExceptJobID              string `json:"exceptJobId,omitempty"`
}

// Error is a rule/option problem the caller should surface as a validation
// error (HTTP 400). The service layer maps it to apperror.ValidationError.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func errf(format string, a ...any) error { return &Error{Msg: fmt.Sprintf(format, a...)} }

func (r Rule) interval() int {
	if r.Interval < 1 {
		return 1
	}
	return r.Interval
}

func (r Rule) endsType() string {
	if r.EndsType == "" {
		return EndsNever
	}
	return r.EndsType
}

func (r Rule) exceptType() string {
	if r.ExceptType == "" {
		return ExceptOff
	}
	return r.ExceptType
}
