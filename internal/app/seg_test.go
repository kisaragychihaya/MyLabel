package app

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"mylabel/internal/store"
)

// Tests for the seg (polygon) annotation chain: model conversion, undo deep
// copies, the tools, creation and editing on the canvas, hit priority,
// rendering and the round trip through the store and the export document.
// docs/phase2-design.md §4.

// squarePolyAnn is the reference polygon: a 200×150 rectangle from (100,100).
func squarePolyAnn(id string, cat int) Ann {
	return newPolyAnn(id, cat, []Point{{100, 100}, {300, 100}, {300, 250}, {100, 250}})
}

// polyDown/polyUp/polyKey build canvas input events with the click count and
// modifiers the Tester cannot set. Coordinates are image pixels.
func polyDown(a *App, p Point, clicks int, mods ui.Modifiers) ui.InputEvent {
	lx, ly := localOf(a, p)
	return ui.InputEvent{Kind: ui.InputPointerDown, X: lx, Y: ly, Button: 0, Clicks: clicks, Mods: mods}
}

func polyUp(a *App, p Point) ui.InputEvent {
	lx, ly := localOf(a, p)
	return ui.InputEvent{Kind: ui.InputPointerUp, X: lx, Y: ly, Button: 0, Clicks: 1}
}

func polyMove(a *App, p Point, mods ui.Modifiers) ui.InputEvent {
	lx, ly := localOf(a, p)
	return ui.InputEvent{Kind: ui.InputPointerMove, X: lx, Y: ly, Mods: mods}
}

func polyKey(key ui.Key) ui.InputEvent { return ui.InputEvent{Kind: ui.InputKeyDown, Key: key} }

func polyKeyUp(key ui.Key) ui.InputEvent { return ui.InputEvent{Kind: ui.InputKeyUp, Key: key} }

// polyClick clicks image point p once on the canvas handler.
func polyClick(a *App, p Point) {
	a.handleCanvasInput(ui.Element{}, polyDown(a, p, 1, 0))
	a.handleCanvasInput(ui.Element{}, polyUp(a, p))
}

// polyWantPoints checks an annotation's vertices and its mirrored bounding box.
func polyWantPoints(t *testing.T, what string, an Ann, want []Point) {
	t.Helper()
	polyWantPointsNear(t, what, an, want, 1e-9)
}

// polyWantPointsNear is polyWantPoints with a coordinate tolerance: a vertex
// that went through the float32 pointer mapping needs a pixel-sized one.
func polyWantPointsNear(t *testing.T, what string, an Ann, want []Point, tol float64) {
	t.Helper()
	if !isPoly(&an) {
		t.Fatalf("%s: kind = %q, want %q", what, an.Kind, KindPoly)
	}
	if len(an.Points) != len(want) {
		t.Fatalf("%s: %d vertices %v, want %d %v", what, len(an.Points), an.Points, len(want), want)
	}
	for i := range want {
		if !nearly(an.Points[i].X, want[i].X, tol) || !nearly(an.Points[i].Y, want[i].Y, tol) {
			t.Fatalf("%s: vertex %d = %v, want %v (all: %v)", what, i, an.Points[i], want[i], an.Points)
		}
	}
	x, y, w, h := polygonBBox(want)
	if !nearly(an.CX, x+w/2, tol) || !nearly(an.CY, y+h/2, tol) ||
		!nearly(an.W, w, tol) || !nearly(an.H, h, tol) || an.Angle != 0 {
		t.Fatalf("%s: bounds = (%v,%v %v×%v angle %v), want (%v,%v %v×%v angle 0)",
			what, an.CX, an.CY, an.W, an.H, an.Angle, x+w/2, y+h/2, w, h)
	}
}

// --- model -----------------------------------------------------------------

func TestPolyAnnRecConversion(t *testing.T) {
	pts := []Point{{0, 0}, {10, 0}, {5, 8}}
	an := Ann{ID: "p", Cat: 3, Kind: KindPoly, CX: -1, CY: -1, W: -1, H: -1, Angle: 1, Points: pts}
	rect := Ann{ID: "r", Cat: 1, CX: 5, CY: 5, W: 10, H: 20, Angle: 0.5}

	recs := annsToRecs([]Ann{rect, an})
	if len(recs) != 2 {
		t.Fatalf("recs = %d, want 2", len(recs))
	}
	if recs[0].Kind != KindRect || recs[0].Points != nil {
		t.Errorf("rect rec = kind %q points %v, want %q/nil", recs[0].Kind, recs[0].Points, KindRect)
	}
	// The stored bounding box comes from the vertices, never from the mirrored
	// (here bogus) fields; Angle is 0 and the vertices are flattened.
	if recs[1].Kind != KindPoly {
		t.Errorf("poly rec kind = %q, want %q", recs[1].Kind, KindPoly)
	}
	if recs[1].CX != 5 || recs[1].CY != 4 || recs[1].W != 10 || recs[1].H != 8 || recs[1].Angle != 0 {
		t.Errorf("poly rec bounds = (%v,%v %v×%v angle %v), want (5,4 10×8 angle 0)",
			recs[1].CX, recs[1].CY, recs[1].W, recs[1].H, recs[1].Angle)
	}
	wantFlat := []float64{0, 0, 10, 0, 5, 8}
	if len(recs[1].Points) != len(wantFlat) {
		t.Fatalf("poly rec points = %v, want %v", recs[1].Points, wantFlat)
	}
	for i := range wantFlat {
		if recs[1].Points[i] != wantFlat[i] {
			t.Fatalf("poly rec points = %v, want %v", recs[1].Points, wantFlat)
		}
	}

	// Back: the vertices are authoritative, so a stale stored box is ignored.
	recs[1].CX, recs[1].CY, recs[1].W, recs[1].H = 999, 999, 999, 999
	back := annsFromRecs(recs)
	polyWantPoints(t, "round trip", back[1], pts)
	if back[0].Kind != KindRect {
		t.Errorf("rect round trip kind = %q, want %q", back[0].Kind, KindRect)
	}
	if back[1].Cat != 3 || back[1].ID != "p" {
		t.Errorf("poly round trip = %+v, want cat 3 id p", back[1])
	}

	// A trailing unpaired coordinate and an empty list are tolerated.
	tail := annsFromRecs([]store.AnnRec{{ID: "t", Kind: store.KindPoly, Points: []float64{1, 2, 3, 4, 5}}})
	polyWantPoints(t, "odd flat list", tail[0], []Point{{1, 2}, {3, 4}})
	if got := annsFromRecs([]store.AnnRec{{ID: "e", Kind: store.KindPoly}}); len(got[0].Points) != 0 {
		t.Errorf("empty flat list points = %v, want none", got[0].Points)
	}
}

func TestCloneAnnIsDeep(t *testing.T) {
	orig := squarePolyAnn("p", 1)
	cp := cloneAnn(orig)

	if &cp.Points[0] == &orig.Points[0] {
		t.Fatal("cloneAnn shares the vertices' backing array")
	}
	orig.Points[0] = Point{999, 999}
	if cp.Points[0] != (Point{100, 100}) {
		t.Errorf("mutating the original changed the clone: %v", cp.Points[0])
	}
	cp.Points[1] = Point{888, 888}
	if orig.Points[1] != (Point{300, 100}) {
		t.Errorf("mutating the clone changed the original: %v", orig.Points[1])
	}
	if !sameAnn(cp, cp) {
		t.Error("sameAnn is not reflexive")
	}
	if sameAnn(cp, orig) {
		t.Error("edited annotations still compare equal")
	}
	// A rect clone keeps a nil Points and stays comparable through sameAnn.
	r := Ann{ID: "r", CX: 1, CY: 2, W: 3, H: 4}
	if rc := cloneAnn(r); rc.Points != nil || !sameAnn(rc, r) {
		t.Errorf("rect clone = %+v, want an identical copy with nil points", rc)
	}
}

// --- creation --------------------------------------------------------------

func TestPolyCreateByClicksAndCloseOnFirstVertex(t *testing.T) {
	a := newTestApp()
	a.setTool(ToolPoly)

	pts := []Point{{100, 100}, {300, 100}, {300, 250}}
	for _, p := range pts {
		polyClick(a, p)
	}
	if a.mode != ModePolyCreate {
		t.Fatalf("mode = %v after three clicks, want ModePolyCreate", a.mode)
	}
	if len(a.polyDraft) != 3 {
		t.Fatalf("draft = %v, want 3 vertices", a.polyDraft)
	}
	if n := len(anns(a)); n != 0 {
		t.Fatalf("annotations = %d while drafting, want 0", n)
	}

	// A click within polyCloseSnapDIP of the first vertex closes the polygon;
	// the closing point is not a vertex of its own.
	polyClick(a, Point{103, 101})
	if a.mode != ModeIdle {
		t.Fatalf("mode = %v after closing, want ModeIdle", a.mode)
	}
	if a.polyDraft != nil {
		t.Errorf("draft = %v after closing, want none", a.polyDraft)
	}
	got := anns(a)
	if len(got) != 1 {
		t.Fatalf("annotations = %d after closing, want 1", len(got))
	}
	polyWantPoints(t, "created", got[0], pts)
	if got[0].ID == "" {
		t.Error("created polygon has no ID")
	}
	// A creation tool leaves the polygon unselected (docs/phase6-design.md §6.2).
	if a.selectionCount() != 0 {
		t.Errorf("selectionCount = %d after closing, want 0", a.selectionCount())
	}

	a.undo()
	if n := len(anns(a)); n != 0 {
		t.Errorf("annotations = %d after undo, want 0", n)
	}
	a.redo()
	if n := len(anns(a)); n != 1 {
		t.Fatalf("annotations = %d after redo, want 1", n)
	}
	polyWantPoints(t, "after redo", anns(a)[0], pts)
}

func TestPolyCreateWithTester(t *testing.T) {
	// The same flow through the real event path: a press/release on the
	// canvas element, not the handler directly.
	a, tt := tester(t)
	a.setTool(ToolPoly)
	tt.Frame()
	cv := canvasBounds(t, tt)

	click := func(p Point) {
		x, y := screenOf(cv, a, p)
		tt.Press(x, y)
		tt.Release(x, y)
	}
	pts := []Point{{100, 100}, {300, 100}, {300, 250}}
	for _, p := range pts {
		click(p)
	}
	if len(a.polyDraft) != 3 {
		t.Fatalf("draft = %v after three clicks on the canvas, want 3 vertices", a.polyDraft)
	}
	// Closing on the first vertex.
	click(Point{102, 102})

	got := anns(a)
	if len(got) != 1 {
		t.Fatalf("annotations = %d after closing, want 1", len(got))
	}
	polyWantPointsNear(t, "created", got[0], pts, 0.5)
	// A creation tool leaves the polygon unselected (docs/phase6-design.md §6.2).
	if a.selectionCount() != 0 {
		t.Errorf("selectionCount = %d, want the new polygon unselected", a.selectionCount())
	}
}

func TestPolyCreateClosesWithEnter(t *testing.T) {
	a := newTestApp()
	a.setTool(ToolPoly)

	// Two vertices cannot be closed: the polygon needs three.
	polyClick(a, Point{100, 100})
	polyClick(a, Point{300, 100})
	if a.closePolyDraft() {
		t.Fatal("a two-vertex draft must not close")
	}
	if a.mode != ModePolyCreate {
		t.Fatalf("mode = %v, want ModePolyCreate to stay", a.mode)
	}

	polyClick(a, Point{300, 250})
	if !a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyEnter)) {
		t.Fatal("Enter did not close the draft")
	}
	got := anns(a)
	if len(got) != 1 {
		t.Fatalf("annotations = %d after Enter, want 1", len(got))
	}
	polyWantPoints(t, "created", got[0], []Point{{100, 100}, {300, 100}, {300, 250}})
	if a.mode != ModeIdle {
		t.Errorf("mode = %v after Enter, want ModeIdle", a.mode)
	}
	if len(a.undoStack) != 1 {
		t.Errorf("undo stack = %d, want 1", len(a.undoStack))
	}
}

func TestPolyCreateDoubleClickCloses(t *testing.T) {
	a := newTestApp()
	a.setTool(ToolPoly)
	for _, p := range []Point{{100, 100}, {300, 100}, {300, 250}} {
		polyClick(a, p)
	}

	// The first click of the pair adds the last vertex, the second one (the
	// platform's click count 2) closes the polygon.
	a.handleCanvasInput(ui.Element{}, polyDown(a, Point{100, 250}, 1, 0))
	a.handleCanvasInput(ui.Element{}, polyUp(a, Point{100, 250}))
	if len(a.polyDraft) != 4 {
		t.Fatalf("draft = %v, want 4 vertices", a.polyDraft)
	}
	a.handleCanvasInput(ui.Element{}, polyDown(a, Point{100, 250}, 2, 0))
	a.handleCanvasInput(ui.Element{}, polyUp(a, Point{100, 250}))

	got := anns(a)
	if len(got) != 1 {
		t.Fatalf("annotations = %d after a double click, want 1", len(got))
	}
	polyWantPoints(t, "created", got[0], []Point{{100, 100}, {300, 100}, {300, 250}, {100, 250}})
}

func TestPolyCreateEscapeCancels(t *testing.T) {
	a := newTestApp()
	a.setTool(ToolPoly)
	polyClick(a, Point{100, 100})
	polyClick(a, Point{300, 100})

	if !a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyEscape)) {
		t.Fatal("Escape was not consumed")
	}
	if a.mode != ModeIdle {
		t.Fatalf("mode = %v after Escape, want ModeIdle", a.mode)
	}
	if a.polyDraft != nil {
		t.Errorf("draft = %v after Escape, want none", a.polyDraft)
	}
	if n := len(anns(a)); n != 0 {
		t.Errorf("annotations = %d after Escape, want 0", n)
	}
	if len(a.undoStack) != 0 {
		t.Errorf("undo stack = %d after Escape, want 0", len(a.undoStack))
	}
}

func TestPolyCreateBackspaceRemovesVertex(t *testing.T) {
	a := newTestApp()
	a.setTool(ToolPoly)
	polyClick(a, Point{100, 100})
	polyClick(a, Point{300, 100})
	polyClick(a, Point{300, 250})

	// Backspace steps back one vertex instead of deleting the selection.
	if !a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyBackspace)) {
		t.Fatal("Backspace was not consumed while drafting")
	}
	if len(a.polyDraft) != 2 {
		t.Fatalf("draft = %v after Backspace, want 2 vertices", a.polyDraft)
	}

	// Drawing continues from there.
	polyClick(a, Point{100, 250})
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyEnter))
	got := anns(a)
	if len(got) != 1 {
		t.Fatalf("annotations = %d, want 1", len(got))
	}
	polyWantPoints(t, "created", got[0], []Point{{100, 100}, {300, 100}, {100, 250}})

	// Backspacing the last vertex ends the draft; one more does nothing.
	b := newTestApp()
	b.setTool(ToolPoly)
	polyClick(b, Point{100, 100})
	b.handleCanvasInput(ui.Element{}, polyKey(ui.KeyBackspace))
	if b.mode != ModeIdle || b.polyDraft != nil {
		t.Fatalf("empty draft: mode = %v draft = %v, want idle/none", b.mode, b.polyDraft)
	}
	if b.handleCanvasInput(ui.Element{}, polyKey(ui.KeyBackspace)) {
		t.Error("Backspace on an empty draft was consumed")
	}
}

func TestPolyDraftKeepsWorldVerticesThroughPanAndZoom(t *testing.T) {
	a := newTestApp()
	a.setTool(ToolPoly)
	p1, p2 := Point{100, 100}, Point{300, 100}
	polyClick(a, p1)
	polyClick(a, p2)

	// The wheel pans, Ctrl+wheel zooms and Space+drag pans, all while the
	// draft is open: the vertices are world coordinates and do not move.
	a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputScroll, X: 100, Y: 100, DY: 99})
	if !nearly(a.panY, -99, 1e-9) {
		t.Fatalf("pan y = %v after the wheel, want -99", a.panY)
	}
	zoomBefore := a.zoom
	a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputScroll, X: 100, Y: 100, DY: -99, Mods: ui.Ctrl})
	if a.zoom <= zoomBefore {
		t.Fatalf("zoom = %v after Ctrl+wheel, want > %v", a.zoom, zoomBefore)
	}

	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeySpace))
	a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputPointerDown, X: 50, Y: 50, Button: 0})
	if a.mode != ModePan {
		t.Fatalf("mode = %v while space-dragging, want ModePan", a.mode)
	}
	a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputPointerMove, X: 80, Y: 65})
	a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputPointerUp, X: 80, Y: 65, Button: 0})
	a.handleCanvasInput(ui.Element{}, polyKeyUp(ui.KeySpace))
	if a.mode != ModePolyCreate {
		t.Fatalf("mode = %v after the pan, want the draft back", a.mode)
	}
	if len(a.polyDraft) != 2 || a.polyDraft[0] != p1 || a.polyDraft[1] != p2 {
		t.Fatalf("draft = %v after pan/zoom, want %v", a.polyDraft, []Point{p1, p2})
	}

	polyClick(a, Point{300, 250})
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyEnter))
	got := anns(a)
	if len(got) != 1 {
		t.Fatalf("annotations = %d, want 1", len(got))
	}
	polyWantPointsNear(t, "created", got[0], []Point{p1, p2, {300, 250}}, 0.5)
}

func TestPolyToolLeavesRectCreationAlone(t *testing.T) {
	// ToolRect: a drag still creates a rectangle, tagged KindRect.
	a := newTestApp()
	a.setTool(ToolRect)
	dragAll(a, Point{100, 100}, Point{300, 250})
	got := anns(a)
	if len(got) != 1 {
		t.Fatalf("annotations = %d after a drag, want 1", len(got))
	}
	if got[0].Kind != KindRect {
		t.Errorf("kind = %q, want %q", got[0].Kind, KindRect)
	}
	if got[0].Points != nil {
		t.Errorf("rect points = %v, want none", got[0].Points)
	}

	// ToolPoly: the same drag starts a draft instead and creates nothing.
	b := newTestApp()
	b.setTool(ToolPoly)
	dragAll(b, Point{100, 100}, Point{300, 250})
	if n := len(anns(b)); n != 0 {
		t.Fatalf("annotations = %d after a drag with the polygon tool, want 0", n)
	}
	if len(b.polyDraft) != 1 {
		t.Fatalf("draft = %v after the press, want 1 vertex", b.polyDraft)
	}
}

// dragAll presses at from, moves to to and releases, through the canvas
// handler (no Tester, so the click count is explicit).
func dragAll(a *App, from, to Point) {
	a.handleCanvasInput(ui.Element{}, polyDown(a, from, 1, 0))
	a.handleCanvasInput(ui.Element{}, polyMove(a, to, 0))
	a.handleCanvasInput(ui.Element{}, polyUp(a, to))
}

// --- editing ---------------------------------------------------------------

func TestPolyVertexDragMovesOneVertex(t *testing.T) {
	a := newTestApp()
	a.images[0].Anns = []Ann{squarePolyAnn("p", 0)}
	a.selectOnly(0)

	// Drag the first vertex from (100,100) to (60,70).
	a.handleCanvasInput(ui.Element{}, polyDown(a, Point{100, 100}, 1, 0))
	if a.mode != ModeHandle || a.drag.handle != 0 {
		t.Fatalf("mode = %v handle = %d after pressing a vertex, want ModeHandle/0", a.mode, a.drag.handle)
	}
	a.handleCanvasInput(ui.Element{}, polyMove(a, Point{80, 85}, 0))
	a.handleCanvasInput(ui.Element{}, polyMove(a, Point{60, 70}, 0))
	a.handleCanvasInput(ui.Element{}, polyUp(a, Point{60, 70}))

	polyWantPoints(t, "after vertex drag", anns(a)[0], []Point{{60, 70}, {300, 100}, {300, 250}, {100, 250}})
	if len(a.undoStack) != 1 {
		t.Fatalf("undo stack = %d after the drag, want 1", len(a.undoStack))
	}

	a.undo()
	polyWantPoints(t, "after undo", anns(a)[0], squarePolyAnn("p", 0).Points)
	a.redo()
	polyWantPoints(t, "after redo", anns(a)[0], []Point{{60, 70}, {300, 100}, {300, 250}, {100, 250}})
}

func TestPolyCtrlClickDeletesVertex(t *testing.T) {
	a := newTestApp()
	a.images[0].Anns = []Ann{squarePolyAnn("p", 0)}
	a.selectOnly(0)

	// Ctrl+click on the second vertex removes it, without starting a drag.
	a.handleCanvasInput(ui.Element{}, polyDown(a, Point{300, 100}, 1, ui.Ctrl))
	a.handleCanvasInput(ui.Element{}, polyUp(a, Point{300, 100}))
	if a.mode != ModeIdle {
		t.Errorf("mode = %v after Ctrl+click, want ModeIdle", a.mode)
	}
	polyWantPoints(t, "after Ctrl+click", anns(a)[0], []Point{{100, 100}, {300, 250}, {100, 250}})

	// A triangle keeps its three vertices: the click is ignored, no command.
	a.handleCanvasInput(ui.Element{}, polyDown(a, Point{300, 250}, 1, ui.Ctrl))
	a.handleCanvasInput(ui.Element{}, polyUp(a, Point{300, 250}))
	if n := len(anns(a)[0].Points); n != 3 {
		t.Fatalf("vertices = %d after Ctrl+click on a triangle, want 3", n)
	}
	if len(a.undoStack) != 1 {
		t.Errorf("undo stack = %d, want 1 (the ignored click pushed nothing)", len(a.undoStack))
	}

	a.undo()
	polyWantPoints(t, "after undo", anns(a)[0], squarePolyAnn("p", 0).Points)
}

func TestPolyDoubleClickInsertsVertexOnEdge(t *testing.T) {
	a := newTestApp()
	a.images[0].Anns = []Ann{squarePolyAnn("p", 0)}
	a.selectOnly(0)

	// Double-click the middle of the top edge: a vertex lands between the two
	// corners of that edge.
	mid := Point{200, 100}
	a.handleCanvasInput(ui.Element{}, polyDown(a, mid, 2, 0))
	a.handleCanvasInput(ui.Element{}, polyUp(a, mid))

	polyWantPoints(t, "after double click", anns(a)[0],
		[]Point{{100, 100}, mid, {300, 100}, {300, 250}, {100, 250}})
	if len(a.undoStack) != 1 {
		t.Fatalf("undo stack = %d, want 1", len(a.undoStack))
	}

	// Far from every edge nothing is inserted.
	a.handleCanvasInput(ui.Element{}, polyDown(a, Point{200, 175}, 2, 0))
	a.handleCanvasInput(ui.Element{}, polyUp(a, Point{200, 175}))
	if n := len(anns(a)[0].Points); n != 5 {
		t.Fatalf("vertices = %d after a double click in the middle, want 5", n)
	}

	a.undo()
	polyWantPoints(t, "after undo", anns(a)[0], squarePolyAnn("p", 0).Points)
}

func TestPolyBodyDragMovesWholePolygon(t *testing.T) {
	a := newTestApp()
	a.images[0].Anns = []Ann{squarePolyAnn("p", 0)}
	a.selectOnly(0)

	a.handleCanvasInput(ui.Element{}, polyDown(a, Point{200, 180}, 1, 0))
	if a.mode != ModeMove {
		t.Fatalf("mode = %v after pressing the body, want ModeMove", a.mode)
	}
	a.handleCanvasInput(ui.Element{}, polyMove(a, Point{225, 165}, 0))
	a.handleCanvasInput(ui.Element{}, polyMove(a, Point{250, 150}, 0))
	a.handleCanvasInput(ui.Element{}, polyUp(a, Point{250, 150}))

	polyWantPoints(t, "after moving", anns(a)[0], []Point{
		{150, 70}, {350, 70}, {350, 220}, {150, 220},
	})
	a.undo()
	polyWantPoints(t, "after undo", anns(a)[0], squarePolyAnn("p", 0).Points)
}

func TestPolyClickSelectsThroughBodyAndDeselectsInNotch(t *testing.T) {
	a := newTestApp()
	// A concave L: the notch at x>400, y>400 is outside the polygon.
	a.images[0].Anns = []Ann{newPolyAnn("c", 0, []Point{
		{100, 100}, {400, 100}, {400, 400}, {250, 400}, {250, 600}, {100, 600},
	})}
	a.clearSelection()

	// A click inside the body selects it (the owner of the pointInPolygon hit).
	polyClick(a, Point{200, 200})
	if a.soleSelection() != 0 {
		t.Fatalf("soleSelection = %d after clicking inside the polygon, want 0", a.soleSelection())
	}
	// A click in the notch is a click on empty canvas: it deselects and, with
	// the polygon tool, starts a draft.
	a.setTool(ToolPoly)
	polyClick(a, Point{350, 500})
	if a.selectionCount() != 0 {
		t.Errorf("selectionCount = %d after clicking the notch, want 0", a.selectionCount())
	}
	if a.mode != ModePolyCreate || len(a.polyDraft) != 1 {
		t.Errorf("mode = %v draft = %v after the notch click, want a new draft", a.mode, a.polyDraft)
	}
}

func TestPolyDeleteKeyRemovesSelectedPolygon(t *testing.T) {
	a := newTestApp()
	a.images[0].Anns = []Ann{squarePolyAnn("p", 0), {ID: "r", Kind: KindRect, CX: 500, CY: 400, W: 40, H: 30}}
	a.selectOnly(0)

	if !a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyDelete)) {
		t.Fatal("Delete was not consumed")
	}
	got := anns(a)
	if len(got) != 1 || got[0].ID != "r" {
		t.Fatalf("annotations after Delete = %+v, want just the rect", got)
	}
	if a.selectionCount() != 0 {
		t.Errorf("selectionCount = %d after Delete, want 0", a.selectionCount())
	}
	a.undo()
	if n := len(anns(a)); n != 2 {
		t.Fatalf("annotations = %d after undo, want 2", n)
	}
	polyWantPoints(t, "restored", anns(a)[0], squarePolyAnn("p", 0).Points)
}

func TestPolyCategoryKeysApply(t *testing.T) {
	a := newTestApp()
	a.images[0].Anns = []Ann{squarePolyAnn("p", 0)}
	a.selectOnly(0)

	a.handleCanvasInput(ui.Element{}, polyKey(ui.Key3))
	if cat := anns(a)[0].Cat; cat != 2 {
		t.Errorf("category = %d after key 3, want 2", cat)
	}
	a.undo()
	if cat := anns(a)[0].Cat; cat != 0 {
		t.Errorf("category = %d after undo, want 0", cat)
	}
}

// --- hit testing -----------------------------------------------------------

func TestPolyHitPriority(t *testing.T) {
	anns := []Ann{squarePolyAnn("p", 0)}
	sel := 0

	// Vertex handles beat the body, and carry the vertex index.
	hitNear(t, "vertex 0", hitTestAnns(anns, sel, Point{100, 100}, 1), HitVertex, 0, 0)
	hitNear(t, "vertex 2", hitTestAnns(anns, sel, Point{300, 250}, 1), HitVertex, 0, 2)
	hitNear(t, "body", hitTestAnns(anns, sel, Point{200, 175}, 1), HitBody, 0, -1)
	hitNear(t, "outside", hitTestAnns(anns, sel, Point{50, 50}, 1), HitNone, -1, -1)

	// A polygon has no resize and no rotate handles: where a rectangle's top
	// edge handle and rotate handle would be, it is just its body.
	hitNear(t, "no resize handle", hitTestAnns(anns, sel, Point{200, 100}, 1), HitBody, 0, -1)
	hitNear(t, "no rotate handle", hitTestAnns(anns, sel, Point{200, 76}, 1), HitNone, -1, -1)

	// The vertex pick radius scales with the zoom, like every handle's.
	off := Point{103, 103} // 4.24 px from the corner, inside the body
	hitNear(t, "vertex pick zoom 1", hitTestAnns(anns, sel, off, 1), HitVertex, 0, 0)
	hitNear(t, "vertex pick zoom 2", hitTestAnns(anns, sel, off, 2), HitBody, 0, -1)
}

func TestPolyHitUsesPointInPolygon(t *testing.T) {
	// Concave L: the notch is outside the annotation.
	concave := newPolyAnn("c", 0, []Point{{0, 0}, {10, 0}, {10, 4}, {4, 4}, {4, 10}, {0, 10}})
	anns := []Ann{concave}

	hitNear(t, "inside the arm", hitTestAnns(anns, -1, Point{2, 2}, 1), HitBody, 0, -1)
	hitNear(t, "inside the foot", hitTestAnns(anns, -1, Point{8, 2}, 1), HitBody, 0, -1)
	hitNear(t, "notch", hitTestAnns(anns, -1, Point{8, 8}, 1), HitNone, -1, -1)
	// Borders count as a body hit.
	hitNear(t, "on the reflex edge", hitTestAnns(anns, -1, Point{4, 7}, 1), HitBody, 0, -1)
}

func TestPolyHitZOrder(t *testing.T) {
	// The rect covers the polygon, which is the reference 100..300 × 100..250.
	rect := Ann{ID: "r", Kind: KindRect, CX: 200, CY: 175, W: 400, H: 400}
	poly := squarePolyAnn("p", 0)

	// Inside the overlap the annotation drawn last wins.
	hitNear(t, "poly on top", hitTestAnns([]Ann{rect, poly}, -1, Point{200, 175}, 1), HitBody, 1, -1)
	hitNear(t, "rect on top", hitTestAnns([]Ann{poly, rect}, -1, Point{200, 175}, 1), HitBody, 1, -1)
	// Inside the rect only (the polygon's body is pointInPolygon, not a box).
	hitNear(t, "only the rect", hitTestAnns([]Ann{rect, poly}, -1, Point{50, 50}, 1), HitBody, 0, -1)
}

// --- rendering -------------------------------------------------------------

func TestPolySelectedRendersFillAndVertexHandles(t *testing.T) {
	a, tt := tester(t)
	a.bmp = markerImage() // black, so the translucent fill is unmistakable
	a.imgW, a.imgH = 800, 600
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.images[0].Anns = []Ann{squarePolyAnn("p", 0)}
	a.selectOnly(0)
	tt.Frame()

	cv := canvasBounds(t, tt)
	frame := tt.Image()
	at := func(p Point) (int, int) {
		x, y := screenOf(cv, a, p)
		return int(math.Round(float64(x))), int(math.Round(float64(y)))
	}

	// Translucent category-0 red inside the polygon.
	x, y := at(Point{200, 175})
	body := frame.RGBAAt(x, y)
	if !(body.R > body.G && body.R > body.B && body.R > 20) {
		t.Errorf("polygon body = %v, want a translucent red fill over black", body)
	}
	// The outline is painted at full strength on the edge.
	x, y = at(Point{200, 100})
	if edge := frame.RGBAAt(x, y); edge.R < body.R+60 {
		t.Errorf("top edge = %v, want the category color over the %v fill", edge, body)
	}
	// A vertex handle square sits on each vertex.
	for i, v := range a.images[0].Anns[0].Points {
		x, y = at(v)
		if got := frame.RGBAAt(x, y); !whiteish(got) {
			t.Errorf("vertex %d handle at %v = %v, want a white square", i, v, got)
		}
	}

	// Unselected: no fill, no handles, only the outline.
	a.clearSelection()
	tt.Frame()
	frame = tt.Image()
	x, y = at(Point{200, 175})
	if got := frame.RGBAAt(x, y); !blackish(got) {
		t.Errorf("unselected polygon body = %v, want the black image back", got)
	}
	x, y = at(Point{100, 100})
	if got := frame.RGBAAt(x, y); whiteish(got) {
		t.Errorf("unselected vertex = %v, want no handle square", got)
	}
}

func TestPolyDraftIsDrawn(t *testing.T) {
	a, tt := tester(t)
	a.bmp = markerImage()
	a.imgW, a.imgH = 800, 600
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.setTool(ToolPoly)
	tt.Frame()

	// Two vertices plus the pointer: the draft's line crosses the midpoint of
	// the segment from the second vertex to the pointer.
	a.handleCanvasInput(ui.Element{}, polyDown(a, Point{100, 100}, 1, 0))
	a.handleCanvasInput(ui.Element{}, polyUp(a, Point{100, 100}))
	a.handleCanvasInput(ui.Element{}, polyDown(a, Point{300, 100}, 1, 0))
	a.handleCanvasInput(ui.Element{}, polyUp(a, Point{300, 100}))
	a.handleCanvasInput(ui.Element{}, polyMove(a, Point{300, 300}, 0))
	tt.Frame()

	cv := canvasBounds(t, tt)
	frame := tt.Image()
	// The committed edge is drawn at full accent strength over black.
	for _, p := range []Point{{200, 100}, {300, 200}} {
		x, y := screenOf(cv, a, p)
		got := frame.RGBAAt(int(math.Round(float64(x))), int(math.Round(float64(y))))
		if sum := int(got.R) + int(got.G) + int(got.B); sum < 150 {
			t.Errorf("draft line at %v = %v, want the accent color", p, got)
		}
	}
	// The first vertex is marked with a square, the ring appears once the
	// draft may be closed (three vertices).
	x, y := screenOf(cv, a, Point{100, 100})
	if got := frame.RGBAAt(int(math.Round(float64(x))), int(math.Round(float64(y)))); !whiteish(got) {
		t.Errorf("first draft vertex = %v, want its square", got)
	}
	if len(a.polyDraft) != 2 {
		t.Fatalf("draft = %v after the pointer move, want the 2 committed vertices", a.polyDraft)
	}
}

// --- tools -----------------------------------------------------------------

func TestToolShortcuts(t *testing.T) {
	a := newTestApp()
	if a.tool != ToolSelect {
		t.Fatalf("default tool = %v, want ToolSelect", a.tool)
	}
	if !a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyP)) {
		t.Fatal("P was not consumed")
	}
	if a.tool != ToolPoly {
		t.Fatalf("tool = %v after P, want ToolPoly", a.tool)
	}
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyR))
	if a.tool != ToolRect {
		t.Fatalf("tool = %v after R, want ToolRect", a.tool)
	}
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyV))
	if a.tool != ToolSelect {
		t.Fatalf("tool = %v after V, want ToolSelect", a.tool)
	}

	// Switching away from the polygon tool drops the draft in progress.
	a.setTool(ToolPoly)
	polyClick(a, Point{100, 100})
	if a.mode != ModePolyCreate {
		t.Fatalf("mode = %v, want ModePolyCreate", a.mode)
	}
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyR))
	if a.tool != ToolRect || a.mode != ModeIdle || a.polyDraft != nil {
		t.Fatalf("after switching to the rect tool: tool = %v mode = %v draft = %v", a.tool, a.mode, a.polyDraft)
	}
	if n := len(anns(a)); n != 0 {
		t.Errorf("annotations = %d, want 0", n)
	}
}

func TestToolButtonsSwitchTools(t *testing.T) {
	a, tt := realViewTester(t)
	a.setTool(ToolRect)
	tt.Frame()

	for _, label := range []string{"矩形 (R)", "多边形 (P)"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("toolbar has no %q button; texts = %v", label, tt.Texts())
		}
	}
	if err := tt.Click("多边形 (P)"); err != nil {
		t.Fatalf("click the polygon tool: %v", err)
	}
	if a.tool != ToolPoly {
		t.Errorf("tool = %v after clicking 多边形, want ToolPoly", a.tool)
	}
	tt.Frame()
	if err := tt.Click("矩形 (R)"); err != nil {
		t.Fatalf("click the rect tool: %v", err)
	}
	if a.tool != ToolRect {
		t.Errorf("tool = %v after clicking 矩形, want ToolRect", a.tool)
	}
}

// --- undo deep copies ------------------------------------------------------

func TestPolyUndoSnapshotsAreDeepCopies(t *testing.T) {
	a := undoTestApp(t, 1)
	poly := squarePolyAnn("p", 0)
	a.images[0].Anns = []Ann{poly}
	original := cloneAnn(poly)

	// Deleting snapshots the polygon; restoring it must not hand back the very
	// slice the annotation was built from.
	del := cmdDelete(a, 0, 0)
	del.Do()
	if len(a.images[0].Anns) != 0 {
		t.Fatalf("delete left %d annotations", len(a.images[0].Anns))
	}
	del.Undo()
	restored := &a.images[0].Anns[0]
	if &restored.Points[0] == &poly.Points[0] {
		t.Error("undo restored the very slice captured at deletion time")
	}

	if !sameAnn(*restored, original) {
		t.Fatalf("restored annotation = %+v, want %+v", *restored, original)
	}

	// The create command's snapshot must not follow an in-place edit of the
	// live annotation it inserted (docs/phase2-design.md §2.3).
	b := undoTestApp(t, 1)
	created := squarePolyAnn("q", 0)
	create := cmdCreate(b, 0, created)
	create.Do()
	b.images[0].Anns[0].Points[0] = Point{1, 2}
	if created.Points[0] != (Point{100, 100}) {
		t.Fatalf("the command's argument was mutated through the live annotation: %v", created.Points)
	}
	create.Undo()
	create.Do()
	if !sameAnn(b.images[0].Anns[0], created) {
		t.Fatalf("redo after an in-place edit = %+v, want %+v", b.images[0].Anns[0], created)
	}

	// A modify command's snapshots are copies too.
	before := cloneAnn(a.images[0].Anns[0])
	after := cloneAnn(before)
	after.Points = translatePolygon(after.Points, Point{X: 10, Y: 20})
	syncPolyBounds(&after)
	mod := cmdModify(a, 0, 0, before, after)
	mod.Do()
	// The live annotation is edited in place afterwards (a drag does this).
	a.images[0].Anns[0].Points[0] = Point{-100, -100}
	mod.Undo()
	if !sameAnn(a.images[0].Anns[0], before) {
		t.Fatalf("undo after an in-place edit = %+v, want %+v", a.images[0].Anns[0], before)
	}
	mod.Do()
	if !sameAnn(a.images[0].Anns[0], after) {
		t.Fatalf("redo = %+v, want %+v", a.images[0].Anns[0], after)
	}
	// The command's own `after` snapshot did not follow the live edit.
	if after.Points[0] == (Point{-100, -100}) {
		t.Fatal("the command's snapshot was mutated through the live annotation")
	}
}

func TestPolyDragCancelledRestoresSnapshot(t *testing.T) {
	a := newTestApp()
	a.images[0].Anns = []Ann{squarePolyAnn("p", 0)}
	a.selectOnly(0)
	before := cloneAnn(anns(a)[0])

	a.handleCanvasInput(ui.Element{}, polyDown(a, Point{100, 100}, 1, 0))
	a.handleCanvasInput(ui.Element{}, polyMove(a, Point{50, 50}, 0))
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyEscape))
	if !sameAnn(anns(a)[0], before) {
		t.Fatalf("after Escape = %+v, want %+v", anns(a)[0], before)
	}
	if len(a.undoStack) != 0 {
		t.Errorf("undo stack = %d after a cancelled drag, want 0", len(a.undoStack))
	}
	// The cancelled annotation does not alias the snapshot either.
	anns(a)[0].Points[0] = Point{7, 7}
	if sameAnn(anns(a)[0], before) {
		t.Fatal("the restored annotation aliases the drag snapshot")
	}
}

// --- persistence -----------------------------------------------------------

func TestPolySaveReopenRoundTrip(t *testing.T) {
	a := storeApp(t, "a.png")
	poly := newPolyAnn("ann_p", 2, []Point{{10, 20}, {110, 20}, {60, 90}})
	rect := Ann{ID: "ann_r", Cat: 1, Kind: KindRect, CX: 200, CY: 200, W: 30, H: 20, Angle: 0.3}
	seedAnns(t, a, 0, []Ann{poly, rect})

	b := reopen(t, a.dir)
	if len(b.images) != 1 {
		t.Fatalf("reopened %d images, want 1", len(b.images))
	}
	// The stored row carries the kind, the flat vertices and the bounding box
	// of those vertices (§2.1).
	recs, err := b.store.LoadImages()
	if err != nil {
		t.Fatalf("LoadImages: %v", err)
	}
	stored := recs[0].Anns[0]
	if stored.Kind != KindPoly {
		t.Errorf("stored kind = %q, want %q", stored.Kind, KindPoly)
	}
	if len(stored.Points) != 6 || stored.Points[0] != 10 || stored.Points[1] != 20 || stored.Points[5] != 90 {
		t.Errorf("stored points = %v, want the flattened vertices", stored.Points)
	}
	if stored.CX != 60 || stored.CY != 55 || stored.W != 100 || stored.H != 70 || stored.Angle != 0 {
		t.Errorf("stored bounds = (%v,%v %v×%v angle %v), want (60,55 100×70 angle 0)",
			stored.CX, stored.CY, stored.W, stored.H, stored.Angle)
	}

	got := b.images[0].Anns
	if len(got) != 2 {
		t.Fatalf("annotations = %d, want 2", len(got))
	}
	polyWantPoints(t, "poly after reopen", got[0], poly.Points)
	if got[0].Cat != 2 || got[0].ID != "ann_p" {
		t.Errorf("poly after reopen = %+v, want cat 2 id ann_p", got[0])
	}
	if got[1].Kind != KindRect || got[1].Angle != 0.3 || got[1].Points != nil {
		t.Errorf("rect after reopen = %+v, want a rect with angle 0.3 and no points", got[1])
	}
	if got[1].CX != 200 || got[1].W != 30 {
		t.Errorf("rect geometry after reopen = %+v", got[1])
	}
}

func TestPolyExportJSONCarriesKindAndPoints(t *testing.T) {
	a := storeApp(t, "a.png")
	poly := newPolyAnn("ann_p", 1, []Point{{10, 20}, {110, 20}, {60, 90}})
	seedAnns(t, a, 0, []Ann{poly, {ID: "ann_r", Cat: 0, Kind: KindRect, CX: 5, CY: 5, W: 10, H: 10}})

	if err := a.exportJSON(a.dir); err != nil {
		t.Fatalf("exportJSON: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(a.dir, docName))
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	text := string(raw)
	for _, want := range []string{`"kind": "poly"`, `"kind": "rect"`, `"points": [`} {
		if !strings.Contains(text, want) {
			t.Errorf("export lacks %s:\n%s", want, text)
		}
	}

	doc, err := loadDoc(a.dir)
	if err != nil || doc == nil {
		t.Fatalf("loadDoc: doc = %v err = %v", doc, err)
	}
	got := doc.Images[0].Anns
	if len(got) != 2 {
		t.Fatalf("exported annotations = %d, want 2", len(got))
	}
	if !sameAnn(got[0], poly) {
		t.Errorf("exported polygon = %+v, want %+v", got[0], poly)
	}
	if got[1].Kind != KindRect {
		t.Errorf("exported rect kind = %q, want %q", got[1].Kind, KindRect)
	}
}

func TestPolyLegacyDocumentWithoutKindIsRect(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"ir_version":"0.1-poc","categories":["x"],"images":[` +
		`{"file":"a.png","width":10,"height":10,"anns":[{"id":"ann_1","cat":0,"cx":5,"cy":5,"w":4,"h":4,"angle":0}]}]}`
	if err := os.WriteFile(filepath.Join(dir, docName), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := loadDoc(dir)
	if err != nil || doc == nil {
		t.Fatalf("loadDoc: doc = %v err = %v", doc, err)
	}
	an := doc.Images[0].Anns[0]
	if an.Kind != "" {
		t.Fatalf("legacy annotation kind = %q, want the empty key", an.Kind)
	}
	if isPoly(&an) {
		t.Error("a legacy annotation without a kind must not be a polygon")
	}
	rec := annsToRecs([]Ann{an})[0]
	if rec.Kind != KindRect || rec.Points != nil {
		t.Errorf("legacy rec = kind %q points %v, want %q/nil", rec.Kind, rec.Points, KindRect)
	}
	// And a stored row of it loads back as an explicit rectangle.
	back := annsFromRecs([]store.AnnRec{rec})
	if back[0].Kind != KindRect || back[0].Points != nil {
		t.Errorf("legacy round trip = %+v, want an explicit rect", back[0])
	}
}
