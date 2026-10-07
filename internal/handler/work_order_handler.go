package handler

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/dto"
	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

// WorkOrderService is what the work order handler needs from the service layer.
type WorkOrderService interface {
	Create(ctx context.Context, jobID string, date time.Time, in service.WorkOrderInput) (*model.WorkOrder, error)
	Get(ctx context.Context, id string) (*model.WorkOrder, error)
	List(ctx context.Context, q service.WorkOrderListQuery) ([]model.WorkOrder, error)
	Update(ctx context.Context, id string, p service.WorkOrderPatch) (*model.WorkOrder, error)
	ChangeStatus(ctx context.Context, id, to string) (*model.WorkOrder, error)
	Delete(ctx context.Context, id string) error
}

// WorkOrderHandler serves /work-orders and the per-occurrence creation route.
type WorkOrderHandler struct {
	orders WorkOrderService
}

func NewWorkOrderHandler(orders WorkOrderService) *WorkOrderHandler {
	return &WorkOrderHandler{orders: orders}
}

func toTaskInputs(in []dto.TaskRequest) []service.TaskInput {
	out := make([]service.TaskInput, 0, len(in))
	for _, t := range in {
		out = append(out, service.TaskInput{Description: t.Description, Done: t.Done})
	}
	return out
}

// Create godoc
// @Summary  Create a draft work order for one occurrence of a job
// @Tags     work-orders
// @Accept   json
// @Produce  json
// @Param    id   path string true "job id"
// @Param    date path string true "occurrence date, YYYY-MM-DD"
// @Param    body body dto.CreateWorkOrderRequest true "work order"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Failure  409 {object} map[string]any
// @Router   /jobs/{id}/occurrences/{date}/work-order [post]
func (h *WorkOrderHandler) Create(c *gin.Context) {
	date, err := parseDate("date", c.Param("date"))
	if err != nil {
		fail(c, err)
		return
	}
	var req dto.CreateWorkOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apperror.Validation(err.Error()))
		return
	}
	wo, err := h.orders.Create(c.Request.Context(), c.Param("id"), date, service.WorkOrderInput{Notes: req.Notes, Tasks: toTaskInputs(req.Tasks)})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "work order created", dto.NewWorkOrderResponse(wo))
}

// Get godoc
// @Summary  Get one work order with its tasks
// @Tags     work-orders
// @Produce  json
// @Param    id path string true "work order id"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Router   /work-orders/{id} [get]
func (h *WorkOrderHandler) Get(c *gin.Context) {
	wo, err := h.orders.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "work order", dto.NewWorkOrderResponse(wo))
}

// List godoc
// @Summary  List work orders, newest first
// @Tags     work-orders
// @Produce  json
// @Param    status query string false "filter by status"
// @Param    customerId query string false "filter by customer"
// @Param    locationId query string false "filter by location"
// @Param    jobId query string false "filter by job"
// @Param    q query string false "customer name or work order number contains"
// @Param    occurrenceFrom query string false "occurrence date from, YYYY-MM-DD"
// @Param    occurrenceTo query string false "occurrence date to, YYYY-MM-DD"
// @Param    limit  query int    false "1..200 (default 50)"
// @Param    offset query int    false "rows to skip (default 0)"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Router   /work-orders [get]
func (h *WorkOrderHandler) List(c *gin.Context) {
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
	from, err := optionalDate(c, "occurrenceFrom")
	if err != nil {
		fail(c, err)
		return
	}
	to, err := optionalDate(c, "occurrenceTo")
	if err != nil {
		fail(c, err)
		return
	}
	wos, err := h.orders.List(c.Request.Context(), service.WorkOrderListQuery{
		Status: c.Query("status"), CustomerID: c.Query("customerId"), LocationID: c.Query("locationId"), JobID: c.Query("jobId"),
		Q: c.Query("q"), OccurrenceFrom: from, OccurrenceTo: to, Limit: limit, Offset: offset,
	})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "work orders", dto.NewWorkOrderResponses(wos))
}

// Update godoc
// @Summary  Edit a work order (a given tasks list replaces all tasks)
// @Tags     work-orders
// @Accept   json
// @Produce  json
// @Param    id   path string true "work order id"
// @Param    body body dto.UpdateWorkOrderRequest true "fields to change"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Failure  409 {object} map[string]any
// @Router   /work-orders/{id} [patch]
func (h *WorkOrderHandler) Update(c *gin.Context) {
	var req dto.UpdateWorkOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apperror.Validation(err.Error()))
		return
	}
	patch := service.WorkOrderPatch{Notes: req.Notes}
	if req.Tasks != nil {
		tasks := toTaskInputs(*req.Tasks)
		patch.Tasks = &tasks
	}
	wo, err := h.orders.Update(c.Request.Context(), c.Param("id"), patch)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "work order updated", dto.NewWorkOrderResponse(wo))
}

// Status godoc
// @Summary  Change the status of a work order
// @Tags     work-orders
// @Accept   json
// @Produce  json
// @Param    id   path string true "work order id"
// @Param    body body dto.WorkOrderStatusRequest true "new status"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Failure  409 {object} map[string]any
// @Router   /work-orders/{id}/status [patch]
func (h *WorkOrderHandler) Status(c *gin.Context) {
	var req dto.WorkOrderStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apperror.Validation(err.Error()))
		return
	}
	wo, err := h.orders.ChangeStatus(c.Request.Context(), c.Param("id"), req.Status)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "status updated", dto.NewWorkOrderResponse(wo))
}

// Delete godoc
// @Summary  Delete a draft work order
// @Tags     work-orders
// @Produce  json
// @Param    id path string true "work order id"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Failure  409 {object} map[string]any
// @Router   /work-orders/{id} [delete]
func (h *WorkOrderHandler) Delete(c *gin.Context) {
	if err := h.orders.Delete(c.Request.Context(), c.Param("id")); err != nil {
		fail(c, err)
		return
	}
	ok(c, "work order deleted", gin.H{"id": c.Param("id")})
}
