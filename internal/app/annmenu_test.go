package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"mylabel/internal/store"
)

// The two menus that act on one annotation (annmenu.go): the category picker a
// double-click on a category chip opens, and the z-order menu a right-click on an
// annotation opens.

// chipPoint returns the image point at the center of annotation i's chip, which
// is where a click lands on that chip.
func chipPoint(t *testing.T, a *App, i int) Point {
	t.Helper()
	img := a.curImage()
	if img == nil || i < 0 || i >= len(img.Anns) {
		t.Fatalf("标注 %d 不存在", i)
	}
	cs, ok := a.chipSizeFor(img.Anns[i].Cat)
	if !ok {
		t.Fatalf("类别 %d 的 chip 还没测量（texts 里的帧没画过？）", img.Anns[i].Cat)
	}
	x, y := chipBounds(&img.Anns[i], cs, a.zoom, Point{X: a.panX, Y: a.panY})
	z := safeZoom(a.zoom)
	return Point{X: (float64(x+cs.w/2) - a.panX) / z, Y: (float64(y+cs.h/2) - a.panY) / z}
}

// chipLocal returns the canvas-local rect chipBounds places annotation i's chip
// at, as the frame draws it.
func chipLocal(t *testing.T, a *App, i int) ui.Rect {
	t.Helper()
	an := &a.images[0].Anns[i]
	cs, ok := a.chipSizeFor(an.Cat)
	if !ok {
		t.Fatalf("类别 %d 的 chip 还没测量", an.Cat)
	}
	x, y := chipBounds(an, cs, a.zoom, Point{X: a.panX, Y: a.panY})
	return ui.Rect{X: x, Y: y, W: cs.w, H: cs.h}
}

// rightClickImage presses and releases the secondary button at image point p.
func rightClickImage(t *testing.T, a *App, tt *ui.Tester, p Point) {
	t.Helper()
	cv := canvasBounds(t, tt)
	x, y := screenOf(cv, a, p)
	tt.PressButton(1, x, y)
	tt.ReleaseButton(1, x, y)
}

// annOrder returns the annotation IDs of the loaded image in slice order, which
// is the drawing order.
func annOrder(a *App) []string {
	img := a.curImage()
	if img == nil {
		return nil
	}
	ids := make([]string, len(img.Anns))
	for i := range img.Anns {
		ids[i] = img.Anns[i].ID
	}
	return ids
}

// wantOrder checks the loaded image's annotation order.
func wantOrder(t *testing.T, a *App, want ...string) {
	t.Helper()
	got := annOrder(a)
	if len(got) != len(want) {
		t.Fatalf("标注顺序 = %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("标注顺序 = %v，期望 %v", got, want)
		}
	}
}

// TestChipBoundsMatchesDrawing cross-checks the geometry the pointer hits against
// the chip the frame paints: same corner, same size, whatever the zoom, for a
// rectangle, a polygon, and a chip that flips inside its box.
func TestChipBoundsMatchesDrawing(t *testing.T) {
	a, tt := tester(t)
	a.bmp = markerImage()
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.images[0].Anns = []Ann{
		rectAnn("rect", 0, 400, 300, 200, 100), // box x 300..500, y 250..350
		newPolyAnn("poly", 1, []Point{{100, 100}, {250, 100}, {250, 250}, {100, 250}}),
	}
	a.clearSelection()

	// chipPainted checks the chip painted inside a canvas-local region: its left
	// edge is the annotation's AABB left edge, its bottom edge sits on the AABB top
	// edge, and the rect the pointer hits is the rect the frame painted.
	//
	// The region must hold the chip and nothing else — no neighbouring outline, no
	// other annotation's edge — so it must end above the annotation's own top edge
	// (the last chip row merges with it) and stay clear of everything else.
	chipPainted := func(what string, i int, regionX0, regionY0, regionX1, regionY1 float32) {
		t.Helper()
		cv := canvasBounds(t, tt)
		an := &a.images[0].Anns[i]
		painted, ok := chipBox(tt.Image(), cv.X+regionX0, cv.Y+regionY0, cv.X+regionX1, cv.Y+regionY1)
		if !ok {
			t.Fatalf("%s：区域 %v,%v-%v,%v 里没画 chip", what, regionX0, regionY0, regionX1, regionY1)
		}
		got := ui.Rect{X: painted.X - cv.X, Y: painted.Y - cv.Y, W: painted.W, H: painted.H}
		b := annBounds(an)
		cs, ok := a.chipSizeFor(an.Cat)
		if !ok {
			t.Fatalf("%s：类别 %d 的 chip 没测量", what, an.Cat)
		}
		left, top := float32(a.panX+b.MinX*a.zoom), float32(a.panY+b.MinY*a.zoom)
		if !nearly(float64(got.X), float64(left), 2) {
			t.Errorf("%s：画出来的 chip 左边 %v，期望标注的左边 %v", what, got.X, left)
		}
		if !nearly(float64(got.Y), float64(top-cs.h), 2) {
			t.Errorf("%s：画出来的 chip 顶边 %v，期望标注顶边上移 %v = %v", what, got.Y, cs.h, top-cs.h)
		}
		if !nearly(float64(got.X+got.W), float64(left+cs.w), 3) {
			t.Errorf("%s：画出来的 chip 右边 %v，期望 %v", what, got.X+got.W, left+cs.w)
		}
		// What the pointer hits is the same rect the frame painted, and its bottom
		// edge is the annotation's top edge.
		hit := chipLocal(t, a, i)
		if !nearly(float64(hit.X), float64(got.X), 2) || !nearly(float64(hit.Y), float64(got.Y), 2) ||
			!nearly(float64(hit.W), float64(cs.w), 1e-6) || !nearly(float64(hit.Y+hit.H), float64(top), 1e-6) {
			t.Errorf("%s：chipBounds 给出 %+v，画面画的是 %+v（标注顶边 %v）", what, hit, got, top)
		}
	}

	tt.Frame()
	chipPainted("矩形上方", 0, 296, 218, 338, 248)
	chipPainted("多边形上方", 1, 96, 68, 138, 98)

	// Zoomed in and panned: the chip keeps its screen size and still sits on the
	// annotation's top edge.
	a.images[0].Anns = a.images[0].Anns[:1]
	a.zoom, a.panX, a.panY = 2, 30, 20
	tt.Frame()
	chipPainted("放大后矩形上方", 0, 626, 468, 668, 518)

	// A box hugging the canvas top would push its chip off screen: it goes inside,
	// where the region stays clear of the box's own left and right edges.
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.images[0].Anns = []Ann{rectAnn("high", 0, 400, 20, 200, 40)} // box y 0..40
	tt.Frame()
	got := chipLocal(t, a, 0)
	if !nearly(float64(got.Y), 0, 1.5) {
		t.Errorf("贴顶的框，chip 的 y = %v，期望落在画布顶边 0（翻转进框）", got.Y)
	}
	cv := canvasBounds(t, tt)
	if _, ok := chipBox(tt.Image(), cv.X+303, cv.Y+1, cv.X+460, cv.Y+14); !ok {
		t.Error("贴顶的框：框顶没有翻转进来的 chip")
	}
	if _, ok := chipBox(tt.Image(), cv.X+303, cv.Y+30, cv.X+460, cv.Y+38); ok {
		t.Error("贴顶的框：chip 不该出现在框的中部")
	}
}

// TestChipHitOnlyForPaintedChips: the pointer only hits the chips the frame
// paints — none with the labels off, only the selection's past chipLabelLimit,
// and none for a category beyond the list.
func TestChipHitOnlyForPaintedChips(t *testing.T) {
	a, tt := tester(t)
	a.bmp = markerImage()
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.images[0].Anns = []Ann{rectAnn("a", 0, 400, 300, 200, 100)}
	a.clearSelection()
	tt.Frame()

	p := chipPoint(t, a, 0)
	if got := a.chipHitAt(p); got != 0 {
		t.Fatalf("chipHitAt = %d，期望 0", got)
	}
	// Just above the chip, and inside the annotation: neither is a chip.
	top := chipLocal(t, a, 0)
	above := Point{X: p.X, Y: (float64(top.Y) - 3 - a.panY) / a.zoom}
	if got := a.chipHitAt(above); got != -1 {
		t.Errorf("chip 上方 3 DIP 处 chipHitAt = %d，期望 -1", got)
	}
	if got := a.chipHitAt(Point{400, 300}); got != -1 {
		t.Errorf("标注内部 chipHitAt = %d，期望 -1", got)
	}

	// Labels off: nothing painted, nothing hit.
	a.toggleLabels()
	tt.Frame()
	if got := a.chipHitAt(p); got != -1 {
		t.Errorf("隐藏标签后 chipHitAt = %d，期望 -1", got)
	}
	a.toggleLabels()

	// Past the limit only the selection carries a chip.
	a.images[0].Anns = make([]Ann, 0, chipLabelLimit+1)
	for i := 0; i < chipLabelLimit+1; i++ {
		a.images[0].Anns = append(a.images[0].Anns,
			rectAnn("f", 0, float64(30+(i%20)*6), float64(500+(i/20)*3), 4, 4))
	}
	a.clearSelection()
	tt.Frame()
	if got := a.chipHitAt(chipPoint(t, a, 0)); got != -1 {
		t.Errorf("%d 个标注时未选中项的 chip 不该命中：%d", len(a.images[0].Anns), got)
	}
	a.selectOnly(0)
	tt.Frame()
	if got := a.chipHitAt(chipPoint(t, a, 0)); got != 0 {
		t.Errorf("选中项的 chip 应命中：%d", got)
	}

	// A category beyond the list draws no chip, so there is nothing to hit.
	a.images[0].Anns = []Ann{{ID: "x", Cat: len(a.categories) + 5, CX: 400, CY: 300, W: 100, H: 50}}
	a.clearSelection()
	tt.Frame()
	if got := a.chipHitAt(Point{350, 257}); got != -1 {
		t.Errorf("类别越界的标注 chipHitAt = %d，期望 -1", got)
	}
}

// chipFixture returns an App showing one unselected rectangle at (400,300) with a
// chip above it, in the panel Tester, plus the picker rendered.
func chipFixture(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	a := newTestApp()
	a.bmp = markerImage()
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.images[0].Anns = []Ann{rectAnn("ann_1", 0, 400, 300, 200, 100)} // box x 300..500, y 250..350
	a.clearSelection()
	tt := panelFor(t, a)
	separateClicks(tt)
	tt.Frame()
	return a, tt
}

// TestClickChipSelectsItsAnnotation: a single click on a chip selects the
// annotation it belongs to and does nothing else — above all it starts no draft
// and no move, so a double-click on it can open the picker.
func TestClickChipSelectsItsAnnotation(t *testing.T) {
	a, tt := chipFixture(t)

	clickImage(t, a, tt, chipPoint(t, a, 0))
	tt.Frame()
	if a.soleSelection() != 0 {
		t.Errorf("点 chip 后 soleSelection = %d，期望 0", a.soleSelection())
	}
	if a.mode != ModeIdle {
		t.Errorf("点 chip 后 mode = %v，期望 ModeIdle", a.mode)
	}
	if len(a.images[0].Anns) != 1 {
		t.Errorf("点 chip 创建了标注：%+v", a.images[0].Anns)
	}
	if a.props.Open || a.catPick.Open {
		t.Errorf("单击 chip 打开了对话框：props=%v catPick=%v", a.props.Open, a.catPick.Open)
	}
}

// TestDoubleClickChipOpensCategoryPicker: a double-click on a chip opens the
// category picker on that annotation, preselecting its category, and 取消 closes
// it without touching anything.
func TestDoubleClickChipOpensCategoryPicker(t *testing.T) {
	a, tt := chipFixture(t)

	doubleClickImage(t, a, tt, chipPoint(t, a, 0))
	tt.Frame()
	if !a.catPick.Open {
		t.Fatalf("双击 chip 没打开类别选择（mode %v, props %v, status %q）", a.mode, a.props.Open, a.lastStatus)
	}
	if a.catPick.Image != 0 || a.catPick.Index != 0 || a.catPick.Cat != 0 {
		t.Errorf("类别选择指向 %+v，期望第 0 张图的第 0 条（类别 0）", a.catPick)
	}
	if !tt.HasText("选择类别") || !tt.HasText("取消") {
		t.Fatalf("类别选择里没有标题或取消，texts = %v", tt.Texts())
	}
	if a.props.Open {
		t.Error("双击 chip 还打开了属性面板")
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("点击取消: %v", err)
	}
	tt.Frame()
	if a.catPick.Open {
		t.Error("取消后类别选择仍开着")
	}
	if n := len(a.undoStack); n != 0 {
		t.Errorf("取消后 undo 栈 = %d 条，期望 0", n)
	}
	if cat := a.images[0].Anns[0].Cat; cat != 0 {
		t.Errorf("取消后类别 = %d，期望 0", cat)
	}

	// Escape closes it the same way, without touching the annotation.
	doubleClickImage(t, a, tt, chipPoint(t, a, 0))
	tt.Frame()
	if !a.catPick.Open {
		t.Fatalf("再次双击 chip 没打开类别选择（status %q）", a.lastStatus)
	}
	tt.KeyDown(ui.KeyEscape)
	tt.Frame()
	if a.catPick.Open {
		t.Error("Escape 后类别选择仍开着")
	}
	if cat := a.images[0].Anns[0].Cat; cat != 0 || len(a.undoStack) != 0 {
		t.Errorf("Escape 改了标注：类别 %d，undo %d 条", cat, len(a.undoStack))
	}
}

// TestChipPickerAppliesCategoryAsOneUndo: picking a category applies it at once,
// closes the picker, pushes one undo step, and marks a fresh image as labeling.
func TestChipPickerAppliesCategoryAsOneUndo(t *testing.T) {
	a, tt := chipFixture(t)
	if a.images[0].Status != "" && normalizeStatus(a.images[0].Status) != store.StatusNew {
		t.Fatalf("夹具图片状态 = %q，期望新图", a.images[0].Status)
	}

	doubleClickImage(t, a, tt, chipPoint(t, a, 0))
	tt.Frame()
	if !a.catPick.Open {
		t.Fatalf("双击 chip 没打开类别选择（status %q）", a.lastStatus)
	}
	if err := tt.Click("cat3"); err != nil {
		t.Fatalf("点击类别 cat3: %v（texts %v）", err, tt.Texts())
	}
	tt.Frame()

	if a.catPick.Open {
		t.Error("选好类别后对话框仍开着")
	}
	if cat := a.images[0].Anns[0].Cat; cat != 2 {
		t.Errorf("选好类别后 Cat = %d，期望 2（cat3）", cat)
	}
	if n := len(a.undoStack); n != 1 {
		t.Fatalf("undo 栈 = %d 条，期望 1 条", n)
	}
	if got := a.undoStack[0].Name; got != "modify" {
		t.Errorf("undo 命令 = %q，期望 modify", got)
	}
	if got := normalizeStatus(a.images[0].Status); got != store.StatusLabeling {
		t.Errorf("图片状态 = %q，期望 %q（首次编辑转标注中）", got, store.StatusLabeling)
	}

	a.undo()
	if cat := a.images[0].Anns[0].Cat; cat != 0 {
		t.Errorf("撤销后 Cat = %d，期望 0", cat)
	}
	a.redo()
	if cat := a.images[0].Anns[0].Cat; cat != 2 {
		t.Errorf("重做后 Cat = %d，期望 2", cat)
	}
}

// TestDoubleClickBodyStillOpensProps: away from its chip, a double-click on the
// annotation opens the properties dialog, not the category picker.
func TestDoubleClickBodyStillOpensProps(t *testing.T) {
	a, tt := chipFixture(t)

	doubleClickImage(t, a, tt, Point{400, 300})
	tt.Frame()
	if !a.props.Open {
		t.Fatalf("双击标注内部没打开属性面板（mode %v, catPick %v）", a.mode, a.catPick.Open)
	}
	if a.catPick.Open {
		t.Error("双击标注内部也打开了类别选择")
	}
}

// TestChipPickerIgnoresVanishedAnnotation: the picker names its annotation by
// index, so an undo that removes it must close the picker instead of writing to
// whatever moved into that index.
func TestChipPickerIgnoresVanishedAnnotation(t *testing.T) {
	a, tt := chipFixture(t)

	doubleClickImage(t, a, tt, chipPoint(t, a, 0))
	tt.Frame()
	if !a.catPick.Open {
		t.Fatalf("双击 chip 没打开类别选择（status %q）", a.lastStatus)
	}
	a.images[0].Anns = nil
	tt.Frame()
	if a.catPick.Open {
		t.Error("标注消失后类别选择仍开着")
	}
}

// zFixture returns an App with three rectangles side by side, in the shared
// Tester: a at x 60..140, b at 260..340, c at 460..540, every one y 270..330.
func zFixture(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	a, tt := tester(t)
	a.bmp = markerImage()
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.images[0].Anns = []Ann{
		rectAnn("a", 0, 100, 300, 80, 60),
		rectAnn("b", 1, 300, 300, 80, 60),
		rectAnn("c", 2, 500, 300, 80, 60),
	}
	a.clearSelection()
	separateClicks(tt)
	tt.Frame()
	return a, tt
}

// TestZMenuMovesOneLayer: a right-click on an annotation opens the z-order menu on
// it, and 上移一层 swaps it with its neighbour as one undo step.
func TestZMenuMovesOneLayer(t *testing.T) {
	a, tt := zFixture(t)

	rightClickImage(t, a, tt, Point{300, 300}) // b
	if !a.zMenu.Open {
		t.Fatalf("右键标注没打开层级菜单（status %q）", a.lastStatus)
	}
	if a.zMenu.Index != 1 || a.zMenu.Image != 0 {
		t.Errorf("菜单指向 %+v，期望第 1 条", a.zMenu)
	}
	if a.soleSelection() != 1 {
		t.Errorf("右键后选择 = %d，期望 1（菜单作用的标注）", a.soleSelection())
	}
	for _, label := range []string{"上移一层", "下移一层", "移到顶部", "移到底部"} {
		if !tt.HasText(label) {
			t.Fatalf("层级菜单里没有 %q，texts = %v", label, tt.Texts())
		}
	}

	if err := tt.Click("上移一层"); err != nil {
		t.Fatalf("点击上移一层: %v", err)
	}
	tt.Frame()
	if a.zMenu.Open {
		t.Error("选中菜单项后菜单仍开着")
	}
	wantOrder(t, a, "a", "c", "b")
	if a.soleSelection() != 2 {
		t.Errorf("上移后选择 = %d，期望 2（移动后的位置）", a.soleSelection())
	}
	if n := len(a.undoStack); n != 1 {
		t.Fatalf("undo 栈 = %d 条，期望 1 条", n)
	}
	if got := a.undoStack[0].Name; got != "reorder" {
		t.Errorf("undo 命令 = %q，期望 reorder", got)
	}

	a.undo()
	wantOrder(t, a, "a", "b", "c")
	a.redo()
	wantOrder(t, a, "a", "c", "b")
}

// TestZMenuMovesToTopAndBottom: 移到顶部 and 移到底部 move the annotation to the
// end of the order (the top of the drawing), and the moves that cannot change
// anything are disabled.
func TestZMenuMovesToTopAndBottom(t *testing.T) {
	a, tt := zFixture(t)

	// The bottom annotation cannot go down or further down: those items do
	// nothing at all.
	rightClickImage(t, a, tt, Point{100, 300}) // a, index 0
	if !a.zMenu.Open {
		t.Fatal("右键标注没打开层级菜单")
	}
	for _, label := range []string{"下移一层", "移到底部"} {
		if err := tt.Click(label); err != nil {
			t.Fatalf("点击 %q: %v", label, err)
		}
		tt.Frame()
		wantOrder(t, a, "a", "b", "c")
		if n := len(a.undoStack); n != 0 {
			t.Fatalf("点击禁用的 %q 推了 undo：%d 条", label, n)
		}
	}
	if !a.zMenu.Open {
		t.Fatal("点击禁用的菜单项后菜单关掉了")
	}
	if err := tt.Click("移到顶部"); err != nil {
		t.Fatalf("点击移到顶部: %v", err)
	}
	tt.Frame()
	wantOrder(t, a, "b", "c", "a")

	// Now it is on top: neither 上移一层 nor 移到顶部 can move it.
	rightClickImage(t, a, tt, Point{100, 300}) // a, index 2
	if !a.zMenu.Open {
		t.Fatal("右键标注（第二个场景）没打开层级菜单")
	}
	for _, label := range []string{"上移一层", "移到顶部"} {
		if err := tt.Click(label); err != nil {
			t.Fatalf("点击 %q: %v", label, err)
		}
		tt.Frame()
		wantOrder(t, a, "b", "c", "a")
		if n := len(a.undoStack); n != 1 {
			t.Fatalf("点击禁用的 %q 推了 undo：%d 条", label, n)
		}
	}
	if err := tt.Click("移到底部"); err != nil {
		t.Fatalf("点击移到底部: %v", err)
	}
	tt.Frame()
	wantOrder(t, a, "a", "b", "c")
	if n := len(a.undoStack); n != 2 {
		t.Errorf("undo 栈 = %d 条，期望 2 条", n)
	}
}

// TestZMenuNotOpenedOnEmptyCanvas: a right-click that hits no annotation opens
// nothing.
func TestZMenuNotOpenedOnEmptyCanvas(t *testing.T) {
	a, tt := zFixture(t)

	rightClickImage(t, a, tt, Point{700, 500})
	tt.Frame()
	if a.zMenu.Open {
		t.Error("右键空白处打开了层级菜单")
	}
	if len(a.images[0].Anns) != 3 {
		t.Errorf("右键空白处改动了标注：%d 条", len(a.images[0].Anns))
	}
}

// TestZMenuNotOpenedDuringDraft: a right-click while a polygon is being drawn
// opens no menu, and the draft keeps its vertices.
func TestZMenuNotOpenedDuringDraft(t *testing.T) {
	a, tt := zFixture(t)
	a.setTool(ToolPoly)

	clickImage(t, a, tt, Point{700, 500})
	tt.Frame()
	if a.mode != ModePolyCreate || len(a.polyDraft) != 1 {
		t.Fatalf("没有进入多边形草稿：mode %v, 顶点 %d", a.mode, len(a.polyDraft))
	}

	rightClickImage(t, a, tt, Point{300, 300}) // on b, but a draft is in progress
	tt.Frame()
	if a.zMenu.Open {
		t.Error("画草稿时右键打开了层级菜单")
	}
	if len(a.polyDraft) != 1 {
		t.Errorf("右键丢掉了草稿顶点：%d 个", len(a.polyDraft))
	}
	a.cancelPolyDraft()
}

// TestZMenuStaysClickableNearCanvasBottom: the menu is an overlay, so a
// right-click low on the canvas still shows a panel whose items can be pressed —
// the canvas clips its own drawing, not the menu.
func TestZMenuStaysClickableNearCanvasBottom(t *testing.T) {
	a, tt := tester(t)
	a.bmp = markerImage()
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.images[0].Anns = []Ann{
		rectAnn("low", 0, 400, 560, 200, 60),
		rectAnn("high", 0, 400, 400, 200, 60),
	}
	a.clearSelection()
	separateClicks(tt)
	tt.Frame()

	rightClickImage(t, a, tt, Point{400, 560})
	if !a.zMenu.Open {
		t.Fatalf("画布底部右键没打开层级菜单（status %q）", a.lastStatus)
	}
	for _, label := range []string{"上移一层", "下移一层", "移到顶部", "移到底部"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("菜单项 %q 不在画面上，texts = %v", label, tt.Texts())
		}
	}
	if err := tt.Click("移到顶部"); err != nil {
		t.Fatalf("点击移到顶部: %v", err)
	}
	tt.Frame()
	wantOrder(t, a, "high", "low")
}

// TestZMenuClosesOnOutsidePressAndEscape: a left press elsewhere drops the menu
// without selecting, creating or clearing anything, and Escape drops it without
// touching the selection.
func TestZMenuClosesOnOutsidePressAndEscape(t *testing.T) {
	a, tt := zFixture(t)

	rightClickImage(t, a, tt, Point{300, 300})
	if !a.zMenu.Open {
		t.Fatal("右键标注没打开层级菜单")
	}
	clickImage(t, a, tt, Point{700, 500}) // empty canvas, outside the menu
	tt.Frame()
	if a.zMenu.Open {
		t.Error("点菜单外面没关掉菜单")
	}
	if len(a.images[0].Anns) != 3 {
		t.Errorf("关闭菜单的那一下创建了标注：%d 条", len(a.images[0].Anns))
	}
	if a.mode != ModeIdle {
		t.Errorf("关闭菜单后 mode = %v，期望 ModeIdle", a.mode)
	}
	if len(a.undoStack) != 0 {
		t.Errorf("关闭菜单推了 undo：%d 条", len(a.undoStack))
	}

	rightClickImage(t, a, tt, Point{300, 300})
	if !a.zMenu.Open {
		t.Fatal("右键标注（第二个场景）没打开层级菜单")
	}
	sel := a.soleSelection()
	if handled := a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyEscape)); !handled {
		t.Error("Escape 没被画布处理")
	}
	if a.zMenu.Open {
		t.Error("Escape 没关掉菜单")
	}
	if a.soleSelection() != sel {
		t.Errorf("Escape 关菜单改了选择：%d，期望 %d", a.soleSelection(), sel)
	}

	// The same key through the window's own routing closes it too.
	tt.Frame() // rebuild the menu's overlay after the direct call above
	rightClickImage(t, a, tt, Point{300, 300})
	if !a.zMenu.Open {
		t.Fatal("右键标注（第三个场景）没打开层级菜单")
	}
	tt.KeyDown(ui.KeyEscape)
	tt.Frame()
	if a.zMenu.Open {
		t.Error("Escape 键没关掉菜单")
	}
	if len(a.images[0].Anns) != 3 || len(a.undoStack) != 0 {
		t.Errorf("Escape 关菜单动了标注：%d 条，undo %d 条", len(a.images[0].Anns), len(a.undoStack))
	}
}

// TestAnnsOrderSurvivesSaveLoad: the slice order is the drawing order, so the
// working repository must keep it: a save writes the annotations in slice order
// and a reload reads them back in that order.
func TestAnnsOrderSurvivesSaveLoad(t *testing.T) {
	a := storeApp(t, "a.png")
	seedAnns(t, a, 0, []Ann{
		rectAnn("z", 0, 10, 10, 4, 4),
		rectAnn("m", 1, 20, 20, 4, 4),
		rectAnn("a", 2, 30, 30, 4, 4),
	})

	b := reopen(t, a.dir)
	wantOrder(t, b, "z", "m", "a")

	// A reorder written through the command survives the same round trip.
	b.selectOnly(0)
	b.push(cmdReorder(b, b.loaded, 0, 2, "z"))
	wantOrder(t, b, "m", "a", "z")
	if err := b.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	c := reopen(t, b.dir)
	wantOrder(t, c, "m", "a", "z")
}

// TestReorderTarget covers the pure index arithmetic of the four moves.
func TestReorderTarget(t *testing.T) {
	cases := []struct {
		kind  reorderKind
		index int
		n     int
		want  int
		ok    bool
	}{
		{zUp, 0, 3, 1, true},
		{zUp, 2, 3, 0, false},
		{zDown, 0, 3, 0, false},
		{zDown, 2, 3, 1, true},
		{zTop, 0, 3, 2, true},
		{zTop, 2, 3, 0, false},
		{zBottom, 0, 3, 0, false},
		{zBottom, 2, 3, 0, true},
		{zTop, 0, 1, 0, false},  // a lone annotation has nowhere to go
		{zUp, -1, 3, 0, false},  // stale indices move nothing
		{zDown, 3, 3, 0, false}, // ditto
		{reorderKind(0), 1, 3, 0, false},
	}
	for _, c := range cases {
		got, ok := reorderTarget(c.kind, c.index, c.n)
		if ok != c.ok || got != c.want {
			t.Errorf("reorderTarget(%v, %d, %d) = (%d, %v)，期望 (%d, %v)",
				c.kind, c.index, c.n, got, ok, c.want, c.ok)
		}
	}
}

// TestCmdReorderKeepsStrangersPut: a redo whose index now names another
// annotation leaves the list alone instead of moving a stranger.
func TestCmdReorderKeepsStrangersPut(t *testing.T) {
	a := newTestApp()
	a.images[0].Anns = []Ann{
		rectAnn("a", 0, 100, 300, 80, 60),
		rectAnn("b", 1, 300, 300, 80, 60),
		rectAnn("c", 2, 500, 300, 80, 60),
	}
	cmd := cmdReorder(a, 0, 1, 2, "b")
	cmd.Do()
	wantOrder(t, a, "a", "c", "b")

	// Something else moved into index 1 in the meantime: the undo does nothing.
	a.images[0].Anns = []Ann{
		rectAnn("x", 0, 100, 300, 80, 60),
		rectAnn("y", 1, 300, 300, 80, 60),
	}
	cmd.Undo()
	wantOrder(t, a, "x", "y")
}

// TestChipPointIsInsideItsChip guards the helper the tests above click through:
// the point chipPoint returns must be the chip's, not its neighbour's.
func TestChipPointIsInsideItsChip(t *testing.T) {
	a, _ := zFixture(t)
	for i := range a.images[0].Anns {
		p := chipPoint(t, a, i)
		if got := a.chipHitAt(p); got != i {
			t.Errorf("第 %d 条 chip 的中心点命中了 %d", i, got)
		}
		if r := chipLocal(t, a, i); r.H < 5 {
			t.Errorf("第 %d 条 chip 的高度只有 %v", i, r.H)
		}
	}
}
