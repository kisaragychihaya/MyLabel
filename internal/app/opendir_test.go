package app

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// mkdirs creates every path of dirs (relative to root) as a directory tree.
func mkdirs(t *testing.T, root string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
}

// TestResolveOpenDir is the table for widening an Ultralytics dataset
// subfolder to its root: only an "images" directory (its own name or the name
// of its parent) with a sibling "labels" directory qualifies.
func TestResolveOpenDir(t *testing.T) {
	cases := []struct {
		name  string
		dirs  []string // created under the case root
		files []string // created as empty files under the case root
		in    string   // path relative to the case root, passed to resolveOpenDir
		want  string   // path relative to the case root, "" = same as in
	}{
		{
			name: "train under images expands to root",
			dirs: []string{"images/train", "labels"},
			in:   "images/train",
			want: ".",
		},
		{
			name: "images itself expands to root",
			dirs: []string{"images", "labels"},
			in:   "images",
			want: ".",
		},
		{
			name: "no labels leaves the split alone",
			dirs: []string{"images/train"},
			in:   "images/train",
		},
		{
			name: "no labels leaves images alone",
			dirs: []string{"images"},
			in:   "images",
		},
		{
			name:  "labels as a file does not count",
			dirs:  []string{"images/train"},
			files: []string{"labels"},
			in:    "images/train",
		},
		{
			name: "ordinary folder is untouched",
			dirs: []string{"photos", "labels"},
			in:   "photos",
		},
		{
			name: "deeper nesting is not widened twice",
			dirs: []string{"images/train/batch1", "labels"},
			in:   "images/train/batch1",
		},
		{
			name: "images and labels mixed case",
			dirs: []string{"IMAGEs/train", "LABELS"},
			in:   "IMAGEs/train",
			want: ".",
		},
		{
			name: "images mixed case, labels ordinary",
			dirs: []string{"Images", "labels"},
			in:   "Images",
			want: ".",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			mkdirs(t, root, c.dirs...)
			for _, f := range c.files {
				if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(f)), nil, 0o644); err != nil {
					t.Fatalf("write %s: %v", f, err)
				}
			}
			got := resolveOpenDir(filepath.Join(root, filepath.FromSlash(c.in)))
			want := filepath.Join(root, filepath.FromSlash(c.in))
			if c.want != "" {
				want = filepath.Join(root, filepath.FromSlash(c.want))
			}
			if got != want {
				t.Errorf("resolveOpenDir(%q) = %q，期望 %q", c.in, got, want)
			}
		})
	}
}

// TestResolveOpenDirMissingPath: a path that is not an existing directory is
// returned verbatim, even when it looks like a dataset child.
func TestResolveOpenDirMissingPath(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "images", "train")
	if got := resolveOpenDir(missing); got != missing {
		t.Errorf("resolveOpenDir(%q) = %q，期望原样返回", missing, got)
	}
}

// TestOpenDirExpandsYoloRoot opens the train split of an Ultralytics dataset:
// the workspace becomes the dataset root, so train and val images are both
// loaded and the store sits next to the splits.
func TestOpenDirExpandsYoloRoot(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "images/train", "images/val", "labels")
	writePNG(t, filepath.Join(root, "images", "train", "a.png"), 20, 10)
	writePNG(t, filepath.Join(root, "images", "val", "b.png"), 30, 20)

	a := New()
	t.Cleanup(a.Close)
	a.openDir(filepath.Join(root, "images", "train"))

	if a.dir != root {
		t.Fatalf("a.dir = %q，期望数据集根 %q：%s", a.dir, root, a.lastStatus)
	}
	var rels []string
	for _, img := range a.images {
		rels = append(rels, img.RelPath)
	}
	sort.Strings(rels)
	want := []string{"images/train/a.png", "images/val/b.png"}
	if strings.Join(rels, ",") != strings.Join(want, ",") {
		t.Errorf("载入图片 = %v，期望 %v：%s", rels, want, a.lastStatus)
	}
	if !strings.Contains(a.lastStatus, "检测到 YOLO 数据集布局，已改为打开数据集根") {
		t.Errorf("lastStatus = %q，期望含数据集布局提示", a.lastStatus)
	}
	if !strings.Contains(a.lastStatus, "2 张图片") {
		t.Errorf("lastStatus = %q，期望与打开结果文案拼接", a.lastStatus)
	}
	if _, err := os.Stat(filepath.Join(root, dbName)); err != nil {
		t.Errorf("工作库应建在数据集根 %s: %v", root, err)
	}
}

// TestOpenDirKeepsSplitWithoutLabels is the control: without a labels
// directory the opened folder is the workspace, untouched.
func TestOpenDirKeepsSplitWithoutLabels(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "images/train", "images/val")
	writePNG(t, filepath.Join(root, "images", "train", "a.png"), 20, 10)
	writePNG(t, filepath.Join(root, "images", "val", "b.png"), 20, 10)

	split := filepath.Join(root, "images", "train")
	a := New()
	t.Cleanup(a.Close)
	a.openDir(split)

	if a.dir != split {
		t.Fatalf("a.dir = %q，期望 %q", a.dir, split)
	}
	if len(a.images) != 1 || a.images[0].RelPath != "a.png" {
		t.Errorf("images = %+v，期望只有 train/a.png", a.images)
	}
	if strings.Contains(a.lastStatus, "检测到 YOLO 数据集布局") {
		t.Errorf("lastStatus = %q，不应出现数据集布局提示", a.lastStatus)
	}
}
