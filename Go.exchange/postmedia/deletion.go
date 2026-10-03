package postmedia

import (
	"errors"
	"strconv"
	"strings"
)

// UserDeletionKeys resolves only one author's canonical upload folder. Original
// extensions are not retained in PostMedia, so delete their three supported
// names explicitly. Never use a bucket-wide prefix or a user-provided URL list.
func UserDeletionKeys(ownerID uint, mediumURL, largeURL string) (string, []string, error) {
	medium := strings.TrimPrefix(mediumURL, FilesURLPrefix)
	large := strings.TrimPrefix(largeURL, FilesURLPrefix)
	if ownerID == 0 || !strings.HasPrefix(mediumURL, FilesURLPrefix) || !strings.HasPrefix(largeURL, FilesURLPrefix) ||
		!IsPublicObjectKey(medium) || !IsPublicObjectKey(large) {
		return "", nil, errors.New("invalid user media deletion identity")
	}
	parts := strings.Split(medium, "/")
	if len(parts) != 6 || !strings.HasPrefix(medium, UserV1ObjectPrefix) || parts[3] != strconv.FormatUint(uint64(ownerID), 10) ||
		!strings.HasPrefix(parts[5], "medium.") {
		return "", nil, errors.New("user media deletion owner or variant mismatch")
	}
	prefix := strings.Join(parts[:5], "/") + "/"
	if !strings.HasPrefix(large, prefix+"large.") {
		return "", nil, errors.New("user media deletion variants are from different uploads")
	}
	return parts[4], []string{medium, large, prefix + "original.jpg", prefix + "original.png", prefix + "original.webp", prefix + "manifest.json"}, nil
}
