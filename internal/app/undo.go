package app

import (
	"fmt"
	"sort"
)

// Command is one undoable operation.
type Command struct {
	Name string
	// Img indexes a.images and names the image the command edits, so the right
	// row is marked dirty even when another image is loaded by the time the
	// command is undone or redone. Negative means "the loaded image".
	Img  int
	Do   func()
	Undo func()
}

const undoLimit = 200

// push executes cmd.Do, pushes it on the undo stack, and clears the redo
// stack. It also marks the document dirty.
func (a *App) push(cmd Command) {
	if cmd.Do == nil {
		return
	}
	cmd.Do()

	a.undoStack = append(a.undoStack, cmd)
	if len(a.undoStack) > undoLimit {
		// drop the oldest entries, without keeping the growing array alive
		trimmed := make([]Command, undoLimit)
		copy(trimmed, a.undoStack[len(a.undoStack)-undoLimit:])
		a.undoStack = trimmed
	}
	a.redoStack = nil

	a.setDirty(cmd.Img)
}

// undo reverts the most recent command, if any.
func (a *App) undo() {
	if len(a.undoStack) == 0 {
		return
	}
	cmd := a.undoStack[len(a.undoStack)-1]
	a.undoStack = a.undoStack[:len(a.undoStack)-1]
	if cmd.Undo != nil {
		cmd.Undo()
	}
	a.redoStack = append(a.redoStack, cmd)
	a.setDirty(cmd.Img)
}

// redo re-applies the most recently undone command, if any.
func (a *App) redo() {
	if len(a.redoStack) == 0 {
		return
	}
	cmd := a.redoStack[len(a.redoStack)-1]
	a.redoStack = a.redoStack[:len(a.redoStack)-1]
	if cmd.Do != nil {
		cmd.Do()
	}
	a.undoStack = append(a.undoStack, cmd)
	a.setDirty(cmd.Img)
}

// cmdCreate returns the command that appends ann to images[img].Anns. The
// annotation is deep-copied on every Do, so the command's snapshot never
// aliases the live annotation's vertex slice (docs/phase2-design.md §2.3).
func cmdCreate(a *App, img int, ann Ann) Command {
	index := -1 // insertion point, fixed on the first Do
	return Command{
		Name: "create",
		Img:  img,
		Do: func() {
			if img < 0 || img >= len(a.images) {
				return
			}
			anns := a.images[img].Anns
			if index < 0 || index > len(anns) {
				index = len(anns)
			}
			anns = append(anns, Ann{})
			copy(anns[index+1:], anns[index:])
			anns[index] = cloneAnn(ann)
			a.images[img].Anns = anns
		},
		Undo: func() {
			if img < 0 || img >= len(a.images) || index < 0 {
				return
			}
			anns := a.images[img].Anns
			if index >= len(anns) {
				return
			}
			a.images[img].Anns = append(anns[:index:index], anns[index+1:]...)
		},
	}
}

// cmdDelete returns the command that removes images[img].Anns[index]. The
// removed annotation is snapshotted with a deep copy, and a fresh copy is
// inserted on undo, so neither the snapshot nor the restored annotation can be
// mutated through the other.
func cmdDelete(a *App, img, index int) Command {
	var removed Ann
	haveRemoved := false
	return Command{
		Name: "delete",
		Img:  img,
		Do: func() {
			if img < 0 || img >= len(a.images) || index < 0 {
				return
			}
			anns := a.images[img].Anns
			if index >= len(anns) {
				return
			}
			removed = cloneAnn(anns[index])
			haveRemoved = true
			a.images[img].Anns = append(anns[:index:index], anns[index+1:]...)
		},
		Undo: func() {
			if !haveRemoved || img < 0 || img >= len(a.images) {
				return
			}
			anns := a.images[img].Anns
			// the slice may have shrunk since the removal: clamp the index
			at := index
			if at > len(anns) {
				at = len(anns)
			}
			if at < 0 {
				at = 0
			}
			anns = append(anns, Ann{})
			copy(anns[at+1:], anns[at:])
			anns[at] = cloneAnn(removed)
			a.images[img].Anns = anns
		},
	}
}

// cmdModify returns the command that replaces before with after at
// images[img].Anns[index]. Both snapshots must already be deep copies; the
// written annotation is copied again so a later edit of the live annotation
// cannot reach into the command's snapshots.
func cmdModify(a *App, img, index int, before, after Ann) Command {
	set := func(v Ann) {
		if img < 0 || img >= len(a.images) || index < 0 {
			return
		}
		anns := a.images[img].Anns
		if index >= len(anns) {
			return
		}
		a.images[img].Anns[index] = cloneAnn(v)
	}
	return Command{
		Name: "modify",
		Img:  img,
		Do:   func() { set(after) },
		Undo: func() { set(before) },
	}
}

// cmdReorder returns the command that moves the annotation carrying id from
// index from to index to, keeping every other annotation in the order it had:
// the slice order is the drawing order, so this is the z-order (annmenu.go).
//
// The annotation is named by its ID as well as its index, because the indices of
// a redo may have moved under other edits: a command that finds another
// annotation where it expects its own does nothing rather than move a stranger.
func cmdReorder(a *App, img, from, to int, id string) Command {
	move := func(source, target int) {
		if img < 0 || img >= len(a.images) {
			return
		}
		anns := a.images[img].Anns
		if source < 0 || source >= len(anns) || target < 0 || target >= len(anns) {
			return
		}
		if anns[source].ID != id {
			return
		}
		an := anns[source]
		out := make([]Ann, 0, len(anns))
		for i := range anns {
			if i == source {
				continue
			}
			if len(out) == target {
				out = append(out, an)
			}
			out = append(out, anns[i])
		}
		if len(out) == target {
			out = append(out, an)
		}
		a.images[img].Anns = out
	}
	return Command{
		Name: "reorder",
		Img:  img,
		Do:   func() { move(from, to) },
		Undo: func() { move(to, from) },
	}
}

// annEdit is one annotation's before/after snapshot inside a batch command.
type annEdit struct {
	index  int
	before Ann
	after  Ann
}

// cmdBatchSet returns the command that writes every edit's after snapshot in a
// single undo step and restores the before snapshots on undo: a multi-selection
// move or re-categorization is one operation (§5). The snapshots must already
// be deep copies, and each write is copied again, so neither the command nor
// the live list can reach into the other's vertex slices.
func cmdBatchSet(a *App, img int, edits []annEdit, name string) Command {
	apply := func(pick func(annEdit) Ann) {
		if img < 0 || img >= len(a.images) {
			return
		}
		anns := a.images[img].Anns
		for _, e := range edits {
			if e.index < 0 || e.index >= len(anns) {
				continue
			}
			anns[e.index] = cloneAnn(pick(e))
		}
	}
	return Command{
		Name: name,
		Img:  img,
		Do:   func() { apply(func(e annEdit) Ann { return e.after }) },
		Undo: func() { apply(func(e annEdit) Ann { return e.before }) },
	}
}

// cmdBatchDelete returns the command that removes every annotation named by
// indices in one undo step, so undoing a multi-selection delete brings the
// whole set back at once (§5). The removed annotations are deep-copied on the
// first Do, and a fresh copy is inserted on undo, as cmdDelete does for one;
// the snapshots are not retaken on a redo, so a later in-place edit of the live
// list cannot leak into the undo state.
func cmdBatchDelete(a *App, img int, indices []int) Command {
	type removed struct {
		index int
		ann   Ann
	}
	var gone []removed

	remove := func() {
		if img < 0 || img >= len(a.images) {
			return
		}
		anns := a.images[img].Anns
		idx := make([]int, 0, len(indices))
		seen := make(map[int]bool, len(indices))
		for _, i := range indices {
			if i >= 0 && i < len(anns) && !seen[i] {
				idx = append(idx, i)
				seen[i] = true
			}
		}
		// Ascending snapshots, then removal from the highest index down, so
		// each remaining index still names the annotation it snapshotted. The
		// snapshots are taken once: a redo removes what the first Do saw, and
		// its undo restores exactly those annotations.
		sort.Ints(idx)
		if len(gone) == 0 {
			for _, i := range idx {
				gone = append(gone, removed{index: i, ann: cloneAnn(anns[i])})
			}
		}
		for k := len(idx) - 1; k >= 0; k-- {
			i := idx[k]
			anns = append(anns[:i:i], anns[i+1:]...)
		}
		a.images[img].Anns = anns
	}

	insert := func() {
		if img < 0 || img >= len(a.images) {
			return
		}
		anns := a.images[img].Anns
		for _, r := range gone { // ascending: every insertion lands at its index
			at := r.index
			if at > len(anns) {
				at = len(anns)
			}
			if at < 0 {
				at = 0
			}
			anns = append(anns, Ann{})
			copy(anns[at+1:], anns[at:])
			anns[at] = cloneAnn(r.ann)
		}
		a.images[img].Anns = anns
	}

	return Command{
		Name: fmt.Sprintf("delete %d", len(indices)),
		Img:  img,
		Do:   remove,
		Undo: insert,
	}
}

// cmdDeleteCategory returns the command that removes categories[idx] and moves
// every annotation of a later category one step down, in a single undo step
// (docs/phase2-design.md §6).
//
// The category list and the category column of every image are snapshotted on
// the first Do, so a redo removes what that Do saw and its undo puts exactly
// that back. Every image whose annotations moved is marked dirty: removing a
// category rewrites that image's rows too, not only the loaded image's.
func cmdDeleteCategory(a *App, idx int) Command {
	var before []Category
	var cols map[int][]int // image index -> one category per annotation

	snapshot := func() {
		before = append([]Category(nil), a.categories...)
		cols = make(map[int][]int, len(a.images))
		for i := range a.images {
			anns := a.images[i].Anns
			if len(anns) == 0 {
				continue
			}
			cats := make([]int, len(anns))
			for k := range anns {
				cats[k] = anns[k].Cat
			}
			cols[i] = cats
		}
	}
	// each writes every image's category column through f, and marks the images
	// whose column actually moved dirty.
	each := func(f func(cat int) int) {
		for i, cats := range cols {
			anns := a.images[i].Anns
			moved := false
			for k := range cats {
				if k >= len(anns) {
					break // the image lost annotations since the snapshot
				}
				if c := f(anns[k].Cat); c != anns[k].Cat {
					anns[k].Cat = c
					moved = true
				}
			}
			if moved {
				a.setDirty(i)
			}
		}
	}

	return Command{
		Name: "delete category",
		Img:  -1,
		Do: func() {
			if before == nil {
				snapshot()
			}
			if idx < 0 || idx >= len(a.categories) {
				return
			}
			// A three-index slice makes append copy, so the snapshot keeps
			// pointing at the untouched array.
			a.categories = append(a.categories[:idx:idx], a.categories[idx+1:]...)
			each(func(cat int) int {
				if cat > idx {
					return cat - 1
				}
				return cat
			})
			if a.curCat > idx {
				a.curCat--
			}
			a.clampCurCat()
		},
		Undo: func() {
			if before == nil {
				return
			}
			a.categories = append([]Category(nil), before...)
			for i, cats := range cols {
				anns := a.images[i].Anns
				moved := false
				for k := range cats {
					if k >= len(anns) {
						break
					}
					if anns[k].Cat != cats[k] {
						anns[k].Cat = cats[k]
						moved = true
					}
				}
				if moved {
					a.setDirty(i)
				}
			}
			if a.curCat >= idx {
				a.curCat++
			}
			a.clampCurCat()
		},
	}
}
