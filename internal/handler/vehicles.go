package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"entity-management/internal/auth"
	"entity-management/internal/db"
	"entity-management/internal/push"
	"entity-management/internal/vehicle"
	"entity-management/internal/ws"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// VehicleHandler serves live tracking: REST for the fleet and for reported
// positions, plus a WebSocket stream that pushes every accepted position to
// the browsers that subscribed.
type VehicleHandler struct {
	db  *db.Pool
	hub *ws.Hub
	// offlineAfter is how long a unit may stay silent before it counts as
	// offline. Zero turns the check off.
	offlineAfter time.Duration
	// idleAfter is how long a unit may stand still — while it keeps
	// reporting — before the server calls it idle. Zero turns the server-side
	// idle detection off.
	idleAfter time.Duration
	// idleRadiusM is how far a unit may drift from its anchor and still count
	// as standing still, in meters.
	idleRadiusM float64
	// idleMaxSpeedKmh is the speed above which a report counts as movement.
	idleMaxSpeedKmh float64
	// push tells the operators that a unit arrived.
	push *push.Service
}

func NewVehicleHandler(pool *db.Pool, hub *ws.Hub, pushService *push.Service) *VehicleHandler {
	return &VehicleHandler{db: pool, hub: hub, push: pushService}
}

// SetOfflineAfter turns the automatic offline detection on: a unit that has
// not reported for that long counts as offline. Zero turns it off again.
func (h *VehicleHandler) SetOfflineAfter(d time.Duration) {
	h.offlineAfter = d
}

// SetIdleDetection turns the automatic idle detection on: a unit that keeps
// reporting the same spot for that long counts as idle.
//
// The radius is how far the unit may drift from its anchor and still count as
// standing still, the speed limit is the speed above which a report always
// counts as movement.
func (h *VehicleHandler) SetIdleDetection(after time.Duration, radiusM, maxSpeedKmh float64) {
	h.idleAfter = after
	h.idleRadiusM = radiusM
	h.idleMaxSpeedKmh = maxSpeedKmh
}

func (h *VehicleHandler) ctxWithTimeout(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 10*time.Second)
}

const vehicleCols = `SELECT v.id, v.code, v.name, v.kind, v.status, v.driver_id,
       v.last_lat, v.last_lng, v.last_speed_kmh, v.last_seen_at,
       v.last_heading, v.route_color,
       ` + vehicleIdleCols

const vehicleFrom = ` FROM vehicles v` + vehicleIdleJoins

// vehicleIdleCols are the columns that say how long a unit has been standing
// still and which work area it stands in.
//
// The idle session is the open row of idle_events. A unit that reports the
// status idle before the server opened a session for it (the reporter decides
// that first) falls back to the anchor clock, so the badge shows the same
// number either way: the moment the unit started standing still.
const vehicleIdleCols = `COALESCE(ie.started_at, CASE WHEN v.status = 'idle' THEN v.stationary_since END),
       EXTRACT(EPOCH FROM (now() - COALESCE(ie.started_at, CASE WHEN v.status = 'idle' THEN v.stationary_since END)))::double precision,
       cz.zone_id, COALESCE(z.name, ''), cz.created_at,
       EXTRACT(EPOCH FROM (now() - cz.created_at))::double precision`

// vehicleIdleJoins add the idle session and the work area of a unit to a
// vehicle query.
//
// Two lateral joins, both of them cheap. The first one finds the idle session
// that is still running. The second one finds the zone the unit stands in:
// the last crossing into a zone that was never left again, and the zone has
// to hold the last known position of the unit — a unit that walked out of a
// zone without the exit being recorded is not "in" that zone.
const vehicleIdleJoins = ` LEFT JOIN LATERAL (
         SELECT e.started_at
           FROM idle_events e
          WHERE e.vehicle_id = v.id
            AND e.ended_at IS NULL
          ORDER BY e.started_at DESC, e.id DESC
          LIMIT 1
     ) ie ON true
     LEFT JOIN LATERAL (
         SELECT g.zone_id, g.created_at
           FROM geofence_events g
           JOIN zones zz ON zz.id = g.zone_id
          WHERE g.vehicle_id = v.id
            AND g.event_type = 'ENTER'
            AND v.last_lat IS NOT NULL
            AND v.last_lng IS NOT NULL
            AND ST_Covers(zz.geom, ST_SetSRID(ST_MakePoint(v.last_lng, v.last_lat), 4326))
            AND NOT EXISTS (
                  SELECT 1
                    FROM geofence_events x
                   WHERE x.vehicle_id = g.vehicle_id
                     AND x.zone_id = g.zone_id
                     AND x.event_type = 'EXIT'
                     AND (x.created_at > g.created_at
                          OR (x.created_at = g.created_at AND x.id > g.id))
                )
          ORDER BY g.created_at DESC, g.id DESC
          LIMIT 1
     ) cz ON true
     LEFT JOIN zones z ON z.id = cz.zone_id`

// vehicleColsWithRoute adds the delivery route every unit is following to
// the fleet list, so the map can draw the line and the fleet card can say
// where the unit is heading without a second request.
//
// The route is picked with a lateral join: one unit has at most one active
// route, and the join keeps the fleet list a single round trip.
const vehicleColsWithRoute = `SELECT v.id, v.code, v.name, v.kind, v.status, v.driver_id,
       v.last_lat, v.last_lng, v.last_speed_kmh, v.last_seen_at,
       v.last_heading, v.route_color,
       ` + vehicleIdleCols + `,
       rt.id, rt.origin_name, rt.origin_lat, rt.origin_lng,
       rt.dest_name, rt.dest_lat, rt.dest_lng,
       COALESCE(ST_AsGeoJSON(rt.geom, 6)::json, '{}'::json),
       rt.distance_m, rt.duration_s, rt.source, rt.status, rt.created_at, rt.progress,
       rt.started_at, rt.ended_at,
       (v.driver_id IS NOT NULL) AS driver_assigned
  FROM vehicles v
  LEFT JOIN LATERAL (
       SELECT rt.id, rt.origin_name, rt.origin_lat, rt.origin_lng,
              rt.dest_name, rt.dest_lat, rt.dest_lng, rt.geom, rt.distance_m,
              rt.duration_s, rt.source, rt.status, rt.created_at,
              rt.started_at, rt.ended_at,
              ` + routeProgressSQL + ` AS progress
         FROM vehicle_routes rt
        WHERE rt.vehicle_id = v.id AND rt.status = 'active'
        ORDER BY rt.id DESC
        LIMIT 1
  ) rt ON true` + vehicleIdleJoins

// rowScanner is satisfied by both pgx.Row and pgx.Rows, so one scan function
// serves the list, the snapshot and the post-update read-back.
type rowScanner interface {
	Scan(dest ...any) error
}

// idleScan holds the places the idle columns of a vehicle row are scanned
// into.
type idleScan struct {
	since     *time.Time
	duration  *float64
	zoneID    *int64
	zoneName  string
	zoneSince *time.Time
	dwellS    *float64
}

// apply fills the idle fields of a vehicle with what was scanned.
func (s *idleScan) apply(v *vehicle.Vehicle) {
	v.IdleSince = s.since
	v.IdleDurationS = s.duration
	if s.zoneID == nil || s.zoneSince == nil {
		return
	}
	dwell := 0.0
	if s.dwellS != nil {
		dwell = *s.dwellS
	}
	v.CurrentZone = &vehicle.VehicleZone{
		ZoneID:   *s.zoneID,
		ZoneName: s.zoneName,
		Since:    *s.zoneSince,
		DwellS:   dwell,
	}
}

// vehicleDest returns the destinations of the columns every vehicle row
// carries, in the order the queries return them: the fleet data first, then
// the idle columns.
func vehicleDest(v *vehicle.Vehicle, idle *idleScan) []any {
	return []any{&v.ID, &v.Code, &v.Name, &v.Kind, &v.Status, &v.DriverID,
		&v.Lat, &v.Lng, &v.SpeedKmh, &v.LastSeenAt, &v.Heading, &v.RouteColor,
		&idle.since, &idle.duration, &idle.zoneID, &idle.zoneName,
		&idle.zoneSince, &idle.dwellS}
}

// scanVehicle reads one fleet row. extra collects the additional columns of
// queries that return more than the fleet itself (the position update adds the
// coordinates the vehicle had before it).
func scanVehicle(row rowScanner, extra ...any) (vehicle.Vehicle, error) {
	var v vehicle.Vehicle
	var idle idleScan

	dest := vehicleDest(&v, &idle)
	dest = append(dest, extra...)
	if err := row.Scan(dest...); err != nil {
		return vehicle.Vehicle{}, err
	}
	idle.apply(&v)

	return v, nil
}

// scanVehicleWithRoute reads a fleet row that also carries the delivery route
// the unit is following. The route columns are nullable: a unit without a
// route leaves them empty and the vehicle travels on without one.
func scanVehicleWithRoute(row rowScanner) (vehicle.Vehicle, error) {
	var (
		v          vehicle.Vehicle
		idle       idleScan
		routeID    *int64
		originName *string
		originLat  *float64
		originLng  *float64
		destName   *string
		destLat    *float64
		destLng    *float64
		geojson    []byte
		distanceM  *float64
		durationS  *float64
		source     *string
		status     *string
		createdAt  *time.Time
		progress   *float64
		startedAt  *time.Time
		endedAt    *time.Time
		driverSet  *bool
	)

	dest := vehicleDest(&v, &idle)
	dest = append(dest, &routeID, &originName, &originLat, &originLng,
		&destName, &destLat, &destLng,
		&geojson, &distanceM, &durationS, &source, &status, &createdAt, &progress,
		&startedAt, &endedAt, &driverSet)
	if err := row.Scan(dest...); err != nil {
		return vehicle.Vehicle{}, err
	}
	idle.apply(&v)

	if routeID == nil {
		return v, nil
	}

	route := &vehicle.Route{
		ID:          *routeID,
		VehicleID:   v.ID,
		Origin:      vehicle.RoutePlace{Name: *originName, Lat: *originLat, Lng: *originLng},
		Destination: vehicle.RoutePlace{Name: *destName, Lat: *destLat, Lng: *destLng},
		Geometry:    json.RawMessage(geojson),
		DistanceM:   *distanceM,
		DurationS:   *durationS,
		Source:      *source,
		Status:      *status,
		CreatedAt:   *createdAt,
		StartedAt:   startedAt,
		EndedAt:     endedAt,
	}
	if driverSet != nil {
		route.DriverAssigned = *driverSet
	}
	if progress != nil {
		route.Progress = round4(*progress)
		route.RemainingM = round2(route.DistanceM * (1 - route.Progress))
	}
	v.ActiveRoute = route

	return v, nil
}

// fillRouteStops reads the stops of the active routes of the given units, in
// one query.
//
// The stops of a route are the steps of the trip, each one with a status of
// its own (migration 028), so the fleet list carries them too.
func fillRouteStops(ctx context.Context, pool *db.Pool, vehicles []vehicle.Vehicle) error {
	routes := make([]*vehicle.Route, 0, len(vehicles))
	for i := range vehicles {
		if vehicles[i].ActiveRoute != nil {
			routes = append(routes, vehicles[i].ActiveRoute)
		}
	}

	return loadStops(ctx, pool, routes)
}

// driverScope returns the signed-in driver's user id, or nil for the roles that
// see the whole fleet. An unparsable subject yields id 0, which matches no
// vehicle: a malformed token must not widen the view.
func driverScope(c *gin.Context) *int64 {
	if auth.UserRole(c) != auth.RoleDriver {
		return nil
	}
	id, err := strconv.ParseInt(auth.UserID(c), 10, 64)
	if err != nil {
		id = 0
	}
	return &id
}

// fleetWhere turns a scope into the WHERE clause that limits a fleet query.
func fleetWhere(driverID *int64) (string, []any) {
	if driverID == nil {
		return "", nil
	}
	return " WHERE v.driver_id = $1", []any{*driverID}
}

// List returns the fleet together with the latest known position of each unit
// and the delivery route it is following. A driver only receives the vehicles
// assigned to them.
func (h *VehicleHandler) List(c *gin.Context) {
	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	where, args := fleetWhere(driverScope(c))
	rows, err := h.db.Query(ctx, vehicleColsWithRoute+where+" ORDER BY v.id", args...)
	if err != nil {
		log.Printf("vehicles: list query failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	defer rows.Close()

	vehicles := make([]vehicle.Vehicle, 0)
	for rows.Next() {
		v, err := scanVehicleWithRoute(rows)
		if err != nil {
			log.Printf("vehicles: list scan failed: %v", err)
			c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
			return
		}
		vehicles = append(vehicles, v)
	}
	if err := rows.Err(); err != nil {
		log.Printf("vehicles: list read failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	// The stops of the active routes, in one query.
	if err := fillRouteStops(ctx, h.db, vehicles); err != nil {
		log.Printf("vehicles: read the stops of the routes failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{"vehicles": vehicles, "count": len(vehicles)}})
}

// Create adds a vehicle to the tracked fleet. A vehicle starts without a
// position: the first reported position fills it in.
func (h *VehicleHandler) Create(c *gin.Context) {
	var in vehicle.CreateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be valid JSON"))
		return
	}

	in.Normalize()
	if fields := in.Validate(); len(fields) > 0 {
		c.JSON(http.StatusBadRequest, validationErrorBody(fields))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	var id int64
	err := h.db.QueryRow(ctx,
		`INSERT INTO vehicles (code, name, kind, driver_id, route_color) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		in.Code, in.Name, in.Kind, in.DriverID, in.RouteColor).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				c.JSON(http.StatusConflict, errorBody("duplicate_code", "a vehicle with that code already exists"))
				return
			case "23503":
				c.JSON(http.StatusBadRequest, validationErrorBody(map[string]string{"driver_id": "unknown user"}))
				return
			}
		}
		log.Printf("vehicles: insert failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	created, err := scanVehicle(h.db.QueryRow(ctx, vehicleCols+vehicleFrom+" WHERE v.id = $1", id))
	if err != nil {
		log.Printf("vehicles: read-back failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	h.publish("vehicle_updated", gin.H{"vehicle": created})

	c.JSON(http.StatusCreated, gin.H{"data": created})
}

// Update edits the details of one vehicle. The fleet row is locked while it
// changes, so a concurrent position report cannot be lost.
func (h *VehicleHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, errorBody("invalid_id", "vehicle id must be a positive integer"))
		return
	}

	var in vehicle.UpdateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be valid JSON"))
		return
	}

	in.Normalize()
	if fields := in.Validate(); len(fields) > 0 {
		c.JSON(http.StatusBadRequest, validationErrorBody(fields))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	updated, err := scanVehicle(h.db.QueryRow(ctx, updateVehicleSQL, id, in.Code, in.Name, in.Kind, in.DriverID, in.RouteColor))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		c.JSON(http.StatusNotFound, errorBody("not_found", "vehicle not found"))
		return
	case err != nil:
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				c.JSON(http.StatusConflict, errorBody("duplicate_code", "a vehicle with that code already exists"))
				return
			case "23503":
				c.JSON(http.StatusBadRequest, validationErrorBody(map[string]string{"driver_id": "unknown user"}))
				return
			}
		}
		log.Printf("vehicles: update failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	h.publish("vehicle_updated", gin.H{"vehicle": updated})

	c.JSON(http.StatusOK, gin.H{"data": updated})
}

// updateVehicleSQL edits the fleet row and reads it back in one round trip.
const updateVehicleSQL = `WITH updated AS (
         UPDATE vehicles
            SET code = $2, name = $3, kind = $4, driver_id = $5, route_color = $6, updated_at = now()
          WHERE id = $1
         RETURNING id, code, name, kind, status, driver_id,
                   last_lat, last_lng, last_speed_kmh, last_seen_at,
                   last_heading, route_color
     )
` + vehicleCols + ` FROM updated v` + vehicleIdleJoins

// Delete removes a vehicle from the fleet. Its positions and routes go with
// it, and the removal is pushed so every open map drops the unit at once.
func (h *VehicleHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, errorBody("invalid_id", "vehicle id must be a positive integer"))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	tag, err := h.db.Exec(ctx, `DELETE FROM vehicles WHERE id = $1`, id)
	if err != nil {
		log.Printf("vehicles: delete failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, errorBody("not_found", "vehicle not found"))
		return
	}

	h.publish("vehicle_deleted", gin.H{"id": id})

	c.Status(http.StatusNoContent)
}

// UpdatePosition records one reported position. The history insert and the
// fleet update happen in a single statement, so the map and the log can never
// disagree, and every accepted position is pushed to the subscribers.
func (h *VehicleHandler) UpdatePosition(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, errorBody("invalid_id", "vehicle id must be a positive integer"))
		return
	}

	var in vehicle.PositionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be valid JSON"))
		return
	}

	in.Normalize()
	if fields := in.Validate(); len(fields) > 0 {
		c.JSON(http.StatusBadRequest, validationErrorBody(fields))
		return
	}

	speed := 0.0
	if in.SpeedKmh != nil {
		speed = *in.SpeedKmh
	}

	// A report without a heading keeps the one the unit had: the direction a
	// vehicle faces is not always known, but it is not lost either.
	var heading *float64
	if in.Heading != nil {
		heading = in.Heading
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	// The query also returns the coordinates the vehicle had before this
	// report; the geofence check compares them with the new ones. It also
	// returns what the idle detection needs: the anchor of the unit and the
	// distance between the anchor and the new position.
	var (
		prevLat, prevLng *float64
		anchorSince      *time.Time
		anchorDistanceM  *float64
	)
	updated, err := scanVehicle(h.db.QueryRow(ctx, reportPositionSQL, id, *in.Lat, *in.Lng, speed, in.Status, heading),
		&prevLat, &prevLng, &anchorSince, &anchorDistanceM)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		c.JSON(http.StatusNotFound, errorBody("not_found", "vehicle not found"))
		return
	case err != nil:
		log.Printf("vehicles: position update failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	// The anchor of the unit is kept up to date right after the report was
	// stored: the server decides on its own when a unit is standing still.
	//
	// A failure here never fails the report — the position is already
	// stored, and dropping it because the anchor could not be written would
	// lose data. The error is logged instead, and the unit is picked up by
	// the next report.
	if status, err := h.trackStationaryAnchor(ctx, id, *in.Lat, *in.Lng, speed, anchorSince, anchorDistanceM); err != nil {
		log.Printf("vehicles: anchor tracking for vehicle %d failed: %v", id, err)
	} else {
		updated.Status = status
		if status != vehicle.StatusIdle {
			// The unit moved again: the idle session is over, so the badge
			// of the fleet card has nothing left to count.
			updated.IdleSince = nil
			updated.IdleDurationS = nil
		}
	}

	h.publish("position", gin.H{"vehicle": updated})

	// Every zone boundary the unit crossed on its way here becomes its own
	// frame, so the frontend can toast it right away and the stored history
	// matches what the map just showed.
	for _, ev := range h.detectGeofence(ctx, id, prevLat, prevLng, *in.Lat, *in.Lng) {
		h.publish("geofence", gin.H{
			"id":           ev.ID,
			"event":        ev.EventType,
			"vehicle_id":   ev.VehicleID,
			"vehicle_name": updated.Name,
			"driver_id":    updated.DriverID,
			"zone_id":      ev.ZoneID,
			"zone_name":    ev.ZoneName,
			"timestamp":    ev.CreatedAt,
		})
	}

	// A vehicle that reaches its destination closes its own route.
	h.followRoute(ctx, id, *in.Lat, *in.Lng)

	c.JSON(http.StatusOK, gin.H{"data": updated})
}

// reportPositionSQL appends to the position log and refreshes the fleet row in
// one round trip. The `logged` CTE is data-modifying, so PostgreSQL runs it
// even though the outer SELECT only reads `updated`.
//
// `previous` captures the coordinates the vehicle had before this report. All
// CTEs of a statement see the same snapshot, so it holds the old position even
// though `updated` overwrites it — that is what the geofence check compares
// the new position against.
//
// The heading is written with COALESCE: a report that carries no direction
// keeps the last known one instead of clearing it.
//
// The statement also hands back what the idle detection needs to know: where
// the anchor of the unit stands, how far the new position is from it, and
// whether an idle session is still running.
const reportPositionSQL = `WITH previous AS (
         SELECT last_lat AS prev_lat, last_lng AS prev_lng
           FROM vehicles
          WHERE id = $1
     ), updated AS (
         UPDATE vehicles
            SET last_lat = $2, last_lng = $3, last_speed_kmh = $4, status = $5,
                last_heading = COALESCE($6, last_heading),
                last_seen_at = now(), updated_at = now()
          WHERE id = $1
         RETURNING id, code, name, kind, status, driver_id,
                   last_lat, last_lng, last_speed_kmh, last_seen_at,
                   last_heading, route_color,
                   stationary_since, stationary_anchor_lat, stationary_anchor_lng
     ), logged AS (
         INSERT INTO vehicle_positions (vehicle_id, lat, lng, speed_kmh, heading)
         SELECT $1, $2, $3, $4, $6 FROM updated
         RETURNING id
     )
     SELECT v.id, v.code, v.name, v.kind, v.status, v.driver_id,
            v.last_lat, v.last_lng, v.last_speed_kmh, v.last_seen_at,
            v.last_heading, v.route_color,
            ` + vehicleIdleCols + `,
            p.prev_lat, p.prev_lng,
            v.stationary_since,
            ` + anchorDistanceSQL + `
       FROM updated v
       CROSS JOIN previous p` + vehicleIdleJoins

// anchorDistanceSQL measures how far the new position of a unit is from its
// anchor, on the sphere and not on the flat map. It is null while the unit
// has no anchor yet.
const anchorDistanceSQL = `ST_Distance(
        ST_SetSRID(ST_MakePoint(v.stationary_anchor_lng, v.stationary_anchor_lat), 4326)::geography,
        ST_SetSRID(ST_MakePoint(v.last_lng, v.last_lat), 4326)::geography)`

// applyAnchorSQL writes the anchor of a unit and closes its idle session.
//
// $1 vehicle id, $2 whether the anchor moves, $3 the new latitude, $4 the new
// longitude.
//
// The decision itself is not made here: the distance was measured by the
// report and the rule is vehicle.ShouldMoveAnchor, a pure function. This
// statement only writes what the decision came to.
//
// The clock of the session is the database clock: `now()` is the same instant
// in every row of one statement.
//
// The status follows the report, with one exception: a unit that stands still
// while an idle session is running stays idle, even when the reporter calls
// it moving. The reporter does not know how long the unit has been standing
// there — the server does.
const applyAnchorSQL = `WITH closed AS (
         UPDATE idle_events e
            SET ended_at = now()
          WHERE e.vehicle_id = $1
            AND e.ended_at IS NULL
            AND $2
         RETURNING e.id
    )
    UPDATE vehicles v
       SET stationary_anchor_lat = CASE WHEN $2 THEN $3 ELSE v.stationary_anchor_lat END,
           stationary_anchor_lng = CASE WHEN $2 THEN $4 ELSE v.stationary_anchor_lng END,
           stationary_since   = CASE WHEN $2 THEN now() ELSE v.stationary_since END,
           status = CASE
                      WHEN NOT $2
                       AND v.status <> 'offline'
                       AND EXISTS (
                             SELECT 1 FROM idle_events e
                              WHERE e.vehicle_id = v.id AND e.ended_at IS NULL
                           )
                      THEN 'idle'
                      ELSE v.status
                    END,
           updated_at = now()
     WHERE v.id = $1
    RETURNING v.status`

// publish frames a payload and fans it out to every subscriber.
func (h *VehicleHandler) publish(kind string, payload gin.H) {
	publishFrame(h.hub, kind, payload)
}

// publishFrame frames a payload and broadcasts it. Shared with the route
// handler so positions and route changes travel over the same feed.
func publishFrame(hub *ws.Hub, kind string, payload gin.H) {
	payload["type"] = kind
	payload["at"] = time.Now().UTC()

	frame, err := json.Marshal(payload)
	if err != nil {
		log.Printf("vehicles: marshal %s frame failed: %v", kind, err)
		return
	}
	hub.Broadcast(frame)
}

// Stream upgrades the request to a WebSocket and pushes the live feed: the
// current fleet first, then every accepted position.
func (h *VehicleHandler) Stream(c *gin.Context) {
	// The configured WriteTimeout would otherwise cut this long-lived
	// connection at the first write past the deadline, so clear it here.
	rc := http.NewResponseController(c.Writer)
	_ = rc.SetWriteDeadline(time.Time{})
	_ = rc.SetReadDeadline(time.Time{})

	// Echo the requested subprotocol (the JWT) so the browser accepts the handshake.
	subprotocol := strings.TrimSpace(c.Request.Header.Get("Sec-WebSocket-Protocol"))
	opts := &websocket.AcceptOptions{
		// Local single-user tool without auth: only the dev server origins may
		// open a stream, which keeps a random web page from reading the feed.
		OriginPatterns: []string{"localhost:*", "127.0.0.1:*"},
	}
	if subprotocol != "" {
		opts.Subprotocols = []string{subprotocol}
	}

	conn, err := websocket.Accept(c.Writer, c.Request, opts)
	if err != nil {
		log.Printf("vehicles: websocket accept failed: %v", err)
		return
	}
	conn.SetReadLimit(4096)

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	client := newStreamClient(conn, driverScope(c))
	go client.run(ctx)

	// Snapshot before subscribing: a position reported in the gap is picked up
	// by the next update, and clients treat the snapshot as the full state.
	if frame, err := h.snapshot(ctx, driverScope(c)); err != nil {
		log.Printf("vehicles: snapshot failed: %v", err)
	} else if err := client.Send(frame); err != nil {
		log.Printf("vehicles: snapshot send failed: %v", err)
	}

	remove := h.hub.Add(client)
	defer remove()

	log.Printf("vehicles: stream opened (subscribers=%d)", h.hub.Subscribers())

	// CloseRead keeps processing control frames and returns a context that is
	// cancelled once the browser goes away, which is what ends this handler.
	<-conn.CloseRead(ctx).Done()

	log.Printf("vehicles: stream closed (subscribers=%d)", h.hub.Subscribers())
}

// snapshot is the fleet state a new subscriber receives before the live feed.
// A driver only gets the units assigned to them.
func (h *VehicleHandler) snapshot(ctx context.Context, driverID *int64) ([]byte, error) {
	where, args := fleetWhere(driverID)
	rows, err := h.db.Query(ctx, vehicleColsWithRoute+where+" ORDER BY v.id", args...)
	if err != nil {
		return nil, fmt.Errorf("query fleet: %w", err)
	}
	defer rows.Close()

	vehicles := make([]vehicle.Vehicle, 0)
	for rows.Next() {
		v, err := scanVehicleWithRoute(rows)
		if err != nil {
			return nil, fmt.Errorf("scan vehicle: %w", err)
		}
		vehicles = append(vehicles, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read fleet: %w", err)
	}

	// The stops of the active routes, in one query.
	if err := fillRouteStops(ctx, h.db, vehicles); err != nil {
		return nil, fmt.Errorf("read the stops of the routes: %w", err)
	}

	return json.Marshal(gin.H{"type": "snapshot", "vehicles": vehicles, "at": time.Now().UTC()})
}

// staleOfflineSQL puts the units that stopped reporting into the offline
// state.
//
// A unit that never reported keeps the status it was created with: there is
// nothing to be late about. A unit that reported and then fell silent is
// marked, because a truck that stopped sending positions is not driving
// anymore, whatever its last report said.
const staleOfflineSQL = `UPDATE vehicles
    SET status = 'offline'
  WHERE last_seen_at IS NOT NULL
    AND last_seen_at < now() - make_interval(secs => $1)
    AND status <> 'offline'
  RETURNING id`

// MarkStaleOffline puts the units that stopped reporting into the offline
// state and tells the browsers about them, so the map greys them out at once
// instead of on the next poll.
//
// A unit that comes back brings itself back: every position report writes a
// fresh status and a fresh timestamp, so nothing else has to restore it.
//
// It returns how many units were marked.
func (h *VehicleHandler) MarkStaleOffline(ctx context.Context) (int, error) {
	if h.offlineAfter <= 0 {
		return 0, nil
	}

	rows, err := h.db.Query(ctx, staleOfflineSQL, h.offlineAfter.Seconds())
	if err != nil {
		return 0, fmt.Errorf("mark stale vehicles offline: %w", err)
	}
	defer rows.Close()

	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scan stale vehicle: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read stale vehicles: %w", err)
	}

	for _, id := range ids {
		row := h.db.QueryRow(ctx, vehicleColsWithRoute+" WHERE v.id = $1", id)
		v, err := scanVehicleWithRoute(row)
		if err != nil {
			// The unit was deleted in the meantime. It is gone from the map
			// anyway, so there is nothing to tell the browsers about.
			continue
		}
		if err := fillRouteStops(ctx, h.db, []vehicle.Vehicle{v}); err != nil {
			log.Printf("vehicles: read the stops of the routes failed: %v", err)
		}
		h.publish("vehicle_updated", gin.H{"vehicle": v})
	}

	return len(ids), nil
}

// trackStationaryAnchor keeps the anchor of a unit up to date and applies the
// idle decision of the server.
//
// The anchor is the point where the unit started standing still. A report that
// stays around it keeps the anchor where it is, so the clock of the idle
// session keeps running. A report that leaves the radius — or one that is
// faster than the speed limit — moves the anchor to the new position, starts
// the clock again and closes the session that was running.
//
// The distance was measured by the report (ST_Distance on geography); the
// rule that decides what happens with it is vehicle.ShouldMoveAnchor, a pure
// function that can be tested without a database.
//
// The status the unit ends up with is returned: the report wrote its own, and
// the server only keeps it when the unit really stands still.
func (h *VehicleHandler) trackStationaryAnchor(ctx context.Context, id int64, lat, lng, speed float64, anchorSince *time.Time, distanceM *float64) (string, error) {
	distance := 0.0
	if distanceM != nil {
		distance = *distanceM
	}

	move := vehicle.ShouldMoveAnchor(anchorSince != nil, distance, speed, h.idleRadiusM, h.idleMaxSpeedKmh)

	var status string
	if err := h.db.QueryRow(ctx, applyAnchorSQL, id, move, lat, lng).Scan(&status); err != nil {
		return "", fmt.Errorf("write the anchor of vehicle %d: %w", id, err)
	}

	return status, nil
}

// stationaryIdleSQL opens an idle session for every unit that stood still
// long enough, and puts it into the idle state.
//
// $1 is how long a unit has to stand still (seconds), $2 how fresh its last
// report has to be (seconds).
//
// A unit that stopped reporting is not idle, it is offline — the offline
// sweep is the one that says so, and it always wins.
//
// A unit that reports the status idle itself still gets a session: the server
// records how long the unit stands still, and the badge of the fleet card and
// the history need that session. The unit is never counted twice: a unit
// that already has an open session is not a candidate, so the next sweep
// leaves it alone.
const stationaryIdleSQL = `WITH candidates AS (
         SELECT v.id, v.stationary_since,
                v.stationary_anchor_lat, v.stationary_anchor_lng
           FROM vehicles v
          WHERE v.stationary_since IS NOT NULL
            AND v.status <> 'offline'
            AND now() - v.stationary_since >= make_interval(secs => $1)
            AND v.last_seen_at IS NOT NULL
            AND v.last_seen_at >= now() - make_interval(secs => $2)
            AND NOT EXISTS (
                  SELECT 1
                    FROM idle_events e
                   WHERE e.vehicle_id = v.id AND e.ended_at IS NULL
                )
     ), opened AS (
         INSERT INTO idle_events (vehicle_id, started_at, anchor_lat, anchor_lng, zone_id)
         SELECT c.id, c.stationary_since, c.stationary_anchor_lat, c.stationary_anchor_lng,
                (SELECT z.id
                   FROM zones z
                  WHERE c.stationary_anchor_lat IS NOT NULL
                    AND z.geom && ST_SetSRID(ST_MakePoint(c.stationary_anchor_lng, c.stationary_anchor_lat), 4326)
                    AND ST_Covers(z.geom, ST_SetSRID(ST_MakePoint(c.stationary_anchor_lng, c.stationary_anchor_lat), 4326))
                  ORDER BY z.id
                  LIMIT 1)
           FROM candidates c
         RETURNING vehicle_id
     )
     UPDATE vehicles v
        SET status = 'idle', updated_at = now()
       FROM opened o
      WHERE v.id = o.vehicle_id
     RETURNING v.id`

// MarkStationaryIdle puts the units that stood still long enough into the
// idle state, opens an idle session for each of them and tells the browsers
// about them, so every screen shows the new status at once.
//
// A unit that moves again ends its session by itself: the position report
// closes it (see trackStationaryAnchor). Nothing has to sweep for that.
//
// It returns how many sessions were opened.
func (h *VehicleHandler) MarkStationaryIdle(ctx context.Context) (int, error) {
	if h.idleAfter <= 0 {
		return 0, nil
	}

	// A unit whose last report is older than the offline threshold is not
	// standing still, it is gone: the offline sweep is the one that says so.
	fresh := h.offlineAfter
	if fresh <= 0 {
		// The offline check is off, so there is no offline threshold to
		// borrow: a unit that has not reported for as long as the idle
		// threshold is not standing still either.
		fresh = h.idleAfter
	}

	rows, err := h.db.Query(ctx, stationaryIdleSQL, h.idleAfter.Seconds(), fresh.Seconds())
	if err != nil {
		return 0, fmt.Errorf("mark stationary vehicles idle: %w", err)
	}
	defer rows.Close()

	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scan stationary vehicle: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read stationary vehicles: %w", err)
	}

	for _, id := range ids {
		row := h.db.QueryRow(ctx, vehicleColsWithRoute+" WHERE v.id = $1", id)
		v, err := scanVehicleWithRoute(row)
		if err != nil {
			// The unit was deleted in the meantime. It is gone from the map
			// anyway, so there is nothing to tell the browsers about.
			continue
		}
		if err := fillRouteStops(ctx, h.db, []vehicle.Vehicle{v}); err != nil {
			log.Printf("vehicles: read the stops of the routes failed: %v", err)
		}
		h.publish("vehicle_updated", gin.H{"vehicle": v})
	}

	return len(ids), nil
}

// streamClient adapts one WebSocket connection to the ws.Sink interface. A
// single goroutine owns the connection, so frames and pings never interleave,
// and Send only queues: a browser that cannot keep up is dropped by the hub
// instead of slowing the position API down.
type streamClient struct {
	conn *websocket.Conn
	send chan []byte
	done chan struct{}
	once sync.Once
	// scope is the driver id a subscriber is limited to; nil means the whole
	// fleet (admin and viewer).
	scope *int64
}

func newStreamClient(conn *websocket.Conn, scope *int64) *streamClient {
	return &streamClient{
		conn:  conn,
		send:  make(chan []byte, 64),
		done:  make(chan struct{}),
		scope: scope,
	}
}

const (
	streamWriteTimeout = 5 * time.Second
	streamPingInterval = 20 * time.Second
)

// Send queues one frame without blocking. A driver only receives the units
// assigned to them.
func (c *streamClient) Send(frame []byte) error {
	if !c.visible(frame) {
		return nil
	}

	select {
	case <-c.done:
		return errors.New("stream closed")
	default:
	}

	select {
	case c.send <- frame:
		return nil
	default:
		return errors.New("subscriber too slow")
	}
}

// visible reports whether a frame carries a unit this subscriber may see.
// shortcut: a unit handed over to another driver stays on the old driver's
// map until the next snapshot; upgrade when hand-over must be instant.
func (c *streamClient) visible(frame []byte) bool {
	if c.scope == nil {
		return true
	}

	var probe struct {
		Vehicle *struct {
			DriverID *int64 `json:"driver_id"`
		} `json:"vehicle"`
	}
	if json.Unmarshal(frame, &probe) != nil {
		return true
	}
	if probe.Vehicle != nil {
		return probe.Vehicle.DriverID != nil && *probe.Vehicle.DriverID == *c.scope
	}

	// A geofence frame names the unit at the root instead of carrying a
	// vehicle object. The key is looked up in the raw frame because a unit
	// without a driver has an explicit null there, and that unit belongs to
	// nobody but the admins.
	var fields map[string]json.RawMessage
	if json.Unmarshal(frame, &fields) != nil {
		return true
	}
	raw, ok := fields["driver_id"]
	if !ok {
		// A frame that is not about a unit (route, snapshot, deletion).
		return true
	}
	var driverID *int64
	if err := json.Unmarshal(raw, &driverID); err != nil {
		return true
	}
	return driverID != nil && *driverID == *c.scope
}

// Close releases the client. Safe to call more than once.
func (c *streamClient) Close() {
	c.once.Do(func() { close(c.done) })
}

// run writes queued frames and keeps the connection alive with pings.
func (c *streamClient) run(ctx context.Context) {
	defer c.conn.Close(websocket.StatusNormalClosure, "")

	ping := time.NewTicker(streamPingInterval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case frame := <-c.send:
			if err := c.write(ctx, frame); err != nil {
				return
			}
		case <-ping.C:
			pingCtx, cancel := context.WithTimeout(ctx, streamWriteTimeout)
			err := c.conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (c *streamClient) write(ctx context.Context, frame []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, streamWriteTimeout)
	defer cancel()

	return c.conn.Write(writeCtx, websocket.MessageText, frame)
}
