package app

import (
	"path/filepath"

	"mylabel/internal/exporter"
	"mylabel/internal/ir"
)

// App → IR assembly for the export wizard (docs/phase4-design.md §9). The
// exporter never sees the app model: it takes IR, so the session's in-memory
// state is converted here — the annotations on screen, not the ones in the
// store, which is why an export needs no save first.

// inferExportTask guesses the YOLO task from what the session holds: any
// polygon means segmentation, otherwise any rotated box means obb, otherwise
// detection. The wizard seeds its task choice with it and the user can change it.
func (a *App) inferExportTask() exporter.Task {
	task := exporter.TaskDet
	for i := range a.images {
		for j := range a.images[i].Anns {
			an := &a.images[i].Anns[j]
			if isPoly(an) {
				return exporter.TaskSeg
			}
			if an.Angle != 0 {
				task = exporter.TaskObb
			}
		}
	}
	return task
}

// exportProjectIR assembles the IR the exporter works on from the open folder:
// the category table, and one ir.Image per image with its relative path, size,
// content hash and annotations. File is the slash-separated folder-relative
// path (the same spelling ImageInfo.RelPath uses), which is also the copy
// source and the split hint.
//
// The project's Task is "mixed": the session holds boxes, polygons and rotated
// boxes side by side, and the target format's task is the wizard's choice
// (exporter.Options.Task), not a property of the source.
func (a *App) exportProjectIR() (*ir.Project, []ir.Image) {
	keys := categoryKeys(a.categories)
	proj := &ir.Project{
		IRVersion:  ir.Version,
		Name:       filepath.Base(a.dir),
		Task:       ir.TaskMixed,
		Categories: appCategoriesToIR(a.categories),
	}
	images := make([]ir.Image, 0, len(a.images))
	for i := range a.images {
		info := &a.images[i]
		images = append(images, ir.Image{
			File:        cleanImportPath(info.RelPath),
			Width:       info.Width,
			Height:      info.Height,
			SHA256:      info.SHA256,
			Annotations: annsToIR(info.Anns, keys),
		})
	}
	return proj, images
}

// exportAnnCount counts every annotation of the session, for the wizard's
// header line.
func (a *App) exportAnnCount() int {
	n := 0
	for i := range a.images {
		n += len(a.images[i].Anns)
	}
	return n
}
