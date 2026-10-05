package dto

import "recurringjob/internal/model"

// CreateCustomerRequest is the POST /customers body.
type CreateCustomerRequest struct {
	Name  string `json:"name" binding:"required" example:"Ada Lovelace"`
	Email string `json:"email" example:"ada@example.com"`
	Phone string `json:"phone" example:"555-0100"`
}

// UpdateCustomerRequest is the PATCH /customers/{id} body; omitted fields stay
// unchanged and an empty email or phone clears it.
type UpdateCustomerRequest struct {
	Name  *string `json:"name"`
	Email *string `json:"email"`
	Phone *string `json:"phone"`
}

// CustomerResponse is a customer as returned by the API.
type CustomerResponse struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Email *string `json:"email"`
	Phone *string `json:"phone"`
}

func NewCustomerResponse(c *model.Customer) CustomerResponse {
	return CustomerResponse{ID: c.ID, Name: c.Name, Email: c.Email, Phone: c.Phone}
}

// NewCustomerResponses converts a list.
func NewCustomerResponses(in []model.Customer) []CustomerResponse {
	out := make([]CustomerResponse, 0, len(in))
	for i := range in {
		out = append(out, NewCustomerResponse(&in[i]))
	}
	return out
}

// CreateLocationRequest is the POST /customers/{id}/locations body.
type CreateLocationRequest struct {
	AddressLine1 string `json:"addressLine1" binding:"required" example:"1 Main Street"`
	City         string `json:"city" example:"Springfield"`
	State        string `json:"state" example:"IL"`
	Zip          string `json:"zip" example:"62701"`
}

// LocationResponse is a service address as returned by the API.
type LocationResponse struct {
	ID           string  `json:"id"`
	CustomerID   string  `json:"customerId"`
	AddressLine1 string  `json:"addressLine1"`
	City         *string `json:"city"`
	State        *string `json:"state"`
	Zip          *string `json:"zip"`
}

func NewLocationResponse(l *model.Location) LocationResponse {
	return LocationResponse{ID: l.ID, CustomerID: l.CustomerID, AddressLine1: l.AddressLine1, City: l.City, State: l.State, Zip: l.Zip}
}

// NewLocationResponses converts a list.
func NewLocationResponses(in []model.Location) []LocationResponse {
	out := make([]LocationResponse, 0, len(in))
	for i := range in {
		out = append(out, NewLocationResponse(&in[i]))
	}
	return out
}

// CreateServiceTypeRequest is the POST /service-types body.
type CreateServiceTypeRequest struct {
	Name        string `json:"name" binding:"required" example:"Window cleaning"`
	Description string `json:"description"`
}

// ServiceTypeResponse is a service type as returned by the API.
type ServiceTypeResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

func NewServiceTypeResponse(s *model.ServiceType) ServiceTypeResponse {
	return ServiceTypeResponse{ID: s.ID, Name: s.Name, Description: s.Description}
}

// NewServiceTypeResponses converts a list.
func NewServiceTypeResponses(in []model.ServiceType) []ServiceTypeResponse {
	out := make([]ServiceTypeResponse, 0, len(in))
	for i := range in {
		out = append(out, NewServiceTypeResponse(&in[i]))
	}
	return out
}
