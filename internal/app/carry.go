package app

import "fmt"

// Carry-over of the previous image's annotations (Ctrl+B,
// docs/phase5-design.md §2).

// carryPrevImage appends deep copies of the previous image's annotations to
// the loaded one, as a single undo step. Categories are global indices, so
// they carry over verbatim; IDs are re-issued on collision with the live set.
// The carried batch keeps the source's relative order and lands above the
// existing annotations (the slice order is the z-order).
func (a *App) carryPrevImage() bool {
	if len(a.images) == 0 || a.loaded < 0 {
		return false
	}
	if a.loaded == 0 {
		a.lastStatus = "没有上一张图片"
		return false
	}
	// Same guard as Delete: a drag holds annotation snapshots by index.
	if a.dragInProgress() {
		return false
	}
	src := a.images[a.loaded-1].Anns
	if len(src) == 0 {
		a.lastStatus = "上一张没有标注可沿用"
		return false
	}
	img := a.curImage()
	if img == nil {
		return false
	}
	seen := make(map[string]bool, len(img.Anns)+len(src))
	for _, live := range img.Anns {
		seen[live.ID] = true
	}
	anns := make([]Ann, 0, len(src))
	for _, an := range src {
		copied := cloneAnn(an)
		if copied.ID == "" || seen[copied.ID] {
			copied.ID = newAnnID()
		}
		seen[copied.ID] = true
		anns = append(anns, copied)
	}
	base := len(img.Anns)
	a.push(cmdCarry(a, a.loaded, anns))
	// Select the carried batch so the user sees what arrived (§2).
	a.clearSelection()
	for i := range anns {
		a.selSet[base+i] = true
	}
	a.selPrimary = base + len(anns) - 1
	a.lastStatus = fmt.Sprintf("已从上一张沿用 %d 条标注", len(anns))
	return true
}

// cmdCarry returns the command that appends anns to images[img].Anns in one
// undo step. It mirrors cmdImport's per-image shape: Do appends deep copies
// and records the length it started from, Undo truncates back to it — the
// carried annotations are always the tail (the undo stack is LIFO and push
// clears the redo stack, so nothing appends behind them before they are
// undone or redone).
func cmdCarry(a *App, img int, anns []Ann) Command {
	before := -1

	insert := func() {
		if img < 0 || img >= len(a.images) {
			return
		}
		live := a.images[img].Anns
		if before < 0 || before > len(live) {
			before = len(live)
		}
		for _, an := range anns {
			live = append(live, cloneAnn(an))
		}
		a.images[img].Anns = live
	}
	remove := func() {
		if before < 0 || img < 0 || img >= len(a.images) {
			return
		}
		live := a.images[img].Anns
		if before <= len(live) {
			a.images[img].Anns = live[:before:before]
		}
	}

	return Command{
		Name: fmt.Sprintf("carry %d", len(anns)),
		Img:  img,
		Do:   insert,
		Undo: remove,
	}
}
