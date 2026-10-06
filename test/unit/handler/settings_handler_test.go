package handler_test

import (
	"context"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/handler"
	"recurringjob/internal/model"
)

type fakeSettingsService struct {
	err    error
	called bool
	tz     string
}

func (f *fakeSettingsService) Get(context.Context) (*model.TenantSettings, error) {
	f.called = true
	if f.err != nil {
		return nil, f.err
	}
	return &model.TenantSettings{ID: 1, Timezone: "UTC"}, nil
}

func (f *fakeSettingsService) UpdateTimezone(_ context.Context, tz string) (*model.TenantSettings, error) {
	f.called, f.tz = true, tz
	if f.err != nil {
		return nil, f.err
	}
	return &model.TenantSettings{ID: 1, Timezone: tz}, nil
}

func (f *fakeSettingsService) TodayFor(string) time.Time {
	return time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
}

func settingsEngine(f *fakeSettingsService) *gin.Engine {
	return newEngine(func(r *gin.Engine) {
		h := handler.NewSettingsHandler(f)
		r.GET("/settings", h.Get)
		r.PUT("/settings", h.Update)
	})
}

func TestSettingsHandler(t *testing.T) {
	t.Run("get returns the zone and today", func(t *testing.T) {
		w := do(settingsEngine(&fakeSettingsService{}), "GET", "/settings", "")
		data := decode(t, w)["data"].(map[string]any)
		if w.Code != 200 || data["timezone"] != "UTC" || data["today"] != "2026-10-05" {
			t.Fatalf("status %d body %s", w.Code, w.Body)
		}
	})

	t.Run("put passes the zone to the service", func(t *testing.T) {
		f := &fakeSettingsService{}
		w := do(settingsEngine(f), "PUT", "/settings", `{"timezone":"Asia/Kolkata"}`)
		data := decode(t, w)["data"].(map[string]any)
		if w.Code != 200 || f.tz != "Asia/Kolkata" || data["timezone"] != "Asia/Kolkata" {
			t.Fatalf("status %d tz %q body %s", w.Code, f.tz, w.Body)
		}
	})

	t.Run("put: bad bodies are 400 and never reach the service", func(t *testing.T) {
		for name, body := range map[string]string{"no zone": `{}`, "bad json": `{`, "wrong type": `{"timezone":1}`} {
			f := &fakeSettingsService{}
			if w := do(settingsEngine(f), "PUT", "/settings", body); w.Code != 400 || f.called {
				t.Errorf("%s: status %d called=%v", name, w.Code, f.called)
			}
		}
	})

	t.Run("service errors map through the middleware", func(t *testing.T) {
		bad := &fakeSettingsService{err: apperror.Validation("unknown timezone")}
		if w := do(settingsEngine(bad), "PUT", "/settings", `{"timezone":"x"}`); w.Code != 400 {
			t.Errorf("put: %d", w.Code)
		}
		if w := do(settingsEngine(&fakeSettingsService{err: context.DeadlineExceeded}), "GET", "/settings", ""); w.Code != 500 {
			t.Errorf("get: %d", w.Code)
		}
	})
}
