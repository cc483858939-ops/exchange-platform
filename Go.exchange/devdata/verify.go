package devdata

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

const DefaultColdFeedLimit = 20

type AccountVerification struct {
	RegistryKey string
	Active      int
	MaxPosts    int
}

type CoreVerification struct {
	RegistryEnabled     int
	MirrorUsers         int
	ActiveImportedRoots int
	Tombstones          int
	Age24Hours          int
	Age72Hours          int
	Age7Days            int
	Age30Days           int
	AgeOlder            int
	RecentCandidates    int
	FinalColdFeed       int
	PerAccount          []AccountVerification
}

func VerifyCore(ctx context.Context, db *gorm.DB, registry SourceRegistry, now time.Time) (CoreVerification, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil {
		return CoreVerification{}, errors.New("database is not initialized")
	}
	if err := ValidateRegistry(registry); err != nil {
		return CoreVerification{}, err
	}
	if err := ValidateMetadataSchema(ctx, db); err != nil {
		return CoreVerification{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result := CoreVerification{RegistryEnabled: len(registry.EnabledAccounts())}
	var mirrorUsers, activeRoots, tombstones int64
	if err := db.WithContext(ctx).Model(&models.DevDataMirrorAccount{}).Where("enabled = TRUE").Count(&mirrorUsers).Error; err != nil {
		return CoreVerification{}, fmt.Errorf("count mirror users: %w", err)
	}
	if err := db.WithContext(ctx).Table("devdata_mirror_posts AS mp").
		Joins("JOIN devdata_mirror_accounts AS ma ON ma.id = mp.mirror_account_id").
		Where("mp.state = ? AND ma.enabled = TRUE", models.DevDataMirrorPostStateActive).
		Count(&activeRoots).Error; err != nil {
		return CoreVerification{}, fmt.Errorf("count active imported roots: %w", err)
	}
	if err := db.WithContext(ctx).Model(&models.DevDataMirrorPost{}).Where("state = ?", models.DevDataMirrorPostStateTombstone).Count(&tombstones).Error; err != nil {
		return CoreVerification{}, fmt.Errorf("count tombstones: %w", err)
	}
	result.MirrorUsers = int(mirrorUsers)
	result.ActiveImportedRoots = int(activeRoots)
	result.Tombstones = int(tombstones)

	var posts []models.DevDataMirrorPost
	if err := db.WithContext(ctx).Table("devdata_mirror_posts AS mp").
		Joins("JOIN devdata_mirror_accounts AS ma ON ma.id = mp.mirror_account_id").
		Where("mp.state = ? AND ma.enabled = TRUE", models.DevDataMirrorPostStateActive).
		Select("mp.*").
		Find(&posts).Error; err != nil {
		return CoreVerification{}, fmt.Errorf("load active imported roots: %w", err)
	}
	cutoffs := []time.Duration{24 * time.Hour, 72 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour}
	for _, post := range posts {
		age := now.Sub(post.SourceCreatedAt.UTC())
		switch {
		case age <= cutoffs[0]:
			result.Age24Hours++
		case age <= cutoffs[1]:
			result.Age72Hours++
		case age <= cutoffs[2]:
			result.Age7Days++
		case age <= cutoffs[3]:
			result.Age30Days++
		default:
			result.AgeOlder++
		}
	}

	perAccount := make(map[string]AccountVerification, len(registry.EnabledAccounts()))
	for _, account := range registry.EnabledAccounts() {
		perAccount[account.Key] = AccountVerification{RegistryKey: account.Key, MaxPosts: account.MaxPosts}
	}
	var rows []struct {
		RegistryKey string `gorm:"column:registry_key"`
		Active      int64  `gorm:"column:active_count"`
	}
	if err := db.WithContext(ctx).Table("devdata_mirror_accounts AS a").
		Select("a.registry_key, COUNT(mp.id) AS active_count").
		Joins("LEFT JOIN devdata_mirror_posts AS mp ON mp.mirror_account_id = a.id AND mp.state = ?", models.DevDataMirrorPostStateActive).
		Where("a.enabled = TRUE").
		Group("a.registry_key").Scan(&rows).Error; err != nil {
		return CoreVerification{}, fmt.Errorf("count active roots per account: %w", err)
	}
	for _, row := range rows {
		item, exists := perAccount[row.RegistryKey]
		if exists {
			item.Active = int(row.Active)
			perAccount[row.RegistryKey] = item
		}
	}
	for _, account := range registry.EnabledAccounts() {
		item := perAccount[account.Key]
		if item.Active > item.MaxPosts {
			return CoreVerification{}, fmt.Errorf("active imported roots for %q exceed max_posts=%d", account.Key, item.MaxPosts)
		}
		result.PerAccount = append(result.PerAccount, item)
	}
	if result.MirrorUsers != result.RegistryEnabled {
		return CoreVerification{}, fmt.Errorf("enabled mirror users=%d, want %d", result.MirrorUsers, result.RegistryEnabled)
	}
	maxActive := 0
	for _, account := range registry.EnabledAccounts() {
		maxActive += account.MaxPosts
	}
	if result.ActiveImportedRoots > maxActive {
		return CoreVerification{}, fmt.Errorf("active imported roots=%d exceed configured maximum=%d", result.ActiveImportedRoots, maxActive)
	}
	sort.Slice(result.PerAccount, func(i, j int) bool { return result.PerAccount[i].RegistryKey < result.PerAccount[j].RegistryKey })

	// Imported posts are root public posts and do not have PostArticle rows in
	// V1. This query mirrors the existing recent root candidate shape without
	// modifying or invoking the recommender implementation.
	var recent int64
	if err := db.WithContext(ctx).Table("posts").
		Where("deleted_at IS NULL AND visibility = 'public' AND reply_to_post_id IS NULL AND created_at <= ?", now).
		Count(&recent).Error; err != nil {
		return CoreVerification{}, fmt.Errorf("count recent candidates: %w", err)
	}
	result.RecentCandidates = int(recent)
	if result.RecentCandidates > DefaultColdFeedLimit {
		result.FinalColdFeed = DefaultColdFeedLimit
	} else {
		result.FinalColdFeed = result.RecentCandidates
	}
	return result, nil
}

func FormatCoreVerification(result CoreVerification) string {
	var output string
	output += fmt.Sprintf("Registry enabled: %d\n", result.RegistryEnabled)
	output += fmt.Sprintf("Mirror users: %d\n", result.MirrorUsers)
	output += fmt.Sprintf("Active imported roots: %d\n", result.ActiveImportedRoots)
	output += fmt.Sprintf("Tombstones: %d\n", result.Tombstones)
	output += "Per account:\n"
	for _, account := range result.PerAccount {
		output += fmt.Sprintf("%s %d/%d\n", MirrorUsername(account.RegistryKey), account.Active, account.MaxPosts)
	}
	output += fmt.Sprintf("<=24h %d\n", result.Age24Hours)
	output += fmt.Sprintf("<=72h %d\n", result.Age72Hours)
	output += fmt.Sprintf("<=7d %d\n", result.Age7Days)
	output += fmt.Sprintf("<=30d %d\n", result.Age30Days)
	output += fmt.Sprintf(">30d %d\n", result.AgeOlder)
	output += fmt.Sprintf("Recent candidates for cold user: %d\n", result.RecentCandidates)
	output += fmt.Sprintf("Final cold Feed: %d\n", result.FinalColdFeed)
	return output
}
