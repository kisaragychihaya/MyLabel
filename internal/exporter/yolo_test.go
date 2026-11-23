package exporter

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mylabel/internal/ir"
)

// --- plan-inspection helpers shared with voc_test.go ---

// expTestProject builds a project with one category per given name.
func expTestProject(names ...string) *ir.Project {
	cats := make([]ir.Category, len(names))
	for i, n := range names {
		cats[i] = ir.Category{Key: n, Name: n}
	}
	return &ir.Project{IRVersion: ir.Version, Task: ir.TaskMixed, Categories: cats}
}

// expPlanFile returns the planned contents of rel, failing when it is absent.
func expPlanFile(t *testing.T, plan *Plan, rel string) []byte {
	t.Helper()
	for _, f := range plan.Files {
		if f.Rel == rel {
			return f.Data
		}
	}
	rels := make([]string, len(plan.Files))
	for i, f := range plan.Files {
		rels[i] = f.Rel
	}
	t.Fatalf("计划里没有 %s（有 %v）", rel, rels)
	return nil
}

// expHasFile reports whether the plan writes rel.
func expHasFile(plan *Plan, rel string) bool {
	for _, f := range plan.Files {
		if f.Rel == rel {
			return true
		}
	}
	return false
}

// expLossyCount returns how often rule fired.
func expLossyCount(plan *Plan, rule string) int {
	for _, st := range plan.Report.Lossy {
		if st.Rule == rule {
			return st.Count
		}
	}
	return 0
}

// expWarned reports whether some warning contains substr.
func expWarned(plan *Plan, substr string) bool {
	for _, w := range plan.Report.Warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// expCopyDsts lists the planned image destinations in plan order.
func expCopyDsts(plan *Plan) []string {
	out := make([]string, len(plan.Copies))
	for i, c := range plan.Copies {
		out[i] = c.Dst
	}
	return out
}

func TestBuildYOLOPlanDetGoldenBytes(t *testing.T) {
	proj := expTestProject("cat", "dog")
	images := []ir.Image{{
		File: "a.jpg", Width: 640, Height: 480,
		Annotations: []ir.Annotation{
			{ID: "1", Category: "cat", Kind: ir.KindBBox, X: 64, Y: 48, W: 320, H: 240},
			{ID: "2", Category: "dog", Kind: ir.KindBBox, W: 640, H: 480},
		},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatYOLO, Task: TaskDet})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	const wantLabels = "0 0.35 0.35 0.5 0.5\n1 0.5 0.5 1 1\n"
	if got := string(expPlanFile(t, plan, "labels/train/a.txt")); got != wantLabels {
		t.Fatalf("labels/train/a.txt = %q, 期望 %q", got, wantLabels)
	}
	const wantYAML = "train: images/train\nnames:\n  - cat\n  - dog\n"
	if got := string(expPlanFile(t, plan, "data.yaml")); got != wantYAML {
		t.Fatalf("data.yaml = %q, 期望 %q", got, wantYAML)
	}
	if dsts := expCopyDsts(plan); !slices.Equal(dsts, []string{"images/train/a.jpg"}) {
		t.Fatalf("Copies = %v", dsts)
	}
	if plan.Report.Images != 1 || plan.Report.ImagesWithAnns != 1 ||
		plan.Report.Annotations != 2 || plan.Report.AnnotationsDropped != 0 {
		t.Fatalf("Report = %+v", plan.Report)
	}
	if len(plan.Categories) != 2 || plan.Categories[0].Index != 0 || plan.Categories[1].Index != 1 {
		t.Fatalf("Categories = %+v", plan.Categories)
	}
}

func TestBuildYOLOPlanSegGoldenBytes(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "seg/a.png", Width: 640, Height: 480,
		Annotations: []ir.Annotation{{
			ID: "1", Category: "cat", Kind: ir.KindPolygon,
			Rings: [][][2]float64{{{64, 48}, {320, 48}, {320, 240}}},
		}},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatYOLO, Task: TaskSeg})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	const want = "0 0.1 0.1 0.5 0.1 0.5 0.5\n"
	if got := string(expPlanFile(t, plan, "labels/train/seg/a.txt")); got != want {
		t.Fatalf("labels = %q, 期望 %q", got, want)
	}
	if dsts := expCopyDsts(plan); !slices.Equal(dsts, []string{"images/train/seg/a.png"}) {
		t.Fatalf("Copies = %v", dsts)
	}
	if n := expLossyCount(plan, RuleDetToSeg) + expLossyCount(plan, RuleMultiRing); n != 0 {
		t.Fatalf("单环 seg 不应记损失, 得到 %d", n)
	}
}

func TestBuildYOLOPlanObbGoldenBytes(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "r.jpg", Width: 640, Height: 480,
		Annotations: []ir.Annotation{{
			ID: "1", Category: "cat", Kind: ir.KindRotated,
			CX: 320, CY: 240, RW: 200, RH: 100,
		}},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatYOLO, Task: TaskObb})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	const want = "0 0.34375 0.395833 0.65625 0.395833 0.65625 0.604167 0.34375 0.604167\n"
	if got := string(expPlanFile(t, plan, "labels/train/r.txt")); got != want {
		t.Fatalf("labels = %q, 期望 %q", got, want)
	}
	if n := expLossyCount(plan, RuleDetToObb) + expLossyCount(plan, RuleSegToObb); n != 0 {
		t.Fatalf("旋转框直写不应记损失, 得到 %d", n)
	}
}

func TestBuildYOLOPlanConversionMatrix(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "m.jpg", Width: 100, Height: 100,
		Annotations: []ir.Annotation{
			{ID: "r", Category: "cat", Kind: ir.KindRotated,
				Points: [][2]float64{{10, 10}, {50, 10}, {50, 30}, {10, 30}}},
			{ID: "p", Category: "cat", Kind: ir.KindPolygon,
				Rings: [][][2]float64{
					{{10, 60}, {60, 60}, {60, 90}, {10, 90}},
					{{20, 65}, {40, 65}, {30, 80}},
				}},
			{ID: "b", Category: "cat", Kind: ir.KindBBox, X: 70, Y: 70, W: 20, H: 20},
		},
	}}

	for _, tc := range []struct {
		task  Task
		rules []string
	}{
		{TaskDet, []string{RuleObbToDet, RuleSegToDet}},
		{TaskSeg, []string{RuleObbToSeg, RuleDetToSeg, RuleMultiRing}},
		{TaskObb, []string{RuleDetToObb, RuleSegToObb}},
	} {
		t.Run(string(tc.task), func(t *testing.T) {
			plan, err := BuildPlan(proj, images, Options{Format: FormatYOLO, Task: tc.task})
			if err != nil {
				t.Fatalf("BuildPlan: %v", err)
			}
			if plan.Report.Annotations != 3 || plan.Report.AnnotationsDropped != 0 {
				t.Fatalf("Report = %+v, 期望 3 条全部写出", plan.Report)
			}
			for _, rule := range tc.rules {
				if n := expLossyCount(plan, rule); n != 1 {
					t.Fatalf("%s 计数 = %d, 期望 1（Report = %+v）", rule, n, plan.Report.Lossy)
				}
			}
			if got := string(expPlanFile(t, plan, "labels/train/m.txt")); strings.Count(got, "\n") != 3 {
				t.Fatalf("标签应为 3 行, 得到 %q", got)
			}
		})
	}
}

func TestBuildYOLOPlanDropsUnknownCategoryAndEmptyGeometry(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "d.jpg", Width: 100, Height: 100,
		Annotations: []ir.Annotation{
			{ID: "u", Category: "nope", Kind: ir.KindBBox, W: 10, H: 10},
			{ID: "o", Category: "cat", Kind: ir.KindBBox, X: 200, Y: 0, W: 10, H: 10},
			{ID: "z", Category: "cat", Kind: ir.KindBBox, X: 10, Y: 10},
			{ID: "e", Category: "cat", Kind: ir.KindPolygon},
		},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatYOLO, Task: TaskDet})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Report.Annotations != 0 || plan.Report.AnnotationsDropped != 4 {
		t.Fatalf("Report = %+v, 期望 4 条丢弃", plan.Report)
	}
	if plan.Report.ImagesWithAnns != 0 {
		t.Fatalf("ImagesWithAnns = %d, 期望 0", plan.Report.ImagesWithAnns)
	}
	if expHasFile(plan, "labels/train/d.txt") {
		t.Fatal("没有可写标注时不应生成 txt")
	}
	if dsts := expCopyDsts(plan); !slices.Equal(dsts, []string{"images/train/d.jpg"}) {
		t.Fatalf("无标注图片仍需拷贝, Copies = %v", dsts)
	}
	if n := expLossyCount(plan, RuleDropped); n != 4 {
		t.Fatalf("%s 计数 = %d, 期望 4", RuleDropped, n)
	}
	if len(plan.Report.Lossy[0].Samples) == 0 {
		t.Fatal("丢弃样本应带定位信息")
	}
}

func TestBuildYOLOPlanClipsOutOfBounds(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "c.jpg", Width: 100, Height: 100,
		Annotations: []ir.Annotation{
			{ID: "1", Category: "cat", Kind: ir.KindBBox, X: -10, Y: -10, W: 50, H: 50},
			{ID: "2", Category: "cat", Kind: ir.KindPolygon,
				Rings: [][][2]float64{{{120, 120}, {150, 120}, {150, 150}}}},
		},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatYOLO, Task: TaskDet})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	// The polygon collapses onto the corner and is dropped; the box survives.
	if plan.Report.Annotations != 1 || plan.Report.AnnotationsDropped != 1 {
		t.Fatalf("Report = %+v", plan.Report)
	}
	const want = "0 0.2 0.2 0.4 0.4\n"
	if got := string(expPlanFile(t, plan, "labels/train/c.txt")); got != want {
		t.Fatalf("labels = %q, 期望 %q", got, want)
	}
	if n := expLossyCount(plan, RuleClipped); n != 1 {
		t.Fatalf("%s 计数 = %d, 期望 1（丢弃的标注不重复计裁剪）", RuleClipped, n)
	}
	if n := expLossyCount(plan, RuleDropped); n != 1 {
		t.Fatalf("%s 计数 = %d, 期望 1", RuleDropped, n)
	}
}

func TestFormatNorm(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{0.5, "0.5"},
		{0, "0"},
		{negZero, "0"},
		{-1, "0"},
		{1, "1"},
		{1.5, "1"},
		{1.0 / 3, "0.333333"},
		{2.0 / 3, "0.666667"},
		{0.1234567, "0.123457"},
		{0.1234564, "0.123456"},
		{0.1, "0.1"},
		{0.34375, "0.34375"},
	} {
		if got := formatNorm(tc.in); got != tc.want {
			t.Fatalf("formatNorm(%v) = %q, 期望 %q", tc.in, got, tc.want)
		}
	}
}

// negZero is a negative zero, which must never render as "-0".
var negZero = math.Copysign(0, -1)

func TestYAMLScalarEscaping(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"cat", "cat"},
		{"a b", "a b"},
		{"cat:1", "'cat:1'"},
		{"it's", "'it''s'"},
		{"a#b", "'a#b'"},
		{" padded ", "' padded '"},
		{"", "''"},
		{"123", "'123'"},
		{"1.5", "'1.5'"},
		{"true", "'true'"},
		{"No", "'No'"},
		{"null", "'null'"},
		{"- x", "'- x'"},
		{"[x]", "'[x]'"},
		{"50%", "'50%'"},
	} {
		if got := yamlScalar(tc.in); got != tc.want {
			t.Fatalf("yamlScalar(%q) = %q, 期望 %q", tc.in, got, tc.want)
		}
	}
}

func TestYOLODataYAMLOrderAndVal(t *testing.T) {
	cats, _ := planCategories([]ir.Category{{Key: "b", Name: "b"}, {Key: "a", Name: "a"}})
	if got, want := string(yoloDataYAML(cats, false)), "train: images/train\nnames:\n  - b\n  - a\n"; got != want {
		t.Fatalf("无 val = %q, 期望 %q", got, want)
	}
	if got, want := string(yoloDataYAML(cats, true)), "train: images/train\nval: images/val\nnames:\n  - b\n  - a\n"; got != want {
		t.Fatalf("有 val = %q, 期望 %q", got, want)
	}
	if got, want := string(yoloDataYAML(nil, false)), "train: images/train\nnames: []\n"; got != want {
		t.Fatalf("零类别 = %q, 期望 %q", got, want)
	}
}

func TestBuildYOLOPlanDataYAMLFromProject(t *testing.T) {
	proj := expTestProject("cat:1", "it's", "a b", "123", "true")
	images := []ir.Image{
		{File: "train/a.jpg", Width: 10, Height: 10},
		{File: "val/b.jpg", Width: 10, Height: 10},
	}
	plan, err := BuildPlan(proj, images, Options{Format: FormatYOLO, Task: TaskDet})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	want := "train: images/train\nval: images/val\nnames:\n" +
		"  - 'cat:1'\n  - 'it''s'\n  - a b\n  - '123'\n  - 'true'\n"
	if got := string(expPlanFile(t, plan, "data.yaml")); got != want {
		t.Fatalf("data.yaml = %q, 期望 %q", got, want)
	}
	if dsts := expCopyDsts(plan); !slices.Equal(dsts, []string{"images/train/a.jpg", "images/val/b.jpg"}) {
		// A leading source split segment is not duplicated under images/<split>/.
		t.Fatalf("Copies = %v", dsts)
	}
}

func TestBuildYOLOPlanAttributesWarning(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "a.jpg", Width: 100, Height: 100,
		Annotations: []ir.Annotation{
			{ID: "1", Category: "cat", Kind: ir.KindBBox, W: 10, H: 10, Attributes: map[string]string{"occluded": "yes"}},
			{ID: "2", Category: "cat", Kind: ir.KindBBox, W: 10, H: 10, Attributes: map[string]string{"hard": "1"}},
			{ID: "3", Category: "cat", Kind: ir.KindBBox, X: 20, W: 10, H: 10},
		},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatYOLO, Task: TaskDet})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if !expWarned(plan, "2 条标注的属性不随 YOLO 导出") {
		t.Fatalf("Warnings = %v, 期望属性汇总告警", plan.Report.Warnings)
	}
	if len(plan.Report.Warnings) != 1 {
		t.Fatalf("Warnings = %v, 期望恰好一条汇总", plan.Report.Warnings)
	}
}

func TestBuildYOLOPlanZeroCategories(t *testing.T) {
	proj := expTestProject()
	images := []ir.Image{{
		File: "a.jpg", Width: 10, Height: 10,
		Annotations: []ir.Annotation{{ID: "1", Category: "cat", Kind: ir.KindBBox, W: 5, H: 5}},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatYOLO, Task: TaskDet})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if got, want := string(expPlanFile(t, plan, "data.yaml")), "train: images/train\nnames: []\n"; got != want {
		t.Fatalf("data.yaml = %q, 期望 %q", got, want)
	}
	if plan.Report.AnnotationsDropped != 1 {
		t.Fatalf("未知类别应丢弃, Report = %+v", plan.Report)
	}
}

func TestBuildYOLOPlanInvalidImageSizeDropsAnnotations(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "a.jpg",
		Annotations: []ir.Annotation{
			{ID: "1", Category: "cat", Kind: ir.KindBBox, W: 5, H: 5},
		},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatYOLO, Task: TaskDet})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Report.AnnotationsDropped != 1 || plan.Report.Annotations != 0 {
		t.Fatalf("Report = %+v, 期望全部丢弃", plan.Report)
	}
	if n := expLossyCount(plan, RuleDropped); n != 1 {
		t.Fatalf("%s 计数 = %d, 期望 1（Report = %+v）", RuleDropped, n, plan.Report.Lossy)
	}
}

func TestBuildYOLOExportIntegration(t *testing.T) {
	root := t.TempDir()
	imgRoot := filepath.Join(root, "imgs")
	for _, sub := range []string{"train", "val"} {
		if err := os.MkdirAll(filepath.Join(imgRoot, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(imgRoot, "train", "a.jpg"), []byte("jpeg-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imgRoot, "val", "b.jpg"), []byte("jpeg-b"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj := expTestProject("cat")
	images := []ir.Image{
		{File: "val/b.jpg", Width: 100, Height: 100},
		{File: "train/a.jpg", Width: 100, Height: 100, Annotations: []ir.Annotation{
			{ID: "1", Category: "cat", Kind: ir.KindBBox, X: 10, Y: 10, W: 20, H: 20},
		}},
	}
	plan, err := BuildPlan(proj, images, Options{Format: FormatYOLO, Task: TaskDet})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	out := filepath.Join(root, "out")
	var done, total int
	if err := WriteExport(out, imgRoot, plan, func(d, tt int) { done, total = d, tt }); err != nil {
		t.Fatalf("WriteExport: %v", err)
	}
	if done != total || total != len(plan.Copies)+len(plan.Files)+1 {
		t.Fatalf("progress = %d/%d, plan = %d copies + %d files + 1", done, total, len(plan.Copies), len(plan.Files))
	}

	if data, err := os.ReadFile(filepath.Join(out, "images", "train", "a.jpg")); err != nil || string(data) != "jpeg-a" {
		t.Fatalf("images/train/a.jpg = %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(out, "images", "val", "b.jpg")); err != nil || string(data) != "jpeg-b" {
		t.Fatalf("images/val/b.jpg = %q, %v", data, err)
	}
	labels, err := os.ReadFile(filepath.Join(out, "labels", "train", "a.txt"))
	if err != nil {
		t.Fatalf("labels/train/a.txt: %v", err)
	}
	if want := "0 0.2 0.2 0.2 0.2\n"; string(labels) != want {
		t.Fatalf("labels = %q, 期望 %q", labels, want)
	}
	if _, err := os.Stat(filepath.Join(out, "labels", "val", "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("无标注图片不应有 txt, stat err = %v", err)
	}
	yamlBytes, err := os.ReadFile(filepath.Join(out, "data.yaml"))
	if err != nil {
		t.Fatalf("data.yaml: %v", err)
	}
	if !strings.Contains(string(yamlBytes), "val: images/val") {
		t.Fatalf("data.yaml = %q, 期望含 val 行", yamlBytes)
	}

	manifest, err := os.ReadFile(filepath.Join(out, manifestName))
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	var doc manifestDoc
	if err := json.Unmarshal(manifest, &doc); err != nil {
		t.Fatalf("manifest 不是 JSON: %v", err)
	}
	if doc.Format != FormatYOLO || doc.Task != TaskDet || doc.Report.Annotations != 1 || doc.Report.Images != 2 {
		t.Fatalf("manifest = %+v", doc)
	}
}
