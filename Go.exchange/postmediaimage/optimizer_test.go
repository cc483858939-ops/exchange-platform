package postmediaimage

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestProcessResizesWithoutUpscalingOrCropping(t *testing.T) {
	result, err := Process(encodePostJPEG(t, 4032, 3024, color.RGBA{R: 220, G: 80, B: 40, A: 255}))
	if err != nil {
		t.Fatal(err)
	}
	if result.OriginalContentType != "image/jpeg" || result.OriginalExtension != ".jpg" {
		t.Fatalf("source metadata=%#v", result)
	}
	assertDerivativeDimensions(t, result.Medium, 1200, 900, "image/jpeg", ".jpg")
	assertDerivativeDimensions(t, result.Large, 2048, 1536, "image/jpeg", ".jpg")

	small, err := Process(encodePostJPEG(t, 800, 600, color.RGBA{G: 200, A: 255}))
	if err != nil {
		t.Fatal(err)
	}
	assertDerivativeDimensions(t, small.Medium, 800, 600, "image/jpeg", ".jpg")
	assertDerivativeDimensions(t, small.Large, 800, 600, "image/jpeg", ".jpg")
}

func TestProcessPreservesAlphaAndReencodesWebP(t *testing.T) {
	transparent := image.NewNRGBA(image.Rect(0, 0, 80, 60))
	transparent.SetNRGBA(10, 10, color.NRGBA{R: 255, A: 100})
	result, err := Process(encodePostPNG(t, transparent))
	if err != nil {
		t.Fatal(err)
	}
	assertDerivativeDimensions(t, result.Medium, 80, 60, "image/png", ".png")
	assertDerivativeDimensions(t, result.Large, 80, 60, "image/png", ".png")

	webp, err := base64.StdEncoding.DecodeString(postWebPFixtureBase64)
	if err != nil {
		t.Fatal(err)
	}
	result, err = Process(webp)
	if err != nil {
		t.Fatal(err)
	}
	if result.OriginalContentType != "image/webp" || result.OriginalExtension != ".webp" {
		t.Fatalf("WebP source metadata=%#v", result)
	}
	if result.Medium.ContentType != "image/jpeg" || result.Medium.Extension != ".jpg" {
		t.Fatalf("WebP derivative=%#v", result.Medium)
	}
}

func TestProcessAppliesJPEGEXIFOrientationBeforeResize(t *testing.T) {
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
	result, err := Process(addPostEXIFOrientation(t, encodePostJPEGImage(t, source), 6))
	if err != nil {
		t.Fatal(err)
	}
	assertDerivativeDimensions(t, result.Medium, 200, 400, "image/jpeg", ".jpg")
	decoded, err := jpeg.Decode(bytes.NewReader(result.Medium.Body))
	if err != nil {
		t.Fatal(err)
	}
	if !isRedderPost(decoded.At(100, 50), decoded.At(100, 350)) || !isBluerPost(decoded.At(100, 350), decoded.At(100, 50)) {
		t.Fatalf("EXIF orientation was not applied: top=%v bottom=%v", decoded.At(100, 50), decoded.At(100, 350))
	}
}

func TestProcessRejectsInvalidOrUnsafeInputBeforeFullDecode(t *testing.T) {
	for _, body := range [][]byte{
		[]byte("not an image"),
		mutatePostPNGDimensions(t, 0, 100),
		mutatePostPNGDimensions(t, MaxDimension+1, 100),
		mutatePostPNGDimensions(t, 5000, 5000),
		bytes.Repeat([]byte{'x'}, MaxSourceBytes+1),
	} {
		if _, err := Process(body); err == nil {
			t.Fatal("Process unexpectedly accepted unsafe input")
		}
	}
	if _, err := Process([]byte("GIF89a")); err == nil {
		t.Fatal("Process unexpectedly accepted GIF bytes")
	}
}

func assertDerivativeDimensions(t *testing.T, derivative Derivative, width, height int, contentType, extension string) {
	t.Helper()
	if derivative.Width != width || derivative.Height != height || derivative.ContentType != contentType || derivative.Extension != extension || len(derivative.Body) == 0 {
		t.Fatalf("derivative=%#v", derivative)
	}
	if contentType == "image/png" {
		if _, err := png.Decode(bytes.NewReader(derivative.Body)); err != nil {
			t.Fatalf("PNG derivative cannot decode: %v", err)
		}
	} else if _, err := jpeg.Decode(bytes.NewReader(derivative.Body)); err != nil {
		t.Fatalf("JPEG derivative cannot decode: %v", err)
	}
}

func encodePostJPEG(t *testing.T, width, height int, fill color.RGBA) []byte {
	t.Helper()
	data := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			data.SetRGBA(x, y, fill)
		}
	}
	return encodePostJPEGImage(t, data)
}

func encodePostJPEGImage(t *testing.T, data image.Image) []byte {
	t.Helper()
	var body bytes.Buffer
	if err := jpeg.Encode(&body, data, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func encodePostPNG(t *testing.T, data image.Image) []byte {
	t.Helper()
	var body bytes.Buffer
	if err := png.Encode(&body, data); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func addPostEXIFOrientation(t *testing.T, jpegBody []byte, orientation uint16) []byte {
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
	result := make([]byte, 0, len(jpegBody)+len(exif)+4)
	result = append(result, jpegBody[:2]...)
	result = append(result, 0xff, 0xe1, byte(segmentLength>>8), byte(segmentLength))
	result = append(result, exif...)
	result = append(result, jpegBody[2:]...)
	return result
}

func mutatePostPNGDimensions(t *testing.T, width, height int) []byte {
	t.Helper()
	body := encodePostPNG(t, image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	binary.BigEndian.PutUint32(body[16:20], uint32(width))
	binary.BigEndian.PutUint32(body[20:24], uint32(height))
	binary.BigEndian.PutUint32(body[29:33], crc32.ChecksumIEEE(body[12:29]))
	return body
}

func isRedderPost(first, second color.Color) bool {
	firstR, _, firstB, _ := first.RGBA()
	secondR, _, secondB, _ := second.RGBA()
	return firstR > firstB && firstR > secondR && firstB < secondB
}

func isBluerPost(first, second color.Color) bool {
	firstR, _, firstB, _ := first.RGBA()
	secondR, _, secondB, _ := second.RGBA()
	return firstB > firstR && firstB > secondB && firstR < secondR
}

const postWebPFixtureBase64 = "UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA=="
