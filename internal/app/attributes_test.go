package app

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mylabel/internal/importer"
	"mylabel/internal/ir"
)

// Annotation attributes (docs/phase3-design.md §5.1): the Ann field, its deep
// copies through undo, the store round trip, the exchange format and the
// properties dialog.

// attrsAnn is a rectangle with two attributes, one of them non-ASCII.
func attrsAnn(id string, cat int) Ann {
	return Ann{
		ID: id, Cat: cat, Kind: KindRect, CX: 10, CY: 20, W: 30, H: 40,
		Attributes: map[string]string{"difficult": "1", "备注": "划痕"},
	}
}

// TestCloneAnnAttributesDeepCopy pins the undo discipline: a copy owns its own
// attribute map, in both directions.
func TestCloneAnnAttributesDeepCopy(t *testing.T) {
	an := attrsAnn("ann_1", 0)
	c := cloneAnn(an)
	if !maps.Equal(c.Attributes, an.Attributes) {
		t.Fatalf("clone attributes = %v, want %v", c.Attributes, an.Attributes)
	}
	c.Attributes["difficult"] = "changed"
	c.Attributes["new"] = "x"
	if an.Attributes["difficult"] != "1" || len(an.Attributes) != 2 {
		t.Errorf("editing the clone changed the original: %v", an.Attributes)
	}
	an.Attributes["备注"] = "改过"
	if c.Attributes["备注"] != "划痕" {
		t.Errorf("editing the original changed the clone: %v", c.Attributes)
	}

	// A nil map stays nil: "no attributes" survives a copy as the empty state.
	if got := cloneAnn(Ann{ID: "x"}).Attributes; got != nil {
		t.Errorf("clone of an annotation without attributes has %v, want nil", got)
	}
	// An empty map is the same empty state.
	if got := cloneAttrs(map[string]string{}); got != nil {
		t.Errorf("cloneAttrs(empty) = %v, want nil", got)
	}
}

// TestSameAnnComparesAttributes: the drag and dialog commit paths decide whether
// anything changed with sameAnn, so an attribute-only edit must be visible.
func TestSameAnnComparesAttributes(t *testing.T) {
	a := attrsAnn("ann_1", 0)
	if !sameAnn(a, cloneAnn(a)) {
		t.Error("sameAnn says a copy of an annotation differs from it")
	}
	b := cloneAnn(a)
	b.Attributes["difficult"] = "0"
	if sameAnn(a, b) {
		t.Error("sameAnn ignores a changed attribute value")
	}
	c := cloneAnn(a)
	delete(c.Attributes, "备注")
	if sameAnn(a, c) {
		t.Error("sameAnn ignores a removed attribute")
	}
	d := cloneAnn(a)
	d.Attributes["extra"] = "1"
	if sameAnn(a, d) {
		t.Error("sameAnn ignores an added attribute")
	}
	// An empty map and no map are the same "no attributes" state: maps.Equal
	// compares by content, so the store's "" <-> nil round trip cannot make an
	// annotation look changed.
	bare := Ann{ID: "ann_2", Kind: KindRect, CX: 1, CY: 1, W: 2, H: 2}
	empty := bare
	empty.Attributes = map[string]string{}
	if !sameAnn(bare, empty) {
		t.Error("sameAnn says nil attributes differ from an empty map")
	}
}

// TestAnnAttributesUndoIsolation edits through the undo stack: the command's
// snapshots are copies, so a later edit of the live annotation cannot reach
// them, and a redo restores what the command saw rather than the live map.
func TestAnnAttributesUndoIsolation(t *testing.T) {
	a := newTestApp()
	a.images[0].Anns = []Ann{attrsAnn("ann_1", 0)}

	before := cloneAnn(a.images[0].Anns[0])
	after := cloneAnn(before)
	after.Attributes["difficult"] = "0"
	a.push(cmdModify(a, 0, 0, before, after))

	if got := a.images[0].Anns[0].Attributes["difficult"]; got != "0" {
		t.Fatalf("after commit difficult = %q, want 0", got)
	}
	a.undo()
	if got := a.images[0].Anns[0].Attributes["difficult"]; got != "1" {
		t.Fatalf("after undo difficult = %q, want 1", got)
	}
	a.redo()
	if got := a.images[0].Anns[0].Attributes["difficult"]; got != "0" {
		t.Fatalf("after redo difficult = %q, want 0", got)
	}

	// A live edit after the command was pushed must not leak into the undo
	// state: the command holds its own copies.
	a.images[0].Anns[0].Attributes["difficult"] = "leaked"
	a.undo()
	if got := a.images[0].Anns[0].Attributes["difficult"]; got != "1" {
		t.Errorf("undo after a live edit = %q, want the snapshot's 1", got)
	}
	a.redo()
	if got := a.images[0].Anns[0].Attributes["difficult"]; got != "0" {
		t.Errorf("redo after a live edit = %q, want the snapshot's 0", got)
	}
}

// TestAnnAttributesStoreRoundTrip saves attributes and reads them back on a
// fresh App, i.e. through the store's v4 attrs column.
func TestAnnAttributesStoreRoundTrip(t *testing.T) {
	a := storeApp(t, "a.png")
	anns := []Ann{
		attrsAnn("ann_1", 1),
		{ID: "ann_2", Cat: 2, Kind: KindPoly, Points: []Point{{0, 0}, {10, 0}, {5, 8}}},
	}
	syncPolyBounds(&anns[1])
	seedAnns(t, a, 0, anns)

	b := reopen(t, a.dir)
	got := b.images[0].Anns
	if len(got) != 2 {
		t.Fatalf("reopen 后 %d 条标注，期望 2: %+v", len(got), got)
	}
	if want := anns[0].Attributes; !maps.Equal(got[0].Attributes, want) {
		t.Errorf("重开后 attributes = %v，期望 %v", got[0].Attributes, want)
	}
	if got[1].Attributes != nil {
		t.Errorf("没有属性的标注重开后 attributes = %v，期望 nil", got[1].Attributes)
	}
}

// TestExportJSONWritesAttributes: the PoC exchange document carries the
// attributes too, which is backwards compatible — a reader that does not know
// the key ignores it.
func TestExportJSONWritesAttributes(t *testing.T) {
	a := storeApp(t, "a.png")
	seedAnns(t, a, 0, []Ann{attrsAnn("ann_1", 1)})
	if err := a.exportJSON(a.dir); err != nil {
		t.Fatalf("exportJSON: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(a.dir, docName))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Images []struct {
			Anns []struct {
				ID         string            `json:"id"`
				Attributes map[string]string `json:"attributes"`
			} `json:"anns"`
		} `json:"images"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("导出的 JSON 解析失败: %v\n%s", err, raw)
	}
	if len(doc.Images) != 1 || len(doc.Images[0].Anns) != 1 {
		t.Fatalf("导出的文档 = %s", raw)
	}
	if want := attrsAnn("ann_1", 1).Attributes; !maps.Equal(doc.Images[0].Anns[0].Attributes, want) {
		t.Errorf("导出的 attributes = %v，期望 %v", doc.Images[0].Anns[0].Attributes, want)
	}
	// An annotation without attributes writes no key at all (omitempty).
	if strings.Contains(string(raw), `"attributes": null`) {
		t.Errorf("导出的文档里有空的 attributes 键: %s", raw)
	}
}

// TestIRImportAttributesLandInStore is the end-to-end path of §5.1: an IR
// annotation's Attributes reach the store's attrs column through one import and
// one save, and come back on reopen.
func TestIRImportAttributesLandInStore(t *testing.T) {
	a := storeApp(t, "a.png")
	ds := &importer.Dataset{
		Categories: []ir.Category{{Key: "person", Name: "person"}},
		Images: []ir.Image{{
			File: "a.png", Width: 100, Height: 50,
			Annotations: []ir.Annotation{
				{ID: "1", Category: "person", Kind: ir.KindBBox, X: 1, Y: 2, W: 10, H: 20,
					Attributes: map[string]string{"difficult": "1", "备注": "遮挡"}},
				{ID: "2", Category: "person", Kind: ir.KindBBox, X: 30, Y: 2, W: 10, H: 20},
			},
		}},
	}
	res := a.importDataset(ds, a.defaultChoices(ds))
	if res.Annots != 2 || res.Attrs != 1 {
		t.Fatalf("导入摘要 = %+v，期望 2 条标注、1 条带属性", res)
	}
	if err := a.save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	b := reopen(t, a.dir)
	got := b.images[0].Anns
	if len(got) != 2 {
		t.Fatalf("重开后 %d 条标注，期望 2: %+v", len(got), got)
	}
	want := map[string]string{"difficult": "1", "备注": "遮挡"}
	if !maps.Equal(got[0].Attributes, want) {
		t.Errorf("落库后的 attributes = %v，期望 %v", got[0].Attributes, want)
	}
	if got[1].Attributes != nil {
		t.Errorf("没有属性的一条 = %v，期望 nil", got[1].Attributes)
	}
}

// TestAttrsFromRows pins the dialog's row -> map rule: keys are trimmed, rows
// with an empty key are dropped, the last value of a repeated key wins, and no
// rows at all means "no attributes".
func TestAttrsFromRows(t *testing.T) {
	rows := []propRow{
		{ID: 1, Key: " difficult ", Value: "1"},
		{ID: 2, Key: "", Value: "orphan"},
		{ID: 3, Key: "   ", Value: "blank key"},
		{ID: 4, Key: "备注", Value: "划痕"},
		{ID: 5, Key: "difficult", Value: "0"},
	}
	want := map[string]string{"difficult": "0", "备注": "划痕"}
	if got := attrsFromRows(rows); !reflect.DeepEqual(got, want) {
		t.Errorf("attrsFromRows = %v, want %v", got, want)
	}
	if got := attrsFromRows(nil); got != nil {
		t.Errorf("attrsFromRows(nil) = %v, want nil", got)
	}
	if got := attrsFromRows([]propRow{{ID: 1}}); got != nil {
		t.Errorf("attrsFromRows(one empty row) = %v, want nil", got)
	}
	// A value without a key is dropped with the row: an attribute needs a name.
	if got := attrsFromRows([]propRow{{ID: 1, Value: "v"}}); got != nil {
		t.Errorf("attrsFromRows(a value without a key) = %v, want nil", got)
	}
}

// TestRowsFromAttrsIsSorted: the dialog lists attributes alphabetically, so
// reopening it does not reshuffle the rows, and every row gets an id of its
// own (the widget keys are built from it).
func TestRowsFromAttrsIsSorted(t *testing.T) {
	a := newTestApp()
	rows := a.rowsFromAttrs(map[string]string{"z": "1", "a": "2", "m": "3"})
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want 3", rows)
	}
	keys := []string{rows[0].Key, rows[1].Key, rows[2].Key}
	if want := []string{"a", "m", "z"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("row keys = %v, want %v", keys, want)
	}
	seen := make(map[int]bool, 3)
	for _, r := range rows {
		if r.ID <= 0 || seen[r.ID] {
			t.Errorf("row id = %d in %+v: ids must be unique and positive", r.ID, rows)
		}
		seen[r.ID] = true
	}
	if got := a.rowsFromAttrs(nil); got != nil {
		t.Errorf("rowsFromAttrs(nil) = %+v, want nil", got)
	}
}
