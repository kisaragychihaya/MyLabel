package app

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// undoTestApp returns an App with imgs empty images, ready for annotation
// edits, rooted in a temporary directory.
func undoTestApp(t *testing.T, imgs int) *App {
	t.Helper()
	a := New()
	a.dir = t.TempDir()
	for i := 0; i < imgs; i++ {
		a.images = append(a.images, ImageInfo{
			Path:   fmt.Sprintf("%s/img%d.png", a.dir, i),
			Name:   fmt.Sprintf("img%d.png", i),
			Width:  640,
			Height: 480,
		})
	}
	a.selImg = 0
	a.loaded = 0
	return a
}

// pendingStubPanic reports whether v is the panic raised by a sibling module
// that is still a stub (persist.go's scheduleSave, reached through setDirty).
func pendingStubPanic(v any) bool {
	s, ok := v.(string)
	return ok && strings.Contains(s, "TODO: module")
}

// ignoringStubs runs f and swallows only the panic of a still-stubbed sibling
// module. push/undo/redo finish all stack bookkeeping before they call
// setDirty, so state is final even when the autosave path panics.
// Remove this accommodation once internal/app/persist.go is implemented.
func ignoringStubs(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			if !pendingStubPanic(r) {
				panic(r)
			}
			t.Logf("ignored pending stub panic: %s", r)
		}
	}()
	f()
}

func undoDo(t *testing.T, a *App, cmd Command) {
	t.Helper()
	ignoringStubs(t, func() { a.push(cmd) })
}

func undoUndo(t *testing.T, a *App) {
	t.Helper()
	ignoringStubs(t, a.undo)
}

func undoRedo(t *testing.T, a *App) {
	t.Helper()
	ignoringStubs(t, a.redo)
}

func undoAnns(t *testing.T, a *App, img int) []Ann {
	t.Helper()
	if img < 0 || img >= len(a.images) {
		t.Fatalf("image index %d out of range", img)
	}
	return a.images[img].Anns
}

func undoWant(t *testing.T, what string, got, want []Ann) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %+v, want %+v", what, got, want)
	}
}

func TestUndoCmdTrip(t *testing.T) {
	a := undoTestApp(t, 1)
	first := Ann{ID: "a", Cat: 0, CX: 10, CY: 10, W: 20, H: 20}
	a.images[0].Anns = []Ann{first}
	initial := append([]Ann(nil), a.images[0].Anns...)

	// 1. create
	created := Ann{ID: "b", Cat: 1, CX: 50, CY: 60, W: 30, H: 10, Angle: 0.4}
	undoDo(t, a, cmdCreate(a, 0, created))
	undoWant(t, "after create", undoAnns(t, a, 0), []Ann{first, created})

	// 2. modify
	before := a.images[0].Anns[0]
	after := before
	after.CX, after.CY, after.Angle = 77, 88, 0.9
	undoDo(t, a, cmdModify(a, 0, 0, before, after))
	undoWant(t, "after modify", undoAnns(t, a, 0), []Ann{after, created})

	// 3. delete
	undoDo(t, a, cmdDelete(a, 0, 1))
	final := []Ann{after}
	undoWant(t, "after delete", undoAnns(t, a, 0), final)

	if len(a.undoStack) != 3 || len(a.redoStack) != 0 {
		t.Fatalf("stacks after 3 pushes: undo=%d redo=%d, want 3/0", len(a.undoStack), len(a.redoStack))
	}
	if !a.dirty {
		t.Fatalf("push must mark the document dirty")
	}

	// undo all three: back to the initial state
	for i := 0; i < 3; i++ {
		undoUndo(t, a)
	}
	undoWant(t, "after 3 undos", undoAnns(t, a, 0), initial)
	if len(a.undoStack) != 0 || len(a.redoStack) != 3 {
		t.Fatalf("stacks after 3 undos: undo=%d redo=%d, want 0/3", len(a.undoStack), len(a.redoStack))
	}

	// redo all three: back to the final state
	for i := 0; i < 3; i++ {
		undoRedo(t, a)
	}
	undoWant(t, "after 3 redos", undoAnns(t, a, 0), final)
	if len(a.undoStack) != 3 || len(a.redoStack) != 0 {
		t.Fatalf("stacks after 3 redos: undo=%d redo=%d, want 3/0", len(a.undoStack), len(a.redoStack))
	}

	// a second undo/redo cycle must still be stable
	for i := 0; i < 3; i++ {
		undoUndo(t, a)
	}
	undoWant(t, "second undo cycle", undoAnns(t, a, 0), initial)
	for i := 0; i < 3; i++ {
		undoRedo(t, a)
	}
	undoWant(t, "second redo cycle", undoAnns(t, a, 0), final)
}

func TestUndoLimitDropsOldest(t *testing.T) {
	a := undoTestApp(t, 1)
	count := 0
	for i := 0; i < undoLimit+50; i++ {
		undoDo(t, a, Command{
			Name: "inc",
			Do:   func() { count++ },
			Undo: func() { count-- },
		})
	}
	if count != undoLimit+50 {
		t.Fatalf("Do not applied %d times: count=%d", undoLimit+50, count)
	}
	if len(a.undoStack) != undoLimit {
		t.Fatalf("undo stack length %d, want %d", len(a.undoStack), undoLimit)
	}

	for i := 0; i < undoLimit; i++ {
		undoUndo(t, a)
	}
	if count != 50 {
		t.Fatalf("after undoing the kept %d commands count=%d, want 50 (oldest dropped)",
			undoLimit, count)
	}
	if len(a.undoStack) != 0 || len(a.redoStack) != undoLimit {
		t.Fatalf("stacks after drain: undo=%d redo=%d, want 0/%d",
			len(a.undoStack), len(a.redoStack), undoLimit)
	}

	// undoing past the oldest command is a safe no-op
	undoUndo(t, a)
	if count != 50 || len(a.redoStack) != undoLimit {
		t.Fatalf("undo on empty stack changed state: count=%d redo=%d", count, len(a.redoStack))
	}

	// redos are limited to what was undone
	for i := 0; i < undoLimit; i++ {
		undoRedo(t, a)
	}
	if count != undoLimit+50 {
		t.Fatalf("after redo count=%d, want %d", count, undoLimit+50)
	}
	undoRedo(t, a)
	if count != undoLimit+50 {
		t.Fatalf("redo on empty stack changed count to %d", count)
	}
}

func TestRedoClearedByNewPush(t *testing.T) {
	a := undoTestApp(t, 1)
	base := Ann{ID: "base", CX: 0, CY: 0, W: 10, H: 10}
	a.images[0].Anns = []Ann{base}

	for i := 0; i < 2; i++ {
		undoDo(t, a, cmdCreate(a, 0, Ann{ID: fmt.Sprintf("n%d", i), W: 10, H: 10}))
	}
	undoUndo(t, a)
	undoUndo(t, a)
	if len(a.redoStack) != 2 {
		t.Fatalf("redo stack length %d, want 2", len(a.redoStack))
	}
	undoWant(t, "after 2 undos", undoAnns(t, a, 0), []Ann{base})

	n := Ann{ID: "c", W: 5, H: 5}
	undoDo(t, a, cmdCreate(a, 0, n))
	if len(a.redoStack) != 0 {
		t.Fatalf("redo stack not cleared by push: %d entries", len(a.redoStack))
	}
	if len(a.undoStack) != 1 {
		t.Fatalf("undo stack length %d, want 1", len(a.undoStack))
	}
	undoWant(t, "after new push", undoAnns(t, a, 0), []Ann{base, n})
}

func TestUndoRedoOnEmptyStacks(t *testing.T) {
	a := undoTestApp(t, 1)
	undoUndo(t, a)
	undoRedo(t, a)
	if len(a.undoStack) != 0 || len(a.redoStack) != 0 {
		t.Fatalf("empty stacks mutated: undo=%d redo=%d", len(a.undoStack), len(a.redoStack))
	}
	if a.dirty {
		t.Fatalf("undo/redo on empty stacks must not mark the document dirty")
	}
}

func TestPushMarksDirty(t *testing.T) {
	a := undoTestApp(t, 1)
	a.dirty = false
	undoDo(t, a, Command{Name: "nop", Do: func() {}, Undo: func() {}})
	if !a.dirty {
		t.Fatalf("push must set dirty")
	}
	a.dirty = false
	undoUndo(t, a)
	if !a.dirty {
		t.Fatalf("undo must set dirty")
	}
	a.dirty = false
	undoRedo(t, a)
	if !a.dirty {
		t.Fatalf("redo must set dirty")
	}
}

func TestCmdCreateReinsertsClampedIndex(t *testing.T) {
	a := undoTestApp(t, 1)
	base := Ann{ID: "base", W: 1, H: 1}
	extra := Ann{ID: "extra", W: 2, H: 2}
	a.images[0].Anns = []Ann{base}

	cmd := cmdCreate(a, 0, extra)
	cmd.Do()
	undoWant(t, "create", undoAnns(t, a, 0), []Ann{base, extra})

	cmd.Undo()
	undoWant(t, "create undo", undoAnns(t, a, 0), []Ann{base})

	// the slice shrank behind the command's back: the redo index must clamp
	a.images[0].Anns = nil
	cmd.Do()
	undoWant(t, "create redo clamped", undoAnns(t, a, 0), []Ann{extra})
}

func TestCmdDeleteReinsertsClampedIndex(t *testing.T) {
	a := undoTestApp(t, 1)
	x := Ann{ID: "x", W: 1, H: 1}
	y := Ann{ID: "y", W: 2, H: 2}
	z := Ann{ID: "z", W: 3, H: 3}
	a.images[0].Anns = []Ann{x, y, z}

	cmd := cmdDelete(a, 0, 2)
	cmd.Do()
	undoWant(t, "delete", undoAnns(t, a, 0), []Ann{x, y})

	// other operations shortened the slice: undo must insert, not panic
	a.images[0].Anns = []Ann{x}
	cmd.Undo()
	undoWant(t, "delete undo clamped", undoAnns(t, a, 0), []Ann{x, z})

	// undo before any Do is a no-op
	fresh := cmdDelete(a, 0, 0)
	fresh.Undo()
	undoWant(t, "delete undo before do", undoAnns(t, a, 0), []Ann{x, z})
}

func TestCmdModifySnapshots(t *testing.T) {
	a := undoTestApp(t, 1)
	before := Ann{ID: "m", Cat: 0, CX: 1, CY: 2, W: 3, H: 4, Angle: 0.5}
	after := before
	after.CX, after.Cat = 99, 4
	a.images[0].Anns = []Ann{before}

	cmd := cmdModify(a, 0, 0, before, after)
	cmd.Do()
	undoWant(t, "modify do", undoAnns(t, a, 0), []Ann{after})
	cmd.Undo()
	undoWant(t, "modify undo", undoAnns(t, a, 0), []Ann{before})
	cmd.Do()
	undoWant(t, "modify redo", undoAnns(t, a, 0), []Ann{after})

	// out-of-range targets are ignored, not panics
	bad := cmdModify(a, 0, 7, before, after)
	bad.Do()
	bad.Undo()
	undoWant(t, "modify out of range", undoAnns(t, a, 0), []Ann{after})

	badImg := cmdModify(a, 9, 0, before, after)
	badImg.Do()
	badImg.Undo()
	undoWant(t, "modify bad image", undoAnns(t, a, 0), []Ann{after})
}

func TestCmdAcrossImages(t *testing.T) {
	a := undoTestApp(t, 2)
	a.images[1].Anns = []Ann{{ID: "b0", W: 1, H: 1}}

	cmd := cmdCreate(a, 1, Ann{ID: "b1", W: 2, H: 2})
	cmd.Do()
	if len(a.images[0].Anns) != 0 {
		t.Fatalf("command touched the wrong image")
	}
	if len(a.images[1].Anns) != 2 {
		t.Fatalf("image 1 has %d annotations, want 2", len(a.images[1].Anns))
	}
	cmd.Undo()
	if len(a.images[1].Anns) != 1 {
		t.Fatalf("undo left %d annotations, want 1", len(a.images[1].Anns))
	}
}
