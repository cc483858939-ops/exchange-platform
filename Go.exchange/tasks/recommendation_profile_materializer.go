package tasks

import (
	"context"
	"errors"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/metrics"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const recommendationProfileAdvisoryLockNamespace int64 = -811128441771

var errRecommendationProfileLockSkipped = errors.New("recommendation profile advisory lock unavailable")

type recommendationProfileMaterializerClaim struct {
	UserID       uint
	DirtyVersion int64
	Attempts     int
}

func startRecommendationProfileMaterializer(ctx context.Context, wg *sync.WaitGroup) {
	// This worker is deliberately unconditional. Configuration controls its
	// cadence and batch sizes, not whether durable invalidations are serviced.
	wg.Add(1)
	go func() {
		defer wg.Done()
		PipelineStarted(PipelineRecommendationProfile)
		defer PipelineStopped(PipelineRecommendationProfile)
		lastRebase := time.Time{}
		for {
			settings := recommendationProfileMaterializerSettings()
			now := time.Now().UTC()
			if global.Db != nil {
				if err := materializeDueRecommendationProfiles(now, settings); err != nil {
					PipelineFailure(PipelineRecommendationProfile, "materialization_failed", 0)
					log.Printf("[RecommendationProfile] materialize due profiles: %v", err)
				} else {
					PipelineSuccess(PipelineRecommendationProfile, 0)
				}
				if lastRebase.IsZero() || now.Sub(lastRebase) >= time.Duration(settings.StaleScanIntervalSeconds)*time.Second {
					if err := enqueuePeriodicRecommendationProfileRebuilds(now, settings); err != nil {
						PipelineFailure(PipelineRecommendationProfile, "rebuild_enqueue_failed", 0)
						log.Printf("[RecommendationProfile] enqueue periodic rebase: %v", err)
					}
					lastRebase = now
				}
			}
			timer := time.NewTimer(time.Duration(settings.PollIntervalSeconds) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

func recommendationProfileMaterializerSettings() config.RecommendationProfileMaterializationConfig {
	if config.AppConfig == nil {
		return (config.RecommendationProfileMaterializationConfig{}).Normalized()
	}
	return config.AppConfig.Recommendation.ProfileMaterialization.Normalized()
}

func recommendationProfileMaterializerRecommendationConfig() config.RecommendationConfig {
	if config.AppConfig == nil {
		return config.RecommendationConfig{
			BehaviorWeights:    config.RecommendationBehaviorWeights{View: 0.5, Like: 6, Click: 1.5, QualifiedRead: 3, Reply: 5, QuickBounce: -3, NotInterested: -6},
			SignalHalfLifeDays: 14, FeedbackLookbackDays: 90, PositiveSignalCoexistBonus: 1, PositivePostWeightCap: 7,
			NegativeConfidenceSaturationScale: 12, AuthorAffinitySaturationScale: 6,
		}
	}
	cfg := config.AppConfig.Recommendation
	defaults := recommendationProfileMaterializerRecommendationConfigWithoutApp()
	if cfg.BehaviorWeights.View == 0 && !config.AppConfig.HasRecommendationSetting("behavior_weights.view") {
		cfg.BehaviorWeights.View = defaults.BehaviorWeights.View
	}
	if cfg.BehaviorWeights.Like == 0 && !config.AppConfig.HasRecommendationSetting("behavior_weights.like") {
		cfg.BehaviorWeights.Like = defaults.BehaviorWeights.Like
	}
	if cfg.BehaviorWeights.Click == 0 && !config.AppConfig.HasRecommendationSetting("behavior_weights.click") {
		cfg.BehaviorWeights.Click = defaults.BehaviorWeights.Click
	}
	if cfg.BehaviorWeights.QualifiedRead == 0 && !config.AppConfig.HasRecommendationSetting("behavior_weights.qualified_read") {
		cfg.BehaviorWeights.QualifiedRead = defaults.BehaviorWeights.QualifiedRead
	}
	if (cfg.BehaviorWeights.Reply == 0 && !config.AppConfig.HasRecommendationSetting("behavior_weights.reply")) || cfg.BehaviorWeights.Reply < 0 {
		cfg.BehaviorWeights.Reply = defaults.BehaviorWeights.Reply
	}
	if cfg.BehaviorWeights.QuickBounce == 0 && !config.AppConfig.HasRecommendationSetting("behavior_weights.quick_bounce") {
		cfg.BehaviorWeights.QuickBounce = defaults.BehaviorWeights.QuickBounce
	}
	if cfg.BehaviorWeights.NotInterested == 0 && !config.AppConfig.HasRecommendationSetting("behavior_weights.not_interested") {
		cfg.BehaviorWeights.NotInterested = defaults.BehaviorWeights.NotInterested
	}
	if cfg.SignalHalfLifeDays <= 0 {
		cfg.SignalHalfLifeDays = defaults.SignalHalfLifeDays
	}
	if cfg.FeedbackLookbackDays <= 0 {
		cfg.FeedbackLookbackDays = defaults.FeedbackLookbackDays
	}
	if cfg.PositiveSignalCoexistBonus < 0 || (cfg.PositiveSignalCoexistBonus == 0 && !config.AppConfig.HasRecommendationSetting("positive_signal_coexist_bonus")) {
		cfg.PositiveSignalCoexistBonus = defaults.PositiveSignalCoexistBonus
	}
	if cfg.PositivePostWeightCap <= 0 || cfg.PositivePostWeightCap < math.Max(cfg.BehaviorWeights.Like, cfg.BehaviorWeights.Reply) {
		cfg.PositivePostWeightCap = defaults.PositivePostWeightCap
	}
	if cfg.NegativeConfidenceSaturationScale <= 0 {
		cfg.NegativeConfidenceSaturationScale = defaults.NegativeConfidenceSaturationScale
	}
	if cfg.AuthorAffinitySaturationScale <= 0 {
		cfg.AuthorAffinitySaturationScale = defaults.AuthorAffinitySaturationScale
	}
	return cfg
}

func recommendationProfileMaterializerRecommendationConfigWithoutApp() config.RecommendationConfig {
	return config.RecommendationConfig{
		BehaviorWeights:    config.RecommendationBehaviorWeights{View: 0.5, Like: 6, Click: 1.5, QualifiedRead: 3, Reply: 5, QuickBounce: -3, NotInterested: -6},
		SignalHalfLifeDays: 14, FeedbackLookbackDays: 90, PositiveSignalCoexistBonus: 1, PositivePostWeightCap: 7,
		NegativeConfidenceSaturationScale: 12, AuthorAffinitySaturationScale: 6,
	}
}

func materializeDueRecommendationProfiles(now time.Time, settings config.RecommendationProfileMaterializationConfig) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	settings = settings.Normalized()
	cutoff := now.Add(-time.Duration(settings.DebounceSeconds) * time.Second)
	var dirty []models.UserRecoProfileDirty
	if err := global.Db.Where("dirty_at <= ? AND next_attempt_at <= ?", cutoff, now).
		Order("dirty_at ASC, user_id ASC").Limit(settings.BatchSize).Find(&dirty).Error; err != nil {
		return err
	}
	for _, row := range dirty {
		if err := materializeRecommendationProfileUser(row.UserID, now, settings, cutoff); err != nil {
			if errors.Is(err, errRecommendationProfileLockSkipped) {
				continue
			}
			log.Printf("[RecommendationProfile] user=%d materialization: %v", row.UserID, err)
		}
	}
	return nil
}

func recommendationProfileLockKey(userID uint) int64 {
	return recommendationProfileAdvisoryLockNamespace + int64(userID)
}

func materializeRecommendationProfileUser(userID uint, now time.Time, settings config.RecommendationProfileMaterializationConfig, cutoff time.Time) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	started := time.Now()
	settings = settings.Normalized()
	if cutoff.IsZero() {
		cutoff = now.Add(-time.Duration(settings.DebounceSeconds) * time.Second)
	}
	var claim *recommendationProfileMaterializerClaim
	lockSkipped := false
	err := global.Db.Transaction(func(tx *gorm.DB) error {
		var acquired bool
		if err := tx.Raw("SELECT pg_try_advisory_xact_lock(?)", recommendationProfileLockKey(userID)).Scan(&acquired).Error; err != nil {
			return err
		}
		if !acquired {
			lockSkipped = true
			return errRecommendationProfileLockSkipped
		}
		var dirty models.UserRecoProfileDirty
		if err := tx.Where("user_id = ?", userID).First(&dirty).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if dirty.DirtyAt.After(cutoff) || dirty.NextAttemptAt.After(now) {
			return nil
		}
		claim = &recommendationProfileMaterializerClaim{UserID: userID, DirtyVersion: dirty.DirtyVersion, Attempts: dirty.Attempts}

		cfg := recommendationProfileMaterializerRecommendationConfig()
		if cfg.FeedbackLookbackDays <= 0 {
			cfg.FeedbackLookbackDays = 90
		}
		sources, err := recommendation.LoadSourceSignals(tx, userID, now.AddDate(0, 0, -cfg.FeedbackLookbackDays))
		if err != nil {
			return err
		}
		canonical := recommendation.CanonicalizeOutcomes(sources.Behaviors, sources.Feedback, sources.Reactions)
		embeddingVersion := config.ActiveEmbeddingVersion()
		embeddings, err := loadMaterializerEmbeddings(tx, canonical.Outcomes, embeddingVersion)
		if err != nil {
			return err
		}
		built, err := recommendation.BuildInterestProfile(canonical, now, cfg, embeddingVersion, func(ids []uint, version string) (map[uint][]float32, error) {
			return embeddings, nil
		})
		if err != nil {
			return err
		}
		affinity, err := loadMaterializerAuthorAffinity(tx, built.PositiveAffinityContributions)
		if err != nil {
			return err
		}
		if err := replaceMaterializedCanonicalState(tx, userID, canonical, now); err != nil {
			return err
		}
		if err := upsertMaterializedProfile(tx, userID, built, now, settings, cfg, embeddingVersion); err != nil {
			return err
		}
		if err := replaceMaterializedAuthorAffinity(tx, userID, affinity, now); err != nil {
			return err
		}
		_, err = deleteMaterializedProfileClaim(tx, userID, dirty.DirtyVersion)
		return err
	})
	metrics.ObserveRecommendationProfileMaterializationDuration(time.Since(started))
	if lockSkipped || errors.Is(err, errRecommendationProfileLockSkipped) {
		metrics.RecordRecommendationProfileMaterialization("lock_skipped")
		return errRecommendationProfileLockSkipped
	}
	if err == nil {
		metrics.RecordRecommendationProfileMaterialization("success")
		return nil
	}
	metrics.RecordRecommendationProfileMaterialization("error")
	if claim != nil {
		if retryErr := retryMaterializedProfileClaim(*claim, err, now); retryErr != nil {
			log.Printf("[RecommendationProfile] user=%d retry update: %v", userID, retryErr)
		}
	}
	return err
}

func deleteMaterializedProfileClaim(tx *gorm.DB, userID uint, dirtyVersion int64) (int64, error) {
	if tx == nil {
		return 0, errors.New("database is not initialized")
	}
	result := tx.Where("user_id = ? AND dirty_version = ?", userID, dirtyVersion).Delete(&models.UserRecoProfileDirty{})
	return result.RowsAffected, result.Error
}

func loadMaterializerEmbeddings(tx *gorm.DB, outcomes []recommendation.UserPostOutcome, version string) (map[uint][]float32, error) {
	ids := make([]uint, 0, len(outcomes))
	seen := make(map[uint]struct{}, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.PostID != 0 {
			if _, exists := seen[outcome.PostID]; !exists {
				seen[outcome.PostID] = struct{}{}
				ids = append(ids, outcome.PostID)
			}
		}
	}
	result := make(map[uint][]float32, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var rows []models.PostEmbedding
	if err := tx.Select("post_id, embedding").Where("post_id IN ? AND version = ?", ids, version).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.PostID] = append([]float32(nil), row.Embedding.Slice()...)
	}
	return result, nil
}

type materializedAuthorAffinity struct {
	AuthorID    uint
	RawAffinity float64
}

func loadMaterializerAuthorAffinity(tx *gorm.DB, contributions map[uint]float64) ([]materializedAuthorAffinity, error) {
	postIDs := make([]uint, 0, len(contributions))
	for postID := range contributions {
		if postID != 0 {
			postIDs = append(postIDs, postID)
		}
	}
	sort.Slice(postIDs, func(i, j int) bool { return postIDs[i] < postIDs[j] })
	if len(postIDs) == 0 {
		return nil, nil
	}
	type postAuthorRow struct {
		PostID   uint
		AuthorID uint
	}
	var rows []postAuthorRow
	if err := tx.Table("posts").Select("id AS post_id, author_id").Where("id IN ?", postIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	raw := make(map[uint]float64)
	for _, row := range rows {
		if row.AuthorID != 0 {
			raw[row.AuthorID] += contributions[row.PostID]
		}
	}
	result := make([]materializedAuthorAffinity, 0, len(raw))
	for authorID, value := range raw {
		if value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
			result = append(result, materializedAuthorAffinity{AuthorID: authorID, RawAffinity: value})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].AuthorID < result[j].AuthorID })
	return result, nil
}

func replaceMaterializedCanonicalState(tx *gorm.DB, userID uint, canonical recommendation.CanonicalizationResult, rebuiltAt time.Time) error {
	if err := tx.Where("user_id = ?", userID).Delete(&models.UserPostRecoState{}).Error; err != nil {
		return err
	}
	if len(canonical.InteractedPostIDs) == 0 {
		return nil
	}
	outcomes := make(map[uint]recommendation.UserPostOutcome, len(canonical.Outcomes))
	for _, outcome := range canonical.Outcomes {
		outcomes[outcome.PostID] = outcome
	}
	rows := make([]models.UserPostRecoState, 0, len(canonical.InteractedPostIDs))
	for _, postID := range canonical.InteractedPostIDs {
		row := models.UserPostRecoState{
			UserID: userID, PostID: postID, Interacted: true,
			CanonicalVersion: recommendation.CanonicalOutcomeVersion, RebuiltAt: rebuiltAt,
		}
		if outcome, ok := outcomes[postID]; ok {
			for _, signal := range outcome.PositiveSignals {
				switch signal.SignalType {
				case "like":
					value := signal.OccurredAt
					row.LikeAt = &value
				case "reply":
					value := signal.OccurredAt
					row.ReplyAt = &value
				}
			}
			if outcome.PassiveSignal != nil {
				row.PassiveSignal = outcome.PassiveSignal.SignalType
				value := outcome.PassiveSignal.OccurredAt
				row.PassiveSignalAt = &value
			}
			if outcome.NegativeSignal != nil {
				row.NegativeSignal = outcome.NegativeSignal.SignalType
				value := outcome.NegativeSignal.OccurredAt
				row.NegativeSignalAt = &value
			}
		}
		rows = append(rows, row)
	}
	return tx.CreateInBatches(&rows, 200).Error
}

func upsertMaterializedProfile(tx *gorm.DB, userID uint, built recommendation.InterestProfile, now time.Time, settings config.RecommendationProfileMaterializationConfig, cfg config.RecommendationConfig, embeddingVersion string) error {
	dimensions := 0
	if len(built.PositiveVector) > 0 {
		dimensions = len(built.PositiveVector)
	}
	if len(built.NegativeVector) > 0 {
		if dimensions == 0 {
			dimensions = len(built.NegativeVector)
		} else if dimensions != len(built.NegativeVector) {
			return errors.New("positive and negative profile vectors have different dimensions")
		}
	}
	var positiveVector, negativeVector *pgvector.Vector
	if len(built.PositiveVector) > 0 {
		vector := pgvector.NewVector(built.PositiveVector)
		positiveVector = &vector
	}
	if len(built.NegativeVector) > 0 {
		vector := pgvector.NewVector(built.NegativeVector)
		negativeVector = &vector
	}
	profile := models.UserRecoProfile{
		UserID: userID, ProfileVersion: recommendation.MaterializedProfileVersion,
		ProfileConfigHash: recommendation.ProfileConfigHash(cfg, embeddingVersion), EmbeddingVersion: embeddingVersion,
		Dimensions: dimensions, PositiveVector: positiveVector, NegativeVector: negativeVector,
		NegativeEvidence: built.NegativeEvidence, PositiveSignalCount: built.PositiveSignalCount,
		NegativeSignalCount: built.NegativeSignalCount, PersonalizedSignalCount: built.PersonalizedSignalCount,
		ComputedAt: now, NextRebuildAt: now.Add(time.Duration(settings.RebuildIntervalHours) * time.Hour), UpdatedAt: now,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"profile_version":           profile.ProfileVersion,
			"profile_config_hash":       profile.ProfileConfigHash,
			"embedding_version":         profile.EmbeddingVersion,
			"dimensions":                profile.Dimensions,
			"positive_vector":           profile.PositiveVector,
			"negative_vector":           profile.NegativeVector,
			"negative_evidence":         profile.NegativeEvidence,
			"positive_signal_count":     profile.PositiveSignalCount,
			"negative_signal_count":     profile.NegativeSignalCount,
			"personalized_signal_count": profile.PersonalizedSignalCount,
			"computed_at":               profile.ComputedAt,
			"next_rebuild_at":           profile.NextRebuildAt,
			"updated_at":                profile.UpdatedAt,
		}),
	}).Create(&profile).Error
}

func replaceMaterializedAuthorAffinity(tx *gorm.DB, userID uint, affinities []materializedAuthorAffinity, rebuiltAt time.Time) error {
	if err := tx.Where("user_id = ?", userID).Delete(&models.UserAuthorAffinity{}).Error; err != nil {
		return err
	}
	if len(affinities) == 0 {
		return nil
	}
	rows := make([]models.UserAuthorAffinity, 0, len(affinities))
	for _, affinity := range affinities {
		rows = append(rows, models.UserAuthorAffinity{UserID: userID, AuthorID: affinity.AuthorID, RawAffinity: affinity.RawAffinity, RebuiltAt: rebuiltAt})
	}
	return tx.CreateInBatches(&rows, 200).Error
}

func retryMaterializedProfileClaim(claim recommendationProfileMaterializerClaim, materializationErr error, now time.Time) error {
	attempt := claim.Attempts + 1
	backoffSeconds := 2.0 * math.Pow(2, float64(attempt-1))
	if backoffSeconds > 300 {
		backoffSeconds = 300
	}
	lastError := strings.TrimSpace(materializationErr.Error())
	if len(lastError) > 512 {
		lastError = lastError[:512]
	}
	result := global.Db.Model(&models.UserRecoProfileDirty{}).
		Where("user_id = ? AND dirty_version = ?", claim.UserID, claim.DirtyVersion).
		Updates(map[string]interface{}{
			"attempts": attempt, "next_attempt_at": now.Add(time.Duration(backoffSeconds * float64(time.Second))),
			"last_error": lastError, "updated_at": now,
		})
	return result.Error
}

func enqueuePeriodicRecommendationProfileRebuilds(now time.Time, settings config.RecommendationProfileMaterializationConfig) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	settings = settings.Normalized()
	var userIDs []uint
	if err := global.Db.Model(&models.UserRecoProfile{}).
		Where("next_rebuild_at <= ?", now).
		Order("next_rebuild_at ASC, user_id ASC").Limit(settings.StaleEnqueueBatchSize).
		Pluck("user_id", &userIDs).Error; err != nil {
		return err
	}
	return recommendation.EnsureProfilesQueued(global.Db, userIDs, "periodic_rebase", now)
}
