package repository_test

import (
	"context"
	"reflect"
	"regexp"
	"testing"
	"time"

	"gorm.io/gorm"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/repository"
	"recurringjob/test/fixtures"
	"recurringjob/test/helpers"
)

var workOrderNumberRe = regexp.MustCompile(`^WO-\d{6}$`)

func taskTexts(tasks []model.WorkOrderTask) []string {
	out := []string{}
	for _, t := range tasks {
		out = append(out, t.Description)
	}
	return out
}

func TestWorkOrderRepository(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	orders := repository.NewWorkOrderRepository(db)
	day := func(n int) time.Time { return civil.New(2026, 10, n) }
	newJob := func() *model.Job { return helpers.SeedJob(t, ctx, db, fixtures.DailyJob(day(1))) }
	newOrder := func(jobID string, date time.Time) *model.WorkOrder {
		return fixtures.WorkOrder(jobID, date, r.CustomerID, r.LocationID, r.ServiceTypeID)
	}

	t.Run("Create saves the work order and its tasks; Get returns them in position order", func(t *testing.T) {
		job := newJob()
		wo := newOrder(job.ID, day(5))
		wo.Notes = "bring ladder"
		wo.Tasks = append(wo.Tasks, model.WorkOrderTask{Position: 2, Description: "Wipe sills"})
		if err := orders.Create(ctx, wo); err != nil {
			t.Fatal(err)
		}
		if !uuidRe.MatchString(wo.ID) || wo.CreatedAt.IsZero() || !workOrderNumberRe.MatchString(wo.Number) {
			t.Fatalf("not populated: %+v", wo)
		}
		got, err := orders.Get(ctx, wo.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.JobID != job.ID || !got.OccurrenceDate.Equal(day(5)) || got.Status != "draft" || got.Notes != "bring ladder" || got.Number != wo.Number || got.CompletedAt != nil {
			t.Fatalf("got %+v", got)
		}
		if got.Customer == nil || got.Customer.Name != "Ada Lovelace" || got.Location == nil || got.ServiceType == nil || got.JobSnapshot == nil || got.JobSnapshot.ID != job.ID {
			t.Fatalf("references or snapshot missing: %+v", got)
		}
		if want := []string{"Clean windows", "Mop floor", "Wipe sills"}; !reflect.DeepEqual(taskTexts(got.Tasks), want) {
			t.Fatalf("tasks %v, want %v", taskTexts(got.Tasks), want)
		}
		for _, task := range got.Tasks {
			if task.WorkOrderID != wo.ID || !uuidRe.MatchString(task.ID) || task.Done {
				t.Fatalf("task %+v", task)
			}
		}
	})

	t.Run("the database gives every work order its own number", func(t *testing.T) {
		a := helpers.SeedWorkOrder(t, ctx, db, newOrder(newJob().ID, day(5)))
		b := helpers.SeedWorkOrder(t, ctx, db, newOrder(newJob().ID, day(5)))
		if !workOrderNumberRe.MatchString(a.Number) || !workOrderNumberRe.MatchString(b.Number) || a.Number == b.Number || a.Number >= b.Number {
			t.Fatalf("numbers %q then %q", a.Number, b.Number)
		}
	})

	t.Run("Get: an unknown id is a 404", func(t *testing.T) {
		if _, err := orders.Get(ctx, "00000000-0000-0000-0000-0000000000ff"); appStatus(t, err) != 404 {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("a second live work order for the occurrence is a 409; a canceled one frees it", func(t *testing.T) {
		job := newJob()
		first := newOrder(job.ID, day(6))
		if err := orders.Create(ctx, first); err != nil {
			t.Fatal(err)
		}
		if err := orders.Create(ctx, newOrder(job.ID, day(6))); appStatus(t, err) != 409 {
			t.Fatalf("duplicate: %v", err)
		}
		if err := orders.Create(ctx, newOrder(job.ID, day(7))); err != nil {
			t.Fatalf("another date: %v", err)
		}
		if n, err := orders.UpdateStatusGuarded(ctx, first.ID, []string{"draft"}, map[string]any{"status": "canceled"}); err != nil || n != 1 {
			t.Fatalf("cancel n=%d err=%v", n, err)
		}
		if err := orders.Create(ctx, newOrder(job.ID, day(6))); err != nil {
			t.Fatalf("after cancel: %v", err)
		}
	})

	t.Run("a job that does not exist is a 409, and nothing is saved", func(t *testing.T) {
		wo := newOrder("00000000-0000-0000-0000-0000000000ee", day(5))
		if err := orders.Create(ctx, wo); appStatus(t, err) != 409 {
			t.Fatalf("got %v", err)
		}
		if n := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM work_order_tasks WHERE description = 'Clean windows' AND work_order_id NOT IN (SELECT id FROM work_orders)`); n != 0 {
			t.Fatalf("%d orphan tasks", n)
		}
	})

	t.Run("UpdateContent changes fields and replaces tasks, only from the allowed statuses", func(t *testing.T) {
		wo := helpers.SeedWorkOrder(t, ctx, db, newOrder(newJob().ID, day(5)))
		allowed := []string{"draft", "scheduled"}
		before, _ := orders.Get(ctx, wo.ID)

		if n, err := orders.UpdateContent(ctx, wo.ID, allowed, map[string]any{"notes": "new"}, nil); err != nil || n != 1 {
			t.Fatalf("notes only n=%d err=%v", n, err)
		}
		got, _ := orders.Get(ctx, wo.ID)
		if got.Notes != "new" || len(got.Tasks) != 2 || !got.UpdatedAt.After(before.UpdatedAt) {
			t.Fatalf("notes only: %+v", got)
		}

		tasks := []model.WorkOrderTask{{Position: 0, Description: "Only this", Done: true}}
		if n, err := orders.UpdateContent(ctx, wo.ID, allowed, map[string]any{}, tasks); err != nil || n != 1 {
			t.Fatalf("tasks n=%d err=%v", n, err)
		}
		got, _ = orders.Get(ctx, wo.ID)
		if got.Notes != "new" || !reflect.DeepEqual(taskTexts(got.Tasks), []string{"Only this"}) || !got.Tasks[0].Done {
			t.Fatalf("tasks replaced: %+v", got)
		}

		if n, err := orders.UpdateContent(ctx, wo.ID, allowed, map[string]any{}, []model.WorkOrderTask{}); err != nil || n != 1 {
			t.Fatalf("empty n=%d err=%v", n, err)
		}
		if got, _ = orders.Get(ctx, wo.ID); len(got.Tasks) != 0 {
			t.Fatalf("an empty list must remove every task: %+v", got.Tasks)
		}

		n, err := orders.UpdateContent(ctx, wo.ID, []string{"in_progress"}, map[string]any{"notes": "late"}, []model.WorkOrderTask{{Description: "x"}})
		if err != nil || n != 0 {
			t.Fatalf("wrong status n=%d err=%v", n, err)
		}
		if got, _ = orders.Get(ctx, wo.ID); got.Notes != "new" || len(got.Tasks) != 0 {
			t.Fatalf("a refused update changed the work order: %+v", got)
		}
	})

	t.Run("UpdateStatusGuarded: only from the allowed statuses; completed needs its timestamp", func(t *testing.T) {
		wo := helpers.SeedWorkOrder(t, ctx, db, newOrder(newJob().ID, day(5)))
		if n, err := orders.UpdateStatusGuarded(ctx, wo.ID, []string{"scheduled"}, map[string]any{"status": "in_progress"}); err != nil || n != 0 {
			t.Fatalf("wrong status n=%d err=%v", n, err)
		}
		if n, err := orders.UpdateStatusGuarded(ctx, wo.ID, []string{"draft"}, map[string]any{"status": "scheduled"}); err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if _, err := orders.UpdateStatusGuarded(ctx, wo.ID, []string{"scheduled"}, map[string]any{"status": "completed"}); err == nil {
			t.Fatal("completed without completed_at must violate the constraint")
		}
		done := time.Now().UTC().Truncate(time.Microsecond)
		if n, err := orders.UpdateStatusGuarded(ctx, wo.ID, []string{"scheduled"}, map[string]any{"status": "completed", "completed_at": done}); err != nil || n != 1 {
			t.Fatalf("completed n=%d err=%v", n, err)
		}
		got, _ := orders.Get(ctx, wo.ID)
		if got.Status != "completed" || got.CompletedAt == nil || !got.CompletedAt.Equal(done) {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("DeleteGuarded removes the work order and its tasks, only from the allowed statuses", func(t *testing.T) {
		wo := helpers.SeedWorkOrder(t, ctx, db, newOrder(newJob().ID, day(5)))
		if n, err := orders.DeleteGuarded(ctx, wo.ID, []string{"scheduled"}); err != nil || n != 0 {
			t.Fatalf("wrong status n=%d err=%v", n, err)
		}
		if n, err := orders.DeleteGuarded(ctx, wo.ID, []string{"draft"}); err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if _, err := orders.Get(ctx, wo.ID); appStatus(t, err) != 404 {
			t.Fatalf("still there: %v", err)
		}
		if n := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM work_order_tasks WHERE work_order_id = ?`, wo.ID); n != 0 {
			t.Fatalf("%d tasks survived", n)
		}
	})

	t.Run("GetForUpdate holds the row: a status change from another transaction waits for it", func(t *testing.T) {
		wo := helpers.SeedWorkOrder(t, ctx, db, newOrder(newJob().ID, day(5)))
		release := make(chan struct{})
		locked := make(chan struct{})
		holder := make(chan error, 1)
		go func() {
			holder <- orders.Transaction(ctx, func(ctx context.Context) error {
				if _, err := orders.GetForUpdate(ctx, wo.ID); err != nil {
					return err
				}
				close(locked)
				<-release
				return nil
			})
		}()
		<-locked
		changed := make(chan int64, 1)
		go func() {
			n, _ := orders.UpdateStatusGuarded(ctx, wo.ID, []string{"draft"}, map[string]any{"status": "scheduled"})
			changed <- n
		}()
		select {
		case <-changed:
			t.Fatal("the status change did not wait for the lock")
		case <-time.After(300 * time.Millisecond):
		}
		close(release)
		if err := <-holder; err != nil {
			t.Fatal(err)
		}
		if n := <-changed; n != 1 {
			t.Fatalf("n=%d", n)
		}
	})
}

func TestWorkOrderRepositoryList(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	a := helpers.SeedRefs(t, ctx, db) // "Ada Lovelace"
	b := helpers.SeedRefs(t, ctx, db)
	helpers.MustTx(t, ctx, db, func(tx *gorm.DB) error {
		return tx.Exec(`UPDATE customers SET name = 'Bob Builder' WHERE id = ?`, b.CustomerID).Error
	})
	orders := repository.NewWorkOrderRepository(db)
	day := func(n int) time.Time { return civil.New(2026, 10, n) }
	jobA := helpers.SeedJob(t, ctx, db, &model.Job{Date: day(1), Status: "unconfirmed", CustomerID: a.CustomerID, LocationID: a.LocationID, ServiceTypeID: a.ServiceTypeID})
	jobB := helpers.SeedJob(t, ctx, db, &model.Job{Date: day(1), Status: "unconfirmed", CustomerID: b.CustomerID, LocationID: b.LocationID, ServiceTypeID: b.ServiceTypeID})

	seed := func(job *model.Job, r helpers.Refs, date time.Time, status string) *model.WorkOrder {
		wo := fixtures.WorkOrder(job.ID, date, r.CustomerID, r.LocationID, r.ServiceTypeID)
		wo.Status = status
		if status == "completed" {
			now := time.Now().UTC()
			wo.CompletedAt = &now
		}
		return helpers.SeedWorkOrder(t, ctx, db, wo)
	}
	w1 := seed(jobA, a, day(2), "draft")
	w2 := seed(jobA, a, day(3), "scheduled")
	w3 := seed(jobB, b, day(4), "completed")
	w4 := seed(jobB, b, day(5), "canceled")

	ids := func(f model.WorkOrderFilter, limit, offset int) []string {
		t.Helper()
		got, err := orders.List(ctx, f, limit, offset)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, wo := range got {
			out = append(out, wo.ID)
		}
		return out
	}

	t.Run("newest first, with the references and tasks loaded", func(t *testing.T) {
		got, err := orders.List(ctx, model.WorkOrderFilter{}, 10, 0)
		if err != nil || len(got) != 4 {
			t.Fatalf("n=%d err=%v", len(got), err)
		}
		if got[0].ID != w4.ID || got[3].ID != w1.ID {
			t.Fatalf("order %s ... %s", got[0].ID, got[3].ID)
		}
		if got[0].Customer == nil || got[0].Customer.Name != "Bob Builder" || got[0].Location == nil || got[0].ServiceType == nil || len(got[0].Tasks) != 2 || got[0].Tasks[0].Position != 0 {
			t.Fatalf("not loaded: %+v", got[0])
		}
	})

	t.Run("filters", func(t *testing.T) {
		tests := []struct {
			name string
			f    model.WorkOrderFilter
			want []string
		}{
			{"status", model.WorkOrderFilter{Status: "scheduled"}, []string{w2.ID}},
			{"customer", model.WorkOrderFilter{CustomerID: b.CustomerID}, []string{w4.ID, w3.ID}},
			{"location", model.WorkOrderFilter{LocationID: a.LocationID}, []string{w2.ID, w1.ID}},
			{"job", model.WorkOrderFilter{JobID: jobB.ID}, []string{w4.ID, w3.ID}},
			{"number", model.WorkOrderFilter{Q: w3.Number}, []string{w3.ID}},
			{"customer name, any case", model.WorkOrderFilter{Q: "bob"}, []string{w4.ID, w3.ID}},
			{"from", model.WorkOrderFilter{OccurrenceFrom: day(4)}, []string{w4.ID, w3.ID}},
			{"to", model.WorkOrderFilter{OccurrenceTo: day(3)}, []string{w2.ID, w1.ID}},
			{"range, inclusive", model.WorkOrderFilter{OccurrenceFrom: day(3), OccurrenceTo: day(4)}, []string{w3.ID, w2.ID}},
			{"combined", model.WorkOrderFilter{CustomerID: b.CustomerID, Status: "canceled"}, []string{w4.ID}},
			{"nothing matches", model.WorkOrderFilter{Q: "zzz"}, []string{}},
			{"a % in the search is literal", model.WorkOrderFilter{Q: "%"}, []string{}},
		}
		for _, tt := range tests {
			if got := ids(tt.f, 10, 0); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
			}
		}
	})

	t.Run("limit and offset page through the list", func(t *testing.T) {
		if got := ids(model.WorkOrderFilter{}, 2, 0); !reflect.DeepEqual(got, []string{w4.ID, w3.ID}) {
			t.Errorf("first page %v", got)
		}
		if got := ids(model.WorkOrderFilter{}, 2, 2); !reflect.DeepEqual(got, []string{w2.ID, w1.ID}) {
			t.Errorf("second page %v", got)
		}
		if got := ids(model.WorkOrderFilter{}, 2, 4); !reflect.DeepEqual(got, []string{}) {
			t.Errorf("past the end %v", got)
		}
	})
}

func TestWorkOrderRepositoryOccurrenceQueries(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	r := helpers.RefsFor(t, ctx, db)
	orders := repository.NewWorkOrderRepository(db)
	day := func(n int) time.Time { return civil.New(2026, 10, n) }
	job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(day(1)))
	other := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(day(1)))

	seed := func(j *model.Job, date time.Time, status string) *model.WorkOrder {
		wo := fixtures.WorkOrder(j.ID, date, r.CustomerID, r.LocationID, r.ServiceTypeID)
		wo.Status = status
		if status == "completed" {
			now := time.Now().UTC()
			wo.CompletedAt = &now
		}
		return helpers.SeedWorkOrder(t, ctx, db, wo)
	}
	// Two live work orders cannot share an occurrence, so a canceled one and
	// then one per status is spread over separate occurrences.
	draft := seed(job, day(5), "draft")
	scheduled := seed(job, day(6), "scheduled")
	progress := seed(job, day(7), "in_progress")
	done := seed(job, day(8), "completed")
	elsewhere := seed(other, day(5), "draft")
	statusOf := func(id string) string {
		t.Helper()
		wo, err := orders.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return wo.Status
	}

	t.Run("UpdateStatusForOccurrence changes only that job, date and the allowed statuses", func(t *testing.T) {
		open := []string{"draft", "scheduled", "in_progress"}
		if n, err := orders.UpdateStatusForOccurrence(ctx, job.ID, day(8), open, "canceled"); err != nil || n != 0 {
			t.Fatalf("a completed one is not open: n=%d err=%v", n, err)
		}
		if n, err := orders.UpdateStatusForOccurrence(ctx, job.ID, day(5), open, "canceled"); err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if statusOf(draft.ID) != "canceled" || statusOf(elsewhere.ID) != "draft" || statusOf(scheduled.ID) != "scheduled" || statusOf(done.ID) != "completed" {
			t.Fatal("the wrong work orders changed")
		}
	})

	t.Run("MoveForOccurrence moves only the given statuses from that date", func(t *testing.T) {
		if n, err := orders.MoveForOccurrence(ctx, job.ID, day(7), day(12), []string{"draft", "scheduled"}); err != nil || n != 0 {
			t.Fatalf("in_progress is not movable: n=%d err=%v", n, err)
		}
		if n, err := orders.MoveForOccurrence(ctx, job.ID, day(6), day(12), []string{"draft", "scheduled"}); err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		moved, _ := orders.Get(ctx, scheduled.ID)
		stayed, _ := orders.Get(ctx, progress.ID)
		if !moved.OccurrenceDate.Equal(day(12)) || !stayed.OccurrenceDate.Equal(day(7)) {
			t.Fatalf("moved to %v, in progress on %v", moved.OccurrenceDate, stayed.OccurrenceDate)
		}
	})

	t.Run("IDsForOccurrence lists the given statuses of that occurrence, never nil", func(t *testing.T) {
		got, err := orders.IDsForOccurrence(ctx, job.ID, day(7), []string{"in_progress", "completed"})
		if err != nil || !reflect.DeepEqual(got, []string{progress.ID}) {
			t.Fatalf("got %v err=%v", got, err)
		}
		got, err = orders.IDsForOccurrence(ctx, job.ID, day(9), []string{"in_progress", "completed"})
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("nothing there: %#v err=%v", got, err)
		}
	})

	t.Run("CountForJob counts every status of that job", func(t *testing.T) {
		if n, err := orders.CountForJob(ctx, job.ID); err != nil || n != 4 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if n, err := orders.CountForJob(ctx, other.ID); err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if n, err := orders.CountForJob(ctx, "00000000-0000-0000-0000-0000000000ff"); err != nil || n != 0 {
			t.Fatalf("unknown job: n=%d err=%v", n, err)
		}
	})
}
