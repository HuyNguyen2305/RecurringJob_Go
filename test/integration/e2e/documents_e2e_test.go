package e2e_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"gorm.io/gorm"

	"recurringjob/internal/app"
	"recurringjob/internal/common/auth"
	"recurringjob/internal/common/civil"
	"recurringjob/test/helpers"
)

const draftBody = `{"notes":"first visit","lineItems":[{"description":"Window cleaning","quantity":2,"unitPriceCents":7500},{"description":"Gutter check","quantity":1,"unitPriceCents":2500}]}`

func TestE2EEstimateLifecycle(t *testing.T) {
	c, _, _ := newClient(t)
	today := civil.Today()
	date := civil.Format(civil.AddDays(today, 3))

	created := c.post("/estimates", draftBody).expect(t, 200)
	created.envelope(t)
	est := created.obj()
	id, _ := est["id"].(string)
	if !uuidRe.MatchString(id) || est["type"] != "estimate" || est["status"] != "draft" || nameOf(est["customer"]) != "Ada Lovelace" || streetOf(est["location"]) != "1 Main Street" || nameOf(est["serviceType"]) != "Window cleaning" ||
		est["jobId"] != nil || est["jobSnapshot"] != nil || est["occurrenceDate"] != nil ||
		est["totalCents"] != float64(17500) || est["subtotalCents"] != float64(17500) {
		t.Fatalf("created: %s", created.raw)
	}
	items := est["lineItems"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["description"] != "Window cleaning" || items[0].(map[string]any)["totalCents"] != float64(15000) {
		t.Fatalf("items: %s", created.raw)
	}

	t.Run("get and list", func(t *testing.T) {
		got := c.get("/estimates/"+id).expect(t, 200)
		got.envelope(t)
		if got.obj()["id"] != id || len(got.obj()["lineItems"].([]any)) != 2 {
			t.Fatalf("get: %s", got.raw)
		}
		list := c.get("/estimates").expect(t, 200)
		list.envelope(t)
		if len(list.list()) != 1 || list.list()[0].(map[string]any)["id"] != id {
			t.Fatalf("list: %s", list.raw)
		}
		if n := len(c.get("/estimates?status=sent").expect(t, 200).list()); n != 0 {
			t.Fatalf("status filter matched %d", n)
		}
		if n := len(c.get("/invoices").expect(t, 200).list()); n != 0 {
			t.Fatalf("estimates leaked into the invoice list: %d", n)
		}
	})

	t.Run("a draft can be edited and its line items replaced", func(t *testing.T) {
		upd := c.patch("/estimates/"+id, `{"notes":"updated","lineItems":[{"description":"Deep clean","quantity":1,"unitPriceCents":20000}]}`).expect(t, 200)
		upd.envelope(t)
		if upd.obj()["notes"] != "updated" || nameOf(upd.obj()["customer"]) != "Ada Lovelace" || upd.obj()["totalCents"] != float64(20000) || len(upd.obj()["lineItems"].([]any)) != 1 {
			t.Fatalf("update: %s", upd.raw)
		}
	})

	t.Run("approval is not available through the status endpoint", func(t *testing.T) {
		c.patch("/estimates/"+id+"/status", `{"status":"approved"}`).expect(t, 400).envelope(t)
	})

	sent := c.patch("/estimates/"+id+"/status", `{"status":"sent"}`).expect(t, 200)
	sent.envelope(t)
	if sent.obj()["status"] != "sent" {
		t.Fatalf("sent: %s", sent.raw)
	}
	t.Run("a sent estimate can still be edited and stays sent", func(t *testing.T) {
		upd := c.patch("/estimates/"+id, `{"notes":"after sending"}`).expect(t, 200)
		upd.envelope(t)
		if upd.obj()["notes"] != "after sending" || upd.obj()["status"] != "sent" {
			t.Fatalf("update: %s", upd.raw)
		}
	})

	approved := c.post("/estimates/"+id+"/approve", fmt.Sprintf(`{"date":%q,"recurrence":%s}`, date, jsonRule(civil.AddDays(today, 3).Weekday()))).expect(t, 200)
	approved.envelope(t)
	jobID, _ := approved.obj()["jobId"].(string)
	snap, _ := approved.obj()["jobSnapshot"].(map[string]any)
	if approved.obj()["status"] != "approved" || !uuidRe.MatchString(jobID) || snap["id"] != jobID || snap["date"] != date || snap["status"] != "unconfirmed" {
		t.Fatalf("approved: %s", approved.raw)
	}

	t.Run("the job exists and follows the recurrence", func(t *testing.T) {
		occ := c.get("/jobs/"+jobID+"/occurrences?limit=2").expect(t, 200)
		if got := fmt.Sprint(occ.list()); got != fmt.Sprint([]any{date, civil.Format(civil.AddDays(civil.AddDays(today, 3), 7))}) {
			t.Fatalf("occurrences %v", got)
		}
	})

	t.Run("an approved estimate cannot be approved or declined again, but can still be edited", func(t *testing.T) {
		c.post("/estimates/"+id+"/approve", fmt.Sprintf(`{"date":%q}`, date)).expect(t, 409).envelope(t)
		c.patch("/estimates/"+id+"/status", `{"status":"declined"}`).expect(t, 409).envelope(t)
		upd := c.patch("/estimates/"+id, `{"notes":"adjusted after approval"}`).expect(t, 200)
		if upd.obj()["status"] != "approved" || upd.obj()["jobId"] != jobID || upd.obj()["notes"] != "adjusted after approval" {
			t.Fatalf("edit after approval: %s", upd.raw)
		}
	})
}

func TestE2EEstimateValidation(t *testing.T) {
	c, _, _ := newClient(t)
	valid := c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)
	empty := c.post("/estimates", `{"notes":"Empty"}`).expect(t, 200).obj()["id"].(string)
	noRefs := c
	noRefs.refs = nil
	// A second customer with a location of their own.
	otherCust := c.post("/customers", `{"name":"Bob"}`).expect(t, 200).obj()["id"].(string)
	otherLoc := c.post("/customers/"+otherCust+"/locations", `{"addressLine1":"2 Side Street"}`).expect(t, 200).obj()["id"].(string)

	for name, r := range map[string]resp{
		"no references":                        noRefs.post("/estimates", `{}`),
		"missing customer":                     noRefs.post("/estimates", `{"locationId":"`+c.refs.location+`","serviceTypeId":"`+c.refs.serviceType+`"}`),
		"malformed customer id":                c.post("/estimates", `{"customerId":"nope"}`),
		"location of another customer":         c.post("/estimates", `{"locationId":"`+otherLoc+`"}`),
		"patch to another customer's location": c.patch("/estimates/"+valid, `{"locationId":"`+otherLoc+`"}`),
		"approve without a start time":         noRefs.post("/estimates/"+valid+"/approve", `{"date":"`+soon()+`"}`),
		"approve with a bad start time":        c.post("/estimates/"+valid+"/approve", `{"date":"`+soon()+`","startTime":"25:00"}`),
		"approve with no length":               noRefs.post("/estimates/"+valid+"/approve", `{"date":"`+soon()+`","startTime":"09:00"}`),
		"zero quantity":                        c.post("/estimates", `{"lineItems":[{"description":"x","quantity":0}]}`),
		"negative price":                       c.post("/estimates", `{"lineItems":[{"description":"x","quantity":1,"unitPriceCents":-5}]}`),
		"missing description":                  c.post("/estimates", `{"lineItems":[{"quantity":1}]}`),
		"bad json":                             c.post("/estimates", `{`),
		"malformed id":                         c.get("/estimates/nope"),
		"empty patch":                          c.patch("/estimates/"+valid, `{}`),
		"unknown status":                       c.patch("/estimates/"+valid+"/status", `{"status":"bogus"}`),
		"invoice status":                       c.patch("/estimates/"+valid+"/status", `{"status":"paid"}`),
		"missing status":                       c.patch("/estimates/"+valid+"/status", `{}`),
		"sending with no items":                c.patch("/estimates/"+empty+"/status", `{"status":"sent"}`),
		"approve no items":                     c.post("/estimates/"+empty+"/approve", `{"date":"`+soon()+`"}`),
		"approve bad date":                     c.post("/estimates/"+valid+"/approve", `{"date":"12/10/2026"}`),
		"approve no date":                      c.post("/estimates/"+valid+"/approve", `{}`),
		"approve bad rule":                     c.post("/estimates/"+valid+"/approve", `{"date":"`+soon()+`","recurrence":{"frequency":"weekly"}}`),
		"list bad limit":                       c.get("/estimates?limit=0x"),
		"list limit too big":                   c.get("/estimates?limit=201"),
		"list unknown status":                  c.get("/estimates?status=paid"),
		"list negative offset":                 c.get("/estimates?offset=-1"),
	} {
		if r.code != 400 {
			t.Errorf("%s: status %d: %s", name, r.code, r.raw)
			continue
		}
		r.envelope(t)
	}

	for name, r := range map[string]resp{
		"unknown customer":     c.post("/estimates", `{"customerId":"`+unknownID+`"}`),
		"unknown location":     c.post("/estimates", `{"locationId":"`+unknownID+`"}`),
		"unknown service type": c.post("/estimates", `{"serviceTypeId":"`+unknownID+`"}`),
		"get unknown":          c.get("/estimates/" + unknownID),
		"patch unknown":        c.patch("/estimates/"+unknownID, `{"notes":"x"}`),
		"status unknown":       c.patch("/estimates/"+unknownID+"/status", `{"status":"sent"}`),
		"approve unknown":      c.post("/estimates/"+unknownID+"/approve", `{"date":"`+soon()+`"}`),
		"approve bad rule":     c.post("/estimates/"+valid+"/approve", `{"date":"`+soon()+`","recurrence":{"frequency":"daily","exceptType":"frequency","exceptJobId":"`+unknownID+`"}}`),
	} {
		if r.code != 404 {
			t.Errorf("%s: status %d: %s", name, r.code, r.raw)
			continue
		}
		r.envelope(t)
	}

	t.Run("a rejected approval leaves the estimate draft and creates no job", func(t *testing.T) {
		got := c.get("/estimates/"+valid).expect(t, 200).obj()
		if got["status"] != "draft" || got["jobId"] != nil {
			t.Fatalf("estimate changed: %v", got)
		}
	})

	t.Run("a job date in the past is refused, today is accepted", func(t *testing.T) {
		today := civil.Today()
		dated := c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)
		past := c.post("/estimates/"+dated+"/approve", fmt.Sprintf(`{"date":%q}`, civil.Format(civil.AddDays(today, -1)))).expect(t, 400)
		past.envelope(t)
		if got := c.get("/estimates/"+dated).expect(t, 200).obj(); got["status"] != "draft" || got["jobId"] != nil {
			t.Fatalf("the refused approval changed the estimate: %v", got)
		}
		onDay := c.post("/estimates/"+dated+"/approve", fmt.Sprintf(`{"date":%q}`, civil.Format(today))).expect(t, 200)
		if onDay.obj()["status"] != "approved" || onDay.obj()["jobId"] == nil {
			t.Fatalf("approval on the day itself: %s", onDay.raw)
		}
	})

	t.Run("declined estimates cannot be approved", func(t *testing.T) {
		c.patch("/estimates/"+valid+"/status", `{"status":"declined"}`).expect(t, 200)
		c.post("/estimates/"+valid+"/approve", `{"date":"`+soon()+`"}`).expect(t, 409).envelope(t)
	})
}

func TestE2EInvoiceLifecycle(t *testing.T) {
	c, _, _ := newClient(t)
	today := civil.Today()
	day := func(n int) string { return civil.Format(civil.AddDays(today, n)) }
	invoiceURL := func(job, date string) string { return "/jobs/" + job + "/occurrences/" + date + "/invoice" }

	job := c.post("/jobs", fmt.Sprintf(`{"date":%q,"recurrence":{"frequency":"daily"}}`, day(0))).expect(t, 200).obj()["id"].(string)

	created := c.post(invoiceURL(job, day(0)), draftBody).expect(t, 200)
	created.envelope(t)
	inv := created.obj()
	id, _ := inv["id"].(string)
	snap, _ := inv["jobSnapshot"].(map[string]any)
	if !uuidRe.MatchString(id) || inv["type"] != "invoice" || inv["status"] != "draft" || inv["jobId"] != job || inv["occurrenceDate"] != day(0) ||
		snap["id"] != job || snap["date"] != day(0) || inv["totalCents"] != float64(17500) {
		t.Fatalf("created: %s", created.raw)
	}

	t.Run("the job is snapshotted, not referenced live", func(t *testing.T) {
		got := c.get("/invoices/"+id).expect(t, 200).obj()
		rule, _ := got["jobSnapshot"].(map[string]any)["recurrence"].(map[string]any)
		if rule["frequency"] != "daily" || rule["endsType"] != "never" {
			t.Fatalf("snapshot recurrence: %v", got["jobSnapshot"])
		}
	})

	t.Run("one invoice per occurrence", func(t *testing.T) {
		c.post(invoiceURL(job, day(0)), draftBody).expect(t, 409).envelope(t)
	})

	t.Run("the occurrence must exist and be available", func(t *testing.T) {
		c.post(invoiceURL(job, day(-1)), draftBody).expect(t, 404).envelope(t) // before the job started
		c.post(invoiceURL(job, day(1)), draftBody).expect(t, 409).envelope(t)  // still hollow: today's visit is open
		c.post(invoiceURL(unknownID, day(0)), draftBody).expect(t, 404).envelope(t)
		c.post(invoiceURL("nope", day(0)), draftBody).expect(t, 400).envelope(t)
		c.post(invoiceURL(job, "10-02-2026"), draftBody).expect(t, 400).envelope(t)
		c.post(invoiceURL(job, day(0)), `{"lineItems":[{"description":"x"}]}`).expect(t, 400).envelope(t)
	})

	t.Run("completing a visit makes the next one invoiceable", func(t *testing.T) {
		c.patch("/jobs/"+job+"/occurrences/"+day(0), `{"status":"completed"}`).expect(t, 200)
		c.post(invoiceURL(job, day(1)), `{"notes":"Next"}`).expect(t, 200).envelope(t)
	})

	t.Run("a canceled occurrence cannot be invoiced", func(t *testing.T) {
		c.patch("/jobs/"+job+"/occurrences/"+day(1), `{"status":"canceled"}`).expect(t, 200)
		c.post(invoiceURL(job, day(1)), `{"notes":"Again"}`).expect(t, 409).envelope(t)
	})

	t.Run("get, list and type separation", func(t *testing.T) {
		list := c.get("/invoices?limit=10").expect(t, 200)
		list.envelope(t)
		if len(list.list()) != 2 {
			t.Fatalf("list: %s", list.raw)
		}
		c.get("/estimates/"+id).expect(t, 404).envelope(t)
		if n := len(c.get("/estimates").expect(t, 200).list()); n != 0 {
			t.Fatalf("invoices leaked into the estimate list: %d", n)
		}
	})

	t.Run("status flow: draft -> sent -> paid, then final", func(t *testing.T) {
		c.patch("/invoices/"+id+"/status", `{"status":"paid"}`).expect(t, 409).envelope(t) // skipping sent
		c.patch("/invoices/"+id+"/status", `{"status":"approved"}`).expect(t, 400).envelope(t)
		c.patch("/invoices/"+id, `{"notes":"before sending"}`).expect(t, 200)
		c.patch("/invoices/"+id+"/status", `{"status":"sent"}`).expect(t, 200).envelope(t)
		c.patch("/invoices/"+id, `{"notes":"after sending"}`).expect(t, 409).envelope(t)
		paid := c.patch("/invoices/"+id+"/status", `{"status":"paid"}`).expect(t, 200)
		if paid.obj()["status"] != "paid" || paid.obj()["notes"] != "before sending" {
			t.Fatalf("paid: %s", paid.raw)
		}
		c.patch("/invoices/"+id+"/status", `{"status":"void"}`).expect(t, 409).envelope(t)
		if n := len(c.get("/invoices?status=paid").expect(t, 200).list()); n != 1 {
			t.Fatalf("status filter: %d", n)
		}
	})
}

// Approving does two writes - create the job, mark the estimate - in one
// transaction. With several simultaneous approvals exactly one may win, and
// the losers must not leave a job behind.
func TestE2EConcurrentApproveCreatesOneJob(t *testing.T) {
	c, db, schema := newClient(t)
	ctx := auth.WithTenantSchema(context.Background(), schema)
	id := c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)
	c.patch("/estimates/"+id+"/status", `{"status":"sent"}`).expect(t, 200)

	const n = 6
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = c.post("/estimates/"+id+"/approve", `{"date":"`+soon()+`"}`).code
		}()
	}
	wg.Wait()

	wins, conflicts := 0, 0
	for _, code := range codes {
		switch code {
		case 200:
			wins++
		case 409:
			conflicts++
		}
	}
	if wins != 1 || conflicts != n-1 {
		t.Fatalf("codes %v: want one 200 and %d 409s", codes, n-1)
	}
	if jobs := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM jobs`); jobs != 1 {
		t.Fatalf("%d jobs exist, want exactly 1 (losing approvals must roll their job back)", jobs)
	}
	got := c.get("/estimates/"+id).expect(t, 200).obj()
	if got["status"] != "approved" || got["jobId"] == nil {
		t.Fatalf("estimate: %v", got)
	}
}

func TestE2EDocumentsAreTenantScoped(t *testing.T) {
	db := helpers.Connect(t)
	a, _ := helpers.NewSchema(t, db)
	b, _ := helpers.NewSchema(t, db)
	// The default schema is a; a request naming schema b must not see a's data.
	srv := newClientFor(t, db, a)
	id := srv.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)

	inB := srv
	inB.tenant = b
	inB.get("/estimates/"+id).expect(t, 404)
	if n := len(inB.get("/estimates").expect(t, 200).list()); n != 0 {
		t.Fatalf("tenant b sees %d of a's estimates", n)
	}
	srv.get("/estimates/"+id).expect(t, 200)
}

// newClientFor serves the production router over db with schema as the
// default tenant.
func newClientFor(t *testing.T, db *gorm.DB, schema string) client {
	t.Helper()
	c := client{t: t, h: app.NewServer(db, schema)}
	c.refs = seedRefs(t, c)
	return c
}

// soon is a job date a week from today (UTC): always valid for approval.
func soon() string { return civil.Format(civil.AddDays(civil.Today(), 7)) }

// dailyJob creates a job that starts today and repeats daily, and returns its id.
func dailyJob(t *testing.T, c client) string {
	t.Helper()
	body := fmt.Sprintf(`{"date":%q,"recurrence":{"frequency":"daily"}}`, civil.Format(civil.Today()))
	return c.post("/jobs", body).expect(t, 200).obj()["id"].(string)
}

func invoiceFor(t *testing.T, c client, jobID, date string) string {
	t.Helper()
	return c.post("/jobs/"+jobID+"/occurrences/"+date+"/invoice", draftBody).expect(t, 200).obj()["id"].(string)
}

func invoiceStatus(t *testing.T, c client, id string) string {
	t.Helper()
	return c.get("/invoices/"+id).expect(t, 200).obj()["status"].(string)
}

func TestE2ECancelingAnOccurrenceVoidsItsInvoices(t *testing.T) {
	today := civil.Format(civil.Today())

	t.Run("canceled: draft and sent invoices are voided", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := dailyJob(t, c)
		draft := invoiceFor(t, c, job, today)
		// A voided invoice no longer blocks a new one, so a second invoice
		// for the occurrence can be sent and then voided in the same way.
		c.patch("/invoices/"+draft+"/status", `{"status":"void"}`).expect(t, 200)
		sent := invoiceFor(t, c, job, today)
		c.patch("/invoices/"+sent+"/status", `{"status":"sent"}`).expect(t, 200)

		res := c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"canceled"}`).expect(t, 200)
		res.envelope(t)
		if _, has := res.obj()["paidInvoiceIds"]; has {
			t.Fatalf("nothing was paid, so nothing should be flagged: %s", res.raw)
		}
		if got := invoiceStatus(t, c, sent); got != "void" {
			t.Fatalf("sent invoice is %s, want void", got)
		}
		c.patch("/invoices/"+sent+"/status", `{"status":"paid"}`).expect(t, 409)
		c.post("/jobs/"+job+"/occurrences/"+today+"/invoice", draftBody).expect(t, 409) // canceled occurrence
	})

	t.Run("a draft invoice is voided too", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := dailyJob(t, c)
		draft := invoiceFor(t, c, job, today)
		c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"canceled"}`).expect(t, 200)
		if got := invoiceStatus(t, c, draft); got != "void" {
			t.Fatalf("draft invoice is %s, want void", got)
		}
	})

	t.Run("terminating the service voids them as well", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := dailyJob(t, c)
		inv := invoiceFor(t, c, job, today)
		c.patch("/invoices/"+inv+"/status", `{"status":"sent"}`).expect(t, 200)
		c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"terminate_service"}`).expect(t, 200).envelope(t)
		if got := invoiceStatus(t, c, inv); got != "void" {
			t.Fatalf("invoice is %s, want void", got)
		}
	})

	t.Run("a paid invoice is kept and flagged", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := dailyJob(t, c)
		inv := invoiceFor(t, c, job, today)
		c.patch("/invoices/"+inv+"/status", `{"status":"sent"}`).expect(t, 200)
		c.patch("/invoices/"+inv+"/status", `{"status":"paid"}`).expect(t, 200)

		res := c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"canceled"}`).expect(t, 200)
		res.envelope(t)
		flagged, _ := res.obj()["paidInvoiceIds"].([]any)
		if len(flagged) != 1 || flagged[0] != inv {
			t.Fatalf("paidInvoiceIds: %s", res.raw)
		}
		if got := invoiceStatus(t, c, inv); got != "paid" {
			t.Fatalf("paid invoice is %s, want paid (the money was received)", got)
		}
	})

	t.Run("only that occurrence's invoices are affected", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := dailyJob(t, c)
		other := dailyJob(t, c)
		mine := invoiceFor(t, c, job, today)
		theirs := invoiceFor(t, c, other, today)
		c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"canceled"}`).expect(t, 200)
		if invoiceStatus(t, c, mine) != "void" || invoiceStatus(t, c, theirs) != "draft" {
			t.Fatal("canceling one job's occurrence touched another job's invoice")
		}
	})

	t.Run("completing or confirming leaves the invoice alone", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := dailyJob(t, c)
		inv := invoiceFor(t, c, job, today)
		c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"confirmed"}`).expect(t, 200)
		c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"completed"}`).expect(t, 200)
		if got := invoiceStatus(t, c, inv); got != "draft" {
			t.Fatalf("invoice is %s, want draft", got)
		}
	})

	t.Run("cancelling a visit with no invoice just works", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := dailyJob(t, c)
		res := c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"canceled"}`).expect(t, 200)
		if _, has := res.obj()["paidInvoiceIds"]; has || res.obj()["status"] != "canceled" {
			t.Fatalf("response: %s", res.raw)
		}
	})
}

func TestE2EInvoiceCanBePaidBeforeTheWorkIsDone(t *testing.T) {
	c, _, _ := newClient(t)
	inThree := civil.Format(civil.AddDays(civil.Today(), 3))
	job := c.post("/jobs", fmt.Sprintf(`{"date":%q}`, inThree)).expect(t, 200).obj()["id"].(string)

	inv := invoiceFor(t, c, job, inThree) // the visit is days away and still unconfirmed
	c.patch("/invoices/"+inv+"/status", `{"status":"sent"}`).expect(t, 200)
	paid := c.patch("/invoices/"+inv+"/status", `{"status":"paid"}`).expect(t, 200)
	if paid.obj()["status"] != "paid" {
		t.Fatalf("paid: %s", paid.raw)
	}

	sched := c.get("/jobs/"+job+"/schedule").expect(t, 200).list()
	first, _ := sched[0].(map[string]any)
	if first["date"] != inThree || first["status"] != "unconfirmed" {
		t.Fatalf("payment must not touch the visit: %v", first)
	}
}

// A one-off job's estimate closes once its invoice is paid (a recurring
// job's never does: see TestE2ERecurringEstimateStaysEditable).
func TestE2EEstimateStaysEditableUntilAnInvoiceIsPaid(t *testing.T) {
	c, _, _ := newClient(t)
	today := civil.Format(civil.Today())

	est := c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)
	c.patch("/estimates/"+est+"/status", `{"status":"sent"}`).expect(t, 200)
	approved := c.post("/estimates/"+est+"/approve", fmt.Sprintf(`{"date":%q}`, today)).expect(t, 200).obj() // a one-off job
	job, _ := approved["jobId"].(string)

	edit := func(notes string, want int) resp {
		t.Helper()
		return c.patch("/estimates/"+est, fmt.Sprintf(`{"notes":%q}`, notes)).expect(t, want)
	}
	edit("approved, no invoice yet", 200)

	inv := invoiceFor(t, c, job, today)
	edit("invoice is a draft", 200)
	c.patch("/invoices/"+inv+"/status", `{"status":"sent"}`).expect(t, 200)
	edit("invoice is sent", 200)

	// Editing the estimate never rewrites the job or the invoice already issued.
	got := c.get("/invoices/"+inv).expect(t, 200).obj()
	if got["totalCents"] != float64(17500) {
		t.Fatalf("invoice changed: %v", got)
	}

	c.patch("/invoices/"+inv+"/status", `{"status":"paid"}`).expect(t, 200)
	closed := edit("too late", 409)
	closed.envelope(t)
	if e := c.get("/estimates/"+est).expect(t, 200).obj(); e["notes"] != "invoice is sent" || e["status"] != "approved" {
		t.Fatalf("a refused edit changed the estimate: %v", e)
	}

	t.Run("an unpaid invoice of another job does not close it", func(t *testing.T) {
		est2 := c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)
		c.patch("/estimates/"+est2+"/status", `{"status":"sent"}`).expect(t, 200)
		c.post("/estimates/"+est2+"/approve", fmt.Sprintf(`{"date":%q}`, today)).expect(t, 200)
		c.patch("/estimates/"+est2, `{"notes":"still open"}`).expect(t, 200)
	})
}

func TestE2EVoidedInvoiceCanBeReissued(t *testing.T) {
	c, _, _ := newClient(t)
	today := civil.Format(civil.Today())
	job := dailyJob(t, c)

	first := invoiceFor(t, c, job, today)
	c.post("/jobs/"+job+"/occurrences/"+today+"/invoice", draftBody).expect(t, 409) // a live one exists

	c.patch("/invoices/"+first+"/status", `{"status":"void"}`).expect(t, 200)
	second := invoiceFor(t, c, job, today)
	if second == first {
		t.Fatal("expected a new invoice")
	}
	c.post("/jobs/"+job+"/occurrences/"+today+"/invoice", draftBody).expect(t, 409) // the new one is live

	if n := len(c.get("/invoices").expect(t, 200).list()); n != 2 {
		t.Fatalf("%d invoices, want the voided one and the new one", n)
	}
	if invoiceStatus(t, c, first) != "void" || invoiceStatus(t, c, second) != "draft" {
		t.Fatal("wrong statuses")
	}
}

func TestE2EDocumentsNeedPricedItemsToBeSentOrApproved(t *testing.T) {
	c, _, _ := newClient(t)
	today := civil.Format(civil.Today())
	freeBody := `{"notes":"Free","lineItems":[{"description":"Free quote","quantity":1,"unitPriceCents":0}]}`

	est := c.post("/estimates", freeBody).expect(t, 200).obj()["id"].(string)
	c.patch("/estimates/"+est+"/status", `{"status":"sent"}`).expect(t, 400).envelope(t)
	c.post("/estimates/"+est+"/approve", fmt.Sprintf(`{"date":%q}`, today)).expect(t, 400).envelope(t)
	c.patch("/estimates/"+est, `{"lineItems":[{"description":"Real work","quantity":1,"unitPriceCents":5000}]}`).expect(t, 200)
	c.patch("/estimates/"+est+"/status", `{"status":"sent"}`).expect(t, 200)

	t.Run("a sent estimate cannot be edited down to nothing billable", func(t *testing.T) {
		c.patch("/estimates/"+est, `{"lineItems":[]}`).expect(t, 400).envelope(t)
		c.patch("/estimates/"+est, `{"lineItems":[{"description":"Free","quantity":1,"unitPriceCents":0}]}`).expect(t, 400)
		if got := c.get("/estimates/"+est).expect(t, 200).obj(); got["totalCents"] != float64(5000) {
			t.Fatalf("estimate changed: %v", got)
		}
	})

	t.Run("an invoice with only free lines cannot be sent", func(t *testing.T) {
		job := dailyJob(t, c)
		inv := c.post("/jobs/"+job+"/occurrences/"+today+"/invoice", freeBody).expect(t, 200).obj()["id"].(string)
		c.patch("/invoices/"+inv+"/status", `{"status":"sent"}`).expect(t, 400).envelope(t)
		if invoiceStatus(t, c, inv) != "draft" {
			t.Fatal("invoice left draft expected")
		}
	})
}

// A draft is edited to nothing while another request sends it. With the row
// locked the two run one after the other, so a sent document always still has
// something to bill - whichever request wins.
func TestE2ESendRacingAnEditNeverLeavesAnEmptySentDocument(t *testing.T) {
	c, _, _ := newClient(t)
	for round := 0; round < 25; round++ {
		id := c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			c.patch("/estimates/"+id, `{"lineItems":[]}`)
		}()
		go func() {
			defer wg.Done()
			c.patch("/estimates/"+id+"/status", `{"status":"sent"}`)
		}()
		wg.Wait()

		got := c.get("/estimates/"+id).expect(t, 200).obj()
		if got["status"] == "sent" && got["totalCents"] == float64(0) {
			t.Fatalf("round %d: a sent estimate with nothing to bill: %v", round, got)
		}
	}
}

// nameOf, streetOf: the name / address of an embedded customer, service type or
// location in a response, or "" when it is missing.
func nameOf(v any) string {
	m, _ := v.(map[string]any)
	s, _ := m["name"].(string)
	return s
}

func streetOf(v any) string {
	m, _ := v.(map[string]any)
	s, _ := m["addressLine1"].(string)
	return s
}

func TestE2ERecurringEstimateStaysEditable(t *testing.T) {
	c, _, _ := newClient(t)
	today := civil.Format(civil.Today())

	est := c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)
	c.patch("/estimates/"+est+"/status", `{"status":"sent"}`).expect(t, 200)
	approved := c.post("/estimates/"+est+"/approve", fmt.Sprintf(`{"date":%q,"recurrence":{"frequency":"daily"}}`, today)).expect(t, 200).obj()
	job, _ := approved["jobId"].(string)

	inv := invoiceFor(t, c, job, today)
	c.patch("/invoices/"+inv+"/status", `{"status":"sent"}`).expect(t, 200)
	c.patch("/invoices/"+inv+"/status", `{"status":"paid"}`).expect(t, 200)

	// One paid visit does not freeze the template of the whole series.
	c.patch("/estimates/"+est, `{"notes":"next visits cost more"}`).expect(t, 200)
	if e := c.get("/estimates/"+est).expect(t, 200).obj(); e["notes"] != "next visits cost more" {
		t.Fatalf("estimate: %v", e)
	}
	// The invoice that was paid is not rewritten.
	if got := c.get("/invoices/"+inv).expect(t, 200).obj(); got["status"] != "paid" || got["totalCents"] != float64(17500) {
		t.Fatalf("invoice: %v", got)
	}
}

func TestE2ERescheduleMovesUnpaidInvoices(t *testing.T) {
	today := civil.Format(civil.Today())
	later := civil.Format(civil.AddDays(civil.Today(), 3))
	reschedule := `{"status":"rescheduled","rescheduledTo":"` + later + `"}`
	oneOff := func(c client) string {
		return c.post("/jobs", fmt.Sprintf(`{"date":%q}`, today)).expect(t, 200).obj()["id"].(string)
	}
	dateOf := func(c client, id string) any { return c.get("/invoices/"+id).expect(t, 200).obj()["occurrenceDate"] }

	t.Run("a draft invoice follows the visit and can be worked on the new date", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := oneOff(c)
		inv := invoiceFor(t, c, job, today)
		res := c.patch("/jobs/"+job+"/occurrences/"+today, reschedule).expect(t, 200)
		res.envelope(t)
		if _, has := res.obj()["paidInvoiceIds"]; has {
			t.Fatalf("nothing was paid: %s", res.raw)
		}
		if dateOf(c, inv) != later || invoiceStatus(t, c, inv) != "draft" {
			t.Fatalf("invoice: %v", c.get("/invoices/"+inv).obj())
		}
		c.patch("/invoices/"+inv+"/status", `{"status":"sent"}`).expect(t, 200)
		// The old date is free of live invoices and refuses new ones (it was rescheduled).
		c.post("/jobs/"+job+"/occurrences/"+today+"/invoice", draftBody).expect(t, 409)
		// A second invoice for the new date is refused: the moved one is live there.
		c.post("/jobs/"+job+"/occurrences/"+later+"/invoice", draftBody).expect(t, 409)
	})

	t.Run("a sent invoice moves too", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := oneOff(c)
		inv := invoiceFor(t, c, job, today)
		c.patch("/invoices/"+inv+"/status", `{"status":"sent"}`).expect(t, 200)
		c.patch("/jobs/"+job+"/occurrences/"+today, reschedule).expect(t, 200)
		if dateOf(c, inv) != later || invoiceStatus(t, c, inv) != "sent" {
			t.Fatalf("invoice: %v", c.get("/invoices/"+inv).obj())
		}
	})

	t.Run("a paid invoice stays on the old date and is flagged", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := oneOff(c)
		inv := invoiceFor(t, c, job, today)
		c.patch("/invoices/"+inv+"/status", `{"status":"sent"}`).expect(t, 200)
		c.patch("/invoices/"+inv+"/status", `{"status":"paid"}`).expect(t, 200)
		res := c.patch("/jobs/"+job+"/occurrences/"+today, reschedule).expect(t, 200)
		flagged, _ := res.obj()["paidInvoiceIds"].([]any)
		if len(flagged) != 1 || flagged[0] != inv {
			t.Fatalf("paidInvoiceIds: %s", res.raw)
		}
		if dateOf(c, inv) != today || invoiceStatus(t, c, inv) != "paid" {
			t.Fatalf("invoice: %v", c.get("/invoices/"+inv).obj())
		}
	})

	t.Run("only that occurrence's invoices move", func(t *testing.T) {
		c, _, _ := newClient(t)
		job, other := oneOff(c), oneOff(c)
		mine, theirs := invoiceFor(t, c, job, today), invoiceFor(t, c, other, today)
		c.patch("/jobs/"+job+"/occurrences/"+today, reschedule).expect(t, 200)
		if dateOf(c, mine) != later || dateOf(c, theirs) != today {
			t.Fatal("rescheduling one job's visit moved another job's invoice")
		}
	})

	t.Run("a refused reschedule moves nothing", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := oneOff(c)
		inv := invoiceFor(t, c, job, today)
		c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"rescheduled","rescheduledTo":"`+today+`"}`).expect(t, 400)
		if dateOf(c, inv) != today {
			t.Fatalf("the invoice moved although the reschedule was refused: %v", dateOf(c, inv))
		}
	})
}

// Creating an invoice and changing the same occurrence's status at the same
// time must never leave a live invoice on a visit that no longer happens.
func TestE2EInvoiceRacingAStatusChange(t *testing.T) {
	today := civil.Format(civil.Today())
	later := civil.Format(civil.AddDays(civil.Today(), 3))
	c, _, _ := newClient(t)

	race := func(status string) (invoice resp, change resp, job string) {
		job = c.post("/jobs", fmt.Sprintf(`{"date":%q}`, today)).expect(t, 200).obj()["id"].(string)
		body := fmt.Sprintf(`{"status":%q}`, status)
		if status == "rescheduled" {
			body = `{"status":"rescheduled","rescheduledTo":"` + later + `"}`
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			invoice = c.post("/jobs/"+job+"/occurrences/"+today+"/invoice", draftBody)
		}()
		go func() {
			defer wg.Done()
			change = c.patch("/jobs/"+job+"/occurrences/"+today, body)
		}()
		wg.Wait()
		return invoice, change, job
	}

	for round := 0; round < 25; round++ {
		invoice, change, _ := race("canceled")
		if change.code != 200 {
			t.Fatalf("round %d: cancel %d: %s", round, change.code, change.raw)
		}
		switch invoice.code {
		case 409: // the cancel won; nothing was created
		case 200:
			id := invoice.obj()["id"].(string)
			if got := invoiceStatus(t, c, id); got != "void" {
				t.Fatalf("round %d: a live (%s) invoice was left on a canceled occurrence", round, got)
			}
		default:
			t.Fatalf("round %d: invoice %d: %s", round, invoice.code, invoice.raw)
		}
	}

	for round := 0; round < 25; round++ {
		invoice, change, _ := race("rescheduled")
		if change.code != 200 {
			t.Fatalf("round %d: reschedule %d: %s", round, change.code, change.raw)
		}
		switch invoice.code {
		case 409: // the reschedule won; nothing was created
		case 200:
			id := invoice.obj()["id"].(string)
			if got := c.get("/invoices/"+id).expect(t, 200).obj(); got["occurrenceDate"] != later || got["status"] != "draft" {
				t.Fatalf("round %d: the invoice was left behind on the rescheduled date: %v", round, got)
			}
		default:
			t.Fatalf("round %d: invoice %d: %s", round, invoice.code, invoice.raw)
		}
	}
}
