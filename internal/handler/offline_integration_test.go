//go:build integration

// Integration tests for the automatic offline detection.
//
// Prerequisite: PostgreSQL reachable with the repo .env (DB_*), database
// created, and the migrations applied.
//
// Run: go test -tags integration ./internal/handler/ -v
package handler

import (
	"context"
	"testing"
	"time"

	"entity-management/internal/config"
	"entity-management/internal/db"
	"entity-management/internal/ws"

	"github.com/joho/godotenv"
)

// offlineFixture is a handler with the automatic offline detection turned on.
func offlineFixture(t *testing.T, after time.Duration) (*VehicleHandler, *db.Pool) {
	t.Helper()

	if err := godotenv.Load("../../.env"); err != nil {
		t.Fatalf("load .env: %v (run from repo root with DB_PASSWORD set)", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	pool, err := db.NewPool(&cfg.Database)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `DELETE FROM vehicles WHERE code LIKE 'offtest-%'`); err != nil {
		t.Fatalf("cleanup leftovers: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pool.Exec(ctx, `DELETE FROM vehicles WHERE code LIKE 'offtest-%'`)
	})

	h := NewVehicleHandler(pool, ws.NewHub())
	h.SetOfflineAfter(after)

	return h, pool
}

// addVehicle creates a unit that last reported at the given moment.
func addVehicle(t *testing.T, pool *db.Pool, code string, lastSeen any) int64 {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var id int64
	err := pool.QueryRow(ctx, `
        INSERT INTO vehicles (code, name, kind, status, last_seen_at)
        VALUES ($1, $1, 'truck', 'moving', $2::timestamptz)
        RETURNING id`, code, lastSeen).Scan(&id)
	if err != nil {
		t.Fatalf("insert vehicle %s: %v", code, err)
	}

	return id
}

// statusOf reads the stored status of a unit.
func statusOf(t *testing.T, pool *db.Pool, id int64) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM vehicles WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}

	return status
}

func TestMarkStaleOfflineMarksTheSilentUnits(t *testing.T) {
	h, pool := offlineFixture(t, 60*time.Second)

	// Silent for two minutes: it fell asleep.
	silent := addVehicle(t, pool, "offtest-silent", time.Now().Add(-2*time.Minute))
	// Silent for ten seconds: it is still talking.
	fresh := addVehicle(t, pool, "offtest-fresh", time.Now().Add(-10*time.Second))
	// Never reported: it has nothing to be late about.
	never := addVehicle(t, pool, "offtest-never", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	n, err := h.MarkStaleOffline(ctx)
	if err != nil {
		t.Fatalf("MarkStaleOffline: %v", err)
	}
	if n != 1 {
		t.Errorf("marked %d unit(s), want 1", n)
	}

	if got := statusOf(t, pool, silent); got != "offline" {
		t.Errorf("the silent unit is %q, want \"offline\"", got)
	}
	if got := statusOf(t, pool, fresh); got != "moving" {
		t.Errorf("the fresh unit is %q, want \"moving\"", got)
	}
	if got := statusOf(t, pool, never); got != "moving" {
		t.Errorf("the unit that never reported is %q, want \"moving\"", got)
	}
}

func TestMarkStaleOfflineLeavesTheOfflineUnitsAlone(t *testing.T) {
	h, pool := offlineFixture(t, 60*time.Second)

	silent := addVehicle(t, pool, "offtest-silent", time.Now().Add(-2*time.Minute))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := h.MarkStaleOffline(ctx); err != nil {
		t.Fatalf("MarkStaleOffline: %v", err)
	}
	// The second sweep has nothing left to do: the unit is offline already.
	n, err := h.MarkStaleOffline(ctx)
	if err != nil {
		t.Fatalf("MarkStaleOffline: %v", err)
	}
	if n != 0 {
		t.Errorf("the second sweep marked %d unit(s), want 0", n)
	}
	if got := statusOf(t, pool, silent); got != "offline" {
		t.Errorf("the silent unit is %q, want \"offline\"", got)
	}
}

func TestMarkStaleOfflineIsOffWhenTheCheckIsTurnedOff(t *testing.T) {
	h, pool := offlineFixture(t, 0)

	silent := addVehicle(t, pool, "offtest-silent", time.Now().Add(-2*time.Minute))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	n, err := h.MarkStaleOffline(ctx)
	if err != nil {
		t.Fatalf("MarkStaleOffline: %v", err)
	}
	if n != 0 {
		t.Errorf("marked %d unit(s), want 0", n)
	}
	if got := statusOf(t, pool, silent); got != "moving" {
		t.Errorf("the silent unit is %q, want \"moving\"", got)
	}
}

// A unit that reports again comes back to life by itself: the position report
// writes a fresh status and a fresh timestamp.
func TestAUnitThatReportsAgainComesBack(t *testing.T) {
	h, pool := offlineFixture(t, 60*time.Second)

	id := addVehicle(t, pool, "offtest-back", time.Now().Add(-2*time.Minute))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := h.MarkStaleOffline(ctx); err != nil {
		t.Fatalf("MarkStaleOffline: %v", err)
	}
	if got := statusOf(t, pool, id); got != "offline" {
		t.Fatalf("the silent unit is %q, want \"offline\"", got)
	}

	if _, err := pool.Exec(ctx, `
        UPDATE vehicles
           SET status = 'moving', last_seen_at = now()
         WHERE id = $1`, id); err != nil {
		t.Fatalf("report a position: %v", err)
	}

	if _, err := h.MarkStaleOffline(ctx); err != nil {
		t.Fatalf("MarkStaleOffline: %v", err)
	}
	if got := statusOf(t, pool, id); got != "moving" {
		t.Errorf("the unit that reported again is %q, want \"moving\"", got)
	}
}
