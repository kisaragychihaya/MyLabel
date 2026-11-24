package app

// Image navigation and view fitting (docs/phase5-design.md §1, §3).

// goImage moves the thumbnail selection delta images along the list (A/D
// keys). It only sets selImg: the thumbStrip's reconcile (selImg != loaded)
// performs the load, clears the selection and closes the dialogs, exactly as
// a thumbnail click does — the logic is not duplicated here.
func (a *App) goImage(delta int) bool {
	if len(a.images) == 0 {
		return false
	}
	// A drag holds annotation snapshots by index; switching images under it
	// would leave the gesture pointing at another set (same guard as Delete).
	if a.dragInProgress() {
		return false
	}
	target := a.selImg + delta
	if target < 0 {
		a.lastStatus = "已经是第一张"
		return false
	}
	if target >= len(a.images) {
		a.lastStatus = "已经是最后一张"
		return false
	}
	a.selImg = target
	return true
}

// fitWindow refits the loaded image to the canvas on the next frame (F key):
// the same "fit and center" an image gets on load, via the flag the canvas's
// Draw consumes — only Draw knows the canvas's size (canvas.go fitToCanvas).
func (a *App) fitWindow() bool {
	if !a.hasImage() {
		return false
	}
	a.fitOnLoad = true
	return true
}
