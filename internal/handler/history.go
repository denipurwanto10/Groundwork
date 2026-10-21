package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// History of a vehicle: the positions it reported in the past, so the map can
// replay a day instead of showing only where the unit is right now.
//
// The positions are written by the position report (see
// VehicleHandler.UpdatePosition), so the history and the live feed are the
// same data: what the unit did is what it reported, second by second.
const (
	// historyDefaultLimit is how many points one answer carries. A day of a
	// two-second feed is 43.000 points; the browser cannot use that many, and
	// the shape of the drive is already told by a few thousand.
	historyDefaultLimit = 2000
	// historyMaxLimit is the ceiling for ?limit=.
	historyMaxLimit = 20000
	// historyMaxRange is the longest window one request may ask for.
	historyMaxRange = 31 * 24 * time.Hour
)

// historyPoint is one recorded position of a unit.
type historyPoint struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
	// Speed in km/h, the unit the fleet reports everywhere else too.
	Speed float64 `json:"speed"`
	// Heading in degrees, when the unit reported one.
	Heading   *float64  `json:"heading"`
	Timestamp time.Time `json:"timestamp"`
}

// historySQL reads the positions of one vehicle in a time window, oldest
// first.
//
// When the window holds more points than the limit, every n-th point is kept
// (plus the first and the last, so the track still starts and ends where the
// unit was). The drive keeps its shape and the answer stays small.
const historySQL = `WITH ranged AS (
         SELECT lat, lng, speed_kmh, heading, recorded_at,
                row_number() OVER (ORDER BY recorded_at) AS rn,
                count(*) OVER () AS total
           FROM vehicle_positions
          WHERE vehicle_id = $1
            AND recorded_at >= $2
            AND recorded_at <  $3
     )
     SELECT lat, lng, speed_kmh, heading, recorded_at
       FROM ranged
      WHERE total <= $4
         OR rn = 1
         OR rn = total
         OR rn % GREATEST(1, (total + $4 - 1) / $4) = 0
      ORDER BY recorded_at`

// History returns the recorded positions of one vehicle, oldest first.
//
// Every signed-in role may read the history; a driver only gets the unit that
// is theirs, the same rule the fleet list follows.
func (h *VehicleHandler) History(c *gin.Context) {
	id, ok := vehicleIDParam(c)
	if !ok {
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	if scope := driverScope(c); scope != nil {
		allowed, err := h.vehicleBelongsToDriver(ctx, id, *scope)
		if err != nil {
			log.Printf("history: driver scope for vehicle %d failed: %v", id, err)
			c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
			return
		}
		if !allowed {
			c.JSON(http.StatusForbidden, errorBody("forbidden", "this vehicle is not assigned to you"))
			return
		}
	}

	from, to, err := historyRange(c, time.Now())
	if err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_range", err.Error()))
		return
	}
	limit := historyLimit(c)

	rows, err := h.db.Query(ctx, historySQL, id, from, to, limit)
	if err != nil {
		log.Printf("history: query for vehicle %d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	defer rows.Close()

	points := make([]historyPoint, 0, limit)
	for rows.Next() {
		var p historyPoint
		if err := rows.Scan(&p.Lat, &p.Lng, &p.Speed, &p.Heading, &p.Timestamp); err != nil {
			log.Printf("history: scan for vehicle %d failed: %v", id, err)
			c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
			return
		}
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		log.Printf("history: read for vehicle %d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": points})
}

// vehicleBelongsToDriver reports whether the unit is assigned to the driver.
// A unit without a driver belongs to the admins, not to a driver.
func (h *VehicleHandler) vehicleBelongsToDriver(ctx context.Context, id, driverID int64) (bool, error) {
	var owner *int64
	err := h.db.QueryRow(ctx, `SELECT driver_id FROM vehicles WHERE id = $1`, id).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return owner != nil && *owner == driverID, nil
}

// historyRange reads the time window of a history request.
//
//   - ?date=YYYY-MM-DD selects one day in the time zone of the server. The
//     operator thinks in local days, and the server runs on the same machine
//     as the browser.
//   - ?from=...&to=... selects an explicit window. Both accept RFC3339 and a
//     plain date; a missing end means one day after the start, a missing
//     start means one day before the end.
func historyRange(c *gin.Context, now time.Time) (time.Time, time.Time, error) {
	loc := now.Location()
	fromRaw := strings.TrimSpace(c.Query("from"))
	toRaw := strings.TrimSpace(c.Query("to"))
	dateRaw := strings.TrimSpace(c.Query("date"))

	var from, to time.Time
	var err error

	switch {
	case fromRaw != "" || toRaw != "":
		if fromRaw != "" {
			if from, err = parseHistoryTime(fromRaw, loc); err != nil {
				return time.Time{}, time.Time{}, fmt.Errorf("from: %w", err)
			}
		}
		if toRaw != "" {
			if to, err = parseHistoryTime(toRaw, loc); err != nil {
				return time.Time{}, time.Time{}, fmt.Errorf("to: %w", err)
			}
		}
		if from.IsZero() {
			from = to.AddDate(0, 0, -1)
		}
		if to.IsZero() {
			to = from.AddDate(0, 0, 1)
		}
	default:
		if dateRaw == "" {
			dateRaw = now.Format("2006-01-02")
		}
		day, err := time.ParseInLocation("2006-01-02", dateRaw, loc)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("date must be written as YYYY-MM-DD")
		}
		from, to = day, day.AddDate(0, 0, 1)
	}

	if !from.Before(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("the range must end after it starts")
	}
	if to.Sub(from) > historyMaxRange {
		return time.Time{}, time.Time{}, fmt.Errorf("the range must be at most 31 days")
	}

	return from, to, nil
}

// parseHistoryTime reads one end of a range. RFC3339 is the exact form; the
// shorter ones are what a person types.
func parseHistoryTime(raw string, loc *time.Location) (time.Time, error) {
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a date or a timestamp", raw)
}

// historyLimit reads ?limit= and keeps it inside the ceiling.
func historyLimit(c *gin.Context) int {
	limit := historyDefaultLimit
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = min(n, historyMaxLimit)
		}
	}
	return limit
}
