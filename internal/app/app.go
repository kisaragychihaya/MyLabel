package app

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mylabel/internal/imaging"
	"mylabel/internal/sam2"
	"mylabel/internal/store"
	"mylabel/locales"
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
	tool     Tool // the tool the strip selects: annotation or view (docs/phase6-design.md §1)
	draft    Ann  // rubber-band preview while ModeCreating; category/ID of a polygon draft
	drag     dragState
	spacePan bool // Space held: left-drag pans instead of creating (labelme-style)
	// zoomOutNext is the zoom tool's direction: its canvas clicks zoom out
	// instead of in. The strip's zoom button and Z flip it, and coming to the
	// tool starts it at a zoom in (docs/phase6-design.md §3).
	zoomOutNext bool

	// AI assist (docs/phase7-design.md §6). sam is the native client; nil means
	// Detect found no library next to the executable, so the tool does not exist
	// at all. aiPoints are the foreground points collected so far (image pixels),
	// aiRectMode the output mode (false = polygon, true = bounding box), aiBusy
	// the single inference in flight, and aiGen the generation counter that lets a
	// result recognize it arrived too late. aiEmb is the encoder embedding cached
	// for images[aiEmbFor] (-1 = none): it is released when the image, the folder
	// or the app goes away.
	sam *sam2.Client
	// First-run runtime acquisition (aidl.go): aiBootState renders the consent
	// or progress modal, aiBootGen discards stale download results, aiDL is
	// what the progress modal shows, and aiDownloadModel is the test hook
	// standing in for sam2.DownloadModel.
	aiBootState     int
	aiBootDir       string
	aiBootGen       uint64
	aiDL            aiDLUI
	aiDLCancel      context.CancelFunc
	aiDownloadModel func(ctx context.Context, dir string, cb func(sam2.ModelProgress)) error
	aiRectMode bool
	aiPoints   []Point
	aiBusy     bool
	aiGen      uint64
	aiEmb      sam2.Embedding
	aiEmbFor   int
	// The encode running ahead of the first click's inference (§6): aiEncBusy
	// says one is in flight and aiEncFor which image it belongs to (-1 = none).
	// There is at most one, app-wide, whoever asked for it. It shares aiGen with
	// the inferences, so an image switch or a new trigger makes it stale exactly
	// like a late inference result.
	aiEncBusy bool
	aiEncFor  int
	// aiPredict replaces the real pipeline in tests: it answers one inference for
	// a prompt and needs no DLL (aitool.go). aiEncodeHook does the same for the
	// encoder alone; nil means the real client encodes.
	aiPredict    aiPredictFunc
	aiEncodeHook aiEncodeFunc

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
	a := &App{
		selImg:      -1,
		loaded:      -1,
		selSet:      make(map[int]bool),
		selPrimary:  -1,
		curCat:      -1, // no categories yet: see clampCurCat
		zoom:        1,
		thumbs:      make(map[string]*ui.Bitmap),
		dirtyImages: make(map[int64]bool),
		showLabels:  true,
		aiEmbFor:    -1,
		aiEncFor:    -1,
	}
	a.detectSAM2()
	return a
}

// detectSAM2 creates the native client when the runtime sits next to the
// executable (docs/phase7-design.md §4): the DLL plus the model set under
// models/. When the DLL is missing entirely, a fire-and-forget goroutine
// fetches it from the dist zip — pure filesystem work, no App state, so the
// app startup never waits on it. The models are the user's decision: the AI
// button is always in the strip, and pressing it without a runtime opens the
// consent modal instead (aidl.go).
func (a *App) detectSAM2() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dir := filepath.Dir(exe)
	a.aiBootDir = dir
	if cfg, ok := sam2.Detect(dir); ok {
		a.sam = sam2.New(cfg)
		return
	}
	if !aiDownloadEnvOff() {
		go func() { _ = sam2.EnsureDLL(dir) }()
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
		a.lastStatus = locales.StatusScanFailed(err.Error())
		return
	}

	st, err := store.Open(filepath.Join(dir, dbName))
	if err != nil {
		a.lastStatus = locales.StatusOpenDBFailed(err.Error())
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
		a.lastStatus = locales.StatusImagesUpsertFailed(err.Error())
		return
	}
	if _, err := st.DeleteMissingImages(keep); err != nil {
		_ = st.Close()
		a.lastStatus = locales.StatusCleanupFailed(err.Error())
		return
	}

	loaded, err := st.LoadImages()
	if err != nil {
		_ = st.Close()
		a.lastStatus = locales.StatusLoadDBFailed(err.Error())
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
				a.lastStatus = locales.StatusLoadDBFailed(err.Error())
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
	a.tool = ToolSelect
	a.polyDraft = nil
	a.hasHover = false
	// The AI assist belongs to the images of the folder being replaced: the
	// collected points and the cached embedding do not survive it (§6).
	a.aiDropPoints()
	a.aiReleaseEmbedding()
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

	a.lastStatus = openDirStatusLine(dir, len(a.images), openDirNotes{
		widened:      widened,
		rotated:      rotated,
		migrated:     migrated,
		importResult: irRes,
		importErr:    irErr,
		migrationErr: migrateErr,
	})
}

// openDirNotes collects what opening a folder changed, for the status line.
type openDirNotes struct {
	// widened says the folder resolved to an Ultralytics dataset root.
	widened bool
	// rotated counts the JPEGs physically straightened on this scan.
	rotated int
	// migrated counts the images imported from the legacy JSON document.
	migrated int
	// importResult is what the IR project import brought in, if any.
	importResult importResult
	// importErr and migrationErr are the failures of those two one-time
	// imports; they are reported instead of the counts.
	importErr    error
	migrationErr error
}

// The two layouts statusOpened's select understands: the folder was widened to
// the Ultralytics dataset root, or opened as given.
const (
	layoutWiden = "widen"
	layoutKeep  = "keep"
)

// openDirStatusLine builds the status bar line of a freshly opened folder. The
// counts that did something are mentioned only when non-zero. A failed IR
// import is reported before a failed legacy migration: both can only happen on
// an annotation-free repository, and the IR project is the one that was tried
// first (docs/phase3-design.md §4.2).
func openDirStatusLine(dir string, images int, n openDirNotes) string {
	layout := layoutKeep
	if n.widened {
		layout = layoutWiden
	}
	switch {
	case n.importErr != nil:
		return locales.StatusIRImportFailed(layout, dir, images, n.importErr.Error())
	case n.migrationErr != nil && n.migrated > 0:
		return locales.StatusLegacyMigrationPartial(layout, dir, images, n.migrated, n.migrationErr.Error())
	case n.migrationErr != nil:
		return locales.StatusLegacyMigrationFailed(layout, dir, images, n.migrationErr.Error())
	}
	return locales.StatusOpened(layout, dir, images, n.rotated, n.migrated,
		n.importResult.Images, n.importResult.Annots, n.importResult.NewCategories,
		n.importResult.SkippedAnns)
}

// openDirStatus is the plain open-folder line: no dataset widening, no IR
// project and no import failure.
func openDirStatus(dir string, images, rotated, migrated int) string {
	return openDirStatusLine(dir, images, openDirNotes{rotated: rotated, migrated: migrated})
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
	// The AI assist goes with the app: the collected points, the cached embedding
	// and then the native client, in that order — an embedding has to be released
	// before the client it came from closes (docs/phase7-design.md §6).
	a.aiDropPoints()
	a.aiBusy = false
	a.aiReleaseEmbedding()
	if a.sam != nil {
		a.sam.Close()
	}
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
			a.toolStrip(c)
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
		a.aiDownloadModal(c)
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
		if ui.Button(c.Key("open"), locales.ToolbarOpenFolder()).Clicked() {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
				Parent:    a.win,
				Title:     locales.ToolbarOpenFolderTitle(),
				Directory: true,
			})
			if err == nil && len(paths) > 0 {
				a.openDir(paths[0])
			}
		}
		if ui.Button(c.Key("undo"), locales.ToolbarUndo()).
			Tooltip(locales.ToolbarUndoTip()).
			Disabled(len(a.undoStack) == 0).Clicked() {
			a.undo()
		}
		if ui.Button(c.Key("redo"), locales.ToolbarRedo()).
			Tooltip(locales.ToolbarRedoTip()).
			Disabled(len(a.redoStack) == 0).Clicked() {
			a.redo()
		}
		if ui.Button(c.Key("save"), locales.ToolbarSave()).Clicked() {
			_ = a.save()
		}
		if ui.Button(c.Key("export"), locales.ToolbarExportJSON()).Clicked() {
			if err := a.exportJSON(a.dir); err != nil {
				a.lastStatus = locales.StatusExportJSONFailed(err.Error())
			} else {
				a.lastStatus = locales.StatusExportedJSON(filepath.Join(a.dir, docName))
			}
		}
		if ui.Button(c.Key("import"), locales.ToolbarImportAnnotations()).Clicked() {
			a.openImportWizard()
		}
		if ui.Button(c.Key("exportDataset"), locales.ToolbarExportDataset()).Clicked() {
			a.openExportWizard()
		}
		a.labelButton(c)
		ui.Text(c, locales.ToolbarShortcutHint()).Padding(8, 0, 0, 0).TextColor(t.TextMuted)
	})
}

// labelButton toggles the category chips, highlighted while they are shown —
// they are by default.
func (a *App) labelButton(c *ui.Context) {
	var btn ui.Element
	if a.showLabels {
		btn = ui.PrimaryButton(c.Key("labels"), locales.ToolbarLabels())
	} else {
		btn = ui.Button(c.Key("labels"), locales.ToolbarLabels())
	}
	if btn.Clicked() {
		a.toggleLabels()
	}
}

// toggleLabels flips the category chips and reports it on the status bar.
func (a *App) toggleLabels() {
	a.showLabels = !a.showLabels
	if a.showLabels {
		a.lastStatus = locales.ToolbarLabelsShown()
	} else {
		a.lastStatus = locales.ToolbarLabelsHidden()
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
		// The AI assist's points and its cached embedding name the image being
		// left, so both go (docs/phase7-design.md §6). A/D reach here too: goImage
		// only moves selImg and leaves the load to this reconcile.
		a.aiDropPoints()
		a.aiReleaseEmbedding()
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
		ui.Text(c, locales.StatusImageOf(a.loaded+1, len(a.images))).FontSize(11)
		ui.Text(c, cur).FontSize(11)
		ui.Text(c, locales.StatusAnnots(annCount)).FontSize(11)
		if status != "" {
			ui.Text(c, locales.StatusState(status)).FontSize(11)
		}
		if n := a.selectionCount(); n > 1 {
			ui.Text(c, locales.StatusSelectedCount(n)).FontSize(11).TextColor(t.Accent)
		}
		ui.Text(c, locales.StatusZoom(int(math.Round(a.zoom*100)))).FontSize(11)
		ui.Text(c, locales.StatusMemory(int(math.Round(float64(ms.Alloc)/1048576)))).FontSize(11)
		if a.dirty {
			ui.Text(c, locales.StatusUnsaved()).FontSize(11).TextColor(t.Warning)
		}
		ui.Text(c, a.lastStatus).FontSize(11).TextColor(t.TextMuted)
	})
}
