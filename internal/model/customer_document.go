package model

import (
	"time"

	"recurringjob/internal/recurrence"
)

const (
	DocTypeEstimate = "estimate"
	DocTypeInvoice  = "invoice"
)

// JobSnapshot is the job as it was when a document was tied to it, so later
// edits to the job or its customer do not rewrite issued documents. The
// camelCase keys match the snapshots the Sequelize schema stores.
type JobSnapshot struct {
	ID              string           `json:"id"`
	Date            string           `json:"date"`
	StartTime       string           `json:"startTime"`
	LengthMinutes   int              `json:"lengthMinutes"`
	Status          string           `json:"status"`
	CustomerID      string           `json:"customerId"`
	CustomerName    string           `json:"customerName"`
	LocationID      string           `json:"locationId"`
	LocationAddress string           `json:"locationAddress"`
	ServiceTypeID   string           `json:"serviceTypeId"`
	ServiceTypeName string           `json:"serviceTypeName"`
	Recurrence      *recurrence.Rule `json:"recurrence"`
}

// CustomerDocument is one row of customer_documents: an estimate or an
// invoice, told apart by Type. Always go through the type-scoped
// repositories (EstimateRepository / InvoiceRepository), never query it
// directly. The database enforces which fields each type needs.
//
// Customer, Location and ServiceType are read-only associations, loaded with
// the document and never written through it.
type CustomerDocument struct {
	ID            string `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Type          string `gorm:"not null"`
	Status        string `gorm:"not null"`
	CustomerID    string `gorm:"type:uuid;not null"`
	LocationID    string `gorm:"type:uuid;not null"`
	ServiceTypeID string `gorm:"type:uuid;not null"`
	Notes         string `gorm:"not null"`

	JobID          *string      `gorm:"type:uuid"`
	JobSnapshot    *JobSnapshot `gorm:"type:jsonb;serializer:json"`
	OccurrenceDate *time.Time   `gorm:"type:date"`

	// Number (EST-000123 / INV-000123) is given by the database on insert.
	Number   string `gorm:"not null;default:(-)"`
	Revision int    `gorm:"not null;default:1"` // grows with each edit of a sent or approved estimate

	SentAt     *time.Time
	PaidAt     *time.Time
	RefundedAt *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time

	Customer    *Customer          `gorm:"foreignKey:CustomerID"`
	Location    *Location          `gorm:"foreignKey:LocationID"`
	ServiceType *ServiceType       `gorm:"foreignKey:ServiceTypeID"`
	LineItems   []CustomerLineItem `gorm:"foreignKey:ParentID"`
}

// TotalCents sums the line items.
func (d CustomerDocument) TotalCents() int64 {
	var sum int64
	for _, it := range d.LineItems {
		sum += it.TotalCents()
	}
	return sum
}
