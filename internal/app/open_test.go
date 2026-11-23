package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mylabel/internal/store"
)

// TestOpenDirRecursiveScan opens a folder with a nested image: the scan is
// recursive, the rel paths are slash-separated, and an annotation on the
// nested image survives save + reopen.
func TestOpenDirRecursiveScan(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "a.png"), 20, 10)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(dir, "sub", "b.png"), 30, 20)

	a := New()
	t.Cleanup(a.Close)
	a.openDir(dir)

	want := []struct {
		rel, name string
		w, h      int
	}{{"a.png", "a.png", 20, 10}, {"sub/b.png", "b.png", 30, 20}}
	if len(a.images) != len(want) {
		t.Fatalf("载入 %d 张图片，期望 %d：%s", len(a.images), len(want), a.lastStatus)
	}
	for i, w := range want {
		img := a.images[i]
		if img.RelPath != w.rel || img.Name != w.name {
			t.Errorf("images[%d] RelPath/Name = %q/%q，期望 %q/%q", i, img.RelPath, img.Name, w.rel, w.name)
		}
		if img.Width != w.w || img.Height != w.h {
			t.Errorf("images[%d] 尺寸 = %dx%d，期望 %dx%d", i, img.Width, img.Height, w.w, w.h)
		}
		if wantPath := filepath.Join(dir, filepath.FromSlash(w.rel)); img.Path != wantPath {
			t.Errorf("images[%d].Path = %q，期望 %q", i, img.Path, wantPath)
		}
		if img.ID == 0 {
			t.Errorf("images[%d].ID = 0，应由库回填", i)
		}
		if img.Status != store.StatusNew {
			t.Errorf("images[%d].Status = %q，期望 %q", i, img.Status, store.StatusNew)
		}
	}

	// The working database lives next to the images and is not an image itself.
	if _, err := os.Stat(filepath.Join(dir, dbName)); err != nil {
		t.Errorf("缺少工作库 %s: %v", dbName, err)
	}

	// A nested image round-trips through the store like a top-level one.
	a.images[1].Anns = testAnns
	a.setDirty(1)
	if err := a.save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	b := reopen(t, dir)
	if len(b.images) != 2 {
		t.Fatalf("重开后载入 %d 张图片", len(b.images))
	}
	if !reflect.DeepEqual(b.images[1].Anns, testAnns) {
		t.Errorf("sub/b.png 的标注 = %+v\n期望 %+v", b.images[1].Anns, testAnns)
	}
	if len(b.images[0].Anns) != 0 {
		t.Errorf("a.png 不该有标注，得到 %+v", b.images[0].Anns)
	}
}

// TestOpenDirDropsMissingImages: a file deleted between two opens loses its
// row (and its annotations) from the store.
func TestOpenDirDropsMissingImages(t *testing.T) {
	dir := t.TempDir()
	aFile := filepath.Join(dir, "a.png")
	bFile := filepath.Join(dir, "b.png")
	writePNG(t, aFile, 20, 10)
	writePNG(t, bFile, 20, 10)

	a := New()
	t.Cleanup(a.Close)
	a.openDir(dir)
	if len(a.images) != 2 {
		t.Fatalf("载入 %d 张图片，期望 2", len(a.images))
	}
	seedAnns(t, a, 1, testAnns) // b.png
	if err := os.Remove(bFile); err != nil {
		t.Fatal(err)
	}

	b := reopen(t, dir)
	if len(b.images) != 1 || b.images[0].RelPath != "a.png" {
		t.Fatalf("重开后 images = %+v，期望只剩 a.png", b.images)
	}
	recs, err := b.store.LoadImages()
	if err != nil {
		t.Fatalf("LoadImages: %v", err)
	}
	if len(recs) != 1 || recs[0].RelPath != "a.png" {
		t.Errorf("库中记录 = %+v，期望只剩 a.png", recs)
	}
}

// TestOpenDirScanFailure: an unreadable folder reports it and opens nothing.
func TestOpenDirScanFailure(t *testing.T) {
	a := New()
	t.Cleanup(a.Close)
	a.openDir(filepath.Join(t.TempDir(), "does-not-exist"))
	if !strings.Contains(a.lastStatus, "扫描失败") {
		t.Errorf("lastStatus = %q，期望含“扫描失败”", a.lastStatus)
	}
	if a.dir != "" || len(a.images) != 0 || a.store != nil {
		t.Errorf("失败的打开改变了状态：dir=%q images=%d store=%v", a.dir, len(a.images), a.store)
	}
}

func TestOpenDirStatus(t *testing.T) {
	plain := openDirStatus("/imgs", 3, 0, 0)
	if want := "已打开 /imgs（3 张图片）"; plain != want {
		t.Errorf("状态 = %q，期望 %q", plain, want)
	}
	if strings.Contains(plain, "已转正") || strings.Contains(plain, "已迁移") {
		t.Errorf("没有转正/迁移时不应出现对应提示：%q", plain)
	}
	if got := openDirStatus("/imgs", 3, 2, 0); !strings.Contains(got, "已转正 2 张") {
		t.Errorf("状态 = %q，期望含“已转正 2 张”", got)
	}
	if got := openDirStatus("/imgs", 3, 0, 4); !strings.Contains(got, "已迁移旧 JSON 标注 4 张") {
		t.Errorf("状态 = %q，期望含迁移提示", got)
	}
	if got := openDirStatus("/imgs", 3, 1, 2); !strings.Contains(got, "已转正 1 张") || !strings.Contains(got, "已迁移旧 JSON 标注 2 张") {
		t.Errorf("状态 = %q，期望同时含转正与迁移提示", got)
	}
}
