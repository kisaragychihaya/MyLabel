package importer

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"mylabel/internal/ir"
)

// --- shared test helpers (YOLO/VOC) ---

// mustWriteFixture writes content to path, creating parent directories.
func mustWriteFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录 %s 失败：%v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 %s 失败：%v", path, err)
	}
}

func nearlyEqual(got, want float64) bool {
	return math.Abs(got-want) < 1e-9
}

// imageByFile looks an image up by its relative path.
func imageByFile(t *testing.T, ds *Dataset, file string) ir.Image {
	t.Helper()
	for _, img := range ds.Images {
		if img.File == file {
			return img
		}
	}
	files := make([]string, 0, len(ds.Images))
	for _, img := range ds.Images {
		files = append(files, img.File)
	}
	t.Fatalf("Dataset 里没有图片 %q，实际有 %v", file, files)
	return ir.Image{}
}

// categoryByKey looks a category up by key.
func categoryByKey(t *testing.T, ds *Dataset, key string) ir.Category {
	t.Helper()
	for _, c := range ds.Categories {
		if c.Key == key {
			return c
		}
	}
	keys := make([]string, 0, len(ds.Categories))
	for _, c := range ds.Categories {
		keys = append(keys, c.Key)
	}
	t.Fatalf("Dataset 里没有类别 %q，实际有 %v", key, keys)
	return ir.Category{}
}

// issueSummary renders an Issue as "file:line:field:message" for readable
// assertion failures.
func issueSummary(iss []ir.Issue) []string {
	out := make([]string, 0, len(iss))
	for _, i := range iss {
		out = append(out, i.File+":"+itoa(i.Line)+":index="+itoa(i.Index)+":"+i.Field+":"+i.Message)
	}
	return out
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// findIssue returns the first issue matching file, line and field.
func findIssue(iss []ir.Issue, file string, line int, field string) (ir.Issue, bool) {
	for _, i := range iss {
		if i.File == file && i.Line == line && i.Field == field {
			return i, true
		}
	}
	return ir.Issue{}, false
}

// findIssueIndex returns the first issue matching file, annotation index and
// field; VOC locates object errors by Index instead of Line.
func findIssueIndex(iss []ir.Issue, file string, index int, field string) (ir.Issue, bool) {
	for _, i := range iss {
		if i.File == file && i.Index == index && i.Field == field {
			return i, true
		}
	}
	return ir.Issue{}, false
}

// --- YOLO ---

func TestParseYOLODet(t *testing.T) {
	root := t.TempDir()
	labels := filepath.Join(root, "labels")
	mustWriteFixture(t, filepath.Join(labels, "a.txt"), "0 0.5 0.5 0.4 0.2\n1 0.25 0.25 0.1 0.1\n")
	mustWriteFixture(t, filepath.Join(labels, "empty.txt"), "")
	mustWriteFixture(t, filepath.Join(labels, "sub", "b.txt"), "\r\n0 0.5 0.5 0.5 0.5\r\n\r\n")
	mustWriteFixture(t, filepath.Join(root, "data.yaml"), "path: .\nnc: 2\nnames: ['cat', 'dog']\n")

	dims := map[string][2]int{
		"a.jpg":     {640, 480},
		"empty.jpg": {32, 32},
		"sub/b.png": {200, 100},
	}
	ds, issues, err := ParseYOLO(labels, dims, Options{Task: "det"})
	if err != nil {
		t.Fatalf("ParseYOLO 返回错误：%v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("期望没有解析错误，实际：%v", issueSummary(issues))
	}
	if len(ds.Images) != 3 {
		t.Fatalf("期望 3 张图，实际 %d 张：%v", len(ds.Images), ds.Images)
	}
	if len(ds.Categories) != 2 {
		t.Fatalf("期望 2 个类别，实际 %d 个：%v", len(ds.Categories), ds.Categories)
	}
	if got := categoryByKey(t, ds, "cat"); got.Index != 0 || got.Name != "cat" {
		t.Fatalf("类别 cat 期望 Index=0 Name=cat，实际 %+v", got)
	}
	if got := categoryByKey(t, ds, "dog"); got.Index != 1 || got.Name != "dog" {
		t.Fatalf("类别 dog 期望 Index=1 Name=dog，实际 %+v", got)
	}

	img := imageByFile(t, ds, "a.jpg")
	if img.Width != 640 || img.Height != 480 {
		t.Fatalf("a.jpg 尺寸期望 640x480，实际 %dx%d", img.Width, img.Height)
	}
	if len(img.Annotations) != 2 {
		t.Fatalf("a.jpg 期望 2 个标注，实际 %d 个：%+v", len(img.Annotations), img.Annotations)
	}
	if got := img.Annotations[0]; got.Kind != ir.KindBBox || !nearlyEqual(got.X, 192) ||
		!nearlyEqual(got.Y, 192) || !nearlyEqual(got.W, 256) || !nearlyEqual(got.H, 96) {
		t.Fatalf("a.jpg 第 1 个标注期望 bbox (192,192,256,96)，实际 %+v", got)
	}
	if got := img.Annotations[0].Category; got != "cat" {
		t.Fatalf("a.jpg 第 1 个标注期望类别 cat，实际 %q", got)
	}
	if got := img.Annotations[1]; got.Kind != ir.KindBBox || !nearlyEqual(got.X, 128) ||
		!nearlyEqual(got.Y, 96) || !nearlyEqual(got.W, 64) || !nearlyEqual(got.H, 48) {
		t.Fatalf("a.jpg 第 2 个标注期望 bbox (128,96,64,48)，实际 %+v", got)
	}
	if got := img.Annotations[1].Category; got != "dog" {
		t.Fatalf("a.jpg 第 2 个标注期望类别 dog，实际 %q", got)
	}

	empty := imageByFile(t, ds, "empty.jpg")
	if empty.Width != 32 || empty.Height != 32 {
		t.Fatalf("empty.jpg 尺寸期望 32x32，实际 %dx%d", empty.Width, empty.Height)
	}
	if len(empty.Annotations) != 0 {
		t.Fatalf("空 txt 的图片不应有标注，实际 %+v", empty.Annotations)
	}

	sub := imageByFile(t, ds, "sub/b.png")
	if len(sub.Annotations) != 1 {
		t.Fatalf("sub/b.png 期望 1 个标注，实际 %d 个", len(sub.Annotations))
	}
	if got := sub.Annotations[0]; !nearlyEqual(got.X, 50) || !nearlyEqual(got.Y, 25) ||
		!nearlyEqual(got.W, 100) || !nearlyEqual(got.H, 50) {
		t.Fatalf("sub/b.png 标注期望 bbox (50,25,100,50)，实际 %+v", got)
	}
}

func TestParseYOLOSeg(t *testing.T) {
	labels := filepath.Join(t.TempDir(), "labels")
	mustWriteFixture(t, filepath.Join(labels, "a.txt"),
		"0 0.1 0.1 0.9 0.1 0.9 0.9 0.1 0.9\n1 0.2 0.2 0.8 0.2 0.8 0.8\n")

	ds, issues, err := ParseYOLO(labels, map[string][2]int{"a.jpg": {100, 200}}, Options{Task: "seg"})
	if err != nil {
		t.Fatalf("ParseYOLO 返回错误：%v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("Task=seg 时 9 字段应按多边形解析，期望没有错误，实际：%v", issueSummary(issues))
	}
	img := imageByFile(t, ds, "a.jpg")
	if len(img.Annotations) != 2 {
		t.Fatalf("期望 2 个标注，实际 %d 个", len(img.Annotations))
	}
	for i, want := range [][][2]float64{
		{{10, 20}, {90, 20}, {90, 180}, {10, 180}},
		{{20, 40}, {80, 40}, {80, 160}},
	} {
		got := img.Annotations[i]
		if got.Kind != ir.KindPolygon {
			t.Fatalf("第 %d 个标注期望 polygon，实际 %q", i+1, got.Kind)
		}
		if len(got.Rings) != 1 {
			t.Fatalf("第 %d 个标注期望 1 个环，实际 %d 个", i+1, len(got.Rings))
		}
		if len(got.Rings[0]) != len(want) {
			t.Fatalf("第 %d 个标注期望 %d 个点，实际 %d 个", i+1, len(want), len(got.Rings[0]))
		}
		for j := range want {
			if !nearlyEqual(got.Rings[0][j][0], want[j][0]) || !nearlyEqual(got.Rings[0][j][1], want[j][1]) {
				t.Fatalf("第 %d 个标注第 %d 个点期望 %v，实际 %v", i+1, j+1, want[j], got.Rings[0][j])
			}
		}
	}
	if len(ds.Categories) != 2 || ds.Categories[0].Key != "class_0" || ds.Categories[1].Key != "class_1" {
		t.Fatalf("无 data.yaml 时期望合成 class_0/class_1，实际 %+v", ds.Categories)
	}
}

func TestParseYOLOObb(t *testing.T) {
	labels := filepath.Join(t.TempDir(), "labels")
	mustWriteFixture(t, filepath.Join(labels, "a.txt"), "0 0.1 0.2 0.5 0.2 0.5 0.8 0.1 0.8\n")

	ds, issues, err := ParseYOLO(labels, map[string][2]int{"a.jpg": {100, 200}}, Options{Task: "obb"})
	if err != nil {
		t.Fatalf("ParseYOLO 返回错误：%v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("期望没有解析错误，实际：%v", issueSummary(issues))
	}
	ann := imageByFile(t, ds, "a.jpg").Annotations[0]
	if ann.Kind != ir.KindRotated {
		t.Fatalf("期望 rotated_bbox，实际 %q", ann.Kind)
	}
	if len(ann.Points) != 4 {
		t.Fatalf("期望 4 个顶点，实际 %d 个：%v", len(ann.Points), ann.Points)
	}
	want := [][2]float64{{10, 40}, {50, 40}, {50, 160}, {10, 160}}
	sort.Slice(ann.Points, func(i, j int) bool {
		if ann.Points[i][0] != ann.Points[j][0] {
			return ann.Points[i][0] < ann.Points[j][0]
		}
		return ann.Points[i][1] < ann.Points[j][1]
	})
	sort.Slice(want, func(i, j int) bool {
		if want[i][0] != want[j][0] {
			return want[i][0] < want[j][0]
		}
		return want[i][1] < want[j][1]
	})
	for i := range want {
		if !nearlyEqual(ann.Points[i][0], want[i][0]) || !nearlyEqual(ann.Points[i][1], want[i][1]) {
			t.Fatalf("第 %d 个顶点期望 %v，实际 %v", i+1, want[i], ann.Points[i])
		}
	}
	if !nearlyEqual(ann.CX, 30) || !nearlyEqual(ann.CY, 100) {
		t.Fatalf("期望中心 (30,100)，实际 (%v,%v)", ann.CX, ann.CY)
	}
	if !nearlyEqual(ann.RW, 40) || !nearlyEqual(ann.RH, 120) {
		t.Fatalf("期望 rw/rh = 40/120，实际 %v/%v", ann.RW, ann.RH)
	}
	if !nearlyEqual(ann.Angle, 0) {
		t.Fatalf("期望 angle=0，实际 %v", ann.Angle)
	}
}

// TestParseYOLOAutoTask covers the 9-field ambiguity under auto: the first
// data line decides (5 -> det, 9 -> obb, else seg).
func TestParseYOLOAutoTask(t *testing.T) {
	cases := []struct {
		name string
		line string
		want ir.Kind
	}{
		{"det", "0 0.5 0.5 0.2 0.2", ir.KindBBox},
		{"obb", "0 0.1 0.2 0.5 0.2 0.5 0.8 0.1 0.8", ir.KindRotated},
		{"seg", "0 0.1 0.1 0.9 0.1 0.9 0.9", ir.KindPolygon},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			labels := filepath.Join(t.TempDir(), "labels")
			mustWriteFixture(t, filepath.Join(labels, "a.txt"), tc.line+"\n")
			ds, issues, err := ParseYOLO(labels, map[string][2]int{"a.jpg": {100, 100}}, Options{})
			if err != nil {
				t.Fatalf("ParseYOLO 返回错误：%v", err)
			}
			if len(issues) != 0 {
				t.Fatalf("期望没有解析错误，实际：%v", issueSummary(issues))
			}
			ann := imageByFile(t, ds, "a.jpg").Annotations[0]
			if ann.Kind != tc.want {
				t.Fatalf("auto 首行 %q 期望 %q，实际 %q", tc.line, tc.want, ann.Kind)
			}
		})
	}
}

func TestParseYOLOIssues(t *testing.T) {
	t.Run("line", func(t *testing.T) {
		labels := filepath.Join(t.TempDir(), "labels")
		mustWriteFixture(t, filepath.Join(labels, "x.txt"), strings.Join([]string{
			"0 0.5 0.5 0.2 0.2",
			"abc 0.5 0.5 0.2 0.2",
			"0 0.5 0.5 0.2",
			"0 0.5 0.5 nan 0.2",
			"-1 0.5 0.5 0.2 0.2",
			"0 0.5 0.5",
		}, "\n")+"\n")
		mustWriteFixture(t, filepath.Join(labels, "orphan.txt"), "0 0.5 0.5 0.1 0.1\n")

		ds, issues, err := ParseYOLO(labels, map[string][2]int{"x.jpg": {100, 100}}, Options{Task: "det"})
		if err != nil {
			t.Fatalf("ParseYOLO 返回错误：%v", err)
		}
		if len(issues) != 6 {
			t.Fatalf("期望 6 条解析错误，实际 %d 条：%v", len(issues), issueSummary(issues))
		}
		if len(imageByFile(t, ds, "x.jpg").Annotations) != 1 {
			t.Fatalf("只有第 1 行合法，期望 1 个标注")
		}
		for _, img := range ds.Images {
			if img.File == "orphan.jpg" || strings.HasPrefix(img.File, "orphan") {
				t.Fatalf("没有 dims 的 txt 不应产出图片，实际 %+v", img)
			}
		}

		checks := []struct {
			line    int
			field   string
			message string
		}{
			{2, "class", "不是非负整数"},
			{3, "", "det 期望 5 字段，实际 4"},
			{4, "w", `字段 w="nan" 不是有效数字`},
			{5, "class", "不是非负整数"},
			{6, "", "det 期望 5 字段，实际 3"},
		}
		for _, c := range checks {
			got, ok := findIssue(issues, "x.txt", c.line, c.field)
			if !ok {
				t.Fatalf("没有找到 x.txt 第 %d 行 field=%q 的错误：%v", c.line, c.field, issueSummary(issues))
			}
			if !strings.Contains(got.Message, c.message) {
				t.Fatalf("x.txt 第 %d 行错误信息期望包含 %q，实际 %q", c.line, c.message, got.Message)
			}
		}
		if got, ok := findIssue(issues, "orphan.txt", 0, ""); !ok {
			t.Fatalf("没有找到 orphan.txt 缺 dims 的错误：%v", issueSummary(issues))
		} else if !strings.Contains(got.Message, "dims") {
			t.Fatalf("缺 dims 的错误信息应提到 dims，实际 %q", got.Message)
		}
	})

	t.Run("class out of names range", func(t *testing.T) {
		root := t.TempDir()
		labels := filepath.Join(root, "labels")
		mustWriteFixture(t, filepath.Join(labels, "x.txt"), "5 0.5 0.5 0.2 0.2\n")
		mustWriteFixture(t, filepath.Join(root, "data.yaml"), "names:\n  0: cat\n  1: dog\n")

		_, issues, err := ParseYOLO(labels, map[string][2]int{"x.jpg": {100, 100}}, Options{Task: "det"})
		if err != nil {
			t.Fatalf("ParseYOLO 返回错误：%v", err)
		}
		got, ok := findIssue(issues, "x.txt", 1, "class")
		if !ok {
			t.Fatalf("期望类别超范围错误，实际：%v", issueSummary(issues))
		}
		if !strings.Contains(got.Message, "超出 names 范围（0..1）") {
			t.Fatalf("错误信息期望提到 names 范围，实际 %q", got.Message)
		}
	})
}

func TestParseYOLOSynthesizeNames(t *testing.T) {
	labels := filepath.Join(t.TempDir(), "labels")
	mustWriteFixture(t, filepath.Join(labels, "a.txt"), "2 0.5 0.5 0.2 0.2\n0 0.5 0.5 0.2 0.2\n")

	ds, issues, err := ParseYOLO(labels, map[string][2]int{"a.jpg": {100, 100}}, Options{Task: "det"})
	if err != nil {
		t.Fatalf("ParseYOLO 返回错误：%v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("期望没有解析错误，实际：%v", issueSummary(issues))
	}
	if len(ds.Categories) != 3 {
		t.Fatalf("无 names 时应按最大 id 合成 class_0..class_2，实际 %+v", ds.Categories)
	}
	for i, c := range ds.Categories {
		want := "class_" + itoa(i)
		if c.Key != want || c.Name != want || c.Index != i {
			t.Fatalf("第 %d 个类别期望 key/name=%q index=%d，实际 %+v", i, want, i, c)
		}
	}
	anns := imageByFile(t, ds, "a.jpg").Annotations
	if len(anns) != 2 || anns[0].Category != "class_2" || anns[1].Category != "class_0" {
		t.Fatalf("标注类别映射错误：%+v", anns)
	}
}

func TestParseYOLOBadOptions(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := ParseYOLO(filepath.Join(dir, "nope"), nil, Options{}); err == nil {
		t.Fatalf("labelsDir 不存在时期望返回错误")
	}
	if _, _, err := ParseYOLO(dir, nil, Options{Task: "bogus"}); err == nil {
		t.Fatalf("未知 task 期望返回错误")
	}
}

func TestDataNamesParse(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    map[int]string
	}{
		{
			name:    "inline single quotes",
			content: "path: ../data\nnc: 2\nnames: ['cat', 'dog']\n",
			want:    map[int]string{0: "cat", 1: "dog"},
		},
		{
			name:    "inline double quotes with spaces",
			content: "names: [\"traffic light\", \"car\"]\n",
			want:    map[int]string{0: "traffic light", 1: "car"},
		},
		{
			name:    "inline bare",
			content: "names: [a, b , c]\n",
			want:    map[int]string{0: "a", 1: "b", 2: "c"},
		},
		{
			name:    "block mapping with quotes and comment",
			content: "nc: 2\nnames:\n  0: person\n  1: 'car'  # 车\nnc2: 9\n",
			want:    map[int]string{0: "person", 1: "car"},
		},
		{
			name:    "block sequence",
			content: "names:\n  - a\n  - b\n",
			want:    map[int]string{0: "a", 1: "b"},
		},
		{
			name:    "crlf block mapping",
			content: "names:\r\n  0: a\r\n  1: b\r\n",
			want:    map[int]string{0: "a", 1: "b"},
		},
		{
			name:    "no names key",
			content: "path: .\nnc: 3\n",
			want:    nil,
		},
		{
			name:    "unrecognized inline scalar",
			content: "names: person\n",
			want:    nil,
		},
		{
			name:    "commented out",
			content: "# names: [a, b]\nnc: 2\n",
			want:    nil,
		},
		{
			name:    "empty",
			content: "",
			want:    nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseYOLONames(tc.content)
			if len(got) != len(tc.want) {
				t.Fatalf("期望 %v，实际 %v", tc.want, got)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("id %d 期望 %q，实际 %q（整体 %v）", k, v, got[k], got)
				}
			}
		})
	}
}

func TestDataNamesFind(t *testing.T) {
	root := t.TempDir()
	mustWriteFixture(t, filepath.Join(root, "dataset", "data.yaml"), "names: [x]\n")
	deep := filepath.Join(root, "dataset", "labels", "train")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("创建目录失败：%v", err)
	}
	if got, want := findDataYAML(deep), filepath.Join(root, "dataset", "data.yaml"); got != want {
		t.Fatalf("向上查找 data.yaml 期望 %s，实际 %q", want, got)
	}
	if got := yoloNamesFor(deep); got[0] != "x" {
		t.Fatalf("期望 names[0]=x，实际 %v", got)
	}
	if got := findDataYAML(filepath.Join(root, "absent")); got != "" {
		t.Fatalf("没有 data.yaml 时期望空串，实际 %q", got)
	}
	// data.yml is accepted as well.
	mustWriteFixture(t, filepath.Join(root, "other", "data.yml"), "names:\n  0: y\n")
	if got := yoloNamesFor(filepath.Join(root, "other")); got[0] != "y" {
		t.Fatalf("期望从 data.yml 读到 names[0]=y，实际 %v", got)
	}
}

// datasetFiles lists a Dataset's image paths, sorted, for order-insensitive
// assertions.
func datasetFiles(ds *Dataset) []string {
	out := make([]string, 0, len(ds.Images))
	for _, img := range ds.Images {
		out = append(out, img.File)
	}
	sort.Strings(out)
	return out
}

// TestParseYOLODatasetRoot covers the Ultralytics layout the user hit: a
// dataset root with images/{train,val} + labels/{train,val} + coco8.yaml.
// Passing the root or the labels dir must behave identically, and the matched
// Image.File must be the real dims key so the app can map it to its own path.
func TestParseYOLODatasetRoot(t *testing.T) {
	root := t.TempDir()
	mustWriteFixture(t, filepath.Join(root, "coco8.yaml"),
		"path: .\ntrain: images/train\nval: images/val\nnames:\n  0: person\n  1: bicycle\n")
	mustWriteFixture(t, filepath.Join(root, "labels", "train", "a.txt"), "0 0.5 0.5 0.2 0.2\n")
	mustWriteFixture(t, filepath.Join(root, "labels", "val", "b.txt"), "1 0.5 0.5 0.1 0.1\n")
	dims := map[string][2]int{
		"images/train/a.jpg": {640, 480},
		"images/val/b.jpg":   {320, 240},
	}

	for _, tc := range []struct {
		name string
		dir  string
	}{
		{"dataset root", root},
		{"labels dir", filepath.Join(root, "labels")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds, issues, err := ParseYOLO(tc.dir, dims, Options{})
			if err != nil {
				t.Fatalf("ParseYOLO 返回错误：%v", err)
			}
			if len(issues) != 0 {
				t.Fatalf("期望没有解析错误，实际：%v", issueSummary(issues))
			}
			want := []string{"images/train/a.jpg", "images/val/b.jpg"}
			if got := datasetFiles(ds); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
				t.Fatalf("期望镜像匹配到 %v，实际 %v", want, got)
			}
			if len(ds.Categories) != 2 {
				t.Fatalf("期望 2 个类别（来自 coco8.yaml），实际 %+v", ds.Categories)
			}
			if got := categoryByKey(t, ds, "person"); got.Index != 0 {
				t.Fatalf("person 期望 Index=0，实际 %+v", got)
			}
			if got := categoryByKey(t, ds, "bicycle"); got.Index != 1 {
				t.Fatalf("bicycle 期望 Index=1，实际 %+v", got)
			}

			train := imageByFile(t, ds, "images/train/a.jpg")
			if train.Width != 640 || train.Height != 480 {
				t.Fatalf("images/train/a.jpg 尺寸期望 640x480，实际 %dx%d", train.Width, train.Height)
			}
			if len(train.Annotations) != 1 {
				t.Fatalf("期望 1 个标注，实际 %+v", train.Annotations)
			}
			ann := train.Annotations[0]
			if ann.Category != "person" || !nearlyEqual(ann.X, 256) || !nearlyEqual(ann.Y, 192) ||
				!nearlyEqual(ann.W, 128) || !nearlyEqual(ann.H, 96) {
				t.Fatalf("images/train/a.jpg 标注期望 person bbox (256,192,128,96)，实际 %+v", ann)
			}
			val := imageByFile(t, ds, "images/val/b.jpg")
			if got := val.Annotations[0]; got.Category != "bicycle" || !nearlyEqual(got.X, 144) ||
				!nearlyEqual(got.Y, 108) || !nearlyEqual(got.W, 32) || !nearlyEqual(got.H, 24) {
				t.Fatalf("images/val/b.jpg 标注期望 bicycle bbox (144,108,32,24)，实际 %+v", got)
			}
		})
	}
}

// TestParseYOLOMirrorPriority pins the match order: a txt's own path wins over
// its images/ mirror, and the mirror still resolves txts that only match that
// way.
func TestParseYOLOMirrorPriority(t *testing.T) {
	root := t.TempDir()
	mustWriteFixture(t, filepath.Join(root, "labels", "c.txt"), "0 0.5 0.5 0.2 0.2\n")
	mustWriteFixture(t, filepath.Join(root, "labels", "train", "d.txt"), "0 0.5 0.5 0.2 0.2\n")
	dims := map[string][2]int{
		"c.jpg":              {10, 10}, // rule 1: the txt's own path
		"images/c.jpg":       {20, 20}, // mirror candidate, must lose
		"images/train/d.jpg": {30, 40},
	}

	ds, issues, err := ParseYOLO(root, dims, Options{})
	if err != nil {
		t.Fatalf("ParseYOLO 返回错误：%v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("期望没有解析错误，实际：%v", issueSummary(issues))
	}
	got := datasetFiles(ds)
	want := []string{"c.jpg", "images/train/d.jpg"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("期望匹配 %v，实际 %v", want, got)
	}
	if img := imageByFile(t, ds, "c.jpg"); img.Width != 10 || img.Height != 10 {
		t.Fatalf("同 stem 规则应优先于镜像规则，期望 10x10，实际 %dx%d", img.Width, img.Height)
	}
	if img := imageByFile(t, ds, "images/train/d.jpg"); img.Width != 30 || img.Height != 40 {
		t.Fatalf("镜像规则应命中 images/train/d.jpg 30x40，实际 %dx%d", img.Width, img.Height)
	}
}

// TestParseYOLOLabelsDirResolution covers the labels/ subdirectory hunt.
func TestParseYOLOLabelsDirResolution(t *testing.T) {
	if _, _, err := ParseYOLO(filepath.Join(t.TempDir(), "nope"), nil, Options{}); err == nil {
		t.Fatalf("目录不存在时期望返回错误")
	}

	root := t.TempDir()
	mustWriteFixture(t, filepath.Join(root, "a", "labels", "x.txt"), "0 0.5 0.5 0.2 0.2\n")
	mustWriteFixture(t, filepath.Join(root, "b", "c", "labels", "y.txt"), "0 0.5 0.5 0.2 0.2\n")
	mustWriteFixture(t, filepath.Join(root, "data.yaml"), "names: [thing]\n")

	if got, want := findLabelsDir(root), filepath.Join(root, "a", "labels"); got != want {
		t.Fatalf("多层 labels 应取最短路径 %s，实际 %q", want, got)
	}
	if got := findLabelsDir(filepath.Join(root, "b", "c", "labels")); got != "" {
		t.Fatalf("labels 目录本身不应再下钻，实际 %q", got)
	}

	// A deeper labels dir that sorts before a shallow one must still lose.
	deep := t.TempDir()
	mustWriteFixture(t, filepath.Join(deep, "a", "deeper", "labels", "y.txt"), "0 0.5 0.5 0.2 0.2\n")
	mustWriteFixture(t, filepath.Join(deep, "z", "labels", "x.txt"), "0 0.5 0.5 0.2 0.2\n")
	if got, want := findLabelsDir(deep), filepath.Join(deep, "z", "labels"); got != want {
		t.Fatalf("应取路径最短的 labels（%s），实际 %q", want, got)
	}

	// The nested layout still matches through the mirror rule.
	dims := map[string][2]int{"a/images/x.jpg": {100, 50}}
	ds, issues, err := ParseYOLO(root, dims, Options{})
	if err != nil {
		t.Fatalf("ParseYOLO 返回错误：%v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("期望没有解析错误，实际：%v", issueSummary(issues))
	}
	// Only the shallowest labels dir is read.
	if got := datasetFiles(ds); len(got) != 1 || got[0] != "a/images/x.jpg" {
		t.Fatalf("期望只解析 a/labels 下的 txt，实际 %v", got)
	}
	if got := imageByFile(t, ds, "a/images/x.jpg"); len(got.Annotations) != 1 {
		t.Fatalf("期望 1 个标注，实际 %+v", got.Annotations)
	}
}

// TestParseYOLOYAMLDiscovery pins which yaml supplies the class names.
func TestParseYOLOYAMLDiscovery(t *testing.T) {
	t.Run("data.yaml beats a sibling yaml", func(t *testing.T) {
		root := t.TempDir()
		mustWriteFixture(t, filepath.Join(root, "data.yaml"), "names: [alpha]\n")
		mustWriteFixture(t, filepath.Join(root, "coco8.yaml"), "names: [beta]\n")
		mustWriteFixture(t, filepath.Join(root, "labels", "a.txt"), "0 0.5 0.5 0.2 0.2\n")
		ds, issues, err := ParseYOLO(root, map[string][2]int{"images/a.jpg": {10, 10}}, Options{})
		if err != nil {
			t.Fatalf("ParseYOLO 返回错误：%v", err)
		}
		if len(issues) != 0 {
			t.Fatalf("期望没有解析错误，实际：%v", issueSummary(issues))
		}
		if len(ds.Categories) != 1 || ds.Categories[0].Key != "alpha" {
			t.Fatalf("data.yaml 应优先于同级 coco8.yaml，期望类别 alpha，实际 %+v", ds.Categories)
		}
	})

	t.Run("several yaml files are ambiguous", func(t *testing.T) {
		root := t.TempDir()
		mustWriteFixture(t, filepath.Join(root, "a.yaml"), "names: [alpha]\n")
		mustWriteFixture(t, filepath.Join(root, "b.yml"), "names: [beta]\n")
		mustWriteFixture(t, filepath.Join(root, "labels", "a.txt"), "0 0.5 0.5 0.2 0.2\n")
		if got := yoloNamesFor(filepath.Join(root, "labels")); got != nil {
			t.Fatalf("同级多个 yaml 应放弃 names，实际 %v", got)
		}
		ds, issues, err := ParseYOLO(root, map[string][2]int{"images/a.jpg": {10, 10}}, Options{})
		if err != nil {
			t.Fatalf("ParseYOLO 返回错误：%v", err)
		}
		if len(issues) != 0 {
			t.Fatalf("歧义只应退化为合成类别，不应报错，实际：%v", issueSummary(issues))
		}
		if len(ds.Categories) != 1 || ds.Categories[0].Key != "class_0" {
			t.Fatalf("歧义时期望合成 class_0，实际 %+v", ds.Categories)
		}
	})

	t.Run("data.yaml without names stops the search", func(t *testing.T) {
		root := t.TempDir()
		mustWriteFixture(t, filepath.Join(root, "data.yaml"), "path: .\nnc: 1\n")
		mustWriteFixture(t, filepath.Join(root, "other.yaml"), "names: [alpha]\n")
		mustWriteFixture(t, filepath.Join(root, "labels", "a.txt"), "0 0.5 0.5 0.2 0.2\n")
		if got := yoloNamesFor(filepath.Join(root, "labels")); got != nil {
			t.Fatalf("最近的 data.yaml 没有 names 时应停止查找，实际 %v", got)
		}
	})

	t.Run("labels dir inside a dataset with a nested descriptor", func(t *testing.T) {
		root := t.TempDir()
		mustWriteFixture(t, filepath.Join(root, "ds", "my-dataset.yml"), "names:\n  0: person\n")
		mustWriteFixture(t, filepath.Join(root, "ds", "labels", "a.txt"), "0 0.5 0.5 0.2 0.2\n")
		if got := yoloNamesFor(filepath.Join(root, "ds", "labels")); got[0] != "person" {
			t.Fatalf("期望从唯一 yaml 读到 names[0]=person，实际 %v", got)
		}
	})
}

// TestParseYOLOMirrorNoFalseMatch ensures the mirror rule does not invent a
// match when no image exists for the txt.
func TestParseYOLOMirrorNoFalseMatch(t *testing.T) {
	root := t.TempDir()
	mustWriteFixture(t, filepath.Join(root, "labels", "train", "ghost.txt"), "0 0.5 0.5 0.2 0.2\n")
	mustWriteFixture(t, filepath.Join(root, "labels", "train", "real.txt"), "0 0.5 0.5 0.2 0.2\n")

	ds, issues, err := ParseYOLO(root, map[string][2]int{"images/train/real.jpg": {10, 10}}, Options{})
	if err != nil {
		t.Fatalf("ParseYOLO 返回错误：%v", err)
	}
	if got := datasetFiles(ds); len(got) != 1 || got[0] != "images/train/real.jpg" {
		t.Fatalf("期望只产出 images/train/real.jpg，实际 %v", got)
	}
	got, ok := findIssue(issues, "train/ghost.txt", 0, "")
	if !ok || !strings.Contains(got.Message, "找不到对应的图片尺寸") {
		t.Fatalf("没有对应图片的 txt 期望 Issue，实际：%v", issueSummary(issues))
	}
	if len(issues) != 1 {
		t.Fatalf("期望 1 条错误，实际 %d 条：%v", len(issues), issueSummary(issues))
	}
}

// TestParseYOLOIgnoredNonTxt guards against picking up stray files.
func TestParseYOLOIgnoredNonTxt(t *testing.T) {
	root := t.TempDir()
	mustWriteFixture(t, filepath.Join(root, "labels", "a.txt"), "0 0.5 0.5 0.2 0.2\n")
	mustWriteFixture(t, filepath.Join(root, "labels", "notes.md"), "not a label\n")
	mustWriteFixture(t, filepath.Join(root, "labels", "classes.txt.bak"), "0 0 0 0\n")
	ds, issues, err := ParseYOLO(root, map[string][2]int{"images/a.jpg": {10, 10}}, Options{})
	if err != nil {
		t.Fatalf("ParseYOLO 返回错误：%v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("非 .txt 文件应被忽略，实际：%v", issueSummary(issues))
	}
	if len(ds.Images) != 1 {
		t.Fatalf("期望 1 张图，实际 %v", datasetFiles(ds))
	}
}

// TestParseYOLOSuffixPrefixedDims covers dims keyed from a parent of the
// dataset directory (the app opened a folder above the dataset). The suffix
// rule matches only when exactly one dims entry can be meant.
func TestParseYOLOSuffixPrefixedDims(t *testing.T) {
	t.Run("unique suffix matches", func(t *testing.T) {
		root := t.TempDir()
		mustWriteFixture(t, filepath.Join(root, "labels", "train", "a.txt"), "0 0.5 0.5 0.2 0.2\n")
		dims := map[string][2]int{"coco8/images/train/a.jpg": {640, 480}}
		ds, issues, err := ParseYOLO(root, dims, Options{})
		if err != nil {
			t.Fatalf("ParseYOLO 返回错误：%v", err)
		}
		if len(issues) != 0 {
			t.Fatalf("唯一后缀应命中，实际：%v", issueSummary(issues))
		}
		img := imageByFile(t, ds, "coco8/images/train/a.jpg")
		if img.Width != 640 || len(img.Annotations) != 1 {
			t.Fatalf("期望命中 640 宽的图片且 1 个标注，实际 %+v", img)
		}
	})

	t.Run("case differences still match", func(t *testing.T) {
		root := t.TempDir()
		mustWriteFixture(t, filepath.Join(root, "labels", "train", "a.txt"), "0 0.5 0.5 0.2 0.2\n")
		dims := map[string][2]int{"Images/Train/A.JPG": {800, 600}}
		ds, issues, err := ParseYOLO(root, dims, Options{})
		if err != nil {
			t.Fatalf("ParseYOLO 返回错误：%v", err)
		}
		if len(issues) != 0 {
			t.Fatalf("大小写差异应命中（Windows 不区分大小写），实际：%v", issueSummary(issues))
		}
		if img := imageByFile(t, ds, "Images/Train/A.JPG"); img.Width != 800 {
			t.Fatalf("期望命中 800 宽的图片，实际 %+v", img)
		}
	})

	t.Run("ambiguous suffix is not guessed", func(t *testing.T) {
		root := t.TempDir()
		mustWriteFixture(t, filepath.Join(root, "labels", "train", "a.txt"), "0 0.5 0.5 0.2 0.2\n")
		dims := map[string][2]int{
			"coco8/images/train/a.jpg": {640, 480},
			"other/images/train/a.jpg": {320, 240},
		}
		ds, issues, err := ParseYOLO(root, dims, Options{})
		if err != nil {
			t.Fatalf("ParseYOLO 返回错误：%v", err)
		}
		if len(ds.Images) != 0 {
			t.Fatalf("歧义后缀不应猜测匹配，实际 %v", datasetFiles(ds))
		}
		if got, ok := findIssue(issues, "train/a.txt", 0, ""); !ok ||
			!strings.Contains(got.Message, "找不到对应的图片尺寸") {
			t.Fatalf("歧义后缀期望 Issue，实际：%v", issueSummary(issues))
		}
	})
}

// TestParseYOLOBasenameFallback covers a workspace opened inside the image
// tree (coco8/images/train), where dims keys are bare file names while the
// import still starts at the dataset root.
func TestParseYOLOBasenameFallback(t *testing.T) {
	t.Run("bare file names", func(t *testing.T) {
		root := t.TempDir()
		mustWriteFixture(t, filepath.Join(root, "labels", "train", "000000000009.txt"), "0 0.5 0.5 0.2 0.2\n")
		mustWriteFixture(t, filepath.Join(root, "labels", "val", "000000000061.txt"), "0 0.5 0.5 0.2 0.2\n")
		dims := map[string][2]int{"000000000009.jpg": {640, 480}}

		ds, issues, err := ParseYOLO(root, dims, Options{})
		if err != nil {
			t.Fatalf("ParseYOLO 返回错误：%v", err)
		}
		if got := datasetFiles(ds); len(got) != 1 || got[0] != "000000000009.jpg" {
			t.Fatalf("基名兜底应只命中工作区里的 000000000009.jpg，实际 %v", got)
		}
		img := imageByFile(t, ds, "000000000009.jpg")
		if img.Width != 640 || img.Height != 480 || len(img.Annotations) != 1 {
			t.Fatalf("期望 640x480 且 1 个标注，实际 %+v", img)
		}
		// The val image is not in the workspace: that txt must report an Issue.
		if got, ok := findIssue(issues, "val/000000000061.txt", 0, ""); !ok ||
			!strings.Contains(got.Message, "找不到对应的图片尺寸") {
			t.Fatalf("工作区外的图片期望 Issue，实际：%v", issueSummary(issues))
		}
		if len(issues) != 1 {
			t.Fatalf("期望 1 条错误，实际 %d 条：%v", len(issues), issueSummary(issues))
		}
	})

	t.Run("duplicate basenames are ambiguous", func(t *testing.T) {
		root := t.TempDir()
		mustWriteFixture(t, filepath.Join(root, "labels", "train", "000000000009.txt"), "0 0.5 0.5 0.2 0.2\n")
		dims := map[string][2]int{
			"setA/000000000009.jpg": {640, 480},
			"setB/000000000009.jpg": {320, 240},
		}
		ds, issues, err := ParseYOLO(root, dims, Options{})
		if err != nil {
			t.Fatalf("ParseYOLO 返回错误：%v", err)
		}
		if len(ds.Images) != 0 {
			t.Fatalf("同名图片多于一个时不应猜测，实际 %v", datasetFiles(ds))
		}
		if got, ok := findIssue(issues, "train/000000000009.txt", 0, ""); !ok ||
			!strings.Contains(got.Message, "找不到对应的图片尺寸") {
			t.Fatalf("歧义基名期望 Issue，实际：%v", issueSummary(issues))
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		root := t.TempDir()
		mustWriteFixture(t, filepath.Join(root, "labels", "train", "IMG_0001.txt"), "0 0.5 0.5 0.2 0.2\n")
		dims := map[string][2]int{"img_0001.JPG": {800, 600}}
		ds, issues, err := ParseYOLO(root, dims, Options{})
		if err != nil {
			t.Fatalf("ParseYOLO 返回错误：%v", err)
		}
		if len(issues) != 0 {
			t.Fatalf("基名匹配应不区分大小写，实际：%v", issueSummary(issues))
		}
		if img := imageByFile(t, ds, "img_0001.JPG"); img.Width != 800 {
			t.Fatalf("期望命中 img_0001.JPG，实际 %v", datasetFiles(ds))
		}
	})

	t.Run("exact path still wins over basename", func(t *testing.T) {
		root := t.TempDir()
		mustWriteFixture(t, filepath.Join(root, "labels", "train", "a.txt"), "0 0.5 0.5 0.2 0.2\n")
		dims := map[string][2]int{
			"train/a.jpg": {100, 100},
			"b/a.jpg":     {200, 200},
		}
		ds, issues, err := ParseYOLO(root, dims, Options{})
		if err != nil {
			t.Fatalf("ParseYOLO 返回错误：%v", err)
		}
		if len(issues) != 0 {
			t.Fatalf("精确 stem 应命中，实际：%v", issueSummary(issues))
		}
		if img := imageByFile(t, ds, "train/a.jpg"); img.Width != 100 {
			t.Fatalf("规则 1 应优先于基名兜底，实际 %v", datasetFiles(ds))
		}
	})
}
