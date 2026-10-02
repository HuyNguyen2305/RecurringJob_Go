package dto_test

import (
	"testing"
	"time"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/dto"
	"recurringjob/internal/model"
)

func TestNewOccurrenceResponse(t *testing.T) {
	t.Run("minimal row has null optionals", func(t *testing.T) {
		got := dto.NewOccurrenceResponse(&model.JobOccurrence{JobID: "j", OccurrenceDate: civil.New(2026, 10, 2), Status: "confirmed"})
		if got.JobID != "j" || got.Date != "2026-10-02" || got.Status != "confirmed" ||
			got.RescheduledTo != nil || got.RescheduledFrom != nil || got.CompletedAt != nil {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("reschedule fields are formatted as dates", func(t *testing.T) {
		to, from := civil.New(2026, 10, 12), civil.New(2026, 10, 9)
		got := dto.NewOccurrenceResponse(&model.JobOccurrence{OccurrenceDate: civil.New(2026, 10, 9), Status: "rescheduled", RescheduledTo: &to, RescheduledFrom: &from})
		if got.RescheduledTo == nil || *got.RescheduledTo != "2026-10-12" || got.RescheduledFrom == nil || *got.RescheduledFrom != "2026-10-09" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("completedAt is converted to UTC", func(t *testing.T) {
		local := time.Date(2026, 10, 2, 9, 33, 27, 0, time.FixedZone("+7", 7*3600))
		got := dto.NewOccurrenceResponse(&model.JobOccurrence{Status: "completed", CompletedAt: &local})
		if got.CompletedAt == nil || got.CompletedAt.Location() != time.UTC || !got.CompletedAt.Equal(local) {
			t.Fatalf("got %v", got.CompletedAt)
		}
		if got.CompletedAt.Hour() != 2 {
			t.Fatalf("hour %d, want 2 (09:33 +07:00 == 02:33 UTC)", got.CompletedAt.Hour())
		}
	})
	t.Run("does not mutate the model", func(t *testing.T) {
		local := time.Date(2026, 10, 2, 9, 33, 27, 0, time.FixedZone("+7", 7*3600))
		m := &model.JobOccurrence{CompletedAt: &local}
		dto.NewOccurrenceResponse(m)
		if m.CompletedAt.Location() == time.UTC {
			t.Fatal("model value was converted in place")
		}
	})
}
