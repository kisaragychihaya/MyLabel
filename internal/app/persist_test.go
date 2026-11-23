package app

import (
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mylabel/internal/store"
)

// testAnns is the annotation set the persistence tests work with. Since phase 2
// a loaded rectangle carries its explicit kind.
var testAnns = []Ann{
	{ID: "ann_1", Cat: 1, Kind: KindRect, CX: 10, CY: 20, W: 30, H: 40, Angle: 0.5},
	{ID: "ann_2", Cat: 2, Kind: KindRect, CX: 60, CY: 25, W: 5, H: 6, Angle: 0},
}

// storeApp returns an App whose folder holds the given image files (relative
// paths, slash-separated) and whose store was opened by openDir. The App is
// closed with the test, which releases the database file.
func storeApp(t *testing.T, files ...string) *App {
	t.Helper()
	dir := t.TempDir()
	for _, name := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		writePNG(t, p, 100, 50)
	}
	a := New()
	t.Cleanup(a.Close)
	a.openDir(dir)
	if len(a.images) != len(files) {
		t.Fatalf("openDir 载入 %d 张图片，期望 %d：%s", len(a.images), len(files), a.lastStatus)
	}
	// A new folder has no categories; these tests edit and import, so give the
	// workspace the nine-category fixture list they assume.
	a.categories = defaultCategoryList()
	a.curCat = 0
	return a
}

// seedAnns stores anns on images[index] the way an edit does (setDirty + save).
func seedAnns(t *testing.T, a *App, index int, anns []Ann) {
	t.Helper()
	if index < 0 || index >= len(a.images) {
		t.Fatalf("images[%d] 不存在（共 %d 张）", index, len(a.images))
	}
	a.images[index].Anns = anns
	a.setDirty(index)
	if err := a.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
}

// reopen returns a fresh App on the same folder, as restarting the app does.
func reopen(t *testing.T, dir string) *App {
	t.Helper()
	b := New()
	t.Cleanup(b.Close)
	b.openDir(dir)
	return b
}

func TestSaveWritesStore(t *testing.T) {
	a := storeApp(t, "a.png")
	seedAnns(t, a, 0, testAnns)

	if a.dirty {
		t.Error("保存成功后 dirty 应为 false")
	}
	if len(a.dirtyImages) != 0 {
		t.Errorf("保存后 dirtyImages 应为空，得到 %v", a.dirtyImages)
	}
	if _, err := os.Stat(filepath.Join(a.dir, dbName)); err != nil {
		t.Errorf("缺少工作库 %s: %v", dbName, err)
	}
	// The PoC document is no longer written by a save: it is an export only.
	if _, err := os.Stat(filepath.Join(a.dir, docName)); !os.IsNotExist(err) {
		t.Errorf("保存不应再写 %s: %v", docName, err)
	}

	b := reopen(t, a.dir)
	if len(b.images) != 1 {
		t.Fatalf("重开后载入 %d 张图片", len(b.images))
	}
	got := b.images[0]
	if got.ID == 0 {
		t.Error("重开后 ID 为 0，应从库里回填")
	}
	if got.RelPath != "a.png" || got.Name != "a.png" {
		t.Errorf("RelPath/Name = %q/%q，期望 a.png/a.png", got.RelPath, got.Name)
	}
	if got.Width != 100 || got.Height != 50 {
		t.Errorf("尺寸 = %dx%d，期望 100x50", got.Width, got.Height)
	}
	// The first annotation edit upgrades the image from new to labeling, and
	// that status is written straight away, not with the debounced save (§7).
	if got.Status != store.StatusLabeling {
		t.Errorf("Status = %q，期望 %q", got.Status, store.StatusLabeling)
	}
	if !reflect.DeepEqual(got.Anns, testAnns) {
		t.Errorf("Anns = %+v\n期望 %+v", got.Anns, testAnns)
	}
	if !reflect.DeepEqual(b.categories, a.categories) {
		t.Errorf("Categories = %v，期望 %v", b.categories, a.categories)
	}
}

func TestSaveWritesOnlyDirtyImages(t *testing.T) {
	a := storeApp(t, "a.png", "b.png")
	if a.images[0].RelPath != "a.png" || a.images[1].RelPath != "b.png" {
		t.Fatalf("图片顺序 = %q, %q，期望 a.png, b.png", a.images[0].RelPath, a.images[1].RelPath)
	}
	seedAnns(t, a, 0, testAnns[:1])

	// b.png is changed in memory without an edit going through setDirty: with
	// no dirty flag, save must leave its row alone.
	a.images[1].Anns = []Ann{{ID: "ann_9", Cat: 0, CX: 1, CY: 2, W: 3, H: 4}}
	if err := a.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if a.dirty {
		t.Error("没有登记脏图片时保存后 dirty 应为 false")
	}

	b := reopen(t, a.dir)
	if n := len(b.images[0].Anns); n != 1 {
		t.Errorf("a.png 标点数 = %d，期望 1", n)
	}
	if n := len(b.images[1].Anns); n != 0 {
		t.Errorf("b.png 标点数 = %d，期望 0（未登记为脏，不应落库）", n)
	}

	// Registered as dirty, it does get written.
	seedAnns(t, a, 1, []Ann{{ID: "ann_9", Cat: 0, CX: 1, CY: 2, W: 3, H: 4}})
	c := reopen(t, a.dir)
	if n := len(c.images[1].Anns); n != 1 {
		t.Errorf("登记为脏后 b.png 标点数 = %d，期望 1", n)
	}
}

func TestSaveFailedKeepsDirty(t *testing.T) {
	a := storeApp(t, "a.png")
	a.images[0].Anns = testAnns
	a.setDirty(0)

	// A closed store fails every write: the edit must stay pending.
	_ = a.store.Close()
	if err := a.save(); err == nil {
		t.Fatal("关闭的工作库保存应失败")
	}
	if !a.dirty {
		t.Error("保存失败后 dirty 应为 true")
	}
	if !a.dirtyImages[a.images[0].ID] {
		t.Errorf("保存失败后图片应仍在 dirtyImages 中，得到 %v", a.dirtyImages)
	}
	if !strings.Contains(a.lastStatus, "保存失败") {
		t.Errorf("lastStatus = %q，期望含“保存失败”", a.lastStatus)
	}
}

func TestSaveWithoutDir(t *testing.T) {
	a := New()
	if err := a.save(); err != nil {
		t.Errorf("未打开目录时 save 应返回 nil，得到 %v", err)
	}
}

func TestUndoSetsCommandImageDirty(t *testing.T) {
	a := storeApp(t, "a.png", "b.png")
	// An edit on a.png, then the user switches to b.png: undoing must still
	// register a.png, whose annotations the command actually changed.
	undoDo(t, a, cmdCreate(a, 0, Ann{ID: "ann_1", Cat: 0, CX: 5, CY: 5, W: 10, H: 10}))
	a.dirtyImages = make(map[int64]bool)
	a.loaded = 1
	undoUndo(t, a)
	if id := a.images[0].ID; !a.dirtyImages[id] {
		t.Errorf("undo 后 dirtyImages = %v，期望含 a.png 的 id %d", a.dirtyImages, id)
	}
	if id := a.images[1].ID; a.dirtyImages[id] {
		t.Errorf("undo 后 dirtyImages = %v，不应含当前图片 b.png 的 id %d", a.dirtyImages, id)
	}
}

func TestExportJSON(t *testing.T) {
	a := storeApp(t, "a.png", "sub/b.png")
	seedAnns(t, a, 0, testAnns)
	a.categories = []Category{{Name: "x"}, {Name: "y"}}

	if err := a.exportJSON(a.dir); err != nil {
		t.Fatalf("exportJSON: %v", err)
	}
	target := filepath.Join(a.dir, docName)
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("读取导出文件: %v", err)
	}
	text := string(raw)
	// The PoC key "file" stays, and now carries the slash-separated rel path.
	for _, want := range []string{`"file": "a.png"`, `"file": "sub/b.png"`} {
		if !strings.Contains(text, want) {
			t.Errorf("导出文件缺少 %s：%s", want, text)
		}
	}
	if strings.Contains(text, a.dir) || strings.Contains(text, filepath.ToSlash(a.dir)) {
		t.Errorf("导出文件包含绝对路径：%s", text)
	}
	if strings.Contains(text, `"name"`) {
		t.Errorf("导出文件不应含运行时字段 name：%s", text)
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("临时文件仍存在: %v", err)
	}

	doc, err := loadDoc(a.dir)
	if err != nil || doc == nil {
		t.Fatalf("loadDoc: doc=%v err=%v", doc, err)
	}
	if doc.IRVersion != docIRVersion {
		t.Errorf("IRVersion = %q，期望 %q", doc.IRVersion, docIRVersion)
	}
	if !reflect.DeepEqual(doc.Categories, []string{"x", "y"}) {
		t.Errorf("Categories = %v，期望 [x y]", doc.Categories)
	}
	if len(doc.Images) != 2 {
		t.Fatalf("导出 %d 张图片，期望 2", len(doc.Images))
	}
	if !reflect.DeepEqual(doc.Images[0].Anns, testAnns) {
		t.Errorf("Anns = %+v\n期望 %+v", doc.Images[0].Anns, testAnns)
	}
	if doc.Images[0].Path != "" || doc.Images[0].Name != "" || doc.Images[0].ID != 0 {
		t.Errorf("运行时字段不应出现在导出文件里: %+v", doc.Images[0])
	}
}

func TestExportJSONWithoutDir(t *testing.T) {
	if err := New().exportJSON(""); err == nil {
		t.Error("未打开文件夹时 exportJSON 应返回错误")
	}
}

func TestLoadDocMissing(t *testing.T) {
	// Directory exists but holds no document.
	if doc, err := loadDoc(t.TempDir()); doc != nil || err != nil {
		t.Errorf("无文档目录: doc = %v, err = %v；期望 (nil, nil)", doc, err)
	}
	// Directory does not exist: reading the document reports "not exist".
	if doc, err := loadDoc(filepath.Join(t.TempDir(), "nope")); doc != nil || err != nil {
		t.Errorf("目录不存在: doc = %v, err = %v；期望 (nil, nil)", doc, err)
	}
	// No folder opened at all.
	if doc, err := loadDoc(""); doc != nil || err != nil {
		t.Errorf("空路径: doc = %v, err = %v；期望 (nil, nil)", doc, err)
	}
}

func TestLoadDocCorrupt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, docName)
	if err := os.WriteFile(p, []byte("\x00\xff{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := loadDoc(dir)
	if err == nil {
		t.Fatal("损坏的文档应返回错误")
	}
	if doc != nil {
		t.Errorf("损坏的文档应返回 nil，得到 %+v", doc)
	}
	if !strings.Contains(err.Error(), docName) {
		t.Errorf("错误信息应含文件名 %q，得到 %v", docName, err)
	}

	// Truncated JSON is corrupt too.
	if err := os.WriteFile(p, []byte(`{"ir_version": "0.1-poc"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDoc(dir); err == nil {
		t.Error("截断的 JSON 应返回错误")
	}
}

// TestMigrateLegacyDocMatching covers the legacy file-name matching rules of
// docs/phase1-design.md §5.2 step 5.
func TestMigrateLegacyDocMatching(t *testing.T) {
	const legacyAnns = `[{"id":"ann_1","cat":1,"cx":1,"cy":2,"w":3,"h":4,"angle":0.5}]`
	docWith := func(file string) string {
		return `{"ir_version":"0.1-poc","categories":["x","y"],"images":[{"file":"` + file +
			`","width":10,"height":10,"anns":` + legacyAnns + `}]}`
	}

	cases := []struct {
		name string
		rows []string // rel paths in the repository
		doc  string   // legacy document, "" = none
		// preinstalled annotations, written before the migration runs
		pre map[string]string
		// expected rel path -> annotation ids after the migration
		want map[string][]string
		// expected number of images the migration reports as migrated
		wantMigrated int
	}{
		{
			name:         "exact relative path",
			rows:         []string{"a.png"},
			doc:          docWith("a.png"),
			want:         map[string][]string{"a.png": {"ann_1"}},
			wantMigrated: 1,
		},
		{
			name:         "bare name matches the single row with that base name",
			rows:         []string{"sub/a.png"},
			doc:          docWith("a.png"),
			want:         map[string][]string{"sub/a.png": {"ann_1"}},
			wantMigrated: 1,
		},
		{
			name: "ambiguous base name is skipped",
			rows: []string{"x/a.png", "y/a.png"},
			doc:  docWith("a.png"),
			want: map[string][]string{"x/a.png": {}, "y/a.png": {}},
		},
		{
			name: "unknown file is skipped",
			rows: []string{"a.png"},
			doc:  docWith("gone.png"),
			want: map[string][]string{"a.png": {}},
		},
		{
			name:         "an annotated database is left alone",
			rows:         []string{"a.png"},
			doc:          docWith("a.png"),
			pre:          map[string]string{"a.png": "ann_0"},
			want:         map[string][]string{"a.png": {"ann_0"}},
			wantMigrated: 0,
		},
		{
			name: "no legacy document",
			rows: []string{"a.png"},
			want: map[string][]string{"a.png": {}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.doc != "" {
				if err := os.WriteFile(filepath.Join(dir, docName), []byte(tc.doc), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			st, err := store.Open(filepath.Join(dir, dbName))
			if err != nil {
				t.Fatalf("store.Open: %v", err)
			}
			defer func() { _ = st.Close() }()

			recs := make([]store.ImageRec, len(tc.rows))
			for i, rel := range tc.rows {
				recs[i] = store.ImageRec{RelPath: rel, Name: path.Base(rel), Width: 10, Height: 10, SHA256: "00"}
			}
			if err := st.UpsertImages(recs); err != nil {
				t.Fatalf("UpsertImages: %v", err)
			}
			loaded, err := st.LoadImages()
			if err != nil {
				t.Fatalf("LoadImages: %v", err)
			}
			for rel, id := range tc.pre {
				for _, rec := range loaded {
					if rec.RelPath == rel {
						ann := store.AnnRec{ID: id, Cat: 0, CX: 1, CY: 1, W: 2, H: 2}
						if err := st.ReplaceAnns(rec.ID, []store.AnnRec{ann}); err != nil {
							t.Fatalf("ReplaceAnns: %v", err)
						}
					}
				}
			}
			if loaded, err = st.LoadImages(); err != nil {
				t.Fatalf("LoadImages: %v", err)
			}

			migrated, err := migrateLegacyDoc(dir, st, loaded)
			if err != nil {
				t.Fatalf("migrateLegacyDoc: %v", err)
			}
			if migrated != tc.wantMigrated {
				t.Errorf("迁移 %d 张图片，期望 %d", migrated, tc.wantMigrated)
			}

			after, err := st.LoadImages()
			if err != nil {
				t.Fatalf("LoadImages: %v", err)
			}
			got := make(map[string][]string, len(after))
			for _, rec := range after {
				ids := []string{}
				for _, ann := range rec.Anns {
					ids = append(ids, ann.ID)
				}
				got[rec.RelPath] = ids
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("库中标注 = %v\n期望 %v", got, tc.want)
			}
		})
	}
}

// TestOpenMigratesLegacyJSON imports a PoC document once, keeps the file, and
// does not import it again on the next open.
func TestOpenMigratesLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "a.png"), 20, 10)
	legacy := `{"ir_version":"0.1-poc","categories":["x","y"],"images":[` +
		`{"file":"a.png","width":20,"height":10,"anns":[{"id":"ann_9","cat":1,"cx":5,"cy":6,"w":7,"h":8,"angle":0.25}]}]}`
	legacyPath := filepath.Join(dir, docName)
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	a := reopen(t, dir)
	if len(a.images) != 1 {
		t.Fatalf("载入 %d 张图片: %s", len(a.images), a.lastStatus)
	}
	want := []Ann{{ID: "ann_9", Cat: 1, Kind: KindRect, CX: 5, CY: 6, W: 7, H: 8, Angle: 0.25}}
	if !reflect.DeepEqual(a.images[0].Anns, want) {
		t.Errorf("迁移后标注 = %+v\n期望 %+v", a.images[0].Anns, want)
	}
	if !reflect.DeepEqual(a.categories, []Category{{Name: "x"}, {Name: "y"}}) {
		t.Errorf("迁移后类别 = %v，期望 [x y]", a.categories)
	}
	if !strings.Contains(a.lastStatus, "已迁移") {
		t.Errorf("状态栏 = %q，期望提示已迁移", a.lastStatus)
	}
	// The legacy document is a backup, not something to delete.
	if _, err := os.Stat(legacyPath); err != nil {
		t.Errorf("迁移不应删除旧 JSON: %v", err)
	}

	// The second open reads the store: the annotations are already there and
	// nothing is imported twice.
	b := reopen(t, dir)
	if !reflect.DeepEqual(b.images[0].Anns, want) {
		t.Errorf("第二次打开后标注 = %+v\n期望 %+v", b.images[0].Anns, want)
	}
	if strings.Contains(b.lastStatus, "已迁移") {
		t.Errorf("第二次打开又迁移了一次：%q", b.lastStatus)
	}
}
