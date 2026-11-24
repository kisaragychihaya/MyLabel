package app

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The rotated-box tool (docs/phase3-design.md §5.2): four clicks make one
// annotation whose W/H/Angle come from the corners, drawn with the polygon
// draft's mechanics.

// TestObbFromPoints pins the fit: the first edge gives width and angle, the last
// edge the height, the center is the centroid. Pure geometry, so 1e-9.
func TestObbFromPoints(t *testing.T) {
	const eps = 1e-9
	for _, tc := range []struct {
		name  string
		pts   []Point
		w, h  float64
		angle float64
		cx    float64
		cy    float64
	}{
		{
			name: "axis aligned",
			pts:  []Point{{10, 10}, {50, 10}, {50, 40}, {10, 40}},
			w:    40, h: 30, angle: 0, cx: 30, cy: 25,
		},
		{
			// Clockwise quarter turn: the first edge runs down the +Y axis.
			name: "quarter turn",
			pts:  []Point{{0, 0}, {0, 20}, {-10, 20}, {-10, 0}},
			w:    20, h: 10, angle: math.Pi / 2, cx: -5, cy: 10,
		},
		{
			// p0 -> p1 points up-left: the angle is normalized into (-π, π].
			name: "negative angle",
			pts:  []Point{{0, 0}, {-10, -10}, {0, -20}, {10, -10}},
			w:    math.Hypot(10, 10), h: math.Hypot(10, 10), angle: -3 * math.Pi / 4, cx: 0, cy: -10,
		},
		{
			name: "second corner off the ideal rectangle does not change w",
			// p2 is ignored by the fit except for the centroid.
			pts: []Point{{0, 0}, {30, 0}, {99, 99}, {0, 10}},
			w:   30, h: 10, angle: 0, cx: 32.25, cy: 27.25,
		},
	} {
		box, ok := obbFromPoints(tc.pts)
		if !ok {
			t.Fatalf("%s: obbFromPoints(%v) rejected a usable box", tc.name, tc.pts)
		}
		if box.Kind != KindRect {
			t.Errorf("%s: kind = %q, want %q", tc.name, box.Kind, KindRect)
		}
		if !nearly(box.W, tc.w, eps) || !nearly(box.H, tc.h, eps) {
			t.Errorf("%s: %v×%v, want %v×%v", tc.name, box.W, box.H, tc.w, tc.h)
		}
		if !nearly(box.Angle, tc.angle, eps) {
			t.Errorf("%s: angle = %v, want %v", tc.name, box.Angle, tc.angle)
		}
		if !nearly(box.CX, tc.cx, eps) || !nearly(box.CY, tc.cy, eps) {
			t.Errorf("%s: center = (%v,%v), want (%v,%v)", tc.name, box.CX, box.CY, tc.cx, tc.cy)
		}
	}

	// Rejections: too few points, no area, and a non-finite corner.
	if _, ok := obbFromPoints([]Point{{0, 0}, {10, 0}, {10, 10}}); ok {
		t.Error("three points made a box")
	}
	if _, ok := obbFromPoints(nil); ok {
		t.Error("no points made a box")
	}
	if _, ok := obbFromPoints([]Point{{5, 5}, {5, 5}, {5, 5}, {5, 5}}); ok {
		t.Error("four identical points made a box")
	}
	// A sliver narrower than minObbSide is rejected, as a too-small drag is.
	if _, ok := obbFromPoints([]Point{{0, 0}, {0.2, 0}, {0.2, 10}, {0, 10}}); ok {
		t.Error("a 0.2 px wide box was accepted")
	}
	if _, ok := obbFromPoints([]Point{{0, 0}, {math.NaN(), 0}, {10, 10}, {0, 10}}); ok {
		t.Error("a NaN corner made a box")
	}
}

// TestObbToolCreatesRotatedBox clicks four corners on the canvas: the result is
// one rotated rectangle, selected, with the geometry obbFromPoints describes.
func TestObbToolCreatesRotatedBox(t *testing.T) {
	a, tt := tester(t)
	separateClicks(tt)
	a.setTool(ToolObb)

	pts := []Point{{100, 100}, {220, 160}, {180, 240}, {60, 180}}
	for _, p := range pts {
		clickImage(t, a, tt, p)
	}
	if n := len(anns(a)); n != 1 {
		t.Fatalf("4 次点击后 %d 条标注，期望 1（mode %v）", n, a.mode)
	}
	got := anns(a)[0]
	want, ok := obbFromPoints(pts)
	if !ok {
		t.Fatal("测试用的四点不应被拒绝")
	}
	if got.Kind != KindRect {
		t.Errorf("kind = %q，期望 %q（obb 在模型里就是旋转矩形）", got.Kind, KindRect)
	}
	// The clicks went through the float32 pointer mapping: a hundredth of a
	// pixel of slack is enough.
	const tol = 0.01
	if !nearly(got.W, want.W, tol) || !nearly(got.H, want.H, tol) {
		t.Errorf("尺寸 = %v×%v，期望 %v×%v", got.W, got.H, want.W, want.H)
	}
	if !nearly(got.Angle, want.Angle, 1e-6) {
		t.Errorf("角度 = %v，期望 %v", got.Angle, want.Angle)
	}
	if !nearly(got.CX, want.CX, tol) || !nearly(got.CY, want.CY, tol) {
		t.Errorf("中心 = (%v,%v)，期望 (%v,%v)", got.CX, got.CY, want.CX, want.CY)
	}
	if got.Cat != a.curCat {
		t.Errorf("类别 = %d，期望当前类别 %d", got.Cat, a.curCat)
	}
	if a.mode != ModeIdle || len(a.polyDraft) != 0 {
		t.Errorf("成框后 mode = %v、草稿 %v，期望空闲且没有草稿", a.mode, a.polyDraft)
	}
	// A creation tool leaves the box unselected: the pointer tool is where it is
	// picked up again (docs/phase6-design.md §6.2).
	if a.selectionCount() != 0 {
		t.Errorf("成框后 selectionCount = %d，期望 0", a.selectionCount())
	}
	// One undo removes the whole box.
	a.undo()
	if n := len(anns(a)); n != 0 {
		t.Errorf("撤销后 %d 条标注，期望 0", n)
	}
}

// TestObbToolEscapeAndBackspace: Escape drops the draft, Backspace steps back
// one corner, and the draft still completes after either.
func TestObbToolEscapeAndBackspace(t *testing.T) {
	a, tt := tester(t)
	separateClicks(tt)
	a.setTool(ToolObb)

	clickImage(t, a, tt, Point{100, 100})
	clickImage(t, a, tt, Point{200, 100})
	if a.mode != ModePolyCreate || len(a.polyDraft) != 2 {
		t.Fatalf("两次点击后 mode = %v、草稿 %v", a.mode, a.polyDraft)
	}
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyEscape))
	if a.mode != ModeIdle || len(a.polyDraft) != 0 {
		t.Fatalf("Esc 后 mode = %v、草稿 %v，期望取消", a.mode, a.polyDraft)
	}
	if n := len(anns(a)); n != 0 {
		t.Fatalf("Esc 后 %d 条标注，期望 0", n)
	}

	// Backspace removes the last corner; three more clicks then complete a box
	// from the two that were left.
	clickImage(t, a, tt, Point{50, 50})
	clickImage(t, a, tt, Point{150, 50})
	clickImage(t, a, tt, Point{150, 90})
	if len(a.polyDraft) != 3 {
		t.Fatalf("三次点击后草稿 = %v，期望 3 点", a.polyDraft)
	}
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyBackspace))
	if len(a.polyDraft) != 2 || a.mode != ModePolyCreate {
		t.Fatalf("Backspace 后草稿 = %v（mode %v），期望剩 2 点且仍在创建", a.polyDraft, a.mode)
	}
	clickImage(t, a, tt, Point{150, 90})
	clickImage(t, a, tt, Point{50, 90})
	if n := len(anns(a)); n != 1 {
		t.Fatalf("补满四点后 %d 条标注，期望 1", n)
	}
	got := anns(a)[0]
	if !nearly(got.W, 100, 0.01) || !nearly(got.H, 40, 0.01) || !nearly(got.Angle, 0, 1e-6) {
		t.Errorf("框 = %v×%v angle %v，期望 100×40 angle 0", got.W, got.H, got.Angle)
	}
}

// TestObbToolDropsDegenerateBox: four clicks in the same place describe nothing,
// so no annotation appears and the reason is reported.
func TestObbToolDropsDegenerateBox(t *testing.T) {
	a, tt := tester(t)
	separateClicks(tt)
	a.setTool(ToolObb)

	p := Point{100, 100}
	for range 4 {
		clickImage(t, a, tt, p)
	}
	if n := len(anns(a)); n != 0 {
		t.Fatalf("退化框仍生成了 %d 条标注: %+v", n, anns(a))
	}
	if a.mode != ModeIdle {
		t.Errorf("mode = %v，期望回到空闲", a.mode)
	}
	if a.lastStatus != "旋转框太小，已放弃" {
		t.Errorf("lastStatus = %q", a.lastStatus)
	}
}

// TestObbAndPolyDraftsDoNotMix: the two click-by-click tools share the draft
// mechanism, and switching tools drops the draft that belongs to the old one.
func TestObbAndPolyDraftsDoNotMix(t *testing.T) {
	a, tt := tester(t)
	separateClicks(tt)

	// A polygon draft in progress.
	a.setTool(ToolPoly)
	clickImage(t, a, tt, Point{100, 100})
	clickImage(t, a, tt, Point{200, 100})
	if a.mode != ModePolyCreate || a.draft.Kind != KindPoly {
		t.Fatalf("多边形草稿 = %v（kind %q），mode %v", a.polyDraft, a.draft.Kind, a.mode)
	}

	// Switching to the obb tool drops it instead of turning it into a box.
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyO))
	if a.tool != ToolObb {
		t.Fatalf("O 键后 tool = %v，期望 ToolObb", a.tool)
	}
	if a.mode != ModeIdle || len(a.polyDraft) != 0 {
		t.Fatalf("切到 obb 后草稿还在: %v（mode %v）", a.polyDraft, a.mode)
	}

	// Four clicks now make a rotated rectangle, not a polygon.
	for _, p := range []Point{{100, 100}, {200, 100}, {200, 160}, {100, 160}} {
		clickImage(t, a, tt, p)
	}
	got := anns(a)
	if len(got) != 1 {
		t.Fatalf("%d 条标注，期望 1", len(got))
	}
	if isPoly(&got[0]) {
		t.Errorf("obb 工具产出了多边形: %+v", got[0])
	}

	// And the polygon tool still closes with its own gesture: a fifth click
	// after three used to close it early, an obb draft never closes on Enter.
	a.setTool(ToolPoly)
	for _, p := range []Point{{300, 100}, {400, 100}, {400, 200}} {
		clickImage(t, a, tt, p)
	}
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyEnter))
	got = anns(a)
	if len(got) != 2 || !isPoly(&got[1]) || len(got[1].Points) != 3 {
		t.Fatalf("多边形未被 Enter 闭合: %+v", got)
	}
}

// TestObbDraftIgnoresEnterAndCloseClick: the obb draft closes on its fourth
// click only — Enter does nothing, and clicking near the first corner just adds
// that corner.
func TestObbDraftIgnoresEnterAndCloseClick(t *testing.T) {
	a, tt := tester(t)
	separateClicks(tt)
	a.setTool(ToolObb)

	clickImage(t, a, tt, Point{100, 100})
	clickImage(t, a, tt, Point{200, 100})
	clickImage(t, a, tt, Point{200, 160})
	if a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyEnter)) {
		t.Error("obb 草稿的 Enter 被当成闭合处理了")
	}
	if n := len(anns(a)); n != 0 {
		t.Fatalf("Enter 后生成了 %d 条标注，期望 0", n)
	}
	// A click on the first corner is a fourth corner — here a box with no
	// height, which is dropped — and not the polygon's snap-to-close gesture,
	// which would have made a three-vertex polygon instead.
	clickImage(t, a, tt, Point{100, 100})
	if len(a.polyDraft) != 0 || a.mode != ModeIdle {
		t.Fatalf("第四点未收尾: 草稿 %v（mode %v）", a.polyDraft, a.mode)
	}
	if n := len(anns(a)); n != 0 {
		t.Fatalf("退化四点仍生成了标注: %+v", anns(a))
	}
	if a.lastStatus != "旋转框太小，已放弃" {
		t.Errorf("lastStatus = %q", a.lastStatus)
	}
}

// TestObbToolButtonAndKey pins the rotated box tool's shortcut: R and P still
// switch back, and the window starts on the pointer tool.
func TestObbToolButtonAndKey(t *testing.T) {
	a := newTestApp()
	if a.tool != ToolSelect {
		t.Fatalf("初始 tool = %v，期望 ToolSelect", a.tool)
	}
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyO))
	if a.tool != ToolObb {
		t.Errorf("O 键后 tool = %v，期望 ToolObb", a.tool)
	}
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyP))
	if a.tool != ToolPoly {
		t.Errorf("P 键后 tool = %v，期望 ToolPoly", a.tool)
	}
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyR))
	if a.tool != ToolRect {
		t.Errorf("R 键后 tool = %v，期望 ToolRect", a.tool)
	}
}

// TestObbBoxIsEditableLikeAnyRect: the created box keeps the rectangle editing
// the dialog and the handles offer — a resize drag changes W and Angle as for
// any rotated rectangle.
func TestObbBoxIsEditableLikeAnyRect(t *testing.T) {
	a, tt := tester(t)
	separateClicks(tt)
	a.setTool(ToolObb)
	for _, p := range []Point{{100, 100}, {200, 100}, {200, 160}, {100, 160}} {
		clickImage(t, a, tt, p)
	}
	got := anns(a)
	if len(got) != 1 {
		t.Fatalf("%d 条标注，期望 1", len(got))
	}
	// The rotate handle sits above the top edge midpoint along the box's own
	// axis; dragging it re-angles the box as for any rectangle (§5.2: obb 编辑
	// 不变). Handles belong to the pointer tool, so the box is selected with it.
	a.setTool(ToolSelect)
	clickImage(t, a, tt, Point{150, 130})
	before := anns(a)[0]
	hs := handlePoints(&before, safeZoom(a.zoom))
	if hs[8].Y >= before.CY {
		t.Fatalf("旋转柄位置异常: %+v", hs[8])
	}
	dragImage(t, a, tt, hs[8], Point{hs[8].X + 60, hs[8].Y})
	if after := anns(a)[0]; after.Angle == before.Angle {
		t.Errorf("拖动旋转柄后角度没变: %v", after.Angle)
	}
}
