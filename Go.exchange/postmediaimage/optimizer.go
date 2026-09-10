// Package postmediaimage contains the image transformation contract for Post
// media. It deliberately does not share the avatar crop pipeline: Post media
// keeps its aspect ratio and is never center-cropped.
package postmediaimage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	stdDraw "image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"strings"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	MaxSourceBytes = 5 << 20
	MaxDimension   = 8192
	MaxPixels      = int64(25_000_000)
	MediumMaxSide  = 1200
	LargeMaxSide   = 2048
	JPEGQuality    = 85
)

// Derivative is one resized representation of a source image.
type Derivative struct {
	Body        []byte
	ContentType string
	Extension   string
	Width       int
	Height      int
}

// Result contains the source format metadata and both resized
// representations. The source bytes themselves are intentionally not copied;
// the caller keeps the original upload body unchanged for object storage.
type Result struct {
	OriginalContentType string
	OriginalExtension   string
	Medium              Derivative
	Large               Derivative
}

// Process validates and decodes one supported image, applies JPEG EXIF
// orientation, and produces non-upscaled, uncropped Medium and Large images.
func Process(body []byte) (Result, error) {
	if len(body) == 0 {
		return Result{}, errors.New("post media image is empty")
	}
	if len(body) > MaxSourceBytes {
		return Result{}, fmt.Errorf("post media image exceeds %d bytes", MaxSourceBytes)
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("decode post media dimensions: %w", err)
	}
	originalContentType, originalExtension, ok := sourceFormat(format)
	if !ok {
		return Result{}, fmt.Errorf("post media image format %q is not supported", format)
	}
	if err := validateDimensions(config.Width, config.Height); err != nil {
		return Result{}, err
	}

	source, decodedFormat, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("decode post media image: %w", err)
	}
	if _, _, ok := sourceFormat(decodedFormat); !ok || strings.ToLower(decodedFormat) != strings.ToLower(format) {
		return Result{}, errors.New("post media image format changed during decode")
	}

	oriented := source
	if strings.EqualFold(format, "jpeg") {
		oriented = applyOrientation(source, exifOrientation(body))
	}
	medium, err := encodeDerivative(resizeDown(oriented, MediumMaxSide))
	if err != nil {
		return Result{}, fmt.Errorf("encode post media medium: %w", err)
	}
	large, err := encodeDerivative(resizeDown(oriented, LargeMaxSide))
	if err != nil {
		return Result{}, fmt.Errorf("encode post media large: %w", err)
	}

	return Result{
		OriginalContentType: originalContentType,
		OriginalExtension:   originalExtension,
		Medium:              medium,
		Large:               large,
	}, nil
}

func sourceFormat(format string) (string, string, bool) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpeg":
		return "image/jpeg", ".jpg", true
	case "png":
		return "image/png", ".png", true
	case "webp":
		return "image/webp", ".webp", true
	default:
		return "", "", false
	}
}

func validateDimensions(width, height int) error {
	if width <= 0 || height <= 0 {
		return errors.New("post media image dimensions must be positive")
	}
	if width > MaxDimension || height > MaxDimension {
		return fmt.Errorf("post media image dimensions exceed %d pixels", MaxDimension)
	}
	if int64(width) > MaxPixels/int64(height) {
		return fmt.Errorf("post media image exceeds %d pixels", MaxPixels)
	}
	return nil
}

func resizeDown(source image.Image, maxSide int) *image.NRGBA {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	targetWidth, targetHeight := width, height
	if width > maxSide || height > maxSide {
		scale := float64(maxSide) / float64(maxInt(width, height))
		targetWidth = maxInt(1, int(math.Round(float64(width)*scale)))
		targetHeight = maxInt(1, int(math.Round(float64(height)*scale)))
		if targetWidth > maxSide {
			targetWidth = maxSide
		}
		if targetHeight > maxSide {
			targetHeight = maxSide
		}
	}
	output := image.NewNRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	if targetWidth == width && targetHeight == height {
		stdDraw.Draw(output, output.Bounds(), source, bounds.Min, stdDraw.Src)
		return output
	}
	xdraw.CatmullRom.Scale(output, output.Bounds(), source, bounds, xdraw.Src, nil)
	return output
}

func maxInt(first, second int) int {
	if first > second {
		return first
	}
	return second
}

func encodeDerivative(source image.Image) (Derivative, error) {
	bounds := source.Bounds()
	derivative := Derivative{Width: bounds.Dx(), Height: bounds.Dy()}
	var encoded bytes.Buffer
	if hasAlpha(source) {
		derivative.ContentType = "image/png"
		derivative.Extension = ".png"
		if err := png.Encode(&encoded, source); err != nil {
			return Derivative{}, err
		}
	} else {
		derivative.ContentType = "image/jpeg"
		derivative.Extension = ".jpg"
		if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: JPEGQuality}); err != nil {
			return Derivative{}, err
		}
	}
	derivative.Body = append([]byte(nil), encoded.Bytes()...)
	return derivative, nil
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

func applyOrientation(source image.Image, orientation int) image.Image {
	if orientation < 1 || orientation > 8 {
		orientation = 1
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	outWidth, outHeight := width, height
	if orientation >= 5 {
		outWidth, outHeight = height, width
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
