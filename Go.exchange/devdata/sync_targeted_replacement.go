package devdata

import (
	"context"
	"errors"
	"fmt"
	"time"

	"Go.exchange/models"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SyncTargetedReplacement applies the old->new mirror transition in one DB
// transaction. The old mirror row and local user are retained, while its
// imported posts follow the existing full-sync retirement semantics.
func SyncTargetedReplacement(ctx context.Context, db *gorm.DB, registry SourceRegistry, baseline Snapshot, oldKey string, batch TargetedRefreshBatch, redisClient *redis.Client, syncAt time.Time, options SyncOptions) (SyncResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	replacement := normalizedReplacement(RegistryReplacement{OldKey: oldKey, NewKey: batch.RegistryKey})
	oldKey = replacement.OldKey
	if err := ValidateReplacementBaseline(baseline, registry, replacement); err != nil {
		return SyncResult{}, err
	}
	if err := ValidateTargetedRefreshBatch(batch, registry); err != nil {
		return SyncResult{}, err
	}
	// Re-run the in-memory transition merge at the sync boundary. The CLI
	// already builds this snapshot before media preparation, but callers of
	// this public sync function must receive the same strict final-snapshot
	// and source-identity guarantees without relying on that orchestration.
	if _, err := MergeReplacementSnapshot(baseline, batch, registry, oldKey, time.Time{}); err != nil {
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
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		oldAccount, newAccount, err := lockReplacementAccounts(tx, replacement)
		if err != nil {
			return err
		}
		if err := validateReplacementDatabaseState(tx, registry, baseline, replacement, batch, oldAccount, newAccount); err != nil {
			return err
		}
		if oldAccount != nil {
			if err := retireReplacementAccountPosts(tx, *oldAccount, syncAt, &result, maintenance); err != nil {
				return err
			}
			if err := tx.Model(&models.DevDataMirrorAccount{}).Where("id = ?", oldAccount.ID).Updates(map[string]interface{}{
				"enabled":    false,
				"updated_at": syncAt,
			}).Error; err != nil {
				return fmt.Errorf("disable retired DevData mirror account %q: %w", replacement.OldKey, err)
			}
		}

		incrementalBatch := IncrementalBatch{
			FetchedAt: batch.FetchedAt,
			Accounts:  batch.Accounts,
			Posts:     batch.Posts,
		}
		accountsByKey, profileChanges, err := syncIncrementalAccounts(tx, registry, incrementalBatch, syncAt, options)
		if err != nil {
			return err
		}
		if err := syncIncrementalPosts(tx, registry, incrementalBatch, accountsByKey, profileChanges, syncAt, options, &result, maintenance); err != nil {
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

func lockReplacementAccounts(tx *gorm.DB, replacement RegistryReplacement) (*models.DevDataMirrorAccount, *models.DevDataMirrorAccount, error) {
	var rows []models.DevDataMirrorAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("registry_key IN ?", []string{replacement.OldKey, replacement.NewKey}).Find(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("load replacement DevData mirror accounts: %w", err)
	}
	byKey := make(map[string]models.DevDataMirrorAccount, len(rows))
	for _, row := range rows {
		if _, exists := byKey[row.RegistryKey]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate database registry key %q", ErrMirrorMappingInconsistent, row.RegistryKey)
		}
		byKey[row.RegistryKey] = row
	}
	var oldAccount *models.DevDataMirrorAccount
	if row, ok := byKey[replacement.OldKey]; ok {
		oldAccount = &row
	}
	var newAccount *models.DevDataMirrorAccount
	if row, ok := byKey[replacement.NewKey]; ok {
		newAccount = &row
	}
	return oldAccount, newAccount, nil
}

func validateReplacementDatabaseState(tx *gorm.DB, registry SourceRegistry, baseline Snapshot, replacement RegistryReplacement, batch TargetedRefreshBatch, oldAccount, newAccount *models.DevDataMirrorAccount) error {
	oldSource := replacementBaselineAccountByKey(baseline, replacement.OldKey)
	newSource := batch.Accounts[0]
	newConfigured, exists := registry.AccountByKey(replacement.NewKey)
	if !exists || !newConfigured.Enabled {
		return fmt.Errorf("replacement new registry key %q is not enabled", replacement.NewKey)
	}
	if oldAccount != nil {
		if oldAccount.Platform != "x" || oldAccount.SourceUserID != oldSource.SourceUserID {
			return fmt.Errorf("%w: old registry key %q is bound to source user %q, baseline resolved %q", ErrSourceIdentityMismatch, replacement.OldKey, oldAccount.SourceUserID, oldSource.SourceUserID)
		}
		if err := validateReplacementMirrorUser(tx, *oldAccount, replacement.OldKey); err != nil {
			return err
		}
	}
	if newAccount != nil {
		if newAccount.Enabled {
			return fmt.Errorf("replacement new registry key %q already has an enabled mirror account", replacement.NewKey)
		}
		if newAccount.Platform != newConfigured.Platform || newAccount.SourceUserID != newSource.SourceUserID {
			return fmt.Errorf("%w: replacement new registry key %q is bound to source user %q, fetched %q", ErrSourceIdentityMismatch, replacement.NewKey, newAccount.SourceUserID, newSource.SourceUserID)
		}
		if err := validateReplacementMirrorUser(tx, *newAccount, replacement.NewKey); err != nil {
			return err
		}
	}

	var collisions []models.DevDataMirrorAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"platform = ? AND source_user_id = ? AND registry_key <> ?",
		newConfigured.Platform, newSource.SourceUserID, replacement.NewKey,
	).Find(&collisions).Error; err != nil {
		return fmt.Errorf("check replacement source identity: %w", err)
	}
	for _, collision := range collisions {
		if collision.RegistryKey == replacement.OldKey {
			return fmt.Errorf("%w: replacement new source user conflicts with retired account %q", ErrSourceIdentityMismatch, collision.RegistryKey)
		}
		return fmt.Errorf("%w: replacement new source user conflicts with retained account %q", ErrSourceIdentityMismatch, collision.RegistryKey)
	}
	return nil
}

func validateReplacementMirrorUser(tx *gorm.DB, account models.DevDataMirrorAccount, registryKey string) error {
	if account.LocalUserID == 0 {
		return fmt.Errorf("%w: registry key %q has no local user", ErrMirrorMappingInconsistent, registryKey)
	}
	var user models.User
	if err := tx.Unscoped().First(&user, account.LocalUserID).Error; err != nil {
		return fmt.Errorf("%w: load local user for %q: %w", ErrMirrorMappingInconsistent, registryKey, err)
	}
	if user.DeletedAt.Valid || user.Username != MirrorUsername(registryKey) {
		return fmt.Errorf("%w: local mirror user for %q is invalid", ErrMirrorMappingInconsistent, registryKey)
	}
	return nil
}

func retireReplacementAccountPosts(tx *gorm.DB, account models.DevDataMirrorAccount, syncAt time.Time, result *SyncResult, maintenance *syncMaintenance) error {
	var mappings []models.DevDataMirrorPost
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("mirror_account_id = ?", account.ID).Find(&mappings).Error; err != nil {
		return fmt.Errorf("load replacement posts for account %q: %w", account.RegistryKey, err)
	}
	for _, mapping := range mappings {
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
	return nil
}
