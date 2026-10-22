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
	"errors"
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

// PlanMulti plans the path from `from` through every stop and works out the
// order the stops are visited in.
//
// Three engines are tried, from the best to the most forgiving:
//
//  1. The trip service, which solves the visiting order and returns the whole
//     path in one call. The public demo server supports it.
//  2. The route service with the stops sorted by OptimizeOrder, for servers
//     without a trip service.
//  3. Straight lines between the stops, so the feature still works without
//     any engine at all.
//
// The returned error is informational: when it is non-nil the route is the
// fallback and Source says which one it is. The error of the last engine
// tried is reported, so the log says why the good engines were skipped.
func (c *Client) PlanMulti(ctx context.Context, from Point, stops []Point) (Route, error) {
	switch len(stops) {
	case 0:
		return Route{}, errors.New("plan multi: no stops")
	case 1:
		route, err := c.Plan(ctx, from, stops[0])
		if err == nil {
			route.Order = []int{0}
		}
		return route, err
	}

	if c.disabled || c.baseURL == "" {
		return DirectMultiRoute(from, stops), nil
	}

	route, tripErr := c.fetchTrip(ctx, from, stops)
	if tripErr == nil {
		return route, nil
	}

	// No trip service here: sort the stops ourselves and let the route
	// service draw the line through them.
	order := OptimizeOrder(from, stops)
	route, routeErr := c.fetchThrough(ctx, orderedPoints(from, stops, order))
	if routeErr == nil {
		route.Order = order
		return route, nil
	}

	return DirectMultiRoute(from, stops), fmt.Errorf(
		"osrm trip failed (%v) and osrm route failed (%v)", tripErr, routeErr)
}

// OptimizeOrder returns the order the stops are best visited in, using the
// greedy nearest-neighbour rule: from the current position, go to the closest
// stop that is left, then repeat from there.
//
// The result is not guaranteed to be the shortest possible tour — finding that
// is an NP-hard problem — but it is a good one, and it costs O(n²) instead of
// the factorial cost of an exact solution. The trip service solves it exactly;
// this is the fallback for servers without a trip service.
//
// The returned slice holds the indices of the stops, in visiting order.
func OptimizeOrder(from Point, stops []Point) []int {
	remaining := make([]int, len(stops))
	for i := range remaining {
		remaining[i] = i
	}

	order := make([]int, 0, len(stops))
	current := from
	for len(remaining) > 0 {
		best := 0
		bestDistance := math.MaxFloat64
		for k, idx := range remaining {
			d := DistanceMeters(current, stops[idx])
			if d < bestDistance {
				best = k
				bestDistance = d
			}
		}

		idx := remaining[best]
		order = append(order, idx)
		current = stops[idx]
		remaining = append(remaining[:best], remaining[best+1:]...)
	}

	return order
}

// orderedPoints puts the origin first and the stops after it, in the visiting
// order.
func orderedPoints(from Point, stops []Point, order []int) []Point {
	points := make([]Point, 0, len(order)+1)
	points = append(points, from)
	for _, idx := range order {
		if idx >= 0 && idx < len(stops) {
			points = append(points, stops[idx])
		}
	}

	return points
}

// DirectMultiRoute connects the stops with straight lines, in the order
// OptimizeOrder works out. It is the offline fallback: no engine, no road
// path, but the unit still drives the stops one after the other.
//
// It is exported so the caller can rebuild the same geometry when the engine
// returns a line that cannot be stored.
func DirectMultiRoute(from Point, stops []Point) Route {
	order := OptimizeOrder(from, stops)
	points := orderedPoints(from, stops, order)

	distance := 0.0
	for i := 1; i < len(points); i++ {
		distance += DistanceMeters(points[i-1], points[i])
	}

	return Route{
		Points:    points,
		DistanceM: distance,
		DurationS: distance / directSpeedMps,
		Source:    SourceDirect,
		Order:     order,
	}
}

// Route is the planned path plus its measurements.
type Route struct {
	Points    []Point
	DistanceM float64
	DurationS float64
	Source    string
	// Order is the visiting order of the stops of a multi-stop route:
	// order[i] is the index in the list the caller handed over of the stop
	// that is served i-th. It always matches the order of Points: the point
	// after the origin is the stop order[0].
	//
	// A route with a single stop carries the order [0].
	Order []int
}

// fetch plans the path through the given points, in the order they are given.
func (c *Client) fetch(ctx context.Context, from, to Point) (Route, error) {
	return c.fetchThrough(ctx, []Point{from, to})
}

// fetchThrough plans the path through every point, in the order they are
// given. The route service keeps that order: it draws the line from the first
// point to the last one, visiting the ones in between on the way.
func (c *Client) fetchThrough(ctx context.Context, waypoints []Point) (Route, error) {
	parts := make([]string, 0, len(waypoints))
	for _, p := range waypoints {
		parts = append(parts, fmt.Sprintf("%s,%s", coord(p.Lng), coord(p.Lat)))
	}
	endpoint := fmt.Sprintf("%s/route/v1/driving/%s?overview=full&geometries=geojson",
		c.baseURL, strings.Join(parts, ";"))

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

// fetchTrip asks the trip service for the best tour through the stops.
//
// The trip service differs from the route service in two ways: it decides the
// visiting order itself, and it reports that order. Every waypoint of the
// answer carries the position it takes in the tour ("waypoint_index"), while
// the waypoints themselves stay in the order they were asked for. The origin
// is the first waypoint and is pinned to the start of the tour.
//
// The answer is one polyline through every stop, in visiting order.
func (c *Client) fetchTrip(ctx context.Context, from Point, stops []Point) (Route, error) {
	parts := make([]string, 0, len(stops)+1)
	parts = append(parts, fmt.Sprintf("%s,%s", coord(from.Lng), coord(from.Lat)))
	for _, p := range stops {
		parts = append(parts, fmt.Sprintf("%s,%s", coord(p.Lng), coord(p.Lat)))
	}

	endpoint := fmt.Sprintf(
		"%s/trip/v1/driving/%s?overview=full&geometries=geojson&source=first&roundtrip=false",
		c.baseURL, strings.Join(parts, ";"))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Route{}, fmt.Errorf("build osrm trip request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Route{}, fmt.Errorf("call osrm trip: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Route{}, fmt.Errorf("read osrm trip response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Route{}, fmt.Errorf("osrm trip returned %s", resp.Status)
	}

	var out struct {
		Code  string `json:"code"`
		Trips []struct {
			Distance float64 `json:"distance"`
			Duration float64 `json:"duration"`
			Geometry struct {
				Coordinates [][2]float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"trips"`
		Waypoints []struct {
			WaypointIndex int `json:"waypoint_index"`
		} `json:"waypoints"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Route{}, fmt.Errorf("decode osrm trip response: %w", err)
	}
	if out.Code != "Ok" || len(out.Trips) == 0 {
		return Route{}, fmt.Errorf("osrm trip found no solution (code %q)", out.Code)
	}

	order, err := tripOrder(out.Waypoints, len(stops))
	if err != nil {
		return Route{}, err
	}

	coordsIn := out.Trips[0].Geometry.Coordinates
	points := make([]Point, 0, len(coordsIn))
	for _, pair := range coordsIn {
		points = append(points, Point{Lat: pair[1], Lng: pair[0]})
	}
	points = Dedupe(points)
	if len(points) < 2 {
		return Route{}, fmt.Errorf("osrm trip returned a geometry with %d distinct point(s)", len(points))
	}

	return Route{
		Points:    points,
		DistanceM: out.Trips[0].Distance,
		DurationS: out.Trips[0].Duration,
		Source:    SourceOSRM,
		Order:     order,
	}, nil
}

// tripOrder reads the visiting order out of the waypoints of a trip answer.
//
// The waypoints come back in the order they were asked for, and every one of
// them says where it lands in the tour. The first waypoint is the origin, so
// the stops are the ones after it.
//
// The result is checked to be a permutation of the stops: a tour that visits
// a stop twice, or skips one, is not a tour and is refused instead of stored.
func tripOrder(waypoints []struct {
	WaypointIndex int `json:"waypoint_index"`
}, stopCount int) ([]int, error) {
	if len(waypoints) != stopCount+1 {
		return nil, fmt.Errorf("osrm trip returned %d waypoint(s) for %d stop(s)",
			len(waypoints), stopCount)
	}

	// Tour position -> index in the stop list.
	byPosition := make(map[int]int, stopCount)
	for i, wp := range waypoints {
		if i == 0 {
			continue // the origin
		}
		byPosition[wp.WaypointIndex] = i - 1
	}

	order := make([]int, 0, stopCount)
	for position := 1; position <= stopCount; position++ {
		idx, ok := byPosition[position]
		if !ok {
			return nil, fmt.Errorf("osrm trip is missing the waypoint at tour position %d", position)
		}
		order = append(order, idx)
	}

	seen := make(map[int]bool, stopCount)
	for _, idx := range order {
		if idx < 0 || idx >= stopCount {
			return nil, fmt.Errorf("osrm trip returned the unknown stop %d", idx)
		}
		if seen[idx] {
			return nil, fmt.Errorf("osrm trip visits the stop %d twice", idx)
		}
		seen[idx] = true
	}

	return order, nil
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
