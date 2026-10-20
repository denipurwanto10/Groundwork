package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"entity-management/internal/db"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ZoneHandler serves operator-drawn zones. Official administrative boundaries
// live in admin_regions and are read-only; these are editable/deletable.
type ZoneHandler struct {
	db *db.Pool
}

func NewZoneHandler(db *db.Pool) *ZoneHandler {
	return &ZoneHandler{db: db}
}

// ctxWithTimeout bounds zone queries. Slightly longer than the entity default
// because area/perimeter need a geography cast on each row.
func (h *ZoneHandler) ctxWithTimeout(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 15*time.Second)
}

var (
	colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	zoneCats     = map[string]bool{"operational": true, "restricted": true, "planned": true, "other": true}
	// BPS region codes are concatenated, not dotted: 2/4/6/9/12 chars per level.
	codeLenToLevel = map[int]int{2: 0, 4: 1, 6: 2, 9: 3, 12: 4}
)

// zoneInput is one zone as posted by the client. Geometry is passed straight
// through as raw JSON — PostGIS parses it, so no hand-written GeoJSON reader.
type zoneInput struct {
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	Color       string          `json:"color"`
	Description string          `json:"description"`
	RegionLevel *int            `json:"region_level"`
	RegionCode  *string         `json:"region_code"`
	Geometry    json.RawMessage `json:"geometry"`
}

// geomConds is the single source of truth for "is this an acceptable polygon".
// Applied inside the INSERT/UPDATE so a rejected shape can never be stored,
// regardless of what the frontend believed.
const geomConds = `g.geom IS NOT NULL
      AND ST_GeometryType(g.geom) IN ('ST_Polygon', 'ST_MultiPolygon')
      AND NOT ST_IsEmpty(g.geom)
      AND ST_IsValid(g.geom)
      AND ST_XMin(Box3D(g.geom)) >= -180 AND ST_XMax(Box3D(g.geom)) <= 180
      AND ST_YMin(Box3D(g.geom)) >= -90 AND ST_YMax(Box3D(g.geom)) <= 90`

// zoneCols already carries the SELECT keyword: it is only ever used as the
// head of a zone query, and keeping the keyword inside the constant removes the
// "SELECTz.id" concatenation hazard.
//
// The geometry here is a display copy. Above 2000 vertices a boundary is
// shipped simplified (200k+ points would be a 5 MB response) and the client
// never writes that copy back, because vertex editing is off at that size.
const zoneCols = `SELECT z.id, z.name, z.category, z.color, z.description,
       z.region_level, z.region_code, r.name,
       ST_AsGeoJSON(CASE WHEN ST_NPoints(z.geom) > 2000
            THEN ST_SimplifyPreserveTopology(z.geom, 0.0005) ELSE z.geom END, 6)::json,
       ST_Area(z.geom::geography) / 1e6,
       ST_Perimeter(z.geom::geography) / 1000,
       ST_NPoints(z.geom), z.created_at, z.updated_at,
       (SELECT count(*) FROM entities e WHERE ST_Covers(z.geom, e.geom::geometry)),
       (SELECT count(*) FROM vehicles v WHERE v.last_geom IS NOT NULL AND ST_Covers(z.geom, v.last_geom::geometry))`

const zoneFrom = ` FROM zones z
       LEFT JOIN admin_regions r ON r.level = z.region_level AND r.code = z.region_code`

// zoneFeature is a GeoJSON Feature plus the derived measurements, so a client
// never has to compute area/perimeter itself (and can never disagree with us).
type zoneFeature struct {
	Type       string         `json:"type"`
	ID         int64          `json:"id"`
	Properties map[string]any `json:"properties"`
	Geometry   map[string]any `json:"geometry"`
}

func scanZone(rows pgx.Rows) (zoneFeature, error) {
	var (
		id                          int64
		name, category, color, desc string
		regionLevel                 *int
		regionCode, regionName      *string
		geojsonRaw                  []byte
		areaKm2, perimeterKm        float64
		vertices                    int
		createdAt, updatedAt        any
		entityCount, vehicleCount   int
	)
	if err := rows.Scan(&id, &name, &category, &color, &desc,
		&regionLevel, &regionCode, &regionName, &geojsonRaw,
		&areaKm2, &perimeterKm, &vertices, &createdAt, &updatedAt,
		&entityCount, &vehicleCount); err != nil {
		return zoneFeature{}, err
	}
	f := zoneFeature{
		Type: "Feature",
		ID:   id,
		Properties: map[string]any{
			"id":            id,
			"name":          name,
			"category":      category,
			"color":         color,
			"description":   desc,
			"region_level":  regionLevel,
			"region_code":   regionCode,
			"region_name":   regionName,
			"area_km2":      round2(areaKm2),
			"perimeter_km":  round2(perimeterKm),
			"vertices":      vertices,
			"entity_count":  entityCount,
			"vehicle_count": vehicleCount,
			"created_at":    createdAt,
			"updated_at":    updatedAt,
		},
	}
	if geojsonRaw != nil {
		var geomMap map[string]any
		_ = json.Unmarshal(geojsonRaw, &geomMap)
		f.Geometry = geomMap
	}
	return f, nil
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// validateZoneInput covers everything checkable without a database. Anything
// geometric is left to PostGIS (see geomGuard). requireGeometry is false on
// update, where an omitted geometry means "keep the stored shape".
func validateZoneInput(in *zoneInput, requireGeometry bool) map[string]string {
	fields := map[string]string{}

	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		fields["name"] = "Name is required."
	} else if len(in.Name) > 120 {
		fields["name"] = "Name must be at most 120 characters."
	}

	in.Category = strings.TrimSpace(strings.ToLower(in.Category))
	if in.Category == "" {
		in.Category = "other"
	} else if !zoneCats[in.Category] {
		fields["category"] = "Category must be one of operational, restricted, planned, other."
	}

	if in.Color == "" {
		in.Color = "#2563eb"
	} else if !colorPattern.MatchString(in.Color) {
		fields["color"] = "Colour must be a hex value like #2563eb."
	}

	in.Description = strings.TrimSpace(in.Description)
	if len(in.Description) > 500 {
		fields["description"] = "Description must be at most 500 characters."
	}

	if in.RegionCode != nil && *in.RegionCode == "" {
		in.RegionCode = nil
	}
	if in.RegionCode == nil {
		in.RegionLevel = nil
	} else if in.RegionLevel == nil {
		lvl, ok := codeLenToLevel[len(*in.RegionCode)]
		if !ok {
			fields["region_code"] = "Region code must be 2, 4, 6, 9 or 12 characters."
		} else {
			in.RegionLevel = &lvl
		}
	} else if *in.RegionLevel < 0 || *in.RegionLevel > 4 {
		fields["region_level"] = "Region level must be between 0 and 4."
	}

	if len(in.Geometry) == 0 {
		if requireGeometry {
			fields["geometry"] = "A polygon geometry is required."
		}
	} else {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(in.Geometry, &probe); err != nil {
			fields["geometry"] = "Geometry must be a GeoJSON object."
		} else if probe.Type != "Polygon" && probe.Type != "MultiPolygon" {
			fields["geometry"] = "Geometry must be a Polygon or MultiPolygon."
		}
	}

	return fields
}

// geometryReason explains why PostGIS refused the shape, for a 400 the client
// can act on. Only called after a guarded write returned no row.
func (h *ZoneHandler) geometryReason(c *gin.Context, raw json.RawMessage) string {
	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	var gtype string
	var empty, valid bool
	var reason *string
	err := h.db.QueryRow(ctx, `
		SELECT ST_GeometryType(geom), ST_IsEmpty(geom), ST_IsValid(geom), ST_IsValidReason(geom)
		FROM (SELECT ST_GeomFromGeoJSON($1) AS geom) s`, string(raw)).
		Scan(&gtype, &empty, &valid, &reason)
	if err != nil {
		return "Geometry could not be parsed as GeoJSON."
	}
	switch {
	case gtype != "ST_Polygon" && gtype != "ST_MultiPolygon":
		return "Geometry must be a Polygon or MultiPolygon."
	case empty:
		return "Geometry is empty."
	case !valid:
		msg := "Geometry is not valid."
		if reason != nil && *reason != "" {
			msg = "Geometry is not valid: " + *reason + "."
		}
		return msg
	default:
		return "Geometry is outside the valid longitude/latitude range."
	}
}

// isBadGeometry reports the Postgres errors ST_GeomFromGeoJSON raises for
// malformed input, so they surface as 400 instead of 500.
func isBadGeometry(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "22023" || pgErr.Code == "XX000"
	}
	return false
}

func (h *ZoneHandler) List(c *gin.Context) {
	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	rows, err := h.db.Query(ctx, zoneCols+zoneFrom+" ORDER BY z.id")
	if err != nil {
		log.Printf("zones: list query failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	defer rows.Close()

	features := make([]zoneFeature, 0)
	for rows.Next() {
		f, err := scanZone(rows)
		if err != nil {
			log.Printf("zones: list scan failed: %v", err)
			c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
			return
		}
		features = append(features, f)
	}
	if err := rows.Err(); err != nil {
		log.Printf("zones: list rows failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"type": "FeatureCollection", "features": features}})
}

func (h *ZoneHandler) Get(c *gin.Context) {
	id, ok := parseZoneID(c)
	if !ok {
		return
	}
	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	rows, err := h.db.Query(ctx, zoneCols+zoneFrom+" WHERE z.id = $1", id)
	if err != nil {
		log.Printf("zones: get %d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	defer rows.Close()
	if !rows.Next() {
		c.JSON(http.StatusNotFound, errorBody("not_found", "zone not found"))
		return
	}
	f, err := scanZone(rows)
	if err != nil {
		log.Printf("zones: get %d scan failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": f})
}

func (h *ZoneHandler) Create(c *gin.Context) {
	var in zoneInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be valid JSON"))
		return
	}
	if fields := validateZoneInput(&in, true); len(fields) > 0 {
		c.JSON(http.StatusBadRequest, validationErrorBody(fields))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	var id int64
	err := h.db.QueryRow(ctx, `
		WITH g AS (SELECT ST_GeomFromGeoJSON($7) AS geom)
		INSERT INTO zones (name, category, color, description, region_level, region_code, geom)
		SELECT $1, $2, $3, $4, $5, $6, ST_Multi(g.geom) FROM g WHERE `+geomConds+`
		RETURNING id`,
		in.Name, in.Category, in.Color, in.Description, in.RegionLevel, in.RegionCode, string(in.Geometry)).Scan(&id)

	if err != nil {
		h.writeZoneWriteError(c, err, &in, "create")
		return
	}
	h.respondZone(c, id, http.StatusCreated)
}

func (h *ZoneHandler) Update(c *gin.Context) {
	id, ok := parseZoneID(c)
	if !ok {
		return
	}
	var in zoneInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be valid JSON"))
		return
	}
	if fields := validateZoneInput(&in, false); len(fields) > 0 {
		c.JSON(http.StatusBadRequest, validationErrorBody(fields))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	// A body without geometry only edits attributes: the stored outline is kept
	// as it is, so saving a 200k-point boundary never rewrites its shape.
	var updated int64
	var err error
	if len(in.Geometry) == 0 {
		err = h.db.QueryRow(ctx, `
			UPDATE zones SET name = $2, category = $3, color = $4, description = $5,
			                 region_level = $6, region_code = $7
			WHERE id = $1
			RETURNING id`,
			id, in.Name, in.Category, in.Color, in.Description, in.RegionLevel, in.RegionCode).Scan(&updated)
	} else {
		err = h.db.QueryRow(ctx, `
			WITH g AS (SELECT ST_GeomFromGeoJSON($8) AS geom)
			UPDATE zones SET name = $2, category = $3, color = $4, description = $5,
			                 region_level = $6, region_code = $7, geom = ST_Multi(g.geom)
			FROM g WHERE zones.id = $1 AND `+geomConds+`
			RETURNING zones.id`,
			id, in.Name, in.Category, in.Color, in.Description, in.RegionLevel, in.RegionCode, string(in.Geometry)).Scan(&updated)
	}

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if !h.zoneExists(c, id) {
				c.JSON(http.StatusNotFound, errorBody("not_found", "zone not found"))
				return
			}
			c.JSON(http.StatusBadRequest, validationErrorBody(map[string]string{"geometry": h.geometryReason(c, in.Geometry)}))
			return
		}
		h.writeZoneWriteError(c, err, &in, "update")
		return
	}
	h.respondZone(c, id, http.StatusOK)
}

func (h *ZoneHandler) Delete(c *gin.Context) {
	id, ok := parseZoneID(c)
	if !ok {
		return
	}
	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	var deleted int64
	err := h.db.QueryRow(ctx, `DELETE FROM zones WHERE id = $1 RETURNING id`, id).Scan(&deleted)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, errorBody("not_found", "zone not found"))
			return
		}
		log.Printf("zones: delete %d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	c.Status(http.StatusNoContent)
}

// Import accepts a GeoJSON FeatureCollection and stores every usable feature,
// reporting per-feature failures instead of discarding the whole batch.
func (h *ZoneHandler) Import(c *gin.Context) {
	var fc struct {
		Type     string `json:"type"`
		Features []struct {
			Type       string          `json:"type"`
			Properties map[string]any  `json:"properties"`
			Geometry   json.RawMessage `json:"geometry"`
		} `json:"features"`
	}
	if err := c.ShouldBindJSON(&fc); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be a valid GeoJSON FeatureCollection"))
		return
	}
	if len(fc.Features) == 0 {
		c.JSON(http.StatusBadRequest, errorBody("empty_collection", "the FeatureCollection contains no features"))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	imported := make([]zoneFeature, 0)
	failed := make([]gin.H, 0)

	for i, f := range fc.Features {
		in := zoneInput{
			Name:        propString(f.Properties, "name"),
			Category:    propString(f.Properties, "category"),
			Color:       propString(f.Properties, "color"),
			Description: propString(f.Properties, "description"),
			Geometry:    f.Geometry,
		}
		if code := propString(f.Properties, "region_code"); code != "" {
			in.RegionCode = &code
		}
		if lvl, ok := propNumber(f.Properties, "region_level"); ok {
			in.RegionLevel = &lvl
		}
		if fields := validateZoneInput(&in, true); len(fields) > 0 {
			failed = append(failed, gin.H{"index": i, "name": in.Name, "reason": firstFieldMessage(fields)})
			continue
		}

		var id int64
		err := h.db.QueryRow(ctx, `
			WITH g AS (SELECT ST_GeomFromGeoJSON($7) AS geom)
					INSERT INTO zones (name, category, color, description, region_level, region_code, geom)
					SELECT $1, $2, $3, $4, $5, $6, ST_Multi(g.geom) FROM g WHERE `+geomConds+`
					RETURNING id`,
			in.Name, in.Category, in.Color, in.Description, in.RegionLevel, in.RegionCode, string(in.Geometry)).Scan(&id)
		if err != nil {
			reason := "could not be stored"
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				reason = h.geometryReason(c, in.Geometry)
			case isConstraintViolation(err) || isForeignKeyViolation(err):
				reason = "linked region does not exist"
			}
			failed = append(failed, gin.H{"index": i, "name": in.Name, "reason": reason})
			continue
		}

		rows, err := h.db.Query(ctx, zoneCols+zoneFrom+" WHERE z.id = $1", id)
		if err == nil && rows.Next() {
			if zf, err := scanZone(rows); err == nil {
				imported = append(imported, zf)
			}
		}
		rows.Close()
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"imported": len(imported),
		"failed":   failed,
		"zones":    imported,
	}})
}

// respondZone re-reads the row so create/update return the same derived
// measurements (area, perimeter, region name) the list endpoint returns.
func (h *ZoneHandler) respondZone(c *gin.Context, id int64, status int) {
	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	rows, err := h.db.Query(ctx, zoneCols+zoneFrom+" WHERE z.id = $1", id)
	if err != nil {
		log.Printf("zones: reread %d failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	defer rows.Close()
	if !rows.Next() {
		c.JSON(http.StatusNotFound, errorBody("not_found", "zone not found"))
		return
	}
	f, err := scanZone(rows)
	if err != nil {
		log.Printf("zones: reread %d scan failed: %v", id, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	c.JSON(status, gin.H{"data": f})
}

func (h *ZoneHandler) zoneExists(c *gin.Context, id int64) bool {
	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()
	var one int
	err := h.db.QueryRow(ctx, `SELECT 1 FROM zones WHERE id = $1`, id).Scan(&one)
	return err == nil
}

func (h *ZoneHandler) writeZoneWriteError(c *gin.Context, err error, in *zoneInput, op string) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		c.JSON(http.StatusBadRequest, validationErrorBody(map[string]string{"geometry": h.geometryReason(c, in.Geometry)}))
	case isForeignKeyViolation(err):
		c.JSON(http.StatusBadRequest, validationErrorBody(map[string]string{"region_code": "Region does not exist."}))
	case isConstraintViolation(err):
		c.JSON(http.StatusBadRequest, validationErrorBody(map[string]string{"name": "Value rejected by the database constraints."}))
	case isBadGeometry(err):
		c.JSON(http.StatusBadRequest, validationErrorBody(map[string]string{"geometry": "Geometry could not be parsed as GeoJSON."}))
	default:
		log.Printf("zones: %s failed: %v", op, err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
	}
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	return false
}

func parseZoneID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, errorBody("invalid_id", "zone id must be a positive integer"))
		return 0, false
	}
	return id, true
}

func propString(props map[string]any, key string) string {
	if props == nil {
		return ""
	}
	if v, ok := props[key].(string); ok {
		return v
	}
	return ""
}

func propNumber(props map[string]any, key string) (int, bool) {
	if props == nil {
		return 0, false
	}
	if v, ok := props[key].(float64); ok {
		return int(v), true
	}
	return 0, false
}

func firstFieldMessage(fields map[string]string) string {
	for _, k := range []string{"name", "category", "color", "description", "geometry", "region_code", "region_level"} {
		if msg, ok := fields[k]; ok {
			return msg
		}
	}
	for _, msg := range fields {
		return msg
	}
	return "invalid feature"
}
