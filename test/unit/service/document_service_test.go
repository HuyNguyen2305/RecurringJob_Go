package service_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

// memDocs is an in-memory service.DocumentStore / service.EstimateStore.
type memDocs struct {
	docs         map[string]*model.CustomerDocument
	seq          int
	createErr    error
	getErr       error
	updateErr    error
	zeroRows     bool // report 0 rows affected, as if another request won
	transacted   int
	locked       int // GetForUpdate calls
	voidCalls    []voidCall
	voidedN      int64
	paidIDs      []string
	hasPaid      bool
	occErr       error
	idsStatus    string
	existsStatus string
	lastAllowed  []string
	listArgs     struct {
		status        string
		limit, offset int
	}
}

func newMemDocs() *memDocs { return &memDocs{docs: map[string]*model.CustomerDocument{}} }

func (m *memDocs) put(status string, items ...model.CustomerLineItem) *model.CustomerDocument {
	m.seq++
	d := &model.CustomerDocument{ID: uid(fmt.Sprintf("%02d", m.seq)), Status: status, CustomerID: refCustomer, LocationID: refLocation, ServiceTypeID: refService, LineItems: items}
	m.docs[d.ID] = d
	return d
}

func oneItem() model.CustomerLineItem {
	return model.CustomerLineItem{Description: "Clean", Quantity: 2, UnitPriceCents: 500}
}

func (m *memDocs) Create(_ context.Context, d *model.CustomerDocument) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.seq++
	d.ID = uid(fmt.Sprintf("%02d", m.seq))
	m.docs[d.ID] = d
	return nil
}

func (m *memDocs) Get(_ context.Context, id string) (*model.CustomerDocument, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	d, ok := m.docs[id]
	if !ok {
		return nil, apperror.NotFound("document not found")
	}
	cp := *d
	return &cp, nil
}

func (m *memDocs) List(_ context.Context, status string, limit, offset int) ([]model.CustomerDocument, error) {
	m.listArgs.status, m.listArgs.limit, m.listArgs.offset = status, limit, offset
	return []model.CustomerDocument{}, nil
}

func (m *memDocs) UpdateContent(_ context.Context, id string, allowedFrom []string, fields map[string]any, items []model.CustomerLineItem) (int64, error) {
	m.lastAllowed = allowedFrom
	if m.updateErr != nil {
		return 0, m.updateErr
	}
	d := m.docs[id]
	if m.zeroRows || d == nil || !hasString(allowedFrom, d.Status) {
		return 0, nil
	}
	if v, ok := fields["customer_id"]; ok {
		d.CustomerID, d.LocationID, d.ServiceTypeID = v.(string), fields["location_id"].(string), fields["service_type_id"].(string)
	}
	if v, ok := fields["notes"]; ok {
		d.Notes = v.(string)
	}
	if items != nil {
		d.LineItems = items
	}
	return 1, nil
}

func (m *memDocs) UpdateStatusGuarded(_ context.Context, id string, allowedFrom []string, updates map[string]any) (int64, error) {
	m.lastAllowed = allowedFrom
	if m.updateErr != nil {
		return 0, m.updateErr
	}
	d := m.docs[id]
	if m.zeroRows || d == nil || !hasString(allowedFrom, d.Status) {
		return 0, nil
	}
	d.Status = updates["status"].(string)
	return 1, nil
}

func (m *memDocs) MarkApproved(_ context.Context, id string, allowedFrom []string, status, jobID string, snap *model.JobSnapshot) (int64, error) {
	if m.updateErr != nil {
		return 0, m.updateErr
	}
	d := m.docs[id]
	if m.zeroRows || d == nil || !hasString(allowedFrom, d.Status) {
		return 0, nil
	}
	d.Status, d.JobID, d.JobSnapshot = status, &jobID, snap
	return 1, nil
}

func (m *memDocs) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	m.transacted++
	return fn(ctx)
}

func hasString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// newDocCore returns the shared behaviour through an invoice service, which
// embeds it unchanged.
func newDocCore(store *memDocs) *service.InvoiceService {
	return service.NewInvoiceService(store, mockJobs{}, nopAvailability{})
}

type nopAvailability struct{}

func (nopAvailability) AvailableFor(context.Context, string, time.Time) error { return nil }

func TestDocumentGet(t *testing.T) {
	ctx := context.Background()
	store := newMemDocs()
	d := store.put("draft", oneItem())
	s := newDocCore(store)

	if got, err := s.Get(ctx, d.ID); err != nil || got.ID != d.ID {
		t.Fatalf("got %+v err=%v", got, err)
	}
	for name, c := range map[string]struct {
		id     string
		status int
	}{
		"malformed id": {"nope", 400},
		"unknown id":   {uid("ff"), 404},
	} {
		if _, err := s.Get(ctx, c.id); statusOf(t, err) != c.status {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDocumentList(t *testing.T) {
	ctx := context.Background()
	store := newMemDocs()
	s := newDocCore(store)

	if _, err := s.List(ctx, service.DocumentListQuery{}); err != nil || store.listArgs.limit != service.DefaultDocumentLimit || store.listArgs.offset != 0 {
		t.Fatalf("defaults: %+v err=%v", store.listArgs, err)
	}
	if _, err := s.List(ctx, service.DocumentListQuery{Status: "paid", Limit: 5, Offset: 10}); err != nil || store.listArgs.status != "paid" || store.listArgs.limit != 5 || store.listArgs.offset != 10 {
		t.Fatalf("args: %+v err=%v", store.listArgs, err)
	}
	for name, q := range map[string]service.DocumentListQuery{
		"unknown status":  {Status: "approved"}, // an estimate status, not an invoice one
		"limit too large": {Limit: service.MaxDocumentLimit + 1},
		"negative limit":  {Limit: -1},
		"negative offset": {Offset: -1},
	} {
		if _, err := s.List(ctx, q); statusOf(t, err) != 400 {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDocumentUpdate(t *testing.T) {
	ctx := context.Background()
	str := func(s string) *string { return &s }

	t.Run("edits a draft and replaces line items", func(t *testing.T) {
		store := newMemDocs()
		d := store.put("draft", oneItem())
		s := newDocCore(store)
		items := []service.LineItemInput{{Description: " A ", Quantity: 1, UnitPriceCents: 100}, {Description: "B", Quantity: 3, UnitPriceCents: 0}}
		got, err := s.Update(ctx, d.ID, service.DocumentPatch{Notes: str("updated"), LineItems: &items})
		if err != nil || got.Notes != "updated" || len(got.LineItems) != 2 || got.LineItems[0].Description != "A" || got.LineItems[1].Position != 1 {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("an empty line item list clears the items", func(t *testing.T) {
		store := newMemDocs()
		d := store.put("draft", oneItem())
		empty := []service.LineItemInput{}
		got, err := newDocCore(store).Update(ctx, d.ID, service.DocumentPatch{LineItems: &empty})
		if err != nil || len(got.LineItems) != 0 {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("rejections change nothing", func(t *testing.T) {
		tests := []struct {
			name   string
			status string
			id     string
			patch  service.DocumentPatch
			want   int
		}{
			{"not a draft", "sent", "", service.DocumentPatch{Notes: str("x")}, 409},
			{"empty patch", "draft", "", service.DocumentPatch{}, 400},
			{"notes too long", "draft", "", service.DocumentPatch{Notes: str(strings.Repeat("n", service.MaxNotesLen+1))}, 400},
			{"customer ids on an invoice", "draft", "", service.DocumentPatch{CustomerID: str(refCustomer)}, 400},
			{"line item without a price", "draft", "", service.DocumentPatch{LineItems: &[]service.LineItemInput{{Description: "x", Quantity: 0}}}, 400},
			{"malformed id", "draft", "nope", service.DocumentPatch{Notes: str("x")}, 400},
			{"unknown id", "draft", uid("ff"), service.DocumentPatch{Notes: str("x")}, 404},
		}
		for _, tt := range tests {
			store := newMemDocs()
			d := store.put(tt.status, oneItem())
			id := tt.id
			if id == "" {
				id = d.ID
			}
			if _, err := newDocCore(store).Update(ctx, id, tt.patch); statusOf(t, err) != tt.want {
				t.Errorf("%s: %v", tt.name, err)
			}
			if d.Notes != "" || d.CustomerID != refCustomer || len(d.LineItems) != 1 {
				t.Errorf("%s: document changed: %+v", tt.name, d)
			}
		}
	})

	t.Run("losing the race is a 409, a store error is returned as is", func(t *testing.T) {
		store := newMemDocs()
		d := store.put("draft", oneItem())
		store.zeroRows = true
		if _, err := newDocCore(store).Update(ctx, d.ID, service.DocumentPatch{Notes: str("x")}); statusOf(t, err) != 409 {
			t.Fatalf("race: %v", err)
		}
		boom := errors.New("db down")
		store.zeroRows, store.updateErr = false, boom
		if _, err := newDocCore(store).Update(ctx, d.ID, service.DocumentPatch{Notes: str("x")}); !errors.Is(err, boom) {
			t.Fatalf("store error: %v", err)
		}
	})
}

func TestDocumentChangeStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("allowed transitions", func(t *testing.T) {
		store := newMemDocs()
		d := store.put("draft", oneItem())
		s := newDocCore(store)
		for _, to := range []string{"sent", "paid"} {
			got, err := s.ChangeStatus(ctx, d.ID, to)
			if err != nil || got.Status != to {
				t.Fatalf("-> %s: %+v err=%v", to, got, err)
			}
		}
	})

	t.Run("rejections", func(t *testing.T) {
		tests := []struct {
			name, from, to string
			items          bool
			want           int
		}{
			{"unknown status", "draft", "bogus", true, 400},
			{"estimate-only status on an invoice", "draft", "approved", true, 400},
			{"skipping sent", "draft", "paid", true, 409},
			{"leaving a final status", "paid", "void", true, 409},
			{"same status", "draft", "draft", true, 409},
			{"sending with no line items", "draft", "sent", false, 400},
		}
		for _, tt := range tests {
			store := newMemDocs()
			var d *model.CustomerDocument
			if tt.items {
				d = store.put(tt.from, oneItem())
			} else {
				d = store.put(tt.from)
			}
			if _, err := newDocCore(store).ChangeStatus(ctx, d.ID, tt.to); statusOf(t, err) != tt.want {
				t.Errorf("%s: %v", tt.name, err)
			}
			if d.Status != tt.from {
				t.Errorf("%s: status changed to %s", tt.name, d.Status)
			}
		}
	})

	t.Run("the update is guarded by the allowed source statuses", func(t *testing.T) {
		store := newMemDocs()
		d := store.put("sent", oneItem())
		if _, err := newDocCore(store).ChangeStatus(ctx, d.ID, "void"); err != nil {
			t.Fatal(err)
		}
		if !hasString(store.lastAllowed, "draft") || !hasString(store.lastAllowed, "sent") || len(store.lastAllowed) != 2 {
			t.Fatalf("allowedFrom %v", store.lastAllowed)
		}
	})

	t.Run("losing the race is a 409, a store error is returned as is", func(t *testing.T) {
		store := newMemDocs()
		d := store.put("draft", oneItem())
		store.zeroRows = true
		if _, err := newDocCore(store).ChangeStatus(ctx, d.ID, "sent"); statusOf(t, err) != 409 {
			t.Fatalf("race: %v", err)
		}
		boom := errors.New("db down")
		store.zeroRows, store.updateErr = false, boom
		if _, err := newDocCore(store).ChangeStatus(ctx, d.ID, "sent"); !errors.Is(err, boom) {
			t.Fatalf("store error: %v", err)
		}
		store.getErr = boom
		if _, err := newDocCore(store).ChangeStatus(ctx, d.ID, "sent"); !errors.Is(err, boom) {
			t.Fatalf("get error: %v", err)
		}
	})

	t.Run("malformed and unknown ids", func(t *testing.T) {
		s := newDocCore(newMemDocs())
		if _, err := s.ChangeStatus(ctx, "nope", "sent"); statusOf(t, err) != 400 {
			t.Errorf("malformed: %v", err)
		}
		if _, err := s.ChangeStatus(ctx, uid("ff"), "sent"); statusOf(t, err) != 404 {
			t.Errorf("unknown: %v", err)
		}
	})
}

// GetForUpdate is Get: the in-memory fake has no concurrent writers to lock out.
func (m *memDocs) GetForUpdate(ctx context.Context, id string) (*model.CustomerDocument, error) {
	m.locked++
	return m.Get(ctx, id)
}

// voidCall records one UpdateStatusForOccurrence call.
type voidCall struct {
	jobID       string
	date        time.Time
	allowedFrom []string
	status      string
}

func (m *memDocs) UpdateStatusForOccurrence(_ context.Context, jobID string, date time.Time, allowedFrom []string, status string) (int64, error) {
	m.voidCalls = append(m.voidCalls, voidCall{jobID, date, allowedFrom, status})
	return m.voidedN, m.occErr
}

func (m *memDocs) IDsForOccurrence(_ context.Context, _ string, _ time.Time, status string) ([]string, error) {
	m.idsStatus = status
	return m.paidIDs, m.occErr
}

func (m *memDocs) ExistsForJob(_ context.Context, _, status string) (bool, error) {
	m.existsStatus = status
	return m.hasPaid, m.occErr
}

// Ids the reference fake hands out.
var (
	refCustomer = uid("c1")
	refLocation = uid("c2")
	refService  = uid("c3")
)

// fakeRefs is a service.ReferenceResolver that accepts any ids, or fails.
type fakeRefs struct {
	err   error
	calls [][3]string
}

func (f *fakeRefs) Resolve(_ context.Context, customerID, locationID, serviceTypeID string) (*service.Refs, error) {
	f.calls = append(f.calls, [3]string{customerID, locationID, serviceTypeID})
	if f.err != nil {
		return nil, f.err
	}
	return &service.Refs{
		Customer:    &model.Customer{ID: customerID, Name: "Ada"},
		Location:    &model.Location{ID: locationID, CustomerID: customerID, AddressLine1: "1 Main Street"},
		ServiceType: &model.ServiceType{ID: serviceTypeID, Name: "Window cleaning"},
	}, nil
}
