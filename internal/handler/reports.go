package handler

import (
	"context"
	"encoding/csv"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"entity-management/internal/db"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// ReportHandler serves the operational report: what the fleet did in a period
// of time, as numbers on the screen and as a CSV file on the disk.
//
// Every role may read the report. A driver only reads the report of their own
// unit: the kilometres of somebody else's truck are none of their business,
// the same rule the fleet list and the history follow.
type ReportHandler struct {
	db *db.Pool
}

func NewReportHandler(pool *db.Pool) *ReportHandler {
	return &ReportHandler{db: pool}
}

// The period a report may cover.
const (
	// reportDefaultDays is the length of the period a request without dates
	// asks for: the last seven days, today included.
	reportDefaultDays = 7
	// reportMaxRange is the longest window one request may ask for. A report
	// that covers a quarter is already more than anybody reads.
	reportMaxRange = 92 * 24 * time.Hour
)

// reportPeriod is the window the report covers.
type reportPeriod struct {
	// From is the first instant of the period, included.
	From time.Time
	// To is the first instant after the period, excluded.
	To time.Time
	// FromDate and ToDate are the days as the operator reads them.
	FromDate string
	ToDate   string
}

// Report is the answer of GET /api/reports.
type Report struct {
	Period    ReportPeriod    `json:"period"`
	Totals    ReportTotals    `json:"totals"`
	ByVehicle []ReportVehicle `json:"by_vehicle"`
	ByZone    []ReportZone    `json:"by_zone"`
}

// ReportPeriod is the window the report covers, as it is shown.
type ReportPeriod struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ReportTotals are the numbers of the whole period.
type ReportTotals struct {
	// DistanceKM is how far the whole fleet drove in the period.
	DistanceKM float64 `json:"distance_km"`
	// RoutesCompleted are the routes that were finished in the period.
	RoutesCompleted int `json:"routes_completed"`
	// RoutesTotal are the routes that ran in the period, finished or not.
	RoutesTotal int `json:"routes_total"`
	// TasksCompleted are the work orders of the period that are done.
	TasksCompleted int `json:"tasks_completed"`
	// TasksTotal are the work orders of the period.
	TasksTotal int `json:"tasks_total"`
	// GeofenceEnter and GeofenceExit count the crossings of the zone borders.
	GeofenceEnter int `json:"geofence_enter"`
	GeofenceExit  int `json:"geofence_exit"`
}

// ReportVehicle is one unit and what it did in the period.
type ReportVehicle struct {
	VehicleID       int64   `json:"vehicle_id"`
	VehicleCode     string  `json:"vehicle_code"`
	VehicleName     string  `json:"vehicle_name"`
	DriverName      string  `json:"driver_name"`
	DistanceKM      float64 `json:"distance_km"`
	RoutesCompleted int     `json:"routes_completed"`
	RoutesTotal     int     `json:"routes_total"`
	GeofenceEnter   int     `json:"geofence_enter"`
	GeofenceExit    int     `json:"geofence_exit"`
}

// ReportZone is one work area and how often it was crossed in the period.
type ReportZone struct {
	ZoneID         int64  `json:"zone_id"`
	ZoneName       string `json:"zone_name"`
	Enter          int    `json:"enter"`
	Exit           int    `json:"exit"`
	Total          int    `json:"total"`
	UniqueVehicles int    `json:"unique_vehicles"`
}

// Get answers GET /api/reports with the operational report of the period.
func (h *ReportHandler) Get(c *gin.Context) {
	report, ok := h.build(c)
	if !ok {
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": report})
}

// ExportCSV answers GET /api/reports/export.csv with the same numbers as a
// file that opens in every spreadsheet.
//
// The CSV is written with the standard library only: a report of a small
// workspace is a few dozen lines, and a spreadsheet library would be a heavy
// dependency for it.
func (h *ReportHandler) ExportCSV(c *gin.Context) {
	report, ok := h.build(c)
	if !ok {
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(
		"attachment; filename=\"groundwork-report-%s-%s.csv\"",
		report.Period.From, report.Period.To,
	))

	w := csv.NewWriter(c.Writer)
	if err := w.Write([]string{
		"vehicle_id", "vehicle_code", "vehicle_name", "driver_name",
		"distance_km", "routes_completed", "routes_total",
		"geofence_enter", "geofence_exit",
	}); err != nil {
		log.Printf("reports: write csv header failed: %v", err)
		return
	}

	for _, v := range report.ByVehicle {
		if err := w.Write([]string{
			strconv.FormatInt(v.VehicleID, 10),
			v.VehicleCode,
			v.VehicleName,
			v.DriverName,
			strconv.FormatFloat(v.DistanceKM, 'f', 2, 64),
			strconv.Itoa(v.RoutesCompleted),
			strconv.Itoa(v.RoutesTotal),
			strconv.Itoa(v.GeofenceEnter),
			strconv.Itoa(v.GeofenceExit),
		}); err != nil {
			log.Printf("reports: write csv row failed: %v", err)
			return
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		log.Printf("reports: flush csv failed: %v", err)
	}
}

// build reads the request, asks the database for the period and hands the
// report to the caller. It answers the request itself when something is
// wrong, and says so by returning false.
func (h *ReportHandler) build(c *gin.Context) (Report, bool) {
	period, err := reportPeriodFrom(c.Query("from"), c.Query("to"), time.Now())
	if err != nil {
		c.JSON(http.StatusBadRequest, errorBody("validation_failed", err.Error()))
		return Report{}, false
	}

	vehicleID, err := reportVehicleParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorBody("validation_failed", err.Error()))
		return Report{}, false
	}

	// The report counts a whole period of positions, so it needs more room
	// than a single request of the API: a quarter of history is a lot of
	// rows for the database to add up.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	scope := driverScope(c)

	// A driver may ask for their own unit only: the report of another truck
	// is refused, the same way the history refuses it.
	if scope != nil && vehicleID != nil {
		allowed, err := vehicleBelongsToDriver(ctx, h.db, *vehicleID, *scope)
		if err != nil {
			log.Printf("reports: driver scope for vehicle %d failed: %v", *vehicleID, err)
			c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
			return Report{}, false
		}
		if !allowed {
			c.JSON(http.StatusForbidden, errorBody("forbidden", "this vehicle is not assigned to you"))
			return Report{}, false
		}
	}

	report, err := h.aggregate(ctx, period, scope, vehicleID)
	if err != nil {
		log.Printf("reports: aggregate failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return Report{}, false
	}

	return report, true
}

// aggregate asks the database for the whole report.
//
// Everything is counted there: the rows of the positions never travel to Go,
// they are only summed by the database.
//
// The query is sent in the simple protocol on purpose. A prepared statement
// would be planned once with the values of the first executions and then
// reused with a generic plan, and that plan is a bad one here: without the
// real dates the planner believes the period holds a handful of positions
// instead of a hundred thousand, and the report takes eight times longer.
// The simple protocol sends the dates with the query, so every period is
// planned with the numbers it really holds.
func (h *ReportHandler) aggregate(ctx context.Context, p reportPeriod, driverID, vehicleID *int64) (Report, error) {
	var (
		distanceKM      float64
		routesCompleted int
		routesTotal     int
		tasksCompleted  int
		tasksTotal      int
		geofenceEnter   int
		geofenceExit    int
		byVehicleRaw    []byte
		byZoneRaw       []byte
	)

	row := h.db.QueryRow(ctx, reportSQL,
		pgx.QueryExecModeSimpleProtocol,
		reportScopeArg(driverID), p.From, p.To, reportVehicleArg(vehicleID))
	if err := row.Scan(
		&distanceKM,
		&routesCompleted,
		&routesTotal,
		&tasksCompleted,
		&tasksTotal,
		&geofenceEnter,
		&geofenceExit,
		&byVehicleRaw,
		&byZoneRaw,
	); err != nil {
		return Report{}, err
	}

	report := Report{
		Period: ReportPeriod{From: p.FromDate, To: p.ToDate},
		Totals: ReportTotals{
			DistanceKM:      round2(distanceKM),
			RoutesCompleted: routesCompleted,
			RoutesTotal:     routesTotal,
			TasksCompleted:  tasksCompleted,
			TasksTotal:      tasksTotal,
			GeofenceEnter:   geofenceEnter,
			GeofenceExit:    geofenceExit,
		},
		ByVehicle: make([]ReportVehicle, 0),
		ByZone:    make([]ReportZone, 0),
	}

	if err := decodeJSONColumn(byVehicleRaw, &report.ByVehicle); err != nil {
		return Report{}, err
	}
	if err := decodeJSONColumn(byZoneRaw, &report.ByZone); err != nil {
		return Report{}, err
	}

	// The distance is rounded like the one of the Overview tab, so the report
	// and the overview never disagree about the same kilometre.
	for i := range report.ByVehicle {
		report.ByVehicle[i].DistanceKM = round2(report.ByVehicle[i].DistanceKM)
	}

	return report, nil
}

// reportVehicleParam reads the ?vehicle_id= filter of a report.
func reportVehicleParam(c *gin.Context) (*int64, error) {
	raw := strings.TrimSpace(c.Query("vehicle_id"))
	if raw == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return nil, fmt.Errorf("vehicle_id must be a positive integer")
	}
	return &id, nil
}

// reportScopeArg turns the driver scope into a query argument: the id of the
// driver, or NULL for the roles that see the whole fleet.
func reportScopeArg(driverID *int64) any {
	if driverID == nil {
		return nil
	}
	return *driverID
}

// reportVehicleArg turns the vehicle filter into a query argument: the id of
// the unit, or NULL for all of them.
func reportVehicleArg(vehicleID *int64) any {
	if vehicleID == nil {
		return nil
	}
	return *vehicleID
}

// reportPeriodFrom reads the period of a report.
//
// The two ends accept a plain day (YYYY-MM-DD) or a timestamp (RFC3339). A
// plain day covers the whole day: "to=2026-10-21" includes everything that
// happened on that day. A timestamp is the exact instant it names, and the
// period ends there.
//
// A missing end means the day of the start, a missing start means the day
// before the end. Without any date the report covers the last seven days,
// today included.
func reportPeriodFrom(fromRaw, toRaw string, now time.Time) (reportPeriod, error) {
	loc := now.Location()
	fromRaw = strings.TrimSpace(fromRaw)
	toRaw = strings.TrimSpace(toRaw)

	if fromRaw == "" && toRaw == "" {
		from := startOfDay(now.AddDate(0, 0, -(reportDefaultDays-1)), loc)
		to := startOfDay(now.AddDate(0, 0, 1), loc)
		return reportPeriod{
			From:     from,
			To:       to,
			FromDate: from.Format("2006-01-02"),
			ToDate:   to.AddDate(0, 0, -1).Format("2006-01-02"),
		}, nil
	}

	var (
		from, to       time.Time
		toIsDay        bool
		err            error
		fromWasWritten bool
		toWasWritten   bool
	)

	if fromRaw != "" {
		// The start of the period is the instant itself, also when it is a
		// plain day: a day starts at midnight anyway.
		from, _, err = parseReportTime(fromRaw, loc)
		if err != nil {
			return reportPeriod{}, fmt.Errorf("from: %w", err)
		}
		fromWasWritten = true
	}
	if toRaw != "" {
		to, toIsDay, err = parseReportTime(toRaw, loc)
		if err != nil {
			return reportPeriod{}, fmt.Errorf("to: %w", err)
		}
		toWasWritten = true
	}

	// A missing end means the day of the start, a missing start the day
	// before the end.
	if !toWasWritten {
		to = from
		toIsDay = true
	}
	if !fromWasWritten {
		from = to.AddDate(0, 0, -1)
	}

	// The end of the period is the first instant after it: the whole day when
	// the end is a plain day, the instant itself when it is a timestamp.
	end := to
	if toIsDay {
		end = startOfDay(to, loc).AddDate(0, 0, 1)
	}

	if from.After(end) {
		return reportPeriod{}, fmt.Errorf("from must not be after to")
	}
	if end.Sub(from) > reportMaxRange {
		return reportPeriod{}, fmt.Errorf("the range must be at most 92 days")
	}

	// The days as the operator reads them: the last day that is inside the
	// period, not the instant the period ends at.
	toDate := to
	if toIsDay {
		toDate = startOfDay(to, loc)
	} else {
		toDate = to.Add(-time.Second)
	}

	return reportPeriod{
		From:     from,
		To:       end,
		FromDate: from.Format("2006-01-02"),
		ToDate:   toDate.Format("2006-01-02"),
	}, nil
}

// parseReportTime reads one end of a period. RFC3339 is the exact form, the
// plain day is what a person types.
func parseReportTime(raw string, loc *time.Location) (time.Time, bool, error) {
	if t, err := time.ParseInLocation("2006-01-02", raw, loc); err == nil {
		return t, true, nil
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t, false, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("%q is not a date (YYYY-MM-DD) or a timestamp (RFC3339)", raw)
}

// startOfDay is the first instant of a day in the given place.
func startOfDay(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// reportSQL is the whole report in one round trip.
//
// $1 is the id of the signed-in driver, or NULL for the roles that see the
// whole fleet. $2 and $3 are the ends of the period, $3 excluded. $4 is the
// unit the operator picked, or NULL for all of them.
//
// The distance is the one of the Overview tab: the sum of the steps between
// the reported positions, measured on the sphere and not on the flat map. Two
// guards keep the number honest. The step from the last fix before the
// period counts, so the kilometres driven across the first day of the period
// are not lost. And a step that would need an impossible speed (more than
// 200 km/h) is left out, because a unit that is placed at the origin of a
// new route has not driven that distance.
//
// A unit without a single step in the period still gets a row: the report
// shows what everybody did, not only what somebody did.
const reportSQL = `WITH scoped AS (
    SELECT v.id, v.code, v.name,
           COALESCE(NULLIF(u.name, ''), u.username, '') AS driver_name
      FROM vehicles v
      LEFT JOIN users u ON u.id = v.driver_id
     WHERE ($1::bigint IS NULL OR v.driver_id = $1)
       AND ($4::bigint IS NULL OR v.id = $4)
), predecessor AS (
    SELECT s.id AS vehicle_id, p.lat, p.lng, p.recorded_at
      FROM scoped s
     CROSS JOIN LATERAL (
           SELECT prev.lat, prev.lng, prev.recorded_at
             FROM vehicle_positions prev
            WHERE prev.vehicle_id = s.id
              AND prev.recorded_at < $2
            ORDER BY prev.recorded_at DESC
            LIMIT 1
     ) p
), period_pos AS (
    SELECT vp.vehicle_id, vp.lat, vp.lng, vp.recorded_at
      FROM vehicle_positions vp
      JOIN scoped s ON s.id = vp.vehicle_id
     WHERE vp.recorded_at >= $2
       AND vp.recorded_at <  $3
    UNION ALL
    SELECT vehicle_id, lat, lng, recorded_at FROM predecessor
), steps AS (
    SELECT vehicle_id, recorded_at,
           ST_Distance(
               ST_SetSRID(ST_MakePoint(lng, lat), 4326)::geography,
               ST_SetSRID(ST_MakePoint(prev_lng, prev_lat), 4326)::geography) AS meters,
           EXTRACT(EPOCH FROM (recorded_at - prev_at)) AS seconds
      FROM (
            SELECT vehicle_id, lat, lng, recorded_at,
                   lag(lat)         OVER w AS prev_lat,
                   lag(lng)         OVER w AS prev_lng,
                   lag(recorded_at) OVER w AS prev_at
              FROM period_pos
            WINDOW w AS (PARTITION BY vehicle_id ORDER BY recorded_at)
      ) p
     WHERE prev_lat IS NOT NULL
       AND recorded_at >= $2
       AND recorded_at <  $3
), distance AS (
    SELECT vehicle_id, sum(meters) / 1000.0::double precision AS km
      FROM steps
     WHERE seconds > 0 AND meters / seconds * 3.6 <= 200
     GROUP BY vehicle_id
), route_rows AS (
    SELECT r.vehicle_id,
           count(*) AS total,
           count(*) FILTER (WHERE r.status = 'completed'
                              AND r.ended_at >= $2
                              AND r.ended_at <  $3) AS completed
      FROM vehicle_routes r
     WHERE r.vehicle_id IN (SELECT id FROM scoped)
       AND r.created_at < $3
       AND (r.ended_at IS NULL OR r.ended_at >= $2)
     GROUP BY r.vehicle_id
), event_rows AS (
    SELECT g.vehicle_id,
           count(*) FILTER (WHERE g.event_type = 'ENTER') AS enter,
           count(*) FILTER (WHERE g.event_type = 'EXIT') AS exit
      FROM geofence_events g
     WHERE g.vehicle_id IN (SELECT id FROM scoped)
       AND g.created_at >= $2
       AND g.created_at <  $3
     GROUP BY g.vehicle_id
), task_rows AS (
    SELECT count(*) AS total,
           count(*) FILTER (WHERE t.status = 'completed') AS completed
      FROM tasks t
     WHERE t.created_at >= $2
       AND t.created_at <  $3
       AND ($1::bigint IS NULL OR t.assigned_user_id = $1)
), vehicle_rows AS (
    SELECT s.id AS vehicle_id,
           COALESCE(s.code, '') AS vehicle_code,
           COALESCE(s.name, '') AS vehicle_name,
           s.driver_name,
           COALESCE(d.km, 0)::double precision AS distance_km,
           COALESCE(r.completed, 0) AS routes_completed,
           COALESCE(r.total, 0) AS routes_total,
           COALESCE(e.enter, 0) AS geofence_enter,
           COALESCE(e.exit, 0) AS geofence_exit
      FROM scoped s
      LEFT JOIN distance    d ON d.vehicle_id = s.id
      LEFT JOIN route_rows r ON r.vehicle_id = s.id
      LEFT JOIN event_rows e ON e.vehicle_id = s.id
), zone_rows AS (
    SELECT g.zone_id,
           COALESCE(z.name, '') AS zone_name,
           count(*) FILTER (WHERE g.event_type = 'ENTER') AS enter,
           count(*) FILTER (WHERE g.event_type = 'EXIT') AS exit,
           count(*) AS total,
           count(DISTINCT g.vehicle_id) AS unique_vehicles
      FROM geofence_events g
      JOIN scoped s ON s.id = g.vehicle_id
      LEFT JOIN zones z ON z.id = g.zone_id
     WHERE g.created_at >= $2
       AND g.created_at <  $3
     GROUP BY g.zone_id, z.name
)
SELECT (SELECT COALESCE(sum(km), 0)::double precision FROM distance),
       (SELECT COALESCE(sum(routes_completed), 0) FROM vehicle_rows),
       (SELECT COALESCE(sum(routes_total), 0) FROM vehicle_rows),
       (SELECT completed FROM task_rows),
       (SELECT total FROM task_rows),
       (SELECT COALESCE(sum(geofence_enter), 0) FROM vehicle_rows),
       (SELECT COALESCE(sum(geofence_exit), 0) FROM vehicle_rows),
       COALESCE((
           SELECT jsonb_agg(to_jsonb(t) ORDER BY round(t.distance_km::numeric, 2) DESC, t.vehicle_code)
             FROM vehicle_rows t
       ), '[]'::jsonb),
       COALESCE((
           SELECT jsonb_agg(to_jsonb(t) ORDER BY t.total DESC, t.zone_name, t.zone_id)
             FROM zone_rows t
       ), '[]'::jsonb)`
