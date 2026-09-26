package recommendation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

type GormProfileRepository struct {
	db *gorm.DB
}

func NewGormProfileRepository(db *gorm.DB) (*GormProfileRepository, error) {
	if db == nil {
		return nil, errors.New("recommendation profile repository database is nil")
	}
	return &GormProfileRepository{db: db}, nil
}

func (r *GormProfileRepository) Load(ctx context.Context, query ProfileLoadQuery) (ProfileLoadResult, error) {
	profile := Profile{
		AuthorAffinity:     make(map[uint]float64),
		FollowingAuthorIDs: make(map[uint]struct{}),
		ProfileStatus:      ProfileStatusMiss,
	}
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return ProfileLoadResult{}, fmt.Errorf("load materialized profile: %w", err)
	}
	var row models.UserRecoProfile
	if err := db.Where("user_id = ?", query.UserID).First(&row).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return ProfileLoadResult{}, fmt.Errorf("load materialized profile: %w", err)
		}
		return r.withRecovery(ctx, profile, query.UserID, "serving_miss", query.Now), nil
	}
	if row.ProfileVersion != query.ExpectedProfileVersion ||
		row.ProfileConfigHash != query.ExpectedProfileConfigHash ||
		row.EmbeddingVersion != query.EmbeddingVersion {
		profile.ProfileStatus = ProfileStatusIncompatible
		return r.withRecovery(ctx, profile, query.UserID, "profile_incompatible", query.Now), nil
	}
	profile.ProfileStatus = ProfileStatusHit
	if !row.NextRebuildAt.After(query.Now) {
		profile.ProfileStatus = ProfileStatusStale
	}
	profile.ProfileVersion = row.ProfileVersion
	profile.ProfileConfigHash = row.ProfileConfigHash
	profile.MaterializedInteractionsReady = true
	profile.PositiveSignalCount = row.PositiveSignalCount
	profile.NegativeSignalCount = row.NegativeSignalCount
	profile.PersonalizedSignalCount = row.PersonalizedSignalCount
	profile.LanguageZHWeight = row.LanguageZHWeight
	profile.LanguageJAWeight = row.LanguageJAWeight
	profile.LanguageENWeight = row.LanguageENWeight
	profile.LanguageEvidence = row.LanguageEvidence
	if row.PositiveVector != nil {
		profile.PositiveVector = append([]float32(nil), row.PositiveVector.Slice()...)
	}
	if row.NegativeVector != nil {
		profile.NegativeVector = append([]float32(nil), row.NegativeVector.Slice()...)
	}
	profile.NegativeConfidence = MaterializedNegativeConfidence(
		row.NegativeEvidence,
		row.ComputedAt,
		query.Now,
		query.NegativeConfidenceHalfLifeDays,
		query.NegativeConfidenceSaturation,
		len(profile.NegativeVector) > 0,
	)
	age := query.Now.Sub(row.ComputedAt)
	if age < 0 {
		age = 0
	}
	profile.ProfileAgeMS = age.Milliseconds()
	result := ProfileLoadResult{Profile: profile}
	if profile.ProfileStatus == ProfileStatusStale {
		result = r.withRecovery(ctx, profile, query.UserID, "serving_stale", query.Now)
	}
	return result, nil
}

func (r *GormProfileRepository) withRecovery(ctx context.Context, profile Profile, userID uint, reason string, now time.Time) ProfileLoadResult {
	result := ProfileLoadResult{Profile: profile, RecoveryReason: reason}
	dirty, err := NewGormDirtyProfileRepository(r.db)
	if err == nil {
		result.RecoveryError = dirty.EnsureProfilesQueued(ctx, []uint{userID}, reason, now)
	} else {
		result.RecoveryError = err
	}
	return result
}

func (r *GormProfileRepository) LoadAuthorContext(ctx context.Context, query AuthorContextQuery) (AuthorContext, error) {
	result := AuthorContext{Affinity: make(map[uint]float64), FollowingAuthorIDs: make(map[uint]struct{})}
	if len(query.AuthorIDs) == 0 {
		return result, nil
	}
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return AuthorContext{}, fmt.Errorf("load recommendation author context: %w", err)
	}
	ids := uniqueNonZeroIDs(query.AuthorIDs)
	if len(ids) == 0 {
		return result, nil
	}
	if query.LoadAffinity {
		var affinities []models.UserAuthorAffinity
		if err := db.Where("user_id = ? AND author_id IN ?", query.UserID, ids).Find(&affinities).Error; err != nil {
			return AuthorContext{}, fmt.Errorf("load author affinities: %w", err)
		}
		scale := query.AffinitySaturationScale
		if scale <= 0 {
			scale = 6
		}
		for _, affinity := range affinities {
			if affinity.RawAffinity > 0 {
				result.Affinity[affinity.AuthorID] = math.Max(0, math.Min(1, math.Tanh(affinity.RawAffinity/scale)))
			}
		}
	}
	var follows []uint
	if err := db.Table("user_follows").Where("follower_id = ? AND following_id IN ?", query.UserID, ids).Pluck("following_id", &follows).Error; err != nil {
		return AuthorContext{}, fmt.Errorf("load followed authors: %w", err)
	}
	for _, authorID := range follows {
		if authorID != 0 {
			result.FollowingAuthorIDs[authorID] = struct{}{}
		}
	}
	return result, nil
}

func (r *GormProfileRepository) LoadPostAffinityInputs(ctx context.Context, postIDs []uint) ([]PostAffinityInput, error) {
	ids := uniqueNonZeroIDs(postIDs)
	if len(ids) == 0 {
		return []PostAffinityInput{}, nil
	}
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("load profile post affinity inputs: %w", err)
	}
	var rows []PostAffinityInput
	if err := db.Table("posts").Select("id AS post_id, author_id, language").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load profile post affinity inputs: %w", err)
	}
	return rows, nil
}

func uniqueNonZeroIDs(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	result := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func MaterializedNegativeConfidence(negativeEvidence float64, computedAt, now time.Time, signalHalfLifeDays, saturationScale float64, hasNegativeVector bool) float64 {
	if !hasNegativeVector || negativeEvidence <= 0 ||
		math.IsNaN(negativeEvidence) || math.IsInf(negativeEvidence, 0) ||
		computedAt.IsZero() || now.IsZero() ||
		signalHalfLifeDays <= 0 || math.IsNaN(signalHalfLifeDays) || math.IsInf(signalHalfLifeDays, 0) ||
		saturationScale <= 0 || math.IsNaN(saturationScale) || math.IsInf(saturationScale, 0) {
		return 0
	}
	elapsedDays := 0.0
	if computedAt.Before(now) {
		elapsedDays = now.Sub(computedAt).Hours() / 24
		if elapsedDays < 0 || math.IsNaN(elapsedDays) || math.IsInf(elapsedDays, 0) {
			return 0
		}
	}
	currentEvidence := negativeEvidence * math.Exp(-math.Ln2*elapsedDays/signalHalfLifeDays)
	if currentEvidence <= 0 || math.IsNaN(currentEvidence) || math.IsInf(currentEvidence, 0) {
		return 0
	}
	confidence := math.Tanh(currentEvidence / saturationScale)
	if math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < 0 {
		return 0
	}
	if confidence >= 1 {
		return math.Nextafter(1, 0)
	}
	return confidence
}

var _ ProfileRepository = (*GormProfileRepository)(nil)
