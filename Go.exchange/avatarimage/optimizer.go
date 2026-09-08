// Package avatarimage contains the single image transformation contract used
// by user uploads, DevData refreshes, and legacy avatar migration.
package avatarimage

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	stdDraw "image/draw"
	"image/jpeg"
	"image/png"
	"strings"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	MaxSourceBytes = 2 << 20
	MaxDimension   = 8192
	MaxPixels      = int64(20_000_000)
	MaxOutputSide  = 256
	JPEGQuality    = 85
)

// Derivative is the deterministic, content-addressed avatar representation.
type Derivative struct {
	Body        []byte
	ContentType string
	Extension   string
	ContentHash string
	Width       int
	Height      int
}

// Optimize decodes one supported avatar, applies EXIF orientation, crops the
// largest centered square, and emits one opaque JPEG or transparent PNG.
func Optimize(body []byte) (Derivative, error) {
	if len(body) == 0 {
		return Derivative{}, errors.New("avatar image is empty")
	}
	if len(body) > MaxSourceBytes {
		return Derivative{}, fmt.Errorf("avatar image exceeds %d bytes", MaxSourceBytes)
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return Derivative{}, fmt.Errorf("decode avatar dimensions: %w", err)
	}
	if !isSupportedFormat(format) {
		return Derivative{}, fmt.Errorf("avatar image format %q is not supported", format)
	}
	if err := validateDimensions(config.Width, config.Height); err != nil {
		return Derivative{}, err
	}

	orientation := 1
	if format == "jpeg" {
		orientation = exifOrientation(body)
	}
	source, decodedFormat, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return Derivative{}, fmt.Errorf("decode avatar image: %w", err)
	}
	if !isSupportedFormat(decodedFormat) {
		return Derivative{}, fmt.Errorf("avatar image format %q is not supported", decodedFormat)
	}

	oriented := applyOrientation(source, orientation)
	cropped := centeredSquare(oriented)
	output := resizeDown(cropped)

	contentType := "image/jpeg"
	extension := ".jpg"
	var encoded bytes.Buffer
	if hasAlpha(output) {
		contentType = "image/png"
		extension = ".png"
		if err := png.Encode(&encoded, output); err != nil {
			return Derivative{}, fmt.Errorf("encode avatar PNG: %w", err)
		}
	} else if err := jpeg.Encode(&encoded, output, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		return Derivative{}, fmt.Errorf("encode avatar JPEG: %w", err)
	}

	encodedBody := encoded.Bytes()
	hash := sha256.Sum256(encodedBody)
	return Derivative{
		Body:        append([]byte(nil), encodedBody...),
		ContentType: contentType,
		Extension:   extension,
		ContentHash: fmt.Sprintf("%x", hash[:]),
		Width:       output.Bounds().Dx(),
		Height:      output.Bounds().Dy(),
	}, nil
}

func isSupportedFormat(format string) bool {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpeg", "png", "webp":
		return true
	default:
		return false
	}
}

func validateDimensions(width, height int) error {
	if width <= 0 || height <= 0 {
		return errors.New("avatar image dimensions must be positive")
	}
	if width > MaxDimension || height > MaxDimension {
		return fmt.Errorf("avatar image dimensions exceed %d pixels", MaxDimension)
	}
	if int64(width) > MaxPixels/int64(height) {
		return fmt.Errorf("avatar image exceeds %d pixels", MaxPixels)
	}
	return nil
}

func applyOrientation(source image.Image, orientation int) image.Image {
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

func centeredSquare(source image.Image) *image.NRGBA {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	side := width
	if height < side {
		side = height
	}
	left := bounds.Min.X + (width-side)/2
	top := bounds.Min.Y + (height-side)/2
	cropBounds := image.Rect(0, 0, side, side)
	crop := image.NewNRGBA(cropBounds)
	stdDraw.Draw(crop, cropBounds, source, image.Point{X: left, Y: top}, stdDraw.Src)
	return crop
}

func resizeDown(source image.Image) *image.NRGBA {
	side := source.Bounds().Dx()
	if source.Bounds().Dy() < side {
		side = source.Bounds().Dy()
	}
	if side <= MaxOutputSide {
		output := image.NewNRGBA(image.Rect(0, 0, side, side))
		stdDraw.Draw(output, output.Bounds(), source, source.Bounds().Min, stdDraw.Src)
		return output
	}
	output := image.NewNRGBA(image.Rect(0, 0, MaxOutputSide, MaxOutputSide))
	xdraw.CatmullRom.Scale(output, output.Bounds(), source, source.Bounds(), xdraw.Src, nil)
	return output
}

func hasAlpha(source image.Image) bool {
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

func exifOrientation(body []byte) int {
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
