package avatarimage

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestOptimizeJPEGLandscapeAndPortraitCropTo256(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		width, height int
	}{
		{name: "landscape", width: 800, height: 400},
		{name: "portrait", width: 400, height: 800},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			derivative := mustOptimize(t, encodeJPEG(t, testCase.width, testCase.height, color.RGBA{R: 220, G: 30, B: 30, A: 255}))
			assertDerivative(t, derivative, 256, 256, "image/jpeg", ".jpg")
		})
	}
}

func TestOptimizeDoesNotUpscaleSmallSquare(t *testing.T) {
	derivative := mustOptimize(t, encodeJPEG(t, 128, 96, color.RGBA{R: 30, G: 180, B: 80, A: 255}))
	assertDerivative(t, derivative, 96, 96, "image/jpeg", ".jpg")
}

func TestOptimizeAlphaAndOpaquePNGEncodingPolicy(t *testing.T) {
	transparent := image.NewNRGBA(image.Rect(0, 0, 80, 60))
	transparent.SetNRGBA(10, 10, color.NRGBA{R: 255, A: 100})
	transparentDerivative := mustOptimize(t, encodePNG(t, transparent))
	assertDerivative(t, transparentDerivative, 60, 60, "image/png", ".png")
	decodedTransparent, err := png.Decode(bytes.NewReader(transparentDerivative.Body))
	if err != nil {
		t.Fatalf("decode transparent derivative: %v", err)
	}
	_, _, _, alpha := decodedTransparent.At(0, 10).RGBA()
	if alpha == 0xffff {
		t.Fatal("transparent PNG derivative lost alpha")
	}

	opaque := image.NewNRGBA(image.Rect(0, 0, 60, 60))
	for y := 0; y < 60; y++ {
		for x := 0; x < 60; x++ {
			opaque.SetNRGBA(x, y, color.NRGBA{R: 240, G: 240, B: 240, A: 255})
		}
	}
	opaque.SetNRGBA(20, 20, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	opaqueDerivative := mustOptimize(t, encodePNG(t, opaque))
	assertDerivative(t, opaqueDerivative, 60, 60, "image/jpeg", ".jpg")
}

func TestOptimizeWebP(t *testing.T) {
	body, err := base64.StdEncoding.DecodeString(webPFixtureBase64)
	if err != nil {
		t.Fatal(err)
	}
	derivative := mustOptimize(t, body)
	if derivative.ContentType != "image/jpeg" || derivative.Extension != ".jpg" {
		t.Fatalf("WebP derivative=%#v", derivative)
	}
	if derivative.Width <= 0 || derivative.Height <= 0 || derivative.Width > MaxOutputSide || derivative.Height > MaxOutputSide || derivative.Width != derivative.Height {
		t.Fatalf("WebP dimensions=%dx%d", derivative.Width, derivative.Height)
	}
}

func TestOptimizeAppliesEXIFOrientationBeforeCrop(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 400; x++ {
			if x < 200 {
				source.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
			} else {
				source.SetRGBA(x, y, color.RGBA{B: 255, A: 255})
			}
		}
	}
	rotated := addEXIFOrientation(t, encodeJPEGImage(t, source), 6)
	derivative := mustOptimize(t, rotated)
	decoded, err := jpeg.Decode(bytes.NewReader(derivative.Body))
	if err != nil {
		t.Fatalf("decode rotated derivative: %v", err)
	}
	if decoded.Bounds().Dx() != 200 || decoded.Bounds().Dy() != 200 {
		t.Fatalf("rotated dimensions=%v", decoded.Bounds())
	}
	if !isRedder(decoded.At(100, 10), decoded.At(100, 190)) || !isBluer(decoded.At(100, 190), decoded.At(100, 10)) {
		t.Fatalf("EXIF orientation was not applied before crop: top=%v bottom=%v", decoded.At(100, 10), decoded.At(100, 190))
	}
}

func TestOptimizeRejectsInvalidAndUnsafeDimensions(t *testing.T) {
	for _, testCase := range []struct {
		name string
		body []byte
	}{
		{name: "invalid bytes", body: []byte("not an image")},
		{name: "zero dimensions", body: mutatePNGDimensions(t, 0, 100)},
		{name: "oversized width", body: mutatePNGDimensions(t, MaxDimension+1, 100)},
		{name: "pixel count", body: mutatePNGDimensions(t, 5000, 5000)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := Optimize(testCase.body); err == nil {
				t.Fatal("Optimize unexpectedly accepted unsafe input")
			}
		})
	}
	if _, err := Optimize(bytes.Repeat([]byte{'x'}, MaxSourceBytes+1)); err == nil {
		t.Fatal("Optimize unexpectedly accepted an oversized source")
	}
}

func TestOptimizeIsDeterministicAndHashesFinalBytes(t *testing.T) {
	source := encodeJPEG(t, 400, 300, color.RGBA{R: 50, G: 100, B: 220, A: 255})
	first := mustOptimize(t, source)
	second := mustOptimize(t, source)
	if !bytes.Equal(first.Body, second.Body) || first.ContentHash != second.ContentHash {
		t.Fatal("repeated optimization was not deterministic")
	}
	hash := sha256.Sum256(first.Body)
	if first.ContentHash != strings.ToLower(hexHash(hash[:])) {
		t.Fatalf("content hash=%q does not match derivative bytes", first.ContentHash)
	}
}

func mustOptimize(t *testing.T, body []byte) Derivative {
	t.Helper()
	derivative, err := Optimize(body)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	return derivative
}

func assertDerivative(t *testing.T, derivative Derivative, width, height int, contentType, extension string) {
	t.Helper()
	if derivative.Width != width || derivative.Height != height || derivative.ContentType != contentType || derivative.Extension != extension || len(derivative.Body) == 0 {
		t.Fatalf("derivative=%#v", derivative)
	}
	hash := sha256.Sum256(derivative.Body)
	if derivative.ContentHash != hexHash(hash[:]) {
		t.Fatalf("hash=%q want=%q", derivative.ContentHash, hexHash(hash[:]))
	}
}

func encodeJPEG(t *testing.T, width, height int, fill color.RGBA) []byte {
	t.Helper()
	data := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			data.SetRGBA(x, y, fill)
		}
	}
	return encodeJPEGImage(t, data)
}

func encodeJPEGImage(t *testing.T, data image.Image) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, data, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func encodePNG(t *testing.T, data image.Image) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, data); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func addEXIFOrientation(t *testing.T, jpegBody []byte, orientation uint16) []byte {
	t.Helper()
	tiff := make([]byte, 26)
	copy(tiff[:2], "II")
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], 8)
	binary.LittleEndian.PutUint16(tiff[8:10], 1)
	binary.LittleEndian.PutUint16(tiff[10:12], 0x0112)
	binary.LittleEndian.PutUint16(tiff[12:14], 3)
	binary.LittleEndian.PutUint32(tiff[14:18], 1)
	binary.LittleEndian.PutUint16(tiff[18:20], orientation)
	exif := append([]byte("Exif\x00\x00"), tiff...)
	segmentLength := len(exif) + 2
	if segmentLength > 0xffff {
		t.Fatal("EXIF fixture is too large")
	}
	result := make([]byte, 0, len(jpegBody)+len(exif)+4)
	result = append(result, jpegBody[:2]...)
	result = append(result, 0xff, 0xe1, byte(segmentLength>>8), byte(segmentLength))
	result = append(result, exif...)
	result = append(result, jpegBody[2:]...)
	return result
}

func mutatePNGDimensions(t *testing.T, width, height int) []byte {
	t.Helper()
	data := encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	binary.BigEndian.PutUint32(data[16:20], uint32(width))
	binary.BigEndian.PutUint32(data[20:24], uint32(height))
	checksum := crc32.ChecksumIEEE(data[12:29])
	binary.BigEndian.PutUint32(data[29:33], checksum)
	return data
}

func isRedder(first, second color.Color) bool {
	firstR, _, firstB, _ := first.RGBA()
	secondR, _, secondB, _ := second.RGBA()
	return firstR > firstB && firstR > secondR && firstB < secondB
}

func isBluer(first, second color.Color) bool {
	firstR, _, firstB, _ := first.RGBA()
	secondR, _, secondB, _ := second.RGBA()
	return firstB > firstR && firstB > secondB && firstR < secondR
}

func hexHash(hash []byte) string {
	const digits = "0123456789abcdef"
	result := make([]byte, len(hash)*2)
	for index, value := range hash {
		result[index*2] = digits[value>>4]
		result[index*2+1] = digits[value&0x0f]
	}
	return string(result)
}

const webPFixtureBase64 = "UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA=="
