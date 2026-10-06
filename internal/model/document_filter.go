package model

import "time"

// DocumentFilter narrows a list of estimates or invoices. Empty fields do not
// filter. Q matches the customer's name or the document number.
type DocumentFilter struct {
	Status         string
	CustomerID     string
	LocationID     string
	JobID          string
	Q              string
	OccurrenceFrom time.Time // invoices only; zero = no lower bound
	OccurrenceTo   time.Time // invoices only; zero = no upper bound
}
