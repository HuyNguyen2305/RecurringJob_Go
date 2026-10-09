package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"recurringjob/internal/app"
	"recurringjob/internal/common/auth"
	"recurringjob/internal/common/civil"
	"recurringjob/test/helpers"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const unknownID = "00000000-0000-0000-0000-0000000000ff"

// client drives the production router (app.NewServer) in-process.
type client struct {
	t      *testing.T
	h      http.Handler
	tenant string // sent as X-Tenant-Schema when set

	// refs, when set, are added to POST /jobs and POST /estimates bodies (and a
	// start time and length to jobs and approvals) that do not carry them, so
	// tests about something else need not spell them out.
	refs *refIDs
}

// refIDs are the ids of a customer, one of its locations and a service type.
type refIDs struct{ customer, location, serviceType string }

type resp struct {
	code int
	raw  string
	body map[string]any
}

func (r resp) obj() map[string]any {
	m, _ := r.body["data"].(map[string]any)
	return m
}

func (r resp) list() []any {
	l, _ := r.body["data"].([]any)
	return l
}

func (c client) do(method, path, body string) resp {
	c.t.Helper()
	body = c.fill(method, path, body)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.tenant != "" {
		req.Header.Set(auth.TenantHeader, c.tenant)
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, req)
	out := resp{code: w.Code, raw: w.Body.String()}
	_ = json.Unmarshal(w.Body.Bytes(), &out.body)
	return out
}

func (c client) post(path, body string) resp  { return c.do("POST", path, body) }
func (c client) get(path string) resp         { return c.do("GET", path, "") }
func (c client) patch(path, body string) resp { return c.do("PATCH", path, body) }

func newClient(t *testing.T) (client, *gorm.DB, string) {
	t.Helper()
	db := helpers.Connect(t)
	schema, _ := helpers.NewSchema(t, db)
	c := client{t: t, h: app.NewServer(db, schema)}
	c.refs = seedRefs(t, c)
	return c, db, schema
}

func (r resp) expect(t *testing.T, code int) resp {
	t.Helper()
	if r.code != code {
		t.Fatalf("status %d, want %d: %s", r.code, code, r.raw)
	}
	return r
}

// envelope checks the response shape promised to API clients.
func (r resp) envelope(t *testing.T) {
	t.Helper()
	ok, _ := r.body["success"].(bool)
	msg, _ := r.body["message"].(string)
	_, hasData := r.body["data"]
	switch {
	case r.code == 200 && (!ok || msg == "" || !hasData):
		t.Fatalf("bad success envelope: %s", r.raw)
	case r.code != 200 && (ok || msg == "" || hasData):
		t.Fatalf("bad error envelope: %s", r.raw)
	}
}

func jsonRule(weekday time.Weekday) string {
	return fmt.Sprintf(`{"frequency":"weekly","weeklyPeriod":"every","weeklyDaysOfWeek":[%d]}`, int(weekday))
}

func TestE2EJobLifecycle(t *testing.T) {
	c, _, _ := newClient(t)
	today := civil.Today()
	day := func(n int) string { return civil.Format(civil.AddDays(today, n)) }

	created := c.post("/jobs", fmt.Sprintf(`{"date":%q,"recurrence":%s}`, day(0), jsonRule(today.Weekday()))).expect(t, 200)
	created.envelope(t)
	job := created.obj()
	id, _ := job["id"].(string)
	rule, _ := job["recurrence"].(map[string]any)
	if !uuidRe.MatchString(id) || job["date"] != day(0) || job["status"] != "unconfirmed" ||
		rule["interval"] != float64(1) || rule["endsType"] != "never" || rule["exceptType"] != "off" {
		t.Fatalf("created job: %s", created.raw)
	}

	occ := c.get("/jobs/"+id+"/occurrences?limit=3").expect(t, 200)
	occ.envelope(t)
	if got := fmt.Sprint(occ.list()); got != fmt.Sprint([]any{day(0), day(7), day(14)}) {
		t.Fatalf("occurrences %v", got)
	}

	states := func(limit int) string {
		t.Helper()
		var parts []string
		for _, it := range c.get(fmt.Sprintf("/jobs/%s/schedule?limit=%d", id, limit)).expect(t, 200).list() {
			m := it.(map[string]any)
			parts = append(parts, fmt.Sprintf("%s:%s:%s", m["date"], m["state"], m["status"]))
		}
		return strings.Join(parts, " ")
	}
	if got, want := states(3), fmt.Sprintf("%s:real:unconfirmed %s:hollow:unconfirmed %s:hollow:unconfirmed", day(0), day(7), day(14)); got != want {
		t.Fatalf("initial schedule\n got  %s\n want %s", got, want)
	}

	done := c.patch("/jobs/"+id+"/occurrences/"+day(0), `{"status":"completed"}`).expect(t, 200)
	done.envelope(t)
	completedAt, _ := done.obj()["completedAt"].(string)
	if parsed, err := time.Parse(time.RFC3339Nano, completedAt); err != nil || !strings.HasSuffix(completedAt, "Z") || time.Since(parsed).Abs() > 2*time.Minute {
		t.Fatalf("completedAt %q (%v)", completedAt, err)
	}
	// The PATCH response must equal what a later read returns (no sub-microsecond digits).
	matched := false
	for _, it := range c.get("/jobs/"+id+"/schedule").expect(t, 200).list() {
		if m, _ := it.(map[string]any); m != nil && m["date"] == day(0) {
			matched = true
			if m["completedAt"] != completedAt {
				t.Fatalf("completedAt differs between PATCH (%q) and schedule (%v)", completedAt, m["completedAt"])
			}
		}
	}
	if !matched {
		t.Fatal("the completed occurrence is missing from the schedule")
	}

	hollow := c.patch("/jobs/"+id+"/occurrences/"+day(14), `{"status":"confirmed"}`).expect(t, 409)
	hollow.envelope(t)

	moved := c.patch("/jobs/"+id+"/occurrences/"+day(7), fmt.Sprintf(`{"status":"rescheduled","rescheduledTo":%q}`, day(9))).expect(t, 200)
	if moved.obj()["rescheduledTo"] != day(9) || moved.obj()["status"] != "rescheduled" {
		t.Fatalf("reschedule response: %s", moved.raw)
	}
	want := fmt.Sprintf("%s:real:completed %s:real:rescheduled %s:real:unconfirmed %s:hollow:unconfirmed", day(0), day(7), day(9), day(14))
	if got := states(4); got != want {
		t.Fatalf("after reschedule\n got  %s\n want %s", got, want)
	}

	final := c.patch("/jobs/"+id+"/occurrences/"+day(0), `{"status":"canceled"}`).expect(t, 409)
	if !strings.Contains(final.raw, "completed") {
		t.Fatalf("message should say why: %s", final.raw)
	}
	c.patch("/jobs/"+id+"/occurrences/"+day(3), `{"status":"confirmed"}`).expect(t, 404).envelope(t)
	c.patch("/jobs/"+id+"/occurrences/nope", `{"status":"confirmed"}`).expect(t, 400)
	c.patch("/jobs/"+id+"/occurrences/"+day(9), `{"status":"rescheduled"}`).expect(t, 400)
	c.patch("/jobs/"+id+"/occurrences/"+day(9), fmt.Sprintf(`{"status":"confirmed","rescheduledTo":%q}`, day(10))).expect(t, 400)
	c.patch("/jobs/"+id+"/occurrences/"+day(9), `{"status":"bogus"}`).expect(t, 400)
	c.patch("/jobs/"+unknownID+"/occurrences/"+day(0), `{"status":"confirmed"}`).expect(t, 404)
	c.patch("/jobs/not-a-uuid/occurrences/"+day(0), `{"status":"confirmed"}`).expect(t, 400)
	c.get("/jobs/"+unknownID+"/schedule").expect(t, 404)
	c.get("/jobs/not-a-uuid/schedule").expect(t, 400)

	c.patch("/jobs/"+id+"/occurrences/"+day(9), `{"status":"terminate_service"}`).expect(t, 200)
	if got, want := states(10), fmt.Sprintf("%s:real:completed %s:real:rescheduled %s:real:terminate_service", day(0), day(7), day(9)); got != want {
		t.Fatalf("after terminate\n got  %s\n want %s", got, want)
	}
}

func TestE2EOverdueGating(t *testing.T) {
	c, _, _ := newClient(t)
	today := civil.Today()
	day := func(n int) string { return civil.Format(civil.AddDays(today, n)) }

	id, _ := c.post("/jobs", fmt.Sprintf(`{"date":%q,"recurrence":{"frequency":"daily"}}`, day(-2))).expect(t, 200).obj()["id"].(string)
	items := c.get("/jobs/"+id+"/schedule").expect(t, 200).list()
	if len(items) < 3 || items[0].(map[string]any)["state"] != "overdue" || items[0].(map[string]any)["date"] != day(-2) {
		t.Fatalf("an overdue first occurrence must come first: %v", items)
	}
	for i, it := range items[1:] {
		if it.(map[string]any)["state"] != "hollow" {
			t.Fatalf("item %d after the overdue one must be listed but locked (hollow): %v", i+1, it)
		}
	}
	c.patch("/jobs/"+id+"/occurrences/"+day(-1), `{"status":"completed"}`).expect(t, 409)
	c.patch("/jobs/"+id+"/occurrences/"+day(-2), `{"status":"completed"}`).expect(t, 200)
	c.patch("/jobs/"+id+"/occurrences/"+day(-1), `{"status":"completed"}`).expect(t, 200)
	c.patch("/jobs/"+id+"/occurrences/"+day(1), `{"status":"confirmed"}`).expect(t, 409) // hollow: today is still open
	c.patch("/jobs/"+id+"/occurrences/"+day(1), `{"status":"completed"}`).expect(t, 400) // a future date cannot be completed (checked first)
	c.patch("/jobs/"+id+"/occurrences/"+day(0), `{"status":"completed"}`).expect(t, 200)
	c.patch("/jobs/"+id+"/occurrences/"+day(1), `{"status":"completed"}`).expect(t, 400) // available now, but still in the future
	c.patch("/jobs/"+id+"/occurrences/"+day(1), `{"status":"confirmed"}`).expect(t, 200)
}

func TestE2EExceptFrequency(t *testing.T) {
	c, _, _ := newClient(t)
	today := civil.Today()
	day := func(n int) string { return civil.Format(civil.AddDays(today, n)) }

	b, _ := c.post("/jobs", fmt.Sprintf(`{"date":%q}`, day(1))).expect(t, 200).obj()["id"].(string)
	body := fmt.Sprintf(`{"date":%q,"recurrence":{"frequency":"daily","exceptType":"frequency","exceptJobId":%q}}`, day(0), b)
	a := c.post("/jobs", body).expect(t, 200)
	aid, _ := a.obj()["id"].(string)
	if a.obj()["recurrence"].(map[string]any)["exceptJobId"] != b {
		t.Fatalf("exceptJobId not echoed: %s", a.raw)
	}
	got := fmt.Sprint(c.get("/jobs/"+aid+"/occurrences?limit=4").expect(t, 200).list())
	if want := fmt.Sprint([]any{day(0), day(2), day(3), day(4)}); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	c.patch("/jobs/"+aid+"/occurrences/"+day(1), `{"status":"confirmed"}`).expect(t, 404) // excluded by B

	unknown := fmt.Sprintf(`{"date":%q,"recurrence":{"frequency":"daily","exceptType":"frequency","exceptJobId":%q}}`, day(0), unknownID)
	c.post("/jobs", unknown).expect(t, 404)
	c.post("/jobs", strings.Replace(unknown, unknownID, "nope", 1)).expect(t, 400)
}

func TestE2EValidation(t *testing.T) {
	c, _, _ := newClient(t)
	post := func(body string, want int) {
		t.Helper()
		r := c.post("/jobs", body)
		if r.code != want {
			t.Errorf("%s\n -> %d, want %d (%s)", body, r.code, want, r.raw)
		}
		r.envelope(t)
	}
	d := `"date":"2026-10-02"`
	rec := func(r string) string { return `{` + d + `,"recurrence":` + r + `}` }

	post(`{}`, 400)
	post(`{"date":"2026-02-30"}`, 400)
	post(`{"date":"2026-10-02","status":"rescheduled"}`, 400)
	post(`{"date":"2026-10-02","status":"bogus"}`, 400)
	post(`{"date":"2026-10-02","status":"in_progress"}`, 400) // no longer a status
	post(`{"date":"2026-10-02","status":"confirmed"}`, 200)
	post(rec(`{"frequency":"hourly"}`), 400)
	post(rec(`{"frequency":"weekly"}`), 400)
	post(rec(`{"frequency":"weekly","weeklyPeriod":"every","weeklyDaysOfWeek":[]}`), 400)
	post(rec(`{"frequency":"weekly","weeklyPeriod":"every","weeklyDaysOfWeek":[1,1]}`), 400) // F2
	post(rec(`{"frequency":"weekly","weeklyPeriod":"every","weeklyDaysOfWeek":[7]}`), 400)
	post(rec(`{"frequency":"weekly","weeklyPeriod":"every","weeklyDaysOfWeek":[5,1,3]}`), 200)
	post(rec(`{"frequency":"monthly"}`), 400)
	post(rec(`{"frequency":"monthly","monthlyRepeatBy":"day_of_week"}`), 200)
	post(rec(`{"frequency":"yearly"}`), 400)
	post(rec(`{"frequency":"yearly","yearlyRepeatBy":"day_of_year","interval":999}`), 200)
	post(rec(`{"frequency":"daily","interval":1000}`), 400)
	post(rec(`{"frequency":"daily","interval":-1}`), 400)
	post(rec(`{"frequency":"daily","endsType":"after"}`), 400)
	post(rec(`{"frequency":"daily","endsType":"after","endsAfterCount":3}`), 200)
	post(rec(`{"frequency":"daily","endsType":"on_date","endsOnDate":"2026-10-01"}`), 400)
	post(rec(`{"frequency":"daily","endsType":"on_date","endsOnDate":"2026-10-02"}`), 200)
	post(rec(`{"frequency":"daily","exceptType":"month"}`), 400)
	post(rec(`{"frequency":"daily","exceptType":"month","exceptMonths":[13]}`), 400)
	post(rec(`{"frequency":"daily","exceptType":"condition","exceptConditionEvery":"month","exceptConditionDayOfWeek":5}`), 400)
	post(rec(`{"frequency":"daily","exceptType":"condition","exceptConditionEvery":"month","exceptConditionPeriod":"last","exceptConditionDayOfWeek":0}`), 200)
	post(rec(`{"frequency":"daily","weeklyDaysOfWeek":"mon"}`), 400) // wrong JSON type

	id, _ := c.post("/jobs", `{"date":"2026-10-02","recurrence":{"frequency":"daily"}}`).expect(t, 200).obj()["id"].(string)
	for path, want := range map[string]int{
		"/jobs/" + id + "/occurrences?limit=1":                            200,
		"/jobs/" + id + "/occurrences?limit=1000":                         200,
		"/jobs/" + id + "/occurrences?limit=1001":                         400,
		"/jobs/" + id + "/occurrences?limit=-1":                           400,
		"/jobs/" + id + "/occurrences?limit=abc":                          400,
		"/jobs/" + id + "/occurrences?from=nope":                          400,
		"/jobs/" + id + "/occurrences?from=2026-10-20&to=2026-10-10":      400,
		"/jobs/" + id + "/occurrences?from=2026-10-10&to=2026-10-10":      200,
		"/jobs/" + id + "/schedule?limit=1001":                            400,
		"/jobs/" + id + "/schedule?from=2026-10-20&to=2026-10-10":         400, // F3
		"/jobs/" + id + "/schedule?from=2026-10-10&to=2026-10-12&limit=2": 200,
		"/jobs/" + id + "/nope":                                           404,
	} {
		if r := c.get(path); r.code != want {
			t.Errorf("GET %s -> %d, want %d (%s)", path, r.code, want, r.raw)
		}
	}
	if r := c.do("DELETE", "/jobs/"+id, ""); r.code != 404 && r.code != 405 {
		t.Errorf("DELETE -> %d", r.code)
	}
}

func TestE2ECalendarEdge(t *testing.T) {
	c, _, _ := newClient(t) // F1: a series ends at 9999-12-31
	id, _ := c.post("/jobs", `{"date":"9999-12-30","recurrence":{"frequency":"daily"}}`).expect(t, 200).obj()["id"].(string)
	got := fmt.Sprint(c.get("/jobs/"+id+"/occurrences?limit=10").expect(t, 200).list())
	if got != "[9999-12-30 9999-12-31]" {
		t.Fatalf("got %s", got)
	}
}

// FINDING F4: the schedule walk gives up at 2099-12-31, so a job dated after
// that gets an empty schedule even though its occurrences and PATCH work.
func TestE2EScheduleForJobsBeyondTheHorizon(t *testing.T) {
	c, _, _ := newClient(t)
	for name, body := range map[string]string{
		"one-off in 2100":       `{"date":"2100-03-01"}`,
		"endless daily in 2100": `{"date":"2100-03-01","recurrence":{"frequency":"daily"}}`,
		"endless daily in 9999": `{"date":"9999-12-30","recurrence":{"frequency":"daily"}}`,
		"finite series in 9999": `{"date":"9999-12-30","recurrence":{"frequency":"daily","endsType":"after","endsAfterCount":5}}`,
	} {
		id, _ := c.post("/jobs", body).expect(t, 200).obj()["id"].(string)
		if occ := c.get("/jobs/"+id+"/occurrences?limit=2").expect(t, 200).list(); len(occ) == 0 {
			t.Fatalf("%s: no occurrences", name)
		}
		if items := c.get("/jobs/"+id+"/schedule").expect(t, 200).list(); len(items) == 0 {
			t.Errorf("%s: the schedule is empty although the job has occurrences", name)
		}
	}
}

func TestE2ETenantIsolation(t *testing.T) {
	db := helpers.Connect(t)
	schemaA, _ := helpers.NewSchema(t, db)
	schemaB, _ := helpers.NewSchema(t, db)
	h := app.NewServer(db, schemaA)
	inA := client{t: t, h: h}
	inB := client{t: t, h: h, tenant: schemaB}
	inA.refs, inB.refs = seedRefs(t, inA), seedRefs(t, inB)

	id, _ := inB.post("/jobs", `{"date":"2026-10-02"}`).expect(t, 200).obj()["id"].(string)
	inB.get("/jobs/"+id+"/schedule").expect(t, 200)
	inA.get("/jobs/"+id+"/schedule").expect(t, 404)                                           // default tenant A cannot see B's job
	client{t: t, h: h, tenant: schemaA}.get("/jobs/"+id+"/schedule").expect(t, 404)           // explicit header, same result
	inA.patch("/jobs/"+id+"/occurrences/2026-10-02", `{"status":"confirmed"}`).expect(t, 404) // nor change it
	inB.patch("/jobs/"+id+"/occurrences/2026-10-02", `{"status":"confirmed"}`).expect(t, 200)

	idA, _ := inA.post("/jobs", `{"date":"2026-10-02"}`).expect(t, 200).obj()["id"].(string)
	inB.get("/jobs/"+idA+"/schedule").expect(t, 404)

	// An except-frequency reference cannot cross tenants either.
	cross := fmt.Sprintf(`{"date":"2026-10-02","recurrence":{"frequency":"daily","exceptType":"frequency","exceptJobId":%q}}`, id)
	inA.post("/jobs", cross).expect(t, 404)

	t.Run("a malformed tenant header is a 400", func(t *testing.T) {
		for _, bad := range []string{"Bad;Schema", "UPPER", "a b", `x"y`} {
			r := client{t: t, h: h, tenant: bad}.get("/jobs/" + id + "/schedule")
			if r.code != 400 {
				t.Errorf("%q -> %d (%s)", bad, r.code, r.raw)
			}
		}
	})

	t.Run("a well-formed tenant that does not exist is a 500 that leaks nothing", func(t *testing.T) {
		r := client{t: t, h: h, tenant: "t_doesnotexist"}.get("/jobs/" + id + "/schedule")
		lower := strings.ToLower(r.raw)
		if r.code != 500 || !strings.Contains(r.raw, "internal server error") || strings.Contains(lower, "relation") || strings.Contains(lower, "jobs") || strings.Contains(lower, "sqlstate") {
			t.Fatalf("status %d body %s", r.code, r.raw)
		}
	})
}

func TestE2EConcurrentPatch(t *testing.T) {
	c, _, _ := newClient(t)
	date := civil.Format(civil.Today())
	for round := 0; round < 5; round++ {
		id, _ := c.post("/jobs", fmt.Sprintf(`{"date":%q}`, date)).expect(t, 200).obj()["id"].(string)
		codes := make([]int, 8)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range codes {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				codes[i] = c.patch("/jobs/"+id+"/occurrences/"+date, `{"status":"completed"}`).code
			}(i)
		}
		close(start)
		wg.Wait()
		ok, conflict := 0, 0
		for _, code := range codes {
			switch code {
			case 200:
				ok++
			case 409:
				conflict++
			}
		}
		if ok != 1 || conflict != 7 {
			t.Fatalf("round %d: status codes %v, want exactly one 200 and seven 409", round, codes)
		}
	}
}

func TestE2EInternalErrorsDoNotLeak(t *testing.T) {
	admin := helpers.Connect(t) // owns the schema and drops it at the end
	schema, _ := helpers.NewSchema(t, admin)
	db := helpers.Connect(t) // the server's own connection, which this test breaks
	h := app.NewServer(db, schema)
	good := client{t: t, h: h}
	good.refs = seedRefs(t, good)
	good.post("/jobs", `{"date":"2026-10-02"}`).expect(t, 200)

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.Close() // every later query fails with a driver error

	for name, r := range map[string]resp{
		"create":   client{t: t, h: h}.post("/jobs", fullJobBody),
		"schedule": client{t: t, h: h}.get("/jobs/" + unknownID + "/schedule"),
		"patch":    client{t: t, h: h}.patch("/jobs/"+unknownID+"/occurrences/2026-10-02", `{"status":"confirmed"}`),
	} {
		lower := strings.ToLower(r.raw)
		if r.code != 500 || !strings.Contains(r.raw, "internal server error") || strings.Contains(lower, "closed") || strings.Contains(lower, "sql") || strings.Contains(lower, "pgx") {
			t.Errorf("%s: status %d body %s", name, r.code, r.raw)
		}
		r.envelope(t)
	}
}

// fill adds the client's references (and a start time and length) to the
// request bodies that need them and lack them. Malformed bodies are left as
// they are, so validation tests still see what they sent.
func (c client) fill(method, path, body string) string {
	if c.refs == nil || method != "POST" || body == "" {
		return body
	}
	var add map[string]any
	switch {
	case path == "/jobs":
		add = map[string]any{"customerId": c.refs.customer, "locationId": c.refs.location, "serviceTypeId": c.refs.serviceType, "startTime": "09:00", "lengthMinutes": 60}
	case path == "/estimates":
		add = map[string]any{"customerId": c.refs.customer, "locationId": c.refs.location, "serviceTypeId": c.refs.serviceType}
	case strings.HasPrefix(path, "/estimates/") && strings.HasSuffix(path, "/approve"):
		add = map[string]any{"startTime": "09:00", "lengthMinutes": 60}
	default:
		return body
	}
	var m map[string]any
	if json.Unmarshal([]byte(body), &m) != nil || m == nil {
		return body
	}
	for k, v := range add {
		if _, has := m[k]; !has {
			m[k] = v
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return string(out)
}

// seedRefs creates a customer ("Ada Lovelace"), a location and a service type
// through the API and returns their ids.
func seedRefs(t *testing.T, c client) *refIDs {
	t.Helper()
	id := func(r resp) string {
		t.Helper()
		s, _ := r.expect(t, 200).obj()["id"].(string)
		return s
	}
	customer := id(c.post("/customers", `{"name":"Ada Lovelace","email":"ada@example.com"}`))
	location := id(c.post("/customers/"+customer+"/locations", `{"addressLine1":"1 Main Street","city":"Springfield"}`))
	serviceType := id(c.post("/service-types", `{"name":"Window cleaning"}`))
	return &refIDs{customer: customer, location: location, serviceType: serviceType}
}

// fullJobBody is a complete job request with made-up reference ids, for tests
// that never get as far as looking them up.
const fullJobBody = `{"customerId":"11111111-1111-1111-1111-111111111111","locationId":"22222222-2222-2222-2222-222222222222","serviceTypeId":"33333333-3333-3333-3333-333333333333","date":"2026-10-02","startTime":"09:00","lengthMinutes":60}`
