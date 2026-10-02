package recurrence_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"recurringjob/internal/recurrence"
)

// FuzzGenerateOccurrences checks invariants that must hold for every rule the
// engine accepts. The seeds below run as ordinary tests; run the real fuzzer
// with: go test ./internal/recurrence -fuzz=FuzzGenerateOccurrences
func FuzzGenerateOccurrences(f *testing.F) {
	// freq, mode, interval, anchorOffsetDays, weekdayMask, endsKind, endsN, exceptKind, limit, toOffsetDays
	f.Add(uint8(0), uint8(0), int16(1), int32(0), uint8(0), uint8(0), int16(0), uint8(0), uint16(10), int32(0))
	f.Add(uint8(1), uint8(0), int16(2), int32(300), uint8(0b0101010), uint8(1), int16(5), uint8(0), uint16(50), int32(0))
	f.Add(uint8(1), uint8(1), int16(1), int32(12), uint8(0b0100000), uint8(0), int16(0), uint8(1), uint16(20), int32(900))
	f.Add(uint8(1), uint8(2), int16(3), int32(77), uint8(0b1111111), uint8(2), int16(400), uint8(2), uint16(500), int32(0))
	f.Add(uint8(2), uint8(1), int16(12), int32(5000), uint8(0), uint8(0), int16(0), uint8(3), uint16(30), int32(0))
	f.Add(uint8(3), uint8(1), int16(999), int32(20000), uint8(0), uint8(1), int16(3), uint8(0), uint16(7), int32(0))
	f.Add(uint8(3), uint8(0), int16(4), int32(1154), uint8(0), uint8(0), int16(0), uint8(1), uint16(5), int32(40000))
	// near the end of the supported calendar (mask bit 7 set)
	f.Add(uint8(0), uint8(0), int16(1), int32(3), uint8(0x80), uint8(0), int16(0), uint8(0), uint16(20), int32(0))
	f.Add(uint8(1), uint8(0), int16(1), int32(40), uint8(0x80|0b0100000), uint8(0), int16(0), uint8(0), uint16(20), int32(0))
	f.Add(uint8(3), uint8(1), int16(1), int32(500), uint8(0x80), uint8(0), int16(0), uint8(0), uint16(20), int32(0))

	f.Fuzz(checkEngineInvariants)
}

// checkEngineInvariants builds a rule from the raw parameters and verifies the properties every accepted rule must satisfy.
func checkEngineInvariants(t *testing.T, freq, mode uint8, interval int16, anchorOff int32, mask, endsKind uint8, endsN int16, exceptKind uint8, limit uint16, toOff int32) {
	t.Helper()
	fail := func(format string, a ...any) {
		t.Helper()
		t.Fatalf(fmt.Sprintf("params(freq=%d mode=%d interval=%d anchorOff=%d mask=%#x endsKind=%d endsN=%d exceptKind=%d limit=%d toOff=%d): ", freq, mode, interval, anchorOff, mask, endsKind, endsN, exceptKind, limit, toOff)+format, a...)
	}
	base := time.Date(1950, 1, 1, 0, 0, 0, 0, time.UTC)
	anchor := base.AddDate(0, 0, int(uint32(anchorOff)%80000)) // 1950 .. ~2169
	if mask&0x80 != 0 {                                        // bit 7 is unused by the weekday mask: probe the year-9999 edge
		anchor = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -int(uint32(anchorOff)%800))
	}
	rule := recurrence.Rule{Interval: int(interval)}

	switch freq % 4 {
	case 0:
		rule.Frequency = recurrence.FreqDaily
	case 1:
		rule.Frequency = recurrence.FreqWeekly
		rule.WeeklyPeriod = []string{recurrence.WeeklyEvery, recurrence.WeeklyFirstThird, recurrence.WeeklySecondFour}[mode%3]
		for w := 0; w < 7; w++ { // unique by construction
			if mask&(1<<w) != 0 {
				rule.WeeklyDaysOfWeek = append(rule.WeeklyDaysOfWeek, w)
			}
		}
		if len(rule.WeeklyDaysOfWeek) == 0 {
			rule.WeeklyDaysOfWeek = []int{int(anchor.Weekday())}
		}
	case 2:
		rule.Frequency = recurrence.FreqMonthly
		rule.MonthlyRepeatBy = []string{recurrence.RepeatDayOfMonth, recurrence.RepeatDayOfWeek}[mode%2]
	case 3:
		rule.Frequency = recurrence.FreqYearly
		rule.YearlyRepeatBy = []string{recurrence.RepeatDayOfYear, recurrence.RepeatDayOfWeek}[mode%2]
	}
	var endsOn time.Time
	switch endsKind % 3 {
	case 1:
		rule.EndsType, rule.EndsAfterCount = recurrence.EndsAfter, 1+int(uint16(endsN)%500)
	case 2:
		rule.EndsType = recurrence.EndsOnDate
		endsOn = anchor.AddDate(0, 0, int(uint16(endsN)))
		rule.EndsOnDate = endsOn.Format("2006-01-02")
	}
	switch exceptKind % 4 {
	case 1:
		rule.ExceptType, rule.ExceptMonths = recurrence.ExceptMonth, []int{1 + int(mode)%12, 1 + int(mask)%12}
	case 2:
		rule.ExceptType, rule.ExceptConditionEvery = recurrence.ExceptCondition, recurrence.ConditionEveryWeek
		rule.ExceptConditionDayOfWeek = intp(int(mask) % 7)
	case 3:
		rule.ExceptType, rule.ExceptConditionEvery = recurrence.ExceptCondition, recurrence.ConditionEveryMonth
		rule.ExceptConditionPeriod = []string{"1st", "2nd", "3rd", "4th", "5th", "last"}[int(mode)%6]
		rule.ExceptConditionDayOfWeek = intp(int(mask) % 7)
	}

	opts := recurrence.Options{Limit: 1 + int(limit)%2000}
	var to time.Time
	if toOff != 0 {
		to = anchor.AddDate(0, 0, int(uint32(toOff)%60000))
		opts.To = to
	}

	got, err := recurrence.GenerateOccurrences(rule, anchor, opts)
	if err != nil {
		return // rejecting a rule is allowed; panicking or hanging is not
	}
	if len(got) > opts.Limit {
		fail("returned %d > limit %d", len(got), opts.Limit)
	}
	if rule.EndsType == recurrence.EndsAfter && len(got) > rule.EndsAfterCount {
		fail("returned %d > endsAfterCount %d", len(got), rule.EndsAfterCount)
	}
	prev := time.Time{}
	for _, s := range got {
		day, perr := time.Parse("2006-01-02", s)
		if perr != nil {
			fail("not a civil date: %q", s)
		}
		if day.Before(anchor) {
			fail("%s before anchor %s", s, anchor.Format("2006-01-02"))
		}
		if !to.IsZero() && day.After(to) {
			fail("%s after To", s)
		}
		if rule.EndsType == recurrence.EndsOnDate && day.After(endsOn) {
			fail("%s after endsOnDate", s)
		}
		if !prev.IsZero() && !day.After(prev) {
			fail("not strictly ascending: %s then %s", prev.Format("2006-01-02"), s)
		}
		prev = day
	}
	again, err2 := recurrence.GenerateOccurrences(rule, anchor, opts)
	if err2 != nil || !reflect.DeepEqual(got, again) {
		fail("not deterministic")
	}
}

// The Go fuzzer cannot run on every platform (it does not on 32-bit Windows),
// so the same property check is also driven by a large seeded random run.
func TestEngineInvariantsRandomised(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	n := 20000
	if testing.Short() {
		n = 3000
	}
	for i := 0; i < n; i++ {
		mask := uint8(rng.Intn(256))
		if rng.Intn(6) == 0 {
			mask |= 0x80 // probe the year-9999 edge
		}
		checkEngineInvariants(t,
			uint8(rng.Intn(256)), uint8(rng.Intn(256)), int16(rng.Intn(65536)-32768),
			int32(rng.Uint32()), mask, uint8(rng.Intn(256)), int16(rng.Intn(65536)-32768),
			uint8(rng.Intn(256)), uint16(rng.Intn(65536)), int32(rng.Uint32()))
	}
}
