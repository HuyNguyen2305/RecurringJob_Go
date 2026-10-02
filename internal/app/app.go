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
	jobRepo := repository.NewJobRepository(db)
	occRepo := repository.NewOccurrenceRepository(db)
	resolver := service.NewOccurrenceResolver(jobRepo)
	jobSvc := service.NewJobService(jobRepo, resolver)
	occSvc := service.NewOccurrenceService(jobRepo, occRepo, resolver)

	return router.New(defaultSchema,
		handler.NewJobHandler(jobSvc),
		handler.NewOccurrenceHandler(occSvc),
	)
}
