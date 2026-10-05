package service

import (
	"context"
	"net/mail"
	"strings"
	"unicode/utf8"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
)

// Limits match the column sizes of the customers and locations tables.
const (
	MaxNameLen         = 255
	MaxEmailLen        = 254
	MaxPhoneLen        = 50
	MaxAddressLen      = 255
	MaxCityStateLen    = 100
	MaxZipLen          = 20
	MaxDescriptionText = 2000

	DefaultCustomerLimit = 50
	MaxCustomerLimit     = 200
)

// CustomerStore is the customer storage the customer service needs.
type CustomerStore interface {
	Create(ctx context.Context, c *model.Customer) error
	Get(ctx context.Context, id string) (*model.Customer, error)
	List(ctx context.Context, limit, offset int) ([]model.Customer, error)
	// Update applies fields and returns the rows affected.
	Update(ctx context.Context, id string, fields map[string]any) (int64, error)
}

// LocationStore is the location storage the customer service needs.
type LocationStore interface {
	Create(ctx context.Context, l *model.Location) error
	Get(ctx context.Context, id string) (*model.Location, error)
	ListByCustomer(ctx context.Context, customerID string) ([]model.Location, error)
}

// CustomerInput creates a customer; Email and Phone are optional.
type CustomerInput struct {
	Name, Email, Phone string
}

// CustomerPatch edits a customer; nil fields stay unchanged and an empty
// Email or Phone clears it.
type CustomerPatch struct {
	Name, Email, Phone *string
}

// LocationInput creates a location; everything but the address is optional.
type LocationInput struct {
	AddressLine1, City, State, Zip string
}

// CustomerService holds customer and location business rules.
type CustomerService struct {
	customers CustomerStore
	locations LocationStore
}

func NewCustomerService(customers CustomerStore, locations LocationStore) *CustomerService {
	return &CustomerService{customers: customers, locations: locations}
}

// requiredText trims s and checks it is present and at most max characters.
func requiredText(field, s string, max int) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", apperror.Validation(field + " is required")
	}
	if utf8.RuneCountInString(s) > max {
		return "", bad("%s must be at most %d characters", field, max)
	}
	return s, nil
}

// optionalText trims s; empty becomes nil (stored as NULL).
func optionalText(field, s string, max int) (*string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(s) > max {
		return nil, bad("%s must be at most %d characters", field, max)
	}
	return &s, nil
}

func optionalEmail(s string) (*string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if len(s) > MaxEmailLen {
		return nil, bad("email must be at most %d characters", MaxEmailLen)
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s {
		return nil, apperror.Validation("email must be a valid email address")
	}
	return &s, nil
}

// CreateCustomer validates and saves a customer.
func (s *CustomerService) CreateCustomer(ctx context.Context, in CustomerInput) (*model.Customer, error) {
	name, err := requiredText("name", in.Name, MaxNameLen)
	if err != nil {
		return nil, err
	}
	email, err := optionalEmail(in.Email)
	if err != nil {
		return nil, err
	}
	phone, err := optionalText("phone", in.Phone, MaxPhoneLen)
	if err != nil {
		return nil, err
	}
	c := &model.Customer{Name: name, Email: email, Phone: phone}
	if err := s.customers.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// GetCustomer returns one customer.
func (s *CustomerService) GetCustomer(ctx context.Context, id string) (*model.Customer, error) {
	if err := ValidateID("customer id", id); err != nil {
		return nil, err
	}
	return s.customers.Get(ctx, id)
}

// ListCustomers returns customers by name.
func (s *CustomerService) ListCustomers(ctx context.Context, limit, offset int) ([]model.Customer, error) {
	if limit == 0 {
		limit = DefaultCustomerLimit
	}
	if limit < 1 || limit > MaxCustomerLimit {
		return nil, bad("limit must be between 1 and %d", MaxCustomerLimit)
	}
	if offset < 0 {
		return nil, apperror.Validation("offset cannot be negative")
	}
	return s.customers.List(ctx, limit, offset)
}

// UpdateCustomer changes the given fields of a customer.
func (s *CustomerService) UpdateCustomer(ctx context.Context, id string, p CustomerPatch) (*model.Customer, error) {
	if err := ValidateID("customer id", id); err != nil {
		return nil, err
	}
	fields := map[string]any{}
	if p.Name != nil {
		name, err := requiredText("name", *p.Name, MaxNameLen)
		if err != nil {
			return nil, err
		}
		fields["name"] = name
	}
	if p.Email != nil {
		email, err := optionalEmail(*p.Email)
		if err != nil {
			return nil, err
		}
		fields["email"] = email
	}
	if p.Phone != nil {
		phone, err := optionalText("phone", *p.Phone, MaxPhoneLen)
		if err != nil {
			return nil, err
		}
		fields["phone"] = phone
	}
	if len(fields) == 0 {
		return nil, apperror.Validation("nothing to update")
	}
	n, err := s.customers.Update(ctx, id, fields)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, apperror.NotFound("customer not found")
	}
	return s.customers.Get(ctx, id)
}

// CreateLocation adds a service address to an existing customer.
func (s *CustomerService) CreateLocation(ctx context.Context, customerID string, in LocationInput) (*model.Location, error) {
	if err := ValidateID("customer id", customerID); err != nil {
		return nil, err
	}
	address, err := requiredText("addressLine1", in.AddressLine1, MaxAddressLen)
	if err != nil {
		return nil, err
	}
	city, err := optionalText("city", in.City, MaxCityStateLen)
	if err != nil {
		return nil, err
	}
	state, err := optionalText("state", in.State, MaxCityStateLen)
	if err != nil {
		return nil, err
	}
	zip, err := optionalText("zip", in.Zip, MaxZipLen)
	if err != nil {
		return nil, err
	}
	if _, err := s.customers.Get(ctx, customerID); err != nil {
		return nil, err
	}
	l := &model.Location{CustomerID: customerID, AddressLine1: address, City: city, State: state, Zip: zip}
	if err := s.locations.Create(ctx, l); err != nil {
		return nil, err
	}
	return l, nil
}

// ListLocations returns a customer's locations.
func (s *CustomerService) ListLocations(ctx context.Context, customerID string) ([]model.Location, error) {
	if err := ValidateID("customer id", customerID); err != nil {
		return nil, err
	}
	if _, err := s.customers.Get(ctx, customerID); err != nil {
		return nil, err
	}
	return s.locations.ListByCustomer(ctx, customerID)
}
