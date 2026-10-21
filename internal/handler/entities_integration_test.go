//go:build integration

// Integration tests for the entities CRUD API.
//
// Prerequisite: PostgreSQL reachable with the repo .env (DB_*), database
// created, and migrations/001_create_entities.up.sql applied.
// godotenv is loaded from the repo root (../../.env) because `go test`
// runs with the package directory as working directory.
//
// Run: go test -tags integration ./internal/handler/ -v
package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"entity-management/internal/config"
	"entity-management/internal/db"
	"entity-management/internal/ws"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func setupRouter(t *testing.T) (*gin.Engine, *db.Pool) {
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
	if _, err := pool.Exec(ctx, `DELETE FROM entities WHERE name LIKE 'itest-%'`); err != nil {
		t.Fatalf("cleanup leftovers: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pool.Exec(ctx, `DELETE FROM entities WHERE name LIKE 'itest-%'`)
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewEntityHandler(pool, ws.NewHub())
	api := r.Group("/api")
	api.GET("/entities", h.List)
	api.GET("/entities/:id", h.Get)
	api.POST("/entities", h.Create)
	api.PUT("/entities/:id", h.Update)
	api.DELETE("/entities/:id", h.Delete)

	return r, pool
}

func doRequest(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeData(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	data, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data object, got %q", w.Body.String())
	}
	return data
}

func TestEntitiesCRUDCycle(t *testing.T) {
	r, _ := setupRouter(t)

	// Create.
	w := doRequest(t, r, http.MethodPost, "/api/entities", map[string]any{
		"name": "itest-monas", "type": "monument", "status": "active",
		"latitude": -6.1754, "longitude": 106.8272,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201 (%s)", w.Code, w.Body.String())
	}
	created := decodeData(t, w)
	id := int64(created["id"].(float64))
	if id <= 0 || created["name"] != "itest-monas" {
		t.Fatalf("unexpected created entity: %v", created)
	}
	if created["created_at"] == nil || created["updated_at"] == nil {
		t.Fatalf("expected timestamps, got %v", created)
	}
	path := fmt.Sprintf("/api/entities/%d", id)

	// List contains it.
	w = doRequest(t, r, http.MethodGet, "/api/entities", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET list status = %d (%s)", w.Code, w.Body.String())
	}
	var listResp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, e := range listResp.Data {
		if int64(e["id"].(float64)) == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("created entity %d missing from list", id)
	}

	// Get one.
	w = doRequest(t, r, http.MethodGet, path, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET one status = %d (%s)", w.Code, w.Body.String())
	}
	if got := decodeData(t, w); got["name"] != "itest-monas" {
		t.Fatalf("unexpected entity: %v", got)
	}

	// Update (full replacement).
	w = doRequest(t, r, http.MethodPut, path, map[string]any{
		"name": "itest-monas-upd", "type": "landmark", "status": "inactive",
		"latitude": -6.2, "longitude": 106.8,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d (%s)", w.Code, w.Body.String())
	}
	if got := decodeData(t, w); got["name"] != "itest-monas-upd" || got["status"] != "inactive" {
		t.Fatalf("update not applied: %v", got)
	}

	// Delete.
	w = doRequest(t, r, http.MethodDelete, path, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204 (%s)", w.Code, w.Body.String())
	}

	// Gone.
	w = doRequest(t, r, http.MethodGet, path, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET after delete status = %d, want 404", w.Code)
	}
}

func TestEntitiesNotFound(t *testing.T) {
	r, _ := setupRouter(t)

	w := doRequest(t, r, http.MethodGet, "/api/entities/999999999", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET missing status = %d, want 404", w.Code)
	}

	w = doRequest(t, r, http.MethodPut, "/api/entities/999999999", map[string]any{
		"name": "itest-x", "type": "y", "status": "active", "latitude": 0, "longitude": 0,
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("PUT missing status = %d, want 404", w.Code)
	}

	w = doRequest(t, r, http.MethodDelete, "/api/entities/999999999", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("DELETE missing status = %d, want 404", w.Code)
	}
}

func TestEntitiesInvalidInput(t *testing.T) {
	r, _ := setupRouter(t)

	// Invalid IDs never reach the database.
	for _, path := range []string{
		"/api/entities/abc", "/api/entities/0", "/api/entities/-5", "/api/entities/1.5",
	} {
		if w := doRequest(t, r, http.MethodGet, path, nil); w.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d, want 400", path, w.Code)
		}
	}

	// Invalid bodies.
	cases := []struct {
		name string
		body map[string]any
	}{
		{"blank name", map[string]any{"name": "   ", "type": "t", "status": "active", "latitude": 0.0, "longitude": 0.0}},
		{"missing type", map[string]any{"name": "itest-x", "status": "active", "latitude": 0.0, "longitude": 0.0}},
		{"bad status", map[string]any{"name": "itest-x", "type": "t", "status": "deleted", "latitude": 0.0, "longitude": 0.0}},
		{"lat out of range", map[string]any{"name": "itest-x", "type": "t", "status": "active", "latitude": 91.0, "longitude": 0.0}},
		{"lon out of range", map[string]any{"name": "itest-x", "type": "t", "status": "active", "latitude": 0.0, "longitude": -181.0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRequest(t, r, http.MethodPost, "/api/entities", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("POST status = %d, want 400 (%s)", w.Code, w.Body.String())
			}
		})
	}

	// Malformed JSON.
	req := httptest.NewRequest(http.MethodPost, "/api/entities", bytes.NewReader([]byte("{not-json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST malformed JSON status = %d, want 400", w.Code)
	}
}

func TestParseID(t *testing.T) {
	if id, ok := parseID("42"); !ok || id != 42 {
		t.Fatalf("parseID(42) = %d,%v", id, ok)
	}
	for _, s := range []string{"", "abc", "0", "-1", "1.5", "9999999999999999999999"} {
		if _, ok := parseID(s); ok {
			t.Fatalf("parseID(%q) should fail", s)
		}
	}
}
