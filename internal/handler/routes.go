package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"entity-management/internal/db"
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
}

func NewRouteHandler(pool *db.Pool, hub *ws.Hub) *RouteHandler {
	return &RouteHandler{db: pool, hub: hub, router: routing.NewClient()}
}

// arrivalRadiusM is how close a reported position must be to the destination
// before the route closes itself, so the map stops drawing a path that has
// already been driven.
const arrivalRadiusM = 150.0

// routeStop is one end of a route.
type routeStop = vehicle.RouteStop

// setRouteInput is the body of POST /api/vehicles/:id/route.
type setRouteInput struct {
	Origin      routeStop `json:"origin"`
	Destination routeStop `json:"destination"`
}

func (in *setRouteInput) normalize() {
	in.Origin.Name = strings.TrimSpace(in.Origin.Name)
	in.Destination.Name = strings.TrimSpace(in.Destination.Name)
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
	check("destination", in.Destination)

	if len(fields) == 0 {
		gap := routing.DistanceMeters(
			routing.Point{Lat: in.Origin.Lat, Lng: in.Origin.Lng},
			routing.Point{Lat: in.Destination.Lat, Lng: in.Destination.Lng},
		)
		if gap < 10 {
			fields["destination"] = "destination must differ from origin"
		}
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
       rt.source, rt.status, rt.created_at, rt.ended_at,
       ` + routeProgressSQL + `
  FROM vehicle_routes rt
  JOIN vehicles v ON v.id = rt.vehicle_id`

// routeView is one route as the client sees it. The geometry is PostGIS'
// GeoJSON, passed through untouched.
type routeView = vehicle.Route

// scanRoute reads one route row.
func scanRoute(row rowScanner) (routeView, error) {
	var (
		v       routeView
		geojson []byte
		run     float64
	)
	if err := row.Scan(&v.ID, &v.VehicleID, &v.Origin.Name, &v.Destination.Name,
		&v.Origin.Lat, &v.Origin.Lng, &v.Destination.Lat, &v.Destination.Lng,
		&geojson, &v.DistanceM, &v.DurationS, &v.Source, &v.Status,
		&v.CreatedAt, &v.EndedAt, &run); err != nil {
		return routeView{}, err
	}

	v.Geometry = json.RawMessage(geojson)
	v.Progress = round4(run)
	v.RemainingM = round2(v.DistanceM * (1 - v.Progress))

	return v, nil
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
	scope := driverScope(c)
	if scope == nil {
		return true
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
	to := routing.Point{Lat: in.Destination.Lat, Lng: in.Destination.Lng}

	// The engine has its own deadline: a slow third party must not hold the
	// request open, the straight-line fallback is always available.
	planCtx, cancelPlan := context.WithTimeout(c.Request.Context(), 8*time.Second)
	plan, planErr := h.router.Plan(planCtx, from, to)
	cancelPlan()
	if planErr != nil {
		log.Printf("routes: engine fallback to straight line: %v", planErr)
	}

	// A line that PostGIS refuses to store would fail the whole request with
	// an opaque 500, so the points are cleaned up first: a repeated point is
	// dropped, and a line that is left with fewer than two distinct points
	// (the engine snapped both ends to the same road node) becomes the
	// straight connection between the two stops.
	points := routing.Dedupe(plan.Points)
	if len(points) < 2 {
		log.Printf("routes: degenerate geometry, using the straight line")
		fallback := routing.DirectRoute(from, to)
		points = fallback.Points
		plan.DistanceM = fallback.DistanceM
		plan.DurationS = fallback.DurationS
		plan.Source = routing.SourceDirect
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
                dest_lat, dest_lng, geom, distance_m, duration_s, source)
        VALUES ($1, $2, $3, $4, $5, $6, $7,
                ST_SetSRID(ST_GeomFromGeoJSON($8), 4326), $9, $10, $11)
        RETURNING id`,
		id, in.Origin.Name, in.Destination.Name,
		in.Origin.Lat, in.Origin.Lng, in.Destination.Lat, in.Destination.Lng,
		string(line), plan.DistanceM, plan.DurationS, plan.Source).Scan(&newID)
	if err != nil {
		log.Printf("routes: insert failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("routes: commit failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	created, err := scanRoute(h.db.QueryRow(ctx, routeCols+" WHERE rt.id = $1", newID))
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

// completeRouteIfArrived closes the active route once a reported position is
// inside the arrival radius of its destination. Called from UpdatePosition, so
// a route ends where the vehicle actually stops, not on a timer.
//
// The end of the drawn line counts as an arrival too. The routing engine snaps
// the requested destination to the closest road it knows, which can be a few
// hundred metres away from the point the operator picked; the unit drives to
// the end of that line, so the line is where the delivery really ends.
func (h *VehicleHandler) completeRouteIfArrived(ctx context.Context, vehicleID int64, lat, lng float64) {
	var routeID int64
	err := h.db.QueryRow(ctx, `
        UPDATE vehicle_routes rt
           SET status = 'completed', ended_at = now()
         WHERE rt.vehicle_id = $1 AND rt.status = 'active'
           AND LEAST(
                ST_Distance(
                    ST_SetSRID(ST_MakePoint($3, $2), 4326)::geography,
                    ST_SetSRID(ST_MakePoint(rt.dest_lng, rt.dest_lat), 4326)::geography
                ),
                ST_Distance(
                    ST_SetSRID(ST_MakePoint($3, $2), 4326)::geography,
                    ST_Endpoint(rt.geom)::geography
                )
           ) <= $4
        RETURNING rt.id`, vehicleID, lat, lng, arrivalRadiusM).Scan(&routeID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return
	case err != nil:
		log.Printf("routes: auto-complete failed: %v", err)
		return
	}

	publishFrame(h.hub, "route_completed", gin.H{"route_id": routeID, "vehicle_id": vehicleID})
}

// followRoute closes the delivery when the unit arrives, otherwise re-publishes
// the route so the map's progress, ETA and remaining distance move with the
// truck. Without this the line would only change when the route itself is
// edited, which makes a live delivery look frozen.
func (h *VehicleHandler) followRoute(ctx context.Context, vehicleID int64, lat, lng float64) {
	h.completeRouteIfArrived(ctx, vehicleID, lat, lng)

	row := h.db.QueryRow(ctx,
		routeCols+" WHERE rt.vehicle_id = $1 AND rt.status = 'active' ORDER BY rt.id DESC LIMIT 1", vehicleID)
	view, err := scanRoute(row)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("routes: progress reload failed: %v", err)
		}
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

	return routes, rows.Err()
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
