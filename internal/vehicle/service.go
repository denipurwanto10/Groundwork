package vehicle

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// The service schedule of a unit (migration 029).
//
// The odometer of a unit is accumulated by the server, one position report at
// a time. The schedule is counted from that odometer: the next service is
// due when the odometer reaches the reading of the last service plus the
// interval of the unit.
//
// The schedule itself is worked out in ONE place — ComputeService — so the
// fleet list, the sweep of the reminders and the answer of the service
// endpoint can never disagree about the same unit.

// The three states of the service schedule.
const (
	// ServiceStatusOK means the unit still has room before its next service.
	ServiceStatusOK = "ok"
	// ServiceStatusDueSoon means the unit is close to its next service: it
	// is still inside the schedule, but the warning distance is used up.
	ServiceStatusDueSoon = "due_soon"
	// ServiceStatusOverdue means the unit drove past its next service.
	ServiceStatusOverdue = "overdue"
)

// The warning levels of the reminders, from the quietest to the loudest.
const (
	// ServiceLevelNone means nothing was warned about in this cycle yet.
	ServiceLevelNone = ""
	// ServiceLevelDueSoon is the warning of a unit that comes close to its
	// next service.
	ServiceLevelDueSoon = ServiceStatusDueSoon
	// ServiceLevelOverdue is the warning of a unit that drove past its next
	// service.
	ServiceLevelOverdue = ServiceStatusOverdue
)

// The defaults of the schedule. They mirror the two variables of the .env
// (SERVICE_INTERVAL_DEFAULT_KM and SERVICE_WARN_KM) and are used when the
// server did not configure them.
const (
	// DefaultServiceIntervalKM is the interval a unit falls back to when it
	// has no interval of its own.
	DefaultServiceIntervalKM = 5000.0
	// DefaultServiceWarnKM is how close to the schedule a unit has to come
	// before it counts as due.
	DefaultServiceWarnKM = 500.0
)

// MaxServiceIntervalKM is the longest interval a unit may have. It mirrors
// the CHECK constraint of migration 029.
const MaxServiceIntervalKM = 100000.0

// MaxServiceNote is how long the note of a service may be. It says what was
// done, not why: half a page is plenty.
const MaxServiceNote = 500

// ServiceSchedule is the service schedule of a unit, as the API hands it out.
type ServiceSchedule struct {
	// IntervalKM is the interval the unit is serviced on: its own one, or
	// the default of the server.
	IntervalKM float64 `json:"interval_km"`
	// LastServiceAt is when the unit was serviced last. Null while it has
	// never been serviced.
	LastServiceAt *time.Time `json:"last_service_at"`
	// LastServiceKM is the odometer at that moment.
	LastServiceKM float64 `json:"last_service_km"`
	// NextDueKM is the odometer at which the next service is due.
	NextDueKM float64 `json:"next_due_km"`
	// RemainingKM is how many kilometres are left before the next service.
	// It is negative once the unit drove past it.
	RemainingKM float64 `json:"remaining_km"`
	// Status is one of ok, due_soon and overdue.
	Status string `json:"status"`
}

// ServiceRecord is one entry of the service history of a unit.
type ServiceRecord struct {
	ID        int64 `json:"id"`
	VehicleID int64 `json:"vehicle_id"`
	// ServicedAt is when the service was done.
	ServicedAt time.Time `json:"serviced_at"`
	// OdometerM is the odometer of the unit at that moment, in metres.
	OdometerM float64 `json:"odometer_m"`
	// Note says what was done. It may be empty.
	Note string `json:"note"`
	// RecordedBy is the account that wrote the record down. Null once the
	// account is gone.
	RecordedBy *int64 `json:"recorded_by"`
}

// ServiceLevelRank puts the warning levels in order: nothing (0), due soon
// (1) and overdue (2).
//
// The sweep compares the level of a unit with the one it warned about last,
// so a unit that escalates is warned again and a unit that stays at its
// level is not warned twice.
func ServiceLevelRank(level string) int {
	switch level {
	case ServiceLevelDueSoon:
		return 1
	case ServiceLevelOverdue:
		return 2
	default:
		return 0
	}
}

// ComputeService works out the service schedule of a unit.
//
// The numbers come in as they are stored: the odometer and the baseline of
// the last service in metres, the interval of the unit in kilometres (null
// when the unit has none of its own). The default interval and the warning
// distance of the server fill the gaps.
//
// The rule is simple:
//
//   - the next service is due at the odometer of the last service plus the
//     interval;
//   - what is left is that reading minus the odometer of the unit right now;
//   - nothing left means the unit drove past its schedule (overdue), a small
//     remainder means it comes close to it (due_soon), and everything else is
//     fine (ok).
//
// The status is worked out from the exact numbers, the numbers themselves are
// rounded to two decimals — the way the reports round their kilometres.
func ComputeService(totalDistanceM float64, intervalKM *float64, lastServiceAt *time.Time, lastServiceOdometerM, defaultIntervalKM, warnKM float64) ServiceSchedule {
	interval := defaultIntervalKM
	if intervalKM != nil && *intervalKM > 0 {
		interval = *intervalKM
	}
	if interval <= 0 {
		interval = DefaultServiceIntervalKM
	}
	if warnKM < 0 {
		warnKM = 0
	}

	totalKM := totalDistanceM / 1000
	lastKM := lastServiceOdometerM / 1000
	nextDueKM := lastKM + interval
	remainingKM := nextDueKM - totalKM

	status := ServiceStatusOK
	switch {
	case remainingKM <= 0:
		status = ServiceStatusOverdue
	case remainingKM <= warnKM:
		status = ServiceStatusDueSoon
	}

	return ServiceSchedule{
		IntervalKM:    round2(interval),
		LastServiceAt: lastServiceAt,
		LastServiceKM: round2(lastKM),
		NextDueKM:     round2(nextDueKM),
		RemainingKM:   round2(remainingKM),
		Status:        status,
	}
}

// ServiceRecordInput is the body of POST /api/vehicles/:id/services: what
// was done, and when.
type ServiceRecordInput struct {
	// Note says what was done. It may be empty.
	Note string `json:"note"`
	// ServicedAt is when the service was done: a timestamp (RFC3339) or a
	// plain day (YYYY-MM-DD). Empty means "now".
	ServicedAt string `json:"serviced_at"`

	// at is the moment of the service, read by Normalize. It is the zero
	// time when the date of the body could not be read.
	at time.Time
}

// At is the moment of the service: what Normalize read out of the body.
func (in ServiceRecordInput) At() time.Time {
	return in.at
}

// Normalize trims the note and reads the moment of the service.
//
// A body without a date means the service was done now. A date that cannot
// be read leaves the zero time behind, and Validate rejects it.
func (in *ServiceRecordInput) Normalize(now time.Time) {
	in.Note = strings.TrimSpace(in.Note)
	in.at = now

	raw := strings.TrimSpace(in.ServicedAt)
	if raw == "" {
		return
	}
	t, ok := parseServiceTime(raw)
	if !ok {
		in.at = time.Time{}
		return
	}
	in.at = t
}

// Validate reports every rejected field at once.
func (in *ServiceRecordInput) Validate(now time.Time) FieldErrors {
	errs := FieldErrors{}

	if len(in.Note) > MaxServiceNote {
		errs["note"] = fmt.Sprintf("note must be at most %d characters", MaxServiceNote)
	}
	if in.at.IsZero() {
		errs["serviced_at"] = "serviced_at must be a date (YYYY-MM-DD) or a timestamp (RFC3339)"
		return errs
	}
	// A service that is done later than now is not a service yet.
	if in.at.After(now) {
		errs["serviced_at"] = "serviced_at must not be in the future"
	}

	return errs
}

// parseServiceTime reads the moment of a service. RFC3339 is the exact form,
// a plain day is what a person types, and the two short forms in between are
// accepted for the clients that send a local timestamp.
func parseServiceTime(raw string) (time.Time, bool) {
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return t, true
		}
	}

	return time.Time{}, false
}

// round2 rounds to two decimals, the way the reports round their kilometres.
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
