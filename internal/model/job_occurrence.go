package model

import "time"

// JobOccurrence is a row for an occurrence that changed state. Occurrences
// that never changed are generated, not stored.
type JobOccurrence struct {
	ID              string     `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	JobID           string     `gorm:"type:uuid;not null"`
	OccurrenceDate  time.Time  `gorm:"type:date;not null"`
	Status          string     `gorm:"not null"`
	RescheduledTo   *time.Time `gorm:"type:date"`
	RescheduledFrom *time.Time `gorm:"type:date"`
	CompletedAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
