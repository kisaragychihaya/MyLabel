package app

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/egoist/mygo/ui"
)

// testJPEG encodes a w x h image of one color as JPEG data.
func testJPEG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		row := img.Pix[y*img.Stride:]
		for x := range w {
			i := x * 4
			row[i+0], row[i+1], row[i+2], row[i+3] = c.R, c.G, c.B, 255
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	return buf.Bytes()
}

func TestNeedTiles(t *testing.T) {
	for _, c := range []struct {
		w, h int
		want bool
	}{
		{8000, 8000, false},
		{8000, 100, false},
		{100, 8000, false},
		{8001, 100, true},
		{100, 8001, true},
		{8192, 8192, true},
		{10000, 7500, true},
		{100, 100, false},
		{640, 480, false},
	} {
		if got := needTiles(c.w, c.h); got != c.want {
			t.Errorf("needTiles(%d, %d) = %v，期望 %v", c.w, c.h, got, c.want)
		}
	}
}

func TestTiledImageSingleTile(t *testing.T) {
	data := testJPEG(t, 100, 50, color.RGBA{200, 30, 30, 255})
	tiled, err := newTiledImage(data, 100, 50)
	if err != nil {
		t.Fatalf("newTiledImage: %v", err)
	}
	if tiled.W != 100 || tiled.H != 50 {
		t.Errorf("W,H = %d,%d，期望 100,50", tiled.W, tiled.H)
	}
	if tiled.Cols != 1 || tiled.Rows != 1 {
		t.Errorf("Cols,Rows = %d,%d，期望 1,1", tiled.Cols, tiled.Rows)
	}
	if len(tiled.Tiles) != 1 || len(tiled.Tiles[0]) != 1 || tiled.Tiles[0][0] == nil {
		t.Fatalf("Tiles 形状不对: %v", tiled.Tiles)
	}
	if w, h := tiled.Tiles[0][0].Size(); w != 100 || h != 50 {
		t.Errorf("tile 尺寸 = %dx%d，期望 100x50", w, h)
	}
	if tiled.Overview == nil {
		t.Fatal("Overview 为 nil")
	}
	if w, h := tiled.Overview.Size(); w != 100 || h != 50 {
		t.Errorf("overview 尺寸 = %dx%d，期望 100x50（不放大）", w, h)
	}
}

func TestTiledImageGrid(t *testing.T) {
	// Two columns and two rows, both trailing tiles smaller than tileSize.
	const w, h = 2100, 2050
	data := testJPEG(t, w, h, color.RGBA{30, 120, 200, 255})
	tiled, err := newTiledImage(data, w, h)
	if err != nil {
		t.Fatalf("newTiledImage: %v", err)
	}
	if tiled.Cols != 2 || tiled.Rows != 2 {
		t.Fatalf("Cols,Rows = %d,%d，期望 2,2", tiled.Cols, tiled.Rows)
	}
	if len(tiled.Tiles) != 2 || len(tiled.Tiles[0]) != 2 || len(tiled.Tiles[1]) != 2 {
		t.Fatalf("Tiles 形状不对: %d 行", len(tiled.Tiles))
	}

	want := [][]image.Point{
		{{X: tileSize, Y: tileSize}, {X: w - tileSize, Y: tileSize}},
		{{X: tileSize, Y: h - tileSize}, {X: w - tileSize, Y: h - tileSize}},
	}
	for row := range want {
		for col := range want[row] {
			bmp := tiled.Tiles[row][col]
			if bmp == nil {
				t.Fatalf("Tiles[%d][%d] 为 nil", row, col)
			}
			gw, gh := bmp.Size()
			if gw != want[row][col].X || gh != want[row][col].Y {
				t.Errorf("Tiles[%d][%d] = %dx%d，期望 %dx%d",
					row, col, gw, gh, want[row][col].X, want[row][col].Y)
			}
		}
	}

	if tiled.Overview == nil {
		t.Fatal("Overview 为 nil")
	}
	// DecodeBitmapScaled keeps the aspect ratio within overviewMaxSide.
	ow, oh := tiled.Overview.Size()
	if ow != overviewMaxSide || oh != h*overviewMaxSide/w {
		t.Errorf("overview 尺寸 = %dx%d，期望 %dx%d", ow, oh, overviewMaxSide, h*overviewMaxSide/w)
	}
}

func TestTiledImageBadData(t *testing.T) {
	if tiled, err := newTiledImage([]byte("not an image"), 10000, 10000); err == nil {
		t.Errorf("损坏数据应返回错误，得到 %+v", tiled)
	}
}

// TestTiledImageDraw renders a tiled image through a real painter transform
// and checks the pixels: tiles when zoomed in, the overview when zoomed far
// out (and the tiles again when the overview is missing).
func TestTiledImageDraw(t *testing.T) {
	const w, h = 2100, 2050
	data := testJPEG(t, w, h, color.RGBA{220, 20, 20, 255})
	tiled, err := newTiledImage(data, w, h)
	if err != nil {
		t.Fatalf("newTiledImage: %v", err)
	}

	zoom := float64(1)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Clip().Draw(func(p *ui.Painter, r ui.Rect) {
			p.Fill(r, ui.RGB(0, 0, 0), 0)
			p.Transform(ui.Scaling(float32(zoom), float32(zoom)), r.X, r.Y, func() {
				tiled.Draw(p)
			})
		})
	}, 200, 150)

	check := func(what string) {
		t.Helper()
		tt.Frame()
		img := tt.Image()
		b := img.Bounds()
		px := img.RGBAAt(b.Dx()/2, b.Dy()/2)
		if px.R < 150 || px.G > 90 || px.B > 90 {
			t.Errorf("%s: 视口中心像素 = %v，期望偏红", what, px)
		}
	}

	check("zoom=1（分块）")

	zoom = 0.05 // the visible area covers the whole image: overview
	check("zoom=0.05（概览）")

	tiled.Overview = nil // falls back to tiles when there is no overview
	check("zoom=0.05，无概览（回退分块）")
}
