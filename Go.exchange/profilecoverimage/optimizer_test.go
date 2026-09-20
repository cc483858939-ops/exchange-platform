package profilecoverimage

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestOptimizeKeepsAspectRatioAndDoesNotCropOrUpscale(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		width      int
		height     int
		wantWidth  int
		wantHeight int
	}{
		{name: "wide", width: 4000, height: 1000, wantWidth: 1500, wantHeight: 375},
		{name: "tall", width: 1000, height: 2000, wantWidth: 250, wantHeight: 500},
		{name: "small", width: 320, height: 120, wantWidth: 320, wantHeight: 120},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			derivative := mustOptimize(t, jpegFixture(t, testCase.width, testCase.height))
			if derivative.Width != testCase.wantWidth || derivative.Height != testCase.wantHeight {
				t.Fatalf("dimensions=%dx%d want=%dx%d", derivative.Width, derivative.Height, testCase.wantWidth, testCase.wantHeight)
			}
			if derivative.ContentType != "image/jpeg" || derivative.Extension != ".jpg" {
				t.Fatalf("derivative format=%s %s", derivative.ContentType, derivative.Extension)
			}
		})
	}
}

func TestOptimizePreservesTransparencyAndSupportsWebP(t *testing.T) {
	transparent := image.NewNRGBA(image.Rect(0, 0, 80, 40))
	transparent.SetNRGBA(10, 10, color.NRGBA{R: 255, A: 100})
	derivative := mustOptimize(t, pngFixture(t, transparent))
	if derivative.ContentType != "image/png" || derivative.Extension != ".png" {
		t.Fatalf("transparent derivative=%#v", derivative)
	}
	decoded, err := png.Decode(bytes.NewReader(derivative.Body))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, alpha := decoded.At(10, 10).RGBA()
	if alpha == 0xffff {
		t.Fatal("transparent source lost alpha")
	}

	webp, err := base64.StdEncoding.DecodeString(webPFixtureBase64)
	if err != nil {
		t.Fatal(err)
	}
	webpDerivative := mustOptimize(t, webp)
	if webpDerivative.Width <= 0 || webpDerivative.Height <= 0 || webpDerivative.Width > MaxOutputWidth || webpDerivative.Height > MaxOutputHeight {
		t.Fatalf("webp dimensions=%dx%d", webpDerivative.Width, webpDerivative.Height)
	}
}

func TestOptimizeIsDeterministicAndHashesEncodedBytes(t *testing.T) {
	source := jpegFixture(t, 800, 300)
	first := mustOptimize(t, source)
	second := mustOptimize(t, source)
	if !bytes.Equal(first.Body, second.Body) || first.ContentHash != second.ContentHash {
		t.Fatal("optimization was not deterministic")
	}
	hash := sha256.Sum256(first.Body)
	if first.ContentHash != fmt.Sprintf("%x", hash[:]) {
		t.Fatalf("hash=%q does not match derivative bytes", first.ContentHash)
	}
}

func TestOptimizeRejectsInvalidOrUnsafeSource(t *testing.T) {
	for _, body := range [][]byte{
		[]byte("not an image"),
		bytes.Repeat([]byte{'x'}, MaxSourceBytes+1),
	} {
		if _, err := Optimize(body); err == nil {
			t.Fatal("unsafe source accepted")
		}
	}
	if _, err := Optimize(pngDimensionsFixture(t, MaxDimension+1, 10)); err == nil {
		t.Fatal("oversized dimensions accepted")
	}
	if _, err := Optimize(pngDimensionsFixture(t, 5001, 5000)); err == nil {
		t.Fatal("pixel bomb accepted")
	}
}

func mustOptimize(t *testing.T, body []byte) Derivative {
	t.Helper()
	derivative, err := Optimize(body)
	if err != nil {
		t.Fatal(err)
	}
	return derivative
}

func jpegFixture(t *testing.T, width, height int) []byte {
	t.Helper()
	data := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			data.SetRGBA(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 100, A: 255})
		}
	}
	var body bytes.Buffer
	if err := jpeg.Encode(&body, data, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func pngFixture(t *testing.T, data image.Image) []byte {
	t.Helper()
	var body bytes.Buffer
	if err := png.Encode(&body, data); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func pngDimensionsFixture(t *testing.T, width, height int) []byte {
	t.Helper()
	return pngFixture(t, image.NewNRGBA(image.Rect(0, 0, width, height)))
}

const webPFixtureBase64 = "UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA=="
