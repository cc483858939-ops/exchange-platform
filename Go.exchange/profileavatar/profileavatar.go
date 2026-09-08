package profileavatar

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	FilesURLPrefix        = "/api/files/"
	LegacyObjectPrefix    = "profile-avatars/"
	UserV1ObjectPrefix    = "profile-avatars/users/v1/"
	DevDataV1ObjectPrefix = "profile-avatars/devdata/v1/"
)

type UserAvatarURLKind string

const (
	UserAvatarURLLegacy UserAvatarURLKind = "legacy"
	UserAvatarURLV1     UserAvatarURLKind = "v1"
)

// BuildUserV1ObjectKey returns the user-scoped immutable derivative key.
func BuildUserV1ObjectKey(userID uint, contentHash, extension string) (string, error) {
	if userID == 0 {
		return "", errors.New("user id must be positive")
	}
	if !isLowerSHA256(contentHash) {
		return "", errors.New("avatar content hash must be lowercase SHA-256")
	}
	extension = strings.ToLower(strings.TrimSpace(extension))
	if extension != ".jpg" && extension != ".png" {
		return "", errors.New("avatar extension is not supported")
	}
	return fmt.Sprintf("%s%d/%s%s", UserV1ObjectPrefix, userID, contentHash, extension), nil
}

// ParseUserAvatarURL recognizes only the exact local avatar URLs accepted by
// profile updates. It returns the storage key and whether it is legacy/V1.
func ParseUserAvatarURL(value string, userID uint) (string, UserAvatarURLKind, error) {
	if userID == 0 || value == "" || strings.ContainsAny(value, "\r\n") || strings.Contains(value, "..") {
		return "", "", errors.New("invalid avatar_url")
	}
	if strings.ContainsAny(value, "?#") || !strings.HasPrefix(value, FilesURLPrefix) {
		return "", "", errors.New("invalid avatar_url")
	}
	objectKey := strings.TrimPrefix(value, FilesURLPrefix)
	legacyPrefix := fmt.Sprintf("%s%d/", LegacyObjectPrefix, userID)
	if strings.HasPrefix(objectKey, legacyPrefix) {
		filename := strings.TrimPrefix(objectKey, legacyPrefix)
		if validSingleFilename(filename) {
			for _, extension := range []string{".jpg", ".png", ".webp"} {
				if strings.HasSuffix(filename, extension) {
					stem := strings.TrimSuffix(filename, extension)
					parsed, err := uuid.Parse(stem)
					if err == nil && parsed.String() == stem {
						return objectKey, UserAvatarURLLegacy, nil
					}
				}
			}
		}
		return "", "", errors.New("invalid avatar_url")
	}

	v1Prefix := fmt.Sprintf("%s%d/", UserV1ObjectPrefix, userID)
	if strings.HasPrefix(objectKey, v1Prefix) {
		filename := strings.TrimPrefix(objectKey, v1Prefix)
		if !validSingleFilename(filename) {
			return "", "", errors.New("invalid avatar_url")
		}
		for _, extension := range []string{".jpg", ".png"} {
			if strings.HasSuffix(filename, extension) && isLowerSHA256(strings.TrimSuffix(filename, extension)) {
				return objectKey, UserAvatarURLV1, nil
			}
		}
		return "", "", errors.New("invalid avatar_url")
	}
	return "", "", errors.New("avatar_url must belong to the current user")
}

func validSingleFilename(filename string) bool {
	return filename != "" && !strings.ContainsAny(filename, "/\\")
}

func isLowerSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
