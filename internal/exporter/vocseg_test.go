package exporter

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mylabel/internal/ir"
)

// expPlanPNG decodes one planned mask and asserts it is a palette image.
func expPlanPNG(t *testing.T, plan *Plan, rel string) *image.Paletted {
	t.Helper()
	data := expPlanFile(t, plan, rel)
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s 不是合法 PNG: %v", rel, err)
	}
	m, ok := img.(*image.Paletted)
	if !ok {
		t.Fatalf("%s 解码类型 = %T, 期望调色板图", rel, img)
	}
	return m
}

// countMaskValue counts the pixels carrying v.
func countMaskValue(m *image.Paletted, v uint8) int {
	n := 0
	b := m.Bounds()
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			if m.ColorIndexAt(x, y) == v {
				n++
			}
		}
	}
	return n
}

// boxRing is a clockwise-free rectangle ring in ir's vertex order.
func boxRing(x0, y0, x1, y1 float64) [][2]float64 {
	return [][2]float64{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}
}

// TestBuildVOCPlanSegWritesMasks: VOCSeg turns a polygon into an XML object with
// an enclosing bndbox plus the two PNG masks (docs/phase5-design.md §6).
func TestBuildVOCPlanSegWritesMasks(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "a.jpg", Width: 8, Height: 6,
		Annotations: []ir.Annotation{{
			ID: "1", Category: "cat", Kind: ir.KindPolygon,
			Rings: [][][2]float64{boxRing(1, 1, 5, 4)},
		}},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatVOC, VOCSeg: true})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Report.Annotations != 1 || plan.Report.AnnotationsDropped != 0 {
		t.Fatalf("Report = %+v, 期望 1 条写出", plan.Report)
	}
	if n := expLossyCount(plan, RuleSegDropped); n != 0 {
		t.Fatalf("%s 计数 = %d, VOCSeg=true 时不应跳过 seg", RuleSegDropped, n)
	}
	if expWarned(plan, "VOC 导出不含分割") {
		t.Fatalf("Warnings = %v, 不应有跳过告警", plan.Report.Warnings)
	}

	// The XML keeps an enclosing bndbox, identical to what det would write.
	obj := expVOCParse(t, plan, "Annotations/a.xml").Objects[0]
	if obj.Name != "cat" || obj.RobndBox != nil || obj.BndBox == nil {
		t.Fatalf("object = %+v", obj)
	}
	if *obj.BndBox != (vocBndBox{XMin: 1, YMin: 1, XMax: 5, YMax: 4}) {
		t.Fatalf("bndbox = %+v, 期望多边形外接框取整", *obj.BndBox)
	}

	class := expPlanPNG(t, plan, "SegmentationClass/a.png")
	object := expPlanPNG(t, plan, "SegmentationObject/a.png")
	for name, m := range map[string]*image.Paletted{"class": class, "object": object} {
		if got, want := countMaskValue(m, 1), 4*3; got != want {
			t.Fatalf("%s 掩码填充像素 = %d, 期望 %d:\n%s", name, got, want, maskArt(m))
		}
		if v := m.ColorIndexAt(0, 0); v != 0 {
			t.Fatalf("%s 掩码 (0,0) = %d, 期望背景 0", name, v)
		}
	}
	// The bndbox is the painted area's bounding box, so they must agree.
	if class.ColorIndexAt(1, 1) != 1 || class.ColorIndexAt(4, 3) != 1 {
		t.Fatalf("掩码未覆盖 bndbox 内角:\n%s", maskArt(class))
	}
}

// TestBuildVOCPlanSegOneBasedBndbox: VOCOneBased shifts the seg bndbox too.
func TestBuildVOCPlanSegOneBasedBndbox(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "a.jpg", Width: 8, Height: 6,
		Annotations: []ir.Annotation{{
			ID: "1", Category: "cat", Kind: ir.KindPolygon,
			Rings: [][][2]float64{boxRing(1, 1, 5, 4)},
		}},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatVOC, VOCSeg: true, VOCOneBased: true})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	obj := expVOCParse(t, plan, "Annotations/a.xml").Objects[0]
	if *obj.BndBox != (vocBndBox{XMin: 2, YMin: 2, XMax: 6, YMax: 5}) {
		t.Fatalf("1-based bndbox = %+v, 期望 2/2/6/5", *obj.BndBox)
	}
}

// TestBuildVOCPlanSegZOrderOverwrites: later annotations win in both masks (VOC
// z order), and object ids follow the annotation order.
func TestBuildVOCPlanSegZOrderOverwrites(t *testing.T) {
	proj := expTestProject("cat", "dog")
	images := []ir.Image{{
		File: "a.jpg", Width: 8, Height: 6,
		Annotations: []ir.Annotation{
			{ID: "1", Category: "cat", Kind: ir.KindPolygon,
				Rings: [][][2]float64{boxRing(1, 1, 5, 5)}},
			{ID: "2", Category: "dog", Kind: ir.KindPolygon,
				Rings: [][][2]float64{boxRing(3, 3, 7, 6)}},
		},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatVOC, VOCSeg: true})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Report.Annotations != 2 {
		t.Fatalf("Report = %+v", plan.Report)
	}
	class := expPlanPNG(t, plan, "SegmentationClass/a.png")
	object := expPlanPNG(t, plan, "SegmentationObject/a.png")

	// Overlap x=3..4, y=3..4 belongs to the later object (dog = value 2).
	if v := class.ColorIndexAt(3, 3); v != 2 {
		t.Fatalf("重叠区 class 值 = %d, 期望后写的 2", v)
	}
	if v := class.ColorIndexAt(1, 1); v != 1 {
		t.Fatalf("cat 独占区 class 值 = %d, 期望 1", v)
	}
	if v := object.ColorIndexAt(1, 1); v != 1 {
		t.Fatalf("cat 独占区 object id = %d, 期望 1", v)
	}
	if v := object.ColorIndexAt(3, 3); v != 2 {
		t.Fatalf("重叠区 object id = %d, 期望后写的 2", v)
	}
	if v := object.ColorIndexAt(6, 5); v != 2 {
		t.Fatalf("dog 独占区 object id = %d, 期望 2", v)
	}
}

// TestBuildVOCPlanSegMultiRing: a multi-ring polygon is bridged (RuleMultiRing)
// before rasterising and still produces a single XML object.
func TestBuildVOCPlanSegMultiRing(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "a.jpg", Width: 8, Height: 6,
		Annotations: []ir.Annotation{{
			ID: "1", Category: "cat", Kind: ir.KindPolygon,
			Rings: [][][2]float64{boxRing(1, 1, 7, 5), boxRing(3, 2, 5, 4)},
		}},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatVOC, VOCSeg: true})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if n := expLossyCount(plan, RuleMultiRing); n != 1 {
		t.Fatalf("%s 计数 = %d, 期望 1", RuleMultiRing, n)
	}
	doc := expVOCParse(t, plan, "Annotations/a.xml")
	if len(doc.Objects) != 1 || *doc.Objects[0].BndBox != (vocBndBox{XMin: 1, YMin: 1, XMax: 7, YMax: 5}) {
		t.Fatalf("objects = %+v, 期望 1 条外接框 1/1/7/5", doc.Objects)
	}
	class := expPlanPNG(t, plan, "SegmentationClass/a.png")
	// Bridging keeps the inner ring as a hole, so both sides stay painted.
	if v := class.ColorIndexAt(1, 3); v != 1 {
		t.Fatalf("外环左侧 = %d, 期望 1", v)
	}
	if v := class.ColorIndexAt(6, 3); v != 1 {
		t.Fatalf("外环右侧 = %d, 期望 1", v)
	}
	if v := class.ColorIndexAt(4, 3); v != 0 {
		t.Fatalf("内环内部 = %d, 期望背景 0:\n%s", v, maskArt(class))
	}
}

// TestBuildVOCPlanSegClassValueLimit: category index+1 beyond 254 drops the seg
// (RuleDropped), while index 253 (= value 254) still fits.
func TestBuildVOCPlanSegClassValueLimit(t *testing.T) {
	names := make([]string, vocMaskMaxClass+1) // indices 0..254
	for i := range names {
		names[i] = "c" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	proj := expTestProject(names...)
	images := []ir.Image{{
		File: "a.jpg", Width: 8, Height: 6,
		Annotations: []ir.Annotation{
			{ID: "ok", Category: names[253], Kind: ir.KindPolygon,
				Rings: [][][2]float64{boxRing(1, 1, 3, 3)}},
			{ID: "over", Category: names[254], Kind: ir.KindPolygon,
				Rings: [][][2]float64{boxRing(4, 4, 6, 6)}},
		},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatVOC, VOCSeg: true})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Report.Annotations != 1 || plan.Report.AnnotationsDropped != 1 {
		t.Fatalf("Report = %+v, 期望保留 1 条、丢弃 1 条", plan.Report)
	}
	if n := expLossyCount(plan, RuleDropped); n != 1 {
		t.Fatalf("%s 计数 = %d, 期望 1", RuleDropped, n)
	}
	class := expPlanPNG(t, plan, "SegmentationClass/a.png")
	if v := class.ColorIndexAt(1, 1); v != uint8(vocMaskMaxClass) {
		t.Fatalf("序号 253 的类别掩码值 = %d, 期望 %d", v, vocMaskMaxClass)
	}
	if v := class.ColorIndexAt(4, 4); v != 0 {
		t.Fatalf("超限类别不应写入掩码, 得到 %d", v)
	}
}

// TestBuildVOCPlanSegObjectIDLimit: ids are 1..255 in annotation order; the
// 256th seg object is dropped with one summary warning.
func TestBuildVOCPlanSegObjectIDLimit(t *testing.T) {
	const side = 16 // 16x16 single-pixel objects, one more than the id limit
	proj := expTestProject("cat")
	anns := make([]ir.Annotation, side*side)
	for i := range anns {
		x, y := float64(i%side), float64(i/side)
		anns[i] = ir.Annotation{
			ID: "a" + strconv.Itoa(i), Category: "cat", Kind: ir.KindPolygon,
			Rings: [][][2]float64{boxRing(x, y, x+1, y+1)},
		}
	}
	images := []ir.Image{{File: "a.jpg", Width: side, Height: side, Annotations: anns}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatVOC, VOCSeg: true})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Report.Annotations != vocMaskMaxObjects || plan.Report.AnnotationsDropped != 1 {
		t.Fatalf("Report = %+v, 期望保留 %d 条、丢弃 1 条", plan.Report, vocMaskMaxObjects)
	}
	if n := len(expVOCParse(t, plan, "Annotations/a.xml").Objects); n != vocMaskMaxObjects {
		t.Fatalf("XML object 数 = %d, 期望 %d", n, vocMaskMaxObjects)
	}
	if !expWarned(plan, "seg 对象最多 255") {
		t.Fatalf("Warnings = %v, 期望对象上限告警", plan.Report.Warnings)
	}
	object := expPlanPNG(t, plan, "SegmentationObject/a.png")
	if v := object.ColorIndexAt(0, 0); v != 1 {
		t.Fatalf("第一个对象 id = %d, 期望 1", v)
	}
	if v := object.ColorIndexAt(14, 15); v != uint8(vocMaskMaxObjects) {
		t.Fatalf("第 255 个对象 id = %d, 期望 %d", v, vocMaskMaxObjects)
	}
	if v := object.ColorIndexAt(15, 15); v != 0 {
		t.Fatalf("超限对象不应写入掩码, 得到 %d", v)
	}
}

// TestBuildVOCPlanSegClippedAndEmpty: out-of-bounds geometry counts
// RuleClipped, and a polygon that rasterises to nothing is dropped.
func TestBuildVOCPlanSegClippedAndEmpty(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "a.jpg", Width: 8, Height: 6,
		Annotations: []ir.Annotation{
			{ID: "clip", Category: "cat", Kind: ir.KindPolygon,
				Rings: [][][2]float64{boxRing(-2, -2, 4, 4)}},
			{ID: "tiny", Category: "cat", Kind: ir.KindPolygon,
				Rings: [][][2]float64{{{5.0, 5.0}, {5.4, 5.0}, {5.0, 5.4}}}},
		},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatVOC, VOCSeg: true})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Report.Annotations != 1 || plan.Report.AnnotationsDropped != 1 {
		t.Fatalf("Report = %+v", plan.Report)
	}
	if n := expLossyCount(plan, RuleClipped); n != 1 {
		t.Fatalf("%s 计数 = %d, 期望 1", RuleClipped, n)
	}
	if n := expLossyCount(plan, RuleDropped); n != 1 {
		t.Fatalf("%s 计数 = %d, 期望 1（Report = %+v）", RuleDropped, n, plan.Report.Lossy)
	}
	if !expLossySample(plan, RuleDropped, "多边形栅格化为空") {
		t.Fatalf("Report.Lossy = %+v, 期望消息「多边形栅格化为空」", plan.Report.Lossy)
	}
	class := expPlanPNG(t, plan, "SegmentationClass/a.png")
	if v := class.ColorIndexAt(0, 0); v != 1 {
		t.Fatalf("裁剪后应填充 (0,0), 得到 %d:\n%s", v, maskArt(class))
	}
	if got := countMaskValue(class, 1); got != 4*4 {
		t.Fatalf("裁剪后填充像素 = %d, 期望 16:\n%s", got, maskArt(class))
	}
}

// TestBuildVOCPlanSegDisabledWritesNoMasks: the default keeps skipping polygons
// and writes no mask files.
func TestBuildVOCPlanSegDisabledWritesNoMasks(t *testing.T) {
	proj := expTestProject("cat")
	images := []ir.Image{{
		File: "a.jpg", Width: 8, Height: 6,
		Annotations: []ir.Annotation{{
			ID: "1", Category: "cat", Kind: ir.KindPolygon,
			Rings: [][][2]float64{boxRing(1, 1, 5, 4)},
		}},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatVOC})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	for _, rel := range []string{"SegmentationClass/a.png", "SegmentationObject/a.png"} {
		if expHasFile(plan, rel) {
			t.Fatalf("VOCSeg=false 不应写 %s", rel)
		}
	}
	if n := expLossyCount(plan, RuleSegDropped); n != 1 {
		t.Fatalf("%s 计数 = %d, 期望 1", RuleSegDropped, n)
	}
}

// TestBuildVOCExportIntegrationWithSeg: the masks land on disk next to the XML.
func TestBuildVOCExportIntegrationWithSeg(t *testing.T) {
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
		File: "a.jpg", Width: 8, Height: 6,
		Annotations: []ir.Annotation{{
			ID: "1", Category: "cat", Kind: ir.KindPolygon,
			Rings: [][][2]float64{boxRing(1, 1, 5, 4)},
		}},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatVOC, VOCSeg: true})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	out := filepath.Join(root, "out")
	if err := WriteExport(context.Background(), out, imgRoot, plan, nil); err != nil {
		t.Fatalf("WriteExport: %v", err)
	}
	for _, rel := range []string{"SegmentationClass/a.png", "SegmentationObject/a.png"} {
		data, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s 未落盘: %v", rel, err)
		}
		if !bytes.HasPrefix(data, []byte("\x89PNG")) {
			t.Fatalf("%s 不是 PNG", rel)
		}
	}

	// The manifest records the option so a re-run can be reproduced.
	manifest, err := os.ReadFile(filepath.Join(out, manifestName))
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	var mdoc manifestDoc
	if err := json.Unmarshal(manifest, &mdoc); err != nil {
		t.Fatalf("manifest 不是 JSON: %v", err)
	}
	if !mdoc.Options.VOCSeg || !strings.Contains(string(manifest), `"voc_seg": true`) {
		t.Fatalf("manifest 未记录 voc_seg: %s", manifest)
	}
	if mdoc.Report.Annotations != 1 || mdoc.Report.AnnotationsDropped != 0 {
		t.Fatalf("manifest report = %+v", mdoc.Report)
	}
}

// expLossySample reports whether rule recorded a sample whose message contains
// substr.
func expLossySample(plan *Plan, rule, substr string) bool {
	for _, st := range plan.Report.Lossy {
		if st.Rule != rule {
			continue
		}
		for _, s := range st.Samples {
			if strings.Contains(s.Message, substr) {
				return true
			}
		}
	}
	return false
}
