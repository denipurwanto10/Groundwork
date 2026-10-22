//go:build integration

// Integration tests for the operational report and its CSV export.
//
// Prerequisite: PostgreSQL reachable with the repo .env (DB_*), database
// created, and the migrations applied.
//
// Run: go test -tags integration ./internal/handler/ -v -run Report
package handler

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"entity-management/internal/auth"
	"entity-management/internal/config"
	"entity-management/internal/db"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

// reportFixture is the report handler, the database and a router with the two
// report endpoints behind the auth middleware.
type reportFixture struct {
	handler *ReportHandler
	pool    *db.Pool
	router  *gin.Engine
	service *auth.Service
}

// reportFixtureFor builds the fixture and cleans the leftovers of an earlier
// run.
func reportFixtureFor(t *testing.T) *reportFixture {
	t.Helper()

	if err := godotenv.Load("../../.env"); err != nil {
		t.Fatalf("load .env: %v (run from repo root with DB_PASSWORD set)", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	pool, err := db.NewPool(&cfg.Database)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reportCleanup(ctx, pool)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		reportCleanup(ctx, pool)
	})

	gin.SetMode(gin.TestMode)
	service := auth.NewServiceWithRefresh(pool, "groundwork-report-test-secret", time.Hour, time.Hour)
	mw := auth.NewMiddleware(service)

	router := gin.New()
	protected := router.Group("/api", mw.Require())
	handler := NewReportHandler(pool)
	protected.GET("/reports", handler.Get)
	protected.GET("/reports/export.csv", handler.ExportCSV)

	return &reportFixture{handler: handler, pool: pool, router: router, service: service}
}

// reportCleanup removes everything the tests wrote.
func reportCleanup(ctx context.Context, pool *db.Pool) {
	pool.Exec(ctx, `DELETE FROM tasks WHERE title LIKE 'rptest-%'`)
	pool.Exec(ctx, `DELETE FROM geofence_events WHERE vehicle_id IN (SELECT id FROM vehicles WHERE code LIKE 'rptest-%')`)
	pool.Exec(ctx, `DELETE FROM vehicle_positions WHERE vehicle_id IN (SELECT id FROM vehicles WHERE code LIKE 'rptest-%')`)
	pool.Exec(ctx, `DELETE FROM vehicle_routes WHERE vehicle_id IN (SELECT id FROM vehicles WHERE code LIKE 'rptest-%')`)
	pool.Exec(ctx, `DELETE FROM vehicles WHERE code LIKE 'rptest-%'`)
	pool.Exec(ctx, `DELETE FROM zones WHERE name LIKE 'rptest-%'`)
	pool.Exec(ctx, `DELETE FROM users WHERE username LIKE 'rptest-%'`)
}

// seedReportUser creates an account and returns its id.
func seedReportUser(t *testing.T, pool *db.Pool, username, role string) int64 {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var id int64
	err := pool.QueryRow(ctx, `
        INSERT INTO users (username, email, password_hash, role, name)
        VALUES ($1, $2, 'rptest', $3, $1)
        RETURNING id`, username, username+"@rptest.local", role).Scan(&id)
	if err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}

	return id
}

// seedReportVehicle creates a unit and returns its id.
func seedReportVehicle(t *testing.T, pool *db.Pool, code string, driverID *int64) int64 {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var id int64
	err := pool.QueryRow(ctx, `
        INSERT INTO vehicles (code, name, kind, status, driver_id)
        VALUES ($1, $1, 'truck', 'moving', $2)
        RETURNING id`, code, driverID).Scan(&id)
	if err != nil {
		t.Fatalf("insert vehicle %s: %v", code, err)
	}

	return id
}

// seedReportPositions writes the positions of a unit.
func seedReportPositions(t *testing.T, pool *db.Pool, vehicleID int64, points []struct {
	Lat, Lng float64
	At       time.Time
}) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, p := range points {
		if _, err := pool.Exec(ctx, `
            INSERT INTO vehicle_positions (vehicle_id, lat, lng, speed_kmh, recorded_at)
            VALUES ($1, $2, $3, 10, $4)`, vehicleID, p.Lat, p.Lng, p.At); err != nil {
			t.Fatalf("insert position: %v", err)
		}
	}
}

// seedReportRoute writes one route of a unit.
func seedReportRoute(t *testing.T, pool *db.Pool, vehicleID int64, status string, createdAt, endedAt time.Time) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var ended any
	if !endedAt.IsZero() {
		ended = endedAt
	}
	if _, err := pool.Exec(ctx, `
        INSERT INTO vehicle_routes
            (vehicle_id, origin_name, dest_name, origin_lat, origin_lng, dest_lat, dest_lng,
             geom, distance_m, duration_s, source, status, created_at, ended_at)
        VALUES ($1, 'A', 'B', 0, 0, 0, 0.01,
                ST_GeomFromText('LINESTRING(0 0, 0.01 0)', 4326), 1113, 60, 'direct', $2, $3, $4)`,
		vehicleID, status, createdAt, ended); err != nil {
		t.Fatalf("insert route: %v", err)
	}
}

// seedReportZone writes a work area and returns its id.
func seedReportZone(t *testing.T, pool *db.Pool, name string) int64 {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var id int64
	err := pool.QueryRow(ctx, `
        INSERT INTO zones (name, category, color, geom)
        VALUES ($1, 'other', '#d0171e', ST_GeomFromText('MULTIPOLYGON(((0 0, 0 1, 1 1, 1 0, 0 0)))', 4326))
        RETURNING id`, name).Scan(&id)
	if err != nil {
		t.Fatalf("insert zone %s: %v", name, err)
	}

	return id
}

// seedReportEvent writes one crossing of a zone border.
func seedReportEvent(t *testing.T, pool *db.Pool, vehicleID, zoneID int64, eventType string, at time.Time) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := pool.Exec(ctx, `
        INSERT INTO geofence_events (vehicle_id, zone_id, event_type, created_at)
        VALUES ($1, $2, $3, $4)`, vehicleID, zoneID, eventType, at); err != nil {
		t.Fatalf("insert geofence event: %v", err)
	}
}

// seedReportTask writes one work order.
func seedReportTask(t *testing.T, pool *db.Pool, title, status string, assigned *int64, at time.Time) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := pool.Exec(ctx, `
        INSERT INTO tasks (title, status, assigned_user_id, created_at)
        VALUES ($1, $2, $3, $4)`, title, status, assigned, at); err != nil {
		t.Fatalf("insert task: %v", err)
	}
}

// reportToken signs a token for an account.
func reportToken(t *testing.T, f *reportFixture, id int64, username, role string) string {
	t.Helper()

	session, err := f.service.NewSession(auth.User{ID: id, Username: username, Role: role})
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return session.AccessToken
}

// reportRequest calls an endpoint with a token.
func reportRequest(t *testing.T, f *reportFixture, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)

	return rec
}

// reportPeriodParams is the window the tests ask for: an hour around now.
// The timestamps are escaped: a "+" in a query string is a space otherwise.
func reportPeriodParams(now time.Time) string {
	from := url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339))
	to := url.QueryEscape(now.Add(time.Hour).Format(time.RFC3339))
	return "?from=" + from + "&to=" + to
}

// readReport decodes the answer of GET /api/reports.
func readReport(t *testing.T, rec *httptest.ResponseRecorder) Report {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("the report answered %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data Report `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode report: %v (%s)", err, rec.Body.String())
	}
	return body.Data
}

// findReportVehicle returns the row of one unit.
func findReportVehicle(report Report, id int64) (ReportVehicle, bool) {
	for _, v := range report.ByVehicle {
		if v.VehicleID == id {
			return v, true
		}
	}
	return ReportVehicle{}, false
}

// The distance of the period is the sum of the steps between the reported
// positions: the same arithmetic the Overview tab does.
func TestReportCountsTheDistanceOfAVehicle(t *testing.T) {
	f := reportFixtureFor(t)
	now := time.Now().Truncate(time.Second)

	vehicle := seedReportVehicle(t, f.pool, "rptest-distance", nil)
	// Two fixes a tenth of a degree apart on the equator: 1113 metres,
	// ten minutes apart, so the step is a plausible drive and not a jump.
	seedReportPositions(t, f.pool, vehicle, []struct {
		Lat, Lng float64
		At       time.Time
	}{
		{0, 0, now.Add(-20 * time.Minute)},
		{0, 0.01, now.Add(-10 * time.Minute)},
	})

	token := reportToken(t, f, 1, "rptest-admin", auth.RoleAdmin)
	report := readReport(t, reportRequest(t, f, http.MethodGet, "/api/reports"+reportPeriodParams(now), token))

	row, ok := findReportVehicle(report, vehicle)
	if !ok {
		t.Fatalf("the unit is missing from the report: %+v", report.ByVehicle)
	}
	if row.DistanceKM < 1.09 || row.DistanceKM > 1.13 {
		t.Errorf("the distance is %v km, want about 1.11 km", row.DistanceKM)
	}

	// The total of the whole fleet also holds the kilometres of the units
	// that are really driving, so the total is asked for the one unit.
	only := readReport(t, reportRequest(t, f, http.MethodGet,
		"/api/reports"+reportPeriodParams(now)+"&vehicle_id="+itoa(vehicle), token))
	if only.Totals.DistanceKM < 1.09 || only.Totals.DistanceKM > 1.13 {
		t.Errorf("the total distance of the unit is %v km, want about 1.11 km", only.Totals.DistanceKM)
	}
	if len(only.ByVehicle) != 1 {
		t.Errorf("the filtered report holds %d units, want 1", len(only.ByVehicle))
	}
}

// A route that was finished counts as completed, a route that was cancelled
// does not.
func TestReportCountsTheRoutes(t *testing.T) {
	f := reportFixtureFor(t)
	now := time.Now().Truncate(time.Second)

	vehicle := seedReportVehicle(t, f.pool, "rptest-routes", nil)
	seedReportRoute(t, f.pool, vehicle, "completed", now.Add(-30*time.Minute), now.Add(-20*time.Minute))
	seedReportRoute(t, f.pool, vehicle, "cancelled", now.Add(-30*time.Minute), now.Add(-20*time.Minute))
	// A route that is still running counts as a route of the period, but it
	// is not a finished one.
	seedReportRoute(t, f.pool, vehicle, "active", now.Add(-10*time.Minute), time.Time{})

	token := reportToken(t, f, 1, "rptest-admin", auth.RoleAdmin)
	report := readReport(t, reportRequest(t, f, http.MethodGet, "/api/reports"+reportPeriodParams(now), token))

	row, ok := findReportVehicle(report, vehicle)
	if !ok {
		t.Fatalf("the unit is missing from the report: %+v", report.ByVehicle)
	}
	if row.RoutesCompleted != 1 {
		t.Errorf("routes completed = %d, want 1 (the cancelled and the running route do not count)", row.RoutesCompleted)
	}
	if row.RoutesTotal != 3 {
		t.Errorf("routes total = %d, want 3", row.RoutesTotal)
	}
	if report.Totals.RoutesCompleted != 1 {
		t.Errorf("the total of the finished routes is %d, want 1", report.Totals.RoutesCompleted)
	}
}

// The crossings of a zone border are counted per unit and per zone.
func TestReportCountsTheGeofenceEvents(t *testing.T) {
	f := reportFixtureFor(t)
	now := time.Now().Truncate(time.Second)

	vehicle := seedReportVehicle(t, f.pool, "rptest-events", nil)
	zone := seedReportZone(t, f.pool, "rptest-zone")
	seedReportEvent(t, f.pool, vehicle, zone, "ENTER", now.Add(-20*time.Minute))
	seedReportEvent(t, f.pool, vehicle, zone, "EXIT", now.Add(-10*time.Minute))

	token := reportToken(t, f, 1, "rptest-admin", auth.RoleAdmin)
	report := readReport(t, reportRequest(t, f, http.MethodGet, "/api/reports"+reportPeriodParams(now), token))

	row, ok := findReportVehicle(report, vehicle)
	if !ok {
		t.Fatalf("the unit is missing from the report: %+v", report.ByVehicle)
	}
	if row.GeofenceEnter != 1 || row.GeofenceExit != 1 {
		t.Errorf("the crossings are %d in and %d out, want 1 and 1", row.GeofenceEnter, row.GeofenceExit)
	}
	if report.Totals.GeofenceEnter != 1 || report.Totals.GeofenceExit != 1 {
		t.Errorf("the totals are %d in and %d out, want 1 and 1",
			report.Totals.GeofenceEnter, report.Totals.GeofenceExit)
	}

	if len(report.ByZone) == 0 {
		t.Fatal("the zone is missing from the report")
	}
	z := report.ByZone[0]
	if z.ZoneID != zone {
		t.Errorf("the zone is %d, want %d", z.ZoneID, zone)
	}
	if z.Enter != 1 || z.Exit != 1 || z.Total != 2 {
		t.Errorf("the zone counts are %d in, %d out, %d total, want 1, 1, 2", z.Enter, z.Exit, z.Total)
	}
	if z.UniqueVehicles != 1 {
		t.Errorf("the zone counts %d units, want 1", z.UniqueVehicles)
	}
}

// The work orders of the period are counted, and the empty ones are zero.
func TestReportCountsTheTasks(t *testing.T) {
	f := reportFixtureFor(t)
	now := time.Now().Truncate(time.Second)

	seedReportTask(t, f.pool, "rptest-task-done", "completed", nil, now.Add(-30*time.Minute))
	seedReportTask(t, f.pool, "rptest-task-open", "pending", nil, now.Add(-30*time.Minute))

	token := reportToken(t, f, 1, "rptest-admin", auth.RoleAdmin)
	report := readReport(t, reportRequest(t, f, http.MethodGet, "/api/reports"+reportPeriodParams(now), token))

	if report.Totals.TasksTotal != 2 {
		t.Errorf("the work orders of the period are %d, want 2", report.Totals.TasksTotal)
	}
	if report.Totals.TasksCompleted != 1 {
		t.Errorf("the finished work orders are %d, want 1", report.Totals.TasksCompleted)
	}
}

// A unit without a single step still gets a row: the report shows what
// everybody did, not only what somebody did.
func TestReportShowsTheQuietUnits(t *testing.T) {
	f := reportFixtureFor(t)
	now := time.Now().Truncate(time.Second)

	quiet := seedReportVehicle(t, f.pool, "rptest-quiet", nil)

	token := reportToken(t, f, 1, "rptest-admin", auth.RoleAdmin)
	report := readReport(t, reportRequest(t, f, http.MethodGet, "/api/reports"+reportPeriodParams(now), token))

	row, ok := findReportVehicle(report, quiet)
	if !ok {
		t.Fatalf("the quiet unit is missing from the report: %+v", report.ByVehicle)
	}
	if row.DistanceKM != 0 || row.RoutesCompleted != 0 || row.RoutesTotal != 0 {
		t.Errorf("the quiet unit has numbers: %+v", row)
	}
	if row.GeofenceEnter != 0 || row.GeofenceExit != 0 {
		t.Errorf("the quiet unit has crossings: %+v", row)
	}
}

// A period without a single event is an empty report, not an error.
func TestReportOfAnEmptyPeriod(t *testing.T) {
	f := reportFixtureFor(t)

	token := reportToken(t, f, 1, "rptest-admin", auth.RoleAdmin)
	// A day in 2001, long before the workspace held a single position.
	path := "?from=2001-01-01&to=2001-01-02"
	report := readReport(t, reportRequest(t, f, http.MethodGet, "/api/reports"+path, token))

	if report.Totals.DistanceKM != 0 || report.Totals.RoutesCompleted != 0 || report.Totals.TasksTotal != 0 {
		t.Errorf("the totals of an empty period are not zero: %+v", report.Totals)
	}
	if len(report.ByZone) != 0 {
		t.Errorf("an empty period has %d zones, want none", len(report.ByZone))
	}
	if report.Period.From != "2001-01-01" || report.Period.To != "2001-01-02" {
		t.Errorf("the period is %s..%s, want 2001-01-01..2001-01-02", report.Period.From, report.Period.To)
	}
}

// The CSV carries the same rows as the report, in the same order.
func TestReportExportCSV(t *testing.T) {
	f := reportFixtureFor(t)
	now := time.Now().Truncate(time.Second)

	vehicle := seedReportVehicle(t, f.pool, "rptest-csv", nil)
	seedReportPositions(t, f.pool, vehicle, []struct {
		Lat, Lng float64
		At       time.Time
	}{
		{0, 0, now.Add(-20 * time.Minute)},
		{0, 0.01, now.Add(-10 * time.Minute)},
	})

	token := reportToken(t, f, 1, "rptest-admin", auth.RoleAdmin)
	// The unit filter narrows the report down to one row, so the file has a
	// known shape.
	path := "/api/reports/export.csv" + reportPeriodParams(now) + "&vehicle_id=" + itoa(vehicle)
	rec := reportRequest(t, f, http.MethodGet, path, token)

	if rec.Code != http.StatusOK {
		t.Fatalf("the export answered %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Errorf("the content type is %q, want \"text/csv; charset=utf-8\"", got)
	}
	from := now.Add(-time.Hour).Format("2006-01-02")
	to := now.Add(time.Hour).Format("2006-01-02")
	want := "attachment; filename=\"groundwork-report-" + from + "-" + to + ".csv\""
	if got := rec.Header().Get("Content-Disposition"); got != want {
		t.Errorf("the content disposition is %q, want %q", got, want)
	}

	rows, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("the file holds %d rows, want a header and one row", len(rows))
	}
	wantHeader := []string{
		"vehicle_id", "vehicle_code", "vehicle_name", "driver_name",
		"distance_km", "routes_completed", "routes_total",
		"geofence_enter", "geofence_exit",
	}
	if strings.Join(rows[0], ",") != strings.Join(wantHeader, ",") {
		t.Errorf("the header is %v, want %v", rows[0], wantHeader)
	}
	if rows[1][1] != "rptest-csv" {
		t.Errorf("the row is about %q, want \"rptest-csv\"", rows[1][1])
	}
	if rows[1][4] != "1.11" {
		t.Errorf("the distance in the file is %q, want \"1.11\"", rows[1][4])
	}
}

// A driver reads the report of their own unit only: the report of another
// truck is refused.
func TestReportDriverScope(t *testing.T) {
	f := reportFixtureFor(t)
	now := time.Now().Truncate(time.Second)

	driver := seedReportUser(t, f.pool, "rptest-driver", auth.RoleDriver)
	mine := seedReportVehicle(t, f.pool, "rptest-mine", &driver)
	other := seedReportVehicle(t, f.pool, "rptest-other", nil)

	seedReportPositions(t, f.pool, mine, []struct {
		Lat, Lng float64
		At       time.Time
	}{
		{0, 0, now.Add(-20 * time.Minute)},
		{0, 0.01, now.Add(-10 * time.Minute)},
	})
	seedReportPositions(t, f.pool, other, []struct {
		Lat, Lng float64
		At       time.Time
	}{
		{1, 1, now.Add(-20 * time.Minute)},
		{1, 1.01, now.Add(-10 * time.Minute)},
	})

	token := reportToken(t, f, driver, "rptest-driver", auth.RoleDriver)
	report := readReport(t, reportRequest(t, f, http.MethodGet, "/api/reports"+reportPeriodParams(now), token))

	if len(report.ByVehicle) != 1 {
		t.Fatalf("the driver sees %d units, want only their own", len(report.ByVehicle))
	}
	if report.ByVehicle[0].VehicleID != mine {
		t.Errorf("the driver sees the unit %d, want %d", report.ByVehicle[0].VehicleID, mine)
	}

	// The unit of somebody else is refused, the same way the history refuses
	// it.
	rec := reportRequest(t, f, http.MethodGet,
		"/api/reports"+reportPeriodParams(now)+"&vehicle_id="+itoa(other), token)
	if rec.Code != http.StatusForbidden {
		t.Errorf("the report of another unit answered %d, want 403", rec.Code)
	}
}

// A period that cannot be read is refused with the error format of the API.
func TestReportRejectsABadPeriod(t *testing.T) {
	f := reportFixtureFor(t)

	token := reportToken(t, f, 1, "rptest-admin", auth.RoleAdmin)
	rec := reportRequest(t, f, http.MethodGet, "/api/reports?from=nonsense", token)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a broken period answered %d, want 400", rec.Code)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Error.Code != "validation_failed" {
		t.Errorf("the error code is %q, want \"validation_failed\"", body.Error.Code)
	}
	if body.Error.Message == "" {
		t.Error("the error has no message")
	}
}

// itoa writes a number in base 10.
func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
