// Package router declares the HTTP routes. It is deliberately thin.
package router

import (
	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/auth"
	"recurringjob/internal/handler"
)

// New builds the Gin engine with the error and tenant middlewares and the
// job routes.
func New(defaultSchema string, jobs *handler.JobHandler, occs *handler.OccurrenceHandler) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), apperror.ErrorHandler(), auth.TenantSchema(defaultSchema))

	g := r.Group("/jobs")
	g.POST("", jobs.Create)
	g.GET("/:id/occurrences", jobs.Occurrences)
	g.GET("/:id/schedule", occs.Schedule)
	g.PATCH("/:id/occurrences/:date", occs.Update)
	return r
}
