package model

import "time"

// CustomerLineItem is one row of the customer_line_items table. It belongs to
// a CustomerDocument (an estimate or an invoice) through ParentID; the
// parent's Type says which. Money is in integer cents.
type CustomerLineItem struct {
	ID             string `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	ParentID       string `gorm:"type:uuid;not null"`
	Position       int    `gorm:"not null"`
	Description    string `gorm:"not null"`
	Quantity       int    `gorm:"not null"`
	UnitPriceCents int64  `gorm:"not null"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// TotalCents is quantity times unit price.
func (i CustomerLineItem) TotalCents() int64 {
	return int64(i.Quantity) * i.UnitPriceCents
}
