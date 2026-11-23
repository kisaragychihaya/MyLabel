// Package store is the SQLite working repository for MyLabel annotations.
//
// The database lives at <image root>/mylabel.db (WAL mode). All methods must
// be called from the UI main thread; Store deliberately has no internal
// locking. See docs/phase1-design.md §3 for the binding contract.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// SchemaVersion is the PRAGMA user_version written by Open.
// Version 2 drops the global PRIMARY KEY on annotations.id: annotation IDs
// are only meaningful within their image (the PoC JSON exchange format
// reuses IDs across sessions and even within one image), so uniqueness is
// enforced nowhere and insertion order (rowid) is the only ordering.
// Version 3 adds annotations.kind/points (polygons) and categories.color.
// Version 4 adds annotations.attrs, a JSON object of annotation attributes
// (docs/phase3-design.md §5.1).
const SchemaVersion = 4

// Status values for ImageRec.Status.
const (
	StatusNew      = "new"
	StatusLabeling = "labeling"
	StatusDone     = "done"
	StatusReviewed = "reviewed"
	StatusSkipped  = "skipped"
)

// AnnRec.Kind values.
const (
	KindRect = "rect" // axis-aligned (angle 0) or rotated rectangle
	KindPoly = "poly" // closed polygon, Points holds the vertices
)

// AnnRec is one annotation row. An empty Kind is stored and returned as
// KindRect. For KindPoly, Points holds the flattened vertices
// [x0,y0,x1,y1,...] in image pixels (implicitly closed) and CX/CY/W/H carry
// the bounding box with Angle 0; Points is authoritative. Points is preserved
// verbatim for KindRect, which ignores it.
//
// Attrs holds free-form annotation attributes (VOC difficult/truncated, COCO
// iscrowd, a user's own keys), stored as a JSON object; nil and an empty map
// are both stored as the empty string and load back as nil.
type AnnRec struct {
	ID     string
	Cat    int
	Kind   string
	CX     float64
	CY     float64
	W      float64
	H      float64
	Angle  float64
	Points []float64
	Attrs  map[string]string
}

// Category is one category row. Color is "#RRGGBB"; empty means the UI
// palette default for the category index.
type Category struct {
	Name  string
	Color string
}

// ImageRec is one image row, with its annotations attached on load.
type ImageRec struct {
	ID      int64  // filled by LoadImages; ignored on input
	RelPath string // slash-separated, relative to the image root
	Name    string // base file name
	Width   int
	Height  int
	SHA256  string   // hex content hash, computed after EXIF normalization
	Status  string   // empty on input means StatusNew
	Anns    []AnnRec // filled by LoadImages only
}

// Store is the annotation working repository. Not safe for concurrent use.
type Store struct {
	db *sql.DB
}

// driverName is the modernc.org/sqlite database/sql driver.
const driverName = "sqlite"

// busyTimeoutMS matches docs/phase1-design.md §3.2.
const busyTimeoutMS = 5000

// deleteChunk bounds the number of host parameters per DELETE statement so
// large keep sets never approach SQLITE_MAX_VARIABLE_NUMBER.
const deleteChunk = 500

var errStoreClosed = errors.New("store: closed")

// schemaV4 is the current schema, split per statement because a single Exec
// is not required to run multiple statements. Every statement is idempotent:
// on an existing v1/v2/v3 database the CREATE TABLE IF NOT EXISTS statements
// are no-ops, so the tables keep their old shape and the columns newer
// versions add are added by addMissingColumns instead. New columns are
// declared last so a freshly created table and a migrated one have the same
// column order.
var schemaV4 = []string{
	`CREATE TABLE IF NOT EXISTS categories (
  idx   INTEGER PRIMARY KEY,
  name  TEXT NOT NULL,
  color TEXT NOT NULL DEFAULT ''
)`,
	`CREATE TABLE IF NOT EXISTS images (
  id         INTEGER PRIMARY KEY,
  rel_path   TEXT NOT NULL UNIQUE,
  name       TEXT NOT NULL,
  width      INTEGER NOT NULL,
  height     INTEGER NOT NULL,
  sha256     TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'new'
)`,
	`CREATE TABLE IF NOT EXISTS annotations (
  id       TEXT NOT NULL,
  image_id INTEGER NOT NULL REFERENCES images(id) ON DELETE CASCADE,
  cat      INTEGER NOT NULL,
  cx REAL NOT NULL, cy REAL NOT NULL,
  w  REAL NOT NULL, h  REAL NOT NULL,
  angle REAL NOT NULL DEFAULT 0,
  kind   TEXT NOT NULL DEFAULT 'rect',
  points TEXT NOT NULL DEFAULT '',
  attrs  TEXT NOT NULL DEFAULT ''
)`,
	`CREATE INDEX IF NOT EXISTS idx_anns_image ON annotations(image_id)`,
}

// migrateV1V2 rebuilds the annotations table without the global PRIMARY KEY
// on id, preserving every row. It produces the v2 shape, so the v2 -> v3 step
// below still runs on the rebuilt table.
var migrateV1V2 = []string{
	`CREATE TABLE annotations_v2 (
  id       TEXT NOT NULL,
  image_id INTEGER NOT NULL REFERENCES images(id) ON DELETE CASCADE,
  cat      INTEGER NOT NULL,
  cx REAL NOT NULL, cy REAL NOT NULL,
  w  REAL NOT NULL, h  REAL NOT NULL,
  angle REAL NOT NULL DEFAULT 0
)`,
	`INSERT INTO annotations_v2 (id, image_id, cat, cx, cy, w, h, angle)
SELECT id, image_id, cat, cx, cy, w, h, angle FROM annotations`,
	`DROP TABLE annotations`,
	`ALTER TABLE annotations_v2 RENAME TO annotations`,
	`CREATE INDEX IF NOT EXISTS idx_anns_image ON annotations(image_id)`,
}

// columnsAddedSinceV2 lists every column the schema gained after version 2, in
// the order the versions added them: the v2 -> v3 columns first, then v4's.
// ALTER TABLE ADD COLUMN has no IF NOT EXISTS form and the CREATE TABLE IF NOT
// EXISTS statements in schemaV4 do not touch existing tables, so each column is
// added only after checking PRAGMA table_info. Probing per column, instead of
// per version, also repairs a database that lost a step half-way.
var columnsAddedSinceV2 = []struct {
	table, column, decl string
}{
	{"annotations", "kind", `TEXT NOT NULL DEFAULT 'rect'`},
	{"annotations", "points", `TEXT NOT NULL DEFAULT ''`},
	{"categories", "color", `TEXT NOT NULL DEFAULT ''`},
	{"annotations", "attrs", `TEXT NOT NULL DEFAULT ''`},
}

// dsn appends the per-connection PRAGMAs to path. busy_timeout and
// foreign_keys are connection scoped, so they must ride the DSN instead of a
// one-off Exec (which would only configure whichever pooled connection ran it).
// journal_mode is persistent but is set here as well to keep the declaration
// in one place.
func dsn(path string) string {
	return fmt.Sprintf("%s?_pragma=busy_timeout(%d)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)", path, busyTimeoutMS)
}

func (s *Store) conn() (*sql.DB, error) {
	if s == nil || s.db == nil {
		return nil, errStoreClosed
	}
	return s.db, nil
}

// Open opens (creating if needed) the database at path, applies the schema
// and sets PRAGMA user_version = SchemaVersion. WAL, foreign_keys and a
// busy_timeout are enabled. Opening an existing up-to-date database is a
// no-op; Open is idempotent.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: open: empty database path")
	}
	db, err := sql.Open(driverName, dsn(path))
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// One connection keeps per-connection PRAGMAs and transaction scope
	// unambiguous for the documented single-threaded caller.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// init applies the schema, pending migrations and the schema version. Every
// statement is idempotent, so Open on an up-to-date database leaves it
// unchanged. Migrations run one schema version at a time: v1 -> v2 rebuilds
// the annotations table, then every column added after v2 is added where it is
// missing, which covers v2 -> v3, v3 -> v4 and the tail of v1 -> v4 in the same
// step; a v1 database therefore goes through both steps in that order.
func (s *Store) init() error {
	var fk int
	if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		return fmt.Errorf("store: check foreign_keys: %w", err)
	}
	if fk != 1 {
		// Without it ON DELETE CASCADE silently stops working.
		return errors.New("store: could not enable pragma foreign_keys")
	}

	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("store: read user_version: %w", err)
	}
	if version > SchemaVersion {
		return fmt.Errorf("store: schema version %d is newer than supported version %d", version, SchemaVersion)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin schema transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Create anything that is missing at the current shape. On an existing
	// database every statement here is a no-op.
	for _, stmt := range schemaV4 {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("store: apply schema: %w", err)
		}
	}
	if version == 1 {
		// v1 -> v2: the v1 annotations table has a global PRIMARY KEY on id;
		// the CREATE above was a no-op for it, so rebuild the table.
		for _, stmt := range migrateV1V2 {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("store: migrate v1 to v2: %w", err)
			}
		}
	}
	if version < SchemaVersion {
		// v2 -> v3 -> v4 (and the tail of v1 -> v4): a brand new database
		// already got these columns from schemaV4, so nothing is added there.
		if err := addMissingColumns(tx); err != nil {
			return err
		}
	}
	if version != SchemaVersion {
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion)); err != nil {
			return fmt.Errorf("store: set user_version: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit schema: %w", err)
	}
	return nil
}

// addMissingColumns adds every column of columnsAddedSinceV2 its table does not
// have yet.
func addMissingColumns(tx *sql.Tx) error {
	for _, col := range columnsAddedSinceV2 {
		have, err := tableColumns(tx, col.table)
		if err != nil {
			return fmt.Errorf("store: inspect table %s: %w", col.table, err)
		}
		if _, ok := have[col.column]; ok {
			continue
		}
		stmt := "ALTER TABLE " + col.table + " ADD COLUMN " + col.column + " " + col.decl
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("store: add column %s.%s: %w", col.table, col.column, err)
		}
	}
	return nil
}

// tableColumns returns the column names of table. PRAGMA table_info yields no
// rows for an unknown table, so a missing table simply has no columns. The
// rows are drained and closed before returning because the caller runs on the
// store's single connection and may issue another statement next.
func tableColumns(tx *sql.Tx, table string) (map[string]struct{}, error) {
	rows, err := tx.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, err
	}
	cols := make(map[string]struct{})
	for rows.Next() {
		var (
			cid, notNull, pk int
			name, ctype      string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return nil, err
		}
		cols[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return cols, nil
}

// Close closes the database. Closing a nil or already closed Store is a no-op.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	db := s.db
	s.db = nil
	if err := db.Close(); err != nil {
		return fmt.Errorf("store: close: %w", err)
	}
	return nil
}

// Categories returns the categories ordered by idx. No rows -> empty slice.
// Color is "" when the category uses the palette default.
func (s *Store) Categories() ([]Category, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT name, color FROM categories ORDER BY idx`)
	if err != nil {
		return nil, fmt.Errorf("store: load categories: %w", err)
	}
	defer func() { _ = rows.Close() }()

	cats := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.Name, &c.Color); err != nil {
			return nil, fmt.Errorf("store: scan category: %w", err)
		}
		cats = append(cats, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate categories: %w", err)
	}
	return cats, nil
}

// SetCategories replaces the whole category list in one transaction. An empty
// Color is stored as "" (palette default); names are not validated.
func (s *Store) SetCategories(cats []Category) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin categories transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM categories`); err != nil {
		return fmt.Errorf("store: clear categories: %w", err)
	}
	if len(cats) > 0 {
		stmt, err := tx.Prepare(`INSERT INTO categories (idx, name, color) VALUES (?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("store: prepare category insert: %w", err)
		}
		defer func() { _ = stmt.Close() }()
		for i, c := range cats {
			if _, err := stmt.Exec(i, c.Name, c.Color); err != nil {
				return fmt.Errorf("store: insert category %d: %w", i, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit categories: %w", err)
	}
	return nil
}

// UpsertImages inserts or updates rows matched by RelPath in one
// transaction. Existing rows get name/width/height/sha256 updated while
// status and annotations are preserved. New rows get Status, or StatusNew
// when empty. Anns on input is ignored.
func (s *Store) UpsertImages(recs []ImageRec) error {
	if len(recs) == 0 {
		if _, err := s.conn(); err != nil {
			return err
		}
		return nil
	}
	db, err := s.conn()
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin upsert transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const q = `INSERT INTO images (rel_path, name, width, height, sha256, status)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(rel_path) DO UPDATE SET
  name   = excluded.name,
  width  = excluded.width,
  height = excluded.height,
  sha256 = excluded.sha256`
	stmt, err := tx.Prepare(q)
	if err != nil {
		return fmt.Errorf("store: prepare image upsert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, rec := range recs {
		status := rec.Status
		if status == "" {
			status = StatusNew
		}
		if _, err := stmt.Exec(rec.RelPath, rec.Name, rec.Width, rec.Height, rec.SHA256, status); err != nil {
			return fmt.Errorf("store: upsert image %q: %w", rec.RelPath, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit image upsert: %w", err)
	}
	return nil
}

// DeleteMissingImages deletes image rows whose rel_path is not in keep
// (annotations cascade) and returns how many rows were removed.
func (s *Store) DeleteMissingImages(keep []string) (int, error) {
	db, err := s.conn()
	if err != nil {
		return 0, err
	}
	keepSet := make(map[string]struct{}, len(keep))
	for _, rel := range keep {
		keepSet[rel] = struct{}{}
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: begin delete transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.Query(`SELECT id, rel_path FROM images`)
	if err != nil {
		return 0, fmt.Errorf("store: select image ids: %w", err)
	}
	var doomed []int64
	for rows.Next() {
		var (
			id      int64
			relPath string
		)
		if err := rows.Scan(&id, &relPath); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("store: scan image id: %w", err)
		}
		if _, ok := keepSet[relPath]; !ok {
			doomed = append(doomed, id)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("store: iterate image ids: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("store: close image ids: %w", err)
	}

	deleted := 0
	for start := 0; start < len(doomed); start += deleteChunk {
		chunk := doomed[start:min(start+deleteChunk, len(doomed))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		res, err := tx.Exec(`DELETE FROM images WHERE id IN (`+placeholders+`)`, args...)
		if err != nil {
			return 0, fmt.Errorf("store: delete images: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("store: delete image count: %w", err)
		}
		deleted += int(n)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit image delete: %w", err)
	}
	return deleted, nil
}

// LoadImages returns all images ordered by rel_path, each with its
// annotations ordered by insertion order (rowid).
func (s *Store) LoadImages() ([]ImageRec, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT id, rel_path, name, width, height, sha256, status FROM images ORDER BY rel_path`)
	if err != nil {
		return nil, fmt.Errorf("store: load images: %w", err)
	}
	var (
		images []ImageRec
		at     = make(map[int64]int)
	)
	for rows.Next() {
		var rec ImageRec
		if err := rows.Scan(&rec.ID, &rec.RelPath, &rec.Name, &rec.Width, &rec.Height, &rec.SHA256, &rec.Status); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store: scan image: %w", err)
		}
		at[rec.ID] = len(images)
		images = append(images, rec)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("store: iterate images: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: close images: %w", err)
	}
	if len(images) == 0 {
		return nil, nil
	}

	// Second pass: one query for every annotation, grouped back by image, so
	// the number of statements does not grow with the number of images.
	arows, err := db.Query(`SELECT image_id, id, cat, kind, points, cx, cy, w, h, angle, attrs FROM annotations ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("store: load annotations: %w", err)
	}
	defer func() { _ = arows.Close() }()
	for arows.Next() {
		var (
			imageID   int64
			ann       AnnRec
			kind      string
			rawPoints string
			rawAttrs  string
		)
		if err := arows.Scan(&imageID, &ann.ID, &ann.Cat, &kind, &rawPoints, &ann.CX, &ann.CY, &ann.W, &ann.H, &ann.Angle, &rawAttrs); err != nil {
			return nil, fmt.Errorf("store: scan annotation: %w", err)
		}
		i, ok := at[imageID]
		if !ok {
			continue
		}
		ann.Kind = normalizeKind(kind)
		points, err := decodePoints(rawPoints)
		if err != nil {
			return nil, fmt.Errorf("store: annotation %q of image %q: %w", ann.ID, images[i].RelPath, err)
		}
		ann.Points = points
		attrs, err := decodeAttrs(rawAttrs)
		if err != nil {
			return nil, fmt.Errorf("store: annotation %q of image %q: %w", ann.ID, images[i].RelPath, err)
		}
		ann.Attrs = attrs
		images[i].Anns = append(images[i].Anns, ann)
	}
	if err := arows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate annotations: %w", err)
	}
	return images, nil
}

// normalizeKind maps an empty Kind to the documented default KindRect.
func normalizeKind(kind string) string {
	if kind == "" {
		return KindRect
	}
	return kind
}

// encodePoints serializes flattened vertices as a JSON array string. A nil or
// empty slice is stored as the empty string, so "no points" needs no JSON.
// Callers add the annotation context to the error.
func encodePoints(points []float64) (string, error) {
	if len(points) == 0 {
		return "", nil
	}
	b, err := json.Marshal(points)
	if err != nil {
		return "", fmt.Errorf("encode points: %w", err)
	}
	return string(b), nil
}

// decodePoints is the inverse of encodePoints: "" -> nil, otherwise JSON.
func decodePoints(raw string) ([]float64, error) {
	if raw == "" {
		return nil, nil
	}
	var points []float64
	if err := json.Unmarshal([]byte(raw), &points); err != nil {
		return nil, fmt.Errorf("invalid points JSON %q: %w", raw, err)
	}
	return points, nil
}

// encodeAttrs serializes an attribute map as a JSON object. A nil or empty map
// is stored as the empty string, so "no attributes" needs no JSON. Callers add
// the annotation context to the error.
func encodeAttrs(attrs map[string]string) (string, error) {
	if len(attrs) == 0 {
		return "", nil
	}
	b, err := json.Marshal(attrs)
	if err != nil {
		return "", fmt.Errorf("encode attributes: %w", err)
	}
	return string(b), nil
}

// decodeAttrs is the inverse of encodeAttrs: "" -> nil, otherwise JSON.
func decodeAttrs(raw string) (map[string]string, error) {
	if raw == "" {
		return nil, nil
	}
	var attrs map[string]string
	if err := json.Unmarshal([]byte(raw), &attrs); err != nil {
		return nil, fmt.Errorf("invalid attributes JSON %q: %w", raw, err)
	}
	if len(attrs) == 0 {
		return nil, nil
	}
	return attrs, nil
}

// ReplaceAnns replaces all annotations of one image in a transaction
// (DELETE + INSERT). Kind defaults to KindRect, Points and Attrs are stored as
// JSON; geometry is not validated here.
func (s *Store) ReplaceAnns(imageID int64, anns []AnnRec) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin annotations transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM annotations WHERE image_id = ?`, imageID); err != nil {
		return fmt.Errorf("store: clear annotations of image %d: %w", imageID, err)
	}
	if len(anns) > 0 {
		stmt, err := tx.Prepare(`INSERT INTO annotations (id, image_id, cat, kind, points, cx, cy, w, h, angle, attrs) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("store: prepare annotation insert: %w", err)
		}
		defer func() { _ = stmt.Close() }()
		for _, ann := range anns {
			points, err := encodePoints(ann.Points)
			if err != nil {
				return fmt.Errorf("store: annotation %q of image %d: %w", ann.ID, imageID, err)
			}
			attrs, err := encodeAttrs(ann.Attrs)
			if err != nil {
				return fmt.Errorf("store: annotation %q of image %d: %w", ann.ID, imageID, err)
			}
			if _, err := stmt.Exec(ann.ID, imageID, ann.Cat, normalizeKind(ann.Kind), points, ann.CX, ann.CY, ann.W, ann.H, ann.Angle, attrs); err != nil {
				return fmt.Errorf("store: insert annotation %q of image %d: %w", ann.ID, imageID, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit annotations of image %d: %w", imageID, err)
	}
	return nil
}

// SetImageStatus updates one image's status.
func (s *Store) SetImageStatus(imageID int64, status string) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	if status == "" {
		status = StatusNew
	}
	if _, err := db.Exec(`UPDATE images SET status = ? WHERE id = ?`, status, imageID); err != nil {
		return fmt.Errorf("store: set status of image %d: %w", imageID, err)
	}
	return nil
}
