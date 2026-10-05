// Package router declares the HTTP routes. It is deliberately thin.
package router

import (
	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/auth"
	"recurringjob/internal/handler"
)

// Handlers are the HTTP handlers the router serves.
type Handlers struct {
	Customers    *handler.CustomerHandler
	ServiceTypes *handler.ServiceTypeHandler
	Jobs         *handler.JobHandler
	Occurrences  *handler.OccurrenceHandler
	Estimates    *handler.EstimateHandler
	Invoices     *handler.InvoiceHandler
}

// New builds the Gin engine with the error and tenant middlewares and the
// customer, service type, job, estimate and invoice routes.
func New(defaultSchema string, h Handlers) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), apperror.ErrorHandler(), auth.TenantSchema(defaultSchema))

	c := r.Group("/customers")
	c.POST("", h.Customers.Create)
	c.GET("", h.Customers.List)
	c.GET("/:id", h.Customers.Get)
	c.PATCH("/:id", h.Customers.Update)
	c.POST("/:id/locations", h.Customers.CreateLocation)
	c.GET("/:id/locations", h.Customers.ListLocations)

	st := r.Group("/service-types")
	st.POST("", h.ServiceTypes.Create)
	st.GET("", h.ServiceTypes.List)

	g := r.Group("/jobs")
	g.POST("", h.Jobs.Create)
	g.GET("/:id/occurrences", h.Jobs.Occurrences)
	g.GET("/:id/schedule", h.Occurrences.Schedule)
	g.PATCH("/:id/occurrences/:date", h.Occurrences.Update)
	g.POST("/:id/occurrences/:date/invoice", h.Invoices.Create)

	e := r.Group("/estimates")
	e.POST("", h.Estimates.Create)
	e.GET("", h.Estimates.List)
	e.GET("/:id", h.Estimates.Get)
	e.PATCH("/:id", h.Estimates.Update)
	e.PATCH("/:id/status", h.Estimates.Status)
	e.POST("/:id/approve", h.Estimates.Approve)

	i := r.Group("/invoices")
	i.GET("", h.Invoices.List)
	i.GET("/:id", h.Invoices.Get)
	i.PATCH("/:id", h.Invoices.Update)
	i.PATCH("/:id/status", h.Invoices.Status)
	return r
}
