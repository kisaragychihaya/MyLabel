package app

import (
	"fmt"
	"strconv"
	"strings"

	"mylabel/internal/importer"
	"mylabel/internal/ir"
)

// The import pipeline (docs/phase3-design.md §4.2 step 5): a parsed Dataset
// becomes annotations of the open folder's images, as one undoable step. It
// depends on no dialog and no window, so tests drive it directly.

// importAdd is one image's share of a batch import.
type importAdd struct {
	img  int
	anns []Ann
}

// maxImportWarnings caps the warning list a summary keeps, so a dataset that
// matches nothing cannot pile up thousands of lines in the status bar.
const maxImportWarnings = 20

// importResult summarizes one applied import.
type importResult struct {
	Images        int      // images that received annotations
	Annots        int      // annotations written
	SkippedImages int      // dataset images with no matching image in the folder
	SkippedAnns   int      // annotations that could not be converted
	Attrs         int      // annotations that carry attributes (stored on Ann)
	DroppedRings  int      // polygon rings the single-ring app model cannot hold
	NewCategories int      // categories the import created
	Warnings      []string // human-readable notes, capped at maxImportWarnings
}

// String renders the summary for the status bar.
func (r importResult) String() string {
	parts := []string{fmt.Sprintf("已导入 %d 张图片 %d 条标注", r.Images, r.Annots)}
	if r.NewCategories > 0 {
		parts = append(parts, fmt.Sprintf("新建类别 %d 个", r.NewCategories))
	}
	if r.SkippedImages > 0 {
		parts = append(parts, fmt.Sprintf("跳过 %d 张不匹配图片", r.SkippedImages))
	}
	if r.SkippedAnns > 0 {
		parts = append(parts, fmt.Sprintf("跳过 %d 条无法转换的标注", r.SkippedAnns))
	}
	if r.DroppedRings > 0 {
		parts = append(parts, fmt.Sprintf("丢弃内部环 %d 个", r.DroppedRings))
	}
	if r.Attrs > 0 {
		parts = append(parts, fmt.Sprintf("%d 条带属性", r.Attrs))
	}
	return strings.Join(parts, "，")
}

// addWarning appends one warning, keeping the list bounded.
func (r *importResult) addWarning(format string, args ...any) {
	if len(r.Warnings) >= maxImportWarnings {
		return
	}
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

// datasetCategories returns the dataset's category table without duplicates, in
// order. Annotations whose category key is not listed are not converted (they
// count as skipped): a category the source never declared is a data error the
// importer already reports as an Issue, not something to invent a category for.
func datasetCategories(ds *importer.Dataset) []ir.Category {
	if ds == nil {
		return nil
	}
	out := make([]ir.Category, 0, len(ds.Categories))
	seen := make(map[string]bool, len(ds.Categories))
	for _, c := range ds.Categories {
		if seen[c.Key] {
			continue
		}
		seen[c.Key] = true
		if c.Name == "" {
			c.Name = c.Key
		}
		out = append(out, c)
	}
	return out
}

// categoryIndexByName returns the index of the category called name, or -1.
func (a *App) categoryIndexByName(name string) int {
	for i := range a.categories {
		if a.categories[i].Name == name {
			return i
		}
	}
	return -1
}

// existingOption is the mapping select's label for "reuse category i".
func (a *App) existingOption(i int) string {
	return fmt.Sprintf("%d. %s", i+1, a.categories[i].Name)
}

// newCatOption is the mapping select's label for "create a category called
// name".
func newCatOption(name string) string { return "＋ 新建“" + name + "”" }

// parseExistingOption reads the index back out of an existingOption label
// ("3. car" → 2).
func parseExistingOption(label string) (int, bool) {
	label = strings.TrimSpace(label)
	dot := strings.IndexByte(label, '.')
	if dot <= 0 {
		return 0, false
	}
	n, err := strconv.Atoi(label[:dot])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n - 1, true
}

// defaultChoices maps every source category to the app category of the same
// name, or to a new one (§4.2 step 3's default).
func (a *App) defaultChoices(ds *importer.Dataset) []importChoice {
	cats := datasetCategories(ds)
	out := make([]importChoice, 0, len(cats))
	for _, c := range cats {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			name = c.Key
		}
		ch := importChoice{Key: c.Key, Name: name, NewCat: true, Target: -1, Pick: newCatOption(name)}
		if idx := a.categoryIndexByName(name); idx >= 0 {
			ch.NewCat, ch.Target, ch.Pick = false, idx, a.existingOption(idx)
		}
		out = append(out, ch)
	}
	return out
}

// choiceFromPick resolves a row's selected option label into a mapping. A label
// the select wrote wins over the stored fields; an unparsable label (the
// category list changed under the dialog) falls back to "create".
func (a *App) choiceFromPick(ch importChoice) importChoice {
	if ch.Pick == "" {
		return ch
	}
	if idx, ok := parseExistingOption(ch.Pick); ok && idx >= 0 && idx < len(a.categories) {
		ch.NewCat, ch.Target = false, idx
		return ch
	}
	ch.NewCat, ch.Target = true, -1
	return ch
}

// resolveChoices turns the per-source-category mapping into source key → app
// category index, appending the categories whose choice is "new" (§4.2 step
// 5). It only reads existing categories, so a cancelled wizard changes nothing.
func (a *App) resolveChoices(ds *importer.Dataset, choices []importChoice) (map[string]int, int) {
	cats := datasetCategories(ds)
	catOf := make(map[string]int, len(cats))
	byKey := make(map[string]importChoice, len(choices))
	for _, ch := range choices {
		byKey[ch.Key] = ch
	}
	created := 0
	for _, c := range cats {
		ch, ok := byKey[c.Key]
		if !ok {
			ch = importChoice{Key: c.Key, Name: c.Name, NewCat: true, Target: -1}
		}
		ch = a.choiceFromPick(ch)
		if !ch.NewCat && ch.Target >= 0 && ch.Target < len(a.categories) {
			catOf[c.Key] = ch.Target
			continue
		}
		cat := irCategoryToApp(c)
		if name := strings.TrimSpace(ch.Name); name != "" {
			cat.Name = name
		}
		a.categories = append(a.categories, cat)
		catOf[c.Key] = len(a.categories) - 1
		created++
	}
	if created > 0 {
		a.clampCurCat()
		a.setCategoriesDirty()
	}
	return catOf, created
}

// importDataset resolves the category mapping and applies the dataset. When
// nothing could be written it also drops the categories it just created, so a
// dataset that matches nothing leaves the project untouched.
func (a *App) importDataset(ds *importer.Dataset, choices []importChoice) importResult {
	catOf, created := a.resolveChoices(ds, choices)
	res := a.applyImport(ds, catOf)
	res.NewCategories = created
	if res.Annots == 0 && created > 0 {
		a.categories = a.categories[:len(a.categories)-created]
		a.clampCurCat()
		res.NewCategories = 0
		if a.store != nil {
			_ = a.store.SetCategories(a.categories)
		}
	}
	return res
}

// applyImport appends every annotation of every matching dataset image to the
// open folder's image, as one undo step. A dataset image whose File names no
// image of the folder is skipped with a warning; an annotation that cannot be
// converted is counted in SkippedAnns.
func (a *App) applyImport(ds *importer.Dataset, catOf map[string]int) importResult {
	var res importResult
	if ds == nil || len(ds.Images) == 0 || len(a.images) == 0 {
		return res
	}

	byRel := make(map[string]int, len(a.images))
	for i := range a.images {
		byRel[a.images[i].RelPath] = i
	}

	// One seen-set per image: annotation ids must stay unique within an image,
	// against the live annotations as well as among the imported ones.
	seen := make(map[int]map[string]bool, len(ds.Images))
	var adds []importAdd
	for _, src := range ds.Images {
		rel := cleanImportPath(src.File)
		idx, ok := byRel[rel]
		if !ok {
			res.SkippedImages++
			res.addWarning("未找到图片 %s（其 %d 条标注已跳过）", rel, len(src.Annotations))
			continue
		}
		if len(src.Annotations) == 0 {
			continue
		}

		var stats convStats
		anns := make([]Ann, 0, len(src.Annotations))
		for _, x := range src.Annotations {
			an, ok := irAnnToApp(x, catOf, &stats)
			if !ok {
				res.SkippedAnns++
				continue
			}
			anns = append(anns, an)
		}
		res.Attrs += stats.attrs
		res.DroppedRings += stats.droppedRings
		if stats.droppedRings > 0 {
			res.addWarning("%s: %d 个内部环超出单环模型，已丢弃", rel, stats.droppedRings)
		}
		if len(anns) == 0 {
			continue
		}

		ids := seen[idx]
		if ids == nil {
			ids = make(map[string]bool, len(a.images[idx].Anns)+len(anns))
			for _, live := range a.images[idx].Anns {
				ids[live.ID] = true
			}
			seen[idx] = ids
		}
		for k := range anns {
			if anns[k].ID == "" || ids[anns[k].ID] {
				anns[k].ID = newAnnID()
			}
			ids[anns[k].ID] = true
		}

		adds = append(adds, importAdd{img: idx, anns: anns})
		res.Images++
		res.Annots += len(anns)
	}

	if len(adds) == 0 {
		if res.SkippedImages == 0 {
			res.addWarning("数据集里没有可导入的标注")
		}
		return res
	}

	a.push(cmdImport(a, adds))
	return res
}

// cmdImport returns the command that appends every add's annotations in one
// undo step, so the whole import is a single Ctrl+Z (§4.2 step 5). Each Do
// records the length it started from, and Undo truncates back to it: the
// annotations of an import are always the tail of the list (the undo stack is
// LIFO, so nothing appends behind them before they are undone).
func cmdImport(a *App, adds []importAdd) Command {
	before := make([]int, len(adds))
	applied := false

	insert := func() {
		for i, add := range adds {
			before[i] = -1
			if add.img < 0 || add.img >= len(a.images) {
				continue
			}
			anns := a.images[add.img].Anns
			before[i] = len(anns)
			for _, an := range add.anns {
				anns = append(anns, cloneAnn(an))
			}
			a.images[add.img].Anns = anns
			// Every touched image is written, not only the one push marks
			// (docs/phase3-design.md §4.2 step 5).
			a.setDirty(add.img)
		}
		applied = true
	}
	remove := func() {
		if !applied {
			return
		}
		for i := len(adds) - 1; i >= 0; i-- {
			add := adds[i]
			if before[i] < 0 || add.img < 0 || add.img >= len(a.images) {
				continue
			}
			anns := a.images[add.img].Anns
			if before[i] <= len(anns) {
				a.images[add.img].Anns = anns[:before[i]:before[i]]
			}
			a.setDirty(add.img)
		}
		applied = false
	}

	total := 0
	for _, add := range adds {
		total += len(add.anns)
	}
	first := -1
	if len(adds) > 0 {
		first = adds[0].img
	}
	return Command{
		Name: fmt.Sprintf("import %d", total),
		Img:  first,
		Do:   insert,
		Undo: remove,
	}
}
