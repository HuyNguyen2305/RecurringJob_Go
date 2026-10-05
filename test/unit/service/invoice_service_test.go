package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
	"recurringjob/internal/service"
)

func recurrenceDaily() recurrence.Rule { return recurrence.Rule{Frequency: "daily"} }

type fakeAvailability struct {
	jobID string
	date  time.Time
	err   error
}

func (f *fakeAvailability) AvailableFor(_ context.Context, jobID string, date time.Time) error {
	f.jobID, f.date = jobID, date
	return f.err
}

func TestInvoiceCreate(t *testing.T) {
	ctx := context.Background()
	jobID := uid("a1")
	in := service.DocumentInput{Notes: "n", LineItems: []service.LineItemInput{{Description: "Clean", Quantity: 1, UnitPriceCents: 900}}}
	rule := recurrenceDaily()

	t.Run("snapshots the job and ties the invoice to the occurrence", func(t *testing.T) {
		store := newMemDocs()
		occs := &fakeAvailability{}
		s := service.NewInvoiceService(store, mockJobs{jobID: withRefs(recJob(jobID, "2026-10-02", rule))}, occs)
		withTime := time.Date(2026, 10, 5, 14, 30, 0, 0, time.FixedZone("+7", 7*3600))
		doc, err := s.Create(ctx, jobID, withTime, in)
		if err != nil {
			t.Fatal(err)
		}
		if doc.Status != service.DocStatusDraft || doc.JobID == nil || *doc.JobID != jobID {
			t.Fatalf("doc %+v", doc)
		}
		if doc.OccurrenceDate == nil || !doc.OccurrenceDate.Equal(dt("2026-10-05")) {
			t.Fatalf("occurrence date %v", doc.OccurrenceDate)
		}
		if doc.JobSnapshot == nil || doc.JobSnapshot.ID != jobID || doc.JobSnapshot.Date != "2026-10-02" || doc.JobSnapshot.Recurrence == nil {
			t.Fatalf("snapshot %+v", doc.JobSnapshot)
		}
		if occs.jobID != jobID || !occs.date.Equal(dt("2026-10-05")) {
			t.Fatalf("availability checked for %s %v", occs.jobID, occs.date)
		}
		if doc.CustomerID != refCustomer || doc.LocationID != refLocation || doc.ServiceTypeID != refService {
			t.Fatalf("an invoice is for the job's customer, location and service type: %s %s %s", doc.CustomerID, doc.LocationID, doc.ServiceTypeID)
		}
		if doc.Customer == nil || doc.Customer.Name != "Ada" || doc.Location == nil || doc.ServiceType == nil {
			t.Fatalf("the response needs the loaded references: %+v", doc)
		}
		snap := doc.JobSnapshot
		if snap.CustomerID != refCustomer || snap.CustomerName != "Ada" || snap.LocationAddress != "1 Main Street" || snap.ServiceTypeName != "Window cleaning" || snap.StartTime != "09:30:00" || snap.LengthMinutes != 90 {
			t.Fatalf("snapshot %+v", snap)
		}
	})

	t.Run("rejections save nothing", func(t *testing.T) {
		boom := errors.New("db down")
		tests := []struct {
			name  string
			job   string
			input service.DocumentInput
			occs  error
			want  int
			isErr error
		}{
			{"malformed job id", "nope", in, nil, 400, nil},
			{"invalid input", jobID, service.DocumentInput{LineItems: []service.LineItemInput{{Description: "x", Quantity: 0}}}, nil, 400, nil},
			{"not an occurrence", jobID, in, apperror.NotFound("date is not an occurrence of this job"), 404, nil},
			{"occurrence not available", jobID, in, apperror.Conflict("occurrence is canceled"), 409, nil},
			{"availability infrastructure error", jobID, in, boom, 0, boom},
			{"unknown job", uid("ff"), in, nil, 404, nil},
		}
		for _, tt := range tests {
			store := newMemDocs()
			s := service.NewInvoiceService(store, mockJobs{jobID: withRefs(recJob(jobID, "2026-10-02", rule))}, &fakeAvailability{err: tt.occs})
			_, err := s.Create(ctx, tt.job, dt("2026-10-05"), tt.input)
			if tt.isErr != nil {
				if !errors.Is(err, tt.isErr) {
					t.Errorf("%s: %v", tt.name, err)
				}
			} else if statusOf(t, err) != tt.want {
				t.Errorf("%s: %v", tt.name, err)
			}
			if len(store.docs) != 0 {
				t.Errorf("%s: an invoice was saved", tt.name)
			}
		}
	})

	t.Run("a duplicate invoice and store errors come back as is", func(t *testing.T) {
		store := newMemDocs()
		s := service.NewInvoiceService(store, mockJobs{jobID: withRefs(recJob(jobID, "2026-10-02", rule))}, &fakeAvailability{})
		dup := apperror.Conflict("an invoice already exists for this job")
		store.createErr = dup
		if _, err := s.Create(ctx, jobID, dt("2026-10-05"), in); !errors.Is(err, dup) {
			t.Fatalf("duplicate: %v", err)
		}
		s = service.NewInvoiceService(store, faultyJobs{err: dup}, &fakeAvailability{})
		if _, err := s.Create(ctx, jobID, dt("2026-10-05"), in); !errors.Is(err, dup) {
			t.Fatalf("job lookup: %v", err)
		}
	})
}

func TestInvoiceOccurrenceLookups(t *testing.T) {
	ctx := context.Background()
	jobID := uid("a1")
	local := time.Date(2026, 10, 5, 22, 0, 0, 0, time.FixedZone("-8", -8*3600)) // 06:00 UTC the next day

	t.Run("voiding touches only draft and sent invoices and sets them void", func(t *testing.T) {
		store := newMemDocs()
		store.voidedN = 2
		s := service.NewInvoiceService(store, mockJobs{}, nopAvailability{})
		n, err := s.VoidUnpaidForOccurrence(ctx, jobID, local)
		if err != nil || n != 2 || len(store.voidCalls) != 1 {
			t.Fatalf("n=%d err=%v calls=%+v", n, err, store.voidCalls)
		}
		c := store.voidCalls[0]
		if c.jobID != jobID || c.status != "void" || !c.date.Equal(dt("2026-10-06")) || len(c.allowedFrom) != 2 || !hasString(c.allowedFrom, "draft") || !hasString(c.allowedFrom, "sent") {
			t.Fatalf("call %+v", c)
		}
		if hasString(c.allowedFrom, "paid") {
			t.Fatal("a paid invoice must never be voided")
		}
	})

	t.Run("paid ids and the paid-for-job check look at the paid status", func(t *testing.T) {
		store := newMemDocs()
		store.paidIDs, store.hasPaid = []string{"p1"}, true
		s := service.NewInvoiceService(store, mockJobs{}, nopAvailability{})
		if ids, err := s.PaidIDsForOccurrence(ctx, jobID, dt("2026-10-05")); err != nil || len(ids) != 1 || store.idsStatus != "paid" {
			t.Fatalf("ids=%v err=%v status=%s", ids, err, store.idsStatus)
		}
		if ok, err := s.HasPaidForJob(ctx, jobID); err != nil || !ok || store.existsStatus != "paid" {
			t.Fatalf("ok=%v err=%v status=%s", ok, err, store.existsStatus)
		}
	})

	t.Run("store errors are returned as is", func(t *testing.T) {
		store := newMemDocs()
		store.occErr = errors.New("db down")
		s := service.NewInvoiceService(store, mockJobs{}, nopAvailability{})
		if _, err := s.VoidUnpaidForOccurrence(ctx, jobID, dt("2026-10-05")); !errors.Is(err, store.occErr) {
			t.Errorf("void: %v", err)
		}
		if _, err := s.PaidIDsForOccurrence(ctx, jobID, dt("2026-10-05")); !errors.Is(err, store.occErr) {
			t.Errorf("paid ids: %v", err)
		}
		if _, err := s.HasPaidForJob(ctx, jobID); !errors.Is(err, store.occErr) {
			t.Errorf("has paid: %v", err)
		}
	})
}

// withRefs gives a job the fake customer, location and service type, loaded
// the way the repository loads them.
func withRefs(j *model.Job) *model.Job {
	j.CustomerID, j.LocationID, j.ServiceTypeID = refCustomer, refLocation, refService
	j.StartTime, j.LengthMinutes = "09:30:00", 90
	j.Customer = &model.Customer{ID: refCustomer, Name: "Ada"}
	j.Location = &model.Location{ID: refLocation, CustomerID: refCustomer, AddressLine1: "1 Main Street"}
	j.ServiceType = &model.ServiceType{ID: refService, Name: "Window cleaning"}
	return j
}
