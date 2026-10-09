package repository_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/repository"
	"recurringjob/test/fixtures"
	"recurringjob/test/helpers"
)

// newDraft returns an unsaved draft estimate for the refs.
func newDraft(r helpers.Refs, notes string) *model.CustomerDocument {
	return &model.CustomerDocument{
		Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID,
		Notes: notes, LineItems: items("A", "B"),
	}
}

// send moves a draft invoice to sent, which is what numbers it.
func send(t *testing.T, ctx context.Context, invoices *repository.InvoiceRepository, id string) {
	t.Helper()
	n, err := invoices.UpdateStatusGuarded(ctx, id, []string{"draft"}, map[string]any{"status": "sent", "sent_at": time.Now().UTC()})
	if err != nil || n != 1 {
		t.Fatalf("send n=%d err=%v", n, err)
	}
}

// newInvoice returns an unsaved invoice for the job and date.
func newInvoice(r helpers.Refs, jobID string, date time.Time) *model.CustomerDocument {
	return &model.CustomerDocument{
		Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID,
		LineItems: items("A"), JobID: &jobID, OccurrenceDate: &date,
		JobSnapshot: &model.JobSnapshot{ID: jobID, Date: "2026-10-01", Status: "unconfirmed"},
	}
}

func TestDocumentNumbers(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	estimates := repository.NewEstimateRepository(db)
	invoices := repository.NewInvoiceRepository(db)
	job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))

	t.Run("each type counts on its own, in order, from 1", func(t *testing.T) {
		var got []string
		for i := 0; i < 3; i++ {
			e := newDraft(r, "")
			if err := estimates.Create(ctx, e); err != nil {
				t.Fatal(err)
			}
			got = append(got, e.Number)
		}
		// An invoice is numbered when it is sent, not when it is created.
		inv := newInvoice(r, job.ID, civil.New(2026, 10, 5))
		if err := invoices.Create(ctx, inv); err != nil {
			t.Fatal(err)
		}
		if inv.Number != "" {
			t.Fatalf("a draft invoice got the number %q", inv.Number)
		}
		send(t, ctx, invoices, inv.ID)
		sent, err := invoices.Get(ctx, inv.ID)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, sent.Number)
		if want := []string{"EST-000001", "EST-000002", "EST-000003", "INV-000001"}; strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("numbers %v, want %v", got, want)
		}
	})

	t.Run("invoice numbers have no gaps: failed creates, deleted drafts and failed sends use none", func(t *testing.T) {
		db, ctx := helpers.NewTestDB(t)
		r := helpers.RefsFor(t, ctx, db)
		invoices := repository.NewInvoiceRepository(db)
		job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
		day := func(n int) time.Time { return civil.New(2026, 10, n) }
		create := func(n int) *model.CustomerDocument {
			t.Helper()
			inv := newInvoice(r, job.ID, day(n))
			if err := invoices.Create(ctx, inv); err != nil {
				t.Fatal(err)
			}
			return inv
		}
		numberOf := func(inv *model.CustomerDocument) string {
			t.Helper()
			got, err := invoices.Get(ctx, inv.ID)
			if err != nil {
				t.Fatal(err)
			}
			return got.Number
		}

		a, b, c := create(2), create(3), create(4)
		if err := invoices.Create(ctx, newInvoice(r, job.ID, day(2))); appStatus(t, err) != 409 { // a second live invoice for the day
			t.Fatalf("duplicate: %v", err)
		}
		if n, err := invoices.DeleteGuarded(ctx, b.ID, []string{"draft"}); err != nil || n != 1 {
			t.Fatalf("delete n=%d err=%v", n, err)
		}
		send(t, ctx, invoices, c.ID) // sent before a, so it is the first
		send(t, ctx, invoices, a.ID)
		if numberOf(c) != "INV-000001" || numberOf(a) != "INV-000002" {
			t.Fatalf("numbers %s then %s: they follow the order of sending, with no hole", numberOf(c), numberOf(a))
		}

		// A send that fails after taking its number gives it back.
		d := create(5)
		clash := create(6)
		send(t, ctx, invoices, clash.ID)
		if _, err := invoices.UpdateStatusGuarded(ctx, d.ID, []string{"draft"}, map[string]any{"status": "sent", "occurrence_date": day(6)}); err == nil {
			t.Fatal("expected the one-live-invoice index to refuse the send")
		}
		send(t, ctx, invoices, d.ID)
		if numberOf(clash) != "INV-000003" || numberOf(d) != "INV-000004" {
			t.Fatalf("numbers %s then %s", numberOf(clash), numberOf(d))
		}
	})

	t.Run("a draft or a voided draft has no number; sending numbers it once", func(t *testing.T) {
		db, ctx := helpers.NewTestDB(t)
		r := helpers.RefsFor(t, ctx, db)
		invoices := repository.NewInvoiceRepository(db)
		job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
		voided := newInvoice(r, job.ID, civil.New(2026, 10, 2))
		sent := newInvoice(r, job.ID, civil.New(2026, 10, 3))
		for _, inv := range []*model.CustomerDocument{voided, sent} {
			if err := invoices.Create(ctx, inv); err != nil {
				t.Fatal(err)
			}
		}
		if n := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM customer_documents WHERE type = 'invoice' AND number IS NULL`); n != 2 {
			t.Fatalf("%d numberless draft invoices, want 2", n)
		}
		if n, err := invoices.UpdateStatusGuarded(ctx, voided.ID, []string{"draft"}, map[string]any{"status": "void"}); err != nil || n != 1 {
			t.Fatalf("void n=%d err=%v", n, err)
		}
		send(t, ctx, invoices, sent.ID)
		if got, _ := invoices.Get(ctx, voided.ID); got.Number != "" {
			t.Errorf("a voided draft has the number %q", got.Number)
		}
		if got, _ := invoices.Get(ctx, sent.ID); got.Number != "INV-000001" {
			t.Errorf("the sent invoice has the number %q", got.Number)
		}
		// Voiding a sent invoice keeps its number.
		if n, err := invoices.UpdateStatusGuarded(ctx, sent.ID, []string{"sent"}, map[string]any{"status": "void"}); err != nil || n != 1 {
			t.Fatalf("void sent n=%d err=%v", n, err)
		}
		if got, _ := invoices.Get(ctx, sent.ID); got.Number != "INV-000001" || got.Status != "void" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("concurrent sends get distinct, consecutive numbers", func(t *testing.T) {
		db, ctx := helpers.NewTestDB(t)
		r := helpers.RefsFor(t, ctx, db)
		invoices := repository.NewInvoiceRepository(db)
		job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
		const total = 12
		ids := make([]string, total)
		for i := range ids {
			inv := newInvoice(r, job.ID, civil.New(2026, 10, 2+i))
			if err := invoices.Create(ctx, inv); err != nil {
				t.Fatal(err)
			}
			ids[i] = inv.ID
		}
		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if n, err := invoices.UpdateStatusGuarded(ctx, id, []string{"draft"}, map[string]any{"status": "sent"}); err != nil || n != 1 {
					t.Errorf("send n=%d err=%v", n, err)
				}
			}()
		}
		wg.Wait()
		var numbers []string
		helpers.MustTx(t, ctx, db, func(tx *gorm.DB) error {
			return tx.Raw(`SELECT number FROM customer_documents WHERE type = 'invoice' ORDER BY number`).Scan(&numbers).Error
		})
		if len(numbers) != total {
			t.Fatalf("%d numbers, want %d: %v", len(numbers), total, numbers)
		}
		for i, got := range numbers {
			if want := fmt.Sprintf("INV-%06d", i+1); got != want {
				t.Fatalf("numbers %v: position %d is %s, want %s", numbers, i, got, want)
			}
		}
	})

	t.Run("the number is stored and read back", func(t *testing.T) {
		e := newDraft(r, "")
		if err := estimates.Create(ctx, e); err != nil {
			t.Fatal(err)
		}
		got, err := estimates.Get(ctx, e.ID)
		if err != nil || got.Number != e.Number || got.Number == "" || got.Revision != 1 {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("a raw insert gets a number too", func(t *testing.T) {
		helpers.MustTx(t, ctx, db, func(tx *gorm.DB) error {
			return tx.Exec(`INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id) VALUES ('estimate', 'draft', ?, ?, ?)`,
				r.CustomerID, r.LocationID, r.ServiceTypeID).Error
		})
		if n := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM customer_documents WHERE number !~ '^(EST|INV)-[0-9]{6}$'`); n != 0 {
			t.Fatalf("%d documents have a malformed number", n)
		}
	})

	t.Run("an explicit number is kept, and numbers are unique per type", func(t *testing.T) {
		e := newDraft(r, "")
		e.Number = "EST-900001"
		if err := estimates.Create(ctx, e); err != nil || e.Number != "EST-900001" {
			t.Fatalf("number %q err=%v", e.Number, err)
		}
		dup := newDraft(r, "")
		dup.Number = "EST-900001"
		if err := estimates.Create(ctx, dup); err == nil {
			t.Fatal("a duplicate number must be refused")
		}
	})

	t.Run("a rolled-back insert leaves a gap, never a repeat", func(t *testing.T) {
		before := newDraft(r, "")
		if err := estimates.Create(ctx, before); err != nil {
			t.Fatal(err)
		}
		_ = helpers.Tx(ctx, db, func(tx *gorm.DB) error {
			if err := tx.Omit("Customer", "Location", "ServiceType").Create(&model.CustomerDocument{Type: "estimate", Status: "draft", CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID}).Error; err != nil {
				t.Fatal(err)
			}
			return fmt.Errorf("roll back")
		})
		after := newDraft(r, "")
		if err := estimates.Create(ctx, after); err != nil {
			t.Fatal(err)
		}
		var a, b int
		_, _ = fmt.Sscanf(before.Number, "EST-%d", &a)
		_, _ = fmt.Sscanf(after.Number, "EST-%d", &b)
		if b <= a {
			t.Fatalf("%s then %s", before.Number, after.Number)
		}
	})

	t.Run("each tenant schema counts on its own", func(t *testing.T) {
		db2, ctx2 := helpers.NewTestDB(t)
		r2 := helpers.RefsFor(t, ctx2, db2)
		e := newDraft(r2, "")
		if err := repository.NewEstimateRepository(db2).Create(ctx2, e); err != nil || e.Number != "EST-000001" {
			t.Fatalf("number %q err=%v", e.Number, err)
		}
	})
}

func TestDocumentLifecycleConstraints(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
	snap := `'{"id":"` + job.ID + `"}'`
	refVals := fmt.Sprintf(`'%s', '%s', '%s'`, r.CustomerID, r.LocationID, r.ServiceTypeID)
	invoice := func(status, extraCols, extraVals string) string {
		return `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, job_id, job_snapshot, occurrence_date` + extraCols + `) VALUES ('invoice', '` + status + `', ` +
			refVals + `, '` + job.ID + `', ` + snap + `, '2026-10-02'` + extraVals + `)`
	}
	exec := func(sql string) error {
		return helpers.Tx(ctx, db, func(tx *gorm.DB) error { return tx.Exec(sql).Error })
	}

	rejected := []struct{ name, sql, constraint string }{
		{"paid without paid_at", invoice("paid", "", ""), "customer_documents_paid_at_check"},
		{"refunded without refunded_at", invoice("refunded", ", paid_at", ", now()"), "customer_documents_refunded_at_check"},
		{"refunded without paid_at", invoice("refunded", ", refunded_at", ", now()"), "customer_documents_paid_at_check"},
		{"draft with paid_at", invoice("draft", ", paid_at", ", now()"), "customer_documents_paid_at_check"},
		{"sent with refunded_at", invoice("sent", ", refunded_at", ", now()"), "customer_documents_refunded_at_check"},
		{"an estimate cannot be refunded", `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, paid_at, refunded_at) VALUES ('estimate', 'refunded', ` + refVals + `, now(), now())`, "customer_documents_status_per_type"},
		{"revision 0", `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, revision) VALUES ('estimate', 'draft', ` + refVals + `, 0)`, "revision"},
		{"a second estimate number", `INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, number) VALUES ('estimate', 'draft', ` + refVals + `, 'EST-X'), ('estimate', 'draft', ` + refVals + `, 'EST-X')`, "customer_documents_number_key"},
	}
	for _, tt := range rejected {
		if err := exec(tt.sql); err == nil || !strings.Contains(err.Error(), tt.constraint) {
			t.Errorf("%s: want %q, got %v", tt.name, tt.constraint, err)
		}
	}

	t.Run("a paid and a refunded invoice are accepted", func(t *testing.T) {
		if err := exec(invoice("paid", ", paid_at", ", now()")); err != nil {
			t.Errorf("paid: %v", err)
		}
		if err := exec(strings.Replace(invoice("refunded", ", paid_at, refunded_at", ", now(), now()"), "2026-10-02", "2026-10-03", 1)); err != nil {
			t.Errorf("refunded: %v", err)
		}
	})

	t.Run("the same number may be used by an estimate and an invoice", func(t *testing.T) {
		if err := exec(`INSERT INTO customer_documents (type, status, customer_id, location_id, service_type_id, number) VALUES ('estimate', 'draft', ` + refVals + `, 'SAME-1')`); err != nil {
			t.Fatal(err)
		}
		if err := exec(strings.Replace(invoice("draft", ", number", ", 'SAME-1'"), "2026-10-02", "2026-10-04", 1)); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRefundedInvoiceFreesTheOccurrence(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	invoices := repository.NewInvoiceRepository(db)
	job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
	day := civil.New(2026, 10, 5)

	first := newInvoice(r, job.ID, day)
	if err := invoices.Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := invoices.Create(ctx, newInvoice(r, job.ID, day)); appStatus(t, err) != 409 {
		t.Fatalf("a second live invoice: %v", err)
	}
	for _, step := range []struct {
		from, to string
		updates  map[string]any
	}{
		{"draft", "sent", map[string]any{"status": "sent", "sent_at": time.Now()}},
		{"sent", "paid", map[string]any{"status": "paid", "paid_at": time.Now()}},
	} {
		if n, err := invoices.UpdateStatusGuarded(ctx, first.ID, []string{step.from}, step.updates); err != nil || n != 1 {
			t.Fatalf("%s: n=%d err=%v", step.to, n, err)
		}
	}
	// Paid still holds the occurrence.
	if err := invoices.Create(ctx, newInvoice(r, job.ID, day)); appStatus(t, err) != 409 {
		t.Fatalf("while paid: %v", err)
	}
	if n, err := invoices.UpdateStatusGuarded(ctx, first.ID, []string{"paid"}, map[string]any{"status": "refunded", "refunded_at": time.Now()}); err != nil || n != 1 {
		t.Fatalf("refund: n=%d err=%v", n, err)
	}
	second := newInvoice(r, job.ID, day)
	if err := invoices.Create(ctx, second); err != nil {
		t.Fatalf("after the refund: %v", err)
	}
	if got, _ := invoices.Get(ctx, first.ID); got.Status != "refunded" || got.RefundedAt == nil || got.PaidAt == nil || got.SentAt == nil {
		t.Fatalf("refunded invoice %+v", got)
	}
	if ok, err := invoices.ExistsForJob(ctx, job.ID, "paid"); err != nil || ok {
		t.Fatalf("a refunded invoice must not count as paid: ok=%v err=%v", ok, err)
	}
	if ids, err := invoices.IDsForOccurrence(ctx, job.ID, day, "paid"); err != nil || len(ids) != 0 {
		t.Fatalf("a refunded invoice must not be flagged as paid: %v err=%v", ids, err)
	}
	if n, err := invoices.CountForJob(ctx, job.ID); err != nil || n != 2 {
		t.Fatalf("CountForJob (every status) = %d err=%v", n, err)
	}
}

func TestDocumentListFiltersInTheDatabase(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db) // customer "Ada Lovelace"
	estimates := repository.NewEstimateRepository(db)
	invoices := repository.NewInvoiceRepository(db)
	customers := repository.NewCustomerRepository(db)
	locations := repository.NewLocationRepository(db)

	// A second customer whose name holds LIKE wildcards and a backslash.
	other := &model.Customer{Name: `100%_Fan\Club`}
	if err := customers.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	otherLoc := &model.Location{CustomerID: other.ID, AddressLine1: "9 Side Road"}
	if err := locations.Create(ctx, otherLoc); err != nil {
		t.Fatal(err)
	}
	plain := &model.Customer{Name: "Plain Name"}
	if err := customers.Create(ctx, plain); err != nil {
		t.Fatal(err)
	}

	mk := func(c, l string, status string) *model.CustomerDocument {
		d := &model.CustomerDocument{Status: status, CustomerID: c, LocationID: l, ServiceTypeID: r.ServiceTypeID, LineItems: items("A")}
		if err := estimates.Create(ctx, d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	ada1 := mk(r.CustomerID, r.LocationID, "draft")
	ada2 := mk(r.CustomerID, r.LocationID, "sent")
	fan := mk(other.ID, otherLoc.ID, "draft")

	job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
	job2 := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
	inv5 := newInvoice(r, job.ID, civil.New(2026, 10, 5))
	inv9 := newInvoice(r, job.ID, civil.New(2026, 10, 9))
	inv20 := newInvoice(r, job2.ID, civil.New(2026, 10, 20))
	for _, inv := range []*model.CustomerDocument{inv5, inv9, inv20} {
		if err := invoices.Create(ctx, inv); err != nil {
			t.Fatal(err)
		}
	}
	// Only a sent invoice has a number to search by.
	send(t, ctx, invoices, inv9.ID)
	if sent, err := invoices.Get(ctx, inv9.ID); err != nil || sent.Number == "" {
		t.Fatalf("sent invoice: %+v err=%v", sent, err)
	} else {
		inv9.Number = sent.Number
	}

	ids := func(list []model.CustomerDocument) string {
		out := make([]string, 0, len(list))
		for _, d := range list {
			out = append(out, d.ID)
		}
		return strings.Join(out, ",")
	}
	set := func(docs ...*model.CustomerDocument) map[string]bool {
		m := map[string]bool{}
		for _, d := range docs {
			m[d.ID] = true
		}
		return m
	}
	same := func(t *testing.T, got []model.CustomerDocument, want map[string]bool) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %d documents (%s), want %d", len(got), ids(got), len(want))
		}
		for _, d := range got {
			if !want[d.ID] {
				t.Fatalf("unexpected document %s (%s) in %s", d.ID, d.Number, ids(got))
			}
		}
	}

	estimateCases := []struct {
		name string
		f    model.DocumentFilter
		want map[string]bool
	}{
		{"no filter", model.DocumentFilter{}, set(ada1, ada2, fan)},
		{"status", model.DocumentFilter{Status: "draft"}, set(ada1, fan)},
		{"customer", model.DocumentFilter{CustomerID: r.CustomerID}, set(ada1, ada2)},
		{"customer and status", model.DocumentFilter{CustomerID: r.CustomerID, Status: "sent"}, set(ada2)},
		{"location", model.DocumentFilter{LocationID: otherLoc.ID}, set(fan)},
		{"customer without documents", model.DocumentFilter{CustomerID: plain.ID}, set()},
		{"customer name, any case", model.DocumentFilter{Q: "LOVELACE"}, set(ada1, ada2)},
		{"customer name, part of it", model.DocumentFilter{Q: "ve"}, set(ada1, ada2)},
		{"percent is literal", model.DocumentFilter{Q: "100%"}, set(fan)},
		{"a lone percent matches only names that hold one", model.DocumentFilter{Q: "%"}, set(fan)},
		{"underscore is literal", model.DocumentFilter{Q: "0%_F"}, set(fan)},
		{"a lone underscore matches only names that hold one", model.DocumentFilter{Q: "_"}, set(fan)},
		{"backslash is literal", model.DocumentFilter{Q: `Fan\Club`}, set(fan)},
		{"no wildcard escapes a normal name", model.DocumentFilter{Q: "Ad_ Lo"}, set()},
		{"document number", model.DocumentFilter{Q: ada2.Number}, set(ada2)},
		{"number prefix is only estimates here", model.DocumentFilter{Q: "INV-"}, set()},
		{"nothing matches", model.DocumentFilter{Q: "zzz"}, set()},
	}
	for _, tt := range estimateCases {
		t.Run("estimates: "+tt.name, func(t *testing.T) {
			got, err := estimates.List(ctx, tt.f, 100, 0)
			if err != nil {
				t.Fatal(err)
			}
			same(t, got, tt.want)
		})
	}

	invoiceCases := []struct {
		name string
		f    model.DocumentFilter
		want map[string]bool
	}{
		{"never mixes in estimates", model.DocumentFilter{}, set(inv5, inv9, inv20)},
		{"job", model.DocumentFilter{JobID: job.ID}, set(inv5, inv9)},
		{"another job", model.DocumentFilter{JobID: job2.ID}, set(inv20)},
		{"from", model.DocumentFilter{OccurrenceFrom: civil.New(2026, 10, 9)}, set(inv9, inv20)},
		{"to", model.DocumentFilter{OccurrenceTo: civil.New(2026, 10, 9)}, set(inv5, inv9)},
		{"from and to, both inclusive", model.DocumentFilter{OccurrenceFrom: civil.New(2026, 10, 5), OccurrenceTo: civil.New(2026, 10, 9)}, set(inv5, inv9)},
		{"a range with nothing in it", model.DocumentFilter{OccurrenceFrom: civil.New(2026, 10, 10), OccurrenceTo: civil.New(2026, 10, 19)}, set()},
		{"job and range", model.DocumentFilter{JobID: job.ID, OccurrenceFrom: civil.New(2026, 10, 6)}, set(inv9)},
		{"by number", model.DocumentFilter{Q: inv9.Number}, set(inv9)},
		{"by customer name", model.DocumentFilter{Q: "ada"}, set(inv5, inv9, inv20)},
	}
	for _, tt := range invoiceCases {
		t.Run("invoices: "+tt.name, func(t *testing.T) {
			got, err := invoices.List(ctx, tt.f, 100, 0)
			if err != nil {
				t.Fatal(err)
			}
			same(t, got, tt.want)
		})
	}

	t.Run("a range given in another zone still means that calendar day", func(t *testing.T) {
		from := time.Date(2026, 10, 9, 23, 30, 0, 0, time.FixedZone("-8", -8*3600)) // already the 10th in UTC
		got, err := invoices.List(ctx, model.DocumentFilter{OccurrenceFrom: civil.Truncate(from), OccurrenceTo: civil.Truncate(from)}, 100, 0)
		if err != nil || len(got) != 0 {
			t.Fatalf("got %d err=%v", len(got), err)
		}
	})

	t.Run("filters keep newest-first ordering and paging", func(t *testing.T) {
		all, _ := estimates.List(ctx, model.DocumentFilter{CustomerID: r.CustomerID}, 100, 0)
		page, err := estimates.List(ctx, model.DocumentFilter{CustomerID: r.CustomerID}, 1, 1)
		if err != nil || len(all) != 2 || len(page) != 1 || page[0].ID != all[1].ID || all[0].CreatedAt.Before(all[1].CreatedAt) {
			t.Fatalf("all=%s page=%s err=%v", ids(all), ids(page), err)
		}
	})

	t.Run("listed documents carry their number and references", func(t *testing.T) {
		got, _ := estimates.List(ctx, model.DocumentFilter{Q: ada2.Number}, 10, 0)
		if len(got) != 1 || got[0].Number != ada2.Number || got[0].Customer == nil || got[0].Customer.Name != "Ada Lovelace" || len(got[0].LineItems) != 1 {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestDeleteGuarded(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	estimates := repository.NewEstimateRepository(db)
	invoices := repository.NewInvoiceRepository(db)
	job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))

	t.Run("deletes a draft with its line items and revisions", func(t *testing.T) {
		d := newDraft(r, "x")
		if err := estimates.Create(ctx, d); err != nil {
			t.Fatal(err)
		}
		if err := estimates.SaveRevision(ctx, &model.DocumentRevision{DocumentID: d.ID, Revision: 1, Content: model.RevisionContent{Notes: "old"}}); err != nil {
			t.Fatal(err)
		}
		n, err := estimates.DeleteGuarded(ctx, d.ID, []string{"draft"})
		if err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if _, err := estimates.Get(ctx, d.ID); appStatus(t, err) != 404 {
			t.Fatalf("still there: %v", err)
		}
		for _, table := range []string{"customer_line_items WHERE parent_id", "customer_document_revisions WHERE document_id"} {
			if n := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM `+table+` = ?`, d.ID); n != 0 {
				t.Fatalf("%d rows left in %s", n, table)
			}
		}
	})

	t.Run("does nothing when the status is not allowed", func(t *testing.T) {
		d := newDraft(r, "x")
		d.Status = "sent"
		if err := estimates.Create(ctx, d); err != nil {
			t.Fatal(err)
		}
		if n, err := estimates.DeleteGuarded(ctx, d.ID, []string{"draft"}); err != nil || n != 0 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if _, err := estimates.Get(ctx, d.ID); err != nil {
			t.Fatalf("the document must still exist: %v", err)
		}
	})

	t.Run("is pinned to its own type", func(t *testing.T) {
		d := newDraft(r, "x")
		if err := estimates.Create(ctx, d); err != nil {
			t.Fatal(err)
		}
		if n, err := invoices.DeleteGuarded(ctx, d.ID, []string{"draft"}); err != nil || n != 0 {
			t.Fatalf("an invoice repository deleted an estimate: n=%d err=%v", n, err)
		}
	})

	t.Run("deleting a draft invoice frees its occurrence", func(t *testing.T) {
		day := civil.New(2026, 10, 5)
		inv := newInvoice(r, job.ID, day)
		if err := invoices.Create(ctx, inv); err != nil {
			t.Fatal(err)
		}
		if n, err := invoices.DeleteGuarded(ctx, inv.ID, []string{"draft"}); err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if err := invoices.Create(ctx, newInvoice(r, job.ID, day)); err != nil {
			t.Fatalf("the occurrence should be free again: %v", err)
		}
	})
}

func TestEstimateRevisionsRepository(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	estimates := repository.NewEstimateRepository(db)
	d, other := newDraft(r, "x"), newDraft(r, "y")
	for _, e := range []*model.CustomerDocument{d, other} {
		if err := estimates.Create(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("no revisions is an empty list, not nil", func(t *testing.T) {
		got, err := estimates.ListRevisions(ctx, d.ID)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("got %#v err=%v", got, err)
		}
	})

	t.Run("revisions come back newest first with their content", func(t *testing.T) {
		for i := 1; i <= 3; i++ {
			rev := &model.DocumentRevision{DocumentID: d.ID, Revision: i, Content: model.RevisionContent{
				Notes: fmt.Sprintf("v%d", i), CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID,
				LineItems: []model.RevisionLine{{Description: "A", Quantity: i, UnitPriceCents: 100}},
			}}
			if err := estimates.SaveRevision(ctx, rev); err != nil {
				t.Fatal(err)
			}
			if !uuidRe.MatchString(rev.ID) || rev.CreatedAt.IsZero() {
				t.Fatalf("not populated: %+v", rev)
			}
		}
		if err := estimates.SaveRevision(ctx, &model.DocumentRevision{DocumentID: other.ID, Revision: 1}); err != nil {
			t.Fatal(err)
		}
		got, err := estimates.ListRevisions(ctx, d.ID)
		if err != nil || len(got) != 3 || got[0].Revision != 3 || got[2].Revision != 1 {
			t.Fatalf("got %+v err=%v", got, err)
		}
		if c := got[0].Content; c.Notes != "v3" || c.CustomerID != r.CustomerID || len(c.LineItems) != 1 || c.LineItems[0].Quantity != 3 || c.TotalCents() != 300 {
			t.Fatalf("content %+v", c)
		}
	})

	t.Run("a revision number is used once per document", func(t *testing.T) {
		err := estimates.SaveRevision(ctx, &model.DocumentRevision{DocumentID: d.ID, Revision: 2})
		if code := pgCode(err); code != "23505" {
			t.Fatalf("pg code %q (err=%v), want 23505", code, err)
		}
	})

	t.Run("the revision of an unknown document is refused", func(t *testing.T) {
		err := estimates.SaveRevision(ctx, &model.DocumentRevision{DocumentID: "00000000-0000-0000-0000-0000000000ff", Revision: 1})
		if code := pgCode(err); code != "23503" {
			t.Fatalf("pg code %q (err=%v), want 23503", code, err)
		}
	})
}

func TestApproveAndReopenInTheDatabase(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	estimates := repository.NewEstimateRepository(db)
	job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
	snap := &model.JobSnapshot{ID: job.ID, Date: "2026-10-01", Status: "unconfirmed"}

	t.Run("approving stamps sent_at when the estimate was never sent", func(t *testing.T) {
		d := newDraft(r, "x")
		if err := estimates.Create(ctx, d); err != nil {
			t.Fatal(err)
		}
		other := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
		if n, err := estimates.MarkApproved(ctx, d.ID, []string{"draft"}, "approved", other.ID, &model.JobSnapshot{ID: other.ID}); err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		got, _ := estimates.Get(ctx, d.ID)
		if got.SentAt == nil || time.Since(*got.SentAt) > time.Minute {
			t.Fatalf("sent_at %v", got.SentAt)
		}
	})

	t.Run("approving keeps the original sent_at", func(t *testing.T) {
		d := newDraft(r, "x")
		sentAt := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Microsecond)
		d.Status, d.SentAt = "sent", &sentAt
		if err := estimates.Create(ctx, d); err != nil {
			t.Fatal(err)
		}
		other := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
		if n, err := estimates.MarkApproved(ctx, d.ID, []string{"sent"}, "approved", other.ID, &model.JobSnapshot{ID: other.ID}); err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		got, _ := estimates.Get(ctx, d.ID)
		if got.SentAt == nil || !got.SentAt.Equal(sentAt) {
			t.Fatalf("sent_at %v, want %v", got.SentAt, sentAt)
		}
	})

	t.Run("MarkReopened undoes an approval and is guarded by the status", func(t *testing.T) {
		d := newDraft(r, "x")
		d.Status = "sent"
		if err := estimates.Create(ctx, d); err != nil {
			t.Fatal(err)
		}
		if n, err := estimates.MarkReopened(ctx, d.ID, []string{"approved"}, "sent"); err != nil || n != 0 {
			t.Fatalf("a sent estimate cannot be reopened: n=%d err=%v", n, err)
		}
		if n, err := estimates.MarkApproved(ctx, d.ID, []string{"sent"}, "approved", job.ID, snap); err != nil || n != 1 {
			t.Fatalf("approve: n=%d err=%v", n, err)
		}
		if n, err := estimates.MarkReopened(ctx, d.ID, []string{"approved"}, "sent"); err != nil || n != 1 {
			t.Fatalf("reopen: n=%d err=%v", n, err)
		}
		got, _ := estimates.Get(ctx, d.ID)
		if got.Status != "sent" || got.JobID != nil || got.JobSnapshot != nil {
			t.Fatalf("got %+v", got)
		}
		// The job is free to be deleted now.
		if err := repository.NewJobRepository(db).Delete(ctx, job.ID); err != nil {
			t.Fatalf("delete job: %v", err)
		}
	})
}

func TestJobLockAndDelete(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	jobs := repository.NewJobRepository(db)
	occs := repository.NewOccurrenceRepository(db)
	invoices := repository.NewInvoiceRepository(db)

	t.Run("LockJob of an unknown job is a 404", func(t *testing.T) {
		err := jobs.Transaction(ctx, func(ctx context.Context) error {
			return jobs.LockJob(ctx, "00000000-0000-0000-0000-0000000000ff")
		})
		if appStatus(t, err) != 404 {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("Delete is refused (409) while an invoice points at the job; occurrence rows go with it", func(t *testing.T) {
		withInvoice := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
		if err := invoices.Create(ctx, newInvoice(r, withInvoice.ID, civil.New(2026, 10, 5))); err != nil {
			t.Fatal(err)
		}
		if err := jobs.Delete(ctx, withInvoice.ID); appStatus(t, err) != 409 {
			t.Fatalf("with an invoice: %v", err)
		}
		withRow := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
		if err := occs.Create(ctx, &model.JobOccurrence{JobID: withRow.ID, OccurrenceDate: civil.New(2026, 10, 1), Status: "confirmed"}); err != nil {
			t.Fatal(err)
		}
		// Occurrence rows cascade with the job (they are not a document), so
		// the reopen rule checks for them explicitly instead.
		if err := jobs.Delete(ctx, withRow.ID); err != nil {
			t.Fatalf("an occurrence row alone must not block the delete: %v", err)
		}
	})

	t.Run("Delete of a job nothing points at works, twice", func(t *testing.T) {
		job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
		if err := jobs.Delete(ctx, job.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := jobs.GetJob(ctx, job.ID); appStatus(t, err) != 404 {
			t.Fatalf("still there: %v", err)
		}
		if err := jobs.Delete(ctx, job.ID); err != nil {
			t.Fatalf("deleting a missing job is not an error: %v", err)
		}
	})

	t.Run("a locked job makes a racing occurrence insert wait, then fail cleanly once the job is gone", func(t *testing.T) {
		job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
		locked, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			finished <- jobs.Transaction(ctx, func(ctx context.Context) error {
				if err := jobs.LockJob(ctx, job.ID); err != nil {
					return err
				}
				close(locked)
				<-release
				return jobs.Delete(ctx, job.ID)
			})
		}()
		<-locked
		insert := make(chan error, 1)
		go func() {
			insert <- occs.Create(ctx, &model.JobOccurrence{JobID: job.ID, OccurrenceDate: civil.New(2026, 10, 1), Status: "confirmed"})
		}()
		select {
		case err := <-insert:
			t.Fatalf("the insert did not wait for the job lock: %v", err)
		case <-time.After(400 * time.Millisecond):
		}
		close(release)
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
		if err := <-insert; appStatus(t, err) != 409 {
			t.Fatalf("insert after the job was deleted: %v", err)
		}
	})

	t.Run("an invoice insert for a vanished job is a 409, not a 500", func(t *testing.T) {
		job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
		if err := jobs.Delete(ctx, job.ID); err != nil {
			t.Fatal(err)
		}
		if err := invoices.Create(ctx, newInvoice(r, job.ID, civil.New(2026, 10, 5))); appStatus(t, err) != 409 {
			t.Fatalf("got %v", err)
		}
	})
}
