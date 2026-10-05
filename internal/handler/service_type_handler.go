package handler

import (
	"context"

	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/dto"
	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

// ServiceTypeService is what the service type handler needs from the service layer.
type ServiceTypeService interface {
	CreateServiceType(ctx context.Context, in service.ServiceTypeInput) (*model.ServiceType, error)
	ListServiceTypes(ctx context.Context) ([]model.ServiceType, error)
}

type ServiceTypeHandler struct {
	types ServiceTypeService
}

func NewServiceTypeHandler(types ServiceTypeService) *ServiceTypeHandler {
	return &ServiceTypeHandler{types: types}
}

// Create godoc
// @Summary  Create a service type
// @Tags     service-types
// @Accept   json
// @Produce  json
// @Param    body body dto.CreateServiceTypeRequest true "service type"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Router   /service-types [post]
func (h *ServiceTypeHandler) Create(c *gin.Context) {
	var req dto.CreateServiceTypeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apperror.Validation(err.Error()))
		return
	}
	t, err := h.types.CreateServiceType(c.Request.Context(), service.ServiceTypeInput{Name: req.Name, Description: req.Description})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "service type created", dto.NewServiceTypeResponse(t))
}

// List godoc
// @Summary  List service types by name
// @Tags     service-types
// @Produce  json
// @Success  200 {object} map[string]any
// @Router   /service-types [get]
func (h *ServiceTypeHandler) List(c *gin.Context) {
	list, err := h.types.ListServiceTypes(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "service types", dto.NewServiceTypeResponses(list))
}
