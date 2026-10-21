package main

import (
	"math"
	"testing"
	"time"
)

// testPlan is a straight line of five points, 100 metres apart.
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

func TestFollowWalksTheRoutePointByPoint(t *testing.T) {
	plan := testPlan()
	u := &unit{id: 1, code: "SIM-01"}
	interval := 2 * time.Second

	// The first tick puts the unit on the first point of the line.
	u.follow(plan, interval, 1)
	if u.lat != plan.pts[0][0] || u.lng != plan.pts[0][1] {
		t.Fatalf("unit at %.4f,%.4f, want the first point %.4f,%.4f",
			u.lat, u.lng, plan.pts[0][0], plan.pts[0][1])
	}

	// Every following tick moves the unit to the next point of the line, and
	// never to a place between two points.
	for i := 1; i < len(plan.pts)-1; i++ {
		speed := u.follow(plan, interval, 1)
		if u.routeIdx != i {
			t.Fatalf("tick %d: index = %d, want %d", i, u.routeIdx, i)
		}
		if u.lat != plan.pts[i][0] || u.lng != plan.pts[i][1] {
			t.Fatalf("tick %d: unit at %.6f,%.6f, want the point %.6f,%.6f",
				i, u.lat, u.lng, plan.pts[i][0], plan.pts[i][1])
		}
		if speed <= 0 {
			t.Fatalf("tick %d: speed = %f, want the distance it covered", i, speed)
		}
	}

	// The last tick parks the unit at the destination and ends the delivery.
	u.follow(plan, interval, 1)
	if !u.arrived {
		t.Fatal("the unit should have arrived at the end of the line")
	}
	if u.lat != plan.pts[len(plan.pts)-1][0] {
		t.Fatalf("unit at %.6f, want the last point %.6f", u.lat, plan.pts[len(plan.pts)-1][0])
	}
}

func TestFollowCoversSeveralPointsPerTick(t *testing.T) {
	plan := testPlan()
	u := &unit{id: 1, code: "SIM-01"}
	interval := 2 * time.Second

	// The first tick starts the route at its origin.
	u.follow(plan, interval, 2)
	if u.routeIdx != 0 {
		t.Fatalf("index = %d, want 0 (the origin of the route)", u.routeIdx)
	}

	u.follow(plan, interval, 2)
	if u.routeIdx != 2 {
		t.Fatalf("index = %d, want 2 (two points per tick)", u.routeIdx)
	}
	if u.lat != plan.pts[2][0] {
		t.Fatalf("unit at %.6f, want the third point %.6f", u.lat, plan.pts[2][0])
	}
}

func TestFollowStartsOverOnANewRoute(t *testing.T) {
	plan := testPlan()
	other := &pathPlan{id: 9, pts: [][2]float64{{-7.0, 107.0}, {-7.001, 107.0}}, cum: []float64{0, 111}}
	other.total = 111

	u := &unit{id: 1, code: "SIM-01"}
	u.follow(plan, time.Second, 1)
	u.follow(plan, time.Second, 1)

	// A different plan: the unit goes back to the beginning of that line.
	u.follow(other, time.Second, 1)
	if u.routeID != other.id {
		t.Fatalf("route id = %d, want %d", u.routeID, other.id)
	}
	if u.lat != other.pts[0][0] {
		t.Fatalf("unit at %.6f, want the first point of the new route %.6f", u.lat, other.pts[0][0])
	}
}

func TestFollowStaysOnTheLine(t *testing.T) {
	// A line with a corner: every point the unit reports has to be a point of
	// the line, never somewhere beside it.
	plan := &pathPlan{
		id: 3,
		pts: [][2]float64{
			{-6.0, 106.0},
			{-6.0, 106.001},
			{-6.001, 106.001},
		},
		cum: []float64{0, 111, 222},
	}
	plan.total = 222

	u := &unit{id: 1, code: "SIM-01"}
	for i := 0; i < 4; i++ {
		u.follow(plan, 2*time.Second, 1)
		for _, p := range plan.pts {
			if math.Abs(u.lat-p[0]) < 1e-9 && math.Abs(u.lng-p[1]) < 1e-9 {
				goto onLine
			}
		}
		t.Fatalf("the unit left the line: %.9f,%.9f", u.lat, u.lng)
	}
onLine:
}
