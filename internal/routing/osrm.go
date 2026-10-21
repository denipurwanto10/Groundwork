// Package routing plans a driving path between two points.
//
// It talks to an OSRM-compatible engine over plain HTTP (no SDK, no new
// dependency) and falls back to a straight line when the engine is unreachable,
// disabled or has no route. The fallback is reported through Route.Source so the
// caller — and the UI — can tell a real road path from a placeholder.
package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Point is a WGS84 coordinate.
type Point struct {
	Lat float64
	Lng float64
}

// Geometry sources reported to the caller.
const (
	SourceOSRM   = "osrm"
	SourceDirect = "direct"
)

// directSpeedMps is the speed assumed for the straight-line fallback, so a
// route still carries a plausible ETA when no engine answered (~50 km/h).
const directSpeedMps = 50_000.0 / 3600.0

// Client is an OSRM route-service client.
type Client struct {
	baseURL  string
	disabled bool
	http     *http.Client
}

// NewClient reads OSRM_BASE_URL once. An empty value uses the public demo
// server; "off"/"none"/"direct" disables the engine and always plans a straight
// line, which keeps the feature working on a machine without internet.
func NewClient() *Client {
	raw := strings.TrimRight(strings.TrimSpace(os.Getenv("OSRM_BASE_URL")), "/")
	disabled := false
	switch strings.ToLower(raw) {
	case "off", "none", "direct":
		disabled = true
		raw = ""
	case "":
		raw = "https://router.project-osrm.org"
	}

	return &Client{
		baseURL:  raw,
		disabled: disabled,
		http:     &http.Client{Timeout: 6 * time.Second},
	}
}

// Plan returns the driving path from -> to. The returned error is informational:
// when it is non-nil the route is the straight-line fallback and Source says so.
func (c *Client) Plan(ctx context.Context, from, to Point) (Route, error) {
	if c.disabled || c.baseURL == "" {
		return DirectRoute(from, to), nil
	}

	route, err := c.fetch(ctx, from, to)
	if err != nil {
		return DirectRoute(from, to), err
	}

	return route, nil
}

// Route is the planned path plus its measurements.
type Route struct {
	Points    []Point
	DistanceM float64
	DurationS float64
	Source    string
}

func (c *Client) fetch(ctx context.Context, from, to Point) (Route, error) {
	coords := fmt.Sprintf("%s,%s;%s,%s",
		coord(from.Lng), coord(from.Lat), coord(to.Lng), coord(to.Lat))
	endpoint := fmt.Sprintf("%s/route/v1/driving/%s?overview=full&geometries=geojson", c.baseURL, coords)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Route{}, fmt.Errorf("build osrm request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Route{}, fmt.Errorf("call osrm: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Route{}, fmt.Errorf("read osrm response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Route{}, fmt.Errorf("osrm returned %s", resp.Status)
	}

	var out struct {
		Code   string `json:"code"`
		Routes []struct {
			Distance float64 `json:"distance"`
			Duration float64 `json:"duration"`
			Geometry struct {
				Coordinates [][2]float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Route{}, fmt.Errorf("decode osrm response: %w", err)
	}
	if out.Code != "Ok" || len(out.Routes) == 0 {
		return Route{}, fmt.Errorf("osrm found no route (code %q)", out.Code)
	}

	coordsIn := out.Routes[0].Geometry.Coordinates
	points := make([]Point, 0, len(coordsIn))
	for _, pair := range coordsIn {
		points = append(points, Point{Lat: pair[1], Lng: pair[0]})
	}

	// The engine snaps a coordinate to the closest road it knows. Two stops
	// that are a few metres apart, or that lie outside the road network, are
	// therefore snapped to the very same node: the answer is a line that goes
	// nowhere (distance 0) and PostGIS refuses to store it. Such a line is
	// not a route, so it is rejected here and the caller falls back to the
	// straight connection between the two stops.
	points = Dedupe(points)
	if len(points) < 2 {
		return Route{}, fmt.Errorf("osrm returned a geometry with %d distinct point(s)", len(points))
	}

	return Route{
		Points:    points,
		DistanceM: out.Routes[0].Distance,
		DurationS: out.Routes[0].Duration,
		Source:    SourceOSRM,
	}, nil
}

// Dedupe drops the points that repeat their predecessor. A routing engine can
// return the same coordinate twice in a row, and a line with a repeated point
// is invalid in PostGIS ("too few points in geometry component"), which the
// CHECK constraint on vehicle_routes turns into a failed insert.
func Dedupe(points []Point) []Point {
	out := make([]Point, 0, len(points))
	for _, p := range points {
		if len(out) > 0 && SameSpot(out[len(out)-1], p) {
			continue
		}
		out = append(out, p)
	}

	return out
}

// SameSpot reports whether two coordinates point at the same place. The
// tolerance is a centimetre, so two stops on the same road corner count as
// one point while a real step of a few metres does not.
func SameSpot(a, b Point) bool {
	return math.Abs(a.Lat-b.Lat) < 1e-7 && math.Abs(a.Lng-b.Lng) < 1e-7
}

// coord formats a coordinate the way the OSRM URL expects, without exponent
// notation for small values.
func coord(v float64) string {
	return strconv.FormatFloat(v, 'f', 6, 64)
}

// DirectRoute is the straight-line fallback: a two-point line with a
// haversine distance and an ETA at a constant speed. It is exported so the
// caller can rebuild the same geometry when the engine returns a line that
// cannot be stored (two stops snapped to the same road node).
func DirectRoute(from, to Point) Route {
	return directRoute(from, to)
}

// directRoute is the offline fallback: a two-point line with a haversine
// distance and an ETA at a constant speed.
func directRoute(from, to Point) Route {
	distance := DistanceMeters(from, to)
	return Route{
		Points:    []Point{from, to},
		DistanceM: distance,
		DurationS: distance / directSpeedMps,
		Source:    SourceDirect,
	}
}

// DistanceMeters is the great-circle distance between two points.
func DistanceMeters(a, b Point) float64 {
	const earthRadius = 6_371_000.0

	lat1 := a.Lat * math.Pi / 180
	lat2 := b.Lat * math.Pi / 180
	dLat := lat2 - lat1
	dLng := (b.Lng - a.Lng) * math.Pi / 180

	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLng/2)*math.Sin(dLng/2)

	return 2 * earthRadius * math.Asin(math.Min(1, math.Sqrt(h)))
}
