package exporter

import (
	"math"
	"slices"
	"testing"
)

// geoEps is the round-trip tolerance for the pure geometry helpers
// (docs/phase4-design.md §8).
const geoEps = 1e-9

// signedArea is the shoelace area; positive means ir's vertex order
// (increasing atan2 in image coordinates).
func signedArea(pts [][2]float64) float64 {
	var sum float64
	for i, p := range pts {
		q := pts[(i+1)%len(pts)]
		sum += p[0]*q[1] - q[0]*p[1]
	}
	return sum / 2
}

// samePointSet asserts got and want hold the same points in any order.
func samePointSet(t *testing.T, got, want [][2]float64, eps float64, what string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: 得到 %d 个点，期望 %d 个（%v vs %v）", what, len(got), len(want), got, want)
	}
	used := make([]bool, len(got))
	for _, w := range want {
		found := false
		for i, g := range got {
			if used[i] || math.Hypot(g[0]-w[0], g[1]-w[1]) > eps {
				continue
			}
			used[i], found = true, true
			break
		}
		if !found {
			t.Fatalf("%s: 缺少点 %v（得到 %v）", what, w, got)
		}
	}
}

func TestConvexHullDropsInteriorAndCollinearPoints(t *testing.T) {
	in := [][2]float64{{0, 0}, {10, 0}, {10, 10}, {0, 10}, {5, 5}, {5, 0}, {10, 5}, {0, 5}}
	orig := slices.Clone(in)

	hull := convexHull(in)
	want := [][2]float64{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	samePointSet(t, hull, want, geoEps, "凸包")

	if a := signedArea(hull); a <= 0 {
		t.Fatalf("凸包 shoelace 面积 = %v, 期望正值（ir 顶点序）", a)
	}
	if !slices.Equal(in, orig) {
		t.Fatalf("convexHull 修改了入参: %v", in)
	}
	hull[0] = [2]float64{-1, -1}
	if !slices.Equal(in, orig) {
		t.Fatalf("convexHull 返回值与入参共享底层数组: %v", in)
	}
}

func TestConvexHullDegenerate(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   [][2]float64
		want int
	}{
		{"空", nil, 0},
		{"单点", [][2]float64{{3, 4}}, 1},
		{"两点", [][2]float64{{0, 0}, {4, 5}}, 2},
		{"重复点", [][2]float64{{3, 3}, {3, 3}, {3, 3}}, 1},
		{"共线", [][2]float64{{0, 0}, {10, 0}, {20, 0}}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(convexHull(tc.in)); got != tc.want {
				t.Fatalf("凸包点数 = %d, 期望 %d", got, tc.want)
			}
		})
	}
}

func TestMinAreaRectSquare(t *testing.T) {
	pts := [][2]float64{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	cx, cy, w, h, angle := minAreaRect(pts)
	for _, tc := range []struct {
		name      string
		got, want float64
	}{
		{"cx", cx, 5}, {"cy", cy, 5}, {"w", w, 10}, {"h", h, 10}, {"angle", angle, 0},
	} {
		if math.Abs(tc.got-tc.want) > 1e-6 {
			t.Fatalf("正方形 %s = %v, 期望 %v", tc.name, tc.got, tc.want)
		}
	}
}

func TestMinAreaRectRotatedRectangle(t *testing.T) {
	const (
		cx0, cy0 = 50.0, 60.0
		rw0, rh0 = 40.0, 20.0
		angle0   = math.Pi / 4
	)
	pts := quadFromParamsPts(cx0, cy0, rw0, rh0, angle0)

	cx, cy, w, h, angle := minAreaRect(pts)
	if math.Abs(cx-cx0) > 1e-6 || math.Abs(cy-cy0) > 1e-6 {
		t.Fatalf("中心 = (%v,%v), 期望 (%v,%v)", cx, cy, cx0, cy0)
	}
	if math.Abs(w-rw0) > 1e-6 || math.Abs(h-rh0) > 1e-6 {
		t.Fatalf("边长 = (%v,%v), 期望 (%v,%v)", w, h, rw0, rh0)
	}
	if w < h {
		t.Fatalf("规范化后 w 应 >= h, 得到 (%v,%v)", w, h)
	}
	if math.Abs(angle-angle0) > 1e-6 {
		t.Fatalf("angle = %v, 期望 %v", angle, angle0)
	}
	if angle <= -math.Pi/2 || angle > math.Pi/2 {
		t.Fatalf("angle = %v 超出 (-pi/2, pi/2]", angle)
	}
	samePointSet(t, quadFromParamsPts(cx, cy, w, h, angle), pts, 1e-6, "还原的矩形角点")
}

func TestMinAreaRectDegenerate(t *testing.T) {
	cx, cy, w, h, angle := minAreaRect([][2]float64{{3, 4}})
	if cx != 3 || cy != 4 || w != 0 || h != 0 || angle != 0 {
		t.Fatalf("单点 = (%v,%v,%v,%v,%v), 期望 (3,4,0,0,0)", cx, cy, w, h, angle)
	}

	cx, cy, w, h, angle = minAreaRect([][2]float64{{0, 0}, {10, 0}, {20, 0}})
	if math.Abs(cx-10) > 1e-9 || math.Abs(cy) > 1e-9 {
		t.Fatalf("共线中心 = (%v,%v), 期望 (10,0)", cx, cy)
	}
	if math.Abs(w-20) > 1e-9 || h != 0 {
		t.Fatalf("共线边长 = (%v,%v), 期望 (20,0)", w, h)
	}
	if math.Abs(angle) > 1e-9 {
		t.Fatalf("共线 angle = %v, 期望 0", angle)
	}
}

// sameRings reports whether two ring lists hold identical vertices.
func sameRings(a, b [][][2]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !slices.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func TestBridgeRingsSingleRingIsIdentity(t *testing.T) {
	ring := [][2]float64{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	in := [][][2]float64{ring}
	orig := [][][2]float64{slices.Clone(ring)}

	out := bridgeRings(in)
	if !slices.Equal(out, ring) {
		t.Fatalf("单环 = %v, 期望 %v", out, ring)
	}
	out[0] = [2]float64{-1, -1}
	if !sameRings(in, orig) {
		t.Fatalf("bridgeRings 返回值与入参共享底层数组: %v", in)
	}
}

func TestBridgeRingsConnectsTwoRings(t *testing.T) {
	a := [][2]float64{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	b := [][2]float64{{20, 0}, {30, 0}, {30, 10}, {20, 10}}
	in := [][][2]float64{a, b}
	orig := [][][2]float64{slices.Clone(a), slices.Clone(b)}

	out := bridgeRings(in)
	if len(out) != len(a)+len(b)+1 {
		t.Fatalf("桥接后点数 = %d, 期望 %d", len(out), len(a)+len(b)+1)
	}
	// Every original vertex survives; only the bridge vertex repeats.
	multiset := map[[2]float64]int{}
	for _, p := range out {
		multiset[p]++
	}
	for _, p := range append(slices.Clone(a), b...) {
		if multiset[p] < 1 {
			t.Fatalf("桥接结果丢失顶点 %v: %v", p, out)
		}
	}
	if multiset[[2]float64{20, 0}] != 2 {
		t.Fatalf("桥接点 %v 应出现两次, 得到 %d（%v）", [2]float64{20, 0}, multiset[[2]float64{20, 0}], out)
	}
	if !sameRings(in, orig) {
		t.Fatalf("bridgeRings 修改了入参: %v", in)
	}
}

func TestBridgeRingsIgnoresEmptyRings(t *testing.T) {
	ring := [][2]float64{{0, 0}, {10, 0}, {10, 10}}
	if got := bridgeRings([][][2]float64{{}, ring}); !slices.Equal(got, ring) {
		t.Fatalf("含空环 = %v, 期望 %v", got, ring)
	}
	if got := bridgeRings(nil); got != nil {
		t.Fatalf("nil 输入 = %v, 期望 nil", got)
	}
	if got := bridgeRings([][][2]float64{{}, {}}); got != nil {
		t.Fatalf("全空环 = %v, 期望 nil", got)
	}
}

func TestClipBBox(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		x, y, w, h                 float64
		wantX, wantY, wantW, wantH float64
		wantChanged                bool
	}{
		{"内部不动", 10, 10, 20, 20, 10, 10, 20, 20, false},
		{"左上越界", -10, -10, 50, 50, 0, 0, 40, 40, true},
		{"右下越界", 90, 90, 50, 50, 90, 90, 10, 10, true},
		{"完全在右边", 200, 0, 10, 10, 100, 0, 0, 10, true},
		{"超过整图", -5, -5, 500, 500, 0, 0, 100, 100, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x, y, w, h, changed := clipBBox(tc.x, tc.y, tc.w, tc.h, 100, 100)
			if x != tc.wantX || y != tc.wantY || w != tc.wantW || h != tc.wantH || changed != tc.wantChanged {
				t.Fatalf("clipBBox = (%v,%v,%v,%v,%v), 期望 (%v,%v,%v,%v,%v)",
					x, y, w, h, changed, tc.wantX, tc.wantY, tc.wantW, tc.wantH, tc.wantChanged)
			}
		})
	}
}

func TestClipPoints(t *testing.T) {
	in := [][2]float64{{10, 10}, {90, 90}}
	orig := slices.Clone(in)
	out, changed := clipPoints(in, 100, 100)
	if changed || !slices.Equal(out, in) {
		t.Fatalf("内部点 = %v (changed=%v), 期望原样", out, changed)
	}
	if !slices.Equal(in, orig) {
		t.Fatalf("clipPoints 修改了入参: %v", in)
	}
	out[0] = [2]float64{-1, -1}
	if !slices.Equal(in, orig) {
		t.Fatalf("clipPoints 返回值与入参共享底层数组: %v", in)
	}

	out, changed = clipPoints([][2]float64{{-5, 20}, {120, 40}}, 100, 100)
	if !changed {
		t.Fatal("越界点应报告 changed")
	}
	want := [][2]float64{{0, 20}, {100, 40}}
	if !slices.Equal(out, want) {
		t.Fatalf("裁剪结果 = %v, 期望 %v", out, want)
	}
}
