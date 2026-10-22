package routing

import (
	"context"
	"math"
	"testing"
)

// jakarta, bandung, semarang, surabaya.
var (
	jakarta  = Point{Lat: -6.1754, Lng: 106.8272}
	bandung  = Point{Lat: -6.9175, Lng: 107.6191}
	semarang = Point{Lat: -6.9667, Lng: 110.4167}
	surabaya = Point{Lat: -7.2575, Lng: 112.7521}
)

func TestOptimizeOrderPicksTheClosestStopFirst(t *testing.T) {
	// From Jakarta the closest stop is Bandung, so it is served first.
	order := OptimizeOrder(jakarta, []Point{semarang, bandung})

	if len(order) != 2 {
		t.Fatalf("order length = %d, want 2", len(order))
	}
	if order[0] != 1 {
		t.Errorf("first stop = %d, want 1 (Bandung is closest to Jakarta)", order[0])
	}
	if order[1] != 0 {
		t.Errorf("second stop = %d, want 0", order[1])
	}
}

func TestOptimizeOrderVisitsEveryStopOnce(t *testing.T) {
	stops := []Point{jakarta, bandung, semarang, surabaya}
	order := OptimizeOrder(jakarta, stops)

	if len(order) != len(stops) {
		t.Fatalf("order length = %d, want %d", len(order), len(stops))
	}

	seen := make(map[int]bool, len(order))
	for _, idx := range order {
		if idx < 0 || idx >= len(stops) {
			t.Fatalf("order holds the unknown index %d", idx)
		}
		if seen[idx] {
			t.Fatalf("order visits the stop %d twice: %v", idx, order)
		}
		seen[idx] = true
	}
}

func TestOptimizeOrderIsNotLongerThanTheInputOrder(t *testing.T) {
	// The greedy tour of these four cities is shorter than the order they
	// were handed in, which is the point of sorting them at all.
	stops := []Point{semarang, jakarta, bandung, surabaya}

	tour := legLength(jakarta, stops, OptimizeOrder(jakarta, stops))
	handed := legLength(jakarta, stops, []int{0, 1, 2, 3})

	if tour >= handed {
		t.Errorf("greedy tour = %.0f m, input order = %.0f m: the sort made it longer", tour, handed)
	}
}

// legLength is the length of the tour that visits the stops in the order.
func legLength(from Point, stops []Point, order []int) float64 {
	total := 0.0
	current := from
	for _, idx := range order {
		total += DistanceMeters(current, stops[idx])
		current = stops[idx]
	}

	return total
}

func TestPlanMultiWithoutAnEngineUsesStraightLines(t *testing.T) {
	client := &Client{disabled: true}

	route, err := client.PlanMulti(context.Background(), jakarta, []Point{semarang, bandung})
	if err != nil {
		t.Fatalf("PlanMulti returned %v, want no error", err)
	}
	if route.Source != SourceDirect {
		t.Errorf("source = %q, want %q", route.Source, SourceDirect)
	}
	if len(route.Order) != 2 {
		t.Fatalf("order = %v, want 2 entries", route.Order)
	}
	if len(route.Points) != 3 {
		t.Fatalf("points = %d, want 3 (origin plus two stops)", len(route.Points))
	}
	if route.DistanceM <= 0 {
		t.Error("distance must be positive")
	}

	// The line must start at the origin and end at the last stop of the order.
	if !SameSpot(route.Points[0], jakarta) {
		t.Errorf("the line starts at %+v, want the origin %+v", route.Points[0], jakarta)
	}
	last := route.Points[len(route.Points)-1]
	want := stopsByOrder(t, []Point{semarang, bandung}, route.Order)[len(route.Order)-1]
	if !SameSpot(last, want) {
		t.Errorf("the line ends at %+v, want the last stop %+v", last, want)
	}
}

// stopsByOrder puts the stops in the visiting order.
func stopsByOrder(t *testing.T, stops []Point, order []int) []Point {
	t.Helper()
	out := make([]Point, 0, len(order))
	for _, idx := range order {
		out = append(out, stops[idx])
	}

	return out
}

func TestPlanMultiWithOneStopKeepsTheSingleStopOrder(t *testing.T) {
	client := &Client{disabled: true}

	route, err := client.PlanMulti(context.Background(), jakarta, []Point{bandung})
	if err != nil {
		t.Fatalf("PlanMulti returned %v, want no error", err)
	}
	if len(route.Order) != 1 || route.Order[0] != 0 {
		t.Errorf("order = %v, want [0]", route.Order)
	}
}

func TestPlanMultiWithoutStopsIsRefused(t *testing.T) {
	client := &Client{disabled: true}

	if _, err := client.PlanMulti(context.Background(), jakarta, nil); err == nil {
		t.Error("PlanMulti without stops must fail")
	}
}

func TestTripOrderReadsTheTourPosition(t *testing.T) {
	// The waypoints come back in the order they were asked for, and every one
	// of them says where it lands in the tour: the second stop is served
	// first, the first one last.
	waypoints := []struct {
		WaypointIndex int `json:"waypoint_index"`
	}{
		{WaypointIndex: 0}, // the origin
		{WaypointIndex: 3}, // stop 0 -> third
		{WaypointIndex: 1}, // stop 1 -> first
		{WaypointIndex: 2}, // stop 2 -> second
	}

	order, err := tripOrder(waypoints, 3)
	if err != nil {
		t.Fatalf("tripOrder returned %v", err)
	}

	want := []int{1, 2, 0}
	for i, idx := range want {
		if order[i] != idx {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestTripOrderRefusesATourThatSkipsAStop(t *testing.T) {
	waypoints := []struct {
		WaypointIndex int `json:"waypoint_index"`
	}{
		{WaypointIndex: 0},
		{WaypointIndex: 1},
		{WaypointIndex: 1}, // the same tour position twice
		{WaypointIndex: 2},
	}

	if _, err := tripOrder(waypoints, 3); err == nil {
		t.Error("a tour that misses a stop must be refused")
	}
}

func TestTripOrderRefusesTheWrongNumberOfWaypoints(t *testing.T) {
	waypoints := []struct {
		WaypointIndex int `json:"waypoint_index"`
	}{
		{WaypointIndex: 0},
		{WaypointIndex: 1},
	}

	if _, err := tripOrder(waypoints, 3); err == nil {
		t.Error("a tour with too few waypoints must be refused")
	}
}

func TestDirectMultiRouteMeasuresEveryLeg(t *testing.T) {
	stops := []Point{semarang, bandung}
	route := DirectMultiRoute(jakarta, stops)

	order := route.Order
	want := legLength(jakarta, stops, order)
	if math.Abs(route.DistanceM-want) > 1 {
		t.Errorf("distance = %.2f m, want %.2f m", route.DistanceM, want)
	}
	if route.DurationS <= 0 {
		t.Error("duration must be positive")
	}
}
