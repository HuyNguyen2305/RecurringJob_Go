package service_test

import (
	"math/rand"
	"reflect"
	"testing"
	"time"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

func dt(s string) time.Time {
	t, err := civil.Parse(s)
	if err != nil {
		panic(err)
	}
	return t
}

func dp(s string) *time.Time { t := dt(s); return &t }

func row(date, status string) model.JobOccurrence {
	return model.JobOccurrence{OccurrenceDate: dt(date), Status: status}
}

func resched(from, to string) []model.JobOccurrence {
	a := row(from, service.StatusRescheduled)
	a.RescheduledTo = dp(to)
	b := row(to, service.StatusUnconfirmed)
	b.RescheduledFrom = dp(from)
	return []model.JobOccurrence{a, b}
}

type brief struct{ Date, State, Status string }

func summarize(items []service.ScheduleItem) []brief {
	out := []brief{}
	for _, it := range items {
		out = append(out, brief{it.Date, it.State, it.Status})
	}
	return out
}

func TestWalkSchedule(t *testing.T) {
	slots := []string{"2026-10-01", "2026-10-02", "2026-10-03"}
	tests := []struct {
		name      string
		slots     []string
		rows      []model.JobOccurrence
		jobStatus string
		today     string
		want      []brief
		ended     bool
	}{
		{
			name: "first open is real, later ones hollow", slots: slots, jobStatus: service.StatusUnconfirmed, today: "2026-10-01",
			want: []brief{{"2026-10-01", service.StateReal, "unconfirmed"}, {"2026-10-02", service.StateHollow, "unconfirmed"}, {"2026-10-03", service.StateHollow, "unconfirmed"}},
		},
		{
			name: "open past available occurrence is overdue and ends the walk", slots: slots, jobStatus: service.StatusUnconfirmed, today: "2026-10-05",
			want: []brief{{"2026-10-01", service.StateOverdue, "unconfirmed"}}, ended: true,
		},
		{
			name: "completed makes the next real", slots: slots, jobStatus: service.StatusUnconfirmed, today: "2026-10-02",
			rows: []model.JobOccurrence{row("2026-10-01", service.StatusCompleted)},
			want: []brief{{"2026-10-01", service.StateReal, "completed"}, {"2026-10-02", service.StateReal, "unconfirmed"}, {"2026-10-03", service.StateHollow, "unconfirmed"}},
		},
		{
			name: "canceled makes the next real", slots: slots, jobStatus: service.StatusUnconfirmed, today: "2026-10-01",
			rows: []model.JobOccurrence{row("2026-10-01", service.StatusCanceled)},
			want: []brief{{"2026-10-01", service.StateReal, "canceled"}, {"2026-10-02", service.StateReal, "unconfirmed"}, {"2026-10-03", service.StateHollow, "unconfirmed"}},
		},
		{
			name: "terminate_service is emitted and ends", slots: slots, jobStatus: service.StatusUnconfirmed, today: "2026-10-03",
			rows: []model.JobOccurrence{row("2026-10-01", service.StatusCompleted), row("2026-10-02", service.StatusTerminateService)},
			want: []brief{{"2026-10-01", service.StateReal, "completed"}, {"2026-10-02", service.StateReal, "terminate_service"}}, ended: true,
		},
		{
			name: "reschedule follows the visit within the same slot", slots: slots, jobStatus: service.StatusUnconfirmed, today: "2026-10-01",
			rows: resched("2026-10-01", "2026-10-05"),
			want: []brief{{"2026-10-01", service.StateReal, "rescheduled"}, {"2026-10-05", service.StateReal, "unconfirmed"}, {"2026-10-02", service.StateHollow, "unconfirmed"}, {"2026-10-03", service.StateHollow, "unconfirmed"}},
		},
		{
			name: "reschedule chains when moved again", slots: slots, jobStatus: service.StatusUnconfirmed, today: "2026-10-01",
			rows: append(resched("2026-10-01", "2026-10-05"), func() model.JobOccurrence {
				r := row("2026-10-05", service.StatusRescheduled)
				r.RescheduledFrom = dp("2026-10-01")
				r.RescheduledTo = dp("2026-10-06")
				return r
			}(), func() model.JobOccurrence {
				r := row("2026-10-06", service.StatusUnconfirmed)
				r.RescheduledFrom = dp("2026-10-05")
				return r
			}()),
			want: []brief{{"2026-10-01", service.StateReal, "rescheduled"}, {"2026-10-05", service.StateReal, "rescheduled"}, {"2026-10-06", service.StateReal, "unconfirmed"}, {"2026-10-02", service.StateHollow, "unconfirmed"}, {"2026-10-03", service.StateHollow, "unconfirmed"}},
		},
		{
			name: "a completed job status applies to the first occurrence only, and the next one opens", slots: slots, jobStatus: service.StatusCompleted, today: "2026-10-01",
			want: []brief{{"2026-10-01", service.StateReal, "completed"}, {"2026-10-02", service.StateReal, "unconfirmed"}, {"2026-10-03", service.StateHollow, "unconfirmed"}},
		},
		{
			name: "confirmed job status carries to later occurrences", slots: slots, jobStatus: service.StatusConfirmed, today: "2026-10-01",
			want: []brief{{"2026-10-01", service.StateReal, "confirmed"}, {"2026-10-02", service.StateHollow, "confirmed"}, {"2026-10-03", service.StateHollow, "confirmed"}},
		},
		{
			name: "no slots", slots: nil, jobStatus: service.StatusUnconfirmed, today: "2026-10-01", want: []brief{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, ended := service.WalkSchedule(tt.slots, rowsByDate(tt.rows), tt.jobStatus, "2026-10-01", dt(tt.today))
			if got := summarize(items); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %v\nwant %v", got, tt.want)
			}
			if ended != tt.ended {
				t.Fatalf("ended = %v, want %v", ended, tt.ended)
			}
		})
	}
}

func TestWalkScheduleMore(t *testing.T) {
	walk := func(slots []string, rows []model.JobOccurrence, jobStatus, today string) ([]service.ScheduleItem, bool) {
		return service.WalkSchedule(slots, rowsByDate(rows), jobStatus, "2026-10-01", dt(today))
	}

	t.Run("a rescheduled-to visit in the past is overdue", func(t *testing.T) {
		items, ended := walk([]string{"2026-10-01", "2026-10-03"}, resched("2026-10-01", "2026-10-02"), service.StatusUnconfirmed, "2026-10-05")
		want := []brief{{"2026-10-01", service.StateReal, "rescheduled"}, {"2026-10-02", service.StateOverdue, "unconfirmed"}}
		if got := summarize(items); !reflect.DeepEqual(got, want) || !ended {
			t.Fatalf("got %v ended=%v", got, ended)
		}
	})

	t.Run("the occurrence dated today is not overdue", func(t *testing.T) {
		items, ended := walk([]string{"2026-10-01"}, nil, service.StatusUnconfirmed, "2026-10-01")
		if got := summarize(items); !reflect.DeepEqual(got, []brief{{"2026-10-01", service.StateReal, "unconfirmed"}}) || ended {
			t.Fatalf("got %v ended=%v", got, ended)
		}
	})

	t.Run("a rescheduled row without a target does not break the walk", func(t *testing.T) {
		items, ended := walk([]string{"2026-10-01", "2026-10-02"}, []model.JobOccurrence{row("2026-10-01", service.StatusRescheduled)}, service.StatusUnconfirmed, "2026-10-01")
		want := []brief{{"2026-10-01", service.StateReal, "rescheduled"}, {"2026-10-02", service.StateReal, "unconfirmed"}}
		if got := summarize(items); !reflect.DeepEqual(got, want) || ended {
			t.Fatalf("got %v ended=%v", got, ended)
		}
	})

	t.Run("a corrupt reschedule cycle terminates", func(t *testing.T) {
		a, b := row("2026-10-01", service.StatusRescheduled), row("2026-10-02", service.StatusRescheduled)
		a.RescheduledTo, b.RescheduledTo = dp("2026-10-02"), dp("2026-10-01")
		items, _ := walk([]string{"2026-10-01"}, []model.JobOccurrence{a, b}, service.StatusUnconfirmed, "2026-10-01")
		if len(items) == 0 || len(items) > service.MaxChainHops {
			t.Fatalf("len = %d", len(items))
		}
	})

	t.Run("rows for dates outside the slots are ignored", func(t *testing.T) {
		items, _ := walk([]string{"2026-10-01"}, []model.JobOccurrence{row("2026-10-09", service.StatusCompleted)}, service.StatusUnconfirmed, "2026-10-01")
		if got := summarize(items); !reflect.DeepEqual(got, []brief{{"2026-10-01", service.StateReal, "unconfirmed"}}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("nil rows and no slots", func(t *testing.T) {
		items, ended := service.WalkSchedule(nil, nil, service.StatusUnconfirmed, "2026-10-01", dt("2026-10-01"))
		if items == nil || len(items) != 0 || ended {
			t.Fatalf("items=%v ended=%v", items, ended)
		}
	})

	t.Run("completedAt is reported in UTC", func(t *testing.T) {
		r := row("2026-10-01", service.StatusCompleted)
		local := time.Date(2026, 10, 1, 9, 30, 0, 0, time.FixedZone("+7", 7*3600))
		r.CompletedAt = &local
		items, _ := walk([]string{"2026-10-01"}, []model.JobOccurrence{r}, service.StatusUnconfirmed, "2026-10-02")
		if items[0].CompletedAt == nil || items[0].CompletedAt.Location() != time.UTC || !items[0].CompletedAt.Equal(local) {
			t.Fatalf("got %v", items[0].CompletedAt)
		}
	})

	t.Run("a stored row overrides the job status for the first occurrence", func(t *testing.T) {
		items, _ := walk([]string{"2026-10-01"}, []model.JobOccurrence{row("2026-10-01", service.StatusCompleted)}, service.StatusConfirmed, "2026-10-02")
		if items[0].Status != service.StatusCompleted {
			t.Fatalf("status %q", items[0].Status)
		}
	})

	t.Run("terminate on the first occurrence", func(t *testing.T) {
		items, ended := walk([]string{"2026-10-01", "2026-10-02"}, nil, service.StatusTerminateService, "2026-10-09")
		if len(items) != 1 || items[0].Status != service.StatusTerminateService || !ended {
			t.Fatalf("items=%v ended=%v", items, ended)
		}
	})
}

// Random data (not all of it reachable through the API) must still respect the
// walk's structural rules.
func TestWalkScheduleInvariants(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	all := []string{service.StatusUnconfirmed, service.StatusConfirmed, service.StatusCompleted, service.StatusCanceled, service.StatusTerminateService, service.StatusRescheduled}
	base := dt("2026-10-01")
	day := func(n int) string { return civil.Format(base.AddDate(0, 0, n)) }

	for i := 0; i < 3000; i++ {
		n := 1 + rng.Intn(12)
		slots := make([]string, n)
		var rows []model.JobOccurrence
		for k := range slots {
			slots[k] = day(k)
			if rng.Intn(3) == 0 {
				continue
			}
			st := all[rng.Intn(len(all))]
			r := row(slots[k], st)
			if st == service.StatusRescheduled && rng.Intn(4) != 0 {
				to := base.AddDate(0, 0, 40+k)
				r.RescheduledTo = &to
				visit := row(civil.Format(to), all[rng.Intn(4)]) // an open or completed visit
				visit.RescheduledFrom = dp(slots[k])
				rows = append(rows, visit)
			}
			rows = append(rows, r)
		}
		today := day(rng.Intn(20))
		items, ended := service.WalkSchedule(slots, rowsByDate(rows), all[rng.Intn(6)], slots[0], dt(today))

		if len(items) == 0 {
			t.Fatalf("case %d: empty output for %d slots", i, n)
		}
		blocked := false
		for idx, it := range items {
			last := idx == len(items)-1
			if (it.Status == service.StatusTerminateService || it.State == service.StateOverdue) && !last {
				t.Fatalf("case %d: item %d (%+v) must end the walk but %d items follow", i, idx, it, len(items)-1-idx)
			}
			wantState := service.StateReal
			if blocked {
				wantState = service.StateHollow
			}
			if it.State != service.StateOverdue && it.State != wantState {
				t.Fatalf("case %d: item %d %+v has state %s, want %s", i, idx, it, it.State, wantState)
			}
			switch {
			case service.IsResolved(it.Status):
				blocked = false
			case service.IsOpen(it.Status):
				blocked = true
			}
		}
		last := items[len(items)-1]
		wantEnded := last.Status == service.StatusTerminateService || last.State == service.StateOverdue
		if ended != wantEnded {
			t.Fatalf("case %d: ended=%v but last item is %+v", i, ended, last)
		}
	}
}

func rowsByDate(rows []model.JobOccurrence) map[string]model.JobOccurrence {
	m := make(map[string]model.JobOccurrence, len(rows))
	for _, r := range rows {
		m[civil.Format(r.OccurrenceDate)] = r
	}
	return m
}
