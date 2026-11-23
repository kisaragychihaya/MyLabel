package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
)

// The annotation properties dialog (docs/phase3-design.md §5.1): a double-click
// on a selected annotation opens it, and it edits the category and the
// attributes as one undoable step.
//
// Every edit is staged in propsState and committed on 确定, so 取消 (and a click
// outside the dialog, or Escape) drops the whole edit without touching the
// annotation or the undo stack. The category buttons therefore do not write the
// model as they are clicked: the dialog's 取消 must be able to take a new
// category back too. The commit is one cmdModify — the same command the digit
// shortcuts' single-annotation path uses — so the undo granularity matches
// setSelectionCategory.

// propRow is one attribute row of the dialog: a key value pair being edited.
// ID makes the row's widget keys stable while rows are added and removed
// (design rule 6: keys in one parent must stay unique, and a row's text field
// must keep its identity when its neighbour goes away).
type propRow struct {
	ID    int
	Key   string
	Value string
}

// propsState is the properties dialog's state, owned by App because it outlives
// the frame that builds the dialog (design rule 1).
type propsState struct {
	Open bool
	// Image is a.loaded when the dialog opened and Index names the annotation in
	// that image; ID guards against the list having changed behind the dialog.
	Image int
	Index int
	ID    string

	Cat  int       // pending category
	Rows []propRow // pending attributes, in display order
	Seq  int       // row id counter
}

// propsWidth is the dialog's content width in DIP.
const propsWidth = 460

// openProps opens the properties dialog on annotation idx of the loaded image,
// staging its current category and attributes.
func (a *App) openProps(idx int) {
	img := a.curImage()
	if img == nil || idx < 0 || idx >= len(img.Anns) {
		return
	}
	an := &img.Anns[idx]
	// The dialog edits one annotation: a multi-selection collapses onto the
	// double-clicked one. A drag in progress holds index-based snapshots that
	// this edit would invalidate, so it is dropped (nothing moved yet).
	a.selectOnly(idx)
	a.mode = ModeIdle
	a.drag = dragState{}

	a.props = propsState{Open: true, Image: a.loaded, Index: idx, ID: an.ID, Cat: an.Cat}
	a.props.Rows = a.rowsFromAttrs(an.Attributes)
}

// closeProps drops the dialog and everything staged in it.
func (a *App) closeProps() { a.props = propsState{} }

// propsTarget returns the annotation the open dialog edits, or nil when it no
// longer exists (another image loaded, the annotation deleted or undone away).
func (a *App) propsTarget() *Ann {
	m := &a.props
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

// rowsFromAttrs stages an attribute map as dialog rows in a stable order:
// alphabetical by key, so reopening the dialog does not reshuffle them.
func (a *App) rowsFromAttrs(attrs map[string]string) []propRow {
	if len(attrs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([]propRow, len(keys))
	for i, k := range keys {
		a.props.Seq++
		rows[i] = propRow{ID: a.props.Seq, Key: k, Value: attrs[k]}
	}
	return rows
}

// addPropRow appends an empty row for the user to fill in.
func (a *App) addPropRow() {
	if !a.props.Open {
		return
	}
	a.props.Seq++
	a.props.Rows = append(a.props.Rows, propRow{ID: a.props.Seq})
}

// attrsFromRows turns the dialog's rows into the attribute map to store: keys
// are trimmed, rows with an empty key are dropped (they are how a user abandons
// a row he started), and a repeated key keeps the last value typed. No rows
// left means nil, i.e. "the annotation has no attributes".
func attrsFromRows(rows []propRow) map[string]string {
	var out map[string]string
	for _, r := range rows {
		key := strings.TrimSpace(r.Key)
		if key == "" {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(rows))
		}
		out[key] = r.Value
	}
	return out
}

// commitProps writes the staged category and attributes as one undo step.
func (a *App) commitProps() {
	an := a.propsTarget()
	if an == nil {
		a.closeProps()
		return
	}
	before := cloneAnn(*an)
	after := before
	after.Cat = a.props.Cat
	after.Attributes = attrsFromRows(a.props.Rows)
	idx := a.props.Index
	a.closeProps()

	if sameAnn(before, after) {
		a.lastStatus = "属性未改动"
		return
	}
	a.push(cmdModify(a, a.loaded, idx, before, after))
	a.lastStatus = fmt.Sprintf("已更新标注属性（%d 条属性）", len(after.Attributes))
}

// propsKindLabel names the annotation's shape for the dialog's subtitle.
func propsKindLabel(an *Ann) string {
	if isPoly(an) {
		return "多边形"
	}
	if an.Angle != 0 {
		return "旋转框"
	}
	return "矩形"
}

// propsModal draws the properties dialog. It is a no-op while the dialog is
// closed, so the frame costs nothing when it is not in use.
func (a *App) propsModal(c *ui.Context) {
	if !a.props.Open {
		return
	}
	an := a.propsTarget()
	if an == nil {
		// The annotation went away under the dialog: drop the staged edit
		// rather than write it to whatever now sits at that index.
		a.closeProps()
		return
	}
	m := &a.props
	t := c.Theme()
	title := propsKindLabel(an)

	ui.Modal(c, &m.Open, func() {
		ui.Column(c).Width(propsWidth).Gap(8).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "标注属性").FontSize(14).Bold()
				ui.Textf(c, "%s · 第 %d 条", title, m.Index+1).FontSize(11).TextColor(t.TextMuted)
			})

			ui.Text(c, "类别").FontSize(11).TextColor(t.TextMuted)
			ui.Scroll(c).Height(110).Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					for i := range a.categories {
						ui.Radio(c.Key(fmt.Sprintf("propsCat%d", i)), &m.Cat, i, a.categories[i].Name)
					}
				})
			})

			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Textf(c, "属性（%d）", len(m.Rows)).FontSize(11).TextColor(t.TextMuted)
				ui.Text(c, "键为空的属性在确定时丢弃").FontSize(10).TextColor(t.TextMuted)
			})
			ui.Scroll(c).Height(160).Children(func() {
				ui.Column(c).Gap(4).Children(func() {
					del := -1
					for i := range m.Rows {
						row := &m.Rows[i]
						n := i + 1
						ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
							ui.TextInput(c.Key(fmt.Sprintf("propKey%d", row.ID)), &row.Key).
								Width(130).Label(fmt.Sprintf("属性键%d", n))
							ui.TextInput(c.Key(fmt.Sprintf("propVal%d", row.ID)), &row.Value).
								Grow(1).Label(fmt.Sprintf("属性值%d", n))
							if ui.Button(c.Key(fmt.Sprintf("propDel%d", row.ID)), "×").
								Label(fmt.Sprintf("删除属性%d", n)).Clicked() {
								del = i
							}
						})
					}
					// The removal waits for the loop: deleting while walking the
					// slice would shift the rows the pointers in it name.
					if del >= 0 {
						m.Rows = append(m.Rows[:del:del], m.Rows[del+1:]...)
					}
					if len(m.Rows) == 0 {
						ui.Text(c, "还没有属性").FontSize(11).TextColor(t.TextMuted)
					}
				})
			})
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Button(c.Key("propAdd"), "添加属性").OnClick(a.addPropRow)
			})
			ui.Row(c).Gap(8).Children(func() {
				if ui.PrimaryButton(c.Key("propOk"), "确定").Clicked() {
					a.commitProps()
				}
				if ui.Button(c.Key("propCancel"), "取消").Clicked() {
					a.closeProps()
				}
			})
		})
	})
}
