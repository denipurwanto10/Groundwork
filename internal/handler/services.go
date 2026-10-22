package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"entity-management/internal/db"
	"entity-management/internal/push"
	"entity-management/internal/vehicle"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// The service schedule of the fleet (migration 029).
//
// The odometer of a unit is accumulated by the server while the positions
// come in (see reportPositionSQL in vehicles.go). Everything else follows
// from it: when the next service is due, how far the unit is from it, and
// whether the operator has to be warned about it.

// serviceHistorySQL reads the service history of one unit, newest first.
const serviceHistorySQL = `SELECT id, vehicle_id, serviced_at, odometer_m, note, recorded_by
  FROM vehicle_services
 WHERE vehicle_id = $1
 ORDER BY serviced_at DESC, id DESC`

// serviceReminderSQL reads the service state of every unit: what the
// odometer says, what the schedule is, and which warning was sent last.
//
// The schedule itself is not computed here — the rows are read as they are
// stored and vehicle.ComputeService works them out, so the sweep and the
// fleet list always say the same thing about a unit.
const serviceReminderSQL = `SELECT v.id, v.code, v.name,
       v.total_distance_m, v.service_interval_km, v.last_service_at,
       v.last_service_odometer_m, v.service_notified_level
  FROM vehicles v`

// markServiceNotifiedSQL writes down which warning was sent for a unit, so
// the sweep does not say it twice.
//
// The update is guarded: it only happens when the level of the row is still
// the one the sweep read, so two sweeps that run at the same time cannot
// both claim the same warning.
const markServiceNotifiedSQL = `UPDATE vehicles
    SET service_notified_level = $2
  WHERE id = $1
    AND service_notified_level = $3`

// vehicleReadable reports whether the signed-in account may read the data of
// this vehicle.
//
// Everyone but a driver may; a driver only sees the unit that is theirs, the
// same rule the fleet list follows. It writes the error response itself, so
// the caller can simply return.
func vehicleReadable(c *gin.Context, pool *db.Pool, id int64) bool {
	scope := driverScope(c)
	if scope == nil {
		return true
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	allowed, err := vehicleBelongsToDriver(ctx, pool, id, *scope)
	if err != nil {
		log.Printf("services: driver scope for vehicle %d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return false
	}
	if !allowed {
		c.JSON(http.StatusForbidden, errorBody("forbidden", "this vehicle is not assigned to you"))
		return false
	}
	return true
}

// ListServices answers GET /api/vehicles/:id/services: the service history
// of one unit, newest first.
//
// A driver only reads the history of their own unit; every other role reads
// the history of every unit.
func (h *VehicleHandler) ListServices(c *gin.Context) {
	id, ok := vehicleIDParam(c)
	if !ok {
		return
	}

	if !vehicleReadable(c, h.db, id) {
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	rows, err := h.db.Query(ctx, serviceHistorySQL, id)
	if err != nil {
		log.Printf("services: history of vehicle %d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	defer rows.Close()

	services := make([]vehicle.ServiceRecord, 0)
	for rows.Next() {
		record, err := scanServiceRecord(rows)
		if err != nil {
			log.Printf("services: scan the history of vehicle %d failed: %v", id, err)
			c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
			return
		}
		services = append(services, record)
	}
	if err := rows.Err(); err != nil {
		log.Printf("services: read the history of vehicle %d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{"services": services, "count": len(services)}})
}

// scanServiceRecord reads one row of the service history.
func scanServiceRecord(row rowScanner) (vehicle.ServiceRecord, error) {
	var record vehicle.ServiceRecord
	err := row.Scan(&record.ID, &record.VehicleID, &record.ServicedAt,
		&record.OdometerM, &record.Note, &record.RecordedBy)
	if err != nil {
		return vehicle.ServiceRecord{}, err
	}

	return record, nil
}

// RecordService answers POST /api/vehicles/:id/services: a service was done,
// so the schedule of the unit starts over from the odometer it has right now.
//
// Only an admin writes a service down — the driver does not service the
// truck, the workshop does.
//
// The whole thing happens in one transaction: the row of the history, the
// baseline of the unit and the marker of the last warning. Either the
// service counts or nothing changed at all.
func (h *VehicleHandler) RecordService(c *gin.Context) {
	id, ok := vehicleIDParam(c)
	if !ok {
		return
	}

	var in vehicle.ServiceRecordInput
	if err := c.ShouldBindJSON(&in); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be valid JSON"))
		return
	}

	now := time.Now()
	in.Normalize(now)
	if fields := in.Validate(now); len(fields) > 0 {
		c.JSON(http.StatusBadRequest, validationErrorBody(fields))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	tx, err := h.db.Begin(ctx)
	if err != nil {
		log.Printf("services: begin failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The row of the unit is locked while the service is written down, so
	// two services cannot claim the same odometer.
	var (
		odometerM float64
		interval  *float64
	)
	err = tx.QueryRow(ctx,
		`SELECT total_distance_m, service_interval_km FROM vehicles WHERE id = $1 FOR UPDATE`,
		id).Scan(&odometerM, &interval)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		c.JSON(http.StatusNotFound, errorBody("not_found", "vehicle not found"))
		return
	case err != nil:
		log.Printf("services: read vehicle %d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	var record vehicle.ServiceRecord
	err = tx.QueryRow(ctx, `
        INSERT INTO vehicle_services (vehicle_id, serviced_at, odometer_m, note, recorded_by)
        VALUES ($1, $2, $3, $4, $5)
        RETURNING id, vehicle_id, serviced_at, odometer_m, note, recorded_by`,
		id, in.At(), odometerM, in.Note, stopActor(c)).Scan(
		&record.ID, &record.VehicleID, &record.ServicedAt,
		&record.OdometerM, &record.Note, &record.RecordedBy)
	if err != nil {
		log.Printf("services: insert the service of vehicle %d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	// The schedule starts over from the odometer the unit has right now, and
	// the marker of the last warning is cleared: the next cycle of the
	// schedule warns again.
	if _, err := tx.Exec(ctx, `
        UPDATE vehicles
           SET last_service_at = $2, last_service_odometer_m = $3, service_notified_level = ''
         WHERE id = $1`,
		id, in.At(), odometerM); err != nil {
		log.Printf("services: update the schedule of vehicle %d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("services: commit failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	// The schedule of the unit after the service: the baseline is the
	// odometer it has right now, so the next service is a whole interval
	// away again.
	servicedAt := in.At()
	schedule := vehicle.ComputeService(
		odometerM, interval, &servicedAt, odometerM,
		h.serviceIntervalKM, h.serviceWarnKM,
	)

	c.JSON(http.StatusCreated, gin.H{"data": gin.H{
		"service":        record,
		"service_status": schedule,
	}})
}

// CheckServiceReminders warns the operators about the units that come close
// to their next service, and about the ones that drove past it.
//
// It is a sweep, like the offline sweep and the idle sweep: it runs every
// minute and it does the same thing every time. A unit is warned once per
// level — the marker of the unit remembers which warning was sent last, and
// a unit that stays at its level is not warned again. Only a written service
// record clears the marker, so the next cycle of the schedule warns again.
//
// A unit that is still fine is left alone: no push, and no change of the
// marker either.
//
// It returns how many reminders were sent.
func (h *VehicleHandler) CheckServiceReminders(ctx context.Context) (int, error) {
	rows, err := h.db.Query(ctx, serviceReminderSQL)
	if err != nil {
		return 0, fmt.Errorf("read the service state of the fleet: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		id       int64
		label    string
		level    string
		notified string
		schedule vehicle.ServiceSchedule
	}

	candidates := make([]candidate, 0)
	for rows.Next() {
		var (
			id                   int64
			code, name           string
			totalDistanceM       float64
			intervalKM           *float64
			lastServiceAt        *time.Time
			lastServiceOdometerM float64
			serviceNotifiedLevel string
		)
		if err := rows.Scan(&id, &code, &name, &totalDistanceM, &intervalKM,
			&lastServiceAt, &lastServiceOdometerM, &serviceNotifiedLevel); err != nil {
			return 0, fmt.Errorf("scan the service state of a unit: %w", err)
		}

		schedule := vehicle.ComputeService(
			totalDistanceM, intervalKM, lastServiceAt, lastServiceOdometerM,
			h.serviceIntervalKM, h.serviceWarnKM,
		)

		// A unit that is fine stays quiet, and a unit that was warned
		// about at this level already is not warned about twice.
		if schedule.Status == vehicle.ServiceStatusOK {
			continue
		}
		if vehicle.ServiceLevelRank(schedule.Status) <= vehicle.ServiceLevelRank(serviceNotifiedLevel) {
			continue
		}

		candidates = append(candidates, candidate{
			id:       id,
			label:    serviceLabel(code, name),
			level:    schedule.Status,
			notified: serviceNotifiedLevel,
			schedule: schedule,
		})
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read the service state of the fleet: %w", err)
	}

	sent := 0
	for _, c := range candidates {
		// The push goes out first: a warning that fails is only a lost
		// warning, a marker that is written too early would swallow the
		// next attempt as well.
		h.notifyServiceReminder(c.id, c.label, c.level, c.schedule)
		sent++

		// The marker remembers that this level was warned about, so the
		// next sweep leaves the unit alone.
		tag, err := h.db.Exec(ctx, markServiceNotifiedSQL, c.id, c.level, c.notified)
		if err != nil {
			// The warning went out but the marker could not be written:
			// the unit may be warned about again. Logged, not fatal.
			log.Printf("services: mark the warning of vehicle %d failed: %v", c.id, err)
			continue
		}
		if tag.RowsAffected() == 0 {
			// Another sweep was faster and wrote the marker already.
			continue
		}
		log.Printf("services: unit %d is %s (%s)", c.id, c.level, c.label)
	}

	return sent, nil
}

// notifyServiceReminder tells the operators that a unit comes close to its
// next service, or that it drove past it.
//
// The push goes out in the background, so it never holds the sweep up. A
// server without Web Push keys simply sends nothing: the marker is written
// either way, so the warning is not repeated every minute.
func (h *VehicleHandler) notifyServiceReminder(vehicleID int64, label, level string, schedule vehicle.ServiceSchedule) {
	if h.push == nil {
		return
	}

	var payload push.Payload
	switch level {
	case vehicle.ServiceLevelOverdue:
		payload = push.Payload{
			Title: "Servis terlewat",
			Body: fmt.Sprintf("%s sudah lewat %s km dari jadwal servis",
				label, formatKmShort(-schedule.RemainingKM)),
			Tag: fmt.Sprintf("service-%d-%s", vehicleID, level),
			URL: "/",
		}
	default:
		payload = push.Payload{
			Title: "Servis sebentar lagi",
			Body: fmt.Sprintf("%s sisa %s km menuju servis %s km",
				label, formatKmShort(schedule.RemainingKM), formatKmShort(schedule.IntervalKM)),
			Tag: fmt.Sprintf("service-%d-%s", vehicleID, level),
			URL: "/",
		}
	}

	push.Background(func(ctx context.Context) {
		h.push.NotifyOperators(ctx, 0, payload)
	})
}

// serviceLabel is how a unit is named in a reminder: the code if it has one,
// the name otherwise.
func serviceLabel(code, name string) string {
	if label := strings.TrimSpace(code); label != "" {
		return label
	}
	if label := strings.TrimSpace(name); label != "" {
		return label
	}
	return "unit"
}

// formatKmShort writes a number of kilometres the way the operator reads it:
// no trailing zeros, at most two decimals.
func formatKmShort(km float64) string {
	return strconv.FormatFloat(round2(km), 'f', -1, 64)
}
