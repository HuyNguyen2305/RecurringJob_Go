package dto_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/dto"
	"recurringjob/internal/model"
)

func TestNewDocumentResponse(t *testing.T) {
	t.Run("an estimate with no job has null job fields and an empty item list", func(t *testing.T) {
		got := dto.NewDocumentResponse(&model.CustomerDocument{ID: "d", Type: "estimate", Status: "draft", Notes: "n"})
		if got.ID != "d" || got.Type != "estimate" || got.Status != "draft" || got.Notes != "n" ||
			got.JobID != nil || got.JobSnapshot != nil || got.OccurrenceDate != nil {
			t.Fatalf("got %+v", got)
		}
		if got.LineItems == nil || len(got.LineItems) != 0 || got.TotalCents != 0 || got.SubtotalCents != 0 {
			t.Fatalf("items %+v total=%d", got.LineItems, got.TotalCents)
		}
	})

	t.Run("the customer, location and service type are embedded when loaded", func(t *testing.T) {
		email, city := "ada@example.com", "Springfield"
		got := dto.NewDocumentResponse(&model.CustomerDocument{
			Customer:    &model.Customer{ID: "c1", Name: "Ada", Email: &email},
			Location:    &model.Location{ID: "l1", CustomerID: "c1", AddressLine1: "1 Main Street", City: &city},
			ServiceType: &model.ServiceType{ID: "s1", Name: "Window cleaning"},
		})
		if got.Customer == nil || got.Customer.ID != "c1" || got.Customer.Name != "Ada" || got.Customer.Email == nil || *got.Customer.Email != email || got.Customer.Phone != nil {
			t.Fatalf("customer %+v", got.Customer)
		}
		if got.Location == nil || got.Location.AddressLine1 != "1 Main Street" || got.Location.CustomerID != "c1" || got.Location.City == nil || *got.Location.City != city || got.Location.State != nil {
			t.Fatalf("location %+v", got.Location)
		}
		if got.ServiceType == nil || got.ServiceType.ID != "s1" || got.ServiceType.Name != "Window cleaning" || got.ServiceType.Description != nil {
			t.Fatalf("service type %+v", got.ServiceType)
		}
	})

	t.Run("they are null when not loaded", func(t *testing.T) {
		got := dto.NewDocumentResponse(&model.CustomerDocument{})
		if got.Customer != nil || got.Location != nil || got.ServiceType != nil {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("line totals and the document total are computed", func(t *testing.T) {
		got := dto.NewDocumentResponse(&model.CustomerDocument{LineItems: []model.CustomerLineItem{
			{Description: "Clean", Quantity: 2, UnitPriceCents: 500},
			{Description: "Polish", Quantity: 3, UnitPriceCents: 0},
			{Description: "Wax", Quantity: 1, UnitPriceCents: 250},
		}})
		if len(got.LineItems) != 3 || got.LineItems[0].TotalCents != 1000 || got.LineItems[1].TotalCents != 0 || got.LineItems[2].TotalCents != 250 {
			t.Fatalf("items %+v", got.LineItems)
		}
		if got.TotalCents != 1250 || got.SubtotalCents != 1250 {
			t.Fatalf("total %d subtotal %d", got.TotalCents, got.SubtotalCents)
		}
	})

	t.Run("an invoice shows its job, snapshot and occurrence date", func(t *testing.T) {
		jobID, date := "j1", civil.New(2026, 10, 5)
		got := dto.NewDocumentResponse(&model.CustomerDocument{
			Type: "invoice", JobID: &jobID, OccurrenceDate: &date,
			JobSnapshot: &model.JobSnapshot{ID: jobID, Date: "2026-10-02", Status: "unconfirmed", CustomerName: "Ada", LocationAddress: "1 Main Street", ServiceTypeName: "Window cleaning", StartTime: "09:00:00", LengthMinutes: 60},
		})
		if got.JobID == nil || *got.JobID != "j1" || got.OccurrenceDate == nil || *got.OccurrenceDate != "2026-10-05" ||
			got.JobSnapshot == nil || got.JobSnapshot.Date != "2026-10-02" || got.JobSnapshot.CustomerName != "Ada" || got.JobSnapshot.LengthMinutes != 60 {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("timestamps are UTC and the model is not mutated", func(t *testing.T) {
		local := time.Date(2026, 10, 2, 9, 33, 27, 0, time.FixedZone("+7", 7*3600))
		m := &model.CustomerDocument{CreatedAt: local, UpdatedAt: local}
		got := dto.NewDocumentResponse(m)
		if got.CreatedAt.Location() != time.UTC || got.UpdatedAt.Hour() != 2 || !got.CreatedAt.Equal(local) {
			t.Fatalf("got %v %v", got.CreatedAt, got.UpdatedAt)
		}
		if m.CreatedAt.Location() == time.UTC {
			t.Fatal("model value was converted in place")
		}
	})
}

func TestNewDocumentResponses(t *testing.T) {
	if got := dto.NewDocumentResponses(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil list: %#v", got)
	}
	got := dto.NewDocumentResponses([]model.CustomerDocument{{ID: "a"}, {ID: "b"}})
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("got %+v", got)
	}
}

func TestDocumentResponseLifecycleFields(t *testing.T) {
	sent := time.Date(2026, 10, 2, 9, 0, 0, 0, time.FixedZone("+7", 7*3600))
	got := dto.NewDocumentResponse(&model.CustomerDocument{ID: "d", Number: "INV-000007", Revision: 3, SentAt: &sent})
	if got.Number != "INV-000007" || got.Revision != 3 || got.SentAt == nil || !got.SentAt.Equal(sent) || got.SentAt.Location() != time.UTC {
		t.Fatalf("got %+v", got)
	}
	if got.PaidAt != nil || got.RefundedAt != nil {
		t.Fatalf("unset timestamps must be null: %+v", got)
	}
	raw, _ := json.Marshal(got)
	for _, want := range []string{`"number":"INV-000007"`, `"revision":3`, `"sentAt":"2026-10-02T02:00:00Z"`, `"paidAt":null`, `"refundedAt":null`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("json lacks %s: %s", want, raw)
		}
	}
}

func TestRevisionResponses(t *testing.T) {
	if got := dto.NewRevisionResponses(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil list: %#v", got)
	}
	at := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	got := dto.NewRevisionResponses([]model.DocumentRevision{{
		Revision: 2, CreatedAt: at,
		Content: model.RevisionContent{
			Notes: "old", CustomerID: "c", LocationID: "l", ServiceTypeID: "s",
			LineItems: []model.RevisionLine{{Description: "A", Quantity: 2, UnitPriceCents: 150}, {Description: "B", Quantity: 1, UnitPriceCents: 50}},
		},
	}})
	if len(got) != 1 || got[0].Revision != 2 || got[0].Notes != "old" || got[0].CustomerID != "c" || got[0].TotalCents != 350 ||
		len(got[0].LineItems) != 2 || got[0].LineItems[0].TotalCents != 300 || !got[0].ReplacedAt.Equal(at) {
		t.Fatalf("got %+v", got)
	}
}
