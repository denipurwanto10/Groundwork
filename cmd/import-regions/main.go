// Command import-regions loads Indonesian administrative boundaries into
// admin_regions.
//
// Source: HDX dataset `cod-ab-idn` — "Indonesia - Subnational Administrative
// Boundaries", published by Badan Pusat Statistik (BPS), licence CC BY-IGO.
//
// Usage:
//
//	go run ./cmd/import-regions -file <geojson|zip> -probe
//	go run ./cmd/import-regions -file <geojson|zip> -levels 0,1,2
//	go run ./cmd/import-regions -file <geojson|zip> -levels 3 -parent 12
//
// The archive holds one FeatureCollection for the whole country (well over a
// gigabyte of JSON), so features are decoded one at a time and streamed
// straight into a staging table; nothing is held in memory beyond the current
// feature.
//
// Re-runnable: rows are upserted on (level, code), so re-running refreshes the
// same regions instead of duplicating them.
package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"entity-management/internal/config"
	"entity-management/internal/db"

	"github.com/jackc/pgx/v5"
)

type feature struct {
	Type       string          `json:"type"`
	Properties map[string]any  `json:"properties"`
	Geometry   json.RawMessage `json:"geometry"`
}

type options struct {
	file       string
	entry      string
	levels     map[int]bool // at most one level per run
	wantLevel  int
	parent     string
	codeProp   string
	nameProp   string
	parentProp string
	version    string
	probe      bool
	limit      int
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		file       = flag.String("file", "", "path to the COD-AB .zip archive or a single .geojson file (required)")
		entry      = flag.String("entry", "", "zip entry to read (default: derived from -levels, e.g. idn_admin1.geojson)")
		levelsF    = flag.String("levels", "", "comma-separated levels to import, e.g. 1,2 (default: every level in the file)")
		parent     = flag.String("parent", "", "only import regions whose parent_code equals this value")
		codeProp   = flag.String("code-prop", "", "override the property holding the region code")
		nameProp   = flag.String("name-prop", "", "override the property holding the region name")
		parentProp = flag.String("parent-prop", "", "override the property holding the parent region code (default: adm{level-1}_pcode)")
		version    = flag.String("source-version", "COD-AB v01", "value recorded in admin_regions.source_version")
		probe      = flag.Bool("probe", false, "print the attribute keys of the first features and exit without writing")
		limit      = flag.Int("limit", 0, "stop after this many decoded features (0 = no limit); for smoke tests")
		dbName     = flag.String("db-name", "", "override database name from config (for testing against probe DB)")
	)
	flag.Parse()

	if *file == "" {
		flag.Usage()
		return fmt.Errorf("-file is required")
	}

	opts := options{
		file:    *file,
		entry:   *entry,
		levels:  map[int]bool{},
		parent:  *parent,
		version: *version,
		probe:   *probe,
		limit:   *limit,
	}
	for _, s := range strings.Split(*levelsF, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		var lvl int
		if _, err := fmt.Sscanf(s, "%d", &lvl); err != nil {
			return fmt.Errorf("bad -levels value %q: %w", s, err)
		}
		if lvl < 0 || lvl > 4 {
			return fmt.Errorf("-levels %d out of range 0..4", lvl)
		}
		opts.levels[lvl] = true
	}

	// A single level at a time: the archive stores one FeatureCollection per
	// level (idn_adminN.geojson), each with its own property names.
	if len(opts.levels) > 1 {
		return fmt.Errorf("-levels accepts one level per run (the archive stores a separate file per level); got %s", *levelsF)
	}
	for lvl := range opts.levels {
		opts.wantLevel = lvl
		opts.entry = defaultEntry(opts.entry, lvl)
		opts.codeProp = defaultProp(*codeProp, "pcode", lvl)
		opts.nameProp = defaultProp(*nameProp, "name", lvl)
		if lvl > 0 {
			opts.parentProp = defaultProp(*parentProp, "pcode", lvl-1)
		}
	}

	r, closer, srcName, err := openSource(opts.file, opts.entry)
	if err != nil {
		return err
	}
	defer closer()

	next := newFeatureStream(r)

	if opts.probe {
		return probeSource(next, srcName)
	}

	if len(opts.levels) == 0 {
		return fmt.Errorf("-levels is required when importing (the level cannot be guessed from the code before the file is read)")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if *dbName != "" {
		cfg.Database.Name = *dbName
	}
	pool, err := db.NewPool(&cfg.Database)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	return importRegions(context.Background(), pool, next, srcName, opts)
}

func defaultEntry(current string, level int) string {
	if current != "" {
		return current
	}
	return fmt.Sprintf("idn_admin%d.geojson", level)
}

// defaultProp builds the COD-AB property name for a level, e.g.
// ("", "pcode", 2) -> "adm2_pcode".
func defaultProp(current, suffix string, level int) string {
	if current != "" {
		return current
	}
	if level == 0 {
		return "adm0_" + suffix
	}
	return fmt.Sprintf("adm%d_%s", level, suffix)
}

// openSource opens a .geojson file, or the named entry inside a .zip archive.
func openSource(path, entry string) (io.Reader, func(), string, error) {
	if strings.HasSuffix(strings.ToLower(path), ".zip") {
		zr, err := zip.OpenReader(path)
		if err != nil {
			return nil, nil, "", fmt.Errorf("open zip: %w", err)
		}
		if entry == "" {
			return nil, nil, "", fmt.Errorf("-entry is required for a zip archive")
		}
		for _, f := range zr.File {
			if f.Name != entry {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				zr.Close()
				return nil, nil, "", fmt.Errorf("open zip entry %s: %w", entry, err)
			}
			return rc, func() { rc.Close(); zr.Close() }, f.Name, nil
		}
		names := make([]string, 0, len(zr.File))
		for _, f := range zr.File {
			names = append(names, f.Name)
		}
		zr.Close()
		return nil, nil, "", fmt.Errorf("entry %q not in %s (contains: %s)", entry, path, strings.Join(names, ", "))
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, "", fmt.Errorf("open file: %w", err)
	}
	return f, func() { f.Close() }, path, nil
}

// newFeatureStream walks a FeatureCollection without loading it, returning a
// function that yields one feature per call.
//
// One json.Decoder is shared across every call on purpose: a fresh decoder per
// batch would discard the previous one's read-ahead buffer and silently corrupt
// the stream. Memory stays flat regardless of file size.
func newFeatureStream(r io.Reader) func() (feature, bool, error) {
	dec := json.NewDecoder(r)
	atFeatures := false
	return func() (feature, bool, error) {
		if !atFeatures {
			for {
				tok, err := dec.Token()
				if err == io.EOF {
					return feature{}, false, nil
				}
				if err != nil {
					return feature{}, false, fmt.Errorf("read token: %w", err)
				}
				if key, ok := tok.(string); ok && key == "features" {
					if _, err := dec.Token(); err != nil { // opening bracket
						return feature{}, false, fmt.Errorf("read features array: %w", err)
					}
					atFeatures = true
					break
				}
			}
		}
		if !dec.More() {
			return feature{}, false, nil
		}
		var f feature
		if err := dec.Decode(&f); err != nil {
			return feature{}, false, fmt.Errorf("decode feature: %w", err)
		}
		return f, true, nil
	}
}

func probeSource(next func() (feature, bool, error), srcName string) error {
	fmt.Println("source:", srcName)
	for i := 1; i <= 3; i++ {
		f, ok, err := next()
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("(fewer than 3 features)")
			break
		}
		keys := make([]string, 0, len(f.Properties))
		for k := range f.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Printf("\n--- feature %d ---\ngeometry type: %s\nproperties (%d):\n", i, geometryType(f.Geometry), len(keys))
		for _, k := range keys {
			v := fmt.Sprint(f.Properties[k])
			if len(v) > 70 {
				v = v[:70] + "..."
			}
			fmt.Printf("  %-24s = %s\n", k, v)
		}
	}
	return nil
}

func geometryType(raw json.RawMessage) string {
	var g struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		return ""
	}
	return g.Type
}

// levelCodePrefixLen holds the BPS code width per level. The dataset
// concatenates segments (ID11, ID1107, ID1306050, ID1671060006) rather than
// separating them with dots, so the shape of a code has to be read by length.
var levelCodePrefixLen = [5]int{2, 4, 6, 9, 12}

// levelFromCode infers the administrative level from the length of a
// COD-AB/BPS region code: ID -> 0, ID11 -> 1, ID1107 -> 2,
// ID1306050 -> 3, ID1671060006 -> 4.
func levelFromCode(code string) int {
	code = strings.TrimSpace(code)
	for lvl := len(levelCodePrefixLen) - 1; lvl >= 0; lvl-- {
		if len(code) >= levelCodePrefixLen[lvl] {
			return lvl
		}
	}
	return -1
}

// parentFromCode is the fallback for when the source feature does not carry an
// explicit parent property: it trims the code to the previous level's width.
func parentFromCode(code string, level int) string {
	code = strings.TrimSpace(code)
	if level <= 0 || level >= len(levelCodePrefixLen) {
		return ""
	}
	n := levelCodePrefixLen[level-1]
	if len(code) < n {
		return ""
	}
	return code[:n]
}

func propString(p map[string]any, key string) string {
	if key == "" {
		return ""
	}
	v, ok := p[key]
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func importRegions(ctx context.Context, pool *db.Pool, next func() (feature, bool, error), srcName string, opts options) error {
	started := time.Now()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE stage_regions (
		code TEXT, name TEXT, level SMALLINT, parent_code TEXT, geom_geojson TEXT
	) ON COMMIT DROP`); err != nil {
		return fmt.Errorf("create staging table: %w", err)
	}

	// Counters, reported at the end so the run proves what it did.
	byLevel := map[int]int{}
	read, skippedUnusable, skippedDup, levelMismatch := 0, 0, 0, 0
	seen := map[string]bool{}
	maxLevel := opts.wantLevel

	const batchSize = 500
	batch := make([][]any, 0, batchSize)

	// Buffer all valid features into staging via batched INSERT.
	// This avoids pgx COPY protocol issues and is still fast for our row counts.
	for {
		if opts.limit > 0 && read >= opts.limit {
			break
		}
		f, ok, err := next()
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		read++

		code := propString(f.Properties, opts.codeProp)
		name := propString(f.Properties, opts.nameProp)
		if code == "" || name == "" {
			skippedUnusable++
			continue
		}

		// The file we chose defines the level, so it is authoritative;
		// a code whose shape disagrees means the source is malformed or
		// we picked the wrong entry. Count it and trust the file.
		level := opts.wantLevel
		if !opts.probe && levelFromCode(code) != level {
			levelMismatch++
		}

		// The source feature carries the parent code explicitly
		// (adm1_pcode inside an adm2 feature); that is authoritative.
		// Strip-the-prefix is only the fallback.
		parent := propString(f.Properties, opts.parentProp)
		if parent == "" {
			parent = parentFromCode(code, level)
		}
		if opts.parent != "" && parent != opts.parent {
			continue
		}

		geomJSON := ""
		if t := geometryType(f.Geometry); t == "Polygon" || t == "MultiPolygon" {
			geomJSON = string(f.Geometry)
		}

		// A duplicate (level, code) inside one run makes the upsert fail
		// with "cannot affect row a second time"; keep the first.
		key := fmt.Sprintf("%d|%s", level, code)
		if seen[key] {
			skippedDup++
			continue
		}
		seen[key] = true
		byLevel[level]++

		batch = append(batch, []any{code, name, level, nullIfEmpty(parent), nullIfEmpty(geomJSON)})

		if len(batch) >= batchSize {
			if err := flushBatch(ctx, tx, batch); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}

	// Flush remaining batch.
	if len(batch) > 0 {
		if err := flushBatch(ctx, tx, batch); err != nil {
			return err
		}
	}

	fmt.Printf("source:      %s\n", srcName)
	fmt.Printf("features read: %d, staged: %d\n", read, read-skippedUnusable-skippedDup)
	for lvl := 0; lvl <= maxLevel; lvl++ {
		if n, ok := byLevel[lvl]; ok {
			fmt.Printf("  level %d:     %d\n", lvl, n)
		}
	}
	fmt.Printf("skipped (no name/code/usable level): %d\n", skippedUnusable)
	fmt.Printf("skipped (duplicate level+code):      %d\n", skippedDup)
	fmt.Printf("code shape disagrees with level:     %d\n", levelMismatch)

	// One upsert for the whole batch. ST_MakeValid repairs self-intersections
	// instead of importing them; ST_CollectionExtract(...,3) drops any point or
	// line fragments it can produce, so the value fits geography(MultiPolygon).
	// Repair only runs when the geometry is actually invalid — it is by far the
	// most expensive step and the source is mostly clean.
	tag, err := tx.Exec(ctx, `
		WITH parsed AS (
			SELECT DISTINCT ON (level, code)
				code, name, level, parent_code,
				ST_SetSRID(ST_GeomFromGeoJSON(geom_geojson), 4326) AS geom
			FROM stage_regions
			ORDER BY level, code
		),
		repaired AS (
			SELECT code, name, level, parent_code,
				ST_Multi(ST_CollectionExtract(
					CASE WHEN ST_IsValid(geom) THEN geom ELSE ST_MakeValid(geom) END, 3
				))::geography AS g
			FROM parsed
		)
		INSERT INTO public.admin_regions (code, name, level, parent_code, geom, source_version)
		SELECT code, name, level, parent_code, g, $1
		FROM repaired
		WHERE g IS NOT NULL AND NOT ST_IsEmpty(g::geometry)
		ON CONFLICT (level, code) DO UPDATE SET
			name = EXCLUDED.name,
			parent_code = EXCLUDED.parent_code,
			geom = EXCLUDED.geom,
			source_version = EXCLUDED.source_version,
			updated_at = now()`,
		opts.version)
	if err != nil {
		return fmt.Errorf("upsert admin_regions: %w", err)
	}
	fmt.Printf("rows inserted or refreshed: %d\n", tag.RowsAffected())

	// A child whose parent_code has no matching row means the hierarchy is
	// broken; the API and the drill-down rely on it, so surface it rather than
	// hiding it.
	var orphans int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM public.admin_regions c
		WHERE c.parent_code IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM public.admin_regions p WHERE p.code = c.parent_code)
	`).Scan(&orphans); err != nil {
		return fmt.Errorf("check parent references: %w", err)
	}
	fmt.Printf("orphans (parent_code with no matching row): %d\n", orphans)

	var total, withoutGeom, invalid int
	if err := tx.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE geom IS NULL),
		       count(*) FILTER (WHERE geom IS NOT NULL AND NOT ST_IsValid(geom::geometry))
		FROM admin_regions`).Scan(&total, &withoutGeom, &invalid); err != nil {
		return fmt.Errorf("verify admin_regions: %w", err)
	}
	fmt.Printf("admin_regions now: %d rows, %d without geometry, %d invalid\n", total, withoutGeom, invalid)

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	fmt.Printf("done in %s\n", time.Since(started).Round(time.Second))
	return nil
}

func flushBatch(ctx context.Context, tx pgx.Tx, batch [][]any) error {
	if len(batch) == 0 {
		return nil
	}
	placeholders := make([]string, len(batch))
	args := make([]any, 0, len(batch)*5)
	for i, row := range batch {
		placeholders[i] = fmt.Sprintf("($%d,$%d,$%d,$%d,$%d)",
			i*5+1, i*5+2, i*5+3, i*5+4, i*5+5)
		args = append(args, row...)
	}
	sql := "INSERT INTO stage_regions (code, name, level, parent_code, geom_geojson) VALUES " +
		strings.Join(placeholders, ",") + " ON CONFLICT DO NOTHING"
	_, err := tx.Exec(ctx, sql, args...)
	return err
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
