package service

import (
	"time"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
)

const (
	StateReal    = "real"
	StateHollow  = "hollow"
	StateOverdue = "overdue"

	// MaxChainHops bounds a reschedule chain so corrupt data cannot loop.
	MaxChainHops = 1000
)

// ScheduleItem is one visit in a job's schedule.
type ScheduleItem struct {
	Date            string     `json:"date"`
	State           string     `json:"state"`
	Status          string     `json:"status"`
	RescheduledTo   *string    `json:"rescheduledTo"`
	RescheduledFrom *string    `json:"rescheduledFrom"`
	CompletedAt     *time.Time `json:"completedAt"`
}

func rowsByDate(rows []model.JobOccurrence) map[string]model.JobOccurrence {
	m := make(map[string]model.JobOccurrence, len(rows))
	for _, r := range rows {
		m[civil.Format(r.OccurrenceDate)] = r
	}
	return m
}

func fmtPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := civil.Format(*t)
	return &s
}

// WalkSchedule walks the generated slots in order, gating each on the one
// before it. It returns the visits emitted and whether the series ended
// (terminate_service, or an overdue open occurrence that blocks the rest).
func WalkSchedule(slots []string, rows map[string]model.JobOccurrence, jobStatus, jobDate string, today time.Time) ([]ScheduleItem, bool) {
	items := []ScheduleItem{}
	available := true

	for _, slot := range slots {
		cur := slot
		for hop := 0; hop < MaxChainHops; hop++ {
			row, has := rows[cur]
			status := EffectiveStatus(jobStatus, cur == jobDate)
			item := ScheduleItem{Date: cur, State: StateHollow}
			if has {
				status = row.Status
				item.RescheduledTo = fmtPtr(row.RescheduledTo)
				item.RescheduledFrom = fmtPtr(row.RescheduledFrom)
				if row.CompletedAt != nil {
					utc := row.CompletedAt.UTC()
					item.CompletedAt = &utc
				}
			}
			item.Status = status
			if available {
				item.State = StateReal
			}

			switch {
			case status == StatusTerminateService:
				return append(items, item), true
			case status == StatusRescheduled:
				items = append(items, item)
				available = true
				if !has || row.RescheduledTo == nil {
					hop = MaxChainHops
					break
				}
				cur = civil.Format(*row.RescheduledTo)
				continue
			case IsResolved(status):
				items = append(items, item)
				available = true
			default: // open
				d, _ := civil.Parse(cur)
				if available && d.Before(today) {
					item.State = StateOverdue
					return append(items, item), true
				}
				items = append(items, item)
				available = false
			}
			break
		}
	}
	return items, false
}
