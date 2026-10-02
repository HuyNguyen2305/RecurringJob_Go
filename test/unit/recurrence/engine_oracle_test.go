package recurrence_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"

	"recurringjob/internal/recurrence"
)

// The oracle decides, for every single calendar day, whether the day belongs
// to a schedule, using arithmetic that is deliberately different from the
// engine's iterators (day counting, week/month/year differences and ordinal
// predicates instead of stepping forward). It deliberately does not use the
// civil package.

func daysSinceEpoch(t time.Time) int { return int(t.Unix() / 86400) }

func daysIn(y int, m time.Month) int {
	switch m {
	case time.April, time.June, time.September, time.November:
		return 30
	case time.February:
		if y%4 == 0 && (y%100 != 0 || y%400 == 0) {
			return 29
		}
		return 28
	}
	return 31
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func ordinalOf(day int) int { return (day-1)/7 + 1 }

// oracleMember reports whether day d is in the raw schedule (before ends and
// except rules) of rule anchored at anchor.
func oracleMember(r recurrence.Rule, anchor, d time.Time) bool {
	if d.Before(anchor) {
		return false
	}
	interval := r.Interval
	if interval < 1 {
		interval = 1
	}
	monthsDiff := (d.Year()-anchor.Year())*12 + int(d.Month()) - int(anchor.Month())
	yearsDiff := d.Year() - anchor.Year()
	hasWeekday := func() bool {
		for _, w := range r.WeeklyDaysOfWeek {
			if int(d.Weekday()) == w {
				return true
			}
		}
		return false
	}

	switch r.Frequency {
	case recurrence.FreqDaily:
		return (daysSinceEpoch(d)-daysSinceEpoch(anchor))%interval == 0
	case recurrence.FreqWeekly:
		if !hasWeekday() {
			return false
		}
		switch r.WeeklyPeriod {
		case recurrence.WeeklyEvery:
			sunD := daysSinceEpoch(d) - int(d.Weekday())
			sunA := daysSinceEpoch(anchor) - int(anchor.Weekday())
			return ((sunD-sunA)/7)%interval == 0
		case recurrence.WeeklyFirstThird:
			o := ordinalOf(d.Day())
			return o == 1 || o == 3
		default:
			o := ordinalOf(d.Day())
			return o == 2 || o == 4
		}
	case recurrence.FreqMonthly:
		if monthsDiff%interval != 0 {
			return false
		}
		if r.MonthlyRepeatBy == recurrence.RepeatDayOfMonth {
			return d.Day() == minInt(anchor.Day(), daysIn(d.Year(), d.Month()))
		}
		return d.Weekday() == anchor.Weekday() && ordinalOf(d.Day()) == ordinalOf(anchor.Day())
	case recurrence.FreqYearly:
		if yearsDiff%interval != 0 || d.Month() != anchor.Month() {
			return false
		}
		if r.YearlyRepeatBy == recurrence.RepeatDayOfYear {
			return d.Day() == minInt(anchor.Day(), daysIn(d.Year(), d.Month()))
		}
		return d.Weekday() == anchor.Weekday() && ordinalOf(d.Day()) == ordinalOf(anchor.Day())
	}
	return false
}

func oracleExcepted(r recurrence.Rule, d time.Time, exclude map[string]struct{}) bool {
	switch r.ExceptType {
	case recurrence.ExceptMonth:
		for _, m := range r.ExceptMonths {
			if int(d.Month()) == m {
				return true
			}
		}
	case recurrence.ExceptCondition:
		if int(d.Weekday()) != *r.ExceptConditionDayOfWeek {
			return false
		}
		if r.ExceptConditionEvery == recurrence.ConditionEveryWeek {
			return true
		}
		switch r.ExceptConditionPeriod {
		case "last":
			return d.Day()+7 > daysIn(d.Year(), d.Month())
		default:
			n := int(r.ExceptConditionPeriod[0] - '0')
			return ordinalOf(d.Day()) == n
		}
	case recurrence.ExceptFrequency:
		_, ok := exclude[d.Format("2006-01-02")]
		return ok
	}
	return false
}

// oracle returns the expected occurrence list over [anchor, end].
func oracle(r recurrence.Rule, anchor, end time.Time, exclude map[string]struct{}) []string {
	out := []string{}
	count := 0
	var endsOn time.Time
	if r.EndsType == recurrence.EndsOnDate {
		endsOn, _ = time.Parse("2006-01-02", r.EndsOnDate)
	}
	for day := anchor; !day.After(end); day = day.AddDate(0, 0, 1) {
		if !oracleMember(r, anchor, day) {
			continue
		}
		count++
		if r.EndsType == recurrence.EndsAfter && count > r.EndsAfterCount {
			break
		}
		if r.EndsType == recurrence.EndsOnDate && day.After(endsOn) {
			break
		}
		if oracleExcepted(r, day, exclude) {
			continue
		}
		out = append(out, day.Format("2006-01-02"))
	}
	return out
}

func randomRule(rng *rand.Rand, anchor, end time.Time) (recurrence.Rule, map[string]struct{}) {
	r := recurrence.Rule{}
	r.Frequency = []string{recurrence.FreqDaily, recurrence.FreqWeekly, recurrence.FreqMonthly, recurrence.FreqYearly}[rng.Intn(4)]
	switch r.Frequency {
	case recurrence.FreqDaily:
		r.Interval = 1 + rng.Intn(40)
	case recurrence.FreqWeekly:
		r.WeeklyPeriod = []string{recurrence.WeeklyEvery, recurrence.WeeklyFirstThird, recurrence.WeeklySecondFour}[rng.Intn(3)]
		r.Interval = 1 + rng.Intn(6)
		r.WeeklyDaysOfWeek = append(r.WeeklyDaysOfWeek, rng.Perm(7)[:1+rng.Intn(4)]...) // unique, unsorted
	case recurrence.FreqMonthly:
		r.MonthlyRepeatBy = []string{recurrence.RepeatDayOfMonth, recurrence.RepeatDayOfWeek}[rng.Intn(2)]
		r.Interval = 1 + rng.Intn(14)
	case recurrence.FreqYearly:
		r.YearlyRepeatBy = []string{recurrence.RepeatDayOfYear, recurrence.RepeatDayOfWeek}[rng.Intn(2)]
		r.Interval = 1 + rng.Intn(5)
	}

	switch rng.Intn(3) {
	case 1:
		r.EndsType, r.EndsAfterCount = recurrence.EndsAfter, 1+rng.Intn(25)
	case 2:
		span := int(end.Sub(anchor).Hours() / 24)
		r.EndsType = recurrence.EndsOnDate
		r.EndsOnDate = anchor.AddDate(0, 0, rng.Intn(span)).Format("2006-01-02")
	}

	var exclude map[string]struct{}
	switch rng.Intn(5) {
	case 1:
		r.ExceptType = recurrence.ExceptMonth
		for _, m := range rng.Perm(12)[:1+rng.Intn(4)] {
			r.ExceptMonths = append(r.ExceptMonths, m+1)
		}
	case 2:
		r.ExceptType = recurrence.ExceptCondition
		r.ExceptConditionEvery = recurrence.ConditionEveryWeek
		r.ExceptConditionDayOfWeek = intp(rng.Intn(7))
	case 3:
		r.ExceptType = recurrence.ExceptCondition
		r.ExceptConditionEvery = recurrence.ConditionEveryMonth
		r.ExceptConditionDayOfWeek = intp(rng.Intn(7))
		r.ExceptConditionPeriod = []string{"1st", "2nd", "3rd", "4th", "5th", "last"}[rng.Intn(6)]
	case 4:
		r.ExceptType = recurrence.ExceptFrequency
		r.ExceptJobID = "other"
		exclude = map[string]struct{}{}
		for i := 0; i < 40; i++ {
			exclude[anchor.AddDate(0, 0, rng.Intn(int(end.Sub(anchor).Hours()/24))).Format("2006-01-02")] = struct{}{}
		}
	}
	return r, exclude
}

func TestEngineMatchesBruteForceOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	for i := 0; i < 1500; i++ {
		anchor := time.Date(2024+rng.Intn(6), time.Month(1+rng.Intn(12)), 1+rng.Intn(31), 0, 0, 0, 0, time.UTC)
		if anchor.Month() == 0 {
			continue
		}
		end := anchor.AddDate(8, 0, 0)
		rule, exclude := randomRule(rng, anchor, end)

		want := oracle(rule, anchor, end, exclude)
		got, err := recurrence.GenerateOccurrences(rule, anchor, recurrence.Options{To: end, Limit: recurrence.Unlimited, ExcludeDates: exclude})
		if err != nil {
			t.Fatalf("case %d: %+v anchor %s: %v", i, rule, anchor.Format("2006-01-02"), err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d MISMATCH\nrule   %+v\nanchor %s\ngot  (%d) %v\nwant (%d) %v",
				i, rule, anchor.Format("2006-01-02"), len(got), head(got), len(want), head(want))
		}
	}
}

func head(s []string) []string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// From and Limit only trim the output; they must equal slicing the full list.
func TestEngineFromAndLimitAreJustTrims(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 400; i++ {
		anchor := time.Date(2024+rng.Intn(4), time.Month(1+rng.Intn(12)), 1+rng.Intn(28), 0, 0, 0, 0, time.UTC)
		end := anchor.AddDate(4, 0, 0)
		rule, exclude := randomRule(rng, anchor, end)
		full, err := recurrence.GenerateOccurrences(rule, anchor, recurrence.Options{To: end, Limit: recurrence.Unlimited, ExcludeDates: exclude})
		if err != nil {
			t.Fatal(err)
		}
		from := anchor.AddDate(0, 0, rng.Intn(1200))
		limit := 1 + rng.Intn(20)

		var want []string
		for _, s := range full {
			if s >= from.Format("2006-01-02") { // ISO dates compare correctly as strings
				want = append(want, s)
			}
		}
		if len(want) > limit {
			want = want[:limit]
		}
		got, err := recurrence.GenerateOccurrences(rule, anchor, recurrence.Options{From: from, To: end, Limit: limit, ExcludeDates: exclude})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) || (len(want) > 0 && !reflect.DeepEqual(got, want)) {
			t.Fatalf("case %d rule %+v from %s limit %d\ngot  %v\nwant %v", i, rule, from.Format("2006-01-02"), limit, got, want)
		}
	}
}

func TestOracleSelfCheck(t *testing.T) {
	// A hand-computed sanity check so the oracle itself is not trusted blindly.
	r := recurrence.Rule{Frequency: recurrence.FreqWeekly, WeeklyPeriod: recurrence.WeeklyEvery, WeeklyDaysOfWeek: []int{5}}
	got := oracle(r, d("2026-10-02"), d("2026-10-31"), nil)
	want := []string{"2026-10-02", "2026-10-09", "2026-10-16", "2026-10-23", "2026-10-30"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if !sort.StringsAreSorted(got) {
		t.Fatal("not sorted")
	}
	if got := fmt.Sprint(daysIn(1900, time.February), daysIn(2000, time.February), daysIn(2026, time.April)); got != "28 29 30" {
		t.Fatalf("daysIn: %s", got)
	}
}
