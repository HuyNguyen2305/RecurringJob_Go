package model

import (
	"time"

	"recurringjob/internal/recurrence"
)

// Job is one row of the jobs table. Date is the civil anchor date; a nil
// Recurrence means a one-off job.
type Job struct {
	ID         string           `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Date       time.Time        `gorm:"type:date;not null"`
	Status     string           `gorm:"not null;default:unconfirmed"`
	Recurrence *recurrence.Rule `gorm:"type:jsonb;serializer:json"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
