package handler

import (
	"testing"
	"time"
)

// The filter of the idle history: the same two ends the report knows, with
// the same format.
func TestIdleEventWindowReadsPlainDays(t *testing.T) {
	now := reportTestNow()

	from, to, err := idleEventWindow("2026-10-01", "2026-10-03", now)
	if err != nil {
		t.Fatalf("idleEventWindow: %v", err)
	}
	if from == nil || to == nil {
		t.Fatalf("expected both ends, got from=%v to=%v", from, to)
	}

	if want := time.Date(2026, 10, 1, 0, 0, 0, 0, reportTestLoc); !from.Equal(want) {
		t.Errorf("from = %v, want %v", from, want)
	}
	// The whole last day counts, so the window ends at the next midnight.
	if want := time.Date(2026, 10, 4, 0, 0, 0, 0, reportTestLoc); !to.Equal(want) {
		t.Errorf("to = %v, want %v", to, want)
	}
}

func TestIdleEventWindowReadsTimestamps(t *testing.T) {
	now := reportTestNow()

	from, to, err := idleEventWindow("2026-10-01T08:00:00Z", "2026-10-03T09:30:00Z", now)
	if err != nil {
		t.Fatalf("idleEventWindow: %v", err)
	}
	if from == nil || to == nil {
		t.Fatalf("expected both ends, got from=%v to=%v", from, to)
	}

	if want := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC); !from.Equal(want) {
		t.Errorf("from = %v, want %v", from, want)
	}
	// A timestamp is the exact instant: the window ends there, not a day
	// later.
	if want := time.Date(2026, 10, 3, 9, 30, 0, 0, time.UTC); !to.Equal(want) {
		t.Errorf("to = %v, want %v", to, want)
	}
}

func TestIdleEventWindowWithoutDatesIsEmpty(t *testing.T) {
	from, to, err := idleEventWindow("", "", reportTestNow())
	if err != nil {
		t.Fatalf("idleEventWindow: %v", err)
	}
	if from != nil || to != nil {
		t.Fatalf("expected no bounds, got from=%v to=%v", from, to)
	}
}

func TestIdleEventWindowRejectsRubbish(t *testing.T) {
	now := reportTestNow()

	if _, _, err := idleEventWindow("yesterday", "", now); err == nil {
		t.Error("expected a broken from to be rejected")
	}
	if _, _, err := idleEventWindow("", "22-10-2026", now); err == nil {
		t.Error("expected a broken to to be rejected")
	}
}
