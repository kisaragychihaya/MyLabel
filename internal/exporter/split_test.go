package exporter

import (
	"slices"
	"strings"
	"testing"

	"mylabel/internal/ir"
)

// TestAssignSplitsOverridesPreserve: Overrides wins over a detected split in
// preserve mode (docs/phase5-design.md §7.1).
func TestAssignSplitsOverridesPreserve(t *testing.T) {
	images := []ir.Image{{File: "a.jpg"}, {File: "val/b.jpg"}}
	splits, err := assignSplits(images, SplitPolicy{Overrides: map[string]string{
		"a.jpg":     "val",
		"val/b.jpg": "train",
	}})
	if err != nil {
		t.Fatalf("assignSplits: %v", err)
	}
	if splits["a.jpg"] != "val" || splits["val/b.jpg"] != "train" {
		t.Fatalf("splits = %v, 期望被 Overrides 覆盖", splits)
	}
}

// TestAssignSplitsOverridesRandom: Overrides also applies after a random split,
// and the untouched images keep the random assignment.
func TestAssignSplitsOverridesRandom(t *testing.T) {
	images := make([]ir.Image, 10)
	for i := range images {
		images[i] = ir.Image{File: string(rune('a'+i)) + ".jpg"}
	}
	pol := SplitPolicy{Mode: SplitRandom, TrainRatio: 0.5, Seed: 7}
	base, err := assignSplits(images, pol)
	if err != nil {
		t.Fatalf("assignSplits: %v", err)
	}
	pol.Overrides = map[string]string{"a.jpg": "val", "b.jpg": "train"}
	got, err := assignSplits(images, pol)
	if err != nil {
		t.Fatalf("assignSplits with overrides: %v", err)
	}
	if got["a.jpg"] != "val" || got["b.jpg"] != "train" {
		t.Fatalf("splits = %v, 期望 a.jpg=val b.jpg=train", got)
	}
	for _, img := range images[2:] {
		if got[img.File] != base[img.File] {
			t.Fatalf("%s = %q, 随机结果应保持 %q", img.File, got[img.File], base[img.File])
		}
	}
}

func TestAssignSplitsRejectsBadOverrides(t *testing.T) {
	images := []ir.Image{{File: "a.jpg"}}
	for _, tc := range []struct {
		name      string
		overrides map[string]string
		want      []string
	}{
		{"未知图片", map[string]string{"nope.jpg": "val"}, []string{"nope.jpg", "不在待导出图片"}},
		{"非法值", map[string]string{"a.jpg": "test"}, []string{"a.jpg", "train 或 val", "test"}},
		{"空值", map[string]string{"a.jpg": ""}, []string{"a.jpg", "train 或 val"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := assignSplits(images, SplitPolicy{Overrides: tc.overrides})
			if err == nil {
				t.Fatal("期望报错")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %q, 应包含 %q", err, want)
				}
			}
		})
	}
}

// TestBuildPlanOverridesRejected: a bad override fails BuildPlan for every
// format, before anything is written.
func TestBuildPlanOverridesRejected(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{File: "a.jpg", Width: 10, Height: 10}}
	for _, format := range []Format{FormatYOLO, FormatCOCO, FormatVOC} {
		t.Run(string(format), func(t *testing.T) {
			_, err := BuildPlan(proj, images, Options{
				Format: format, Task: TaskDet,
				Split: SplitPolicy{Overrides: map[string]string{"zzz.jpg": "val"}},
			})
			if err == nil || !strings.Contains(err.Error(), "zzz.jpg") {
				t.Fatalf("err = %v, 期望指明未知图片", err)
			}
			if _, err := BuildPlan(proj, images, Options{
				Format: format, Task: TaskDet,
				Split: SplitPolicy{Overrides: map[string]string{"a.jpg": "holdout"}},
			}); err == nil || !strings.Contains(err.Error(), "holdout") {
				t.Fatalf("err = %v, 期望指明非法值", err)
			}
		})
	}
}

// TestBuildPlanOverridesAffectsEveryFormat: the override reaches the three
// builders (YOLO copy dirs, COCO per-split files, VOC ImageSets).
func TestBuildPlanOverridesAffectsEveryFormat(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{
		{File: "a.jpg", Width: 10, Height: 10, Annotations: []ir.Annotation{
			{ID: "1", Category: "cat", Kind: ir.KindBBox, X: 1, Y: 1, W: 5, H: 5},
		}},
		{File: "val/b.jpg", Width: 10, Height: 10},
	}
	opts := Options{
		Split: SplitPolicy{Overrides: map[string]string{"a.jpg": "val", "val/b.jpg": "train"}},
	}

	t.Run("yolo", func(t *testing.T) {
		o := opts
		o.Format, o.Task = FormatYOLO, TaskDet
		plan, err := BuildPlan(proj, images, o)
		if err != nil {
			t.Fatalf("BuildPlan: %v", err)
		}
		want := []string{"images/val/a.jpg", "images/train/b.jpg"}
		if got := expCopyDsts(plan); !slices.Equal(got, want) {
			t.Fatalf("Copies = %v, 期望 %v", got, want)
		}
		if !expHasFile(plan, "labels/val/a.txt") {
			t.Fatalf("Files = %+v, 期望 labels/val/a.txt", plan.Files)
		}
	})

	t.Run("coco", func(t *testing.T) {
		o := opts
		o.Format, o.Task = FormatCOCO, ""
		plan, err := BuildPlan(proj, images, o)
		if err != nil {
			t.Fatalf("BuildPlan: %v", err)
		}
		valDoc := string(expPlanFile(t, plan, "annotations/instances_val.json"))
		trainDoc := string(expPlanFile(t, plan, "annotations/instances_train.json"))
		if !strings.Contains(valDoc, `"a.jpg"`) || !strings.Contains(trainDoc, `"b.jpg"`) {
			t.Fatalf("COCO per-split 内容未按 Overrides 划分:\nval=%s\ntrain=%s", valDoc, trainDoc)
		}
	})

	t.Run("voc", func(t *testing.T) {
		o := opts
		o.Format, o.Task = FormatVOC, ""
		plan, err := BuildPlan(proj, images, o)
		if err != nil {
			t.Fatalf("BuildPlan: %v", err)
		}
		if got := string(expPlanFile(t, plan, "ImageSets/Main/train.txt")); got != "b\n" {
			t.Fatalf("train.txt = %q, 期望 %q", got, "b\n")
		}
		if got := string(expPlanFile(t, plan, "ImageSets/Main/val.txt")); got != "a\n" {
			t.Fatalf("val.txt = %q, 期望 %q", got, "a\n")
		}
	})

	if plan, err := BuildPlan(proj, images, Options{
		Format: FormatYOLO, Task: TaskDet,
		Split: SplitPolicy{Overrides: map[string]string{"a.jpg": "val"}},
	}); err != nil {
		t.Fatalf("BuildPlan: %v", err)
	} else if plan.Splits["a.jpg"] != "val" || plan.Splits["val/b.jpg"] != "val" {
		t.Fatalf("plan.Splits = %v", plan.Splits)
	}
}
