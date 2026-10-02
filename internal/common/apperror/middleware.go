package apperror

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ErrorHandler is the single place errors become HTTP responses. Handlers
// call c.Error(err) and return; an *AppError keeps its status, anything
// else is a 500 whose detail is logged, not returned.
func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if len(c.Errors) == 0 {
			return
		}
		err := c.Errors.Last().Err
		status, msg := http.StatusInternalServerError, "internal server error"
		var ae *AppError
		if errors.As(err, &ae) {
			status, msg = ae.Status, ae.Message
		} else {
			log.Printf("unhandled error: %v", err)
		}
		c.AbortWithStatusJSON(status, gin.H{"success": false, "message": msg})
	}
}
