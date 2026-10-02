package service

import (
	"context"
	"errors"
	"time"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
)

// MaxExceptCandidates caps how many raw candidates are generated while
// widening to satisfy a limit under an except-frequency rule.
const MaxExceptCandidates = 10_000

// Window bounds a resolve call. Zero From/To are open; Limit 0 means the
// engine default and recurrence.Unlimited means no cap.
type Window struct {
	From, To time.Time
	Limit    int
}

// OccurrenceResolver turns a job into its occurrence dates, honouring
// exceptType=frequency by subtracting another job's occurrences.
type OccurrenceResolver struct {
	jobs JobGetter
}

func NewOccurrenceResolver(jobs JobGetter) *OccurrenceResolver {
	return &OccurrenceResolver{jobs: jobs}
}

// ResolveOccurrences returns the job's occurrence dates in w, ascending.
// visited holds the ids already being resolved up the recursion; it is the
// cycle guard (A excepts B, B excepts A).
func (r *OccurrenceResolver) ResolveOccurrences(ctx context.Context, job *model.Job, w Window, visited map[string]struct{}) ([]string, error) {
	date := civil.Truncate(job.Date)
	if job.Recurrence == nil {
		if (!w.From.IsZero() && date.Before(civil.Truncate(w.From))) ||
			(!w.To.IsZero() && date.After(civil.Truncate(w.To))) {
			return []string{}, nil
		}
		return []string{civil.Format(date)}, nil
	}

	rule := *job.Recurrence
	_, seen := visited[job.ID]
	if rule.ExceptType != recurrence.ExceptFrequency || seen {
		return generate(rule, date, w, w.Limit, nil)
	}

	other, err := r.jobs.GetJob(ctx, rule.ExceptJobID)
	if err != nil {
		return nil, err
	}
	inner := make(map[string]struct{}, len(visited)+1)
	for id := range visited {
		inner[id] = struct{}{}
	}
	inner[job.ID] = struct{}{}

	wanted := w.Limit
	if wanted == 0 {
		wanted = recurrence.DefaultLimit
	}
	rawLimit := wanted
	if rawLimit != recurrence.Unlimited && rawLimit > MaxExceptCandidates {
		rawLimit = MaxExceptCandidates
	}
	for {
		cands, err := generate(rule, date, w, rawLimit, nil)
		if err != nil {
			return nil, err
		}
		if len(cands) == 0 {
			return cands, nil
		}
		from, _ := civil.Parse(cands[0])
		to, _ := civil.Parse(cands[len(cands)-1])
		excluded, err := r.ResolveOccurrences(ctx, other, Window{From: from, To: to, Limit: recurrence.Unlimited}, inner)
		if err != nil {
			return nil, err
		}
		skip := make(map[string]struct{}, len(excluded))
		for _, d := range excluded {
			skip[d] = struct{}{}
		}
		kept := make([]string, 0, len(cands))
		for _, d := range cands {
			if _, ok := skip[d]; !ok {
				kept = append(kept, d)
			}
		}
		exhausted := len(cands) < rawLimit || rawLimit == recurrence.Unlimited || rawLimit >= MaxExceptCandidates
		if len(kept) >= wanted || exhausted {
			if len(kept) > wanted {
				kept = kept[:wanted]
			}
			return kept, nil
		}
		rawLimit *= 2
		if rawLimit > MaxExceptCandidates {
			rawLimit = MaxExceptCandidates
		}
	}
}

func generate(rule recurrence.Rule, anchor time.Time, w Window, limit int, exclude map[string]struct{}) ([]string, error) {
	out, err := recurrence.GenerateOccurrences(rule, anchor, recurrence.Options{
		From: w.From, To: w.To, Limit: limit, ExcludeDates: exclude,
	})
	var re *recurrence.Error
	if errors.As(err, &re) {
		return nil, apperror.Validation(re.Msg)
	}
	return out, err
}
