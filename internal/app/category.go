package app

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// categoryPanelWidth is the fixed width of the category panel, in DIP
// (docs/phase2-design.md §6).
const categoryPanelWidth = 190

// palettePresets are the twelve colors the panel's swatch picker offers:
// presets, not a free color picker (§6).
var palettePresets = [...]string{
	"#ef4444", "#f97316", "#f59e0b", "#eab308",
	"#84cc16", "#22c55e", "#10b981", "#06b6d4",
	"#3b82f6", "#6366f1", "#8b5cf6", "#ec4899",
}

// paletteColumns is how many presets the picker lays out per row.
const paletteColumns = 4

// paletteHueStep is the golden angle in degrees: consecutive category indices
// land far apart on the hue circle, so any number of categories stays
// distinguishable and no hand-written table caps the list (§6).
const paletteHueStep = 137.508

// paletteColor returns the display color of the category at index i when it
// carries no custom color: a hue rotated by the golden angle, at a saturation
// and value that keep every swatch readable on the theme's surfaces.
func paletteColor(i int) ui.Color {
	if i < 0 {
		i = 0
	}
	h := math.Mod(float64(i)*paletteHueStep, 360)
	return hsvColor(h, 0.68, 0.94-0.12*float64(i%3))
}

// hsvColor converts h (degrees), s and v (0..1) to an 8-bit RGB color.
func hsvColor(h, s, v float64) ui.Color {
	h = math.Mod(math.Mod(h, 360)+360, 360)
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	to8 := func(f float64) uint8 { return uint8(math.Round(clampFloat(f+m, 0, 1) * 255)) }
	return ui.RGB(to8(r), to8(g), to8(b))
}

// parseHexColor reads a "#RRGGBB" color. Anything else — a color name, a
// shorthand, a missing '#' — is rejected instead of guessed at, so a stored
// color can never paint something the user did not pick.
func parseHexColor(s string) (ui.Color, bool) {
	if len(s) != 7 || s[0] != '#' {
		return ui.Color{}, false
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return ui.Color{}, false
	}
	return ui.RGB(uint8(v>>16), uint8(v>>8), uint8(v)), true
}

// formatHexColor renders c in the store's "#rrggbb" form.
func formatHexColor(c ui.Color) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// catColor returns the color category i is drawn in: its custom color when it
// has a valid one, the generated palette color otherwise (§6).
func (a *App) catColor(i int) ui.Color {
	if i < 0 || i >= len(a.categories) {
		return paletteColor(0)
	}
	if c, ok := parseHexColor(a.categories[i].Color); ok {
		return c
	}
	return paletteColor(i)
}

// catColors refills the canvas's reusable color buffer with every category's
// color, so the annotation drawing loop looks a color up by index instead of
// parsing a hex string per annotation.
func (a *App) catColors() []ui.Color {
	cols := a.scratchColors[:0]
	for i := range a.categories {
		cols = append(cols, a.catColor(i))
	}
	a.scratchColors = cols
	return cols
}

// clampCurCat keeps the current category index inside the list after the list
// changed or a folder was opened. An empty list has no current category:
// curCat is -1, and nothing may be created until the panel adds one.
func (a *App) clampCurCat() {
	if len(a.categories) == 0 {
		a.curCat = -1
		return
	}
	if a.curCat < 0 {
		a.curCat = 0
	}
	if a.curCat >= len(a.categories) {
		a.curCat = len(a.categories) - 1
	}
}

// setCategoriesDirty records that the category list changed: the document is
// dirty and the debounced save will write the whole list. No image is named,
// because a category change is not an annotation change (§6).
func (a *App) setCategoriesDirty() {
	a.dirty = true
	a.scheduleSave()
}

// categoryRefs counts the annotations of every image that use category idx.
func (a *App) categoryRefs(idx int) int {
	if idx < 0 {
		return 0
	}
	n := 0
	for i := range a.images {
		for k := range a.images[i].Anns {
			if a.images[i].Anns[k].Cat == idx {
				n++
			}
		}
	}
	return n
}

// categoryNameTaken reports whether a category already carries name.
func (a *App) categoryNameTaken(name string) bool {
	for i := range a.categories {
		if a.categories[i].Name == name {
			return true
		}
	}
	return false
}

// addCategory appends a category and makes it current (§6). Its name is the
// first free "catN" from the list's length plus one, and its color stays
// empty, which means "the generated palette color of the new index".
func (a *App) addCategory() {
	name := ""
	for n := len(a.categories) + 1; ; n++ {
		candidate := fmt.Sprintf("cat%d", n)
		if !a.categoryNameTaken(candidate) {
			name = candidate
			break
		}
	}
	a.categories = append(a.categories, Category{Name: name})
	a.curCat = len(a.categories) - 1
	a.setCategoriesDirty()
}

// noteCategoryRenamed records an inline rename: EditableText has already
// written the new name into the list, so the work here is to keep a blank name
// from sticking (a nameless row could not be told from its neighbours, so the
// slot's default name comes back) and to save the list.
func (a *App) noteCategoryRenamed(i int) {
	if i < 0 || i >= len(a.categories) {
		return
	}
	name := strings.TrimSpace(a.categories[i].Name)
	if name == "" {
		name = fmt.Sprintf("cat%d", i+1)
	}
	a.categories[i].Name = name
	a.setCategoriesDirty()
}

// setCategoryColor gives category i a custom color; an invalid color is
// refused rather than stored, and a valid one is saved in the canonical
// "#rrggbb" form.
func (a *App) setCategoryColor(i int, hex string) {
	if i < 0 || i >= len(a.categories) {
		return
	}
	col, ok := parseHexColor(hex)
	if !ok {
		return
	}
	hex = formatHexColor(col)
	if a.categories[i].Color == hex {
		return
	}
	a.categories[i].Color = hex
	a.setCategoriesDirty()
}

// deleteCategory removes category idx, refusing while annotations still
// reference it: their category index would point at another category
// afterwards. Annotations of later categories move down one step, as one undo
// step (§6). Deleting the last category is allowed: an empty list is a legal
// state, it just cannot create annotations until one is added again.
func (a *App) deleteCategory(idx int) {
	if idx < 0 || idx >= len(a.categories) {
		return
	}
	if n := a.categoryRefs(idx); n > 0 {
		msg := fmt.Sprintf("“%s”还有 %d 个标注在用，不能删除", a.categories[idx].Name, n)
		a.lastStatus = msg
		if a.win != nil {
			_, _ = mygo.Dialog.Message(mygo.MessageOptions{
				Parent:  a.win,
				Type:    mygo.MessageWarning,
				Title:   "无法删除类别",
				Message: msg,
				Buttons: []string{"知道了"},
			})
		}
		return
	}
	a.push(cmdDeleteCategory(a, idx))
	a.lastStatus = "已删除类别"
}

// categoryPanel builds the right-hand category list (§6): a swatch and a name
// per category, the current one highlighted, [添加][删除] at the bottom.
func (a *App) categoryPanel(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(categoryPanelWidth).FillHeight().Padding(6).Gap(4).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			ui.Text(c, "类别").FontSize(11).TextColor(t.TextMuted)
			ui.Textf(c, "%d", len(a.categories)).FontSize(11).TextColor(t.TextMuted)
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				if len(a.categories) == 0 {
					ui.Text(c, "暂无类别，点“添加”新建").FontSize(11).TextColor(t.TextMuted)
				}
				for i := range a.categories {
					a.categoryRow(c, i)
				}
			})
		})
		ui.Row(c).Gap(4).Children(func() {
			ui.Button(c.Key("catAdd"), "添加").OnClick(a.addCategory)
			ui.Button(c.Key("catDel"), "删除").OnClick(func() { a.deleteCategory(a.curCat) })
		})
	})
}

// categoryRow builds one row of the panel: the color swatch, which opens the
// palette, and the name, editable in place. A click on either makes the
// category current.
func (a *App) categoryRow(c *ui.Context, i int) {
	t := c.Theme()
	selectIt := func() { a.curCat = i }

	row := ui.Row(c.Key(fmt.Sprintf("catRow%d", i))).AlignItems(ui.Center).Gap(6).Padding(3).Radius(4)
	if i == a.curCat {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		ui.Box(c.Key(fmt.Sprintf("catSwatch%d", i))).Width(14).Height(14).Radius(4).
			Background(a.catColor(i)).
			Label(fmt.Sprintf("swatch%d", i)).
			OnClick(func() {
				selectIt()
				a.paletteCat = i
				a.paletteOpen = true
			})
		name := ui.EditableText(c.Key(fmt.Sprintf("catName%d", i)), &a.categories[i].Name).FontSize(12)
		name.OnClick(selectIt)
		if name.Changed() {
			a.noteCategoryRenamed(i)
		}
	})
}

// paletteModal shows the twelve preset colors for the category the panel's
// swatch was clicked on (§6). Clicking a preset applies it and closes the
// dialog; so does clicking outside it or pressing Escape.
func (a *App) paletteModal(c *ui.Context) {
	idx := a.paletteCat
	ui.Modal(c, &a.paletteOpen, func() {
		ui.Column(c).Gap(8).Children(func() {
			name := ""
			if idx >= 0 && idx < len(a.categories) {
				name = a.categories[idx].Name
			}
			ui.Textf(c, "“%s”的颜色", name).FontSize(12)
			for start := 0; start < len(palettePresets); start += paletteColumns {
				start := start
				ui.Row(c).Gap(6).Children(func() {
					for k := start; k < start+paletteColumns && k < len(palettePresets); k++ {
						preset := palettePresets[k]
						col, ok := parseHexColor(preset)
						if !ok {
							continue
						}
						ui.Box(c.Key(fmt.Sprintf("preset%d", k))).Width(24).Height(24).Radius(4).
							Background(col).
							Label(preset).
							OnClick(func() {
								a.setCategoryColor(idx, preset)
								a.paletteOpen = false
							})
					}
				})
			}
			ui.Row(c).Gap(6).Children(func() {
				ui.Button(c.Key("paletteClose"), "关闭").OnClick(func() { a.paletteOpen = false })
			})
		})
	})
}
