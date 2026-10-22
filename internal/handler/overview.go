package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"entity-management/internal/db"
	"github.com/gin-gonic/gin"
)

// OverviewHandler serves the aggregate the Overview tab opens with: what the
// fleet did today, in one request.
//
// The numbers are computed in the database rather than in the browser: the
// whole history of the reported positions lives there, and shipping it to the
// client only to add it up would be both slow and pointless.
type OverviewHandler struct {
	db *db.Pool
}

func NewOverviewHandler(pool *db.Pool) *OverviewHandler {
	return &OverviewHandler{db: pool}
}

func (h *OverviewHandler) ctxWithTimeout(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 10*time.Second)
}

// Overview is the answer of GET /api/overview.
type Overview struct {
	// Date is the day the numbers belong to, in the time zone of the server.
	Date string `json:"date"`
	// DistanceKM is how far the whole fleet drove today.
	DistanceKM float64 `json:"distance_km"`
	// Vehicles counts the fleet by its current status.
	Vehicles OverviewVehicles `json:"vehicles"`
	// Events counts the zone crossings of today.
	Events OverviewEvents `json:"events"`
	// Recent are the latest zone crossings of the whole history, newest
	// first. They are not limited to today, so the panel keeps showing what
	// happened even on a day without a single crossing.
	Recent []GeofenceEvent `json:"recent"`
}

// OverviewVehicles is the fleet counted by status.
type OverviewVehicles struct {
	Total   int `json:"total"`
	Moving  int `json:"moving"`
	Idle    int `json:"idle"`
	Offline int `json:"offline"`
}

// OverviewEvents counts the zone crossings of today.
type OverviewEvents struct {
	Total int `json:"total"`
	Enter int `json:"enter"`
	Exit  int `json:"exit"`
	// Zones breaks the crossings of today down per work area, so the panel
	// can say "3 trucks entered Warehouse North" instead of a bare number.
	Zones []OverviewZone `json:"zones"`
}

// OverviewZone is one work area and how often it was crossed today.
type OverviewZone struct {
	ZoneID   int64  `json:"zone_id"`
	ZoneName string `json:"zone_name"`
	Enter    int    `json:"enter"`
	Exit     int    `json:"exit"`
	Total    int    `json:"total"`
}

// Get answers with the aggregate of the day.
//
// A driver only sees their own unit, the same rule the fleet list follows:
// the distance driven and the crossings of somebody else's truck are none of
// their business.
func (h *OverviewHandler) Get(c *gin.Context) {
	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	var (
		ov       Overview
		recent   []byte
		zones    []byte
		distance float64
	)

	row := h.db.QueryRow(ctx, overviewSQL, overviewScope(driverScope(c)))
	if err := row.Scan(
		&ov.Date,
		&distance,
		&ov.Vehicles.Total,
		&ov.Vehicles.Moving,
		&ov.Vehicles.Idle,
		&ov.Vehicles.Offline,
		&ov.Events.Total,
		&ov.Events.Enter,
		&ov.Events.Exit,
		&recent,
		&zones,
	); err != nil {
		log.Printf("overview: query failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	ov.DistanceKM = round2(distance)

	ov.Recent = make([]GeofenceEvent, 0)
	if err := decodeOverviewJSON(recent, &ov.Recent); err != nil {
		log.Printf("overview: decode recent events failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	ov.Events.Zones = make([]OverviewZone, 0)
	if err := decodeOverviewJSON(zones, &ov.Events.Zones); err != nil {
		log.Printf("overview: decode zone breakdown failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": ov})
}

// decodeOverviewJSON turns one JSON column of the aggregate into a slice.
func decodeOverviewJSON(raw []byte, out any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// overviewScope turns the driver scope into a query argument: the id of the
// driver, or NULL for the roles that see the whole fleet.
func overviewScope(driverID *int64) any {
	if driverID == nil {
		return nil
	}
	return *driverID
}

// overviewSQL is the whole answer of the Overview tab in one round trip.
//
// $1 is the id of the signed-in driver, or NULL for the roles that see the
// whole fleet. The same scope is applied to the distance, to the counters and
// to the crossing history.
//
// The distance is the sum of the steps between the positions the units
// reported today, measured on the sphere and not on the flat map.
//
// Two guards keep the number honest. A unit that is placed at the origin of a
// new route has not driven that distance, so a step that would need an
// impossible speed is left out. And the step from the last fix before
// midnight into the first fix of today is counted, so the kilometres driven
// across the day are not lost.
const overviewSQL = `WITH scoped AS (
    SELECT v.id, v.status
      FROM vehicles v
     WHERE $1::bigint IS NULL OR v.driver_id = $1
), predecessor AS (
    SELECT s.id AS vehicle_id, p.lat, p.lng, p.recorded_at
      FROM scoped s
     CROSS JOIN LATERAL (
           SELECT prev.lat, prev.lng, prev.recorded_at
             FROM vehicle_positions prev
            WHERE prev.vehicle_id = s.id
              AND prev.recorded_at < date_trunc('day', now())
            ORDER BY prev.recorded_at DESC
            LIMIT 1
     ) p
), today_pos AS (
    SELECT vp.vehicle_id, vp.lat, vp.lng, vp.recorded_at
      FROM vehicle_positions vp
      JOIN scoped s ON s.id = vp.vehicle_id
     WHERE vp.recorded_at >= date_trunc('day', now())
    UNION ALL
    SELECT vehicle_id, lat, lng, recorded_at FROM predecessor
), steps AS (
    SELECT vehicle_id,
           recorded_at,
           ST_Distance(
               ST_SetSRID(ST_MakePoint(lng, lat), 4326)::geography,
               ST_SetSRID(ST_MakePoint(prev_lng, prev_lat), 4326)::geography) AS meters,
           EXTRACT(EPOCH FROM (recorded_at - prev_at)) AS seconds
      FROM (
            SELECT vehicle_id, lat, lng, recorded_at,
                   lag(lat)         OVER w AS prev_lat,
                   lag(lng)         OVER w AS prev_lng,
                   lag(recorded_at) OVER w AS prev_at
              FROM today_pos
            WINDOW w AS (PARTITION BY vehicle_id ORDER BY recorded_at)
      ) p
     WHERE prev_lat IS NOT NULL
       AND recorded_at >= date_trunc('day', now())
)
SELECT to_char(now(), 'YYYY-MM-DD'),
       COALESCE((
           SELECT sum(meters)
             FROM steps
            WHERE seconds > 0 AND meters / seconds * 3.6 <= 200
       ), 0) / 1000.0,
       (SELECT count(*) FROM scoped),
       (SELECT count(*) FROM scoped WHERE status = 'moving'),
       (SELECT count(*) FROM scoped WHERE status = 'idle'),
       (SELECT count(*) FROM scoped WHERE status = 'offline'),
       (SELECT count(*) FROM geofence_events g
          JOIN scoped s ON s.id = g.vehicle_id
         WHERE g.created_at >= date_trunc('day', now())),
       (SELECT count(*) FROM geofence_events g
          JOIN scoped s ON s.id = g.vehicle_id
         WHERE g.created_at >= date_trunc('day', now()) AND g.event_type = 'ENTER'),
       (SELECT count(*) FROM geofence_events g
          JOIN scoped s ON s.id = g.vehicle_id
         WHERE g.created_at >= date_trunc('day', now()) AND g.event_type = 'EXIT'),
       COALESCE((
           SELECT jsonb_agg(to_jsonb(t) ORDER BY t.created_at DESC, t.id DESC)
             FROM (
                   SELECT g.id,
                          g.vehicle_id,
                          COALESCE(v.name, '') AS vehicle_name,
                          g.zone_id,
                          COALESCE(z.name, '') AS zone_name,
                          g.event_type,
                          g.created_at
                     FROM geofence_events g
                     JOIN scoped s ON s.id = g.vehicle_id
                     LEFT JOIN vehicles v ON v.id = g.vehicle_id
                     LEFT JOIN zones z ON z.id = g.zone_id
                    ORDER BY g.created_at DESC, g.id DESC
                    LIMIT 5
             ) t
       ), '[]'::jsonb),
       COALESCE((
           SELECT jsonb_agg(to_jsonb(t) ORDER BY t.total DESC, t.zone_name, t.zone_id)
             FROM (
                   SELECT g.zone_id,
                          COALESCE(z.name, '') AS zone_name,
                          count(*) FILTER (WHERE g.event_type = 'ENTER') AS enter,
                          count(*) FILTER (WHERE g.event_type = 'EXIT') AS exit,
                          count(*) AS total
                     FROM geofence_events g
                     JOIN scoped s ON s.id = g.vehicle_id
                     LEFT JOIN zones z ON z.id = g.zone_id
                    WHERE g.created_at >= date_trunc('day', now())
                    GROUP BY g.zone_id, z.name
             ) t
       ), '[]'::jsonb)`
