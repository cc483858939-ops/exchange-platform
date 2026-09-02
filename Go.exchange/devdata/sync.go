package devdata

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"Go.exchange/controllers"
	"Go.exchange/likes"
	"Go.exchange/models"
	"Go.exchange/utils"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrSourceIdentityMismatch    = errors.New("DevData source identity mismatch")
	ErrMirrorUsernameCollision   = errors.New("DevData mirror username collision")
	ErrMirrorMappingInconsistent = errors.New("DevData mirror mapping is inconsistent")
)

type SyncResult struct {
	MirrorUsers         int
	ActiveImportedRoots int
	Tombstones          int
	Kept                int
	Reactivated         int
	Inserted            int
	RetiredSoft         int
	RetiredHard         int
	AffectedPostIDs     []uint
	NewPostIDs          []uint
	PurgedPostIDs       []uint
}

type syncMaintenance struct {
	affected map[uint]struct{}
	newPosts map[uint]struct{}
	purged   map[uint]struct{}
}

func newSyncMaintenance() *syncMaintenance {
	return &syncMaintenance{
		affected: make(map[uint]struct{}),
		newPosts: make(map[uint]struct{}),
		purged:   make(map[uint]struct{}),
	}
}

func (m *syncMaintenance) affect(postID uint) {
	if m != nil && postID != 0 {
		m.affected[postID] = struct{}{}
	}
}

func (m *syncMaintenance) addNew(postID uint) {
	if m != nil && postID != 0 {
		m.newPosts[postID] = struct{}{}
		m.affect(postID)
	}
}

func (m *syncMaintenance) addPurge(postID uint) {
	if m != nil && postID != 0 {
		m.purged[postID] = struct{}{}
		m.affect(postID)
	}
}

// SyncSnapshot applies a complete, already validated snapshot as desired
// state. The relational mutation is one transaction; Redis/cache maintenance
// runs only after that transaction commits and is intentionally best effort.
func SyncSnapshot(ctx context.Context, db *gorm.DB, registry SourceRegistry, snapshot Snapshot, redisClient *redis.Client, syncAt time.Time) (SyncResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateSnapshot(snapshot, registry); err != nil {
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
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		profileChanges, err = syncAccounts(tx, registry, snapshot, syncAt)
		if err != nil {
			return err
		}
		if err := syncPosts(tx, registry, snapshot, syncAt, profileChanges, &result, maintenance); err != nil {
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

func syncAccounts(tx *gorm.DB, registry SourceRegistry, snapshot Snapshot, syncAt time.Time) (map[uint]bool, error) {
	var existing []models.DevDataMirrorAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Find(&existing).Error; err != nil {
		return nil, fmt.Errorf("load DevData mirror accounts: %w", err)
	}
	profileChanges := make(map[uint]bool)
	existingByKey := make(map[string]models.DevDataMirrorAccount, len(existing))
	for _, account := range existing {
		if _, exists := existingByKey[account.RegistryKey]; exists {
			return nil, fmt.Errorf("%w: duplicate database registry key %q", ErrMirrorMappingInconsistent, account.RegistryKey)
		}
		existingByKey[account.RegistryKey] = account
	}

	snapshotByKey := make(map[string]SnapshotAccount, len(snapshot.Accounts))
	for _, account := range snapshot.Accounts {
		snapshotByKey[account.RegistryKey] = account
	}
	for _, account := range registry.EnabledAccounts() {
		source, ok := snapshotByKey[account.Key]
		if !ok {
			return nil, fmt.Errorf("snapshot is missing enabled account %q", account.Key)
		}
		stored, exists := existingByKey[account.Key]
		if exists {
			if stored.Platform != account.Platform || stored.SourceUserID != source.SourceUserID {
				return nil, fmt.Errorf("%w: registry key %q is bound to source user %q, snapshot resolved %q", ErrSourceIdentityMismatch, account.Key, stored.SourceUserID, source.SourceUserID)
			}
			if stored.LocalUserID == 0 {
				return nil, fmt.Errorf("%w: registry key %q has no local user", ErrMirrorMappingInconsistent, account.Key)
			}
			var user models.User
			if err := tx.Unscoped().First(&user, stored.LocalUserID).Error; err != nil {
				return nil, fmt.Errorf("%w: load local user for %q: %w", ErrMirrorMappingInconsistent, account.Key, err)
			}
			if user.DeletedAt.Valid {
				return nil, fmt.Errorf("%w: mirror user %q is deleted", ErrMirrorMappingInconsistent, account.Key)
			}
			if user.Username != MirrorUsername(account.Key) {
				return nil, fmt.Errorf("%w: local user %d username is %q, want %q", ErrMirrorMappingInconsistent, user.ID, user.Username, MirrorUsername(account.Key))
			}
			profileChanged, err := updateMirrorUser(tx, &user, source)
			if err != nil {
				return nil, err
			}
			profileChanges[stored.ID] = profileChanged
			if err := tx.Model(&models.DevDataMirrorAccount{}).Where("id = ?", stored.ID).Updates(map[string]interface{}{
				"platform":        stored.Platform,
				"source_handle":   source.Handle,
				"category":        source.Category,
				"enabled":         true,
				"last_fetched_at": sourceFetchedAt(snapshot),
				"updated_at":      syncAt,
			}).Error; err != nil {
				return nil, fmt.Errorf("update DevData mirror account %q: %w", account.Key, err)
			}
			continue
		}

		username := MirrorUsername(account.Key)
		var collision models.User
		collisionErr := tx.Unscoped().Where("username = ?", username).First(&collision).Error
		if collisionErr == nil {
			return nil, fmt.Errorf("%w: %s", ErrMirrorUsernameCollision, username)
		}
		if !errors.Is(collisionErr, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("check mirror username %q: %w", username, collisionErr)
		}
		passwordHash, err := newMirrorPasswordHash()
		if err != nil {
			return nil, fmt.Errorf("generate mirror password: %w", err)
		}
		user := models.User{
			Username:    username,
			Password:    passwordHash,
			DisplayName: truncateRunes(strings.TrimSpace(source.Name), 50),
			Bio:         truncateRunes(strings.TrimSpace(source.Description), 160),
			AvatarURL:   truncateRunes(strings.TrimSpace(source.ProfileImageURL), 512),
		}
		if err := tx.Create(&user).Error; err != nil {
			return nil, fmt.Errorf("create mirror user %q: %w", username, err)
		}
		fetchedAt := sourceFetchedAt(snapshot)
		accountRow := models.DevDataMirrorAccount{
			RegistryKey:   account.Key,
			Platform:      account.Platform,
			SourceUserID:  source.SourceUserID,
			SourceHandle:  source.Handle,
			LocalUserID:   user.ID,
			Category:      source.Category,
			Enabled:       true,
			LastFetchedAt: &fetchedAt,
			CreatedAt:     syncAt,
			UpdatedAt:     syncAt,
		}
		if err := tx.Create(&accountRow).Error; err != nil {
			return nil, fmt.Errorf("create DevData mirror account %q: %w", account.Key, err)
		}
	}

	configuredKeys := make([]string, 0, len(registry.Accounts))
	for _, account := range registry.Accounts {
		configuredKeys = append(configuredKeys, account.Key)
	}
	if len(configuredKeys) > 0 {
		if err := tx.Model(&models.DevDataMirrorAccount{}).
			Where("registry_key NOT IN ? OR enabled = FALSE", configuredKeys).
			Updates(map[string]interface{}{"enabled": false, "updated_at": syncAt}).Error; err != nil {
			return nil, fmt.Errorf("disable removed DevData mirror accounts: %w", err)
		}
	}
	return profileChanges, nil
}

func sourceFetchedAt(snapshot Snapshot) time.Time { return snapshot.FetchedAt.UTC() }

func updateMirrorUser(tx *gorm.DB, user *models.User, source SnapshotAccount) (bool, error) {
	if user == nil {
		return false, nil
	}
	nextDisplayName := truncateRunes(strings.TrimSpace(source.Name), 50)
	nextBio := truncateRunes(strings.TrimSpace(source.Description), 160)
	nextAvatarURL := truncateRunes(strings.TrimSpace(source.ProfileImageURL), 512)
	changed := user.DisplayName != nextDisplayName || user.Bio != nextBio || user.AvatarURL != nextAvatarURL
	if !changed {
		return false, nil
	}
	if err := tx.Model(user).Updates(map[string]interface{}{
		"display_name": nextDisplayName,
		"bio":          nextBio,
		"avatar_url":   nextAvatarURL,
	}).Error; err != nil {
		return false, fmt.Errorf("update mirror user %d profile: %w", user.ID, err)
	}
	user.DisplayName = nextDisplayName
	user.Bio = nextBio
	user.AvatarURL = nextAvatarURL
	return true, nil
}

func newMirrorPasswordHash() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)
	for index := range raw {
		raw[index] = 0
	}
	hash, err := utils.HashPassword(secret)
	secret = ""
	return hash, err
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func syncPosts(tx *gorm.DB, registry SourceRegistry, snapshot Snapshot, syncAt time.Time, profileChanges map[uint]bool, result *SyncResult, maintenance *syncMaintenance) error {
	var accounts []models.DevDataMirrorAccount
	if err := tx.Where("enabled = TRUE").Find(&accounts).Error; err != nil {
		return fmt.Errorf("load enabled DevData mirror accounts: %w", err)
	}
	accountsByKey := make(map[string]models.DevDataMirrorAccount, len(accounts))
	for _, account := range accounts {
		accountsByKey[account.RegistryKey] = account
	}
	var mappings []models.DevDataMirrorPost
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Find(&mappings).Error; err != nil {
		return fmt.Errorf("load DevData mirror Post mappings: %w", err)
	}
	mappingsByAccount := make(map[uint]map[string]models.DevDataMirrorPost)
	mappingsBySource := make(map[string]models.DevDataMirrorPost, len(mappings))
	for _, mapping := range mappings {
		if mapping.MirrorAccountID == 0 || mapping.LocalPostID == 0 {
			return fmt.Errorf("%w: mapping %d has empty relationship", ErrMirrorMappingInconsistent, mapping.ID)
		}
		if _, exists := mappingsByAccount[mapping.MirrorAccountID]; !exists {
			mappingsByAccount[mapping.MirrorAccountID] = make(map[string]models.DevDataMirrorPost)
		}
		key := metadataSourceKey(mapping.Platform, mapping.SourcePostID)
		mappingsByAccount[mapping.MirrorAccountID][mapping.SourcePostID] = mapping
		if _, exists := mappingsBySource[key]; exists {
			return fmt.Errorf("%w: duplicate source Post %q", ErrMirrorMappingInconsistent, mapping.SourcePostID)
		}
		mappingsBySource[key] = mapping
	}
	desiredByAccount := make(map[string][]SnapshotPost)
	for _, post := range snapshot.Posts {
		desiredByAccount[post.RegistryKey] = append(desiredByAccount[post.RegistryKey], post)
	}
	for _, configured := range registry.EnabledAccounts() {
		account, exists := accountsByKey[configured.Key]
		if !exists {
			return fmt.Errorf("enabled mirror account %q was not persisted", configured.Key)
		}
		if account.Platform != configured.Platform {
			return fmt.Errorf("%w: account %q platform changed", ErrSourceIdentityMismatch, configured.Key)
		}
		desired := desiredByAccount[configured.Key]
		current := mappingsByAccount[account.ID]
		if current == nil {
			current = make(map[string]models.DevDataMirrorPost)
		}
		if profileChanges[account.ID] {
			for _, mapping := range current {
				maintenance.affect(mapping.LocalPostID)
			}
		}
		for _, desiredPost := range desired {
			mapping, exists := current[desiredPost.SourcePostID]
			if exists {
				if mapping.State != models.DevDataMirrorPostStateActive && mapping.State != models.DevDataMirrorPostStateTombstone {
					return fmt.Errorf("%w: mapping %d has invalid state %q", ErrMirrorMappingInconsistent, mapping.ID, mapping.State)
				}
				if mapping.Platform != configured.Platform {
					return fmt.Errorf("%w: source Post %q platform changed", ErrSourceIdentityMismatch, desiredPost.SourcePostID)
				}
				if err := syncExistingPost(tx, account, mapping, desiredPost, syncAt, maintenance); err != nil {
					return err
				}
				if mapping.State == models.DevDataMirrorPostStateActive {
					result.Kept++
				} else {
					result.Reactivated++
				}
				continue
			}
			if other, exists := mappingsBySource[metadataSourceKey(configured.Platform, desiredPost.SourcePostID)]; exists {
				return fmt.Errorf("%w: source Post %q is mapped to account %d", ErrMirrorMappingInconsistent, desiredPost.SourcePostID, other.MirrorAccountID)
			}
			if err := insertPost(tx, account, desiredPost, syncAt, maintenance); err != nil {
				return err
			}
			result.Inserted++
		}
		for sourceID, mapping := range current {
			if _, exists := desiredPostByID(desired, sourceID); exists {
				continue
			}
			switch mapping.State {
			case models.DevDataMirrorPostStateActive:
				retiredSoft, err := retirePost(tx, mapping, syncAt, maintenance)
				if err != nil {
					return err
				}
				if retiredSoft {
					result.RetiredSoft++
				} else {
					result.RetiredHard++
				}
			case models.DevDataMirrorPostStateTombstone:
				if err := gcTombstone(tx, mapping, syncAt, result, maintenance); err != nil {
					return err
				}
			default:
				return fmt.Errorf("%w: mapping %d has invalid state %q", ErrMirrorMappingInconsistent, mapping.ID, mapping.State)
			}
		}
	}
	return nil
}

func desiredPostByID(posts []SnapshotPost, sourceID string) (SnapshotPost, bool) {
	for _, post := range posts {
		if post.SourcePostID == sourceID {
			return post, true
		}
	}
	return SnapshotPost{}, false
}

func syncExistingPost(tx *gorm.DB, account models.DevDataMirrorAccount, mapping models.DevDataMirrorPost, desired SnapshotPost, syncAt time.Time, maintenance *syncMaintenance) error {
	var post models.Post
	if err := tx.Unscoped().First(&post, mapping.LocalPostID).Error; err != nil {
		return fmt.Errorf("%w: load mapped Post %d: %w", ErrMirrorMappingInconsistent, mapping.LocalPostID, err)
	}
	if post.ReplyToPostID != nil || post.QuotePostID != nil || post.ConversationID != nil {
		return fmt.Errorf("%w: imported Post %d is not a root", ErrMirrorMappingInconsistent, post.ID)
	}
	contentChanged := post.Content != desired.Text || post.AuthorID != account.LocalUserID || post.Visibility != "public" || !post.CreatedAt.Equal(desired.CreatedAt.UTC())
	reactivate := mapping.State == models.DevDataMirrorPostStateTombstone || post.DeletedAt.Valid
	values := map[string]interface{}{
		"author_id":  account.LocalUserID,
		"content":    desired.Text,
		"created_at": desired.CreatedAt.UTC(),
		"updated_at": syncAt,
		"visibility": "public",
	}
	if reactivate {
		values["deleted_at"] = nil
	}
	update := tx.Unscoped().Model(&models.Post{}).Where("id = ?", post.ID).Updates(values)
	if update.Error != nil || update.RowsAffected != 1 {
		if update.Error != nil {
			return fmt.Errorf("update imported Post %d: %w", post.ID, update.Error)
		}
		return fmt.Errorf("update imported Post %d affected %d rows", post.ID, update.RowsAffected)
	}
	if contentChanged || reactivate {
		maintenance.affect(post.ID)
	}
	if err := updateMirrorMapping(tx, mapping, account.ID, desired, models.DevDataMirrorPostStateActive, syncAt); err != nil {
		return err
	}
	return nil
}

func insertPost(tx *gorm.DB, account models.DevDataMirrorAccount, desired SnapshotPost, syncAt time.Time, maintenance *syncMaintenance) error {
	post := models.Post{
		Model:           gorm.Model{CreatedAt: desired.CreatedAt.UTC(), UpdatedAt: syncAt},
		AuthorID:        account.LocalUserID,
		Content:         desired.Text,
		Visibility:      "public",
		LikeCount:       0,
		ReplyCount:      0,
		ViewCount:       0,
		LikeSyncVersion: 0,
	}
	if err := tx.Create(&post).Error; err != nil {
		return fmt.Errorf("insert imported Post %q: %w", desired.SourcePostID, err)
	}
	mapping := models.DevDataMirrorPost{
		Platform:          "x",
		SourcePostID:      desired.SourcePostID,
		SourceURL:         desired.SourceURL,
		MirrorAccountID:   account.ID,
		LocalPostID:       post.ID,
		SourceCreatedAt:   desired.CreatedAt.UTC(),
		SourceLikeCount:   desired.SourceMetrics.LikeCount,
		SourceReplyCount:  desired.SourceMetrics.ReplyCount,
		SourceRepostCount: desired.SourceMetrics.RepostCount,
		SourceQuoteCount:  desired.SourceMetrics.QuoteCount,
		ContentHash:       SourceTextContentHash(desired.Text),
		State:             models.DevDataMirrorPostStateActive,
		ImportedAt:        syncAt,
		CreatedAt:         syncAt,
		UpdatedAt:         syncAt,
	}
	if err := tx.Create(&mapping).Error; err != nil {
		return fmt.Errorf("create DevData mirror Post mapping %q: %w", desired.SourcePostID, err)
	}
	maintenance.addNew(post.ID)
	return nil
}

func updateMirrorMapping(tx *gorm.DB, mapping models.DevDataMirrorPost, accountID uint, desired SnapshotPost, state string, syncAt time.Time) error {
	values := map[string]interface{}{
		"platform":            "x",
		"source_post_id":      desired.SourcePostID,
		"source_url":          desired.SourceURL,
		"mirror_account_id":   accountID,
		"source_created_at":   desired.CreatedAt.UTC(),
		"source_like_count":   desired.SourceMetrics.LikeCount,
		"source_reply_count":  desired.SourceMetrics.ReplyCount,
		"source_repost_count": desired.SourceMetrics.RepostCount,
		"source_quote_count":  desired.SourceMetrics.QuoteCount,
		"content_hash":        SourceTextContentHash(desired.Text),
		"state":               state,
		"updated_at":          syncAt,
	}
	if err := tx.Model(&models.DevDataMirrorPost{}).Where("id = ?", mapping.ID).Updates(values).Error; err != nil {
		return fmt.Errorf("update DevData mirror Post mapping %q: %w", desired.SourcePostID, err)
	}
	return nil
}

func retirePost(tx *gorm.DB, mapping models.DevDataMirrorPost, syncAt time.Time, maintenance *syncMaintenance) (bool, error) {
	target, hasDependency, err := lockAndCheckPostDependencies(tx, mapping.LocalPostID)
	if err != nil {
		return false, err
	}
	if hasDependency {
		if err := tx.Unscoped().Where("post_id = ?", target.ID).Delete(&models.PostRepost{}).Error; err != nil {
			return false, fmt.Errorf("delete reposts for tombstone Post %d: %w", target.ID, err)
		}
		if err := softDeleteImportedPost(tx, target.ID, syncAt); err != nil {
			return false, err
		}
		if err := updateMappingState(tx, mapping.ID, models.DevDataMirrorPostStateTombstone, syncAt); err != nil {
			return false, err
		}
		maintenance.addPurge(target.ID)
		maintenance.affect(target.ID)
		return true, nil
	}
	return false, hardDeleteImportedPost(tx, target, maintenance)
}

func gcTombstone(tx *gorm.DB, mapping models.DevDataMirrorPost, syncAt time.Time, result *SyncResult, maintenance *syncMaintenance) error {
	target, hasDependency, err := lockAndCheckPostDependencies(tx, mapping.LocalPostID)
	if err != nil {
		return err
	}
	if hasDependency {
		return nil
	}
	if err := hardDeleteImportedPost(tx, target, maintenance); err != nil {
		return err
	}
	if result != nil {
		result.RetiredHard++
	}
	return nil
}

func lockAndCheckPostDependencies(tx *gorm.DB, postID uint) (models.Post, bool, error) {
	var target models.Post
	if err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).Where("posts.id = ?", postID).First(&target).Error; err != nil {
		return models.Post{}, false, fmt.Errorf("%w: lock target Post %d: %w", ErrMirrorMappingInconsistent, postID, err)
	}
	var count int64
	if err := tx.Unscoped().Model(&models.Post{}).Where(
		"reply_to_post_id = ? OR quote_post_id = ? OR conversation_id = ?", postID, postID, postID,
	).Count(&count).Error; err != nil {
		return models.Post{}, false, fmt.Errorf("check structural dependencies for Post %d: %w", postID, err)
	}
	return target, count > 0, nil
}

func softDeleteImportedPost(tx *gorm.DB, postID uint, syncAt time.Time) error {
	update := tx.Unscoped().Model(&models.Post{}).Where("id = ?", postID).Updates(map[string]interface{}{
		"deleted_at": syncAt,
		"updated_at": syncAt,
	})
	if update.Error != nil {
		return fmt.Errorf("soft delete imported Post %d: %w", postID, update.Error)
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("soft delete imported Post %d affected %d rows", postID, update.RowsAffected)
	}
	return nil
}

func hardDeleteImportedPost(tx *gorm.DB, target models.Post, maintenance *syncMaintenance) error {
	if err := tx.Where("post_id = ?", target.ID).Delete(&models.Notification{}).Error; err != nil {
		return fmt.Errorf("delete derived notifications for Post %d: %w", target.ID, err)
	}
	deleteResult := tx.Unscoped().Delete(&target)
	if deleteResult.Error != nil {
		return fmt.Errorf("hard delete imported Post %d: %w", target.ID, deleteResult.Error)
	}
	if deleteResult.RowsAffected != 1 {
		return fmt.Errorf("hard delete imported Post %d affected %d rows", target.ID, deleteResult.RowsAffected)
	}
	maintenance.addPurge(target.ID)
	maintenance.affect(target.ID)
	return nil
}

func updateMappingState(tx *gorm.DB, mappingID uint, state string, syncAt time.Time) error {
	if err := tx.Model(&models.DevDataMirrorPost{}).Where("id = ?", mappingID).Updates(map[string]interface{}{
		"state":      state,
		"updated_at": syncAt,
	}).Error; err != nil {
		return fmt.Errorf("update DevData tombstone state %d: %w", mappingID, err)
	}
	return nil
}

func readSyncCounts(tx *gorm.DB, result *SyncResult) error {
	if result == nil {
		return nil
	}
	var mirrorUsers, activeRoots, tombstones int64
	if err := tx.Model(&models.DevDataMirrorAccount{}).Where("enabled = TRUE").Count(&mirrorUsers).Error; err != nil {
		return fmt.Errorf("count mirror users: %w", err)
	}
	if err := tx.Table("devdata_mirror_posts AS mp").
		Joins("JOIN devdata_mirror_accounts AS ma ON ma.id = mp.mirror_account_id").
		Where("mp.state = ? AND ma.enabled = TRUE", models.DevDataMirrorPostStateActive).
		Count(&activeRoots).Error; err != nil {
		return fmt.Errorf("count active imported roots: %w", err)
	}
	if err := tx.Model(&models.DevDataMirrorPost{}).Where("state = ?", models.DevDataMirrorPostStateTombstone).Count(&tombstones).Error; err != nil {
		return fmt.Errorf("count tombstones: %w", err)
	}
	result.MirrorUsers = int(mirrorUsers)
	result.ActiveImportedRoots = int(activeRoots)
	result.Tombstones = int(tombstones)
	return nil
}

func performPostCommitMaintenance(ctx context.Context, redisClient *redis.Client, maintenance *syncMaintenance) {
	if redisClient == nil || maintenance == nil {
		return
	}
	for _, postID := range sortedIDs(maintenance.affected) {
		if err := controllers.InvalidatePostDetailCacheByIDWithRedis(redisClient, postID); err != nil {
			log.Printf("WARN [DevData] invalidate Post detail cache for %d: %v", postID, err)
		}
	}
	store := likes.NewStore(redisClient)
	for _, postID := range sortedIDs(maintenance.newPosts) {
		if _, err := store.Initialize(ctx, postID, 0, 0, nil); err != nil {
			log.Printf("WARN [DevData] initialize Redis like state for %d: %v", postID, err)
		}
	}
	for _, postID := range sortedIDs(maintenance.purged) {
		if err := store.PurgePost(ctx, postID); err != nil {
			log.Printf("WARN [DevData] purge Redis like state for %d: %v", postID, err)
		}
	}
}

func sortedIDs(values map[uint]struct{}) []uint {
	ids := make([]uint, 0, len(values))
	for id := range values {
		if id != 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
