package controllers

import (
	"context"
	"errors"
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/recommendation"
	"github.com/google/uuid"
)

const (
	defaultRecommendationLimit = 20
	maxRecommendationLimit     = 50
)

type recommendationServingOutcome struct {
	Profile          userInterestProfile
	EmbeddingVersion string
	FreshSet         recommendationCandidateSet
	RecallSets       []recommendationCandidateSet
	Selected         []recommendation.SelectedCandidate
	LanguageContext  recommendation.LanguageContext
}

type recommendationServingSnapshot struct {
	EmbeddingVersion string
}

func (snapshot recommendationServingSnapshot) validate() error {
	if strings.TrimSpace(snapshot.EmbeddingVersion) == "" {
		return errors.New("recommendation serving version is blank")
	}
	return nil
}

// servePublicRecommendationCandidatePath is the guest-safe variant of the
// recommendation pipeline. It deliberately has no user identity, profile,
// Redis access, semantic recall, social graph lookup, or author affinity
// hydration. Guest served state is passed in by the controller.
func servePublicRecommendationCandidatePath(ctx context.Context, dependencies recommendation.DataDependencies, limit uint, cfg config.RecommendationConfig, now time.Time, requestID string, snapshot recommendationServingSnapshot, browser recommendation.LanguageContext, served recommendation.ServedHistory) (recommendationServingOutcome, error) {
	outcome := recommendationServingOutcome{Profile: userInterestProfile{}, EmbeddingVersion: snapshot.EmbeddingVersion}
	if err := snapshot.validate(); err != nil {
		return outcome, err
	}
	if ctx == nil {
		return outcome, errors.New("recommendation context is nil")
	}
	if dependencies.Candidates == nil {
		return outcome, errors.New("recommendation candidate repository is nil")
	}
	if err := ctx.Err(); err != nil {
		return outcome, err
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

	outcome.LanguageContext = recommendation.BuildLanguageContext(browser, recommendation.LanguagePrior{}, 0, recommendationLanguageConfig(cfg))
	freshExcluded := guestFreshExcludedPostIDs(served)
	publicSet, err := buildPublicRecommendationCandidateSet(ctx, dependencies.Candidates, now, cfg, freshExcluded)
	if err != nil {
		return outcome, err
	}
	if err := ctx.Err(); err != nil {
		return outcome, err
	}
	hydrated, err := dependencies.Candidates.HydrateCandidates(ctx, snapshot.EmbeddingVersion, publicSet.Candidates, now)
	if err != nil {
		return outcome, err
	}
	if err := ctx.Err(); err != nil {
		return outcome, err
	}
	ranked := recommendation.RankCandidates(recommendation.ProfileFeatures{}, hydrated, now, recommendationRankingConfig(cfg), outcome.LanguageContext)
	diversified := recommendation.DiversifyPublicCandidates(ranked, int(limit), requestID)
	selected := recommendation.SelectCandidates(recommendation.SelectionInput{
		Candidates: diversified, Limit: int(limit), Now: now, Phase: recommendation.SelectionPhaseFresh,
		RequestID: requestID, Config: recommendationSelectionConfig(cfg),
	})
	mergedSet := publicSet
	if len(selected) < int(limit) {
		if err := ctx.Err(); err != nil {
			return outcome, err
		}
		fallbackExcluded := guestFallbackExcludedPostIDs(served, selected)
		fallbackSet, fallbackErr := buildPublicRecommendationCandidateSet(ctx, dependencies.Candidates, now, cfg, fallbackExcluded)
		if fallbackErr != nil {
			return outcome, fallbackErr
		}
		outcome.RecallSets = []recommendationCandidateSet{publicSet, fallbackSet}
		fallbackHydrated, fallbackErr := dependencies.Candidates.HydrateCandidates(ctx, snapshot.EmbeddingVersion, fallbackSet.Candidates, now)
		if fallbackErr != nil {
			return outcome, fallbackErr
		}
		if err := ctx.Err(); err != nil {
			return outcome, err
		}
		annotateGuestFallbackServedState(fallbackHydrated, served)
		rankedFallback := recommendation.RankCandidates(recommendation.ProfileFeatures{}, fallbackHydrated, now, recommendationRankingConfig(cfg), outcome.LanguageContext)
		selected = recommendation.SelectCandidates(recommendation.SelectionInput{
			Candidates: rankedFallback, Initial: selected, Limit: int(limit), Now: now, Phase: recommendation.SelectionPhaseFresh,
			RequestID: requestID, Config: recommendationSelectionConfig(cfg),
		})
		if len(selected) < int(limit) {
			selected = recommendation.SelectCandidates(recommendation.SelectionInput{
				Candidates: rankedFallback, Initial: selected, Limit: int(limit), Now: now, Phase: recommendation.SelectionPhaseSoft,
				RequestID: requestID, Config: recommendationSelectionConfig(cfg),
			})
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

func guestFallbackExcludedPostIDs(served map[uint]servedPost, selected []recommendation.SelectedCandidate) map[uint]struct{} {
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

func annotateGuestFallbackServedState(candidates []recommendation.RankedCandidate, served map[uint]servedPost) {
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
func serveRecommendationCandidatePath(ctx context.Context, dependencies recommendation.DataDependencies, userID, limit uint, cfg config.RecommendationConfig, now time.Time, requestID string, snapshot recommendationServingSnapshot, browser recommendation.LanguageContext, served recommendation.ServedHistory) (recommendationServingOutcome, error) {
	outcome := recommendationServingOutcome{EmbeddingVersion: snapshot.EmbeddingVersion}
	if err := snapshot.validate(); err != nil {
		return outcome, err
	}
	if ctx == nil {
		return outcome, errors.New("recommendation context is nil")
	}
	if dependencies.Candidates == nil || dependencies.Profiles == nil {
		return outcome, errors.New("recommendation serving repositories are not initialized")
	}
	if err := ctx.Err(); err != nil {
		return outcome, err
	}
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

	profile, err := loadRecommendationProfile(ctx, dependencies.Profiles, userID, snapshot.EmbeddingVersion, now, cfg)
	outcome.Profile = profile
	if err != nil {
		return outcome, err
	}
	if err := ctx.Err(); err != nil {
		return outcome, err
	}
	languageContext := recommendation.BuildLanguageContext(
		browser,
		recommendation.LanguagePrior{ZH: profile.LanguageZHWeight, JA: profile.LanguageJAWeight, EN: profile.LanguageENWeight},
		profile.LanguageEvidence,
		recommendationLanguageConfig(cfg),
	)
	outcome.LanguageContext = languageContext
	loadedAuthors := make(map[uint]struct{})

	freshSet, err := buildRecommendationCandidateSet(ctx, dependencies.Candidates, snapshot.EmbeddingVersion, userID, profile, served, now, cfg, false)
	if err != nil {
		return outcome, err
	}
	outcome.RecallSets = append(outcome.RecallSets, freshSet)
	if err := ctx.Err(); err != nil {
		return outcome, err
	}
	freshHydrated, err := dependencies.Candidates.HydrateCandidates(ctx, snapshot.EmbeddingVersion, freshSet.Candidates, now)
	if err != nil {
		return outcome, err
	}
	if err := ctx.Err(); err != nil {
		return outcome, err
	}
	if err := loadMaterializedCandidateAuthorContext(ctx, dependencies.Profiles, userID, &profile, freshHydrated, loadedAuthors, cfg); err != nil {
		return outcome, err
	}
	rankedFresh := recommendation.RankCandidates(recommendationProfileFeatures(profile), freshHydrated, now, recommendationRankingConfig(cfg), languageContext)
	selected := recommendation.SelectCandidates(recommendation.SelectionInput{
		Candidates: rankedFresh, Limit: int(limit), Now: now, Phase: recommendation.SelectionPhaseFresh,
		RequestID: requestID, Config: recommendationSelectionConfig(cfg),
	})

	if len(selected) < int(limit) {
		if err := ctx.Err(); err != nil {
			return outcome, err
		}
		softSet, softErr := buildRecommendationCandidateSet(ctx, dependencies.Candidates, snapshot.EmbeddingVersion, userID, profile, served, now, cfg, true)
		if softErr != nil {
			return outcome, softErr
		}
		outcome.RecallSets = append(outcome.RecallSets, softSet)
		softHydrated, softErr := dependencies.Candidates.HydrateCandidates(ctx, snapshot.EmbeddingVersion, softSet.Candidates, now)
		if softErr != nil {
			return outcome, softErr
		}
		if err := ctx.Err(); err != nil {
			return outcome, err
		}
		if err := loadMaterializedCandidateAuthorContext(ctx, dependencies.Profiles, userID, &profile, softHydrated, loadedAuthors, cfg); err != nil {
			return outcome, err
		}
		rankedSoft := recommendation.RankCandidates(recommendationProfileFeatures(profile), softHydrated, now, recommendationRankingConfig(cfg), languageContext)
		selected = recommendation.SelectCandidates(recommendation.SelectionInput{
			Candidates: rankedSoft, Initial: selected, Limit: int(limit), Now: now, Phase: recommendation.SelectionPhaseSoft,
			RequestID: requestID, Config: recommendationSelectionConfig(cfg),
		})
		freshSet = mergeCandidateSets(freshSet, softSet, recommendationCandidateCaps(profile, cfg).Merged)
	}

	outcome.Profile = profile
	outcome.FreshSet = freshSet
	outcome.Selected = selected
	return outcome, nil
}
