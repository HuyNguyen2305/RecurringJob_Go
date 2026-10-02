// Package fixtures builds reusable model values for tests.
package fixtures

import (
	"time"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
)

// OneOffJob is a non-recurring, unconfirmed job on date.
func OneOffJob(date time.Time) *model.Job {
	return &model.Job{Date: civil.Truncate(date), Status: "unconfirmed"}
}

// DailyJob is an endless daily unconfirmed job starting on date.
func DailyJob(date time.Time) *model.Job {
	return &model.Job{
		Date: civil.Truncate(date), Status: "unconfirmed",
		Recurrence: &recurrence.Rule{Frequency: recurrence.FreqDaily, Interval: 1, EndsType: recurrence.EndsNever, ExceptType: recurrence.ExceptOff},
	}
}
