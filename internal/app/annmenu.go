package app

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"mylabel/locales"
)

// Two menus that act on one annotation, both named by image and index and
// guarded by the annotation's ID (as propsState is): the category picker a
// double-click on a category chip opens, and the z-order menu a right-click on
// an annotation opens. Both are App state because they outlive the frame that
// builds them (design rule 1).

// --- the category picker ----------------------------------------------------

// catPickState is the category picker's state.
type catPickState struct {
	Open bool
	// Image is a.loaded when the picker opened and Index names the annotation in
	// that image; ID guards against the list having changed behind it.
	Image int
	Index int
	ID    string
	// Cat is the annotation's category, written by the picker's radio group: a
	// click on another one applies it at once.
	Cat int
}

// catPickWidth is the picker's content width in DIP.
const catPickWidth = 320

// openCatPick opens the category picker on annotation idx of the loaded image,
// preselecting its current category. Without a category there is nothing to
// pick, so the dialog stays closed.
func (a *App) openCatPick(idx int) {
	img := a.curImage()
	if img == nil || idx < 0 || idx >= len(img.Anns) || len(a.categories) == 0 {
		return
	}
	an := &img.Anns[idx]
	a.catPick = catPickState{Open: true, Image: a.loaded, Index: idx, ID: an.ID, Cat: an.Cat}
}

// catPickTarget returns the annotation the open picker edits, or nil when it no
// longer exists (another image loaded, the annotation deleted or undone away).
func (a *App) catPickTarget() *Ann {
	m := &a.catPick
	if !m.Open || m.Image != a.loaded {
		return nil
	}
	img := a.curImage()
	if img == nil || m.Index < 0 || m.Index >= len(img.Anns) {
		return nil
	}
	if img.Anns[m.Index].ID != m.ID {
		return nil
	}
	return &img.Anns[m.Index]
}

// pickCategory gives the picker's annotation category cat as one undo step — the
// same cmdModify the digit keys push for a single annotation, so a fresh image is
// upgraded to labeling by the same rule — and closes the picker. Picking the
// current category changes nothing and pushes nothing.
func (a *App) pickCategory(cat int) {
	an := a.catPickTarget()
	if an == nil {
		a.catPick = catPickState{}
		return
	}
	idx := a.catPick.Index
	a.catPick = catPickState{}
	if cat < 0 || cat >= len(a.categories) || an.Cat == cat {
		return
	}
	before := cloneAnn(*an)
	after := before
	after.Cat = cat
	a.push(cmdModify(a, a.loaded, idx, before, after))
	a.lastStatus = locales.CatPickChanged(a.categories[cat].Name)
}

// catPickModal draws the category picker: one radio row per category, the
// current one preselected, and a click on any other applies it and closes. It is
// a no-op while the picker is closed, so the frame costs nothing when it is not
// in use.
func (a *App) catPickModal(c *ui.Context) {
	if !a.catPick.Open {
		return
	}
	an := a.catPickTarget()
	if an == nil {
		a.catPick = catPickState{}
		return
	}
	m := &a.catPick
	t := c.Theme()
	// The categories may have shrunk under the dialog (an undo of 添加): the
	// current category then names nothing, and clamping keeps a pick in range.
	m.Cat = clampCat(m.Cat, len(a.categories))
	current := locales.CatPickUncategorized()
	if m.Cat >= 0 && m.Cat < len(a.categories) {
		current = a.categories[m.Cat].Name
	}
	before := m.Cat

	ui.Modal(c, &m.Open, func() {
		ui.Column(c).Width(catPickWidth).Gap(8).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.CatPickTitle()).FontSize(14).Bold()
				ui.Text(c, locales.CatPickSubtitle(m.Index+1, current)).FontSize(11).TextColor(t.TextMuted)
			})
			ui.Scroll(c).Height(280).Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					for i := range a.categories {
						ui.Row(c.Key(fmt.Sprintf("catPick%d", i))).AlignItems(ui.Center).Gap(6).Padding(2).
							Children(func() {
								ui.Box(c).Width(12).Height(12).Radius(3).Background(a.catColor(i))
								ui.Radio(c.Key(fmt.Sprintf("catPickRadio%d", i)), &m.Cat, i, a.categories[i].Name)
							})
					}
				})
			})
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c.Key("catPickCancel"), locales.CommonCancel()).Clicked() {
					a.catPick = catPickState{}
				}
			})
		})
	})

	// A click on another category wrote it into m.Cat: apply it once, after the
	// list is built, so the dialog is not rebuilt while it is being walked.
	if m.Cat != before && a.catPick.Open {
		a.pickCategory(m.Cat)
	}
}

// clampCat keeps a category index inside a list of n categories, -1 when there
// are none.
func clampCat(cat, n int) int {
	if n <= 0 {
		return -1
	}
	if cat < 0 {
		return 0
	}
	if cat >= n {
		return n - 1
	}
	return cat
}

// --- the z-order menu -------------------------------------------------------

// reorderKind is one z-order move: the annotation goes one step up or down the
// drawing order, or to its top or bottom.
type reorderKind int

const (
	zUp   reorderKind = iota + 1 // one layer up: drawn later
	zDown                        // one layer down: drawn earlier
	zTop
	zBottom
)

// zMoves lists each move in menu order: its kind and the key of its button.
var zMoves = []struct {
	kind reorderKind
	key  string
}{
	{zUp, "zUp"},
	{zDown, "zDown"},
	{zTop, "zTop"},
	{zBottom, "zBottom"},
}

// zMenuLabel names one move in the interface's language, read per frame: a
// package-level label would freeze the language the app started in.
func zMenuLabel(kind reorderKind) string {
	switch kind {
	case zUp:
		return locales.MenuZUp()
	case zDown:
		return locales.MenuZDown()
	case zTop:
		return locales.MenuZTop()
	case zBottom:
		return locales.MenuZBottom()
	}
	return ""
}

// zMenuState is the z-order menu's state: the annotation it acts on and where it
// opened, in canvas-local DIP (the popover's anchor).
type zMenuState struct {
	Open bool
	// Image is a.loaded when the menu opened and Index names the annotation in
	// that image; ID guards against the list having changed behind it.
	Image int
	Index int
	ID    string
	X, Y  float32
}

// reorderTarget returns the index an annotation at index moves to among n
// annotations, and whether it moves at all: one already at the top cannot move
// up, one at the bottom cannot move down. The slice order is the drawing order,
// so the end of the slice is the top.
func reorderTarget(kind reorderKind, index, n int) (int, bool) {
	if index < 0 || index >= n || n < 2 {
		return 0, false
	}
	switch kind {
	case zUp:
		if index >= n-1 {
			return 0, false
		}
		return index + 1, true
	case zDown:
		if index <= 0 {
			return 0, false
		}
		return index - 1, true
	case zTop:
		if index >= n-1 {
			return 0, false
		}
		return n - 1, true
	case zBottom:
		if index <= 0 {
			return 0, false
		}
		return 0, true
	}
	return 0, false
}

// openZMenu opens the z-order menu on the annotation under the pointer (image
// pixel p), selecting it first when it is not selected: a context menu acts on
// one annotation. It reports whether the menu opened — a right-click on empty
// canvas opens nothing.
func (a *App) openZMenu(ev ui.InputEvent, p Point) bool {
	img := a.curImage()
	if img == nil {
		return false
	}
	hit := hitTestAnns(img.Anns, a.soleSelection(), p, a.zoom)
	if hit.Kind == HitNone || hit.Ann < 0 || hit.Ann >= len(img.Anns) {
		return false
	}
	if !a.isSelected(hit.Ann) {
		a.selectOnly(hit.Ann)
	}
	a.zMenu = zMenuState{
		Open:  true,
		Image: a.loaded,
		Index: hit.Ann,
		ID:    img.Anns[hit.Ann].ID,
		X:     ev.X,
		Y:     ev.Y,
	}
	return true
}

// zMenuTarget returns the annotation the open menu reorders, and how many
// annotations its image holds, or nil when it no longer exists.
func (a *App) zMenuTarget() (*Ann, int) {
	m := &a.zMenu
	if !m.Open || m.Image != a.loaded {
		return nil, 0
	}
	img := a.curImage()
	if img == nil || m.Index < 0 || m.Index >= len(img.Anns) {
		return nil, 0
	}
	if img.Anns[m.Index].ID != m.ID {
		return nil, 0
	}
	return &img.Anns[m.Index], len(img.Anns)
}

// applyReorder moves the menu's annotation as kind says, as one undo step, and
// keeps it selected: it is the annotation the user is working on.
func (a *App) applyReorder(kind reorderKind) {
	an, n := a.zMenuTarget()
	if an == nil {
		a.zMenu = zMenuState{}
		return
	}
	idx := a.zMenu.Index
	a.zMenu = zMenuState{}
	to, ok := reorderTarget(kind, idx, n)
	if !ok {
		return
	}
	a.push(cmdReorder(a, a.loaded, idx, to, an.ID))
	a.selectOnly(to)
}

// zMenuView anchors the z-order menu at the point it opened on. It is a no-op
// while the menu is closed, so the frame costs nothing when it is not in use.
// The anchor is inert: the press that dismisses the menu must reach the canvas,
// which drops the menu and does nothing else (handleCanvasInput).
func (a *App) zMenuView(c *ui.Context, cv ui.Element) {
	if !a.zMenu.Open {
		return
	}
	m := a.zMenu
	cv.Children(func() {
		anchor := ui.Box(c).Absolute().Left(m.X).Top(m.Y).Width(1).Height(1).IgnoreInput(true)
		ui.Popover(c, anchor, &a.zMenu.Open, func() { a.zMenuPanel(c) })
	})
}

// zMenuPanel builds the z-order menu: one button per move, disabled where the
// annotation cannot move that way.
func (a *App) zMenuPanel(c *ui.Context) {
	an, n := a.zMenuTarget()
	if an == nil {
		a.zMenu = zMenuState{}
		return
	}
	idx := a.zMenu.Index
	ui.Column(c).Gap(2).Children(func() {
		for _, item := range zMoves {
			_, enabled := reorderTarget(item.kind, idx, n)
			btn := ui.Button(c.Key(item.key), zMenuLabel(item.kind))
			if !enabled {
				btn = btn.Disabled(true)
			}
			if btn.Clicked() {
				a.applyReorder(item.kind)
			}
		}
	})
}
