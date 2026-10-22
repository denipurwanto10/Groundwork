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

// IdleHandler serves the history of the idle sessions: how long a unit stood
// still, and where it stood.
//
// The sessions themselves are written by the position report and by the sweep
// (see internal/handler/vehicles.go), so the history and the fleet card can
// never disagree.
type IdleHandler struct {
	db *db.Pool
}

func NewIdleHandler(pool *db.Pool) *IdleHandler {
	return &IdleHandler{db: pool}
}

func (h *IdleHandler) ctxWithTimeout(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 10*time.Second)
}

// IdleEvent is one spell of standing still.
//
// A session without an end is still running: the unit stands there right now.
type IdleEvent struct {
	ID          int64  `json:"id"`
	VehicleID   int64  `json:"vehicle_id"`
	VehicleName string `json:"vehicle_name"`
	// The zone the unit stood in. Null when it stood outside every zone.
	ZoneID   *int64 `json:"zone_id"`
	ZoneName string `json:"zone_name"`
	// StartedAt is the moment the unit started standing still.
	StartedAt time.Time `json:"started_at"`
	// EndedAt is the moment it moved again; null while the session runs.
	EndedAt *time.Time `json:"ended_at"`
	// DurationS is how long the session lasted, in seconds. A session that is
	// still running is counted up to now.
	DurationS float64 `json:"duration_s"`
}

const (
	idleDefaultLimit = 100
	idleMaxLimit     = 500
)

// List returns the idle sessions, newest first.
//
// Every signed-in role may read them; a driver only sees the sessions of
// their own unit, the same rule the fleet list follows.
func (h *IdleHandler) List(c *gin.Context) {
	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	args := make([]any, 0, 5)
	where := make([]string, 0, 4)

	if raw := strings.TrimSpace(c.Query("vehicle_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, errorBody("invalid_id", "vehicle_id must be a positive integer"))
			return
		}
		args = append(args, id)
		where = append(where, fmt.Sprintf("e.vehicle_id = $%d", len(args)))
	}

	from, to, err := idleEventWindow(c.Query("from"), c.Query("to"), time.Now())
	if err != nil {
		c.JSON(http.StatusBadRequest, errorBody("validation_failed", err.Error()))
		return
	}
	if from != nil {
		args = append(args, *from)
		where = append(where, fmt.Sprintf("e.started_at >= $%d", len(args)))
	}
	if to != nil {
		args = append(args, *to)
		where = append(where, fmt.Sprintf("e.started_at < $%d", len(args)))
	}

	if scope := driverScope(c); scope != nil {
		args = append(args, *scope)
		where = append(where, fmt.Sprintf("v.driver_id = $%d", len(args)))
	}

	limit := idleDefaultLimit
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = min(n, idleMaxLimit)
		}
	}
	args = append(args, limit)

	// LEFT JOIN: an idle session outlives neither its unit nor its zone (the
	// foreign keys cascade or are set to null), but a row with a missing
	// counterpart must not disappear from the history either.
	query := `SELECT e.id, e.vehicle_id, COALESCE(v.name, ''), e.zone_id, COALESCE(z.name, ''),
                     e.started_at, e.ended_at,
                     EXTRACT(EPOCH FROM (COALESCE(e.ended_at, now()) - e.started_at))::double precision
                FROM idle_events e
                LEFT JOIN vehicles v ON v.id = e.vehicle_id
                LEFT JOIN zones z ON z.id = e.zone_id`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += fmt.Sprintf(" ORDER BY e.started_at DESC, e.id DESC LIMIT $%d", len(args))

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		log.Printf("idle: list query failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	defer rows.Close()

	events := make([]IdleEvent, 0)
	for rows.Next() {
		var ev IdleEvent
		if err := rows.Scan(&ev.ID, &ev.VehicleID, &ev.VehicleName, &ev.ZoneID, &ev.ZoneName,
			&ev.StartedAt, &ev.EndedAt, &ev.DurationS); err != nil {
			log.Printf("idle: scan history row failed: %v", err)
			c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
			return
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		log.Printf("idle: read history failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": events})
}

// idleEventWindow reads the optional from/to filter of the idle history.
//
// The two ends accept a plain day (YYYY-MM-DD) or a timestamp (RFC3339), the
// same way the report does. A plain day covers the whole day: "to=2026-10-21"
// includes everything that happened on that day. A timestamp is the exact
// instant it names, and the window ends there.
//
// A missing end means "until now": the history of a period that has not
// ended yet is still worth reading.
func idleEventWindow(fromRaw, toRaw string, now time.Time) (*time.Time, *time.Time, error) {
	loc := now.Location()
	fromRaw = strings.TrimSpace(fromRaw)
	toRaw = strings.TrimSpace(toRaw)

	var from, to *time.Time

	if fromRaw != "" {
		t, _, err := parseReportTime(fromRaw, loc)
		if err != nil {
			return nil, nil, fmt.Errorf("from: %w", err)
		}
		from = &t
	}

	if toRaw != "" {
		t, isDay, err := parseReportTime(toRaw, loc)
		if err != nil {
			return nil, nil, fmt.Errorf("to: %w", err)
		}
		if isDay {
			// A plain day covers the whole day, so the window ends with the
			// first instant of the next one.
			end := startOfDay(t, loc).AddDate(0, 0, 1)
			to = &end
		} else {
			to = &t
		}
	}

	return from, to, nil
}
