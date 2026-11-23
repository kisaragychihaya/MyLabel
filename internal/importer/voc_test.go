package importer

import (
	"math"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"mylabel/internal/ir"
)

const vocBasicDoc = `<?xml version="1.0" encoding="UTF-8"?>
<annotation>
  <folder>VOC2007</folder>
  <filename>0001.jpg</filename>
  <size><width>353</width><height>500</height><depth>3</depth></size>
  <object>
    <name>dog</name><difficult>0</difficult><truncated>0</truncated>
    <bndbox><xmin>48</xmin><ymin>240</ymin><xmax>195</xmax><ymax>371</ymax></bndbox>
  </object>
  <object>
    <name>person</name><difficult>1</difficult><truncated>1</truncated>
    <bndbox><xmin>8</xmin><ymin>12</ymin><xmax>288</xmax><ymax>323</ymax></bndbox>
  </object>
</annotation>
`

func TestParseVOCBasic(t *testing.T) {
	xmlDir := filepath.Join(t.TempDir(), "Annotations")
	mustWriteFixture(t, filepath.Join(xmlDir, "0001.xml"), vocBasicDoc)
	mustWriteFixture(t, filepath.Join(xmlDir, "sub", "0002.xml"), `<annotation>
  <filename>0002.png</filename>
  <size><width>100</width><height>50</height></size>
  <object>
    <name>dog</name>
    <bndbox><xmin>10</xmin><ymin>10</ymin><xmax>40</xmax><ymax>30</ymax></bndbox>
  </object>
</annotation>
`)

	ds, issues, err := ParseVOC(xmlDir, Options{})
	if err != nil {
		t.Fatalf("ParseVOC 返回错误：%v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("期望没有解析错误，实际：%v", issueSummary(issues))
	}
	if len(ds.Images) != 2 {
		t.Fatalf("期望 2 张图，实际 %d 张：%+v", len(ds.Images), ds.Images)
	}
	if len(ds.Categories) != 2 {
		t.Fatalf("期望 2 个类别，实际 %+v", ds.Categories)
	}
	if got := categoryByKey(t, ds, "dog"); got.Index != 0 || got.Name != "dog" {
		t.Fatalf("按出现顺序 dog 期望 Index=0，实际 %+v", got)
	}
	if got := categoryByKey(t, ds, "person"); got.Index != 1 {
		t.Fatalf("按出现顺序 person 期望 Index=1，实际 %+v", got)
	}

	img := imageByFile(t, ds, "0001.jpg")
	if img.Width != 353 || img.Height != 500 {
		t.Fatalf("0001.jpg 尺寸期望 353x500，实际 %dx%d", img.Width, img.Height)
	}
	if len(img.Annotations) != 2 {
		t.Fatalf("0001.jpg 期望 2 个标注，实际 %+v", img.Annotations)
	}
	dog := img.Annotations[0]
	if dog.Kind != ir.KindBBox || !nearlyEqual(dog.X, 48) || !nearlyEqual(dog.Y, 240) ||
		!nearlyEqual(dog.W, 147) || !nearlyEqual(dog.H, 131) {
		t.Fatalf("dog 期望 bbox (48,240,147,131)，实际 %+v", dog)
	}
	if dog.Category != "dog" {
		t.Fatalf("dog 标注类别期望 dog，实际 %q", dog.Category)
	}
	if dog.Attributes != nil {
		t.Fatalf("difficult=0/truncated=0 不应写属性，实际 %+v", dog.Attributes)
	}
	person := img.Annotations[1]
	if !nearlyEqual(person.X, 8) || !nearlyEqual(person.Y, 12) ||
		!nearlyEqual(person.W, 280) || !nearlyEqual(person.H, 311) {
		t.Fatalf("person 期望 bbox (8,12,280,311)，实际 %+v", person)
	}
	if person.Attributes["difficult"] != "1" || person.Attributes["truncated"] != "1" {
		t.Fatalf("difficult/truncated=1 应写属性，实际 %+v", person.Attributes)
	}

	sub := imageByFile(t, ds, "0002.png")
	if sub.Width != 100 || sub.Height != 50 {
		t.Fatalf("0002.png 尺寸期望 100x50，实际 %dx%d", sub.Width, sub.Height)
	}
	if len(sub.Annotations) != 1 || !nearlyEqual(sub.Annotations[0].W, 30) {
		t.Fatalf("0002.png 标注错误：%+v", sub.Annotations)
	}
}

// assertRotatedBox checks the rotation-invariant geometry of a canonical
// rotated_bbox: centroid, unordered side lengths, and edge direction modulo
// 90° (ir.Normalize's canonical vertex order rotates w/h and angle together).
func assertRotatedBox(t *testing.T, ann ir.Annotation, cx, cy, sideA, sideB, angle float64) {
	t.Helper()
	if ann.Kind != ir.KindRotated || len(ann.Points) != 4 {
		t.Fatalf("期望 4 个顶点的 rotated_bbox，实际 %+v", ann)
	}
	var sx, sy float64
	for _, p := range ann.Points {
		sx += p[0]
		sy += p[1]
	}
	if !nearlyEqual(sx/4, cx) || !nearlyEqual(sy/4, cy) {
		t.Fatalf("顶点质心期望 (%v,%v)，实际 (%v,%v)：%v", cx, cy, sx/4, sy/4, ann.Points)
	}
	halfPi := math.Pi / 2
	lens := make([]float64, 4)
	for i := range ann.Points {
		j := (i + 1) % 4
		dx := ann.Points[j][0] - ann.Points[i][0]
		dy := ann.Points[j][1] - ann.Points[i][1]
		lens[i] = math.Hypot(dx, dy)
		// Every edge runs along angle or angle+90°.
		d := math.Atan2(dy, dx) - angle
		d = math.Mod(d, halfPi)
		if d < 0 {
			d += halfPi
		}
		if d > 1e-9 && halfPi-d > 1e-9 {
			t.Fatalf("第 %d 条边方向 %v 不是 angle=%v 的整数倍 90°，顶点 %v",
				i+1, math.Atan2(dy, dx), angle, ann.Points)
		}
	}
	sort.Float64s(lens)
	want := []float64{math.Min(sideA, sideB), math.Min(sideA, sideB), math.Max(sideA, sideB), math.Max(sideA, sideB)}
	for i := range want {
		if !nearlyEqual(lens[i], want[i]) {
			t.Fatalf("边长期望 %v，实际 %v", want, lens)
		}
	}
	if !nearlyEqual(ann.CX, cx) || !nearlyEqual(ann.CY, cy) {
		t.Fatalf("派生缓存中心期望 (%v,%v)，实际 (%v,%v)", cx, cy, ann.CX, ann.CY)
	}
	if !nearlyEqual(ann.RW*ann.RH, sideA*sideB) {
		t.Fatalf("派生缓存面积期望 %v，实际 rw*rh=%v", sideA*sideB, ann.RW*ann.RH)
	}
}

func TestParseVOCRobndBoxPreferred(t *testing.T) {
	xmlDir := filepath.Join(t.TempDir(), "Annotations")
	mustWriteFixture(t, filepath.Join(xmlDir, "0001.xml"), `<annotation>
  <filename>0001.jpg</filename>
  <size><width>353</width><height>500</height></size>
  <object>
    <name>ship</name>
    <robndbox><cx>100</cx><cy>50</cy><w>40</w><h>20</h><angle>0.5</angle></robndbox>
    <bndbox><xmin>80</xmin><ymin>40</ymin><xmax>120</xmax><ymax>60</ymax></bndbox>
  </object>
  <object>
    <name>car</name>
    <bndbox><xmin>10</xmin><ymin>10</ymin><xmax>50</xmax><ymax>30</ymax></bndbox>
  </object>
  <object>
    <name>plane</name>
    <robndbox><cx>200</cx><cy>100</cy><w>30</w><h>10</h></robndbox>
    <bndbox><xmin>185</xmin><ymin>95</ymin><xmax>215</xmax><ymax>105</ymax></bndbox>
  </object>
</annotation>
`)

	ds, issues, err := ParseVOC(xmlDir, Options{})
	if err != nil {
		t.Fatalf("ParseVOC 返回错误：%v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("robndbox 缺 angle 时应退回 bndbox 而不是报错，实际：%v", issueSummary(issues))
	}
	var warned string
	for _, w := range ds.Warnings {
		if strings.Contains(w.Message, "改用 bndbox") {
			warned = w.Message
		}
	}
	if warned == "" {
		t.Fatalf("robndbox 非法退回 bndbox 应产生警告，实际 Warnings=%v", issueSummary(ds.Warnings))
	}

	anns := imageByFile(t, ds, "0001.jpg").Annotations
	if len(anns) != 3 {
		t.Fatalf("期望 3 个标注，实际 %+v", anns)
	}

	ship := anns[0]
	if ship.Kind != ir.KindRotated {
		t.Fatalf("同对象有 robndbox+bndbox 时期望 rotated_bbox，实际 %q", ship.Kind)
	}
	// ir.Normalize canonicalizes the vertex order, so w/h and angle only
	// round-trip up to a 90° permutation: assert the geometry instead.
	assertRotatedBox(t, ship, 100, 50, 40, 20, 0.5)

	car := anns[1]
	if car.Kind != ir.KindBBox || !nearlyEqual(car.X, 10) || !nearlyEqual(car.W, 40) || !nearlyEqual(car.H, 20) {
		t.Fatalf("只有 bndbox 的对象期望 bbox (10,10,40,20)，实际 %+v", car)
	}

	plane := anns[2]
	if plane.Kind != ir.KindBBox || !nearlyEqual(plane.X, 185) || !nearlyEqual(plane.Y, 95) ||
		!nearlyEqual(plane.W, 30) || !nearlyEqual(plane.H, 10) {
		t.Fatalf("robndbox 非法时应退回 bndbox (185,95,30,10)，实际 %+v", plane)
	}
}

func TestParseVOCOneBased(t *testing.T) {
	doc := `<annotation>
  <filename>a.jpg</filename>
  <size><width>100</width><height>100</height></size>
  <object>
    <name>cat</name>
    <bndbox><xmin>10</xmin><ymin>20</ymin><xmax>30</xmax><ymax>60</ymax></bndbox>
  </object>
</annotation>
`
	for _, tc := range []struct {
		name     string
		oneBased bool
		x, y     float64
		w, h     float64
	}{
		{"zero based", false, 10, 20, 20, 40},
		{"one based", true, 9, 19, 20, 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xmlDir := filepath.Join(t.TempDir(), "Annotations")
			mustWriteFixture(t, filepath.Join(xmlDir, "a.xml"), doc)
			ds, issues, err := ParseVOC(xmlDir, Options{VOCOneBased: tc.oneBased})
			if err != nil {
				t.Fatalf("ParseVOC 返回错误：%v", err)
			}
			if len(issues) != 0 {
				t.Fatalf("期望没有解析错误，实际：%v", issueSummary(issues))
			}
			ann := imageByFile(t, ds, "a.jpg").Annotations[0]
			if !nearlyEqual(ann.X, tc.x) || !nearlyEqual(ann.Y, tc.y) ||
				!nearlyEqual(ann.W, tc.w) || !nearlyEqual(ann.H, tc.h) {
				t.Fatalf("期望 bbox (%v,%v,%v,%v)，实际 %+v", tc.x, tc.y, tc.w, tc.h, ann)
			}
		})
	}
}

func TestParseVOCIssues(t *testing.T) {
	xmlDir := filepath.Join(t.TempDir(), "Annotations")
	mustWriteFixture(t, filepath.Join(xmlDir, "bad.xml"), `<annotation><object><name>dog</name>`)
	mustWriteFixture(t, filepath.Join(xmlDir, "nosize.xml"), `<annotation>
  <filename>nosize.jpg</filename>
  <object><name>dog</name><bndbox><xmin>1</xmin><ymin>1</ymin><xmax>9</xmax><ymax>9</ymax></bndbox></object>
</annotation>
`)
	mustWriteFixture(t, filepath.Join(xmlDir, "zerowidth.xml"), `<annotation>
  <filename>zero.jpg</filename>
  <size><width>0</width><height>100</height></size>
</annotation>
`)
	mustWriteFixture(t, filepath.Join(xmlDir, "noname.xml"), `<annotation>
  <size><width>10</width><height>10</height></size>
</annotation>
`)
	mustWriteFixture(t, filepath.Join(xmlDir, "objects.xml"), `<annotation>
  <filename>objects.jpg</filename>
  <size><width>100</width><height>100</height></size>
  <object><name>nobox</name></object>
  <object><name>badnum</name><bndbox><xmin>abc</xmin><ymin>1</ymin><xmax>9</xmax><ymax>9</ymax></bndbox></object>
  <object><name></name><bndbox><xmin>1</xmin><ymin>1</ymin><xmax>9</xmax><ymax>9</ymax></bndbox></object>
  <object><name>good</name><bndbox><xmin>1</xmin><ymin>2</ymin><xmax>9</xmax><ymax>10</ymax></bndbox></object>
</annotation>
`)
	mustWriteFixture(t, filepath.Join(xmlDir, "good.xml"), `<annotation>
  <filename>good.jpg</filename>
  <size><width>10</width><height>10</height></size>
</annotation>
`)

	ds, issues, err := ParseVOC(xmlDir, Options{})
	if err != nil {
		t.Fatalf("坏文件不应中断解析，返回错误：%v", err)
	}
	if len(ds.Images) != 2 {
		t.Fatalf("坏 xml 跳过后应剩 objects.jpg 与 good.jpg，实际 %+v", ds.Images)
	}
	if got, ok := findIssue(issues, "bad.xml", 0, ""); !ok {
		t.Fatalf("坏 XML 期望 Issue{File:bad.xml}，实际：%v", issueSummary(issues))
	} else if !strings.Contains(got.Message, "XML 解析失败") {
		t.Fatalf("坏 XML 的信息期望提到解析失败，实际 %q", got.Message)
	}
	if got, ok := findIssue(issues, "nosize.xml", 0, "size"); !ok || !strings.Contains(got.Message, "缺少 <size>") {
		t.Fatalf("缺 size 期望 Issue{Field:size, Message:缺少 <size>}，实际：%v", issueSummary(issues))
	}
	if got, ok := findIssue(issues, "zerowidth.xml", 0, "size"); !ok || !strings.Contains(got.Message, "<size> 无效") {
		t.Fatalf("width=0 期望 Issue{Field:size}，实际：%v", issueSummary(issues))
	}
	if got, ok := findIssue(issues, "noname.xml", 0, "filename"); !ok || !strings.Contains(got.Message, "缺少 <filename>") {
		t.Fatalf("缺 filename 期望 Issue{Field:filename}，实际：%v", issueSummary(issues))
	}
	if got, ok := findIssueIndex(issues, "objects.xml", 0, "bndbox"); !ok || !strings.Contains(got.Message, "既没有 robndbox 也没有 bndbox") {
		t.Fatalf("没有 box 的对象期望 Issue{Field:bndbox}，实际：%v", issueSummary(issues))
	}
	if got, ok := findIssueIndex(issues, "objects.xml", 1, "bndbox"); !ok || !strings.Contains(got.Message, "不是数字") {
		t.Fatalf("xmin=abc 期望 Issue{Index:1, Field:bndbox}，实际：%v", issueSummary(issues))
	}
	if got, ok := findIssueIndex(issues, "objects.xml", 2, "name"); !ok || !strings.Contains(got.Message, "缺少 <name>") {
		t.Fatalf("空 name 期望 Issue{Index:2, Field:name}，实际：%v", issueSummary(issues))
	}
	if len(issues) != 7 {
		t.Fatalf("期望共 7 条解析错误，实际 %d 条：%v", len(issues), issueSummary(issues))
	}

	obj := imageByFile(t, ds, "objects.jpg")
	if len(obj.Annotations) != 1 || obj.Annotations[0].Category != "good" {
		t.Fatalf("objects.jpg 只应留下 good 一个标注，实际 %+v", obj.Annotations)
	}
	for _, iss := range issues {
		if iss.File == "objects.xml" && iss.Line != 0 {
			t.Fatalf("VOC 的 Issue 用 Index 定位而非 Line，期望 Line=0，实际 %+v", iss)
		}
	}
}

func TestParseVOCDuplicateFilename(t *testing.T) {
	xmlDir := filepath.Join(t.TempDir(), "Annotations")
	doc := `<annotation>
  <filename>same.jpg</filename>
  <size><width>10</width><height>10</height></size>
</annotation>
`
	mustWriteFixture(t, filepath.Join(xmlDir, "a.xml"), doc)
	mustWriteFixture(t, filepath.Join(xmlDir, "b.xml"), doc)

	ds, issues, err := ParseVOC(xmlDir, Options{})
	if err != nil {
		t.Fatalf("ParseVOC 返回错误：%v", err)
	}
	if len(ds.Images) != 1 {
		t.Fatalf("两张 xml 声明同一图片时应只保留一张，实际 %+v", ds.Images)
	}
	if got, ok := findIssue(issues, "b.xml", 0, "filename"); !ok || !strings.Contains(got.Message, "已由前一个 xml 声明") {
		t.Fatalf("重复 filename 期望 Issue，实际：%v", issueSummary(issues))
	}
}

func TestParseVOCMissingDir(t *testing.T) {
	if _, _, err := ParseVOC(filepath.Join(t.TempDir(), "nope"), Options{}); err == nil {
		t.Fatalf("xmlDir 不存在时期望返回错误")
	}
}
