package app

import (
	"bytes"
	"errors"
	"image"
	"os"

	"mylabel/internal/maskgeom"
	"mylabel/internal/sam2"
	"mylabel/locales"
)

// The AI assist (docs/phase7-design.md): the 智能标注 tool collects foreground
// points on the canvas, a double-click runs SAM2 over them, and the resulting
// mask becomes an ordinary annotation — its traced outline, or its bounding box
// in the tool's other mode. The whole pipeline is §3 of the contract; the tool
// only exists when the native library was detected at startup, so a machine
// without it keeps the seven-button strip and notices nothing (§2, §4).

const (
	// aiThreshold is the mask threshold: a logit above 0 is the same decision as
	// sigmoid > 0.5, which is what the libsam2_ncnn QC gate compares (§3).
	aiThreshold = 0
	// aiSimplifyEpsilon is Douglas-Peucker's tolerance for the outline, in mask
	// pixels (§3).
	aiSimplifyEpsilon = 1.5
	// aiCrossDIP is the arm length of a collected point's cross marker, on
	// screen: the marks keep their size at any zoom, like the handles.
	aiCrossDIP = 6
)

// errAINoMask reports a mask without any foreground, or one whose foreground is
// too small to be a shape: the model found nothing at the collected points.
var errAINoMask = errors.New("ai: the mask has no foreground")

// aiPredict is the injectable half of the pipeline: it runs one inference and
// returns the mask its prompt produced. A nil field means the real client
// (aiInferSAM2) answers; tests replace it so no DLL is needed (§8).
type aiPredictFunc func(img *ImageInfo, pts []Point) (mask []float32, mw, mh int, err error)

// aiRun is one inference's outcome, handed from the worker (or the inline path)
// back to the main thread, which alone touches the embedding cache.
type aiRun struct {
	mask   []float32
	mw, mh int
	// emb is the embedding this run produced, valid only when fresh is set: an
	// embedding a reused cache entry already holds must not be released here.
	emb   sam2.Embedding
	fresh bool
	err   error
}

// aiReady reports whether the AI tool exists in this session: the native library
// was detected, or a test injected its own predictor. Everything the tool
// changes on screen — the strip's eighth button, the S shortcut — hangs off it,
// so a machine without the DLL notices nothing (§2).
func (a *App) aiReady() bool { return a.sam != nil || a.aiPredict != nil }

// aiClick handles one left press of the AI tool at image point p (§2): a press
// collects a foreground point, and the second press of a double-click moves the
// last point to where the pointer is and runs the inference instead of adding
// one.
func (a *App) aiClick(p Point, clicks int) {
	if len(a.categories) == 0 {
		// The same block the three creation tools have: an annotation needs a
		// category (contract §3).
		a.lastStatus = locales.StatusNoCategories()
		return
	}
	if clicks >= 2 && len(a.aiPoints) > 0 {
		a.aiPoints[len(a.aiPoints)-1] = p
		a.aiGenerate()
		return
	}
	a.aiPoints = append(a.aiPoints, p)
}

// aiDropPoints forgets the collected points and invalidates every inference
// running for them: a result that comes back afterwards finds a bumped
// generation and is dropped (§6).
func (a *App) aiDropPoints() {
	a.aiPoints = nil
	a.aiGen++
}

// aiReleaseEmbedding drops the cached embedding. While an inference is in
// flight the native handle it is decoding with stays alive: the worker holds the
// only reference, so the release waits for aiFinish, which runs once that worker
// is done. Freeing it underneath the decoder would take the process down.
func (a *App) aiReleaseEmbedding() {
	a.aiEmbFor = -1
	if a.aiBusy {
		return
	}
	a.aiEmb.Release()
}

// aiGenerate turns the collected points into an annotation (§3). The encoder and
// the decoder run off the main thread and hand their result back through
// win.Update (design rule 2); without a window there is no thread to hand it to,
// so the same work runs inline, as exportStart does.
func (a *App) aiGenerate() {
	if a.aiBusy || len(a.aiPoints) == 0 || !a.aiReady() {
		return
	}
	img := a.curImage()
	if img == nil {
		return
	}
	// One generation per run: Esc, an image switch and a new trigger all bump the
	// counter, which is what makes a stale result recognizably stale (§6).
	a.aiGen++
	gen := a.aiGen
	a.aiBusy = true

	// Everything the worker touches is copied out here: the image it reads, the
	// prompt it sends, and the identity the annotation will get if the run still
	// matters when it lands.
	info := *img
	imgIndex := a.loaded
	pts := append([]Point(nil), a.aiPoints...)
	cat, id := a.curCat, newAnnID()
	w, h := float64(info.Width), float64(info.Height)

	// The status says which half of the work this run starts with: the first
	// inference for an image has to encode it, the ones after it only decode (§3).
	if a.aiEmbFor == imgIndex {
		a.lastStatus = locales.StatusAIDecoding()
	} else {
		a.lastStatus = locales.StatusAIEncoding()
	}

	win := a.win
	// The cache state is snapshotted here, on the main thread: the worker must
	// not touch a.aiEmb/a.aiEmbFor, which the main thread can change while it
	// runs (aiReleaseEmbedding defers only the native release, not the flag).
	cached, cachedFor := a.aiEmb, a.aiEmbFor
	if win == nil {
		a.aiFinish(a.aiInfer(&info, imgIndex, pts, cached, cachedFor), gen, imgIndex, cat, id, w, h)
		return
	}
	go func() {
		res := a.aiInfer(&info, imgIndex, pts, cached, cachedFor)
		win.Update(func() { a.aiFinish(res, gen, imgIndex, cat, id, w, h) })
	}()
}

// aiInfer runs one inference: the injected predictor when a test supplied one,
// otherwise the real client over the cached embedding handed in. It never reads
// or writes App state — the embedding cache is adopted and released by aiFinish
// alone, so a worker and the main thread never share a variable (design rule 2).
func (a *App) aiInfer(img *ImageInfo, imgIndex int, pts []Point, cached sam2.Embedding, cachedFor int) aiRun {
	if a.aiPredict != nil {
		mask, mw, mh, err := a.aiPredict(img, pts)
		return aiRun{mask: mask, mw: mw, mh: mh, err: err}
	}
	if a.sam == nil {
		return aiRun{err: errors.New("ai: the native library is not available")}
	}

	emb, fresh := cached, false
	if cachedFor != imgIndex {
		rgb, w, h, err := readRGB24(img.Path)
		if err != nil {
			return aiRun{err: err}
		}
		if emb, err = a.sam.Encode(rgb, w, h, w*3); err != nil {
			return aiRun{err: err}
		}
		fresh = true
	}

	flat := make([]float32, 0, 2*len(pts))
	for _, p := range pts {
		flat = append(flat, float32(p.X), float32(p.Y))
	}
	labels := make([]int32, len(pts))
	for i := range labels {
		labels[i] = 1 // every collected point is a foreground point (§3)
	}
	res, err := a.sam.Decode(emb, flat, labels)
	if err != nil {
		if fresh {
			emb.Release()
		}
		return aiRun{err: err}
	}
	best := res.Best()
	if best < 0 {
		if fresh {
			emb.Release()
		}
		return aiRun{err: errAINoMask}
	}
	return aiRun{mask: res.Masks[best], mw: res.W, mh: res.H, emb: emb, fresh: fresh}
}

// aiFinish adopts the run's embedding, builds the annotation and pushes it as one
// undo step. A run whose generation or image no longer matches is dropped, and
// whatever embedding it produced is released here (§6).
func (a *App) aiFinish(res aiRun, gen uint64, imgIndex int, cat int, id string, w, h float64) {
	a.aiBusy = false
	if gen != a.aiGen || imgIndex != a.loaded {
		// The user moved on: Esc, another image, another folder. The run's own
		// embedding is freed here, and so is the cache entry it may have decoded
		// with — aiReleaseEmbedding deferred that release to this point.
		if res.fresh {
			res.emb.Release()
		}
		a.aiReleaseEmbedding()
		return
	}
	if res.err != nil {
		if res.fresh {
			res.emb.Release()
		}
		if errors.Is(res.err, errAINoMask) {
			a.lastStatus = locales.StatusAINoMask()
			return
		}
		a.lastStatus = locales.StatusAIFailed(res.err.Error())
		return
	}
	if res.fresh {
		// Only the main thread replaces the cache, so the older embedding (if any)
		// is released here and nowhere else.
		a.aiEmb.Release()
		a.aiEmb = res.emb
	}
	a.aiEmbFor = imgIndex

	ann, err := buildAIAnn(res.mask, res.mw, res.mh, w, h, a.aiRectMode, cat, id)
	if err != nil {
		if errors.Is(err, errAINoMask) {
			a.lastStatus = locales.StatusAINoMask()
			return
		}
		a.lastStatus = locales.StatusAIFailed(err.Error())
		return
	}
	a.push(cmdCreate(a, imgIndex, ann))
	// The points are spent: the mask is the answer they asked for. The new
	// annotation is left unselected, as a creation tool's is (§6.2 of phase 6).
	a.aiPoints = nil
	a.lastStatus = locales.StatusAIDone(aiKindName(a.aiRectMode))
}

// aiKindName is the shape name statusAIDone reports.
func aiKindName(rectMode bool) string {
	if rectMode {
		return locales.PropsKindRect()
	}
	return locales.PropsKindPolygon()
}

// buildAIAnn turns one mask (mw×mh logits) into an annotation over an imgW×imgH
// image (§3): the largest 4-connected component is kept, and rectMode takes its
// bounding box while the polygon mode takes its traced and simplified outline.
// A mask pixel (x,y) maps to the centre of the mask cell it names,
// (x+0.5)*imgW/mw, for the outline and the box alike — so a solid rectangle
// traced as a polygon and boxed in rectangle mode describe the same shape.
func buildAIAnn(m []float32, mw, mh int, imgW, imgH float64, rectMode bool, cat int, id string) (Ann, error) {
	if mw <= 0 || mh <= 0 || len(m) < mw*mh {
		return Ann{}, errAINoMask
	}
	// An annotation holds one shape, and the model happily adds specks elsewhere:
	// only the largest component survives.
	largest := maskgeom.LargestComponent(m, mw, mh, aiThreshold)
	mapPt := func(p maskgeom.Pt) Point {
		return Point{
			X: (float64(p.X) + 0.5) * imgW / float64(mw),
			Y: (float64(p.Y) + 0.5) * imgH / float64(mh),
		}
	}

	if rectMode {
		minX, minY, maxX, maxY, ok := maskgeom.BBox(largest, mw, mh, aiThreshold)
		if !ok {
			return Ann{}, errAINoMask
		}
		a, b := mapPt(maskgeom.Pt{X: minX, Y: minY}), mapPt(maskgeom.Pt{X: maxX, Y: maxY})
		return Ann{
			ID: id, Cat: cat, Kind: KindRect,
			CX: (a.X + b.X) / 2, CY: (a.Y + b.Y) / 2,
			W: b.X - a.X, H: b.Y - a.Y,
		}, nil
	}

	contour, err := maskgeom.TraceContour(largest, mw, mh, aiThreshold)
	if err != nil {
		return Ann{}, errAINoMask
	}
	outline := maskgeom.Simplify(contour, aiSimplifyEpsilon)
	if len(outline) < minPolyVertices {
		return Ann{}, errAINoMask
	}
	pts := make([]Point, len(outline))
	for i, p := range outline {
		pts[i] = mapPt(p)
	}
	return newPolyAnn(id, cat, pts), nil
}

// readRGB24 decodes an image file into the tightly packed RGB24 buffer
// sam2.Encode expects, returning its size. The scan already applied the EXIF
// rotation physically (internal/imaging), so the pixels decoded here are the ones
// the canvas shows; the decoders are the ones tiles.go registers for the app.
func readRGB24(path string) (rgb []byte, w, h int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, 0, err
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, err
	}
	b := src.Bounds()
	w, h = b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, 0, 0, errAINoMask
	}
	rgb = make([]byte, w*h*3)
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := src.At(x, y).RGBA()
			rgb[i], rgb[i+1], rgb[i+2] = byte(r>>8), byte(g>>8), byte(bl>>8)
			i += 3
		}
	}
	return rgb, w, h, nil
}
