package ir

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func intPtr(v int) *int { return &v }

// roundTripProject is the fixture written and read back in the round-trip test.
func roundTripProject() *Project {
	return &Project{
		Name: "demo 数据集",
		Task: TaskMixed,
		Categories: []Category{
			{Key: "person", Index: 0, Name: "person", Color: "#ff0000"},
			{Key: "car", Index: 1, Name: "car", SuperCategory: "vehicle", COCOID: intPtr(7)},
		},
	}
}

func roundTripImages() []Image {
	return []Image{
		{
			File: "sub/a.jpg", Width: 640, Height: 480, SHA256: "abc",
			Annotations: []Annotation{
				{
					ID: "1", Category: "person", Kind: KindBBox, X: 1.5, Y: 2.5, W: 30, H: 40,
					Attributes: map[string]string{"difficult": "1", "iscrowd": "0"},
				},
				{
					ID: "2", Category: "car", Kind: KindPolygon,
					Rings: [][][2]float64{{{0, 0}, {10, 0}, {10, 10}}},
				},
				{
					ID: "3", Category: "car", Kind: KindRotated,
					Points: [][2]float64{{0, 0}, {10, 0}, {10, 5}, {0, 5}},
					CX:     5, CY: 2.5, RW: 10, RH: 5, Angle: 0,
				},
			},
		},
		{File: "b.png", Width: 100, Height: 80},
		{File: "deep/nested/c.jpg", Width: 10, Height: 10, Annotations: []Annotation{}},
	}
}

func TestWriteReadProjectRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := roundTripProject()
	p.IRVersion = "0.9" // must be overridden by Version
	images := roundTripImages()

	// Written twice so the Windows remove-then-rename path is exercised.
	for i := range 2 {
		if err := WriteProject(dir, p, images); err != nil {
			t.Fatalf("第 %d 次 WriteProject 失败: %v", i+1, err)
		}
	}

	for _, want := range []string{
		"project.json",
		filepath.Join("ir", "sub", "a.jpg.json"),
		filepath.Join("ir", "b.png.json"),
		filepath.Join("ir", "deep", "nested", "c.jpg.json"),
	} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("缺少 %s: %v", want, err)
		}
	}

	// ir_version is forced to Version.
	raw, err := os.ReadFile(filepath.Join(dir, "project.json"))
	if err != nil {
		t.Fatalf("读取 project.json 失败: %v", err)
	}
	var onDisk Project
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("project.json 解析失败: %v", err)
	}
	if onDisk.IRVersion != Version {
		t.Errorf("ir_version = %q, 期望 %q", onDisk.IRVersion, Version)
	}
	if !strings.Contains(string(raw), "\n  ") {
		t.Errorf("project.json 未使用两空格缩进:\n%s", raw)
	}

	gotP, gotImages, err := ReadProject(dir)
	if err != nil {
		t.Fatalf("ReadProject 失败: %v", err)
	}

	wantP := *p
	wantP.IRVersion = Version
	if !reflect.DeepEqual(gotP, &wantP) {
		t.Errorf("Project 往返不一致:\n got %+v\nwant %+v", gotP, wantP)
	}

	// Images come back sorted by File. A nil Annotations survives the round
	// trip as nil (JSON null) and an empty one as empty (JSON []).
	want := make([]Image, len(images))
	copy(want, images)
	want[0], want[1], want[2] = want[1], want[2], want[0] // b.png, deep/..., sub/a.jpg
	if !reflect.DeepEqual(gotImages, want) {
		t.Errorf("Images 往返不一致:\n got %+v\nwant %+v", gotImages, want)
	}

	// Attributes and the COCOID pointer survive the trip.
	if got := gotImages[2].Annotations[0].Attributes; got["difficult"] != "1" || got["iscrowd"] != "0" {
		t.Errorf("Attributes = %v, 期望保留 difficult/iscrowd", got)
	}
	if coco := gotP.Categories[1].COCOID; coco == nil || *coco != 7 {
		t.Errorf("COCOID = %v, 期望 7", coco)
	}
	if gotP.Categories[0].COCOID != nil {
		t.Errorf("未设置的 COCOID 应为 nil, 得到 %v", *gotP.Categories[0].COCOID)
	}
}

func TestWriteProjectErrors(t *testing.T) {
	p := roundTripProject()

	cases := []struct {
		name   string
		dir    string
		images []Image
	}{
		{"重复的 File", t.TempDir(), []Image{{File: "a.jpg"}, {File: "a.jpg"}}},
		{"File 为空", t.TempDir(), []Image{{File: ""}}},
		{"File 绝对路径", t.TempDir(), []Image{{File: "/etc/passwd"}}},
		{"File 越出项目根", t.TempDir(), []Image{{File: "../evil.jpg"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := WriteProject(tc.dir, p, tc.images); err == nil {
				t.Fatalf("期望 error, 得到 nil")
			}
		})
	}

	if err := WriteProject("", p, nil); err == nil {
		t.Error("空目录应报错")
	}
	if err := WriteProject(t.TempDir(), nil, nil); err == nil {
		t.Error("nil 项目应报错")
	}
}

func TestReadProjectNotExist(t *testing.T) {
	dir := t.TempDir()
	_, _, err := ReadProject(dir)
	if err == nil {
		t.Fatal("期望 error")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error 未包裹 fs.ErrNotExist: %v", err)
	}
}

func TestReadProjectCorruptImage(t *testing.T) {
	dir := t.TempDir()
	if err := WriteProject(dir, roundTripProject(), roundTripImages()); err != nil {
		t.Fatalf("WriteProject 失败: %v", err)
	}
	bad := filepath.Join(dir, "ir", "sub", "a.jpg.json")
	if err := os.WriteFile(bad, []byte("{ 不是 JSON"), 0o644); err != nil {
		t.Fatalf("写坏文件失败: %v", err)
	}
	_, _, err := ReadProject(dir)
	if err == nil {
		t.Fatal("期望 error")
	}
	if !strings.Contains(err.Error(), bad) {
		t.Fatalf("error 未包含完整路径 %q: %v", bad, err)
	}
}

func TestReadProjectCorruptProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "project.json"), []byte("nope"), 0o644); err != nil {
		t.Fatalf("写坏文件失败: %v", err)
	}
	_, _, err := ReadProject(dir)
	if err == nil {
		t.Fatal("期望 error")
	}
	if !strings.Contains(err.Error(), "project.json") {
		t.Fatalf("error 未包含路径: %v", err)
	}
}

func TestReadProjectVersion(t *testing.T) {
	cases := []struct {
		name    string
		version string
		wantErr bool
	}{
		{"当前版本", Version, false},
		{"同主版本", "1.7", false},
		{"主版本不同", "2.0", true},
		{"主版本不同 2", "0.9", true},
		{"缺失版本", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			doc := Project{IRVersion: tc.version, Task: TaskDet}
			raw, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("marshal 失败: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "project.json"), raw, 0o644); err != nil {
				t.Fatalf("写 project.json 失败: %v", err)
			}
			p, images, err := ReadProject(dir)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望版本不兼容 error")
				}
				if !strings.Contains(err.Error(), "版本不兼容") {
					t.Fatalf("error 未说明版本不兼容: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadProject 失败: %v", err)
			}
			if p.IRVersion != tc.version {
				t.Errorf("IRVersion = %q, 期望 %q", p.IRVersion, tc.version)
			}
			if len(images) != 0 {
				t.Errorf("无 ir 目录时应返回 0 张图, 得到 %d", len(images))
			}
		})
	}
}

// TestReadProjectDerivesFile checks the File fallback for documents written
// without one.
func TestReadProjectDerivesFile(t *testing.T) {
	dir := t.TempDir()
	p := &Project{Task: TaskDet}
	if err := WriteProject(dir, p, nil); err != nil {
		t.Fatalf("WriteProject 失败: %v", err)
	}
	doc := filepath.Join(dir, "ir", "sub", "lonely.jpg.json")
	if err := os.MkdirAll(filepath.Dir(doc), 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	if err := os.WriteFile(doc, []byte(`{"width":10,"height":10,"annotations":null}`), 0o644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	_, images, err := ReadProject(dir)
	if err != nil {
		t.Fatalf("ReadProject 失败: %v", err)
	}
	if len(images) != 1 || images[0].File != "sub/lonely.jpg" {
		t.Fatalf("images = %+v, 期望单张 sub/lonely.jpg", images)
	}
}
