package store

import (
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ---- helpers ----

// newTestStore opens a store on a fresh database inside t.TempDir and
// registers its Close with the test cleanup.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mylabel.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): unexpected error: %v", path, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustUpsertImages(t *testing.T, s *Store, recs []ImageRec) {
	t.Helper()
	if err := s.UpsertImages(recs); err != nil {
		t.Fatalf("UpsertImages(%v): unexpected error: %v", recs, err)
	}
}

func mustLoadImages(t *testing.T, s *Store) []ImageRec {
	t.Helper()
	images, err := s.LoadImages()
	if err != nil {
		t.Fatalf("LoadImages: unexpected error: %v", err)
	}
	return images
}

// mustImageIDs maps rel_path -> row id via LoadImages (also asserting the ids
// are filled in).
func mustImageIDs(t *testing.T, s *Store) map[string]int64 {
	t.Helper()
	ids := make(map[string]int64)
	for _, im := range mustLoadImages(t, s) {
		if im.ID == 0 {
			t.Fatalf("LoadImages: image %q has no id", im.RelPath)
		}
		ids[im.RelPath] = im.ID
	}
	return ids
}

func mustImageByRel(t *testing.T, s *Store, rel string) ImageRec {
	t.Helper()
	for _, im := range mustLoadImages(t, s) {
		if im.RelPath == rel {
			return im
		}
	}
	t.Fatalf("LoadImages: image %q not found", rel)
	return ImageRec{}
}

func mustReplaceAnns(t *testing.T, s *Store, imageID int64, anns []AnnRec) {
	t.Helper()
	if err := s.ReplaceAnns(imageID, anns); err != nil {
		t.Fatalf("ReplaceAnns(%d, %v): unexpected error: %v", imageID, anns, err)
	}
}

func tableCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: unexpected error: %v", table, err)
	}
	return n
}

func relPaths(images []ImageRec) []string {
	out := make([]string, len(images))
	for i, im := range images {
		out[i] = im.RelPath
	}
	return out
}

func annIDs(anns []AnnRec) []string {
	out := make([]string, len(anns))
	for i, a := range anns {
		out[i] = a.ID
	}
	return out
}

// asLoaded returns a copy of anns normalized the way LoadImages reports rows
// (empty Kind -> KindRect, empty Points -> nil), for DeepEqual comparisons.
func asLoaded(anns []AnnRec) []AnnRec {
	if len(anns) == 0 {
		return nil
	}
	out := make([]AnnRec, len(anns))
	for i, a := range anns {
		a.Kind = normalizeKind(a.Kind)
		if len(a.Points) == 0 {
			a.Points = nil
		}
		out[i] = a
	}
	return out
}

// ---- Open / Close ----

func TestOpenIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mylabel.db")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: unexpected error: %v", err)
	}
	defer func() { _ = first.Close() }()

	var version int
	if err := first.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: unexpected error: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("user_version = %d, want %d", version, SchemaVersion)
	}

	var journal string
	if err := first.db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatalf("read journal_mode: unexpected error: %v", err)
	}
	if !strings.EqualFold(journal, "wal") {
		t.Errorf("journal_mode = %q, want wal", journal)
	}

	var fk, busy int
	if err := first.db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("read foreign_keys: unexpected error: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
	if err := first.db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatalf("read busy_timeout: unexpected error: %v", err)
	}
	if busy != busyTimeoutMS {
		t.Errorf("busy_timeout = %d, want %d", busy, busyTimeoutMS)
	}

	for _, obj := range []string{"categories", "images", "annotations", "idx_anns_image"} {
		var name string
		err := first.db.QueryRow(`SELECT name FROM sqlite_master WHERE name = ?`, obj).Scan(&name)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			t.Errorf("schema object %q missing after Open", obj)
		case err != nil:
			t.Fatalf("look up schema object %q: unexpected error: %v", obj, err)
		}
	}

	// Opening the same file again (while the first handle is still open) must
	// not fail and must not disturb the schema.
	second, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: unexpected error: %v", err)
	}
	cats := []Category{{Name: "缺陷", Color: "#ff0000"}, {Name: "划痕"}}
	if err := first.SetCategories(cats); err != nil {
		t.Fatalf("SetCategories: unexpected error: %v", err)
	}
	got, err := second.Categories()
	if err != nil {
		t.Fatalf("Categories on second handle: unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, cats) {
		t.Errorf("Categories seen by second handle = %v, want %v", got, cats)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close on second handle: unexpected error: %v", err)
	}

	// And once more after both handles are gone: data and version persist.
	if err := first.Close(); err != nil {
		t.Fatalf("Close on first handle: unexpected error: %v", err)
	}
	third, err := Open(path)
	if err != nil {
		t.Fatalf("third Open: unexpected error: %v", err)
	}
	defer func() { _ = third.Close() }()
	if got, err := third.Categories(); err != nil {
		t.Fatalf("Categories on third handle: unexpected error: %v", err)
	} else if !reflect.DeepEqual(got, cats) {
		t.Errorf("Categories after reopen = %v, want %v", got, cats)
	}
	if err := third.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version after reopen: unexpected error: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("user_version after reopen = %d, want %d", version, SchemaVersion)
	}
}

func TestOpenErrors(t *testing.T) {
	if s, err := Open(""); err == nil {
		_ = s.Close()
		t.Error(`Open("") = nil error, want error`)
	}

	missing := filepath.Join(t.TempDir(), "absent-dir", "mylabel.db")
	if s, err := Open(missing); err == nil {
		_ = s.Close()
		t.Errorf("Open(%q) in a missing directory = nil error, want error", missing)
	}
}

func TestCloseIdempotent(t *testing.T) {
	var nilStore *Store
	if err := nilStore.Close(); err != nil {
		t.Errorf("(*Store)(nil).Close() = %v, want nil", err)
	}
	if err := (&Store{}).Close(); err != nil {
		t.Errorf("zero Store Close() = %v, want nil", err)
	}

	s := newTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("first Close() = %v, want nil", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close() = %v, want nil", err)
	}

	closed := []struct {
		name string
		call func() error
	}{
		{"Categories", func() error { _, err := s.Categories(); return err }},
		{"SetCategories", func() error { return s.SetCategories([]Category{{Name: "a"}}) }},
		{"UpsertImages", func() error { return s.UpsertImages([]ImageRec{{RelPath: "a.jpg"}}) }},
		{"DeleteMissingImages", func() error { _, err := s.DeleteMissingImages(nil); return err }},
		{"LoadImages", func() error { _, err := s.LoadImages(); return err }},
		{"ReplaceAnns", func() error { return s.ReplaceAnns(1, nil) }},
		{"SetImageStatus", func() error { return s.SetImageStatus(1, StatusDone) }},
	}
	for _, tc := range closed {
		if err := tc.call(); err == nil {
			t.Errorf("%s on a closed Store = nil error, want error", tc.name)
		}
	}
}

// ---- categories ----

func TestCategories(t *testing.T) {
	s := newTestStore(t)

	cases := []struct {
		name string
		set  []Category
		want []Category
	}{
		{"fresh database is empty", nil, []Category{}},
		{"roundtrip colors", []Category{
			{Name: "缺陷", Color: "#ff0000"},
			{Name: "划痕", Color: "#00ff00"},
			{Name: "脏污", Color: "#0000ff"},
		}, []Category{
			{Name: "缺陷", Color: "#ff0000"},
			{Name: "划痕", Color: "#00ff00"},
			{Name: "脏污", Color: "#0000ff"},
		}},
		{"empty color is the palette default", []Category{
			{Name: "person"},
			{Name: "car", Color: ""},
		}, []Category{
			{Name: "person", Color: ""},
			{Name: "car", Color: ""},
		}},
		{"overwrite shorter", []Category{{Name: "person", Color: "#123456"}}, []Category{{Name: "person", Color: "#123456"}}},
		{"overwrite empty", []Category{}, []Category{}},
		{"overwrite again", []Category{{Name: "a"}, {Name: "b", Color: "#abcdef"}}, []Category{{Name: "a"}, {Name: "b", Color: "#abcdef"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.SetCategories(tc.set); err != nil {
				t.Fatalf("SetCategories(%v): unexpected error: %v", tc.set, err)
			}
			got, err := s.Categories()
			if err != nil {
				t.Fatalf("Categories: unexpected error: %v", err)
			}
			if got == nil {
				t.Fatalf("Categories = nil, want empty slice (contract)")
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Categories = %v, want %v", got, tc.want)
			}
			if n := tableCount(t, s, "categories"); n != len(tc.want) {
				t.Errorf("categories row count = %d, want %d", n, len(tc.want))
			}
		})
	}
}

// TestCategoriesColorColumn pins the v3 column itself: a row inserted without
// a color (raw SQL) reads back as "" and the column is NOT NULL DEFAULT ”.
func TestCategoriesColorColumn(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO categories (idx, name) VALUES (0, 'raw')`); err != nil {
		t.Fatalf("insert category without color: unexpected error: %v", err)
	}
	got, err := s.Categories()
	if err != nil {
		t.Fatalf("Categories: unexpected error: %v", err)
	}
	want := []Category{{Name: "raw", Color: ""}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Categories = %v, want %v (missing color defaults to empty)", got, want)
	}
	if _, err := s.db.Exec(`INSERT INTO categories (idx, name, color) VALUES (1, 'nocolor', NULL)`); err == nil {
		t.Error("inserting NULL color = nil error, want NOT NULL constraint violation")
	}
}

// ---- images: upsert ----

func TestUpsertImages(t *testing.T) {
	s := newTestStore(t)

	if err := s.UpsertImages(nil); err != nil {
		t.Fatalf("UpsertImages(nil) = %v, want nil", err)
	}
	if n := tableCount(t, s, "images"); n != 0 {
		t.Fatalf("images after empty upsert = %d, want 0", n)
	}

	mustUpsertImages(t, s, []ImageRec{
		{RelPath: "a/one.jpg", Name: "one.jpg", Width: 640, Height: 480, SHA256: "aaa"},
		{RelPath: "b/two.png", Name: "two.png", Width: 800, Height: 600, SHA256: "bbb", Status: StatusDone},
	})

	ids := mustImageIDs(t, s)
	one := mustImageByRel(t, s, "a/one.jpg")
	if one.Status != StatusNew {
		t.Errorf("status of image inserted with empty status = %q, want %q", one.Status, StatusNew)
	}
	if one.Name != "one.jpg" || one.Width != 640 || one.Height != 480 || one.SHA256 != "aaa" {
		t.Errorf("first insert stored %+v, want name/width/height/sha256 from input", one)
	}
	if two := mustImageByRel(t, s, "b/two.png"); two.Status != StatusDone {
		t.Errorf("status of image inserted with explicit status = %q, want %q", two.Status, StatusDone)
	}

	// Give the first image annotations and a non-default status, so the second
	// upsert can prove it preserves both.
	mustReplaceAnns(t, s, one.ID, []AnnRec{{ID: "ann_1", Cat: 1, CX: 10, CY: 20, W: 30, H: 40}})
	if err := s.SetImageStatus(one.ID, StatusLabeling); err != nil {
		t.Fatalf("SetImageStatus: unexpected error: %v", err)
	}

	// Re-upsert the same rel paths with new geometry plus one new image. The
	// Status on input must be ignored for rows that already exist.
	mustUpsertImages(t, s, []ImageRec{
		{RelPath: "a/one.jpg", Name: "one-renamed.jpg", Width: 1024, Height: 768, SHA256: "ccc", Status: StatusSkipped},
		{RelPath: "b/two.png", Name: "two.png", Width: 800, Height: 600, SHA256: "bbb"},
		{RelPath: "c/three.webp", Name: "three.webp", Width: 10, Height: 20, SHA256: "ddd"},
	})

	images := mustLoadImages(t, s)
	if got, want := relPaths(images), []string{"a/one.jpg", "b/two.png", "c/three.webp"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rel paths after upsert = %v, want %v (upsert must not insert duplicates)", got, want)
	}

	one = mustImageByRel(t, s, "a/one.jpg")
	if one.ID != ids["a/one.jpg"] {
		t.Errorf("row id of updated image = %d, want %d (same row, matched by rel_path)", one.ID, ids["a/one.jpg"])
	}
	if one.Name != "one-renamed.jpg" || one.Width != 1024 || one.Height != 768 || one.SHA256 != "ccc" {
		t.Errorf("updated image = %+v, want name/width/height/sha256 refreshed", one)
	}
	if one.Status != StatusLabeling {
		t.Errorf("status after upsert = %q, want %q (existing status must be preserved)", one.Status, StatusLabeling)
	}
	if len(one.Anns) != 1 || one.Anns[0].ID != "ann_1" {
		t.Errorf("annotations after upsert = %v, want the existing [ann_1] preserved", annIDs(one.Anns))
	}

	two := mustImageByRel(t, s, "b/two.png")
	if two.Status != StatusDone {
		t.Errorf("status after upsert with empty status = %q, want %q (preserved)", two.Status, StatusDone)
	}
	if three := mustImageByRel(t, s, "c/three.webp"); three.Status != StatusNew {
		t.Errorf("status of newly upserted image = %q, want %q", three.Status, StatusNew)
	}
}

// ---- images: delete missing ----

func TestDeleteMissingImages(t *testing.T) {
	all := []string{"a.jpg", "b/two.png", "c/three.webp"}

	cases := []struct {
		name          string
		keep          []string
		wantDeleted   int
		wantRemaining []string
	}{
		{"keep everything", all, 0, all},
		{"keep subset", []string{"a.jpg", "c/three.webp"}, 1, []string{"a.jpg", "c/three.webp"}},
		{"keep single", []string{"b/two.png"}, 2, []string{"b/two.png"}},
		{"keep empty deletes all", []string{}, 3, nil},
		{"keep only unknown rel paths deletes all", []string{"zzz.jpg"}, 3, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)

			recs := make([]ImageRec, len(all))
			for i, rel := range all {
				recs[i] = ImageRec{RelPath: rel, Name: filepath.Base(rel), Width: 100 + i, Height: 200 + i, SHA256: "hash"}
			}
			mustUpsertImages(t, s, recs)
			ids := mustImageIDs(t, s)
			for _, rel := range all {
				mustReplaceAnns(t, s, ids[rel], []AnnRec{
					{ID: "ann1_" + rel, Cat: 0, CX: 1, CY: 2, W: 3, H: 4},
					{ID: "ann2_" + rel, Cat: 1, CX: 5, CY: 6, W: 7, H: 8},
				})
			}

			deleted, err := s.DeleteMissingImages(tc.keep)
			if err != nil {
				t.Fatalf("DeleteMissingImages(%v): unexpected error: %v", tc.keep, err)
			}
			if deleted != tc.wantDeleted {
				t.Errorf("DeleteMissingImages(%v) = %d deleted, want %d", tc.keep, deleted, tc.wantDeleted)
			}

			images := mustLoadImages(t, s)
			got := relPaths(images)
			if len(got) != len(tc.wantRemaining) {
				t.Fatalf("remaining images = %v, want %v", got, tc.wantRemaining)
			}
			for i := range got {
				if got[i] != tc.wantRemaining[i] {
					t.Fatalf("remaining images = %v, want %v", got, tc.wantRemaining)
				}
			}
			for _, im := range images {
				if len(im.Anns) != 2 {
					t.Errorf("image %q kept %d annotations, want 2", im.RelPath, len(im.Anns))
				}
			}
			// ON DELETE CASCADE: no orphan annotation rows may survive.
			if n, want := tableCount(t, s, "annotations"), 2*len(tc.wantRemaining); n != want {
				t.Errorf("annotation rows after cascade delete = %d, want %d", n, want)
			}
			if n, want := tableCount(t, s, "images"), len(tc.wantRemaining); n != want {
				t.Errorf("image rows after delete = %d, want %d", n, want)
			}
		})
	}
}

// ---- annotations ----

func TestReplaceAnns(t *testing.T) {
	s := newTestStore(t)
	mustUpsertImages(t, s, []ImageRec{
		{RelPath: "a.jpg", Name: "a.jpg", Width: 1, Height: 1, SHA256: "a"},
		{RelPath: "b.jpg", Name: "b.jpg", Width: 1, Height: 1, SHA256: "b"},
	})
	ids := mustImageIDs(t, s)
	aID, bID := ids["a.jpg"], ids["b.jpg"]

	// The other image's annotations must never be touched by ReplaceAnns(aID).
	mustReplaceAnns(t, s, bID, []AnnRec{{ID: "other_1", Cat: 3, CX: 7, CY: 8, W: 9, H: 10, Angle: 0.25}})

	cases := []struct {
		name string
		anns []AnnRec
	}{
		{"insert two", []AnnRec{
			{ID: "ann_1", Cat: 2, CX: 10, CY: 20, W: 30, H: 40, Angle: 0.5},
			{ID: "ann_2", Cat: 5, CX: -1.5, CY: 2.25, W: 3.125, H: 4.5, Angle: -1.25},
		}},
		{"replace with one", []AnnRec{
			{ID: "ann_9", Cat: 7, CX: 100, CY: 200, W: 300, H: 400, Angle: 3.14159265358979},
		}},
		{"replace with empty", nil},
		{"insert after empty", []AnnRec{
			{ID: "ann_a", Cat: 0, CX: 1e6, CY: 2.5e-3, W: 0, H: 0, Angle: 0},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mustReplaceAnns(t, s, aID, tc.anns)

			got := mustImageByRel(t, s, "a.jpg")
			want := asLoaded(tc.anns)
			if !reflect.DeepEqual(got.Anns, want) {
				t.Errorf("annotations = %+v, want %+v", got.Anns, want)
			}
			if other := mustImageByRel(t, s, "b.jpg"); len(other.Anns) != 1 || other.Anns[0].ID != "other_1" {
				t.Errorf("annotations of the other image = %v, want [other_1] untouched", annIDs(other.Anns))
			}
			if n, wantN := tableCount(t, s, "annotations"), len(tc.anns)+1; n != wantN {
				t.Errorf("annotation row count = %d, want %d", n, wantN)
			}
		})
	}

	t.Run("unknown image is rejected", func(t *testing.T) {
		err := s.ReplaceAnns(999999, []AnnRec{{ID: "ann_orphan"}})
		if err == nil {
			t.Fatal("ReplaceAnns with an unknown image id = nil error, want foreign key error")
		}
		if n := tableCount(t, s, "annotations"); n != 2 {
			t.Errorf("annotation row count after rejected insert = %d, want 2 (transaction rolled back)", n)
		}
	})
}

// ---- annotation kind / points ----

// TestAnnsKindPointsRoundTrip covers the v3 kind/points columns end to end,
// including empty points, polygons and the empty-Kind default.
func TestAnnsKindPointsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	mustUpsertImages(t, s, []ImageRec{{RelPath: "a.png", Name: "a.png", Width: 640, Height: 480, SHA256: "a"}})
	id := mustImageIDs(t, s)["a.png"]

	cases := []struct {
		name string
		anns []AnnRec
	}{
		{"rect keeps geometry", []AnnRec{
			{ID: "r1", Cat: 0, Kind: KindRect, CX: 10, CY: 20, W: 30, H: 40, Angle: 0.75},
		}},
		{"empty kind is rect", []AnnRec{
			{ID: "r2", Cat: 1, CX: 1, CY: 2, W: 3, H: 4},
		}},
		{"poly with flat points", []AnnRec{
			{ID: "p1", Cat: 2, Kind: KindPoly, CX: 25, CY: 30, W: 30, H: 40,
				Points: []float64{10, 10, 40, 10, 40, 50, 5.5, -2.25}},
		}},
		{"empty points round trip to nil", []AnnRec{
			{ID: "p2", Cat: 0, Kind: KindPoly, CX: 1, CY: 1, W: 2, H: 2, Points: []float64{}},
		}},
		{"mixed z order preserved", []AnnRec{
			{ID: "m1", Cat: 0, CX: 0, CY: 0, W: 1, H: 1},
			{ID: "m2", Cat: 1, Kind: KindPoly, CX: 1, CY: 1, W: 2, H: 2, Points: []float64{0, 0, 1, 0, 0, 1}},
			{ID: "m3", Cat: 2, Kind: KindPoly, CX: 2, CY: 2, W: 2, H: 2, Points: []float64{2, 2, 4, 2, 4, 4, 2, 4}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mustReplaceAnns(t, s, id, tc.anns)

			got := mustImageByRel(t, s, "a.png").Anns
			want := asLoaded(tc.anns)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("annotations = %+v, want %+v", got, want)
			}
		})
	}
}

// TestReplaceAnnsStoresPointsAsJSON pins the on-disk representation: a JSON
// flat array for polygons and the empty string for "no points".
func TestReplaceAnnsStoresPointsAsJSON(t *testing.T) {
	s := newTestStore(t)
	mustUpsertImages(t, s, []ImageRec{{RelPath: "a.png", Name: "a.png", Width: 8, Height: 8, SHA256: "a"}})
	id := mustImageIDs(t, s)["a.png"]

	mustReplaceAnns(t, s, id, []AnnRec{
		{ID: "poly", Cat: 0, Kind: KindPoly, CX: 2, CY: 2, W: 4, H: 4, Points: []float64{10, 20, 30.5, -40, 0, 0}},
		{ID: "rect", Cat: 0, CX: 1, CY: 1, W: 1, H: 1},
	})

	cases := []struct {
		id, wantKind, wantPoints string
	}{
		{"poly", KindPoly, `[10,20,30.5,-40,0,0]`},
		{"rect", KindRect, ``},
	}
	for _, tc := range cases {
		var kind, points string
		if err := s.db.QueryRow(`SELECT kind, points FROM annotations WHERE id = ?`, tc.id).Scan(&kind, &points); err != nil {
			t.Fatalf("read raw row %q: %v", tc.id, err)
		}
		if kind != tc.wantKind {
			t.Errorf("stored kind of %q = %q, want %q", tc.id, kind, tc.wantKind)
		}
		if points != tc.wantPoints {
			t.Errorf("stored points of %q = %q, want %q", tc.id, points, tc.wantPoints)
		}
	}
}

// TestLoadImagesInvalidPoints makes sure a corrupt points value surfaces as an
// error carrying the image and annotation it belongs to, rather than being
// silently dropped.
func TestLoadImagesInvalidPoints(t *testing.T) {
	s := newTestStore(t)
	mustUpsertImages(t, s, []ImageRec{{RelPath: "dir/a.png", Name: "a.png", Width: 8, Height: 8, SHA256: "a"}})
	id := mustImageIDs(t, s)["dir/a.png"]
	mustReplaceAnns(t, s, id, []AnnRec{{ID: "bad", Cat: 0, Kind: KindPoly, CX: 1, CY: 1, W: 2, H: 2, Points: []float64{0, 0, 1, 0, 0, 1}}})

	if _, err := s.db.Exec(`UPDATE annotations SET points = '[1,2,' WHERE id = 'bad'`); err != nil {
		t.Fatalf("corrupt points: %v", err)
	}
	_, err := s.LoadImages()
	if err == nil {
		t.Fatal("LoadImages with corrupt points = nil error, want error")
	}
	for _, want := range []string{"bad", "dir/a.png", "points"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("LoadImages error %q does not mention %q", err, want)
		}
	}
}

// TestReplaceAnnsRejectsUnencodablePoints: points that cannot be serialized
// abort the whole replacement (nothing is half-written).
func TestReplaceAnnsRejectsUnencodablePoints(t *testing.T) {
	s := newTestStore(t)
	mustUpsertImages(t, s, []ImageRec{{RelPath: "a.png", Name: "a.png", Width: 8, Height: 8, SHA256: "a"}})
	id := mustImageIDs(t, s)["a.png"]

	keep := []AnnRec{{ID: "keep", Cat: 0, CX: 1, CY: 1, W: 2, H: 2}}
	mustReplaceAnns(t, s, id, keep)

	err := s.ReplaceAnns(id, []AnnRec{
		{ID: "ok", Cat: 0, CX: 1, CY: 1, W: 2, H: 2},
		{ID: "nan", Cat: 0, Kind: KindPoly, Points: []float64{0, 0, math.NaN(), 0, 0, 1}},
	})
	if err == nil {
		t.Fatal("ReplaceAnns with NaN points = nil error, want encode error")
	}
	if got, want := mustImageByRel(t, s, "a.png").Anns, asLoaded(keep); !reflect.DeepEqual(got, want) {
		t.Errorf("annotations after failed ReplaceAnns = %+v, want %+v (transaction rolled back)", got, want)
	}
}

// ---- load ----

func TestLoadImages(t *testing.T) {
	t.Run("empty database", func(t *testing.T) {
		s := newTestStore(t)
		if images := mustLoadImages(t, s); len(images) != 0 {
			t.Errorf("LoadImages on empty database = %v, want no rows", relPaths(images))
		}
	})

	t.Run("ordering and annotation backfill", func(t *testing.T) {
		s := newTestStore(t)
		// Inserted out of rel_path order on purpose.
		mustUpsertImages(t, s, []ImageRec{
			{RelPath: "c/z.jpg", Name: "z.jpg", Width: 3, Height: 30, SHA256: "c"},
			{RelPath: "a.jpg", Name: "a.jpg", Width: 1, Height: 10, SHA256: "a"},
			{RelPath: "b/m.jpg", Name: "m.jpg", Width: 2, Height: 20, SHA256: "b"},
		})
		ids := mustImageIDs(t, s)

		// Annotation ids are deliberately unsorted, so the assertion below
		// tests rowid (insertion) order rather than primary-key order.
		mustReplaceAnns(t, s, ids["b/m.jpg"], []AnnRec{{ID: "ann_b1"}, {ID: "ann_b2"}})
		mustReplaceAnns(t, s, ids["a.jpg"], []AnnRec{{ID: "ann_z"}, {ID: "ann_a"}, {ID: "ann_m"}})

		images := mustLoadImages(t, s)
		wantRel := []string{"a.jpg", "b/m.jpg", "c/z.jpg"}
		if got := relPaths(images); !reflect.DeepEqual(got, wantRel) {
			t.Fatalf("LoadImages order = %v, want %v", got, wantRel)
		}
		for i, wantName := range []string{"a.jpg", "m.jpg", "z.jpg"} {
			if images[i].Name != wantName {
				t.Errorf("images[%d].Name = %q, want %q", i, images[i].Name, wantName)
			}
			if images[i].Width != i+1 || images[i].Height != (i+1)*10 {
				t.Errorf("images[%d] size = %dx%d, want %dx%d", i, images[i].Width, images[i].Height, i+1, (i+1)*10)
			}
		}
		wantAnns := [][]string{{"ann_z", "ann_a", "ann_m"}, {"ann_b1", "ann_b2"}, nil}
		for i, want := range wantAnns {
			got := annIDs(images[i].Anns)
			if len(got) != len(want) {
				t.Fatalf("images[%d] anns = %v, want %v", i, got, want)
			}
			for j := range want {
				if got[j] != want[j] {
					t.Fatalf("images[%d] anns = %v, want %v (rowid order)", i, got, want)
				}
			}
		}
		if len(images[2].Anns) != 0 {
			t.Errorf("image without annotations loaded %v, want none", annIDs(images[2].Anns))
		}
	})
}

// ---- status ----

func TestSetImageStatus(t *testing.T) {
	s := newTestStore(t)
	mustUpsertImages(t, s, []ImageRec{{RelPath: "a.jpg", Name: "a.jpg", Width: 1, Height: 1, SHA256: "a"}})
	id := mustImageIDs(t, s)["a.jpg"]

	cases := []struct {
		name   string
		status string
		want   string
	}{
		{"labeling", StatusLabeling, StatusLabeling},
		{"done", StatusDone, StatusDone},
		{"reviewed", StatusReviewed, StatusReviewed},
		{"skipped", StatusSkipped, StatusSkipped},
		{"empty means new", "", StatusNew},
		{"back to new", StatusNew, StatusNew},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.SetImageStatus(id, tc.status); err != nil {
				t.Fatalf("SetImageStatus(%d, %q): unexpected error: %v", id, tc.status, err)
			}
			if got := mustImageByRel(t, s, "a.jpg").Status; got != tc.want {
				t.Errorf("status = %q, want %q", got, tc.want)
			}
		})
	}
}
