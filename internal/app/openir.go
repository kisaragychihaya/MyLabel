package app

import (
	"os"
	"path/filepath"

	"mylabel/internal/store"
)

// Opening a folder that carries an IR project (docs/phase3-design.md §4.2
// "直接打开 IR"): when the working repository holds no annotations yet,
// <dir>/project.json is imported through the same conversion and apply
// pipeline the wizard uses, with every category created fresh. An IR project
// wins over the legacy mylabel.annotations.json when both are present.

// hasIRProject reports whether dir carries an IR project document.
func hasIRProject(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, irProjectName))
	return err == nil && !info.IsDir()
}

// repoHasAnnotations reports whether any stored image already carries
// annotations. Both one-time imports only ever run into an empty repository,
// so a session never overwrites what the user labelled.
func repoHasAnnotations(recs []store.ImageRec) bool {
	for _, rec := range recs {
		if len(rec.Anns) > 0 {
			return true
		}
	}
	return false
}

// importIRProject imports dir's IR project into the folder that was just
// opened: the dataset is normalized and validated, every category it names is
// created, and its annotations are applied as one undoable step. It returns a
// zero result when dir has no project.json.
func (a *App) importIRProject(dir string) (importResult, error) {
	if !hasIRProject(dir) {
		return importResult{}, nil
	}
	ds, _, err := readIRDataset(dir)
	if err != nil {
		return importResult{}, err
	}
	return a.importDataset(ds, a.defaultChoices(ds)), nil
}
