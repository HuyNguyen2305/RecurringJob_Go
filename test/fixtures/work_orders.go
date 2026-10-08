package fixtures

import (
	"time"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
)

// WorkOrder is a draft work order with two open tasks for the occurrence of
// jobID on date, for the given customer, location and service type (the job's).
// The job snapshot is a minimal one.
func WorkOrder(jobID string, date time.Time, customerID, locationID, serviceTypeID string) *model.WorkOrder {
	return &model.WorkOrder{
		Status:         "draft",
		JobID:          jobID,
		OccurrenceDate: civil.Truncate(date),
		JobSnapshot:    &model.JobSnapshot{ID: jobID, CustomerID: customerID, LocationID: locationID, ServiceTypeID: serviceTypeID},
		CustomerID:     customerID,
		LocationID:     locationID,
		ServiceTypeID:  serviceTypeID,
		Tasks:          []model.WorkOrderTask{{Position: 0, Description: "Clean windows"}, {Position: 1, Description: "Mop floor"}},
	}
}
