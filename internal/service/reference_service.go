package service

import (
	"context"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
)

// Refs are the customer, location and service type a job or estimate points at.
type Refs struct {
	Customer    *model.Customer
	Location    *model.Location
	ServiceType *model.ServiceType
}

// ReferenceResolver turns three ids into the records they name, checking that
// they exist and belong together. JobService and EstimateService use it.
type ReferenceResolver interface {
	Resolve(ctx context.Context, customerID, locationID, serviceTypeID string) (*Refs, error)
}

// CustomerGetter, LocationGetter and ServiceTypeGetter are the lookups the
// resolver needs; the repositories satisfy them.
type CustomerGetter interface {
	Get(ctx context.Context, id string) (*model.Customer, error)
}

type LocationGetter interface {
	Get(ctx context.Context, id string) (*model.Location, error)
}

type ServiceTypeGetter interface {
	Get(ctx context.Context, id string) (*model.ServiceType, error)
}

// References is the ReferenceResolver backed by the three tables.
type References struct {
	customers    CustomerGetter
	locations    LocationGetter
	serviceTypes ServiceTypeGetter
}

func NewReferences(customers CustomerGetter, locations LocationGetter, serviceTypes ServiceTypeGetter) *References {
	return &References{customers: customers, locations: locations, serviceTypes: serviceTypes}
}

// Resolve returns the three records. A malformed id is a 400, a missing
// record a 404, and a location that belongs to another customer a 400.
func (r *References) Resolve(ctx context.Context, customerID, locationID, serviceTypeID string) (*Refs, error) {
	for _, id := range []struct{ name, value string }{
		{"customerId", customerID}, {"locationId", locationID}, {"serviceTypeId", serviceTypeID},
	} {
		if err := ValidateID(id.name, id.value); err != nil {
			return nil, err
		}
	}
	customer, err := r.customers.Get(ctx, customerID)
	if err != nil {
		return nil, err
	}
	location, err := r.locations.Get(ctx, locationID)
	if err != nil {
		return nil, err
	}
	if location.CustomerID != customer.ID {
		return nil, apperror.Validation("locationId does not belong to the customer")
	}
	serviceType, err := r.serviceTypes.Get(ctx, serviceTypeID)
	if err != nil {
		return nil, err
	}
	return &Refs{Customer: customer, Location: location, ServiceType: serviceType}, nil
}
