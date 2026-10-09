package e2e_test

import (
	"strings"
	"testing"

	"recurringjob/internal/common/civil"
)

func workOrderStatus(t *testing.T, c client, id string) string {
	t.Helper()
	return c.get("/work-orders/"+id).expect(t, 200).obj()["status"].(string)
}

func workOrderDate(t *testing.T, c client, id string) any {
	t.Helper()
	return c.get("/work-orders/"+id).expect(t, 200).obj()["occurrenceDate"]
}

// workOrderIn creates a work order for the occurrence and walks it to status.
func workOrderIn(t *testing.T, c client, jobID, date, status string) string {
	t.Helper()
	id := workOrderFor(t, c, jobID, date)
	steps := map[string][]string{
		"draft":       nil,
		"scheduled":   {"scheduled"},
		"in_progress": {"scheduled", "in_progress"},
		"completed":   {"scheduled", "in_progress", "completed"},
	}[status]
	for _, step := range steps {
		if step == "completed" {
			c.patch("/work-orders/"+id, `{"tasks":[{"description":"Clean windows","done":true},{"description":"Mop floor","done":true}]}`).expect(t, 200)
		}
		c.patch("/work-orders/"+id+"/status", `{"status":"`+step+`"}`).expect(t, 200)
	}
	return id
}

func TestE2EWorkOrderNeedsATaskToBeScheduledOrCompleted(t *testing.T) {
	c, _, _ := newClient(t)
	job := dailyJob(t, c)
	today := civil.Format(civil.Today())
	id := workOrderFor(t, c, job, today)

	// A draft can be emptied, but then it cannot be scheduled.
	c.patch("/work-orders/"+id, `{"tasks":[]}`).expect(t, 200)
	c.patch("/work-orders/"+id+"/status", `{"status":"scheduled"}`).expect(t, 400)
	c.patch("/work-orders/"+id, `{"tasks":[{"description":"Clean windows"}]}`).expect(t, 200)

	// Past draft the tasks cannot be emptied, so it can never reach completed with none.
	for _, to := range []string{"scheduled", "in_progress"} {
		c.patch("/work-orders/"+id+"/status", `{"status":"`+to+`"}`).expect(t, 200)
		c.patch("/work-orders/"+id, `{"tasks":[]}`).expect(t, 400).envelope(t)
		if n := len(c.get("/work-orders/"+id).expect(t, 200).obj()["tasks"].([]any)); n != 1 {
			t.Fatalf("a refused edit left %d tasks", n)
		}
	}
	c.patch("/work-orders/"+id+"/status", `{"status":"completed"}`).expect(t, 400) // the task is not done
}

func TestE2ECancelingAnOccurrenceCancelsItsWorkOrders(t *testing.T) {
	today := civil.Format(civil.Today())

	for _, status := range []string{"canceled", "terminate_service"} {
		t.Run(status+": draft, scheduled and in-progress work orders are canceled", func(t *testing.T) {
			c, _, _ := newClient(t)
			job := dailyJob(t, c)
			wo := workOrderIn(t, c, job, today, "in_progress")
			res := c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"`+status+`"}`).expect(t, 200)
			res.envelope(t)
			if _, has := res.obj()["keptWorkOrderIds"]; has {
				t.Fatalf("nothing was kept: %s", res.raw)
			}
			if got := workOrderStatus(t, c, wo); got != "canceled" {
				t.Fatalf("work order is %s, want canceled", got)
			}
			c.patch("/work-orders/"+wo+"/status", `{"status":"in_progress"}`).expect(t, 409)
			c.post("/jobs/"+job+"/occurrences/"+today+"/work-order", `{}`).expect(t, 409) // canceled occurrence
		})
	}

	for _, status := range []string{"draft", "scheduled"} {
		t.Run("a "+status+" work order is canceled too", func(t *testing.T) {
			c, _, _ := newClient(t)
			job := dailyJob(t, c)
			wo := workOrderIn(t, c, job, today, status)
			c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"canceled"}`).expect(t, 200)
			if got := workOrderStatus(t, c, wo); got != "canceled" {
				t.Fatalf("work order is %s, want canceled", got)
			}
		})
	}

	t.Run("a completed work order is kept and flagged", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := dailyJob(t, c)
		wo := workOrderIn(t, c, job, today, "completed")
		res := c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"canceled"}`).expect(t, 200)
		kept, _ := res.obj()["keptWorkOrderIds"].([]any)
		if len(kept) != 1 || kept[0] != wo {
			t.Fatalf("keptWorkOrderIds: %s", res.raw)
		}
		if got := workOrderStatus(t, c, wo); got != "completed" {
			t.Fatalf("work order is %s, want completed (the work was done)", got)
		}
	})

	t.Run("only that occurrence's work orders are affected", func(t *testing.T) {
		c, _, _ := newClient(t)
		job, other := dailyJob(t, c), dailyJob(t, c)
		mine, theirs := workOrderFor(t, c, job, today), workOrderFor(t, c, other, today)
		c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"canceled"}`).expect(t, 200)
		if workOrderStatus(t, c, mine) != "canceled" || workOrderStatus(t, c, theirs) != "draft" {
			t.Fatal("canceling one job's occurrence touched another job's work order")
		}
	})

	t.Run("confirming leaves the work order alone", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := dailyJob(t, c)
		wo := workOrderFor(t, c, job, today)
		c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"confirmed"}`).expect(t, 200)
		if got := workOrderStatus(t, c, wo); got != "draft" {
			t.Fatalf("work order is %s, want draft", got)
		}
	})
}

func TestE2EAVisitCannotBeCompletedWithOpenWorkOrders(t *testing.T) {
	today := civil.Format(civil.Today())
	complete := `{"status":"completed"}`

	for _, status := range []string{"draft", "scheduled", "in_progress"} {
		t.Run("a "+status+" work order blocks completing the visit", func(t *testing.T) {
			c, _, _ := newClient(t)
			job := dailyJob(t, c)
			wo := workOrderIn(t, c, job, today, status)
			refused := c.patch("/jobs/"+job+"/occurrences/"+today, complete).expect(t, 409)
			refused.envelope(t)
			if !strings.Contains(refused.raw, wo) {
				t.Fatalf("the refusal should name the work order: %s", refused.raw)
			}
			if got := scheduleStatus(t, c, job, today); got != "unconfirmed" {
				t.Fatalf("the visit is %s after a refused completion", got)
			}
		})
	}

	t.Run("it completes once the work order is canceled", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := dailyJob(t, c)
		wo := workOrderFor(t, c, job, today)
		c.patch("/jobs/"+job+"/occurrences/"+today, complete).expect(t, 409)
		c.patch("/work-orders/"+wo+"/status", `{"status":"canceled"}`).expect(t, 200)
		c.patch("/jobs/"+job+"/occurrences/"+today, complete).expect(t, 200)
	})

	t.Run("completing the work order does not complete the visit, and then the visit can be completed", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := dailyJob(t, c)
		wo := workOrderIn(t, c, job, today, "completed")
		if got := scheduleStatus(t, c, job, today); got != "unconfirmed" {
			t.Fatalf("completing the work order changed the visit to %s", got)
		}
		c.patch("/jobs/"+job+"/occurrences/"+today, complete).expect(t, 200)
		if got := workOrderStatus(t, c, wo); got != "completed" {
			t.Fatalf("work order is %s", got)
		}
	})

	t.Run("a finished visit takes no new work order, but still takes an invoice", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := dailyJob(t, c)
		c.patch("/jobs/"+job+"/occurrences/"+today, complete).expect(t, 200)
		c.post("/jobs/"+job+"/occurrences/"+today+"/work-order", `{}`).expect(t, 409).envelope(t)
		c.post("/jobs/"+job+"/occurrences/"+today+"/invoice", draftBody).expect(t, 200)
	})

	t.Run("a visit with only other visits' work orders completes", func(t *testing.T) {
		c, _, _ := newClient(t)
		job, other := dailyJob(t, c), dailyJob(t, c)
		workOrderFor(t, c, other, today)
		c.patch("/jobs/"+job+"/occurrences/"+today, complete).expect(t, 200)
	})
}

func TestE2EAWorkOrderCannotStartBeforeItsVisitDate(t *testing.T) {
	c, _, _ := newClient(t)
	soonDate := civil.Format(civil.AddDays(civil.Today(), 3))
	// The first occurrence of a future job is available, so it can take a work order.
	job := c.post("/jobs", `{"date":"`+soonDate+`"}`).expect(t, 200).obj()["id"].(string)
	wo := workOrderFor(t, c, job, soonDate)

	c.patch("/work-orders/"+wo+"/status", `{"status":"scheduled"}`).expect(t, 200) // planning ahead is fine
	started := c.patch("/work-orders/"+wo+"/status", `{"status":"in_progress"}`).expect(t, 409)
	started.envelope(t)
	if !strings.Contains(started.raw, soonDate) {
		t.Fatalf("the refusal should give the visit date: %s", started.raw)
	}
	if got := workOrderStatus(t, c, wo); got != "scheduled" {
		t.Fatalf("a refused start changed the status to %s", got)
	}
	c.patch("/work-orders/"+wo+"/status", `{"status":"canceled"}`).expect(t, 200) // canceling is fine too

	t.Run("the date rule follows the tenant's calendar", func(t *testing.T) {
		c, _, _ := newClient(t)
		// A zone far ahead of UTC makes "today" a day later than the UTC date.
		set := c.put("/settings", `{"timezone":"Pacific/Kiritimati"}`).expect(t, 200).obj()
		tenantToday := set["today"].(string)
		job := c.post("/jobs", `{"date":"`+tenantToday+`"}`).expect(t, 200).obj()["id"].(string)
		id := workOrderIn(t, c, job, tenantToday, "in_progress")
		c.patch("/work-orders/"+id, `{"tasks":[{"description":"a","done":true}]}`).expect(t, 200)
		c.patch("/work-orders/"+id+"/status", `{"status":"completed"}`).expect(t, 200)
	})
}

func TestE2ERescheduleMovesWorkOrdersThatHaveNotStarted(t *testing.T) {
	today := civil.Format(civil.Today())
	later := civil.Format(civil.AddDays(civil.Today(), 3))
	reschedule := `{"status":"rescheduled","rescheduledTo":"` + later + `"}`
	oneOff := func(t *testing.T, c client) string {
		t.Helper()
		return c.post("/jobs", `{"date":"`+today+`"}`).expect(t, 200).obj()["id"].(string)
	}

	for _, status := range []string{"draft", "scheduled"} {
		t.Run("a "+status+" work order follows the visit", func(t *testing.T) {
			c, _, _ := newClient(t)
			job := oneOff(t, c)
			wo := workOrderIn(t, c, job, today, status)
			res := c.patch("/jobs/"+job+"/occurrences/"+today, reschedule).expect(t, 200)
			res.envelope(t)
			if _, has := res.obj()["keptWorkOrderIds"]; has {
				t.Fatalf("nothing was left behind: %s", res.raw)
			}
			if workOrderDate(t, c, wo) != later || workOrderStatus(t, c, wo) != status {
				t.Fatalf("work order: %v", c.get("/work-orders/"+wo).obj())
			}
			// The old date refuses a new one (it was rescheduled); the new date already has this one.
			c.post("/jobs/"+job+"/occurrences/"+today+"/work-order", `{}`).expect(t, 409)
			c.post("/jobs/"+job+"/occurrences/"+later+"/work-order", `{}`).expect(t, 409)
		})
	}

	for _, status := range []string{"in_progress", "completed"} {
		t.Run("a "+status+" work order stays on the old date and is flagged", func(t *testing.T) {
			c, _, _ := newClient(t)
			job := oneOff(t, c)
			wo := workOrderIn(t, c, job, today, status)
			res := c.patch("/jobs/"+job+"/occurrences/"+today, reschedule).expect(t, 200)
			kept, _ := res.obj()["keptWorkOrderIds"].([]any)
			if len(kept) != 1 || kept[0] != wo {
				t.Fatalf("keptWorkOrderIds: %s", res.raw)
			}
			if workOrderDate(t, c, wo) != today || workOrderStatus(t, c, wo) != status {
				t.Fatalf("work order: %v", c.get("/work-orders/"+wo).obj())
			}
		})
	}

	t.Run("only that occurrence's work orders move", func(t *testing.T) {
		c, _, _ := newClient(t)
		job, other := oneOff(t, c), oneOff(t, c)
		mine, theirs := workOrderFor(t, c, job, today), workOrderFor(t, c, other, today)
		c.patch("/jobs/"+job+"/occurrences/"+today, reschedule).expect(t, 200)
		if workOrderDate(t, c, mine) != later || workOrderDate(t, c, theirs) != today {
			t.Fatal("rescheduling one job's visit moved another job's work order")
		}
	})

	t.Run("a refused reschedule moves nothing", func(t *testing.T) {
		c, _, _ := newClient(t)
		job := oneOff(t, c)
		wo := workOrderFor(t, c, job, today)
		c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"rescheduled","rescheduledTo":"`+today+`"}`).expect(t, 400)
		if workOrderDate(t, c, wo) != today {
			t.Fatalf("the work order moved although the reschedule was refused: %v", workOrderDate(t, c, wo))
		}
	})
}

func TestE2EReopenIsRefusedWhileTheJobHasWorkOrders(t *testing.T) {
	c, _, _ := newClient(t)
	est, job := approvedEstimate(t, c)
	wo := workOrderFor(t, c, job, civil.Format(civil.Today()))
	refused := c.post("/estimates/"+est+"/reopen", "").expect(t, 409)
	refused.envelope(t)
	if !strings.Contains(refused.raw, "work orders") {
		t.Fatalf("the refusal should say why: %s", refused.raw)
	}
	c.do("DELETE", "/work-orders/"+wo, "").expect(t, 200)
	c.post("/estimates/"+est+"/reopen", "").expect(t, 200)
}
