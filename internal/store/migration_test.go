package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

// ---- old-schema fixtures ----

// buildDB runs stmts against a fresh database at path and closes it, leaving a
// hand-made old-schema fixture for Open to migrate.
func buildDB(t *testing.T, path string, stmts []string) {
	t.Helper()
	db, err := sql.Open(driverName, dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			_ = db.Close()
			t.Fatalf("build db: %v (statement %s)", err, q)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

// v1Stmts is a version-1 database: annotations.id is a global PRIMARY KEY and
// neither annotations nor categories have the v3 columns.
func v1Stmts() []string {
	return []string{
		`CREATE TABLE categories (
		  idx  INTEGER PRIMARY KEY,
		  name TEXT NOT NULL
		)`,
		`CREATE TABLE images (
		  id         INTEGER PRIMARY KEY,
		  rel_path   TEXT NOT NULL UNIQUE,
		  name       TEXT NOT NULL,
		  width      INTEGER NOT NULL,
		  height     INTEGER NOT NULL,
		  sha256     TEXT NOT NULL,
		  status     TEXT NOT NULL DEFAULT 'new'
		)`,
		`CREATE TABLE annotations (
		  id       TEXT PRIMARY KEY,
		  image_id INTEGER NOT NULL REFERENCES images(id) ON DELETE CASCADE,
		  cat      INTEGER NOT NULL,
		  cx REAL NOT NULL, cy REAL NOT NULL,
		  w  REAL NOT NULL, h  REAL NOT NULL,
		  angle REAL NOT NULL DEFAULT 0
		)`,
		`INSERT INTO categories (idx, name) VALUES (0, '缺陷'), (1, '划痕')`,
		`INSERT INTO images (id, rel_path, name, width, height, sha256, status)
		 VALUES (1, 'a.png', 'a.png', 8, 6, 'aa', 'done'), (2, 'b.png', 'b.png', 8, 6, 'bb', 'new')`,
		`INSERT INTO annotations (id, image_id, cat, cx, cy, w, h, angle)
		 VALUES ('ann_1', 1, 0, 1, 1, 2, 2, 0.5), ('ann_2', 1, 1, 3, 3, 2, 2, 0)`,
		`PRAGMA user_version = 1`,
	}
}

// v2Stmts is a version-2 database: the global primary key on annotations.id is
// gone, but the v3 columns (kind/points/color) do not exist yet.
func v2Stmts() []string {
	return []string{
		`CREATE TABLE categories (
		  idx  INTEGER PRIMARY KEY,
		  name TEXT NOT NULL
		)`,
		`CREATE TABLE images (
		  id         INTEGER PRIMARY KEY,
		  rel_path   TEXT NOT NULL UNIQUE,
		  name       TEXT NOT NULL,
		  width      INTEGER NOT NULL,
		  height     INTEGER NOT NULL,
		  sha256     TEXT NOT NULL,
		  status     TEXT NOT NULL DEFAULT 'new'
		)`,
		`CREATE TABLE annotations (
		  id       TEXT NOT NULL,
		  image_id INTEGER NOT NULL REFERENCES images(id) ON DELETE CASCADE,
		  cat      INTEGER NOT NULL,
		  cx REAL NOT NULL, cy REAL NOT NULL,
		  w  REAL NOT NULL, h  REAL NOT NULL,
		  angle REAL NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX idx_anns_image ON annotations(image_id)`,
		`INSERT INTO categories (idx, name) VALUES (0, '缺陷'), (1, '划痕')`,
		`INSERT INTO images (id, rel_path, name, width, height, sha256, status)
		 VALUES (1, 'a.png', 'a.png', 8, 6, 'aa', 'done'), (2, 'b.png', 'b.png', 8, 6, 'bb', 'new')`,
		`INSERT INTO annotations (id, image_id, cat, cx, cy, w, h, angle)
		 VALUES ('ann_1', 1, 0, 1, 1, 2, 2, 0.5), ('ann_2', 1, 1, 3, 3, 2, 2, 0)`,
		`PRAGMA user_version = 2`,
	}
}

// v3Stmts is a version-3 database: the shape v3 creates, i.e. annotations with
// kind/points and categories with color, but no v4 annotations.attrs column.
func v3Stmts() []string {
	return []string{
		`CREATE TABLE categories (
		  idx   INTEGER PRIMARY KEY,
		  name  TEXT NOT NULL,
		  color TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE images (
		  id         INTEGER PRIMARY KEY,
		  rel_path   TEXT NOT NULL UNIQUE,
		  name       TEXT NOT NULL,
		  width      INTEGER NOT NULL,
		  height     INTEGER NOT NULL,
		  sha256     TEXT NOT NULL,
		  status     TEXT NOT NULL DEFAULT 'new'
		)`,
		`CREATE TABLE annotations (
		  id       TEXT NOT NULL,
		  image_id INTEGER NOT NULL REFERENCES images(id) ON DELETE CASCADE,
		  cat      INTEGER NOT NULL,
		  cx REAL NOT NULL, cy REAL NOT NULL,
		  w  REAL NOT NULL, h  REAL NOT NULL,
		  angle REAL NOT NULL DEFAULT 0,
		  kind   TEXT NOT NULL DEFAULT 'rect',
		  points TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX idx_anns_image ON annotations(image_id)`,
		`INSERT INTO categories (idx, name, color) VALUES (0, '缺陷', '#ff0000'), (1, '划痕', '')`,
		`INSERT INTO images (id, rel_path, name, width, height, sha256, status)
		 VALUES (1, 'a.png', 'a.png', 8, 6, 'aa', 'done')`,
		`INSERT INTO annotations (id, image_id, cat, cx, cy, w, h, angle, kind, points)
		 VALUES ('rect_1', 1, 0, 1, 1, 2, 2, 0.5, 'rect', ''),
		        ('poly_1', 1, 1, 5, 5, 10, 10, 0, 'poly', '[0,0,10,0,5,10]')`,
		`PRAGMA user_version = 3`,
	}
}

func mustUserVersion(t *testing.T, s *Store) int {
	t.Helper()
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: unexpected error: %v", err)
	}
	return version
}

// ---- migrations ----

// TestOpenMigratesV1 builds a version-1 database by hand (global PRIMARY KEY
// on annotations.id, no v3 columns), fills it, then lets Open migrate it all
// the way to the current version. All rows must survive, the new columns must
// carry their defaults and duplicate annotation IDs across images must become
// insertable.
func TestOpenMigratesV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	buildDB(t, path, v1Stmts())

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open v1 db: %v", err)
	}
	defer func() { _ = s.Close() }()

	if got := mustUserVersion(t, s); got != SchemaVersion {
		t.Errorf("user_version = %d, want %d", got, SchemaVersion)
	}

	images, err := s.LoadImages()
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 || len(images[0].Anns) != 2 {
		t.Fatalf("migration lost data: %+v", images)
	}
	if images[0].Anns[0].ID != "ann_1" || images[0].Anns[0].Angle != 0.5 {
		t.Errorf("annotation row mangled: %+v", images[0].Anns[0])
	}
	if images[0].Status != "done" {
		t.Errorf("status lost: %q", images[0].Status)
	}
	// v2 -> v3 columns were added to the rebuilt table with their defaults.
	for _, ann := range images[0].Anns {
		if ann.Kind != KindRect {
			t.Errorf("migrated annotation %q kind = %q, want %q", ann.ID, ann.Kind, KindRect)
		}
		if ann.Points != nil {
			t.Errorf("migrated annotation %q points = %v, want nil", ann.ID, ann.Points)
		}
	}

	// The v1 categories table survived and got an empty (palette default) color.
	cats, err := s.Categories()
	if err != nil {
		t.Fatal(err)
	}
	wantCats := []Category{{Name: "缺陷"}, {Name: "划痕"}}
	if !reflect.DeepEqual(cats, wantCats) {
		t.Errorf("categories after migration = %v, want %v", cats, wantCats)
	}
	if err := s.SetCategories([]Category{{Name: "缺陷", Color: "#ff0000"}}); err != nil {
		t.Fatalf("SetCategories after migration: %v", err)
	}
	if got, err := s.Categories(); err != nil {
		t.Fatal(err)
	} else if want := []Category{{Name: "缺陷", Color: "#ff0000"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("categories with color after migration = %v, want %v", got, want)
	}

	// The v1 constraint is gone: duplicate IDs across images insert fine.
	if err := s.ReplaceAnns(images[1].ID, []AnnRec{{ID: "ann_1", Cat: 2, CX: 5, CY: 5, W: 1, H: 1}}); err != nil {
		t.Errorf("duplicate ann ID across images rejected after migration: %v", err)
	}
	// Foreign keys still cascade after the rebuild.
	if _, err := s.DeleteMissingImages([]string{"b.png"}); err != nil {
		t.Fatal(err)
	}
	var anns int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM annotations`).Scan(&anns); err != nil {
		t.Fatal(err)
	}
	if anns != 1 {
		t.Errorf("cascade after migration: %d annotations left, want 1", anns)
	}
}

// TestOpenMigratesV2ToV3 takes a hand-made version-2 database straight to v3:
// the three ALTER TABLE ADD COLUMN statements must preserve every row and give
// existing rows the documented defaults.
func TestOpenMigratesV2ToV3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	buildDB(t, path, v2Stmts())

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open v2 db: %v", err)
	}
	defer func() { _ = s.Close() }()

	if got := mustUserVersion(t, s); got != SchemaVersion {
		t.Errorf("user_version = %d, want %d", got, SchemaVersion)
	}

	images, err := s.LoadImages()
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 || len(images[0].Anns) != 2 || len(images[1].Anns) != 0 {
		t.Fatalf("migration lost data: %+v", images)
	}
	want := []AnnRec{
		{ID: "ann_1", Cat: 0, Kind: KindRect, CX: 1, CY: 1, W: 2, H: 2, Angle: 0.5},
		{ID: "ann_2", Cat: 1, Kind: KindRect, CX: 3, CY: 3, W: 2, H: 2},
	}
	if !reflect.DeepEqual(images[0].Anns, want) {
		t.Errorf("annotations after migration = %+v, want %+v", images[0].Anns, want)
	}

	// A row inserted without the new columns still gets their DEFAULT values,
	// i.e. the columns exist with the right defaults (not just nullable ones).
	if _, err := s.db.Exec(`INSERT INTO annotations (id, image_id, cat, cx, cy, w, h) VALUES ('ann_default', 2, 0, 4, 4, 1, 1)`); err != nil {
		t.Fatalf("insert annotation without kind/points: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO categories (idx, name) VALUES (2, '脏污')`); err != nil {
		t.Fatalf("insert category without color: %v", err)
	}
	images, err = s.LoadImages()
	if err != nil {
		t.Fatal(err)
	}
	b := images[1]
	if len(b.Anns) != 1 {
		t.Fatalf("annotations of %q = %+v, want 1", b.RelPath, b.Anns)
	}
	if b.Anns[0].Kind != KindRect || b.Anns[0].Points != nil {
		t.Errorf("defaults of inserted annotation = kind %q points %v, want %q/nil", b.Anns[0].Kind, b.Anns[0].Points, KindRect)
	}
	cats, err := s.Categories()
	if err != nil {
		t.Fatal(err)
	}
	wantCats := []Category{{Name: "缺陷"}, {Name: "划痕"}, {Name: "脏污"}}
	if !reflect.DeepEqual(cats, wantCats) {
		t.Errorf("categories after migration = %v, want %v (empty color default)", cats, wantCats)
	}

	// Polygons work straight away on a migrated database.
	poly := AnnRec{ID: "p1", Cat: 1, Kind: KindPoly, CX: 5, CY: 5, W: 10, H: 10, Points: []float64{0, 0, 10, 0, 5, 10}}
	if err := s.ReplaceAnns(images[1].ID, []AnnRec{poly}); err != nil {
		t.Fatalf("ReplaceAnns with a poly after migration: %v", err)
	}
	got, err := s.LoadImages()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got[1].Anns, []AnnRec{poly}) {
		t.Errorf("poly after migration = %+v, want %+v", got[1].Anns, poly)
	}
}

// TestOpenMigratesV3ToV4 takes a hand-made version-3 database to v4: the one
// ALTER TABLE adds annotations.attrs with its empty default, every row survives
// untouched, and attributes work straight away.
func TestOpenMigratesV3ToV4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3.db")
	buildDB(t, path, v3Stmts())

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open v3 db: %v", err)
	}
	defer func() { _ = s.Close() }()

	if got := mustUserVersion(t, s); got != 4 {
		t.Errorf("user_version = %d, want 4", got)
	}

	images, err := s.LoadImages()
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || len(images[0].Anns) != 2 {
		t.Fatalf("migration lost data: %+v", images)
	}
	// The v3 rows come back exactly as they were, with no attributes.
	want := []AnnRec{
		{ID: "rect_1", Cat: 0, Kind: KindRect, CX: 1, CY: 1, W: 2, H: 2, Angle: 0.5},
		{ID: "poly_1", Cat: 1, Kind: KindPoly, CX: 5, CY: 5, W: 10, H: 10, Points: []float64{0, 0, 10, 0, 5, 10}},
	}
	if !reflect.DeepEqual(images[0].Anns, want) {
		t.Errorf("annotations after migration = %+v, want %+v", images[0].Anns, want)
	}

	// A row inserted without attrs gets the column's default, so the column
	// exists with the documented default rather than being nullable.
	if _, err := s.db.Exec(`INSERT INTO annotations (id, image_id, cat, cx, cy, w, h) VALUES ('ann_default', 1, 0, 4, 4, 1, 1)`); err != nil {
		t.Fatalf("insert annotation without attrs: %v", err)
	}
	// Attributes round-trip on the migrated database.
	withAttrs := AnnRec{ID: "ann_attrs", Cat: 1, Kind: KindRect, CX: 5, CY: 5, W: 2, H: 2, Attrs: map[string]string{"difficult": "1", "备注": "划痕"}}
	if err := s.ReplaceAnns(images[0].ID, []AnnRec{withAttrs}); err != nil {
		t.Fatalf("ReplaceAnns with attrs after migration: %v", err)
	}
	got, err := s.LoadImages()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got[0].Anns, []AnnRec{withAttrs}) {
		t.Errorf("attrs after migration = %+v, want %+v", got[0].Anns, withAttrs)
	}
}

// TestReplaceAnnsAllowsDuplicateIDs pins the v2 semantics: ann IDs are only
// meaningful within their image and are never a uniqueness constraint.
func TestReplaceAnnsAllowsDuplicateIDs(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "dup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	if err := s.UpsertImages([]ImageRec{{RelPath: "a.png", Name: "a.png", Width: 8, Height: 6, SHA256: "aa"}}); err != nil {
		t.Fatal(err)
	}
	images, err := s.LoadImages()
	if err != nil {
		t.Fatal(err)
	}
	err = s.ReplaceAnns(images[0].ID, []AnnRec{
		{ID: "ann_1", Cat: 0, CX: 1, CY: 1, W: 2, H: 2},
		{ID: "ann_1", Cat: 1, CX: 3, CY: 3, W: 2, H: 2},
	})
	if err != nil {
		t.Fatalf("duplicate IDs within an image rejected: %v", err)
	}
	got, err := s.LoadImages()
	if err != nil {
		t.Fatal(err)
	}
	if len(got[0].Anns) != 2 {
		t.Errorf("anns = %d, want 2", len(got[0].Anns))
	}
}

// TestOpenOnMigratedV3IsNoop reopens a migrated database: the ALTER statements
// must not run twice (they are guarded by PRAGMA table_info) and data must
// survive.
func TestOpenOnMigratedV3IsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	buildDB(t, path, v2Stmts())

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatalf("reopen migrated v3 db: %v", err)
	}
	defer func() { _ = second.Close() }()

	if got := mustUserVersion(t, second); got != SchemaVersion {
		t.Errorf("user_version after reopen = %d, want %d", got, SchemaVersion)
	}
	images, err := second.LoadImages()
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 || len(images[0].Anns) != 2 || images[0].Anns[0].Kind != KindRect {
		t.Fatalf("reopen disturbed migrated data: %+v", images)
	}
}
