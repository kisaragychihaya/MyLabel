package app

import (
	"math"

	"github.com/egoist/mygo/ui"
)

const (
	// canvasLabel is the canvas's accessible name; tests find the canvas's
	// box through it (ui.Tester.Find).
	canvasLabel = "标注画布"
	// canvasHint is shown while no image is loaded.
	canvasHint = "点击“打开文件夹”载入图片"
	// annStrokeDIP is the annotation outline width, in screen DIP.
	annStrokeDIP = 1.5
	// annSelectedStrokeDIP is the outline width of the selected annotation: a
	// polygon's outline is drawn bolder when selected (§4.5).
	annSelectedStrokeDIP = 2.5
	// annChipFontSizeDIP is the category chip's text size on screen, constant
	// whatever the zoom: the chip is painted in canvas DIP, like the handles.
	annChipFontSizeDIP = 11
	// annChipPadXDIP and annChipPadYDIP pad the chip's text from its edges.
	annChipPadXDIP = 5
	annChipPadYDIP = 2
	// annChipRadiusDIP rounds the chip's corners.
	annChipRadiusDIP = 3
	// annChipAlpha is the chip background's opacity: the category color stays
	// recognizable and the outline underneath shows through.
	annChipAlpha = 0.85
	// chipLabelLimit is how many annotations one image may hold and still get a
	// chip on every one. Past it only the selection carries a chip: text runs
	// the CPU rasterizer hot, and a wall of labels is unreadable anyway.
	chipLabelLimit = 500
	// handleSizeDIP is the side of a resize handle on screen.
	handleSizeDIP = 8
	// vertexSizeDIP is the side of a polygon vertex handle on screen (§4.3).
	vertexSizeDIP = 6
	// handleRotateRadiusDIP is the radius of the rotate handle on screen.
	handleRotateRadiusDIP = 5
	// selFillAlpha is the opacity of the selected annotation's fill.
	selFillAlpha = 0.18
	// polyCloseSnapDIP is how close to the first vertex a polygon draft's next
	// click must land to close the polygon, on screen (§4.2).
	polyCloseSnapDIP = 8
	// dblClickSlopDIP is how far the second press of a double-click may travel
	// and still open the selected annotation's properties, on screen (§5.1).
	dblClickSlopDIP = 4
)

// canvasView builds the annotation canvas: a single Box whose Draw callback
// renders the base image, all annotations, the rubber-band draft and the
// selection handles. All annotations are drawn in one Draw callback; never
// one Element per annotation.
func (a *App) canvasView(c *ui.Context) ui.Element {
	// The theme is captured here: a Draw callback gets no *ui.Context.
	t := c.Theme()
	bg, muted := t.Background, t.TextMuted

	cv := ui.Box(c).FillWidth().Grow(1).Clip().Label(canvasLabel)
	cv.Bind(&a.canvasRef)
	cv.Focusable()
	if a.spacePan {
		cv.Cursor(ui.CursorGrab)
	} else {
		cv.Cursor(ui.CursorCrosshair)
	}
	cv.Draw(func(p *ui.Painter, r ui.Rect) {
		if a.fitOnLoad {
			a.fitToCanvas(r)
		}
		// Clip in the callback, not with Element.Clip: that one hides what the
		// element's *children* draw (ui/paint.go pushes its clip after
		// p.draw(e)), while the image and the annotations are painted here, in
		// window coordinates. Without this, a panned or zoomed image paints
		// over the toolbar above the canvas and the strip below it.
		p.Clip(r, 0, func() {
			a.drawCanvas(p, r, bg, muted)
		})
	})
	cv.HandleInput(func(ev ui.InputEvent) bool { return a.handleCanvasInput(cv, ev) })
	if a.focusCanvas {
		a.canvasRef.Focus(c)
		a.focusCanvas = false
	}
	return cv
}

// fitToCanvas fits the image into the canvas box r and centers it: zoom is the
// smaller of the two ratios, so a small image may be blown up (up to maxZoom),
// and pan puts the image's center on the canvas's center. A box with no size
// yet leaves fitOnLoad for a real frame.
func (a *App) fitToCanvas(r ui.Rect) {
	if a.imgW <= 0 || a.imgH <= 0 || r.W <= 0 || r.H <= 0 {
		return
	}
	a.zoom = clampFloat(math.Min(float64(r.W)/a.imgW, float64(r.H)/a.imgH), minZoom, maxZoom)
	a.panX = (float64(r.W) - a.imgW*a.zoom) / 2
	a.panY = (float64(r.H) - a.imgH*a.zoom) / 2
	a.fitOnLoad = false
}

// drawCanvas paints the background, the image, the annotations and the
// handles, all in window coordinates.
func (a *App) drawCanvas(p *ui.Painter, r ui.Rect, bg, muted ui.Color) {
	p.Fill(r, bg, 0)
	if a.bmp == nil && a.tiled == nil {
		p.Text(r.X+16, r.Y+16, canvasHint, 14, muted)
		return
	}

	// Image pixels -> the window: scale by zoom, then add pan (canvas DIP),
	// then the canvas origin. Scaling.Then(Translation) applies the scale
	// first, so pan stays in screen DIP and the pointer maps back with
	// (local-pan)/zoom (design §3, §4).
	z := safeZoom(a.zoom)
	view := ui.Scaling(float32(z), float32(z)).
		Then(ui.Translation(float32(a.panX)+r.X, float32(a.panY)+r.Y))
	p.Transform(view, 0, 0, func() { a.drawImage(p) })

	// Annotations and handles are painted in the canvas's own DIPs under a
	// plain translation, so they keep their stroke and handle sizes at any
	// zoom, and no annotation op carries a scale or a rotation: the CPU
	// rasterizer charges a scaled or rotated op around 20× the same shape as
	// a translated polyline (BenchmarkDrawFrame).
	p.Transform(ui.Translation(r.X, r.Y), 0, 0, func() { a.annLayer(p, r) })
}

// viewport maps image pixels to canvas-local DIP for drawing, and knows which
// part of the canvas is on screen (canvas-local, origin at 0,0).
//
// pts, path and colors are scratch buffers reused across frames (the canvas
// draws once per frame in one Draw callback), so drawing a thousand polygons
// allocates nothing and no annotation parses a category color.
type viewport struct {
	pan    Point
	zoom   float64
	view   ui.Rect
	pts    [][2]float32
	path   ui.Path
	colors []ui.Color
	// cats are the category currently in effect (the chip looks a name up by
	// index), and chipSizes caches each category's measured chip size for this
	// frame: many annotations share one category.
	cats      []Category
	chipSizes []chipSize
}

// chipSize is one category chip's measured size in canvas DIP.
type chipSize struct{ w, h float32 }

// chipSize returns category i's chip size, measuring its name on first use in
// the frame. ok is false for a category index beyond the list, which draws no
// chip at all.
func (v *viewport) chipSize(p *ui.Painter, i int) (chipSize, bool) {
	if i < 0 || i >= len(v.cats) || i >= len(v.chipSizes) {
		return chipSize{}, false
	}
	cs := &v.chipSizes[i]
	if cs.w == 0 && cs.h == 0 {
		tw, th := p.MeasureText(0, ui.Span{Text: v.cats[i].Name, Size: annChipFontSizeDIP})
		cs.w = tw + 2*annChipPadXDIP
		cs.h = th + 2*annChipPadYDIP
	}
	return *cs, true
}

// chipDraw says whether an annotation's category chip is painted this frame.
type chipDraw int

const (
	chipOff     chipDraw = iota // labels switched off
	chipSelOnly                 // too many annotations: only the selection
	chipAll                     // every annotation
)

// chipsFor decides the chip policy of a frame: labels off draws nothing, and
// an image above chipLabelLimit draws chips for the selection only (§ label
// guard, BenchmarkDrawFrame).
func chipsFor(show bool, count int) chipDraw {
	if !show {
		return chipOff
	}
	if count > chipLabelLimit {
		return chipSelOnly
	}
	return chipAll
}

// catColor returns the display color of category i from the buffer filled once
// per frame; an out-of-range category falls back to the first color, as the
// annotation's tag does.
func (v *viewport) catColor(i int) ui.Color {
	if i < 0 || i >= len(v.colors) {
		if len(v.colors) == 0 {
			return paletteColor(0)
		}
		return v.colors[0]
	}
	return v.colors[i]
}

// quad returns the annotation's 4 corners in canvas-local DIP (TL, TR, BR, BL)
// and whether any of it lands on the canvas.
func (v viewport) quad(an *Ann) (q [4][2]float32, onCanvas bool) {
	cs := corners(an)
	minX, minY := float32(math.MaxFloat32), float32(math.MaxFloat32)
	maxX, maxY := -minX, -minY
	for i, c := range cs {
		x := float32(v.pan.X + c.X*v.zoom)
		y := float32(v.pan.Y + c.Y*v.zoom)
		q[i] = [2]float32{x, y}
		minX, maxX = min(minX, x), max(maxX, x)
		minY, maxY = min(minY, y), max(maxY, y)
	}
	// The box's AABB is conservative: an annotation it misses cannot paint.
	onCanvas = maxX >= v.view.X && minX <= v.view.X+v.view.W &&
		maxY >= v.view.Y && minY <= v.view.Y+v.view.H
	return q, onCanvas
}

// polyPoints converts a polygon's vertices to canvas-local DIP in v.pts and
// returns them, along with whether any of the polygon lands on the canvas.
func (v *viewport) polyPoints(an *Ann) (pts [][2]float32, onCanvas bool) {
	pts = v.pts[:0]
	minX, minY := float32(math.MaxFloat32), float32(math.MaxFloat32)
	maxX, maxY := -minX, -minY
	for _, c := range an.Points {
		x := float32(v.pan.X + c.X*v.zoom)
		y := float32(v.pan.Y + c.Y*v.zoom)
		pts = append(pts, [2]float32{x, y})
		minX, maxX = min(minX, x), max(maxX, x)
		minY, maxY = min(minY, y), max(maxY, y)
	}
	v.pts = pts
	onCanvas = maxX >= v.view.X && minX <= v.view.X+v.view.W &&
		maxY >= v.view.Y && minY <= v.view.Y+v.view.H
	return pts, onCanvas
}

// annLayer draws every annotation of the current image, the rubber-band draft,
// the marquee selection rectangle and the selection handles, in canvas-local
// DIP.
func (a *App) annLayer(p *ui.Painter, r ui.Rect) {
	v := &viewport{
		pan:       Point{X: a.panX, Y: a.panY},
		zoom:      safeZoom(a.zoom),
		view:      ui.Rect{W: r.W, H: r.H},
		pts:       a.scratchPts,
		path:      a.scratchPath,
		colors:    a.catColors(),
		cats:      a.categories,
		chipSizes: a.resetChipSizes(),
	}
	// Every selected annotation is highlighted; only a sole selection carries
	// the fill and the handles (§5), so a Ctrl+A over a thousand annotations
	// stays as cheap as the plain frame.
	sole := a.soleSelection()
	if img := a.curImage(); img != nil {
		// One policy decision per frame: the chips are a per-annotation cost and
		// count is what decides it, not the selection.
		chips := chipsFor(a.showLabels, len(img.Anns))
		for i := range img.Anns {
			drawAnn(p, &img.Anns[i], a.isSelected(i), i == sole, v, chips)
		}
	}
	switch a.mode {
	case ModeCreating:
		// The rubber band has no committed category yet: no chip on it.
		drawAnn(p, &a.draft, false, false, v, chipOff)
	case ModePolyCreate:
		a.drawPolyDraft(p, v)
	case ModeMarquee:
		a.drawMarquee(p, v)
	}
	a.drawHandles(p)
	a.scratchPts, a.scratchPath = v.pts, v.path
}

// resetChipSizes clears the canvas's chip measurement buffer for a new frame:
// category names change between frames, so their measured widths do too.
func (a *App) resetChipSizes() []chipSize {
	if cap(a.scratchChipSizes) < len(a.categories) {
		a.scratchChipSizes = make([]chipSize, len(a.categories))
	} else {
		a.scratchChipSizes = a.scratchChipSizes[:len(a.categories)]
		clear(a.scratchChipSizes)
	}
	return a.scratchChipSizes
}

// drawAnn renders one annotation in canvas-local DIP, dispatching on its kind.// selected draws it as part of the selection (a bolder outline); sole marks the
// selection's only annotation, which alone gets the fill and the handles (§5).
// chips is the frame's chip policy; the chip itself is drawn for the selection
// only when the policy says so.
//
// A rectangle is painted from its rotated corner points, not as a rotated
// rectangle op under a pushed transform: on the CPU rasterizer a rotated
// stroke costs ~140 µs against ~10 µs for the same outline as a translated
// polyline, per annotation, which is 146 ms against 7 ms per frame at a
// thousand boxes (BenchmarkDrawFrame). Taking the corners from corners() also
// makes the painted shape and the shape hitTestAnns tests agree by
// construction.
func drawAnn(p *ui.Painter, an *Ann, selected, sole bool, v *viewport, chips chipDraw) {
	if isPoly(an) {
		drawPolyAnn(p, an, selected, sole, v, chips)
		return
	}
	if an.W <= 0 || an.H <= 0 {
		return
	}
	quad, onCanvas := v.quad(an)
	if !onCanvas && !selected {
		return
	}

	col := v.catColor(an.Cat)
	if sole {
		var path ui.Path
		path.MoveTo(quad[0][0], quad[0][1])
		for _, q := range quad[1:] {
			path.LineTo(q[0], q[1])
		}
		path.Close()
		p.FillPath(&path, ui.RGBA(col.R, col.G, col.B, selFillAlpha))
	}

	// Opaque polyline, closed by repeating the first corner: on a GPU each
	// segment draws as a rounded rectangle instead of a mask (StrokePolyline).
	width := float32(annStrokeDIP)
	if selected {
		width = annSelectedStrokeDIP
	}
	outline := [5][2]float32{quad[0], quad[1], quad[2], quad[3], quad[0]}
	p.StrokePolyline(outline[:], width, col)

	if chips == chipAll || chips == chipSelOnly && selected {
		left, top, _, _ := aabb(quad[:])
		drawChip(p, an, left, top, v)
	}
}

// drawPolyAnn renders one polygon annotation: a closed stroke in the category
// color, plus a translucent fill and a bolder outline when it is the sole
// selection (§4.5, §5). The whole polygon is one closed polyline and one fill
// path, never one op per edge.
func drawPolyAnn(p *ui.Painter, an *Ann, selected, sole bool, v *viewport, chips chipDraw) {
	if len(an.Points) < 2 {
		return
	}
	pts, onCanvas := v.polyPoints(an)
	if !onCanvas && !selected {
		return
	}
	if len(pts) < 2 {
		return
	}
	left, top, _, _ := aabb(pts)

	col := v.catColor(an.Cat)
	if sole && len(pts) >= minPolyVertices {
		v.path.Reset()
		v.path.MoveTo(pts[0][0], pts[0][1])
		for _, q := range pts[1:] {
			v.path.LineTo(q[0], q[1])
		}
		v.path.Close()
		p.FillPath(&v.path, ui.RGBA(col.R, col.G, col.B, selFillAlpha))
	}

	// Closed outline: repeat the first vertex so the closing edge is stroked
	// too (the polygon is implicitly closed everywhere else).
	v.pts = append(pts, pts[0])
	width := float32(annStrokeDIP)
	if selected {
		width = annSelectedStrokeDIP
	}
	p.StrokePolyline(v.pts, width, col)

	if chips == chipAll || chips == chipSelOnly && selected {
		drawChip(p, an, left, top, v)
	}
}

// aabb returns the bounds of canvas-local points.
func aabb(pts [][2]float32) (left, top, right, bottom float32) {
	left, top = float32(math.MaxFloat32), float32(math.MaxFloat32)
	right, bottom = -left, -top
	for _, q := range pts {
		left, right = min(left, q[0]), max(right, q[0])
		top, bottom = min(top, q[1]), max(bottom, q[1])
	}
	return left, top, right, bottom
}

// drawChip paints one annotation's category-name chip in canvas-local DIP: the
// category color as a rounded background and the name in whichever of black or
// white contrasts with it. The chip's bottom edge sits on the annotation's
// AABB top edge and flips inside the box when it would leave the canvas. A
// category index beyond the list draws nothing, so a stale annotation never
// panics the frame.
func drawChip(p *ui.Painter, an *Ann, left, top float32, v *viewport) {
	cs, ok := v.chipSize(p, an.Cat)
	if !ok {
		return
	}
	col := v.catColor(an.Cat)
	x, y := left, top-cs.h
	if y < v.view.Y {
		y = top
	}
	p.Fill(ui.Rect{X: x, Y: y, W: cs.w, H: cs.h}, ui.RGBA(col.R, col.G, col.B, annChipAlpha), annChipRadiusDIP)
	p.Text(x+annChipPadXDIP, y+annChipPadYDIP, v.cats[an.Cat].Name, annChipFontSizeDIP, contrastTextColor(col))
}

// contrastTextColor returns black or white, whichever reads on a background of
// color bg: the perceived luminance (ITU-R BT.601 weights) of the dark half
// takes white text, the bright half black.
func contrastTextColor(bg ui.Color) ui.Color {
	lum := (0.299*float64(bg.R) + 0.587*float64(bg.G) + 0.114*float64(bg.B)) / 255
	if lum < 0.5 {
		return ui.RGB(0xff, 0xff, 0xff)
	}
	return ui.RGB(0, 0, 0)
}

// drawPolyDraft renders the polygon being drawn: the committed vertices joined
// by a line, the rubber band from the last vertex to the pointer, a square per
// vertex and a ring on the first one that grows when a click would close the
// polygon there (§4.2).
func (a *App) drawPolyDraft(p *ui.Painter, v *viewport) {
	pts := a.polyDraft
	if len(pts) == 0 {
		return
	}
	accent := p.Theme().Accent
	z := safeZoom(a.zoom)
	at := func(q Point) (float32, float32) {
		return float32(a.panX + q.X*z), float32(a.panY + q.Y*z)
	}

	// Committed vertices plus the rubber band to the pointer: one polyline.
	line := make([][2]float32, 0, len(pts)+1)
	for _, q := range pts {
		x, y := at(q)
		line = append(line, [2]float32{x, y})
	}
	if a.hasHover {
		x, y := at(a.hover)
		line = append(line, [2]float32{x, y})
	}
	p.StrokePolyline(line, annStrokeDIP, accent)

	white := ui.RGB(0xff, 0xff, 0xff)
	half := float32(vertexSizeDIP) / 2
	for _, q := range pts {
		x, y := at(q)
		r := ui.Rect{X: x - half, Y: y - half, W: vertexSizeDIP, H: vertexSizeDIP}
		p.Fill(r, white, 0)
		p.Stroke(r, accent, 0, 1)
	}

	// The first vertex is the close target: a ring that grows when the draft
	// may be closed there. The obb draft has no close gesture — its fourth
	// click makes the box (§5.2) — so it draws no ring.
	if len(pts) >= minPolyVertices && a.tool != ToolObb {
		x, y := at(pts[0])
		r := float32(polyCloseSnapDIP) / 2
		if a.hasHover && a.nearDraftStart(a.hover) {
			r *= 1.6
		}
		var ring ui.Path
		ring.Circle(x, y, r)
		p.StrokePath(&ring, 2, accent)
	}
}

// drawMarquee renders the Shift+drag selection rectangle in canvas-local DIP:
// one translucent accent box, painted inside the same Draw callback as the
// annotations and clipped with them (§5).
func (a *App) drawMarquee(p *ui.Painter, v *viewport) {
	if !a.hasHover {
		return
	}
	x0 := float32(v.pan.X + a.drag.start.X*v.zoom)
	y0 := float32(v.pan.Y + a.drag.start.Y*v.zoom)
	x1 := float32(v.pan.X + a.hover.X*v.zoom)
	y1 := float32(v.pan.Y + a.hover.Y*v.zoom)
	r := ui.Rect{X: min(x0, x1), Y: min(y0, y1), W: max(x0, x1) - min(x0, x1), H: max(y0, y1) - min(y0, y1)}
	accent := p.Theme().Accent
	p.Fill(r, ui.RGBA(accent.R, accent.G, accent.B, 0.12), 0)
	p.Stroke(r, accent, 0, 1)
}

// drawImage renders the base image (whole bitmap or tiled) inside the
// current painter transform.
func (a *App) drawImage(p *ui.Painter) {
	whole := ui.Rect{W: float32(a.imgW), H: float32(a.imgH)}
	switch {
	case a.bmp != nil:
		p.Image(a.bmp, whole, ui.FillBox)
	case a.tiled == nil:
		return
	case a.zoom < 0.5 && a.tiled.Overview != nil:
		// Far out, one overview texture instead of the tile grid.
		p.Image(a.tiled.Overview, whole, ui.FillBox)
	default:
		a.tiled.Draw(p)
	}
}

// drawHandles renders the selected annotation's handles at a constant screen
// size, whatever the zoom. Coordinates are canvas-local DIPs:
// pan + imagePoint*zoom, like the annotations they belong to.
//
// Handles exist only for a sole selection (§5): the resize and rotate drags
// they start edit one annotation, and a polygon's vertex handles likewise.
// With several annotations selected there is nothing to drag a handle of, so
// none is drawn.
func (a *App) drawHandles(p *ui.Painter) {
	an := a.handlesAnn()
	if an == nil || a.bmp == nil && a.tiled == nil {
		return
	}
	accent := p.Theme().Accent
	white := ui.RGB(0xff, 0xff, 0xff)
	z := safeZoom(a.zoom)

	at := func(q Point) (float32, float32) {
		return float32(a.panX + q.X*z), float32(a.panY + q.Y*z)
	}

	// A polygon has vertex handles only: no resize handles, no rotate handle
	// (§4.3).
	if isPoly(an) {
		half := float32(vertexSizeDIP) / 2
		for _, q := range an.Points {
			x, y := at(q)
			r := ui.Rect{X: x - half, Y: y - half, W: vertexSizeDIP, H: vertexSizeDIP}
			p.Fill(r, white, 0)
			p.Stroke(r, accent, 0, 1)
		}
		return
	}

	hs := handlePoints(an, z)

	half := float32(handleSizeDIP) / 2
	for i := 0; i < 8; i++ {
		x, y := at(hs[i])
		r := ui.Rect{X: x - half, Y: y - half, W: handleSizeDIP, H: handleSizeDIP}
		p.Fill(r, white, 0)
		p.Stroke(r, accent, 0, 1)
	}

	// The rotate handle hangs off the top edge midpoint along the
	// annotation's own axis, so the connector may be slanted: a path, not
	// Painter.Line (which only draws horizontal or vertical lines).
	tx, ty := at(hs[4])
	rx, ry := at(hs[8])
	var stem ui.Path
	stem.MoveTo(tx, ty)
	stem.LineTo(rx, ry)
	p.StrokePath(&stem, 1.5, accent)

	var knob ui.Path
	knob.Circle(rx, ry, handleRotateRadiusDIP)
	p.FillPath(&knob, white)
	p.StrokePath(&knob, 1, accent)
}
