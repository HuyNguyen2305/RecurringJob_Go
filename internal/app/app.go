// Package app wires the application together: repositories -> services ->
// handlers -> router. cmd/api calls it, and end-to-end tests use the same
// wiring.
package app

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"recurringjob/internal/handler"
	"recurringjob/internal/repository"
	"recurringjob/internal/router"
	"recurringjob/internal/service"
)

// NewServer builds the HTTP engine. defaultSchema is the tenant schema used
// when a request carries no X-Tenant-Schema header.
func NewServer(db *gorm.DB, defaultSchema string) *gin.Engine {
	customerRepo := repository.NewCustomerRepository(db)
	locationRepo := repository.NewLocationRepository(db)
	serviceTypeRepo := repository.NewServiceTypeRepository(db)
	jobRepo := repository.NewJobRepository(db)
	occRepo := repository.NewOccurrenceRepository(db)
	estimateRepo := repository.NewEstimateRepository(db)
	invoiceRepo := repository.NewInvoiceRepository(db)

	refs := service.NewReferences(customerRepo, locationRepo, serviceTypeRepo)
	customerSvc := service.NewCustomerService(customerRepo, locationRepo)
	serviceTypeSvc := service.NewServiceTypeService(serviceTypeRepo)
	resolver := service.NewOccurrenceResolver(jobRepo)
	jobSvc := service.NewJobService(jobRepo, resolver, refs)
	occSvc := service.NewOccurrenceService(jobRepo, occRepo, resolver)
	invoiceSvc := service.NewInvoiceService(invoiceRepo, jobRepo, occSvc)
	estimateSvc := service.NewEstimateService(estimateRepo, jobSvc, refs).WithInvoices(invoiceSvc)
	// The invoice service needs the occurrence service (availability) and the
	// occurrence service needs the invoice service (voiding), so one is set late.
	occSvc.WithInvoices(invoiceSvc)

	return router.New(defaultSchema, router.Handlers{
		Customers:    handler.NewCustomerHandler(customerSvc),
		ServiceTypes: handler.NewServiceTypeHandler(serviceTypeSvc),
		Jobs:         handler.NewJobHandler(jobSvc),
		Occurrences:  handler.NewOccurrenceHandler(occSvc),
		Estimates:    handler.NewEstimateHandler(estimateSvc),
		Invoices:     handler.NewInvoiceHandler(invoiceSvc),
	})
}
