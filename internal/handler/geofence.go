package handler

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"entity-management/internal/db"
	"github.com/gin-gonic/gin"
)

// GeofenceHandler serves the history of zone boundary crossings. The crossing
// itself is detected and stored by the position update (see
// VehicleHandler.UpdatePosition), so the fleet map and the history always agree.
type GeofenceHandler struct {
	db *db.Pool
}

func NewGeofenceHandler(pool *db.Pool) *GeofenceHandler {
	return &GeofenceHandler{db: pool}
}

func (h *GeofenceHandler) ctxWithTimeout(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 10*time.Second)
}

// GeofenceEvent is one crossing of a zone boundary by a vehicle.
type GeofenceEvent struct {
	ID          int64     `json:"id"`
	VehicleID   int64     `json:"vehicle_id"`
	VehicleName string    `json:"vehicle_name"`
	ZoneID      int64     `json:"zone_id"`
	ZoneName    string    `json:"zone_name"`
	EventType   string    `json:"event_type"`
	CreatedAt   time.Time `json:"created_at"`
}

const (
	geofenceEnter = "ENTER"
	geofenceExit  = "EXIT"

	geofenceDefaultLimit = 100
	geofenceMaxLimit     = 500
)

// detectGeofence records every boundary the reported position crossed and
// returns the stored events, so the caller can push one frame per crossing.
//
// prevLat/prevLng is the position the vehicle had before this report; nil means
// it had no fix yet, which counts as "outside" — a first fix inside a zone is
// an ENTER rather than silence.
//
// A failure here never fails the position report: the position is already
// stored, and dropping it because the geofence insert failed would lose data.
// The error is logged instead.
func (h *VehicleHandler) detectGeofence(ctx context.Context, id int64, prevLat, prevLng *float64, lat, lng float64) []GeofenceEvent {
	rows, err := h.db.Query(ctx, detectGeofenceSQL, id, prevLat, prevLng, lat, lng)
	if err != nil {
		log.Printf("geofence: detect for vehicle %d failed: %v", id, err)
		return nil
	}
	defer rows.Close()

	events := make([]GeofenceEvent, 0, 1)
	for rows.Next() {
		ev := GeofenceEvent{VehicleID: id}
		if err := rows.Scan(&ev.ID, &ev.ZoneID, &ev.ZoneName, &ev.EventType, &ev.CreatedAt); err != nil {
			log.Printf("geofence: scan event for vehicle %d failed: %v", id, err)
			return events
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		log.Printf("geofence: read events for vehicle %d failed: %v", id, err)
	}

	return events
}

// detectGeofenceSQL compares the previous point of a vehicle with the new one
// against every zone and stores the transitions.
//
// $1 vehicle_id, $2 previous lat, $3 previous lng, $4 new lat, $5 new lng.
// ST_Covers returns true for a point inside or on the boundary of a polygon, so
// a unit parked on a fence line stays "inside" instead of flapping between the
// two zones that share it. The `&&` test is the bounding-box prefilter that lets
// PostgreSQL use the GIST index on zones.geom.
const detectGeofenceSQL = `WITH old_pt AS (
         SELECT CASE
                    WHEN $2::double precision IS NULL OR $3::double precision IS NULL THEN NULL
                    ELSE ST_SetSRID(ST_MakePoint($3::double precision, $2::double precision), 4326)
                END AS g
     ), new_pt AS (
         SELECT ST_SetSRID(ST_MakePoint($5::double precision, $4::double precision), 4326) AS g
     ), hit AS (
         SELECT z.id AS zone_id,
                z.name AS zone_name,
                ST_Covers(z.geom, new_pt.g) AS inside_new,
                COALESCE(ST_Covers(z.geom, old_pt.g), false) AS inside_old
           FROM zones z
           CROSS JOIN new_pt
           CROSS JOIN old_pt
          WHERE (new_pt.g && z.geom AND ST_Covers(z.geom, new_pt.g))
             OR (old_pt.g IS NOT NULL AND old_pt.g && z.geom AND ST_Covers(z.geom, old_pt.g))
     ), events AS (
         INSERT INTO geofence_events (vehicle_id, zone_id, event_type)
         SELECT $1,
                hit.zone_id,
                CASE WHEN hit.inside_new THEN '` + geofenceEnter + `' ELSE '` + geofenceExit + `' END
           FROM hit
          WHERE hit.inside_new <> hit.inside_old
         RETURNING id, zone_id, event_type, created_at
     )
     SELECT e.id, e.zone_id, z.name, e.event_type, e.created_at
       FROM events e
       JOIN zones z ON z.id = e.zone_id
      ORDER BY e.id`

// List returns the crossing history, newest first. Every signed-in role may
// read it; a driver only sees the crossings of their own unit, the same rule
// the fleet list follows.
func (h *GeofenceHandler) List(c *gin.Context) {
	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	args := make([]any, 0, 4)
	where := make([]string, 0, 3)

	if raw := strings.TrimSpace(c.Query("vehicle_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, errorBody("invalid_id", "vehicle_id must be a positive integer"))
			return
		}
		args = append(args, id)
		where = append(where, fmt.Sprintf("g.vehicle_id = $%d", len(args)))
	}

	if raw := strings.TrimSpace(c.Query("zone_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, errorBody("invalid_id", "zone_id must be a positive integer"))
			return
		}
		args = append(args, id)
		where = append(where, fmt.Sprintf("g.zone_id = $%d", len(args)))
	}

	if scope := driverScope(c); scope != nil {
		args = append(args, *scope)
		where = append(where, fmt.Sprintf("v.driver_id = $%d", len(args)))
	}

	limit := geofenceDefaultLimit
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = min(n, geofenceMaxLimit)
		}
	}
	args = append(args, limit)

	// LEFT JOIN: a crossing outlives neither its unit nor its zone (the foreign
	// keys cascade), but a row with a missing counterpart must not disappear
	// from the history either.
	query := `SELECT g.id, g.vehicle_id, COALESCE(v.name, ''), g.zone_id, COALESCE(z.name, ''),
                     g.event_type, g.created_at
                FROM geofence_events g
                LEFT JOIN vehicles v ON v.id = g.vehicle_id
                LEFT JOIN zones z ON z.id = g.zone_id`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += fmt.Sprintf(" ORDER BY g.created_at DESC, g.id DESC LIMIT $%d", len(args))

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		log.Printf("geofence: list query failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	defer rows.Close()

	events := make([]GeofenceEvent, 0)
	for rows.Next() {
		var ev GeofenceEvent
		if err := rows.Scan(&ev.ID, &ev.VehicleID, &ev.VehicleName, &ev.ZoneID, &ev.ZoneName,
			&ev.EventType, &ev.CreatedAt); err != nil {
			log.Printf("geofence: scan history row failed: %v", err)
			c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
			return
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		log.Printf("geofence: read history failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": events})
}
