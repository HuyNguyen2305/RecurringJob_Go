package recurrence

import (
	"math"
	"sort"
	"time"

	"recurringjob/internal/common/civil"
)

const (
	// DefaultLimit caps the result size when Options.Limit is unset.
	DefaultLimit = 1000
	// MaxIterations is the hard stop on candidates examined per call.
	MaxIterations = 100_000
	// Unlimited is the explicit "no cap" Limit.
	Unlimited = math.MaxInt
	// MaxYear is the last year a civil date (YYYY-MM-DD) can have; a series
	// ends before it would emit a later date.
	MaxYear = 9999

	// maxEmptyPeriods bounds consecutive periods with no candidate (e.g. a
	// never-existing 5th weekday) so a malformed rule cannot spin forever.
	maxEmptyPeriods = 1000
)

// Options narrows what GenerateOccurrences returns. Zero time values mean
// "unset"; all dates are civil (UTC midnight).
type Options struct {
	From  time.Time
	To    time.Time
	Limit int // 0 = DefaultLimit; use Unlimited for no cap
	// ExcludeDates feeds exceptType=frequency. nil excludes nothing.
	ExcludeDates map[string]struct{}
}

// GenerateOccurrences returns the occurrence dates (YYYY-MM-DD) of rule
// anchored at anchor, ascending.
func GenerateOccurrences(rule Rule, anchor time.Time, opts Options) ([]string, error) {
	anchor = civil.Truncate(anchor)
	if err := checkRule(rule); err != nil {
		return nil, err
	}
	endsType := rule.endsType()
	if endsType == EndsNever && opts.To.IsZero() && opts.Limit == 0 {
		return nil, errf("an endless recurrence needs a 'to' date or a limit")
	}
	limit := opts.Limit
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 0 {
		return nil, errf("limit must be positive")
	}

	var endsOn time.Time
	if endsType == EndsOnDate {
		d, err := civil.Parse(rule.EndsOnDate)
		if err != nil {
			return nil, errf("endsOnDate must be a YYYY-MM-DD date")
		}
		endsOn = d
	}
	var from, to time.Time
	if !opts.From.IsZero() {
		from = civil.Truncate(opts.From)
	}
	if !opts.To.IsZero() {
		to = civil.Truncate(opts.To)
	}

	next, err := candidates(rule, anchor)
	if err != nil {
		return nil, err
	}

	out := []string{}
	ordinal := 0
	for i := 0; i < MaxIterations; i++ {
		d, ok := next()
		if !ok || d.Year() > MaxYear {
			break
		}
		ordinal++
		if endsType == EndsAfter && ordinal > rule.EndsAfterCount {
			break
		}
		if endsType == EndsOnDate && d.After(endsOn) {
			break
		}
		if !to.IsZero() && d.After(to) {
			break
		}
		if (from.IsZero() || !d.Before(from)) && !isExcepted(rule, d, opts.ExcludeDates) {
			out = append(out, civil.Format(d))
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func checkRule(r Rule) error {
	switch r.Frequency {
	case FreqDaily:
	case FreqWeekly:
		if len(r.WeeklyDaysOfWeek) == 0 {
			return errf("weeklyDaysOfWeek is required for weekly recurrence")
		}
		seen := map[int]bool{}
		for _, d := range r.WeeklyDaysOfWeek {
			if d < 0 || d > 6 {
				return errf("weeklyDaysOfWeek values must be 0..6")
			}
			if seen[d] {
				return errf("weeklyDaysOfWeek must not contain duplicates")
			}
			seen[d] = true
		}
		switch r.WeeklyPeriod {
		case WeeklyEvery, WeeklyFirstThird, WeeklySecondFour:
		default:
			return errf("weeklyPeriod is required for weekly recurrence")
		}
	case FreqMonthly:
		if r.MonthlyRepeatBy != RepeatDayOfMonth && r.MonthlyRepeatBy != RepeatDayOfWeek {
			return errf("monthlyRepeatBy is required for monthly recurrence")
		}
	case FreqYearly:
		if r.YearlyRepeatBy != RepeatDayOfYear && r.YearlyRepeatBy != RepeatDayOfWeek {
			return errf("yearlyRepeatBy is required for yearly recurrence")
		}
	default:
		return errf("frequency must be daily, weekly, monthly or yearly")
	}
	if r.endsType() == EndsAfter && r.EndsAfterCount < 1 {
		return errf("endsAfterCount must be >= 1")
	}
	return nil
}

// candidates returns a lazy iterator over the raw schedule starting at the
// anchor, before ends/except rules are applied.
func candidates(r Rule, anchor time.Time) (func() (time.Time, bool), error) {
	step := r.interval()
	am, ad, ay := int(anchor.Month()), anchor.Day(), anchor.Year()
	wd := anchor.Weekday()
	ord := civil.Ordinal(anchor)

	var batch func(k int) []time.Time
	switch r.Frequency {
	case FreqDaily:
		batch = func(k int) []time.Time {
			return []time.Time{civil.AddDays(anchor, k*step)}
		}
	case FreqWeekly:
		days := append([]int(nil), r.WeeklyDaysOfWeek...)
		sort.Ints(days)
		switch r.WeeklyPeriod {
		case WeeklyEvery:
			weekStart := civil.AddDays(anchor, -int(wd))
			batch = func(k int) []time.Time {
				ws := civil.AddDays(weekStart, 7*step*k)
				var out []time.Time
				for _, d := range days {
					if dt := civil.AddDays(ws, d); !dt.Before(anchor) {
						out = append(out, dt)
					}
				}
				return out
			}
		default: // first_third / second_fourth: ignores interval
			ords := [2]int{1, 3}
			if r.WeeklyPeriod == WeeklySecondFour {
				ords = [2]int{2, 4}
			}
			batch = func(k int) []time.Time {
				first := civil.New(ay, time.Month(am+k), 1)
				var out []time.Time
				for _, d := range days {
					for _, n := range ords {
						if dt, ok := civil.NthWeekday(first.Year(), first.Month(), time.Weekday(d), n); ok && !dt.Before(anchor) {
							out = append(out, dt)
						}
					}
				}
				sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
				return out
			}
		}
	case FreqMonthly:
		if r.MonthlyRepeatBy == RepeatDayOfMonth {
			batch = func(k int) []time.Time {
				return []time.Time{civil.MonthDayClamped(ay, am+k*step, ad)}
			}
		} else {
			batch = func(k int) []time.Time {
				first := civil.New(ay, time.Month(am+k*step), 1)
				if dt, ok := civil.NthWeekday(first.Year(), first.Month(), wd, ord); ok {
					return []time.Time{dt}
				}
				return nil
			}
		}
	case FreqYearly:
		if r.YearlyRepeatBy == RepeatDayOfYear {
			batch = func(k int) []time.Time {
				return []time.Time{civil.MonthDayClamped(ay+k*step, am, ad)}
			}
		} else {
			batch = func(k int) []time.Time {
				if dt, ok := civil.NthWeekday(ay+k*step, time.Month(am), wd, ord); ok {
					return []time.Time{dt}
				}
				return nil
			}
		}
	default:
		return nil, errf("unsupported frequency %q", r.Frequency)
	}

	k := 0
	var queue []time.Time
	return func() (time.Time, bool) {
		for empty := 0; len(queue) == 0; k++ {
			if empty >= maxEmptyPeriods {
				return time.Time{}, false
			}
			queue = batch(k)
			if len(queue) == 0 {
				empty++
			}
		}
		d := queue[0]
		queue = queue[1:]
		return d, true
	}, nil
}
