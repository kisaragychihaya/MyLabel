package app

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"

	"mylabel/locales"
)

// Tests for the left tool strip (docs/phase6-design.md §1, §2, §4): the icons,
// the strip's layout and highlight, the tool shortcuts, the draft a tool switch
// drops, and the cursor each tool shows. The view tools' behaviour on the canvas
// is toolview_test.go.

// stripRig returns an App showing the real App.View in a Tester of its own, plus
// the theme its frames paint with. Unlike tester, this rig renders the tool
// strip and the category panel, which is what these tests are about; one Tester
// per test function, as each leaks a thread (mygo issue #1902).
func stripRig(t *testing.T) (*App, *ui.Tester, *ui.Theme) {
	t.Helper()
	a := New()
	a.categories = defaultCategoryList()
	a.curCat = 0
	a.images = []ImageInfo{{Path: "s1", Name: "s1.png", Width: 800, Height: 600}}
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
	return a, tt, &theme
}

// stripRects returns the six buttons' boxes in toolSpecs order (top to bottom).
func stripRects(t *testing.T, tt *ui.Tester) []ui.Rect {
	t.Helper()
	out := make([]ui.Rect, len(toolSpecs))
	for i, spec := range toolSpecs {
		r, ok := tt.Find(spec.text())
		if !ok || r.W <= 0 || r.H <= 0 {
			t.Fatalf("工具条按钮 %q 未找到：ok=%v rect=%+v texts=%v", spec.text(), ok, r, tt.Texts())
		}
		out[i] = r
	}
	return out
}

// atPixel is the frame's color at a window DIP.
func atPixel(frame *image.RGBA, x, y float32) color.RGBA {
	return frame.RGBAAt(int(math.Round(float64(x))), int(math.Round(float64(y))))
}

// accentish reports whether c is the face of a primary button, in any of its
// states: the pointer rests on the button the last click went to, so its face is
// the hover or the pressed shade of the accent.
func accentish(theme *ui.Theme, c color.RGBA) bool {
	for _, want := range []ui.Color{theme.Accent, theme.AccentHover, theme.AccentPressed} {
		if closeColor(c, want, 4) {
			return true
		}
	}
	return false
}

// toolIcons returns every drawing the strip can show, by label: each tool's icon
// in the state it starts in, plus the zoom tool's other direction.
func toolIcons() []struct {
	label string
	svg   *ui.SVG
} {
	var zero App
	out := make([]struct {
		label string
		svg   *ui.SVG
	}, 0, len(toolSpecs)+1)
	for _, spec := range toolSpecs {
		out = append(out, struct {
			label string
			svg   *ui.SVG
		}{spec.key, spec.icon(&zero)})
	}
	return append(out, struct {
		label string
		svg   *ui.SVG
	}{"toolZoomOut", iconToolZoomOut})
}

// TestToolIconsParse: every tool has its own 24×24 outline, and none of them
// carries an SVG feature the renderer would have to skip.
func TestToolIconsParse(t *testing.T) {
	if len(toolSpecs) != 7 {
		t.Fatalf("工具条有 %d 个工具，期望 7 个", len(toolSpecs))
	}
	seen := map[*ui.SVG]string{}
	for _, icon := range toolIcons() {
		if icon.svg == nil {
			t.Fatalf("%s 没有图标", icon.label)
		}
		if w, h := icon.svg.Size(); w != 24 || h != 24 {
			t.Errorf("%s: 图标 %v×%v，期望 24×24", icon.label, w, h)
		}
		if un := icon.svg.Unsupported(); len(un) > 0 {
			t.Errorf("%s: 图标含不支持的 SVG 特性 %v", icon.label, un)
		}
		if other, dup := seen[icon.svg]; dup {
			t.Errorf("%s 与 %s 用了同一个图标", icon.label, other)
		}
		seen[icon.svg] = icon.label
	}
}

// TestToolIconsPaint: each icon draws ink inside a box of its size, in the
// theme's text color — a parsed-but-empty icon would paint the background only.
func TestToolIconsPaint(t *testing.T) {
	var theme ui.Theme
	icons := toolIcons()
	tt := ui.NewTester(func(c *ui.Context) {
		theme = *c.Theme()
		ui.Column(c).Fill().Background(theme.Background).Padding(8).Gap(8).Children(func() {
			for _, icon := range icons {
				ui.Box(c).Size(24, 24).Label(icon.label).Children(func() {
					ui.Icon(c, icon.svg).FontSize(24)
				})
			}
		})
	}, 200, 400)

	for _, icon := range icons {
		r, ok := tt.Find(icon.label)
		if !ok {
			t.Fatalf("图标 %s 未找到", icon.label)
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
			t.Errorf("图标 %s 只画出 %d 个非背景像素，%+v 内应是一条可见的轮廓", icon.label, ink, r)
		}
	}
}

// TestToolStripLayout: six square buttons in a narrow column left of the canvas,
// top to bottom in toolSpecs order, the view tools separated from the annotation
// ones by a wider gap, and only the active tool painted with the accent.
func TestToolStripLayout(t *testing.T) {
	a, tt, theme := stripRig(t)
	rects := stripRects(t, tt)

	for i, r := range rects {
		if math.Abs(float64(r.W)-toolButtonDIP) > 1 || math.Abs(float64(r.H)-toolButtonDIP) > 1 {
			t.Errorf("%s: 按钮 %v×%v，期望 %d×%d", toolSpecs[i].key, r.W, r.H, toolButtonDIP, toolButtonDIP)
		}
		if r.X < 0 || r.X+r.W > toolStripWidth {
			t.Errorf("%s: 按钮在 x=%v，宽 %v，超出 %d DIP 宽的工具条", toolSpecs[i].key, r.X, r.W, toolStripWidth)
		}
	}
	for i := 1; i < len(rects); i++ {
		if rects[i].Y <= rects[i-1].Y+rects[i-1].H {
			t.Fatalf("%s 在 %s 的上方或与之重叠：%+v %+v",
				toolSpecs[i].key, toolSpecs[i-1].key, rects[i], rects[i-1])
		}
	}
	cv := canvasBounds(t, tt)
	if cv.X < toolStripWidth {
		t.Errorf("画布从 x=%v 开始，工具条宽 %d DIP，画布被压在工具条下", cv.X, toolStripWidth)
	}
	for i, r := range rects {
		if r.X+r.W > cv.X {
			t.Errorf("%s: 按钮右边界 %v 覆盖画布起点 %v", toolSpecs[i].key, r.X+r.W, cv.X)
		}
	}
	// The divider sits between the pointer and view tools and the creation ones,
	// so that gap is wider than the plain gap between two buttons.
	i := toolStripDividerAfter
	gap := rects[i].Y - (rects[i-1].Y + rects[i-1].H)
	plain := rects[1].Y - (rects[0].Y + rects[0].H)
	if gap <= plain+4 {
		t.Errorf("指针/视图工具与创建工具之间只有 %v DIP 的间距，普通间距 %v：缺少分隔线", gap, plain)
	}

	// The default tool is the pointer one; its button shows the accent, the
	// others show the surface the strip is made of.
	frame := tt.Image()
	if a.tool != ToolSelect {
		t.Fatalf("默认工具 = %v，期望 ToolSelect", a.tool)
	}
	for i, r := range rects {
		// A point inside the button's face, left of its centered 18 DIP icon.
		got := atPixel(frame, r.X+4, r.Y+r.H/2)
		active := accentish(theme, got)
		if want := toolSpecs[i].tool == a.tool; active != want {
			t.Errorf("%s: 按钮面色 %v，accent %v，active 应为 %v", toolSpecs[i].key, got, theme.Accent, want)
		}
	}

	// The icon sits in the middle of its square, not pushed to a side: a
	// highlighted button's face blends into the strip's surface along its rounded
	// corners, and those edge pixels read as ink, so this frame clears the
	// highlight and measures only the drawing.
	a.tool = Tool(-1) // no button active
	tt.Frame()
	frame = tt.Image()
	for i, r := range rects {
		ink, ok := inkBounds(frame, r, theme.Surface)
		if !ok {
			t.Fatalf("%s: %+v 内没有画出图标", toolSpecs[i].key, r)
		}
		cx, cy := ink.X+ink.W/2, ink.Y+ink.H/2
		if dx, dy := math.Abs(float64(cx-(r.X+r.W/2))), math.Abs(float64(cy-(r.Y+r.H/2))); dx > 1 || dy > 1 {
			t.Errorf("%s: 图标中心 (%v,%v)，按钮中心 (%v,%v)，偏移 (%v,%v) 超过 1 DIP",
				toolSpecs[i].key, cx, cy, r.X+r.W/2, r.Y+r.H/2, dx, dy)
		}
	}
}

// inkBounds returns the box of the pixels inside a button that are neither its
// face nor the theme's background: the icon it draws, in window DIPs. The scan
// keeps inset DIP off the edges, which skips the button's border and the arc of
// its rounded corners (the arc reaches in by radius*(1-1/√2) at most, under 3
// DIP for a 32 DIP square) while still covering the icon around the middle.
func inkBounds(frame *image.RGBA, r ui.Rect, face ui.Color) (ui.Rect, bool) {
	const inset = 4
	minX, minY := float32(math.MaxFloat32), float32(math.MaxFloat32)
	maxX, maxY := -minX, -minY
	for y := int(r.Y + inset); y < int(r.Y+r.H-inset); y++ {
		for x := int(r.X + inset); x < int(r.X+r.W-inset); x++ {
			c := frame.RGBAAt(x, y)
			if closeColor(c, face, 10) {
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

// TestToolStripZoomIcon: the zoom tool's button shows the magnifier with a plus
// while the tool zooms in and the one with a minus while it zooms out, and
// clicking that button is what flips it. Its name and tooltip stay the same
// either way.
func TestToolStripZoomIcon(t *testing.T) {
	var zero App
	zoom := -1
	for i, spec := range toolSpecs {
		if spec.tool == ToolZoom {
			zoom = i
		}
	}
	if zoom < 0 {
		t.Fatalf("工具条里没有缩放工具")
	}
	spec := toolSpecs[zoom]
	if iconToolZoomIn == iconToolZoomOut {
		t.Fatalf("放大与缩小的图标是同一个 SVG")
	}
	if got := spec.icon(&zero); got != iconToolZoomIn {
		t.Errorf("zoomOutNext = false 时图标不是放大镜+（%p），而是 %p", iconToolZoomIn, got)
	}
	if got := spec.icon(&App{zoomOutNext: true}); got != iconToolZoomOut {
		t.Errorf("zoomOutNext = true 时图标不是放大镜−（%p），而是 %p", iconToolZoomOut, got)
	}

	// The button switches to the tool first, and flips its direction only once
	// the tool is the active one: the drawing is what changes, the name is not.
	a, tt, _ := stripRig(t)
	rects := stripRects(t, tt)
	r := rects[zoom]
	if err := tt.Click(spec.text()); err != nil {
		t.Fatalf("点击 %q：%v", spec.text(), err)
	}
	if a.tool != ToolZoom || a.zoomOutNext {
		t.Fatalf("第一次点击后 tool = %v，zoomOutNext = %v，期望 ToolZoom 与 false", a.tool, a.zoomOutNext)
	}
	tt.Frame()
	plus := block(tt.Image(), r)
	if err := tt.Click(spec.text()); err != nil {
		t.Fatalf("再次点击 %q：%v", spec.text(), err)
	}
	if !a.zoomOutNext {
		t.Fatalf("再次点击后 zoomOutNext = false，期望 true")
	}
	tt.Frame()
	minus := block(tt.Image(), r)
	if changed := countChanged(plus, minus); changed < 5 {
		t.Errorf("方向翻转后按钮只变了 %d 个像素，期望放大镜+/− 明显不同", changed)
	}
	if got := spec.text(); got != locales.ToolStripZoom() {
		t.Errorf("缩放按钮的文案 %q，期望 %q", got, locales.ToolStripZoom())
	}
}

// block returns the pixels inside a button, off its border and rounded corners,
// row by row.
func block(frame *image.RGBA, r ui.Rect) []color.RGBA {
	const inset = 2
	out := make([]color.RGBA, 0, 28*28)
	for y := int(r.Y + inset); y < int(r.Y+r.H-inset); y++ {
		for x := int(r.X + inset); x < int(r.X+r.W-inset); x++ {
			out = append(out, frame.RGBAAt(x, y))
		}
	}
	return out
}

// countChanged counts the pixels that differ between two blocks of one size.
func countChanged(a, b []color.RGBA) int {
	n := 0
	for i := range a {
		if i >= len(b) {
			break
		}
		if a[i] != b[i] {
			n++
		}
	}
	return n
}

// TestToolStripButtons: clicking a button activates its tool, and the highlight
// follows the click.
func TestToolStripButtons(t *testing.T) {
	a, tt, theme := stripRig(t)
	rects := stripRects(t, tt)

	for _, spec := range toolSpecs {
		if err := tt.Click(spec.text()); err != nil {
			t.Fatalf("点击 %q：%v", spec.text(), err)
		}
		if a.tool != spec.tool {
			t.Fatalf("点击 %q 后 tool = %v，期望 %v", spec.text(), a.tool, spec.tool)
		}
		// The tool switched as a plain selection would: no gesture, no draft.
		if a.mode != ModeIdle || len(a.polyDraft) != 0 {
			t.Fatalf("点击 %q 后 mode = %v，草稿 %d 点", spec.text(), a.mode, len(a.polyDraft))
		}
	}

	// The last click was the rotated-box button: it is the highlighted one.
	tt.Frame()
	frame := tt.Image()
	for i, r := range rects {
		got := atPixel(frame, r.X+4, r.Y+r.H/2)
		if want := toolSpecs[i].tool == ToolObb; accentish(theme, got) != want {
			t.Errorf("%s: 按钮面色 %v，高亮应为 %v", toolSpecs[i].key, got, want)
		}
	}
}

// TestToolStripShortcuts: V reaches the pointer tool, H, Z and M the three view
// tools, R, P and O the creation ones; a modifier or a gesture in progress keeps
// the current tool.
func TestToolStripShortcuts(t *testing.T) {
	a := newTestApp()
	a.zoom, a.panX, a.panY = 1, 0, 0

	for _, tc := range []struct {
		key  ui.Key
		want Tool
	}{
		{ui.KeyV, ToolSelect},
		{ui.KeyH, ToolPan},
		{ui.KeyZ, ToolZoom},
		{ui.KeyM, ToolMarquee},
		{ui.KeyR, ToolRect},
		{ui.KeyP, ToolPoly},
		{ui.KeyO, ToolObb},
	} {
		// Start from another tool, so a shortcut that does nothing fails.
		a.tool = ToolRect
		if tc.want == ToolRect {
			a.tool = ToolPoly
		}
		if !keyWith(a, tc.key, 0) {
			t.Errorf("快捷键 %v 未被画布处理", tc.key)
		}
		if a.tool != tc.want {
			t.Errorf("快捷键 %v 后 tool = %v，期望 %v", tc.key, a.tool, tc.want)
		}
	}

	for _, tc := range []struct {
		name string
		key  ui.Key
		mods ui.Modifiers
	}{
		{"Ctrl+Z 是撤销", ui.KeyZ, ui.Ctrl},
		{"Shift+H 不是抓手", ui.KeyH, ui.Shift},
		{"Alt+M 不是选框", ui.KeyM, ui.Alt},
		{"Alt+V 不是指针", ui.KeyV, ui.Alt},
	} {
		a.tool = ToolRect
		keyWith(a, tc.key, tc.mods)
		if a.tool != ToolRect {
			t.Errorf("%s：组合键把工具切成了 %v", tc.name, a.tool)
		}
	}

	// L keeps its meaning: it toggles the chips, not the tool.
	a.tool = ToolRect
	labels := a.showLabels
	keyWith(a, ui.KeyL, 0)
	if a.showLabels == labels || a.tool != ToolRect {
		t.Errorf("L：showLabels %v→%v，tool = %v", labels, a.showLabels, a.tool)
	}
	keyWith(a, ui.KeyL, 0)
	if a.showLabels != labels {
		t.Errorf("L 两次后 showLabels = %v，期望回到 %v", a.showLabels, labels)
	}

	// Z on the active zoom tool is its direction switch, as the strip's button
	// is: the first press selects the tool, the next ones flip the magnifier.
	a.setTool(ToolRect)
	keyWith(a, ui.KeyZ, 0)
	if a.tool != ToolZoom || a.zoomOutNext {
		t.Fatalf("第一次 Z 后 tool = %v，zoomOutNext = %v，期望 ToolZoom 与 false", a.tool, a.zoomOutNext)
	}
	keyWith(a, ui.KeyZ, 0)
	if a.tool != ToolZoom || !a.zoomOutNext {
		t.Fatalf("第二次 Z 后 tool = %v，zoomOutNext = %v，期望 ToolZoom 与 true", a.tool, a.zoomOutNext)
	}

	// A drag in progress belongs to the tool that started it.
	a.tool = ToolRect
	a.mode = ModeMove
	keyWith(a, ui.KeyH, 0)
	if a.tool != ToolRect {
		t.Errorf("拖拽中切换工具：tool = %v，期望保持 ToolRect", a.tool)
	}
	a.mode = ModeIdle
}

// TestToolSwitchDropsDraft: switching tools drops the click-by-click draft and
// the rubber band the tool being left had in flight, as Escape would.
func TestToolSwitchDropsDraft(t *testing.T) {
	a := newTestApp()
	a.zoom, a.panX, a.panY = 1, 0, 0

	a.setTool(ToolPoly)
	a.handleCanvasInput(ui.Element{}, polyDown(a, Point{X: 100, Y: 100}, 1, 0))
	if a.mode != ModePolyCreate || len(a.polyDraft) != 1 {
		t.Fatalf("多边形草稿：mode = %v，%d 个点", a.mode, len(a.polyDraft))
	}
	a.setTool(ToolRect)
	if len(a.polyDraft) != 0 || a.mode != ModeIdle || a.tool != ToolRect {
		t.Fatalf("换工具后仍在起草稿：tool = %v，mode = %v，%d 个点", a.tool, a.mode, len(a.polyDraft))
	}

	// The same for a rectangle drag in flight: it creates nothing afterwards.
	a.handleCanvasInput(ui.Element{}, polyDown(a, Point{X: 200, Y: 200}, 1, 0))
	a.handleCanvasInput(ui.Element{}, polyMove(a, Point{X: 300, Y: 300}, 0))
	if a.mode != ModeCreating {
		t.Fatalf("矩形拖拽：mode = %v，期望 ModeCreating", a.mode)
	}
	a.setTool(ToolPoly)
	if a.mode != ModeIdle {
		t.Fatalf("换工具后 mode = %v，期望 ModeIdle", a.mode)
	}
	a.handleCanvasInput(ui.Element{}, polyUp(a, Point{X: 300, Y: 300}))
	if got := len(anns(a)); got != 0 {
		t.Errorf("被放弃的拖拽留下了 %d 个标注", got)
	}
}

// TestCanvasCursor: the hand shows over the pan tool and while Space pans, the
// arrow over the pointer tool, the pointer over the zoom tool, and the crosshair
// for everything that draws or selects a rectangle.
func TestCanvasCursor(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tool     Tool
		spacePan bool
		want     ui.Cursor
	}{
		{"hand tool", ToolPan, false, ui.CursorGrab},
		{"space pans in any tool", ToolRect, true, ui.CursorGrab},
		{"select tool", ToolSelect, false, ui.CursorDefault},
		{"zoom tool", ToolZoom, false, ui.CursorPointer},
		{"marquee tool", ToolMarquee, false, ui.CursorCrosshair},
		{"rect tool", ToolRect, false, ui.CursorCrosshair},
		{"poly tool", ToolPoly, false, ui.CursorCrosshair},
		{"obb tool", ToolObb, false, ui.CursorCrosshair},
	} {
		if got := canvasCursor(tc.tool, tc.spacePan); got != tc.want {
			t.Errorf("%s: canvasCursor = %v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// TestToolStripLabels: every button is named by its locale message, and that
// message carries the button's shortcut, so an icon-only button still says what
// it does.
func TestToolStripLabels(t *testing.T) {
	want := []string{
		locales.ToolStripSelect(), locales.ToolStripPan(), locales.ToolStripZoom(),
		locales.ToolStripMarquee(), locales.ToolStripRect(), locales.ToolStripPoly(),
		locales.ToolStripObb(),
	}
	if len(toolSpecs) != len(want) {
		t.Fatalf("工具条有 %d 个按钮，期望 %d 个", len(toolSpecs), len(want))
	}
	for i, spec := range toolSpecs {
		if got := spec.text(); got != want[i] {
			t.Errorf("%s: 文案 %q，期望 %q", spec.key, got, want[i])
		}
	}
	keys := map[string]bool{}
	for _, spec := range toolSpecs {
		if keys[spec.key] {
			t.Errorf("按钮 Key %q 重复", spec.key)
		}
		keys[spec.key] = true
	}
}
