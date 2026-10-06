package handler

import (
	"context"

	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/dto"
	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

// EstimateService is what the estimate handler needs from the service layer.
type EstimateService interface {
	DocumentService
	Create(ctx context.Context, in service.EstimateInput) (*model.CustomerDocument, error)
	Approve(ctx context.Context, id string, in service.ApproveEstimateInput) (*model.CustomerDocument, error)
	Reopen(ctx context.Context, id string) (*model.CustomerDocument, error)
	Revisions(ctx context.Context, id string) ([]model.DocumentRevision, error)
}

// EstimateHandler serves /estimates; the endpoints shared with invoices
// (Get, List, Update, Status) come from the embedded documentHandler.
type EstimateHandler struct {
	documentHandler
	estimates EstimateService
}

func NewEstimateHandler(estimates EstimateService) *EstimateHandler {
	return &EstimateHandler{documentHandler: documentHandler{docs: estimates}, estimates: estimates}
}

// Create godoc
// @Summary  Create a draft estimate
// @Tags     estimates
// @Accept   json
// @Produce  json
// @Param    body body dto.CreateEstimateRequest true "estimate"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Router   /estimates [post]
func (h *EstimateHandler) Create(c *gin.Context) {
	var req dto.CreateEstimateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apperror.Validation(err.Error()))
		return
	}
	doc, err := h.estimates.Create(c.Request.Context(), service.EstimateInput{
		CustomerID: req.CustomerID, LocationID: req.LocationID, ServiceTypeID: req.ServiceTypeID,
		DocumentInput: toDocumentInput(req.Notes, req.LineItems),
	})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "estimate created", dto.NewDocumentResponse(doc))
}

// Approve godoc
// @Summary  Approve an estimate and create its job
// @Tags     estimates
// @Accept   json
// @Produce  json
// @Param    id   path string true "estimate id"
// @Param    body body dto.ApproveEstimateRequest true "the job to create"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Failure  409 {object} map[string]any
// @Router   /estimates/{id}/approve [post]
func (h *EstimateHandler) Approve(c *gin.Context) {
	var req dto.ApproveEstimateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apperror.Validation(err.Error()))
		return
	}
	date, err := parseDate("date", req.Date)
	if err != nil {
		fail(c, err)
		return
	}
	doc, err := h.estimates.Approve(c.Request.Context(), c.Param("id"), service.ApproveEstimateInput{
		Date: date, StartTime: req.StartTime, LengthMinutes: req.LengthMinutes, Recurrence: req.Recurrence,
	})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "estimate approved", dto.NewDocumentResponse(doc))
}

// Reopen godoc
// @Summary  Undo an approval: delete the job and return the estimate to sent
// @Tags     estimates
// @Produce  json
// @Param    id path string true "estimate id"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Failure  409 {object} map[string]any
// @Router   /estimates/{id}/reopen [post]
func (h *EstimateHandler) Reopen(c *gin.Context) {
	doc, err := h.estimates.Reopen(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "estimate reopened", dto.NewDocumentResponse(doc))
}

// Revisions godoc
// @Summary  What the estimate said before each edit, newest first
// @Tags     estimates
// @Produce  json
// @Param    id path string true "estimate id"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Router   /estimates/{id}/revisions [get]
func (h *EstimateHandler) Revisions(c *gin.Context) {
	revs, err := h.estimates.Revisions(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "revisions", dto.NewRevisionResponses(revs))
}
