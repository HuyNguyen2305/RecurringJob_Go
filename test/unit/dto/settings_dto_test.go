package dto_test

import (
	"encoding/json"
	"testing"
	"time"

	"recurringjob/internal/dto"
	"recurringjob/internal/model"
)

func TestSettingsResponse(t *testing.T) {
	got := dto.NewSettingsResponse(&model.TenantSettings{ID: 1, Timezone: "Asia/Kolkata"}, time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC))
	raw, _ := json.Marshal(got)
	if string(raw) != `{"timezone":"Asia/Kolkata","today":"2026-10-06"}` {
		t.Fatalf("json %s", raw)
	}
}
