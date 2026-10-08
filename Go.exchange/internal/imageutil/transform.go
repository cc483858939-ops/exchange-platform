// Package imageutil contains image algorithms shared by upload transformations.
package imageutil

import (
	"bytes"
	"encoding/binary"
	"image"
	stdDraw "image/draw"

	xdraw "golang.org/x/image/draw"
)

// ResizeTo copies or scales pixels to the target dimensions chosen by the caller.
func ResizeTo(source image.Image, width, height int) *image.NRGBA {
	bounds := source.Bounds()
	output := image.NewNRGBA(image.Rect(0, 0, width, height))
	if bounds.Dx() == width && bounds.Dy() == height {
		stdDraw.Draw(output, output.Bounds(), source, bounds.Min, stdDraw.Src)
		return output
	}
	xdraw.CatmullRom.Scale(output, output.Bounds(), source, bounds, xdraw.Src, nil)
	return output
}

// ApplyOrientation copies pixels into EXIF-oriented coordinates without resizing.
func ApplyOrientation(source image.Image, orientation int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	outWidth, outHeight := width, height
	if orientation >= 5 && orientation <= 8 {
		outWidth, outHeight = height, width
	}
	if orientation < 1 || orientation > 8 {
		orientation = 1
	}

	output := image.NewNRGBA(image.Rect(0, 0, outWidth, outHeight))
	if orientation == 1 {
		stdDraw.Draw(output, output.Bounds(), source, bounds.Min, stdDraw.Src)
		return output
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			destinationX, destinationY := orientedCoordinates(x, y, width, height, orientation)
			output.Set(destinationX, destinationY, source.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	return output
}

func orientedCoordinates(x, y, width, height, orientation int) (int, int) {
	switch orientation {
	case 2:
		return width - 1 - x, y
	case 3:
		return width - 1 - x, height - 1 - y
	case 4:
		return x, height - 1 - y
	case 5:
		return y, x
	case 6:
		return height - 1 - y, x
	case 7:
		return height - 1 - y, width - 1 - x
	case 8:
		return y, width - 1 - x
	default:
		return x, y
	}
}

// HasAlpha reports whether any pixel is non-opaque.
func HasAlpha(source image.Image) bool {
	if opaque, ok := source.(interface{ Opaque() bool }); ok {
		return !opaque.Opaque()
	}
	bounds := source.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			_, _, _, alpha := source.At(x, y).RGBA()
			if alpha != 0xffff {
				return true
			}
		}
	}
	return false
}

// EXIFOrientation reads JPEG orientation and defaults to normal for absent or invalid metadata.
func EXIFOrientation(body []byte) int {
	if len(body) < 4 || body[0] != 0xff || body[1] != 0xd8 {
		return 1
	}
	for offset := 2; offset+1 < len(body); {
		if body[offset] != 0xff {
			offset++
			continue
		}
		for offset < len(body) && body[offset] == 0xff {
			offset++
		}
		if offset >= len(body) {
			break
		}
		marker := body[offset]
		offset++
		if marker == 0xda || marker == 0xd9 {
			break
		}
		if marker >= 0xd0 && marker <= 0xd7 {
			continue
		}
		if offset+2 > len(body) {
			break
		}
		segmentLength := int(binary.BigEndian.Uint16(body[offset : offset+2]))
		if segmentLength < 2 || segmentLength > len(body)-offset {
			break
		}
		if marker == 0xe1 && segmentLength >= 8 && bytes.Equal(body[offset+2:offset+8], []byte("Exif\x00\x00")) {
			if orientation, ok := parseTIFFOrientation(body[offset+8 : offset+segmentLength]); ok {
				return orientation
			}
		}
		offset += segmentLength
	}
	return 1
}

func parseTIFFOrientation(tiff []byte) (int, bool) {
	if len(tiff) < 8 {
		return 0, false
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0, false
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 0, false
	}
	ifdOffset := uint64(order.Uint32(tiff[4:8]))
	if ifdOffset > uint64(len(tiff)-2) {
		return 0, false
	}
	entryCount := int(order.Uint16(tiff[ifdOffset : ifdOffset+2]))
	entriesStart := ifdOffset + 2
	if uint64(entryCount) > uint64(len(tiff)-int(entriesStart))/12 {
		return 0, false
	}
	for index := 0; index < entryCount; index++ {
		entryStart := entriesStart + uint64(index*12)
		entry := tiff[entryStart : entryStart+12]
		if order.Uint16(entry[0:2]) != 0x0112 {
			continue
		}
		if order.Uint16(entry[2:4]) != 3 || order.Uint32(entry[4:8]) < 1 {
			return 0, false
		}
		orientation := int(order.Uint16(entry[8:10]))
		if orientation < 1 || orientation > 8 {
			return 0, false
		}
		return orientation, true
	}
	return 0, false
}
