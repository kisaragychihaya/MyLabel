package exporter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mylabel/internal/ir"
)

// testPlan builds a minimal plan with one copy and one file.
func testPlan() *Plan {
	return &Plan{
		Opts: Options{Format: FormatYOLO, Task: TaskDet, Split: SplitPolicy{Mode: SplitPreserve}},
		Categories: []ir.Category{
			{Key: "cat", Index: 0, Name: "cat"},
		},
		Files: []OutFile{
			{Rel: "data.yaml", Data: []byte("names:\n  - cat\n")},
		},
		Copies: []ImageCopy{
			{Src: "a.jpg", Dst: "images/train/a.jpg"},
		},
		Report: Report{Images: 1},
	}
}

func TestWriteExportAssemblesAtomically(t *testing.T) {
	root := t.TempDir()
	imgRoot := filepath.Join(root, "imgs")
	if err := os.MkdirAll(imgRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imgRoot, "a.jpg"), []byte("jpeg-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "out")

	var lastDone, lastTotal int
	plan := testPlan()
	if err := WriteExport(context.Background(), out, imgRoot, plan, func(done, total int) { lastDone, lastTotal = done, total }); err != nil {
		t.Fatalf("WriteExport: %v", err)
	}
	if want := len(plan.Copies) + len(plan.Files) + 1; lastDone != want || lastTotal != want {
		t.Fatalf("progress = %d/%d, want %d/%d", lastDone, lastTotal, want, want)
	}
	data, err := os.ReadFile(filepath.Join(out, "images", "train", "a.jpg"))
	if err != nil || string(data) != "jpeg-bytes" {
		t.Fatalf("copied image = %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(out, "data.yaml")); err != nil || !strings.Contains(string(data), "cat") {
		t.Fatalf("data.yaml = %q, %v", data, err)
	}
	manifest, err := os.ReadFile(filepath.Join(out, manifestName))
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	var doc manifestDoc
	if err := json.Unmarshal(manifest, &doc); err != nil {
		t.Fatalf("manifest not JSON: %v", err)
	}
	if doc.Tool != "mylabel" || doc.Format != FormatYOLO || doc.Task != TaskDet || doc.CoordinateBasis == "" {
		t.Fatalf("manifest fields = %+v", doc)
	}
	if _, err := os.Stat(out + tmpSuffix); !os.IsNotExist(err) {
		t.Fatalf("tmp dir should be gone, stat err = %v", err)
	}
}

func TestWriteExportRefusesExistingDir(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteExport(context.Background(), out, root, testPlan(), nil); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("err = %v, want 已存在", err)
	}
}

func TestWriteExportCleansTmpOnError(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "out")
	// imgRoot has no a.jpg: the copy fails and the tmp dir must be removed.
	err := WriteExport(context.Background(), out, root, testPlan(), nil)
	if err == nil {
		t.Fatal("want copy failure")
	}
	if _, statErr := os.Stat(out + tmpSuffix); !os.IsNotExist(statErr) {
		t.Fatalf("tmp dir should be cleaned, stat err = %v", statErr)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("out dir should not exist, stat err = %v", statErr)
	}
}

// TestWriteExportCancelsCleansUpAndWraps: cancelling mid-export removes the
// temporary directory, leaves no output, and reports errors.Is context.Canceled
// (docs/phase5-design.md §5.1).
func TestWriteExportCancelsCleansUpAndWraps(t *testing.T) {
	root := t.TempDir()
	imgRoot := filepath.Join(root, "imgs")
	if err := os.MkdirAll(imgRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imgRoot, "a.jpg"), []byte("jpeg-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "out")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel after the first tick (the image copy), so the files loop sees it.
	err := WriteExport(ctx, out, imgRoot, testPlan(), func(done, total int) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, 期望 errors.Is(err, context.Canceled)", err)
	}
	if !strings.Contains(err.Error(), "exporter:") {
		t.Fatalf("err = %q, 期望带 exporter: 前缀", err)
	}
	if _, statErr := os.Stat(out + tmpSuffix); !os.IsNotExist(statErr) {
		t.Fatalf("tmp dir should be cleaned, stat err = %v", statErr)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("out dir should not exist, stat err = %v", statErr)
	}
}

// TestWriteExportPreCanceledWritesNothing: a ctx canceled before the call fails
// on the first iteration and still cleans up.
func TestWriteExportPreCanceledWritesNothing(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "out")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := WriteExport(ctx, out, root, testPlan(), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, 期望 context.Canceled", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("out dir should not exist, stat err = %v", statErr)
	}
}

// TestWriteExportNilContext: a nil ctx is treated as context.Background().
func TestWriteExportNilContext(t *testing.T) {
	root := t.TempDir()
	imgRoot := filepath.Join(root, "imgs")
	if err := os.MkdirAll(imgRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imgRoot, "a.jpg"), []byte("jpeg-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "out")
	if err := WriteExport(nil, out, imgRoot, testPlan(), nil); err != nil {
		t.Fatalf("WriteExport(nil ctx): %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, manifestName)); err != nil {
		t.Fatalf("manifest: %v", err)
	}
}

func TestCleanRel(t *testing.T) {
	for _, bad := range []string{"", "..", "../x", "/abs", "c:/win", "a/../../b"} {
		if _, err := cleanRel(bad); err == nil {
			t.Fatalf("cleanRel(%q) should fail", bad)
		}
	}
	for _, good := range []string{"a.jpg", "sub/dir/a.jpg", "a/../b.jpg"} {
		if _, err := cleanRel(good); err != nil {
			t.Fatalf("cleanRel(%q): %v", good, err)
		}
	}
}

func TestCheckPlanPathsDetectsCaseInsensitiveDuplicates(t *testing.T) {
	p := &Plan{Files: []OutFile{{Rel: "A.txt"}, {Rel: "a.TXT"}}}
	if err := checkPlanPaths(p); err == nil || !strings.Contains(err.Error(), "冲突") {
		t.Fatalf("err = %v, want 冲突", err)
	}
	p = &Plan{Copies: []ImageCopy{{Src: "a.jpg", Dst: "images/train/a.jpg"}}}
	if err := checkPlanPaths(p); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildPlanValidation(t *testing.T) {
	proj := &ir.Project{Categories: []ir.Category{{Key: "c", Name: "c"}}}
	imgs := []ir.Image{{File: "a.jpg", Width: 10, Height: 10}}
	if _, err := BuildPlan(nil, imgs, Options{Format: FormatYOLO, Task: TaskDet}); err == nil {
		t.Fatal("nil project should fail")
	}
	if _, err := BuildPlan(proj, imgs, Options{Format: FormatYOLO}); err == nil {
		t.Fatal("missing task should fail")
	}
	if _, err := BuildPlan(proj, imgs, Options{Format: "png"}); err == nil {
		t.Fatal("unknown format should fail")
	}
	if _, err := BuildPlan(proj, imgs, Options{Format: FormatYOLO, Task: TaskDet, Split: SplitPolicy{Mode: SplitRandom, TrainRatio: 1.5}}); err == nil {
		t.Fatal("bad ratio should fail")
	}
	if _, err := BuildPlan(proj, nil, Options{Format: FormatYOLO, Task: TaskDet}); err == nil {
		t.Fatal("no images should fail")
	}
	dup := []ir.Image{{File: "a.jpg"}, {File: "a.jpg"}}
	if _, err := BuildPlan(proj, dup, Options{Format: FormatYOLO, Task: TaskDet}); err == nil {
		t.Fatal("duplicate files should fail")
	}
}
