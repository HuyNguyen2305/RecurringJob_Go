// Package handler is the HTTP layer: it parses requests, calls services and
// writes the success envelope. Errors go to the error middleware.
package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
)

func ok(c *gin.Context, message string, data any) {
	c.JSON(http.StatusOK, gin.H{"success": true, "message": message, "data": data})
}

func fail(c *gin.Context, err error) {
	_ = c.Error(err)
	c.Abort()
}

// parseDate parses a YYYY-MM-DD value; name is used in the error message.
func parseDate(name, value string) (time.Time, error) {
	d, err := civil.Parse(value)
	if err != nil {
		return time.Time{}, apperror.Validation(name + " must be a YYYY-MM-DD date")
	}
	return d, nil
}

// optionalDate parses a query parameter that may be absent (zero time).
func optionalDate(c *gin.Context, name string) (time.Time, error) {
	v := c.Query(name)
	if v == "" {
		return time.Time{}, nil
	}
	return parseDate(name, v)
}

// optionalInt parses an integer query parameter that may be absent (0).
func optionalInt(c *gin.Context, name string) (int, error) {
	v := c.Query(name)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, apperror.Validation(name + " must be an integer")
	}
	return n, nil
}
