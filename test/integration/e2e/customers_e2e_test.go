package e2e_test

import (
	"fmt"
	"strings"
	"testing"

	"recurringjob/internal/common/civil"
	"recurringjob/test/helpers"
)

func TestE2ECustomers(t *testing.T) {
	c, _, _ := newClient(t) // already holds "Ada Lovelace" from the seed

	t.Run("create, get and update", func(t *testing.T) {
		created := c.post("/customers", `{"name":"  Bob Builder ","email":"bob@example.com","phone":"555-0199"}`).expect(t, 200)
		created.envelope(t)
		id, _ := created.obj()["id"].(string)
		if !uuidRe.MatchString(id) || created.obj()["name"] != "Bob Builder" || created.obj()["email"] != "bob@example.com" || created.obj()["phone"] != "555-0199" {
			t.Fatalf("created: %s", created.raw)
		}

		got := c.get("/customers/"+id).expect(t, 200)
		got.envelope(t)
		if got.obj()["id"] != id || got.obj()["name"] != "Bob Builder" {
			t.Fatalf("get: %s", got.raw)
		}

		upd := c.patch("/customers/"+id, `{"name":"Bob B.","email":""}`).expect(t, 200)
		upd.envelope(t)
		if upd.obj()["name"] != "Bob B." || upd.obj()["email"] != nil || upd.obj()["phone"] != "555-0199" {
			t.Fatalf("update: %s", upd.raw)
		}
	})

	t.Run("a customer with only a name has null email and phone", func(t *testing.T) {
		r := c.post("/customers", `{"name":"Bare"}`).expect(t, 200)
		if r.obj()["email"] != nil || r.obj()["phone"] != nil {
			t.Fatalf("got %s", r.raw)
		}
	})

	t.Run("list is ordered by name and pages", func(t *testing.T) {
		c2, _, _ := newClient(t)
		for _, n := range []string{"Zed", "Mo", "Bob"} {
			c2.post("/customers", fmt.Sprintf(`{"name":%q}`, n)).expect(t, 200)
		}
		names := func(query string) string {
			t.Helper()
			var out []string
			for _, it := range c2.get("/customers"+query).expect(t, 200).list() {
				out = append(out, it.(map[string]any)["name"].(string))
			}
			return strings.Join(out, ",")
		}
		if got := names(""); got != "Ada Lovelace,Bob,Mo,Zed" {
			t.Errorf("all: %q", got)
		}
		if got := names("?limit=2&offset=1"); got != "Bob,Mo" {
			t.Errorf("page: %q", got)
		}
		c2.get("/customers").expect(t, 200).envelope(t)
	})

	t.Run("bad requests are 400", func(t *testing.T) {
		id := c.refs.customer
		for name, r := range map[string]resp{
			"no name":               c.post("/customers", `{}`),
			"blank name":            c.post("/customers", `{"name":"   "}`),
			"name too long":         c.post("/customers", `{"name":"`+strings.Repeat("a", 256)+`"}`),
			"bad email":             c.post("/customers", `{"name":"A","email":"nope"}`),
			"bad json":              c.post("/customers", `{`),
			"get malformed id":      c.get("/customers/nope"),
			"patch empty":           c.patch("/customers/"+id, `{}`),
			"patch blank name":      c.patch("/customers/"+id, `{"name":" "}`),
			"patch bad email":       c.patch("/customers/"+id, `{"email":"nope"}`),
			"list bad limit":        c.get("/customers?limit=x"),
			"list limit too big":    c.get("/customers?limit=201"),
			"list negative offset":  c.get("/customers?offset=-1"),
			"location no address":   c.post("/customers/"+id+"/locations", `{}`),
			"location bad customer": c.post("/customers/nope/locations", `{"addressLine1":"x"}`),
			"locations bad id":      c.get("/customers/nope/locations"),
		} {
			if r.code != 400 {
				t.Errorf("%s: status %d: %s", name, r.code, r.raw)
				continue
			}
			r.envelope(t)
		}
	})

	t.Run("unknown customers are 404", func(t *testing.T) {
		for name, r := range map[string]resp{
			"get":            c.get("/customers/" + unknownID),
			"patch":          c.patch("/customers/"+unknownID, `{"name":"x"}`),
			"add location":   c.post("/customers/"+unknownID+"/locations", `{"addressLine1":"x"}`),
			"list locations": c.get("/customers/" + unknownID + "/locations"),
		} {
			if r.code != 404 {
				t.Errorf("%s: status %d: %s", name, r.code, r.raw)
				continue
			}
			r.envelope(t)
		}
	})

	t.Run("locations belong to one customer", func(t *testing.T) {
		cust := c.post("/customers", `{"name":"Locations"}`).expect(t, 200).obj()["id"].(string)
		a := c.post("/customers/"+cust+"/locations", `{"addressLine1":"1 Main Street","city":"Springfield","state":"IL","zip":"62701"}`).expect(t, 200)
		a.envelope(t)
		if a.obj()["customerId"] != cust || a.obj()["city"] != "Springfield" || a.obj()["zip"] != "62701" {
			t.Fatalf("location: %s", a.raw)
		}
		c.post("/customers/"+cust+"/locations", `{"addressLine1":"2 Side Street"}`).expect(t, 200)
		list := c.get("/customers/"+cust+"/locations").expect(t, 200)
		list.envelope(t)
		if len(list.list()) != 2 {
			t.Fatalf("list: %s", list.raw)
		}
		// Another customer's list does not include them.
		if n := len(c.get("/customers/"+c.refs.customer+"/locations").expect(t, 200).list()); n != 1 {
			t.Fatalf("the seeded customer has %d locations, want 1", n)
		}
	})

	t.Run("service types", func(t *testing.T) {
		c2, _, _ := newClient(t) // starts with "Window cleaning"
		created := c2.post("/service-types", `{"name":"Gutters","description":"Cleaning and repair"}`).expect(t, 200)
		created.envelope(t)
		if created.obj()["name"] != "Gutters" || created.obj()["description"] != "Cleaning and repair" || !uuidRe.MatchString(created.obj()["id"].(string)) {
			t.Fatalf("created: %s", created.raw)
		}
		list := c2.get("/service-types").expect(t, 200)
		list.envelope(t)
		if len(list.list()) != 2 || list.list()[0].(map[string]any)["name"] != "Gutters" {
			t.Fatalf("list (by name): %s", list.raw)
		}
		for name, body := range map[string]string{"no name": `{}`, "blank": `{"name":" "}`, "bad json": `{`} {
			if r := c2.post("/service-types", body); r.code != 400 {
				t.Errorf("%s: status %d", name, r.code)
			}
		}
	})
}

func TestE2ECustomersAreTenantScoped(t *testing.T) {
	db := helpers.Connect(t)
	a, _ := helpers.NewSchema(t, db)
	b, _ := helpers.NewSchema(t, db)
	inA := newClientFor(t, db, a)
	inB := inA
	inB.tenant = b

	id := inA.post("/customers", `{"name":"Only in A"}`).expect(t, 200).obj()["id"].(string)
	inB.get("/customers/"+id).expect(t, 404)
	if n := len(inB.get("/customers").expect(t, 200).list()); n != 0 {
		t.Fatalf("tenant B sees %d of A's customers", n)
	}
	inB.refs = nil
	// B cannot use A's customer for a job or an estimate either.
	body := fmt.Sprintf(`{"customerId":%q,"locationId":%q,"serviceTypeId":%q,"date":"2026-10-02","startTime":"09:00","lengthMinutes":60}`, inA.refs.customer, inA.refs.location, inA.refs.serviceType)
	inB.post("/jobs", body).expect(t, 404)
	inA.get("/customers/"+id).expect(t, 200)
}

func TestE2EJobsNeedCustomerLocationServiceAndTime(t *testing.T) {
	c, _, _ := newClient(t)
	noFill := c
	noFill.refs = nil
	otherCust := c.post("/customers", `{"name":"Bob"}`).expect(t, 200).obj()["id"].(string)
	otherLoc := c.post("/customers/"+otherCust+"/locations", `{"addressLine1":"2 Side Street"}`).expect(t, 200).obj()["id"].(string)
	body := func(mutate map[string]string) string {
		fields := map[string]string{
			"customerId": `"` + c.refs.customer + `"`, "locationId": `"` + c.refs.location + `"`, "serviceTypeId": `"` + c.refs.serviceType + `"`,
			"date": `"2026-10-02"`, "startTime": `"09:30"`, "lengthMinutes": `90`,
		}
		var parts []string
		for k, v := range fields {
			if nv, ok := mutate[k]; ok {
				if nv == "" {
					continue // drop the field
				}
				v = nv
			}
			parts = append(parts, `"`+k+`":`+v)
		}
		return "{" + strings.Join(parts, ",") + "}"
	}

	t.Run("a complete request creates the job and returns what was stored", func(t *testing.T) {
		r := noFill.post("/jobs", body(nil)).expect(t, 200)
		r.envelope(t)
		j := r.obj()
		if j["customerId"] != c.refs.customer || j["locationId"] != c.refs.location || j["serviceTypeId"] != c.refs.serviceType ||
			j["startTime"] != "09:30:00" || j["lengthMinutes"] != float64(90) || j["date"] != "2026-10-02" {
			t.Fatalf("job: %s", r.raw)
		}
	})

	t.Run("each field is required", func(t *testing.T) {
		for _, f := range []string{"customerId", "locationId", "serviceTypeId", "startTime", "lengthMinutes", "date"} {
			noFill.post("/jobs", body(map[string]string{f: ""})).expect(t, 400).envelope(t)
		}
	})

	t.Run("references that do not fit", func(t *testing.T) {
		noFill.post("/jobs", body(map[string]string{"locationId": `"` + otherLoc + `"`})).expect(t, 400).envelope(t)
		noFill.post("/jobs", body(map[string]string{"customerId": `"nope"`})).expect(t, 400).envelope(t)
		noFill.post("/jobs", body(map[string]string{"customerId": `"` + unknownID + `"`})).expect(t, 404).envelope(t)
		noFill.post("/jobs", body(map[string]string{"locationId": `"` + unknownID + `"`})).expect(t, 404).envelope(t)
		noFill.post("/jobs", body(map[string]string{"serviceTypeId": `"` + unknownID + `"`})).expect(t, 404).envelope(t)
		// The other customer's own location is fine with that customer.
		noFill.post("/jobs", body(map[string]string{"customerId": `"` + otherCust + `"`, "locationId": `"` + otherLoc + `"`})).expect(t, 200)
	})

	t.Run("start time and length", func(t *testing.T) {
		for _, ok := range []string{`"00:00"`, `"23:59"`, `"09:00:30"`} {
			noFill.post("/jobs", body(map[string]string{"startTime": ok})).expect(t, 200)
		}
		for _, bad := range []string{`"24:00"`, `"9"`, `"9am"`, `"09:60"`, `""`, `5`} {
			noFill.post("/jobs", body(map[string]string{"startTime": bad})).expect(t, 400)
		}
		for _, ok := range []string{`1`, `1440`} {
			noFill.post("/jobs", body(map[string]string{"lengthMinutes": ok})).expect(t, 200)
		}
		for _, bad := range []string{`0`, `-5`, `1441`, `"60"`} {
			noFill.post("/jobs", body(map[string]string{"lengthMinutes": bad})).expect(t, 400)
		}
	})
}

// A customer, location and service type chosen on an estimate carry through
// the approval to the job and from the job to its invoices.
func TestE2EDocumentsFollowTheCustomer(t *testing.T) {
	c, _, _ := newClient(t)
	today := civil.Format(civil.Today())
	otherCust := c.post("/customers", `{"name":"Bob Builder","email":"bob@example.com"}`).expect(t, 200).obj()["id"].(string)
	otherLoc := c.post("/customers/"+otherCust+"/locations", `{"addressLine1":"2 Side Street","city":"Shelbyville"}`).expect(t, 200).obj()["id"].(string)
	otherType := c.post("/service-types", `{"name":"Gutters"}`).expect(t, 200).obj()["id"].(string)

	est := c.post("/estimates", draftBody).expect(t, 200).obj()["id"].(string)

	t.Run("an estimate can move to another customer, location and service type while it has no job", func(t *testing.T) {
		upd := c.patch("/estimates/"+est, fmt.Sprintf(`{"customerId":%q,"locationId":%q,"serviceTypeId":%q}`, otherCust, otherLoc, otherType)).expect(t, 200)
		upd.envelope(t)
		if nameOf(upd.obj()["customer"]) != "Bob Builder" || streetOf(upd.obj()["location"]) != "2 Side Street" || nameOf(upd.obj()["serviceType"]) != "Gutters" {
			t.Fatalf("update: %s", upd.raw)
		}
		// Changing the customer alone leaves the old location, which is not theirs.
		c.patch("/estimates/"+est, fmt.Sprintf(`{"customerId":%q}`, c.refs.customer)).expect(t, 400).envelope(t)
		if got := c.get("/estimates/"+est).expect(t, 200).obj(); nameOf(got["customer"]) != "Bob Builder" {
			t.Fatalf("a refused edit changed the customer: %v", got["customer"])
		}
	})

	c.patch("/estimates/"+est+"/status", `{"status":"sent"}`).expect(t, 200)
	approved := c.post("/estimates/"+est+"/approve", fmt.Sprintf(`{"date":%q,"startTime":"14:15","lengthMinutes":45,"recurrence":{"frequency":"daily"}}`, today)).expect(t, 200)
	approved.envelope(t)
	jobID, _ := approved.obj()["jobId"].(string)

	t.Run("approval creates a job for the estimate's customer, with the given time and length", func(t *testing.T) {
		snap, _ := approved.obj()["jobSnapshot"].(map[string]any)
		want := map[string]any{
			"id": jobID, "date": today, "startTime": "14:15:00", "lengthMinutes": float64(45), "status": "unconfirmed",
			"customerId": otherCust, "customerName": "Bob Builder", "locationId": otherLoc, "locationAddress": "2 Side Street",
			"serviceTypeId": otherType, "serviceTypeName": "Gutters",
		}
		for k, v := range want {
			if snap[k] != v {
				t.Errorf("snapshot[%s] = %v, want %v (%v)", k, snap[k], v, snap)
			}
		}
		if rule, _ := snap["recurrence"].(map[string]any); rule["frequency"] != "daily" {
			t.Errorf("snapshot recurrence: %v", snap["recurrence"])
		}
	})

	t.Run("an approved estimate keeps its customer fixed, but notes stay editable", func(t *testing.T) {
		c.patch("/estimates/"+est, fmt.Sprintf(`{"customerId":%q,"locationId":%q}`, c.refs.customer, c.refs.location)).expect(t, 409).envelope(t)
		c.patch("/estimates/"+est, `{"notes":"adjusted"}`).expect(t, 200)
	})

	inv := c.post("/jobs/"+jobID+"/occurrences/"+today+"/invoice", draftBody).expect(t, 200)
	inv.envelope(t)

	t.Run("the invoice is for the job's customer, location and service type", func(t *testing.T) {
		o := inv.obj()
		if nameOf(o["customer"]) != "Bob Builder" || streetOf(o["location"]) != "2 Side Street" || nameOf(o["serviceType"]) != "Gutters" {
			t.Fatalf("invoice: %s", inv.raw)
		}
		snap, _ := o["jobSnapshot"].(map[string]any)
		if snap["customerName"] != "Bob Builder" || snap["locationAddress"] != "2 Side Street" || snap["serviceTypeName"] != "Gutters" || snap["startTime"] != "14:15:00" || snap["lengthMinutes"] != float64(45) {
			t.Fatalf("invoice snapshot: %v", snap)
		}
		got := c.get("/invoices/"+o["id"].(string)).expect(t, 200).obj()
		if nameOf(got["customer"]) != "Bob Builder" {
			t.Fatalf("reloaded invoice: %v", got)
		}
	})

	t.Run("an invoice's customer cannot be changed", func(t *testing.T) {
		id := inv.obj()["id"].(string)
		c.patch("/invoices/"+id, fmt.Sprintf(`{"customerId":%q}`, c.refs.customer)).expect(t, 400).envelope(t)
		c.patch("/invoices/"+id, `{"notes":"fine"}`).expect(t, 200)
	})

	t.Run("a snapshot keeps the customer's name as it was when the invoice was made", func(t *testing.T) {
		id := inv.obj()["id"].(string)
		c.patch("/customers/"+otherCust, `{"name":"Robert Builder"}`).expect(t, 200)
		got := c.get("/invoices/"+id).expect(t, 200).obj()
		snap, _ := got["jobSnapshot"].(map[string]any)
		if snap["customerName"] != "Bob Builder" {
			t.Fatalf("the snapshot must not follow later renames: %v", snap)
		}
		if nameOf(got["customer"]) != "Robert Builder" {
			t.Fatalf("the live customer is the current one: %v", got["customer"])
		}
	})

	t.Run("the list shows each estimate with its current customer", func(t *testing.T) {
		list := c.get("/estimates").expect(t, 200).list()
		if len(list) != 1 || nameOf(list[0].(map[string]any)["customer"]) != "Robert Builder" {
			t.Fatalf("list: %v", list)
		}
	})
}
