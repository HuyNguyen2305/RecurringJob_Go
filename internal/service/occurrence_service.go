package service

import (
	"context"
	"time"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
)

const (
	DefaultScheduleLimit = 100
	MaxScheduleLimit     = 1000
)

// scheduleHorizonSpan is how many years past its own date a job dated beyond
// 2099-12-31 is walked.
const scheduleHorizonSpan = 10

// ScheduleHorizon is where a schedule walk gives up for a job starting on
// jobDate: 2099-12-31, or jobDate plus ten years for a later job, never past
// the last representable year.
func ScheduleHorizon(jobDate time.Time) time.Time {
	horizon := civil.New(2099, 12, 31)
	if later := civil.Truncate(jobDate).AddDate(scheduleHorizonSpan, 0, 0); later.After(horizon) {
		horizon = later
	}
	if last := civil.New(recurrence.MaxYear, 12, 31); horizon.After(last) {
		horizon = last
	}
	return horizon
}

// OccurrenceRepository is the storage the occurrence service needs.
type OccurrenceRepository interface {
	ListByJob(ctx context.Context, jobID string) ([]model.JobOccurrence, error)
	Create(ctx context.Context, occ *model.JobOccurrence) error
	// UpdateGuarded updates only while status is in allowedFrom and returns
	// the number of rows affected.
	UpdateGuarded(ctx context.Context, id string, allowedFrom []string, updates map[string]any) (int64, error)
	Transaction(ctx context.Context, fn func(ctx context.Context) error) error
	// LockOccurrence takes the transaction-scoped lock of one occurrence, so
	// a status change and an invoice being created for it run one after the
	// other. It must be called inside Transaction.
	LockOccurrence(ctx context.Context, jobID string, date time.Time) error
}

// UpdateOccurrenceRequest is the PATCH body.
type UpdateOccurrenceRequest struct {
	Status        string
	RescheduledTo *time.Time
}

// OccurrenceInvoices handles the invoices of an occurrence that is canceled,
// terminated or rescheduled. InvoiceService satisfies it.
type OccurrenceInvoices interface {
	// VoidUnpaidForOccurrence voids the draft and sent invoices and returns
	// how many it voided.
	VoidUnpaidForOccurrence(ctx context.Context, jobID string, date time.Time) (int64, error)
	// MoveUnpaidForOccurrence moves the draft and sent invoices from one
	// occurrence date to another and returns how many it moved.
	MoveUnpaidForOccurrence(ctx context.Context, jobID string, from, to time.Time) (int64, error)
	// PaidIDsForOccurrence returns the paid invoices, which stay as they are.
	PaidIDsForOccurrence(ctx context.Context, jobID string, date time.Time) ([]string, error)
}

type OccurrenceService struct {
	jobs     JobGetter
	occs     OccurrenceRepository
	resolver *OccurrenceResolver
	invoices OccurrenceInvoices
	today    TodayProvider
	now      func() time.Time
}

func NewOccurrenceService(jobs JobGetter, occs OccurrenceRepository, resolver *OccurrenceResolver) *OccurrenceService {
	return &OccurrenceService{jobs: jobs, occs: occs, resolver: resolver, now: time.Now}
}

// WithClock replaces the clock used to decide what "today" is (for tests).
func (s *OccurrenceService) WithClock(now func() time.Time) *OccurrenceService {
	s.now = now
	return s
}

// WithToday makes "today" the tenant's calendar date instead of the UTC one.
func (s *OccurrenceService) WithToday(t TodayProvider) *OccurrenceService {
	s.today = t
	return s
}

// WithInvoices makes canceling or terminating an occurrence void its unpaid
// invoices and rescheduling it move them to the new date. It is set after construction because the invoice service itself
// depends on this one. Without it invoices are not touched.
func (s *OccurrenceService) WithInvoices(invoices OccurrenceInvoices) *OccurrenceService {
	s.invoices = invoices
	return s
}

// occurrenceView is what locate knows about one occurrence date.
type occurrenceView struct {
	row       *model.JobOccurrence
	status    string
	available bool
}

// locate checks date is a valid occurrence of job (series date or a
// rescheduled-to visit) and reports its status and availability.
func (s *OccurrenceService) locate(ctx context.Context, job *model.Job, rows map[string]model.JobOccurrence, date time.Time) (*occurrenceView, error) {
	ds := civil.Format(date)
	row, has := rows[ds]
	if has && row.RescheduledFrom != nil {
		return &occurrenceView{row: &row, status: row.Status, available: true}, nil
	}

	slots, err := s.resolver.ResolveOccurrences(ctx, job, Window{From: date, To: date, Limit: 1}, nil)
	if err != nil {
		return nil, err
	}
	if len(slots) == 0 {
		return nil, apperror.NotFound("date is not an occurrence of this job")
	}

	jobDate := civil.Format(job.Date)
	view := &occurrenceView{status: EffectiveStatus(job.Status, ds == jobDate)}
	if has {
		view.row = &row
		view.status = row.Status
	}

	upTo, err := s.resolver.ResolveOccurrences(ctx, job, Window{To: date, Limit: recurrence.Unlimited}, nil)
	if err != nil {
		return nil, err
	}
	today, err := currentDate(ctx, s.today, s.now)
	if err != nil {
		return nil, err
	}
	items, _ := WalkSchedule(upTo, rows, job.Status, jobDate, today)
	for _, it := range items {
		if it.Date == ds {
			view.available = it.State == StateReal || it.State == StateOverdue
		}
	}
	return view, nil
}

func (s *OccurrenceService) load(ctx context.Context, jobID string) (*model.Job, map[string]model.JobOccurrence, error) {
	if err := ValidateID("job id", jobID); err != nil {
		return nil, nil, err
	}
	job, err := s.jobs.GetJob(ctx, jobID)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.occs.ListByJob(ctx, jobID)
	if err != nil {
		return nil, nil, err
	}
	return job, rowsByDate(rows), nil
}

// ValidOccurrenceDate returns nil when date is an occurrence of the job.
// Invoices and work orders call it before attaching to an occurrence.
func (s *OccurrenceService) ValidOccurrenceDate(ctx context.Context, jobID string, date time.Time) error {
	job, rows, err := s.load(ctx, jobID)
	if err != nil {
		return err
	}
	_, err = s.locate(ctx, job, rows, civil.Truncate(date))
	return err
}

// AvailableFor returns nil when something can attach to the occurrence: it
// must be valid, not canceled/rescheduled/terminated, and not hollow (409).
func (s *OccurrenceService) AvailableFor(ctx context.Context, jobID string, date time.Time) error {
	job, rows, err := s.load(ctx, jobID)
	if err != nil {
		return err
	}
	view, err := s.locate(ctx, job, rows, civil.Truncate(date))
	if err != nil {
		return err
	}
	switch view.status {
	case StatusCanceled, StatusRescheduled, StatusTerminateService:
		return apperror.Conflict("occurrence is " + view.status)
	}
	if !view.available {
		return apperror.Conflict("occurrence is not available yet: an earlier occurrence is still open")
	}
	return nil
}

// UpdateOccurrence changes the status of one occurrence (PATCH).
func (s *OccurrenceService) UpdateOccurrence(ctx context.Context, jobID string, date time.Time, req UpdateOccurrenceRequest) (*model.JobOccurrence, error) {
	date = civil.Truncate(date)
	if !IsKnownStatus(req.Status) {
		return nil, apperror.Validation("unknown status")
	}
	if req.Status == StatusRescheduled && req.RescheduledTo == nil {
		return nil, apperror.Validation("rescheduledTo is required when status is rescheduled")
	}
	if req.Status != StatusRescheduled && req.RescheduledTo != nil {
		return nil, apperror.Validation("rescheduledTo is only allowed when status is rescheduled")
	}

	job, rows, err := s.load(ctx, jobID)
	if err != nil {
		return nil, err
	}
	view, err := s.locate(ctx, job, rows, date)
	if err != nil {
		return nil, err
	}
	if IsFinal(view.status) {
		return nil, apperror.Conflict("occurrence is already " + view.status + " and cannot change")
	}
	if !CanTransition(view.status, req.Status) {
		return nil, apperror.Conflict("cannot change an occurrence from " + view.status + " to " + req.Status)
	}

	today, err := currentDate(ctx, s.today, s.now)
	if err != nil {
		return nil, err
	}
	if req.Status == StatusCompleted && date.After(today) {
		return nil, apperror.Validation("a future occurrence cannot be completed")
	}

	var to time.Time
	if req.Status == StatusRescheduled {
		to = civil.Truncate(*req.RescheduledTo)
		if err := s.checkRescheduleTarget(ctx, job, rows, date, to, today); err != nil {
			return nil, err
		}
	}

	if !view.available {
		return nil, apperror.Conflict("occurrence is not available yet: an earlier occurrence is still open")
	}

	now := s.now().UTC().Truncate(time.Microsecond) // Postgres keeps microseconds
	updates := map[string]any{"status": req.Status}
	if req.Status == StatusCompleted {
		updates["completed_at"] = now
	}
	if req.Status == StatusRescheduled {
		updates["rescheduled_to"] = to
	}

	saved := model.JobOccurrence{JobID: jobID, OccurrenceDate: date, Status: req.Status}
	if view.row != nil {
		saved = *view.row
		saved.Status = req.Status
	}
	if req.Status == StatusCompleted {
		saved.CompletedAt = &now
	}
	if req.Status == StatusRescheduled {
		saved.RescheduledTo = &to
	}

	err = s.occs.Transaction(ctx, func(ctx context.Context) error {
		// An invoice being created for this occurrence waits for this
		// change (and then sees it), or this change waits for the invoice
		// (and then voids or moves it).
		if err := s.occs.LockOccurrence(ctx, jobID, date); err != nil {
			return err
		}
		if view.row != nil {
			n, err := s.occs.UpdateGuarded(ctx, view.row.ID, AllowedFrom(req.Status), updates)
			if err != nil {
				return err
			}
			if n == 0 {
				return apperror.Conflict("occurrence was changed by another request")
			}
		} else if err := s.occs.Create(ctx, &saved); err != nil {
			return err
		}
		if s.invoices != nil && (req.Status == StatusCanceled || req.Status == StatusTerminateService) {
			if _, err := s.invoices.VoidUnpaidForOccurrence(ctx, jobID, date); err != nil {
				return err
			}
			paid, err := s.invoices.PaidIDsForOccurrence(ctx, jobID, date)
			if err != nil {
				return err
			}
			saved.PaidInvoiceIDs = paid
		}
		if req.Status == StatusRescheduled {
			if err := s.occs.Create(ctx, &model.JobOccurrence{
				JobID: jobID, OccurrenceDate: to, Status: StatusUnconfirmed, RescheduledFrom: &date,
			}); err != nil {
				return err
			}
			// Draft and sent invoices follow the visit; a paid one stays on
			// the old date and is reported.
			if s.invoices != nil {
				if _, err := s.invoices.MoveUnpaidForOccurrence(ctx, jobID, date, to); err != nil {
					return err
				}
				paid, err := s.invoices.PaidIDsForOccurrence(ctx, jobID, date)
				if err != nil {
					return err
				}
				saved.PaidInvoiceIDs = paid
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

func (s *OccurrenceService) checkRescheduleTarget(ctx context.Context, job *model.Job, rows map[string]model.JobOccurrence, date, to, today time.Time) error {
	if !to.After(date) {
		return apperror.Validation("rescheduledTo must be after the occurrence date")
	}
	if to.Before(today) {
		return apperror.Validation("rescheduledTo cannot be in the past")
	}
	end, err := s.seriesEnd(ctx, job)
	if err != nil {
		return err
	}
	if end != nil && to.After(*end) {
		return apperror.Validation("rescheduledTo cannot be after the end of the series")
	}
	if _, taken := rows[civil.Format(to)]; taken {
		return apperror.Conflict("rescheduledTo already has an occurrence")
	}
	slots, err := s.resolver.ResolveOccurrences(ctx, job, Window{From: to, To: to, Limit: 1}, nil)
	if err != nil {
		return err
	}
	if len(slots) > 0 {
		return apperror.Conflict("rescheduledTo is already an occurrence date of this job")
	}
	return nil
}

// seriesEnd is the last date of a finite recurring series, nil when the job
// is a one-off or endless.
func (s *OccurrenceService) seriesEnd(ctx context.Context, job *model.Job) (*time.Time, error) {
	if job.Recurrence == nil {
		return nil, nil
	}
	rule := *job.Recurrence
	switch rule.EndsType {
	case recurrence.EndsOnDate:
		d, err := civil.Parse(rule.EndsOnDate)
		if err != nil {
			return nil, apperror.Validation("endsOnDate must be a YYYY-MM-DD date")
		}
		return &d, nil
	case recurrence.EndsAfter:
		rule.ExceptType = recurrence.ExceptOff
		dates, err := generate(rule, civil.Truncate(job.Date), Window{}, recurrence.Unlimited, nil)
		if err != nil || len(dates) == 0 {
			return nil, err
		}
		d, _ := civil.Parse(dates[len(dates)-1])
		return &d, nil
	}
	return nil, nil
}

// ScheduleQuery narrows a schedule; it only trims output, the walk always
// starts at the job date.
type ScheduleQuery struct {
	From, To time.Time
	Limit    int
}

// Schedule returns the job's visits with their real/hollow/overdue state.
func (s *OccurrenceService) Schedule(ctx context.Context, jobID string, q ScheduleQuery) ([]ScheduleItem, error) {
	if q.Limit == 0 {
		q.Limit = DefaultScheduleLimit
	}
	if q.Limit < 1 || q.Limit > MaxScheduleLimit {
		return nil, apperror.Validation("limit must be between 1 and 1000")
	}
	if !q.From.IsZero() && !q.To.IsZero() && q.From.After(q.To) {
		return nil, apperror.Validation("from cannot be after to")
	}
	job, rows, err := s.load(ctx, jobID)
	if err != nil {
		return nil, err
	}
	today, err := currentDate(ctx, s.today, s.now)
	if err != nil {
		return nil, err
	}
	jobDate := civil.Truncate(job.Date)
	horizon := ScheduleHorizon(jobDate)

	walk := func(upTo time.Time) ([]ScheduleItem, bool, error) {
		slots, err := s.resolver.ResolveOccurrences(ctx, job, Window{To: upTo, Limit: recurrence.Unlimited}, nil)
		if err != nil {
			return nil, false, err
		}
		items, ended := WalkSchedule(slots, rows, job.Status, civil.Format(jobDate), today)
		return items, ended, nil
	}
	trim := func(items []ScheduleItem) []ScheduleItem {
		out := []ScheduleItem{}
		for _, it := range items {
			d, _ := civil.Parse(it.Date)
			if !q.From.IsZero() && d.Before(civil.Truncate(q.From)) {
				continue
			}
			if !q.To.IsZero() && d.After(civil.Truncate(q.To)) {
				continue
			}
			out = append(out, it)
			if len(out) == q.Limit {
				break
			}
		}
		return out
	}

	if !q.To.IsZero() {
		items, _, err := walk(civil.Truncate(q.To))
		return trim(items), err
	}
	// A finite series is fully generated in one pass.
	if job.Recurrence == nil || (job.Recurrence.EndsType != "" && job.Recurrence.EndsType != recurrence.EndsNever) {
		items, _, err := walk(horizon)
		return trim(items), err
	}
	window := q.Limit * 2
	for {
		upTo := civil.AddDays(jobDate, window)
		if upTo.After(horizon) {
			upTo = horizon
		}
		items, ended, err := walk(upTo)
		if err != nil {
			return nil, err
		}
		visible := trim(items)
		if len(visible) >= q.Limit || ended || !upTo.Before(horizon) {
			return visible, nil
		}
		window *= 2
	}
}
