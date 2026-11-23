package app

import (
	"maps"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The annotation properties dialog (docs/phase3-design.md §5.1): it opens on a
// double-click, stages the category and the attributes, commits them as one
// undo step on 确定, and drops them all on 取消.

// separateClicks breaks the Tester's double-click tracking before a test starts
// clicking. One Tester serves the whole package and its clock only moves when a
// test advances it, so a click at the same window position as the previous
// test's last click would otherwise arrive as a double-click.
func separateClicks(tt *ui.Tester) { tt.Advance(600 * time.Millisecond) }

// propsFixture returns an App with one selected-friendly rectangle at (200,175)
// carrying two attributes, shown in the panel Tester.
func propsFixture(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	a := newTestApp()
	an := attrsAnn("ann_1", 1)
	an.CX, an.CY, an.W, an.H = 200, 175, 200, 150
	a.images[0].Anns = []Ann{an}
	tt := panelFor(t, a)
	separateClicks(tt)
	return a, tt
}

// doubleClickImage clicks image point p twice, which the Tester reports as a
// double-click (the two presses are within the platform's double-click window).
// The clock is advanced first so the first press is a plain click even when an
// earlier click landed on p: a press that already carries Clicks=2 would open
// the dialog on its own, and the second press would then land on the dialog's
// backdrop and dismiss it.
func doubleClickImage(t *testing.T, a *App, tt *ui.Tester, p Point) {
	t.Helper()
	tt.Advance(600 * time.Millisecond)
	clickImage(t, a, tt, p)
	clickImage(t, a, tt, p)
}

// openPropsDialog double-clicks the body of the fixture's annotation and checks
// the dialog opened.
func openPropsDialog(t *testing.T, a *App, tt *ui.Tester) {
	t.Helper()
	doubleClickImage(t, a, tt, Point{200, 175})
	tt.Frame()
	if !a.props.Open {
		t.Fatalf("双击标注后属性面板没打开（mode %v, status %q, texts %v）", a.mode, a.lastStatus, tt.Texts())
	}
	if !tt.HasText("标注属性") {
		t.Fatalf("属性面板里没有标题，texts = %v", tt.Texts())
	}
}

// TestPropsModalOpensOnDoubleClick: a double-click on a selected annotation
// shows its category and its attributes, while a single click does not (§5.1).
func TestPropsModalOpensOnDoubleClick(t *testing.T) {
	a, tt := propsFixture(t)

	clickImage(t, a, tt, Point{200, 175})
	tt.Frame()
	if a.props.Open {
		t.Fatal("单击就打开了属性面板")
	}

	openPropsDialog(t, a, tt)
	if a.soleSelection() != 0 {
		t.Errorf("打开属性面板后 soleSelection = %d，期望 0", a.soleSelection())
	}
	if len(a.props.Rows) != 2 {
		t.Errorf("属性行 = %+v，期望 2 行", a.props.Rows)
	}
	if a.props.Cat != 1 {
		t.Errorf("面板暂存类别 = %d，期望 1", a.props.Cat)
	}
	// The dialog's text fields are painted, not text nodes: what the tests can
	// read is each row's accessible label, so the staged keys are checked on the
	// state and the labels stand for the rows' presence.
	for _, want := range []string{"属性键1", "属性值1", "属性键2", "属性值2"} {
		if !tt.HasText(want) {
			t.Errorf("属性面板里没有 %q，texts = %v", want, tt.Texts())
		}
	}
}

// TestPropsModalCommitIsOneUndo changes the category and adds an attribute in
// the dialog, then commits both as a single undo step.
func TestPropsModalCommitIsOneUndo(t *testing.T) {
	a, tt := propsFixture(t)
	openPropsDialog(t, a, tt)

	// Pick a new category through the radio group.
	if err := tt.Click("cat3"); err != nil {
		t.Fatalf("点击类别 cat3: %v（texts %v）", err, tt.Texts())
	}
	tt.Frame()
	if a.props.Cat != 2 {
		t.Fatalf("面板暂存类别 = %d，期望 2（cat3）", a.props.Cat)
	}

	// Add a row and type a key and a value into it.
	if err := tt.Click("添加属性"); err != nil {
		t.Fatalf("点击添加属性: %v", err)
	}
	tt.Frame()
	if len(a.props.Rows) != 3 {
		t.Fatalf("添加后 %d 行，期望 3: %+v", len(a.props.Rows), a.props.Rows)
	}
	if err := tt.Click("属性键3"); err != nil {
		t.Fatalf("点击新行的键输入框: %v", err)
	}
	tt.Type("iscrowd")
	if err := tt.Click("属性值3"); err != nil {
		t.Fatalf("点击新行的值输入框: %v", err)
	}
	tt.Type("1")
	tt.Frame()
	if a.props.Rows[2].Key != "iscrowd" || a.props.Rows[2].Value != "1" {
		t.Fatalf("输入后新行 = %+v，期望 iscrowd=1", a.props.Rows[2])
	}

	// Commit: one command, one undo step for category and attributes together.
	if err := tt.Click("确定"); err != nil {
		t.Fatalf("点击确定: %v", err)
	}
	tt.Frame()
	if a.props.Open {
		t.Fatal("确定后面板仍开着")
	}
	if n := len(a.undoStack); n != 1 {
		t.Fatalf("undo 栈 = %d 条，期望 1 条（类别与属性一起提交）", n)
	}
	an := a.images[0].Anns[0]
	if an.Cat != 2 {
		t.Errorf("提交后类别 = %d，期望 2", an.Cat)
	}
	want := map[string]string{"difficult": "1", "备注": "划痕", "iscrowd": "1"}
	if !maps.Equal(an.Attributes, want) {
		t.Errorf("提交后 attributes = %v，期望 %v", an.Attributes, want)
	}
	if !a.dirty || len(a.dirtyImages) != 0 {
		t.Errorf("提交后 dirty = %v, dirtyImages = %v（测试 App 无库，dirtyImages 应为空）", a.dirty, a.dirtyImages)
	}

	// The single undo takes both changes back.
	a.undo()
	back := a.images[0].Anns[0]
	if back.Cat != 1 {
		t.Errorf("撤销后类别 = %d，期望 1", back.Cat)
	}
	if want := attrsAnn("ann_1", 1).Attributes; !maps.Equal(back.Attributes, want) {
		t.Errorf("撤销后 attributes = %v，期望 %v", back.Attributes, want)
	}
}

// TestPropsModalCancelDiscards: 取消 drops the staged category and attributes
// and leaves the undo stack alone.
func TestPropsModalCancelDiscards(t *testing.T) {
	a, tt := propsFixture(t)
	openPropsDialog(t, a, tt)

	if err := tt.Click("cat5"); err != nil {
		t.Fatalf("点击类别 cat5: %v", err)
	}
	tt.Frame()
	if err := tt.Click("删除属性1"); err != nil {
		t.Fatalf("点击删除属性1: %v", err)
	}
	tt.Frame()
	if len(a.props.Rows) != 1 {
		t.Fatalf("删除后 %d 行，期望 1: %+v", len(a.props.Rows), a.props.Rows)
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("点击取消: %v", err)
	}
	tt.Frame()

	if a.props.Open {
		t.Fatal("取消后面板仍开着")
	}
	if n := len(a.undoStack); n != 0 {
		t.Errorf("取消后 undo 栈 = %d 条，期望 0", n)
	}
	an := a.images[0].Anns[0]
	if an.Cat != 1 {
		t.Errorf("取消后类别 = %d，期望 1", an.Cat)
	}
	if want := attrsAnn("ann_1", 1).Attributes; !maps.Equal(an.Attributes, want) {
		t.Errorf("取消后 attributes = %v，期望 %v", an.Attributes, want)
	}
}

// TestPropsModalDeletesAllAttributes: deleting every row and committing leaves
// the annotation with no attributes at all (nil, not an empty map).
func TestPropsModalDeletesAllAttributes(t *testing.T) {
	a, tt := propsFixture(t)
	openPropsDialog(t, a, tt)

	// Deleting row 1 shifts the second row into its place, so the delete button
	// labelled 1 always removes the first remaining row.
	for range a.props.Rows {
		if err := tt.Click("删除属性1"); err != nil {
			t.Fatalf("点击删除属性1: %v（rows %+v）", err, a.props.Rows)
		}
		tt.Frame()
	}
	if len(a.props.Rows) != 0 {
		t.Fatalf("删空后 %d 行，期望 0", len(a.props.Rows))
	}
	if !tt.HasText("还没有属性") {
		t.Errorf("删空后没有提示，texts = %v", tt.Texts())
	}
	if err := tt.Click("确定"); err != nil {
		t.Fatalf("点击确定: %v", err)
	}
	tt.Frame()
	if an := a.images[0].Anns[0]; an.Attributes != nil {
		t.Errorf("提交后 attributes = %v，期望 nil", an.Attributes)
	}
	if n := len(a.undoStack); n != 1 {
		t.Errorf("undo 栈 = %d 条，期望 1", n)
	}
}

// TestPropsModalUnchangedCommitsNothing: pressing 确定 without an edit must not
// push an undo step.
func TestPropsModalUnchangedCommitsNothing(t *testing.T) {
	a, tt := propsFixture(t)
	openPropsDialog(t, a, tt)
	if err := tt.Click("确定"); err != nil {
		t.Fatalf("点击确定: %v", err)
	}
	tt.Frame()
	if n := len(a.undoStack); n != 0 {
		t.Errorf("没改任何东西，undo 栈 = %d 条，期望 0", n)
	}
	if a.props.Open {
		t.Error("确定后面板仍开着")
	}
}

// TestPropsModalClosesWhenAnnotationVanishes: the dialog names its annotation
// by index, so an undo that removes it must close the dialog instead of letting
// it write to whatever moved into that index.
func TestPropsModalClosesWhenAnnotationVanishes(t *testing.T) {
	a, tt := propsFixture(t)
	openPropsDialog(t, a, tt)

	a.images[0].Anns = nil
	tt.Frame()
	if a.props.Open {
		t.Error("标注消失后面板仍开着")
	}
}

// TestPropsModalBlocksCanvasShortcuts: while the dialog is open, a canvas key
// must not delete the annotation behind it.
func TestPropsModalBlocksCanvasShortcuts(t *testing.T) {
	a, tt := propsFixture(t)
	openPropsDialog(t, a, tt)

	handled := a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyDelete})
	if handled {
		t.Error("属性面板开着时画布仍处理了 Delete")
	}
	if len(a.images[0].Anns) != 1 {
		t.Errorf("属性面板开着时标注被删了: %+v", a.images[0].Anns)
	}
	// Escape reaches the dialog, not the canvas: the canvas reports it unhandled
	// so ui.Modal can dismiss the dialog with it.
	if a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEscape}) {
		t.Error("属性面板开着时画布处理了 Escape")
	}
	if a.selectionCount() == 0 {
		t.Error("属性面板开着时画布清掉了选择")
	}
}

// TestPolyDoubleClickDispatch is §5.1 against §4.3: a double-click on a
// polygon's edge still inserts a vertex, a double-click inside it opens the
// properties dialog.
func TestPolyDoubleClickDispatch(t *testing.T) {
	a := newTestApp()
	poly := squarePolyAnn("poly_1", 0) // (100,100)-(300,250)
	a.images[0].Anns = []Ann{poly}
	tt := panelFor(t, a)
	separateClicks(tt)
	tt.Frame()

	// Select it first (a single click inside), so the double-click's presses do
	// not race the selection.
	clickImage(t, a, tt, Point{200, 175})
	if a.soleSelection() != 0 {
		t.Fatalf("soleSelection = %d，期望 0", a.soleSelection())
	}

	// On the edge: the double-click inserts a vertex there and opens nothing.
	a.handleCanvasInput(ui.Element{}, polyDown(a, Point{300, 175}, 2, 0))
	a.handleCanvasInput(ui.Element{}, polyUp(a, Point{300, 175}))
	tt.Frame()
	if a.props.Open {
		t.Fatal("双击多边形的边打开了属性面板")
	}
	if got := len(a.images[0].Anns[0].Points); got != 5 {
		t.Fatalf("双击边后顶点数 = %d，期望 5 个（插入一个）", got)
	}

	// Inside, far from every edge: the properties dialog opens.
	doubleClickImage(t, a, tt, Point{200, 175})
	tt.Frame()
	if !a.props.Open {
		t.Fatalf("双击多边形内部没打开属性面板（mode %v）", a.mode)
	}
	if got := len(a.images[0].Anns[0].Points); got != 5 {
		t.Errorf("双击内部后又插入了顶点: %d 个", got)
	}
	if !tt.HasText("多边形") {
		t.Errorf("面板没有标出多边形，texts = %v", tt.Texts())
	}
}

// TestRectDoubleClickDoesNotMove: the double-click that opens the dialog must
// not leave a move behind — the annotation stays where it was and no undo step
// is pushed by the press.
func TestRectDoubleClickDoesNotMove(t *testing.T) {
	a, tt := propsFixture(t)
	openPropsDialog(t, a, tt)
	an := a.images[0].Anns[0]
	if an.CX != 200 || an.CY != 175 {
		t.Errorf("双击后标注移动到了 (%v,%v)", an.CX, an.CY)
	}
	if n := len(a.undoStack); n != 0 {
		t.Errorf("双击后 undo 栈 = %d 条，期望 0", n)
	}
}
