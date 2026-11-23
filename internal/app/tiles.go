package app

import (
	"bytes"
	"image"
	"image/draw"
	_ "image/gif"  // image.Decode in newTiledImage
	_ "image/jpeg" // image.Decode in newTiledImage
	_ "image/png"  // image.Decode in newTiledImage
	"os"

	"github.com/egoist/mygo/ui"

	_ "golang.org/x/image/bmp"  // image.Decode in newTiledImage
	_ "golang.org/x/image/webp" // image.Decode in newTiledImage
)

const (
	// tileThreshold: images with either side above this are drawn tiled
	// (conservative, below the documented 8192 renderer limits).
	tileThreshold = 8000
	// tileSize is the tile edge length in image pixels.
	tileSize = 2048
	// overviewMaxSide is the longest side of the whole-image overview bitmap
	// used when the image is zoomed far out.
	overviewMaxSide = 2048
)

// needTiles reports whether an image of size w x h must be drawn tiled.
func needTiles(w, h int) bool {
	return w > tileThreshold || h > tileThreshold
}

// TiledImage draws a very large image as a grid of tiles so no single GPU
// texture exceeds the renderer limit. PoC simplification: the decoded image
// is kept in memory whole; tiles are pre-cut *ui.Bitmap. A small overview
// bitmap serves zoomed-out views.
//
// Known limitation (design §11): every tile holds its own pixel copy, so a
// tiled image costs about its own size in RGBA memory for the tiles (plus the
// transient copy of the decoded image while newTiledImage runs). Nothing is
// loaded lazily from the original file yet.
type TiledImage struct {
	// Overview is the whole image shrunk to at most overviewMaxSide pixels.
	// It is drawn instead of the tile grid when the visible area covers the
	// whole image, where every tile would shrink to a few pixels. Might be
	// nil: Draw falls back to the tiles then.
	Overview *ui.Bitmap
	// Tiles is the tile grid, Tiles[row][col], row-major from the top left.
	// Every tile is at most tileSize on a side; the last column and the last
	// row are shorter. Beyond W/H there are no tiles.
	Tiles [][]*ui.Bitmap
	// W and H are the image size in pixels.
	W, H int
	// Cols and Rows are the tile grid dimensions.
	Cols, Rows int
}

// newTiledImage decodes data and prepares the tile grid for a w x h image.
//
// The image is decoded with the standard library rather than
// ui.DecodeBitmap: cutting tiles needs pixel access, which *ui.Bitmap does not
// offer. Each tile is copied into its own image.RGBA, which ui.NewBitmap
// adopts without another copy. EXIF orientation is not applied here, so a
// rotated huge photo would be tiled unrotated (design §11).
func newTiledImage(data []byte, w, h int) (*TiledImage, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	if w <= 0 || h <= 0 {
		w, h = b.Dx(), b.Dy()
	}
	t := &TiledImage{
		W:    w,
		H:    h,
		Cols: (w + tileSize - 1) / tileSize,
		Rows: (h + tileSize - 1) / tileSize,
	}
	t.Tiles = make([][]*ui.Bitmap, t.Rows)
	for row := range t.Rows {
		t.Tiles[row] = make([]*ui.Bitmap, t.Cols)
		for col := range t.Cols {
			x0, y0 := col*tileSize, row*tileSize
			tw, th := min(tileSize, w-x0), min(tileSize, h-y0)
			if tw <= 0 || th <= 0 {
				continue
			}
			tile := image.NewRGBA(image.Rect(0, 0, tw, th))
			draw.Draw(tile, tile.Bounds(), img, image.Pt(x0, y0).Add(b.Min), draw.Src)
			t.Tiles[row][col] = ui.NewBitmap(tile)
		}
	}
	// The overview comes from the encoder's own downscaling, which is much
	// cheaper than shrinking the tiles here. Failing to make it is not fatal:
	// Draw then always uses the tiles.
	if ov, err := ui.DecodeBitmapScaled(data, overviewMaxSide); err == nil {
		t.Overview = ov
	}
	return t, nil
}

// Draw renders the visible tiles (and overview when zoomed far out) inside
// the current painter transform, at rect (0,0,W,H) in image coordinates.
//
// Zoomed-out detection is self-contained: when the visible area (Painter.
// Visible, in image coordinates under the caller's transform) covers the whole
// image, the overview is drawn as one rectangle; otherwise only the tiles that
// intersect it are drawn, each through ImageRegion so no tile texture is drawn
// outside the viewport.
func (t *TiledImage) Draw(p *ui.Painter) {
	if t == nil || t.W <= 0 || t.H <= 0 {
		return
	}
	whole := ui.Rect{X: 0, Y: 0, W: float32(t.W), H: float32(t.H)}
	vis := p.Visible()
	if t.Overview != nil && vis.X <= 0 && vis.Y <= 0 &&
		vis.X+vis.W >= whole.W && vis.Y+vis.H >= whole.H {
		p.ImageRegion(t.Overview, ui.Rect{}, whole, ui.ImageOptions{})
		return
	}
	for row := range t.Rows {
		for col := range t.Cols {
			bmp := t.Tiles[row][col]
			if bmp == nil {
				continue
			}
			tw, th := bmp.Size()
			dst := ui.Rect{
				X: float32(col * tileSize),
				Y: float32(row * tileSize),
				W: float32(tw),
				H: float32(th),
			}
			// Outside the visible rectangle: skip the op altogether.
			if dst.X >= vis.X+vis.W || dst.Y >= vis.Y+vis.H ||
				dst.X+dst.W <= vis.X || dst.Y+dst.H <= vis.Y {
				continue
			}
			p.ImageRegion(bmp, ui.Rect{W: dst.W, H: dst.H}, dst, ui.ImageOptions{})
		}
	}
}

// loadImage loads images[i] as the main image: whole bitmap when small
// enough, tiled otherwise. It resets bmp/tiled/imgW/imgH but not the
// annotation selection (caller's job).
//
// On error the previously loaded image stays in place and lastStatus tells
// why. A successful load asks the next frame to fit the image to the canvas
// (fitOnLoad): only Draw knows the canvas's size.
func (a *App) loadImage(i int) {
	if i < 0 || i >= len(a.images) {
		return
	}
	info := &a.images[i]
	data, err := os.ReadFile(info.Path)
	if err != nil {
		a.lastStatus = "读取失败 " + info.Name + ": " + err.Error()
		return
	}

	w, h := info.Width, info.Height
	var (
		bmp   *ui.Bitmap
		tiled *TiledImage
	)
	if needTiles(w, h) {
		if tiled, err = newTiledImage(data, w, h); err == nil {
			w, h = tiled.W, tiled.H
		}
	} else if bmp, err = ui.DecodeBitmap(data); err == nil && (w <= 0 || h <= 0) {
		// The import always records the dimensions; a row without them still
		// draws at the decoded size instead of not at all.
		w, h = bmp.Size()
	}
	if err != nil {
		a.lastStatus = "解码失败 " + info.Name + ": " + err.Error()
		return
	}
	info.Width, info.Height = w, h

	a.bmp, a.tiled = bmp, tiled
	a.imgW, a.imgH = float64(w), float64(h)
	a.fitOnLoad = true
	a.focusCanvas = true
}
