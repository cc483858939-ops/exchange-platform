package controllers

import (
	"errors"
	"time"

	"Go.exchange/config"

	"github.com/google/uuid"
)

// RecommendationServingVerification is the observable result of the same
// candidate, hydration, ranking, and selection path used by the product
// recommendation handler. It intentionally does not persist serving traces.
type RecommendationServingVerification struct {
	RecentCandidateCount int
	RecentPostIDs        []uint
	FinalPostIDs         []uint
}

type recommendationServingOutcome struct {
	Profile                userInterestProfile
	FreshSet               recommendationCandidateSet
	RecallSets             []recommendationCandidateSet
	Selected               []selectedRecommendation
	ServedHistoryLoadError error
}

// serveRecommendationCandidatePath is shared by GetPostRecommendations and
// DevData verification. Keeping the path here prevents verification from
// copying the recommender's SQL, ranking, or selection rules.
func serveRecommendationCandidatePath(userID, limit uint, cfg config.RecommendationConfig, now time.Time, requestID string) (recommendationServingOutcome, error) {
	outcome := recommendationServingOutcome{}
	if userID == 0 {
		return outcome, errors.New("missing recommendation verification user")
	}
	if limit == 0 {
		limit = defaultRecommendationLimit
	}
	if limit > maxRecommendationLimit {
		limit = maxRecommendationLimit
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if requestID == "" {
		requestID = uuid.NewString()
	}

	profile, err := loadMaterializedUserInterestProfile(userID, now, cfg)
	outcome.Profile = profile
	if err != nil {
		return outcome, err
	}
	loadedAuthors := make(map[uint]struct{})

	served, err := loadRecommendationServedHistory(userID, now, cfg)
	if err != nil {
		outcome.ServedHistoryLoadError = err
		// The product handler treats a served-history read failure as a soft
		// fallback, so verification must observe the same behavior.
		served = map[uint]servedPost{}
	}
	freshSet, err := loadRecommendationCandidateSet(userID, profile, served, now, cfg, false)
	if err != nil {
		return outcome, err
	}
	outcome.RecallSets = append(outcome.RecallSets, freshSet)
	freshHydrated, err := hydrateRecommendationCandidates(freshSet.Candidates, now)
	if err != nil {
		return outcome, err
	}
	if err := loadMaterializedCandidateAuthorContext(userID, &profile, freshHydrated, loadedAuthors, cfg); err != nil {
		return outcome, err
	}
	rankedFresh := rankRecommendationCandidates(profile, freshHydrated, now, cfg)
	selected := selectRecommendationCandidates(rankedFresh, nil, int(limit), cfg, now, recommendationSelectionFresh, requestID)

	if len(selected) < int(limit) {
		softSet, softErr := loadRecommendationCandidateSet(userID, profile, served, now, cfg, true)
		if softErr != nil {
			return outcome, softErr
		}
		outcome.RecallSets = append(outcome.RecallSets, softSet)
		softHydrated, softErr := hydrateRecommendationCandidates(softSet.Candidates, now)
		if softErr != nil {
			return outcome, softErr
		}
		if err := loadMaterializedCandidateAuthorContext(userID, &profile, softHydrated, loadedAuthors, cfg); err != nil {
			return outcome, err
		}
		rankedSoft := rankRecommendationCandidates(profile, softHydrated, now, cfg)
		selected = selectRecommendationCandidates(rankedSoft, selected, int(limit), cfg, now, recommendationSelectionSoft, requestID)
		freshSet = mergeCandidateSets(freshSet, softSet, recommendationCandidateCaps(profile, cfg).Merged)
	}

	outcome.Profile = profile
	outcome.FreshSet = freshSet
	outcome.Selected = selected
	return outcome, nil
}

// VerifyRecommendationServing executes the product recommendation path for a
// caller-provided isolated user. The caller is responsible for ensuring the
// package global database points at the database being verified, matching the
// existing controller architecture.
func VerifyRecommendationServing(userID uint, limit int, now time.Time) (RecommendationServingVerification, error) {
	if limit <= 0 {
		limit = defaultRecommendationLimit
	}
	outcome, err := serveRecommendationCandidatePath(userID, uint(limit), normalizedRecommendationConfig(), now, uuid.NewString())
	if err != nil {
		return RecommendationServingVerification{}, err
	}
	finalIDs := make([]uint, 0, len(outcome.Selected))
	for _, item := range outcome.Selected {
		if item.Post.ID != 0 {
			finalIDs = append(finalIDs, item.Post.ID)
		}
	}
	return RecommendationServingVerification{
		RecentCandidateCount: outcome.FreshSet.RecentCount,
		RecentPostIDs:        append([]uint(nil), outcome.FreshSet.RecentPostIDs...),
		FinalPostIDs:         finalIDs,
	}, nil
}
