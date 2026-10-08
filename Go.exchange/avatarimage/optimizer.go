// Package avatarimage contains the single image transformation contract used
// by user uploads, DevData refreshes, and legacy avatar migration.
package avatarimage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	stdDraw "image/draw"
	"image/jpeg"
	"image/png"
	"strings"

	"Go.exchange/internal/imageutil"

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
		orientation = imageutil.EXIFOrientation(body)
	}
	source, decodedFormat, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return Derivative{}, fmt.Errorf("decode avatar image: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Derivative{}, err
	}
	if !isSupportedFormat(decodedFormat) {
		return Derivative{}, fmt.Errorf("avatar image format %q is not supported", decodedFormat)
	}

	oriented := imageutil.ApplyOrientation(source, orientation)
	if err := ctx.Err(); err != nil {
		return Derivative{}, err
	}
	cropped := centeredSquare(oriented)
	output := resizeDown(cropped)
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
			return Derivative{}, fmt.Errorf("encode avatar PNG: %w", err)
		}
	} else if err := jpeg.Encode(&encoded, output, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		return Derivative{}, fmt.Errorf("encode avatar JPEG: %w", err)
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

func centeredSquare(source image.Image) *image.NRGBA {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	side := width
	if height < side {
		side = height
	}
	left := bounds.Min.X + (width-side)/2
	top := bounds.Min.Y + (height-side)/2
	if pixels, ok := source.(*image.NRGBA); ok {
		return pixels.SubImage(image.Rect(left, top, left+side, top+side)).(*image.NRGBA)
	}
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
	if side > MaxOutputSide {
		side = MaxOutputSide
	}
	return imageutil.ResizeTo(source, side, side)
}
