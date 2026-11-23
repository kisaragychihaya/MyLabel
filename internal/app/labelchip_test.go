package app

import (
	"fmt"
	"image"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestContrastTextColor: the chip's text is black or white by the background's
// perceived luminance, with the threshold in the middle of the greys.
func TestContrastTextColor(t *testing.T) {
	white := ui.RGB(0xff, 0xff, 0xff)
	black := ui.RGB(0, 0, 0)
	cases := []struct {
		name string
		bg   ui.Color
		want ui.Color
	}{
		{"black", ui.RGB(0, 0, 0), white},
		{"white", ui.RGB(0xff, 0xff, 0xff), black},
		{"dark grey under the threshold", ui.RGB(127, 127, 127), white},
		{"light grey over the threshold", ui.RGB(128, 128, 128), black},
		{"red takes white", ui.RGB(220, 40, 40), white},
		{"green takes black", ui.RGB(40, 200, 40), black},
		{"blue takes white", ui.RGB(30, 60, 200), white},
		{"yellow takes black", ui.RGB(240, 220, 40), black},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := contrastTextColor(c.bg)
			if got.R != c.want.R || got.G != c.want.G || got.B != c.want.B {
				t.Errorf("contrastTextColor(%v) = %v，期望 %v", c.bg, got, c.want)
			}
		})
	}
}

// TestChipsFor: the chip policy is off when the switch is off, on for every
// annotation up to the limit, and selection-only above it.
func TestChipsFor(t *testing.T) {
	cases := []struct {
		show  bool
		count int
		want  chipDraw
	}{
		{false, 0, chipOff},
		{false, 3, chipOff},
		{true, 0, chipAll},
		{true, 1, chipAll},
		{true, chipLabelLimit, chipAll},
		{true, chipLabelLimit + 1, chipSelOnly},
		{true, 5000, chipSelOnly},
	}
	for _, c := range cases {
		if got := chipsFor(c.show, c.count); got != c.want {
			t.Errorf("chipsFor(%v, %d) = %v，期望 %v", c.show, c.count, got, c.want)
		}
	}
}

// chipBox finds the chip painted in a band by scanning for non-black pixels
// (the marker image's content is black), and returns its bounds in frame
// pixels.
func chipBox(frame *image.RGBA, x0, y0, x1, y1 float32) (ui.Rect, bool) {
	minX, minY := float32(math.MaxFloat32), float32(math.MaxFloat32)
	maxX, maxY := -minX, -minY
	for y := int(y0); y <= int(y1); y++ {
		for x := int(x0); x <= int(x1); x++ {
			if blackish(frame.RGBAAt(x, y)) {
				continue
			}
			minX, maxX = min(minX, float32(x)), max(maxX, float32(x))
			minY, maxY = min(minY, float32(y)), max(maxY, float32(y))
		}
	}
	if maxX < minX {
		return ui.Rect{}, false
	}
	return ui.Rect{X: minX, Y: minY, W: maxX - minX, H: maxY - minY}, true
}

// countColor counts the pixels of r close to want.
func countColor(frame *image.RGBA, r ui.Rect, want ui.Color, tol int) int {
	n := 0
	for y := int(r.Y); y <= int(r.Y+r.H); y++ {
		for x := int(r.X); x <= int(r.X+r.W); x++ {
			if closeColor(frame.RGBAAt(x, y), want, tol) {
				n++
			}
		}
	}
	return n
}

// TestCategoryChipIsDrawn: a single annotation gets a chip on its box's top
// edge, in the category color, carrying its name in the contrasting color;
// switching the labels off removes it.
func TestCategoryChipIsDrawn(t *testing.T) {
	a, tt := tester(t)
	a.bmp = markerImage()
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.images[0].Anns = []Ann{rectAnn("a", 0, 400, 300, 200, 100)} // box x 300..500, y 250..350
	a.clearSelection()
	tt.Frame()

	cv := canvasBounds(t, tt)
	col := a.catColor(0)
	// The band above the box's top edge (the outline itself is at y = top).
	x0, y0 := cv.X+300, cv.Y+250-26
	x1, y1 := cv.X+520, cv.Y+250-2
	frame := tt.Image()
	chip, ok := chipBox(frame, x0, y0, x1, y1)
	if !ok {
		t.Fatalf("标注上方没有找到标签块：区域 (%v,%v)-(%v,%v)", x0, y0, x1, y1)
	}
	if chip.W < 12 || chip.H < 10 {
		t.Errorf("标签块 %+v 太小，期望一个带文字的 chip", chip)
	}
	// The background is the category color at the chip's opacity over black.
	blend := ui.RGB(uint8(float64(col.R)*annChipAlpha), uint8(float64(col.G)*annChipAlpha), uint8(float64(col.B)*annChipAlpha))
	if n := countColor(frame, chip, blend, 30); n < 30 {
		t.Errorf("标签块里只有 %d 个类别色像素，期望实心底色", n)
	}
	// The name is drawn in the contrasting color.
	if n := countColor(frame, chip, contrastTextColor(col), 30); n < 5 {
		t.Errorf("标签块里只有 %d 个文字色像素，期望类别名文字", n)
	}
	// Its bottom edge sits on the box's top edge.
	if got, want := chip.Y+chip.H, cv.Y+250; math.Abs(float64(got-want)) > 2 {
		t.Errorf("标签底边 y = %v，期望贴住框顶 %v", got, want)
	}

	// Switching the labels off removes the chip and leaves the box alone.
	a.toggleLabels()
	if a.showLabels {
		t.Fatal("切换后标签应关闭")
	}
	tt.Frame()
	frame = tt.Image()
	if block, ok := chipBox(frame, x0, y0, x1, y1); ok {
		t.Errorf("隐藏标签后仍有 chip：%+v", block)
	}
	if !closeColor(frame.RGBAAt(int(cv.X+400), int(cv.Y+250)), col, 40) {
		// The stroke is 1.5 DIP wide and anti-aliased, so check that the top
		// edge still carries color rather than a precise pixel value.
		if blackish(frame.RGBAAt(int(cv.X+400), int(cv.Y+249))) && blackish(frame.RGBAAt(int(cv.X+400), int(cv.Y+250))) {
			t.Errorf("隐藏标签不该影响框的描边")
		}
	}
}

// TestCategoryChipFlipsInsideTheBox: a box hugging the canvas top edge would
// push its chip off screen, so the chip goes inside the box instead.
func TestCategoryChipFlipsInsideTheBox(t *testing.T) {
	a, tt := tester(t)
	a.bmp = markerImage()
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.images[0].Anns = []Ann{rectAnn("a", 0, 400, 20, 200, 40)} // box y 0..40
	a.clearSelection()
	tt.Frame()

	cv := canvasBounds(t, tt)
	frame := tt.Image()
	// Inside the box's own top area, right below the canvas's top edge; the
	// marker image has nothing white there (its marker sits at x 100..110).
	if _, ok := chipBox(frame, cv.X+301, cv.Y+1, cv.X+460, cv.Y+14); !ok {
		t.Errorf("框顶没有翻转进来的 chip")
	}
}

// TestCategoryChipOnlyForSelectionWhenCrowded: past chipLabelLimit the frame
// paints a chip for the selection only, which is what keeps a thousand-box
// frame off the text rasterizer (BenchmarkDrawFrame).
func TestCategoryChipOnlyForSelectionWhenCrowded(t *testing.T) {
	a, tt := tester(t)
	a.bmp = markerImage()
	a.zoom, a.panX, a.panY = 1, 0, 0

	anns := make([]Ann, 0, chipLabelLimit+1)
	for i := 0; i < chipLabelLimit-1; i++ {
		// Filler boxes in the bottom-left corner, out of the two bands checked
		// below.
		anns = append(anns, rectAnn(fmt.Sprintf("f%d", i), 0, float64(30+(i%20)*6), float64(520+(i/20)*3), 4, 4))
	}
	anns = append(anns, rectAnn("sel", 0, 100, 100, 60, 40))   // index 499, selected
	anns = append(anns, rectAnn("plain", 0, 400, 200, 60, 40)) // index 500, not selected
	a.images[0].Anns = anns
	if len(a.images[0].Anns) != chipLabelLimit+1 {
		t.Fatalf("构造了 %d 个标注，期望 %d", len(a.images[0].Anns), chipLabelLimit+1)
	}
	a.selectOnly(len(anns) - 2)
	tt.Frame()

	cv := canvasBounds(t, tt)
	frame := tt.Image()
	// The sel box is centred (100,100) 60x40, so its top edge is at y = 80 and
	// its chip hangs at y 56..77; the plain box's top edge is at 180.
	if _, ok := chipBox(frame, cv.X+70, cv.Y+56, cv.X+170, cv.Y+77); !ok {
		t.Errorf("选中项上方应有 chip")
	}
	if block, ok := chipBox(frame, cv.X+370, cv.Y+156, cv.X+470, cv.Y+177); ok {
		t.Errorf("超过 %d 个标注时未选中项不该有 chip：%+v", chipLabelLimit, block)
	}
}

// TestCategoryChipSkipsUnknownCategory: an annotation whose category index is
// past the list draws no chip and must not panic the frame.
func TestCategoryChipSkipsUnknownCategory(t *testing.T) {
	a, tt := tester(t)
	a.bmp = markerImage()
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.images[0].Anns = []Ann{{ID: "x", Cat: len(a.categories) + 5, CX: 400, CY: 300, W: 100, H: 50}}
	a.clearSelection()
	tt.Frame()

	cv := canvasBounds(t, tt)
	frame := tt.Image()
	if block, ok := chipBox(frame, cv.X+350, cv.Y+275-26, cv.X+460, cv.Y+275-2); ok {
		t.Errorf("类别越界时不该画 chip：%+v", block)
	}
	// The outline still paints in the fallback color.
	if !closeColor(frame.RGBAAt(int(cv.X+400), int(cv.Y+275)), a.catColor(0), 60) {
		t.Errorf("越界类别不该影响描边绘制")
	}
}

// TestLabelToggleKey: L flips the chips, like the toolbar button does.
func TestLabelToggleKey(t *testing.T) {
	a, _ := tester(t)
	if !a.showLabels {
		t.Fatal("标签默认应打开")
	}
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyL))
	if a.showLabels {
		t.Error("按 L 后标签应关闭")
	}
	if !strings.Contains(a.lastStatus, "已隐藏类别标签") {
		t.Errorf("lastStatus = %q，期望提到已隐藏", a.lastStatus)
	}
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyL))
	if !a.showLabels {
		t.Error("再按 L 后标签应打开")
	}
	if !strings.Contains(a.lastStatus, "已显示类别标签") {
		t.Errorf("lastStatus = %q，期望提到已显示", a.lastStatus)
	}
}

// TestLabelButtonToggles: the toolbar's 标签 button shows and hides the chips.
func TestLabelButtonToggles(t *testing.T) {
	a := New()
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			a.toolbar(c)
			a.statusBar(c)
		})
	}, testWinW, 140)
	tt.Advance(600 * time.Millisecond)

	if err := tt.Click("标签 (L)"); err != nil {
		t.Fatalf("点击标签按钮: %v", err)
	}
	if a.showLabels {
		t.Error("点击后标签应关闭")
	}
	if err := tt.Click("标签 (L)"); err != nil {
		t.Fatalf("再次点击标签按钮: %v", err)
	}
	if !a.showLabels {
		t.Error("再次点击后标签应打开")
	}
}
