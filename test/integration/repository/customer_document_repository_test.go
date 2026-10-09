package repository_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/repository"
	"recurringjob/test/fixtures"
	"recurringjob/test/helpers"
)

func items(descriptions ...string) []model.CustomerLineItem {
	out := make([]model.CustomerLineItem, 0, len(descriptions))
	for i, d := range descriptions {
		out = append(out, model.CustomerLineItem{Position: i, Description: d, Quantity: i + 1, UnitPriceCents: int64(100 * (i + 1))})
	}
	return out
}

func appStatus(t *testing.T, err error) int {
	t.Helper()
	var ae *apperror.AppError
	if !errors.As(err, &ae) {
		t.Fatalf("want an AppError, got %v", err)
	}
	return ae.Status
}

func TestEstimateRepositoryLineItemsForJob(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	estimates := repository.NewEstimateRepository(db)
	invoices := repository.NewInvoiceRepository(db)
	job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
	other := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))

	t.Run("a job without an estimate has no lines, never nil", func(t *testing.T) {
		got, err := estimates.LineItemsForJob(ctx, job.ID)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("got %#v err=%v", got, err)
		}
	})

	est := &model.CustomerDocument{Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, LineItems: items("B", "A", "C")}
	if err := estimates.Create(ctx, est); err != nil {
		t.Fatal(err)
	}
	// Another job's estimate and an invoice of this job must never leak in.
	otherEst := &model.CustomerDocument{Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, LineItems: items("Other")}
	if err := estimates.Create(ctx, otherEst); err != nil {
		t.Fatal(err)
	}
	for id, j := range map[string]*model.Job{est.ID: job, otherEst.ID: other} {
		if n, err := estimates.MarkApproved(ctx, id, []string{"draft"}, "approved", j.ID, &model.JobSnapshot{ID: j.ID}); err != nil || n != 1 {
			t.Fatalf("approve n=%d err=%v", n, err)
		}
	}
	date := civil.New(2026, 10, 1)
	inv := &model.CustomerDocument{Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID,
		JobID: &job.ID, JobSnapshot: &model.JobSnapshot{ID: job.ID}, OccurrenceDate: &date, LineItems: items("Invoice line")}
	if err := invoices.Create(ctx, inv); err != nil {
		t.Fatal(err)
	}

	t.Run("the job's estimate lines come back in position order", func(t *testing.T) {
		got, err := estimates.LineItemsForJob(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"B", "A", "C"}; len(got) != 3 || got[0].Description != want[0] || got[1].Description != want[1] || got[2].Description != want[2] {
			t.Fatalf("got %+v", got)
		}
		if got[1].Quantity != 2 || got[1].UnitPriceCents != 200 || got[2].Position != 2 {
			t.Fatalf("line fields %+v", got)
		}
	})

	t.Run("another job's estimate is separate", func(t *testing.T) {
		got, err := estimates.LineItemsForJob(ctx, other.ID)
		if err != nil || len(got) != 1 || got[0].Description != "Other" {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})
}

func TestEstimateRepository(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	estimates := repository.NewEstimateRepository(db)
	invoices := repository.NewInvoiceRepository(db)

	t.Run("Create saves the estimate and its items; Get returns them in position order", func(t *testing.T) {
		doc := &model.CustomerDocument{Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, Notes: "n", LineItems: items("A", "B", "C")}
		if err := estimates.Create(ctx, doc); err != nil {
			t.Fatal(err)
		}
		if !uuidRe.MatchString(doc.ID) || doc.Type != "estimate" || doc.CreatedAt.IsZero() {
			t.Fatalf("not populated: %+v", doc)
		}
		got, err := estimates.Get(ctx, doc.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.CustomerID != r.CustomerID || got.Customer == nil || got.Customer.Name != "Ada Lovelace" || got.Location == nil || got.ServiceType == nil || got.Notes != "n" || got.Status != "draft" ||
			got.JobID != nil || got.JobSnapshot != nil || got.OccurrenceDate != nil {
			t.Fatalf("got %+v", got)
		}
		if len(got.LineItems) != 3 || got.LineItems[0].Description != "A" || got.LineItems[2].Description != "C" || got.TotalCents() != 100+400+900 {
			t.Fatalf("items %+v", got.LineItems)
		}
		for _, it := range got.LineItems {
			if it.ParentID != doc.ID || !uuidRe.MatchString(it.ID) {
				t.Fatalf("item %+v", it)
			}
		}
	})

	t.Run("a draft with no items is fine", func(t *testing.T) {
		doc := &model.CustomerDocument{Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, Notes: "Bob"}
		if err := estimates.Create(ctx, doc); err != nil {
			t.Fatal(err)
		}
		if got, err := estimates.Get(ctx, doc.ID); err != nil || len(got.LineItems) != 0 {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("Get of an unknown id is a 404", func(t *testing.T) {
		if _, err := estimates.Get(ctx, "00000000-0000-0000-0000-0000000000ff"); appStatus(t, err) != 404 {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("each repository only sees its own type", func(t *testing.T) {
		job := helpers.SeedJob(t, ctx, db, fixtures.OneOffJob(civil.New(2026, 10, 2)))
		date := civil.New(2026, 10, 2)
		inv := &model.CustomerDocument{
			Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, Notes: "Inv", JobID: &job.ID, OccurrenceDate: &date,
			JobSnapshot: &model.JobSnapshot{ID: job.ID, Date: "2026-10-02", Status: "unconfirmed"},
		}
		if err := invoices.Create(ctx, inv); err != nil {
			t.Fatal(err)
		}
		est := &model.CustomerDocument{Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, Notes: "Est"}
		if err := estimates.Create(ctx, est); err != nil {
			t.Fatal(err)
		}

		if _, err := estimates.Get(ctx, inv.ID); appStatus(t, err) != 404 {
			t.Errorf("estimate repo returned an invoice: %v", err)
		}
		if _, err := invoices.Get(ctx, est.ID); appStatus(t, err) != 404 {
			t.Errorf("invoice repo returned an estimate: %v", err)
		}
		estList, _ := estimates.List(ctx, model.DocumentFilter{}, 100, 0)
		for _, d := range estList {
			if d.Type != "estimate" || d.ID == inv.ID {
				t.Errorf("estimate list leaked %+v", d)
			}
		}
		invList, _ := invoices.List(ctx, model.DocumentFilter{}, 100, 0)
		if len(invList) != 1 || invList[0].ID != inv.ID {
			t.Errorf("invoice list %+v", invList)
		}
		if n, err := estimates.UpdateStatusGuarded(ctx, inv.ID, []string{"draft"}, map[string]any{"status": "sent"}); err != nil || n != 0 {
			t.Errorf("estimate repo changed an invoice: n=%d err=%v", n, err)
		}
		if n, err := estimates.UpdateContent(ctx, inv.ID, []string{"draft"}, map[string]any{"notes": "x"}, nil); err != nil || n != 0 {
			t.Errorf("estimate repo edited an invoice: n=%d err=%v", n, err)
		}
	})
}

func TestCustomerDocumentList(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	estimates := repository.NewEstimateRepository(db)
	for i, st := range []string{"draft", "sent", "draft", "declined"} {
		doc := &model.CustomerDocument{Status: st, CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, Notes: string(rune('A' + i)), LineItems: items("x")}
		if err := estimates.Create(ctx, doc); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond) // distinct created_at
	}

	names := func(status string, limit, offset int) string {
		t.Helper()
		docs, err := estimates.List(ctx, model.DocumentFilter{Status: status}, limit, offset)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for _, d := range docs {
			b.WriteString(d.Notes)
			if len(d.LineItems) != 1 {
				t.Fatalf("items not preloaded: %+v", d)
			}
		}
		return b.String()
	}
	if got := names("", 10, 0); got != "DCBA" {
		t.Errorf("newest first: %q", got)
	}
	if got := names("draft", 10, 0); got != "CA" {
		t.Errorf("status filter: %q", got)
	}
	if got := names("", 2, 1); got != "CB" {
		t.Errorf("paging: %q", got)
	}
	if got := names("approved", 10, 0); got != "" {
		t.Errorf("no match: %q", got)
	}
	if docs, err := estimates.List(ctx, model.DocumentFilter{}, 10, 0); err != nil || docs == nil {
		t.Errorf("list must be a non-nil slice: %v %v", docs, err)
	}
}

func TestCustomerDocumentUpdates(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	estimates := repository.NewEstimateRepository(db)
	newDoc := func(status string) *model.CustomerDocument {
		doc := &model.CustomerDocument{Status: status, CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, Notes: "Ada", LineItems: items("A", "B")}
		if err := estimates.Create(ctx, doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}

	t.Run("UpdateContent edits fields and replaces every item while the status allows it", func(t *testing.T) {
		doc := newDoc("draft")
		time.Sleep(5 * time.Millisecond)
		n, err := estimates.UpdateContent(ctx, doc.ID, []string{"draft"}, map[string]any{"notes": "Bob"}, items("X"))
		if err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		got, _ := estimates.Get(ctx, doc.ID)
		if got.Notes != "Bob" || len(got.LineItems) != 1 || got.LineItems[0].Description != "X" || !got.UpdatedAt.After(doc.UpdatedAt) {
			t.Fatalf("got %+v (updated %v -> %v)", got, doc.UpdatedAt, got.UpdatedAt)
		}
		if c := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM customer_line_items WHERE parent_id = ?`, doc.ID); c != 1 {
			t.Fatalf("%d item rows left, want 1", c)
		}
	})

	t.Run("nil items leave the items alone; empty items clear them", func(t *testing.T) {
		doc := newDoc("draft")
		if n, err := estimates.UpdateContent(ctx, doc.ID, []string{"draft"}, map[string]any{"notes": "hi"}, nil); err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if got, _ := estimates.Get(ctx, doc.ID); got.Notes != "hi" || len(got.LineItems) != 2 {
			t.Fatalf("nil items changed the items: %+v", got)
		}
		if n, err := estimates.UpdateContent(ctx, doc.ID, []string{"draft"}, map[string]any{}, []model.CustomerLineItem{}); err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if got, _ := estimates.Get(ctx, doc.ID); len(got.LineItems) != 0 {
			t.Fatalf("items %+v", got.LineItems)
		}
	})

	t.Run("a document past the allowed status is left untouched", func(t *testing.T) {
		doc := newDoc("sent")
		n, err := estimates.UpdateContent(ctx, doc.ID, []string{"draft"}, map[string]any{"notes": "Eve"}, items("Z"))
		if err != nil || n != 0 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		got, _ := estimates.Get(ctx, doc.ID)
		if got.Notes != "Ada" || len(got.LineItems) != 2 || got.LineItems[0].Description != "A" {
			t.Fatalf("changed: %+v", got)
		}
	})

	t.Run("a failed item insert rolls the whole edit back", func(t *testing.T) {
		doc := newDoc("draft")
		bad := []model.CustomerLineItem{{Position: 0, Description: "ok", Quantity: 1}, {Position: 1, Description: "zero qty", Quantity: 0}}
		if _, err := estimates.UpdateContent(ctx, doc.ID, []string{"draft"}, map[string]any{"notes": "Eve"}, bad); err == nil {
			t.Fatal("want a CHECK violation")
		}
		got, _ := estimates.Get(ctx, doc.ID)
		if got.Notes != "Ada" || len(got.LineItems) != 2 {
			t.Fatalf("partial edit survived: %+v", got)
		}
	})

	t.Run("UpdateStatusGuarded only moves a document from an allowed status", func(t *testing.T) {
		doc := newDoc("draft")
		if n, err := estimates.UpdateStatusGuarded(ctx, doc.ID, []string{"sent"}, map[string]any{"status": "declined"}); err != nil || n != 0 {
			t.Fatalf("wrong source: n=%d err=%v", n, err)
		}
		if n, err := estimates.UpdateStatusGuarded(ctx, doc.ID, []string{"draft", "sent"}, map[string]any{"status": "declined"}); err != nil || n != 1 {
			t.Fatalf("right source: n=%d err=%v", n, err)
		}
		if got, _ := estimates.Get(ctx, doc.ID); got.Status != "declined" {
			t.Fatalf("status %s", got.Status)
		}
	})

	t.Run("an invalid status for the type is rejected by the database", func(t *testing.T) {
		doc := newDoc("draft")
		if _, err := estimates.UpdateStatusGuarded(ctx, doc.ID, []string{"draft"}, map[string]any{"status": "paid"}); err == nil {
			t.Fatal("want a CHECK violation")
		}
	})

	t.Run("MarkApproved ties the estimate to a job and stores the snapshot", func(t *testing.T) {
		doc := newDoc("sent")
		job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 12)))
		snap := &model.JobSnapshot{ID: job.ID, Date: "2026-10-12", Status: "unconfirmed", Recurrence: job.Recurrence}

		if n, err := estimates.MarkApproved(ctx, doc.ID, []string{"approved"}, "approved", job.ID, snap); err != nil || n != 0 {
			t.Fatalf("wrong source: n=%d err=%v", n, err)
		}
		n, err := estimates.MarkApproved(ctx, doc.ID, []string{"draft", "sent"}, "approved", job.ID, snap)
		if err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		got, _ := estimates.Get(ctx, doc.ID)
		if got.Status != "approved" || got.JobID == nil || *got.JobID != job.ID {
			t.Fatalf("got %+v", got)
		}
		if got.JobSnapshot == nil || got.JobSnapshot.Date != "2026-10-12" || got.JobSnapshot.Recurrence == nil || got.JobSnapshot.Recurrence.Frequency != "daily" {
			t.Fatalf("snapshot %+v", got.JobSnapshot)
		}
	})
}

func TestInvoiceRepository(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	invoices := repository.NewInvoiceRepository(db)
	job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
	newInvoice := func(date time.Time) *model.CustomerDocument {
		return &model.CustomerDocument{
			Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, Notes: "Ada", LineItems: items("A"),
			JobID: &job.ID, OccurrenceDate: &date,
			JobSnapshot: &model.JobSnapshot{ID: job.ID, Date: "2026-10-01", Status: "unconfirmed", Recurrence: job.Recurrence},
		}
	}

	t.Run("an invoice round-trips its job, snapshot and occurrence date", func(t *testing.T) {
		inv := newInvoice(civil.New(2026, 10, 5))
		if err := invoices.Create(ctx, inv); err != nil {
			t.Fatal(err)
		}
		got, err := invoices.Get(ctx, inv.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Type != "invoice" || got.JobID == nil || *got.JobID != job.ID || got.OccurrenceDate == nil ||
			civil.Format(*got.OccurrenceDate) != "2026-10-05" || got.OccurrenceDate.Location() != time.UTC {
			t.Fatalf("got %+v", got)
		}
		if got.JobSnapshot == nil || got.JobSnapshot.ID != job.ID || got.JobSnapshot.Recurrence == nil {
			t.Fatalf("snapshot %+v", got.JobSnapshot)
		}
	})

	t.Run("a second invoice for the same occurrence is a 409 and stores nothing", func(t *testing.T) {
		before := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM customer_documents`)
		itemsBefore := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM customer_line_items`)
		if err := invoices.Create(ctx, newInvoice(civil.New(2026, 10, 5))); appStatus(t, err) != 409 {
			t.Fatalf("got %v", err)
		}
		if helpers.Scalar(t, ctx, db, `SELECT count(*) FROM customer_documents`) != before ||
			helpers.Scalar(t, ctx, db, `SELECT count(*) FROM customer_line_items`) != itemsBefore {
			t.Fatal("the rejected invoice left rows behind")
		}
	})

	t.Run("another occurrence of the same job can be invoiced", func(t *testing.T) {
		if err := invoices.Create(ctx, newInvoice(civil.New(2026, 10, 6))); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("an invoice without a job, snapshot or date is rejected by the database", func(t *testing.T) {
		date := civil.New(2026, 10, 7)
		for name, doc := range map[string]*model.CustomerDocument{
			"no job":      {Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, Notes: "x", OccurrenceDate: &date, JobSnapshot: &model.JobSnapshot{ID: "x"}},
			"no snapshot": {Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, Notes: "x", JobID: &job.ID, OccurrenceDate: &date},
			"no date":     {Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, Notes: "x", JobID: &job.ID, JobSnapshot: &model.JobSnapshot{ID: job.ID}},
		} {
			if err := invoices.Create(ctx, doc); err == nil {
				t.Errorf("%s: want a CHECK violation", name)
			}
		}
	})
}

// TestCustomerDocumentConstraints inserts rows straight into the tables to
// prove the database itself enforces the rules the services rely on.
func TestCustomerDocumentConstraints(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	job := helpers.SeedJob(t, ctx, db, fixtures.OneOffJob(civil.New(2026, 10, 2)))
	other := helpers.SeedJob(t, ctx, db, fixtures.OneOffJob(civil.New(2026, 10, 3)))

	exec := func(sql string, args ...any) error {
		return helpers.Tx(ctx, db, func(tx *gorm.DB) error { return tx.Exec(sql, args...).Error })
	}
	refVals := "'" + r.CustomerID + "', '" + r.LocationID + "', '" + r.ServiceTypeID + "'"
	const snap = `'{"id":"x","date":"2026-10-02","status":"unconfirmed","recurrence":null}'::jsonb`

	rejected := []struct {
		name, sql, constraint string
	}{
		{"unknown type", `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id) VALUES ('quote', 'draft', ` + refVals + `)`, "violates check constraint"}, // which CHECK fires first is up to Postgres
		{"estimate with an invoice status", `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, paid_at) VALUES ('estimate', 'paid', ` + refVals + `, now())`, "customer_documents_status_per_type"},
		{"invoice with an estimate status", `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, job_id, job_snapshot, occurrence_date) VALUES ('invoice', 'approved', ` + refVals + `, '` + job.ID + `', ` + snap + `, '2026-10-02')`, "customer_documents_status_per_type"},
		{"invoice without a job", `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, job_snapshot, occurrence_date) VALUES ('invoice', 'draft', ` + refVals + `, ` + snap + `, '2026-10-02')`, "customer_documents_invoice_requires_job"},
		{"invoice without a snapshot", `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, job_id, occurrence_date) VALUES ('invoice', 'draft', ` + refVals + `, '` + job.ID + `', '2026-10-02')`, "customer_documents_invoice_requires_job"},
		{"invoice without an occurrence date", `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, job_id, job_snapshot) VALUES ('invoice', 'draft', ` + refVals + `, '` + job.ID + `', ` + snap + `)`, "customer_documents_invoice_requires_job"},
		{"estimate with an occurrence date", `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, occurrence_date) VALUES ('estimate', 'draft', ` + refVals + `, '2026-10-02')`, "customer_documents_estimate_shape"},
		{"estimate with a job but no snapshot", `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, job_id) VALUES ('estimate', 'approved', ` + refVals + `, '` + job.ID + `')`, "customer_documents_estimate_shape"},
		{"estimate with a snapshot but no job", `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, job_snapshot) VALUES ('estimate', 'approved', ` + refVals + `, ` + snap + `)`, "customer_documents_estimate_shape"},
		{"estimate with a job that is not approved", `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, job_id, job_snapshot) VALUES ('estimate', 'sent', ` + refVals + `, '` + job.ID + `', ` + snap + `)`, "customer_documents_estimate_shape"},
		{"job that does not exist", `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, job_id, job_snapshot, occurrence_date) VALUES ('invoice', 'draft', ` + refVals + `, '00000000-0000-0000-0000-0000000000ff', ` + snap + `, '2026-10-02')`, "customer_documents_job_id_fkey"},
		{"missing customer, location and service type", `INSERT INTO customer_documents (type, status) VALUES ('estimate', 'draft')`, "customer_id"},
		{"customer that does not exist", `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id) VALUES ('estimate', 'draft', '00000000-0000-0000-0000-0000000000ff', '` + r.LocationID + `', '` + r.ServiceTypeID + `')`, "customer_documents_customer_id_fkey"},
		{"location that does not exist", `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id) VALUES ('estimate', 'draft', '` + r.CustomerID + `', '00000000-0000-0000-0000-0000000000ff', '` + r.ServiceTypeID + `')`, "customer_documents_location_id_fkey"},
		{"service type that does not exist", `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id) VALUES ('estimate', 'draft', '` + r.CustomerID + `', '` + r.LocationID + `', '00000000-0000-0000-0000-0000000000ff')`, "customer_documents_service_type_id_fkey"},
	}
	for _, tt := range rejected {
		err := exec(tt.sql)
		if err == nil || !strings.Contains(err.Error(), tt.constraint) {
			t.Errorf("%s: want %q, got %v", tt.name, tt.constraint, err)
		}
	}

	t.Run("a well-formed estimate and invoice are accepted", func(t *testing.T) {
		for name, sql := range map[string]string{
			"draft estimate":    `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id) VALUES ('estimate', 'draft', ` + refVals + `)`,
			"approved estimate": `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, job_id, job_snapshot) VALUES ('estimate', 'approved', ` + refVals + `, '` + job.ID + `', ` + snap + `)`,
			"invoice":           `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, job_id, job_snapshot, occurrence_date) VALUES ('invoice', 'draft', ` + refVals + `, '` + job.ID + `', ` + snap + `, '2026-10-02')`,
		} {
			if err := exec(sql); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
	})

	t.Run("one estimate per job, one invoice per occurrence", func(t *testing.T) {
		if err := exec(`INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, job_id, job_snapshot) VALUES ('estimate', 'approved', ` + refVals + `, '` + job.ID + `', ` + snap + `)`); err == nil ||
			!strings.Contains(err.Error(), "customer_documents_estimate_job_key") {
			t.Errorf("second estimate for a job: %v", err)
		}
		if err := exec(`INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, job_id, job_snapshot, occurrence_date) VALUES ('invoice', 'draft', ` + refVals + `, '` + job.ID + `', ` + snap + `, '2026-10-02')`); err == nil ||
			!strings.Contains(err.Error(), "customer_documents_invoice_job_date_key") {
			t.Errorf("second invoice for an occurrence: %v", err)
		}
		// Same job on another date, and another job on the same date, are fine.
		if err := exec(`INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, job_id, job_snapshot, occurrence_date) VALUES ('invoice', 'draft', ` + refVals + `, '` + job.ID + `', ` + snap + `, '2026-10-09')`); err != nil {
			t.Errorf("another date: %v", err)
		}
		if err := exec(`INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, job_id, job_snapshot, occurrence_date) VALUES ('invoice', 'draft', ` + refVals + `, '` + other.ID + `', ` + snap + `, '2026-10-02')`); err != nil {
			t.Errorf("another job: %v", err)
		}
		// Draft estimates carry no job, so any number of them may coexist.
		for i := 0; i < 3; i++ {
			if err := exec(`INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id) VALUES ('estimate', 'draft', ` + refVals + `)`); err != nil {
				t.Errorf("draft estimate %d: %v", i, err)
			}
		}
	})

	t.Run("line item rules", func(t *testing.T) {
		var docID string
		if err := helpers.Tx(ctx, db, func(tx *gorm.DB) error {
			return tx.Raw(`INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id) VALUES ('estimate', 'draft', ` + refVals + `) RETURNING id`).Scan(&docID).Error
		}); err != nil {
			t.Fatal(err)
		}
		const ins = `INSERT INTO customer_line_items (parent_id, position, description, quantity, unit_price_cents) VALUES (?, ?, ?, ?, ?)`
		if err := exec(ins, docID, 0, "ok", 1, 0); err != nil {
			t.Fatalf("valid item: %v", err)
		}
		for name, c := range map[string]struct {
			args       []any
			constraint string
		}{
			"duplicate position": {[]any{docID, 0, "dup", 1, 1}, "customer_line_items_parent_position_key"},
			"zero quantity":      {[]any{docID, 1, "q", 0, 1}, "customer_line_items_quantity_check"},
			"negative price":     {[]any{docID, 1, "p", 1, -1}, "customer_line_items_unit_price_cents_check"},
			"negative position":  {[]any{docID, -1, "n", 1, 1}, "customer_line_items_position_check"},
			"no parent":          {[]any{"00000000-0000-0000-0000-0000000000ff", 0, "o", 1, 1}, "customer_line_items_parent_id_fkey"},
		} {
			if err := exec(ins, c.args...); err == nil || !strings.Contains(err.Error(), c.constraint) {
				t.Errorf("%s: want %q, got %v", name, c.constraint, err)
			}
		}

		if err := exec(`DELETE FROM customer_documents WHERE id = ?`, docID); err != nil {
			t.Fatal(err)
		}
		if n := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM customer_line_items WHERE parent_id = ?`, docID); n != 0 {
			t.Fatalf("%d items survived deleting their document", n)
		}
	})

	t.Run("a job with documents cannot be deleted out from under them", func(t *testing.T) {
		if err := exec(`DELETE FROM jobs WHERE id = ?`, job.ID); err == nil || !strings.Contains(err.Error(), "customer_documents_job_id_fkey") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestGetForUpdate(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	estimates := repository.NewEstimateRepository(db)
	invoices := repository.NewInvoiceRepository(db)
	doc := &model.CustomerDocument{Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, Notes: "Ada", LineItems: items("A", "B", "C")}
	if err := estimates.Create(ctx, doc); err != nil {
		t.Fatal(err)
	}

	t.Run("returns the document with its items in order, like Get", func(t *testing.T) {
		got, err := estimates.GetForUpdate(ctx, doc.ID)
		if err != nil || got.Notes != "Ada" || len(got.LineItems) != 3 || got.LineItems[0].Description != "A" || got.LineItems[2].Description != "C" {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("an unknown id, or the other type's id, is a 404", func(t *testing.T) {
		if _, err := estimates.GetForUpdate(ctx, "00000000-0000-0000-0000-0000000000ff"); appStatus(t, err) != 404 {
			t.Errorf("unknown: %v", err)
		}
		if _, err := invoices.GetForUpdate(ctx, doc.ID); appStatus(t, err) != 404 {
			t.Errorf("wrong type: %v", err)
		}
	})

	t.Run("holds the row so a concurrent writer waits for the transaction to end", func(t *testing.T) {
		locked, release := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		releaseLock := func() { releaseOnce.Do(func() { close(release) }) }
		// If an assertion below fails, still let the holder commit so the
		// schema cleanup is not left waiting on its lock.
		t.Cleanup(releaseLock)
		holderDone := make(chan error, 1)
		go func() {
			holderDone <- estimates.Transaction(ctx, func(ctx context.Context) error {
				if _, err := estimates.GetForUpdate(ctx, doc.ID); err != nil {
					return err
				}
				close(locked)
				<-release
				return nil
			})
		}()
		select {
		case <-locked:
		case err := <-holderDone:
			t.Fatalf("transaction ended before locking: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out taking the lock")
		}

		writerDone := make(chan error, 1)
		go func() {
			_, err := estimates.UpdateStatusGuarded(ctx, doc.ID, []string{"draft"}, map[string]any{"status": "sent"})
			writerDone <- err
		}()
		select {
		case err := <-writerDone:
			t.Fatalf("the writer was not blocked by the lock (err=%v)", err)
		case <-time.After(300 * time.Millisecond):
		}

		releaseLock()
		if err := <-holderDone; err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-writerDone:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the writer never finished after the lock was released")
		}
		if got, _ := estimates.Get(ctx, doc.ID); got.Status != "sent" {
			t.Fatalf("status %s", got.Status)
		}
	})
}

func TestInvoiceOccurrenceQueries(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	invoices := repository.NewInvoiceRepository(db)
	job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
	other := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
	oct5, oct6 := civil.New(2026, 10, 5), civil.New(2026, 10, 6)

	// invoice creates an invoice in the given status. Only one live invoice
	// may exist per occurrence, so each case below uses its own occurrence.
	invoice := func(jobID string, date time.Time, status string) *model.CustomerDocument {
		t.Helper()
		inv := &model.CustomerDocument{
			Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, Notes: "Ada", LineItems: items("A"),
			JobID: &jobID, OccurrenceDate: &date, JobSnapshot: &model.JobSnapshot{ID: jobID, Date: "2026-10-01", Status: "unconfirmed"},
		}
		if err := invoices.Create(ctx, inv); err != nil {
			t.Fatal(err)
		}
		if status != "draft" {
			if n, err := invoices.UpdateStatusGuarded(ctx, inv.ID, []string{"draft"}, map[string]any{"status": status}); err != nil || n != 1 {
				t.Fatalf("set %s: n=%d err=%v", status, n, err)
			}
		}
		return inv
	}
	statusOf := func(id string) string {
		t.Helper()
		got, err := invoices.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return got.Status
	}

	draft := invoice(job.ID, oct5, "draft")
	sentOtherDate := invoice(job.ID, oct6, "sent")
	otherJob := invoice(other.ID, oct5, "draft")

	t.Run("UpdateStatusForOccurrence moves only the matching occurrence of that job", func(t *testing.T) {
		n, err := invoices.UpdateStatusForOccurrence(ctx, job.ID, oct5, []string{"draft", "sent"}, "void")
		if err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if statusOf(draft.ID) != "void" || statusOf(sentOtherDate.ID) != "sent" || statusOf(otherJob.ID) != "draft" {
			t.Fatal("it changed an invoice of another date or another job")
		}
		if n, err := invoices.UpdateStatusForOccurrence(ctx, job.ID, oct5, []string{"draft", "sent"}, "void"); err != nil || n != 0 {
			t.Fatalf("already void, second run: n=%d err=%v", n, err)
		}
	})

	t.Run("a paid invoice is not touched by the allowed statuses", func(t *testing.T) {
		paid := invoice(job.ID, civil.New(2026, 10, 7), "sent")
		if n, err := invoices.UpdateStatusGuarded(ctx, paid.ID, []string{"sent"}, map[string]any{"status": "paid", "paid_at": time.Now()}); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		if n, err := invoices.UpdateStatusForOccurrence(ctx, job.ID, civil.New(2026, 10, 7), []string{"draft", "sent"}, "void"); err != nil || n != 0 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if statusOf(paid.ID) != "paid" {
			t.Fatal("a paid invoice was voided")
		}
	})

	t.Run("IDsForOccurrence lists the invoices of that occurrence in a status", func(t *testing.T) {
		ids, err := invoices.IDsForOccurrence(ctx, job.ID, civil.New(2026, 10, 7), "paid")
		if err != nil || len(ids) != 1 {
			t.Fatalf("ids=%v err=%v", ids, err)
		}
		for name, c := range map[string]struct {
			job    string
			date   time.Time
			status string
		}{
			"other status": {job.ID, civil.New(2026, 10, 7), "sent"},
			"other date":   {job.ID, civil.New(2026, 10, 9), "paid"},
			"other job":    {other.ID, civil.New(2026, 10, 7), "paid"},
		} {
			got, err := invoices.IDsForOccurrence(ctx, c.job, c.date, c.status)
			if err != nil || got == nil || len(got) != 0 {
				t.Errorf("%s: got %#v err=%v, want an empty non-nil list", name, got, err)
			}
		}
	})

	t.Run("ExistsForJob looks at the job and the status", func(t *testing.T) {
		for name, c := range map[string]struct {
			job, status string
			want        bool
		}{
			"job with a paid invoice":       {job.ID, "paid", true},
			"job with a sent invoice":       {job.ID, "sent", true},
			"job with no paid invoice":      {other.ID, "paid", false},
			"unknown job":                   {"00000000-0000-0000-0000-0000000000ff", "paid", false},
			"status nobody has":             {job.ID, "approved", false},
			"void invoice of the first job": {job.ID, "void", true},
		} {
			if got, err := invoices.ExistsForJob(ctx, c.job, c.status); err != nil || got != c.want {
				t.Errorf("%s: got %v err=%v, want %v", name, got, err, c.want)
			}
		}
	})
}

func TestVoidedInvoiceFreesTheOccurrence(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	invoices := repository.NewInvoiceRepository(db)
	job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
	date := civil.New(2026, 10, 5)
	newInvoice := func() *model.CustomerDocument {
		return &model.CustomerDocument{
			Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, Notes: "Ada", LineItems: items("A"),
			JobID: &job.ID, OccurrenceDate: &date, JobSnapshot: &model.JobSnapshot{ID: job.ID, Date: "2026-10-01", Status: "unconfirmed"},
		}
	}

	first := newInvoice()
	if err := invoices.Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := invoices.Create(ctx, newInvoice()); appStatus(t, err) != 409 {
		t.Fatalf("a second live invoice: %v", err)
	}

	if n, err := invoices.UpdateStatusGuarded(ctx, first.ID, []string{"draft"}, map[string]any{"status": "void"}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	second := newInvoice()
	if err := invoices.Create(ctx, second); err != nil {
		t.Fatalf("re-invoicing after a void: %v", err)
	}
	if err := invoices.Create(ctx, newInvoice()); appStatus(t, err) != 409 {
		t.Fatalf("a third invoice while the second is live: %v", err)
	}

	// Voiding the second as well frees it again; voided rows pile up freely.
	if n, err := invoices.UpdateStatusGuarded(ctx, second.ID, []string{"draft"}, map[string]any{"status": "void"}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := invoices.Create(ctx, newInvoice()); err != nil {
		t.Fatalf("after the second void: %v", err)
	}
	if n := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM customer_documents WHERE type = 'invoice' AND status = 'void'`); n != 2 {
		t.Fatalf("%d void invoices, want 2", n)
	}
}

func TestMoveUnpaidForOccurrence(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	invoices := repository.NewInvoiceRepository(db)
	estimates := repository.NewEstimateRepository(db)
	job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
	other := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
	oct5, oct6, oct9 := civil.New(2026, 10, 5), civil.New(2026, 10, 6), civil.New(2026, 10, 9)

	invoice := func(jobID string, date time.Time, status string) *model.CustomerDocument {
		t.Helper()
		inv := &model.CustomerDocument{
			Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, LineItems: items("A"),
			JobID: &jobID, OccurrenceDate: &date, JobSnapshot: &model.JobSnapshot{ID: jobID, Date: "2026-10-01", Status: "unconfirmed"},
		}
		if err := invoices.Create(ctx, inv); err != nil {
			t.Fatal(err)
		}
		if status != "draft" {
			if n, err := invoices.UpdateStatusGuarded(ctx, inv.ID, []string{"draft"}, map[string]any{"status": status}); err != nil || n != 1 {
				t.Fatalf("set %s: n=%d err=%v", status, n, err)
			}
		}
		return inv
	}
	dateOf := func(id string) string {
		t.Helper()
		got, err := invoices.Get(ctx, id)
		if err != nil || got.OccurrenceDate == nil {
			t.Fatalf("get %s: %+v err=%v", id, got, err)
		}
		return civil.Format(*got.OccurrenceDate)
	}

	draft := invoice(job.ID, oct5, "draft")
	// One live invoice per occurrence, so the sent one is for another visit
	// that is moved on its own below.
	sent := invoice(job.ID, oct6, "sent")
	otherJob := invoice(other.ID, oct5, "draft")
	before, _ := invoices.Get(ctx, draft.ID)

	t.Run("moves a draft invoice to the new date and touches updated_at", func(t *testing.T) {
		time.Sleep(5 * time.Millisecond)
		n, err := invoices.MoveUnpaidForOccurrence(ctx, job.ID, oct5, oct9)
		if err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if dateOf(draft.ID) != "2026-10-09" {
			t.Fatalf("draft is on %s", dateOf(draft.ID))
		}
		after, _ := invoices.Get(ctx, draft.ID)
		if !after.UpdatedAt.After(before.UpdatedAt) {
			t.Fatalf("updated_at %v -> %v", before.UpdatedAt, after.UpdatedAt)
		}
		if dateOf(sent.ID) != "2026-10-06" || dateOf(otherJob.ID) != "2026-10-05" {
			t.Fatal("it moved an invoice of another date or another job")
		}
	})

	t.Run("moves a sent invoice too", func(t *testing.T) {
		if n, err := invoices.MoveUnpaidForOccurrence(ctx, job.ID, oct6, civil.New(2026, 10, 10)); err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if dateOf(sent.ID) != "2026-10-10" {
			t.Fatalf("sent is on %s", dateOf(sent.ID))
		}
	})

	t.Run("paid and void invoices stay where they are", func(t *testing.T) {
		oct12 := civil.New(2026, 10, 12)
		voided := invoice(job.ID, oct12, "void") // a void invoice frees the slot for the paid one
		paid := invoice(job.ID, oct12, "sent")
		if n, err := invoices.UpdateStatusGuarded(ctx, paid.ID, []string{"sent"}, map[string]any{"status": "paid", "paid_at": time.Now()}); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		if n, err := invoices.MoveUnpaidForOccurrence(ctx, job.ID, oct12, civil.New(2026, 10, 20)); err != nil || n != 0 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if dateOf(paid.ID) != "2026-10-12" || dateOf(voided.ID) != "2026-10-12" {
			t.Fatal("a paid or void invoice moved")
		}
	})

	t.Run("nothing to move is zero rows, not an error", func(t *testing.T) {
		if n, err := invoices.MoveUnpaidForOccurrence(ctx, job.ID, civil.New(2026, 11, 1), oct9); err != nil || n != 0 {
			t.Fatalf("n=%d err=%v", n, err)
		}
	})

	t.Run("an estimate is never moved", func(t *testing.T) {
		est := &model.CustomerDocument{Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, LineItems: items("A")}
		if err := estimates.Create(ctx, est); err != nil {
			t.Fatal(err)
		}
		if n := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM customer_documents WHERE type = 'estimate' AND occurrence_date IS NOT NULL`); n != 0 {
			t.Fatalf("%d estimates have an occurrence date", n)
		}
	})
}
