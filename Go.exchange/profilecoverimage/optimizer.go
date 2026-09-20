// Package profilecoverimage contains the deterministic profile-cover
// derivative transformation contract.
package profilecoverimage

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
	"math"
	"strings"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	MaxSourceBytes  = 5 << 20
	MaxDimension    = 8192
	MaxPixels       = int64(25_000_000)
	MaxOutputWidth  = 1500
	MaxOutputHeight = 500
	JPEGQuality     = 85
)

// Derivative is the deterministic, content-addressed cover representation.
type Derivative struct {
	Body        []byte
	ContentType string
	Extension   string
	ContentHash string
	Width       int
	Height      int
}

// Optimize validates, orients, proportionally resizes, and encodes one cover
// derivative. It never crops, upscales, or emits multiple variants.
func Optimize(body []byte) (Derivative, error) {
	if len(body) == 0 {
		return Derivative{}, errors.New("cover image is empty")
	}
	if len(body) > MaxSourceBytes {
		return Derivative{}, fmt.Errorf("cover image exceeds %d bytes", MaxSourceBytes)
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return Derivative{}, fmt.Errorf("decode cover dimensions: %w", err)
	}
	if !isSupportedFormat(format) {
		return Derivative{}, fmt.Errorf("cover image format %q is not supported", format)
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
		return Derivative{}, fmt.Errorf("decode cover image: %w", err)
	}
	if !isSupportedFormat(decodedFormat) {
		return Derivative{}, fmt.Errorf("cover image format %q is not supported", decodedFormat)
	}
	if err := validateDimensions(source.Bounds().Dx(), source.Bounds().Dy()); err != nil {
		return Derivative{}, err
	}

	oriented := applyOrientation(source, orientation)
	output := resizeDown(oriented)

	contentType := "image/jpeg"
	extension := ".jpg"
	var encoded bytes.Buffer
	if hasAlpha(output) {
		contentType = "image/png"
		extension = ".png"
		if err := png.Encode(&encoded, output); err != nil {
			return Derivative{}, fmt.Errorf("encode cover PNG: %w", err)
		}
	} else if err := jpeg.Encode(&encoded, output, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		return Derivative{}, fmt.Errorf("encode cover JPEG: %w", err)
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
		return errors.New("cover image dimensions must be positive")
	}
	if width > MaxDimension || height > MaxDimension {
		return fmt.Errorf("cover image dimensions exceed %d pixels", MaxDimension)
	}
	if int64(width) > MaxPixels/int64(height) {
		return fmt.Errorf("cover image exceeds %d pixels", MaxPixels)
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

func resizeDown(source image.Image) *image.NRGBA {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	scale := math.Min(1, math.Min(float64(MaxOutputWidth)/float64(width), float64(MaxOutputHeight)/float64(height)))
	outputWidth := maxOne(int(math.Round(float64(width) * scale)))
	outputHeight := maxOne(int(math.Round(float64(height) * scale)))
	if outputWidth > MaxOutputWidth {
		outputWidth = MaxOutputWidth
	}
	if outputHeight > MaxOutputHeight {
		outputHeight = MaxOutputHeight
	}
	if outputWidth == width && outputHeight == height {
		output := image.NewNRGBA(image.Rect(0, 0, width, height))
		stdDraw.Draw(output, output.Bounds(), source, bounds.Min, stdDraw.Src)
		return output
	}
	output := image.NewNRGBA(image.Rect(0, 0, outputWidth, outputHeight))
	xdraw.CatmullRom.Scale(output, output.Bounds(), source, bounds, xdraw.Src, nil)
	return output
}

func maxOne(value int) int {
	if value < 1 {
		return 1
	}
	return value
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
