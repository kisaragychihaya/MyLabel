package exporter

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mylabel/internal/ir"
)

// expVOCParse unmarshals one generated annotation file, asserting the UTF-8
// declaration and that encoding/xml can read the document back.
func expVOCParse(t *testing.T, plan *Plan, rel string) vocDoc {
	t.Helper()
	data := expPlanFile(t, plan, rel)
	if !strings.HasPrefix(string(data), vocXMLHeader) {
		t.Fatalf("%s 缺少 XML 声明: %q", rel, data)
	}
	var doc vocDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%s 无法解析: %v\n%s", rel, err, data)
	}
	return doc
}

func TestBuildVOCPlanBndBoxRounding(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "a.jpg", Width: 640, Height: 480,
		Annotations: []ir.Annotation{
			{ID: "1", Category: "cat", Kind: ir.KindBBox, X: 10.4, Y: 20.6, W: 100.5, H: 50.4},
		},
	}}

	plan, err := BuildPlan(proj, images, Options{Format: FormatVOC})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	doc := expVOCParse(t, plan, "Annotations/a.xml")
	if doc.Folder != "JPEGImages" || doc.Filename != "a.jpg" {
		t.Fatalf("folder/filename = %q/%q", doc.Folder, doc.Filename)
	}
	if doc.Size != (vocSize{Width: 640, Height: 480, Depth: 3}) {
		t.Fatalf("size = %+v", doc.Size)
	}
	if len(doc.Objects) != 1 {
		t.Fatalf("objects = %+v", doc.Objects)
	}
	obj := doc.Objects[0]
	if obj.Name != "cat" || obj.BndBox == nil || obj.RobndBox != nil {
		t.Fatalf("object = %+v", obj)
	}
	if *obj.BndBox != (vocBndBox{XMin: 10, YMin: 21, XMax: 111, YMax: 71}) {
		t.Fatalf("bndbox = %+v, 期望取整 10/21/111/71", *obj.BndBox)
	}
	if plan.Report.Annotations != 1 || plan.Report.AnnotationsDropped != 0 {
		t.Fatalf("Report = %+v", plan.Report)
	}

	plan, err = BuildPlan(proj, images, Options{Format: FormatVOC, VOCOneBased: true})
	if err != nil {
		t.Fatalf("BuildPlan one-based: %v", err)
	}
	obj = expVOCParse(t, plan, "Annotations/a.xml").Objects[0]
	if *obj.BndBox != (vocBndBox{XMin: 11, YMin: 22, XMax: 112, YMax: 72}) {
		t.Fatalf("1-based bndbox = %+v, 期望 11/22/112/72", *obj.BndBox)
	}
}

func TestBuildVOCPlanRobndBoxRadians(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "a.jpg", Width: 640, Height: 480,
		Annotations: []ir.Annotation{
			{ID: "1", Category: "cat", Kind: ir.KindRotated, CX: 100, CY: 50, RW: 40, RH: 20, Angle: 0.3},
		},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatVOC})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	obj := expVOCParse(t, plan, "Annotations/a.xml").Objects[0]
	if obj.BndBox != nil || obj.RobndBox == nil {
		t.Fatalf("object = %+v, 期望 robndbox", obj)
	}
	want := vocRobndBox{CX: "100", CY: "50", W: "40", H: "20", Angle: "0.3"}
	if *obj.RobndBox != want {
		t.Fatalf("robndbox = %+v, 期望 %+v（弧度原样）", *obj.RobndBox, want)
	}
	if n := expLossyCount(plan, RuleObbToBndbox); n != 0 {
		t.Fatalf("%s 计数 = %d, 期望 0", RuleObbToBndbox, n)
	}
	if data := string(expPlanFile(t, plan, "Annotations/a.xml")); !strings.Contains(data, "<angle>0.3</angle>") {
		t.Fatalf("xml 缺少 <angle>0.3</angle>:\n%s", data)
	}
}

func TestBuildVOCPlanDowngradeOBB(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "a.jpg", Width: 640, Height: 480,
		Annotations: []ir.Annotation{
			{ID: "1", Category: "cat", Kind: ir.KindRotated,
				Points: [][2]float64{{80, 40}, {120, 40}, {120, 60}, {80, 60}}},
		},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatVOC, VOCDowngradeOBB: true})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	obj := expVOCParse(t, plan, "Annotations/a.xml").Objects[0]
	if obj.RobndBox != nil || obj.BndBox == nil {
		t.Fatalf("object = %+v, 期望降级为 bndbox", obj)
	}
	if *obj.BndBox != (vocBndBox{XMin: 80, YMin: 40, XMax: 120, YMax: 60}) {
		t.Fatalf("bndbox = %+v", *obj.BndBox)
	}
	if n := expLossyCount(plan, RuleObbToBndbox); n != 1 {
		t.Fatalf("%s 计数 = %d, 期望 1", RuleObbToBndbox, n)
	}
}

func TestBuildVOCPlanSkipsSegments(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "a.jpg", Width: 640, Height: 480,
		Annotations: []ir.Annotation{
			{ID: "1", Category: "cat", Kind: ir.KindPolygon,
				Rings: [][][2]float64{{{10, 10}, {100, 10}, {100, 100}}}},
			{ID: "2", Category: "cat", Kind: ir.KindPolygon,
				Rings: [][][2]float64{{{20, 20}, {40, 20}, {40, 40}}}},
			{ID: "3", Category: "cat", Kind: ir.KindBBox, X: 5, Y: 5, W: 10, H: 10},
		},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatVOC})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if n := expLossyCount(plan, RuleSegDropped); n != 2 {
		t.Fatalf("%s 计数 = %d, 期望 2", RuleSegDropped, n)
	}
	if !expWarned(plan, "VOC 导出不含分割，2 条 seg 标注被跳过") {
		t.Fatalf("Warnings = %v, 期望 seg 汇总告警", plan.Report.Warnings)
	}
	if plan.Report.Annotations != 1 || plan.Report.AnnotationsDropped != 2 {
		t.Fatalf("Report = %+v", plan.Report)
	}
	if n := len(expVOCParse(t, plan, "Annotations/a.xml").Objects); n != 1 {
		t.Fatalf("object 数 = %d, 期望 1", n)
	}
}

func TestBuildVOCPlanDropsUnknownCategoryAndDegenerateBox(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "a.jpg", Width: 100, Height: 100,
		Annotations: []ir.Annotation{
			{ID: "1", Category: "nope", Kind: ir.KindBBox, W: 10, H: 10},
			{ID: "2", Category: "cat", Kind: ir.KindBBox, X: 200, Y: 200, W: 10, H: 10},
			{ID: "3", Category: "cat", Kind: ir.KindBBox},
		},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatVOC})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Report.AnnotationsDropped != 3 || plan.Report.Annotations != 0 {
		t.Fatalf("Report = %+v", plan.Report)
	}
	if n := expLossyCount(plan, RuleDropped); n != 3 {
		t.Fatalf("%s 计数 = %d, 期望 3", RuleDropped, n)
	}
	// An annotation-less image still gets an object-less xml.
	if n := len(expVOCParse(t, plan, "Annotations/a.xml").Objects); n != 0 {
		t.Fatalf("object 数 = %d, 期望 0", n)
	}
}

func TestBuildVOCPlanImageSets(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{
		{File: "val/b.jpg", Width: 10, Height: 10},
		{File: "train/a.jpg", Width: 10, Height: 10},
	}
	plan, err := BuildPlan(proj, images, Options{Format: FormatVOC})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if got := string(expPlanFile(t, plan, "ImageSets/Main/train.txt")); got != "a\n" {
		t.Fatalf("train.txt = %q, 期望 %q", got, "a\n")
	}
	if got := string(expPlanFile(t, plan, "ImageSets/Main/val.txt")); got != "b\n" {
		t.Fatalf("val.txt = %q, 期望 %q", got, "b\n")
	}
	if got := expCopyDsts(plan); len(got) != 2 ||
		got[0] != "JPEGImages/a.jpg" || got[1] != "JPEGImages/b.jpg" {
		t.Fatalf("Copies = %v, 期望平铺到 JPEGImages", got)
	}

	// No val images: val.txt is omitted entirely.
	plan, err = BuildPlan(proj, []ir.Image{{File: "train/a.jpg", Width: 10, Height: 10}}, Options{Format: FormatVOC})
	if err != nil {
		t.Fatalf("BuildPlan 单 split: %v", err)
	}
	if expHasFile(plan, "ImageSets/Main/val.txt") {
		t.Fatal("没有 val 图片时不应写 val.txt")
	}
}

func TestBuildVOCPlanRejectsDuplicateBasenames(t *testing.T) {
	proj := expTestProject("cat")
	for _, tc := range []struct {
		name   string
		images []ir.Image
	}{
		{"同名不同目录", []ir.Image{
			{File: "d1/a.jpg", Width: 10, Height: 10},
			{File: "d2/a.jpg", Width: 10, Height: 10},
		}},
		{"同主名不同扩展", []ir.Image{
			{File: "x/a.jpg", Width: 10, Height: 10},
			{File: "y/a.png", Width: 10, Height: 10},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildPlan(proj, tc.images, Options{Format: FormatVOC})
			if err == nil || !strings.Contains(err.Error(), "冲突") {
				t.Fatalf("err = %v, 期望输出路径冲突", err)
			}
		})
	}
}

func TestBuildVOCPlanXMLShape(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "sub/a.jpg", Width: 20, Height: 10,
		Annotations: []ir.Annotation{
			{ID: "1", Category: "cat", Kind: ir.KindBBox, X: 1, Y: 2, W: 3, H: 4},
		},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatVOC})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	data := string(expPlanFile(t, plan, "Annotations/a.xml"))
	for _, want := range []string{
		"<annotation>",
		"<folder>JPEGImages</folder>",
		"<filename>a.jpg</filename>",
		"<width>20</width>",
		"<height>10</height>",
		"<depth>3</depth>",
		"<object>",
		"<name>cat</name>",
		"<xmin>1</xmin>",
		"<ymax>6</ymax>",
	} {
		if !strings.Contains(data, want) {
			t.Fatalf("xml 缺少 %q:\n%s", want, data)
		}
	}
	if !strings.HasSuffix(data, "\n") {
		t.Fatalf("xml 应以换行结尾:\n%q", data)
	}
}

func TestBuildVOCExportIntegration(t *testing.T) {
	root := t.TempDir()
	imgRoot := filepath.Join(root, "imgs")
	if err := os.MkdirAll(imgRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imgRoot, "a.jpg"), []byte("jpeg-a"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "a.jpg", Width: 100, Height: 50,
		Annotations: []ir.Annotation{
			{ID: "1", Category: "cat", Kind: ir.KindBBox, X: 10, Y: 10, W: 20, H: 20},
		},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatVOC, VOCOneBased: true})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	out := filepath.Join(root, "out")
	if err := WriteExport(out, imgRoot, plan, nil); err != nil {
		t.Fatalf("WriteExport: %v", err)
	}

	if data, err := os.ReadFile(filepath.Join(out, "JPEGImages", "a.jpg")); err != nil || string(data) != "jpeg-a" {
		t.Fatalf("JPEGImages/a.jpg = %q, %v", data, err)
	}
	xmlData, err := os.ReadFile(filepath.Join(out, "Annotations", "a.xml"))
	if err != nil {
		t.Fatalf("Annotations/a.xml: %v", err)
	}
	var doc vocDoc
	if err := xml.Unmarshal(xmlData, &doc); err != nil {
		t.Fatalf("落盘的 xml 无法解析: %v", err)
	}
	if len(doc.Objects) != 1 || doc.Objects[0].BndBox == nil {
		t.Fatalf("落盘 objects = %+v", doc.Objects)
	}
	if *doc.Objects[0].BndBox != (vocBndBox{XMin: 11, YMin: 11, XMax: 31, YMax: 31}) {
		t.Fatalf("落盘 bndbox = %+v, 期望 1-based 11/11/31/31", *doc.Objects[0].BndBox)
	}
	sets, err := os.ReadFile(filepath.Join(out, "ImageSets", "Main", "train.txt"))
	if err != nil || string(sets) != "a\n" {
		t.Fatalf("ImageSets/Main/train.txt = %q, %v", sets, err)
	}
	if _, err := os.Stat(filepath.Join(out, "ImageSets", "Main", "val.txt")); !os.IsNotExist(err) {
		t.Fatalf("不应有 val.txt, stat err = %v", err)
	}

	manifest, err := os.ReadFile(filepath.Join(out, manifestName))
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	var mdoc manifestDoc
	if err := json.Unmarshal(manifest, &mdoc); err != nil {
		t.Fatalf("manifest 不是 JSON: %v", err)
	}
	if mdoc.Format != FormatVOC || !mdoc.Options.VOCOneBased || mdoc.Report.Annotations != 1 {
		t.Fatalf("manifest = %+v", mdoc)
	}
}
