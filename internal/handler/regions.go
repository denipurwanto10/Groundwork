package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"entity-management/internal/db"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type RegionHandler struct {
	db *db.Pool
}

func NewRegionHandler(db *db.Pool) *RegionHandler {
	return &RegionHandler{db: db}
}

func (h *RegionHandler) ctxWithTimeout(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 20*time.Second)
}

type regionFeature struct {
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties"`
	Geometry   map[string]any `json:"geometry,omitempty"`
}

type regionFeatureCollection struct {
	Type     string          `json:"type"`
	Features []regionFeature `json:"features"`
}

func (h *RegionHandler) List(c *gin.Context) {
	levelStr := c.DefaultQuery("level", "")
	parent := c.Query("parent")
	code := c.Query("code")
	q := strings.TrimSpace(c.Query("q"))
	geom := c.DefaultQuery("geom", "true") != "false"

	if levelStr == "" {
		c.JSON(http.StatusBadRequest, errorBody("level_required", "query parameter 'level' (0-4) is required"))
		return
	}
	level, err := strconv.Atoi(levelStr)
	if err != nil || level < 0 || level > 4 {
		c.JSON(http.StatusBadRequest, errorBody("invalid_level", "level must be an integer 0..4"))
		return
	}

	// level 0 (country) and 1 (province) ignore parent
	if level <= 1 {
		parent = ""
	} else if parent == "" && q == "" {
		// A name search may span the whole level (e.g. one kecamatan out of
		// 7.069), so it does not need the parent drill-down filter.
		c.JSON(http.StatusBadRequest, errorBody("parent_required", "query parameter 'parent' is required for level >= 2 (or use 'q' to search)"))
		return
	}

	where := "WHERE level = $1"
	args := []any{level}
	argPos := 2
	if parent != "" {
		where += " AND parent_code = $" + strconv.Itoa(argPos)
		args = append(args, parent)
		argPos++
	}
	if code != "" {
		where += " AND code = $" + strconv.Itoa(argPos)
		args = append(args, code)
		argPos++
	}
	if q != "" {
		where += " AND name ILIKE $" + strconv.Itoa(argPos)
		args = append(args, "%"+q+"%")
		argPos++
	}

	// Search results are always bounded: the point of 'q' is to avoid
	// pulling a whole level into the browser.
	limit := 0
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 {
		limit = v
	} else if q != "" {
		limit = 40
	}
	if limit > 500 {
		limit = 500
	}
	order := " ORDER BY code"
	if q != "" {
		order = " ORDER BY name"
	}
	if limit > 0 {
		order += " LIMIT " + strconv.Itoa(limit)
	}

	geomSelect := ""
	if geom {
		// Geometry comes from the precomputed generalised column; only the
		// coordinate precision is tunable per request (5 decimals ~ 1 m).
		precision := 5
		if p := c.Query("precision"); p != "" {
			if v, err := strconv.Atoi(p); err == nil && v >= 0 && v <= 9 {
				precision = v
			}
		}
		geomSelect = fmt.Sprintf(
			", ST_AsGeoJSON(geom_display, %d)::json AS geojson", precision)
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	sql := "SELECT code, name, level, parent_code, source, license, source_version" + geomSelect +
		" FROM admin_regions " + where + order
	rows, err := h.db.Query(ctx, sql, args...)
	if err != nil {
		log.Printf("regions: list query failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	defer rows.Close()

	features := make([]regionFeature, 0)
	for rows.Next() {
		var code, name, source, license, sourceVersion string
		var level int
		var parentCode *string
		var geojsonRaw []byte
		if geom {
			err = rows.Scan(&code, &name, &level, &parentCode, &source, &license, &sourceVersion, &geojsonRaw)
		} else {
			err = rows.Scan(&code, &name, &level, &parentCode, &source, &license, &sourceVersion)
		}
		if err != nil {
			log.Printf("regions: list scan failed: %v", err)
			c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
			return
		}
		f := regionFeature{
			Type: "Feature",
			Properties: map[string]any{
				"code":           code,
				"name":           name,
				"level":          level,
				"parent_code":    parentCode,
				"source":         source,
				"license":        license,
				"source_version": sourceVersion,
			},
		}
		if geom && geojsonRaw != nil {
			var geomMap map[string]any
			_ = json.Unmarshal(geojsonRaw, &geomMap)
			f.Geometry = geomMap
		}
		features = append(features, f)
	}
	if err := rows.Err(); err != nil {
		log.Printf("regions: list rows failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": regionFeatureCollection{
		Type:     "FeatureCollection",
		Features: features,
	}})
}

func (h *RegionHandler) Count(c *gin.Context) {
	levelStr := c.DefaultQuery("level", "")
	parent := c.Query("parent")

	if levelStr == "" {
		c.JSON(http.StatusBadRequest, errorBody("level_required", "query parameter 'level' (0-4) is required"))
		return
	}
	level, err := strconv.Atoi(levelStr)
	if err != nil || level < 0 || level > 4 {
		c.JSON(http.StatusBadRequest, errorBody("invalid_level", "level must be an integer 0..4"))
		return
	}

	where := "WHERE level = $1"
	args := []any{level}
	if level >= 2 && parent != "" {
		where += " AND parent_code = $2"
		args = append(args, parent)
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	var cnt int
	err = h.db.QueryRow(ctx, "SELECT count(*) FROM admin_regions "+where, args...).Scan(&cnt)
	if err != nil {
		log.Printf("regions: count failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"count": cnt}})
}

func (h *RegionHandler) Get(c *gin.Context) {
	code := c.Param("code")
	levelStr := c.DefaultQuery("level", "")
	if levelStr == "" {
		c.JSON(http.StatusBadRequest, errorBody("level_required", "query parameter 'level' is required"))
		return
	}
	level, err := strconv.Atoi(levelStr)
	if err != nil || level < 0 || level > 4 {
		c.JSON(http.StatusBadRequest, errorBody("invalid_level", "level must be an integer 0..4"))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	var name, source, license, sourceVersion string
	var parentCode *string
	var geojsonRaw []byte
	err = h.db.QueryRow(ctx, `
		SELECT code, name, level, parent_code, source, license, source_version,
		       ST_AsGeoJSON(geom_display, 5)::json
		FROM admin_regions WHERE code = $1 AND level = $2`, code, level).Scan(
		&code, &name, &level, &parentCode, &source, &license, &sourceVersion, &geojsonRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, errorBody("not_found", "region not found"))
			return
		}
		log.Printf("regions: get code=%s level=%d failed: %v", code, level, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	f := regionFeature{
		Type: "Feature",
		Properties: map[string]any{
			"code":           code,
			"name":           name,
			"level":          level,
			"parent_code":    parentCode,
			"source":         source,
			"license":        license,
			"source_version": sourceVersion,
		},
	}
	if geojsonRaw != nil {
		var geomMap map[string]any
		_ = json.Unmarshal(geojsonRaw, &geomMap)
		f.Geometry = geomMap
	}
	c.JSON(http.StatusOK, gin.H{"data": f})
}
