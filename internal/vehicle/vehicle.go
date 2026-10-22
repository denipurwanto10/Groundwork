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
}

// RouteStop is one end of a delivery route.
type RouteStop struct {
	Lat  float64 `json:"lat"`
	Lng  float64 `json:"lng"`
	Name string  `json:"name"`
}

// Route is a planned delivery path: the line the map draws and the unit
// drives. The geometry is PostGIS' GeoJSON (coordinates are lng, lat) and is
// passed through untouched.
type Route struct {
	ID        int64     `json:"id"`
	VehicleID int64     `json:"vehicle_id"`
	Origin    RouteStop `json:"origin"`
	// Destination is the last stop of the route: the place the unit ends up.
	Destination RouteStop `json:"destination"`
	// Stops are the destinations of a multi-stop route, in the visiting order
	// the server worked out. The first one is served first and the last one
	// is the destination above. It is empty for a route with a single
	// destination.
	Stops      []RouteStop     `json:"stops"`
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
