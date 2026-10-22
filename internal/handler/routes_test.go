package handler

import (
	"testing"

	"entity-management/internal/vehicle"
)

// The stop a unit is serving is the first one that is not finished yet: the
// stops of a route are visited one after the other.
func TestCurrentStopIsTheFirstOneThatIsNotFinished(t *testing.T) {
	cases := []struct {
		name  string
		stops []vehicle.RouteStop
		want  int // the seq of the expected stop, -1 when there is none
	}{
		{
			name:  "nothing visited yet",
			stops: []vehicle.RouteStop{{Seq: 0, Status: vehicle.StopPending}, {Seq: 1, Status: vehicle.StopPending}},
			want:  0,
		},
		{
			name:  "the first stop is done",
			stops: []vehicle.RouteStop{{Seq: 0, Status: vehicle.StopCompleted}, {Seq: 1, Status: vehicle.StopPending}},
			want:  1,
		},
		{
			name:  "the first stop was skipped",
			stops: []vehicle.RouteStop{{Seq: 0, Status: vehicle.StopSkipped}, {Seq: 1, Status: vehicle.StopPending}},
			want:  1,
		},
		{
			name:  "an arrived stop is still the current one",
			stops: []vehicle.RouteStop{{Seq: 0, Status: vehicle.StopArrived}, {Seq: 1, Status: vehicle.StopPending}},
			want:  0,
		},
		{
			name:  "every stop is done",
			stops: []vehicle.RouteStop{{Seq: 0, Status: vehicle.StopCompleted}, {Seq: 1, Status: vehicle.StopSkipped}},
			want:  -1,
		},
		{
			name:  "no stops at all",
			stops: nil,
			want:  -1,
		},
		{
			name: "the smallest sequence wins, whatever the order of the rows",
			stops: []vehicle.RouteStop{
				{Seq: 2, Status: vehicle.StopPending},
				{Seq: 0, Status: vehicle.StopArrived},
				{Seq: 1, Status: vehicle.StopPending},
			},
			want: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := currentStop(tc.stops)
			if tc.want < 0 {
				if current != nil {
					t.Fatalf("the current stop is the seq %d, want none", current.Seq)
				}
				return
			}
			if current == nil {
				t.Fatal("there is no current stop, want the seq", tc.want)
			}
			if current.Seq != tc.want {
				t.Errorf("the current stop is the seq %d, want %d", current.Seq, tc.want)
			}
		})
	}
}

// The note of a stop is the proof of the visit: it may be empty, but it may
// not run into a novel.
func TestValidateStopNote(t *testing.T) {
	if fields := validateStopNote(""); len(fields) != 0 {
		t.Errorf("an empty note is rejected: %v", fields)
	}
	if fields := validateStopNote("Paket diterima satpam"); len(fields) != 0 {
		t.Errorf("a short note is rejected: %v", fields)
	}

	long := make([]byte, maxStopNote+1)
	for i := range long {
		long[i] = 'a'
	}
	fields := validateStopNote(string(long))
	if len(fields) == 0 {
		t.Fatalf("a note of %d characters is accepted", len(long))
	}
	if _, ok := fields["note"]; !ok {
		t.Errorf("the note is not named in the error: %v", fields)
	}

	// The longest note that still fits.
	if fields := validateStopNote(string(long[:maxStopNote])); len(fields) != 0 {
		t.Errorf("a note of %d characters is rejected: %v", maxStopNote, fields)
	}
}

// The order the routing engine worked out is the order the stops are served
// in. An order that does not cover every stop is ignored.
func TestOrderStops(t *testing.T) {
	stops := []routeStop{
		{Lat: 1, Lng: 1, Name: "A"},
		{Lat: 2, Lng: 2, Name: "B"},
		{Lat: 3, Lng: 3, Name: "C"},
	}

	ordered := orderStops(stops, []int{2, 0, 1})
	if len(ordered) != 3 {
		t.Fatalf("the order holds %d stops, want 3", len(ordered))
	}
	if ordered[0].Name != "C" || ordered[1].Name != "A" || ordered[2].Name != "B" {
		t.Errorf("the order is %v, want C, A, B", ordered)
	}

	// An order that does not line up with the stops is not worth storing:
	// the stops keep the order they were handed over in.
	ordered = orderStops(stops, []int{0, 1})
	if len(ordered) != 3 || ordered[0].Name != "A" {
		t.Errorf("a broken order changed the stops: %v", ordered)
	}
}
