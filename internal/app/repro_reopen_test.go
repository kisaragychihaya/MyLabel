package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestReopenKeepsAnnotations is the regression test for the phase-1 " reopen
// and the annotations are gone" bug: a PoC legacy JSON reuses ann IDs across
// sessions (duplicates within one image and across images), which the v1
// schema rejected with a UNIQUE violation, silently dropping the whole
// migration. The full user flow is exercised: open (legacy JSON present,
// fresh DB) -> annotate -> save -> close -> reopen.
func TestReopenKeepsAnnotations(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "a.png"), 8, 6)
	writePNG(t, filepath.Join(dir, "b.png"), 8, 6)

	// Legacy PoC document: ann_1 appears twice in a.png and again in b.png.
	legacy := docFile{
		IRVersion:  docIRVersion,
		Categories: []string{"x", "y"},
		Images: []ImageInfo{
			{RelPath: "a.png", Width: 8, Height: 6, Anns: []Ann{
				{ID: "ann_1", Cat: 0, CX: 1, CY: 1, W: 2, H: 2},
				{ID: "ann_1", Cat: 1, CX: 3, CY: 3, W: 2, H: 2},
			}},
			{RelPath: "b.png", Width: 8, Height: 6, Anns: []Ann{
				{ID: "ann_1", Cat: 1, CX: 5, CY: 5, W: 2, H: 2},
			}},
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, docName), data, 0o644); err != nil {
		t.Fatal(err)
	}

	a := New()
	a.openDir(dir)
	if len(a.images) != 2 {
		t.Fatalf("images = %d, want 2", len(a.images))
	}
	if got := len(a.images[0].Anns); got != 2 {
		t.Fatalf("a.png migrated anns = %d, want 2 (status %q)", got, a.lastStatus)
	}
	if got := len(a.images[1].Anns); got != 1 {
		t.Fatalf("b.png migrated anns = %d, want 1 (status %q)", got, a.lastStatus)
	}
	// Migrated IDs must be unique per image.
	seen := map[string]bool{}
	for _, ann := range a.images[0].Anns {
		if seen[ann.ID] {
			t.Fatalf("duplicate ann ID after migration: %s", ann.ID)
		}
		seen[ann.ID] = true
	}

	// The user adds an annotation. Its ID must not collide with the migrated
	// ones even though the process counter started at ann_1.
	a.images[0].Anns = append(a.images[0].Anns, Ann{ID: newAnnID(), Cat: 1, CX: 4, CY: 4, W: 1, H: 1})
	a.setDirty(0)
	if err := a.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	want := len(a.images[0].Anns)
	a.Close()

	b := New()
	b.openDir(dir)
	defer b.Close()
	if got := len(b.images[0].Anns); got != want {
		t.Errorf("annotations lost after reopen: want %d, got %d", want, got)
	}
	if got := len(b.images[1].Anns); got != 1 {
		t.Errorf("b.png anns after reopen = %d, want 1", got)
	}
}

// TestSeedAnnIDCounter checks that a fresh process never reissues an ID that
// a loaded document already uses.
func TestSeedAnnIDCounter(t *testing.T) {
	images := []ImageInfo{{Anns: []Ann{{ID: "ann_7"}, {ID: "junk"}, {ID: "ann_3"}}}}
	seedAnnIDCounter(images)
	for range 3 {
		if id := newAnnID(); id == "ann_7" || id == "ann_3" {
			t.Fatalf("newAnnID reissued loaded ID %s", id)
		}
	}
}

// TestEnsureUniqueAnnIDs renames empty and duplicate IDs, keeping order.
func TestEnsureUniqueAnnIDs(t *testing.T) {
	anns := []Ann{{ID: "k"}, {ID: "k"}, {ID: ""}, {ID: "k"}}
	ensureUniqueAnnIDs(anns)
	if anns[0].ID != "k" {
		t.Errorf("first occurrence renamed to %s, want it kept", anns[0].ID)
	}
	seen := map[string]bool{}
	for _, ann := range anns {
		if ann.ID == "" || seen[ann.ID] {
			t.Errorf("ID %q still empty or duplicated in %v", ann.ID, anns)
		}
		seen[ann.ID] = true
	}
}
