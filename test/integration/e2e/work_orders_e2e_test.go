package e2e_test

import (
	"regexp"
	"strings"
	"sync"
	"testing"

	"recurringjob/internal/common/civil"
	"recurringjob/test/helpers"
)

var workOrderNumberRe = regexp.MustCompile(`^WO-\d{6}$`)

const workOrderBody = `{"notes":"bring ladder","tasks":[{"description":"Clean windows"},{"description":"Mop floor"}]}`

func workOrderFor(t *testing.T, c client, jobID, date string) string {
	t.Helper()
	return c.post("/jobs/"+jobID+"/occurrences/"+date+"/work-order", workOrderBody).expect(t, 200).obj()["id"].(string)
}

func TestE2EWorkOrderLifecycle(t *testing.T) {
	c, _, _ := newClient(t)
	job := dailyJob(t, c)
	today := civil.Format(civil.Today())

	created := c.post("/jobs/"+job+"/occurrences/"+today+"/work-order", workOrderBody).expect(t, 200)
	created.envelope(t)
	wo := created.obj()
	id := wo["id"].(string)
	if wo["number"] != "WO-000001" || wo["status"] != "draft" || wo["occurrenceDate"] != today || wo["jobId"] != job {
		t.Fatalf("created %v", wo)
	}
	if nameOf(wo["customer"]) != "Ada Lovelace" || wo["jobSnapshot"] == nil || len(wo["tasks"].([]any)) != 2 {
		t.Fatalf("created %v", wo)
	}

	// One live work order per occurrence; another occurrence is fine.
	c.post("/jobs/"+job+"/occurrences/"+today+"/work-order", workOrderBody).expect(t, 409)
	// Numbers can have gaps (the rejected duplicate above used one), but never repeat.
	second := c.post("/jobs/"+dailyJob(t, c)+"/occurrences/"+today+"/work-order", `{}`).expect(t, 200).obj()
	secondNumber, _ := second["number"].(string)
	if !workOrderNumberRe.MatchString(secondNumber) || secondNumber == wo["number"] {
		t.Fatalf("second number %v", second["number"])
	}

	// Cannot schedule an empty one, or complete with open tasks.
	empty := c.post("/jobs/"+dailyJob(t, c)+"/occurrences/"+today+"/work-order", `{}`).expect(t, 200).obj()["id"].(string)
	c.patch("/work-orders/"+empty+"/status", `{"status":"scheduled"}`).expect(t, 400)
	c.patch("/work-orders/"+id+"/status", `{"status":"completed"}`).expect(t, 409)
	c.patch("/work-orders/"+id+"/status", `{"status":"scheduled"}`).expect(t, 200)
	c.patch("/work-orders/"+id+"/status", `{"status":"in_progress"}`).expect(t, 200)
	c.patch("/work-orders/"+id+"/status", `{"status":"completed"}`).expect(t, 400)

	// Tick the tasks off while in progress, then complete.
	c.patch("/work-orders/"+id, `{"tasks":[{"description":"Clean windows","done":true},{"description":"Mop floor","done":true}]}`).expect(t, 200)
	done := c.patch("/work-orders/"+id+"/status", `{"status":"completed"}`).expect(t, 200).obj()
	if done["status"] != "completed" || done["completedAt"] == nil {
		t.Fatalf("completed %v", done)
	}

	// Final: no edits, no cancel, no delete.
	c.patch("/work-orders/"+id, `{"notes":"late"}`).expect(t, 409)
	c.patch("/work-orders/"+id+"/status", `{"status":"canceled"}`).expect(t, 409)
	c.do("DELETE", "/work-orders/"+id, "").expect(t, 409)

	// Reads and filters.
	c.get("/work-orders/"+id).expect(t, 200)
	c.get("/work-orders/"+unknownID).expect(t, 404)
	c.get("/work-orders/nope").expect(t, 400)
	if n := len(c.get("/work-orders?status=completed").expect(t, 200).list()); n != 1 {
		t.Fatalf("completed list has %d", n)
	}
	if n := len(c.get("/work-orders?q="+secondNumber).expect(t, 200).list()); n != 1 {
		t.Fatalf("number search has %d", n)
	}
	c.get("/work-orders?status=paid").expect(t, 400)

	// Only a draft can be deleted.
	c.do("DELETE", "/work-orders/"+empty, "").expect(t, 200)
	c.get("/work-orders/"+empty).expect(t, 404)
}

func TestE2EOccurrenceHasNoInProgressStatus(t *testing.T) {
	c, _, _ := newClient(t)
	job := dailyJob(t, c)
	today := civil.Format(civil.Today())
	c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"in_progress"}`).expect(t, 400)
	c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"confirmed"}`).expect(t, 200)
	c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"confirmed"}`).expect(t, 409)
	c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"completed"}`).expect(t, 200)
}

func TestE2EWorkOrderCanceledFreesTheOccurrence(t *testing.T) {
	c, _, _ := newClient(t)
	job := dailyJob(t, c)
	today := civil.Format(civil.Today())
	id := workOrderFor(t, c, job, today)
	c.patch("/work-orders/"+id+"/status", `{"status":"canceled"}`).expect(t, 200)
	workOrderFor(t, c, job, today)
}

func TestE2EWorkOrderNeedsAnAvailableOccurrence(t *testing.T) {
	c, _, _ := newClient(t)
	job := dailyJob(t, c)
	today := civil.Format(civil.Today())
	c.post("/jobs/"+job+"/occurrences/"+unknownID+"/work-order", `{}`).expect(t, 400)
	c.post("/jobs/"+unknownID+"/occurrences/"+today+"/work-order", `{}`).expect(t, 404)
	c.post("/jobs/"+job+"/occurrences/"+today+"/work-order", `{"tasks":[{"description":""}]}`).expect(t, 400)
	c.patch("/jobs/"+job+"/occurrences/"+today, `{"status":"canceled"}`).expect(t, 200)
	c.post("/jobs/"+job+"/occurrences/"+today+"/work-order", `{}`).expect(t, 409)
}

func TestE2EWorkOrdersAreTenantScoped(t *testing.T) {
	db := helpers.Connect(t)
	a, _ := helpers.NewSchema(t, db)
	b, _ := helpers.NewSchema(t, db)
	srv := newClientFor(t, db, a)
	id := workOrderFor(t, srv, dailyJob(t, srv), civil.Format(civil.Today()))

	inB := srv
	inB.tenant = b
	inB.get("/work-orders/"+id).expect(t, 404)
	if n := len(inB.get("/work-orders").expect(t, 200).list()); n != 0 {
		t.Fatalf("tenant b sees %d of a's work orders", n)
	}
	srv.get("/work-orders/"+id).expect(t, 200)
}

func TestE2EConcurrentWorkOrdersForOneOccurrenceCreateOne(t *testing.T) {
	c, _, _ := newClient(t)
	job := dailyJob(t, c)
	today := civil.Format(civil.Today())
	codes := make([]int, 6)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = c.post("/jobs/"+job+"/occurrences/"+today+"/work-order", workOrderBody).code
		}()
	}
	wg.Wait()
	var created, conflicts int
	for _, code := range codes {
		switch code {
		case 200:
			created++
		case 409:
			conflicts++
		}
	}
	if created != 1 || conflicts != len(codes)-1 {
		t.Fatalf("codes %v", codes)
	}
	if !strings.Contains(c.get("/work-orders?jobId="+job).expect(t, 200).raw, "WO-000001") {
		t.Fatal("the one work order is listed")
	}
}
