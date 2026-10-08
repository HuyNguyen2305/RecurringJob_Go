package dto_test

import (
	"encoding/json"
	"testing"
	"time"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/dto"
	"recurringjob/internal/model"
)

func TestNewWorkOrderResponse(t *testing.T) {
	t.Run("a bare work order has an empty task list and null references", func(t *testing.T) {
		got := dto.NewWorkOrderResponse(&model.WorkOrder{ID: "w", Number: "WO-000007", Status: "draft", Notes: "n", JobID: "j", OccurrenceDate: civil.New(2026, 10, 5)})
		if got.ID != "w" || got.Number != "WO-000007" || got.Status != "draft" || got.Notes != "n" || got.JobID != "j" || got.OccurrenceDate != "2026-10-05" {
			t.Fatalf("got %+v", got)
		}
		if got.Tasks == nil || len(got.Tasks) != 0 || got.Customer != nil || got.Location != nil || got.ServiceType != nil || got.JobSnapshot != nil || got.CompletedAt != nil {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("the occurrence date is the civil date whatever zone the value carries", func(t *testing.T) {
		late := time.Date(2026, 10, 5, 23, 30, 0, 0, time.FixedZone("-8", -8*3600)) // 2026-10-06 07:30Z
		if got := dto.NewWorkOrderResponse(&model.WorkOrder{OccurrenceDate: civil.Truncate(late)}); got.OccurrenceDate != "2026-10-06" {
			t.Fatalf("date %q", got.OccurrenceDate)
		}
	})

	t.Run("tasks keep their order and done flag", func(t *testing.T) {
		got := dto.NewWorkOrderResponse(&model.WorkOrder{Tasks: []model.WorkOrderTask{{Description: "A"}, {Description: "B", Done: true}}})
		if len(got.Tasks) != 2 || got.Tasks[0].Description != "A" || got.Tasks[0].Done || got.Tasks[1].Description != "B" || !got.Tasks[1].Done {
			t.Fatalf("tasks %+v", got.Tasks)
		}
	})

	t.Run("the references and snapshot are embedded when loaded", func(t *testing.T) {
		city := "Springfield"
		got := dto.NewWorkOrderResponse(&model.WorkOrder{
			Customer:    &model.Customer{ID: "c1", Name: "Ada"},
			Location:    &model.Location{ID: "l1", CustomerID: "c1", AddressLine1: "1 Main Street", City: &city},
			ServiceType: &model.ServiceType{ID: "s1", Name: "Window cleaning"},
			JobSnapshot: &model.JobSnapshot{ID: "j", CustomerName: "Ada"},
		})
		if got.Customer == nil || got.Customer.Name != "Ada" || got.Location == nil || got.Location.AddressLine1 != "1 Main Street" ||
			got.ServiceType == nil || got.ServiceType.Name != "Window cleaning" || got.JobSnapshot == nil || got.JobSnapshot.CustomerName != "Ada" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("times are UTC", func(t *testing.T) {
		zone := time.FixedZone("+7", 7*3600)
		done := time.Date(2026, 10, 5, 14, 0, 0, 0, zone)
		got := dto.NewWorkOrderResponse(&model.WorkOrder{CompletedAt: &done, CreatedAt: done, UpdatedAt: done})
		if got.CompletedAt == nil || got.CompletedAt.Location() != time.UTC || got.CreatedAt.Location() != time.UTC || got.UpdatedAt.Location() != time.UTC || !got.CompletedAt.Equal(done) {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("the JSON keys are the camelCase the API promises", func(t *testing.T) {
		raw, err := json.Marshal(dto.NewWorkOrderResponse(&model.WorkOrder{}))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"id", "number", "status", "customer", "location", "serviceType", "notes", "jobId", "jobSnapshot", "occurrenceDate", "tasks", "completedAt", "createdAt", "updatedAt"} {
			if _, ok := m[key]; !ok {
				t.Errorf("missing key %q in %s", key, raw)
			}
		}
	})
}

func TestNewWorkOrderResponses(t *testing.T) {
	if got := dto.NewWorkOrderResponses(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil in must give an empty, non-nil list: %#v", got)
	}
	got := dto.NewWorkOrderResponses([]model.WorkOrder{{ID: "a"}, {ID: "b"}})
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("got %+v", got)
	}
}
