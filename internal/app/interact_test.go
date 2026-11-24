package app

import (
	"image"
	"image/color"
	"math"
	"path/filepath"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"mylabel/locales"
)

// testImage returns a w×h bitmap with a gradient pattern, built in memory so
// the tests need no files.
func testImage(w, h int) *ui.Bitmap {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0x40, A: 0xff})
		}
	}
	return ui.NewBitmap(img)
}

const testWinW, testWinH = 1280, 800

// The Tester leaks a thread per instance (mygo issue #1902), so the whole
// package shares one and swaps the App behind it.
var (
	sharedApp    *App
	sharedTester *ui.Tester
)

// sharedView renders sharedApp the way App.View does, with a plain 48 DIP bar
// in place of the toolbar. The bar makes the canvas's origin non-zero, which
// is what makes the canvas-local mapping (design §4) testable; the toolbar's
// buttons and its dialog are not what these tests exercise.
func sharedView(c *ui.Context) {
	a := sharedApp
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Box(c).FillWidth().Height(48).Background(t.Surface)
		a.canvasView(c)
		a.thumbStrip(c)
		a.statusBar(c)
	})
}

// newTestApp returns an App with one 800×600 in-memory image loaded and the
// nine-category fixture list: a real workspace starts empty, and the tests
// below create annotations, so they inject the categories they assume.
func newTestApp() *App {
	a := New()
	a.categories = defaultCategoryList()
	a.curCat = 0
	a.images = []ImageInfo{{Path: "t1", Name: "t1.png", Width: 800, Height: 600}}
	a.loaded = 0
	a.bmp = testImage(800, 600)
	a.imgW, a.imgH = 800, 600
	a.grid.Key = func(i int) any { return a.images[i].Path }
	a.grid.Selected = &a.selImg
	return a
}

// tester returns a fresh App shown in the shared Tester. The tester is built
// once and re-framed for each App, so every test starts from idle state.
func tester(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	a := newTestApp()
	sharedApp = a
	if sharedTester == nil {
		sharedTester = ui.NewTester(sharedView, testWinW, testWinH)
	} else {
		sharedTester.Frame()
	}
	return a, sharedTester
}

// canvasBounds returns the canvas box in window DIPs.
func canvasBounds(t *testing.T, tt *ui.Tester) ui.Rect {
	t.Helper()
	r, ok := tt.Find(locales.HintCanvasName())
	if !ok || r.W <= 0 || r.H <= 0 {
		t.Fatalf("canvas %q not found: ok=%v rect=%+v texts=%v", locales.HintCanvasName(), ok, r, tt.Texts())
	}
	return r
}

// localOf maps image pixels to canvas-local DIPs: local = pan + image*zoom.
func localOf(a *App, p Point) (float32, float32) {
	z := safeZoom(a.zoom)
	return float32(a.panX + p.X*z), float32(a.panY + p.Y*z)
}

// screenOf maps image pixels to the Tester's window DIPs.
func screenOf(cv ui.Rect, a *App, p Point) (float32, float32) {
	lx, ly := localOf(a, p)
	return cv.X + lx, cv.Y + ly
}

// dragImage presses at from, moves through the midpoint to to and releases
// there, in image pixels.
func dragImage(t *testing.T, a *App, tt *ui.Tester, from, to Point) {
	t.Helper()
	cv := canvasBounds(t, tt)
	x0, y0 := screenOf(cv, a, from)
	x1, y1 := screenOf(cv, a, to)
	if cv.W <= 0 || cv.H <= 0 || x0 < cv.X || y0 < cv.Y || x1 > cv.X+cv.W || y1 > cv.Y+cv.H {
		t.Fatalf("drag %v->%v leaves the canvas %+v", from, to, cv)
	}
	tt.Press(x0, y0)
	tt.Move((x0+x1)/2, (y0+y1)/2)
	tt.Move(x1, y1)
	tt.Release(x1, y1)
}

// drawRect drags out a rectangle with the rectangle tool. Creating belongs to the
// creation tools alone — the pointer tool a fresh window starts with creates
// nothing (docs/phase6-design.md §6.1, §6.2) — so a test that draws names its
// tool; a test that then edits adds setTool(ToolSelect) and clicks the body.
func drawRect(t *testing.T, a *App, tt *ui.Tester, from, to Point) {
	t.Helper()
	a.setTool(ToolRect)
	dragImage(t, a, tt, from, to)
}

// clickImage clicks image point p without dragging.
func clickImage(t *testing.T, a *App, tt *ui.Tester, p Point) {
	t.Helper()
	cv := canvasBounds(t, tt)
	x, y := screenOf(cv, a, p)
	tt.Press(x, y)
	tt.Release(x, y)
}

func anns(a *App) []Ann {
	img := a.curImage()
	if img == nil {
		return nil
	}
	return img.Anns
}

func nearly(got, want, tol float64) bool { return math.Abs(got-want) <= tol }

// markerImage returns a 800×600 black bitmap with a white 10×10 blob at
// (100,100), to find image pixels in the rendered frame.
func markerImage() *ui.Bitmap {
	img := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 800; x++ {
			c := color.RGBA{A: 0xff}
			if x >= 100 && x < 110 && y >= 100 && y < 110 {
				c = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return ui.NewBitmap(img)
}

func whiteish(c color.RGBA) bool { return c.R >= 200 && c.G >= 200 && c.B >= 200 }
func blackish(c color.RGBA) bool { return c.R <= 60 && c.G <= 60 && c.B <= 60 }

// whiteImage returns a w×h all-white bitmap, unmistakable over the theme's
// dark canvas background.
func whiteImage(w, h int) *ui.Bitmap {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
		}
	}
	return ui.NewBitmap(img)
}

// TestDrawFollowsViewportMapping checks verification item #4 from the drawing
// side: the frame must put image pixel (x,y) exactly where the pointer
// mapping (pan + image*zoom, plus the canvas origin) says it is, so what the
// user sees and what a click selects cannot disagree.
func TestDrawFollowsViewportMapping(t *testing.T) {
	for _, tc := range []struct {
		name string
		zoom float64
		pan  Point
	}{
		{name: "zoom1", zoom: 1},
		{name: "zoom2", zoom: 2, pan: Point{20, 10}},
		{name: "zoomHalf", zoom: 0.5, pan: Point{300, 200}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, tt := tester(t)
			a.bmp = markerImage()
			a.zoom, a.panX, a.panY = tc.zoom, tc.pan.X, tc.pan.Y
			tt.Frame()

			cv := canvasBounds(t, tt)
			frame := tt.Image()
			at := func(p Point) color.RGBA {
				x, y := screenOf(cv, a, p)
				return frame.RGBAAt(int(math.Round(float64(x))), int(math.Round(float64(y))))
			}
			if got := at(Point{105, 105}); !whiteish(got) {
				t.Errorf("frame at image (105,105) = %v, want the white marker", got)
			}
			if got := at(Point{300, 200}); !blackish(got) {
				t.Errorf("frame at image (300,200) = %v, want black image content", got)
			}
		})
	}
}

// TestAnnAndHandlesAreDrawn checks that the annotation layer paints the
// selected annotation's translucent fill and that the handles layer paints a
// handle at the annotation's corner, in screen space.
func TestAnnAndHandlesAreDrawn(t *testing.T) {
	a, tt := tester(t)
	a.bmp = markerImage()
	tt.Frame()
	drawRect(t, a, tt, Point{150, 150}, Point{350, 300})
	// The pointer tool is what selects an annotation, and the handles are only
	// drawn for a selection (docs/phase6-design.md §6.2).
	a.setTool(ToolSelect)
	clickImage(t, a, tt, Point{250, 225})
	if a.soleSelection() != 0 {
		t.Fatalf("soleSelection = %d, want 0", a.soleSelection())
	}

	cv := canvasBounds(t, tt)
	frame := tt.Image()
	body := frame.RGBAAt(int(cv.X+250), int(cv.Y+225))
	if !(body.R > body.G && body.R > body.B && body.R > 20) {
		t.Errorf("annotation body = %v, want a translucent red fill over black", body)
	}
	handle := frame.RGBAAt(int(cv.X+150), int(cv.Y+150))
	if !whiteish(handle) {
		t.Errorf("top-left corner handle = %v, want a white handle square", handle)
	}
	// The outline is painted at full strength on the box's own edge, not only
	// the 18% fill inside it: a rotated-corner polyline must land on the
	// rectangle the drag created.
	edge := frame.RGBAAt(int(cv.X+250), int(cv.Y+150))
	if edge.R < body.R+60 {
		t.Errorf("top edge midpoint = %v, want the category color over the %v fill", edge, body)
	}
}

// TestAnnOutlineFollowsZoom checks that an annotation's outline is painted
// where the viewport says its image pixels are at a zoom other than 1: the
// outline is drawn in canvas DIP (pan + image*zoom), so a scale applied in the
// wrong order would move it off its own box.
func TestAnnOutlineFollowsZoom(t *testing.T) {
	a, tt := tester(t)
	a.images[0].Anns = []Ann{{ID: "a", Cat: 3, CX: 200, CY: 150, W: 100, H: 60}}
	a.clearSelection() // no fill, no tag: only the outline is painted
	a.zoom, a.panX, a.panY = 2, 10, 5
	tt.Frame()

	cv := canvasBounds(t, tt)
	frame := tt.Image()
	x, y := screenOf(cv, a, Point{200, 120}) // top edge midpoint in image px
	got := frame.RGBAAt(int(math.Round(float64(x))), int(math.Round(float64(y))))
	// The outline is drawn in category 3's own color: the generated palette
	// rotates a hue per index, so compare against the app's own lookup.
	want := a.catColor(3)
	if !(got.G != 0 || got.R != 0 || got.B != 0) || !closeColor(got, want, 40) {
		t.Errorf("outline at image (200,120) = %v, want category 3's color %v", got, want)
	}
}

// closeColor reports whether the painted color a matches b within tol per
// channel: the rasterizer blends the outline with what lies under it, so an
// exact match is not on offer.
func closeColor(a color.RGBA, b ui.Color, tol int) bool {
	d := func(x, y uint8) int {
		if x > y {
			return int(x) - int(y)
		}
		return int(y) - int(x)
	}
	return d(a.R, b.R) <= tol && d(a.G, b.G) <= tol && d(a.B, b.B) <= tol
}

// TestDragCreatesAnnotation is design §9 item 1: dragging on empty canvas
// creates one axis-aligned annotation with the geometry the pointer traced.
func TestDragCreatesAnnotation(t *testing.T) {
	for _, tc := range []struct {
		name string
		zoom float64
		pan  Point
		from Point
		to   Point
	}{
		{name: "zoom1", zoom: 1, pan: Point{}, from: Point{100, 100}, to: Point{300, 250}},
		// Verification item #4: the pointer round trip must hold with pan and
		// zoom combined.
		{name: "panZoom", zoom: 2.5, pan: Point{37.5, -12.25}, from: Point{20, 20}, to: Point{120, 80}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, tt := tester(t)
			a.zoom = tc.zoom
			a.panX, a.panY = tc.pan.X, tc.pan.Y
			tt.Frame()

			drawRect(t, a, tt, tc.from, tc.to)

			got := anns(a)
			if len(got) != 1 {
				t.Fatalf("annotations = %d, want 1", len(got))
			}
			an := got[0]
			wantW := math.Abs(tc.to.X - tc.from.X)
			wantH := math.Abs(tc.to.Y - tc.from.Y)
			wantCX := (tc.from.X + tc.to.X) / 2
			wantCY := (tc.from.Y + tc.to.Y) / 2
			if !nearly(an.W, wantW, 0.5) || !nearly(an.H, wantH, 0.5) {
				t.Errorf("size = %v×%v, want %v×%v", an.W, an.H, wantW, wantH)
			}
			if !nearly(an.CX, wantCX, 0.5) || !nearly(an.CY, wantCY, 0.5) {
				t.Errorf("center = (%v,%v), want (%v,%v)", an.CX, an.CY, wantCX, wantCY)
			}
			if an.Angle != 0 {
				t.Errorf("angle = %v, want 0", an.Angle)
			}
			if an.ID == "" {
				t.Error("annotation has no ID")
			}
			// A creation tool leaves its new annotation unselected: editing is
			// the pointer tool's (docs/phase6-design.md §6.2).
			if a.selectionCount() != 0 {
				t.Errorf("selectionCount = %d after creating, want 0", a.selectionCount())
			}

			a.undo()
			if n := len(anns(a)); n != 0 {
				t.Errorf("after undo annotations = %d, want 0", n)
			}
		})
	}
}

// TestSelectMoveUndo is design §9 item 2 (and part of item 5).
func TestSelectMoveUndo(t *testing.T) {
	a, tt := tester(t)
	drawRect(t, a, tt, Point{100, 100}, Point{300, 250})
	// The pointer tool is the editing half: select, move, undo (§6.2).
	a.setTool(ToolSelect)

	// Click empty canvas: deselect, no annotation created (a click, not a drag).
	clickImage(t, a, tt, Point{600, 450})
	if a.selectionCount() != 0 {
		t.Fatalf("selectionCount = %d after clicking empty canvas, want 0", a.selectionCount())
	}
	if n := len(anns(a)); n != 1 {
		t.Fatalf("annotations = %d after a click, want 1", n)
	}

	// Click the body: select.
	clickImage(t, a, tt, Point{200, 175})
	if a.soleSelection() != 0 {
		t.Fatalf("soleSelection = %d after clicking the body, want 0", a.soleSelection())
	}
	if a.mode != ModeIdle {
		t.Fatalf("mode = %v after release, want idle", a.mode)
	}

	before := anns(a)[0]
	if before.CX != 200 || before.CY != 175 {
		t.Fatalf("a plain click moved the annotation: %+v", before)
	}

	// Drag the body by (+50, -25).
	dragImage(t, a, tt, Point{200, 175}, Point{250, 150})
	moved := anns(a)[0]
	if !nearly(moved.CX, 250, 0.5) || !nearly(moved.CY, 150, 0.5) {
		t.Errorf("center = (%v,%v), want (250,150)", moved.CX, moved.CY)
	}
	if !nearly(moved.W, before.W, 0.01) || !nearly(moved.H, before.H, 0.01) || moved.Angle != before.Angle {
		t.Errorf("a move changed the size or angle: %+v -> %+v", before, moved)
	}

	a.undo()
	back := anns(a)[0]
	if !nearly(back.CX, 200, 1e-9) || !nearly(back.CY, 175, 1e-9) {
		t.Errorf("after undo center = (%v,%v), want (200,175)", back.CX, back.CY)
	}
}

// TestResizeAndRotate is design §9 item 3 (and part of item 5): corner handles
// resize the opposite corner fixed, the rotate handle sets the angle, and
// Shift snaps it to 15°.
func TestResizeAndRotate(t *testing.T) {
	a, tt := tester(t)
	drawRect(t, a, tt, Point{100, 100}, Point{300, 250})
	a.setTool(ToolSelect)
	clickImage(t, a, tt, Point{200, 175})
	an := anns(a)[0]
	if a.soleSelection() != 0 {
		t.Fatalf("soleSelection = %d, want 0", a.soleSelection())
	}

	// Drag the bottom-right corner from (300,250) to (350,320): the top-left
	// corner stays at (100,100), so W,H grow and the center moves.
	dragImage(t, a, tt, Point{an.CX + an.W/2, an.CY + an.H/2}, Point{350, 320})
	an = anns(a)[0]
	if !nearly(an.W, 250, 0.5) || !nearly(an.H, 220, 0.5) {
		t.Errorf("after corner drag size = %v×%v, want 250×220", an.W, an.H)
	}
	if !nearly(an.CX, 225, 0.5) || !nearly(an.CY, 210, 0.5) {
		t.Errorf("after corner drag center = (%v,%v), want (225,210)", an.CX, an.CY)
	}
	if tl := corners(&an)[0]; !nearly(tl.X, 100, 0.5) || !nearly(tl.Y, 100, 0.5) {
		t.Errorf("fixed corner moved to (%v,%v), want (100,100)", tl.X, tl.Y)
	}

	// The rotate handle hangs 24 DIP above the top edge midpoint.
	rotateAt := func(an Ann, angle, dist float64) Point {
		return Point{X: an.CX + dist*math.Sin(angle), Y: an.CY - dist*math.Cos(angle)}
	}
	dist := an.H/2 + handleRotateOffsetDIP/a.zoom
	from := rotateAt(an, an.Angle, dist)
	const want = 0.5 // radians
	to := rotateAt(an, want, 150)
	dragImage(t, a, tt, from, to)
	an = anns(a)[0]
	if !nearly(an.Angle, want, 0.01) {
		t.Errorf("angle = %v, want %v ±0.01", an.Angle, want)
	}

	// Shift snaps to 15° steps. The Tester sends no modifier keys with
	// pointer events, so the move and the release go to the canvas handler
	// directly; the Tester's Release then only lets the engine let go (a
	// move with no modifiers would otherwise recompute the angle unsnapped).
	dist = an.H/2 + handleRotateOffsetDIP/a.zoom
	cv := canvasBounds(t, tt)
	from = rotateAt(an, an.Angle, dist)
	to = rotateAt(an, 0.4, 150)
	fx, fy := screenOf(cv, a, from)
	tt.Press(fx, fy)
	lx, ly := localOf(a, to)
	a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputPointerMove, X: lx, Y: ly, Mods: ui.Shift})
	a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputPointerUp, X: lx, Y: ly, Button: 0})
	tx, ty := screenOf(cv, a, to)
	tt.Release(tx, ty)
	if got := anns(a)[0].Angle; !nearly(got, math.Pi/6, 1e-9) {
		t.Errorf("shift-snapped angle = %v, want %v (15°)", got, math.Pi/6)
	}
}

// TestDeleteUndo is design §9 item 5.
func TestDeleteUndo(t *testing.T) {
	a, tt := tester(t)
	drawRect(t, a, tt, Point{100, 100}, Point{300, 250})
	a.setTool(ToolSelect)
	clickImage(t, a, tt, Point{200, 175})
	if a.soleSelection() != 0 {
		t.Fatalf("soleSelection = %d, want 0", a.soleSelection())
	}

	tt.Key(0, ui.KeyDelete)
	if n := len(anns(a)); n != 0 {
		t.Fatalf("annotations = %d after Delete, want 0", n)
	}
	if a.selectionCount() != 0 {
		t.Errorf("selectionCount = %d after Delete, want 0", a.selectionCount())
	}

	a.undo()
	got := anns(a)
	if len(got) != 1 {
		t.Fatalf("annotations = %d after undo, want 1", len(got))
	}
	if !nearly(got[0].CX, 200, 1e-9) || !nearly(got[0].W, 200, 1e-9) {
		t.Errorf("restored annotation = %+v, want center x 200 and width 200", got[0])
	}
}

// TestCanvasClipKeepsImageInside is the regression test for the image painting
// over the toolbar: the canvas's own Draw callback must clip itself, since an
// element's Clip() only hides what its *children* draw.
func TestCanvasClipKeepsImageInside(t *testing.T) {
	a, tt := tester(t)
	a.bmp = whiteImage(1200, 900)
	a.imgW, a.imgH = 1200, 900
	a.zoom, a.panX, a.panY = 1, 0, 0
	tt.Frame()
	cv := canvasBounds(t, tt)
	base := tt.Image()

	// Pan the image up and left, as a wheel or a drag does: most of it now
	// wants to paint over the toolbar above the canvas and the thumbnail
	// strip below it.
	a.panX, a.panY = -300, -300
	tt.Frame()
	frame := tt.Image()

	barY := int(cv.Y) - 12 // inside the toolbar placeholder above the canvas
	if barY < 0 {
		t.Fatalf("canvas starts at y=%v, no toolbar above it", cv.Y)
	}
	for _, x := range []int{2, int(cv.W / 2), int(cv.W) - 3} {
		got, want := frame.RGBAAt(x, barY), base.RGBAAt(x, barY)
		if got != want {
			t.Errorf("toolbar pixel (%d,%d) = %v after panning, want the bar's %v: the image painted outside the canvas", x, barY, got, want)
		}
	}
	stripY := int(cv.Y+cv.H) + 20 // inside the thumbnail strip below it
	for _, x := range []int{int(cv.W) - 40, int(cv.W) - 300} {
		got, want := frame.RGBAAt(x, stripY), base.RGBAAt(x, stripY)
		if got != want {
			t.Errorf("thumbnail strip pixel (%d,%d) = %v after panning, want the strip's %v", x, stripY, got, want)
		}
	}

	// Not vacuous: the image still shows inside the canvas, where it is.
	if got := frame.RGBAAt(int(cv.X+cv.W/2), int(cv.Y+cv.H/2)); !whiteish(got) {
		t.Errorf("canvas center = %v, want the white image", got)
	}
}

// TestLoadFitsCanvas: a freshly loaded image is fitted and centered in the
// canvas on its first frame, and the user's own view survives later frames.
func TestLoadFitsCanvas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.png")
	writePNG(t, path, 400, 300)

	a, tt := tester(t)
	a.images = []ImageInfo{{Path: path, Name: "a.png"}}
	a.loaded = 0
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.loadImage(0)
	if !a.fitOnLoad {
		t.Fatalf("loadImage did not schedule a fit")
	}
	tt.Frame()

	cv := canvasBounds(t, tt)
	wantZoom := math.Min(float64(cv.W)/400, float64(cv.H)/300)
	if !nearly(a.zoom, wantZoom, 1e-5) {
		t.Errorf("zoom = %v after load, want %v (fit to the %vx%v canvas)", a.zoom, wantZoom, cv.W, cv.H)
	}
	wantX := (float64(cv.W) - 400*wantZoom) / 2
	wantY := (float64(cv.H) - 300*wantZoom) / 2
	if !nearly(a.panX, wantX, 1e-4) || !nearly(a.panY, wantY, 1e-4) {
		t.Errorf("pan = (%v,%v) after load, want (%v,%v) to center the image", a.panX, a.panY, wantX, wantY)
	}
	if a.fitOnLoad {
		t.Errorf("fitOnLoad still set after the frame drew the image")
	}

	// The image's own center lands on the canvas center.
	cx, cy := screenOf(cv, a, Point{200, 150})
	if !nearly(float64(cx), float64(cv.X+cv.W/2), 0.5) || !nearly(float64(cy), float64(cv.Y+cv.H/2), 0.5) {
		t.Errorf("image center drawn at (%v,%v), want the canvas center (%v,%v)", cx, cy, cv.X+cv.W/2, cv.Y+cv.H/2)
	}

	// A user's own zoom and pan are not reset by later frames.
	a.zoom, a.panX, a.panY = 3, 5, 6
	tt.Frame()
	tt.Frame()
	if a.zoom != 3 || a.panX != 5 || a.panY != 6 {
		t.Errorf("view = (%v,%v,%v) after two frames, want the user's (3,5,6)", a.zoom, a.panX, a.panY)
	}
}

// TestWheelPansContent checks the wheel's real semantics, as the Windows
// backend sends them (internal/windows/surface.go): a notch toward the user is
// WHEEL_DELTA -120, three lines of 33 DIP, DY = +99, and positive DY moves the
// view down the content, so the image moves up.
func TestWheelPansContent(t *testing.T) {
	a, tt := tester(t)
	a.bmp = markerImage()
	a.imgW, a.imgH = 800, 600
	a.zoom, a.panX, a.panY = 1, 0, 0
	tt.Frame()
	cv := canvasBounds(t, tt)
	x, y := cv.X+cv.W/2, cv.Y+cv.H/2

	tt.Wheel(x, y, 0, 99)
	if !nearly(a.panY, -99, 1e-4) {
		t.Fatalf("pan y = %v after a notch toward the user (DY=99), want -99: the content moves up", a.panY)
	}
	// The white blob at image (105,105) is a notch higher on screen, and the
	// pixel it left shows the black image.
	frame := tt.Image()
	bx, by := screenOf(cv, a, Point{105, 105})
	if got := frame.RGBAAt(int(math.Round(float64(bx))), int(math.Round(float64(by)))); !whiteish(got) {
		t.Errorf("image pixel (105,105) = %v after one notch down, want the blob a notch up at (%v,%v)", got, bx, by)
	}
	if got := frame.RGBAAt(int(cv.X+105), int(cv.Y+105)); whiteish(got) {
		t.Errorf("blob still at (%v,%v) after the wheel: the image did not move up", cv.X+105, cv.Y+105)
	}

	tt.Wheel(x, y, 0, -99)
	if !nearly(a.panY, 0, 1e-4) {
		t.Errorf("pan y = %v after a notch away from the user (DY=-99), want 0 back", a.panY)
	}

	// A tilt to the right (DX=+99) scrolls the view right: the content moves left.
	tt.Wheel(x, y, 99, 0)
	if !nearly(a.panX, -99, 1e-4) {
		t.Errorf("pan x = %v after a tilt right (DX=99), want -99: the content moves left", a.panX)
	}

	// A touchpad scrolls by pixels with the same signs (Precise).
	tt.Scroll(x, y, 0, 7)
	if !nearly(a.panY, -7, 1e-4) {
		t.Errorf("pan y = %v after a 7 DIP touchpad scroll, want -7", a.panY)
	}
}

// TestAppViewLayout renders the real App.View (toolbar, canvas, thumbnail
// strip, status bar) and checks that the strip keeps its 120 DIPs between the
// canvas and the status bar, with a cell for the image: the bottom of the
// window is not left to the canvas.
func TestAppViewLayout(t *testing.T) {
	_, tt := realViewTester(t)
	cv := canvasBounds(t, tt)

	stripTop := cv.Y + cv.H
	if stripTop < 0 || stripTop+120 > testWinH {
		t.Fatalf("canvas ends at %v, leaving no room for a 120 DIP strip in a %v DIP window", stripTop, testWinH)
	}
	for _, name := range []string{"v1.png", "v2.png"} {
		b, ok := tt.Find(name)
		if !ok {
			t.Errorf("no cell for %s in the strip; texts = %v", name, tt.Texts())
			continue
		}
		if b.Y < stripTop-1 || b.Y+b.H > stripTop+120 {
			t.Errorf("cell text %q at y=%v..%v, want it inside the strip %v..%v", name, b.Y, b.Y+b.H, stripTop, stripTop+120)
		}
	}
	if b, ok := tt.Find("图片 1/2"); !ok {
		t.Errorf("no status bar; texts = %v", tt.Texts())
	} else if b.Y < stripTop+120-1 || b.Y+b.H > testWinH {
		t.Errorf("status bar at y=%v..%v, want it below the strip, inside the %v DIP window", b.Y, b.Y+b.H, testWinH)
	}

	// v1's cell draws its thumbnail: the cell paints pixels with the test
	// image's blue (0x40), which the theme's neutral surfaces never have.
	frame := tt.Image()
	thumb := false
	for y := int(stripTop) + 8; y < int(stripTop)+80 && !thumb; y++ {
		for x := 8; x < 96; x++ {
			if frame.RGBAAt(x, y).B == 0x40 {
				thumb = true
				break
			}
		}
	}
	if !thumb {
		t.Errorf("the v1 cell shows no thumbnail pixels (test image blue 0x40)")
	}
}

// realViewTester renders the real App.View in a Tester of its own (the shared
// one is bound to sharedView), built once for the package's tests.
var (
	realViewOnce   sync.Once
	realViewTest   *ui.Tester
	realViewAppRef *App
)

func realViewTester(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	realViewOnce.Do(func() {
		a := New()
		a.images = []ImageInfo{
			{Path: "v1", Name: "v1.png", Width: 40, Height: 30},
			{Path: "v2", Name: "v2.png", Width: 40, Height: 30}, // no thumbnail yet
		}
		a.loaded = 0
		a.selImg = 0
		a.bmp = testImage(40, 30)
		a.imgW, a.imgH = 40, 30
		a.thumbs["v1"] = testImage(8, 8) // as if the worker had finished
		a.grid.Key = func(i int) any { return a.images[i].Path }
		a.grid.Selected = &a.selImg
		realViewAppRef = a
		realViewTest = ui.NewTester(a.View, testWinW, testWinH)
	})
	realViewTest.Frame()
	return realViewAppRef, realViewTest
}

// TestCtrlScrollAnchorsCursor is verification item #4: zooming at the cursor
// keeps the image point under it fixed.
func TestCtrlScrollAnchorsImageCenter(t *testing.T) {
	a, tt := tester(t)
	cv := canvasBounds(t, tt)

	// Scroll from somewhere off the image center: the anchor must be the
	// image's center, not the pointer position (product decision).
	x, y := cv.X+cv.W/4, cv.Y+cv.H/5
	centerBefore := a.imageCenterScreen()
	zoomBefore := a.zoom

	// The Tester sends no modifier keys with scroll events, so Ctrl+scroll
	// goes to the canvas handler directly (a real wheel event carries Mods).
	// DY=-99 is one notch away from the user, as Windows sends it.
	a.handleCanvasInput(ui.Element{}, ui.InputEvent{
		Kind: ui.InputScroll, X: x - cv.X, Y: y - cv.Y, DY: -99, Mods: ui.Ctrl,
	})

	if a.zoom <= zoomBefore {
		t.Fatalf("zoom = %v after scrolling up with Ctrl, want > %v", a.zoom, zoomBefore)
	}
	if want := zoomBefore * math.Pow(1.0015, 99); !nearly(a.zoom, want, 1e-6) {
		t.Errorf("zoom = %v after one notch up with Ctrl, want %v", a.zoom, want)
	}
	if c := a.imageCenterScreen(); !nearly(c.X, centerBefore.X, 1e-6) || !nearly(c.Y, centerBefore.Y, 1e-6) {
		t.Errorf("image center moved from %+v to %+v on Ctrl+scroll", centerBefore, c)
	}

	// Ctrl+scroll down zooms back out; plain scroll pans the content the
	// other way than the wheel (see TestWheelPansContent).
	zoomIn := a.zoom
	a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputScroll, X: x - cv.X, Y: y - cv.Y, DY: 99, Mods: ui.Ctrl})
	if a.zoom >= zoomIn {
		t.Errorf("zoom = %v after scrolling down with Ctrl, want < %v", a.zoom, zoomIn)
	}
	if c := a.imageCenterScreen(); !nearly(c.X, centerBefore.X, 1e-6) || !nearly(c.Y, centerBefore.Y, 1e-6) {
		t.Errorf("image center moved from %+v to %+v on Ctrl+scroll down", centerBefore, c)
	}
	panX, panY := a.panX, a.panY
	a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputScroll, X: x - cv.X, Y: y - cv.Y, DX: 25, DY: -10})
	if !nearly(a.panX, panX-25, 1e-9) || !nearly(a.panY, panY+10, 1e-9) {
		t.Errorf("pan = (%v,%v) after plain scroll of (25,-10), want (%v,%v)", a.panX, a.panY, panX-25, panY+10)
	}
}

// TestEscapeCancelsCreate is design §9 item 6.
func TestEscapeCancelsCreate(t *testing.T) {
	a, tt := tester(t)
	a.setTool(ToolRect)
	cv := canvasBounds(t, tt)
	x0, y0 := screenOf(cv, a, Point{100, 100})
	x1, y1 := screenOf(cv, a, Point{300, 250})

	tt.Press(x0, y0)
	tt.Move(x1, y1)
	if a.mode != ModeCreating {
		t.Fatalf("mode = %v while dragging on empty canvas, want creating", a.mode)
	}
	tt.Key(0, ui.KeyEscape)
	if a.mode != ModeIdle {
		t.Fatalf("mode = %v after Escape, want idle", a.mode)
	}
	tt.Release(x1, y1)

	if n := len(anns(a)); n != 0 {
		t.Fatalf("annotations = %d after Escape, want 0", n)
	}
	if len(a.undoStack) != 0 {
		t.Errorf("undo stack = %d entries, want 0", len(a.undoStack))
	}
}

// TestCategoryKeys is design §9 item 7.
func TestCategoryKeys(t *testing.T) {
	a, tt := tester(t)
	drawRect(t, a, tt, Point{100, 100}, Point{300, 250})
	a.setTool(ToolSelect)
	clickImage(t, a, tt, Point{200, 175})
	if a.soleSelection() != 0 {
		t.Fatalf("soleSelection = %d, want 0", a.soleSelection())
	}

	tt.Key(0, ui.Key3)
	if got := anns(a)[0].Cat; got != 2 {
		t.Errorf("selected annotation category = %d after key 3, want 2", got)
	}
	if a.curCat != 0 {
		t.Errorf("curCat = %d, want 0 (only the selected annotation changes)", a.curCat)
	}

	// No selection: the digit sets the category for the next annotation.
	clickImage(t, a, tt, Point{600, 450})
	if a.selectionCount() != 0 {
		t.Fatalf("selectionCount = %d, want 0", a.selectionCount())
	}
	tt.Key(0, ui.Key5)
	if a.curCat != 4 {
		t.Errorf("curCat = %d after key 5 with no selection, want 4", a.curCat)
	}
	drawRect(t, a, tt, Point{400, 300}, Point{500, 380})
	got := anns(a)
	if len(got) != 2 {
		t.Fatalf("annotations = %d, want 2", len(got))
	}
	if got[1].Cat != 4 {
		t.Errorf("new annotation category = %d, want 4", got[1].Cat)
	}
}

// TestSpaceDragPans: with Space held, a left-drag pans the image (the
// labelme-style hand tool) and never creates an annotation.
func TestSpaceDragPans(t *testing.T) {
	a, tt := tester(t)
	a.setTool(ToolRect)
	a.bmp = markerImage()
	a.imgW, a.imgH = 800, 600
	a.zoom, a.panX, a.panY = 1, 0, 0
	tt.Frame()
	cv := canvasBounds(t, tt)
	x, y := cv.X+cv.W/2, cv.Y+cv.H/2

	// Space down turns the left button into the hand tool.
	a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeySpace})
	if !a.spacePan {
		t.Fatal("spacePan not set by KeyDown Space")
	}
	tt.Press(x, y)
	tt.Move(x+40, y+25)
	tt.Release(x+40, y+25)
	if !nearly(a.panX, 40, 1e-4) || !nearly(a.panY, 25, 1e-4) {
		t.Errorf("pan = (%v,%v) after a space-drag of (40,25), want (40,25)", a.panX, a.panY)
	}
	if n := len(a.curImage().Anns); n != 0 {
		t.Errorf("space-drag created %d annotations, want none", n)
	}

	// Space up: a left-drag on empty canvas creates an annotation again.
	a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeySpace})
	if a.spacePan {
		t.Fatal("spacePan still set after KeyUp Space")
	}
	tt.Press(x, y)
	tt.Move(x+30, y+20)
	tt.Release(x+30, y+20)
	if n := len(a.curImage().Anns); n != 1 {
		t.Errorf("plain drag after releasing space created %d annotations, want 1", n)
	}
}

// TestShiftWheelPansHorizontally: with Shift held, the vertical wheel pans
// the content horizontally (labelme-style).
func TestShiftWheelPansHorizontally(t *testing.T) {
	a, tt := tester(t)
	a.bmp = markerImage()
	a.imgW, a.imgH = 800, 600
	a.zoom, a.panX, a.panY = 1, 0, 0
	tt.Frame()
	cv := canvasBounds(t, tt)

	// The Tester cannot put modifiers on wheel events; call the handler as
	// the framework would (DY=99 is one notch toward the user on Windows).
	a.handleCanvasInput(ui.Element{}, ui.InputEvent{
		Kind: ui.InputScroll, X: cv.W / 2, Y: cv.H / 2, DY: 99, Mods: ui.Shift,
	})
	if !nearly(a.panX, -99, 1e-4) || !nearly(a.panY, 0, 1e-9) {
		t.Errorf("pan = (%v,%v) after shift-wheel DY=99, want (-99,0)", a.panX, a.panY)
	}
}
