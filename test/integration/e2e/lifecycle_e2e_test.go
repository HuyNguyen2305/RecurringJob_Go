package e2e_test

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"testing"

	"gorm.io/gorm"

	"recurringjob/internal/common/auth"
	"recurringjob/internal/common/civil"
	"recurringjob/test/helpers"
)

func (c client) del(path string) resp { return c.do("DELETE", path, "") }

// approvedEstimate creates and approves an estimate for a one-off job today
// and returns the estimate and job ids.
func approvedEstimate(t *testing.T, c client) (est, job string) {
	t.Helper()
	est = c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)
	approved := c.post("/estimates/"+est+"/approve", fmt.Sprintf(`{"date":%q}`, civil.Format(civil.Today()))).expect(t, 200).obj()
	return est, approved["jobId"].(string)
}

func tenantScalar(t *testing.T, db *gorm.DB, schema, query string, args ...any) int64 {
	t.Helper()
	return helpers.Scalar(t, auth.WithTenantSchema(context.Background(), schema), db, query, args...)
}

func TestE2ENumbersAndTimestamps(t *testing.T) {
	c, _, _ := newClient(t)
	today := civil.Format(civil.Today())

	est := c.post("/estimates", draftBody).expect(t, 200).obj()
	if est["number"] != "EST-000001" || est["revision"] != float64(1) || est["sentAt"] != nil || est["paidAt"] != nil || est["refundedAt"] != nil {
		t.Fatalf("new estimate: %v", est)
	}
	if second := c.post("/estimates", draftBody).expect(t, 200).obj(); second["number"] != "EST-000002" {
		t.Fatalf("second estimate: %v", second["number"])
	}
	id := est["id"].(string)
	sent := c.patch("/estimates/"+id+"/status", `{"status":"sent"}`).expect(t, 200).obj()
	if sent["sentAt"] == nil || sent["number"] != "EST-000001" {
		t.Fatalf("sent: %v", sent)
	}

	job := c.post("/estimates/"+id+"/approve", fmt.Sprintf(`{"date":%q,"recurrence":{"frequency":"daily"}}`, today)).expect(t, 200).obj()["jobId"].(string)
	inv := c.post("/jobs/"+job+"/occurrences/"+today+"/invoice", draftBody).expect(t, 200).obj()
	if inv["number"] != "INV-000001" || inv["sentAt"] != nil {
		t.Fatalf("invoice: %v", inv)
	}
	invID := inv["id"].(string)
	if got := c.patch("/invoices/"+invID+"/status", `{"status":"sent"}`).expect(t, 200).obj(); got["sentAt"] == nil || got["paidAt"] != nil {
		t.Fatalf("sent invoice: %v", got)
	}
	paid := c.patch("/invoices/"+invID+"/status", `{"status":"paid"}`).expect(t, 200).obj()
	if paid["paidAt"] == nil || paid["sentAt"] == nil || paid["refundedAt"] != nil {
		t.Fatalf("paid invoice: %v", paid)
	}
	refunded := c.patch("/invoices/"+invID+"/status", `{"status":"refunded"}`).expect(t, 200).obj()
	if refunded["status"] != "refunded" || refunded["refundedAt"] == nil || refunded["paidAt"] != paid["paidAt"] || refunded["number"] != "INV-000001" {
		t.Fatalf("refunded invoice: %v", refunded)
	}
	// What was read back is what is stored.
	if got := c.get("/invoices/"+invID).expect(t, 200).obj(); got["refundedAt"] != refunded["refundedAt"] || got["status"] != "refunded" {
		t.Fatalf("stored invoice: %v", got)
	}
}

func TestE2ERefund(t *testing.T) {
	today := civil.Format(civil.Today())
	pay := func(c client, inv string) {
		c.patch("/invoices/"+inv+"/status", `{"status":"sent"}`).expect(t, 200)
		c.patch("/invoices/"+inv+"/status", `{"status":"paid"}`).expect(t, 200)
	}

	t.Run("only a paid invoice can be refunded, and a refunded one is final", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := dailyJob(t, c)
		inv := invoiceFor(t, c, job, today)
		c.patch("/invoices/"+inv+"/status", `{"status":"refunded"}`).expect(t, 409) // draft
		c.patch("/invoices/"+inv+"/status", `{"status":"sent"}`).expect(t, 200)
		c.patch("/invoices/"+inv+"/status", `{"status":"refunded"}`).expect(t, 409) // sent
		c.patch("/invoices/"+inv+"/status", `{"status":"paid"}`).expect(t, 200)
		c.patch("/invoices/"+inv+"/status", `{"status":"refunded"}`).expect(t, 200)
		for _, to := range []string{"paid", "sent", "void", "refunded"} {
			c.patch("/invoices/"+inv+"/status", fmt.Sprintf(`{"status":%q}`, to)).expect(t, 409)
		}
		c.patch("/invoices/"+inv, `{"notes":"x"}`).expect(t, 409)
		// "refunded" is an invoice status only.
		est := c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)
		c.patch("/estimates/"+est+"/status", `{"status":"refunded"}`).expect(t, 400)
		if got := c.get("/invoices?status=refunded").expect(t, 200).list(); len(got) != 1 || got[0].(map[string]any)["id"] != inv {
			t.Fatalf("status filter: %v", got)
		}
	})

	t.Run("after a refund the occurrence can be invoiced again, but not twice", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := dailyJob(t, c)
		first := invoiceFor(t, c, job, today)
		pay(c, first)
		c.post("/jobs/"+job+"/occurrences/"+today+"/invoice", draftBody).expect(t, 409) // paid still holds it
		c.patch("/invoices/"+first+"/status", `{"status":"refunded"}`).expect(t, 200)
		second := invoiceFor(t, c, job, today)
		c.post("/jobs/"+job+"/occurrences/"+today+"/invoice", draftBody).expect(t, 409) // the new live one holds it
		if first == second || invoiceStatus(t, c, first) != "refunded" || invoiceStatus(t, c, second) != "draft" {
			t.Fatal("the refunded invoice must stay as it is")
		}
	})

	t.Run("a refunded invoice is not flagged as paid when the visit is canceled", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := dailyJob(t, c)
		inv := invoiceFor(t, c, job, today)
		pay(c, inv)
		c.patch("/invoices/"+inv+"/status", `{"status":"refunded"}`).expect(t, 200)
		res := c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"canceled"}`).expect(t, 200)
		if _, has := res.obj()["paidInvoiceIds"]; has {
			t.Fatalf("flagged a refunded invoice: %s", res.raw)
		}
		if invoiceStatus(t, c, inv) != "refunded" {
			t.Fatal("the refunded invoice changed")
		}
	})

	t.Run("a refund opens a one-off estimate for edits again", func(t *testing.T) {
		c, _, _ := newClient(t)
		est, job := approvedEstimate(t, c)
		inv := invoiceFor(t, c, job, civil.Format(civil.Today()))
		pay(c, inv)
		c.patch("/estimates/"+est, `{"notes":"locked"}`).expect(t, 409)
		c.patch("/invoices/"+inv+"/status", `{"status":"refunded"}`).expect(t, 200)
		c.patch("/estimates/"+est, `{"notes":"open again"}`).expect(t, 200)
	})
}

func TestE2EEstimateRevisions(t *testing.T) {
	c, _, _ := newClient(t)
	est := c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)
	revisions := func() []any { return c.get("/estimates/"+est+"/revisions").expect(t, 200).list() }

	if got := revisions(); got == nil || len(got) != 0 {
		t.Fatalf("a new estimate has no revisions: %v", got)
	}

	// A draft was never shown: editing it leaves nothing behind.
	c.patch("/estimates/"+est, `{"notes":"draft edit"}`).expect(t, 200)
	if len(revisions()) != 0 || c.get("/estimates/" + est).obj()["revision"] != float64(1) {
		t.Fatal("a draft edit must not create a revision")
	}

	c.patch("/estimates/"+est+"/status", `{"status":"sent"}`).expect(t, 200)
	second := c.patch("/estimates/"+est, `{"notes":"second","lineItems":[{"description":"Deep clean","quantity":1,"unitPriceCents":20000}]}`).expect(t, 200).obj()
	if second["revision"] != float64(2) || second["totalCents"] != float64(20000) {
		t.Fatalf("after the first edit: %v", second)
	}
	c.patch("/estimates/"+est, `{"notes":"third"}`).expect(t, 200)

	got := revisions()
	if len(got) != 2 {
		t.Fatalf("revisions: %v", got)
	}
	newest, oldest := got[0].(map[string]any), got[1].(map[string]any)
	if newest["revision"] != float64(2) || newest["notes"] != "second" || newest["totalCents"] != float64(20000) {
		t.Fatalf("newest: %v", newest)
	}
	if oldest["revision"] != float64(1) || oldest["notes"] != "draft edit" || oldest["totalCents"] != float64(17500) || len(oldest["lineItems"].([]any)) != 2 ||
		oldest["customerId"] != c.refs.customer || oldest["replacedAt"] == nil {
		t.Fatalf("oldest: %v", oldest)
	}
	if now := c.get("/estimates/" + est).obj(); now["revision"] != float64(3) || now["notes"] != "third" {
		t.Fatalf("current: %v", now)
	}

	t.Run("a refused edit keeps no revision and does not move the counter", func(t *testing.T) {
		before := len(revisions())
		c.patch("/estimates/"+est, `{"lineItems":[]}`).expect(t, 400) // a sent estimate must keep something to bill
		if len(revisions()) != before || c.get("/estimates/" + est).obj()["revision"] != float64(3) {
			t.Fatal("a refused edit left a trace")
		}
	})

	t.Run("editing an approved estimate is traced too, and unknown ids are refused", func(t *testing.T) {
		approved, _ := approvedEstimate(t, c)
		c.patch("/estimates/"+approved, `{"notes":"after approval"}`).expect(t, 200)
		if got := c.get("/estimates/"+approved+"/revisions").expect(t, 200).list(); len(got) != 1 {
			t.Fatalf("revisions: %v", got)
		}
		c.get("/estimates/"+unknownID+"/revisions").expect(t, 404)
		c.get("/estimates/nope/revisions").expect(t, 400)
	})
}

func TestE2EReopenAnApproval(t *testing.T) {
	today := civil.Format(civil.Today())

	t.Run("a fresh approval is undone: no job, back to sent, and it can be approved again", func(t *testing.T) {
		c, db, schema := newClient(t)
		est, job := approvedEstimate(t, c)
		res := c.post("/estimates/"+est+"/reopen", "").expect(t, 200)
		res.envelope(t)
		if res.obj()["status"] != "sent" || res.obj()["jobId"] != nil || res.obj()["jobSnapshot"] != nil || res.obj()["sentAt"] == nil {
			t.Fatalf("reopened: %s", res.raw)
		}
		c.get("/jobs/"+job+"/occurrences").expect(t, 404)
		if n := tenantScalar(t, db, schema, `SELECT count(*) FROM jobs`); n != 0 {
			t.Fatalf("%d jobs left", n)
		}
		c.patch("/estimates/"+est, `{"notes":"fixed the mistake"}`).expect(t, 200)
		again := c.post("/estimates/"+est+"/approve", fmt.Sprintf(`{"date":%q}`, civil.Format(civil.AddDays(civil.Today(), 2)))).expect(t, 200).obj()
		if again["status"] != "approved" || again["jobId"] == job || again["jobId"] == nil {
			t.Fatalf("approved again: %v", again)
		}
	})

	t.Run("it is refused once the job has an occurrence change, and nothing moves", func(t *testing.T) {
		c, db, schema := newClient(t)
		est, job := approvedEstimate(t, c)
		c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"confirmed"}`).expect(t, 200)
		refused := c.post("/estimates/"+est+"/reopen", "").expect(t, 409)
		refused.envelope(t)
		got := c.get("/estimates/"+est).expect(t, 200).obj()
		if got["status"] != "approved" || got["jobId"] != job || tenantScalar(t, db, schema, `SELECT count(*) FROM jobs`) != 1 {
			t.Fatalf("a refused reopen changed something: %v", got)
		}
	})

	t.Run("it is refused once the job has any invoice, even a void one", func(t *testing.T) {
		c, _, _ := newClient(t)
		est, job := approvedEstimate(t, c)
		inv := invoiceFor(t, c, job, today)
		c.post("/estimates/"+est+"/reopen", "").expect(t, 409)
		c.patch("/invoices/"+inv+"/status", `{"status":"void"}`).expect(t, 200)
		c.post("/estimates/"+est+"/reopen", "").expect(t, 409)
		// Delete the draft instead and nothing references the job any more.
		draft := invoiceFor(t, c, job, today)
		c.del("/invoices/"+draft).expect(t, 200)
		c.post("/estimates/"+est+"/reopen", "").expect(t, 409) // the void one is still there
	})

	t.Run("a draft invoice that was deleted no longer blocks it", func(t *testing.T) {
		c, _, _ := newClient(t)
		est, job := approvedEstimate(t, c)
		inv := invoiceFor(t, c, job, today)
		c.del("/invoices/"+inv).expect(t, 200)
		c.post("/estimates/"+est+"/reopen", "").expect(t, 200)
	})

	t.Run("only an approved estimate can be reopened", func(t *testing.T) {
		c, _, _ := newClient(t)
		draft := c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)
		c.post("/estimates/"+draft+"/reopen", "").expect(t, 409)
		c.patch("/estimates/"+draft+"/status", `{"status":"sent"}`).expect(t, 200)
		c.post("/estimates/"+draft+"/reopen", "").expect(t, 409)
		c.patch("/estimates/"+draft+"/status", `{"status":"declined"}`).expect(t, 200)
		c.post("/estimates/"+draft+"/reopen", "").expect(t, 409)
		c.post("/estimates/"+unknownID+"/reopen", "").expect(t, 404)
		c.post("/estimates/nope/reopen", "").expect(t, 400)
	})

	t.Run("a reopened estimate can be reopened only once", func(t *testing.T) {
		c, _, _ := newClient(t)
		est, _ := approvedEstimate(t, c)
		c.post("/estimates/"+est+"/reopen", "").expect(t, 200)
		c.post("/estimates/"+est+"/reopen", "").expect(t, 409)
	})
}

func TestE2EDeleteDrafts(t *testing.T) {
	today := civil.Format(civil.Today())
	c, _, _ := newClient(t)

	t.Run("a draft estimate is deleted with its lines", func(t *testing.T) {
		id := c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)
		res := c.del("/estimates/"+id).expect(t, 200)
		res.envelope(t)
		if res.obj()["id"] != id {
			t.Fatalf("response: %s", res.raw)
		}
		c.get("/estimates/"+id).expect(t, 404)
		c.del("/estimates/"+id).expect(t, 404)
	})

	t.Run("anything past draft is refused", func(t *testing.T) {
		id := c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)
		c.patch("/estimates/"+id+"/status", `{"status":"sent"}`).expect(t, 200)
		c.del("/estimates/"+id).expect(t, 409).envelope(t)
		c.patch("/estimates/"+id+"/status", `{"status":"declined"}`).expect(t, 200)
		c.del("/estimates/"+id).expect(t, 409)
		c.get("/estimates/"+id).expect(t, 200)

		approved, _ := approvedEstimate(t, c)
		c.del("/estimates/"+approved).expect(t, 409)
	})

	t.Run("an invoice is deleted only as a draft, and its occurrence is free again", func(t *testing.T) {
		job := dailyJob(t, c)
		inv := invoiceFor(t, c, job, today)
		c.del("/invoices/"+inv).expect(t, 200)
		c.get("/invoices/"+inv).expect(t, 404)
		again := invoiceFor(t, c, job, today)
		c.patch("/invoices/"+again+"/status", `{"status":"sent"}`).expect(t, 200)
		c.del("/invoices/"+again).expect(t, 409)
		c.patch("/invoices/"+again+"/status", `{"status":"paid"}`).expect(t, 200)
		c.del("/invoices/"+again).expect(t, 409)
		c.patch("/invoices/"+again+"/status", `{"status":"refunded"}`).expect(t, 200)
		c.del("/invoices/"+again).expect(t, 409)
	})

	t.Run("one type's endpoint never deletes the other's", func(t *testing.T) {
		id := c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)
		c.del("/invoices/"+id).expect(t, 404)
		c.get("/estimates/"+id).expect(t, 200)
		c.del("/estimates/nope").expect(t, 400)
	})
}

func TestE2EListFilters(t *testing.T) {
	c, _, _ := newClient(t) // customer "Ada Lovelace"
	today := civil.Format(civil.Today())

	// A second customer with a location of their own.
	bob := c.post("/customers", `{"name":"Bob 100%"}`).expect(t, 200).obj()["id"].(string)
	bobLoc := c.post("/customers/"+bob+"/locations", `{"addressLine1":"2 Side Road"}`).expect(t, 200).obj()["id"].(string)
	bobEst := c.post("/estimates", fmt.Sprintf(`{"customerId":%q,"locationId":%q,"serviceTypeId":%q,"lineItems":[{"description":"x","quantity":1,"unitPriceCents":100}]}`, bob, bobLoc, c.refs.serviceType)).expect(t, 200).obj()
	adaEst := c.post("/estimates", draftBody).expect(t, 200).obj()
	c.patch("/estimates/"+adaEst["id"].(string)+"/status", `{"status":"sent"}`).expect(t, 200)

	ids := func(r resp) map[string]bool {
		out := map[string]bool{}
		for _, v := range r.expect(t, 200).list() {
			out[v.(map[string]any)["id"].(string)] = true
		}
		return out
	}
	only := func(t *testing.T, got map[string]bool, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for _, id := range want {
			if !got[id] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	}
	bobID, adaID := bobEst["id"].(string), adaEst["id"].(string)
	q := func(path string, params ...string) string {
		v := url.Values{}
		for i := 0; i < len(params); i += 2 {
			v.Set(params[i], params[i+1])
		}
		return path + "?" + v.Encode()
	}

	t.Run("estimates by customer, location, status and text", func(t *testing.T) {
		only(t, ids(c.get(q("/estimates", "customerId", bob))), bobID)
		only(t, ids(c.get(q("/estimates", "locationId", c.refs.location))), adaID)
		only(t, ids(c.get(q("/estimates", "status", "sent"))), adaID)
		only(t, ids(c.get(q("/estimates", "q", "lovelace"))), adaID)
		only(t, ids(c.get(q("/estimates", "q", "100%"))), bobID)
		only(t, ids(c.get(q("/estimates", "q", "%"))), bobID) // a wildcard is literal
		only(t, ids(c.get(q("/estimates", "q", adaEst["number"].(string)))), adaID)
		only(t, ids(c.get(q("/estimates", "q", "nobody"))))
		only(t, ids(c.get(q("/estimates", "customerId", bob, "status", "sent"))))
	})

	t.Run("invoices by job, occurrence range and text", func(t *testing.T) {
		job, other := dailyJob(t, c), dailyJob(t, c)
		later := civil.Format(civil.AddDays(civil.Today(), 2))
		c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"completed"}`).expect(t, 200)
		a := invoiceFor(t, c, job, today)
		b := invoiceFor(t, c, other, today)
		c.patch("/jobs/"+other+"/occurrences/"+today, `{"status":"completed"}`).expect(t, 200)
		d := invoiceFor(t, c, other, civil.Format(civil.AddDays(civil.Today(), 1)))
		_ = later
		only(t, ids(c.get(q("/invoices", "jobId", job))), a)
		only(t, ids(c.get(q("/invoices", "jobId", other))), b, d)
		only(t, ids(c.get(q("/invoices", "occurrenceFrom", civil.Format(civil.AddDays(civil.Today(), 1))))), d)
		only(t, ids(c.get(q("/invoices", "occurrenceTo", today))), a, b)
		only(t, ids(c.get(q("/invoices", "occurrenceFrom", today, "occurrenceTo", today))), a, b)
		only(t, ids(c.get(q("/invoices", "jobId", other, "occurrenceFrom", today, "occurrenceTo", today))), b)
		only(t, ids(c.get(q("/invoices", "q", "lovelace"))), a, b, d)
		only(t, ids(c.get(q("/invoices", "q", "INV-000002"))), b)
		only(t, ids(c.get(q("/invoices", "customerId", bob))))
	})

	t.Run("bad filters are 400s", func(t *testing.T) {
		for _, path := range []string{
			q("/estimates", "customerId", "nope"),
			q("/estimates", "locationId", "nope"),
			q("/invoices", "jobId", "nope"),
			q("/invoices", "occurrenceFrom", "2026-13-40"),
			q("/invoices", "occurrenceTo", "tomorrow"),
			q("/invoices", "occurrenceFrom", "2026-10-09", "occurrenceTo", "2026-10-01"),
			q("/estimates", "occurrenceFrom", today),
			q("/invoices", "q", string(make([]byte, 0))+fmt.Sprintf("%0101d", 0)),
			q("/invoices", "status", "approved"),
		} {
			c.get(path).expect(t, 400).envelope(t)
		}
	})
}

// Undoing an approval and the things that need the job to exist must never
// both succeed, and a lost race is a clean 4xx, never a 500.
func TestE2EReopenRacingOtherChanges(t *testing.T) {
	today := civil.Format(civil.Today())

	t.Run("reopen vs creating an invoice", func(t *testing.T) {
		c, db, schema := newClient(t)
		for round := 0; round < 25; round++ {
			est, job := approvedEstimate(t, c)
			var invoice, reopen resp
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				invoice = c.post("/jobs/"+job+"/occurrences/"+today+"/invoice", draftBody)
			}()
			go func() {
				defer wg.Done()
				reopen = c.post("/estimates/"+est+"/reopen", "")
			}()
			wg.Wait()

			if (invoice.code == 200) == (reopen.code == 200) {
				t.Fatalf("round %d: invoice %d (%s), reopen %d (%s): exactly one must win", round, invoice.code, invoice.raw, reopen.code, reopen.raw)
			}
			for name, r := range map[string]resp{"invoice": invoice, "reopen": reopen} {
				if r.code != 200 && r.code != 404 && r.code != 409 {
					t.Fatalf("round %d: %s answered %d: %s", round, name, r.code, r.raw)
				}
			}
			got := c.get("/estimates/"+est).expect(t, 200).obj()
			if reopen.code == 200 {
				if got["status"] != "sent" || got["jobId"] != nil || tenantScalar(t, db, schema, `SELECT count(*) FROM jobs WHERE id = ?`, job) != 0 {
					t.Fatalf("round %d: reopened but %v", round, got)
				}
			} else if got["status"] != "approved" || got["jobId"] != job || tenantScalar(t, db, schema, `SELECT count(*) FROM customer_documents WHERE type = 'invoice' AND job_id = ?`, job) != 1 {
				t.Fatalf("round %d: invoice won but %v", round, got)
			}
		}
		if n := tenantScalar(t, db, schema, `SELECT count(*) FROM customer_documents d WHERE type = 'invoice' AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.id = d.job_id)`); n != 0 {
			t.Fatalf("%d invoices without a job", n)
		}
	})

	t.Run("reopen vs changing the occurrence", func(t *testing.T) {
		c, db, schema := newClient(t)
		for round := 0; round < 25; round++ {
			est, job := approvedEstimate(t, c)
			var patch, reopen resp
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				patch = c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"confirmed"}`)
			}()
			go func() {
				defer wg.Done()
				reopen = c.post("/estimates/"+est+"/reopen", "")
			}()
			wg.Wait()

			if (patch.code == 200) == (reopen.code == 200) {
				t.Fatalf("round %d: patch %d (%s), reopen %d (%s): exactly one must win", round, patch.code, patch.raw, reopen.code, reopen.raw)
			}
			for name, r := range map[string]resp{"patch": patch, "reopen": reopen} {
				if r.code != 200 && r.code != 404 && r.code != 409 {
					t.Fatalf("round %d: %s answered %d: %s", round, name, r.code, r.raw)
				}
			}
			if reopen.code == 200 && tenantScalar(t, db, schema, `SELECT count(*) FROM job_occurrences WHERE job_id = ?`, job) != 0 {
				t.Fatalf("round %d: occurrence rows survived the job", round)
			}
		}
	})

	t.Run("two reopens at once: one wins", func(t *testing.T) {
		c, db, schema := newClient(t)
		for round := 0; round < 25; round++ {
			est, _ := approvedEstimate(t, c)
			codes := make([]int, 2)
			var wg sync.WaitGroup
			for i := range codes {
				wg.Add(1)
				go func() {
					defer wg.Done()
					codes[i] = c.post("/estimates/"+est+"/reopen", "").code
				}()
			}
			wg.Wait()
			if codes[0]+codes[1] != 200+409 {
				t.Fatalf("round %d: %v", round, codes)
			}
		}
		if n := tenantScalar(t, db, schema, `SELECT count(*) FROM jobs`); n != 0 {
			t.Fatalf("%d jobs left", n)
		}
	})

	t.Run("approve vs delete of a draft never leaves a job without its estimate", func(t *testing.T) {
		c, db, schema := newClient(t)
		for round := 0; round < 25; round++ {
			id := c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)
			var approve, del resp
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				approve = c.post("/estimates/"+id+"/approve", fmt.Sprintf(`{"date":%q}`, today))
			}()
			go func() {
				defer wg.Done()
				del = c.del("/estimates/" + id)
			}()
			wg.Wait()
			if (approve.code == 200) == (del.code == 200) {
				t.Fatalf("round %d: approve %d (%s), delete %d (%s): exactly one must win", round, approve.code, approve.raw, del.code, del.raw)
			}
			if approve.code != 200 && approve.code != 404 && approve.code != 409 || del.code != 200 && del.code != 404 && del.code != 409 {
				t.Fatalf("round %d: approve %d, delete %d", round, approve.code, del.code)
			}
		}
		jobs := tenantScalar(t, db, schema, `SELECT count(*) FROM jobs`)
		tied := tenantScalar(t, db, schema, `SELECT count(*) FROM customer_documents WHERE type = 'estimate' AND job_id IS NOT NULL`)
		if jobs != tied {
			t.Fatalf("%d jobs but %d estimates tied to a job", jobs, tied)
		}
	})
}
