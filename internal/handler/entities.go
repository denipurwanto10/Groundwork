package handler

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"entity-management/internal/db"
	"entity-management/internal/entity"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// dbTimeout bounds every entity query so a slow database cannot hang
// an HTTP request past the server write timeout.
const dbTimeout = 5 * time.Second

type EntityHandler struct {
	db *db.Pool
}

func NewEntityHandler(db *db.Pool) *EntityHandler {
	return &EntityHandler{db: db}
}

func errorBody(code, message string) gin.H {
	return gin.H{"error": gin.H{"code": code, "message": message}}
}

func validationErrorBody(fields map[string]string) gin.H {
	return gin.H{"error": gin.H{
		"code":    "validation_failed",
		"message": "validation failed",
		"fields":  fields,
	}}
}

// parseID converts a path ID to int64. Non-numeric, zero, and negative
// values are rejected without touching the database.
func parseID(s string) (int64, bool) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func (h *EntityHandler) ctxWithTimeout(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), dbTimeout)
}

// isConstraintViolation reports Postgres NOT NULL (23502) and CHECK
// (23514) violations. Defense in depth: input validation should catch
// these first, but a violation reaching the DB maps to 400, never 500.
func isConstraintViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23502" || pgErr.Code == "23514"
	}
	return false
}

func scanEntity(row pgx.Row) (entity.Entity, error) {
	var e entity.Entity
	err := row.Scan(
		&e.ID, &e.Name, &e.Type, &e.Status,
		&e.Latitude, &e.Longitude,
		&e.CreatedAt, &e.UpdatedAt,
	)
	return e, err
}

// List handles GET /api/entities.
func (h *EntityHandler) List(c *gin.Context) {
	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	rows, err := h.db.Query(ctx, `SELECT id, name, type, status, latitude, longitude, created_at, updated_at
		FROM entities ORDER BY id ASC`)
	if err != nil {
		log.Printf("entities: list query failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	defer rows.Close()

	entities := make([]entity.Entity, 0)
	for rows.Next() {
		e, err := scanEntity(rows)
		if err != nil {
			log.Printf("entities: list scan failed: %v", err)
			c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
			return
		}
		entities = append(entities, e)
	}
	if err := rows.Err(); err != nil {
		log.Printf("entities: list rows failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": entities})
}

// Get handles GET /api/entities/:id.
func (h *EntityHandler) Get(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.JSON(http.StatusBadRequest, errorBody("invalid_id", "invalid entity id"))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	e, err := scanEntity(h.db.QueryRow(ctx, `SELECT id, name, type, status, latitude, longitude, created_at, updated_at
		FROM entities WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, errorBody("not_found", "entity not found"))
			return
		}
		log.Printf("entities: get id=%d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": e})
}

// Create handles POST /api/entities.
func (h *EntityHandler) Create(c *gin.Context) {
	var in entity.EntityInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be valid JSON"))
		return
	}
	in.Normalize()
	if fields := in.Validate(); len(fields) > 0 {
		c.JSON(http.StatusBadRequest, validationErrorBody(fields))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	var e entity.Entity
	err := h.db.QueryRow(ctx, `INSERT INTO entities (name, type, status, latitude, longitude)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, name, type, status, latitude, longitude, created_at, updated_at`,
		in.Name, in.Type, in.Status, in.Latitude, in.Longitude).Scan(
		&e.ID, &e.Name, &e.Type, &e.Status,
		&e.Latitude, &e.Longitude,
		&e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		if isConstraintViolation(err) {
			c.JSON(http.StatusBadRequest, errorBody("validation_failed", "validation failed"))
			return
		}
		log.Printf("entities: create failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": e})
}

// Update handles PUT /api/entities/:id (full replacement).
func (h *EntityHandler) Update(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.JSON(http.StatusBadRequest, errorBody("invalid_id", "invalid entity id"))
		return
	}

	var in entity.EntityInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be valid JSON"))
		return
	}
	in.Normalize()
	if fields := in.Validate(); len(fields) > 0 {
		c.JSON(http.StatusBadRequest, validationErrorBody(fields))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	var e entity.Entity
	err := h.db.QueryRow(ctx, `UPDATE entities
		SET name = $1, type = $2, status = $3, latitude = $4, longitude = $5
		WHERE id = $6
		RETURNING id, name, type, status, latitude, longitude, created_at, updated_at`,
		in.Name, in.Type, in.Status, in.Latitude, in.Longitude, id).Scan(
		&e.ID, &e.Name, &e.Type, &e.Status,
		&e.Latitude, &e.Longitude,
		&e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, errorBody("not_found", "entity not found"))
			return
		}
		if isConstraintViolation(err) {
			c.JSON(http.StatusBadRequest, errorBody("validation_failed", "validation failed"))
			return
		}
		log.Printf("entities: update id=%d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": e})
}

// Delete handles DELETE /api/entities/:id.
func (h *EntityHandler) Delete(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		c.JSON(http.StatusBadRequest, errorBody("invalid_id", "invalid entity id"))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	res, err := h.db.Exec(ctx, `DELETE FROM entities WHERE id = $1`, id)
	if err != nil {
		log.Printf("entities: delete id=%d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	if res.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, errorBody("not_found", "entity not found"))
		return
	}

	c.Status(http.StatusNoContent)
}
