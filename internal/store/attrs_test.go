package store

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The v4 annotation attributes (docs/phase3-design.md §5.1): a JSON object per
// annotation row, stored as the empty string when there is none.

// attrsStore returns an empty store on a fresh database with one image, ready
// for ReplaceAnns.
func attrsStore(t *testing.T) (*Store, int64) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "attrs.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.UpsertImages([]ImageRec{{RelPath: "a.png", Name: "a.png", Width: 8, Height: 6, SHA256: "aa"}}); err != nil {
		t.Fatalf("UpsertImages: %v", err)
	}
	images, err := s.LoadImages()
	if err != nil || len(images) != 1 {
		t.Fatalf("LoadImages = %+v, %v", images, err)
	}
	return s, images[0].ID
}

// TestAnnsAttrsRoundTrip writes annotations with no attributes, an empty map,
// several keys and non-ASCII keys and values, and reads them back unchanged.
func TestAnnsAttrsRoundTrip(t *testing.T) {
	s, imageID := attrsStore(t)

	want := []AnnRec{
		{ID: "none", Cat: 0, CX: 1, CY: 1, W: 2, H: 2},
		{ID: "empty", Cat: 0, CX: 2, CY: 2, W: 2, H: 2, Attrs: map[string]string{}},
		{ID: "multi", Cat: 1, CX: 3, CY: 3, W: 2, H: 2, Attrs: map[string]string{"difficult": "1", "truncated": "0", "occluded": "yes"}},
		{ID: "cjk", Cat: 2, CX: 4, CY: 4, W: 2, H: 2, Attrs: map[string]string{"备注": "划痕，浅", "空值": ""}},
	}
	if err := s.ReplaceAnns(imageID, want); err != nil {
		t.Fatalf("ReplaceAnns: %v", err)
	}
	images, err := s.LoadImages()
	if err != nil {
		t.Fatalf("LoadImages: %v", err)
	}
	if len(images) != 1 || len(images[0].Anns) != len(want) {
		t.Fatalf("load back = %+v, want %d annotations", images, len(want))
	}
	// An empty map is stored as "no attributes" and loads back as nil, so
	// callers can test Attrs == nil for "none".
	expect := []AnnRec{
		{ID: "none", Kind: KindRect, Cat: 0, CX: 1, CY: 1, W: 2, H: 2},
		{ID: "empty", Kind: KindRect, Cat: 0, CX: 2, CY: 2, W: 2, H: 2},
		{ID: "multi", Kind: KindRect, Cat: 1, CX: 3, CY: 3, W: 2, H: 2, Attrs: want[2].Attrs},
		{ID: "cjk", Kind: KindRect, Cat: 2, CX: 4, CY: 4, W: 2, H: 2, Attrs: want[3].Attrs},
	}
	if !reflect.DeepEqual(images[0].Anns, expect) {
		t.Errorf("annotations after round trip = %+v, want %+v", images[0].Anns, expect)
	}

	// The row really holds a JSON object, not a quoted string or lossy pass
	// through the driver.
	var raw string
	if err := s.db.QueryRow(`SELECT attrs FROM annotations WHERE id = 'cjk'`).Scan(&raw); err != nil {
		t.Fatalf("read raw attrs: %v", err)
	}
	if !strings.HasPrefix(raw, "{") || !strings.Contains(raw, `"备注"`) {
		t.Errorf("stored attrs = %q, want a JSON object holding the key", raw)
	}
	var emptyRaw string
	if err := s.db.QueryRow(`SELECT attrs FROM annotations WHERE id = 'empty'`).Scan(&emptyRaw); err != nil {
		t.Fatalf("read raw attrs of the empty map: %v", err)
	}
	if emptyRaw != "" {
		t.Errorf("stored attrs of an empty map = %q, want the empty string", emptyRaw)
	}
}

// TestAnnsAttrsOverwrite checks that replacing an image's annotations drops the
// old attributes with the old rows.
func TestAnnsAttrsOverwrite(t *testing.T) {
	s, imageID := attrsStore(t)
	if err := s.ReplaceAnns(imageID, []AnnRec{{ID: "a", CX: 1, CY: 1, W: 2, H: 2, Attrs: map[string]string{"k": "v"}}}); err != nil {
		t.Fatalf("first ReplaceAnns: %v", err)
	}
	if err := s.ReplaceAnns(imageID, []AnnRec{{ID: "a", CX: 1, CY: 1, W: 2, H: 2}}); err != nil {
		t.Fatalf("second ReplaceAnns: %v", err)
	}
	images, err := s.LoadImages()
	if err != nil {
		t.Fatalf("LoadImages: %v", err)
	}
	if got := images[0].Anns; len(got) != 1 || got[0].Attrs != nil {
		t.Errorf("annotations after overwrite = %+v, want one with no attributes", got)
	}
}

// TestLoadImagesRejectsInvalidAttrs: a row whose attrs column is not a JSON
// object is an error naming the annotation and the image, not a silent zero.
func TestLoadImagesRejectsInvalidAttrs(t *testing.T) {
	for _, bad := range []string{`{oops`, `[1,2,3]`, `"just a string"`} {
		s, imageID := attrsStore(t)
		if err := s.ReplaceAnns(imageID, []AnnRec{{ID: "bad", CX: 1, CY: 1, W: 2, H: 2}}); err != nil {
			t.Fatalf("ReplaceAnns: %v", err)
		}
		if _, err := s.db.Exec(`UPDATE annotations SET attrs = ? WHERE id = 'bad'`, bad); err != nil {
			t.Fatalf("write bad attrs: %v", err)
		}
		_, err := s.LoadImages()
		if err == nil {
			t.Fatalf("LoadImages with attrs %q: expected an error", bad)
		}
		for _, want := range []string{"bad", "a.png", "attributes"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("LoadImages with attrs %q: error %q does not mention %q", bad, err, want)
			}
		}
	}
}

// TestEncodeAttrs pins the encoding contract: nothing for nil/empty, a JSON
// object otherwise, and the inverse mapping for the empty string.
func TestEncodeAttrs(t *testing.T) {
	if got, err := encodeAttrs(nil); err != nil || got != "" {
		t.Errorf("encodeAttrs(nil) = %q, %v; want \"\", nil", got, err)
	}
	if got, err := encodeAttrs(map[string]string{}); err != nil || got != "" {
		t.Errorf("encodeAttrs(empty) = %q, %v; want \"\", nil", got, err)
	}
	got, err := encodeAttrs(map[string]string{"k": "v"})
	if err != nil || got != `{"k":"v"}` {
		t.Errorf(`encodeAttrs({"k":"v"}) = %q, %v; want {"k":"v"}, nil`, got, err)
	}
	for _, tc := range []struct {
		raw  string
		want map[string]string
	}{
		{"", nil},
		{`{}`, nil},
		{`{"a":"b"}`, map[string]string{"a": "b"}},
	} {
		got, err := decodeAttrs(tc.raw)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("decodeAttrs(%q) = %v, %v; want %v, nil", tc.raw, got, err, tc.want)
		}
	}
}
