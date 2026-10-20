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

// An island is not an administrative level in the BPS dataset: the official
// hierarchy is province -> regency -> district. What people mean by "pulau"
// (Jawa, Sumatera, Papua, …) is a group of whole provinces, and the BPS
// province code already encodes that grouping in its first two digits
// (11-21 Sumatera, 31-36 Jawa, 51-53 Bali & Nusa Tenggara, 61-65 Kalimantan,
// 71-76 Sulawesi, 81-82 Maluku, 91-96 Papua).
//
// So the island polygons below are unions of official province boundaries,
// grouped by that code prefix. Nothing here invents a boundary: it only merges
// real ones, and provinces created after 2022 (93-96) stay inside their group
// because the grouping is by prefix rather than by a hardcoded name list.
type islandGroup struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Prefixes []string `json:"-"`
}

var islandGroups = []islandGroup{
	{Key: "sumatera", Label: "Sumatera", Prefixes: []string{"11", "12", "13", "14", "15", "16", "17", "18", "19", "21"}},
	{Key: "jawa", Label: "Jawa", Prefixes: []string{"31", "32", "33", "34", "35", "36"}},
	{Key: "bali-nusa-tenggara", Label: "Bali & Nusa Tenggara", Prefixes: []string{"51", "52", "53"}},
	{Key: "kalimantan", Label: "Kalimantan", Prefixes: []string{"61", "62", "63", "64", "65"}},
	{Key: "sulawesi", Label: "Sulawesi", Prefixes: []string{"71", "72", "73", "74", "75", "76"}},
	{Key: "maluku", Label: "Maluku", Prefixes: []string{"81", "82"}},
	{Key: "papua", Label: "Papua", Prefixes: []string{"91", "92", "93", "94", "95", "96"}},
}

// provincePrefix returns the two-digit BPS grouping prefix of a province code.
// The COD-AB dataset stores codes with an "ID" country prefix (ID3173), so the
// digits that carry the island grouping start at index 2.
func provincePrefix(code string) string {
	code = strings.TrimSpace(code)
	if len(code) >= 4 && strings.EqualFold(code[:2], "ID") {
		return code[2:4]
	}
	if len(code) >= 2 {
		return code[:2]
	}
	return ""
}

func findIsland(key string) (islandGroup, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, g := range islandGroups {
		if g.Key == key {
			return g, true
		}
	}
	return islandGroup{}, false
}

// islandResp is what the picker lists: the group plus the provinces that
// actually exist in the imported dataset, so the UI never offers an empty one.
type islandResp struct {
	Key       string   `json:"key"`
	Label     string   `json:"label"`
	Provinces int      `json:"provinces"`
	Codes     []string `json:"codes"`
}

// Islands lists the island groups available in admin_regions.
func (h *ZoneHandler) Islands(c *gin.Context) {
	ctx, cancel := h.ctxWithTimeout(c)
	defer cancel()

	rows, err := h.db.Query(ctx,
		`SELECT code, name FROM admin_regions WHERE level = 1 ORDER BY code`)
	if err != nil {
		log.Printf("zones: islands query failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}
	defer rows.Close()

	type province struct {
		code string
		name string
	}
	provinces := make([]province, 0, 40)
	for rows.Next() {
		var p province
		if err := rows.Scan(&p.code, &p.name); err != nil {
			log.Printf("zones: islands scan failed: %v", err)
			c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
			return
		}
		provinces = append(provinces, p)
	}
	if err := rows.Err(); err != nil {
		log.Printf("zones: islands rows failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	out := make([]islandResp, 0, len(islandGroups))
	for _, g := range islandGroups {
		codes := make([]string, 0, len(g.Prefixes))
		for _, p := range provinces {
			pfx := provincePrefix(p.code)
			for _, prefix := range g.Prefixes {
				if pfx == prefix {
					codes = append(codes, p.code)
					break
				}
			}
		}
		if len(codes) == 0 {
			continue
		}
		out = append(out, islandResp{Key: g.Key, Label: g.Label, Provinces: len(codes), Codes: codes})
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{"islands": out, "count": len(out)}})
}

type islandZoneInput struct {
	Island      string `json:"island"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

// fromIslandSQL merges the province polygons of one island group into a single
// MultiPolygon zone. Every input is made valid first: a self-intersecting
// province would otherwise fail the whole union. admin_regions.geom is
// geography, so polygons are cast to geometry before repair and union.
const fromIslandSQL = `WITH src AS (
        SELECT ST_Multi(ST_CollectionExtract(ST_MakeValid(ST_Union(ST_MakeValid(r.geom::geometry))), 3)) AS geom,
               count(*) AS provinces
          FROM admin_regions r
         WHERE r.level = 1
           AND CASE WHEN r.code LIKE 'ID%' THEN substring(r.code FROM 3 FOR 2)
                    ELSE left(r.code, 2) END = ANY($1)
    )
    INSERT INTO zones (name, category, color, description, geom)
    SELECT COALESCE(NULLIF($2, ''), $3), $4, $5, $6, s.geom
      FROM src s
     WHERE s.provinces > 0
       AND s.geom IS NOT NULL
       AND NOT ST_IsEmpty(s.geom)
       AND ST_IsValid(s.geom)
    RETURNING id`

// FromIsland creates a zone whose polygon is the union of an island's
// provinces, e.g. "Jawa" or "Sumatera".
func (h *ZoneHandler) FromIsland(c *gin.Context) {
	var in islandZoneInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_json", "request body must be valid JSON"))
		return
	}

	group, ok := findIsland(in.Island)
	in.Name = strings.TrimSpace(in.Name)
	in.Category = strings.TrimSpace(strings.ToLower(in.Category))
	in.Description = strings.TrimSpace(in.Description)

	fields := map[string]string{}
	if !ok {
		fields["island"] = "Pick one of the listed island groups."
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

	// The island zone has no single region_code, so it is identified by name.
	// Unioning 40 provinces twice would just stack identical polygons.
	var (
		existingID   int64
		existingName string
	)
	err := h.db.QueryRow(ctx,
		`SELECT id, name FROM zones WHERE region_code IS NULL AND lower(name) = lower($1) ORDER BY id LIMIT 1`,
		group.Label).Scan(&existingID, &existingName)
	switch {
	case err == nil:
		c.JSON(http.StatusConflict, errorBody("zone_exists",
			"this island already has a zone: "+existingName+" (id "+strconv.FormatInt(existingID, 10)+")"))
		return
	case !errors.Is(err, pgx.ErrNoRows):
		log.Printf("zones: island duplicate check failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	name := in.Name
	description := in.Description
	if description == "" {
		description = group.Label + " — gabungan batas provinsi resmi (BPS)."
	}

	var id int64
	err = h.db.QueryRow(ctx, fromIslandSQL,
		group.Prefixes, name, group.Label, in.Category, in.Color, description).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		c.JSON(http.StatusNotFound, errorBody("not_found",
			"no province boundary stored for that island group (import admin level 1 first)"))
		return
	case isConstraintViolation(err):
		log.Printf("zones: from-island constraint: %v", err)
		c.JSON(http.StatusUnprocessableEntity, errorBody("invalid_geometry",
			"the merged boundary could not be stored as a valid polygon"))
		return
	case err != nil:
		log.Printf("zones: from-island insert failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	h.respondZone(c, id, http.StatusCreated)
}
