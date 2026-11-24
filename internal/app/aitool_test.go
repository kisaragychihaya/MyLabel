package app

import (
	"errors"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"

	"mylabel/internal/sam2"
	"mylabel/locales"
)

// Tests for the AI assist (docs/phase7-design.md §2, §3, §8): the strip button
// that only exists when the native library was detected, the point collection and
// the double-click that runs the inference, Esc, the stale-result guard, and the
// mask→annotation function on synthetic masks. No test needs the DLL: the client
// is created with sam2.New (which loads nothing) and every inference goes through
// the injected predictor.

// aiTestMask is a solid mw×mh mask rectangle covering mask cells
// [x0,x1]×[y0,y1] inclusive, in logits above the threshold.
func aiTestMask(mw, mh, x0, y0, x1, y1 int) []float32 {
	m := make([]float32, mw*mh)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			m[y*mw+x] = 1
		}
	}
	return m
}

// aiTestClient is a client that passes the "library was detected" gate without
// loading anything: New only records the config (sam2.New).
func aiTestClient() *sam2.Client {
	return sam2.New(sam2.Config{DLL: "sam2_ncnn.dll", EncoderParam: "e.param", DecoderParam: "d.param"})
}

// aiTestApp returns an App with the AI tool available and a predictor that always
// answers the same synthetic mask: an 8×6 grid whose foreground is the 4×4 block
// at (2,1)..(5,4). Over the fixture's 800×600 image each mask cell is 100 image
// pixels wide, so the block is the rectangle (250,150)..(550,450).
func aiTestApp() *App {
	a := newTestApp()
	a.sam = aiTestClient()
	a.aiPredict = func(img *ImageInfo, pts []Point) ([]float32, int, int, error) {
		return aiTestMask(8, 6, 2, 1, 5, 4), 8, 6, nil
	}
	return a
}

// TestAIToolHiddenWithoutLibrary: a machine without the native library keeps the
// seven-button strip, its S key does nothing, and the tool still counts as a
// creation tool for the state machine (docs/phase7-design.md §2, §8).
func TestAIToolHiddenWithoutLibrary(t *testing.T) {
	a, tt, _ := stripRig(t)
	if a.sam != nil {
		t.Fatalf("测试进程旁边没有 DLL，sam = %v，期望 nil", a.sam)
	}
	if a.aiReady() {
		t.Errorf("没有 DLL 时 aiReady() = true")
	}

	// The seven buttons are there, found by the names their locale messages give.
	names := []string{
		locales.ToolStripSelect(), locales.ToolStripPan(), locales.ToolStripZoom(),
		locales.ToolStripMarquee(), locales.ToolStripRect(), locales.ToolStripPoly(),
		locales.ToolStripObb(),
	}
	for _, name := range names {
		if r, ok := tt.Find(name); !ok || r.W <= 0 {
			t.Errorf("工具条按钮 %q 未找到：ok=%v rect=%+v", name, ok, r)
		}
	}
	if got := len(a.stripSpecs()); got != 7 {
		t.Errorf("工具条渲染 %d 个按钮，期望 7 个", got)
	}
	if _, ok := tt.Find(locales.ToolStripAI()); ok {
		t.Errorf("没有 DLL 时仍出现了 %q 按钮", locales.ToolStripAI())
	}

	// The tool is a creation tool even where it cannot be reached: the state
	// machine's rules (clear the selection, never edit what is under the press)
	// do not depend on the library being there.
	if !isCreationTool(ToolAI) {
		t.Errorf("isCreationTool(ToolAI) = false，期望 true")
	}

	// S is simply not a shortcut here: it must neither panic nor switch the tool.
	if keyWith(a, ui.KeyS, 0) {
		t.Errorf("没有 DLL 时 S 被处理了")
	}
	if a.tool != ToolSelect {
		t.Errorf("没有 DLL 时按 S 后 tool = %v，期望保持 ToolSelect", a.tool)
	}
}

// TestAIToolStripButton: with the library detected the strip grows an eighth
// button, in the creation group's end, and clicking it twice flips the output
// mode exactly as the zoom button flips its direction (docs/phase7-design.md §2).
func TestAIToolStripButton(t *testing.T) {
	a, tt, _ := stripRig(t)
	a.sam = aiTestClient()
	tt.Frame()

	r, ok := tt.Find(locales.ToolStripAI())
	if !ok || r.W <= 0 || r.H <= 0 {
		t.Fatalf("AI 按钮 %q 未找到：ok=%v rect=%+v texts=%v", locales.ToolStripAI(), ok, r, tt.Texts())
	}
	if r.W != toolButtonDIP || r.H != toolButtonDIP {
		t.Errorf("AI 按钮 %v×%v，期望 %d×%d", r.W, r.H, toolButtonDIP, toolButtonDIP)
	}
	if r.X < 0 || r.X+r.W > toolStripWidth {
		t.Errorf("AI 按钮在 x=%v，超出 %d DIP 宽的工具条", r.X, toolStripWidth)
	}
	// It joins the creation group: below the divider and below the rotated box.
	obb, ok := tt.Find(locales.ToolStripObb())
	if !ok || r.Y <= obb.Y+obb.H {
		t.Errorf("AI 按钮 y=%v 不在旋转框按钮（底边 %v）之下", r.Y, obb.Y+obb.H)
	}
	if got := len(a.stripSpecs()); got != 8 {
		t.Errorf("工具条渲染 %d 个按钮，期望 8 个", got)
	}

	// A click switches to it, a second one flips the shape, a third flips back.
	if err := tt.Click(locales.ToolStripAI()); err != nil {
		t.Fatalf("点击 %q：%v", locales.ToolStripAI(), err)
	}
	if a.tool != ToolAI || a.aiRectMode {
		t.Fatalf("点击后 tool = %v，aiRectMode = %v，期望 ToolAI 与 false", a.tool, a.aiRectMode)
	}
	if err := tt.Click(locales.ToolStripAI()); err != nil {
		t.Fatalf("再次点击 %q：%v", locales.ToolStripAI(), err)
	}
	if a.tool != ToolAI || !a.aiRectMode {
		t.Fatalf("再次点击后 tool = %v，aiRectMode = %v，期望 ToolAI 与 true", a.tool, a.aiRectMode)
	}
	if err := tt.Click(locales.ToolStripAI()); err != nil {
		t.Fatalf("第三次点击 %q：%v", locales.ToolStripAI(), err)
	}
	if a.aiRectMode {
		t.Errorf("第三次点击后 aiRectMode = true，期望换回多边形模式")
	}
	// Its icon follows the mode, as the zoom tool's does.
	var zero App
	if got := aiToolIcon(&zero); got != iconToolAIPoly {
		t.Errorf("多边形模式的图标是 %p，期望 %p（魔棒+火花）", got, iconToolAIPoly)
	}
	if got := aiToolIcon(&App{aiRectMode: true}); got != iconToolAIRect {
		t.Errorf("矩形模式的图标是 %p，期望 %p（魔棒+方框）", got, iconToolAIRect)
	}
	if iconToolAIPoly == iconToolAIRect {
		t.Errorf("两种模式的图标是同一个 SVG")
	}
	if got, want := aiToolTip(&zero), locales.ToolStripAI()+" · "+locales.PropsKindPolygon(); got != want {
		t.Errorf("多边形模式的 Tooltip = %q，期望 %q", got, want)
	}
	if got, want := aiToolTip(&App{aiRectMode: true}), locales.ToolStripAI()+" · "+locales.PropsKindRect(); got != want {
		t.Errorf("矩形模式的 Tooltip = %q，期望 %q", got, want)
	}
}

// TestAIToolFlow: S activates the tool, clicks collect foreground points, the
// second press of a double-click moves the last point and runs the inference, and
// the mask lands as one polygon annotation; a second run in rectangle mode gives
// the bounding box instead. Esc drops the points on the way (§2, §3).
func TestAIToolFlow(t *testing.T) {
	a := aiTestApp()
	if !keyWith(a, ui.KeyS, 0) || a.tool != ToolAI {
		t.Fatalf("S 后 tool = %v，期望 ToolAI", a.tool)
	}
	// A creation tool on the way in: the selection is dropped.
	if got := a.selectionCount(); got != 0 {
		t.Errorf("切入 AI 工具后仍选中 %d 条标注", got)
	}

	// One click collects one point, in image pixels.
	clickAt(a, Point{X: 400, Y: 300}, 1)
	if len(a.aiPoints) != 1 || !nearly(a.aiPoints[0].X, 400, 1e-9) || !nearly(a.aiPoints[0].Y, 300, 1e-9) {
		t.Fatalf("单击后 aiPoints = %v，期望一个 (400,300)", a.aiPoints)
	}
	// A second click collects a second one; nothing is inferred yet.
	clickAt(a, Point{X: 420, Y: 300}, 1)
	if len(a.aiPoints) != 2 {
		t.Fatalf("第二次单击后 aiPoints = %v，期望 2 个点", a.aiPoints)
	}
	if got := len(anns(a)); got != 0 {
		t.Fatalf("单击就产生了 %d 条标注", got)
	}

	// The second press of a double-click moves the last point and runs.
	clickAt(a, Point{X: 450, Y: 320}, 2)
	if len(a.aiPoints) != 0 {
		t.Errorf("推理后 aiPoints = %v，期望清空", a.aiPoints)
	}
	got := anns(a)
	if len(got) != 1 {
		t.Fatalf("推理后 %d 条标注，期望 1 条（状态栏 %q）", len(got), a.lastStatus)
	}
	poly := got[0]
	if !isPoly(&poly) {
		t.Fatalf("多边形模式产生了 %q 标注，期望 KindPoly", poly.Kind)
	}
	// The synthetic mask's 4×4 block, each cell 100 image pixels: the outline is
	// its four corners.
	wantCorners := []Point{{X: 250, Y: 150}, {X: 550, Y: 150}, {X: 550, Y: 450}, {X: 250, Y: 450}}
	if len(poly.Points) != len(wantCorners) {
		t.Fatalf("多边形有 %d 个顶点 %v，期望 4 个角", len(poly.Points), poly.Points)
	}
	for _, w := range wantCorners {
		if !hasPoint(poly.Points, w, 1e-9) {
			t.Errorf("多边形缺少顶点 %v，实际 %v", w, poly.Points)
		}
	}
	// The mirrored bounds follow the vertices, as for any polygon.
	if !nearly(poly.CX, 400, 1e-9) || !nearly(poly.CY, 300, 1e-9) ||
		!nearly(poly.W, 300, 1e-9) || !nearly(poly.H, 300, 1e-9) {
		t.Errorf("多边形外框 = (%v,%v,%v,%v)，期望 (400,300,300,300)", poly.CX, poly.CY, poly.W, poly.H)
	}
	// The new annotation is not selected, exactly as a creation tool leaves it.
	if got := a.selectionCount(); got != 0 {
		t.Errorf("生成后选中了 %d 条标注", got)
	}
	if want := locales.StatusAIDone(locales.PropsKindPolygon()); a.lastStatus != want {
		t.Errorf("状态栏 = %q，期望 %q", a.lastStatus, want)
	}

	// S on the active tool flips the output mode; the next run makes a box.
	if !keyWith(a, ui.KeyS, 0) || !a.aiRectMode {
		t.Fatalf("再按 S 后 aiRectMode = %v，期望 true", a.aiRectMode)
	}
	clickAt(a, Point{X: 400, Y: 300}, 1)
	clickAt(a, Point{X: 410, Y: 310}, 2)
	got = anns(a)
	if len(got) != 2 {
		t.Fatalf("矩形模式推理后 %d 条标注，期望 2 条（状态栏 %q）", len(got), a.lastStatus)
	}
	box := got[1]
	if annKind(&box) != KindRect {
		t.Fatalf("矩形模式产生了 %q 标注，期望 KindRect", box.Kind)
	}
	// The bounding box of the same mask: the outline's own corners.
	if !nearly(box.CX, 400, 1e-9) || !nearly(box.CY, 300, 1e-9) ||
		!nearly(box.W, 300, 1e-9) || !nearly(box.H, 300, 1e-9) {
		t.Errorf("矩形 = (%v,%v,%v,%v)，期望 (400,300,300,300)", box.CX, box.CY, box.W, box.H)
	}

	// Esc drops the collected points, and the run in flight with them.
	clickAt(a, Point{X: 100, Y: 100}, 1)
	if len(a.aiPoints) != 1 {
		t.Fatalf("Esc 前 aiPoints = %v，期望 1 个点", a.aiPoints)
	}
	gen := a.aiGen
	if !keyWith(a, ui.KeyEscape, 0) {
		t.Errorf("有点时 Esc 未被处理")
	}
	if len(a.aiPoints) != 0 {
		t.Errorf("Esc 后 aiPoints = %v，期望清空", a.aiPoints)
	}
	if a.aiGen <= gen {
		t.Errorf("Esc 后 aiGen = %d，期望大于 %d（丢弃在途结果）", a.aiGen, gen)
	}
	if got := len(anns(a)); got != 2 {
		t.Errorf("Esc 产生了标注：%d 条", got)
	}

	// Leaving the tool drops what it collected, as a draft is dropped.
	clickAt(a, Point{X: 120, Y: 120}, 1)
	a.setTool(ToolRect)
	if len(a.aiPoints) != 0 {
		t.Errorf("换工具后 aiPoints = %v，期望清空", a.aiPoints)
	}
}

// clickAt sends one left press of the AI tool at image point p, carrying the
// click count the double-click gesture needs (the Tester cannot set it).
func clickAt(a *App, p Point, clicks int) {
	a.handleCanvasInput(ui.Element{}, polyDown(a, p, clicks, 0))
	a.handleCanvasInput(ui.Element{}, polyUp(a, p))
}

// hasPoint reports whether pts holds p within tol on both axes.
func hasPoint(pts []Point, p Point, tol float64) bool {
	for _, q := range pts {
		if math.Abs(q.X-p.X) <= tol && math.Abs(q.Y-p.Y) <= tol {
			return true
		}
	}
	return false
}

// TestAIToolNeedsCategories: the AI tool is blocked by an empty category list,
// with the same message the three creation tools report (contract §3 of phase 2).
func TestAIToolNeedsCategories(t *testing.T) {
	a := aiTestApp()
	a.categories = nil
	a.curCat = -1
	a.setTool(ToolAI)

	clickAt(a, Point{X: 400, Y: 300}, 1)
	if len(a.aiPoints) != 0 {
		t.Errorf("空类别表下仍收集了 %v", a.aiPoints)
	}
	if want := locales.StatusNoCategories(); a.lastStatus != want {
		t.Errorf("状态栏 = %q，期望 %q", a.lastStatus, want)
	}
	if got := len(anns(a)); got != 0 {
		t.Errorf("空类别表下产生了 %d 条标注", got)
	}
}

// TestAIStaleResultDropped: a result whose generation or image no longer matches
// is dropped, and it does not leave the tool busy forever (docs/phase7-design.md
// §6). This is the landing half of the goroutine path; the trigger half runs
// inline because a test has no window.
func TestAIStaleResultDropped(t *testing.T) {
	a := aiTestApp()
	a.setTool(ToolAI)

	// A run was started for this image, then the user moved on: aiGenerate took
	// gen, aiDropPoints (Esc, or a thumbnail click) bumped it.
	gen := a.aiGen + 1
	a.aiGen = gen
	a.aiBusy = true
	res := aiRun{mask: aiTestMask(8, 6, 2, 1, 5, 4), mw: 8, mh: 6}
	a.aiDropPoints()

	a.aiFinish(res, gen, a.loaded, 0, "ann_stale", 800, 600)
	if got := len(anns(a)); got != 0 {
		t.Errorf("过期结果产生了 %d 条标注", got)
	}
	if a.aiBusy {
		t.Errorf("过期结果回来时仍在 busy")
	}

	// The same for a result that arrives after the image changed.
	a.aiBusy = true
	gen = a.aiGen
	a.images = append(a.images, ImageInfo{Path: "t2", Name: "t2.png", Width: 800, Height: 600})
	a.loaded = 1
	a.aiFinish(res, gen, 0, 0, "ann_other", 800, 600)
	if got := len(anns(a)); got != 0 {
		t.Errorf("换图后的过期结果产生了 %d 条标注", got)
	}
	if a.aiBusy {
		t.Errorf("换图后的过期结果回来时仍在 busy")
	}
}

// TestAIFailureReported: a failing predictor and an empty mask each report
// themselves on the status bar, and neither creates anything (§7).
func TestAIFailureReported(t *testing.T) {
	a := aiTestApp()
	a.setTool(ToolAI)

	a.aiPredict = func(img *ImageInfo, pts []Point) ([]float32, int, int, error) {
		return nil, 0, 0, errors.New("boom")
	}
	clickAt(a, Point{X: 10, Y: 10}, 1)
	clickAt(a, Point{X: 10, Y: 10}, 2)
	if want := locales.StatusAIFailed("boom"); a.lastStatus != want {
		t.Errorf("失败后状态栏 = %q，期望 %q", a.lastStatus, want)
	}
	if got := len(anns(a)); got != 0 {
		t.Errorf("失败后产生了 %d 条标注", got)
	}

	// An all-zero mask is "nothing found", not a failure.
	a.aiPredict = func(img *ImageInfo, pts []Point) ([]float32, int, int, error) {
		return make([]float32, 8*6), 8, 6, nil
	}
	clickAt(a, Point{X: 20, Y: 20}, 1)
	clickAt(a, Point{X: 20, Y: 20}, 2)
	if want := locales.StatusAINoMask(); a.lastStatus != want {
		t.Errorf("空 mask 后状态栏 = %q，期望 %q", a.lastStatus, want)
	}
	if got := len(anns(a)); got != 0 {
		t.Errorf("空 mask 产生了 %d 条标注", got)
	}
}

// TestBuildAIAnn: the mask→annotation function on synthetic masks (design §3, §8):
// both output modes, the threshold, the largest-component rule and the error a
// mask without foreground yields.
func TestBuildAIAnn(t *testing.T) {
	const mw, mh = 64, 48
	// A 64×48 mask with a box covering x 8..23, y 6..17: over a 640×480 image
	// every mask cell is 10×10 image pixels.
	box := aiTestMask(mw, mh, 8, 6, 23, 17)
	// The mask grid maps a cell's centre: (x+0.5)*imgW/mw.
	mapX := func(x int) float64 { return (float64(x) + 0.5) * 640 / mw }
	mapY := func(y int) float64 { return (float64(y) + 0.5) * 480 / mh }

	poly, err := buildAIAnn(box, mw, mh, 640, 480, false, 3, "p1")
	if err != nil {
		t.Fatalf("多边形模式：%v", err)
	}
	if !isPoly(&poly) || len(poly.Points) != 4 {
		t.Fatalf("多边形模式得到 kind=%q、%d 个顶点，期望 KindPoly 与 4 个角", poly.Kind, len(poly.Points))
	}
	if poly.ID != "p1" || poly.Cat != 3 {
		t.Errorf("多边形 ID/类别 = %q/%d，期望 p1/3", poly.ID, poly.Cat)
	}
	for _, want := range []Point{
		{X: mapX(8), Y: mapY(6)}, {X: mapX(23), Y: mapY(6)},
		{X: mapX(23), Y: mapY(17)}, {X: mapX(8), Y: mapY(17)},
	} {
		if !hasPoint(poly.Points, want, 1e-9) {
			t.Errorf("多边形缺少顶点 %v，实际 %v", want, poly.Points)
		}
	}

	rect, err := buildAIAnn(box, mw, mh, 640, 480, true, 3, "r1")
	if err != nil {
		t.Fatalf("矩形模式：%v", err)
	}
	if annKind(&rect) != KindRect {
		t.Fatalf("矩形模式得到 kind=%q，期望 KindRect", rect.Kind)
	}
	if !nearly(rect.CX, (mapX(8)+mapX(23))/2, 1e-9) ||
		!nearly(rect.CY, (mapY(6)+mapY(17))/2, 1e-9) ||
		!nearly(rect.W, mapX(23)-mapX(8), 1e-9) ||
		!nearly(rect.H, mapY(17)-mapY(6), 1e-9) {
		t.Errorf("矩形 = (%v,%v,%v,%v)，期望中心 (%v,%v) 尺寸 (%v,%v)",
			rect.CX, rect.CY, rect.W, rect.H,
			(mapX(8)+mapX(23))/2, (mapY(6)+mapY(17))/2, mapX(23)-mapX(8), mapY(17)-mapY(6))
	}
	// Both modes describe the same shape: the box is the outline's own bounds.
	if !nearly(poly.CX, rect.CX, 1e-9) || !nearly(poly.W, rect.W, 1e-9) || !nearly(poly.H, rect.H, 1e-9) {
		t.Errorf("多边形外框 (%v,%v,%v) 与矩形 (%v,%v,%v) 不一致", poly.CX, poly.W, poly.H, rect.CX, rect.W, rect.H)
	}

	// A mask cell holds a value exactly at the threshold: not foreground.
	edge := aiTestMask(mw, mh, 8, 6, 23, 17)
	for i := range edge {
		if edge[i] == 1 {
			edge[i] = 0 // the whole box is exactly at the threshold now
		}
	}
	if _, err := buildAIAnn(edge, mw, mh, 640, 480, true, 0, "e"); !errors.Is(err, errAINoMask) {
		t.Errorf("阈值上的 mask 得到 err=%v，期望 errAINoMask", err)
	}
	if _, err := buildAIAnn(edge, mw, mh, 640, 480, false, 0, "e"); !errors.Is(err, errAINoMask) {
		t.Errorf("阈值上的 mask（多边形）得到 err=%v，期望 errAINoMask", err)
	}

	// Two components: only the larger one survives, in both modes.
	two := aiTestMask(mw, mh, 2, 2, 5, 5)   // 4×4 = 16 cells
	two = union(two, aiTestMask(mw, mh, 40, 30, 49, 39)) // 10×10 = 100 cells
	big, err := buildAIAnn(two, mw, mh, 640, 480, true, 0, "b")
	if err != nil {
		t.Fatalf("两个连通域：%v", err)
	}
	if !nearly(big.CX, (mapX(40)+mapX(49))/2, 1e-9) || !nearly(big.CY, (mapY(30)+mapY(39))/2, 1e-9) {
		t.Errorf("最大连通域的外框中心 = (%v,%v)，期望 (%v,%v)",
			big.CX, big.CY, (mapX(40)+mapX(49))/2, (mapY(30)+mapY(39))/2)
	}

	// A circle: the outline is simplified, and every vertex stays on it (±1 cell).
	const mw2, mh2, cx, cy, r = 64, 48, 32.0, 24.0, 15.0
	circle := make([]float32, mw2*mh2)
	for y := range mh2 {
		for x := range mw2 {
			dx, dy := float64(x)-cx, float64(y)-cy
			if dx*dx+dy*dy <= r*r {
				circle[y*mw2+x] = 1
			}
		}
	}
	ring, err := buildAIAnn(circle, mw2, mh2, 640, 480, false, 0, "c")
	if err != nil {
		t.Fatalf("圆：%v", err)
	}
	if len(ring.Points) < 8 {
		t.Errorf("圆的轮廓只有 %d 个顶点，期望抽稀后仍是一条多边形", len(ring.Points))
	}
	for _, p := range ring.Points {
		// Back to mask cells, then to the distance from the centre in cells.
		mx := p.X*mw2/640 - 0.5
		my := p.Y*mh2/480 - 0.5
		if d := math.Hypot(mx-cx, my-cy); math.Abs(d-r) > 1.5 {
			t.Errorf("顶点 %v 距圆心 %v 个 mask 像素，期望 %v ±1.5", p, d, r)
		}
	}

	// A mask whose buffer does not match its size is an error, not a guess.
	if _, err := buildAIAnn(make([]float32, 3), 8, 6, 640, 480, false, 0, "x"); !errors.Is(err, errAINoMask) {
		t.Errorf("尺寸不符的 mask 得到 err=%v，期望 errAINoMask", err)
	}
}

// union returns m with every foreground cell of other set as well.
func union(m, other []float32) []float32 {
	out := append([]float32(nil), m...)
	for i, v := range other {
		if v > 0 && i < len(out) {
			out[i] = v
		}
	}
	return out
}

// TestAIDrawsPoints: the collected points are painted on the canvas as accent
// crosses, inside the canvas's single Draw callback (docs/phase7-design.md §7).
func TestAIDrawsPoints(t *testing.T) {
	a, tt, theme := stripRig(t)
	a.sam = aiTestClient()
	a.aiPoints = []Point{{X: 400, Y: 300}}
	tt.Frame()

	cv := canvasBounds(t, tt)
	frame := tt.Image()
	// The mark is 6 DIP long on each side of the point, whatever the zoom; the
	// samples stay inside that arm, off its antialiased end.
	arm := float64(aiCrossDIP - 2)
	ink := 0
	for _, d := range []Point{{X: 0, Y: 0}, {X: -arm, Y: 0}, {X: arm, Y: 0}, {X: 0, Y: -arm}, {X: 0, Y: arm}} {
		x, y := screenOf(cv, a, Point{X: 400 + d.X, Y: 300 + d.Y})
		if closeColor(atPixel(frame, x, y), theme.Accent, 60) {
			ink++
		}
	}
	if ink < 3 {
		t.Errorf("点 (400,300) 处的十字只有 %d/5 个采样点是强调色 %v", ink, theme.Accent)
	}
	// A frame without points paints nothing there: the image's own pixels show.
	a.aiPoints = nil
	tt.Frame()
	frame = tt.Image()
	x, y := screenOf(cv, a, Point{X: 404, Y: 300})
	if closeColor(atPixel(frame, x, y), theme.Accent, 60) {
		t.Errorf("清空点后十字仍在 (%v,%v)", x, y)
	}
}

// TestAIIconsPaint: both AI icons parse as 24×24 outlines with nothing the
// renderer would skip, and each paints visible ink inside its box (§7).
func TestAIIconsPaint(t *testing.T) {
	icons := map[string]*ui.SVG{"aiPoly": iconToolAIPoly, "aiRect": iconToolAIRect}
	if iconToolAIPoly == iconToolAIRect {
		t.Fatalf("两个 AI 图标是同一个 SVG")
	}
	for label, svg := range icons {
		if svg == nil {
			t.Fatalf("%s 没有图标", label)
		}
		if w, h := svg.Size(); w != 24 || h != 24 {
			t.Errorf("%s: 图标 %v×%v，期望 24×24", label, w, h)
		}
		if un := svg.Unsupported(); len(un) > 0 {
			t.Errorf("%s: 图标含不支持的 SVG 特性 %v", label, un)
		}
	}
	// The two variants must not be the same drawing under two names.
	if iconToolAIPoly == iconToolObb || iconToolAIPoly == iconToolZoomIn {
		t.Errorf("AI 图标与既有工具图标共用了同一个 SVG")
	}

	var theme ui.Theme
	tt := ui.NewTester(func(c *ui.Context) {
		theme = *c.Theme()
		ui.Column(c).Fill().Background(theme.Background).Padding(8).Gap(8).Children(func() {
			for _, label := range []string{"aiPoly", "aiRect"} {
				svg := icons[label]
				ui.Box(c).Size(24, 24).Label(label).Children(func() {
					ui.Icon(c, svg).FontSize(24)
				})
			}
		})
	}, 80, 120)
	for label := range icons {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("图标 %s 未找到", label)
		}
		ink := 0
		for y := int(r.Y); y < int(r.Y+r.H); y++ {
			for x := int(r.X); x < int(r.X+r.W); x++ {
				if !closeColor(atPixel(tt.Image(), float32(x), float32(y)), theme.Background, 30) {
					ink++
				}
			}
		}
		if ink < 8 {
			t.Errorf("图标 %s 只画出 %d 个非背景像素", label, ink)
		}
	}
}

// TestAIPrefetchOnFirstPoint: the first collected point starts encoding the
// current image in the background (docs/phase7-design.md §6). With no window the
// same work runs inline, so the embedding is cached by the time the click returns;
// the prompt's second point starts nothing, and neither does a click on an image
// whose embedding is already cached.
func TestAIPrefetchOnFirstPoint(t *testing.T) {
	a := aiTestApp()
	var calls, gotIndex int
	var gotPath string
	a.aiEncodeHook = func(info *ImageInfo, imgIndex int) (sam2.Embedding, error) {
		calls++
		gotIndex, gotPath = imgIndex, info.Path
		return sam2.Embedding{}, nil
	}
	a.setTool(ToolAI)

	clickAt(a, Point{X: 400, Y: 300}, 1)
	if calls != 1 {
		t.Fatalf("首点后编码 %d 次，期望 1 次", calls)
	}
	if want := a.images[a.loaded].Path; gotIndex != a.loaded || gotPath != want {
		t.Errorf("编码的是图 %d（%q），期望 %d（%q）", gotIndex, gotPath, a.loaded, want)
	}
	// No window: the encode ran inline and had landed before the click returned.
	if a.aiEmbFor != a.loaded {
		t.Errorf("落地后 aiEmbFor = %d，期望 %d", a.aiEmbFor, a.loaded)
	}
	if a.aiEncBusy || a.aiEncFor != -1 {
		t.Errorf("落地后 aiEncBusy = %v、aiEncFor = %d，期望 false 与 -1", a.aiEncBusy, a.aiEncFor)
	}
	// The status bar tells the user what the first click set going: it says the
	// encode finished once the landing adopted the embedding.
	if want := locales.StatusAIPrefetchDone(); a.lastStatus != want {
		t.Errorf("落地后状态栏 = %q，期望 %q", a.lastStatus, want)
	}
	if len(a.aiPoints) != 1 {
		t.Errorf("首点后 aiPoints = %v，期望 1 个点", a.aiPoints)
	}

	// The prompt's second point is not a new start.
	clickAt(a, Point{X: 420, Y: 300}, 1)
	if calls != 1 {
		t.Errorf("第二个点又触发了一次编码（共 %d 次），期望仍是 1 次", calls)
	}

	// Esc drops the points but not the cached embedding, so the next first click
	// finds the cache and encodes nothing.
	if !keyWith(a, ui.KeyEscape, 0) {
		t.Fatalf("有点时 Esc 未被处理")
	}
	if a.aiEmbFor != a.loaded {
		t.Fatalf("Esc 后 aiEmbFor = %d，期望缓存仍在（%d）", a.aiEmbFor, a.loaded)
	}
	clickAt(a, Point{X: 200, Y: 200}, 1)
	if calls != 1 {
		t.Errorf("缓存命中时又编码了一次（共 %d 次），期望 1 次", calls)
	}
}

// TestAIPrefetchDedup: one encode at a time, app-wide — an image being encoded,
// another image's encode in flight, an embedding already cached and a prompt that
// never collected a point all start none (docs/phase7-design.md §6).
func TestAIPrefetchDedup(t *testing.T) {
	a := aiTestApp()
	var calls int
	a.aiEncodeHook = func(info *ImageInfo, imgIndex int) (sam2.Embedding, error) {
		calls++
		return sam2.Embedding{}, nil
	}
	a.setTool(ToolAI)

	// This image is already being encoded.
	a.aiEncBusy, a.aiEncFor = true, a.loaded
	clickAt(a, Point{X: 400, Y: 300}, 1)
	if calls != 0 {
		t.Errorf("同图在途时又编码了 %d 次，期望 0 次", calls)
	}
	if !a.aiEncBusy || a.aiEncFor != a.loaded {
		t.Errorf("在途标志被改动：aiEncBusy = %v、aiEncFor = %d", a.aiEncBusy, a.aiEncFor)
	}

	// Another image is: the flag is app-wide, so this click waits for it.
	a.aiEncBusy, a.aiEncFor = true, 7
	a.aiPoints = nil
	clickAt(a, Point{X: 401, Y: 300}, 1)
	if calls != 0 {
		t.Errorf("别的图在途时又编码了 %d 次，期望 0 次", calls)
	}

	// Nothing in flight, but the cache already holds this image.
	a.aiEncBusy, a.aiEncFor = false, -1
	a.aiEmbFor = a.loaded
	a.aiPoints = nil
	clickAt(a, Point{X: 402, Y: 300}, 1)
	if calls != 0 {
		t.Errorf("缓存命中时又编码了 %d 次，期望 0 次", calls)
	}

	// A click the empty category list blocks collects no point, so there is nothing
	// to encode ahead of.
	a.aiEmbFor, a.aiPoints, a.categories = -1, nil, nil
	clickAt(a, Point{X: 403, Y: 300}, 1)
	if calls != 0 || len(a.aiPoints) != 0 {
		t.Errorf("空类别表下编码 %d 次、收集 %v，期望两者都不发生", calls, a.aiPoints)
	}

	// An inference in flight holds the cache handle until it lands: the encode waits
	// for it, and the point is still collected.
	a.categories, a.aiPoints = defaultCategoryList(), nil
	a.aiBusy = true
	clickAt(a, Point{X: 404, Y: 300}, 1)
	if calls != 0 {
		t.Errorf("推理在途时编码了 %d 次，期望 0 次", calls)
	}
	if len(a.aiPoints) != 1 {
		t.Errorf("推理在途时的点击收集了 %v，期望 1 个点", a.aiPoints)
	}
	a.aiBusy = false
}

// TestAIPrefetchDiscarded: an encode that lands after the user moved on drops its
// embedding instead of filling the cache, a failure says nothing, and every
// landing clears the in-flight flag (docs/phase7-design.md §6).
func TestAIPrefetchDiscarded(t *testing.T) {
	a := aiTestApp()
	a.setTool(ToolAI)

	// Esc while the encode was in flight: the generation moved on.
	gen := a.aiGen + 1
	a.aiGen = gen
	a.aiEncBusy, a.aiEncFor = true, a.loaded
	a.aiDropPoints()
	a.aiEncodeLanded(sam2.Embedding{}, nil, gen, a.loaded)
	if a.aiEmbFor != -1 {
		t.Errorf("过期编码落地后 aiEmbFor = %d，期望 -1（未收编）", a.aiEmbFor)
	}
	if a.aiEncBusy || a.aiEncFor != -1 {
		t.Errorf("过期编码落地后 aiEncBusy = %v、aiEncFor = %d，期望 false 与 -1", a.aiEncBusy, a.aiEncFor)
	}

	// The generation still matches, but the user switched images.
	a.images = append(a.images, ImageInfo{Path: "t2", Name: "t2.png", Width: 800, Height: 600})
	a.loaded = 1
	a.aiEncBusy, a.aiEncFor = true, 1
	a.aiEncodeLanded(sam2.Embedding{}, nil, a.aiGen, 0)
	if a.aiEmbFor != -1 {
		t.Errorf("换图后落地的编码 aiEmbFor = %d，期望 -1（未收编）", a.aiEmbFor)
	}

	// A failure is reported, not hidden: the status says the encode failed (the
	// double-click encodes again and surfaces that error), and nothing is cached.
	a.lastStatus = ""
	a.aiEncBusy, a.aiEncFor = true, 1
	a.aiEncodeLanded(sam2.Embedding{}, errors.New("boom"), a.aiGen, 1)
	if want := locales.StatusAIPrefetchFailed(); a.lastStatus != want {
		t.Errorf("预编码失败后状态栏 = %q，期望 %q", a.lastStatus, want)
	}
	if a.aiEmbFor != -1 || a.aiEncBusy {
		t.Errorf("失败落地后 aiEmbFor = %d、aiEncBusy = %v，期望 -1 与 false", a.aiEmbFor, a.aiEncBusy)
	}
}

// TestAIPrefetchThenInference: the embedding the encode-ahead cached is what the
// inference finds, so the run only decodes and says so; without one the
// double-click encodes for itself. Either way the annotation lands and the cache
// survives the run (docs/phase7-design.md §3, §6).
func TestAIPrefetchThenInference(t *testing.T) {
	// No hook, so the prefetch takes the real encoder over a fixture path that is
	// not a file: it fails and says so on the status bar. The inference then
	// encodes its own.
	a := aiTestApp()
	var seen []string
	a.aiPredict = func(img *ImageInfo, pts []Point) ([]float32, int, int, error) {
		seen = append(seen, a.lastStatus)
		return aiTestMask(8, 6, 2, 1, 5, 4), 8, 6, nil
	}
	a.setTool(ToolAI)
	clickAt(a, Point{X: 400, Y: 300}, 1)
	if want := locales.StatusAIPrefetchFailed(); a.lastStatus != want {
		t.Errorf("编码失败后状态栏 = %q，期望 %q", a.lastStatus, want)
	}
	if a.aiEmbFor != -1 || a.aiEncBusy {
		t.Errorf("编码失败后 aiEmbFor = %d、aiEncBusy = %v，期望 -1 与 false", a.aiEmbFor, a.aiEncBusy)
	}
	clickAt(a, Point{X: 410, Y: 310}, 2)
	if len(seen) != 1 || seen[0] != locales.StatusAIEncoding() {
		t.Errorf("无缓存时推理期间状态栏 = %v，期望 [%q]", seen, locales.StatusAIEncoding())
	}
	if got := len(anns(a)); got != 1 {
		t.Fatalf("无缓存时推理产生 %d 条标注，期望 1 条（状态栏 %q）", got, a.lastStatus)
	}

	// With a successful prefetch the double-click starts from the cache.
	b := aiTestApp()
	b.aiEncodeHook = func(info *ImageInfo, imgIndex int) (sam2.Embedding, error) {
		return sam2.Embedding{}, nil
	}
	var decodes int
	var status string
	b.aiPredict = func(img *ImageInfo, pts []Point) ([]float32, int, int, error) {
		decodes++
		status = b.lastStatus
		return aiTestMask(8, 6, 2, 1, 5, 4), 8, 6, nil
	}
	b.setTool(ToolAI)
	clickAt(b, Point{X: 400, Y: 300}, 1)
	if b.aiEmbFor != b.loaded {
		t.Fatalf("预编码后 aiEmbFor = %d，期望 %d", b.aiEmbFor, b.loaded)
	}
	clickAt(b, Point{X: 410, Y: 310}, 1)
	clickAt(b, Point{X: 420, Y: 320}, 2)
	if decodes != 1 || status != locales.StatusAIDecoding() {
		t.Errorf("预编码后推理 %d 次、期间状态栏 %q，期望 1 次与 %q", decodes, status, locales.StatusAIDecoding())
	}
	if got := len(anns(b)); got != 1 {
		t.Fatalf("预编码后推理产生 %d 条标注，期望 1 条（状态栏 %q）", got, b.lastStatus)
	}
	if b.aiEmbFor != b.loaded {
		t.Errorf("推理后 aiEmbFor = %d，期望缓存仍在（%d）", b.aiEmbFor, b.loaded)
	}
}
