package devdata

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"Go.exchange/models"
	"Go.exchange/postmedia"
	"Go.exchange/postmediaimage"

	"gorm.io/gorm"
)

func postMediaResolutionsForSync(desired SnapshotPost, options SyncOptions) ([]PostMediaResolution, bool) {
	if len(desired.Media) == 0 {
		return nil, true
	}
	key := SourcePostKey{RegistryKey: desired.RegistryKey, SourcePostID: desired.SourcePostID}
	resolutions, ok := options.PostMediaResolutions[key]
	if !ok || len(resolutions) != len(desired.Media) {
		return nil, false
	}
	byPosition := make(map[int]PostMediaResolution, len(resolutions))
	for _, resolution := range resolutions {
		if resolution.Position < 0 || resolution.Position >= len(desired.Media) {
			return nil, false
		}
		if _, exists := byPosition[resolution.Position]; exists {
			return nil, false
		}
		byPosition[resolution.Position] = resolution
	}
	ordered := make([]PostMediaResolution, len(desired.Media))
	for position, media := range desired.Media {
		resolution, exists := byPosition[position]
		if !exists || resolution.RegistryKey != desired.RegistryKey || resolution.SourcePostID != desired.SourcePostID {
			return nil, false
		}
		if strings.TrimSpace(resolution.SourceURL) != strings.TrimSpace(media.SourceURL) || strings.TrimSpace(resolution.LocalURL) == "" || strings.TrimSpace(resolution.LargeObjectKey) == "" || strings.TrimSpace(resolution.LargeLocalURL) == "" {
			return nil, false
		}
		if !isLowerHexHash(resolution.ContentHash) {
			return nil, false
		}
		mediumExtension := postmedia.ObjectExtension(resolution.ObjectKey)
		largeExtension := postmedia.ObjectExtension(resolution.LargeObjectKey)
		mediumKey, err := postmedia.BuildDevDataV1VariantObjectKey(desired.RegistryKey, desired.SourcePostID, resolution.ContentHash, "medium", mediumExtension)
		if err != nil || resolution.ObjectKey != mediumKey || resolution.LocalURL != postmedia.PublicURL(mediumKey) {
			return nil, false
		}
		largeKey, err := postmedia.BuildDevDataV1VariantObjectKey(desired.RegistryKey, desired.SourcePostID, resolution.ContentHash, "large", largeExtension)
		if err != nil || resolution.LargeObjectKey != largeKey || resolution.LargeLocalURL != postmedia.PublicURL(largeKey) {
			return nil, false
		}
		if resolution.Width <= 0 || resolution.Height <= 0 || resolution.Width > postmediaimage.MediumMaxSide || resolution.Height > postmediaimage.MediumMaxSide || maxPostMediaDimension(resolution.Width, resolution.Height) > postmediaimage.MediumMaxSide {
			return nil, false
		}
		ordered[position] = resolution
	}
	return ordered, true
}

func maxPostMediaDimension(first, second int) int {
	if first > second {
		return first
	}
	return second
}

func insertImportedPostMedia(tx *gorm.DB, postID uint, resolutions []PostMediaResolution, createdAt time.Time) error {
	ordered := append([]PostMediaResolution(nil), resolutions...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Position < ordered[j].Position })
	for position, resolution := range ordered {
		if resolution.Position != position {
			return fmt.Errorf("post media resolutions are not contiguous at position %d", position)
		}
		if err := tx.Create(&models.PostMedia{
			PostID: postID, MediaType: "image", URL: resolution.LocalURL,
			LargeURL: resolution.LargeLocalURL, Width: resolution.Width, Height: resolution.Height,
			Position: position, CreatedAt: createdAt.UTC(),
		}).Error; err != nil {
			return fmt.Errorf("insert imported PostMedia for Post %d position %d: %w", postID, position, err)
		}
	}
	return nil
}

func syncImportedPostMedia(tx *gorm.DB, postID uint, desired SnapshotPost, options SyncOptions, syncAt time.Time, maintenance *syncMaintenance) error {
	if len(desired.Media) == 0 {
		if desired.HasMedia {
			// The source still reports media, but there is no definitive
			// mirrorable photo set. Preserve any existing complete gallery.
			return nil
		}
		result := tx.Unscoped().Where("post_id = ?", postID).Delete(&models.PostMedia{})
		if result.Error != nil {
			return fmt.Errorf("clear imported PostMedia for Post %d: %w", postID, result.Error)
		}
		if result.RowsAffected > 0 {
			maintenance.affect(postID)
		}
		return nil
	}
	resolutions, complete := postMediaResolutionsForSync(desired, options)
	if !complete {
		return nil
	}
	var current []models.PostMedia
	if err := tx.Where("post_id = ?", postID).Order("position ASC").Find(&current).Error; err != nil {
		return fmt.Errorf("load imported PostMedia for Post %d: %w", postID, err)
	}
	identical := len(current) == len(resolutions)
	if identical {
		for position, resolution := range resolutions {
			if current[position].MediaType != "image" || current[position].URL != resolution.LocalURL || current[position].LargeURL != resolution.LargeLocalURL || current[position].Width != resolution.Width || current[position].Height != resolution.Height || current[position].Position != position {
				identical = false
				break
			}
		}
	}
	if identical {
		return nil
	}
	if err := tx.Unscoped().Where("post_id = ?", postID).Delete(&models.PostMedia{}).Error; err != nil {
		return fmt.Errorf("replace imported PostMedia for Post %d: %w", postID, err)
	}
	if err := insertImportedPostMedia(tx, postID, resolutions, syncAt); err != nil {
		return err
	}
	maintenance.affect(postID)
	return nil
}
