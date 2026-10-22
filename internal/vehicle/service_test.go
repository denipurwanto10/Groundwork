package vehicle

import (
	"testing"
	"time"
)

// The three states of the schedule, and the boundaries between them.
//
// The numbers are picked so that the arithmetic is exact: a unit that has
// driven 4.500 km of a 5.000 km interval has 500 km left, and that is
// exactly the warning distance — so it is due, not fine.
func TestComputeServiceStatus(t *testing.T) {
	cases := []struct {
		name       string
		totalM     float64
		interval   *float64
		lastOdoM   float64
		warnKM     float64
		wantStatus string
		wantRemain float64
	}{
		{
			name:       "far from the schedule",
			totalM:     1_000_000,
			interval:   float64Ptr(5000),
			lastOdoM:   0,
			warnKM:     500,
			wantStatus: ServiceStatusOK,
			wantRemain: 4000,
		},
		{
			name:       "exactly the warning distance is due soon",
			totalM:     4_500_000,
			interval:   float64Ptr(5000),
			lastOdoM:   0,
			warnKM:     500,
			wantStatus: ServiceStatusDueSoon,
			wantRemain: 500,
		},
		{
			name:       "one metre before the schedule is due soon",
			totalM:     4_999_999,
			interval:   float64Ptr(5000),
			lastOdoM:   0,
			warnKM:     500,
			wantStatus: ServiceStatusDueSoon,
			wantRemain: 0,
		},
		{
			name:       "exactly the schedule is overdue",
			totalM:     5_000_000,
			interval:   float64Ptr(5000),
			lastOdoM:   0,
			warnKM:     500,
			wantStatus: ServiceStatusOverdue,
			wantRemain: 0,
		},
		{
			name:       "past the schedule is overdue",
			totalM:     5_150_000,
			interval:   float64Ptr(5000),
			lastOdoM:   0,
			warnKM:     500,
			wantStatus: ServiceStatusOverdue,
			wantRemain: -150,
		},
		{
			name:       "a unit without an interval follows the default",
			totalM:     1_000_000,
			interval:   nil,
			lastOdoM:   0,
			warnKM:     500,
			wantStatus: ServiceStatusOK,
			wantRemain: 4000,
		},
		{
			name:       "the schedule counts from the last service",
			totalM:     12_000_000,
			interval:   float64Ptr(5000),
			lastOdoM:   10_000_000,
			warnKM:     500,
			wantStatus: ServiceStatusOK,
			wantRemain: 3000,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schedule := ComputeService(tc.totalM, tc.interval, nil, tc.lastOdoM, 5000, tc.warnKM)

			if schedule.Status != tc.wantStatus {
				t.Errorf("the status is %q, want %q", schedule.Status, tc.wantStatus)
			}
			if schedule.RemainingKM != tc.wantRemain {
				t.Errorf("what is left is %v km, want %v km", schedule.RemainingKM, tc.wantRemain)
			}
		})
	}
}

// A unit without an interval of its own follows the default of the server.
func TestComputeServiceUsesTheDefaultInterval(t *testing.T) {
	schedule := ComputeService(0, nil, nil, 0, 2000, 100)

	if schedule.IntervalKM != 2000 {
		t.Errorf("the interval is %v km, want 2000 km", schedule.IntervalKM)
	}
	if schedule.NextDueKM != 2000 {
		t.Errorf("the next service is due at %v km, want 2000 km", schedule.NextDueKM)
	}
	if schedule.Status != ServiceStatusOK {
		t.Errorf("the status is %q, want %q", schedule.Status, ServiceStatusOK)
	}
}

// The schedule of a unit that was serviced: the baseline is the odometer of
// that moment, and the odometer of the unit has grown since.
func TestComputeServiceCountsFromTheLastService(t *testing.T) {
	servicedAt := time.Now().Add(-24 * time.Hour)
	schedule := ComputeService(10_500_000, float64Ptr(5000), &servicedAt, 10_000_000, 5000, 500)

	if schedule.LastServiceAt == nil || !schedule.LastServiceAt.Equal(servicedAt) {
		t.Errorf("the last service is %v, want %v", schedule.LastServiceAt, servicedAt)
	}
	if schedule.LastServiceKM != 10_000 {
		t.Errorf("the last service was at %v km, want 10000 km", schedule.LastServiceKM)
	}
	if schedule.NextDueKM != 15_000 {
		t.Errorf("the next service is due at %v km, want 15000 km", schedule.NextDueKM)
	}
	if schedule.RemainingKM != 4500 {
		t.Errorf("what is left is %v km, want 4500 km", schedule.RemainingKM)
	}
	if schedule.Status != ServiceStatusOK {
		t.Errorf("the status is %q, want %q", schedule.Status, ServiceStatusOK)
	}
}

// The warning levels are ordered: nothing, then due soon, then overdue. The
// sweep compares them, so a unit that escalates is warned again.
func TestServiceLevelRank(t *testing.T) {
	if ServiceLevelRank(ServiceLevelNone) != 0 {
		t.Error("nothing warned about yet is not level 0")
	}
	if ServiceLevelRank(ServiceLevelDueSoon) <= ServiceLevelRank(ServiceLevelNone) {
		t.Error("due soon has to be higher than nothing")
	}
	if ServiceLevelRank(ServiceLevelOverdue) <= ServiceLevelRank(ServiceLevelDueSoon) {
		t.Error("overdue has to be higher than due soon")
	}
}

// The interval of a unit: it may be empty (the default of the server), but
// when it is there it has to make sense.
func TestServiceIntervalValidation(t *testing.T) {
	cases := []struct {
		name  string
		value *float64
		valid bool
	}{
		{"not set", nil, true},
		{"a normal interval", float64Ptr(5000), true},
		{"the smallest interval that makes sense", float64Ptr(0.1), true},
		{"the longest interval allowed", float64Ptr(MaxServiceIntervalKM), true},
		{"zero", float64Ptr(0), false},
		{"a negative interval", float64Ptr(-100), false},
		{"too long", float64Ptr(MaxServiceIntervalKM + 1), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := CreateInput{Code: "TRK-01", Name: "Truk 1", Kind: KindTruck, ServiceIntervalKM: tc.value}
			in.Normalize()
			errs := in.Validate()

			_, rejected := errs["service_interval_km"]
			if rejected == tc.valid {
				t.Errorf("the interval was rejected: %v, want rejected=%v", errs, !tc.valid)
			}
		})
	}
}

// A service that is done later than now is not a service yet.
func TestServiceRecordValidation(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)

	cases := []struct {
		name  string
		in    ServiceRecordInput
		field string
	}{
		{
			name:  "no date means now",
			in:    ServiceRecordInput{},
			field: "",
		},
		{
			name:  "a plain day",
			in:    ServiceRecordInput{ServicedAt: "2026-10-21"},
			field: "",
		},
		{
			name:  "a timestamp",
			in:    ServiceRecordInput{ServicedAt: "2026-10-21T09:30:00"},
			field: "",
		},
		{
			name:  "a date in the future",
			in:    ServiceRecordInput{ServicedAt: "2026-10-23"},
			field: "serviced_at",
		},
		{
			name:  "a moment in the future",
			in:    ServiceRecordInput{ServicedAt: "2026-10-22T18:00:00"},
			field: "serviced_at",
		},
		{
			name:  "a date that is not a date",
			in:    ServiceRecordInput{ServicedAt: "yesterday"},
			field: "serviced_at",
		},
		{
			name:  "a note that is too long",
			in:    ServiceRecordInput{Note: string(make([]byte, MaxServiceNote+1))},
			field: "note",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			in.Normalize(now)
			errs := in.Validate(now)

			if tc.field == "" {
				if len(errs) > 0 {
					t.Fatalf("the body was rejected: %v", errs)
				}
				return
			}
			if _, ok := errs[tc.field]; !ok {
				t.Errorf("the field %q was not rejected, got %v", tc.field, errs)
			}
		})
	}
}

// The note of a service is trimmed, and the moment of the service is read
// out of the body.
func TestServiceRecordNormalize(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)

	in := ServiceRecordInput{Note: "  ganti oli  ", ServicedAt: " 2026-10-21 "}
	in.Normalize(now)

	if in.Note != "ganti oli" {
		t.Errorf("the note is %q, want %q", in.Note, "ganti oli")
	}
	want := time.Date(2026, 10, 21, 0, 0, 0, 0, time.Local)
	if !in.At().Equal(want) {
		t.Errorf("the service is at %v, want %v", in.At(), want)
	}

	// A body without a date means the service was done now.
	empty := ServiceRecordInput{}
	empty.Normalize(now)
	if !empty.At().Equal(now) {
		t.Errorf("the service is at %v, want %v", empty.At(), now)
	}
}

// float64Ptr is a small helper for the tables above.
func float64Ptr(v float64) *float64 {
	return &v
}
