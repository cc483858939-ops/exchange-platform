package devdata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"Go.exchange/models"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SyncIncremental applies only the selected shard's account and source-post
// updates. Unlike SyncSnapshot, it never treats an absent source item as a
// retirement signal.
func SyncIncremental(ctx context.Context, db *gorm.DB, registry SourceRegistry, batch IncrementalBatch, redisClient *redis.Client, syncAt time.Time, options SyncOptions) (SyncResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateIncrementalBatch(batch, registry); err != nil {
		return SyncResult{}, err
	}
	if db == nil {
		return SyncResult{}, errors.New("database is not initialized")
	}
	if err := ValidateMetadataSchema(ctx, db); err != nil {
		return SyncResult{}, err
	}
	if syncAt.IsZero() {
		syncAt = time.Now().UTC()
	}
	maintenance := newSyncMaintenance()
	result := SyncResult{}
	var profileChanges map[uint]bool
	var accountsByKey map[string]models.DevDataMirrorAccount
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		accountsByKey, profileChanges, err = syncIncrementalAccounts(tx, registry, batch, syncAt, options)
		if err != nil {
			return err
		}
		if err := syncIncrementalPosts(tx, registry, batch, accountsByKey, profileChanges, syncAt, options, &result, maintenance); err != nil {
			return err
		}
		return readSyncCounts(tx, &result)
	})
	if err != nil {
		return SyncResult{}, err
	}
	result.AffectedPostIDs = sortedIDs(maintenance.affected)
	result.NewPostIDs = sortedIDs(maintenance.newPosts)
	result.PurgedPostIDs = sortedIDs(maintenance.purged)
	performPostCommitMaintenance(ctx, redisClient, maintenance)
	return result, nil
}

func syncIncrementalAccounts(tx *gorm.DB, registry SourceRegistry, batch IncrementalBatch, syncAt time.Time, options SyncOptions) (map[string]models.DevDataMirrorAccount, map[uint]bool, error) {
	keys := make([]string, 0, len(batch.Accounts))
	for _, source := range batch.Accounts {
		keys = append(keys, source.RegistryKey)
	}
	if len(keys) == 0 {
		return map[string]models.DevDataMirrorAccount{}, map[uint]bool{}, nil
	}
	var existing []models.DevDataMirrorAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("registry_key IN ?", keys).Find(&existing).Error; err != nil {
		return nil, nil, fmt.Errorf("load incremental DevData mirror accounts: %w", err)
	}
	existingByKey := make(map[string]models.DevDataMirrorAccount, len(existing))
	for _, account := range existing {
		if _, exists := existingByKey[account.RegistryKey]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate database registry key %q", ErrMirrorMappingInconsistent, account.RegistryKey)
		}
		existingByKey[account.RegistryKey] = account
	}
	profileChanges := make(map[uint]bool)
	fetchedAt := batch.FetchedAt.UTC()
	for _, source := range batch.Accounts {
		configured, ok := registry.AccountByKey(source.RegistryKey)
		if !ok || !configured.Enabled {
			return nil, nil, fmt.Errorf("incremental batch account %q is not enabled in registry", source.RegistryKey)
		}
		stored, exists := existingByKey[source.RegistryKey]
		if exists {
			if stored.Platform != configured.Platform || stored.SourceUserID != source.SourceUserID {
				return nil, nil, fmt.Errorf("%w: registry key %q is bound to source user %q, batch resolved %q", ErrSourceIdentityMismatch, source.RegistryKey, stored.SourceUserID, source.SourceUserID)
			}
			if stored.LocalUserID == 0 {
				return nil, nil, fmt.Errorf("%w: registry key %q has no local user", ErrMirrorMappingInconsistent, source.RegistryKey)
			}
			var user models.User
			if err := tx.Unscoped().First(&user, stored.LocalUserID).Error; err != nil {
				return nil, nil, fmt.Errorf("%w: load local user for %q: %w", ErrMirrorMappingInconsistent, source.RegistryKey, err)
			}
			if user.DeletedAt.Valid || user.Username != MirrorUsername(source.RegistryKey) {
				return nil, nil, fmt.Errorf("%w: local mirror user for %q is invalid", ErrMirrorMappingInconsistent, source.RegistryKey)
			}
			profileChanged, err := updateMirrorUser(tx, &user, source, options)
			if err != nil {
				return nil, nil, err
			}
			profileChanges[stored.ID] = profileChanged
			accountUpdates := map[string]interface{}{
				"platform":          stored.Platform,
				"source_handle":     source.Handle,
				"category":          source.Category,
				"enabled":           true,
				"source_avatar_url": source.ProfileImageURL,
				"last_fetched_at":   fetchedAt,
				"updated_at":        syncAt,
			}
			if resolution, ok := avatarResolutionForSync(source, options); ok {
				accountUpdates["avatar_object_key"] = resolution.ObjectKey
				accountUpdates["avatar_content_hash"] = resolution.ContentHash
			}
			if err := tx.Model(&models.DevDataMirrorAccount{}).Where("id = ?", stored.ID).Updates(accountUpdates).Error; err != nil {
				return nil, nil, fmt.Errorf("update incremental DevData mirror account %q: %w", source.RegistryKey, err)
			}
			stored.SourceHandle = source.Handle
			stored.Category = source.Category
			stored.Enabled = true
			stored.SourceAvatarURL = source.ProfileImageURL
			stored.LastFetchedAt = &fetchedAt
			existingByKey[source.RegistryKey] = stored
			continue
		}

		username := MirrorUsername(source.RegistryKey)
		var collision models.User
		collisionErr := tx.Unscoped().Where("username = ?", username).First(&collision).Error
		if collisionErr == nil {
			return nil, nil, fmt.Errorf("%w: %s", ErrMirrorUsernameCollision, username)
		}
		if !errors.Is(collisionErr, gorm.ErrRecordNotFound) {
			return nil, nil, fmt.Errorf("check mirror username %q: %w", username, collisionErr)
		}
		passwordHash, err := newMirrorPasswordHash()
		if err != nil {
			return nil, nil, fmt.Errorf("generate mirror password: %w", err)
		}
		user := models.User{
			Username:    username,
			Password:    passwordHash,
			DisplayName: truncateRunes(strings.TrimSpace(source.Name), 50),
			Bio:         truncateRunes(strings.TrimSpace(source.Description), 160),
			AvatarURL:   sourceAvatarURLForSync(nil, source, options),
		}
		if err := tx.Create(&user).Error; err != nil {
			return nil, nil, fmt.Errorf("create incremental mirror user %q: %w", username, err)
		}
		account := models.DevDataMirrorAccount{
			RegistryKey:     source.RegistryKey,
			Platform:        configured.Platform,
			SourceUserID:    source.SourceUserID,
			SourceHandle:    source.Handle,
			LocalUserID:     user.ID,
			Category:        source.Category,
			Enabled:         true,
			SourceAvatarURL: source.ProfileImageURL,
			LastFetchedAt:   &fetchedAt,
			CreatedAt:       syncAt,
			UpdatedAt:       syncAt,
		}
		if resolution, ok := avatarResolutionForSync(source, options); ok {
			account.AvatarObjectKey = resolution.ObjectKey
			account.AvatarContentHash = resolution.ContentHash
		}
		if err := tx.Create(&account).Error; err != nil {
			return nil, nil, fmt.Errorf("create incremental DevData mirror account %q: %w", source.RegistryKey, err)
		}
		existingByKey[source.RegistryKey] = account
	}
	return existingByKey, profileChanges, nil
}

func syncIncrementalPosts(tx *gorm.DB, registry SourceRegistry, batch IncrementalBatch, accountsByKey map[string]models.DevDataMirrorAccount, profileChanges map[uint]bool, syncAt time.Time, options SyncOptions, result *SyncResult, maintenance *syncMaintenance) error {
	if len(accountsByKey) == 0 {
		return nil
	}
	for _, account := range accountsByKey {
		if profileChanges[account.ID] {
			var mappings []models.DevDataMirrorPost
			if err := tx.Where("mirror_account_id = ?", account.ID).Find(&mappings).Error; err != nil {
				return fmt.Errorf("load incremental profile mappings for account %q: %w", account.RegistryKey, err)
			}
			for _, mapping := range mappings {
				maintenance.affect(mapping.LocalPostID)
			}
		}
	}

	sourceIDs := make([]string, 0, len(batch.Posts))
	seenSourceIDs := make(map[string]struct{}, len(batch.Posts))
	for _, post := range batch.Posts {
		if _, exists := seenSourceIDs[post.SourcePostID]; !exists {
			sourceIDs = append(sourceIDs, post.SourcePostID)
			seenSourceIDs[post.SourcePostID] = struct{}{}
		}
	}
	mappingsBySource := make(map[string]models.DevDataMirrorPost, len(sourceIDs))
	if len(sourceIDs) > 0 {
		var mappings []models.DevDataMirrorPost
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("platform = ? AND source_post_id IN ?", "x", sourceIDs).Find(&mappings).Error; err != nil {
			return fmt.Errorf("load incremental DevData mirror Post mappings: %w", err)
		}
		for _, mapping := range mappings {
			if mapping.MirrorAccountID == 0 || mapping.LocalPostID == 0 {
				return fmt.Errorf("%w: mapping %d has empty relationship", ErrMirrorMappingInconsistent, mapping.ID)
			}
			if _, exists := mappingsBySource[mapping.SourcePostID]; exists {
				return fmt.Errorf("%w: duplicate source Post %q", ErrMirrorMappingInconsistent, mapping.SourcePostID)
			}
			mappingsBySource[mapping.SourcePostID] = mapping
		}
	}
	for _, desired := range batch.Posts {
		account, exists := accountsByKey[desired.RegistryKey]
		if !exists {
			return fmt.Errorf("incremental account %q was not persisted", desired.RegistryKey)
		}
		mapping, exists := mappingsBySource[desired.SourcePostID]
		if exists {
			if mapping.MirrorAccountID != account.ID {
				return fmt.Errorf("%w: source Post %q is mapped to account %d, want %d", ErrMirrorMappingInconsistent, desired.SourcePostID, mapping.MirrorAccountID, account.ID)
			}
			if mapping.Platform != account.Platform {
				return fmt.Errorf("%w: source Post %q platform changed", ErrSourceIdentityMismatch, desired.SourcePostID)
			}
			if mapping.State != models.DevDataMirrorPostStateActive && mapping.State != models.DevDataMirrorPostStateTombstone {
				return fmt.Errorf("%w: mapping %d has invalid state %q", ErrMirrorMappingInconsistent, mapping.ID, mapping.State)
			}
			if err := syncExistingPost(tx, account, mapping, desired, syncAt, options, maintenance); err != nil {
				return err
			}
			if mapping.State == models.DevDataMirrorPostStateActive {
				result.Kept++
			} else {
				result.Reactivated++
			}
			continue
		}
		if err := insertPost(tx, account, desired, syncAt, options, maintenance); err != nil {
			return err
		}
		result.Inserted++
	}
	return nil
}
