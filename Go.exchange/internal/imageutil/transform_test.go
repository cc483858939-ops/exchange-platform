package imageutil

import (
	"fmt"
	"image"
	"image/color"
	"testing"
)

func referenceOrientationCopy(source image.Image, orientation int) *image.NRGBA {
	if orientation < 1 || orientation > 8 {
		orientation = 1
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	ow, oh := width, height
	if orientation >= 5 {
		ow, oh = height, width
	}
	output := image.NewNRGBA(image.Rect(0, 0, ow, oh))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			dx, dy := orientedCoordinates(x, y, width, height, orientation)
			output.Set(dx, dy, source.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	return output
}

func TestOptimizedOrientationAndAlphaPreservePixelContract(t *testing.T) {
	bounds := image.Rect(7, 11, 20, 28)
	nrgba := image.NewNRGBA(bounds)
	rgba := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.NRGBA{R: uint8(x * 13), G: uint8(y * 7), B: uint8(x * y), A: uint8((x + y) % 4 * 85)}
			nrgba.Set(x, y, c)
			rgba.Set(x, y, c)
		}
	}
	for _, source := range []image.Image{nrgba, rgba} {
		for _, orientation := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9} {
			t.Run(fmt.Sprintf("%T/%d", source, orientation), func(t *testing.T) {
				want := referenceOrientationCopy(source, orientation)
				got := ApplyOrientation(source, orientation)
				if got.Bounds() != want.Bounds() {
					t.Fatalf("bounds=%v want=%v", got.Bounds(), want.Bounds())
				}
				for y := 0; y < want.Bounds().Dy(); y++ {
					for x := 0; x < want.Bounds().Dx(); x++ {
						if color.NRGBAModel.Convert(got.At(x, y)) != want.At(x, y) {
							t.Fatalf("pixel (%d,%d)=%v want=%v", x, y, got.At(x, y), want.At(x, y))
						}
					}
				}
			})
		}
	}
	opaque := image.NewNRGBA(image.Rect(0, 0, 13, 17))
	for i := 3; i < len(opaque.Pix); i += 4 {
		opaque.Pix[i] = 255
	}
	if HasAlpha(opaque) {
		t.Fatal("opaque image classified as transparent")
	}
	opaque.Pix[len(opaque.Pix)-1] = 254
	if !HasAlpha(opaque) {
		t.Fatal("last-pixel transparency lost")
	}
}

func BenchmarkAlphaScanOptimized(b *testing.B) {
	source := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
	for i := 3; i < len(source.Pix); i += 4 {
		source.Pix[i] = 255
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if HasAlpha(source) {
			b.Fatal("unexpected alpha")
		}
	}
}

//go:noinline
func referenceAlphaScan(source image.Image) bool {
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

func BenchmarkAlphaScanReference(b *testing.B) {
	source := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
	for i := 3; i < len(source.Pix); i += 4 {
		source.Pix[i] = 255
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if referenceAlphaScan(source) {
			b.Fatal("unexpected alpha")
		}
	}
}
