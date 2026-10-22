package main

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

// testPlan is a straight line of five points, about 111 metres apart.
func testPlan() *pathPlan {
	pts := [][2]float64{
		{-6.0, 106.0},
		{-6.001, 106.0},
		{-6.002, 106.0},
		{-6.003, 106.0},
		{-6.004, 106.0},
	}
	plan := &pathPlan{id: 7, pts: pts, cum: make([]float64, 0, len(pts))}
	plan.cum = append(plan.cum, 0)
	for i := 1; i < len(pts); i++ {
		plan.total += haversineMeters(pts[i-1][0], pts[i-1][1], pts[i][0], pts[i][1])
		plan.cum = append(plan.cum, plan.total)
	}
	return plan
}

func TestFollowStartsAtTheOriginOfTheRoute(t *testing.T) {
	plan := testPlan()
	u := &unit{id: 1, code: "SIM-01"}

	u.follow(plan, 2*time.Second, 20)

	if u.lat != plan.pts[0][0] || u.lng != plan.pts[0][1] {
		t.Fatalf("unit at %.6f,%.6f, want the first point of the route %.6f,%.6f",
			u.lat, u.lng, plan.pts[0][0], plan.pts[0][1])
	}
	if u.routePos != 0 {
		t.Fatalf("route position = %f, want 0", u.routePos)
	}
}

func TestFollowWalksForwardOnTheLine(t *testing.T) {
	plan := testPlan()
	u := &unit{id: 1, code: "SIM-01"}
	interval := 2 * time.Second
	pace := 20.0

	u.follow(plan, interval, pace)

	// Every tick moves the unit further along the line, never backwards, and
	// every position it reports lies on the route.
	previous := u.routePos
	for i := 0; i < 10; i++ {
		speed := u.follow(plan, interval, pace)
		if u.routePos <= previous {
			t.Fatalf("tick %d: route position = %f, want more than %f", i, u.routePos, previous)
		}
		previous = u.routePos

		if !onLine(plan, u.lat, u.lng) {
			t.Fatalf("tick %d: unit at %.9f,%.9f, which is not on the route", i, u.lat, u.lng)
		}
		if u.routePos < plan.total && speed <= 0 {
			t.Fatalf("tick %d: speed = %f, want the distance it covered", i, speed)
		}
	}
}

func TestFollowEndsAtTheDestination(t *testing.T) {
	plan := testPlan()
	u := &unit{id: 1, code: "SIM-01"}
	interval := 2 * time.Second

	// A pace of a kilometre per tick reaches the end at once.
	u.follow(plan, interval, 20)
	u.follow(plan, interval, 5000)

	if !u.arrived {
		t.Fatal("the unit should have arrived at the end of the line")
	}
	last := plan.pts[len(plan.pts)-1]
	if u.lat != last[0] || u.lng != last[1] {
		t.Fatalf("unit at %.6f,%.6f, want the last point %.6f,%.6f", u.lat, u.lng, last[0], last[1])
	}
}

func TestFollowStartsOverOnANewRoute(t *testing.T) {
	plan := testPlan()
	other := &pathPlan{id: 9, pts: [][2]float64{{-7.0, 107.0}, {-7.001, 107.0}}, cum: []float64{0, 111}}
	other.total = 111

	u := &unit{id: 1, code: "SIM-01"}
	u.follow(plan, time.Second, 20)
	u.follow(plan, time.Second, 20)

	// A different plan: the unit goes back to the beginning of that line.
	u.follow(other, time.Second, 20)
	if u.routeID != other.id {
		t.Fatalf("route id = %d, want %d", u.routeID, other.id)
	}
	if u.lat != other.pts[0][0] {
		t.Fatalf("unit at %.6f, want the first point of the new route %.6f", u.lat, other.pts[0][0])
	}
}

func TestFollowPassesThePointsInOrder(t *testing.T) {
	// The unit has to pass every point of the route, in order: that is what
	// "walking the array" means.
	plan := testPlan()
	u := &unit{id: 1, code: "SIM-01"}
	interval := 2 * time.Second
	pace := 20.0

	passed := 0
	next := 1
	for i := 0; i < 40 && next < len(plan.pts); i++ {
		u.follow(plan, interval, pace)
		if u.routePos >= plan.cum[next] {
			passed++
			next++
		}
	}
	if passed != len(plan.pts)-1 {
		t.Fatalf("passed %d points, want %d", passed, len(plan.pts)-1)
	}
}

// TestFollowWalksAMultiStopRouteToTheLastStop checks the route of a
// multi-stop delivery: one line through every stop, and the unit has to walk
// all of it, ending on the last stop.
func TestFollowWalksAMultiStopRouteToTheLastStop(t *testing.T) {
	// Three stops after the origin, each a few hundred metres further on.
	pts := [][2]float64{
		{-6.0, 106.0},   // the origin
		{-6.005, 106.0}, // stop 1
		{-6.010, 106.0}, // stop 2
		{-6.015, 106.0}, // stop 3, the last one
	}
	plan := &pathPlan{id: 9, pts: pts, cum: make([]float64, 0, len(pts))}
	plan.cum = append(plan.cum, 0)
	for i := 1; i < len(pts); i++ {
		plan.total += haversineMeters(pts[i-1][0], pts[i-1][1], pts[i][0], pts[i][1])
		plan.cum = append(plan.cum, plan.total)
	}

	u := &unit{id: 1, code: "SIM-01"}
	interval := 2 * time.Second
	pace := 20.0

	for i := 0; i < 200 && !u.arrived; i++ {
		u.follow(plan, interval, pace)
	}

	if !u.arrived {
		t.Fatal("the unit never reached the end of the route")
	}
	last := plan.pts[len(plan.pts)-1]
	if u.lat != last[0] || u.lng != last[1] {
		t.Fatalf("the unit parked at %.6f,%.6f, want the last stop %.6f,%.6f",
			u.lat, u.lng, last[0], last[1])
	}
}

func TestClampSpeedKeepsTheSpeedInsideTheAcceptedRange(t *testing.T) {
	// The API refuses a speed above 300 km/h, and a brisk pace reports one.
	if got := clampSpeed(540); got != 300 {
		t.Errorf("clampSpeed(540) = %v, want 300", got)
	}
	if got := clampSpeed(-5); got != 0 {
		t.Errorf("clampSpeed(-5) = %v, want 0", got)
	}
	if got := clampSpeed(42); got != 42 {
		t.Errorf("clampSpeed(42) = %v, want 42", got)
	}
}

// onLine reports whether the position lies on the route: the distance to the
// closest point of the line has to be negligible.
func onLine(plan *pathPlan, lat, lng float64) bool {
	const tolerance = 1e-7 // about a centimetre in degrees
	for i := 0; i+1 < len(plan.pts); i++ {
		a := plan.pts[i]
		b := plan.pts[i+1]
		// Project the position on the segment and measure what is left.
		dx, dy := b[0]-a[0], b[1]-a[1]
		length := dx*dx + dy*dy
		if length == 0 {
			continue
		}
		t := ((lat-a[0])*dx + (lng-a[1])*dy) / length
		if t < 0 {
			t = 0
		}
		if t > 1 {
			t = 1
		}
		nearestLat := a[0] + t*dx
		nearestLng := a[1] + t*dy
		if math.Abs(lat-nearestLat) < tolerance && math.Abs(lng-nearestLng) < tolerance {
			return true
		}
	}
	return false
}

// planOf builds a plan out of a few points, the way newPathPlan does for a
// route that comes from the server.
func planOf(pts [][2]float64) *pathPlan {
	plan := &pathPlan{id: 1, pts: pts, cum: make([]float64, 0, len(pts))}
	plan.cum = append(plan.cum, 0)
	for i := 1; i < len(pts); i++ {
		plan.total += haversineMeters(pts[i-1][0], pts[i-1][1], pts[i][0], pts[i][1])
		plan.cum = append(plan.cum, plan.total)
	}
	return plan
}

// TestHeadingIsACompassBearing pins the direction the unit reports to the
// convention the API and the map use: 0 = north, 90 = east. A bearing that
// counts from the east instead turns every marker a quarter of a circle
// away from the road it drives.
func TestHeadingIsACompassBearing(t *testing.T) {
	cases := []struct {
		name string
		pts  [][2]float64
		want float64
	}{
		{"north", [][2]float64{{-6.0, 107.0}, {-5.999, 107.0}}, 0},
		{"east", [][2]float64{{-6.0, 107.0}, {-6.0, 107.001}}, 90},
		{"south", [][2]float64{{-6.0, 107.0}, {-6.001, 107.0}}, 180},
		{"west", [][2]float64{{-6.0, 107.0}, {-6.0, 106.999}}, 270},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := planOf(tc.pts)
			got := toDegrees(heading(plan, 0))
			diff := math.Abs(got - tc.want)
			if diff > 180 {
				diff = 360 - diff
			}
			if diff > 0.5 {
				t.Fatalf("heading = %.1f degrees, want %.1f", got, tc.want)
			}
		})
	}
}

// TestReportedHeadingMatchesTheMovement is the end-to-end version of the
// same rule: the direction the unit reports has to be the direction it
// actually moved in, or the marker points away from the road.
func TestReportedHeadingMatchesTheMovement(t *testing.T) {
	plan := planOf([][2]float64{{-6.0, 107.0}, {-6.0, 107.002}})
	u := &unit{id: 1, code: "SIM-01"}

	u.follow(plan, 2*time.Second, 20)
	firstLat, firstLng := u.lat, u.lng
	firstHeading := toDegrees(u.dir)

	u.follow(plan, 2*time.Second, 20)
	secondLat, secondLng := u.lat, u.lng

	// The bearing from the first position to the second one.
	dLat := secondLat - firstLat
	dLng := (secondLng - firstLng) * math.Cos(firstLat*math.Pi/180)
	moved := toDegrees(math.Atan2(dLng, dLat))

	diff := math.Abs(moved - firstHeading)
	if diff > 180 {
		diff = 360 - diff
	}
	if diff > 1 {
		t.Fatalf("reported heading %.1f degrees, but the unit moved towards %.1f", firstHeading, moved)
	}
}

// TestStepMovesTowardsTheFacing checks the free-roam step: a unit that faces
// north moves north, one that faces east moves east. The step turns the unit
// a little at random first, so the movement is compared with the direction the
// unit faced rather than with an exact angle.
func TestStepMovesTowardsTheFacing(t *testing.T) {
	cases := []struct {
		name string
		dir  float64
	}{
		{"north", 0},
		{"east", math.Pi / 2},
		{"south", math.Pi},
		{"west", 3 * math.Pi / 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := &unit{id: 1, code: "SIM-01", lat: -6.0, lng: 107.0, dir: tc.dir}
			startLat, startLng := u.lat, u.lng
			u.step(rand.New(rand.NewSource(1)), 2*time.Second)

			dLat := u.lat - startLat
			dLng := (u.lng - startLng) * math.Cos(startLat*math.Pi/180)
			moved := toDegrees(math.Atan2(dLng, dLat))

			if diff := angleDiff(moved, toDegrees(tc.dir)); diff > 20 {
				t.Fatalf("facing %.0f degrees the unit moved towards %.0f degrees",
					toDegrees(tc.dir), moved)
			}
		})
	}
}

// angleDiff is the smallest distance between two compass bearings, in degrees.
func angleDiff(a, b float64) float64 {
	diff := math.Abs(a - b)
	if diff > 180 {
		diff = 360 - diff
	}
	return diff
}

// TestStepDrivesAtTheForcedSpeed checks that the unit reports the speed it
// drove and covers the ground that speed implies.
func TestStepDrivesAtTheForcedSpeed(t *testing.T) {
	u := &unit{id: 1, code: "SIM-01", lat: -6.0, lng: 107.0, dir: 0}
	interval := 2 * time.Second

	speed := u.step(rand.New(rand.NewSource(1)), interval)

	if speed <= 0 || speed > 80 {
		t.Fatalf("speed = %v, want a speed inside the normal range", speed)
	}

	// The speed it reports and the distance it covers are the same number:
	// the feed never lies about the trip.
	distance := haversineMeters(-6.0, 107.0, u.lat, u.lng)
	want := speed * interval.Hours() * 1000
	if math.Abs(distance-want) > 5 {
		t.Fatalf("the unit covered %.1f metres at %.1f km/h, want about %.1f", distance, speed, want)
	}
}
