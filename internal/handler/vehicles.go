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

func (h *VehicleHandler) ctxWithTimeout(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 10*time.Second)
}

const vehicleCols = `SELECT v.id, v.code, v.name, v.kind, v.status, v.driver_id,
       v.last_lat, v.last_lng, v.last_speed_kmh, v.last_seen_at,
       v.last_heading, v.route_color`

const vehicleFrom = ` FROM vehicles v`

// vehicleColsWithRoute adds the delivery route every unit is following to
// the fleet list, so the map can draw the line and the fleet card can say
// where the unit is heading without a second request.
//
// The route is picked with a lateral join: one unit has at most one active
// route, and the join keeps the fleet list a single round trip.
const vehicleColsWithRoute = `SELECT v.id, v.code, v.name, v.kind, v.status, v.driver_id,
       v.last_lat, v.last_lng, v.last_speed_kmh, v.last_seen_at,
       v.last_heading, v.route_color,
       rt.id, rt.origin_name, rt.origin_lat, rt.origin_lng,
       rt.dest_name, rt.dest_lat, rt.dest_lng,
       COALESCE(ST_AsGeoJSON(rt.geom, 6)::json, '{}'::json),
       rt.distance_m, rt.duration_s, rt.source, rt.status, rt.created_at, rt.progress,
       rt.stops, rt.started_at, rt.ended_at,
       (v.driver_id IS NOT NULL) AS driver_assigned
  FROM vehicles v
  LEFT JOIN LATERAL (
       SELECT rt.id, rt.origin_name, rt.origin_lat, rt.origin_lng,
              rt.dest_name, rt.dest_lat, rt.dest_lng, rt.geom, rt.distance_m,
              rt.duration_s, rt.source, rt.status, rt.created_at, rt.stops,
              rt.started_at, rt.ended_at,
              ` + routeProgressSQL + ` AS progress
         FROM vehicle_routes rt
        WHERE rt.vehicle_id = v.id AND rt.status = 'active'
        ORDER BY rt.id DESC
        LIMIT 1
  ) rt ON true`

// rowScanner is satisfied by both pgx.Row and pgx.Rows, so one scan function
// serves the list, the snapshot and the post-update read-back.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanVehicle reads one fleet row. extra collects the additional columns of
// queries that return more than the fleet itself (the position update adds the
// coordinates the vehicle had before it).
func scanVehicle(row rowScanner, extra ...any) (vehicle.Vehicle, error) {
	var v vehicle.Vehicle
	dest := []any{&v.ID, &v.Code, &v.Name, &v.Kind, &v.Status, &v.DriverID,
		&v.Lat, &v.Lng, &v.SpeedKmh, &v.LastSeenAt, &v.Heading, &v.RouteColor}
	dest = append(dest, extra...)
	if err := row.Scan(dest...); err != nil {
		return vehicle.Vehicle{}, err
	}

	return v, nil
}

// scanVehicleWithRoute reads a fleet row that also carries the delivery route
// the unit is following. The route columns are nullable: a unit without a
// route leaves them empty and the vehicle travels on without one.
func scanVehicleWithRoute(row rowScanner) (vehicle.Vehicle, error) {
	var (
		v          vehicle.Vehicle
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
		stops      []byte
		startedAt  *time.Time
		endedAt    *time.Time
		driverSet  *bool
	)

	if err := row.Scan(&v.ID, &v.Code, &v.Name, &v.Kind, &v.Status, &v.DriverID,
		&v.Lat, &v.Lng, &v.SpeedKmh, &v.LastSeenAt, &v.Heading, &v.RouteColor,
		&routeID, &originName, &originLat, &originLng,
		&destName, &destLat, &destLng,
		&geojson, &distanceM, &durationS, &source, &status, &createdAt, &progress,
		&stops, &startedAt, &endedAt, &driverSet); err != nil {
		return vehicle.Vehicle{}, err
	}

	if routeID == nil {
		return v, nil
	}

	route := &vehicle.Route{
		ID:          *routeID,
		VehicleID:   v.ID,
		Origin:      vehicle.RouteStop{Name: *originName, Lat: *originLat, Lng: *originLng},
		Destination: vehicle.RouteStop{Name: *destName, Lat: *destLat, Lng: *destLng},
		Stops:       decodeStops(stops),
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
` + vehicleCols + ` FROM updated v`

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
	// report; the geofence check compares them with the new ones.
	var prevLat, prevLng *float64
	updated, err := scanVehicle(h.db.QueryRow(ctx, reportPositionSQL, id, *in.Lat, *in.Lng, speed, in.Status, heading),
		&prevLat, &prevLng)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		c.JSON(http.StatusNotFound, errorBody("not_found", "vehicle not found"))
		return
	case err != nil:
		log.Printf("vehicles: position update failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
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
                   last_heading, route_color
     ), logged AS (
         INSERT INTO vehicle_positions (vehicle_id, lat, lng, speed_kmh, heading)
         SELECT $1, $2, $3, $4, $6 FROM updated
         RETURNING id
     )
     SELECT v.id, v.code, v.name, v.kind, v.status, v.driver_id,
            v.last_lat, v.last_lng, v.last_speed_kmh, v.last_seen_at,
            v.last_heading, v.route_color,
            p.prev_lat, p.prev_lng
       FROM updated v
       CROSS JOIN previous p`

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
