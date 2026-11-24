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
//
// The first collected point starts encoding the image right away, in the
// background (§6): by the time the user double-clicks, the heavy half of the work
// is usually already cached and the run only has to decode. Encoding ahead is
// best effort — silent, at most one in flight, dropped when the image or the
// generation moved on — and it never changes what the inference does: a
// double-click whose embedding is not cacheable yet simply encodes again.

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

// aiEncodeFunc is the injectable encoder: it turns one image into the embedding
// the decoder prompts against. A nil field means the real client encodes
// (readRGB24 + sam.Encode); tests replace it so no DLL is needed (§8).
type aiEncodeFunc func(info *ImageInfo, imgIndex int) (sam2.Embedding, error)

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
	if len(a.aiPoints) == 1 {
		// The first point is the moment the inference becomes likely: start the
		// encoder now, in the background, so the double-click that follows finds
		// the embedding waiting (§6).
		a.aiEncodeAhead()
	}
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

// aiEncodeAhead encodes the current image in the background as soon as the first
// foreground point is collected (§6), so the double-click that follows has only
// the decode left to do. It is deliberately silent: no status, no aiBusy, and a
// failure is simply forgotten — the next first click tries again, and the
// inference itself reports whatever goes wrong when the user actually asks for a
// mask. It runs on the main thread, since it decides what the cache will hold;
// the encode it starts never reads App state. It starts nothing while an
// inference is in flight — the reason is in the guard below.
func (a *App) aiEncodeAhead() {
	img := a.curImage()
	if img == nil {
		return
	}
	imgIndex := a.loaded
	// One encode at a time, app-wide: this skips the image already being encoded
	// and any other image's encode still in flight, and leaves an image whose
	// embedding the cache already holds alone.
	if a.aiEmbFor == imgIndex || a.aiEncBusy {
		return
	}
	// Not while an inference runs. This is not about the trigger (a double-click
	// during a run is refused anyway): it is about the landing. The cache handle
	// aiAdoptEmbedding releases must have no other owner, and the one way it can
	// have one is a run that started decoding with it and then saw the image
	// switch — aiReleaseEmbedding defers that native release to aiFinish, so the
	// handle sits in a.aiEmb while a worker still reads it. Waiting for aiBusy to
	// clear means the landing's gen check (no trigger, no Esc, no image switch
	// since the encode started) is also proof that nobody holds the handle.
	if a.aiBusy {
		return
	}
	hook, samClient := a.aiEncodeHook, a.sam
	if hook == nil && samClient == nil {
		return
	}
	// Its own generation, shared with the inferences: Esc, an image switch and a
	// new trigger all bump the counter, so an encode that lands afterwards can
	// tell it is stale instead of overwriting the cache of what replaced it.
	a.aiGen++
	gen := a.aiGen
	a.aiEncBusy, a.aiEncFor = true, imgIndex

	info := *img
	win := a.win
	if win == nil {
		emb, err := aiEncode(&info, imgIndex, hook, samClient)
		a.aiEncodeLanded(emb, err, gen, imgIndex)
		return
	}
	go func() {
		emb, err := aiEncode(&info, imgIndex, hook, samClient)
		win.Update(func() { a.aiEncodeLanded(emb, err, gen, imgIndex) })
	}()
}

// aiEncode runs one encoder pass without touching App state: the hook a test
// injected, otherwise the real client over the image file. Everything it needs
// is handed in, because with a window it runs on a goroutine (design rule 2).
func aiEncode(info *ImageInfo, imgIndex int, hook aiEncodeFunc, sam *sam2.Client) (sam2.Embedding, error) {
	if hook != nil {
		return hook(info, imgIndex)
	}
	if sam == nil {
		return sam2.Embedding{}, errors.New("ai: the native library is not available")
	}
	rgb, w, h, err := readRGB24(info.Path)
	if err != nil {
		return sam2.Embedding{}, err
	}
	emb, err := sam.Encode(rgb, w, h, w*3)
	if err != nil {
		return sam2.Embedding{}, err
	}
	// Warm the decoder net too: its first Decode lazily creates the net, which
	// costs seconds on Vulkan (decoder_create ≈ 3.2s on the RTX 4060, the same
	// order as the encode). A throwaway decode against the fresh embedding hides
	// that in the background as well, so the double-click that follows is decode
	// only. Best effort: a warmup failure changes nothing — the real decode
	// reports it when the user asks for a mask.
	if _, werr := sam.Decode(emb, []float32{float32(w) / 2, float32(h) / 2}, []int32{1}); werr != nil {
		// silent: the inference re-runs the decode and surfaces the error
	}
	return emb, nil
}

// aiEncodeLanded hands a finished encode back to the main thread and clears the
// busy flag first, whatever happens next. A failure is dropped in silence: the
// user asked for nothing visible. A run whose generation or image no longer
// matches is stale — its embedding is released here and the cache keeps what the
// current image's own runs put there (§6).
func (a *App) aiEncodeLanded(emb sam2.Embedding, err error, gen uint64, imgIndex int) {
	a.aiEncBusy, a.aiEncFor = false, -1
	if err != nil {
		return
	}
	if gen != a.aiGen || imgIndex != a.loaded {
		emb.Release()
		return
	}
	a.aiAdoptEmbedding(emb, imgIndex)
}

// aiAdoptEmbedding makes emb the cache entry for imgIndex, releasing the
// embedding it replaces. This is the only writer of the cache, so the older
// handle has a single owner and is freed exactly once.
//
// The invariant it relies on: the handle being replaced is not in use by any
// running inference. aiEncodeAhead refuses to start while aiBusy, and its
// landing only adopts when its gen still matches — which means no trigger, no Esc
// and no image switch happened meanwhile — so nothing can be holding the old
// handle when the encode-ahead adopts. aiFinish runs after its own worker
// returned, and a run that encodes (fresh) was not decoding with the cache.
//
// Two producers land here — the encode-ahead, between the clicks, and aiFinish —
// and the generation they share is what keeps them from interleaving badly: only
// the run that is still current passes its gen check, so a pre-encoded embedding
// is either what the inference decodes with (it is in the cache by then) or is
// replaced by the one that inference encoded itself. When both encode, the cost
// is one extra Encode and no correctness risk: each handle has one owner (freed
// here, or by the run that lands stale), and the native client serializes calls
// on a handle with its own mutex (sam2_ncnn.cpp, enc->mu for the encoder and the
// decoder's for the decoder), so two threads encoding is safe, just serial.
func (a *App) aiAdoptEmbedding(emb sam2.Embedding, imgIndex int) {
	a.aiEmb.Release()
	a.aiEmb = emb
	a.aiEmbFor = imgIndex
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
		// The run encoded the image itself (the cache held nothing usable when it
		// started): its embedding becomes the cache entry.
		a.aiAdoptEmbedding(res.emb, imgIndex)
	} else {
		// It decoded with the cache, so the entry stays where it is.
		a.aiEmbFor = imgIndex
	}

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
