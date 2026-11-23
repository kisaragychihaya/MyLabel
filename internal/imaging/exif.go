package imaging

import (
	"encoding/binary"
	"image"
	"image/draw"
)

// orientationTag is the EXIF tag number of Orientation in IFD0.
const orientationTag = 0x0112

// tiffTypeShort is the EXIF field type of a SHORT value.
const tiffTypeShort = 3

// exifOrientation returns the EXIF Orientation stored in the JPEG segment
// list starting at data[0] (the SOI marker). It always yields a usable
// orientation: anything missing, malformed or out of range maps to 1.
func exifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}

	pos := 2
	for pos+1 < len(data) {
		if data[pos] != 0xFF {
			// Lost marker alignment: the rest is entropy-coded data or garbage.
			return 1
		}
		marker := data[pos+1]
		pos += 2
		switch {
		case marker == 0xFF:
			// Fill byte before the real marker: re-read from the same spot.
			pos--
			continue
		case marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7):
			// Standalone markers carry no length field.
			continue
		case marker == 0xD9 || marker == 0xDA:
			// Image end / start of scan: no APP1 ahead.
			return 1
		}

		if pos+2 > len(data) {
			return 1
		}
		segLen := int(binary.BigEndian.Uint16(data[pos:]))
		if segLen < 2 || pos+segLen > len(data) {
			return 1
		}
		if marker == 0xE1 {
			payload := data[pos+2 : pos+segLen]
			const exifHeader = "Exif\x00\x00"
			if len(payload) > len(exifHeader) && string(payload[:len(exifHeader)]) == exifHeader {
				return tiffOrientation(payload[len(exifHeader):])
			}
		}
		pos += segLen
	}
	return 1
}

// tiffOrientation walks the TIFF header and IFD0 looking for the Orientation
// tag. It returns 1 when absent, malformed, or out of the 1..8 range.
func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	const tiffMagic = 42
	if order.Uint16(tiff[2:4]) != tiffMagic {
		return 1
	}

	ifd0 := int(order.Uint32(tiff[4:8]))
	if ifd0 < 8 || ifd0+2 > len(tiff) {
		return 1
	}
	count := int(order.Uint16(tiff[ifd0:]))
	const entrySize = 12
	for i := 0; i < count; i++ {
		off := ifd0 + 2 + i*entrySize
		if off+entrySize > len(tiff) {
			return 1
		}
		entry := tiff[off : off+entrySize]
		if order.Uint16(entry[0:2]) != orientationTag {
			continue
		}
		// A single SHORT lives inline in the first two bytes of the value
		// field, for either byte order.
		if order.Uint16(entry[2:4]) != tiffTypeShort || order.Uint32(entry[4:8]) == 0 {
			return 1
		}
		value := int(order.Uint16(entry[8:10]))
		if value < 1 || value > 8 {
			return 1
		}
		return value
	}
	return 1
}

// applyOrientation returns a copy of src with the EXIF orientation baked into
// the pixels. Orientation 5..8 swaps width and height.
func applyOrientation(src image.Image, orientation int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := sw, sh
	if orientation >= 5 {
		dw, dh = sh, sw
	}

	// The copy loop moves raw RGBA bytes; images that already are RGBA are used
	// as is, everything else is converted once (the result is re-encoded as
	// JPEG anyway, so no precision is lost).
	source, ok := src.(*image.RGBA)
	if !ok {
		source = image.NewRGBA(image.Rect(0, 0, sw, sh))
		draw.Draw(source, source.Bounds(), src, b.Min, draw.Src)
	}
	sourceMin := source.Bounds().Min

	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			sx, sy := sourcePixel(x, y, sw, sh, orientation)
			si := source.PixOffset(sourceMin.X+sx, sourceMin.Y+sy)
			di := dst.PixOffset(x, y)
			copy(dst.Pix[di:di+4], source.Pix[si:si+4])
		}
	}
	return dst
}

// sourcePixel maps a destination pixel of the oriented image back to its
// source pixel, both in 0-based local coordinates.
func sourcePixel(x, y, sw, sh, orientation int) (int, int) {
	switch orientation {
	case 2: // horizontal flip
		return sw - 1 - x, y
	case 3: // rotate 180
		return sw - 1 - x, sh - 1 - y
	case 4: // vertical flip
		return x, sh - 1 - y
	case 5: // transpose (main diagonal)
		return y, x
	case 6: // rotate 90 clockwise
		return y, sh - 1 - x
	case 7: // transverse (anti-diagonal)
		return sw - 1 - y, sh - 1 - x
	case 8: // rotate 90 counter-clockwise
		return sw - 1 - y, x
	}
	return x, y
}
