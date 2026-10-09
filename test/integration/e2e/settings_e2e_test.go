package e2e_test

import (
	"fmt"
	"testing"

	"recurringjob/internal/common/civil"
	"recurringjob/test/helpers"
)

func (c client) put(path, body string) resp { return c.do("PUT", path, body) }

func TestE2ESettingsAndTenantToday(t *testing.T) {
	c, _, _ := newClient(t)

	t.Run("a new tenant is on UTC", func(t *testing.T) {
		got := c.get("/settings").expect(t, 200)
		got.envelope(t)
		if got.obj()["timezone"] != "UTC" || got.obj()["today"] != civil.Format(civil.Today()) {
			t.Fatalf("settings: %s", got.raw)
		}
	})

	t.Run("bad zones are refused and change nothing", func(t *testing.T) {
		for name, body := range map[string]string{
			"unknown":  `{"timezone":"Mars/Base"}`,
			"Local":    `{"timezone":"Local"}`,
			"empty":    `{"timezone":""}`,
			"missing":  `{}`,
			"bad json": `{`,
		} {
			c.put("/settings", body).expect(t, 400).envelope(t)
			if got := c.get("/settings").expect(t, 200).obj()["timezone"]; got != "UTC" {
				t.Fatalf("%s: zone changed to %v", name, got)
			}
		}
	})

	// Approving needs a job date of today or later, where today is the
	// tenant's date. Two zones a day or more apart prove it follows the zone.
	// Approving creates a job, which locks the zone, so each zone gets its own tenant.
	for _, zone := range []string{"Pacific/Kiritimati", "Etc/GMT+12", "Asia/Kolkata", "UTC"} {
		t.Run("approval follows "+zone, func(t *testing.T) {
			c, _, _ := newClient(t)
			approveOn := func(date string) resp {
				id := c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)
				return c.post("/estimates/"+id+"/approve", fmt.Sprintf(`{"date":%q}`, date))
			}
			set := c.put("/settings", fmt.Sprintf(`{"timezone":%q}`, zone)).expect(t, 200)
			set.envelope(t)
			today, err := civil.Parse(set.obj()["today"].(string))
			if err != nil || set.obj()["timezone"] != zone {
				t.Fatalf("settings: %s", set.raw)
			}
			if got := c.get("/settings").expect(t, 200).obj(); got["timezone"] != zone || got["today"] != set.obj()["today"] {
				t.Fatalf("not stored: %v", got)
			}
			approveOn(civil.Format(civil.AddDays(today, -1))).expect(t, 400)
			approveOn(civil.Format(today)).expect(t, 200)
		})
	}
}

func TestE2ETimezoneIsLockedOnceThereAreJobs(t *testing.T) {
	c, _, _ := newClient(t)
	c.put("/settings", `{"timezone":"Asia/Kolkata"}`).expect(t, 200) // no jobs yet: free to change
	c.put("/settings", `{"timezone":"Asia/Tokyo"}`).expect(t, 200)

	c.post("/jobs", `{"date":"2026-10-02"}`).expect(t, 200)

	refused := c.put("/settings", `{"timezone":"UTC"}`).expect(t, 409)
	refused.envelope(t)
	if got := c.get("/settings").expect(t, 200).obj()["timezone"]; got != "Asia/Tokyo" {
		t.Fatalf("a refused change moved the zone to %v", got)
	}
	// Saving the same zone again is not a change.
	c.put("/settings", `{"timezone":"Asia/Tokyo"}`).expect(t, 200)
	// A bad zone is still a 400, not a 409.
	c.put("/settings", `{"timezone":"Mars/Base"}`).expect(t, 400)
}

func TestE2ESettingsAreTenantScoped(t *testing.T) {
	db := helpers.Connect(t)
	a, _ := helpers.NewSchema(t, db)
	b, _ := helpers.NewSchema(t, db)
	inA := newClientFor(t, db, a)
	inA.put("/settings", `{"timezone":"Pacific/Kiritimati"}`).expect(t, 200)

	inB := inA
	inB.tenant = b
	if got := inB.get("/settings").expect(t, 200).obj()["timezone"]; got != "UTC" {
		t.Fatalf("tenant b sees zone %v", got)
	}
	if got := inA.get("/settings").expect(t, 200).obj()["timezone"]; got != "Pacific/Kiritimati" {
		t.Fatalf("tenant a lost its zone: %v", got)
	}
}
