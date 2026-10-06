package handler

import (
	"context"

	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/dto"
	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

// DocumentService is what the shared document endpoints need from the service
// layer; both the estimate and the invoice service provide it.
type DocumentService interface {
	Get(ctx context.Context, id string) (*model.CustomerDocument, error)
	List(ctx context.Context, q service.DocumentListQuery) ([]model.CustomerDocument, error)
	Update(ctx context.Context, id string, p service.DocumentPatch) (*model.CustomerDocument, error)
	ChangeStatus(ctx context.Context, id, to string) (*model.CustomerDocument, error)
	Delete(ctx context.Context, id string) error
}

// documentHandler serves the endpoints estimates and invoices share. The
// estimate and invoice handlers embed it, so the same code backs both URLs.
type documentHandler struct {
	docs DocumentService
}

func toDocumentInput(notes string, items []dto.LineItemRequest) service.DocumentInput {
	return service.DocumentInput{Notes: notes, LineItems: toLineItemInputs(items)}
}

func toLineItemInputs(in []dto.LineItemRequest) []service.LineItemInput {
	out := make([]service.LineItemInput, 0, len(in))
	for _, it := range in {
		out = append(out, service.LineItemInput{Description: it.Description, Quantity: it.Quantity, UnitPriceCents: it.UnitPriceCents})
	}
	return out
}

// Get godoc
// @Summary  Get one estimate or invoice with its line items
// @Tags     estimates, invoices
// @Produce  json
// @Param    id path string true "document id"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Router   /estimates/{id} [get]
// @Router   /invoices/{id} [get]
func (h *documentHandler) Get(c *gin.Context) {
	doc, err := h.docs.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "document", dto.NewDocumentResponse(doc))
}

// List godoc
// @Summary  List estimates or invoices, newest first
// @Tags     estimates, invoices
// @Produce  json
// @Param    status query string false "filter by status"
// @Param    customerId query string false "filter by customer"
// @Param    locationId query string false "filter by location"
// @Param    jobId query string false "filter by job"
// @Param    q query string false "customer name or document number contains"
// @Param    occurrenceFrom query string false "invoices only: occurrence date from, YYYY-MM-DD"
// @Param    occurrenceTo query string false "invoices only: occurrence date to, YYYY-MM-DD"
// @Param    limit  query int    false "1..200 (default 50)"
// @Param    offset query int    false "rows to skip (default 0)"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Router   /estimates [get]
// @Router   /invoices [get]
func (h *documentHandler) List(c *gin.Context) {
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
	docs, err := h.docs.List(c.Request.Context(), service.DocumentListQuery{
		Status: c.Query("status"), CustomerID: c.Query("customerId"), LocationID: c.Query("locationId"), JobID: c.Query("jobId"),
		Q: c.Query("q"), OccurrenceFrom: from, OccurrenceTo: to, Limit: limit, Offset: offset,
	})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "documents", dto.NewDocumentResponses(docs))
}

// Delete godoc
// @Summary  Delete a draft estimate or invoice
// @Tags     estimates, invoices
// @Produce  json
// @Param    id path string true "document id"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Failure  409 {object} map[string]any
// @Router   /estimates/{id} [delete]
// @Router   /invoices/{id} [delete]
func (h *documentHandler) Delete(c *gin.Context) {
	if err := h.docs.Delete(c.Request.Context(), c.Param("id")); err != nil {
		fail(c, err)
		return
	}
	ok(c, "document deleted", gin.H{"id": c.Param("id")})
}

// Update godoc
// @Summary  Edit a draft estimate or invoice (a given lineItems list replaces all lines)
// @Tags     estimates, invoices
// @Accept   json
// @Produce  json
// @Param    id   path string true "document id"
// @Param    body body dto.UpdateDocumentRequest true "fields to change"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Failure  409 {object} map[string]any
// @Router   /estimates/{id} [patch]
// @Router   /invoices/{id} [patch]
func (h *documentHandler) Update(c *gin.Context) {
	var req dto.UpdateDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apperror.Validation(err.Error()))
		return
	}
	patch := service.DocumentPatch{
		Notes: req.Notes, CustomerID: req.CustomerID, LocationID: req.LocationID, ServiceTypeID: req.ServiceTypeID,
	}
	if req.LineItems != nil {
		items := toLineItemInputs(*req.LineItems)
		patch.LineItems = &items
	}
	doc, err := h.docs.Update(c.Request.Context(), c.Param("id"), patch)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "document updated", dto.NewDocumentResponse(doc))
}

// Status godoc
// @Summary  Change the status of an estimate or invoice
// @Tags     estimates, invoices
// @Accept   json
// @Produce  json
// @Param    id   path string true "document id"
// @Param    body body dto.DocumentStatusRequest true "new status"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Failure  409 {object} map[string]any
// @Router   /estimates/{id}/status [patch]
// @Router   /invoices/{id}/status [patch]
func (h *documentHandler) Status(c *gin.Context) {
	var req dto.DocumentStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apperror.Validation(err.Error()))
		return
	}
	doc, err := h.docs.ChangeStatus(c.Request.Context(), c.Param("id"), req.Status)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "status updated", dto.NewDocumentResponse(doc))
}
