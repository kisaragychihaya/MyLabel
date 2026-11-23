package app

import (
	"path/filepath"
	"strings"
	"testing"

	"mylabel/internal/exporter"
	"mylabel/internal/ir"
)

// TestExportProjectIRFields converts a session's categories and the three
// geometries into IR: keys, indices, sizes, relative path, hash and the
// per-kind fields of docs/phase3-design.md §4.1.
func TestExportProjectIRFields(t *testing.T) {
	rot := Ann{ID: "o1", Cat: 2, Kind: KindRect, CX: 100, CY: 120, W: 40, H: 20, Angle: 0.3}
	poly := newPolyAnn("p1", 1, []Point{{10, 10}, {40, 10}, {25, 40}})
	a := New()
	a.dir = filepath.Join("root", "ws")
	a.categories = []Category{{Name: "person"}, {Name: "person"}, {Name: "bike", Color: "#ff0000"}}
	a.images = []ImageInfo{{
		RelPath: "sub/a.png",
		Width:   640,
		Height:  480,
		SHA256:  "abc123",
		Anns: []Ann{
			{ID: "r1", Cat: 0, Kind: KindRect, CX: 50, CY: 40, W: 20, H: 10},
			rot, poly,
		},
	}}

	proj, images := a.exportProjectIR()
	if proj.IRVersion != ir.Version || proj.Task != ir.TaskMixed || proj.Name != "ws" {
		t.Errorf("project = %+v，期望版本 %s / 任务 mixed / 名称 ws", proj, ir.Version)
	}
	wantKeys := []string{"person", "person~2", "bike"}
	if len(proj.Categories) != 3 {
		t.Fatalf("类别 = %+v，期望 3 个", proj.Categories)
	}
	for i, want := range wantKeys {
		if proj.Categories[i].Key != want {
			t.Errorf("类别 %d key = %q，期望 %q", i, proj.Categories[i].Key, want)
		}
		if proj.Categories[i].Index != i {
			t.Errorf("类别 %d index = %d，期望 %d", i, proj.Categories[i].Index, i)
		}
	}
	if proj.Categories[2].Color != "#ff0000" {
		t.Errorf("类别颜色 = %q，期望 #ff0000", proj.Categories[2].Color)
	}

	if len(images) != 1 {
		t.Fatalf("图片 = %d 张，期望 1", len(images))
	}
	img := images[0]
	if img.File != "sub/a.png" || img.Width != 640 || img.Height != 480 || img.SHA256 != "abc123" {
		t.Errorf("图片 = %+v，期望 sub/a.png 640×480 hash abc123", img)
	}
	if len(img.Annotations) != 3 {
		t.Fatalf("标注 = %d 条，期望 3", len(img.Annotations))
	}

	box := img.Annotations[0]
	if box.Kind != ir.KindBBox || box.Category != "person" || box.ID != "r1" {
		t.Errorf("矩形 = %+v，期望 bbox/person/r1", box)
	}
	if !nearly(box.X, 40, 1e-9) || !nearly(box.Y, 35, 1e-9) || !nearly(box.W, 20, 1e-9) || !nearly(box.H, 10, 1e-9) {
		t.Errorf("矩形 = (%v,%v,%v,%v)，期望左上角 40,35 宽高 20,10", box.X, box.Y, box.W, box.H)
	}

	rotIR := img.Annotations[1]
	if rotIR.Kind != ir.KindRotated || rotIR.Category != "bike" {
		t.Errorf("旋转框 = %+v，期望 rotated_bbox/bike", rotIR)
	}
	if len(rotIR.Points) != 4 {
		t.Fatalf("旋转框顶点 = %d，期望 4", len(rotIR.Points))
	}
	want := corners(&rot)
	for i, p := range want {
		if !nearly(rotIR.Points[i][0], p.X, 1e-9) || !nearly(rotIR.Points[i][1], p.Y, 1e-9) {
			t.Errorf("顶点 %d = %v，期望 %v", i, rotIR.Points[i], p)
		}
	}
	if !nearly(rotIR.CX, 100, 1e-9) || !nearly(rotIR.RW, 40, 1e-9) || !nearly(rotIR.Angle, 0.3, 1e-9) {
		t.Errorf("旋转框缓存 = %+v，期望中心 100 宽 40 角度 0.3", rotIR)
	}

	polyIR := img.Annotations[2]
	if polyIR.Kind != ir.KindPolygon || polyIR.Category != "person~2" {
		t.Errorf("多边形 = %+v，期望 polygon/person~2", polyIR)
	}
	if len(polyIR.Rings) != 1 || len(polyIR.Rings[0]) != 3 {
		t.Fatalf("多边形环 = %v，期望单环 3 点", polyIR.Rings)
	}
	if !nearly(polyIR.Rings[0][2][0], 25, 1e-9) || !nearly(polyIR.Rings[0][2][1], 40, 1e-9) {
		t.Errorf("多边形第 3 点 = %v，期望 25,40", polyIR.Rings[0][2])
	}
}

// TestExportProjectIRCarriesStoreHash: the content hash recorded when the folder
// was scanned reaches the IR, so an export says which bytes it copied.
func TestExportProjectIRCarriesStoreHash(t *testing.T) {
	a := storeApp(t, "a.png")
	if a.images[0].SHA256 == "" {
		t.Fatalf("openDir 没有记录 sha256: %+v", a.images[0])
	}
	_, images := a.exportProjectIR()
	if images[0].SHA256 != a.images[0].SHA256 {
		t.Errorf("IR sha256 = %q，期望 %q", images[0].SHA256, a.images[0].SHA256)
	}
}

// TestInferExportTask covers the wizard's default task: polygons mean
// segmentation, else rotated boxes mean obb, else detection. A polygon wins even
// when a rotated box comes first.
func TestInferExportTask(t *testing.T) {
	cases := []struct {
		name string
		anns []Ann
		want exporter.Task
	}{
		{"空", nil, exporter.TaskDet},
		{"只有矩形", []Ann{{ID: "r", Kind: KindRect, W: 10, H: 10}}, exporter.TaskDet},
		{"有旋转框", []Ann{{ID: "r", Kind: KindRect, W: 10, H: 10}, {ID: "o", Kind: KindRect, W: 10, H: 10, Angle: 0.2}}, exporter.TaskObb},
		{"有多边形", []Ann{{ID: "p", Kind: KindPoly, Points: []Point{{0, 0}, {5, 0}, {0, 5}}}}, exporter.TaskSeg},
		{"旋转框与多边形并存", []Ann{{ID: "o", Kind: KindRect, Angle: 0.2}, {ID: "p", Kind: KindPoly, Points: []Point{{0, 0}, {5, 0}, {0, 5}}}}, exporter.TaskSeg},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := New()
			a.images = []ImageInfo{{RelPath: "a.png", Width: 100, Height: 100, Anns: tc.anns}}
			if got := a.inferExportTask(); got != tc.want {
				t.Errorf("inferExportTask = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestExportProjectIRRelativePaths: Windows-style relative paths are normalized
// the way the importer's are, so the export's copy source and split hint match
// the folder's spelling.
func TestExportProjectIRRelativePaths(t *testing.T) {
	a := New()
	a.dir = "ws"
	a.categories = []Category{{Name: "a"}}
	a.images = []ImageInfo{
		{RelPath: `train\one.png`, Width: 10, Height: 10},
		{RelPath: "./val/two.png", Width: 10, Height: 10},
	}
	_, images := a.exportProjectIR()
	if images[0].File != "train/one.png" {
		t.Errorf("File = %q，期望 train/one.png", images[0].File)
	}
	if images[1].File != "val/two.png" {
		t.Errorf("File = %q，期望 val/two.png", images[1].File)
	}
	if strings.Contains(images[0].File, `\`) {
		t.Errorf("File 仍是反斜杠: %q", images[0].File)
	}
}
