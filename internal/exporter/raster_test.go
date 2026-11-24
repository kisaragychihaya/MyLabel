package exporter

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"strings"
	"testing"
)

// maskArt renders a mask as one line per row: '.' for the background and the
// palette index otherwise. Only masks with single-digit indices should use it.
func maskArt(m *image.Paletted) string {
	b := m.Bounds()
	rows := make([]string, b.Dy())
	for y := 0; y < b.Dy(); y++ {
		var sb strings.Builder
		for x := 0; x < b.Dx(); x++ {
			if v := m.ColorIndexAt(x, y); v == 0 {
				sb.WriteByte('.')
			} else {
				sb.WriteString(strconv.FormatUint(uint64(v), 10))
			}
		}
		rows[y] = sb.String()
	}
	return strings.Join(rows, "\n")
}

func TestNewVOCPalette(t *testing.T) {
	p := newVOCPalette()
	if len(p) != vocMaskPaletteSize {
		t.Fatalf("len = %d, 期望 %d", len(p), vocMaskPaletteSize)
	}
	if got, want := p[vocMaskBackground], (color.RGBA{A: 0xff}); got != color.Color(want) {
		t.Fatalf("背景色 = %v, 期望不透明黑", got)
	}
	// The first official entry and a wrap-around one.
	if got, want := p[1], (color.RGBA{R: 128, A: 0xff}); got != color.Color(want) {
		t.Fatalf("palette[1] = %v, 期望 (128,0,0)", got)
	}
	// The official colours fill 1..20; cycling restarts at entry 21.
	if got, want := p[len(vocLabelColors)+1], (color.RGBA{R: 128, A: 0xff}); got != color.Color(want) {
		t.Fatalf("palette[%d] = %v, 期望循环复用 palette[1]", len(vocLabelColors)+1, got)
	}
	if got := p[len(vocLabelColors)+2]; got != p[2] {
		t.Fatalf("palette[%d] = %v, 期望循环复用 palette[2] = %v", len(vocLabelColors)+2, got, p[2])
	}
	// Last usable class value recycles the official colours too.
	if got, want := p[vocMaskMaxClass], p[(vocMaskMaxClass-1)%len(vocLabelColors)+1]; got != want {
		t.Fatalf("palette[%d] = %v, 期望循环复用 %v", vocMaskMaxClass, got, want)
	}
	if got, want := p[vocMaskBorder], (color.RGBA{R: 224, G: 224, B: 192, A: 0xff}); got != color.Color(want) {
		t.Fatalf("palette[255] = %v, 期望 border 白 (224,224,192)", got)
	}
}

func TestRasterizeRingScanline(t *testing.T) {
	// Triangle (0,0)-(4,0)-(0,6) on an 8x6 mask: pixel centres (x+0.5, y+0.5)
	// inside the triangle are filled, so each further row loses one pixel.
	m := newVOCMask(8, 6)
	ring := [][2]float64{{0, 0}, {4, 0}, {0, 6}}
	painted, clipped := rasterizeRing(m, ring, 1)
	if !painted || clipped {
		t.Fatalf("painted = %v, clipped = %v, 期望 true/false", painted, clipped)
	}
	want := strings.Join([]string{
		"1111....",
		"111.....",
		"11......",
		"11......",
		"1.......",
		"........",
	}, "\n")
	if got := maskArt(m); got != want {
		t.Fatalf("掩码 =\n%s\n期望\n%s", got, want)
	}
}

func TestRasterizeRingClipAndDegenerate(t *testing.T) {
	// A ring reaching outside the mask is clipped, and only in-bounds pixels
	// are painted.
	m := newVOCMask(4, 4)
	ring := [][2]float64{{-10, -10}, {2, -10}, {2, 2}, {-10, 2}}
	painted, clipped := rasterizeRing(m, ring, 3)
	if !painted || !clipped {
		t.Fatalf("painted = %v, clipped = %v, 期望 true/true", painted, clipped)
	}
	if got, want := maskArt(m), "33..\n33..\n....\n...."; got != want {
		t.Fatalf("掩码 =\n%s\n期望\n%s", got, want)
	}

	// Entirely outside: nothing painted, still reported as clipped.
	m = newVOCMask(4, 4)
	if painted, clipped := rasterizeRing(m, [][2]float64{{10, 10}, {20, 10}, {20, 20}}, 1); painted || !clipped {
		t.Fatalf("越界环 painted = %v, clipped = %v, 期望 false/true", painted, clipped)
	}

	// Fewer than three vertices cannot enclose a pixel.
	if painted, _ := rasterizeRing(newVOCMask(4, 4), [][2]float64{{0, 0}, {4, 4}}, 1); painted {
		t.Fatal("两点环不应画出像素")
	}
	if painted, _ := rasterizeRing(nil, [][2]float64{{0, 0}, {4, 0}, {0, 4}}, 1); painted {
		t.Fatal("nil 掩码不应 panic 或画出像素")
	}
}

// TestRasterizeRingBridgedHole: bridging an outer ring with an inner one (same
// orientation) yields an even-odd fill that keeps the inner area background —
// the VOC mask has no hole concept, so this is the closest approximation.
func TestRasterizeRingBridgedHole(t *testing.T) {
	outer := [][2]float64{{1, 1}, {7, 1}, {7, 5}, {1, 5}}
	inner := [][2]float64{{3, 2}, {5, 2}, {5, 4}, {3, 4}}
	ring := bridgeRings([][][2]float64{outer, inner})

	m := newVOCMask(8, 6)
	if painted, _ := rasterizeRing(m, ring, 1); !painted {
		t.Fatal("桥接环应画出像素")
	}
	// Row 3 crosses the inner ring: ground on both sides, background inside.
	rows := strings.Split(maskArt(m), "\n")
	if rows[3] != ".11..11." {
		t.Fatalf("第 3 行 = %q, 期望 %q（内环镂空）:\n%s", rows[3], ".11..11.", maskArt(m))
	}
	if v := m.ColorIndexAt(4, 3); v != 0 {
		t.Fatalf("内环内部像素 = %d, 期望背景 0", v)
	}
	if v := m.ColorIndexAt(2, 3); v != 1 {
		t.Fatalf("内环左侧像素 = %d, 期望 1", v)
	}
	if v := m.ColorIndexAt(6, 3); v != 1 {
		t.Fatalf("内环右侧像素 = %d, 期望 1", v)
	}
}

func TestEncodeVOCMaskPNG(t *testing.T) {
	m := newVOCMask(5, 3)
	if painted, _ := rasterizeRing(m, [][2]float64{{0, 0}, {3, 0}, {3, 2}, {0, 2}}, 7); !painted {
		t.Fatal("应画出像素")
	}
	data, err := encodeVOCMask(m)
	if err != nil {
		t.Fatalf("encodeVOCMask: %v", err)
	}
	// PNG signature + IHDR: palette image (colour type 3), 8 bits per index.
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("缺少 PNG 签名: % x", data[:8])
	}
	if got := binary.BigEndian.Uint32(data[8:12]); got != 13 {
		t.Fatalf("IHDR 长度 = %d, 期望 13", got)
	}
	if string(data[12:16]) != "IHDR" {
		t.Fatalf("块类型 = %q, 期望 IHDR", data[12:16])
	}
	if w := binary.BigEndian.Uint32(data[16:20]); w != 5 {
		t.Fatalf("宽 = %d, 期望 5", w)
	}
	if h := binary.BigEndian.Uint32(data[20:24]); h != 3 {
		t.Fatalf("高 = %d, 期望 3", h)
	}
	if bitDepth, colorType := data[24], data[25]; bitDepth != 8 || colorType != 3 {
		t.Fatalf("bitDepth/colorType = %d/%d, 期望 8/3（调色板）", bitDepth, colorType)
	}

	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	pm, ok := decoded.(*image.Paletted)
	if !ok {
		t.Fatalf("解码类型 = %T, 期望 *image.Paletted", decoded)
	}
	if art := maskArt(pm); art != "777..\n777..\n....." {
		t.Fatalf("解码后掩码 =\n%s", art)
	}
	if got := pm.Palette[7]; got != color.Color(m.Palette[7]) {
		t.Fatalf("解码调色板[7] = %v, 期望 %v", got, m.Palette[7])
	}
}
