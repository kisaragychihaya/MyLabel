package app

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Visual regression: the frame's pixels, not just the state behind them
// (docs/phase5-design.md §4).
//
// Every expectation is a color the frame itself paints — a category's color
// from catColor, the theme's accent or surface — compared within visualTol per
// channel. The CPU rasterizer blends 8-bit sRGB linearly over whatever is under
// a paint (internal/raster blend), so a full-coverage paint equals the wanted
// color up to one unit of rounding, and an antialiased edge is that color at
// some coverage: the helpers below fit the coverage rather than requiring an
// exact pixel, which keeps the tests independent of where the canvas origin
// lands on the pixel grid. Text glyphs are never asserted — their shaping
// differs across machines — only the shapes and background blocks around them,
// and no golden file is involved.

// visualTol is the per-channel slack of every color comparison: the 8-bit
// rounding of one blend.
const visualTol = 2

// visualImageBG is what the scenarios' base image paints: markerImage is black
// everywhere but its white marker at (100,100), which the sample points avoid.
var visualImageBG = ui.RGB(0, 0, 0)

// visualRig returns the App every scenario starts from — one 800×600 marker
// image in one image row, the nine-category fixture, zoom 1 and no pan, so
// image pixel (x,y) is canvas DIP (x,y) — plus a Tester of its own showing the
// real App.View, and the theme the frames paint with. One Tester per test
// function: each leaks a thread (mygo issue #1902).
func visualRig(t *testing.T) (*App, *ui.Tester, *ui.Theme) {
	t.Helper()
	a := New()
	a.categories = defaultCategoryList()
	a.curCat = 0
	a.images = []ImageInfo{{Path: "v1", Name: "v1.png", Width: 800, Height: 600}}
	a.selImg, a.loaded = 0, 0
	a.bmp = markerImage()
	a.imgW, a.imgH = 800, 600
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.grid.Key = func(i int) any { return a.images[i].Path }
	a.grid.Selected = &a.selImg
	theme := ui.Theme{}
	tt := ui.NewTester(func(c *ui.Context) {
		theme = *c.Theme()
		a.View(c)
	}, testWinW, testWinH)
	// The scenarios sample image pixels no deeper than (750,560), which at zoom 1
	// and no pan must be on the canvas: a shrunk canvas (a toolbar that wraps, say)
	// would make them sample the theme's background instead of the picture.
	if cv := canvasBounds(t, tt); cv.W < 760 || cv.H < 570 {
		t.Fatalf("画布 %v 装不下 800×600 测试图片的 (0,0)-(750,560)，场景坐标失效", cv)
	}
	return a, tt, &theme
}

// over returns the color a paint of fg at opacity alpha leaves over bg, as the
// rasterizer computes it: a linear blend of the 8-bit channels, rounded.
func over(fg, bg ui.Color, alpha float64) ui.Color {
	mix := func(f, b uint8) uint8 {
		v := float64(f)*alpha + float64(b)*(1-alpha)
		return uint8(math.Round(clampFloat(v, 0, 255)))
	}
	return ui.RGB(mix(fg.R, bg.R), mix(fg.G, bg.G), mix(fg.B, bg.B))
}

// channelDiff is the largest per-channel difference between a frame pixel and
// a wanted color, in 8-bit units.
func channelDiff(px color.RGBA, want ui.Color) int {
	d := func(got, exp uint8) int {
		if got > exp {
			return int(got) - int(exp)
		}
		return int(exp) - int(got)
	}
	return max(d(px.R, want.R), d(px.G, want.G), d(px.B, want.B))
}

// paintFit returns the opacity at which fg over bg best matches px, and the
// largest channel error left there: 0 is a pixel of fg at exactly that
// coverage, 1..visualTol its rounding.
func paintFit(px color.RGBA, fg, bg ui.Color) (alpha float64, err int) {
	best := 256
	for i := 0; i <= 255; i++ {
		a := float64(i) / 255
		if e := channelDiff(px, over(fg, bg, a)); e < best {
			best, alpha = e, a
		}
	}
	return alpha, best
}

// bestPaint scans the pixels of r (inclusive: X..X+W and Y..Y+H) for the one
// painted most like fg over bg and reports the coverage it was painted at and
// the error left there. Nothing matching fg within visualTol reports coverage 0.
func bestPaint(frame *image.RGBA, r ui.Rect, fg, bg ui.Color) (alpha float64, err int) {
	err = 256
	for y := int(r.Y); y <= int(r.Y+r.H); y++ {
		for x := int(r.X); x <= int(r.X+r.W); x++ {
			a, e := paintFit(frame.RGBAAt(x, y), fg, bg)
			if e > visualTol {
				continue
			}
			if a > alpha {
				alpha, err = a, e
			}
		}
	}
	return alpha, err
}

// wantPaint fails unless r holds a pixel that is fg over bg within visualTol at
// a coverage of at least min: an antialiased outline leaves its own edge pixels
// at partial coverage, a filled shape whole ones.
func wantPaint(t *testing.T, what string, frame *image.RGBA, r ui.Rect, fg, bg ui.Color, min float64) {
	t.Helper()
	alpha, err := bestPaint(frame, r, fg, bg)
	switch {
	case err > visualTol:
		t.Errorf("%s：像素(%v,%v)-(%v,%v) 里没有 %v 与 %v 混合的像素（容差 %d）",
			what, r.X, r.Y, r.X+r.W, r.Y+r.H, fg, bg, visualTol)
	case alpha < min:
		t.Errorf("%s：像素(%v,%v)-(%v,%v) 里 %v 的最高覆盖率只有 %.2f，期望至少 %.2f",
			what, r.X, r.Y, r.X+r.W, r.Y+r.H, fg, alpha, min)
	}
}

// wantPlain fails unless every pixel of r is bg within visualTol: the place a
// shape would have painted is left alone.
func wantPlain(t *testing.T, what string, frame *image.RGBA, r ui.Rect, bg ui.Color) {
	t.Helper()
	for y := int(r.Y); y <= int(r.Y+r.H); y++ {
		for x := int(r.X); x <= int(r.X+r.W); x++ {
			got := frame.RGBAAt(x, y)
			if d := channelDiff(got, bg); d > visualTol {
				t.Fatalf("%s：像素(%d,%d) = %d,%d,%d，期望 %v（差 %d > %d）",
					what, x, y, got.R, got.G, got.B, bg, d, visualTol)
			}
		}
	}
}

// screenRect maps the image-pixel rectangle (x0,y0)-(x1,y1) to the frame pixels
// it covers, both edges included: the scan helpers walk X..X+W, Y..Y+H.
func screenRect(cv ui.Rect, a *App, x0, y0, x1, y1 float64) ui.Rect {
	sx0, sy0 := screenOf(cv, a, Point{x0, y0})
	sx1, sy1 := screenOf(cv, a, Point{x1, y1})
	left, top := math.Floor(float64(sx0)), math.Floor(float64(sy0))
	return ui.Rect{
		X: float32(left),
		Y: float32(top),
		W: float32(math.Ceil(float64(sx1)) - left),
		H: float32(math.Ceil(float64(sy1)) - top),
	}
}

// TestVisualRectSelected is §4 item 1: the selected rectangle's outline carries
// the category color, its eight resize handles paint white squares, and a part
// of the image without a handle stays untouched.
func TestVisualRectSelected(t *testing.T) {
	a, tt, _ := visualRig(t)
	box := rectAnn("r", 0, 400, 300, 200, 100) // image x 300..500, y 250..350
	a.images[0].Anns = []Ann{box}
	a.selectOnly(0)
	tt.Frame()

	cv := canvasBounds(t, tt)
	frame := tt.Image()
	col := a.catColor(0)
	white := ui.RGB(0xff, 0xff, 0xff)

	// Both the top and the left edge, sampled away from the handles and the
	// chip, are the category color at full strength.
	wantPaint(t, "矩形上边框", frame, screenRect(cv, a, 420, 246, 480, 250), col, visualImageBG, 0.5)
	wantPaint(t, "矩形左边框", frame, screenRect(cv, a, 297, 320, 301, 350), col, visualImageBG, 0.5)

	// The sole selection's translucent fill covers the box's inside.
	fill := over(col, visualImageBG, selFillAlpha)
	if n := countColor(frame, screenRect(cv, a, 380, 300, 420, 330), fill, visualTol); n < 800 {
		t.Errorf("选中的填充只有 %d 个像素 ≈ %v，期望实心填充", n, fill)
	}

	// Eight resize handles, one white square per handle point.
	hs := handlePoints(&box, a.zoom)
	half := float64(handleSizeDIP)/2 + 1
	for i := 0; i < 8; i++ {
		h := hs[i]
		wantPaint(t, fmt.Sprintf("第 %d 个缩放手柄 (%.0f,%.0f)", i, h.X, h.Y), frame,
			screenRect(cv, a, h.X-half, h.Y-half, h.X+half, h.Y+half), white, visualImageBG, 0.9)
	}

	// Below the box there is no handle at all.
	wantPlain(t, "框外的空画布", frame, screenRect(cv, a, 550, 420, 750, 560), visualImageBG)
}

// TestVisualObbRotated is §4 item 2: a box rotated 30° paints its outline on its
// own rotated corners and leaves the corners of its axis-aligned bounding box
// empty, so the frame is not a horizontal box of that size.
func TestVisualObbRotated(t *testing.T) {
	a, tt, _ := visualRig(t)
	obb := Ann{ID: "o", Cat: 1, Kind: KindRect, CX: 400, CY: 300, W: 200, H: 100, Angle: math.Pi / 6}
	a.images[0].Anns = []Ann{obb}
	a.clearSelection()
	a.showLabels = false // a chip would hang over the AABB's top-left corner
	tt.Frame()

	cv := canvasBounds(t, tt)
	frame := tt.Image()
	col := a.catColor(1)

	// The four corners the rotated box must paint, in image pixels, rotated here
	// with plain trigonometry rather than through corners(): the frame has to
	// agree with the geometry the model describes, not with itself.
	cos, sin := math.Cos(obb.Angle), math.Sin(obb.Angle)
	hw, hh := obb.W/2, obb.H/2
	for i, l := range [][2]float64{{-hw, -hh}, {hw, -hh}, {hw, hh}, {-hw, hh}} {
		q := Point{X: obb.CX + l[0]*cos - l[1]*sin, Y: obb.CY + l[0]*sin + l[1]*cos}
		wantPaint(t, fmt.Sprintf("旋转框第 %d 个顶点 (%.1f,%.1f)", i, q.X, q.Y), frame,
			screenRect(cv, a, q.X-2, q.Y-2, q.X+2, q.Y+2), col, visualImageBG, 0.5)
	}

	// The bounding box a horizontal 223×187 box would paint its outline on: the
	// rotated box's stroke is tens of pixels away from every one of these.
	bb := annBounds(&obb)
	for _, q := range []Point{
		{bb.MinX, bb.MinY}, {bb.MaxX, bb.MinY}, {bb.MaxX, bb.MaxY}, {bb.MinX, bb.MaxY},
		{(bb.MinX + bb.MaxX) / 2, bb.MinY},
	} {
		wantPlain(t, fmt.Sprintf("轴对齐包围盒上的 (%.1f,%.1f)", q.X, q.Y), frame,
			screenRect(cv, a, q.X-2, q.Y-2, q.X+2, q.Y+2), visualImageBG)
	}
}

// TestVisualPolygon is §4 item 3: a triangle is filled and outlined, its three
// edges carry the category color, and a sole selection's three vertex handles
// are painted.
func TestVisualPolygon(t *testing.T) {
	a, tt, _ := visualRig(t)
	tri := newPolyAnn("t", 2, []Point{{300, 150}, {550, 150}, {420, 380}})
	a.images[0].Anns = []Ann{tri}
	a.selectOnly(0)
	tt.Frame()

	cv := canvasBounds(t, tt)
	frame := tt.Image()
	col := a.catColor(2)

	// The fill: a point a few pixels inside each edge midpoint, toward the
	// centroid, is the category color at the selection's fill opacity.
	fill := over(col, visualImageBG, selFillAlpha)
	centroid := Point{X: 0, Y: 0}
	for _, p := range tri.Points {
		centroid.X += p.X / float64(len(tri.Points))
		centroid.Y += p.Y / float64(len(tri.Points))
	}
	edges := [][2]Point{
		{tri.Points[0], tri.Points[1]},
		{tri.Points[1], tri.Points[2]},
		{tri.Points[2], tri.Points[0]},
	}
	for i, e := range edges {
		mid := Point{X: (e[0].X + e[1].X) / 2, Y: (e[0].Y + e[1].Y) / 2}
		dx, dy := centroid.X-mid.X, centroid.Y-mid.Y
		d := math.Hypot(dx, dy)
		in := Point{X: mid.X + dx/d*6, Y: mid.Y + dy/d*6}
		if n := countColor(frame, screenRect(cv, a, in.X-1, in.Y-1, in.X+1, in.Y+1), fill, visualTol); n < 4 {
			t.Errorf("第 %d 条边内侧 (%v) 只有 %d 个像素 ≈ %v，期望多边形填充", i, in, n, fill)
		}
		// The edge itself, and the stroke over the fill, is the category color.
		wantPaint(t, fmt.Sprintf("第 %d 条边的描边", i), frame,
			screenRect(cv, a, mid.X-2, mid.Y-2, mid.X+2, mid.Y+2), col, visualImageBG, 0.5)
	}

	// A triangle's sole selection carries one vertex handle per vertex.
	white := ui.RGB(0xff, 0xff, 0xff)
	for i, v := range tri.Points {
		wantPaint(t, fmt.Sprintf("第 %d 个顶点手柄", i), frame,
			screenRect(cv, a, v.X-2, v.Y-2, v.X+2, v.Y+2), white, visualImageBG, 0.9)
	}

	// Above the top edge is outside the triangle: the image shows through, so the
	// shape is a triangle and not its bounding box.
	wantPlain(t, "三角形上边之外", frame, screenRect(cv, a, 400, 132, 480, 142), visualImageBG)
}

// TestVisualChips is §4 item 4: two annotations of different categories get a
// chip each, in their own category color, and switching the labels off leaves
// the same pixels as bare image.
func TestVisualChips(t *testing.T) {
	a, tt, _ := visualRig(t)
	a.images[0].Anns = []Ann{
		rectAnn("a", 0, 400, 200, 200, 100), // image x 300..500, y 150..250
		rectAnn("b", 3, 400, 450, 200, 100), // image x 300..500, y 400..500
	}
	a.clearSelection()
	tt.Frame()

	cv := canvasBounds(t, tt)
	frame := tt.Image()
	type chipSample struct {
		bg   ui.Rect // the chip's left padding: background, no glyphs
		want ui.Color
	}
	samples := make([]chipSample, 0, 2)
	for i := range a.images[0].Anns {
		an := &a.images[0].Anns[i]
		cs, ok := a.chipSizeFor(an.Cat)
		if !ok {
			t.Fatalf("第 %d 条标注的类别 %d 没有量出 chip 尺寸", i, an.Cat)
		}
		x, y := chipBounds(an, cs, a.zoom, Point{X: a.panX, Y: a.panY})
		// The chip's left padding, between its rounded corner and its text.
		pad := ui.Rect{
			X: cv.X + x + 1, Y: cv.Y + y + 3,
			W: 3, H: cs.h - 6,
		}
		want := over(a.catColor(an.Cat), visualImageBG, annChipAlpha)
		if n := countColor(frame, pad, want, visualTol); n < 20 {
			t.Errorf("第 %d 条标注的 chip 背景只有 %d 个像素 ≈ %v，期望类别色 %v 的底色",
				i, n, want, a.catColor(an.Cat))
		}
		samples = append(samples, chipSample{bg: pad, want: want})
	}
	// The two chips are told apart by their category colors.
	if samples[0].want == samples[1].want {
		t.Fatalf("两条不同类别的 chip 底色相同：%v", samples[0].want)
	}

	// Switching the labels off removes the chips: the same pixels are the bare
	// image, which is black above the boxes.
	a.toggleLabels()
	if a.showLabels {
		t.Fatal("切换后标签应关闭")
	}
	tt.Frame()
	frame = tt.Image()
	for i, s := range samples {
		if n := countColor(frame, s.bg, s.want, visualTol); n != 0 {
			t.Errorf("隐藏标签后第 %d 个 chip 位置仍有 %d 个底色像素", i, n)
		}
		wantPlain(t, fmt.Sprintf("隐藏标签后第 %d 个 chip 位置", i), frame, s.bg, visualImageBG)
	}
}

// TestVisualMarquee is §4 item 5: mid-drag the marquee paints its accent border
// and its translucent fill over the dragged region.
func TestVisualMarquee(t *testing.T) {
	a, tt, theme := visualRig(t)
	a.images[0].Anns = []Ann{
		rectAnn("a", 0, 400, 200, 100, 100),
		rectAnn("b", 1, 500, 400, 100, 100),
	}
	a.clearSelection()
	tt.Frame()

	// The Tester sends no modifier keys with pointer events, so the Shift drag
	// goes through the canvas handler, as the other tests do.
	const shiftFill = 0.12 // drawMarquee's fill opacity
	a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputPointerDown, X: 150, Y: 150, Button: 0, Mods: ui.Shift})
	a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputPointerMove, X: 600, Y: 450, Mods: ui.Shift})
	if a.mode != ModeMarquee {
		t.Fatalf("Shift+拖拽后 mode = %v，期望框选中", a.mode)
	}
	tt.Frame()

	cv := canvasBounds(t, tt)
	frame := tt.Image()
	accent := theme.Accent
	fill := over(accent, visualImageBG, shiftFill)

	// The dragged region (150,150)-(600,450), sampled clear of the two boxes.
	for _, p := range []Point{{200, 300}, {300, 350}, {520, 200}} {
		if n := countColor(frame, screenRect(cv, a, p.X-2, p.Y-2, p.X+2, p.Y+2), fill, visualTol); n < 16 {
			t.Errorf("框选区域内 (%v) 只有 %d 个像素 ≈ %v，期望半透明填充", p, n, fill)
		}
	}
	// The border: the left edge at image x=150, the top edge at image y=150.
	wantPaint(t, "框选左边框", frame, screenRect(cv, a, 148, 280, 152, 320), accent, visualImageBG, 0.5)
	wantPaint(t, "框选上边框", frame, screenRect(cv, a, 250, 148, 350, 152), accent, visualImageBG, 0.5)
	// Outside the marquee neither the fill nor the border is painted.
	wantPlain(t, "框选区域之外", frame, screenRect(cv, a, 200, 60, 400, 120), visualImageBG)
}

// TestVisualEmptyState is §4 item 6: with no folder open the initial frame
// renders without panicking, the canvas shows the theme's background behind its
// hint and the status bar is a plain surface strip.
func TestVisualEmptyState(t *testing.T) {
	a := New()
	theme := ui.Theme{}
	tt := ui.NewTester(func(c *ui.Context) {
		theme = *c.Theme()
		a.View(c)
	}, testWinW, testWinH)
	frame := tt.Image() // the first frames: an empty workspace must draw them
	if frame == nil {
		t.Fatal("空状态的初始帧没有画出任何东西")
	}
	if img := a.curImage(); img != nil {
		t.Fatalf("空白 App 却有当前图片 %+v", img)
	}

	cv := canvasBounds(t, tt)
	// The canvas is the theme's background; its hint text sits in the top-left
	// corner, so sample the middle.
	if n := countColor(frame, ui.Rect{X: cv.X + cv.W/2 - 20, Y: cv.Y + cv.H/2 - 20, W: 40, H: 40}, theme.Background, visualTol); n < 1681 {
		t.Errorf("空画布中央只有 %d 个背景色像素，期望整块 %v", n, theme.Background)
	}
	// The status bar: the bottom strip, sampled right of any text.
	wantPlain(t, "状态栏", frame, ui.Rect{
		X: testWinW - 60, Y: testWinH - 8,
		W: 59, H: 7,
	}, theme.Surface)
}
