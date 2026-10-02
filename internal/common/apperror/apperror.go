// Package apperror defines the typed errors returned by services and mapped
// to HTTP responses by the error-handling middleware.
package apperror

import "net/http"

// AppError is an error with an HTTP status.
type AppError struct {
	Status  int
	Message string
}

func (e *AppError) Error() string { return e.Message }

// Validation is a 400 error.
func Validation(msg string) *AppError { return &AppError{Status: http.StatusBadRequest, Message: msg} }

// NotFound is a 404 error.
func NotFound(msg string) *AppError { return &AppError{Status: http.StatusNotFound, Message: msg} }

// Conflict is a 409 error.
func Conflict(msg string) *AppError { return &AppError{Status: http.StatusConflict, Message: msg} }
