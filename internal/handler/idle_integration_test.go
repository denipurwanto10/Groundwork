//go:build integration

// Integration tests for the idle detection: the anchor of a unit, the sweep
// that turns a unit that stands still into an idle one, and the history of
// the idle sessions.
//
// Prerequisite: PostgreSQL reachable with the repo .env (DB_*), database
// created, and the migrations applied.
//
// Run: go test -tags integration ./internal/handler/ -v -run Idle
package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"entity-management/internal/auth"
	"entity-management/internal/config"
	"entity-management/internal/db"
	"entity-management/internal/ws"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

// idleTestAfter is the idle threshold of the tests: small enough to wait for
// it, big enough to survive a slow machine.
const idleTestAfter = 2 * time.Second

// idleFixture is the vehicle handler with the idle detection turned on, the
// database and a router with the three endpoints the tests need.
type idleFixture struct {
	handler *VehicleHandler
	pool    *db.Pool
	router  *gin.Engine
	service *auth.Service
}

// idleFixtureFor builds the fixture and cleans the leftovers of an earlier run.
func idleFixtureFor(t *testing.T) *idleFixture {
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
	idleCleanup(ctx, pool)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		idleCleanup(ctx, pool)
	})

	gin.SetMode(gin.TestMode)
	service := auth.NewServiceWithRefresh(pool, "groundwork-idle-test-secret", time.Hour, time.Hour)
	mw := auth.NewMiddleware(service)

	router := gin.New()
	protected := router.Group("/api", mw.Require())

	vehicleHandler := NewVehicleHandler(pool, ws.NewHub(), nil)
	vehicleHandler.SetOfflineAfter(60 * time.Second)
	vehicleHandler.SetIdleDetection(idleTestAfter, 25, 2)

	protected.POST("/vehicles/:id/position", vehicleHandler.UpdatePosition)
	protected.GET("/vehicles", vehicleHandler.List)

	idleHandler := NewIdleHandler(pool)
	protected.GET("/idle-events", idleHandler.List)

	return &idleFixture{handler: vehicleHandler, pool: pool, router: router, service: service}
}

// idleCleanup removes everything the tests wrote.
func idleCleanup(ctx context.Context, pool *db.Pool) {
	pool.Exec(ctx, `DELETE FROM idle_events WHERE vehicle_id IN (SELECT id FROM vehicles WHERE code LIKE 'idletest-%')`)
	pool.Exec(ctx, `DELETE FROM geofence_events WHERE vehicle_id IN (SELECT id FROM vehicles WHERE code LIKE 'idletest-%')`)
	pool.Exec(ctx, `DELETE FROM vehicle_positions WHERE vehicle_id IN (SELECT id FROM vehicles WHERE code LIKE 'idletest-%')`)
	pool.Exec(ctx, `DELETE FROM vehicles WHERE code LIKE 'idletest-%'`)
	pool.Exec(ctx, `DELETE FROM zones WHERE name LIKE 'idletest-%'`)
	pool.Exec(ctx, `DELETE FROM users WHERE username LIKE 'idletest-%'`)
}

// idleSeedVehicle creates a unit that never reported and returns its id.
func idleSeedVehicle(t *testing.T, pool *db.Pool, code string, driverID *int64) int64 {
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

// idleToken signs a token for an account.
func idleToken(t *testing.T, f *idleFixture, id int64, username, role string) string {
	t.Helper()

	session, err := f.service.NewSession(auth.User{ID: id, Username: username, Role: role})
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return session.AccessToken
}

// idleRequest calls an endpoint with a token.
func idleRequest(t *testing.T, f *idleFixture, method, path, token string, body any) *httptest.ResponseRecorder {
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

// reportPosition lets a unit report a position through the API.
func reportPosition(t *testing.T, f *idleFixture, token string, id int64, lat, lng, speed float64, status string) {
	t.Helper()

	rec := idleRequest(t, f, http.MethodPost, "/api/vehicles/"+strconv.FormatInt(id, 10)+"/position", token,
		map[string]any{"lat": lat, "lng": lng, "speed_kmh": speed, "status": status})
	if rec.Code != http.StatusOK {
		t.Fatalf("the position report answered %d: %s", rec.Code, rec.Body.String())
	}
}

// statusOf reads the stored status of a unit.
func idleStatusOf(t *testing.T, pool *db.Pool, id int64) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM vehicles WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}

	return status
}

// openSessions counts the idle sessions of a unit that are still running.
func openSessions(t *testing.T, pool *db.Pool, id int64) int {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var n int
	if err := pool.QueryRow(ctx, `
        SELECT count(*) FROM idle_events
         WHERE vehicle_id = $1 AND ended_at IS NULL`, id).Scan(&n); err != nil {
		t.Fatalf("count the open idle sessions: %v", err)
	}

	return n
}

// A unit that keeps reporting the same spot turns idle, and the sweep never
// opens a second session for it.
func TestIdleSweepMarksTheUnitThatStandsStill(t *testing.T) {
	f := idleFixtureFor(t)
	token := idleToken(t, f, 1, "idletest-admin", auth.RoleAdmin)

	id := idleSeedVehicle(t, f.pool, "idletest-standing", nil)

	// Two reports from the same spot: the first one places the anchor, the
	// second one stays inside the radius of it.
	reportPosition(t, f, token, id, -6.2, 106.8, 0, "idle")
	reportPosition(t, f, token, id, -6.2, 106.8, 0, "idle")

	// Nothing to say yet: the unit has not stood still long enough.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	n, err := f.handler.MarkStationaryIdle(ctx)
	if err != nil {
		t.Fatalf("MarkStationaryIdle: %v", err)
	}
	if n != 0 {
		t.Fatalf("the sweep opened %d session(s) too early", n)
	}

	time.Sleep(idleTestAfter + 500*time.Millisecond)

	n, err = f.handler.MarkStationaryIdle(ctx)
	if err != nil {
		t.Fatalf("MarkStationaryIdle: %v", err)
	}
	if n != 1 {
		t.Fatalf("the sweep opened %d session(s), want 1", n)
	}
	if got := idleStatusOf(t, f.pool, id); got != "idle" {
		t.Errorf("the unit is %q, want \"idle\"", got)
	}
	if got := openSessions(t, f.pool, id); got != 1 {
		t.Errorf("the unit has %d open session(s), want 1", got)
	}

	// The sweep is idempotent: a unit that already has a session keeps it.
	n, err = f.handler.MarkStationaryIdle(ctx)
	if err != nil {
		t.Fatalf("MarkStationaryIdle: %v", err)
	}
	if n != 0 {
		t.Errorf("the second sweep opened %d session(s), want 0", n)
	}
	if got := openSessions(t, f.pool, id); got != 1 {
		t.Errorf("the unit has %d open session(s) after the second sweep, want 1", got)
	}
}

// A unit that drives on ends its session by itself: the position report
// closes it, and the status follows the report again.
func TestAMovingUnitEndsItsIdleSession(t *testing.T) {
	f := idleFixtureFor(t)
	token := idleToken(t, f, 1, "idletest-admin", auth.RoleAdmin)

	id := idleSeedVehicle(t, f.pool, "idletest-moving", nil)

	reportPosition(t, f, token, id, -6.2, 106.8, 0, "idle")
	time.Sleep(idleTestAfter + 500*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := f.handler.MarkStationaryIdle(ctx); err != nil {
		t.Fatalf("MarkStationaryIdle: %v", err)
	}
	if got := openSessions(t, f.pool, id); got != 1 {
		t.Fatalf("the unit has %d open session(s), want 1", got)
	}

	// A report from far outside the radius of the anchor: the unit moved.
	reportPosition(t, f, token, id, -6.201, 106.8, 0, "moving")

	if got := idleStatusOf(t, f.pool, id); got != "moving" {
		t.Errorf("the unit is %q, want \"moving\"", got)
	}

	var (
		ended   *time.Time
		started time.Time
	)
	if err := f.pool.QueryRow(ctx, `
        SELECT started_at, ended_at FROM idle_events
         WHERE vehicle_id = $1 ORDER BY started_at DESC LIMIT 1`, id).Scan(&started, &ended); err != nil {
		t.Fatalf("read the idle session: %v", err)
	}
	if ended == nil {
		t.Fatal("the idle session is still open, want it closed")
	}
	if ended.Before(started) {
		t.Errorf("the session ends at %v, before it started at %v", ended, started)
	}
	if got := ended.Sub(started); got < idleTestAfter {
		t.Errorf("the session lasted %v, want at least %v", got, idleTestAfter)
	}
}

// A unit that stops reporting is offline, not idle: offline always wins.
func TestASilentUnitIsOfflineAndNotIdle(t *testing.T) {
	f := idleFixtureFor(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// A unit that stood still for ten minutes and then fell silent five
	// minutes ago.
	var id int64
	err := f.pool.QueryRow(ctx, `
        INSERT INTO vehicles (code, name, kind, status, last_lat, last_lng, last_seen_at,
                              stationary_since, stationary_anchor_lat, stationary_anchor_lng)
        VALUES ('idletest-silent', 'idletest-silent', 'truck', 'moving', -6.2, 106.8,
                now() - interval '5 minutes', now() - interval '10 minutes', -6.2, 106.8)
        RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatalf("insert the silent unit: %v", err)
	}

	n, err := f.handler.MarkStationaryIdle(ctx)
	if err != nil {
		t.Fatalf("MarkStationaryIdle: %v", err)
	}
	if n != 0 {
		t.Errorf("the sweep opened %d session(s) for a silent unit, want 0", n)
	}
	if got := openSessions(t, f.pool, id); got != 0 {
		t.Errorf("the silent unit has %d open session(s), want 0", got)
	}

	// The offline sweep is the one that speaks about a silent unit.
	marked, err := f.handler.MarkStaleOffline(ctx)
	if err != nil {
		t.Fatalf("MarkStaleOffline: %v", err)
	}
	if marked != 1 {
		t.Errorf("the offline sweep marked %d unit(s), want 1", marked)
	}
	if got := idleStatusOf(t, f.pool, id); got != "offline" {
		t.Errorf("the silent unit is %q, want \"offline\"", got)
	}
}

// A unit that never reported has nothing to be late about.
func TestTheIdleSweepLeavesTheUnitsThatNeverReportedAlone(t *testing.T) {
	f := idleFixtureFor(t)

	id := idleSeedVehicle(t, f.pool, "idletest-never", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	n, err := f.handler.MarkStationaryIdle(ctx)
	if err != nil {
		t.Fatalf("MarkStationaryIdle: %v", err)
	}
	if n != 0 {
		t.Errorf("the sweep opened %d session(s), want 0", n)
	}
	if got := idleStatusOf(t, f.pool, id); got != "moving" {
		t.Errorf("the unit that never reported is %q, want \"moving\"", got)
	}
	if got := openSessions(t, f.pool, id); got != 0 {
		t.Errorf("the unit has %d open session(s), want 0", got)
	}
}

// The history of the idle sessions, and the scope of a driver: the sessions
// of somebody else's truck are none of their business.
func TestIdleEventsAndDriverScope(t *testing.T) {
	f := idleFixtureFor(t)

	driver := idleSeedUser(t, f.pool, "idletest-driver", auth.RoleDriver)
	mine := idleSeedVehicle(t, f.pool, "idletest-mine", &driver)
	other := idleSeedVehicle(t, f.pool, "idletest-other", nil)

	idleSeedSession(t, f.pool, mine, time.Now().Add(-10*time.Minute), nil)
	idleSeedSession(t, f.pool, other, time.Now().Add(-5*time.Minute), nil)

	// The admin sees both.
	admin := idleToken(t, f, 1, "idletest-admin", auth.RoleAdmin)
	rec := idleRequest(t, f, http.MethodGet, "/api/idle-events", admin, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/idle-events answered %d: %s", rec.Code, rec.Body.String())
	}
	events := readIdleEvents(t, rec)
	if len(events) != 2 {
		t.Fatalf("the admin sees %d session(s), want 2", len(events))
	}
	// Newest first.
	if !events[0].StartedAt.After(events[1].StartedAt) {
		t.Errorf("the sessions are not sorted newest first: %v", events)
	}
	if events[0].VehicleID != other {
		t.Errorf("the newest session is about the unit %d, want %d", events[0].VehicleID, other)
	}
	if events[0].DurationS <= 0 {
		t.Errorf("a running session lasts %v seconds, want more than 0", events[0].DurationS)
	}
	if events[0].EndedAt != nil {
		t.Errorf("the session of a standing unit is closed at %v, want it open", events[0].EndedAt)
	}

	// The driver sees only their own unit.
	driverToken := idleToken(t, f, driver, "idletest-driver", auth.RoleDriver)
	rec = idleRequest(t, f, http.MethodGet, "/api/idle-events", driverToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/idle-events answered %d: %s", rec.Code, rec.Body.String())
	}
	events = readIdleEvents(t, rec)
	if len(events) != 1 {
		t.Fatalf("the driver sees %d session(s), want only their own", len(events))
	}
	if events[0].VehicleID != mine {
		t.Errorf("the driver sees the session of the unit %d, want %d", events[0].VehicleID, mine)
	}
}

// A driver may not read the sessions of another unit, not even by asking for
// them directly.
func TestIdleEventsOfAnotherUnitAreRefused(t *testing.T) {
	f := idleFixtureFor(t)

	driver := idleSeedUser(t, f.pool, "idletest-driver2", auth.RoleDriver)
	mine := idleSeedVehicle(t, f.pool, "idletest-mine2", &driver)
	other := idleSeedVehicle(t, f.pool, "idletest-other2", nil)
	idleSeedSession(t, f.pool, other, time.Now().Add(-5*time.Minute), nil)

	token := idleToken(t, f, driver, "idletest-driver2", auth.RoleDriver)
	rec := idleRequest(t, f, http.MethodGet, "/api/idle-events?vehicle_id="+strconv.FormatInt(other, 10), token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/idle-events answered %d: %s", rec.Code, rec.Body.String())
	}
	if events := readIdleEvents(t, rec); len(events) != 0 {
		t.Errorf("the driver sees %d session(s) of another unit, want 0", len(events))
	}

	// Their own unit is still there.
	rec = idleRequest(t, f, http.MethodGet, "/api/idle-events?vehicle_id="+strconv.FormatInt(mine, 10), token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/idle-events answered %d: %s", rec.Code, rec.Body.String())
	}
}

// The zone a unit stands in, and how long it has been there: the dwell time
// comes from the crossings that were never left again.
func TestCurrentZoneAndDwellTime(t *testing.T) {
	f := idleFixtureFor(t)
	token := idleToken(t, f, 1, "idletest-admin", auth.RoleAdmin)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	zoneID := idleSeedZone(t, f.pool, "idletest-warehouse")
	id := idleSeedVehicle(t, f.pool, "idletest-dwell", nil)

	// The unit stands inside the zone and crossed into it five minutes ago.
	if _, err := f.pool.Exec(ctx, `
        UPDATE vehicles
           SET last_lat = 0.5, last_lng = 0.5, last_seen_at = now()
         WHERE id = $1`, id); err != nil {
		t.Fatalf("place the unit inside the zone: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `
        INSERT INTO geofence_events (vehicle_id, zone_id, event_type, created_at)
        VALUES ($1, $2, 'ENTER', now() - interval '5 minutes')`, id, zoneID); err != nil {
		t.Fatalf("write the crossing: %v", err)
	}

	rec := idleRequest(t, f, http.MethodGet, "/api/vehicles", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/vehicles answered %d: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Data struct {
			Vehicles []struct {
				ID          int64    `json:"id"`
				IdleSince   *string  `json:"idle_since"`
				IdleDurS    *float64 `json:"idle_duration_s"`
				CurrentZone *struct {
					ZoneID   int64   `json:"zone_id"`
					ZoneName string  `json:"zone_name"`
					Since    string  `json:"since"`
					DwellS   float64 `json:"dwell_s"`
				} `json:"current_zone"`
			} `json:"vehicles"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the fleet: %v (%s)", err, rec.Body.String())
	}

	var found bool
	for _, v := range body.Data.Vehicles {
		if v.ID != id {
			continue
		}
		found = true
		if v.CurrentZone == nil {
			t.Fatalf("the unit has no current zone, want the zone %d", zoneID)
		}
		if v.CurrentZone.ZoneID != zoneID {
			t.Errorf("the unit stands in the zone %d, want %d", v.CurrentZone.ZoneID, zoneID)
		}
		if v.CurrentZone.ZoneName != "idletest-warehouse" {
			t.Errorf("the zone is called %q, want \"idletest-warehouse\"", v.CurrentZone.ZoneName)
		}
		if v.CurrentZone.DwellS < 240 || v.CurrentZone.DwellS > 600 {
			t.Errorf("the dwell time is %v seconds, want about 300", v.CurrentZone.DwellS)
		}
		if v.CurrentZone.Since == "" {
			t.Error("the zone has no arrival time")
		}
	}
	if !found {
		t.Fatalf("the unit %d is not in the fleet", id)
	}
}

// A unit that is not idle has no idle time to show.
func TestAFleetWithoutIdleSessionsHasNoIdleTime(t *testing.T) {
	f := idleFixtureFor(t)
	token := idleToken(t, f, 1, "idletest-admin", auth.RoleAdmin)

	id := idleSeedVehicle(t, f.pool, "idletest-fresh", nil)
	reportPosition(t, f, token, id, -6.2, 106.8, 30, "moving")

	rec := idleRequest(t, f, http.MethodGet, "/api/vehicles", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/vehicles answered %d: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Data struct {
			Vehicles []struct {
				ID        int64    `json:"id"`
				Status    string   `json:"status"`
				IdleSince *string  `json:"idle_since"`
				IdleDurS  *float64 `json:"idle_duration_s"`
			} `json:"vehicles"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the fleet: %v (%s)", err, rec.Body.String())
	}

	for _, v := range body.Data.Vehicles {
		if v.ID != id {
			continue
		}
		if v.Status != "moving" {
			t.Errorf("the unit is %q, want \"moving\"", v.Status)
		}
		if v.IdleSince != nil {
			t.Errorf("the unit has an idle time since %v, want none", *v.IdleSince)
		}
		if v.IdleDurS != nil {
			t.Errorf("the unit has an idle duration of %v, want none", *v.IdleDurS)
		}
	}
}

// idleSeedUser creates an account and returns its id.
func idleSeedUser(t *testing.T, pool *db.Pool, username, role string) int64 {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var id int64
	err := pool.QueryRow(ctx, `
        INSERT INTO users (username, email, password_hash, role, name)
        VALUES ($1, $2, 'idletest', $3, $1)
        RETURNING id`, username, username+"@idletest.local", role).Scan(&id)
	if err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}

	return id
}

// idleSeedSession writes an idle session of a unit.
func idleSeedSession(t *testing.T, pool *db.Pool, vehicleID int64, started time.Time, ended *time.Time) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := pool.Exec(ctx, `
        INSERT INTO idle_events (vehicle_id, started_at, ended_at, anchor_lat, anchor_lng)
        VALUES ($1, $2, $3, -6.2, 106.8)`, vehicleID, started, ended); err != nil {
		t.Fatalf("insert the idle session: %v", err)
	}
}

// idleSeedZone writes a work area around (0.5, 0.5) and returns its id.
func idleSeedZone(t *testing.T, pool *db.Pool, name string) int64 {
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

// readIdleEvents decodes the answer of GET /api/idle-events.
func readIdleEvents(t *testing.T, rec *httptest.ResponseRecorder) []IdleEvent {
	t.Helper()

	var body struct {
		Data []IdleEvent `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the idle history: %v (%s)", err, rec.Body.String())
	}

	return body.Data
}
