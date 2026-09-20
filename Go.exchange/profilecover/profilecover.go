// Package profilecover contains the storage and public-URL contract for
// user-owned profile cover derivatives.
package profilecover

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	FilesURLPrefix     = "/api/files/"
	UserV1ObjectPrefix = "profile-covers/users/v1/"
)

// BuildUserV1ObjectKey returns the immutable, user-scoped cover derivative
// object key. The hash is the SHA-256 of the final encoded derivative.
func BuildUserV1ObjectKey(userID uint, contentHash string, extension string) (string, error) {
	if userID == 0 {
		return "", errors.New("user id must be positive")
	}
	if !isLowerSHA256(contentHash) {
		return "", errors.New("cover content hash must be lowercase SHA-256")
	}
	if extension != ".jpg" && extension != ".png" {
		return "", errors.New("cover extension is not supported")
	}
	return fmt.Sprintf("%s%d/%s%s", UserV1ObjectPrefix, userID, contentHash, extension), nil
}

// ParseUserCoverURL recognizes only an exact local cover URL owned by userID.
// Empty strings are handled by the profile PATCH decoder as removal and are
// intentionally not accepted here.
func ParseUserCoverURL(value string, userID uint) (string, error) {
	if userID == 0 || value == "" || strings.ContainsAny(value, "\r\n?#\\") || strings.Contains(value, "..") {
		return "", errors.New("invalid cover_image_url")
	}
	if !strings.HasPrefix(value, FilesURLPrefix) {
		return "", errors.New("invalid cover_image_url")
	}
	objectKey := strings.TrimPrefix(value, FilesURLPrefix)
	parsedUserID, _, err := parseObjectKey(objectKey)
	if err != nil {
		return "", err
	}
	if parsedUserID != userID {
		return "", errors.New("cover_image_url must belong to the current user")
	}
	return objectKey, nil
}

// IsPublicObjectKey reports whether objectKey is a valid immutable public
// cover object in the exact v1 namespace.
func IsPublicObjectKey(objectKey string) bool {
	_, _, err := parseObjectKey(objectKey)
	return err == nil
}

func parseObjectKey(objectKey string) (uint, string, error) {
	if objectKey == "" || strings.ContainsAny(objectKey, "\r\n?#\\") || strings.Contains(objectKey, "..") {
		return 0, "", errors.New("invalid cover object key")
	}
	if !strings.HasPrefix(objectKey, UserV1ObjectPrefix) {
		return 0, "", errors.New("invalid cover object key")
	}
	rest := strings.TrimPrefix(objectKey, UserV1ObjectPrefix)
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, "", errors.New("invalid cover object key")
	}
	parsed, err := strconv.ParseUint(parts[0], 10, 0)
	if err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != parts[0] {
		return 0, "", errors.New("invalid cover object key")
	}
	filename := parts[1]
	if strings.HasSuffix(filename, ".jpg") {
		if !isLowerSHA256(strings.TrimSuffix(filename, ".jpg")) {
			return 0, "", errors.New("invalid cover object key")
		}
	} else if strings.HasSuffix(filename, ".png") {
		if !isLowerSHA256(strings.TrimSuffix(filename, ".png")) {
			return 0, "", errors.New("invalid cover object key")
		}
	} else {
		return 0, "", errors.New("invalid cover object key")
	}
	return uint(parsed), filename, nil
}

func isLowerSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
