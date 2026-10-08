package service_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
	"recurringjob/internal/service"
)

type fakeJobCreator struct {
	in  service.CreateJobInput
	err error
}

func (f *fakeJobCreator) CreateJob(_ context.Context, in service.CreateJobInput) (*model.Job, error) {
	f.in = in
	if f.err != nil {
		return nil, f.err
	}
	return &model.Job{
		ID: uid("j1"), CustomerID: in.CustomerID, LocationID: in.LocationID, ServiceTypeID: in.ServiceTypeID,
		Date: in.Date, StartTime: in.StartTime, LengthMinutes: in.LengthMinutes, Status: service.StatusUnconfirmed, Recurrence: in.Recurrence,
		Customer:    &model.Customer{ID: in.CustomerID, Name: "Ada"},
		Location:    &model.Location{ID: in.LocationID, CustomerID: in.CustomerID, AddressLine1: "1 Main Street"},
		ServiceType: &model.ServiceType{ID: in.ServiceTypeID, Name: "Window cleaning"},
	}, nil
}

func TestEstimateCreate(t *testing.T) {
	ctx := context.Background()
	store := newMemDocs()
	refs := &fakeRefs{}
	s := service.NewEstimateService(store, &fakeJobCreator{}, refs)

	doc, err := s.Create(ctx, estimateFor(service.DocumentInput{
		Notes: "first visit", LineItems: []service.LineItemInput{{Description: "Clean", Quantity: 2, UnitPriceCents: 500}, {Description: "Polish", Quantity: 1, UnitPriceCents: 250}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Status != service.DocStatusDraft || doc.Notes != "first visit" || doc.JobID != nil || doc.OccurrenceDate != nil || doc.JobSnapshot != nil {
		t.Fatalf("doc %+v", doc)
	}
	if doc.CustomerID != refCustomer || doc.LocationID != refLocation || doc.ServiceTypeID != refService {
		t.Fatalf("references %s %s %s", doc.CustomerID, doc.LocationID, doc.ServiceTypeID)
	}
	if doc.Customer == nil || doc.Customer.Name != "Ada" || doc.Location == nil || doc.Location.AddressLine1 != "1 Main Street" || doc.ServiceType == nil || doc.ServiceType.Name != "Window cleaning" {
		t.Fatalf("the response needs the loaded references: %+v %+v %+v", doc.Customer, doc.Location, doc.ServiceType)
	}
	if len(refs.calls) != 1 || refs.calls[0] != [3]string{refCustomer, refLocation, refService} {
		t.Fatalf("resolver calls %v", refs.calls)
	}
	if len(doc.LineItems) != 2 || doc.LineItems[0].Position != 0 || doc.LineItems[1].Position != 1 || doc.TotalCents() != 1250 {
		t.Fatalf("items %+v total=%d", doc.LineItems, doc.TotalCents())
	}

	boom := errors.New("db down")
	store.createErr = boom
	if _, err := s.Create(ctx, estimateFor(service.DocumentInput{})); !errors.Is(err, boom) {
		t.Fatalf("store error: %v", err)
	}
}

func TestEstimateCreateNeedsValidReferences(t *testing.T) {
	ctx := context.Background()
	for name, refsErr := range map[string]error{
		"unknown customer":           apperror.NotFound("customer not found"),
		"location of someone else":   apperror.Validation("locationId does not belong to the customer"),
		"malformed id":               apperror.Validation("customerId must be a UUID"),
		"infrastructure error":       errors.New("db down"),
		"unknown service type (404)": apperror.NotFound("service type not found"),
	} {
		store := newMemDocs()
		s := service.NewEstimateService(store, &fakeJobCreator{}, &fakeRefs{err: refsErr})
		if _, err := s.Create(ctx, estimateFor(service.DocumentInput{})); !errors.Is(err, refsErr) {
			t.Errorf("%s: %v", name, err)
		}
		if len(store.docs) != 0 {
			t.Errorf("%s: an estimate was saved", name)
		}
	}
}

func TestEstimateUpdateReferences(t *testing.T) {
	ctx := context.Background()
	otherCustomer, otherLocation := uid("d1"), uid("d2")
	str := func(s string) *string { return &s }

	t.Run("an estimate without a job can change customer, location and service type", func(t *testing.T) {
		for _, status := range []string{"draft", "sent"} {
			store := newMemDocs()
			refs := &fakeRefs{}
			d := store.put(status, oneItem())
			s := service.NewEstimateService(store, &fakeJobCreator{}, refs)
			got, err := s.Update(ctx, d.ID, service.DocumentPatch{CustomerID: &otherCustomer, LocationID: &otherLocation})
			if err != nil || got.CustomerID != otherCustomer || got.LocationID != otherLocation || got.ServiceTypeID != refService {
				t.Fatalf("%s: got %+v err=%v", status, got, err)
			}
			// What the patch leaves out keeps the estimate's current value.
			if len(refs.calls) != 1 || refs.calls[0] != [3]string{otherCustomer, otherLocation, refService} {
				t.Fatalf("%s: resolver calls %v", status, refs.calls)
			}
		}
	})

	t.Run("a patch that fails the reference check changes nothing", func(t *testing.T) {
		store := newMemDocs()
		d := store.put("draft", oneItem())
		bad := apperror.Validation("locationId does not belong to the customer")
		s := service.NewEstimateService(store, &fakeJobCreator{}, &fakeRefs{err: bad})
		if _, err := s.Update(ctx, d.ID, service.DocumentPatch{Notes: str("x"), CustomerID: &otherCustomer}); !errors.Is(err, bad) {
			t.Fatalf("got %v", err)
		}
		if d.CustomerID != refCustomer || d.Notes != "" {
			t.Fatalf("estimate changed: %+v", d)
		}
	})

	t.Run("an approved estimate is fixed to its job's references", func(t *testing.T) {
		store := newMemDocs()
		d := store.put("approved", oneItem())
		jobID := uid("j1")
		d.JobID = &jobID
		refs := &fakeRefs{}
		s := service.NewEstimateService(store, &fakeJobCreator{}, refs)
		if _, err := s.Update(ctx, d.ID, service.DocumentPatch{CustomerID: &otherCustomer}); statusOf(t, err) != 409 {
			t.Fatalf("got %v", err)
		}
		if d.CustomerID != refCustomer || len(refs.calls) != 0 {
			t.Fatalf("estimate changed or resolver called: %+v %v", d, refs.calls)
		}
		// Notes and lines of an approved estimate stay editable.
		if _, err := s.Update(ctx, d.ID, service.DocumentPatch{Notes: str("still editable")}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestEstimateChangeStatusRejectsApproved(t *testing.T) {
	store := newMemDocs()
	d := store.put("sent", oneItem())
	s := newEstimateService(store, &fakeJobCreator{})
	if _, err := s.ChangeStatus(context.Background(), d.ID, "approved"); statusOf(t, err) != 400 {
		t.Fatalf("got %v", err)
	}
	if d.Status != "sent" || d.JobID != nil {
		t.Fatalf("estimate changed: %+v", d)
	}
	if got, err := s.ChangeStatus(context.Background(), d.ID, "declined"); err != nil || got.Status != "declined" {
		t.Fatalf("declined: %+v err=%v", got, err)
	}
}

func TestEstimateApprove(t *testing.T) {
	ctx := context.Background()
	rule := &recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{1}}

	t.Run("creates the job and ties the estimate to it", func(t *testing.T) {
		for _, from := range []string{"draft", "sent"} {
			store := newMemDocs()
			d := store.put(from, oneItem())
			jobs := &fakeJobCreator{}
			s := newEstimateService(store, jobs)
			got, err := s.Approve(ctx, d.ID, service.ApproveEstimateInput{StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-12"), Recurrence: rule})
			if err != nil {
				t.Fatalf("from %s: %v", from, err)
			}
			if got.Status != "approved" || got.JobID == nil || *got.JobID != uid("j1") {
				t.Fatalf("from %s: doc %+v", from, got)
			}
			if got.JobSnapshot == nil || got.JobSnapshot.Date != "2026-10-12" || got.JobSnapshot.ID != uid("j1") || got.JobSnapshot.Recurrence != rule {
				t.Fatalf("from %s: snapshot %+v", from, got.JobSnapshot)
			}
			if !jobs.in.Date.Equal(dt("2026-10-12")) || jobs.in.Recurrence != rule {
				t.Fatalf("job input %+v", jobs.in)
			}
			// The job is for the estimate's customer, location and service type.
			if jobs.in.CustomerID != refCustomer || jobs.in.LocationID != refLocation || jobs.in.ServiceTypeID != refService ||
				jobs.in.StartTime != "09:00" || jobs.in.LengthMinutes != 60 {
				t.Fatalf("job input %+v", jobs.in)
			}
			snap := got.JobSnapshot
			if snap.CustomerID != refCustomer || snap.CustomerName != "Ada" || snap.LocationAddress != "1 Main Street" || snap.ServiceTypeName != "Window cleaning" || snap.StartTime != "09:00" || snap.LengthMinutes != 60 {
				t.Fatalf("snapshot %+v", snap)
			}
			if store.transacted != 1 {
				t.Fatalf("job and estimate were not saved in one transaction (%d)", store.transacted)
			}
		}
	})

	t.Run("the job date must be today or later", func(t *testing.T) {
		tests := []struct {
			name string
			date string
			want int // 0 = approved
		}{
			{"a week ago", "2026-09-28", 400},
			{"yesterday", "2026-10-04", 400},
			{"today", "2026-10-05", 0},
			{"tomorrow", "2026-10-06", 0},
			{"next year", "2027-10-05", 0},
		}
		for _, tt := range tests {
			store := newMemDocs()
			d := store.put("sent", oneItem())
			jobs := &fakeJobCreator{}
			// A time of day later than midnight: only the calendar day counts.
			late := dt(tt.date).Add(23*time.Hour + 59*time.Minute)
			_, err := newEstimateService(store, jobs).Approve(ctx, d.ID, service.ApproveEstimateInput{StartTime: "09:00", LengthMinutes: 60, Date: late})
			if tt.want == 0 {
				if err != nil || d.Status != "approved" {
					t.Errorf("%s: err=%v status=%s", tt.name, err, d.Status)
				}
				continue
			}
			if statusOf(t, err) != tt.want {
				t.Errorf("%s: %v", tt.name, err)
			}
			if !jobs.in.Date.IsZero() || store.transacted != 0 || d.Status != "sent" || d.JobID != nil {
				t.Errorf("%s: a job was created or the estimate changed", tt.name)
			}
		}
	})

	t.Run("rejections create no job", func(t *testing.T) {
		tests := []struct {
			name, status string
			items        bool
			id           string
			want         int
		}{
			{"already approved", "approved", true, "", 409},
			{"declined", "declined", true, "", 409},
			{"no line items", "draft", false, "", 400},
			{"malformed id", "draft", true, "nope", 400},
			{"unknown id", "draft", true, uid("ff"), 404},
		}
		for _, tt := range tests {
			store := newMemDocs()
			var d *model.CustomerDocument
			if tt.items {
				d = store.put(tt.status, oneItem())
			} else {
				d = store.put(tt.status)
			}
			id := tt.id
			if id == "" {
				id = d.ID
			}
			jobs := &fakeJobCreator{}
			_, err := newEstimateService(store, jobs).Approve(ctx, id, service.ApproveEstimateInput{StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-12")})
			if statusOf(t, err) != tt.want {
				t.Errorf("%s: %v", tt.name, err)
			}
			if !jobs.in.Date.IsZero() || d.JobID != nil {
				t.Errorf("%s: work was started", tt.name)
			}
		}
	})

	t.Run("a job error is returned and the estimate stays unapproved", func(t *testing.T) {
		store := newMemDocs()
		d := store.put("sent", oneItem())
		bad := apperror.Validation("bad recurrence")
		_, err := newEstimateService(store, &fakeJobCreator{err: bad}).Approve(ctx, d.ID, service.ApproveEstimateInput{StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-12")})
		if !errors.Is(err, bad) || d.Status != "sent" || d.JobID != nil {
			t.Fatalf("err=%v doc=%+v", err, d)
		}
	})

	t.Run("losing the race is a 409, a store error is returned as is", func(t *testing.T) {
		store := newMemDocs()
		d := store.put("sent", oneItem())
		s := newEstimateService(store, &fakeJobCreator{})
		store.zeroRows = true
		if _, err := s.Approve(ctx, d.ID, service.ApproveEstimateInput{StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-12")}); statusOf(t, err) != 409 {
			t.Fatalf("race: %v", err)
		}
		boom := errors.New("db down")
		store.zeroRows, store.updateErr = false, boom
		if _, err := s.Approve(ctx, d.ID, service.ApproveEstimateInput{StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-12")}); !errors.Is(err, boom) {
			t.Fatalf("store error: %v", err)
		}
		store.updateErr, store.getErr = nil, boom
		if _, err := s.Approve(ctx, d.ID, service.ApproveEstimateInput{StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-12")}); !errors.Is(err, boom) {
			t.Fatalf("get error: %v", err)
		}
	})
}

// newEstimateService pins "today" to 2026-10-05, so 2026-10-12 is in the future
// however late the tests run.
func newEstimateService(store service.EstimateStore, jobs service.JobCreator) *service.EstimateService {
	return service.NewEstimateService(store, jobs, &fakeRefs{}).WithClock(func() time.Time {
		return dt("2026-10-05").Add(9 * time.Hour)
	})
}

type fakePaid struct {
	paid  bool
	err   error
	asked []string
}

func (f *fakePaid) HasPaidForJob(_ context.Context, jobID string) (bool, error) {
	f.asked = append(f.asked, jobID)
	return f.paid, f.err
}

func TestEstimateUpdate(t *testing.T) {
	ctx := context.Background()
	str := func(s string) *string { return &s }
	jobID := uid("j1")
	// putEstimate makes an estimate in the given status; an approved one is tied to a job.
	putEstimate := func(store *memDocs, status string) *model.CustomerDocument {
		d := store.put(status, oneItem())
		if status == "approved" {
			d.JobID = &jobID
		}
		return d
	}

	t.Run("an estimate can be edited while draft, sent or approved", func(t *testing.T) {
		for _, status := range []string{"draft", "sent", "approved"} {
			store := newMemDocs()
			d := putEstimate(store, status)
			got, err := newEstimateService(store, &fakeJobCreator{}).WithInvoices(&fakePaid{}).Update(ctx, d.ID, service.DocumentPatch{Notes: str("changed")})
			if err != nil || got.Notes != "changed" || got.Status != status {
				t.Errorf("%s: got %+v err=%v", status, got, err)
			}
		}
	})

	t.Run("editing keeps the status and the job tie of an approved estimate", func(t *testing.T) {
		store := newMemDocs()
		d := putEstimate(store, "approved")
		items := []service.LineItemInput{{Description: "New scope", Quantity: 1, UnitPriceCents: 9900}}
		got, err := newEstimateService(store, &fakeJobCreator{}).WithInvoices(&fakePaid{}).Update(ctx, d.ID, service.DocumentPatch{LineItems: &items})
		if err != nil || got.Status != "approved" || got.JobID == nil || *got.JobID != jobID || got.TotalCents() != 9900 {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("a declined estimate is final", func(t *testing.T) {
		store := newMemDocs()
		d := store.put("declined", oneItem())
		if _, err := newEstimateService(store, &fakeJobCreator{}).Update(ctx, d.ID, service.DocumentPatch{Notes: str("x")}); statusOf(t, err) != 409 {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("once an invoice for the job is paid the estimate is closed", func(t *testing.T) {
		store := newMemDocs()
		d := putEstimate(store, "approved")
		paid := &fakePaid{paid: true}
		_, err := newEstimateService(store, &fakeJobCreator{}).WithInvoices(paid).Update(ctx, d.ID, service.DocumentPatch{Notes: str("late")})
		if statusOf(t, err) != 409 || d.Notes != "" {
			t.Fatalf("err=%v notes=%q", err, d.Notes)
		}
		if len(paid.asked) != 1 || paid.asked[0] != jobID {
			t.Fatalf("asked about %v", paid.asked)
		}
	})

	t.Run("a recurring job's estimate is never closed by a paid invoice", func(t *testing.T) {
		store := newMemDocs()
		d := putEstimate(store, "approved")
		d.JobSnapshot = &model.JobSnapshot{ID: jobID, Recurrence: &recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{1}}}
		paid := &fakePaid{paid: true}
		got, err := newEstimateService(store, &fakeJobCreator{}).WithInvoices(paid).Update(ctx, d.ID, service.DocumentPatch{Notes: str("still editable")})
		if err != nil || got.Notes != "still editable" {
			t.Fatalf("err=%v got=%+v", err, got)
		}
		if len(paid.asked) != 0 {
			t.Fatalf("a recurring estimate needs no paid check, asked %v", paid.asked)
		}
	})

	t.Run("a one-off job's estimate is closed once its invoice is paid", func(t *testing.T) {
		store := newMemDocs()
		d := putEstimate(store, "approved")
		d.JobSnapshot = &model.JobSnapshot{ID: jobID} // no recurrence
		_, err := newEstimateService(store, &fakeJobCreator{}).WithInvoices(&fakePaid{paid: true}).Update(ctx, d.ID, service.DocumentPatch{Notes: str("late")})
		if statusOf(t, err) != 409 {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("an estimate with no job yet is never checked against invoices", func(t *testing.T) {
		store := newMemDocs()
		d := store.put("sent", oneItem())
		paid := &fakePaid{paid: true}
		if _, err := newEstimateService(store, &fakeJobCreator{}).WithInvoices(paid).Update(ctx, d.ID, service.DocumentPatch{Notes: str("x")}); err != nil || len(paid.asked) != 0 {
			t.Fatalf("err=%v asked=%v", err, paid.asked)
		}
	})

	t.Run("an error from the paid check is returned as is and nothing changes", func(t *testing.T) {
		store := newMemDocs()
		d := putEstimate(store, "approved")
		boom := errors.New("db down")
		if _, err := newEstimateService(store, &fakeJobCreator{}).WithInvoices(&fakePaid{err: boom}).Update(ctx, d.ID, service.DocumentPatch{Notes: str("x")}); !errors.Is(err, boom) || d.Notes != "" {
			t.Fatalf("err=%v notes=%q", err, d.Notes)
		}
	})

	t.Run("a sent or approved estimate must keep something to bill", func(t *testing.T) {
		empty := []service.LineItemInput{}
		free := []service.LineItemInput{{Description: "Free", Quantity: 1, UnitPriceCents: 0}}
		for _, status := range []string{"sent", "approved"} {
			for name, items := range map[string]*[]service.LineItemInput{"no items": &empty, "only free items": &free} {
				store := newMemDocs()
				d := putEstimate(store, status)
				if _, err := newEstimateService(store, &fakeJobCreator{}).Update(ctx, d.ID, service.DocumentPatch{LineItems: items}); statusOf(t, err) != 400 {
					t.Errorf("%s/%s: %v", status, name, err)
				}
				if len(d.LineItems) != 1 {
					t.Errorf("%s/%s: items changed to %+v", status, name, d.LineItems)
				}
			}
		}
		// A draft is still being written, so it may be emptied.
		store := newMemDocs()
		d := store.put("draft", oneItem())
		if got, err := newEstimateService(store, &fakeJobCreator{}).Update(ctx, d.ID, service.DocumentPatch{LineItems: &empty}); err != nil || len(got.LineItems) != 0 {
			t.Fatalf("draft: got %+v err=%v", got, err)
		}
	})

	t.Run("the edit runs on the locked row, in a transaction", func(t *testing.T) {
		store := newMemDocs()
		d := store.put("sent", oneItem())
		if _, err := newEstimateService(store, &fakeJobCreator{}).Update(ctx, d.ID, service.DocumentPatch{Notes: str("x")}); err != nil {
			t.Fatal(err)
		}
		if store.locked != 1 || store.transacted != 1 {
			t.Fatalf("locked=%d transacted=%d", store.locked, store.transacted)
		}
	})
}

func TestEstimateNeedsPricedItemsToSendOrApprove(t *testing.T) {
	ctx := context.Background()
	free := model.CustomerLineItem{Description: "Free", Quantity: 1, UnitPriceCents: 0}

	for name, items := range map[string][]model.CustomerLineItem{"no items": nil, "only free items": {free, free}} {
		store := newMemDocs()
		d := store.put("draft", items...)
		if _, err := newEstimateService(store, &fakeJobCreator{}).ChangeStatus(ctx, d.ID, "sent"); statusOf(t, err) != 400 || d.Status != "draft" {
			t.Errorf("send with %s: %v status=%s", name, err, d.Status)
		}
		jobs := &fakeJobCreator{}
		if _, err := newEstimateService(store, jobs).Approve(ctx, d.ID, service.ApproveEstimateInput{StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-12")}); statusOf(t, err) != 400 || d.Status != "draft" || !jobs.in.Date.IsZero() {
			t.Errorf("approve with %s: %v status=%s", name, err, d.Status)
		}
	}

	// One priced line is enough, even next to free ones.
	store := newMemDocs()
	d := store.put("draft", free, oneItem())
	if got, err := newEstimateService(store, &fakeJobCreator{}).ChangeStatus(ctx, d.ID, "sent"); err != nil || got.Status != "sent" {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestApproveRunsOnTheLockedRow(t *testing.T) {
	store := newMemDocs()
	d := store.put("sent", oneItem())
	if _, err := newEstimateService(store, &fakeJobCreator{}).Approve(context.Background(), d.ID, service.ApproveEstimateInput{StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-12")}); err != nil {
		t.Fatal(err)
	}
	if store.locked != 1 || store.transacted != 1 {
		t.Fatalf("locked=%d transacted=%d", store.locked, store.transacted)
	}
}

type fakeToday struct {
	day time.Time
	err error
}

func (f fakeToday) Today(context.Context) (time.Time, error) { return f.day, f.err }

func TestEstimateApproveUsesTheTenantsToday(t *testing.T) {
	ctx := context.Background()
	// The clock says it is 2026-10-05 in UTC; the tenant's calendar disagrees.
	approve := func(today fakeToday, date string) error {
		store := newMemDocs()
		d := store.put("sent", oneItem())
		s := newEstimateService(store, &fakeJobCreator{}).WithToday(today)
		_, err := s.Approve(ctx, d.ID, service.ApproveEstimateInput{StartTime: "09:00", LengthMinutes: 60, Date: dt(date)})
		return err
	}
	// UTC+14: already the 6th, so the 5th is yesterday there.
	ahead := fakeToday{day: dt("2026-10-06")}
	if got := statusOf(t, approve(ahead, "2026-10-05")); got != 400 {
		t.Errorf("UTC today but local yesterday: status %d, want 400", got)
	}
	if err := approve(ahead, "2026-10-06"); err != nil {
		t.Errorf("local today: %v", err)
	}
	// UTC-8: still the 4th, so the 4th is allowed although UTC is on the 5th.
	behind := fakeToday{day: dt("2026-10-04")}
	if err := approve(behind, "2026-10-04"); err != nil {
		t.Errorf("local today behind UTC: %v", err)
	}
	if got := statusOf(t, approve(behind, "2026-10-03")); got != 400 {
		t.Errorf("local yesterday behind UTC: status %d, want 400", got)
	}
	boom := errors.New("boom")
	if err := approve(fakeToday{err: boom}, "2026-10-12"); !errors.Is(err, boom) {
		t.Errorf("provider failure: %v", err)
	}
}

// fakeJobRemover records the job lock and delete, in order, next to the
// estimate store's own events.
type fakeJobRemover struct {
	events    *[]string
	lockErr   error
	deleteErr error
	locked    []string
	deleted   []string
}

func (f *fakeJobRemover) LockJob(_ context.Context, id string) error {
	*f.events = append(*f.events, "lock-job")
	f.locked = append(f.locked, id)
	return f.lockErr
}

func (f *fakeJobRemover) Delete(_ context.Context, id string) error {
	*f.events = append(*f.events, "delete-job")
	f.deleted = append(f.deleted, id)
	return f.deleteErr
}

type fakeOccRows struct {
	rows []model.JobOccurrence
	err  error
}

func (f fakeOccRows) ListByJob(context.Context, string) ([]model.JobOccurrence, error) {
	return f.rows, f.err
}

func TestEstimateEditsKeepRevisions(t *testing.T) {
	ctx := context.Background()
	str := func(s string) *string { return &s }
	approved := func(store *memDocs) *model.CustomerDocument {
		d := store.put("approved", oneItem())
		jobID := uid("j1")
		d.JobID = &jobID
		d.Notes = "v1"
		return d
	}

	t.Run("editing a sent estimate keeps what it said and moves the counter", func(t *testing.T) {
		store := newMemDocs()
		d := store.put("sent", oneItem())
		d.Notes = "original"
		items := []service.LineItemInput{{Description: "New", Quantity: 3, UnitPriceCents: 100}}
		if _, err := newEstimateService(store, &fakeJobCreator{}).Update(ctx, d.ID, service.DocumentPatch{Notes: str("changed"), LineItems: &items}); err != nil {
			t.Fatal(err)
		}
		if len(store.revisions) != 1 {
			t.Fatalf("revisions %+v", store.revisions)
		}
		rev := store.revisions[0]
		want := model.RevisionContent{
			Notes: "original", CustomerID: refCustomer, LocationID: refLocation, ServiceTypeID: refService,
			LineItems: []model.RevisionLine{{Description: "Clean", Quantity: 2, UnitPriceCents: 500}},
		}
		if rev.DocumentID != d.ID || rev.Revision != 1 || !reflect.DeepEqual(rev.Content, want) {
			t.Fatalf("revision %+v, want content %+v", rev, want)
		}
		if d.Revision != 2 || d.Notes != "changed" {
			t.Fatalf("doc revision=%d notes=%q", d.Revision, d.Notes)
		}
	})

	t.Run("each edit of an approved estimate adds a revision, newest first", func(t *testing.T) {
		store := newMemDocs()
		d := approved(store)
		s := newEstimateService(store, &fakeJobCreator{})
		for _, n := range []string{"v2", "v3"} {
			if _, err := s.Update(ctx, d.ID, service.DocumentPatch{Notes: str(n)}); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.Revisions(ctx, d.ID)
		if err != nil || len(got) != 2 || got[0].Revision != 2 || got[0].Content.Notes != "v2" || got[1].Revision != 1 || got[1].Content.Notes != "v1" {
			t.Fatalf("revisions %+v err=%v", got, err)
		}
		if d.Revision != 3 {
			t.Fatalf("revision counter %d, want 3", d.Revision)
		}
	})

	t.Run("a draft was never shown, so editing it leaves no trace", func(t *testing.T) {
		store := newMemDocs()
		d := store.put("draft", oneItem())
		if _, err := newEstimateService(store, &fakeJobCreator{}).Update(ctx, d.ID, service.DocumentPatch{Notes: str("x")}); err != nil {
			t.Fatal(err)
		}
		if len(store.revisions) != 0 || d.Revision != 1 {
			t.Fatalf("revisions=%v revision=%d", store.revisions, d.Revision)
		}
	})

	t.Run("a refused edit keeps nothing", func(t *testing.T) {
		store := newMemDocs()
		d := approved(store)
		_, err := newEstimateService(store, &fakeJobCreator{}).WithInvoices(&fakePaid{paid: true}).Update(ctx, d.ID, service.DocumentPatch{Notes: str("late")})
		if statusOf(t, err) != 409 || len(store.revisions) != 0 || d.Revision != 1 {
			t.Fatalf("err=%v revisions=%v revision=%d", err, store.revisions, d.Revision)
		}
		// A closed estimate (declined) is refused before anything is read.
		declined := store.put("declined", oneItem())
		if _, err := newEstimateService(store, &fakeJobCreator{}).Update(ctx, declined.ID, service.DocumentPatch{Notes: str("x")}); statusOf(t, err) != 409 || len(store.revisions) != 0 {
			t.Fatalf("declined: err=%v revisions=%v", err, store.revisions)
		}
	})

	t.Run("a failure to keep the revision stops the edit", func(t *testing.T) {
		store := newMemDocs()
		d := approved(store)
		store.revisionErr = errors.New("db down")
		if _, err := newEstimateService(store, &fakeJobCreator{}).Update(ctx, d.ID, service.DocumentPatch{Notes: str("x")}); !errors.Is(err, store.revisionErr) {
			t.Fatalf("got %v", err)
		}
		if d.Notes != "v1" || d.Revision != 1 {
			t.Fatalf("the estimate changed: notes=%q revision=%d", d.Notes, d.Revision)
		}
	})

	t.Run("Revisions: never nil, 400 for a bad id, 404 for an unknown estimate", func(t *testing.T) {
		store := newMemDocs()
		d := store.put("sent", oneItem())
		s := newEstimateService(store, &fakeJobCreator{})
		if got, err := s.Revisions(ctx, d.ID); err != nil || got == nil || len(got) != 0 {
			t.Fatalf("got %#v err=%v", got, err)
		}
		if _, err := s.Revisions(ctx, "nope"); statusOf(t, err) != 400 {
			t.Fatalf("malformed: %v", err)
		}
		if _, err := s.Revisions(ctx, uid("ff")); statusOf(t, err) != 404 {
			t.Fatalf("unknown: %v", err)
		}
	})
}

// fakeWorkOrderCounter answers how many work orders a job has.
type fakeWorkOrderCounter struct {
	n   int64
	err error
}

func (f fakeWorkOrderCounter) CountForJob(context.Context, string) (int64, error) { return f.n, f.err }

func TestEstimateReopen(t *testing.T) {
	ctx := context.Background()
	approved := func(store *memDocs) *model.CustomerDocument {
		d := store.put("approved", oneItem())
		jobID := uid("j1")
		d.JobID = &jobID
		d.JobSnapshot = &model.JobSnapshot{ID: jobID}
		return d
	}
	// setup wires an estimate service whose job lock/delete and checks are fakes.
	setup := func(store *memDocs, occs fakeOccRows) (*service.EstimateService, *fakeJobRemover) {
		jobs := &fakeJobRemover{events: &store.events}
		return newEstimateService(store, &fakeJobCreator{}).WithReopen(jobs, occs, store), jobs
	}

	t.Run("undoes a fresh approval: the job is gone and the estimate is sent again", func(t *testing.T) {
		store := newMemDocs()
		d := approved(store)
		s, jobs := setup(store, fakeOccRows{})
		got, err := s.Reopen(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "sent" || got.JobID != nil || got.JobSnapshot != nil {
			t.Fatalf("estimate %+v", got)
		}
		if !reflect.DeepEqual(jobs.deleted, []string{uid("j1")}) || store.transacted != 1 || store.locked != 1 {
			t.Fatalf("deleted=%v transacted=%d locked=%d", jobs.deleted, store.transacted, store.locked)
		}
		if want := []string{"begin", "lock-job", "reopen", "delete-job"}; !reflect.DeepEqual(store.events, want) {
			t.Fatalf("order %v, want %v (the job is locked before it is judged, and deleted only after the estimate let go of it)", store.events, want)
		}
		if !reflect.DeepEqual(store.lastAllowed, []string{"approved"}) {
			t.Fatalf("guard %v", store.lastAllowed)
		}
	})

	t.Run("refusals change nothing", func(t *testing.T) {
		tests := []struct {
			name  string
			setup func(*memDocs) (id string, occs fakeOccRows)
			want  int
		}{
			{"a draft", func(s *memDocs) (string, fakeOccRows) { return s.put("draft", oneItem()).ID, fakeOccRows{} }, 409},
			{"a sent estimate", func(s *memDocs) (string, fakeOccRows) { return s.put("sent", oneItem()).ID, fakeOccRows{} }, 409},
			{"a declined estimate", func(s *memDocs) (string, fakeOccRows) { return s.put("declined", oneItem()).ID, fakeOccRows{} }, 409},
			{"an occurrence was changed", func(s *memDocs) (string, fakeOccRows) {
				return approved(s).ID, fakeOccRows{rows: []model.JobOccurrence{{Status: "confirmed"}}}
			}, 409},
			{"an invoice exists (any status)", func(s *memDocs) (string, fakeOccRows) {
				s.countN = 1
				return approved(s).ID, fakeOccRows{}
			}, 409},
			{"unknown estimate", func(*memDocs) (string, fakeOccRows) { return uid("ff"), fakeOccRows{} }, 404},
			{"malformed id", func(*memDocs) (string, fakeOccRows) { return "nope", fakeOccRows{} }, 400},
		}
		for _, tt := range tests {
			store := newMemDocs()
			id, occs := tt.setup(store)
			s, jobs := setup(store, occs)
			if _, err := s.Reopen(ctx, id); statusOf(t, err) != tt.want {
				t.Errorf("%s: %v", tt.name, err)
			}
			if len(jobs.deleted) != 0 || len(store.reopened) != 0 {
				t.Errorf("%s: something changed (deleted=%v reopened=%v)", tt.name, jobs.deleted, store.reopened)
			}
		}
	})

	t.Run("store errors come back as is and nothing is deleted", func(t *testing.T) {
		boom := errors.New("db down")
		tests := map[string]func(*memDocs, *fakeJobRemover) fakeOccRows{
			"lock fails":        func(_ *memDocs, j *fakeJobRemover) fakeOccRows { j.lockErr = boom; return fakeOccRows{} },
			"occurrence lookup": func(*memDocs, *fakeJobRemover) fakeOccRows { return fakeOccRows{err: boom} },
			"invoice count":     func(s *memDocs, _ *fakeJobRemover) fakeOccRows { s.occErr = boom; return fakeOccRows{} },
			"estimate update":   func(s *memDocs, _ *fakeJobRemover) fakeOccRows { s.updateErr = boom; return fakeOccRows{} },
		}
		for name, arrange := range tests {
			store := newMemDocs()
			d := approved(store)
			jobs := &fakeJobRemover{events: &store.events}
			occs := arrange(store, jobs)
			s := newEstimateService(store, &fakeJobCreator{}).WithReopen(jobs, occs, store)
			if _, err := s.Reopen(ctx, d.ID); !errors.Is(err, boom) {
				t.Errorf("%s: %v", name, err)
			}
			if len(jobs.deleted) != 0 {
				t.Errorf("%s: the job was deleted", name)
			}
		}
	})

	t.Run("a work order of the job (any status) blocks it; a failing count comes back as is", func(t *testing.T) {
		boom := errors.New("db down")
		for name, c := range map[string]struct {
			counter fakeWorkOrderCounter
			want    int
			isErr   error
		}{
			"has work orders": {fakeWorkOrderCounter{n: 1}, 409, nil},
			"none":            {fakeWorkOrderCounter{}, 0, nil},
			"count fails":     {fakeWorkOrderCounter{err: boom}, 0, boom},
		} {
			store := newMemDocs()
			d := approved(store)
			s, jobs := setup(store, fakeOccRows{})
			s.WithWorkOrders(c.counter)
			got, err := s.Reopen(ctx, d.ID)
			switch {
			case c.isErr != nil:
				if !errors.Is(err, c.isErr) || len(jobs.deleted) != 0 {
					t.Errorf("%s: err=%v deleted=%v", name, err, jobs.deleted)
				}
			case c.want != 0:
				if statusOf(t, err) != c.want || len(jobs.deleted) != 0 || len(store.reopened) != 0 {
					t.Errorf("%s: err=%v deleted=%v reopened=%v", name, err, jobs.deleted, store.reopened)
				}
			default:
				if err != nil || got.Status != "sent" || len(jobs.deleted) != 1 {
					t.Errorf("%s: err=%v got=%+v deleted=%v", name, err, got, jobs.deleted)
				}
			}
		}
	})

	t.Run("a failure while deleting the job comes back as is", func(t *testing.T) {
		store := newMemDocs()
		d := approved(store)
		s, jobs := setup(store, fakeOccRows{})
		jobs.deleteErr = errors.New("fk")
		if _, err := s.Reopen(ctx, d.ID); !errors.Is(err, jobs.deleteErr) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("losing the race to another change is a 409", func(t *testing.T) {
		store := newMemDocs()
		d := approved(store)
		store.zeroRows = true
		s, jobs := setup(store, fakeOccRows{})
		if _, err := s.Reopen(ctx, d.ID); statusOf(t, err) != 409 || len(jobs.deleted) != 0 {
			t.Fatalf("err=%v deleted=%v", err, jobs.deleted)
		}
	})

	t.Run("without the wiring it fails instead of guessing", func(t *testing.T) {
		store := newMemDocs()
		d := approved(store)
		if _, err := newEstimateService(store, &fakeJobCreator{}).Reopen(ctx, d.ID); err == nil {
			t.Fatal("expected an error")
		}
	})
}
