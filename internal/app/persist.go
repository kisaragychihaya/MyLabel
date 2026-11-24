package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"time"

	"mylabel/internal/store"
	"mylabel/locales"
)

// docName is the annotation document file name inside the image folder. Since
// phase 1 it is an exchange format only: the working data lives in dbName.
const docName = "mylabel.annotations.json"

// dbName is the SQLite working repository inside the image folder.
const dbName = "mylabel.db"

// autosaveDelay is the debounce delay for automatic saves after changes.
const autosaveDelay = 2e9 // 2s in nanoseconds

// save writes the annotations of the images recorded in dirtyImages to the
// store of the opened folder, plus the category list. No-op (nil error) when no
// folder is open.
//
// A failed image stays in dirtyImages, so a later save retries it and the
// document stays marked unsaved.
func (a *App) save() error {
	if a.saveTimer != nil {
		a.saveTimer.Stop()
		a.saveTimer = nil
	}
	st := a.store
	if st == nil {
		return nil
	}

	if err := st.SetCategories(a.categories); err != nil {
		a.lastStatus = locales.StatusSaveFailed(err.Error())
		return err
	}

	ids := make([]int64, 0, len(a.dirtyImages))
	for id := range a.dirtyImages {
		ids = append(ids, id)
	}
	slices.Sort(ids) // deterministic write order and status message

	byID := make(map[int64]*ImageInfo, len(a.images))
	for i := range a.images {
		if id := a.images[i].ID; id != 0 {
			byID[id] = &a.images[i]
		}
	}

	saved := 0
	for _, id := range ids {
		img, ok := byID[id]
		if !ok {
			// The row is gone: the folder was rescanned without that image.
			delete(a.dirtyImages, id)
			continue
		}
		if err := st.ReplaceAnns(id, annsToRecs(img.Anns)); err != nil {
			a.lastStatus = locales.StatusSaveFailed(err.Error())
			return err
		}
		delete(a.dirtyImages, id)
		saved++
	}

	a.dirty = false
	if saved == 0 {
		a.lastStatus = locales.StatusSavedNothing()
	} else {
		a.lastStatus = locales.StatusSavedImages(saved)
	}
	return nil
}

// scheduleSave debounces an automatic save after annotation changes.
func (a *App) scheduleSave() {
	if a.store == nil {
		return
	}
	if a.saveTimer != nil {
		a.saveTimer.Stop()
	}
	delay := time.Duration(autosaveDelay)
	a.saveTimer = time.AfterFunc(delay, func() {
		if a.win != nil {
			// The timer fires on its own goroutine: save on the main thread,
			// where the state it reads belongs (design rule 2).
			a.win.Update(func() { _ = a.save() })
			return
		}
		_ = a.save()
	})
}

// exportJSON writes the whole document to <dir>/mylabel.annotations.json in the
// PoC exchange format, atomically (tmp file + rename).
//
// On Windows os.Rename cannot replace an existing file, so the target is
// removed first: between the Remove and the Rename the file briefly does not
// exist. That is not strictly atomic; the phase-1 design accepts it for an
// export file (docs/phase1-design.md §5.3).
func (a *App) exportJSON(dir string) error {
	if dir == "" {
		return errors.New(locales.ErrorNoFolder())
	}
	// The PoC document format carries plain names only: the exchange file drops
	// the custom colors, which live in the working repository (phase2 §6).
	names := make([]string, len(a.categories))
	for i, cat := range a.categories {
		names[i] = cat.Name
	}
	doc := docFile{
		IRVersion:  docIRVersion,
		Categories: names,
		Images:     a.images,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}

	target := filepath.Join(dir, docName)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// loadDoc reads and parses the document in dir. Returns (nil, nil) when the
// file does not exist; an error for a corrupt document. Since phase 1 it
// serves the one-time legacy import and reads exported documents.
func loadDoc(dir string) (*docFile, error) {
	if dir == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, docName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var doc docFile
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s", locales.ErrorParseDoc(docName, err.Error()))
	}
	return &doc, nil
}

// migrateLegacyDoc imports the PoC JSON document of dir into st, but only when
// the repository holds no annotations at all: as soon as anything is annotated
// the database is the source of truth and the legacy file is left alone. The
// file is never deleted — it stays as a backup and can still be exported.
//
// A legacy entry names only a bare file name (the PoC's "file" key), so it
// matches the row whose RelPath equals that name or, failing that, the single
// row whose base name equals it. Ambiguous and vanished files are skipped.
// Returns how many images received annotations.
func migrateLegacyDoc(dir string, st *store.Store, recs []store.ImageRec) (int, error) {
	if st == nil || len(recs) == 0 {
		return 0, nil
	}
	for _, rec := range recs {
		if len(rec.Anns) > 0 {
			return 0, nil // the database is already in use
		}
	}
	doc, err := loadDoc(dir)
	if err != nil {
		return 0, err
	}
	if doc == nil {
		return 0, nil
	}

	byRel := make(map[string]int, len(recs))
	byBase := make(map[string][]int, len(recs))
	for i, rec := range recs {
		byRel[rec.RelPath] = i
		base := path.Base(rec.RelPath)
		byBase[base] = append(byBase[base], i)
	}

	matched := make(map[int][]store.AnnRec)
	for _, docImg := range doc.Images {
		if len(docImg.Anns) == 0 {
			continue
		}
		legacy := docImg.RelPath // the PoC "file" key held the bare file name
		if legacy == "" {
			legacy = docImg.Name
		}
		if legacy == "" {
			continue
		}
		idx, ok := byRel[legacy]
		if !ok {
			if candidates := byBase[path.Base(legacy)]; len(candidates) == 1 {
				idx, ok = candidates[0], true
			}
		}
		if !ok {
			continue
		}
		// PoC documents reuse ann IDs across sessions, so duplicates within
		// one image are expected; rename them instead of failing the import.
		ensureUniqueAnnIDs(docImg.Anns)
		matched[idx] = annsToRecs(docImg.Anns)
	}
	if len(matched) == 0 {
		return 0, nil
	}

	if len(doc.Categories) > 0 {
		if err := st.SetCategories(categoriesFromNames(doc.Categories)); err != nil {
			return 0, err
		}
	}
	indexes := make([]int, 0, len(matched))
	for idx := range matched {
		indexes = append(indexes, idx)
	}
	slices.Sort(indexes)
	// A broken image must not abort the whole import: migrate the rest and
	// report the failures together.
	var errs []error
	migrated := 0
	for _, idx := range indexes {
		if err := st.ReplaceAnns(recs[idx].ID, matched[idx]); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", recs[idx].RelPath, err))
			continue
		}
		migrated++
	}
	return migrated, errors.Join(errs...)
}
