package model

import "time"

// RevisionLine is one line item as it was in a revision.
type RevisionLine struct {
	Description    string `json:"description"`
	Quantity       int    `json:"quantity"`
	UnitPriceCents int64  `json:"unitPriceCents"`
}

// RevisionContent is what an estimate said before an edit.
type RevisionContent struct {
	Notes         string         `json:"notes"`
	CustomerID    string         `json:"customerId"`
	LocationID    string         `json:"locationId"`
	ServiceTypeID string         `json:"serviceTypeId"`
	LineItems     []RevisionLine `json:"lineItems"`
}

// TotalCents sums the lines.
func (c RevisionContent) TotalCents() int64 {
	var sum int64
	for _, l := range c.LineItems {
		sum += int64(l.Quantity) * l.UnitPriceCents
	}
	return sum
}

// DocumentRevision is one row of customer_document_revisions: the content
// the document had as revision Revision, kept when an edit replaced it.
// CreatedAt is when it was replaced.
type DocumentRevision struct {
	ID         string          `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	DocumentID string          `gorm:"type:uuid;not null"`
	Revision   int             `gorm:"not null"`
	Content    RevisionContent `gorm:"type:jsonb;serializer:json;not null"`
	CreatedAt  time.Time
}

// TableName is the table the revisions live in.
func (DocumentRevision) TableName() string { return "customer_document_revisions" }
