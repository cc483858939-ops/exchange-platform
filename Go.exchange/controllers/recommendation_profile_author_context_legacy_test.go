package controllers

import (
	"context"
	"errors"

	"Go.exchange/config"
	"Go.exchange/recommendation"
)

// loadMaterializedCandidateAuthorContext scopes both profile affinity and
// following lookups to authors represented by the current candidate batch.
// loadedAuthors is retained across the fresh and soft passes so the second
// pass only queries newly encountered authors.
func loadMaterializedCandidateAuthorContext(ctx context.Context, repository recommendation.ProfileRepository, userID uint, profile *userInterestProfile, candidates []recommendation.RankedCandidate, loadedAuthors map[uint]struct{}, cfg config.RecommendationConfig) error {
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
	if len(authorIDs) == 0 {
		return nil
	}
	if repository == nil {
		return errors.New("recommendation profile repository is nil")
	}
	contextResult, err := repository.LoadAuthorContext(ctx, recommendation.AuthorContextQuery{
		UserID: userID, AuthorIDs: authorIDs, LoadAffinity: profile.MaterializedInteractionsReady,
		AffinitySaturationScale: cfg.AuthorAffinitySaturationScale,
	})
	if err != nil {
		return err
	}
	for authorID, affinity := range contextResult.Affinity {
		profile.AuthorAffinity[authorID] = affinity
	}
	for authorID := range contextResult.FollowingAuthorIDs {
		profile.FollowingAuthorIDs[authorID] = struct{}{}
	}
	return nil
}
