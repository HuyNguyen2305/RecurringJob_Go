package model

import "time"

// WorkOrder is one row of work_orders: the task sheet for one occurrence of a
// job. Customer, Location and ServiceType are read-only associations, loaded
// with the work order and never written through it.
type WorkOrder struct {
	ID             string       `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Number         string       `gorm:"not null;default:(-)"` // WO-000123, given by the database on insert
	Status         string       `gorm:"not null"`
	JobID          string       `gorm:"type:uuid;not null"`
	OccurrenceDate time.Time    `gorm:"type:date;not null"`
	JobSnapshot    *JobSnapshot `gorm:"type:jsonb;serializer:json;not null"`
	CustomerID     string       `gorm:"type:uuid;not null"`
	LocationID     string       `gorm:"type:uuid;not null"`
	ServiceTypeID  string       `gorm:"type:uuid;not null"`
	Notes          string       `gorm:"not null"`
	CompletedAt    *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time

	Customer    *Customer       `gorm:"foreignKey:CustomerID"`
	Location    *Location       `gorm:"foreignKey:LocationID"`
	ServiceType *ServiceType    `gorm:"foreignKey:ServiceTypeID"`
	Tasks       []WorkOrderTask `gorm:"foreignKey:WorkOrderID"`
}

// WorkOrderTask is one row of work_order_tasks: a step to do during the visit.
type WorkOrderTask struct {
	ID          string `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	WorkOrderID string `gorm:"type:uuid;not null"`
	Position    int    `gorm:"not null"`
	Description string `gorm:"not null"`
	Done        bool   `gorm:"not null"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// WorkOrderFilter narrows a work-order list; empty fields match everything.
type WorkOrderFilter struct {
	Status         string
	CustomerID     string
	LocationID     string
	JobID          string
	Q              string // customer name or work order number contains
	OccurrenceFrom time.Time
	OccurrenceTo   time.Time
}
