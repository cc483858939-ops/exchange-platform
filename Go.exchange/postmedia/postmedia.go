// Package postmedia owns the object-key, URL, manifest, and public-variant
// contracts for Post media. It intentionally has no storage, HTTP, database,
// or image-processing dependencies.
package postmedia

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

const (
	UserV1ObjectPrefix    = "post-media/users/v1/"
	DevDataV1ObjectPrefix = "post-media/devdata/v1/"
	FilesURLPrefix        = "/api/files/"
)

type Manifest struct {
	Version           int             `json:"version"`
	OwnerID           uint            `json:"owner_id"`
	MediaID           string          `json:"media_id"`
	OriginalObjectKey string          `json:"original_object_key"`
	Medium            VariantManifest `json:"medium"`
	Large             VariantManifest `json:"large"`
}

type VariantManifest struct {
	ObjectKey   string `json:"object_key"`
	ContentType string `json:"content_type"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}

type UserV1ObjectPaths struct {
	OriginalObjectKey string
	MediumObjectKey   string
	LargeObjectKey    string
	ManifestObjectKey string
}

type DevDataV1ObjectPaths struct {
	OriginalObjectKey string
	MediumObjectKey   string
	LargeObjectKey    string
}

func BuildUserV1ObjectPaths(ownerID uint, mediaID, originalExtension, derivativeExtension string) (UserV1ObjectPaths, error) {
	if ownerID == 0 {
		return UserV1ObjectPaths{}, errors.New("post media owner id must be positive")
	}
	if err := validateCanonicalUUID(mediaID); err != nil {
		return UserV1ObjectPaths{}, err
	}
	originalExtension, err := validateOriginalExtension(originalExtension)
	if err != nil {
		return UserV1ObjectPaths{}, err
	}
	derivativeExtension, err = validateDerivativeExtension(derivativeExtension)
	if err != nil {
		return UserV1ObjectPaths{}, err
	}
	prefix := fmt.Sprintf("%s%d/%s/", UserV1ObjectPrefix, ownerID, mediaID)
	return UserV1ObjectPaths{
		OriginalObjectKey: prefix + "original" + originalExtension,
		MediumObjectKey:   prefix + "medium" + derivativeExtension,
		LargeObjectKey:    prefix + "large" + derivativeExtension,
		ManifestObjectKey: prefix + "manifest.json",
	}, nil
}

func BuildUserV1VariantObjectKey(ownerID uint, mediaID, variant, extension string) (string, error) {
	paths, err := BuildUserV1ObjectPaths(ownerID, mediaID, ".jpg", extension)
	if err != nil {
		return "", err
	}
	switch variant {
	case "medium":
		return paths.MediumObjectKey, nil
	case "large":
		return paths.LargeObjectKey, nil
	default:
		return "", errors.New("post media variant is not supported")
	}
}

func BuildUserV1ManifestObjectKey(ownerID uint, mediaID string) (string, error) {
	paths, err := BuildUserV1ObjectPaths(ownerID, mediaID, ".jpg", ".jpg")
	if err != nil {
		return "", err
	}
	return paths.ManifestObjectKey, nil
}

func BuildDevDataV1ObjectPaths(registryKey, sourcePostID, contentHash, originalExtension, derivativeExtension string) (DevDataV1ObjectPaths, error) {
	safeRegistryKey, err := sanitizeRegistryKey(registryKey)
	if err != nil {
		return DevDataV1ObjectPaths{}, err
	}
	if !isNumericSourceID(sourcePostID) {
		return DevDataV1ObjectPaths{}, errors.New("source Post ID must be numeric")
	}
	if !isLowerSHA256(contentHash) {
		return DevDataV1ObjectPaths{}, errors.New("post media content hash must be lowercase SHA-256")
	}
	originalExtension, err = validateOriginalExtension(originalExtension)
	if err != nil {
		return DevDataV1ObjectPaths{}, err
	}
	derivativeExtension, err = validateDerivativeExtension(derivativeExtension)
	if err != nil {
		return DevDataV1ObjectPaths{}, err
	}
	prefix := DevDataV1ObjectPrefix + safeRegistryKey + "/" + strings.TrimSpace(sourcePostID) + "/" + contentHash + "/"
	return DevDataV1ObjectPaths{
		OriginalObjectKey: prefix + "original" + originalExtension,
		MediumObjectKey:   prefix + "medium" + derivativeExtension,
		LargeObjectKey:    prefix + "large" + derivativeExtension,
	}, nil
}

func BuildDevDataV1VariantObjectKey(registryKey, sourcePostID, contentHash, variant, extension string) (string, error) {
	paths, err := BuildDevDataV1ObjectPaths(registryKey, sourcePostID, contentHash, ".jpg", extension)
	if err != nil {
		return "", err
	}
	switch variant {
	case "medium":
		return paths.MediumObjectKey, nil
	case "large":
		return paths.LargeObjectKey, nil
	default:
		return "", errors.New("post media variant is not supported")
	}
}

func PublicURL(objectKey string) string {
	return FilesURLPrefix + objectKey
}

func ContentTypeForExtension(extension string) string {
	switch strings.ToLower(strings.TrimSpace(extension)) {
	case ".jpg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

// ObjectExtension returns a supported lower-case extension only when it is
// the final extension of an object key.
func ObjectExtension(objectKey string) string {
	for _, extension := range []string{".jpg", ".png", ".webp"} {
		if strings.HasSuffix(objectKey, extension) {
			return extension
		}
	}
	return ""
}

// IsPublicObjectKey strictly accepts only immutable Medium/Large variants in
// the V1 user and DevData namespaces. Original files and manifests remain
// internal objects.
func IsPublicObjectKey(objectKey string) bool {
	if objectKey == "" || strings.Contains(objectKey, "..") || strings.ContainsAny(objectKey, "\r\n") || strings.HasPrefix(objectKey, "/") {
		return false
	}
	parts := strings.Split(objectKey, "/")
	switch {
	case len(parts) == 6 && parts[0] == "post-media" && parts[1] == "users" && parts[2] == "v1":
		return isCanonicalUserID(parts[3]) && isCanonicalUUID(parts[4]) && isDerivativeFilename(parts[5])
	case len(parts) == 7 && parts[0] == "post-media" && parts[1] == "devdata" && parts[2] == "v1":
		return isSafeRegistryKey(parts[3]) && isNumericSourceID(parts[4]) && isLowerSHA256(parts[5]) && isDerivativeFilename(parts[6])
	default:
		return false
	}
}

func validateCanonicalUUID(value string) error {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != value {
		return errors.New("post media id must be a canonical lowercase UUID")
	}
	return nil
}

func validateOriginalExtension(extension string) (string, error) {
	extension = strings.ToLower(strings.TrimSpace(extension))
	if extension != ".jpg" && extension != ".png" && extension != ".webp" {
		return "", errors.New("post media original extension is not supported")
	}
	return extension, nil
}

func validateDerivativeExtension(extension string) (string, error) {
	extension = strings.ToLower(strings.TrimSpace(extension))
	if extension != ".jpg" && extension != ".png" {
		return "", errors.New("post media derivative extension is not supported")
	}
	return extension, nil
}

func sanitizeRegistryKey(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "..") || strings.ContainsAny(raw, `/\\`) {
		return "", errors.New("registry key is unsafe for a post media object key")
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return "", errors.New("registry key contains control characters")
		}
	}
	var builder strings.Builder
	for _, r := range strings.ToLower(raw) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			builder.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteByte('-')
		default:
			builder.WriteByte('-')
		}
	}
	safe := strings.Trim(builder.String(), "-_")
	if safe == "" {
		return "", errors.New("registry key cannot produce a safe post media path")
	}
	return safe, nil
}

func isSafeRegistryKey(value string) bool {
	if value == "" || strings.Trim(value, "-_") != value {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func isCanonicalUserID(value string) bool {
	parsed, err := strconv.ParseUint(value, 10, 64)
	return value != "" && err == nil && parsed > 0 && strconv.FormatUint(parsed, 10) == value
}

func isCanonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func isDerivativeFilename(value string) bool {
	for _, variant := range []string{"medium", "large"} {
		for _, extension := range []string{".jpg", ".png"} {
			if value == variant+extension {
				return true
			}
		}
	}
	return false
}

func isNumericSourceID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 19 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isLowerSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
