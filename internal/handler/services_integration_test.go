//go:build integration

// Integration tests for the service reminders: the odometer of a unit, the
// schedule that is counted from it, the history of the services and the
// sweep that warns the operators.
//
// Prerequisite: PostgreSQL reachable with the repo .env (DB_*), database
// created, and the migrations applied.
//
// Run: go test -tags integration ./internal/handler/ -v -run Service
//
// Nothing here talks to a real push service: every delivery goes through a
// fake sender, so the test never leaves the machine.
package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"entity-management/internal/auth"
	"entity-management/internal/config"
	"entity-management/internal/db"
	"entity-management/internal/push"
	"entity-management/internal/vehicle"
	"entity-management/internal/ws"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

// The schedule of the tests: an interval of 5000 km and a warning distance
// of 500 km, the same as the defaults of the server.
const (
	serviceTestIntervalKM = 5000.0
	serviceTestWarnKM     = 500.0
)

// serviceFixture is the vehicle handler with the service schedule turned on,
// the database, a router with the endpoints of the feature and a fake push
// sender.
type serviceFixture struct {
	handler *VehicleHandler
	pool    *db.Pool
	router  *gin.Engine
	service *auth.Service
	sender  *fakePushSender
}

// serviceFixtureFor builds the fixture and cleans the leftovers of an earlier
// run.
func serviceFixtureFor(t *testing.T) *serviceFixture {
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
	serviceCleanup(ctx, pool)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		serviceCleanup(ctx, pool)
	})

	gin.SetMode(gin.TestMode)
	service := auth.NewServiceWithRefresh(pool, "groundwork-service-test-secret", time.Hour, time.Hour)
	mw := auth.NewMiddleware(service)

	sender := &fakePushSender{}
	pushService := push.NewService(pool, push.Config{
		PublicKey:  "test-public-key",
		PrivateKey: "test-private-key",
		Subject:    "mailto:test@localhost",
	}, sender)

	vehicleHandler := NewVehicleHandler(pool, ws.NewHub(), pushService)
	vehicleHandler.SetServiceSchedule(serviceTestIntervalKM, serviceTestWarnKM)

	router := gin.New()
	protected := router.Group("/api", mw.Require())
	adminOnly := mw.RequireRole(auth.RoleAdmin)

	protected.GET("/vehicles", vehicleHandler.List)
	protected.PUT("/vehicles/:id", adminOnly, vehicleHandler.Update)
	protected.POST("/vehicles/:id/position", vehicleHandler.UpdatePosition)
	protected.GET("/vehicles/:id/services", vehicleHandler.ListServices)
	protected.POST("/vehicles/:id/services", adminOnly, vehicleHandler.RecordService)
	protected.POST("/push/subscribe", NewPushHandler(pushService).Subscribe)

	return &serviceFixture{
		handler: vehicleHandler,
		pool:    pool,
		router:  router,
		service: service,
		sender:  sender,
	}
}

// serviceCleanup removes everything the tests wrote.
func serviceCleanup(ctx context.Context, pool *db.Pool) {
	pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE endpoint LIKE 'https://push.test/%'`)
	pool.Exec(ctx, `DELETE FROM vehicle_services WHERE vehicle_id IN (SELECT id FROM vehicles WHERE code LIKE 'svctest-%')`)
	pool.Exec(ctx, `DELETE FROM vehicle_positions WHERE vehicle_id IN (SELECT id FROM vehicles WHERE code LIKE 'svctest-%')`)
	pool.Exec(ctx, `DELETE FROM vehicles WHERE code LIKE 'svctest-%'`)
	pool.Exec(ctx, `DELETE FROM users WHERE username LIKE 'svctest-%'`)
}

// serviceSeedUser creates an account and returns its id.
func serviceSeedUser(t *testing.T, pool *db.Pool, username, role string) int64 {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var id int64
	err := pool.QueryRow(ctx, `
        INSERT INTO users (username, email, password_hash, role, name)
        VALUES ($1, $2, 'svctest', $3, $1)
        RETURNING id`, username, username+"@svctest.local", role).Scan(&id)
	if err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}

	return id
}

// serviceSeedVehicle creates a unit and returns its id.
func serviceSeedVehicle(t *testing.T, pool *db.Pool, code string, driverID *int64) int64 {
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

// serviceToken signs a token for an account.
func serviceToken(t *testing.T, f *serviceFixture, id int64, username, role string) string {
	t.Helper()

	session, err := f.service.NewSession(auth.User{ID: id, Username: username, Role: role})
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return session.AccessToken
}

// serviceRequest calls an endpoint with a token and a body.
func serviceRequest(t *testing.T, f *serviceFixture, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		payload = b
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)

	return rec
}

// serviceOdometer reads the odometer of a unit, in metres.
func serviceOdometer(t *testing.T, pool *db.Pool, id int64) float64 {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var meters float64
	if err := pool.QueryRow(ctx, `SELECT total_distance_m FROM vehicles WHERE id = $1`, id).Scan(&meters); err != nil {
		t.Fatalf("read the odometer: %v", err)
	}

	return meters
}

// serviceNotifiedLevel reads the warning that was sent last for a unit.
func serviceNotifiedLevel(t *testing.T, pool *db.Pool, id int64) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var level string
	if err := pool.QueryRow(ctx, `SELECT service_notified_level FROM vehicles WHERE id = $1`, id).Scan(&level); err != nil {
		t.Fatalf("read the warning level: %v", err)
	}

	return level
}

// serviceShiftLastSeen moves the clock of the last report of a unit back, so
// the step of the next report is measured over a realistic duration.
//
// The two reports of a test arrive within milliseconds of each other, and a
// step of a kilometre in a millisecond is a speed of thousands of kilometres
// per hour — the guard of the odometer drops it, and rightly so.
func serviceShiftLastSeen(t *testing.T, pool *db.Pool, id int64, seconds int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := pool.Exec(ctx,
		`UPDATE vehicles SET last_seen_at = now() - make_interval(secs => $2) WHERE id = $1`,
		id, seconds); err != nil {
		t.Fatalf("shift the last report: %v", err)
	}
}

// serviceReport lets a unit report a position through the API.
func serviceReport(t *testing.T, f *serviceFixture, token string, id int64, lat, lng float64) {
	t.Helper()

	rec := serviceRequest(t, f, http.MethodPost,
		"/api/vehicles/"+strconv.FormatInt(id, 10)+"/position", token,
		map[string]any{"lat": lat, "lng": lng, "speed_kmh": 30, "status": "moving"})
	if rec.Code != http.StatusOK {
		t.Fatalf("the position report answered %d: %s", rec.Code, rec.Body.String())
	}
}

// serviceFleet reads the fleet and returns the unit with the given id.
func serviceFleetUnit(t *testing.T, f *serviceFixture, token string, id int64) serviceVehicle {
	t.Helper()

	rec := serviceRequest(t, f, http.MethodGet, "/api/vehicles", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/vehicles answered %d: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Data struct {
			Vehicles []serviceVehicle `json:"vehicles"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the fleet: %v (%s)", err, rec.Body.String())
	}

	for _, v := range body.Data.Vehicles {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("the unit %d is not in the fleet", id)

	return serviceVehicle{}
}

// serviceVehicle is the part of a unit the tests read.
type serviceVehicle struct {
	ID              int64   `json:"id"`
	TotalDistanceKM float64 `json:"total_distance_km"`
	Service         struct {
		IntervalKM    float64 `json:"interval_km"`
		LastServiceAt *string `json:"last_service_at"`
		LastServiceKM float64 `json:"last_service_km"`
		NextDueKM     float64 `json:"next_due_km"`
		RemainingKM   float64 `json:"remaining_km"`
		Status        string  `json:"status"`
	} `json:"service"`
}

// The odometer of a unit grows with the steps it drives — and a jump that
// would need an impossible speed does not count at all.
func TestServiceOdometerCountsTheStepsAndDropsTheJumps(t *testing.T) {
	f := serviceFixtureFor(t)
	token := serviceToken(t, f, 1, "svctest-admin", auth.RoleAdmin)

	id := serviceSeedVehicle(t, f.pool, "svctest-odometer", nil)

	// The first report of a unit has no step before it: it adds nothing.
	serviceReport(t, f, token, id, 0, 0)
	if got := serviceOdometer(t, f.pool, id); got != 0 {
		t.Errorf("the odometer is %v m after the first report, want 0", got)
	}

	// A step of about 1.1 km, measured over a minute: 66 km/h.
	serviceShiftLastSeen(t, f.pool, id, 60)
	serviceReport(t, f, token, id, 0.01, 0)

	odometer := serviceOdometer(t, f.pool, id)
	if odometer < 1100 || odometer > 1130 {
		t.Errorf("the odometer is %v m after the step, want about 1113 m", odometer)
	}

	// A jump of 100 km in a minute: 6000 km/h. That is not a drive, so the
	// odometer does not grow — even though the report claims 30 km/h.
	serviceShiftLastSeen(t, f.pool, id, 60)
	serviceReport(t, f, token, id, 1.0, 0)

	if got := serviceOdometer(t, f.pool, id); got != odometer {
		t.Errorf("the odometer is %v m after the jump, want %v m (the jump does not count)", got, odometer)
	}
}

// The fleet carries the odometer of every unit and the schedule that is
// counted from it.
func TestServiceFleetCarriesTheOdometerAndTheSchedule(t *testing.T) {
	f := serviceFixtureFor(t)
	token := serviceToken(t, f, 1, "svctest-admin", auth.RoleAdmin)

	id := serviceSeedVehicle(t, f.pool, "svctest-fleet", nil)

	unit := serviceFleetUnit(t, f, token, id)
	if unit.TotalDistanceKM != 0 {
		t.Errorf("the odometer is %v km, want 0 km", unit.TotalDistanceKM)
	}
	if unit.Service.IntervalKM != serviceTestIntervalKM {
		t.Errorf("the interval is %v km, want %v km", unit.Service.IntervalKM, serviceTestIntervalKM)
	}
	if unit.Service.Status != vehicle.ServiceStatusOK {
		t.Errorf("the status is %q, want %q", unit.Service.Status, vehicle.ServiceStatusOK)
	}
	if unit.Service.NextDueKM != serviceTestIntervalKM {
		t.Errorf("the next service is due at %v km, want %v km", unit.Service.NextDueKM, serviceTestIntervalKM)
	}
	if unit.Service.LastServiceAt != nil {
		t.Errorf("the unit has a last service at %v, want none", *unit.Service.LastServiceAt)
	}
}

// A unit that comes close to its schedule is warned once, and a unit that
// stays at its level is not warned again.
func TestServiceSweepWarnsOnceAndEscalatesOnce(t *testing.T) {
	f := serviceFixtureFor(t)

	admin := serviceSeedUser(t, f.pool, "svctest-admin", auth.RoleAdmin)
	token := serviceToken(t, f, admin, "svctest-admin", auth.RoleAdmin)

	endpoint := "https://push.test/service-admin"
	rec := serviceRequest(t, f, http.MethodPost, "/api/push/subscribe", token,
		subscriptionBody(endpoint))
	if rec.Code != http.StatusOK {
		t.Fatalf("the subscribe answered %d: %s", rec.Code, rec.Body.String())
	}

	id := serviceSeedVehicle(t, f.pool, "svctest-sweep", nil)

	// 100 km short of the schedule: the warning distance is used up.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := f.pool.Exec(ctx,
		`UPDATE vehicles SET total_distance_m = 4900000 WHERE id = $1`, id); err != nil {
		t.Fatalf("set the odometer: %v", err)
	}

	n, err := f.handler.CheckServiceReminders(ctx)
	if err != nil {
		t.Fatalf("CheckServiceReminders: %v", err)
	}
	if n != 1 {
		t.Fatalf("the sweep sent %d reminder(s), want 1", n)
	}

	deliveries := f.sender.waitFor(t, 1)
	var found bool
	for _, d := range deliveries {
		if d.UserID != admin {
			continue
		}
		found = true
		if d.Payload.Title != "Servis sebentar lagi" {
			t.Errorf("the title is %q, want \"Servis sebentar lagi\"", d.Payload.Title)
		}
		if d.Payload.Tag != "service-"+strconv.FormatInt(id, 10)+"-due_soon" {
			t.Errorf("the tag is %q, want \"service-%d-due_soon\"", d.Payload.Tag, id)
		}
		if !strings.Contains(d.Payload.Body, "svctest-sweep") {
			t.Errorf("the body %q does not name the unit", d.Payload.Body)
		}
		if !strings.Contains(d.Payload.Body, "100 km") {
			t.Errorf("the body %q does not say how much is left", d.Payload.Body)
		}
	}
	if !found {
		t.Errorf("the test-created admin (id=%d) did not receive the reminder", admin)
	}
	if got := serviceNotifiedLevel(t, f.pool, id); got != vehicle.ServiceLevelDueSoon {
		t.Errorf("the warning level is %q, want %q", got, vehicle.ServiceLevelDueSoon)
	}

	// The sweep runs again and again: the unit stays at its level, so
	// nothing is sent again.
	time.Sleep(300 * time.Millisecond)
	before := len(f.sender.deliveries())
	n, err = f.handler.CheckServiceReminders(ctx)
	if err != nil {
		t.Fatalf("CheckServiceReminders: %v", err)
	}
	if n != 0 {
		t.Errorf("the second sweep sent %d reminder(s), want 0", n)
	}
	time.Sleep(300 * time.Millisecond)
	if got := len(f.sender.deliveries()); got != before {
		t.Errorf("%d messages were delivered after the second sweep, want %d", got, before)
	}

	// The unit drives past its schedule: that is a louder warning, and it
	// is sent once.
	if _, err := f.pool.Exec(ctx,
		`UPDATE vehicles SET total_distance_m = 5150000 WHERE id = $1`, id); err != nil {
		t.Fatalf("set the odometer: %v", err)
	}
	n, err = f.handler.CheckServiceReminders(ctx)
	if err != nil {
		t.Fatalf("CheckServiceReminders: %v", err)
	}
	if n != 1 {
		t.Fatalf("the sweep sent %d reminder(s), want 1", n)
	}

	deliveries = f.sender.waitFor(t, before+1)
	var overdue bool
	// Only the messages that arrived after the second sweep are looked at:
	// the first one is the warning of the level before.
	for _, d := range deliveries[before:] {
		if d.UserID != admin {
			continue
		}
		if d.Payload.Title != "Servis terlewat" {
			t.Errorf("the title is %q, want \"Servis terlewat\"", d.Payload.Title)
		}
		if d.Payload.Tag != "service-"+strconv.FormatInt(id, 10)+"-overdue" {
			t.Errorf("the tag is %q, want \"service-%d-overdue\"", d.Payload.Tag, id)
		}
		if !strings.Contains(d.Payload.Body, "150 km") {
			t.Errorf("the body %q does not say how much it is past the schedule", d.Payload.Body)
		}
		overdue = true
	}
	if !overdue {
		t.Error("the test-created admin did not receive the overdue reminder")
	}
	if got := serviceNotifiedLevel(t, f.pool, id); got != vehicle.ServiceLevelOverdue {
		t.Errorf("the warning level is %q, want %q", got, vehicle.ServiceLevelOverdue)
	}
}

// A service that is written down starts the schedule over: the odometer of
// the unit right now is the new baseline, and the warning is not sent again
// for this cycle.
func TestServiceRecordResetsTheSchedule(t *testing.T) {
	f := serviceFixtureFor(t)

	admin := serviceSeedUser(t, f.pool, "svctest-admin", auth.RoleAdmin)
	token := serviceToken(t, f, admin, "svctest-admin", auth.RoleAdmin)

	id := serviceSeedVehicle(t, f.pool, "svctest-record", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := f.pool.Exec(ctx, `
        UPDATE vehicles
           SET total_distance_m = 4900000, service_notified_level = 'due_soon'
         WHERE id = $1`, id); err != nil {
		t.Fatalf("prepare the unit: %v", err)
	}

	rec := serviceRequest(t, f, http.MethodPost,
		"/api/vehicles/"+strconv.FormatInt(id, 10)+"/services", token,
		map[string]any{"note": "ganti oli"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("the service answered %d: %s", rec.Code, rec.Body.String())
	}

	var answer struct {
		Data struct {
			Service struct {
				ID         int64   `json:"id"`
				VehicleID  int64   `json:"vehicle_id"`
				OdometerM  float64 `json:"odometer_m"`
				Note       string  `json:"note"`
				RecordedBy *int64  `json:"recorded_by"`
			} `json:"service"`
			ServiceStatus struct {
				Status        string  `json:"status"`
				LastServiceKM float64 `json:"last_service_km"`
				RemainingKM   float64 `json:"remaining_km"`
			} `json:"service_status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode the answer: %v (%s)", err, rec.Body.String())
	}

	if answer.Data.Service.VehicleID != id {
		t.Errorf("the service belongs to the unit %d, want %d", answer.Data.Service.VehicleID, id)
	}
	if answer.Data.Service.OdometerM != 4_900_000 {
		t.Errorf("the odometer of the service is %v m, want 4900000 m", answer.Data.Service.OdometerM)
	}
	if answer.Data.Service.Note != "ganti oli" {
		t.Errorf("the note is %q, want %q", answer.Data.Service.Note, "ganti oli")
	}
	if answer.Data.Service.RecordedBy == nil || *answer.Data.Service.RecordedBy != admin {
		t.Errorf("the service was written by %v, want the account %d", answer.Data.Service.RecordedBy, admin)
	}
	if answer.Data.ServiceStatus.Status != vehicle.ServiceStatusOK {
		t.Errorf("the status after the service is %q, want %q",
			answer.Data.ServiceStatus.Status, vehicle.ServiceStatusOK)
	}
	if answer.Data.ServiceStatus.LastServiceKM != 4900 {
		t.Errorf("the baseline is %v km, want 4900 km", answer.Data.ServiceStatus.LastServiceKM)
	}
	if answer.Data.ServiceStatus.RemainingKM != serviceTestIntervalKM {
		t.Errorf("what is left is %v km, want %v km",
			answer.Data.ServiceStatus.RemainingKM, serviceTestIntervalKM)
	}

	// The marker of the last warning is cleared, and the baseline of the
	// unit is the odometer it had when the service was written down.
	if got := serviceNotifiedLevel(t, f.pool, id); got != "" {
		t.Errorf("the warning level is %q, want it to be cleared", got)
	}
	if got := serviceOdometer(t, f.pool, id); got != 4_900_000 {
		t.Errorf("the odometer is %v m, want 4900000 m (the service does not move it)", got)
	}

	// The history holds the service, newest first.
	rec = serviceRequest(t, f, http.MethodGet,
		"/api/vehicles/"+strconv.FormatInt(id, 10)+"/services", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the history answered %d: %s", rec.Code, rec.Body.String())
	}
	var history struct {
		Data struct {
			Services []vehicle.ServiceRecord `json:"services"`
			Count    int                     `json:"count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &history); err != nil {
		t.Fatalf("decode the history: %v (%s)", err, rec.Body.String())
	}
	if history.Data.Count != 1 || len(history.Data.Services) != 1 {
		t.Fatalf("the history holds %d service(s), want 1", len(history.Data.Services))
	}
	if history.Data.Services[0].Note != "ganti oli" {
		t.Errorf("the note of the service is %q, want %q", history.Data.Services[0].Note, "ganti oli")
	}
}

// Only an admin writes a service down, and a service that is done later than
// now is refused.
func TestServiceRecordIsRefusedForTheWrongRoleAndTheWrongDate(t *testing.T) {
	f := serviceFixtureFor(t)

	admin := serviceSeedUser(t, f.pool, "svctest-admin", auth.RoleAdmin)
	staff := serviceSeedUser(t, f.pool, "svctest-staff", auth.RoleStaff)
	adminToken := serviceToken(t, f, admin, "svctest-admin", auth.RoleAdmin)
	staffToken := serviceToken(t, f, staff, "svctest-staff", auth.RoleStaff)

	id := serviceSeedVehicle(t, f.pool, "svctest-refused", nil)
	path := "/api/vehicles/" + strconv.FormatInt(id, 10) + "/services"

	// A staff member is not an admin.
	rec := serviceRequest(t, f, http.MethodPost, path, staffToken, map[string]any{"note": "dilarang"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("the staff member answered %d, want 403", rec.Code)
	}

	// A service that is done later than now is not a service yet.
	tomorrow := time.Now().Add(24 * time.Hour).Format("2006-01-02")
	rec = serviceRequest(t, f, http.MethodPost, path, adminToken,
		map[string]any{"serviced_at": tomorrow})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("the service of tomorrow answered %d, want 400", rec.Code)
	}
	var answer struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode the error: %v", err)
	}
	if answer.Error.Code != "validation_failed" {
		t.Errorf("the error code is %q, want \"validation_failed\"", answer.Error.Code)
	}
}

// A driver reads the history of their own unit — and nothing else.
func TestServiceHistoryOfAnotherUnitIsRefused(t *testing.T) {
	f := serviceFixtureFor(t)

	driver := serviceSeedUser(t, f.pool, "svctest-driver", auth.RoleDriver)
	token := serviceToken(t, f, driver, "svctest-driver", auth.RoleDriver)

	mine := serviceSeedVehicle(t, f.pool, "svctest-mine", &driver)
	other := serviceSeedVehicle(t, f.pool, "svctest-other", nil)

	rec := serviceRequest(t, f, http.MethodGet,
		"/api/vehicles/"+strconv.FormatInt(mine, 10)+"/services", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the history of the own unit answered %d: %s", rec.Code, rec.Body.String())
	}

	rec = serviceRequest(t, f, http.MethodGet,
		"/api/vehicles/"+strconv.FormatInt(other, 10)+"/services", token, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("the history of another unit answered %d, want 403", rec.Code)
	}
}

// The interval of the service is a field of the unit, and changing it moves
// the schedule — without touching the odometer.
func TestServiceIntervalOfTheVehicleMovesTheSchedule(t *testing.T) {
	f := serviceFixtureFor(t)

	admin := serviceSeedUser(t, f.pool, "svctest-admin", auth.RoleAdmin)
	token := serviceToken(t, f, admin, "svctest-admin", auth.RoleAdmin)

	id := serviceSeedVehicle(t, f.pool, "svctest-interval", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := f.pool.Exec(ctx,
		`UPDATE vehicles SET total_distance_m = 1000000 WHERE id = $1`, id); err != nil {
		t.Fatalf("set the odometer: %v", err)
	}

	rec := serviceRequest(t, f, http.MethodPut,
		"/api/vehicles/"+strconv.FormatInt(id, 10), token,
		map[string]any{
			"code":                "svctest-interval",
			"name":                "svctest-interval",
			"kind":                "truck",
			"driver_id":           nil,
			"route_color":         "#d0171e",
			"service_interval_km": 2000,
		})
	if rec.Code != http.StatusOK {
		t.Fatalf("the update answered %d: %s", rec.Code, rec.Body.String())
	}

	unit := serviceFleetUnit(t, f, token, id)
	if unit.Service.IntervalKM != 2000 {
		t.Errorf("the interval is %v km, want 2000 km", unit.Service.IntervalKM)
	}
	if unit.Service.NextDueKM != 2000 {
		t.Errorf("the next service is due at %v km, want 2000 km", unit.Service.NextDueKM)
	}
	if unit.TotalDistanceKM != 1000 {
		t.Errorf("the odometer is %v km, want 1000 km (the update does not touch it)", unit.TotalDistanceKM)
	}
	if got := serviceOdometer(t, f.pool, id); got != 1_000_000 {
		t.Errorf("the odometer is %v m, want 1000000 m", got)
	}

	// An interval that makes no sense is refused.
	rec = serviceRequest(t, f, http.MethodPut,
		"/api/vehicles/"+strconv.FormatInt(id, 10), token,
		map[string]any{
			"code":                "svctest-interval",
			"name":                "svctest-interval",
			"kind":                "truck",
			"service_interval_km": 0,
		})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("the interval of zero answered %d, want 400", rec.Code)
	}
}
