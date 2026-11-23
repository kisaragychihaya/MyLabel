package app

import (
	"fmt"
	"image"
	"math"
	"math/rand"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Verification item #3: how much does one canvas frame cost with a thousand
// annotations? The benchmark paints whole frames through a Tester, so it
// includes building the view, layout, painting and the scene diff — what the
// app pays per frame — not just the annotation loop.

// benchApp is the App the benchmark's Tester renders.
var benchApp *App

func benchView(c *ui.Context) {
	ui.Column(c).Fill().Children(func() { benchApp.canvasView(c) })
}

// Benchmarks keep a Tester of their own: the tests' Tester renders the whole
// app layout, whose thumbnail spinner keeps asking for frames, which would
// dominate a frame benchmark. Two Tester instances in one test binary stay
// within the one-thread-per-Tester cost of mygo issue #1902.
var (
	benchOnce   sync.Once
	benchTester *ui.Tester
)

// benchAnnotations returns an App with n random annotations over an 800×600
// image, the first one selected as a real session has one. With poly the
// annotations are five-vertex polygons of the same spread, to price the seg
// draw path against the rect one.
func benchAnnotations(n int, poly bool) *App {
	a := New()
	a.images = []ImageInfo{{Path: "bench", Name: "bench.png", Width: 800, Height: 600}}
	a.loaded = 0
	a.bmp = ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 800, 600)))
	a.imgW, a.imgH = 800, 600
	a.zoom = 1

	rng := rand.New(rand.NewSource(42))
	anns := make([]Ann, n)
	for i := range anns {
		cx := 40 + rng.Float64()*720
		cy := 30 + rng.Float64()*540
		w := 20 + rng.Float64()*80
		h := 20 + rng.Float64()*80
		angle := rng.Float64() * math.Pi
		if poly {
			anns[i] = newPolyAnn(fmt.Sprintf("ann_%d", i), rng.Intn(9), []Point{
				{cx - w/2, cy - h/2}, {cx + w/2, cy - h/2},
				{cx + w/2, cy + h/2}, {cx, cy + h/4}, {cx - w/2, cy + h/2},
			})
			continue
		}
		anns[i] = Ann{
			ID:    fmt.Sprintf("ann_%d", i),
			Cat:   rng.Intn(9),
			CX:    cx,
			CY:    cy,
			W:     w,
			H:     h,
			Angle: angle,
		}
	}
	a.images[0].Anns = anns
	if n > 0 {
		a.selectOnly(0)
	}
	return a
}

// BenchmarkDrawFrame times one canvas frame with 0 (the frame's own overhead),
// 1000 and 5000 rectangle annotations, one sub-benchmark at zoom 2 to show what
// the scaled painter transform costs, and 1000 five-vertex polygons to price the
// seg draw path against the rect one. Numbers only: design §3 wants 1000
// rectangle annotations under 16 ms, which the log reports but no assertion
// enforces.
func BenchmarkDrawFrame(b *testing.B) {
	cases := []struct {
		name string
		n    int
		zoom float64
		poly bool
	}{
		{"anns0", 0, 1, false},
		{"anns1000", 1000, 1, false},
		{"anns5000", 5000, 1, false},
		{"anns1000/zoom2", 1000, 2, false},
		{"anns1000poly", 1000, 1, true},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			benchApp = benchAnnotations(tc.n, tc.poly)
			benchApp.zoom = tc.zoom
			benchOnce.Do(func() { benchTester = ui.NewTester(benchView, testWinW, testWinH) })
			benchTester.Frame() // draw once outside the timed loop

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Pan one DIP per frame: an identical scene lets the renderer
				// reuse the last frame, and a still canvas paints nothing
				// new. Panning is what a session does all day, and it keeps
				// zoom at 1, so the image transform stays a pure translation
				// the CPU rasterizer folds into the op — the fast path.
				benchApp.panX = float64(i & 1)
				benchTester.Frame()
			}
			b.StopTimer()

			ns := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
			ms := ns / 1e6
			b.ReportMetric(ms, "ms/frame")
			// The harness runs a benchmark more than once (a calibration pass
			// first), so these logs repeat; the last one matches the reported
			// result line.
			if tc.n == 0 {
				b.Logf("no annotations at zoom %g: %.3f ms/frame (%.0f ns/op)", tc.zoom, ms, ns)
			} else {
				per := ns / float64(tc.n)
				b.ReportMetric(per, "ns/ann")
				b.Logf("%d annotations at zoom %g: %.3f ms/frame (%.0f ns/op, %.0f ns per annotation)",
					tc.n, tc.zoom, ms, ns, per)
			}
			// The §3 target is stated for rectangle annotations; the polygon
			// case is a recorded number, not a target.
			if tc.n == 1000 && tc.zoom == 1 && !tc.poly {
				verdict := "OK, design §3's target is met"
				if ms >= 16 {
					verdict = "MISS, over design §3's target"
				}
				b.Logf("1000 annotations at %.3f ms/frame: %s", ms, verdict)
			}
		})
	}
}
