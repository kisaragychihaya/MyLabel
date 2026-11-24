package app

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mylabel/internal/imaging"
	"mylabel/internal/store"
)

// App is the whole application state. The view is rebuilt from it every
// frame; other goroutines mutate it only through win.Update.
type App struct {
	win *mygo.Window

	// current folder and its images
	dir    string
	images []ImageInfo
	selImg int // index into images, -1 = none
	loaded int // index of the image currently loaded into bmp/tiled
	grid   ui.GridState

	// store is the SQLite working repository of the opened folder, at
	// <dir>/mylabel.db; nil until a folder is opened.
	store *store.Store
	// dirtyImages holds the ids of the images whose annotations changed since
	// the last save: save rewrites only those rows.
	dirtyImages map[int64]bool

	// thumbnails, keyed by image path (never stored on row elements)
	thumbs map[string]*ui.Bitmap

	// main image: whole bitmap, or tiled when too large for one texture
	bmp        *ui.Bitmap
	tiled      *TiledImage
	imgW, imgH float64

	// viewport: pan is screen-space offset, zoom scales image pixels to DIP
	zoom float64
	panX float64
	panY float64
	// fitOnLoad asks the next Draw, which alone knows the canvas size, to fit
	// and center the image. Set when an image loads, consumed by one frame.
	fitOnLoad bool

	// annotation state
	//
	// The selection is a set of annotation indices of the current image
	// (docs/phase2-design.md §5): selSet holds every selected annotation,
	// selPrimary the one clicked last (-1 = none). Only a sole selection has
	// handles, resize, rotate and vertex edits; the set as a whole is deleted,
	// re-categorized and moved as one.
	selSet     map[int]bool
	selPrimary int
	curCat     int
	categories []Category

	// paletteCat is the category the color palette dialog edits; paletteOpen
	// shows it (docs/phase2-design.md §6).
	paletteCat  int
	paletteOpen bool

	// wiz is the import wizard's state (docs/phase3-design.md §4.2): the
	// dialog's step, the parsed dataset and the category mapping live here, not
	// in the view, which is rebuilt every frame (design rule 1).
	wiz wizardState

	// props is the annotation properties dialog's state (docs/phase3-design.md
	// §5.1), for the same reason: it outlives the frame it is built in.
	props propsState

	// catPick is the category picker a double-click on a chip opens, and zMenu
	// the z-order menu a right-click on an annotation opens: both name their
	// annotation by image and index, guarded by its ID (see annmenu.go).
	catPick catPickState
	zMenu   zMenuState

	// ex is the export wizard's state (docs/phase4-design.md §9), on App for the
	// same reason as wiz: the plan, the chosen folder and the running progress
	// outlive the frame that shows them.
	ex exportState

	// toast is a message the next frame shows as a toast. A background export
	// finishes off the main thread and cannot call c.Toast itself (design rule 2),
	// so it leaves the text here for the main thread to show.
	toast string

	// interaction state machine
	mode     Mode
	tool     Tool // annotation tool: rectangles or polygons (docs/phase2-design.md §4.1)
	draft    Ann  // rubber-band preview while ModeCreating; category/ID of a polygon draft
	drag     dragState
	spacePan bool // Space held: left-drag pans instead of creating (labelme-style)

	// Click-by-click draft in progress (ModePolyCreate): the vertices clicked so
	// far, in image pixels — a polygon's, or the four corners of a rotated box
	// (§5.2) — and the pointer's image position the rubber band follows.
	polyDraft []Point
	hover     Point
	hasHover  bool

	// draw scratch buffers for the canvas's polygon rendering, reused every
	// frame by the single Draw callback (never read outside it)
	scratchPts    [][2]float32
	scratchPath   ui.Path
	scratchColors []ui.Color

	// chipSizes is one measured chip size per category, in canvas DIP, and
	// chipNames the names they were measured from. The frame measures them
	// (refreshChipSizes) because the pointer has to hit exactly the chips that
	// frame painted; they outlive the frame for the same reason.
	chipSizes []chipSize
	chipNames []string

	// showLabels draws each annotation's category-name chip on the canvas
	// (labelme-style); the toolbar's 标签 button and the L key toggle it.
	showLabels bool

	// canvasRef binds the canvas element; focusCanvas asks the next frame to
	// move keyboard focus to the canvas (after opening a folder or image).
	canvasRef   ui.Handle
	focusCanvas bool

	undoStack []Command
	redoStack []Command

	dirty      bool
	saveTimer  *time.Timer
	lastStatus string
}

// New returns an initialized App.
func New() *App {
	return &App{
		selImg:      -1,
		loaded:      -1,
		selSet:      make(map[int]bool),
		selPrimary:  -1,
		curCat:      -1, // no categories yet: see clampCurCat
		zoom:        1,
		thumbs:      make(map[string]*ui.Bitmap),
		dirtyImages: make(map[int64]bool),
		showLabels:  true,
	}
}

// SetWindow stores the window handle; called once from main's WhenReady.
func (a *App) SetWindow(w *mygo.Window) { a.win = w }

// curImage returns the currently loaded image, or nil.
func (a *App) curImage() *ImageInfo {
	if a.loaded < 0 || a.loaded >= len(a.images) {
		return nil
	}
	return &a.images[a.loaded]
}

// --- selection (docs/phase2-design.md §5) ----------------------------------
//
// The selection is a set of annotation indices of the loaded image. Every
// reader goes through these helpers, never through the map, so emptiness,
// the sole-selection rule and the primary index stay consistent.

// isSelected reports whether annotation i of the loaded image is selected.
func (a *App) isSelected(i int) bool { return a.selSet[i] }

// selectionCount returns how many annotations are selected.
func (a *App) selectionCount() int { return len(a.selSet) }

// soleSelection returns the selected index when exactly one annotation is
// selected, and -1 otherwise. Handles, resize/rotate and polygon vertex edits
// are offered only there (§5).
func (a *App) soleSelection() int {
	if len(a.selSet) == 1 && a.selPrimary >= 0 && a.selSet[a.selPrimary] {
		return a.selPrimary
	}
	return -1
}

// selectedIndices returns the selected indices in ascending order, so callers
// that remove or edit several annotations walk the list in a stable order.
func (a *App) selectedIndices() []int {
	out := make([]int, 0, len(a.selSet))
	for i := range a.selSet {
		out = append(out, i)
	}
	sort.Ints(out)
	return out
}

// selectOnly replaces the selection with i alone. A negative index clears it.
func (a *App) selectOnly(i int) {
	a.clearSelection()
	if i < 0 {
		return
	}
	a.selSet[i] = true
	a.selPrimary = i
}

// selectToggle flips i in the selection (Ctrl+click, §5): selecting makes it
// the primary, deselecting hands the primary to the lowest remaining index.
func (a *App) selectToggle(i int) {
	if i < 0 {
		return
	}
	if a.selSet[i] {
		delete(a.selSet, i)
		if a.selPrimary == i {
			a.selPrimary = a.lowestSelected()
		}
		return
	}
	a.selSet[i] = true
	a.selPrimary = i
}

// selectAll selects every annotation of the loaded image (Ctrl+A, §5).
func (a *App) selectAll() {
	a.clearSelection()
	img := a.curImage()
	if img == nil || len(img.Anns) == 0 {
		return
	}
	for i := range img.Anns {
		a.selSet[i] = true
	}
	a.selPrimary = len(img.Anns) - 1
}

// clearSelection drops the whole selection.
func (a *App) clearSelection() {
	if a.selSet == nil {
		a.selSet = make(map[int]bool)
	}
	clear(a.selSet)
	a.selPrimary = -1
}

// lowestSelected returns the smallest selected index, or -1.
func (a *App) lowestSelected() int {
	best := -1
	for i := range a.selSet {
		if best < 0 || i < best {
			best = i
		}
	}
	return best
}

// pruneSelection drops indices that no longer name an annotation of the loaded
// image: deleting, undoing a create or switching images can leave them behind.
func (a *App) pruneSelection() {
	anns := 0
	if img := a.curImage(); img != nil {
		anns = len(img.Anns)
	}
	for i := range a.selSet {
		if i < 0 || i >= anns {
			delete(a.selSet, i)
		}
	}
	if a.selPrimary >= 0 && !a.selSet[a.selPrimary] {
		a.selPrimary = a.lowestSelected()
	}
}

// handlesAnn returns the annotation whose handles drawHandles paints and whose
// handles the hit test offers, or nil when the selection is not exactly one
// annotation (§5).
func (a *App) handlesAnn() *Ann {
	i := a.soleSelection()
	img := a.curImage()
	if i < 0 || img == nil || i >= len(img.Anns) {
		return nil
	}
	return &img.Anns[i]
}

// setDirty marks the document dirty and records which image's annotations
// changed, so save rewrites only those rows. img indexes a.images; a negative
// value means the currently loaded image (undo/redo pass the index of the
// command's own image, whose row must not be lost after switching images).
//
// The first change to a fresh image also upgrades it to labeling
// (docs/phase2-design.md §7).
func (a *App) setDirty(img int) {
	a.dirty = true
	if id := a.imageID(img); id > 0 {
		a.dirtyImages[id] = true
	}
	a.markLabeling(img)
	a.scheduleSave()
}

// imageID returns the store id of images[img]; negative img means the loaded
// image, and 0 means "no row" (image unknown or not stored yet).
func (a *App) imageID(img int) int64 {
	if img < 0 {
		img = a.loaded
	}
	if img < 0 || img >= len(a.images) {
		return 0
	}
	return a.images[img].ID
}

// openDir opens dir as the current folder: scan the files recursively, sync
// the SQLite working repository with them, import a legacy PoC JSON document
// when the repository has no annotations yet, then load the images for the
// view. See docs/phase1-design.md §5.2.
//
// An Ultralytics dataset subfolder (X/images/train, X/images) is widened to the
// dataset root X first, so every split shares one workspace and one store.
func (a *App) openDir(dir string) {
	_ = a.save() // flush the previous folder through its store
	a.closeStore()

	root := resolveOpenDir(dir)
	widened := root != dir
	dir = root

	scanned, err := imaging.Scan(dir)
	if err != nil {
		a.lastStatus = "扫描失败: " + err.Error()
		return
	}

	st, err := store.Open(filepath.Join(dir, dbName))
	if err != nil {
		a.lastStatus = "打开数据库失败: " + err.Error()
		return
	}

	recs := make([]store.ImageRec, len(scanned))
	keep := make([]string, len(scanned))
	rotated := 0
	for i, s := range scanned {
		recs[i] = store.ImageRec{
			RelPath: s.RelPath,
			Name:    s.Name,
			Width:   s.Width,
			Height:  s.Height,
			SHA256:  s.SHA256,
		}
		keep[i] = s.RelPath
		if s.Rotated {
			rotated++
		}
	}
	// Nothing is committed to the App before the repository is consistent, so
	// a failure leaves the previously opened folder on screen.
	if err := st.UpsertImages(recs); err != nil {
		_ = st.Close()
		a.lastStatus = "图片入库失败: " + err.Error()
		return
	}
	if _, err := st.DeleteMissingImages(keep); err != nil {
		_ = st.Close()
		a.lastStatus = "清理失效记录失败: " + err.Error()
		return
	}

	loaded, err := st.LoadImages()
	if err != nil {
		_ = st.Close()
		a.lastStatus = "读取数据库失败: " + err.Error()
		return
	}
	// A legacy document and an IR project are both one-time imports into an
	// annotation-free repository, and an IR project wins when both are there
	// (docs/phase3-design.md §4.2): the legacy file is only read when no
	// project.json exists.
	var migrated int
	var migrateErr error
	if !hasIRProject(dir) {
		migrated, migrateErr = migrateLegacyDoc(dir, st, loaded)
		if migrated > 0 {
			if loaded, err = st.LoadImages(); err != nil {
				_ = st.Close()
				a.lastStatus = "读取数据库失败: " + err.Error()
				return
			}
		}
	}

	a.dir = dir
	a.store = st
	a.dirtyImages = make(map[int64]bool)
	a.images = imagesFromRecs(dir, loaded)
	seedAnnIDCounter(a.images)
	a.thumbs = make(map[string]*ui.Bitmap)
	a.selImg = -1
	a.loaded = -1
	a.clearSelection()
	a.bmp = nil
	a.tiled = nil
	a.zoom = 1
	a.panX, a.panY = 0, 0
	a.mode = ModeIdle
	a.tool = ToolRect
	a.polyDraft = nil
	a.hasHover = false
	a.undoStack = nil
	a.redoStack = nil
	a.dirty = false

	// A workspace starts with no categories: the panel's 添加 makes the first
	// one, and until then no annotation can be created (contract §1).
	a.categories = nil
	if cats, err := st.Categories(); err == nil && len(cats) > 0 {
		a.categories = append([]Category(nil), cats...)
	}
	a.paletteOpen = false
	a.paletteCat = 0
	a.closeProps() // the dialog edits an annotation of the folder being replaced
	a.catPick, a.zMenu = catPickState{}, zMenuState{}
	a.curCat = 0
	a.clampCurCat()

	// Direct open of an IR project: the annotations of project.json land in the
	// empty repository now, so the rest of the session works on them as on any
	// other annotations.
	var irRes importResult
	var irErr error
	if !repoHasAnnotations(loaded) && hasIRProject(dir) {
		irRes, irErr = a.importIRProject(dir)
		if irErr == nil && (irRes.Annots > 0 || irRes.NewCategories > 0) {
			if err := a.save(); err != nil {
				irErr = err
			} else {
				seedAnnIDCounter(a.images)
			}
		}
	}

	a.grid.Key = func(item int) any { return a.images[item].Path }
	a.grid.Selected = &a.selImg

	a.startThumbWorkers()

	if len(a.images) > 0 {
		a.selImg = 0
		a.loadImage(0)
		a.loaded = 0
	}

	status := openDirStatus(dir, len(a.images), rotated, migrated)
	if irRes.Images > 0 {
		status += fmt.Sprintf("，已导入 IR 项目 %d 张图片 %d 条标注", irRes.Images, irRes.Annots)
		if irRes.NewCategories > 0 {
			status += fmt.Sprintf("（新建类别 %d 个）", irRes.NewCategories)
		}
		if irRes.SkippedAnns > 0 {
			status += fmt.Sprintf("，%d 条标注无法转换已跳过", irRes.SkippedAnns)
		}
	}
	if irErr != nil {
		status += "，IR 项目导入失败: " + irErr.Error()
	}
	if migrateErr != nil {
		if migrated > 0 {
			status += "，部分旧 JSON 未迁移: " + migrateErr.Error()
		} else {
			status += "，旧 JSON 未迁移: " + migrateErr.Error()
		}
	}
	a.lastStatus = status
	if widened {
		a.lastStatus = yoloRootNote + a.lastStatus
	}
}

// openDirStatus builds the status bar line of a freshly opened folder. The
// counts that did something are mentioned only when non-zero.
func openDirStatus(dir string, images, rotated, migrated int) string {
	status := fmt.Sprintf("已打开 %s（%d 张图片）", dir, images)
	if rotated > 0 {
		status += fmt.Sprintf("，已转正 %d 张", rotated)
	}
	if migrated > 0 {
		status += fmt.Sprintf("，已迁移旧 JSON 标注 %d 张", migrated)
	}
	return status
}

// imagesFromRecs assembles the view's image list from stored rows.
func imagesFromRecs(dir string, recs []store.ImageRec) []ImageInfo {
	images := make([]ImageInfo, len(recs))
	for i, rec := range recs {
		images[i] = ImageInfo{
			ID:      rec.ID,
			Path:    filepath.Join(dir, filepath.FromSlash(rec.RelPath)),
			RelPath: rec.RelPath,
			Name:    rec.Name,
			Width:   rec.Width,
			Height:  rec.Height,
			Status:  rec.Status,
			Anns:    annsFromRecs(rec.Anns),
			SHA256:  rec.SHA256,
		}
	}
	return images
}

// closeStore releases the database handle of the folder being replaced.
func (a *App) closeStore() {
	if a.store == nil {
		return
	}
	_ = a.store.Close()
	a.store = nil
}

// Close flushes pending changes and releases the database. It is safe to call
// more than once.
func (a *App) Close() {
	if a.saveTimer != nil {
		a.saveTimer.Stop()
		a.saveTimer = nil
	}
	_ = a.save()
	a.closeStore()
}

// View builds the interface from the state, on the main thread, every frame.
func (a *App) View(c *ui.Context) {
	if c.Shortcut(ui.Cmd, ui.KeyS) {
		_ = a.save()
	}
	if c.Shortcut(ui.Cmd, ui.KeyZ) {
		a.undo()
	}
	if c.Shortcut(ui.Cmd|ui.Shift, ui.KeyZ) {
		a.redo()
	}
	if c.Shortcut(ui.Cmd, ui.KeyD) {
		a.toggleImageStatus()
	}

	t := c.Theme()
	root := ui.Column(c).Fill().Background(t.Background)
	root.Children(func() {
		a.toolbar(c)
		// Stretch pins the canvas to the row's height: a Row centers its
		// children vertically by default, which would leave the canvas 0 high.
		ui.Row(c).FillWidth().Grow(1).AlignItems(ui.Stretch).Children(func() {
			a.canvasView(c)
			a.categoryPanel(c)
		})
		a.thumbStrip(c)
		a.statusBar(c)
		a.paletteModal(c)
		a.importModal(c)
		a.exportModal(c)
		a.propsModal(c)
		a.catPickModal(c)
	})
	a.handleDroppedFiles(root)
	a.flushToast(c)
}

// flushToast shows a message a background task left for the main thread, once.
func (a *App) flushToast(c *ui.Context) {
	if a.toast == "" {
		return
	}
	c.Toast(a.toast)
	a.toast = ""
}

func (a *App) toolbar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).FillWidth().Padding(6).Gap(8).Background(t.Surface).Children(func() {
		if ui.Button(c.Key("open"), "打开文件夹").Clicked() {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
				Parent:    a.win,
				Title:     "选择图片文件夹",
				Directory: true,
			})
			if err == nil && len(paths) > 0 {
				a.openDir(paths[0])
			}
		}
		if ui.Button(c.Key("save"), "保存 (Ctrl+S)").Clicked() {
			_ = a.save()
		}
		if ui.Button(c.Key("export"), "导出 JSON").Clicked() {
			if err := a.exportJSON(a.dir); err != nil {
				a.lastStatus = "导出失败: " + err.Error()
			} else {
				a.lastStatus = "已导出 " + filepath.Join(a.dir, docName)
			}
		}
		if ui.Button(c.Key("import"), "导入标注").Clicked() {
			a.openImportWizard()
		}
		if ui.Button(c.Key("exportDataset"), "导出数据集").Clicked() {
			a.openExportWizard()
		}
		ui.Text(c, "工具:").Padding(8, 0, 0, 0)
		a.toolButton(c, "toolRect", "矩形 (R)", ToolRect)
		a.toolButton(c, "toolPoly", "多边形 (P)", ToolPoly)
		a.toolButton(c, "toolObb", "旋转框 (O)", ToolObb)
		a.labelButton(c)
		ui.Text(c, "数字键 1-9 改类别，双击标注改属性，Ctrl+D 标记完成，A/D 切换图片，Ctrl+B 沿用上一张").Padding(8, 0, 0, 0).TextColor(t.TextMuted)
	})
}

// labelButton toggles the category chips, highlighted while they are shown —
// they are by default.
func (a *App) labelButton(c *ui.Context) {
	var btn ui.Element
	if a.showLabels {
		btn = ui.PrimaryButton(c.Key("labels"), "标签 (L)")
	} else {
		btn = ui.Button(c.Key("labels"), "标签 (L)")
	}
	if btn.Clicked() {
		a.toggleLabels()
	}
}

// toggleLabels flips the category chips and reports it on the status bar.
func (a *App) toggleLabels() {
	a.showLabels = !a.showLabels
	if a.showLabels {
		a.lastStatus = "已显示类别标签"
	} else {
		a.lastStatus = "已隐藏类别标签"
	}
}

// toolButton renders one annotation-tool button, highlighted while that tool
// is active (docs/phase2-design.md §4.1).
func (a *App) toolButton(c *ui.Context, key, label string, t Tool) {
	var btn ui.Element
	if a.tool == t {
		btn = ui.PrimaryButton(c.Key(key), label)
	} else {
		btn = ui.Button(c.Key(key), label)
	}
	if btn.Clicked() {
		a.setTool(t)
	}
}

func (a *App) thumbStrip(c *ui.Context) {
	if len(a.images) == 0 {
		return
	}
	t := c.Theme()
	ui.Box(c).FillWidth().Height(120).Background(t.Surface).Children(func() {
		ui.GridView(c, &a.grid, len(a.images), 104, 104, func(i int) {
			cell := ui.Box(c).Padding(4).Radius(6)
			if a.selImg == i {
				cell.Border(2, t.Accent)
			}
			cell.Children(func() {
				if bmp := a.thumbOf(a.images[i].Path); bmp != nil {
					ui.Image(c, bmp).Fit(ui.Contain).Grow(1)
				} else {
					ui.Box(c).Grow(1).Center().Children(func() {
						ui.Spinner(c)
					})
				}
				ui.Text(c, a.images[i].Name).FontSize(10).SingleLine().TruncateMiddle()
				// The status badge of §7 sits in the cell's top right corner, over
				// the thumbnail. Its Label is the stored status word, which is what
				// the tests find the dot by.
				if dot, ok := statusDotColor(a.images[i].Status, t); ok {
					ui.Box(c).Absolute().Top(2).Right(2).Width(8).Height(8).Radius(4).
						Background(dot).Label(normalizeStatus(a.images[i].Status))
				}
			})
		}).Grow(1)
	})

	// Grid selection changed -> load the image. The old image's selection
	// never carries over (§5): indices mean a different annotation set.
	if a.selImg != a.loaded && a.selImg >= 0 && a.selImg < len(a.images) {
		a.loadImage(a.selImg)
		a.loaded = a.selImg
		a.clearSelection()
		a.mode = ModeIdle
		a.polyDraft = nil
		a.hasHover = false
		// The properties dialog and the annotations' menus name an annotation of
		// the image being left.
		a.closeProps()
		a.catPick, a.zMenu = catPickState{}, zMenuState{}
	}
}

func (a *App) statusBar(c *ui.Context) {
	t := c.Theme()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	cur := "-"
	annCount := 0
	status := ""
	if img := a.curImage(); img != nil {
		cur = img.RelPath // the folder-relative path, so subfolders stay distinct
		annCount = len(img.Anns)
		status = statusLabel(img.Status)
	}
	ui.Row(c).FillWidth().Padding(4, 8, 4, 8).Gap(16).Background(t.Surface).Children(func() {
		ui.Textf(c, "图片 %d/%d", a.loaded+1, len(a.images)).FontSize(11)
		ui.Text(c, cur).FontSize(11)
		ui.Textf(c, "标注 %d", annCount).FontSize(11)
		if status != "" {
			ui.Text(c, "状态 "+status).FontSize(11)
		}
		if n := a.selectionCount(); n > 1 {
			ui.Textf(c, "选中 %d", n).FontSize(11).TextColor(t.Accent)
		}
		ui.Textf(c, "缩放 %.0f%%", a.zoom*100).FontSize(11)
		ui.Textf(c, "内存 %.0f MB", float64(ms.Alloc)/1048576).FontSize(11)
		if a.dirty {
			ui.Text(c, "●未保存").FontSize(11).TextColor(t.Warning)
		}
		ui.Text(c, a.lastStatus).FontSize(11).TextColor(t.TextMuted)
	})
}
