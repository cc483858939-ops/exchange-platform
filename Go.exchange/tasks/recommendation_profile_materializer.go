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
	"Go.exchange/embeddingstate"
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
			if global.WorkerDb != nil {
				if err := materializeDueRecommendationProfiles(ctx, now, settings); err != nil {
					PipelineFailure(PipelineRecommendationProfile, "materialization_failed", 0)
					log.Printf("[RecommendationProfile] materialize due profiles: %v", err)
				} else {
					PipelineSuccess(PipelineRecommendationProfile, 0)
				}
				if lastRebase.IsZero() || now.Sub(lastRebase) >= time.Duration(settings.StaleScanIntervalSeconds)*time.Second {
					if err := enqueuePeriodicRecommendationProfileRebuilds(ctx, now, settings); err != nil {
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
			BehaviorWeights:    config.RecommendationBehaviorWeights{View: 0.25, Like: 4, Click: 1, QualifiedRead: 2.5, Reply: 5, QuickBounce: -2, NotInterested: -8},
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
		BehaviorWeights:    config.RecommendationBehaviorWeights{View: 0.25, Like: 4, Click: 1, QualifiedRead: 2.5, Reply: 5, QuickBounce: -2, NotInterested: -8},
		SignalHalfLifeDays: 14, FeedbackLookbackDays: 90, PositiveSignalCoexistBonus: 1, PositivePostWeightCap: 7,
		NegativeConfidenceSaturationScale: 12, AuthorAffinitySaturationScale: 6,
	}
}

func materializeDueRecommendationProfiles(ctx context.Context, now time.Time, settings config.RecommendationProfileMaterializationConfig) error {
	if ctx == nil {
		return errors.New("materializer context is nil")
	}
	if global.WorkerDb == nil {
		return errors.New("database is not initialized")
	}
	settings = settings.Normalized()
	cutoff := now.Add(-time.Duration(settings.DebounceSeconds) * time.Second)
	dirtyRepository, err := recommendation.NewGormDirtyProfileRepository(global.WorkerDb)
	if err != nil {
		return err
	}
	dirty, err := dirtyRepository.ListDue(ctx, cutoff, now, settings.BatchSize)
	if err != nil {
		return err
	}
	for _, row := range dirty {
		if err := materializeRecommendationProfileUser(ctx, row.UserID, now, settings, cutoff); err != nil {
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

func materializeRecommendationProfileUser(ctx context.Context, userID uint, now time.Time, settings config.RecommendationProfileMaterializationConfig, cutoff time.Time) error {
	if ctx == nil {
		return errors.New("materializer context is nil")
	}
	if global.WorkerDb == nil {
		return errors.New("database is not initialized")
	}
	started := time.Now()
	settings = settings.Normalized()
	if cutoff.IsZero() {
		cutoff = now.Add(-time.Duration(settings.DebounceSeconds) * time.Second)
	}
	var claim *recommendationProfileMaterializerClaim
	lockSkipped := false
	err := global.WorkerDb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var acquired bool
		if err := tx.Raw("SELECT pg_try_advisory_xact_lock(?)", recommendationProfileLockKey(userID)).Scan(&acquired).Error; err != nil {
			return err
		}
		if !acquired {
			lockSkipped = true
			return errRecommendationProfileLockSkipped
		}
		dirtyRepository, err := recommendation.NewGormDirtyProfileRepository(tx)
		if err != nil {
			return err
		}
		dirty, err := dirtyRepository.Load(ctx, userID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if dirty.DirtyAt.After(cutoff) || dirty.NextAttemptAt.After(now) {
			return nil
		}
		claim = &recommendationProfileMaterializerClaim{UserID: userID, DirtyVersion: dirty.DirtyVersion, Attempts: dirty.Attempts}

		servingVersion, err := embeddingstate.LoadServingVersion(ctx, tx)
		if err != nil {
			return err
		}
		cfg := recommendationProfileMaterializerRecommendationConfig()
		if cfg.FeedbackLookbackDays <= 0 {
			cfg.FeedbackLookbackDays = 90
		}
		sourceRepository, err := recommendation.NewGormSourceRepository(tx)
		if err != nil {
			return err
		}
		sources, err := sourceRepository.LoadSourceSignals(ctx, userID, now.AddDate(0, 0, -cfg.FeedbackLookbackDays))
		if err != nil {
			return err
		}
		canonical := recommendation.CanonicalizeOutcomes(sources.Behaviors, sources.Feedback, sources.Reactions)
		embeddingVersion := servingVersion
		candidateRepository, err := recommendation.NewGormCandidateRepository(tx)
		if err != nil {
			return err
		}
		embeddings, err := loadMaterializerEmbeddings(ctx, candidateRepository, canonical.Outcomes, embeddingVersion)
		if err != nil {
			return err
		}
		built, err := recommendation.BuildInterestProfile(canonical, now, cfg, embeddingVersion, func(ids []uint, version string) (map[uint][]float32, error) {
			return embeddings, nil
		})
		if err != nil {
			return err
		}
		profileRepository, err := recommendation.NewGormProfileRepository(tx)
		if err != nil {
			return err
		}
		affinity, languageAffinity, err := loadMaterializerAffinities(ctx, profileRepository, built.PositiveAffinityContributions)
		if err != nil {
			return err
		}
		if err := replaceMaterializedCanonicalState(tx, userID, canonical, now); err != nil {
			return err
		}
		if err := upsertMaterializedProfile(tx, userID, built, languageAffinity, now, settings, cfg, embeddingVersion); err != nil {
			return err
		}
		if err := replaceMaterializedAuthorAffinity(tx, userID, affinity, now); err != nil {
			return err
		}
		_, err = dirtyRepository.DeleteClaim(ctx, userID, dirty.DirtyVersion)
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
		if retryErr := retryMaterializedProfileClaim(ctx, *claim, err, now); retryErr != nil {
			log.Printf("[RecommendationProfile] user=%d retry update: %v", userID, retryErr)
		}
	}
	return err
}

func loadMaterializerEmbeddings(ctx context.Context, repository recommendation.CandidateRepository, outcomes []recommendation.UserPostOutcome, version string) (map[uint][]float32, error) {
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
	if len(ids) == 0 {
		return map[uint][]float32{}, nil
	}
	if repository == nil {
		return nil, errors.New("recommendation candidate repository is nil")
	}
	return repository.LoadPostEmbeddings(ctx, ids, version)
}

type materializedAuthorAffinity struct {
	AuthorID    uint
	RawAffinity float64
}

type materializedLanguageAffinity struct {
	LanguageZHWeight float64
	LanguageJAWeight float64
	LanguageENWeight float64
	LanguageEvidence float64
}

type materializerPostMetadata = recommendation.PostAffinityInput

func loadMaterializerAffinities(ctx context.Context, repository recommendation.ProfileRepository, contributions map[uint]float64) ([]materializedAuthorAffinity, materializedLanguageAffinity, error) {
	postIDs := make([]uint, 0, len(contributions))
	for postID := range contributions {
		if postID != 0 {
			postIDs = append(postIDs, postID)
		}
	}
	sort.Slice(postIDs, func(i, j int) bool { return postIDs[i] < postIDs[j] })
	if len(postIDs) == 0 {
		return nil, materializedLanguageAffinity{}, nil
	}
	if repository == nil {
		return nil, materializedLanguageAffinity{}, errors.New("recommendation profile repository is nil")
	}
	rows, err := repository.LoadPostAffinityInputs(ctx, postIDs)
	if err != nil {
		return nil, materializedLanguageAffinity{}, err
	}
	affinities, languageAffinity := aggregateMaterializerAffinities(contributions, rows)
	return affinities, languageAffinity, nil
}

func aggregateMaterializerAffinities(contributions map[uint]float64, rows []materializerPostMetadata) ([]materializedAuthorAffinity, materializedLanguageAffinity) {
	raw := make(map[uint]float64)
	languageAffinity := materializedLanguageAffinity{}
	for _, row := range rows {
		contribution := contributions[row.PostID]
		if contribution <= 0 || math.IsNaN(contribution) || math.IsInf(contribution, 0) {
			continue
		}
		if row.AuthorID != 0 {
			raw[row.AuthorID] += contribution
		}
		switch strings.ToLower(strings.TrimSpace(row.Language)) {
		case "zh":
			languageAffinity.LanguageZHWeight += contribution
		case "ja":
			languageAffinity.LanguageJAWeight += contribution
		case "en":
			languageAffinity.LanguageENWeight += contribution
		}
	}
	result := make([]materializedAuthorAffinity, 0, len(raw))
	for authorID, value := range raw {
		if value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
			result = append(result, materializedAuthorAffinity{AuthorID: authorID, RawAffinity: value})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].AuthorID < result[j].AuthorID })
	languageAffinity.LanguageEvidence = languageAffinity.LanguageZHWeight + languageAffinity.LanguageJAWeight + languageAffinity.LanguageENWeight
	if !validMaterializerLanguageWeight(languageAffinity.LanguageZHWeight) ||
		!validMaterializerLanguageWeight(languageAffinity.LanguageJAWeight) ||
		!validMaterializerLanguageWeight(languageAffinity.LanguageENWeight) ||
		!validMaterializerLanguageWeight(languageAffinity.LanguageEvidence) {
		languageAffinity = materializedLanguageAffinity{}
	}
	return result, languageAffinity
}

func validMaterializerLanguageWeight(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
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

func upsertMaterializedProfile(tx *gorm.DB, userID uint, built recommendation.InterestProfile, languageAffinity materializedLanguageAffinity, now time.Time, settings config.RecommendationProfileMaterializationConfig, cfg config.RecommendationConfig, embeddingVersion string) error {
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
		LanguageZHWeight: languageAffinity.LanguageZHWeight, LanguageJAWeight: languageAffinity.LanguageJAWeight,
		LanguageENWeight: languageAffinity.LanguageENWeight, LanguageEvidence: languageAffinity.LanguageEvidence,
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
			"language_zh_weight":        profile.LanguageZHWeight,
			"language_ja_weight":        profile.LanguageJAWeight,
			"language_en_weight":        profile.LanguageENWeight,
			"language_evidence":         profile.LanguageEvidence,
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

func retryMaterializedProfileClaim(ctx context.Context, claim recommendationProfileMaterializerClaim, materializationErr error, now time.Time) error {
	if ctx == nil {
		return errors.New("materializer retry context is nil")
	}
	if global.WorkerDb == nil {
		return errors.New("database is not initialized")
	}
	repository, err := recommendation.NewGormDirtyProfileRepository(global.WorkerDb)
	if err != nil {
		return err
	}
	return repository.RetryClaim(ctx, recommendation.DirtyProfile{
		UserID: claim.UserID, DirtyVersion: claim.DirtyVersion, Attempts: claim.Attempts,
	}, materializationErr, now)
}

func enqueuePeriodicRecommendationProfileRebuilds(ctx context.Context, now time.Time, settings config.RecommendationProfileMaterializationConfig) error {
	if ctx == nil {
		return errors.New("materializer enqueue context is nil")
	}
	if global.WorkerDb == nil {
		return errors.New("database is not initialized")
	}
	settings = settings.Normalized()
	var userIDs []uint
	db := global.WorkerDb.WithContext(ctx)
	if err := db.Model(&models.UserRecoProfile{}).
		Where("next_rebuild_at <= ?", now).
		Order("next_rebuild_at ASC, user_id ASC").Limit(settings.StaleEnqueueBatchSize).
		Pluck("user_id", &userIDs).Error; err != nil {
		return err
	}
	repository, err := recommendation.NewGormDirtyProfileRepository(db)
	if err != nil {
		return err
	}
	return repository.EnsureProfilesQueued(ctx, userIDs, "periodic_rebase", now)
}
