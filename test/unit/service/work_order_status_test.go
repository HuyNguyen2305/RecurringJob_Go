package service_test

import (
	"reflect"
	"slices"
	"testing"

	"recurringjob/internal/service"
)

func TestWorkOrderStatusTransitions(t *testing.T) {
	all := []string{"draft", "scheduled", "in_progress", "completed", "canceled"}
	allowed := map[string][]string{
		"draft":       {"scheduled", "canceled"},
		"scheduled":   {"in_progress", "canceled"},
		"in_progress": {"completed", "canceled"},
	}
	for _, from := range all {
		for _, to := range all {
			want := slices.Contains(allowed[from], to)
			if got := service.CanTransitionWorkOrder(from, to); got != want {
				t.Errorf("%s -> %s: %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestIsKnownWorkOrderStatus(t *testing.T) {
	for _, s := range []string{"draft", "scheduled", "in_progress", "completed", "canceled"} {
		if !service.IsKnownWorkOrderStatus(s) {
			t.Errorf("%s should be known", s)
		}
	}
	for _, s := range []string{"", "Draft", "paid", "void", "cancelled", "in progress"} {
		if service.IsKnownWorkOrderStatus(s) {
			t.Errorf("%q should be unknown", s)
		}
	}
}

func TestWorkOrderAllowedFrom(t *testing.T) {
	tests := []struct {
		to   string
		want []string
	}{
		{service.WOStatusScheduled, []string{service.WOStatusDraft}},
		{service.WOStatusInProgress, []string{service.WOStatusScheduled}},
		{service.WOStatusCompleted, []string{service.WOStatusInProgress}},
		{service.WOStatusCanceled, []string{service.WOStatusDraft, service.WOStatusScheduled, service.WOStatusInProgress}},
		{service.WOStatusDraft, nil},
	}
	for _, tt := range tests {
		if got := service.WorkOrderAllowedFrom(tt.to); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("WorkOrderAllowedFrom(%s) = %v, want %v", tt.to, got, tt.want)
		}
	}
}
