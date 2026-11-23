package ir

import (
	"math"
	"strings"
	"testing"
)

// sampleProject is a small category table shared by the validate tests.
func sampleProject() *Project {
	return &Project{
		IRVersion:  Version,
		Name:       "demo",
		Task:       TaskMixed,
		Categories: []Category{{Key: "cat", Name: "cat"}, {Key: "dog", Name: "dog"}},
	}
}

func TestValidate(t *testing.T) {
	p := sampleProject()

	cases := []struct {
		name    string
		img     Image
		want    int      // expected issue count
		fields  []string // expected Issue.Field values, in order
		message string   // substring expected in Issue[0].Message (when want == 1)
	}{
		{
			name: "有效样本无 Issue",
			img: Image{
				File: "a.jpg", Width: 640, Height: 480,
				Annotations: []Annotation{
					{ID: "1", Category: "cat", Kind: KindBBox, X: 10, Y: 20, W: 30, H: 40},
					{ID: "2", Category: "dog", Kind: KindPolygon, Rings: [][][2]float64{{{0, 0}, {10, 0}, {10, 10}}}},
					{ID: "3", Category: "cat", Kind: KindRotated, Points: [][2]float64{{0, 0}, {10, 0}, {10, 5}, {0, 5}}},
				},
			},
			want: 0,
		},
		{
			name: "只有派生参数的旋转框合法",
			img: Image{
				File: "a.jpg", Width: 640, Height: 480,
				Annotations: []Annotation{
					{ID: "1", Category: "cat", Kind: KindRotated, CX: 100, CY: 100, RW: 40, RH: 20, Angle: 0.5},
				},
			},
			want: 0,
		},
		{
			name: "旋转框既无点也无参数",
			img: Image{
				File: "a.jpg", Width: 640, Height: 480,
				Annotations: []Annotation{{ID: "1", Category: "cat", Kind: KindRotated}},
			},
			want:    1,
			fields:  []string{"points"},
			message: "旋转框既没有 points",
		},
		{
			name: "旋转框点数不是 4",
			img: Image{
				File: "a.jpg", Width: 640, Height: 480,
				Annotations: []Annotation{{
					ID: "1", Category: "cat", Kind: KindRotated,
					Points: [][2]float64{{0, 0}, {10, 0}, {10, 5}},
				}},
			},
			want:    1,
			fields:  []string{"points"},
			message: "实际 3 个",
		},
		{
			name: "多边形外环点数不足",
			img: Image{
				File: "a.jpg", Width: 640, Height: 480,
				Annotations: []Annotation{{
					ID: "1", Category: "cat", Kind: KindPolygon,
					Rings: [][][2]float64{{{0, 0}, {10, 10}}},
				}},
			},
			want:    1,
			fields:  []string{"rings"},
			message: "至少需要 3 个",
		},
		{
			name: "多边形缺少外环",
			img: Image{
				File: "a.jpg", Width: 640, Height: 480,
				Annotations: []Annotation{{ID: "1", Category: "cat", Kind: KindPolygon}},
			},
			want:    1,
			fields:  []string{"rings"},
			message: "缺少外环",
		},
		{
			name: "bbox 宽度为 0",
			img: Image{
				File: "a.jpg", Width: 640, Height: 480,
				Annotations: []Annotation{{ID: "1", Category: "cat", Kind: KindBBox, X: 1, Y: 2, W: 0, H: 5}},
			},
			want:    1,
			fields:  []string{"w"},
			message: "宽度为 0",
		},
		{
			name: "bbox 高度为 0",
			img: Image{
				File: "a.jpg", Width: 640, Height: 480,
				Annotations: []Annotation{{ID: "1", Category: "cat", Kind: KindBBox, X: 1, Y: 2, W: 5, H: 0}},
			},
			want:    1,
			fields:  []string{"h"},
			message: "高度为 0",
		},
		{
			name: "未知类别 key",
			img: Image{
				File: "a.jpg", Width: 640, Height: 480,
				Annotations: []Annotation{{ID: "1", Category: "bird", Kind: KindBBox, X: 1, Y: 2, W: 5, H: 6}},
			},
			want:    1,
			fields:  []string{"category"},
			message: `"bird"`,
		},
		{
			name: "bbox 坐标为 NaN",
			img: Image{
				File: "a.jpg", Width: 640, Height: 480,
				Annotations: []Annotation{{ID: "1", Category: "cat", Kind: KindBBox, X: math.NaN(), Y: 2, W: 5, H: 6}},
			},
			want:    1,
			fields:  []string{"x"},
			message: "NaN",
		},
		{
			name: "多边形坐标为 Inf",
			img: Image{
				File: "a.jpg", Width: 640, Height: 480,
				Annotations: []Annotation{{
					ID: "1", Category: "cat", Kind: KindPolygon,
					Rings: [][][2]float64{{{0, 0}, {10, 0}, {math.Inf(1), 10}}},
				}},
			},
			want:    1,
			fields:  []string{"rings"},
			message: "NaN/Inf",
		},
		{
			name: "旋转框派生参数为 NaN",
			img: Image{
				File: "a.jpg", Width: 640, Height: 480,
				Annotations: []Annotation{{
					ID: "1", Category: "cat", Kind: KindRotated,
					Points: [][2]float64{{0, 0}, {10, 0}, {10, 5}, {0, 5}},
					Angle:  math.NaN(),
				}},
			},
			want:    1,
			fields:  []string{"cx"},
			message: "cx 坐标不是有限数值",
		},
		{
			name: "越界坐标不是 Issue",
			img: Image{
				File: "a.jpg", Width: 100, Height: 100,
				Annotations: []Annotation{{ID: "1", Category: "cat", Kind: KindBBox, X: 90, Y: 90, W: 50, H: 50}},
			},
			want: 0,
		},
		{
			name: "多条标注各带下标",
			img: Image{
				File: "b.png", Width: 100, Height: 100,
				Annotations: []Annotation{
					{ID: "1", Category: "cat", Kind: KindBBox, X: 0, Y: 0, W: 10, H: 10},
					{ID: "2", Category: "dog", Kind: KindBBox, X: 0, Y: 0, W: 0, H: 10},
				},
			},
			want:   1,
			fields: []string{"w"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img := tc.img
			got := Validate(p, &img)
			if len(got) != tc.want {
				t.Fatalf("Issue 数量 = %d (%+v), 期望 %d", len(got), got, tc.want)
			}
			for i, f := range tc.fields {
				if got[i].Field != f {
					t.Errorf("Issue[%d].Field = %q, 期望 %q", i, got[i].Field, f)
				}
			}
			if tc.want == 1 && tc.message != "" {
				if !strings.Contains(got[0].Message, tc.message) {
					t.Errorf("Message = %q, 期望包含 %q", got[0].Message, tc.message)
				}
			}
			for i, is := range got {
				if is.File != img.File {
					t.Errorf("Issue[%d].File = %q, 期望 %q", i, is.File, img.File)
				}
				if is.Index < 0 || is.Index >= len(img.Annotations) {
					t.Errorf("Issue[%d].Index = %d 超出标注范围", i, is.Index)
				}
			}
		})
	}

	// The documented multi-annotation case points at the second annotation.
	img := Image{
		File: "b.png", Width: 100, Height: 100,
		Annotations: []Annotation{
			{ID: "1", Category: "cat", Kind: KindBBox, X: 0, Y: 0, W: 10, H: 10},
			{ID: "2", Category: "dog", Kind: KindBBox, X: 0, Y: 0, W: 0, H: 10},
		},
	}
	if got := Validate(p, &img); len(got) != 1 || got[0].Index != 1 {
		t.Fatalf("期望仅第 2 条标注报错, 得到 %+v", got)
	}
}

// TestValidateDoesNotMutate guards Validate's pure-check promise.
func TestValidateDoesNotMutate(t *testing.T) {
	p := sampleProject()
	img := Image{
		File: "a.jpg", Width: 50, Height: 50,
		Annotations: []Annotation{
			{ID: "1", Category: "cat", Kind: KindBBox, X: 90, Y: 90, W: -10, H: 20},
			{ID: "2", Category: "cat", Kind: KindRotated, Points: [][2]float64{{200, 200}, {210, 200}, {210, 210}, {200, 210}}},
			{ID: "3", Category: "nope", Kind: KindPolygon, Rings: [][][2]float64{{{0, 0}, {1, 1}, {2, 2}}}},
		},
	}
	before := deepCopyImage(img)
	Validate(p, &img)
	if !imagesEqual(img, before) {
		t.Fatalf("Validate 修改了输入:\n got %+v\nwant %+v", img, before)
	}
}
