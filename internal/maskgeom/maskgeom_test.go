package maskgeom

import (
	"math"
	"testing"
)

// emptyMask returns a w*h mask with every pixel at 0.
func emptyMask(w, h int) []float32 { return make([]float32, w*h) }

// fillRect sets every pixel of [x0,x1]x[y0,y1] (inclusive) to v.
func fillRect(m []float32, w, x0, y0, x1, y1 int, v float32) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			m[y*w+x] = v
		}
	}
}

// fillDisk sets every pixel whose centre lies within r of (cx, cy) to v.
func fillDisk(m []float32, w, cx, cy, r int, v float32) {
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if dx, dy := x-cx, y-cy; dx*dx+dy*dy <= r*r {
				m[y*w+x] = v
			}
		}
	}
}

// fgCount counts the foreground pixels at thresh.
func fgCount(m []float32, thresh float32) int {
	n := 0
	for _, v := range m {
		if v > thresh {
			n++
		}
	}
	return n
}

// samePtSet compares two vertex sets ignoring order and starting point.
func samePtSet(got, want []Pt) (Pt, bool) {
	if len(got) != len(want) {
		return Pt{}, false
	}
	used := make([]bool, len(want))
	for _, g := range got {
		found := false
		for i, w := range want {
			if !used[i] && g == w {
				used[i], found = true, true
				break
			}
		}
		if !found {
			return g, false
		}
	}
	return Pt{}, true
}

func TestBBox(t *testing.T) {
	cases := []struct {
		name   string
		w, h   int
		build  func() []float32
		thresh float32
		want   [4]int // minX, minY, maxX, maxY
		ok     bool
	}{
		{
			name: "solid rectangle",
			w:    12, h: 10, thresh: 0.5,
			build: func() []float32 {
				m := emptyMask(12, 10)
				fillRect(m, 12, 2, 1, 8, 6, 1)
				return m
			},
			want: [4]int{2, 1, 8, 6}, ok: true,
		},
		{
			name: "value equal to threshold is background",
			w:    4, h: 4, thresh: 0.5,
			build: func() []float32 {
				m := emptyMask(4, 4)
				for i := range m {
					m[i] = 0.5
				}
				return m
			},
			ok: false,
		},
		{
			name: "single pixel just above threshold",
			w:    4, h: 4, thresh: 0.5,
			build: func() []float32 {
				m := emptyMask(4, 4)
				m[2*4+3] = 0.5001
				return m
			},
			want: [4]int{3, 2, 3, 2}, ok: true,
		},
		{
			name: "negative logits with negative threshold",
			w:    6, h: 6, thresh: -2,
			build: func() []float32 {
				m := emptyMask(6, 6)
				for i := range m {
					m[i] = -5
				}
				fillRect(m, 6, 1, 2, 3, 4, -1)
				return m
			},
			want: [4]int{1, 2, 3, 4}, ok: true,
		},
		{
			name: "empty mask",
			w:    8, h: 8, thresh: 0,
			build: func() []float32 { return emptyMask(8, 8) },
			ok:    false,
		},
		{
			name: "buffer smaller than the grid",
			w:    8, h: 8, thresh: 0,
			build: func() []float32 { return emptyMask(4, 4) },
			ok:    false,
		},
		{
			name: "disk",
			w:    61, h: 61, thresh: 0,
			build: func() []float32 {
				m := emptyMask(61, 61)
				fillDisk(m, 61, 30, 30, 20, 1)
				return m
			},
			want: [4]int{10, 10, 50, 50}, ok: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			minX, minY, maxX, maxY, ok := BBox(tc.build(), tc.w, tc.h, tc.thresh)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			got := [4]int{minX, minY, maxX, maxY}
			if got != tc.want {
				t.Fatalf("bbox = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLargestComponent(t *testing.T) {
	t.Run("keeps the larger component", func(t *testing.T) {
		const w, h = 40, 20
		m := emptyMask(w, h)
		fillRect(m, w, 1, 1, 10, 10, 1) // 100 px
		fillRect(m, w, 20, 1, 24, 5, 1) // 25 px
		got := LargestComponent(m, w, h, 0.5)
		if n := fgCount(got, 0.5); n != 100 {
			t.Fatalf("foreground pixels = %d, want 100", n)
		}
		if got[1*w+20] != 0 {
			t.Fatalf("smaller component survived at (20,1)")
		}
		if got[5*w+5] != 1 {
			t.Fatalf("larger component was not copied through at (5,5)")
		}
	})

	t.Run("equal components keep the first in raster order", func(t *testing.T) {
		const w, h = 30, 12
		m := emptyMask(w, h)
		fillRect(m, w, 2, 2, 5, 5, 1)   // 16 px, earlier in scan order
		fillRect(m, w, 20, 6, 23, 9, 1) // 16 px
		got := LargestComponent(m, w, h, 0.5)
		if n := fgCount(got, 0.5); n != 16 {
			t.Fatalf("foreground pixels = %d, want 16", n)
		}
		if got[7*w+21] != 0 {
			t.Fatalf("second of two equal components survived")
		}
	})

	t.Run("4-connectivity splits diagonal pixels", func(t *testing.T) {
		const w, h = 8, 8
		m := emptyMask(w, h)
		m[2*w+2] = 1 // upper-left of the diagonal pair
		m[3*w+3] = 1 // lower-right
		got := LargestComponent(m, w, h, 0.5)
		if n := fgCount(got, 0.5); n != 1 {
			t.Fatalf("foreground pixels = %d, want 1 (diagonal pixels are separate components)", n)
		}
		if got[2*w+2] != 1 || got[3*w+3] != 0 {
			t.Fatalf("kept the wrong single pixel")
		}
	})

	t.Run("hole stays background", func(t *testing.T) {
		const w, h = 20, 20
		m := emptyMask(w, h)
		fillRect(m, w, 2, 2, 17, 17, 1)
		fillRect(m, w, 8, 8, 11, 11, 0)
		got := LargestComponent(m, w, h, 0.5)
		if n := fgCount(got, 0.5); n != 16*16-4*4 {
			t.Fatalf("foreground pixels = %d, want %d", n, 16*16-4*4)
		}
	})

	t.Run("no foreground yields an all-zero copy", func(t *testing.T) {
		const w, h = 9, 7
		m := emptyMask(w, h)
		got := LargestComponent(m, w, h, 0)
		if len(got) != len(m) {
			t.Fatalf("len = %d, want %d", len(got), len(m))
		}
		for i, v := range got {
			if v != 0 {
				t.Fatalf("pixel %d = %v, want an all-zero copy", i, v)
			}
		}
	})
}

func TestTraceContour(t *testing.T) {
	t.Run("solid rectangle simplifies to its four corners", func(t *testing.T) {
		const w, h = 12, 10
		m := emptyMask(w, h)
		fillRect(m, w, 2, 1, 9, 8, 1)
		contour, err := TraceContour(m, w, h, 0.5)
		if err != nil {
			t.Fatalf("TraceContour: %v", err)
		}
		if len(contour) < 4 {
			t.Fatalf("contour has %d vertices, want at least 4", len(contour))
		}
		if contour[0] == contour[len(contour)-1] {
			t.Fatalf("contour repeats its first vertex at the end")
		}
		for _, p := range contour {
			// Every traced vertex is a boundary pixel of the rectangle.
			onEdge := p.X == 2 || p.X == 9 || p.Y == 1 || p.Y == 8
			if !onEdge {
				t.Fatalf("contour vertex %v is not on the rectangle boundary", p)
			}
		}
		want := []Pt{{2, 1}, {9, 1}, {9, 8}, {2, 8}}
		got := Simplify(contour, 1.5)
		if len(got) != 4 {
			t.Fatalf("simplified contour = %v (%d vertices), want the 4 corners", got, len(got))
		}
		if bad, ok := samePtSet(got, want); !ok {
			t.Fatalf("simplified contour = %v, want corners %v (unexpected vertex %v)", got, want, bad)
		}
	})

	t.Run("disk keeps points on the radius", func(t *testing.T) {
		const w, h = 121, 121
		const cx, cy, r = 60, 60, 40
		m := emptyMask(w, h)
		fillDisk(m, w, cx, cy, r, 1)
		contour, err := TraceContour(m, w, h, 0.5)
		if err != nil {
			t.Fatalf("TraceContour: %v", err)
		}
		if len(contour) < 200 {
			t.Fatalf("contour has %d vertices, want the full digital circle (>200)", len(contour))
		}
		got := Simplify(contour, 1.5)
		if len(got) >= len(contour)/2 {
			t.Fatalf("simplified to %d of %d vertices, want a clear reduction", len(got), len(contour))
		}
		for _, p := range got {
			d := math.Hypot(float64(p.X-cx), float64(p.Y-cy))
			if math.Abs(d-float64(r)) > 1 {
				t.Fatalf("retained vertex %v sits at radius %.2f, want %d±1", p, d, r)
			}
		}
	})

	t.Run("L shape keeps the concave corner", func(t *testing.T) {
		const w, h = 20, 18
		m := emptyMask(w, h)
		fillRect(m, w, 4, 3, 7, 12, 1)  // vertical arm
		fillRect(m, w, 4, 9, 12, 12, 1) // horizontal foot
		contour, err := TraceContour(m, w, h, 0.5)
		if err != nil {
			t.Fatalf("TraceContour: %v", err)
		}
		got := Simplify(contour, 1.5)
		if len(got) != 6 {
			t.Fatalf("simplified L = %v (%d vertices), want 6", got, len(got))
		}
		// The reentrant corner sits where the arm's right edge meets the foot's
		// top edge; the digital step lands on one of the two pixels beside it.
		const cx, cy = 7, 9
		kept := false
		for _, p := range got {
			if abs(p.X-cx) <= 1 && abs(p.Y-cy) <= 1 {
				kept = true
			}
		}
		if !kept {
			t.Fatalf("simplified L = %v, concave corner near (%d,%d) was dropped", got, cx, cy)
		}
	})

	t.Run("one pixel wide column traces without looping", func(t *testing.T) {
		const w, h = 11, 20
		m := emptyMask(w, h)
		for y := 3; y <= 16; y++ {
			m[y*w+5] = 1
		}
		contour, err := TraceContour(m, w, h, 0.5)
		if err != nil {
			t.Fatalf("TraceContour: %v", err)
		}
		if len(contour) < 4 {
			t.Fatalf("contour has %d vertices, want at least 4", len(contour))
		}
		for _, p := range contour {
			if p.X != 5 || p.Y < 3 || p.Y > 16 {
				t.Fatalf("contour vertex %v left the column", p)
			}
		}
	})

	t.Run("isolated pixel", func(t *testing.T) {
		const w, h = 9, 9
		m := emptyMask(w, h)
		m[4*w+4] = 1
		contour, err := TraceContour(m, w, h, 0.5)
		if err != nil {
			t.Fatalf("TraceContour: %v", err)
		}
		if len(contour) != 1 || contour[0] != (Pt{4, 4}) {
			t.Fatalf("contour = %v, want the single pixel", contour)
		}
	})

	t.Run("only the first region is traced", func(t *testing.T) {
		const w, h = 30, 20
		m := emptyMask(w, h)
		fillRect(m, w, 2, 2, 6, 6, 1)
		fillRect(m, w, 15, 10, 25, 18, 1)
		contour, err := TraceContour(m, w, h, 0.5)
		if err != nil {
			t.Fatalf("TraceContour: %v", err)
		}
		for _, p := range contour {
			if p.X < 2 || p.X > 6 || p.Y < 2 || p.Y > 6 {
				t.Fatalf("contour vertex %v comes from the second region", p)
			}
		}
	})

	t.Run("no foreground is an error", func(t *testing.T) {
		if _, err := TraceContour(emptyMask(10, 10), 10, 10, 0.5); err == nil {
			t.Fatal("TraceContour on an empty mask returned no error")
		}
	})

	t.Run("bad dimensions are an error", func(t *testing.T) {
		if _, err := TraceContour(emptyMask(4, 4), 8, 8, 0.5); err == nil {
			t.Fatal("TraceContour accepted a buffer smaller than the grid")
		}
	})
}

func TestSimplify(t *testing.T) {
	t.Run("collinear run collapses to its endpoints", func(t *testing.T) {
		pts := []Pt{{0, 0}, {1, 2}, {2, 4}, {3, 6}, {4, 8}}
		got := Simplify(pts, 0.5)
		if len(got) != 2 || got[0] != pts[0] || got[len(got)-1] != pts[len(pts)-1] {
			t.Fatalf("Simplify = %v, want the two endpoints", got)
		}
	})

	t.Run("two or fewer vertices pass through", func(t *testing.T) {
		for _, pts := range [][]Pt{nil, {{3, 4}}, {{0, 0}, {5, 5}}} {
			got := Simplify(pts, 1)
			if len(got) != len(pts) {
				t.Fatalf("Simplify(%v) = %v, want it unchanged", pts, got)
			}
			for i := range pts {
				if got[i] != pts[i] {
					t.Fatalf("Simplify(%v) = %v, want it unchanged", pts, got)
				}
			}
		}
	})

	t.Run("zigzag survives a zero epsilon", func(t *testing.T) {
		pts := []Pt{{0, 0}, {1, 3}, {2, 0}, {3, 3}, {4, 0}}
		if got := Simplify(pts, 0); len(got) != len(pts) {
			t.Fatalf("Simplify = %v, want all %d vertices", got, len(pts))
		}
	})

	t.Run("large epsilon leaves a straight chord", func(t *testing.T) {
		pts := []Pt{{0, 0}, {2, 1}, {4, 0}, {6, 1}, {8, 0}}
		if got := Simplify(pts, 100); len(got) != 2 {
			t.Fatalf("Simplify = %v, want 2 vertices", got)
		}
	})

	t.Run("corner is kept when it deviates more than epsilon", func(t *testing.T) {
		pts := []Pt{{0, 0}, {5, 0}, {5, 5}, {10, 5}}
		got := Simplify(pts, 1)
		if len(got) != 4 {
			t.Fatalf("Simplify = %v, want all four vertices", got)
		}
	})
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
