package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// Tests for the three view tools on the canvas (docs/phase6-design.md §3): the
// hand pans, the zoom tool zooms one step around the press, the marquee drags a
// selection rectangle, and none of the three ever creates, moves or selects by
// the annotation under the press. The strip and the shortcuts are
// toolstrip_test.go.

// localDown/localMove/localUp build canvas input events at a canvas-local DIP,
// the space the view tools measure their drags in.
func localDown(x, y float32, clicks int, mods ui.Modifiers) ui.InputEvent {
	return ui.InputEvent{Kind: ui.InputPointerDown, X: x, Y: y, Button: 0, Clicks: clicks, Mods: mods}
}

func localMove(x, y float32, mods ui.Modifiers) ui.InputEvent {
	return ui.InputEvent{Kind: ui.InputPointerMove, X: x, Y: y, Mods: mods}
}

func localUp(x, y float32, mods ui.Modifiers) ui.InputEvent {
	return ui.InputEvent{Kind: ui.InputPointerUp, X: x, Y: y, Button: 0, Clicks: 1, Mods: mods}
}

// localClick presses and releases at one canvas-local DIP.
func localClick(a *App, x, y float32, mods ui.Modifiers) {
	a.handleCanvasInput(ui.Element{}, localDown(x, y, 1, mods))
	a.handleCanvasInput(ui.Element{}, localUp(x, y, mods))
}

// localDrag presses at from, moves through the midpoint to to and releases
// there, everything in canvas-local DIP.
func localDrag(a *App, from, to Point, mods ui.Modifiers) {
	a.handleCanvasInput(ui.Element{}, localDown(float32(from.X), float32(from.Y), 1, mods))
	a.handleCanvasInput(ui.Element{}, localMove(float32((from.X+to.X)/2), float32((from.Y+to.Y)/2), mods))
	a.handleCanvasInput(ui.Element{}, localMove(float32(to.X), float32(to.Y), mods))
	a.handleCanvasInput(ui.Element{}, localUp(float32(to.X), float32(to.Y), mods))
}

// TestPanToolDragsView: the hand tool pans by the pointer's travel even when the
// press lands on an annotation, and it neither selects nor moves anything.
func TestPanToolDragsView(t *testing.T) {
	a := threeRects(t)
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.setTool(ToolPan)

	// (100,100) is the center of the first box: a plain press there would select
	// and move it.
	localDrag(a, Point{X: 100, Y: 100}, Point{X: 140, Y: 120}, 0)

	if !nearly(a.panX, 40, 1e-9) || !nearly(a.panY, 20, 1e-9) {
		t.Errorf("拖拽 (100,100)→(140,120) 后 pan = (%v,%v)，期望 (40,20)", a.panX, a.panY)
	}
	if a.mode != ModeIdle {
		t.Errorf("松手后 mode = %v，期望 ModeIdle", a.mode)
	}
	wantSelection(t, a)
	if got := a.images[0].Anns[0]; got.CX != 100 || got.CY != 100 || got.W != 100 || got.H != 100 {
		t.Errorf("抓手拖拽改动了标注：%+v", got)
	}
}

// TestZoomToolClick: a click zooms one step in around the pressed point, Alt
// zooms one step out, the image point under the pointer stays under it, and a
// press that travels zooms nothing.
func TestZoomToolClick(t *testing.T) {
	a := threeRects(t)
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.setTool(ToolZoom)

	// The center of the second box: the tool must zoom there, not select it.
	x, y := localOf(a, Point{X: 300, Y: 100})
	localClick(a, x, y, 0)

	if !nearly(a.zoom, zoomStep, 1e-9) {
		t.Fatalf("单击后 zoom = %v，期望 %v", a.zoom, zoomStep)
	}
	if p := a.pointerImage(x, y); !nearly(p.X, 300, 1e-6) || !nearly(p.Y, 100, 1e-6) {
		t.Errorf("缩放后指针下的图像点是 %+v，期望 (300,100)：锚点漂移了", p)
	}
	wantSelection(t, a)

	// Alt is the other direction, around the same point.
	localClick(a, x, y, ui.Alt)
	if !nearly(a.zoom, 1, 1e-9) {
		t.Fatalf("Alt+单击后 zoom = %v，期望回到 1", a.zoom)
	}
	if p := a.pointerImage(x, y); !nearly(p.X, 300, 1e-6) || !nearly(p.Y, 100, 1e-6) {
		t.Errorf("缩小后指针下的图像点是 %+v，期望 (300,100)", p)
	}

	// A press that travelled is a drag, and dragging zooms nothing.
	zoom, panX, panY := a.zoom, a.panX, a.panY
	localDrag(a, Point{X: 400, Y: 300}, Point{X: 420, Y: 300}, 0)
	if a.zoom != zoom || a.panX != panX || a.panY != panY {
		t.Errorf("拖动后 zoom/pan = %v/(%v,%v)，期望不变 %v/(%v,%v)",
			a.zoom, a.panX, a.panY, zoom, panX, panY)
	}
	if a.mode != ModeIdle {
		t.Errorf("松手后 mode = %v，期望 ModeIdle", a.mode)
	}
	wantSelection(t, a)
}

// TestMarqueeToolDrags: the marquee selects what its rectangle touches without
// Shift, starts even on an annotation, and a click without a drag clears the
// selection instead of leaving one behind.
func TestMarqueeToolDrags(t *testing.T) {
	a := threeRects(t)
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.setTool(ToolMarquee)

	localDrag(a, Point{X: 40, Y: 40}, Point{X: 160, Y: 160}, 0)
	wantSelection(t, a, 0)

	// A rectangle that touches nothing selects nothing.
	localDrag(a, Point{X: 700, Y: 500}, Point{X: 740, Y: 540}, 0)
	wantSelection(t, a)

	// A press on an annotation drags a rectangle as well, it does not move it.
	localDrag(a, Point{X: 100, Y: 100}, Point{X: 160, Y: 160}, 0)
	wantSelection(t, a, 0)

	// A click with no travel leaves nothing selected.
	localClick(a, 100, 100, 0)
	wantSelection(t, a)

	// Shift changes nothing here: the marquee tool already is that gesture.
	localDrag(a, Point{X: 100, Y: 100}, Point{X: 160, Y: 160}, ui.Shift)
	wantSelection(t, a, 0)

	if a.panX != 0 || a.panY != 0 {
		t.Errorf("选框拖拽平移了画布：pan = (%v,%v)", a.panX, a.panY)
	}
	if got := len(a.images[0].Anns); got != 4 {
		t.Errorf("选框拖拽后标注数 = %d，期望 4", got)
	}
}

// TestShiftMarqueeInEveryTool: Shift+drag on empty canvas keeps its old meaning
// in every tool, the hand and the zoom tool included (docs/phase6-design.md §3).
func TestShiftMarqueeInEveryTool(t *testing.T) {
	a := threeRects(t)
	a.zoom, a.panX, a.panY = 1, 0, 0

	for _, tool := range []Tool{ToolPan, ToolZoom, ToolMarquee, ToolRect, ToolPoly, ToolObb} {
		a.tool = tool
		a.mode = ModeIdle
		a.clearSelection()
		localDrag(a, Point{X: 40, Y: 40}, Point{X: 160, Y: 160}, ui.Shift)
		wantSelection(t, a, 0)
		if a.panX != 0 || a.panY != 0 {
			t.Fatalf("%v：Shift+框选平移了画布：pan = (%v,%v)", tool, a.panX, a.panY)
		}
		if !nearly(a.zoom, 1, 1e-12) {
			t.Fatalf("%v：Shift+框选缩放了画布：zoom = %v", tool, a.zoom)
		}
	}
}

// TestChipAndZMenuWorkInEveryTool: a category chip and a right click stay
// reachable in all six tools — the chip selects its annotation (a double-click
// opens the category picker) and the right button opens the z-order menu,
// whatever the tool would otherwise do with the press.
func TestChipAndZMenuWorkInEveryTool(t *testing.T) {
	a, tt, _ := stripRig(t)
	a.images[0].Anns = []Ann{rectAnn("a", 0, 400, 300, 200, 100)} // box x 300..500, y 250..350
	a.clearSelection()
	tt.Frame()

	cv := canvasBounds(t, tt)
	// The chip is painted just above the box's top edge (labelchip_test.go).
	chip, ok := chipBox(tt.Image(), cv.X+300, cv.Y+250-26, cv.X+520, cv.Y+248)
	if !ok {
		t.Fatalf("标注上方没有找到标签块")
	}
	cx, cy := chip.X+chip.W/2-cv.X, chip.Y+chip.H/2-cv.Y

	for _, tool := range []Tool{ToolPan, ToolZoom, ToolMarquee, ToolRect, ToolPoly, ToolObb} {
		a.tool = tool
		a.mode = ModeIdle
		a.clearSelection()

		a.handleCanvasInput(ui.Element{}, localDown(cx, cy, 1, 0))
		if a.soleSelection() != 0 {
			t.Fatalf("%v：点标签块后 soleSelection = %d，期望 0", tool, a.soleSelection())
		}
		if a.mode != ModeIdle {
			t.Fatalf("%v：点标签块后 mode = %v，期望 ModeIdle", tool, a.mode)
		}
		if a.panX != 0 || a.panY != 0 {
			t.Fatalf("%v：点标签块平移了画布", tool)
		}
		a.handleCanvasInput(ui.Element{}, localUp(cx, cy, 0))

		a.handleCanvasInput(ui.Element{}, localDown(cx, cy, 2, 0))
		if !a.catPick.Open || a.catPick.Index != 0 {
			t.Fatalf("%v：双击标签块后 catPick = %+v，期望打开第 0 个标注的类别选择", tool, a.catPick)
		}
		a.catPick = catPickState{}
		a.handleCanvasInput(ui.Element{}, localUp(cx, cy, 0))

		// The right button still opens the annotation's z-order menu.
		bx, by := localOf(a, Point{X: 400, Y: 300})
		a.handleCanvasInput(ui.Element{}, ui.InputEvent{
			Kind: ui.InputPointerDown, X: bx, Y: by, Button: 1, Clicks: 1,
		})
		if !a.zMenu.Open || a.zMenu.Index != 0 {
			t.Fatalf("%v：右键标注后 zMenu = %+v，期望打开第 0 个标注的菜单", tool, a.zMenu)
		}
		a.zMenu = zMenuState{}
		a.mode = ModeIdle
	}
}
