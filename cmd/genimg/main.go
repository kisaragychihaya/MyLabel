// Command genimg generates deterministic test JPEGs for the MyLabel PoC:
// a gradient with pseudo-random color blocks per index, plus the two
// oversized images that exercise the tiled drawing path.
//
//	go run ./cmd/genimg -dir ./testdata/imgs -n 10000
//	go run ./cmd/genimg -dir ./testdata/imgs -big
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// job is one image to write.
type job struct {
	name string
	w, h int
	seed int64
}

func main() {
	dir := flag.String("dir", "", "输出目录（必填）")
	n := flag.Int("n", 100, "生成图片数量")
	w := flag.Int("w", 640, "图片宽度")
	h := flag.Int("h", 480, "图片高度")
	big := flag.Bool("big", false, "只生成超大图：8192x8192 与 10000x7500 各一张（忽略 -n -w -h）")
	flag.Parse()

	if *dir == "" {
		fmt.Fprintln(os.Stderr, "genimg: -dir 必填")
		flag.Usage()
		os.Exit(2)
	}
	if !*big && (*n < 0 || *w < 1 || *h < 1) {
		fmt.Fprintln(os.Stderr, "genimg: -n 需 >= 0，-w/-h 需 >= 1")
		os.Exit(2)
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "genimg:", err)
		os.Exit(1)
	}

	var jobs []job
	if *big {
		jobs = append(jobs,
			job{name: "big_8192.jpg", w: 8192, h: 8192, seed: 1},
			job{name: "big_10000.jpg", w: 10000, h: 7500, seed: 2},
		)
	} else {
		jobs = make([]job, *n)
		for i := range jobs {
			jobs[i] = job{name: fmt.Sprintf("img_%05d.jpg", i), w: *w, h: *h, seed: int64(i)}
		}
	}

	start := time.Now()
	if err := run(*dir, jobs); err != nil {
		fmt.Fprintln(os.Stderr, "genimg:", err)
		os.Exit(1)
	}
	elapsed := time.Since(start)

	files, size, err := dirStats(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "genimg:", err)
		os.Exit(1)
	}
	fmt.Printf("完成：%d 个文件，共 %.1f MB，耗时 %.2fs\n", files, float64(size)/(1<<20), elapsed.Seconds())
}

// run writes every job with a worker pool, printing progress as it goes.
func run(dir string, jobs []job) error {
	workers := min(runtime.GOMAXPROCS(0), 8)
	if workers < 1 {
		workers = 1
	}
	total := len(jobs)
	step := max(total/20, 1)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		done    atomic.Int64
		firstEr error
	)
	ch := make(chan job)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				if err := writeJPEG(filepath.Join(dir, j.name), j.w, j.h, j.seed); err != nil {
					mu.Lock()
					if firstEr == nil {
						firstEr = err
					}
					mu.Unlock()
					continue
				}
				d := int(done.Add(1))
				if d%step == 0 || d == total {
					fmt.Printf("生成 %d/%d\n", d, total)
				}
			}
		}()
	}
	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	wg.Wait()
	return firstEr
}

// writeJPEG draws job's picture and encodes it as JPEG quality 80.
func writeJPEG(path string, w, h int, seed int64) error {
	img := picture(w, h, seed)
	var buf bytes.Buffer
	buf.Grow(w * h / 8)
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// picture draws a two-color diagonal gradient plus pseudo-random color blocks,
// both driven by seed: every seed gives a different picture and a slightly
// different file size, as real photos would.
func picture(w, h int, seed int64) *image.RGBA {
	rng := rand.New(rand.NewSource(seed))
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	// Gradient corners, offset by the seed so that the pictures differ.
	off := uint8(rng.Intn(64))
	c0 := color.RGBA{uint8(rng.Intn(128)) + off, uint8(rng.Intn(128)) + off, uint8(rng.Intn(128)) + off, 255}
	c1 := color.RGBA{uint8(rng.Intn(128)) + 96, uint8(rng.Intn(128)) + 96, uint8(rng.Intn(128)) + 96, 255}

	// Integer diagonal ramp: fast enough for 10000 images.
	maxX, maxY := max(w-1, 1), max(h-1, 1)
	for y := range h {
		row := img.Pix[y*img.Stride:]
		fy := y * 256 / maxY
		for x := range w {
			v := (x*256/maxX + fy) / 2 // 0..256
			i := x * 4
			row[i+0] = uint8((int(c0.R)*(256-v) + int(c1.R)*v) >> 8)
			row[i+1] = uint8((int(c0.G)*(256-v) + int(c1.G)*v) >> 8)
			row[i+2] = uint8((int(c0.B)*(256-v) + int(c1.B)*v) >> 8)
			row[i+3] = 255
		}
	}

	// Color blocks, so that neighbouring pictures never encode to the same
	// bytes and file sizes spread out as they do for real photos.
	blocks := 8 + rng.Intn(8)
	for range blocks {
		bw := 1 + rng.Intn(max(w/3, 1))
		bh := 1 + rng.Intn(max(h/3, 1))
		x0 := rng.Intn(max(w-bw, 1))
		y0 := rng.Intn(max(h-bh, 1))
		r := image.Rect(x0, y0, min(x0+bw, w), min(y0+bh, h))
		c := color.RGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255}
		draw.Draw(img, r, &image.Uniform{C: c}, image.Point{}, draw.Src)
	}
	return img
}

// dirStats counts the regular files in dir and their total size.
func dirStats(dir string) (files int, size int64, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files++
		size += info.Size()
	}
	return files, size, nil
}
