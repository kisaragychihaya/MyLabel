package app

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The category panel tests render the canvas beside the panel, as App.View
// does. The Tester leaks a thread per instance (mygo issue #1902), so the whole
// package shares one and swaps the App behind it.
var (
	panelAppRef *App
	panelTester *ui.Tester
)

// panelView renders an App the way App.View does, with a plain 48 DIP bar in
// place of the toolbar: the panel, the canvas, the thumbnail strip, the status
// bar, the color palette dialog and the annotation's own dialogs.
func panelView(c *ui.Context) {
	a := panelAppRef
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Box(c).FillWidth().Height(48).Background(t.Surface)
		ui.Row(c).FillWidth().Grow(1).AlignItems(ui.Stretch).Children(func() {
			a.canvasView(c)
			a.categoryPanel(c)
		})
		a.thumbStrip(c)
		a.statusBar(c)
		a.paletteModal(c)
		a.propsModal(c)
		a.catPickModal(c)
	})
}

// panelFor shows a in the shared panel Tester.
func panelFor(t *testing.T, a *App) *ui.Tester {
	t.Helper()
	panelAppRef = a
	if panelTester == nil {
		panelTester = ui.NewTester(panelView, testWinW, testWinH)
	} else {
		panelTester.Frame()
	}
	return panelTester
}

// TestPaletteColorIsGenerated checks the generated palette: the same index
// always paints the same color, neighbors differ, and every index of a long
// list gets a color of its own (docs/phase2-design.md §6).
func TestPaletteColorIsGenerated(t *testing.T) {
	seen := make(map[ui.Color]int, 40)
	for i := 0; i < 40; i++ {
		got := paletteColor(i)
		if again := paletteColor(i); got != again {
			t.Fatalf("paletteColor(%d) = %v, then %v: not deterministic", i, got, again)
		}
		if neighbor := paletteColor(i + 1); got == neighbor {
			t.Errorf("paletteColor(%d) = paletteColor(%d) = %v: neighbors look alike", i, i+1, got)
		}
		if prev, ok := seen[got]; ok {
			t.Errorf("paletteColor(%d) = %v repeats paletteColor(%d)", i, got, prev)
		}
		seen[got] = i
	}
	if paletteColor(-1) != paletteColor(0) {
		t.Errorf("paletteColor(-1) = %v, want paletteColor(0) = %v", paletteColor(-1), paletteColor(0))
	}
}

// TestHsvColor checks the HSV conversion at the primaries and at gray.
func TestHsvColor(t *testing.T) {
	for _, tc := range []struct {
		h, s, v float64
		want    ui.Color
	}{
		{0, 1, 1, ui.RGB(255, 0, 0)},
		{120, 1, 1, ui.RGB(0, 255, 0)},
		{240, 1, 1, ui.RGB(0, 0, 255)},
		{360, 1, 1, ui.RGB(255, 0, 0)},
		{720, 1, 1, ui.RGB(255, 0, 0)},
		{-120, 1, 1, ui.RGB(0, 0, 255)},
		{45, 0, 0.5, ui.RGB(128, 128, 128)},
	} {
		if got := hsvColor(tc.h, tc.s, tc.v); got != tc.want {
			t.Errorf("hsvColor(%v,%v,%v) = %v, want %v", tc.h, tc.s, tc.v, got, tc.want)
		}
	}
}

// TestParseHexColor is the table of accepted and rejected color strings.
func TestParseHexColor(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want ui.Color
		ok   bool
	}{
		{"#ef4444", ui.RGB(0xef, 0x44, 0x44), true},
		{"#EF4444", ui.RGB(0xef, 0x44, 0x44), true},
		{"#000000", ui.RGB(0, 0, 0), true},
		{"#ffffff", ui.RGB(0xff, 0xff, 0xff), true},
		{"ef4444", ui.Color{}, false},   // no '#'
		{"#fff", ui.Color{}, false},     // shorthand
		{"#1234567", ui.Color{}, false}, // too long
		{"#zzzzzz", ui.Color{}, false},  // not hexadecimal
		{"#12345g", ui.Color{}, false},
		{"", ui.Color{}, false},
		{" #ef4444", ui.Color{}, false},
	} {
		got, ok := parseHexColor(tc.in)
		if ok != tc.ok {
			t.Errorf("parseHexColor(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("parseHexColor(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestFormatHexColorRoundTrip checks that formatting and parsing are inverses:
// what the store saves is what the swatch painted.
func TestFormatHexColorRoundTrip(t *testing.T) {
	for i := 0; i < 20; i++ {
		col := paletteColor(i)
		hex := formatHexColor(col)
		back, ok := parseHexColor(hex)
		if !ok || back != col {
			t.Errorf("paletteColor(%d) = %v: %q reads back as %v (ok=%v)", i, col, hex, back, ok)
		}
		if len(hex) != 7 {
			t.Errorf("formatHexColor(%v) = %q, want 7 characters", col, hex)
		}
	}
}

// TestCatColorPrefersCustomColor checks the lookup the canvas and the swatches
// share: a valid custom color wins, an empty or broken one falls back to the
// generated palette (§6).
func TestCatColorPrefersCustomColor(t *testing.T) {
	a := New()
	a.categories = []Category{
		{Name: "x"},
		{Name: "y", Color: "#3b82f6"},
		{Name: "z", Color: "bogus"},
	}
	if got, want := a.catColor(0), paletteColor(0); got != want {
		t.Errorf("catColor(0) = %v, want the palette color %v", got, want)
	}
	if got, want := a.catColor(1), ui.RGB(0x3b, 0x82, 0xf6); got != want {
		t.Errorf("catColor(1) = %v, want its custom color %v", got, want)
	}
	if got, want := a.catColor(2), paletteColor(2); got != want {
		t.Errorf("catColor(2) = %v, want the palette color %v for a broken custom one", got, want)
	}
	if got, want := a.catColor(99), paletteColor(0); got != want {
		t.Errorf("catColor(99) = %v, want the fallback %v", got, want)
	}

	cols := a.catColors()
	if len(cols) != len(a.categories) {
		t.Fatalf("catColors() has %d colors for %d categories", len(cols), len(a.categories))
	}
	for i, col := range cols {
		if col != a.catColor(i) {
			t.Errorf("catColors()[%d] = %v, want %v", i, col, a.catColor(i))
		}
	}
}

// TestCategoryPanelAddsCategory clicks the panel's 添加 button: a new category
// with the next free catN name appears and becomes current (§6).
func TestCategoryPanelAddsCategory(t *testing.T) {
	a := newTestApp()
	a.categories = []Category{{Name: "cat1"}, {Name: "cat2"}}
	before := len(a.categories)
	tt := panelFor(t, a)

	if err := tt.Click("添加"); err != nil {
		t.Fatalf("点击添加: %v", err)
	}
	tt.Frame()
	if len(a.categories) != before+1 {
		t.Fatalf("类别数 = %d，期望 %d", len(a.categories), before+1)
	}
	if got, want := a.categories[before].Name, "cat3"; got != want {
		t.Errorf("新类别名 = %q，期望 %q", got, want)
	}
	if a.categories[before].Color != "" {
		t.Errorf("新类别颜色 = %q，期望留空取调色板色", a.categories[before].Color)
	}
	if a.curCat != before {
		t.Errorf("curCat = %d，期望 %d（新类别成为当前）", a.curCat, before)
	}
	if !a.dirty {
		t.Error("添加类别后 dirty 应为 true")
	}
	if !tt.HasText("cat3") {
		t.Errorf("面板没有显示新类别；texts = %v", tt.Texts())
	}
}

// TestAddCategoryNameSkipsTaken checks that the generated name is the first
// free catN from the list's length plus one.
func TestAddCategoryNameSkipsTaken(t *testing.T) {
	a := New()
	a.categories = []Category{{Name: "cat1"}, {Name: "cat2"}, {Name: "cat4"}}
	a.addCategory()
	if got, want := a.categories[3].Name, "cat5"; got != want {
		t.Errorf("新类别名 = %q，期望 %q（cat4 已占用）", got, want)
	}
}

// TestClampCurCat keeps the current category inside a shrinking list, and off
// the list when it is empty: no current category exists then.
func TestClampCurCat(t *testing.T) {
	a := New()
	a.categories = defaultCategoryList()
	a.curCat = 99
	a.clampCurCat()
	if want := len(a.categories) - 1; a.curCat != want {
		t.Errorf("clampCurCat 后 curCat = %d，期望 %d", a.curCat, want)
	}
	a.curCat = -4
	a.clampCurCat()
	if a.curCat != 0 {
		t.Errorf("负的 curCat 钳制后 = %d，期望 0", a.curCat)
	}
	a.categories = nil
	a.curCat = 5
	a.clampCurCat()
	if a.curCat != -1 {
		t.Errorf("空类别表的 curCat = %d，期望 -1", a.curCat)
	}
}

// TestCategoryPanelSelectsRow clicks a category's name: it becomes the current
// category, which is what digit keys and new annotations use.
func TestCategoryPanelSelectsRow(t *testing.T) {
	a := newTestApp()
	tt := panelFor(t, a)
	if a.curCat != 0 {
		t.Fatalf("初始 curCat = %d，期望 0", a.curCat)
	}
	if err := tt.Click("cat3"); err != nil {
		t.Fatalf("点击类别名: %v", err)
	}
	tt.Frame()
	if a.curCat != 2 {
		t.Errorf("curCat = %d，期望 2", a.curCat)
	}
}

// TestCategoryPanelRenameInline renames a category through the row's inline
// editor, as the panel offers it: double click, type, Enter (§6).
func TestCategoryPanelRenameInline(t *testing.T) {
	a := newTestApp()
	tt := panelFor(t, a)

	r, ok := tt.Find("cat1")
	if !ok {
		t.Fatalf("面板没有类别名；texts = %v", tt.Texts())
	}
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	tt.ClickAt(cx, cy)
	tt.ClickAt(cx, cy)
	tt.Frame()

	tt.Type("车辆")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	if got, want := a.categories[0].Name, "车辆"; got != want {
		t.Fatalf("改名后名字 = %q，期望 %q（texts = %v）", got, want, tt.Texts())
	}
	if !a.dirty {
		t.Error("改名后 dirty 应为 true")
	}
	if !tt.HasText("车辆") {
		t.Errorf("面板没有显示新名字；texts = %v", tt.Texts())
	}
}

// TestCategoryPanelRenameEscapeAbandons checks that Escape leaves the old name:
// the inline editor's documented way out.
func TestCategoryPanelRenameEscapeAbandons(t *testing.T) {
	a := newTestApp()
	tt := panelFor(t, a)

	r, ok := tt.Find("cat1")
	if !ok {
		t.Fatalf("面板没有类别名；texts = %v", tt.Texts())
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Type("废弃")
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	if got := a.categories[0].Name; got != "cat1" {
		t.Errorf("按 Esc 后名字 = %q，期望 cat1", got)
	}
	if a.dirty {
		t.Error("按 Esc 放弃改名后 dirty 应为 false")
	}
}

// TestCategoryPanelRenameBlankRestoresDefault checks the one rename the panel
// refuses to keep: a blank name would leave a row that cannot be told from its
// neighbours, so the slot's default name comes back.
func TestCategoryPanelRenameBlankRestoresDefault(t *testing.T) {
	a := newTestApp()
	tt := panelFor(t, a)

	r, ok := tt.Find("cat2")
	if !ok {
		t.Fatalf("面板没有类别名；texts = %v", tt.Texts())
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Type("   ")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	if got, want := a.categories[1].Name, "cat2"; got != want {
		t.Errorf("清空名字后 = %q，期望回到默认 %q", got, want)
	}
}

// TestPaletteModalAppliesPreset clicks a swatch and then a preset color: the
// category keeps the new color and the dialog closes (§6).
func TestPaletteModalAppliesPreset(t *testing.T) {
	a := newTestApp()
	tt := panelFor(t, a)

	if err := tt.Click("swatch1"); err != nil {
		t.Fatalf("点击颜色块: %v", err)
	}
	tt.Frame()
	if !a.paletteOpen {
		t.Fatalf("点击颜色块后色板未打开；texts = %v", tt.Texts())
	}
	if a.paletteCat != 1 {
		t.Errorf("paletteCat = %d，期望 1", a.paletteCat)
	}

	if err := tt.Click("#3b82f6"); err != nil {
		t.Fatalf("点击预设色: %v", err)
	}
	tt.Frame()
	if got := a.categories[1].Color; got != "#3b82f6" {
		t.Fatalf("类别颜色 = %q，期望 #3b82f6", got)
	}
	if a.paletteOpen {
		t.Error("选色后色板应关闭")
	}
	if !a.dirty {
		t.Error("选色后 dirty 应为 true")
	}
	if got, want := a.catColor(1), ui.RGB(0x3b, 0x82, 0xf6); got != want {
		t.Errorf("catColor(1) = %v，期望 %v", got, want)
	}
}

// TestDeleteCategoryRefusedWhileReferenced is the delete protection of §6: an
// annotation still using the category keeps it alive.
func TestDeleteCategoryRefusedWhileReferenced(t *testing.T) {
	a := newTestApp()
	a.categories = []Category{{Name: "x"}, {Name: "y"}}
	a.images[0].Anns = []Ann{{ID: "a", Cat: 1, CX: 10, CY: 10, W: 4, H: 4}}

	a.deleteCategory(1)

	if len(a.categories) != 2 {
		t.Fatalf("类别数 = %d，期望 2（删除应被拒绝）", len(a.categories))
	}
	if got := a.images[0].Anns[0].Cat; got != 1 {
		t.Errorf("标注类别 = %d，期望 1（不该重映射）", got)
	}
	if len(a.undoStack) != 0 {
		t.Errorf("被拒绝的删除压入了 undo 栈：%d 条", len(a.undoStack))
	}
	if !strings.Contains(a.lastStatus, "y") || !strings.Contains(a.lastStatus, "1") {
		t.Errorf("状态栏 = %q，期望说明引用数", a.lastStatus)
	}
}

// TestDeleteCategoryRemapsAndUndoes removes an unused category: annotations of
// later categories move down one step, and one undo brings list and
// annotations back together (§6).
func TestDeleteCategoryRemapsAndUndoes(t *testing.T) {
	a := newTestApp()
	a.images = append(a.images, ImageInfo{Path: "t2", Name: "t2.png", Width: 800, Height: 600})
	a.categories = []Category{{Name: "x"}, {Name: "y"}, {Name: "z"}}
	a.images[0].Anns = []Ann{
		{ID: "a0", Cat: 0, CX: 10, CY: 10, W: 4, H: 4},
		{ID: "a1", Cat: 2, CX: 20, CY: 10, W: 4, H: 4},
		{ID: "a2", Cat: 2, CX: 30, CY: 10, W: 4, H: 4},
	}
	a.images[1].Anns = []Ann{{ID: "b0", Cat: 2, CX: 10, CY: 10, W: 4, H: 4}}
	a.curCat = 2

	a.deleteCategory(1)

	if want := []Category{{Name: "x"}, {Name: "z"}}; !reflect.DeepEqual(a.categories, want) {
		t.Fatalf("删除后类别 = %v，期望 %v", a.categories, want)
	}
	if got, want := catsOf(a.images[0].Anns), []int{0, 1, 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("图片 1 的类别 = %v，期望 %v", got, want)
	}
	if got, want := catsOf(a.images[1].Anns), []int{1}; !reflect.DeepEqual(got, want) {
		t.Errorf("图片 2 的类别 = %v，期望 %v", got, want)
	}
	if a.curCat != 1 {
		t.Errorf("curCat = %d，期望 1（跟着类别一起移动）", a.curCat)
	}

	a.undo()
	if want := []Category{{Name: "x"}, {Name: "y"}, {Name: "z"}}; !reflect.DeepEqual(a.categories, want) {
		t.Errorf("撤销后类别 = %v，期望 %v", a.categories, want)
	}
	if got, want := catsOf(a.images[0].Anns), []int{0, 2, 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("撤销后图片 1 的类别 = %v，期望 %v", got, want)
	}
	if got, want := catsOf(a.images[1].Anns), []int{2}; !reflect.DeepEqual(got, want) {
		t.Errorf("撤销后图片 2 的类别 = %v，期望 %v", got, want)
	}
	if a.curCat != 2 {
		t.Errorf("撤销后 curCat = %d，期望 2", a.curCat)
	}

	a.redo()
	if got, want := catsOf(a.images[0].Anns), []int{0, 1, 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("重做后图片 1 的类别 = %v，期望 %v", got, want)
	}
}

// TestDeleteLastCategoryIsAllowed: an empty category list is a legal state, so
// the last category can go; curCat has nothing to point at (-1) and one undo
// brings the list back.
func TestDeleteLastCategoryIsAllowed(t *testing.T) {
	a := newTestApp()
	a.categories = []Category{{Name: "only"}}
	a.curCat = 0

	a.deleteCategory(0)

	if len(a.categories) != 0 {
		t.Fatalf("类别数 = %d，期望 0（空表合法）", len(a.categories))
	}
	if a.curCat != -1 {
		t.Errorf("curCat = %d，期望 -1（空表没有当前类别）", a.curCat)
	}
	if len(a.undoStack) != 1 {
		t.Fatalf("undo 栈 = %d 条，期望 1", len(a.undoStack))
	}
	a.undo()
	if len(a.categories) != 1 || a.categories[0].Name != "only" {
		t.Errorf("撤销后类别 = %v，期望 [only]", a.categories)
	}
	if a.curCat != 0 {
		t.Errorf("撤销后 curCat = %d，期望 0", a.curCat)
	}
}

// TestDigitKeysPickFirstNine checks the toolbar's replaced buttons: digits 1-9
// still pick the first nine categories, and a shorter list clamps instead of
// pointing past its end.
func TestDigitKeysPickFirstNine(t *testing.T) {
	a, _ := tester(t)
	a.categories = make([]Category, 12)
	for i := range a.categories {
		a.categories[i].Name = fmt.Sprintf("c%d", i)
	}
	a.clearSelection()

	a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.Key9})
	if a.curCat != 8 {
		t.Errorf("按 9 后 curCat = %d，期望 8", a.curCat)
	}

	a.categories = a.categories[:3]
	a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.Key9})
	if a.curCat != 2 {
		t.Errorf("只有 3 个类别时按 9，curCat = %d，期望 2", a.curCat)
	}
}

// TestCategoryColorsPersist saves a colored list and reopens the folder: names
// and colors both come back (§8 item 4).
func TestCategoryColorsPersist(t *testing.T) {
	a := storeApp(t, "a.png")
	a.categories = []Category{
		{Name: "车主", Color: "#3b82f6"},
		{Name: "行人"},
	}
	if err := a.save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	b := reopen(t, a.dir)
	if !reflect.DeepEqual(b.categories, a.categories) {
		t.Fatalf("重开后类别 = %v，期望 %v", b.categories, a.categories)
	}
	if got, want := b.catColor(0), ui.RGB(0x3b, 0x82, 0xf6); got != want {
		t.Errorf("重开后 catColor(0) = %v，期望 %v", got, want)
	}
}

// TestDeleteCategorySavesEveryImage checks the part of §6 that is easy to get
// wrong: a category removal rewrites the rows of every image whose annotations
// moved, not only the loaded one's.
func TestDeleteCategorySavesEveryImage(t *testing.T) {
	a := storeApp(t, "a.png", "b.png")
	a.categories = []Category{{Name: "x"}, {Name: "y"}, {Name: "z"}}
	seedAnns(t, a, 0, []Ann{
		{ID: "ann_1", Cat: 0, Kind: KindRect, CX: 10, CY: 10, W: 4, H: 4},
		{ID: "ann_2", Cat: 2, Kind: KindRect, CX: 20, CY: 10, W: 4, H: 4},
	})
	seedAnns(t, a, 1, []Ann{{ID: "ann_3", Cat: 2, Kind: KindRect, CX: 10, CY: 10, W: 4, H: 4}})

	a.loaded = 1 // the user is looking at b.png when they delete the category
	a.deleteCategory(1)
	if err := a.save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	b := reopen(t, a.dir)
	if want := []Category{{Name: "x"}, {Name: "z"}}; !reflect.DeepEqual(b.categories, want) {
		t.Errorf("重开后类别 = %v，期望 %v", b.categories, want)
	}
	if got, want := catsOf(b.images[0].Anns), []int{0, 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("重开后 a.png 的类别 = %v，期望 %v（那张图没被载入过）", got, want)
	}
	if got, want := catsOf(b.images[1].Anns), []int{1}; !reflect.DeepEqual(got, want) {
		t.Errorf("重开后 b.png 的类别 = %v，期望 %v", got, want)
	}
}

// TestCategoryPanelSitsRightOfCanvas checks the layout of §6: the panel is a
// fixed column on the right, and the canvas keeps the rest of the window.
func TestCategoryPanelSitsRightOfCanvas(t *testing.T) {
	a := newTestApp()
	tt := panelFor(t, a)
	cv := canvasBounds(t, tt)

	add, ok := tt.Find("添加")
	if !ok {
		t.Fatalf("面板没有添加按钮；texts = %v", tt.Texts())
	}
	if add.X < cv.X+cv.W {
		t.Errorf("添加按钮在 x=%v，画布右边界 %v：面板应在画布右侧", add.X, cv.X+cv.W)
	}
	if cv.W > float32(testWinW-categoryPanelWidth)+1 || cv.W < float32(testWinW-categoryPanelWidth)-1 {
		t.Errorf("画布宽 = %v，期望窗口减去面板宽 %d", cv.W, categoryPanelWidth)
	}
	if cv.H <= 0 {
		t.Errorf("画布高 = %v，期望铺满面板旁边的高度", cv.H)
	}
}

// catsOf returns one category index per annotation, for comparisons.
func catsOf(anns []Ann) []int {
	out := make([]int, len(anns))
	for i := range anns {
		out[i] = anns[i].Cat
	}
	return out
}
