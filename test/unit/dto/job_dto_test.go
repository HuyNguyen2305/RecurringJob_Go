package dto_test

import (
	"encoding/json"
	"testing"
	"time"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/dto"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
)

func TestNewJobResponse(t *testing.T) {
	t.Run("one-off job", func(t *testing.T) {
		got := dto.NewJobResponse(&model.Job{ID: "id-1", Date: civil.New(2026, 10, 2), Status: "confirmed"})
		if got.ID != "id-1" || got.Date != "2026-10-02" || got.Status != "confirmed" || got.Recurrence != nil {
			t.Fatalf("got %+v", got)
		}
		b, _ := json.Marshal(got)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if v, present := m["recurrence"]; !present || v != nil {
			t.Fatalf("recurrence must serialise as null, got %v (present=%v)", v, present)
		}
	})
	t.Run("date comes from the UTC calendar day", func(t *testing.T) {
		late := time.Date(2026, 10, 3, 2, 0, 0, 0, time.FixedZone("+7", 7*3600)) // 2026-10-02 19:00 UTC
		if got := dto.NewJobResponse(&model.Job{Date: late}); got.Date != "2026-10-02" {
			t.Fatalf("date %q", got.Date)
		}
	})
	t.Run("recurrence is passed through with camelCase JSON", func(t *testing.T) {
		rule := &recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{1, 3}}
		got := dto.NewJobResponse(&model.Job{Date: civil.New(2026, 10, 2), Recurrence: rule})
		b, _ := json.Marshal(got)
		var m map[string]map[string]any
		_ = json.Unmarshal(b, &m)
		if m["recurrence"]["frequency"] != "weekly" || m["recurrence"]["weeklyPeriod"] != "every" {
			t.Fatalf("json %s", b)
		}
	})
}
