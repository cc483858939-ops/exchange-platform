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
	Profile         userInterestProfile
	FreshSet        recommendationCandidateSet
	RecallSets      []recommendationCandidateSet
	Selected        []selectedRecommendation
	LanguageContext recommendationLanguageContext
}

var (
	publicRecommendationCandidateSetForServing        = loadPublicRecommendationCandidateSet
	publicRecommendationHydrateForServing             = hydrateRecommendationCandidates
	diversifyPublicRecommendationCandidatesForServing = diversifyPublicRecommendationCandidates
)

// servePublicRecommendationCandidatePath is the guest-safe variant of the
// recommendation pipeline. It deliberately has no user identity, profile,
// Redis access, semantic recall, social graph lookup, or author affinity
// hydration. Guest served state is passed in by the controller.
func servePublicRecommendationCandidatePath(limit uint, cfg config.RecommendationConfig, now time.Time, requestID string, browser recommendationLanguageContext, served map[uint]servedPost) (recommendationServingOutcome, error) {
	outcome := recommendationServingOutcome{Profile: userInterestProfile{}}
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

	outcome.LanguageContext = buildRecommendationLanguageContext(browser, recommendationLanguagePrior{}, 0, cfg)
	freshExcluded := guestFreshExcludedPostIDs(served)
	publicSet, err := publicRecommendationCandidateSetForServing(now, cfg, freshExcluded)
	if err != nil {
		return outcome, err
	}
	hydrated, err := publicRecommendationHydrateForServing(publicSet.Candidates, now)
	if err != nil {
		return outcome, err
	}
	ranked := rankRecommendationCandidates(userInterestProfile{}, hydrated, now, cfg, outcome.LanguageContext)
	diversified := diversifyPublicRecommendationCandidatesForServing(ranked, int(limit), requestID)
	selected := selectRecommendationCandidates(diversified, nil, int(limit), cfg, now, recommendationSelectionFresh, requestID)
	mergedSet := publicSet
	if len(selected) < int(limit) {
		fallbackExcluded := guestFallbackExcludedPostIDs(served, selected)
		fallbackSet, fallbackErr := publicRecommendationCandidateSetForServing(now, cfg, fallbackExcluded)
		if fallbackErr != nil {
			return outcome, fallbackErr
		}
		outcome.RecallSets = []recommendationCandidateSet{publicSet, fallbackSet}
		fallbackHydrated, fallbackErr := publicRecommendationHydrateForServing(fallbackSet.Candidates, now)
		if fallbackErr != nil {
			return outcome, fallbackErr
		}
		annotateGuestFallbackServedState(fallbackHydrated, served)
		rankedFallback := rankRecommendationCandidates(userInterestProfile{}, fallbackHydrated, now, cfg, outcome.LanguageContext)
		selected = selectRecommendationCandidates(rankedFallback, selected, int(limit), cfg, now, recommendationSelectionFresh, requestID)
		if len(selected) < int(limit) {
			selected = selectRecommendationCandidates(rankedFallback, selected, int(limit), cfg, now, recommendationSelectionSoft, requestID)
		}
		mergedSet = mergeCandidateSets(publicSet, fallbackSet, cfg.Candidates.ColdStart.Merged)
	} else {
		outcome.RecallSets = []recommendationCandidateSet{publicSet}
	}
	outcome.FreshSet = mergedSet
	outcome.Selected = selected
	return outcome, nil
}

func guestFreshExcludedPostIDs(served map[uint]servedPost) map[uint]struct{} {
	excluded := make(map[uint]struct{}, len(served))
	for postID, item := range served {
		if postID != 0 && (item.Hard || item.Soft) {
			excluded[postID] = struct{}{}
		}
	}
	return excluded
}

func guestFallbackExcludedPostIDs(served map[uint]servedPost, selected []selectedRecommendation) map[uint]struct{} {
	excluded := make(map[uint]struct{}, len(served)+len(selected))
	for postID, item := range served {
		if postID != 0 && item.Hard {
			excluded[postID] = struct{}{}
		}
	}
	for _, item := range selected {
		if item.Post.ID != 0 {
			excluded[item.Post.ID] = struct{}{}
		}
	}
	return excluded
}

func annotateGuestFallbackServedState(candidates []hydratedRecommendationCandidate, served map[uint]servedPost) {
	for index := range candidates {
		item, ok := served[candidates[index].Post.ID]
		if !ok || item.Hard || !item.Soft {
			continue
		}
		candidates[index].Candidate.WasSoftServed = true
		candidates[index].Candidate.LastServedAt = item.LastServedAt
	}
}

// serveRecommendationCandidatePath is shared by GetPostRecommendations and
// DevData verification. Keeping the path here prevents verification from
// copying the recommender's SQL, ranking, or selection rules.
func serveRecommendationCandidatePath(userID, limit uint, cfg config.RecommendationConfig, now time.Time, requestID string, browser recommendationLanguageContext, served map[uint]servedPost) (recommendationServingOutcome, error) {
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
	languageContext := buildRecommendationLanguageContext(
		browser,
		recommendationLanguagePrior{ZH: profile.LanguageZHWeight, JA: profile.LanguageJAWeight, EN: profile.LanguageENWeight},
		profile.LanguageEvidence,
		cfg,
	)
	outcome.LanguageContext = languageContext
	loadedAuthors := make(map[uint]struct{})

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
	rankedFresh := rankRecommendationCandidates(profile, freshHydrated, now, cfg, languageContext)
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
		rankedSoft := rankRecommendationCandidates(profile, softHydrated, now, cfg, languageContext)
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
	outcome, err := serveRecommendationCandidatePath(userID, uint(limit), normalizedRecommendationConfig(), now, uuid.NewString(), recommendationLanguageContext{}, map[uint]servedPost{})
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
