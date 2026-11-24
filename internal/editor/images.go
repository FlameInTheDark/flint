// Image decoding for the editor's image viewer: the standard library's
// PNG/JPEG/GIF decoders plus a hand-rolled BMP decoder (the repo stays
// dependency-free; golang.org/x/image is not a dependency).

package editor

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	// The stdlib decoders register themselves on image.Decode.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
)

// imageExts are the extensions the viewer claims (checked after decoding
// succeeds, so a .png-named text file still opens as text... no — the
// name gates the attempt; decode failure falls back to text/binary
// classification).
var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true,
	".apng": true, ".jfif": true, ".pjpeg": true, ".pjp": true,
}

// IsImageName reports whether a file name looks like a supported image.
func IsImageName(name string) bool {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return false
	}
	return imageExts[strings.ToLower(name[i:])]
}

// DecodeImage decodes PNG/JPEG/GIF/BMP bytes (BMP by the hand-rolled
// decoder below; everything else through image.Decode).
func DecodeImage(data []byte) (image.Image, error) {
	if len(data) < 8 {
		return nil, errors.New("image: too short")
	}
	// BMP magic "BM" — the stdlib has no BMP decoder.
	if data[0] == 'B' && data[1] == 'M' {
		return decodeBMP(data)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

// decodeBMP decodes the common BMP variants: uncompressed 1/4/8-bit
// palette, 16/24/32-bit RGB(A), bottom-up and top-down rows, OS/2 and
// Windows headers. RLE and BITFIELDS masks are rejected.
func decodeBMP(data []byte) (image.Image, error) {
	if len(data) < 14+40 {
		return nil, errors.New("bmp: truncated")
	}
	off := uint32(readLE32(data, 10)) // pixel array offset

	// DIB header: size distinguishes BITMAPCOREHEADER (12) from the
	// Windows BITMAPINFOHEADER+ family (>= 40).
	hdrSize := uint32(readLE32(data, 14))
	var w, h int32
	var bpp uint16
	var paletteOff int
	var paletteCount int
	var topDown bool
	if hdrSize == 12 {
		w, h = int32(int16(readLE16(data, 18))), int32(int16(readLE16(data, 20)))
		bpp = uint16(readLE16(data, 24))
		paletteOff = 14 + 12
		paletteCount = 1 << bpp
	} else {
		w = int32(readLE32(data, 18))
		h = int32(readLE32(data, 22))
		if h < 0 {
			h = -h
			topDown = true
		}
		bpp = uint16(readLE16(data, 28))
		compression := uint32(readLE32(data, 30))
		if compression != 0 {
			return nil, fmt.Errorf("bmp: compressed (method %d) not supported", compression)
		}
		paletteOff = 14 + int(hdrSize)
		if uint32(readLE32(data, 46)) > 0 {
			paletteCount = int(readLE32(data, 46))
		} else {
			paletteCount = 1 << bpp
		}
	}
	if w <= 0 || h <= 0 || w > 1<<17 || h > 1<<17 {
		return nil, errors.New("bmp: bad dimensions")
	}
	switch bpp {
	case 1, 4, 8, 24, 32:
	default:
		return nil, fmt.Errorf("bmp: %d bpp not supported", bpp)
	}

	// Row stride: 4-byte aligned (the stride in the header is trusted only
	// when it is at least the minimal aligned width).
	stride := (int(w)*int(bpp) + 31) / 32 * 4
	if declared := int(readLE32(data, 34)); hdrSize >= 40 && declared >= stride {
		stride = declared
	}
	if int64(off)+int64(stride)*int64(h) > int64(len(data)) {
		return nil, errors.New("bmp: truncated pixel data")
	}

	// Palette (1/4/8-bit): BGRA quads.
	var pal []color.RGBA
	if bpp <= 8 {
		pal = make([]color.RGBA, paletteCount)
		for i := 0; i < paletteCount; i++ {
			o := paletteOff + i*4
			if o+3 >= len(data) {
				return nil, errors.New("bmp: truncated palette")
			}
			pal[i] = color.RGBA{R: data[o+2], G: data[o+1], B: data[o], A: 0xFF}
		}
	}

	img := image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
	pix := data[off:]
	for y := 0; y < int(h); y++ {
		srcRow := y
		if !topDown {
			srcRow = int(h) - 1 - y
		}
		dst := img.Pix[y*img.Stride : y*img.Stride+int(w)*4]
		for x := 0; x < int(w); x++ {
			var r, g, bl, a uint8
			switch bpp {
			case 1:
				if len(pal) < 2 {
					return nil, errors.New("bmp: missing palette")
				}
				if (pix[srcRow*stride+x/8]>>(7-uint(x%8)))&1 == 1 {
					r, g, bl, a = pal[1].R, pal[1].G, pal[1].B, 0xFF
				} else {
					r, g, bl, a = pal[0].R, pal[0].G, pal[0].B, 0xFF
				}
			case 4:
				idx := (pix[srcRow*stride+x/2] >> (4 - 4*uint(x%2))) & 0xF
				if int(idx) >= len(pal) {
					return nil, errors.New("bmp: palette index out of range")
				}
				r, g, bl, a = pal[idx].R, pal[idx].G, pal[idx].B, 0xFF
			case 8:
				idx := pix[srcRow*stride+x]
				if int(idx) >= len(pal) {
					return nil, errors.New("bmp: palette index out of range")
				}
				r, g, bl, a = pal[idx].R, pal[idx].G, pal[idx].B, 0xFF
			case 24:
				o := srcRow*stride + x*3
				r, g, bl, a = pix[o+2], pix[o+1], pix[o], 0xFF
			case 32:
				o := srcRow*stride + x*4
				r, g, bl, a = pix[o+2], pix[o+1], pix[o], pix[o+3]
				if a == 0 {
					a = 0xFF
				}
			}
			d := dst[x*4:]
			d[0], d[1], d[2], d[3] = r, g, bl, a
		}
	}
	return img, nil
}

// ImageInfo returns image info ("WxH" display) for the status bar.
func ImageInfo(b *Buffer) string {
	if b.Image == nil {
		return ""
	}
	rect := b.Image.Bounds()
	return fmt.Sprintf("%d x %d px", rect.Dx(), rect.Dy())
}

func readLE16(b []byte, off int) uint16 { return uint16(b[off]) | uint16(b[off+1])<<8 }

func readLE32(b []byte, off int) uint32 {
	return uint32(b[off]) | uint32(b[off+1])<<8 | uint32(b[off+2])<<16 | uint32(b[off+3])<<24
}
