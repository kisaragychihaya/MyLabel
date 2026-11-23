package app

import (
	"os"
	"sync/atomic"

	"github.com/egoist/mygo/ui"
)

// thumbMaxSide is the thumbnail decode size in pixels.
const thumbMaxSide = 256

// thumbBytesLimit skips thumbnail generation for files larger than this.
const thumbBytesLimit = 64 << 20

// thumbWorkers is the size of the thumbnail worker pool.
const thumbWorkers = 4

// thumbGeneration counts calls to startThumbWorkers. Workers compare the
// generation they started with against this counter before touching the map,
// so workers of a previous folder never write into the new folder's thumbs
// (openDir replaces the map). It is read from goroutines, hence the atomic.
var thumbGeneration atomic.Uint64

// startThumbWorkers launches background goroutines that decode scaled
// thumbnails for all images in a.images and store them via win.Update.
// Safe to call again after openDir; thumbs map has been reset by then.
//
// Without a window there is no main thread to hand the bitmaps to (unit
// tests), and several workers writing a.thumbs at once is a data race: no
// workers run, so those App instances simply show spinners.
func (a *App) startThumbWorkers() {
	if a.win == nil {
		return
	}
	gen := thumbGeneration.Add(1)
	// Snapshot the paths: a.images is replaced when another folder opens.
	paths := make([]string, len(a.images))
	for i := range a.images {
		paths[i] = a.images[i].Path
	}

	queue := make(chan string)
	for range thumbWorkers {
		go a.thumbWorker(gen, queue)
	}
	go func() {
		defer close(queue)
		for _, path := range paths {
			if thumbGeneration.Load() != gen {
				return // another folder opened: abandon the rest
			}
			queue <- path
		}
	}()
}

// thumbWorker decodes thumbnails off the main thread until queue is closed,
// handing each bitmap to the main thread through win.Update (design rule 2).
func (a *App) thumbWorker(gen uint64, queue <-chan string) {
	for path := range queue {
		if thumbGeneration.Load() != gen {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) > thumbBytesLimit {
			continue
		}
		bmp, err := ui.DecodeBitmapScaled(data, thumbMaxSide)
		if err != nil {
			continue
		}
		// The bitmap reaches the main thread only through win.Update (design
		// rule 2); startThumbWorkers never starts without a window.
		a.win.Update(func() {
			if thumbGeneration.Load() == gen {
				a.thumbs[path] = bmp
			}
		})
	}
}

// thumbOf returns the cached thumbnail for path, or nil while pending.
func (a *App) thumbOf(path string) *ui.Bitmap {
	return a.thumbs[path]
}
