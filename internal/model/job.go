package model

import (
	"time"

	"recurringjob/internal/recurrence"
)

// Job is one row of the jobs table. Date is the civil anchor date; a nil
// Recurrence means a one-off job. Every job belongs to a customer, one of the
// customer's locations and a service type.
//
// Customer, Location and ServiceType are read-only: they are loaded with the
// job (and set by the job service before saving, for the snapshot) but never
// written through the job, so the repository omits them on create.
type Job struct {
	ID            string           `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	CustomerID    string           `gorm:"type:uuid;not null"`
	LocationID    string           `gorm:"type:uuid;not null"`
	ServiceTypeID string           `gorm:"type:uuid;not null"`
	Date          time.Time        `gorm:"type:date;not null"`
	StartTime     string           `gorm:"type:time;not null"` // HH:MM:SS
	LengthMinutes int              `gorm:"not null"`
	Status        string           `gorm:"not null;default:unconfirmed"`
	Recurrence    *recurrence.Rule `gorm:"type:jsonb;serializer:json"`
	CreatedAt     time.Time
	UpdatedAt     time.Time

	Customer    *Customer    `gorm:"foreignKey:CustomerID"`
	Location    *Location    `gorm:"foreignKey:LocationID"`
	ServiceType *ServiceType `gorm:"foreignKey:ServiceTypeID"`
}
