package devdata

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"Go.exchange/controllers"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const DefaultColdFeedLimit = 20

const (
	VerificationModeGeneric         = "generic"
	VerificationModeCuratedV1Live   = "curated_v1_live"
	curatedV1MinimumPopulatedUsers  = 12
	curatedV1MinimumActiveRootPosts = 300
)

type VerificationOptions struct {
	Mode string
}

type AccountVerification struct {
	RegistryKey string
	Active      int
	MaxPosts    int
}

type CoreVerification struct {
	RegistryEnabled         int
	MirrorUsers             int
	PopulatedSourceAccounts int
	ActiveImportedRoots     int
	Tombstones              int
	Age24Hours              int
	Age72Hours              int
	Age7Days                int
	Age30Days               int
	AgeOlder                int
	RecentCandidates        int
	FinalColdFeed           int
	DevDataRecentCandidates int
	DevDataFinalFeed        int
	DevDataPostsInFinalFeed bool
	PerAccount              []AccountVerification
}

func VerifyCore(ctx context.Context, db *gorm.DB, registry SourceRegistry, now time.Time) (CoreVerification, error) {
	return VerifyCoreWithOptions(ctx, db, registry, now, VerificationOptions{Mode: VerificationModeGeneric})
}

func VerifyCoreWithOptions(ctx context.Context, db *gorm.DB, registry SourceRegistry, now time.Time, options VerificationOptions) (CoreVerification, error) {
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
	var mirrorUsers, populatedAccounts, activeRoots, tombstones int64
	if err := db.WithContext(ctx).Model(&models.DevDataMirrorAccount{}).Where("enabled = TRUE").Count(&mirrorUsers).Error; err != nil {
		return CoreVerification{}, fmt.Errorf("count mirror users: %w", err)
	}
	if err := db.WithContext(ctx).Table("devdata_mirror_accounts AS ma").
		Select("COUNT(DISTINCT ma.id)").
		Joins("JOIN devdata_mirror_posts AS mp ON mp.mirror_account_id = ma.id AND mp.state = ?", models.DevDataMirrorPostStateActive).
		Where("ma.enabled = TRUE").
		Scan(&populatedAccounts).Error; err != nil {
		return CoreVerification{}, fmt.Errorf("count populated source accounts: %w", err)
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
	result.PopulatedSourceAccounts = int(populatedAccounts)
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

	if options.Mode == VerificationModeCuratedV1Live {
		if err := ValidateCuratedV1Registry(registry); err != nil {
			return CoreVerification{}, fmt.Errorf("curated live verification requires the V1 registry: %w", err)
		}
		if result.PopulatedSourceAccounts < curatedV1MinimumPopulatedUsers {
			return CoreVerification{}, fmt.Errorf("populated source accounts=%d, want at least %d for curated live activation", result.PopulatedSourceAccounts, curatedV1MinimumPopulatedUsers)
		}
		if result.ActiveImportedRoots < curatedV1MinimumActiveRootPosts {
			return CoreVerification{}, fmt.Errorf("active imported roots=%d, want at least %d for curated live activation", result.ActiveImportedRoots, curatedV1MinimumActiveRootPosts)
		}
	}

	serving, err := verifyActualRecommendationServing(db, now)
	if err != nil {
		return CoreVerification{}, err
	}
	result.RecentCandidates = serving.RecentCandidateCount
	result.FinalColdFeed = len(serving.FinalPostIDs)
	if result.RecentCandidates <= 0 {
		return CoreVerification{}, errors.New("actual recommendation path returned no recent recall candidates")
	}
	if result.FinalColdFeed <= 0 {
		return CoreVerification{}, errors.New("actual recommendation path returned no final results")
	}
	result.DevDataRecentCandidates, err = countActiveDevDataPosts(db, serving.RecentPostIDs)
	if err != nil {
		return CoreVerification{}, fmt.Errorf("count DevData recent candidates: %w", err)
	}
	result.DevDataFinalFeed, err = countActiveDevDataPosts(db, serving.FinalPostIDs)
	if err != nil {
		return CoreVerification{}, fmt.Errorf("count DevData final results: %w", err)
	}
	result.DevDataPostsInFinalFeed = result.DevDataFinalFeed > 0
	if !result.DevDataPostsInFinalFeed {
		return CoreVerification{}, errors.New("actual recommendation final results contain no active DevData imported Post")
	}
	return result, nil
}

func verifyActualRecommendationServing(db *gorm.DB, now time.Time) (controllers.RecommendationServingVerification, error) {
	if db == nil {
		return controllers.RecommendationServingVerification{}, errors.New("database is not initialized")
	}
	previousDB := global.Db
	global.Db = db
	defer func() { global.Db = previousDB }()

	verificationUser := models.User{
		Username:    "x_devdata_verify_" + uuid.NewString(),
		DisplayName: "DevData verification user",
	}
	if err := db.Create(&verificationUser).Error; err != nil {
		return controllers.RecommendationServingVerification{}, fmt.Errorf("create isolated cold-start verification user: %w", err)
	}
	verification, verifyErr := controllers.VerifyRecommendationServing(verificationUser.ID, DefaultColdFeedLimit, now)
	cleanupErr := cleanupVerificationUser(db, verificationUser.ID)
	if verifyErr != nil {
		if cleanupErr != nil {
			return controllers.RecommendationServingVerification{}, fmt.Errorf("actual recommendation serving verification: %v; cleanup: %w", verifyErr, cleanupErr)
		}
		return controllers.RecommendationServingVerification{}, fmt.Errorf("actual recommendation serving verification: %w", verifyErr)
	}
	if cleanupErr != nil {
		return controllers.RecommendationServingVerification{}, cleanupErr
	}
	return verification, nil
}

func cleanupVerificationUser(db *gorm.DB, userID uint) error {
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Where("follower_id = ? OR following_id = ?", userID, userID).Delete(&models.UserFollow{}).Error; err != nil {
			return fmt.Errorf("cleanup verification user follows: %w", err)
		}
		for _, row := range []interface{}{
			&models.PostReaction{},
			&models.PostBehavior{},
			&models.UserPostRecoState{},
			&models.UserAuthorAffinity{},
			&models.UserRecoProfileDirty{},
			&models.UserRecoProfile{},
			&models.RecommendationRequest{},
			&models.PostRepost{},
		} {
			if err := tx.Unscoped().Where("user_id = ?", userID).Delete(row).Error; err != nil {
				return fmt.Errorf("cleanup verification user data %T: %w", row, err)
			}
		}
		if err := tx.Unscoped().Where("id = ?", userID).Delete(&models.User{}).Error; err != nil {
			return fmt.Errorf("delete verification user: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func countActiveDevDataPosts(db *gorm.DB, postIDs []uint) (int, error) {
	if len(postIDs) == 0 {
		return 0, nil
	}
	var count int64
	if err := db.Table("devdata_mirror_posts AS mp").
		Joins("JOIN devdata_mirror_accounts AS ma ON ma.id = mp.mirror_account_id").
		Where("mp.state = ? AND ma.enabled = TRUE AND mp.local_post_id IN ?", models.DevDataMirrorPostStateActive, postIDs).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

func FormatCoreVerification(result CoreVerification) string {
	var output string
	output += fmt.Sprintf("Registry enabled: %d\n", result.RegistryEnabled)
	output += fmt.Sprintf("Mirror users: %d\n", result.MirrorUsers)
	output += fmt.Sprintf("Populated source accounts: %d\n", result.PopulatedSourceAccounts)
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
	output += fmt.Sprintf("DevData recent candidates: %d\n", result.DevDataRecentCandidates)
	output += fmt.Sprintf("DevData final Feed results: %d\n", result.DevDataFinalFeed)
	output += fmt.Sprintf("DevData Post in final results: %t\n", result.DevDataPostsInFinalFeed)
	return output
}
