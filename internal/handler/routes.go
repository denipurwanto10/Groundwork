package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"entity-management/internal/auth"
	"entity-management/internal/db"
	"entity-management/internal/push"
	"entity-management/internal/routing"
	"entity-management/internal/vehicle"
	"entity-management/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// RouteHandler serves the delivery routes: the planned from -> to path of a
// vehicle. Positions stay in the vehicle handler; this one owns the plan.
type RouteHandler struct {
	db     *db.Pool
	hub    *ws.Hub
	router *routing.Client
	// push tells the operators that a unit left for its destination.
	push *push.Service
}

func NewRouteHandler(pool *db.Pool, hub *ws.Hub, pushService *push.Service) *RouteHandler {
	return &RouteHandler{db: pool, hub: hub, router: routing.NewClient(), push: pushService}
}

// arrivalRadiusM is how close a reported position must be to the destination
// before the route closes itself, so the map stops drawing a path that has
// already been driven.
const arrivalRadiusM = 150.0

// routeStop is one end of a route: a place, without a status. The status of
// a stop is data of its own (see vehicle.RouteStop), and the body of a route
// plan only carries places.
type routeStop = vehicle.RoutePlace

// maxRouteStops is how many destinations one route may have. The trip
// service of the public OSRM server gets slow with many stops, and the
// request line stays short enough for every proxy in between.
const maxRouteStops = 12

// setRouteInput is the body of POST /api/vehicles/:id/route.
//
// The destinations of a multi-stop route are sent as an array. A body with a
// single "destination" still works: it is the one-stop route it always was.
type setRouteInput struct {
	Origin       routeStop   `json:"origin"`
	Destination  *routeStop  `json:"destination"`
	Destinations []routeStop `json:"destinations"`
}

// stops returns the destinations of the route, in the order they were handed
// over. The order they are served in is worked out by the server.
func (in setRouteInput) stops() []routeStop {
	if len(in.Destinations) > 0 {
		return in.Destinations
	}
	if in.Destination != nil {
		return []routeStop{*in.Destination}
	}
	return nil
}

// multi reports whether the route was asked for as a list of destinations.
func (in setRouteInput) multi() bool {
	return len(in.Destinations) > 0
}

// stopField names the field of one destination in the validation answer. A
// route that was asked for with a single "destination" keeps the old name, so
// the error lands on the field the client knows.
func (in setRouteInput) stopField(i int) string {
	if !in.multi() {
		return "destination"
	}

	return fmt.Sprintf("destinations[%d]", i)
}

func (in *setRouteInput) normalize() {
	in.Origin.Name = strings.TrimSpace(in.Origin.Name)
	if in.Destination != nil {
		in.Destination.Name = strings.TrimSpace(in.Destination.Name)
	}
	for i := range in.Destinations {
		in.Destinations[i].Name = strings.TrimSpace(in.Destinations[i].Name)
	}
}

func (in setRouteInput) validate() map[string]string {
	fields := map[string]string{}

	check := func(prefix string, stop routeStop) {
		if math.IsNaN(stop.Lat) || stop.Lat < -90 || stop.Lat > 90 {
			fields[prefix+".lat"] = "lat must be between -90 and 90"
		}
		if math.IsNaN(stop.Lng) || stop.Lng < -180 || stop.Lng > 180 {
			fields[prefix+".lng"] = "lng must be between -180 and 180"
		}
		if len(stop.Name) > 120 {
			fields[prefix+".name"] = "name must be at most 120 characters"
		}
	}
	check("origin", in.Origin)

	stops := in.stops()
	if len(stops) == 0 {
		fields["destinations"] = "at least one destination is required"
		return fields
	}
	if len(stops) > maxRouteStops {
		fields["destinations"] = fmt.Sprintf("a route may have at most %d destinations", maxRouteStops)
		return fields
	}

	for i, stop := range stops {
		check(in.stopField(i), stop)
	}

	if len(fields) > 0 {
		return fields
	}

	// Every stop must be a place of its own. The routing engine snaps the
	// coordinates to the closest road it knows, so two stops on the same
	// corner would be served as one and the line would go nowhere.
	origin := routing.Point{Lat: in.Origin.Lat, Lng: in.Origin.Lng}
	seen := make([]routing.Point, 0, len(stops))
	for i, stop := range stops {
		p := routing.Point{Lat: stop.Lat, Lng: stop.Lng}
		if routing.DistanceMeters(origin, p) < 10 {
			fields[in.stopField(i)] = "a destination must differ from the origin"
			seen = append(seen, p)
			continue
		}
		for j, other := range seen {
			if routing.DistanceMeters(other, p) < 10 {
				fields[in.stopField(i)] = fmt.Sprintf(
					"this destination is the same place as destination %d", j+1)
				break
			}
		}
		seen = append(seen, p)
	}

	return fields
}

// routeProgressSQL is how far along the line the vehicle's last position
// sits, computed by PostGIS so the UI never has to re-derive it from the
// geometry.
//
// Two guards keep it honest. A route is not "100% covered" just because the
// truck happens to be parked 600 km away: progress only counts once the vehicle
// has reported a position *after* the route was created, and only while it is
// within 3 km of the path (roughly the width of a city). Otherwise the
// projection would snap to whichever vertex of the line is closest and report a
// meaningless number.
//
// It is shared by the route queries and by the fleet list, which carries the
// active route of every unit.
const routeProgressSQL = `CASE
            WHEN rt.status = 'completed' THEN 1
            -- A closed route must not keep moving. Progress is a projection of
            -- the live position, and the truck drives on to other deliveries, so
            -- reading today's position against an old line would rewrite history
            -- (a route cancelled at 27% later read 90%). Closed routes replay the
            -- last position logged before they ended instead.
            WHEN rt.status <> 'active' THEN COALESCE((
                SELECT LEAST(1, GREATEST(0, ST_LineLocatePoint(
                           rt.geom, ST_SetSRID(ST_MakePoint(p.lng, p.lat), 4326))))
                  FROM vehicle_positions p
                 WHERE p.vehicle_id = rt.vehicle_id
                   AND p.recorded_at <= COALESCE(rt.ended_at, now())
                 ORDER BY p.recorded_at DESC
                 LIMIT 1), 0)
            WHEN v.last_lat IS NULL OR v.last_lng IS NULL THEN 0
            WHEN v.last_seen_at IS NULL OR v.last_seen_at < rt.created_at THEN 0
            WHEN v.last_geom IS NULL THEN 0
            WHEN ST_Distance(v.last_geom, rt.geom::geography) > 3000 THEN 0
            ELSE LEAST(1, GREATEST(0, ST_LineLocatePoint(
                     rt.geom, ST_SetSRID(ST_MakePoint(v.last_lng, v.last_lat), 4326))))
        END`

const routeCols = `SELECT rt.id, rt.vehicle_id, rt.origin_name, rt.dest_name,
       rt.origin_lat, rt.origin_lng, rt.dest_lat, rt.dest_lng,
       ST_AsGeoJSON(rt.geom, 6)::json, rt.distance_m, rt.duration_s,
       rt.source, rt.status, rt.created_at, rt.ended_at, rt.started_at,
       (v.driver_id IS NOT NULL) AS driver_assigned,
       ` + routeProgressSQL + `
  FROM vehicle_routes rt
  JOIN vehicles v ON v.id = rt.vehicle_id`

// routeView is one route as the client sees it. The geometry is PostGIS'
// GeoJSON, passed through untouched.
type routeView = vehicle.Route

// scanRoute reads one route row.
//
// The stops of a route are not part of the row: they live in their own table
// (migration 028) and are read separately, because a route carries one stop
// per step of the trip and a status with every one of them.
func scanRoute(row rowScanner) (routeView, error) {
	var (
		v       routeView
		geojson []byte
		run     float64
	)
	if err := row.Scan(&v.ID, &v.VehicleID, &v.Origin.Name, &v.Destination.Name,
		&v.Origin.Lat, &v.Origin.Lng, &v.Destination.Lat, &v.Destination.Lng,
		&geojson, &v.DistanceM, &v.DurationS, &v.Source, &v.Status,
		&v.CreatedAt, &v.EndedAt, &v.StartedAt, &v.DriverAssigned, &run); err != nil {
		return routeView{}, err
	}

	v.Geometry = json.RawMessage(geojson)
	v.Progress = round4(run)
	v.RemainingM = round2(v.DistanceM * (1 - v.Progress))

	return v, nil
}

// routeStopsSQL reads the stops of the given routes in the visiting order.
const routeStopsSQL = `SELECT route_id, id, seq, name, lat, lng, status,
       arrived_at, completed_at, completion_note
  FROM route_stops
 WHERE route_id = ANY($1)
 ORDER BY route_id, seq`

// scanStop reads one stop of a route.
func scanStop(row rowScanner) (vehicle.RouteStop, error) {
	var s vehicle.RouteStop
	if err := row.Scan(&s.ID, &s.Seq, &s.Name, &s.Lat, &s.Lng, &s.Status,
		&s.ArrivedAt, &s.CompletedAt, &s.CompletionNote); err != nil {
		return vehicle.RouteStop{}, err
	}

	return s, nil
}

// loadStops fills the stops of the given routes, in one query.
//
// The stops of a route are the steps of the trip: every one of them has a
// status of its own, and the client reads that status from here.
func loadStops(ctx context.Context, pool *db.Pool, routes []*vehicle.Route) error {
	if len(routes) == 0 {
		return nil
	}

	ids := make([]int64, 0, len(routes))
	byID := make(map[int64]*vehicle.Route, len(routes))
	for _, route := range routes {
		ids = append(ids, route.ID)
		byID[route.ID] = route
	}

	rows, err := pool.Query(ctx, routeStopsSQL, ids)
	if err != nil {
		return fmt.Errorf("read the stops of the routes: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var routeID int64
		var s vehicle.RouteStop
		if err := rows.Scan(&routeID, &s.ID, &s.Seq, &s.Name, &s.Lat, &s.Lng,
			&s.Status, &s.ArrivedAt, &s.CompletedAt, &s.CompletionNote); err != nil {
			return fmt.Errorf("scan a stop: %w", err)
		}
		route := byID[routeID]
		if route == nil {
			continue
		}
		route.Stops = append(route.Stops, s)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read the stops of the routes: %w", err)
	}

	for _, route := range routes {
		countStops(route)
	}

	return nil
}

// countStops counts the stops of a route and how many of them are done.
//
// A stop that was skipped counts as done: the trip moved on, and the route
// is one step further.
func countStops(route *vehicle.Route) {
	route.StopsTotal = len(route.Stops)
	route.StopsDone = 0
	for _, stop := range route.Stops {
		if stop.Status == vehicle.StopCompleted || stop.Status == vehicle.StopSkipped {
			route.StopsDone++
		}
	}
}

// currentStop is the stop a unit is serving right now: the first one that is
// not finished yet.
//
// The stops are served one after the other, so the next stop is never
// reached before the one before it is done. A unit that drives past a later
// stop at the beginning of the trip does not count it — the order of the
// stops is the order of the visits.
func currentStop(stops []vehicle.RouteStop) *vehicle.RouteStop {
	var current *vehicle.RouteStop
	for i := range stops {
		stop := &stops[i]
		if stop.Status != vehicle.StopPending && stop.Status != vehicle.StopArrived {
			continue
		}
		if current == nil || stop.Seq < current.Seq {
			current = stop
		}
	}

	return current
}

// maxStopNote is how long the note of a stop may be. It is the proof of the
// visit, not a report: half a page is plenty.
const maxStopNote = 500

// validateStopNote checks the note somebody left on a stop.
func validateStopNote(note string) map[string]string {
	fields := map[string]string{}
	if len(note) > maxStopNote {
		fields["note"] = fmt.Sprintf("note must be at most %d characters", maxStopNote)
	}

	return fields
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}

func (h *RouteHandler) ctxWithTimeout(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 15*time.Second)
}

// List returns every active route, which is what the map draws.
func (h *RouteHandler) List(c *gin.Context) {
	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	routes, err := h.queryRoutes(ctx, routeCols+" WHERE rt.status = 'active' ORDER BY rt.id")
	if err != nil {
		log.Printf("routes: list failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{"routes": routes, "count": len(routes)}})
}

// History returns the recent routes of one vehicle, newest first, so a finished
// delivery keeps its record after the map stopped drawing it.
func (h *RouteHandler) History(c *gin.Context) {
	id, ok := vehicleIDParam(c)
	if !ok {
		return
	}

	if !h.vehicleReadable(c, id) {
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	routes, err := h.queryRoutes(ctx,
		routeCols+" WHERE rt.vehicle_id = $1 ORDER BY rt.created_at DESC LIMIT 20", id)
	if err != nil {
		log.Printf("routes: history failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{"routes": routes, "count": len(routes)}})
}

// Active returns the route a vehicle is following right now, with its
// geometry: the line a driver (or the simulator) needs to walk the delivery.
//
// A unit without an active route answers 404.
func (h *RouteHandler) Active(c *gin.Context) {
	id, ok := vehicleIDParam(c)
	if !ok {
		return
	}

	if !h.vehicleReadable(c, id) {
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	routes, err := h.queryRoutes(ctx,
		routeCols+" WHERE rt.vehicle_id = $1 AND rt.status = 'active' ORDER BY rt.id DESC LIMIT 1", id)
	if err != nil {
		log.Printf("routes: active route failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	if len(routes) == 0 {
		c.JSON(http.StatusNotFound, errorBody("not_found", "this vehicle has no active route"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": routes[0]})
}

// vehicleReadable reports whether the signed-in account may read the routes
// of this vehicle. Everyone but a driver may; a driver only sees the unit that
// is theirs, the same rule the fleet list follows. It writes the error
// response itself, so the caller can simply return.
func (h *RouteHandler) vehicleReadable(c *gin.Context, id int64) bool {
	return vehicleReadable(c, h.db, id)
}

// Set plans and stores a new active route. An earlier active route for the same
// vehicle is cancelled in the same statement, so a unit follows one path only.
func (h *RouteHandler) Set(c *gin.Context) {
	id, ok := vehicleIDParam(c)
	if !ok {
		return
	}

	var in setRouteInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be valid JSON"))
		return
	}
	in.normalize()
	if fields := in.validate(); len(fields) > 0 {
		c.JSON(http.StatusBadRequest, validationErrorBody(fields))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	var exists bool
	if err := h.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM vehicles WHERE id = $1)`, id).Scan(&exists); err != nil {
		log.Printf("routes: vehicle lookup failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	if !exists {
		c.JSON(http.StatusNotFound, errorBody("not_found", "vehicle not found"))
		return
	}

	from := routing.Point{Lat: in.Origin.Lat, Lng: in.Origin.Lng}
	stops := in.stops()
	stopPoints := make([]routing.Point, 0, len(stops))
	for _, stop := range stops {
		stopPoints = append(stopPoints, routing.Point{Lat: stop.Lat, Lng: stop.Lng})
	}

	// The engine has its own deadline: a slow third party must not hold the
	// request open, the straight-line fallback is always available.
	planCtx, cancelPlan := context.WithTimeout(c.Request.Context(), 8*time.Second)
	plan, planErr := h.router.PlanMulti(planCtx, from, stopPoints)
	cancelPlan()
	if planErr != nil {
		log.Printf("routes: engine fallback to straight lines: %v", planErr)
	}

	// A line that PostGIS refuses to store would fail the whole request with
	// an opaque 500, so the points are cleaned up first: a repeated point is
	// dropped, and a line that is left with fewer than two distinct points
	// (the engine snapped both ends to the same road node) becomes the
	// straight connection between the two stops.
	points := routing.Dedupe(plan.Points)
	if len(points) < 2 {
		log.Printf("routes: degenerate geometry, using the straight lines")
		fallback := routing.DirectMultiRoute(from, stopPoints)
		points = fallback.Points
		plan.DistanceM = fallback.DistanceM
		plan.DurationS = fallback.DurationS
		plan.Source = routing.SourceDirect
		plan.Order = fallback.Order
	}

	// The stops in the order the unit serves them. The engine worked that
	// order out; the last stop is where the delivery ends.
	ordered := orderStops(stops, plan.Order)
	if len(ordered) == 0 {
		ordered = stops
	}
	last := ordered[len(ordered)-1]

	// The stops of a multi-stop route are kept in the visiting order. A route
	// with a single destination keeps none in this column: its origin and
	// destination columns already say everything, and the map draws it the
	// way it always did. The column stays for compatibility only — the
	// status of a stop lives in the route_stops table.
	stopsJSON := []byte("[]")
	if len(ordered) > 1 {
		encoded, err := json.Marshal(ordered)
		if err != nil {
			log.Printf("routes: encode stops failed: %v", err)
			c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
			return
		}
		stopsJSON = encoded
	}

	line, err := lineStringGeoJSON(points)
	if err != nil {
		log.Printf("routes: build geometry failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	// The old route is cancelled and the new one stored in one transaction.
	// A single statement cannot do it: a data-modifying CTE runs next to the
	// insert, so the partial unique index still holds the old "active" row
	// and the insert is rejected as a duplicate.
	tx, err := h.db.Begin(ctx)
	if err != nil {
		log.Printf("routes: begin failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
        UPDATE vehicle_routes
           SET status = 'cancelled', ended_at = now()
         WHERE vehicle_id = $1 AND status = 'active'`, id); err != nil {
		log.Printf("routes: cancel previous failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	var newID int64
	err = tx.QueryRow(ctx, `
        INSERT INTO vehicle_routes
               (vehicle_id, origin_name, dest_name, origin_lat, origin_lng,
                dest_lat, dest_lng, geom, distance_m, duration_s, source, stops)
        VALUES ($1, $2, $3, $4, $5, $6, $7,
                ST_SetSRID(ST_GeomFromGeoJSON($8), 4326), $9, $10, $11, $12::jsonb)
        RETURNING id`,
		id, in.Origin.Name, last.Name,
		in.Origin.Lat, in.Origin.Lng, last.Lat, last.Lng,
		string(line), plan.DistanceM, plan.DurationS, plan.Source, string(stopsJSON)).Scan(&newID)
	if err != nil {
		log.Printf("routes: insert failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	// Every stop of the route becomes a step of the trip, in the order the
	// unit serves them. A route with a single destination has one stop: the
	// destination itself.
	for seq, stop := range ordered {
		if _, err := tx.Exec(ctx, `
            INSERT INTO route_stops (route_id, seq, name, lat, lng, status)
            VALUES ($1, $2, $3, $4, $5, 'pending')`,
			newID, seq, stop.Name, stop.Lat, stop.Lng); err != nil {
			log.Printf("routes: insert the stops failed: %v", err)
			c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("routes: commit failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	created, err := readRoute(ctx, h.db, newID)
	if err != nil {
		log.Printf("routes: read-back failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	publishFrame(h.hub, "route", gin.H{"route": created})

	c.JSON(http.StatusCreated, gin.H{"data": created})
}

// Clear cancels the active route of a vehicle. The vehicle keeps reporting
// positions; it simply stops having a planned path.
func (h *RouteHandler) Clear(c *gin.Context) {
	id, ok := vehicleIDParam(c)
	if !ok {
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	var routeID int64
	err := h.db.QueryRow(ctx, `
        UPDATE vehicle_routes
           SET status = 'cancelled', ended_at = now()
         WHERE vehicle_id = $1 AND status = 'active'
        RETURNING id`, id).Scan(&routeID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		c.JSON(http.StatusNotFound, errorBody("not_found", "this vehicle has no active route"))
		return
	case err != nil:
		log.Printf("routes: cancel failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	publishFrame(h.hub, "route_cleared", gin.H{"route_id": routeID, "vehicle_id": id})

	c.Status(http.StatusNoContent)
}

// Start marks the active route of a vehicle as started: the driver accepted
// the delivery, so the unit begins to drive it.
//
// Only the driver of the unit may start its route; an admin may start any
// route (a dispatcher can send a unit on its way). A unit without an active
// route answers 409 — there is nothing to start.
func (h *RouteHandler) Start(c *gin.Context) {
	id, ok := vehicleIDParam(c)
	if !ok {
		return
	}
	if !h.mayStart(c, id) {
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	var routeID int64
	err := h.db.QueryRow(ctx, `SELECT id FROM vehicle_routes
		WHERE vehicle_id = $1 AND status = 'active'
		ORDER BY id DESC LIMIT 1`, id).Scan(&routeID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		c.JSON(http.StatusConflict, errorBody("no_active_route", "this vehicle has no active route"))
		return
	case err != nil:
		log.Printf("routes: start lookup failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	// The first press wins: the trip began when the driver started it, not
	// when the button was pressed again.
	if _, err := h.db.Exec(ctx, `UPDATE vehicle_routes
		SET started_at = COALESCE(started_at, now())
		WHERE id = $1`, routeID); err != nil {
		log.Printf("routes: start failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	// The unit is on its way: the fleet list and the map show it moving
	// right away, even before the first position comes in.
	if _, err := h.db.Exec(ctx, `UPDATE vehicles SET status = 'moving' WHERE id = $1`, id); err != nil {
		log.Printf("routes: start vehicle update failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	started, err := readRoute(ctx, h.db, routeID)
	if err != nil {
		log.Printf("routes: start read-back failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	publishFrame(h.hub, "route", gin.H{"route": started})

	// The unit is on its way. The operators hear about it, even when their
	// tab is closed: the push goes out in the background, so it never holds
	// this answer up.
	h.notifyRouteStarted(c, started, vehicleLabel(ctx, h.db, id))

	c.JSON(http.StatusOK, gin.H{"data": started})
}

// notifyRouteStarted tells the operators that a unit left for its
// destination.
//
// Only this one moment counts: the route was planned before, and the frame
// of the route is published again and again while the unit drives it. A
// departure is one push, not a stream of them.
//
// The account that pressed the button is left out — a driver does not have
// to be told that their own trip began.
func (h *RouteHandler) notifyRouteStarted(c *gin.Context, route routeView, label string) {
	if h.push == nil {
		return
	}

	actor := int64(0)
	if id, err := strconv.ParseInt(auth.UserID(c), 10, 64); err == nil {
		actor = id
	}

	payload := push.Payload{
		Title: "Unit berangkat",
		Body:  fmt.Sprintf("%s berangkat menuju %s", label, route.Destination.Name),
		Tag:   fmt.Sprintf("route-started-%d", route.ID),
		URL:   "/",
	}

	push.Background(func(ctx context.Context) {
		h.push.NotifyOperators(ctx, actor, payload)
	})
}

// vehicleLabel is how a unit is named in a notification: the code if it has
// one, the name otherwise.
func vehicleLabel(ctx context.Context, pool *db.Pool, vehicleID int64) string {
	var code, name string
	err := pool.QueryRow(ctx, `SELECT code, name FROM vehicles WHERE id = $1`, vehicleID).
		Scan(&code, &name)
	if err != nil {
		log.Printf("routes: read the label of unit %d failed: %v", vehicleID, err)
		return fmt.Sprintf("unit %d", vehicleID)
	}
	if strings.TrimSpace(code) != "" {
		return code
	}
	return name
}

// mayStart reports whether the caller may start the route of this vehicle.
// The driver of the unit may, and an admin may start any route; anybody else
// gets a 403. It writes the error response itself.
func (h *RouteHandler) mayStart(c *gin.Context, id int64) bool {
	if auth.UserRole(c) == auth.RoleAdmin {
		return true
	}

	scope := driverScope(c)
	if scope == nil {
		c.JSON(http.StatusForbidden, errorBody("forbidden", "only the driver of this vehicle can start its route"))
		return false
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	allowed, err := vehicleBelongsToDriver(ctx, h.db, id, *scope)
	if err != nil {
		log.Printf("routes: driver scope for vehicle %d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return false
	}
	if !allowed {
		c.JSON(http.StatusForbidden, errorBody("forbidden", "this vehicle is not assigned to you"))
		return false
	}
	return true
}

// arriveStopSQL marks the stop a unit is serving as arrived.
//
// The stop the unit is serving is the first one that is not finished yet: the
// stops of a route are visited one after the other, so a later stop is never
// reached before the one before it is done. A unit that drives past a later
// stop at the beginning of the trip does not count it.
//
// The statement is one round trip, and it is idempotent: a stop that is
// already arrived keeps its status and its time, so a second report from the
// same place does not detect a second arrival.
//
// $1 the unit, $2 the latitude, $3 the longitude, $4 the radius in metres.
const arriveStopSQL = `WITH route AS (
         SELECT rt.id
           FROM vehicle_routes rt
          WHERE rt.vehicle_id = $1
            AND rt.status = 'active'
            AND rt.started_at IS NOT NULL
          ORDER BY rt.id DESC
          LIMIT 1
     ), stops AS (
         SELECT rs.id, rs.route_id, rs.seq, rs.name, rs.lat, rs.lng, rs.status,
                (SELECT max(x.seq) FROM route_stops x WHERE x.route_id = rs.route_id) AS last_seq
           FROM route_stops rs
           JOIN route r ON r.id = rs.route_id
          WHERE rs.status IN ('pending', 'arrived')
     ), target AS (
         SELECT * FROM stops ORDER BY seq LIMIT 1
     )
     UPDATE route_stops rs
        SET status = 'arrived', arrived_at = now()
       FROM target
      WHERE rs.id = target.id
        AND target.status = 'pending'
        AND ST_Distance(
              ST_SetSRID(ST_MakePoint($3, $2), 4326)::geography,
              ST_SetSRID(ST_MakePoint(target.lng, target.lat), 4326)::geography) <= $4
     RETURNING target.route_id, target.last_seq, target.seq,
               rs.id, rs.name, rs.lat, rs.lng, rs.status,
               rs.arrived_at, rs.completed_at, rs.completion_note`

// checkStopArrival marks the stop a unit is serving as arrived, when the unit
// is close enough to it.
//
// Called from the flow of a position report, so a failure here never fails
// the report: the position is already stored, and the error is logged. The
// next report tries again.
//
// The arrival is not the end of the stop: the driver still has to confirm it.
// That is what the note of the stop is for.
func (h *VehicleHandler) checkStopArrival(ctx context.Context, vehicleID int64, lat, lng float64) {
	var (
		routeID int64
		lastSeq int
		stop    vehicle.RouteStop
	)
	err := h.db.QueryRow(ctx, arriveStopSQL, vehicleID, lat, lng, arrivalRadiusM).Scan(
		&routeID, &lastSeq, &stop.Seq, &stop.ID, &stop.Name, &stop.Lat, &stop.Lng,
		&stop.Status, &stop.ArrivedAt, &stop.CompletedAt, &stop.CompletionNote)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The unit is not near the stop it is serving.
		return
	case err != nil:
		log.Printf("routes: arrival check for vehicle %d failed: %v", vehicleID, err)
		return
	}

	publishFrame(h.hub, "route_stop", gin.H{
		"route_id":   routeID,
		"vehicle_id": vehicleID,
		"stop":       stop,
	})

	// The last stop of a route is the arrival itself: the route closes there
	// and the operators hear about it as "Unit tiba". A second push for the
	// same moment would say the same thing twice.
	if stop.Seq == lastSeq {
		return
	}

	h.notifyStopArrived(ctx, vehicleID, stop)
}

// notifyStopArrived tells the operators that a unit reached a stop of its
// route.
//
// The push goes out in the background, so it never holds up the position
// report that noticed the arrival.
func (h *VehicleHandler) notifyStopArrived(ctx context.Context, vehicleID int64, stop vehicle.RouteStop) {
	if h.push == nil {
		return
	}

	payload := push.Payload{
		Title: "Unit singgah",
		Body:  fmt.Sprintf("%s singgah di %s", vehicleLabel(ctx, h.db, vehicleID), stop.Name),
		Tag:   fmt.Sprintf("route-stop-arrived-%d", stop.ID),
		URL:   "/",
	}

	push.Background(func(ctx context.Context) {
		h.push.NotifyOperators(ctx, 0, payload)
	})
}

// completeLastStop closes the last stop of a route that is finished.
//
// The unit is at the end of the line, so the last stop is served even when
// nobody confirmed it by hand: the route says so, and the note says why.
func (h *VehicleHandler) completeLastStop(ctx context.Context, routeID, vehicleID int64, endedAt time.Time) {
	row := h.db.QueryRow(ctx, `
        UPDATE route_stops rs
           SET status = 'completed', completed_at = $2, completion_note = 'Otomatis: rute selesai'
         WHERE rs.route_id = $1
           AND rs.seq = (SELECT max(seq) FROM route_stops WHERE route_id = $1)
           AND rs.status IN ('pending', 'arrived')
     RETURNING id, seq, name, lat, lng, status, arrived_at, completed_at, completion_note`,
		routeID, endedAt)

	stop, err := scanStop(row)
	if errors.Is(err, pgx.ErrNoRows) {
		// The last stop was already finished by hand.
		return
	}
	if err != nil {
		log.Printf("routes: close the last stop of route %d failed: %v", routeID, err)
		return
	}

	publishFrame(h.hub, "route_stop", gin.H{
		"route_id":   routeID,
		"vehicle_id": vehicleID,
		"stop":       stop,
	})
}

// stopNoteInput is the body of the two endpoints that finish a stop.
//
// The body is optional: a stop may be finished without a note. The note is
// the proof of the visit, so it may be up to maxStopNote characters.
type stopNoteInput struct {
	Note string `json:"note"`
}

// CompleteStop handles POST /api/vehicles/:id/route/stops/:stop_id/complete.
//
// The driver says the stop is done. The proof of the visit is written with
// it: the note, the name of the account that wrote it, the time of the
// server, and the position of the unit at that moment.
//
// Finishing a stop is not the end of the route: the route closes itself when
// the unit reaches the end of the line.
func (h *RouteHandler) CompleteStop(c *gin.Context) {
	h.finishStop(c, vehicle.StopCompleted)
}

// SkipStop handles POST /api/vehicles/:id/route/stops/:stop_id/skip.
//
// A stop that is passed by is finished too: the trip moves on to the next
// stop instead of waiting for a delivery that will not happen.
func (h *RouteHandler) SkipStop(c *gin.Context) {
	h.finishStop(c, vehicle.StopSkipped)
}

// finishStop finishes the stop the unit is serving right now.
//
// The rules are the same for both actions: the stop belongs to the active
// route of this vehicle, it is the stop the unit is serving (the stops are
// visited one after the other), and its status allows the action. A stop
// that is confirmed has to be arrived at first; a stop that is passed by does
// not need an arrival.
func (h *RouteHandler) finishStop(c *gin.Context, status string) {
	id, ok := vehicleIDParam(c)
	if !ok {
		return
	}
	stopID, ok := parseID(c.Param("stop_id"))
	if !ok {
		c.JSON(http.StatusBadRequest, errorBody("invalid_id", "stop id must be a positive integer"))
		return
	}

	var in stopNoteInput
	if err := c.ShouldBindJSON(&in); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be valid JSON"))
		return
	}
	note := strings.TrimSpace(in.Note)
	if fields := validateStopNote(note); len(fields) > 0 {
		c.JSON(http.StatusBadRequest, validationErrorBody(fields))
		return
	}

	if !h.mayFinishStop(c, id) {
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	// The route the stop belongs to: the active one of this vehicle.
	var (
		routeID   int64
		startedAt *time.Time
	)
	err := h.db.QueryRow(ctx, `SELECT rt.id, rt.started_at FROM vehicle_routes rt
		 WHERE rt.vehicle_id = $1 AND rt.status = 'active'
		 ORDER BY rt.id DESC LIMIT 1`, id).Scan(&routeID, &startedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		c.JSON(http.StatusConflict, errorBody("conflict", "this vehicle has no active route"))
		return
	case err != nil:
		log.Printf("routes: read the active route failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	// A driver finishes the stops of a trip that is running. A plan that
	// still waits for its start has no stops to finish yet.
	if auth.UserRole(c) == auth.RoleDriver && startedAt == nil {
		c.JSON(http.StatusConflict, errorBody("conflict", "the trip has not started yet"))
		return
	}

	// The stops of the route, in the visiting order.
	stops, err := h.stopsOfRoute(ctx, routeID)
	if err != nil {
		log.Printf("routes: read the stops of route %d failed: %v", routeID, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	var stop *vehicle.RouteStop
	for i := range stops {
		if stops[i].ID == stopID {
			stop = &stops[i]
			break
		}
	}
	if stop == nil {
		c.JSON(http.StatusNotFound, errorBody("not_found", "stop not found"))
		return
	}

	// The stops are served one after the other: only the stop the unit is
	// serving right now can be finished.
	current := currentStop(stops)
	if current == nil || current.ID != stop.ID {
		c.JSON(http.StatusConflict, errorBody("conflict", "this stop is not the stop the unit is serving right now"))
		return
	}

	// A stop is confirmed after the unit arrived at it. A stop that is passed
	// by does not need an arrival.
	if status == vehicle.StopCompleted && stop.Status != vehicle.StopArrived {
		c.JSON(http.StatusConflict, errorBody("conflict", "the unit has not arrived at this stop yet"))
		return
	}

	updated, err := h.writeStop(ctx, id, routeID, stop.ID, status, note, stopActor(c))
	if err != nil {
		log.Printf("routes: finish the stop %d failed: %v", stopID, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	publishFrame(h.hub, "route_stop", gin.H{
		"route_id":   routeID,
		"vehicle_id": id,
		"stop":       updated,
	})

	c.JSON(http.StatusOK, gin.H{"data": updated})
}

// stopsOfRoute reads the stops of one route, in the visiting order.
func (h *RouteHandler) stopsOfRoute(ctx context.Context, routeID int64) ([]vehicle.RouteStop, error) {
	rows, err := h.db.Query(ctx, `SELECT id, seq, name, lat, lng, status,
			arrived_at, completed_at, completion_note
		  FROM route_stops
		 WHERE route_id = $1
		 ORDER BY seq`, routeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stops := make([]vehicle.RouteStop, 0)
	for rows.Next() {
		stop, err := scanStop(rows)
		if err != nil {
			return nil, err
		}
		stops = append(stops, stop)
	}

	return stops, rows.Err()
}

// writeStop finishes one stop: confirmed by somebody, or passed by.
//
// The proof of the stop is written with it: the note, the account that wrote
// it, and the position of the unit at that moment.
func (h *RouteHandler) writeStop(ctx context.Context, vehicleID, routeID, stopID int64, status, note string, actor *int64) (vehicle.RouteStop, error) {
	row := h.db.QueryRow(ctx, `
        UPDATE route_stops
           SET status = $3,
               completed_at = now(),
               completed_by = $4,
               completion_note = $5,
               completed_lat = (SELECT last_lat FROM vehicles WHERE id = $1),
               completed_lng = (SELECT last_lng FROM vehicles WHERE id = $1)
         WHERE id = $2 AND route_id = $6
     RETURNING id, seq, name, lat, lng, status, arrived_at, completed_at, completion_note`,
		vehicleID, stopID, status, actor, note, routeID)

	return scanStop(row)
}

// mayFinishStop reports whether the signed-in account may finish a stop of
// this vehicle.
//
// An admin may finish the stops of any vehicle — a dispatcher closes a
// delivery by hand — and a driver the stops of their own unit. The staff and
// the viewer read the route, they do not drive it. It writes the error
// response itself, so the caller can simply return.
func (h *RouteHandler) mayFinishStop(c *gin.Context, id int64) bool {
	if auth.UserRole(c) == auth.RoleAdmin {
		return true
	}

	scope := driverScope(c)
	if scope == nil {
		c.JSON(http.StatusForbidden, errorBody("forbidden", "only the driver of this vehicle or an admin can finish a stop"))
		return false
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	allowed, err := vehicleBelongsToDriver(ctx, h.db, id, *scope)
	if err != nil {
		log.Printf("routes: driver scope for vehicle %d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return false
	}
	if !allowed {
		c.JSON(http.StatusForbidden, errorBody("forbidden", "this vehicle is not assigned to you"))
		return false
	}

	return true
}

// actorID is the account that made the request, as the users table knows it.
// A token without a readable subject has no actor: the stop is then finished
// without a name.
func stopActor(c *gin.Context) *int64 {
	id, ok := actorID(c)
	if !ok || id <= 0 {
		return nil
	}

	return &id
}

// completeRouteIfArrived closes the active route once a reported position is
// inside the arrival radius of its destination. Called from UpdatePosition, so
// a route ends where the vehicle actually stops, not on a timer.
//
// The end of the drawn line counts as an arrival too. The routing engine snaps
// the requested destination to the closest road it knows, which can be a few
// hundred metres away from the point the operator picked; the unit drives to
// the end of that line, so the line is where the delivery really ends.
//
// Arriving at the destination only counts once the unit is nearly done with
// the line. A multi-stop route can have a destination close to the origin —
// the tour may even lead back there — and without that guard the route would
// close on the very first position report.
func (h *VehicleHandler) completeRouteIfArrived(ctx context.Context, vehicleID int64, lat, lng float64) {
	var (
		routeID     int64
		destName    string
		vehicleCode string
		vehicleName string
		endedAt     time.Time
	)
	// The closed route is read back together with the unit it belongs to:
	// the notification names both, and the statement does it in one round
	// trip.
	err := h.db.QueryRow(ctx, `
        WITH closed AS (
            UPDATE vehicle_routes rt
               SET status = 'completed', ended_at = now()
             WHERE rt.vehicle_id = $1 AND rt.status = 'active'
               AND (
                    ST_Distance(
                        ST_SetSRID(ST_MakePoint($3, $2), 4326)::geography,
                        ST_Endpoint(rt.geom)::geography
                    ) <= $4
                    OR (
                        ST_Distance(
                            ST_SetSRID(ST_MakePoint($3, $2), 4326)::geography,
                            ST_SetSRID(ST_MakePoint(rt.dest_lng, rt.dest_lat), 4326)::geography
                        ) <= $4
                        AND ST_LineLocatePoint(
                            rt.geom, ST_SetSRID(ST_MakePoint($3, $2), 4326)
                        ) >= 0.95
                    )
               )
            RETURNING rt.id, rt.dest_name, rt.vehicle_id, rt.ended_at
        )
        SELECT closed.id, closed.dest_name, v.code, v.name, closed.ended_at
          FROM closed
          JOIN vehicles v ON v.id = closed.vehicle_id`,
		vehicleID, lat, lng, arrivalRadiusM).Scan(&routeID, &destName, &vehicleCode, &vehicleName, &endedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return
	case err != nil:
		log.Printf("routes: auto-complete failed: %v", err)
		return
	}

	publishFrame(h.hub, "route_completed", gin.H{"route_id": routeID, "vehicle_id": vehicleID})

	// The last stop of the route is served by the arrival itself: the unit is
	// at the end of the line, so the delivery is over even when nobody
	// confirmed that stop by hand.
	h.completeLastStop(ctx, routeID, vehicleID, endedAt)

	// The unit arrived. The operators hear about it, even when their tab is
	// closed.
	h.notifyRouteCompleted(routeID, vehicleCode, vehicleName, destName)
}

// notifyRouteCompleted tells the operators that a unit reached its
// destination.
//
// This is the one moment the route really ends, so this is the only moment
// the arrival is announced.
func (h *VehicleHandler) notifyRouteCompleted(routeID int64, code, name, destination string) {
	if h.push == nil {
		return
	}

	label := strings.TrimSpace(code)
	if label == "" {
		label = strings.TrimSpace(name)
	}

	payload := push.Payload{
		Title: "Unit tiba",
		Body:  fmt.Sprintf("%s tiba di %s", label, destination),
		Tag:   fmt.Sprintf("route-completed-%d", routeID),
		URL:   "/",
	}

	push.Background(func(ctx context.Context) {
		h.push.NotifyOperators(ctx, 0, payload)
	})
}

// followRoute closes the delivery when the unit arrives, otherwise re-publishes
// the route so the map's progress, ETA and remaining distance move with the
// truck. Without this the line would only change when the route itself is
// edited, which makes a live delivery look frozen.
func (h *VehicleHandler) followRoute(ctx context.Context, vehicleID int64, lat, lng float64) {
	// The stop the unit is serving is marked as arrived before the route is
	// closed: the arrival at the last stop is the arrival of the route.
	h.checkStopArrival(ctx, vehicleID, lat, lng)
	h.completeRouteIfArrived(ctx, vehicleID, lat, lng)

	view, err := scanRoute(h.db.QueryRow(ctx,
		routeCols+" WHERE rt.vehicle_id = $1 AND rt.status = 'active' ORDER BY rt.id DESC LIMIT 1", vehicleID))
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("routes: progress reload failed: %v", err)
		}
		return
	}

	// The stops travel with the route, so the clients see the status of
	// every stop without asking for it.
	if err := loadStops(ctx, h.db, []*vehicle.Route{&view}); err != nil {
		log.Printf("routes: read the stops of route %d failed: %v", view.ID, err)
		return
	}

	publishFrame(h.hub, "route", gin.H{"route": view})
}

func (h *RouteHandler) queryRoutes(ctx context.Context, sql string, args ...any) ([]routeView, error) {
	rows, err := h.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	routes := make([]routeView, 0)
	for rows.Next() {
		route, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// The stops of every route in the answer, in one query.
	refs := make([]*vehicle.Route, 0, len(routes))
	for i := range routes {
		refs = append(refs, &routes[i])
	}
	if err := loadStops(ctx, h.db, refs); err != nil {
		return nil, err
	}

	return routes, nil
}

// readRoute reads one route with its stops.
func readRoute(ctx context.Context, pool *db.Pool, id int64) (routeView, error) {
	route, err := scanRoute(pool.QueryRow(ctx, routeCols+" WHERE rt.id = $1", id))
	if err != nil {
		return routeView{}, err
	}
	if err := loadStops(ctx, pool, []*vehicle.Route{&route}); err != nil {
		return routeView{}, err
	}

	return route, nil
}

// vehicleIDParam parses :id and writes the error response itself, so each
// handler starts with `if !ok { return }`.
func vehicleIDParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, errorBody("invalid_id", "vehicle id must be a positive integer"))
		return 0, false
	}

	return id, true
}

// lineStringGeoJSON turns the planned points into the GeoJSON PostGIS parses.
func lineStringGeoJSON(points []routing.Point) ([]byte, error) {
	coords := make([][2]float64, 0, len(points))
	for _, p := range points {
		coords = append(coords, [2]float64{p.Lng, p.Lat})
	}

	return json.Marshal(map[string]any{"type": "LineString", "coordinates": coords})
}

// orderStops puts the stops in the visiting order the engine worked out.
//
// An order that does not cover every stop is ignored and the stops are kept
// in the order they were handed over: a plan that does not line up with the
// stops is not worth storing.
func orderStops(stops []routeStop, order []int) []routeStop {
	if len(order) != len(stops) {
		return stops
	}

	out := make([]routeStop, 0, len(stops))
	for _, idx := range order {
		if idx < 0 || idx >= len(stops) {
			return stops
		}
		out = append(out, stops[idx])
	}

	return out
}
