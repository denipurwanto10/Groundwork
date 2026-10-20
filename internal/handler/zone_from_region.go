package handler

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// regionZoneInput is the body of POST /api/zones/from-region: create a zone
// whose polygon is an official boundary the operator picked, instead of one
// drawn by hand.
type regionZoneInput struct {
	RegionCode  string `json:"region_code"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

// fromRegionSQL copies the boundary into zones. The source table stays
// read-only: this is an INSERT ... SELECT, never an UPDATE of admin_regions.
//
// ST_MakeValid + ST_CollectionExtract(…, 3) normalises the BPS shape to a
// valid MultiPolygon: some published boundaries self-intersect, and a
// GeometryCollection (a valid shape that lost its polygon type) would violate
// the zones.geom column type.
//
// admin_regions.geom is geography, and the validity functions only accept
// geometry, so the cast happens before repair. zones stores geometry, so the
// repaired value needs no cast back.
const fromRegionSQL = `WITH src AS (
        SELECT r.level, r.code, r.name,
               ST_Multi(ST_CollectionExtract(ST_MakeValid(r.geom::geometry), 3)) AS geom
          FROM admin_regions r
         WHERE r.code = $1
    )
    INSERT INTO zones (name, category, color, description, region_level, region_code, geom)
    SELECT COALESCE(NULLIF($2, ''), s.name), $3, $4,
           COALESCE(NULLIF($5, ''),
                    'Batas resmi ' || CASE s.level WHEN 1 THEN 'provinsi' WHEN 2 THEN 'kabupaten/kota'
                                                  WHEN 3 THEN 'kecamatan' ELSE 'wilayah' END
                    || ' ' || s.name || ' (BPS).'),
           s.level, s.code, s.geom
      FROM src s
     WHERE s.geom IS NOT NULL
       AND NOT ST_IsEmpty(s.geom)
       AND ST_IsValid(s.geom)
    RETURNING id`

// FromRegion creates a zone from an official administrative boundary.
func (h *ZoneHandler) FromRegion(c *gin.Context) {
	var in regionZoneInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be valid JSON"))
		return
	}

	fields := map[string]string{}

	in.RegionCode = strings.TrimSpace(in.RegionCode)
	in.Name = strings.TrimSpace(in.Name)
	in.Category = strings.TrimSpace(strings.ToLower(in.Category))
	in.Description = strings.TrimSpace(in.Description)

	if in.RegionCode == "" {
		fields["region_code"] = "Pick a region first."
	}
	if len(in.Name) > 120 {
		fields["name"] = "Name must be at most 120 characters."
	}
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
	if len(in.Description) > 500 {
		fields["description"] = "Description must be at most 500 characters."
	}
	if len(fields) > 0 {
		c.JSON(http.StatusBadRequest, validationErrorBody(fields))
		return
	}

	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	// One zone per region keeps the map readable and makes the picker's
	// "already added" state honest. The check runs before the insert so the
	// answer is a clear 409 instead of a duplicate polygon.
	var (
		existingID   int64
		existingName string
	)
	err := h.db.QueryRow(ctx,
		`SELECT id, name FROM zones WHERE region_code = $1 ORDER BY id LIMIT 1`,
		in.RegionCode).Scan(&existingID, &existingName)
	switch {
	case err == nil:
		c.JSON(http.StatusConflict, errorBody("zone_exists",
			"this region already has a zone: "+existingName+" (id "+strconv.FormatInt(existingID, 10)+")"))
		return
	case !errors.Is(err, pgx.ErrNoRows):
		log.Printf("zones: region duplicate check failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	var id int64
	err = h.db.QueryRow(ctx, fromRegionSQL,
		in.RegionCode, in.Name, in.Category, in.Color, in.Description).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		c.JSON(http.StatusNotFound, errorBody("not_found",
			"no boundary stored for that region code"))
		return
	case isConstraintViolation(err):
		log.Printf("zones: from-region constraint: %v", err)
		c.JSON(http.StatusUnprocessableEntity, errorBody("invalid_geometry",
			"the official boundary could not be stored as a valid polygon"))
		return
	case err != nil:
		log.Printf("zones: from-region insert failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	h.respondZone(c, id, http.StatusCreated)
}

