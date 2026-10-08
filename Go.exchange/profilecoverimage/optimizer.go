// Package profilecoverimage contains the deterministic profile-cover
// derivative transformation contract.
package profilecoverimage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"strings"

	"Go.exchange/internal/imageutil"

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
	return OptimizeContext(context.Background(), body)
}

// OptimizeContext checks cancellation between CPU stages. Standard-library
// codecs run synchronously; an in-progress codec keeps its processing budget
// until it returns, preventing timed-out work from escaping into the background.
func OptimizeContext(ctx context.Context, body []byte) (Derivative, error) {
	if err := ctx.Err(); err != nil {
		return Derivative{}, err
	}
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
		orientation = imageutil.EXIFOrientation(body)
	}
	source, decodedFormat, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return Derivative{}, fmt.Errorf("decode cover image: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Derivative{}, err
	}
	if !isSupportedFormat(decodedFormat) {
		return Derivative{}, fmt.Errorf("cover image format %q is not supported", decodedFormat)
	}
	if err := validateDimensions(source.Bounds().Dx(), source.Bounds().Dy()); err != nil {
		return Derivative{}, err
	}

	oriented := imageutil.ApplyOrientation(source, orientation)
	if err := ctx.Err(); err != nil {
		return Derivative{}, err
	}
	output := resizeDown(oriented)
	if err := ctx.Err(); err != nil {
		return Derivative{}, err
	}

	contentType := "image/jpeg"
	extension := ".jpg"
	var encoded bytes.Buffer
	if imageutil.HasAlpha(output) {
		contentType = "image/png"
		extension = ".png"
		if err := png.Encode(&encoded, output); err != nil {
			return Derivative{}, fmt.Errorf("encode cover PNG: %w", err)
		}
	} else if err := jpeg.Encode(&encoded, output, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		return Derivative{}, fmt.Errorf("encode cover JPEG: %w", err)
	}

	encodedBody := encoded.Bytes()
	if err := ctx.Err(); err != nil {
		return Derivative{}, err
	}
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
	return imageutil.ResizeTo(source, outputWidth, outputHeight)
}

func maxOne(value int) int {
	if value < 1 {
		return 1
	}
	return value
}
