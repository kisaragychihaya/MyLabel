package app

import (
	"math"

	"github.com/egoist/mygo/ui"

	"mylabel/locales"
)

// handleCanvasInput is the canvas interaction state machine. All pointer
// positions are converted to image pixels by pointerImage, the inverse of the
// Draw transform, before any geometry math. Events not consumed return false
// so global shortcuts still work.
//
// State machine (see docs/poc-design.md §4 and docs/phase2-design.md §5):
//
//	idle --left down on the selected ann's handle--> dragging handle (resize/rotate/vertex)
//	idle --left down inside ann-------------------> select it + dragging move
//	idle --left down inside a selected ann--------> dragging the whole selection
//	idle --left down on empty---------------------> clear selection + creating (rubber band)
//	idle --ctrl+left down inside ann--------------> toggle it in the selection
//	idle --shift+left down on empty---------------> marquee selection
//	any  --middle down----------------------------> panning
//	ctrl+scroll ----------------------------------> zoom at cursor
//	scroll ---------------------------------------> pan
//	ctrl+A ---------------------------------------> select every annotation
//	Delete/Backspace -----------------------------> delete the selection
//	Escape ---------------------------------------> cancel in-progress op, then clear selection
//	digits 1..9 ----------------------------------> set category (selection or default)
//
// Pointer coordinates: ui.HandleInput delivers ev.X, ev.Y already relative to
// the canvas's box (ui delivers state.local of the pointer, ui/transform.go),
// which is precisely the canvas-local DIP that WindowToLocal returns for the
// pointer's window position. So the inverse of the Draw transform is
// imgPt = (ev.X-panX)/zoom, (ev.Y-panY)/zoom — see pointerImage. The canvas
// element is not needed for that and is left unnamed.
func (a *App) handleCanvasInput(_ ui.Element, ev ui.InputEvent) bool {
	// The properties dialog and the category picker own the interaction while
	// they are open: a canvas shortcut (Delete, a digit, Escape) must not edit the
	// annotations behind them, whose staged edit names one of them by index (§5.1).
	if a.props.Open || a.catPick.Open {
		return false
	}
	switch ev.Kind {
	case ui.InputPointerDown:
		return a.pointerDown(ev)
	case ui.InputPointerMove:
		// The polygon draft's rubber band follows the pointer, and a Draw
		// callback cannot ask where the pointer is: keep its image position
		// here, on every move whatever the mode.
		a.hover = a.pointerImage(ev.X, ev.Y)
		a.hasHover = true
		return a.pointerMove(ev)
	case ui.InputPointerUp:
		return a.pointerUp(ev)
	case ui.InputScroll:
		return a.scrollBy(ev)
	case ui.InputZoom:
		return a.zoomPinch(ev)
	case ui.InputKeyDown:
		return a.keyDown(ev)
	case ui.InputKeyUp:
		return a.keyUp(ev)
	}
	return false
}

// hasImage reports whether a main image is loaded into the canvas.
func (a *App) hasImage() bool { return a.bmp != nil || a.tiled != nil }

// pointerImage maps a pointer position in canvas-local DIP to image pixels:
// the inverse of the Draw transform (screen = pan + image*zoom).
func (a *App) pointerImage(x, y float32) Point {
	z := safeZoom(a.zoom)
	return Point{X: (float64(x) - a.panX) / z, Y: (float64(y) - a.panY) / z}
}

// pointerDown starts one of the drag gestures. Button 0 is the primary
// button (left), 1 the secondary one (right) and 2 the middle one
// (ui.InputEvent.Button).
//
// The annotation tool decides what a left click on empty canvas does: with
// ToolRect it starts a rubber-band box, with ToolPoly it starts (or extends) a
// polygon draft; Shift turns that click-and-drag into a marquee selection
// instead (§5). Clicking an existing annotation selects it either way, so a
// polygon stays selectable — and therefore editable — while ToolPoly is active.
// A right click opens the annotation's z-order menu instead, and a click on a
// category chip selects — or, double-clicked, re-categorizes — its annotation.
func (a *App) pointerDown(ev ui.InputEvent) bool {
	// A z-order menu is a panel of its own: the press that dismisses it must not
	// also select, clear or create anything behind it. The popover closes itself
	// on the same press, so the two agree on which press that was.
	if a.zMenu.Open {
		a.zMenu = zMenuState{}
		return true
	}
	switch ev.Button {
	case 2: // middle button: pan
		a.mode = ModePan
		// start holds the previous pointer position in canvas DIP while
		// panning (pointer events carry no deltas).
		a.drag = dragState{annIndex: -1, handle: -1, start: Point{X: float64(ev.X), Y: float64(ev.Y)}}
		return true
	case 1: // secondary button: the z-order menu of the annotation under it
		if !a.gestureIdle() {
			return false
		}
		return a.openZMenu(ev, a.pointerImage(ev.X, ev.Y))
	case 0:
		// Space held (labelme-style hand tool): left-drag pans the image
		// instead of creating an annotation.
		if a.spacePan {
			a.mode = ModePan
			a.drag = dragState{annIndex: -1, handle: -1, start: Point{X: float64(ev.X), Y: float64(ev.Y)}}
			return true
		}
		img := a.curImage()
		if img == nil || !a.hasImage() {
			return false
		}
		p := a.pointerImage(ev.X, ev.Y)

		// A draft is being drawn: every click appends a vertex, or closes it
		// by landing on the first one (§4.2).
		if a.mode == ModePolyCreate {
			a.polyDraftClick(p, ev.Clicks)
			return true
		}
		// A category chip is a control of its own, on top of everything the
		// annotation under it offers: a click selects the annotation it belongs to
		// and a double-click opens the category picker (annmenu.go), so neither can
		// turn into a move, a vertex insert or a new draft.
		if chip := a.chipHitAt(p); chip >= 0 {
			a.selectOnly(chip)
			if ev.Clicks >= 2 {
				a.openCatPick(chip)
			}
			return true
		}
		a.pruneSelection()
		ctrl := ev.Mods&ui.Ctrl != 0
		shift := ev.Mods&ui.Shift != 0

		// Handles, edges and vertices belong to a sole selection (§5), so the
		// hit test is asked about that one annotation or about none.
		hit := hitTestAnns(img.Anns, a.soleSelection(), p, a.zoom)

		// Ctrl+click on a vertex of the selected polygon deletes that vertex.
		if hit.Kind == HitVertex && ctrl {
			a.deletePolyVertex(hit.Ann, hit.Handle)
			return true
		}
		// Double-click on the edge of the selected polygon inserts a vertex
		// there (nearestEdge locates the edge). A double-click on a vertex is
		// not an edge: it must not add a duplicate vertex on top of it. On the
		// body it opens the properties dialog instead, which pointerUp does once
		// it knows the second press was not a drag (§5.1).
		if ev.Clicks >= 2 && hit.Kind != HitVertex && a.insertPolyVertexAt(hit.Ann, p) {
			return true
		}

		switch hit.Kind {
		case HitVertex, HitRotate, HitResize:
			// Reachable only with a sole selection: resize, rotate and vertex
			// drags are single-annotation edits (§5).
			a.mode = ModeHandle
			a.drag = dragState{
				annIndex: hit.Ann,
				handle:   hit.Handle,
				start:    p,
				before:   cloneAnn(img.Anns[hit.Ann]),
			}
		case HitBody:
			switch {
			case ctrl:
				// Ctrl+click toggles the annotation in the selection and never
				// starts a drag (§5).
				a.selectToggle(hit.Ann)
			case a.isSelected(hit.Ann):
				// Dragging one of the selected annotations moves them all and
				// leaves the selection as it is (§5).
				a.startGroupMove(p)
				a.drag.clickProps = ev.Clicks >= 2
			default:
				a.selectOnly(hit.Ann)
				a.startGroupMove(p)
				a.drag.clickProps = ev.Clicks >= 2
			}
		default:
			// Empty canvas: Shift+drag selects with a rectangle, a plain drag
			// creates an annotation (§5). Both start by dropping the old
			// selection, and a click without a drag leaves nothing selected.
			if shift {
				a.mode = ModeMarquee
				a.drag = dragState{annIndex: -1, handle: -1, start: p}
				a.hover, a.hasHover = p, true
				return true
			}
			a.clearSelection()
			// Creating needs a category to give the annotation: an empty panel
			// blocks all three tools instead of making a category-less
			// annotation (contract §3). A draft in progress is unaffected — the
			// list cannot empty under it, since a category in use cannot be
			// deleted.
			if len(a.categories) == 0 {
				a.lastStatus = locales.StatusNoCategories()
				return true
			}
			if a.tool == ToolPoly || a.tool == ToolObb {
				a.startPolyDraft(p)
				return true
			}
			a.mode = ModeCreating
			a.draft = Ann{ID: newAnnID(), Cat: a.curCat, Kind: KindRect, CX: p.X, CY: p.Y}
			a.drag = dragState{annIndex: -1, handle: -1, start: p}
		}
		return true
	}
	return false
}

// startGroupMove begins a ModeMove drag at image point p with every selected
// annotation snapshotted: dragging any selected annotation moves the whole
// selection as one undo step (§5). A single selection is the one-annotation
// case of the same path.
func (a *App) startGroupMove(p Point) {
	a.mode = ModeMove
	a.drag = dragState{annIndex: a.selPrimary, handle: -1, start: p}
	img := a.curImage()
	if img == nil {
		return
	}
	for _, i := range a.selectedIndices() {
		if i < 0 || i >= len(img.Anns) {
			continue
		}
		a.drag.group = append(a.drag.group, annSnapshot{index: i, before: cloneAnn(img.Anns[i])})
	}
}

// moveAnn writes the position of pre-drag snapshot before, translated by d,
// into an: a rectangle moves by its center, a polygon by translating every
// vertex (§5).
func moveAnn(an *Ann, before Ann, d Point) {
	if isPoly(&before) {
		an.Points = translatePolygon(before.Points, d)
		syncPolyBounds(an)
		return
	}
	an.CX = before.CX + d.X
	an.CY = before.CY + d.Y
}

// restoreDrag puts every annotation the drag in progress touched back to its
// pre-drag snapshot (Escape, or a canceled pointer sequence).
func (a *App) restoreDrag(img *ImageInfo) {
	if len(a.drag.group) > 0 {
		for _, s := range a.drag.group {
			if s.index >= 0 && s.index < len(img.Anns) {
				img.Anns[s.index] = cloneAnn(s.before)
			}
		}
		return
	}
	if a.drag.annIndex >= 0 && a.drag.annIndex < len(img.Anns) {
		img.Anns[a.drag.annIndex] = cloneAnn(a.drag.before)
	}
}

// commitDrag pushes the drag as one undo command: a lone annotation edit as
// cmdModify (as before), a multi-selection move as one cmdBatchSet (§5).
func (a *App) commitDrag(img *ImageInfo) {
	var edits []annEdit
	if len(a.drag.group) > 0 {
		for _, s := range a.drag.group {
			if s.index < 0 || s.index >= len(img.Anns) {
				continue
			}
			if after := img.Anns[s.index]; !sameAnn(after, s.before) {
				edits = append(edits, annEdit{index: s.index, before: s.before, after: cloneAnn(after)})
			}
		}
	} else {
		if a.drag.annIndex < 0 || a.drag.annIndex >= len(img.Anns) {
			return
		}
		if after := img.Anns[a.drag.annIndex]; !sameAnn(after, a.drag.before) {
			edits = append(edits, annEdit{index: a.drag.annIndex, before: a.drag.before, after: cloneAnn(after)})
		}
	}
	switch len(edits) {
	case 0:
	case 1:
		a.push(cmdModify(a, a.loaded, edits[0].index, edits[0].before, edits[0].after))
	default:
		a.push(cmdBatchSet(a, a.loaded, edits, "move"))
	}
}

// selectInMarquee replaces the selection with the annotations whose bounding
// box the marquee from a.drag.start to p intersects (§5). A drag too small to
// have area is a click on empty canvas: it clears the selection.
func (a *App) selectInMarquee(p Point) {
	img := a.curImage()
	if img == nil {
		return
	}
	r := boundsThrough(a.drag.start, p)
	a.clearSelection()
	if r.area() < 1 {
		return
	}
	for i := range img.Anns {
		if r.intersects(annBounds(&img.Anns[i])) {
			a.selSet[i] = true
			a.selPrimary = i
		}
	}
}

// releasedAsClick reports whether the release at ev is close enough to where
// the drag started to count as a click rather than a drag: dblClickSlopDIP on
// screen, so a shaky hand does not turn a double-click into a move.
func (a *App) releasedAsClick(ev ui.InputEvent) bool {
	return distance(a.pointerImage(ev.X, ev.Y), a.drag.start)*safeZoom(a.zoom) <= dblClickSlopDIP
}

// pointerMove applies a pointer move according to the current mode. Drags are
// recomputed from the snapshot taken at press time, never accumulated, so
// rounding cannot drift.
func (a *App) pointerMove(ev ui.InputEvent) bool {
	switch a.mode {
	case ModePan:
		a.panX += float64(ev.X) - a.drag.start.X
		a.panY += float64(ev.Y) - a.drag.start.Y
		a.drag.start = Point{X: float64(ev.X), Y: float64(ev.Y)}
		return true

	case ModeCreating:
		p := a.pointerImage(ev.X, ev.Y)
		s := a.drag.start
		a.draft.CX = (s.X + p.X) / 2
		a.draft.CY = (s.Y + p.Y) / 2
		a.draft.W = math.Abs(p.X - s.X)
		a.draft.H = math.Abs(p.Y - s.Y)
		return true

	case ModePolyCreate:
		// The draft's vertices are world coordinates: moving the pointer only
		// moves the rubber band, which drawCanvas reads from a.hover.
		return true

	case ModeMarquee:
		// Same for the selection rectangle: it is drawn from drag.start and
		// a.hover, and only the release decides what gets selected (§5).
		return true

	case ModeMove:
		img := a.curImage()
		if img == nil || len(a.drag.group) == 0 {
			return false
		}
		p := a.pointerImage(ev.X, ev.Y)
		d := Point{X: p.X - a.drag.start.X, Y: p.Y - a.drag.start.Y}
		moved := false
		for _, s := range a.drag.group {
			if s.index < 0 || s.index >= len(img.Anns) {
				continue
			}
			moveAnn(&img.Anns[s.index], s.before, d)
			moved = true
		}
		return moved

	case ModeHandle:
		img := a.curImage()
		if img == nil || a.drag.annIndex < 0 || a.drag.annIndex >= len(img.Anns) {
			return false
		}
		p := a.pointerImage(ev.X, ev.Y)
		an := &img.Anns[a.drag.annIndex]
		if isPoly(an) {
			// One vertex moves; the rest come from the pre-drag snapshot, so a
			// drag never accumulates rounding.
			if a.drag.handle < 0 || a.drag.handle >= len(a.drag.before.Points) {
				return false
			}
			pts := clonePoints(a.drag.before.Points)
			pts[a.drag.handle] = p
			an.Points = pts
			syncPolyBounds(an)
			return true
		}
		if a.drag.handle == 8 {
			moved := rotateTo(a.drag.before, p)
			if ev.Mods&ui.Shift != 0 {
				moved.Angle = snapAngle(moved.Angle, math.Pi/12) // 15°
			}
			*an = moved
			return true
		}
		*an = resizeHandle(a.drag.before, a.drag.handle, toLocal(&a.drag.before, p))
		return true
	}
	return false
}

// pointerUp commits or discards the gesture in progress.
func (a *App) pointerUp(ev ui.InputEvent) bool {
	mode := a.mode
	if mode == ModePan && ev.Button == 2 {
		a.endPan()
		return true
	}
	if ev.Button != 0 {
		return false
	}

	switch mode {
	case ModeIdle:
		return false

	case ModePan:
		a.endPan()
		return true

	case ModePolyCreate:
		// Vertices are added on press: the release of a click commits nothing.
		return true

	case ModeCreating:
		d := a.draft
		a.draft = Ann{}
		a.mode = ModeIdle
		if ev.Canceled {
			return true
		}
		// Too small to be a real box: a click or a twitch, not a drag.
		if d.W < 2 || d.H < 2 {
			return true
		}
		a.push(cmdCreate(a, a.loaded, d))
		if img := a.curImage(); img != nil && len(img.Anns) > 0 {
			a.selectOnly(len(img.Anns) - 1)
		}
		return true

	case ModeMarquee:
		a.mode = ModeIdle
		if ev.Canceled {
			return true
		}
		a.selectInMarquee(a.pointerImage(ev.X, ev.Y))
		return true

	case ModeMove, ModeHandle:
		defer func() { a.mode = ModeIdle }()
		img := a.curImage()
		if img == nil {
			return true
		}
		if ev.Canceled {
			a.restoreDrag(img)
			return true
		}
		// A double-click on an annotation's body opens its properties (§5.1).
		// The second press starts a move like any other, and only a release that
		// did not travel opens the dialog: click-then-drag, the shortest way to
		// select and move, must keep moving.
		if mode == ModeMove && a.drag.clickProps && a.releasedAsClick(ev) {
			a.restoreDrag(img)
			a.openProps(a.drag.annIndex)
			return true
		}
		a.commitDrag(img)
		return true
	}
	a.mode = ModeIdle
	return false
}

// scrollBy pans, or zooms around the pointer with Ctrl held. With Shift held
// the wheel pans horizontally instead of vertically (labelme-style).
func (a *App) scrollBy(ev ui.InputEvent) bool {
	if !a.hasImage() {
		return false
	}
	if ev.Mods&ui.Ctrl == 0 {
		// DX and DY move the *view* right and down the content (ui.InputEvent),
		// so the content itself moves the other way. A notch toward the user
		// comes as DY=+99: Windows' WHEEL_DELTA -120 over three lines of 33
		// DIP, sent with the sign flipped (internal/windows/surface.go).
		dx, dy := float64(ev.DX), float64(ev.DY)
		if ev.Mods&ui.Shift != 0 && dx == 0 {
			dx, dy = dy, 0 // vertical wheel -> horizontal pan
		}
		a.panX -= dx
		a.panY -= dy
		return true
	}
	// Scrolling up (negative DY) zooms in, as charts and maps do. The anchor
	// is the image's center, not the pointer: the image stays centered while
	// the wheel zooms (product decision).
	a.zoomAround(a.imageCenterScreen(), math.Pow(1.0015, -float64(ev.DY)))
	return true
}

// imageCenterScreen returns the image center's position in canvas DIP.
func (a *App) imageCenterScreen() Point {
	z := safeZoom(a.zoom)
	return Point{X: a.panX + a.imgW/2*z, Y: a.panY + a.imgH/2*z}
}

// zoomPinch zooms around the image's center by a touchpad pinch's scale.
func (a *App) zoomPinch(ev ui.InputEvent) bool {
	if !a.hasImage() {
		return false
	}
	f := float64(ev.Scale)
	if !(f > 0) || math.IsInf(f, 0) || math.IsNaN(f) {
		return false
	}
	a.zoomAround(a.imageCenterScreen(), f)
	return true
}

// zoomAround applies geom's zoomAroundPoint to the viewport.
func (a *App) zoomAround(cursor Point, factor float64) {
	pan, zoom := zoomAroundPoint(Point{X: a.panX, Y: a.panY}, a.zoom, cursor, factor)
	a.panX, a.panY, a.zoom = pan.X, pan.Y, zoom
}

// keyDown handles the canvas's shortcuts.
func (a *App) keyDown(ev ui.InputEvent) bool {
	// The z-order menu takes Escape first: it closes the menu rather than
	// cancelling a gesture or dropping the selection behind it.
	if a.zMenu.Open && ev.Key == ui.KeyEscape {
		a.zMenu = zMenuState{}
		return true
	}
	// Ctrl+A selects every annotation of the loaded image (§5).
	if ev.Mods&ui.Ctrl != 0 && ev.Key == ui.KeyA {
		a.selectAll()
		return true
	}
	// Ctrl+B carries the previous image's annotations over (§2).
	if ev.Mods&ui.Ctrl != 0 && ev.Key == ui.KeyB {
		return a.carryPrevImage()
	}
	// A/D switch images, F refits the view: only without modifiers, so the
	// Ctrl+A/Ctrl+D combinations above and in View keep their single meaning.
	if ev.Mods == 0 {
		switch ev.Key {
		case ui.KeyA:
			return a.goImage(-1)
		case ui.KeyD:
			return a.goImage(1)
		case ui.KeyF:
			return a.fitWindow()
		}
	}
	switch ev.Key {
	case ui.KeySpace:
		a.spacePan = true
		return true
	case ui.KeyEscape:
		// Escape first cancels the gesture in progress, and only then clears
		// the selection (§5).
		if a.cancelDrag() {
			return true
		}
		if a.selectionCount() > 0 {
			a.clearSelection()
			return true
		}
		return false
	case ui.KeyR:
		a.setTool(ToolRect)
		return true
	case ui.KeyP:
		a.setTool(ToolPoly)
		return true
	case ui.KeyO:
		a.setTool(ToolObb)
		return true
	case ui.KeyL:
		// L shows and hides the category chips on the canvas.
		a.toggleLabels()
		return true
	case ui.KeyEnter:
		// The obb draft closes on its fourth click, so Enter has nothing to do
		// there (§5.2); a polygon closes on it (§4.2).
		if a.mode == ModePolyCreate && a.tool != ToolObb {
			return a.closePolyDraft()
		}
		return false
	case ui.KeyBackspace:
		// While a polygon is being drawn, Backspace steps back one vertex
		// instead of deleting the selection (§4.2).
		if a.mode == ModePolyCreate {
			return a.popPolyDraftVertex()
		}
		fallthrough
	case ui.KeyDelete:
		// A drag holds snapshots by index: removing annotations now would make
		// them name other ones when the drag commits. Escape ends the drag.
		if a.dragInProgress() {
			return false
		}
		return a.deleteSelection()
	}

	if ev.Key >= ui.Key1 && ev.Key <= ui.Key9 {
		return a.setSelectionCategory(int(ev.Key - ui.Key1))
	}
	return false
}

// dragInProgress reports whether a gesture holds annotation snapshots by
// index: a move, a handle drag or a marquee. The annotation list must not
// change during one, or the snapshots would name other annotations when the
// gesture commits.
func (a *App) dragInProgress() bool {
	switch a.mode {
	case ModeMove, ModeHandle, ModeMarquee:
		return true
	}
	return false
}

// gestureIdle reports whether no gesture and no draft is in progress: the menus
// that edit an annotation by index (the z-order menu, annmenu.go) wait for it.
func (a *App) gestureIdle() bool {
	return a.mode == ModeIdle && len(a.polyDraft) == 0 && !a.dragInProgress()
}

// deleteSelection deletes every selected annotation as one undo step, so a
// single undo brings the whole set back (§5).
func (a *App) deleteSelection() bool {
	img := a.curImage()
	if img == nil {
		return false
	}
	a.pruneSelection()
	idx := a.selectedIndices()
	if len(idx) == 0 {
		return false
	}
	if len(idx) == 1 {
		a.push(cmdDelete(a, a.loaded, idx[0]))
	} else {
		a.push(cmdBatchDelete(a, a.loaded, idx))
	}
	a.clearSelection()
	return true
}

// setSelectionCategory sets the category of every selected annotation, as one
// undo step (§5). With nothing selected the digit picks the category of the
// next annotation instead.
func (a *App) setSelectionCategory(cat int) bool {
	// Like Delete, a batch write during a drag would race the drag's index
	// based snapshots (dragInProgress).
	if a.dragInProgress() {
		return false
	}
	// The digit keys cover the first nine categories whatever the list holds:
	// a shorter list clamps, so no annotation points past its end (§6). An
	// empty list has no category to pick, so the digits do nothing.
	if len(a.categories) == 0 {
		return true
	}
	if cat >= len(a.categories) {
		cat = len(a.categories) - 1
	}
	img := a.curImage()
	if img == nil || a.selectionCount() == 0 {
		a.curCat = cat
		return true
	}
	a.pruneSelection()
	var edits []annEdit
	for _, i := range a.selectedIndices() {
		if i < 0 || i >= len(img.Anns) {
			continue
		}
		before := cloneAnn(img.Anns[i])
		if before.Cat == cat {
			continue
		}
		after := before
		after.Cat = cat
		edits = append(edits, annEdit{index: i, before: before, after: after})
	}
	switch len(edits) {
	case 0:
	case 1:
		a.push(cmdModify(a, a.loaded, edits[0].index, edits[0].before, edits[0].after))
	default:
		a.push(cmdBatchSet(a, a.loaded, edits, "category"))
	}
	return true
}

// keyUp releases momentary modifiers.
func (a *App) keyUp(ev ui.InputEvent) bool {
	if ev.Key == ui.KeySpace {
		a.spacePan = false
		return true
	}
	return false
}

// cancelDrag reverts the gesture in progress (Escape): the annotations the
// drag moved go back to their snapshots, a draft or marquee is dropped.
func (a *App) cancelDrag() bool {
	switch a.mode {
	case ModeCreating:
		a.draft = Ann{}
		a.mode = ModeIdle
		return true
	case ModePolyCreate:
		a.cancelPolyDraft()
		return true
	case ModeMove, ModeHandle:
		if img := a.curImage(); img != nil {
			a.restoreDrag(img)
		}
		a.mode = ModeIdle
		return true
	case ModeMarquee:
		a.mode = ModeIdle
		return true
	case ModePan:
		// Panning may have interrupted a polygon draft: Escape ends the pan
		// only, so the vertices clicked so far survive.
		a.endPan()
		return true
	}
	return false
}

// endPan leaves panning and returns to what the pan interrupted: an in-progress
// polygon draft keeps its vertices, anything else goes idle.
func (a *App) endPan() {
	if len(a.polyDraft) > 0 {
		a.mode = ModePolyCreate
		return
	}
	a.mode = ModeIdle
}

// setTool switches the annotation tool, dropping a polygon draft that belongs
// to the tool being left.
func (a *App) setTool(t Tool) {
	if t == a.tool {
		return
	}
	if a.mode == ModePolyCreate {
		a.cancelPolyDraft()
	}
	a.tool = t
}

// startPolyDraft begins a click-by-click draft at image point p: ToolPoly's
// click on empty canvas (docs/phase2-design.md §4.2), or ToolObb's, which
// collects the four corners of a rotated box the same way (§5.2). The draft's
// Kind is the kind the finished annotation will have.
func (a *App) startPolyDraft(p Point) {
	if len(a.categories) == 0 {
		a.lastStatus = locales.StatusNoCategories()
		return
	}
	a.mode = ModePolyCreate
	a.polyDraft = []Point{p}
	kind := KindPoly
	if a.tool == ToolObb {
		kind = KindRect
	}
	a.draft = Ann{ID: newAnnID(), Cat: a.curCat, Kind: kind}
}

// polyDraftClick appends a vertex to the draft. The draft closes when the click
// lands within polyCloseSnapDIP of the first vertex, or when it is the second
// click of a double-click (§4.2), as long as three vertices exist.
//
// The obb tool closes differently: exactly four clicks make the box, the fourth
// one immediately, and none of the polygon's close gestures apply (§5.2).
func (a *App) polyDraftClick(p Point, clicks int) {
	if len(a.polyDraft) == 0 {
		a.startPolyDraft(p)
		return
	}
	if a.tool == ToolObb {
		if len(a.polyDraft) >= obbCorners {
			return
		}
		a.polyDraft = append(a.polyDraft, p)
		if len(a.polyDraft) == obbCorners {
			a.closeObbDraft()
		}
		return
	}
	if len(a.polyDraft) >= minPolyVertices && (clicks >= 2 || a.nearDraftStart(p)) {
		a.closePolyDraft()
		return
	}
	a.polyDraft = append(a.polyDraft, p)
}

// nearDraftStart reports whether p is close enough to the draft's first vertex
// to close the polygon there: polyCloseSnapDIP on screen, so the snap radius
// does not shrink as the user zooms out.
func (a *App) nearDraftStart(p Point) bool {
	if len(a.polyDraft) == 0 {
		return false
	}
	return distance(p, a.polyDraft[0])*safeZoom(a.zoom) <= polyCloseSnapDIP
}

// closePolyDraft commits the draft as a polygon annotation and selects it. A
// draft of fewer than three vertices is not closable and stays open.
func (a *App) closePolyDraft() bool {
	pts := a.polyDraft
	if len(pts) < minPolyVertices {
		return false
	}
	ann := newPolyAnn(a.draft.ID, a.draft.Cat, pts)
	if ann.ID == "" {
		ann.ID = newAnnID()
	}
	a.polyDraft = nil
	a.draft = Ann{}
	a.mode = ModeIdle
	a.push(cmdCreate(a, a.loaded, ann))
	if img := a.curImage(); img != nil && len(img.Anns) > 0 {
		a.selectOnly(len(img.Anns) - 1)
	}
	return true
}

// cancelPolyDraft drops the vertices clicked so far.
func (a *App) cancelPolyDraft() {
	a.polyDraft = nil
	a.draft = Ann{}
	a.mode = ModeIdle
}

// closeObbDraft turns the four clicked corners into a rotated rectangle and
// selects it (§5.2). A draft the four clicks did not give an area for is
// dropped, as a too-small rectangle drag is, and the reason is reported.
func (a *App) closeObbDraft() bool {
	pts := a.polyDraft
	if len(pts) < obbCorners {
		return false
	}
	box, ok := obbFromPoints(pts)
	if !ok {
		a.cancelPolyDraft()
		a.lastStatus = locales.StatusObbTooSmall()
		return false
	}
	box.ID = a.draft.ID
	if box.ID == "" {
		box.ID = newAnnID()
	}
	box.Cat = a.draft.Cat
	a.polyDraft = nil
	a.draft = Ann{}
	a.mode = ModeIdle
	a.push(cmdCreate(a, a.loaded, box))
	if img := a.curImage(); img != nil && len(img.Anns) > 0 {
		a.selectOnly(len(img.Anns) - 1)
	}
	return true
}

// popPolyDraftVertex removes the last clicked vertex; emptying the draft ends
// polygon creation.
func (a *App) popPolyDraftVertex() bool {
	if len(a.polyDraft) == 0 {
		return false
	}
	a.polyDraft = a.polyDraft[:len(a.polyDraft)-1]
	if len(a.polyDraft) == 0 {
		a.cancelPolyDraft()
	}
	return true
}

// deletePolyVertex removes vertex i of images[annIndex] as one undoable edit.
// A triangle keeps its three vertices (removeVertex returns nil).
func (a *App) deletePolyVertex(annIndex, vertex int) {
	img := a.curImage()
	if img == nil || annIndex < 0 || annIndex >= len(img.Anns) {
		return
	}
	an := &img.Anns[annIndex]
	if !isPoly(an) {
		return
	}
	before := cloneAnn(*an)
	pts := removeVertex(before.Points, vertex)
	if pts == nil {
		return
	}
	after := before
	after.Points = pts
	syncPolyBounds(&after)
	a.selectOnly(annIndex)
	a.push(cmdModify(a, a.loaded, annIndex, before, after))
}

// insertPolyVertexAt inserts a vertex into the selected polygon on the edge
// nearest to p, as a double-click does (§4.3). It reports whether it did: the
// pointer must be within polyEdgeRadiusDIP of an edge of the selected polygon,
// which annIndex is expected to name. A double-click just outside the outline
// reaches here with annIndex -1 (no body hit) and falls back to the selection.
// Vertex editing needs a sole selection (§5), so a multi-selection is refused.
func (a *App) insertPolyVertexAt(annIndex int, p Point) bool {
	img := a.curImage()
	if img == nil {
		return false
	}
	sole := a.soleSelection()
	if annIndex < 0 {
		annIndex = sole
	}
	if annIndex < 0 || annIndex != sole || annIndex >= len(img.Anns) {
		return false
	}
	an := &img.Anns[annIndex]
	if !isPoly(an) || len(an.Points) < minPolyVertices {
		return false
	}
	edge, dist := nearestEdge(p, an.Points)
	if edge < 0 || dist > polyEdgeRadiusDIP/safeZoom(a.zoom) {
		return false
	}
	before := cloneAnn(*an)
	after := before
	after.Points = insertPointOnEdge(before.Points, edge, p)
	syncPolyBounds(&after)
	a.push(cmdModify(a, a.loaded, annIndex, before, after))
	return true
}
