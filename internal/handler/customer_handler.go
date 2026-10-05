package handler

import (
	"context"

	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/dto"
	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

// CustomerService is what the customer handler needs from the service layer.
type CustomerService interface {
	CreateCustomer(ctx context.Context, in service.CustomerInput) (*model.Customer, error)
	GetCustomer(ctx context.Context, id string) (*model.Customer, error)
	ListCustomers(ctx context.Context, limit, offset int) ([]model.Customer, error)
	UpdateCustomer(ctx context.Context, id string, p service.CustomerPatch) (*model.Customer, error)
	CreateLocation(ctx context.Context, customerID string, in service.LocationInput) (*model.Location, error)
	ListLocations(ctx context.Context, customerID string) ([]model.Location, error)
}

type CustomerHandler struct {
	customers CustomerService
}

func NewCustomerHandler(customers CustomerService) *CustomerHandler {
	return &CustomerHandler{customers: customers}
}

// Create godoc
// @Summary  Create a customer
// @Tags     customers
// @Accept   json
// @Produce  json
// @Param    body body dto.CreateCustomerRequest true "customer"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Router   /customers [post]
func (h *CustomerHandler) Create(c *gin.Context) {
	var req dto.CreateCustomerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apperror.Validation(err.Error()))
		return
	}
	cust, err := h.customers.CreateCustomer(c.Request.Context(), service.CustomerInput{Name: req.Name, Email: req.Email, Phone: req.Phone})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "customer created", dto.NewCustomerResponse(cust))
}

// Get godoc
// @Summary  Get one customer
// @Tags     customers
// @Produce  json
// @Param    id path string true "customer id"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Router   /customers/{id} [get]
func (h *CustomerHandler) Get(c *gin.Context) {
	cust, err := h.customers.GetCustomer(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "customer", dto.NewCustomerResponse(cust))
}

// List godoc
// @Summary  List customers by name
// @Tags     customers
// @Produce  json
// @Param    limit  query int false "1..200 (default 50)"
// @Param    offset query int false "rows to skip (default 0)"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Router   /customers [get]
func (h *CustomerHandler) List(c *gin.Context) {
	limit, err := optionalInt(c, "limit")
	if err != nil {
		fail(c, err)
		return
	}
	offset, err := optionalInt(c, "offset")
	if err != nil {
		fail(c, err)
		return
	}
	list, err := h.customers.ListCustomers(c.Request.Context(), limit, offset)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "customers", dto.NewCustomerResponses(list))
}

// Update godoc
// @Summary  Change a customer's name, email or phone
// @Tags     customers
// @Accept   json
// @Produce  json
// @Param    id   path string true "customer id"
// @Param    body body dto.UpdateCustomerRequest true "fields to change"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Router   /customers/{id} [patch]
func (h *CustomerHandler) Update(c *gin.Context) {
	var req dto.UpdateCustomerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apperror.Validation(err.Error()))
		return
	}
	cust, err := h.customers.UpdateCustomer(c.Request.Context(), c.Param("id"), service.CustomerPatch{Name: req.Name, Email: req.Email, Phone: req.Phone})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "customer updated", dto.NewCustomerResponse(cust))
}

// CreateLocation godoc
// @Summary  Add a service address to a customer
// @Tags     customers
// @Accept   json
// @Produce  json
// @Param    id   path string true "customer id"
// @Param    body body dto.CreateLocationRequest true "location"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Router   /customers/{id}/locations [post]
func (h *CustomerHandler) CreateLocation(c *gin.Context) {
	var req dto.CreateLocationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apperror.Validation(err.Error()))
		return
	}
	loc, err := h.customers.CreateLocation(c.Request.Context(), c.Param("id"), service.LocationInput{
		AddressLine1: req.AddressLine1, City: req.City, State: req.State, Zip: req.Zip,
	})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "location created", dto.NewLocationResponse(loc))
}

// ListLocations godoc
// @Summary  List a customer's service addresses
// @Tags     customers
// @Produce  json
// @Param    id path string true "customer id"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Router   /customers/{id}/locations [get]
func (h *CustomerHandler) ListLocations(c *gin.Context) {
	list, err := h.customers.ListLocations(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "locations", dto.NewLocationResponses(list))
}
