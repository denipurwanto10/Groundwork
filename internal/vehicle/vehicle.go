// Package vehicle holds the live-tracking models and the validation rules the
// HTTP layer applies before a position reaches PostGIS or the WebSocket hub.
//
// The rules mirror the CHECK constraints in migrations/006_vehicles.up.sql, so
// a value the API rejects can never reach the database either.
package vehicle

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
)

// Vehicle kinds.
const (
	KindTruck      = "truck"
	KindVan        = "van"
	KindMotorcycle = "motorcycle"
	KindPickup     = "pickup"
)

// Vehicle statuses.
const (
	StatusIdle    = "idle"
	StatusMoving  = "moving"
	StatusOffline = "offline"
)

var (
	kinds = map[string]bool{
		KindTruck:      true,
		KindVan:        true,
		KindMotorcycle: true,
		KindPickup:     true,
	}
	statuses = map[string]bool{
		StatusIdle:    true,
		StatusMoving:  true,
		StatusOffline: true,
	}
)

// FieldErrors maps a field name to the reason it was rejected. The handler
// turns it into the same validation envelope the entity and zone endpoints use.
type FieldErrors map[string]string

// Error implements the error interface so a FieldErrors can be returned as one.
func (f FieldErrors) Error() string {
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+f[k])
	}
	return strings.Join(parts, "; ")
}

// Vehicle is one tracked unit together with its latest known position.
type Vehicle struct {
	ID         int64      `json:"id"`
	Code       string     `json:"code"`
	Name       string     `json:"name"`
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	Lat        *float64   `json:"lat"`
	Lng        *float64   `json:"lng"`
	SpeedKmh   float64    `json:"speed_kmh"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	DriverID   *int64     `json:"driver_id"`
	// Compass bearing the unit is facing, in degrees (0 = north, 90 = east).
	// Null until the unit reports one; the map then falls back to the direction
	// between the last two positions.
	Heading *float64 `json:"heading"`
	// Colour of the delivery line of this unit. Every vehicle can have its
	// own, so two units are told apart on the map.
	RouteColor string `json:"route_color"`
	// The delivery route the unit is following right now, if any. It travels
	// with the fleet list so the map can draw the line and the fleet card can
	// say where the unit is heading without a second request.
	ActiveRoute *Route `json:"active_route"`
	// IdleSince is when the running idle session began: the moment the unit
	// started standing still. Null while the unit is not idle.
	IdleSince *time.Time `json:"idle_since"`
	// IdleDurationS is how long the running idle session has lasted, in
	// seconds. The server counts it, so every client sees the same number.
	// Null while the unit is not idle.
	IdleDurationS *float64 `json:"idle_duration_s"`
	// CurrentZone is the work area the unit is standing in right now, with
	// the dwell time it has spent there. Null while the unit is outside every
	// zone.
	CurrentZone *VehicleZone `json:"current_zone"`
}

// VehicleZone is the work area a unit is standing in, and for how long.
//
// The dwell time is counted from the last crossing into the zone that was
// never left: the zone history holds an ENTER for every crossing and an EXIT
// for every crossing back, so the open ENTER is the moment the unit arrived.
type VehicleZone struct {
	ZoneID   int64     `json:"zone_id"`
	ZoneName string    `json:"zone_name"`
	Since    time.Time `json:"since"`
	DwellS   float64   `json:"dwell_s"`
}

// The status of one stop of a route.
//
// The stops of a route are a workflow: the server marks the stop as arrived
// when the unit is close enough to it, and the driver finishes it (or passes
// it by).
const (
	// StopPending means the unit has not reached the stop yet.
	StopPending = "pending"
	// StopArrived means the server saw the unit at the stop. The stop is
	// not done yet: the driver still has to confirm it.
	StopArrived = "arrived"
	// StopCompleted means the driver confirmed the stop, with a note as the
	// proof of the visit.
	StopCompleted = "completed"
	// StopSkipped means the stop was passed by without being served.
	StopSkipped = "skipped"
)

// RoutePlace is one place of a route: the origin, the destination, or a stop
// that is being planned. It carries no status — a place only says where it
// is.
type RoutePlace struct {
	Lat  float64 `json:"lat"`
	Lng  float64 `json:"lng"`
	Name string  `json:"name"`
}

// RouteStop is one step of a delivery route: the place and its own status.
//
// The status travels with the stop, so the client never has to guess which
// stops are already served. ArrivedAt is the moment the server detected the
// unit at the stop, CompletedAt the moment somebody finished it, and
// CompletionNote what was written as the proof of the visit.
type RouteStop struct {
	// The place of the stop.
	Lat  float64 `json:"lat"`
	Lng  float64 `json:"lng"`
	Name string  `json:"name"`
	// ID identifies the stop, Seq its place in the visiting order (the first
	// stop is 0).
	ID  int64 `json:"id"`
	Seq int   `json:"seq"`
	// Status is one of pending, arrived, completed and skipped.
	Status string `json:"status"`
	// ArrivedAt is when the server detected the unit at the stop.
	ArrivedAt *time.Time `json:"arrived_at"`
	// CompletedAt is when the stop was finished: confirmed by the driver,
	// skipped by the driver, or closed with the route itself.
	CompletedAt *time.Time `json:"completed_at"`
	// CompletionNote is what the driver wrote as the proof of the stop.
	CompletionNote string `json:"completion_note"`
}

// Route is a planned delivery path: the line the map draws and the unit
// drives. The geometry is PostGIS' GeoJSON (coordinates are lng, lat) and is
// passed through untouched.
type Route struct {
	ID        int64      `json:"id"`
	VehicleID int64      `json:"vehicle_id"`
	Origin    RoutePlace `json:"origin"`
	// Destination is the last stop of the route: the place the unit ends up.
	Destination RoutePlace `json:"destination"`
	// Stops are the steps of the route, in the visiting order the server
	// worked out. The first one is served first and the last one is the
	// destination above. Every route has at least one stop: a route with a
	// single destination has that destination as its only stop.
	Stops []RouteStop `json:"stops"`
	// StopsTotal is how many stops the route has, StopsDone how many of
	// them are finished (confirmed or passed by).
	StopsTotal int             `json:"stops_total"`
	StopsDone  int             `json:"stops_done"`
	Geometry   json.RawMessage `json:"geometry"`
	DistanceM  float64         `json:"distance_m"`
	DurationS  float64         `json:"duration_s"`
	Source     string          `json:"source"`
	Status     string          `json:"status"`
	Progress   float64         `json:"progress"`
	RemainingM float64         `json:"remaining_m"`
	CreatedAt  time.Time       `json:"created_at"`
	EndedAt    *time.Time      `json:"ended_at"`
	// StartedAt is when the driver started the trip. A route without it is
	// a plan waiting for the driver.
	StartedAt *time.Time `json:"started_at"`
	// DriverAssigned tells whether the unit has a driver. The route of a
	// unit without a driver is driven right away — there is nobody to ask
	// — while the route of a unit with a driver waits for the start.
	DriverAssigned bool `json:"driver_assigned"`
}

// CreateInput is the body of POST /api/vehicles.
type CreateInput struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	DriverID *int64 `json:"driver_id"`
	// Colour of the delivery line. There is one colour for the whole
	// workspace, so the server decides it: the field is accepted for the sake
	// of older clients and then overwritten.
	RouteColor string `json:"route_color"`
}

// DefaultRouteColor is the colour of every delivery line: the primary red of
// the theme.
const DefaultRouteColor = "#d0171e"

// Normalize trims the text fields and applies the default kind and the route
// colour.
func (in *CreateInput) Normalize() {
	in.Code = strings.TrimSpace(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	if in.Kind == "" {
		in.Kind = KindTruck
	}
	// One colour for every unit: whatever the client sent is dropped.
	in.RouteColor = DefaultRouteColor
}

// Validate reports every rejected field at once.
func (in *CreateInput) Validate() FieldErrors {
	errs := FieldErrors{}
	if in.Code == "" {
		errs["code"] = "code is required"
	} else if len(in.Code) > 32 {
		errs["code"] = "code must be at most 32 characters"
	}
	if in.Name == "" {
		errs["name"] = "name is required"
	} else if len(in.Name) > 120 {
		errs["name"] = "name must be at most 120 characters"
	}
	if !kinds[in.Kind] {
		errs["kind"] = "kind must be one of truck, van, motorcycle, pickup"
	}
	return errs
}

// UpdateInput is the body of PUT /api/vehicles/:id: the same fields as create.
type UpdateInput = CreateInput

// PositionInput is the body of POST /api/vehicles/:id/position.
type PositionInput struct {
	Lat      *float64 `json:"lat"`
	Lng      *float64 `json:"lng"`
	SpeedKmh *float64 `json:"speed_kmh"`
	Status   string   `json:"status"`
	// Compass bearing the unit is facing, in degrees. Optional: a feed that
	// does not know the direction simply leaves it out.
	Heading *float64 `json:"heading"`
}

// Normalize trims the status and applies the default status.
func (in *PositionInput) Normalize() {
	in.Status = strings.ToLower(strings.TrimSpace(in.Status))
	if in.Status == "" {
		in.Status = StatusMoving
	}
}

// Validate reports every rejected field at once.
func (in *PositionInput) Validate() FieldErrors {
	errs := FieldErrors{}
	if in.Lat == nil {
		errs["lat"] = "lat is required"
	} else if math.IsNaN(*in.Lat) || *in.Lat < -90 || *in.Lat > 90 {
		errs["lat"] = "lat must be between -90 and 90"
	}
	if in.Lng == nil {
		errs["lng"] = "lng is required"
	} else if math.IsNaN(*in.Lng) || *in.Lng < -180 || *in.Lng > 180 {
		errs["lng"] = "lng must be between -180 and 180"
	}
	if in.SpeedKmh != nil && (math.IsNaN(*in.SpeedKmh) || *in.SpeedKmh < 0 || *in.SpeedKmh > 300) {
		errs["speed_kmh"] = "speed_kmh must be between 0 and 300"
	}
	if in.Heading != nil && (math.IsNaN(*in.Heading) || *in.Heading < 0 || *in.Heading >= 360) {
		errs["heading"] = "heading must be between 0 and 360 degrees"
	}
	if !statuses[in.Status] {
		errs["status"] = "status must be one of idle, moving, offline"
	}
	return errs
}

// ShouldMoveAnchor decides whether the anchor of a unit moves to the position
// of a fresh report.
//
// The anchor is the point where a unit started standing still. While the
// reports keep coming in around that point, the unit stands still and the
// clock keeps running; the moment it leaves the spot, the anchor moves to
// the new position and the clock starts again.
//
// The rule has three parts:
//
//   - A unit without an anchor has nothing to compare with, so its first
//     report becomes the anchor.
//   - A report that is faster than the speed limit always counts as movement,
//     however little the position moved: a unit that reports 40 km/h is
//     driving, whatever its GPS says.
//   - A report that stays inside the radius of the anchor keeps the anchor
//     where it is. The radius absorbs the drift of a real GPS.
//
// The distance is measured by the database (ST_Distance on geography), so the
// function only carries the rule itself: it is a pure function and can be
// tested without a database.
func ShouldMoveAnchor(hasAnchor bool, distanceM, speedKmh, radiusM, maxSpeedKmh float64) bool {
	if !hasAnchor {
		return true
	}
	if speedKmh > maxSpeedKmh {
		return true
	}
	return distanceM > radiusM
}
