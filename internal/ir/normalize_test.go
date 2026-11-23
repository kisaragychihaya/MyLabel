package ir

import (
	"math"
	"reflect"
	"testing"
)

const eps = 1e-9

func closeTo(a, b float64) bool { return math.Abs(a-b) <= eps }

// assertPoints compares two vertex lists with the 1e-9 tolerance.
func assertPoints(t *testing.T, what string, got, want [][2]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s 点数 = %d, 期望 %d (%v)", what, len(got), len(want), got)
	}
	for i := range want {
		if !closeTo(got[i][0], want[i][0]) || !closeTo(got[i][1], want[i][1]) {
			t.Fatalf("%s[%d] = (%.12f, %.12f), 期望 (%.12f, %.12f)", what, i,
				got[i][0], got[i][1], want[i][0], want[i][1])
		}
	}
}

// sortedPoints returns the vertices sorted by x then y, for set comparisons.
func sortedPoints(pts [][2]float64) [][2]float64 {
	out := append([][2]float64(nil), pts...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			if out[j][0] < out[j-1][0] || (out[j][0] == out[j-1][0] && out[j][1] < out[j-1][1]) {
				out[j], out[j-1] = out[j-1], out[j]
			}
		}
	}
	return out
}

func TestNormalizeQuadVertexOrder(t *testing.T) {
	// A 40x20 box centred at (100,100), rotated by 60°; corners hand-computed
	// from TL/TR/BR/BL = c + R(60°)·(±20, ±10):
	//   TL (98.660254037844,  77.679491924311)
	//   TR (118.660254037844, 112.320508075689)
	//   BR (101.339745962156, 122.320508075689)
	//   BL (81.339745962156,  87.679491924311)
	// The canonical form starts at the leftmost vertex BL and runs
	// BL->TL->TR->BR, so its width is the BL->TL edge (20), its height the
	// TL->TR edge (40) and its angle the BL->TL direction (-30°).
	tl := [2]float64{100 - 10 + 5*math.Sqrt(3), 100 - 10*math.Sqrt(3) - 5}
	tr := [2]float64{100 + 10 + 5*math.Sqrt(3), 100 + 10*math.Sqrt(3) - 5}
	br := [2]float64{100 + 10 - 5*math.Sqrt(3), 100 + 10*math.Sqrt(3) + 5}
	bl := [2]float64{100 - 10 - 5*math.Sqrt(3), 100 - 10*math.Sqrt(3) + 5}

	cases := []struct {
		name  string
		input [][2]float64
	}{
		{"乱序 TR,BR,BL,TL", [][2]float64{tr, br, bl, tl}},
		{"乱序 BR,TL,TR,BL", [][2]float64{br, tl, tr, bl}},
		{"顺时针 TL,BL,BR,TR", [][2]float64{tl, bl, br, tr}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img := Image{
				File: "a.jpg", Width: 640, Height: 480,
				Annotations: []Annotation{{
					ID: "1", Category: "cat", Kind: KindRotated,
					Points: tc.input,
					CX:     12345, CY: -7, RW: 1, RH: 2, Angle: 3, // stale cache must be overwritten
				}},
			}
			if w := Normalize(sampleProject(), &img); len(w) != 0 {
				t.Fatalf("warnings = %+v, 期望 0 条", w)
			}
			a := img.Annotations[0]
			assertPoints(t, "Points", a.Points, [][2]float64{bl, tl, tr, br})
			if !closeTo(a.CX, 100) || !closeTo(a.CY, 100) {
				t.Errorf("中心 = (%.12f, %.12f), 期望 (100, 100)", a.CX, a.CY)
			}
			if !closeTo(a.RW, 20) || !closeTo(a.RH, 40) {
				t.Errorf("RW/RH = (%.12f, %.12f), 期望 (20, 40)", a.RW, a.RH)
			}
			if !closeTo(a.Angle, -math.Pi/6) {
				t.Errorf("Angle = %.12f, 期望 %.12f", a.Angle, -math.Pi/6)
			}
		})
	}
}

func TestNormalizeQuadFromParams(t *testing.T) {
	t.Run("angle 为 0 反推出轴对齐四点", func(t *testing.T) {
		img := Image{
			File: "a.jpg", Width: 640, Height: 480,
			Annotations: []Annotation{{
				ID: "1", Category: "cat", Kind: KindRotated,
				CX: 100, CY: 100, RW: 40, RH: 20,
			}},
		}
		if w := Normalize(sampleProject(), &img); len(w) != 0 {
			t.Fatalf("warnings = %+v, 期望 0 条", w)
		}
		a := img.Annotations[0]
		assertPoints(t, "Points", a.Points, [][2]float64{{80, 90}, {120, 90}, {120, 110}, {80, 110}})
		if !closeTo(a.CX, 100) || !closeTo(a.CY, 100) || !closeTo(a.RW, 40) || !closeTo(a.RH, 20) || !closeTo(a.Angle, 0) {
			t.Errorf("参数被改写为 cx=%v cy=%v rw=%v rh=%v angle=%v", a.CX, a.CY, a.RW, a.RH, a.Angle)
		}
	})

	t.Run("angle 非 0 反推保持同一形状", func(t *testing.T) {
		const angle = math.Pi / 3
		wantShape := sortedPoints(quadFromParams(100, 100, 40, 20, angle))
		img := Image{
			File: "a.jpg", Width: 640, Height: 480,
			Annotations: []Annotation{{
				ID: "1", Category: "cat", Kind: KindRotated,
				CX: 100, CY: 100, RW: 40, RH: 20, Angle: angle,
			}},
		}
		if w := Normalize(sampleProject(), &img); len(w) != 0 {
			t.Fatalf("warnings = %+v, 期望 0 条", w)
		}
		a := img.Annotations[0]
		if len(a.Points) != 4 {
			t.Fatalf("Points = %v, 期望 4 个点", a.Points)
		}
		got := sortedPoints(a.Points)
		for i := range wantShape {
			if !closeTo(got[i][0], wantShape[i][0]) || !closeTo(got[i][1], wantShape[i][1]) {
				t.Fatalf("形状改变: got %v, want %v", got, wantShape)
			}
		}
		if !closeTo(a.CX, 100) || !closeTo(a.CY, 100) {
			t.Errorf("中心 = (%v, %v), 期望 (100, 100)", a.CX, a.CY)
		}
		// Opposite-edge averages must still describe a 40x20 box.
		dims := []float64{a.RW, a.RH}
		if !(closeTo(dims[0], 40) && closeTo(dims[1], 20)) && !(closeTo(dims[0], 20) && closeTo(dims[1], 40)) {
			t.Errorf("RW/RH = %v, 期望 40x20 或 20x40", dims)
		}

		// A second pass must be a no-op.
		before := deepCopyImage(img)
		if w := Normalize(sampleProject(), &img); len(w) != 0 {
			t.Fatalf("第二次 Normalize warnings = %+v, 期望 0 条", w)
		}
		if !imagesEqual(img, before) {
			t.Fatalf("第二次 Normalize 改变了结果:\n got %+v\nwant %+v", img, before)
		}
	})

	t.Run("正角度时起点改为最左点，宽高互换", func(t *testing.T) {
		// 契约要求起点是最左顶点。cx=100, cy=50, rw=40, rh=20, angle=0.5 的
		// 框，最左顶点是局部坐标系里的 BL 而非 TL，于是规范化后起点前移一位：
		// 宽变成原来的高(20)、高变成原来的宽(40)、角度减去 90°。形状不变。
		img := Image{
			File: "a.jpg", Width: 640, Height: 480,
			Annotations: []Annotation{{
				ID: "1", Category: "cat", Kind: KindRotated,
				CX: 100, CY: 50, RW: 40, RH: 20, Angle: 0.5,
			}},
		}
		if w := Normalize(sampleProject(), &img); len(w) != 0 {
			t.Fatalf("warnings = %+v, 期望 0 条", w)
		}
		a := img.Annotations[0]
		if !closeTo(a.CX, 100) || !closeTo(a.CY, 50) {
			t.Errorf("中心 = (%v, %v), 期望 (100, 50)", a.CX, a.CY)
		}
		if !closeTo(a.RW, 20) || !closeTo(a.RH, 40) {
			t.Errorf("RW/RH = (%v, %v), 期望 (20, 40)", a.RW, a.RH)
		}
		if !closeTo(a.Angle, 0.5-math.Pi/2) {
			t.Errorf("Angle = %v, 期望 %v", a.Angle, 0.5-math.Pi/2)
		}
		// The swapped parameters describe the very same four corners.
		wantCorners := sortedPoints(quadFromParams(100, 50, 40, 20, 0.5))
		gotCorners := sortedPoints(a.Points)
		for i := range wantCorners {
			if !closeTo(gotCorners[i][0], wantCorners[i][0]) || !closeTo(gotCorners[i][1], wantCorners[i][1]) {
				t.Fatalf("形状改变: got %v, want %v", gotCorners, wantCorners)
			}
		}
	})

	t.Run("参数与点都有时以 Points 为准", func(t *testing.T) {
		img := Image{
			File: "a.jpg", Width: 640, Height: 480,
			Annotations: []Annotation{{
				ID: "1", Category: "cat", Kind: KindRotated,
				Points: [][2]float64{{200, 200}, {300, 200}, {300, 250}, {200, 250}},
				CX:     999, CY: 999, RW: 5, RH: 5, Angle: 1,
			}},
		}
		Normalize(sampleProject(), &img)
		a := img.Annotations[0]
		assertPoints(t, "Points", a.Points, [][2]float64{{200, 200}, {300, 200}, {300, 250}, {200, 250}})
		if !closeTo(a.CX, 250) || !closeTo(a.CY, 225) || !closeTo(a.RW, 100) || !closeTo(a.RH, 50) || !closeTo(a.Angle, 0) {
			t.Errorf("缓存未按 Points 重算: cx=%v cy=%v rw=%v rh=%v angle=%v", a.CX, a.CY, a.RW, a.RH, a.Angle)
		}
	})
}

func TestNormalizeRingOrder(t *testing.T) {
	big := [][2]float64{{0, 0}, {0, 20}, {20, 20}, {20, 0}}   // |area| 400, 顺时针
	mid := [][2]float64{{40, 0}, {50, 0}, {50, 10}, {40, 10}} // |area| 300, 逆时针
	small := [][2]float64{{60, 0}, {62, 0}, {62, 2}, {60, 2}} // |area| 4

	cases := []struct {
		name  string
		input [][][2]float64
		want  [][][2]float64
	}{
		{"中/小/大 → 大/中/小", [][][2]float64{mid, small, big}, [][][2]float64{big, mid, small}},
		{"已有序不变", [][][2]float64{big, mid, small}, [][][2]float64{big, mid, small}},
		{"单环不变", [][][2]float64{small}, [][][2]float64{small}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img := Image{
				File: "a.jpg", Width: 640, Height: 480,
				Annotations: []Annotation{{
					ID: "1", Category: "cat", Kind: KindPolygon, Rings: tc.input,
				}},
			}
			if w := Normalize(sampleProject(), &img); len(w) != 0 {
				t.Fatalf("warnings = %+v, 期望 0 条", w)
			}
			if !reflect.DeepEqual(img.Annotations[0].Rings, tc.want) {
				t.Fatalf("Rings = %v, 期望 %v", img.Annotations[0].Rings, tc.want)
			}
		})
	}
}

func TestNormalizeClip(t *testing.T) {
	cases := []struct {
		name     string
		width    int
		height   int
		ann      Annotation
		want     Annotation
		warnings int
		field    string
	}{
		{
			name:  "bbox 右下角越界",
			width: 100, height: 80,
			ann:      Annotation{ID: "1", Kind: KindBBox, X: 90, Y: 70, W: 30, H: 30},
			want:     Annotation{ID: "1", Kind: KindBBox, X: 90, Y: 70, W: 10, H: 10},
			warnings: 1, field: "x",
		},
		{
			name:  "bbox 左上角越界",
			width: 100, height: 80,
			ann:      Annotation{ID: "1", Kind: KindBBox, X: -10, Y: -10, W: 20, H: 20},
			want:     Annotation{ID: "1", Kind: KindBBox, X: 0, Y: 0, W: 10, H: 10},
			warnings: 1, field: "x",
		},
		{
			name:  "bbox 完全在外",
			width: 100, height: 80,
			ann:      Annotation{ID: "1", Kind: KindBBox, X: 150, Y: 150, W: 10, H: 10},
			want:     Annotation{ID: "1", Kind: KindBBox, X: 100, Y: 80, W: 0, H: 0},
			warnings: 1, field: "x",
		},
		{
			name:  "polygon 顶点越界",
			width: 100, height: 80,
			ann: Annotation{ID: "1", Kind: KindPolygon, Rings: [][][2]float64{
				{{90, 70}, {200, 70}, {200, 200}},
			}},
			want: Annotation{ID: "1", Kind: KindPolygon, Rings: [][][2]float64{
				{{90, 70}, {100, 70}, {100, 80}},
			}},
			warnings: 1, field: "rings",
		},
		{
			name:  "rotated 顶点越界",
			width: 100, height: 80,
			ann: Annotation{ID: "1", Kind: KindRotated, Points: [][2]float64{
				{60, 10}, {120, 10}, {120, 30}, {60, 30},
			}},
			want: Annotation{
				ID: "1", Kind: KindRotated,
				Points: [][2]float64{{60, 10}, {100, 10}, {100, 30}, {60, 30}},
				CX:     80, CY: 20, RW: 40, RH: 20, Angle: 0,
			},
			warnings: 1, field: "points",
		},
		{
			name:  "尺寸为 0 时不裁剪",
			width: 0, height: 0,
			ann:      Annotation{ID: "1", Kind: KindBBox, X: 90, Y: 70, W: 30, H: 30},
			want:     Annotation{ID: "1", Kind: KindBBox, X: 90, Y: 70, W: 30, H: 30},
			warnings: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img := Image{File: "a.jpg", Width: tc.width, Height: tc.height, Annotations: []Annotation{tc.ann}}
			got := Normalize(sampleProject(), &img)
			if len(got) != tc.warnings {
				t.Fatalf("warnings = %d (%+v), 期望 %d", len(got), got, tc.warnings)
			}
			if tc.warnings > 0 && got[0].Field != tc.field {
				t.Errorf("警告 Field = %q, 期望 %q", got[0].Field, tc.field)
			}
			if tc.warnings > 0 && got[0].Index != 0 {
				t.Errorf("警告 Index = %d, 期望 0", got[0].Index)
			}
			a := img.Annotations[0]
			if len(tc.want.Rings) > 0 {
				if !reflect.DeepEqual(a.Rings, tc.want.Rings) {
					t.Fatalf("Rings = %v, 期望 %v", a.Rings, tc.want.Rings)
				}
				return
			}
			if len(tc.want.Points) > 0 {
				assertPoints(t, "Points", a.Points, tc.want.Points)
				if !closeTo(a.CX, tc.want.CX) || !closeTo(a.CY, tc.want.CY) ||
					!closeTo(a.RW, tc.want.RW) || !closeTo(a.RH, tc.want.RH) ||
					!closeTo(a.Angle, tc.want.Angle) {
					t.Fatalf("缓存 = cx=%v cy=%v rw=%v rh=%v angle=%v, 期望 cx=%v cy=%v rw=%v rh=%v angle=%v",
						a.CX, a.CY, a.RW, a.RH, a.Angle, tc.want.CX, tc.want.CY, tc.want.RW, tc.want.RH, tc.want.Angle)
				}
				return
			}
			if !closeTo(a.X, tc.want.X) || !closeTo(a.Y, tc.want.Y) || !closeTo(a.W, tc.want.W) || !closeTo(a.H, tc.want.H) {
				t.Fatalf("bbox = (%v, %v, %v, %v), 期望 (%v, %v, %v, %v)",
					a.X, a.Y, a.W, a.H, tc.want.X, tc.want.Y, tc.want.W, tc.want.H)
			}
		})
	}
}

func TestNormalizeNegativeSize(t *testing.T) {
	img := Image{
		File: "a.jpg", Width: 100, Height: 80,
		Annotations: []Annotation{
			{ID: "1", Category: "cat", Kind: KindBBox, X: 50, Y: 50, W: -20, H: -10},
			{ID: "2", Category: "cat", Kind: KindBBox, X: 50, Y: 50, W: -20, H: 10},
			{ID: "3", Category: "cat", Kind: KindBBox, X: 50, Y: 50, W: 20, H: -10},
		},
	}
	got := Normalize(sampleProject(), &img)
	if len(got) != 4 {
		t.Fatalf("warnings = %d (%+v), 期望 4 条（1 条 w + 1 条 h + 各 1 条）", len(got), got)
	}
	type box struct{ x, y, w, h float64 }
	want := []box{{30, 40, 20, 10}, {30, 50, 20, 10}, {50, 40, 20, 10}}
	for i, w := range want {
		a := img.Annotations[i]
		if !closeTo(a.X, w.x) || !closeTo(a.Y, w.y) || !closeTo(a.W, w.w) || !closeTo(a.H, w.h) {
			t.Errorf("标注 %d = (%v, %v, %v, %v), 期望 %+v", i, a.X, a.Y, a.W, a.H, w)
		}
	}
}

func TestNormalizeIdempotent(t *testing.T) {
	img := Image{
		File: "sub/a.jpg", Width: 100, Height: 80,
		Annotations: []Annotation{
			{ID: "1", Category: "cat", Kind: KindBBox, X: 90, Y: 70, W: -30, H: 30},
			{ID: "2", Category: "dog", Kind: KindPolygon, Rings: [][][2]float64{
				{{60, 60}, {62, 60}, {62, 62}, {60, 62}},
				{{-5, -5}, {50, -5}, {50, 50}, {-5, 50}},
				{{150, 10}, {160, 10}, {160, 20}},
			}},
			{ID: "3", Category: "cat", Kind: KindRotated, Points: [][2]float64{
				{60, 10}, {120, 10}, {120, 30}, {60, 30},
			}},
			{ID: "4", Category: "cat", Kind: KindRotated, CX: 90, CY: 70, RW: 40, RH: 20, Angle: 0.3},
		},
	}
	p := sampleProject()
	first := Normalize(p, &img)
	if len(first) == 0 {
		t.Fatal("第一遍未产生任何 warning, 用例应触发裁剪/翻正")
	}
	afterFirst := deepCopyImage(img)
	second := Normalize(p, &img)
	if len(second) != 0 {
		t.Fatalf("第二遍 warnings = %+v, 期望 0 条", second)
	}
	if !imagesEqual(img, afterFirst) {
		t.Fatalf("非幂等:\n第一次 %+v\n第二次 %+v", afterFirst, img)
	}
}

func TestNormalizeNilAndEmpty(t *testing.T) {
	if w := Normalize(nil, nil); w != nil {
		t.Errorf("Normalize(nil, nil) = %+v, 期望 nil", w)
	}
	img := Image{File: "a.jpg", Width: 10, Height: 10}
	if w := Normalize(sampleProject(), &img); len(w) != 0 {
		t.Errorf("空图 warnings = %+v, 期望 0 条", w)
	}
}
