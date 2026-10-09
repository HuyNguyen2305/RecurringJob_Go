package service

import "recurringjob/internal/common/apperror"

const (
	StatusUnconfirmed      = "unconfirmed"
	StatusConfirmed        = "confirmed"
	StatusCompleted        = "completed"
	StatusCanceled         = "canceled"
	StatusTerminateService = "terminate_service"
	StatusRescheduled      = "rescheduled"
)

var (
	openStatuses  = []string{StatusUnconfirmed, StatusConfirmed}
	finalStatuses = []string{StatusCompleted, StatusCanceled, StatusRescheduled, StatusTerminateService}
)

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// IsKnownStatus reports whether s is any occurrence status.
func IsKnownStatus(s string) bool { return contains(openStatuses, s) || contains(finalStatuses, s) }

// IsOpen: the occurrence can still change.
func IsOpen(s string) bool { return contains(openStatuses, s) }

// IsResolved: completed, canceled or rescheduled - the next occurrence
// becomes available.
func IsResolved(s string) bool {
	return s == StatusCompleted || s == StatusCanceled || s == StatusRescheduled
}

// IsFinal: cannot change any more (resolved plus terminate_service).
func IsFinal(s string) bool { return contains(finalStatuses, s) }

// CanTransition reports whether from -> to is allowed.
func CanTransition(from, to string) bool {
	switch from {
	case StatusUnconfirmed:
		return to == StatusConfirmed || IsFinal(to)
	case StatusConfirmed:
		return IsFinal(to)
	}
	return false
}

// AllowedFrom lists the statuses from which a change to `to` is allowed; it
// feeds the optimistic UPDATE guard.
func AllowedFrom(to string) []string {
	var out []string
	for _, from := range openStatuses {
		if CanTransition(from, to) {
			out = append(out, from)
		}
	}
	return out
}

// EffectiveStatus is the status of an occurrence that has no stored row: the
// first occurrence takes the job's status; a later one takes it only when
// it is unconfirmed or confirmed, otherwise it is unconfirmed.
func EffectiveStatus(jobStatus string, first bool) string {
	if first || jobStatus == StatusUnconfirmed || jobStatus == StatusConfirmed {
		return jobStatus
	}
	return StatusUnconfirmed
}

// ValidateJobCreateStatus rejects statuses a new job may not start with: a job
// starts open, and is completed, canceled, terminated or rescheduled through
// its occurrences.
func ValidateJobCreateStatus(s string) error {
	if !IsKnownStatus(s) {
		return apperror.Validation("unknown status")
	}
	if !IsOpen(s) {
		return apperror.Validation("a job can only be created as unconfirmed or confirmed")
	}
	return nil
}
