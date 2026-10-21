package main

import (
	"math"
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
