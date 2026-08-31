package controllers

import (
	"math"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/models"
)

// loadMaterializedCandidateAuthorContext scopes both profile affinity and
// following lookups to authors represented by the current candidate batch.
// loadedAuthors is retained across the fresh and soft passes so the second
// pass only queries newly encountered authors.
func loadMaterializedCandidateAuthorContext(userID uint, profile *userInterestProfile, candidates []hydratedRecommendationCandidate, loadedAuthors map[uint]struct{}, cfg config.RecommendationConfig) error {
	if profile.AuthorAffinity == nil {
		profile.AuthorAffinity = make(map[uint]float64)
	}
	if profile.FollowingAuthorIDs == nil {
		profile.FollowingAuthorIDs = make(map[uint]struct{})
	}
	if loadedAuthors == nil {
		loadedAuthors = make(map[uint]struct{})
	}
	authorIDs := make([]uint, 0, len(candidates))
	seen := make(map[uint]struct{}, len(candidates))
	for _, candidate := range candidates {
		authorID := candidate.Post.AuthorID
		if authorID == 0 {
			continue
		}
		if _, exists := loadedAuthors[authorID]; exists {
			continue
		}
		if _, exists := seen[authorID]; exists {
			continue
		}
		seen[authorID] = struct{}{}
		authorIDs = append(authorIDs, authorID)
	}
	for _, authorID := range authorIDs {
		loadedAuthors[authorID] = struct{}{}
	}
	if len(authorIDs) == 0 || global.Db == nil {
		return nil
	}

	if profile.MaterializedInteractionsReady {
		var affinities []models.UserAuthorAffinity
		if err := global.Db.Where("user_id = ? AND author_id IN ?", userID, authorIDs).Find(&affinities).Error; err != nil {
			return err
		}
		scale := cfg.AuthorAffinitySaturationScale
		if scale <= 0 {
			scale = 6
		}
		for _, affinity := range affinities {
			if affinity.RawAffinity > 0 {
				profile.AuthorAffinity[affinity.AuthorID] = math.Max(0, math.Min(1, math.Tanh(affinity.RawAffinity/scale)))
			}
		}
	}
	var follows []uint
	if err := global.Db.Table("user_follows").Where("follower_id = ? AND following_id IN ?", userID, authorIDs).Pluck("following_id", &follows).Error; err != nil {
		return err
	}
	for _, authorID := range follows {
		if authorID != 0 {
			profile.FollowingAuthorIDs[authorID] = struct{}{}
		}
	}
	return nil
}
