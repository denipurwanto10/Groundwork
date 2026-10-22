package handler

import (
	"testing"
	"time"
)

// A fixed place, so the tests do not depend on the machine they run on.
var reportTestLoc = time.FixedZone("WIB", 7*60*60)

func reportTestNow() time.Time {
	return time.Date(2026, 10, 22, 14, 30, 0, 0, reportTestLoc)
}

func TestReportPeriodDefaultsToTheLastSevenDays(t *testing.T) {
	now := reportTestNow()

	p, err := reportPeriodFrom("", "", now)
	if err != nil {
		t.Fatalf("reportPeriodFrom: %v", err)
	}

	// Today included: seven days, from the 16th to the 22nd.
	if p.FromDate != "2026-10-16" {
		t.Errorf("from = %q, want 2026-10-16", p.FromDate)
	}
	if p.ToDate != "2026-10-22" {
		t.Errorf("to = %q, want 2026-10-22", p.ToDate)
	}
	if want := 7 * 24 * time.Hour; p.To.Sub(p.From) != want {
		t.Errorf("the period covers %v, want %v", p.To.Sub(p.From), want)
	}
	// The end of the period is the first instant after it: tomorrow at
	// midnight, so the report can say "until the end of today".
	if !p.To.Equal(time.Date(2026, 10, 23, 0, 0, 0, 0, reportTestLoc)) {
		t.Errorf("the period ends at %v, want 2026-10-23 00:00", p.To)
	}
}

func TestReportPeriodReadsPlainDays(t *testing.T) {
	now := reportTestNow()

	p, err := reportPeriodFrom("2026-10-01", "2026-10-03", now)
	if err != nil {
		t.Fatalf("reportPeriodFrom: %v", err)
	}

	if p.FromDate != "2026-10-01" || p.ToDate != "2026-10-03" {
		t.Errorf("the period is %s..%s, want 2026-10-01..2026-10-03", p.FromDate, p.ToDate)
	}
	// The whole last day counts, so the period ends at the next midnight.
	if want := time.Date(2026, 10, 4, 0, 0, 0, 0, reportTestLoc); !p.To.Equal(want) {
		t.Errorf("the period ends at %v, want %v", p.To, want)
	}
	if want := 3 * 24 * time.Hour; p.To.Sub(p.From) != want {
		t.Errorf("the period covers %v, want %v", p.To.Sub(p.From), want)
	}
}

func TestReportPeriodReadsTimestamps(t *testing.T) {
	now := reportTestNow()

	p, err := reportPeriodFrom("2026-10-01T08:00:00+07:00", "2026-10-02T09:00:00+07:00", now)
	if err != nil {
		t.Fatalf("reportPeriodFrom: %v", err)
	}

	if p.FromDate != "2026-10-01" {
		t.Errorf("from = %q, want 2026-10-01", p.FromDate)
	}
	// The period ends at the instant it names, so the last day it holds is
	// the one before it.
	if p.ToDate != "2026-10-02" {
		t.Errorf("to = %q, want 2026-10-02", p.ToDate)
	}
	if want := 25 * time.Hour; p.To.Sub(p.From) != want {
		t.Errorf("the period covers %v, want %v", p.To.Sub(p.From), want)
	}
}

func TestReportPeriodRejectsAWrongFormat(t *testing.T) {
	now := reportTestNow()

	for _, tc := range []struct{ from, to string }{
		{"01-10-2026", ""},
		{"2026-10-01", "not a date"},
		{"2026/10/01", ""},
	} {
		if _, err := reportPeriodFrom(tc.from, tc.to, now); err == nil {
			t.Errorf("reportPeriodFrom(%q, %q) was accepted, want an error", tc.from, tc.to)
		}
	}
}

func TestReportPeriodRejectsAReversedRange(t *testing.T) {
	now := reportTestNow()

	_, err := reportPeriodFrom("2026-10-10", "2026-10-01", now)
	if err == nil {
		t.Fatal("a range that ends before it starts was accepted")
	}
	if got := err.Error(); got != "from must not be after to" {
		t.Errorf("the error is %q, want \"from must not be after to\"", got)
	}
}

func TestReportPeriodAcceptsASingleDay(t *testing.T) {
	now := reportTestNow()

	p, err := reportPeriodFrom("2026-10-10", "2026-10-10", now)
	if err != nil {
		t.Fatalf("reportPeriodFrom: %v", err)
	}
	if p.FromDate != "2026-10-10" || p.ToDate != "2026-10-10" {
		t.Errorf("the period is %s..%s, want 2026-10-10..2026-10-10", p.FromDate, p.ToDate)
	}
}

func TestReportPeriodLimitsTheRange(t *testing.T) {
	now := reportTestNow()

	// 92 days are allowed, one more is not.
	if _, err := reportPeriodFrom("2026-01-01", "2026-04-02", now); err != nil {
		t.Errorf("a range of 92 days was refused: %v", err)
	}
	_, err := reportPeriodFrom("2026-01-01", "2026-04-03", now)
	if err == nil {
		t.Fatal("a range of 93 days was accepted")
	}
	if got := err.Error(); got != "the range must be at most 92 days" {
		t.Errorf("the error is %q, want \"the range must be at most 92 days\"", got)
	}
}

func TestReportPeriodFillsTheMissingEnd(t *testing.T) {
	now := reportTestNow()

	// Only a start: the period is that one day.
	p, err := reportPeriodFrom("2026-10-10", "", now)
	if err != nil {
		t.Fatalf("reportPeriodFrom: %v", err)
	}
	if p.FromDate != "2026-10-10" || p.ToDate != "2026-10-10" {
		t.Errorf("the period is %s..%s, want 2026-10-10..2026-10-10", p.FromDate, p.ToDate)
	}

	// Only an end: the period is the day before it.
	p, err = reportPeriodFrom("", "2026-10-10", now)
	if err != nil {
		t.Fatalf("reportPeriodFrom: %v", err)
	}
	if p.FromDate != "2026-10-09" || p.ToDate != "2026-10-10" {
		t.Errorf("the period is %s..%s, want 2026-10-09..2026-10-10", p.FromDate, p.ToDate)
	}
}
