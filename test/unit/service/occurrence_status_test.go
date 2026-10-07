package service_test

import (
	"reflect"
	"testing"

	"recurringjob/internal/service"
)

func TestCanTransition(t *testing.T) {
	all := []string{service.StatusUnconfirmed, service.StatusConfirmed, service.StatusCompleted, service.StatusCanceled, service.StatusTerminateService, service.StatusRescheduled}
	allowed := map[[2]string]bool{
		{service.StatusUnconfirmed, service.StatusConfirmed}: true,
	}
	for _, from := range []string{service.StatusUnconfirmed, service.StatusConfirmed} {
		for _, to := range finalStatuses {
			allowed[[2]string{from, to}] = true
		}
	}
	for _, from := range all {
		for _, to := range all {
			if got, want := service.CanTransition(from, to), allowed[[2]string{from, to}]; got != want {
				t.Errorf("%s -> %s: got %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestStatusSets(t *testing.T) {
	tests := []struct {
		status                string
		open, resolved, final bool
	}{
		{service.StatusUnconfirmed, true, false, false},
		{service.StatusConfirmed, true, false, false},
		{service.StatusCompleted, false, true, true},
		{service.StatusCanceled, false, true, true},
		{service.StatusRescheduled, false, true, true},
		{service.StatusTerminateService, false, false, true},
	}
	for _, tt := range tests {
		if service.IsOpen(tt.status) != tt.open || service.IsResolved(tt.status) != tt.resolved || service.IsFinal(tt.status) != tt.final {
			t.Errorf("%s: wrong set membership", tt.status)
		}
	}
}

func TestAllowedFrom(t *testing.T) {
	tests := []struct {
		to   string
		want []string
	}{
		{service.StatusConfirmed, []string{service.StatusUnconfirmed}},
		{service.StatusCompleted, []string{service.StatusUnconfirmed, service.StatusConfirmed}},
		{service.StatusUnconfirmed, nil},
	}
	for _, tt := range tests {
		if got := service.AllowedFrom(tt.to); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("AllowedFrom(%s) = %v, want %v", tt.to, got, tt.want)
		}
	}
}

func TestEffectiveStatus(t *testing.T) {
	tests := []struct {
		job   string
		first bool
		want  string
	}{
		{service.StatusCompleted, true, service.StatusCompleted},
		{service.StatusConfirmed, false, service.StatusConfirmed},
		{service.StatusUnconfirmed, false, service.StatusUnconfirmed},
		{service.StatusCanceled, false, service.StatusUnconfirmed},
		{service.StatusCompleted, false, service.StatusUnconfirmed},
	}
	for _, tt := range tests {
		if got := service.EffectiveStatus(tt.job, tt.first); got != tt.want {
			t.Errorf("EffectiveStatus(%s, first=%v) = %s, want %s", tt.job, tt.first, got, tt.want)
		}
	}
}

func TestValidateJobCreateStatus(t *testing.T) {
	if service.ValidateJobCreateStatus(service.StatusRescheduled) == nil {
		t.Error("rescheduled must be rejected")
	}
	if service.ValidateJobCreateStatus("bogus") == nil {
		t.Error("unknown status must be rejected")
	}
	if err := service.ValidateJobCreateStatus(service.StatusConfirmed); err != nil {
		t.Error(err)
	}
}

func TestValidateJobCreateStatusAll(t *testing.T) {
	for _, s := range []string{service.StatusUnconfirmed, service.StatusConfirmed, service.StatusCompleted, service.StatusCanceled, service.StatusTerminateService} {
		if err := service.ValidateJobCreateStatus(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	for _, s := range []string{service.StatusRescheduled, "in_progress", "UNCONFIRMED", " confirmed", "done"} {
		if got := statusOf(t, service.ValidateJobCreateStatus(s)); got != 400 {
			t.Errorf("%q: status %d, want 400", s, got)
		}
	}
}

func TestIsKnownStatus(t *testing.T) {
	for _, s := range allStatuses {
		if !service.IsKnownStatus(s) {
			t.Errorf("%s should be known", s)
		}
	}
	for _, s := range []string{"", "Confirmed", "done", "rescheduled ", "terminated"} {
		if service.IsKnownStatus(s) {
			t.Errorf("%q should be unknown", s)
		}
	}
}

func TestStatusSetsPartitionTheStatuses(t *testing.T) {
	for _, s := range allStatuses {
		open, final := service.IsOpen(s), service.IsFinal(s)
		if open == final {
			t.Errorf("%s must be exactly one of open/final (open=%v final=%v)", s, open, final)
		}
		if service.IsResolved(s) && !final {
			t.Errorf("%s is resolved but not final", s)
		}
	}
	if service.IsResolved(service.StatusTerminateService) {
		t.Error("terminate_service must not make the next occurrence available")
	}
}

// The statuses that can no longer change, spelled out independently of the package.
var finalStatuses = []string{service.StatusCompleted, service.StatusCanceled, service.StatusRescheduled, service.StatusTerminateService}
