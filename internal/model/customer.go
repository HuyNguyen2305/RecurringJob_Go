package model

import "time"

// Customer is one row of the customers table: who is billed and serviced.
type Customer struct {
	ID        string  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Name      string  `gorm:"not null"`
	Email     *string // optional
	Phone     *string // optional
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Location is one row of the locations table: a service address of a customer.
type Location struct {
	ID           string  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	CustomerID   string  `gorm:"type:uuid;not null"`
	AddressLine1 string  `gorm:"column:address_line1;not null"`
	City         *string // optional
	State        *string // optional
	Zip          *string // optional
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ServiceType is one row of the service_types table: a kind of work offered.
type ServiceType struct {
	ID          string  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Name        string  `gorm:"not null"`
	Description *string // optional
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
