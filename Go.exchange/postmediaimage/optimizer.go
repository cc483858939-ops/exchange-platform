// Package postmediaimage contains the image transformation contract for Post
// media. It deliberately does not share the avatar crop pipeline: Post media
// keeps its aspect ratio and is never center-cropped.
package postmediaimage

import (
	"bytes"
	"context"
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
	return ProcessContext(context.Background(), body)
}

// ProcessContext checks cancellation between CPU stages. Standard-library
// codecs run synchronously; an in-progress codec keeps its processing budget
// until it returns, preventing timed-out work from escaping into the background.
func ProcessContext(ctx context.Context, body []byte) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
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
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if _, _, ok := sourceFormat(decodedFormat); !ok || strings.ToLower(decodedFormat) != strings.ToLower(format) {
		return Result{}, errors.New("post media image format changed during decode")
	}

	oriented := source
	if strings.EqualFold(format, "jpeg") {
		oriented = imageutil.ApplyOrientation(source, imageutil.EXIFOrientation(body))
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	mediumPixels := resizeDown(oriented, MediumMaxSide)
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	medium, err := encodeDerivative(mediumPixels)
	if err != nil {
		return Result{}, fmt.Errorf("encode post media medium: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	large := medium
	if oriented.Bounds().Dx() <= MediumMaxSide && oriented.Bounds().Dy() <= MediumMaxSide {
		// Keep separately owned output bytes while avoiding identical encoding.
		large.Body = append([]byte(nil), medium.Body...)
	} else {
		largePixels := resizeDown(oriented, LargeMaxSide)
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		large, err = encodeDerivative(largePixels)
		if err != nil {
			return Result{}, fmt.Errorf("encode post media large: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
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
	return imageutil.ResizeTo(source, targetWidth, targetHeight)
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
	if imageutil.HasAlpha(source) {
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
