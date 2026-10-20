package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"entity-management/internal/db"
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
}

func NewVehicleHandler(pool *db.Pool, hub *ws.Hub) *VehicleHandler {
	return &VehicleHandler{db: pool, hub: hub}
}

func (h *VehicleHandler) ctxWithTimeout(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 10*time.Second)
}

const vehicleCols = `SELECT v.id, v.code, v.name, v.kind, v.status,
       v.last_lat, v.last_lng, v.last_speed_kmh, v.last_seen_at`

const vehicleFrom = ` FROM vehicles v`

// rowScanner is satisfied by both pgx.Row and pgx.Rows, so one scan function
// serves the list, the snapshot and the post-update read-back.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanVehicle(row rowScanner) (vehicle.Vehicle, error) {
	var v vehicle.Vehicle
	if err := row.Scan(&v.ID, &v.Code, &v.Name, &v.Kind, &v.Status,
		&v.Lat, &v.Lng, &v.SpeedKmh, &v.LastSeenAt); err != nil {
		return vehicle.Vehicle{}, err
	}

	return v, nil
}

// List returns the fleet together with the latest known position of each unit.
func (h *VehicleHandler) List(c *gin.Context) {
	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	rows, err := h.db.Query(ctx, vehicleCols+vehicleFrom+" ORDER BY v.id")
	if err != nil {
		log.Printf("vehicles: list query failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	defer rows.Close()

	vehicles := make([]vehicle.Vehicle, 0)
	for rows.Next() {
		v, err := scanVehicle(rows)
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
		`INSERT INTO vehicles (code, name, kind) VALUES ($1, $2, $3) RETURNING id`,
		in.Code, in.Name, in.Kind).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			c.JSON(http.StatusConflict, errorBody("duplicate_code", "a vehicle with that code already exists"))
			return
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

	c.JSON(http.StatusCreated, gin.H{"data": created})
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

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	updated, err := scanVehicle(h.db.QueryRow(ctx, reportPositionSQL, id, *in.Lat, *in.Lng, speed, in.Status))
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

	// A vehicle that reaches its destination closes its own route.
	h.followRoute(ctx, id, *in.Lat, *in.Lng)

	c.JSON(http.StatusOK, gin.H{"data": updated})
}

// reportPositionSQL appends to the position log and refreshes the fleet row in
// one round trip. The `logged` CTE is data-modifying, so PostgreSQL runs it
// even though the outer SELECT only reads `updated`.
const reportPositionSQL = `WITH updated AS (
         UPDATE vehicles
            SET last_lat = $2, last_lng = $3, last_speed_kmh = $4, status = $5,
                last_seen_at = now(), updated_at = now()
          WHERE id = $1
         RETURNING id, code, name, kind, status, last_lat, last_lng, last_speed_kmh, last_seen_at
     ), logged AS (
         INSERT INTO vehicle_positions (vehicle_id, lat, lng, speed_kmh)
         SELECT $1, $2, $3, $4 FROM updated
         RETURNING id
     )
` + vehicleCols + ` FROM updated v`

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

	conn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{
		// Local single-user tool without auth: only the dev server origins may
		// open a stream, which keeps a random web page from reading the feed.
		OriginPatterns: []string{"localhost:*", "127.0.0.1:*"},
	})
	if err != nil {
		log.Printf("vehicles: websocket accept failed: %v", err)
		return
	}
	conn.SetReadLimit(4096)

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	client := newStreamClient(conn)
	go client.run(ctx)

	// Snapshot before subscribing: a position reported in the gap is picked up
	// by the next update, and clients treat the snapshot as the full state.
	if frame, err := h.snapshot(ctx); err != nil {
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
func (h *VehicleHandler) snapshot(ctx context.Context) ([]byte, error) {
	rows, err := h.db.Query(ctx, vehicleCols+vehicleFrom+" ORDER BY v.id")
	if err != nil {
		return nil, fmt.Errorf("query fleet: %w", err)
	}
	defer rows.Close()

	vehicles := make([]vehicle.Vehicle, 0)
	for rows.Next() {
		v, err := scanVehicle(rows)
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

// streamClient adapts one WebSocket connection to the ws.Sink interface. A
// single goroutine owns the connection, so frames and pings never interleave,
// and Send only queues: a browser that cannot keep up is dropped by the hub
// instead of slowing the position API down.
type streamClient struct {
	conn *websocket.Conn
	send chan []byte
	done chan struct{}
	once sync.Once
}

func newStreamClient(conn *websocket.Conn) *streamClient {
	return &streamClient{
		conn: conn,
		send: make(chan []byte, 64),
		done: make(chan struct{}),
	}
}

const (
	streamWriteTimeout = 5 * time.Second
	streamPingInterval = 20 * time.Second
)

// Send queues one frame without blocking.
func (c *streamClient) Send(frame []byte) error {
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
