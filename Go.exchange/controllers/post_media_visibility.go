package controllers

import (
	"context"
	"errors"
	"strings"

	"Go.exchange/global"
	"Go.exchange/postmedia"
)

// Query the primary database on each validation: a positive cache entry must
// never outlive deletion. Exact URL indexes bound this lookup for either variant.
var publicPostMediaReadable = func(ctx context.Context, objectKey string) (bool, error) {
	if global.APIDb == nil {
		return false, errors.New("database is not initialized")
	}
	column := "url"
	if strings.HasPrefix(objectKey[strings.LastIndex(objectKey, "/")+1:], "large.") {
		column = "large_url"
	}
	var allowed bool
	err := global.APIDb.WithContext(ctx).Raw(`SELECT EXISTS (
SELECT 1 FROM post_media AS media
JOIN posts ON posts.id = media.post_id
WHERE media.`+column+` = ? AND `+publicPostEligibilitySQL("posts")+`)`, postmedia.PublicURL(objectKey)).Scan(&allowed).Error
	return allowed, err
}

func matchesFileETag(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}
