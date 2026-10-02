package apperror_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
)

func TestErrorHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{"validation", apperror.Validation("bad input"), 400, "bad input"},
		{"not found", apperror.NotFound("no job"), 404, "no job"},
		{"conflict", apperror.Conflict("taken"), 409, "taken"},
		{"wrapped AppError", fmt.Errorf("ctx: %w", apperror.Conflict("taken")), 409, "taken"},
		{"unknown hides detail", errors.New("pq: secret detail"), 500, "internal server error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.Use(apperror.ErrorHandler())
			r.GET("/x", func(c *gin.Context) { _ = c.Error(tt.err); c.Abort() })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
			if w.Code != tt.wantStatus {
				t.Fatalf("status %d, want %d", w.Code, tt.wantStatus)
			}
			body := w.Body.String()
			if !strings.Contains(body, tt.wantBody) || !strings.Contains(body, `"success":false`) || strings.Contains(body, "secret") {
				t.Fatalf("body %s", body)
			}
		})
	}

	t.Run("no error leaves the response alone", func(t *testing.T) {
		r := gin.New()
		r.Use(apperror.ErrorHandler())
		r.GET("/x", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
		if w.Code != 200 {
			t.Fatalf("status %d", w.Code)
		}
	})
}
