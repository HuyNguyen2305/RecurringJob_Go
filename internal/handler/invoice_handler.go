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

// InvoiceService is what the invoice handler needs from the service layer.
type InvoiceService interface {
	DocumentService
	Create(ctx context.Context, jobID string, date time.Time, in service.DocumentInput) (*model.CustomerDocument, error)
}

// InvoiceHandler serves /invoices; the endpoints shared with estimates
// (Get, List, Update, Status) come from the embedded documentHandler.
type InvoiceHandler struct {
	documentHandler
	invoices InvoiceService
}

func NewInvoiceHandler(invoices InvoiceService) *InvoiceHandler {
	return &InvoiceHandler{documentHandler: documentHandler{docs: invoices}, invoices: invoices}
}

// Create godoc
// @Summary  Create a draft invoice for one occurrence of a job
// @Tags     invoices
// @Accept   json
// @Produce  json
// @Param    id   path string true "job id"
// @Param    date path string true "occurrence date, YYYY-MM-DD"
// @Param    body body dto.CreateInvoiceRequest true "invoice"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Failure  409 {object} map[string]any
// @Router   /jobs/{id}/occurrences/{date}/invoice [post]
func (h *InvoiceHandler) Create(c *gin.Context) {
	date, err := parseDate("date", c.Param("date"))
	if err != nil {
		fail(c, err)
		return
	}
	var req dto.CreateInvoiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apperror.Validation(err.Error()))
		return
	}
	doc, err := h.invoices.Create(c.Request.Context(), c.Param("id"), date, toDocumentInput(req.Notes, req.LineItems))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "invoice created", dto.NewDocumentResponse(doc))
}
