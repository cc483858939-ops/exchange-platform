package postmedia

import (
	"strings"
	"testing"
)

const testMediaID = "550e8400-e29b-41d4-a716-446655440000"

func TestBuildUserV1ObjectPaths(t *testing.T) {
	paths, err := BuildUserV1ObjectPaths(42, testMediaID, ".webp", ".jpg")
	if err != nil {
		t.Fatal(err)
	}
	if paths.OriginalObjectKey != "post-media/users/v1/42/"+testMediaID+"/original.webp" || paths.MediumObjectKey != "post-media/users/v1/42/"+testMediaID+"/medium.jpg" || paths.LargeObjectKey != "post-media/users/v1/42/"+testMediaID+"/large.jpg" || paths.ManifestObjectKey != "post-media/users/v1/42/"+testMediaID+"/manifest.json" {
		t.Fatalf("paths=%#v", paths)
	}
	if PublicURL(paths.MediumObjectKey) != "/api/files/"+paths.MediumObjectKey {
		t.Fatalf("medium URL=%q", PublicURL(paths.MediumObjectKey))
	}
}

func TestBuildDevDataV1ObjectPathsAndPublicAllowlist(t *testing.T) {
	hash := strings.Repeat("a", 64)
	paths, err := BuildDevDataV1ObjectPaths("Dotey", "123456", hash, ".jpg", ".png")
	if err != nil {
		t.Fatal(err)
	}
	if paths.OriginalObjectKey != "post-media/devdata/v1/dotey/123456/"+hash+"/original.jpg" || paths.MediumObjectKey != "post-media/devdata/v1/dotey/123456/"+hash+"/medium.png" || paths.LargeObjectKey != "post-media/devdata/v1/dotey/123456/"+hash+"/large.png" {
		t.Fatalf("paths=%#v", paths)
	}
	for _, key := range []string{paths.MediumObjectKey, paths.LargeObjectKey, "post-media/users/v1/42/" + testMediaID + "/medium.jpg"} {
		if !IsPublicObjectKey(key) {
			t.Fatalf("public key rejected: %q", key)
		}
	}
	for _, key := range []string{
		paths.OriginalObjectKey,
		"post-media/devdata/v1/dotey/123456/" + hash + "/manifest.json",
		"post-media/users/v1/42/" + testMediaID + "/medium.webp",
		"post-media/users/v1/42/" + strings.ToUpper(testMediaID) + "/large.jpg",
		"post-media/users/v1/42/" + testMediaID + "/../../large.jpg",
	} {
		if IsPublicObjectKey(key) {
			t.Fatalf("private/unsafe key accepted: %q", key)
		}
	}
}

func TestBuildDevDataV1ObjectPathsRejectsUnsafeInputs(t *testing.T) {
	hash := strings.Repeat("a", 64)
	tests := []struct {
		name                string
		registryKey         string
		sourcePostID        string
		contentHash         string
		originalExtension   string
		derivativeExtension string
	}{
		{
			name: "unsafe registry key", registryKey: "../dotey", sourcePostID: "123456",
			contentHash: hash, originalExtension: ".jpg", derivativeExtension: ".png",
		},
		{
			name: "non-numeric source post ID", registryKey: "dotey", sourcePostID: "not-numeric",
			contentHash: hash, originalExtension: ".jpg", derivativeExtension: ".png",
		},
		{
			name: "uppercase content hash", registryKey: "dotey", sourcePostID: "123456",
			contentHash: strings.Repeat("A", 64), originalExtension: ".jpg", derivativeExtension: ".png",
		},
		{
			name: "unsupported original extension", registryKey: "dotey", sourcePostID: "123456",
			contentHash: hash, originalExtension: ".gif", derivativeExtension: ".png",
		},
		{
			name: "unsupported derivative extension", registryKey: "dotey", sourcePostID: "123456",
			contentHash: hash, originalExtension: ".jpg", derivativeExtension: ".gif",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := BuildDevDataV1ObjectPaths(test.registryKey, test.sourcePostID, test.contentHash, test.originalExtension, test.derivativeExtension); err == nil {
				t.Fatal("unsafe object path inputs were accepted")
			}
		})
	}
}
