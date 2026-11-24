package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// Tests for the pointer tool and the create/edit split (docs/phase6-design.md
// §6): the creation tools only ever start a new annotation, wherever the press
// lands, and the pointer tool is the one that selects, moves and edits. The strip
// is toolstrip_test.go, the view tools toolview_test.go.

// TestDefaultToolIsSelect: a fresh window starts on the pointer tool, so a press
// on an annotation selects it instead of drawing a new box over it, and the three
// creation tools are the ones that create.
func TestDefaultToolIsSelect(t *testing.T) {
	a := New()
	if a.tool != ToolSelect {
		t.Fatalf("New() 的 tool = %v，期望 ToolSelect（指针）", a.tool)
	}
	for _, tool := range []Tool{ToolRect, ToolPoly, ToolObb} {
		if !isCreationTool(tool) {
			t.Errorf("%v 应是创建工具", tool)
		}
	}
	for _, tool := range []Tool{ToolSelect, ToolPan, ToolZoom, ToolMarquee} {
		if isCreationTool(tool) {
			t.Errorf("%v 不该是创建工具", tool)
		}
	}
}

// TestCreateToolsIgnoreAnnotations: with a creation tool active, a press on an
// existing annotation starts a new annotation instead of selecting or moving it,
// and the selection stays empty — the creation tools have none.
func TestCreateToolsIgnoreAnnotations(t *testing.T) {
	t.Run("rect", func(t *testing.T) {
		a := threeRects(t)
		a.zoom, a.panX, a.panY = 1, 0, 0
		a.setTool(ToolRect)
		before := anns(a)[0]

		// (100,100) is the center of the first box: the pointer tool would select
		// it and a drag would move it.
		a.handleCanvasInput(ui.Element{}, localDown(100, 100, 1, 0))
		if a.mode != ModeCreating {
			t.Fatalf("按下已有标注后 mode = %v，期望 ModeCreating", a.mode)
		}
		wantSelection(t, a)
		a.handleCanvasInput(ui.Element{}, localMove(140, 130, 0))
		a.handleCanvasInput(ui.Element{}, localUp(140, 130, 0))

		got := anns(a)
		if len(got) != 5 {
			t.Fatalf("标注数 = %d，期望 5（新加一个）", len(got))
		}
		nb := got[len(got)-1]
		if nb.Kind != KindRect || !nearly(nb.CX, 120, 1e-6) || !nearly(nb.CY, 115, 1e-6) ||
			!nearly(nb.W, 40, 1e-6) || !nearly(nb.H, 30, 1e-6) {
			t.Errorf("新标注 = %+v，期望中心 (120,115) 的 40×30 矩形", nb)
		}
		if g := got[0]; g.CX != before.CX || g.CY != before.CY || g.W != before.W ||
			g.H != before.H || g.Angle != before.Angle {
			t.Errorf("已有标注被改动了：%+v → %+v", before, g)
		}
		wantSelection(t, a)
	})

	t.Run("poly", func(t *testing.T) {
		a := threeRects(t)
		a.zoom, a.panX, a.panY = 1, 0, 0
		a.setTool(ToolPoly)
		before := anns(a)[0]

		a.handleCanvasInput(ui.Element{}, polyDown(a, Point{100, 100}, 1, 0))
		if a.mode != ModePolyCreate || len(a.polyDraft) != 1 {
			t.Fatalf("多边形工具下按下已有标注：mode = %v，草稿 %v，期望开新草稿", a.mode, a.polyDraft)
		}
		if a.draft.Kind != KindPoly {
			t.Errorf("草稿 kind = %q，期望 %q", a.draft.Kind, KindPoly)
		}
		wantSelection(t, a)
		if g := anns(a)[0]; g.CX != before.CX || g.CY != before.CY || g.W != before.W || g.H != before.H {
			t.Errorf("已有标注被改动了：%+v → %+v", before, g)
		}

		// Escape drops the draft, as it always did.
		keyWith(a, ui.KeyEscape, 0)
		if a.mode != ModeIdle || a.polyDraft != nil {
			t.Fatalf("Esc 后 mode = %v，草稿 %v", a.mode, a.polyDraft)
		}
		if n := len(anns(a)); n != 4 {
			t.Errorf("标注数 = %d，期望仍是 4", n)
		}
	})

	t.Run("obb", func(t *testing.T) {
		a := threeRects(t)
		a.zoom, a.panX, a.panY = 1, 0, 0
		a.setTool(ToolObb)

		a.handleCanvasInput(ui.Element{}, polyDown(a, Point{100, 100}, 1, 0))
		if a.mode != ModePolyCreate || len(a.polyDraft) != 1 {
			t.Fatalf("旋转框工具下按下已有标注：mode = %v，草稿 %v，期望开新草稿", a.mode, a.polyDraft)
		}
		if a.draft.Kind != KindRect {
			t.Errorf("草稿 kind = %q，期望 %q（旋转框在模型里是旋转矩形）", a.draft.Kind, KindRect)
		}
		wantSelection(t, a)
	})

	t.Run("shift on an annotation still creates", func(t *testing.T) {
		a := threeRects(t)
		a.zoom, a.panX, a.panY = 1, 0, 0
		a.setTool(ToolRect)

		a.handleCanvasInput(ui.Element{}, localDown(100, 100, 1, ui.Shift))
		if a.mode != ModeCreating {
			t.Fatalf("Shift 按在标注上：mode = %v，期望 ModeCreating（创建工具在任何位置都新建）", a.mode)
		}
		a.handleCanvasInput(ui.Element{}, localUp(100, 100, ui.Shift))

		// Shift on empty canvas keeps its old meaning: a selection rectangle.
		a.handleCanvasInput(ui.Element{}, localDown(700, 500, 1, ui.Shift))
		if a.mode != ModeMarquee {
			t.Fatalf("Shift 按在空白处：mode = %v，期望 ModeMarquee", a.mode)
		}
		a.handleCanvasInput(ui.Element{}, localUp(700, 500, ui.Shift))
	})
}

// TestSelectToolEdits is the pointer tool's half of the split: select, move,
// multi-select, delete, the category digit and the double-click on a body that
// opens the properties dialog.
func TestSelectToolEdits(t *testing.T) {
	a := threeRects(t)
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.setTool(ToolSelect)

	// A click selects the box under it, a drag moves it, and one undo puts it
	// back.
	localClick(a, 100, 100, 0)
	wantSelection(t, a, 0)
	if a.mode != ModeIdle {
		t.Fatalf("单击后 mode = %v，期望 ModeIdle", a.mode)
	}
	localDrag(a, Point{100, 100}, Point{140, 130}, 0)
	if got := anns(a)[0]; !nearly(got.CX, 140, 1e-6) || !nearly(got.CY, 130, 1e-6) {
		t.Errorf("拖动后中心 = (%v,%v)，期望 (140,130)", got.CX, got.CY)
	}
	a.undo()
	if got := anns(a)[0]; !nearly(got.CX, 100, 1e-9) || !nearly(got.CY, 100, 1e-9) {
		t.Errorf("撤销后中心 = (%v,%v)，期望 (100,100)", got.CX, got.CY)
	}

	// The category digit reaches the selection here, as Ctrl+A and Delete do.
	keyWith(a, ui.Key3, 0)
	if got := anns(a)[0].Cat; got != 2 {
		t.Errorf("按下 3 后类别 = %d，期望 2", got)
	}
	keyWith(a, ui.KeyA, ui.Ctrl)
	if n := a.selectionCount(); n != 4 {
		t.Fatalf("Ctrl+A 后选中 %d 个，期望 4 个", n)
	}
	keyWith(a, ui.KeyDelete, 0)
	if n := len(anns(a)); n != 0 {
		t.Fatalf("Delete 后标注数 = %d，期望 0", n)
	}
	a.undo()
	if n := len(anns(a)); n != 4 {
		t.Fatalf("撤销后标注数 = %d，期望 4", n)
	}

	// A double-click on a body opens the properties dialog; the release of the
	// second press without travel is what opens it (§5.1).
	a.clearSelection()
	a.handleCanvasInput(ui.Element{}, localDown(300, 100, 2, 0))
	a.handleCanvasInput(ui.Element{}, localUp(300, 100, 0))
	if !a.props.Open || a.props.Index != 1 {
		t.Errorf("双击标注本体后 props = %+v，期望打开第 1 个标注", a.props)
	}
}

// TestCreationToolClearsSelection: the selection is dropped on the way into a
// creation tool, where no handles are drawn and Delete has nothing to delete,
// and it survives a move between the pointer, the marquee and the view tools.
func TestCreationToolClearsSelection(t *testing.T) {
	a := threeRects(t)
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.setTool(ToolSelect)
	localClick(a, 100, 100, 0)
	wantSelection(t, a, 0)

	a.setTool(ToolRect)
	wantSelection(t, a)
	keyWith(a, ui.KeyDelete, 0)
	if n := len(anns(a)); n != 4 {
		t.Fatalf("创建工具下 Delete 删掉了 %d 个标注，期望一个都不删", 4-n)
	}

	// Back on the pointer tool the box is selectable again.
	a.setTool(ToolSelect)
	localClick(a, 100, 100, 0)
	wantSelection(t, a, 0)

	// Neither the marquee, the hand nor the zoom tool is a creation tool: the
	// selection stays through them.
	for _, tool := range []Tool{ToolMarquee, ToolSelect, ToolPan, ToolZoom, ToolSelect} {
		a.setTool(tool)
		wantSelection(t, a, 0)
	}
}
