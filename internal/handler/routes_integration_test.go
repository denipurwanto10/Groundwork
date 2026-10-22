//go:build integration

// Integration tests for the status of the stops of a route.
//
// The stops of a route are steps of the trip: the server detects that the
// unit arrived, and the driver confirms the stop (with a note as the proof)
// or passes it by.
//
// Prerequisite: PostgreSQL reachable with the repo .env (DB_*), database
// created, and the migrations applied.
//
// Run: go test -tags integration ./internal/handler/ -v -run RouteStops
package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"entity-management/internal/auth"
	"entity-management/internal/config"
	"entity-management/internal/db"
	"entity-management/internal/ws"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

// routeFixture is the router with the endpoints the tests need, the database
// and the service that signs the tokens.
type routeFixture struct {
	pool    *db.Pool
	router  *gin.Engine
	service *auth.Service
}

// routeFixtureFor builds the fixture and cleans the leftovers of an earlier
// run.
func routeFixtureFor(t *testing.T) *routeFixture {
	t.Helper()

	if err := godotenv.Load("../../.env"); err != nil {
		t.Fatalf("load .env: %v (run from repo root with DB_PASSWORD set)", err)
	}
	// The routing engine is not needed here: straight lines are enough, and
	// the order of the stops is then the one the test can rely on.
	t.Setenv("OSRM_BASE_URL", "off")

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
	routeCleanup(ctx, pool)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		routeCleanup(ctx, pool)
	})

	gin.SetMode(gin.TestMode)
	service := auth.NewServiceWithRefresh(pool, "groundwork-route-stop-test-secret", time.Hour, time.Hour)
	mw := auth.NewMiddleware(service)

	router := gin.New()
	protected := router.Group("/api", mw.Require())

	routeHandler := NewRouteHandler(pool, ws.NewHub(), nil)
	vehicleHandler := NewVehicleHandler(pool, ws.NewHub(), nil)

	protected.GET("/vehicles", vehicleHandler.List)
	protected.POST("/vehicles/:id/position", vehicleHandler.UpdatePosition)
	protected.GET("/vehicles/:id/route", routeHandler.Active)
	protected.POST("/vehicles/:id/route", routeHandler.Set)
	protected.POST("/vehicles/:id/route/start", routeHandler.Start)
	protected.POST("/vehicles/:id/route/stops/:stop_id/complete", routeHandler.CompleteStop)
	protected.POST("/vehicles/:id/route/stops/:stop_id/skip", routeHandler.SkipStop)

	return &routeFixture{pool: pool, router: router, service: service}
}

// routeCleanup removes everything the tests wrote.
func routeCleanup(ctx context.Context, pool *db.Pool) {
	pool.Exec(ctx, `DELETE FROM idle_events WHERE vehicle_id IN (SELECT id FROM vehicles WHERE code LIKE 'routetest-%')`)
	pool.Exec(ctx, `DELETE FROM geofence_events WHERE vehicle_id IN (SELECT id FROM vehicles WHERE code LIKE 'routetest-%')`)
	pool.Exec(ctx, `DELETE FROM vehicle_positions WHERE vehicle_id IN (SELECT id FROM vehicles WHERE code LIKE 'routetest-%')`)
	pool.Exec(ctx, `DELETE FROM vehicle_routes WHERE vehicle_id IN (SELECT id FROM vehicles WHERE code LIKE 'routetest-%')`)
	pool.Exec(ctx, `DELETE FROM vehicles WHERE code LIKE 'routetest-%'`)
	pool.Exec(ctx, `DELETE FROM users WHERE username LIKE 'routetest-%'`)
}

// routeSeedUser creates an account and returns its id.
func routeSeedUser(t *testing.T, pool *db.Pool, username, role string) int64 {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var id int64
	err := pool.QueryRow(ctx, `
        INSERT INTO users (username, email, password_hash, role, name)
        VALUES ($1, $2, 'routetest', $3, $1)
        RETURNING id`, username, username+"@routetest.local", role).Scan(&id)
	if err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}

	return id
}

// routeSeedVehicle creates a unit and returns its id.
func routeSeedVehicle(t *testing.T, pool *db.Pool, code string, driverID *int64) int64 {
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

// routeToken signs a token for an account.
func routeToken(t *testing.T, f *routeFixture, id int64, username, role string) string {
	t.Helper()

	session, err := f.service.NewSession(auth.User{ID: id, Username: username, Role: role})
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	return session.AccessToken
}

// routeRequest calls an endpoint with a token.
func routeRequest(t *testing.T, f *routeFixture, method, path, token string, body any) *httptest.ResponseRecorder {
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

// routeData calls an endpoint and decodes the "data" object of the answer.
func routeData(t *testing.T, f *routeFixture, method, path, token string, body any) map[string]any {
	t.Helper()

	rec := routeRequest(t, f, method, path, token, body)
	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("%s %s answered %d: %s", method, path, rec.Code, rec.Body.String())
	}

	var answer struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode the answer %q: %v", rec.Body.String(), err)
	}

	return answer.Data
}

// routeStop is one stop of a route, as the tests read it.
type routeStopRow struct {
	Seq          int
	Name         string
	Status       string
	ArrivedAt    *time.Time
	CompletedAt  *time.Time
	CompletedBy  *int64
	Note         string
	CompletedLat *float64
	CompletedLng *float64
}

// routeStopsOf reads the stops of a route from the database.
func routeStopsOf(t *testing.T, pool *db.Pool, routeID int64) []routeStopRow {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := pool.Query(ctx, `SELECT seq, name, status, arrived_at, completed_at,
			completed_by, completion_note, completed_lat, completed_lng
		  FROM route_stops
		 WHERE route_id = $1
		 ORDER BY seq`, routeID)
	if err != nil {
		t.Fatalf("read the stops of route %d: %v", routeID, err)
	}
	defer rows.Close()

	stops := make([]routeStopRow, 0)
	for rows.Next() {
		var s routeStopRow
		if err := rows.Scan(&s.Seq, &s.Name, &s.Status, &s.ArrivedAt, &s.CompletedAt,
			&s.CompletedBy, &s.Note, &s.CompletedLat, &s.CompletedLng); err != nil {
			t.Fatalf("scan a stop: %v", err)
		}
		stops = append(stops, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the stops of route %d: %v", routeID, err)
	}

	return stops
}

// routePlan asks the server for a route with the given destinations.
func routePlan(t *testing.T, f *routeFixture, token string, vehicleID int64, origin map[string]any, destinations []map[string]any) map[string]any {
	t.Helper()

	return routeData(t, f, http.MethodPost, fmt.Sprintf("/api/vehicles/%d/route", vehicleID), token,
		map[string]any{"origin": origin, "destinations": destinations})
}

// routeStart starts the trip of a unit.
func routeStart(t *testing.T, f *routeFixture, token string, vehicleID int64) {
	t.Helper()

	rec := routeRequest(t, f, http.MethodPost,
		fmt.Sprintf("/api/vehicles/%d/route/start", vehicleID), token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("start the trip answered %d: %s", rec.Code, rec.Body.String())
	}
}

// routeReport lets a unit report a position.
func routeReport(t *testing.T, f *routeFixture, token string, vehicleID int64, lat, lng float64) {
	t.Helper()

	rec := routeRequest(t, f, http.MethodPost,
		fmt.Sprintf("/api/vehicles/%d/position", vehicleID), token,
		map[string]any{"lat": lat, "lng": lng, "speed_kmh": 20, "status": "moving"})
	if rec.Code != http.StatusOK {
		t.Fatalf("the position report answered %d: %s", rec.Code, rec.Body.String())
	}
}

// routeStopID reads the id of one stop out of the answer of the API.
func routeStopID(t *testing.T, route map[string]any, index int) int64 {
	t.Helper()

	stops, ok := route["stops"].([]any)
	if !ok || len(stops) == 0 {
		t.Fatalf("the route carries no stops: %v", route)
	}
	if index >= len(stops) {
		t.Fatalf("the route carries %d stops, want at least %d", len(stops), index+1)
	}
	stop, ok := stops[index].(map[string]any)
	if !ok {
		t.Fatalf("the stop %d is not an object: %v", index, stops[index])
	}
	id, ok := stop["id"].(float64)
	if !ok || id <= 0 {
		t.Fatalf("the stop %d has no id: %v", index, stop)
	}

	return int64(id)
}

// The stops of a route are written in the visiting order, and every route has
// at least one stop.
func TestRouteStopsAreWrittenInTheVisitingOrder(t *testing.T) {
	f := routeFixtureFor(t)
	admin := routeToken(t, f, 1, "routetest-admin", auth.RoleAdmin)

	vehicle := routeSeedVehicle(t, f.pool, "routetest-plan", nil)

	// Three stops, in a line: the greedy order is the order they are given.
	origin := map[string]any{"lat": -6.90, "lng": 107.60, "name": "Origin"}
	destinations := []map[string]any{
		{"lat": -6.85, "lng": 107.60, "name": "Stop A"},
		{"lat": -6.80, "lng": 107.60, "name": "Stop B"},
		{"lat": -6.75, "lng": 107.60, "name": "Stop C"},
	}

	route := routePlan(t, f, admin, vehicle, origin, destinations)
	routeID := int64(route["id"].(float64))

	stops := routeStopsOf(t, f.pool, routeID)
	if len(stops) != 3 {
		t.Fatalf("the route has %d stops, want 3", len(stops))
	}
	for i, stop := range stops {
		if stop.Seq != i {
			t.Errorf("the stop %d has the sequence %d, want %d", i, stop.Seq, i)
		}
		if stop.Status != "pending" {
			t.Errorf("the stop %d is %q, want \"pending\"", i, stop.Status)
		}
	}

	// The order of the stops is the one the server worked out, and the last
	// one is the destination of the route.
	names := []string{}
	for _, stop := range stops {
		names = append(names, stop.Name)
	}
	want := []string{"Stop A", "Stop B", "Stop C"}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("the stop %d is %q, want %q", i, names[i], want[i])
		}
	}
	destination, ok := route["destination"].(map[string]any)
	if !ok {
		t.Fatalf("the route has no destination: %v", route)
	}
	if destination["name"] != "Stop C" {
		t.Errorf("the destination is %q, want \"Stop C\"", destination["name"])
	}
	if route["stops_total"].(float64) != 3 {
		t.Errorf("the route has %v stops, want 3", route["stops_total"])
	}
	if route["stops_done"].(float64) != 0 {
		t.Errorf("the route has %v stops done, want 0", route["stops_done"])
	}

	// A route with a single destination has one stop: the destination.
	single := routePlan(t, f, admin, vehicle, origin, []map[string]any{
		{"lat": -6.85, "lng": 107.60, "name": "Only stop"},
	})
	singleID := int64(single["id"].(float64))
	stops = routeStopsOf(t, f.pool, singleID)
	if len(stops) != 1 {
		t.Fatalf("the one-destination route has %d stops, want 1", len(stops))
	}
	if stops[0].Seq != 0 || stops[0].Name != "Only stop" {
		t.Errorf("the only stop is %+v, want the sequence 0 and the name \"Only stop\"", stops[0])
	}
	if stops[0].Status != "pending" {
		t.Errorf("the only stop is %q, want \"pending\"", stops[0].Status)
	}
}

// The server notices the arrival at the stop the unit is serving, once, and
// the stops are served one after the other.
func TestArrivalIsDetectedOnceAndTheStopsAreServedInOrder(t *testing.T) {
	f := routeFixtureFor(t)
	admin := routeToken(t, f, 1, "routetest-admin", auth.RoleAdmin)

	vehicle := routeSeedVehicle(t, f.pool, "routetest-arrival", nil)
	route := routePlan(t, f, admin, vehicle,
		map[string]any{"lat": -6.90, "lng": 107.60, "name": "Origin"},
		[]map[string]any{
			{"lat": -6.85, "lng": 107.60, "name": "Stop A"},
			{"lat": -6.80, "lng": 107.60, "name": "Stop B"},
			{"lat": -6.75, "lng": 107.60, "name": "Stop C"},
		})
	routeID := int64(route["id"].(float64))

	// The trip has to run before the stops count.
	routeStart(t, f, admin, vehicle)

	// The unit reports a position at the first stop.
	routeReport(t, f, admin, vehicle, -6.85, 107.60)

	stops := routeStopsOf(t, f.pool, routeID)
	if stops[0].Status != "arrived" {
		t.Fatalf("the first stop is %q, want \"arrived\"", stops[0].Status)
	}
	if stops[0].ArrivedAt == nil {
		t.Error("the first stop has no arrival time")
	}
	arrived := *stops[0].ArrivedAt

	// The same place again: the arrival is not detected twice.
	routeReport(t, f, admin, vehicle, -6.85, 107.60)
	stops = routeStopsOf(t, f.pool, routeID)
	if stops[0].ArrivedAt == nil || !stops[0].ArrivedAt.Equal(arrived) {
		t.Errorf("the arrival time changed: %v, want %v", stops[0].ArrivedAt, arrived)
	}
	if stops[0].Status != "arrived" {
		t.Errorf("the first stop is %q, want \"arrived\"", stops[0].Status)
	}

	// The next stop is not reached before the first one is done.
	routeReport(t, f, admin, vehicle, -6.80, 107.60)
	stops = routeStopsOf(t, f.pool, routeID)
	if stops[1].Status != "pending" {
		t.Errorf("the second stop is %q, want \"pending\": the first stop is not done yet", stops[1].Status)
	}
	if stops[1].ArrivedAt != nil {
		t.Errorf("the second stop has an arrival time: %v", stops[1].ArrivedAt)
	}
}

// The driver finishes the stop the unit is serving, with a note as the
// proof of the visit.
func TestTheDriverFinishesAStop(t *testing.T) {
	f := routeFixtureFor(t)
	admin := routeToken(t, f, 1, "routetest-admin", auth.RoleAdmin)

	driver := routeSeedUser(t, f.pool, "routetest-driver", auth.RoleDriver)
	other := routeSeedUser(t, f.pool, "routetest-other", auth.RoleDriver)
	vehicle := routeSeedVehicle(t, f.pool, "routetest-complete", &driver)

	driverToken := routeToken(t, f, driver, "routetest-driver", auth.RoleDriver)
	otherToken := routeToken(t, f, other, "routetest-other", auth.RoleDriver)

	route := routePlan(t, f, admin, vehicle,
		map[string]any{"lat": -6.90, "lng": 107.60, "name": "Origin"},
		[]map[string]any{
			{"lat": -6.85, "lng": 107.60, "name": "Stop A"},
			{"lat": -6.80, "lng": 107.60, "name": "Stop B"},
		})
	routeID := int64(route["id"].(float64))
	firstStop := routeStopID(t, route, 0)
	secondStop := routeStopID(t, route, 1)

	routeStart(t, f, admin, vehicle)

	// A stop that was not reached yet cannot be confirmed.
	rec := routeRequest(t, f, http.MethodPost,
		fmt.Sprintf("/api/vehicles/%d/route/stops/%d/complete", vehicle, secondStop),
		driverToken, map[string]any{"note": "too early"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("confirming a pending stop answered %d, want 409: %s", rec.Code, rec.Body.String())
	}

	// The unit arrives at the first stop.
	routeReport(t, f, admin, vehicle, -6.85, 107.60)

	// A driver of another unit may not touch this one.
	rec = routeRequest(t, f, http.MethodPost,
		fmt.Sprintf("/api/vehicles/%d/route/stops/%d/complete", vehicle, firstStop),
		otherToken, map[string]any{"note": "not my unit"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("another driver answered %d, want 403: %s", rec.Code, rec.Body.String())
	}

	// The owner of the unit confirms the stop, with a note.
	data := routeData(t, f, http.MethodPost,
		fmt.Sprintf("/api/vehicles/%d/route/stops/%d/complete", vehicle, firstStop),
		driverToken, map[string]any{"note": "Paket diterima satpam"})
	if data["status"] != "completed" {
		t.Fatalf("the stop is %v, want \"completed\"", data["status"])
	}
	if data["completion_note"] != "Paket diterima satpam" {
		t.Errorf("the note is %q, want the note of the driver", data["completion_note"])
	}

	stops := routeStopsOf(t, f.pool, routeID)
	if stops[0].Status != "completed" {
		t.Fatalf("the first stop is %q, want \"completed\"", stops[0].Status)
	}
	if stops[0].CompletedAt == nil {
		t.Error("the first stop has no completion time")
	}
	if stops[0].CompletedBy == nil || *stops[0].CompletedBy != driver {
		t.Errorf("the first stop was finished by %v, want the driver %d", stops[0].CompletedBy, driver)
	}
	if stops[0].Note != "Paket diterima satpam" {
		t.Errorf("the note is %q, want the note of the driver", stops[0].Note)
	}
	if stops[0].CompletedLat == nil || stops[0].CompletedLng == nil {
		t.Fatalf("the first stop has no position of evidence: %+v", stops[0])
	}
	if *stops[0].CompletedLat != -6.85 || *stops[0].CompletedLng != 107.60 {
		t.Errorf("the position of the evidence is %v,%v, want the position of the unit",
			*stops[0].CompletedLat, *stops[0].CompletedLng)
	}

	// The second stop is the one the unit is serving now: it can be skipped.
	rec = routeRequest(t, f, http.MethodPost,
		fmt.Sprintf("/api/vehicles/%d/route/stops/%d/skip", vehicle, secondStop),
		driverToken, map[string]any{"note": "tidak ada paket"})
	if rec.Code != http.StatusOK {
		t.Fatalf("skipping the stop answered %d: %s", rec.Code, rec.Body.String())
	}
	stops = routeStopsOf(t, f.pool, routeID)
	if stops[1].Status != "skipped" {
		t.Errorf("the second stop is %q, want \"skipped\"", stops[1].Status)
	}
	if stops[1].Note != "tidak ada paket" {
		t.Errorf("the note of the skipped stop is %q", stops[1].Note)
	}

	// A note that runs into a novel is refused.
	rec = routeRequest(t, f, http.MethodPost,
		fmt.Sprintf("/api/vehicles/%d/route/stops/%d/complete", vehicle, secondStop),
		driverToken, map[string]any{"note": string(make([]byte, 501))})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a long note answered %d, want 400: %s", rec.Code, rec.Body.String())
	}

	// A stop that does not exist is not found.
	rec = routeRequest(t, f, http.MethodPost,
		fmt.Sprintf("/api/vehicles/%d/route/stops/999999/complete", vehicle),
		driverToken, map[string]any{"note": "hantu"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown stop answered %d, want 404: %s", rec.Code, rec.Body.String())
	}

	// The viewer and the staff read the route, they do not finish its stops.
	viewer := routeToken(t, f, 1, "routetest-viewer", auth.RoleViewer)
	rec = routeRequest(t, f, http.MethodPost,
		fmt.Sprintf("/api/vehicles/%d/route/stops/%d/skip", vehicle, secondStop),
		viewer, map[string]any{"note": "hantu"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("the viewer answered %d, want 403: %s", rec.Code, rec.Body.String())
	}
}

// Skipping the stop the unit is serving frees the next one: the trip moves
// on instead of waiting.
func TestSkippingAStopFreesTheNextOne(t *testing.T) {
	f := routeFixtureFor(t)
	admin := routeToken(t, f, 1, "routetest-admin", auth.RoleAdmin)

	vehicle := routeSeedVehicle(t, f.pool, "routetest-skip", nil)
	route := routePlan(t, f, admin, vehicle,
		map[string]any{"lat": -6.90, "lng": 107.60, "name": "Origin"},
		[]map[string]any{
			{"lat": -6.85, "lng": 107.60, "name": "Stop A"},
			{"lat": -6.80, "lng": 107.60, "name": "Stop B"},
			{"lat": -6.75, "lng": 107.60, "name": "Stop C"},
		})
	routeID := int64(route["id"].(float64))
	firstStop := routeStopID(t, route, 0)

	routeStart(t, f, admin, vehicle)

	rec := routeRequest(t, f, http.MethodPost,
		fmt.Sprintf("/api/vehicles/%d/route/stops/%d/skip", vehicle, firstStop),
		admin, map[string]any{"note": "lama"})
	if rec.Code != http.StatusOK {
		t.Fatalf("skipping the stop answered %d: %s", rec.Code, rec.Body.String())
	}

	// The unit arrives at the second stop: it is the one it is serving now.
	routeReport(t, f, admin, vehicle, -6.80, 107.60)

	stops := routeStopsOf(t, f.pool, routeID)
	if stops[0].Status != "skipped" {
		t.Errorf("the first stop is %q, want \"skipped\"", stops[0].Status)
	}
	if stops[1].Status != "arrived" {
		t.Errorf("the second stop is %q, want \"arrived\"", stops[1].Status)
	}
}

// The route closes itself when the unit reaches the end of the line, and the
// last stop is served by that arrival.
func TestTheRouteClosesWithItsLastStop(t *testing.T) {
	f := routeFixtureFor(t)
	admin := routeToken(t, f, 1, "routetest-admin", auth.RoleAdmin)

	vehicle := routeSeedVehicle(t, f.pool, "routetest-last", nil)
	route := routePlan(t, f, admin, vehicle,
		map[string]any{"lat": -6.90, "lng": 107.60, "name": "Origin"},
		[]map[string]any{
			{"lat": -6.85, "lng": 107.60, "name": "Stop A"},
			{"lat": -6.75, "lng": 107.60, "name": "Stop B"},
		})
	routeID := int64(route["id"].(float64))

	routeStart(t, f, admin, vehicle)

	// The unit reaches the end of the line.
	routeReport(t, f, admin, vehicle, -6.75, 107.60)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var status string
	if err := f.pool.QueryRow(ctx, `SELECT status FROM vehicle_routes WHERE id = $1`, routeID).Scan(&status); err != nil {
		t.Fatalf("read the route: %v", err)
	}
	if status != "completed" {
		t.Fatalf("the route is %q, want \"completed\"", status)
	}

	stops := routeStopsOf(t, f.pool, routeID)
	last := stops[len(stops)-1]
	if last.Status != "completed" {
		t.Errorf("the last stop is %q, want \"completed\"", last.Status)
	}
	if last.CompletedAt == nil {
		t.Error("the last stop has no completion time")
	}
	if last.Note != "Otomatis: rute selesai" {
		t.Errorf("the note of the last stop is %q, want the note of the server", last.Note)
	}
}

// A new route starts from scratch: the stops of the route before it keep
// their own status and do not follow the unit.
func TestANewRouteDoesNotCarryTheStopsOfTheOldOne(t *testing.T) {
	f := routeFixtureFor(t)
	admin := routeToken(t, f, 1, "routetest-admin", auth.RoleAdmin)

	vehicle := routeSeedVehicle(t, f.pool, "routetest-replan", nil)
	first := routePlan(t, f, admin, vehicle,
		map[string]any{"lat": -6.90, "lng": 107.60, "name": "Origin"},
		[]map[string]any{
			{"lat": -6.85, "lng": 107.60, "name": "Stop A"},
			{"lat": -6.80, "lng": 107.60, "name": "Stop B"},
		})
	firstID := int64(first["id"].(float64))

	routeStart(t, f, admin, vehicle)
	routeReport(t, f, admin, vehicle, -6.85, 107.60)

	stops := routeStopsOf(t, f.pool, firstID)
	if stops[0].Status != "arrived" {
		t.Fatalf("the first stop is %q, want \"arrived\"", stops[0].Status)
	}

	// A new plan cancels the route before it.
	second := routePlan(t, f, admin, vehicle,
		map[string]any{"lat": -6.90, "lng": 107.60, "name": "Origin"},
		[]map[string]any{
			{"lat": -6.85, "lng": 107.60, "name": "Stop A"},
			{"lat": -6.80, "lng": 107.60, "name": "Stop B"},
		})
	secondID := int64(second["id"].(float64))
	if secondID == firstID {
		t.Fatal("the new plan kept the id of the route before it")
	}

	// The stops of the new route start from the beginning.
	stops = routeStopsOf(t, f.pool, secondID)
	if len(stops) != 2 {
		t.Fatalf("the new route has %d stops, want 2", len(stops))
	}
	for i, stop := range stops {
		if stop.Status != "pending" {
			t.Errorf("the stop %d of the new route is %q, want \"pending\"", i, stop.Status)
		}
		if stop.ArrivedAt != nil || stop.CompletedAt != nil {
			t.Errorf("the stop %d of the new route carries the history of the old one: %+v", i, stop)
		}
	}

	// The stops of the old route keep their own status.
	stops = routeStopsOf(t, f.pool, firstID)
	if stops[0].Status != "arrived" {
		t.Errorf("the first stop of the old route is %q, want \"arrived\"", stops[0].Status)
	}
}

// The active route of a vehicle carries its stops, so the client sees the
// status of every stop without asking for it again.
func TestTheActiveRouteCarriesItsStops(t *testing.T) {
	f := routeFixtureFor(t)
	admin := routeToken(t, f, 1, "routetest-admin", auth.RoleAdmin)

	vehicle := routeSeedVehicle(t, f.pool, "routetest-active", nil)
	routePlan(t, f, admin, vehicle,
		map[string]any{"lat": -6.90, "lng": 107.60, "name": "Origin"},
		[]map[string]any{
			{"lat": -6.85, "lng": 107.60, "name": "Stop A"},
			{"lat": -6.80, "lng": 107.60, "name": "Stop B"},
		})
	routeStart(t, f, admin, vehicle)
	routeReport(t, f, admin, vehicle, -6.85, 107.60)

	data := routeData(t, f, http.MethodGet,
		fmt.Sprintf("/api/vehicles/%d/route", vehicle), admin, nil)

	stops, ok := data["stops"].([]any)
	if !ok || len(stops) != 2 {
		t.Fatalf("the active route carries %v stops, want 2", data["stops"])
	}
	first := stops[0].(map[string]any)
	if first["status"] != "arrived" {
		t.Errorf("the first stop is %v, want \"arrived\"", first["status"])
	}
	if data["stops_total"].(float64) != 2 {
		t.Errorf("the route has %v stops, want 2", data["stops_total"])
	}
	if data["stops_done"].(float64) != 0 {
		t.Errorf("the route has %v stops done, want 0", data["stops_done"])
	}

	// The fleet list carries the same route.
	data = routeData(t, f, http.MethodGet, "/api/vehicles", admin, nil)
	fleet, ok := data["vehicles"].([]any)
	if !ok {
		t.Fatalf("the fleet carries no vehicles: %v", data)
	}
	found := false
	for _, item := range fleet {
		v := item.(map[string]any)
		if int64(v["id"].(float64)) != vehicle {
			continue
		}
		found = true
		active, ok := v["active_route"].(map[string]any)
		if !ok {
			t.Fatalf("the unit has no active route: %v", v)
		}
		if active["stops_total"].(float64) != 2 {
			t.Errorf("the active route of the fleet has %v stops, want 2", active["stops_total"])
		}
	}
	if !found {
		t.Fatalf("the unit %d is not in the fleet", vehicle)
	}
}
