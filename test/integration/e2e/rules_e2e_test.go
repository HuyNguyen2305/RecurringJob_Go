package e2e_test

import (
	"fmt"
	"sort"
	"sync"
	"testing"

	"recurringjob/internal/common/civil"
)

// scheduleStatus returns the status of the schedule item on date, or "".
func scheduleStatus(t *testing.T, c client, jobID, date string) string {
	t.Helper()
	for _, it := range c.get("/jobs/"+jobID+"/schedule").expect(t, 200).list() {
		if m := it.(map[string]any); m["date"] == date {
			s, _ := m["status"].(string)
			return s
		}
	}
	return ""
}

func TestE2ERescheduleKeepsTheVisitsStatus(t *testing.T) {
	today := civil.Format(civil.Today())
	later := civil.Format(civil.AddDays(civil.Today(), 3))
	reschedule := `{"status":"rescheduled","rescheduledTo":"` + later + `"}`

	for _, status := range []string{"unconfirmed", "confirmed"} {
		t.Run("a "+status+" visit is "+status+" on its new date", func(t *testing.T) {
			c, _, _ := newClient(t)
			job := c.post("/jobs", `{"date":"`+today+`"}`).expect(t, 200).obj()["id"].(string)
			if status == "confirmed" {
				c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"confirmed"}`).expect(t, 200)
			}
			c.patch("/jobs/"+job+"/occurrences/"+today, reschedule).expect(t, 200)
			if got := scheduleStatus(t, c, job, later); got != status {
				t.Fatalf("the new date is %q, want %q", got, status)
			}
			if got := scheduleStatus(t, c, job, today); got != "rescheduled" {
				t.Fatalf("the old date is %q, want rescheduled", got)
			}
		})
	}
}

func TestE2EAJobStartsOpen(t *testing.T) {
	c, _, _ := newClient(t)
	for status, want := range map[string]int{
		"unconfirmed": 200, "confirmed": 200,
		"completed": 400, "canceled": 400, "terminate_service": 400, "rescheduled": 400, "in_progress": 400,
	} {
		r := c.post("/jobs", `{"date":"2026-10-02","status":"`+status+`"}`)
		if r.code != want {
			t.Errorf("%s: status %d, want %d (%s)", status, r.code, want, r.raw)
		}
		r.envelope(t)
	}
}

func TestE2EInvoiceStartsFromTheJobsEstimate(t *testing.T) {
	c, _, _ := newClient(t)
	today := civil.Format(civil.Today())
	est := c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)
	job := c.post("/estimates/"+est+"/approve", `{"date":"`+today+`","recurrence":{"frequency":"daily"}}`).expect(t, 200).obj()["jobId"].(string)
	create := func(body string) map[string]any {
		t.Helper()
		inv := c.post("/jobs/"+job+"/occurrences/"+today+"/invoice", body).expect(t, 200).obj()
		c.patch("/invoices/"+inv["id"].(string)+"/status", `{"status":"void"}`).expect(t, 200) // frees the occurrence for the next try
		return inv
	}
	lines := func(inv map[string]any) []any { return inv["lineItems"].([]any) }

	t.Run("no lineItems: the estimate's lines are copied", func(t *testing.T) {
		inv := create(`{"notes":"visit 1"}`)
		if len(lines(inv)) != 2 || inv["totalCents"] != float64(17500) || inv["notes"] != "visit 1" {
			t.Fatalf("invoice: %v", inv)
		}
		if first := lines(inv)[0].(map[string]any); first["description"] != "Window cleaning" || first["quantity"] != float64(2) || first["unitPriceCents"] != float64(7500) {
			t.Fatalf("first line: %v", first)
		}
	})

	t.Run("an empty body works too", func(t *testing.T) {
		if inv := create(`{}`); len(lines(inv)) != 2 {
			t.Fatalf("invoice: %v", inv)
		}
	})

	t.Run("lines in the request win", func(t *testing.T) {
		inv := create(`{"lineItems":[{"description":"Extra","quantity":1,"unitPriceCents":100}]}`)
		if len(lines(inv)) != 1 || inv["totalCents"] != float64(100) {
			t.Fatalf("invoice: %v", inv)
		}
	})

	t.Run("an explicit empty list is an empty draft", func(t *testing.T) {
		if inv := create(`{"lineItems":[]}`); len(lines(inv)) != 0 {
			t.Fatalf("invoice: %v", inv)
		}
	})

	t.Run("it follows later edits of the estimate", func(t *testing.T) {
		c.patch("/estimates/"+est, `{"lineItems":[{"description":"New price","quantity":1,"unitPriceCents":9900}]}`).expect(t, 200)
		inv := create(`{}`)
		if len(lines(inv)) != 1 || inv["totalCents"] != float64(9900) {
			t.Fatalf("invoice: %v", inv)
		}
	})

	t.Run("a job without an estimate starts empty", func(t *testing.T) {
		bare := dailyJob(t, c)
		inv := c.post("/jobs/"+bare+"/occurrences/"+today+"/invoice", `{}`).expect(t, 200).obj()
		if len(lines(inv)) != 0 {
			t.Fatalf("invoice: %v", inv)
		}
	})
}

func TestE2EInvoiceNumbersHaveNoGaps(t *testing.T) {
	c, _, _ := newClient(t)
	today := civil.Format(civil.Today())
	send := func(inv string) string {
		t.Helper()
		return c.patch("/invoices/"+inv+"/status", `{"status":"sent"}`).expect(t, 200).obj()["number"].(string)
	}

	t.Run("a draft has no number; sending gives it one that never changes", func(t *testing.T) {
		job := dailyJob(t, c)
		draft := c.post("/jobs/"+job+"/occurrences/"+today+"/invoice", draftBody).expect(t, 200).obj()
		if draft["number"] != nil {
			t.Fatalf("draft number: %v", draft["number"])
		}
		id := draft["id"].(string)
		if got := c.get("/invoices/"+id).expect(t, 200).obj()["number"]; got != nil {
			t.Fatalf("stored draft number: %v", got)
		}
		number := send(id)
		if number != "INV-000001" {
			t.Fatalf("first number %s", number)
		}
		for _, to := range []string{"paid", "refunded"} {
			if got := c.patch("/invoices/"+id+"/status", `{"status":"`+to+`"}`).expect(t, 200).obj()["number"]; got != number {
				t.Fatalf("number after %s: %v", to, got)
			}
		}
	})

	t.Run("a rejected duplicate, a deleted draft and a voided draft use no number", func(t *testing.T) {
		job, other := dailyJob(t, c), dailyJob(t, c)
		a := invoiceFor(t, c, job, today)
		c.post("/jobs/"+job+"/occurrences/"+today+"/invoice", draftBody).expect(t, 409) // a second live invoice

		gone := invoiceFor(t, c, other, today)
		c.do("DELETE", "/invoices/"+gone, "").expect(t, 200)
		voided := invoiceFor(t, c, other, today)
		c.patch("/invoices/"+voided+"/status", `{"status":"void"}`).expect(t, 200)
		if got := c.get("/invoices/"+voided).expect(t, 200).obj()["number"]; got != nil {
			t.Fatalf("voided draft number: %v", got)
		}

		if got := send(a); got != "INV-000002" {
			t.Fatalf("number %s, want INV-000002 right after INV-000001", got)
		}
		last := invoiceFor(t, c, other, today)
		if got := send(last); got != "INV-000003" {
			t.Fatalf("number %s, want INV-000003", got)
		}
		// Voiding a sent invoice keeps its number, and a re-issue gets the next one.
		c.patch("/invoices/"+last+"/status", `{"status":"void"}`).expect(t, 200)
		if got := c.get("/invoices/"+last).expect(t, 200).obj()["number"]; got != "INV-000003" {
			t.Fatalf("voided sent invoice number: %v", got)
		}
		if got := send(invoiceFor(t, c, other, today)); got != "INV-000004" {
			t.Fatalf("re-issued number %s, want INV-000004", got)
		}
	})

	t.Run("invoices sent at the same time get distinct consecutive numbers", func(t *testing.T) {
		const total = 8
		ids := make([]string, total)
		for i := range ids {
			ids[i] = invoiceFor(t, c, dailyJob(t, c), today)
		}
		numbers := make([]string, total)
		var wg sync.WaitGroup
		for i, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				numbers[i] = c.patch("/invoices/"+id+"/status", `{"status":"sent"}`).expect(t, 200).obj()["number"].(string)
			}()
		}
		wg.Wait()
		sort.Strings(numbers)
		for i, got := range numbers {
			if want := fmt.Sprintf("INV-%06d", 5+i); got != want {
				t.Fatalf("numbers %v: position %d is %s, want %s", numbers, i, got, want)
			}
		}
	})

	t.Run("estimates are still numbered when created", func(t *testing.T) {
		if got := c.post("/estimates", draftBody).expect(t, 200).obj()["number"]; got == nil {
			t.Fatal("an estimate must have its number from the start")
		}
	})
}
