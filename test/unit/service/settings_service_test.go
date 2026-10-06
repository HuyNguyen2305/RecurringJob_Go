package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

type fakeSettings struct {
	tz     string
	getErr error
	setErr error
	saved  []string
}

func (f *fakeSettings) Get(context.Context) (*model.TenantSettings, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &model.TenantSettings{ID: 1, Timezone: f.tz}, nil
}

func (f *fakeSettings) SetTimezone(_ context.Context, tz string) (*model.TenantSettings, error) {
	if f.setErr != nil {
		return nil, f.setErr
	}
	f.saved = append(f.saved, tz)
	f.tz = tz
	return &model.TenantSettings{ID: 1, Timezone: tz}, nil
}

func TestSettingsUpdateTimezone(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		tz   string
		want int // 0 = accepted
	}{
		{"utc", "UTC", 0},
		{"los angeles", "America/Los_Angeles", 0},
		{"kiritimati (UTC+14)", "Pacific/Kiritimati", 0},
		{"kolkata (UTC+5:30)", "Asia/Kolkata", 0},
		{"empty", "", 400},
		{"unknown zone", "Mars/Base", 400},
		{"Local is the server's zone, not a tenant setting", "Local", 400},
		{"too long", strings.Repeat("a", 65), 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeSettings{tz: "UTC"}
			got, err := service.NewSettingsService(store).UpdateTimezone(ctx, tt.tz)
			if tt.want == 0 {
				if err != nil || got.Timezone != tt.tz || len(store.saved) != 1 {
					t.Fatalf("got %+v err=%v saved=%v", got, err, store.saved)
				}
				return
			}
			if statusOf(t, err) != tt.want || len(store.saved) != 0 {
				t.Fatalf("err=%v saved=%v, want %d and nothing saved", err, store.saved, tt.want)
			}
		})
	}

	t.Run("a store failure propagates", func(t *testing.T) {
		boom := errors.New("boom")
		_, err := service.NewSettingsService(&fakeSettings{setErr: boom}).UpdateTimezone(ctx, "UTC")
		if !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestSettingsToday(t *testing.T) {
	ctx := context.Background()
	at := func(s string) func() time.Time {
		return func() time.Time {
			v, err := time.Parse(time.RFC3339, s)
			if err != nil {
				panic(err)
			}
			return v
		}
	}
	tests := []struct {
		name string
		tz   string
		now  string
		want string
	}{
		{"UTC is the UTC day", "UTC", "2026-10-05T23:30:00Z", "2026-10-05"},
		{"UTC-8 evening is still the day before in UTC terms", "America/Los_Angeles", "2026-10-06T02:30:00Z", "2026-10-05"},
		{"UTC-8 morning", "America/Los_Angeles", "2026-10-05T12:00:00Z", "2026-10-05"},
		{"UTC+14 is already tomorrow", "Pacific/Kiritimati", "2026-10-05T12:00:00Z", "2026-10-06"},
		{"UTC+14 at 09:59Z is still the 5th", "Pacific/Kiritimati", "2026-10-05T09:59:00Z", "2026-10-05"},
		{"UTC+5:30 crosses midnight at 18:30Z", "Asia/Kolkata", "2026-10-05T18:30:00Z", "2026-10-06"},
		{"UTC+5:30 just before", "Asia/Kolkata", "2026-10-05T18:29:00Z", "2026-10-05"},
		{"an unloadable stored zone falls back to UTC", "Not/AZone", "2026-10-05T23:30:00Z", "2026-10-05"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := service.NewSettingsService(&fakeSettings{tz: tt.tz}).WithClock(at(tt.now))
			got, err := s.Today(ctx)
			if err != nil || !got.Equal(dt(tt.want)) {
				t.Fatalf("Today = %v err=%v, want %s", got, err, tt.want)
			}
			if got := s.TodayFor(tt.tz); !got.Equal(dt(tt.want)) {
				t.Fatalf("TodayFor = %v, want %s", got, tt.want)
			}
		})
	}

	t.Run("a store failure propagates", func(t *testing.T) {
		boom := errors.New("boom")
		if _, err := service.NewSettingsService(&fakeSettings{getErr: boom}).Today(ctx); !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})
}
