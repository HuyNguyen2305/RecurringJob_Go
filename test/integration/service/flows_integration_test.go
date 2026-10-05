package service_test

import (
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
	"recurringjob/internal/repository"
	"recurringjob/internal/service"
	"recurringjob/test/helpers"
)

func buildJobs(db *gorm.DB) *service.JobService {
	jobs := repository.NewJobRepository(db)
	refs := service.NewReferences(repository.NewCustomerRepository(db), repository.NewLocationRepository(db), repository.NewServiceTypeRepository(db))
	return service.NewJobService(jobs, service.NewOccurrenceResolver(jobs), refs)
}

func weeklyOn(day time.Time) *recurrence.Rule {
	return &recurrence.Rule{
		Frequency: recurrence.FreqWeekly, Interval: 1, WeeklyPeriod: recurrence.WeeklyEvery,
		WeeklyDaysOfWeek: []int{int(day.Weekday())}, EndsType: recurrence.EndsNever, ExceptType: recurrence.ExceptOff,
	}
}

func dailyRule() *recurrence.Rule {
	return &recurrence.Rule{Frequency: recurrence.FreqDaily, Interval: 1, EndsType: recurrence.EndsNever, ExceptType: recurrence.ExceptOff}
}

type view struct{ Date, State, Status string }

func views(items []service.ScheduleItem) []view {
	out := []view{}
	for _, it := range items {
		out = append(out, view{it.Date, it.State, it.Status})
	}
	return out
}

func TestIntegrationScheduleLifecycle(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	svc := build(db, nil)
	today := civil.Today()
	day := func(n int) time.Time { return civil.AddDays(today, n) }
	f := civil.Format

	job := helpers.SeedJob(t, ctx, db, &model.Job{Date: today, Status: "unconfirmed", Recurrence: weeklyOn(today)})
	schedule := func(limit int) []view {
		t.Helper()
		items, err := svc.Schedule(ctx, job.ID, service.ScheduleQuery{Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		return views(items)
	}
	update := func(date time.Time, req service.UpdateOccurrenceRequest) error {
		_, err := svc.UpdateOccurrence(ctx, job.ID, date, req)
		return err
	}
	to := func(d time.Time) *time.Time { return &d }

	// 1. Nothing stored yet: the first occurrence is real, the rest hollow.
	want := []view{{f(day(0)), "real", "unconfirmed"}, {f(day(7)), "hollow", "unconfirmed"}, {f(day(14)), "hollow", "unconfirmed"}}
	if got := schedule(3); !reflect.DeepEqual(got, want) {
		t.Fatalf("initial schedule\n got  %v\n want %v", got, want)
	}

	// 2. Completing the first (today) makes the second real; completedAt is a UTC instant "now".
	saved, err := svc.UpdateOccurrence(ctx, job.ID, day(0), service.UpdateOccurrenceRequest{Status: service.StatusCompleted})
	if err != nil {
		t.Fatal(err)
	}
	if saved.CompletedAt == nil || time.Since(*saved.CompletedAt).Abs() > 2*time.Minute {
		t.Fatalf("completedAt %v", saved.CompletedAt)
	}
	items, _ := svc.Schedule(ctx, job.ID, service.ScheduleQuery{Limit: 3})
	if items[0].CompletedAt == nil || items[0].CompletedAt.Location() != time.UTC || !items[0].CompletedAt.Equal(*saved.CompletedAt) { // the response already carries the stored (microsecond) value
		t.Fatalf("completedAt after the database round trip: %v (saved %v)", items[0].CompletedAt, saved.CompletedAt)
	}
	want = []view{{f(day(0)), "real", "completed"}, {f(day(7)), "real", "unconfirmed"}, {f(day(14)), "hollow", "unconfirmed"}}
	if got := views(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("after completing\n got  %v\n want %v", got, want)
	}

	// 3. Reschedule the second to a non-series date: original + visit, the third stays hollow.
	if err := update(day(7), service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: to(day(9))}); err != nil {
		t.Fatal(err)
	}
	want = []view{
		{f(day(0)), "real", "completed"}, {f(day(7)), "real", "rescheduled"},
		{f(day(9)), "real", "unconfirmed"}, {f(day(14)), "hollow", "unconfirmed"},
	}
	items, _ = svc.Schedule(ctx, job.ID, service.ScheduleQuery{Limit: 4})
	if got := views(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("after rescheduling\n got  %v\n want %v", got, want)
	}
	if items[1].RescheduledTo == nil || *items[1].RescheduledTo != f(day(9)) || items[2].RescheduledFrom == nil || *items[2].RescheduledFrom != f(day(7)) {
		t.Fatalf("reschedule links wrong: %+v / %+v", items[1], items[2])
	}

	// 4. The visit can be confirmed even though it is in the future; the later date stays hollow.
	if err := update(day(9), service.UpdateOccurrenceRequest{Status: service.StatusConfirmed}); err != nil {
		t.Fatal(err)
	}
	if got := schedule(4)[2]; got != (view{f(day(9)), "real", "confirmed"}) {
		t.Fatalf("visit: %v", got)
	}

	// 5. Availability checks for invoices / work orders.
	if err := svc.AvailableFor(ctx, job.ID, day(9)); err != nil {
		t.Errorf("the open visit must accept attachments: %v", err)
	}
	if err := svc.AvailableFor(ctx, job.ID, day(14)); !is409(err) {
		t.Errorf("a hollow occurrence must be refused: %v", err)
	}
	if err := svc.AvailableFor(ctx, job.ID, day(7)); !is409(err) {
		t.Errorf("a rescheduled original must be refused: %v", err)
	}
	if err := svc.ValidOccurrenceDate(ctx, job.ID, day(14)); err != nil {
		t.Errorf("a hollow occurrence is still a valid date: %v", err)
	}
	if err := svc.ValidOccurrenceDate(ctx, job.ID, day(1)); appStatus(err) != 404 {
		t.Errorf("a non-occurrence date must be a 404: %v", err)
	}

	// 6. Terminate the visit: the series ends there.
	if err := update(day(9), service.UpdateOccurrenceRequest{Status: service.StatusTerminateService}); err != nil {
		t.Fatal(err)
	}
	got := schedule(10)
	if len(got) != 3 || got[2] != (view{f(day(9)), "real", "terminate_service"}) {
		t.Fatalf("after terminate: %v", got)
	}
	if err := update(day(14), service.UpdateOccurrenceRequest{Status: service.StatusConfirmed}); !is409(err) {
		t.Fatalf("an occurrence after the end of the series must be refused: %v", err)
	}
}

func TestIntegrationOverdueGating(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	svc := build(db, nil)
	today := civil.Today()
	day := func(n int) time.Time { return civil.AddDays(today, n) }
	f := civil.Format

	job := helpers.SeedJob(t, ctx, db, &model.Job{Date: day(-3), Status: "unconfirmed", Recurrence: dailyRule()})
	schedule := func() []view {
		items, err := svc.Schedule(ctx, job.ID, service.ScheduleQuery{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		return views(items)
	}
	complete := func(d time.Time) {
		t.Helper()
		if _, err := svc.UpdateOccurrence(ctx, job.ID, d, service.UpdateOccurrenceRequest{Status: service.StatusCompleted}); err != nil {
			t.Fatalf("complete %s: %v", f(d), err)
		}
	}

	if got, want := schedule(), []view{{f(day(-3)), "overdue", "unconfirmed"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if _, err := svc.UpdateOccurrence(ctx, job.ID, day(-2), service.UpdateOccurrenceRequest{Status: service.StatusCompleted}); !is409(err) {
		t.Fatalf("a later occurrence behind an overdue one must be refused: %v", err)
	}
	complete(day(-3))
	if got, want := schedule(), []view{{f(day(-3)), "real", "completed"}, {f(day(-2)), "overdue", "unconfirmed"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	complete(day(-2))
	complete(day(-1))
	got := schedule()
	if len(got) != 10 || got[3] != (view{f(day(0)), "real", "unconfirmed"}) || got[4].State != "hollow" {
		t.Fatalf("today must be real (not overdue) and the next hollow: %v", got)
	}
}

func TestIntegrationExceptFrequency(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	svc := build(db, nil)
	jobs := buildJobs(db)
	today := civil.Today()
	day := func(n int) time.Time { return civil.AddDays(today, n) }
	f := civil.Format

	b := helpers.SeedJob(t, ctx, db, &model.Job{Date: day(1), Status: "unconfirmed"})
	rule := dailyRule()
	rule.ExceptType, rule.ExceptJobID = recurrence.ExceptFrequency, b.ID
	a := helpers.SeedJob(t, ctx, db, &model.Job{Date: today, Status: "unconfirmed", Recurrence: rule})

	t.Run("occurrences skip the dates of the referenced job", func(t *testing.T) {
		got, err := jobs.Occurrences(ctx, a.ID, time.Time{}, time.Time{}, 4)
		want := []string{f(day(0)), f(day(2)), f(day(3)), f(day(4))}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v err=%v\nwant %v", got, err, want)
		}
	})

	t.Run("the schedule gates over the remaining dates", func(t *testing.T) {
		items, err := svc.Schedule(ctx, a.ID, service.ScheduleQuery{Limit: 3})
		want := []view{{f(day(0)), "real", "unconfirmed"}, {f(day(2)), "hollow", "unconfirmed"}, {f(day(3)), "hollow", "unconfirmed"}}
		if err != nil || !reflect.DeepEqual(views(items), want) {
			t.Fatalf("got %v err=%v", views(items), err)
		}
	})

	t.Run("the excluded date is not an occurrence of A", func(t *testing.T) {
		if err := svc.ValidOccurrenceDate(ctx, a.ID, day(1)); appStatus(err) != 404 {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("a cycle between two stored jobs terminates", func(t *testing.T) {
		x := helpers.SeedJob(t, ctx, db, &model.Job{Date: today, Status: "unconfirmed", Recurrence: dailyRule()})
		y := helpers.SeedJob(t, ctx, db, &model.Job{Date: today, Status: "unconfirmed", Recurrence: dailyRule()})
		rx, ry := dailyRule(), dailyRule()
		rx.ExceptType, rx.ExceptJobID = recurrence.ExceptFrequency, y.ID
		ry.ExceptType, ry.ExceptJobID, ry.Interval = recurrence.ExceptFrequency, x.ID, 2
		helpers.SetRecurrence(t, ctx, db, x.ID, *rx)
		helpers.SetRecurrence(t, ctx, db, y.ID, *ry)

		done := make(chan struct{})
		go func() {
			defer close(done)
			for _, id := range []string{x.ID, y.ID} {
				if got, err := jobs.Occurrences(ctx, id, time.Time{}, time.Time{}, 5); err != nil || len(got) > 5 {
					t.Errorf("%s: %v %v", id, got, err)
				}
				if _, err := svc.Schedule(ctx, id, service.ScheduleQuery{Limit: 5}); err != nil {
					t.Errorf("schedule %s: %v", id, err)
				}
			}
		}()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("resolving a cycle did not terminate")
		}
	})
}

func TestIntegrationCreateJobStoresCustomerLocationServiceAndTime(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	jobs := buildJobs(db)
	repo := repository.NewJobRepository(db)
	r := helpers.RefsFor(t, ctx, db)
	other := helpers.SeedRefs(t, ctx, db) // a second customer with its own location
	date := civil.New(2026, 10, 2)
	in := service.CreateJobInput{
		CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID,
		Date: date, StartTime: "09:30", LengthMinutes: 90,
	}

	t.Run("the job is stored with its references, time and length, and loads with their names", func(t *testing.T) {
		job, err := jobs.CreateJob(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		got, err := repo.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.CustomerID != r.CustomerID || got.LocationID != r.LocationID || got.ServiceTypeID != r.ServiceTypeID || got.LengthMinutes != 90 {
			t.Fatalf("stored %+v", got)
		}
		if got.StartTime != "09:30:00" {
			t.Fatalf("start time %q, want 09:30:00", got.StartTime)
		}
		if got.Customer == nil || got.Customer.Name != "Ada Lovelace" || got.Location == nil || got.Location.AddressLine1 != "1 Main Street" || got.ServiceType == nil || got.ServiceType.Name != "Window cleaning" {
			t.Fatalf("references not loaded: %+v %+v %+v", got.Customer, got.Location, got.ServiceType)
		}
	})

	t.Run("references that do not fit are refused and leave nothing behind", func(t *testing.T) {
		before := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM jobs`)
		tests := []struct {
			name   string
			mutate func(c *service.CreateJobInput)
			want   int
		}{
			{"another customer's location", func(c *service.CreateJobInput) { c.LocationID = other.LocationID }, 400},
			{"unknown customer", func(c *service.CreateJobInput) { c.CustomerID = "00000000-0000-0000-0000-0000000000ff" }, 404},
			{"unknown location", func(c *service.CreateJobInput) { c.LocationID = "00000000-0000-0000-0000-0000000000ff" }, 404},
			{"unknown service type", func(c *service.CreateJobInput) { c.ServiceTypeID = "00000000-0000-0000-0000-0000000000ff" }, 404},
			{"malformed customer", func(c *service.CreateJobInput) { c.CustomerID = "nope" }, 400},
			{"bad start time", func(c *service.CreateJobInput) { c.StartTime = "25:00" }, 400},
			{"zero length", func(c *service.CreateJobInput) { c.LengthMinutes = 0 }, 400},
		}
		for _, tt := range tests {
			c := in
			tt.mutate(&c)
			if _, err := jobs.CreateJob(ctx, c); appStatus(err) != tt.want {
				t.Errorf("%s: %v", tt.name, err)
			}
		}
		if after := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM jobs`); after != before {
			t.Fatalf("rows %d -> %d", before, after)
		}
	})

	t.Run("the database itself refuses a customer or location that is in use being deleted", func(t *testing.T) {
		err := helpers.Tx(ctx, db, func(tx *gorm.DB) error {
			return tx.Exec(`DELETE FROM customers WHERE id = ?`, r.CustomerID).Error
		})
		if err == nil {
			t.Fatal("deleting a customer that has jobs must be refused")
		}
	})
}

func TestIntegrationCreateJobThroughTheDatabase(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	jobs := buildJobs(db)
	repo := repository.NewJobRepository(db)
	date := civil.New(2026, 10, 2)
	r := helpers.RefsFor(t, ctx, db)

	t.Run("defaults are normalised and persisted", func(t *testing.T) {
		job, err := jobs.CreateJob(ctx, service.CreateJobInput{CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, StartTime: "09:00", LengthMinutes: 60, Date: date, Recurrence: &recurrence.Rule{Frequency: "daily"}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := repo.GetJob(ctx, job.ID)
		if err != nil || got.Recurrence.Interval != 1 || got.Recurrence.EndsType != "never" || got.Recurrence.ExceptType != "off" || got.Status != "unconfirmed" {
			t.Fatalf("stored %+v err=%v", got, err)
		}
	})

	t.Run("a date with a clock time and zone is stored as its UTC calendar day", func(t *testing.T) {
		late := time.Date(2026, 10, 3, 2, 30, 0, 0, time.FixedZone("+7", 7*3600)) // 2026-10-02 19:30 UTC
		job, err := jobs.CreateJob(ctx, service.CreateJobInput{CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, StartTime: "09:00", LengthMinutes: 60, Date: late})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := repo.GetJob(ctx, job.ID)
		if civil.Format(got.Date) != "2026-10-02" {
			t.Fatalf("stored date %s", civil.Format(got.Date))
		}
	})

	t.Run("an except job that exists is accepted, one that does not is a 404, a malformed one a 400", func(t *testing.T) {
		other, _ := jobs.CreateJob(ctx, service.CreateJobInput{CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, StartTime: "09:00", LengthMinutes: 60, Date: date})
		ok := &recurrence.Rule{Frequency: "daily", ExceptType: "frequency", ExceptJobID: other.ID}
		if _, err := jobs.CreateJob(ctx, service.CreateJobInput{CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, StartTime: "09:00", LengthMinutes: 60, Date: date, Recurrence: ok}); err != nil {
			t.Fatal(err)
		}
		missing := &recurrence.Rule{Frequency: "daily", ExceptType: "frequency", ExceptJobID: "00000000-0000-0000-0000-0000000000ff"}
		if _, err := jobs.CreateJob(ctx, service.CreateJobInput{CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, StartTime: "09:00", LengthMinutes: 60, Date: date, Recurrence: missing}); appStatus(err) != 404 {
			t.Errorf("missing: %v", err)
		}
		bad := &recurrence.Rule{Frequency: "daily", ExceptType: "frequency", ExceptJobID: "nope"}
		if _, err := jobs.CreateJob(ctx, service.CreateJobInput{CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, StartTime: "09:00", LengthMinutes: 60, Date: date, Recurrence: bad}); appStatus(err) != 400 {
			t.Errorf("malformed: %v", err)
		}
	})

	t.Run("a rejected job leaves nothing behind", func(t *testing.T) {
		before := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM jobs`)
		_, err := jobs.CreateJob(ctx, service.CreateJobInput{CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, StartTime: "09:00", LengthMinutes: 60, Date: date, Status: "rescheduled"})
		_, err2 := jobs.CreateJob(ctx, service.CreateJobInput{CustomerID: r.CustomerID, LocationID: r.LocationID, ServiceTypeID: r.ServiceTypeID, StartTime: "09:00", LengthMinutes: 60, Date: date, Recurrence: &recurrence.Rule{Frequency: "weekly"}})
		if appStatus(err) != 400 || appStatus(err2) != 400 {
			t.Fatalf("%v / %v", err, err2)
		}
		if after := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM jobs`); after != before {
			t.Fatalf("rows %d -> %d", before, after)
		}
	})

	t.Run("unknown and malformed job ids through the services", func(t *testing.T) {
		svc := build(db, nil)
		if _, err := svc.Schedule(ctx, "00000000-0000-0000-0000-0000000000ff", service.ScheduleQuery{}); appStatus(err) != 404 {
			t.Errorf("unknown: %v", err)
		}
		if _, err := svc.Schedule(ctx, "nope", service.ScheduleQuery{}); appStatus(err) != 400 {
			t.Errorf("malformed: %v", err)
		}
		if _, err := jobs.Occurrences(ctx, "nope", time.Time{}, time.Time{}, 5); appStatus(err) != 400 {
			t.Errorf("malformed occurrences: %v", err)
		}
	})
}

// is409 reports whether err is a 409 application error.
func is409(err error) bool { return appStatus(err) == 409 }
