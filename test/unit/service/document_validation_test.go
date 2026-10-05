package service_test

import (
	"context"
	"strings"
	"testing"

	"recurringjob/internal/service"
)

// estimateFor wraps a document's content in an estimate request for the fake
// customer, location and service type.
func estimateFor(in service.DocumentInput) service.EstimateInput {
	return service.EstimateInput{CustomerID: refCustomer, LocationID: refLocation, ServiceTypeID: refService, DocumentInput: in}
}

// The validation rules are exercised through EstimateService.Create, which
// runs them before saving anything.
func TestDocumentInputValidation(t *testing.T) {
	ctx := context.Background()
	line := service.LineItemInput{Description: "Clean", Quantity: 1, UnitPriceCents: 100}
	valid := func() service.DocumentInput {
		return service.DocumentInput{Notes: "first visit", LineItems: []service.LineItemInput{line}}
	}
	many := make([]service.LineItemInput, service.MaxLineItems+1)
	for i := range many {
		many[i] = line
	}

	tests := []struct {
		name   string
		mutate func(in *service.DocumentInput)
		want   int // 0 = accepted
	}{
		{"valid", func(*service.DocumentInput) {}, 0},
		{"no notes and no items is a fine draft", func(in *service.DocumentInput) { in.Notes, in.LineItems = "", nil }, 0},
		{"notes too long", func(in *service.DocumentInput) { in.Notes = strings.Repeat("n", service.MaxNotesLen+1) }, 400},
		{"notes at the limit", func(in *service.DocumentInput) { in.Notes = strings.Repeat("n", service.MaxNotesLen) }, 0},
		{"too many line items", func(in *service.DocumentInput) { in.LineItems = many }, 400},
		{"blank description", func(in *service.DocumentInput) {
			in.LineItems = []service.LineItemInput{{Description: " ", Quantity: 1}}
		}, 400},
		{"description too long", func(in *service.DocumentInput) {
			in.LineItems = []service.LineItemInput{{Description: strings.Repeat("d", service.MaxDescriptionLen+1), Quantity: 1}}
		}, 400},
		{"zero quantity", func(in *service.DocumentInput) {
			in.LineItems = []service.LineItemInput{{Description: "x", Quantity: 0}}
		}, 400},
		{"quantity too large", func(in *service.DocumentInput) {
			in.LineItems = []service.LineItemInput{{Description: "x", Quantity: service.MaxQuantity + 1}}
		}, 400},
		{"negative price", func(in *service.DocumentInput) {
			in.LineItems = []service.LineItemInput{{Description: "x", Quantity: 1, UnitPriceCents: -1}}
		}, 400},
		{"price too large", func(in *service.DocumentInput) {
			in.LineItems = []service.LineItemInput{{Description: "x", Quantity: 1, UnitPriceCents: service.MaxUnitPriceCents + 1}}
		}, 400},
		{"free item", func(in *service.DocumentInput) {
			in.LineItems = []service.LineItemInput{{Description: "x", Quantity: 1, UnitPriceCents: 0}}
		}, 0},
	}
	for _, tt := range tests {
		store := newMemDocs()
		s := service.NewEstimateService(store, &fakeJobCreator{}, &fakeRefs{})
		in := valid()
		tt.mutate(&in)
		_, err := s.Create(ctx, estimateFor(in))
		if tt.want == 0 {
			if err != nil {
				t.Errorf("%s: unexpected %v", tt.name, err)
			}
			continue
		}
		if statusOf(t, err) != tt.want {
			t.Errorf("%s: %v", tt.name, err)
		}
		if len(store.docs) != 0 {
			t.Errorf("%s: a document was saved", tt.name)
		}
	}
}

func TestMaximumDocumentTotalDoesNotOverflow(t *testing.T) {
	items := make([]service.LineItemInput, service.MaxLineItems)
	for i := range items {
		items[i] = service.LineItemInput{Description: "x", Quantity: service.MaxQuantity, UnitPriceCents: service.MaxUnitPriceCents}
	}
	s := service.NewEstimateService(newMemDocs(), &fakeJobCreator{}, &fakeRefs{})
	doc, err := s.Create(context.Background(), estimateFor(service.DocumentInput{LineItems: items}))
	if err != nil {
		t.Fatal(err)
	}
	want := int64(service.MaxLineItems) * service.MaxQuantity * service.MaxUnitPriceCents
	if got := doc.TotalCents(); got != want || got <= 0 {
		t.Fatalf("total %d, want %d", got, want)
	}
}
